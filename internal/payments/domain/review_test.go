package domain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
)

// Regressions from the Phase 2 review.

func TestReconcilerSurvivesManyChecksAndUserChecksDoNotCount(t *testing.T) {
	svc, st := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	ctx := context.Background()
	stuck, _ := svc.StartGateway(ctx, gwIntent("rv1", 1_000_000), "", "")
	fresh, _ := svc.StartGateway(ctx, gwIntent("rv2", 2_000_000), "", "")
	if stuck.ExpiresAt == nil {
		t.Fatal("a Zarinpal intent must expire")
	}
	// 2^1024 overflowed a double and stopped the reconciler for everyone.
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET check_attempts = 5000, checked_at = now() - interval '1 day' WHERE id = $1`, stuck.ID); err != nil {
		t.Fatal(err)
	}
	gw.set(*fresh.ExternalID, gateway.Result{State: gateway.Paid, Amount: 2_000_000, Currency: "IRR"})
	if n, err := svc.ReconcileOnce(ctx, time.Second); err != nil || n != 2 {
		t.Fatalf("reconcile with a heavily checked intent: %d %v", n, err)
	}
	if got, _ := st.GetIntent(ctx, nil, fresh.ID); got.Status != "succeeded" {
		t.Fatalf("fresh paid intent starved: %+v", got)
	}

	// The customer's "check" presses do not push the reconciler's backoff.
	open, _ := svc.StartGateway(ctx, gwIntent("rv3", 1_000_000), "", "")
	for i := 0; i < 3; i++ {
		if _, err := svc.CheckIntent(ctx, uid, open.ID, "check"); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := st.GetIntent(ctx, nil, open.ID); got.CheckAttempts != 0 {
		t.Fatalf("user checks counted towards the backoff: %d", got.CheckAttempts)
	}
	// ...and repeated presses within the gap do not reach the gateway.
	if gw.checks.Load() > 3+1 {
		t.Fatalf("unthrottled checks: %d gateway calls", gw.checks.Load())
	}
}

func TestLatePaymentStillSettles(t *testing.T) {
	svc, st := testService(t)
	gw := &fakeGW{name: "zarinpal"}
	svc.SetGateways(gw)
	ctx := context.Background()

	// Within the grace after the pay window the intent stays open.
	grace := gwIntent("lp0", 1_000_000)
	justPast := time.Now().Add(-time.Minute)
	grace.ExpiresAt = &justPast
	grace, _ = svc.StartGateway(ctx, grace, "", "")
	if got, _ := svc.CheckIntent(ctx, uid, grace.ID, "check"); got.Status != "pending" {
		t.Fatalf("expired within the grace: %+v", got)
	}

	// Expired by the reconciler, then the gateway confirms the payment (the
	// return was lost): the customer's "check" press still credits it.
	late := gwIntent("lp1", 1_000_000)
	past := time.Now().Add(-expiryGrace - time.Minute)
	late.ExpiresAt = &past
	late, _ = svc.StartGateway(ctx, late, "", "")
	if _, err := svc.ReconcileOnce(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetIntent(ctx, nil, late.ID); got.Status != "expired" {
		t.Fatalf("not expired by the reconciler: %+v", got)
	}
	gw.set(*late.ExternalID, gateway.Result{State: gateway.Paid, Amount: 1_000_000, Currency: "IRR"})
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET checked_at = now() - interval '1 minute' WHERE id = $1`, late.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.CheckIntent(ctx, uid, late.ID, "check"); err != nil || got.Status != "succeeded" {
		t.Fatalf("late payment via the check button: %+v %v", got, err)
	}

	// An amount mismatch stays failed even if the gateway is asked again.
	mm, _ := svc.StartGateway(ctx, gwIntent("lp2", 1_000_000), "", "")
	gw.set(*mm.ExternalID, gateway.Result{State: gateway.Paid, Amount: 5, Currency: "IRR"})
	_, _ = svc.CheckIntent(ctx, uid, mm.ID, "check")
	gw.set(*mm.ExternalID, gateway.Result{State: gateway.Paid, Amount: 1_000_000, Currency: "IRR"})
	if got, _ := svc.CheckAfterReturn(ctx, mm.ID); got.Status != "failed" {
		t.Fatalf("mismatch became paid: %+v", got)
	}
	if ledgerCount(t, st) != 1 {
		t.Fatalf("ledger entries: %d", ledgerCount(t, st))
	}

	// Closed intents are re-asked only behind the gap and only for 48 h.
	old, _ := svc.StartGateway(ctx, gwIntent("lp3", 1_000_000), "", "")
	gw.set(*old.ExternalID, gateway.Result{State: gateway.Failed, Reason: "gateway_failed"})
	_, _ = svc.CheckIntent(ctx, uid, old.ID, "check")
	before := gw.checks.Load()
	for i := 0; i < 5; i++ {
		_, _ = svc.CheckAfterReturn(ctx, old.ID)
	}
	if n := gw.checks.Load() - before; n != 0 {
		t.Fatalf("a closed intent was re-asked within the gap: %d calls", n)
	}
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET created_at = now() - interval '3 days', checked_at = NULL WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	_, _ = svc.CheckAfterReturn(ctx, old.ID)
	if n := gw.checks.Load() - before; n != 0 {
		t.Fatalf("a closed intent older than 48 h was re-asked: %d calls", n)
	}
}

func TestLosingStarsChargeIsRefusedAndRecorded(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()
	in, _ := svc.StartStars(ctx, starsIntent("ls1", 100))
	if _, err := svc.ConfirmStars(ctx, uid, in.ID, 100, "XTR", "chg-a"); err != nil {
		t.Fatal(err)
	}
	// A charge for an intent that can no longer be paid is refused (refund it).
	failed, _ := svc.StartStars(ctx, starsIntent("ls2", 100))
	_, _ = svc.ConfirmStars(ctx, uid, failed.ID, 50, "XTR", "chg-b") // wrong amount: fails the intent
	if _, err := svc.ConfirmStars(ctx, uid, failed.ID, 100, "XTR", "chg-c"); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("charge for a failed intent: %v", err)
	}
	var n int
	if err := st.DB().QueryRow(ctx, `SELECT count(*) FROM payments.gateway_events WHERE outcome = 'rejected' AND detail->>'charge_id' = 'chg-c'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the refused charge was not recorded: %d %v", n, err)
	}
	// A charge id that already settled another intent is refused, not an internal error.
	other, _ := svc.StartStars(ctx, starsIntent("ls3", 100))
	if _, err := svc.ConfirmStars(ctx, uid, other.ID, 100, "XTR", "chg-a"); !errors.Is(err, ErrAlreadyPaid) {
		t.Fatalf("reused charge: %v", err)
	}
}
