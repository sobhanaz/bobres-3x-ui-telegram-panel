// Package tests holds cross-service tests: every service runs in-process on
// real gRPC (with per-service tokens), sharing one throwaway database the way
// production shares one Postgres, and a fake 3x-ui panel.
package tests

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/paymentsclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/provisionerclient"
	coreserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/server"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	paydomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	payserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/server"
	paystore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	provserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/server"
	provstore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
	"google.golang.org/grpc"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const (
	coreToken = "flow-core-token-0123456789abcdef" //nolint:gosec // test fixture, gitleaks:allow
	botToken  = "flow-bot-token-0123456789abcdef"  //nolint:gosec // test fixture, gitleaks:allow
	panelTok  = "flow-panel-token"                 //gitleaks:allow test fixture
	ownerTG   = 777
)

type stack struct {
	core  corev1.CoreServiceClient
	feed  eventsv1.EventFeedServiceClient
	panel *xuifake.Server
}

func serve(t *testing.T, gs *grpc.Server) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis.Addr().String()
}

func dial(t *testing.T, addr, token string) *grpc.ClientConn {
	t.Helper()
	c, err := grpcx.Dial(addr, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func startStack(t *testing.T) *stack {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dsn := testdb.DSN(t)
	for _, schema := range migrate.Schemas() {
		if err := migrate.Up(ctx, dsn, schema); err != nil {
			t.Fatalf("migrate %s: %v", schema, err)
		}
	}

	// payments (accepts core only)
	ps, err := paystore.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ps.Close)
	pgs, _ := grpcx.NewServer(nil, grpcauth.Peer{Name: "core", Token: coreToken})
	payserver.New(paydomain.New(ps), ps).Register(pgs)
	eventsv1.RegisterEventFeedServiceServer(pgs, eventbus.NewFeedServer(eventbus.NewFeed(ps.DB(), "outbox_payments")))
	payAddr := serve(t, pgs)

	// provisioner (accepts core only) + fake panel
	panel := xuifake.NewServer(panelTok)
	t.Cleanup(panel.Close)
	env, _ := bcrypto.NewEnvelope([]byte("flow-test-master-key-0123456789abcdef"))
	vs, err := provstore.New(ctx, dsn, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vs.Close)
	prov := provserver.New(vs, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if _, err := prov.EnsureDefaultServer(ctx, panel.URL, panelTok, "https://sub.example.test/sub/", true); err != nil {
		t.Fatal(err)
	}
	vgs, _ := grpcx.NewServer(nil, grpcauth.Peer{Name: "core", Token: coreToken})
	prov.Register(vgs)
	provAddr := serve(t, vgs)

	// core (accepts the bot only), wired like cmd/core
	cs, err := corestore.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cs.Close)
	dom := coredomain.New(cs, coredomain.Config{OwnerTelegramID: ownerTG})
	payConn := dial(t, payAddr, coreToken)
	payClient := paymentsclient.New(paymentsv1.NewPaymentsServiceClient(payConn))
	provClient := provisionerclient.New(provisionerv1.NewProvisionerServiceClient(dial(t, provAddr, coreToken)))
	csrv := coreserver.New(cs, dom)
	csrv.SetPayments(payClient)
	csrv.SetProvisioner(provClient)
	consumer, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
		Source:     eventbus.NewGRPCSource(eventsv1.NewEventFeedServiceClient(payConn)),
		Handle:     dom.HandlePaymentEvent,
		DeadLetter: dom.DeadLetter("payments"),
		Interval:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = consumer.Run(ctx) }()
	worker := dom.NewProvisionWorker(provClient, nil)
	worker.Interval = 50 * time.Millisecond
	go func() { _ = worker.Run(ctx) }()
	cgs, _ := grpcx.NewServer(nil, grpcauth.Peer{Name: "bot", Token: botToken})
	csrv.Register(cgs)
	eventsv1.RegisterEventFeedServiceServer(cgs, eventbus.NewFeedServer(eventbus.NewFeed(cs.DB(), "outbox_core")))
	coreConn := dial(t, serve(t, cgs), botToken)
	return &stack{
		core:  corev1.NewCoreServiceClient(coreConn),
		feed:  eventsv1.NewEventFeedServiceClient(coreConn),
		panel: panel,
	}
}

func waitOrder(t *testing.T, c corev1.CoreServiceClient, id, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		o, err := c.GetOrder(context.Background(), &corev1.GetOrderRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		if o.GetStatus() == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("order %s is %q, want %q", id, o.GetStatus(), want)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

// The first sellable slice, as the bot will drive it: an admin sets up the
// store, a user pays by card, the admin approves in the review queue, and the
// user gets a working subscription link. Then a trial and a wallet purchase.
func TestPurchaseLoop(t *testing.T) {
	s := startStack(t)
	ctx := context.Background()
	c := s.core

	if o, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: ownerTG, Language: "fa"}); err != nil || o.GetRole() != "owner" {
		t.Fatalf("owner: %v %+v", err, o)
	}
	plan, err := c.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: ownerTG, Plan: &corev1.Plan{
		NameI18N: map[string]string{"en": "Monthly 50 GB", "fa": "ماهانه ۵۰ گیگ"}, Kind: "both",
		DurationDays: 30, TrafficBytes: 50 << 30, Price: &commonv1.Money{Amount: 150000, Currency: "IRT"}, Enabled: true,
	}})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if _, err := c.AdminSetSetting(ctx, &corev1.AdminSetSettingRequest{ActorTelegramId: ownerTG, Key: "payments.card_number", Value: "6037-0000-1111-2222"}); err != nil {
		t.Fatal(err)
	}

	// 1. Card payment, reviewed by the owner.
	u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 1001, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	order, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: plan.GetId(), IdempotencyKey: "o1"})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := c.CreatePaymentIntent(ctx, &corev1.CreatePaymentIntentRequest{UserId: u.GetId(), OrderId: order.GetId(), Provider: "manual_card", IdempotencyKey: "i1"})
	if err != nil || intent.GetDetails()["card_number"] != "6037-0000-1111-2222" || intent.GetAmount().GetAmount() != 150000 {
		t.Fatalf("intent: %v %+v", err, intent)
	}
	if _, err := c.SubmitPaymentProof(ctx, &corev1.SubmitPaymentProofRequest{UserId: u.GetId(), IntentId: intent.GetId(), ReceiptFile: "tg-file", ReferenceNumber: "123456"}); err != nil {
		t.Fatal(err)
	}
	queue, err := c.AdminListPendingPayments(ctx, &corev1.AdminListPendingPaymentsRequest{ActorTelegramId: ownerTG})
	if err != nil || len(queue.GetPayments()) != 1 || queue.GetPayments()[0].GetUser().GetTelegramId() != 1001 {
		t.Fatalf("queue: %v %+v", err, queue)
	}
	if _, err := c.ReviewManualPayment(ctx, &corev1.ReviewManualPaymentRequest{ActorTelegramId: 1001, IntentId: intent.GetId(), Decision: "approved"}); err == nil {
		t.Fatal("a regular user approved their own payment")
	}
	if _, err := c.ReviewManualPayment(ctx, &corev1.ReviewManualPaymentRequest{ActorTelegramId: ownerTG, IntentId: intent.GetId(), Decision: "approved"}); err != nil {
		t.Fatal(err)
	}
	waitOrder(t, c, order.GetId(), "active")

	subs, err := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: u.GetId()})
	if err != nil || len(subs.GetSubscriptions()) != 1 || subs.GetSubscriptions()[0].GetStatus() != "active" {
		t.Fatalf("subscriptions: %v %+v", err, subs)
	}
	sub := subs.GetSubscriptions()[0]
	links, err := c.GetSubscriptionLinks(ctx, &corev1.GetSubscriptionLinksRequest{UserId: u.GetId(), SubscriptionId: sub.GetId()})
	if err != nil || !strings.HasPrefix(links.GetSubscriptionLink(), "https://sub.example.test/sub/") || len(links.GetQrPng()) == 0 {
		t.Fatalf("links: %v %+v", err, links)
	}
	if !s.panel.Has(sub.GetClientEmail()) {
		t.Fatalf("client %s missing on the panel", sub.GetClientEmail())
	}

	// 2. Free trial, once.
	trial, err := c.AdminUpsertPlan(ctx, &corev1.AdminUpsertPlanRequest{ActorTelegramId: ownerTG, Plan: &corev1.Plan{
		NameI18N: map[string]string{"en": "Trial"}, Kind: "both", DurationDays: 1, TrafficBytes: 1 << 30,
		Price: &commonv1.Money{Amount: 0, Currency: "IRT"}, Enabled: true, IsTrial: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := c.StartTrial(ctx, &corev1.StartTrialRequest{UserId: u.GetId(), PlanId: trial.GetId(), IdempotencyKey: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	waitOrder(t, c, tr.GetId(), "active")
	if _, err := c.StartTrial(ctx, &corev1.StartTrialRequest{UserId: u.GetId(), PlanId: trial.GetId(), IdempotencyKey: "t2"}); err == nil {
		t.Fatal("second trial accepted")
	}

	// 3. Wallet purchase after an admin top-up.
	if _, err := c.AdminAdjustBalance(ctx, &corev1.AdminAdjustBalanceRequest{ActorTelegramId: ownerTG, UserId: u.GetId(),
		Delta: &commonv1.Money{Amount: 200000, Currency: "IRT"}, Reason: "gift", IdempotencyKey: "a1"}); err != nil {
		t.Fatal(err)
	}
	o2, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: plan.GetId(), IdempotencyKey: "o2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o2.GetId(), UserId: u.GetId()}); err != nil {
		t.Fatal(err)
	}
	waitOrder(t, c, o2.GetId(), "active")
	w, _ := c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: u.GetId(), Currency: "IRT"})
	if w.GetBalance() != 50000 {
		t.Fatalf("wallet after purchase = %d, want 50000", w.GetBalance())
	}

	// The bot's view: everything it must tell the user is on core's feed.
	evs, err := s.feed.PullEvents(ctx, &eventsv1.PullEventsRequest{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	topics := map[string]int{}
	for _, e := range evs.GetEvents() {
		topics[e.GetTopic()]++
	}
	if topics["subscription.provisioned.v1"] != 3 || topics["order.paid.v1"] != 3 || topics["wallet.credited.v1"] != 1 {
		t.Fatalf("core feed: %v", topics)
	}
}
