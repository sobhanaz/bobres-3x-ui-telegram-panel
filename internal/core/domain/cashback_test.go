package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// cashbackEnv is a store with an owner, a plan of 10,000 Toman and the cashback switched on at 2.5%.
type cashbackEnv struct {
	*namingEnv
	fp   *fakeProvisioner
	keys int
}

func newCashbackEnv(t *testing.T) *cashbackEnv {
	t.Helper()
	e := &cashbackEnv{namingEnv: newNamingEnv(t), fp: &fakeProvisioner{}}
	e.plan.Price = 10_000
	if err := e.s.UpsertPlan(context.Background(), e.s.Conn(), e.plan); err != nil {
		t.Fatal(err)
	}
	e.set(map[string]string{SettingCashbackEnabled: "true", SettingCashbackPercent: "2.5"})
	return e
}

func (e *cashbackEnv) set(values map[string]string) {
	e.t.Helper()
	if _, err := e.svc.SetSettings(context.Background(), e.owner, values); err != nil {
		e.t.Fatal(err)
	}
}

// key is a request key no other call of this test used.
func (e *cashbackEnv) key() string {
	e.keys++
	return fmt.Sprintf("cb-%d", e.keys)
}

// fundNow puts amount in the wallet of u under a key of its own, so a test can fund the same user more than once.
func (e *cashbackEnv) fundNow(u *store.User, amount int64) {
	e.t.Helper()
	err := e.s.WithTx(context.Background(), func(tx pgx.Tx) error {
		_, err := e.s.Credit(context.Background(), tx, u.ID, "IRT", amount, &store.LedgerEntry{Kind: "topup", IdempotencyKey: "fund-" + e.key()})
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

// pay makes an order and pays it from the wallet, without delivering it.
func (e *cashbackEnv) pay(u *store.User, p CreateOrderParams) *store.Order {
	e.t.Helper()
	ctx := context.Background()
	p.UserID, p.IdempotencyKey = u.ID, e.key()
	if p.PlanID == "" {
		p.PlanID = e.plan.ID
	}
	o, err := e.svc.CreateOrder(ctx, p)
	if err != nil {
		e.t.Fatal(err)
	}
	if o.Status != "paid" {
		if o, err = e.svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
			e.t.Fatal(err)
		}
	}
	return o
}

// deliver has the worker deliver the next paid order.
func (e *cashbackEnv) deliver() {
	e.t.Helper()
	if worked, err := e.svc.NewProvisionWorker(e.fp, nil).ProvisionNext(context.Background()); err != nil || !worked {
		e.t.Fatalf("provisioning: %v %v", worked, err)
	}
}

// buy has u buy one new service of the plan and the worker deliver it.
func (e *cashbackEnv) buy(u *store.User) *store.Order {
	e.t.Helper()
	o := e.pay(u, CreateOrderParams{})
	e.deliver()
	return o
}

func (e *cashbackEnv) balance(u *store.User) int64 {
	e.t.Helper()
	w, err := e.s.GetWallet(context.Background(), e.s.Conn(), u.ID, "IRT")
	if err != nil {
		e.t.Fatal(err)
	}
	return w.Balance
}

// entries counts the ledger entries of a kind.
func (e *cashbackEnv) entries(kind string) int {
	e.t.Helper()
	var n int
	if err := e.s.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.ledger_entries WHERE kind = $1`, kind).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// consistent checks the wallet equals the sum of its ledger, the books' one rule.
func (e *cashbackEnv) consistent(u *store.User) {
	e.t.Helper()
	if bal, sum, _ := ledger(e.t, e.s, u.ID); bal != sum {
		e.t.Fatalf("the wallet holds %d but its ledger adds up to %d", bal, sum)
	}
}

func TestPercentIsRead(t *testing.T) {
	for in, want := range map[string]int64{
		"5": 500, "2.5": 250, "2,5": 250, "0.5": 50, "0.05": 5, "0.01": 1, "20": 2000, "20.00": 2000, "7%": 700, " 7 % ": 700,
		"۲٫۵": 250, "۰/۵": 50, "٢،٥": 250, ".5": 50, "5.": 500, "12.34": 1234, "05": 500,
	} {
		if got, err := ParsePercentBP(in); err != nil || got != want {
			t.Errorf("ParsePercentBP(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", ".", "0", "0.00", "0.001", "20.01", "21", "100", "-1", "-0.5", "abc", "1.2.3", "1e1", "5 5", "٪", "1,000.5", "9999"} {
		if got, err := ParsePercentBP(bad); err == nil {
			t.Errorf("ParsePercentBP(%q) = %d: accepted", bad, got)
		}
	}
	for bp, want := range map[int64]string{250: "2.5", 2000: "20", 5: "0.05", 50: "0.5", 1: "0.01", 1234: "12.34", 100: "1"} {
		if got := FormatPercentBP(bp); got != want {
			t.Errorf("FormatPercentBP(%d) = %q, want %q", bp, got, want)
		}
	}
}

// The settings are checked as they are saved: the percent is written the short way, the amounts are whole numbers.
func TestCashbackSettingsAreChecked(t *testing.T) {
	for in, want := range map[string]string{"5": "5", "5.50": "5.5", "۲٫۵": "2.5", "0.5%": "0.5", "": ""} {
		if got, err := NormalizeSetting(SettingCashbackPercent, in); err != nil || got != want {
			t.Errorf("percent %q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"0", "20.01", "abc", "1.234", "-3"} {
		var fe *FieldError
		if _, err := NormalizeSetting(SettingCashbackPercent, bad); !errors.As(err, &fe) {
			t.Errorf("percent %q: %v", bad, err)
		}
	}
	for _, key := range []string{SettingCashbackMax, SettingCashbackMinOrder} {
		if got, err := NormalizeSetting(key, "50,000"); err != nil || got != "50000" {
			t.Errorf("%s: %q %v", key, got, err)
		}
		if got, err := NormalizeSetting(key, "۱۰۰۰"); err != nil || got != "1000" {
			t.Errorf("%s in Persian digits: %q %v", key, got, err)
		}
		for _, bad := range []string{"-1", "1.5", "abc", "100000000001"} {
			if _, err := NormalizeSetting(key, bad); err == nil {
				t.Errorf("%s: %q accepted", key, bad)
			}
		}
	}
	for _, key := range []string{SettingCashbackEnabled, SettingCashbackRenewals} {
		if got, err := NormalizeSetting(key, "on"); err != nil || got != "true" {
			t.Errorf("%s: %q %v", key, got, err)
		}
	}
	// It is off until the owner switches it on, and renewals earn unless the owner says not.
	cfg := CashbackConfigOf(map[string]string{})
	if cfg.Enabled || !cfg.Renewals || cfg.PercentBP != 0 {
		t.Errorf("defaults: %+v", cfg)
	}
	// A value that is not valid reads as unset, so a bad one never pays on a guess.
	if cfg := CashbackConfigOf(map[string]string{SettingCashbackEnabled: "true", SettingCashbackPercent: "lots", SettingCashbackMax: "-5"}); cfg.PercentBP != 0 || cfg.MaxPerOrder != 0 {
		t.Errorf("bad values: %+v", cfg)
	}
}

// floor(amount x percent / 100), cut to the maximum; the product never wraps.
func TestCashbackAmount(t *testing.T) {
	for _, c := range []struct {
		base, bp, most int64
		amount         int64
		capped         bool
	}{
		{10_000, 250, 0, 250, false},
		{999, 250, 0, 24, false}, // 24.975 rounds down
		{39, 250, 0, 0, false},   // 0.975: nothing
		{40, 250, 0, 1, false},
		{10_000, 250, 100, 100, true},
		{10_000, 250, 250, 250, false}, // exactly the maximum is not a cut
		{10_000, 250, 1_000, 250, false},
		{1_000_000, 2000, 0, 200_000, false},
		{1, 2000, 0, 0, false},
		{5, 2000, 0, 1, false},
		{0, 250, 0, 0, false},
		{-5, 250, 0, 0, false},
		{10_000, 0, 0, 0, false},
		{math.MaxInt64, 2000, 0, math.MaxInt64 / 5, false}, // a huge price does not wrap
		{math.MaxInt64, 1, 0, math.MaxInt64 / 10_000, false},
	} {
		amount, capped := cashbackAmount(c.base, c.bp, c.most)
		// The exact floor, by big arithmetic.
		if c.base > 0 && c.bp > 0 && c.most == 0 {
			exact := new(big.Int).Mul(big.NewInt(c.base), big.NewInt(c.bp))
			if want := exact.Div(exact, big.NewInt(10_000)).Int64(); amount != want {
				t.Errorf("cashbackAmount(%d, %d) = %d, the exact floor is %d", c.base, c.bp, amount, want)
			}
		}
		if amount != c.amount || capped != c.capped {
			t.Errorf("cashbackAmount(%d, %d, %d) = %d, %v; want %d, %v", c.base, c.bp, c.most, amount, capped, c.amount, c.capped)
		}
	}
}

func TestWhoEarnsCashback(t *testing.T) {
	e := newCashbackEnv(t)
	buyer := seedUser(t, e.s, 9601)
	banned := *buyer
	banned.Status = "banned"
	reseller := e.reseller(9602)
	trial := *e.plan
	trial.IsTrial = true
	on := CashbackConfig{Enabled: true, PercentBP: 250, Renewals: true}
	noRenewals := on
	noRenewals.Renewals = false
	min := on
	min.MinOrder = 10_000
	order := func(typ string, amount int64) store.Order {
		return store.Order{Type: typ, Amount: amount, Currency: "IRT"}
	}
	for name, tc := range map[string]struct {
		cfg   CashbackConfig
		o     store.Order
		plan  *store.Plan
		buyer *store.User
		want  bool
	}{
		"a new purchase":              {on, order("new", 10_000), e.plan, buyer, true},
		"a renewal":                   {on, order("renew", 10_000), e.plan, buyer, true},
		"a traffic package":           {on, order("traffic_topup", 10_000), e.plan, buyer, true},
		"a renewal when they do not":  {noRenewals, order("renew", 10_000), e.plan, buyer, false},
		"a package when renewals not": {noRenewals, order("traffic_topup", 10_000), e.plan, buyer, false},
		"a new one when renewals not": {noRenewals, order("new", 10_000), e.plan, buyer, true},
		"switched off":                {CashbackConfig{PercentBP: 250, Renewals: true}, order("new", 10_000), e.plan, buyer, false},
		"no percent":                  {CashbackConfig{Enabled: true, Renewals: true}, order("new", 10_000), e.plan, buyer, false},
		"a free order":                {on, order("new", 0), e.plan, buyer, false},
		"a reseller":                  {on, order("new", 10_000), e.plan, reseller, false},
		"a banned customer":           {on, order("new", 10_000), e.plan, &banned, false},
		"a free trial":                {on, order("new", 10_000), &trial, buyer, false},
		"paid in USDT":                {on, store.Order{Type: "new", Amount: 5_000_000, Currency: "USDT"}, e.plan, buyer, false},
		"below the minimum":           {min, order("new", 9_999), e.plan, buyer, false},
		"at the minimum":              {min, order("new", 10_000), e.plan, buyer, true},
		"an unknown type":             {on, order("gift", 10_000), e.plan, buyer, false},
		"no customer":                 {on, order("new", 10_000), e.plan, nil, false},
	} {
		if got := tc.cfg.earns(&tc.o, tc.plan, tc.buyer); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	refunded := order("new", 10_000)
	now := refunded.CreatedAt
	refunded.RefundedAt = &now
	if on.earns(&refunded, e.plan, buyer) {
		t.Error("a refunded order earns")
	}
}

// An order that is paid and delivered credits the buyer's wallet once: a ledger entry, a record of how it was worked out, and a message.
func TestCashbackIsCreditedWhenTheOrderIsDelivered(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9611)
	e.fundNow(u, 10_000)

	o := e.pay(u, CreateOrderParams{})
	// Paid is not delivered: nothing yet.
	if n := e.entries("cashback"); n != 0 || e.balance(u) != 0 {
		t.Fatalf("cashback before delivery: %d entries, balance %d", n, e.balance(u))
	}
	e.deliver()

	if got := e.balance(u); got != 250 {
		t.Fatalf("balance %d, want 250 (2.5%% of 10,000)", got)
	}
	e.consistent(u)
	var kind, key, refType, refID string
	var amount int64
	if err := e.s.DB().QueryRow(ctx, `SELECT kind, idempotency_key, ref_type, ref_id, amount FROM core.ledger_entries WHERE kind = 'cashback'`).
		Scan(&kind, &key, &refType, &refID, &amount); err != nil {
		t.Fatalf("the ledger entry: %v", err)
	}
	if key != "cashback:"+o.ID || refType != "order" || refID != o.ID || amount != 250 {
		t.Fatalf("entry: %s %s %s %s %d", kind, key, refType, refID, amount)
	}
	c, err := e.svc.OrderCashback(ctx, o.ID)
	if err != nil || c == nil {
		t.Fatalf("the record: %v %v", c, err)
	}
	if c.UserID != u.ID || c.BaseAmount != 10_000 || c.PercentBP != 250 || c.Amount != 250 || c.Capped || c.Currency != "IRT" || c.ReversedAt != nil {
		t.Fatalf("record %+v", c)
	}
	var evs []events.WalletCreditedEvent
	for _, raw := range outboxEvents(t, e.s, events.WalletCredited) {
		var ev events.WalletCreditedEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		evs = append(evs, ev)
	}
	if len(evs) != 1 || evs[0].Reason != events.CreditCashback || evs[0].Amount != 250 || evs[0].Balance != 250 || evs[0].OrderID != o.ID ||
		evs[0].TelegramID != u.TelegramID || evs[0].Currency != "IRT" {
		t.Fatalf("events %+v", evs)
	}
	if total, err := e.svc.CashbackEarned(ctx, u.ID); err != nil || total != 250 {
		t.Fatalf("earned %d %v", total, err)
	}

	// Doing it again for the same order (a retried delivery) pays nothing more and tells nobody.
	for range 3 {
		err := e.s.WithTx(ctx, func(tx pgx.Tx) error { return e.svc.afterDelivery(ctx, tx, o, e.plan, "") })
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := e.entries("cashback"); n != 1 || e.balance(u) != 250 {
		t.Fatalf("paid again: %d entries, balance %d", n, e.balance(u))
	}
	if got := outboxEvents(t, e.s, events.WalletCredited); len(got) != 1 {
		t.Fatalf("told again: %d events", len(got))
	}
	e.consistent(u)
}

// The owner's percent, cap and minimum decide the amount; each order earns by its own price after any discount.
func TestCashbackFollowsTheOwnersSettings(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9621)
	e.fundNow(u, 100_000)

	// A cap.
	e.set(map[string]string{SettingCashbackMax: "100"})
	o1 := e.buy(u)
	c, _ := e.svc.OrderCashback(ctx, o1.ID)
	if c == nil || c.Amount != 100 || !c.Capped {
		t.Fatalf("a capped credit: %+v", c)
	}
	// No cap, a different percent: the amount follows the setting at the time of delivery.
	e.set(map[string]string{SettingCashbackMax: "", SettingCashbackPercent: "0.5"})
	o2 := e.buy(u)
	if c, _ = e.svc.OrderCashback(ctx, o2.ID); c == nil || c.Amount != 50 || c.PercentBP != 50 || c.Capped {
		t.Fatalf("0.5%%: %+v", c)
	}
	// A minimum above the price: nothing.
	e.set(map[string]string{SettingCashbackMinOrder: "10001"})
	o3 := e.buy(u)
	if c, _ = e.svc.OrderCashback(ctx, o3.ID); c != nil {
		t.Fatalf("below the minimum: %+v", c)
	}
	// Switched off: nothing, and what was paid stays.
	e.set(map[string]string{SettingCashbackMinOrder: "", SettingCashbackEnabled: "false"})
	o4 := e.buy(u)
	if c, _ = e.svc.OrderCashback(ctx, o4.ID); c != nil {
		t.Fatalf("switched off: %+v", c)
	}
	if n := e.entries("cashback"); n != 2 {
		t.Fatalf("%d credits", n)
	}
	// 100,000 - 4 x 10,000 + 100 + 50.
	if got := e.balance(u); got != 60_150 {
		t.Fatalf("balance %d", got)
	}
	e.consistent(u)
}

// An order discounted by a code earns on what it cost, and one the code made free earns nothing.
func TestCashbackIsWorkedOutOnWhatTheOrderCost(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9631)
	e.fundNow(u, 50_000)
	half, all := int32(50), int32(100)
	for _, d := range []*store.Discount{{Code: "HALF", Percent: &half, Enabled: true}, {Code: "FREE", Percent: &all, Enabled: true}} {
		if _, err := e.svc.UpsertDiscount(ctx, e.owner, d); err != nil {
			t.Fatal(err)
		}
	}
	halfOrder := e.pay(u, CreateOrderParams{DiscountCode: "HALF"})
	e.deliver()
	if c, _ := e.svc.OrderCashback(ctx, halfOrder.ID); halfOrder.Amount != 5_000 || c == nil || c.BaseAmount != 5_000 || c.Amount != 125 {
		t.Fatalf("a half-price order (%d): %+v", halfOrder.Amount, c)
	}
	free := e.pay(u, CreateOrderParams{DiscountCode: "FREE"})
	e.deliver()
	if c, _ := e.svc.OrderCashback(ctx, free.ID); free.Amount != 0 || c != nil {
		t.Fatalf("a free order (%d): %+v", free.Amount, c)
	}
	e.consistent(u)
}

// Renewals and traffic packages earn while the owner lets renewals earn.
func TestCashbackOnRenewals(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9641)
	e.fundNow(u, 100_000)
	first := e.buy(u)
	sub, err := e.s.SubscriptionByOrder(ctx, e.s.Conn(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	renew := func() *store.Order {
		o := e.pay(u, CreateOrderParams{Type: OrderRenew, SubscriptionID: sub.ID})
		e.deliver()
		return o
	}
	r1 := renew()
	if c, _ := e.svc.OrderCashback(ctx, r1.ID); c == nil || c.Amount != 250 {
		t.Fatalf("a renewal: %+v", c)
	}
	e.set(map[string]string{SettingCashbackRenewals: "false"})
	r2 := renew()
	if c, _ := e.svc.OrderCashback(ctx, r2.ID); c != nil {
		t.Fatalf("a renewal when they do not earn: %+v", c)
	}
	// A new service still does.
	if c, _ := e.svc.OrderCashback(ctx, e.buy(u).ID); c == nil {
		t.Fatal("a new service did not earn")
	}
	e.consistent(u)
}

// A reseller buys at tier prices on credit and has targets instead: no cashback, though it is on for everyone else.
func TestResellersEarnNoCashback(t *testing.T) {
	e := newCashbackEnv(t)
	r := e.reseller(9651)
	o := e.buy(r)
	if c, _ := e.svc.OrderCashback(context.Background(), o.ID); c != nil {
		t.Fatalf("a reseller earned: %+v", c)
	}
	if n := e.entries("cashback"); n != 0 {
		t.Fatalf("%d credits", n)
	}
}

// The customer is read as they are when the delivery commits: one banned while the panel was called earns nothing.
func TestCashbackChecksTheBuyerAtDelivery(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9661)
	e.fundNow(u, 10_000)
	o := e.pay(u, CreateOrderParams{})
	if err := e.s.SetUserRole(ctx, e.s.Conn(), u.ID, "user", "banned"); err != nil {
		t.Fatal(err)
	}
	e.deliver()
	if c, _ := e.svc.OrderCashback(ctx, o.ID); c != nil {
		t.Fatalf("a banned customer earned: %+v", c)
	}
	if got, err := e.s.GetOrder(ctx, e.s.Conn(), o.ID); err != nil || got.Status != "active" {
		t.Fatalf("the paid service must still be delivered: %v %v", got, err)
	}
}

// Many deliveries racing for one order pay once.
func TestCashbackIsPaidOnceUnderConcurrency(t *testing.T) {
	e := newCashbackEnv(t)
	needConns(t, e.s, 8)
	ctx := context.Background()
	u := seedUser(t, e.s, 9671)
	e.fundNow(u, 10_000)
	o := e.pay(u, CreateOrderParams{})
	e.deliver() // the delivery itself paid it

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.s.WithTx(ctx, func(tx pgx.Tx) error { return e.svc.afterDelivery(ctx, tx, o, e.plan, "") })
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a racing delivery failed: %v", err)
		}
	}
	if n := e.entries("cashback"); n != 1 || e.balance(u) != 250 {
		t.Fatalf("%d credits, balance %d", n, e.balance(u))
	}
	e.consistent(u)
}

// Many orders delivered at once for one customer: every one earns, the wallet equals its ledger.
func TestCashbackOfManyOrdersAtOnce(t *testing.T) {
	e := newCashbackEnv(t)
	needConns(t, e.s, 8)
	ctx := context.Background()
	u := seedUser(t, e.s, 9672)
	e.fundNow(u, 60_000)
	var orders []*store.Order
	for range 6 {
		orders = append(orders, e.pay(u, CreateOrderParams{}))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.NewProvisionWorker(&fakeProvisioner{}, nil).ProvisionNext(ctx)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("provisioning: %v", err)
		}
	}
	// A worker that found nothing left is fine; every order that was delivered earned.
	delivered := 0
	for _, o := range orders {
		got, _ := e.s.GetOrder(ctx, e.s.Conn(), o.ID)
		c, _ := e.svc.OrderCashback(ctx, o.ID)
		if (got.Status == "active") != (c != nil) {
			t.Errorf("order %s is %s and its cashback is %v", o.ID, got.Status, c)
		}
		if got.Status == "active" {
			delivered++
		}
	}
	if n := e.entries("cashback"); n != delivered || e.balance(u) != int64(delivered)*250 {
		t.Fatalf("%d credits for %d deliveries, balance %d", n, delivered, e.balance(u))
	}
	e.consistent(u)
}

// A refund takes the cashback back with it, once, in the same transaction, and says so.
func TestRefundTakesTheCashbackBack(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9681)
	e.fundNow(u, 10_000)
	o := e.buy(u)
	if e.balance(u) != 250 {
		t.Fatalf("balance %d", e.balance(u))
	}

	res, err := e.svc.RefundOrder(ctx, e.owner, e.fp, o.ID, "asked for it back")
	if err != nil {
		t.Fatal(err)
	}
	// The customer paid 10,000, got 250 back, and now has the 10,000 back and no cashback: 10,000.
	if got := e.balance(u); got != 10_000 || res.Wallet.Balance != 10_000 {
		t.Fatalf("balance %d, refund said %d", got, res.Wallet.Balance)
	}
	e.consistent(u)
	c, _ := e.svc.OrderCashback(ctx, o.ID)
	if c == nil || c.ReversedAt == nil || c.ReversedAmount == nil || *c.ReversedAmount != 250 || c.ReversalEntryID == nil {
		t.Fatalf("the record after the refund: %+v", c)
	}
	var key, kind string
	var amount int64
	if err := e.s.DB().QueryRow(ctx, `SELECT idempotency_key, kind, amount FROM core.ledger_entries WHERE kind = 'cashback_reversal'`).Scan(&key, &kind, &amount); err != nil {
		t.Fatalf("the reversal: %v", err)
	}
	if key != "cashback_reversal:"+o.ID || amount != -250 {
		t.Fatalf("reversal %s %d", key, amount)
	}
	if total, _ := e.svc.CashbackEarned(ctx, u.ID); total != 0 {
		t.Fatalf("earned %d after the refund", total)
	}
	// The refund message names the cashback taken and the balance after both.
	evs := outboxEvents(t, e.s, events.WalletCredited)
	var ev events.WalletCreditedEvent
	if err := json.Unmarshal(evs[len(evs)-1], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Reason != events.CreditRefund || ev.Amount != 10_000 || ev.CashbackTaken != 250 || ev.Balance != 10_000 {
		t.Fatalf("refund event %+v", ev)
	}
	// The audit log says what was taken and that nothing was short.
	var after []byte
	if err := e.s.DB().QueryRow(ctx, `SELECT after FROM core.audit_log WHERE action = 'order.cashback_reversal' AND entity_id = $1`, o.ID).Scan(&after); err != nil {
		t.Fatalf("no audit entry: %v", err)
	}
	var detail map[string]any
	if json.Unmarshal(after, &detail) != nil || detail["credited"] != float64(250) || detail["taken"] != float64(250) || detail["shortfall"] != float64(0) {
		t.Fatalf("audit %s", after)
	}
	// A second refund is refused and takes nothing more.
	if _, err := e.svc.RefundOrder(ctx, e.owner, e.fp, o.ID, "again"); !errors.Is(err, ErrState) {
		t.Fatalf("a second refund: %v", err)
	}
	if n := e.entries("cashback_reversal"); n != 1 || e.balance(u) != 10_000 {
		t.Fatalf("%d reversals, balance %d", n, e.balance(u))
	}
}

// The customer may have spent the cashback by then: the refund fills the wallet first, so it is still taken from the refund and the balance is
// never pushed below zero.
func TestRefundAfterTheCashbackWasSpent(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9691)
	e.fundNow(u, 10_000)
	o := e.buy(u)
	// Spend the 250 on a package of 250 Toman.
	small := *e.plan
	small.ID, small.Price = "", 250
	if err := e.s.UpsertPlan(ctx, e.s.Conn(), &small); err != nil {
		t.Fatal(err)
	}
	e.set(map[string]string{SettingCashbackEnabled: "false"})
	e.pay(u, CreateOrderParams{PlanID: small.ID})
	if e.balance(u) != 0 {
		t.Fatalf("balance %d", e.balance(u))
	}
	if _, err := e.svc.RefundOrder(ctx, e.owner, e.fp, o.ID, "asked for it back"); err != nil {
		t.Fatal(err)
	}
	if got := e.balance(u); got != 9_750 {
		t.Fatalf("balance %d, want 10,000 refunded less the 250 cashback", got)
	}
	e.consistent(u)
}

// When the wallet cannot cover the cashback (it holds less, or the customer owes), only what it holds is taken, the balance does not go below
// zero, and the shortfall is in the audit log.
func TestCashbackReversalNeverGoesBelowZero(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9701)
	e.fundNow(u, 10_000)
	o := e.buy(u)
	// Empty the wallet but 100 Toman, as if the refund had not filled it.
	err := e.s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := e.s.Debit(ctx, tx, u.ID, "IRT", e.balance(u)-100, &store.LedgerEntry{Kind: "adjust", IdempotencyKey: "drain-" + e.key()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var taken int64
	var after *store.Wallet
	err = e.s.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		taken, after, err = e.svc.reverseCashback(ctx, tx, e.owner, o)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if taken != 100 || after == nil || after.Balance != 0 || e.balance(u) != 0 {
		t.Fatalf("taken %d, balance %d", taken, e.balance(u))
	}
	c, _ := e.svc.OrderCashback(ctx, o.ID)
	if c == nil || c.ReversedAmount == nil || *c.ReversedAmount != 100 || c.ReversalEntryID == nil {
		t.Fatalf("record %+v", c)
	}
	var detail []byte
	if err := e.s.DB().QueryRow(ctx, `SELECT after FROM core.audit_log WHERE action = 'order.cashback_reversal'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if json.Unmarshal(detail, &m) != nil || m["taken"] != float64(100) || m["shortfall"] != float64(150) {
		t.Fatalf("audit %s", detail)
	}
	e.consistent(u)

	// Taken again: nothing (the credit is reversed once).
	err = e.s.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		taken, after, err = e.svc.reverseCashback(ctx, tx, e.owner, o)
		return err
	})
	if err != nil || taken != 0 || after != nil {
		t.Fatalf("reversed twice: %d %v %v", taken, after, err)
	}
	if n := e.entries("cashback_reversal"); n != 1 {
		t.Fatalf("%d reversals", n)
	}
}

// With nothing in the wallet at all, the reversal writes no entry, the record says 0 was taken, and the audit log has the whole amount as short.
func TestCashbackReversalFromAnEmptyWallet(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9711)
	e.fundNow(u, 10_000)
	o := e.buy(u)
	err := e.s.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := e.s.Debit(ctx, tx, u.ID, "IRT", e.balance(u), &store.LedgerEntry{Kind: "adjust", IdempotencyKey: "drain-" + e.key()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var taken int64
	err = e.s.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		taken, _, err = e.svc.reverseCashback(ctx, tx, e.owner, o)
		return err
	})
	if err != nil || taken != 0 {
		t.Fatalf("taken %d %v", taken, err)
	}
	c, _ := e.svc.OrderCashback(ctx, o.ID)
	if c == nil || c.ReversedAmount == nil || *c.ReversedAmount != 0 || c.ReversalEntryID != nil || c.ReversedAt == nil {
		t.Fatalf("record %+v", c)
	}
	if n := e.entries("cashback_reversal"); n != 0 {
		t.Fatalf("%d reversal entries", n)
	}
	var detail []byte
	if err := e.s.DB().QueryRow(ctx, `SELECT after FROM core.audit_log WHERE action = 'order.cashback_reversal'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if json.Unmarshal(detail, &m) != nil || m["taken"] != float64(0) || m["shortfall"] != float64(250) {
		t.Fatalf("audit %s", detail)
	}
	e.consistent(u)
}

// A refund of an order that earned nothing leaves no trace of a reversal, and the refund event carries no cashback.
func TestRefundWithoutCashbackIsAsBefore(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	e.set(map[string]string{SettingCashbackEnabled: "false"})
	u := seedUser(t, e.s, 9721)
	e.fundNow(u, 10_000)
	o := e.buy(u)
	if _, err := e.svc.RefundOrder(ctx, e.owner, e.fp, o.ID, "x"); err != nil {
		t.Fatal(err)
	}
	if n := e.entries("cashback_reversal"); n != 0 {
		t.Fatalf("%d reversals", n)
	}
	var n int
	if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.audit_log WHERE action = 'order.cashback_reversal'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("audit entries %d %v", n, err)
	}
	evs := outboxEvents(t, e.s, events.WalletCredited)
	var ev events.WalletCreditedEvent
	if err := json.Unmarshal(evs[len(evs)-1], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.CashbackTaken != 0 || ev.Balance != 10_000 {
		t.Fatalf("event %+v", ev)
	}
}

// A refund and a replayed delivery racing: the cashback is taken once and the books stay whole.
func TestRefundRacesAReplayedDelivery(t *testing.T) {
	e := newCashbackEnv(t)
	needConns(t, e.s, 8)
	ctx := context.Background()
	u := seedUser(t, e.s, 9731)
	e.fundNow(u, 10_000)
	o := e.buy(u)
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				_, _ = e.svc.RefundOrder(ctx, e.owner, e.fp, o.ID, "race")
				return
			}
			_ = e.s.WithTx(ctx, func(tx pgx.Tx) error { return e.svc.afterDelivery(ctx, tx, o, e.plan, "") })
		}()
	}
	wg.Wait()
	if n := e.entries("cashback_reversal"); n > 1 {
		t.Fatalf("%d reversals", n)
	}
	e.consistent(u)
	if got := e.balance(u); got < 0 {
		t.Fatalf("balance %d", got)
	}
}

// The services of a bulk purchase are not told one by one: each earns, and the bulk's one message carries the sum.
func TestCashbackOfABulkPurchaseIsToldOnce(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9741)
	e.fundNow(u, 30_000)
	v, err := e.svc.CreateBulk(ctx, BulkParams{UserID: u.ID, PlanID: e.plan.ID, Quantity: 3, IdempotencyKey: e.key()})
	if err != nil {
		t.Fatal(err)
	}
	w := e.svc.NewProvisionWorker(e.fp, nil)
	for range 3 {
		if worked, err := w.ProvisionNext(ctx); err != nil || !worked {
			t.Fatalf("provisioning: %v %v", worked, err)
		}
	}
	if n, err := w.SettleBulks(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	if n := e.entries("cashback"); n != 3 || e.balance(u) != 750 {
		t.Fatalf("%d credits, balance %d", n, e.balance(u))
	}
	// No message per service; one bulk message with the 750.
	if got := outboxEvents(t, e.s, events.WalletCredited); len(got) != 0 {
		t.Fatalf("%d wallet messages for a bulk", len(got))
	}
	ready := bulkReadyEvents(t, e.s)
	if len(ready) != 1 || ready[0].BulkID != v.ID || ready[0].Cashback != 750 {
		t.Fatalf("bulk announcements %+v", ready)
	}
	e.consistent(u)
}

// The ledger takes the kinds of this feature, and its data is in the books like any other: the wallet equals the ledger.
func TestCashbackKindsAreInTheLedgerRule(t *testing.T) {
	e := newCashbackEnv(t)
	ctx := context.Background()
	u := seedUser(t, e.s, 9751)
	for _, kind := range []string{"cashback", "cashback_reversal", "target_bonus", "wheel_prize"} {
		_, err := e.s.DB().Exec(ctx, `INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after)
			VALUES (gen_random_uuid(), $1, 'IRT', 0, $2, $3, 0)`, u.ID, kind, "kind-"+kind)
		if err != nil {
			t.Errorf("the ledger refuses %s: %v", kind, err)
		}
	}
	if _, err := e.s.DB().Exec(ctx, `INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after)
		VALUES (gen_random_uuid(), $1, 'IRT', 0, 'made_up', 'kind-x', 0)`, u.ID); err == nil {
		t.Error("the ledger took a made-up kind")
	}
}
