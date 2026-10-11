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

func seedTier(t *testing.T, svc *Service, actor *store.User, name string, discount, commission int32, prices map[string]int64) *store.ResellerTier {
	t.Helper()
	tier, err := svc.CreateTier(context.Background(), actor, TierInput{Name: name, DiscountPercent: discount,
		CommissionPercent: commission, Prices: prices}, "")
	if err != nil {
		t.Fatal(err)
	}
	return tier
}

// reseller makes a new user (Telegram id tg) a reseller on tier with an IRT
// credit limit.
func reseller(t *testing.T, svc *Service, s *store.Store, actor *store.User, tg int64, tier *store.ResellerTier, limit int64) *store.User {
	t.Helper()
	u := seedUser(t, s, tg)
	if _, err := svc.MakeReseller(context.Background(), actor, u.ID, ResellerInput{TierID: tier.ID,
		CreditLimits: map[string]int64{"IRT": limit}}, "a shop in Tabriz"); err != nil {
		t.Fatal(err)
	}
	u.Role = ResellerRole
	return u
}

func fieldOf(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

func walletEvents(t *testing.T, s *store.Store, userID, reason string) []events.WalletCreditedEvent {
	t.Helper()
	var out []events.WalletCreditedEvent
	for _, raw := range outboxEvents(t, s, events.WalletCredited) {
		var e events.WalletCreditedEvent
		if json.Unmarshal(raw, &e) == nil && e.UserID == userID && e.Reason == reason {
			out = append(out, e)
		}
	}
	return out
}

func ledgerKinds(t *testing.T, s *store.Store, userID, kind string) (n int, sum int64) {
	t.Helper()
	if err := s.DB().QueryRow(context.Background(), `SELECT count(*), COALESCE(sum(amount), 0) FROM core.ledger_entries
		WHERE user_id = $1 AND kind = $2`, userID, kind).Scan(&n, &sum); err != nil {
		t.Fatal(err)
	}
	return n, sum
}

// A reseller pays the tier's fixed price for a plan when it has one, else the
// store price less the tier's discount (rounded down); everyone else, and a
// banned reseller, pays the store price.
func TestPriceFor(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9100, "admin")
	a, b := seedPlan(t, s, false, 1000), seedPlan(t, s, false, 999)
	tier := seedTier(t, svc, admin, "Gold", 15, 10, map[string]int64{a.ID: 500})
	r := reseller(t, svc, s, admin, 9101, tier, 0)
	c := seedUser(t, s, 9102)

	price := func(u *store.User, p *store.Plan) int64 {
		t.Helper()
		got, err := svc.priceFor(ctx, u, p)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := price(r, a); got != 500 {
		t.Errorf("fixed price: %d", got)
	}
	if got := price(r, b); got != 849 { // 999 * 85 / 100 = 849.15
		t.Errorf("discounted price: %d", got)
	}
	if price(c, a) != 1000 || price(c, b) != 999 {
		t.Errorf("a customer pays the store price")
	}
	prices, err := svc.PlanPrices(ctx, r.ID, []store.Plan{*a, *b})
	if err != nil || prices[0] != 500 || prices[1] != 849 {
		t.Fatalf("plan prices for the reseller: %v %v", err, prices)
	}
	if prices, _ := svc.PlanPrices(ctx, c.ID, []store.Plan{*a, *b}); prices[0] != 1000 || prices[1] != 999 {
		t.Fatalf("plan prices for a customer: %v", prices)
	}
	if _, err := svc.PlanPrices(ctx, "00000000-0000-0000-0000-000000000099", []store.Plan{*a}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
	banned := *r
	banned.Status = "banned"
	if price(&banned, a) != 1000 {
		t.Errorf("a banned reseller gets the tier price")
	}
	// The role without a reseller row (never made so) is the store price.
	odd := *c
	odd.Role = ResellerRole
	if price(&odd, b) != 999 {
		t.Errorf("a reseller role without terms gets a tier price")
	}
}

// A reseller's orders and quotes carry the tier price and keep the label of
// a new service; discount codes are refused; customers' labels are ignored.
func TestResellerOrdersAndQuotes(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9200, "admin")
	plan := seedPlan(t, s, false, 1000)
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	r := reseller(t, svc, s, admin, 9201, tier, 0)
	c := seedUser(t, s, 9202)
	pct := int32(10)
	if _, err := svc.UpsertDiscount(ctx, admin, &store.Discount{Code: "SPRING", Percent: &pct, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	q, err := svc.QuoteOrder(ctx, QuoteParams{UserID: r.ID, PlanID: plan.ID})
	if err != nil || q.ListPrice != 800 || q.Discount != 0 || q.Total != 800 {
		t.Fatalf("reseller quote: %v %+v", err, q)
	}
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, IdempotencyKey: "r-1", Label: "  Ali's phone  "})
	if err != nil || o.Amount != 800 || o.Label == nil || *o.Label != "Ali's phone" || o.DiscountAmount != 0 {
		t.Fatalf("reseller order: %v %+v", err, o)
	}
	for name, label := range map[string]string{
		"too long": strings.Repeat("ب", MaxLabel+1), "two lines": "Ali\nphone", "a tab": "Ali\tphone",
	} {
		if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, IdempotencyKey: "r-bad-" + name, Label: label}); !errors.Is(err, ErrInvalid) {
			t.Errorf("label %s: %v", name, err)
		}
	}
	if o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, IdempotencyKey: "r-max", Label: strings.Repeat("ب", MaxLabel)}); err != nil || o.Label == nil {
		t.Fatalf("a 64-character Persian label: %v %+v", err, o)
	}
	if o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, IdempotencyKey: "r-none", Label: "   "}); err != nil || o.Label != nil {
		t.Fatalf("a blank label: %v %+v", err, o)
	}

	// Codes are for customers; resellers get tier prices instead.
	var de *DiscountError
	if _, err := svc.QuoteOrder(ctx, QuoteParams{UserID: r.ID, PlanID: plan.ID, DiscountCode: "spring"}); !errors.As(err, &de) || de.Reason != "reseller" || !errors.Is(err, ErrDiscount) {
		t.Fatalf("reseller quote with a code: %v", err)
	}
	if _, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, IdempotencyKey: "r-2", DiscountCode: "SPRING"}); !errors.As(err, &de) || de.Reason != "reseller" {
		t.Fatalf("reseller order with a code: %v", err)
	}
	// A customer pays the store price, uses the code, and their label is ignored.
	q, err = svc.QuoteOrder(ctx, QuoteParams{UserID: c.ID, PlanID: plan.ID, DiscountCode: "spring"})
	if err != nil || q.ListPrice != 1000 || q.Discount != 100 || q.Total != 900 {
		t.Fatalf("customer quote: %v %+v", err, q)
	}
	o, err = svc.CreateOrder(ctx, CreateOrderParams{UserID: c.ID, PlanID: plan.ID, IdempotencyKey: "c-1", DiscountCode: "SPRING", Label: "mine"})
	if err != nil || o.Amount != 900 || o.Label != nil {
		t.Fatalf("customer order: %v %+v", err, o)
	}
	if o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: c.ID, PlanID: plan.ID, IdempotencyKey: "c-2", Label: strings.Repeat("x", 100)}); err != nil || o.Label != nil {
		t.Fatalf("a customer's long label must be ignored, not refused: %v %+v", err, o)
	}

	// A fixed price of 0 makes a free order, paid at once and paying nobody.
	free := seedPlan(t, s, false, 500)
	if _, err := svc.UpdateTier(ctx, admin, tier.ID, tier.Version, TierInput{Name: "Gold", DiscountPercent: 20,
		CommissionPercent: 10, Prices: map[string]int64{free.ID: 0}}, ""); err != nil {
		t.Fatal(err)
	}
	if o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: free.ID, IdempotencyKey: "r-free"}); err != nil || o.Amount != 0 || o.Status != "paid" {
		t.Fatalf("free tier order: %v %+v", err, o)
	}
}

// A reseller buys for a customer on credit down to the limit and no further;
// the label reaches the service and its events; renewals keep the name;
// payments and refunds lower the debt.
func TestResellerBuysOnCredit(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9300, "admin")
	plan := planWithLimits(t, s, 30, 50<<30) // 1000
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	r := reseller(t, svc, s, admin, 9301, tier, 1000)

	buy := func(key, label string) (*store.Order, error) {
		t.Helper()
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, IdempotencyKey: key, Label: label})
		if err != nil {
			t.Fatal(err)
		}
		return svc.PayOrderWithWallet(ctx, o.ID, r.ID)
	}
	o1, err := buy("credit-1", "Ali's phone")
	if err != nil || o1.Status != "paid" {
		t.Fatalf("pay on credit: %v %+v", err, o1)
	}
	if bal, sum, _ := ledger(t, s, r.ID); bal != -800 || sum != -800 {
		t.Fatalf("after the first: balance %d ledger %d", bal, sum)
	}
	if _, err := buy("credit-2", ""); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("past the limit (-1600 of -1000): %v", err)
	}

	// Delivered under the reseller's account, named, and announced with the name.
	fp := &fakeProvisioner{}
	w := svc.NewProvisionWorker(fp, nil)
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provision: %v %v", worked, err)
	}
	sub, err := s.SubscriptionByOrder(ctx, s.Conn(), o1.ID)
	if err != nil || sub.UserID != r.ID || sub.Label == nil || *sub.Label != "Ali's phone" {
		t.Fatalf("subscription: %v %+v", err, sub)
	}
	var pe events.SubscriptionProvisionedEvent
	if evs := outboxEvents(t, s, events.SubscriptionProvisioned); len(evs) != 1 || json.Unmarshal(evs[0], &pe) != nil || pe.Label != "Ali's phone" {
		t.Fatalf("provisioned event: %s", evs)
	}

	// The staff record a payment: the debt goes down and buying works again.
	if _, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, 500, "IRT", "cash in the shop", "pay-key-0000000001"); err != nil {
		t.Fatal(err)
	}
	// A renewal with another name keeps the service's name (-300 - 800 = -1100 is too far).
	renew := func(key string) (*store.Order, error) {
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, Type: OrderRenew,
			SubscriptionID: sub.ID, IdempotencyKey: key, Label: "Someone else"})
		if err != nil {
			t.Fatal(err)
		}
		if o.Label != nil || o.Amount != 800 {
			t.Fatalf("renewal order: %+v", o)
		}
		return svc.PayOrderWithWallet(ctx, o.ID, r.ID)
	}
	if _, err := renew("renew-1"); !errors.Is(err, store.ErrInsufficientFunds) {
		t.Fatalf("renewal past the limit: %v", err)
	}
	if _, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, 100, "IRT", "card transfer", "pay-key-0000000002"); err != nil {
		t.Fatal(err)
	}
	if _, err := renew("renew-2"); err != nil {
		t.Fatalf("renewal to the limit (-1000): %v", err)
	}
	if bal, _, _ := ledger(t, s, r.ID); bal != -1000 {
		t.Fatalf("at the limit: %d", bal)
	}
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("extend: %v %v", worked, err)
	}
	got, _ := s.GetSubscription(ctx, s.Conn(), sub.ID)
	var ee events.SubscriptionExtendedEvent
	if evs := outboxEvents(t, s, events.SubscriptionExtended); got.Label == nil || *got.Label != "Ali's phone" ||
		len(evs) != 1 || json.Unmarshal(evs[0], &ee) != nil || ee.Label != "Ali's phone" {
		t.Fatalf("after the renewal: %+v %s", got, evs)
	}

	// Reminders name the service too.
	uw := svc.NewUsageWorker(fp, nil)
	uw.Every = -time.Hour
	exp := time.Now().Add(2 * 24 * time.Hour).Unix()
	fp.setUsage(sub.ID, Usage{UsedBytes: 1 << 30, TotalBytes: 100 << 30, ExpiresAt: exp, Enabled: true})
	if _, err := uw.SyncDue(ctx); err != nil {
		t.Fatal(err)
	}
	var re events.SubscriptionReminderEvent
	if evs := outboxEvents(t, s, events.SubscriptionReminder); len(evs) != 1 || json.Unmarshal(evs[0], &re) != nil || re.Label != "Ali's phone" {
		t.Fatalf("reminder: %s", evs)
	}

	// A refund credits the reseller, who paid: the debt goes down.
	owner := owner(t, svc)
	if _, err := svc.RefundOrder(ctx, owner, fp, o1.ID, "customer left"); err != nil {
		t.Fatal(err)
	}
	if bal, sum, _ := ledger(t, s, r.ID); bal != -200 || sum != -200 {
		t.Fatalf("after the refund: %d %d", bal, sum)
	}
}

// A settlement credits the reseller once per key, with its audit entry and a
// settlement event; a replay answers the first entry.
func TestResellerPayment(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9400, "admin")
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	r := reseller(t, svc, s, admin, 9401, tier, 5000)
	c := seedUser(t, s, 9402)
	// Owes 3000.
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := s.Debit(ctx, tx, r.ID, "IRT", 3000, &store.LedgerEntry{Kind: "purchase", IdempotencyKey: "owes"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	w, e, err := svc.RecordResellerPayment(ctx, admin, r.ID, 1200, "IRT", "paid at the shop", "settle-key-00000001")
	if err != nil || w.Balance != -1800 || e.Kind != "settlement" || e.Amount != 1200 || e.IdempotencyKey != "settlement:settle-key-00000001" ||
		e.RefType == nil || *e.RefType != "admin" || e.RefID == nil || *e.RefID != admin.ID || e.BalanceAfter != -1800 {
		t.Fatalf("payment: %v %+v %+v", err, w, e)
	}
	w2, e2, err := svc.RecordResellerPayment(ctx, admin, r.ID, 1200, "IRT", "paid at the shop", "settle-key-00000001")
	if err != nil || e2.ID != e.ID || w2.Balance != -1800 {
		t.Fatalf("replay: %v %+v %+v", err, w2, e2)
	}
	if bal, sum, _ := ledger(t, s, r.ID); bal != -1800 || sum != -1800 {
		t.Fatalf("balance after the replay: %d %d", bal, sum)
	}
	if n := auditCount(t, s, "reseller.payment"); n != 1 {
		t.Fatalf("audit entries: %d", n)
	}
	var entity, entityID, reason string
	if err := s.DB().QueryRow(ctx, `SELECT entity, entity_id, reason FROM core.audit_log WHERE action = 'reseller.payment'`).
		Scan(&entity, &entityID, &reason); err != nil || entity != "reseller" || entityID != r.ID || reason != "paid at the shop" {
		t.Fatalf("audit entry: %v %s %s %s", err, entity, entityID, reason)
	}
	if evs := walletEvents(t, s, r.ID, events.CreditSettlement); len(evs) != 1 || evs[0].Amount != 1200 || evs[0].Balance != -1800 || evs[0].TelegramID != r.TelegramID {
		t.Fatalf("settlement events: %+v", evs)
	}

	// The same key for another amount is a conflict; refusals.
	if _, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, 999, "IRT", "x", "settle-key-00000001"); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("key reused: %v", err)
	}
	for name, call := range map[string]func() error{
		"no reason": func() error {
			_, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, 1, "IRT", " ", "k-1")
			return err
		},
		"zero": func() error {
			_, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, 0, "IRT", "x", "k-2")
			return err
		},
		"negative": func() error {
			_, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, -5, "IRT", "x", "k-3")
			return err
		},
		"currency": func() error {
			_, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, 5, "EUR", "x", "k-4")
			return err
		},
		"no key": func() error { _, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, 5, "IRT", "x", ""); return err },
		"too absurd": func() error {
			_, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, maxTopupMinor+1, "IRT", "x", "k-5")
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := svc.RecordResellerPayment(ctx, admin, c.ID, 5, "IRT", "x", "k-6"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a customer is not a reseller: %v", err)
	}
	// Paying more than the debt leaves money in the wallet.
	if w, _, err := svc.RecordResellerPayment(ctx, admin, r.ID, 2000, "IRT", "overpaid", "settle-key-00000002"); err != nil || w.Balance != 200 {
		t.Fatalf("overpayment: %v %+v", err, w)
	}
}

// Commissions: every paid order of a customer who joined through an active
// reseller's link pays the reseller once; the referral reward is not paid
// then; a reseller's own purchases pay nobody.
func TestResellerCommission(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9500, "admin")
	plan := seedPlan(t, s, false, 1000)
	small := seedPlan(t, s, false, 5)
	setSetting(t, s, "referral.reward_percent", "20")
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	r := reseller(t, svc, s, admin, 9501, tier, 0)
	info, err := svc.ResellerInfo(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: 9502, ReferralCode: info.RefCode})
	if err != nil || c.ReferredBy == nil || *c.ReferredBy != r.ID {
		t.Fatalf("customer through the link: %v %+v", err, c)
	}
	fund(t, s, c.ID, 10_000)
	buy := func(u *store.User, p *store.Plan, key string) *store.Order {
		t.Helper()
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		if o, err = svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
			t.Fatal(err)
		}
		return o
	}

	o1 := buy(c, plan, "c-1") // the customer pays the store price
	if o1.Amount != 1000 {
		t.Fatalf("customer price: %d", o1.Amount)
	}
	buy(c, plan, "c-2")
	if n, sum := ledgerKinds(t, s, r.ID, "commission"); n != 2 || sum != 200 {
		t.Fatalf("commissions: %d %d", n, sum)
	}
	if n, _ := ledgerKinds(t, s, r.ID, "referral"); n != 0 {
		t.Fatal("a reseller was also paid the referral reward")
	}
	evs := walletEvents(t, s, r.ID, events.CreditCommission)
	if len(evs) != 2 || evs[0].Amount != 100 || evs[0].OrderID != o1.ID || evs[0].Balance != 100 || evs[1].Balance != 200 {
		t.Fatalf("commission events: %+v", evs)
	}
	var key, refType, refID string
	if err := s.DB().QueryRow(ctx, `SELECT idempotency_key, ref_type, ref_id FROM core.ledger_entries WHERE user_id = $1 AND kind = 'commission' ORDER BY created_at LIMIT 1`, r.ID).
		Scan(&key, &refType, &refID); err != nil || key != "commission:"+o1.ID || refType != "order" || refID != o1.ID {
		t.Fatalf("commission entry: %v %s %s %s", err, key, refType, refID)
	}

	// Once per order: paying the same order's effects again is a no-op.
	if err := s.WithTx(ctx, func(tx pgx.Tx) error { return svc.orderPaidEffects(ctx, tx, o1, events.PaidByWallet) }); err != nil {
		t.Fatalf("effects again: %v", err)
	}
	if n, _ := ledgerKinds(t, s, r.ID, "commission"); n != 2 {
		t.Fatalf("commission paid twice for one order: %d", n)
	}

	// The reseller's own rate wins; a commission rounding to 0 pays nothing.
	got, _ := s.GetReseller(ctx, s.Conn(), r.ID, false)
	own := int32(25)
	if _, err := svc.UpdateReseller(ctx, admin, r.ID, got.Version, ResellerInput{TierID: tier.ID, CommissionPercent: &own}, "top seller"); err != nil {
		t.Fatal(err)
	}
	buy(c, plan, "c-3")
	buy(c, small, "c-4") // 5 * 25% = 1.25: 1
	buy(c, small, "c-5")
	if n, sum := ledgerKinds(t, s, r.ID, "commission"); n != 5 || sum != 200+250+1+1 {
		t.Fatalf("own rate: %d %d", n, sum)
	}
	if _, err := svc.UpdateReseller(ctx, admin, r.ID, got.Version+1, ResellerInput{TierID: tier.ID, CommissionPercent: new(int32)}, "zero"); err != nil {
		t.Fatal(err)
	}
	buy(c, plan, "c-6")
	if n, _ := ledgerKinds(t, s, r.ID, "commission"); n != 5 {
		t.Fatal("a 0% rate paid a commission")
	}
	if _, err := svc.UpdateReseller(ctx, admin, r.ID, got.Version+2, ResellerInput{TierID: tier.ID}, "back to the tier"); err != nil {
		t.Fatal(err)
	}

	// The reseller's own purchases pay neither their inviter nor anyone else.
	inviter := seedUser(t, s, 9503)
	r2u, err := s.UpsertUser(ctx, s.Conn(), 9504, "", "", inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MakeReseller(ctx, admin, r2u.ID, ResellerInput{TierID: tier.ID, CreditLimits: map[string]int64{"IRT": 5000}}, "second shop"); err != nil {
		t.Fatal(err)
	}
	r2u.Role = ResellerRole
	buy(r2u, plan, "r2-1")
	if bal, _, _ := ledger(t, s, inviter.ID); bal != 0 {
		t.Fatalf("a reseller's purchase rewarded their inviter: %d", bal)
	}
	if n, _ := ledgerKinds(t, s, r2u.ID, "commission"); n != 0 {
		t.Fatal("a reseller earned a commission on their own purchase")
	}

	// A customer invited before the inviter became a reseller counts too.
	early, err := s.UpsertUser(ctx, s.Conn(), 9505, "", "", inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	fund(t, s, early.ID, 5000)
	buy(early, plan, "early-1") // the inviter is a customer: the referral reward
	if n, sum := ledgerKinds(t, s, inviter.ID, "referral"); n != 1 || sum != 200 {
		t.Fatalf("referral reward: %d %d", n, sum)
	}
	if _, err := svc.MakeReseller(ctx, admin, inviter.ID, ResellerInput{TierID: tier.ID}, "promoted"); err != nil {
		t.Fatal(err)
	}
	buy(early, plan, "early-2")
	if n, sum := ledgerKinds(t, s, inviter.ID, "commission"); n != 1 || sum != 100 {
		t.Fatalf("commission for an earlier customer: %d %d", n, sum)
	}

	// A banned reseller earns nothing (and the referral reward is not paid either).
	if _, err := svc.SetUserStatus(ctx, admin, r.ID, "banned", "fraud"); err != nil {
		t.Fatal(err)
	}
	buy(c, plan, "c-7")
	if n, _ := ledgerKinds(t, s, r.ID, "commission"); n != 5 {
		t.Fatal("a banned reseller earned a commission")
	}
	if n, _ := ledgerKinds(t, s, r.ID, "referral"); n != 0 {
		t.Fatal("a banned reseller was paid the referral reward")
	}
}

// A commission key already used for another amount is a conflict: the
// payment rolls back instead of being swallowed.
func TestResellerCommissionConflictRollsBack(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9550, "admin")
	plan := seedPlan(t, s, false, 1000)
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	r := reseller(t, svc, s, admin, 9551, tier, 0)
	c, err := s.UpsertUser(ctx, s.Conn(), 9552, "", "", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	fund(t, s, c.ID, 1000)
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: c.ID, PlanID: plan.ID, IdempotencyKey: "conflict"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := s.Credit(ctx, tx, r.ID, "IRT", 7, &store.LedgerEntry{Kind: "commission", IdempotencyKey: "commission:" + o.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, c.ID); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("want the conflict, got %v", err)
	}
	if got, _ := s.GetOrder(ctx, s.Conn(), o.ID); got.Status != "awaiting_payment" {
		t.Fatalf("the order was paid anyway: %s", got.Status)
	}
	if bal, _, _ := ledger(t, s, c.ID); bal != 1000 {
		t.Fatalf("the customer was charged: %d", bal)
	}
}

func TestResellerLifecycle(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9600, "admin")
	gold := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	silver := seedTier(t, svc, admin, "Silver", 10, 5, nil)
	u := seedUser(t, s, 9601)

	// Refusals before anything changes.
	in := ResellerInput{TierID: gold.ID, CreditLimits: map[string]int64{"IRT": 50_000}, Note: "  Tabriz bazaar  "}
	if _, err := svc.MakeReseller(ctx, admin, u.ID, in, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no reason: %v", err)
	}
	for field, bad := range map[string]ResellerInput{
		"tier_id":            {TierID: "00000000-0000-0000-0000-000000000099"},
		"credit_limit.EUR":   {TierID: gold.ID, CreditLimits: map[string]int64{"EUR": 1}},
		"credit_limit.IRT":   {TierID: gold.ID, CreditLimits: map[string]int64{"IRT": -1}},
		"commission_percent": {TierID: gold.ID, CommissionPercent: ptr32(51)},
		"note":               {TierID: gold.ID, Note: strings.Repeat("n", 501)},
	} {
		if _, err := svc.MakeReseller(ctx, admin, u.ID, bad, "x"); fieldOf(err) != field {
			t.Errorf("%s: %v", field, err)
		}
	}
	if _, err := svc.MakeReseller(ctx, admin, u.ID, ResellerInput{TierID: "nope"}, "x"); fieldOf(err) != "tier_id" {
		t.Errorf("a malformed tier id: %v", err)
	}
	staff := staffMember(t, s, 9602, "support")
	banned := seedUser(t, s, 9603)
	if _, err := svc.SetUserStatus(ctx, admin, banned.ID, "banned", "spam"); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"staff": staff.ID, "banned": banned.ID} {
		if _, err := svc.MakeReseller(ctx, admin, id, in, "x"); !errors.Is(err, ErrState) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := svc.MakeReseller(ctx, admin, "00000000-0000-0000-0000-000000000099", in, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
	if got, _ := s.GetUser(ctx, s.Conn(), u.ID); got.Role != "user" || auditCount(t, s, "reseller.add") != 0 {
		t.Fatalf("a refused request changed something: %+v", got)
	}

	// Made a reseller: role, terms, credit and the audit entry.
	r, err := svc.MakeReseller(ctx, admin, u.ID, in, "a shop in Tabriz")
	if err != nil || r.Version != 1 || r.Note != "Tabriz bazaar" || r.TierID != gold.ID {
		t.Fatalf("make: %v %+v", err, r)
	}
	if got, _ := s.GetUser(ctx, s.Conn(), u.ID); got.Role != ResellerRole {
		t.Fatalf("role: %s", got.Role)
	}
	if w, _ := s.GetWallet(ctx, s.Conn(), u.ID, "IRT"); w.CreditLimit != 50_000 {
		t.Fatalf("credit limit: %+v", w)
	}
	var before, after string
	if err := s.DB().QueryRow(ctx, `SELECT before::text, after::text FROM core.audit_log WHERE action = 'reseller.add' AND entity = 'reseller' AND entity_id = $1`, u.ID).
		Scan(&before, &after); err != nil || before != `{"role": "user"}` || !strings.Contains(after, `"tier": "Gold"`) || !strings.Contains(after, `"IRT": 50000`) {
		t.Fatalf("add audit: %v %s %s", err, before, after)
	}
	if _, err := svc.MakeReseller(ctx, admin, u.ID, in, "again"); !errors.Is(err, ErrState) {
		t.Fatalf("made twice: %v", err)
	}
	// AddStaff still refuses anyone who is not a customer.
	owner := owner(t, svc)
	if _, err := svc.AddStaff(ctx, owner, u.ID, "support", "x"); !errors.Is(err, ErrState) {
		t.Fatalf("a reseller made staff: %v", err)
	}

	// They owe 30,000.
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := s.Debit(ctx, tx, u.ID, "IRT", 30_000, &store.LedgerEntry{Kind: "purchase", IdempotencyKey: "owes-30k"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Terms: stale versions and limits below the debt are refused.
	up := ResellerInput{TierID: silver.ID, CreditLimits: map[string]int64{"IRT": 29_999}, CommissionPercent: ptr32(7), Note: "moved"}
	if _, err := svc.UpdateReseller(ctx, admin, u.ID, 1, up, "x"); fieldOf(err) != "credit_limit.IRT" || !strings.Contains(err.Error(), "30000 IRT") {
		t.Fatalf("limit below the debt: %v", err)
	}
	if _, err := svc.UpdateReseller(ctx, admin, u.ID, 7, up, "x"); !errors.Is(err, ErrState) {
		t.Fatalf("stale version: %v", err)
	}
	if _, err := svc.UpdateReseller(ctx, admin, u.ID, 1, up, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no reason: %v", err)
	}
	if _, err := svc.UpdateReseller(ctx, admin, staff.ID, 1, up, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("not a reseller: %v", err)
	}
	if err := svc.st.SetCreditLimit(ctx, s.Conn(), u.ID, "USDT", 5_000_000); err != nil {
		t.Fatal(err)
	}
	up.CreditLimits = map[string]int64{"IRT": 30_000} // USDT left out: it keeps its limit
	r, err = svc.UpdateReseller(ctx, admin, u.ID, 1, up, "moved to silver")
	if err != nil || r.Version != 2 || r.TierID != silver.ID || *r.CommissionPercent != 7 {
		t.Fatalf("update: %v %+v", err, r)
	}
	ws, _ := s.ListWallets(ctx, s.Conn(), u.ID)
	if len(ws) != 2 || ws[0].CreditLimit != 30_000 || ws[1].Currency != "USDT" || ws[1].CreditLimit != 5_000_000 {
		t.Fatalf("limits after the update: %+v", ws)
	}
	if terms, _ := s.ResellerTerms(ctx, s.Conn(), u.ID); terms.TierName != "Silver" || terms.CommissionPercent != 7 {
		t.Fatalf("terms: %+v", terms)
	}
	if err := s.DB().QueryRow(ctx, `SELECT before::text, after::text FROM core.audit_log WHERE action = 'reseller.terms'`).Scan(&before, &after); err != nil ||
		!strings.Contains(before, `"tier": "Gold"`) || !strings.Contains(after, `"tier": "Silver"`) || !strings.Contains(after, `"USDT": 5000000`) {
		t.Fatalf("terms audit: %v %s %s", err, before, after)
	}
	if _, err := svc.UpdateReseller(ctx, admin, u.ID, 1, up, "again with the old version"); !errors.Is(err, ErrState) {
		t.Fatalf("stale after an update: %v", err)
	}

	// The tier in use cannot go; removal waits for the debt.
	if err := svc.DeleteTier(ctx, admin, silver.ID, ""); !errors.Is(err, ErrState) {
		t.Fatalf("delete a tier in use: %v", err)
	}
	if err := svc.RemoveReseller(ctx, admin, u.ID, "closing"); !errors.Is(err, ErrState) || !strings.Contains(err.Error(), "30000 IRT") {
		t.Fatalf("remove while in debt: %v", err)
	}
	if _, _, err := svc.RecordResellerPayment(ctx, admin, u.ID, 30_000, "IRT", "settled", "settle-all-00000001"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveReseller(ctx, admin, u.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("remove without a reason: %v", err)
	}
	if err := svc.RemoveReseller(ctx, admin, u.ID, "closing the shop"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got, _ := s.GetUser(ctx, s.Conn(), u.ID); got.Role != "user" {
		t.Fatalf("role after removal: %s", got.Role)
	}
	if _, err := s.GetReseller(ctx, s.Conn(), u.ID, false); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reseller row after removal: %v", err)
	}
	for _, w := range func() []store.Wallet { ws, _ := s.ListWallets(ctx, s.Conn(), u.ID); return ws }() {
		if w.CreditLimit != 0 {
			t.Fatalf("credit kept after removal: %+v", w)
		}
	}
	if n := auditCount(t, s, "reseller.remove"); n != 1 {
		t.Fatalf("remove audit: %d", n)
	}
	if err := svc.RemoveReseller(ctx, admin, u.ID, "twice"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
	if err := svc.DeleteTier(ctx, admin, silver.ID, "unused now"); err != nil {
		t.Fatalf("delete the unused tier: %v", err)
	}
	// A customer again: the store price, and they can be made a reseller again.
	plan := seedPlan(t, s, false, 1000)
	if p, _ := svc.priceFor(ctx, u, plan); p != 1000 {
		t.Fatalf("price after removal: %d", p)
	}
	if _, err := svc.MakeReseller(ctx, admin, u.ID, ResellerInput{TierID: gold.ID}, "back"); err != nil {
		t.Fatalf("made a reseller again: %v", err)
	}
}

func ptr32(v int32) *int32 { return &v }

func TestResellerTiers(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9700, "admin")
	plan := seedPlan(t, s, false, 1000)
	trial := seedPlan(t, s, true, 0)

	for field, in := range map[string]TierInput{
		"name":               {Name: "  "},
		"discount_percent":   {Name: "A", DiscountPercent: 91},
		"commission_percent": {Name: "A", CommissionPercent: 51},
		"prices." + trial.ID: {Name: "A", Prices: map[string]int64{trial.ID: 0}},
		"prices.not-a-plan":  {Name: "A", Prices: map[string]int64{"not-a-plan": 1}},
		"prices." + plan.ID:  {Name: "A", Prices: map[string]int64{plan.ID: -1}},
		"prices.00000000-0000-0000-0000-000000000099": {Name: "A", Prices: map[string]int64{"00000000-0000-0000-0000-000000000099": 1}},
	} {
		if _, err := svc.CreateTier(ctx, admin, in, ""); fieldOf(err) != field {
			t.Errorf("%s: %v", field, err)
		}
	}
	if _, err := svc.CreateTier(ctx, admin, TierInput{Name: strings.Repeat("ط", 41)}, ""); fieldOf(err) != "name" {
		t.Errorf("a 41-character name: %v", err)
	}
	if _, err := svc.CreateTier(ctx, admin, TierInput{Name: "A", DiscountPercent: -1}, ""); fieldOf(err) != "discount_percent" {
		t.Errorf("a negative discount: %v", err)
	}

	gold, err := svc.CreateTier(ctx, admin, TierInput{Name: " طلایی ", DiscountPercent: 90, CommissionPercent: 50,
		Prices: map[string]int64{plan.ID: 650}}, "launch")
	if err != nil || gold.Name != "طلایی" || gold.Version != 1 || gold.Prices[plan.ID] != 650 {
		t.Fatalf("create: %v %+v", err, gold)
	}
	if _, err := svc.CreateTier(ctx, admin, TierInput{Name: "طلایی"}, ""); fieldOf(err) != "name" {
		t.Fatalf("duplicate name: %v", err)
	}
	if n := auditCount(t, s, "reseller_tier.upsert"); n != 1 {
		t.Fatalf("audit: %d", n)
	}

	// The update replaces the price list; an old version is stale.
	upd, err := svc.UpdateTier(ctx, admin, gold.ID, 1, TierInput{Name: "Gold", DiscountPercent: 30, CommissionPercent: 10}, "")
	if err != nil || upd.Version != 2 || len(upd.Prices) != 0 {
		t.Fatalf("update: %v %+v", err, upd)
	}
	if got, _ := s.GetTier(ctx, s.Conn(), gold.ID, false); got.Name != "Gold" || len(got.Prices) != 0 {
		t.Fatalf("stored: %+v", got)
	}
	if _, err := svc.UpdateTier(ctx, admin, gold.ID, 1, TierInput{Name: "Gold"}, ""); !errors.Is(err, ErrState) {
		t.Fatalf("stale: %v", err)
	}
	if _, err := svc.UpdateTier(ctx, admin, "00000000-0000-0000-0000-000000000099", 1, TierInput{Name: "X"}, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing tier: %v", err)
	}
	if _, err := svc.UpdateTier(ctx, admin, "bad", 1, TierInput{Name: "X"}, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("malformed id: %v", err)
	}
	if _, err := svc.UpdateTier(ctx, admin, gold.ID, 2, TierInput{Name: "Gold"}, strings.Repeat("r", 501)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a long reason: %v", err)
	}
	var before, after string
	if err := s.DB().QueryRow(ctx, `SELECT before::text, after::text FROM core.audit_log WHERE action = 'reseller_tier.upsert' AND before IS NOT NULL`).
		Scan(&before, &after); err != nil || !strings.Contains(before, "طلایی") || !strings.Contains(after, `"name": "Gold"`) {
		t.Fatalf("update audit: %v %s %s", err, before, after)
	}

	rows, err := svc.ListTiers(ctx)
	if err != nil || len(rows) != 1 || rows[0].Resellers != 0 {
		t.Fatalf("list: %v %+v", err, rows)
	}
	r := reseller(t, svc, s, admin, 9701, upd, 0)
	if err := svc.DeleteTier(ctx, admin, gold.ID, ""); !errors.Is(err, ErrState) {
		t.Fatalf("delete in use: %v", err)
	}
	if rows, _ := svc.ListTiers(ctx); rows[0].Resellers != 1 {
		t.Fatalf("count: %+v", rows)
	}
	if err := svc.RemoveReseller(ctx, admin, r.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteTier(ctx, admin, gold.ID, ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.DeleteTier(ctx, admin, gold.ID, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if n := auditCount(t, s, "reseller_tier.delete"); n != 1 {
		t.Fatalf("delete audit: %d", n)
	}
}

// Only a reseller names services, only their own, and the name is checked.
func TestSetSubscriptionLabel(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9800, "admin")
	plan := planWithLimits(t, s, 30, 50<<30)
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	// A customer's service, then they become a reseller.
	c, sub, _, _ := delivered(t, svc, s, 9801, plan)
	other, otherSub, _, _ := delivered(t, svc, s, 9802, plan)
	_ = other
	if _, err := svc.SetSubscriptionLabel(ctx, c.ID, sub.ID, "Ali"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a customer named a service: %v", err)
	}
	if _, err := svc.MakeReseller(ctx, admin, c.ID, ResellerInput{TierID: tier.ID}, "shop"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.SetSubscriptionLabel(ctx, c.ID, sub.ID, " Ali's phone ")
	if err != nil || got.Label == nil || *got.Label != "Ali's phone" {
		t.Fatalf("name: %v %+v", err, got)
	}
	if stored, _ := s.GetSubscription(ctx, s.Conn(), sub.ID); stored.Label == nil || *stored.Label != "Ali's phone" {
		t.Fatalf("stored: %+v", stored)
	}
	for name, id := range map[string]string{"someone else's": otherSub.ID, "unknown": "00000000-0000-0000-0000-000000000099", "malformed": "x"} {
		if _, err := svc.SetSubscriptionLabel(ctx, c.ID, id, "x"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := svc.SetSubscriptionLabel(ctx, c.ID, sub.ID, strings.Repeat("x", 65)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too long: %v", err)
	}
	if got, err := svc.SetSubscriptionLabel(ctx, c.ID, sub.ID, ""); err != nil || got.Label != nil {
		t.Fatalf("clear: %v %+v", err, got)
	}
	if err := s.SetSubscriptionStatus(ctx, s.Conn(), sub.ID, "deleted"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSubscriptionLabel(ctx, c.ID, sub.ID, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a deleted service: %v", err)
	}
	if _, err := svc.SetUserStatus(ctx, admin, c.ID, "banned", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSubscriptionLabel(ctx, c.ID, otherSub.ID, "x"); !errors.Is(err, ErrUserBanned) {
		t.Fatalf("banned: %v", err)
	}
}

func TestResellerInfo(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9900, "admin")
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	c := seedUser(t, s, 9901)
	if _, err := svc.ResellerInfo(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a customer: %v", err)
	}
	u := seedUser(t, s, 9902)
	if _, err := svc.MakeReseller(ctx, admin, u.ID, ResellerInput{TierID: tier.ID, CommissionPercent: ptr32(12)}, "x"); err != nil {
		t.Fatal(err)
	}
	info, err := svc.ResellerInfo(ctx, u.ID)
	if err != nil || info.Terms.TierName != "Gold" || info.Terms.DiscountPercent != 20 || info.Terms.CommissionPercent != 12 ||
		len(info.Wallets) != 1 || info.Wallets[0].Currency != "IRT" || info.Wallets[0].Balance != 0 || len(info.RefCode) != 8 || info.Customers != 0 {
		t.Fatalf("info without wallets: %v %+v", err, info)
	}
	if again, _ := svc.ResellerInfo(ctx, u.ID); again.RefCode != info.RefCode {
		t.Fatalf("the code changed: %s %s", info.RefCode, again.RefCode)
	}
	if err := s.SetCreditLimit(ctx, s.Conn(), u.ID, "IRT", 9000); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCreditLimit(ctx, s.Conn(), u.ID, "USDT", 1_000_000); err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.Debit(ctx, tx, u.ID, "IRT", 4000, &store.LedgerEntry{Kind: "purchase", IdempotencyKey: "info-buy"}); err != nil {
			return err
		}
		_, err := s.Credit(ctx, tx, u.ID, "IRT", 300, &store.LedgerEntry{Kind: "commission", IdempotencyKey: "commission:info"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertUser(ctx, s.Conn(), 9903, "", "", u.ID); err != nil {
		t.Fatal(err)
	}
	info, err = svc.ResellerInfo(ctx, u.ID)
	if err != nil || len(info.Wallets) != 2 || info.Wallets[0].Balance != -3700 || info.Wallets[0].CreditLimit != 9000 ||
		info.Wallets[1].Currency != "USDT" || info.Wallets[1].CreditLimit != 1_000_000 || info.Customers != 1 || info.Earned["IRT"] != 300 {
		t.Fatalf("info: %v %+v", err, info)
	}
	if _, err := svc.SetUserStatus(ctx, admin, u.ID, "banned", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResellerInfo(ctx, u.ID); !errors.Is(err, ErrUserBanned) {
		t.Fatalf("banned: %v", err)
	}
}

// The configured owner is not made owner while they are a reseller (their
// debt and customers would be orphaned); once removed, they are.
func TestPromoteOwnerSkipsReseller(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9950, "admin")
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	// The owner's account, created before the server named them (no promotion).
	u, err := s.UpsertUser(ctx, s.Conn(), ownerTG, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MakeReseller(ctx, admin, u.ID, ResellerInput{TierID: tier.ID}, "x"); !errors.Is(err, ErrState) {
		t.Fatalf("the configured owner made a reseller: %v", err)
	}
	// Made one some other way (an older install, a changed configuration).
	if err := s.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.SetRole(ctx, tx, u.ID, "user", ResellerRole); err != nil {
			return err
		}
		return s.InsertReseller(ctx, tx, &store.Reseller{UserID: u.ID, TierID: tier.ID})
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureOwner(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: ownerTG})
	if err != nil || got.Role != ResellerRole {
		t.Fatalf("after start-up and a visit: %v %+v", err, got)
	}
	if n := auditCount(t, s, "staff.owner_bootstrap"); n != 0 {
		t.Fatalf("promoted: %d", n)
	}
	if err := svc.RemoveReseller(ctx, admin, u.ID, "they own the store"); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: ownerTG}); err != nil || got.Role != "owner" {
		t.Fatalf("after removal: %v %+v", err, got)
	}
}

// Removing a reseller cancels their unpaid orders, which carry tier prices:
// paid later as a customer, one would buy at the tier price and pay their
// inviter a commission. A payment that still arrives for one stays in the
// wallet; their paid orders and other people's orders are left alone.
func TestRemoveResellerCancelsOpenOrders(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9960, "admin")
	plan := seedPlan(t, s, false, 1000)
	setSetting(t, s, "referral.reward_percent", "20")
	tier := seedTier(t, svc, admin, "Half", 50, 10, nil)
	r1 := reseller(t, svc, s, admin, 9961, tier, 0)
	// C joined through R1's link and was then made a reseller too.
	c, err := s.UpsertUser(ctx, s.Conn(), 9962, "", "", r1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MakeReseller(ctx, admin, c.ID, ResellerInput{TierID: tier.ID}, "a second shop"); err != nil {
		t.Fatal(err)
	}
	fund(t, s, c.ID, 500)
	paid, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: c.ID, PlanID: plan.ID, IdempotencyKey: "rm-paid"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, paid.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	var open []*store.Order
	for _, key := range []string{"rm-1", "rm-2"} {
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: c.ID, PlanID: plan.ID, IdempotencyKey: key})
		if err != nil || o.Amount != 500 || o.Status != "awaiting_payment" {
			t.Fatalf("tier-priced order: %v %+v", err, o)
		}
		open = append(open, o)
	}
	x := seedUser(t, s, 9963)
	other, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: x.ID, PlanID: plan.ID, IdempotencyKey: "rm-x"})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.RemoveReseller(ctx, admin, c.ID, "closing the shop"); err != nil {
		t.Fatal(err)
	}
	status := func(o *store.Order) string {
		t.Helper()
		got, err := s.GetOrder(ctx, s.Conn(), o.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Status
	}
	if status(open[0]) != "cancelled" || status(open[1]) != "cancelled" {
		t.Fatalf("unpaid orders after the removal: %s %s", status(open[0]), status(open[1]))
	}
	if status(paid) != "paid" || status(other) != "awaiting_payment" {
		t.Fatalf("paid or someone else's order changed: %s %s", status(paid), status(other))
	}
	var after string
	if err := s.DB().QueryRow(ctx, `SELECT after::text FROM core.audit_log WHERE action = 'reseller.remove' AND entity_id = $1`, c.ID).
		Scan(&after); err != nil || !strings.Contains(after, `"cancelled_orders": 2`) || !strings.Contains(after, `"role": "user"`) {
		t.Fatalf("remove audit: %v %s", err, after)
	}

	// A customer now: the wallet cannot pay a tier-priced order...
	fund(t, s, c.ID, 2000)
	if _, err := svc.PayOrderWithWallet(ctx, open[0].ID, c.ID); !errors.Is(err, ErrOrderNotPayable) {
		t.Fatalf("paid a cancelled tier-priced order: %v", err)
	}
	// ...and a payment that arrives for one stays in the wallet.
	if err := svc.HandlePaymentEvent(ctx, payEvent(t, "rm-e1", events.PaymentsPaymentSucceeded, events.PaymentEvent{
		IntentID: "00000000-0000-7000-8000-0000000009c1", OrderID: open[1].ID, UserID: c.ID, Amount: 500, Currency: "IRT",
	})); err != nil {
		t.Fatal(err)
	}
	if status(open[1]) != "cancelled" || len(walletEvents(t, s, c.ID, events.CreditOrderNotPayable)) != 1 {
		t.Fatalf("a late payment paid the order: %s", status(open[1]))
	}
	if bal, sum, _ := ledger(t, s, c.ID); bal != 2500 || sum != 2500 { // 500 - 500 + 2000 + 500
		t.Fatalf("balance %d ledger %d", bal, sum)
	}
	if n, _ := ledgerKinds(t, s, r1.ID, "commission"); n != 0 {
		t.Fatal("the inviter earned a commission on a business purchase")
	}
	if n, _ := ledgerKinds(t, s, r1.ID, "referral"); n != 0 {
		t.Fatal("the inviter was paid a referral reward on a business purchase")
	}

	// A new order is a customer's: the store price, and the inviter's commission.
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: c.ID, PlanID: plan.ID, IdempotencyKey: "rm-3"})
	if err != nil || o.Amount != 1000 {
		t.Fatalf("order as a customer: %v %+v", err, o)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if n, sum := ledgerKinds(t, s, r1.ID, "commission"); n != 1 || sum != 100 {
		t.Fatalf("commission on the customer's order: %d %d", n, sum)
	}
}

// waitLockWaiters waits until n sessions of the test database wait for a lock.
func waitLockWaiters(t *testing.T, s *store.Store, n int, what string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		var waiting int
		if err := s.DB().QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never waited (%d of %d waiting)", what, waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type orderResult struct {
	o   *store.Order
	err error
}

// An order being created at a tier price when the reseller is removed: the
// removal waits for it and then cancels it.
func TestRemoveResellerWaitsForAnOrderInFlight(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9970, "admin")
	plan := seedPlan(t, s, false, 1000)
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	r := reseller(t, svc, s, admin, 9971, tier, 0)

	// Holding the user's row stops the order at its insert (the foreign key
	// check), after it has priced the plan and held the reseller.
	tx, err := s.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit
	if _, err := tx.Exec(ctx, `SELECT 1 FROM core.users WHERE id = $1 FOR UPDATE`, r.ID); err != nil {
		t.Fatal(err)
	}
	created := make(chan orderResult, 1)
	go func() {
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, IdempotencyKey: "in-flight"})
		created <- orderResult{o, err}
	}()
	waitLockWaiters(t, s, 1, "the order")
	removed := make(chan error, 1)
	go func() { removed <- svc.RemoveReseller(ctx, admin, r.ID, "closing the shop") }()
	waitLockWaiters(t, s, 2, "the removal")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	res := <-created
	if res.err != nil || res.o.Amount != 800 {
		t.Fatalf("order: %v %+v", res.err, res.o)
	}
	if err := <-removed; err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got, _ := s.GetOrder(ctx, s.Conn(), res.o.ID); got.Status != "cancelled" {
		t.Fatalf("the tier-priced order outlived the removal: %s", got.Status)
	}
}

// An order that prices the plan for a reseller whose removal is under way
// waits for the removal and is then refused: no tier-priced order after it.
func TestOrderAfterResellerRemovedIsRefused(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9980, "admin")
	plan := seedPlan(t, s, false, 1000)
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	r := reseller(t, svc, s, admin, 9981, tier, 1000)

	// Holding the wallet stops the removal after it locked the reseller.
	tx, err := s.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Rollback
	if _, err := tx.Exec(ctx, `SELECT 1 FROM core.wallets WHERE user_id = $1 FOR UPDATE`, r.ID); err != nil {
		t.Fatal(err)
	}
	removed := make(chan error, 1)
	go func() { removed <- svc.RemoveReseller(ctx, admin, r.ID, "closing the shop") }()
	waitLockWaiters(t, s, 1, "the removal")
	created := make(chan orderResult, 1)
	go func() {
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, IdempotencyKey: "too-late"})
		created <- orderResult{o, err}
	}()
	waitLockWaiters(t, s, 2, "the order")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatalf("remove: %v", err)
	}
	if res := <-created; !errors.Is(res.err, ErrState) {
		t.Fatalf("an order priced for a removed reseller: %v %+v", res.err, res.o)
	}
	if _, err := s.OrderByIdempotencyKey(ctx, s.Conn(), "too-late"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the order was stored: %v", err)
	}
	// Asked again, they are a customer: the store price.
	if o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: r.ID, PlanID: plan.ID, IdempotencyKey: "too-late"}); err != nil || o.Amount != 1000 {
		t.Fatalf("retry as a customer: %v %+v", err, o)
	}
}
