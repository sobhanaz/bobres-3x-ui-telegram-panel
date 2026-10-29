package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00042 adds the one index "My codes" needs (a customer's reward codes by user, newest first, leaving out the codes staff made for
// everyone) and nothing else; Down removes it and touches no data.
func TestDiscountCodesUserIndexMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 39); err != nil {
		t.Fatalf("up to 39: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	indexDef := func() string {
		var def string
		err := db.QueryRowContext(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'core' AND indexname = 'discount_codes_user_idx'`).Scan(&def)
		if err == sql.ErrNoRows {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return def
	}
	if indexDef() != "" {
		t.Fatal("discount_codes_user_idx exists before 00042")
	}
	var before int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'core' AND tablename = 'discount_codes'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// A staff code and a buyer's code from before: the migration keeps both as they are.
	const user = "00000000-0000-7000-8000-000000000a01"
	for _, q := range []string{
		`INSERT INTO core.users (id, telegram_id) VALUES ('` + user + `', 4201)`,
		`INSERT INTO core.discount_codes (code, percent) VALUES ('SPRING20', 20)`,
		`INSERT INTO core.discount_codes (code, percent, max_uses, user_id) VALUES ('THX-AAAAAAAA', 20, 1, '` + user + `')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}

	// Up to 42 only: later migrations (other features) may add indexes of their own, on other tables.
	if err := upTo(ctx, dsn, "core", 42); err != nil {
		t.Fatalf("up to 42: %v", err)
	}
	if def := indexDef(); !strings.Contains(def, "(user_id, created_at DESC)") || !strings.Contains(def, "user_id IS NOT NULL") {
		t.Errorf("discount_codes_user_idx: %s", def)
	}
	var after int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'core' AND tablename = 'discount_codes'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Errorf("%d indexes on discount_codes before, %d after: 00042 adds exactly one", before, after)
	}
	var codes int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.discount_codes`).Scan(&codes); err != nil || codes != 2 {
		t.Errorf("codes after the migration: %d %v", codes, err)
	}

	if err := downTo(ctx, dsn, "core", 39); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if indexDef() != "" {
		t.Error("discount_codes_user_idx survived Down")
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.discount_codes`).Scan(&codes); err != nil || codes != 2 {
		t.Errorf("codes after Down: %d %v", codes, err)
	}
	if err := upTo(ctx, dsn, "core", 42); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
