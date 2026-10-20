package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00005: every server that exists starts with the group backfill still to do
// (NULL), whatever its kind; the time can be stored; Down takes the column away
// and nothing else, and an Up after it puts every server back to "to do".
func TestGroupBackfillMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "provisioner", 4); err != nil {
		t.Fatalf("up to 4: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	const (
		xs = "00000000-0000-7000-8000-000000000501"
		ms = "00000000-0000-7000-8000-000000000502"
	)
	for _, q := range []string{
		`INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc, kind) VALUES
			('` + xs + `', 'panel', 'https://a.test', '\x01', 'xui'),
			('` + ms + `', 'marz', 'https://b.test', '\x01', 'marzban')`,
		`INSERT INTO provisioner.client_map (subscription_id, server_id, email, inbound_ids)
			VALUES ('00000000-0000-7000-8000-000000000511', '` + xs + `', 'u1-a', '{1}')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	done := func() (nulls int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM provisioner.xui_servers WHERE group_backfilled_at IS NULL`).Scan(&nulls); err != nil {
			t.Fatal(err)
		}
		return nulls
	}
	if n := done(); n != 2 {
		t.Fatalf("%d servers still to do after Up, want both", n)
	}
	var typ string
	if err := db.QueryRowContext(ctx, `SELECT data_type FROM information_schema.columns
		WHERE table_schema = 'provisioner' AND table_name = 'xui_servers' AND column_name = 'group_backfilled_at'`).Scan(&typ); err != nil || typ != "timestamp with time zone" {
		t.Fatalf("column type %q, %v", typ, err)
	}
	var updatedBefore, updatedAfter sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM provisioner.xui_servers WHERE id = $1`, xs).Scan(&updatedBefore); err != nil {
		t.Fatal(err)
	}
	if err := exec(`UPDATE provisioner.xui_servers SET group_backfilled_at = now() WHERE id = $1`, xs); err != nil {
		t.Fatal(err)
	}
	if n := done(); n != 1 {
		t.Fatalf("%d servers still to do after one is done", n)
	}
	// Recording it does not touch updated_at, which keys the cached panel clients
	// (the store sets only this column; the table has no trigger moving it).
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM provisioner.xui_servers WHERE id = $1`, xs).Scan(&updatedAfter); err != nil || updatedBefore.Time != updatedAfter.Time {
		t.Fatalf("updated_at moved: %v -> %v (%v)", updatedBefore.Time, updatedAfter.Time, err)
	}

	// Down: the column goes, the servers and their clients stay.
	if err := downTo(ctx, dsn, "provisioner", 4); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var servers, clients int
	var hasColumn bool
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM provisioner.xui_servers), (SELECT count(*) FROM provisioner.client_map),
		EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'provisioner' AND table_name = 'xui_servers' AND column_name = 'group_backfilled_at')`).
		Scan(&servers, &clients, &hasColumn); err != nil {
		t.Fatal(err)
	}
	if servers != 2 || clients != 1 || hasColumn {
		t.Fatalf("after Down: %d servers, %d clients, column %v", servers, clients, hasColumn)
	}
	if err := Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if n := done(); n != 2 {
		t.Fatalf("%d servers still to do after Down and Up, want both", n)
	}
}
