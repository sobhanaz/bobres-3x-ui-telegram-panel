package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

func (e *env) events(q EventsQuery) *EventsResult {
	e.t.Helper()
	e.m.logMu.Lock()
	e.m.logs = nil
	e.m.logMu.Unlock()
	r, err := e.m.Events(context.Background(), q)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// raise runs the watchdog until a full disk is announced, and again until it is fixed.
func (e *env) raiseDiskAlert(w *domain.Watchdog, extra ...func(*snapshot.Snapshot)) {
	e.t.Helper()
	e.snap(func(s *snapshot.Snapshot) {
		s.Host.Disks = []snapshot.Disk{{Mount: "/", Total: 100 << 30, Used: 91 << 30, Avail: 7 << 30}}
		for _, f := range extra {
			f(s)
		}
	})
	e.clk.add(time.Second)
	for range 2 {
		if err := w.Step(context.Background()); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *env) fixDiskAlert(w *domain.Watchdog) {
	e.t.Helper()
	e.clk.add(time.Minute)
	e.snap(nil)
	e.clk.add(time.Second)
	for range 2 {
		if err := w.Step(context.Background()); err != nil {
			e.t.Fatal(err)
		}
	}
}

func TestEventsMergeAlertsLogsAndTheJournalNewestFirst(t *testing.T) {
	e := newEnv(t)
	real := time.Now()
	e.clk.t = real
	e.logs["bot"] = &fakeLogs{lines: []logging.Line{
		{Time: real.Add(-30 * time.Minute).Unix(), Level: "ERROR", Msg: "send failed <b>x</b>‮", Attrs: `{"user_id":42,"err":"timeout"}`},
		{Time: real.Add(-20 * time.Minute).Unix(), Level: "WARN", Msg: "slow reply", Attrs: "{}"},
	}}
	e.logs["payments"] = &fakeLogs{err: errBoom}
	e.build()
	w := e.dom.NewWatchdog(domain.WatchdogSources{Monitor: e.m}, nil)
	w.FixAfter, w.QuietAfter = 0, 0
	e.raiseDiskAlert(w, func(s *snapshot.Snapshot) {
		s.Journal = &snapshot.Journal{WindowS: 3600, Sources: []snapshot.JournalSource{
			{Name: "docker", Errors: 1, Lines: []snapshot.JournalLine{{At: real.Add(-40 * time.Minute).Unix(), Msg: "level=error msg=\"failed to start\""}}}}}
	})

	r := e.events(EventsQuery{Level: LevelWarn, Limit: 50, Logs: true})
	if !r.LogsVisible || len(r.Unreachable) != 1 || r.Unreachable[0] != "payments" || r.Truncated {
		t.Fatalf("meta: %+v", r)
	}
	var kinds []string
	for i, ev := range r.Items {
		kinds = append(kinds, ev.Kind+"/"+ev.Source)
		if i > 0 && ev.At > r.Items[i-1].At {
			t.Errorf("not newest first at %d", i)
		}
	}
	want := map[string]bool{"alert/alert": false, "journal/host.docker": false, "log/bot": false}
	for _, k := range kinds {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("no %s among %v", k, kinds)
		}
	}
	for _, ev := range r.Items {
		switch ev.Source {
		case "alert":
			if ev.Level != LevelWarn || ev.State != AlertOpen || ev.Key != "monitor.disk:/" || !strings.Contains(ev.Title, "The disk at / is 93% full") {
				t.Errorf("alert: %+v", ev)
			}
		case "bot":
			if strings.ContainsAny(ev.Title, "<>‮") || (ev.Level == LevelError) != (strings.HasPrefix(ev.Title, "send failed")) {
				t.Errorf("log: %+v", ev)
			}
			if ev.Level == LevelError && !strings.Contains(ev.Detail, `"user_id":42`) {
				t.Errorf("log attrs: %+v", ev)
			}
			if ev.Level == LevelWarn && ev.Detail != "" {
				t.Errorf("empty attrs shown: %+v", ev)
			}
		case "host.docker":
			if ev.Level != LevelError || ev.Kind != KindJournal {
				t.Errorf("journal: %+v", ev)
			}
		}
	}

	// level=error: the error lines only (the disk alert is a warning).
	r = e.events(EventsQuery{Level: LevelError, Limit: 50, Logs: true})
	for _, ev := range r.Items {
		if ev.Level != LevelError {
			t.Errorf("a %s event under level=error: %+v", ev.Level, ev)
		}
	}
	if len(r.Items) < 2 {
		t.Errorf("error events: %+v", r.Items)
	}

	// Without the right to read logs: no service lines, and the response says so.
	r = e.events(EventsQuery{Level: LevelWarn, Limit: 50, Logs: false})
	if r.LogsVisible || len(r.Unreachable) != 0 {
		t.Errorf("meta: %+v", r)
	}
	for _, ev := range r.Items {
		if ev.Kind == KindLog {
			t.Errorf("a log line for a role that may not read logs: %+v", ev)
		}
	}
	if len(r.Items) < 2 {
		t.Errorf("alerts and the journal should remain: %+v", r.Items)
	}

	// The limit cuts, newest first, and says so.
	r = e.events(EventsQuery{Level: LevelWarn, Limit: 1, Logs: true})
	if len(r.Items) != 1 || !r.Truncated {
		t.Errorf("limit: %+v", r)
	}
	r = e.events(EventsQuery{Level: LevelWarn, Limit: 100000, Logs: true})
	if len(r.Items) > MaxEvents {
		t.Errorf("limit not bounded: %d", len(r.Items))
	}
}

func TestEventsShowAnAlertRaisedAndFixed(t *testing.T) {
	e := newEnv(t)
	real := time.Now()
	e.clk.t = real
	w := e.dom.NewWatchdog(domain.WatchdogSources{Monitor: e.m}, nil)
	w.FixAfter, w.QuietAfter = 0, 0
	e.raiseDiskAlert(w)
	e.fixDiskAlert(w)

	r := e.events(EventsQuery{Level: LevelWarn, Limit: 50})
	var raised, fixed *Event
	for i := range r.Items {
		ev := &r.Items[i]
		if ev.Kind != KindAlert {
			continue
		}
		if ev.State == AlertResolved {
			fixed = ev
		} else {
			raised = ev
		}
	}
	if raised == nil || fixed == nil || raised.State != AlertClosed || fixed.Level != LevelWarn || fixed.Key != "monitor.disk:/" {
		t.Fatalf("raised %+v fixed %+v", raised, fixed)
	}
	if fixed.At < raised.At {
		t.Errorf("the fix is older than the problem: %d < %d", fixed.At, raised.At)
	}
	// The detail of a monitoring alert (a few figures) is included.
	if !strings.Contains(raised.Detail, "of") {
		t.Errorf("detail: %q", raised.Detail)
	}

	// The same problem again is open, and the repeat reminders are not listed twice.
	e.raiseDiskAlert(w)
	r = e.events(EventsQuery{Level: LevelWarn, Limit: 50})
	open := 0
	for _, ev := range r.Items {
		if ev.Kind == KindAlert && ev.State == AlertOpen {
			open++
		}
	}
	if open != 1 {
		t.Errorf("%d open entries", open)
	}
}

// The text of an event is plain and bounded whoever wrote it.
func TestEventsAreBounded(t *testing.T) {
	e := newEnv(t)
	real := time.Now()
	e.logs["bot"] = &fakeLogs{lines: []logging.Line{{Time: real.Unix(), Level: "ERROR", Msg: strings.Repeat("é", 5000), Attrs: strings.Repeat("a", 5000)}}}
	e.build()
	r := e.events(EventsQuery{Level: LevelError, Limit: 10, Logs: true})
	var found bool
	for _, ev := range r.Items {
		if ev.Source == "bot" {
			found = true
			if len([]rune(ev.Title)) > maxEventText+1 || len([]rune(ev.Detail)) > maxEventAttr+1 {
				t.Errorf("lengths %d %d", len([]rune(ev.Title)), len([]rune(ev.Detail)))
			}
		}
	}
	if !found {
		t.Fatal("no event")
	}
}
