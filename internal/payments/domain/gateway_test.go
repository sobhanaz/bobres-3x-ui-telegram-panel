package domain

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

// fakeGW is a scriptable gateway: Check answers with result (per external id).
type fakeGW struct {
	name    string
	creates atomic.Int32
	checks  atomic.Int32
	mu      sync.Mutex
	result  map[string]gateway.Result
	failErr error
}

func (f *fakeGW) Name() string { return f.name }

func (f *fakeGW) Create(_ context.Context, c gateway.Charge) (gateway.Started, error) {
	if f.failErr != nil {
		return gateway.Started{}, f.failErr
	}
	n := f.creates.Add(1)
	ext := fmt.Sprintf("ext-%s-%d", c.IntentID[len(c.IntentID)-4:], n)
	return gateway.Started{ExternalID: ext, PayURL: "https://pay.example.test/" + ext}, nil
}

func (f *fakeGW) Check(_ context.Context, ext string, _ gateway.Charge) (gateway.Result, error) {
	f.checks.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.result[ext]; ok {
		return r, nil
	}
	return gateway.Result{State: gateway.Pending}, nil
}

func (f *fakeGW) set(ext string, r gateway.Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.result == nil {
		f.result = map[string]gateway.Result{}
	}
	f.result[ext] = r
}

func gwIntent(key string, rial int64) *store.Intent {
	ga, gc := rial, "IRR"
	o := orderUUID
	return &store.Intent{
		UserID: uid, OrderID: &o, Provider: "zarinpal", Amount: rial / 10, Currency: "IRT",
		IdempotencyKey: key, GatewayAmount: &ga, GatewayCurrency: &gc,
	}
}

func ledgerCount(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(context.Background(), `SELECT count(*) FROM payments.ledger_entries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStartGatewayIsIdempotent(t *testing.T) {
	svc, _ := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	ctx := context.Background()

	a, err := svc.StartGateway(ctx, gwIntent("k1", 1_500_000), "Monthly", "https://shop.test/webhooks/zarinpal")
	if err != nil || a.ExternalID == nil || a.PayURL == nil || *a.PayURL == "" {
		t.Fatalf("start: %+v %v", a, err)
	}
	b, err := svc.StartGateway(ctx, gwIntent("k1", 1_500_000), "Monthly", "https://shop.test/webhooks/zarinpal")
	if err != nil || b.ID != a.ID || *b.ExternalID != *a.ExternalID || gw.creates.Load() != 1 {
		t.Fatalf("retry created a second gateway payment: %+v %v creates=%d", b, err, gw.creates.Load())
	}
	// Wrong unit or an unconfigured method is refused before anything is created.
	bad := gwIntent("k2", 1_500_000)
	usd := "USDT"
	bad.GatewayCurrency = &usd
	if _, err := svc.StartGateway(ctx, bad, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong gateway currency: %v", err)
	}
	other := gwIntent("k3", 1_500_000)
	other.Provider = "cryptopay"
	if _, err := svc.StartGateway(ctx, other, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unconfigured gateway: %v", err)
	}
	// A gateway outage leaves a pending intent without an external id and an error.
	gw.failErr = errors.New("timeout")
	if _, err := svc.StartGateway(ctx, gwIntent("k4", 2_000_000), "", ""); err == nil {
		t.Fatal("gateway outage hidden")
	}
}

func TestGatewayPaidSettlesOnceEvenConcurrently(t *testing.T) {
	svc, st := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	ctx := context.Background()
	in, err := svc.StartGateway(ctx, gwIntent("p1", 1_500_000), "Monthly", "")
	if err != nil {
		t.Fatal(err)
	}
	gw.set(*in.ExternalID, gateway.Result{State: gateway.Paid, Amount: 1_500_000, Currency: "IRR", Reference: "REF123"})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.CheckIntent(ctx, uid, in.ID, "callback"); err != nil {
				t.Errorf("check: %v", err)
			}
		}()
	}
	wg.Wait()
	got, _ := st.GetIntent(ctx, nil, in.ID)
	if got.Status != "succeeded" || got.ProviderRef == nil || *got.ProviderRef != "REF123" {
		t.Fatalf("intent: %+v", got)
	}
	ev := outbox(t, st)
	if len(ev[events.PaymentsPaymentSucceeded]) != 1 || ev[events.PaymentsPaymentSucceeded][0].Amount != 150_000 ||
		ev[events.PaymentsPaymentSucceeded][0].Currency != "IRT" || ledgerCount(t, st) != 1 {
		t.Fatalf("settled more than once or with the wrong amount: %+v ledger=%d", ev, ledgerCount(t, st))
	}
	// Checking a settled intent does not ask the gateway again.
	before := gw.checks.Load()
	if _, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil || gw.checks.Load() != before {
		t.Fatalf("settled intent re-checked: %v", err)
	}
	// Someone else's intent is not theirs to check.
	if _, err := svc.CheckIntent(ctx, "00000000-0000-7000-8000-00000000dfff", in.ID, "check"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other user: %v", err)
	}
}

func TestGatewayAmountMismatchFailedAndExpiry(t *testing.T) {
	svc, st := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	ctx := context.Background()

	mm, _ := svc.StartGateway(ctx, gwIntent("m1", 1_500_000), "", "")
	gw.set(*mm.ExternalID, gateway.Result{State: gateway.Paid, Amount: 10_000, Currency: "IRR"})
	got, err := svc.CheckIntent(ctx, uid, mm.ID, "callback")
	if err != nil || got.Status != "failed" || got.FailureReason == nil || *got.FailureReason != ReasonAmountMismatch {
		t.Fatalf("mismatch: %+v %v", got, err)
	}

	ca, _ := svc.StartGateway(ctx, gwIntent("c1", 1_500_000), "", "")
	gw.set(*ca.ExternalID, gateway.Result{State: gateway.Failed, Reason: "cancelled"})
	if got, _ := svc.CheckIntent(ctx, uid, ca.ID, "callback"); got.Status != "failed" || *got.FailureReason != "cancelled" {
		t.Fatalf("cancelled: %+v", got)
	}

	ex := gwIntent("e1", 1_500_000)
	past := time.Now().Add(-time.Minute)
	ex.ExpiresAt = &past
	ex, _ = svc.StartGateway(ctx, ex, "", "")
	if got, _ := svc.CheckIntent(ctx, uid, ex.ID, "check"); got.Status != "expired" {
		t.Fatalf("expired: %+v", got)
	}
	ev := outbox(t, st)
	reasons := map[string]bool{}
	for _, e := range ev[events.PaymentsPaymentRejected] {
		reasons[e.Reason] = true
	}
	if len(ev[events.PaymentsPaymentSucceeded]) != 0 || !reasons[ReasonAmountMismatch] || !reasons["cancelled"] || !reasons[ReasonExpired] {
		t.Fatalf("events: %+v", ev)
	}
	if ledgerCount(t, st) != 0 {
		t.Fatal("ledger credited for an unpaid intent")
	}
}

func TestReconcilerChecksDueIntentsWithBackoff(t *testing.T) {
	svc, st := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	ctx := context.Background()
	a, _ := svc.StartGateway(ctx, gwIntent("r1", 1_000_000), "", "")
	b, _ := svc.StartGateway(ctx, gwIntent("r2", 2_000_000), "", "")
	gw.set(*b.ExternalID, gateway.Result{State: gateway.Paid, Amount: 2_000_000, Currency: "IRR"})

	if n, err := svc.ReconcileOnce(ctx, time.Hour); err != nil || n != 2 {
		t.Fatalf("first pass: %d %v", n, err)
	}
	if got, _ := st.GetIntent(ctx, nil, b.ID); got.Status != "succeeded" {
		t.Fatalf("paid intent not settled by the reconciler: %+v", got)
	}
	// a stays pending and was just checked: not due again within the interval.
	if n, _ := svc.ReconcileOnce(ctx, time.Hour); n != 0 {
		t.Fatalf("checked again too soon: %d", n)
	}
	if got, _ := st.GetIntent(ctx, nil, a.ID); got.CheckAttempts != 1 || got.Status != "pending" {
		t.Fatalf("pending intent: %+v", got)
	}
}
