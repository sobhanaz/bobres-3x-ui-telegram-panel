package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00057 on a store from before: an existing order has no chosen server (NULL) and takes one; the ledger takes the
// kind commission_reversal and keeps every kind it had. Down drops the column, keeps the ledger's commission_reversal
// rows (it is append-only) but refuses new ones, and Up brings both back.
func TestOrderServerMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 56); err != nil {
		t.Fatalf("up to 56: %v", err)
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
		uid    = "00000000-0000-0000-0000-000000005701"
		plan   = "00000000-0000-0000-0000-000000005702"
		ord    = "00000000-0000-0000-0001-000000005703"
		server = "00000000-0000-7000-8000-000000005704"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + uid + `', 5701)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES ('` + ord + `', '` + uid + `', '` + plan + `', 'new', 'provision_failed', 1000, 'IRT', 'k-server')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	entry := func(kind, key string) error {
		return exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after)
			VALUES (gen_random_uuid(), $1, 'IRT', -10, $2, $3, 0)`, uid, kind, key)
	}
	if err := entry("commission_reversal", "cr:before"); err == nil {
		t.Fatal("commission_reversal was accepted before 00057")
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	chosen := func() (n sql.NullString) {
		if err := db.QueryRowContext(ctx, `SELECT server_id::text FROM core.orders WHERE id = $1`, ord).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := chosen(); n.Valid {
		t.Fatalf("an existing order got a server: %q", n.String)
	}
	if err := exec(`UPDATE core.orders SET server_id = $2 WHERE id = $1`, ord, server); err != nil || chosen().String != server {
		t.Fatalf("a chosen server: %v", err)
	}
	for kind, key := range map[string]string{"commission_reversal": "cr:1", "cashback_reversal": "cb:1", "affiliate_payout": "ap:1"} {
		if err := entry(kind, key); err != nil {
			t.Fatalf("kind %s after Up: %v", kind, err)
		}
	}
	if err := entry("no_such_kind", "x:1"); err == nil {
		t.Fatal("an unknown kind was accepted")
	}

	if err := downTo(ctx, dsn, "core", 56); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var has bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'orders' AND column_name = 'server_id')`).Scan(&has); err != nil || has {
		t.Fatalf("after Down: column present %v %v", has, err)
	}
	var kept int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.ledger_entries WHERE kind = 'commission_reversal'`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("after Down: %d commission_reversal rows, %v", kept, err)
	}
	if err := entry("commission_reversal", "cr:2"); err == nil {
		t.Fatal("a new commission_reversal was accepted after Down")
	}
	if err := entry("cashback_reversal", "cb:2"); err != nil {
		t.Fatalf("an older kind after Down: %v", err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if n := chosen(); n.Valid {
		t.Fatalf("after Up again: server %q", n.String)
	}
	if err := entry("commission_reversal", "cr:3"); err != nil {
		t.Fatalf("after Up again: %v", err)
	}
}
