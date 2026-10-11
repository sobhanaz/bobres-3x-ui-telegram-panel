package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const ownerTG = 900001

func testService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	for _, schema := range []string{"core", "payments"} {
		if err := migrate.Up(ctx, dsn, schema); err != nil {
			t.Fatalf("migrate %s: %v", schema, err)
		}
	}
	s, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(s.Close)
	if _, err := s.DB().Exec(ctx, `TRUNCATE core.ledger_entries, core.wallets, core.subscriptions, core.orders,
		core.discount_redemptions, core.discount_codes,
		core.plans, core.users, core.settings, core.inbox_core, core.dead_letters, core.outbox_core,
		core.outbox_core_cursors, payments.outbox_payments, payments.outbox_payments_cursors CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return New(s, Config{OwnerTelegramID: ownerTG}), s
}

func seedPlan(t *testing.T, s *store.Store, trial bool, price int64) *store.Plan {
	t.Helper()
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
	t.Helper()
	u, err := s.UpsertUser(context.Background(), s.Conn(), tg, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func fund(t *testing.T, s *store.Store, userID string, amount int64) {
	t.Helper()
	err := s.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := s.Credit(context.Background(), tx, userID, "IRT", amount,
			&store.LedgerEntry{Kind: "topup", IdempotencyKey: fmt.Sprintf("fund-%s-%d", userID, amount)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ledger returns the wallet balance and the signed ledger sum; they must match.
func ledger(t *testing.T, s *store.Store, userID string) (balance, sum int64, rows int) {
	t.Helper()
	ctx := context.Background()
	w, err := s.GetWallet(ctx, s.Conn(), userID, "IRT")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(ctx, `SELECT COALESCE(SUM(amount),0), count(*) FROM core.ledger_entries WHERE user_id=$1 AND currency='IRT'`, userID).Scan(&sum, &rows); err != nil {
		t.Fatal(err)
	}
	return w.Balance, sum, rows
}

func outboxEvents(t *testing.T, s *store.Store, topic string) []json.RawMessage {
	t.Helper()
	rows, err := s.DB().Query(context.Background(), `SELECT payload FROM core.outbox_core WHERE topic = $1 ORDER BY id`, topic)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var p json.RawMessage
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func TestCreateOrderIdempotent(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1001)
	p := seedPlan(t, s, false, 1000)

	o1, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "idem-1"})
	if err != nil {
		t.Fatal(err)
	}
	if o1.Status != "awaiting_payment" || o1.Amount != 1000 || o1.Type != "new" {
		t.Errorf("bad order: %+v", o1)
	}
	o2, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "idem-1"})
	if err != nil || o2.ID != o1.ID {
		t.Fatalf("idempotent retry: %v (%s vs %s)", err, o2.ID, o1.ID)
	}
	if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, Type: "renew", IdempotencyKey: "idem-2"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("renewals are not available yet, got %v", err)
	}
}

func TestWalletOrderPaysOnceAndAnnouncesIt(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1002)
	p := seedPlan(t, s, false, 500)
	fund(t, s, u.ID, 1000)

	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "idem-2"})
	paid, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID)
	if err != nil || paid.Status != "paid" {
		t.Fatalf("pay: %v %+v", err, paid)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		t.Fatalf("replay of a paid order must succeed without charging: %v", err)
	}
	if bal, sum, _ := ledger(t, s, u.ID); bal != 500 || sum != 500 {
		t.Fatalf("balance=%d ledger=%d, want 500/500", bal, sum)
	}
	evs := outboxEvents(t, s, events.OrderPaid)
	if len(evs) != 1 {
		t.Fatalf("order.paid events = %d, want 1", len(evs))
	}
	var e events.OrderPaidEvent
	_ = json.Unmarshal(evs[0], &e)
	if e.OrderID != o.ID || e.TelegramID != 1002 || e.Source != events.PaidByWallet {
		t.Errorf("bad event: %+v", e)
	}
}

// A double tap on "pay with wallet" used to debit twice: the order was read
// without a lock and the duplicate ledger key was swallowed after the balance
// had already moved. Both payers are held at the wallet so they interleave.
func TestWalletDoubleTapChargesOnce(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1003)
	p := seedPlan(t, s, false, 400)
	fund(t, s, u.ID, 1000)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "tap"})

	blocker, err := s.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM core.wallets WHERE user_id=$1 FOR UPDATE`, u.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.PayOrderWithWallet(ctx, o.ID, u.ID)
		}(i)
	}
	time.Sleep(300 * time.Millisecond)
	_ = blocker.Rollback(ctx)
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Errorf("payer error: %v", err)
		}
	}
	if bal, sum, _ := ledger(t, s, u.ID); bal != 600 || sum != 600 {
		t.Fatalf("one order charged twice? balance=%d ledger=%d, want 600/600", bal, sum)
	}
	if n := len(outboxEvents(t, s, events.OrderPaid)); n != 1 {
		t.Fatalf("order.paid published %d times", n)
	}
}

func TestWalletPayChecks(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1004)
	other := seedUser(t, s, 1005)
	p := seedPlan(t, s, false, 5000)
	o, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "chk"})
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("want ErrInsufficientFunds, got %v", err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, other.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("someone else's order: want ErrForbidden, got %v", err)
	}
}

func TestTrialOncePerUser(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1006)
	tp := seedPlan(t, s, true, 0)

	tr1, err := svc.StartTrial(ctx, u.ID, tp.ID, "idem-trial-1")
	if err != nil {
		t.Fatal(err)
	}
	if tr1.Amount != 0 || tr1.Status != "paid" {
		t.Errorf("bad trial order: %+v", tr1)
	}
	again, err := svc.StartTrial(ctx, u.ID, tp.ID, "idem-trial-1")
	if err != nil || again.ID != tr1.ID {
		t.Fatalf("retry with the same key must return the same trial: %v", err)
	}
	if _, err := svc.StartTrial(ctx, u.ID, tp.ID, "idem-trial-2"); !errors.Is(err, ErrTrialAlreadyUsed) {
		t.Fatalf("second trial: want ErrTrialAlreadyUsed, got %v", err)
	}
	if n := len(outboxEvents(t, s, events.OrderPaid)); n != 1 {
		t.Fatalf("order.paid for trial published %d times", n)
	}
}

// CreateOrder used to accept trial plans (price 0 => "paid"), giving unlimited
// free service around the one-trial rule.
func TestTrialPlansOnlyThroughStartTrial(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1007)
	tp := seedPlan(t, s, true, 0)
	if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: tp.ID, IdempotencyKey: "free"}); !errors.Is(err, ErrPlanUnavailable) {
		t.Fatalf("trial plan via CreateOrder: want ErrPlanUnavailable, got %v", err)
	}
	tp.Enabled = false
	_ = s.UpsertPlan(ctx, s.Conn(), tp)
	if _, err := svc.StartTrial(ctx, u.ID, tp.ID, "off"); !errors.Is(err, ErrPlanUnavailable) {
		t.Fatalf("disabled trial plan: want ErrPlanUnavailable, got %v", err)
	}
}

func TestBannedUserCannotBuy(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 1008)
	p := seedPlan(t, s, false, 10)
	tp := seedPlan(t, s, true, 0)
	if err := s.SetUserRole(ctx, s.Conn(), u.ID, "user", "banned"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "b1"}); !errors.Is(err, ErrUserBanned) {
		t.Fatalf("CreateOrder: %v", err)
	}
	if _, err := svc.StartTrial(ctx, u.ID, tp.ID, "b2"); !errors.Is(err, ErrUserBanned) {
		t.Fatalf("StartTrial: %v", err)
	}
}

func TestOwnerIsBootstrapped(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	owner, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: ownerTG, Language: "fa"})
	if err != nil || owner.Role != "owner" {
		t.Fatalf("owner: %v %+v", err, owner)
	}
	user, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: ownerTG + 1})
	if err != nil || user.Role != "user" {
		t.Fatalf("regular user: %v %+v", err, user)
	}
	if _, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: 5, Language: "de"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported language accepted: %v", err)
	}
}
