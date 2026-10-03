package migrate

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func testDSN(t *testing.T) string {
	t.Helper()
	return testdb.DSN(t)
}

func dropSchemas(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	for _, s := range Schemas() {
		if _, err := db.Exec("DROP SCHEMA IF EXISTS " + s + " CASCADE"); err != nil {
			t.Fatalf("drop schema %s: %v", s, err)
		}
		if _, err := db.Exec("CREATE SCHEMA " + s); err != nil {
			t.Fatalf("create schema %s: %v", s, err)
		}
	}
}

func TestUpAppliesAllSchemas(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()

	for _, schema := range Schemas() {
		if err := Up(ctx, dsn, schema); err != nil {
			t.Fatalf("Up(%s): %v", schema, err)
		}
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper

	want := map[string][]string{
		"core": {"users", "wallets", "ledger_entries", "plans", "orders",
			"subscriptions", "tickets", "audit_log", "settings", "staff",
			"outbox_core", "inbox_core", "outbox_core_cursors", "dead_letters"},
		"payments": {"payment_intents", "manual_receipts", "ledger_entries",
			"outbox_payments", "inbox_payments", "outbox_payments_cursors"},
		"provisioner": {"xui_servers", "provision_jobs", "client_map",
			"outbox_provisioner", "inbox_provisioner", "outbox_provisioner_cursors"},
	}
	for schema, tables := range want {
		for _, table := range tables {
			var exists bool
			q := `SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = $1 AND table_name = $2)`
			if err := db.QueryRowContext(ctx, q, schema, table).Scan(&exists); err != nil {
				t.Fatalf("check %s.%s: %v", schema, table, err)
			}
			if !exists {
				t.Errorf("missing table %s.%s", schema, table)
			}
		}
	}
}

func TestUpIsIdempotent(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	for _, schema := range Schemas() {
		if err := Up(ctx, dsn, schema); err != nil {
			t.Fatalf("first Up(%s): %v", schema, err)
		}
		if err := Up(ctx, dsn, schema); err != nil {
			t.Fatalf("second Up(%s): %v", schema, err)
		}
	}
}

func TestUpRejectsUnknownSchema(t *testing.T) {
	dsn := testDSN(t)
	err := Up(context.Background(), dsn, "core; DROP TABLE users;--")
	if !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("want ErrUnknownSchema, got %v", err)
	}
}

// A missing schema used to surface as goose's "no schema has been selected to
// create in", which sent people looking in the wrong place.
func TestUpExplainsMissingSchema(t *testing.T) {
	dsn := testDSN(t)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck // test helper
	if _, err := db.Exec("DROP SCHEMA IF EXISTS provisioner CASCADE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dropSchemas(t, dsn) })
	if err := Up(context.Background(), dsn, "provisioner"); !errors.Is(err, ErrMissingSchema) {
		t.Fatalf("want ErrMissingSchema, got %v", err)
	}
}

func TestWalletBalanceConstraint(t *testing.T) {
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

	uid := "00000000-0000-0000-0000-000000000001"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO core.users (id, telegram_id) VALUES ($1, 1)`, uid); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO core.wallets (user_id, currency, balance) VALUES ($1, 'IRT', -1)`, uid)
	if err == nil {
		t.Fatal("negative wallet balance accepted; CHECK constraint missing")
	}
}

func TestLedgerIdempotencyKeyUnique(t *testing.T) {
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

	uid := "00000000-0000-0000-0000-000000000002"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO core.users (id, telegram_id) VALUES ($1, 2)`, uid); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	ins := `INSERT INTO core.ledger_entries
		(id, user_id, currency, amount, kind, idempotency_key, balance_after)
		VALUES ($1, $2, 'IRT', 100, 'topup', 'k-1', 100)`
	if _, err := db.ExecContext(ctx, ins, "00000000-0000-0000-0000-000000000011", uid); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	_, err = db.ExecContext(ctx, ins, "00000000-0000-0000-0000-000000000012", uid)
	if err == nil {
		t.Fatal("duplicate idempotency_key accepted; UNIQUE constraint missing")
	}
}
