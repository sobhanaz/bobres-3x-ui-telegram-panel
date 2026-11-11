package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00059 on a store from before: every existing order and ledger entry is not internal and keeps its numbers; the ledger stays append-only
// except for the internal flag (a change to the flag alone is allowed, a change to anything else or a delete is refused); Down drops both
// columns and restores the strict trigger, and Up brings the columns back empty.
func TestInternalOrdersMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 58); err != nil {
		t.Fatalf("up to 58: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	const (
		uid  = "00000000-0000-0000-0000-000000005901"
		plan = "00000000-0000-0000-0000-000000005902"
		ord  = "00000000-0000-0000-0001-000000005903"
		led  = "00000000-0000-0000-0002-000000005904"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + uid + `', 5901)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES ('` + ord + `', '` + uid + `', '` + plan + `', 'new', 'active', 1000, 'IRT', 'k-internal')`,
		`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after)
			VALUES ('` + led + `', '` + uid + `', 'IRT', -1000, 'purchase', 'order', '` + ord + `', 'k-internal-led', 0)`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	flags := func() (order, ledger bool) {
		if err := db.QueryRowContext(ctx, `SELECT internal FROM core.orders WHERE id = $1`, ord).Scan(&order); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT internal FROM core.ledger_entries WHERE id = $1`, led).Scan(&ledger); err != nil {
			t.Fatal(err)
		}
		return order, ledger
	}
	if o, l := flags(); o || l {
		t.Fatalf("existing rows are internal: order %v, ledger %v", o, l)
	}

	// The flag alone may change, both ways, on the append-only ledger.
	if err := exec(`UPDATE core.ledger_entries SET internal = true WHERE id = $1`, led); err != nil {
		t.Fatalf("flag the entry: %v", err)
	}
	if err := exec(`UPDATE core.orders SET internal = true WHERE id = $1`, ord); err != nil {
		t.Fatalf("flag the order: %v", err)
	}
	if o, l := flags(); !o || !l {
		t.Fatalf("after flagging: order %v, ledger %v", o, l)
	}
	// Anything beside it is refused, with or without the flag in the same statement; so is a delete.
	for _, q := range []string{
		`UPDATE core.ledger_entries SET amount = -1 WHERE id = $1`,
		`UPDATE core.ledger_entries SET internal = false, balance_after = 5 WHERE id = $1`,
		`UPDATE core.ledger_entries SET kind = 'adjust' WHERE id = $1`,
		`DELETE FROM core.ledger_entries WHERE id = $1`,
	} {
		err := exec(q, led)
		if err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := exec(`UPDATE core.ledger_entries SET internal = false WHERE id = $1`, led); err != nil {
		t.Fatalf("unflag the entry: %v", err)
	}

	// The partial indexes are there.
	for _, idx := range []string{"orders_internal_idx", "ledger_entries_internal_idx"} {
		var has bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'core' AND indexname = $1)`, idx).Scan(&has); err != nil || !has {
			t.Fatalf("index %s: %v %v", idx, has, err)
		}
	}

	if err := downTo(ctx, dsn, "core", 58); err != nil {
		t.Fatalf("Down: %v", err)
	}
	for _, col := range []string{"orders.internal", "ledger_entries.internal"} {
		var has bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'core' AND table_name || '.' || column_name = $1)`, col).Scan(&has); err != nil || has {
			t.Fatalf("after Down: %s present %v %v", col, has, err)
		}
	}
	// The strict trigger is back: no update of the ledger at all.
	if err := exec(`UPDATE core.ledger_entries SET created_at = created_at WHERE id = $1`, led); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("after Down an update of the ledger: %v", err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if o, l := flags(); o || l {
		t.Fatalf("after Up again: order %v, ledger %v", o, l)
	}
}
