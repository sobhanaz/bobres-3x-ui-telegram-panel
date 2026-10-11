package migrate

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
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
			"outbox_core", "inbox_core", "outbox_core_cursors", "dead_letters", "reseller_tiers", "reseller_tier_prices", "resellers",
			"plan_servers", "install", "license"},
		"payments": {"payment_intents", "manual_receipts", "ledger_entries",
			"outbox_payments", "inbox_payments", "outbox_payments_cursors", "gateway_configs"},
		"provisioner": {"xui_servers", "provision_jobs", "client_map",
			"outbox_provisioner", "inbox_provisioner", "outbox_provisioner_cursors", "server_inbounds"},
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

// downTo rolls the schema's migrations back to version (the tests of a
// migration's Down).
func downTo(ctx context.Context, dsn, schema string, version int64) error {
	globalMu.Lock()
	defer globalMu.Unlock()
	goose.SetBaseFS(fsys)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck // test helper
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		return err
	}
	return goose.DownToContext(ctx, db, "migrations/"+schema, version)
}

// 00010 lets a reseller's wallet go down to -credit_limit and adds the
// commission and settlement kinds. Going back is refused while anyone owes
// money; otherwise the old check returns, the kinds stay (append-only
// ledger) and resellers become customers.
func TestResellersMigrationDown(t *testing.T) {
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

	uid := "00000000-0000-0000-0000-000000000201"
	tier := "00000000-0000-0000-0000-0000000002a1"
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id, role) VALUES ('` + uid + `', 201, 'reseller')`,
		`INSERT INTO core.reseller_tiers (id, name, discount_percent, commission_percent) VALUES ('` + tier + `', 'Gold', 20, 10)`,
		`INSERT INTO core.resellers (user_id, tier_id) VALUES ('` + uid + `', '` + tier + `')`,
		`INSERT INTO core.wallets (user_id, currency, balance, credit_limit) VALUES ('` + uid + `', 'IRT', -500, 1000)`,
		`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after)
			VALUES ('00000000-0000-0000-0000-000000000211', '` + uid + `', 'IRT', 100, 'commission', 'commission:x', 100)`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	// The new checks hold.
	if err := exec(`UPDATE core.wallets SET balance = -1001 WHERE user_id = $1`, uid); err == nil {
		t.Fatal("a balance below -credit_limit was accepted")
	}
	if err := exec(`UPDATE core.wallets SET credit_limit = -1 WHERE user_id = $1`, uid); err == nil {
		t.Fatal("a negative credit limit was accepted")
	}
	if err := exec(`INSERT INTO core.reseller_tiers (id, name, discount_percent, commission_percent) VALUES (gen_random_uuid(), 'X', 91, 0)`); err == nil {
		t.Fatal("a 91% discount was accepted")
	}
	if err := exec(`DELETE FROM core.reseller_tiers WHERE id = $1`, tier); err == nil {
		t.Fatal("a tier in use was deleted")
	}

	if err := downTo(ctx, dsn, "core", 9); err == nil || !strings.Contains(err.Error(), "negative balance") {
		t.Fatalf("Down with a debt: %v", err)
	}
	if err := exec(`UPDATE core.wallets SET balance = 0 WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if err := downTo(ctx, dsn, "core", 9); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var role string
	var hasLimit, hasTiers bool
	if err := db.QueryRowContext(ctx, `SELECT role,
		EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'wallets' AND column_name = 'credit_limit'),
		EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'reseller_tiers')
		FROM core.users WHERE id = $1`, uid).Scan(&role, &hasLimit, &hasTiers); err != nil {
		t.Fatal(err)
	}
	if role != "user" || hasLimit || hasTiers {
		t.Fatalf("after Down: role %s, credit_limit column %v, tiers table %v", role, hasLimit, hasTiers)
	}
	if err := exec(`UPDATE core.wallets SET balance = -1 WHERE user_id = $1`, uid); err == nil {
		t.Fatal("after Down a negative balance was accepted")
	}
	if err := exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after)
		VALUES ('00000000-0000-0000-0000-000000000212', $1, 'IRT', 5, 'settlement', 'settlement:y', 5)`, uid); err != nil {
		t.Fatalf("after Down the ledger refused a settlement: %v", err)
	}
	// And up again.
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
