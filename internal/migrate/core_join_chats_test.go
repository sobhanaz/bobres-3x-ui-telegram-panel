package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00028 adds the table of the extra required chats and nothing else: an install
// from before has none (its first required chat is the setting join.channel,
// which the migration leaves alone); the table takes the chats the settings
// take and refuses what they refuse; Down drops it and touches no other data;
// Up runs again afterwards.
func TestJoinChatsMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 27); err != nil {
		t.Fatalf("up to 27: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	tables := func() (n int, has bool) {
		if err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(bool_or(table_name = 'join_chats'), false)
			FROM information_schema.tables WHERE table_schema = 'core'`).Scan(&n, &has); err != nil {
			t.Fatal(err)
		}
		return n, has
	}
	before, has := tables()
	if has {
		t.Fatal("the table exists before 00028")
	}
	// A store that already requires a channel keeps it as it is.
	for k, v := range map[string]string{"join.channel": `"@mychannel"`, "join.link": `"https://t.me/mychannel"`} {
		if _, err := db.ExecContext(ctx, `INSERT INTO core.settings (key, value) VALUES ($1, $2::jsonb)`, k, v); err != nil {
			t.Fatal(err)
		}
	}

	if err := upTo(ctx, dsn, "core", 28); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if n, has := tables(); !has || n != before+1 {
		t.Fatalf("%d tables before, %d after (has the table: %v): 00028 adds exactly one", before, n, has)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.join_chats`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("%d extra chats after the migration, want none: %v", rows, err)
	}
	var channel string
	if err := db.QueryRowContext(ctx, `SELECT value::text FROM core.settings WHERE key = 'join.channel'`).Scan(&channel); err != nil || channel != `"@mychannel"` {
		t.Fatalf("the first required chat changed: %q %v", channel, err)
	}

	insert := func(position int, chat, link, label string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO core.join_chats (position, chat, link, label) VALUES ($1, $2, $3, $4)`, position, chat, link, label)
		return err
	}
	if err := insert(1, "@second_channel", "", ""); err != nil {
		t.Fatalf("a public chat with no link: %v", err)
	}
	if err := insert(2, "-1001234567890", "https://t.me/+AbCdEf123", "VIP room"); err != nil {
		t.Fatalf("a private chat with its link: %v", err)
	}
	for name, err := range map[string]error{
		"a position taken":             insert(1, "@other_channel", "", ""),
		"a position of 0":              insert(0, "@zero_channel", "", ""),
		"a chat twice":                 insert(3, "@second_channel", "", ""),
		"a chat twice, case aside":     insert(3, "@SECOND_Channel", "", ""),
		"a chat that is no chat":       insert(3, "channel", "", ""),
		"a short @name":                insert(3, "@abc", "", ""),
		"a private chat with no link":  insert(3, "-1009876543210", "", ""),
		"a label that is a novella":    insert(3, "@third_channel", "", strings.Repeat("x", 33)),
		"a link that is a novella":     insert(3, "@third_channel", "https://t.me/"+strings.Repeat("x", 1000), ""),
		"a chat that is a link":        insert(3, "https://t.me/third_channel", "", ""),
		"a user's numeric id, no -100": insert(3, "123456789", "", ""),
	} {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	var link, label string
	if err := db.QueryRowContext(ctx, `SELECT link, label FROM core.join_chats WHERE position = 1`).Scan(&link, &label); err != nil || link != "" || label != "" {
		t.Errorf("link and label have no defaults: %q %q %v", link, label, err)
	}

	// the same statements run again do not fail and keep the rows (IF NOT EXISTS)
	body, err := fsys.ReadFile("migrations/core/00028_join_chats.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(body), "-- +goose Down")
	if !strings.Contains(up, "CREATE TABLE IF NOT EXISTS join_chats") || !strings.Contains(string(body), "DROP TABLE IF EXISTS join_chats") {
		t.Fatal("the migration is not written with IF NOT EXISTS and IF EXISTS")
	}
	again := strings.NewReplacer("-- +goose Up", "", "-- +goose StatementBegin", "", "-- +goose StatementEnd", "").Replace(up)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path TO core`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, again); err != nil {
		t.Errorf("running the Up statements again: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Errorf("committing them: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.join_chats`).Scan(&rows); err != nil || rows != 2 {
		t.Errorf("%d rows after running the statements again, want the 2 there: %v", rows, err)
	}

	if err := downTo(ctx, dsn, "core", 27); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if n, has := tables(); has || n != before {
		t.Errorf("%d tables after Down (the table is there: %v), want %d", n, has, before)
	}
	if err := db.QueryRowContext(ctx, `SELECT value::text FROM core.settings WHERE key = 'join.channel'`).Scan(&channel); err != nil || channel != `"@mychannel"` {
		t.Errorf("the first required chat did not survive: %q %v", channel, err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if _, has := tables(); !has {
		t.Error("the table is missing after Up again")
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.join_chats`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("%d rows after down and up (the table is new): %v", rows, err)
	}
}
