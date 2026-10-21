package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00034 on a store from before: every plan stays as it was, in no group. A group is deleted without taking its plans
// along (they stay on sale, ungrouped), a plan cannot name a group that does not exist, and Down removes both.
func TestPlanGroupsMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 33); err != nil {
		t.Fatalf("up to 33: %v", err)
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
		plan  = "00000000-0000-0000-0000-000000000602"
		group = "00000000-0000-7000-8000-000000000611"
	)
	if err := exec(`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ($1, '{"en":"P"}', 'time', 30, 1000, 'IRT')`, plan); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var gid sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT group_id::text FROM core.plans WHERE id = $1`, plan).Scan(&gid); err != nil || gid.Valid {
		t.Fatalf("a plan from before is in group %v (%v)", gid, err)
	}

	if err := exec(`INSERT INTO core.plan_groups (id, name_i18n) VALUES ($1, '{"en":"Starter"}')`, group); err != nil {
		t.Fatal(err)
	}
	var texts string
	if err := db.QueryRowContext(ctx, `SELECT description_i18n::text FROM core.plan_groups WHERE id = $1`, group).Scan(&texts); err != nil || texts != "{}" {
		t.Fatalf("a group's default text: %q %v", texts, err)
	}
	if err := exec(`UPDATE core.plans SET group_id = $2 WHERE id = $1`, plan, group); err != nil {
		t.Fatal(err)
	}
	if err := exec(`UPDATE core.plans SET group_id = '00000000-0000-7000-8000-0000000006ff' WHERE id = $1`, plan); err == nil {
		t.Fatal("a plan took a group that does not exist")
	}
	// Deleting the group frees the plan, it does not delete it.
	if err := exec(`DELETE FROM core.plan_groups WHERE id = $1`, group); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.plans WHERE id = $1 AND group_id IS NULL`, plan).Scan(&n); err != nil || n != 1 {
		t.Fatalf("after deleting the group the plan is %d (%v)", n, err)
	}

	if err := downTo(ctx, dsn, "core", 33); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'plan_groups')
		OR EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'plans' AND column_name = 'group_id')`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left {
		t.Fatal("after Down: the groups are still there")
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
