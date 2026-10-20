package domain

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// buyWithWallet tops a customer's wallet up with a payment and buys a plan
// from it, the way the bot does.
func buyWithWallet(t *testing.T, svc *Service, s *store.Store, tg int64, p *store.Plan, n int) {
	t.Helper()
	ctx := context.Background()
	u := seedUser(t, s, tg)
	ev := events.PaymentEvent{IntentID: fmt.Sprintf("00000000-0000-7000-8000-%012d", 5000+n), UserID: u.ID, Provider: "manual_card", Amount: p.Price, Currency: p.Currency}
	if err := svc.HandlePaymentEvent(ctx, payEvent(t, fmt.Sprintf("series-%d", n), events.PaymentsPaymentSucceeded, ev)); err != nil {
		t.Fatal(err)
	}
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: fmt.Sprintf("series-order-%d", n)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID); err != nil {
		t.Fatal(err)
	}
}

// Whatever the group and the calendar, the plan series are cut into the
// report's own buckets and add up to the summary's revenue in each of them.
func TestSalesByPlanAddsUpToTheSummaryInEveryBucket(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	basic, pro := seedPlan(t, s, false, 700), seedPlan(t, s, false, 1500)
	usdt := &store.Plan{NameI18n: map[string]string{"en": "Pro USDT"}, Kind: "both", Price: 5_000_000, Currency: "USDT", Enabled: true}
	if err := s.UpsertPlan(ctx, s.Conn(), usdt); err != nil {
		t.Fatal(err)
	}
	for i, p := range []*store.Plan{basic, basic, pro, usdt} {
		buyWithWallet(t, svc, s, int64(6100+i), p, i)
	}
	for _, calendar := range []string{CalendarGregorian, CalendarJalali} {
		for _, group := range []string{GroupDay, GroupWeek, GroupMonth} {
			t.Run(calendar+" by "+group, func(t *testing.T) {
				rng, err := svc.ReportRange(ctx, ReportRequest{Range: "last_30_days", Group: group, Calendar: calendar}, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				rep, err := svc.SalesReport(ctx, rng)
				if err != nil {
					t.Fatal(err)
				}
				series, err := svc.SalesByPlan(ctx, rng)
				if err != nil {
					t.Fatal(err)
				}
				if len(series) != 3 {
					t.Fatalf("%d series, want one per plan (3): %+v", len(series), series)
				}
				starts := map[string]bool{}
				for _, b := range rep.Buckets {
					starts[b.Date] = true
				}
				type bc struct{ bucket, cur string }
				revenue, orders := map[bc]int64{}, map[bc]int64{}
				for _, sr := range series {
					var total int64
					last := ""
					for _, p := range sr.Points {
						if !starts[p.Date] {
							t.Errorf("%s: a point on %s, which no bucket of the report starts on", sr.PlanID, p.Date)
						}
						if p.Date <= last {
							t.Errorf("%s: points are not oldest first: %s after %s", sr.PlanID, p.Date, last)
						}
						last = p.Date
						revenue[bc{p.Date, sr.Currency}] += p.Revenue
						orders[bc{p.Date, sr.Currency}] += p.Orders
						total += p.Revenue
					}
					if total != sr.Revenue {
						t.Errorf("%s: points add up to %d, the plan's total says %d", sr.PlanID, total, sr.Revenue)
					}
				}
				for _, b := range rep.Buckets {
					for cur, c := range b.Currencies {
						if got := revenue[bc{b.Date, cur}]; got != c.Revenue {
							t.Errorf("%s %s: plans add up to %d, the summary says %d", b.Date, cur, got, c.Revenue)
						}
						if got := orders[bc{b.Date, cur}]; got != c.Orders {
							t.Errorf("%s %s: plans have %d orders, the summary says %d", b.Date, cur, got, c.Orders)
						}
					}
				}
				// Best first, in the order of the top list.
				top, err := svc.TopPlans(ctx, rng)
				if err != nil {
					t.Fatal(err)
				}
				for i, p := range top {
					if series[i].PlanID != p.PlanID || series[i].Currency != p.Currency || series[i].Revenue != p.Revenue || series[i].Orders != p.Orders {
						t.Errorf("series %d is %+v, the top list has %+v", i, series[i], p)
					}
				}
			})
		}
	}
}

// Sales on both sides of the end of a Jalali month land in the two months, named
// by the Gregorian date each starts on, even though the store cuts the days.
func TestSalesByPlanCutsJalaliMonths(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	plan := seedPlan(t, s, false, 700)
	days := []string{"2026-10-22T12:00:00Z", "2026-10-23T12:00:00Z", "2026-10-30T12:00:00Z"} // 30 Mehr, 1 Aban, 8 Aban 1405
	for i, at := range days {
		// The ledger cannot be updated, so the paid order is written as it would
		// have been on that day: the order and its purchase entry.
		when, _ := time.Parse(time.RFC3339, at)
		u, oid := seedUser(t, s, int64(6300+i)), buuid.MustV7().String()
		if _, err := s.DB().Exec(ctx, `INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, created_at, updated_at)
			VALUES ($1, $2, $3, 'new', 'active', 700, 'IRT', $4, $5, $5)`, oid, u.ID, plan.ID, "k-"+oid, when); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(ctx, `INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at)
			VALUES ($1, $2, 'IRT', -700, 'purchase', 'order', $3, $4, 0, $5)`, buuid.MustV7().String(), u.ID, oid, "purchase:"+oid, when); err != nil {
			t.Fatal(err)
		}
	}
	rng, err := svc.ReportRange(ctx, ReportRequest{From: "2026-10-01", To: "2026-11-15", Group: GroupMonth, Calendar: CalendarJalali, TZ: "UTC"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	series, err := svc.SalesByPlan(ctx, rng)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 || len(series[0].Points) != 2 {
		t.Fatalf("series: %+v", series)
	}
	want := []PlanPoint{{Date: "2026-09-23", Orders: 1, Revenue: 700}, {Date: "2026-10-23", Orders: 2, Revenue: 1400}}
	for i, p := range series[0].Points {
		if p != want[i] {
			t.Errorf("point %d is %+v, want %+v", i, p, want[i])
		}
	}
}

// Beyond the best ReportTopPlans plans of a currency the rest is one row, so
// the answer stays small and still adds up.
func TestSalesByPlanFoldsTheRestIntoOther(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	const plans = ReportTopPlans + 3
	var all int64
	for i := 0; i < plans; i++ {
		price := int64(1000 + 10*i) // distinct revenue: the last three are the cheapest
		all += price
		buyWithWallet(t, svc, s, int64(6200+i), seedPlan(t, s, false, price), 100+i)
	}
	rng, err := svc.ReportRange(ctx, ReportRequest{Range: "last_7_days", Group: GroupWeek}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	series, err := svc.SalesByPlan(ctx, rng)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != ReportTopPlans+1 {
		t.Fatalf("%d series, want the best %d and one for the rest", len(series), ReportTopPlans)
	}
	other := series[len(series)-1]
	if other.PlanID != "" || other.Name != nil || other.Currency != "IRT" || other.Orders != 3 || other.Revenue != 1000+1010+1020 {
		t.Errorf("the rest: %+v", other)
	}
	var total int64
	for _, sr := range series {
		total += sr.Revenue
	}
	if total != all {
		t.Errorf("series add up to %d, sales were %d", total, all)
	}
	if series[0].Revenue != 1000+10*int64(plans-1) {
		t.Errorf("the best plan sold %d", series[0].Revenue)
	}
	if len(other.Points) == 0 || other.Points[0].Revenue != other.Revenue {
		t.Errorf("the rest has no points: %+v", other.Points)
	}
}

// Nothing sold is an empty list, never nil (the dashboard reads the array).
func TestSalesByPlanAndHoursOfAnEmptyStore(t *testing.T) {
	svc, _ := testService(t)
	ctx := context.Background()
	rng, err := svc.ReportRange(ctx, ReportRequest{Range: "today"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	series, err := svc.SalesByPlan(ctx, rng)
	if err != nil || series == nil || len(series) != 0 {
		t.Errorf("plan series: %v %v", series, err)
	}
	hours, err := svc.SalesHours(ctx, rng)
	if err != nil || len(hours) != 0 {
		t.Errorf("hours: %v %v", hours, err)
	}
}
