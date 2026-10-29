package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00046 and 00047: a sign-in waiting for approval has one row, a status from a fixed list, a hash of 32 bytes for its cookie, and one row per
// approver whose nonce is a hash too; the approvers go with the sign-in, and Down drops both tables.
func TestLoginApprovalsMigration(t *testing.T) {
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
	exec := func(q string, args ...any) error {
		_, err := db.ExecContext(ctx, q, args...)
		return err
	}
	const (
		user     = "00000000-0000-7000-8000-000000000b01"
		approval = "00000000-0000-7000-8000-000000000b02"
	)
	hash := strings.Repeat("a", 32)
	if err := exec(`INSERT INTO core.users (id, telegram_id) VALUES ($1, 1001)`, user); err != nil {
		t.Fatal(err)
	}
	insert := func(id, method, status string, ticket []byte) error {
		return exec(`INSERT INTO core.login_approvals (id, user_id, method, ticket_hash, status, expires_at)
			VALUES ($1, $2, $3, $4, $5, now() + interval '5 minutes')`, id, user, method, ticket, status)
	}
	if err := insert(approval, "link", "pending", []byte(hash)); err != nil {
		t.Fatalf("a pending sign-in: %v", err)
	}
	if err := insert("00000000-0000-7000-8000-000000000b03", "link", "pending", []byte(hash)); err == nil {
		t.Error("two sign-ins with one cookie hash")
	}
	if err := insert("00000000-0000-7000-8000-000000000b04", "link", "pending", []byte("short")); err == nil {
		t.Error("a cookie hash of the wrong length")
	}
	if err := insert("00000000-0000-7000-8000-000000000b05", "sms", "pending", []byte(strings.Repeat("b", 32))); err == nil {
		t.Error("a method outside the list")
	}
	if err := insert("00000000-0000-7000-8000-000000000b06", "link", "maybe", []byte(strings.Repeat("c", 32))); err == nil {
		t.Error("a status outside the list")
	}
	if err := exec(`INSERT INTO core.login_approvals (id, user_id, method, ticket_hash, expires_at)
		VALUES ('00000000-0000-7000-8000-000000000b07', $1, 'password', $2, now() - interval '1 minute')`, user, []byte(strings.Repeat("d", 32))); err == nil {
		t.Error("a sign-in that ends before it begins")
	}

	nonce := []byte(strings.Repeat("n", 32))
	if err := exec(`INSERT INTO core.login_approval_targets (approval_id, telegram_id, user_id, role, nonce_hash) VALUES ($1, 1001, $2, 'self', $3)`,
		approval, user, nonce); err != nil {
		t.Fatalf("an approver: %v", err)
	}
	if err := exec(`INSERT INTO core.login_approval_targets (approval_id, telegram_id, user_id, role, nonce_hash) VALUES ($1, 1001, $2, 'self', $3)`,
		approval, user, nonce); err == nil {
		t.Error("one person asked twice")
	}
	if err := exec(`INSERT INTO core.login_approval_targets (approval_id, telegram_id, user_id, role, nonce_hash) VALUES ($1, 1002, $2, 'friend', $3)`,
		approval, user, nonce); err == nil {
		t.Error("an approver role outside the list")
	}
	if err := exec(`INSERT INTO core.login_approval_targets (approval_id, telegram_id, user_id, role, nonce_hash) VALUES ($1, 1003, $2, 'owner', $3)`,
		approval, user, []byte("plain")); err == nil {
		t.Error("a nonce that is not a hash")
	}
	if err := exec(`DELETE FROM core.login_approvals WHERE id = $1`, approval); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.login_approval_targets`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the approvers outlived the sign-in: %d %v", n, err)
	}

	if err := downTo(ctx, dsn, "core", 39); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core'
		AND table_name IN ('login_approvals', 'login_approval_targets')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("after Down: %d (%v)", left, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
