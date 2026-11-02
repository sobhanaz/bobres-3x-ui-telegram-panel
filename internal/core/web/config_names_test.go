package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// renamingRegistry is a server registry where the server of sub renames its clients (renames) or not.
func renamingRegistry(sub *store.Subscription, renames bool) *fakeServers {
	return &fakeServers{servers: []domain.Server{{ID: sub.ServerID, Name: "s1", Enabled: true, RenamesClients: renames}}}
}

// Staff rename a service from its page: only where its server keeps the link, with a reason, audited; a name core does not
// take is a 400 with the reason, for the page to say why.
func TestDashboardRenamesAService(t *testing.T) {
	f, b, _, prov := salesFixture(t)
	p := f.plan("Monthly", 30, 50<<30, 120000)
	_, sub := f.order(f.customer(8601, "nima"), p, "active", "active", time.Now().Add(48*time.Hour), 50<<30)
	f.api.cfg.Servers = renamingRegistry(sub, true)

	code, out := b.do(http.MethodGet, "/api/v1/services/"+sub.ID, nil)
	if code != 200 || out["service"].(map[string]any)["renamable"] != true {
		t.Fatalf("service page: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/rename", map[string]any{"name": "nima1234"}); code != 400 || out["error"] != "invalid" {
		t.Fatalf("no reason: %d %v", code, out)
	}
	if code, out = b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/rename", map[string]any{"name": "nima", "reason": "asked"}); code != 400 ||
		out["error"] != "config_name" || out["reason"] != domain.NameMix {
		t.Fatalf("a name without a digit: %d %v", code, out)
	}
	prov.taken = map[string]bool{"taken123": true}
	if code, out = b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/rename", map[string]any{"name": "taken123", "reason": "asked"}); code != 400 || out["reason"] != domain.NameTaken {
		t.Fatalf("a name a panel has: %d %v", code, out)
	}
	code, out = b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/rename", map[string]any{"name": "Nima1234", "reason": "the customer asked"})
	if code != 200 || out["service"].(map[string]any)["client_email"] != "nima1234" || out["service"].(map[string]any)["sub_link"] != sub.SubLink {
		t.Fatalf("rename: %d %v", code, out)
	}
	if !strings.Contains(prov.done(), "rename:"+sub.ID+":nima1234") {
		t.Fatalf("panel calls: %s", prov.done())
	}
	var n int
	if err := f.st.Conn().QueryRow(context.Background(), `SELECT count(*) FROM core.audit_log WHERE action = 'subscription.rename' AND entity_id = $1`, sub.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit: %d %v", n, err)
	}

	// Support may read but not rename (the write permission).
	f.staffUser(8602, "support")
	sup := f.browser()
	sup.loginWithLink(8602)
	if code, _ = sup.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/rename", map[string]any{"name": "abcd1234", "reason": "x"}); code != 403 {
		t.Fatalf("support renamed: %d", code)
	}
	// A server that cannot keep the link (Marzban, S-UI): no Rename, and a 409.
	f.api.cfg.Servers = renamingRegistry(sub, false)
	f.dom.ResetRenameCache()
	if _, out = b.do(http.MethodGet, "/api/v1/services/"+sub.ID, nil); out["service"].(map[string]any)["renamable"] != false {
		t.Fatalf("renamable on a Marzban server: %v", out)
	}
	if code, _ = b.do(http.MethodPost, "/api/v1/services/"+sub.ID+"/rename", map[string]any{"name": "abcd1234", "reason": "x"}); code != 409 {
		t.Fatalf("rename on a Marzban server: %d", code)
	}
}

// The Mini App: the option follows the setting (features.config_names), a name is checked before the order, the order
// carries it, and a service says whether it can be renamed and is renamed from My services.
func TestMiniAppConfigNames(t *testing.T) {
	m := newMiniFixture(t, true)
	tok := m.signIn("tg:8701:sara:en")
	u := m.user(8701)
	p := m.plan("Monthly", 30, 50<<30, 1000)
	_, sub := m.orderSub(u, p, "active", "active", time.Now().Add(48*time.Hour), 50<<30)
	m.api.cfg.Servers = renamingRegistry(sub, true)

	flag := func() any {
		_, me := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil)
		return me["features"].(map[string]any)["config_names"]
	}
	if flag() != false {
		t.Fatal("config names on by default")
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/config-name/check", tok, map[string]any{"name": "sara1234"}); code != 200 || out["ok"] != false || out["reason"] != domain.NameOff {
		t.Fatalf("check while off: %d %v", code, out)
	}
	if _, out := m.call(http.MethodGet, "/api/mini/v1/services", tok, nil); out["services"].([]any)[0].(map[string]any)["renamable"] != false {
		t.Fatalf("renamable while off: %v", out)
	}
	m.setting(domain.SettingConfigNamesCustomers, "true")
	m.dom.ResetRenameCache()
	if flag() != true {
		t.Fatal("config names off after switching them on")
	}
	m.prov.taken = map[string]bool{"taken123": true}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/config-name/check", tok, map[string]any{"name": "taken123"}); code != 200 || out["reason"] != domain.NameTaken {
		t.Fatalf("a taken name: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/config-name/check", tok, map[string]any{"name": " Sara1234 "}); code != 200 || out["ok"] != true || out["name"] != "sara1234" {
		t.Fatalf("a free name: %d %v", code, out)
	}
	// The order carries the name; a name taken meanwhile is refused with its reason.
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": p.ID, "key": "names-order-1", "client_name": "taken123"}); code != 400 ||
		out["error"] != "config_name" || out["reason"] != domain.NameTaken {
		t.Fatalf("an order with a taken name: %d %v", code, out)
	}
	code, out := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": p.ID, "key": "names-order-2", "client_name": "sara1234"})
	if code != 200 {
		t.Fatalf("an order with a name: %d %v", code, out)
	}
	if o, err := m.st.GetOrder(context.Background(), m.st.Conn(), out["id"].(string)); err != nil || o.ClientName == nil || *o.ClientName != "sara1234" {
		t.Fatalf("the order's name: %+v %v", o, err)
	}

	// My services: renamable, and renamed.
	_, out = m.call(http.MethodGet, "/api/mini/v1/services", tok, nil)
	svc := out["services"].([]any)[0].(map[string]any)
	if svc["renamable"] != true || svc["config_name"] != sub.ClientEmail {
		t.Fatalf("service: %v", svc)
	}
	if code, out = m.call(http.MethodPost, "/api/mini/v1/services/"+sub.ID+"/rename", tok, map[string]any{"name": "ab"}); code != 200 || out["ok"] != false || out["reason"] != domain.NameLength {
		t.Fatalf("a bad name: %d %v", code, out)
	}
	code, out = m.call(http.MethodPost, "/api/mini/v1/services/"+sub.ID+"/rename", tok, map[string]any{"name": "mine5678"})
	if code != 200 || out["ok"] != true || out["service"].(map[string]any)["config_name"] != "mine5678" || out["service"].(map[string]any)["link"] != sub.SubLink {
		t.Fatalf("rename: %d %v", code, out)
	}
	// Someone else's service does not exist for this customer.
	other := m.signIn("tg:8702:omid:en")
	if code, _ = m.call(http.MethodPost, "/api/mini/v1/services/"+sub.ID+"/rename", other, map[string]any{"name": "abcd1234"}); code != 404 {
		t.Fatalf("another customer's service: %d", code)
	}
}

// The two switches are a card of the Settings page (group config_names): listed, off by default, and saved there, so
// the owner can turn config names on from the dashboard.
func TestConfigNamesSettingsCard(t *testing.T) {
	f, b, _ := setupFixture(t)
	t.Cleanup(func() {
		if err := testdb.Truncate(context.Background(), f.st.DB(), `core.settings`); err != nil {
			t.Error(err)
		}
	})
	code, out := b.do(http.MethodGet, "/api/v1/settings", nil)
	if code != 200 || !strings.Contains(asJSON(out["groups"]), `"config_names"`) {
		t.Fatalf("groups: %d %v", code, out["groups"])
	}
	for _, key := range []string{domain.SettingConfigNamesCustomers, domain.SettingConfigNamesResellers} {
		if it := settingByKey(out, key); it == nil || it["group"] != "config_names" || it["kind"] != "bool" || it["default"] != "false" || it["editable"] != true {
			t.Errorf("%s: %v", key, it)
		}
	}
	code, out = b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{
		domain.SettingConfigNamesCustomers: "true", domain.SettingConfigNamesResellers: "true"}})
	if code != 200 || settingByKey(out, domain.SettingConfigNamesCustomers)["value"] != "true" || settingByKey(out, domain.SettingConfigNamesResellers)["value"] != "true" {
		t.Fatalf("put: %d %v", code, out)
	}
}
