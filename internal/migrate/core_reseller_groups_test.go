package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00035 on a store from before: every reseller has no prefix and no group name (their clients are in the default group and have the
// usual names), the values are checked (a prefix is 2 to 10 of a-z and 0-9, a group name 2 to 24 of A-Z, 0-9 and _), each is
// unique across resellers, and Down drops the columns.
func TestResellerGroupsMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 34); err != nil {
		t.Fatalf("up to 34: %v", err)
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
		tier = "00000000-0000-7000-8000-000000000701"
		u1   = "00000000-0000-7000-8000-000000000711"
		u2   = "00000000-0000-7000-8000-000000000712"
	)
	for _, q := range []string{
		`INSERT INTO core.reseller_tiers (id, name, discount_percent, commission_percent) VALUES ('` + tier + `', 'Gold', 10, 5)`,
		`INSERT INTO core.users (id, telegram_id, role) VALUES ('` + u1 + `', 701, 'reseller'), ('` + u2 + `', 702, 'reseller')`,
		`INSERT INTO core.resellers (user_id, tier_id) VALUES ('` + u1 + `', '` + tier + `'), ('` + u2 + `', '` + tier + `')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var withNames int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.resellers WHERE config_prefix IS NOT NULL OR panel_group IS NOT NULL OR groups_synced_at IS NOT NULL`).Scan(&withNames); err != nil || withNames != 0 {
		t.Fatalf("resellers from before have names: %d %v", withNames, err)
	}

	set := func(user, col, val string) error {
		return exec(`UPDATE core.resellers SET `+col+` = $2 WHERE user_id = $1`, user, val)
	}
	for _, ok := range []struct{ col, val string }{{"config_prefix", "sobhan"}, {"config_prefix", "a1"}, {"panel_group", "SOBHANADMIN"}, {"panel_group", "A_1"}} {
		if err := set(u1, ok.col, ok.val); err != nil {
			t.Errorf("%s %q: %v", ok.col, ok.val, err)
		}
	}
	for _, bad := range []struct{ col, val string }{
		{"config_prefix", "A"}, {"config_prefix", "Sobhan"}, {"config_prefix", "abcdefghijk"}, {"config_prefix", "a-b"}, {"config_prefix", ""},
		{"panel_group", "a"}, {"panel_group", "sobhan"}, {"panel_group", "A-B"}, {"panel_group", "ABCDEFGHIJKLMNOPQRSTUVWXY"}, {"panel_group", ""},
	} {
		if err := set(u2, bad.col, bad.val); err == nil {
			t.Errorf("%s %q was accepted", bad.col, bad.val)
		}
	}
	// Unique across resellers; NULL is not a name, so any number of resellers have none.
	if err := set(u1, "config_prefix", "taken"); err != nil {
		t.Fatal(err)
	}
	if err := set(u2, "config_prefix", "taken"); err == nil {
		t.Error("a prefix two resellers share")
	}
	if err := set(u1, "panel_group", "SAME"); err != nil {
		t.Fatal(err)
	}
	if err := set(u2, "panel_group", "SAME"); err == nil {
		t.Error("a group name two resellers share")
	}

	if err := downTo(ctx, dsn, "core", 34); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'resellers'
		AND column_name IN ('config_prefix', 'panel_group', 'groups_synced_at')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after Down: %d columns left (%v)", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
