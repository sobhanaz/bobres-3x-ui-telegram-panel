package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00058 on a store from before: an existing service and order wait for nothing (NULL) and keep their dates; a service
// takes 1 to 36500 days to wait for its first connection, and an order the same as its target. Down drops both columns,
// and Up brings them back empty.
func TestFirstUseMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 57); err != nil {
		t.Fatalf("up to 57: %v", err)
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
		uid  = "00000000-0000-0000-0000-000000005801"
		plan = "00000000-0000-0000-0000-000000005802"
		ord  = "00000000-0000-0000-0001-000000005803"
		sub  = "00000000-0000-0000-0002-000000005804"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + uid + `', 5801)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES ('` + ord + `', '` + uid + `', '` + plan + `', 'new', 'active', 1000, 'IRT', 'k-first-use')`,
		`INSERT INTO core.subscriptions (id, user_id, order_id, client_email, status, expires_at)
			VALUES ('` + sub + `', '` + uid + `', '` + ord + `', 'u5801-x', 'active', now() + interval '30 days')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	waiting := func() (days sql.NullInt64, expires sql.NullTime) {
		if err := db.QueryRowContext(ctx, `SELECT first_use_days, expires_at FROM core.subscriptions WHERE id = $1`, sub).Scan(&days, &expires); err != nil {
			t.Fatal(err)
		}
		return days, expires
	}
	if days, expires := waiting(); days.Valid || !expires.Valid {
		t.Fatalf("an existing service: waits %v, ends %v", days, expires)
	}
	var target sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT target_first_use_days FROM core.orders WHERE id = $1`, ord).Scan(&target); err != nil || target.Valid {
		t.Fatalf("an existing order: %v %v", target, err)
	}
	for _, n := range []int{0, -1, 36501} {
		if err := exec(`UPDATE core.subscriptions SET first_use_days = $2 WHERE id = $1`, sub, n); err == nil {
			t.Fatalf("a service waited %d days", n)
		}
		if err := exec(`UPDATE core.orders SET target_first_use_days = $2 WHERE id = $1`, ord, n); err == nil {
			t.Fatalf("an order aimed at %d days", n)
		}
	}
	if err := exec(`UPDATE core.subscriptions SET first_use_days = 30, expires_at = NULL WHERE id = $1`, sub); err != nil {
		t.Fatal(err)
	}
	if err := exec(`UPDATE core.orders SET target_first_use_days = 36500 WHERE id = $1`, ord); err != nil {
		t.Fatal(err)
	}
	if days, _ := waiting(); days.Int64 != 30 {
		t.Fatalf("waiting: %v", days)
	}

	if err := downTo(ctx, dsn, "core", 57); err != nil {
		t.Fatalf("Down: %v", err)
	}
	for _, col := range []string{"subscriptions.first_use_days", "orders.target_first_use_days"} {
		var has bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'core' AND table_name || '.' || column_name = $1)`, col).Scan(&has); err != nil || has {
			t.Fatalf("after Down: %s present %v %v", col, has, err)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if days, _ := waiting(); days.Valid {
		t.Fatalf("after Up again: %v", days)
	}
}
