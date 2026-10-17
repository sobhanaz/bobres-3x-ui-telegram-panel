package miniapp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHandlerServesTheAppAndItsRoutes(t *testing.T) {
	h := handler(fstest.MapFS{
		"index.html":           {Data: []byte("<html>mini</html>")},
		"assets/index-ab12.js": {Data: []byte("console.log(1)")},
	})
	for path, want := range map[string]string{
		"/app/":                     "<html>mini</html>",
		"/app/anything":             "<html>mini</html>",
		"/app/assets/index-ab12.js": "console.log(1)",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Security-Policy") != CSP {
			t.Errorf("%s: CSP %q", path, rec.Header().Get("Content-Security-Policy"))
		}
		if strings.Contains(path, "/assets/") && !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("%s: hashed assets must be cached", path)
		}
		if path == "/app/" && rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("index must not be cached")
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/assets/missing.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a missing asset must be 404, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/app/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
}

func TestNotBuilt(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not built") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

// Only Telegram's own origins may script or frame the page.
func TestCSPAllowsOnlyTelegram(t *testing.T) {
	for _, want := range []string{"script-src 'self' https://telegram.org", "frame-ancestors https://*.telegram.org", "base-uri 'none'"} {
		if !strings.Contains(CSP, want) {
			t.Errorf("CSP lacks %q", want)
		}
	}
	if strings.Contains(CSP, "unsafe-eval") || strings.Contains(CSP, "script-src 'self' 'unsafe-inline'") {
		t.Error("scripts must not be inline or eval")
	}
}

func TestCaddyfileSendsTheSameCSP(t *testing.T) {
	caddy, err := os.ReadFile("../../deploy/caddy/Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	text := string(caddy)
	i := strings.Index(text, "handle /app*")
	if i < 0 {
		t.Fatal("deploy/caddy/Caddyfile must route /app")
	}
	block := text[i:]
	if j := strings.Index(block, "\n\t}\n"); j > 0 {
		block = block[:j]
	}
	if !strings.Contains(block, `header Content-Security-Policy "`+CSP+`"`) {
		t.Fatalf("the /app block must send the Mini App CSP:\n%s", CSP)
	}
	if !strings.Contains(block, "header -X-Frame-Options") {
		t.Fatal("the /app block must drop X-Frame-Options so Telegram web clients can frame it")
	}
}
