package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00012 gives an existing store its install id (one row, made once) and an
// empty license slot that holds at most one token; Down drops both and Up
// makes a new id.
func TestLicenseMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 11); err != nil {
		t.Fatalf("up to 11: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	const uid = "00000000-0000-0000-0000-000000000501"
	if _, err := db.ExecContext(ctx, `INSERT INTO core.users (id, telegram_id) VALUES ($1, 501)`, uid); err != nil {
		t.Fatal(err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var (
		id  string
		n   int
		lic int
	)
	if err := db.QueryRowContext(ctx, `SELECT (SELECT id::text FROM core.install), (SELECT count(*) FROM core.install),
		(SELECT count(*) FROM core.license)`).Scan(&id, &n, &lic); err != nil || id == "" || n != 1 || lic != 0 {
		t.Fatalf("after Up: id %q, %d install rows, %d licenses, %v", id, n, lic, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatal(err)
	}
	var again string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM core.install`).Scan(&again); err != nil || again != id {
		t.Fatalf("the install id changed: %q %q %v", id, again, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.license (id, token, updated_by) VALUES (1, 'a.b', $1)`, uid); err != nil {
		t.Fatalf("a license: %v", err)
	}
	for name, q := range map[string]string{ //nolint:gosec // SQL statements, no credentials
		"a second install id": `INSERT INTO core.install DEFAULT VALUES`,
		"a second license":    `INSERT INTO core.license (id, token) VALUES (2, 'c.d')`,
		"the same slot":       `INSERT INTO core.license (id, token) VALUES (1, 'c.d')`,
		"an empty token":      `UPDATE core.license SET token = ''`,
		"a huge token":        `UPDATE core.license SET token = repeat('a', 16385)`,
		"an unknown staff":    `UPDATE core.license SET updated_by = '00000000-0000-0000-0000-0000000005ff'`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// Removing the staff member who installed it keeps the license.
	if _, err := db.ExecContext(ctx, `DELETE FROM core.users WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	var by sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT updated_by::text FROM core.license`).Scan(&by); err != nil || by.Valid {
		t.Fatalf("updated_by after the user went: %v %v", by, err)
	}

	if err := downTo(ctx, dsn, "core", 11); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'core' AND table_name IN ('install', 'license'))`).Scan(&left); err != nil || left {
		t.Fatalf("after Down: %v %v", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM core.install`).Scan(&again); err != nil || again == id {
		t.Fatalf("a new install after Down/Up: %q %v", again, err)
	}
}
