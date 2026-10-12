package runner

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeLicenseServer is the vendor's license server: it answers status and
// body (with headers) and keeps what it was sent.
type fakeLicenseServer struct {
	mu      sync.Mutex
	paths   []string
	bodies  []string
	status  int
	body    string
	headers map[string]string
}

func (f *fakeLicenseServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type"))
	f.bodies = append(f.bodies, string(b))
	status, body, headers := f.status, f.body, f.headers
	f.mu.Unlock()
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func post(t *testing.T, h http.Handler, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct{ Error, Message string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Message == "" {
		t.Fatalf("not the bot's error JSON: %d %q %v", rec.Code, rec.Body, err)
	}
	return out.Error
}

// Only core may call; the body goes to the server's /v1/<call> untouched,
// and its answer (a token, or a refusal) comes back as it was.
func TestLicenseProxyPassesThrough(t *testing.T) {
	f := &fakeLicenseServer{status: http.StatusOK, body: `{"token":"tok.sig"}`}
	srv := httptest.NewTLSServer(f)
	defer srv.Close()
	p := LicenseProxy{Server: srv.URL, Client: srv.Client(), CoreToken: coreTok}
	activate, refresh := p.Handler(LicenseActivate), p.Handler(LicenseRefresh)

	const req = `{"key":"BOBRES-AAAA-BBBB","install_id":"0b5c","domain":"panel.example.com","version":"1.2.3"}`
	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if rec := post(t, activate, "/internal/license/activate", tok, req); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, rec.Code)
		}
	}
	if len(f.paths) != 0 {
		t.Fatalf("an unauthorized call reached the server: %v", f.paths)
	}
	rec := post(t, activate, "/internal/license/activate", coreTok, req)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"token":"tok.sig"}` || rec.Header().Get("Content-Type") != "application/json" ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("activate: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	if f.paths[0] != "POST /v1/activate application/json" || f.bodies[0] != req {
		t.Fatalf("sent: %v %v", f.paths, f.bodies)
	}
	if rec := post(t, refresh, "/internal/license/refresh", coreTok, `{"token":"old.sig"}`); rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d %q", rec.Code, rec.Body)
	}
	if f.paths[1] != "POST /v1/refresh application/json" || f.bodies[1] != `{"token":"old.sig"}` {
		t.Fatalf("refresh sent: %v %v", f.paths, f.bodies)
	}

	// A refusal passes through with its status (and Retry-After on a 429).
	for status, body := range map[int]string{
		http.StatusForbidden:       `{"error":"revoked","message":"this license has been revoked; ask the vendor"}`,
		http.StatusConflict:        `{"error":"not_active","message":"the license is active on another install now"}`,
		http.StatusTooManyRequests: `{"error":"rate_limited","message":"too many requests; try again later"}`,
	} {
		f.status, f.body, f.headers = status, body, map[string]string{"Retry-After": "60"}
		rec := post(t, activate, "/internal/license/activate", coreTok, req)
		if rec.Code != status || rec.Body.String() != body {
			t.Errorf("%d: %d %q", status, rec.Code, rec.Body)
		}
		if ra := rec.Header().Get("Retry-After"); (status == http.StatusTooManyRequests) != (ra == "60") {
			t.Errorf("%d: Retry-After %q", status, ra)
		}
	}
}

// What the bot answers itself: no server set, a body that is not a small
// JSON object, a server that does not answer, or answers with an error,
// a redirect or something unreadable.
func TestLicenseProxyFailures(t *testing.T) {
	f := &fakeLicenseServer{}
	srv := httptest.NewTLSServer(f)
	defer srv.Close()
	const req = `{"token":"old.sig"}`

	rec := post(t, LicenseProxy{CoreToken: coreTok}.Handler(LicenseRefresh), "/internal/license/refresh", coreTok, req)
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "not_configured" {
		t.Fatalf("no server: %d %q", rec.Code, rec.Body)
	}

	h := LicenseProxy{Server: srv.URL, Client: srv.Client(), CoreToken: coreTok}.Handler(LicenseRefresh)
	for name, body := range map[string]string{
		"not JSON":     "token=old.sig",
		"an array":     `["old.sig"]`,
		"two objects":  `{"token":"a"}{"token":"b"}`,
		"null":         `null`,
		"far too long": `{"token":"` + strings.Repeat("a", maxLicenseRequest) + `"}`,
	} {
		if rec := post(t, h, "/internal/license/refresh", coreTok, body); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "bad_request" {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Body)
		}
	}
	if len(f.paths) != 0 {
		t.Fatalf("a bad body reached the server: %v", f.paths)
	}

	for name, c := range map[string]struct {
		status  int
		body    string
		headers map[string]string
	}{
		"a server error":  {http.StatusInternalServerError, `{"error":"internal","message":"something went wrong"}`, nil},
		"a proxy's page":  {http.StatusBadGateway, `<html>bad gateway</html>`, nil},
		"HTML with 403":   {http.StatusForbidden, `<html>forbidden</html>`, nil},
		"a redirect":      {http.StatusFound, `{}`, map[string]string{"Location": "http://elsewhere.example/v1/refresh"}},
		"a huge answer":   {http.StatusOK, `{"token":"` + strings.Repeat("a", maxLicenseAnswer) + `"}`, nil},
		"an empty answer": {http.StatusOK, ``, nil},
	} {
		f.status, f.body, f.headers = c.status, c.body, c.headers
		if rec := post(t, h, "/internal/license/refresh", coreTok, req); rec.Code != http.StatusBadGateway || errorCode(t, rec) != "server_error" {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Body)
		}
	}
	if n := len(f.paths); n != 6 {
		t.Fatalf("calls: %d %v (a redirect must not be followed)", n, f.paths)
	}

	srv.Close()
	if rec := post(t, h, "/internal/license/refresh", coreTok, req); rec.Code != http.StatusBadGateway || errorCode(t, rec) != "unreachable" {
		t.Fatalf("server down: %d %q", rec.Code, rec.Body)
	}
}

// The default client follows no redirect.
func TestLicenseClientFollowsNoRedirect(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	resp, err := licenseClient().Post(srv.URL+"/v1/refresh", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || hits != 1 {
		t.Fatalf("%d after %d calls", resp.StatusCode, hits)
	}
}
