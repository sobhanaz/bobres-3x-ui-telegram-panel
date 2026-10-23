package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00038: one intent per customer, for 1 or 12 months, gone with the customer, and Down drops the table.
func TestVendorIntentsMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
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
	const user = "00000000-0000-7000-8000-000000000a01"
	if err := exec(`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 1001)`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.vendor_intents (user_id, tier, months, expires_at) VALUES ($1, 'standard', 1, now() + interval '1 hour')`, user); err != nil {
		t.Fatalf("an intent: %v", err)
	}
	if err := exec(`INSERT INTO core.vendor_intents (user_id, tier, months, expires_at) VALUES ($1, 'pro', 12, now())`, user); err == nil {
		t.Error("two intents for one customer")
	}
	if err := exec(`UPDATE core.vendor_intents SET months = 6 WHERE user_id = $1`, user); err == nil {
		t.Error("an intent for 6 months")
	}
	if err := exec(`DELETE FROM core.users WHERE id = $1`, user); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.vendor_intents`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the intent outlived the customer: %d %v", n, err)
	}
	if err := downTo(ctx, dsn, "core", 37); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'vendor_intents'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after Down: %d (%v)", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
