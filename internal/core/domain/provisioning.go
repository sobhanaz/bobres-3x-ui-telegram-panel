package domain

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Provisioner is core's view of the provisioner service.
type Provisioner interface {
	// CreateClient is idempotent per subscriptionID.
	CreateClient(ctx context.Context, subscriptionID, email string, durationDays, trafficBytes int64) (ProvisionedClient, error)
	GetLinks(ctx context.Context, subscriptionID string) (Links, error)
	Health(ctx context.Context) (healthy bool, detail string, err error)
	// SetLimits sets absolute limits (unix seconds, bytes; 0 = unlimited) and
	// enables the client: renewals and top-ups, safe to repeat.
	SetLimits(ctx context.Context, subscriptionID string, expiresAt, trafficBytes int64) error
	// Usage reports used traffic and the limits the panel holds.
	Usage(ctx context.Context, subscriptionID string) (Usage, error)
	// SetEnabled turns the client on or off on the panel (its limits stay).
	SetEnabled(ctx context.Context, subscriptionID string, enabled bool) error
	// ResetTraffic zeroes the client's used traffic.
	ResetTraffic(ctx context.Context, subscriptionID string) error
	// Delete removes the client from the panel; deleting it again is fine.
	Delete(ctx context.Context, subscriptionID string) error
}

// Usage is a subscription's traffic and limits as the panel holds them.
type Usage struct {
	UsedBytes  int64
	TotalBytes int64 // 0 = unlimited
	ExpiresAt  int64 // unix seconds; 0 = none; negative = starts on first use
	Enabled    bool
}

// ProvisionedClient is what the panel holds for a subscription.
type ProvisionedClient struct {
	ServerID  string
	XUISubID  string
	ExpiresAt int64 // unix seconds, 0 = no expiry
}

// Links are what the user needs to connect.
type Links struct {
	SubscriptionLink string
	QRPNG            []byte
	ConfigLinks      []string
}

// kickProvisioning wakes the worker after an order became paid, so a wallet
// purchase is delivered in about a second instead of after the next poll.
func (s *Service) kickProvisioning() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// ProvisionWorker turns paid orders into VPN accounts.
type ProvisionWorker struct {
	svc  *Service
	prov Provisioner
	log  *slog.Logger
	// Interval is the idle poll interval.
	Interval time.Duration
	// StaleAfter is the provisioning lease: an order stuck in "provisioning"
	// longer than this (worker crashed mid-call) is claimed again.
	StaleAfter time.Duration
	// AlertAfter is the attempt at which provision_failed is announced (once).
	AlertAfter int
	now        func() time.Time
}

// NewProvisionWorker builds a worker with production defaults.
func (s *Service) NewProvisionWorker(prov Provisioner, log *slog.Logger) *ProvisionWorker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &ProvisionWorker{
		svc: s, prov: prov, log: log.With("worker", "provisioning"),
		Interval: 3 * time.Second, StaleAfter: 2 * time.Minute, AlertAfter: 3, now: time.Now,
	}
}

// Run provisions until ctx is done. Errors are logged; they never stop the loop.
func (w *ProvisionWorker) Run(ctx context.Context) error {
	for {
		worked, err := w.ProvisionNext(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.log.Warn("provisioning step failed", "err", err)
		}
		if worked {
			continue // there may be more
		}
		t := time.NewTimer(w.Interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-w.svc.kick:
			t.Stop()
		case <-t.C:
		}
	}
}

type provisionJob struct {
	order   *store.Order
	user    *store.User
	plan    *store.Plan
	sub     *store.Subscription
	attempt int    // this run's attempt number
	blocked string // why the order cannot be applied now (no panel call)
}

// ProvisionNext handles at most one order. It reports whether there was one.
func (w *ProvisionWorker) ProvisionNext(ctx context.Context) (bool, error) {
	st := w.svc.st
	var job *provisionJob
	err := st.WithTx(ctx, func(tx pgx.Tx) error {
		o, err := st.ClaimDueOrder(ctx, tx, w.StaleAfter)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		u, err := st.GetUser(ctx, tx, o.UserID)
		if err != nil {
			return err
		}
		plan, err := st.GetPlan(ctx, tx, o.PlanID)
		if err != nil {
			return err
		}
		var sub *store.Subscription
		if o.SubscriptionID != nil {
			// A renewal or top-up: its limits are computed once, from the
			// subscription as it was when first claimed, and kept for retries.
			if sub, err = st.LockSubscription(ctx, tx, *o.SubscriptionID); err != nil {
				return err
			}
			if sub.Status == "disabled" || sub.Status == "deleted" {
				// Paid before staff turned the service off: it waits (and is
				// retried) until the service is on again or the order refunded.
				attempt, err := st.MarkProvisioning(ctx, tx, o.ID)
				if err != nil {
					return err
				}
				job = &provisionJob{order: o, user: u, plan: plan, sub: sub, attempt: attempt, blocked: "the service is " + sub.Status}
				return nil
			}
			if !o.TargetsSet {
				days, bytes := orderLimits(o, plan)
				o.TargetExpiresAt, o.TargetTrafficBytes = extendBy(o.Type, sub, days, bytes, w.now())
				o.TargetsSet = true
				if err := st.SetOrderTargets(ctx, tx, o.ID, o.TargetExpiresAt, o.TargetTrafficBytes); err != nil {
					return err
				}
			}
		} else {
			sub, err = st.SubscriptionByOrder(ctx, tx, o.ID)
			if errors.Is(err, store.ErrNotFound) {
				sub, err = st.CreateSubscription(ctx, tx, &store.Subscription{
					UserID: o.UserID, OrderID: o.ID, ClientEmail: clientEmail(u.TelegramID),
				})
			}
			if err != nil {
				return err
			}
		}
		attempt, err := st.MarkProvisioning(ctx, tx, o.ID)
		if err != nil {
			return err
		}
		job = &provisionJob{order: o, user: u, plan: plan, sub: sub, attempt: attempt}
		return nil
	})
	if err != nil || job == nil {
		return false, err
	}
	if job.blocked != "" {
		return true, w.fail(ctx, job, errors.New(job.blocked))
	}

	if job.order.SubscriptionID != nil {
		return true, w.extend(ctx, job)
	}
	days, traffic := planLimits(job.plan)
	client, err := w.prov.CreateClient(ctx, job.sub.ID, job.sub.ClientEmail, days, traffic)
	var links Links
	if err == nil {
		links, err = w.prov.GetLinks(ctx, job.sub.ID)
	}
	if err != nil {
		if ctx.Err() != nil {
			return true, err // shutting down: the lease expires and a later run retries
		}
		return true, w.fail(ctx, job, err)
	}
	return true, w.succeed(ctx, job, client, links, traffic)
}

func (w *ProvisionWorker) succeed(ctx context.Context, job *provisionJob, c ProvisionedClient, links Links, traffic int64) error {
	st := w.svc.st
	var expiresAt *time.Time
	if c.ExpiresAt > 0 {
		t := time.Unix(c.ExpiresAt, 0).UTC()
		expiresAt = &t
	}
	var total *int64
	if traffic > 0 {
		total = &traffic
	}
	return st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := st.ActivateSubscription(ctx, tx, job.sub.ID, c.ServerID, c.XUISubID, links.SubscriptionLink, expiresAt, total); err != nil {
			return err
		}
		ok, err := st.TransitionOrder(ctx, tx, job.order.ID, []string{"provisioning"}, "active")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("order %s changed state while provisioning", job.order.ID)
		}
		w.log.Info("provisioned", "order_id", job.order.ID, "subscription_id", job.sub.ID)
		return w.svc.publish(ctx, tx, events.SubscriptionProvisioned, events.SubscriptionProvisionedEvent{
			SubscriptionID: job.sub.ID, OrderID: job.order.ID, UserID: job.user.ID, TelegramID: job.user.TelegramID,
			SubscriptionLink: links.SubscriptionLink, ExpiresAt: c.ExpiresAt, TrafficBytes: traffic,
		})
	})
}

// extend applies a renewal's or top-up's target limits on the panel, then
// records them on the subscription and announces the result.
func (w *ProvisionWorker) extend(ctx context.Context, job *provisionJob) error {
	o := job.order
	var expires, traffic int64
	if o.TargetExpiresAt != nil {
		expires = o.TargetExpiresAt.Unix()
	}
	if o.TargetTrafficBytes != nil {
		traffic = *o.TargetTrafficBytes
	}
	if err := w.prov.SetLimits(ctx, job.sub.ID, expires, traffic); err != nil {
		if ctx.Err() != nil {
			return err // shutting down: the lease expires and a later run retries
		}
		return w.fail(ctx, job, err)
	}
	st := w.svc.st
	return st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := st.ApplySubscriptionLimits(ctx, tx, job.sub.ID, o.TargetExpiresAt, o.TargetTrafficBytes); err != nil {
			return err
		}
		ok, err := st.TransitionOrder(ctx, tx, o.ID, []string{"provisioning"}, "active")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("order %s changed state while extending", o.ID)
		}
		w.log.Info("extended", "order_id", o.ID, "subscription_id", job.sub.ID, "type", o.Type)
		return w.svc.publish(ctx, tx, events.SubscriptionExtended, events.SubscriptionExtendedEvent{
			SubscriptionID: job.sub.ID, OrderID: o.ID, UserID: job.user.ID, TelegramID: job.user.TelegramID,
			Type: o.Type, ExpiresAt: expires, TrafficTotalBytes: traffic, TrafficUsedBytes: job.sub.TrafficUsed,
		})
	})
}

// provisionBackoff is the wait before attempt n+1 (capped at 30 minutes).
func provisionBackoff(attempt int) time.Duration {
	steps := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute}
	if attempt >= 1 && attempt <= len(steps) {
		return steps[attempt-1]
	}
	return 30 * time.Minute
}

func (w *ProvisionWorker) fail(ctx context.Context, job *provisionJob, cause error) error {
	st := w.svc.st
	reason := cause.Error()
	return st.WithTx(ctx, func(tx pgx.Tx) error {
		attempts, err := st.RecordProvisionFailure(ctx, tx, job.order.ID, reason, w.now().Add(provisionBackoff(job.attempt)))
		if err != nil {
			return err
		}
		w.log.Warn("provisioning failed; will retry", "order_id", job.order.ID, "attempt", attempts, "err", cause)
		if attempts != w.AlertAfter {
			return nil
		}
		return w.svc.publish(ctx, tx, events.ProvisionFailed, events.ProvisionFailedEvent{
			OrderID: job.order.ID, UserID: job.user.ID, TelegramID: job.user.TelegramID,
			Attempts: attempts, Error: reason,
		})
	})
}

// planLimits converts a plan into panel limits (0 = unlimited).
func planLimits(p *store.Plan) (days, traffic int64) {
	if p.DurationDays != nil && *p.DurationDays > 0 {
		days = int64(*p.DurationDays)
	}
	if p.TrafficBytes != nil && *p.TrafficBytes > 0 {
		traffic = *p.TrafficBytes
	}
	return days, traffic
}

// clientEmail is the panel's client key: readable for the operator (Telegram
// id) and unique (random suffix).
func clientEmail(telegramID int64) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(alphabet[int(c)%len(alphabet)])
	}
	return fmt.Sprintf("u%d-%s", telegramID, sb.String())
}
