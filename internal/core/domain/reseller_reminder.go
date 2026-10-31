package domain

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The weekly settlement reminder (docs/04-dashboard.md, Resellers). Opt in: when the store turns it on, core's worker sends the resellers a
// message once a week, at a weekday and time of the store's time zone (general.timezone; Thursday 21:00 by default): "your account debt is
// {debt}, please settle it as agreed", in each reseller's own bot language. By default only the resellers in debt get it.
//
// It is a message to resellers like the ones staff write (reseller_messages.go): one broadcast a week, sent by the broadcast machinery at its
// pace, each reseller's text written with their debt when it starts. A week is queued exactly once: its claim (core.reseller_reminder_claims,
// keyed by the local date it was due on) is written in the transaction that writes the broadcast, so a crash can neither lose nor repeat it,
// and a restart or a second claimer finds the claim. The worker looks once a minute and queues when
//   - the reminder is on, and was switched on before the due time (switching it on after this week's time starts the next week);
//   - the local clock has passed the weekday and time (a time that does not exist on the day clocks go forward is reached when they land after
//     it; one that happens twice is reached the first time);
//   - it is at most reminderLate after the due time: a core that was down over the due time sends this week's reminder when it is back within
//     those hours, never one for a week it missed;
//   - no reminder went out in the 6 days before (a moved weekday never sends twice in a week).

// The settings (SettingSpecs) and their defaults.
const (
	SettingReminderEnabled  = "resellers.reminder.enabled"
	SettingReminderWeekday  = "resellers.reminder.weekday"
	SettingReminderTime     = "resellers.reminder.time"
	SettingReminderDebtOnly = "resellers.reminder.debt_only"
	SettingReminderSettle   = "resellers.reminder.settle_button"
	// SettingReminderText is the text of a language: SettingReminderText + "fa".
	SettingReminderText = "resellers.reminder.text."

	DefaultReminderWeekday = "thursday"
	DefaultReminderTime    = "21:00"
)

// DefaultReminderTexts are the reminder's texts while the store has written none.
var DefaultReminderTexts = map[string]string{
	"fa": "سلام {name}،\nبدهی حساب شما <b>{debt}</b> است. لطفاً طبق توافق، هرچه زودتر تسویه کنید.\nبا سپاس از همکاری شما 🙏",
	"en": "Hello {name},\nYour account debt is <b>{debt}</b>. Please settle it as agreed.\nThank you for working with us 🙏",
}

// ReminderWeekdays are the weekday setting's options, Saturday first as the Persian week starts.
var ReminderWeekdays = []string{"saturday", "sunday", "monday", "tuesday", "wednesday", "thursday", "friday"}

var weekdayOf = map[string]time.Weekday{"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday}

const (
	reminderEvery = time.Minute
	// reminderLate is how long after its time a week's reminder may still go out.
	reminderLate = 6 * time.Hour
	// reminderGap is how close two reminders may be, in days.
	reminderGap = 6
	// reminderTextMax is the longest reminder text the settings take, in characters (Telegram's limit applies to the text written from it).
	reminderTextMax = 3000
)

// ReminderConfig is the reminder's settings as the worker reads them (defaults for what is unset or unreadable).
type ReminderConfig struct {
	Enabled  bool
	Weekday  string // one of ReminderWeekdays
	Time     string // HH:MM
	DebtOnly bool
	Settle   bool
	Texts    map[string]string // per language: the store's text, else the default
	// Custom says which languages have the store's own text.
	Custom map[string]bool
}

func (c ReminderConfig) dueMinute() int {
	m, _ := clockMinutes(c.Time)
	return m
}

// reminderConfig reads the settings.
func reminderConfig(set map[string]string) ReminderConfig {
	c := ReminderConfig{Enabled: set[SettingReminderEnabled] == "true", Weekday: set[SettingReminderWeekday], DebtOnly: set[SettingReminderDebtOnly] != "false",
		Settle: set[SettingReminderSettle] != "false", Texts: map[string]string{}, Custom: map[string]bool{}}
	if _, ok := weekdayOf[c.Weekday]; !ok {
		c.Weekday = DefaultReminderWeekday
	}
	if t, ok := clockTime(set[SettingReminderTime]); ok {
		c.Time = t
	} else {
		c.Time = DefaultReminderTime
	}
	for _, l := range i18n.Languages {
		v := set[SettingReminderText+l]
		if _, err := CheckResellerText(l, SettingReminderText+l, v); v != "" && err == nil {
			c.Texts[l], c.Custom[l] = v, true
		} else {
			c.Texts[l] = DefaultReminderTexts[l]
		}
	}
	return c
}

// lastDue is the most recent due time at or before now (in its zone): the local date it falls on and the instant.
func lastDue(now time.Time, weekday time.Weekday, due int) (day, at time.Time) {
	back := (int(now.Weekday()) - int(weekday) + 7) % 7
	day = dateOf(now).AddDate(0, 0, -back)
	if back == 0 && minuteOfDay(now) < due {
		day = day.AddDate(0, 0, -7)
	}
	return day, time.Date(day.Year(), day.Month(), day.Day(), due/60, due%60, 0, 0, now.Location())
}

// nextDue is the first due time after now.
func nextDue(now time.Time, weekday time.Weekday, due int) time.Time {
	day, _ := lastDue(now, weekday, due)
	day = day.AddDate(0, 0, 7)
	return time.Date(day.Year(), day.Month(), day.Day(), due/60, due%60, 0, 0, now.Location())
}

// ReminderStatus is the reminder's card on the Resellers page: its settings, the zone they are read in, the next due time after now (while
// on) and the last week it went out.
type ReminderStatus struct {
	Config  ReminderConfig
	Zone    string
	NextDue *time.Time
	Last    *store.ReminderClaim
}

// ResellerReminderStatus reads the reminder the way the worker does.
func (s *Service) ResellerReminderStatus(ctx context.Context) (*ReminderStatus, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	out := &ReminderStatus{Config: reminderConfig(set), Zone: s.storeZone(set, func(string, string) {})}
	if out.Last, err = s.st.LastResellerReminder(ctx, s.st.Conn()); err != nil {
		return nil, err
	}
	if loc, err := loadZone(out.Zone); out.Config.Enabled && err == nil {
		next := nextDue(s.now().In(loc), weekdayOf[out.Config.Weekday], out.Config.dueMinute())
		out.NextDue = &next
	}
	return out, nil
}

// ResellerReminder is the worker that queues the weekly reminder.
type ResellerReminder struct {
	s       *Service
	log     *slog.Logger
	every   time.Duration
	now     func() time.Time
	skipped map[string]bool
}

// NewResellerReminder builds the worker.
func (s *Service) NewResellerReminder(log *slog.Logger) *ResellerReminder {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ResellerReminder{s: s, log: log.With("worker", "reseller-reminder"), every: reminderEvery, now: time.Now, skipped: map[string]bool{}}
}

// Run looks every minute until ctx ends.
func (r *ResellerReminder) Run(ctx context.Context) error {
	t := time.NewTicker(r.every)
	defer t.Stop()
	for {
		if err := r.Step(ctx); err != nil && ctx.Err() == nil {
			r.log.Warn("reseller reminder failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step looks once: it queues this week's reminder when it is due and not yet claimed.
func (r *ResellerReminder) Step(ctx context.Context) error {
	st := r.s.st
	set, err := r.s.Settings(ctx)
	if err != nil {
		return err
	}
	cfg := reminderConfig(set)
	if !cfg.Enabled {
		return nil
	}
	zone := r.s.storeZone(set, r.skipZone)
	loc, err := loadZone(zone)
	if err != nil {
		return fmt.Errorf("reseller reminder: %w", err)
	}
	now := r.now().In(loc)
	day, dueAt := lastDue(now, weekdayOf[cfg.Weekday], cfg.dueMinute())
	if now.Sub(dueAt) > reminderLate {
		return nil // missed (core was down): the next week's
	}
	since, found, err := st.SettingChangedAt(ctx, st.Conn(), SettingReminderEnabled)
	if err != nil {
		return err
	}
	if !found || since.After(dueAt) {
		return nil // switched on after this week's time: it starts next week
	}
	last, err := st.LastResellerReminder(ctx, st.Conn())
	if err != nil {
		return err
	}
	if last != nil {
		if prev, err := time.Parse(dateLayout, last.Day); err == nil && day.Before(prev.AddDate(0, 0, reminderGap)) {
			return nil // this week's went already (or one at most 6 days ago)
		}
	}
	return r.queue(ctx, cfg, day.Format(dateLayout), zone, dueAt)
}

// queue writes the claim and the week's broadcast in one transaction.
func (r *ResellerReminder) queue(ctx context.Context, cfg ReminderConfig, day, zone string, dueAt time.Time) error {
	var (
		b   *store.Broadcast
		won bool
	)
	err := r.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		if won, err = r.s.st.ClaimResellerReminder(ctx, tx, day, zone, dueAt); err != nil || !won {
			return err
		}
		b = reminderBroadcast(cfg)
		if err := r.s.queueResellerBroadcast(ctx, tx, b); err != nil {
			return err
		}
		if err := r.s.st.SetReminderBroadcast(ctx, tx, day, b.ID); err != nil {
			return err
		}
		after := resellerMessageAudit(b)
		after["day"], after["zone"], after["due_at"] = day, zone, dueAt.Unix()
		return r.s.audit(ctx, tx, nil, "reseller_reminder.queue", "broadcast", b.ID, after, "")
	})
	if err != nil || !won {
		return err
	}
	r.s.KickBroadcasts()
	r.log.Info("reseller reminder queued", "day", day, "zone", zone, "broadcast", b.ID, "debt_only", cfg.DebtOnly)
	return nil
}

// reminderBroadcast is the week's reminder as a message to resellers: every language's text, so each reseller reads their own.
func reminderBroadcast(cfg ReminderConfig) *store.Broadcast {
	texts := map[string]string{}
	for _, l := range i18n.Languages {
		texts[l] = i18n.NormalizeText(cfg.Texts[l])
	}
	return &store.Broadcast{Segment: store.SegmentResellers, Texts: texts, ResellerDebtOnly: cfg.DebtOnly, SettleButton: cfg.Settle, Kind: "reminder"}
}

func (r *ResellerReminder) skipZone(source, zone string) {
	key := source + "=" + zone
	if r.skipped[key] {
		return
	}
	r.skipped[key] = true
	r.log.Warn("reseller reminder: not a time zone, skipped", "source", source, "value", zone)
}

// ReminderTestText is the reminder as the reseller resellerID (""= the first of its audience, else the sample one) would get it now in lang.
func (s *Service) ReminderTestText(ctx context.Context, resellerID, lang string) (*ResellerPreview, ReminderConfig, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, ReminderConfig{}, err
	}
	cfg := reminderConfig(set)
	lang = i18n.Normalize(lang)
	p, err := s.PreviewResellerText(ctx, resellerID, lang, cfg.Texts[lang], cfg.DebtOnly, "")
	return p, cfg, err
}

// checkReminderText is NormalizeSetting's check of a reminder text: a message to resellers.
func checkReminderText(key, value string) (string, error) {
	return CheckResellerText(strings.TrimPrefix(key, SettingReminderText), key, value)
}
