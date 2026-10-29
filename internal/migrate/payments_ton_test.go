package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// payments 00010 (automatic TON) on a schema with the other gateways' settings and payments: they stay; the new
// provider, its settings, its comment, its transfers and its cursors are accepted and checked; going back is
// refused while there are payments of it, and works (and up again) once there are none.
func TestTONMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "payments", 9); err != nil {
		t.Fatalf("up to 9: %v", err)
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
		user = "00000000-0000-7000-8000-00000000a0c9"
		hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	)
	id := func(n string) string { return "00000000-0000-7000-8000-00000000a0" + n }
	tonIntent := func(n, key, memo string) string {
		return `INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key,
			gateway_amount, gateway_currency, pay_address, pay_memo, quote_usdt, quote_rate, quote_source)
			VALUES ('` + id(n) + `', '` + user + `', 'ton_auto', 1000, 'IRT', 'pending', '` + key + `', 3000000000, 'TON',
			'UQAAAA', ` + memo + `, 10000000, 3300000, 'binance')`
	}
	if err := exec(`INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key,
			gateway_amount, gateway_currency, external_id)
			VALUES ('` + id("a1") + `', '` + user + `', 'aqayepardakht', 1000, 'IRT', 'pending', 'aq-1', 10000, 'IRR', '4800001')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := exec(tonIntent("a2", "t-0", `'BOB-AAAAAAAA'`)); err == nil {
		t.Fatal("a ton payment was accepted before the migration")
	}
	if err := upTo(ctx, dsn, "payments", 10); err != nil {
		t.Fatalf("up to 10: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM payments.payment_intents WHERE provider = 'aqayepardakht'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("aqayepardakht payment after the migration: %d %v", n, err)
	}

	refused := map[string]string{
		"a ton payment without a comment":      tonIntent("a3", "t-1", `NULL`),
		"a ton payment in another currency":    strings.Replace(tonIntent("a4", "t-2", `'BOB-BBBBBBBB'`), "'TON'", "'USDT'", 1),
		"a ton payment with no quote":          strings.Replace(tonIntent("a5", "t-3", `'BOB-CCCCCCCC'`), "10000000, 3300000", "NULL, NULL", 1),
		"ton on without an address":            `INSERT INTO payments.gateway_configs (provider, enabled) VALUES ('ton_auto', true)`,
		"ton on with only an api key":          `INSERT INTO payments.gateway_configs (provider, enabled, api_key_enc) VALUES ('ton_auto', true, '\x01')`,
		"a transfer with a bad hash":           `INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, block_ts, outcome) VALUES ('XYZ', 1, 'a', 'b', 5, now(), 'unmatched')`,
		"a transfer of nothing":                `INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, block_ts, outcome) VALUES ('` + hash + `', 1, 'a', 'b', 0, now(), 'unmatched')`,
		"a matched transfer without a payment": `INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, block_ts, outcome) VALUES ('` + hash + `', 1, 'a', 'b', 5, now(), 'matched')`,
		"an unknown outcome":                   `INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, block_ts, outcome) VALUES ('` + hash + `', 1, 'a', 'b', 5, now(), 'paid')`,
	}
	for name, q := range refused {
		if err := exec(q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, q := range []string{
		tonIntent("b1", "t-ok-1", `'BOB-DDDDDDDD'`),
		tonIntent("b2", "t-ok-2", `'BOB-EEEEEEEE'`),
		`INSERT INTO payments.gateway_configs (provider, enabled, address_enc, api_key_enc, proxy_enc) VALUES ('ton_auto', true, '\x01', '\x02', '\x03')`,
		`INSERT INTO payments.ton_cursors (address, last_lt, floor_ts) VALUES ('UQAAAA', 0, now())`,
		`INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, comment, block_ts, intent_id, outcome)
			VALUES ('` + hash + `', 7, 'a', 'b', 3000000000, 'BOB-DDDDDDDD', now(), '` + id("b1") + `', 'matched')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("valid row refused: %v\n%s", err, q)
		}
	}
	// A comment names one payment for ever; a transaction pays once; a payment is paid by one transfer.
	again := map[string]string{
		"a second payment with the same comment": tonIntent("b3", "t-ok-3", `'BOB-DDDDDDDD'`),
		"the same transaction and time twice": `INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, block_ts, outcome)
			VALUES ('` + hash + `', 7, 'a', 'b', 5, now(), 'unmatched')`,
		"one transaction settling another payment": `INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, block_ts, intent_id, outcome)
			VALUES ('` + hash + `', 8, 'a', 'b', 5, now(), '` + id("b2") + `', 'matched')`,
		"a second transfer settling the same payment": `INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, block_ts, intent_id, outcome)
			VALUES ('` + strings.Repeat("ab", 32) + `', 9, 'a', 'b', 5, now(), '` + id("b1") + `', 'matched')`,
	}
	for name, q := range again {
		if err := exec(q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// An unmatched transfer may name the payment its comment pointed to, and the same payment may have several.
	for _, q := range []string{
		`INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, block_ts, intent_id, outcome, reason)
			VALUES ('` + strings.Repeat("cd", 32) + `', 10, 'a', 'b', 5, now(), '` + id("b2") + `', 'unmatched', 'underpaid')`,
		`INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, block_ts, intent_id, outcome, reason)
			VALUES ('` + strings.Repeat("ef", 32) + `', 11, 'a', 'b', 5, now(), '` + id("b2") + `', 'unmatched', 'underpaid')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("unmatched transfer refused: %v", err)
		}
	}

	if err := downTo(ctx, dsn, "payments", 9); err == nil || !strings.Contains(err.Error(), "ton_auto payments") {
		t.Fatalf("down with ton payments: %v", err)
	}
	if err := exec(`DELETE FROM payments.ton_transfers`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`DELETE FROM payments.payment_intents WHERE provider = 'ton_auto'`); err != nil {
		t.Fatal(err)
	}
	if err := downTo(ctx, dsn, "payments", 9); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM payments.payment_intents WHERE id = $1`, id("a1")).Scan(&n); err != nil || n != 1 {
		t.Fatalf("aqayepardakht payment after down: %d %v", n, err)
	}
	if err := exec(`INSERT INTO payments.gateway_configs (provider, enabled, address_enc) VALUES ('ton_auto', true, '\x01')`); err == nil {
		t.Fatal("after down a ton setting was accepted")
	}
	if err := upTo(ctx, dsn, "payments", 10); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
