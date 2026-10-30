package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00008: S-UI and Hiddify servers can be added beside 3x-ui and Marzban, an
// unknown kind is still refused, and Down removes the S-UI and Hiddify servers
// with their clients (the old code cannot serve them) and keeps the others.
func TestMorePanelKindsMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "provisioner", 7); err != nil {
		t.Fatalf("up to 7: %v", err)
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
		xs = "00000000-0000-7000-8000-000000000801"
		ms = "00000000-0000-7000-8000-000000000802"
		ss = "00000000-0000-7000-8000-000000000803"
		hs = "00000000-0000-7000-8000-000000000804"
	)
	if err := exec(`INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc, kind) VALUES ($1, 'x', 'https://a.test', '\x01', 'xui'),
		($2, 'm', 'https://b.test', '\x01', 'marzban')`, xs, ms); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc, kind) VALUES ($1, 's', 'https://c.test', '\x01', 'sui')`, ss); err == nil {
		t.Fatal("an S-UI server accepted before the migration")
	}
	if err := Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := exec(`INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc, kind) VALUES ($1, 's', 'https://c.test', '\x01', 'sui'),
		($2, 'h', 'https://d.test/p', '\x01', 'hiddify')`, ss, hs); err != nil {
		t.Fatalf("S-UI and Hiddify servers: %v", err)
	}
	if err := exec(`INSERT INTO provisioner.xui_servers (name, base_url, api_token_enc, kind) VALUES ('bad', 'https://e.test', '\x01', 'nope')`); err == nil {
		t.Error("unknown kind accepted")
	}
	for i, sv := range []string{xs, ss, hs} {
		if err := exec(`INSERT INTO provisioner.client_map (subscription_id, server_id, email, inbound_ids)
			VALUES ($1, $2, $3, '{1}')`, "00000000-0000-7000-8000-00000000081"+string(rune('1'+i)), sv, "u-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := downTo(ctx, dsn, "provisioner", 7); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var servers, clients int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM provisioner.xui_servers), (SELECT count(*) FROM provisioner.client_map)`).Scan(&servers, &clients); err != nil {
		t.Fatal(err)
	}
	if servers != 2 || clients != 1 {
		t.Fatalf("after Down: %d servers, %d clients (want the 3x-ui and Marzban servers and the 3x-ui client)", servers, clients)
	}
	if err := Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
