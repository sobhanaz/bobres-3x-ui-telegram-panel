package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// The license sales' endpoints are the owner's: an admin gets 403 on all of them, the license server's key is
// written once and never read back, and bad input is a field error.
func TestVendorEndpointsAreTheOwners(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}, Secrets: env})
	f.api.cfg.Domain = f.dom

	f.staffUser(8911, "admin")
	adm := f.browser()
	adm.loginWithLink(8911)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/vendor"},
		{http.MethodPut, "/api/v1/vendor/config"},
		{http.MethodGet, "/api/v1/vendor/licenses"},
		{http.MethodPost, "/api/v1/vendor/licenses/00000000-0000-0000-0000-000000000000/grant"},
		{http.MethodPost, "/api/v1/vendor/licenses/00000000-0000-0000-0000-000000000000/revoke"},
	} {
		if code, _ := adm.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("admin %s %s: %d, want 403", c.method, c.path, code)
		}
	}

	// A store that has set nothing up.
	code, out := b.do(http.MethodGet, "/api/v1/vendor", nil)
	if code != http.StatusOK || out["available"] != true || out["configured"] != false || out["on"] != false {
		t.Fatalf("fresh status: %d %v", code, out)
	}
	if tiers, _ := out["tiers"].([]any); len(tiers) != 2 {
		t.Fatalf("tiers: %v", out["tiers"])
	}
	// Bad input names the field.
	priv, _, err := license.NewAdminKey()
	if err != nil {
		t.Fatal(err)
	}
	if code, out = b.do(http.MethodPut, "/api/v1/vendor/config", map[string]any{"admin_key": "nope"}); code != 400 || out["field"] != "admin_key" {
		t.Fatalf("bad key: %d %v", code, out)
	}
	// A good one: configured, and the key is nowhere in the answers.
	code, out = b.do(http.MethodPut, "/api/v1/vendor/config", map[string]any{"admin_key": priv})
	if code != http.StatusOK || out["configured"] != true {
		t.Fatalf("set: %d %v", code, out)
	}
	_, _, raw := b.get("/api/v1/vendor")
	if body := string(raw); strings.Contains(body, priv) || strings.Contains(body, "admin_key") {
		t.Fatalf("the status carries the key: %s", raw)
	}
	// Nothing sold yet; the listing refuses a bad filter.
	code, out = b.do(http.MethodGet, "/api/v1/vendor/licenses", nil)
	if code != http.StatusOK || out["total"] != float64(0) {
		t.Fatalf("list: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodGet, "/api/v1/vendor/licenses?status=gone", nil); code != http.StatusBadRequest {
		t.Fatalf("bad status filter: %d", code)
	}
}
