package migrate

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
)

// 00041: an order records the price it was listed at and the rate it was charged at, all three together or none, and Down drops them without touching
// what was charged.
func TestOrdersListPriceMigration(t *testing.T) {
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
		user = "00000000-0000-7000-8000-000000000c01"
		plan = "00000000-0000-7000-8000-000000000c02"
	)
	if err := exec(`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 2101)`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core.plans (id, name_i18n, kind, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'both', 4000000, 'USDT')`); err != nil {
		t.Fatal(err)
	}
	// order inserts a paid order of 1,176,000 Toman with the named list columns set to args.
	order := func(key string, cols []string, args ...any) error {
		names, marks := "", ""
		for i, c := range cols {
			names += ", " + c
			marks += ", $" + strconv.Itoa(i+2)
		}
		return exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key`+names+`)
			VALUES (gen_random_uuid(), '`+user+`', '`+plan+`', 'new', 'paid', 1176000, 'IRT', $1`+marks+`)`, append([]any{key}, args...)...)
	}
	all := []string{"list_amount", "list_currency", "rate"}
	if err := order("plain", nil); err != nil {
		t.Fatalf("an order with no list price: %v", err)
	}
	if err := order("listed", all, int64(4_000_000), "USDT", int64(294_000)); err != nil {
		t.Fatalf("an order with its list price and rate: %v", err)
	}
	for name, c := range map[string]struct {
		cols []string
		args []any
	}{
		"amount and currency only": {[]string{"list_amount", "list_currency"}, []any{int64(4_000_000), "USDT"}},
		"currency and rate only":   {[]string{"list_currency", "rate"}, []any{"USDT", int64(294_000)}},
		"amount and rate only":     {[]string{"list_amount", "rate"}, []any{int64(4_000_000), int64(294_000)}},
		"the amount only":          {[]string{"list_amount"}, []any{int64(4_000_000)}},
		"the rate only":            {[]string{"rate"}, []any{int64(294_000)}},
	} {
		if err := order("part "+name, c.cols, c.args...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := order("zero", all, int64(0), "USDT", int64(294_000)); err == nil {
		t.Error("a list price of 0")
	}
	if err := order("rate0", all, int64(4_000_000), "USDT", int64(0)); err == nil {
		t.Error("a rate of 0")
	}
	if err := order("badcur", all, int64(4_000_000), "usdt", int64(294_000)); err == nil {
		t.Error("a currency that is not a code")
	}
	if err := downTo(ctx, dsn, "core", 40); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.orders WHERE amount = 1176000 AND currency = 'IRT'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("the orders after Down: %d (%v)", n, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
