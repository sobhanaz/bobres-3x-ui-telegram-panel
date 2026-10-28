package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00045: one spin per customer and day, a prize that has its ledger entry and a roll inside the weights; Down drops the table and Up brings it back.
func TestWheelMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
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
		user  = "00000000-0000-7000-8000-000000000f02"
		other = "00000000-0000-7000-8000-000000000f03"
		entry = "00000000-0000-7000-8000-000000000f04"
	)
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 3004)`,
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + other + `', 3005)`,
		`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after) VALUES ('` + entry + `', '` + user + `', 'IRT', 1000, 'wheel_prize', 'wheel:1', 1000)`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	spin := func(who, day string, prize int64, ledger any, roll, total int64) error {
		return exec(`INSERT INTO core.wheel_spins (user_id, day, prize, ledger_entry_id, roll, total_weight) VALUES ($1, $2::date, $3, $4, $5, $6)`,
			who, day, prize, ledger, roll, total)
	}
	if err := spin(user, "2026-10-15", -1, nil, 0, 8); err == nil {
		t.Error("a negative prize")
	}
	if err := spin(user, "2026-10-15", 1000, nil, 0, 8); err == nil {
		t.Error("a prize with no ledger entry")
	}
	if err := spin(user, "2026-10-15", 0, entry, 5, 8); err == nil {
		t.Error("a ledger entry behind no prize")
	}
	if err := spin(user, "2026-10-15", 0, nil, 8, 8); err == nil {
		t.Error("a roll as big as the total weight")
	}
	if err := spin(user, "2026-10-15", 0, nil, -1, 8); err == nil {
		t.Error("a negative roll")
	}
	if err := spin(user, "2026-10-15", 0, nil, 0, 0); err == nil {
		t.Error("no weights")
	}
	if err := spin(user, "2026-10-15", 1000, entry, 2, 8); err != nil {
		t.Fatalf("a winning spin: %v", err)
	}
	if err := spin(user, "2026-10-15", 0, nil, 6, 8); err == nil {
		t.Error("two spins on one day")
	}
	if err := spin(user, "2026-10-16", 0, nil, 6, 8); err != nil {
		t.Errorf("the next day: %v", err)
	}
	if err := spin(other, "2026-10-15", 0, nil, 6, 8); err != nil {
		t.Errorf("another customer the same day: %v", err)
	}
	if err := spin(user, "2026-10-17", 1000, entry, 2, 8); err == nil {
		t.Error("one ledger entry behind two spins")
	}

	if err := downTo(ctx, dsn, "core", 44); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'wheel_spins'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after Down: %d (%v)", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'wheel_spins'`).Scan(&left); err != nil || left != 1 {
		t.Fatalf("after Up again: %d (%v)", left, err)
	}
}
