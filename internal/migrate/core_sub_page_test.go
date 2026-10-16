package migrate

import (
	"context"
	"database/sql"
	"regexp"
	"testing"
)

// 00020 on a store from before: every existing service gets its own page
// token, no two alike; new rows get one by default; the format and
// uniqueness are enforced; Down drops it and Up comes back.
func TestSubPageMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 19); err != nil {
		t.Fatalf("up to 19: %v", err)
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
		uid  = "00000000-0000-0000-0000-000000000501"
		plan = "00000000-0000-0000-0000-000000000502"
		srv  = "00000000-0000-7000-8000-000000000511"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + uid + `', 501)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	insert := func(id, email string) error {
		ord := "00000000-0000-0000-0001-" + id[24:] // one order per service
		if err := exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES ($1, $2, $3, 'new', 'active', 1000, 'IRT', $4)`, ord, uid, plan, "k-"+email); err != nil {
			return err
		}
		return exec(`INSERT INTO core.subscriptions (id, user_id, order_id, server_id, client_email, status, sub_link)
			VALUES ($1, $2, $3, $4, $5, 'active', 'https://old.example/sub/x')`, id, uid, ord, srv, email)
	}
	const a, b = "00000000-0000-0000-0000-000000000521", "00000000-0000-0000-0000-000000000522"
	if err := insert(a, "u501-a"); err != nil {
		t.Fatal(err)
	}
	if err := insert(b, "u501-b"); err != nil {
		t.Fatal(err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}

	format := regexp.MustCompile(`^[0-9a-f]{32}$`)
	tok := func(id string) string {
		var s string
		if err := db.QueryRowContext(ctx, `SELECT page_token FROM core.subscriptions WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	ta, tb := tok(a), tok(b)
	if !format.MatchString(ta) || !format.MatchString(tb) || ta == tb {
		t.Fatalf("backfilled tokens: %q %q", ta, tb)
	}

	// A row inserted without a token gets one by default.
	const c = "00000000-0000-0000-0000-000000000523"
	if err := insert(c, "u501-c"); err != nil {
		t.Fatal(err)
	}
	if tc := tok(c); !format.MatchString(tc) || tc == ta || tc == tb {
		t.Fatalf("default token %q", tc)
	}

	// Format and uniqueness are enforced.
	for name, token := range map[string]string{
		"duplicate": ta, "short": "abc", "upper": "ABCDEF0123456789ABCDEF0123456789",
		"not hex": "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "empty": "", "long": ta + "0",
	} {
		if err := exec(`UPDATE core.subscriptions SET page_token = $2 WHERE id = $1`, c, token); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := exec(`UPDATE core.subscriptions SET page_token = NULL WHERE id = $1`, c); err == nil {
		t.Error("a null token was accepted")
	}

	if err := downTo(ctx, dsn, "core", 19); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var has bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'subscriptions' AND column_name = 'page_token')`).Scan(&has); err != nil || has {
		t.Fatalf("after Down: column present %v %v", has, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	// Rows that existed before get new tokens, all valid and distinct.
	if t1, t2 := tok(a), tok(b); !format.MatchString(t1) || t1 == t2 {
		t.Fatalf("after Up again: %q %q", t1, t2)
	}
}
