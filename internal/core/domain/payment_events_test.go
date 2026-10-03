package domain

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func payEvent(t *testing.T, eventID, topic string, p events.PaymentEvent) eventbus.Message {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return eventbus.Message{EventID: eventID, Topic: topic, Payload: b}
}

func TestManualPaymentPaysTheOrderOnce(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 2001)
	p := seedPlan(t, s, false, 700)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "c-ord-1"})

	m := payEvent(t, "e1", events.PaymentsPaymentSucceeded, events.PaymentEvent{
		IntentID: "00000000-0000-7000-8000-0000000000f1", OrderID: o.ID, UserID: u.ID, Amount: 700, Currency: "IRT",
	})
	for i := 0; i < 2; i++ { // the second delivery is a no-op (inbox)
		if err := svc.HandlePaymentEvent(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.GetOrder(ctx, s.Conn(), o.ID)
	if got.Status != "paid" {
		t.Fatalf("order status = %q", got.Status)
	}
	// Revenue is visible in core's ledger: +700 in, -700 for the purchase.
	if bal, sum, rows := ledger(t, s, u.ID); bal != 0 || sum != 0 || rows != 2 {
		t.Fatalf("balance=%d ledger=%d rows=%d, want 0/0/2", bal, sum, rows)
	}
	if n := len(outboxEvents(t, s, events.OrderPaid)); n != 1 {
		t.Fatalf("order.paid events = %d", n)
	}
}

// The ledger key used to be checked after the balance moved, and the
// duplicate was swallowed with a commit: a second event for one intent (a
// different event id) credited the wallet twice.
func TestOneIntentCreditsOnceWhateverArrives(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 2002)
	p := events.PaymentEvent{IntentID: "00000000-0000-7000-8000-00000000abcd", UserID: u.ID, Amount: 5000, Currency: "IRT"}
	for _, m := range []eventbus.Message{
		payEvent(t, "e-a", events.PaymentsPaymentSucceeded, p),
		payEvent(t, "e-b", events.PaymentsLegacyWalletCredited, p),
		payEvent(t, "e-c", events.PaymentsPaymentSucceeded, p),
	} {
		if err := svc.HandlePaymentEvent(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if bal, sum, _ := ledger(t, s, u.ID); bal != 5000 || sum != 5000 {
		t.Fatalf("balance=%d ledger=%d, want 5000/5000", bal, sum)
	}
	if n := len(outboxEvents(t, s, events.WalletCredited)); n != 1 {
		t.Fatalf("wallet.credited published %d times", n)
	}
}

func TestRejectionIsForwardedNotFatal(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 2003)
	m := payEvent(t, "e-r", events.PaymentsPaymentRejected, events.PaymentEvent{
		IntentID: "00000000-0000-7000-8000-0000000000e1", UserID: u.ID, Amount: 1, Currency: "IRT", Reason: "blurry photo",
	})
	if err := svc.HandlePaymentEvent(ctx, m); err != nil {
		t.Fatalf("a rejection must be applied, not retried: %v", err)
	}
	evs := outboxEvents(t, s, events.PaymentRejected)
	if len(evs) != 1 {
		t.Fatalf("payment.rejected forwarded %d times", len(evs))
	}
	var e events.PaymentRejectedEvent
	_ = json.Unmarshal(evs[0], &e)
	if e.TelegramID != 2003 || e.Reason != "blurry photo" {
		t.Errorf("bad forwarded event: %+v", e)
	}
	if err := svc.HandlePaymentEvent(ctx, eventbus.Message{EventID: "e-x", Topic: "refund.completed.v9", Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("an unknown topic must be ignored: %v", err)
	}
}

func TestBadPaymentEventsArePermanent(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	if err := svc.HandlePaymentEvent(ctx, eventbus.Message{EventID: "e1", Topic: events.PaymentsPaymentSucceeded, Payload: []byte(`{`)}); !eventbus.IsPermanent(err) {
		t.Fatalf("malformed payload: %v", err)
	}
	m := payEvent(t, "e2", events.PaymentsPaymentSucceeded, events.PaymentEvent{
		IntentID: "00000000-0000-7000-8000-0000000000e2", UserID: "00000000-0000-7000-8000-00000000dead", Amount: 1, Currency: "IRT",
	})
	if err := svc.HandlePaymentEvent(ctx, m); !eventbus.IsPermanent(err) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestLatePaymentForAPaidOrderStaysInTheWallet(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 2004)
	p := seedPlan(t, s, false, 300)
	fund(t, s, u.ID, 300)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "late"})
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	m := payEvent(t, "e-late", events.PaymentsPaymentSucceeded, events.PaymentEvent{
		IntentID: "00000000-0000-7000-8000-0000000000aa", OrderID: o.ID, UserID: u.ID, Amount: 300, Currency: "IRT",
	})
	if err := svc.HandlePaymentEvent(ctx, m); err != nil {
		t.Fatal(err)
	}
	if bal, sum, _ := ledger(t, s, u.ID); bal != 300 || sum != 300 {
		t.Fatalf("the second payment must stay in the wallet: balance=%d ledger=%d", bal, sum)
	}
	evs := outboxEvents(t, s, events.WalletCredited)
	var e events.WalletCreditedEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &e) != nil || e.Reason != events.CreditOrderNotPayable {
		t.Fatalf("bad wallet.credited: %s", evs)
	}
}

// End to end through the real feed and consumer: a top-up approval and a
// rejection come first, then an order payment. The old relay stopped at the
// first of these and never applied anything after it.
func TestPaymentsFeedNeverStallsOnUnhandledEvents(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 2005)
	p := seedPlan(t, s, false, 700)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "feed"})

	ob := eventbus.NewOutbox("outbox_payments")
	topup := events.PaymentEvent{IntentID: "00000000-0000-7000-8000-0000000000a1", UserID: u.ID, Amount: 9000, Currency: "IRT"}
	rejected := events.PaymentEvent{IntentID: "00000000-0000-7000-8000-0000000000a2", UserID: u.ID, OrderID: o.ID, Amount: 700, Currency: "IRT"}
	paid := events.PaymentEvent{IntentID: "00000000-0000-7000-8000-0000000000a3", UserID: u.ID, OrderID: o.ID, Amount: 700, Currency: "IRT"}
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		for _, e := range []struct {
			topic string
			p     events.PaymentEvent
		}{
			{events.PaymentsPaymentSucceeded, topup},
			{events.PaymentsLegacyWalletCredited, topup},
			{events.PaymentsPaymentRejected, rejected},
			{"something.new.v1", topup},
			{events.PaymentsPaymentSucceeded, paid},
		} {
			if _, err := ob.Publish(ctx, tx, e.topic, e.p); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	src := eventbus.LocalSource{Feed: eventbus.NewFeed(s.DB(), "outbox_payments"), Consumer: "core"}
	c, err := eventbus.NewConsumer(eventbus.ConsumerConfig{Source: src, Handle: svc.HandlePaymentEvent, DeadLetter: svc.DeadLetter("payments")})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := c.Step(ctx); err != nil || n != 5 {
		t.Fatalf("step: handled=%d err=%v", n, err)
	}
	got, _ := s.GetOrder(ctx, s.Conn(), o.ID)
	if bal, _, _ := ledger(t, s, u.ID); got.Status != "paid" || bal != 9000 {
		t.Fatalf("order=%q wallet=%d, want paid/9000", got.Status, bal)
	}
	var dead int
	_ = s.DB().QueryRow(ctx, `SELECT count(*) FROM core.dead_letters`).Scan(&dead)
	if dead != 0 {
		t.Fatalf("%d events dead-lettered", dead)
	}
}

func TestDeadLetterRecordsTheEvent(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	m := eventbus.Message{EventID: "e-dl", Topic: events.PaymentsPaymentSucceeded, Payload: []byte(`{`)}
	if err := svc.DeadLetter("payments")(ctx, m, eventbus.Permanent(errBoom)); err != nil {
		t.Fatal(err)
	}
	var d store.DeadLetter
	if err := s.DB().QueryRow(ctx, `SELECT source, event_id, error FROM core.dead_letters`).Scan(&d.Source, &d.EventID, &d.Error); err != nil {
		t.Fatal(err)
	}
	if d.Source != "payments" || d.EventID != "e-dl" || d.Error != "boom" {
		t.Errorf("bad dead letter: %+v", d)
	}
}

type boomErr struct{}

func (boomErr) Error() string { return "boom" }

var errBoom = boomErr{}

func TestOrchestratedIntentForOrder(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 2006)
	pl := seedPlan(t, s, false, 1500)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: pl.ID, IdempotencyKey: "oi-1"})
	raw, _ := json.Marshal("6104-0000")
	_ = s.SetSetting(ctx, s.Conn(), "payments.card_number", raw)

	fake := &fakePayments{id: "int-1", status: "pending"}
	res, err := svc.CreatePaymentIntent(ctx, fake, CreatePaymentIntentParams{
		UserID: u.ID, OrderID: o.ID, Provider: "manual_card", IdempotencyKey: "oi-int-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IntentID != "int-1" || res.Instructions == "" || fake.amount != 1500 || fake.currency != "IRT" {
		t.Errorf("bad result: %+v fake=%+v", res, fake)
	}
	for name, p := range map[string]CreatePaymentIntentParams{
		"bad provider":   {UserID: u.ID, Provider: "zarinpal", Amount: 1, Currency: "IRT", IdempotencyKey: "x1"},
		"bad currency":   {UserID: u.ID, Provider: "manual_card", Amount: 1, Currency: "EUR", IdempotencyKey: "x2"},
		"zero top-up":    {UserID: u.ID, Provider: "manual_card", Amount: 0, Currency: "IRT", IdempotencyKey: "x3"},
		"missing key":    {UserID: u.ID, Provider: "manual_card", Amount: 1, Currency: "IRT"},
		"absurd top-up":  {UserID: u.ID, Provider: "manual_card", Amount: maxTopupMinor + 1, Currency: "IRT", IdempotencyKey: "x4"},
		"other's order":  {UserID: seedUser(t, s, 2007).ID, OrderID: o.ID, Provider: "manual_card", IdempotencyKey: "x5"},
		"unknown person": {UserID: "00000000-0000-7000-8000-00000000dead", Provider: "manual_card", Amount: 1, Currency: "IRT", IdempotencyKey: "x6"},
	} {
		if _, err := svc.CreatePaymentIntent(ctx, fake, p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

type fakePayments struct {
	id, status string
	amount     int64
	currency   string
	pending    []PendingPayment
	reviews    []string // reviewer|intent|decision
	proofs     []string
}

func (f *fakePayments) CreateIntent(_ context.Context, _, _, _ string, amount int64, currency, _ string) (string, string, error) {
	f.amount, f.currency = amount, currency
	return f.id, f.status, nil
}

func (f *fakePayments) SubmitReceipt(_ context.Context, userID, intentID, fileID, ref string) (string, error) {
	f.proofs = append(f.proofs, "card|"+userID+"|"+intentID+"|"+fileID+"|"+ref)
	return "confirming", nil
}

func (f *fakePayments) SubmitTXID(_ context.Context, userID, intentID, network, txid string) (string, error) {
	f.proofs = append(f.proofs, "crypto|"+userID+"|"+intentID+"|"+network+"|"+txid)
	return "confirming", nil
}

func (f *fakePayments) ListPending(context.Context, int) ([]PendingPayment, error) {
	return f.pending, nil
}

func (f *fakePayments) Review(_ context.Context, reviewerID, intentID, decision, _ string) (string, error) {
	f.reviews = append(f.reviews, reviewerID+"|"+intentID+"|"+decision)
	if decision == "approved" {
		return "succeeded", nil
	}
	return "failed", nil
}
