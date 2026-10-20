package web

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// minorUnits reads a cell of money back into the currency's minor units, exactly.
func minorUnits(t *testing.T, cell, currency string) int64 {
	t.Helper()
	if cell == "" {
		return 0
	}
	neg := strings.HasPrefix(cell, "-")
	whole, frac, _ := strings.Cut(strings.TrimPrefix(cell, "-"), ".")
	scale := max(bmoney.Scale(currency), 0)
	if len(frac) != scale {
		t.Fatalf("%q has %d decimals, %s has %d", cell, len(frac), currency, scale)
	}
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		t.Fatalf("%q: %v", cell, err)
	}
	if neg {
		n = -n
	}
	return n
}

// jsonAt reads a number out of a summary cell by a path such as "refunds.amount".
func jsonAt(v map[string]any, path string) int64 {
	cur := any(v)
	for _, key := range strings.Split(path, ".") {
		cur = cur.(map[string]any)[key]
	}
	return int64(cur.(float64))
}

// The file and the page say the same: summed by currency, the rows of
// summary.csv are the report's totals, in every grouping and both calendars, and
// the people rows add up to its people.
func TestSummaryCSVAddsUpToTheReport(t *testing.T) {
	_, b, _ := reportFixture(t)
	columns := map[string]string{
		"revenue": "revenue", "refund_amount": "refunds.amount", "net": "net", "topup_amount": "topups.amount", "rejected_amount": "rejected.amount",
		"gateway_failed_amount": "gateway_failed.amount", "cancelled_amount": "cancelled.amount", "paid_orders": "orders", "new_orders": "new_orders",
		"renewals": "renewals", "traffic_topups": "traffic_topups", "topup_count": "topups.count", "refund_count": "refunds.count",
		"rejected_count": "rejected.count", "gateway_failed_count": "gateway_failed.count", "cancelled_count": "cancelled.count",
	}
	people := map[string]string{"new_customers": "new_customers", "free_trials": "trials", "churned_services": "churned_services",
		"churned_customers": "churned_customers", "at_risk_services": "at_risk"}
	for _, v := range []string{"day", "week", "month", "day&calendar=jalali", "week&calendar=jalali", "month&calendar=jalali"} {
		q := "?from=2026-10-31&to=2026-11-03&tz=Asia/Tehran&group=" + v
		code, out := b.do(http.MethodGet, "/api/v1/reports/summary"+q, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", v, code, out)
		}
		totals := out["totals"].(map[string]any)
		perCurrency := totals["currencies"].(map[string]any)
		_, _, body := b.get("/api/v1/reports/summary.csv" + q)
		rows := parseCSV(t, body)
		col := map[string]int{}
		for i, name := range rows[0] {
			col[name] = i
		}
		sums := map[string]map[string]int64{} // currency -> column -> sum
		peopleSums := map[string]int64{}
		starts := map[string]bool{}
		for _, r := range rows[1:] {
			starts[r[0]] = true
			cur := r[col["currency"]]
			if cur == "" { // the people row of a period
				for name := range people {
					n, err := strconv.ParseInt(r[col[name]], 10, 64)
					if err != nil {
						t.Fatalf("%s: %s of %v: %v", v, name, r, err)
					}
					peopleSums[name] += n
				}
				continue
			}
			if sums[cur] == nil {
				sums[cur] = map[string]int64{}
			}
			for name := range columns {
				cell := r[col[name]]
				if strings.HasSuffix(name, "_amount") || name == "revenue" || name == "net" {
					sums[cur][name] += minorUnits(t, cell, cur)
					continue
				}
				n, err := strconv.ParseInt(cell, 10, 64)
				if err != nil {
					t.Fatalf("%s: %s of %v: %v", v, name, r, err)
				}
				sums[cur][name] += n
			}
		}
		if len(sums) != len(perCurrency) {
			t.Errorf("%s: %d currencies in the file, %d in the report", v, len(sums), len(perCurrency))
		}
		for cur, cell := range perCurrency {
			for name, path := range columns {
				if got, want := sums[cur][name], jsonAt(cell.(map[string]any), path); got != want {
					t.Errorf("%s: %s %s adds up to %d in the file, the report says %d", v, cur, name, got, want)
				}
			}
		}
		for name, path := range people {
			if got, want := peopleSums[name], int64(totals[path].(float64)); got != want {
				t.Errorf("%s: %s adds up to %d in the file, the report says %d", v, name, got, want)
			}
		}
		// one people row and one row per currency for each period of the report
		periods := len(out["buckets"].([]any))
		if want := periods * (1 + len(perCurrency)); len(rows)-1 != want || len(starts) != periods {
			t.Errorf("%s: %d rows for %d periods and %d currencies", v, len(rows)-1, periods, len(perCurrency))
		}
	}
}

// Every paid order of the range is a row of orders.csv, and what they cost is the report's revenue.
func TestOrdersCSVAddsUpToTheRevenue(t *testing.T) {
	_, b, _ := reportFixture(t)
	_, out := b.do(http.MethodGet, "/api/v1/reports/summary"+reportQuery, nil)
	perCurrency := out["totals"].(map[string]any)["currencies"].(map[string]any)
	_, _, body := b.get("/api/v1/reports/orders.csv" + reportQuery)
	rows := parseCSV(t, body)
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	paid := map[string]int64{}
	orders := map[string]int64{}
	for _, r := range rows[1:] {
		cur := r[col["currency"]]
		paid[cur] += minorUnits(t, r[col["amount"]], cur)
		orders[cur]++
	}
	for cur, cell := range perCurrency {
		c := cell.(map[string]any)
		if paid[cur] != jsonAt(c, "revenue") || orders[cur] != jsonAt(c, "orders") {
			t.Errorf("%s: the file has %d orders for %d, the report %d for %d", cur, orders[cur], paid[cur], jsonAt(c, "orders"), jsonAt(c, "revenue"))
		}
	}
	if len(paid) != len(perCurrency) {
		t.Errorf("currencies: %v vs %v", paid, perCurrency)
	}
}
