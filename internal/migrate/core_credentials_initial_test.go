package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00022 adds staff_credentials.must_change (false for every existing login)
// and the restricted session method 'password_initial'. Down ends restricted
// sessions and removes both.
func TestCredentialsInitialMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 21); err != nil {
		t.Fatalf("up to 21: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper

	const uid = "00000000-0000-0000-0000-000000000301"
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO core.users (id, telegram_id, role) VALUES ($1, 301, 'owner')`, uid)
	mustExec(`INSERT INTO core.staff_credentials (user_id, username, password_hash, totp_secret_enc) VALUES ($1, 'olduser', 'h', '\x01')`, uid)
	session := func(id, method string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO core.web_sessions (id_hash, user_id, csrf_token, method, expires_at)
			VALUES ($1::bytea, $2, 'c', $3, now() + interval '1 hour')`, id, uid, method)
		return err
	}
	if err := session("\\x01", "password_initial"); err == nil {
		t.Fatal("a restricted session was accepted before 00022")
	}

	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var must bool
	if err := db.QueryRowContext(ctx, `SELECT must_change FROM core.staff_credentials WHERE user_id = $1`, uid).Scan(&must); err != nil || must {
		t.Fatalf("existing login must_change = %v, %v (want false)", must, err)
	}
	for i, m := range []string{"link", "password", "password_initial"} {
		if err := session("\\x0"+string(rune('2'+i)), m); err != nil {
			t.Fatalf("session method %s: %v", m, err)
		}
	}
	if err := session("\\x09", "other"); err == nil {
		t.Fatal("an unknown session method was accepted")
	}

	if err := downTo(ctx, dsn, "core", 21); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.web_sessions`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("sessions after Down: %d, %v (the restricted one ends, the others stay)", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'core' AND table_name = 'staff_credentials' AND column_name = 'must_change'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("must_change after Down: %d, %v", n, err)
	}
	if err := session("\\x0a", "password_initial"); err == nil {
		t.Fatal("a restricted session was accepted after Down")
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
