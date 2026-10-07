package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(s.Close)
	if _, err := s.DB().Exec(ctx, `TRUNCATE core.ledger_entries, core.wallets, core.subscriptions, core.orders,
		core.plans, core.users, core.tickets, core.audit_log, core.settings, core.staff_credentials, core.web_sessions, core.login_links, core.inbox_core,
		core.dead_letters CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
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

// Many Telegram users have no public @username: registering them used to fail
// with "cannot scan NULL into *string".
func TestUserWithoutUsername(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, err := s.UpsertUser(ctx, s.Conn(), 112, "", "fa", "")
	if err != nil {
		t.Fatalf("register without username: %v", err)
	}
	if u.Username != "" {
		t.Errorf("username = %q", u.Username)
	}
	if _, err := s.GetUser(ctx, s.Conn(), u.ID); err != nil {
		t.Fatalf("read back: %v", err)
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

func credit(t *testing.T, s *Store, userID string, amount int64, key string) error {
	t.Helper()
	return s.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := s.Credit(context.Background(), tx, userID, "IRT", amount, &LedgerEntry{Kind: "topup", IdempotencyKey: key})
		return err
	})
}

func balanceAndLedgerSum(t *testing.T, s *Store, userID string) (balance, sum int64) {
	t.Helper()
	ctx := context.Background()
	if err := s.DB().QueryRow(ctx, `SELECT balance FROM core.wallets WHERE user_id=$1 AND currency='IRT'`, userID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM core.ledger_entries WHERE user_id=$1 AND currency='IRT'`, userID).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	return balance, sum
}

func TestReplayMovesNoMoney(t *testing.T) {
	s := testStore(t)
	u, _ := s.UpsertUser(context.Background(), s.Conn(), 222, "bob", "", "")
	if err := credit(t, s, u.ID, 1000, "k1"); err != nil {
		t.Fatal(err)
	}
	// A replay reports ErrDuplicateIdempotency WITHOUT touching the balance,
	// so a caller that commits after it cannot double-credit.
	err := s.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := s.Credit(context.Background(), tx, u.ID, "IRT", 1000, &LedgerEntry{Kind: "topup", IdempotencyKey: "k1"})
		if !errors.Is(err, ErrDuplicateIdempotency) {
			return fmt.Errorf("want ErrDuplicateIdempotency, got %w", err)
		}
		return nil // commit on purpose
	})
	if err != nil {
		t.Fatal(err)
	}
	if bal, sum := balanceAndLedgerSum(t, s, u.ID); bal != 1000 || sum != 1000 {
		t.Fatalf("after committed replay: balance=%d ledger=%d, want 1000/1000", bal, sum)
	}
	// The same key for a different movement is a conflict, not a replay.
	if err := credit(t, s, u.ID, 5, "k1"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reused key with another amount: want ErrIdempotencyConflict, got %v", err)
	}
}

func TestDebitsAreSignedAndCannotOverdraw(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 223, "carol", "", "")
	if err := credit(t, s, u.ID, 1000, "top"); err != nil {
		t.Fatal(err)
	}
	err := s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := s.Debit(ctx, tx, u.ID, "IRT", 2000, &LedgerEntry{Kind: "purchase", IdempotencyKey: "big"})
		return err
	})
	if !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}

	// 20 workers race to debit 100 from 1000: exactly 10 succeed, and the
	// balance always equals the sum of the (signed) ledger.
	var (
		wg sync.WaitGroup
		mu sync.Mutex
		ok int
	)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := s.WithTx(context.Background(), func(tx pgx.Tx) error {
				_, err := s.Debit(context.Background(), tx, u.ID, "IRT", 100,
					&LedgerEntry{Kind: "purchase", IdempotencyKey: fmt.Sprintf("race-%d", i)})
				return err
			})
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 10 {
		t.Errorf("concurrent debits: %d succeeded, want 10", ok)
	}
	if bal, sum := balanceAndLedgerSum(t, s, u.ID); bal != 0 || sum != 0 {
		t.Fatalf("balance=%d ledger sum=%d, want 0/0", bal, sum)
	}
}

func TestLedgerIsAppendOnly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 224, "dave", "", "")
	if err := credit(t, s, u.ID, 10, "once"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.ledger_entries SET amount = 1000000 WHERE user_id = $1`, u.ID); err == nil {
		t.Fatal("ledger row was edited")
	}
	if _, err := s.DB().Exec(ctx, `DELETE FROM core.ledger_entries WHERE user_id = $1`, u.ID); err == nil {
		t.Fatal("ledger row was deleted")
	}
}

func TestCreateOrderIdempotencyConflict(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, _ := s.UpsertUser(ctx, s.Conn(), 225, "a", "", "")
	b, _ := s.UpsertUser(ctx, s.Conn(), 226, "b", "", "")
	p := &Plan{NameI18n: map[string]string{"en": "P"}, Kind: "time", Price: 10, Currency: "IRT", Enabled: true}
	_ = s.UpsertPlan(ctx, s.Conn(), p)
	o, inserted, err := s.CreateOrder(ctx, s.Conn(), &Order{UserID: a.ID, PlanID: p.ID, Type: "new", Status: "awaiting_payment", Amount: 10, Currency: "IRT", IdempotencyKey: "same"})
	if err != nil || !inserted {
		t.Fatalf("create: %v inserted=%v", err, inserted)
	}
	again, inserted, err := s.CreateOrder(ctx, s.Conn(), &Order{UserID: a.ID, PlanID: p.ID, Type: "new", Status: "awaiting_payment", Amount: 10, Currency: "IRT", IdempotencyKey: "same"})
	if err != nil || inserted || again.ID != o.ID {
		t.Fatalf("replay: %v inserted=%v", err, inserted)
	}
	if _, _, err := s.CreateOrder(ctx, s.Conn(), &Order{UserID: b.ID, PlanID: p.ID, Type: "new", Status: "awaiting_payment", Amount: 10, Currency: "IRT", IdempotencyKey: "same"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("another user's key returned their order: %v", err)
	}
}

func TestInboxClaimAndDeadLetter(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	var first, second bool
	_ = s.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		first, err = s.ClaimInbox(ctx, tx, "payments:e1")
		return err
	})
	_ = s.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		second, err = s.ClaimInbox(ctx, tx, "payments:e1")
		return err
	})
	if !first || second {
		t.Fatalf("claims: first=%v second=%v", first, second)
	}
	d := DeadLetter{Source: "payments", EventID: "e2", Topic: "x.v1", Payload: []byte(`{}`), Error: "bad"}
	if err := s.InsertDeadLetter(ctx, s.Conn(), d); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertDeadLetter(ctx, s.Conn(), d); err != nil {
		t.Fatalf("dead letter not idempotent: %v", err)
	}
}
