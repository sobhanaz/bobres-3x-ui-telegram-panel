package domain

import (
	"context"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func TestServerFill(t *testing.T) {
	for _, c := range []struct {
		name     string
		clients  int64
		max      int32
		pct      int
		full, ok bool
	}{
		{"no limit", 500, 0, 0, false, false},
		{"empty", 0, 10, 0, false, true},
		{"half", 5, 10, 50, false, true},
		{"just under 90", 899, 1000, 89, false, true},
		{"90", 9, 10, 90, false, true},
		{"one place left", 99, 100, 99, false, true},
		{"full", 100, 100, 100, true, true},
		{"limit set below what it holds", 150, 100, 150, true, true},
	} {
		pct, full, ok := ServerFill(&Server{Clients: c.clients, MaxClients: c.max})
		if pct != c.pct || full != c.full || ok != c.ok {
			t.Errorf("%s: %d%% full=%v limited=%v", c.name, pct, full, ok)
		}
	}
	if _, _, ok := ServerFill(nil); ok {
		t.Error("no server has a limit")
	}
}

// The notice is for a server that takes new services and has a limit, from 90% on, with another text once it is full; it is a
// daily notice of the warning level, and its numbers are the server's.
func TestCapacityCheck(t *testing.T) {
	sv := func(clients int64, limit int32, on bool) *Server {
		return &Server{ID: "s1", Name: "DE <1>", Enabled: on, Clients: clients, MaxClients: limit}
	}
	if _, ok := capacityCheck(sv(500, 0, true)); ok {
		t.Error("a server with no limit has a check")
	}
	if _, ok := capacityCheck(sv(100, 100, false)); ok {
		t.Error("a server that is switched off has a check")
	}
	for _, c := range []struct {
		clients int64
		problem bool
		title   string
	}{
		{0, false, alertServerCapacity}, {89, false, alertServerCapacity}, {90, true, alertServerCapacity}, {99, true, alertServerCapacity},
		{100, true, alertServerFull}, {130, true, alertServerFull},
	} {
		got, ok := capacityCheck(sv(c.clients, 100, true))
		if !ok || got.problem != c.problem || got.title != c.title || !got.daily || got.once || got.severity != events.SeverityWarning ||
			got.key != "server.s1.capacity" || got.args["server"] != "DE <1>" || got.args["limit"] != "100" {
			t.Errorf("%d of 100: %+v", c.clients, got)
		}
	}
	got, _ := capacityCheck(sv(93, 100, true))
	if got.args["percent"] != "93" || got.args["count"] != "93" {
		t.Errorf("numbers: %v", got.args)
	}
}

// A server that fills up is announced when it reaches 90%, again as full, and once a day while it stays so; raising the limit ends
// it quietly (time passing does not fix a limit, but a limit that is raised does).
func TestWatchdogAnnouncesAServerThatIsFillingUp(t *testing.T) {
	servers := &fakeLister{servers: []Server{
		{ID: "s1", Name: "DE <1>", Enabled: true, Healthy: true, CheckedAt: 1, Clients: 40, MaxClients: 100},
		{ID: "s2", Name: "NL", Enabled: true, Healthy: true, CheckedAt: 1, Clients: 900},
	}}
	a := newAlertWorld(t, WatchdogSources{Servers: servers})
	for range 3 {
		a.step()
		a.after(time.Minute)
	}
	a.expectSent(0) // 40%, and a server without a limit is never full

	servers.servers[0].Clients = 90
	a.step() // seen once: pending
	a.expectSent(0)
	a.after(time.Minute)
	a.step()
	got := a.expectSent(1)[0]
	if got.Key != "server.s1.capacity" || got.TitleKey != "alert.server_capacity" || got.Severity != events.SeverityWarning || got.Repeat ||
		got.Args["server"] != "DE <1>" || got.Args["percent"] != "90" || got.Args["count"] != "90" || got.Args["limit"] != "100" {
		t.Fatalf("90%%: %+v", got)
	}
	// It is not said again every minute, and the number moving a little is only kept for the System page.
	servers.servers[0].Clients = 95
	for range 5 {
		a.after(time.Minute)
		a.step()
	}
	a.expectSent(1)
	if st := a.state("server.s1.capacity"); st == nil || st.State != store.AlertProblem || st.Args["count"] != "95" {
		t.Fatalf("stored: %+v", st)
	}

	// Full: another text, announced as a new problem.
	servers.servers[0].Clients = 100
	a.after(time.Minute)
	a.step()
	all := a.expectSent(2)
	if got = all[1]; got.TitleKey != "alert.server_full" || got.Repeat || got.Args["count"] != "100" {
		t.Fatalf("full: %+v", got)
	}

	// Still full a day later: reminded once, not before.
	a.after(23 * time.Hour)
	a.step()
	a.expectSent(2)
	a.after(time.Hour + time.Minute)
	a.step()
	all = a.expectSent(3)
	if got = all[2]; got.TitleKey != "alert.server_full" || !got.Repeat {
		t.Fatalf("a day later: %+v", got)
	}

	// The owner raises the limit: the notice ends without a "fixed" message.
	servers.servers[0].MaxClients = 500
	for range 8 {
		a.after(time.Minute)
		a.step()
	}
	a.expectSent(3)
	if st := a.state("server.s1.capacity"); st == nil || st.State != store.AlertOK {
		t.Fatalf("after raising the limit: %+v", st)
	}
	// Taking the limit away, or switching the server off, forgets the check.
	servers.servers[0].MaxClients = 0
	a.after(time.Minute)
	a.step()
	if st := a.state("server.s1.capacity"); st != nil {
		t.Fatalf("a server with no limit is still watched: %+v", st)
	}
}

// A server turned off, or one the provisioner cannot list, neither raises the notice nor loses it.
func TestWatchdogCapacityNoticeFollowsTheProvisioner(t *testing.T) {
	servers := &fakeLister{servers: []Server{{ID: "s1", Name: "DE", Enabled: true, Healthy: true, CheckedAt: 1, Clients: 100, MaxClients: 100}}}
	a := newAlertWorld(t, WatchdogSources{Servers: servers})
	a.step()
	a.after(time.Minute)
	a.step()
	a.expectSent(1)

	servers.err = context.DeadlineExceeded // the provisioner is away: nothing is known
	a.after(time.Minute)
	a.step()
	if st := a.state("server.s1.capacity"); st == nil || st.State != store.AlertProblem {
		t.Fatalf("kept while the provisioner is away: %+v", st)
	}

	servers.err = nil
	servers.servers[0].Enabled = false
	a.after(time.Minute)
	a.step()
	if st := a.state("server.s1.capacity"); st != nil {
		t.Fatalf("a server turned off is still watched: %+v", st)
	}
	a.expectSent(1)
}
