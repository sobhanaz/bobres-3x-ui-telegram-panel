package domain

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// codesEnv is a store with a plan that gives a 15% reward code, an owner who makes gift codes, and the means to give customers codes
// without going through a purchase.
type codesEnv struct {
	*rewardEnv
	typed map[string]string // gift code id -> the code as the customer types it
}

func newCodesEnv(t *testing.T) *codesEnv {
	return &codesEnv{rewardEnv: newRewardEnv(t), typed: map[string]string{}}
}

// giveReward gives u a reward code of pct percent that ends in days days (negative: it ended). source, when not empty, is the purchase that gave it.
func (e *codesEnv) giveReward(u *store.User, code string, pct int32, days int, source string) {
	e.t.Helper()
	d := &store.Discount{Code: code, Percent: &pct, UserID: &u.ID}
	if days != 0 {
		at := time.Now().Add(time.Duration(days) * 24 * time.Hour)
		d.ExpiresAt = &at
	}
	if source != "" {
		d.SourceOrderID = &source
	}
	if made, err := e.s.InsertRewardCode(context.Background(), e.s.Conn(), d); err != nil || !made {
		e.t.Fatalf("reward code %s: %v %v", code, made, err)
	}
}

// at puts a reward code's date, for the order of a list.
func (e *codesEnv) at(code string, when time.Time) {
	e.t.Helper()
	if _, err := e.s.DB().Exec(context.Background(), `UPDATE core.discount_codes SET created_at = $2 WHERE code = $1`, code, when); err != nil {
		e.t.Fatal(err)
	}
}

// gift makes a gift code that n customers may use (once each); the code as typed is kept for redeem.
func (e *codesEnv) gift(amount int64, n int) store.GiftCode {
	e.t.Helper()
	made, err := e.svc.CreateGiftCodes(context.Background(), e.owner, GiftCodeSpec{Amount: amount, Currency: "IRT", MaxRedemptions: n})
	if err != nil {
		e.t.Fatal(err)
	}
	e.typed[made[0].ID] = made[0].Code
	return made[0].GiftCode
}

// redeem has u apply the gift code, as the bot does.
func (e *codesEnv) redeem(u *store.User, g store.GiftCode) {
	e.t.Helper()
	if _, err := e.svc.RedeemGiftCode(context.Background(), RedeemGiftCodeParams{UserID: u.ID, Code: e.typed[g.ID], RequestKey: "redeem-" + g.ID + "-" + u.ID}); err != nil {
		e.t.Fatalf("redeem: %v", err)
	}
}

func ids(list []CustomerCode) []string {
	out := make([]string, len(list))
	for i := range list {
		out[i] = list[i].ID
	}
	return out
}

// The state of an entry follows its own facts: used for good once a paid order used it, else expired after its date, else off when staff
// switched it off; a gift entry is always used (it is listed because the customer applied it).
func TestCustomerCodeState(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	one, two := int32(1), int32(2)
	for name, tc := range map[string]struct {
		row  store.CodeRow
		want string
	}{
		"unused":                        {store.CodeRow{Kind: CodeReward, Enabled: true, MaxUses: &one, ExpiresAt: &future}, CodeUnused},
		"no expiry":                     {store.CodeRow{Kind: CodeReward, Enabled: true, MaxUses: &one}, CodeUnused},
		"used up":                       {store.CodeRow{Kind: CodeReward, Enabled: true, MaxUses: &one, Used: 1, ExpiresAt: &future}, CodeUsed},
		"used (a redemption)":           {store.CodeRow{Kind: CodeReward, Enabled: true, MaxUses: &two, UsedAt: &past, ExpiresAt: &future}, CodeUsed},
		"used, then its date passed":    {store.CodeRow{Kind: CodeReward, Enabled: true, MaxUses: &one, Used: 1, ExpiresAt: &past}, CodeUsed},
		"used, then switched off":       {store.CodeRow{Kind: CodeReward, MaxUses: &one, Used: 1}, CodeUsed},
		"some uses left":                {store.CodeRow{Kind: CodeReward, Enabled: true, MaxUses: &two, Used: 1}, CodeUnused},
		"unlimited uses":                {store.CodeRow{Kind: CodeReward, Enabled: true, Used: 40}, CodeUnused},
		"expired":                       {store.CodeRow{Kind: CodeReward, Enabled: true, MaxUses: &one, ExpiresAt: &past}, CodeExpired},
		"expired this very moment":      {store.CodeRow{Kind: CodeReward, Enabled: true, MaxUses: &one, ExpiresAt: &now}, CodeExpired},
		"switched off":                  {store.CodeRow{Kind: CodeReward, MaxUses: &one, ExpiresAt: &future}, CodeOff},
		"switched off, then it ended":   {store.CodeRow{Kind: CodeReward, MaxUses: &one, ExpiresAt: &past}, CodeExpired},
		"a gift code the customer used": {store.CodeRow{Kind: CodeGift, Enabled: true}, CodeUsed},
	} {
		if got := codeState(&tc.row, now); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// A customer's list holds their own reward codes and the gift codes they applied, and nothing else: not another customer's codes, not a
// code staff made for everyone, not a gift code nobody applied, and a gift code two customers applied is in each list with that customer's
// own redemption.
func TestCustomerCodesAreOnlyTheirOwn(t *testing.T) {
	e := newCodesEnv(t)
	ctx := context.Background()
	a, b, c := seedUser(t, e.s, 9701), seedUser(t, e.s, 9702), seedUser(t, e.s, 9703)
	e.giveReward(a, "THX-AAAAAAA2", 10, 5, "")
	e.giveReward(a, "THX-AAAAAAA3", 20, 5, "")
	e.giveReward(b, "THX-BBBBBBB2", 30, 5, "")
	// A code staff made for everyone is nobody's.
	pct := int32(25)
	if _, err := e.svc.UpsertDiscount(ctx, e.owner, &store.Discount{Code: "SPRING25", Percent: &pct, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	onlyA, both, nobody := e.gift(25_000, 1), e.gift(40_000, 2), e.gift(60_000, 1)
	e.redeem(a, onlyA)
	e.redeem(a, both)
	e.redeem(b, both)
	_ = nobody

	list, total, err := e.svc.CustomerCodes(ctx, a.ID, store.Page{Limit: 50})
	if err != nil || total != 4 || len(list) != 4 {
		t.Fatalf("a's codes: %d of %d %v", len(list), total, err)
	}
	var rewards, gifts []string
	for _, it := range list {
		switch it.Kind {
		case CodeReward:
			rewards = append(rewards, it.Code)
			if it.Tail != "" || it.State != CodeUnused || it.UsedAt != nil {
				t.Errorf("a reward entry: %+v", it)
			}
		case CodeGift:
			gifts = append(gifts, it.Tail)
			// A gift code is never kept: the entry has its last characters and what it credited, never the code.
			if it.Code != "" || it.State != CodeUsed || it.UsedAt == nil || it.Currency != "IRT" || it.Amount == 0 {
				t.Errorf("a gift entry: %+v", it)
			}
		}
	}
	slices.Sort(rewards)
	slices.Sort(gifts)
	if !slices.Equal(rewards, []string{"THX-AAAAAAA2", "THX-AAAAAAA3"}) || !slices.Equal(gifts, slices.Sorted(slices.Values([]string{onlyA.Tail, both.Tail}))) {
		t.Fatalf("a's rewards %v, gifts %v", rewards, gifts)
	}

	listB, totalB, err := e.svc.CustomerCodes(ctx, b.ID, store.Page{Limit: 50})
	if err != nil || totalB != 2 || len(listB) != 2 {
		t.Fatalf("b's codes: %d of %d %v", len(listB), totalB, err)
	}
	for _, it := range listB {
		switch it.Kind {
		case CodeReward:
			if it.Code != "THX-BBBBBBB2" || it.Percent != 30 {
				t.Errorf("b's reward entry: %+v", it)
			}
		case CodeGift:
			if it.Tail != both.Tail || it.Amount != 40_000 {
				t.Errorf("b's gift entry: %+v", it)
			}
		}
	}
	// Nothing leaks to a customer who has none, nor to a user id that does not exist.
	if list, total, err := e.svc.CustomerCodes(ctx, c.ID, store.Page{Limit: 50}); err != nil || total != 0 || len(list) != 0 {
		t.Fatalf("c has no codes: %d %d %v", len(list), total, err)
	}
	// What the store reads for a customer is by their id alone: the rows of the others, ownerless codes and unused gift codes stay out.
	rows, n, err := e.s.ListUserCodes(ctx, e.s.Conn(), b.ID, store.Page{Limit: 200})
	if err != nil || n != 2 {
		t.Fatalf("store rows for b: %d %d %v", len(rows), n, err)
	}
	for _, r := range rows {
		if r.ID == "SPRING25" || r.Code == "THX-AAAAAAA2" || r.Code == "THX-AAAAAAA3" || r.Tail == onlyA.Tail || r.Tail == nobody.Tail {
			t.Errorf("another's code in b's rows: %+v", r)
		}
	}
}

// Newest first across both kinds, paged with a total that does not change from page to page, and a page beyond the end is empty.
func TestCustomerCodesPaging(t *testing.T) {
	e := newCodesEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9711)
	base := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	// Eight entries, a minute apart, rewards and gifts interleaved: oldest R0, G1, R2, G3, R4, R5, G6, R7 newest.
	var want []string
	gifts := map[int]store.GiftCode{}
	for i := range 8 {
		when := base.Add(time.Duration(i) * time.Minute)
		if i == 1 || i == 3 || i == 6 {
			g := e.gift(1_000+int64(i), 1)
			e.redeem(u, g)
			gifts[i] = g
			if _, err := e.s.DB().Exec(ctx, `UPDATE core.gift_code_redemptions SET created_at = $2 WHERE gift_code_id = $1`, g.ID, when); err != nil {
				t.Fatal(err)
			}
			continue
		}
		code := fmt.Sprintf("THX-PAGE%04d", i)
		e.giveReward(u, code, 10, 5, "")
		e.at(code, when)
	}
	for i := 7; i >= 0; i-- { // newest first
		if g, ok := gifts[i]; ok {
			want = append(want, g.Tail)
		} else {
			want = append(want, fmt.Sprintf("THX-PAGE%04d", i))
		}
	}
	var got []string
	for page := 0; page < 3; page++ {
		list, total, err := e.svc.CustomerCodes(ctx, u.ID, store.Page{Limit: 3, Offset: page * 3})
		if err != nil || total != 8 {
			t.Fatalf("page %d: total %d %v", page+1, total, err)
		}
		if wantLen := min(3, 8-page*3); len(list) != wantLen {
			t.Fatalf("page %d holds %d, want %d", page+1, len(list), wantLen)
		}
		for _, it := range list {
			if it.Kind == CodeGift {
				got = append(got, it.Tail)
			} else {
				got = append(got, it.Code)
			}
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("order across pages:\n got %v\nwant %v", got, want)
	}
	if list, total, err := e.svc.CustomerCodes(ctx, u.ID, store.Page{Limit: 3, Offset: 30}); err != nil || total != 8 || len(list) != 0 {
		t.Fatalf("a page past the end: %d %d %v", len(list), total, err)
	}
	// Two entries made at the same moment keep a fixed order, so a page boundary never repeats or drops one.
	same := seedUser(t, e.s, 9712)
	for _, c := range []string{"THX-TIE00001", "THX-TIE00002", "THX-TIE00003"} {
		e.giveReward(same, c, 10, 5, "")
		e.at(c, base)
	}
	seen := map[string]bool{}
	for page := 0; page < 3; page++ {
		list, _, err := e.svc.CustomerCodes(ctx, same.ID, store.Page{Limit: 1, Offset: page})
		if err != nil || len(list) != 1 || seen[list[0].ID] {
			t.Fatalf("tie page %d: %v %v", page+1, ids(list), err)
		}
		seen[list[0].ID] = true
	}
}

// A reward code earned by a purchase says which plan and which purchase gave it; used on a later purchase it is used for good, and the list
// says when; expired and switched-off codes say so; a code from before reward codes were for any plan names the one plan it works on.
func TestCustomerCodesStateAndSource(t *testing.T) {
	e := newCodesEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9721)
	fund(t, e.s, u.ID, 100_000)

	// A purchase of the plan that gives a reward earns a code.
	earned, err := e.buyPlan(u, e.reward, "cs-1", "")
	if err != nil {
		t.Fatal(err)
	}
	list, total, err := e.svc.CustomerCodes(ctx, u.ID, store.Page{})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("after one purchase: %d %d %v", len(list), total, err)
	}
	first := list[0]
	if first.Kind != CodeReward || first.State != CodeUnused || first.Percent != 15 || first.Amount != 0 || first.OrderID != earned.ID ||
		first.PlanNames["en"] != "Unlimited" || first.PlanNames["fa"] != "نامحدود" || first.UsedAt != nil || first.OnlyPlanNames != nil ||
		first.ExpiresAt == nil || first.ExpiresAt.Before(time.Now().Add(9*24*time.Hour)) || first.ExpiresAt.After(time.Now().Add(11*24*time.Hour)) ||
		time.Since(first.ReceivedAt) > time.Minute {
		t.Fatalf("the code earned: %+v", first)
	}

	// Used on the next purchase (which earns another code): used for good, with the day it was used.
	if _, err := e.buyPlan(u, e.plan, "cs-2", first.Code); err != nil {
		t.Fatal(err)
	}
	list, total, err = e.svc.CustomerCodes(ctx, u.ID, store.Page{})
	if err != nil || total != 1 {
		// e.plan gives no reward: the second purchase added no code.
		t.Fatalf("after using it: %d %v", total, err)
	}
	if list[0].State != CodeUsed || list[0].UsedAt == nil || time.Since(*list[0].UsedAt) > time.Minute || list[0].Code != first.Code {
		t.Fatalf("the code used: %+v", list[0])
	}

	// Expired, switched off, and one with no purchase behind it.
	e.giveReward(u, "THX-OLDCODE2", 5, -2, "")
	e.giveReward(u, "THX-OFFCODE2", 5, 5, "")
	e.giveReward(u, "THX-NEWCODE2", 5, 0, "") // never expires
	if _, err := e.s.DB().Exec(ctx, `UPDATE core.discount_codes SET enabled = false WHERE code = 'THX-OFFCODE2'`); err != nil {
		t.Fatal(err)
	}
	// A code made before reward codes were for any plan: bound to the plan that earned it.
	twelve := int32(12)
	legacy := &store.Discount{Code: "THX-LEGACY22", Percent: &twelve, UserID: &u.ID, PlanID: &e.plan.ID}
	if made, err := e.s.InsertRewardCode(ctx, e.s.Conn(), legacy); err != nil || !made {
		t.Fatalf("legacy: %v %v", made, err)
	}
	list, total, err = e.svc.CustomerCodes(ctx, u.ID, store.Page{Limit: 50})
	if err != nil || total != 5 {
		t.Fatalf("all of them: %d %v", total, err)
	}
	state := map[string]CustomerCode{}
	for _, it := range list {
		state[it.Code] = it
	}
	for code, want := range map[string]string{first.Code: CodeUsed, "THX-OLDCODE2": CodeExpired, "THX-OFFCODE2": CodeOff, "THX-NEWCODE2": CodeUnused, "THX-LEGACY22": CodeUnused} {
		if state[code].State != want {
			t.Errorf("%s is %q, want %q", code, state[code].State, want)
		}
	}
	if n := state["THX-NEWCODE2"]; n.ExpiresAt != nil || n.OrderID != "" || n.PlanNames != nil {
		t.Errorf("a code with no end and no purchase: %+v", n)
	}
	if l := state["THX-LEGACY22"]; l.OnlyPlanNames["en"] != "Monthly" || l.Percent != 12 {
		t.Errorf("a code bound to a plan: %+v", l)
	}
}

// A reward code that is an amount carries its amount and currency, and a gift entry what it credited.
func TestCustomerCodesCarryTheirValue(t *testing.T) {
	e := newCodesEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9731)
	amount, cur := int64(4_000), "IRT"
	d := &store.Discount{Code: "THX-AMOUNT22", Amount: &amount, Currency: &cur, UserID: &u.ID}
	if made, err := e.s.InsertRewardCode(ctx, e.s.Conn(), d); err != nil || !made {
		t.Fatal(made, err)
	}
	e.redeem(u, e.gift(25_000, 1))
	list, _, err := e.svc.CustomerCodes(ctx, u.ID, store.Page{})
	if err != nil || len(list) != 2 {
		t.Fatalf("%d %v", len(list), err)
	}
	for _, it := range list {
		switch it.Kind {
		case CodeReward:
			if it.Amount != 4_000 || it.Currency != "IRT" || it.Percent != 0 {
				t.Errorf("an amount code: %+v", it)
			}
		case CodeGift:
			if it.Amount != 25_000 || it.Currency != "IRT" || it.Percent != 0 {
				t.Errorf("a gift entry: %+v", it)
			}
		}
	}
}

// A banned customer is refused as everywhere else, an unknown or malformed id is told apart, and staff may read a banned customer's list.
func TestCustomerCodesRefusals(t *testing.T) {
	e := newCodesEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9741)
	e.giveReward(u, "THX-BANNED22", 10, 5, "")
	if err := e.s.SetUserRole(ctx, e.s.Conn(), u.ID, "user", "banned"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.CustomerCodes(ctx, u.ID, store.Page{}); !errors.Is(err, ErrUserBanned) {
		t.Errorf("a banned customer: %v", err)
	}
	if list, total, err := e.svc.UserCodes(ctx, u.ID, store.Page{}); err != nil || total != 1 || len(list) != 1 {
		t.Errorf("staff read a banned customer's codes: %d %d %v", len(list), total, err)
	}
	const missing = "00000000-0000-7000-8000-00000000dead"
	for _, call := range []func(context.Context, string, store.Page) ([]CustomerCode, int64, error){e.svc.CustomerCodes, e.svc.UserCodes} {
		if _, _, err := call(ctx, missing, store.Page{}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("an unknown customer: %v", err)
		}
		for _, bad := range []string{"", "x", "not-a-uuid"} {
			if _, _, err := call(ctx, bad, store.Page{}); !errors.Is(err, ErrInvalid) {
				t.Errorf("id %q: %v", bad, err)
			}
		}
	}
}
