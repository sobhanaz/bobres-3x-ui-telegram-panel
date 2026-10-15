package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func spans(r *ReportRange) []string {
	var out []string
	for _, b := range r.Buckets {
		s := b.Start
		if b.Partial {
			s += "*"
		}
		out = append(out, s)
	}
	return out
}

func TestReportRangeBucketsAndEdges(t *testing.T) {
	// 14 Oct 2026 is a Wednesday.
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		req  ReportRequest
		from string
		to   string
		want []string // bucket starts, "*" marks a bucket the range cuts
	}{
		{"days", ReportRequest{From: "2026-10-12", To: "2026-10-14"}, "2026-10-12", "2026-10-14", []string{"2026-10-12", "2026-10-13", "2026-10-14"}},
		{"one day", ReportRequest{From: "2026-10-14", To: "2026-10-14"}, "2026-10-14", "2026-10-14", []string{"2026-10-14"}},
		{"weeks start on Monday and the ends are partial", ReportRequest{From: "2026-10-07", To: "2026-10-20", Group: "week"}, "2026-10-07", "2026-10-20",
			[]string{"2026-10-05*", "2026-10-12", "2026-10-19*"}},
		{"a whole week is not partial", ReportRequest{From: "2026-10-05", To: "2026-10-18", Group: "week"}, "2026-10-05", "2026-10-18", []string{"2026-10-05", "2026-10-12"}},
		{"a Sunday belongs to the week before", ReportRequest{From: "2026-10-18", To: "2026-10-18", Group: "week"}, "2026-10-18", "2026-10-18", []string{"2026-10-12*"}},
		{"months", ReportRequest{From: "2026-09-15", To: "2026-11-02", Group: "month"}, "2026-09-15", "2026-11-02", []string{"2026-09-01*", "2026-10-01", "2026-11-01*"}},
		{"whole months", ReportRequest{From: "2026-02-01", To: "2026-03-31", Group: "month"}, "2026-02-01", "2026-03-31", []string{"2026-02-01", "2026-03-01"}},
		{"today", ReportRequest{Range: "today"}, "2026-10-14", "2026-10-14", []string{"2026-10-14"}},
		{"last 7 days", ReportRequest{Range: "last_7_days"}, "2026-10-08", "2026-10-14", nil},
		{"last 30 days", ReportRequest{Range: "last_30_days"}, "2026-09-15", "2026-10-14", nil},
		{"this month", ReportRequest{Range: "this_month", Group: "month"}, "2026-10-01", "2026-10-14", []string{"2026-10-01*"}},
		{"last month", ReportRequest{Range: "last_month", Group: "month"}, "2026-09-01", "2026-09-30", []string{"2026-09-01"}},
		{"nothing asked: the last 30 days", ReportRequest{}, "2026-09-15", "2026-10-14", nil},
		{"leap day", ReportRequest{From: "2028-02-28", To: "2028-03-01"}, "2028-02-28", "2028-03-01", []string{"2028-02-28", "2028-02-29", "2028-03-01"}},
		// Jalali: 14 Oct 2026 is 22 Mehr 1405 (Wednesday); Mehr starts on 23 Sep and Aban on 23 Oct.
		{"jalali days are the same", ReportRequest{From: "2026-10-12", To: "2026-10-14", Calendar: "jalali"}, "2026-10-12", "2026-10-14", []string{"2026-10-12", "2026-10-13", "2026-10-14"}},
		{"jalali weeks start on Saturday and the ends are partial", ReportRequest{From: "2026-10-07", To: "2026-10-20", Group: "week", Calendar: "jalali"}, "2026-10-07", "2026-10-20",
			[]string{"2026-10-03*", "2026-10-10", "2026-10-17*"}},
		{"a whole jalali week is not partial", ReportRequest{From: "2026-10-03", To: "2026-10-16", Group: "week", Calendar: "jalali"}, "2026-10-03", "2026-10-16", []string{"2026-10-03", "2026-10-10"}},
		{"a Friday ends the week before it", ReportRequest{From: "2026-10-16", To: "2026-10-16", Group: "week", Calendar: "jalali"}, "2026-10-16", "2026-10-16", []string{"2026-10-10*"}},
		{"jalali months", ReportRequest{From: "2026-09-15", To: "2026-11-02", Group: "month", Calendar: "jalali"}, "2026-09-15", "2026-11-02", []string{"2026-08-23*", "2026-09-23", "2026-10-23*"}},
		{"whole jalali months", ReportRequest{From: "2026-09-23", To: "2026-11-21", Group: "month", Calendar: "jalali"}, "2026-09-23", "2026-11-21", []string{"2026-09-23", "2026-10-23"}},
		{"a jalali month cut on its last day", ReportRequest{From: "2026-09-23", To: "2026-10-21", Group: "month", Calendar: "jalali"}, "2026-09-23", "2026-10-21", []string{"2026-09-23*"}},
		{"a 29 day Esfand (1405) is whole", ReportRequest{From: "2027-02-20", To: "2027-03-20", Group: "month", Calendar: "jalali"}, "2027-02-20", "2027-03-20", []string{"2027-02-20"}},
		{"a 30 day Esfand (1403) is whole", ReportRequest{From: "2025-02-19", To: "2025-03-20", Group: "month", Calendar: "jalali"}, "2025-02-19", "2025-03-20", []string{"2025-02-19"}},
		{"the 30th of Esfand 1403 and Nowruz", ReportRequest{From: "2025-03-20", To: "2025-03-21", Group: "month", Calendar: "jalali"}, "2025-03-20", "2025-03-21", []string{"2025-02-19*", "2025-03-21*"}},
		{"jalali this month", ReportRequest{Range: "this_month", Group: "month", Calendar: "jalali"}, "2026-09-23", "2026-10-14", []string{"2026-09-23*"}},
		{"jalali last month", ReportRequest{Range: "last_month", Group: "month", Calendar: "jalali"}, "2026-08-23", "2026-09-22", []string{"2026-08-23"}},
		{"jalali this month by day", ReportRequest{Range: "this_month", Calendar: "jalali"}, "2026-09-23", "2026-10-14", nil},
		{"gregorian is the default and is named", ReportRequest{Range: "last_month", Group: "month", Calendar: "gregorian"}, "2026-09-01", "2026-09-30", []string{"2026-09-01"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := NewReportRange(c.req, "Asia/Tehran", now)
			if err != nil {
				t.Fatal(err)
			}
			if r.From != c.from || r.To != c.to {
				t.Errorf("range %s to %s, want %s to %s", r.From, r.To, c.from, c.to)
			}
			if c.want != nil && !reflect.DeepEqual(spans(r), c.want) {
				t.Errorf("buckets %v, want %v", spans(r), c.want)
			}
			if c.req.Group == "" && r.Group != "day" {
				t.Errorf("group %q", r.Group)
			}
			if want := map[string]string{"": "gregorian"}[c.req.Calendar]; want != "" && r.Calendar != want {
				t.Errorf("calendar %q, want %q", r.Calendar, want)
			} else if want == "" && r.Calendar != c.req.Calendar {
				t.Errorf("calendar %q, want %q", r.Calendar, c.req.Calendar)
			}
		})
	}
}

// Day 1 of a Jalali month at the turn of the Jalali year, and the named ranges at
// the start and end of a month: "this month" follows the calendar.
func TestReportRangeNamedMonthsFollowTheCalendar(t *testing.T) {
	for _, c := range []struct {
		now, calendar, thisFrom, lastFrom, lastTo string
	}{
		{"2026-10-23", "jalali", "2026-10-23", "2026-09-23", "2026-10-22"}, // 1 Aban
		{"2026-10-22", "jalali", "2026-09-23", "2026-08-23", "2026-09-22"}, // 30 Mehr
		{"2026-03-21", "jalali", "2026-03-21", "2026-02-20", "2026-03-20"}, // 1 Farvardin 1405, after a 29 day Esfand
		{"2024-03-20", "jalali", "2024-03-20", "2024-02-20", "2024-03-19"}, // 1 Farvardin 1403
		{"2025-03-21", "jalali", "2025-03-21", "2025-02-19", "2025-03-20"}, // 1 Farvardin 1404, after the 30 day Esfand 1403
		{"2026-10-23", "gregorian", "2026-10-01", "2026-09-01", "2026-09-30"},
		{"2026-10-23", "", "2026-10-01", "2026-09-01", "2026-09-30"},
	} {
		now := day(c.now).Add(12 * time.Hour)
		this, err := NewReportRange(ReportRequest{Range: "this_month", Calendar: c.calendar}, "UTC", now)
		if err != nil || this.From != c.thisFrom || this.To != c.now {
			t.Errorf("%s %s this_month: %+v %v, want %s to %s", c.calendar, c.now, this, err, c.thisFrom, c.now)
		}
		last, err := NewReportRange(ReportRequest{Range: "last_month", Calendar: c.calendar}, "UTC", now)
		if err != nil || last.From != c.lastFrom || last.To != c.lastTo {
			t.Errorf("%s %s last_month: %+v %v, want %s to %s", c.calendar, c.now, last, err, c.lastFrom, c.lastTo)
		}
	}
}

func TestReportRangeRejectsAnUnknownCalendar(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, cal := range []string{"julian", "Jalali", "persian", "jalali "} {
		_, err := NewReportRange(ReportRequest{Calendar: cal}, "UTC", now)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "calendar must be gregorian or jalali") {
			t.Errorf("calendar %q: %v", cal, err)
		}
	}
}

// A Jalali week or month is asked of the store as days: it cuts Gregorian
// weeks and months only.
func TestReportRangeQueryAsksForDaysOfAJalaliBucket(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct{ group, calendar, want string }{
		{"week", "jalali", "day"}, {"month", "jalali", "day"}, {"day", "jalali", "day"},
		{"week", "gregorian", "week"}, {"month", "", "month"},
	} {
		r, err := NewReportRange(ReportRequest{Group: c.group, Calendar: c.calendar}, "UTC", now)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Query().Group; got != c.want {
			t.Errorf("%s %s: store group %q, want %q", c.group, c.calendar, got, c.want)
		}
		if r.Group != c.group && c.group != "" {
			t.Errorf("the range's own group changed to %q", r.Group)
		}
	}
}

// "Today" is the date in the report's zone, not the server's or UTC's.
func TestReportRangeTodayFollowsTheZone(t *testing.T) {
	// 21:00 UTC on 14 Oct is 00:30 on 15 Oct in Tehran and 17:00 on 14 Oct in New York.
	now := time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC)
	tehran, err := NewReportRange(ReportRequest{Range: "today"}, "Asia/Tehran", now)
	if err != nil || tehran.From != "2026-10-15" || tehran.TZ != "Asia/Tehran" {
		t.Fatalf("Tehran: %+v %v", tehran, err)
	}
	ny, err := NewReportRange(ReportRequest{Range: "today", TZ: "America/New_York"}, "Asia/Tehran", now)
	if err != nil || ny.From != "2026-10-14" || ny.TZ != "America/New_York" {
		t.Fatalf("New York: %+v %v", ny, err)
	}
}

func TestReportRangeBounds(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	ok := func(from, to, group string) error {
		_, err := NewReportRange(ReportRequest{From: from, To: to, Group: group}, "Asia/Tehran", now)
		return err
	}
	// 400 days is the most: 2026-01-01 to 2027-02-04 is exactly 400 days.
	if err := ok("2026-01-01", "2027-02-04", "day"); err != nil {
		t.Errorf("400 days refused: %v", err)
	}
	r, _ := NewReportRange(ReportRequest{From: "2026-01-01", To: "2027-02-04"}, "Asia/Tehran", now)
	if len(r.Buckets) != ReportMaxBuckets {
		t.Errorf("400 days make %d buckets", len(r.Buckets))
	}
	if err := ok("2026-01-01", "2027-02-05", "day"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "400") {
		t.Errorf("401 days: %v", err)
	}
	for name, c := range map[string][3]string{
		"from after to":   {"2026-10-14", "2026-10-13", ""},
		"not a date":      {"yesterday", "2026-10-13", ""},
		"a one digit day": {"2026-10-1", "2026-10-13", ""},
		"a datetime":      {"2026-10-01T00:00:00Z", "2026-10-13", ""},
		"before 2000":     {"1999-12-31", "2000-01-02", ""},
		"after 2100":      {"2100-12-31", "2101-01-02", ""},
		"a bad group":     {"2026-10-01", "2026-10-13", "year"},
		"only from":       {"2026-10-01", "", ""},
		"only to":         {"", "2026-10-13", ""},
	} {
		if err := ok(c[0], c[1], c[2]); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, req := range map[string]ReportRequest{
		"a range and dates":  {Range: "today", From: "2026-10-01", To: "2026-10-02"},
		"an unknown range":   {Range: "forever"},
		"a range and a from": {Range: "today", From: "2026-10-01"},
		"unknown zone":       {TZ: "Mars/Base"},
		"Local":              {TZ: "Local"},
		"a long name":        {TZ: strings.Repeat("a", 65)},
		"path tricks":        {TZ: "../../etc/passwd"},
	} {
		if _, err := NewReportRange(req, "Asia/Tehran", now); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The zone is the request's, else the store's general.timezone, else Tehran.
func TestReportRangeZoneComesFromTheSetting(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	zone := func(req ReportRequest) string {
		t.Helper()
		r, err := svc.ReportRange(ctx, req, now)
		if err != nil {
			t.Fatal(err)
		}
		return r.TZ
	}
	if got := zone(ReportRequest{}); got != "Asia/Tehran" {
		t.Errorf("no setting: %s", got)
	}
	if _, err := s.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('general.timezone', '"Europe/Berlin"')`); err != nil {
		t.Fatal(err)
	}
	if got := zone(ReportRequest{}); got != "Europe/Berlin" {
		t.Errorf("setting: %s", got)
	}
	if got := zone(ReportRequest{TZ: "America/New_York"}); got != "America/New_York" {
		t.Errorf("request: %s", got)
	}
	// A value that is no zone (it was valid when saved) falls back instead of failing every report.
	if _, err := s.DB().Exec(ctx, `UPDATE core.settings SET value = '"Mars/Base"' WHERE key = 'general.timezone'`); err != nil {
		t.Fatal(err)
	}
	if got := zone(ReportRequest{}); got != "Asia/Tehran" {
		t.Errorf("a lost zone: %s", got)
	}
}

// The reports follow what the code really does with money. Every flow below
// runs through the real service (orders, payment events, the wallet, refunds,
// trials), and the report must read each as the docs say.
func TestReportsFollowTheRealMoneyFlows(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	staff := owner(t, svc)
	plan := seedPlan(t, s, false, 700)
	usdt := &store.Plan{NameI18n: map[string]string{"en": "Pro USDT"}, Kind: "both", Price: 5_000_000, Currency: "USDT", Enabled: true}
	if err := s.UpsertPlan(ctx, s.Conn(), usdt); err != nil {
		t.Fatal(err)
	}
	trial := seedPlan(t, s, true, 0)
	intent := func(n int) string { return fmt.Sprintf("00000000-0000-7000-8000-%012d", n) }
	order := func(u *store.User, p *store.Plan, key string) *store.Order {
		t.Helper()
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	pay := func(id, topic string, p events.PaymentEvent) {
		t.Helper()
		if err := svc.HandlePaymentEvent(ctx, payEvent(t, id, topic, p)); err != nil {
			t.Fatal(err)
		}
	}

	// 1. A gateway payment for an order (Zarinpal): revenue, and not a top-up.
	u1 := seedUser(t, s, 3001)
	o1 := order(u1, plan, "r-1")
	pay("e1", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: intent(1), OrderID: o1.ID, UserID: u1.ID, Provider: "zarinpal", Amount: 700, Currency: "IRT"})
	// 2. A manual payment (card to card) that staff approved, then refunded.
	u2 := seedUser(t, s, 3002)
	o2 := order(u2, plan, "r-2")
	pay("e2", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: intent(2), OrderID: o2.ID, UserID: u2.ID, Provider: "manual_card", Amount: 700, Currency: "IRT"})
	if _, err := svc.RefundOrder(ctx, staff, &fakeProvisioner{}, o2.ID, "asked for it back"); err != nil {
		t.Fatal(err)
	}
	// 3. A wallet top-up with no order, then a purchase paid from the wallet.
	u3 := seedUser(t, s, 3003)
	pay("e3", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: intent(3), UserID: u3.ID, Provider: "manual_card", Amount: 5000, Currency: "IRT"})
	o3 := order(u3, plan, "r-3")
	if _, err := svc.PayOrderWithWallet(ctx, o3.ID, u3.ID); err != nil {
		t.Fatal(err)
	}
	// 4. A payment that arrives for an order paid another way: the money stays in the wallet (a top-up).
	u4 := seedUser(t, s, 3004)
	pay("e4", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: intent(4), UserID: u4.ID, Provider: "manual_card", Amount: 700, Currency: "IRT"})
	o4 := order(u4, plan, "r-4")
	if _, err := svc.PayOrderWithWallet(ctx, o4.ID, u4.ID); err != nil {
		t.Fatal(err)
	}
	pay("e5", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: intent(5), OrderID: o4.ID, UserID: u4.ID, Provider: "manual_card", Amount: 700, Currency: "IRT"})
	// 4b. A payment larger than the order: the order is paid, the surplus stays in the wallet.
	u8 := seedUser(t, s, 3008)
	o8 := order(u8, plan, "r-9")
	pay("e11", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: intent(11), OrderID: o8.ID, UserID: u8.ID, Provider: "manual_card", Amount: 1000, Currency: "IRT"})
	if got, _ := s.GetOrder(ctx, s.Conn(), o8.ID); got.Status != "paid" {
		t.Fatalf("the over-paid order is %q", got.Status)
	}
	if bal, _, _ := ledger(t, s, u8.ID); bal != 300 {
		t.Fatalf("the surplus in the wallet: %d", bal)
	}
	// 5. A payment in another currency, by an automatic gateway.
	u5 := seedUser(t, s, 3005)
	o5 := order(u5, usdt, "r-5")
	pay("e6", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: intent(6), OrderID: o5.ID, UserID: u5.ID, Provider: "usdt_trc20_auto", Amount: 5_000_000, Currency: "USDT"})
	// 6. A free trial: no money moves, and the trial is counted.
	u6 := seedUser(t, s, 3006)
	if _, err := svc.StartTrial(ctx, u6.ID, trial.ID, "t-1"); err != nil {
		t.Fatal(err)
	}
	// 7. Payments that did not go through. Staff rejected a card payment (and typed "expired" as
	// their reason for another: the provider says who rejected, not the words); a Zarinpal payment
	// expired and a Stars payment failed at the gateway; an order was cancelled before it was paid.
	pay("e7", events.PaymentsPaymentRejected, events.PaymentEvent{IntentID: intent(7), UserID: u6.ID, Provider: "manual_card", Amount: 900, Currency: "IRT", Reason: "blurry"})
	pay("e8", events.PaymentsPaymentRejected, events.PaymentEvent{IntentID: intent(8), UserID: u6.ID, Provider: "manual_crypto", Amount: 300, Currency: "IRT", Reason: "expired"})
	pay("e9", events.PaymentsPaymentRejected, events.PaymentEvent{IntentID: intent(9), UserID: u6.ID, Provider: "zarinpal", Amount: 700, Currency: "IRT", Reason: "expired"})
	pay("e10", events.PaymentsPaymentRejected, events.PaymentEvent{IntentID: intent(10), UserID: u6.ID, Provider: "stars", Amount: 400, Currency: "IRT", Reason: "payer_mismatch"})
	u7 := seedUser(t, s, 3007)
	order(u7, plan, "r-7")
	if n, err := s.CancelOpenOrders(ctx, s.Conn(), u7.ID); err != nil || n != 1 {
		t.Fatalf("cancel: %d %v", n, err)
	}
	// 8. An order still waiting for payment is nothing yet.
	order(u7, plan, "r-8")

	rng, err := svc.ReportRange(ctx, ReportRequest{Range: "last_7_days", Group: "week"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := svc.SalesReport(ctx, rng)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Currencies, []string{"IRT", "USDT"}) {
		t.Fatalf("currencies %v", rep.Currencies)
	}
	irt, usd := rep.Totals.Currencies["IRT"], rep.Totals.Currencies["USDT"]
	if irt.Revenue != 5*700 || irt.Orders != 5 || irt.NewOrders != 5 || irt.Renewals != 0 {
		t.Errorf("IRT sales: %+v", irt)
	}
	if usd.Revenue != 5_000_000 || usd.Orders != 1 {
		t.Errorf("USDT sales: %+v", usd)
	}
	if irt.Refunds != (store.ReportFlow{Count: 1, Amount: 700}) || Net(irt) != 4*700 {
		t.Errorf("IRT refunds %+v net %d", irt.Refunds, Net(irt))
	}
	// Top-ups: u3's 5000, u4's 700, the 700 that came late for u4's order, and the 300 left of
	// u8's 1000 after its 700 order. The payments for orders 1 and 2 bought an order at once, so
	// they are no top-ups.
	if irt.Topups != (store.ReportFlow{Count: 4, Amount: 5000 + 700 + 700 + 300}) || usd.Topups != (store.ReportFlow{}) {
		t.Errorf("top-ups IRT %+v USDT %+v", irt.Topups, usd.Topups)
	}
	if irt.Rejected != (store.ReportFlow{Count: 2, Amount: 900 + 300}) || irt.GatewayFailed != (store.ReportFlow{Count: 2, Amount: 700 + 400}) ||
		irt.Cancelled != (store.ReportFlow{Count: 1, Amount: 700}) {
		t.Errorf("rejected by staff %+v, gateway failed %+v, cancelled %+v", irt.Rejected, irt.GatewayFailed, irt.Cancelled)
	}
	if rep.Totals.NewCustomers != 6 || rep.Totals.Trials != 1 {
		t.Errorf("customers %d trials %d", rep.Totals.NewCustomers, rep.Totals.Trials)
	}
	// The same sums, from the ledger by SQL.
	var purchases, refunds int64
	if err := s.DB().QueryRow(ctx, `SELECT COALESCE(-sum(amount) FILTER (WHERE kind = 'purchase'), 0), COALESCE(sum(amount) FILTER (WHERE kind = 'refund'), 0)
		FROM core.ledger_entries WHERE currency = 'IRT'`).Scan(&purchases, &refunds); err != nil || purchases != irt.Revenue || refunds != irt.Refunds.Amount {
		t.Errorf("ledger purchases %d refunds %d (%v)", purchases, refunds, err)
	}

	methods, err := svc.SalesByMethod(ctx, rng)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, m := range methods {
		got[m.Currency+" "+m.Method] = m.Revenue
	}
	want := map[string]int64{"IRT gateway": 700, "IRT manual": 1400, "IRT wallet": 1400, "USDT gateway": 5_000_000}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("by method %v, want %v", got, want)
	}
	plans, err := svc.TopPlans(ctx, rng)
	if err != nil || len(plans) != 2 || plans[0].PlanID != plan.ID || plans[0].Orders != 5 || plans[0].Revenue != 3500 ||
		plans[1].PlanID != usdt.ID || plans[1].Currency != "USDT" || plans[1].Revenue != 5_000_000 {
		t.Errorf("top plans: %+v %v", plans, err)
	}
	// Empty buckets are zeros, in every currency.
	empty := 0
	for _, b := range rep.Buckets {
		if len(b.Currencies) != 2 {
			t.Errorf("bucket %s has %d currencies", b.Date, len(b.Currencies))
		}
		if b.Currencies["IRT"].Orders == 0 && b.Currencies["USDT"].Orders == 0 {
			empty++
		}
	}
	if empty == len(rep.Buckets) {
		t.Error("every bucket is empty")
	}
}

// A report over days with no sales is a row of zeros with no currency, not an error.
func TestSalesReportWithNothingSold(t *testing.T) {
	svc, _ := testService(t)
	rng, err := svc.ReportRange(context.Background(), ReportRequest{From: "2026-10-01", To: "2026-10-10"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := svc.SalesReport(context.Background(), rng)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Buckets) != 10 || len(rep.Currencies) != 0 || rep.Totals.NewCustomers != 0 {
		t.Errorf("%d buckets, currencies %v", len(rep.Buckets), rep.Currencies)
	}
	if _, err := svc.SalesReport(context.Background(), &ReportRange{From: "2026-10-01", To: "2026-10-02", Group: "day", TZ: "Mars/Base",
		Buckets: []ReportSpan{{Start: "2026-10-01"}, {Start: "2026-10-02"}}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a zone PostgreSQL does not know is an input error, not a failure: %v", err)
	}
}

// Orders of every kind through the real service methods: a discounted order, a
// free one (100% off), a renewal and a traffic package of a delivered service,
// a reseller's purchase on credit, and a purchase in another currency. The report
// counts what cost money, by type, and keeps the currencies apart.
func TestReportsFollowOrdersOfEveryKind(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	admin := staffMember(t, s, 9500, "admin")
	if _, err := svc.UpsertUser(ctx, UpsertUserParams{TelegramID: ownerTG}); err != nil {
		t.Fatal(err)
	}
	plan := planWithLimits(t, s, 30, 50<<30) // 1000 IRT
	pack := topupPlan(t, s, 10<<30, 300)
	usdt := &store.Plan{NameI18n: map[string]string{"en": "Pro USDT"}, Kind: "both", Price: 5_000_000, Currency: "USDT", Enabled: true}
	if err := s.UpsertPlan(ctx, s.Conn(), usdt); err != nil {
		t.Fatal(err)
	}
	ten, full := int32(10), int32(100)
	for code, pct := range map[string]*int32{"TEN": &ten, "GIFT": &full} {
		if _, err := svc.AdminUpsertDiscount(ctx, ownerTG, &store.Discount{Code: code, Percent: pct, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	topup := func(u *store.User, n int, cur string, amt int64) {
		t.Helper()
		ev := events.PaymentEvent{IntentID: fmt.Sprintf("00000000-0000-7000-8000-%012d", 100+n), UserID: u.ID, Provider: "manual_card", Amount: amt, Currency: cur}
		if err := svc.HandlePaymentEvent(ctx, payEvent(t, fmt.Sprintf("t%d", n), events.PaymentsPaymentSucceeded, ev)); err != nil {
			t.Fatal(err)
		}
	}
	wallet := func(u *store.User, p *store.Plan, key string, typ, sub, code string) *store.Order {
		t.Helper()
		o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, Type: typ, SubscriptionID: sub, IdempotencyKey: key, DiscountCode: code})
		if err != nil {
			t.Fatalf("order %s: %v", key, err)
		}
		paid, err := svc.PayOrderWithWallet(ctx, o.ID, u.ID)
		if err != nil {
			t.Fatalf("pay %s: %v", key, err)
		}
		return paid
	}

	// A customer: a top-up, a discounted purchase (10% off 1000 = 900), its delivery,
	// then a renewal (1000) and a traffic package (300) of that service.
	u := seedUser(t, s, 3101)
	topup(u, 1, "IRT", 5000)
	first := wallet(u, plan, "k-new", OrderNew, "", "ten")
	if first.Amount != 900 {
		t.Fatalf("discounted price %d", first.Amount)
	}
	if worked, err := svc.NewProvisionWorker(&fakeProvisioner{}, nil).ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("deliver: %v %v", worked, err)
	}
	sub, err := s.SubscriptionByOrder(ctx, s.Conn(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	wallet(u, plan, "k-renew", OrderRenew, sub.ID, "")
	wallet(u, pack, "k-pack", OrderTrafficTopup, sub.ID, "")
	// 100% off: a free order, paid at once. No money moves and no customer is "new" for it.
	v := seedUser(t, s, 3102)
	free, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: v.ID, PlanID: plan.ID, IdempotencyKey: "k-free", DiscountCode: "gift"})
	if err != nil || free.Amount != 0 || free.Status != "paid" {
		t.Fatalf("free order: %+v %v", free, err)
	}
	// A reseller buys on credit (20% off: 800) and owes it.
	tier := seedTier(t, svc, admin, "Gold", 20, 10, nil)
	r := reseller(t, svc, s, admin, 3103, tier, 5000)
	wallet(r, plan, "k-reseller", OrderNew, "", "")
	if bal, _, _ := ledger(t, s, r.ID); bal != -800 {
		t.Fatalf("the reseller's balance %d", bal)
	}
	// Another currency.
	w := seedUser(t, s, 3104)
	usdtTopup := events.PaymentEvent{IntentID: "00000000-0000-7000-8000-000000000999", UserID: w.ID, Provider: "usdt_trc20_auto", Amount: 6_000_000, Currency: "USDT"}
	if err := svc.HandlePaymentEvent(ctx, payEvent(t, "t-usdt", events.PaymentsPaymentSucceeded, usdtTopup)); err != nil {
		t.Fatal(err)
	}
	wallet(w, usdt, "k-usdt", OrderNew, "", "")

	rng, err := svc.ReportRange(ctx, ReportRequest{Range: "last_7_days"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := svc.SalesReport(ctx, rng)
	if err != nil {
		t.Fatal(err)
	}
	irt, usd := rep.Totals.Currencies["IRT"], rep.Totals.Currencies["USDT"]
	if irt.Revenue != 900+1000+300+800 || irt.Orders != 4 || irt.NewOrders != 2 || irt.Renewals != 1 || irt.TrafficTopups != 1 {
		t.Errorf("IRT: %+v", irt)
	}
	if usd.Revenue != 5_000_000 || usd.Orders != 1 || usd.NewOrders != 1 {
		t.Errorf("USDT: %+v", usd)
	}
	// The customer and the reseller are the new customers; the free order's buyer and nobody else is not.
	if rep.Totals.NewCustomers != 3 { // u, the reseller, w
		t.Errorf("new customers %d", rep.Totals.NewCustomers)
	}
	if irt.Topups != (store.ReportFlow{Count: 1, Amount: 5000}) || usd.Topups != (store.ReportFlow{Count: 1, Amount: 6_000_000}) {
		t.Errorf("top-ups IRT %+v USDT %+v", irt.Topups, usd.Topups)
	}
	methods, err := svc.SalesByMethod(ctx, rng)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range methods {
		if m.Method != "wallet" { // every one of these was paid from a wallet, a reseller's on credit
			t.Errorf("unexpected method %+v", m)
		}
	}
	if len(methods) != 2 || methods[0].Revenue != 3000 || methods[1].Revenue != 5_000_000 {
		t.Errorf("methods: %+v", methods)
	}
}

// A payment that fails at its gateway or is rejected by a reviewer is told
// apart by the provider; an event from before core passed the provider on is
// told by its reason. This runs both through core's real handler, and reads the
// reasons payments emits so a new one is a decision, not an accident.
func TestReportsSplitStaffRejectionsFromFailedGatewayPayments(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	u := seedUser(t, s, 3201)
	reject := func(n int, provider, reason string, amt int64) {
		t.Helper()
		ev := events.PaymentEvent{IntentID: fmt.Sprintf("00000000-0000-7000-8000-%012d", n), UserID: u.ID, Provider: provider, Amount: amt, Currency: "IRT", Reason: reason}
		if err := svc.HandlePaymentEvent(ctx, payEvent(t, fmt.Sprintf("rej-%d", n), events.PaymentsPaymentRejected, ev)); err != nil {
			t.Fatal(err)
		}
	}
	reject(1, "manual_card", "blurry photo", 100)         // staff, any words
	reject(2, "manual_zarinpal", "expired", 200)          // staff, who typed a gateway-looking word
	reject(3, "zarinpal", "expired", 400)                 // a gateway
	reject(4, "usdt_trc20_auto", "expired", 800)          // a gateway
	reject(5, "stars", "payer_mismatch", 1600)            // a gateway
	reject(6, "zarinpal", "a_reason_nobody_knew", 3200)   // a gateway whatever its reason: a new reason is no staff rejection
	reject(7, "some_new_gateway", "gateway_failed", 6400) // a provider that is not manual is a gateway
	// Events published before the provider was passed on: told by the reason.
	for i, c := range []struct {
		reason string
		amt    int64
	}{{"expired", 12800}, {"amount_mismatch", 25600}, {"please send a clearer photo", 51200}, {"", 102400}} {
		payload := fmt.Sprintf(`{"user_id":%q,"intent_id":"00000000-0000-7000-8000-0000000007%02d","amount":%d,"currency":"IRT","reason":%q}`, u.ID, i, c.amt, c.reason)
		if _, err := s.DB().Exec(ctx, `INSERT INTO core.outbox_core (topic, payload, event_id) VALUES ('payment.rejected.v1', $1::text::jsonb, $2)`,
			payload, fmt.Sprintf("00000000-0000-7000-8000-0000000008%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	rng, err := svc.ReportRange(ctx, ReportRequest{Range: "today"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := svc.SalesReport(ctx, rng)
	if err != nil {
		t.Fatal(err)
	}
	irt := rep.Totals.Currencies["IRT"]
	if want := (store.ReportFlow{Count: 4, Amount: 100 + 200 + 51200 + 102400}); irt.Rejected != want {
		t.Errorf("rejected by staff %+v, want %+v", irt.Rejected, want)
	}
	if want := (store.ReportFlow{Count: 7, Amount: 400 + 800 + 1600 + 3200 + 6400 + 12800 + 25600}); irt.GatewayFailed != want {
		t.Errorf("failed gateway payments %+v, want %+v", irt.GatewayFailed, want)
	}
	// The rejection reaches the bot with its provider, for those who read it.
	var e events.PaymentRejectedEvent
	if evs := outboxEvents(t, s, events.PaymentRejected); len(evs) != 7+4 || json.Unmarshal(evs[2], &e) != nil || e.Provider != "zarinpal" || e.Reason != "expired" {
		t.Errorf("published: %d events (7 real, 4 inserted), third %+v", len(evs), e)
	}
}

// The zone is the request's, else the setting's, else BOBRES_TIMEZONE's, else
// Tehran; a value that is no zone is skipped, not fatal.
func TestReportRangeZoneFallsBackToTheEnvironment(t *testing.T) {
	_, s := testService(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	zone := func(env string) string {
		t.Helper()
		r, err := New(s, Config{Timezone: env}).ReportRange(ctx, ReportRequest{}, now)
		if err != nil {
			t.Fatal(err)
		}
		return r.TZ
	}
	if got := zone(""); got != "Asia/Tehran" {
		t.Errorf("nothing set: %s", got)
	}
	if got := zone("Europe/Berlin"); got != "Europe/Berlin" {
		t.Errorf("BOBRES_TIMEZONE: %s", got)
	}
	if got := zone("Mars/Base"); got != "Asia/Tehran" {
		t.Errorf("a bad BOBRES_TIMEZONE: %s", got)
	}
	if got := zone("Local"); got != "Asia/Tehran" {
		t.Errorf("Local: %s", got)
	}
	if r, err := New(s, Config{Timezone: "Europe/Berlin"}).ReportRange(ctx, ReportRequest{TZ: "America/New_York"}, now); err != nil || r.TZ != "America/New_York" {
		t.Errorf("the request wins: %v %v", r, err)
	}
	if _, err := s.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('general.timezone', '"Asia/Tokyo"')`); err != nil {
		t.Fatal(err)
	}
	if got := zone("Europe/Berlin"); got != "Asia/Tokyo" {
		t.Errorf("the setting wins over the environment: %s", got)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.settings SET value = '"Mars/Base"' WHERE key = 'general.timezone'`); err != nil {
		t.Fatal(err)
	}
	if got := zone("Europe/Berlin"); got != "Europe/Berlin" {
		t.Errorf("a bad setting falls through to the environment: %s", got)
	}
}

// A report that cannot finish in time says so: PostgreSQL's statement limit and
// the report's own deadline are one error staff can act on (a shorter range),
// not a failure.
func TestReportTimeoutIsNamed(t *testing.T) {
	svc, s := testService(t)
	needConns(t, s, 2)
	ctx := context.Background()
	old := store.ReportTimeout
	store.ReportTimeout = 200 * time.Millisecond
	t.Cleanup(func() { store.ReportTimeout = old })
	// A held lock on the ledger stops the report's first statement until PostgreSQL cancels it.
	tx, err := s.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // released at the end
	if _, err := tx.Exec(ctx, `LOCK TABLE core.ledger_entries IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	rng, err := NewReportRange(ReportRequest{From: "2026-10-01", To: "2026-10-02"}, "Asia/Tehran", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := svc.SalesReport(ctx, rng); !errors.Is(err, ErrReportTimeout) || !strings.Contains(err.Error(), "shorter range") {
		t.Errorf("a blocked report: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("the report waited %s", time.Since(start))
	}
	if _, err := svc.TopPlans(ctx, rng); !errors.Is(err, ErrReportTimeout) {
		t.Errorf("a blocked plans report: %v", err)
	}
	// The deadline of the whole report counts the same.
	if err := reportError(fmt.Errorf("report: %w", context.DeadlineExceeded)); !errors.Is(err, ErrReportTimeout) {
		t.Errorf("a deadline: %v", err)
	}
	if err := reportError(context.Canceled); errors.Is(err, ErrReportTimeout) {
		t.Errorf("a closed connection is no timeout: %v", err)
	}
}
