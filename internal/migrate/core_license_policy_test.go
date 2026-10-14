package migrate

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// 00017 starts the implicit trial: an install that already exists (an
// upgrade) and a fresh one both get trial_started_at when this migration
// first runs, once. Running Up again does not move it, a second row is still
// impossible, and Down drops both columns.
func TestLicensePolicyMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	// An install from before the policy: its id exists, the columns do not.
	if err := upTo(ctx, dsn, "core", 16); err != nil {
		t.Fatalf("up to 16: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	var before string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM core.install`).Scan(&before); err != nil {
		t.Fatalf("the install id: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT trial_started_at FROM core.install`); err == nil {
		t.Fatal("the trial column exists before 00017")
	}

	started := time.Now()
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var (
		id      string
		trial   time.Time
		hit     sql.NullTime
		changed bool
	)
	if err := db.QueryRowContext(ctx, `SELECT id::text, trial_started_at, limit_hit_at FROM core.install`).Scan(&id, &trial, &hit); err != nil {
		t.Fatalf("after Up: %v", err)
	}
	if id != before {
		t.Errorf("the install id changed: %s %s", before, id)
	}
	if trial.Before(started.Add(-time.Minute)) || trial.After(time.Now().Add(time.Minute)) {
		t.Errorf("the trial started at %s, not when the migration ran (%s)", trial, started)
	}
	if hit.Valid {
		t.Errorf("a limit hit before any refusal: %v", hit)
	}

	// Once: another Up (a restart) and later queries keep the first moment.
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT trial_started_at <> $1 FROM core.install`, trial).Scan(&changed); err != nil || changed {
		t.Fatalf("the trial start moved: %v %v", changed, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.install DEFAULT VALUES`); err == nil {
		t.Error("a second install row was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE core.install SET trial_started_at = NULL`); err == nil {
		t.Error("an install without a trial start was accepted")
	}

	if err := downTo(ctx, dsn, "core", 16); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'install' AND column_name IN ('trial_started_at', 'limit_hit_at')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("columns left after Down: %d %v", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up once more: %v", err)
	}
}
