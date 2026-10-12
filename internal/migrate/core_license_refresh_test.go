package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00014 tells how the token in use came (a license installed before it
// counts as manual) and when a refresh installed it; Down drops both and
// keeps the license.
func TestLicenseRefreshMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 13); err != nil {
		t.Fatalf("up to 13: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	if _, err := db.ExecContext(ctx, `INSERT INTO core.license (id, token) VALUES (1, 'a.b')`); err != nil {
		t.Fatalf("an older license: %v", err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var (
		source    string
		refreshed sql.NullTime
	)
	if err := db.QueryRowContext(ctx, `SELECT source, refreshed_at FROM core.license`).Scan(&source, &refreshed); err != nil ||
		source != "manual" || refreshed.Valid {
		t.Fatalf("the older license: %q %v %v", source, refreshed, err)
	}
	for _, q := range []string{`UPDATE core.license SET source = 'activation'`,
		`UPDATE core.license SET source = 'refresh', refreshed_at = now()`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"an unknown source":          `UPDATE core.license SET source = 'paste', refreshed_at = NULL`,
		"no source":                  `UPDATE core.license SET source = NULL, refreshed_at = NULL`,
		"refreshed without a time":   `UPDATE core.license SET refreshed_at = NULL`,
		"a refresh time, not by one": `UPDATE core.license SET source = 'activation'`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if err := downTo(ctx, dsn, "core", 13); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var cols int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'license' AND column_name IN ('source', 'refreshed_at')`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("after Down: %d columns, %v", cols, err)
	}
	var token string
	if err := db.QueryRowContext(ctx, `SELECT token FROM core.license`).Scan(&token); err != nil || token != "a.b" {
		t.Fatalf("the license after Down: %q %v", token, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
