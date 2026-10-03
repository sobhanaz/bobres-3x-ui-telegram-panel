package domain

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/provider"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func testService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "payments"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB().Exec(ctx, `TRUNCATE payments.manual_receipts, payments.ledger_entries,
		payments.payment_intents, payments.outbox_payments CASCADE`); err != nil {
		t.Fatal(err)
	}
	return New(st), st
}

const (
	uid       = "00000000-0000-7000-8000-00000000d001"
	admin     = "00000000-0000-7000-8000-00000000d0a1"
	orderUUID = "00000000-0000-7000-8000-00000000d0b1"
)

func outbox(t *testing.T, st *store.Store) map[string][]events.PaymentEvent {
	t.Helper()
	rows, err := st.DB().Query(context.Background(), `SELECT topic, payload FROM payments.outbox_payments ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]events.PaymentEvent{}
	for rows.Next() {
		var (
			topic string
			raw   []byte
			e     events.PaymentEvent
		)
		if err := rows.Scan(&topic, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		out[topic] = append(out[topic], e)
	}
	return out
}

func TestManualCardOrderApproveFlow(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()

	in, err := svc.CreateIntent(ctx, &store.Intent{
		OrderID: strptr(orderUUID), UserID: uid, Provider: provider.ManualCard,
		Amount: 50000, Currency: "IRT", IdempotencyKey: "ord-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitReceipt(ctx, uid, in.ID, "photo123", "REF9988"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitTXID(ctx, uid, in.ID, "TRC20", "0x1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("txid on a card intent: %v", err)
	}
	if _, err := svc.SubmitReceipt(ctx, admin, in.ID, "x", "y"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign user submitted a receipt: %v", err)
	}
	got, err := svc.Review(ctx, admin, in.ID, "approved", "ok")
	if err != nil || got.Status != "succeeded" {
		t.Fatalf("review: %v %+v", err, got)
	}
	if _, err := svc.Review(ctx, admin, in.ID, "approved", "again"); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("double approve: %v", err)
	}
	ev := outbox(t, st)
	if len(ev[events.PaymentsPaymentSucceeded]) != 1 || ev[events.PaymentsPaymentSucceeded][0].OrderID != orderUUID {
		t.Fatalf("outbox: %+v", ev)
	}
}

// Core owns wallets: payments announces only the review outcome. Emitting a
// second event for top-ups was one of the triggers of the core-side double
// credit and of the stalled event queue.
func TestTopupApprovalEmitsOneEvent(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()
	in, _ := svc.CreateIntent(ctx, &store.Intent{
		UserID: uid, Provider: provider.ManualCrypto, Amount: 25_000_000, Currency: "USDT", IdempotencyKey: "top-1",
	})
	if _, err := svc.SubmitTXID(ctx, uid, in.ID, "trc20", "0xfeed"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Review(ctx, admin, in.ID, "approved", ""); err != nil {
		t.Fatal(err)
	}
	ev := outbox(t, st)
	if len(ev) != 1 || len(ev[events.PaymentsPaymentSucceeded]) != 1 || ev[events.PaymentsPaymentSucceeded][0].Amount != 25_000_000 {
		t.Fatalf("outbox: %+v", ev)
	}
	var bal int64
	if err := st.DB().QueryRow(ctx, `SELECT balance_after FROM payments.ledger_entries WHERE user_id=$1`, uid).Scan(&bal); err != nil || bal != 25_000_000 {
		t.Fatalf("ledger mirror: %d %v", bal, err)
	}
}

func TestRejectionCarriesTheReason(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()
	in, _ := svc.CreateIntent(ctx, &store.Intent{
		UserID: uid, Provider: provider.ManualCard, Amount: 1000, Currency: "IRT", IdempotencyKey: "rej-1",
	})
	if _, err := svc.SubmitReceipt(ctx, uid, in.ID, "p", "r"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Review(ctx, admin, in.ID, "rejected", "fake receipt")
	if err != nil || got.Status != "failed" {
		t.Fatalf("reject: %v %+v", err, got)
	}
	ev := outbox(t, st)[events.PaymentsPaymentRejected]
	if len(ev) != 1 || ev[0].Reason != "fake receipt" {
		t.Fatalf("payment.rejected: %+v", ev)
	}
}

// One crypto transaction cannot pay for two intents, in any letter case.
func TestTXIDCanPayOnlyOnce(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	a, _ := svc.CreateIntent(ctx, &store.Intent{UserID: uid, Provider: provider.ManualCrypto, Amount: 1, Currency: "USDT", IdempotencyKey: "tx-a"})
	b, _ := svc.CreateIntent(ctx, &store.Intent{UserID: uid, Provider: provider.ManualCrypto, Amount: 1, Currency: "USDT", IdempotencyKey: "tx-b"})
	if _, err := svc.SubmitTXID(ctx, uid, a.ID, "TRC20", "ABCDEF01"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitTXID(ctx, uid, b.ID, "TRC20", "abcdef01"); !errors.Is(err, store.ErrDuplicateProof) {
		t.Fatalf("reused TXID: want ErrDuplicateProof, got %v", err)
	}
}

func TestCreateIntentValidation(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	for name, in := range map[string]*store.Intent{
		"no key":        {UserID: uid, Provider: provider.ManualCard, Amount: 1, Currency: "IRT"},
		"bad provider":  {UserID: uid, Provider: "zarinpal", Amount: 1, Currency: "IRT", IdempotencyKey: "v1"},
		"zero amount":   {UserID: uid, Provider: provider.ManualCard, Amount: 0, Currency: "IRT", IdempotencyKey: "v2"},
		"bad currency":  {UserID: uid, Provider: provider.ManualCard, Amount: 1, Currency: "EUR", IdempotencyKey: "v3"},
		"wallet intent": {UserID: uid, Provider: provider.Wallet, Amount: 1, Currency: "IRT", IdempotencyKey: "v4"},
	} {
		if _, err := svc.CreateIntent(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if _, err := svc.CreateIntent(ctx, &store.Intent{UserID: uid, Provider: provider.ManualCard, Amount: 5, Currency: "IRT", IdempotencyKey: "same"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateIntent(ctx, &store.Intent{UserID: uid, Provider: provider.ManualCard, Amount: 6, Currency: "IRT", IdempotencyKey: "same"}); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("reused key with another amount: %v", err)
	}
}

func strptr(s string) *string { return &s }
