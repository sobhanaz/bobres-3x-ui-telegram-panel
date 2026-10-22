package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestRenderLegalEscapesEverything(t *testing.T) {
	got := string(renderLegal("# Title <script>\n\nA **bold** and *soft* word, a [link](https://example.com/a?b=1&c=2) and <img src=x onerror=alert(1)>.\n- one\n- two & <b>three</b>\n\n[bad](javascript:alert(1)) [http](http://example.com)\n\n## Sub\n### Third"))
	for _, want := range []string{"<h1>Title &lt;script&gt;</h1>", "<strong>bold</strong>", "<em>soft</em>",
		`<a href="https://example.com/a?b=1&amp;c=2" rel="noopener noreferrer">link</a>`, "&lt;img src=x onerror=alert(1)&gt;",
		"<ul>", "<li>one</li>", "<li>two &amp; &lt;b&gt;three&lt;/b&gt;</li>", "</ul>", "<h2>Sub</h2>", "<h3>Third</h3>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	for _, bad := range []string{"<script", "<img", "<b>", `href="javascript`, `href="http://`} {
		if strings.Contains(got, bad) {
			t.Errorf("found %q in\n%s", bad, got)
		}
	}
}

// Every store serves its own Terms and Privacy pages: a default text in both languages with the store's name and contact put in, the owner's
// own text when they wrote one, headers of a public page, and a language switch.
func TestLegalPages(t *testing.T) {
	f := newPageFixture(t)
	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{"branding.name": "Acme <VPN>", "branding.support": "@acme_help"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, lang, dir, title string
		want                   []string
	}{
		{"/terms", "fa", "rtl", "قوانین", []string{"قوانین و شرایط استفاده", "Acme &lt;VPN&gt;", "@acme_help", `href="/terms?l=en"`}},
		{"/terms?l=en", "en", "ltr", "Terms", []string{"Terms of Service", "Acme &lt;VPN&gt;", "@acme_help", "Refunds"}},
		{"/privacy", "fa", "rtl", "حریم خصوصی", []string{"سیاست حریم خصوصی", "Acme &lt;VPN&gt;"}},
		{"/privacy?l=en", "en", "ltr", "Privacy", []string{"Privacy Policy", "We do not sell your data."}},
	} {
		code, h, body := f.page(tc.path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", tc.path, code)
		}
		for _, w := range append(tc.want, `lang="`+tc.lang+`"`, `dir="`+tc.dir+`"`, "<title>"+tc.title) {
			if !strings.Contains(body, w) {
				t.Errorf("%s lacks %q", tc.path, w)
			}
		}
		if strings.Contains(body, "{store}") || strings.Contains(body, "{contact}") || strings.Contains(body, "Acme <VPN>") {
			t.Errorf("%s: a placeholder was left or the name was not escaped", tc.path)
		}
		if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: headers %v", tc.path, h)
		}
	}
	// No support contact: the page points to the bot's Support button.
	f.sql(`DELETE FROM core.settings WHERE key = 'branding.support'`)
	if _, _, body := f.page("/terms?l=en"); !strings.Contains(body, "the Support button in the bot") {
		t.Error("no contact fallback")
	}

	// The owner's own text replaces the default for that language only, and is escaped.
	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{"legal.terms.en": "# Our rules\n\nBe kind to {store} <b>staff</b>."}); err != nil {
		t.Fatal(err)
	}
	if _, _, body := f.page("/terms?l=en"); !strings.Contains(body, "<h1>Our rules</h1>") || !strings.Contains(body, "Be kind to Acme &lt;VPN&gt; &lt;b&gt;staff&lt;/b&gt;.") || strings.Contains(body, "Refunds") {
		t.Errorf("the owner's text: %s", body)
	}
	if _, _, body := f.page("/terms"); !strings.Contains(body, "قوانین و شرایط استفاده") {
		t.Error("the Persian page lost its default")
	}
	// Other paths are not pages.
	if code, _, _ := f.page("/terms/x"); code == http.StatusOK {
		t.Error("/terms/x served")
	}
}

// The subscription page links the store's own pages while the owner has set no address.
func TestSubPageFootersFallBackToTheStoresPages(t *testing.T) {
	f := newPageFixture(t)
	if _, _, body := f.page("/sub/" + f.token); !strings.Contains(body, `href="/terms"`) || !strings.Contains(body, `href="/privacy"`) {
		t.Errorf("no fallback links: %s", body)
	}
	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{"branding.terms_url": "https://acme.example/terms"}); err != nil {
		t.Fatal(err)
	}
	if _, _, body := f.page("/sub/" + f.token); !strings.Contains(body, `href="https://acme.example/terms"`) || !strings.Contains(body, `href="/privacy"`) {
		t.Errorf("the owner's address and the fallback: %s", body)
	}
}

// The Branding API carries the pages' texts: the owner's own ("" = default) and the defaults; a save keeps what it does not name, and "" goes back
// to the default.
func TestBrandingLegalTexts(t *testing.T) {
	_, b, _, _ := salesFixture(t)
	code, out := b.do(http.MethodGet, "/api/v1/branding", nil)
	defs, _ := out["legal_defaults"].(map[string]any)
	terms, _ := defs["terms"].(map[string]any)
	if code != http.StatusOK || !strings.Contains(terms["en"].(string), "{store}") || !strings.Contains(terms["fa"].(string), "{store}") {
		t.Fatalf("defaults: %d %v", code, out)
	}
	if mine, _ := out["legal"].(map[string]any)["terms"].(map[string]any); mine["en"] != "" || mine["fa"] != "" {
		t.Fatalf("own texts before: %v", out["legal"])
	}
	save := map[string]any{"name": "Acme", "support": "", "color": "", "terms_url": "", "privacy_url": "", "currency": map[string]string{"fa": "", "en": ""},
		"legal": map[string]any{"terms": map[string]string{"fa": "", "en": "# Mine"}}}
	if code, out = b.do(http.MethodPut, "/api/v1/branding", save); code != http.StatusOK {
		t.Fatalf("save: %d %v", code, out)
	}
	if mine := out["legal"].(map[string]any)["terms"].(map[string]any); mine["en"] != "# Mine" || mine["fa"] != "" {
		t.Fatalf("own texts after: %v", out["legal"])
	}
	// A save that names no page keeps them.
	delete(save, "legal")
	if code, out = b.do(http.MethodPut, "/api/v1/branding", save); code != http.StatusOK || out["legal"].(map[string]any)["terms"].(map[string]any)["en"] != "# Mine" {
		t.Fatalf("a save without legal: %d %v", code, out["legal"])
	}
	// "" goes back to the default; an unknown page and a too long text are refused.
	save["legal"] = map[string]any{"terms": map[string]string{"fa": "", "en": ""}}
	if code, out = b.do(http.MethodPut, "/api/v1/branding", save); code != http.StatusOK || out["legal"].(map[string]any)["terms"].(map[string]any)["en"] != "" {
		t.Fatalf("reset: %d %v", code, out["legal"])
	}
	save["legal"] = map[string]any{"cookies": map[string]string{"fa": "", "en": "x"}}
	if code, _ = b.do(http.MethodPut, "/api/v1/branding", save); code != http.StatusBadRequest {
		t.Errorf("an unknown page: %d", code)
	}
	save["legal"] = map[string]any{"terms": map[string]string{"fa": "", "en": strings.Repeat("x", 30001)}}
	if code, _ = b.do(http.MethodPut, "/api/v1/branding", save); code != http.StatusBadRequest {
		t.Errorf("a text over the limit: %d", code)
	}
}

type fakeRateTransport struct {
	status int
	body   string
}

func (f fakeRateTransport) PostUSDTRate(context.Context) (int, []byte, error) {
	return f.status, []byte(f.body), nil
}

// The Gateways page shows what the store charges for one USDT, and can read the live price now.
func TestUsdtRateAPI(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	f.sql(`DELETE FROM core.settings WHERE key IN ('state.usdt_rate.live', 'payments.usdt_rate', 'payments.usdt_rate_auto', 'payments.usdt_rate_margin')`)
	code, out := b.do(http.MethodGet, "/api/v1/usdt-rate", nil)
	if code != http.StatusOK || out["rate"] != float64(0) || out["source"] != "" || out["live"] != nil || out["auto"] != true {
		t.Fatalf("before: %d %v", code, out)
	}
	// No bot, no price.
	if code, _ = b.do(http.MethodPost, "/api/v1/usdt-rate/refresh", nil); code != http.StatusBadGateway {
		t.Fatalf("refresh without a bot: %d", code)
	}
	f.dom.SetRateTransport(fakeRateTransport{status: 200, body: `{"rate":271600,"sources":["tetherland","wallex"]}`})
	code, out = b.do(http.MethodPost, "/api/v1/usdt-rate/refresh", nil)
	live, _ := out["live"].(map[string]any)
	if code != http.StatusOK || out["rate"] != float64(271600) || out["source"] != "live" || live["rate"] != float64(271600) || live["fresh"] != true || len(live["sources"].([]any)) != 2 {
		t.Fatalf("after: %d %v", code, out)
	}
	f.dom.SetRateTransport(fakeRateTransport{status: 502, body: `{"error":"unreachable","message":"no price source answered"}`})
	if code, out = b.do(http.MethodPost, "/api/v1/usdt-rate/refresh", nil); code != http.StatusBadGateway {
		t.Fatalf("a failed read: %d %v", code, out)
	}
	if code, out = b.do(http.MethodGet, "/api/v1/usdt-rate", nil); out["rate"] != float64(271600) {
		t.Fatalf("the last good price stays: %d %v", code, out)
	}
	f.sql(`DELETE FROM core.settings WHERE key = 'state.usdt_rate.live'`)
}
