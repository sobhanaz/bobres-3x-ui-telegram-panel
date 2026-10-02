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
	if err := migrate.Up(ctx, dsn, "payments"); err != nil {
		t.Skipf("migration: %v", err)
	}
	s, err := New(ctx, dsn)
	if err != nil {
		t.Skipf("connect: %v", err)
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
	if err != nil || len(list) != 1 {
		t.Fatalf("pending list: %v %d", err, len(list))
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
