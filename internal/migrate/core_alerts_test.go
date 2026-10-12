package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00013 adds the watchdog's check states: one row per check, a known state
// and severity, args as a JSON object, passing_since only on a problem; Down
// drops it.
func TestAlertsMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 12); err != nil {
		t.Fatalf("up to 12: %v", err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper

	if _, err := db.ExecContext(ctx, `INSERT INTO core.alert_state (key, state, severity, title_key, args)
		VALUES ('service.bot', 'problem', 'critical', 'alert.service_down', '{"service": "bot"}')`); err != nil {
		t.Fatalf("a problem: %v", err)
	}
	var (
		detail, args  string
		sent, passing sql.NullTime
	)
	if err := db.QueryRowContext(ctx, `SELECT detail, args::text, last_sent_at, passing_since FROM core.alert_state
		WHERE key = 'service.bot'`).Scan(&detail, &args, &sent, &passing); err != nil || detail != "" ||
		args != `{"service": "bot"}` || sent.Valid || passing.Valid {
		t.Fatalf("defaults: %q %q %v %v %v", detail, args, sent, passing, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE core.alert_state SET passing_since = now() WHERE key = 'service.bot'`); err != nil {
		t.Fatalf("a problem passing again: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.alert_state (key, state) VALUES ('backup.disk', 'pending')`); err != nil {
		t.Fatalf("pending with defaults: %v", err)
	}
	for name, q := range map[string]string{
		"the same check twice": `INSERT INTO core.alert_state (key, state) VALUES ('service.bot', 'ok')`,
		"an empty key":         `INSERT INTO core.alert_state (key, state) VALUES ('', 'ok')`,
		"an unknown state":     `INSERT INTO core.alert_state (key, state) VALUES ('x', 'broken')`,
		"an unknown severity":  `INSERT INTO core.alert_state (key, state, severity) VALUES ('x', 'ok', 'info')`,
		"args not an object":   `INSERT INTO core.alert_state (key, state, args) VALUES ('x', 'ok', '[1]')`,
		"a huge detail":        `UPDATE core.alert_state SET detail = repeat('a', 1001)`,
		"pending, fixing":      `UPDATE core.alert_state SET passing_since = now() WHERE key = 'backup.disk'`,
		"ok, fixing":           `INSERT INTO core.alert_state (key, state, passing_since) VALUES ('x', 'ok', now())`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if err := downTo(ctx, dsn, "core", 12); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'core' AND table_name = 'alert_state')`).Scan(&left); err != nil || left {
		t.Fatalf("after Down: %v %v", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
