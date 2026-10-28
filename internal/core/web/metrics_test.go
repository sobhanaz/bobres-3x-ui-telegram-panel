package web

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// get reads a public address the way Prometheus would, with the headers given; xff stands for the client address Caddy sets.
func (f *fixture) get(path, xff string, headers map[string]string) (int, http.Header, string) {
	f.t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.srv.URL+path, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	resp, err := f.srv.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

func bearerHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// /metrics does not exist until the owner makes a token; then only a request that carries it is answered, in the Prometheus
// text format; replacing the token ends the old one at once and turning it off makes the endpoint a 404 again.
func TestMetricsEndpointFollowsItsToken(t *testing.T) {
	m := newMonFixture(t)
	owner := m.login(ownerTG, "owner")

	// Off: a plain 404 whatever is sent, the same as a path that does not exist.
	for _, h := range []map[string]string{nil, bearerHeader("whatever"), {"Authorization": "Basic Zm9vOmJhcg=="}} {
		code, _, body := m.get("/metrics", "", h)
		if code != http.StatusNotFound || !strings.Contains(body, "404") {
			t.Fatalf("off, %v: %d %q", h, code, body)
		}
	}

	code, out := owner.do(http.MethodPost, "/api/v1/metrics-token/generate", nil)
	token, _ := out["token"].(string)
	if code != 200 || out["enabled"] != true || !strings.HasPrefix(token, "bm_") {
		t.Fatalf("generate: %d %v", code, out)
	}

	// On: no credential, a wrong one, another scheme, the token in the address, in a header that is too long: all 401.
	for name, h := range map[string]map[string]string{
		"none": nil, "wrong": bearerHeader("bm_not-the-token"), "empty": bearerHeader(""), "basic": {"Authorization": "Basic " + token},
		"too long": bearerHeader(strings.Repeat("a", 600)), "token alone": {"Authorization": token},
	} {
		code, hdr, _ := m.get("/metrics", "10.0.0.1", h)
		if code != http.StatusUnauthorized || !strings.HasPrefix(hdr.Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: %d %v", name, code, hdr)
		}
	}
	if code, _, _ := m.get("/metrics?token="+token, "10.0.0.1", nil); code != http.StatusUnauthorized {
		t.Errorf("the token in the address: %d", code)
	}

	// The token: the text format, nothing cached, the scheme in any case.
	for _, h := range []map[string]string{bearerHeader(token), {"Authorization": "bearer " + token}, {"Authorization": "  BEARER   " + token + "  "}} {
		code, hdr, body := m.get("/metrics", "10.0.0.2", h)
		if code != 200 || hdr.Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" || hdr.Get("Cache-Control") != "no-store" ||
			hdr.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(body, "# TYPE bobres_build_info gauge\nbobres_build_info{version=\"0.7.0\"} 1\n") {
			t.Fatalf("with the token: %d %v\n%s", code, hdr, body)
		}
	}
	if code, _, _ := m.get("/metrics?x=1", "10.0.0.2", bearerHeader(token)); code != 200 {
		t.Errorf("a query string: %d", code)
	}
	// Only GET and HEAD: a POST is not a scrape.
	resp, err := m.srv.Client().Post(m.srv.URL+"/metrics", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}

	// A new token ends the old one at once.
	_, out = owner.do(http.MethodPost, "/api/v1/metrics-token/generate", nil)
	next := out["token"].(string)
	if code, _, _ := m.get("/metrics", "10.0.0.2", bearerHeader(token)); code != http.StatusUnauthorized {
		t.Errorf("the replaced token: %d", code)
	}
	if code, _, _ := m.get("/metrics", "10.0.0.2", bearerHeader(next)); code != 200 {
		t.Errorf("the new token: %d", code)
	}

	// The owner's own token works the same; turning it off is a 404 again.
	mine := "my-own-token-0123456789-ABCDEFGH"
	if code, out := owner.do(http.MethodPut, "/api/v1/metrics-token", map[string]string{"token": mine}); code != 200 || out["enabled"] != true || out["token"] != nil {
		t.Fatalf("put: %d %v", code, out)
	}
	if code, _, _ := m.get("/metrics", "10.0.0.2", bearerHeader(mine)); code != 200 {
		t.Errorf("the owner's token: %d", code)
	}
	if code, out := owner.do(http.MethodDelete, "/api/v1/metrics-token", nil); code != 200 || out["enabled"] != false {
		t.Fatalf("delete: %d %v", code, out)
	}
	if code, _, _ := m.get("/metrics", "10.0.0.2", bearerHeader(mine)); code != http.StatusNotFound {
		t.Errorf("after turning it off: %d", code)
	}
}

// A monitor that is not wired (an install without one) has no endpoint, token or not.
func TestMetricsEndpointNeedsTheMonitor(t *testing.T) {
	m := newMonFixture(t)
	owner := m.login(ownerTG, "owner")
	_, out := owner.do(http.MethodPost, "/api/v1/metrics-token/generate", nil)
	token := out["token"].(string)
	m.api.cfg.Monitor = nil
	if code, _, _ := m.get("/metrics", "", bearerHeader(token)); code != http.StatusNotFound {
		t.Errorf("no monitor: %d", code)
	}
}

// Wrong tries are counted per client address: the 31st in a minute is told to wait, the right token from the same address is
// still answered, and another address starts from none.
func TestMetricsWrongTriesAreLimited(t *testing.T) {
	m := newMonFixture(t)
	owner := m.login(ownerTG, "owner")
	_, out := owner.do(http.MethodPost, "/api/v1/metrics-token/generate", nil)
	token := out["token"].(string)
	for i := 1; i <= metricsFailsPerMinute; i++ {
		if code, _, _ := m.get("/metrics", "192.0.2.7", bearerHeader("bm_wrong")); code != http.StatusUnauthorized {
			t.Fatalf("try %d: %d", i, code)
		}
	}
	code, hdr, _ := m.get("/metrics", "192.0.2.7", bearerHeader("bm_wrong"))
	if code != http.StatusTooManyRequests || hdr.Get("Retry-After") != "60" {
		t.Fatalf("one too many: %d %v", code, hdr)
	}
	if code, _, _ := m.get("/metrics", "192.0.2.7", bearerHeader(token)); code != 200 {
		t.Errorf("the right token from the same address: %d", code)
	}
	if code, _, _ := m.get("/metrics", "192.0.2.8", bearerHeader("bm_wrong")); code != http.StatusUnauthorized {
		t.Errorf("another address: %d", code)
	}
}

// Making the token is the owner's; admins see only whether it is on; support has no part; every change needs the session's CSRF
// token; the token is shown once, and is nowhere else: not in the settings, the audit log or the next answer.
func TestMetricsTokenEndpointsAndWhatTheyLeak(t *testing.T) {
	m := newMonFixture(t)
	owner := m.login(ownerTG, "owner")
	admin := m.login(7301, "admin")
	support := m.login(7302, "support")

	for name, b := range map[string]*browser{"owner": owner, "admin": admin} {
		if code, out := b.do(http.MethodGet, "/api/v1/metrics-token", nil); code != 200 || out["enabled"] != false {
			t.Errorf("%s state: %d %v", name, code, out)
		}
	}
	if code, _ := support.do(http.MethodGet, "/api/v1/metrics-token", nil); code != http.StatusForbidden {
		t.Errorf("support state: %d", code)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/v1/metrics-token/generate", nil},
		{http.MethodPut, "/api/v1/metrics-token", map[string]string{"token": strings.Repeat("a", 30)}},
		{http.MethodDelete, "/api/v1/metrics-token", nil},
	} {
		for name, b := range map[string]*browser{"admin": admin, "support": support} {
			if code, _ := b.do(c.method, c.path, c.body); code != http.StatusForbidden {
				t.Errorf("%s %s %s: %d", name, c.method, c.path, code)
			}
		}
		anon := m.browser()
		if code, _ := anon.do(c.method, c.path, c.body); code != http.StatusUnauthorized {
			t.Errorf("no session, %s %s: %d", c.method, c.path, code)
		}
	}
	if code, _, _ := m.get("/metrics", "", nil); code != http.StatusNotFound {
		t.Fatalf("a refused change still turned it on: %d", code)
	}

	// The CSRF token: a change without it is refused.
	owner.csrf = ""
	if code, out := owner.do(http.MethodPost, "/api/v1/metrics-token/generate", nil); code != http.StatusForbidden || out["error"] != "csrf" {
		t.Errorf("without the CSRF token: %d %v", code, out)
	}
	owner = m.login(ownerTG, "owner")

	code, out := owner.do(http.MethodPost, "/api/v1/metrics-token/generate", nil)
	token, _ := out["token"].(string)
	if code != 200 || token == "" {
		t.Fatalf("generate: %d %v", code, out)
	}
	code, out = admin.do(http.MethodGet, "/api/v1/metrics-token", nil)
	if code != 200 || out["enabled"] != true || strings.Contains(asJSON(out), token) {
		t.Fatalf("the admin's state: %d %v", code, out)
	}
	// Not in the Settings page, not as a setting that can be written, not in the audit log.
	_, settings := owner.do(http.MethodGet, "/api/v1/settings", nil)
	if strings.Contains(asJSON(settings), "metrics_token") || strings.Contains(asJSON(settings), token) {
		t.Error("the Settings page lists the token")
	}
	if code, out := owner.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"monitor.metrics_token": "abc"}}); code != 400 || out["field"] != "monitor.metrics_token" {
		t.Errorf("writing it as a setting: %d %v", code, out)
	}
	_, audit := owner.do(http.MethodGet, "/api/v1/audit?action=metrics", nil)
	if audit["total"] != float64(1) || strings.Contains(asJSON(audit), token) || !strings.Contains(asJSON(audit), "metrics.token.generate") {
		t.Errorf("the audit log: %v", audit)
	}
	var stored string
	if err := m.st.DB().QueryRow(t.Context(), `SELECT value::text FROM core.settings WHERE key = 'monitor.metrics_token'`).Scan(&stored); err != nil ||
		strings.Contains(stored, token) || len(stored) != 66 {
		t.Errorf("stored %q: %v", stored, err)
	}

	// A token that cannot be used is refused with its field; nothing changes.
	for _, bad := range []string{"", "short", strings.Repeat("a", 129), strings.Repeat("a", 30) + " b", strings.Repeat("a", 30) + "é"} {
		if code, out := owner.do(http.MethodPut, "/api/v1/metrics-token", map[string]string{"token": bad}); code != 400 || out["field"] != "token" {
			t.Errorf("%q: %d %v", bad, code, out)
		}
	}
	if code, _, _ := m.get("/metrics", "10.1.1.1", bearerHeader(token)); code != 200 {
		t.Errorf("a refused change replaced the token: %d", code)
	}
	if code, _ := owner.do(http.MethodPut, "/api/v1/metrics-token", map[string]any{"token": "x", "extra": 1}); code != 400 {
		t.Errorf("an unknown field: %d", code)
	}
}
