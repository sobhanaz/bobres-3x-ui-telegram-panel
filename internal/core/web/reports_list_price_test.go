package web

import (
	"testing"
)

// orders.csv keeps, for an order listed in USDT and paid from the Toman wallet, the USDT price it was listed at and the rate, after the columns that were
// always there (and after the Jalali column of a Jalali file); amount and currency are what was charged. Other orders have those cells empty.
func TestReportsOrdersCSVKeepsTheListPrice(t *testing.T) {
	f := newFixture(t)
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	if code := b.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	r := newReportSeed(t, f)
	ali := r.user(8201, "ali")
	usdt := r.plan("تتر", "Pro USDT", "USDT")
	listed := r.paid(ali, usdt, "IRT", 1_176_000, "2026-11-02T10:00:00Z", "wallet")
	r.exec(`UPDATE core.orders SET list_amount = 4000000, list_currency = 'USDT', rate = 294000 WHERE id = $1`, listed)
	crypto := r.paidWith(ali, usdt, "USDT", 2_500_000, "2026-11-02T11:00:00Z", "gateway", "usdt_trc20_auto")

	for _, c := range []struct {
		calendar string
		cols     int
		first    int // the index of list_amount
	}{{"", 17, 14}, {"&calendar=jalali", 18, 15}} {
		code, _, body := b.get("/api/v1/reports/orders.csv" + reportQuery + c.calendar)
		if code != 200 {
			t.Fatalf("orders.csv%s: %d %s", c.calendar, code, body)
		}
		rows := parseCSV(t, body)
		head := rows[0]
		if len(head) != c.cols || head[c.first] != "list_amount" || head[c.first+1] != "list_currency" || head[c.first+2] != "rate" || head[13] != "tz" {
			t.Fatalf("header%s: %v", c.calendar, head)
		}
		byID := map[string][]string{}
		for _, row := range rows[1:] {
			if len(row) != len(head) {
				t.Fatalf("row%s has %d cells, the header %d: %v", c.calendar, len(row), len(head), row)
			}
			byID[row[2]] = row
		}
		l, p := byID[listed], byID[crypto]
		if l == nil || p == nil {
			t.Fatalf("rows%s: %v", c.calendar, rows)
		}
		if l[8] != "1176000" || l[9] != "IRT" || l[10] != "wallet" || l[c.first] != "4.000000" || l[c.first+1] != "USDT" || l[c.first+2] != "294000" {
			t.Errorf("the order paid from the wallet%s: %v", c.calendar, l)
		}
		if p[8] != "2.500000" || p[9] != "USDT" || p[c.first] != "" || p[c.first+1] != "" || p[c.first+2] != "" {
			t.Errorf("the order paid by crypto%s: %v", c.calendar, p)
		}
	}
}
