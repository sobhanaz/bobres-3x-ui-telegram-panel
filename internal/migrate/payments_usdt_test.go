package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// payments 00006 (the USDT watcher) on a schema with Zarinpal settings and
// payments: they stay; the new provider, its settings, its transfer log and
// cursors are accepted and checked; going back is refused while there are
// automatic USDT payments, and works (and up again) once there are none.
func TestUSDTWatcherMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "payments", 5); err != nil {
		t.Fatalf("up to 5: %v", err)
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
		zpIntent   = "00000000-0000-7000-8000-0000000006a1"
		usdtIntent = "00000000-0000-7000-8000-0000000006a2"
		user       = "00000000-0000-7000-8000-0000000006b1"
		txid       = "4f1c0c3e8d9a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1f0a9b8c7d6e5f4a3b2c"
	)
	for _, q := range []string{
		`INSERT INTO payments.gateway_configs (provider, enabled, merchant_enc) VALUES ('zarinpal', true, '\x01')`,
		`INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key, gateway_amount, gateway_currency)
			VALUES ('` + zpIntent + `', '` + user + `', 'zarinpal', 1000, 'IRT', 'pending', 'zp-1', 10000, 'IRR')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "payments"); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM payments.gateway_configs WHERE provider = 'zarinpal' AND enabled`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("zarinpal settings after the migration: %d %v", n, err)
	}

	refused := map[string]string{
		"usdt switched on without an address":                    `INSERT INTO payments.gateway_configs (provider, enabled) VALUES ('usdt_trc20_auto', true)`,
		"zarinpal switched on without a merchant id, by address": `UPDATE payments.gateway_configs SET merchant_enc = NULL, address_enc = '\x01' WHERE provider = 'zarinpal'`,
		"usdt intent without an address": `INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key, gateway_amount, gateway_currency)
			VALUES (gen_random_uuid(), '` + user + `', 'usdt_trc20_auto', 1000, 'IRT', 'pending', 'u-x', 1000001, 'USDT')`,
		"usdt intent in another currency": `INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key, gateway_amount, gateway_currency, pay_address)
			VALUES (gen_random_uuid(), '` + user + `', 'usdt_trc20_auto', 1000, 'IRT', 'pending', 'u-y', 1000001, 'IRR', 'TCKR37NfLumfScqBQX6qrnJR3mZkv6xGqi')`,
		"upper-case txid": `INSERT INTO payments.chain_transfers (txid, event_index, network, token, from_address, to_address, value, block_ts, outcome)
			VALUES ('` + strings.ToUpper(txid) + `', 0, 'TRC20', 't', 'f', 'a', 1, now(), 'unmatched')`,
		"zero value": `INSERT INTO payments.chain_transfers (txid, event_index, network, token, from_address, to_address, value, block_ts, outcome)
			VALUES ('` + txid + `', 0, 'TRC20', 't', 'f', 'a', 0, now(), 'unmatched')`,
		"matched without an intent": `INSERT INTO payments.chain_transfers (txid, event_index, network, token, from_address, to_address, value, block_ts, outcome)
			VALUES ('` + txid + `', 0, 'TRC20', 't', 'f', 'a', 1, now(), 'matched')`,
		"unknown outcome": `INSERT INTO payments.chain_transfers (txid, event_index, network, token, from_address, to_address, value, block_ts, outcome)
			VALUES ('` + txid + `', 0, 'TRC20', 't', 'f', 'a', 1, now(), 'credited')`,
		"an announcement pending on a deposit that is not unmatched": `INSERT INTO payments.chain_transfers (txid, event_index, network, token,
			from_address, to_address, value, block_ts, outcome, reason, announce_at)
			VALUES ('` + txid + `', 0, 'TRC20', 't', 'f', 'a', 1, now(), 'ignored', 'dust', now())`,
	}
	for name, q := range refused {
		if err := exec(q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, q := range []string{
		`INSERT INTO payments.gateway_configs (provider, enabled, address_enc, api_key_enc) VALUES ('usdt_trc20_auto', true, '\x01', '\x02')`,
		`INSERT INTO payments.payment_intents (id, user_id, provider, amount, currency, status, idempotency_key, gateway_amount, gateway_currency, pay_address)
			VALUES ('` + usdtIntent + `', '` + user + `', 'usdt_trc20_auto', 1000, 'IRT', 'pending', 'u-1', 1000001, 'USDT', 'TCKR37NfLumfScqBQX6qrnJR3mZkv6xGqi')`,
		`INSERT INTO payments.gateway_events (intent_id, provider, kind, outcome) VALUES ('` + usdtIntent + `', 'usdt_trc20_auto', 'match', 'paid')`,
		`INSERT INTO payments.gateway_events (provider, kind, outcome) VALUES ('usdt_trc20_auto', 'scan', 'error')`,
		`INSERT INTO payments.chain_transfers (txid, event_index, network, token, from_address, to_address, value, block_ts, intent_id, outcome)
			VALUES ('` + txid + `', 0, 'TRC20', 't', 'f', 'a', 1000001, now(), '` + usdtIntent + `', 'matched')`,
		`INSERT INTO payments.chain_cursors (address, last_ts) VALUES ('TCKR37NfLumfScqBQX6qrnJR3mZkv6xGqi', now())`,
		`INSERT INTO payments.chain_transfers (txid, event_index, network, token, from_address, to_address, value, block_ts, outcome, reason, announce_at)
			VALUES ('` + txid + `', 2, 'TRC20', 't', 'f', 'a', 7, now(), 'unmatched', 'no_payment', now() + interval '20 minutes')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("valid row refused: %v\n%s", err, q)
		}
	}
	twice := map[string]string{
		"the same transfer twice": `INSERT INTO payments.chain_transfers (txid, event_index, network, token, from_address, to_address, value, block_ts, outcome)
			VALUES ('` + txid + `', 0, 'TRC20', 't', 'f', 'a', 5, now(), 'unmatched')`,
		"a second transfer settling the same intent": `INSERT INTO payments.chain_transfers (txid, event_index, network, token, from_address, to_address, value, block_ts, intent_id, outcome)
			VALUES ('` + txid + `', 1, 'TRC20', 't', 'f', 'a', 1000001, now(), '` + usdtIntent + `', 'matched')`,
	}
	for name, q := range twice {
		if err := exec(q); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	if err := downTo(ctx, dsn, "payments", 5); err == nil || !strings.Contains(err.Error(), "usdt_trc20_auto payments") {
		t.Fatalf("down with automatic USDT payments: %v", err)
	}
	if err := exec(`DELETE FROM payments.chain_transfers`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`DELETE FROM payments.gateway_events WHERE intent_id = $1`, usdtIntent); err != nil {
		t.Fatal(err)
	}
	if err := exec(`DELETE FROM payments.payment_intents WHERE id = $1`, usdtIntent); err != nil {
		t.Fatal(err)
	}
	if err := downTo(ctx, dsn, "payments", 5); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM payments.payment_intents WHERE id = $1`, zpIntent).Scan(&n); err != nil || n != 1 {
		t.Fatalf("zarinpal payment after down: %d %v", n, err)
	}
	if err := exec(`INSERT INTO payments.gateway_events (provider, kind, outcome) VALUES ('zarinpal', 'scan', 'error')`); err == nil {
		t.Fatal("after down a scan event was accepted")
	}
	if err := Up(ctx, dsn, "payments"); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
