package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// config is one load run.
type config struct {
	Users    int           // simulated customers at the same time
	Duration time.Duration // how long new journeys are started
	Think    time.Duration // pause between a customer's bot updates, give or take half (0: none)
	Timeout  time.Duration // per call
	Owner    int64         // Telegram id of the owner (or an admin) who tops wallets up
	TGBase   int64         // simulated customers get Telegram ids above this
}

// price is the load test plan's price: each journey's top-up buys it once.
const price = 10000

// runner drives core as the bot does, with many customers at once.
type runner struct {
	cfg  config
	core corev1.CoreServiceClient
	plan *corev1.Plan
	run  string       // makes idempotency keys unique to this run
	seq  atomic.Int64 // the last customer's number
}

// newRunner makes sure the owner exists and adds an enabled plan to buy.
// Call close afterwards: it switches the plan off again.
func newRunner(ctx context.Context, core corev1.CoreServiceClient, cfg config) (*runner, error) {
	if cfg.Users < 1 || cfg.Duration <= 0 || cfg.Timeout <= 0 || cfg.Owner == 0 {
		return nil, errors.New("users, duration, timeout and owner are required")
	}
	_, err := core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: cfg.Owner}})
	if status.Code(err) == codes.NotFound {
		// A fresh stack: the owner's first contact makes them the owner.
		_, err = core.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: cfg.Owner, Language: "en"})
	}
	if err != nil {
		return nil, fmt.Errorf("owner %d: %w", cfg.Owner, err)
	}
	plan, err := core.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: cfg.Owner, Plan: &corev1.Plan{
		NameI18N: map[string]string{"en": "Load test", "fa": "تست بار"}, Kind: "both",
		DurationDays: 30, TrafficBytes: 10 << 30, Price: &commonv1.Money{Amount: price, Currency: "IRT"}, Enabled: true,
	}})
	if err != nil {
		return nil, fmt.Errorf("add the load test plan: %w", err)
	}
	run := fmt.Sprintf("%08x", rand.Uint32()) //nolint:gosec // tells runs apart, not a secret
	return &runner{cfg: cfg, core: core, plan: plan, run: run}, nil
}

// close switches the load test plan off, so a stack that is kept does not
// offer it to anyone.
func (r *runner) close(ctx context.Context) error {
	off := r.plan
	off.Enabled = false
	_, err := r.core.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: r.cfg.Owner, Plan: off})
	return err
}

// start runs cfg.Users customers at once, each starting journey after
// journey until cfg.Duration has passed, then waits for the journeys in
// flight.
func (r *runner) start(ctx context.Context) result {
	recs := make([]*recorder, r.cfg.Users)
	begin := time.Now()
	stop := begin.Add(r.cfg.Duration)
	var wg sync.WaitGroup
	for i := range recs {
		rec := newRecorder()
		recs[i] = rec
		wg.Go(func() {
			if r.cfg.Think > 0 { // customers arrive spread out, not all in the same second
				sleep(ctx, rand.N(r.cfg.Think)) //nolint:gosec // timing jitter, not a secret
			}
			for ctx.Err() == nil && time.Now().Before(stop) {
				t := time.Now()
				err := r.journey(ctx, rec)
				rec.add(journeyName, time.Since(t), err)
			}
		})
	}
	wg.Wait()
	res := result{Users: r.cfg.Users, Duration: r.cfg.Duration, Elapsed: time.Since(begin), Rec: newRecorder()}
	for _, rec := range recs {
		res.Rec.merge(rec)
	}
	return res
}

// journey is one new customer, update by update as the bot drives core:
// /start, the language tap that registers them, an admin's top-up, the plan
// list, the plan's page, paying from the wallet, their services and a
// support message. Every update starts with the bot loading its sender.
func (r *runner) journey(ctx context.Context, rec *recorder) error {
	tg := r.cfg.TGBase + r.seq.Add(1)
	key := fmt.Sprintf("lt:%s:%d", r.run, tg)
	v := &visit{ctx: ctx, r: r, rec: rec}
	var (
		u     *corev1.User
		order *corev1.Order
	)

	v.whoami(tg, codes.NotFound) // /start from someone new
	v.pause()
	v.whoami(tg, codes.NotFound) // the language tap registers them
	v.call("UpsertUser", func(ctx context.Context) (err error) {
		u, err = r.core.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: tg, Username: fmt.Sprintf("lt%d", tg), Language: "fa"})
		return err
	})

	v.pause()
	v.whoami(r.cfg.Owner) // an admin tops the wallet up with one plan's price
	v.call("AdminAdjustBalance", func(ctx context.Context) error {
		_, err := r.core.AdminAdjustBalance(ctx, &corev1.AdminAdjustBalanceRequest{ActorTelegramId: r.cfg.Owner, UserId: u.GetId(),
			Delta: &commonv1.Money{Amount: price, Currency: "IRT"}, Reason: "load test", IdempotencyKey: key + ":adj"})
		return err
	})

	v.pause()
	v.whoami(tg) // Buy: the plan list
	v.listPlans(u)
	v.pause()
	v.whoami(tg) // a plan's page, with the wallet balance
	v.listPlans(u)
	v.call("GetWallet", func(ctx context.Context) error {
		_, err := r.core.GetWallet(ctx, &corev1.GetWalletRequest{UserId: u.GetId(), Currency: "IRT"})
		return err
	})
	v.pause()
	v.whoami(tg) // Pay from the wallet
	v.listPlans(u)
	v.call("CreateOrder", func(ctx context.Context) (err error) {
		order, err = r.core.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: r.plan.GetId(),
			Type: "new", IdempotencyKey: key + ":ord"})
		return err
	})
	v.call("PayOrderWithWallet", func(ctx context.Context) error {
		_, err := r.core.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: order.GetId(), UserId: u.GetId()})
		return err
	})

	v.pause()
	v.whoami(tg) // My services
	v.call("ListSubscriptions", func(ctx context.Context) error {
		_, err := r.core.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: u.GetId()})
		return err
	})
	v.pause()
	v.whoami(tg) // a message to support opens a ticket
	v.call("AddTicketMessage", func(ctx context.Context) error {
		_, err := r.core.AddTicketMessage(ctx, &corev1.AddTicketMessageRequest{UserId: u.GetId(), Category: "other",
			Text: "load test: my service does not connect", TgMessageId: 1})
		return err
	})
	return v.err
}

// visit is one journey in progress. The first error ends it, as the bot
// shows the error and the customer stops there; later calls are skipped.
type visit struct {
	ctx context.Context
	r   *runner
	rec *recorder
	err error
}

// call times one RPC with the per-call timeout. want lists codes that are a
// normal answer, such as NOT_FOUND for someone new.
func (v *visit) call(name string, fn func(ctx context.Context) error, want ...codes.Code) {
	if v.err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(v.ctx, v.r.cfg.Timeout)
	defer cancel()
	t := time.Now()
	err := fn(ctx)
	if err != nil && slices.Contains(want, status.Code(err)) {
		err = nil
	}
	v.rec.add(name, time.Since(t), err)
	if err != nil {
		v.err = fmt.Errorf("%s: %w", name, err)
	}
}

func (v *visit) whoami(tg int64, want ...codes.Code) {
	v.call("GetUser", func(ctx context.Context) error {
		_, err := v.r.core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: tg}})
		return err
	}, want...)
}

func (v *visit) listPlans(u *corev1.User) {
	v.call("ListPlans", func(ctx context.Context) error {
		_, err := v.r.core.ListPlans(ctx, &corev1.ListPlansRequest{UserId: u.GetId()})
		return err
	})
}

// pause is the customer reading and tapping between two updates: think
// time, give or take half, so customers do not move in step.
func (v *visit) pause() {
	if v.err != nil || v.r.cfg.Think <= 0 {
		return
	}
	sleep(v.ctx, v.r.cfg.Think/2+rand.N(v.r.cfg.Think)) //nolint:gosec // timing jitter, not a secret
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
