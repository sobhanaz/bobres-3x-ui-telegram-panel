package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00024 on a store from before: every existing service has no note (NULL),
// a note is at most 200 characters, and Down drops the column (the note goes
// with it) and Up brings it back empty.
func TestResellerNoteMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 23); err != nil {
		t.Fatalf("up to 23: %v", err)
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
		uid  = "00000000-0000-0000-0000-000000000601"
		plan = "00000000-0000-0000-0000-000000000602"
		ord  = "00000000-0000-0000-0001-000000000603"
		sub  = "00000000-0000-0000-0000-000000000604"
		srv  = "00000000-0000-7000-8000-000000000611"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + uid + `', 601)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES ('` + ord + `', '` + uid + `', '` + plan + `', 'new', 'active', 1000, 'IRT', 'k-note')`,
		`INSERT INTO core.subscriptions (id, user_id, order_id, server_id, client_email, status)
			VALUES ('` + sub + `', '` + uid + `', '` + ord + `', '` + srv + `', 'u601', 'active')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}

	note := func() (n sql.NullString) {
		if err := db.QueryRowContext(ctx, `SELECT note FROM core.subscriptions WHERE id = $1`, sub).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := note(); n.Valid {
		t.Fatalf("an existing service got a note: %q", n.String)
	}
	if err := exec(`UPDATE core.subscriptions SET note = $2 WHERE id = $1`, sub, strings.Repeat("ب", 200)); err != nil {
		t.Fatalf("a 200-character note: %v", err)
	}
	if err := exec(`UPDATE core.subscriptions SET note = $2 WHERE id = $1`, sub, strings.Repeat("x", 201)); err == nil {
		t.Fatal("a 201-character note was accepted")
	}
	if err := exec(`UPDATE core.subscriptions SET note = NULL WHERE id = $1`, sub); err != nil {
		t.Fatalf("clearing the note: %v", err)
	}

	if err := downTo(ctx, dsn, "core", 23); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var has bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'subscriptions' AND column_name = 'note')`).Scan(&has); err != nil || has {
		t.Fatalf("after Down: column present %v %v", has, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if n := note(); n.Valid {
		t.Fatalf("after Up again: note %q", n.String)
	}
}
