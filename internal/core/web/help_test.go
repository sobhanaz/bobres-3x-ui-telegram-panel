package web

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/helpcontent"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// helpFixture is the dashboard with a fake bot, an owner logged in, and no help
// entries (newFixture does not clear them between tests).
func helpFixture(t *testing.T) (*fixture, *browser, *fakeBot) {
	t.Helper()
	f, b, bot := setupFixture(t)
	if err := testdb.Truncate(context.Background(), f.st.DB(), `core.help_entries`); err != nil {
		t.Fatal(err)
	}
	return f, b, bot
}

// getHelp reads GET /api/v1/help: the entries of a kind, and the whole answer.
func getHelp(t *testing.T, b *browser, kind string) ([]map[string]any, map[string]any) {
	t.Helper()
	code, out := b.do(http.MethodGet, "/api/v1/help", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /help: %d %v", code, out)
	}
	raw, _ := out[kind].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		items = append(items, r.(map[string]any))
	}
	return items, out
}

func helpTitles(items []map[string]any, lang string) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it["title"].(map[string]any)[lang].(string))
	}
	return out
}

func helpFAQBody(question, answer string) map[string]any {
	return map[string]any{"kind": "faq", "title": map[string]string{"en": question, "fa": "پرسش " + question},
		"body": map[string]string{"en": answer, "fa": "پاسخ " + answer}}
}

func helpTutorialBody(title string, buttons ...map[string]any) map[string]any {
	if buttons == nil {
		buttons = []map[string]any{}
	}
	return map[string]any{"kind": "tutorial", "title": map[string]string{"en": title}, "body": map[string]string{"en": "How to " + title}, "buttons": buttons}
}

func helpButton(url, en string) map[string]any {
	return map[string]any{"url": url, "label": map[string]string{"en": en}}
}

// Everything under /help needs the Branding permission, as the bot's texts do:
// the owner and admins have it, support staff and the signed-out do not.
func TestHelpNeedsTheBrandingPermission(t *testing.T) {
	f, owner, _ := helpFixture(t)
	f.staffUser(7101, "admin")
	f.staffUser(7102, "support")
	anon, admin, support := f.browser(), f.browser(), f.browser()
	if code := admin.loginWithLink(7101); code != 200 {
		t.Fatalf("admin login: %d", code)
	}
	if code := support.loginWithLink(7102); code != 200 {
		t.Fatalf("support login: %d", code)
	}
	const id = "00000000-0000-7000-8000-000000000a01"
	calls := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/help", nil},
		{http.MethodPost, "/api/v1/help", helpFAQBody("q", "a")},
		{http.MethodPost, "/api/v1/help/check", helpFAQBody("q", "a")},
		{http.MethodPost, "/api/v1/help/order", map[string]any{"kind": "faq", "ids": []string{}}},
		{http.MethodPost, "/api/v1/help/defaults", map[string]any{"kind": "faq"}},
		{http.MethodPut, "/api/v1/help/" + id, helpFAQBody("q", "a")},
		{http.MethodPut, "/api/v1/help/" + id + "/enabled", map[string]any{"enabled": false, "version": 1}},
		{http.MethodDelete, "/api/v1/help/" + id, nil},
	}
	for _, c := range calls {
		if code, _ := anon.do(c.method, c.path, c.body); code != http.StatusUnauthorized {
			t.Errorf("signed out %s %s: %d", c.method, c.path, code)
		}
		if code, out := support.do(c.method, c.path, c.body); code != http.StatusForbidden || out["error"] != "forbidden" {
			t.Errorf("support %s %s: %d %v", c.method, c.path, code, out)
		}
	}
	if items, _ := getHelp(t, owner, "faq"); len(items) != 0 {
		t.Fatal("support staff or a signed-out client wrote an entry")
	}
	// Admins and the owner may; a write without the CSRF token is refused for both.
	for name, b := range map[string]*browser{"admin": admin, "owner": owner} {
		if code, out := b.do(http.MethodPost, "/api/v1/help", helpFAQBody(name, "a")); code != http.StatusOK {
			t.Errorf("%s create: %d %v", name, code, out)
		}
		csrf := b.csrf
		b.csrf = ""
		if code, out := b.do(http.MethodPost, "/api/v1/help", helpFAQBody(name+"2", "a")); code != http.StatusForbidden || out["error"] != "csrf" {
			t.Errorf("%s without CSRF: %d %v", name, code, out)
		}
		b.csrf = csrf
	}
	if items, _ := getHelp(t, owner, "faq"); len(items) != 2 {
		t.Fatalf("%d entries, want the two the admin and the owner wrote", len(items))
	}
}

func TestHelpListStartsEmptyWithTheLimits(t *testing.T) {
	_, b, _ := helpFixture(t)
	faq, out := getHelp(t, b, "faq")
	tut, _ := getHelp(t, b, "tutorials")
	if len(faq) != 0 || len(tut) != 0 {
		t.Fatalf("%v %v", faq, tut)
	}
	layout, _ := out["faq_layout"].(map[string]any)
	if layout["fa"] != "" || layout["en"] != "" {
		t.Fatalf("layout of nothing: %v", layout)
	}
	limits, _ := out["limits"].(map[string]any)
	want := map[string]float64{"max_faq": helpcontent.MaxFAQ, "max_tutorials": helpcontent.MaxTutorials, "question": helpcontent.MaxQuestion,
		"tutorial_title": helpcontent.MaxTutorialTitle, "body": helpcontent.MaxBody, "buttons": helpcontent.MaxButtons,
		"button_label": helpcontent.MaxButtonLabel, "url": helpcontent.MaxURL, "one_message_entries": helpcontent.OneMessageMaxEntries,
		"one_message_units": helpcontent.OneMessageMaxUnits}
	for k, v := range want {
		if got, _ := limits[k].(float64); got != v {
			t.Errorf("limit %s: %v, want %v", k, limits[k], v)
		}
	}
	if langs := asJSON(out["languages"]); langs != `["fa","en"]` {
		t.Fatalf("languages: %s", langs)
	}
}

func waitBot(t *testing.T, bot *fakeBot, what string, n int) {
	t.Helper()
	waitFor(t, what, func() bool { return bot.refreshCount() >= n })
}

func TestHelpCreateEditSwitchOrderAndDelete(t *testing.T) {
	f, b, bot := helpFixture(t)

	// Create: the entry comes back whole, both languages, at the end of its list.
	code, out := b.do(http.MethodPost, "/api/v1/help", helpFAQBody("first", "one"))
	item, _ := out["item"].(map[string]any)
	if code != http.StatusOK || item["kind"] != "faq" || item["sort"] != float64(1) || item["version"] != float64(1) || item["enabled"] != true {
		t.Fatalf("create: %d %v", code, out)
	}
	waitBot(t, bot, "the bot told about the first question", 1)
	id1 := item["id"].(string)
	tut := helpTutorialBody("Install", helpButton("https://example.com/app", "Download"))
	code, out = b.do(http.MethodPost, "/api/v1/help", tut)
	tutItem, _ := out["item"].(map[string]any)
	if code != http.StatusOK || tutItem["kind"] != "tutorial" || len(tutItem["buttons"].([]any)) != 1 {
		t.Fatalf("create tutorial: %d %v", code, out)
	}
	// Both languages are always there for the editor, empty when not written.
	if title := tutItem["title"].(map[string]any); title["en"] != "Install" || title["fa"] != "" {
		t.Fatalf("title: %v", title)
	}
	if label := tutItem["buttons"].([]any)[0].(map[string]any)["label"].(map[string]any); label["en"] != "Download" || label["fa"] != "" {
		t.Fatalf("label: %v", label)
	}
	for _, q := range []string{"second", "third"} {
		if code, _ := b.do(http.MethodPost, "/api/v1/help", helpFAQBody(q, q)); code != http.StatusOK {
			t.Fatalf("create %s: %d", q, code)
		}
	}
	faq, page := getHelp(t, b, "faq")
	if got := helpTitles(faq, "en"); strings.Join(got, ",") != "first,second,third" {
		t.Fatalf("the questions: %v", got)
	}
	if layout := page["faq_layout"].(map[string]any); layout["en"] != "message" || layout["fa"] != "message" {
		t.Fatalf("layout: %v", layout)
	}

	// Edit with the version read: the content changes, the place stays, the version moves on.
	body := helpFAQBody("first, edited", "one, edited")
	body["version"] = 1
	code, out = b.do(http.MethodPut, "/api/v1/help/"+id1, body)
	item, _ = out["item"].(map[string]any)
	if code != http.StatusOK || item["version"] != float64(2) || item["sort"] != float64(1) || item["title"].(map[string]any)["en"] != "first, edited" {
		t.Fatalf("edit: %d %v", code, out)
	}
	// From the old copy: a conflict, and nothing changes.
	body["title"] = map[string]string{"en": "lost", "fa": "گم"}
	if code, out = b.do(http.MethodPut, "/api/v1/help/"+id1, body); code != http.StatusConflict || out["error"] != "state" {
		t.Fatalf("stale edit: %d %v", code, out)
	}
	// Bad content: 400 with each problem and its field.
	bad := map[string]any{"kind": "faq", "version": 2, "title": map[string]string{"en": "q"}, "body": map[string]string{"en": "<b>open", "fa": "پاسخ"}}
	code, out = b.do(http.MethodPut, "/api/v1/help/"+id1, bad)
	if code != http.StatusBadRequest || out["error"] != "invalid" {
		t.Fatalf("bad edit: %d %v", code, out)
	}
	probs := asJSON(out["problems"])
	for _, want := range []string{`"arg":"b","code":"tag_unclosed","field":"body.en"`, `"arg":"title","code":"pair_missing","field":"title.fa"`} {
		if !strings.Contains(probs, want) {
			t.Errorf("problems %s lack %s", probs, want)
		}
	}
	// Unknown and malformed ids.
	if code, _ := b.do(http.MethodPut, "/api/v1/help/00000000-0000-7000-8000-00000000dead", body); code != http.StatusNotFound {
		t.Fatalf("unknown id: %d", code)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/help/not-an-id", body); code != http.StatusNotFound {
		t.Fatalf("malformed id: %d", code)
	}
	// An unknown field is refused as the rest of the API refuses it.
	if code, _ := b.do(http.MethodPost, "/api/v1/help", map[string]any{"kind": "faq", "whatever": 1}); code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", code)
	}

	// Switch off and on.
	code, out = b.do(http.MethodPut, "/api/v1/help/"+id1+"/enabled", map[string]any{"enabled": false, "version": 2})
	item, _ = out["item"].(map[string]any)
	if code != http.StatusOK || item["enabled"] != false || item["version"] != float64(3) {
		t.Fatalf("off: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPut, "/api/v1/help/"+id1+"/enabled", map[string]any{"enabled": true, "version": 2}); code != http.StatusConflict {
		t.Fatalf("on from an old copy: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodPut, "/api/v1/help/"+id1+"/enabled", map[string]any{"enabled": true, "version": 3}); code != http.StatusOK {
		t.Fatalf("on: %d", code)
	}

	// Order: the lists come back in the new order, and a list that is not the
	// entries of the kind is a conflict.
	faq, _ = getHelp(t, b, "faq")
	ids := []string{faq[2]["id"].(string), faq[0]["id"].(string), faq[1]["id"].(string)}
	code, out = b.do(http.MethodPost, "/api/v1/help/order", map[string]any{"kind": "faq", "ids": ids})
	if code != http.StatusOK {
		t.Fatalf("order: %d %v", code, out)
	}
	faq, _ = getHelp(t, b, "faq")
	if got := helpTitles(faq, "en"); strings.Join(got, "|") != "third|first, edited|second" {
		t.Fatalf("after the order: %v", got)
	}
	if code, out = b.do(http.MethodPost, "/api/v1/help/order", map[string]any{"kind": "faq", "ids": ids[:2]}); code != http.StatusConflict {
		t.Fatalf("a short list: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodPost, "/api/v1/help/order", map[string]any{"kind": "faq", "ids": []string{ids[0], ids[0], ids[1]}}); code != http.StatusBadRequest {
		t.Fatalf("a repeat: %d", code)
	}
	if code, _ = b.do(http.MethodPost, "/api/v1/help/order", map[string]any{"kind": "news", "ids": ids}); code != http.StatusBadRequest {
		t.Fatalf("a kind that does not exist: %d", code)
	}

	// Delete.
	if code, _ = b.do(http.MethodDelete, "/api/v1/help/"+id1, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	if code, _ = b.do(http.MethodDelete, "/api/v1/help/"+id1, nil); code != http.StatusNotFound {
		t.Fatalf("delete again: %d", code)
	}
	faq, _ = getHelp(t, b, "faq")
	if len(faq) != 2 {
		t.Fatalf("%d questions after the delete", len(faq))
	}

	// Every write told the bot (it hides its buttons while nothing is enabled),
	// and every one is in the audit log under the owner, from the dashboard.
	waitBot(t, bot, "the bot told about every change", 9)
	rows, err := f.st.DB().Query(context.Background(), `SELECT action, source, entity FROM core.audit_log WHERE action LIKE 'help.%' ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var seen []string
	for rows.Next() {
		var action, source, entity string
		if err := rows.Scan(&action, &source, &entity); err != nil {
			t.Fatal(err)
		}
		if source != "dashboard" || entity != "help_entry" {
			t.Errorf("%s: source %q entity %q", action, source, entity)
		}
		seen = append(seen, action)
	}
	want := "help.create,help.create,help.create,help.create,help.update,help.disable,help.enable,help.reorder,help.delete"
	if strings.Join(seen, ",") != want {
		t.Fatalf("audit: %v\nwant %s", seen, want)
	}
}

func TestHelpCheckSavesNothing(t *testing.T) {
	_, b, bot := helpFixture(t)
	before := bot.refreshCount()
	code, out := b.do(http.MethodPost, "/api/v1/help/check", helpFAQBody("q", "a"))
	if code != http.StatusOK || out["ok"] != true || len(out["problems"].([]any)) != 0 {
		t.Fatalf("a good entry: %d %v", code, out)
	}
	bad := helpTutorialBody("t", helpButton("http://example.com", "Go"), helpButton("https://example.com", ""))
	bad["body"] = map[string]string{"en": "<script>x</script>"}
	code, out = b.do(http.MethodPost, "/api/v1/help/check", bad)
	probs := asJSON(out["problems"])
	if code != http.StatusOK || out["ok"] != false {
		t.Fatalf("a bad entry: %d %v", code, out)
	}
	for _, want := range []string{`"arg":"script","code":"tag_not_allowed","field":"body.en"`, `"arg":"http://example.com","code":"bad_url","field":"buttons.0.url"`,
		`"arg":"","code":"label_missing","field":"buttons.1.label.fa"`} {
		if !strings.Contains(probs, want) {
			t.Errorf("problems %s lack %s", probs, want)
		}
	}
	if items, _ := getHelp(t, b, "faq"); len(items) != 0 {
		t.Fatal("a check saved an entry")
	}
	if tut, _ := getHelp(t, b, "tutorials"); len(tut) != 0 {
		t.Fatal("a check saved a tutorial")
	}
	if bot.refreshCount() != before {
		t.Fatal("a check told the bot")
	}
}

// The limits of the lists are enforced: a store keeps at most 50 questions.
func TestHelpListsAreLimited(t *testing.T) {
	f, b, _ := helpFixture(t)
	for i := 0; i < helpcontent.MaxFAQ; i++ {
		f.sql(`INSERT INTO core.help_entries (id, kind, title, body, sort) VALUES (gen_random_uuid(), 'faq', '{"en":"q"}', '{"en":"a"}', $1)`, i+1)
	}
	code, out := b.do(http.MethodPost, "/api/v1/help", helpFAQBody("one too many", "a"))
	if code != http.StatusConflict || out["error"] != "state" || !strings.Contains(asJSON(out["message"]), "50") {
		t.Fatalf("the 51st: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/help", helpTutorialBody("still room")); code != http.StatusOK {
		t.Fatalf("a tutorial: %d", code)
	}
}

func TestHelpDefaultsFillAnEmptyListOnly(t *testing.T) {
	_, b, bot := helpFixture(t)
	code, out := b.do(http.MethodPost, "/api/v1/help/defaults", map[string]any{"kind": "tutorial"})
	if code != http.StatusOK {
		t.Fatalf("defaults: %d %v", code, out)
	}
	tut, page := getHelp(t, b, "tutorials")
	if len(tut) != len(domain.DefaultHelp("tutorial")) || len(tut) == 0 {
		t.Fatalf("%d tutorials", len(tut))
	}
	if faq, _ := getHelp(t, b, "faq"); len(faq) != 0 {
		t.Fatal("the questions were filled too")
	}
	if layout := page["faq_layout"].(map[string]any); layout["en"] != "" {
		t.Fatalf("layout: %v", layout)
	}
	if code, out = b.do(http.MethodPost, "/api/v1/help/defaults", map[string]any{"kind": "tutorial"}); code != http.StatusConflict || out["error"] != "state" {
		t.Fatalf("over entries: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodPost, "/api/v1/help/defaults", map[string]any{"kind": "news"}); code != http.StatusBadRequest {
		t.Fatalf("unknown kind: %d", code)
	}
	if code, _ = b.do(http.MethodPost, "/api/v1/help/defaults", map[string]any{"kind": "faq"}); code != http.StatusOK {
		t.Fatalf("faq defaults: %d", code)
	}
	if _, page = getHelp(t, b, "faq"); page["faq_layout"].(map[string]any)["fa"] != "message" {
		t.Fatalf("the default FAQ is not one message: %v", page["faq_layout"])
	}
	waitBot(t, bot, "the bot told", 2)
}

// The default tutorials cover every app the subscription page has a button for:
// the page's list is read from the page, so an app added there is missed here
// until a tutorial for it is written.
func TestHelpDefaultTutorialsMatchTheSubscriptionPage(t *testing.T) {
	f := newPageFixture(t)
	code, _, body := f.page("/sub/" + f.token + "?l=en")
	if code != http.StatusOK {
		t.Fatalf("page: %d", code)
	}
	apps := regexp.MustCompile(`<summary>([^<]+)</summary>`).FindAllStringSubmatch(body, -1)
	if len(apps) < 4 {
		t.Fatalf("the page lists %d apps: %v", len(apps), apps)
	}
	tutorials := domain.DefaultHelp("tutorial")
	for _, m := range apps {
		name, found := m[1], false
		for _, in := range tutorials {
			if strings.Contains(in.Title["en"], name) && strings.Contains(in.Title["fa"], name) && len(in.Buttons) > 0 {
				found = true
			}
		}
		if !found {
			t.Errorf("the page has a button for %s, and the default tutorials have no download for it", name)
		}
	}
}
