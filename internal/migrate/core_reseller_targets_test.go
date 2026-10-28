package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00044: a sale is recorded once per order in a month with a real label, a bonus once per reseller, month and tier, with its own ledger entry; Down
// drops both tables and Up brings them back.
func TestResellerTargetsMigration(t *testing.T) {
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
		user   = "00000000-0000-7000-8000-000000000d02"
		plan   = "00000000-0000-7000-8000-000000000d03"
		order1 = "00000000-0000-7000-8000-000000000d04"
		order2 = "00000000-0000-7000-8000-000000000d05"
		entry1 = "00000000-0000-7000-8000-000000000d06"
		entry2 = "00000000-0000-7000-8000-000000000d07"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 3003)`,
		`INSERT INTO core.plans (id, name_i18n, kind, price, currency) VALUES ('` + plan + `', '{}', 'time', 10000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key) VALUES ('` + order1 + `', '` + user + `', '` + plan + `', 'new', 'active', 10000, 'IRT', 'o1')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key) VALUES ('` + order2 + `', '` + user + `', '` + plan + `', 'new', 'active', 10000, 'IRT', 'o2')`,
		`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after) VALUES ('` + entry1 + `', '` + user + `', 'IRT', 50000, 'target_bonus', 'target:1', 50000)`,
		`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after) VALUES ('` + entry2 + `', '` + user + `', 'IRT', 90000, 'target_bonus', 'target:2', 140000)`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	sale := func(order, period string) error {
		return exec(`INSERT INTO core.reseller_sales (order_id, user_id, period) VALUES ($1, $2, $3)`, order, user, period)
	}
	for _, bad := range []string{"", "2026-13", "2026-00", "26-10", "2026-1", "2026/10", "2026-10-01", "abcd-ef"} {
		if err := sale(order1, bad); err == nil {
			t.Errorf("a sale in the month %q", bad)
		}
	}
	if err := sale(order1, "2026-10"); err != nil {
		t.Fatalf("a sale: %v", err)
	}
	if err := sale(order1, "2026-10"); err == nil {
		t.Error("one order counted twice")
	}
	if err := sale(order2, "1405-07"); err != nil { // a Jalali label is a month label too
		t.Errorf("a Jalali month: %v", err)
	}

	bonus := func(period string, threshold, sales int, amount int64, currency, entry string) error {
		return exec(`INSERT INTO core.reseller_target_bonuses (user_id, period, threshold, amount, currency, sales, ledger_entry_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, user, period, threshold, amount, currency, sales, entry)
	}
	if err := bonus("2026-10", 0, 1, 50000, "IRT", entry1); err == nil {
		t.Error("a tier of no services")
	}
	if err := bonus("2026-10", 5, 5, 0, "IRT", entry1); err == nil {
		t.Error("a bonus of nothing")
	}
	if err := bonus("2026-10", 5, 4, 50000, "IRT", entry1); err == nil {
		t.Error("a bonus paid before the tier was reached")
	}
	if err := bonus("2026-13", 5, 5, 50000, "IRT", entry1); err == nil {
		t.Error("a bonus for a month that does not exist")
	}
	if err := bonus("2026-10", 5, 5, 50000, "irt", entry1); err == nil {
		t.Error("a currency in small letters")
	}
	if err := bonus("2026-10", 5, 6, 50000, "IRT", entry1); err != nil {
		t.Fatalf("a bonus: %v", err)
	}
	if err := bonus("2026-10", 5, 7, 50000, "IRT", entry2); err == nil {
		t.Error("the same tier paid twice in a month")
	}
	if err := bonus("2026-10", 10, 10, 90000, "IRT", entry1); err == nil {
		t.Error("one ledger entry behind two bonuses")
	}
	if err := bonus("2026-11", 5, 5, 90000, "IRT", entry2); err != nil {
		t.Errorf("the same tier in the next month: %v", err)
	}

	if err := downTo(ctx, dsn, "core", 43); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name IN ('reseller_sales', 'reseller_target_bonuses')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after Down: %d (%v)", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name IN ('reseller_sales', 'reseller_target_bonuses')`).Scan(&left); err != nil || left != 2 {
		t.Fatalf("after Up again: %d (%v)", left, err)
	}
}
