package web

import (
	"context"
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
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}, Secrets: env, PrivateFeaturesOverride: &granted})
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

// stubVendor is the license server behind a transport: it answers by path, keeping what it was sent.
type stubVendor struct {
	status int
	body   string
	paths  []string
}

func (s *stubVendor) PostVendorAdmin(_ context.Context, path, _, _, _ string, _ []byte) (int, []byte, error) {
	s.paths = append(s.paths, path)
	if s.status == 0 {
		s.status = 200
	}
	if s.body == "" {
		return s.status, []byte(`{"id":"lic_abc","customer":"Gift Co","tier":"pro","status":"active","ends_at":"2027-06-30T00:00:00Z","created_at":"2026-10-21T09:00:00Z","grace_days":14,"moves":1}`), nil
	}
	return s.status, []byte(s.body), nil
}

// Every license of the license server: the owner's endpoints, what they refuse, and how the server's answers are told.
func TestVendorServerLicensesEndpoints(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}, Secrets: env, PrivateFeaturesOverride: &granted})
	f.api.cfg.Domain = f.dom
	stub := &stubVendor{}
	f.dom.SetVendorTransport(stub)
	priv, _, err := license.NewAdminKey()
	if err != nil {
		t.Fatal(err)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/vendor/config", map[string]any{"admin_key": priv}); code != http.StatusOK {
		t.Fatalf("config: %d %v", code, out)
	}

	f.staffUser(8921, "admin")
	adm := f.browser()
	adm.loginWithLink(8921)
	const id = "/api/v1/vendor/server-licenses/lic_abc"
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/vendor/server-licenses"}, {http.MethodPost, "/api/v1/vendor/server-licenses"}, {http.MethodGet, id},
		{http.MethodPut, id}, {http.MethodPost, id + "/revoke"}, {http.MethodPost, id + "/rekey"},
	} {
		if code, _ := adm.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("admin %s %s: %d, want 403", c.method, c.path, code)
		}
	}

	// A license id that is not one is a 404 before anything is sent.
	n := len(stub.paths)
	for _, p := range []string{"/api/v1/vendor/server-licenses/x", "/api/v1/vendor/server-licenses/LIC_ABC%2F..", "/api/v1/vendor/server-licenses/lic_" + strings.Repeat("a", 50)} {
		if code, _ := b.do(http.MethodGet, p, nil); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}
	if len(stub.paths) != n {
		t.Fatalf("a bad id reached the license server: %v", stub.paths[n:])
	}

	// A good request: the answer has unix times, and the right admin path was asked.
	stub.body = `{"licenses":[{"id":"lic_abc","customer":"Gift Co","tier":"pro","status":"active","ends_at":"2027-06-30T00:00:00Z","created_at":"2026-10-21T09:00:00Z","active_domain":"shop.example","last_seen_at":"2026-10-21T10:00:00Z","moves":2}]}`
	code, out := b.do(http.MethodGet, "/api/v1/vendor/server-licenses?status=active", nil)
	items, _ := out["items"].([]any)
	if code != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["active_domain"] != "shop.example" || items[0].(map[string]any)["moves"] != float64(2) {
		t.Fatalf("list: %d %v", code, out)
	}
	if stub.paths[len(stub.paths)-1] != "/admin/v1/licenses/list" {
		t.Fatalf("asked %v", stub.paths)
	}
	stub.body = ""
	code, out = b.do(http.MethodPost, "/api/v1/vendor/server-licenses", map[string]any{"customer": "Gift Co", "tier": "pro", "ends_at": "2027-06-30", "request_id": "click-create-1"})
	if code != http.StatusOK || out["id"] != "lic_abc" || out["ends_at"] == nil {
		t.Fatalf("create: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPut, id, map[string]any{"customer": "New", "request_id": "click-upd-0001"}); code != http.StatusOK {
		t.Fatalf("update: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodPost, id+"/revoke", map[string]any{"reason": "test"}); code != http.StatusOK {
		t.Fatalf("revoke: %d", code)
	}

	// What core refuses itself: a missing field, a missing request id.
	if code, out = b.do(http.MethodPost, "/api/v1/vendor/server-licenses", map[string]any{"tier": "pro", "months": 1, "request_id": "click-create-2"}); code != http.StatusBadRequest || out["field"] != "customer" {
		t.Fatalf("no customer: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodPut, id, map[string]any{"customer": "x"}); code != http.StatusBadRequest {
		t.Fatalf("no request id: %d", code)
	}

	// The license server's answers keep their meaning; one that is down is a 502 that can be retried.
	for name, c := range map[string]struct {
		status int
		body   string
		want   int
	}{
		"a refusal of the request": {400, `{"error":"invalid","message":"ends_at is more than 10 years away"}`, 400},
		"no such license":          {404, `{"error":"not_found"}`, 404},
		"a revoked license":        {409, `{"error":"revoked","message":"a revoked license does not change"}`, 409},
		"a key it does not accept": {401, `{"error":"unauthorized"}`, 409},
		"a rate limit":             {429, `{"error":"rate_limited"}`, 409},
		"a server error":           {503, `{"error":"internal"}`, 502},
		"the bot unreachable":      {502, `{"error":"unreachable","message":"the license server did not answer"}`, 502},
	} {
		stub.status, stub.body = c.status, c.body
		if code, out = b.do(http.MethodPut, id, map[string]any{"customer": "x", "request_id": "click-upd-0002"}); code != c.want {
			t.Errorf("%s: %d %v, want %d", name, code, out, c.want)
		}
	}
	// And the answers never carry the admin key.
	_, _, raw := b.get("/api/v1/vendor/server-licenses")
	if strings.Contains(string(raw), priv) {
		t.Fatal("the admin key is in an answer")
	}
}

// granted switches the vendor's own features on in a test (Config.PrivateFeaturesOverride).
var granted = true

// A store whose license does not grant the vendor's own features: every path of them is a 404, the dashboard is not told to
// show them, and the bot's switches read off. An owner of a store that has them is told, and the bot's switches are as set.
func TestPrivateFeaturesAreLockedWithoutTheLicense(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}, Secrets: env}) // a plain license: no override
	f.api.cfg.Domain = f.dom
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/vendor"}, {http.MethodPut, "/api/v1/vendor/config"}, {http.MethodGet, "/api/v1/vendor/licenses"},
		{http.MethodGet, "/api/v1/vendor/server-licenses"}, {http.MethodPost, "/api/v1/vendor/server-licenses"},
		{http.MethodGet, "/api/v1/supplier"}, {http.MethodPut, "/api/v1/supplier/config"}, {http.MethodPost, "/api/v1/supplier/sync"},
		{http.MethodGet, "/api/v1/supplier/products"}, {http.MethodGet, "/api/v1/supplier/orders"},
	} {
		if code, _ := b.do(c.method, c.path, map[string]any{}); code != http.StatusNotFound {
			t.Errorf("owner %s %s: %d, want 404 without the license's private features", c.method, c.path, code)
		}
	}
	if f.dom.PrivateFeatures(context.Background()) {
		t.Fatal("a plain license grants the private features")
	}
	if err := f.dom.SetVendorConfig(context.Background(), nil, "x"); err == nil {
		t.Fatal("core took an admin key without the feature")
	}
	_, _, raw := b.get("/api/v1/me")
	if strings.Contains(string(raw), "private.features") {
		t.Fatalf("the dashboard is told to show the private features: %s", raw)
	}
	// With the feature granted, the owner is told (and only the owner).
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}, Secrets: env, PrivateFeaturesOverride: &granted})
	f.api.cfg.Domain = f.dom
	_, _, raw = b.get("/api/v1/me")
	if !strings.Contains(string(raw), "private.features") {
		t.Fatalf("an owner with the feature is not told: %s", raw)
	}
}
