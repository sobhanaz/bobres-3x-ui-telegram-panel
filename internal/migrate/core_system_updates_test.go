package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00025 adds the table that records the updates asked for in the dashboard and
// nothing else: one row per request, an outcome that is NULL while it is open,
// and at most one open at a time; Down drops it and touches no other data; Up
// runs again afterwards.
func TestSystemUpdatesMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 24); err != nil {
		t.Fatalf("up to 24: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	tables := func() (n int, has bool) {
		if err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(bool_or(table_name = 'system_updates'), false)
			FROM information_schema.tables WHERE table_schema = 'core'`).Scan(&n, &has); err != nil {
			t.Fatal(err)
		}
		return n, has
	}
	before, has := tables()
	if has {
		t.Fatal("the table exists before 00025")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.settings (key, value) VALUES ('updates.channel', '"beta"')`); err != nil {
		t.Fatal(err)
	}

	if err := upTo(ctx, dsn, "core", 25); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if n, has := tables(); !has || n != before+1 {
		t.Fatalf("%d tables before, %d after (has the table: %v): 00025 adds exactly one", before, n, has)
	}
	const id1, id2 = "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f60", "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f61"
	insert := func(id string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel)
			VALUES ($1, 'boss', '0.6.1', '0.7.0', 'stable')`, id)
		return err
	}
	if err := insert(id1); err != nil {
		t.Fatalf("a request: %v", err)
	}
	var requested sql.NullTime
	var outcome sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT requested_at, outcome FROM core.system_updates WHERE id = $1`, id1).Scan(&requested, &outcome); err != nil ||
		!requested.Valid || outcome.Valid {
		t.Fatalf("requested_at has no default or the outcome is not NULL: %v %v %v", requested, outcome, err)
	}
	// one open update at a time
	if err := insert(id2); err == nil {
		t.Fatal("a second open update was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE core.system_updates SET outcome = 'done', outcome_at = now() WHERE id = $1`, id1); err != nil {
		t.Fatalf("ending it: %v", err)
	}
	if err := insert(id2); err != nil {
		t.Fatalf("a request after the first ended: %v", err)
	}
	for name, q := range map[string]string{
		"an id that is no uuid":       `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at) VALUES ('abc', 'b', '1.0.0', '1.1.0', 'stable', 'done', now())`,
		"an upper-case id":            `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at) VALUES (upper('` + id1 + `'), 'b', '1.0.0', '1.1.0', 'stable', 'done', now())`,
		"no name":                     `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f62', '', '1.0.0', '1.1.0', 'stable', 'done', now())`,
		"a long name":                 `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f62', repeat('x', 65), '1.0.0', '1.1.0', 'stable', 'done', now())`,
		"a channel that is none":      `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f62', 'b', '1.0.0', '1.1.0', 'nightly', 'done', now())`,
		"an outcome nobody knows":     `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f62', 'b', '1.0.0', '1.1.0', 'stable', 'exploded', now())`,
		"an outcome with no time":     `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f62', 'b', '1.0.0', '1.1.0', 'stable', 'done')`,
		"a time with no outcome":      `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f62', 'b', '1.0.0', '1.1.0', 'stable', now())`,
		"no versions":                 `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f62', 'b', '', '', 'stable', 'done', now())`,
		"a message that is a novella": `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at, message) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f62', 'b', '1.0.0', '1.1.0', 'stable', 'failed', now(), repeat('x', 301))`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// the same statements run again do not fail and keep the rows (IF NOT EXISTS)
	body, err := fsys.ReadFile("migrations/core/00025_system_updates.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(body), "-- +goose Down")
	if !strings.Contains(up, "CREATE TABLE IF NOT EXISTS system_updates") || !strings.Contains(string(body), "DROP TABLE IF EXISTS system_updates") {
		t.Fatal("the migration is not written with IF NOT EXISTS and IF EXISTS")
	}
	again := strings.NewReplacer("-- +goose Up", "", "-- +goose StatementBegin", "", "-- +goose StatementEnd", "").Replace(up)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path TO core`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, again); err != nil {
		t.Errorf("running the Up statements again: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Errorf("committing them: %v", err)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.system_updates`).Scan(&rows); err != nil || rows != 2 {
		t.Errorf("%d rows after running the statements again, want the 2 there: %v", rows, err)
	}

	if err := downTo(ctx, dsn, "core", 24); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if n, has := tables(); has || n != before {
		t.Errorf("%d tables after Down (the table is there: %v), want %d", n, has, before)
	}
	var setting string
	if err := db.QueryRowContext(ctx, `SELECT value::text FROM core.settings WHERE key = 'updates.channel'`).Scan(&setting); err != nil || setting != `"beta"` {
		t.Errorf("a setting did not survive: %q %v", setting, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if _, has := tables(); !has {
		t.Error("the table is missing after Up again")
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.system_updates`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows after down and up (the table is new): %v", n, err)
	}
}
