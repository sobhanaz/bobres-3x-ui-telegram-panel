package store

import (
	"reflect"
	"testing"
	"time"
)

// The plan series and the hours of the week are checked against the same model
// as the summary (reports_test.go): every purchase is recorded in plain Go
// values with its instant, and the expected rows are computed from those
// values with Go's own time zone arithmetic.

type planKey struct {
	bucket, plan, cur string
}

type planNums struct{ orders, revenue int64 }

func TestPlanSeriesMatchesTheModel(t *testing.T) {
	x, pl := reportScenario(t)
	for _, tz := range []string{"Asia/Tehran", "America/New_York", "UTC"} {
		loc := mustLoc(t, tz)
		for _, rg := range [][2]string{{"2026-10-30", "2026-11-03"}, {"2026-10-01", "2026-11-30"}, {"2026-11-04", "2026-11-04"}} {
			for _, group := range []string{"day", "week", "month"} {
				t.Run(tz+" "+rg[0]+" to "+rg[1]+" by "+group, func(t *testing.T) {
					rows, err := x.s.PlanSeries(x.ctx, ReportQuery{From: rg[0], To: rg[1], TZ: tz, Group: group})
					if err != nil {
						t.Fatal(err)
					}
					got := map[planKey]planNums{}
					for _, r := range rows {
						k := planKey{r.Bucket, r.PlanID, r.Currency}
						if _, dup := got[k]; dup {
							t.Errorf("%v listed twice", k)
						}
						got[k] = planNums{r.Orders, r.Revenue}
					}
					want := map[planKey]planNums{}
					for _, p := range x.m.purchases {
						if !inRange(p.at, loc, rg[0], rg[1]) {
							continue
						}
						k := planKey{bucketKey(p.at, loc, group), p.plan, p.cur}
						n := want[k]
						n.orders++
						n.revenue += p.amt
						want[k] = n
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("plan series differ\n got: %+v\nwant: %+v", got, want)
					}
				})
			}
		}
	}

	// A plan's rows add up to its row in the top plans, with its name.
	q := ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "Asia/Tehran", Group: "week"}
	rows, err := x.s.PlanSeries(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	top, err := x.s.TopPlans(x.ctx, q, 20)
	if err != nil {
		t.Fatal(err)
	}
	sum := map[planKey]planNums{}
	names := map[string]map[string]string{}
	for _, r := range rows {
		k := planKey{plan: r.PlanID, cur: r.Currency}
		n := sum[k]
		n.orders += r.Orders
		n.revenue += r.Revenue
		sum[k] = n
		names[r.PlanID] = r.Name
	}
	if len(sum) != len(top) {
		t.Fatalf("%d plans in the series, %d in the top list", len(sum), len(top))
	}
	for _, p := range top {
		if got := sum[planKey{plan: p.PlanID, cur: p.Currency}]; got != (planNums{p.Orders, p.Revenue}) {
			t.Errorf("plan %s %s: series add up to %+v, top list says %d orders %d", p.PlanID, p.Currency, got, p.Orders, p.Revenue)
		}
	}
	if n := names[pl["pro"].id]; n["en"] != "Pro" || n["fa"] != "ویژه ⭐" {
		t.Errorf("name: %v", n)
	}
}

// A range with no sale lists nothing (an empty answer, not an error).
func TestPlanSeriesAndHoursOfAnEmptyRange(t *testing.T) {
	x, _ := reportScenario(t)
	q := ReportQuery{From: "2026-01-01", To: "2026-01-31", TZ: "Asia/Tehran", Group: "day"}
	rows, err := x.s.PlanSeries(x.ctx, q)
	if err != nil || len(rows) != 0 {
		t.Errorf("plan series: %v %v", rows, err)
	}
	hours, err := x.s.SalesHours(x.ctx, q)
	if err != nil || len(hours) != 0 {
		t.Errorf("hours: %v %v", hours, err)
	}
}

func TestSalesHoursMatchesTheModel(t *testing.T) {
	x, _ := reportScenario(t)
	type hk struct {
		weekday, hour int
		cur           string
	}
	for _, tz := range []string{"Asia/Tehran", "America/New_York", "UTC"} {
		loc := mustLoc(t, tz)
		for _, rg := range [][2]string{{"2026-10-30", "2026-11-03"}, {"2026-10-01", "2026-11-30"}} {
			t.Run(tz+" "+rg[0]+" to "+rg[1], func(t *testing.T) {
				rows, err := x.s.SalesHours(x.ctx, ReportQuery{From: rg[0], To: rg[1], TZ: tz})
				if err != nil {
					t.Fatal(err)
				}
				got := map[hk]planNums{}
				for _, r := range rows {
					k := hk{r.Weekday, r.Hour, r.Currency}
					if _, dup := got[k]; dup {
						t.Errorf("%v listed twice", k)
					}
					got[k] = planNums{r.Orders, r.Revenue}
				}
				want := map[hk]planNums{}
				for _, p := range x.m.purchases {
					if !inRange(p.at, loc, rg[0], rg[1]) {
						continue
					}
					l := p.at.In(loc)
					wd := int(l.Weekday()) // Sunday is 0; ISO says 7
					if wd == 0 {
						wd = 7
					}
					k := hk{wd, l.Hour(), p.cur}
					n := want[k]
					n.orders++
					n.revenue += p.amt
					want[k] = n
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("hours differ\n got: %+v\nwant: %+v", got, want)
				}
			})
		}
	}
}

// The seconds on either side of local midnight fall on the last hour of one
// weekday and the first of the next, in the zone asked for.
func TestSalesHoursSplitAtLocalMidnight(t *testing.T) {
	x := newSeed(t)
	pl := x.plan("پایه", "Basic", "IRT", false)
	// Saturday 31 Oct 2026 23:59:59 and Sunday 1 Nov 00:00:00 in Tehran (UTC+3:30, no daylight saving).
	x.paid(1, pl, "new", 1000, time.Date(2026, 10, 31, 20, 29, 59, 0, time.UTC), "wallet")
	x.paid(2, pl, "new", 2000, time.Date(2026, 10, 31, 20, 30, 0, 0, time.UTC), "wallet")
	rows, err := x.s.SalesHours(x.ctx, ReportQuery{From: "2026-10-31", To: "2026-11-01", TZ: "Asia/Tehran"})
	if err != nil {
		t.Fatal(err)
	}
	want := []HourRow{
		{Weekday: 6, Hour: 23, Currency: "IRT", Orders: 1, Revenue: 1000},
		{Weekday: 7, Hour: 0, Currency: "IRT", Orders: 1, Revenue: 2000},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("hours\n got: %+v\nwant: %+v", rows, want)
	}
}

func TestPlanSeriesRejectsAnUnknownTimezone(t *testing.T) {
	x := newSeed(t)
	if _, err := x.s.PlanSeries(x.ctx, ReportQuery{From: "2026-10-01", To: "2026-10-02", TZ: "Mars/Olympus", Group: "day"}); err == nil {
		t.Error("plan series accepted an unknown time zone")
	}
	if _, err := x.s.SalesHours(x.ctx, ReportQuery{From: "2026-10-01", To: "2026-10-02", TZ: "Mars/Olympus"}); err == nil {
		t.Error("hours accepted an unknown time zone")
	}
}
