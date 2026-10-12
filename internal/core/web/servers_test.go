package web

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeServers is the provisioner's registry: it keeps the tokens it is given
// and never answers one. failNext makes the next write fail with that error.
type fakeServers struct {
	mu       sync.Mutex
	servers  []domain.Server
	tokens   map[string]string
	inbounds map[int32]bool
	probes   []domain.ServerProbe
	failNext error
	// failUpdate makes every UpdateServer fail.
	failUpdate error
	down       bool
	next       int
}

func (f *fakeServers) fail() error {
	if f.down {
		return status.Error(codes.Unavailable, "provisioner down")
	}
	err := f.failNext
	f.failNext = nil
	return err
}

func (f *fakeServers) ListServers(context.Context) (*domain.ServerList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, fmt.Errorf("provisioner.ListServers: %w", status.Error(codes.Unavailable, "down"))
	}
	return &domain.ServerList{Servers: slices.Clone(f.servers), TestedMin: "v3.8.5", TestedMax: "v3.9.0"}, nil
}

func (f *fakeServers) AddServer(_ context.Context, in domain.ServerInput) (*domain.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, fmt.Errorf("provisioner.AddServer: %w", err)
	}
	f.next++
	sv := domain.Server{ID: fmt.Sprintf("00000000-0000-7000-8000-%012d", f.next), Name: in.Name, BaseURL: in.BaseURL,
		SubBaseURL: in.SubBaseURL, Enabled: in.Enabled, AllowPrivate: in.AllowPrivate, Weight: in.Weight, Managed: "dashboard",
		HasToken: true, PanelVersion: "v3.9.0", Healthy: true, CheckedAt: 1_800_000_000}
	if f.tokens == nil {
		f.tokens = map[string]string{}
	}
	f.tokens[sv.ID] = in.Token
	f.servers = append(f.servers, sv)
	return &sv, nil
}

func (f *fakeServers) UpdateServer(_ context.Context, id string, in domain.ServerInput) (*domain.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, fmt.Errorf("provisioner.UpdateServer: %w", err)
	}
	if f.failUpdate != nil {
		return nil, fmt.Errorf("provisioner.UpdateServer: %w", f.failUpdate)
	}
	for i := range f.servers {
		if sv := &f.servers[i]; sv.ID == id {
			sv.Name, sv.BaseURL, sv.SubBaseURL, sv.Enabled, sv.AllowPrivate, sv.Weight =
				in.Name, in.BaseURL, in.SubBaseURL, in.Enabled, in.AllowPrivate, in.Weight
			if in.Token != "" {
				f.tokens[id] = in.Token
			}
			out := *sv
			return &out, nil
		}
	}
	return nil, status.Error(codes.NotFound, "3x-ui server not found")
}

func (f *fakeServers) DeleteServer(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return fmt.Errorf("provisioner.DeleteServer: %w", err)
	}
	for i, sv := range f.servers {
		if sv.ID == id {
			if sv.Clients > 0 {
				return fmt.Errorf("provisioner.DeleteServer: %w",
					status.Errorf(codes.FailedPrecondition, "the server still has %d clients: delete or move them first", sv.Clients))
			}
			f.servers = slices.Delete(f.servers, i, i+1)
			return nil
		}
	}
	return status.Error(codes.NotFound, "3x-ui server not found")
}

func (f *fakeServers) TestServer(_ context.Context, p domain.ServerProbe) (*domain.ServerTest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, fmt.Errorf("provisioner.TestServer: %w", err)
	}
	f.probes = append(f.probes, p)
	if p.Token == "wrong" {
		return &domain.ServerTest{Detail: "the panel refused the API token"}, nil
	}
	return &domain.ServerTest{OK: true, PanelVersion: "v3.9.0", Inbounds: 2}, nil
}

func (f *fakeServers) ListInbounds(_ context.Context, id string) ([]domain.Inbound, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, err
	}
	attach := func(id int32) bool { a, ok := f.inbounds[id]; return !ok || a }
	return []domain.Inbound{
		{ID: 1, Tag: "vless-443", Protocol: "vless", Port: 443, Enabled: true, Attach: attach(1)},
		{ID: 2, Tag: "vmess-80", Protocol: "vmess", Port: 80, Enabled: false, Attach: attach(2)},
	}, nil
}

func (f *fakeServers) SetInboundAttach(_ context.Context, _ string, inbound int32, attach bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return err
	}
	if f.inbounds == nil {
		f.inbounds = map[int32]bool{}
	}
	f.inbounds[inbound] = attach
	return nil
}

// serversFixture logs the owner in with a fake provisioner registry.
func serversFixture(t *testing.T) (*fixture, *browser, *fakeServers) {
	t.Helper()
	f, b, _, _ := salesFixture(t)
	fs := &fakeServers{}
	f.api.cfg.Servers = fs
	return f, b, fs
}

const panelToken = "panel-api-token-SECRET-1234" //nolint:gosec // test fixture

// The Servers page is owner and admin only: support gets 403 on every call.
func TestServersPermissions(t *testing.T) {
	f, _, _ := serversFixture(t)
	f.staffUser(8701, "support")
	sup := f.browser()
	sup.loginWithLink(8701)
	id := "00000000-0000-7000-8000-000000000001"
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/servers"},
		{http.MethodPost, "/api/v1/servers"},
		{http.MethodPost, "/api/v1/servers/test"},
		{http.MethodPut, "/api/v1/servers/" + id},
		{http.MethodDelete, "/api/v1/servers/" + id},
		{http.MethodGet, "/api/v1/servers/" + id + "/inbounds"},
		{http.MethodPut, "/api/v1/servers/" + id + "/inbounds/1"},
	} {
		if code, _ := sup.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("support %s %s: %d", c.method, c.path, code)
		}
	}
	if !Allowed("admin", PermServersWrite) || Allowed("support", PermServersWrite) {
		t.Fatal("servers.write: owner and admin")
	}
	// Without a provisioner the page says so.
	f.api.cfg.Servers = nil
	b := f.browser()
	b.loginWithLink(ownerTG)
	if code, out := b.do(http.MethodGet, "/api/v1/servers", nil); code != http.StatusServiceUnavailable || out["error"] != "unavailable" {
		t.Fatalf("no provisioner: %d %v", code, out)
	}
}

// The whole page against the registry: the token goes in and never comes
// back, in an answer or in the audit log.
func TestServersAPI(t *testing.T) {
	f, b, fs := serversFixture(t)
	body := map[string]any{"name": "Germany 1", "base_url": "https://de.example.com:2053/p", "sub_base_url": "https://sub.de.example.com/sub/",
		"token": panelToken, "weight": 0, "enabled": true, "allow_private": false, "skip_check": false}
	code, out := b.do(http.MethodPost, "/api/v1/servers", body)
	sv := item(out)
	if code != http.StatusCreated || sv["name"] != "Germany 1" || sv["weight"] != float64(100) || sv["has_token"] != true ||
		sv["checked_at"] == nil {
		t.Fatalf("add: %d %v", code, out)
	}
	id := sv["id"].(string)
	if fs.tokens[id] != panelToken {
		t.Fatal("the token did not reach the provisioner")
	}

	// A plan naming it shows on the list.
	p := f.plan("Monthly", 30, 50<<30, 150_000)
	p.ServerIDs = []string{id}
	if err := f.st.UpsertPlan(context.Background(), f.st.Conn(), p); err != nil {
		t.Fatal(err)
	}
	code, out = b.do(http.MethodGet, "/api/v1/servers", nil)
	list := items(out)
	if code != 200 || len(list) != 1 || out["tested_min_version"] != "v3.8.5" || out["tested_max_version"] != "v3.9.0" {
		t.Fatalf("list: %d %v", code, out)
	}
	if plans := asJSON(list[0]["plans"]); !strings.Contains(plans, p.ID) || !strings.Contains(plans, "Monthly") {
		t.Fatalf("plans of the server: %s", plans)
	}

	// Edit: an empty token keeps the stored one; a new one replaces it.
	body["name"], body["token"], body["weight"] = "Germany", "", 300
	if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, body); code != 200 || item(out)["weight"] != float64(300) || fs.tokens[id] != panelToken {
		t.Fatalf("edit keeping the token: %d %v", code, out)
	}
	body["token"] = panelToken + "-NEW"
	if code, _ := b.do(http.MethodPut, "/api/v1/servers/"+id, body); code != 200 || fs.tokens[id] != panelToken+"-NEW" {
		t.Fatalf("new token: %d", code)
	}

	// Inbounds and their switch.
	code, out = b.do(http.MethodGet, "/api/v1/servers/"+id+"/inbounds", nil)
	if code != 200 || len(items(out)) != 2 || items(out)[0]["protocol"] != "vless" || items(out)[0]["attach"] != true {
		t.Fatalf("inbounds: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/servers/"+id+"/inbounds/1", map[string]any{"attach": false}); code != http.StatusNoContent || fs.inbounds[1] {
		t.Fatalf("detach: %d %v", code, fs.inbounds)
	}
	for _, bad := range []string{"0", "-1", "x", "99999999999"} {
		if code, _ := b.do(http.MethodPut, "/api/v1/servers/"+id+"/inbounds/"+bad, map[string]any{"attach": true}); code != http.StatusNotFound {
			t.Errorf("inbound %s: %d", bad, code)
		}
	}
	if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id+"/inbounds/1", map[string]any{}); code != 400 || out["field"] != "attach" {
		t.Fatalf("attach missing: %d %v", code, out)
	}

	// Tests: saved (empty token = stored), candidate, a refusal as 200 ok:false.
	if code, out := b.do(http.MethodPost, "/api/v1/servers/test", map[string]any{"id": strings.ToUpper(id), "base_url": "https://de.example.com:2053/p", "token": "", "allow_private": false}); code != 200 || out["ok"] != true || out["inbounds"] != float64(2) {
		t.Fatalf("test saved: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/servers/test", map[string]any{"base_url": "https://fr.example.com", "token": "wrong", "allow_private": false}); code != 200 || out["ok"] != false || out["detail"] != "the panel refused the API token" {
		t.Fatalf("test refused: %d %v", code, out)
	}
	if fs.probes[0].ID != id || fs.probes[0].Token != "" {
		t.Fatalf("probe: %+v", fs.probes[0])
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/servers/test", map[string]any{"id": "nope", "base_url": "https://x.example"}); code != 404 {
		t.Fatalf("test of a malformed id: %d", code)
	}

	// Field errors name the field and never repeat a secret.
	for field, override := range map[string]map[string]any{
		"name":         {"name": ""},
		"base_url":     {"base_url": "https://root:" + panelToken + "@de.example.com"},
		"sub_base_url": {"sub_base_url": "sub.example.com"},
		"weight":       {"weight": 5000},
		"token":        {"token": "two words"},
	} {
		bad := map[string]any{}
		for k, v := range body {
			bad[k] = v
		}
		for k, v := range override {
			bad[k] = v
		}
		code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, bad)
		if code != 400 || out["field"] != field || strings.Contains(asJSON(out), panelToken) {
			t.Errorf("%s: %d %v", field, code, out)
		}
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/servers", map[string]any{"name": "x", "base_url": "https://x.example", "token": "t", "extra": 1}); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}

	// The provisioner's refusals.
	fs.failNext = status.Error(codes.AlreadyExists, "another server already has this name")
	if code, out := b.do(http.MethodPost, "/api/v1/servers", map[string]any{"name": "Germany", "base_url": "https://x.example", "token": "t", "weight": 1}); code != 400 || out["field"] != "name" {
		t.Fatalf("name taken: %d %v", code, out)
	}
	fs.failNext = status.Error(codes.FailedPrecondition, "connection test failed: panel unreachable: connection refused")
	if code, out := b.do(http.MethodPost, "/api/v1/servers", map[string]any{"name": "FR", "base_url": "https://fr.example", "token": "t", "weight": 1}); code != 409 || !strings.Contains(out["message"].(string), "connection test failed") {
		t.Fatalf("connection test failed: %d %v", code, out)
	}
	fs.failNext = status.Error(codes.InvalidArgument, "enter the API token again: the panel's address changed")
	if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, body); code != 400 || out["field"] != "token" {
		t.Fatalf("token again: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/servers/00000000-0000-7000-8000-00000000ffff", body); code != 404 {
		t.Fatalf("edit unknown: %d", code)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/servers/not-an-id", body); code != 404 {
		t.Fatalf("edit malformed: %d", code)
	}

	// Delete: refused while a plan names it (its orders could never be
	// delivered) or services are on it, then gone.
	if code, out := b.do(http.MethodDelete, "/api/v1/servers/"+id, nil); code != 409 || out["error"] != "state" ||
		!strings.Contains(out["message"].(string), `"Monthly"`) || len(fs.servers) != 1 {
		t.Fatalf("delete named by a plan: %d %v", code, out)
	}
	p.ServerIDs = []string{}
	if err := f.st.UpsertPlan(context.Background(), f.st.Conn(), p); err != nil {
		t.Fatal(err)
	}
	fs.servers[0].Clients = 2
	if code, out := b.do(http.MethodDelete, "/api/v1/servers/"+id, nil); code != 409 || !strings.Contains(out["message"].(string), "2 clients") {
		t.Fatalf("delete with clients: %d %v", code, out)
	}
	fs.servers[0].Clients = 0
	if code, _ := b.do(http.MethodDelete, "/api/v1/servers/"+id, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	fs.down = true
	if code, out := b.do(http.MethodGet, "/api/v1/servers", nil); code != 503 || out["error"] != "unavailable" {
		t.Fatalf("provisioner down: %d %v", code, out)
	}
	fs.down = false

	// The audit trail: each write, the token only as "changed".
	for _, action := range []string{"server.add", "server.update", "server.inbound", "server.delete"} {
		e := lastAudit(t, b, "action="+action)
		if e["entity"] != "server" || e["entity_id"] != id {
			t.Errorf("%s: %v", action, e)
		}
	}
	if e := lastAudit(t, b, "action=server.update"); asJSON(e["after"]) == "" || !strings.Contains(asJSON(e["after"]), `"token":"changed"`) {
		t.Fatalf("token change: %v", e)
	}
	var all string
	if err := f.st.DB().QueryRow(context.Background(), `SELECT coalesce(string_agg(coalesce(before::text,'') || coalesce(after::text,''), ' '), '')
		FROM core.audit_log WHERE entity = 'server'`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(all, "SECRET") || strings.Contains(all, panelToken) {
		t.Fatalf("a token reached the audit log: %s", all)
	}
}

// A server added switched off is saved switched off in one step: no later
// switch-off that could fail and leave it taking new services.
func TestServerAddedSwitchedOffIsNeverLeftOn(t *testing.T) {
	_, b, fs := serversFixture(t)
	fs.failUpdate = status.Error(codes.InvalidArgument, "weight must be 1..1000") // never needed
	code, out := b.do(http.MethodPost, "/api/v1/servers", map[string]any{"name": "Spare", "base_url": "https://spare.example.com",
		"token": panelToken, "weight": 100, "enabled": false})
	if code != http.StatusCreated || item(out)["enabled"] != false || len(fs.servers) != 1 || fs.servers[0].Enabled ||
		strings.Contains(asJSON(out), panelToken) {
		t.Fatalf("add switched off: %d %v %+v", code, out, fs.servers)
	}
}

// An admin manages servers but cannot let BOBRES reach a private network
// with an address or token of their choosing (403 on allow_private).
func TestServersPrivateNetworkIsTheOwners(t *testing.T) {
	f, b, fs := serversFixture(t)
	f.staffUser(8711, "admin")
	adm := f.browser()
	adm.loginWithLink(8711)
	priv := map[string]any{"name": "Home", "base_url": "http://10.0.0.5:2053/p", "sub_base_url": "", "token": panelToken,
		"weight": 100, "enabled": true, "allow_private": true, "skip_check": true}
	if code, out := adm.do(http.MethodPost, "/api/v1/servers", priv); code != http.StatusForbidden || out["field"] != "allow_private" {
		t.Fatalf("admin adds a private server: %d %v", code, out)
	}
	if len(fs.servers) != 0 {
		t.Fatal("the refused server reached the provisioner")
	}
	code, out := b.do(http.MethodPost, "/api/v1/servers", priv)
	if code != http.StatusCreated {
		t.Fatalf("owner: %d %v", code, out)
	}
	id := item(out)["id"].(string)
	priv["token"], priv["weight"], priv["name"] = "", 7, "Home 2"
	if code, out := adm.do(http.MethodPut, "/api/v1/servers/"+id, priv); code != 200 || item(out)["weight"] != float64(7) {
		t.Fatalf("admin reweights it: %d %v", code, out)
	}
	priv["base_url"] = "http://172.17.0.1:5432"
	if code, _ := adm.do(http.MethodPut, "/api/v1/servers/"+id, priv); code != http.StatusForbidden {
		t.Fatalf("admin moves it: %d", code)
	}
	if code, _ := adm.do(http.MethodPost, "/api/v1/servers/test", map[string]any{"base_url": "http://10.0.0.1:6379", "token": "x", "allow_private": true}); code != http.StatusForbidden {
		t.Fatalf("admin probes the private network: %d", code)
	}
	if code, out := adm.do(http.MethodPost, "/api/v1/servers/test", map[string]any{"id": id}); code != 200 || out["ok"] != true {
		t.Fatalf("admin tests it as stored: %d %v", code, out)
	}
}

// Connection tests are limited per staff member.
func TestServerTestsAreRateLimited(t *testing.T) {
	_, b, _ := serversFixture(t)
	probe := map[string]any{"base_url": "https://fr.example.com", "token": "t"}
	for i := 0; i < 10; i++ {
		if code, _ := b.do(http.MethodPost, "/api/v1/servers/test", probe); code != 200 {
			t.Fatalf("test %d: %d", i, code)
		}
	}
	if code, out := b.do(http.MethodPost, "/api/v1/servers/test", probe); code != http.StatusTooManyRequests || out["error"] != "rate_limited" {
		t.Fatalf("11th test: %d %v", code, out)
	}
}

// A new subscription address refreshes the stored links of that server's
// services (the bot shows the stored link).
func TestServerEditRefreshesStoredLinks(t *testing.T) {
	f, b, fs := serversFixture(t)
	code, out := b.do(http.MethodPost, "/api/v1/servers", map[string]any{"name": "DE", "base_url": "https://de.example.com",
		"sub_base_url": "https://old.example/sub/", "token": "t", "weight": 100, "enabled": true})
	if code != http.StatusCreated {
		t.Fatalf("add: %d %v", code, out)
	}
	id := item(out)["id"].(string)
	u := f.customer(8721, "c")
	p := f.plan("M", 30, 1<<30, 1000)
	_, sub := f.order(u, p, "active", "active", time.Now().Add(30*24*time.Hour), 1<<30)
	if _, err := f.st.DB().Exec(context.Background(), `UPDATE core.subscriptions SET server_id = $2, sub_link = 'https://old.example/sub/x' WHERE id = $1`, sub.ID, id); err != nil {
		t.Fatal(err)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/servers/"+id, map[string]any{"name": "DE", "base_url": "https://de.example.com",
		"sub_base_url": "https://new.example/sub/", "token": "", "weight": 100, "enabled": true}); code != 200 {
		t.Fatalf("edit: %d", code)
	}
	stale, err := f.st.StaleSubLinks(context.Background(), f.st.Conn(), 10)
	if err != nil || len(stale) != 1 || stale[0].ID != sub.ID {
		t.Fatalf("marked: %v %+v", err, stale)
	}
	w := f.dom.NewSubLinkWorker(&linkProv{base: "https://new.example/sub/"}, fs, nil)
	if n, err := w.RefreshStale(context.Background()); n != 1 || err != nil {
		t.Fatalf("refresh: %d %v", n, err)
	}
	got, _ := f.st.GetSubscription(context.Background(), f.st.Conn(), sub.ID)
	if got.SubLink != "https://new.example/sub/"+sub.ID {
		t.Fatalf("stored link: %q", got.SubLink)
	}
}

// linkProv builds links from a subscription address.
type linkProv struct {
	fakeProv
	base string
}

func (p *linkProv) GetLinks(_ context.Context, id string) (domain.Links, error) {
	return domain.Links{SubscriptionLink: p.base + id}, nil
}

// Plans carry their servers: set on create, kept when an edit leaves them
// out, cleared with [], checked, and audited with the plan.
func TestPlanServerIDs(t *testing.T) {
	f, b, _ := serversFixture(t)
	const a, c = "00000000-0000-7000-8000-0000000000a1", "00000000-0000-7000-8000-0000000000c3"
	plan := map[string]any{"name": map[string]string{"en": "Monthly"}, "kind": "both", "duration_days": 30, "traffic_gb": "50",
		"price": "150000", "currency": "IRT", "enabled": true, "is_trial": false, "is_topup": false, "sort": 0,
		"server_ids": []string{c, strings.ToUpper(a), a}}
	code, out := b.do(http.MethodPost, "/api/v1/plans", plan)
	saved, _ := out["plan"].(map[string]any)
	if code != 200 || asJSON(saved["server_ids"]) != `["`+a+`","`+c+`"]` {
		t.Fatalf("create: %d %v", code, out)
	}
	id := saved["id"].(string)
	delete(plan, "server_ids")
	plan["price"] = "160000"
	if code, out := b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != 200 || len(out["plan"].(map[string]any)["server_ids"].([]any)) != 2 {
		t.Fatalf("edit without servers keeps them: %d %v", code, out)
	}
	code, out = b.do(http.MethodGet, "/api/v1/plans", nil)
	var listed map[string]any
	for _, it := range items(out) {
		if it["id"] == id {
			listed = it
		}
	}
	if code != 200 || asJSON(listed["server_ids"]) != `["`+a+`","`+c+`"]` {
		t.Fatalf("list: %d %v", code, listed)
	}
	plan["server_ids"] = []string{}
	if code, out := b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != 200 || asJSON(out["plan"].(map[string]any)["server_ids"]) != `[]` {
		t.Fatalf("any server: %d %v", code, out)
	}
	plan["server_ids"] = []string{"default"}
	if code, out := b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != 400 || out["field"] != "server_ids" {
		t.Fatalf("bad id: %d %v", code, out)
	}
	if e := lastAudit(t, b, "action=plan.upsert&entity_id="+id); !strings.Contains(asJSON(e["before"]), a) {
		t.Fatalf("audit: %v", e)
	}
	// Plans without servers list [] (never null).
	other := f.plan("Other", 30, 1<<30, 1000)
	_, out = b.do(http.MethodGet, "/api/v1/plans", nil)
	for _, it := range items(out) {
		if it["id"] == other.ID && asJSON(it["server_ids"]) != `[]` {
			t.Fatalf("plan without servers: %v", it)
		}
	}
}

// The overview lists the servers with their health and sums them up in
// panel; without an answer from the provisioner it says so. Why a panel
// failed (its address) is for those who manage servers only.
func TestOverviewServers(t *testing.T) {
	f, b, fs := serversFixture(t)
	fs.servers = []domain.Server{
		{ID: "00000000-0000-7000-8000-000000000001", Name: "DE", Enabled: true, Healthy: true, CheckedAt: 1, Clients: 5},
		{ID: "00000000-0000-7000-8000-000000000002", Name: "FR", Enabled: true, Healthy: false, HealthError: "dial tcp 10.0.0.5:2053: connection refused", CheckedAt: 1, Clients: 3},
		{ID: "00000000-0000-7000-8000-000000000003", Name: "New", Enabled: true},
		{ID: "00000000-0000-7000-8000-000000000004", Name: "Spare", Healthy: true, CheckedAt: 1},
	}
	code, out := b.do(http.MethodGet, "/api/v1/overview", nil)
	servers, _ := out["servers"].([]any)
	if code != 200 || len(servers) != 4 || asJSON(out["panel"]) != `{"detail":"1 of 3 servers do not answer","healthy":false}` {
		t.Fatalf("overview: %d %v %v", code, out["servers"], out["panel"])
	}
	if fr := servers[1].(map[string]any); fr["healthy"] != false || fr["detail"] != "dial tcp 10.0.0.5:2053: connection refused" || fr["clients"] != float64(3) {
		t.Fatalf("FR: %v", fr)
	}
	// Support reads the overview too, but not a panel's address.
	f.staffUser(8731, "support")
	sup := f.browser()
	sup.loginWithLink(8731)
	code, out = sup.do(http.MethodGet, "/api/v1/overview", nil)
	servers, _ = out["servers"].([]any)
	if code != 200 || len(servers) != 4 || strings.Contains(asJSON(out), "10.0.0.5") || servers[1].(map[string]any)["detail"] != "does not answer" ||
		servers[2].(map[string]any)["detail"] != "not checked yet" || servers[0].(map[string]any)["detail"] != "" ||
		servers[3].(map[string]any)["detail"] != "takes no new services" {
		t.Fatalf("support overview: %d %v", code, out["servers"])
	}
	if nw := servers[2].(map[string]any); nw["healthy"] != true || nw["detail"] != "not checked yet" {
		t.Fatalf("never checked: %v", nw)
	}
	if sp := servers[3].(map[string]any); sp["healthy"] != true || sp["detail"] != "takes no new services" {
		t.Fatalf("switched off: %v", sp)
	}
	fs.servers = fs.servers[:1]
	if _, out := b.do(http.MethodGet, "/api/v1/overview", nil); asJSON(out["panel"]) != `{"detail":"","healthy":true}` {
		t.Fatalf("all healthy: %v", out["panel"])
	}
	fs.servers = nil
	if _, out := b.do(http.MethodGet, "/api/v1/overview", nil); asJSON(out["servers"]) != `[]` || out["panel"].(map[string]any)["detail"] != "no 3x-ui server yet" {
		t.Fatalf("no server: %v %v", out["servers"], out["panel"])
	}
	fs.down = true
	if _, out := b.do(http.MethodGet, "/api/v1/overview", nil); out["servers"] != nil || out["panel"].(map[string]any)["detail"] != "provisioner unreachable" {
		t.Fatalf("down: %v %v", out["servers"], out["panel"])
	}
	_ = f
}
