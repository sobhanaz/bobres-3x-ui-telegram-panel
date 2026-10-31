package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00052: broadcasts to resellers (their filters only on that segment), core's own reminder without an author, a recipient's own text and one
// reminder claim a day; Down takes the messages to resellers out and Up brings the columns back.
func TestResellerMessagesMigration(t *testing.T) {
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
		user     = "00000000-0000-7000-8000-000000000f12"
		manual   = "00000000-0000-7000-8000-000000000f13"
		reminder = "00000000-0000-7000-8000-000000000f14"
		plain    = "00000000-0000-7000-8000-000000000f15"
	)
	if err := exec(`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 3104)`); err != nil {
		t.Fatal(err)
	}
	insert := func(id, segment string, debtOnly, settle bool, kind string, by any) error {
		return exec(`INSERT INTO core.broadcasts (id, status, segment, texts, reseller_debt_only, settle_button, kind, created_by)
			VALUES ($1, 'draft', $2, '{"fa":"x"}', $3, $4, $5, $6)`, id, segment, debtOnly, settle, kind, by)
	}
	if err := insert(manual, "all", true, false, "manual", user); err == nil {
		t.Error("a debt filter on a broadcast to customers")
	}
	if err := insert(manual, "all", false, true, "manual", user); err == nil {
		t.Error("a settle button on a broadcast to customers")
	}
	if err := insert(manual, "resellers", false, false, "manual", nil); err == nil {
		t.Error("a staff message without its author")
	}
	if err := insert(manual, "resellers", true, true, "manual", user); err != nil {
		t.Fatalf("a message to resellers in debt: %v", err)
	}
	if err := insert(reminder, "resellers", true, true, "reminder", nil); err != nil {
		t.Fatalf("core's weekly reminder: %v", err)
	}
	if err := insert(plain, "active", false, false, "manual", user); err != nil {
		t.Fatalf("a broadcast to customers: %v", err)
	}
	if err := exec(`INSERT INTO core.broadcast_recipients (broadcast_id, user_id, telegram_id, language, text) VALUES ($1, $2, 3104, 'fa', 'سلام')`,
		manual, user); err != nil {
		t.Fatalf("a recipient's own text: %v", err)
	}
	claim := func(day string) error {
		return exec(`INSERT INTO core.reseller_reminder_claims (day, zone, due_at, broadcast_id) VALUES ($1::date, 'Asia/Tehran', now(), $2)`, day, reminder)
	}
	if err := claim("2026-10-29"); err != nil {
		t.Fatalf("a claim: %v", err)
	}
	if err := claim("2026-10-29"); err == nil {
		t.Error("two claims on one day")
	}

	if err := downTo(ctx, dsn, "core", 51); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.broadcasts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("after Down, broadcasts: %d (%v), want only the one to customers", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core'
		AND ((table_name = 'broadcasts' AND column_name IN ('kind', 'settle_button', 'reseller_tier_id', 'reseller_debt_only'))
		  OR (table_name = 'broadcast_recipients' AND column_name = 'text')
		  OR table_name = 'reseller_reminder_claims')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after Down: %d columns left (%v)", n, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if err := insert(reminder, "resellers", false, false, "reminder", nil); err != nil {
		t.Fatalf("after Up again: %v", err)
	}
}
