package store

import (
	"reflect"
	"testing"
)

// Internal rows (internal.go) are out of every figure of the reports and still in the export, flagged. The reports are checked against the
// model of reports_test.go with the internal rows taken out of the model.

// flagInternal marks the purchases (with their refunds, which carry the order's id) and a top-up of user n at the given instant as internal in
// the database and takes them out of the model, the way a report must read them.
func (x *seed) flagInternal(purchases []*mPurchase, topupUser int, topupAt string, topupAmt int64) {
	x.t.Helper()
	ids := make([]string, 0, len(purchases))
	drop := map[*mPurchase]bool{}
	for _, p := range purchases {
		ids = append(ids, p.order)
		drop[p] = true
		if p.refunded > 0 { // its refund leaves with it
			for i, f := range x.m.refunds {
				if f.cur == p.cur && f.amt == p.refunded {
					x.m.refunds = append(x.m.refunds[:i], x.m.refunds[i+1:]...)
					break
				}
			}
		}
	}
	x.exec(`UPDATE core.orders SET internal = true WHERE id = ANY($1::uuid[])`, ids)
	x.exec(`UPDATE core.ledger_entries SET internal = true WHERE ref_type = 'order' AND ref_id = ANY($1::uuid[])`, ids)
	kept := x.m.purchases[:0:0]
	for _, p := range x.m.purchases {
		if !drop[p] {
			kept = append(kept, p)
		}
	}
	x.m.purchases = kept
	if topupAmt > 0 {
		at := utc(topupAt)
		x.exec(`UPDATE core.ledger_entries SET internal = true WHERE kind = 'topup' AND user_id = $1 AND created_at = $2`, x.user(topupUser), at)
		for i, f := range x.m.topups {
			if f.amt == topupAmt && f.at.Equal(at) {
				x.m.topups = append(x.m.topups[:i], x.m.topups[i+1:]...)
				break
			}
		}
	}
}

func TestInternalRowsAreOutOfTheReportsAndInTheExport(t *testing.T) {
	x, _ := reportScenario(t)
	// user 1's refunded renewal, and user 2's order with the pay-in that bought it
	flagged := []*mPurchase{x.m.purchases[2], x.m.purchases[3]}
	exported := map[string]bool{flagged[0].order: true, flagged[1].order: true}
	x.flagInternal(flagged, 2, "2026-10-31T20:29:59Z", 250_000)

	for _, tz := range []string{"Asia/Tehran", "America/New_York", "UTC"} {
		for _, rg := range [][2]string{{"2026-10-30", "2026-11-03"}, {"2026-10-01", "2026-11-30"}} {
			for _, group := range []string{"day", "month"} {
				got, err := x.s.SalesSummary(x.ctx, ReportQuery{From: rg[0], To: rg[1], TZ: tz, Group: group})
				if err != nil {
					t.Fatal(err)
				}
				if want := x.expect(tz, rg[0], rg[1], group); !reflect.DeepEqual(got.flat(), want) {
					t.Errorf("%s %s to %s by %s\n got: %+v\nwant: %+v", tz, rg[0], rg[1], group, got.flat(), want)
				}
			}
		}
	}

	q := ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "Asia/Tehran"}
	// How they were paid: the events of the internal orders are not counted either, so nothing is left "unknown".
	methods, err := x.s.SalesByMethod(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var orders, revenue int64
	for _, m := range methods {
		if m.Method == "unknown" {
			t.Errorf("an internal order left a gap: %+v", m)
		}
		if m.Currency == "IRT" {
			orders += m.Orders
			revenue += m.Revenue
		}
	}
	var wantOrders, wantRevenue int64
	for _, p := range x.m.purchases {
		if p.cur == "IRT" && p.at.After(utc("2026-09-30T00:00:00Z")) {
			wantOrders++
			wantRevenue += p.amt
		}
	}
	if orders != wantOrders || revenue != wantRevenue {
		t.Errorf("methods IRT: %d orders %d, want %d and %d", orders, revenue, wantOrders, wantRevenue)
	}
	// The plans, their series and the hours.
	plans, err := x.s.TopPlans(x.ctx, q, 20)
	if err != nil {
		t.Fatal(err)
	}
	var planOrders, planRevenue int64
	for _, p := range plans {
		if p.Currency == "IRT" {
			planOrders += p.Orders
			planRevenue += p.Revenue
		}
	}
	if planOrders != wantOrders || planRevenue != wantRevenue {
		t.Errorf("top plans: %d orders %d, want %d and %d", planOrders, planRevenue, wantOrders, wantRevenue)
	}
	series, err := x.s.PlanSeries(x.ctx, ReportQuery{From: q.From, To: q.To, TZ: q.TZ, Group: "month"})
	if err != nil {
		t.Fatal(err)
	}
	var seriesOrders int64
	for _, r := range series {
		if r.Currency == "IRT" {
			seriesOrders += r.Orders
		}
	}
	hours, err := x.s.SalesHours(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var hourOrders int64
	for _, h := range hours {
		if h.Currency == "IRT" {
			hourOrders += h.Orders
		}
	}
	if seriesOrders != wantOrders || hourOrders != wantOrders {
		t.Errorf("plan series %d, hours %d orders, want %d", seriesOrders, hourOrders, wantOrders)
	}

	// The export lists every paid order, the internal ones flagged.
	var rows []PaidOrderRow
	if _, err := x.s.EachPaidOrder(x.ctx, ReportQuery{From: "2026-10-30", To: "2026-11-03", TZ: "Asia/Tehran"}, 100, func(r PaidOrderRow) error { rows = append(rows, r); return nil }); err != nil {
		t.Fatal(err)
	}
	var flaggedRows, plainRows int
	for _, r := range rows {
		if r.Internal != exported[r.OrderID] {
			t.Errorf("order %s: internal %v", r.OrderID, r.Internal)
		}
		if r.Internal {
			flaggedRows++
		} else {
			plainRows++
		}
	}
	if flaggedRows != 2 || plainRows == 0 {
		t.Errorf("the export: %d internal rows and %d others", flaggedRows, plainRows)
	}

	// The card: the two purchases, the one refund, the two owners... here user 1 and user 2.
	in, err := x.s.InternalOrders(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if in.Orders != 2 || len(in.Currencies) != 1 || in.Currencies[0] != (InternalCurrency{Currency: "IRT", Orders: 2, Amount: 350_000, Refunds: 1, RefundsAmount: 100_000}) ||
		len(in.Accounts) != 2 {
		t.Errorf("internal orders: %+v", in)
	}
	for _, a := range in.Accounts {
		if a.Orders != 1 || len(a.Currencies) != 1 || a.TelegramID == 0 {
			t.Errorf("account: %+v", a)
		}
	}
	// A range without any: an empty card, not an error.
	if none, err := x.s.InternalOrders(x.ctx, ReportQuery{From: "2026-01-01", To: "2026-01-31", TZ: "UTC"}); err != nil || none.Orders != 0 || len(none.Accounts) != 0 || none.Currencies == nil || none.Accounts == nil {
		t.Errorf("an empty range: %+v %v", none, err)
	}
}

// A service made by an internal order is no churn: the owner's own test services that ended are not lost customers.
func TestChurnLeavesInternalOrdersOut(t *testing.T) {
	c := churnScenario(t)
	q := ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "UTC", Group: "month", ChurnAsOf: churnNow, ChurnGraceDays: 7}
	before, err := c.s.SalesSummary(c.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// Customer 20's only service (churn: a service and a customer in October) and customer 23's (at risk in November).
	c.exec(`UPDATE core.orders SET internal = true WHERE user_id = ANY($1::uuid[])`, []string{c.user(20), c.user(23)})
	after, err := c.s.SalesSummary(c.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if after.ChurnedServices["2026-10-01"] != before.ChurnedServices["2026-10-01"]-1 || after.ChurnedCustomers["2026-10-01"] != before.ChurnedCustomers["2026-10-01"]-1 ||
		after.AtRisk["2026-11-01"] != before.AtRisk["2026-11-01"]-1 {
		t.Errorf("churn before %v %v %v, after %v %v %v", before.ChurnedServices, before.ChurnedCustomers, before.AtRisk,
			after.ChurnedServices, after.ChurnedCustomers, after.AtRisk)
	}
}
