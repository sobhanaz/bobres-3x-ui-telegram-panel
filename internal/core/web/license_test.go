package web

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// licenseFixture is the owner logged in, with a domain service that trusts
// one vendor key for panel.example.com; sign makes tokens with that key.
func licenseFixture(t *testing.T) (*fixture, *browser, func(func(*license.Token)) string) {
	t.Helper()
	f, b, _, _ := salesFixture(t)
	if _, err := f.st.DB().Exec(context.Background(), `TRUNCATE core.settings`); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f.dom = domain.New(f.st, domain.Config{OwnerTelegramID: ownerTG,
		License: domain.LicenseConfig{Keys: license.KeyRing{"k1": pub}, Domain: "panel.example.com"}})
	f.api.cfg.Domain = f.dom
	f.api.cfg.Servers = &fakeServers{}
	f.api.cfg.Gateways = &fakeGateways{cfg: domain.GatewayConfig{Provider: domain.Zarinpal, Enabled: true}}
	now := time.Now().UTC()
	sign := func(edit func(*license.Token)) string {
		tok := license.Token{KeyID: "k1", LicenseID: "lic_7", Customer: "Shop", Domain: "panel.example.com",
			Tier: license.TierPro, IssuedAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(10*24*time.Hour + time.Minute), GraceDays: 7,
			Entitlements: license.Entitlements{MaxServers: 2, MaxActiveServices: license.Unlimited, DashboardStaffSeat: 0,
				Gateways: []string{"zarinpal"}, UpdatesUntil: "2027-09-30"}}
		if edit != nil {
			edit(&tok)
		}
		raw, err := license.Sign(priv, tok)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	return f, b, sign
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

// The license and System pages: support sees neither, admins read them but
// change nothing, the owner does both.
func TestLicenseAndSystemPermissions(t *testing.T) {
	f, _, sign := licenseFixture(t)
	f.staffUser(8901, "support")
	f.staffUser(8902, "admin")
	sup, adm := f.browser(), f.browser()
	sup.loginWithLink(8901)
	adm.loginWithLink(8902)
	body := map[string]any{"token": sign(nil), "reason": "x"}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/system"},
		{http.MethodGet, "/api/v1/license"},
		{http.MethodPost, "/api/v1/license/check"},
		{http.MethodPut, "/api/v1/license"},
	} {
		if code, _ := sup.do(c.method, c.path, body); code != http.StatusForbidden {
			t.Errorf("support %s %s: %d", c.method, c.path, code)
		}
	}
	for _, path := range []string{"/api/v1/system", "/api/v1/license"} {
		if code, _ := adm.do(http.MethodGet, path, nil); code != http.StatusOK {
			t.Errorf("admin GET %s: %d", path, code)
		}
	}
	for _, c := range []struct{ method, path string }{{http.MethodPost, "/api/v1/license/check"}, {http.MethodPut, "/api/v1/license"}} {
		if code, _ := adm.do(c.method, c.path, body); code != http.StatusForbidden {
			t.Errorf("admin %s %s: %d", c.method, c.path, code)
		}
	}
	if Allowed("admin", PermLicenseWrite) || Allowed("admin", PermSystemWrite) || !Allowed("admin", PermSystemRead) || Allowed("support", PermSystemRead) {
		t.Error("role table")
	}
}

func TestLicensePage(t *testing.T) {
	f, b, sign := licenseFixture(t)
	// Unlicensed: everything works, the footer is not shown.
	code, out := b.do(http.MethodGet, "/api/v1/license", nil)
	if code != 200 || out["state"] != "missing" || out["install_id"] == "" || out["keys"] != 1.0 || out["license"] != nil ||
		out["powered_by"] != false || out["domain"] != "panel.example.com" {
		t.Fatalf("missing: %d %v", code, out)
	}
	if _, me := b.do(http.MethodGet, "/api/v1/me", nil); asJSON(me["license"]) != `{"powered_by":false,"state":"missing","white_label":false}` {
		t.Fatalf("/me: %v", me["license"])
	}
	if _, brand := b.do(http.MethodGet, "/api/v1/brand", nil); brand["powered_by"] != false {
		t.Fatalf("/brand: %v", brand)
	}

	// Checking a token saves nothing and shows what it would be here.
	raw := sign(nil)
	code, out = b.do(http.MethodPost, "/api/v1/license/check", map[string]any{"token": " " + raw + "\n"})
	lic, ent := obj(out["license"]), obj(out["entitlements"])
	if code != 200 || out["state"] != "active" || lic["license_id"] != "lic_7" || lic["tier"] != "pro" || lic["days_left"] != 11.0 ||
		asJSON(out["binding"]) != `{"domain":"match","install_id":"unbound"}` {
		t.Fatalf("check: %d %v", code, out)
	}
	if asJSON(ent["max_servers"]) != `{"full":false,"limit":2,"near":false,"used":0}` ||
		asJSON(ent["dashboard_staff_seats"]) != `{"full":true,"limit":0,"near":false,"used":0}` ||
		asJSON(ent["gateways"]) != `{"enabled":["manual_card","manual_crypto","manual_zarinpal","stars","zarinpal"],"licensed":["zarinpal"]}` ||
		ent["updates_until"] != "2027-09-30" || ent["resellers"] != false {
		t.Fatalf("entitlements: %v", ent)
	}
	if strings.Contains(asJSON(out), raw) {
		t.Fatal("the token came back")
	}
	if _, out = b.do(http.MethodGet, "/api/v1/license", nil); out["state"] != "missing" {
		t.Fatalf("check saved: %v", out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/license/check", map[string]any{"token": sign(func(t *license.Token) { t.Domain = "other.example" })})
	if code != 200 || out["state"] != "invalid" || out["reason"] != "domain" || asJSON(obj(out["binding"])["domain"]) != `"mismatch"` {
		t.Fatalf("check another domain: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPost, "/api/v1/license/check", map[string]any{"token": ""}); code != 400 || out["field"] != "token" {
		t.Fatalf("check empty: %d %v", code, out)
	}

	// Installing: a reason, then only a token that verifies here.
	if code, _ = b.do(http.MethodPut, "/api/v1/license", map[string]any{"token": raw}); code != 400 {
		t.Fatalf("no reason: %d", code)
	}
	code, out = b.do(http.MethodPut, "/api/v1/license", map[string]any{"token": sign(func(t *license.Token) { t.InstallID = "inst_x" }), "reason": "renewal"})
	if code != 400 || out["field"] != "token" || out["code"] != "install_id" || out["error"] != "invalid" {
		t.Fatalf("another install: %d %v", code, out)
	}
	code, out = b.do(http.MethodPut, "/api/v1/license", map[string]any{"token": raw, "reason": "bought pro"})
	if code != 200 || out["state"] != "active" || out["powered_by"] != true || obj(out["updated_by"])["id"] == nil || out["updated_at"] == nil {
		t.Fatalf("put: %d %v", code, out)
	}
	e := lastAudit(t, b, "action=license.set")
	if e["entity_id"] != "lic_7" || e["reason"] != "bought pro" || strings.Contains(asJSON(e), raw) || strings.Contains(asJSON(e), "token") {
		t.Fatalf("audit: %v", e)
	}
	if _, me := b.do(http.MethodGet, "/api/v1/me", nil); asJSON(me["license"]) != `{"powered_by":true,"state":"active","white_label":false}` {
		t.Fatalf("/me licensed: %v", me["license"])
	}
	// A white-label license hides the footer on the login page too.
	if code, _ = b.do(http.MethodPut, "/api/v1/license", map[string]any{"token": sign(func(t *license.Token) { t.Entitlements.WhiteLabel = true }), "reason": "white label"}); code != 200 {
		t.Fatalf("white label: %d", code)
	}
	if _, brand := f.browser().do(http.MethodGet, "/api/v1/brand", nil); brand["powered_by"] != false {
		t.Fatalf("/brand white label: %v", brand)
	}

	// The license gates new staff (0 seats): 409 naming the gate.
	cust := f.customer(8950, "helper")
	code, out = b.do(http.MethodPost, "/api/v1/staff", map[string]any{"user_id": cust.ID, "role": "support", "reason": "help"})
	if code != http.StatusConflict || out["error"] != "state" || out["license"] != "dashboard_staff_seats" {
		t.Fatalf("staff gate: %d %v", code, out)
	}
}

// GET /api/v1/system: every service asked at once (fake peers here), the
// servers and the payment methods, with a version mismatch flagged.
func TestSystemPage(t *testing.T) {
	f, b, _ := licenseFixture(t)
	peer := func(version string, ready bool) *httptest.Server {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": version})
		})
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
			check := "ok"
			if !ready {
				check = "fail"
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"checks": map[string]string{"db": check}})
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return srv
	}
	core, bot, pay := peer("v1.5.0", true), peer("v1.5.0", false), peer("v1.4.2", true)
	f.api.cfg.System = syshealth.New("v1.5.0", []syshealth.Peer{
		{Name: "core", URL: core.URL}, {Name: "bot", URL: bot.URL}, {Name: "payments", URL: pay.URL},
		{Name: "provisioner", URL: ""}, {Name: "backup", URL: "", Optional: true},
	})
	fs := f.api.cfg.Servers.(*fakeServers)
	fs.servers = append(fs.servers, domain.Server{ID: "s1", Name: "DE", Enabled: true, Healthy: false, CheckedAt: 1, HealthError: "dial tcp 10.0.0.5"})

	code, out := b.do(http.MethodGet, "/api/v1/system", nil)
	if code != 200 || out["version"] != "v1.5.0" || out["version_mismatch"] != true || out["checked_at"] == nil {
		t.Fatalf("system: %d %v", code, out)
	}
	services := map[string]map[string]any{}
	for _, s := range out["services"].([]any) {
		services[obj(s)["name"].(string)] = obj(s)
	}
	for name, want := range map[string]string{"core": "ok", "bot": "not_ready", "payments": "ok", "provisioner": "not_configured", "backup": "not_installed"} {
		if services[name]["status"] != want {
			t.Errorf("%s: %v", name, services[name])
		}
	}
	if services["payments"]["version_mismatch"] != true || services["bot"]["detail"] != "failing: db" {
		t.Errorf("payments/bot: %v %v", services["payments"], services["bot"])
	}
	servers := out["servers"].([]any)
	if len(servers) != 1 || obj(servers[0])["detail"] != "dial tcp 10.0.0.5" || obj(out["panel"])["healthy"] != false {
		t.Fatalf("servers (owner reads the reason): %v %v", out["servers"], out["panel"])
	}
	if gws := out["gateways"].([]any); len(gws) != 5 || obj(gws[0])["provider"] != "zarinpal" {
		t.Fatalf("gateways: %v", gws)
	}

	// Without a health checker or a provisioner the page still answers.
	f.api.cfg.System, f.api.cfg.Servers = nil, nil
	if code, out = b.do(http.MethodGet, "/api/v1/system", nil); code != 200 || asJSON(out["services"]) != "[]" || asJSON(out["servers"]) != "[]" {
		t.Fatalf("bare: %d %v", code, out)
	}
}
