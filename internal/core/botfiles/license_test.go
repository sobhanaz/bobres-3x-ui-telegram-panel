package botfiles

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/runner"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

const coreTok = "core-token-0123456789abcdef0123"

// licenseStack is core's client, the bot's real pass-through and a fake
// license server behind it; answer sets the server's status and body, sent
// returns what it was sent.
func licenseStack(t *testing.T) (*Client, func(int, string), func() []string, func()) {
	t.Helper()
	var (
		status = http.StatusOK
		body   = `{}`
		sent   []string
	)
	vendor := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sent = append(sent, r.URL.Path+" "+string(b))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(vendor.Close)
	p := runner.LicenseProxy{Server: vendor.URL, Client: vendor.Client(), CoreToken: coreTok}
	mux := http.NewServeMux()
	mux.Handle("POST /internal/license/activate", p.Handler(runner.LicenseActivate))
	mux.Handle("POST /internal/license/refresh", p.Handler(runner.LicenseRefresh))
	bot := httptest.NewServer(mux)
	t.Cleanup(bot.Close)
	return New(bot.URL, coreTok), func(s int, b string) { status, body = s, b }, func() []string { return sent }, bot.Close
}

// Activate and refresh go through the bot to the license server and come
// back as the token, or as the server's refusal.
func TestLicenseThroughTheBot(t *testing.T) {
	c, answer, sent, botDown := licenseStack(t)
	ctx := context.Background()
	answer(http.StatusOK, `{"token":"new.sig"}`)
	a := domain.LicenseActivation{Key: "BOBRES-7K2Q-9XMV", InstallID: "0b5c", Domain: "panel.example.com", Version: "1.2.3"}
	tok, err := c.ActivateLicense(ctx, a)
	if err != nil || tok != "new.sig" {
		t.Fatalf("activate: %q %v", tok, err)
	}
	var got domain.LicenseActivation
	path, body, _ := strings.Cut(sent()[0], " ")
	if err := json.Unmarshal([]byte(body), &got); err != nil || path != "/v1/activate" || got != a {
		t.Fatalf("sent %s %s", path, body)
	}
	if tok, err := c.RefreshLicense(ctx, "old.sig"); err != nil || tok != "new.sig" {
		t.Fatalf("refresh: %q %v", tok, err)
	}
	if want := `/v1/refresh {"token":"old.sig","version":"` + version.Version + `"}`; sent()[1] != want {
		t.Fatalf("refresh sent %s", sent()[1])
	}

	for name, c2 := range map[string]struct {
		status     int
		body       string
		code       string
		wantStatus int
		refused    bool
		wantInMsg  string
		wrongTk    bool
	}{
		"a wrong key":   {http.StatusForbidden, `{"error":"invalid_key","message":"this license key is not valid"}`, "invalid_key", 403, true, "not valid", false},
		"moved away":    {http.StatusConflict, `{"error":"not_active","message":"active on another install"}`, "not_active", 409, true, "another install", false},
		"slow down":     {http.StatusTooManyRequests, `{"error":"rate_limited","message":"too many requests"}`, "rate_limited", 429, false, "too many", false},
		"server broken": {http.StatusInternalServerError, `{"error":"internal","message":"x"}`, domain.LicenseServerFailed, 502, false, "500", false},
		"no token":      {http.StatusOK, `{"token":""}`, domain.LicenseServerFailed, 502, false, "200", false},
		"wrong token":   {http.StatusOK, `{"token":"new.sig"}`, domain.LicenseServerFailed, 502, false, "401", true},
	} {
		answer(c2.status, c2.body)
		client := c
		if c2.wrongTk {
			client = New(c.base, "wrong-token-0123456789abcdef012")
		}
		_, err := client.ActivateLicense(ctx, a)
		var se *domain.LicenseServerError
		if !errors.As(err, &se) || se.Code != c2.code || se.Status != c2.wantStatus || se.Refused() != c2.refused ||
			!strings.Contains(se.Message, c2.wantInMsg) {
			t.Errorf("%s: %v", name, err)
		}
		if err != nil && strings.Contains(err.Error(), a.Key) {
			t.Errorf("%s: the key is in the error: %v", name, err)
		}
	}

	botDown()
	var se *domain.LicenseServerError
	if _, err := c.RefreshLicense(ctx, "old.sig"); !errors.As(err, &se) || se.Code != domain.LicenseUnreachable || se.Status != 502 ||
		strings.Contains(err.Error(), "old.sig") {
		t.Fatalf("bot down: %v", err)
	}
}

// A bot without a license server set says so.
func TestLicenseNotConfigured(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("POST /internal/license/activate", runner.LicenseProxy{CoreToken: coreTok}.Handler(runner.LicenseActivate))
	bot := httptest.NewServer(mux)
	defer bot.Close()
	_, err := New(bot.URL, coreTok).ActivateLicense(context.Background(), domain.LicenseActivation{Key: "BOBRES-1"})
	var se *domain.LicenseServerError
	if !errors.As(err, &se) || se.Code != domain.LicenseNotConfigured || se.Status != 503 || se.Refused() {
		t.Fatalf("%v", err)
	}
	// An older bot without the endpoint.
	_, err = New(bot.URL, coreTok).RefreshLicense(context.Background(), "old.sig")
	if !errors.As(err, &se) || se.Code != domain.LicenseServerFailed || !strings.Contains(se.Message, "404") && !strings.Contains(se.Message, "405") {
		t.Fatalf("older bot: %v", err)
	}
}

func TestClip(t *testing.T) {
	if got := clip("سلام", 3); got != "س" {
		t.Fatalf("%q", got)
	}
	if got := clip("abc", 5); got != "abc" {
		t.Fatalf("%q", got)
	}
}
