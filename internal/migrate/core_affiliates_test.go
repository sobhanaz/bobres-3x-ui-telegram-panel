package migrate

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
)

// 00053 adds the affiliate tables and three ledger kinds on top of the rule in force; 00054 the hosted stores' register. Down takes the
// tables away and the kinds out for new rows only (the ledger is append-only).
func TestAffiliatesAndHostedMigrations(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 52); err != nil {
		t.Fatalf("Up to 52: %v", err)
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
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	const (
		aff   = "00000000-0000-7000-8000-000000000a01"
		buyer = "00000000-0000-7000-8000-000000000a02"
		affID = "00000000-0000-7000-8000-000000000a03"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + aff + `', 4001), ('` + buyer + `', 4002)`,
		`INSERT INTO core.affiliates (id, user_id, code) VALUES ('` + affID + `', '` + aff + `', 'abcdef23')`,
		`INSERT INTO core.affiliate_referrals (user_id, affiliate_id) VALUES ('` + buyer + `', '` + affID + `')`,
		`INSERT INTO core.hosted_stores (id, customer, domain) VALUES (gen_random_uuid(), 'Acme', 'shop.example.com')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"a code in capitals":     `INSERT INTO core.affiliates (id, user_id, code) VALUES (gen_random_uuid(), '` + buyer + `', 'ABCDEF23')`,
		"a second affiliate row": `INSERT INTO core.affiliates (id, user_id, code) VALUES (gen_random_uuid(), '` + aff + `', 'zzzzzz23')`,
		"a percent over 100":     `UPDATE core.affiliates SET percent = 101`,
		"a second referral":      `INSERT INTO core.affiliate_referrals (user_id, affiliate_id) VALUES ('` + buyer + `', '` + affID + `')`,
		"a domain with a port":   `INSERT INTO core.hosted_stores (id, customer, domain) VALUES (gen_random_uuid(), 'A', 'a.example.com:443')`,
		"the same domain":        `INSERT INTO core.hosted_stores (id, customer, domain) VALUES (gen_random_uuid(), 'B', 'shop.example.com')`,
		"a beta-ish channel":     `UPDATE core.hosted_stores SET channel = 'nightly'`,
	} {
		if err := exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	n := 0
	entry := func(kind string) error {
		n++
		return exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after) VALUES (gen_random_uuid(), $1, 'IRT', 1, $2, $3, 1)`,
			aff, kind, kind+strconv.Itoa(n))
	}
	for _, kind := range []string{"affiliate_commission", "affiliate_reversal", "affiliate_payout", "cashback", "license_purchase", "topup"} {
		if err := entry(kind); err != nil {
			t.Errorf("%s refused: %v", kind, err)
		}
	}
	if err := downTo(ctx, dsn, "core", 52); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core'
		AND table_name IN ('affiliates', 'affiliate_referrals', 'affiliate_commissions', 'affiliate_payouts', 'hosted_stores')`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("tables after Down: %d %v", tables, err)
	}
	if err := entry("affiliate_commission"); err == nil {
		t.Error("after Down, affiliate_commission is still allowed")
	}
	if err := entry("cashback"); err != nil {
		t.Errorf("after Down, cashback refused: %v", err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
