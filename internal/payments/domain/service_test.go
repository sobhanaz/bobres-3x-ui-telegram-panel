package domain

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/provider"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

func testService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	dsn := os.Getenv("BOBRES_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///postgres?host=/tmp&port=5432&sslmode=disable"
	}
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "payments"); err != nil {
		t.Skipf("migration: %v", err)
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	for _, tbl := range []string{"manual_receipts", "ledger_entries", "payment_intents", "outbox_payments"} {
		if _, err := st.DB().Exec(ctx, "TRUNCATE payments."+tbl+" CASCADE"); err != nil {
			t.Skipf("seed admin and core: %v", err)
		}
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	ob := eventbus.NewOutbox(sqlDB, "outbox_payments")
	return New(st, ob), st
}

const (
	uid       = "00000000-0000-7000-8000-00000000d001"
	admin     = "00000000-0000-7000-8000-00000000d0a1"
	orderUUID = "00000000-0000-7000-8000-00000000d0b1"
)

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
	// Submitting a txid against a card intent must fail.
	if _, err := svc.SubmitTXID(ctx, uid, in.ID, "TRC20", "0x1"); err == nil {
		t.Fatal("txid submitted on card intent")
	}
	// Another user cannot submit to this intent.
	if _, err := svc.SubmitReceipt(ctx, admin, in.ID, "x", "y"); err == nil {
		t.Fatal("foreign user submitted receipt")
	}

	got, err := svc.Review(ctx, admin, in.ID, "approved", "ok")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "succeeded" {
		t.Errorf("status = %q", got.Status)
	}
	// Double approve rejected.
	if _, err := svc.Review(ctx, admin, in.ID, "approved", "again"); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("double approve: %v", err)
	}
	// Outbox has payment.succeeded.v1 with order_id.
	var topic string
	var payload []byte
	err = st.DB().QueryRow(ctx, `SELECT topic, payload FROM payments.outbox_payments WHERE topic='payment.succeeded.v1'`).Scan(&topic, &payload)
	if err != nil || !contains(string(payload), orderUUID) {
		t.Fatalf("outbox: %v %s", err, payload)
	}
}

func TestTopupApproveEmitsWalletCredited(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()

	in, _ := svc.CreateIntent(ctx, &store.Intent{
		UserID: uid, Provider: provider.ManualCrypto, Amount: 25, Currency: "USDT",
		IdempotencyKey: "top-1",
	})
	if _, err := svc.SubmitTXID(ctx, uid, in.ID, "TRC20", "0xfeed"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Review(ctx, admin, in.ID, "approved", "")
	if err != nil || got.Status != "succeeded" {
		t.Fatalf("review: %v %+v", err, got)
	}
	var n int
	if err := st.DB().QueryRow(ctx, `SELECT count(*) FROM payments.outbox_payments WHERE topic='wallet.credited.v1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("wallet.credited count = %d", n)
	}
	// Ledger mirror holds one 25 USDT credit.
	var bal int64
	if err := st.DB().QueryRow(ctx, `SELECT balance_after FROM payments.ledger_entries WHERE user_id=$1`, uid).Scan(&bal); err != nil {
		t.Fatal(err)
	}
	if bal != 25 {
		t.Errorf("ledger balance_after = %d", bal)
	}
}

func TestRejectFlow(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()
	in, _ := svc.CreateIntent(ctx, &store.Intent{
		UserID: uid, Provider: provider.ManualCard, Amount: 1000, Currency: "IRT",
		IdempotencyKey: "rej-1",
	})
	_, _ = svc.SubmitReceipt(ctx, uid, in.ID, "p", "r")
	got, err := svc.Review(ctx, admin, in.ID, "rejected", "fake receipt")
	if err != nil || got.Status != "failed" {
		t.Fatalf("reject: %v %+v", err, got)
	}
	var n int
	_ = st.DB().QueryRow(ctx, `SELECT count(*) FROM payments.outbox_payments WHERE topic='payment.rejected.v1' AND payload::text LIKE '%fake%'`).Scan(&n)
	_ = n
	var reason string
	_ = st.DB().QueryRow(ctx, `SELECT reason FROM payments.manual_receipts WHERE intent_id=$1`, in.ID).Scan(&reason)
	if reason != "fake receipt" {
		t.Errorf("reason = %q", reason)
	}
}

func strptr(s string) *string { return &s }

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (hay == needle || index(hay, needle) >= 0)
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
