package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// rewardEnv is a store with an owner and a plan that gives a reward code of 15%.
type rewardEnv struct {
	*namingEnv
	reward *store.Plan
}

func newRewardEnv(t *testing.T) *rewardEnv {
	t.Helper()
	e := &rewardEnv{namingEnv: newNamingEnv(t)}
	days, pct := int32(30), int32(15)
	p := &store.Plan{NameI18n: map[string]string{"en": "Unlimited", "fa": "نامحدود"}, Kind: "time", DurationDays: &days, Price: 5000, Currency: "IRT", Enabled: true, RewardPercent: &pct}
	got, err := e.svc.UpsertPlan(context.Background(), e.owner, p)
	if err != nil {
		t.Fatal(err)
	}
	e.reward = got
	return e
}

// buyPlan has u pay for one new service of plan (with a discount code when given) and the worker deliver it.
func (e *rewardEnv) buyPlan(u *store.User, plan *store.Plan, key, code string) (*store.Order, error) {
	e.t.Helper()
	ctx := context.Background()
	o, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: key, DiscountCode: code})
	if err != nil {
		return nil, err
	}
	if _, err := e.svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		return o, err
	}
	if worked, err := e.svc.NewProvisionWorker(&fakeProvisioner{}, nil).ProvisionNext(ctx); err != nil || !worked {
		e.t.Fatalf("provisioning: %v %v", worked, err)
	}
	return o, nil
}

func TestPlanRewardPercentRules(t *testing.T) {
	e := newRewardEnv(t)
	ctx := context.Background()
	if e.reward.RewardPercent == nil || *e.reward.RewardPercent != 15 {
		t.Fatalf("saved reward %v", e.reward.RewardPercent)
	}
	// nil keeps it (the bot's plan editor never sends it), 0 takes it away, 1 to 90 sets it, more is refused.
	keep := *e.reward
	keep.RewardPercent, keep.KeepReward = nil, true
	if got, err := e.svc.UpsertPlan(ctx, e.owner, &keep); err != nil || got.RewardPercent == nil || *got.RewardPercent != 15 {
		t.Fatalf("an edit with none kept: %v %v", got, err)
	}
	for _, bad := range []int32{-1, 91, 100} {
		b := bad
		over := *e.reward
		over.RewardPercent = &b
		var fe *FieldError
		if _, err := e.svc.UpsertPlan(ctx, e.owner, &over); !errors.As(err, &fe) || fe.Field != "reward_percent" {
			t.Errorf("percent %d: %v", bad, err)
		}
	}
	zero := int32(0)
	off := *e.reward
	off.RewardPercent = &zero
	if got, err := e.svc.UpsertPlan(ctx, e.owner, &off); err != nil || got.RewardPercent != nil {
		t.Fatalf("0 did not take it away: %v %v", got, err)
	}
	// A free trial and a traffic package never give one.
	pct := int32(10)
	trial := &store.Plan{NameI18n: map[string]string{"en": "Trial"}, Kind: "time", DurationDays: e.reward.DurationDays, Price: 0, Currency: "IRT", Enabled: true, IsTrial: true, RewardPercent: &pct}
	if got, err := e.svc.UpsertPlan(ctx, e.owner, trial); err != nil || got.RewardPercent != nil {
		t.Fatalf("a trial with a reward: %v %v", got, err)
	}
}

// A paid purchase of a plan that gives a reward earns its buyer a code for any plan they buy next: bound to them, one use, 10 days (the plan's
// days), told by an event; it is not given for other plans, resellers, free orders, traffic packages or a bulk purchase.
func TestRewardCodeIsIssuedAndBound(t *testing.T) {
	e := newRewardEnv(t)
	ctx := context.Background()
	buyer, other := seedUser(t, e.s, 9901), seedUser(t, e.s, 9902)
	for _, u := range []*store.User{buyer, other} {
		fund(t, e.s, u.ID, 100_000)
	}
	o, err := e.buyPlan(buyer, e.reward, "rw-1", "")
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.s.RewardCodeOfOrder(ctx, e.s.Conn(), o.ID)
	if err != nil {
		t.Fatalf("no reward code: %v", err)
	}
	if d.Percent == nil || *d.Percent != 15 || d.MaxUses == nil || *d.MaxUses != 1 || !d.Enabled ||
		d.UserID == nil || *d.UserID != buyer.ID || d.PlanID != nil {
		t.Fatalf("the code %+v", d)
	}
	if d.ExpiresAt == nil || d.ExpiresAt.Before(time.Now().Add(9*24*time.Hour)) || d.ExpiresAt.After(time.Now().Add(11*24*time.Hour)) {
		t.Fatalf("expires %v", d.ExpiresAt)
	}
	evs := outboxEvents(t, e.s, events.RewardCodeIssued)
	var ev events.RewardCodeIssuedEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &ev) != nil || ev.Code != d.Code || ev.TelegramID != buyer.TelegramID || ev.Percent != 15 || ev.Days != 10 || ev.PlanNames["fa"] != "نامحدود" {
		t.Fatalf("event %s", evs)
	}
	// Issuing again for the same purchase (a retried delivery) makes nothing.
	if err := e.s.WithTx(ctx, func(tx pgx.Tx) error { return e.svc.issueReward(ctx, tx, o, e.reward, buyer) }); err != nil {
		t.Fatal(err)
	}
	if got := outboxEvents(t, e.s, events.RewardCodeIssued); len(got) != 1 {
		t.Fatalf("a second code event: %d", len(got))
	}

	// The code takes 15% off the buyer's next purchase, of this plan or of any other.
	q, err := e.svc.QuoteOrder(ctx, QuoteParams{UserID: buyer.ID, PlanID: e.reward.ID, Type: "new", DiscountCode: d.Code})
	if err != nil || q.Discount != 750 || q.Total != 4250 {
		t.Fatalf("quote %+v %v", q, err)
	}
	// Another customer is told the code does not exist; another plan of the buyer's takes it.
	var de *DiscountError
	if _, err := e.svc.QuoteOrder(ctx, QuoteParams{UserID: other.ID, PlanID: e.reward.ID, Type: "new", DiscountCode: d.Code}); !errors.As(err, &de) || de.Reason != "unknown" {
		t.Errorf("another customer: %v", err)
	}
	if q2, err := e.svc.QuoteOrder(ctx, QuoteParams{UserID: buyer.ID, PlanID: e.plan.ID, Type: "new", DiscountCode: d.Code}); err != nil || q2.Discount != q2.ListPrice*15/100 {
		t.Errorf("another plan: %+v %v", q2, err)
	}
	// Used once (it is a new purchase that also gives its buyer a new code), then it is used up.
	o2, err := e.buyPlan(buyer, e.reward, "rw-2", d.Code)
	if err != nil {
		t.Fatalf("buying with the code: %v", err)
	}
	if o2.Amount != 4250 {
		t.Fatalf("the order cost %d", o2.Amount)
	}
	if _, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: buyer.ID, PlanID: e.reward.ID, IdempotencyKey: "rw-3", DiscountCode: d.Code}); err == nil {
		t.Error("the code worked twice")
	}
	if d2, err := e.s.RewardCodeOfOrder(ctx, e.s.Conn(), o2.ID); err != nil || d2.Code == d.Code {
		t.Fatalf("the second purchase's own code: %v %v", d2, err)
	}
	// An expired code is refused.
	d2, _ := e.s.RewardCodeOfOrder(ctx, e.s.Conn(), o2.ID)
	if _, err := e.s.DB().Exec(ctx, `UPDATE core.discount_codes SET expires_at = now() - interval '1 minute' WHERE code = $1`, d2.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.QuoteOrder(ctx, QuoteParams{UserID: buyer.ID, PlanID: e.reward.ID, Type: "new", DiscountCode: d2.Code}); !errors.As(err, &de) || de.Reason != "expired" {
		t.Errorf("an expired code: %v", err)
	}

	// No code for a plan with no reward.
	o3, err := e.buyPlan(other, e.plan, "rw-4", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RewardCodeOfOrder(ctx, e.s.Conn(), o3.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a plan without a reward gave a code: %v", err)
	}
}

func TestWhoEarnsAReward(t *testing.T) {
	e := newRewardEnv(t)
	buyer := seedUser(t, e.s, 9911)
	reseller := e.reseller(9912)
	bulk := "00000000-0000-7000-8000-000000000b01"
	topup := *e.reward
	topup.IsTopup = true
	for name, tc := range map[string]struct {
		o     store.Order
		plan  *store.Plan
		buyer *store.User
		want  bool
	}{
		"a new purchase":        {store.Order{Type: "new", Amount: 5000}, e.reward, buyer, true},
		"a renewal":             {store.Order{Type: "renew", Amount: 5000}, e.reward, buyer, true},
		"a traffic package":     {store.Order{Type: "traffic_topup", Amount: 5000}, e.reward, buyer, false},
		"a free order":          {store.Order{Type: "new", Amount: 0}, e.reward, buyer, false},
		"a reseller":            {store.Order{Type: "new", Amount: 5000}, e.reward, reseller, false},
		"a bulk purchase":       {store.Order{Type: "new", Amount: 5000, BulkID: &bulk}, e.reward, buyer, false},
		"a package plan":        {store.Order{Type: "new", Amount: 5000}, &topup, buyer, false},
		"a plan with no reward": {store.Order{Type: "new", Amount: 5000}, e.plan, buyer, false},
	} {
		if got := givesReward(&tc.o, tc.plan, tc.buyer); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// A plan can give a fixed amount instead of a percent, for the days its owner sets and with a text for the buyer: the code is for any plan, takes
// the amount off (never more than the price), and works only in the plan's currency.
func TestAmountRewardCode(t *testing.T) {
	e := newRewardEnv(t)
	ctx := context.Background()
	days, amt := int32(30), int64(4_000)
	p := &store.Plan{NameI18n: map[string]string{"en": "Germany unlimited"}, Kind: "time", DurationDays: &days, Price: 9000, Currency: "IRT", Enabled: true,
		RewardAmount: &amt, RewardDays: 7, RewardTextI18n: map[string]string{"fa": "  برای هر پلن حجمی  ", "en": "For any volume plan", "de": "ignored"}}
	amountPlan, err := e.svc.UpsertPlan(ctx, e.owner, p)
	if err != nil {
		t.Fatal(err)
	}
	if amountPlan.RewardAmount == nil || *amountPlan.RewardAmount != 4000 || amountPlan.RewardPercent != nil || amountPlan.RewardDays != 7 ||
		amountPlan.RewardTextI18n["fa"] != "برای هر پلن حجمی" || len(amountPlan.RewardTextI18n) != 2 {
		t.Fatalf("saved %+v", amountPlan)
	}
	buyer := seedUser(t, e.s, 9921)
	fund(t, e.s, buyer.ID, 100_000)
	o, err := e.buyPlan(buyer, amountPlan, "am-1", "")
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.s.RewardCodeOfOrder(ctx, e.s.Conn(), o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Amount == nil || *d.Amount != 4000 || d.Currency == nil || *d.Currency != "IRT" || d.Percent != nil || d.PlanID != nil ||
		d.ExpiresAt == nil || d.ExpiresAt.Before(time.Now().Add(6*24*time.Hour)) || d.ExpiresAt.After(time.Now().Add(8*24*time.Hour)) {
		t.Fatalf("the code %+v", d)
	}
	evs := outboxEvents(t, e.s, events.RewardCodeIssued)
	var ev events.RewardCodeIssuedEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &ev) != nil || ev.Amount != 4000 || ev.Currency != "IRT" || ev.Percent != 0 || ev.Days != 7 || ev.Text["en"] != "For any volume plan" {
		t.Fatalf("event %s", evs)
	}
	// It takes 4000 off another plan (the group of the plan that gave it does not matter) and off the plan itself.
	for _, plan := range []*store.Plan{e.plan, amountPlan} {
		q, err := e.svc.QuoteOrder(ctx, QuoteParams{UserID: buyer.ID, PlanID: plan.ID, Type: "new", DiscountCode: d.Code})
		if err != nil || q.Discount != min(4000, plan.Price) || q.Total != plan.Price-min(4000, plan.Price) {
			t.Fatalf("quote on %d: %+v %v", plan.Price, q, err)
		}
	}
	// A code worth more than the plan takes the whole price off, and the rest is lost.
	cheapPlan, err := e.svc.UpsertPlan(ctx, e.owner, &store.Plan{NameI18n: map[string]string{"en": "Cheap"}, Kind: "time", DurationDays: &days, Price: 1500, Currency: "IRT", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if q, err := e.svc.QuoteOrder(ctx, QuoteParams{UserID: buyer.ID, PlanID: cheapPlan.ID, Type: "new", DiscountCode: d.Code}); err != nil || q.Discount != 1500 || q.Total != 0 {
		t.Fatalf("a code worth more than the plan: %+v %v", q, err)
	}
	// Only in its own currency.
	usd, err := e.svc.UpsertPlan(ctx, e.owner, &store.Plan{NameI18n: map[string]string{"en": "Dollars"}, Kind: "time", DurationDays: &days, Price: 500_000, Currency: "USDT", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var de *DiscountError
	if _, err := e.svc.QuoteOrder(ctx, QuoteParams{UserID: buyer.ID, PlanID: usd.ID, Type: "new", DiscountCode: d.Code}); !errors.As(err, &de) || de.Reason != "currency" {
		t.Errorf("another currency: %v", err)
	}
}

func TestRewardRulesOfThePlanForm(t *testing.T) {
	e := newRewardEnv(t)
	ctx := context.Background()
	days := int32(30)
	pct, amt := int32(10), int64(500)
	base := func() *store.Plan {
		return &store.Plan{NameI18n: map[string]string{"en": "P"}, Kind: "time", DurationDays: &days, Price: 5000, Currency: "IRT", Enabled: true}
	}
	for name, mod := range map[string]func(*store.Plan){
		"both a percent and an amount": func(p *store.Plan) { p.RewardPercent, p.RewardAmount = &pct, &amt },
		"more than 365 days":           func(p *store.Plan) { p.RewardAmount, p.RewardDays = &amt, 366 },
		"negative days":                func(p *store.Plan) { p.RewardAmount, p.RewardDays = &amt, -1 },
		"a text over 500": func(p *store.Plan) {
			p.RewardAmount, p.RewardTextI18n = &amt, map[string]string{"en": strings.Repeat("x", 501)}
		},
	} {
		p := base()
		mod(p)
		var fe *FieldError
		if _, err := e.svc.UpsertPlan(ctx, e.owner, p); !errors.As(err, &fe) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// No days means the default; a plan with no reward keeps no text; a trial gives none.
	p := base()
	p.RewardAmount = &amt
	if got, err := e.svc.UpsertPlan(ctx, e.owner, p); err != nil || got.RewardDays != DefaultRewardDays {
		t.Fatalf("default days: %+v %v", got, err)
	}
	q := base()
	q.RewardTextI18n = map[string]string{"en": "no reward, no text"}
	if got, err := e.svc.UpsertPlan(ctx, e.owner, q); err != nil || len(got.RewardTextI18n) != 0 || got.RewardDays != DefaultRewardDays {
		t.Fatalf("a plan with no reward: %+v %v", got, err)
	}
	// Switching a plan on or off (nothing sent) keeps its reward, days and text.
	p2 := base()
	p2.RewardAmount, p2.RewardDays, p2.RewardTextI18n = &amt, 20, map[string]string{"en": "hello"}
	saved, err := e.svc.UpsertPlan(ctx, e.owner, p2)
	if err != nil {
		t.Fatal(err)
	}
	off := *saved
	off.Enabled, off.RewardAmount, off.RewardDays, off.RewardTextI18n, off.KeepReward = false, nil, 0, nil, true
	if got, err := e.svc.UpsertPlan(ctx, e.owner, &off); err != nil || got.RewardAmount == nil || *got.RewardAmount != 500 || got.RewardDays != 20 || got.RewardTextI18n["en"] != "hello" {
		t.Fatalf("an edit that keeps the reward: %+v %v", got, err)
	}
}

// Deleting a plan never erases it: it is taken off sale and out of every list, whether or not orders point to it.
func TestDeletePlan(t *testing.T) {
	e := newRewardEnv(t)
	ctx := context.Background()
	days := int32(30)
	fresh, err := e.svc.UpsertPlan(ctx, e.owner, &store.Plan{NameI18n: map[string]string{"en": "Never sold"}, Kind: "time", DurationDays: &days, Price: 1000, Currency: "IRT", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	buyer := seedUser(t, e.s, 9931)
	fund(t, e.s, buyer.ID, 100_000)
	o, err := e.buyPlan(buyer, e.reward, "del-1", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, plan := range map[string]*store.Plan{"a plan nobody bought": fresh, "a plan with an order": e.reward} {
		if err := e.svc.DeletePlan(ctx, e.owner, plan.ID); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Off sale and out of the lists, but the row is still there.
		for _, all := range []bool{true, false} {
			plans, err := e.s.ListPlans(ctx, e.s.Conn(), all)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range plans {
				if p.ID == plan.ID {
					t.Errorf("%s is listed (includeDisabled %v)", name, all)
				}
			}
		}
		got, err := e.s.GetPlan(ctx, e.s.Conn(), plan.ID)
		if err != nil || got.DeletedAt == nil || got.Enabled {
			t.Fatalf("%s: the row %+v %v", name, got, err)
		}
		edit := *got
		if _, err := e.svc.UpsertPlan(ctx, e.owner, &edit); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: editing it: %v", name, err)
		}
		if err := e.svc.DeletePlan(ctx, e.owner, plan.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: deleting it twice: %v", name, err)
		}
	}
	if err := e.svc.DeletePlan(ctx, e.owner, "not an id"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a bad id: %v", err)
	}
	if _, err := e.svc.CreateOrder(ctx, CreateOrderParams{UserID: buyer.ID, PlanID: e.reward.ID, IdempotencyKey: "del-2"}); err == nil {
		t.Error("a deleted plan was ordered")
	}
	// The order made with it is still readable.
	if back, err := e.s.GetOrder(ctx, e.s.Conn(), o.ID); err != nil || back.PlanID != e.reward.ID {
		t.Fatalf("the order of the deleted plan: %v %v", back, err)
	}
}
