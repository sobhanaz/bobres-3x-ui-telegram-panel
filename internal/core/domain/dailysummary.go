package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Settings of the daily sales summary, and where the last day sent is kept.
const (
	settingDailySummary     = "notify.daily_summary"
	settingDailySummaryHour = "notify.daily_summary_hour"
	// dailySummaryMark holds the local date ("2006-01-02") of the last day
	// whose summary was published. It is internal state, not a setting the
	// dashboard shows (it has no SettingSpec), written in the same transaction
	// as the event so a restart never sends a day twice or skips it.
	dailySummaryMark = "state.daily_summary.last"
	// defaultDailySummaryHour is when the summary goes out when none is set.
	defaultDailySummaryHour = 9
)

// DailySummaryWorker publishes yesterday's sales once a day, after the
// configured hour of the store's day, when notify.daily_summary is on. The
// bot sends it to the staff who get alerts (sales.daily_summary.v1).
type DailySummaryWorker struct {
	s     *Service
	log   *slog.Logger
	every time.Duration
	now   func() time.Time
}

// NewDailySummaryWorker builds the worker (checking every minute).
func (s *Service) NewDailySummaryWorker(log *slog.Logger) *DailySummaryWorker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &DailySummaryWorker{s: s, log: log, every: time.Minute, now: time.Now}
}

// Run checks until ctx ends.
func (w *DailySummaryWorker) Run(ctx context.Context) error {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		if date, sent, err := w.Step(ctx); err != nil {
			w.log.Warn("daily summary", "err", err)
		} else if sent {
			w.log.Info("daily summary published", "date", date)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step publishes yesterday's summary when it is due and not yet sent. It
// reports the day it published, if any. When the worker was down for days, or
// the switch was off, only the last finished day is sent: older ones are not
// made up.
func (w *DailySummaryWorker) Step(ctx context.Context) (date string, sent bool, err error) {
	set, err := w.s.Settings(ctx)
	if err != nil {
		return "", false, err
	}
	if set[settingDailySummary] != "true" {
		return "", false, nil
	}
	hour := defaultDailySummaryHour
	if v := set[settingDailySummaryHour]; v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 && n <= 23 {
			hour = n
		}
	}
	now := w.now()
	today, err := w.s.ReportRange(ctx, ReportRequest{Range: RangeToday}, now)
	if err != nil {
		return "", false, err
	}
	loc, err := loadZone(today.TZ)
	if err != nil {
		return "", false, err
	}
	if now.In(loc).Hour() < hour {
		return "", false, nil
	}
	todayDate, err := time.Parse(dateLayout, today.To)
	if err != nil {
		return "", false, err
	}
	yesterday := todayDate.AddDate(0, 0, -1).Format(dateLayout)
	if last := w.last(set); last >= yesterday {
		return "", false, nil
	}
	rng, err := w.s.ReportRange(ctx, ReportRequest{From: yesterday, To: yesterday, TZ: today.TZ}, now)
	if err != nil {
		return "", false, err
	}
	rep, err := w.s.SalesReport(ctx, rng)
	if err != nil {
		return "", false, err
	}
	ev := summaryEvent(rep, yesterday)

	err = w.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		// One sender at a time, even where the mark row does not exist yet.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('core:daily-summary', 0))`); err != nil {
			return fmt.Errorf("daily summary lock: %w", err)
		}
		cur, err := w.s.st.LockSettings(ctx, tx, []string{dailySummaryMark})
		if err != nil {
			return err
		}
		sent = false
		if raw, ok := cur[dailySummaryMark]; ok {
			var v string
			if json.Unmarshal(raw, &v) == nil && v >= yesterday {
				return nil // another run sent it first
			}
		}
		if err := w.s.publish(ctx, tx, events.SalesDailySummary, ev); err != nil {
			return err
		}
		raw, _ := json.Marshal(yesterday)
		if err := w.s.st.SetSetting(ctx, tx, dailySummaryMark, raw); err != nil {
			return err
		}
		sent = true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return yesterday, sent, nil
}

// last is the date of the last summary published ("" when none).
func (w *DailySummaryWorker) last(set map[string]string) string { return set[dailySummaryMark] }

// summaryEvent is a one-day report as the event the bot sends.
func summaryEvent(rep *SalesReport, date string) events.DailySummaryEvent {
	ev := events.DailySummaryEvent{Date: date, TZ: rep.Range.TZ, NewCustomers: rep.Totals.NewCustomers, Trials: rep.Totals.Trials}
	for _, cur := range rep.Currencies { // sorted
		c := rep.Totals.Currencies[cur]
		ev.Currencies = append(ev.Currencies, events.DailySummaryCurrency{
			Currency: cur, Revenue: c.Revenue, Net: Net(c), Orders: c.Orders, NewOrders: c.NewOrders, Renewals: c.Renewals,
			Refunds: c.Refunds.Count, RefundAmount: c.Refunds.Amount, Topups: c.Topups.Count, TopupAmount: c.Topups.Amount,
		})
	}
	return ev
}
