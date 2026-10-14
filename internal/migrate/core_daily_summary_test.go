package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00019 adds the table that holds a day's claim on the daily sales summary and
// nothing else: one row per local date, with a zone and a number of recipients;
// Down drops it and touches no other data; Up runs again afterwards.
func TestDailySummaryMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 18); err != nil {
		t.Fatalf("up to 18: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	tables := func() (n int, has bool) {
		if err := db.QueryRowContext(ctx, `SELECT count(*), bool_or(table_name = 'daily_summary_claims')
			FROM information_schema.tables WHERE table_schema = 'core'`).Scan(&n, &has); err != nil {
			t.Fatal(err)
		}
		return n, has
	}
	before, has := tables()
	if has {
		t.Fatal("the table exists before 00019")
	}
	// Data that must survive both directions.
	if _, err := db.ExecContext(ctx, `INSERT INTO core.settings (key, value) VALUES ('reports.daily_summary', '"true"')`); err != nil {
		t.Fatal(err)
	}

	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if n, has := tables(); !has || n != before+1 {
		t.Fatalf("%d tables before, %d after (has the table: %v): 00019 adds exactly one", before, n, has)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.daily_summary_claims (day, zone, recipients) VALUES ('2026-10-13', 'Asia/Tehran', 2)`); err != nil {
		t.Fatalf("a claim: %v", err)
	}
	var claimed sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT claimed_at FROM core.daily_summary_claims WHERE day = '2026-10-13'`).Scan(&claimed); err != nil || !claimed.Valid {
		t.Fatalf("claimed_at has no default: %v %v", claimed, err)
	}
	for name, q := range map[string]string{
		"the same day twice":   `INSERT INTO core.daily_summary_claims (day, zone, recipients) VALUES ('2026-10-13', 'UTC', 1)`,
		"no zone":              `INSERT INTO core.daily_summary_claims (day, zone, recipients) VALUES ('2026-10-14', '', 1)`,
		"a huge zone":          `INSERT INTO core.daily_summary_claims (day, zone, recipients) VALUES ('2026-10-14', repeat('a', 65), 1)`,
		"nobody told":          `INSERT INTO core.daily_summary_claims (day, zone, recipients) VALUES ('2026-10-14', 'UTC', 0)`,
		"no day":               `INSERT INTO core.daily_summary_claims (zone, recipients) VALUES ('UTC', 1)`,
		"a day that is no day": `INSERT INTO core.daily_summary_claims (day, zone, recipients) VALUES ('2026-02-30', 'UTC', 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// The same statements run again do not fail and keep the rows (CREATE TABLE IF NOT EXISTS).
	body, err := fsys.ReadFile("migrations/core/00019_daily_summary.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(body), "-- +goose Down")
	if !strings.Contains(up, "CREATE TABLE IF NOT EXISTS daily_summary_claims") || !strings.Contains(string(body), "DROP TABLE IF EXISTS daily_summary_claims") {
		t.Fatal("the migration is not written with IF NOT EXISTS and IF EXISTS")
	}
	again := strings.NewReplacer("-- +goose Up", "", "-- +goose StatementBegin", "", "-- +goose StatementEnd", "",
		"IF NOT EXISTS daily_summary_claims", "IF NOT EXISTS core.daily_summary_claims").Replace(up)
	if _, err := db.ExecContext(ctx, again); err != nil {
		t.Errorf("running the Up statements again: %v", err)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.daily_summary_claims`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("%d claims after running the statements again, want the 1 there: %v", rows, err)
	}
	if n, _ := tables(); n != before+1 {
		t.Errorf("%d tables after running them again", n)
	}

	if err := downTo(ctx, dsn, "core", 18); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if n, has := tables(); has || n != before {
		t.Errorf("%d tables after Down (the table is there: %v), want %d", n, has, before)
	}
	var setting string
	if err := db.QueryRowContext(ctx, `SELECT value::text FROM core.settings WHERE key = 'reports.daily_summary'`).Scan(&setting); err != nil || setting != `"true"` {
		t.Errorf("a setting did not survive: %q %v", setting, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if _, has := tables(); !has {
		t.Error("the table is missing after Up again")
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.daily_summary_claims`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d claims after down and up (the table is new): %v", n, err)
	}
}
