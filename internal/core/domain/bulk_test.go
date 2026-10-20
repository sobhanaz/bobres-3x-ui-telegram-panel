package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// bulkFixture is a store with one plan of the given price and a buyer whose
// wallet holds the given balance.
type bulkFixture struct {
	t     *testing.T
	svc   *Service
	s     *store.Store
	plan  *store.Plan
	buyer *store.User
	keys  int
}

func newBulkFixture(t *testing.T, price, balance int64) *bulkFixture {
	t.Helper()
	svc, s := testService(t)
	f := &bulkFixture{t: t, svc: svc, s: s, plan: seedPlan(t, s, false, price), buyer: seedUser(t, s, 7001)}
	if balance > 0 {
		fund(t, s, f.buyer.ID, balance)
	}
	return f
}

// key is a request key no other call of this test used.
func (f *bulkFixture) key() string {
	f.keys++
	return fmt.Sprintf("bulk-test-%d", f.keys)
}

func (f *bulkFixture) params(quantity int) BulkParams {
	return BulkParams{UserID: f.buyer.ID, PlanID: f.plan.ID, Quantity: quantity, IdempotencyKey: f.key()}
}

func (f *bulkFixture) count(table, where string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.s.DB().QueryRow(context.Background(), "SELECT count(*) FROM core."+table+" WHERE "+where, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *bulkFixture) attempts(orderID string) int {
	f.t.Helper()
	var n int
	if err := f.s.DB().QueryRow(context.Background(), `SELECT provision_attempts FROM core.orders WHERE id = $1`, orderID).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// nothingWritten checks a refused purchase left no trace: no bulk, no order,
// no ledger entry beyond the funding, no announcement, no audit entry, and
// the wallet as it was.
func (f *bulkFixture) nothingWritten(what string, balance int64) {
	f.t.Helper()
	ctx := context.Background()
	if n := f.count("bulk_orders", "true"); n != 0 {
		f.t.Errorf("%s: %d bulk rows", what, n)
	}
	if n := f.count("orders", "true"); n != 0 {
		f.t.Errorf("%s: %d orders", what, n)
	}
	if n := f.count("ledger_entries", "kind = 'purchase'"); n != 0 {
		f.t.Errorf("%s: %d purchases in the ledger", what, n)
	}
	if n := len(outboxEvents(f.t, f.s, events.OrderPaid)); n != 0 {
		f.t.Errorf("%s: %d order.paid events", what, n)
	}
	if n := f.count("audit_log", "action = 'order.bulk'"); n != 0 {
		f.t.Errorf("%s: %d audit entries", what, n)
	}
	w, err := f.s.GetWallet(ctx, f.s.Conn(), f.buyer.ID, "IRT")
	if err != nil {
		f.t.Fatal(err)
	}
	if bal, sum, _ := ledger(f.t, f.s, f.buyer.ID); w.Balance != balance || bal != balance || sum != balance {
		f.t.Errorf("%s: balance %d, ledger %d, want %d", what, bal, sum, balance)
	}
}

func orderPaidEvents(t *testing.T, s *store.Store) []events.OrderPaidEvent {
	t.Helper()
	var out []events.OrderPaidEvent
	for _, raw := range outboxEvents(t, s, events.OrderPaid) {
		var e events.OrderPaidEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func bulkReadyEvents(t *testing.T, s *store.Store) []events.BulkReadyEvent {
	t.Helper()
	var out []events.BulkReadyEvent
	for _, raw := range outboxEvents(t, s, events.BulkReady) {
		var e events.BulkReadyEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func provisionedEvents(t *testing.T, s *store.Store) []events.SubscriptionProvisionedEvent {
	t.Helper()
	var out []events.SubscriptionProvisionedEvent
	for _, raw := range outboxEvents(t, s, events.SubscriptionProvisioned) {
		var e events.SubscriptionProvisionedEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// injectFailure makes Postgres refuse every insert into core.<table> that
// matches the condition (written over NEW): a way to break a transaction in
// the middle. The trigger is removed when the test ends, or by the returned
// function.
func injectFailure(t *testing.T, s *store.Store, table, condition string) (remove func()) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION core.bulk_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$`,
		`DROP TRIGGER IF EXISTS bulk_test_fail ON core.` + table,
		`CREATE TRIGGER bulk_test_fail BEFORE INSERT ON core.` + table + ` FOR EACH ROW WHEN (` + condition + `) EXECUTE FUNCTION core.bulk_test_fail()`,
	} {
		if _, err := s.DB().Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	remove = func() {
		if _, err := s.DB().Exec(context.Background(), `DROP TRIGGER IF EXISTS bulk_test_fail ON core.`+table); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(remove)
	return remove
}

// A bulk purchase makes one ordinary paid order per service, each paid by its
// own wallet debit, named "<prefix> <n>", priced as a single purchase, and
// announced as paid with its place in the bulk; the bulk itself is audited
// once with its quantity; the worker then delivers every order.
func TestBulkPurchaseMakesOnePaidOrderPerService(t *testing.T) {
	f := newBulkFixture(t, 1000, 5000)
	ctx := context.Background()
	p := f.params(3)
	p.LabelPrefix = "  Family  "
	v, err := f.svc.CreateBulk(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if v.Replayed || v.Quantity != 3 || v.UnitAmount != 1000 || v.Amount != 3000 || v.Currency != "IRT" || v.UserID != f.buyer.ID || v.PlanID != f.plan.ID ||
		v.LabelPrefix == nil || *v.LabelPrefix != "Family" || v.DeliveredAt != nil || v.PlanName["en"] != "P" || len(v.Services) != 3 {
		t.Fatalf("view: %+v", v)
	}
	var orderIDs []string
	for i, sv := range v.Services {
		want := fmt.Sprintf("Family %d", i+1)
		if sv.BulkIndex == nil || int(*sv.BulkIndex) != i+1 || sv.Order.Status != "paid" || sv.Order.Amount != 1000 || sv.Order.Type != "new" ||
			sv.Order.Label == nil || *sv.Order.Label != want || sv.Order.IdempotencyKey != fmt.Sprintf("%s#%d", p.IdempotencyKey, i+1) ||
			sv.Order.BulkID == nil || *sv.Order.BulkID != v.ID || sv.SubscriptionID != "" {
			t.Fatalf("service %d: %+v", i+1, sv)
		}
		orderIDs = append(orderIDs, sv.Order.ID)
	}
	// One debit per order, kept under the order's own key, and a balance that
	// is the sum of the ledger.
	if bal, sum, rows := ledger(t, f.s, f.buyer.ID); bal != 2000 || sum != 2000 || rows != 4 {
		t.Fatalf("balance %d, ledger sum %d, %d entries", bal, sum, rows)
	}
	for _, id := range orderIDs {
		e, err := f.s.LedgerByKey(ctx, f.s.Conn(), "purchase:"+id)
		if err != nil || e.Amount != -1000 || e.Kind != "purchase" || e.RefID == nil || *e.RefID != id {
			t.Fatalf("ledger of order %s: %v %+v", id, err, e)
		}
	}
	// Announced as paid by the wallet, each with its place in the bulk.
	evs := orderPaidEvents(t, f.s)
	if len(evs) != 3 {
		t.Fatalf("%d order.paid events", len(evs))
	}
	for i, e := range evs {
		if e.Source != events.PaidByWallet || e.Amount != 1000 || e.BulkID != v.ID || e.BulkIndex != i+1 || e.BulkQuantity != 3 || e.TelegramID != f.buyer.TelegramID {
			t.Fatalf("event %d: %+v", i+1, e)
		}
	}
	// Audited once, as the buyer's, with the quantity.
	var after string
	if err := f.s.DB().QueryRow(ctx, `SELECT after::text FROM core.audit_log WHERE action = 'order.bulk' AND entity = 'bulk_order' AND entity_id = $1 AND actor_id = $2`,
		v.ID, f.buyer.ID).Scan(&after); err != nil {
		t.Fatalf("audit entry: %v", err)
	}
	for _, want := range []string{`"quantity": 3`, `"amount": 3000`, `"unit_amount": 1000`, `"label_prefix": "Family"`} {
		if !strings.Contains(after, want) {
			t.Errorf("audit %s lacks %s", after, want)
		}
	}
	// The worker delivers them all like any other orders.
	fp := &fakeProvisioner{}
	w := f.svc.NewProvisionWorker(fp, nil)
	for range 3 {
		if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
			t.Fatalf("provision: %v %v", worked, err)
		}
	}
	for i, id := range orderIDs {
		sub, err := f.s.SubscriptionByOrder(ctx, f.s.Conn(), id)
		if err != nil || sub.UserID != f.buyer.ID || sub.Status != "active" || sub.Label == nil || *sub.Label != fmt.Sprintf("Family %d", i+1) {
			t.Fatalf("service of order %d: %v %+v", i+1, err, sub)
		}
	}
	// A bulk without a name makes unnamed services.
	f2 := f.params(2)
	fund(t, f.s, f.buyer.ID, 2000)
	if v2, err := f.svc.CreateBulk(ctx, f2); err != nil || v2.LabelPrefix != nil || v2.Services[0].Order.Label != nil {
		t.Fatalf("unnamed bulk: %v %+v", err, v2)
	}
}

// How many services a bulk purchase may hold: at least two, at most the
// owner's limit (20 until it is set, never above 100), and nothing at all
// while the owner has it switched off.
func TestBulkQuantityBoundsAndSwitch(t *testing.T) {
	f := newBulkFixture(t, 100, 1_000_000)
	ctx := context.Background()
	own := owner(t, f.svc)
	try := func(q int) error {
		_, err := f.svc.CreateBulk(ctx, f.params(q))
		return err
	}
	for _, q := range []int{-1, 0, 1, 21, 101, 1 << 30} {
		if err := try(q); !errors.Is(err, ErrInvalid) {
			t.Errorf("quantity %d: %v", q, err)
		}
	}
	if n := f.count("orders", "true"); n != 0 {
		t.Fatalf("%d orders after refused quantities", n)
	}
	if err := try(2); err != nil {
		t.Fatalf("the least: %v", err)
	}
	if err := try(20); err != nil {
		t.Fatalf("the default most: %v", err)
	}
	// The owner's limit applies to everything above it, and the quote says what it is.
	if err := f.svc.SetSetting(ctx, own, SettingBulkMax, "5"); err != nil {
		t.Fatal(err)
	}
	if err := try(6); !errors.Is(err, ErrInvalid) {
		t.Errorf("above the owner's limit: %v", err)
	}
	if err := try(5); err != nil {
		t.Errorf("at the owner's limit: %v", err)
	}
	if q, err := f.svc.QuoteBulk(ctx, BulkQuoteParams{UserID: f.buyer.ID, PlanID: f.plan.ID, Quantity: 5}); err != nil || q.MaxQuantity != 5 {
		t.Errorf("quote: %v %+v", err, q)
	}
	if err := f.svc.SetSetting(ctx, own, SettingBulkMax, "100"); err != nil {
		t.Fatal(err)
	}
	if err := try(100); err != nil {
		t.Errorf("a hundred: %v", err)
	}
	// A value written past what the setting takes (by hand) is held to 100.
	raw, _ := json.Marshal("5000")
	if err := f.s.SetSetting(ctx, f.s.Conn(), SettingBulkMax, raw); err != nil {
		t.Fatal(err)
	}
	if enabled, max := f.svc.BulkLimits(ctx); !enabled || max != MaxBulkQuantity {
		t.Errorf("limits with 5000 stored: %v %d", enabled, max)
	}
	// Switched off: refused (the quote too), however small; the setting is
	// checked like any other.
	if err := f.svc.SetSetting(ctx, own, SettingBulkEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	before := f.count("orders", "true")
	if err := try(2); !errors.Is(err, ErrState) || !strings.Contains(err.Error(), "turned off") {
		t.Errorf("switched off: %v", err)
	}
	if _, err := f.svc.QuoteBulk(ctx, BulkQuoteParams{UserID: f.buyer.ID, PlanID: f.plan.ID, Quantity: 2}); !errors.Is(err, ErrState) {
		t.Errorf("a quote while it is off: %v", err)
	}
	if after := f.count("orders", "true"); after != before {
		t.Errorf("a refused purchase made %d orders", after-before)
	}
	for _, bad := range []string{"1", "101", "many"} {
		if err := f.svc.SetSetting(ctx, own, SettingBulkMax, bad); err == nil {
			t.Errorf("bulk.max_quantity = %q was accepted", bad)
		}
	}
}

// The price of a bulk purchase is the unit price of a normal purchase times the
// quantity: the store price for a customer, the tier's price (its discount, or
// a fixed price for the plan) for a reseller. It is refused for a plan that is
// free, off, a trial or a traffic package, and when the buyer was shown another
// total.
func TestBulkPrice(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9300, "admin")
	plan := seedPlan(t, s, false, 1000)
	fixed := seedPlan(t, s, false, 900)
	tier := seedTier(t, svc, admin, "Gold", 20, 10, map[string]int64{fixed.ID: 500})
	res := reseller(t, svc, s, admin, 9301, tier, 0)
	cust := seedUser(t, s, 9302)
	fund(t, s, res.ID, 100_000)
	fund(t, s, cust.ID, 100_000)

	quote := func(u *store.User, p *store.Plan, q int) (*BulkQuote, error) {
		return svc.QuoteBulk(ctx, BulkQuoteParams{UserID: u.ID, PlanID: p.ID, Quantity: q})
	}
	for _, c := range []struct {
		who   string
		u     *store.User
		plan  *store.Plan
		q     int
		unit  int64
		total int64
	}{
		{"a customer", cust, plan, 4, 1000, 4000},
		{"a reseller, by the tier's discount", res, plan, 4, 800, 3200},
		{"a reseller, by a fixed price", res, fixed, 3, 500, 1500},
		{"a customer, a plan the tier prices", cust, fixed, 3, 900, 2700},
	} {
		q, err := quote(c.u, c.plan, c.q)
		if err != nil || q.UnitAmount != c.unit || q.Amount != c.total || q.Currency != "IRT" || q.Quantity != c.q || q.MaxQuantity != DefaultBulkMax {
			t.Fatalf("%s: quote %+v %v", c.who, q, err)
		}
		before, _, _ := ledger(t, s, c.u.ID)
		v, err := svc.CreateBulk(ctx, BulkParams{UserID: c.u.ID, PlanID: c.plan.ID, Quantity: c.q, IdempotencyKey: "price-" + c.who, ExpectedAmount: c.total, ExpectedCurrency: "IRT"})
		if err != nil {
			t.Fatalf("%s: %v", c.who, err)
		}
		if v.Amount != c.total {
			t.Errorf("%s: charged %d, want %d", c.who, v.Amount, c.total)
		}
		for _, sv := range v.Services {
			if sv.Order.Amount != c.unit {
				t.Errorf("%s: an order of %d, want %d (as a single purchase)", c.who, sv.Order.Amount, c.unit)
			}
		}
		if after, _, _ := ledger(t, s, c.u.ID); before-after != c.total {
			t.Errorf("%s: the wallet went down by %d, want %d", c.who, before-after, c.total)
		}
		// The same price as the single purchase the bot already makes.
		single, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: c.u.ID, PlanID: c.plan.ID, IdempotencyKey: "single-" + c.who})
		if err != nil || single.Amount != c.unit {
			t.Errorf("%s: a single purchase costs %d (%v), a bulk's unit %d", c.who, single.Amount, err, c.unit)
		}
	}

	// A total other than the one shown is refused, whole.
	balance, _, _ := ledger(t, s, cust.ID)
	for name, p := range map[string]BulkParams{
		"a lower total":    {UserID: cust.ID, PlanID: plan.ID, Quantity: 2, IdempotencyKey: "shown-1", ExpectedAmount: 1999},
		"a higher total":   {UserID: cust.ID, PlanID: plan.ID, Quantity: 2, IdempotencyKey: "shown-2", ExpectedAmount: 2001},
		"another currency": {UserID: cust.ID, PlanID: plan.ID, Quantity: 2, IdempotencyKey: "shown-3", ExpectedAmount: 2000, ExpectedCurrency: "USDT"},
	} {
		if _, err := svc.CreateBulk(ctx, p); !errors.Is(err, ErrState) || !strings.Contains(err.Error(), "price changed") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if after, _, _ := ledger(t, s, cust.ID); after != balance {
		t.Errorf("a refused price moved money: %d -> %d", balance, after)
	}
	if _, err := s.BulkByKey(ctx, s.Conn(), "shown-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused purchase left a bulk behind: %v", err)
	}

	// Plans a bulk cannot be made of.
	free := seedPlan(t, s, false, 0)
	trial := seedPlan(t, s, true, 100)
	off := seedPlan(t, s, false, 100)
	off.Enabled = false
	if err := s.UpsertPlan(ctx, s.Conn(), off); err != nil {
		t.Fatal(err)
	}
	topup := &store.Plan{NameI18n: map[string]string{"en": "Top"}, Kind: "traffic", Price: 100, Currency: "IRT", Enabled: true, IsTopup: true}
	if err := s.UpsertPlan(ctx, s.Conn(), topup); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]*store.Plan{"a free plan": free, "a trial": trial, "a plan that is off": off, "a traffic package": topup} {
		if _, err := quote(cust, p, 2); !errors.Is(err, ErrPlanUnavailable) {
			t.Errorf("%s: quote: %v", name, err)
		}
		if _, err := svc.CreateBulk(ctx, BulkParams{UserID: cust.ID, PlanID: p.ID, Quantity: 2, IdempotencyKey: "plan-" + name}); !errors.Is(err, ErrPlanUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := quote(cust, &store.Plan{ID: "00000000-0000-7000-8000-00000000dead"}, 2); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown plan: %v", err)
	}

	// Too large a total cannot overflow the arithmetic.
	huge := seedPlan(t, s, false, 1<<62)
	if _, err := quote(cust, huge, 3); !errors.Is(err, ErrInvalid) {
		t.Errorf("an overflowing total: %v", err)
	}
}

// A banned buyer buys nothing, an unknown one is not found, and a key is
// required.
func TestBulkRefusesWhoMayNotBuy(t *testing.T) {
	f := newBulkFixture(t, 100, 10_000)
	ctx := context.Background()
	if err := f.s.SetUserRole(ctx, f.s.Conn(), f.buyer.ID, "user", "banned"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateBulk(ctx, f.params(2)); !errors.Is(err, ErrUserBanned) {
		t.Errorf("a banned buyer: %v", err)
	}
	if _, err := f.svc.QuoteBulk(ctx, BulkQuoteParams{UserID: f.buyer.ID, PlanID: f.plan.ID, Quantity: 2}); !errors.Is(err, ErrUserBanned) {
		t.Errorf("a quote for a banned buyer: %v", err)
	}
	if err := f.s.SetUserRole(ctx, f.s.Conn(), f.buyer.ID, "user", "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateBulk(ctx, BulkParams{UserID: "00000000-0000-7000-8000-00000000dead", PlanID: f.plan.ID, Quantity: 2, IdempotencyKey: "x"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown buyer: %v", err)
	}
	for _, key := range []string{"", strings.Repeat("k", maxBulkKey+1)} {
		p := f.params(2)
		p.IdempotencyKey = key
		if _, err := f.svc.CreateBulk(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("key of %d characters: %v", len(key), err)
		}
	}
	f.nothingWritten("refusals", 10_000)
}

// The name of the services is one line of plain text, short enough for
// "<name> <n>" to be a service name for the biggest n.
func TestBulkNames(t *testing.T) {
	f := newBulkFixture(t, 10, 1_000_000)
	ctx := context.Background()
	if err := f.svc.SetSetting(ctx, owner(t, f.svc), SettingBulkMax, "100"); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("ب", 64)
	for _, c := range []struct {
		prefix string
		q      int
		ok     bool
	}{
		{"Ali's phone", 3, true},
		{"سرویس خانواده", 12, true},
		{strings.Repeat("a", 62), 9, true}, // "<62> 9" is 64 characters
		{strings.Repeat("a", 63), 9, false},
		{strings.Repeat("a", 61), 10, true},
		{strings.Repeat("a", 62), 10, false},
		{strings.Repeat("a", 60), 100, true},
		{strings.Repeat("a", 61), 100, false},
		{long, 2, false},
		{"two\nlines", 2, false},
		{"tab\there", 2, false},
		{"   ", 2, true}, // blank: no name
	} {
		p := f.params(c.q)
		p.LabelPrefix = c.prefix
		v, err := f.svc.CreateBulk(ctx, p)
		switch {
		case c.ok && err != nil:
			t.Errorf("%q x%d: %v", c.prefix, c.q, err)
		case !c.ok && !errors.Is(err, ErrInvalid):
			t.Errorf("%q x%d: %v", c.prefix, c.q, err)
		case c.ok && strings.TrimSpace(c.prefix) != "":
			last := v.Services[c.q-1].Order.Label
			if last == nil || *last != strings.TrimSpace(c.prefix)+fmt.Sprintf(" %d", c.q) {
				t.Errorf("%q x%d: the last service is %v", c.prefix, c.q, last)
			}
		case c.ok && v.LabelPrefix != nil:
			t.Errorf("a blank name was kept: %q", *v.LabelPrefix)
		}
	}
}

// All or nothing: whatever breaks half-way through the purchase (an order
// that cannot be written, a debit, the announcement), nothing is written and
// the wallet is as it was, and the same request, made again once the fault is
// gone, goes through exactly once.
func TestBulkIsAllOrNothingUnderInjectedFailure(t *testing.T) {
	for _, c := range []struct {
		name, table, when string
	}{
		{"the third order cannot be written", "orders", "NEW.bulk_index = 3"},
		{"the last order cannot be written", "orders", "NEW.bulk_index = 4"},
		{"the third debit cannot be written", "ledger_entries", "NEW.kind = 'purchase' AND NEW.balance_after = 7000"},
		{"the third announcement cannot be written", "outbox_core", "NEW.topic = 'order.paid.v1' AND (NEW.payload->>'bulk_index')::int = 3"},
		{"the audit entry cannot be written", "audit_log", "NEW.action = 'order.bulk'"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newBulkFixture(t, 1000, 10_000)
			ctx := context.Background()
			remove := injectFailure(t, f.s, c.table, c.when)
			p := f.params(4)
			p.LabelPrefix = "Shop"
			if _, err := f.svc.CreateBulk(ctx, p); err == nil || !strings.Contains(err.Error(), "injected failure") {
				t.Fatalf("the injected failure was not hit: %v", err)
			}
			f.nothingWritten(c.name, 10_000)
			if n := f.count("subscriptions", "true"); n != 0 {
				t.Errorf("%d subscriptions", n)
			}
			// The fault gone, the same request is made once, as if nothing happened.
			remove()
			v, err := f.svc.CreateBulk(ctx, p)
			if err != nil || v.Replayed || len(v.Services) != 4 {
				t.Fatalf("again: %v %+v", err, v)
			}
			if bal, sum, rows := ledger(t, f.s, f.buyer.ID); bal != 6000 || sum != 6000 || rows != 5 {
				t.Errorf("after the retry: balance %d, ledger %d, %d entries", bal, sum, rows)
			}
			if n := f.count("orders", "true"); n != 4 {
				t.Errorf("%d orders after the retry", n)
			}
		})
	}
}

// A wallet that cannot pay for all of it pays for none of it: nothing is
// written, whatever the balance came short by, and a reseller's credit limit
// counts as balance.
func TestBulkNeedsTheWholeTotal(t *testing.T) {
	ctx := context.Background()
	t.Run("a customer", func(t *testing.T) {
		f := newBulkFixture(t, 1000, 3999)
		if _, err := f.svc.CreateBulk(ctx, f.params(4)); !errors.Is(err, store.ErrInsufficientFunds) {
			t.Fatalf("1 short: %v", err)
		}
		f.nothingWritten("a wallet 1 short", 3999)
		fund(t, f.s, f.buyer.ID, 1)
		if v, err := f.svc.CreateBulk(ctx, f.params(4)); err != nil || v.Amount != 4000 {
			t.Fatalf("exactly enough: %v", err)
		}
		if bal, _, _ := ledger(t, f.s, f.buyer.ID); bal != 0 {
			t.Fatalf("balance after paying all of it: %d", bal)
		}
		if _, err := f.svc.CreateBulk(ctx, f.params(2)); !errors.Is(err, store.ErrInsufficientFunds) {
			t.Fatalf("an empty wallet: %v", err)
		}
		if n := f.count("orders", "true"); n != 4 {
			t.Fatalf("%d orders", n)
		}
	})
	t.Run("a reseller on credit", func(t *testing.T) {
		svc, s := testService(t)
		admin := staffMember(t, s, 9300, "admin")
		plan := seedPlan(t, s, false, 1000)
		tier := seedTier(t, svc, admin, "Gold", 20, 10, nil) // 800 a service
		r := reseller(t, svc, s, admin, 9301, tier, 3000)
		fund(t, s, r.ID, 500) // 3500 to spend
		// Five cost 4000: past the limit, so none is made; four cost 3200.
		_, err := svc.CreateBulk(ctx, BulkParams{UserID: r.ID, PlanID: plan.ID, Quantity: 5, IdempotencyKey: "credit-5"})
		if !errors.Is(err, store.ErrInsufficientFunds) {
			t.Fatalf("past the credit limit: %v", err)
		}
		if n, _ := ledgerKinds(t, s, r.ID, "purchase"); n != 0 {
			t.Fatalf("%d purchases", n)
		}
		v, err := svc.CreateBulk(ctx, BulkParams{UserID: r.ID, PlanID: plan.ID, Quantity: 4, IdempotencyKey: "credit-4", LabelPrefix: "Ali"})
		if err != nil || v.Amount != 3200 {
			t.Fatalf("within the credit: %v", err)
		}
		if bal, sum, _ := ledger(t, s, r.ID); bal != -2700 || sum != -2700 {
			t.Fatalf("the reseller's balance %d, ledger %d: they owe 2700", bal, sum)
		}
		if v.Services[0].Order.Label == nil || *v.Services[0].Order.Label != "Ali 1" {
			t.Fatalf("a reseller's names: %+v", v.Services[0].Order)
		}
		// Past the limit by one: the next purchase, however small, is refused.
		if _, err := svc.CreateBulk(ctx, BulkParams{UserID: r.ID, PlanID: plan.ID, Quantity: 2, IdempotencyKey: "credit-2"}); !errors.Is(err, store.ErrInsufficientFunds) {
			t.Fatalf("a purchase at 2700 of 3000: %v", err)
		}
		// A reseller who buys pays nobody a commission.
		if n, _ := ledgerKinds(t, s, r.ID, "commission"); n != 0 {
			t.Fatalf("commissions %d", n)
		}
	})
}

// A request made twice (a double tap, a retry after a lost answer) makes one
// purchase: the second gets the first's result and moves no money. The same
// key for another purchase is a conflict, not someone else's bulk.
func TestBulkIsIdempotent(t *testing.T) {
	f := newBulkFixture(t, 1000, 10_000)
	ctx := context.Background()
	own := owner(t, f.svc)
	p := f.params(3)
	p.LabelPrefix = "Kids"
	first, err := f.svc.CreateBulk(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.svc.CreateBulk(ctx, p)
	if err != nil || !again.Replayed || again.ID != first.ID || len(again.Services) != 3 {
		t.Fatalf("again: %v %+v", err, again)
	}
	for i := range again.Services {
		if again.Services[i].Order.ID != first.Services[i].Order.ID {
			t.Fatalf("service %d is another order", i+1)
		}
	}
	if bal, _, rows := ledger(t, f.s, f.buyer.ID); bal != 7000 || rows != 4 {
		t.Fatalf("balance %d with %d entries after a repeat", bal, rows)
	}
	if n := f.count("orders", "true"); n != 3 || len(orderPaidEvents(t, f.s)) != 3 || f.count("audit_log", "action = 'order.bulk'") != 1 {
		t.Fatalf("a repeat made more: %d orders", n)
	}
	// It is the same purchase whatever has changed since: the plan switched
	// off, the price, bulk purchase itself (what was bought stays bought).
	if err := f.svc.SetSetting(ctx, own, SettingBulkEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	f.plan.Enabled, f.plan.Price = false, 5
	if err := f.s.UpsertPlan(ctx, f.s.Conn(), f.plan); err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.CreateBulk(ctx, p); err != nil || !got.Replayed || got.ID != first.ID || got.Amount != 3000 {
		t.Fatalf("a repeat after the change: %v %+v", err, got)
	}
	f.plan.Enabled, f.plan.Price = true, 1000
	if err := f.s.UpsertPlan(ctx, f.s.Conn(), f.plan); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetSetting(ctx, own, SettingBulkEnabled, "true"); err != nil {
		t.Fatal(err)
	}

	// The key is the request's: another quantity, plan, name or buyer under it is a conflict.
	other := seedPlan(t, f.s, false, 1000)
	stranger := seedUser(t, f.s, 7002)
	fund(t, f.s, stranger.ID, 10_000)
	for name, q := range map[string]BulkParams{
		"another quantity": {UserID: f.buyer.ID, PlanID: f.plan.ID, Quantity: 4, IdempotencyKey: p.IdempotencyKey, LabelPrefix: "Kids"},
		"another plan":     {UserID: f.buyer.ID, PlanID: other.ID, Quantity: 3, IdempotencyKey: p.IdempotencyKey, LabelPrefix: "Kids"},
		"another name":     {UserID: f.buyer.ID, PlanID: f.plan.ID, Quantity: 3, IdempotencyKey: p.IdempotencyKey, LabelPrefix: "Adults"},
		"no name":          {UserID: f.buyer.ID, PlanID: f.plan.ID, Quantity: 3, IdempotencyKey: p.IdempotencyKey},
		"another buyer":    {UserID: stranger.ID, PlanID: f.plan.ID, Quantity: 3, IdempotencyKey: p.IdempotencyKey, LabelPrefix: "Kids"},
	} {
		got, err := f.svc.CreateBulk(ctx, q)
		if !errors.Is(err, store.ErrIdempotencyConflict) || got != nil {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
	if bal, _, rows := ledger(t, f.s, f.buyer.ID); bal != 7000 || rows != 4 {
		t.Fatalf("a conflict moved money: balance %d, %d entries", bal, rows)
	}
	if bal, _, rows := ledger(t, f.s, stranger.ID); bal != 10_000 || rows != 1 {
		t.Fatalf("the stranger's wallet: balance %d, %d entries", bal, rows)
	}
	// A key an ordinary order already took cannot be used for a bulk's order.
	collide := f.params(2)
	if _, err := f.svc.CreateOrder(ctx, CreateOrderParams{UserID: f.buyer.ID, PlanID: f.plan.ID, IdempotencyKey: bulkOrderKey(collide.IdempotencyKey, 2)}); err != nil {
		t.Fatal(err)
	}
	balance, _, _ := ledger(t, f.s, f.buyer.ID)
	if _, err := f.svc.CreateBulk(ctx, collide); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("an order key that is taken: %v", err)
	}
	if after, _, _ := ledger(t, f.s, f.buyer.ID); after != balance {
		t.Fatalf("a collision moved money: %d -> %d", balance, after)
	}
	if n := f.count("bulk_orders", "idempotency_key = $1", collide.IdempotencyKey); n != 0 {
		t.Fatalf("a collision left a bulk behind")
	}
}

// Many requests at once with one key (a double tap, a retry that races the
// first) make one purchase, and every caller gets that purchase. With a
// license limit the places are counted under a lock; without one the unique key
// makes the later requests wait for the first.
func TestBulkConcurrentDoubleSubmit(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(fmt.Sprintf("limited=%v", limited), func(t *testing.T) {
			f := newBulkFixture(t, 1000, 10_000)
			needConns(t, f.s, 2)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if limited {
				w := &gateWorld{t: t, svc: f.svc, s: f.s, now: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), tg: 970000}
				w.v = licensed(t, f.svc, w.now)
				setTrialStart(t, f.s, w.now.Add(-24*time.Hour))
				w.endTrial()
			}
			p := f.params(5)
			const n = 8
			var (
				wg      sync.WaitGroup
				mu      sync.Mutex
				ids     = map[string]int{}
				created int
				errs    []error
			)
			start := make(chan struct{})
			for range n {
				wg.Go(func() {
					<-start
					v, err := f.svc.CreateBulk(ctx, p)
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						errs = append(errs, err)
						return
					}
					ids[v.ID]++
					if !v.Replayed {
						created++
					}
				})
			}
			close(start)
			wg.Wait()
			if len(errs) != 0 {
				t.Fatalf("errors: %v", errs)
			}
			if len(ids) != 1 || created != 1 {
				t.Fatalf("%d purchases (%v), %d of them made now, want one", len(ids), ids, created)
			}
			if bal, sum, rows := ledger(t, f.s, f.buyer.ID); bal != 5000 || sum != 5000 || rows != 6 {
				t.Fatalf("balance %d, ledger %d, %d entries after %d requests", bal, sum, rows, n)
			}
			if got := f.count("orders", "true"); got != 5 || len(orderPaidEvents(t, f.s)) != 5 {
				t.Fatalf("%d orders", got)
			}
		})
	}
}

// Several different purchases at once by one buyer can only spend what the
// wallet holds: each is made whole or not at all, the balance never goes below
// zero, and the ledger adds up.
func TestBulkConcurrentPurchasesShareOneWallet(t *testing.T) {
	f := newBulkFixture(t, 1000, 7000) // enough for two of three: 3 x 3000 is asked
	needConns(t, f.s, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const n = 6
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		paid      int
		short     int
		otherErrs []error
	)
	start := make(chan struct{})
	for range n {
		p := f.params(3)
		wg.Go(func() {
			<-start
			_, err := f.svc.CreateBulk(ctx, p)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				paid++
			case errors.Is(err, store.ErrInsufficientFunds):
				short++
			default:
				otherErrs = append(otherErrs, err)
			}
		})
	}
	close(start)
	wg.Wait()
	if len(otherErrs) != 0 {
		t.Fatalf("errors: %v", otherErrs)
	}
	if paid != 2 || short != n-2 {
		t.Fatalf("%d paid and %d refused, want 2 and %d", paid, short, n-2)
	}
	if bal, sum, _ := ledger(t, f.s, f.buyer.ID); bal != 1000 || sum != 1000 {
		t.Fatalf("balance %d, ledger %d", bal, sum)
	}
	if got := f.count("orders", "true"); got != 6 || f.count("bulk_orders", "true") != 2 {
		t.Fatalf("%d orders and %d bulks", got, f.count("bulk_orders", "true"))
	}
}

// A bulk purchase needs places for ALL its services among the active ones the
// license allows: five at twenty-three of twenty-five are refused whole, five at
// twenty are not, nothing is half-made, the refusal is noted for the staff alert,
// and a retry of a purchase made at the limit is not refused.
func TestBulkNeedsPlacesForEveryService(t *testing.T) {
	ctx := context.Background()
	newWorld := func(t *testing.T, active int) (*gateWorld, *store.User, *store.Plan) {
		t.Helper()
		w := newGateWorld(t)
		w.endTrial() // the free tier: 25 active services
		w.activeServices(active)
		u := seedUser(t, w.s, 971000)
		fund(t, w.s, u.ID, 1_000_000)
		return w, u, seedPlan(t, w.s, false, 1000)
	}
	buy := func(w *gateWorld, u *store.User, p *store.Plan, key string, q int) (*BulkView, error) {
		return w.svc.CreateBulk(ctx, BulkParams{UserID: u.ID, PlanID: p.ID, Quantity: q, IdempotencyKey: key})
	}
	orders := func(w *gateWorld, u *store.User) int {
		var n int
		if err := w.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.orders WHERE user_id = $1`, u.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("they all fit, or none is made", func(t *testing.T) {
		w, u, p := newWorld(t, license.FreeMaxActiveServices-3) // 3 places left
		_, err := buy(w, u, p, "places-5", 5)
		wantRefusal(t, "five at 22 of 25", err, GateActiveServices, "the free tier allows 25 active services", "buy or renew a license")
		if n := orders(w, u); n != 0 {
			t.Fatalf("%d orders were made", n)
		}
		if bal, _, rows := ledger(t, w.s, u.ID); bal != 1_000_000 || rows != 1 {
			t.Fatalf("money moved: %d, %d entries", bal, rows)
		}
		if at, _ := w.s.LimitHitAt(ctx, w.s.Conn()); at == nil {
			t.Fatal("the refused purchase was not noted for the staff alert")
		}
		if _, err := buy(w, u, p, "places-4", 4); gateOf(err) != GateActiveServices {
			t.Fatalf("four at 22 of 25: %v", err)
		}
		if v, err := buy(w, u, p, "places-3", 3); err != nil || len(v.Services) != 3 {
			t.Fatalf("three at 22 of 25 fill it exactly: %v", err)
		}
		// The place of each service is taken now: not even one more.
		if _, err := buy(w, u, p, "places-2", 2); gateOf(err) != GateActiveServices {
			t.Fatalf("two at 25 of 25: %v", err)
		}
		if _, err := w.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "single-after"}); gateOf(err) != GateActiveServices {
			t.Fatalf("a single purchase at 25 of 25: %v", err)
		}
		// A retry of the purchase made at the limit is that purchase, not a new one.
		if v, err := buy(w, u, p, "places-3", 3); err != nil || !v.Replayed {
			t.Fatalf("a retry at the limit: %v", err)
		}
		if n := orders(w, u); n != 3 {
			t.Fatalf("%d orders", n)
		}
	})

	t.Run("the quote says no beforehand", func(t *testing.T) {
		w, u, p := newWorld(t, license.FreeMaxActiveServices-1)
		if _, err := w.svc.QuoteBulk(ctx, BulkQuoteParams{UserID: u.ID, PlanID: p.ID, Quantity: 2}); gateOf(err) != GateActiveServices {
			t.Fatalf("a quote for two at 24 of 25: %v", err)
		}
		if at, _ := w.s.LimitHitAt(ctx, w.s.Conn()); at == nil {
			t.Fatal("the refused quote was not noted")
		}
	})

	t.Run("a license with more places, and one with none limited", func(t *testing.T) {
		w, u, p := newWorld(t, 0)
		w.install(func(tok *license.Token) { tok.Entitlements.MaxActiveServices = license.Unlimited })
		w.activeServices(30)
		if _, err := buy(w, u, p, "unlimited", 10); err != nil {
			t.Fatalf("unlimited: %v", err)
		}
		w.install(func(tok *license.Token) { tok.Entitlements.MaxActiveServices = 50 })
		if _, err := buy(w, u, p, "fifty-1", 10); err != nil { // 30 + 10 + 10 = 50
			t.Fatalf("up to the limit: %v", err)
		}
		if _, err := buy(w, u, p, "fifty-2", 2); gateOf(err) != GateActiveServices {
			t.Fatalf("past the limit: %v", err)
		}
	})

	t.Run("a reseller's purchase needs the resellers feature", func(t *testing.T) {
		w := newGateWorld(t)
		w.install(func(tok *license.Token) { tok.Entitlements.Resellers = true })
		r, err := w.makeReseller()
		if err != nil {
			t.Fatal(err)
		}
		fund(t, w.s, r.ID, 100_000)
		p := seedPlan(t, w.s, false, 1000)
		if _, err := buy(w, r, p, "reseller-ok", 2); err != nil {
			t.Fatalf("with the feature: %v", err)
		}
		w.install(func(tok *license.Token) { tok.Entitlements.Resellers = false })
		_, err = buy(w, r, p, "reseller-no", 2)
		wantRefusal(t, "a reseller without the feature", err, GateResellerPurchase, "does not include resellers")
		// A retry of what they bought while it was included is still that purchase.
		if v, err := buy(w, r, p, "reseller-ok", 2); err != nil || !v.Replayed {
			t.Fatalf("a retry after the feature left: %v", err)
		}
	})

	t.Run("purchases made at once take turns for the places", func(t *testing.T) {
		w, _, p := newWorld(t, license.FreeMaxActiveServices-5) // 5 places left
		needConns(t, w.s, 2)
		rctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		const n = 6
		buyers := make([]*store.User, n)
		for i := range buyers {
			buyers[i] = seedUser(t, w.s, int64(972000+i))
			fund(t, w.s, buyers[i].ID, 100_000)
		}
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			passed  int
			refused int
			other   []error
		)
		start := make(chan struct{})
		for i := range n {
			wg.Go(func() {
				<-start
				_, err := w.svc.CreateBulk(rctx, BulkParams{UserID: buyers[i].ID, PlanID: p.ID, Quantity: 3, IdempotencyKey: fmt.Sprintf("race-%d", i)})
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					passed++
				case gateOf(err) == GateActiveServices:
					refused++
				default:
					other = append(other, err)
				}
			})
		}
		close(start)
		wg.Wait()
		if len(other) != 0 {
			t.Fatalf("errors: %v", other)
		}
		if passed != 1 || refused != n-1 {
			t.Fatalf("%d passed and %d refused with 5 places and 3 each, want 1 and %d", passed, refused, n-1)
		}
		var total int
		if err := w.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.orders WHERE bulk_id IS NOT NULL`).Scan(&total); err != nil || total != 3 {
			t.Fatalf("%d bulk orders (%v)", total, err)
		}
	})
}

// A banned or removed reseller cannot buy: their tier price holds only while
// they are one, and removing one waits for a purchase in flight.
func TestBulkByAReseller(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9300, "admin")
	plan := seedPlan(t, s, false, 1000)
	tier := seedTier(t, svc, admin, "Gold", 50, 10, nil)
	r := reseller(t, svc, s, admin, 9301, tier, 0)
	fund(t, s, r.ID, 100_000)
	v, err := svc.CreateBulk(ctx, BulkParams{UserID: r.ID, PlanID: plan.ID, Quantity: 3, IdempotencyKey: "r-1", LabelPrefix: "Shop"})
	if err != nil || v.UnitAmount != 500 {
		t.Fatalf("a reseller's bulk: %v %+v", err, v)
	}
	if err := s.SetUserRole(ctx, s.Conn(), r.ID, "reseller", "banned"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateBulk(ctx, BulkParams{UserID: r.ID, PlanID: plan.ID, Quantity: 3, IdempotencyKey: "r-2"}); !errors.Is(err, ErrUserBanned) {
		t.Fatalf("a banned reseller: %v", err)
	}
}

// A customer invited by a reseller earns the reseller a commission on every
// order, so a bulk of three pays three commissions; a customer invited by
// another customer earns the inviter the reward once, on the first order.
func TestBulkCommissionsAndReferralRewards(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9300, "admin")
	own := owner(t, svc)
	plan := seedPlan(t, s, false, 1000)
	tier := seedTier(t, svc, admin, "Gold", 0, 10, nil)
	res := reseller(t, svc, s, admin, 9301, tier, 0)
	invitedByReseller, err := s.UpsertUser(ctx, s.Conn(), 9302, "", "", res.ID)
	if err != nil {
		t.Fatal(err)
	}
	fund(t, s, invitedByReseller.ID, 10_000)
	if _, err := svc.CreateBulk(ctx, BulkParams{UserID: invitedByReseller.ID, PlanID: plan.ID, Quantity: 3, IdempotencyKey: "c-1"}); err != nil {
		t.Fatal(err)
	}
	if n, sum := ledgerKinds(t, s, res.ID, "commission"); n != 3 || sum != 300 {
		t.Fatalf("the reseller's commissions: %d of %d in all, want 3 of 300", n, sum)
	}

	if err := svc.SetSetting(ctx, own, "referral.reward_percent", "10"); err != nil {
		t.Fatal(err)
	}
	inviter := seedUser(t, s, 9303)
	invited, err := s.UpsertUser(ctx, s.Conn(), 9304, "", "", inviter.ID)
	if err != nil {
		t.Fatal(err)
	}
	fund(t, s, invited.ID, 10_000)
	if _, err := svc.CreateBulk(ctx, BulkParams{UserID: invited.ID, PlanID: plan.ID, Quantity: 4, IdempotencyKey: "c-2"}); err != nil {
		t.Fatal(err)
	}
	if n, sum := ledgerKinds(t, s, inviter.ID, "referral"); n != 1 || sum != 100 {
		t.Fatalf("the inviter's rewards: %d of %d in all, want 1 of 100 (the first service only)", n, sum)
	}
}

// Delivered services are announced with their bulk until the bulk's one message
// is announced, once all are delivered; a bulk with a service still on its way
// waits; a service that fails keeps being retried and is announced on its own
// when it is ready, after the message.
func TestBulkIsAnnouncedOnceWhenEveryServiceIsDone(t *testing.T) {
	f := newBulkFixture(t, 1000, 10_000)
	ctx := context.Background()
	v, err := f.svc.CreateBulk(ctx, f.params(3))
	if err != nil {
		t.Fatal(err)
	}
	fp := &fakeProvisioner{}
	w := f.svc.NewProvisionWorker(fp, nil)
	settle := func() int {
		t.Helper()
		n, err := w.SettleBulks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := settle(); n != 0 {
		t.Fatalf("a bulk nobody delivered yet was announced")
	}
	for i := 1; i <= 3; i++ {
		if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
			t.Fatalf("provision %d: %v %v", i, worked, err)
		}
		if n := settle(); (i < 3) == (n == 1) {
			t.Fatalf("after %d of 3 delivered: %d announcements", i, n)
		}
	}
	// Each delivered service carries its bulk, so the bot sends nothing for it on its own.
	evs := provisionedEvents(t, f.s)
	if len(evs) != 3 {
		t.Fatalf("%d provisioned events", len(evs))
	}
	for _, e := range evs {
		if e.BulkID != v.ID {
			t.Fatalf("a delivered service without its bulk: %+v", e)
		}
	}
	ready := bulkReadyEvents(t, f.s)
	if len(ready) != 1 {
		t.Fatalf("%d bulk announcements", len(ready))
	}
	e := ready[0]
	if e.BulkID != v.ID || e.UserID != f.buyer.ID || e.TelegramID != f.buyer.TelegramID || e.PlanID != f.plan.ID || e.Quantity != 3 || e.Delayed != 0 ||
		e.Amount != 3000 || e.Currency != "IRT" || len(e.ReadyOrders) != 3 {
		t.Fatalf("announcement: %+v", e)
	}
	for i, id := range e.ReadyOrders {
		if id != v.Services[i].Order.ID {
			t.Fatalf("ready order %d is %s, want %s (in the bulk's order)", i+1, id, v.Services[i].Order.ID)
		}
	}
	if got, _ := f.s.GetBulk(ctx, f.s.Conn(), v.ID); got.DeliveredAt == nil {
		t.Fatal("the bulk was not marked announced")
	}
	// Once only, however often the worker looks.
	for range 3 {
		if n := settle(); n != 0 {
			t.Fatalf("announced again")
		}
	}
	// The view now carries the links.
	got, err := f.svc.GetBulk(ctx, v.ID, f.buyer.ID)
	if err != nil || got.DeliveredAt == nil || len(got.Services) != 3 {
		t.Fatalf("view: %v %+v", err, got)
	}
	for i, sv := range got.Services {
		if sv.SubscriptionID == "" || sv.SubStatus != "active" || !strings.HasPrefix(sv.SubLink, "https://sub.example.com/sub/") || sv.Order.Status != "active" {
			t.Fatalf("service %d: %+v", i+1, sv)
		}
	}
}

// A service that cannot be delivered does not hold back the others for ever:
// while it may still succeed soon (fewer failures than the alert threshold) the
// bulk waits; once it has failed that often the staff are told as for any order
// (with the bulk named), the bulk is announced with the delivered ones and how
// many are delayed, and the service is announced alone when it is finally ready.
func TestBulkWithAServiceThatFails(t *testing.T) {
	f := newBulkFixture(t, 1000, 10_000)
	ctx := context.Background()
	v, err := f.svc.CreateBulk(ctx, f.params(3))
	if err != nil {
		t.Fatal(err)
	}
	fp := &fakeProvisioner{failures: 1}
	w := f.svc.NewProvisionWorker(fp, nil)
	// The first order the worker takes fails; the other two are delivered.
	for i := 0; i < 3; i++ {
		if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
			t.Fatalf("step %d: %v %v", i, worked, err)
		}
	}
	var failed store.Order
	for _, sv := range v.Services {
		o, _ := f.s.GetOrder(ctx, f.s.Conn(), sv.Order.ID)
		if o.Status == "provision_failed" {
			failed = *o
		}
	}
	if failed.ID == "" {
		t.Fatal("no order failed")
	}
	makeDue := func() {
		t.Helper()
		if _, err := f.s.DB().Exec(ctx, `UPDATE core.orders SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, failed.ID); err != nil {
			t.Fatal(err)
		}
	}
	announce := func() int {
		t.Helper()
		n, err := w.SettleBulks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// One failure so far: it will be retried soon, so the bulk waits for it.
	if n := announce(); n != 0 {
		t.Fatal("announced with a service that is about to be retried")
	}
	fp.failures = 100 // from now on every attempt fails
	for attempt := 2; attempt <= 3; attempt++ {
		makeDue()
		if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
			t.Fatalf("attempt %d: %v %v", attempt, worked, err)
		}
		if n := f.attempts(failed.ID); n != attempt {
			t.Fatalf("attempt %d: the order counts %d", attempt, n)
		}
		if want := attempt == 3; (announce() == 1) != want {
			t.Fatalf("after %d failures: announced = %v", attempt, !want)
		}
	}
	// The staff were told once, naming the bulk.
	var pf []events.ProvisionFailedEvent
	for _, raw := range outboxEvents(t, f.s, events.ProvisionFailed) {
		var e events.ProvisionFailedEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		pf = append(pf, e)
	}
	if len(pf) != 1 || pf[0].OrderID != failed.ID || pf[0].BulkID != v.ID || pf[0].Attempts != 3 {
		t.Fatalf("provision_failed events: %+v", pf)
	}
	ready := bulkReadyEvents(t, f.s)
	if len(ready) != 1 || ready[0].Delayed != 1 || len(ready[0].ReadyOrders) != 2 {
		t.Fatalf("announcement: %+v", ready)
	}
	for _, id := range ready[0].ReadyOrders {
		if id == failed.ID {
			t.Fatal("the failed order is among the ready ones")
		}
	}
	// It is retried in the background and, once ready, announced on its own: no
	// bulk on the event, and no second announcement of the bulk.
	fp.failures = 0
	makeDue()
	if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("the retry: %v %v", worked, err)
	}
	if n := announce(); n != 0 {
		t.Fatal("the bulk was announced again")
	}
	var late []events.SubscriptionProvisionedEvent
	for _, e := range provisionedEvents(t, f.s) {
		if e.OrderID == failed.ID {
			late = append(late, e)
		}
	}
	if len(late) != 1 || late[0].BulkID != "" || late[0].SubscriptionLink == "" {
		t.Fatalf("the late service: %+v", late)
	}
	for _, e := range provisionedEvents(t, f.s) {
		if e.OrderID != failed.ID && e.BulkID != v.ID {
			t.Fatalf("a service delivered in time without its bulk: %+v", e)
		}
	}
}

// Orders staff refund before they are delivered do not hold the bulk back, and a
// bulk with nothing left to deliver is closed without a word.
func TestBulkIgnoresRefundedOrders(t *testing.T) {
	f := newBulkFixture(t, 1000, 10_000)
	ctx := context.Background()
	own := owner(t, f.svc)
	v, err := f.svc.CreateBulk(ctx, f.params(3))
	if err != nil {
		t.Fatal(err)
	}
	fp := &fakeProvisioner{}
	w := f.svc.NewProvisionWorker(fp, nil)
	if _, err := f.svc.RefundOrder(ctx, own, fp, v.Services[2].Order.ID, "wrong plan"); err != nil {
		t.Fatal(err)
	}
	if bal, _, _ := ledger(t, f.s, f.buyer.ID); bal != 8000 {
		t.Fatalf("balance after refunding one: %d", bal)
	}
	for range 2 {
		if worked, err := w.ProvisionNext(ctx); !worked || err != nil {
			t.Fatalf("provision: %v %v", worked, err)
		}
	}
	if n, err := w.SettleBulks(ctx); err != nil || n != 1 {
		t.Fatalf("settle: %d %v", n, err)
	}
	if ready := bulkReadyEvents(t, f.s); len(ready) != 1 || len(ready[0].ReadyOrders) != 2 || ready[0].Delayed != 0 {
		t.Fatalf("announcement: %+v", ready)
	}

	// All refunded before delivery: nothing to tell, so nothing is announced.
	g := newBulkFixture(t, 1000, 10_000)
	vg, err := g.svc.CreateBulk(ctx, g.params(2))
	if err != nil {
		t.Fatal(err)
	}
	own = owner(t, g.svc)
	for _, sv := range vg.Services {
		if _, err := g.svc.RefundOrder(ctx, own, fp, sv.Order.ID, "changed my mind"); err != nil {
			t.Fatal(err)
		}
	}
	wg := g.svc.NewProvisionWorker(fp, nil)
	if n, err := wg.SettleBulks(ctx); err != nil || n != 0 {
		t.Fatalf("settle: %d %v", n, err)
	}
	if got, _ := g.s.GetBulk(ctx, g.s.Conn(), vg.ID); got.DeliveredAt == nil {
		t.Fatal("a bulk with nothing left stays open")
	}
	if len(bulkReadyEvents(t, g.s)) != 0 {
		t.Fatal("announced a bulk with nothing to deliver")
	}
}

// The links of a bulk are the buyer's alone.
func TestGetBulkBelongsToTheBuyer(t *testing.T) {
	f := newBulkFixture(t, 1000, 5000)
	ctx := context.Background()
	v, err := f.svc.CreateBulk(ctx, f.params(2))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.svc.GetBulk(ctx, v.ID, ""); err != nil || got.ID != v.ID {
		t.Fatalf("without a buyer named: %v", err)
	}
	stranger := seedUser(t, f.s, 7003)
	if _, err := f.svc.GetBulk(ctx, v.ID, stranger.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("someone else's bulk: %v", err)
	}
	if _, err := f.svc.GetBulk(ctx, "00000000-0000-7000-8000-00000000dead", f.buyer.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown bulk: %v", err)
	}
}
