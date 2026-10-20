package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/helpcontent"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// --- what an entry may hold ---

// problemKeys writes problems as "field code arg", sorted, for comparing.
func problemKeys(ps []HelpProblem) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, strings.TrimSpace(p.Field+" "+p.Code+" "+p.Arg))
	}
	slices.Sort(out)
	return out
}

func faqInput(fa, en string) HelpInput {
	in := HelpInput{Kind: helpcontent.KindFAQ, Title: map[string]string{}, Body: map[string]string{}, Enabled: true}
	if fa != "" {
		in.Title["fa"], in.Body["fa"] = "سوال "+fa, "پاسخ "+fa
	}
	if en != "" {
		in.Title["en"], in.Body["en"] = "Question "+en, "Answer "+en
	}
	return in
}

func tutorialInput(title, body string, buttons ...store.HelpButton) HelpInput {
	return HelpInput{Kind: helpcontent.KindTutorial, Title: map[string]string{"en": title}, Body: map[string]string{"en": body}, Buttons: buttons, Enabled: true}
}

func goButton(url string) store.HelpButton {
	return store.HelpButton{URL: url, Label: map[string]string{"en": "Go"}}
}

func TestHelpNormalizeAcceptsGoodEntries(t *testing.T) {
	long := strings.Repeat("ا", helpcontent.MaxBody)                    // exactly the limit, in UTF-16 units
	tagged := "<b>" + strings.Repeat("x", helpcontent.MaxBody) + "</b>" // tags do not count
	eight := make([]store.HelpButton, helpcontent.MaxButtons)
	for i := range eight {
		eight[i] = goButton(fmt.Sprintf("https://example.com/%d", i))
	}
	for name, in := range map[string]HelpInput{
		"both languages":               faqInput("۱", "1"),
		"only Persian":                 faqInput("۱", ""),
		"only English":                 faqInput("", "1"),
		"the longest text":             {Kind: "faq", Title: map[string]string{"fa": "س"}, Body: map[string]string{"fa": long}},
		"tags do not count":            {Kind: "faq", Title: map[string]string{"en": "q"}, Body: map[string]string{"en": tagged}},
		"the longest question":         {Kind: "faq", Title: map[string]string{"en": strings.Repeat("q", helpcontent.MaxQuestion)}, Body: map[string]string{"en": "a"}},
		"the longest title":            tutorialInput(strings.Repeat("t", helpcontent.MaxTutorialTitle), "text"),
		"markup characters in a title": {Kind: "faq", Title: map[string]string{"en": "Billing & refunds <or> more?"}, Body: map[string]string{"en": "a"}},
		"a https link in the text": {Kind: "faq", Title: map[string]string{"en": "q"},
			Body: map[string]string{"en": `See <a href="https://example.com/a?x=1&amp;y=2">the page</a>, <i>please</i>.`}},
		"every tag the texts allow": {Kind: "faq", Title: map[string]string{"en": "q"},
			Body: map[string]string{"en": "<b>b</b> <i>i</i> <u>u</u> <s>s</s> <code>c</code> <pre>p</pre> <blockquote>q</blockquote> <tg-spoiler>x</tg-spoiler>"}},
		"eight buttons":                         tutorialInput("t", "b", eight...),
		"a tutorial with no button":             tutorialInput("t", "b"),
		"a button in one language":              tutorialInput("t", "b", store.HelpButton{URL: "https://example.com", Label: map[string]string{"fa": "برو"}}),
		"a button label with markup characters": tutorialInput("t", "b", store.HelpButton{URL: "https://example.com", Label: map[string]string{"en": "A & B"}}),
	} {
		if _, probs := NormalizeHelp(in); len(probs) > 0 {
			t.Errorf("%s: %v", name, problemKeys(probs))
		}
	}
}

func TestHelpNormalizeRefusesBadEntries(t *testing.T) {
	over := func(n int) string { return strings.Repeat("a", n) }
	tooMany := make([]store.HelpButton, helpcontent.MaxButtons+1)
	for i := range tooMany {
		tooMany[i] = goButton("https://example.com")
	}
	withButton := func(b store.HelpButton) HelpInput { return tutorialInput("t", "b", b) }
	for name, c := range map[string]struct {
		in   HelpInput
		want []string
	}{
		"an unknown kind": {HelpInput{Kind: "news", Title: map[string]string{"en": "t"}, Body: map[string]string{"en": "b"}}, []string{"kind unknown_kind news"}},
		"nothing written": {HelpInput{Kind: "faq"}, []string{"need_one_language"}},
		"only spaces":     {HelpInput{Kind: "faq", Title: map[string]string{"en": "  "}, Body: map[string]string{"en": " \n "}}, []string{"need_one_language"}},
		"a title without a text": {HelpInput{Kind: "faq", Title: map[string]string{"fa": "س", "en": "q"}, Body: map[string]string{"en": "a"}},
			[]string{"body.fa pair_missing body"}},
		"a text without a title": {HelpInput{Kind: "faq", Title: map[string]string{"en": "q"}, Body: map[string]string{"fa": "پ", "en": "a"}},
			[]string{"title.fa pair_missing title"}},
		"a title and a text in two languages": {HelpInput{Kind: "faq", Title: map[string]string{"fa": "س"}, Body: map[string]string{"en": "a"}},
			[]string{"body.fa pair_missing body", "title.en pair_missing title"}},
		"an unknown language": {HelpInput{Kind: "faq", Title: map[string]string{"en": "q", "de": "f"}, Body: map[string]string{"en": "a", "de": "a"}},
			[]string{"title.de lang_unused de", "body.de lang_unused de"}},
		"a question too long": {HelpInput{Kind: "faq", Title: map[string]string{"en": over(helpcontent.MaxQuestion + 1)}, Body: map[string]string{"en": "a"}},
			[]string{"title.en too_long 200"}},
		"a title too long":         {tutorialInput(over(helpcontent.MaxTutorialTitle+1), "b"), []string{"title.en too_long 64"}},
		"a text too long":          {tutorialInput("t", over(helpcontent.MaxBody+1)), []string{"body.en too_long 3000"}},
		"emoji count as two":       {tutorialInput("t", strings.Repeat("😀", helpcontent.MaxBody/2+1)), []string{"body.en too_long 3000"}},
		"a control character":      {tutorialInput("t\x07", "b"), []string{"title.en control_char '\\a'"}},
		"a script tag":             {tutorialInput("t", "<script>alert(1)</script>"), []string{"body.en tag_not_allowed script"}},
		"an unclosed tag":          {tutorialInput("t", "<b>bold"), []string{"body.en tag_unclosed b"}},
		"a bare ampersand":         {tutorialInput("t", "this & that"), []string{"body.en bad_entity &"}},
		"an http link":             {tutorialInput("t", `<a href="http://example.com">x</a>`), []string{"body.en bad_link http://example.com"}},
		"a tg link":                {tutorialInput("t", `<a href="tg://user?id=1">x</a>`), []string{"body.en bad_link tg://user?id=1"}},
		"a link without a dot":     {tutorialInput("t", `<a href="https://intranet/x">x</a>`), []string{"body.en bad_link https://intranet/x"}},
		"a link with an attribute": {tutorialInput("t", `<a href="https://example.com" title="x">x</a>`), []string{"body.en bad_attr a"}},
		"buttons on a question": {HelpInput{Kind: "faq", Title: map[string]string{"en": "q"}, Body: map[string]string{"en": "a"}, Buttons: []store.HelpButton{goButton("https://example.com")}},
			[]string{"buttons buttons_not_allowed"}},
		"nine buttons":             {tutorialInput("t", "b", tooMany...), []string{"buttons too_many_buttons 8"}},
		"a button without a link":  {withButton(store.HelpButton{Label: map[string]string{"en": "Go"}}), []string{"buttons.0.url url_missing"}},
		"a button without a label": {withButton(store.HelpButton{URL: "https://example.com"}), []string{"buttons.0.label.fa label_missing"}},
		"a label too long":         {withButton(store.HelpButton{URL: "https://example.com", Label: map[string]string{"en": over(65)}}), []string{"buttons.0.label.en too_long 64"}},
		"a label in an unknown language": {withButton(store.HelpButton{URL: "https://example.com", Label: map[string]string{"en": "Go", "de": "Los"}}),
			[]string{"buttons.0.label.de lang_unused de"}},
		"an http button":                      {withButton(goButton("http://example.com")), []string{"buttons.0.url bad_url http://example.com"}},
		"a javascript button":                 {withButton(goButton("javascript:alert(1)")), []string{"buttons.0.url bad_url javascript:alert(1)"}},
		"a button on localhost":               {withButton(goButton("https://localhost/")), []string{"buttons.0.url bad_url https://localhost/"}},
		"a button with a space":               {withButton(goButton("https://exa mple.com/")), []string{"buttons.0.url bad_url https://exa mple.com/"}},
		"a button with a password":            {withButton(goButton("https://user:pw@example.com/")), []string{"buttons.0.url bad_url https://user:pw@example.com/"}},
		"a button with letters outside ASCII": {withButton(goButton("https://example.com/آموزش")), []string{"buttons.0.url bad_url https://example.com/آموزش"}},
		"a button address too long":           {withButton(goButton("https://example.com/" + over(helpcontent.MaxURL))), []string{"buttons.0.url bad_url https://example.com/" + over(helpcontent.MaxURL)}},
		"a second button that is wrong": {tutorialInput("t", "b", goButton("https://example.com"), goButton("ftp://example.com")),
			[]string{"buttons.1.url bad_url ftp://example.com"}},
	} {
		_, probs := NormalizeHelp(c.in)
		got, want := problemKeys(probs), slices.Clone(c.want)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
}

func TestHelpNormalizeTidiesWhatItKeeps(t *testing.T) {
	in := HelpInput{Kind: "faq", Enabled: true,
		Title: map[string]string{"fa": "  آیا\tاین\nدرست   است؟ ", "en": ""},
		Body:  map[string]string{"fa": "\r\n  خط اول\r\nخط دوم  \r\n", "en": "   "}}
	out, probs := NormalizeHelp(in)
	if len(probs) > 0 {
		t.Fatal(problemKeys(probs))
	}
	if out.Title["fa"] != "آیا این درست است؟" || out.Body["fa"] != "خط اول\nخط دوم" {
		t.Fatalf("tidied: %q %q", out.Title["fa"], out.Body["fa"])
	}
	if _, has := out.Title["en"]; has || len(out.Body) != 1 || !out.Enabled || out.Buttons == nil {
		t.Fatalf("an empty language is not kept: %+v", out)
	}
	// The half-space of Persian stays, and a button's address is trimmed.
	zw, _ := NormalizeHelp(tutorialInput("می\u200cخواهم", "b", goButton("  https://example.com/x  ")))
	if zw.Title["en"] != "می\u200cخواهم" || zw.Buttons[0].URL != "https://example.com/x" {
		t.Fatalf("ZWNJ or address: %+v", zw)
	}
}

// The error the dashboard gets carries every problem with its field, and is an
// invalid argument.
func TestHelpErrorListsTheProblems(t *testing.T) {
	_, probs := NormalizeHelp(HelpInput{Kind: "faq"})
	err := error(&HelpError{probs})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "need_one_language") {
		t.Fatalf("%v", err)
	}
	_, probs = NormalizeHelp(tutorialInput(strings.Repeat("t", 70), "<i>x"))
	if msg := (&HelpError{probs}).Error(); !strings.Contains(msg, "title.en: too_long (64)") || !strings.Contains(msg, "body.en: tag_unclosed (i)") {
		t.Fatalf("%v", msg)
	}
}

// --- the defaults ---

func TestHelpDefaultsAreValidAndComplete(t *testing.T) {
	faq, tutorials := DefaultHelp(helpcontent.KindFAQ), DefaultHelp(helpcontent.KindTutorial)
	if len(faq) < 6 || len(tutorials) < 6 {
		t.Fatalf("%d questions, %d tutorials", len(faq), len(tutorials))
	}
	for _, in := range append(slices.Clone(faq), tutorials...) {
		out, probs := NormalizeHelp(in)
		if len(probs) > 0 {
			t.Errorf("%q: %v", in.Title["en"], problemKeys(probs))
			continue
		}
		// Both languages, written so that the owner can edit them: nothing is lost by tidying.
		for _, l := range i18n.Languages {
			if out.Title[l] == "" || out.Body[l] == "" || out.Title[l] != in.Title[l] || out.Body[l] != in.Body[l] {
				t.Errorf("%q in %s: %q / %q", in.Title["en"], l, out.Title[l], out.Body[l])
			}
		}
		if !in.Enabled {
			t.Errorf("%q is off by default", in.Title["en"])
		}
	}
	// The generic questions cover what the owner listed.
	covers := func(words ...string) bool {
		for _, in := range faq {
			text := strings.ToLower(in.Title["en"] + " " + in.Body["en"])
			ok := true
			for _, w := range words {
				ok = ok && strings.Contains(text, w)
			}
			if ok {
				return true
			}
		}
		return false
	}
	for _, topic := range [][]string{{"ip address"}, {"renew"}, {"devices"}, {"traffic", "left"}, {"does not connect"}, {"money back"}} {
		if !covers(topic...) {
			t.Errorf("no default question about %v", topic)
		}
	}
	// No promise a store cannot make.
	for _, in := range faq {
		for _, l := range i18n.Languages {
			text := strings.ToLower(in.Body[l])
			for _, promise := range []string{"guarantee", "always connect", "100%", "تضمین"} {
				if strings.Contains(text, promise) {
					t.Errorf("%q promises %q", in.Title["en"], promise)
				}
			}
		}
	}
	// The tutorials: how to use the link, then the apps the subscription page
	// knows and Happ; each app has download buttons that are https.
	if !strings.Contains(strings.ToLower(tutorials[0].Title["en"]), "subscription link") || len(tutorials[0].Buttons) != 0 {
		t.Errorf("the first tutorial: %+v", tutorials[0])
	}
	for _, app := range []string{"v2rayNG", "Hiddify", "Streisand", "V2Box", "HAPP"} {
		found := false
		for _, in := range tutorials {
			if strings.Contains(in.Title["en"], app) && strings.Contains(in.Title["fa"], app) {
				found = len(in.Buttons) > 0
			}
		}
		if !found {
			t.Errorf("no tutorial with download buttons for %s", app)
		}
	}
	// Persian digits in the Persian steps, Latin ones in the English.
	if !strings.Contains(tutorials[0].Body["fa"], "<b>۱.</b>") || !strings.Contains(tutorials[0].Body["en"], "<b>1.</b>") {
		t.Errorf("the steps are numbered in the wrong digits: %q / %q", tutorials[0].Body["fa"], tutorials[0].Body["en"])
	}
}

// --- storing, reading and auditing ---

// helpService is a service on a database without help entries, and the user who
// writes them.
func helpService(t *testing.T) (*Service, *store.Store, *store.User) {
	t.Helper()
	svc, s := testService(t)
	if err := testdb.Truncate(context.Background(), s.DB(), `core.help_entries`); err != nil {
		t.Fatal(err)
	}
	return svc, s, seedUser(t, s, 5001)
}

func helpAuditRows(t *testing.T, s *store.Store, action string) (before, after []string) {
	t.Helper()
	rows, err := s.DB().Query(context.Background(),
		`SELECT coalesce(before::text, ''), coalesce(after::text, '') FROM core.audit_log WHERE action = $1 ORDER BY created_at, id`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var b, a string
		if err := rows.Scan(&b, &a); err != nil {
			t.Fatal(err)
		}
		before, after = append(before, b), append(after, a)
	}
	return before, after
}

func TestHelpCreateUpdateDeleteAreAudited(t *testing.T) {
	svc, s, owner := helpService(t)
	ctx := context.Background()

	e, err := svc.CreateHelp(ctx, owner, faqInput("۱", "1"))
	if err != nil || e.ID == "" || e.Sort != 1 || e.Version != 1 || !e.Enabled || e.Title["en"] != "Question 1" {
		t.Fatalf("create: %+v %v", e, err)
	}
	if before, after := helpAuditRows(t, s, "help.create"); len(after) != 1 || before[0] != "" || !strings.Contains(after[0], "Question 1") || !strings.Contains(after[0], `"kind": "faq"`) {
		t.Fatalf("create audit: %v %v", before, after)
	}
	var actor *string
	if err := s.DB().QueryRow(ctx, `SELECT actor_id::text FROM core.audit_log WHERE action = 'help.create'`).Scan(&actor); err != nil || actor == nil || *actor != owner.ID {
		t.Fatalf("the audit names the actor: %v %v", actor, err)
	}

	// A change keeps the place and the kind, raises the version and keeps what was there.
	in := faqInput("۱", "1")
	in.Body["en"] = "A better answer"
	got, err := svc.UpdateHelp(ctx, owner, e.ID, in, 1)
	if err != nil || got.Version != 2 || got.Sort != 1 || got.Body["en"] != "A better answer" || got.Kind != "faq" {
		t.Fatalf("update: %+v %v", got, err)
	}
	before, after := helpAuditRows(t, s, "help.update")
	if len(after) != 1 || !strings.Contains(before[0], "Answer 1") || !strings.Contains(after[0], "A better answer") {
		t.Fatalf("update audit: %v %v", before, after)
	}

	// The same content again is no change: no version, no audit entry.
	again, err := svc.UpdateHelp(ctx, owner, e.ID, in, 2)
	if err != nil || again.Version != 2 || auditCount(t, s, "help.update") != 1 {
		t.Fatalf("saving the same: %+v %v, %d audits", again, err, auditCount(t, s, "help.update"))
	}
	// An edit from an older copy is refused as a conflict and changes nothing.
	if _, err := svc.UpdateHelp(ctx, owner, e.ID, faqInput("", "other"), 1); !errors.Is(err, ErrState) {
		t.Fatalf("stale edit: %v", err)
	}
	if cur, _ := s.GetHelp(ctx, s.Conn(), e.ID, false); cur.Title["en"] != "Question 1" || cur.Version != 2 {
		t.Fatalf("a refused edit changed the entry: %+v", cur)
	}
	// A kind cannot change; unknown entries are not found; bad content is a list of problems.
	if _, err := svc.UpdateHelp(ctx, owner, e.ID, tutorialInput("t", "b"), 2); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a question made a tutorial: %v", err)
	}
	if _, err := svc.UpdateHelp(ctx, owner, "00000000-0000-7000-8000-00000000dead", in, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown entry: %v", err)
	}
	var he *HelpError
	if _, err := svc.UpdateHelp(ctx, owner, e.ID, HelpInput{Title: map[string]string{"en": "x"}}, 2); !errors.As(err, &he) {
		t.Fatalf("bad content: %v", err)
	}
	if _, err := svc.CreateHelp(ctx, owner, HelpInput{Kind: "faq"}); !errors.As(err, &he) || len(he.Problems) != 1 {
		t.Fatalf("create with nothing: %v", err)
	}
	if auditCount(t, s, "help.create") != 1 {
		t.Fatal("a refused create was audited")
	}

	// Deleting keeps what was deleted in the audit log.
	if err := svc.DeleteHelp(ctx, owner, e.ID); err != nil {
		t.Fatal(err)
	}
	if before, after := helpAuditRows(t, s, "help.delete"); len(before) != 1 || !strings.Contains(before[0], "A better answer") || after[0] != "" {
		t.Fatalf("delete audit: %v %v", before, after)
	}
	if err := svc.DeleteHelp(ctx, owner, e.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestHelpSwitchOnAndOff(t *testing.T) {
	svc, s, owner := helpService(t)
	ctx := context.Background()
	e, _ := svc.CreateHelp(ctx, owner, faqInput("", "1"))
	off, err := svc.SetHelpEnabled(ctx, owner, e.ID, false, 1)
	if err != nil || off.Enabled || off.Version != 2 {
		t.Fatalf("off: %+v %v", off, err)
	}
	if again, err := svc.SetHelpEnabled(ctx, owner, e.ID, false, 2); err != nil || again.Version != 2 || auditCount(t, s, "help.disable") != 1 {
		t.Fatalf("off again: %+v %v", again, err)
	}
	if _, err := svc.SetHelpEnabled(ctx, owner, e.ID, true, 1); !errors.Is(err, ErrState) {
		t.Fatalf("from a stale copy: %v", err)
	}
	on, err := svc.SetHelpEnabled(ctx, owner, e.ID, true, 2)
	if err != nil || !on.Enabled || on.Version != 3 || auditCount(t, s, "help.enable") != 1 {
		t.Fatalf("on: %+v %v", on, err)
	}
	if _, err := svc.SetHelpEnabled(ctx, owner, "00000000-0000-7000-8000-00000000dead", true, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	// What was written stays when the entry is switched off and on.
	if cur, _ := s.GetHelp(ctx, s.Conn(), e.ID, false); cur.Title["en"] != "Question 1" {
		t.Fatalf("%+v", cur)
	}
}

func TestHelpLimitsHowManyEntriesAStoreKeeps(t *testing.T) {
	svc, s, owner := helpService(t)
	ctx := context.Background()
	for i := 0; i < helpcontent.MaxFAQ; i++ {
		if err := s.InsertHelp(ctx, s.Conn(), &store.HelpEntry{Kind: "faq", Title: map[string]string{"en": fmt.Sprint(i)}, Body: map[string]string{"en": "a"}, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.CreateHelp(ctx, owner, faqInput("", "x")); !errors.Is(err, ErrState) {
		t.Fatalf("the 51st question: %v", err)
	}
	if _, err := svc.CreateHelp(ctx, owner, tutorialInput("t", "b")); err != nil { // another kind has room
		t.Fatalf("a tutorial among 50 questions: %v", err)
	}
}

func TestHelpReorderIsAuditedAndGuarded(t *testing.T) {
	svc, s, owner := helpService(t)
	ctx := context.Background()
	var ids []string
	for _, q := range []string{"a", "b", "c"} {
		e, err := svc.CreateHelp(ctx, owner, faqInput("", q))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	tut, _ := svc.CreateHelp(ctx, owner, tutorialInput("t", "b"))
	order := func() []string {
		list, _ := s.ListHelp(ctx, s.Conn(), "faq", false)
		return helpIDsOf(list)
	}
	if err := svc.ReorderHelp(ctx, owner, "faq", []string{ids[2], ids[0], ids[1]}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order(), []string{ids[2], ids[0], ids[1]}) {
		t.Fatalf("order: %v", order())
	}
	before, after := helpAuditRows(t, s, "help.reorder")
	if len(after) != 1 || !strings.Contains(before[0], ids[0]) || !strings.Contains(after[0], ids[2]) {
		t.Fatalf("audit: %v %v", before, after)
	}
	// The order a customer sees, and the content version, are what it should be.
	if cur, _ := s.GetHelp(ctx, s.Conn(), ids[0], false); cur.Version != 1 {
		t.Fatalf("reordering counts as a change of content: %+v", cur)
	}
	// The same order again is nothing; wrong lists are refused and leave the order.
	if err := svc.ReorderHelp(ctx, owner, "faq", order()); err != nil || auditCount(t, s, "help.reorder") != 1 {
		t.Fatalf("same order: %v, %d audits", err, auditCount(t, s, "help.reorder"))
	}
	if err := svc.ReorderHelp(ctx, owner, "faq", []string{ids[0], ids[1]}); !errors.Is(err, ErrState) {
		t.Fatalf("an entry left out: %v", err)
	}
	if err := svc.ReorderHelp(ctx, owner, "faq", []string{ids[0], ids[0], ids[1]}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an entry twice: %v", err)
	}
	if err := svc.ReorderHelp(ctx, owner, "faq", append(order(), tut.ID)); !errors.Is(err, ErrState) {
		t.Fatalf("a tutorial among the questions: %v", err)
	}
	if err := svc.ReorderHelp(ctx, owner, "news", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind: %v", err)
	}
	if !slices.Equal(order(), []string{ids[2], ids[0], ids[1]}) {
		t.Fatalf("a refused order changed the list: %v", order())
	}
}

func helpIDsOf(list []store.HelpEntry) []string {
	out := []string{}
	for _, e := range list {
		out = append(out, e.ID)
	}
	return out
}

// --- what customers read ---

func TestHelpResolveFallsBackToTheOtherLanguage(t *testing.T) {
	e := &store.HelpEntry{ID: "x", Kind: "tutorial", Enabled: true,
		Title: map[string]string{"en": "English title", "fa": "عنوان"}, Body: map[string]string{"en": "English text", "fa": "متن"},
		Buttons: []store.HelpButton{
			{URL: "https://example.com/both", Label: map[string]string{"en": "Both", "fa": "هر دو"}},
			{URL: "https://example.com/en", Label: map[string]string{"en": "Only English"}},
			{URL: "https://example.com/fa", Label: map[string]string{"fa": "فقط فارسی"}},
			{URL: "https://example.com/nolabel", Label: map[string]string{}},
			{URL: "http://example.com/insecure", Label: map[string]string{"en": "Insecure"}},
		}}
	fa, ok := ResolveHelp(e, "fa")
	if !ok || fa.Title != "عنوان" || fa.Language != "fa" || len(fa.Buttons) != 3 || fa.Buttons[0].Label != "هر دو" ||
		fa.Buttons[1].Label != "Only English" || fa.Buttons[2].Label != "فقط فارسی" {
		t.Fatalf("fa: %+v", fa)
	}
	en, _ := ResolveHelp(e, "en")
	if en.Title != "English title" || en.Language != "en" || en.Buttons[2].Label != "فقط فارسی" || en.Buttons[0].URL != "https://example.com/both" {
		t.Fatalf("en: %+v", en)
	}
	// A translation missing in the language: the other one, whole (title and text together).
	onlyEN := &store.HelpEntry{Kind: "faq", Title: map[string]string{"en": "Q"}, Body: map[string]string{"en": "A"}}
	if v, ok := ResolveHelp(onlyEN, "fa"); !ok || v.Title != "Q" || v.Body != "A" || v.Language != "en" {
		t.Fatalf("fallback: %+v %v", v, ok)
	}
	// Half a pair is not a translation: the title of one language never goes with the text of the other.
	half := &store.HelpEntry{Kind: "faq", Title: map[string]string{"fa": "س", "en": "Q"}, Body: map[string]string{"en": "A"}}
	if v, _ := ResolveHelp(half, "fa"); v.Title != "Q" || v.Body != "A" {
		t.Fatalf("a mixed pair: %+v", v)
	}
	if _, ok := ResolveHelp(&store.HelpEntry{Kind: "faq", Title: map[string]string{"fa": "س"}, Body: map[string]string{"en": "A"}}, "fa"); ok {
		t.Fatal("an entry with no complete language was read")
	}
	// An unknown language is the default one.
	if v, _ := ResolveHelp(e, "de"); v.Language != "fa" {
		t.Fatalf("de: %+v", v)
	}
}

func helpCustomer(t *testing.T, s *store.Store, tg int64, lang string) *store.User {
	t.Helper()
	u, err := s.UpsertUser(context.Background(), s.Conn(), tg, "", lang, "")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestHelpForCustomers(t *testing.T) {
	svc, s, owner := helpService(t)
	ctx := context.Background()
	fa, en := helpCustomer(t, s, 5101, "fa"), helpCustomer(t, s, 5102, "en")
	mk := func(in HelpInput) *store.HelpEntry {
		t.Helper()
		e, err := svc.CreateHelp(ctx, owner, in)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	both, onlyEN, hidden := mk(faqInput("۱", "1")), mk(faqInput("", "2")), mk(faqInput("۳", "3"))
	if _, err := svc.SetHelpEnabled(ctx, owner, hidden.ID, false, 1); err != nil {
		t.Fatal(err)
	}
	tut := mk(tutorialInput("How", "Steps", goButton("https://example.com/app")))

	got, err := svc.HelpFor(ctx, fa.ID, "faq", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("a Persian customer's questions: %+v %v", got, err)
	}
	if got[0].ID != both.ID || got[0].Language != "fa" || got[0].Title != "سوال ۱" || got[1].ID != onlyEN.ID || got[1].Language != "en" || got[1].Title != "Question 2" {
		t.Fatalf("persian list: %+v", got)
	}
	got, _ = svc.HelpFor(ctx, en.ID, "faq", "")
	if len(got) != 2 || got[0].Language != "en" || got[0].Title != "Question 1" {
		t.Fatalf("an English customer's questions: %+v", got)
	}
	// The language asked for wins over the customer's own.
	if got, _ := svc.HelpFor(ctx, en.ID, "faq", "fa"); got[0].Title != "سوال ۱" {
		t.Fatalf("asked for Persian: %+v", got)
	}
	tuts, err := svc.HelpFor(ctx, fa.ID, "tutorial", "fa")
	if err != nil || len(tuts) != 1 || tuts[0].Title != "How" || len(tuts[0].Buttons) != 1 || tuts[0].Buttons[0].URL != "https://example.com/app" {
		t.Fatalf("tutorials: %+v %v", tuts, err)
	}

	// One entry: the same view; switched off or gone is not found.
	one, err := svc.HelpEntryFor(ctx, fa.ID, tut.ID, "")
	if err != nil || one.Title != "How" || one.Language != "en" {
		t.Fatalf("one: %+v %v", one, err)
	}
	for what, id := range map[string]string{"a switched-off entry": hidden.ID, "an unknown entry": "00000000-0000-7000-8000-00000000dead"} {
		if _, err := svc.HelpEntryFor(ctx, fa.ID, id, ""); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v", what, err)
		}
	}

	// Nothing enabled: an empty list, not an error.
	if err := svc.DeleteHelp(ctx, owner, tut.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.HelpFor(ctx, fa.ID, "tutorial", ""); err != nil || len(got) != 0 {
		t.Fatalf("no tutorials: %+v %v", got, err)
	}

	// The rest of the rules: the kind, who asks, and a banned customer as everywhere else.
	if _, err := svc.HelpFor(ctx, fa.ID, "news", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := svc.HelpFor(ctx, "not-a-uuid", "faq", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("not a uuid: %v", err)
	}
	if _, err := svc.HelpFor(ctx, "00000000-0000-7000-8000-00000000dead", "faq", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown customer: %v", err)
	}
	if err := s.SetUserStatus(ctx, s.Conn(), en.ID, "banned"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.HelpFor(ctx, en.ID, "faq", ""); !errors.Is(err, ErrUserBanned) {
		t.Fatalf("banned: %v", err)
	}
	if _, err := svc.HelpEntryFor(ctx, en.ID, both.ID, ""); !errors.Is(err, ErrUserBanned) {
		t.Fatalf("banned, one entry: %v", err)
	}
	if _, err := svc.HelpEntryFor(ctx, "x", both.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad ids: %v", err)
	}
}

func TestHelpAvailableFollowsWhatIsEnabled(t *testing.T) {
	svc, _, owner := helpService(t)
	ctx := context.Background()
	check := func(faq, tut bool) {
		t.Helper()
		f, tt, err := svc.HelpAvailable(ctx)
		if err != nil || f != faq || tt != tut {
			t.Fatalf("available: %v %v (%v), want %v %v", f, tt, err, faq, tut)
		}
	}
	check(false, false)
	q, _ := svc.CreateHelp(ctx, owner, faqInput("", "1"))
	check(true, false)
	t1, _ := svc.CreateHelp(ctx, owner, tutorialInput("t", "b"))
	check(true, true)
	_, _ = svc.SetHelpEnabled(ctx, owner, q.ID, false, 1)
	check(false, true)
	_ = svc.DeleteHelp(ctx, owner, t1.ID)
	check(false, false)
}

func TestHelpFAQLayout(t *testing.T) {
	entry := func(units int, kind string, enabled bool) store.HelpEntry {
		return store.HelpEntry{Kind: kind, Enabled: enabled, Title: map[string]string{"en": "q"}, Body: map[string]string{"en": strings.Repeat("a", units-1)}}
	}
	repeat := func(n, units int) []store.HelpEntry {
		out := make([]store.HelpEntry, n)
		for i := range out {
			out[i] = entry(units, "faq", true)
		}
		return out
	}
	for name, c := range map[string]struct {
		entries []store.HelpEntry
		want    string
	}{
		"nothing":               {nil, ""},
		"a switched-off one":    {[]store.HelpEntry{entry(10, "faq", false)}, ""},
		"only tutorials":        {[]store.HelpEntry{entry(10, "tutorial", true)}, ""},
		"one short question":    {repeat(1, 100), "message"},
		"twelve that fit":       {repeat(12, 250), "message"},
		"thirteen":              {repeat(13, 10), "list"},
		"at the length limit":   {repeat(3, 1000), "message"},
		"one unit over":         {append(repeat(2, 1000), entry(1001, "faq", true)), "list"},
		"one long answer alone": {repeat(1, 3001), "list"},
	} {
		if got := FAQLayout(c.entries, "en"); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	// A language with no translation reads the other one's text.
	fa := entry(100, "faq", true)
	if FAQLayout([]store.HelpEntry{fa}, "fa") != "message" {
		t.Error("fallback language not counted")
	}
	// Tags do not count, entities count as the character.
	tagged := store.HelpEntry{Kind: "faq", Enabled: true, Title: map[string]string{"en": "q"}, Body: map[string]string{"en": "<b>" + strings.Repeat("a", 2990) + "</b> &amp;"}}
	if FAQLayout([]store.HelpEntry{tagged}, "en") != "message" { // 1 + 2990 + 2 = 2993
		t.Error("tags counted")
	}
}

// --- the default content ---

func TestHelpDefaultsAreOfferedOnce(t *testing.T) {
	svc, s, owner := helpService(t)
	ctx := context.Background()
	if _, err := s.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = $1`, helpDefaultMark); err != nil {
		t.Fatal(err)
	}
	applied, err := svc.EnsureDefaultHelp(ctx)
	if err != nil || !applied {
		t.Fatalf("first start: %v %v", applied, err)
	}
	all, _ := svc.HelpEntries(ctx)
	faq, tut := 0, 0
	for _, e := range all {
		if !e.Enabled {
			t.Errorf("%q is off", e.Title["en"])
		}
		switch e.Kind {
		case "faq":
			faq++
		case "tutorial":
			tut++
		}
	}
	if faq != len(DefaultHelp("faq")) || tut != len(DefaultHelp("tutorial")) || faq == 0 || tut == 0 {
		t.Fatalf("%d questions, %d tutorials", faq, tut)
	}
	// In the order of the defaults, and as the system's own work.
	if all[0].Title["en"] != DefaultHelp("faq")[0].Title["en"] || all[0].Sort != 1 || all[faq].Sort != 1 {
		t.Fatalf("order: %+v", all[0])
	}
	var actor *string
	var source string
	if err := s.DB().QueryRow(ctx, `SELECT actor_id::text, source FROM core.audit_log WHERE action = 'help.defaults'`).Scan(&actor, &source); err != nil || actor != nil || source != "system" {
		t.Fatalf("audit: %v %v %v", actor, source, err)
	}
	// The default FAQ is one message, as in the screenshots that asked for it.
	for _, l := range i18n.Languages {
		if got := FAQLayout(all, l); got != "message" {
			t.Errorf("the default FAQ in %s is a %s", l, got)
		}
	}

	// A second start changes nothing, and an owner who deleted everything does not get it back.
	if applied, err := svc.EnsureDefaultHelp(ctx); err != nil || applied {
		t.Fatalf("second start: %v %v", applied, err)
	}
	for _, e := range all {
		if err := svc.DeleteHelp(ctx, owner, e.ID); err != nil {
			t.Fatal(err)
		}
	}
	if applied, err := svc.EnsureDefaultHelp(ctx); err != nil || applied {
		t.Fatalf("after the owner deleted them: %v %v", applied, err)
	}
	if left, _ := svc.HelpEntries(ctx); len(left) != 0 {
		t.Fatalf("they came back: %d", len(left))
	}
	if auditCount(t, s, "help.defaults") != 1 {
		t.Fatalf("%d audits of the defaults", auditCount(t, s, "help.defaults"))
	}
}

// A store that has help entries already (written before the marker was) is not
// given the defaults over them: it is only marked.
func TestHelpDefaultsLeaveExistingEntriesAlone(t *testing.T) {
	svc, s, owner := helpService(t)
	ctx := context.Background()
	if _, err := s.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = $1`, helpDefaultMark); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateHelp(ctx, owner, faqInput("", "mine")); err != nil {
		t.Fatal(err)
	}
	if applied, err := svc.EnsureDefaultHelp(ctx); err != nil || applied {
		t.Fatalf("%v %v", applied, err)
	}
	if all, _ := svc.HelpEntries(ctx); len(all) != 1 {
		t.Fatalf("%d entries", len(all))
	}
	var marked int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM core.settings WHERE key = $1`, helpDefaultMark).Scan(&marked); err != nil || marked != 1 {
		t.Fatalf("marker: %d %v", marked, err)
	}
	// The marker is state, not a setting.
	if _, ok := SpecOf(helpDefaultMark); ok {
		t.Fatal("the marker has a SettingSpec")
	}
}

func TestHelpAddDefaultsFillsOnlyAnEmptyList(t *testing.T) {
	svc, s, owner := helpService(t)
	ctx := context.Background()
	if _, err := svc.AddDefaultHelp(ctx, owner, "news"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind: %v", err)
	}
	added, err := svc.AddDefaultHelp(ctx, owner, "tutorial")
	if err != nil || len(added) != len(DefaultHelp("tutorial")) {
		t.Fatalf("tutorials: %d %v", len(added), err)
	}
	if faq, _ := s.ListHelp(ctx, s.Conn(), "faq", false); len(faq) != 0 {
		t.Fatal("the other list was filled too")
	}
	if _, err := svc.AddDefaultHelp(ctx, owner, "tutorial"); !errors.Is(err, ErrState) {
		t.Fatalf("again: %v", err)
	}
	if _, err := svc.CreateHelp(ctx, owner, faqInput("", "mine")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddDefaultHelp(ctx, owner, "faq"); !errors.Is(err, ErrState) {
		t.Fatalf("over an entry of mine: %v", err)
	}
	// It is the owner's act, with the owner named.
	var actor *string
	if err := s.DB().QueryRow(ctx, `SELECT actor_id::text FROM core.audit_log WHERE action = 'help.defaults'`).Scan(&actor); err != nil || actor == nil || *actor != owner.ID {
		t.Fatalf("audit: %v %v", actor, err)
	}
	var tuts []store.HelpEntry
	tuts, _ = s.ListHelp(ctx, s.Conn(), "tutorial", false)
	if tuts[0].UpdatedBy == nil || *tuts[0].UpdatedBy != owner.ID {
		t.Fatalf("updated_by: %+v", tuts[0].UpdatedBy)
	}
}

// What is stored is plain JSON in the columns the dashboard and the bot read.
func TestHelpStoredShape(t *testing.T) {
	svc, s, owner := helpService(t)
	ctx := context.Background()
	e, err := svc.CreateHelp(ctx, owner, tutorialInput("T", "B", store.HelpButton{URL: "https://example.com", Label: map[string]string{"en": "Go", "fa": "برو"}}))
	if err != nil {
		t.Fatal(err)
	}
	var title, buttons string
	if err := s.DB().QueryRow(ctx, `SELECT title::text, buttons::text FROM core.help_entries WHERE id = $1`, e.ID).Scan(&title, &buttons); err != nil {
		t.Fatal(err)
	}
	var tm map[string]string
	var bs []map[string]any
	if json.Unmarshal([]byte(title), &tm) != nil || json.Unmarshal([]byte(buttons), &bs) != nil || tm["en"] != "T" || len(bs) != 1 || bs[0]["url"] != "https://example.com" {
		t.Fatalf("stored: %s %s", title, buttons)
	}
}
