package dashboard

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
		"index.html":           {Data: []byte("<html>app</html>")},
		"assets/index-ab12.js": {Data: []byte("console.log(1)")},
		"favicon.svg":          {Data: []byte("<svg/>")},
	})
	for path, want := range map[string]string{
		"/admin/":                     "<html>app</html>",
		"/admin/users/42":             "<html>app</html>", // the app's own route
		"/admin/assets/index-ab12.js": "console.log(1)",
		"/admin/favicon.svg":          "<svg/>",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(path, "/assets/") && !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("%s: hashed assets must be cached: %q", path, rec.Header().Get("Cache-Control"))
		}
		if path == "/admin/" && rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("index must not be cached: %q", rec.Header().Get("Cache-Control"))
		}
		if rec.Header().Get("Content-Security-Policy") != CSP {
			t.Errorf("%s: CSP %q", path, rec.Header().Get("Content-Security-Policy"))
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/assets/missing.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a missing asset must be 404, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/../../etc/passwd", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("path traversal must fall back to the app: %d %q", rec.Code, rec.Body.String())
	}
}

func TestNotBuilt(t *testing.T) {
	rec := httptest.NewRecorder()
	handler(fstest.MapFS{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not built") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

// Caddy sends the dashboard's CSP too; two different policies would both apply
// and could block the app in production only.
func TestCaddyfileSendsTheSameCSP(t *testing.T) {
	caddy, err := os.ReadFile("../../deploy/Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(caddy), `header Content-Security-Policy "`+CSP+`"`) {
		t.Fatalf("deploy/Caddyfile must send the dashboard CSP:\n%s", CSP)
	}
}
