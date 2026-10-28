package rekey

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/hkdf"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const (
	oldMaster   = "0123456789abcdef0123456789abcdef"
	newMaster   = "fedcba9876543210fedcba9876543210"
	otherMaster = "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
)

var (
	migrateOnce sync.Once
	migrateErr  error
)

// pool is a pool on this binary's throwaway database, with the three schemas migrated and every table emptied.
func pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testdb.DSN(t)
	migrateOnce.Do(func() {
		for _, s := range Services {
			if migrateErr = migrate.Up(context.Background(), dsn, s); migrateErr != nil {
				return
			}
		}
	})
	if migrateErr != nil {
		t.Fatalf("migrate: %v", migrateErr)
	}
	p, err := pgxpool.New(context.Background(), testdb.WithPoolCap(t, dsn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	ctx := context.Background()
	if err := testdb.Truncate(ctx, p, "core.users, core.supplier_products, payments.gateway_configs, provisioner.xui_servers, core.vendor_config, core.supplier_config"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE core.tls_config SET mode = 'auto', ip = NULL, cert_pem = NULL, key_sealed = NULL WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	return p
}

// sealV1 is a value as the envelope made it before key rotation: version 1, no key id.
func sealV1(t *testing.T, master, plain string) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(master), nil, []byte("bobres:envelope:v1")), k); err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(k)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := crand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return gcm.Seal(append([]byte{1}, nonce...), nonce, []byte(plain), []byte{1})
}

func sealV2(t *testing.T, master, plain string) []byte {
	t.Helper()
	e, err := crypto.NewEnvelope([]byte(master))
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Encrypt([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func envelope(t *testing.T, k crypto.Keys) *crypto.Envelope {
	t.Helper()
	e, err := crypto.NewKeyring(k)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// rotating is the envelope while a rotation from oldMaster to newMaster runs; fresh is the one after it.
func rotating(t *testing.T) *crypto.Envelope {
	return envelope(t, crypto.Keys{Master: newMaster, Previous: []string{oldMaster}})
}
func fresh(t *testing.T) *crypto.Envelope { return envelope(t, crypto.Keys{Master: newMaster}) }

// expected maps "schema.table.column|key" to the plaintext a seeded value holds.
type expected map[string]string

// seed fills every encrypted column of the registry, with values of both formats under the old key, some columns NULL or empty.
func seed(t *testing.T, p *pgxpool.Pool) expected {
	t.Helper()
	ctx := context.Background()
	want := expected{}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := p.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	const (
		u1 = "00000000-0000-7000-8000-0000000000a1"
		u2 = "00000000-0000-7000-8000-0000000000a2"
		u3 = "00000000-0000-7000-8000-0000000000a3"
	)
	for i, u := range []string{u1, u2, u3} {
		exec(`INSERT INTO core.users (id, telegram_id) VALUES ($1, $2)`, u, 1001+i)
	}
	cred := func(user, name string, v []byte) {
		exec(`INSERT INTO core.staff_credentials (user_id, username, password_hash, totp_secret_enc) VALUES ($1, $2, 'x', $3)`, user, name, v)
	}
	cred(u1, "alpha", sealV1(t, oldMaster, "totp-one"))
	want["core.staff_credentials.totp_secret_enc|"+u1] = "totp-one"
	cred(u2, "bravo", sealV2(t, oldMaster, "totp-two"))
	want["core.staff_credentials.totp_secret_enc|"+u2] = "totp-two"
	cred(u3, "charlie", []byte{}) // no authenticator yet: an empty value is not an encrypted one

	exec(`UPDATE core.tls_config SET mode = 'manual', cert_pem = 'CERT', key_sealed = $1 WHERE id = 1`, sealV2(t, oldMaster, "-----BEGIN PRIVATE KEY-----"))
	want["core.tls_config.key_sealed|1"] = "-----BEGIN PRIVATE KEY-----"
	exec(`INSERT INTO core.vendor_config (admin_key_enc) VALUES ($1)`, sealV1(t, oldMaster, "vendor-admin-key"))
	want["core.vendor_config.admin_key_enc|true"] = "vendor-admin-key"
	exec(`INSERT INTO core.supplier_config (api_key_enc) VALUES ($1)`, sealV2(t, oldMaster, "supplier-api-key"))
	want["core.supplier_config.api_key_enc|true"] = "supplier-api-key"

	const l1, l2 = "00000000-0000-7000-8000-0000000000b1", "00000000-0000-7000-8000-0000000000b2"
	exec(`INSERT INTO core.vendor_licenses (id, user_id, tier, key_enc) VALUES ($1, $2, 'standard', $3)`, l1, u1, sealV1(t, oldMaster, "license-one"))
	exec(`INSERT INTO core.vendor_licenses (id, user_id, tier, key_enc) VALUES ($1, $2, 'pro', $3)`, l2, u2, sealV2(t, oldMaster, "license-two"))
	want["core.vendor_licenses.key_enc|"+l1], want["core.vendor_licenses.key_enc|"+l2] = "license-one", "license-two"

	exec(`INSERT INTO core.supplier_products (id) VALUES ('prod-1')`)
	const o1, o2 = "00000000-0000-7000-8000-0000000000c1", "00000000-0000-7000-8000-0000000000c2"
	exec(`INSERT INTO core.supplier_orders (id, user_id, product_id, quantity, amount, currency, idempotency_key, delivery_enc) VALUES ($1, $2, 'prod-1', 1, 100, 'IRR', 'k1', $3)`,
		o1, u1, sealV1(t, oldMaster, "account:user:pass"))
	exec(`INSERT INTO core.supplier_orders (id, user_id, product_id, quantity, amount, currency, idempotency_key) VALUES ($1, $2, 'prod-1', 1, 100, 'IRR', 'k2')`, o2, u2) // not delivered: NULL
	want["core.supplier_orders.delivery_enc|"+o1] = "account:user:pass"

	exec(`INSERT INTO payments.gateway_configs (provider, enabled, merchant_enc, proxy_enc) VALUES ('zarinpal', true, $1, $2)`,
		sealV1(t, oldMaster, "merchant-id"), sealV2(t, oldMaster, "http://proxy"))
	want["payments.gateway_configs.merchant_enc|zarinpal"], want["payments.gateway_configs.proxy_enc|zarinpal"] = "merchant-id", "http://proxy"
	exec(`INSERT INTO payments.gateway_configs (provider, enabled, address_enc, api_key_enc) VALUES ('usdt_trc20_auto', true, $1, $2)`,
		sealV2(t, oldMaster, "TXaddress"), sealV1(t, oldMaster, "trongrid-key"))
	want["payments.gateway_configs.address_enc|usdt_trc20_auto"], want["payments.gateway_configs.api_key_enc|usdt_trc20_auto"] = "TXaddress", "trongrid-key"

	const s1, s2 = "00000000-0000-7000-8000-0000000000d1", "00000000-0000-7000-8000-0000000000d2"
	exec(`INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc) VALUES ($1, 'one', 'https://one.example', $2)`, s1, sealV1(t, oldMaster, "token-one"))
	exec(`INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc) VALUES ($1, 'two', 'https://two.example', $2)`, s2, sealV2(t, oldMaster, "token-two"))
	want["provisioner.xui_servers.api_token_enc|"+s1], want["provisioner.xui_servers.api_token_enc|"+s2] = "token-one", "token-two"
	return want
}

// snapshot is every encrypted value with the version of its row (xmin changes with any write, even of equal bytes), by table.
func snapshot(t *testing.T, p *pgxpool.Pool, tables []Table) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, tb := range tables {
		var parts []string
		for _, c := range tb.Cols {
			parts = append(parts, fmt.Sprintf("coalesce(%s::text, '-')", c))
		}
		cols := strings.Join(parts, " || '|' || ")
		rows, err := p.Query(context.Background(), fmt.Sprintf(`SELECT %s::text, xmin::text, %s FROM %s ORDER BY 1`, tb.Key, cols, tb.qualified()))
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for rows.Next() {
			var k, xmin, vals string
			if err := rows.Scan(&k, &xmin, &vals); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, k+"@"+xmin+"="+vals)
		}
		rows.Close()
		out[tb.String()] = strings.Join(lines, "\n")
	}
	return out
}

func allTables() []Table {
	var out []Table
	for _, s := range Services {
		out = append(out, Tables[s]...)
	}
	return out
}

// values reads every encrypted value as "schema.table.column|key" -> bytes.
func values(t *testing.T, p *pgxpool.Pool) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, tb := range allTables() {
		for _, c := range tb.Cols {
			rows, err := p.Query(context.Background(), fmt.Sprintf(`SELECT %s::text, %s FROM %s`, tb.Key, c, tb.qualified()))
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var k string
				var v []byte
				if err := rows.Scan(&k, &v); err != nil {
					t.Fatal(err)
				}
				if len(v) > 0 {
					out[fmt.Sprintf("%s.%s|%s", tb, c, k)] = v
				}
			}
			rows.Close()
		}
	}
	return out
}

// requireReadable checks that env reads every value of the database and finds the plaintext that was seeded.
func requireReadable(t *testing.T, p *pgxpool.Pool, env *crypto.Envelope, want expected) {
	t.Helper()
	got := values(t, p)
	if len(got) != len(want) {
		t.Fatalf("the database holds %d encrypted values, %d were seeded", len(got), len(want))
	}
	for k, w := range want {
		pt, err := env.Decrypt(got[k])
		if err != nil || string(pt) != w {
			t.Fatalf("%s: %q, %v (want %q)", k, pt, err, w)
		}
	}
}

func runAll(t *testing.T, p *pgxpool.Pool, env *crypto.Envelope, opts Options) (reports map[string]Report, err error) {
	t.Helper()
	reports = map[string]Report{}
	for _, s := range Services {
		rep, e := Run(context.Background(), p, env, Tables[s], opts)
		reports[s] = rep
		if e != nil {
			return reports, e
		}
	}
	return reports, nil
}

func TestFullRotation(t *testing.T) {
	p := pool(t)
	want := seed(t, p)
	requireReadable(t, p, envelope(t, crypto.Keys{Master: oldMaster}), want)

	reports, err := runAll(t, p, rotating(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	total, sealed := 0, 0
	for _, r := range reports {
		total += r.Values()
		sealed += r.Sealed()
	}
	if total != len(want) || sealed != len(want) {
		t.Fatalf("%d values counted, %d sealed again, %d seeded", total, sealed, len(want))
	}
	// Everything now reads with the new key alone, to the plaintext that was put in.
	requireReadable(t, p, fresh(t), want)
	for k, v := range values(t, p) {
		if v[0] != 2 || hex.EncodeToString(v[1:5]) != fresh(t).KeyID() {
			t.Fatalf("%s: %x is not a version 2 value of the new key", k, v[:5])
		}
		// the old key reads none of it
		if _, err := envelope(t, crypto.Keys{Master: oldMaster}).Decrypt(v); err == nil {
			t.Fatalf("%s is still readable with the old key", k)
		}
	}
	// The empty and NULL ones stayed what they were.
	var empty []byte
	if err := p.QueryRow(context.Background(), `SELECT totp_secret_enc FROM core.staff_credentials WHERE username = 'charlie'`).Scan(&empty); err != nil || len(empty) != 0 {
		t.Fatalf("the empty value changed: %x, %v", empty, err)
	}
	var delivery []byte
	if err := p.QueryRow(context.Background(), `SELECT delivery_enc FROM core.supplier_orders WHERE idempotency_key = 'k2'`).Scan(&delivery); err != nil || delivery != nil {
		t.Fatalf("the NULL value changed: %x, %v", delivery, err)
	}
}

func TestRunAgainDoesNothing(t *testing.T) {
	p := pool(t)
	want := seed(t, p)
	if _, err := runAll(t, p, rotating(t), Options{}); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, p, allTables())
	reports, err := runAll(t, p, rotating(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for s, r := range reports {
		if r.Sealed() != 0 {
			t.Errorf("%s: %d values sealed again on a second run", s, r.Sealed())
		}
	}
	if after := snapshot(t, p, allTables()); !snapshotsEqual(before, after) {
		t.Fatal("a second run wrote to the database")
	}
	requireReadable(t, p, fresh(t), want)
}

func snapshotsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestDryRunWritesNothing(t *testing.T) {
	p := pool(t)
	want := seed(t, p)
	before := snapshot(t, p, allTables())
	reports, err := runAll(t, p, rotating(t), Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	total, would := 0, 0
	for _, r := range reports {
		if !r.DryRun {
			t.Error("the report does not say it was a dry run")
		}
		total += r.Values()
		would += r.Sealed()
	}
	if total != len(want) || would != len(want) {
		t.Fatalf("a dry run counted %d values and %d to re-encrypt, want %d of both", total, would, len(want))
	}
	if after := snapshot(t, p, allTables()); !snapshotsEqual(before, after) {
		t.Fatal("a dry run changed the database")
	}
	requireReadable(t, p, envelope(t, crypto.Keys{Master: oldMaster}), want)
	// and Plan alone is the same thing
	if _, err := Plan(context.Background(), p, rotating(t), Tables["core"]); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(t, p, allTables()); !snapshotsEqual(before, after) {
		t.Fatal("Plan changed the database")
	}
}

func TestTheWrongKeyIsRefusedAndNothingIsWritten(t *testing.T) {
	p := pool(t)
	want := seed(t, p)
	before := snapshot(t, p, allTables())
	wrong := envelope(t, crypto.Keys{Master: newMaster, Previous: []string{otherMaster}}) // neither is the key of the data
	for _, opts := range []Options{{}, {DryRun: true}} {
		for _, s := range Services {
			rep, err := Run(context.Background(), p, wrong, Tables[s], opts)
			var u *UnreadableError
			if !errors.As(err, &u) {
				t.Fatalf("%s %+v: want an UnreadableError, got %v", s, opts, err)
			}
			if rep.Sealed() != 0 {
				t.Errorf("%s: %d values sealed", s, rep.Sealed())
			}
			msg := err.Error()
			if !strings.Contains(msg, u.Table.String()) || !strings.Contains(msg, u.Column) {
				t.Errorf("the error does not say where: %s", msg)
			}
			for k, v := range values(t, p) {
				if strings.Contains(msg, hex.EncodeToString(v)) || strings.Contains(msg, want[k]) {
					t.Fatalf("the error shows a value: %s", msg)
				}
			}
		}
	}
	if after := snapshot(t, p, allTables()); !snapshotsEqual(before, after) {
		t.Fatal("a refused run wrote to the database")
	}
	requireReadable(t, p, envelope(t, crypto.Keys{Master: oldMaster}), want)
}

func TestOneDamagedValueStopsTheServiceBeforeItsFirstWrite(t *testing.T) {
	p := pool(t)
	want := seed(t, p)
	// the LAST table the run would reach holds a value nothing reads; the first tables hold good ones to rewrite
	if _, err := p.Exec(context.Background(), `UPDATE core.supplier_orders SET delivery_enc = '\x0102030405' WHERE idempotency_key = 'k1'`); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, p, Tables["core"])
	_, err := Run(context.Background(), p, rotating(t), Tables["core"], Options{})
	var u *UnreadableError
	if !errors.As(err, &u) || u.Table.Name != "supplier_orders" {
		t.Fatalf("want an UnreadableError for supplier_orders, got %v", err)
	}
	if after := snapshot(t, p, Tables["core"]); !snapshotsEqual(before, after) {
		t.Fatal("tables were written although a value in a later one cannot be read")
	}
	_ = want
}

func TestInterruptedBetweenTablesEverythingStaysReadable(t *testing.T) {
	p := pool(t)
	want := seed(t, p)
	boom := errors.New("killed")
	tables := Tables["core"]
	done := 0
	_, err := Run(context.Background(), p, rotating(t), tables, Options{AfterTable: func(Table) error {
		done++
		if done == 2 {
			return boom
		}
		return nil
	}})
	if !errors.Is(err, boom) {
		t.Fatalf("want the interruption, got %v", err)
	}
	// Two tables are on the new key, the rest still on the old one; while both keys are known nothing is unreadable.
	requireReadable(t, p, rotating(t), want)
	if rep, err := Plan(context.Background(), p, rotating(t), tables); err != nil || rep.Sealed() == 0 || rep.Sealed() >= rep.Values() {
		t.Fatalf("the plan after the interruption: %+v, %v", rep, err)
	}
	// Running it again finishes the work.
	if _, err := runAll(t, p, rotating(t), Options{}); err != nil {
		t.Fatal(err)
	}
	requireReadable(t, p, fresh(t), want)
}

func TestInterruptedInsideATableLeavesItAsItWas(t *testing.T) {
	p := pool(t)
	want := seed(t, p)
	boom := errors.New("killed before the commit")
	before := snapshot(t, p, Tables["payments"])
	_, err := Run(context.Background(), p, rotating(t), Tables["payments"], Options{BeforeCommit: func(Table) error { return boom }})
	if !errors.Is(err, boom) {
		t.Fatalf("want the interruption, got %v", err)
	}
	if after := snapshot(t, p, Tables["payments"]); !snapshotsEqual(before, after) {
		t.Fatal("a table whose transaction did not commit changed")
	}
	requireReadable(t, p, rotating(t), want)
	if _, err := runAll(t, p, rotating(t), Options{}); err != nil {
		t.Fatal(err)
	}
	requireReadable(t, p, fresh(t), want)
}

func TestRotatingBackRestoresTheOldKey(t *testing.T) {
	// The way a failed rotation is undone: the same run with the keys swapped.
	p := pool(t)
	want := seed(t, p)
	if _, err := runAll(t, p, rotating(t), Options{}); err != nil {
		t.Fatal(err)
	}
	back := envelope(t, crypto.Keys{Master: oldMaster, Previous: []string{newMaster}})
	if _, err := runAll(t, p, back, Options{}); err != nil {
		t.Fatal(err)
	}
	requireReadable(t, p, envelope(t, crypto.Keys{Master: oldMaster}), want)
}

func TestARowALockedTableCannotBeChangedByAnotherWriter(t *testing.T) {
	// The rows are locked from the read to the commit: a writer that comes in between waits, so its change is neither lost nor
	// overwritten with a value sealed from the older bytes.
	p := pool(t)
	seed(t, p)
	ctx := context.Background()
	tb := Tables["provisioner"][0]
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // a test
	if _, err := scan(ctx, tx, rotating(t), tb, true); err != nil {
		t.Fatal(err)
	}
	other, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Rollback(ctx) //nolint:errcheck // a test
	if _, err := other.Exec(ctx, `SET LOCAL lock_timeout = '300ms'`); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(ctx, `UPDATE provisioner.xui_servers SET api_token_enc = $1 WHERE name = 'one'`, sealV2(t, oldMaster, "changed meanwhile")); err == nil {
		t.Fatal("another writer changed a row while its table was being re-encrypted")
	}
}

func TestRegistryNamesEveryEncryptedColumn(t *testing.T) {
	p := pool(t)
	// bytea columns that hold no ciphertext, and why.
	notEncrypted := map[string]string{ //nolint:gosec // column names and reasons, no credentials
		"core.assets.data":                       "the store's uploaded pictures, in the clear",
		"core.gift_codes.code_hash":              "an HMAC of the code (a hash, not a ciphertext); it follows the tag key, which a rotation keeps",
		"core.login_links.token_hash":            "a SHA-256 of a one-time token",
		"core.web_sessions.id_hash":              "a SHA-256 of a session cookie",
		"core.login_approvals.ticket_hash":       "a SHA-256 of a cookie",
		"core.login_approval_targets.nonce_hash": "a SHA-256 of a nonce",
	}
	registered := map[string]bool{}
	for _, tb := range allTables() {
		for _, c := range tb.Cols {
			registered[fmt.Sprintf("%s.%s.%s", tb.Schema, tb.Name, c)] = true
		}
	}
	rows, err := p.Query(context.Background(), `SELECT table_schema || '.' || table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema IN ('core','payments','provisioner') AND udt_name = 'bytea' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		seen[name] = true
		if !registered[name] && notEncrypted[name] == "" {
			t.Errorf("%s is a bytea column that rekey.Tables does not list. If it holds a value sealed by crypto.Envelope, add it there, "+
				"or a master key rotation would leave it unreadable. If it holds something else, say what in this test", name)
		}
		if registered[name] && notEncrypted[name] != "" {
			t.Errorf("%s is both registered and listed as not encrypted", name)
		}
	}
	for name := range registered {
		if !seen[name] {
			t.Errorf("rekey.Tables lists %s, which is not a bytea column of the schema", name)
		}
	}
	for name := range notEncrypted {
		if !seen[name] {
			t.Errorf("the test lists %s as not encrypted, but it is no bytea column any more: remove it", name)
		}
	}
	// every Key column is the table's primary key
	for _, tb := range allTables() {
		var pk []string
		prows, err := p.Query(context.Background(), `SELECT a.attname FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY (i.indkey)
			WHERE i.indrelid = $1::regclass AND i.indisprimary`, tb.Schema+"."+tb.Name)
		if err != nil {
			t.Fatal(err)
		}
		for prows.Next() {
			var c string
			if err := prows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			pk = append(pk, c)
		}
		prows.Close()
		sort.Strings(pk)
		if len(pk) != 1 || pk[0] != tb.Key {
			t.Errorf("%s: Key is %q, the primary key is %v", tb, tb.Key, pk)
		}
	}
}

func TestCommand(t *testing.T) {
	p := pool(t)
	want := seed(t, p)
	open := func(env *crypto.Envelope) openFunc {
		return func(context.Context) (DB, *crypto.Envelope, func(), error) { return p, env, func() {}, nil }
	}
	var out, errOut bytes.Buffer
	run := func(service string, env *crypto.Envelope, args ...string) int {
		out.Reset()
		errOut.Reset()
		return command(service, args, &out, &errOut, open(env))
	}
	if code := run("core", rotating(t), "--dry-run"); code != 0 || !strings.Contains(out.String(), "dry run, nothing was written: 8 of 8 values") {
		t.Fatalf("dry run: %d\n%s%s", code, out.String(), errOut.String())
	}
	for _, line := range []string{"core.staff_credentials", "core.supplier_orders", "to re-encrypt"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the dry run does not show %q:\n%s", line, out.String())
		}
	}
	if code := run("core", rotating(t)); code != 0 || !strings.Contains(out.String(), "8 of 8 values re-encrypted") {
		t.Fatalf("run: %d\n%s%s", code, out.String(), errOut.String())
	}
	if code := run("core", rotating(t)); code != 0 || !strings.Contains(out.String(), "0 of 8 values re-encrypted") {
		t.Fatalf("run again: %d\n%s%s", code, out.String(), errOut.String())
	}
	// the wrong key: exit 1, and it says where
	if code := run("payments", envelope(t, crypto.Keys{Master: otherMaster}), "--dry-run"); code != 1 || !strings.Contains(errOut.String(), "payments.gateway_configs") ||
		!strings.Contains(errOut.String(), "nothing was written") {
		t.Fatalf("wrong key: %d\n%s%s", code, out.String(), errOut.String())
	}
	// no master key on the service: nothing to do
	if code := command("payments", nil, &out, &errOut, func(context.Context) (DB, *crypto.Envelope, func(), error) { return nil, nil, func() {}, nil }); code != 0 {
		t.Fatalf("no key: %d", code)
	}
	if code := run("bot", fresh(t)); code != 2 {
		t.Fatalf("an unknown service: %d", code)
	}
	if code := run("core", fresh(t), "--wat"); code != 2 {
		t.Fatalf("a bad flag: %d", code)
	}
	for _, s := range []string{out.String(), errOut.String()} {
		for _, w := range want {
			if strings.Contains(s, w) {
				t.Fatalf("the output shows a secret: %s", s)
			}
		}
	}
}
