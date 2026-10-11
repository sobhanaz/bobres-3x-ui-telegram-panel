package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// A renewal paid before staff turned the service off waits: the worker does
// not touch the panel, and it goes through once the service is on again.
func TestRenewalOfADisabledServiceWaits(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 10<<30)
	u, sub, fp, w := delivered(t, svc, s, 9101, plan)
	staff := seedUser(t, s, 9102)

	fund(t, s, u.ID, plan.Price+1) // the helper keys funds by amount; the first purchase used plan.Price
	ro, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, Type: OrderRenew, SubscriptionID: sub.ID, IdempotencyKey: "renew-off"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, ro.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSubscriptionEnabled(ctx, staff, fp, sub.ID, false, "abuse"); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("worker: %v %v", worked, err)
	}
	got, _ := s.GetOrder(ctx, s.Conn(), ro.ID)
	if got.Status != "provision_failed" || len(fp.limitsFor(sub.ID)) != 0 {
		t.Fatalf("renewal of a disabled service: %s, panel limits %v", got.Status, fp.limitsFor(sub.ID))
	}
	if err := svc.DeleteSubscription(ctx, staff, fp, sub.ID, "x"); !errors.Is(err, ErrState) {
		t.Fatalf("deleted a service with a renewal waiting: %v", err)
	}

	if _, err := svc.SetSubscriptionEnabled(ctx, staff, fp, sub.ID, true, "ok"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RetryOrder(ctx, staff, ro.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("worker after enabling: %v %v", worked, err)
	}
	if got, _ := s.GetOrder(ctx, s.Conn(), ro.ID); got.Status != "active" || len(fp.limitsFor(sub.ID)) != 1 {
		t.Fatalf("after enabling: %s, panel limits %v", got.Status, fp.limitsFor(sub.ID))
	}
	if ops := fp.opsDone(); len(ops) != 2 || ops[0] != sub.ID+"|disable" || ops[1] != sub.ID+"|enable" {
		t.Fatalf("panel calls: %v", ops)
	}
}

// What a staff extension may be, and that it is a free, already-paid order
// by the staff member that never rewards a referrer.
func TestExtendSubscriptionRules(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 10<<30)
	_, sub, _, _ := delivered(t, svc, s, 9201, plan)
	staff := seedUser(t, s, 9202)

	for name, p := range map[string]ExtendParams{
		"nothing":    {Reason: "r", IdempotencyKey: "k1"},
		"no reason":  {Days: 1, IdempotencyKey: "k2"},
		"no key":     {Days: 1, Reason: "r"},
		"too long":   {Days: 4000, Reason: "r", IdempotencyKey: "k3"},
		"negative":   {Bytes: -1, Reason: "r", IdempotencyKey: "k4"},
		"traffic up": {Bytes: maxExtendBytes + 1, Reason: "r", IdempotencyKey: "k5"},
	} {
		p.SubscriptionID = sub.ID
		if _, err := svc.ExtendSubscription(ctx, staff, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	o, err := svc.ExtendSubscription(ctx, staff, ExtendParams{SubscriptionID: sub.ID, Bytes: 1 << 30, Reason: "gift", IdempotencyKey: "k6"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Type != OrderTrafficTopup || o.Status != "paid" || o.Amount != 0 || o.CreatedBy == nil || *o.CreatedBy != staff.ID ||
		o.ExtendBytes == nil || *o.ExtendBytes != 1<<30 || o.ExtendDays != nil {
		t.Fatalf("extension order: %+v", o)
	}

	// An expired service needs days; a service without expiry takes no days.
	past := time.Now().Add(-time.Hour)
	total := int64(10 << 30)
	if err := s.ApplySubscriptionLimits(ctx, s.Conn(), sub.ID, &past, &total); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExtendSubscription(ctx, staff, ExtendParams{SubscriptionID: sub.ID, Bytes: 1 << 30, Reason: "r", IdempotencyKey: "k7"}); !errors.Is(err, ErrState) {
		t.Fatalf("traffic for an expired service: %v", err)
	}
	if err := s.ApplySubscriptionLimits(ctx, s.Conn(), sub.ID, nil, &total); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExtendSubscription(ctx, staff, ExtendParams{SubscriptionID: sub.ID, Days: 3, Reason: "r", IdempotencyKey: "k8"}); !errors.Is(err, ErrState) {
		t.Fatalf("days for a service that never expires: %v", err)
	}
	if err := s.SetSubscriptionStatus(ctx, s.Conn(), sub.ID, "deleted"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExtendSubscription(ctx, staff, ExtendParams{SubscriptionID: sub.ID, Bytes: 1, Reason: "r", IdempotencyKey: "k9"}); !errors.Is(err, ErrState) {
		t.Fatalf("extended a deleted service: %v", err)
	}
}

// orderLimits: a staff extension carries its own amounts, other orders their
// plan's.
func TestOrderLimits(t *testing.T) {
	days, traffic := int32(30), int64(50<<30)
	plan := &store.Plan{DurationDays: &days, TrafficBytes: &traffic}
	if d, b := orderLimits(&store.Order{}, plan); d != 30 || b != 50<<30 {
		t.Fatalf("plan order: %d %d", d, b)
	}
	extra := int64(5 << 30)
	if d, b := orderLimits(&store.Order{ExtendBytes: &extra}, plan); d != 0 || b != 5<<30 {
		t.Fatalf("traffic-only extension: %d %d", d, b)
	}
}
