//go:build integration

package web

// The dashboard's server pages over the real provisioner and a REAL Marzban
// panel (see TestServersEndToEnd, which does the same against the 3x-ui fake):
// the variables are those of the adapter's integration tests,
//
//	MARZBAN_TEST_URL, MARZBAN_TEST_USER, MARZBAN_TEST_PASSWORD, MARZBAN_TEST_ALLOW_PRIVATE=1,
//	MARZBAN_TEST_REQUIRE=1
//
//	go test -tags integration -run MarzbanServerEndToEnd ./internal/core/web/

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/marzban"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func TestMarzbanServerEndToEnd(t *testing.T) {
	baseURL, user, pass := os.Getenv("MARZBAN_TEST_URL"), os.Getenv("MARZBAN_TEST_USER"), os.Getenv("MARZBAN_TEST_PASSWORD")
	if baseURL == "" || user == "" || pass == "" {
		if os.Getenv("MARZBAN_TEST_REQUIRE") == "1" {
			t.Fatal("MARZBAN_TEST_URL, MARZBAN_TEST_USER and MARZBAN_TEST_PASSWORD not set and MARZBAN_TEST_REQUIRE=1")
		}
		t.Skip("MARZBAN_TEST_URL, MARZBAN_TEST_USER and MARZBAN_TEST_PASSWORD not set")
	}
	allowPrivate := os.Getenv("MARZBAN_TEST_ALLOW_PRIVATE") == "1"
	f, b, _, _ := salesFixture(t)
	prov, _, vs := realProvisioner(t)
	f.api.cfg.Servers, f.api.cfg.Provisioner = prov, prov
	ctx := context.Background()
	var opts []xui.Option
	if allowPrivate {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	panel, err := marzban.New(baseURL, user, pass, opts...)
	if err != nil {
		t.Fatal(err)
	}

	// The form's refusals: a wrong password, and the dashboard's address pasted
	// where the panel's own belongs. Neither echoes the password.
	body := map[string]any{"kind": "marzban", "name": "Real Marzban", "base_url": baseURL, "sub_base_url": "https://old.example/sub/",
		"token": user + ":not-the-password", "weight": 100, "enabled": true, "allow_private": allowPrivate, "skip_check": false}
	code, out := b.do(http.MethodPost, "/api/v1/servers", body)
	if msg, _ := out["message"].(string); code != http.StatusConflict || !strings.Contains(msg, "connection test failed") ||
		!strings.Contains(msg, "admin username or password") || strings.Contains(asJSON(out), "not-the-password") {
		t.Fatalf("a wrong password: %d %v", code, out)
	}
	body["base_url"], body["token"] = strings.TrimRight(baseURL, "/")+"/dashboard/", user+":"+pass
	code, out = b.do(http.MethodPost, "/api/v1/servers", body)
	if msg, _ := out["message"].(string); code != http.StatusConflict || !strings.Contains(msg, "without /dashboard/") || strings.Contains(asJSON(out), pass) {
		t.Fatalf("the dashboard's address: %d %v", code, out)
	}

	// Added with the panel's address: tested, listed without the password.
	body["base_url"] = baseURL
	code, out = b.do(http.MethodPost, "/api/v1/servers", body)
	sv := item(out)
	if code != http.StatusCreated || sv["kind"] != "marzban" || sv["healthy"] != true || sv["has_token"] != true ||
		!strings.HasPrefix(sv["panel_version"].(string), "v") || sv["version_untested"] == true {
		t.Fatalf("add: %d %v", code, out)
	}
	t.Logf("registered Marzban %v", sv["panel_version"])
	id := sv["id"].(string)
	code, out = b.do(http.MethodGet, "/api/v1/servers", nil)
	if code != 200 || len(items(out)) != 1 || strings.Contains(asJSON(out), pass) {
		t.Fatalf("list: %d %v", code, out)
	}
	var enc []byte
	if err := vs.DB().QueryRow(ctx, `SELECT api_token_enc FROM provisioner.xui_servers WHERE id = $1`, id).Scan(&enc); err != nil ||
		strings.Contains(string(enc), pass) {
		t.Fatalf("login at rest: %v", err)
	}
	code, out = b.do(http.MethodGet, "/api/v1/servers/"+id+"/inbounds", nil)
	if code != 200 || len(items(out)) == 0 {
		t.Fatalf("inbounds: %d %v", code, out)
	}

	// A plan sends its services to this server; the link is the panel's own
	// subscription token behind the address the server was given.
	days, traffic := int32(30), int64(10<<30)
	p, err := f.dom.UpsertPlan(ctx, owner(t, f), &store.Plan{NameI18n: map[string]string{"en": "Monthly"}, Kind: "both",
		DurationDays: &days, TrafficBytes: &traffic, Price: 0, Currency: "IRT", Enabled: true, ServerIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	u := f.customer(8901, "buyer")
	o, err := f.dom.CreateOrder(ctx, domain.CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "marzban-order-1"})
	if err != nil || o.Status != "paid" {
		t.Fatalf("free order: %v %+v", err, o)
	}
	if worked, err := f.dom.NewProvisionWorker(prov, nil).ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provision: %v %v", worked, err)
	}
	sub, err := f.st.SubscriptionByOrder(ctx, f.st.Conn(), o.ID)
	if err != nil || sub.ServerID != id || !strings.HasPrefix(sub.SubLink, "https://old.example/sub/") || len(sub.SubLink) <= len("https://old.example/sub/") {
		t.Fatalf("delivered: %v %+v", err, sub)
	}
	t.Cleanup(func() { _ = panel.DeleteUser(context.Background(), sub.ClientEmail) })
	pu, err := panel.GetUser(ctx, sub.ClientEmail)
	if err != nil || pu.Status != marzban.StatusActive || pu.DataLimitBytes() != traffic || len(pu.Links) == 0 {
		t.Fatalf("the client on the panel: %+v %v", pu, err)
	}

	// A new subscription address: the stored link follows, and it is still
	// the panel's token (a fresh one: the panel makes a new token on every read).
	body["sub_base_url"], body["token"] = "https://new.example/sub/", ""
	if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, body); code != 200 || item(out)["sub_base_url"] != "https://new.example/sub/" {
		t.Fatalf("edit: %d %v", code, out)
	}
	if n, err := f.dom.NewSubLinkWorker(prov, prov, nil).RefreshStale(ctx); n != 1 || err != nil {
		t.Fatalf("refresh: %d %v", n, err)
	}
	got, _ := f.st.GetSubscription(ctx, f.st.Conn(), sub.ID)
	if !strings.HasPrefix(got.SubLink, "https://new.example/sub/") || len(got.SubLink) <= len("https://new.example/sub/") {
		t.Fatalf("stored link: %q", got.SubLink)
	}

	// Deleting the server is refused while a plan sells on it and while the
	// service is on it; deleting the service takes the client off the panel,
	// and then the server can go.
	if code, out := b.do(http.MethodDelete, "/api/v1/servers/"+id, nil); code != http.StatusConflict || !strings.Contains(out["message"].(string), "plans still send") {
		t.Fatalf("delete with a plan on it: %d %v", code, out)
	}
	p.ServerIDs = []string{}
	if _, err := f.dom.UpsertPlan(ctx, owner(t, f), p); err != nil {
		t.Fatal(err)
	}
	if code, out := b.do(http.MethodDelete, "/api/v1/servers/"+id, nil); code != http.StatusConflict || !strings.Contains(out["message"].(string), "1 clients") {
		t.Fatalf("delete with a client: %d %v", code, out)
	}
	if err := f.dom.DeleteSubscription(ctx, owner(t, f), prov, sub.ID, "marzban end to end"); err != nil {
		t.Fatalf("delete the service: %v", err)
	}
	if _, err := panel.GetUser(ctx, sub.ClientEmail); !marzban.IsNotFound(err) {
		t.Fatalf("the client is still on the panel: %v", err)
	}
	if code, out := b.do(http.MethodDelete, "/api/v1/servers/"+id, nil); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("delete the server: %d %v", code, out)
	}
}
