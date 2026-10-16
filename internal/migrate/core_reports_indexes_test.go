package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00018 adds the three indexes the sales reports need (and nothing else); Down
// removes them and touches no data.
func TestReportsIndexesMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 17); err != nil {
		t.Fatalf("up to 17: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	indexDef := func(name string) string {
		var def string
		err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'core' AND indexname = $1`, name).Scan(&def)
		if err == sql.ErrNoRows {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return def
	}
	for _, name := range []string{"outbox_core_reports_idx", "outbox_core_order_paid_idx", "orders_unpaid_end_idx"} {
		if indexDef(name) != "" {
			t.Fatalf("%s exists before 00018", name)
		}
	}
	var before int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'core'`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	if err := upTo(ctx, dsn, "core", 18); err != nil { // not Up: later migrations add indexes of their own
		t.Fatalf("Up: %v", err)
	}
	if def := indexDef("outbox_core_reports_idx"); !strings.Contains(def, "(topic, created_at)") || !strings.Contains(def, "order.paid.v1") ||
		!strings.Contains(def, "payment.rejected.v1") {
		t.Errorf("outbox index: %s", def)
	}
	if def := indexDef("outbox_core_order_paid_idx"); !strings.Contains(def, "(payload ->> 'order_id'::text)") || !strings.Contains(def, "order.paid.v1") {
		t.Errorf("order id index: %s", def)
	}
	if def := indexDef("orders_unpaid_end_idx"); !strings.Contains(def, "(updated_at)") || !strings.Contains(def, "cancelled") ||
		!strings.Contains(def, "refunded_at IS NULL") {
		t.Errorf("orders index: %s", def)
	}
	var after int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'core'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+3 {
		t.Errorf("%d indexes before, %d after: 00018 adds exactly three", before, after)
	}

	if err := downTo(ctx, dsn, "core", 17); err != nil {
		t.Fatalf("Down: %v", err)
	}
	for _, name := range []string{"outbox_core_reports_idx", "outbox_core_order_paid_idx", "orders_unpaid_end_idx"} {
		if indexDef(name) != "" {
			t.Errorf("%s survived Down", name)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
