package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00030 adds the monitoring time series (two tables) and one partial index on the outbox for the
// alert history; Down removes them and touches no other data.
func TestMonitorMigration(t *testing.T) {
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
	has := func(q string, args ...any) bool {
		var n int
		if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	table := func(name string) bool {
		return has(`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = $1`, name)
	}
	index := func() bool {
		return has(`SELECT count(*) FROM pg_indexes WHERE schemaname = 'core' AND indexname = 'outbox_core_alerts_idx'`)
	}
	if table("monitor_metric") || table("monitor_series") || index() {
		t.Fatal("monitoring objects exist before 00030")
	}
	// A row that must survive the migration both ways.
	if _, err := db.ExecContext(ctx, `INSERT INTO core.outbox_core (topic, payload, event_id) VALUES ('system.alert.v1', '{}', gen_random_uuid())`); err != nil {
		t.Fatal(err)
	}

	if err := upTo(ctx, dsn, "core", 30); err != nil {
		t.Fatalf("up to 30: %v", err)
	}
	if !table("monitor_metric") || !table("monitor_series") || !index() {
		t.Fatal("00030 did not create the monitoring objects")
	}
	var def string
	if err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'core' AND indexname = 'outbox_core_alerts_idx'`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "system.alert.v1") || !strings.Contains(def, "(id)") {
		t.Errorf("outbox_core_alerts_idx: %s", def)
	}

	// A block holds exactly one hour of 30-second slots, or one day of 5-minute slots.
	if _, err := db.ExecContext(ctx, `INSERT INTO core.monitor_metric (name) VALUES ('host.cpu_pct')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.monitor_series (metric_id, step, bucket, vals)
		SELECT id, 30, 0, array_fill(NULL::real, ARRAY[119]) FROM core.monitor_metric`); err == nil {
		t.Error("a block of 119 slots at step 30 was accepted")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.monitor_series (metric_id, step, bucket, vals)
		SELECT id, 60, 0, array_fill(NULL::real, ARRAY[120]) FROM core.monitor_metric`); err == nil {
		t.Error("a step of 60 seconds was accepted")
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO core.monitor_series (metric_id, step, bucket, vals)
		SELECT id, 300, 0, array_fill(NULL::real, ARRAY[288]) FROM core.monitor_metric`); err != nil {
		t.Errorf("a rolled day was refused: %v", err)
	}
	// Removing a metric removes its blocks.
	if _, err := db.ExecContext(ctx, `DELETE FROM core.monitor_metric`); err != nil {
		t.Fatal(err)
	}
	if has(`SELECT count(*) FROM core.monitor_series`) {
		t.Error("blocks outlived their metric")
	}

	if err := downTo(ctx, dsn, "core", 29); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if table("monitor_metric") || table("monitor_series") || index() {
		t.Error("00030's objects survived Down")
	}
	if !has(`SELECT count(*) FROM core.outbox_core WHERE topic = 'system.alert.v1'`) {
		t.Error("Down touched the outbox's rows")
	}
	if err := upTo(ctx, dsn, "core", 30); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
