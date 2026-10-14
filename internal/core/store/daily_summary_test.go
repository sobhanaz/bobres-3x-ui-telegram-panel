package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func claimStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	if err := testdb.Truncate(context.Background(), s.DB(), `core.daily_summary_claims`); err != nil {
		t.Fatal(err)
	}
	return s
}

func claimRows(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.daily_summary_claims`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func claim(t *testing.T, s *Store, day string) bool {
	t.Helper()
	var won bool
	err := s.WithTx(context.Background(), func(tx pgx.Tx) error {
		var err error
		won, err = s.ClaimDailySummary(context.Background(), tx, day, "Asia/Tehran", 2)
		return err
	})
	if err != nil {
		t.Fatalf("claim %s: %v", day, err)
	}
	return won
}

// A day is claimed once; another day is another claim; the row says which zone
// the day was read in and how many people were told.
func TestDailySummaryClaimIsTakenOnce(t *testing.T) {
	s := claimStore(t)
	ctx := context.Background()
	if got, err := s.DailySummaryClaimed(ctx, s.Conn(), "2026-10-13"); err != nil || got {
		t.Fatalf("claimed before any claim: %v %v", got, err)
	}
	if !claim(t, s, "2026-10-13") {
		t.Fatal("the first claim lost")
	}
	if claim(t, s, "2026-10-13") {
		t.Fatal("the same day was claimed twice")
	}
	if !claim(t, s, "2026-10-14") {
		t.Fatal("the next day could not be claimed")
	}
	for day, want := range map[string]bool{"2026-10-12": false, "2026-10-13": true, "2026-10-14": true} {
		if got, err := s.DailySummaryClaimed(ctx, s.Conn(), day); err != nil || got != want {
			t.Errorf("%s claimed: %v %v, want %v", day, got, err, want)
		}
	}
	var zone string
	var recipients int
	var at time.Time
	if err := s.DB().QueryRow(ctx, `SELECT zone, recipients, claimed_at FROM core.daily_summary_claims WHERE day = '2026-10-13'`).Scan(&zone, &recipients, &at); err != nil ||
		zone != "Asia/Tehran" || recipients != 2 || time.Since(at) > time.Minute {
		t.Errorf("the claim: %q %d %v (%v)", zone, recipients, at, err)
	}
	// A claim with nobody to tell, or without a zone, is no claim at all.
	for name, run := range map[string]func(tx pgx.Tx) error{
		"no recipients": func(tx pgx.Tx) error { _, err := s.ClaimDailySummary(ctx, tx, "2026-10-20", "UTC", 0); return err },
		"no zone":       func(tx pgx.Tx) error { _, err := s.ClaimDailySummary(ctx, tx, "2026-10-20", "", 1); return err },
		"not a date":    func(tx pgx.Tx) error { _, err := s.ClaimDailySummary(ctx, tx, "2026-13-45", "UTC", 1); return err },
	} {
		if err := s.WithTx(ctx, run); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if n := claimRows(t, s); n != 2 {
		t.Errorf("%d claims", n)
	}
}

// The claim belongs to its transaction: one that rolls back (the events could
// not be written) leaves the day free for the next try, and while it is open
// nobody else sees it. The open transaction has a connection of its own, so the
// pool's size does not matter.
func TestDailySummaryClaimRollsBackWithItsTransaction(t *testing.T) {
	s := claimStore(t)
	ctx := context.Background()
	c, err := pgx.Connect(ctx, testdb.DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx) //nolint:errcheck // test connection
	tx, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // already rolled back below
	if won, err := s.ClaimDailySummary(ctx, tx, "2026-10-13", "UTC", 1); err != nil || !won {
		t.Fatalf("claim: %v %v", won, err)
	}
	if got, _ := s.DailySummaryClaimed(ctx, tx, "2026-10-13"); !got {
		t.Error("the claim is not seen inside its own transaction")
	}
	if got, _ := s.DailySummaryClaimed(ctx, s.Conn(), "2026-10-13"); got {
		t.Error("an open claim is seen by others")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if claimRows(t, s) != 0 || !claim(t, s, "2026-10-13") {
		t.Error("a rolled-back claim blocked the day")
	}
}

// Many claimers at once: exactly one wins and one row is written. They share the
// pool, so how many connections it has changes how many run at the same time,
// not the answer.
func TestDailySummaryConcurrentClaimersOneWins(t *testing.T) {
	s := claimStore(t)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	start := make(chan struct{})
	for range 12 {
		wg.Go(func() {
			<-start
			if claim(t, s, "2026-10-13") {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}
	close(start)
	wg.Wait()
	if wins != 1 || claimRows(t, s) != 1 {
		t.Errorf("%d claimers won, %d rows", wins, claimRows(t, s))
	}
}

// A claimer that meets a claim another transaction still holds waits for it: it
// loses when that one commits and wins when that one rolls back. The two
// transactions run on connections of their own, so the pool's size does not matter.
func TestDailySummaryClaimerWaitsForAnOpenClaim(t *testing.T) {
	for _, commit := range []bool{true, false} {
		name := "rollback"
		if commit {
			name = "commit"
		}
		t.Run(name, func(t *testing.T) {
			s := claimStore(t)
			ctx := context.Background()
			dsn := testdb.DSN(t)
			open := func() (pgx.Tx, func()) {
				c, err := pgx.Connect(ctx, dsn)
				if err != nil {
					t.Fatal(err)
				}
				tx, err := c.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				return tx, func() { _ = tx.Rollback(ctx); _ = c.Close(ctx) }
			}
			a, closeA := open()
			defer closeA()
			b, closeB := open()
			defer closeB()
			if won, err := s.ClaimDailySummary(ctx, a, "2026-10-13", "UTC", 1); err != nil || !won {
				t.Fatalf("A: %v %v", won, err)
			}
			type result struct {
				won bool
				err error
			}
			got := make(chan result, 1)
			go func() {
				won, err := s.ClaimDailySummary(ctx, b, "2026-10-13", "UTC", 1)
				got <- result{won, err}
			}()
			// B is waiting on A's row (asked through A, whose snapshot is refreshed each time).
			deadline := time.Now().Add(10 * time.Second)
			for {
				if _, err := a.Exec(ctx, `SELECT pg_stat_clear_snapshot()`); err != nil {
					t.Fatal(err)
				}
				var waiting int
				if err := a.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("B never waited for A's claim")
				}
				time.Sleep(10 * time.Millisecond)
			}
			select {
			case r := <-got:
				t.Fatalf("B did not wait: %+v", r)
			default:
			}
			if commit {
				if err := a.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			} else if err := a.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case r := <-got:
				if r.err != nil || r.won == commit {
					t.Fatalf("B after A's commit=%v: won=%v err=%v (it wins only when A rolls back)", commit, r.won, r.err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("B never finished")
			}
			if err := b.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if claimRows(t, s) != 1 {
				t.Errorf("%d rows", claimRows(t, s))
			}
		})
	}
}

// A setting's change time is when it got its value: the worker reads when the
// switch was turned on.
func TestSettingChangedAt(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, ok, err := s.SettingChangedAt(ctx, s.Conn(), "reports.daily_summary"); err != nil || ok {
		t.Fatalf("no row: %v %v", ok, err)
	}
	if err := s.SetSetting(ctx, s.Conn(), "reports.daily_summary", []byte(`"true"`)); err != nil {
		t.Fatal(err)
	}
	first, ok, err := s.SettingChangedAt(ctx, s.Conn(), "reports.daily_summary")
	if err != nil || !ok || time.Since(first) > time.Minute || time.Until(first) > time.Minute {
		t.Fatalf("after a change: %v %v %v", first, ok, err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := s.SetSetting(ctx, s.Conn(), "reports.daily_summary", []byte(`"false"`)); err != nil {
		t.Fatal(err)
	}
	if second, _, _ := s.SettingChangedAt(ctx, s.Conn(), "reports.daily_summary"); !second.After(first) {
		t.Errorf("a new value kept the old time: %v then %v", first, second)
	}
	if err := s.DeleteSetting(ctx, s.Conn(), "reports.daily_summary"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SettingChangedAt(ctx, s.Conn(), "reports.daily_summary"); ok {
		t.Error("a deleted setting has a time")
	}
}

// The zone of the newest claim (by the date it is about, not by when it was
// written) is what the worker compares the current zone with.
func TestLastDailySummaryZone(t *testing.T) {
	s := claimStore(t)
	ctx := context.Background()
	if zone, ok, err := s.LastDailySummaryZone(ctx, s.Conn()); err != nil || ok || zone != "" {
		t.Fatalf("no claim yet: %q %v %v", zone, ok, err)
	}
	claimIn := func(day, zone string) {
		t.Helper()
		err := s.WithTx(ctx, func(tx pgx.Tx) error {
			won, err := s.ClaimDailySummary(ctx, tx, day, zone, 1)
			if err == nil && !won {
				t.Fatalf("%s not won", day)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	claimIn("2026-10-14", "Pacific/Auckland")
	claimIn("2026-10-13", "Asia/Tehran") // an older day claimed later
	if zone, ok, err := s.LastDailySummaryZone(ctx, s.Conn()); err != nil || !ok || zone != "Pacific/Auckland" {
		t.Errorf("the newest day's zone: %q %v %v", zone, ok, err)
	}
	claimIn("2026-10-15", "America/Los_Angeles")
	if zone, _, err := s.LastDailySummaryZone(ctx, s.Conn()); err != nil || zone != "America/Los_Angeles" {
		t.Errorf("after the 15th: %q %v", zone, err)
	}
}
