package domain

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Owner accounts' purchases are internal (docs/14-reports.md, "Internal orders"): with reports.exclude_owner_orders on, what an owner buys is
// stamped when it is made and left out of revenue, the reports, the growth rewards; the stamp is on the row, so the switch never rewrites history.

// internalEnv is the cashback environment (an owner, a plan of 10,000 Toman, cashback at 2.5%) with an admin and a customer to compare with.
type internalEnv struct {
	*cashbackEnv
	admin, customer *store.User
}

func newEnv(t *testing.T) *internalEnv {
	t.Helper()
	e := &internalEnv{cashbackEnv: newCashbackEnv(t)}
	e.admin = staffMember(t, e.s, 9802, "admin")
	e.customer = seedUser(t, e.s, 9803)
	return e
}

func (e *internalEnv) switchOn(on bool) {
	e.t.Helper()
	setSetting(e.t, e.s, SettingExcludeOwnerOrders, strconv.FormatBool(on))
}

// purchase funds u with the plan's price and buys one service, delivered.
func (e *internalEnv) purchase(u *store.User) *store.Order {
	e.t.Helper()
	e.fundNow(u, 10_000)
	o := e.buy(u)
	got, err := e.s.GetOrder(context.Background(), e.s.Conn(), o.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

// ledgerInternal is the flag of the entries of an order of a kind.
func (e *internalEnv) ledgerInternal(kind, orderID string) bool {
	e.t.Helper()
	var v bool
	if err := e.s.DB().QueryRow(context.Background(), `SELECT internal FROM core.ledger_entries WHERE kind = $1 AND ref_id = $2`, kind, orderID).Scan(&v); err != nil {
		e.t.Fatalf("%s of %s: %v", kind, orderID, err)
	}
	return v
}

func (e *internalEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.s.DB().QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// rng is a range of days around now, in UTC.
func (e *internalEnv) rng() *ReportRange {
	e.t.Helper()
	now := time.Now().UTC()
	r, err := NewReportRange(ReportRequest{From: now.AddDate(0, 0, -1).Format("2006-01-02"), To: now.AddDate(0, 0, 1).Format("2006-01-02"), TZ: "UTC"}, "UTC", now)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *internalEnv) totals() *store.ReportCell {
	e.t.Helper()
	rep, err := e.svc.SalesReport(context.Background(), e.rng())
	if err != nil {
		e.t.Fatal(err)
	}
	if c := rep.Totals.Currencies["IRT"]; c != nil {
		return c
	}
	return &store.ReportCell{}
}

func (e *internalEnv) refund(o *store.Order) {
	e.t.Helper()
	if _, err := e.svc.RefundOrder(context.Background(), e.owner, e.fp, o.ID, "test"); err != nil {
		e.t.Fatalf("refund: %v", err)
	}
}

// (1) and (2) and (3): the switch is off by default and stamps nothing; on, an owner's order and entries are internal and out of every figure,
// a customer's, an admin's are not; turning it off does not change what was stamped.
func TestOwnerPurchasesAreStampedInternalWhileTheSwitchIsOn(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if sp, ok := SpecOf(SettingExcludeOwnerOrders); !ok || sp.Default != "false" || sp.Kind != KindBool || sp.Group != "reports" || sp.Perm != permSettings {
		t.Fatalf("the switch: %+v", sp)
	}

	// Off (the default): an owner's purchase is like anyone's, and nothing is stamped.
	offOrder := e.purchase(e.owner)
	if offOrder.Internal || e.ledgerInternal("purchase", offOrder.ID) || e.count(`SELECT count(*) FROM core.orders WHERE internal`) != 0 ||
		e.count(`SELECT count(*) FROM core.ledger_entries WHERE internal`) != 0 {
		t.Fatalf("the switch is off and something was stamped: %+v", offOrder)
	}
	if e.entries("cashback") != 1 {
		t.Fatalf("an owner earns cashback while the switch is off: %d", e.entries("cashback"))
	}

	e.switchOn(true)
	ownerOrder := e.purchase(e.owner)
	adminOrder := e.purchase(e.admin)
	customerOrder := e.purchase(e.customer)
	if !ownerOrder.Internal || !e.ledgerInternal("purchase", ownerOrder.ID) {
		t.Errorf("the owner's order is not internal: %+v", ownerOrder)
	}
	for name, o := range map[string]*store.Order{"admin": adminOrder, "customer": customerOrder} {
		if o.Internal || e.ledgerInternal("purchase", o.ID) {
			t.Errorf("%s's order is internal: %+v", name, o)
		}
	}
	// The owner's top-up of the same moment is stamped too; the others' are not.
	if got := e.count(`SELECT count(*) FROM core.ledger_entries l JOIN core.users u ON u.id = l.user_id WHERE l.internal AND u.role <> 'owner'`); got != 0 {
		t.Errorf("%d entries of other roles are internal", got)
	}
	if got := e.count(`SELECT count(*) FROM core.ledger_entries WHERE internal AND kind = 'topup'`); got != 1 {
		t.Errorf("internal top-ups: %d, want the owner's one", got)
	}
	// An owner's internal purchase earns no cashback; the admin's and the customer's do (the off-switch order earned one before).
	if e.entries("cashback") != 3 {
		t.Errorf("cashback entries: %d, want 3 (the first order, the admin's and the customer's)", e.entries("cashback"))
	}

	// Every figure leaves the owner's internal purchase out: three orders of 10,000 count, not four.
	c := e.totals()
	if c.Revenue != 30_000 || c.Orders != 3 || c.NewOrders != 3 || c.Topups.Count != 3 {
		t.Errorf("the summary: %+v", c)
	}
	plans, err := e.svc.TopPlans(ctx, e.rng())
	if err != nil || len(plans) != 1 || plans[0].Orders != 3 || plans[0].Revenue != 30_000 {
		t.Errorf("top plans: %+v %v", plans, err)
	}
	series, err := e.svc.SalesByPlan(ctx, e.rng())
	var seriesOrders int64
	for _, p := range series {
		seriesOrders += p.Orders
	}
	if err != nil || seriesOrders != 3 {
		t.Errorf("plan series: %d orders %v", seriesOrders, err)
	}
	hours, err := e.svc.SalesHours(ctx, e.rng())
	var hourOrders, hourRevenue int64
	for _, h := range hours {
		hourOrders += h.Orders
		hourRevenue += h.Revenue
	}
	if err != nil || hourOrders != 3 || hourRevenue != 30_000 {
		t.Errorf("hours: %d orders, %d %v", hourOrders, hourRevenue, err)
	}
	methods, err := e.svc.SalesByMethod(ctx, e.rng())
	var methodOrders, methodRevenue int64
	for _, m := range methods {
		if m.Method == "unknown" {
			t.Errorf("an internal order left an unmatched event behind: %+v", m)
		}
		methodOrders += m.Orders
		methodRevenue += m.Revenue
	}
	if err != nil || methodOrders != 3 || methodRevenue != 30_000 {
		t.Errorf("methods: %+v %v", methods, err)
	}
	ov, err := e.s.OverviewStats(ctx, e.s.Conn(), time.Now())
	if err != nil || ov.Revenue24h["IRT"] != 30_000 || ov.Revenue30d["IRT"] != 30_000 || ov.OrdersPaid24h != 3 {
		t.Errorf("overview: %+v %v", ov, err)
	}
	counts, err := e.s.PaidOrderCounts(ctx, e.s.Conn(), time.Now())
	if err != nil || counts.Hour != 3 {
		t.Errorf("paid order counts: %+v %v", counts, err)
	}
	// The card shows what was left out, and only that.
	in, err := e.svc.InternalOrders(ctx, e.rng())
	if err != nil || in.Orders != 1 || len(in.Currencies) != 1 || in.Currencies[0] != (store.InternalCurrency{Currency: "IRT", Orders: 1, Amount: 10_000}) ||
		len(in.Accounts) != 1 || in.Accounts[0].TelegramID != e.owner.TelegramID || in.Accounts[0].Orders != 1 {
		t.Errorf("internal card: %+v %v", in, err)
	}

	// Turned off again, the stamped rows stay, and a new purchase of the owner is not stamped.
	e.switchOn(false)
	if got := e.count(`SELECT count(*) FROM core.orders WHERE internal`); got != 1 {
		t.Errorf("internal orders after the switch went off: %d", got)
	}
	if !e.ledgerInternal("purchase", ownerOrder.ID) {
		t.Error("the stamp went when the switch went off")
	}
	later := e.purchase(e.owner)
	if later.Internal || e.ledgerInternal("purchase", later.ID) {
		t.Errorf("stamped while the switch is off: %+v", later)
	}
	if c := e.totals(); c.Revenue != 40_000 || c.Orders != 4 {
		t.Errorf("after the switch went off: %+v", c)
	}
	// And turned on again, the first (unstamped) order stays counted.
	e.switchOn(true)
	if c := e.totals(); c.Revenue != 40_000 || c.Orders != 4 {
		t.Errorf("after the switch came on again: %+v", c)
	}
}

// (8) A refund carries the flag of the order it gives back, whatever the switch says when it is made; the cashback and its reversal the same.
func TestRefundsOfInternalPurchasesStayInternal(t *testing.T) {
	e := newEnv(t)
	// Stamped while on, refunded after the switch went off.
	e.switchOn(true)
	internalOrder := e.purchase(e.owner)
	customerOrder := e.purchase(e.customer)
	e.switchOn(false)
	// Not stamped (switch off), refunded while it is on.
	plainOrder := e.purchase(e.owner)
	e.switchOn(true)

	e.refund(internalOrder)
	e.refund(plainOrder)
	e.refund(customerOrder)
	if !e.ledgerInternal("refund", internalOrder.ID) {
		t.Error("the refund of an internal purchase is not internal")
	}
	if e.ledgerInternal("refund", plainOrder.ID) {
		t.Error("the refund of an owner's purchase made before the switch is internal: revenue and refunds would disagree")
	}
	if e.ledgerInternal("refund", customerOrder.ID) {
		t.Error("a customer's refund is internal")
	}
	// The owner's plain order earned cashback while the switch was off; its reversal is plain too, and the customer's.
	if e.ledgerInternal("cashback_reversal", plainOrder.ID) || e.ledgerInternal("cashback_reversal", customerOrder.ID) {
		t.Error("a cashback reversal took the owner's switch instead of its order's")
	}
	// What the reports see: two orders and their two refunds (the plain and the customer's), nothing of the internal pair.
	c := e.totals()
	if c.Orders != 2 || c.Revenue != 20_000 || c.Refunds.Count != 2 || c.Refunds.Amount != 20_000 || domainNet(c) != 0 {
		t.Errorf("summary: %+v", c)
	}
	in, err := e.svc.InternalOrders(context.Background(), e.rng())
	if err != nil || in.Orders != 1 || in.Currencies[0].Refunds != 1 || in.Currencies[0].RefundsAmount != 10_000 {
		t.Errorf("the card: %+v %v", in, err)
	}
	// The books still balance: no amount moved.
	for _, u := range []*store.User{e.owner, e.customer} {
		e.consistent(u)
	}
}

func domainNet(c *store.ReportCell) int64 { return Net(c) }

// A license and a digital account refund take the flag of the purchase they give back; a purchase of either by an owner is internal while the
// switch is on, and a customer's is not.
func TestLicenseAndDigitalRefundsFollowTheirPurchase(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	owner := staffMember(t, s, 9810, "owner")
	customer := seedUser(t, s, 9811)
	setSetting(t, s, SettingExcludeOwnerOrders, "true")
	fund(t, s, owner.ID, 1_000_000)
	fund(t, s, customer.ID, 1_000_000)
	flag := func(key string) bool {
		t.Helper()
		var v bool
		if err := s.DB().QueryRow(ctx, `SELECT internal FROM core.ledger_entries WHERE idempotency_key = $1`, key).Scan(&v); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	move := func(u *store.User, kind, refType, ref string, credit bool) {
		t.Helper()
		err := s.WithTx(ctx, func(tx pgx.Tx) error {
			e := &store.LedgerEntry{Kind: kind, RefType: &refType, RefID: &ref, IdempotencyKey: kind + ":" + ref}
			var err error
			if credit {
				_, err = s.Credit(ctx, tx, u.ID, "IRT", 5_000, e)
			} else {
				_, err = s.Debit(ctx, tx, u.ID, "IRT", 5_000, e)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ buy, back, refType string }{{"license_purchase", "license_refund", "vendor_order"}, {"supplier_purchase", "supplier_refund", "supplier_order"}} {
		ownerRef, customerRef := buuid.MustV7().String(), buuid.MustV7().String()
		move(owner, c.buy, c.refType, ownerRef, false)
		move(customer, c.buy, c.refType, customerRef, false)
		if !flag(c.buy+":"+ownerRef) || flag(c.buy+":"+customerRef) {
			t.Fatalf("%s: the owner's purchase is not internal or the customer's is", c.buy)
		}
		setSetting(t, s, SettingExcludeOwnerOrders, "false") // the refunds come after the switch went off
		move(owner, c.back, c.refType, ownerRef, true)
		move(customer, c.back, c.refType, customerRef, true)
		setSetting(t, s, SettingExcludeOwnerOrders, "true")
		if !flag(c.back+":"+ownerRef) || flag(c.back+":"+customerRef) {
			t.Errorf("%s: the refunds do not follow their purchases", c.back)
		}
	}
	// A license bought before the switch, refunded after it came on, stays plain.
	setSetting(t, s, SettingExcludeOwnerOrders, "false")
	ref := buuid.MustV7().String()
	move(owner, "license_purchase", "vendor_order", ref, false)
	setSetting(t, s, SettingExcludeOwnerOrders, "true")
	move(owner, "license_refund", "vendor_order", ref, true)
	if flag("license_purchase:"+ref) || flag("license_refund:"+ref) {
		t.Error("a license bought while the switch was off is internal, or its refund is")
	}
}

// (4) The dashboard's actions on the owner accounts' earlier rows: the dry run counts and changes nothing, the action marks them (the flag
// only), the undo puts them back, both are idempotent and audited.
func TestOwnerRowsCanBeCountedInternalAfterTheFact(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ownerA, ownerB := e.purchase(e.owner), e.purchase(e.owner)
	e.purchase(e.customer)
	e.refund(ownerB)
	if c := e.totals(); c.Orders != 3 || c.Revenue != 30_000 || c.Refunds.Count != 1 {
		t.Fatalf("before: %+v", c)
	}
	snapshot := func() string {
		var s string
		if err := e.s.DB().QueryRow(ctx, `SELECT string_agg(l::text, '|' ORDER BY id) FROM (SELECT id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at FROM core.ledger_entries) l`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		var o string
		if err := e.s.DB().QueryRow(ctx, `SELECT string_agg(l::text, '|' ORDER BY id) FROM (SELECT id, user_id, status, amount, created_at, updated_at, refunded_at FROM core.orders) l`).Scan(&o); err != nil {
			t.Fatal(err)
		}
		return s + "#" + o
	}
	before := snapshot()
	ownerRows := e.count(`SELECT count(*) FROM core.ledger_entries l JOIN core.users u ON u.id = l.user_id WHERE u.role = 'owner'`)

	preview, err := e.svc.InternalBackfillPreview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Orders != 2 || preview.Entries != int64(ownerRows) || len(preview.Currencies) != 1 ||
		preview.Currencies[0] != (store.InternalCurrency{Currency: "IRT", Orders: 2, Amount: 20_000, Refunds: 1, RefundsAmount: 10_000}) {
		t.Errorf("the dry run: %+v", preview)
	}
	if snapshot() != before || e.count(`SELECT count(*) FROM core.orders WHERE internal`) != 0 || auditCount(t, e.s, "reports.internal_backfill") != 0 {
		t.Fatal("the dry run changed something")
	}

	moved, err := e.svc.CountOwnerRowsInternal(ctx, e.owner)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Orders != 2 || moved.Entries != int64(ownerRows) || moved.Currencies[0].Amount != 20_000 {
		t.Errorf("moved: %+v", moved)
	}
	if got := e.count(`SELECT count(*) FROM core.orders WHERE internal`); got != 2 {
		t.Errorf("internal orders: %d", got)
	}
	if got := e.count(`SELECT count(*) FROM core.ledger_entries WHERE internal`); got != ownerRows {
		t.Errorf("internal entries: %d, want %d", got, ownerRows)
	}
	if got := e.count(`SELECT count(*) FROM core.ledger_entries l JOIN core.users u ON u.id = l.user_id WHERE l.internal AND u.role <> 'owner'`); got != 0 {
		t.Errorf("%d entries of other users moved", got)
	}
	if snapshot() != before {
		t.Error("an amount, a status, a time or a balance changed: only the flag may")
	}
	if c := e.totals(); c.Orders != 1 || c.Revenue != 10_000 || c.Refunds.Count != 0 || c.Topups.Count != 1 {
		t.Errorf("after: %+v", c)
	}
	in, err := e.svc.InternalOrders(ctx, e.rng())
	if err != nil || in.Orders != 2 || in.Currencies[0].Amount != 20_000 || in.Currencies[0].Refunds != 1 {
		t.Errorf("the card: %+v %v", in, err)
	}
	for _, o := range []*store.Order{ownerA, ownerB} {
		if got, _ := e.s.GetOrder(ctx, e.s.Conn(), o.ID); !got.Internal {
			t.Errorf("order %s is not internal", o.ID)
		}
	}
	if n := auditCount(t, e.s, "reports.internal_backfill"); n != 1 {
		t.Errorf("audit entries: %d", n)
	}
	var detail string
	if err := e.s.DB().QueryRow(ctx, `SELECT after::text FROM core.audit_log WHERE action = 'reports.internal_backfill'`).Scan(&detail); err != nil ||
		!strings.Contains(detail, `"orders": 2`) || !strings.Contains(detail, fmt.Sprintf(`"ledger_entries": %d`, ownerRows)) {
		t.Errorf("audit detail: %s %v", detail, err)
	}

	// Again: nothing left to move, nothing written.
	again, err := e.svc.CountOwnerRowsInternal(ctx, e.owner)
	if err != nil || again.Orders != 0 || again.Entries != 0 || auditCount(t, e.s, "reports.internal_backfill") != 1 {
		t.Errorf("second backfill: %+v %v, audits %d", again, err, auditCount(t, e.s, "reports.internal_backfill"))
	}
	if p, err := e.svc.InternalBackfillPreview(ctx); err != nil || p.Orders != 0 || p.Entries != 0 {
		t.Errorf("preview after: %+v %v", p, err)
	}

	// Undo: everything counts again, once.
	undone, err := e.svc.UndoOwnerRowsInternal(ctx, e.owner)
	if err != nil || undone.Orders != 2 || undone.Entries != int64(ownerRows) {
		t.Errorf("undo: %+v %v", undone, err)
	}
	if c := e.totals(); c.Orders != 3 || c.Revenue != 30_000 || c.Refunds.Count != 1 {
		t.Errorf("after the undo: %+v", c)
	}
	if got := e.count(`SELECT count(*) FROM core.orders WHERE internal`) + e.count(`SELECT count(*) FROM core.ledger_entries WHERE internal`); got != 0 {
		t.Errorf("%d internal rows after the undo", got)
	}
	if snapshot() != before {
		t.Error("the undo changed more than the flag")
	}
	if again, err := e.svc.UndoOwnerRowsInternal(ctx, e.owner); err != nil || again.Orders != 0 || again.Entries != 0 {
		t.Errorf("second undo: %+v %v", again, err)
	}
	if n := auditCount(t, e.s, "reports.internal_undo"); n != 1 {
		t.Errorf("undo audit entries: %d", n)
	}
	if auditCount(t, e.s, "reports.internal_backfill") != 1 {
		t.Error("the backfill audit entry changed")
	}
}

// (5) Cashback, the reward code, the invite reward, the daily wheel and the resellers' targets ignore an internal purchase.
func TestInternalPurchasesEarnNothing(t *testing.T) {
	t.Run("reward code", func(t *testing.T) {
		e := newRewardEnv(t)
		ctx := context.Background()
		setSetting(t, e.s, SettingExcludeOwnerOrders, "true")
		customer := seedUser(t, e.s, 9820)
		for _, u := range []*store.User{e.owner, customer} {
			fund(t, e.s, u.ID, 10_000)
		}
		codes := func(u *store.User) int {
			var n int
			if err := e.s.DB().QueryRow(ctx, `SELECT count(*) FROM core.discount_codes WHERE user_id = $1`, u.ID).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if _, err := e.buyPlan(e.owner, e.reward, "rw-owner", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := e.buyPlan(customer, e.reward, "rw-cust", ""); err != nil {
			t.Fatal(err)
		}
		if codes(e.owner) != 0 || codes(customer) != 1 {
			t.Errorf("reward codes: owner %d (want 0), customer %d (want 1)", codes(e.owner), codes(customer))
		}
		// The same owner buying while the switch is off earns one: the test is not vacuous.
		setSetting(t, e.s, SettingExcludeOwnerOrders, "false")
		if _, err := e.buyPlan(e.owner, e.reward, "rw-owner-2", ""); err != nil {
			t.Fatal(err)
		}
		if codes(e.owner) != 1 {
			t.Errorf("an owner's purchase with the switch off earns no reward code: %d", codes(e.owner))
		}
	})

	t.Run("invite reward", func(t *testing.T) {
		svc, s := testService(t)
		ctx := context.Background()
		plan := seedPlan(t, s, false, 1000)
		inviter := seedUser(t, s, 9830)
		owner := staffMember(t, s, 9831, "owner")
		customer := seedUser(t, s, 9832)
		for _, u := range []*store.User{owner, customer} {
			if _, err := s.DB().Exec(ctx, `UPDATE core.users SET referred_by = $2 WHERE id = $1`, u.ID, inviter.ID); err != nil {
				t.Fatal(err)
			}
			fund(t, s, u.ID, 3000)
		}
		setSetting(t, s, "referral.reward_percent", "10")
		setSetting(t, s, SettingExcludeOwnerOrders, "true")
		buy := func(u *store.User, key string) {
			t.Helper()
			o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: key})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
				t.Fatal(err)
			}
		}
		buy(owner, "inv-owner-1")
		if bal, _, _ := ledger(t, s, inviter.ID); bal != 0 {
			t.Fatalf("the inviter was rewarded for an internal purchase: %d", bal)
		}
		buy(customer, "inv-cust-1")
		if bal, _, _ := ledger(t, s, inviter.ID); bal != 100 {
			t.Fatalf("the inviter's reward for a customer's purchase: %d, want 100", bal)
		}
		// An internal first purchase leaves the owner's one reward free for a later, real purchase.
		setSetting(t, s, SettingExcludeOwnerOrders, "false")
		buy(owner, "inv-owner-2")
		if bal, _, _ := ledger(t, s, inviter.ID); bal != 200 {
			t.Fatalf("the inviter's balance after a plain purchase: %d, want 200", bal)
		}
	})

	t.Run("daily wheel", func(t *testing.T) {
		e := newWheelEnv(t)
		ctx := context.Background()
		// A customer whose only order was made while they were an owner's account: it does not open the wheel.
		u := seedUser(t, e.s, 9840)
		if err := e.s.SetUserRole(ctx, e.s.Conn(), u.ID, "owner", "active"); err != nil {
			t.Fatal(err)
		}
		e.set(map[string]string{SettingExcludeOwnerOrders: "true"})
		e.payOrder(u)
		if err := e.s.SetUserRole(ctx, e.s.Conn(), u.ID, "user", "active"); err != nil {
			t.Fatal(err)
		}
		st, err := e.svc.Wheel(ctx, u.ID)
		if err != nil || st.Orders != 0 || st.Reason != WheelOrders {
			t.Fatalf("wheel state: %+v %v", st, err)
		}
		if r, err := e.svc.SpinWheel(ctx, u.ID); err != nil || r.Status != WheelOrders {
			t.Fatalf("spin: %+v %v", r, err)
		}
		// Another order, made as a plain customer, opens it.
		e.payOrder(u)
		if st, err := e.svc.Wheel(ctx, u.ID); err != nil || st.Orders != 1 || st.Reason != "" {
			t.Fatalf("wheel state after a real order: %+v %v", st, err)
		}
	})

	t.Run("resellers' targets", func(t *testing.T) {
		plan := &store.Plan{}
		reseller := &store.User{Role: "reseller", Status: "active"}
		o := &store.Order{Type: OrderNew, Amount: 1000}
		if !countsAsSale(o, plan, reseller) {
			t.Fatal("a reseller's new paid order does not count: the next check proves nothing")
		}
		o.Internal = true
		if countsAsSale(o, plan, reseller) {
			t.Error("an internal order counts as a sale for the targets")
		}
	})
}

// The reseller target count (store) leaves an internal order out as well.
func TestResellerSalesCountLeavesInternalOrdersOut(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	plan := seedPlan(t, s, false, 1000)
	u := seedUser(t, s, 9850)
	for i := range 2 {
		id := buuid.MustV7().String()
		if _, err := s.DB().Exec(ctx, `INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, internal)
			VALUES ($1, $2, $3, 'new', 'active', 1000, 'IRT', $4, $5)`, id, u.ID, plan.ID, "k-"+id, i == 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.InsertResellerSale(ctx, s.Conn(), id, u.ID, "2026-10"); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.CountResellerSales(ctx, s.Conn(), u.ID, "2026-10"); err != nil || n != 1 {
		t.Errorf("sales counted: %d %v, want 1", n, err)
	}
}

// The Top users board does not count an owner's own accounts as invited people while the switch is on.
func TestTopInvitersLeaveOutInvitedOwnerAccountsWhileTheSwitchIsOn(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	a, b := seedUser(t, s, 9860), seedUser(t, s, 9861)
	friend := seedUser(t, s, 9862)
	test := staffMember(t, s, 9863, "owner")
	for _, u := range []*store.User{friend, test} {
		if _, err := s.DB().Exec(ctx, `UPDATE core.users SET referred_by = $2 WHERE id = $1`, u.ID, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	other := seedUser(t, s, 9864)
	if _, err := s.DB().Exec(ctx, `UPDATE core.users SET referred_by = $2 WHERE id = $1`, other.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	board := func() map[string]int64 {
		t.Helper()
		rows, err := s.TopInviters(ctx, s.Conn(), 10)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int64{}
		for _, r := range rows {
			out[r.UserID] = r.Invited
		}
		return out
	}
	if got := board(); got[a.ID] != 2 || got[b.ID] != 1 {
		t.Fatalf("switch off: %v", got)
	}
	setSetting(t, s, SettingExcludeOwnerOrders, "true")
	if got := board(); got[a.ID] != 1 || got[b.ID] != 1 {
		t.Fatalf("switch on: %v", got)
	}
}

// (7) The daily summary says what owner accounts bought in one separate line, only when there was any, and does not count it in the figures.
func TestDailySummaryTellsInternalPurchasesApart(t *testing.T) {
	w := newSummaryWorld(t, "UTC", "")
	owner(t, w.svc)
	w.on("2026-10-01 08:00")
	cust, ownerAcc, plan := w.customer(9901), w.customer(9902), w.plan("IRT")
	day := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	m := &ledgerModel{loc: w.loc}
	m.paid(w, cust, plan, "IRT", 1000, day)
	ownerOrder := m.paid(w, ownerAcc, plan, "IRT", 4000, day.Add(time.Hour))
	w.ledger(ownerAcc, "IRT", -2000, "license_purchase", "vendor_order", buuid.MustV7().String(), "p-owner-lic", day.Add(2*time.Hour))
	w.exec(`UPDATE core.orders SET internal = true WHERE id = $1`, ownerOrder)
	w.exec(`UPDATE core.ledger_entries SET internal = true WHERE user_id = $1`, ownerAcc)

	w.tick("2026-10-14 09:00")
	got := w.sent()
	if len(got) != 1 {
		t.Fatalf("events: %d", len(got))
	}
	e := got[0]
	if len(e.Currencies) != 1 || e.Currencies[0].Revenue != 1000 || e.Currencies[0].Orders != 1 {
		t.Errorf("the figures count the owner's purchases: %+v", e.Currencies)
	}
	if len(e.Internal) != 1 || e.Internal[0] != (events.DailySummaryInternal{Currency: "IRT", Count: 2, Amount: 6000}) {
		t.Errorf("the internal line: %+v", e.Internal)
	}

	// A day without any: no line.
	w.tick("2026-10-15 09:00")
	if sent := w.sent(); len(sent) != 2 || len(sent[1].Internal) != 0 {
		t.Errorf("a day without internal purchases: %+v", sent)
	}
}
