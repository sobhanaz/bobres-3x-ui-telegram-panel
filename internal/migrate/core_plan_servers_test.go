package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00011 on a store from before: plans get no servers (any server), services
// no stale link and no failed fetch. A plan's servers go with the plan, each
// once; Down drops it all and Up comes back.
func TestPlanServersMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 10); err != nil {
		t.Fatalf("up to 10: %v", err)
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
		uid  = "00000000-0000-0000-0000-000000000401"
		plan = "00000000-0000-0000-0000-000000000402"
		ord  = "00000000-0000-0000-0000-000000000403"
		sub  = "00000000-0000-0000-0000-000000000404"
		srv1 = "00000000-0000-7000-8000-000000000411"
		srv2 = "00000000-0000-7000-8000-000000000412"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + uid + `', 401)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES ('` + ord + `', '` + uid + `', '` + plan + `', 'new', 'active', 1000, 'IRT', 'k-401')`,
		`INSERT INTO core.subscriptions (id, user_id, order_id, server_id, client_email, status, sub_link)
			VALUES ('` + sub + `', '` + uid + `', '` + ord + `', '` + srv1 + `', 'u401-a', 'active', 'https://old.example/sub/x')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var (
		n, tries int
		stale    sql.NullTime
		link     string
	)
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM core.plan_servers), sub_link_stale_at, sub_link_attempts, sub_link
		FROM core.subscriptions WHERE id = $1`, sub).Scan(&n, &stale, &tries, &link); err != nil {
		t.Fatal(err)
	}
	if n != 0 || stale.Valid || tries != 0 || link != "https://old.example/sub/x" {
		t.Fatalf("after Up: %d plan servers, stale %v, %d attempts, link %q", n, stale, tries, link)
	}

	if err := exec(`INSERT INTO core.plan_servers (plan_id, server_id) VALUES ($1, $2), ($1, $3)`, plan, srv1, srv2); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"a server twice": `INSERT INTO core.plan_servers (plan_id, server_id) VALUES ('` + plan + `', '` + srv1 + `')`,
		"no such plan":   `INSERT INTO core.plan_servers (plan_id, server_id) VALUES ('00000000-0000-0000-0000-0000000004ff', '` + srv1 + `')`,
		"not an id":      `INSERT INTO core.plan_servers (plan_id, server_id) VALUES ('` + plan + `', 'default')`,
	} {
		if err := exec(q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The servers go with their plan (no order may hold it for this check).
	if err := exec(`DELETE FROM core.subscriptions; DELETE FROM core.orders; DELETE FROM core.plans`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.plan_servers`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("servers of a deleted plan: %d %v", n, err)
	}

	if err := downTo(ctx, dsn, "core", 10); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var hasTable, hasColumns bool
	if err := db.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'plan_servers'),
		EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'subscriptions'
			AND column_name IN ('sub_link_stale_at', 'sub_link_attempts'))`).
		Scan(&hasTable, &hasColumns); err != nil {
		t.Fatal(err)
	}
	if hasTable || hasColumns {
		t.Fatalf("after Down: plan_servers %v, sub_link columns %v", hasTable, hasColumns)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
