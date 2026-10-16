package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00019 adds the one index the churn report needs (and nothing else); Down
// removes it and touches no data.
func TestChurnIndexMigration(t *testing.T) {
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
	indexDef := func() string {
		var def string
		err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'core' AND indexname = 'subscriptions_expires_idx'`).Scan(&def)
		if err == sql.ErrNoRows {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return def
	}
	if indexDef() != "" {
		t.Fatal("subscriptions_expires_idx exists before 00019")
	}
	var before int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'core'`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	// Up to 19 only: later migrations (other features) may add indexes of their own.
	if err := upTo(ctx, dsn, "core", 19); err != nil {
		t.Fatalf("up to 19: %v", err)
	}
	if def := indexDef(); !strings.Contains(def, "(expires_at)") || !strings.Contains(def, "expires_at IS NOT NULL") ||
		!strings.Contains(def, "pending") || !strings.Contains(def, "deleted") {
		t.Errorf("subscriptions_expires_idx: %s", def)
	}
	var after int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'core'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Errorf("%d indexes before, %d after: 00019 adds exactly one", before, after)
	}

	if err := downTo(ctx, dsn, "core", 18); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if indexDef() != "" {
		t.Error("subscriptions_expires_idx survived Down")
	}
	if err := upTo(ctx, dsn, "core", 19); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
