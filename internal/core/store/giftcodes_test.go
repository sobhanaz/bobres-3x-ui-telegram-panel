package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func giftHash(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func newGift(t *testing.T, s *Store, hash byte, max, perCustomer int) *GiftCode {
	t.Helper()
	g := &GiftCode{Tail: "ABCD", Generated: true, Amount: 1000, Currency: "IRT", MaxRedemptions: max, PerCustomerLimit: perCustomer, Enabled: true}
	var ok bool
	if err := s.WithTx(context.Background(), func(tx pgx.Tx) (err error) {
		ok, err = s.InsertGiftCode(context.Background(), tx, g, giftHash(hash))
		return
	}); err != nil || !ok {
		t.Fatalf("insert: %v %v", ok, err)
	}
	return g
}

// redeemRow writes a redemption the way the domain does, but by hand: the guards under test are the database's.
func redeemRow(t *testing.T, s *Store, g *GiftCode, userID string, seq int, key string) error {
	t.Helper()
	return s.WithTx(context.Background(), func(tx pgx.Tx) error {
		e := &LedgerEntry{Kind: "gift_code", RefType: ptr("gift_code"), RefID: &g.ID, IdempotencyKey: fmt.Sprintf("gift:%s:%s:%d:%s", g.ID, userID, seq, key)}
		w, err := s.Credit(context.Background(), tx, userID, g.Currency, g.Amount, e)
		if err != nil {
			return err
		}
		return s.InsertGiftRedemption(context.Background(), tx, &GiftRedemption{GiftCodeID: g.ID, UserID: userID, Seq: seq, RequestKey: key,
			Amount: g.Amount, Currency: g.Currency, LedgerEntryID: e.ID, BalanceAfter: w.Balance})
	})
}

func ptr(s string) *string { return &s }

func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code + " " + pe.ConstraintName
	}
	return ""
}

func TestGiftCodeRowsAndTheirGuards(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u1, err := s.UpsertUser(ctx, s.Conn(), 4001, "a", "en", "")
	if err != nil {
		t.Fatal(err)
	}
	u2, _ := s.UpsertUser(ctx, s.Conn(), 4002, "b", "en", "")

	g := newGift(t, s, 1, 1, 1)
	if g.ID == "" || g.CreatedAt.IsZero() {
		t.Fatalf("insert did not set the id and time: %+v", g)
	}
	// The same hash is the same code: nothing is inserted and nothing fails (the caller draws again).
	dup := &GiftCode{Tail: "ZZZZ", Amount: 1, Currency: "IRT", MaxRedemptions: 1, PerCustomerLimit: 1, Enabled: true}
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		ok, err := s.InsertGiftCode(ctx, tx, dup, giftHash(1))
		if ok || err != nil {
			t.Errorf("the same hash twice: %v %v", ok, err)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// The lock by hash finds exactly that code, with its hash.
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		got, err := s.LockGiftCodeByHash(ctx, tx, giftHash(1))
		if err != nil || got.ID != g.ID || !bytes.Equal(got.Hash, giftHash(1)) || got.Amount != 1000 {
			t.Errorf("lock by hash: %+v %v", got, err)
		}
		if _, err := s.LockGiftCodeByHash(ctx, tx, giftHash(2)); !errors.Is(err, ErrNotFound) {
			t.Errorf("another hash: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The database refuses what the application would never do, as a last guard. A code that may be used once is
	// used once; a second redemption cannot be recorded whatever the caller checked.
	if err := redeemRow(t, s, g, u1.ID, 1, "k1"); err != nil {
		t.Fatalf("a redemption: %v", err)
	}
	if err := redeemRow(t, s, g, u2.ID, 1, "k2"); err == nil || pgCode(err) != "23514 gift_codes_redeemed_check" {
		t.Fatalf("a redemption past the code's limit: %v (%s)", err, pgCode(err))
	}
	got, err := s.GetGiftCode(ctx, s.Conn(), g.ID)
	if err != nil || got.Redeemed != 1 || got.LastRedeemedAt == nil {
		t.Fatalf("the code after a refused second redemption: %+v %v", got, err)
	}
	if w, _ := s.GetWallet(ctx, s.Conn(), u2.ID, "IRT"); w.Balance != 0 {
		t.Fatalf("the refused redemption left %d in the wallet", w.Balance)
	}

	// One per customer and number; one per customer and request.
	many := newGift(t, s, 3, 10, 5)
	if err := redeemRow(t, s, many, u1.ID, 1, "a"); err != nil {
		t.Fatal(err)
	}
	if err := redeemRow(t, s, many, u1.ID, 1, "b"); err == nil || pgCode(err) != "23505 gift_code_redemptions_seq_key" {
		t.Fatalf("the same number twice: %v (%s)", err, pgCode(err))
	}
	if err := redeemRow(t, s, many, u1.ID, 2, "a"); err == nil || pgCode(err) != "23505 gift_code_redemptions_request_key" {
		t.Fatalf("the same request twice: %v (%s)", err, pgCode(err))
	}
	if err := redeemRow(t, s, many, u2.ID, 1, "a"); err != nil {
		t.Fatalf("another customer, the same key text and number: %v", err)
	}
	if n, err := s.CountGiftRedemptions(ctx, s.Conn(), many.ID, u1.ID); err != nil || n != 1 {
		t.Fatalf("count: %d %v", n, err)
	}
	r, hash, err := s.GiftRedemptionByKey(ctx, s.Conn(), u1.ID, "a")
	if err != nil || r.GiftCodeID != many.ID || r.Seq != 1 || r.Amount != 1000 || !bytes.Equal(hash, giftHash(3)) {
		t.Fatalf("by key: %+v %v", r, err)
	}
	if _, _, err := s.GiftRedemptionByKey(ctx, s.Conn(), u1.ID, "none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown key: %v", err)
	}
	// Enabled switches, and it is the only thing that does.
	if err := s.WithTx(ctx, func(tx pgx.Tx) error { return s.SetGiftCodeEnabled(ctx, tx, many.ID, false) }); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetGiftCode(ctx, s.Conn(), many.ID); got.Enabled || got.Amount != 1000 || got.Redeemed != 2 {
		t.Fatalf("disabled: %+v", got)
	}
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		return s.SetGiftCodeEnabled(ctx, tx, "00000000-0000-0000-0000-00000000beef", true)
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a code that is not there: %v", err)
	}
	// The ledger entry belongs to one redemption only.
	var entry string
	if err := s.DB().QueryRow(ctx, `SELECT ledger_entry_id::text FROM core.gift_code_redemptions WHERE gift_code_id = $1 AND user_id = $2`, many.ID, u1.ID).Scan(&entry); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(ctx, `INSERT INTO core.gift_code_redemptions (id, gift_code_id, user_id, seq, request_key, amount, currency, ledger_entry_id, balance_after)
		VALUES (gen_random_uuid(), $1, $2, 2, 'again', 1, 'IRT', $3, 1)`, many.ID, u1.ID, entry); err == nil || pgCode(err) != "23505 gift_code_redemptions_ledger_entry_id_key" {
		t.Fatalf("one ledger entry for two redemptions: %v (%s)", err, pgCode(err))
	}
}

func TestRecordGiftFailureCountsLocksAndForgets(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 4101, "a", "en", "")
	const limit = 5
	window, lock := time.Hour, 90*time.Minute
	record := func() GiftFailure {
		t.Helper()
		f, err := s.RecordGiftFailure(ctx, s.Conn(), u.ID, window, lock, limit)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	if d, err := s.GiftLockedFor(ctx, s.Conn(), u.ID); err != nil || d != 0 {
		t.Fatalf("a customer who never failed: %v %v", d, err)
	}
	for i := 1; i <= limit; i++ {
		f := record()
		if f.Failures != i || f.Locked != (i == limit) {
			t.Fatalf("wrong code %d: %+v", i, f)
		}
		d, _ := s.GiftLockedFor(ctx, s.Conn(), u.ID)
		if (i < limit) != (d == 0) {
			t.Fatalf("locked for %v after %d", d, i)
		}
	}
	// Locked for the whole lock, to the second; one more wrong code that gets through counts and extends it.
	if d, _ := s.GiftLockedFor(ctx, s.Conn(), u.ID); d < lock-time.Minute || d > lock {
		t.Fatalf("locked for %v, want about %v", d, lock)
	}
	if f := record(); f.Failures != limit+1 || f.Locked {
		t.Fatalf("a wrong code after the lock: %+v", f)
	}
	// The lock runs out by itself; the window too, and then the count starts over.
	if _, err := s.DB().Exec(ctx, `UPDATE core.gift_code_failures SET locked_until = now() - interval '1 second', window_start = now() - interval '61 minutes'`); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GiftLockedFor(ctx, s.Conn(), u.ID); d != 0 {
		t.Fatalf("still locked for %v", d)
	}
	if f := record(); f.Failures != 1 || f.Locked {
		t.Fatalf("the first after the window: %+v", f)
	}
	// Inside the window the count goes on from where it was, whatever time passed since the last one.
	if _, err := s.DB().Exec(ctx, `UPDATE core.gift_code_failures SET window_start = now() - interval '59 minutes'`); err != nil {
		t.Fatal(err)
	}
	if f := record(); f.Failures != 2 {
		t.Fatalf("inside the window: %+v", f)
	}
	// A different customer has a count of their own.
	v, _ := s.UpsertUser(ctx, s.Conn(), 4102, "b", "en", "")
	if f, err := s.RecordGiftFailure(ctx, s.Conn(), v.ID, window, lock, limit); err != nil || f.Failures != 1 {
		t.Fatalf("another customer: %+v %v", f, err)
	}
}

// 60 wrong codes at once, whatever the pool: all counted, and exactly one is the one that closes the entry.
func TestRecordGiftFailureAtOnce(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 4201, "a", "en", "")
	const tries, limit = 60, 5
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		locked int
		seen   = map[int]bool{}
	)
	for range tries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := s.RecordGiftFailure(ctx, s.Conn(), u.ID, time.Hour, time.Hour, limit)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if f.Locked {
				locked++
			}
			if seen[f.Failures] {
				t.Errorf("count %d was answered twice", f.Failures)
			}
			seen[f.Failures] = true
		}()
	}
	wg.Wait()
	if locked != 1 || len(seen) != tries {
		t.Fatalf("%d locked answers, %d different counts, for %d wrong codes", locked, len(seen), tries)
	}
	var n int
	if err := s.DB().QueryRow(ctx, `SELECT failures FROM core.gift_code_failures WHERE user_id = $1`, u.ID).Scan(&n); err != nil || n != tries {
		t.Fatalf("%d counted of %d (%v)", n, tries, err)
	}
}

// A credit that would pass what a bigint holds is refused whole, as a wrapped balance would be a negative one.
func TestCreditRefusesAnOverflowingBalance(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.UpsertUser(ctx, s.Conn(), 4301, "a", "en", "")
	credit := func(amount int64, key string) error {
		return s.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := s.Credit(ctx, tx, u.ID, "IRT", amount, &LedgerEntry{Kind: "topup", IdempotencyKey: key})
			return err
		})
	}
	if err := credit(math.MaxInt64-10, "big"); err != nil {
		t.Fatalf("a credit up to the largest balance: %v", err)
	}
	if err := credit(11, "one-too-many"); !errors.Is(err, ErrBalanceOverflow) {
		t.Fatalf("a credit past it: %v", err)
	}
	if err := credit(10, "exactly"); err != nil {
		t.Fatalf("a credit to exactly the largest balance: %v", err)
	}
	w, _ := s.GetWallet(ctx, s.Conn(), u.ID, "IRT")
	var sum int64
	if err := s.DB().QueryRow(ctx, `SELECT COALESCE(sum(amount) FILTER (WHERE amount > 0), 0) FROM core.ledger_entries WHERE user_id = $1 AND idempotency_key <> 'one-too-many'`, u.ID).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if w.Balance != math.MaxInt64 || sum != math.MaxInt64 {
		t.Fatalf("balance %d, ledger %d", w.Balance, sum)
	}
	var refused int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM core.ledger_entries WHERE idempotency_key = 'one-too-many'`).Scan(&refused); err != nil || refused != 0 {
		t.Fatalf("the refused credit left %d entries (%v)", refused, err)
	}
}
