package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00037 on a store from before: a plan that gave a percent keeps giving it, for 30 days as before; other plans get 10 days and no text; a
// plan gives a percent or an amount, never both; the days are 1 to 365; a reward code can be for a buyer and any plan; a plan can be archived;
// and Down takes it all back (a code made for a buyer alone goes with it).
func TestRewardAmountMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 36); err != nil {
		t.Fatalf("up to 36: %v", err)
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
		marked = "00000000-0000-7000-8000-000000000901"
		plain  = "00000000-0000-7000-8000-000000000902"
		user   = "00000000-0000-7000-8000-000000000903"
	)
	for _, q := range []string{
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency, reward_percent) VALUES ('` + marked + `', '{"en":"Marked"}', 'time', 30, 1000, 'IRT', 20)`,
		`INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency) VALUES ('` + plain + `', '{"en":"Plain"}', 'time', 30, 1000, 'IRT')`,
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 903)`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var days int
	var text string
	var amount sql.NullInt64
	var deleted sql.NullTime
	for id, want := range map[string]int{marked: 30, plain: 10} {
		if err := db.QueryRowContext(ctx, `SELECT reward_days, reward_text_i18n::text, reward_amount, deleted_at FROM core.plans WHERE id = $1`, id).Scan(&days, &text, &amount, &deleted); err != nil {
			t.Fatal(err)
		}
		if days != want || text != "{}" || amount.Valid || deleted.Valid {
			t.Errorf("plan %s: days %d text %s amount %v deleted %v", id, days, text, amount, deleted)
		}
	}
	for _, ok := range []string{"reward_amount = 260000, reward_percent = NULL", "reward_days = 1", "reward_days = 365"} {
		if err := exec(`UPDATE core.plans SET ` + ok + ` WHERE id = '` + marked + `'`); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"reward_amount = 0", "reward_amount = -5", "reward_days = 0", "reward_days = 366", "reward_percent = 10"} {
		if err := exec(`UPDATE core.plans SET ` + bad + ` WHERE id = '` + marked + `'`); err == nil {
			t.Errorf("%s was accepted (the plan has an amount)", bad)
		}
	}
	if err := exec(`INSERT INTO core.discount_codes (code, amount, currency, max_uses, user_id) VALUES ('THX-DDDDDDDD', 260000, 'IRT', 1, $1)`, user); err != nil {
		t.Errorf("a reward code for a buyer, for any plan: %v", err)
	}
	if err := exec(`UPDATE core.plans SET deleted_at = now() WHERE id = '` + plain + `'`); err != nil {
		t.Errorf("archive: %v", err)
	}

	if err := downTo(ctx, dsn, "core", 36); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'plans'
		AND column_name IN ('reward_amount', 'reward_days', 'reward_text_i18n', 'deleted_at')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after Down: %d columns left (%v)", left, err)
	}
	var codes int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.discount_codes WHERE code = 'THX-DDDDDDDD'`).Scan(&codes); err != nil || codes != 0 {
		t.Fatalf("a code for no plan survived Down: %d %v", codes, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
