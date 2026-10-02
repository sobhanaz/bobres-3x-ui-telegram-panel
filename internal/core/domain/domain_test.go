package domain

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
)

func testService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	dsn := os.Getenv("BOBRES_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///postgres?host=/tmp&port=5432&sslmode=disable"
	}
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "core"); err != nil {
		t.Skipf("migration: %v", err)
	}
	s, err := store.New(ctx, dsn)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	t.Cleanup(s.Close)
	for _, tbl := range []string{"ledger_entries", "wallets", "subscriptions", "orders", "plans", "users"} {
		if _, err := s.DB().Exec(context.Background(), "TRUNCATE core."+tbl+" CASCADE"); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	return New(s), s
}

func seedPlan(t *testing.T, s *store.Store, trial bool, price int64) *store.Plan {
	p := &store.Plan{
		NameI18n: map[string]string{"en": "P"}, Kind: "both",
		Price: price, Currency: "IRT", Enabled: true, IsTrial: trial,
	}
	if err := s.UpsertPlan(context.Background(), s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func seedUser(t *testing.T, s *store.Store, tg int64) *store.User {
	u, err := s.UpsertUser(context.Background(), s.Conn(), tg, "u", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCreateOrderIdempotent(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1001)
	p := seedPlan(t, s, false, 1000)

	o1, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, Type: "new", IdempotencyKey: "idem-1"})
	if err != nil {
		t.Fatal(err)
	}
	if o1.Status != "awaiting_payment" || o1.Amount != 1000 {
		t.Errorf("bad order: %+v", o1)
	}
	o2, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, Type: "new", IdempotencyKey: "idem-1"})
	if err != nil {
		t.Fatal(err)
	}
	if o2.ID != o1.ID {
		t.Error("idempotent retry created a second order")
	}
}

func TestWalletOrderPaysImmediately(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1002)
	p := seedPlan(t, s, false, 500)

	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := s.Credit(ctx, tx, u.ID, "IRT", 1000,
			&store.LedgerEntry{UserID: u.ID, Currency: "IRT", Amount: 1000, Kind: "topup", IdempotencyKey: "top1"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, Type: "new", IdempotencyKey: "idem-2"})
	paid, err := svc.PayOrderWithWallet(ctx, o.ID, "idem-pay-1")
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	if paid.Status != "paid" {
		t.Errorf("status = %q", paid.Status)
	}
	w, _ := s.GetWallet(ctx, s.Conn(), u.ID, "IRT")
	if w.Balance != 500 {
		t.Errorf("balance = %d, want 500", w.Balance)
	}

	// replay: same idempotency key must not double-debit
	_, err = svc.PayOrderWithWallet(ctx, o.ID, "idem-pay-1")
	w2, _ := s.GetWallet(ctx, s.Conn(), u.ID, "IRT")
	if w2.Balance != 500 {
		t.Errorf("replay debited twice: balance = %d", w2.Balance)
	}
	_ = err
}

func TestWalletPayInsufficient(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1003)
	p := seedPlan(t, s, false, 5000)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, Type: "new", IdempotencyKey: "idem-3"})
	_, err := svc.PayOrderWithWallet(ctx, o.ID, "idem-pay-2")
	if !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}
}

func TestTrialOncePerUser(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1004)
	tp := seedPlan(t, s, true, 0)

	ok, err := svc.CanStartTrial(ctx, u.ID)
	if err != nil || !ok {
		t.Fatalf("trial eligibility blocked for new user: %v ok=%v", err, ok)
	}
	tr1, err := svc.StartTrial(ctx, u.ID, tp.ID, "idem-trial-1")
	if err != nil {
		t.Fatal(err)
	}
	if tr1.Amount != 0 || tr1.Status != "paid" {
		t.Errorf("bad trial order: %+v", tr1)
	}
	ok, _ = svc.CanStartTrial(ctx, u.ID)
	if ok {
		t.Error("still eligible after trial")
	}
	if _, err := svc.StartTrial(ctx, u.ID, tp.ID, "idem-trial-2"); !errors.Is(err, ErrTrialAlreadyUsed) {
		t.Fatalf("second trial: want ErrTrialAlreadyUsed, got %v", err)
	}
}
