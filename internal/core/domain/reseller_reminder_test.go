package domain

import (
	"sync"
	"testing"
	"time"
)

// The weekly reminder is run here the way core runs it, one Step a minute, with a clock the test sets: the due time, a restart, two claimers,
// an outage, a late switch, a moved weekday and the days clocks change are each a few ticks.

type reminderWorld struct {
	*resellerWorld
	r   *ResellerReminder
	loc *time.Location
	now time.Time
}

func newReminderWorld(t *testing.T, zone string) *reminderWorld {
	t.Helper()
	w := &reminderWorld{resellerWorld: newResellerWorld(t)}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	w.loc = loc
	w.setting("general.timezone", zone, time.Now().Add(-30*24*time.Hour))
	w.restart()
	return w
}

// restart is a new worker (core restarted): it remembers nothing.
func (w *reminderWorld) restart() {
	w.r = w.svc.NewResellerReminder(nil)
	w.r.now = func() time.Time { return w.now }
}

func (w *reminderWorld) at(local string) time.Time {
	w.t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", local, w.loc)
	if err != nil {
		w.t.Fatal(err)
	}
	return v
}

// setting writes a setting as of when it was changed (the worker reads when the switch was turned on).
func (w *reminderWorld) setting(key, value string, changed time.Time) {
	w.t.Helper()
	if _, err := w.s.DB().Exec(w.ctx, `INSERT INTO core.settings (key, value, updated_at) VALUES ($1, to_jsonb($2::text), $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, key, value, changed); err != nil {
		w.t.Fatal(err)
	}
}

func (w *reminderWorld) on(local string) { w.setting(SettingReminderEnabled, "true", w.at(local)) }

func (w *reminderWorld) tick(local string) {
	w.t.Helper()
	w.now = w.at(local)
	if err := w.r.Step(w.ctx); err != nil {
		w.t.Fatalf("step at %s: %v", local, err)
	}
}

func (w *reminderWorld) tickUTC(instant string) {
	w.t.Helper()
	v, err := time.Parse(time.RFC3339, instant)
	if err != nil {
		w.t.Fatal(err)
	}
	w.now = v
	if err := w.r.Step(w.ctx); err != nil {
		w.t.Fatalf("step at %s: %v", instant, err)
	}
}

// claims are the days claimed, oldest first.
func (w *reminderWorld) claims() []string {
	w.t.Helper()
	rows, err := w.s.DB().Query(w.ctx, `SELECT day::text FROM core.reseller_reminder_claims ORDER BY day`)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func (w *reminderWorld) wantClaims(when string, want ...string) {
	w.t.Helper()
	got := w.claims()
	if len(got) != len(want) {
		w.t.Fatalf("%s: claims %v, want %v", when, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			w.t.Fatalf("%s: claims %v, want %v", when, got, want)
		}
	}
}

func (w *reminderWorld) broadcasts() int {
	w.t.Helper()
	var n int
	if err := w.s.DB().QueryRow(w.ctx, `SELECT count(*) FROM core.broadcasts WHERE kind = 'reminder'`).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// Off (the default) sends nothing, whatever the clock says.
func TestReminderOffSendsNothing(t *testing.T) {
	w := newReminderWorld(t, "Asia/Tehran")
	w.reseller(9601, w.gold, "fa", "a", 100_000)
	w.tick("2026-10-29 21:00")
	w.setting(SettingReminderEnabled, "false", w.at("2026-10-01 10:00"))
	w.tick("2026-10-29 21:01")
	w.wantClaims("off")
	if n := w.broadcasts(); n != 0 {
		t.Fatalf("reminders: %d", n)
	}
}

// Thursday 21:00 store time (the defaults): once, not before, not again after a restart; the resellers in debt get it in their own language
// with their debt, and the next Thursday sends the next one.
func TestReminderWeekly(t *testing.T) {
	w := newReminderWorld(t, "Asia/Tehran")
	w.reseller(9611, w.gold, "fa", "ali", 125_000)
	w.reseller(9612, w.plain, "en", "bob", 40_000)
	w.reseller(9613, w.gold, "en", "cat", 0) // owes nothing
	w.on("2026-10-20 10:00")

	w.tick("2026-10-29 20:59") // a Thursday
	w.wantClaims("before the time")
	w.tick("2026-10-29 21:00")
	w.wantClaims("at the time", "2026-10-29")
	w.tick("2026-10-29 21:01")
	w.restart()
	w.tick("2026-10-29 21:02")
	w.tick("2026-10-30 21:00")
	w.wantClaims("the same week", "2026-10-29")
	if n := w.broadcasts(); n != 1 {
		t.Fatalf("reminders: %d", n)
	}
	a := reminderAudit(t, w.s)
	if a["day"] != "2026-10-29" || a["zone"] != "Asia/Tehran" || a["debt_only"] != true || a["kind"] != "reminder" {
		t.Errorf("audit: %v", a)
	}

	st, err := w.svc.ResellerReminderStatus(w.ctx)
	if err != nil || st.Last == nil || st.Last.Day != "2026-10-29" || st.Last.Broadcast == nil || st.Last.Broadcast.Kind != "reminder" ||
		st.Last.Broadcast.CreatedBy != "" || !st.Last.Broadcast.SettleButton {
		t.Fatalf("status: %+v %v", st, err)
	}
	w.start()
	got := w.texts(st.Last.Broadcast.ID)
	if len(got) != 2 {
		t.Fatalf("recipients: %v", got)
	}
	if want := "سلام @ali،\nبدهی حساب شما <b>۱۲۵٬۰۰۰ تومان</b> است. لطفاً طبق توافق، هرچه زودتر تسویه کنید.\nبا سپاس از همکاری شما 🙏"; got[9611] != want {
		t.Errorf("Persian:\n got %q\nwant %q", got[9611], want)
	}
	if want := "Hello @bob,\nYour account debt is <b>40,000 Toman</b>. Please settle it as agreed.\nThank you for working with us 🙏"; got[9612] != want {
		t.Errorf("English:\n got %q\nwant %q", got[9612], want)
	}
	if st, _ := w.svc.ResellerReminderStatus(w.ctx); st.Last.Broadcast.Total != 2 {
		t.Errorf("how many it went to: %d", st.Last.Broadcast.Total)
	}

	w.tick("2026-11-05 21:00")
	w.wantClaims("next week", "2026-10-29", "2026-11-05")
}

// Not "only in debt": every active reseller gets it.
func TestReminderEveryReseller(t *testing.T) {
	w := newReminderWorld(t, "Asia/Tehran")
	w.reseller(9621, w.gold, "fa", "a", 10_000)
	w.reseller(9622, w.gold, "en", "b", 0)
	w.on("2026-10-20 10:00")
	w.setting(SettingReminderDebtOnly, "false", w.at("2026-10-20 10:00"))
	w.tick("2026-10-29 21:00")
	st, err := w.svc.ResellerReminderStatus(w.ctx)
	if err != nil || st.Last == nil {
		t.Fatal(err)
	}
	w.start()
	if got := w.texts(st.Last.Broadcast.ID); len(got) != 2 || got[9622] == "" {
		t.Fatalf("recipients: %v", got)
	}
}

// Two claimers at once (two cores, or a slow tick overtaken): one reminder.
func TestReminderTwoClaimers(t *testing.T) {
	w := newReminderWorld(t, "Asia/Tehran")
	w.reseller(9631, w.gold, "fa", "a", 10_000)
	w.on("2026-10-20 10:00")
	w.now = w.at("2026-10-29 21:00")
	workers := []*ResellerReminder{w.svc.NewResellerReminder(nil), w.svc.NewResellerReminder(nil)}
	var wg sync.WaitGroup
	errs := make([]error, len(workers))
	for i, r := range workers {
		r.now = func() time.Time { return w.now }
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = r.Step(w.ctx)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	w.wantClaims("two claimers", "2026-10-29")
	if n := w.broadcasts(); n != 1 {
		t.Fatalf("reminders: %d", n)
	}
}

// A core that was down over the due time sends this week's when it is back within 6 hours, never later, and never one for a week it missed.
func TestReminderNoMakeUp(t *testing.T) {
	w := newReminderWorld(t, "Asia/Tehran")
	w.reseller(9641, w.gold, "fa", "a", 10_000)
	w.on("2026-10-01 10:00")
	// Down from Wednesday to Friday morning: Thursday's is more than 6 hours late.
	w.tick("2026-10-30 03:01")
	w.wantClaims("7 hours late")
	w.tick("2026-11-05 22:00")
	w.wantClaims("back an hour late", "2026-11-05")
	// Down for two weeks: only the latest week counts, and only within its 6 hours.
	w.tick("2026-11-19 23:30")
	w.wantClaims("two weeks later", "2026-11-05", "2026-11-19")
}

// Switched on after this week's time: it starts next week.
func TestReminderSwitchedOnLate(t *testing.T) {
	w := newReminderWorld(t, "Asia/Tehran")
	w.reseller(9651, w.gold, "fa", "a", 10_000)
	w.on("2026-10-29 21:30")
	w.tick("2026-10-29 21:31")
	w.wantClaims("switched on late")
	w.tick("2026-11-05 21:00")
	w.wantClaims("next week", "2026-11-05")
}

// Moving the weekday never sends twice in six days: Thursday's went, Friday is skipped, the next Friday sends.
func TestReminderMovedWeekday(t *testing.T) {
	w := newReminderWorld(t, "Asia/Tehran")
	w.reseller(9661, w.gold, "fa", "a", 10_000)
	w.on("2026-10-20 10:00")
	w.tick("2026-10-29 21:00")
	w.setting(SettingReminderWeekday, "friday", w.at("2026-10-29 22:00"))
	w.tick("2026-10-30 21:00")
	w.wantClaims("the day after", "2026-10-29")
	w.tick("2026-11-06 21:00")
	w.wantClaims("the next Friday", "2026-10-29", "2026-11-06")
}

// The store's own zone, and the days its clocks change: 02:30 does not exist on 29 March 2026 in Berlin (clocks go from 02:00 to 03:00), so
// the reminder goes when they land after it; on 25 October it happens twice and the reminder goes the first time.
func TestReminderDST(t *testing.T) {
	w := newReminderWorld(t, "Europe/Berlin")
	w.reseller(9671, w.gold, "en", "a", 10_000)
	w.setting(SettingReminderEnabled, "true", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	w.setting(SettingReminderWeekday, "sunday", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	w.setting(SettingReminderTime, "02:30", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))

	w.tickUTC("2026-03-29T00:59:00Z") // 01:59 CET
	w.wantClaims("before the change")
	w.tickUTC("2026-03-29T01:00:00Z") // 03:00 CEST: the clocks jumped over 02:30
	w.wantClaims("after the jump", "2026-03-29")

	w.tickUTC("2026-10-25T00:29:00Z") // 02:29 CEST
	w.wantClaims("before the first 02:30", "2026-03-29")
	w.tickUTC("2026-10-25T00:30:00Z") // 02:30 CEST, the first one
	w.wantClaims("the first 02:30", "2026-03-29", "2026-10-25")
	w.tickUTC("2026-10-25T01:30:00Z") // 02:30 CET, the second one
	w.restart()
	w.tickUTC("2026-10-25T01:31:00Z")
	w.wantClaims("the second 02:30", "2026-03-29", "2026-10-25")

	// The store's zone, not the server's: Thursday 21:00 in Tehran is 17:30 UTC.
	w.setting("general.timezone", "Asia/Tehran", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	w.setting(SettingReminderWeekday, "thursday", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	w.setting(SettingReminderTime, "21:00", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	w.tickUTC("2026-11-05T17:29:00Z")
	w.wantClaims("20:59 in Tehran", "2026-03-29", "2026-10-25")
	w.tickUTC("2026-11-05T17:30:00Z")
	w.wantClaims("21:00 in Tehran", "2026-03-29", "2026-10-25", "2026-11-05")
}

// lastDue and nextDue on the store's clock.
func TestLastDue(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Tehran")
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct{ now, day, next string }{
		{"2026-10-29 20:59", "2026-10-22", "2026-10-29 21:00"},
		{"2026-10-29 21:00", "2026-10-29", "2026-11-05 21:00"},
		{"2026-10-31 08:00", "2026-10-29", "2026-11-05 21:00"},
		{"2026-11-04 23:59", "2026-10-29", "2026-11-05 21:00"},
	} {
		day, _ := lastDue(at(c.now), time.Thursday, 21*60)
		if got := day.Format(dateLayout); got != c.day {
			t.Errorf("%s: last due %s, want %s", c.now, got, c.day)
		}
		if got := nextDue(at(c.now), time.Thursday, 21*60); !got.Equal(at(c.next)) {
			t.Errorf("%s: next due %s, want %s", c.now, got, c.next)
		}
	}
}
