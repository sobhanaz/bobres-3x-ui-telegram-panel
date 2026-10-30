package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The Announce dialog's API: broadcasts.write only; the draft (prefilled cards, settings, audience), the live check, the preview, the test
// to me with the Buy button, the channel check (none set, the bot may not post), the send with its refusals (the channel, the same product
// within 24 hours unless forced) and the list. The settings are on the Settings page.
func TestAnnouncementsAPI(t *testing.T) {
	f, b, bot := setupFixture(t)
	ctx := context.Background()
	plan := f.plan("Gold", 30, 50<<30, 150_000)
	hexID := strings.ReplaceAll(plan.ID, "-", "")
	yes := f.customer(8801, "yes")
	if _, err := f.dom.SetAnnounceOptIn(ctx, yes.ID, 0, true); err != nil {
		t.Fatal(err)
	}

	f.staffUser(8802, "support")
	sup := f.browser()
	sup.loginWithLink(8802)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/announcements"}, {http.MethodPost, "/api/v1/announcements"}, {http.MethodGet, "/api/v1/announcements/draft?kind=plan&id=" + plan.ID},
		{http.MethodPost, "/api/v1/announcements/preview"}, {http.MethodPost, "/api/v1/announcements/test"}, {http.MethodPost, "/api/v1/announcements/channel-check"},
	} {
		if code, _ := sup.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("support %s %s: %d", c.method, c.path, code)
		}
	}

	// The settings are saved from the Settings page; no quiet hours here, whatever time the test runs at.
	if code, out := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"announce.enabled": "true",
		"announce.quiet_start": "00:00", "announce.quiet_end": "00:00"}}); code != 200 {
		t.Fatalf("settings: %d %v", code, out)
	}
	code, out := b.do(http.MethodGet, "/api/v1/announcements/draft?kind=plan&id="+plan.ID, nil)
	if code != 200 || out["product"].(map[string]any)["start_param"] != "p_"+hexID || out["audience"].(map[string]any)["total"] != float64(1) ||
		!strings.Contains(out["texts"].(map[string]any)["en"].(string), "{traffic}") || out["settings"].(map[string]any)["enabled"] != true ||
		out["buy_labels"].(map[string]any)["en"] != "🛒 Buy" {
		t.Fatalf("draft: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/announcements/draft?kind=plan&id=00000000-0000-7000-8000-000000000009", nil); code != 400 {
		t.Fatalf("draft of an unknown plan: %d", code)
	}
	if _, out := b.do(http.MethodPost, "/api/v1/announcements/check", map[string]any{"lang": "en", "text": "{name} {prise}"}); out["ok"] != false ||
		!strings.Contains(asJSON(out["problems"]), `"arg":"{prise}"`) {
		t.Fatalf("check: %v", out)
	}
	texts := map[string]string{"fa": "🆕 {name}: {price}", "en": "🆕 {name}: {price}, {days}, {traffic}"}
	code, out = b.do(http.MethodPost, "/api/v1/announcements/preview", map[string]any{"kind": "plan", "id": plan.ID, "lang": "en", "texts": texts})
	if code != 200 || out["text"] != "🆕 Gold: 150,000 Toman, 30 days, 50 GB" || out["channel"] != "🆕 Gold: ۱۵۰٬۰۰۰ تومان" || out["channel_lang"] != "fa" {
		t.Fatalf("preview: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/announcements/test", map[string]any{"kind": "plan", "id": plan.ID, "lang": "fa", "texts": texts})
	if code != 200 || out["ok"] != true || len(bot.tests) != 1 || bot.tests[0].ChatID != ownerTG || bot.tests[0].Buy == nil ||
		bot.tests[0].Buy.Data != "an:b:p:"+hexID || bot.tests[0].Buy.Text != "🛒 خرید" || bot.tests[0].Text != "🆕 Gold: ۱۵۰٬۰۰۰ تومان" {
		t.Fatalf("test to me: %d %v %+v", code, out, bot.tests)
	}

	// The channel: none set, then one the bot administers but may not post in.
	body := map[string]any{"kind": "plan", "id": plan.ID, "texts": texts, "channel": true, "private": true}
	if code, out := b.do(http.MethodPost, "/api/v1/announcements/channel-check", map[string]any{}); code != 409 || out["problem"] != "no_channel" {
		t.Fatalf("no channel: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"announce.channel": "@mystore"}}); code != 200 {
		t.Fatalf("the channel: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/announcements/channel-check", map[string]any{}); code != 200 || out["ok"] != false || out["can_post"] != false ||
		out["chat"] != "@mystore" {
		t.Fatalf("may not post: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/announcements", body); code != 409 || out["error"] != "channel" || out["problem"] != "cannot_post" {
		t.Fatalf("sent while the bot may not post: %d %v", code, out)
	}
	bot.mu.Lock()
	bot.canPost = true
	bot.mu.Unlock()
	code, out = b.do(http.MethodPost, "/api/v1/announcements", body)
	if code != 200 || out["queued"] != false || out["audience"] != float64(1) || out["announcement"].(map[string]any)["channel_status"] != "pending" {
		t.Fatalf("sent: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/announcements", body); code != 409 || out["error"] != "repeat" || out["last_at"] == nil {
		t.Fatalf("again within 24 hours: %d %v", code, out)
	}
	body["force"] = true
	if code, out := b.do(http.MethodPost, "/api/v1/announcements", body); code != 200 || out["announcement"].(map[string]any)["forced"] != true {
		t.Fatalf("forced: %d %v", code, out)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/announcements?kind=plan&id="+plan.ID, nil); code != 200 || out["total"] != float64(2) {
		t.Fatalf("the list: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/announcements?kind=car", nil); code != 400 {
		t.Fatalf("an unknown kind: %d", code)
	}
}

// Product news in the Mini App: shown while announcements are on, with the customer's own answer, which they change there.
func TestMiniAppNews(t *testing.T) {
	m := newMiniFixture(t, true)
	tok := m.signIn("tg:8811:reader:en")
	if _, out := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil); out["features"].(map[string]any)["news"] != false {
		t.Fatalf("while off: %v", out["features"])
	}
	if code, _ := m.call(http.MethodPut, "/api/mini/v1/news", tok, map[string]any{"opt_in": true}); code != 404 {
		t.Fatalf("set while off: %d", code)
	}
	m.setting("announce.enabled", "true")
	if code, _ := m.call(http.MethodPut, "/api/mini/v1/news", tok, map[string]any{}); code != 400 {
		t.Fatalf("no answer: %d", code)
	}
	if code, out := m.call(http.MethodPut, "/api/mini/v1/news", tok, map[string]any{"opt_in": true}); code != 200 || out["opt_in"] != true {
		t.Fatalf("yes: %d %v", code, out)
	}
	_, out := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil)
	if f := out["features"].(map[string]any); f["news"] != true || f["news_optin"] != true {
		t.Fatalf("after yes: %v", f)
	}
	u, err := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 8811)
	if err != nil || u.AnnounceOptIn == nil || !*u.AnnounceOptIn || u.AnnounceAskedAt == nil {
		t.Fatalf("stored: %+v %v", u, err)
	}
	if code, out := m.call(http.MethodPut, "/api/mini/v1/news", tok, map[string]any{"opt_in": false}); code != 200 || out["opt_in"] != false {
		t.Fatalf("no: %d %v", code, out)
	}
}
