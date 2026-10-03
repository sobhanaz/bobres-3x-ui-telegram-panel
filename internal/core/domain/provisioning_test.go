package domain

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

type fakeProvisioner struct {
	mu       sync.Mutex
	failures int // fail this many CreateClient calls first
	calls    []string
	days     int64
	traffic  int64
}

func (f *fakeProvisioner) CreateClient(_ context.Context, subscriptionID, email string, days, traffic int64) (ProvisionedClient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, subscriptionID+"|"+email)
	f.days, f.traffic = days, traffic
	if f.failures > 0 {
		f.failures--
		return ProvisionedClient{}, errors.New("panel unreachable")
	}
	exp := int64(0)
	if days > 0 {
		exp = time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix()
	}
	return ProvisionedClient{ServerID: "00000000-0000-7000-8000-00000000c0de", XUISubID: "sub-" + subscriptionID[:8], ExpiresAt: exp}, nil
}

func (f *fakeProvisioner) GetLinks(_ context.Context, subscriptionID string) (Links, error) {
	return Links{SubscriptionLink: "https://sub.example.com/sub/" + subscriptionID, QRPNG: []byte{0x89, 'P', 'N', 'G'}}, nil
}

func (f *fakeProvisioner) Health(context.Context) (bool, string, error) { return true, "", nil }

func paidWalletOrder(t *testing.T, svc *Service, s *store.Store, tg int64, plan *store.Plan) (*store.User, *store.Order) {
	t.Helper()
	ctx := context.Background()
	u := seedUser(t, s, tg)
	fund(t, s, u.ID, plan.Price)
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "prov-" + u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	return u, o
}

func planWithLimits(t *testing.T, s *store.Store, days int32, traffic int64) *store.Plan {
	t.Helper()
	p := &store.Plan{NameI18n: map[string]string{"en": "Monthly"}, Kind: "both", Price: 1000, Currency: "IRT",
		Enabled: true, DurationDays: &days, TrafficBytes: &traffic}
	if err := s.UpsertPlan(context.Background(), s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPaidOrderIsProvisionedAndAnnounced(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 50<<30)
	u, o := paidWalletOrder(t, svc, s, 3001, plan)

	fp := &fakeProvisioner{}
	w := svc.NewProvisionWorker(fp, nil)
	worked, err := w.ProvisionNext(ctx)
	if err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if fp.days != 30 || fp.traffic != 50<<30 {
		t.Errorf("plan limits not passed: days=%d traffic=%d", fp.days, fp.traffic)
	}
	got, _ := s.GetOrder(ctx, s.Conn(), o.ID)
	sub, err := s.SubscriptionByOrder(ctx, s.Conn(), o.ID)
	if err != nil || got.Status != "active" || sub.Status != "active" || sub.SubLink == "" || sub.ExpiresAt == nil || sub.ServerID == "" {
		t.Fatalf("order=%q sub=%+v err=%v", got.Status, sub, err)
	}
	evs := outboxEvents(t, s, events.SubscriptionProvisioned)
	var e events.SubscriptionProvisionedEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &e) != nil || e.TelegramID != u.TelegramID || e.SubscriptionLink != sub.SubLink {
		t.Fatalf("event: %s", evs)
	}
	if worked, _ := w.ProvisionNext(ctx); worked {
		t.Fatal("an active order was provisioned again")
	}
}

func TestProvisioningRetriesWithBackoffAndAlertsOnce(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 0)
	_, o := paidWalletOrder(t, svc, s, 3002, plan)

	fp := &fakeProvisioner{failures: 4}
	w := svc.NewProvisionWorker(fp, nil)
	makeDue := func() {
		if _, err := s.DB().Exec(ctx, `UPDATE core.orders SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, o.ID); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 1; attempt <= 4; attempt++ {
		if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
			t.Fatalf("attempt %d: worked=%v err=%v", attempt, worked, err)
		}
		got, _ := s.GetOrder(ctx, s.Conn(), o.ID)
		if got.Status != "provision_failed" {
			t.Fatalf("attempt %d: status %q", attempt, got.Status)
		}
		if worked, _ := w.ProvisionNext(ctx); worked {
			t.Fatalf("attempt %d: retried before the backoff elapsed", attempt)
		}
		makeDue()
	}
	if n := len(outboxEvents(t, s, events.ProvisionFailed)); n != 1 {
		t.Fatalf("provision_failed announced %d times, want once", n)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("final attempt: %v", err)
	}
	got, _ := s.GetOrder(ctx, s.Conn(), o.ID)
	if got.Status != "active" {
		t.Fatalf("status after recovery = %q", got.Status)
	}
	// Every attempt used the same subscription id and panel email, which is
	// what makes the provisioner's create idempotent.
	for _, c := range fp.calls {
		if c != fp.calls[0] {
			t.Fatalf("attempts used different identities: %v", fp.calls)
		}
	}
}

func TestStaleProvisioningLeaseIsReclaimed(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 7, 0)
	_, o := paidWalletOrder(t, svc, s, 3003, plan)
	// A worker claimed the order and died mid-call.
	if _, err := s.DB().Exec(ctx, `UPDATE core.orders SET status = 'provisioning', updated_at = now() - interval '10 minutes' WHERE id = $1`, o.ID); err != nil {
		t.Fatal(err)
	}
	w := svc.NewProvisionWorker(&fakeProvisioner{}, nil)
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("stale lease not reclaimed: worked=%v err=%v", worked, err)
	}
	if got, _ := s.GetOrder(ctx, s.Conn(), o.ID); got.Status != "active" {
		t.Fatalf("status %q", got.Status)
	}
}

func TestPaymentWakesTheWorker(t *testing.T) {
	svc, s := testService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := svc.NewProvisionWorker(&fakeProvisioner{}, nil)
	w.Interval = time.Hour // only a kick can make it work quickly
	done := make(chan struct{})
	go func() { _ = w.Run(ctx); close(done) }()

	plan := planWithLimits(t, s, 30, 0)
	_, o := paidWalletOrder(t, svc, s, 3004, plan)
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := s.GetOrder(context.Background(), s.Conn(), o.ID)
		if got.Status == "active" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("order not provisioned after a wallet payment (status %q)", got.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestProvisionBackoffGrowsAndCaps(t *testing.T) {
	prev := time.Duration(0)
	for a := 1; a <= 8; a++ {
		d := provisionBackoff(a)
		if d < prev || d > 30*time.Minute {
			t.Fatalf("attempt %d: %v after %v", a, d, prev)
		}
		prev = d
	}
	if e := clientEmail(42); len(e) != len("u42-")+8 || e == clientEmail(42) {
		t.Fatalf("client email %q is not unique per call", e)
	}
}
