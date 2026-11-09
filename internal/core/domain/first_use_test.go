package domain

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// firstUseOn switches days from the first connection on (or off) for customers and resellers.
func firstUseOn(t *testing.T, svc *Service, owner *store.User, customers, resellers bool) {
	t.Helper()
	b := map[bool]string{true: "true", false: "false"}
	if _, err := svc.SetSettings(context.Background(), owner, map[string]string{
		SettingFirstUseCustomers: b[customers], SettingFirstUseResellers: b[resellers],
	}); err != nil {
		t.Fatal(err)
	}
}

// With the switch on, a bought service waits for its first connection: the panel client is made that way, the service is
// active with no end and the days it will run, and the customer is told so. The sync keeps it waiting (no reminder about
// an end), a renewal adds its days to the wait (the panel gets the longer wait), a traffic package keeps it, and the
// panel setting the end at the first connection starts it like any other service.
func TestServiceWaitsForItsFirstConnection(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	firstUseOn(t, svc, owner(t, svc), true, false)
	plan := planWithLimits(t, s, 30, 50<<30)
	topup := topupPlan(t, s, 10<<30, 300)
	u, sub, fp, w := delivered(t, svc, s, 5801, plan)

	if len(fp.firstUse) != 1 || !fp.firstUse[0] || fp.days != 30 {
		t.Fatalf("the panel client: first use %v, days %d", fp.firstUse, fp.days)
	}
	if sub.Status != "active" || sub.ExpiresAt != nil || sub.FirstUseDays == nil || *sub.FirstUseDays != 30 {
		t.Fatalf("the service: %+v", sub)
	}
	var e events.SubscriptionProvisionedEvent
	if evs := outboxEvents(t, s, events.SubscriptionProvisioned); len(evs) != 1 || json.Unmarshal(evs[0], &e) != nil || e.FirstUseDays != 30 || e.ExpiresAt != 0 {
		t.Fatalf("the delivery message: %+v", e)
	}

	// The sync: still waiting, nothing to remind about its end.
	uw := svc.NewUsageWorker(fp, nil)
	uw.Every = -time.Hour
	fp.setUsage(sub.ID, Usage{TotalBytes: 50 << 30, ExpiresAt: -30 * 86400, Enabled: true})
	if _, err := uw.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetSubscription(ctx, s.Conn(), sub.ID)
	if got.Status != "active" || got.ExpiresAt != nil || got.FirstUseDays == nil || *got.FirstUseDays != 30 || len(outboxEvents(t, s, events.SubscriptionReminder)) != 0 {
		t.Fatalf("after a sync while waiting: %+v", got)
	}

	// A renewal adds its 30 days to the wait.
	fund(t, s, u.ID, plan.Price+1) // another amount: fund keys its credit by it
	ren, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, Type: OrderRenew, SubscriptionID: sub.ID, IdempotencyKey: "fu-renew"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, ren.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("renewal: %v %v", worked, err)
	}
	if calls := fp.limits[sub.ID]; len(calls) != 1 || calls[0].expires != -60*86400 || calls[0].traffic != 100<<30 {
		t.Fatalf("the panel after the renewal: %+v", calls)
	}
	got, _ = s.GetSubscription(ctx, s.Conn(), sub.ID)
	if got.ExpiresAt != nil || got.FirstUseDays == nil || *got.FirstUseDays != 60 {
		t.Fatalf("after the renewal: %+v", got)
	}
	var ext events.SubscriptionExtendedEvent
	if evs := outboxEvents(t, s, events.SubscriptionExtended); len(evs) != 1 || json.Unmarshal(evs[0], &ext) != nil || ext.FirstUseDays != 60 || ext.ExpiresAt != 0 {
		t.Fatalf("the renewal message: %+v", ext)
	}

	// A traffic package keeps the wait as it is.
	tp, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: topup.ID, Type: OrderTrafficTopup, SubscriptionID: sub.ID, IdempotencyKey: "fu-topup"})
	if err != nil {
		t.Fatal(err)
	}
	fund(t, s, u.ID, tp.Amount)
	if _, err := svc.PayOrderWithWallet(ctx, tp.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("traffic package: %v %v", worked, err)
	}
	if calls := fp.limits[sub.ID]; len(calls) != 2 || calls[1].expires != -60*86400 || calls[1].traffic != 110<<30 {
		t.Fatalf("the panel after the traffic package: %+v", calls)
	}

	// Staff can add days to a waiting service too; they go to the wait.
	if _, err := svc.ExtendSubscription(ctx, owner(t, svc), ExtendParams{SubscriptionID: sub.ID, Days: 5, Reason: "a gift", IdempotencyKey: "fu-staff"}); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("staff days: %v %v", worked, err)
	}
	got, _ = s.GetSubscription(ctx, s.Conn(), sub.ID)
	if got.FirstUseDays == nil || *got.FirstUseDays != 65 {
		t.Fatalf("after the staff's days: %+v", got)
	}

	// Made again while waiting (the panel lost it): still waiting.
	if err := svc.RecreateSubscriptionClient(ctx, owner(t, svc), fp, sub.ID, "lost"); err != nil {
		t.Fatal(err)
	}
	if len(fp.recreated) != 1 || fp.recreated[0].expires != -65*86400 {
		t.Fatalf("made again: %+v", fp.recreated)
	}

	// The first connection: the panel sets the end, and the service runs like any other.
	end := time.Now().Add(65 * 24 * time.Hour).Truncate(time.Second)
	fp.setUsage(sub.ID, Usage{UsedBytes: 1 << 20, TotalBytes: 110 << 30, ExpiresAt: end.Unix(), Enabled: true})
	if _, err := uw.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSubscription(ctx, s.Conn(), sub.ID)
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(end) || got.FirstUseDays != nil || got.Status != "active" {
		t.Fatalf("after the first connection: %+v", got)
	}

	// A waiting expiry an operator puts on a running service is not taken: core keeps its date.
	fp.setUsage(sub.ID, Usage{UsedBytes: 1 << 20, TotalBytes: 110 << 30, ExpiresAt: -10 * 86400, Enabled: true})
	if _, err := uw.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSubscription(ctx, s.Conn(), sub.ID)
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(end) || got.FirstUseDays != nil {
		t.Fatalf("a running service took the panel's wait: %+v", got)
	}
}

// Off (the default), a bought service starts at once, as before.
func TestServiceStartsAtOnceWhenOff(t *testing.T) {
	svc, s := testService(t)
	plan := planWithLimits(t, s, 30, 50<<30)
	_, sub, fp, _ := delivered(t, svc, s, 5811, plan)
	if len(fp.firstUse) != 1 || fp.firstUse[0] || sub.ExpiresAt == nil || sub.FirstUseDays != nil {
		t.Fatalf("first use %v, service %+v", fp.firstUse, sub)
	}
}

// Who waits: the switch for the buyer's role, a plan with days that is not the free trial, a new service that staff did
// not make.
func TestWhoseServicesWaitForTheFirstConnection(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	w := svc.NewProvisionWorker(&fakeProvisioner{}, nil)
	days := int32(30)
	plan := &store.Plan{DurationDays: &days}
	trial := &store.Plan{DurationDays: &days, IsTrial: true}
	noDays := &store.Plan{}
	customer := &store.User{Role: "user", Status: "active"}
	seller := &store.User{Role: ResellerRole, Status: "active"}
	staff := "00000000-0000-7000-8000-000000000001"
	job := func(u *store.User, p *store.Plan, typ string, by *string) *provisionJob {
		return &provisionJob{user: u, plan: p, order: &store.Order{Type: typ, CreatedBy: by}}
	}
	for _, c := range []struct {
		name                 string
		customers, resellers bool
		job                  *provisionJob
		want                 bool
	}{
		{"off", false, false, job(customer, plan, OrderNew, nil), false},
		{"a customer", true, false, job(customer, plan, OrderNew, nil), true},
		{"a reseller, the customers' switch only", true, false, job(seller, plan, OrderNew, nil), false},
		{"a reseller", false, true, job(seller, plan, OrderNew, nil), true},
		{"a customer, the resellers' switch only", false, true, job(customer, plan, OrderNew, nil), false},
		{"the free trial", true, true, job(customer, trial, OrderNew, nil), false},
		{"a plan with no days", true, true, job(customer, noDays, OrderNew, nil), false},
		{"made by staff", true, true, job(customer, plan, OrderNew, &staff), false},
		{"a renewal", true, true, job(customer, plan, OrderRenew, nil), false},
	} {
		firstUseOn(t, svc, owner(t, svc), c.customers, c.resellers)
		if got := w.startsOnFirstUse(ctx, c.job); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPanelExpiry(t *testing.T) {
	end, days := panelExpiry(1_800_000_000)
	if end == nil || end.Unix() != 1_800_000_000 || days != nil {
		t.Fatalf("an end: %v %v", end, days)
	}
	if end, days = panelExpiry(0); end != nil || days != nil {
		t.Fatalf("none: %v %v", end, days)
	}
	for in, want := range map[int64]int32{-30 * 86400: 30, -86400: 1, -1: 1, -(30*86400 + 1): 31, -40000 * 86400: maxFirstUseDays} {
		if end, days = panelExpiry(in); end != nil || days == nil || *days != want {
			t.Errorf("%d: %v %v, want %d days", in, end, days, want)
		}
	}
}
