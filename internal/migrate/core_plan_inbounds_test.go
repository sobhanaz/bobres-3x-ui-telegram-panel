package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00022 on a store from before: plans get no inbound choice (the default:
// every attachable inbound). A plan's inbounds go with the plan, each once
// per server, and must be positive; Down drops the table and Up comes back.
func TestPlanInboundsMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 20); err != nil {
		t.Fatalf("up to 20: %v", err)
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
		plan = "00000000-0000-0000-0000-000000000502"
		srv1 = "00000000-0000-7000-8000-000000000511"
		srv2 = "00000000-0000-7000-8000-000000000512"
	)
	if err := exec(`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.plan_inbounds`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after Up: %d rows, %v", n, err)
	}

	// The same inbound id on two servers is two choices; each is stored once.
	if err := exec(`INSERT INTO core.plan_inbounds (plan_id, server_id, inbound_id) VALUES ($1, $2, 1), ($1, $2, 2), ($1, $3, 1)`, plan, srv1, srv2); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"an inbound twice": `INSERT INTO core.plan_inbounds (plan_id, server_id, inbound_id) VALUES ('` + plan + `', '` + srv1 + `', 1)`,
		"no such plan":     `INSERT INTO core.plan_inbounds (plan_id, server_id, inbound_id) VALUES ('00000000-0000-0000-0000-0000000005ff', '` + srv1 + `', 1)`,
		"not an id":        `INSERT INTO core.plan_inbounds (plan_id, server_id, inbound_id) VALUES ('` + plan + `', 'default', 1)`,
		"inbound 0":        `INSERT INTO core.plan_inbounds (plan_id, server_id, inbound_id) VALUES ('` + plan + `', '` + srv1 + `', 0)`,
		"inbound negative": `INSERT INTO core.plan_inbounds (plan_id, server_id, inbound_id) VALUES ('` + plan + `', '` + srv1 + `', -1)`,
	} {
		if err := exec(q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// They go with their plan.
	if err := exec(`DELETE FROM core.plans`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.plan_inbounds`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("inbounds of a deleted plan: %d %v", n, err)
	}

	if err := downTo(ctx, dsn, "core", 21); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var hasTable bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'core' AND table_name = 'plan_inbounds')`).Scan(&hasTable); err != nil {
		t.Fatal(err)
	}
	if hasTable {
		t.Fatal("after Down: plan_inbounds is still there")
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
