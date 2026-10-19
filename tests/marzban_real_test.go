//go:build integration

package tests

// The purchase loop against a REAL Marzban panel: core, payments and the
// provisioner run in-process (internal/testenv) on real gRPC and a real
// database, and the panel registered with them is the Marzban container, not
// a fake. The test acts as the owner and as a customer the way the bot and the
// dashboard do (the same core calls), then reads the panel itself to see what
// BOBRES left there. Needs the variables of the adapter's integration tests:
//
//	MARZBAN_TEST_URL, MARZBAN_TEST_USER, MARZBAN_TEST_PASSWORD, MARZBAN_TEST_ALLOW_PRIVATE=1,
//	MARZBAN_TEST_REQUIRE=1 (a missing variable fails instead of skipping)
//	MARZBAN_TEST_OWNERLESS=1 when the credentials are the panel's SUDO_USERNAME account, whose users have no owner admin
//
//	go test -tags integration -run MarzbanReal ./tests/
//
// What it does not cover: the Telegram side (the bot is a thin client of the
// same core calls, and tests/e2e drives it against a 3x-ui panel). The
// dashboard's HTTP layer is covered by internal/core/web's real-panel test, and
// traffic (usage, quota, a client kept out) by the adapter's.

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/marzban"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testenv"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func realMarzbanEnv(t *testing.T) (baseURL, user, pass string) {
	t.Helper()
	baseURL, user, pass = os.Getenv("MARZBAN_TEST_URL"), os.Getenv("MARZBAN_TEST_USER"), os.Getenv("MARZBAN_TEST_PASSWORD")
	if baseURL == "" || user == "" || pass == "" {
		if os.Getenv("MARZBAN_TEST_REQUIRE") == "1" {
			t.Fatal("MARZBAN_TEST_URL, MARZBAN_TEST_USER and MARZBAN_TEST_PASSWORD not set and MARZBAN_TEST_REQUIRE=1")
		}
		t.Skip("MARZBAN_TEST_URL, MARZBAN_TEST_USER and MARZBAN_TEST_PASSWORD not set")
	}
	return baseURL, user, pass
}

func TestMarzbanRealPanelPurchaseLoop(t *testing.T) {
	baseURL, user, pass := realMarzbanEnv(t)
	allowPrivate := os.Getenv("MARZBAN_TEST_ALLOW_PRIVATE") == "1"
	s := testenv.Start(t, ownerTG)
	ctx := context.Background()
	c := s.Core

	if o, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: ownerTG, Language: "en"}); err != nil || o.GetRole() != "owner" {
		t.Fatalf("owner: %v %+v", err, o)
	}
	owner, err := s.Store.GetUserByTelegramID(ctx, s.Store.DB(), ownerTG)
	if err != nil {
		t.Fatal(err)
	}

	// The panel itself, for looking at what BOBRES did.
	var opts []xui.Option
	if allowPrivate {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	panel, err := marzban.New(baseURL, user, pass, opts...)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The owner registers the panel as the dashboard's "add server" form
	// does: kind Marzban, an admin's username:password where 3x-ui has a token.
	sv, err := s.Domain.AddServer(ctx, owner, s.Prov, domain.ServerInput{
		Kind: domain.KindMarzban, Name: "real marzban", BaseURL: baseURL, Token: user + ":" + pass,
		Weight: 1, Enabled: true, AllowPrivate: allowPrivate,
	})
	if err != nil {
		t.Fatalf("add the Marzban server: %v", err)
	}
	if sv.Kind != domain.KindMarzban || !sv.HasToken || sv.VersionUntested || !strings.HasPrefix(sv.PanelVersion, "v") || !sv.Healthy {
		t.Fatalf("server: %+v", sv)
	}
	t.Logf("registered Marzban %s", sv.PanelVersion)
	if want := os.Getenv("MARZBAN_TEST_VERSION"); want != "" && sv.PanelVersion != want {
		t.Fatalf("panel version %q, want %q", sv.PanelVersion, want)
	}
	inbounds, err := s.Prov.ListInbounds(ctx, sv.ID)
	if err != nil || len(inbounds) == 0 {
		t.Fatalf("inbounds of the Marzban server: %v %v", inbounds, err)
	}

	// 2. A plan that sells on that server only (the stack's own fake 3x-ui
	// panel stays out of it), and a traffic package.
	days, gb50, gb10 := int32(30), int64(50)<<30, int64(10)<<30
	plan, err := s.Domain.UpsertPlan(ctx, owner, &store.Plan{
		NameI18n: map[string]string{"en": "Monthly 50 GB"}, Kind: "both", DurationDays: &days, TrafficBytes: &gb50,
		Price: 150000, Currency: "IRT", Enabled: true, ServerIDs: []string{sv.ID},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	topup, err := s.Domain.UpsertPlan(ctx, owner, &store.Plan{
		NameI18n: map[string]string{"en": "10 GB more"}, Kind: "traffic", TrafficBytes: &gb10,
		Price: 20000, Currency: "IRT", Enabled: true, IsTopup: true,
	})
	if err != nil {
		t.Fatalf("top-up plan: %v", err)
	}

	// 3. A customer (a Telegram id of two digits or more: Marzban's username
	// rule) is credited and buys with the wallet.
	u, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 1001, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AdminAdjustBalance(ctx, &corev1.AdminAdjustBalanceRequest{ActorTelegramId: ownerTG, UserId: u.GetId(),
		Delta: &commonv1.Money{Amount: 1_000_000, Currency: "IRT"}, Reason: "gift", IdempotencyKey: "a1"}); err != nil {
		t.Fatal(err)
	}
	buy := func(key string, order *corev1.CreateOrderRequest) {
		t.Helper()
		order.UserId, order.IdempotencyKey = u.GetId(), key
		o, err := c.CreateOrder(ctx, order)
		if err != nil {
			t.Fatalf("order %s: %v", key, err)
		}
		if _, err := c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o.GetId(), UserId: u.GetId()}); err != nil {
			t.Fatalf("pay %s: %v", key, err)
		}
		waitOrder(t, c, o.GetId(), "active")
	}
	buy("o1", &corev1.CreateOrderRequest{PlanId: plan.ID})

	subs, err := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: u.GetId()})
	if err != nil || len(subs.GetSubscriptions()) != 1 || subs.GetSubscriptions()[0].GetStatus() != "active" {
		t.Fatalf("subscriptions: %v %+v", err, subs)
	}
	sub := subs.GetSubscriptions()[0]
	email := sub.GetClientEmail()
	if sub.GetServerId() != sv.ID || !strings.HasPrefix(email, "u1001-") {
		t.Fatalf("subscription %+v is not on the Marzban server %s", sub, sv.ID)
	}
	t.Cleanup(func() { _ = panel.DeleteUser(context.Background(), email) })

	// 4. What is on the panel: a real Marzban user with the plan's limits.
	pu, err := panel.GetUser(ctx, email)
	if err != nil {
		t.Fatalf("the client %s is not on the panel: %v", email, err)
	}
	wantExpire := time.Now().Add(30 * 24 * time.Hour).Unix()
	if pu.Status != marzban.StatusActive || pu.DataLimitBytes() != gb50 || abs(pu.ExpireSeconds()-wantExpire) > 120 ||
		!strings.HasPrefix(pu.NoteText(), "bobres-sub:") || pu.HasOwner() != (os.Getenv("MARZBAN_TEST_OWNERLESS") != "1") {
		t.Fatalf("panel user %+v, want an active one with 50 GB and 30 days", pu)
	}
	if len(pu.Links) < len(inbounds) {
		t.Fatalf("the panel user has %d share link(s) for %d inbound(s)", len(pu.Links), len(inbounds))
	}
	if sub.GetExpiresAt() != pu.ExpireSeconds() || sub.GetTrafficTotalBytes() != gb50 {
		t.Fatalf("core holds expiry %d and %d bytes, the panel %d and %d", sub.GetExpiresAt(), sub.GetTrafficTotalBytes(), pu.ExpireSeconds(), pu.DataLimitBytes())
	}

	// 5. The subscription link the customer gets is the panel's own, and it
	// serves the client's share links.
	links, err := c.GetSubscriptionLinks(ctx, &corev1.GetSubscriptionLinksRequest{UserId: u.GetId(), SubscriptionId: sub.GetId()})
	if err != nil || len(links.GetQrPng()) == 0 || len(links.GetConfigLinks()) != len(pu.Links) {
		t.Fatalf("links: %v %+v", err, links)
	}
	link := links.GetSubscriptionLink()
	if !strings.HasPrefix(link, strings.TrimRight(baseURL, "/")+"/sub/") {
		t.Fatalf("subscription link %q is not the panel's /sub/ address", link)
	}
	if n := fetchSubscription(t, link); n != len(pu.Links) {
		t.Fatalf("the subscription link serves %d link(s), the panel user has %d", n, len(pu.Links))
	}

	// 6. Renewal: the customer renews with the wallet, the staff add traffic,
	// and the panel holds the new absolute limits. The client keeps its
	// identity: the same share links.
	buy("o2", &corev1.CreateOrderRequest{PlanId: plan.ID, Type: "renew", SubscriptionId: sub.GetId()})
	after, err := panel.GetUser(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != marzban.StatusActive || after.DataLimitBytes() != 2*gb50 || after.ExpireSeconds() <= pu.ExpireSeconds()+29*86400 ||
		strings.Join(after.Links, "\n") != strings.Join(pu.Links, "\n") {
		t.Fatalf("after the renewal the panel user is %+v, want 100 GB, about 60 days and the same links", after)
	}
	buy("o3", &corev1.CreateOrderRequest{PlanId: topup.ID, Type: "traffic_topup", SubscriptionId: sub.GetId()})
	if got, err := panel.GetUser(ctx, email); err != nil || got.DataLimitBytes() != 2*gb50+gb10 {
		t.Fatalf("after the top-up: %+v %v, want %d bytes", got, err, 2*gb50+gb10)
	}

	// 6b. A plan that chooses one inbound of the panel: the client carries that
	// inbound's links only (the ids are the ones the adapter derived from the
	// panel's protocol and tag).
	var pick *domain.Inbound
	for i := range inbounds {
		if inbounds[i].Protocol != inbounds[0].Protocol {
			pick = &inbounds[i]
			break
		}
	}
	if pick != nil { // the panel serves at least two protocols
		single, err := s.Domain.UpsertPlan(ctx, owner, &store.Plan{
			NameI18n: map[string]string{"en": "One inbound"}, Kind: "both", DurationDays: &days, TrafficBytes: &gb10,
			Price: 10000, Currency: "IRT", Enabled: true, ServerIDs: []string{sv.ID},
			Inbounds: []store.PlanInbound{{ServerID: sv.ID, InboundIDs: []int32{pick.ID}}},
		})
		if err != nil {
			t.Fatalf("plan with an inbound: %v", err)
		}
		buy("o4", &corev1.CreateOrderRequest{PlanId: single.ID})
		subs, err := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: u.GetId()})
		if err != nil || len(subs.GetSubscriptions()) != 2 {
			t.Fatalf("subscriptions: %v %+v", err, subs)
		}
		other := subs.GetSubscriptions()[0].GetClientEmail()
		if other == email {
			other = subs.GetSubscriptions()[1].GetClientEmail()
		}
		t.Cleanup(func() { _ = panel.DeleteUser(context.Background(), other) })
		ou, err := panel.GetUser(ctx, other)
		if err != nil || len(ou.Links) == 0 {
			t.Fatalf("the client on one inbound: %+v %v", ou, err)
		}
		scheme := pick.Protocol
		if scheme == "shadowsocks" {
			scheme = "ss"
		}
		for _, l := range ou.Links {
			if !strings.HasPrefix(l, scheme+"://") {
				t.Fatalf("a client on the %s inbound %q carries the link %.20q", pick.Protocol, pick.Tag, l)
			}
		}
	}

	// 7. Staff turn the service off and on, and reset its traffic.
	if _, err := s.Domain.SetSubscriptionEnabled(ctx, owner, s.Prov, sub.GetId(), false, "e2e"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got, err := panel.GetUser(ctx, email); err != nil || got.Status != marzban.StatusDisabled {
		t.Fatalf("after disabling: %+v %v", got, err)
	}
	if err := s.Domain.ResetSubscriptionTraffic(ctx, owner, s.Prov, sub.GetId(), "e2e"); err != nil {
		t.Fatalf("reset traffic of a disabled service: %v", err)
	}
	if got, err := panel.GetUser(ctx, email); err != nil || got.Status != marzban.StatusDisabled || got.UsedTraffic != 0 {
		t.Fatalf("a reset must not switch a disabled service on: %+v %v", got, err)
	}
	if _, err := s.Domain.SetSubscriptionEnabled(ctx, owner, s.Prov, sub.GetId(), true, "e2e"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got, err := panel.GetUser(ctx, email); err != nil || got.Status != marzban.StatusActive || got.DataLimitBytes() != 2*gb50+gb10 {
		t.Fatalf("after enabling: %+v %v", got, err)
	}
	if err := s.Domain.SyncSubscription(ctx, s.Prov, sub.GetId()); err != nil {
		t.Fatalf("usage sync: %v", err)
	}
	subs, err = c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: u.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	var synced *corev1.Subscription
	for _, x := range subs.GetSubscriptions() {
		if x.GetId() == sub.GetId() {
			synced = x
		}
	}
	if synced == nil {
		t.Fatalf("the service is not in the list: %+v", subs)
	}
	if synced.GetStatus() != "active" || synced.GetTrafficTotalBytes() != 2*gb50+gb10 || synced.GetTrafficUsedBytes() != 0 ||
		synced.GetExpiresAt() != after.ExpireSeconds() {
		t.Fatalf("core after the sync: %+v, the panel: %+v", synced, after)
	}

	// 8. Staff delete the service: the client is gone from the panel.
	if err := s.Domain.DeleteSubscription(ctx, owner, s.Prov, sub.GetId(), "e2e"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := panel.GetUser(ctx, email); !marzban.IsNotFound(err) {
		t.Fatalf("after the deletion the panel still answers: %v", err)
	}
	if resp, err := (&http.Client{Timeout: 15 * time.Second}).Get(link); err != nil || resp.StatusCode != http.StatusNotFound {
		status := 0
		if resp != nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
		}
		t.Fatalf("the old subscription link after the deletion: HTTP %d, %v, want 404", status, err)
	} else {
		_ = resp.Body.Close()
	}
	// Deleting a deleted service again is fine.
	if err := s.Domain.DeleteSubscription(ctx, owner, s.Prov, sub.GetId(), "e2e again"); err != nil {
		t.Fatalf("a second delete: %v", err)
	}
}

// A Telegram id with one digit makes a username the panel refuses; the order
// fails on the reservation (provision_failed) and is never delivered.
func TestMarzbanRealPanelRefusedUsername(t *testing.T) {
	baseURL, user, pass := realMarzbanEnv(t)
	allowPrivate := os.Getenv("MARZBAN_TEST_ALLOW_PRIVATE") == "1"
	s := testenv.Start(t, ownerTG)
	ctx := context.Background()
	c := s.Core
	if _, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: ownerTG, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	owner, err := s.Store.GetUserByTelegramID(ctx, s.Store.DB(), ownerTG)
	if err != nil {
		t.Fatal(err)
	}
	sv, err := s.Domain.AddServer(ctx, owner, s.Prov, domain.ServerInput{
		Kind: domain.KindMarzban, Name: "real marzban", BaseURL: baseURL, Token: user + ":" + pass,
		Weight: 1, Enabled: true, AllowPrivate: allowPrivate,
	})
	if err != nil {
		t.Fatal(err)
	}
	days := int32(1)
	trial, err := s.Domain.UpsertPlan(ctx, owner, &store.Plan{
		NameI18n: map[string]string{"en": "Trial"}, Kind: "time", DurationDays: &days,
		Price: 0, Currency: "IRT", Enabled: true, IsTrial: true, ServerIDs: []string{sv.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	small, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	o, err := c.StartTrial(ctx, &corev1.StartTrialRequest{UserId: small.GetId(), PlanId: trial.ID, IdempotencyKey: "t1"})
	if err != nil {
		t.Fatalf("start trial: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := c.GetOrder(ctx, &corev1.GetOrderRequest{Id: o.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		switch got.GetStatus() {
		case "active":
			t.Fatal("an order for a username the panel refuses was delivered")
		case "provision_failed":
			return // the delivery failed on the reservation; staff see it in the orders list
		}
		if time.Now().After(deadline) {
			t.Fatalf("order is %q, want provision_failed", got.GetStatus())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// fetchSubscription reads a subscription URL as a client app does and returns
// how many share links it holds.
func fetchSubscription(t *testing.T, link string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, link, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "bobres-integration-test")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("fetch %s: %v", link, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the subscription link answered HTTP %d: %q", resp.StatusCode, raw)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(fmt.Errorf("the subscription body is not base64: %w", err))
	}
	return len(strings.Split(strings.TrimSpace(string(dec)), "\n"))
}
