package server

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A bulk purchase over gRPC, as the bot makes it: priced by core, paid from
// the wallet in one step, idempotent by its key, audited as made in the bot,
// with core's refusals as codes the bot can act on.
func TestBulkPurchaseOverGRPC(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	if _, err := f.st.DB().Exec(ctx, `UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`); err != nil {
		t.Fatal(err)
	}
	plan := &store.Plan{NameI18n: map[string]string{"en": "Month", "fa": "ماهانه"}, Kind: "time", Price: 1000, Currency: "IRT", Enabled: true}
	if err := f.st.UpsertPlan(ctx, f.st.Conn(), plan); err != nil {
		t.Fatal(err)
	}
	buyer, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7300, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	topUp := func(amount int64, key string) {
		t.Helper()
		if err := f.st.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := f.st.Credit(ctx, tx, buyer.GetId(), "IRT", amount, &store.LedgerEntry{Kind: "topup", IdempotencyKey: key})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	topUp(5000, "bulk-grpc-1")
	money := func(a int64) *commonv1.Money { return &commonv1.Money{Amount: a, Currency: "IRT"} }
	code := func(err error) codes.Code { return status.Code(err) }

	// The quote is core's: the unit, the total and the most the store allows.
	q, err := c.QuoteBulkOrder(ctx, &corev1.QuoteBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 3})
	if err != nil || q.GetQuantity() != 3 || q.GetUnitPrice().GetAmount() != 1000 || q.GetTotal().GetAmount() != 3000 ||
		q.GetTotal().GetCurrency() != "IRT" || q.GetMaxQuantity() != domain.DefaultBulkMax {
		t.Fatalf("quote: %v %+v", err, q)
	}

	req := &corev1.CreateBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 3, IdempotencyKey: "bulk-grpc-key-1",
		LabelPrefix: "Family", ExpectedTotal: money(3000)}
	b, err := c.CreateBulkOrder(ctx, req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if b.GetReplayed() || b.GetQuantity() != 3 || b.GetTotal().GetAmount() != 3000 || b.GetUnitPrice().GetAmount() != 1000 || b.GetLabelPrefix() != "Family" ||
		b.GetPlanNameI18N()["fa"] != "ماهانه" || len(b.GetServices()) != 3 || b.GetDeliveredAt() != 0 {
		t.Fatalf("bulk: %+v", b)
	}
	for i, sv := range b.GetServices() {
		if int(sv.GetIndex()) != i+1 || sv.GetOrderStatus() != "paid" || sv.GetLabel() != fmt.Sprintf("Family %d", i+1) || sv.GetSubscriptionId() != "" {
			t.Fatalf("service %d: %+v", i+1, sv)
		}
	}
	if w, _ := c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: buyer.GetId(), Currency: "IRT"}); w.GetBalance() != 2000 {
		t.Fatalf("balance: %+v", w)
	}
	// The same request again is the same purchase, and costs nothing more.
	again, err := c.CreateBulkOrder(ctx, req)
	if err != nil || again.GetId() != b.GetId() || !again.GetReplayed() {
		t.Fatalf("again: %v %+v", err, again)
	}
	if w, _ := c.GetWallet(ctx, &corev1.GetWalletRequest{UserId: buyer.GetId(), Currency: "IRT"}); w.GetBalance() != 2000 {
		t.Fatalf("balance after a repeat: %+v", w)
	}
	// Audited as made in the bot, under the buyer.
	var source, actor string
	if err := f.st.DB().QueryRow(ctx, `SELECT source, actor_id::text FROM core.audit_log WHERE action = 'order.bulk' AND entity_id = $1`, b.GetId()).Scan(&source, &actor); err != nil ||
		source != "bot" || actor != buyer.GetId() {
		t.Fatalf("audit: %q %q %v", source, actor, err)
	}

	// GetBulkOrder is the buyer's: another user's request is NotFound.
	got, err := c.GetBulkOrder(ctx, &corev1.GetBulkOrderRequest{BulkId: b.GetId(), UserId: buyer.GetId()})
	if err != nil || got.GetId() != b.GetId() || len(got.GetServices()) != 3 {
		t.Fatalf("get: %v %+v", err, got)
	}
	other, _ := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7301})
	if _, err := c.GetBulkOrder(ctx, &corev1.GetBulkOrderRequest{BulkId: b.GetId(), UserId: other.GetId()}); code(err) != codes.NotFound {
		t.Fatalf("someone else's bulk: %v", err)
	}
	if _, err := c.GetBulkOrder(ctx, &corev1.GetBulkOrderRequest{BulkId: "00000000-0000-7000-8000-00000000dead"}); code(err) != codes.NotFound {
		t.Fatalf("an unknown bulk: %v", err)
	}

	// Core's refusals, as codes the bot acts on.
	refused := func(what string, err error, want codes.Code, words ...string) {
		t.Helper()
		if code(err) != want {
			t.Fatalf("%s: %v, want %v", what, err, want)
		}
		for _, w := range words {
			if !strings.Contains(strings.ToLower(err.Error()), w) {
				t.Errorf("%s: %q does not say %q", what, err, w)
			}
		}
	}
	_, err = c.CreateBulkOrder(ctx, &corev1.CreateBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 5, IdempotencyKey: "bulk-grpc-key-2"})
	refused("a wallet that falls short", err, codes.FailedPrecondition, "insufficient")
	_, err = c.CreateBulkOrder(ctx, &corev1.CreateBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 2, IdempotencyKey: "bulk-grpc-key-3", ExpectedTotal: money(1999)})
	refused("another total than the one shown", err, codes.FailedPrecondition, "price changed")
	_, err = c.CreateBulkOrder(ctx, &corev1.CreateBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 1, IdempotencyKey: "bulk-grpc-key-4"})
	refused("one service", err, codes.InvalidArgument)
	_, err = c.QuoteBulkOrder(ctx, &corev1.QuoteBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: domain.DefaultBulkMax + 1})
	refused("past the store's most", err, codes.InvalidArgument)
	_, err = c.CreateBulkOrder(ctx, &corev1.CreateBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 2})
	refused("no key", err, codes.InvalidArgument)
	_, err = c.CreateBulkOrder(ctx, &corev1.CreateBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 4, IdempotencyKey: "bulk-grpc-key-1", LabelPrefix: "Family"})
	refused("a key used for another purchase", err, codes.AlreadyExists)
	_, err = c.QuoteBulkOrder(ctx, &corev1.QuoteBulkOrderRequest{UserId: buyer.GetId(), PlanId: "00000000-0000-7000-8000-00000000dead", Quantity: 2})
	refused("an unknown plan", err, codes.NotFound)
	if err := f.st.SetSetting(ctx, f.st.Conn(), domain.SettingBulkEnabled, []byte(`"false"`)); err != nil {
		t.Fatal(err)
	}
	_, err = c.QuoteBulkOrder(ctx, &corev1.QuoteBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 2})
	refused("a switched off store", err, codes.FailedPrecondition, "turned off")
	if err := f.st.DeleteSetting(ctx, f.st.Conn(), domain.SettingBulkEnabled); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetUserRole(ctx, f.st.Conn(), buyer.GetId(), "user", "banned"); err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateBulkOrder(ctx, &corev1.CreateBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 2, IdempotencyKey: "bulk-grpc-key-5"})
	refused("a banned buyer", err, codes.PermissionDenied, "banned")
	if err := f.st.SetUserRole(ctx, f.st.Conn(), buyer.GetId(), "user", "active"); err != nil {
		t.Fatal(err)
	}
	if n := wallet(t, c, buyer.GetId()); n != 2000 {
		t.Fatalf("a refusal moved money: %d", n)
	}
}

// The customer is never told why a bulk purchase was refused for the license's
// limit: the same neutral message as any new purchase, whatever the quantity.
func TestBulkPurchaseRefusedForTheLimitSaysNothingAboutLicensing(t *testing.T) {
	f := newFixture(t)
	c := f.client(t, botToken)
	ctx := context.Background()
	if _, err := f.st.DB().Exec(ctx, `UPDATE core.install SET trial_started_at = now() - interval '31 days'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = f.st.DB().Exec(ctx, `UPDATE core.install SET trial_started_at = now(), limit_hit_at = NULL`)
	})
	plan := &store.Plan{NameI18n: map[string]string{"en": "Month"}, Kind: "time", Price: 1000, Currency: "IRT", Enabled: true}
	if err := f.st.UpsertPlan(ctx, f.st.Conn(), plan); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(10 * 24 * time.Hour)
	for i := range 23 { // the free tier's 25, two places left
		u, err := f.st.UpsertUser(ctx, f.st.Conn(), int64(7400+i), "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		o, _, err := f.st.CreateOrder(ctx, f.st.Conn(), &store.Order{UserID: u.ID, PlanID: plan.ID, Type: "new", Status: "active",
			Amount: 1000, Currency: "IRT", IdempotencyKey: fmt.Sprintf("seed-bulk-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		sub, err := f.st.CreateSubscription(ctx, f.st.Conn(), &store.Subscription{UserID: u.ID, OrderID: o.ID, ClientEmail: fmt.Sprintf("ub%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.st.ActivateSubscription(ctx, f.st.Conn(), sub.ID, "00000000-0000-7000-8000-000000000001", "x", "", &expires, nil); err != nil {
			t.Fatal(err)
		}
	}
	buyer, err := c.UpsertUser(ctx, &corev1.UpsertUserRequest{TelegramId: 7500, Language: "fa"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := f.st.Credit(ctx, tx, buyer.GetId(), "IRT", 100_000, &store.LedgerEntry{Kind: "topup", IdempotencyKey: "bulk-limit-fund"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	paused := func(what string, err error) {
		t.Helper()
		st := status.Convert(err)
		if st.Code() != codes.FailedPrecondition || st.Message() != domain.PurchasesPaused {
			t.Fatalf("%s: %v", what, err)
		}
	}
	_, err = c.QuoteBulkOrder(ctx, &corev1.QuoteBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 3})
	paused("a quote for three with two places", err)
	_, err = c.CreateBulkOrder(ctx, &corev1.CreateBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 3, IdempotencyKey: "bulk-limit-3"})
	paused("three with two places", err)
	if n := wallet(t, c, buyer.GetId()); n != 100_000 {
		t.Fatalf("a refusal moved money: %d", n)
	}
	if b, err := c.CreateBulkOrder(ctx, &corev1.CreateBulkOrderRequest{UserId: buyer.GetId(), PlanId: plan.ID, Quantity: 2, IdempotencyKey: "bulk-limit-2"}); err != nil || len(b.GetServices()) != 2 {
		t.Fatalf("two with two places: %v", err)
	}
}

func wallet(t *testing.T, c corev1.CoreServiceClient, userID string) int64 {
	t.Helper()
	w, err := c.GetWallet(context.Background(), &corev1.GetWalletRequest{UserId: userID, Currency: "IRT"})
	if err != nil {
		t.Fatal(err)
	}
	return w.GetBalance()
}
