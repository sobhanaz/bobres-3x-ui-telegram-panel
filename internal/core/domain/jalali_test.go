package domain

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// The reference calendar of these tests does not use i18n.GregorianToJalali: it
// walks the Persian calendar month by month from a known Nowruz with the
// 33-year leap rule ((25*year+11) mod 33 < 8, the one ICU and so every browser
// uses), and so is an independent check on the conversion the reports rely on.

func refLeap(jy int) bool { return (25*jy+11)%33 < 8 }

func refMonthLen(jy, jm int) int {
	switch {
	case jm <= 6:
		return 31
	case jm <= 11:
		return 30
	case refLeap(jy):
		return 30
	}
	return 29
}

type refMonth struct {
	jy, jm int
	start  time.Time
	days   int
}

// refMonths lists every Jalali month of 1399 to 1479 SH, from 1 Farvardin 1399
// = 20 March 2020 (checked against Intl in the dashboard's reports test too).
func refMonths() []refMonth {
	var out []refMonth
	d := time.Date(2020, 3, 20, 0, 0, 0, 0, time.UTC)
	for jy := 1399; jy <= 1479; jy++ {
		for jm := 1; jm <= 12; jm++ {
			n := refMonthLen(jy, jm)
			out = append(out, refMonth{jy, jm, d, n})
			d = d.AddDate(0, 0, n)
		}
	}
	return out
}

func day(s string) time.Time {
	d, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return d
}

// Nowruz, 1 Farvardin, from calendars the tests do not compute: leap years are
// 1403, 1408, 1412, 1416 and 1420 (their Esfand has 30 days).
func TestJalaliNowruzAnchors(t *testing.T) {
	want := map[int]string{1399: "2020-03-20", 1403: "2024-03-20", 1404: "2025-03-21", 1405: "2026-03-21", 1406: "2027-03-21",
		1407: "2028-03-20", 1408: "2029-03-20", 1412: "2033-03-20", 1416: "2037-03-20", 1420: "2041-03-20", 1479: "2100-03-21"}
	starts := map[int]time.Time{}
	for _, m := range refMonths() {
		if m.jm == 1 {
			starts[m.jy] = m.start
		}
	}
	for jy, date := range want {
		if got := starts[jy].Format(dateLayout); got != date {
			t.Errorf("the reference calendar puts 1 Farvardin %d on %s, want %s", jy, got, date)
		}
		// and the code's month of that day starts on it, and the day before belongs to the previous month
		if got := bucketStart(day(date), GroupMonth, CalendarJalali); !got.Equal(day(date)) {
			t.Errorf("1 Farvardin %d: the month starts on %s", jy, got.Format(dateLayout))
		}
		if got := nextBucket(bucketStart(day(date).AddDate(0, 0, -1), GroupMonth, CalendarJalali), GroupMonth, CalendarJalali); !got.Equal(day(date)) {
			t.Errorf("Esfand %d ends before %s, not before %s", jy-1, date, got.Format(dateLayout))
		}
	}
	for _, jy := range []int{1403, 1408, 1412, 1416, 1420} {
		if refMonthLen(jy, 12) != 30 {
			t.Errorf("Esfand %d has %d days", jy, refMonthLen(jy, 12))
		}
	}
	for _, jy := range []int{1404, 1405, 1406, 1407, 1409} {
		if refMonthLen(jy, 12) != 29 {
			t.Errorf("Esfand %d has %d days", jy, refMonthLen(jy, 12))
		}
	}
}

// Every Jalali month of 1399 to 1479: its first day starts it, its last day is
// in it, the day after is the next one, and every day in between agrees.
func TestJalaliMonthBoundariesAgreeWithTheCalendar(t *testing.T) {
	months := refMonths()
	if got := months[6*12].start.Format(dateLayout); got != "2026-03-21" {
		t.Fatalf("1 Farvardin 1405 is %s, want 2026-03-21", got)
	}
	for i, m := range months {
		name := fmt.Sprintf("%d-%02d", m.jy, m.jm)
		next := m.start.AddDate(0, 0, m.days)
		if i+1 < len(months) && !next.Equal(months[i+1].start) {
			t.Fatalf("%s: reference is not continuous", name)
		}
		if got := nextBucket(m.start, GroupMonth, CalendarJalali); !got.Equal(next) {
			t.Errorf("%s: the next month starts %s, want %s", name, got.Format(dateLayout), next.Format(dateLayout))
		}
		for k := 0; k < m.days; k++ {
			d := m.start.AddDate(0, 0, k)
			if got := bucketStart(d, GroupMonth, CalendarJalali); !got.Equal(m.start) {
				t.Errorf("%s day %d (%s): the month starts %s, want %s", name, k+1, d.Format(dateLayout), got.Format(dateLayout), m.start.Format(dateLayout))
			}
		}
		if got := bucketStart(next, GroupMonth, CalendarJalali); !got.Equal(next) {
			t.Errorf("%s: the day after is in month starting %s, not itself", name, got.Format(dateLayout))
		}
	}
}

func TestJalaliWeeksStartOnSaturday(t *testing.T) {
	// 2026-10-17 is a Saturday, 2026-10-23 a Friday.
	for _, c := range []struct{ date, start string }{
		{"2026-10-17", "2026-10-17"}, {"2026-10-18", "2026-10-17"}, {"2026-10-21", "2026-10-17"}, {"2026-10-23", "2026-10-17"}, {"2026-10-24", "2026-10-24"},
		{"2026-10-16", "2026-10-10"}, // a Friday: the last day of its week
	} {
		got := bucketStart(day(c.date), GroupWeek, CalendarJalali)
		if got.Format(dateLayout) != c.start || got.Weekday() != time.Saturday {
			t.Errorf("%s: the week starts %s (%s), want %s", c.date, got.Format(dateLayout), got.Weekday(), c.start)
		}
		if n := nextBucket(got, GroupWeek, CalendarJalali); n.Sub(got) != 7*24*time.Hour {
			t.Errorf("%s: the next week starts %s", c.date, n.Format(dateLayout))
		}
	}
	// Gregorian weeks are unchanged: Monday.
	if got := bucketStart(day("2026-10-17"), GroupWeek, CalendarGregorian); got.Format(dateLayout) != "2026-10-12" {
		t.Errorf("Gregorian week of Saturday 17 Oct starts %s", got.Format(dateLayout))
	}
}

// ---- Jalali buckets of a real report ----

// reportSeeder writes rows with explicit times, straight into the tables.
type reportSeeder struct {
	t     *testing.T
	s     *store.Store
	users map[int]string
	subs  map[int]string
	plans map[string]string
}

func (x *reportSeeder) exec(sql string, args ...any) {
	x.t.Helper()
	if _, err := x.s.DB().Exec(context.Background(), sql, args...); err != nil {
		x.t.Fatalf("seed: %v\n%s", err, sql)
	}
}

func (x *reportSeeder) user(n int) string {
	if id, ok := x.users[n]; ok {
		return id
	}
	id := buuid.MustV7().String()
	x.exec(`INSERT INTO core.users (id, telegram_id, username) VALUES ($1, $2, $3)`, id, 7000+n, fmt.Sprintf("u%d", n))
	x.users[n] = id
	return id
}

func (x *reportSeeder) plan(cur string) string {
	if id, ok := x.plans[cur]; ok {
		return id
	}
	id := buuid.MustV7().String()
	x.exec(`INSERT INTO core.plans (id, name_i18n, kind, price, currency) VALUES ($1, '{"en":"P"}', 'both', 1000, $2)`, id, cur)
	x.plans[cur] = id
	return id
}

func (x *reportSeeder) ledger(user, cur string, amt int64, kind, refType, refID string, at time.Time) {
	var rt, ri any
	if refType != "" {
		rt, ri = refType, refID
	}
	id := buuid.MustV7().String()
	x.exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9)`, id, user, cur, amt, kind, rt, ri, "k-"+id, at)
}

func (x *reportSeeder) order(n int, cur, typ, status string, amt int64, at time.Time) string {
	u := x.user(n)
	var sub any
	if typ == "renew" {
		sub = x.subs[n]
	}
	id := buuid.MustV7().String()
	x.exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, subscription_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`, id, u, x.plan(cur), typ, status, amt, cur, "k-"+id, sub, at)
	if typ == "new" && x.subs[n] == "" {
		sid := buuid.MustV7().String()
		x.exec(`INSERT INTO core.subscriptions (id, user_id, order_id, server_id, client_email, status) VALUES ($1, $2, $3, $4, $5, 'active')`,
			sid, u, id, buuid.MustV7().String(), "c-"+sid)
		x.subs[n] = sid
	}
	return id
}

func (x *reportSeeder) paid(n int, cur, typ string, amt int64, at time.Time) string {
	id := x.order(n, cur, typ, "active", amt, at)
	x.ledger(x.user(n), cur, -amt, "purchase", "order", id, at)
	return id
}

// Jalali weeks and months are the store's days added up: for every zone and
// every group, a bucket holds exactly the sum of its days' numbers (money by
// currency, people counts), the buckets are the independent calendar's, and
// the totals are the Gregorian report's of the same range.
func TestSalesReportFoldsDaysIntoJalaliBuckets(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	x := &reportSeeder{t: t, s: s, users: map[int]string{}, subs: map[int]string{}, plans: map[string]string{}}
	at := func(v string) time.Time {
		tm, err := time.Parse(time.RFC3339, v)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	// Moments on both sides of local midnight on the first of Aban 1405 (23 Oct
	// 2026) and of Mehr (23 Sep), and around Saturdays (the Jalali week starts).
	x.paid(1, "IRT", "new", 100, at("2026-09-22T20:29:59Z")) // Tehran 22 Sep 23:59:59: Shahrivar
	x.paid(2, "IRT", "new", 200, at("2026-09-22T20:30:00Z")) // Tehran 23 Sep 00:00:00: Mehr
	x.paid(1, "IRT", "renew", 300, at("2026-10-02T21:00:00Z"))
	x.paid(3, "USDT", "new", 5_000_000, at("2026-10-16T20:29:59Z"))
	x.paid(3, "USDT", "renew", 1_000_000, at("2026-10-16T20:30:00Z"))    // Friday night, Saturday 17 Oct in Tehran
	x.paid(4, "IRT", "new", 400, at("2026-10-22T20:29:59Z"))             // Tehran 22 Oct 23:59:59: still Mehr
	refunded := x.paid(5, "IRT", "new", 500, at("2026-10-22T20:30:00Z")) // 1 Aban in Tehran
	x.ledger(x.user(5), "IRT", 500, "refund", "order", refunded, at("2026-10-30T10:00:00Z"))
	x.exec(`UPDATE core.orders SET refunded_at = $2, refund_amount = 500, status = 'cancelled' WHERE id = $1`, refunded, at("2026-10-30T10:00:00Z"))
	x.ledger(x.user(6), "IRT", 900, "topup", "intent", buuid.MustV7().String(), at("2026-10-03T04:00:00Z"))
	x.ledger(x.user(7), "IRT", 0, "trial_grant", "order", x.order(7, "IRT", "new", "paid", 0, at("2026-10-24T01:00:00Z")), at("2026-10-24T01:00:00Z"))
	x.order(8, "IRT", "new", "cancelled", 650, at("2026-11-05T12:00:00Z"))
	// Churn: the services' expiries sit around the same midnights (the report is read as of 1 Dec 2026, so
	// the grace of 7 days ends on 24 Nov). Four have churned; user 5's ended inside the grace period.
	for n, exp := range map[int]string{
		1: "2026-09-22T20:29:59Z", // Tehran 22 Sep 23:59:59: Shahrivar
		2: "2026-09-22T20:30:00Z", // Mehr 1
		3: "2026-10-16T20:30:00Z", // Saturday 17 Oct in Tehran
		4: "2026-10-22T20:30:00Z", // Aban 1
		5: "2026-11-28T10:00:00Z", // at risk
	} {
		x.exec(`UPDATE core.subscriptions SET expires_at = $2, status = 'expired' WHERE id = $1`, x.subs[n], at(exp))
	}
	x.exec(`INSERT INTO core.outbox_core (topic, payload, event_id, created_at) VALUES ('payment.rejected.v1', $1::text::jsonb, $2, $3)`,
		`{"user_id":"`+x.user(9)+`","provider":"manual_card","amount":777,"currency":"IRT","reason":"blurry"}`, buuid.MustV7().String(), at("2026-11-08T08:00:00Z"))

	// refDay says which Jalali bucket a day belongs to by the independent calendar.
	months := refMonths()
	refStart := func(d time.Time, group string) string {
		if group == GroupWeek {
			for d.Weekday() != time.Saturday {
				d = d.AddDate(0, 0, -1)
			}
			return d.Format(dateLayout)
		}
		for i := len(months) - 1; i >= 0; i-- {
			if !months[i].start.After(d) {
				return months[i].start.Format(dateLayout)
			}
		}
		t.Fatalf("no month for %s", d)
		return ""
	}
	for _, tz := range []string{"Asia/Tehran", "America/New_York", "UTC", "Pacific/Auckland"} {
		for _, group := range []string{GroupWeek, GroupMonth} {
			t.Run(tz+" by "+group, func(t *testing.T) {
				req := ReportRequest{From: "2026-08-20", To: "2026-11-30", TZ: tz}
				req.Group = GroupDay
				days, err := svc.SalesReport(ctx, mustRange(t, svc, req, ""))
				if err != nil {
					t.Fatal(err)
				}
				req.Group = group
				greg, err := svc.SalesReport(ctx, mustRange(t, svc, req, CalendarGregorian))
				if err != nil {
					t.Fatal(err)
				}
				jal, err := svc.SalesReport(ctx, mustRange(t, svc, req, CalendarJalali))
				if err != nil {
					t.Fatal(err)
				}
				if jal.Range.Calendar != CalendarJalali || jal.Range.Group != group {
					t.Fatalf("range %+v", jal.Range)
				}
				// The model: the day rows, summed by the independent calendar.
				type sum struct {
					cells     map[string]*store.ReportCell
					customers int64
					trials    int64
					churned   int64
					people    int64
					risk      int64
				}
				want := map[string]*sum{}
				for _, b := range days.Buckets {
					k := refStart(day(b.Date), group)
					if want[k] == nil {
						want[k] = &sum{cells: map[string]*store.ReportCell{}}
					}
					want[k].customers += b.NewCustomers
					want[k].trials += b.Trials
					want[k].churned += b.ChurnedServices
					want[k].people += b.ChurnedCustomers
					want[k].risk += b.AtRisk
					for cur, c := range b.Currencies {
						if want[k].cells[cur] == nil {
							want[k].cells[cur] = &store.ReportCell{}
						}
						addCell(want[k].cells[cur], c)
					}
				}
				var got []string
				for _, b := range jal.Buckets {
					got = append(got, b.Date)
					w := want[b.Date]
					if w == nil {
						t.Fatalf("bucket %s is not a Jalali %s start", b.Date, group)
					}
					if b.NewCustomers != w.customers || b.Trials != w.trials {
						t.Errorf("%s: customers %d trials %d, want %d and %d", b.Date, b.NewCustomers, b.Trials, w.customers, w.trials)
					}
					if b.ChurnedServices != w.churned || b.ChurnedCustomers != w.people || b.AtRisk != w.risk {
						t.Errorf("%s: churn %d services %d customers %d at risk, want %d, %d and %d", b.Date,
							b.ChurnedServices, b.ChurnedCustomers, b.AtRisk, w.churned, w.people, w.risk)
					}
					for cur, c := range b.Currencies {
						if w.cells[cur] == nil {
							w.cells[cur] = &store.ReportCell{}
						}
						if *c != *w.cells[cur] {
							t.Errorf("%s %s: %+v, want %+v", b.Date, cur, *c, *w.cells[cur])
						}
					}
				}
				if len(got) != len(want) {
					t.Errorf("%d buckets %v, want %d", len(got), got, len(want))
				}
				for i := 1; i < len(got); i++ {
					if got[i] <= got[i-1] {
						t.Errorf("buckets out of order: %v", got)
					}
				}
				// Nothing is lost or counted twice: the totals are the Gregorian report's.
				if !reflect.DeepEqual(jal.Currencies, greg.Currencies) || jal.Totals.NewCustomers != greg.Totals.NewCustomers || jal.Totals.Trials != greg.Totals.Trials {
					t.Errorf("totals: %v %d %d, Gregorian %v %d %d", jal.Currencies, jal.Totals.NewCustomers, jal.Totals.Trials,
						greg.Currencies, greg.Totals.NewCustomers, greg.Totals.Trials)
				}
				if jal.Totals.ChurnedServices != greg.Totals.ChurnedServices || jal.Totals.ChurnedCustomers != greg.Totals.ChurnedCustomers ||
					jal.Totals.AtRisk != greg.Totals.AtRisk {
					t.Errorf("churn totals %+v, Gregorian %+v", jal.Totals, greg.Totals)
				}
				if jal.Totals.ChurnedServices != 4 || jal.Totals.ChurnedCustomers != 4 || jal.Totals.AtRisk != 1 {
					t.Errorf("the seeded churn is not all there: %d services %d customers %d at risk", jal.Totals.ChurnedServices,
						jal.Totals.ChurnedCustomers, jal.Totals.AtRisk)
				}
				for cur, c := range greg.Totals.Currencies {
					if *jal.Totals.Currencies[cur] != *c {
						t.Errorf("total %s: %+v, Gregorian %+v", cur, *jal.Totals.Currencies[cur], *c)
					}
				}
				if jal.Totals.Currencies["IRT"].Revenue != 100+200+300+400+500 || jal.Totals.NewCustomers != 5 || jal.Totals.Trials != 1 {
					t.Errorf("the seeded numbers are not all there: %+v customers %d trials %d", jal.Totals.Currencies["IRT"], jal.Totals.NewCustomers, jal.Totals.Trials)
				}
			})
		}
	}

	// In Tehran the two payments around midnight fall in different Jalali months,
	// in New York (UTC-4) they fall on the same day.
	rep, err := svc.SalesReport(ctx, mustRange(t, svc, ReportRequest{From: "2026-08-23", To: "2026-10-22", Group: GroupMonth, TZ: "Asia/Tehran"}, CalendarJalali))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Buckets) != 2 || rep.Buckets[0].Date != "2026-08-23" || rep.Buckets[1].Date != "2026-09-23" ||
		rep.Buckets[0].Partial || rep.Buckets[1].Partial {
		t.Fatalf("Shahrivar and Mehr: %+v", rep.Buckets)
	}
	if got := rep.Buckets[0].Currencies["IRT"].Revenue; got != 100 {
		t.Errorf("Shahrivar revenue %d, want 100", got)
	}
	if got := rep.Buckets[1].Currencies["IRT"].Revenue; got != 200+300+400 {
		t.Errorf("Mehr revenue %d, want 900", got)
	}
}

func mustRange(t *testing.T, svc *Service, req ReportRequest, calendar string) *ReportRange {
	t.Helper()
	req.Calendar = calendar
	rng, err := svc.ReportRange(context.Background(), req, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return rng
}

// A row the store returns for a day outside the range is an error, never a
// number folded quietly into the nearest bucket.
func TestFoldDaysRefusesADayOutsideTheRange(t *testing.T) {
	rng, err := NewReportRange(ReportRequest{From: "2026-10-23", To: "2026-10-30", Group: GroupMonth, Calendar: CalendarJalali}, "UTC", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cell := func() map[string]*store.ReportCell { return map[string]*store.ReportCell{"IRT": {Revenue: 1}} }
	for _, c := range []struct {
		name string
		data store.SalesData
	}{
		{"cells before", store.SalesData{Cells: map[string]map[string]*store.ReportCell{"2026-10-22": cell()}}},
		{"cells after", store.SalesData{Cells: map[string]map[string]*store.ReportCell{"2026-10-31": cell()}}},
		{"customers after", store.SalesData{NewCustomers: map[string]int64{"2026-11-01": 1}}},
		{"trials before", store.SalesData{Trials: map[string]int64{"2026-10-01": 1}}},
		{"churned services after", store.SalesData{ChurnedServices: map[string]int64{"2026-11-01": 1}}},
		{"churned customers before", store.SalesData{ChurnedCustomers: map[string]int64{"2026-10-01": 1}}},
		{"at risk after", store.SalesData{AtRisk: map[string]int64{"2026-10-31": 1}}},
		{"not a date", store.SalesData{Trials: map[string]int64{"x": 1}}},
	} {
		if _, err := foldDays(rng, &c.data); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
	got, err := foldDays(rng, &store.SalesData{
		Cells:           map[string]map[string]*store.ReportCell{"2026-10-23": cell(), "2026-10-30": cell()},
		NewCustomers:    map[string]int64{"2026-10-23": 2, "2026-10-25": 3},
		ChurnedServices: map[string]int64{"2026-10-23": 1, "2026-10-30": 4}, ChurnedCustomers: map[string]int64{"2026-10-24": 2},
		AtRisk: map[string]int64{"2026-10-26": 7},
	})
	if err != nil || got.ChurnedServices["2026-10-23"] != 5 || got.ChurnedCustomers["2026-10-23"] != 2 || got.AtRisk["2026-10-23"] != 7 {
		t.Errorf("fold churn: %+v %v", got, err)
	}
	if err != nil || got.Cells["2026-10-23"]["IRT"].Revenue != 2 || got.NewCustomers["2026-10-23"] != 5 || len(got.Cells) != 1 {
		t.Errorf("fold: %+v %v", got, err)
	}
}
