package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The lookup takes the first service that answers 200 with a public
// address and nothing else; a long, private, redirected or failed answer
// moves on to the next.
func TestIPLookup(t *testing.T) {
	answer := func(status int, body string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	redirect := httptest.NewServer(http.RedirectHandler("https://example.com/", http.StatusFound))
	t.Cleanup(redirect.Close)
	good := answer(200, " 1.2.3.4\n")
	for _, tc := range []struct {
		name     string
		services []string
		want     string
	}{
		{"the first", []string{good, answer(200, "5.6.7.8")}, "1.2.3.4"},
		{"IPv6 in canonical form", []string{answer(200, "2A01:4F8:0:0::1")}, "2a01:4f8::1"},
		{"a failure, then the second", []string{answer(500, "1.2.3.5"), good}, "1.2.3.4"},
		{"a private address, then the second", []string{answer(200, "10.0.0.7"), good}, "1.2.3.4"},
		{"CGNAT, then the second", []string{answer(200, "100.64.0.9"), good}, "1.2.3.4"},
		{"a long answer, then the second", []string{answer(200, strings.Repeat(" ", 64)+"1.2.3.6"), good}, "1.2.3.4"},
		{"a redirect, then the second", []string{redirect.URL, good}, "1.2.3.4"},
		{"not an address, then the second", []string{answer(200, "<html>"), good}, "1.2.3.4"},
	} {
		got, err := IPLookup{Services: tc.services}.Lookup(context.Background())
		if err != nil || got != tc.want {
			t.Errorf("%s: %q %v", tc.name, got, err)
		}
	}
	if _, err := (IPLookup{Services: []string{answer(200, "10.0.0.7"), answer(404, "")}}).Lookup(context.Background()); err == nil {
		t.Error("no usable answer passed")
	}
}

// Only core may ask; the answer is {"ip"} or a 502.
func TestPublicIPHandler(t *testing.T) {
	ok := PublicIP(func(context.Context) (string, error) { return "1.2.3.4", nil }, "core-token-0123456789", nil)
	failing := PublicIP(func(context.Context) (string, error) { return "", errors.New("no") }, "core-token-0123456789", nil)
	call := func(h http.Handler, token string) (int, map[string]string) {
		req := httptest.NewRequest(http.MethodGet, "/internal/public-ip", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, _ := call(ok, ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := call(ok, "another-token"); code != http.StatusUnauthorized {
		t.Fatalf("another token: %d", code)
	}
	if code, out := call(ok, "core-token-0123456789"); code != 200 || out["ip"] != "1.2.3.4" {
		t.Fatalf("ok: %d %v", code, out)
	}
	if code, out := call(failing, "core-token-0123456789"); code != http.StatusBadGateway || out["error"] != "unavailable" {
		t.Fatalf("failing: %d %v", code, out)
	}
}
