package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func helpStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	if err := testdb.Truncate(context.Background(), s.DB(), `core.help_entries`); err != nil {
		t.Fatal(err)
	}
	return s
}

func helpFAQ(title string) *HelpEntry {
	return &HelpEntry{Kind: "faq", Title: map[string]string{"en": title}, Body: map[string]string{"en": "Answer to " + title}, Enabled: true}
}

func helpIDs(list []HelpEntry) []string {
	out := []string{}
	for _, e := range list {
		out = append(out, e.ID)
	}
	return out
}

func TestHelpInsertListGet(t *testing.T) {
	s := helpStore(t)
	ctx := context.Background()
	a, b, c := helpFAQ("a"), helpFAQ("b"), helpFAQ("c")
	tut := &HelpEntry{Kind: "tutorial", Title: map[string]string{"fa": "آموزش"}, Body: map[string]string{"fa": "متن"}, Enabled: false,
		Buttons: []HelpButton{{URL: "https://example.com/x", Label: map[string]string{"fa": "دانلود", "en": "Download"}}}}
	for _, e := range []*HelpEntry{a, b, tut, c} {
		if err := s.InsertHelp(ctx, s.Conn(), e); err != nil {
			t.Fatal(err)
		}
	}
	if a.ID == "" || a.Sort != 1 || b.Sort != 2 || c.Sort != 3 || tut.Sort != 1 || a.Version != 1 || a.CreatedAt.IsZero() {
		t.Fatalf("filled in: %+v %+v %+v %+v", a, b, c, tut)
	}
	faq, err := s.ListHelp(ctx, s.Conn(), "faq", false)
	if err != nil || !slices.Equal(helpIDs(faq), []string{a.ID, b.ID, c.ID}) {
		t.Fatalf("faq list: %v %v", helpIDs(faq), err)
	}
	all, err := s.ListHelp(ctx, s.Conn(), "", false)
	if err != nil || len(all) != 4 || all[0].Kind != "faq" || all[3].Kind != "tutorial" {
		t.Fatalf("all: %+v %v", all, err)
	}
	if shown, err := s.ListHelp(ctx, s.Conn(), "tutorial", true); err != nil || len(shown) != 0 {
		t.Fatalf("a switched-off tutorial is listed for customers: %+v %v", shown, err)
	}
	got, err := s.GetHelp(ctx, s.Conn(), tut.ID, false)
	if err != nil || got.Title["fa"] != "آموزش" || got.Enabled || len(got.Buttons) != 1 || got.Buttons[0].Label["en"] != "Download" ||
		got.Buttons[0].URL != "https://example.com/x" {
		t.Fatalf("tutorial round trip: %+v %v", got, err)
	}
	if empty, _ := s.GetHelp(ctx, s.Conn(), a.ID, false); empty.Buttons == nil || len(empty.Buttons) != 0 {
		t.Fatalf("a question has an empty list of buttons, not none: %#v", empty.Buttons)
	}
	if _, err := s.GetHelp(ctx, s.Conn(), "00000000-0000-7000-8000-00000000dead", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if n, err := s.CountHelp(ctx, s.Conn(), "faq"); err != nil || n != 3 {
		t.Fatalf("count: %d %v", n, err)
	}
}

// An entry made first comes first; the ones made after it take the next place
// even when others were deleted; equal places keep the order they were made in.
func TestHelpOrderAndTies(t *testing.T) {
	s := helpStore(t)
	ctx := context.Background()
	a, b, c := helpFAQ("a"), helpFAQ("b"), helpFAQ("c")
	for _, e := range []*HelpEntry{a, b, c} {
		if err := s.InsertHelp(ctx, s.Conn(), e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteHelp(ctx, s.Conn(), b.ID); err != nil {
		t.Fatal(err)
	}
	d := helpFAQ("d")
	if err := s.InsertHelp(ctx, s.Conn(), d); err != nil || d.Sort != 4 {
		t.Fatalf("after a delete the next one goes last: %+v %v", d, err)
	}
	if err := s.DeleteHelp(ctx, s.Conn(), b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting twice: %v", err)
	}
	// Ties: all at the same place.
	if _, err := s.DB().Exec(ctx, `UPDATE core.help_entries SET sort = 0`); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListHelp(ctx, s.Conn(), "faq", false)
	if !slices.Equal(helpIDs(list), []string{a.ID, c.ID, d.ID}) {
		t.Fatalf("ties: %v want the order made in", helpIDs(list))
	}
}

func TestHelpUpdateVersions(t *testing.T) {
	s := helpStore(t)
	ctx := context.Background()
	e := helpFAQ("q")
	if err := s.InsertHelp(ctx, s.Conn(), e); err != nil {
		t.Fatal(err)
	}
	edit := *e
	edit.Title = map[string]string{"en": "q2", "fa": "پ"}
	edit.Body = map[string]string{"en": "a2", "fa": "ج"}
	edit.Enabled = false
	if err := s.UpdateHelp(ctx, s.Conn(), &edit, 1); err != nil || edit.Version != 2 {
		t.Fatalf("update: %+v %v", edit, err)
	}
	// An edit from the copy read before is refused, and changes nothing.
	stale := *e
	stale.Title = map[string]string{"en": "lost"}
	if err := s.UpdateHelp(ctx, s.Conn(), &stale, 1); !errors.Is(err, ErrStale) {
		t.Fatalf("stale edit: %v", err)
	}
	got, _ := s.GetHelp(ctx, s.Conn(), e.ID, false)
	if got.Version != 2 || got.Title["en"] != "q2" || got.Title["fa"] != "پ" || got.Enabled {
		t.Fatalf("stored: %+v", got)
	}
	if got.Sort != e.Sort || got.Kind != "faq" {
		t.Fatalf("an edit moved or retyped the entry: %+v", got)
	}
	// Gone: not found, not stale.
	ghost := *e
	ghost.ID = "00000000-0000-7000-8000-00000000dead"
	if err := s.UpdateHelp(ctx, s.Conn(), &ghost, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of a missing entry: %v", err)
	}
}

func TestHelpSetOrder(t *testing.T) {
	s := helpStore(t)
	ctx := context.Background()
	var faq []*HelpEntry
	for _, q := range []string{"a", "b", "c"} {
		e := helpFAQ(q)
		if err := s.InsertHelp(ctx, s.Conn(), e); err != nil {
			t.Fatal(err)
		}
		faq = append(faq, e)
	}
	tut := &HelpEntry{Kind: "tutorial", Title: map[string]string{"en": "t"}, Body: map[string]string{"en": "b"}, Enabled: true}
	if err := s.InsertHelp(ctx, s.Conn(), tut); err != nil {
		t.Fatal(err)
	}
	a, b, c := faq[0].ID, faq[1].ID, faq[2].ID
	order := func(ids ...string) ([]string, error) {
		var before []string
		err := s.WithTx(ctx, func(tx pgx.Tx) (err error) {
			before, err = s.SetHelpOrder(ctx, tx, "faq", ids)
			return err
		})
		return before, err
	}
	before, err := order(c, a, b)
	if err != nil || !slices.Equal(before, []string{a, b, c}) {
		t.Fatalf("reorder: before %v, %v", before, err)
	}
	list, _ := s.ListHelp(ctx, s.Conn(), "faq", false)
	if !slices.Equal(helpIDs(list), []string{c, a, b}) || list[0].Sort != 1 || list[2].Sort != 3 {
		t.Fatalf("after: %v %+v", helpIDs(list), list)
	}
	if got, _ := s.GetHelp(ctx, s.Conn(), tut.ID, false); got.Sort != 1 {
		t.Fatalf("the tutorials moved with the questions: %+v", got)
	}
	// The same order again changes nothing; a list that is not exactly the
	// entries of the kind is refused and changes nothing.
	if before, err := order(c, a, b); err != nil || !slices.Equal(before, []string{c, a, b}) {
		t.Fatalf("same order: %v %v", before, err)
	}
	for what, ids := range map[string][]string{
		"one missing":   {c, a},
		"one extra":     {c, a, b, tut.ID},
		"a stranger":    {c, a, "00000000-0000-7000-8000-00000000dead"},
		"a repeat":      {c, a, a},
		"no entries":    {},
		"a tutorial in": {c, a, tut.ID},
	} {
		if _, err := order(ids...); !errors.Is(err, ErrStale) {
			t.Errorf("%s: %v", what, err)
		}
	}
	list, _ = s.ListHelp(ctx, s.Conn(), "faq", false)
	if !slices.Equal(helpIDs(list), []string{c, a, b}) {
		t.Fatalf("a refused order changed the list: %v", helpIDs(list))
	}
}

func TestHelpEnabledKinds(t *testing.T) {
	s := helpStore(t)
	ctx := context.Background()
	check := func(wantFAQ, wantTut bool) {
		t.Helper()
		faq, tut, err := s.HelpEnabledKinds(ctx, s.Conn())
		if err != nil || faq != wantFAQ || tut != wantTut {
			t.Fatalf("kinds: faq %v tutorials %v (%v), want %v %v", faq, tut, err, wantFAQ, wantTut)
		}
	}
	check(false, false)
	q := helpFAQ("q")
	q.Enabled = false
	if err := s.InsertHelp(ctx, s.Conn(), q); err != nil {
		t.Fatal(err)
	}
	check(false, false) // switched off
	q.Enabled = true
	if err := s.UpdateHelp(ctx, s.Conn(), q, 1); err != nil {
		t.Fatal(err)
	}
	check(true, false)
	tut := &HelpEntry{Kind: "tutorial", Title: map[string]string{"en": "t"}, Body: map[string]string{"en": "b"}, Enabled: true}
	if err := s.InsertHelp(ctx, s.Conn(), tut); err != nil {
		t.Fatal(err)
	}
	check(true, true)
	if err := s.DeleteHelp(ctx, s.Conn(), q.ID); err != nil {
		t.Fatal(err)
	}
	check(false, true)
}

// The table itself refuses an entry nobody can read: the store cannot be made to
// hold one by a bug above it.
func TestHelpTableRefusesAnUnreadableEntry(t *testing.T) {
	s := helpStore(t)
	ctx := context.Background()
	if err := s.InsertHelp(ctx, s.Conn(), &HelpEntry{Kind: "faq", Title: map[string]string{"fa": "x"}, Body: map[string]string{"en": "y"}}); err == nil {
		t.Fatal("a title and a text in different languages was stored")
	}
	if err := s.InsertHelp(ctx, s.Conn(), &HelpEntry{Kind: "faq", Title: map[string]string{}, Body: map[string]string{}}); err == nil {
		t.Fatal("an empty entry was stored")
	}
	if err := s.InsertHelp(ctx, s.Conn(), &HelpEntry{Kind: "faq", Title: map[string]string{"en": "x"}, Body: map[string]string{"en": "y"},
		Buttons: []HelpButton{{URL: "https://example.com", Label: map[string]string{"en": "go"}}}}); err == nil {
		t.Fatal("a question with a button was stored")
	}
}
