package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00056 on a store from before: an existing order has no config name (NULL), a name is 1 to 64 characters, and Down
// drops the column (the name goes with it) and Up brings it back empty.
func TestConfigNamesMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 55); err != nil {
		t.Fatalf("up to 55: %v", err)
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
		uid  = "00000000-0000-0000-0000-000000005601"
		plan = "00000000-0000-0000-0000-000000005602"
		ord  = "00000000-0000-0000-0001-000000005603"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + uid + `', 5601)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES ('` + ord + `', '` + uid + `', '` + plan + `', 'new', 'awaiting_payment', 1000, 'IRT', 'k-name')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	name := func() (n sql.NullString) {
		if err := db.QueryRowContext(ctx, `SELECT client_name FROM core.orders WHERE id = $1`, ord).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := name(); n.Valid {
		t.Fatalf("an existing order got a config name: %q", n.String)
	}
	if err := exec(`UPDATE core.orders SET client_name = 'sob-sobhan1234' WHERE id = $1`, ord); err != nil {
		t.Fatalf("a config name: %v", err)
	}
	for _, bad := range []string{"", strings.Repeat("x", 65)} {
		if err := exec(`UPDATE core.orders SET client_name = $2 WHERE id = $1`, ord, bad); err == nil {
			t.Fatalf("config name %q was accepted", bad)
		}
	}
	if err := downTo(ctx, dsn, "core", 55); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var has bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'orders' AND column_name = 'client_name')`).Scan(&has); err != nil || has {
		t.Fatalf("after Down: column present %v %v", has, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if n := name(); n.Valid {
		t.Fatalf("after Up again: config name %q", n.String)
	}
}
