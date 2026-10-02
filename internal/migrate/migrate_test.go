package migrate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("BOBRES_TEST_DATABASE_URL")
	if dsn == "" {
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			candidate := "postgres:///postgres?host=/tmp&port=5432&sslmode=disable"
			db, err := sql.Open("pgx", candidate)
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if db.PingContext(ctx) == nil {
					_ = db.Close()
					return candidate
				}
				_ = db.Close()
			}
		}
		dsn = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	_ = db.Close()
	return dsn
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
			"outbox_core", "inbox_core"},
		"payments": {"payment_intents", "manual_receipts", "ledger_entries",
			"outbox_payments", "inbox_payments"},
		"provisioner": {"xui_servers", "provision_jobs", "client_map",
			"outbox_provisioner", "inbox_provisioner"},
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
	testDSN(t)
	dsn := os.Getenv("BOBRES_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///postgres?host=/tmp&port=5432&sslmode=disable"
	}
	err := Up(context.Background(), dsn, "core; DROP TABLE users;--")
	if !errors.Is(err, ErrUnknownSchema) {
		t.Fatalf("want ErrUnknownSchema, got %v", err)
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
