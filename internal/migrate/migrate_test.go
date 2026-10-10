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
			"subscriptions", "tickets", "ticket_messages", "ticket_canned", "broadcasts", "broadcast_recipients", "audit_log", "settings", "staff_credentials", "web_sessions", "login_links",
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

// 00009 on data the M3 bot left: a ticket for every support message, many
// open per customer. Only each customer's newest open ticket stays open
// (equal created_at broken by id), or the one-open index cannot be built.
func TestSupportMigrationMergesM3Tickets(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 8); err != nil {
		t.Fatalf("up to 8: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper

	u1, u2 := "00000000-0000-0000-0000-000000000101", "00000000-0000-0000-0000-000000000102"
	if _, err := db.ExecContext(ctx, `INSERT INTO core.users (id, telegram_id) VALUES ($1, 101), ($2, 102)`, u1, u2); err != nil {
		t.Fatalf("insert users: %v", err)
	}
	type m3 struct{ id, user, text, status, at string }
	old := []m3{
		{"00000000-0000-0000-0000-0000000000a1", u1, "first", "open", "2026-01-01 10:00:00+00"},
		{"00000000-0000-0000-0000-0000000000a2", u1, "second\nwith more lines", "open", "2026-01-02 10:00:00+00"},
		{"00000000-0000-0000-0000-0000000000a3", u1, "newest", "open", "2026-01-02 10:00:00+00"},
		{"00000000-0000-0000-0000-0000000000a4", u1, "answered long ago", "closed", "2026-01-03 10:00:00+00"},
		{"00000000-0000-0000-0000-0000000000b1", u2, "only one", "open", "2026-01-01 09:00:00+00"},
	}
	for _, tk := range old {
		if _, err := db.ExecContext(ctx, `INSERT INTO core.tickets (id, user_id, category, text, status, created_at)
			VALUES ($1, $2, 'general', $3, $4, $5)`, tk.id, tk.user, tk.text, tk.status, tk.at); err != nil {
			t.Fatalf("insert ticket: %v", err)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}

	want := map[string]struct{ status, reason, subject string }{
		old[0].id: {"closed", "merged", "first"},
		old[1].id: {"closed", "merged", "second"},
		old[2].id: {"open", "", "newest"},
		old[3].id: {"closed", "", "answered long ago"},
		old[4].id: {"open", "", "only one"},
	}
	for _, tk := range old {
		var status, reason, subject, body string
		var msgs int
		err := db.QueryRowContext(ctx, `SELECT t.status, COALESCE(t.close_reason, ''), t.subject,
				(SELECT count(*) FROM core.ticket_messages m WHERE m.ticket_id = t.id),
				COALESCE((SELECT m.body FROM core.ticket_messages m WHERE m.ticket_id = t.id AND m.kind = 'customer'
					AND m.author_id = t.user_id), '')
			FROM core.tickets t WHERE t.id = $1`, tk.id).Scan(&status, &reason, &subject, &msgs, &body)
		if err != nil {
			t.Fatalf("ticket %s: %v", tk.id, err)
		}
		w := want[tk.id]
		if status != w.status || reason != w.reason || subject != w.subject {
			t.Errorf("ticket %s: %s %q %q, want %s %q %q", tk.id, status, reason, subject, w.status, w.reason, w.subject)
		}
		if msgs != 1 || body != tk.text {
			t.Errorf("ticket %s: %d messages, customer's body %q, want 1 with %q", tk.id, msgs, body, tk.text)
		}
	}
}
