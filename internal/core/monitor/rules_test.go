package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/cicd"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

func problems(checks map[string]domain.MonitorCheck) []string {
	var out []string
	for k, c := range checks {
		if c.Problem {
			out = append(out, k)
		}
	}
	return out
}

func TestRulesAllClear(t *testing.T) {
	e := newEnv(t)
	e.snap(nil)
	e.clk.add(5 * time.Second)
	checks, covered := e.checks()
	if !covered || len(problems(checks)) != 0 {
		t.Fatalf("covered=%v problems=%v", covered, problems(checks))
	}
	// Every rule that can be evaluated reports its passing state, so a fixed problem can end.
	for _, key := range []string{"monitor.disk:/", "monitor.memory", "monitor.collector", "monitor.container.postgres.down",
		"monitor.container.postgres.unhealthy", "monitor.container.postgres.restarting", "monitor.container.core.unhealthy", "monitor.container.redis.restarting"} {
		if _, ok := checks[key]; !ok {
			t.Errorf("no %s among %d checks", key, len(checks))
		}
	}
	// The health endpoints already cover these services going down.
	for _, key := range []string{"monitor.container.core.down", "monitor.container.bot.down", "monitor.container.payments.down"} {
		if _, ok := checks[key]; ok {
			t.Errorf("%s duplicates the service check", key)
		}
	}
}

func TestRuleDiskPastItsThreshold(t *testing.T) {
	e := newEnv(t)
	e.snap(func(s *snapshot.Snapshot) {
		s.Host.Disks = []snapshot.Disk{
			{Mount: "/", Total: 100 << 30, Used: 86 << 30, Avail: 12 << 30, Roles: []string{"install"}},
			{Mount: "/var/lib/docker", Total: 100 << 30, Used: 20 << 30, Avail: 80 << 30, Roles: []string{"docker"}},
		}
	})
	e.clk.add(time.Second)
	checks, _ := e.checks()
	root, docker := checks["monitor.disk:/"], checks["monitor.disk:/var/lib/docker"]
	if !root.Problem || docker.Problem || root.Critical {
		t.Fatalf("root %+v docker %+v", root, docker)
	}
	// 86 of 98 GB is 87.8%: df's measure, which leaves out the reserved blocks.
	if root.TitleKey != "alert.monitor_disk" || root.Args["mount"] != "/" || root.Args["percent"] != "88" || root.Args["free"] != "12884901888" {
		t.Fatalf("args: %+v", root.Args)
	}

	// The threshold is a setting.
	e.set(domain.SettingMonitorDiskPct, "90")
	if c, _ := e.checks(); c["monitor.disk:/"].Problem {
		t.Fatal("the raised threshold was ignored")
	}
	// A value out of its range is the default, not the sentence of a typo.
	e.set(domain.SettingMonitorDiskPct, "5")
	if c, _ := e.checks(); !c["monitor.disk:/"].Problem {
		t.Fatal("a threshold out of range should fall back to 85")
	}
	// 95 and over is critical.
	e.snap(func(s *snapshot.Snapshot) {
		s.Host.Disks[0] = snapshot.Disk{Mount: "/", Total: 100 << 30, Used: 96 << 30, Avail: 2 << 30}
	})
	e.clk.add(time.Second)
	if c, _ := e.checks(); !c["monitor.disk:/"].Critical {
		t.Fatal("96% full is critical")
	}
}

func TestRuleMemoryHoldsForItsMinutes(t *testing.T) {
	e := newEnv(t)
	e.snap(func(s *snapshot.Snapshot) { s.Host.Mem = snapshot.Mem{Total: 8 << 30, Available: 400 << 20} })
	e.clk.add(time.Second)
	c, _ := e.checks()
	mem := c["monitor.memory"]
	if !mem.Problem || mem.Hold != 10*time.Minute || mem.Args["percent"] != "95" || mem.Args["minutes"] != "10" {
		t.Fatalf("%+v", mem)
	}
	e.set(domain.SettingMonitorMemMinutes, "3")
	e.set(domain.SettingMonitorMemPct, "96")
	c, _ = e.checks()
	if mem := c["monitor.memory"]; mem.Problem || mem.Hold != 3*time.Minute {
		t.Fatalf("%+v", mem)
	}
}

func TestRuleContainerStates(t *testing.T) {
	e := newEnv(t)
	e.snap(func(s *snapshot.Snapshot) {
		for i := range s.Docker.Containers {
			switch s.Docker.Containers[i].Service {
			case "redis":
				s.Docker.Containers[i].State, s.Docker.Containers[i].Health = snapshot.StateExited, snapshot.HealthNone
			case "caddy":
				s.Docker.Containers[i].Health = snapshot.HealthUnhealthy
			case "payments":
				s.Docker.Containers[i].State = snapshot.StateExited // watched by its health endpoint, not here
			}
		}
		s.Docker.Compose.Missing = []string{"tls-agent", "backup"}
		s.Docker.Containers = s.Docker.Containers[:len(s.Docker.Containers)-1] // tls-agent has no container
	})
	e.clk.add(time.Second)
	c, _ := e.checks()
	if !c["monitor.container.redis.down"].Problem || !c["monitor.container.redis.down"].Critical || c["monitor.container.redis.down"].Hold != 2*time.Minute {
		t.Errorf("redis down: %+v", c["monitor.container.redis.down"])
	}
	if got := c["monitor.container.redis.down"].Args["container"]; got != "redis" {
		t.Errorf("args %v", c["monitor.container.redis.down"].Args)
	}
	if u := c["monitor.container.caddy.unhealthy"]; !u.Problem || u.TitleKey != "alert.monitor_container_unhealthy" || u.Hold == 0 {
		t.Errorf("caddy unhealthy: %+v", u)
	}
	if !c["monitor.container.tls-agent.down"].Problem {
		t.Errorf("a service the install runs that has no container: %+v", c["monitor.container.tls-agent.down"])
	}
	if _, ok := c["monitor.container.backup.down"]; ok {
		t.Error("a missing service the health endpoint covers")
	}
	if _, ok := c["monitor.container.payments.down"]; ok {
		t.Error("payments is covered by its health endpoint")
	}
	if c["monitor.container.postgres.down"].Problem || c["monitor.container.postgres.unhealthy"].Problem {
		t.Error("a healthy database")
	}
}

// A crash loop: the restart counter climbs; a counter that drops is a container made anew.
func TestRuleRestartsWithinTheWindow(t *testing.T) {
	e := newEnv(t)
	record := func(restarts float64) {
		t.Helper()
		if _, err := e.st.RecordSamples(context.Background(), e.clk.now(), []store.MonitorSample{{Name: "container.restarts:caddy", Value: restarts}}); err != nil {
			t.Fatal(err)
		}
	}
	e.snap(nil)
	for i, v := range []float64{10, 10, 11, 12, 13} {
		record(v)
		e.clk.add(30 * time.Second)
		_ = i
	}
	e.snap(nil)
	e.clk.add(time.Second)
	c, _ := e.checks()
	r := c["monitor.container.caddy.restarting"]
	if !r.Problem || r.Args["count"] != "3" || r.Args["minutes"] != "15" || r.Args["container"] != "caddy" {
		t.Fatalf("3 restarts in the window: %+v", r)
	}
	if r.Critical {
		t.Fatal("3 restarts is not yet critical")
	}

	// The threshold is a setting; 4 restarts are needed now.
	e.set(domain.SettingMonitorRestarts, "4")
	if c, _ := e.checks(); c["monitor.container.caddy.restarting"].Problem {
		t.Fatal("below the raised threshold")
	}

	// A fall in the counter (the container was made anew) counts nothing.
	e.set(domain.SettingMonitorRestarts, "2")
	record(0)
	e.clk.add(30 * time.Second)
	record(1)
	e.snap(nil)
	e.clk.add(time.Second)
	if c, _ := e.checks(); c["monitor.container.caddy.restarting"].Args["count"] != "4" {
		t.Fatalf("the drop should not be subtracted: %+v", c["monitor.container.caddy.restarting"])
	}
	// Restarts older than the window are forgotten.
	e.clk.add(domain.MonitorRestartWindow + time.Minute)
	e.snap(nil)
	e.clk.add(time.Second)
	if c, _ := e.checks(); c["monitor.container.caddy.restarting"].Problem {
		t.Fatalf("old restarts counted: %+v", c["monitor.container.caddy.restarting"])
	}
}

func TestRuleCollectorGoesQuiet(t *testing.T) {
	e := newEnv(t)
	// Not installed: nothing to say about the host, nothing to say about the collector.
	if c, covered := e.checks(); len(c) != 0 || !covered {
		t.Fatalf("no collector: %v covered=%v", c, covered)
	}

	e.snap(nil)
	e.clk.add(2 * time.Minute) // past the freshness limit, within the five minutes
	c, covered := e.checks()
	if covered || c["monitor.collector"].Problem {
		t.Fatalf("two minutes: covered=%v %+v", covered, c["monitor.collector"])
	}
	if _, ok := c["monitor.disk:/"]; ok {
		t.Fatal("host rules are evaluated on a stale snapshot")
	}

	e.clk.add(4 * time.Minute)
	c, covered = e.checks()
	q := c["monitor.collector"]
	if !q.Problem || covered || q.TitleKey != "alert.monitor_collector_stale" || q.Args["minutes"] != "5" {
		t.Fatalf("six minutes: covered=%v %+v", covered, q)
	}

	// The threshold is a setting.
	e.set(domain.SettingMonitorCollectorMinutes, "10")
	if c, _ := e.checks(); c["monitor.collector"].Problem {
		t.Fatal("below the raised threshold")
	}

	// A new snapshot ends it.
	e.snap(nil)
	e.clk.add(time.Second)
	c, covered = e.checks()
	if !covered || c["monitor.collector"].Problem {
		t.Fatalf("recovered: covered=%v %+v", covered, c["monitor.collector"])
	}

	// A file that is garbage is as good as none: the collector is broken.
	e.clk.add(20 * time.Minute)
	e.snap(func(s *snapshot.Snapshot) { s.Schema = 7 })
	e.clk.add(time.Second)
	if c, _ := e.checks(); !c["monitor.collector"].Problem {
		t.Fatal("an unreadable snapshot should read as a collector that is not working")
	}

	// Removing it (the collector turned off) is not an outage.
	if err := snapshot.Remove(e.dir); err != nil {
		t.Fatal(err)
	}
	e.clk.add(5 * time.Second)
	if c, covered := e.checks(); len(c) != 0 || !covered {
		t.Fatalf("collector switched off: %v covered=%v", c, covered)
	}
}

func ciRun(workflow, conclusion string) cicd.Run {
	return cicd.Run{Workflow: workflow, Branch: "main", Status: "completed", Conclusion: conclusion, SHA: "abc1234", URL: "https://github.com/o/r/actions/runs/1"}
}

func TestRuleCIOfMain(t *testing.T) {
	e := newEnv(t)
	e.bot.ci = &cicd.Status{Available: true, CheckedAt: 5, Runs: []cicd.Run{
		{Workflow: "CI", Branch: "main", Status: "in_progress"},
		ciRun("CI", "cancelled"),
		ciRun("Pages", "failure"),
		ciRun("CI", "failure"),
		ciRun("CI", "success"),
	}}
	e.m.refreshCI(context.Background())

	// Off by default: a store that runs releases does not care about main.
	if c, _ := e.checks(); len(c) != 0 {
		t.Fatalf("the CI rule is off by default: %v", c)
	}
	e.set(domain.SettingMonitorCIAlert, "true")
	c, _ := e.checks()
	ci := c["monitor.ci.main"]
	if !ci.Problem || ci.TitleKey != "alert.monitor_ci" || ci.Args["workflow"] != "CI" || !strings.Contains(ci.Detail, "abc1234") {
		t.Fatalf("a failed run decides (not the one running, not the cancelled one, not another workflow): %+v", ci)
	}

	// A newer run that passed clears it.
	e.bot.ci = &cicd.Status{Available: true, CheckedAt: 6, Runs: []cicd.Run{ciRun("CI", "success"), ciRun("CI", "failure")}}
	e.m.refreshCI(context.Background())
	if c, _ := e.checks(); c["monitor.ci.main"].Problem {
		t.Fatal("a passing run should clear the alert")
	}

	// GitHub or the bot being unreachable neither raises nor clears it.
	e.bot.ci = &cicd.Status{Available: true, CheckedAt: 7, Runs: []cicd.Run{ciRun("CI", "failure")}}
	e.m.refreshCI(context.Background())
	e.bot.ciErr = errBoom
	e.m.refreshCI(context.Background())
	if c, _ := e.checks(); !c["monitor.ci.main"].Problem {
		t.Fatal("an outage cleared a known failure")
	}
	if st := e.m.ciStatus(); st.Error != cicd.ErrUnreachable || !st.Available || len(st.Runs) != 1 {
		t.Fatalf("the facts should stay, marked with the error: %+v", st)
	}
}

func TestRulesAreForgottenWhileMonitoringIsOff(t *testing.T) {
	e := newEnv(t)
	e.snap(func(s *snapshot.Snapshot) { s.Host.Mem = snapshot.Mem{Total: 8 << 30, Available: 100 << 20} })
	e.clk.add(time.Second)
	e.set(domain.SettingMonitorEnabled, "false")
	c, covered := e.checks()
	if len(c) != 0 || !covered {
		t.Fatalf("off: %v covered=%v", c, covered)
	}
}

// The rules reach the watchdog and the Telegram alert machinery: raised after two sightings, shown
// as an open problem, fixed after it passed.
func TestRulesRaiseAndResolveThroughTheWatchdog(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	w := e.dom.NewWatchdog(domain.WatchdogSources{Monitor: e.m}, nil)
	w.FixAfter, w.QuietAfter = 0, 0

	full := func() {
		e.snap(func(s *snapshot.Snapshot) {
			s.Host.Disks = []snapshot.Disk{{Mount: "/", Total: 100 << 30, Used: 91 << 30, Avail: 7 << 30}}
		})
	}
	full()
	e.clk.add(time.Second)
	if err := w.Step(ctx); err != nil { // seen once: pending, not announced
		t.Fatal(err)
	}
	if open, _ := e.dom.OpenAlerts(ctx); len(open) != 0 {
		t.Fatalf("announced at the first sighting: %+v", open)
	}
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	open, err := e.dom.OpenAlerts(ctx)
	if err != nil || len(open) != 1 || open[0].Key != "monitor.disk:/" || open[0].Severity != "warning" ||
		!strings.Contains(open[0].Title, "The disk at / is 93% full") {
		t.Fatalf("open: %+v %v", open, err)
	}

	// The event the bot sends staff: system.alert.v1 for this check.
	var topic string
	if err := e.st.DB().QueryRow(ctx, `SELECT topic FROM core.outbox_core WHERE topic = 'system.alert.v1' AND payload->>'key' = 'monitor.disk:/'`).Scan(&topic); err != nil {
		t.Fatalf("no alert event: %v", err)
	}

	// Space freed: it passes, and the next run closes it.
	e.clk.add(time.Minute)
	e.snap(nil)
	e.clk.add(time.Second)
	for range 2 {
		if err := w.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if open, _ := e.dom.OpenAlerts(ctx); len(open) != 0 {
		t.Fatalf("still open: %+v", open)
	}
	var resolved int
	if err := e.st.DB().QueryRow(ctx, `SELECT count(*) FROM core.outbox_core WHERE topic = 'system.alert.v1' AND payload->>'key' = 'monitor.disk:/' AND payload->>'state' = 'resolved'`).Scan(&resolved); err != nil || resolved != 1 {
		t.Fatalf("resolved events: %d %v", resolved, err)
	}

	// Switching monitoring off forgets a rule without a message.
	full()
	e.clk.add(time.Second)
	for range 2 {
		if err := w.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if open, _ := e.dom.OpenAlerts(ctx); len(open) != 1 {
		t.Fatalf("raised again: %+v", open)
	}
	e.set(domain.SettingMonitorEnabled, "false")
	var before int
	_ = e.st.DB().QueryRow(ctx, `SELECT count(*) FROM core.outbox_core WHERE topic = 'system.alert.v1'`).Scan(&before)
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	var after int
	_ = e.st.DB().QueryRow(ctx, `SELECT count(*) FROM core.outbox_core WHERE topic = 'system.alert.v1'`).Scan(&after)
	if open, _ := e.dom.OpenAlerts(ctx); len(open) != 0 || after != before {
		t.Fatalf("switching off: %d open, %d -> %d events", len(open), before, after)
	}
}
