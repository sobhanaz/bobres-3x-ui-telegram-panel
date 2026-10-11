package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00003 on the servers the old code left: names were not unique (the oldest
// keeps its name, the others get a suffix) and the new columns take their
// defaults. Down drops reservations, which the old code would take for
// delivered clients.
func TestServersMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "provisioner", 2); err != nil {
		t.Fatalf("up to 2: %v", err)
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
		s1 = "00000000-0000-7000-8000-000000000301"
		s2 = "00000000-0000-7000-8000-000000000302"
		s3 = "00000000-0000-7000-8000-000000000303"
	)
	for _, q := range []string{
		`INSERT INTO provisioner.xui_servers (id, name, base_url, api_token_enc, created_at) VALUES
			('` + s2 + `', 'default', 'https://b.test', '\x01', '2026-01-02'),
			('` + s1 + `', 'default', 'https://a.test', '\x01', '2026-01-01'),
			('` + s3 + `', 'other', 'https://c.test', '\x01', '2026-01-03')`,
		`INSERT INTO provisioner.client_map (subscription_id, server_id, email, inbound_ids)
			VALUES ('00000000-0000-7000-8000-000000000311', '` + s1 + `', 'u1-a', '{1}')`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	if err := Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	for id, want := range map[string]string{s1: "default", s2: "default (00000000)", s3: "other"} {
		var (
			name, managed, lastErr string
			weight                 int
			ok                     sql.NullBool
		)
		if err := db.QueryRowContext(ctx, `SELECT name, weight, managed, last_health_ok, last_error FROM provisioner.xui_servers WHERE id = $1`, id).
			Scan(&name, &weight, &managed, &ok, &lastErr); err != nil {
			t.Fatal(err)
		}
		if name != want || weight != 100 || managed != "env" || ok.Valid || lastErr != "" {
			t.Errorf("server %s: %q weight %d %s %v %q", id, name, weight, managed, ok, lastErr)
		}
	}
	var pending bool
	if err := db.QueryRowContext(ctx, `SELECT pending FROM provisioner.client_map`).Scan(&pending); err != nil || pending {
		t.Fatalf("an existing client became pending: %v %v", pending, err)
	}

	// The new checks hold.
	for name, q := range map[string]string{
		"duplicate name": `UPDATE provisioner.xui_servers SET name = 'other' WHERE id = '` + s1 + `'`,
		"weight 0":       `UPDATE provisioner.xui_servers SET weight = 0 WHERE id = '` + s1 + `'`,
		"weight 1001":    `UPDATE provisioner.xui_servers SET weight = 1001 WHERE id = '` + s1 + `'`,
		"managed":        `UPDATE provisioner.xui_servers SET managed = 'cli' WHERE id = '` + s1 + `'`,
		"inbound of no server": `INSERT INTO provisioner.server_inbounds (server_id, inbound_id)
			VALUES ('00000000-0000-7000-8000-0000000003ff', 1)`,
		"server with clients": `DELETE FROM provisioner.xui_servers WHERE id = '` + s1 + `'`,
	} {
		if err := exec(q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Inbound settings go with their server; a missing row means attach.
	if err := exec(`INSERT INTO provisioner.server_inbounds (server_id, inbound_id) VALUES ($1, 1), ($1, 2)`, s3); err != nil {
		t.Fatal(err)
	}
	var attach bool
	if err := db.QueryRowContext(ctx, `SELECT attach FROM provisioner.server_inbounds WHERE inbound_id = 1`).Scan(&attach); err != nil || !attach {
		t.Fatalf("default attach: %v %v", attach, err)
	}
	if err := exec(`DELETE FROM provisioner.xui_servers WHERE id = $1`, s3); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM provisioner.server_inbounds`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("inbound settings left: %d %v", left, err)
	}

	// Down: the reservation goes, the delivered client stays.
	if err := exec(`INSERT INTO provisioner.client_map (subscription_id, server_id, email, inbound_ids, pending)
		VALUES ('00000000-0000-7000-8000-000000000312', $1, 'u2-a', '{}', true)`, s1); err != nil {
		t.Fatal(err)
	}
	if err := downTo(ctx, dsn, "provisioner", 2); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var rows int
	var hasTable, hasWeight bool
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM provisioner.client_map),
		EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'provisioner' AND table_name = 'server_inbounds'),
		EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'provisioner' AND table_name = 'xui_servers' AND column_name = 'weight')`).
		Scan(&rows, &hasTable, &hasWeight); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || hasTable || hasWeight {
		t.Fatalf("after Down: %d client rows, server_inbounds %v, weight column %v", rows, hasTable, hasWeight)
	}
	if err := Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
