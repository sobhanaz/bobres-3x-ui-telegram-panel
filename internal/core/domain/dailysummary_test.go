package domain

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func setRaw(t *testing.T, s *store.Store, key, value string) {
	t.Helper()
	raw, _ := json.Marshal(value)
	if err := s.SetSetting(context.Background(), s.Conn(), key, raw); err != nil {
		t.Fatal(err)
	}
}

func summaries(t *testing.T, s *store.Store) []events.DailySummaryEvent {
	t.Helper()
	var out []events.DailySummaryEvent
	for _, raw := range outboxEvents(t, s, events.SalesDailySummary) {
		var e events.DailySummaryEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// The summary goes out once a day, after the configured hour, for the day
// before, with the numbers of the Reports page; it is off by default, an empty
// day is still sent, and a day is never sent twice, even by a second worker.
func TestDailySummaryIsSentOncePerDayAfterTheHour(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	loc, _ := time.LoadLocation("Asia/Tehran")
	today := time.Now().In(loc)
	at := func(days, hour, min int) time.Time {
		return time.Date(today.Year(), today.Month(), today.Day()+days, hour, min, 0, 0, loc)
	}
	step := func(w *DailySummaryWorker, now time.Time) (string, bool) {
		t.Helper()
		w.now = func() time.Time { return now }
		date, sent, err := w.Step(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return date, sent
	}
	w := svc.NewDailySummaryWorker(nil)

	// One paid order today, and a trial customer: tomorrow's "yesterday".
	plan := seedPlan(t, s, false, 700)
	u := seedUser(t, s, 4001)
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: plan.ID, IdempotencyKey: "ds-1"})
	if err != nil {
		t.Fatal(err)
	}
	pay := payEvent(t, "ds-e1", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: "00000000-0000-7000-8000-000000000001",
		OrderID: o.ID, UserID: u.ID, Provider: "zarinpal", Amount: 700, Currency: "IRT"})
	if err := svc.HandlePaymentEvent(ctx, pay); err != nil {
		t.Fatal(err)
	}

	// Off by default.
	if _, sent := step(w, at(1, 12, 0)); sent {
		t.Fatal("sent while notify.daily_summary is off")
	}
	setRaw(t, s, "notify.daily_summary", "true")

	// Before the hour (9 by default): nothing yet.
	if _, sent := step(w, at(1, 8, 59)); sent {
		t.Fatal("sent before the hour")
	}
	// After it: yesterday's numbers (today's real date, seen from tomorrow).
	date, sent := step(w, at(1, 9, 0))
	if !sent || date != today.Format("2006-01-02") {
		t.Fatalf("not sent: %q %v", date, sent)
	}
	got := summaries(t, s)
	if len(got) != 1 {
		t.Fatalf("published %d", len(got))
	}
	e := got[0]
	if e.Date != date || e.TZ != "Asia/Tehran" || e.NewCustomers != 1 || e.Trials != 0 || len(e.Currencies) != 1 {
		t.Fatalf("event: %+v", e)
	}
	if c := e.Currencies[0]; c.Currency != "IRT" || c.Revenue != 700 || c.Net != 700 || c.Orders != 1 || c.NewOrders != 1 || c.Renewals != 0 || c.Refunds != 0 {
		t.Fatalf("currency: %+v", c)
	}

	// Once a day: later in the day, after a restart (a new worker), and from a second worker.
	for _, w2 := range []*DailySummaryWorker{w, svc.NewDailySummaryWorker(nil), svc.NewDailySummaryWorker(nil)} {
		if _, sent := step(w2, at(1, 18, 30)); sent {
			t.Fatal("sent the same day twice")
		}
	}
	if n := len(summaries(t, s)); n != 1 {
		t.Fatalf("published %d", n)
	}

	// The next day an empty day is still sent, with the day it covers.
	date, sent = step(w, at(2, 10, 0))
	if !sent || date != at(1, 0, 0).Format("2006-01-02") {
		t.Fatalf("the next day: %q %v", date, sent)
	}
	got = summaries(t, s)
	if len(got) != 2 || got[1].Date != date || len(got[1].Currencies) != 0 || got[1].NewCustomers != 0 {
		t.Fatalf("empty day: %+v", got)
	}

	// Days missed are not made up: three days later only the last finished day is sent.
	date, sent = step(w, at(5, 9, 5))
	if !sent || date != at(4, 0, 0).Format("2006-01-02") {
		t.Fatalf("after a gap: %q %v", date, sent)
	}
	if n := len(summaries(t, s)); n != 3 {
		t.Fatalf("published %d", n)
	}

	// The hour is a setting; 0 means just after midnight.
	setRaw(t, s, "notify.daily_summary_hour", "0")
	if date, sent = step(w, at(6, 0, 5)); !sent || date != at(5, 0, 0).Format("2006-01-02") {
		t.Fatalf("hour 0: %q %v", date, sent)
	}
	setRaw(t, s, "notify.daily_summary_hour", "23")
	if _, sent = step(w, at(7, 22, 59)); sent {
		t.Fatal("sent before hour 23")
	}
	if _, sent = step(w, at(7, 23, 0)); !sent {
		t.Fatal("not sent at hour 23")
	}

	// Turning it off stops it.
	setRaw(t, s, "notify.daily_summary", "false")
	if _, sent = step(w, at(9, 12, 0)); sent {
		t.Fatal("sent while off")
	}
}

// The two settings are checked like the others: a switch, and an hour from 0 to 23.
func TestDailySummarySettingsAreChecked(t *testing.T) {
	for _, c := range []struct {
		key, in, out string
		ok           bool
	}{
		{"notify.daily_summary", "on", "true", true},
		{"notify.daily_summary", "maybe", "", false},
		{"notify.daily_summary_hour", "0", "0", true},
		{"notify.daily_summary_hour", "۲۳", "23", true},
		{"notify.daily_summary_hour", "24", "", false},
		{"notify.daily_summary_hour", "-1", "", false},
	} {
		got, err := NormalizeSetting(c.key, c.in)
		if (err == nil) != c.ok || (c.ok && got != c.out) {
			t.Errorf("%s=%q: %q, %v", c.key, c.in, got, err)
		}
	}
}
