package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
)

// A change made in the dashboard is in the audit log with who, why and from
// where; the log filters, exports and stays closed to support.
func TestAuditLog(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	u := f.customer(8501, "hamid")
	if code, _ := b.do(http.MethodPost, "/api/v1/users/"+u.ID+"/balance",
		map[string]string{"amount": "1000", "currency": "IRT", "reason": "=cmd gift", "key": key()}); code != 200 {
		t.Fatalf("adjust: %d", code)
	}
	code, out := b.do(http.MethodGet, "/api/v1/audit?action=wallet", nil)
	list := items(out)
	if code != 200 || out["total"] != float64(1) || len(list) != 1 {
		t.Fatalf("audit by action group: %d %v", code, out)
	}
	e := list[0]
	if e["action"] != "wallet.adjust" || e["reason"] != "=cmd gift" || e["entity_id"] != u.ID || e["ip"] != "127.0.0.1" ||
		e["actor"].(map[string]any)["username"] != "u" || e["actor_role"] != "owner" {
		t.Fatalf("entry: %v", e)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/audit?entity_id="+u.ID, nil); out["total"] != float64(1) {
		t.Fatalf("by item: %v", out)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/audit?action=plan", nil); out["total"] != float64(0) {
		t.Fatalf("another group: %v", out)
	}
	code, out = b.do(http.MethodGet, "/api/v1/audit/actions", nil)
	if code != 200 || !strings.Contains(asJSON(out["items"]), `"action":"wallet.adjust"`) {
		t.Fatalf("actions: %d %v", code, out)
	}
	resp, err := b.c.Get(f.srv.URL + "/api/v1/audit.csv?action=wallet.adjust")
	if err != nil {
		t.Fatal(err)
	}
	csv, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	lines := strings.Split(strings.TrimSpace(string(csv)), "\n")
	if resp.StatusCode != 200 || len(lines) != 2 || !strings.Contains(lines[1], ",'=cmd gift,127.0.0.1,") {
		t.Fatalf("csv: %d %q", resp.StatusCode, csv)
	}

	f.staffUser(8502, "support")
	sup := f.browser()
	sup.loginWithLink(8502)
	if code, _ := sup.do(http.MethodGet, "/api/v1/audit", nil); code != 403 {
		t.Fatalf("support read the audit log: %d", code)
	}
}

// fakeBot stands in for the bot's internal endpoints.
type fakeBot struct {
	mu        sync.Mutex
	refreshes int
	checked   []string
	down      bool
}

func (b *fakeBot) RefreshSettings(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refreshes++
	return nil
}

func (b *fakeBot) CheckChannel(_ context.Context, chat string) (*botfiles.ChannelCheck, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.down {
		return nil, errors.New("bot down")
	}
	b.checked = append(b.checked, chat)
	return &botfiles.ChannelCheck{OK: true, Title: "News", BotAdmin: true}, nil
}

func (b *fakeBot) refreshCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refreshes
}

// setupFixture is salesFixture with a fake bot and no settings or files left
// over from other tests (settings are not truncated by newFixture).
func setupFixture(t *testing.T) (*fixture, *browser, *fakeBot) {
	t.Helper()
	f, b, _, _ := salesFixture(t)
	if _, err := f.st.DB().Exec(context.Background(), `TRUNCATE core.settings, core.assets`); err != nil {
		t.Fatal(err)
	}
	bot := &fakeBot{}
	f.api.cfg.Bot = bot
	return f, b, bot
}

// raw sends a body as it is (an upload).
func (b *browser) raw(method, path, ctype string, body []byte) (int, map[string]any) {
	b.f.t.Helper()
	req, _ := http.NewRequest(method, b.f.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	req.Header.Set(csrfHeader, b.csrf)
	resp, err := b.c.Do(req)
	if err != nil {
		b.f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func settingByKey(out map[string]any, key string) map[string]any {
	for _, it := range items(out) {
		if it["key"] == key {
			return it
		}
	}
	return nil
}

func lastAudit(t *testing.T, b *browser, query string) map[string]any {
	t.Helper()
	code, out := b.do(http.MethodGet, "/api/v1/audit?"+query, nil)
	list := items(out)
	if code != 200 || len(list) == 0 {
		t.Fatalf("audit %s: %d %v", query, code, out)
	}
	return list[0]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSettingsPage(t *testing.T) {
	f, b, bot := setupFixture(t)
	code, out := b.do(http.MethodGet, "/api/v1/settings", nil)
	if code != 200 || len(items(out)) == 0 || settingByKey(out, "branding.name") != nil || settingByKey(out, "referral.reward_percent") != nil {
		t.Fatalf("get: %d %v", code, out)
	}
	if it := settingByKey(out, "limits.bot_per_10s"); it["default"] != "20" || it["min"] != float64(5) || it["editable"] != true {
		t.Fatalf("limit item: %v", it)
	}

	// Values are normalised: Persian digits, yes for true.
	code, out = b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{
		"limits.bot_per_10s": "۳۰", "maintenance.enabled": "yes", "payments.stars_rate": "۱٬۵۰۰"}})
	if code != 200 || settingByKey(out, "limits.bot_per_10s")["value"] != "30" ||
		settingByKey(out, "maintenance.enabled")["value"] != "true" || settingByKey(out, "payments.stars_rate")["value"] != "1500" {
		t.Fatalf("put: %d %v", code, out)
	}
	waitFor(t, "the bot refresh", func() bool { return bot.refreshCount() == 1 })
	e := lastAudit(t, b, "entity_id=limits.bot_per_10s")
	if e["action"] != "setting.set" || asJSON(e["before"]) != `{"value":""}` || asJSON(e["after"]) != `{"value":"30"}` || e["source"] != "dashboard" {
		t.Fatalf("audit: %v", e)
	}

	// All or nothing, with the field named.
	code, out = b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{
		"limits.bot_per_10s": "40", "limits.tickets_per_day": "1000"}})
	if code != 400 || out["field"] != "limits.tickets_per_day" {
		t.Fatalf("bad value: %d %v", code, out)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/settings", nil); settingByKey(out, "limits.bot_per_10s")["value"] != "30" {
		t.Fatal("a refused save changed a value")
	}
	for k, v := range map[string]string{"join.channel": "news", "join.link": "https://evil.example/x", "general.timezone": "Mars/Base",
		"payments.usdt_trc20": "TXYZ", "payments.card_number": "1234", "branding.name": "x"} {
		if code, out := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{k: v}}); code != 400 || out["field"] != k {
			t.Errorf("%s=%s: %d %v", k, v, code, out)
		}
	}
	// A private channel needs its invite link.
	if code, out := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"join.channel": "-1001234567890"}}); code != 400 || out["field"] != "join.link" {
		t.Fatalf("private channel without link: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{
		"join.channel": "-1001234567890", "join.link": "https://t.me/+AbCdEf", "payments.card_number": "۶۰۳۷-۹۹۱۱-۲۲۲۲-۳۳۳۳"}}); code != 200 ||
		settingByKey(out, "payments.card_number")["value"] != "6037-9911-2222-3333" {
		t.Fatalf("private channel with link: %d %v", code, out)
	}

	// The channel check goes through the bot.
	if code, out := b.do(http.MethodPost, "/api/v1/settings/channel-check", map[string]string{"chat": "@my_news"}); code != 200 || out["ok"] != true || out["title"] != "News" {
		t.Fatalf("channel check: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/settings/channel-check", map[string]string{"chat": "not a channel"}); code != 400 {
		t.Fatalf("bad channel: %d", code)
	}
	bot.down = true
	if code, out := b.do(http.MethodPost, "/api/v1/settings/channel-check", map[string]string{"chat": "@my_news"}); code != 503 || out["error"] != "unavailable" {
		t.Fatalf("bot down: %d %v", code, out)
	}

	// Admins change settings but not where the money goes.
	f.staffUser(8601, "admin")
	adm := f.browser()
	adm.loginWithLink(8601)
	code, out = adm.do(http.MethodGet, "/api/v1/settings", nil)
	if code != 200 || settingByKey(out, "payments.card_number")["editable"] != false || settingByKey(out, "maintenance.enabled")["editable"] != true {
		t.Fatalf("admin view: %d %v", code, out)
	}
	if code, out := adm.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"payments.card_number": "6037991122223333"}}); code != 403 || out["field"] != "payments.card_number" {
		t.Fatalf("admin changed payment details: %d %v", code, out)
	}
	if code, _ := adm.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"maintenance.enabled": "false"}}); code != 200 {
		t.Fatalf("admin maintenance: %d", code)
	}
	f.staffUser(8602, "support")
	sup := f.browser()
	sup.loginWithLink(8602)
	for _, p := range []string{"/api/v1/settings", "/api/v1/branding", "/api/v1/texts"} {
		if code, _ := sup.do(http.MethodGet, p, nil); code != 403 {
			t.Errorf("support %s: %d", p, code)
		}
	}
}

func pngOf(w, h int) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)))
	return buf.Bytes()
}

func TestBrandingAndLogo(t *testing.T) {
	f, b, _ := setupFixture(t)
	code, out := b.do(http.MethodPut, "/api/v1/branding", map[string]any{"name": " My  VPN ", "support": "@help", "color": "#AA3366",
		"terms_url": "https://example.com/terms", "privacy_url": "", "currency": map[string]string{"fa": "تومن", "en": ""}})
	if code != 200 || out["name"] != "My VPN" || out["color"] != "#aa3366" || asJSON(out["currency"]) != `{"en":"","fa":"تومن"}` || out["logo"] != nil {
		t.Fatalf("put: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/branding", map[string]any{"name": "x", "support": "", "color": "teal",
		"terms_url": "", "privacy_url": "", "currency": map[string]string{"fa": "", "en": ""}}); code != 400 || out["field"] != "color" {
		t.Fatalf("bad colour: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/branding", map[string]any{"name": "x", "support": "", "color": "",
		"terms_url": "http://example.com", "privacy_url": "", "currency": map[string]string{"fa": "", "en": ""}}); code != 400 || out["field"] != "terms_url" {
		t.Fatalf("http link: %d %v", code, out)
	}

	// Logo: a real image only, not too big.
	if code, out := b.raw(http.MethodPut, "/api/v1/branding/logo", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>x</script></svg>`)); code != 415 {
		t.Fatalf("svg: %d %v", code, out)
	}
	if code, _ := b.raw(http.MethodPut, "/api/v1/branding/logo", "image/png", pngOf(8, 8)); code != 400 {
		t.Fatalf("tiny logo: %d", code)
	}
	if code, _ := b.raw(http.MethodPut, "/api/v1/branding/logo", "image/png", append(pngOf(64, 64), make([]byte, 600<<10)...)); code != 413 {
		t.Fatalf("big logo: %d", code)
	}
	code, out = b.raw(http.MethodPut, "/api/v1/branding/logo", "image/png", pngOf(64, 32))
	logo, _ := out["logo"].(map[string]any)
	if code != 200 || logo["type"] != "image/png" || !strings.HasPrefix(logo["url"].(string), "/api/v1/brand/logo?v=") {
		t.Fatalf("logo: %d %v", code, out)
	}

	// Public: the login page's brand and the image.
	anon := f.browser()
	code, out = anon.do(http.MethodGet, "/api/v1/brand", nil)
	if code != 200 || out["name"] != "My VPN" || out["color"] != "#aa3366" || out["logo"] != logo["url"] {
		t.Fatalf("public brand: %d %v", code, out)
	}
	resp, err := anon.c.Get(f.srv.URL + logo["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	img, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") ||
		!bytes.Equal(img, pngOf(64, 32)) {
		t.Fatalf("logo file: %d %v", resp.StatusCode, resp.Header)
	}
	_, me := b.do(http.MethodGet, "/api/v1/me", nil)
	if me["brand"] != "My VPN" || asJSON(me["branding"]) != `{"color":"#aa3366","currency":{"en":"","fa":"تومن"},"logo":"`+logo["url"].(string)+`"}` {
		t.Fatalf("me: %v", me)
	}
	if e := lastAudit(t, b, "action=branding.logo.set"); e["entity_id"] != "logo" {
		t.Fatalf("logo audit: %v", e)
	}
	if code, out := b.do(http.MethodDelete, "/api/v1/branding/logo", nil); code != 200 || out["logo"] != nil {
		t.Fatalf("remove logo: %d %v", code, out)
	}
	resp, err = anon.c.Get(f.srv.URL + "/api/v1/brand/logo")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("removed logo still served: %d", resp.StatusCode)
	}
}

func TestBotTexts(t *testing.T) {
	_, b, bot := setupFixture(t)
	code, out := b.do(http.MethodGet, "/api/v1/texts", nil)
	var welcome map[string]any
	for _, it := range items(out) {
		if it["key"] == "welcome" {
			welcome = it
		}
	}
	if code != 200 || len(items(out)) < 200 || welcome == nil || asJSON(welcome["placeholders"]) != `["brand"]` || welcome["context"] != "html" {
		t.Fatalf("list: %d %v", code, welcome)
	}

	// The editor's live check.
	code, out = b.do(http.MethodPost, "/api/v1/texts/check", map[string]string{"lang": "fa", "key": "welcome", "value": "<b>سلام"})
	if code != 200 || out["ok"] != false || !strings.Contains(asJSON(out["problems"]), `"arg":"brand","code":"placeholder_missing"`) ||
		!strings.Contains(asJSON(out["problems"]), `"code":"tag_unclosed"`) {
		t.Fatalf("check: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/texts/check", map[string]string{"lang": "fa", "key": "no.such", "value": "x"}); code != 404 {
		t.Fatalf("unknown key: %d", code)
	}

	// Saving refuses a text that would break the bot.
	code, out = b.do(http.MethodPut, "/api/v1/texts", map[string]string{"lang": "fa", "key": "menu.title", "value": "منو <b>{brand}"})
	if code != 400 || !strings.Contains(asJSON(out["problems"]), `"code":"tag_unclosed"`) {
		t.Fatalf("broken text saved: %d %v", code, out)
	}
	code, out = b.do(http.MethodPut, "/api/v1/texts", map[string]string{"lang": "fa", "key": "welcome", "value": "به <b>{brand}</b> خوش آمدید\r\n"})
	item, _ := out["item"].(map[string]any)
	if ov, _ := item["override"].(map[string]any); code != 200 || ov["fa"] != "به <b>{brand}</b> خوش آمدید" || ov["en"] != "" {
		t.Fatalf("save: %d %v", code, out)
	}
	waitFor(t, "the bot refresh", func() bool { return bot.refreshCount() >= 1 })
	e := lastAudit(t, b, "entity_id=fa.welcome")
	if e["action"] != "text.set" || e["entity"] != "text" {
		t.Fatalf("text audit: %v", e)
	}

	// Export, then an import that is all or nothing.
	resp, err := b.c.Get(b.f.srv.URL + "/api/v1/texts.json?lang=fa")
	if err != nil {
		t.Fatal(err)
	}
	var exp struct {
		Lang  string            `json:"lang"`
		Texts map[string]string `json:"texts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&exp)
	resp.Body.Close()
	if exp.Lang != "fa" || exp.Texts["welcome"] == "" || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("export: %+v", exp)
	}
	code, out = b.do(http.MethodPost, "/api/v1/texts/import", map[string]any{"lang": "fa", "texts": map[string]string{
		"btn.buy": "خرید", "welcome": "بدون نام", "no.such": "x"}})
	if code != 400 || !strings.Contains(asJSON(out["problems"]), `"key":"welcome"`) || !strings.Contains(asJSON(out["problems"]), `"key":"no.such"`) {
		t.Fatalf("bad import: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/texts/import", map[string]any{"lang": "fa", "texts": map[string]string{
		"btn.buy": "🛒 خرید", "welcome": ""}})
	if code != 200 || out["changed"] != float64(2) {
		t.Fatalf("import: %d %v", code, out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/texts", nil)
	for _, it := range items(out) {
		switch it["key"] {
		case "welcome":
			if asJSON(it["override"]) != `{"en":"","fa":""}` {
				t.Fatalf("welcome not reset: %v", it)
			}
		case "btn.buy":
			if it["override"].(map[string]any)["fa"] != "🛒 خرید" {
				t.Fatalf("button not imported: %v", it)
			}
		}
	}
}

func staffByID(out map[string]any, id string) map[string]any {
	for _, it := range items(out) {
		if it["id"] == id {
			return it
		}
	}
	return nil
}

func TestStaffPage(t *testing.T) {
	f, b, _ := setupFixture(t)
	ctx := context.Background()
	code, out := b.do(http.MethodGet, "/api/v1/staff", nil)
	if code != 200 || len(items(out)) != 1 || out["can_grant_owner"] != true || out["owner_configured"] != true {
		t.Fatalf("list: %d %v", code, out)
	}
	me := items(out)[0]
	if me["me"] != true || me["configured_owner"] != true || me["sessions"] != float64(1) || me["last_login"] == nil {
		t.Fatalf("me: %v", me)
	}
	ownerID := me["id"].(string)

	// Add a customer as support.
	cust := f.customer(8701, "sara")
	if code, out := b.do(http.MethodPost, "/api/v1/staff", map[string]string{"user_id": cust.ID, "role": "support"}); code != 400 {
		t.Fatalf("no reason: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/staff", map[string]string{"user_id": cust.ID, "role": "support", "reason": "new helper"})
	if code != 200 || out["staff"].(map[string]any)["role"] != "support" {
		t.Fatalf("add: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/staff", map[string]string{"user_id": cust.ID, "role": "admin", "reason": "x"}); code != 409 {
		t.Fatalf("added twice: %d %v", code, out)
	}
	e := lastAudit(t, b, "entity_id="+cust.ID)
	if e["action"] != "staff.add" || asJSON(e["before"]) != `{"role":"user"}` || asJSON(e["after"]) != `{"role":"support"}` || e["reason"] != "new helper" {
		t.Fatalf("add audit: %v", e)
	}
	banned := f.customer(8702, "bad")
	if err := f.st.SetUserStatus(ctx, f.st.Conn(), banned.ID, "banned"); err != nil {
		t.Fatal(err)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/staff", map[string]string{"user_id": banned.ID, "role": "support", "reason": "x"}); code != 409 {
		t.Fatalf("banned became staff: %d %v", code, out)
	}

	// The new member logs in with a link and sets a password.
	sup := f.browser()
	if code := sup.loginWithLink(8701); code != 200 {
		t.Fatalf("support login: %d", code)
	}
	if code, _ := sup.do(http.MethodGet, "/api/v1/staff", nil); code != 403 {
		t.Fatalf("support saw staff: %d", code)
	}
	_, pw := sup.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "sara", "password": "a long password"})
	secret, _ := pw["secret"].(string)
	c0, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now()))
	if code, _ := sup.do(http.MethodPost, "/api/v1/me/password/confirm", map[string]string{"code": c0}); code != 200 {
		t.Fatalf("confirm: %d", code)
	}
	_, out = b.do(http.MethodGet, "/api/v1/staff", nil)
	if s := staffByID(out, cust.ID); s["password"].(map[string]any)["state"] != "on" || s["sessions"] != float64(1) {
		t.Fatalf("support row: %v", s)
	}

	// Sessions: list, then revoke all; the member is logged out.
	code, out = b.do(http.MethodGet, "/api/v1/staff/"+cust.ID+"/sessions", nil)
	if code != 200 || len(items(out)) != 1 || len(items(out)[0]["id"].(string)) != 64 || items(out)[0]["current"] != false {
		t.Fatalf("sessions: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/staff/"+cust.ID+"/sessions/revoke", map[string]string{"reason": "lost phone"})
	if code != 200 || out["revoked"] != float64(1) {
		t.Fatalf("revoke: %d %v", code, out)
	}
	if code, _ := sup.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("revoked session still works: %d", code)
	}

	// Role change: promoting needs a reason; their sessions end.
	sup2 := f.browser()
	sup2.loginWithLink(8701)
	code, out = b.do(http.MethodPut, "/api/v1/staff/"+cust.ID+"/role", map[string]string{"role": "admin", "reason": "trusted"})
	if code != 200 || out["staff"].(map[string]any)["role"] != "admin" {
		t.Fatalf("role: %d %v", code, out)
	}
	if code, _ := sup2.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("session kept across a role change: %d", code)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/staff/"+cust.ID+"/role", map[string]string{"role": "admin", "reason": "x"}); code != 409 {
		t.Fatalf("same role: %d", code)
	}
	// Nobody changes themselves or the configured owner here.
	if code, out := b.do(http.MethodPut, "/api/v1/staff/"+ownerID+"/role", map[string]string{"role": "admin", "reason": "x"}); code != 409 {
		t.Fatalf("changed myself: %d %v", code, out)
	}
	coOwner := f.staffUser(8703, "owner")
	co := f.browser()
	co.loginWithLink(8703)
	if code, out := co.do(http.MethodPost, "/api/v1/staff/"+ownerID+"/remove", map[string]string{"reason": "coup"}); code != 409 {
		t.Fatalf("co-owner removed the configured owner: %d %v", code, out)
	}
	// Only the configured owner gives or takes the owner role.
	if _, out := co.do(http.MethodGet, "/api/v1/staff", nil); out["can_grant_owner"] != false {
		t.Fatalf("co-owner can grant owner: %v", out)
	}
	if code, out := co.do(http.MethodPut, "/api/v1/staff/"+cust.ID+"/role", map[string]string{"role": "owner", "reason": "x"}); code != 409 {
		t.Fatalf("co-owner granted owner: %d %v", code, out)
	}
	if code, _ := co.do(http.MethodPut, "/api/v1/staff/"+cust.ID+"/role", map[string]string{"role": "support", "reason": "x"}); code != 200 {
		t.Fatalf("co-owner changed an admin: %d", code)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/staff/"+coOwner.ID+"/role", map[string]string{"role": "admin", "reason": "x"}); code != 200 {
		t.Fatalf("owner demoted a co-owner: %d", code)
	}

	// Password reset and unlock.
	creds, _ := f.st.CredentialsFor(ctx, f.st.Conn(), cust.ID)
	for i := 0; i < 5; i++ {
		_, _ = f.st.RecordLoginFailure(ctx, f.st.Conn(), creds.UserID, lockFor)
	}
	_, out = b.do(http.MethodGet, "/api/v1/staff", nil)
	if s := staffByID(out, cust.ID); s["password"].(map[string]any)["locked_until"] == nil {
		t.Fatalf("lock not shown: %v", s)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/staff/"+cust.ID+"/unlock", map[string]string{}); code != 204 {
		t.Fatalf("unlock: %d", code)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/staff/"+cust.ID+"/unlock", map[string]string{}); code != 409 {
		t.Fatalf("unlock again: %d", code)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/staff/"+cust.ID+"/password/reset", map[string]string{"reason": "forgot"}); code != 204 {
		t.Fatalf("reset: %d", code)
	}
	if _, err := f.st.CredentialsFor(ctx, f.st.Conn(), cust.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("password kept after reset: %v", err)
	}

	// Removal: back to customer, sessions end, username free again.
	sup3 := f.browser()
	sup3.loginWithLink(8701)
	if code, _ := b.do(http.MethodPost, "/api/v1/staff/"+cust.ID+"/remove", map[string]string{"reason": "left"}); code != 204 {
		t.Fatalf("remove: %d", code)
	}
	if code, _ := sup3.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("removed member still logged in: %d", code)
	}
	if u, _ := f.st.GetUser(ctx, f.st.Conn(), cust.ID); u.Role != "user" {
		t.Fatalf("role after removal: %s", u.Role)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/staff", nil); staffByID(out, cust.ID) != nil {
		t.Fatal("removed member still listed")
	}

	// Admins never manage staff.
	f.staffUser(8704, "admin")
	adm := f.browser()
	adm.loginWithLink(8704)
	for _, c := range []struct{ m, p string }{{"GET", "/api/v1/staff"}, {"POST", "/api/v1/staff"}, {"PUT", "/api/v1/staff/" + ownerID + "/role"},
		{"POST", "/api/v1/staff/" + ownerID + "/remove"}, {"GET", "/api/v1/staff/" + ownerID + "/sessions"}} {
		if code, _ := adm.do(c.m, c.p, map[string]string{}); code != 403 {
			t.Errorf("admin %s %s: %d", c.m, c.p, code)
		}
	}
}

// Two owners demoting each other at the same moment must not leave the
// store without one: the change re-checks the owners inside its transaction.
func TestLastOwnerKept(t *testing.T) {
	f := newFixture(t)
	f.dom = domain.New(f.st, domain.Config{}) // no owner named by the server
	a, b := f.staffUser(8801, "owner"), f.staffUser(8802, "owner")
	a.Role, b.Role = "owner", "owner"
	ctx := context.Background()
	staleB := *b
	if _, err := f.dom.SetStaffRole(ctx, a, b.ID, "admin", "x"); err != nil {
		t.Fatal(err)
	}
	// B acts on what it loaded before it was demoted.
	_, err := f.dom.SetStaffRole(ctx, &staleB, a.ID, "admin", "x")
	if !errors.Is(err, domain.ErrState) || !strings.Contains(err.Error(), "at least one owner") {
		t.Fatalf("the last owner was demoted: %v", err)
	}
}

func TestMySessionsAndReauth(t *testing.T) {
	f, b, _ := setupFixture(t)
	other := f.browser()
	other.loginWithLink(ownerTG)
	code, out := b.do(http.MethodGet, "/api/v1/me/sessions", nil)
	if code != 200 || len(items(out)) != 2 {
		t.Fatalf("sessions: %d %v", code, out)
	}
	var mine, theirs string
	for _, s := range items(out) {
		if s["current"] == true {
			mine = s["id"].(string)
		} else {
			theirs = s["id"].(string)
		}
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/me/sessions/"+mine+"/revoke", map[string]string{}); code != 409 {
		t.Fatalf("revoked the current session: %d", code)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/me/sessions/"+theirs+"/revoke", map[string]string{}); code != 204 {
		t.Fatalf("revoke other: %d", code)
	}
	if code, _ := other.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("other device still logged in: %d", code)
	}
	third := f.browser()
	third.loginWithLink(ownerTG)
	if code, out := b.do(http.MethodPost, "/api/v1/me/sessions/revoke-others", map[string]string{}); code != 200 || out["revoked"] != float64(1) {
		t.Fatalf("revoke others: %d %v", code, out)
	}

	// Set a password, then log in with it: replacing or removing it now
	// needs the current code.
	// Codes made now must stay in the server's window: skip the end of a step.
	if sec := time.Now().Unix() % 30; sec >= 27 {
		time.Sleep(time.Duration(31-sec) * time.Second)
	}
	_, pw := b.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "boss", "password": "a long password"})
	secret := pw["secret"].(string)
	now := time.Now()
	// Three codes in a row, all within the accepted window: confirm, log in, remove.
	c0, _ := webauth.TOTPCode(secret, webauth.TOTPStep(now)-1)
	b.do(http.MethodPost, "/api/v1/me/password/confirm", map[string]string{"code": c0})
	pass := f.browser()
	c1, _ := webauth.TOTPCode(secret, webauth.TOTPStep(now))
	if code, me := pass.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "a long password", "code": c1}); code != 200 ||
		me["password"].(map[string]any)["reauth"] != true {
		t.Fatalf("password login: %d %v", code, me)
	}
	if code, out := pass.do(http.MethodDelete, "/api/v1/me/password", nil); code != 403 || out["error"] != "code_required" {
		t.Fatalf("removed without a code: %d %v", code, out)
	}
	if code, out := pass.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "evil", "password": "another long password"}); code != 403 {
		t.Fatalf("replaced without a code: %d %v", code, out)
	}
	if code, out := pass.do(http.MethodDelete, "/api/v1/me/password", map[string]string{"code": "000000"}); code != 400 || out["error"] != "code_wrong" {
		t.Fatalf("wrong code: %d %v", code, out)
	}
	c2, _ := webauth.TOTPCode(secret, webauth.TOTPStep(now)+1)
	if code, out := pass.do(http.MethodDelete, "/api/v1/me/password", map[string]string{"code": c2}); code != 200 || out["password"].(map[string]any)["enabled"] != false {
		t.Fatalf("remove with the code: %d %v", code, out)
	}
}

func TestAuditTrail(t *testing.T) {
	f, b, _ := setupFixture(t)
	ctx := context.Background()
	// Failed logins name the account, with no actor.
	owner, _ := f.st.GetUserByTelegramID(ctx, f.st.Conn(), ownerTG)
	hash, _ := webauth.HashPassword("a long password")
	secret, _ := webauth.NewTOTPSecret()
	enc, _ := f.api.cfg.Secrets.Encrypt([]byte(secret))
	_ = f.st.SetPendingCredentials(ctx, f.st.Conn(), owner.ID, "boss", hash, enc)
	_ = f.st.ConfirmCredentials(ctx, f.st.Conn(), owner.ID, 1)
	anon := f.browser()
	anon.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "wrong password!", "code": "1"})
	e := lastAudit(t, b, "action=dashboard.login_failed")
	if e["actor"] != nil || e["entity_id"] != owner.ID || asJSON(e["after"]) != `{"locked":false,"method":"password"}` {
		t.Fatalf("login failed: %v", e)
	}
	if e := lastAudit(t, b, "action=dashboard.link_created"); e["entity_id"] != owner.ID {
		t.Fatalf("link created: %v", e)
	}
	// Exports are recorded.
	if resp, err := b.c.Get(f.srv.URL + "/api/v1/audit.csv?action=dashboard"); err == nil {
		resp.Body.Close()
	}
	if e := lastAudit(t, b, "action=export.audit"); e["actor"] == nil || !strings.Contains(asJSON(e["after"]), `"action":["dashboard"]`) {
		t.Fatalf("export: %v", e)
	}
	// Nobody can change or remove an entry, not even with SQL.
	if _, err := f.st.DB().Exec(ctx, `UPDATE core.audit_log SET reason = 'x'`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("audit entry changed: %v", err)
	}
	if _, err := f.st.DB().Exec(ctx, `DELETE FROM core.audit_log`); err == nil {
		t.Fatal("audit entries deleted")
	}
	// A customer who becomes the configured owner gets the role at start-up.
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramID: 8901})
	c := f.customer(8901, "newowner")
	if err := f.dom.EnsureOwner(store.WithSource(ctx, store.SourceSystem)); err != nil {
		t.Fatal(err)
	}
	if u, _ := f.st.GetUser(ctx, f.st.Conn(), c.ID); u.Role != "owner" {
		t.Fatalf("not promoted: %s", u.Role)
	}
	if e := lastAudit(t, b, "action=staff.owner_bootstrap"); e["entity_id"] != c.ID || e["actor"] != nil || e["source"] != "system" {
		t.Fatalf("bootstrap: %v", e)
	}
}

// Every setting names a real permission (a typo would let nobody change it).
func TestSettingPermsExist(t *testing.T) {
	for _, sp := range domain.SettingSpecs {
		if !contains(allPerms, sp.Perm) {
			t.Errorf("%s: unknown permission %q", sp.Key, sp.Perm)
		}
	}
	if Allowed("owner", "made.up") {
		t.Error("owner allowed a permission that does not exist")
	}
}
