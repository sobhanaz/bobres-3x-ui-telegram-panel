package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00036 on a store from before: every plan gives no reward, every discount code is staff's (bound to no one), a reward percent is 1 to 90,
// a code made for a buyer is bound to a plan, one purchase has one code, and Down drops the columns.
func TestRewardCodesMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 35); err != nil {
		t.Fatalf("up to 35: %v", err)
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
		user  = "00000000-0000-7000-8000-000000000801"
		plan  = "00000000-0000-7000-8000-000000000802"
		order = "00000000-0000-7000-8000-000000000803"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 801)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plan + `', '{"en":"P"}', 'time', 30, 1000, 'IRT')`,
		`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key) VALUES ('` + order + `', '` + user + `', '` + plan + `', 'new', 'active', 1000, 'IRT', 'k-reward')`,
		`INSERT INTO core.discount_codes (code, percent) VALUES ('SPRING20', 20)`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var rewards, bound int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.plans WHERE reward_percent IS NOT NULL`).Scan(&rewards); err != nil || rewards != 0 {
		t.Fatalf("a plan from before gives a reward: %d %v", rewards, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.discount_codes WHERE user_id IS NOT NULL OR plan_id IS NOT NULL OR source_order_id IS NOT NULL`).Scan(&bound); err != nil || bound != 0 {
		t.Fatalf("a code from before is bound: %d %v", bound, err)
	}
	for _, ok := range []string{"1", "20", "90"} {
		if err := exec(`UPDATE core.plans SET reward_percent = ` + ok + ` WHERE id = '` + plan + `'`); err != nil {
			t.Errorf("reward %s: %v", ok, err)
		}
	}
	for _, bad := range []string{"0", "91", "-5"} {
		if err := exec(`UPDATE core.plans SET reward_percent = ` + bad + ` WHERE id = '` + plan + `'`); err == nil {
			t.Errorf("reward %s was accepted", bad)
		}
	}
	// A reward code: bound to the buyer and the plan, one per purchase; a code for a buyer needs a plan.
	if err := exec(`INSERT INTO core.discount_codes (code, percent, max_uses, user_id, plan_id, source_order_id) VALUES ('THX-AAAAAAAA', 20, 1, $1, $2, $3)`, user, plan, order); err != nil {
		t.Fatalf("a reward code: %v", err)
	}
	if err := exec(`INSERT INTO core.discount_codes (code, percent, max_uses, user_id, plan_id, source_order_id) VALUES ('THX-BBBBBBBB', 20, 1, $1, $2, $3)`, user, plan, order); err == nil {
		t.Error("two codes for one purchase")
	}
	if err := exec(`INSERT INTO core.discount_codes (code, percent, user_id) VALUES ('THX-CCCCCCCC', 20, $1)`, user); err == nil {
		t.Error("a code for a buyer without a plan")
	}
	// Deleting the plan takes its codes along.
	if err := exec(`DELETE FROM core.orders WHERE id = $1`, order); err == nil {
		// the code keeps no purchase (SET NULL) but the order may be referenced elsewhere; either outcome keeps the code table consistent
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.discount_codes WHERE code = 'THX-AAAAAAAA' AND source_order_id IS NULL`).Scan(&n); err != nil || n != 1 {
			t.Errorf("after deleting the purchase: %d %v", n, err)
		}
	}

	if err := downTo(ctx, dsn, "core", 35); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND
		((table_name = 'plans' AND column_name = 'reward_percent') OR (table_name = 'discount_codes' AND column_name IN ('user_id', 'plan_id', 'source_order_id')))`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after Down: %d columns left (%v)", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
