package server

import (
	"context"
	"net"
	"strings"
	"testing"

	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const (
	coreTok  = "test-core-token-for-provisioner" //gitleaks:allow test fixture
	panelTok = "fake-panel-token"                //gitleaks:allow test fixture
)

type fixture struct {
	addr   string
	client provisionerv1.ProvisionerServiceClient
	srv    *Server
	st     *store.Store
	fake   *xuifake.Server
	server *store.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(ctx, dsn, env)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB().Exec(ctx, "TRUNCATE provisioner.client_map, provisioner.provision_jobs, provisioner.xui_servers CASCADE"); err != nil {
		t.Fatal(err)
	}

	fake := xuifake.NewServer(panelTok)
	t.Cleanup(fake.Close)
	sv, err := st.AddServer(ctx, &store.Server{Name: "fake", BaseURL: fake.URL, Token: panelTok, Enabled: true,
		SubBaseURL: "https://sub.example.com:2096/sub/"})
	if err != nil {
		t.Fatal(err)
	}

	srv := New(st, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	gs, err := grpcx.NewServer(nil, grpcauth.Peer{Name: "core", Token: coreTok})
	if err != nil {
		t.Fatal(err)
	}
	srv.Register(gs)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpcx.Dial(lis.Addr().String(), coreTok)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &fixture{addr: lis.Addr().String(), client: provisionerv1.NewProvisionerServiceClient(conn), srv: srv, st: st, fake: fake, server: sv}
}

const sub1 = "00000000-0000-7000-8000-0000000000a1"

func TestCreateProvisionFlow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	cl, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: sub1, Email: "u42-abcd", DurationDays: 30, TrafficBytes: 10 << 30,
	})
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if !f.fake.Has("u42-abcd") {
		t.Fatal("client not created on fake panel")
	}
	if len(cl.GetInboundIds()) != 2 || cl.GetExpiresAt() == 0 || len(cl.GetXuiSubId()) != 32 {
		t.Errorf("bad client: %+v", cl)
	}

	links, err := f.client.GetLinks(ctx, &provisionerv1.GetLinksRequest{SubscriptionId: sub1})
	if err != nil {
		t.Fatalf("GetLinks: %v", err)
	}
	if links.GetSubscriptionLink() != "https://sub.example.com:2096/sub/"+cl.GetXuiSubId() {
		t.Errorf("subscription link = %q", links.GetSubscriptionLink())
	}
	if links.GetQrPngBase64() == "" || len(links.GetConfigLinks()) == 0 {
		t.Errorf("links missing qr or config links: %+v", links)
	}

	if _, err := f.client.GetUsage(ctx, &provisionerv1.GetUsageRequest{SubscriptionId: sub1}); err != nil {
		t.Fatalf("GetUsage: %v", err)
	}
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
	if _, err := f.client.DeleteClient(ctx, &provisionerv1.DeleteClientRequest{SubscriptionId: sub1}); err != nil {
		t.Fatalf("second delete not idempotent: %v", err)
	}
	// One panel client served every call: no connection-per-call leak, and the
	// per-email locks are actually shared.
	f.srv.mu.Lock()
	n := len(f.srv.adapters)
	f.srv.mu.Unlock()
	if n != 1 {
		t.Errorf("cached panel clients = %d, want 1", n)
	}
}

// The subId used to be the first 8 hex chars of a UUIDv7, i.e. a timestamp:
// every subscription created in the same ~65 s shared one 3x-ui subscription
// (each customer's link served the others' configs) and all were guessable.
func TestSubIDsAreDistinctAndUnpredictable(t *testing.T) {
	f := newFixture(t)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := f.st.SubIDFor("00000000-0000-7000-8000-" + strings.Repeat("0", 10) + string(rune('a'+i%26)) + string(rune('a'+i/26)))
		if len(id) != 32 || seen[id] {
			t.Fatalf("subId %q repeated or wrong length", id)
		}
		seen[id] = true
	}
	if a, b := f.st.SubIDFor(sub1), f.st.SubIDFor(sub1); a != b {
		t.Fatal("subId must be stable for retries")
	}
	if strings.HasPrefix(sub1, f.st.SubIDFor(sub1)[:8]) {
		t.Fatal("subId leaks the subscription id")
	}
}

func TestCreateClientIsSafeToRetry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	req := &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u7-retry", DurationDays: 1}
	first, err := f.client.CreateClient(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.client.CreateClient(ctx, req)
	if err != nil || again.GetXuiSubId() != first.GetXuiSubId() || f.fake.Calls("add") != 1 {
		t.Fatalf("retry: %v, add calls=%d", err, f.fake.Calls("add"))
	}

	// An earlier attempt created the client but its response was lost (no map
	// row): the retry adopts it because it carries our derived subId.
	const lost = "00000000-0000-7000-8000-0000000000b2"
	f.fake.Seed("u8-lost", f.st.SubIDFor(lost), []int{1})
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: lost, Email: "u8-lost"}); err != nil {
		t.Fatalf("adopting our own client: %v", err)
	}

	// Someone else's client with the same email is never taken over.
	f.fake.Seed("u9-taken", "somebody-elses-sub", []int{1})
	_, err = f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: "00000000-0000-7000-8000-0000000000b3", Email: "u9-taken"})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("foreign client: want AlreadyExists, got %v", err)
	}
}

func TestNoEnabledInboundIsAnError(t *testing.T) {
	f := newFixture(t)
	f.fake.SetInboundsEnabled(false)
	_, err := f.client.CreateClient(context.Background(), &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "u1-x"})
	if status.Code(err) != codes.FailedPrecondition || f.fake.Calls("add") != 0 {
		t.Fatalf("want FailedPrecondition and no create (used to attach to inbound 1): %v", err)
	}
}

func TestServerIDIsHonoured(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.client.ListInbounds(ctx, &provisionerv1.ListInboundsRequest{ServerId: f.server.ID}); err != nil {
		t.Fatalf("explicit server: %v", err)
	}
	_, err := f.client.ListInbounds(ctx, &provisionerv1.ListInboundsRequest{ServerId: "00000000-0000-7000-8000-00000000ffff"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown server must be NotFound, not silently the default: %v", err)
	}
}

func TestAddServerValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sv, err := f.client.AddServer(ctx, &provisionerv1.AddServerRequest{
		Name: "second", BaseUrl: "https://example.com", ApiToken: "tok", SubBaseUrl: "https://example.com:2096/sub/",
	})
	if err != nil || sv.GetSubBaseUrl() == "" {
		t.Fatalf("AddServer: %v %+v", err, sv)
	}
	for name, req := range map[string]*provisionerv1.AddServerRequest{
		"missing token": {Name: "x", BaseUrl: "https://example.com"},
		"creds in url":  {Name: "x", BaseUrl: "https://user:pw@example.com", ApiToken: "t"},
		"bad sub url":   {Name: "x", BaseUrl: "https://example.com", ApiToken: "t", SubBaseUrl: "ftp://x"},
		"not a url":     {Name: "x", BaseUrl: "::::", ApiToken: "t"},
	} {
		if _, err := f.client.AddServer(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: want InvalidArgument, got %v", name, err)
		}
	}
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{
		SubscriptionId: sub1, Email: "u42-bad", DurationDays: -1,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative duration: %v", err)
	}
	if _, err := f.client.CreateClient(ctx, &provisionerv1.CreateClientRequest{SubscriptionId: sub1, Email: "../etc"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("path-like email: %v", err)
	}
}

func TestEnsureDefaultServerOnlyOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	strict := New(f.st) // production options: no test relaxations
	if added, err := strict.EnsureDefaultServer(ctx, "https://panel.example.com", "tok", "", false); err != nil || added {
		t.Fatalf("a panel already exists: added=%v err=%v", added, err)
	}
	if _, err := f.st.DB().Exec(ctx, "TRUNCATE provisioner.client_map, provisioner.xui_servers CASCADE"); err != nil {
		t.Fatal(err)
	}
	if added, err := strict.EnsureDefaultServer(ctx, "http://insecure.example.com", "tok", "", false); err == nil || added {
		t.Fatalf("an http panel URL must be refused at startup: added=%v err=%v", added, err)
	}
	if added, err := strict.EnsureDefaultServer(ctx, "https://panel.example.com", "tok", "", false); err != nil || !added {
		t.Fatalf("first start: added=%v err=%v", added, err)
	}
}

// The provisioner holds the 3x-ui token: nothing but core may call it, so a
// compromised bot cannot delete or create VPN clients directly.
func TestOnlyCoreMayCall(t *testing.T) {
	f := newFixture(t)
	conn, err := grpcx.Dial(f.addr, "the-bots-token-is-not-accepted-here")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_, err = provisionerv1.NewProvisionerServiceClient(conn).DeleteClient(context.Background(),
		&provisionerv1.DeleteClientRequest{SubscriptionId: sub1})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
}
