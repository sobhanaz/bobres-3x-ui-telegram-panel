package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00060 on a store from before: a product that already has a price keeps it, as a fixed one (nothing changes on deploy); a product with no
// price yet follows the USDT rate; a product that arrives later follows it too; Down drops the column and Up brings it back.
func TestSupplierPricingMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 59); err != nil {
		t.Fatalf("up to 59: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	exec(`INSERT INTO core.supplier_products (id, name, cost_micro, enabled, sell_price) VALUES ('priced', 'Priced', 2600000, true, 700000)`)
	exec(`INSERT INTO core.supplier_products (id, name, cost_micro) VALUES ('new', 'New', 1200000)`)
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	mode := func(id string) (m string) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT price_mode FROM core.supplier_products WHERE id = $1`, id).Scan(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got := mode("priced"); got != "fixed" {
		t.Fatalf("a product with a price: %q, want fixed", got)
	}
	if got := mode("new"); got != "auto" {
		t.Fatalf("a product with no price: %q, want auto", got)
	}
	exec(`INSERT INTO core.supplier_products (id, name) VALUES ('later', 'Later')`)
	if got := mode("later"); got != "auto" {
		t.Fatalf("a product that arrives later: %q, want auto", got)
	}
	if _, err := db.ExecContext(ctx, `UPDATE core.supplier_products SET price_mode = 'sometimes' WHERE id = 'later'`); err == nil {
		t.Fatal("a price mode outside auto and fixed was accepted")
	}

	if err := downTo(ctx, dsn, "core", 59); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var has bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'supplier_products' AND column_name = 'price_mode')`).Scan(&has); err != nil || has {
		t.Fatalf("after Down: price_mode present %v %v", has, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if got := mode("priced"); got != "fixed" {
		t.Fatalf("after Up again: %q, want fixed", got)
	}
}
