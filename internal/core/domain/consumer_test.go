package domain

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHandlePaymentSucceededMarksOrderPaidOnce(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 2001)
	p := seedPlan(t, s, false, 700)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, Type: "new", IdempotencyKey: "c-ord-1"})

	payload, _ := json.Marshal(map[string]any{
		"order_id": o.ID, "intent_id": "00000000-0000-7000-8000-0000000000f1",
		"user_id": u.ID, "amount": 700, "currency": "IRT",
	})
	handled, err := svc.HandlePaymentEvent(ctx, "msg-1", "payment.succeeded.v1", payload)
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	got, _ := s.GetOrder(ctx, s.Conn(), o.ID)
	if got.Status != "paid" {
		t.Errorf("status = %q", got.Status)
	}
	// Replay: dedup via inbox.
	handled, err = svc.HandlePaymentEvent(ctx, "msg-1", "payment.succeeded.v1", payload)
	if err != nil || handled {
		t.Fatalf("replay handled=%v err=%v", handled, err)
	}
}

func TestHandleWalletCreditedCreditsWalletOnce(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 2002)
	payload, _ := json.Marshal(map[string]any{
		"intent_id": "00000000-0000-7000-8000-0000000000f2",
		"user_id":   u.ID, "amount": 9000, "currency": "IRT",
	})
	if _, err := svc.HandlePaymentEvent(ctx, "msg-2", "wallet.credited.v1", payload); err != nil {
		t.Fatal(err)
	}
	w, _ := s.GetWallet(ctx, s.Conn(), u.ID, "IRT")
	if w.Balance != 9000 {
		t.Errorf("balance = %d", w.Balance)
	}
	if _, err := svc.HandlePaymentEvent(ctx, "msg-2", "wallet.credited.v1", payload); err != nil {
		t.Fatal(err)
	}
	w, _ = s.GetWallet(ctx, s.Conn(), u.ID, "IRT")
	if w.Balance != 9000 {
		t.Errorf("balance after replay = %d (double credit!)", w.Balance)
	}
}

func TestOrchestratedIntentForOrder(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 2003)
	pl := seedPlan(t, s, false, 1500)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: pl.ID, Type: "new", IdempotencyKey: "oi-1"})

	raw, _ := json.Marshal("6104-0000")
	_ = s.SetSetting(ctx, s.Conn(), "payments.card_number", raw)

	fake := &fakePayments{id: "int-1", status: "pending"}
	res, err := svc.CreatePaymentIntent(ctx, fake, CreatePaymentIntentParams{
		UserID: u.ID, OrderID: o.ID, Provider: "manual_card", IdempotencyKey: "oi-int-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IntentID != "int-1" || res.Instructions == "" {
		t.Errorf("bad result: %+v", res)
	}
	if fake.amount != 1500 || fake.currency != "IRT" {
		t.Errorf("amount not taken from order: %+v", fake)
	}
}

type fakePayments struct {
	id, status string
	amount     int64
	currency   string
	prov       string
}

func (f *fakePayments) CreateIntent(_ context.Context, _, _ string, prov string, amount int64, currency, _ string) (string, string, error) {
	f.amount, f.currency, f.prov = amount, currency, prov
	return f.id, f.status, nil
}
