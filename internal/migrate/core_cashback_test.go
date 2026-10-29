package migrate

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
)

// 00048 puts four kinds into the ledger's rule on top of the rule in force (so a kind another migration added is kept), and Down takes them out
// for new rows only: the ledger is append-only, so the entries that are there stay.
func TestGrowthLedgerKindsMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 47); err != nil {
		t.Fatalf("Up to 47: %v", err)
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
	// Another branch's migration added a kind of its own before this one ran.
	if err := exec(`ALTER TABLE core.ledger_entries DROP CONSTRAINT ledger_entries_kind_check`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`ALTER TABLE core.ledger_entries ADD CONSTRAINT ledger_entries_kind_check
		CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral','commission','settlement','gift_code',
			'license_purchase','license_refund','supplier_purchase','supplier_refund','wallet_convert','another_branch'))`); err != nil {
		t.Fatal(err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	const user = "00000000-0000-7000-8000-000000000c01"
	if err := exec(`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 3001)`); err != nil {
		t.Fatal(err)
	}
	n := 0
	entry := func(kind string) error {
		n++
		return exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after) VALUES (gen_random_uuid(), $1, 'IRT', 1, $2, $3, 1)`,
			user, kind, kind+strconv.Itoa(n))
	}
	for _, kind := range []string{"cashback", "cashback_reversal", "target_bonus", "wheel_prize", "another_branch", "topup", "wallet_convert", "supplier_refund"} {
		if err := entry(kind); err != nil {
			t.Errorf("%s refused: %v", kind, err)
		}
	}
	if err := entry("made_up"); err == nil {
		t.Error("a made-up kind")
	}
	if err := downTo(ctx, dsn, "core", 47); err != nil {
		t.Fatalf("Down: %v", err)
	}
	for _, kind := range []string{"cashback", "cashback_reversal", "target_bonus", "wheel_prize"} {
		if err := entry(kind); err == nil {
			t.Errorf("after Down, %s is still allowed", kind)
		}
	}
	for _, kind := range []string{"another_branch", "topup", "wallet_convert"} {
		if err := entry(kind); err != nil {
			t.Errorf("after Down, %s refused: %v", kind, err)
		}
	}
	var kept int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.ledger_entries WHERE kind IN ('cashback', 'cashback_reversal', 'target_bonus', 'wheel_prize')`).Scan(&kept); err != nil || kept != 4 {
		t.Fatalf("the entries of the new kinds after Down: %d %v", kept, err)
	}
	// Up again: the rule is checked against every row, the four kinds included.
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if err := entry("cashback"); err != nil {
		t.Errorf("after Up again: %v", err)
	}
}

// 00049: one cashback row per order, a share of 0.01 to 100 percent, a reversal that is all or nothing, and Down drops the table.
func TestCashbackMigration(t *testing.T) {
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
	const (
		user  = "00000000-0000-7000-8000-000000000c02"
		plan  = "00000000-0000-7000-8000-000000000c03"
		order = "00000000-0000-7000-8000-000000000c04"
		entry = "00000000-0000-7000-8000-000000000c05"
		rev   = "00000000-0000-7000-8000-000000000c06"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 3002)`,
		`INSERT INTO core.plans (id, name_i18n, kind, price, currency) VALUES ('` + plan + `', '{}', 'time', 10000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key) VALUES ('` + order + `', '` + user + `', '` + plan + `', 'new', 'active', 10000, 'IRT', 'o1')`,
		`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after) VALUES ('` + entry + `', '` + user + `', 'IRT', 250, 'cashback', 'cashback:o1', 250)`,
		`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after) VALUES ('` + rev + `', '` + user + `', 'IRT', -250, 'cashback_reversal', 'cashback_reversal:o1', 0)`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	insert := func(order, entryID string, amount, bp int64) error {
		return exec(`INSERT INTO core.cashback_credits (order_id, user_id, currency, base_amount, percent_bp, amount, ledger_entry_id)
			VALUES ($1, $2, 'IRT', 10000, $3, $4, $5)`, order, user, bp, amount, entryID)
	}
	if err := insert(order, entry, 250, 0); err == nil {
		t.Error("a share of 0%")
	}
	if err := insert(order, entry, 250, 10001); err == nil {
		t.Error("a share above 100%")
	}
	if err := insert(order, entry, 0, 250); err == nil {
		t.Error("a credit of nothing")
	}
	if err := insert(order, entry, 250, 250); err != nil {
		t.Fatalf("a credit: %v", err)
	}
	if err := insert(order, entry, 250, 250); err == nil {
		t.Error("two credits for one order")
	}
	// A reversal is all or nothing in its two columns, and never more than the credit.
	if err := exec(`UPDATE core.cashback_credits SET reversed_amount = 100 WHERE order_id = $1`, order); err == nil {
		t.Error("a reversed amount without a time")
	}
	if err := exec(`UPDATE core.cashback_credits SET reversed_at = now() WHERE order_id = $1`, order); err == nil {
		t.Error("a reversal time without an amount")
	}
	if err := exec(`UPDATE core.cashback_credits SET reversed_amount = 251, reversed_at = now() WHERE order_id = $1`, order); err == nil {
		t.Error("more taken back than was credited")
	}
	if err := exec(`UPDATE core.cashback_credits SET reversed_amount = 250, reversed_at = now(), reversal_entry_id = $2 WHERE order_id = $1`, order, rev); err != nil {
		t.Fatalf("a reversal: %v", err)
	}
	if err := downTo(ctx, dsn, "core", 48); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'cashback_credits'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after Down: %d (%v)", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
