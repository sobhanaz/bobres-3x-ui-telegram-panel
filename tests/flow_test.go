// Package tests holds cross-service tests (see internal/testenv).
package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const ownerTG = 777

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
	s := testenv.Start(t, ownerTG)
	ctx := context.Background()
	c := s.Core

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
	if err != nil || !strings.HasPrefix(links.GetSubscriptionLink(), testenv.SubBase) || len(links.GetQrPng()) == 0 {
		t.Fatalf("links: %v %+v", err, links)
	}
	if !s.Panel.Has(sub.GetClientEmail()) {
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
	evs, err := s.Feed.PullEvents(ctx, &eventsv1.PullEventsRequest{Limit: 100})
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
