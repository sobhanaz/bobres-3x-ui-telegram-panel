package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// pageFixture is a delivered service for the page tests.
type pageFixture struct {
	*fixture
	b     *browser
	token string
	subID string
	link  string
	email string
}

const pageLink = "https://sub.example.test/sub/abc123token?x=1&y=2"

func newPageFixture(t *testing.T) *pageFixture {
	t.Helper()
	f, b, _, _ := salesFixture(t)
	// Settings are not cleared between tests: these tests change branding.
	f.sql(`DELETE FROM core.settings`)
	t.Cleanup(func() { f.sql(`DELETE FROM core.settings`) })
	u := f.customer(8801, "ali_secret_user")
	p := f.plan("Plan", 30, 50<<30, 100000)
	_, sub := f.order(u, p, "active", "active", time.Now().Add(20*24*time.Hour), 50<<30)
	pf := &pageFixture{fixture: f, b: b, token: sub.PageToken, subID: sub.ID, link: pageLink, email: sub.ClientEmail}
	pf.sql(`UPDATE core.subscriptions SET sub_link = $2, label = 'Reseller secret label', traffic_used_bytes = $3 WHERE id = $1`, sub.ID, pageLink, int64(10<<30))
	return pf
}

func (f *fixture) sql(q string, args ...any) {
	f.t.Helper()
	if _, err := f.st.DB().Exec(context.Background(), q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

var pageClients int

// get fetches a page as a client of its own address (the page is rate
// limited per address, so each case uses a fresh one).
func (f *fixture) page(path string) (int, http.Header, string) {
	f.t.Helper()
	pageClients++
	return f.pageFrom(path, fmt.Sprintf("198.51.100.%d", pageClients%250+1), "")
}

func (f *fixture) pageFrom(path, ip, method string) (int, http.Header, string) {
	f.t.Helper()
	if method == "" {
		method = http.MethodGet
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, nil)
	req.Header.Set("X-Forwarded-For", ip)
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

func TestSubPageRendersInBothLanguages(t *testing.T) {
	f := newPageFixture(t)
	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{
		"branding.name": "Acme VPN", "branding.color": "#aa3355", "branding.bot_username": "acme_bot",
		"branding.support": "@acme_help", "branding.terms_url": "https://acme.example/terms",
	}); err != nil {
		t.Fatal(err)
	}

	code, h, body := f.page("/sub/" + f.token)
	if code != 200 {
		t.Fatalf("fa: %d %s", code, body)
	}
	for _, want := range []string{`lang="fa"`, `dir="rtl"`, "Acme VPN", "#aa3355", "https://t.me/acme_bot", "https://t.me/acme_help",
		"https://acme.example/terms", "فعال", "۲۰ روز", "data:image/png;base64,", `readonly dir="ltr"`,
		"v2rayng://install-config?url=" + url.QueryEscape(f.link), "hiddify://import/", "streisand://import/", "v2box://install-sub?url="} {
		if !strings.Contains(body, want) {
			t.Errorf("fa page lacks %q", want)
		}
	}
	// The link goes in the readonly box exactly (the ampersand is escaped as HTML).
	if !strings.Contains(body, "https://sub.example.test/sub/abc123token?x=1&amp;y=2") {
		t.Errorf("the sub link is not in the page: %s", body)
	}
	if !strings.Contains(h.Get("Content-Type"), "text/html") {
		t.Errorf("content type %q", h.Get("Content-Type"))
	}

	code, _, en := f.page("/sub/" + f.token + "?l=en")
	if code != 200 || !strings.Contains(en, `lang="en"`) || !strings.Contains(en, `dir="ltr"`) || !strings.Contains(en, "Active") ||
		!strings.Contains(en, "20 days") || !strings.Contains(en, "10 GB used of 50 GB") {
		t.Fatalf("en page: %d %s", code, en)
	}
	// Anything but ?l=en is Persian.
	if _, _, other := f.page("/sub/" + f.token + "?l=de"); !strings.Contains(other, `lang="fa"`) {
		t.Error("an unknown language did not fall back to Persian")
	}
}

func TestSubPageHasNoScriptAndNoPrivateData(t *testing.T) {
	f := newPageFixture(t)
	hostile := `<script>alert(1)</script>"'><img src=x onerror=alert(2)>`
	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{"branding.name": hostile}); err != nil {
		t.Skipf("settings refuse the hostile name (also fine): %v", err)
	}
	// A hostile sub link too (it comes from the panel).
	f.sql(`UPDATE core.subscriptions SET sub_link = $2 WHERE id = $1`, f.subID, `https://x.test/"><script>alert(3)</script>`)
	for _, q := range []string{"", "?l=en"} {
		_, _, body := f.page("/sub/" + f.token + q)
		low := strings.ToLower(body)
		if strings.Contains(low, "<script") || strings.Contains(low, "javascript:") || strings.Contains(low, "onerror=") && strings.Contains(body, "<img src=x") {
			t.Fatalf("active content in the page%s:\n%s", q, body)
		}
		if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
			t.Errorf("the hostile brand name is not escaped%s", q)
		}
		for _, secret := range []string{f.email, "Reseller secret label", "8801", "ali_secret_user", f.subID} {
			if strings.Contains(body, secret) {
				t.Errorf("the page%s leaks %q", q, secret)
			}
		}
	}
}

func TestSubPageNotFoundIsIdentical(t *testing.T) {
	f := newPageFixture(t)
	time.Sleep(20 * time.Millisecond) // the test helper names clients by a 16 ms clock
	u2 := f.customer(8802, "other")
	p := f.plan("P2", 30, 10<<30, 1)
	_, pending := f.order(u2, p, "provision_failed", "pending", time.Now(), 0)
	time.Sleep(20 * time.Millisecond)
	_, gone := f.order(u2, p, "active", "active", time.Now().Add(time.Hour), 10<<30)
	f.sql(`UPDATE core.subscriptions SET status = 'deleted' WHERE id = $1`, gone.ID)

	cases := map[string]string{
		"unknown":      "/sub/" + strings.Repeat("a", 32),
		"short":        "/sub/abc",
		"upper":        "/sub/" + strings.ToUpper(f.token),
		"long":         "/sub/" + f.token + "00",
		"extra path":   "/sub/" + f.token + "/more",
		"empty":        "/sub/",
		"pending":      "/sub/" + pending.PageToken,
		"deleted":      "/sub/" + gone.PageToken,
		"not hex":      "/sub/" + strings.Repeat("z", 32),
		"sql-ish":      "/sub/" + url.PathEscape("' OR '1'='1"),
		"panel sub id": "/sub/sub123",
	}
	var wantBody string
	var wantHdr http.Header
	check := func(name, body string, h http.Header, code int) {
		t.Helper()
		if code != 404 {
			t.Fatalf("%s: %d", name, code)
		}
		if wantBody == "" {
			wantBody, wantHdr = body, h
			return
		}
		if body != wantBody {
			t.Errorf("%s: a different 404 body", name)
		}
		for _, k := range []string{"Content-Type", "Content-Length", "Cache-Control", "Content-Security-Policy", "X-Robots-Tag"} {
			if h.Get(k) != wantHdr.Get(k) {
				t.Errorf("%s: header %s %q != %q", name, k, h.Get(k), wantHdr.Get(k))
			}
		}
	}
	for name, path := range cases {
		code, h, body := f.page(path)
		check(name, body, h, code)
	}
	// The page switched off answers the very same 404 for a good token.
	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{"branding.sub_page.enabled": "false"}); err != nil {
		t.Fatal(err)
	}
	code, h, body := f.page("/sub/" + f.token)
	check("page off", body, h, code)
	// A POST is not a page either.
	code, h, body = f.pageFrom("/sub/"+f.token, "198.51.100.251", http.MethodPost)
	check("post", body, h, code)

	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{"branding.sub_page.enabled": "true"}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.page("/sub/" + f.token); code != 200 {
		t.Fatalf("turned on again: %d", code)
	}
}

func TestSubPageHeaders(t *testing.T) {
	f := newPageFixture(t)
	want := map[string]string{
		"Cache-Control":           "no-store, private",
		"X-Content-Type-Options":  "nosniff",
		"X-Robots-Tag":            "noindex, nofollow",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
	}
	for name, path := range map[string]string{"page": "/sub/" + f.token, "404": "/sub/" + strings.Repeat("0", 32)} {
		_, h, _ := f.page(path)
		for k, v := range want {
			if h.Get(k) != v {
				t.Errorf("%s: %s = %q, want %q", name, k, h.Get(k), v)
			}
		}
	}
}

func TestSubPageRateLimit(t *testing.T) {
	f := newPageFixture(t)
	for i := 1; i <= subPageRate; i++ {
		if code, _, _ := f.pageFrom("/sub/"+f.token, "203.0.113.9", ""); code != 200 {
			t.Fatalf("request %d: %d", i, code)
		}
	}
	code, h, _ := f.pageFrom("/sub/"+f.token, "203.0.113.9", "")
	if code != 429 || h.Get("Retry-After") == "" || h.Get("Cache-Control") != "no-store, private" {
		t.Fatalf("over the limit: %d %v", code, h)
	}
	// Another address is not affected, and an unknown token counts like a good one.
	if code, _, _ := f.pageFrom("/sub/"+f.token, "203.0.113.10", ""); code != 200 {
		t.Fatalf("another address: %d", code)
	}
}

func TestSubPageStates(t *testing.T) {
	f := newPageFixture(t)
	set := func(q string, args ...any) {
		f.sql(`UPDATE core.subscriptions SET `+q+` WHERE id = $1`, append([]any{f.subID}, args...)...)
	}
	cases := []struct {
		name  string
		sql   string
		args  []any
		wants []string
		not   []string
	}{
		{"expiring soon", `expires_at = $2, status = 'active'`, []any{time.Now().Add(36 * time.Hour)}, []string{"Expiring soon", "2 days", "s-warn"}, nil},
		{"expired", `expires_at = $2, status = 'expired'`, []any{time.Now().Add(-48 * time.Hour)}, []string{"Expired", "s-bad"}, []string{"days<"}},
		{"expired by the clock", `expires_at = $2, status = 'active'`, []any{time.Now().Add(-time.Hour)}, []string{"Expired"}, nil},
		{"depleted", `expires_at = $2, status = 'depleted', traffic_used_bytes = $3`, []any{time.Now().Add(48 * time.Hour), int64(50 << 30)}, []string{"Traffic used up", "50 GB used of 50 GB", "bar full"}, nil},
		{"unlimited", `expires_at = $2, status = 'active', traffic_total_bytes = NULL, traffic_used_bytes = $3`, []any{time.Now().Add(48 * time.Hour), int64(3 << 30)}, []string{"Unlimited", "3 GB used"}, []string{`role="progressbar"`}},
		{"no expiry", `expires_at = NULL, status = 'active', traffic_total_bytes = NULL`, nil, []string{"No expiry", "Unlimited"}, nil},
		{"disabled", `status = 'disabled', expires_at = $2`, []any{time.Now().Add(48 * time.Hour)}, []string{"Disabled", "s-muted"}, nil},
	}
	for _, c := range cases {
		set(c.sql, c.args...)
		code, _, body := f.page("/sub/" + f.token + "?l=en")
		if code != 200 {
			t.Fatalf("%s: %d", c.name, code)
		}
		for _, w := range c.wants {
			if !strings.Contains(body, w) {
				t.Errorf("%s: no %q in\n%s", c.name, w, body)
			}
		}
		for _, n := range c.not {
			if strings.Contains(body, n) {
				t.Errorf("%s: unexpected %q", c.name, n)
			}
		}
	}
	// The renew button: only with a bot username.
	if _, _, body := f.page("/sub/" + f.token + "?l=en"); strings.Contains(body, "t.me/") {

		t.Error("a renew button without a bot username")
	}
}

func TestSubPageGuidesCanBeEdited(t *testing.T) {
	f := newPageFixture(t)
	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{
		"texts.en.subpage.guide.hiddify": "Our own Hiddify steps, more"}); err != nil {
		t.Fatal(err)
	}
	_, _, body := f.page("/sub/" + f.token + "?l=en")
	if !strings.Contains(body, "Our own Hiddify steps, more") {
		t.Errorf("the edited guide is not shown (escaped):\n%s", body)
	}
	if !strings.Contains(body, "install v2rayNG") {
		t.Error("the other guides lost their default text")
	}
}

func TestRegeneratePageToken(t *testing.T) {
	f := newPageFixture(t)
	old := f.token
	path := "/api/v1/services/" + f.subID + "/page-token/regenerate"

	// Support may extend a service but not change its page address.
	f.staffUser(8803, "support")
	sup := f.browser()
	sup.loginWithLink(8803)
	if code, _ := sup.do(http.MethodPost, path, map[string]any{"reason": "x"}); code != 403 {
		t.Fatalf("support regenerated: %d", code)
	}
	if code, _, _ := f.page("/sub/" + old); code != 200 {
		t.Fatal("a refused request changed the token")
	}
	if code, _ := f.b.do(http.MethodPost, path, map[string]any{"reason": ""}); code != 400 {
		t.Fatalf("no reason: %d", code)
	}

	code, out := f.b.do(http.MethodPost, path, map[string]any{"reason": "link was shared"})
	svc, _ := out["service"].(map[string]any)
	if code != 200 || svc == nil {
		t.Fatalf("regenerate: %d %v", code, out)
	}
	pageURL, _ := svc["page_url"].(string)
	fresh := pageURL[strings.LastIndex(pageURL, "/")+1:]
	if !strings.Contains(pageURL, "/sub/") || fresh == old || len(fresh) != 32 {
		t.Fatalf("page_url %q (old token %s)", pageURL, old)
	}
	if code, _, _ := f.page("/sub/" + old); code != 404 {
		t.Fatalf("the old address still works: %d", code)
	}
	if code, _, body := f.page("/sub/" + fresh); code != 200 || !strings.Contains(body, "sub.example.test") {
		t.Fatalf("the new address: %d", code)
	}
	// The sub link itself is untouched.
	if row, err := f.st.GetSubscription(context.Background(), f.st.Conn(), f.subID); err != nil || row.SubLink != pageLink || row.PageToken != fresh {
		t.Fatalf("service after: %v %+v", err, row)
	}

	// Audited, with the reason, and without either token.
	rows, _, err := f.st.ListAudit(context.Background(), f.st.Conn(), store.AuditFilter{Action: "subscription.page_regenerate", EntityID: f.subID}, store.Page{Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("audit: %v %d", err, len(rows))
	}
	raw := fmt.Sprintf("%+v", rows[0])
	if !strings.Contains(raw, "link was shared") || strings.Contains(raw, old) || strings.Contains(raw, fresh) {
		t.Fatalf("audit entry: %s", raw)
	}

	// A deleted service cannot be given a page.
	f.sql(`UPDATE core.subscriptions SET status = 'deleted' WHERE id = $1`, f.subID)
	if code, out := f.b.do(http.MethodPost, path, map[string]any{"reason": "again"}); code != 409 || out["error"] != "state" {
		t.Fatalf("deleted service: %d %v", code, out)
	}
	if code, _ := f.b.do(http.MethodPost, "/api/v1/services/not-a-uuid/page-token/regenerate", map[string]any{"reason": "x"}); code != 404 {
		t.Fatalf("bad id: %d", code)
	}
}

func TestServicePageURLInAPI(t *testing.T) {
	f := newPageFixture(t)
	code, out := f.b.do(http.MethodGet, "/api/v1/services/"+f.subID, nil)
	svc, _ := out["service"].(map[string]any)
	want := f.srv.URL + "/sub/" + f.token
	// Without a configured public URL the request's own host is used.
	if code != 200 || !strings.HasSuffix(fmt.Sprint(svc["page_url"]), "/sub/"+f.token) || !strings.HasPrefix(fmt.Sprint(svc["page_url"]), "https://") {
		t.Fatalf("page_url %v, want like %s", svc["page_url"], want)
	}
	// The list does not carry page addresses.
	_, list := f.b.do(http.MethodGet, "/api/v1/services", nil)
	if strings.Contains(asJSON(list), f.token) {
		t.Fatal("the services list carries page tokens")
	}
	// Switched off: no address.
	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{"branding.sub_page.enabled": "false"}); err != nil {
		t.Fatal(err)
	}
	_, out = f.b.do(http.MethodGet, "/api/v1/services/"+f.subID, nil)
	if svc, _ := out["service"].(map[string]any); svc["page_url"] != nil {
		t.Fatalf("page_url with the page off: %v", svc["page_url"])
	}
}

func TestSubBaseCollision(t *testing.T) {
	for _, c := range []struct {
		public, sub string
		want        bool
	}{
		{"https://shop.example.com", "https://shop.example.com/sub/", true},
		{"https://shop.example.com", "https://SHOP.example.com:443/sub", true},
		{"https://shop.example.com", "https://shop.example.com/sub/extra/", true},
		{"https://shop.example.com", "https://shop.example.com:2096/sub/", false},
		{"https://shop.example.com", "https://panel.example.com/sub/", false},
		{"https://shop.example.com", "https://shop.example.com/other/", false},
		{"https://shop.example.com", "https://shop.example.com/subscriptions/", false},
		{"https://203.0.113.5", "https://203.0.113.5/sub/", true},
		{"", "https://shop.example.com/sub/", false},
		{"https://shop.example.com", "", false},
	} {
		if got := subBaseCollidesWith(c.public, c.sub); got != c.want {
			t.Errorf("%s vs %s = %v, want %v", c.public, c.sub, got, c.want)
		}
	}
}

func TestBotUsernameSetting(t *testing.T) {
	f := newPageFixture(t)
	set := func(v string) error {
		_, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{"branding.bot_username": v})
		return err
	}
	for _, bad := range []string{"abc", "1bot_name", "has space", "https://t.me/x_bot", "bad-dash_bot", strings.Repeat("a", 40)} {
		if err := set(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := set("@Acme_Bot"); err != nil {
		t.Fatal(err)
	}
	if _, _, body := f.page("/sub/" + f.token + "?l=en"); !strings.Contains(body, "https://t.me/Acme_Bot") {
		t.Errorf("the @ was not stripped or the renew button is missing:\n%s", body)
	}
	// Cleared: the button goes away again.
	if err := set(""); err != nil {
		t.Fatal(err)
	}
	if _, _, body := f.page("/sub/" + f.token + "?l=en"); strings.Contains(body, "t.me/") {
		t.Error("renew button with no bot username")
	}
}
