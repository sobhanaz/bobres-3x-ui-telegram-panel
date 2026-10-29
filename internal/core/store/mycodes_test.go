package store

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

// ListUserCodes reads one customer's reward codes and gift redemptions and nothing else: not another customer's, not a code staff made for
// everyone, not a gift code nobody applied. They come newest first across both kinds, paged, with the total of the customer's own.
func TestListUserCodes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, err := s.UpsertUser(ctx, s.Conn(), 4301, "a", "en", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.UpsertUser(ctx, s.Conn(), 4302, "b", "en", "")
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	at := func(code string, minutes int) {
		t.Helper()
		if _, err := s.DB().Exec(ctx, `UPDATE core.discount_codes SET created_at = $2 WHERE code = $1`, code, base.Add(time.Duration(minutes)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	pct, amount, cur := int32(20), int64(5000), "IRT"
	reward := func(code, user string, minutes int) {
		t.Helper()
		d := &Discount{Code: code, Percent: &pct, UserID: &user}
		if made, err := s.InsertRewardCode(ctx, s.Conn(), d); err != nil || !made {
			t.Fatalf("reward %s: %v %v", code, made, err)
		}
		at(code, minutes)
	}
	reward("THX-A0000001", a.ID, 0)
	reward("THX-A0000002", a.ID, 20)
	reward("THX-B0000001", b.ID, 10)
	// An amount code, and a code staff made for everyone.
	if made, err := s.InsertRewardCode(ctx, s.Conn(), &Discount{Code: "THX-A0000003", Amount: &amount, Currency: &cur, UserID: &a.ID}); err != nil || !made {
		t.Fatal(made, err)
	}
	at("THX-A0000003", 30)
	if _, err := s.UpsertDiscount(ctx, s.Conn(), &Discount{Code: "SPRING20", Percent: &pct, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Gift codes: one a applied (at minute 15), one a and b both applied, one nobody applied.
	own, shared, unapplied := newGift(t, s, 11, 1, 1), newGift(t, s, 12, 2, 1), newGift(t, s, 13, 1, 1)
	for g, tail := range map[*GiftCode]string{own: "OWN1", shared: "SHR1", unapplied: "NON1"} { // the helper gives every code the same tail
		g.Tail = tail
		if _, err := s.DB().Exec(ctx, `UPDATE core.gift_codes SET code_tail = $2 WHERE id = $1`, g.ID, tail); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []struct {
		g       *GiftCode
		user    string
		minutes int
	}{{own, a.ID, 15}, {shared, a.ID, 25}, {shared, b.ID, 5}} {
		if err := redeemRow(t, s, r.g, r.user, 1, "k"+r.g.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(ctx, `UPDATE core.gift_code_redemptions SET created_at = $3 WHERE gift_code_id = $1 AND user_id = $2`,
			r.g.ID, r.user, base.Add(time.Duration(r.minutes)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	// a: reward 0, gift 15, reward 20, gift 25, reward 30 (oldest to newest): five entries, newest first.
	rows, total, err := s.ListUserCodes(ctx, s.Conn(), a.ID, Page{Limit: 50})
	if err != nil || total != 5 || len(rows) != 5 {
		t.Fatalf("a's codes: %d of %d %v", len(rows), total, err)
	}
	var order []string
	for _, r := range rows {
		if r.Kind == CodeKindGift {
			order = append(order, "gift:"+r.Tail)
		} else {
			order = append(order, r.Code)
		}
	}
	want := []string{"THX-A0000003", "gift:" + shared.Tail, "THX-A0000002", "gift:" + own.Tail, "THX-A0000001"}
	if !slices.Equal(order, want) {
		t.Fatalf("order:\n got %v\nwant %v", order, want)
	}
	// What each kind carries: a reward code its terms, a gift entry its credit and no code, never another's.
	if r := rows[0]; r.Amount == nil || *r.Amount != 5000 || r.Currency == nil || *r.Currency != "IRT" || r.Percent != nil || r.UsedAt != nil || r.ReceivedAt.IsZero() || r.Tail != "" {
		t.Errorf("an amount code: %+v", r)
	}
	if r := rows[1]; r.Kind != CodeKindGift || r.Code != "" || r.Amount == nil || *r.Amount != 1000 || r.UsedAt == nil || !r.UsedAt.Equal(r.ReceivedAt) || r.ExpiresAt != nil || r.OrderID != nil {
		t.Errorf("a gift entry: %+v", r)
	}
	if r := rows[2]; r.Percent == nil || *r.Percent != 20 || r.Amount != nil || !r.Enabled || r.MaxUses == nil || *r.MaxUses != 1 || r.Used != 0 {
		t.Errorf("a percent code: %+v", r)
	}
	for _, r := range rows {
		if r.ID == "THX-B0000001" || r.ID == "SPRING20" || r.Tail == unapplied.Tail {
			t.Errorf("not a's: %+v", r)
		}
	}
	// b's: the reward code and the shared gift code, theirs by their own redemption.
	if bRows, n, err := s.ListUserCodes(ctx, s.Conn(), b.ID, Page{Limit: 50}); err != nil || n != 2 || len(bRows) != 2 || bRows[0].Code != "THX-B0000001" ||
		bRows[1].Kind != CodeKindGift || bRows[1].Tail != shared.Tail {
		t.Fatalf("b's codes: %+v %d %v", bRows, n, err)
	}
	if rows, n, err := s.ListUserCodes(ctx, s.Conn(), "00000000-0000-7000-8000-00000000dead", Page{}); err != nil || n != 0 || len(rows) != 0 {
		t.Fatalf("an unknown customer: %d %d %v", len(rows), n, err)
	}

	// Pages: the total stays whole, the slices follow each other, and past the end is empty.
	var got []string
	for off := 0; off < 6; off += 2 {
		page, n, err := s.ListUserCodes(ctx, s.Conn(), a.ID, Page{Limit: 2, Offset: off})
		if err != nil || n != 5 {
			t.Fatalf("offset %d: %d %v", off, n, err)
		}
		for _, r := range page {
			got = append(got, fmt.Sprintf("%s/%s", r.Kind, r.ID))
		}
	}
	if len(got) != 5 || got[0] != "reward/THX-A0000003" {
		t.Fatalf("the pages together: %v", got)
	}
}

// What a reward code says about itself: its use (a redemption of the customer's own), the purchase and plan that gave it, and the plan a
// code made before reward codes were for any plan is bound to.
func TestListUserCodesUseAndSource(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, err := s.UpsertUser(ctx, s.Conn(), 4311, "u", "en", "")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.UpsertUser(ctx, s.Conn(), 4312, "o", "en", "")
	var planID, orderID, usedOrder string
	if err := s.DB().QueryRow(ctx, `INSERT INTO core.plans (id, name_i18n, kind, duration_days, price, currency)
		VALUES (gen_random_uuid(), '{"en":"Monthly","fa":"ماهانه"}', 'time', 30, 1000, 'IRT') RETURNING id::text`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	for i, into := range []*string{&orderID, &usedOrder} {
		if err := s.DB().QueryRow(ctx, `INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
			VALUES (gen_random_uuid(), $1, $2, 'new', 'active', 1000, 'IRT', $3) RETURNING id::text`, u.ID, planID, fmt.Sprintf("src-%d", i)).Scan(into); err != nil {
			t.Fatal(err)
		}
	}
	pct := int32(15)
	expires := time.Now().Add(24 * time.Hour)
	for _, d := range []*Discount{
		{Code: "THX-USED0001", Percent: &pct, UserID: &u.ID, SourceOrderID: &orderID, ExpiresAt: &expires},
		{Code: "THX-BOUND001", Percent: &pct, UserID: &u.ID, PlanID: &planID},
	} {
		if made, err := s.InsertRewardCode(ctx, s.Conn(), d); err != nil || !made {
			t.Fatal(made, err)
		}
	}
	// The customer used one on a paid order (the use the order flow records).
	if ok, err := s.RecordRedemption(ctx, s.Conn(), "THX-USED0001", u.ID, usedOrder); err != nil || !ok {
		t.Fatal(ok, err)
	}
	rows, total, err := s.ListUserCodes(ctx, s.Conn(), u.ID, Page{})
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("%d %d %v", len(rows), total, err)
	}
	byCode := map[string]CodeRow{}
	for _, r := range rows {
		byCode[r.Code] = r
	}
	used := byCode["THX-USED0001"]
	if used.UsedAt == nil || used.Used != 1 || used.OrderID == nil || *used.OrderID != orderID || used.PlanNames["en"] != "Monthly" || used.PlanNames["fa"] != "ماهانه" ||
		used.ExpiresAt == nil || used.OnlyPlanNames != nil {
		t.Errorf("a used code with its purchase: %+v", used)
	}
	bound := byCode["THX-BOUND001"]
	if bound.UsedAt != nil || bound.OrderID != nil || bound.PlanNames != nil || bound.OnlyPlanNames["en"] != "Monthly" || bound.ExpiresAt != nil {
		t.Errorf("a code bound to a plan: %+v", bound)
	}
	if rows, n, err := s.ListUserCodes(ctx, s.Conn(), other.ID, Page{}); err != nil || n != 0 || len(rows) != 0 {
		t.Fatalf("another customer: %d %d %v", len(rows), n, err)
	}
}
