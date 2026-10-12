package web

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// fakeLicenseServer is the license server behind the bot.
type fakeLicenseServer struct {
	mu   sync.Mutex
	sent []domain.LicenseActivation
	tok  string
	err  error
}

func (f *fakeLicenseServer) ActivateLicense(_ context.Context, a domain.LicenseActivation) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, a)
	return f.tok, f.err
}

func (f *fakeLicenseServer) RefreshLicense(context.Context, string) (string, error) {
	return "", &domain.LicenseServerError{Status: 500, Code: "unused"}
}

const webKey = "BOBRES-AAAA-BBBB-CCCC"

// POST /api/v1/license/activate: the owner only; the server's refusal is a
// 400 on the key with its code, a server that cannot be reached a 502 (503
// when none is set), and a license that does not verify here a 400 on the
// token, as for a pasted one.
func TestLicenseActivate(t *testing.T) {
	f, b, sign := licenseFixture(t)
	f.api.probes = newLimiter(100, time.Minute)
	body := map[string]any{"key": webKey, "reason": "bought pro"}

	f.staffUser(8903, "admin")
	adm := f.browser()
	adm.loginWithLink(8903)
	if code, _ := adm.do(http.MethodPost, "/api/v1/license/activate", body); code != http.StatusForbidden {
		t.Fatalf("admin: %d", code)
	}

	// No bot to reach the server through.
	code, out := b.do(http.MethodPost, "/api/v1/license/activate", body)
	if code != http.StatusServiceUnavailable || out["error"] != "unavailable" || out["code"] != domain.LicenseNotConfigured {
		t.Fatalf("no bot: %d %v", code, out)
	}
	fake := &fakeLicenseServer{}
	f.api.cfg.LicenseServer = fake
	if code, out = b.do(http.MethodPost, "/api/v1/license/activate", map[string]any{"key": " ", "reason": "x"}); code != 400 || out["field"] != "key" {
		t.Fatalf("no key: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPost, "/api/v1/license/activate", map[string]any{"key": webKey}); code != 400 || out["error"] != "invalid" {
		t.Fatalf("no reason: %d %v", code, out)
	}
	for name, c := range map[string]struct {
		err        error
		status     int
		errCode    string
		code       string
		field, msg string
	}{
		"a wrong key": {&domain.LicenseServerError{Status: 403, Code: "invalid_key", Message: "this license key is not valid"},
			400, "refused", "invalid_key", "key", "license server: this license key is not valid"},
		"too many activations": {&domain.LicenseServerError{Status: 409, Code: "reactivation_limit", Message: "moved too often"},
			400, "refused", "reactivation_limit", "key", "license server: moved too often"},
		"slow down": {&domain.LicenseServerError{Status: 429, Code: "rate_limited", Message: "too many requests"},
			429, "rate_limited", "rate_limited", "", "license server: too many requests"},
		"no answer": {&domain.LicenseServerError{Status: 502, Code: domain.LicenseUnreachable, Message: "the license server did not answer"},
			502, "unavailable", domain.LicenseUnreachable, "", "the license server did not answer"},
		"not set on the bot": {&domain.LicenseServerError{Status: 503, Code: domain.LicenseNotConfigured, Message: "no license server is set"},
			503, "unavailable", domain.LicenseNotConfigured, "", "no license server is set"},
	} {
		fake.err = c.err
		code, out := b.do(http.MethodPost, "/api/v1/license/activate", body)
		field, _ := out["field"].(string)
		if code != c.status || out["error"] != c.errCode || out["code"] != c.code || field != c.field || out["message"] != c.msg {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	fake.err = nil
	fake.tok = sign(func(t *license.Token) { t.Domain = "shop.example.org" })
	if code, out = b.do(http.MethodPost, "/api/v1/license/activate", body); code != 400 || out["field"] != "token" || out["code"] != "domain" {
		t.Fatalf("a token for another domain: %d %v", code, out)
	}
	if _, out = b.do(http.MethodGet, "/api/v1/license", nil); out["state"] != "missing" || out["source"] != nil {
		t.Fatalf("a refused activation saved something: %v", out)
	}

	ends := time.Now().Add(300 * 24 * time.Hour).Truncate(time.Second)
	raw := sign(func(t *license.Token) { t.LicenseEndsAt = &ends })
	fake.tok = raw
	code, out = b.do(http.MethodPost, "/api/v1/license/activate", map[string]any{"key": " " + webKey + "\n", "reason": "bought pro"})
	lic := obj(out["license"])
	if code != 200 || out["state"] != "active" || out["source"] != "activation" || out["refreshed_at"] != nil ||
		lic["license_id"] != "lic_7" || lic["license_ends_at"] != float64(ends.Unix()) || obj(out["updated_by"])["id"] == nil {
		t.Fatalf("activate: %d %v", code, out)
	}
	if strings.Contains(asJSON(out), raw) || strings.Contains(asJSON(out), webKey) {
		t.Fatal("the token or the key came back")
	}
	if last := fake.sent[len(fake.sent)-1]; last.Key != webKey || last.Domain != "panel.example.com" || last.InstallID != out["install_id"] {
		t.Fatalf("sent: %+v", last)
	}
	e := lastAudit(t, b, "action=license.set")
	if e["entity_id"] != "lic_7" || e["reason"] != "bought pro" || strings.Contains(asJSON(e), raw) || strings.Contains(asJSON(e), webKey) ||
		!strings.Contains(asJSON(e), `"source":"activation"`) {
		t.Fatalf("audit: %v", e)
	}
	if _, out = b.do(http.MethodGet, "/api/v1/license", nil); out["source"] != "activation" || obj(out["license"])["license_ends_at"] != float64(ends.Unix()) {
		t.Fatalf("GET after activation: %v", out)
	}
	// A pasted token says manual and has no end of its own.
	if code, out = b.do(http.MethodPut, "/api/v1/license", map[string]any{"token": sign(nil), "reason": "pasted"}); code != 200 ||
		out["source"] != "manual" || obj(out["license"])["license_ends_at"] != nil {
		t.Fatalf("pasted: %d %v", code, out)
	}

	// At most 10 tries a minute per staff member.
	f.api.probes = newLimiter(1, time.Minute)
	b.do(http.MethodPost, "/api/v1/license/activate", body)
	if code, out = b.do(http.MethodPost, "/api/v1/license/activate", body); code != http.StatusTooManyRequests || out["error"] != "rate_limited" {
		t.Fatalf("limit: %d %v", code, out)
	}
}
