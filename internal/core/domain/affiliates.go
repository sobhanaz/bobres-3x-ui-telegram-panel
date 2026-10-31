package domain

import (
	"context"
	"errors"
	"math/bits"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// The affiliate program of the owner's own store (docs/18-hosted-and-affiliate-plans.md, section 2): people who bring a new BOBRES license
// buyer earn a commission on that buyer's first license payment.
//
//   - Every customer can be an affiliate: /affiliate in the bot gives them a code and the link t.me/<bot>?start=aff_<code>.
//   - A customer who opens such a link before they ever bought a license is recorded as brought by that affiliate (the first link wins). The
//     affiliate's own link records nothing for them (no self-referral: the same Telegram account is the same user).
//   - The affiliate is written on the buyer's first new license order when it is made. When that order is paid AND delivered (the license
//     exists), the affiliate's wallet is credited the program's percent of what the buyer paid (20% unless the owner sets another, or a percent
//     of the affiliate's own), once: a ledger entry of kind affiliate_commission under the key affiliate_commission:<order id>, written in the
//     transaction that marks the order done. One commission per buyer, ever: renewals and later licenses earn nothing.
//   - When the owner refunds that license within 30 days of its payment, the same refund takes the commission back from the affiliate's wallet
//     as far as the wallet holds it, never below zero; what could not be taken is written to the audit log. A refund after 30 days keeps it.
//   - The owner pays affiliates by hand (USDT, a card transfer) and records it in the dashboard: the amount leaves the affiliate's wallet (kind
//     affiliate_payout), at least the minimum payout, at most what they earned and was not paid yet.
//
// It runs only where licenses are sold (VendorOn: the vendor's own store) and only while the owner has switched it on (affiliate.enabled).

// The settings of the program.
const (
	SettingAffiliateEnabled   = "affiliate.enabled"
	SettingAffiliatePercent   = "affiliate.percent"
	SettingAffiliateMinPayout = "affiliate.min_payout"
)

const (
	// DefaultAffiliatePercent is the program's commission while the owner has not set one: 20% of the first payment.
	DefaultAffiliatePercent = 20
	// AffiliateRefundWindow is how long after a license's payment a refund takes its commission back.
	AffiliateRefundWindow = 30 * 24 * time.Hour
	// affiliateListMax bounds the commissions and payouts the dashboard shows of one affiliate.
	affiliateListMax = 100
)

var affiliateCodeRe = regexp.MustCompile(`^[a-z0-9]{6,16}$`)

// AffiliateConfig is the owner's settings of the program, read.
type AffiliateConfig struct {
	Enabled   bool
	Percent   int   // the program's percent (1 to 100)
	MinPayout int64 // Toman; 0: no minimum
}

// AffiliateConfigOf reads the program's settings from the settings map (a bad value reads as the default: the settings are checked when saved).
func AffiliateConfigOf(set map[string]string) AffiliateConfig {
	c := AffiliateConfig{Enabled: set[SettingAffiliateEnabled] == "true", Percent: DefaultAffiliatePercent}
	if n, err := strconv.Atoi(strings.TrimSpace(set[SettingAffiliatePercent])); err == nil && n >= 1 && n <= 100 {
		c.Percent = n
	}
	c.MinPayout = settingAmount(set[SettingAffiliateMinPayout])
	return c
}

// percentFor is the commission an affiliate earns under these settings: their own percent when the owner set one (0: none), else the program's.
func (c AffiliateConfig) percentFor(a *store.Affiliate) int {
	if a.Percent != nil {
		return *a.Percent
	}
	return c.Percent
}

// affiliateAmount is floor(base x percent / 100), worked out in 128 bits so a huge base cannot wrap.
func affiliateAmount(base int64, percent int) int64 {
	if base <= 0 || percent <= 0 || percent > 100 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(base), uint64(percent))
	q, _ := bits.Div64(hi, lo, 100) // hi < 100: base < 2^63 and percent <= 100
	return int64(q)                 //nolint:gosec // q <= base < 2^63
}

func (s *Service) affiliateConfig(ctx context.Context) AffiliateConfig {
	set, err := s.Settings(ctx)
	if err != nil {
		return AffiliateConfig{Percent: DefaultAffiliatePercent}
	}
	return AffiliateConfigOf(set)
}

// AffiliateOn reports whether the program runs now: the store sells licenses and the owner switched the program on.
func (s *Service) AffiliateOn(ctx context.Context) bool {
	return s.VendorOn(ctx) && s.setting(ctx, SettingAffiliateEnabled) == "true"
}

// AffiliateInfo is what an affiliate sees of their own: their code, their percent and their figures (Toman).
type AffiliateInfo struct {
	Code      string
	Percent   int
	Status    string
	Referred  int64
	Licenses  int64
	Earned    int64
	Paid      int64
	Balance   int64
	MinPayout int64
}

// MyAffiliate makes a customer an affiliate (once) and returns their code and figures. It is refused while the program is off.
func (s *Service) MyAffiliate(ctx context.Context, userID string) (*AffiliateInfo, error) {
	if !s.AffiliateOn(ctx) {
		return nil, stateErr("the affiliate program is off")
	}
	u, err := s.activeUser(ctx, nil, userID)
	if err != nil {
		return nil, err
	}
	var a *store.Affiliate
	for range 5 {
		if a, err = s.st.CreateAffiliate(ctx, s.st.Conn(), u.ID, newRefCode()); !errors.Is(err, store.ErrAffiliateCodeTaken) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	st, err := s.st.GetAffiliateStats(ctx, s.st.Conn(), a.ID)
	if err != nil {
		return nil, err
	}
	w, err := s.st.GetWallet(ctx, s.st.Conn(), u.ID, WalletCurrency)
	if err != nil {
		return nil, err
	}
	cfg := s.affiliateConfig(ctx)
	return &AffiliateInfo{Code: a.Code, Percent: cfg.percentFor(a), Status: a.Status, Referred: st.Referred, Licenses: st.Licenses,
		Earned: st.Earned[WalletCurrency], Paid: st.Paid[WalletCurrency], Balance: w.Balance, MinPayout: cfg.MinPayout}, nil
}

// ApplyAffiliateCode records that the customer came through an affiliate's link (start=aff_<code>). It says whether it recorded them; a link
// that cannot count (the program is off, an unknown or disabled code, the affiliate's own link, a customer who bought a license already or was
// brought by someone else first) records nothing and is not an error: the customer just goes on to the store.
func (s *Service) ApplyAffiliateCode(ctx context.Context, userID, code string) (bool, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if !affiliateCodeRe.MatchString(code) || !s.AffiliateOn(ctx) {
		return false, nil
	}
	u, err := s.st.GetUser(ctx, s.st.Conn(), userID)
	if err != nil {
		return false, err
	}
	if u.Status != "active" {
		return false, nil
	}
	a, err := s.st.GetAffiliateByCode(ctx, s.st.Conn(), code)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if a.Status != "active" {
		return false, nil
	}
	if a.UserID == u.ID {
		s.cfg.Log.Info("affiliate link: the affiliate opened their own link; not recorded", "affiliate", a.ID)
		return false, nil
	}
	bought, err := s.st.HasLicenseOrder(ctx, s.st.Conn(), u.ID)
	if err != nil || bought {
		return false, err
	}
	return s.st.SetAffiliateReferral(ctx, s.st.Conn(), u.ID, a.ID)
}

// affiliateForOrder is the affiliate to write on a new license order of buyer, in the order's transaction ("" when none).
func (s *Service) affiliateForOrder(ctx context.Context, tx pgx.Tx, buyerID string) (string, error) {
	id, err := s.st.AffiliateOfBuyer(ctx, tx, buyerID, false)
	if err != nil || id == "" {
		return "", err
	}
	bought, err := s.st.HasLicenseOrder(ctx, tx, buyerID)
	if err != nil || bought {
		return "", err // a buyer's later license: the first payment only
	}
	return id, nil
}

// payAffiliate credits the commission a delivered license order earned, in the transaction that marks it done. It never fails the delivery
// for a reason of its own (a disabled affiliate, a buyer that earned before, a ledger key taken by another entry): it pays nothing then. Only
// the database's errors roll the transaction back.
func (s *Service) payAffiliate(ctx context.Context, tx pgx.Tx, o *store.VendorOrder) error {
	if o.Kind != "new" || o.AffiliateID == nil || o.Amount <= 0 {
		return nil
	}
	set, err := s.settingsIn(ctx, tx)
	if err != nil {
		return err
	}
	cfg := AffiliateConfigOf(set)
	if !cfg.Enabled {
		return nil // the owner switched the program off meanwhile: nothing new is paid
	}
	a, err := s.st.GetAffiliate(ctx, tx, *o.AffiliateID)
	if err != nil {
		return err
	}
	if a.Status != "active" || a.UserID == o.UserID {
		return nil
	}
	affUser, err := s.st.GetUser(ctx, tx, a.UserID)
	if err != nil {
		return err
	}
	if affUser.Status != "active" {
		return nil
	}
	// The buyer's referral row is the lock: two of their orders finishing at once are paid one after the other, and only the first earns.
	if _, err := s.st.AffiliateOfBuyer(ctx, tx, o.UserID, true); err != nil {
		return err
	}
	earned, err := s.st.BuyerHasCommission(ctx, tx, o.UserID)
	if err != nil || earned {
		return err
	}
	pct := cfg.percentFor(a)
	amount := affiliateAmount(o.Amount, pct)
	if amount <= 0 {
		return nil
	}
	entry := &store.LedgerEntry{Kind: "affiliate_commission", RefType: strptr("vendor_order"), RefID: strptr(o.ID),
		IdempotencyKey: "affiliate_commission:" + o.ID}
	w, err := s.st.Credit(ctx, tx, a.UserID, o.Currency, amount, entry)
	if errors.Is(err, store.ErrDuplicateIdempotency) {
		return nil // this order earned already
	}
	if errors.Is(err, store.ErrIdempotencyConflict) {
		s.cfg.Log.Error("affiliate: the ledger key is taken by another entry; no commission was paid", "order", o.ID)
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.st.InsertAffiliateCommission(ctx, tx, &store.AffiliateCommission{OrderID: o.ID, AffiliateID: a.ID, AffiliateUserID: a.UserID,
		BuyerUserID: o.UserID, Currency: o.Currency, BaseAmount: o.Amount, Percent: pct, Amount: amount, LedgerEntryID: entry.ID}); err != nil {
		return err
	}
	return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{UserID: affUser.ID, TelegramID: affUser.TelegramID,
		Amount: amount, Currency: o.Currency, Balance: w.Balance, Reason: events.CreditAffiliate})
}

// reverseAffiliate takes back the commission of a refunded order, in the refund's transaction: within the 30 days only, as far as the
// affiliate's wallet holds it, never below zero. It returns what was credited and what was taken (credited 0: the order earned none, or it
// was reversed before); kept is true when the refund came after the window and the commission stays.
func (s *Service) reverseAffiliate(ctx context.Context, tx pgx.Tx, actor *store.User, o *store.VendorOrder, now time.Time) (credited, taken int64, kept bool, err error) {
	c, err := s.st.GetAffiliateCommission(ctx, tx, o.ID, true)
	if errors.Is(err, store.ErrNotFound) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	if c.ReversedAt != nil {
		return 0, 0, false, nil
	}
	if now.Sub(o.CreatedAt) > AffiliateRefundWindow {
		return c.Amount, 0, true, nil
	}
	entry := &store.LedgerEntry{Kind: "affiliate_reversal", RefType: strptr("vendor_order"), RefID: strptr(o.ID),
		IdempotencyKey: "affiliate_reversal:" + o.ID}
	taken, _, err = s.st.DebitAtMost(ctx, tx, c.AffiliateUserID, c.Currency, c.Amount, entry)
	if err != nil && !errors.Is(err, store.ErrDuplicateIdempotency) {
		return 0, 0, false, err
	}
	var entryID *string
	if taken > 0 {
		entryID = &entry.ID
	}
	if _, err := s.st.MarkAffiliateCommissionReversed(ctx, tx, o.ID, taken, entryID); err != nil {
		return 0, 0, false, err
	}
	detail := map[string]any{"order": o.ID, "credited": c.Amount, "taken": taken, "shortfall": c.Amount - taken, "currency": c.Currency}
	if err := s.audit(ctx, tx, actor, "affiliate.reversal", "affiliate", c.AffiliateID, detail, ""); err != nil {
		return 0, 0, false, err
	}
	return c.Amount, taken, false, nil
}

// RefundSoldLicense gives a buyer back what they paid for a license the store sold (its first, "new" order): the license is revoked on the
// license server first (a retry finds it revoked), then one transaction credits the buyer, marks the order refunded and the license revoked,
// takes back the affiliate's commission when the refund is within 30 days of the payment, and audits it. Refunding again changes nothing.
func (s *Service) RefundSoldLicense(ctx context.Context, actor *store.User, licenseID, reason string) (*store.VendorOrder, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 480 || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
		return nil, invalid("a reason is required (at most 480 characters)")
	}
	l, err := s.st.GetVendorLicense(ctx, s.st.Conn(), licenseID, false)
	if err != nil {
		return nil, err
	}
	o, err := s.st.NewLicenseOrderOf(ctx, s.st.Conn(), l.ID, false)
	if errors.Is(err, store.ErrNotFound) {
		return nil, stateErr("this license has no purchase to refund")
	}
	if err != nil {
		return nil, err
	}
	if o.Status == "refunded" {
		return o, nil
	}
	if o.Status != "done" {
		return nil, stateErr("this purchase is not finished yet")
	}
	if l.Status == "active" {
		cl, err := s.vendorClient(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := cl.Revoke(ctx, "revoke-"+l.ID, l.LicenseID, reason); err != nil {
			var ve *VendorError
			if !errors.As(err, &ve) || ve.Status != 409 || ve.Code != "revoked" {
				return nil, err
			}
		}
	}
	now := s.cfg.vendorNow()
	var out *store.VendorOrder
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		cur, err := s.st.GetVendorOrder(ctx, tx, o.ID, true)
		if err != nil {
			return err
		}
		out = cur
		if cur.Status != "done" {
			return nil // refunded meanwhile
		}
		w, err := s.st.Credit(ctx, tx, cur.UserID, cur.Currency, cur.Amount, &store.LedgerEntry{
			Kind: "license_refund", RefType: strptr("vendor_order"), RefID: strptr(cur.ID), IdempotencyKey: "license_refund:" + cur.ID,
		})
		if err != nil && !errors.Is(err, store.ErrDuplicateIdempotency) {
			return err
		}
		if err := s.st.RefundDoneVendorOrder(ctx, tx, cur.ID, reason); err != nil {
			return err
		}
		if err := s.st.SetVendorLicenseStatus(ctx, tx, l.ID, "revoked"); err != nil {
			return err
		}
		credited, taken, kept, err := s.reverseAffiliate(ctx, tx, actor, cur, now)
		if err != nil {
			return err
		}
		cur.Status, cur.LastError = "refunded", reason
		detail := map[string]any{"order": cur.ID, "license_id": l.LicenseID, "amount": cur.Amount, "currency": cur.Currency}
		if credited > 0 {
			detail["affiliate_commission"], detail["affiliate_taken"], detail["affiliate_kept"] = credited, taken, kept
		}
		if err := s.audit(ctx, tx, actor, "vendor.refund", "vendor_license", l.ID, detail, reason); err != nil {
			return err
		}
		buyer, err := s.st.GetUser(ctx, tx, cur.UserID)
		if err != nil || w == nil {
			return err
		}
		return s.publish(ctx, tx, events.WalletCredited, events.WalletCreditedEvent{UserID: buyer.ID, TelegramID: buyer.TelegramID,
			Amount: cur.Amount, Currency: cur.Currency, Balance: w.Balance, Reason: events.CreditRefund})
	})
	return out, err
}

// ---- The owner's side (the dashboard's Affiliates page)

// AffiliateSettings is the program as the dashboard shows it: the settings, and whether it runs (it needs license sales on).
type AffiliateSettings struct {
	AffiliateConfig
	On         bool
	VendorOn   bool
	PercentSet bool
}

// GetAffiliateSettings reads the program's settings.
func (s *Service) GetAffiliateSettings(ctx context.Context) (*AffiliateSettings, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	cfg := AffiliateConfigOf(set)
	vendorOn := s.VendorOn(ctx)
	return &AffiliateSettings{AffiliateConfig: cfg, VendorOn: vendorOn, On: vendorOn && cfg.Enabled,
		PercentSet: strings.TrimSpace(set[SettingAffiliatePercent]) != ""}, nil
}

// SetAffiliateSettings saves the program's settings (checked and audited as every setting).
func (s *Service) SetAffiliateSettings(ctx context.Context, actor *store.User, enabled bool, percent int, minPayout int64) error {
	if err := s.requirePrivate(ctx); err != nil {
		return err
	}
	if percent < 1 || percent > 100 {
		return &FieldError{"percent", "1 to 100"}
	}
	if minPayout < 0 || minPayout > MaxGrowthAmount {
		return &FieldError{"min_payout", "0 or a whole number of Toman"}
	}
	_, err := s.SetSettings(ctx, actor, map[string]string{
		SettingAffiliateEnabled:   strconv.FormatBool(enabled),
		SettingAffiliatePercent:   strconv.Itoa(percent),
		SettingAffiliateMinPayout: strconv.FormatInt(minPayout, 10),
	})
	return err
}

// ListAffiliates lists the affiliates with their figures in Toman, newest first.
func (s *Service) ListAffiliates(ctx context.Context, p store.Page) ([]store.AffiliateRow, int64, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return nil, 0, err
	}
	return s.st.ListAffiliates(ctx, s.st.Conn(), WalletCurrency, p)
}

// AffiliateDetail is one affiliate with their figures, the commissions they earned and what they were paid.
type AffiliateDetail struct {
	Affiliate   *store.Affiliate
	User        *store.User
	Stats       *store.AffiliateStats
	Balance     int64
	Percent     int // what they earn now
	Commissions []store.AffiliateCommissionRow
	Payouts     []store.AffiliatePayout
}

// GetAffiliateDetail reads one affiliate for the dashboard.
func (s *Service) GetAffiliateDetail(ctx context.Context, id string) (*AffiliateDetail, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return nil, err
	}
	a, err := s.st.GetAffiliate(ctx, s.st.Conn(), id)
	if err != nil {
		return nil, err
	}
	d := &AffiliateDetail{Affiliate: a, Percent: s.affiliateConfig(ctx).percentFor(a)}
	if d.User, err = s.st.GetUser(ctx, s.st.Conn(), a.UserID); err != nil {
		return nil, err
	}
	if d.Stats, err = s.st.GetAffiliateStats(ctx, s.st.Conn(), a.ID); err != nil {
		return nil, err
	}
	w, err := s.st.GetWallet(ctx, s.st.Conn(), a.UserID, WalletCurrency)
	if err != nil {
		return nil, err
	}
	d.Balance = w.Balance
	if d.Commissions, err = s.st.ListAffiliateCommissions(ctx, s.st.Conn(), a.ID, affiliateListMax); err != nil {
		return nil, err
	}
	if d.Payouts, err = s.st.ListAffiliatePayouts(ctx, s.st.Conn(), a.ID, affiliateListMax); err != nil {
		return nil, err
	}
	return d, nil
}

// UpdateAffiliate sets an affiliate's own percent (nil: the program's; 0: they earn nothing) and status (active or disabled), audited.
func (s *Service) UpdateAffiliate(ctx context.Context, actor *store.User, id string, percent *int, status string) (*store.Affiliate, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return nil, err
	}
	if percent != nil && (*percent < 0 || *percent > 100) {
		return nil, &FieldError{"percent", "0 to 100, or empty for the program's percent"}
	}
	if status != "active" && status != "disabled" {
		return nil, &FieldError{"status", "active or disabled"}
	}
	var out *store.Affiliate
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		before, err := s.st.GetAffiliate(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.st.UpdateAffiliate(ctx, tx, id, percent, status); err != nil {
			return err
		}
		out = before
		out.Percent, out.Status = percent, status
		return s.audit(ctx, tx, actor, "affiliate.update", "affiliate", id, map[string]any{
			"percent_before": before.Percent, "status_before": before.Status, "percent": percent, "status": status}, "")
	})
	return out, err
}

// RecordAffiliatePayout records what the owner paid an affiliate by hand: the amount leaves their wallet, once per request id. It must be at
// least the minimum payout, and at most what the affiliate earned and was not paid yet (and what the wallet holds).
func (s *Service) RecordAffiliatePayout(ctx context.Context, actor *store.User, id string, amount int64, note, requestID string) (*store.AffiliatePayout, error) {
	if err := s.requirePrivate(ctx); err != nil {
		return nil, err
	}
	if err := checkRequestID(requestID); err != nil {
		return nil, err
	}
	note = strings.TrimSpace(note)
	if len(note) > 500 || strings.IndexFunc(note, unicode.IsControl) >= 0 {
		return nil, &FieldError{"note", "at most 500 characters, on one line"}
	}
	if amount <= 0 || amount > MaxGrowthAmount {
		return nil, &FieldError{"amount", "a whole number of Toman above 0"}
	}
	if prev, err := s.st.GetAffiliatePayoutByRequest(ctx, s.st.Conn(), requestID); err == nil {
		if prev.AffiliateID != id || prev.Amount != amount {
			return nil, stateErr("this request id was used for another payout")
		}
		return prev, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if least := s.affiliateConfig(ctx).MinPayout; least > 0 && amount < least {
		return nil, &FieldError{"amount", "below the minimum payout (" + strconv.FormatInt(least, 10) + " Toman)"}
	}
	var out *store.AffiliatePayout
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		a, err := s.st.GetAffiliate(ctx, tx, id)
		if err != nil {
			return err
		}
		entry := &store.LedgerEntry{Kind: "affiliate_payout", RefType: strptr("affiliate"), RefID: strptr(a.ID),
			IdempotencyKey: "affiliate_payout:" + requestID}
		// The wallet's lock comes first (Debit takes it), so the figures read below cannot move until this commits.
		_, err = s.st.Debit(ctx, tx, a.UserID, WalletCurrency, amount, entry)
		switch {
		case errors.Is(err, store.ErrInsufficientFunds):
			return stateErr("the affiliate's wallet holds less than that")
		case errors.Is(err, store.ErrDuplicateIdempotency):
			out, err = s.st.GetAffiliatePayoutByRequest(ctx, tx, requestID)
			return err
		case err != nil:
			return err
		}
		st, err := s.st.GetAffiliateStats(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		if open := st.Earned[WalletCurrency] - st.Paid[WalletCurrency]; amount > open {
			return stateErr("more than the affiliate earned and was not paid yet (%d Toman)", max(open, 0))
		}
		var by *string
		if actor != nil {
			by = &actor.ID
		}
		out = &store.AffiliatePayout{AffiliateID: a.ID, Currency: WalletCurrency, Amount: amount, Note: note, RequestID: requestID,
			LedgerEntryID: entry.ID, CreatedBy: by}
		if err := s.st.InsertAffiliatePayout(ctx, tx, out); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "affiliate.payout", "affiliate", a.ID, map[string]any{"amount": amount, "currency": WalletCurrency, "note": note}, "")
	})
	return out, err
}
