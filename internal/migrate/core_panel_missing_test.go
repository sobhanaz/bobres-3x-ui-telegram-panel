package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00040: a service records when the panel was first seen to lack its client (NULL while it has it), with an
// index over the marked ones only; Down takes both away and Up brings them back.
func TestPanelMissingMigration(t *testing.T) {
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
	column := func() (typ, nullable string, ok bool) {
		err := db.QueryRowContext(ctx, `SELECT data_type, is_nullable FROM information_schema.columns
			WHERE table_schema = 'core' AND table_name = 'subscriptions' AND column_name = 'panel_missing_since'`).Scan(&typ, &nullable)
		return typ, nullable, err == nil
	}
	index := func() (def string, ok bool) {
		err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'core' AND indexname = 'subscriptions_panel_missing_idx'`).Scan(&def)
		return def, err == nil
	}
	if typ, nullable, ok := column(); !ok || typ != "timestamp with time zone" || nullable != "YES" {
		t.Fatalf("the column: %q %q %v", typ, nullable, ok)
	}
	if def, ok := index(); !ok || !strings.Contains(def, "WHERE (panel_missing_since IS NOT NULL)") {
		t.Fatalf("the index: %q %v", def, ok)
	}
	if err := downTo(ctx, dsn, "core", 39); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if _, _, ok := column(); ok {
		t.Error("the column survived Down")
	}
	if _, ok := index(); ok {
		t.Error("the index survived Down")
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if _, _, ok := column(); !ok {
		t.Error("the column did not come back")
	}
}
