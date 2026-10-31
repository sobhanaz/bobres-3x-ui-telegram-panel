package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hiddifyfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/suifake"
)

const (
	suiPanelTok    = "s-ui-panel-token-0123456789"          //gitleaks:allow test fixture
	hiddifyAPIKey  = "3f0c9a52-7b1e-4d6a-8e2f-1a2b3c4d5e6f" //nolint:gosec //gitleaks:allow test fixture
	panelFlowEmail = "u77-flow"
)

// onlyPanel registers a panel of kind as the only usable server.
func (f *fixture) onlyPanel(t *testing.T, kind, baseURL, token string) *provisionerv1.XUIServer {
	t.Helper()
	if _, err := f.st.DB().Exec(context.Background(), "TRUNCATE provisioner.client_map, provisioner.xui_servers CASCADE"); err != nil {
		t.Fatal(err)
	}
	sv, err := f.client.AddServer(context.Background(), &provisionerv1.AddServerRequest{
		Name: kind, BaseUrl: baseURL, ApiToken: token, AllowPrivate: true, Kind: kind,
	})
	if err != nil {
		t.Fatalf("AddServer(%s): %v", kind, err)
	}
	return sv
}

// panelFlow drives one client through every call core makes, on a server of
// any kind: create, links, usage, limits, renew, switch off and on, reset, a
// deletion behind BOBRES's back (reported as client_missing, not as a failure),
// RecreateClient, and delete. removeBehind deletes the client on the panel the
// way an operator does. The fake-panel tests and the real-panel integration
// tests both run it.
func panelFlow(t *testing.T, f *fixture, kind string, removeBehind func(email string)) {
	t.Helper()
	ctx := context.Background()
	near := func(name string, got, want int64) {
		t.Helper()
		// A panel that keeps whole days (Hiddify) ends a client at the midnight after the day asked for.
		if got < want || got >= want+24*3600 {
			t.Fatalf("%s: expiry %d, want %d (or up to a day later)", name, got, want)
		}
	}
	cl, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: sub1, Email: panelFlowEmail, DurationDays: 30, TrafficBytes: 10 << 30,
	})
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if len(cl.GetInboundIds()) == 0 || cl.GetXuiSubId() == "" {
		t.Fatalf("client = %+v", cl)
	}
	links, err := f.client.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: sub1})
	if err != nil {
		t.Fatalf("GetLinks: %v", err)
	}
	if links.GetSubscriptionLink() == "" || len(links.GetConfigLinks()) == 0 || links.GetQrPngBase64() == "" {
		t.Fatalf("links = %+v", links)
	}
	for _, l := range links.GetConfigLinks() {
		if !strings.Contains(l, "://") {
			t.Fatalf("not a share link: %q", l)
		}
	}
	u, err := f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil || u.GetClientMissing() || u.GetTrafficTotalBytes() != 10<<30 || !u.GetEnabled() {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	near("created", u.GetExpiresAt(), cl.GetExpiresAt())

	expires := time.Now().Add(40 * 24 * time.Hour).Unix()
	if _, err := f.client.SetClientLimits(ctx, &provisionerv1.SetClientLimitsRequest{SubscriptionId: sub1, ExpiresAt: expires, TrafficTotalBytes: 20 << 30}); err != nil {
		t.Fatalf("SetClientLimits: %v", err)
	}
	u, err = f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil || u.GetTrafficTotalBytes() != 20<<30 {
		t.Fatalf("usage after limits = %+v, %v", u, err)
	}
	near("limits", u.GetExpiresAt(), expires)
	before := u.GetExpiresAt()
	if _, err := f.client.RenewClient(ctx, &provisionerv1.RenewClientRequest{SubscriptionId: sub1, AddDays: 2, AddBytes: 1 << 30}); err != nil {
		t.Fatalf("RenewClient: %v", err)
	}
	if u, err = f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1}); err != nil || u.GetExpiresAt() != before+2*86400 || u.GetTrafficTotalBytes() != 21<<30 {
		t.Fatalf("usage after renew = %+v, %v (expiry was %d)", u, err, before)
	}
	if _, err := f.client.SetClientEnabled(ctx, &provisionerv1.SetClientEnabledRequest{SubscriptionId: sub1, Enabled: false}); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if u, err = f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1}); err != nil || u.GetEnabled() {
		t.Fatalf("usage switched off = %+v, %v", u, err)
	}
	if _, err := f.client.SetClientEnabled(ctx, &provisionerv1.SetClientEnabledRequest{SubscriptionId: sub1, Enabled: true}); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	if _, err := f.client.ResetTraffic(ctx, &provisionerv1.ResetTrafficRequest{SubscriptionId: sub1}); err != nil {
		t.Fatalf("ResetTraffic: %v", err)
	}
	if u, err = f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1}); err != nil || !u.GetEnabled() || u.GetTrafficUsedBytes() != 0 {
		t.Fatalf("usage after reset = %+v, %v", u, err)
	}

	// Deleted on the panel behind BOBRES's back: an answer, not a failure.
	removeBehind(panelFlowEmail)
	u, err = f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil || !u.GetClientMissing() {
		t.Fatalf("usage of a client deleted on the panel = %+v, %v", u, err)
	}
	expires = time.Now().Add(12 * 24 * time.Hour).Unix()
	req := &provisionerv1.RecreateClientRequest{SubscriptionId: sub1, ExpiresAt: expires, TrafficTotalBytes: 3 << 30}
	again, err := f.client.RecreateClient(ctx, req)
	if err != nil {
		t.Fatalf("RecreateClient: %v", err)
	}
	if again.GetEmail() != panelFlowEmail || again.GetXuiSubId() != cl.GetXuiSubId() {
		t.Fatalf("made again = %+v, was %+v", again, cl)
	}
	u, err = f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil || u.GetClientMissing() || u.GetTrafficTotalBytes() != 3<<30 {
		t.Fatalf("usage after RecreateClient = %+v, %v", u, err)
	}
	near("recreated", u.GetExpiresAt(), expires)
	if _, err := f.client.RecreateClient(ctx, req); err != nil {
		t.Fatalf("second RecreateClient: %v", err)
	}
	if l2, err := f.client.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: sub1}); err != nil || l2.GetSubscriptionLink() == "" {
		t.Fatalf("links after RecreateClient = %+v, %v", l2, err)
	} else if kind == store.KindHiddify && l2.GetSubscriptionLink() != links.GetSubscriptionLink() {
		t.Fatalf("the customer's subscription link changed: %q, was %q", l2.GetSubscriptionLink(), links.GetSubscriptionLink())
	}

	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: sub1}); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if _, err := f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1}); status.Code(err) != codes.NotFound {
		t.Fatalf("usage after delete: %v", err)
	}
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: sub1}); err != nil {
		t.Fatalf("second DeleteClient: %v", err)
	}
}

func TestSUIServerFlow(t *testing.T) {
	f := newFixture(t)
	fake := suifake.NewServer(suiPanelTok)
	t.Cleanup(fake.Close)
	sv := f.onlyPanel(t, store.KindSUI, fake.URL, suiPanelTok)
	if sv.GetKind() != store.KindSUI || sv.GetPanelVersion() != "v1.6.6" || sv.GetVersionUntested() || !sv.GetHealthy() {
		t.Fatalf("server = %+v", sv)
	}
	ins, err := f.client.ListInbounds(context.Background(), &provisionerv1.ListInboundsRequest{ServerId: sv.GetId()})
	if err != nil || len(ins.GetInbounds()) != 3 {
		t.Fatalf("inbounds = %+v, %v", ins, err)
	}
	panelFlow(t, f, store.KindSUI, func(email string) {
		fake.Remove(email)
		if fake.Has(email) {
			t.Fatal("still on the panel")
		}
	})
	if fake.Has(panelFlowEmail) {
		t.Fatal("deleted client still on the panel")
	}
}

// An S-UI client is made in its group (a reseller's), and a name somebody else
// has on the panel is refused, with the reservation released.
func TestSUIServerGroupsAndStrangers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fake := suifake.NewServer(suiPanelTok)
	t.Cleanup(fake.Close)
	f.onlyPanel(t, store.KindSUI, fake.URL, suiPanelTok)
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u78-g", DurationDays: 3, Group: "BOBRES-ALI"}); err != nil {
		t.Fatal(err)
	}
	if sn, _ := fake.Snapshot("u78-g"); sn.Group != "BOBRES-ALI" {
		t.Fatalf("group = %q", sn.Group)
	}
	fake.Seed("u79-taken", "", "")
	const sub2 = "00000000-0000-7000-8000-0000000000a2"
	_, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub2, Email: "u79-taken", DurationDays: 3})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("a stranger's name: %v", err)
	}
	if _, err := f.st.GetClientMap(ctx, sub2); err == nil {
		t.Fatal("the reservation stayed")
	}
	// A wrong token is the panel's refusal, in words staff understand.
	res, err := f.client.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: fake.URL, ApiToken: "wrong", AllowPrivate: true, Kind: store.KindSUI})
	if err != nil || res.GetOk() || !strings.Contains(res.GetDetail(), "API token") {
		t.Fatalf("wrong token: %+v %v", res, err)
	}
}

func TestHiddifyServerFlow(t *testing.T) {
	f := newFixture(t)
	fake := hiddifyfake.NewServer(hiddifyAPIKey)
	t.Cleanup(fake.Close)
	sv := f.onlyPanel(t, store.KindHiddify, fake.URL, hiddifyAPIKey)
	if sv.GetKind() != store.KindHiddify || sv.GetPanelVersion() != "v14.0.5" || sv.GetVersionUntested() {
		t.Fatalf("server = %+v", sv)
	}
	panelFlow(t, f, store.KindHiddify, func(email string) {
		fake.Remove(email)
		if fake.Has(email) {
			t.Fatal("still on the panel")
		}
	})
	if fake.Has(panelFlowEmail) {
		t.Fatal("deleted client still on the panel")
	}
}

func TestHiddifyServerRefusals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fake := hiddifyfake.NewServer(hiddifyAPIKey)
	t.Cleanup(fake.Close)
	f.onlyPanel(t, store.KindHiddify, fake.URL, hiddifyAPIKey)
	// A key that is not a UUID never reaches the panel.
	if _, err := f.client.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: fake.URL, ApiToken: "nope", AllowPrivate: true, Kind: store.KindHiddify}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("not a UUID: %v", err)
	}
	res, err := f.client.TestServer(ctx, &provisionerv1.TestServerRequest{BaseUrl: fake.URL, ApiToken: "11111111-2222-4333-8444-555555555555", AllowPrivate: true, Kind: store.KindHiddify})
	if err != nil || res.GetOk() || !strings.Contains(res.GetDetail(), "API key") {
		t.Fatalf("wrong key: %+v %v", res, err)
	}
	// An inbound other than the one Hiddify lists cannot be chosen.
	sv, _ := f.st.FirstServer(ctx)
	_, err = f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u80-x", DurationDays: 3,
		Inbounds: []*provisionerv1.InboundSelection{{ServerId: sv.ID, InboundIds: []int32{7}}}})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("a chosen inbound Hiddify does not have: %v", err)
	}
}
