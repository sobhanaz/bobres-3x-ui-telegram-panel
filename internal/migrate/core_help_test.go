package migrate

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// 00026 on a store from before: the help table appears empty, the checks keep a
// bad row out (a kind, the shape of the three JSON columns, at most 8 buttons,
// buttons only on a tutorial, an entry readable in some language), and Down
// drops the table and forgets that the default content was offered, so that Up
// again can offer it again.
func TestHelpContentMigration(t *testing.T) {
	dsn := testDSN(t)
	dropSchemas(t, dsn)
	ctx := context.Background()
	if err := upTo(ctx, dsn, "core", 25); err != nil {
		t.Fatalf("up to 25: %v", err)
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
	var before bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('core.help_entries') IS NOT NULL`).Scan(&before); err != nil || before {
		t.Fatalf("the table exists before its migration: %v %v", before, err)
	}
	// A store with the default content offered already (as a later version would have it).
	if err := exec(`INSERT INTO core.settings (key, value) VALUES ('state.help.default_applied', '"1"'), ('branding.name', '"Shop"')`); err != nil {
		t.Fatal(err)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up: %v", err)
	}
	count := func() (n int) {
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core.help_entries`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 0 {
		t.Fatalf("%d entries after the migration", n)
	}

	const (
		id1 = "00000000-0000-7000-8000-000000000a01"
		id2 = "00000000-0000-7000-8000-000000000a02"
	)
	faq := func(id, title, body string) error {
		return exec(`INSERT INTO core.help_entries (id, kind, title, body) VALUES ($1, 'faq', $2::jsonb, $3::jsonb)`, id, title, body)
	}
	if err := faq(id1, `{"fa":"سوال","en":"Question"}`, `{"fa":"پاسخ","en":"Answer"}`); err != nil {
		t.Fatalf("a good question: %v", err)
	}
	var sort, version int
	var enabled bool
	if err := db.QueryRowContext(ctx, `SELECT sort, version, enabled FROM core.help_entries WHERE id = $1`, id1).Scan(&sort, &version, &enabled); err != nil {
		t.Fatal(err)
	}
	if sort != 0 || version != 1 || !enabled {
		t.Fatalf("defaults: sort %d version %d enabled %v", sort, version, enabled)
	}
	// Readable in one language is enough.
	if err := faq(id2, `{"en":"Only English"}`, `{"en":"Answer"}`); err != nil {
		t.Fatalf("an entry in one language: %v", err)
	}

	buttons := func(n int) string {
		return "[" + strings.TrimSuffix(strings.Repeat(`{"url":"https://example.com","label":{"en":"Go"}},`, n), ",") + "]"
	}
	bad := map[string]error{
		"an unknown kind":                           exec(`INSERT INTO core.help_entries (id, kind, title, body) VALUES ('00000000-0000-7000-8000-000000000b01', 'news', '{"en":"T"}', '{"en":"B"}')`),
		"a title that is not an object":             exec(`INSERT INTO core.help_entries (id, kind, title, body) VALUES ('00000000-0000-7000-8000-000000000b02', 'faq', '"T"', '{"en":"B"}')`),
		"a body that is not an object":              exec(`INSERT INTO core.help_entries (id, kind, title, body) VALUES ('00000000-0000-7000-8000-000000000b03', 'faq', '{"en":"T"}', '[]')`),
		"buttons that are not a list":               exec(`INSERT INTO core.help_entries (id, kind, title, body, buttons) VALUES ('00000000-0000-7000-8000-000000000b04', 'tutorial', '{"en":"T"}', '{"en":"B"}', '{}')`),
		"nine buttons":                              exec(`INSERT INTO core.help_entries (id, kind, title, body, buttons) VALUES ('00000000-0000-7000-8000-000000000b05', 'tutorial', '{"en":"T"}', '{"en":"B"}', $1::jsonb)`, buttons(9)),
		"a button on a question":                    exec(`INSERT INTO core.help_entries (id, kind, title, body, buttons) VALUES ('00000000-0000-7000-8000-000000000b06', 'faq', '{"en":"T"}', '{"en":"B"}', $1::jsonb)`, buttons(1)),
		"a title without a text":                    faq("00000000-0000-7000-8000-000000000b07", `{"fa":"T"}`, `{}`),
		"a text without a title":                    faq("00000000-0000-7000-8000-000000000b08", `{}`, `{"fa":"B"}`),
		"a title and a text in different languages": faq("00000000-0000-7000-8000-000000000b09", `{"fa":"T"}`, `{"en":"B"}`),
		"empty strings":                             faq("00000000-0000-7000-8000-000000000b0a", `{"fa":"","en":""}`, `{"fa":"","en":""}`),
	}
	for what, err := range bad {
		if err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	if n := count(); n != 2 {
		t.Fatalf("%d entries, want the 2 good ones", n)
	}
	if err := exec(`INSERT INTO core.help_entries (id, kind, title, body, buttons) VALUES ('00000000-0000-7000-8000-000000000c01', 'tutorial', '{"en":"T"}', '{"en":"B"}', $1::jsonb)`, buttons(8)); err != nil {
		t.Fatalf("eight buttons on a tutorial: %v", err)
	}

	// Down drops the table and the marker, and leaves the other settings.
	if err := downTo(ctx, dsn, "core", 25); err != nil {
		t.Fatalf("Down: %v", err)
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('core.help_entries') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("the table is still there after Down: %v %v", exists, err)
	}
	var marker, name int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE key = 'state.help.default_applied'), count(*) FILTER (WHERE key = 'branding.name') FROM core.settings`).Scan(&marker, &name); err != nil {
		t.Fatal(err)
	}
	if marker != 0 || name != 1 {
		t.Fatalf("after Down: %d markers, %d other settings", marker, name)
	}
	if err := Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("Up again: %v", err)
	}
	if n := count(); n != 0 {
		t.Fatalf("%d entries after Up again", n)
	}
}
