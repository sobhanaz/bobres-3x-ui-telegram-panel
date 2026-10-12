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
	// checkErr makes every Check fail (an unreachable or refusing gateway).
	checkErr error
	// returned records whether a Check was told the customer came back.
	returned atomic.Bool
	// onCheck runs at the start of every Check (a test cancels its context).
	onCheck func()
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

func (f *fakeGW) Check(ctx context.Context, ext string, c gateway.Charge) (gateway.Result, error) {
	f.checks.Add(1)
	if f.onCheck != nil {
		f.onCheck()
		if err := ctx.Err(); err != nil {
			return gateway.Result{}, err
		}
	}
	if c.Returned {
		f.returned.Store(true)
	}
	if f.checkErr != nil {
		return gateway.Result{}, f.checkErr
	}
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
	other.Provider = "paypal"
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
	past := time.Now().Add(-expiryGrace - time.Minute)
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

func TestReturnTellsTheGatewayTheSessionWasUsed(t *testing.T) {
	svc, _ := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	ctx := context.Background()
	in, _ := svc.StartGateway(ctx, gwIntent("ret1", 1_000_000), "", "")
	if _, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil {
		t.Fatal(err)
	}
	if gw.returned.Load() {
		t.Fatal("a check press is not a return from the bank")
	}
	if _, err := svc.CheckAfterReturn(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if !gw.returned.Load() {
		t.Fatal("the return URL must tell the gateway the session was used")
	}
}

// A gateway that keeps refusing an intent keeps it open while a confirmed
// payment could still settle it, then expires it instead of polling forever.
func TestPersistentGatewayErrorsEventuallyExpire(t *testing.T) {
	svc, st := testService(t)
	gw := &fakeGW{name: "zarinpal", checkErr: errors.New("zarinpal: error -11 (HTTP 401): Terminal is not active")}
	svc.SetGateways(gw)
	ctx := context.Background()
	recent, _ := svc.StartGateway(ctx, gwIntent("pe1", 1_000_000), "", "")
	old, _ := svc.StartGateway(ctx, gwIntent("pe2", 1_000_000), "", "")
	for id, age := range map[string]string{recent.ID: "3 hours", old.ID: "49 hours"} {
		if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents
			SET created_at = now() - $2::interval, expires_at = now() - $2::interval + interval '1 hour', checked_at = NULL
			WHERE id = $1`, id, age); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := svc.ReconcileOnce(ctx, time.Second); err != nil || n != 2 {
		t.Fatalf("reconcile: %d %v", n, err)
	}
	if got, _ := st.GetIntent(ctx, nil, recent.ID); got.Status != "pending" {
		t.Fatalf("an erroring intent a payment may still settle must stay open: %+v", got)
	}
	got, _ := st.GetIntent(ctx, nil, old.ID)
	if got.Status != "expired" || got.FailureReason == nil || *got.FailureReason != ReasonExpired {
		t.Fatalf("an intent erroring past the reopen window must expire: %+v", got)
	}
}

// While an intent can still be paid, the reconciler checks it at least every
// 8 intervals (a payment whose return was lost must be verified in time);
// afterwards the backoff grows to 64 intervals.
func TestReconcilerChecksPayableIntentsOften(t *testing.T) {
	svc, st := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	ctx := context.Background()
	payable, _ := svc.StartGateway(ctx, gwIntent("kc1", 1_000_000), "", "")
	stale, _ := svc.StartGateway(ctx, gwIntent("kc2", 1_000_000), "", "")
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents
		SET check_attempts = 6, checked_at = now() - interval '10 minutes' WHERE id = ANY($1)`, []string{payable.ID, stale.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET expires_at = now() - interval '2 hours' WHERE id = $1`, stale.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ReconcileOnce(ctx, time.Minute); err != nil || n != 1 {
		t.Fatalf("due intents: %d %v (want only the payable one: 8 min gap vs 64)", n, err)
	}
	p, _ := st.GetIntent(ctx, nil, payable.ID)
	s, _ := st.GetIntent(ctx, nil, stale.ID)
	if p.CheckAttempts != 7 || s.CheckAttempts != 6 {
		t.Fatalf("checked: payable %d stale %d", p.CheckAttempts, s.CheckAttempts)
	}
}

// checkErrors counts an intent's check/error events.
func checkErrors(t *testing.T, st *store.Store, intentID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(context.Background(), `SELECT count(*) FROM payments.gateway_events
		WHERE intent_id = $1 AND kind = 'check' AND outcome = 'error'`, intentID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A gateway outage leaves a trace: each intent's failed checks are kept as
// check/error events, at most once an hour however often it is asked (the
// reconciler, the customer's "check payment", the return page), and the
// report shows them.
func TestFailedChecksAreKeptOncePerIntentPerHour(t *testing.T) {
	svc, st := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	ctx := context.Background()
	a, _ := svc.StartGateway(ctx, gwIntent("ce1", 1_000_000), "", "")
	b, _ := svc.StartGateway(ctx, gwIntent("ce2", 1_000_000), "", "")
	gw.checkErr = errors.New("zarinpal: request failed: timed out")
	due := func() {
		t.Helper()
		if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET checked_at = NULL WHERE id = ANY($1)`,
			[]string{a.ID, b.ID}); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		due()
		if n, err := svc.ReconcileOnce(ctx, time.Second); err != nil || n != 2 {
			t.Fatalf("reconcile: %d %v", n, err)
		}
	}
	due()
	if _, err := svc.CheckIntent(ctx, uid, a.ID, "check"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CheckAfterReturn(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if gw.checks.Load() != 12 || checkErrors(t, st, a.ID) != 1 || checkErrors(t, st, b.ID) != 1 {
		t.Fatalf("asked %d times; kept %d and %d errors, want one per intent", gw.checks.Load(),
			checkErrors(t, st, a.ID), checkErrors(t, st, b.ID))
	}
	// The intents stay open: a payment may still settle them.
	if got, _ := st.GetIntent(ctx, nil, a.ID); got.Status != "pending" {
		t.Fatalf("a failed check closed the intent: %+v", got)
	}

	r, err := svc.GatewayReport(ctx, providerZarinpal, time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if r.CheckErrorCount != 2 || len(r.CheckErrors) != 2 || r.CheckErrors[0].Detail != "zarinpal: request failed: timed out" {
		t.Fatalf("report: %d %+v", r.CheckErrorCount, r.CheckErrors)
	}
	// Failed checks are not the gateway's last error: one timed-out poll must
	// not mark it unhealthy (and alert the owner) for an hour.
	if r.LastError != nil {
		t.Fatalf("a failed check is the last error: %+v", r.LastError)
	}

	// An hour later the outage is still on: one more each.
	if _, err := st.DB().Exec(ctx, `UPDATE payments.gateway_events SET created_at = created_at - interval '61 minutes'
		WHERE kind = 'check' AND outcome = 'error'`); err != nil {
		t.Fatal(err)
	}
	due()
	if _, err := svc.ReconcileOnce(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if checkErrors(t, st, a.ID) != 2 || checkErrors(t, st, b.ID) != 2 {
		t.Fatalf("after an hour: %d and %d errors, want 2 each", checkErrors(t, st, a.ID), checkErrors(t, st, b.ID))
	}

	// A working check keeps nothing.
	gw.checkErr = nil
	c, _ := svc.StartGateway(ctx, gwIntent("ce3", 1_000_000), "", "")
	if _, err := svc.CheckIntent(ctx, uid, c.ID, "check"); err != nil || checkErrors(t, st, c.ID) != 0 {
		t.Fatalf("working check: %v, %d errors", err, checkErrors(t, st, c.ID))
	}
}

// A check cut short on our side (shutdown, the caller gave up) is not the
// gateway's failure: nothing is kept.
func TestCanceledCheckIsNotKept(t *testing.T) {
	svc, st := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	in, _ := svc.StartGateway(context.Background(), gwIntent("cc1", 1_000_000), "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gw.onCheck = cancel
	if _, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil {
		t.Fatal(err)
	}
	if gw.checks.Load() != 1 || checkErrors(t, st, in.ID) != 0 {
		t.Fatalf("asked %d times, kept %d errors", gw.checks.Load(), checkErrors(t, st, in.ID))
	}
}
