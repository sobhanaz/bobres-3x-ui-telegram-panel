package server

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

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

func newFixture(t *testing.T) *fixture { return newFixtureWith(t, nil) }

// newFixtureWith is newFixture with a hook to set the server up (a public URL).
func newFixtureWith(t *testing.T, setup func(*Server)) *fixture {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "core"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st, err := store.New(ctx, testdb.WithPoolCap(t, dsn))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB().Exec(ctx, "TRUNCATE core.ledger_entries, core.wallets, core.subscriptions, core.orders, core.plans, core.users, core.reseller_tiers CASCADE"); err != nil {
		t.Fatal(err)
	}
	gs, err := grpcx.NewServer(nil, grpcauth.Peer{Name: "bot", Token: botToken})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, domain.New(st, domain.Config{}))
	if setup != nil {
		setup(srv)
	}
	srv.Register(gs)
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

// A new purchase refused for the active-services limit tells the bot only
// that purchases are paused: nothing about a license, the free tier or the
// trial can reach a customer. Renewals and top-ups still go through.
func TestRefusedPurchaseSaysNothingAboutLicensing(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	// The trial is over, and the free tier's 25 services are all in use.
	if _, err := f.st.DB().Exec(ctx, `UPDATE core.install SET trial_started_at = now() - interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.st.DB().Exec(ctx, `UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`)
	})
	plan := &store.Plan{NameI18n: map[string]string{"en": "Month"}, Kind: "time", Price: 1000, Currency: "IRT", Enabled: true}
	trial := &store.Plan{NameI18n: map[string]string{"en": "Trial"}, Kind: "time", Price: 0, Currency: "IRT", Enabled: true, IsTrial: true}
	for _, p := range []*store.Plan{plan, trial} {
		if err := f.st.UpsertPlan(ctx, f.st.Conn(), p); err != nil {
			t.Fatal(err)
		}
	}
	expires := time.Now().Add(10 * 24 * time.Hour)
	var first *store.Subscription
	for i := range 25 {
		u, err := f.st.UpsertUser(ctx, f.st.Conn(), int64(7000+i), "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		o, _, err := f.st.CreateOrder(ctx, f.st.Conn(), &store.Order{UserID: u.ID, PlanID: plan.ID, Type: "new", Status: "active",
			Amount: 1000, Currency: "IRT", IdempotencyKey: fmt.Sprintf("seed-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := f.st.CreateSubscription(ctx, f.st.Conn(), &store.Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: fmt.Sprintf("u%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.st.ActivateSubscription(ctx, f.st.Conn(), sub.ID, "00000000-0000-7000-8000-000000000001", "x", "", &expires, nil); err != nil {
			t.Fatal(err)
		}
		sub.UserID = u.ID
		if first == nil {
			first = sub
		}
	}
	buyer, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7100, Language: "fa"})
	if err != nil {
		t.Fatal(err)
	}
	paused := func(what string, err error) {
		t.Helper()
		st := status.Convert(err)
		if st.Code() != codes.FailedPrecondition || st.Message() != domain.PurchasesPaused {
			t.Fatalf("%s: %v", what, err)
		}
		for _, word := range []string{"licen", "free tier", "trial", "limit", "buy or renew"} {
			if strings.Contains(strings.ToLower(st.Message()), word) {
				t.Errorf("%s: the customer's error says %q: %s", what, word, st.Message())
			}
		}
	}
	_, err = c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, IdempotencyKey: "late"})
	paused("a new order", err)
	_, err = c.StartTrial(ctx, &corev1.StartTrialRequest{UserId: buyer.GetId(), PlanId: trial.ID, IdempotencyKey: "trial-late"})
	paused("a free trial", err)
	// A renewal of a service that exists is not a new service.
	if _, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: first.UserID, PlanId: plan.ID, Type: "renew",
		SubscriptionId: first.ID, IdempotencyKey: "renew-1"}); err != nil {
		t.Fatalf("a renewal over the limit: %v", err)
	}
}

// Only a new service refused for the limit, or a reseller's purchase refused
// because resellers left the entitlements, is turned into the neutral
// message; any other refusal keeps its words (it comes from a staff action).
func TestOnlyTheServiceLimitIsMadeNeutral(t *testing.T) {
	resellers := &domain.LicenseRefusal{Gate: domain.GateResellerPurchase, Msg: "the free tier does not include resellers: buy or renew a license"}
	if got := status.Convert(fail(resellers)); got.Code() != codes.FailedPrecondition || got.Message() != domain.PurchasesPaused {
		t.Fatalf("a reseller's purchase: %v", got)
	}
	staffGate := &domain.LicenseRefusal{Gate: "resellers", Msg: "the free tier does not include resellers: buy or renew a license"}
	if got := status.Convert(fail(staffGate)); !strings.Contains(got.Message(), "the free tier does not include resellers") {
		t.Fatalf("making a reseller keeps its words: %v", got)
	}
	limit := &domain.LicenseRefusal{Gate: domain.GateActiveServices, Msg: "the free tier allows 25 active services: buy or renew a license"}
	if got := status.Convert(fail(limit)); got.Code() != codes.FailedPrecondition || got.Message() != domain.PurchasesPaused {
		t.Fatalf("service limit: %v", got)
	}
	if got := status.Convert(fail(fmt.Errorf("wrapped: %w", limit))); got.Message() != domain.PurchasesPaused {
		t.Fatalf("wrapped service limit: %v", got)
	}
	other := &domain.LicenseRefusal{Gate: "max_servers", Msg: "the free tier allows 1 server"}
	if got := status.Convert(fail(other)); got.Code() != codes.FailedPrecondition || !strings.Contains(got.Message(), "the free tier allows 1 server") {
		t.Fatalf("another gate: %v", got)
	}
}
