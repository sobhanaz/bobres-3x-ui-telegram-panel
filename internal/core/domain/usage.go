package domain

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Reminder schedule: before expiry (largest first), and when this share of
// the traffic is used.
var (
	expiryWarnings     = []time.Duration{3 * 24 * time.Hour, 24 * time.Hour}
	trafficWarnPercent = int64(80)
	// An end older than this is recorded without a message (e.g. services
	// that ended before reminders existed).
	staleEnd = 3 * 24 * time.Hour
)

// UsageWorker keeps each subscription's usage and limits in step with the
// panel and sends the reminders: expiring soon, most traffic used, expired,
// all traffic used. Each reminder goes out once per period; a renewal or
// top-up starts a new period.
type UsageWorker struct {
	svc  *Service
	prov Provisioner
	log  *slog.Logger
	// Every is how often each subscription is synced.
	Every time.Duration
	// Tick is how often the worker looks for due subscriptions; Batch caps
	// how many it syncs per tick.
	Tick  time.Duration
	Batch int
	now   func() time.Time
}

// NewUsageWorker builds a worker with production defaults.
func (s *Service) NewUsageWorker(prov Provisioner, log *slog.Logger) *UsageWorker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &UsageWorker{svc: s, prov: prov, log: log.With("worker", "usage"),
		Every: 10 * time.Minute, Tick: 30 * time.Second, Batch: 50, now: time.Now}
}

// Run syncs until ctx is done. Errors are logged; they never stop the loop.
func (w *UsageWorker) Run(ctx context.Context) error {
	t := time.NewTicker(w.Tick)
	defer t.Stop()
	for {
		if _, err := w.SyncDue(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("usage sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// SyncDue syncs the subscriptions not synced within Every; it returns how
// many it looked at.
func (w *UsageWorker) SyncDue(ctx context.Context) (int, error) {
	st := w.svc.st
	due, err := st.DueUsageSync(ctx, st.Conn(), w.now().Add(-w.Every), w.Batch)
	if err != nil {
		return 0, err
	}
	topups := w.topupsOnSale(ctx)
	for i := range due {
		if ctx.Err() != nil {
			break
		}
		if err := w.sync(ctx, &due[i], topups); err != nil {
			w.log.Warn("subscription sync failed", "subscription_id", due[i].ID, "err", err)
		}
	}
	return len(due), nil
}

func (w *UsageWorker) topupsOnSale(ctx context.Context) bool {
	plans, err := w.svc.st.ListPlans(ctx, w.svc.st.Conn(), false)
	if err != nil {
		return false
	}
	for _, p := range plans {
		if p.IsTopup {
			return true
		}
	}
	return false
}

// sync reads one subscription from the panel and records it with whatever
// reminders are due. When the panel does not answer, the time-based
// reminders still go out from what core knows.
func (w *UsageWorker) sync(ctx context.Context, cached *store.Subscription, topups bool) error {
	u, uerr := w.prov.Usage(ctx, cached.ID)
	if uerr != nil {
		w.log.Warn("usage unavailable", "subscription_id", cached.ID, "err", uerr)
	}
	st := w.svc.st
	return st.WithTx(ctx, func(tx pgx.Tx) error {
		sub, err := st.LockSubscription(ctx, tx, cached.ID) // a renewal may have changed it meanwhile
		if err != nil {
			return err
		}
		switch sub.Status {
		case "active", "expiring_soon", "expired", "depleted":
		default:
			return nil
		}
		if uerr == nil {
			sub.TrafficUsed = u.UsedBytes
			// The panel's limits are what the customer has (an operator may
			// have changed them there). A start-on-first-use expiry is the
			// panel's business; core keeps its own date for it.
			switch {
			case u.ExpiresAt == 0:
				sub.ExpiresAt = nil
			case u.ExpiresAt > 0:
				t := time.Unix(u.ExpiresAt, 0).UTC()
				sub.ExpiresAt = &t
			}
			sub.TrafficTotal = nil
			if u.TotalBytes > 0 {
				total := u.TotalBytes
				sub.TrafficTotal = &total
			}
		}
		r, kind, days := reminderDue(sub, w.now())
		if err := st.RecordSync(ctx, tx, sub.ID, r); err != nil {
			return err
		}
		if kind == "" {
			return nil
		}
		user, err := st.GetUser(ctx, tx, sub.UserID)
		if err != nil {
			return err
		}
		e := events.SubscriptionReminderEvent{
			SubscriptionID: sub.ID, UserID: sub.UserID, TelegramID: user.TelegramID, Kind: kind, Days: days,
			TrafficUsedBytes: sub.TrafficUsed, TopupAvailable: topups && sub.TrafficTotal != nil,
		}
		if sub.ExpiresAt != nil {
			e.ExpiresAt = sub.ExpiresAt.Unix()
		}
		if sub.TrafficTotal != nil {
			e.TrafficTotalBytes = *sub.TrafficTotal
		}
		return w.svc.publish(ctx, tx, events.SubscriptionReminder, e)
	})
}

// reminderDue decides a subscription's status, its reminder state and the one
// reminder (if any) to send now. At most one reminder goes out per sync; an
// ended subscription gets its end message once, a running one gets each
// expiry warning once (3 days, then 1 day before) and the traffic warning
// once. A flag whose condition no longer holds (the operator extended the
// subscription in the panel) is cleared, so the next period is reminded too.
func reminderDue(sub *store.Subscription, now time.Time) (store.SubscriptionSync, string, int) {
	r := store.SubscriptionSync{Status: "active", TrafficUsed: sub.TrafficUsed, ExpiresAt: sub.ExpiresAt, TrafficTotal: sub.TrafficTotal,
		NotifiedExpiringAt: sub.NotifiedExpiringAt, NotifiedLowTrafficAt: sub.NotifiedLowTrafficAt, NotifiedEndedAt: sub.NotifiedEndedAt}
	expired := sub.ExpiresAt != nil && !now.Before(*sub.ExpiresAt)
	depleted := sub.TrafficTotal != nil && sub.TrafficUsed >= *sub.TrafficTotal
	if !expired && !depleted {
		r.NotifiedEndedAt = nil
	}
	if sub.ExpiresAt == nil || sub.ExpiresAt.Sub(now) > expiryWarnings[0] {
		r.NotifiedExpiringAt = nil
	}
	lowTraffic := sub.TrafficTotal != nil && *sub.TrafficTotal > 0 && sub.TrafficUsed*100 >= *sub.TrafficTotal*trafficWarnPercent
	if !lowTraffic {
		r.NotifiedLowTrafficAt = nil
	}
	if expired || depleted {
		kind := events.ReminderDepleted
		r.Status = "depleted"
		if expired {
			kind, r.Status = events.ReminderExpired, "expired"
		}
		if sub.NotifiedEndedAt != nil {
			return r, "", 0
		}
		r.NotifiedEndedAt = &now
		if expired && now.Sub(*sub.ExpiresAt) > staleEnd {
			return r, "", 0 // long over: record it, do not message out of the blue
		}
		return r, kind, 0
	}
	if sub.ExpiresAt != nil {
		left := sub.ExpiresAt.Sub(now)
		if left <= expiryWarnings[0] {
			r.Status = "expiring_soon"
		}
		for i := len(expiryWarnings) - 1; i >= 0; i-- { // the closest threshold reached
			mark := sub.ExpiresAt.Add(-expiryWarnings[i])
			if left > expiryWarnings[i] {
				continue
			}
			if sub.NotifiedExpiringAt == nil || sub.NotifiedExpiringAt.Before(mark) {
				r.NotifiedExpiringAt = &now
				days := int((left + 24*time.Hour - 1) / (24 * time.Hour))
				return r, events.ReminderExpiring, max(days, 1)
			}
			break
		}
	}
	if lowTraffic {
		r.Status = "expiring_soon"
		if sub.NotifiedLowTrafficAt == nil {
			r.NotifiedLowTrafficAt = &now
			return r, events.ReminderLowTraffic, 0
		}
	}
	return r, "", 0
}
