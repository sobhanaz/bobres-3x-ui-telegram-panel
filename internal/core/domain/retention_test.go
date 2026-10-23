package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func topupPlan(t *testing.T, s *store.Store, traffic, price int64) *store.Plan {
	t.Helper()
	p := &store.Plan{NameI18n: map[string]string{"en": "+10GB"}, Kind: "traffic", Price: price, Currency: "IRT",
		Enabled: true, IsTopup: true, TrafficBytes: &traffic}
	if err := s.UpsertPlan(context.Background(), s.Conn(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

// delivered buys plan with the wallet and provisions it: a running
// subscription to renew or top up.
func delivered(t *testing.T, svc *Service, s *store.Store, tg int64, plan *store.Plan) (*store.User, *store.Subscription, *fakeProvisioner, *ProvisionWorker) {
	t.Helper()
	ctx := context.Background()
	u, o := paidWalletOrder(t, svc, s, tg, plan)
	fp := &fakeProvisioner{}
	w := svc.NewProvisionWorker(fp, nil)
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provision: %v %v", worked, err)
	}
	sub, err := s.SubscriptionByOrder(ctx, s.Conn(), o.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u, sub, fp, w
}

func TestExtendTargets(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	days, gb := int32(30), int64(50<<30)
	plan := &store.Plan{DurationDays: &days, TrafficBytes: &gb}
	in := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	total := func(b int64) *int64 { return &b }
	for _, c := range []struct {
		name          string
		typ           string
		sub           store.Subscription
		wantExp       *time.Time
		wantTotal     *int64
		planOverrides *store.Plan
	}{
		{"renew starts the days again and adds the traffic", OrderRenew, store.Subscription{ExpiresAt: in(5 * 24 * time.Hour), TrafficTotal: total(50 << 30)},
			in(30 * 24 * time.Hour), total(100 << 30), nil},
		{"a long time left is not kept either", OrderRenew, store.Subscription{ExpiresAt: in(90 * 24 * time.Hour), TrafficTotal: total(50 << 30)},
			in(30 * 24 * time.Hour), total(100 << 30), nil},
		{"an expired one counts from now", OrderRenew, store.Subscription{ExpiresAt: in(-3 * 24 * time.Hour), TrafficTotal: total(50 << 30)},
			in(30 * 24 * time.Hour), total(100 << 30), nil},
		{"unlimited stays unlimited", OrderRenew, store.Subscription{},
			nil, nil, nil},
		{"top-up adds traffic only", OrderTrafficTopup, store.Subscription{ExpiresAt: in(5 * 24 * time.Hour), TrafficTotal: total(50 << 30)},
			in(5 * 24 * time.Hour), total(60 << 30), &store.Plan{TrafficBytes: total(10 << 30)}},
	} {
		p := plan
		if c.planOverrides != nil {
			p = c.planOverrides
		}
		exp, tot := extendTargets(c.typ, &c.sub, p, now)
		if !sameTime(exp, c.wantExp) || !sameInt(tot, c.wantTotal) {
			t.Errorf("%s: got %v / %v, want %v / %v", c.name, exp, deref64(tot), c.wantExp, deref64(c.wantTotal))
		}
	}
}

func sameTime(a, b *time.Time) bool { return (a == nil) == (b == nil) && (a == nil || a.Equal(*b)) }
func sameInt(a, b *int64) bool      { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func deref64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func TestRenewalAndTopupOrders(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 50<<30)
	pack := topupPlan(t, s, 10<<30, 300)
	u, sub, fp, w := delivered(t, svc, s, 4101, plan)
	other := seedUser(t, s, 4102)

	refused := []struct {
		name string
		p    CreateOrderParams
		want error
	}{
		{"someone else's subscription", CreateOrderParams{UserID: other.ID, PlanID: plan.ID, Type: OrderRenew, SubscriptionID: sub.ID}, ErrForbidden},
		{"renewal with a traffic package", CreateOrderParams{UserID: u.ID, PlanID: pack.ID, Type: OrderRenew, SubscriptionID: sub.ID}, ErrPlanUnavailable},
		{"a traffic package as a new service", CreateOrderParams{UserID: u.ID, PlanID: pack.ID}, ErrPlanUnavailable},
		{"a top-up with a regular plan", CreateOrderParams{UserID: u.ID, PlanID: plan.ID, Type: OrderTrafficTopup, SubscriptionID: sub.ID}, ErrPlanUnavailable},
		{"a renewal without a subscription", CreateOrderParams{UserID: u.ID, PlanID: plan.ID, Type: OrderRenew}, ErrInvalid},
		{"a new order naming a subscription", CreateOrderParams{UserID: u.ID, PlanID: plan.ID, SubscriptionID: sub.ID}, ErrInvalid},
	}
	for i, c := range refused {
		c.p.IdempotencyKey = "refused-" + string(rune('a'+i))
		if _, err := svc.CreateOrder(ctx, c.p); !errors.Is(err, c.want) {
			t.Errorf("%s: want %v, got %v", c.name, c.want, err)
		}
	}

	// Renewal: the 30 days start again from now (the days left are not added: the first period was delivered a moment ago, so a stacked
	// renewal would end 60 days out), and the quota grows by 50 GB.
	fund(t, s, u.ID, 1300)
	ro, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, Type: OrderRenew, SubscriptionID: sub.ID, IdempotencyKey: "renew-1"})
	if err != nil || ro.Amount != plan.Price || ro.SubscriptionID == nil || *ro.SubscriptionID != sub.ID {
		t.Fatalf("renewal order: %+v %v", ro, err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, ro.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("extend: %v %v", worked, err)
	}
	wantExp := time.Now().Add(30 * 24 * time.Hour)
	near := func(unix int64) bool { return unix >= wantExp.Unix()-30 && unix <= wantExp.Unix()+30 }
	calls := fp.limitsFor(sub.ID)
	if len(calls) != 1 || !near(calls[0].expires) || calls[0].traffic != 100<<30 {
		t.Fatalf("panel limits: %+v, want about %d / %d", calls, wantExp.Unix(), int64(100<<30))
	}
	got, _ := s.GetSubscription(ctx, s.Conn(), sub.ID)
	order, _ := s.GetOrder(ctx, s.Conn(), ro.ID)
	if order.Status != "active" || got.Status != "active" || !near(got.ExpiresAt.Unix()) || *got.TrafficTotal != 100<<30 {
		t.Fatalf("after renewal: order %s, sub %+v", order.Status, got)
	}
	var ext events.SubscriptionExtendedEvent
	if evs := outboxEvents(t, s, events.SubscriptionExtended); len(evs) != 1 || json.Unmarshal(evs[0], &ext) != nil ||
		ext.Type != OrderRenew || !near(ext.ExpiresAt) || ext.TelegramID != u.TelegramID {
		t.Fatalf("extended event: %s", evs)
	}

	renewedAt := got.ExpiresAt

	// Top-up: 10 GB more, same expiry.
	to, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: pack.ID, Type: OrderTrafficTopup, SubscriptionID: sub.ID, IdempotencyKey: "topup-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, to.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("top-up: %v %v", worked, err)
	}
	got, _ = s.GetSubscription(ctx, s.Conn(), sub.ID)
	if !got.ExpiresAt.Equal(*renewedAt) || *got.TrafficTotal != 110<<30 {
		t.Fatalf("after top-up: %+v", got)
	}

	// A top-up is refused once the subscription expired (renew instead).
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET expires_at = now() - interval '1 day' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: pack.ID, Type: OrderTrafficTopup, SubscriptionID: sub.ID, IdempotencyKey: "topup-2"}); !errors.Is(err, ErrNotExtendable) {
		t.Fatalf("top-up of an expired subscription: %v", err)
	}
}

// The limits a renewal sets are fixed when it is first claimed: a retry
// after a panel failure sets the same values even if the subscription
// changed meanwhile, so nothing is added twice.
func TestRenewalRetryKeepsItsTargets(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 50<<30)
	u, sub, fp, w := delivered(t, svc, s, 4201, plan)
	fund(t, s, u.ID, 1001)
	ro, _ := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, Type: OrderRenew, SubscriptionID: sub.ID, IdempotencyKey: "renew-r"})
	if _, err := svc.PayOrderWithWallet(ctx, ro.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	fp.limitsFails = 1
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("first attempt: %v %v", worked, err)
	}
	first, _ := s.GetOrder(ctx, s.Conn(), ro.ID)
	if first.Status != "provision_failed" || !first.TargetsSet || first.TargetExpiresAt == nil || *first.TargetTrafficBytes != 100<<30 {
		t.Fatalf("after a failed attempt: %+v", first)
	}
	// The subscription moves on (usage sync); the retry ignores that.
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET expires_at = expires_at + interval '5 days', traffic_total_bytes = 1 WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.orders SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, ro.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("retry: %v %v", worked, err)
	}
	calls := fp.limitsFor(sub.ID)
	if len(calls) != 1 || calls[0].expires != first.TargetExpiresAt.Unix() || calls[0].traffic != 100<<30 {
		t.Fatalf("retry applied %+v, want the stored targets %d / %d", calls, first.TargetExpiresAt.Unix(), int64(100<<30))
	}
}

func TestReminderSchedule(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	total := int64(100)
	for _, c := range []struct {
		name       string
		sub        store.Subscription
		wantStatus string
		wantKind   string
		wantDays   int
	}{
		{"plenty left", store.Subscription{ExpiresAt: at(10 * 24 * time.Hour), TrafficTotal: &total, TrafficUsed: 10}, "active", "", 0},
		{"3 days left", store.Subscription{ExpiresAt: at(60 * time.Hour)}, "expiring_soon", events.ReminderExpiring, 3},
		{"3-day warning already sent", store.Subscription{ExpiresAt: at(60 * time.Hour), NotifiedExpiringAt: at(-time.Hour)}, "expiring_soon", "", 0},
		{"last day after the 3-day warning", store.Subscription{ExpiresAt: at(20 * time.Hour), NotifiedExpiringAt: at(-48 * time.Hour)}, "expiring_soon", events.ReminderExpiring, 1},
		{"last-day warning already sent", store.Subscription{ExpiresAt: at(10 * time.Hour), NotifiedExpiringAt: at(-2 * time.Hour)}, "expiring_soon", "", 0},
		{"80% of the traffic used", store.Subscription{TrafficTotal: &total, TrafficUsed: 85}, "expiring_soon", events.ReminderLowTraffic, 0},
		{"traffic warning already sent", store.Subscription{TrafficTotal: &total, TrafficUsed: 90, NotifiedLowTrafficAt: at(-time.Hour)}, "expiring_soon", "", 0},
		{"expired now", store.Subscription{ExpiresAt: at(-time.Hour)}, "expired", events.ReminderExpired, 0},
		{"expired long ago: recorded, no message", store.Subscription{ExpiresAt: at(-30 * 24 * time.Hour)}, "expired", "", 0},
		{"end already announced", store.Subscription{ExpiresAt: at(-time.Hour), NotifiedEndedAt: at(-time.Minute)}, "expired", "", 0},
		{"all traffic used", store.Subscription{TrafficTotal: &total, TrafficUsed: 100}, "depleted", events.ReminderDepleted, 0},
	} {
		r, kind, days := reminderDue(&c.sub, now)
		if r.Status != c.wantStatus || kind != c.wantKind || days != c.wantDays {
			t.Errorf("%s: status %q kind %q days %d", c.name, r.Status, kind, days)
		}
	}
	// A flag whose condition passed (extended in the panel) is cleared.
	r, _, _ := reminderDue(&store.Subscription{ExpiresAt: at(20 * 24 * time.Hour), NotifiedExpiringAt: at(-24 * time.Hour),
		NotifiedEndedAt: at(-48 * time.Hour)}, now)
	if r.NotifiedExpiringAt != nil || r.NotifiedEndedAt != nil {
		t.Fatalf("stale reminder flags kept: %+v", r)
	}
}

func TestUsageWorkerSyncsAndReminds(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := planWithLimits(t, s, 30, 100<<30)
	topupPlan(t, s, 10<<30, 300)
	u, sub, fp, _ := delivered(t, svc, s, 4301, plan)
	uw := svc.NewUsageWorker(fp, nil)
	uw.Every = -time.Hour // every subscription is due on every call

	exp := time.Now().Add(10 * 24 * time.Hour).Unix()
	fp.setUsage(sub.ID, Usage{UsedBytes: 10 << 30, TotalBytes: 100 << 30, ExpiresAt: exp, Enabled: true})
	if n, err := uw.SyncDue(ctx); n != 1 || err != nil {
		t.Fatalf("sync: %d %v", n, err)
	}
	got, _ := s.GetSubscription(ctx, s.Conn(), sub.ID)
	if got.TrafficUsed != 10<<30 || got.ExpiresAt.Unix() != exp || got.Status != "active" || len(outboxEvents(t, s, events.SubscriptionReminder)) != 0 {
		t.Fatalf("after a quiet sync: %+v", got)
	}

	// 85% used: one traffic warning, then nothing more.
	fp.setUsage(sub.ID, Usage{UsedBytes: 85 << 30, TotalBytes: 100 << 30, ExpiresAt: exp, Enabled: true})
	for i := 0; i < 2; i++ {
		if _, err := uw.SyncDue(ctx); err != nil {
			t.Fatal(err)
		}
	}
	evs := outboxEvents(t, s, events.SubscriptionReminder)
	var e events.SubscriptionReminderEvent
	if len(evs) != 1 || json.Unmarshal(evs[0], &e) != nil || e.Kind != events.ReminderLowTraffic || e.TelegramID != u.TelegramID || !e.TopupAvailable {
		t.Fatalf("traffic warning: %s", evs)
	}

	// The panel does not answer: the expiry reminder still goes out.
	if _, err := s.DB().Exec(ctx, `UPDATE core.subscriptions SET expires_at = now() + interval '2 days' WHERE id = $1`, sub.ID); err != nil {
		t.Fatal(err)
	}
	fp.mu.Lock()
	delete(fp.usage, sub.ID)
	fp.mu.Unlock()
	if _, err := uw.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	evs = outboxEvents(t, s, events.SubscriptionReminder)
	if len(evs) != 2 || json.Unmarshal(evs[1], &e) != nil || e.Kind != events.ReminderExpiring || e.Days != 2 {
		t.Fatalf("expiry reminder without the panel: %s", evs)
	}

	// A renewal starts a new period: the reminders can come again.
	if err := s.ApplySubscriptionLimits(ctx, s.Conn(), sub.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetSubscription(ctx, s.Conn(), sub.ID)
	if got.Status != "active" || got.NotifiedExpiringAt != nil || got.NotifiedLowTrafficAt != nil {
		t.Fatalf("after a renewal: %+v", got)
	}
}

func TestDiscountCodes(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	if _, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: ownerTG}); err != nil {
		t.Fatal(err)
	}
	plan := seedPlan(t, s, false, 100000)
	pct := int32(20)
	if _, err := svc.AdminUpsertDiscount(ctx, 4401, &store.Discount{Code: "X20", Percent: &pct, Enabled: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a customer created a code: %v", err)
	}
	d, err := svc.AdminUpsertDiscount(ctx, ownerTG, &store.Discount{Code: "spring20", Percent: &pct, Enabled: true})
	if err != nil || d.Code != "SPRING20" {
		t.Fatalf("create code: %+v %v", d, err)
	}
	u := seedUser(t, s, 4402)
	q, err := svc.QuoteOrder(ctx, QuoteParams{UserID: u.ID, PlanID: plan.ID, DiscountCode: " spring20 "})
	if err != nil || q.Total != 80000 || q.Discount != 20000 || q.DiscountCode != "SPRING20" {
		t.Fatalf("quote: %+v %v", q, err)
	}
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "disc-1", DiscountCode: "Spring20"})
	if err != nil || o.Amount != 80000 || o.DiscountAmount != 20000 || o.DiscountCode == nil || *o.DiscountCode != "SPRING20" {
		t.Fatalf("discounted order: %+v %v", o, err)
	}
	fund(t, s, u.ID, 80000)
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetDiscount(ctx, s.Conn(), "SPRING20"); d.Used != 1 {
		t.Fatalf("use not counted: %d", d.Used)
	}

	reason := func(err error) string {
		var de *DiscountError
		if errors.As(err, &de) {
			return de.Reason
		}
		return "no discount error: " + errorString(err)
	}
	if _, err := svc.QuoteOrder(ctx, QuoteParams{UserID: u.ID, PlanID: plan.ID, DiscountCode: "SPRING20"}); reason(err) != "already_used" {
		t.Errorf("second use by the same customer: %v", err)
	}
	one, past, fixed := int32(1), time.Now().Add(-time.Hour), int64(5)
	usdt := "USDT"
	for code, dd := range map[string]*store.Discount{
		"ONCE":    {Percent: &pct, MaxUses: &one, Enabled: true},
		"OLD":     {Percent: &pct, ExpiresAt: &past, Enabled: true},
		"OFF":     {Percent: &pct, Enabled: false},
		"DOLLARS": {Amount: &fixed, Currency: &usdt, Enabled: true},
	} {
		dd.Code = code
		if _, err := svc.AdminUpsertDiscount(ctx, ownerTG, dd); err != nil {
			t.Fatalf("create %s: %v", code, err)
		}
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.discount_codes SET used_count = 1 WHERE code = 'ONCE'`); err != nil {
		t.Fatal(err)
	}
	v := seedUser(t, s, 4403)
	for code, want := range map[string]string{"ONCE": "used_up", "OLD": "expired", "OFF": "unknown", "NOPE": "unknown", "DOLLARS": "currency", "x!": "unknown"} {
		if _, err := svc.QuoteOrder(ctx, QuoteParams{UserID: v.ID, PlanID: plan.ID, DiscountCode: code}); reason(err) != want {
			t.Errorf("%s: want %s, got %v", code, want, err)
		}
	}

	// 100% off: a free order, paid at once, and its use is counted too.
	full := int32(100)
	if _, err := svc.AdminUpsertDiscount(ctx, ownerTG, &store.Discount{Code: "GIFT", Percent: &full, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	free, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: v.ID, PlanID: plan.ID, IdempotencyKey: "gift-1", DiscountCode: "gift"})
	if err != nil || free.Amount != 0 || free.Status != "paid" {
		t.Fatalf("free order: %+v %v", free, err)
	}
	if d, _ := s.GetDiscount(ctx, s.Conn(), "GIFT"); d.Used != 1 {
		t.Fatalf("free order's use not counted: %d", d.Used)
	}
}

func errorString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func TestReferralRewards(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := seedPlan(t, s, false, 1000)
	inviter := seedUser(t, s, 4501)
	info, err := svc.Referral(ctx, inviter.ID)
	if err != nil || len(info.Code) != 8 || info.RewardPercent != 0 {
		t.Fatalf("referral info: %+v %v", info, err)
	}
	if again, _ := svc.Referral(ctx, inviter.ID); again.Code != info.Code {
		t.Fatalf("the code changed: %q then %q", info.Code, again.Code)
	}

	// The code links a new user; an existing user is never re-linked.
	friend, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: 4502, ReferralCode: strings.ToUpper(info.Code)})
	if err != nil || friend.ReferredBy == nil || *friend.ReferredBy != inviter.ID {
		t.Fatalf("invited user: %+v %v", friend, err)
	}
	existing := seedUser(t, s, 4503)
	if again, _ := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: existing.TelegramID, ReferralCode: info.Code}); again.ReferredBy != nil {
		t.Fatal("an existing user was linked to an inviter")
	}

	buy := func(key string) {
		t.Helper()
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: friend.ID, PlanID: plan.ID, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.PayOrderWithWallet(ctx, o.ID, friend.ID); err != nil {
			t.Fatal(err)
		}
	}
	fund(t, s, friend.ID, 3000)
	buy("ref-0") // the program is off: no reward
	if bal, _, _ := ledger(t, s, inviter.ID); bal != 0 {
		t.Fatalf("rewarded while the program is off: %d", bal)
	}

	setSetting(t, s, "referral.reward_percent", "10")
	// The first purchase was before the program; the reward goes to the next
	// one, and to that one only.
	buy("ref-1")
	buy("ref-2")
	if bal, _, _ := ledger(t, s, inviter.ID); bal != 100 {
		t.Fatalf("inviter balance: %d, want 100 (10%% of one purchase)", bal)
	}
	var e events.WalletCreditedEvent
	evs := outboxEvents(t, s, events.WalletCredited)
	found := false
	for _, raw := range evs {
		if json.Unmarshal(raw, &e) == nil && e.Reason == events.CreditReferral && e.UserID == inviter.ID && e.Amount == 100 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no referral credit event: %s", evs)
	}
	if info, _ := svc.Referral(ctx, inviter.ID); info.Invited != 1 || info.Rewarded != 1 || info.Earned["IRT"] != 100 || info.RewardPercent != 10 {
		t.Fatalf("stats: %+v", info)
	}
}

// A code is checked again when the order is paid: orders created with it
// before the first one was paid cannot all be paid at the discounted price.
func TestDiscountCodeCheckedWhenPaid(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	if _, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: ownerTG}); err != nil {
		t.Fatal(err)
	}
	plan := seedPlan(t, s, false, 100000)
	half, one := int32(50), int32(1)
	for code, max := range map[string]*int32{"WELCOME50": &one, "HALF": nil} {
		if _, err := svc.AdminUpsertDiscount(ctx, ownerTG, &store.Discount{Code: code, Percent: &half, MaxUses: max, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	reason := func(err error) string {
		var de *DiscountError
		if errors.As(err, &de) {
			return de.Reason
		}
		return "no discount error: " + errorString(err)
	}
	order := func(u *store.User, code, key string) *store.Order {
		t.Helper()
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: key, DiscountCode: code})
		if err != nil || o.Amount != 50000 {
			t.Fatalf("order %s: %+v %v", key, o, err)
		}
		return o
	}

	// One customer, three orders with a once-only code, created before any
	// is paid: the first pays at half price, the others are refused.
	u := seedUser(t, s, 4501)
	a, b, c := order(u, "WELCOME50", "w-a"), order(u, "WELCOME50", "w-b"), order(u, "WELCOME50", "w-c")
	fund(t, s, u.ID, 300000)
	if _, err := svc.PayOrderWithWallet(ctx, a.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	for _, o := range []*store.Order{b, c} {
		if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); reason(err) != "already_used" {
			t.Fatalf("second order with the code paid: %v", err)
		}
		if got, _ := s.GetOrder(ctx, s.Conn(), o.ID); got.Status != "awaiting_payment" {
			t.Fatalf("refused order is %s", got.Status)
		}
	}
	if bal, _, _ := ledger(t, s, u.ID); bal != 250000 {
		t.Fatalf("balance %d, want 250000 (one half-price purchase)", bal)
	}
	if d, _ := s.GetDiscount(ctx, s.Conn(), "WELCOME50"); d.Used != 1 {
		t.Fatalf("used_count %d", d.Used)
	}

	// Another customer's order made before the only use was taken: used up,
	// even with too little money in the wallet (no top-up for a lost price).
	v := seedUser(t, s, 4502)
	vo := order(v, "HALF", "h-v") // the HALF code, then a WELCOME50 order made earlier:
	if _, err := s.DB().Exec(ctx, `UPDATE core.orders SET discount_code = 'WELCOME50' WHERE id = $1`, vo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, vo.ID, v.ID); reason(err) != "used_up" {
		t.Fatalf("an order past the code's uses: %v", err)
	}

	// A payment that arrives for a refused order stays in the wallet.
	w := seedUser(t, s, 4503)
	first, second := order(w, "HALF", "h-1"), order(w, "HALF", "h-2")
	pay := func(id, intent string, o *store.Order) {
		t.Helper()
		m := payEvent(t, id, events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: intent, OrderID: o.ID,
			UserID: w.ID, Provider: "manual_card", Amount: 50000, Currency: "IRT"})
		if err := svc.HandlePaymentEvent(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	pay("h-e1", "00000000-0000-7000-8000-0000000045a1", first)
	pay("h-e2", "00000000-0000-7000-8000-0000000045a2", second)
	if got, _ := s.GetOrder(ctx, s.Conn(), first.ID); got.Status != "paid" {
		t.Fatalf("first order %s", got.Status)
	}
	if got, _ := s.GetOrder(ctx, s.Conn(), second.ID); got.Status != "awaiting_payment" {
		t.Fatalf("second order with the code: %s", got.Status)
	}
	if bal, _, _ := ledger(t, s, w.ID); bal != 50000 {
		t.Fatalf("balance %d, want the second payment (50000)", bal)
	}
	var reasons []string
	for _, raw := range outboxEvents(t, s, events.WalletCredited) {
		var e events.WalletCreditedEvent
		if json.Unmarshal(raw, &e) == nil && e.UserID == w.ID {
			reasons = append(reasons, e.Reason)
		}
	}
	if strings.Join(reasons, ",") != events.CreditDiscountUsed { // the first paid its order: no credit notice
		t.Fatalf("wallet credits: %v", reasons)
	}
}

// Two orders with a once-only code, paid at the same moment (one from the
// wallet, one by a payment arriving): exactly one gets the discounted
// price, and neither waits forever on the other's locks.
func TestDiscountCodePaidConcurrently(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	if _, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: ownerTG}); err != nil {
		t.Fatal(err)
	}
	plan := seedPlan(t, s, false, 100000)
	half, one := int32(50), int32(1)
	for round := range 5 {
		code := fmt.Sprintf("RACE%d", round)
		if _, err := svc.AdminUpsertDiscount(ctx, ownerTG, &store.Discount{Code: code, Percent: &half, MaxUses: &one, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		u := seedUser(t, s, int64(4600+round))
		var orders [2]*store.Order
		for i := range orders {
			o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, DiscountCode: code,
				IdempotencyKey: fmt.Sprintf("race-%d-%d", round, i)})
			if err != nil {
				t.Fatal(err)
			}
			orders[i] = o
		}
		fund(t, s, u.ID, 50000)
		pay := payEvent(t, fmt.Sprintf("race-e%d", round), events.PaymentsPaymentSucceeded, events.PaymentEvent{
			IntentID: fmt.Sprintf("00000000-0000-7000-8000-0000000046%02d", round), OrderID: orders[1].ID, UserID: u.ID,
			Provider: "manual_card", Amount: 50000, Currency: "IRT"})
		errs := make(chan error, 2)
		go func() { _, err := svc.PayOrderWithWallet(ctx, orders[0].ID, u.ID); errs <- err }()
		go func() { errs <- svc.HandlePaymentEvent(ctx, pay) }()
		for range 2 {
			if err := <-errs; err != nil && !errors.Is(err, ErrDiscount) {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		paid := 0
		for _, o := range orders {
			if got, _ := s.GetOrder(ctx, s.Conn(), o.ID); got.Status == "paid" {
				paid++
			}
		}
		d, _ := s.GetDiscount(ctx, s.Conn(), code)
		if bal, _, _ := ledger(t, s, u.ID); paid != 1 || d.Used != 1 || bal != 50000 {
			t.Fatalf("round %d: %d orders paid, used %d, balance %d", round, paid, d.Used, bal)
		}
	}
}
