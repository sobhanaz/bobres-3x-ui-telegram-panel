package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00016 adds the HTTPS setting, one row in auto mode; its checks keep an
// address to ip mode and a whole pair to manual mode. Down drops it.
// (00015 belongs to another change: the test starts from below both.)
func TestTLSMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 14); err != nil {
		t.Fatalf("up to 14: %v", err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	var (
		mode     string
		ip, cert sql.NullString
		key      []byte
	)
	if err := db.QueryRowContext(ctx, `SELECT mode, ip, cert_pem, key_sealed FROM core.tls_config WHERE id = 1`).
		Scan(&mode, &ip, &cert, &key); err != nil || mode != "auto" || ip.Valid || cert.Valid || key != nil {
		t.Fatalf("the seeded row: %q %v %v %v %v", mode, ip, cert, key, err)
	}
	for _, q := range []string{
		`UPDATE core.tls_config SET mode = 'ip', ip = '203.0.113.10'`,
		`UPDATE core.tls_config SET mode = 'manual', ip = NULL, cert_pem = 'chain', key_sealed = '\x01'`,
		`UPDATE core.tls_config SET mode = 'auto', cert_pem = NULL, key_sealed = NULL`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"a second row":           `INSERT INTO core.tls_config (id) VALUES (2)`,
		"an unknown mode":        `UPDATE core.tls_config SET mode = 'both'`,
		"ip mode without an ip":  `UPDATE core.tls_config SET mode = 'ip'`,
		"an ip outside ip mode":  `UPDATE core.tls_config SET ip = '203.0.113.10'`,
		"an empty ip":            `UPDATE core.tls_config SET mode = 'ip', ip = ''`,
		"manual without a pair":  `UPDATE core.tls_config SET mode = 'manual'`,
		"manual without the key": `UPDATE core.tls_config SET mode = 'manual', cert_pem = 'chain'`,
		"a pair outside manual":  `UPDATE core.tls_config SET cert_pem = 'chain', key_sealed = '\x01'`,
		"a key alone":            `UPDATE core.tls_config SET key_sealed = '\x01'`,
	} {
		if _, err := db.ExecContext(ctx, q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	if err := downTo(ctx, dsn, "core", 15); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.tables
		WHERE table_schema = 'core' AND table_name = 'tls_config')`).Scan(&exists); err != nil || exists {
		t.Fatalf("after Down: %v %v", exists, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
