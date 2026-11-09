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
	// CreateClient is idempotent per subscriptionID: a retry stays on the
	// server the first attempt picked. candidates are the servers it may go
	// to (a plan's servers; empty = any enabled server). inbounds are the
	// plan's choice of panel inbounds per server (empty = the provisioner's
	// default for the server it picks). group is the 3x-ui client group the
	// client is put in ("" is the default group, "BOBRES").
	// firstUse: the durationDays start at the client's first connection (first_use.go); the client's ExpiresAt is then
	// negative.
	CreateClient(ctx context.Context, subscriptionID, email string, durationDays, trafficBytes int64, candidates []string, inbounds []store.PlanInbound, group string, firstUse bool) (ProvisionedClient, error)
	// SetClientGroups changes the group clients should be in ("" is the default
	// group): a reseller's clients made before they had a group move into it. It
	// returns how many subscriptions the provisioner held.
	SetClientGroups(ctx context.Context, items []ClientGroup) (int, error)
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
	// ReleaseClient forgets the server reservation of a client that was never made, so that the next CreateClient
	// picks a server again (MoveOrder). It is refused for a client that was made.
	ReleaseClient(ctx context.Context, subscriptionID string) (Release, error)
	// RecreateClient makes a client that is gone from its panel again (an operator deleted it there):
	// on the same server, in the same inbounds and group, with these absolute limits (unix seconds,
	// bytes; 0 = unlimited). It changes nothing when the panel has the client.
	RecreateClient(ctx context.Context, subscriptionID string, expiresAt, trafficBytes int64) error
	NameChecker
	// RenameClient renames the client on its panel, keeping its subscription link. ErrClientNameTaken when another
	// client has the name, ErrNotRenamable when the server's panel cannot keep the link.
	RenameClient(ctx context.Context, subscriptionID, name string) error
}

// Release is what ReleaseClient did: Released is false when there was no reservation; Leftover is true when the old
// panel could not be asked and may keep a client nothing points to; ServerID is the server the reservation was on.
type Release struct {
	Released, Leftover bool
	ServerID           string
}

// NameChecker asks the provisioner whether a config name is free on every panel (config_names.go).
type NameChecker interface {
	CheckClientName(ctx context.Context, name string) (NameCheck, error)
}

// NameCheck is the provisioner's answer: Available, or Reason "taken" or "unchecked" (a server could not be asked).
type NameCheck struct {
	Available bool
	Reason    string
}

// ClientGroup is the group a subscription's client should be in.
type ClientGroup struct {
	SubscriptionID string
	Group          string // "" is the default group
}

// Usage is a subscription's traffic and limits as the panel holds them.
type Usage struct {
	UsedBytes  int64
	TotalBytes int64 // 0 = unlimited
	ExpiresAt  int64 // unix seconds; 0 = none; negative = starts on first use
	Enabled    bool
	// Missing: the panel answered and has no such client (an operator deleted it there). The other
	// fields are empty then.
	Missing bool
}

// ProvisionedClient is what the panel holds for a subscription.
type ProvisionedClient struct {
	ServerID  string
	XUISubID  string
	ExpiresAt int64 // unix seconds, 0 = no expiry, negative = waits for its first connection, then runs that long
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

// SettleBulks announces the bulk purchases whose services have come to a
// rest (SettleBulks of the service, with this worker's alert threshold: an
// order that failed that many attempts counts as delayed, not as on its way).
func (w *ProvisionWorker) SettleBulks(ctx context.Context) (int, error) {
	return w.svc.SettleBulks(ctx, w.AlertAfter)
}

// Run provisions until ctx is done. Errors are logged; they never stop the loop.
// After each step it also looks for a bulk purchase whose services are all
// done, whose one message with every link is then announced.
func (w *ProvisionWorker) Run(ctx context.Context) error {
	for {
		worked, err := w.ProvisionNext(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.log.Warn("provisioning step failed", "err", err)
		}
		if _, err := w.SettleBulks(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("announcing bulk purchases failed", "err", err)
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
	group   string // the 3x-ui client group a new client goes in ("" is the default)
	prefix  string // a reseller's config prefix: the automatic name is "<prefix>-u<id>-<random>"
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
		var (
			sub    *store.Subscription
			group  string // the panel group a new client goes in
			prefix string // the reseller's prefix of its automatic name
		)
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
				// A renewal bought with a plan restarts the time from now; a staff member's extension adds to the time left.
				restart := o.ExtendDays == nil && o.ExtendBytes == nil
				o.TargetExpiresAt, o.TargetTrafficBytes, o.TargetFirstUseDays = extendBy(o.Type, sub, days, bytes, w.now(), restart)
				o.TargetsSet = true
				if err := st.SetOrderTargets(ctx, tx, o.ID, o.TargetExpiresAt, o.TargetTrafficBytes, o.TargetFirstUseDays); err != nil {
					return err
				}
			}
		} else {
			// A reseller's configs carry their prefix and go in their group (the owner's rules for 3x-ui); a customer's
			// are named as ever and go in the default group.
			naming, err := st.GetResellerNaming(ctx, tx, o.UserID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if naming == nil {
				naming = &store.ResellerNaming{}
			}
			group, prefix = PanelGroupOf(naming.PanelGroup), naming.ConfigPrefix
			sub, err = st.SubscriptionByOrder(ctx, tx, o.ID)
			if errors.Is(err, store.ErrNotFound) {
				// The config name the buyer chose, unless another service took it since the order was made: then the
				// automatic name (the customer is told when it is delivered).
				email := clientEmailFor(prefix, u.TelegramID)
				if o.ClientName != nil {
					taken, err := st.ClientNameTaken(ctx, tx, *o.ClientName, o.UserID, "")
					if err != nil {
						return err
					}
					if !taken {
						email = *o.ClientName
					}
				}
				sub, err = st.CreateSubscription(ctx, tx, &store.Subscription{
					UserID: o.UserID, OrderID: o.ID, ClientEmail: email, Label: o.Label,
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
		job = &provisionJob{order: o, user: u, plan: plan, sub: sub, attempt: attempt, group: group, prefix: prefix}
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
	firstUse := w.startsOnFirstUse(ctx, job)
	client, err := w.prov.CreateClient(ctx, job.sub.ID, job.sub.ClientEmail, days, traffic, candidates(job), job.plan.Inbounds, job.group, firstUse)
	if errors.Is(err, ErrClientNameTaken) && job.order.ClientName != nil && job.sub.ClientEmail == *job.order.ClientName {
		// The panel has a client of the name the buyer chose (made there since it was checked): the service gets the
		// automatic name instead and is delivered, and the customer is told. A paid order never fails for its name.
		auto := clientEmailFor(job.prefix, job.user.TelegramID)
		if err := w.svc.st.SetSubscriptionClientEmail(ctx, w.svc.st.Conn(), job.sub.ID, auto); err != nil {
			return true, w.fail(ctx, job, err)
		}
		w.log.Info("the config name chosen was taken on the panel; the automatic name is used", "order_id", job.order.ID)
		job.sub.ClientEmail = auto
		client, err = w.prov.CreateClient(ctx, job.sub.ID, auto, days, traffic, candidates(job), job.plan.Inbounds, job.group, firstUse)
	}
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

// candidates are the servers a new service may go to: the one staff chose for the order (MoveOrder), else the plan's.
func candidates(job *provisionJob) []string {
	if job.order.ServerID != nil && *job.order.ServerID != "" {
		return []string{*job.order.ServerID}
	}
	return job.plan.ServerIDs
}

// late reports a delivery the customer was told is delayed: an attempt after the one that announced the failure
// (AlertAfter, ProvisionFailed). Its message says sorry for the wait. A service of a bulk purchase is not: the bulk's
// one message says how its services are.
func (w *ProvisionWorker) late(job *provisionJob) bool {
	return job.attempt > w.AlertAfter && job.order.BulkID == nil
}

func (w *ProvisionWorker) succeed(ctx context.Context, job *provisionJob, c ProvisionedClient, links Links, traffic int64) error {
	st := w.svc.st
	// What the panel holds decides (not the switch): a client an earlier attempt made the other way is adopted as it is.
	expiresAt, firstUseDays := panelExpiry(c.ExpiresAt)
	var total *int64
	if traffic > 0 {
		total = &traffic
	}
	return st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := st.ActivateSubscription(ctx, tx, job.sub.ID, c.ServerID, c.XUISubID, links.SubscriptionLink, expiresAt, total, firstUseDays); err != nil {
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
		// A service of a bulk purchase whose one message is still to come is
		// not announced on its own: the bulk's message carries its link.
		bulkID, err := w.svc.bulkIDForEvent(ctx, tx, job.order)
		if err != nil {
			return err
		}
		if err := w.svc.issueReward(ctx, tx, job.order, job.plan, job.user); err != nil {
			return err
		}
		if err := w.svc.afterDelivery(ctx, tx, job.order, job.plan, bulkID); err != nil {
			return err
		}
		e := events.SubscriptionProvisionedEvent{
			SubscriptionID: job.sub.ID, OrderID: job.order.ID, UserID: job.user.ID, TelegramID: job.user.TelegramID,
			SubscriptionLink: links.SubscriptionLink, ExpiresAt: max(c.ExpiresAt, 0), TrafficBytes: traffic, Label: derefStr(job.sub.Label),
			BulkID: bulkID, Late: w.late(job), FirstUseDays: deref32(firstUseDays),
		}
		if chosen := derefStr(job.order.ClientName); chosen != "" {
			if job.sub.ClientEmail == chosen {
				e.ClientName = chosen
			} else {
				e.NameTaken = chosen
			}
		}
		return w.svc.publish(ctx, tx, events.SubscriptionProvisioned, e)
	})
}

// extend applies a renewal's or top-up's target limits on the panel, then
// records them on the subscription and announces the result.
func (w *ProvisionWorker) extend(ctx context.Context, job *provisionJob) error {
	o := job.order
	var expires, traffic int64
	switch {
	case o.TargetExpiresAt != nil:
		expires = o.TargetExpiresAt.Unix()
	case o.TargetFirstUseDays != nil:
		expires = waitingExpiry(*o.TargetFirstUseDays) // still waiting for its first connection, for longer
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
		if err := st.ApplySubscriptionLimits(ctx, tx, job.sub.ID, o.TargetExpiresAt, o.TargetTrafficBytes, o.TargetFirstUseDays); err != nil {
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
		if err := w.svc.issueReward(ctx, tx, o, job.plan, job.user); err != nil {
			return err
		}
		if err := w.svc.afterDelivery(ctx, tx, o, job.plan, ""); err != nil {
			return err
		}
		return w.svc.publish(ctx, tx, events.SubscriptionExtended, events.SubscriptionExtendedEvent{
			SubscriptionID: job.sub.ID, OrderID: o.ID, UserID: job.user.ID, TelegramID: job.user.TelegramID,
			Type: o.Type, ExpiresAt: max(expires, 0), TrafficTotalBytes: traffic, TrafficUsedBytes: job.sub.TrafficUsed,
			Label: derefStr(job.sub.Label), Late: w.late(job), FirstUseDays: deref32(o.TargetFirstUseDays),
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
			Attempts: attempts, Error: reason, BulkID: derefStr(job.order.BulkID),
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
