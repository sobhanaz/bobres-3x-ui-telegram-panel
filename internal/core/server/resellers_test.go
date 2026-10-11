package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The bot's reseller calls: tier prices in ListPlans, labels on orders and
// services, the paged and searched services list, renaming, the panel.
func TestResellerCallsOverGRPC(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	dom := domain.New(f.st, domain.Config{})
	plan := &store.Plan{NameI18n: map[string]string{"en": "M"}, Kind: "time", DurationDays: ptr32(30), Price: 100_000, Currency: "IRT", Enabled: true}
	if err := f.st.UpsertPlan(ctx, f.st.Conn(), plan); err != nil {
		t.Fatal(err)
	}
	admin, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 60})
	if err := f.st.SetUserRole(ctx, f.st.Conn(), admin.GetId(), "admin", "active"); err != nil {
		t.Fatal(err)
	}
	actor, _ := f.st.GetUser(ctx, f.st.Conn(), admin.GetId())
	tier, err := dom.CreateTier(ctx, actor, domain.TierInput{Name: "Gold", DiscountPercent: 20, CommissionPercent: 10}, "")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 61})
	cust, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 62})
	if _, err := dom.MakeReseller(ctx, actor, r.GetId(), domain.ResellerInput{TierID: tier.ID,
		CreditLimits: map[string]int64{"IRT": 1_000_000}}, "shop"); err != nil {
		t.Fatal(err)
	}

	// Prices: the reseller's, the store's for a customer and without a user.
	price := func(userID string) (int64, int64) {
		t.Helper()
		resp, err := c.ListPlans(ctx, &corev1.ListPlansRequest{UserId: userID})
		if err != nil || len(resp.GetPlans()) != 1 {
			t.Fatalf("ListPlans(%q): %v %+v", userID, err, resp)
		}
		p := resp.GetPlans()[0]
		return p.GetPrice().GetAmount(), p.GetListPrice().GetAmount()
	}
	if got, list := price(r.GetId()); got != 80_000 || list != 100_000 {
		t.Fatalf("reseller prices: %d %d", got, list)
	}
	if got, list := price(cust.GetId()); got != 100_000 || list != 100_000 {
		t.Fatalf("customer prices: %d %d", got, list)
	}
	if got, list := price(""); got != 100_000 || list != 100_000 {
		t.Fatalf("store prices: %d %d", got, list)
	}
	if _, err := c.ListPlans(ctx, &corev1.ListPlansRequest{UserId: "00000000-0000-0000-0000-000000000099"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown user: %v", err)
	}

	// Wallet with credit; an order with a label paid on credit.
	if w, err := c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: r.GetId(), Currency: "IRT"}); err != nil || w.GetCreditLimit() != 1_000_000 || w.GetBalance() != 0 {
		t.Fatalf("wallet: %v %+v", err, w)
	}
	o, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: r.GetId(), PlanId: plan.ID, IdempotencyKey: "rord-1", Label: "Ali's phone"})
	if err != nil || o.GetLabel() != "Ali's phone" || o.GetAmount().GetAmount() != 80_000 {
		t.Fatalf("order: %v %+v", err, o)
	}
	if _, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: r.GetId(), PlanId: plan.ID, IdempotencyKey: "rord-x", DiscountCode: "ANY"}); status.Code(err) != codes.FailedPrecondition ||
		!strings.Contains(err.Error(), "discount: reseller") {
		t.Fatalf("a code for a reseller: %v", err)
	}
	if _, err := c.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: r.GetId(), PlanId: plan.ID, IdempotencyKey: "rord-y", Label: strings.Repeat("x", 65)}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("a long label: %v", err)
	}
	if _, err := c.PayOrderWithWallet(ctx, &corev1.PayOrderWithWalletRequest{OrderId: o.GetId(), UserId: r.GetId()}); err != nil {
		t.Fatalf("pay on credit: %v", err)
	}
	if w, _ := c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: r.GetId(), Currency: "IRT"}); w.GetBalance() != -80_000 {
		t.Fatalf("balance: %+v", w)
	}

	// Services, named; the default list is unchanged, paging and search on request.
	var subs []string
	for i := 0; i < 3; i++ {
		ord, _, err := f.st.CreateOrder(ctx, f.st.Conn(), &store.Order{UserID: r.GetId(), PlanID: plan.ID, Type: "new", Status: "active",
			Amount: 1, Currency: "IRT", IdempotencyKey: fmt.Sprintf("seed-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		label := fmt.Sprintf("customer %d", i)
		sub, err := f.st.CreateSubscription(ctx, f.st.Conn(), &store.Subscription{UserID: r.GetId(), OrderID: ord.ID,
			ClientEmail: fmt.Sprintf("seed-%d", i), Label: &label})
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, sub.ID)
	}
	all, err := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: r.GetId()})
	if err != nil || len(all.GetSubscriptions()) != 3 || all.GetPageInfo() != nil || all.GetSubscriptions()[0].GetLabel() != "customer 2" {
		t.Fatalf("default list: %v %+v", err, all)
	}
	page, err := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: r.GetId(), Pagination: &commonv1.Pagination{Page: 2, PageSize: 2}})
	if err != nil || len(page.GetSubscriptions()) != 1 || page.GetPageInfo().GetTotal() != 3 || page.GetPageInfo().GetPage() != 2 ||
		page.GetSubscriptions()[0].GetId() != subs[0] {
		t.Fatalf("page 2: %v %+v", err, page)
	}
	big, _ := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: r.GetId(), Pagination: &commonv1.Pagination{PageSize: 500}})
	if big.GetPageInfo().GetPageSize() != 50 || big.GetPageInfo().GetPage() != 1 {
		t.Fatalf("page size capped at 50: %+v", big.GetPageInfo())
	}
	found, err := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: r.GetId(), Query: "CUSTOMER 1"})
	if err != nil || len(found.GetSubscriptions()) != 1 || found.GetSubscriptions()[0].GetId() != subs[1] || found.GetPageInfo().GetTotal() != 1 {
		t.Fatalf("search: %v %+v", err, found)
	}
	short := strings.ReplaceAll(subs[2], "-", "")[26:]
	if found, _ := c.ListSubscriptions(ctx, &corev1.ListSubscriptionsRequest{UserId: r.GetId(), Query: "#" + short}); len(found.GetSubscriptions()) != 1 || found.GetSubscriptions()[0].GetId() != subs[2] {
		t.Fatalf("search by id: %+v", found)
	}

	// Renaming: resellers only, their own services only.
	got, err := c.SetSubscriptionLabel(ctx, &corev1.SetSubscriptionLabelRequest{UserId: r.GetId(), SubscriptionId: subs[0], Label: "Reza"})
	if err != nil || got.GetLabel() != "Reza" || got.GetId() != subs[0] {
		t.Fatalf("rename: %v %+v", err, got)
	}
	if got, err := c.SetSubscriptionLabel(ctx, &corev1.SetSubscriptionLabelRequest{UserId: r.GetId(), SubscriptionId: subs[0]}); err != nil || got.GetLabel() != "" {
		t.Fatalf("clear: %v %+v", err, got)
	}
	if _, err := c.SetSubscriptionLabel(ctx, &corev1.SetSubscriptionLabelRequest{UserId: cust.GetId(), SubscriptionId: subs[0], Label: "x"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a customer renamed: %v", err)
	}
	r2, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 63})
	if _, err := dom.MakeReseller(ctx, actor, r2.GetId(), domain.ResellerInput{TierID: tier.ID}, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetSubscriptionLabel(ctx, &corev1.SetSubscriptionLabelRequest{UserId: r2.GetId(), SubscriptionId: subs[0], Label: "x"}); status.Code(err) != codes.NotFound {
		t.Fatalf("another reseller's service: %v", err)
	}
	if _, err := c.SetSubscriptionLabel(ctx, &corev1.SetSubscriptionLabelRequest{UserId: r.GetId(), SubscriptionId: subs[0], Label: "a\nb"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("two lines: %v", err)
	}

	// The panel.
	if _, err := c.GetResellerInfo(ctx, &corev1.GetResellerInfoRequest{UserId: cust.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("a customer's panel: %v", err)
	}
	if _, err := f.st.UpsertUser(ctx, f.st.Conn(), 64, "", "", r.GetId()); err != nil {
		t.Fatal(err)
	}
	info, err := c.GetResellerInfo(ctx, &corev1.GetResellerInfoRequest{UserId: r.GetId()})
	if err != nil || info.GetTierName() != "Gold" || info.GetDiscountPercent() != 20 || info.GetCommissionPercent() != 10 ||
		info.GetCustomers() != 1 || len(info.GetRefCode()) != 8 || len(info.GetBalances()) != 1 {
		t.Fatalf("panel: %v %+v", err, info)
	}
	if b := info.GetBalances()[0]; b.GetCurrency() != "IRT" || b.GetBalance() != -80_000 || b.GetCreditLimit() != 1_000_000 || b.GetAvailable() != 920_000 {
		t.Fatalf("panel balance: %+v", b)
	}
	if len(info.GetCommissionEarned()) != 0 {
		t.Fatalf("commission: %+v", info.GetCommissionEarned())
	}
}

func ptr32(v int32) *int32 { return &v }
