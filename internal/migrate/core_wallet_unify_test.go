package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00039: the ledger takes the wallet_convert kind, a license order records its USDT list price and the rate, and Down puts the constraint back
// (for new rows: the ledger is append-only, so the conversion entries that are there stay).
func TestWalletUnifyMigration(t *testing.T) {
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
	const user = "00000000-0000-7000-8000-000000000b01"
	if err := exec(`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 2001)`); err != nil {
		t.Fatal(err)
	}
	entry := func(kind, key string) error {
		return exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after) VALUES (gen_random_uuid(), $1, 'IRT', 1, $2, $3, 1)`, user, kind, key)
	}
	if err := entry("wallet_convert", "c1"); err != nil {
		t.Fatalf("a conversion entry: %v", err)
	}
	if err := entry("made_up", "c2"); err == nil {
		t.Error("a made-up kind")
	}
	if err := exec(`INSERT INTO core.vendor_orders (id, user_id, kind, tier, months, amount, currency, idempotency_key, list_amount, list_currency, rate)
		VALUES (gen_random_uuid(), $1, 'new', 'standard', 1, 1086400, 'IRT', 'k1', 4000000, 'USDT', 271600)`, user); err != nil {
		t.Fatalf("an order with its list price and rate: %v", err)
	}
	if err := exec(`INSERT INTO core.vendor_orders (id, user_id, kind, tier, months, amount, currency, idempotency_key, rate)
		VALUES (gen_random_uuid(), $1, 'new', 'standard', 1, 1, 'IRT', 'k2', 0)`, user); err == nil {
		t.Error("a rate of 0")
	}
	if err := downTo(ctx, dsn, "core", 38); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if err := entry("wallet_convert", "c3"); err == nil {
		t.Error("the old constraint lets a new conversion entry in")
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
