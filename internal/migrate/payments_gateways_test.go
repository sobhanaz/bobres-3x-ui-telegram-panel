package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// payments 00005 on a schema with gateway history: gateway_configs starts
// empty (payments seeds it from the environment at startup), and the schema
// refuses a switched-on gateway without a merchant id, an unknown provider and
// a second row per provider.
func TestGatewayConfigsMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "payments", 4); err != nil {
		t.Fatalf("up to 4: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	if _, err := db.ExecContext(ctx, `INSERT INTO payments.gateway_events (provider, kind, outcome, detail)
		VALUES ('zarinpal', 'create', 'error', '{"error":"timeout"}')`); err != nil {
		t.Fatalf("history: %v", err)
	}
	if err := Up(ctx, dsn, "payments"); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM payments.gateway_configs`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("gateway_configs after the migration: %d %v", n, err)
	}
	for name, q := range map[string]string{
		"switched on without a merchant id": `INSERT INTO payments.gateway_configs (provider, enabled) VALUES ('zarinpal', true)`,
		"unknown provider":                  `INSERT INTO payments.gateway_configs (provider, merchant_enc) VALUES ('paypal', '\x01')`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	ins := `INSERT INTO payments.gateway_configs (provider, enabled, merchant_enc) VALUES ('zarinpal', true, '\x01')`
	if _, err := db.ExecContext(ctx, ins); err != nil {
		t.Fatalf("valid row: %v", err)
	}
	if _, err := db.ExecContext(ctx, ins); err == nil {
		t.Fatal("a second zarinpal row accepted")
	}
	var idx bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes
		WHERE schemaname = 'payments' AND indexname = 'gateway_events_outcome_idx')`).Scan(&idx); err != nil || !idx {
		t.Fatalf("report index: %v %v", idx, err)
	}
}
