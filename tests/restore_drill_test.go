package tests

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backupsvc"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

// Test fixtures, not secrets.
var (
	drillKey        = []byte("drill-backup-key-0123456789abcdef") //nolint:gosec // test fixture, gitleaks:allow
	drillMasterKeys = map[string]string{
		"BOBRES_CORE_MASTER_KEY":        "drill-core-master-key",        // gitleaks:allow
		"BOBRES_PROVISIONER_MASTER_KEY": "drill-provisioner-master-key", // gitleaks:allow
		"BOBRES_PAYMENTS_MASTER_KEY":    "drill-payments-master-key",    // gitleaks:allow
	}
)

// restoreFlags restore over what is there (owners kept), all or nothing.
var restoreFlags = []string{"--clean", "--if-exists", "--single-transaction", "--exit-on-error"}

// The restore drill, with the real pg_dump and pg_restore: a store filled
// through core is backed up the way the backup service does it, restored with
// pg_restore into a new database, and must come back whole: every table's
// rows, the sequences, the goose versions and the schema objects. Then the
// same backup over a store that changed since, and a restore that fails,
// which must change nothing. (`bobres restore` itself, on the real stack, is
// drilled in CI's e2e install job.)
func TestRestoreDrill(t *testing.T) {
	dsn := testdb.DSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pgDump, pgRestore := pgTools(ctx, t, dsn)

	// The stack stops when the subtest ends, so nothing writes during the backup.
	if !t.Run("seed", seedStore) {
		t.FailNow()
	}
	src := snapshot(ctx, t, dsn)
	for _, table := range []string{"core.users", "core.wallets", "core.plans", "core.orders", "core.subscriptions", "core.ledger_entries",
		"core.tickets", "core.audit_log", "payments.payment_intents", "payments.manual_receipts", "payments.ledger_entries",
		"provisioner.xui_servers", "provisioner.client_map"} {
		if v := src.tables[table]; v == "" || strings.HasPrefix(v, "0 rows") {
			t.Errorf("%s is empty after seeding: the drill would prove nothing about it", table)
		}
	}
	for schema, v := range src.versions {
		if v == 0 {
			t.Errorf("schema %s has no goose version", schema)
		}
	}

	// 1. Back up with the backup service's own runner and pg_dump, as a role
	// with svc_backup's rights.
	dir := t.TempDir()
	runner, err := backupsvc.New(backupsvc.Options{Dir: dir, Key: drillKey, Keys: drillMasterKeys,
		Dump: backupsvc.PgDump{Path: pgDump, URL: readOnlyDSN(ctx, t, dsn)}})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := runner.RunOnce(ctx)
	if err != nil || sc.Error != "" {
		t.Fatalf("backup: %v %s", err, sc.Error)
	}
	file := filepath.Join(dir, sc.File)

	// 2. Check the whole file first, as `bobres restore` does.
	f, err := os.Open(file) //nolint:gosec // the test's own backup
	if err != nil {
		t.Fatal(err)
	}
	sum, err := backup.Verify(f, drillKey)
	_ = f.Close()
	if err != nil {
		t.Fatalf("verify %s: %v", sc.File, err)
	}
	if names := slices.Sorted(slices.Values(sum.KeyNames)); !slices.Equal(names, slices.Sorted(maps.Keys(drillMasterKeys))) || sum.DumpSize == 0 {
		t.Fatalf("verify: key names %v, dump %d bytes", names, sum.DumpSize)
	}

	// 3. Restore into a new, empty database, and compare. The services migrate
	// when they start: on the restored store that must find nothing to do.
	target := newDatabase(ctx, t, dsn)
	keys, err := restoreFile(ctx, pgRestore, file, target)
	if err != nil {
		t.Fatalf("restore into a new database: %v", err)
	}
	if !maps.Equal(keys, drillMasterKeys) {
		t.Errorf("the backup's master keys differ from the ones written (names %v)", slices.Sorted(maps.Keys(keys)))
	}
	for _, s := range migrate.Schemas() {
		if err := migrate.Up(ctx, target, s); err != nil {
			t.Fatalf("migrate the restored store: %v", err)
		}
	}
	compareStates(t, "restored into a new database", src, snapshot(ctx, t, target))

	// 4. Over a store that changed after the backup: the change is gone.
	execSQL(ctx, t, target, `INSERT INTO core.users (id, telegram_id) VALUES ('00000000-0000-0000-0000-00000000d001', 990001);
		UPDATE core.plans SET enabled = NOT enabled`)
	if _, err := restoreFile(ctx, pgRestore, file, target); err != nil {
		t.Fatalf("restore over a changed store: %v", err)
	}
	compareStates(t, "restored over a changed store", src, snapshot(ctx, t, target))

	// 5. A failed restore changes nothing. --clean drops only what the backup
	// holds, so a table it does not know (one a newer migration added) keeps
	// its schema from being dropped: pg_restore fails there, and the single
	// transaction leaves the store as it was. (This is why `bobres restore`
	// drops the service schemas first, in the same transaction.)
	execSQL(ctx, t, target, `CREATE TABLE core.drill_newer (id int);
		INSERT INTO core.users (id, telegram_id) VALUES ('00000000-0000-0000-0000-00000000d002', 990002)`)
	before := snapshot(ctx, t, target)
	_, err = restoreFile(ctx, pgRestore, file, target)
	if err == nil || !strings.Contains(err.Error(), "DROP SCHEMA IF EXISTS core") {
		t.Fatalf("restore over an unknown table: want a failure at DROP SCHEMA core, got %v", err)
	}
	compareStates(t, "after a failed restore", before, snapshot(ctx, t, target))
}

// seedStore fills the store through core the way the bot does: a card
// payment the owner approves, a wallet purchase and a support ticket, which
// leave rows in all three schemas. The stack stops when t ends.
func seedStore(t *testing.T) {
	s := testenv.Start(t, ownerTG)
	ctx := context.Background()
	c := s.Core
	if _, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: ownerTG, Language: "fa"}); err != nil {
		t.Fatal(err)
	}
	plan, err := c.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: ownerTG, Plan: &corev1.Plan{
		NameI18N: map[string]string{"en": "Monthly 50 GB", "fa": "ماهانه ۵۰ گیگ"}, Kind: "both",
		DurationDays: 30, TrafficBytes: 50 << 30, Price: &commonv1.Money{Amount: 150000, Currency: "IRT"}, Enabled: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AdminSetSetting(ctx, &corev1.AdminSetSettingRequest{ActorTelegramId: ownerTG, Key: "payments.card_number", Value: "6037-0000-1111-2222"}); err != nil {
		t.Fatal(err)
	}
	u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 1001, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}

	// A card payment, approved by the owner.
	order, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: plan.GetId(), IdempotencyKey: "drill-o1"})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := c.CreatePaymentIntent(ctx, &corev1.CreatePaymentIntentRequest{UserId: u.GetId(), OrderId: order.GetId(), Provider: "manual_card", IdempotencyKey: "drill-i1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SubmitPaymentProof(ctx, &corev1.SubmitPaymentProofRequest{UserId: u.GetId(), IntentId: intent.GetId(), ReceiptFile: "tg-file", ReferenceNumber: "123456"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReviewManualPayment(ctx, &corev1.ReviewManualPaymentRequest{ActorTelegramId: ownerTG, IntentId: intent.GetId(), Decision: "approved"}); err != nil {
		t.Fatal(err)
	}
	waitOrder(t, c, order.GetId(), "active")

	// A wallet purchase after a top-up.
	if _, err := c.AdminAdjustBalance(ctx, &corev1.AdminAdjustBalanceRequest{ActorTelegramId: ownerTG, UserId: u.GetId(),
		Delta: &commonv1.Money{Amount: 200000, Currency: "IRT"}, Reason: "gift", IdempotencyKey: "drill-a1"}); err != nil {
		t.Fatal(err)
	}
	o2, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: plan.GetId(), IdempotencyKey: "drill-o2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o2.GetId(), UserId: u.GetId()}); err != nil {
		t.Fatal(err)
	}
	waitOrder(t, c, o2.GetId(), "active")

	// A support ticket (its number comes from an identity column).
	if _, err := c.CreateSupportTicket(ctx, &corev1.CreateSupportTicketRequest{UserId: u.GetId(), Category: "payment", Text: "سرویس وصل نمی\u200cشود"}); err != nil {
		t.Fatal(err)
	}
}

var pgVersionRe = regexp.MustCompile(`\(PostgreSQL\) (\d+)`)

// pgTools finds pg_dump and pg_restore (in BOBRES_TEST_PG_BINDIR, else on
// PATH). It skips the test when one is missing or is another major version
// than the server: an install dumps and restores with its server's own
// version (the backup image and the database are both postgres:16).
func pgTools(ctx context.Context, t *testing.T, dsn string) (dump, restore string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var num string
	err = conn.QueryRow(ctx, `SHOW server_version_num`).Scan(&num)
	_ = conn.Close(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		t.Fatalf("server_version_num %q: %v", num, err)
	}
	server := n / 10000
	var paths []string
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		path := tool
		if dir := os.Getenv("BOBRES_TEST_PG_BINDIR"); dir != "" {
			path = filepath.Join(dir, tool)
		}
		path, err := exec.LookPath(path)
		if err != nil {
			t.Skipf("restore drill skipped: %s not found (install the PostgreSQL %d client tools, or set BOBRES_TEST_PG_BINDIR): %v", tool, server, err)
		}
		out, err := exec.CommandContext(ctx, path, "--version").Output() //nolint:gosec // pg_dump or pg_restore, found above
		m := pgVersionRe.FindSubmatch(out)
		if err != nil || m == nil {
			t.Skipf("restore drill skipped: cannot tell the version of %s: %v %q", path, err, out)
		}
		if v, _ := strconv.Atoi(string(m[1])); v != server {
			t.Skipf("restore drill skipped: %s is PostgreSQL %d but the server is %d (set BOBRES_TEST_PG_BINDIR to the %d tools)", path, v, server, server)
		}
		paths = append(paths, path)
	}
	return paths[0], paths[1]
}

// readOnlyDSN is dsn as a new login role with only pg_read_all_data, the
// rights svc_backup has on an install (deploy/postgres-init.sh), so the dump
// is one the backup service can take. Where the test role may not create
// roles, the dump runs as the test role.
func readOnlyDSN(ctx context.Context, t *testing.T, dsn string) string {
	t.Helper()
	role, pw := "bobres_test_backup_"+randHex(t, 6), randHex(t, 16)
	if err := tryExec(ctx, dsn, fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s'`, role, pw)); err != nil {
		t.Logf("dumping as the test role: no read-only role (%v)", err)
		return dsn
	}
	t.Cleanup(func() {
		if err := tryExec(context.Background(), dsn, `DROP ROLE IF EXISTS `+role); err != nil {
			t.Logf("could not drop role %s: %v", role, err)
		}
	})
	execSQL(ctx, t, dsn, `GRANT pg_read_all_data TO `+role)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, pw)
	return u.String()
}

// newDatabase creates an empty database next to dsn's, dropped when the test
// ends, and returns its DSN.
func newDatabase(ctx context.Context, t *testing.T, dsn string) string {
	t.Helper()
	name := "bobres_test_drill_" + randHex(t, 6)
	execSQL(ctx, t, dsn, `CREATE DATABASE `+name)
	t.Cleanup(func() {
		if err := tryExec(context.Background(), dsn, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Logf("could not drop %s: %v", name, err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	q := u.Query()
	q.Del("dbname")
	u.RawQuery = q.Encode()
	return u.String()
}

// restoreFile streams a backup's decrypted dump into pg_restore on dsn and
// returns the file's master keys. The password goes to pg_restore in
// PGPASSWORD, as backupsvc.PgDump does.
func restoreFile(ctx context.Context, pgRestore, file, dsn string) (map[string]string, error) {
	f, err := os.Open(file) //nolint:gosec // the test's own backup
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	br, err := backup.Open(f, drillKey)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, err
	}
	env := os.Environ()
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			env = append(env, "PGPASSWORD="+pw)
		}
		u.User = url.User(u.User.Username())
	}
	args := append(slices.Clone(restoreFlags), "--no-password", "--dbname="+u.String())
	cmd := exec.CommandContext(ctx, pgRestore, args...)
	var out bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = br.Dump(), &out, &out, env
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pg_restore: %w\n%s", err, strings.TrimSpace(out.String()))
	}
	return br.Keys(), nil
}

// dbState is what a restore must bring back, by name: each table's row count
// and a checksum of its rows, each sequence's last value, each schema's goose
// version, and the definition of every schema object.
type dbState struct {
	tables    map[string]string
	sequences map[string]string
	versions  map[string]int64
	objects   map[string]string
}

// objectsQuery lists the schemas' objects and their definitions.
const objectsQuery = `
SELECT 'schema ' || nspname, pg_get_userbyid(nspowner)::text || ' ' || coalesce(nspacl::text, '')
  FROM pg_namespace WHERE nspname = ANY($1)
UNION ALL
SELECT 'table ' || schemaname || '.' || tablename, tableowner::text
  FROM pg_tables WHERE schemaname = ANY($1)
UNION ALL
SELECT 'column ' || table_schema || '.' || table_name || '.' || column_name,
       concat_ws(' ', data_type, is_nullable, column_default, is_identity, is_generated, generation_expression)
  FROM information_schema.columns WHERE table_schema = ANY($1)
UNION ALL
SELECT 'index ' || schemaname || '.' || indexname, indexdef
  FROM pg_indexes WHERE schemaname = ANY($1)
UNION ALL
SELECT 'constraint ' || n.nspname || '.' || r.relname || '.' || c.conname, pg_get_constraintdef(c.oid)
  FROM pg_constraint c JOIN pg_class r ON r.oid = c.conrelid JOIN pg_namespace n ON n.oid = r.relnamespace
 WHERE n.nspname = ANY($1)
UNION ALL
SELECT 'trigger ' || n.nspname || '.' || r.relname || '.' || g.tgname, pg_get_triggerdef(g.oid)
  FROM pg_trigger g JOIN pg_class r ON r.oid = g.tgrelid JOIN pg_namespace n ON n.oid = r.relnamespace
 WHERE NOT g.tgisinternal AND n.nspname = ANY($1)
UNION ALL
SELECT 'function ' || n.nspname || '.' || p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')',
       pg_get_functiondef(p.oid)
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname = ANY($1)`

func snapshot(ctx context.Context, t *testing.T, dsn string) dbState {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx) //nolint:errcheck // test helper
	schemas := migrate.Schemas()
	st := dbState{tables: map[string]string{}, sequences: map[string]string{}, versions: map[string]int64{}, objects: map[string]string{}}
	tables := map[string]string{}
	pairs(ctx, t, conn, tables, `SELECT format('%I.%I', schemaname, tablename), '' FROM pg_tables WHERE schemaname = ANY($1)`, schemas)
	for name := range tables {
		var n int64
		var sum string
		// name comes from the catalog, quoted by format('%I').
		q := `SELECT count(*), md5(coalesce(string_agg(r::text, E'\n' ORDER BY r::text), '')) FROM ` + name + ` r`
		if err := conn.QueryRow(ctx, q).Scan(&n, &sum); err != nil {
			t.Fatalf("checksum %s: %v", name, err)
		}
		st.tables[name] = fmt.Sprintf("%d rows, md5 %s", n, sum)
	}
	pairs(ctx, t, conn, st.sequences, `SELECT format('%I.%I', schemaname, sequencename), coalesce(last_value::text, 'unused')
		FROM pg_sequences WHERE schemaname = ANY($1)`, schemas)
	for _, s := range schemas {
		var v int64
		if err := conn.QueryRow(ctx, `SELECT coalesce(max(version_id), 0) FROM `+s+`.goose_db_version`).Scan(&v); err != nil {
			t.Fatalf("goose version of %s: %v", s, err)
		}
		st.versions[s] = v
	}
	pairs(ctx, t, conn, st.objects, objectsQuery, schemas)
	return st
}

// pairs reads a query's two text columns into m; a name seen twice fails,
// since it could hide a difference.
func pairs(ctx context.Context, t *testing.T, conn *pgx.Conn, m map[string]string, q string, args ...any) {
	t.Helper()
	rows, err := conn.Query(ctx, q, args...)
	if err != nil {
		t.Fatalf("query: %v\n%s", err, q)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		if _, dup := m[k]; dup {
			t.Fatalf("%s listed twice", k)
		}
		m[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// compareStates reports every difference between want and got.
func compareStates(t *testing.T, step string, want, got dbState) {
	t.Helper()
	compareMap(t, step, "table", want.tables, got.tables)
	compareMap(t, step, "sequence", want.sequences, got.sequences)
	compareMap(t, step, "goose version of", want.versions, got.versions)
	compareMap(t, step, "object", want.objects, got.objects)
}

func compareMap[V comparable](t *testing.T, step, kind string, want, got map[string]V) {
	t.Helper()
	for _, k := range slices.Sorted(maps.Keys(want)) {
		g, ok := got[k]
		switch {
		case !ok:
			t.Errorf("%s: %s %s is missing", step, kind, k)
		case g != want[k]:
			t.Errorf("%s: %s %s is %v, want %v", step, kind, k, g, want[k])
		}
	}
	for _, k := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: unexpected %s %s: %v", step, kind, k, got[k])
		}
	}
}

func execSQL(ctx context.Context, t *testing.T, dsn, sql string) {
	t.Helper()
	if err := tryExec(ctx, dsn, sql); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
}

func tryExec(ctx context.Context, dsn, sql string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx) //nolint:errcheck // test helper
	_, err = conn.Exec(ctx, sql)
	return err
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := crand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}
