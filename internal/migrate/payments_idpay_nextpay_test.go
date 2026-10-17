package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// payments 00008 (IDPay and NextPay) on a schema with Zarinpal and automatic
// USDT settings and payments: they stay; the two new providers, their settings
// and their open-payment queue are accepted and checked; going back is refused
// while there are payments of either, and works (and up again) once there are
// none.
func TestIDPayNextPayMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "payments", 7); err != nil {
		t.Fatalf("up to 7: %v", err)
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
	const (
		zpIntent = "00000000-0000-7000-8000-0000000008a1"
		ipIntent = "00000000-0000-7000-8000-0000000008a2"
		user     = "00000000-0000-7000-8000-0000000008b1"
	)
	intent := func(id, provider, key string) string {
		return `INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key,
			gateway_amount, gateway_currency, external_id)
			VALUES ('` + id + `', '` + user + `', '` + provider + `', 1000, 'IRT', 'pending', '` + key + `', 10000, 'IRR', 'x-` + key + `')`
	}
	for _, q := range []string{
		`INSERT INTO payments.gateway_configs (provider, enabled, merchant_enc) VALUES ('zarinpal', true, '\x01')`,
		intent(zpIntent, "zarinpal", "zp-1"),
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := exec(intent(ipIntent, "idpay", "ip-0")); err == nil {
		t.Fatal("an idpay payment was accepted before the migration")
	}
	if err := Up(ctx, dsn, "payments"); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM payments.gateway_configs WHERE provider = 'zarinpal' AND enabled`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("zarinpal settings after the migration: %d %v", n, err)
	}

	refused := map[string]string{
		"idpay switched on without an api key":        `INSERT INTO payments.gateway_configs (provider, enabled) VALUES ('idpay', true)`,
		"nextpay switched on with only a merchant id": `INSERT INTO payments.gateway_configs (provider, enabled, merchant_enc) VALUES ('nextpay', true, '\x01')`,
		"zarinpal switched on with only an api key":   `UPDATE payments.gateway_configs SET merchant_enc = NULL, api_key_enc = '\x01' WHERE provider = 'zarinpal'`,
		"an unknown gateway":                          `INSERT INTO payments.gateway_configs (provider, enabled, api_key_enc) VALUES ('payir', true, '\x01')`,
		"a payment of an unknown gateway":             intent("00000000-0000-7000-8000-0000000008a9", "payir", "pi-1"),
	}
	for name, q := range refused {
		if err := exec(q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, q := range []string{
		`INSERT INTO payments.gateway_configs (provider, enabled, api_key_enc) VALUES ('idpay', true, '\x01')`,
		`INSERT INTO payments.gateway_configs (provider, enabled, sandbox, api_key_enc, proxy_enc) VALUES ('nextpay', true, true, '\x01', '\x02')`,
		intent(ipIntent, "idpay", "ip-1"),
		intent("00000000-0000-7000-8000-0000000008a3", "nextpay", "np-1"),
	} {
		if err := exec(q); err != nil {
			t.Fatalf("valid row refused: %v\n%s", err, q)
		}
	}
	// The same gateway reference settles at most one payment.
	if err := exec(`UPDATE payments.payment_intents SET external_id = 'x-ip-1' WHERE id = '00000000-0000-7000-8000-0000000008a3'`); err != nil {
		t.Fatalf("a nextpay reference equal to an idpay one is another gateway's: %v", err)
	}
	if err := exec(intent("00000000-0000-7000-8000-0000000008a4", "idpay", "ip-2")); err != nil {
		t.Fatalf("second idpay payment: %v", err)
	}
	if err := exec(`UPDATE payments.payment_intents SET external_id = 'x-ip-1' WHERE id = '00000000-0000-7000-8000-0000000008a4'`); err == nil {
		t.Error("one idpay reference was accepted on two payments")
	}
	// The reconciler's queue index covers the new providers.
	var def string
	if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'payments' AND indexname = 'payment_intents_gateway_open_idx'`).Scan(&def); err != nil ||
		!strings.Contains(def, "idpay") || !strings.Contains(def, "nextpay") || !strings.Contains(def, "zarinpal") {
		t.Fatalf("open-payment index: %q %v", def, err)
	}

	if err := downTo(ctx, dsn, "payments", 7); err == nil || !strings.Contains(err.Error(), "idpay or nextpay payments") {
		t.Fatalf("down with idpay payments: %v", err)
	}
	if err := exec(`DELETE FROM payments.payment_intents WHERE provider IN ('idpay','nextpay')`); err != nil {
		t.Fatal(err)
	}
	if err := downTo(ctx, dsn, "payments", 7); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM payments.payment_intents WHERE id = $1`, zpIntent).Scan(&n); err != nil || n != 1 {
		t.Fatalf("zarinpal payment after down: %d %v", n, err)
	}
	if err := exec(`INSERT INTO payments.gateway_configs (provider, enabled, api_key_enc) VALUES ('idpay', true, '\x01')`); err == nil {
		t.Fatal("after down an idpay setting was accepted")
	}
	if err := Up(ctx, dsn, "payments"); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
