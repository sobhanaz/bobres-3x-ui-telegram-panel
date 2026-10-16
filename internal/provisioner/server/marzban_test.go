package server

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/marzbanfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
)

const marzbanCreds = "admin:s3cret:pw" //gitleaks:allow test fixture

// marzbanPanel registers a Marzban fake as the only usable server.
func (f *fixture) marzbanPanel(t *testing.T) (*marzbanfake.Server, *provisionerv1.XUIServer) {
	t.Helper()
	fake := marzbanfake.NewServer("admin", "s3cret:pw")
	t.Cleanup(fake.Close)
	if _, err := f.st.DB().Exec(context.Background(), "TRUNCATE provisioner.client_map, provisioner.xui_servers CASCADE"); err != nil {
		t.Fatal(err)
	}
	sv, err := f.client.AddServer(context.Background(), &provisionerv1.AddServerRequest{
		Name: "marz", BaseUrl: fake.URL, ApiToken: marzbanCreds, AllowPrivate: true, Kind: store.KindMarzban,
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	return fake, sv
}

func TestMarzbanServerFlow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fake, sv := f.marzbanPanel(t)
	if sv.GetKind() != store.KindMarzban || sv.GetVersionUntested() || sv.GetPanelVersion() == "" {
		t.Fatalf("server = %+v", sv)
	}

	cl, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: sub1, Email: "u42-abcd1234", DurationDays: 30, TrafficBytes: 10 << 30,
	})
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if !fake.Has("u42-abcd1234") || len(cl.GetInboundIds()) != 3 {
		t.Fatalf("client = %+v", cl)
	}
	links, err := f.client.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: sub1})
	if err != nil {
		t.Fatalf("GetLinks: %v", err)
	}
	if !strings.Contains(links.GetSubscriptionLink(), "/sub/") || links.GetQrPngBase64() == "" {
		t.Errorf("links = %+v", links)
	}
	for name, call := range map[string]func() error{
		"usage": func() error {
			_, err := f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
			return err
		},
		"renew": func() error {
			_, err := f.client.RenewClient(ctx, &provisionerv1.RenewClientRequest{SubscriptionId: sub1, AddDays: 5})
			return err
		},
		"reset": func() error {
			_, err := f.client.ResetTraffic(ctx, &provisionerv1.ResetTrafficRequest{SubscriptionId: sub1})
			return err
		},
	} {
		if err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: sub1}); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if fake.Has("u42-abcd1234") {
		t.Fatal("client still on the panel")
	}
}

func TestMarzbanServerRefusals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	fake, _ := f.marzbanPanel(t)

	// A wrong password is refused with a Marzban-specific reason.
	res, err := f.client.TestServer(ctx, &provisionerv1.TestServerRequest{
		BaseUrl: fake.URL, ApiToken: "admin:wrong", AllowPrivate: true, Kind: store.KindMarzban,
	})
	if err != nil || res.GetOk() || !strings.Contains(res.GetDetail(), "username or password") {
		t.Fatalf("wrong password: %+v, %v", res, err)
	}
	// Credentials without a colon never reach the panel.
	_, err = f.client.TestServer(ctx, &provisionerv1.TestServerRequest{
		BaseUrl: fake.URL, ApiToken: "nocolon", AllowPrivate: true, Kind: store.KindMarzban,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no colon: %v", err)
	}
	// An unknown kind is refused.
	_, err = f.client.TestServer(ctx, &provisionerv1.TestServerRequest{
		BaseUrl: fake.URL, ApiToken: marzbanCreds, AllowPrivate: true, Kind: "nope",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown kind: %v", err)
	}
	// A username Marzban would refuse is stopped before a reservation.
	_, err = f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: sub1, Email: "a b", DurationDays: 30,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad username: %v", err)
	}
}
