package domain

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Reseller monthly targets (docs/03-bot-ux.md, "Reseller panel"): the owner sets tiers, a number of services sold in a month and a bonus in Toman
// for each, and a reseller who reaches a tier is paid its bonus, once for that month. It is off until the owner switches it on.
//
// What counts as a sale: a new service the reseller bought that has been delivered (a renewal or a traffic package is not a new service, and a
// free trial or an order that cost nothing is not a sale). A sale belongs to the month it was delivered in, read in the store's time zone
// (the same zone the reports and the daily summary use) and in the calendar the owner chose (Gregorian, the default, or Jalali). A sale is
// recorded only while targets are on, so turning them on in the middle of a month counts from that moment.
//
// When the count of a month reaches a tier on a delivery, the bonus is credited in the same transaction: a ledger entry of kind target_bonus, key
// target:<reseller user id>:<period>:<threshold>, written once however the delivery is retried and however the owner edits the list (a tier is
// told by its threshold, not by its place in the list). A refunded sale leaves the count of its month, but a bonus already paid is NOT taken
// back: a refund can lower the count below a tier that was reached, and the bonus stays.
const (
	SettingTargetsEnabled  = "targets.enabled"
	SettingTargetsTiers    = "targets.tiers"
	SettingTargetsCalendar = "targets.calendar"

	// MaxTargetTiers is the most tiers the owner may list.
	MaxTargetTiers = 10
	// MaxTargetThreshold is the most sales a tier may ask for in a month.
	MaxTargetThreshold = 100_000
)

// TargetTier is one step of the monthly target: Bonus Toman for Threshold services sold in a month.
type TargetTier struct {
	Threshold int64 `json:"threshold"`
	Bonus     int64 `json:"bonus"`
}

// errTiers is what ParseTargetTiers says about a list that is not one.
var errTiers = errors.New("a list of tiers, each with a threshold and a bonus")

// ParseTargetTiers reads the owner's list (a JSON array of {"threshold", "bonus"}) the way it is saved: whole numbers only, at most MaxTargetTiers
// tiers, each threshold from 1 to MaxTargetThreshold and each bonus from 1 to MaxGrowthAmount, no threshold twice. The result is sorted by
// threshold. An empty text is no tiers.
func ParseTargetTiers(raw string) ([]TargetTier, error) {
	if raw == "" {
		return nil, nil
	}
	var tiers []TargetTier
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tiers); err != nil || dec.More() {
		return nil, errTiers
	}
	if len(tiers) > MaxTargetTiers {
		return nil, fmt.Errorf("at most %d tiers", MaxTargetTiers)
	}
	for _, t := range tiers {
		switch {
		case t.Threshold < 1 || t.Threshold > MaxTargetThreshold:
			return nil, fmt.Errorf("a tier asks for 1 to %d services", MaxTargetThreshold)
		case t.Bonus < 1 || t.Bonus > MaxGrowthAmount:
			return nil, errors.New("a bonus is a whole number of Toman, at least 1")
		}
	}
	slices.SortFunc(tiers, func(a, b TargetTier) int { return cmp.Compare(a.Threshold, b.Threshold) })
	for i := 1; i < len(tiers); i++ {
		if tiers[i].Threshold == tiers[i-1].Threshold {
			return nil, errors.New("two tiers ask for the same number of services")
		}
	}
	return tiers, nil
}

// FormatTargetTiers writes the list the way it is stored: sorted, compact JSON ("" for none).
func FormatTargetTiers(tiers []TargetTier) string {
	if len(tiers) == 0 {
		return ""
	}
	sorted := slices.Clone(tiers)
	slices.SortFunc(sorted, func(a, b TargetTier) int { return cmp.Compare(a.Threshold, b.Threshold) })
	raw, _ := json.Marshal(sorted)
	return string(raw)
}

// TargetsConfig is the owner's target settings, read.
type TargetsConfig struct {
	Enabled bool
	// Tiers, lowest threshold first.
	Tiers []TargetTier
	// Calendar is CalendarGregorian or CalendarJalali.
	Calendar string
}

// TargetsConfigOf reads the settings (a value that is not valid reads as unset: the settings are checked as they are saved, and a bad one must not
// pay on a guess).
func TargetsConfigOf(set map[string]string) TargetsConfig {
	c := TargetsConfig{Enabled: set[SettingTargetsEnabled] == "true", Calendar: CalendarGregorian}
	if set[SettingTargetsCalendar] == CalendarJalali {
		c.Calendar = CalendarJalali
	}
	if tiers, err := ParseTargetTiers(set[SettingTargetsTiers]); err == nil {
		c.Tiers = tiers
	}
	return c
}

// On reports whether targets pay: switched on, with at least one tier.
func (c TargetsConfig) On() bool { return c.Enabled && len(c.Tiers) > 0 }

// storeLocation is the store's time zone (the setting, else BOBRES_TIMEZONE, else Asia/Tehran: the zone of the reports). A zone this process cannot
// load must never stop a delivery or a spin: Tehran is then its fixed offset (it has had no daylight saving since 2022), anything else UTC.
func (s *Service) storeLocation(set map[string]string) *time.Location {
	name := s.storeZone(set, func(source, zone string) {
		s.cfg.Log.Warn("growth tools: not a time zone, skipped", "source", source, "value", zone)
	})
	if loc, err := loadZone(name); err == nil {
		return loc
	}
	if name == DefaultReportTimezone {
		return time.FixedZone("IRST", 3*3600+1800)
	}
	return time.UTC
}

// now is the service's clock (Config.Now, for tests).
func (s *Service) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

// TargetPeriod is a month of the target: its label (yyyy-mm, in the calendar's own numbers), the Gregorian dates it covers in the store's zone
// (From to To, both included) and the instant it ends.
type TargetPeriod struct {
	Label    string
	From, To string
	EndsAt   time.Time
}

// targetPeriodAt is the month an instant falls in, read in loc and the calendar.
func targetPeriodAt(at time.Time, loc *time.Location, calendar string) TargetPeriod {
	local := at.In(loc)
	day := dateOf(local)
	start := bucketStart(day, GroupMonth, calendar)
	next := nextBucket(start, GroupMonth, calendar)
	label := day.Format("2006-01")
	if calendar == CalendarJalali {
		jy, jm, _ := i18n.GregorianToJalali(day.Year(), int(day.Month()), day.Day())
		label = fmt.Sprintf("%04d-%02d", jy, jm)
	}
	return TargetPeriod{
		Label: label, From: start.Format(dateLayout), To: next.AddDate(0, 0, -1).Format(dateLayout),
		EndsAt: time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, loc),
	}
}

// countsAsSale reports whether a delivered order is a sale for the targets: a new service a reseller bought and that cost money.
func countsAsSale(o *store.Order, plan *store.Plan, buyer *store.User) bool {
	switch {
	case buyer == nil || buyer.Status != "active" || !isReseller(buyer):
		return false
	case plan == nil || plan.IsTrial || plan.IsTopup:
		return false
	case o.Type != OrderNew || o.Amount <= 0 || o.RefundedAt != nil:
		return false
	}
	return true
}

// recordSale runs in the transaction that marks a reseller's service delivered: it records the sale in its month and pays the bonus of every
// tier the month's count has reached and that was not paid yet. Nothing is done while targets are off.
func (s *Service) recordSale(ctx context.Context, tx pgx.Tx, set map[string]string, o *store.Order, plan *store.Plan, buyer *store.User) error {
	cfg := TargetsConfigOf(set)
	if !cfg.On() || !countsAsSale(o, plan, buyer) {
		return nil
	}
	period := targetPeriodAt(s.now(), s.storeLocation(set), cfg.Calendar)
	// One reseller's deliveries count and pay one at a time.
	if err := s.st.LockResellerTargets(ctx, tx, buyer.ID); err != nil {
		return err
	}
	fresh, err := s.st.InsertResellerSale(ctx, tx, o.ID, buyer.ID, period.Label)
	if err != nil {
		return err
	}
	if !fresh {
		return nil // counted already: a retried delivery
	}
	sold, err := s.st.CountResellerSales(ctx, tx, buyer.ID, period.Label)
	if err != nil {
		return err
	}
	for _, tier := range cfg.Tiers {
		if sold < tier.Threshold {
			break // the tiers are in order: the rest are not reached either
		}
		if err := s.payTier(ctx, tx, buyer, period.Label, tier, sold); err != nil {
			return err
		}
	}
	return nil
}

// payTier credits a tier's bonus unless the reseller was paid that tier for the month, and queues the message that tells them.
func (s *Service) payTier(ctx context.Context, tx pgx.Tx, buyer *store.User, period string, tier TargetTier, sold int64) error {
	paid, err := s.st.TargetBonusPaid(ctx, tx, buyer.ID, period, tier.Threshold)
	if err != nil || paid {
		return err
	}
	entry := &store.LedgerEntry{Kind: "target_bonus", RefType: strptr("user"), RefID: strptr(buyer.ID),
		IdempotencyKey: fmt.Sprintf("target:%s:%s:%d", buyer.ID, period, tier.Threshold)}
	w, err := s.st.Credit(ctx, tx, buyer.ID, WalletCurrency, tier.Bonus, entry)
	if errors.Is(err, store.ErrDuplicateIdempotency) || errors.Is(err, store.ErrIdempotencyConflict) {
		// The ledger holds this tier for this month already but the table does not say so: nothing moved. The service the reseller delivered must
		// not wait for it, and the books are left for a person to look at.
		s.cfg.Log.Error("reseller targets: the ledger key of a bonus is taken; no bonus was paid", "reseller", buyer.ID, "period", period, "threshold", tier.Threshold)
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.st.InsertTargetBonus(ctx, tx, &store.TargetBonus{UserID: buyer.ID, Period: period, Threshold: tier.Threshold, Amount: tier.Bonus,
		Currency: WalletCurrency, Sales: sold, LedgerEntryID: entry.ID}); err != nil {
		return err
	}
	return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{
		UserID: buyer.ID, TelegramID: buyer.TelegramID, Amount: tier.Bonus, Currency: WalletCurrency, Balance: w.Balance,
		Reason: events.CreditTargetBonus, TargetSales: int(sold), TargetMonth: period, TargetThreshold: int(tier.Threshold),
	})
}

// TargetTierView is a tier as a reseller sees it this month.
type TargetTierView struct {
	Threshold, Bonus int64
	// Reached: this month's count has got to the threshold. Paid: the bonus was paid for the month (a tier can be paid and not reached any more,
	// when a refund lowered the count).
	Reached, Paid bool
}

// TargetProgress is a reseller's month against the owner's targets.
type TargetProgress struct {
	// On is true while targets pay: switched on, with at least one tier. When false the rest is empty.
	On bool
	// Period is the label of the month (yyyy-mm), Calendar the calendar it is read in, From and To the Gregorian dates it covers in the store's
	// zone and EndsAt the instant it ends.
	Period, Calendar string
	From, To         string
	EndsAt           time.Time
	// Sold is the services delivered this month that count (refunded ones do not).
	Sold  int64
	Tiers []TargetTierView
	// Next is the lowest tier not reached yet (nil when all are), and Remaining the sales to it.
	Next      *TargetTierView
	Remaining int64
	// BonusPaid is what was paid for the month, in Toman.
	BonusPaid int64
}

// ResellerTargets is a reseller's progress this month. A user who is not a reseller is not found.
func (s *Service) ResellerTargets(ctx context.Context, userID string) (*TargetProgress, error) {
	u, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return nil, err
	}
	if !isReseller(u) {
		return nil, store.ErrNotFound
	}
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return s.targetProgress(ctx, set, u.ID)
}

// optionalTargets is a reseller's month for a screen that is about something else (their panel): nil while targets are off, and nil, with a note in
// the log, when it cannot be read, so the panel never fails for a bonus.
func (s *Service) optionalTargets(ctx context.Context, userID string) *TargetProgress {
	set, err := s.Settings(ctx)
	if err == nil {
		var p *TargetProgress
		if p, err = s.targetProgress(ctx, set, userID); err == nil {
			if !p.On {
				return nil
			}
			return p
		}
	}
	s.cfg.Log.Warn("reseller targets: progress not read", "reseller", userID, "err", err)
	return nil
}

// targetProgress works out one reseller's month; it reads, and writes nothing.
func (s *Service) targetProgress(ctx context.Context, set map[string]string, userID string) (*TargetProgress, error) {
	cfg := TargetsConfigOf(set)
	p := &TargetProgress{On: cfg.On(), Calendar: cfg.Calendar}
	if !p.On {
		return p, nil
	}
	per := targetPeriodAt(s.now(), s.storeLocation(set), cfg.Calendar)
	p.Period, p.From, p.To, p.EndsAt = per.Label, per.From, per.To, per.EndsAt
	var err error
	if p.Sold, err = s.st.CountResellerSales(ctx, s.st.Conn(), userID, per.Label); err != nil {
		return nil, err
	}
	bonuses, err := s.st.TargetBonuses(ctx, s.st.Conn(), userID, per.Label)
	if err != nil {
		return nil, err
	}
	paid := make(map[int64]bool, len(bonuses))
	for _, b := range bonuses {
		paid[b.Threshold] = true
		p.BonusPaid += b.Amount
	}
	for _, t := range cfg.Tiers {
		v := TargetTierView{Threshold: t.Threshold, Bonus: t.Bonus, Reached: p.Sold >= t.Threshold, Paid: paid[t.Threshold]}
		p.Tiers = append(p.Tiers, v)
		if p.Next == nil && !v.Reached {
			next := v
			p.Next, p.Remaining = &next, t.Threshold-p.Sold
		}
	}
	return p, nil
}
