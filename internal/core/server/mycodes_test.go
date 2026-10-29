package server

import (
	"context"
	"testing"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// reward gives a customer a reward code of pct percent, made by a purchase of a plan with that name (empty: made by no purchase).
func (g *giftFixture) reward(t *testing.T, user, code string, pct int32, plan string, endsIn time.Duration) {
	t.Helper()
	ctx := context.Background()
	d := &store.Discount{Code: code, Percent: &pct, UserID: &user}
	if endsIn != 0 {
		at := time.Now().Add(endsIn)
		d.ExpiresAt = &at
	}
	if plan != "" {
		var planID, orderID string
		if err := g.st.DB().QueryRow(ctx, `INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency)
			VALUES (gen_random_uuid(), jsonb_build_object('en', $1::text, 'fa', 'پلن'), 'time', 30, 1000, 'IRT') RETURNING id::text`, plan).Scan(&planID); err != nil {
			t.Fatal(err)
		}
		if err := g.st.DB().QueryRow(ctx, `INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES (gen_random_uuid(), $1, $2, 'new', 'active', 1000, 'IRT', $3) RETURNING id::text`, user, planID, "src-"+code).Scan(&orderID); err != nil {
			t.Fatal(err)
		}
		d.SourceOrderID = &orderID
	}
	if made, err := g.st.InsertRewardCode(ctx, g.st.Conn(), d); err != nil || !made {
		t.Fatalf("reward code %s: %v %v", code, made, err)
	}
}

// ListMyCodes gives a customer their own codes only, newest first and paged, in the bot's words: a reward code whole with where it came from,
// a gift code as its last characters and what it credited.
func TestListMyCodesOverGRPC(t *testing.T) {
	g := newGiftFixture(t)
	ctx := context.Background()
	a, b := g.customer(t, 7801), g.customer(t, 7802)

	// a: two reward codes (one from a purchase, one ended), and two gift codes (one they share with b). b: a reward code. Staff: one for everyone,
	// and a gift code nobody applied.
	g.reward(t, a, "THX-GRPC0001", 20, "Monthly 50GB", 3*24*time.Hour)
	g.reward(t, a, "THX-GRPC0002", 10, "", -time.Hour)
	g.reward(t, b, "THX-GRPC0003", 30, "", 24*time.Hour)
	pct := int32(25)
	if _, err := g.dom.UpsertDiscount(ctx, g.admin, &store.Discount{Code: "SPRING25", Percent: &pct, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	shared, mine := g.make(t, 125_000, 2), g.make(t, 25_000, 1)
	_ = g.make(t, 99_000, 1)
	g.redeem(t, a, shared.Code, "")
	g.redeem(t, b, shared.Code, "")
	g.redeem(t, a, mine.Code, "")
	// Dates, so that the order is known: the oldest first is reward 1, reward 2, shared gift, own gift.
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	for i, q := range []string{
		`UPDATE core.discount_codes SET created_at = $1 WHERE code = 'THX-GRPC0001'`,
		`UPDATE core.discount_codes SET created_at = $1 WHERE code = 'THX-GRPC0002'`,
	} {
		if _, err := g.st.DB().Exec(ctx, q, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range []string{shared.ID, mine.ID} {
		if _, err := g.st.DB().Exec(ctx, `UPDATE core.gift_code_redemptions SET created_at = $1 WHERE gift_code_id = $2 AND user_id = $3`,
			base.Add(time.Duration(2+i)*time.Minute), id, a); err != nil {
			t.Fatal(err)
		}
	}

	page1, err := g.c.ListMyCodes(ctx, &corev1.ListMyCodesRequest{UserId: a, Pagination: &commonv1.Pagination{Page: 1, PageSize: 3}})
	if err != nil || page1.GetPageInfo().GetTotal() != 4 || page1.GetPageInfo().GetPage() != 1 || page1.GetPageInfo().GetPageSize() != 3 || len(page1.GetCodes()) != 3 {
		t.Fatalf("page 1: %v %+v", err, page1)
	}
	own, sharedEntry, ended := page1.GetCodes()[0], page1.GetCodes()[1], page1.GetCodes()[2]
	if own.GetKind() != "gift" || own.GetTail() != mine.Tail || own.GetCode() != "" || own.GetState() != "used" || own.GetAmount().GetAmount() != 25_000 ||
		own.GetAmount().GetCurrency() != "IRT" || own.GetUsedAt() == 0 || own.GetReceivedAt() != base.Add(3*time.Minute).Unix() || own.GetExpiresAt() != 0 {
		t.Fatalf("own gift entry: %+v", own)
	}
	if sharedEntry.GetKind() != "gift" || sharedEntry.GetTail() != shared.Tail || sharedEntry.GetAmount().GetAmount() != 125_000 {
		t.Fatalf("shared gift entry: %+v", sharedEntry)
	}
	if ended.GetKind() != "reward" || ended.GetCode() != "THX-GRPC0002" || ended.GetState() != "expired" || ended.GetPercent() != 10 || ended.GetAmount() != nil ||
		ended.GetExpiresAt() == 0 || ended.GetUsedAt() != 0 || len(ended.GetPlanNames()) != 0 || ended.GetOrderId() != "" {
		t.Fatalf("expired reward entry: %+v", ended)
	}
	page2, err := g.c.ListMyCodes(ctx, &corev1.ListMyCodesRequest{UserId: a, Pagination: &commonv1.Pagination{Page: 2, PageSize: 3}})
	if err != nil || page2.GetPageInfo().GetTotal() != 4 || len(page2.GetCodes()) != 1 {
		t.Fatalf("page 2: %v %+v", err, page2)
	}
	first := page2.GetCodes()[0]
	if first.GetCode() != "THX-GRPC0001" || first.GetState() != "unused" || first.GetPercent() != 20 || first.GetPlanNames()["en"] != "Monthly 50GB" ||
		first.GetPlanNames()["fa"] != "پلن" || first.GetOrderId() == "" || first.GetExpiresAt() == 0 || len(first.GetOnlyPlanNames()) != 0 {
		t.Fatalf("reward entry from a purchase: %+v", first)
	}

	// b sees theirs and no one else's; a customer with none gets an empty page; nothing of a is in b's answer.
	bAll, err := g.c.ListMyCodes(ctx, &corev1.ListMyCodesRequest{UserId: b})
	if err != nil || bAll.GetPageInfo().GetTotal() != 2 || len(bAll.GetCodes()) != 2 || bAll.GetPageInfo().GetPageSize() != 10 {
		t.Fatalf("b: %v %+v", err, bAll)
	}
	for _, c := range bAll.GetCodes() {
		if c.GetCode() != "" && c.GetCode() != "THX-GRPC0003" || c.GetKind() == "gift" && (c.GetTail() != shared.Tail || c.GetAmount().GetAmount() != 125_000) {
			t.Errorf("another's entry in b's list: %+v", c)
		}
	}
	none, err := g.c.ListMyCodes(ctx, &corev1.ListMyCodesRequest{UserId: g.customer(t, 7803)})
	if err != nil || none.GetPageInfo().GetTotal() != 0 || len(none.GetCodes()) != 0 {
		t.Fatalf("a customer with no codes: %v %+v", err, none)
	}

	// A page size that is too big or negative, and a page before the first, are the defaults.
	for _, p := range []*commonv1.Pagination{{Page: 0, PageSize: 0}, {Page: -3, PageSize: 500}, {Page: 1, PageSize: -1}} {
		r, err := g.c.ListMyCodes(ctx, &corev1.ListMyCodesRequest{UserId: a, Pagination: p})
		if err != nil || r.GetPageInfo().GetPage() != 1 || r.GetPageInfo().GetPageSize() != 10 || len(r.GetCodes()) != 4 {
			t.Errorf("pagination %v: %v %+v", p, err, r)
		}
	}
}

// A banned customer is refused as everywhere else, a customer core does not know is not found, and an id that is not one is a bad request.
func TestListMyCodesRefusals(t *testing.T) {
	g := newGiftFixture(t)
	ctx := context.Background()
	banned := g.customer(t, 7811)
	g.reward(t, banned, "THX-GRPC0011", 10, "", time.Hour)
	if err := g.st.SetUserRole(ctx, g.st.Conn(), banned, "user", "banned"); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		user string
		want codes.Code
	}{
		"banned":   {banned, codes.PermissionDenied},
		"unknown":  {"00000000-0000-7000-8000-00000000dead", codes.NotFound},
		"not a id": {"x", codes.InvalidArgument},
		"empty":    {"", codes.InvalidArgument},
	} {
		if _, err := g.c.ListMyCodes(ctx, &corev1.ListMyCodesRequest{UserId: tc.user}); status.Code(err) != tc.want {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}
