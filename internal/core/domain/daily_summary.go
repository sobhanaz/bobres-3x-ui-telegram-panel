package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// The daily sales summary (docs/14-reports.md). Opt in: when the store turns it
// on, core's worker messages yesterday's sales (one store-local day, from the
// sales report) to the staff who may read the reports, once a local day, a
// little after the time the store chose. The bot sends it (report.daily_summary.v1,
// one event for each recipient).
//
// A summary is queued exactly once. Its claim (core.daily_summary_claims, keyed by
// the local date it is about) is written in the transaction that writes the
// outbox events, so a crash can neither lose nor repeat it, a restart or a
// second claimer finds the claim, and a change of the time setting cannot queue
// a day twice. (Delivery is at least once, like every event's: a bot killed
// between sending and acknowledging sends the message again.) The worker looks
// at the clock once a minute and queues when
//   - the feature is on, and was switched on before today's due time (switching it
//     on after its time of day starts the next day: no day is made up for);
//   - the local clock has reached the time, and it is at least 00:02 (a time that
//     does not exist on the day clocks go forward is reached when they land after
//     it; one that happens twice is reached the first time; the two minutes after
//     midnight let a purchase made just before it commit before its day is read);
//   - the summary of yesterday (the local day before today) has no claim yet.
//
// Only yesterday's summary is ever queued, so a core that was down for days
// queues one when it is back, not one for each day it missed.
//
// A failed attempt (the report is too slow, the database is down) is not run again
// every minute: the worker waits 5, then 10, then 15 minutes between attempts and
// gives up on that day after dailySummaryMaxAttempts.

// The settings (SettingSpecs) and their default.
const (
	SettingDailySummary     = "reports.daily_summary"
	SettingDailySummaryTime = "reports.daily_summary_time"
	// DefaultDailySummaryTime is HH:MM in the store's time zone.
	DefaultDailySummaryTime = "09:00"
)

const (
	dailySummaryEvery = time.Minute
	// dailySummaryGrace is the first minute of the local day a day's figures are
	// read: a purchase that began just before midnight and commits a moment
	// after it is dated the old day, and a read at 00:00 would miss it for good
	// (the claim makes the omission permanent).
	dailySummaryGrace = 2
	// After a failed attempt the worker waits dailySummaryRetryFirst, then twice
	// that, up to dailySummaryRetryMax, and stops trying for the day after
	// dailySummaryMaxAttempts: a report that cannot be made (a statement timeout
	// on a big ledger, a zone the database does not know) is not made again every
	// minute until midnight.
	dailySummaryRetryFirst  = 5 * time.Minute
	dailySummaryRetryMax    = 15 * time.Minute
	dailySummaryMaxAttempts = 8
)

// errSummaryGaveUp marks the error of the last attempt on a day.
var errSummaryGaveUp = errors.New("giving up on this day")

// dailySummarySettings reads the two settings: whether the summary is on, and
// the minute of the local day it is due (the default for a missing or unreadable
// time).
func dailySummarySettings(set map[string]string) (on bool, due int) {
	due, ok := clockMinutes(set[SettingDailySummaryTime])
	if !ok {
		due, _ = clockMinutes(DefaultDailySummaryTime)
	}
	return set[SettingDailySummary] == "true", due
}

// DailySummaryStatus says whether the daily summary is on, at what time (HH:MM)
// and in which time zone, for the Reports page.
type DailySummaryStatus struct {
	Enabled bool
	Time    string
	Zone    string
}

// DailySummaryStatus reads the settings the way the worker does.
func (s *Service) DailySummaryStatus(ctx context.Context) (*DailySummaryStatus, error) {
	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	on, due := dailySummarySettings(set)
	zone := s.storeZone(set, func(source, zone string) {
		s.cfg.Log.Warn("reports: not a time zone, skipped", "source", source, "value", zone)
	})
	return &DailySummaryStatus{Enabled: on, Time: fmt.Sprintf("%02d:%02d", due/60, due%60), Zone: zone}, nil
}

// DailySummary is the worker that sends the daily sales summary.
type DailySummary struct {
	s       *Service
	log     *slog.Logger
	every   time.Duration
	now     func() time.Time
	canRead func(role string) bool
	// skipped remembers the time zone problems already logged, so a bad
	// BOBRES_TIMEZONE is one line, not one a minute.
	skipped map[string]bool
	// failed is the attempts that failed on the day being sent.
	failed summaryFailures
}

// summaryFailures: the failed attempts on one day (a local date), and when the
// next one may start.
type summaryFailures struct {
	day   string
	n     int
	until time.Time
}

// NewDailySummary builds the worker. canRead says whether a staff role holds
// reports.read (the dashboard's permission table, which core's domain does not
// own): only those roles are told.
func (s *Service) NewDailySummary(canRead func(role string) bool, log *slog.Logger) *DailySummary {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &DailySummary{s: s, log: log.With("worker", "daily-summary"), every: dailySummaryEvery, now: time.Now,
		canRead: canRead, skipped: map[string]bool{}}
}

// Run looks every minute until ctx ends.
func (d *DailySummary) Run(ctx context.Context) error {
	t := time.NewTicker(d.every)
	defer t.Stop()
	for {
		if err := d.Step(ctx); err != nil && ctx.Err() == nil {
			level := slog.LevelWarn
			if errors.Is(err, errSummaryGaveUp) {
				level = slog.LevelError
			}
			d.log.Log(ctx, level, "daily summary failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step looks once: it queues yesterday's summary when it is due and not yet
// claimed.
func (d *DailySummary) Step(ctx context.Context) error {
	st := d.s.st
	set, err := d.s.Settings(ctx)
	if err != nil {
		return err
	}
	on, due := dailySummarySettings(set)
	if !on {
		return nil
	}
	due = max(due, dailySummaryGrace)
	zone := d.s.storeZone(set, d.skipZone)
	loc, err := loadZone(zone)
	if err != nil {
		return fmt.Errorf("daily summary: %w", err)
	}
	now := d.now().In(loc)
	if minuteOfDay(now) < due {
		return nil
	}
	since, found, err := st.SettingChangedAt(ctx, st.Conn(), SettingDailySummary)
	if err != nil {
		return err
	}
	if !found || !switchedOnBefore(since.In(loc), now, due) {
		return nil
	}
	day := dateOf(now).AddDate(0, 0, -1)
	date := day.Format(dateLayout)
	if f := d.failed; f.day == date && (f.n >= dailySummaryMaxAttempts || now.Before(f.until)) {
		return nil // waiting to try again, or given up on this day
	}
	if claimed, err := st.DailySummaryClaimed(ctx, st.Conn(), date); err != nil || claimed {
		return err
	}
	// Nobody to tell: nothing is claimed, so whoever is added later today
	// still gets it. (The list is read again in the claim's transaction.)
	staff, err := st.ListStaffContacts(ctx, st.Conn(), true)
	if err != nil || len(d.readers(staff)) == 0 {
		return err
	}
	if err := d.queue(ctx, day, zone, now); err != nil {
		return d.fail(date, now, err)
	}
	d.failed = summaryFailures{}
	return nil
}

// fail records a failed attempt on date and says when the next one may start.
func (d *DailySummary) fail(date string, now time.Time, err error) error {
	if d.failed.day != date {
		d.failed = summaryFailures{day: date}
	}
	d.failed.n++
	wait := min(dailySummaryRetryFirst<<(d.failed.n-1), dailySummaryRetryMax)
	d.failed.until = now.Add(wait)
	if d.failed.n >= dailySummaryMaxAttempts {
		return fmt.Errorf("daily summary of %s: attempt %d of %d failed, %w: %w", date, d.failed.n, dailySummaryMaxAttempts, errSummaryGaveUp, err)
	}
	return fmt.Errorf("daily summary of %s: attempt %d of %d failed, next one in %s: %w", date, d.failed.n, dailySummaryMaxAttempts, wait, err)
}

// queue reads the figures of day (a store-local date, zone its zone) and writes
// the claim and one event for each reader, in one transaction.
func (d *DailySummary) queue(ctx context.Context, day time.Time, zone string, now time.Time) error {
	st := d.s.st
	date := day.Format(dateLayout)
	// Yesterday and the day before, in one report: the second day is only for
	// the comparison.
	rng, err := NewReportRange(ReportRequest{From: day.AddDate(0, 0, -1).Format(dateLayout), To: date, Group: GroupDay, TZ: zone}, zone, now)
	if err != nil {
		return err
	}
	rep, err := d.s.SalesReport(ctx, rng)
	if err != nil {
		return err
	}
	ev, err := summaryEvent(date, zone, rep)
	if err != nil {
		return err
	}
	var (
		queued   int
		lastZone string
		hadLast  bool
	)
	err = st.WithTx(ctx, func(tx pgx.Tx) error {
		queued, lastZone, hadLast = 0, "", false
		// Who may read the reports is decided here, in the transaction that
		// claims the day: a staff member who lost the permission before it
		// commits is not told.
		staff, err := st.ListStaffContacts(ctx, tx, true)
		if err != nil {
			return err
		}
		to := d.readers(staff)
		if len(to) == 0 {
			return nil
		}
		if lastZone, hadLast, err = st.LastDailySummaryZone(ctx, tx); err != nil {
			return err
		}
		if won, err := st.ClaimDailySummary(ctx, tx, date, zone, len(to)); err != nil || !won {
			return err
		}
		for _, u := range to {
			e := ev
			e.TelegramID, e.Language = u.TelegramID, i18n.Normalize(u.Language)
			if err := d.s.publish(ctx, tx, events.DailySummary, e); err != nil {
				return err
			}
		}
		queued = len(to)
		return nil
	})
	if err != nil {
		return err
	}
	if queued > 0 {
		d.log.Info("daily summary queued", "day", date, "zone", zone, "recipients", queued, "currencies", len(ev.Currencies))
		if hadLast && lastZone != zone {
			// The day edges moved with the zone: a day can overlap the last
			// summary's (a sale in both) or leave a gap after it (a sale in none).
			d.log.Warn("daily summary: the store's time zone changed since the last summary; a sale may be in two summaries or in none around the change",
				"from", lastZone, "to", zone, "day", date)
		}
	}
	return nil
}

// readers keeps the staff who hold reports.read and have a Telegram account:
// the ones the summary is for.
func (d *DailySummary) readers(staff []store.User) []store.User {
	var out []store.User
	for _, u := range staff {
		if u.TelegramID > 0 && d.canRead(u.Role) {
			out = append(out, u)
		}
	}
	return out
}

func (d *DailySummary) skipZone(source, zone string) {
	key := source + "=" + zone
	if d.skipped[key] {
		return
	}
	d.skipped[key] = true
	d.log.Warn("daily summary: not a time zone, skipped", "source", source, "value", zone)
}

// minuteOfDay is the minutes since midnight on t's own clock.
func minuteOfDay(t time.Time) int { return t.Hour()*60 + t.Minute() }

// switchedOnBefore reports whether the feature was switched on (since) at or
// before the time of day due on the local day now is on: both in the store's
// zone. A feature switched on up to its time of day sends today; one switched on
// after it starts the next day.
func switchedOnBefore(since, now time.Time, due int) bool {
	if sd, nd := dateOf(since), dateOf(now); !sd.Equal(nd) {
		return sd.Before(nd)
	}
	return minuteOfDay(since) <= due
}

// summaryEvent is the event for the day (a local date) from a report of that
// day and the day before. It has no recipient yet. A currency is in it when
// something happened to it that day; the rest of the sales report (cancelled
// orders, the plans, how orders were paid) stays on the Reports page.
func summaryEvent(day, zone string, rep *SalesReport) (events.DailySummaryEvent, error) {
	if len(rep.Buckets) != 2 || rep.Buckets[1].Date != day {
		return events.DailySummaryEvent{}, fmt.Errorf("daily summary: the report of %s has %d buckets, want that day and the one before", day, len(rep.Buckets))
	}
	before, that := rep.Buckets[0], rep.Buckets[1]
	e := events.DailySummaryEvent{Date: day, Zone: zone, NewCustomers: that.NewCustomers, Trials: that.Trials}
	for _, cur := range rep.Currencies {
		c, prev := that.Currencies[cur], before.Currencies[cur]
		if c == nil || prev == nil || !worthTelling(c) {
			continue
		}
		e.Currencies = append(e.Currencies, events.DailySummaryCurrency{
			Currency: cur, Revenue: c.Revenue, PrevRevenue: prev.Revenue,
			Orders: c.Orders, NewOrders: c.NewOrders, Renewals: c.Renewals, TrafficTopups: c.TrafficTopups,
			Licenses: events.DailySummaryFlow(c.Licenses), Digital: events.DailySummaryFlow(c.Digital),
			Refunds: events.DailySummaryFlow(c.Refunds), Net: Net(c), Topups: events.DailySummaryFlow(c.Topups),
			Rejected: events.DailySummaryFlow(c.Rejected), GatewayFailed: events.DailySummaryFlow(c.GatewayFailed),
		})
	}
	return e, nil
}

// worthTelling: the day did something in this currency that the message shows.
func worthTelling(c *store.ReportCell) bool {
	return c.Revenue != 0 || c.Orders > 0 || c.Refunds.Count > 0 || c.Topups.Count > 0 || c.Rejected.Count > 0 || c.GatewayFailed.Count > 0
}
