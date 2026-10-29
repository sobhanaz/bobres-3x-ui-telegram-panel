package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// payments 00009 (Aqayepardakht) on a schema with the other gateways' settings and payments: they stay; the
// new provider, its setting and its open-payment queue are accepted and checked; going back is refused while
// there are payments of it, and works (and up again) once there are none.
func TestAqayepardakhtMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "payments", 8); err != nil {
		t.Fatalf("up to 8: %v", err)
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
		npIntent = "00000000-0000-7000-8000-0000000009a1"
		aqIntent = "00000000-0000-7000-8000-0000000009a2"
		user     = "00000000-0000-7000-8000-0000000009b1"
	)
	intent := func(id, provider, key, external string) string {
		return `INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key,
			gateway_amount, gateway_currency, external_id)
			VALUES ('` + id + `', '` + user + `', '` + provider + `', 1000, 'IRT', 'pending', '` + key + `', 10000, 'IRR', '` + external + `')`
	}
	for _, q := range []string{
		`INSERT INTO payments.gateway_configs (provider, enabled, api_key_enc) VALUES ('nextpay', true, '\x01')`,
		intent(npIntent, "nextpay", "np-1", "x-np-1"),
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := exec(intent(aqIntent, "aqayepardakht", "aq-0", "123")); err == nil {
		t.Fatal("an aqayepardakht payment was accepted before the migration")
	}
	if err := upTo(ctx, dsn, "payments", 9); err != nil {
		t.Fatalf("up to 9: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM payments.gateway_configs WHERE provider = 'nextpay' AND enabled`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("nextpay settings after the migration: %d %v", n, err)
	}

	refused := map[string]string{
		"aqayepardakht switched on without a PIN":  `INSERT INTO payments.gateway_configs (provider, enabled) VALUES ('aqayepardakht', true)`,
		"aqayepardakht on with only a merchant id": `INSERT INTO payments.gateway_configs (provider, enabled, merchant_enc) VALUES ('aqayepardakht', true, '\x01')`,
		"an unknown gateway":                       `INSERT INTO payments.gateway_configs (provider, enabled, api_key_enc) VALUES ('payir', true, '\x01')`,
		"a payment of an unknown gateway":          intent("00000000-0000-7000-8000-0000000009a9", "payir", "pi-1", "x-pi-1"),
	}
	for name, q := range refused {
		if err := exec(q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, q := range []string{
		`INSERT INTO payments.gateway_configs (provider, enabled, sandbox, api_key_enc, proxy_enc) VALUES ('aqayepardakht', true, true, '\x01', '\x02')`,
		intent(aqIntent, "aqayepardakht", "aq-1", "4800001"),
	} {
		if err := exec(q); err != nil {
			t.Fatalf("valid row refused: %v\n%s", err, q)
		}
	}
	// The same gateway reference settles at most one payment of that gateway, and another gateway's is another.
	if err := exec(`UPDATE payments.payment_intents SET external_id = '4800001' WHERE id = $1`, npIntent); err != nil {
		t.Fatalf("a nextpay reference equal to an aqayepardakht one is another gateway's: %v", err)
	}
	if err := exec(intent("00000000-0000-7000-8000-0000000009a3", "aqayepardakht", "aq-2", "4800002")); err != nil {
		t.Fatalf("second aqayepardakht payment: %v", err)
	}
	if err := exec(`UPDATE payments.payment_intents SET external_id = '4800001' WHERE id = '00000000-0000-7000-8000-0000000009a3'`); err == nil {
		t.Error("one aqayepardakht reference was accepted on two payments")
	}
	// The reconciler's queue index covers the new provider.
	var def string
	if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'payments' AND indexname = 'payment_intents_gateway_open_idx'`).Scan(&def); err != nil ||
		!strings.Contains(def, "aqayepardakht") || !strings.Contains(def, "idpay") || !strings.Contains(def, "nextpay") || !strings.Contains(def, "zarinpal") {
		t.Fatalf("open-payment index: %q %v", def, err)
	}

	if err := downTo(ctx, dsn, "payments", 8); err == nil || !strings.Contains(err.Error(), "aqayepardakht payments") {
		t.Fatalf("down with aqayepardakht payments: %v", err)
	}
	if err := exec(`DELETE FROM payments.payment_intents WHERE provider = 'aqayepardakht'`); err != nil {
		t.Fatal(err)
	}
	if err := downTo(ctx, dsn, "payments", 8); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM payments.payment_intents WHERE id = $1`, npIntent).Scan(&n); err != nil || n != 1 {
		t.Fatalf("nextpay payment after down: %d %v", n, err)
	}
	if err := exec(`INSERT INTO payments.gateway_configs (provider, enabled, api_key_enc) VALUES ('aqayepardakht', true, '\x01')`); err == nil {
		t.Fatal("after down an aqayepardakht setting was accepted")
	}
	if err := upTo(ctx, dsn, "payments", 9); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
