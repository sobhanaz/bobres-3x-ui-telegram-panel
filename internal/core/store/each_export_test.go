package store

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

// EachLedger and EachAudit feed ledger.csv and audit.csv: newest first, capped,
// streamed in a read-only transaction whose statement PostgreSQL cancels at
// ExportTimeout.

func seedLedger(x *seed, n int) string {
	u := x.user(77)
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		x.ledger(u, "IRT", int64(1000+i), "topup", "", "", "each-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Minute))
	}
	return u
}

func TestEachLedgerStreamsNewestFirstAndCaps(t *testing.T) {
	x := newSeed(t)
	u := seedLedger(x, 5)
	var amounts []int64
	more, err := x.s.EachLedger(x.ctx, LedgerFilter{UserID: u}, 3, func(r LedgerRow) error {
		amounts = append(amounts, r.Amount)
		return nil
	})
	if err != nil || !more || len(amounts) != 3 || amounts[0] != 1004 || amounts[2] != 1002 {
		t.Fatalf("capped: %v %v %v", more, err, amounts)
	}
	amounts = nil
	more, err = x.s.EachLedger(x.ctx, LedgerFilter{UserID: u}, 5, func(r LedgerRow) error { amounts = append(amounts, r.Amount); return nil })
	if err != nil || more || len(amounts) != 5 {
		t.Fatalf("exactly max: %v %v %v", more, err, amounts)
	}
	// Nothing matches: no rows, no error.
	n := 0
	more, err = x.s.EachLedger(x.ctx, LedgerFilter{UserID: u, Kind: "no_such_kind"}, 5, func(LedgerRow) error { n++; return nil })
	if err != nil || more || n != 0 {
		t.Fatalf("empty: %v %v %d", more, err, n)
	}
	// An error from the callback ends the stream and comes back; the connection is given back.
	stop := errors.New("stop")
	if _, err := x.s.EachLedger(x.ctx, LedgerFilter{UserID: u}, 5, func(LedgerRow) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("callback error: %v", err)
	}
	if n := x.s.DB().Stat().AcquiredConns(); n != 0 {
		t.Errorf("connections still held: %d", n)
	}
}

// With no deadline on the context, only PostgreSQL's statement timeout can end
// a query that waits on a lock: ErrReportTimeout shows it is set.
func TestEachLedgerAndAuditStatementsAreTimed(t *testing.T) {
	x := newSeed(t)
	seedLedger(x, 2)
	if err := x.s.WriteAudit(x.ctx, x.s.Conn(), &Audit{Action: "each.test", Entity: "seed"}); err != nil {
		t.Fatal(err)
	}
	old := ExportTimeout
	ExportTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ExportTimeout = old })
	for name, c := range map[string]struct {
		table string
		run   func() error
	}{
		"ledger": {"core.ledger_entries", func() error {
			_, err := x.s.EachLedger(x.ctx, LedgerFilter{}, 10, func(LedgerRow) error { return nil })
			return err
		}},
		"audit": {"core.audit_log", func() error {
			_, err := x.s.EachAudit(x.ctx, AuditFilter{Action: "each.test"}, 10, func(AuditRow) error { return nil })
			return err
		}},
	} {
		tx, err := x.s.DB().Begin(x.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(x.ctx, `LOCK TABLE `+c.table+` IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		err = c.run()
		_ = tx.Rollback(x.ctx)
		if !errors.Is(err, ErrReportTimeout) || time.Since(start) > 5*time.Second {
			t.Errorf("%s: %v after %s", name, err, time.Since(start))
		}
		// Once the lock is gone the same call works.
		if err := c.run(); err != nil {
			t.Errorf("%s after the lock: %v", name, err)
		}
	}
}

func TestEachAuditStreamsNewestFirstAndCaps(t *testing.T) {
	x := newSeed(t)
	for i := 0; i < 4; i++ {
		id := "e" + strconv.Itoa(i)
		if err := x.s.WriteAudit(x.ctx, x.s.Conn(), &Audit{Action: "each.order", Entity: "seed", EntityID: &id}); err != nil {
			t.Fatal(err)
		}
	}
	var ids []string
	more, err := x.s.EachAudit(x.ctx, AuditFilter{Action: "each.order"}, 3, func(a AuditRow) error {
		ids = append(ids, *a.EntityID)
		return nil
	})
	if err != nil || !more || len(ids) != 3 || ids[0] != "e3" || ids[2] != "e1" {
		t.Fatalf("capped: %v %v %v", more, err, ids)
	}
	ids = nil
	more, err = x.s.EachAudit(x.ctx, AuditFilter{Action: "each.order"}, 4, func(a AuditRow) error { ids = append(ids, *a.EntityID); return nil })
	if err != nil || more || len(ids) != 4 {
		t.Fatalf("exactly max: %v %v %v", more, err, ids)
	}
	n := 0
	if more, err = x.s.EachAudit(x.ctx, AuditFilter{Action: "no.such"}, 4, func(AuditRow) error { n++; return nil }); err != nil || more || n != 0 {
		t.Fatalf("empty: %v %v %d", more, err, n)
	}
	stop := errors.New("stop")
	if _, err := x.s.EachAudit(x.ctx, AuditFilter{Action: "each.order"}, 4, func(AuditRow) error { return stop }); !errors.Is(err, stop) {
		t.Errorf("callback error: %v", err)
	}
	if n := x.s.DB().Stat().AcquiredConns(); n != 0 {
		t.Errorf("connections still held: %d", n)
	}
}
