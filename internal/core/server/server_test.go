package server

import (
	"context"
	"net"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const botToken = "test-bot-token-0123456789abcdef" //nolint:gosec // test fixture, gitleaks:allow

type fixture struct {
	st   *store.Store
	addr string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB().Exec(ctx, "TRUNCATE core.ledger_entries, core.wallets, core.subscriptions, core.orders, core.plans, core.users CASCADE"); err != nil {
		t.Fatal(err)
	}
	gs, err := grpcx.NewServer(nil, grpcauth.Peer{Name: "bot", Token: botToken})
	if err != nil {
		t.Fatal(err)
	}
	New(st, domain.New(st, domain.Config{})).Register(gs)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return &fixture{st: st, addr: lis.Addr().String()}
}

func (f *fixture) client(t *testing.T, token string) corev1.CoreServiceClient {
	t.Helper()
	conn, err := grpcx.Dial(f.addr, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return corev1.NewCoreServiceClient(conn)
}

func TestUserFlowOverGRPC(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()

	u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 42, Language: "en"}) // no @username
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	if u.GetTelegramId() != 42 || u.GetRole() != "user" {
		t.Fatalf("bad user: %+v", u)
	}
	got, err := c.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: 42}})
	if err != nil || got.GetId() != u.GetId() {
		t.Fatalf("GetUser: %v %+v", err, got)
	}
	w, err := c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: u.GetId(), Currency: "IRT"})
	if err != nil || w.GetBalance() != 0 {
		t.Fatalf("GetWallet: %v %+v", err, w)
	}
	led, err := c.ListLedgerEntries(ctx, &corev1.ListLedgerEntriesRequest{UserId: u.GetId()})
	if err != nil || len(led.GetEntries()) != 0 {
		t.Fatalf("ListLedgerEntries: %v %+v", err, led)
	}
}

func TestErrorsMapToActionableCodes(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	if _, err := c.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_Id{Id: "00000000-0000-0000-0000-000000000000"}}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown user: %v", err)
	}
	if _, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 0}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid input: %v", err)
	}
	u, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 43})
	p := &store.Plan{NameI18n: map[string]string{"en": "T"}, Kind: "time", Price: 0, Currency: "IRT", Enabled: true, IsTrial: true}
	_ = f.st.UpsertPlan(ctx, f.st.Conn(), p)
	if _, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: p.ID, IdempotencyKey: "k"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("trial plan via CreateOrder: %v", err)
	}
}

func TestOnlyTheBotMayCall(t *testing.T) {
	f := newFixture(t)
	if _, err := f.client(t, "a-different-services-token").GetUser(context.Background(),
		&corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: 1}}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("want Unauthenticated, got %v", err)
	}
}
