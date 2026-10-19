package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// The daily summary is run here the way core runs it, one Step a minute, but
// with a clock the test sets: the due time, a restart, an outage, a changed
// setting, another time zone and the days clocks change are each a few ticks.

// holdsReportsRead is who may read the reports: core is given the dashboard's
// permission table (cmd/core, web.Allowed), which the domain tests cannot import.
func holdsReportsRead(role string) bool { return role == "owner" || role == "admin" }

type summaryWorld struct {
	t    *testing.T
	ctx  context.Context
	svc  *Service
	s    *store.Store
	d    *DailySummary
	loc  *time.Location
	now  time.Time
	subs map[string]string // a user's first service, for the renewals the model seeds
}

// newSummaryWorld has the store in zone (the general.timezone setting; "": none
// set) and BOBRES_TIMEZONE env.
func newSummaryWorld(t *testing.T, zone, env string) *summaryWorld {
	t.Helper()
	_, s := testService(t)
	svc := New(s, Config{OwnerTelegramIDs: []int64{ownerTG}, Timezone: env})
	ctx := context.Background()
	if err := testdb.Truncate(ctx, s.DB(), `core.daily_summary_claims`); err != nil {
		t.Fatal(err)
	}
	w := &summaryWorld{t: t, ctx: ctx, svc: svc, s: s, subs: map[string]string{}}
	w.loc = time.UTC
	if zone != "" {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		w.loc = loc
		w.setting("general.timezone", zone, time.Now())
	}
	w.restart()
	return w
}

// restart is a new worker (core restarted): it remembers nothing.
func (w *summaryWorld) restart() {
	w.d = w.svc.NewDailySummary(holdsReportsRead, nil)
	w.d.now = func() time.Time { return w.now }
}

func (w *summaryWorld) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.s.DB().Exec(w.ctx, sql, args...); err != nil {
		w.t.Fatalf("seed: %v\n%s", err, sql)
	}
}

// at reads a time of day on the store's clock ("2026-10-14 09:00").
func (w *summaryWorld) at(local string) time.Time {
	w.t.Helper()
	t, err := time.ParseInLocation("2006-01-02 15:04", local, w.loc)
	if err != nil {
		w.t.Fatal(err)
	}
	return t
}

func utc(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// setting writes a setting as of when it was changed (the worker reads when the
// switch was turned on).
func (w *summaryWorld) setting(key, value string, changed time.Time) {
	w.t.Helper()
	w.exec(`INSERT INTO core.settings (key, value, updated_at) VALUES ($1, to_jsonb($2::text), $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, key, value, changed)
}

// on turns the summary on as of local, and clock moves the time it is sent.
func (w *summaryWorld) on(local string) { w.setting(SettingDailySummary, "true", w.at(local)) }
func (w *summaryWorld) clock(hhmm, local string) {
	w.setting(SettingDailySummaryTime, hhmm, w.at(local))
}

func (w *summaryWorld) step() {
	w.t.Helper()
	if err := w.d.Step(w.ctx); err != nil {
		w.t.Fatalf("step at %s: %v", w.now, err)
	}
}

// tick runs the worker with the store's clock at local; tickUTC with an instant.
func (w *summaryWorld) tick(local string) {
	w.t.Helper()
	w.now = w.at(local)
	w.step()
}

func (w *summaryWorld) tickUTC(instant string) {
	w.t.Helper()
	w.now = utc(w.t, instant)
	w.step()
}

// staff adds an active staff member (or a customer: role "user").
func (w *summaryWorld) staff(tg int64, role, lang string) *store.User {
	w.t.Helper()
	u, err := w.s.UpsertUser(w.ctx, w.s.Conn(), tg, "", lang, "")
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.s.SetUserRole(w.ctx, w.s.Conn(), u.ID, role, "active"); err != nil {
		w.t.Fatal(err)
	}
	u.Role = role
	return u
}

func (w *summaryWorld) claims() []string {
	w.t.Helper()
	rows, err := w.s.DB().Query(w.ctx, `SELECT to_char(day, 'YYYY-MM-DD') FROM core.daily_summary_claims ORDER BY day`)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func (w *summaryWorld) sent() []events.DailySummaryEvent {
	w.t.Helper()
	out := []events.DailySummaryEvent{}
	for _, raw := range outboxEvents(w.t, w.s, events.DailySummary) {
		var e events.DailySummaryEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// expect: exactly these days are claimed, and n events were written in all.
func (w *summaryWorld) expect(days []string, n int) {
	w.t.Helper()
	if got := w.claims(); !reflect.DeepEqual(got, days) {
		w.t.Fatalf("at %s: claimed days %v, want %v", w.now, got, days)
	}
	if got := len(w.sent()); got != n {
		w.t.Fatalf("at %s: %d events, want %d", w.now, got, n)
	}
}

func (w *summaryWorld) nothing() { w.t.Helper(); w.expect([]string{}, 0) }

// Off is the default, in the registry and in what the worker does: it looks
// every minute for days and writes no claim and no event.
func TestDailySummaryIsOffByDefault(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	owner(t, w.svc)
	if sp, ok := SpecOf(SettingDailySummary); !ok || sp.Default != "false" || sp.Kind != KindBool || sp.Group != "reports" || sp.Perm != permSettings {
		t.Errorf("the switch: %+v", sp)
	}
	if sp, ok := SpecOf(SettingDailySummaryTime); !ok || sp.Default != "09:00" || sp.Kind != KindTime || sp.Group != "reports" {
		t.Errorf("the time: %+v", sp)
	}
	for _, at := range []string{"2026-10-14 08:00", "2026-10-14 09:00", "2026-10-14 15:00", "2026-10-15 09:00", "2026-10-16 23:59"} {
		w.tick(at)
	}
	w.nothing()
	// Turned on and off again, it is off.
	w.setting(SettingDailySummary, "false", w.at("2026-10-16 10:00"))
	w.tick("2026-10-17 09:00")
	w.tick("2026-10-18 09:00")
	w.nothing()
}

// On: one claim and one event for each recipient at the due time, nothing at
// the next minute or the rest of the day, nothing from a worker that started
// later the same day, and the next day's summary the next day.
func TestDailySummaryIsSentOnceADay(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	o := owner(t, w.svc)
	a := w.staff(9501, "admin", "en")
	w.on("2026-10-10 12:00")

	w.tick("2026-10-14 08:59")
	w.nothing()
	w.tick("2026-10-14 09:00")
	w.expect([]string{"2026-10-13"}, 2)
	got := w.sent()
	if got[0].TelegramID != o.TelegramID || got[0].Language != "fa" || got[1].TelegramID != a.TelegramID || got[1].Language != "en" {
		t.Errorf("recipients and their languages: %+v", got)
	}
	for _, e := range got {
		if e.Date != "2026-10-13" || e.Zone != "Asia/Tehran" {
			t.Errorf("day and zone: %+v", e)
		}
	}
	for _, at := range []string{"2026-10-14 09:01", "2026-10-14 12:00", "2026-10-14 23:59"} {
		w.tick(at)
	}
	w.expect([]string{"2026-10-13"}, 2)

	w.restart() // core restarted
	for _, at := range []string{"2026-10-14 09:05", "2026-10-14 20:00"} {
		w.tick(at)
	}
	w.expect([]string{"2026-10-13"}, 2)

	w.tick("2026-10-15 08:59")
	w.expect([]string{"2026-10-13"}, 2)
	w.tick("2026-10-15 09:00")
	w.expect([]string{"2026-10-13", "2026-10-14"}, 4)
	var days []string
	for _, e := range w.sent() {
		days = append(days, e.Date)
	}
	if !reflect.DeepEqual(days, []string{"2026-10-13", "2026-10-13", "2026-10-14", "2026-10-14"}) {
		t.Errorf("days: %v", days)
	}
}

// Core down at the due time: the summary goes out as soon as it is back, the same
// local day, once. Down for a day or more: only yesterday's goes, never the days
// that were missed.
func TestDailySummaryCatchesUpWithoutAFlood(t *testing.T) {
	t.Run("back later the same day", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-10 12:00")
		w.tick("2026-10-14 14:30") // core was down from 08:00 to 14:30
		w.expect([]string{"2026-10-13"}, 1)
		w.tick("2026-10-14 14:31")
		w.restart()
		w.tick("2026-10-14 22:00")
		w.expect([]string{"2026-10-13"}, 1)
	})
	t.Run("back a day later", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-10 12:00")
		w.tick("2026-10-14 08:00") // up, but not yet due; then down all of the 14th
		w.tick("2026-10-15 10:00")
		w.expect([]string{"2026-10-14"}, 1) // yesterday's, and not the 13th's
		w.tick("2026-10-15 10:01")
		w.expect([]string{"2026-10-14"}, 1)
	})
	t.Run("back after several days", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-01 12:00")
		w.tick("2026-10-11 09:00") // the last one before the outage: for the 10th
		w.expect([]string{"2026-10-10"}, 1)
		w.tick("2026-10-14 10:00") // down on the 11th, 12th and 13th
		w.expect([]string{"2026-10-10", "2026-10-13"}, 2)
		w.restart()
		w.tick("2026-10-14 11:00")
		w.expect([]string{"2026-10-10", "2026-10-13"}, 2)
		for _, e := range w.sent() {
			if e.Date == "2026-10-11" || e.Date == "2026-10-12" {
				t.Errorf("a missed day was made up for: %+v", e)
			}
		}
	})
}

// A change of the time setting never sends a day twice and never skips today's:
// moved later it waits; moved to a time already passed it sends once, at once,
// if today's was not sent; once sent, no change sends it again.
func TestDailySummaryFollowsTheTimeSetting(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	owner(t, w.svc)
	w.on("2026-10-01 08:00")
	w.clock("09:00", "2026-10-01 08:00")

	// Moved to the future before it was due: it waits for the new time.
	w.tick("2026-10-14 08:00")
	w.clock("14:00", "2026-10-14 08:30")
	w.tick("2026-10-14 10:00")
	w.tick("2026-10-14 13:59")
	w.nothing()
	w.tick("2026-10-14 14:00")
	w.expect([]string{"2026-10-13"}, 1)

	// Sent today: a time that has passed, or any other, does not send it again.
	w.clock("07:00", "2026-10-14 15:00")
	w.tick("2026-10-14 15:01")
	w.clock("23:00", "2026-10-14 15:02")
	w.tick("2026-10-14 23:00")
	w.expect([]string{"2026-10-13"}, 1)

	// Another day, due at 20:00; moved at 15:00 to 09:00, which has passed and
	// today's was not sent: it goes once, now.
	w.clock("20:00", "2026-10-14 23:30")
	w.tick("2026-10-15 15:00")
	w.expect([]string{"2026-10-13"}, 1)
	w.clock("09:00", "2026-10-15 15:00")
	w.tick("2026-10-15 15:00")
	w.expect([]string{"2026-10-13", "2026-10-14"}, 2)
	w.clock("08:00", "2026-10-15 16:00")
	w.tick("2026-10-15 16:01")
	w.clock("17:00", "2026-10-15 16:02")
	w.tick("2026-10-15 17:00")
	w.expect([]string{"2026-10-13", "2026-10-14"}, 2)

	// A time with no setting is the default, 09:00; so is one nobody can read.
	w.exec(`DELETE FROM core.settings WHERE key = $1`, SettingDailySummaryTime)
	w.tick("2026-10-16 08:59")
	w.expect([]string{"2026-10-13", "2026-10-14"}, 2)
	w.tick("2026-10-16 09:00")
	w.expect([]string{"2026-10-13", "2026-10-14", "2026-10-15"}, 3)
	w.exec(`INSERT INTO core.settings (key, value) VALUES ($1, '"soon"')`, SettingDailySummaryTime)
	w.tick("2026-10-17 08:59")
	w.tick("2026-10-17 09:00")
	w.expect([]string{"2026-10-13", "2026-10-14", "2026-10-15", "2026-10-16"}, 4)
}

// Turning it on does not make up for days: switched on before the time it is
// sent, the first summary is today's; switched on after it, tomorrow's, and it is
// for the day it was switched on, never for an earlier one.
func TestDailySummarySwitchedOnMidDayStartsTomorrow(t *testing.T) {
	t.Run("after the time", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-14 15:00")
		for _, at := range []string{"2026-10-14 15:00", "2026-10-14 15:05", "2026-10-14 23:59"} {
			w.tick(at)
		}
		w.nothing()
		w.tick("2026-10-15 08:59")
		w.nothing()
		w.tick("2026-10-15 09:00")
		w.expect([]string{"2026-10-14"}, 1)
	})
	t.Run("before the time", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-14 08:30")
		w.tick("2026-10-14 08:59")
		w.nothing()
		w.tick("2026-10-14 09:00")
		w.expect([]string{"2026-10-13"}, 1)
	})
	t.Run("switched off and on again after the time", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		w.tick("2026-10-13 09:00")
		w.setting(SettingDailySummary, "false", w.at("2026-10-14 08:00"))
		w.tick("2026-10-14 09:00")
		w.setting(SettingDailySummary, "true", w.at("2026-10-14 12:00"))
		w.tick("2026-10-14 12:00")
		w.tick("2026-10-14 20:00")
		w.expect([]string{"2026-10-12"}, 1) // nothing for the 13th: it was off at its time
		w.tick("2026-10-15 09:00")
		w.expect([]string{"2026-10-12", "2026-10-14"}, 2)
	})
}

// A clock that is set back never repeats a summary, and one set forward sends the
// day it lands on, once.
func TestDailySummarySurvivesClockChanges(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	owner(t, w.svc)
	w.on("2026-10-01 08:00")
	w.tick("2026-10-14 09:00")
	w.tick("2026-10-14 08:00") // set back an hour
	w.tick("2026-10-14 09:00")
	w.tick("2026-10-13 23:30") // back past midnight: the 13th's own summary (of the 12th) was never sent, and goes now, once
	w.tick("2026-10-13 10:00")
	w.expect([]string{"2026-10-12", "2026-10-13"}, 2)
	w.tick("2026-10-14 09:30")
	w.expect([]string{"2026-10-12", "2026-10-13"}, 2)
	w.tick("2026-10-20 09:00") // set forward
	w.tick("2026-10-20 09:01")
	w.expect([]string{"2026-10-12", "2026-10-13", "2026-10-19"}, 3)
}

// Days are the store's days: the zone of the general.timezone setting, else
// BOBRES_TIMEZONE, else Tehran; the same instant is another date in another zone.
func TestDailySummaryReadsDaysInTheStoresZone(t *testing.T) {
	t.Run("Tehran", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		w.clock("00:15", "2026-10-01 08:00")
		// 20:45 UTC on the 13th is 00:15 on the 14th in Tehran (UTC+3:30): the summary is of
		// the 13th, where UTC would call yesterday the 12th.
		w.tickUTC("2026-10-13T20:44:00Z")
		w.nothing()
		w.tickUTC("2026-10-13T20:45:00Z")
		w.expect([]string{"2026-10-13"}, 1)
		if e := w.sent()[0]; e.Date != "2026-10-13" || e.Zone != "Asia/Tehran" {
			t.Errorf("%+v", e)
		}
	})
	t.Run("New York", func(t *testing.T) {
		w := newSummaryWorld(t, "America/New_York", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		w.tickUTC("2026-10-14T12:59:00Z") // 08:59 in New York (UTC-4)
		w.nothing()
		w.tickUTC("2026-10-14T13:00:00Z")
		w.expect([]string{"2026-10-13"}, 1)
		if e := w.sent()[0]; e.Zone != "America/New_York" {
			t.Errorf("%+v", e)
		}
		// 03:00 UTC on the 14th is still the 13th, 23:00, in New York: no new day yet.
		w.tickUTC("2026-10-15T03:00:00Z")
		w.expect([]string{"2026-10-13"}, 1)
	})
	t.Run("BOBRES_TIMEZONE when no zone is set, and a bad one is skipped once", func(t *testing.T) {
		w := newSummaryWorld(t, "", "America/New_York")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		var log bytes.Buffer
		w.d = w.svc.NewDailySummary(holdsReportsRead, slog.New(slog.NewTextHandler(&log, nil)))
		w.d.now = func() time.Time { return w.now }
		w.tickUTC("2026-10-14T12:59:00Z")
		w.tickUTC("2026-10-14T13:00:00Z")
		w.expect([]string{"2026-10-13"}, 1)
		if e := w.sent()[0]; e.Zone != "America/New_York" {
			t.Errorf("BOBRES_TIMEZONE: %+v", e)
		}
		// A setting that is no zone (valid when it was saved) falls through to the variable;
		// the worker says so once, not every minute.
		w.setting("general.timezone", "Mars/Base", time.Now())
		w.tickUTC("2026-10-15T13:00:00Z")
		w.tickUTC("2026-10-15T13:01:00Z")
		w.tickUTC("2026-10-15T13:02:00Z")
		w.expect([]string{"2026-10-13", "2026-10-14"}, 2)
		if n := strings.Count(log.String(), "not a time zone"); n != 1 {
			t.Errorf("logged %d times:\n%s", n, log.String())
		}
	})
	t.Run("Tehran when nothing is set", func(t *testing.T) {
		w := newSummaryWorld(t, "", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		w.tickUTC("2026-10-14T05:29:00Z") // 08:59 in Tehran
		w.nothing()
		w.tickUTC("2026-10-14T05:30:00Z")
		w.expect([]string{"2026-10-13"}, 1)
		if e := w.sent()[0]; e.Zone != "Asia/Tehran" {
			t.Errorf("%+v", e)
		}
	})
}

// The days clocks change: a time that does not exist (02:30 when clocks go from
// 02:00 to 03:00) is sent once, when the clocks land after it; a time that happens
// twice (01:30 when they go back from 02:00 to 01:00) is sent the first time and
// not again; and the day between has 23 or 25 hours and holds the sales of all of
// them and no others.
func TestDailySummaryOnDaylightSavingDays(t *testing.T) {
	t.Run("clocks go forward", func(t *testing.T) {
		w := newSummaryWorld(t, "America/New_York", "")
		owner(t, w.svc)
		w.on("2026-03-01 08:00")
		w.clock("02:30", "2026-03-01 08:00")
		// 2026-03-08: 02:00 EST (07:00 UTC) becomes 03:00 EDT.
		w.tickUTC("2026-03-08T06:59:00Z") // 01:59 EST
		w.nothing()
		w.tickUTC("2026-03-08T07:00:00Z") // 03:00 EDT: 02:30 was skipped, so it is due
		w.expect([]string{"2026-03-07"}, 1)
		w.tickUTC("2026-03-08T07:30:00Z")
		w.tickUTC("2026-03-08T16:30:00Z")
		w.restart()
		w.tickUTC("2026-03-08T20:00:00Z")
		w.expect([]string{"2026-03-07"}, 1)

		// The 23-hour day, summed up the next day.
		m := &ledgerModel{loc: w.loc}
		cust, plan := w.customer(9101), w.plan("IRT")
		m.paid(w, cust, plan, "IRT", 100, utc(t, "2026-03-08T04:59:00Z")) // 23:59 on the 7th
		m.paid(w, cust, plan, "IRT", 200, utc(t, "2026-03-08T05:00:00Z")) // 00:00 on the 8th
		m.paid(w, cust, plan, "IRT", 400, utc(t, "2026-03-09T03:59:00Z")) // 23:59 on the 8th
		m.paid(w, cust, plan, "IRT", 800, utc(t, "2026-03-09T04:00:00Z")) // 00:00 on the 9th
		w.clock("09:00", "2026-03-08 20:00")
		w.tickUTC("2026-03-09T12:59:00Z") // 08:59 EDT
		w.expect([]string{"2026-03-07"}, 1)
		w.tickUTC("2026-03-09T13:00:00Z")
		w.expect([]string{"2026-03-07", "2026-03-08"}, 2)
		got := w.sent()[1]
		if got.Date != "2026-03-08" || len(got.Currencies) != 1 || got.Currencies[0].Revenue != 200+400 || got.Currencies[0].PrevRevenue != 100 {
			t.Errorf("the 23-hour day: %+v", got)
		}
		m.check(t, got, "2026-03-08")
	})
	t.Run("clocks go back", func(t *testing.T) {
		w := newSummaryWorld(t, "America/New_York", "")
		owner(t, w.svc)
		w.on("2026-10-20 08:00")
		w.clock("01:30", "2026-10-20 08:00")
		// 2026-11-01: 02:00 EDT (06:00 UTC) becomes 01:00 EST, so 01:30 happens twice.
		w.tickUTC("2026-11-01T05:29:00Z") // 01:29 EDT
		w.nothing()
		w.tickUTC("2026-11-01T05:30:00Z") // 01:30 EDT, the first time
		w.expect([]string{"2026-10-31"}, 1)
		w.tickUTC("2026-11-01T06:29:00Z") // 01:29 EST
		w.tickUTC("2026-11-01T06:30:00Z") // 01:30 EST, the second time
		w.restart()
		w.tickUTC("2026-11-01T06:31:00Z")
		w.tickUTC("2026-11-01T15:00:00Z")
		w.expect([]string{"2026-10-31"}, 1)

		// The 25-hour day.
		m := &ledgerModel{loc: w.loc}
		cust, plan := w.customer(9102), w.plan("IRT")
		m.paid(w, cust, plan, "IRT", 100, utc(t, "2026-11-01T03:59:00Z"))  // 23:59 on 31 October
		m.paid(w, cust, plan, "IRT", 200, utc(t, "2026-11-01T04:00:00Z"))  // 00:00 on 1 November
		m.paid(w, cust, plan, "IRT", 400, utc(t, "2026-11-01T06:30:00Z"))  // 01:30 EST, the second one
		m.paid(w, cust, plan, "IRT", 800, utc(t, "2026-11-02T04:59:00Z"))  // 23:59 EST on 1 November
		m.paid(w, cust, plan, "IRT", 1600, utc(t, "2026-11-02T05:00:00Z")) // 00:00 on 2 November
		w.clock("09:00", "2026-11-01 15:00")
		w.tickUTC("2026-11-02T13:59:00Z") // 08:59 EST
		w.expect([]string{"2026-10-31"}, 1)
		w.tickUTC("2026-11-02T14:00:00Z")
		w.expect([]string{"2026-10-31", "2026-11-01"}, 2)
		got := w.sent()[1]
		if got.Date != "2026-11-01" || got.Currencies[0].Revenue != 200+400+800 || got.Currencies[0].PrevRevenue != 100 {
			t.Errorf("the 25-hour day: %+v", got)
		}
		m.check(t, got, "2026-11-01")
	})
}

// ledgerModel keeps every row a test writes as plain Go values as well, and its
// expected numbers come from those values (Go's own time zone arithmetic, no SQL
// and none of the report's code), so a wrong day edge, a wrong zone or two
// currencies added together shows up as a different number.
type ledgerModel struct {
	loc       *time.Location
	purchases []mPurchase
	refunds   []mFlow
	topups    []mFlow
	rejected  []mFlow // by staff
	gateway   []mFlow // failed or expired at a gateway
	trials    []time.Time
}

type mPurchase struct {
	user, cur, typ string
	amt            int64
	at             time.Time
}

type mFlow struct {
	cur string
	amt int64
	at  time.Time
}

func (m *ledgerModel) day(t time.Time) string { return t.In(m.loc).Format("2006-01-02") }

// expected is what the summary of day must say, from the model alone.
func (m *ledgerModel) expected(day string) events.DailySummaryEvent {
	prev, err := time.Parse("2006-01-02", day)
	if err != nil {
		panic(err)
	}
	before := prev.AddDate(0, 0, -1).Format("2006-01-02")
	cur := map[string]*events.DailySummaryCurrency{}
	get := func(c string) *events.DailySummaryCurrency {
		if cur[c] == nil {
			cur[c] = &events.DailySummaryCurrency{Currency: c}
		}
		return cur[c]
	}
	first := map[string]time.Time{}
	for _, p := range m.purchases {
		if f, ok := first[p.user]; !ok || p.at.Before(f) {
			first[p.user] = p.at
		}
		switch m.day(p.at) {
		case day:
			c := get(p.cur)
			c.Revenue += p.amt
			c.Orders++
			switch p.typ {
			case "new":
				c.NewOrders++
			case "renew":
				c.Renewals++
			case "traffic_topup":
				c.TrafficTopups++
			}
		case before:
			get(p.cur).PrevRevenue += p.amt
		}
	}
	add := func(flows []mFlow, f func(c *events.DailySummaryCurrency) *events.DailySummaryFlow) {
		for _, x := range flows {
			if m.day(x.at) == day {
				fl := f(get(x.cur))
				fl.Count++
				fl.Amount += x.amt
			}
		}
	}
	add(m.refunds, func(c *events.DailySummaryCurrency) *events.DailySummaryFlow { return &c.Refunds })
	add(m.topups, func(c *events.DailySummaryCurrency) *events.DailySummaryFlow { return &c.Topups })
	add(m.rejected, func(c *events.DailySummaryCurrency) *events.DailySummaryFlow { return &c.Rejected })
	add(m.gateway, func(c *events.DailySummaryCurrency) *events.DailySummaryFlow { return &c.GatewayFailed })
	e := events.DailySummaryEvent{Date: day}
	for _, at := range first {
		if m.day(at) == day {
			e.NewCustomers++
		}
	}
	for _, at := range m.trials {
		if m.day(at) == day {
			e.Trials++
		}
	}
	var names []string
	for name := range cur {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := cur[name]
		c.Net = c.Revenue - c.Refunds.Amount
		// A currency is told when the day did something in it; one that only had sales the
		// day before is not (the day before is only there for the comparison).
		if c.Revenue != 0 || c.Orders > 0 || c.Refunds.Count > 0 || c.Topups.Count > 0 || c.Rejected.Count > 0 || c.GatewayFailed.Count > 0 {
			e.Currencies = append(e.Currencies, *c)
		}
	}
	return e
}

// check compares an event's figures with the model's, whatever else it carries.
func (m *ledgerModel) check(t *testing.T, got events.DailySummaryEvent, day string) {
	t.Helper()
	want := m.expected(day)
	got.TelegramID, got.Language, got.Zone = 0, "", ""
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", " ")
		wj, _ := json.MarshalIndent(want, "", " ")
		t.Errorf("the summary of %s\n got %s\nwant %s", day, gj, wj)
	}
}

func (w *summaryWorld) customer(tg int64) string {
	w.t.Helper()
	return seedUser(w.t, w.s, tg).ID
}

func (w *summaryWorld) plan(cur string) string {
	w.t.Helper()
	p := &store.Plan{NameI18n: map[string]string{"en": "P " + cur}, Kind: "both", Price: 1000, Currency: cur, Enabled: true}
	if err := w.s.UpsertPlan(w.ctx, w.s.Conn(), p); err != nil {
		w.t.Fatal(err)
	}
	return p.ID
}

// paid writes a paid order the way the wallet does (the order, its purchase
// entry) at an instant of the test's choosing, and records it in the model.
func (m *ledgerModel) paid(w *summaryWorld, user, plan, cur string, amt int64, at time.Time) string {
	return m.paidAs(w, user, plan, "new", cur, amt, at)
}

func (m *ledgerModel) paidAs(w *summaryWorld, user, plan, typ, cur string, amt int64, at time.Time) string {
	w.t.Helper()
	var sub *string
	if typ != "new" {
		s, ok := w.subs[user]
		if !ok {
			w.t.Fatalf("%s has no service to %s", user, typ)
		}
		sub = &s
	}
	id := buuid.MustV7().String()
	w.exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, subscription_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'active', $5, $6, $7, $8, $9, $9)`, id, user, plan, typ, amt, cur, "k-"+id, sub, at)
	if typ == "new" && w.subs[user] == "" {
		sid := buuid.MustV7().String()
		w.exec(`INSERT INTO core.subscriptions (id, user_id, order_id, server_id, client_email, status) VALUES ($1, $2, $3, $4, $5, 'active')`,
			sid, user, id, buuid.MustV7().String(), "c-"+sid)
		w.subs[user] = sid
	}
	w.ledger(user, cur, -amt, "purchase", "order", id, "purchase:"+id, at)
	m.purchases = append(m.purchases, mPurchase{user: user, cur: cur, typ: typ, amt: amt, at: at})
	return id
}

func (w *summaryWorld) ledger(user, cur string, amt int64, kind, refType, refID, key string, at time.Time) {
	w.t.Helper()
	w.exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 0, $9)`, buuid.MustV7().String(), user, cur, amt, kind, refType, refID, key, at)
}

func (m *ledgerModel) refund(w *summaryWorld, user, order, cur string, amt int64, at time.Time) {
	w.t.Helper()
	w.ledger(user, cur, amt, "refund", "order", order, "refund:"+order, at)
	w.exec(`UPDATE core.orders SET refunded_at = $2, refund_amount = $3, updated_at = $2 WHERE id = $1`, order, at, amt)
	m.refunds = append(m.refunds, mFlow{cur, amt, at})
}

// topup is a payment that stays in the wallet whole: no order is paid from it at
// the same instant.
func (m *ledgerModel) topup(w *summaryWorld, user, cur string, amt int64, at time.Time) {
	w.t.Helper()
	id := buuid.MustV7().String()
	w.ledger(user, cur, amt, "topup", "intent", id, "pay-in:"+id, at)
	m.topups = append(m.topups, mFlow{cur, amt, at})
}

func (m *ledgerModel) trial(w *summaryWorld, user, plan string, at time.Time) {
	w.t.Helper()
	id := buuid.MustV7().String()
	w.exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, created_at, updated_at)
		VALUES ($1, $2, $3, 'new', 'paid', 0, 'IRT', $4, $5, $5)`, id, user, plan, "k-"+id, at)
	w.ledger(user, "IRT", 0, "trial_grant", "order", id, "trial_grant:"+id, at)
	m.trials = append(m.trials, at)
}

func (m *ledgerModel) reject(w *summaryWorld, provider, cur string, amt int64, at time.Time) {
	w.t.Helper()
	payload, _ := json.Marshal(map[string]any{"intent_id": buuid.MustV7().String(), "provider": provider, "amount": amt, "currency": cur, "reason": "x"})
	w.exec(`INSERT INTO core.outbox_core (topic, payload, event_id, created_at) VALUES ('payment.rejected.v1', $1::text::jsonb, $2, $3)`,
		string(payload), buuid.MustV7().String(), at)
	if provider == "manual_card" {
		m.rejected = append(m.rejected, mFlow{cur, amt, at})
	} else {
		m.gateway = append(m.gateway, mFlow{cur, amt, at})
	}
}

// The event says what the sales report says for that local day, in the cent: two
// currencies that are never added, renewals and a refund, top-ups, rejections by
// staff and failed gateway payments apart, new customers (the first purchase) and
// free trials, the day before for the comparison, a currency that only sold the
// day before left out, and sales a minute either side of the day's edges on the
// right sides of them.
func TestDailySummaryFiguresAreTheLedgers(t *testing.T) {
	w := newSummaryWorld(t, "America/New_York", "")
	owner(t, w.svc)
	m := &ledgerModel{loc: w.loc}
	a, b, c, d := w.customer(9201), w.customer(9202), w.customer(9203), w.customer(9204)
	irt, usdt, eur := w.plan("IRT"), w.plan("USDT"), w.plan("EUR")
	trial := w.plan("IRT")

	// The 13th in New York (EDT, UTC-4) runs from 04:00 UTC on the 13th to 04:00 UTC on the 14th.
	a1 := m.paid(w, a, irt, "IRT", 700, utc(t, "2026-10-13T03:59:00Z")) // 23:59 on the 12th: the day before
	m.paid(w, a, irt, "IRT", 900, utc(t, "2026-10-13T04:00:00Z"))       // 00:00 on the 13th: the day itself; a is not a new customer
	m.paid(w, b, irt, "IRT", 1000, utc(t, "2026-10-13T12:00:00Z"))      // b's first: a new customer
	m.paidAs(w, b, irt, "renew", "IRT", 300, utc(t, "2026-10-13T15:00:00Z"))
	m.paid(w, c, usdt, "USDT", 5_000_000, utc(t, "2026-10-14T03:59:00Z")) // 23:59 on the 13th: c's first, a new customer
	m.paid(w, c, usdt, "USDT", 2_000_000, utc(t, "2026-10-14T04:00:00Z")) // 00:00 on the 14th: after the day
	m.paid(w, d, eur, "EUR", 300, utc(t, "2026-10-12T12:00:00Z"))         // only the day before: not in the 13th's summary
	m.refund(w, a, a1, "IRT", 700, utc(t, "2026-10-13T20:00:00Z"))        // refunded on the 13th, though bought on the 12th
	m.topup(w, b, "IRT", 5000, utc(t, "2026-10-13T10:00:00Z"))
	m.topup(w, b, "IRT", 9999, utc(t, "2026-10-14T04:00:00Z")) // the 14th
	m.trial(w, w.customer(9205), trial, utc(t, "2026-10-13T08:00:00Z"))
	m.reject(w, "manual_card", "IRT", 4000, utc(t, "2026-10-13T09:00:00Z"))
	m.reject(w, "zarinpal", "IRT", 1500, utc(t, "2026-10-13T11:00:00Z"))
	m.reject(w, "stars", "IRT", 100, utc(t, "2026-10-14T05:00:00Z")) // the 14th

	staff := w.staff(9210, "admin", "en")
	w.on("2026-10-01 08:00")
	w.tick("2026-10-14 09:00")
	w.expect([]string{"2026-10-13"}, 2)
	for _, e := range w.sent() {
		if e.Date != "2026-10-13" || e.Zone != "America/New_York" {
			t.Errorf("%+v", e)
		}
		m.check(t, e, "2026-10-13")
	}
	// Spelled out once, so the model itself is held to account.
	want := events.DailySummaryEvent{Date: "2026-10-13", NewCustomers: 2, Trials: 1, Currencies: []events.DailySummaryCurrency{
		{
			Currency: "IRT", Revenue: 2200, PrevRevenue: 700, Orders: 3, NewOrders: 2, Renewals: 1,
			Refunds: events.DailySummaryFlow{Count: 1, Amount: 700}, Net: 1500, Topups: events.DailySummaryFlow{Count: 1, Amount: 5000},
			Rejected: events.DailySummaryFlow{Count: 1, Amount: 4000}, GatewayFailed: events.DailySummaryFlow{Count: 1, Amount: 1500},
		},
		{Currency: "USDT", Revenue: 5_000_000, Orders: 1, NewOrders: 1, Net: 5_000_000},
	}}
	if got := m.expected("2026-10-13"); !reflect.DeepEqual(got, want) {
		t.Errorf("the model says %+v\nwant %+v", got, want)
	}
	got := w.sent()
	if got[0].TelegramID != ownerTG || got[1].TelegramID != staff.TelegramID {
		t.Errorf("recipients %+v", got)
	}
	// What the sales report itself says for that day is the same (the event is its summary, not another count).
	rng, err := w.svc.ReportRange(w.ctx, ReportRequest{From: "2026-10-13", To: "2026-10-13"}, w.now)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := w.svc.SalesReport(w.ctx, rng)
	if err != nil {
		t.Fatal(err)
	}
	if irt := rep.Totals.Currencies["IRT"]; irt.Revenue != got[0].Currencies[0].Revenue || irt.Refunds.Amount != got[0].Currencies[0].Refunds.Amount ||
		rep.Totals.NewCustomers != got[0].NewCustomers || rep.Totals.Trials != got[0].Trials {
		t.Errorf("the report: %+v", rep.Totals)
	}
}

// A day with nothing to report is still announced, as an event with no
// currencies (the bot says it in one line: the owner wants to see the job is
// alive); a day with only people (a free trial) has no currency either.
func TestDailySummaryOfADayWithNoSales(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	owner(t, w.svc)
	w.on("2026-10-01 08:00")
	w.tick("2026-10-14 09:00")
	w.expect([]string{"2026-10-13"}, 1)
	if e := w.sent()[0]; e.Date != "2026-10-13" || len(e.Currencies) != 0 || e.NewCustomers != 0 || e.Trials != 0 {
		t.Errorf("a quiet day: %+v", e)
	}
	// A free trial is not a sale: the day has people to name and no currency's block.
	m := &ledgerModel{loc: w.loc}
	m.trial(w, w.customer(9301), w.plan("IRT"), w.at("2026-10-14 10:00"))
	w.tick("2026-10-15 09:00")
	e := w.sent()[1]
	if e.Date != "2026-10-14" || len(e.Currencies) != 0 || e.Trials != 1 {
		t.Errorf("a day with a trial and no sale: %+v", e)
	}
}

// Who is told: the active owners and admins (what reports.read says), each in their
// own language; support staff, customers, a banned admin and anyone demoted or
// removed before the day is claimed are not.
func TestDailySummaryGoesToWhoMayReadTheReports(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	o := owner(t, w.svc)
	admin := w.staff(9401, "admin", "en")
	w.staff(9402, "support", "en")
	w.staff(9403, "user", "en")
	demoted := w.staff(9404, "admin", "fa")
	removed := w.staff(9405, "admin", "fa")
	banned := w.staff(9406, "admin", "fa")
	w.on("2026-10-01 08:00")
	// Before the due time, some lose the permission.
	if err := w.s.SetUserRole(w.ctx, w.s.Conn(), demoted.ID, "support", "active"); err != nil {
		t.Fatal(err)
	}
	if err := w.s.SetUserRole(w.ctx, w.s.Conn(), removed.ID, "user", "active"); err != nil {
		t.Fatal(err)
	}
	if err := w.s.SetUserRole(w.ctx, w.s.Conn(), banned.ID, "admin", "banned"); err != nil {
		t.Fatal(err)
	}
	w.tick("2026-10-14 09:00")
	got := w.sent()
	var to []string
	for _, e := range got {
		to = append(to, fmt.Sprintf("%d/%s", e.TelegramID, e.Language))
	}
	want := []string{fmt.Sprintf("%d/fa", o.TelegramID), fmt.Sprintf("%d/en", admin.TelegramID)}
	if !reflect.DeepEqual(to, want) {
		t.Errorf("told %v, want %v", to, want)
	}
	var recipients int
	if err := w.s.DB().QueryRow(w.ctx, `SELECT recipients FROM core.daily_summary_claims`).Scan(&recipients); err != nil || recipients != 2 {
		t.Errorf("the claim says %d recipients (%v)", recipients, err)
	}
	// The permission table decides, not a list of roles: were support allowed to read the
	// reports they would be told too.
	w.exec(`DELETE FROM core.daily_summary_claims`)
	w.exec(`DELETE FROM core.outbox_core`)
	w.d = w.svc.NewDailySummary(func(role string) bool { return role != "user" }, nil)
	w.d.now = func() time.Time { return w.now }
	w.tick("2026-10-14 09:01")
	if n := len(w.sent()); n != 4 { // owner, admin, support, the demoted one (now support); not the customer, the removed one or the banned one
		t.Errorf("with support allowed: %d told", n)
	}
}

// Nobody to tell: nothing is claimed, so whoever becomes staff later the same
// day still gets it; and nothing is written for a store with no staff at all.
func TestDailySummaryWithNobodyToTell(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	w.on("2026-10-01 08:00")
	w.staff(9501, "support", "en")
	w.tick("2026-10-14 09:00")
	w.tick("2026-10-14 12:00")
	w.nothing()
	a := w.staff(9502, "admin", "en")
	w.tick("2026-10-14 12:01")
	w.expect([]string{"2026-10-13"}, 1)
	if e := w.sent()[0]; e.TelegramID != a.TelegramID {
		t.Errorf("%+v", e)
	}
}

// The claim and the events commit together: a transaction that fails after the
// claim leaves neither, and the next minute sends it.
func TestDailySummaryClaimAndEventsAreOneTransaction(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	owner(t, w.svc)
	w.on("2026-10-01 08:00")
	// The outbox refuses this topic, as a database that fails mid-way would.
	w.exec(`CREATE OR REPLACE FUNCTION core.refuse_daily_summary() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'refused for the test'; END $$`)
	w.exec(`CREATE TRIGGER refuse_daily_summary BEFORE INSERT ON core.outbox_core
		FOR EACH ROW WHEN (NEW.topic = 'report.daily_summary.v1') EXECUTE FUNCTION core.refuse_daily_summary()`)
	dropped := false
	drop := func() {
		if !dropped {
			dropped = true
			w.exec(`DROP TRIGGER refuse_daily_summary ON core.outbox_core`)
			w.exec(`DROP FUNCTION core.refuse_daily_summary()`)
		}
	}
	t.Cleanup(drop)
	w.now = w.at("2026-10-14 09:00")
	if err := w.d.Step(w.ctx); err == nil || !strings.Contains(err.Error(), "refused for the test") {
		t.Fatalf("a failing outbox: %v", err)
	}
	w.nothing() // no claim without its events: the summary is not lost
	drop()
	w.tick("2026-10-14 09:01") // a failed attempt is not repeated at once (see the back-off test)
	w.nothing()
	w.tick("2026-10-14 09:05")
	w.expect([]string{"2026-10-13"}, 1) // and not sent twice
	w.tick("2026-10-14 09:06")
	w.tick("2026-10-14 09:20")
	w.expect([]string{"2026-10-13"}, 1)
}

// Two workers (core is one process, but a restart can overlap a slow one) stepping
// at the same instant send the day once.
func TestDailySummaryTwoWorkersSendItOnce(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	owner(t, w.svc)
	w.staff(9601, "admin", "en")
	w.on("2026-10-01 08:00")
	w.now = w.at("2026-10-14 09:00")
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		d := w.svc.NewDailySummary(holdsReportsRead, nil)
		d.now = func() time.Time { return w.now }
		wg.Go(func() { errs[i] = d.Step(w.ctx) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	w.expect([]string{"2026-10-13"}, 2)
}

// The two settings take what staff type and keep what the worker reads.
func TestDailySummarySettingsNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"09:00": "09:00", "9:05": "09:05", " 23:59 ": "23:59", "00:00": "00:00", "۰۹:۳۰": "09:30", "٩:٤٥": "09:45", "": "",
	} {
		if got, err := NormalizeSetting(SettingDailySummaryTime, in); err != nil || got != want {
			t.Errorf("time %q: %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, bad := range []string{"24:00", "9", "09:60", "9:5", "09-00", "abc", "09:00:00", "9.30", "-1:00", "0900", "12:30 pm"} {
		var fe *FieldError
		if _, err := NormalizeSetting(SettingDailySummaryTime, bad); !errors.As(err, &fe) || fe.Field != SettingDailySummaryTime {
			t.Errorf("time %q accepted: %v", bad, err)
		}
	}
	for in, want := range map[string]string{"true": "true", "off": "false", "1": "true"} {
		if got, err := NormalizeSetting(SettingDailySummary, in); err != nil || got != want {
			t.Errorf("switch %q: %q, %v", in, got, err)
		}
	}
	if _, err := NormalizeSetting(SettingDailySummary, "maybe"); err == nil {
		t.Error("a switch that is neither true nor false")
	}
	// Through the service: the time is stored as HH:MM, and both are audited as settings are.
	svc, s := testService(t)
	o := owner(t, svc)
	if _, err := svc.SetSettings(context.Background(), o, map[string]string{SettingDailySummary: "yes", SettingDailySummaryTime: "7:30"}); err != nil {
		t.Fatal(err)
	}
	set, err := svc.Settings(context.Background())
	if err != nil || set[SettingDailySummary] != "true" || set[SettingDailySummaryTime] != "07:30" {
		t.Errorf("stored %v (%v)", set, err)
	}
	if n := auditCount(t, s, "setting.set"); n != 2 {
		t.Errorf("%d audit entries", n)
	}
	st, err := svc.DailySummaryStatus(context.Background())
	if err != nil || !st.Enabled || st.Time != "07:30" || st.Zone != "Asia/Tehran" {
		t.Errorf("status %+v (%v)", st, err)
	}
	if _, err := svc.SetSettings(context.Background(), o, map[string]string{SettingDailySummary: "", SettingDailySummaryTime: ""}); err != nil {
		t.Fatal(err)
	}
	if st, err = svc.DailySummaryStatus(context.Background()); err != nil || st.Enabled || st.Time != "09:00" {
		t.Errorf("status with the defaults %+v (%v)", st, err)
	}
}

// The edges of the pieces the worker is made of.
func TestSwitchedOnBefore(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Tehran")
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		since, now string
		due        int
		want       bool
	}{
		{"2026-10-14 08:59", "2026-10-14 15:00", 9 * 60, true},
		{"2026-10-14 09:00", "2026-10-14 15:00", 9 * 60, true},
		{"2026-10-14 09:01", "2026-10-14 15:00", 9 * 60, false},
		{"2026-10-13 23:59", "2026-10-14 00:01", 0, true}, // an earlier day, whatever the time
		{"2026-10-14 00:00", "2026-10-14 00:01", 0, true},
		{"2026-10-15 00:01", "2026-10-14 15:00", 9 * 60, false}, // from the future: a clock that went back
	} {
		if got := switchedOnBefore(at(c.since), at(c.now), c.due); got != c.want {
			t.Errorf("on at %s, now %s, due %d: %v", c.since, c.now, c.due, got)
		}
	}
	if _, err := summaryEvent("2026-10-13", "UTC", &SalesReport{Buckets: []ReportBucket{{Date: "2026-10-13"}}}); err == nil {
		t.Error("a report of one bucket cannot make a summary with a comparison")
	}
}

// failedAt runs the worker with the clock at local and returns what Step returns:
// an error means it tried and failed (nil: it did not try, or it worked).
func (w *summaryWorld) failedAt(local string) error {
	w.t.Helper()
	w.now = w.at(local)
	return w.d.Step(w.ctx)
}

// breakReport makes the sales report fail the way a broken database does (a table
// it reads is gone) until the returned function puts it back; the test cleanup
// does too.
func (w *summaryWorld) breakReport() (fix func()) {
	w.t.Helper()
	w.exec(`ALTER TABLE core.orders RENAME TO orders_gone_for_the_test`)
	fixed := false
	fix = func() {
		if !fixed {
			fixed = true
			w.exec(`ALTER TABLE core.orders_gone_for_the_test RENAME TO orders`)
		}
	}
	w.t.Cleanup(fix)
	return fix
}

// A summary that cannot be made (a statement timeout on a big ledger, a time zone
// the database does not know) is not made again every minute until midnight: the
// worker waits 5, 10 and then 15 minutes between attempts, gives up on the day
// after eight (and says so at error level), tries again the next day, and a
// restarted core starts afresh.
func TestDailySummaryBacksOffAfterAFailedAttempt(t *testing.T) {
	t.Run("eight attempts a day, then it gives up", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		fix := w.breakReport()
		var tried []string
		var last error
		for m := 0; m < 4*60; m++ { // 09:00 to 12:59, a tick a minute
			at := w.at("2026-10-14 09:00").Add(time.Duration(m) * time.Minute)
			w.now = at
			if err := w.d.Step(w.ctx); err != nil {
				tried, last = append(tried, at.In(w.loc).Format("15:04")), err
			}
		}
		// 5, 10, 15 and then 15 minutes after each failure.
		if want := []string{"09:00", "09:05", "09:15", "09:30", "09:45", "10:00", "10:15", "10:30"}; !reflect.DeepEqual(tried, want) {
			t.Fatalf("attempts at %v, want %v", tried, want)
		}
		if !errors.Is(last, errSummaryGaveUp) || !strings.Contains(last.Error(), "attempt 8 of 8") {
			t.Errorf("the last attempt's error: %v", last)
		}
		w.nothing()
		// The rest of the day it stays quiet, even with the report fixed.
		fix()
		for _, at := range []string{"2026-10-14 13:00", "2026-10-14 20:00", "2026-10-14 23:59"} {
			if err := w.failedAt(at); err != nil {
				t.Fatal(err)
			}
		}
		w.nothing()
		// The next day is its own: yesterday's (the 14th) goes at the usual time.
		w.tick("2026-10-15 09:00")
		w.expect([]string{"2026-10-14"}, 1)
	})
	t.Run("the first failure's error says when it tries again", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		w.breakReport()
		err := w.failedAt("2026-10-14 09:00")
		if err == nil || errors.Is(err, errSummaryGaveUp) || !strings.Contains(err.Error(), "attempt 1 of 8") || !strings.Contains(err.Error(), "5m0s") {
			t.Fatalf("%v", err)
		}
		if err := w.failedAt("2026-10-14 09:04"); err != nil {
			t.Fatalf("tried again after four minutes: %v", err)
		}
		if err := w.failedAt("2026-10-14 09:05"); err == nil || !strings.Contains(err.Error(), "attempt 2 of 8") || !strings.Contains(err.Error(), "10m0s") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("a fixed report goes out at the next attempt, once", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		fix := w.breakReport()
		if w.failedAt("2026-10-14 09:00") == nil || w.failedAt("2026-10-14 09:05") == nil {
			t.Fatal("the report should fail")
		}
		fix()
		w.tick("2026-10-14 09:14") // waiting until 09:15
		w.nothing()
		w.tick("2026-10-14 09:15")
		w.expect([]string{"2026-10-13"}, 1)
		w.tick("2026-10-14 09:16")
		w.tick("2026-10-14 15:00")
		w.expect([]string{"2026-10-13"}, 1)
	})
	t.Run("a restarted core tries at once", func(t *testing.T) {
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		fix := w.breakReport()
		if w.failedAt("2026-10-14 09:00") == nil {
			t.Fatal("the report should fail")
		}
		fix()
		w.restart()
		w.tick("2026-10-14 09:01")
		w.expect([]string{"2026-10-13"}, 1)
	})
}

// A day's figures are not read before 00:02, whatever time the store chose, so a
// purchase that began just before midnight and commits a moment after it is in the
// day it is dated, not missing from a summary the claim then makes final.
func TestDailySummaryWaitsTwoMinutesAfterMidnight(t *testing.T) {
	w := newSummaryWorld(t, "Asia/Tehran", "")
	owner(t, w.svc)
	w.on("2026-10-01 08:00")
	w.clock("00:00", "2026-10-01 08:00")
	for _, at := range []string{"2026-10-14 00:00", "2026-10-14 00:01"} {
		w.tick(at)
	}
	w.nothing()
	w.tick("2026-10-14 00:02")
	w.expect([]string{"2026-10-13"}, 1)
	w.tick("2026-10-14 00:03")
	w.expect([]string{"2026-10-13"}, 1)

	// The time shown on the Reports page stays the one the store chose.
	if st, err := w.svc.DailySummaryStatus(w.ctx); err != nil || st.Time != "00:00" {
		t.Errorf("status %+v (%v)", st, err)
	}

	// Switched on in the first minutes of the day, before the first read: it counts.
	w2 := newSummaryWorld(t, "Asia/Tehran", "")
	owner(t, w2.svc)
	w2.clock("00:00", "2026-10-01 08:00")
	w2.on("2026-10-14 00:01")
	w2.tick("2026-10-14 00:01")
	w2.nothing()
	w2.tick("2026-10-14 00:02")
	w2.expect([]string{"2026-10-13"}, 1)
	// ... and after the first read it starts tomorrow.
	w3 := newSummaryWorld(t, "Asia/Tehran", "")
	owner(t, w3.svc)
	w3.clock("00:00", "2026-10-01 08:00")
	w3.on("2026-10-14 00:03")
	w3.tick("2026-10-14 00:05")
	w3.nothing()
	w3.tick("2026-10-15 00:02")
	w3.expect([]string{"2026-10-14"}, 1)
}

// logged gives the world a worker that logs into the returned buffer.
func (w *summaryWorld) logged() *bytes.Buffer {
	var log bytes.Buffer
	w.d = w.svc.NewDailySummary(holdsReportsRead, slog.New(slog.NewTextHandler(&log, nil)))
	w.d.now = func() time.Time { return w.now }
	return &log
}

// revenues is each event's revenue in cur, in the order they were written.
func (w *summaryWorld) revenues(cur string) []int64 {
	w.t.Helper()
	out := []int64{}
	for _, e := range w.sent() {
		var v int64
		for _, c := range e.Currencies {
			if c.Currency == cur {
				v = c.Revenue
			}
		}
		out = append(out, v)
	}
	return out
}

// The store changes its time zone: the day edges move, so the summaries around the
// change are not a clean cut. The claim is by local date, so a sale can be in two
// summaries or in none; the worker says so in the log when it sends the first
// summary read in the new zone. (Neither case loses the claim's guarantee: no date
// is sent twice.)
func TestDailySummaryAcrossAChangeOfTimeZone(t *testing.T) {
	t.Run("a sale is in two summaries", func(t *testing.T) {
		// Tehran's 13 October is 12 Oct 20:30Z to 13 Oct 20:30Z; Auckland's 14th (UTC+13)
		// is 13 Oct 11:00Z to 14 Oct 11:00Z: a sale at 13 Oct 12:00Z is in both.
		w := newSummaryWorld(t, "Asia/Tehran", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		log := w.logged()
		m := &ledgerModel{loc: w.loc}
		m.paid(w, w.customer(9701), w.plan("IRT"), "IRT", 500, utc(t, "2026-10-13T12:00:00Z"))
		w.tickUTC("2026-10-14T05:30:00Z") // 09:00 in Tehran: the 13th
		w.expect([]string{"2026-10-13"}, 1)
		if strings.Contains(log.String(), "time zone changed") {
			t.Errorf("warned with no change:\n%s", log)
		}
		w.setting("general.timezone", "Pacific/Auckland", time.Now())
		w.tickUTC("2026-10-14T06:00:00Z") // 19:00 on the 14th in Auckland: the 13th is claimed
		w.expect([]string{"2026-10-13"}, 1)
		w.tickUTC("2026-10-14T19:59:00Z") // 08:59 on the 15th
		w.expect([]string{"2026-10-13"}, 1)
		w.tickUTC("2026-10-14T20:00:00Z") // 09:00: the 14th
		w.expect([]string{"2026-10-13", "2026-10-14"}, 2)
		if got := w.revenues("IRT"); !reflect.DeepEqual(got, []int64{500, 500}) {
			t.Errorf("revenue in each summary: %v", got)
		}
		if got := w.sent()[1]; got.Date != "2026-10-14" || got.Zone != "Pacific/Auckland" {
			t.Errorf("%+v", got)
		}
		if l := log.String(); !strings.Contains(l, "time zone changed") || !strings.Contains(l, "from=Asia/Tehran") || !strings.Contains(l, "to=Pacific/Auckland") {
			t.Errorf("the change is not in the log:\n%s", l)
		}
		if n := strings.Count(log.String(), "time zone changed"); n != 1 {
			t.Errorf("warned %d times", n)
		}
		// Later days in the new zone say nothing more about it.
		w.tickUTC("2026-10-15T20:00:00Z")
		w.expect([]string{"2026-10-13", "2026-10-14", "2026-10-15"}, 3)
		if n := strings.Count(log.String(), "time zone changed"); n != 1 {
			t.Errorf("warned again: %d times", n)
		}
	})
	t.Run("a sale is in none", func(t *testing.T) {
		// Auckland sent the 14th (13 Oct 11:00Z to 14 Oct 11:00Z). The store moves to Los
		// Angeles (UTC-7) before its 15th, where the 14th is 14 Oct 07:00Z to 15 Oct 07:00Z:
		// the 14th is claimed, so it is not sent again, and a sale at 14 Oct 15:00Z, after
		// Auckland's 14th and before Los Angeles's 15th, is in no summary.
		w := newSummaryWorld(t, "Pacific/Auckland", "")
		owner(t, w.svc)
		w.on("2026-10-01 08:00")
		log := w.logged()
		m := &ledgerModel{loc: w.loc}
		m.paid(w, w.customer(9702), w.plan("IRT"), "IRT", 700, utc(t, "2026-10-14T15:00:00Z"))
		w.tickUTC("2026-10-14T20:00:00Z") // 09:00 on the 15th in Auckland: the 14th
		w.expect([]string{"2026-10-14"}, 1)
		w.setting("general.timezone", "America/Los_Angeles", time.Now())
		w.tickUTC("2026-10-15T12:00:00Z") // 05:00 on the 15th in Los Angeles: not due
		w.tickUTC("2026-10-15T16:00:00Z") // 09:00: yesterday, the 14th, is claimed
		w.expect([]string{"2026-10-14"}, 1)
		w.tickUTC("2026-10-16T16:00:00Z") // 09:00 on the 16th: the 15th
		w.expect([]string{"2026-10-14", "2026-10-15"}, 2)
		for i, v := range w.revenues("IRT") {
			if v != 0 {
				t.Errorf("summary %d holds the sale that was between the days: %d", i, v)
			}
		}
		if l := log.String(); !strings.Contains(l, "time zone changed") || !strings.Contains(l, "from=Pacific/Auckland") || !strings.Contains(l, "to=America/Los_Angeles") {
			t.Errorf("the change is not in the log:\n%s", l)
		}
	})
}

// Saving the settings page again with the same values (a click on Save) does not
// move when the switch was turned on, which decides whether today's summary is
// still due; a real change does, for the setting that changed only.
func TestDailySummaryResavingTheSettingsKeepsWhenItWasSwitchedOn(t *testing.T) {
	svc, s := testService(t)
	o := owner(t, svc)
	ctx := context.Background()
	both := map[string]string{SettingDailySummary: "true", SettingDailySummaryTime: "09:00"}
	if _, err := svc.SetSettings(ctx, o, both); err != nil {
		t.Fatal(err)
	}
	week := time.Now().Add(-7 * 24 * time.Hour).Truncate(time.Second)
	if _, err := s.DB().Exec(ctx, `UPDATE core.settings SET updated_at = $1 WHERE key IN ($2, $3)`, week, SettingDailySummary, SettingDailySummaryTime); err != nil {
		t.Fatal(err)
	}
	stamp := func(key string) time.Time {
		t.Helper()
		at, found, err := s.SettingChangedAt(ctx, s.Conn(), key)
		if err != nil || !found {
			t.Fatalf("%s: %v, %v", key, found, err)
		}
		return at
	}
	if _, err := svc.SetSettings(ctx, o, both); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{SettingDailySummary, SettingDailySummaryTime} {
		if at := stamp(k); !at.Equal(week) {
			t.Errorf("an unchanged save moved %s to %s (was %s)", k, at, week)
		}
	}
	// The same value typed another way is the same value.
	if _, err := svc.SetSettings(ctx, o, map[string]string{SettingDailySummary: "yes", SettingDailySummaryTime: "9:00"}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{SettingDailySummary, SettingDailySummaryTime} {
		if at := stamp(k); !at.Equal(week) {
			t.Errorf("an equal value moved %s to %s", k, at)
		}
	}
	// A new time moves its own stamp and not the switch's.
	if _, err := svc.SetSettings(ctx, o, map[string]string{SettingDailySummary: "true", SettingDailySummaryTime: "10:00"}); err != nil {
		t.Fatal(err)
	}
	if at := stamp(SettingDailySummary); !at.Equal(week) {
		t.Errorf("a new time moved the switch to %s", at)
	}
	if at := stamp(SettingDailySummaryTime); !at.After(week) {
		t.Errorf("a new time did not move its own stamp: %s", at)
	}
	// Off and on again is a new switching on.
	for _, v := range []string{"false", "true"} {
		if _, err := svc.SetSettings(ctx, o, map[string]string{SettingDailySummary: v}); err != nil {
			t.Fatal(err)
		}
	}
	if at := stamp(SettingDailySummary); !at.After(week) {
		t.Errorf("switching it off and on did not move the switch: %s", at)
	}
}
