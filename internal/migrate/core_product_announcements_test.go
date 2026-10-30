package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00053: a customer's answer about product news, broadcasts of the segment announce (a Buy button's data on them only) and the
// announcements (somewhere to go, a channel status only with the channel); Down takes them out and Up brings them back.
func TestProductAnnouncementsMigration(t *testing.T) {
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
		user     = "00000000-0000-7000-8000-000000000f31"
		announce = "00000000-0000-7000-8000-000000000f32"
		plain    = "00000000-0000-7000-8000-000000000f33"
		ann      = "00000000-0000-7000-8000-000000000f34"
	)
	if err := exec(`INSERT INTO core.users (id, telegram_id, announce_optin, announce_asked_at) VALUES ('` + user + `', 3204, true, now())`); err != nil {
		t.Fatalf("a customer who said yes: %v", err)
	}
	insert := func(id, segment string, buy any) error {
		return exec(`INSERT INTO core.broadcasts (id, status, segment, texts, buy_callback, created_by) VALUES ($1, 'draft', $2, '{"fa":"x"}', $3, $4)`,
			id, segment, buy, user)
	}
	if err := insert(plain, "all", "an:b:p:1"); err == nil {
		t.Error("a Buy button on a broadcast to customers")
	}
	if err := insert(announce, "announce", ""); err == nil {
		t.Error("an empty Buy button")
	}
	if err := insert(announce, "announce", "an:b:p:0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("an announcement's private messages: %v", err)
	}
	if err := insert(plain, "active", nil); err != nil {
		t.Fatalf("a broadcast to customers: %v", err)
	}
	add := func(id string, channel, private bool, status string) error {
		return exec(`INSERT INTO core.announcements (id, product_kind, product_id, texts, to_channel, to_private, due_at, channel_status, created_by)
			VALUES ($1, 'plan', 'p1', '{}', $2, $3, now(), $4, $5)`, id, channel, private, status, user)
	}
	if err := add(ann, false, false, "none"); err == nil {
		t.Error("an announcement that goes nowhere")
	}
	if err := add(ann, false, true, "pending"); err == nil {
		t.Error("a channel post without the channel")
	}
	if err := add(ann, true, true, "pending"); err != nil {
		t.Fatalf("an announcement: %v", err)
	}
	if err := exec(`UPDATE core.announcements SET broadcast_id = $1 WHERE id = $2`, announce, ann); err != nil {
		t.Fatalf("its private messages: %v", err)
	}

	if err := downTo(ctx, dsn, "core", 52); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.broadcasts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("after Down, broadcasts: %d (%v), want only the one to customers", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = 'core'
		AND ((table_name = 'broadcasts' AND column_name = 'buy_callback')
		  OR (table_name = 'users' AND column_name IN ('announce_optin', 'announce_asked_at'))
		  OR table_name = 'announcements')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("after Down: %d columns left (%v)", n, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if err := insert(announce, "announce", "an:b:l:pro:12"); err != nil {
		t.Fatalf("after Up again: %v", err)
	}
}
