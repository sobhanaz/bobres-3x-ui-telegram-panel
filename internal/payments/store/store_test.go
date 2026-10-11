package store

import (
	"context"
	"errors"
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
	if err := migrate.Up(ctx, dsn, "payments"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(s.Close)
	for _, tbl := range []string{"manual_receipts", "ledger_entries", "payment_intents"} {
		if _, err := s.DB().Exec(ctx, "TRUNCATE payments."+tbl+" CASCADE"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

const uid = "00000000-0000-7000-8000-00000000b001"

func TestIntentLifecycleAndIdempotency(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	in, err := s.CreateIntent(ctx, nil, &Intent{
		UserID: uid, Provider: "manual_card", Amount: 50000, Currency: "IRT",
		Status: "pending", IdempotencyKey: "i1",
	})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateIntent(ctx, nil, &Intent{
		UserID: uid, Provider: "manual_card", Amount: 50000, Currency: "IRT",
		Status: "pending", IdempotencyKey: "i1",
	})
	if err != nil || again.ID != in.ID {
		t.Fatalf("idempotent create: %v %v", err, again)
	}
	file := "file123"
	ref := "998877"
	if err := s.SaveReceipt(ctx, nil, &Receipt{IntentID: in.ID, ReceiptFile: &file, ReferenceNumber: &ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionConfirming(ctx, nil, in.ID); err != nil {
		t.Fatal(err)
	}
	// Second submit blocked: not pending anymore.
	if _, err := s.TransitionConfirming(ctx, nil, in.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("double submit: %v", err)
	}
	list, err := s.ListPendingReceipts(ctx, 10)
	if err != nil || len(list) != 1 || *list[0].Receipt.ReferenceNumber != "998877" || list[0].PossibleDuplicate {
		t.Fatalf("pending list: %v %+v", err, list)
	}

	// A second receipt with the same reference number is flagged, not refused.
	in2, _ := s.CreateIntent(ctx, nil, &Intent{UserID: uid, Provider: "manual_card", Amount: 7, Currency: "IRT", Status: "pending", IdempotencyKey: "i1b"})
	if err := s.SaveReceipt(ctx, nil, &Receipt{IntentID: in2.ID, ReceiptFile: &file, ReferenceNumber: &ref}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.TransitionConfirming(ctx, nil, in2.ID)
	list, _ = s.ListPendingReceipts(ctx, 10)
	if len(list) != 2 || !list[0].PossibleDuplicate || !list[1].PossibleDuplicate {
		t.Fatalf("duplicate reference not flagged: %+v", list)
	}
}

func TestReviewRaceOnlyOneApproverWins(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	in, _ := s.CreateIntent(ctx, nil, &Intent{
		UserID: uid, Provider: "manual_crypto", Amount: 100, Currency: "USDT",
		Status: "pending", IdempotencyKey: "i2",
	})
	tx := "0xabc"
	net := "TRC20"
	_ = s.SaveReceipt(ctx, nil, &Receipt{IntentID: in.ID, TXID: &tx, Network: &net})
	if _, err := s.TransitionConfirming(ctx, nil, in.ID); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wins := make(chan string, 8)
	for i, decision := range []string{"approved", "rejected", "approved", "rejected", "approved"} {
		wg.Add(1)
		go func(i int, decision string) {
			defer wg.Done()
			err := s.WithTx(context.Background(), func(tx pgx.Tx) error {
				if decision == "approved" {
					if _, err := s.TransitionSucceeded(context.Background(), tx, in.ID); err != nil {
						return err
					}
					if err := s.AppendLedgerCredit(context.Background(), tx, uid, "USDT", 100, "intent", in.ID, "app-"+in.ID); err != nil {
						return err
					}
				} else {
					if _, err := s.TransitionFailed(context.Background(), tx, in.ID); err != nil {
						return err
					}
				}
				return s.ReviewReceipt(context.Background(), tx, in.ID, "00000000-0000-7000-8000-0000000000e1", decision, "ok")
			})
			if err == nil {
				wins <- decision
			}
		}(i, decision)
	}
	wg.Wait()
	close(wins)
	var results []string
	for d := range wins {
		results = append(results, d)
	}
	if len(results) != 1 {
		t.Fatalf("%d concurrent reviews committed, want exactly 1: %v", len(results), results)
	}

	final, _ := s.GetIntent(ctx, nil, in.ID)
	if final.Status != "succeeded" && final.Status != "failed" {
		t.Errorf("final status %q", final.Status)
	}
}

func TestLedgerCreditIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.AppendLedgerCredit(ctx, nil, uid, "USDT", 200, "intent", "00000000-0000-7000-8000-0000000000c1", "cred-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendLedgerCredit(ctx, nil, uid, "USDT", 200, "intent", "00000000-0000-7000-8000-0000000000c1", "cred-1"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM payments.ledger_entries WHERE user_id=$1`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("ledger rows = %d, want 1 (idempotent replay)", n)
	}
	if err := s.AppendLedgerCredit(ctx, nil, uid, "USDT", 50, "intent", "00000000-0000-7000-8000-0000000000c2", "cred-2"); err != nil {
		t.Fatal(err)
	}
	var bal int64
	_ = s.DB().QueryRow(ctx, `SELECT balance_after FROM payments.ledger_entries WHERE idempotency_key='cred-2'`).Scan(&bal)
	if bal != 250 {
		t.Errorf("running balance = %d, want 250", bal)
	}
}

// The payment history lists every intent newest first, with its receipt and
// review, filtered by user, order, status and provider.
func TestListIntentsWithReceiptsAndFilters(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const other = "00000000-0000-7000-8000-00000000b002"
	order := "00000000-0000-7000-8000-00000000c001"
	card, err := s.CreateIntent(ctx, nil, &Intent{UserID: uid, OrderID: &order, Provider: "manual_card", Amount: 50000,
		Currency: "IRT", Status: "pending", IdempotencyKey: "h1"})
	if err != nil {
		t.Fatal(err)
	}
	file, ref := "file-1", "12345"
	if err := s.SaveReceipt(ctx, nil, &Receipt{IntentID: card.ID, ReceiptFile: &file, ReferenceNumber: &ref}); err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		return s.ReviewReceipt(ctx, tx, card.ID, other, "rejected", "blurry")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIntent(ctx, nil, &Intent{UserID: other, Provider: "zarinpal", Amount: 70000,
		Currency: "IRT", Status: "pending", IdempotencyKey: "h2"}); err != nil {
		t.Fatal(err)
	}

	all, total, err := s.ListIntents(ctx, IntentFilter{}, 10, 0)
	if err != nil || total != 2 || len(all) != 2 {
		t.Fatalf("all: %d of %d, %v", len(all), total, err)
	}
	if all[0].Intent.Provider != "zarinpal" || all[0].Receipt != nil {
		t.Fatalf("newest first, without a receipt: %+v", all[0])
	}
	r := all[1].Receipt
	if r == nil || *r.ReceiptFile != file || *r.ReferenceNumber != ref || *r.Decision != "rejected" ||
		*r.Reason != "blurry" || *r.ReviewedBy != other || r.ReviewedAt == nil {
		t.Fatalf("receipt and review: %+v", r)
	}
	for _, c := range []struct {
		f    IntentFilter
		want int64
	}{
		{IntentFilter{UserID: uid}, 1},
		{IntentFilter{OrderID: order}, 1},
		{IntentFilter{Provider: "zarinpal"}, 1},
		{IntentFilter{Status: "pending"}, 2},
		{IntentFilter{Status: "succeeded"}, 0},
		{IntentFilter{IntentID: card.ID}, 1},
	} {
		if _, n, err := s.ListIntents(ctx, c.f, 10, 0); err != nil || n != c.want {
			t.Errorf("%+v: %d, want %d (%v)", c.f, n, c.want, err)
		}
	}
	if page, total, err := s.ListIntents(ctx, IntentFilter{}, 1, 1); err != nil || total != 2 || len(page) != 1 || page[0].Intent.ID != card.ID {
		t.Fatalf("second page: %v %d %v", page, total, err)
	}
}
