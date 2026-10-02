package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("BOBRES_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///postgres?host=/tmp&port=5432&sslmode=disable"
	}
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "core"); err != nil {
		t.Skipf("postgres unavailable or migration failed: %v", err)
	}
	s, err := New(ctx, dsn)
	if err != nil {
		t.Skipf("core store connect: %v", err)
	}
	t.Cleanup(s.Close)
	// clean slate for deterministic tests
	for _, tbl := range []string{"ledger_entries", "wallets", "subscriptions", "orders",
		"plans", "users", "tickets", "audit_log", "settings", "staff"} {
		if _, err := s.DB().Exec(context.Background(), "TRUNCATE core."+tbl+" CASCADE"); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	return s
}

func TestUpsertGetUser(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, err := s.UpsertUser(ctx, s.Conn(), 111, "alice", "en", "")
	if err != nil {
		t.Fatal(err)
	}
	if u.Language != "en" || u.Role != "user" {
		t.Errorf("bad user: %+v", u)
	}
	u2, err := s.UpsertUser(ctx, s.Conn(), 111, "alice2", "fa", "")
	if err != nil {
		t.Fatal(err)
	}
	if u2.ID != u.ID || u2.Username != "alice2" || u2.Language != "fa" {
		t.Errorf("upsert conflict wrong: %+v vs %+v", u, u2)
	}
	got, err := s.GetUserByTelegramID(ctx, s.Conn(), 111)
	if err != nil || got.ID != u.ID {
		t.Fatalf("get by telegram: %v %+v", err, got)
	}
	if _, err := s.GetUser(ctx, s.Conn(), "00000000-0000-0000-0000-000000000099"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestPlanCRUD(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := &Plan{
		NameI18n: map[string]string{"fa": "برنزه تست", "en": "Test"},
		Kind:     "both", Price: 50000, Currency: "IRT", Enabled: true, Sort: 1,
	}
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPlan(ctx, s.Conn(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NameI18n["en"] != "Test" || got.Price != 50000 {
		t.Errorf("bad plan: %+v", got)
	}
	list, err := s.ListPlans(ctx, s.Conn(), false)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	p.Enabled = false
	if err := s.UpsertPlan(ctx, s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListPlans(ctx, s.Conn(), false)
	if len(list) != 0 {
		t.Error("disabled plan listed as enabled")
	}
}

func TestCreditDebitIdempotentAndBalance(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 222, "bob", "", "")

	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		e := &LedgerEntry{UserID: u.ID, Currency: "IRT", Amount: 1000, Kind: "topup", IdempotencyKey: "k1"}
		_, err := s.Credit(ctx, tx, u.ID, "IRT", 1000, e)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var bal int64
	var kind string
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		w, err := s.Credit(ctx, tx, u.ID, "IRT", 1000, &LedgerEntry{UserID: u.ID, Currency: "IRT", Amount: 1000, Kind: "topup", IdempotencyKey: "k1"})
		if err != nil {
			return err
		}
		bal = w.Balance
		return nil
	})
	if !errors.Is(err, ErrDuplicateIdempotency) && err != nil {
		// replay either errors with ErrDuplicateIdempotency (tx rolls back) — acceptable
		t.Fatalf("unexpected: %v", err)
	}
	err = s.DB().QueryRow(ctx, `SELECT balance FROM core.wallets WHERE user_id=$1`, u.ID).Scan(&bal)
	if err != nil || bal != 1000 {
		t.Fatalf("balance after replay = %d, want 1000 (%v)", bal, err)
	}
	err = s.DB().QueryRow(ctx, `SELECT kind FROM core.ledger_entries WHERE user_id=$1`, u.ID).Scan(&kind)
	if err != nil || kind != "topup" {
		t.Fatalf("ledger: %v %q", err, kind)
	}

	// debit more than balance fails and rolls back
	err = s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := s.Debit(ctx, tx, u.ID, "IRT", 2000, &LedgerEntry{UserID: u.ID, Currency: "IRT", Amount: 2000, Kind: "purchase", IdempotencyKey: "k2"})
		return err
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}

	// concurrent debits must not overdraw: 20 workers try to debit 100 from a 1000 balance
	// with unique idempotency keys; exactly 10 must succeed.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var okCount, failCount int
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "race-" + string(rune('a'+i))
			err := s.WithTx(context.Background(), func(tx pgx.Tx) error {
				_, err := s.Debit(context.Background(), tx, u.ID, "IRT", 100,
					&LedgerEntry{UserID: u.ID, Currency: "IRT", Amount: 100, Kind: "purchase", IdempotencyKey: key})
				return err
			})
			mu.Lock()
			if err == nil {
				okCount++
			} else {
				failCount++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if okCount != 10 {
		t.Errorf("concurrent debits: %d succeeded, want 10; failures=%d", okCount, failCount)
	}
}
