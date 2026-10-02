package server

import (
	"context"
	"net"
	"os"
	"testing"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	testTok  = "test-svc-token-provisioner"
	panelTok = "fake-panel-token"
)

type fixture struct {
	client provisionerv1.ProvisionerServiceClient
	fake   *xuifake.Server
	server *store.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("BOBRES_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///postgres?host=/tmp&port=5432&sslmode=disable"
	}
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "provisioner"); err != nil {
		t.Skipf("migration: %v", err)
	}
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(ctx, dsn, env)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	for _, tbl := range []string{"client_map", "provision_jobs", "xui_servers"} {
		if _, err := st.DB().Exec(ctx, "TRUNCATE provisioner."+tbl+" CASCADE"); err != nil {
			t.Fatal(err)
		}
	}

	fake := xuifake.NewServer(panelTok)
	t.Cleanup(fake.Close)

	sv, err := st.AddServer(ctx, &store.Server{Name: "fake", BaseURL: fake.URL, Token: panelTok, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	gs := grpc.NewServer(grpc.UnaryInterceptor(grpcauth.UnaryInterceptor(testTok)))
	New(st, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses()).Register(gs)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(grpcauth.ClientCredentials{Token: testTok}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &fixture{client: provisionerv1.NewProvisionerServiceClient(conn), fake: fake, server: sv}
}

const sub1 = "00000000-0000-7000-8000-0000000000a1"

func TestCreateProvisionFlow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	cl, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: sub1,
		Email:          "u42-abcd",
		DurationDays:   30,
		TrafficBytes:   10 << 30,
	})
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if !f.fake.Has("u42-abcd") {
		t.Fatal("client not created on fake panel")
	}
	if len(cl.GetInboundIds()) != 2 {
		t.Errorf("attached inbounds = %v, want 2 enabled", cl.GetInboundIds())
	}
	if cl.GetExpiresAt() == 0 {
		t.Error("expiry not set")
	}

	links, err := f.client.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: sub1})
	if err != nil {
		t.Fatalf("GetLinks: %v", err)
	}
	if links.GetSubscriptionLink() == "" || links.GetQrPngBase64() == "" {
		t.Error("links missing url or qr png")
	}

	usage, err := f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1})
	if err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
	_ = usage

	if _, err := f.client.RenewClient(ctx, &provisionerv1.RenewClientRequest{SubscriptionId: sub1, AddDays: 5}); err != nil {
		t.Fatalf("Renew: %v", err)
	}

	if _, err := f.client.ResetTraffic(ctx, &provisionerv1.ResetTrafficRequest{SubscriptionId: sub1}); err != nil {
		t.Fatalf("ResetTraffic: %v", err)
	}

	ins, err := f.client.ListInbounds(ctx, &provisionerv1.ListInboundsRequest{})
	if err != nil || len(ins.GetInbounds()) != 3 {
		t.Fatalf("ListInbounds: %v %v", err, ins)
	}

	hc, err := f.client.HealthCheck(ctx, &provisionerv1.HealthCheckRequest{})
	if err != nil || !hc.GetHealthy() {
		t.Fatalf("HealthCheck: %v %+v", err, hc)
	}

	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: sub1}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if f.fake.Has("u42-abcd") {
		t.Fatal("client still on panel after delete")
	}
	// idempotent delete
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: sub1}); err != nil {
		t.Fatalf("second delete not idempotent: %v", err)
	}
}

func TestAddServerAndAuth(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	sv, err := f.client.AddServer(ctx, &provisionerv1.AddServerRequest{
		Name: "second", BaseUrl: "https://example.com", ApiToken: "tok0123456789abcdef0123456789ab",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	if sv.GetId() == "" || sv.GetBaseUrl() != "https://example.com" {
		t.Errorf("bad server: %+v", sv)
	}

	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: sub1, Email: "u42-bad", DurationDays: -1, TrafficBytes: -1,
	}); status.Code(err) == codes.OK {
		t.Fatal("negative values accepted")
	}
}
