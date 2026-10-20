package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00030 teaches the table of the dashboard's updates (00025) to hold restores too: three columns
// with defaults that keep every row there is an update, a channel that may be ” for a restore
// only, and the same single open request for both. Down removes the restores and the columns
// and keeps the updates; Up runs again afterwards.
func TestSystemRestoresMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 29); err != nil {
		t.Fatalf("up to 29: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	const upd, rst1, rst2 = "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f60", "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f61", "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f62"
	// an update from before the migration, ended
	if _, err := db.ExecContext(ctx, `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at)
		VALUES ($1, 'boss', '0.6.1', '0.7.0', 'stable', 'done', now())`, upd); err != nil {
		t.Fatal(err)
	}

	if err := upTo(ctx, dsn, "core", 30); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var action, subject, source string
	if err := db.QueryRowContext(ctx, `SELECT action, subject, source FROM core.system_updates WHERE id = $1`, upd).Scan(&action, &subject, &source); err != nil ||
		action != "update" || subject != "" || source != "" {
		t.Fatalf("the old update is %q %q %q: %v", action, subject, source, err)
	}
	// restore records a restore, open or (ended) already over
	restore := func(id string, ended bool) error {
		q := `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, action, subject, source)
			VALUES ($1, 'boss', '0.8.0', '0.8.0', '', 'restore', 'bobres-20261011T033000Z.bkp', 'backup')`
		if ended {
			q = `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, action, subject, source, outcome, outcome_at)
				VALUES ($1, 'boss', '0.8.0', '0.8.0', '', 'restore', 'bobres-20261011T033000Z.bkp', 'backup', 'done', now())`
		}
		_, err := db.ExecContext(ctx, q, id)
		return err
	}
	if err := restore(rst1, false); err != nil {
		t.Fatalf("a restore: %v", err)
	}
	// one open request at a time, an update or a restore
	if err := restore(rst2, false); err == nil {
		t.Fatal("a second open request was accepted")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel)
		VALUES ($1, 'boss', '0.8.0', '0.9.0', 'stable')`, rst2); err == nil {
		t.Fatal("an update was accepted while a restore is open")
	}
	if _, err := db.ExecContext(ctx, `UPDATE core.system_updates SET outcome = 'rolled_back', outcome_at = now() WHERE id = $1`, rst1); err != nil {
		t.Fatalf("ending it: %v", err)
	}
	// a restore the helper's status told core of, recorded already ended
	if err := restore(rst2, true); err != nil {
		t.Fatalf("a restore that is already over: %v", err)
	}
	for name, q := range map[string]string{
		"an update with no channel":       `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f70', 'b', '1.0.0', '1.1.0', '', 'done', now())`,
		"a restore with a channel":        `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, action, subject, source, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f70', 'b', '1.0.0', '1.0.0', 'stable', 'restore', 'x.bkp', 'upload', 'done', now())`,
		"a restore with no backup":        `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, action, subject, source, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f70', 'b', '1.0.0', '1.0.0', '', 'restore', '', 'upload', 'done', now())`,
		"a restore with no source":        `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, action, subject, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f70', 'b', '1.0.0', '1.0.0', '', 'restore', 'x.bkp', 'done', now())`,
		"an update that names a backup":   `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, subject, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f70', 'b', '1.0.0', '1.1.0', 'stable', 'x.bkp', 'done', now())`,
		"an action nobody knows":          `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, action, subject, source, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f70', 'b', '1.0.0', '1.0.0', '', 'format', 'x.bkp', 'upload', 'done', now())`,
		"a source nobody knows":           `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, action, subject, source, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f70', 'b', '1.0.0', '1.0.0', '', 'restore', 'x.bkp', 'url', 'done', now())`,
		"a backup name that is a novella": `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, action, subject, source, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f70', 'b', '1.0.0', '1.0.0', '', 'restore', repeat('x', 201), 'upload', 'done', now())`,
		"a channel that is none":          `INSERT INTO core.system_updates (id, requested_by_name, from_version, to_version, channel, outcome, outcome_at) VALUES ('0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f70', 'b', '1.0.0', '1.1.0', 'nightly', 'done', now())`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if err := downTo(ctx, dsn, "core", 29); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.system_updates`).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d rows after Down, want the one update: %v", n, err)
	}
	var cols int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core' AND table_name = 'system_updates'
		AND column_name IN ('action', 'subject', 'source')`).Scan(&cols); err != nil || cols != 0 {
		t.Errorf("%d columns left after Down: %v", cols, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
