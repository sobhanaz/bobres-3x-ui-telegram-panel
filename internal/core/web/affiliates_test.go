package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hosted"
)

const webHostedToken = "bm_fakeMetricsTokenFakeMetricsTokenFake0123" //nolint:gosec // made up //gitleaks:allow

type stubStores struct{ tokens []string }

func (s *stubStores) FetchHostedMetrics(_ context.Context, _, token string) (int, []byte, error) {
	s.tokens = append(s.tokens, token)
	return 200, []byte("bobres_build_info{version=\"0.20.1\"} 1\nbobres_service_up{service=\"core\"} 1\n"), nil
}

// The Affiliates and Hosted stores pages are the owner's: an admin gets 403; the settings round-trip; a hosted store is registered from a
// code and its metrics token never comes back; it is read through the bot.
func TestAffiliatesAndHostedEndpoints(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	if _, err := f.st.DB().Exec(context.Background(), `TRUNCATE core.hosted_stores`); err != nil {
		t.Fatal(err)
	}
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramIDs: []int64{ownerTG}, Secrets: env, PrivateFeaturesOverride: &granted})
	stores := &stubStores{}
	f.dom.SetHostedTransport(stores)
	f.api.cfg.Domain = f.dom

	f.staffUser(8921, "admin")
	adm := f.browser()
	adm.loginWithLink(8921)
	const anyID = "00000000-0000-0000-0000-000000000000"
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/affiliates"},
		{http.MethodPut, "/api/v1/affiliates/settings"},
		{http.MethodGet, "/api/v1/affiliates/" + anyID},
		{http.MethodPut, "/api/v1/affiliates/" + anyID},
		{http.MethodPost, "/api/v1/affiliates/" + anyID + "/payouts"},
		{http.MethodPost, "/api/v1/vendor/licenses/" + anyID + "/refund"},
		{http.MethodGet, "/api/v1/hosted"},
		{http.MethodPost, "/api/v1/hosted"},
		{http.MethodPut, "/api/v1/hosted/" + anyID},
		{http.MethodDelete, "/api/v1/hosted/" + anyID},
		{http.MethodPost, "/api/v1/hosted/" + anyID + "/check"},
		{http.MethodPost, "/api/v1/hosted/" + anyID + "/license"},
	} {
		if code, _ := adm.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("admin %s %s: %d, want 403", c.method, c.path, code)
		}
	}

	// The program: off by default, 20%, no minimum; it runs only where licenses are sold.
	code, out := b.do(http.MethodGet, "/api/v1/affiliates", nil)
	set, _ := out["settings"].(map[string]any)
	if code != http.StatusOK || set["enabled"] != false || set["percent"] != float64(20) || set["on"] != false || out["total"] != float64(0) {
		t.Fatalf("fresh: %d %v", code, out)
	}
	code, out = b.do(http.MethodPut, "/api/v1/affiliates/settings", map[string]any{"enabled": true, "percent": 25, "min_payout": 100000})
	if code != http.StatusOK || out["enabled"] != true || out["percent"] != float64(25) || out["min_payout"] != float64(100000) || out["vendor_on"] != false {
		t.Fatalf("save: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPut, "/api/v1/affiliates/settings", map[string]any{"enabled": true, "percent": 0}); code != 400 || out["field"] != "percent" {
		t.Fatalf("bad percent: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodGet, "/api/v1/affiliates/"+anyID, nil); code != http.StatusNotFound {
		t.Fatalf("unknown affiliate: %d", code)
	}
	if code, _ = b.do(http.MethodPost, "/api/v1/vendor/licenses/"+anyID+"/refund", map[string]any{"reason": "x"}); code != http.StatusNotFound {
		t.Fatalf("refund of an unknown license: %d", code)
	}

	// A hosted store from a registration code.
	reg, err := hosted.Registration{Customer: "Acme VPN", Domain: "shop.example.com", Version: "0.20.0", MetricsToken: webHostedToken}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	code, out = b.do(http.MethodPost, "/api/v1/hosted", map[string]any{"registration": reg, "notes": "pilot"})
	if code != http.StatusCreated || out["domain"] != "shop.example.com" || out["has_token"] != true || out["channel"] != "stable" {
		t.Fatalf("add: %d %v", code, out)
	}
	id, _ := out["id"].(string)
	if code, out = b.do(http.MethodPost, "/api/v1/hosted", map[string]any{"registration": "nonsense"}); code != 400 || out["field"] != "registration" {
		t.Fatalf("bad code: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/hosted/"+id+"/check", nil)
	if code != http.StatusOK || out["up"] != true || out["last_version"] != "0.20.1" {
		t.Fatalf("check: %d %v", code, out)
	}
	if len(stores.tokens) != 1 || stores.tokens[0] != webHostedToken {
		t.Fatalf("token given to the bot: %v", stores.tokens)
	}
	_, _, raw := b.get("/api/v1/hosted")
	if strings.Contains(string(raw), webHostedToken) || strings.Contains(string(raw), "metrics_token_enc") {
		t.Fatalf("the list carries the token: %s", raw)
	}
	code, out = b.do(http.MethodPut, "/api/v1/hosted/"+id, map[string]any{"customer": "Acme", "notes": "moved", "clear_token": true})
	if code != http.StatusOK || out["has_token"] != false || out["customer"] != "Acme" {
		t.Fatalf("update: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodDelete, "/api/v1/hosted/"+id, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	code, out = b.do(http.MethodGet, "/api/v1/hosted", nil)
	if items, _ := out["items"].([]any); code != http.StatusOK || len(items) != 0 {
		t.Fatalf("after delete: %d %v", code, out)
	}
}
