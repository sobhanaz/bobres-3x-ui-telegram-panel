package migrate

import (
	"context"
	"database/sql"
	"testing"
)

// 00015 lets core.assets hold the bot photo, a JPEG only, beside the logo;
// Down removes the photo and keeps the logo.
func TestBotPhotoMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 14); err != nil {
		t.Fatalf("up to 14: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close() //nolint:errcheck // test helper
	put := func(name, ctype string) error {
		_, err := db.ExecContext(ctx, `INSERT INTO core.assets (name, content_type, data) VALUES ($1, $2, '\x01')`, name, ctype)
		return err
	}
	if err := put("logo", "image/png"); err != nil {
		t.Fatalf("a logo: %v", err)
	}
	if err := put("bot_photo", "image/jpeg"); err == nil {
		t.Fatal("a bot photo was accepted before 00015")
	}

	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := put("bot_photo", "image/jpeg"); err != nil {
		t.Fatalf("a bot photo: %v", err)
	}
	for name, ctype := range map[string]string{"bot_photo": "image/png", "banner": "image/jpeg"} {
		if _, err := db.ExecContext(ctx, `DELETE FROM core.assets WHERE name = 'bot_photo'`); err != nil {
			t.Fatal(err)
		}
		if err := put(name, ctype); err == nil {
			t.Errorf("%s as %s was accepted", name, ctype)
		}
	}
	if err := put("bot_photo", "image/jpeg"); err != nil {
		t.Fatalf("the bot photo again: %v", err)
	}

	if err := downTo(ctx, dsn, "core", 14); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var names []string
	rows, err := db.QueryContext(ctx, `SELECT name FROM core.assets ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "logo" {
		t.Fatalf("after Down: %v", names)
	}
	if err := put("bot_photo", "image/jpeg"); err == nil {
		t.Fatal("a bot photo was accepted after Down")
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
}
