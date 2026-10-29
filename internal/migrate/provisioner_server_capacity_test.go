package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00007: every server that exists starts with no limit (0); the table refuses a limit outside 0..1,000,000; Down takes the column away
// and nothing else, and an Up after it puts every server back to no limit.
func TestServerCapacityMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "provisioner", 6); err != nil {
		t.Fatalf("up to 6: %v", err)
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
		xs = "00000000-0000-7000-8000-000000000701"
		ms = "00000000-0000-7000-8000-000000000702"
	)
	for _, q := range []string{
		`INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc, kind) VALUES
			('` + xs + `', 'panel', 'https://a.test', '\x01', 'xui'),
			('` + ms + `', 'marz', 'https://b.test', '\x01', 'marzban')`,
		`INSERT INTO provisioner.client_map (subscription_id, server_id, email, inbound_ids)
			VALUES ('00000000-0000-7000-8000-000000000711', '` + xs + `', 'u1-a', '{1}')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	limits := func() (unlimited, total int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE max_clients = 0), count(*) FROM provisioner.xui_servers`).Scan(&unlimited, &total); err != nil {
			t.Fatal(err)
		}
		return unlimited, total
	}
	if unlimited, total := limits(); unlimited != 2 || total != 2 {
		t.Fatalf("%d of %d servers have no limit after Up, want both", unlimited, total)
	}
	var typ, nullable, def string
	if err := db.QueryRowContext(ctx, `SELECT data_type, is_nullable, column_default FROM information_schema.columns
		WHERE table_schema = 'provisioner' AND table_name = 'xui_servers' AND column_name = 'max_clients'`).Scan(&typ, &nullable, &def); err != nil ||
		typ != "integer" || nullable != "NO" || def != "0" {
		t.Fatalf("column: %q %q %q %v", typ, nullable, def, err)
	}
	// A limit can be stored at both ends, and nothing outside them.
	for _, ok := range []int{0, 1, 1_000_000} {
		if err := exec(`UPDATE provisioner.xui_servers SET max_clients = $2 WHERE id = $1`, xs, ok); err != nil {
			t.Errorf("a limit of %d refused: %v", ok, err)
		}
	}
	for _, bad := range []int{-1, 1_000_001} {
		if err := exec(`UPDATE provisioner.xui_servers SET max_clients = $2 WHERE id = $1`, xs, bad); err == nil {
			t.Errorf("a limit of %d accepted", bad)
		}
	}
	if err := exec(`UPDATE provisioner.xui_servers SET max_clients = 30 WHERE id = $1`, xs); err != nil {
		t.Fatal(err)
	}

	// Down: the column goes, the servers and their clients stay.
	if err := downTo(ctx, dsn, "provisioner", 6); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var servers, clients int
	var hasColumn bool
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM provisioner.xui_servers), (SELECT count(*) FROM provisioner.client_map),
		EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'provisioner' AND table_name = 'xui_servers' AND column_name = 'max_clients')`).
		Scan(&servers, &clients, &hasColumn); err != nil {
		t.Fatal(err)
	}
	if servers != 2 || clients != 1 || hasColumn {
		t.Fatalf("after Down: %d servers, %d clients, column %v", servers, clients, hasColumn)
	}
	if err := Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if unlimited, total := limits(); unlimited != 2 || total != 2 {
		t.Fatalf("%d of %d servers have no limit after Down and Up, want both", unlimited, total)
	}
}
