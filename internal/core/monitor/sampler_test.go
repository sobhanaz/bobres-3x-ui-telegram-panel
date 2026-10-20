package monitor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// ---- the collector's snapshot as core sees it ----

func TestCollectorViewStates(t *testing.T) {
	e := newEnv(t)
	now := e.clk.now()

	// No directory configured at all.
	none := New(Config{Now: e.clk.now})
	if v := none.collector(now); v.Configured || v.Installed || v.Fresh {
		t.Fatalf("no directory: %+v", v)
	}

	// A directory with no snapshot: the collector is not installed (or was switched off).
	if v := e.m.collector(now); !v.Configured || v.Installed || v.Fresh || v.Snap != nil {
		t.Fatalf("empty directory: %+v", v)
	}

	// A fresh snapshot.
	e.snap(nil)
	e.clk.add(20 * time.Second)
	if v := e.m.collector(e.clk.now()); !v.Installed || !v.Fresh || v.Snap == nil || v.AgeS != 20 || v.Error != "" {
		t.Fatalf("fresh: %+v", v)
	}

	// Left alone for two minutes it is stale: still installed, but it says nothing about now.
	e.clk.add(2 * time.Minute)
	v := e.m.collector(e.clk.now())
	if !v.Installed || v.Fresh || v.Snap != nil || v.Last == nil || v.AgeS != 140 {
		t.Fatalf("stale: %+v", v)
	}
	if got := collectorWhy(v); !strings.Contains(got, "sent nothing for 2 minutes") {
		t.Fatalf("why: %q", got)
	}

	// The collector switched off removes its file.
	if err := snapshot.Remove(e.dir); err != nil {
		t.Fatal(err)
	}
	e.clk.add(3 * time.Second)
	if v := e.m.collector(e.clk.now()); v.Installed || v.Last != nil {
		t.Fatalf("removed: %+v", v)
	}
}

func TestCollectorViewRefusesWhatIsNotASnapshot(t *testing.T) {
	e := newEnv(t)
	file := filepath.Join(e.dir, snapshot.FileName)
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"garbage":    {"<html>captive portal</html>", "invalid"},
		"wrong data": {`{"schema":1}`, "invalid"},
		"future":     {`{"schema":1,"generated_at":9999999999}`, "invalid"},
		"schema":     {`{"schema":99,"generated_at":1}`, "invalid"},
		"oversized":  {`{"schema":1,"x":"` + strings.Repeat("a", snapshot.MaxFileBytes) + `"}`, "too_large"},
	} {
		if err := os.WriteFile(file, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		e.clk.add(5 * time.Second) // past the read throttle
		v := e.m.collector(e.clk.now())
		if !v.Installed || v.Fresh || v.Snap != nil || v.Error != tc.want {
			t.Errorf("%s: %+v", name, v)
		}
	}

	// A link in place of the file is not followed.
	_ = os.Remove(file)
	target := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, file); err != nil {
		t.Fatal(err)
	}
	e.clk.add(5 * time.Second)
	if v := e.m.collector(e.clk.now()); v.Fresh || v.Snap != nil || v.Error == "" {
		t.Fatalf("a symlink was followed: %+v", v)
	}
}

// The network rate needs two snapshots; the first one gives none (unknown, not zero).
func TestCollectorViewComputesNetworkRates(t *testing.T) {
	e := newEnv(t)
	e.snap(nil)
	e.clk.add(time.Second)
	if v := e.m.collector(e.clk.now()); v.NetRx != nil || v.NetTx != nil {
		t.Fatalf("a rate from one snapshot: %+v %+v", v.NetRx, v.NetTx)
	}
	e.clk.add(29 * time.Second)
	e.snap(func(s *snapshot.Snapshot) {
		s.Host.Net.Ifaces[0].RxBytes = 1000 + 30_000
		s.Host.Net.Ifaces[0].TxBytes = 2000 + 3_000
	})
	v := e.m.collector(e.clk.now())
	if v.NetRx == nil || v.NetTx == nil || *v.NetRx != 1000 || *v.NetTx != 100 {
		t.Fatalf("rates: %v %v", v.NetRx, v.NetTx)
	}
	// An older file put back resets what it was compared with.
	older := baseSnap(t0.Add(-time.Hour))
	if err := snapshot.Write(e.dir, older); err != nil {
		t.Fatal(err)
	}
	e.clk.add(5 * time.Second)
	if v := e.m.collector(e.clk.now()); v.NetRx != nil {
		t.Fatalf("a rate across a replaced file: %v", *v.NetRx)
	}
}

// ---- sampling ----

func (e *env) step() {
	e.t.Helper()
	e.m.Step(context.Background())
}

func TestStepRecordsTheHostAtTheTimeOfTheSnapshot(t *testing.T) {
	e := newEnv(t)
	e.snap(nil)
	snapAt := e.clk.now()
	e.clk.add(20 * time.Second)
	e.step()

	names := e.metrics()
	for _, want := range []string{"host.cpu_pct", "host.load1", "host.mem_pct", "host.swap_pct", "host.disk_pct:/",
		"container.cpu_pct:core", "container.mem_bytes:core", "container.restarts:core", "container.restarts:postgres"} {
		if !slices.Contains(names, want) {
			t.Errorf("no %s among %v", want, names)
		}
	}
	// There is no second snapshot to take a network rate from.
	if slices.Contains(names, "host.net_rx_bps") {
		t.Error("a network rate without a second snapshot")
	}
	cpu := e.series("host.cpu_pct")
	if len(cpu) != 1 || cpu[0].V != 12.5 || cpu[0].T != snapAt.Unix()/30*30 {
		t.Fatalf("cpu: %+v (snapshot at %d)", cpu, snapAt.Unix())
	}
	if mem := e.series("host.mem_pct"); len(mem) != 1 || mem[0].V != 25 {
		t.Fatalf("mem: %+v", mem)
	}
	if disk := e.series("host.disk_pct:/"); len(disk) != 1 || disk[0].V < 42 || disk[0].V > 43 {
		t.Fatalf("disk: %+v", disk)
	}

	// The same snapshot again is not recorded twice.
	before := e.series("container.mem_bytes:core")
	e.clk.add(5 * time.Second)
	e.step()
	if after := e.series("container.mem_bytes:core"); len(after) != len(before) {
		t.Fatalf("the same snapshot recorded again: %d -> %d points", len(before), len(after))
	}
}

func TestStepRecordsNothingFromAStaleOrMissingSnapshot(t *testing.T) {
	e := newEnv(t)
	e.step() // no collector at all
	for _, n := range e.metrics() {
		if strings.HasPrefix(n, "host.") || strings.HasPrefix(n, "container.") {
			t.Fatalf("recorded %s without a collector", n)
		}
	}
	// The core-side metrics are recorded all the same.
	if !slices.Contains(e.metrics(), "core.goroutines") {
		t.Fatalf("core's own figures missing: %v", e.metrics())
	}

	e.snap(nil)
	e.clk.add(5 * time.Minute) // far past the freshness limit
	e.step()
	if pts := e.series("host.cpu_pct"); len(pts) != 0 {
		t.Fatalf("a stale snapshot was recorded: %+v", pts)
	}
}

// A figure that is not known is not recorded: no processor share on a first snapshot, no memory or
// processor for a stopped container, no restarts for a service that has no container.
func TestStepNeverRecordsAnUnknownFigureAsZero(t *testing.T) {
	e := newEnv(t)
	e.snap(func(s *snapshot.Snapshot) {
		s.Host.CPU.Pct = nil
		s.Host.Swap = snapshot.Swap{} // no swap on this machine
		s.Docker.Containers[2] = ctr("payments", snapshot.StateExited, snapshot.HealthNone, 4, 0)
		s.Docker.Containers[3].CPUPct = nil
	})
	e.clk.add(10 * time.Second)
	e.step()
	names := e.metrics()
	for _, bad := range []string{"host.cpu_pct", "host.swap_pct", "container.cpu_pct:payments", "container.mem_bytes:payments", "container.cpu_pct:provisioner"} {
		if slices.Contains(names, bad) {
			t.Errorf("recorded %s", bad)
		}
	}
	if r := e.series("container.restarts:payments"); len(r) != 1 || r[0].V != 4 {
		t.Errorf("a stopped container's restarts are a real count: %+v", r)
	}
	if m := e.series("container.mem_bytes:provisioner"); len(m) != 1 {
		t.Errorf("memory of a container with no processor share: %+v", m)
	}
}

func TestStepSumsContainersOfOneServiceAndSkipsOtherProjects(t *testing.T) {
	e := newEnv(t)
	e.snap(func(s *snapshot.Snapshot) {
		second := ctr("bot", snapshot.StateRunning, snapshot.HealthNone, 7, 5)
		second.ID = "aaaaaaaaaaaa"
		second.Name = "bobres-bot-2"
		other := ctr("web", snapshot.StateRunning, snapshot.HealthNone, 0, 50)
		other.Project, other.ID, other.Name = "someone-else", "bbbbbbbbbbbb", "other-web-1"
		s.Docker.Containers = append(s.Docker.Containers, second, other)
	})
	e.clk.add(5 * time.Second)
	e.step()
	if cpu := e.series("container.cpu_pct:bot"); len(cpu) != 1 || cpu[0].V != 8 {
		t.Fatalf("bot cpu: %+v", cpu)
	}
	if r := e.series("container.restarts:bot"); len(r) != 1 || r[0].V != 7 {
		t.Fatalf("bot restarts: %+v", r)
	}
	if slices.Contains(e.metrics(), "container.cpu_pct:web") {
		t.Error("a container of another project was recorded")
	}
}

func TestStepRecordsTheStackFromTheOtherSources(t *testing.T) {
	e := newEnv(t)
	e.payments.n = 3
	e.servers.list = &domain.ServerList{Servers: []domain.Server{
		{ID: "srv-1", Name: "DE-1", Enabled: true, Healthy: true, CheckedAt: 1},
		{ID: "srv-2", Name: "NL-1", Enabled: true, Healthy: false, CheckedAt: 1},
		{ID: "srv-3", Name: "never checked", Enabled: true},
	}}
	e.gateways.list = []domain.GatewayConfig{
		{Provider: "zarinpal", Enabled: true, Offered: true},
		{Provider: "idpay", Enabled: true, Offered: false},
		{Provider: "nextpay", Enabled: false},
	}
	r := allHealthy()
	r.Services[1].Status = "down"
	e.health.set(r)
	e.bot.setStats(botfiles.BotStats{StartedAt: 100, UpdatesTotal: 1000, WarningsTotal: 10, ErrorsTotal: 5, UpdatesQueued: 2, Goroutines: 40, HeapBytes: 1 << 20})

	e.step()
	check := func(metric string, want float64) {
		t.Helper()
		pts := e.series(metric)
		if len(pts) != 1 || pts[0].V != want {
			t.Errorf("%s: %+v, want %v", metric, pts, want)
		}
	}
	check("queue.pending_payments", 3)
	check("queue.provisioning", 0)
	check("queue.outbox", 0)
	check("service.up:core", 1)
	check("service.latency_ms:core", 5)
	check("service.up:bot", 0)
	check("panel.up:srv-1", 1)
	check("panel.up:srv-2", 0)
	check("gateway.up:zarinpal", 1)
	check("gateway.up:idpay", 0)
	check("license.ok", 1)
	check("bot.updates_queued", 2)
	check("bot.goroutines", 40)
	names := e.metrics()
	for _, bad := range []string{"service.latency_ms:bot", "panel.up:srv-3", "gateway.up:nextpay", "bot.updates_per_min"} {
		if slices.Contains(names, bad) {
			t.Errorf("recorded %s", bad)
		}
	}
	if !slices.Contains(names, "core.db_size_bytes") || !slices.Contains(names, "core.heap_bytes") {
		t.Errorf("core's figures: %v", names)
	}
}

// Counters become rates between two readings; a restarted bot (its counters start again) gives no
// rate for that round, never a negative one.
func TestStepTurnsTheBotsCountersIntoRates(t *testing.T) {
	e := newEnv(t)
	e.bot.setStats(botfiles.BotStats{StartedAt: 100, UpdatesTotal: 1000, WarningsTotal: 10, ErrorsTotal: 5})
	e.step()
	e.clk.add(30 * time.Second)
	e.bot.setStats(botfiles.BotStats{StartedAt: 100, UpdatesTotal: 1090, WarningsTotal: 10, ErrorsTotal: 8})
	e.step()
	last := func(metric string) []store.MonitorPoint { return e.series(metric) }
	if u := last("bot.updates_per_min"); len(u) != 1 || u[0].V != 180 {
		t.Fatalf("updates per minute: %+v", u)
	}
	if er := last("bot.errors_per_min"); len(er) != 1 || er[0].V != 6 {
		t.Fatalf("errors per minute: %+v", er)
	}
	if w := last("bot.warnings_per_min"); len(w) != 1 || w[0].V != 0 {
		t.Fatalf("a real zero is recorded: %+v", w)
	}

	// The bot restarted: its counters start again.
	e.clk.add(30 * time.Second)
	e.bot.setStats(botfiles.BotStats{StartedAt: 200, UpdatesTotal: 5})
	e.step()
	if u := last("bot.updates_per_min"); len(u) != 1 {
		t.Fatalf("a rate across a restart: %+v", u)
	}
	// ...and the next round has one again.
	e.clk.add(30 * time.Second)
	e.bot.setStats(botfiles.BotStats{StartedAt: 200, UpdatesTotal: 20})
	e.step()
	if u := last("bot.updates_per_min"); len(u) != 2 || u[1].V != 30 {
		t.Fatalf("after the restart: %+v", u)
	}
}

func TestStepWithSourcesDownRecordsOnlyWhatItKnows(t *testing.T) {
	e := newEnv(t)
	e.payments.err = errBoom
	e.bot.statErr = errBoom
	e.servers.list, e.servers.err = nil, errBoom
	e.gateways.err = errBoom
	e.step()
	names := e.metrics()
	for _, bad := range []string{"queue.pending_payments", "bot.updates_queued", "bot.goroutines", "bot.updates_per_min"} {
		if slices.Contains(names, bad) {
			t.Errorf("recorded %s although its source failed", bad)
		}
	}
	if !slices.Contains(names, "queue.provisioning") || !slices.Contains(names, "service.up:core") {
		t.Errorf("the rest should be there: %v", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "panel.up") || strings.HasPrefix(n, "gateway.up") {
			t.Errorf("recorded %s", n)
		}
	}
}

func TestStepDoesNothingWhileMonitoringIsOff(t *testing.T) {
	e := newEnv(t)
	e.set(domain.SettingMonitorEnabled, "false")
	e.snap(nil)
	e.clk.add(5 * time.Second)
	e.step()
	if names := e.metrics(); len(names) != 0 {
		t.Fatalf("recorded while off: %v", names)
	}
	// Switched on again, it records.
	e.set(domain.SettingMonitorEnabled, "true")
	e.step()
	if len(e.metrics()) == 0 {
		t.Fatal("nothing recorded after switching on")
	}
}

// A restart of core in the middle of a slot writes the same slot again, not a second point.
func TestStepIsRestartSafe(t *testing.T) {
	e := newEnv(t)
	e.snap(nil)
	e.clk.add(2 * time.Second)
	e.step()
	first := e.series("host.cpu_pct")

	e.build() // core restarted: a new monitor, no memory of what was recorded
	e.clk.add(2 * time.Second)
	e.step()
	again := e.series("host.cpu_pct")
	if len(first) != 1 || len(again) != 1 || first[0] != again[0] {
		t.Fatalf("before %+v, after %+v", first, again)
	}
	if g := e.series("core.goroutines"); len(g) != 1 {
		t.Fatalf("core's own figure twice in one slot: %+v", g)
	}
}

// The upkeep rolls old blocks into 5-minute ones and prunes what is older than the history kept.
func TestUpkeepRollsAndPrunes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := e.clk.now().Add(-8 * 24 * time.Hour)
	ancient := e.clk.now().Add(-40 * 24 * time.Hour)
	for _, at := range []time.Time{old, ancient} {
		if _, err := e.st.RecordSamples(ctx, at, []store.MonitorSample{{Name: "host.cpu_pct", Value: 10}}); err != nil {
			t.Fatal(err)
		}
	}
	e.m.upkeep(ctx, e.clk.now())
	var full, rolled int
	for _, q := range []struct {
		step int
		dst  *int
	}{{store.FullStep, &full}, {store.RolledStep, &rolled}} {
		if err := e.st.DB().QueryRow(ctx, `SELECT count(*) FROM core.monitor_series WHERE step = $1`, q.step).Scan(q.dst); err != nil {
			t.Fatal(err)
		}
	}
	if full != 0 || rolled != 1 {
		t.Fatalf("%d full blocks and %d rolled blocks left; want 0 and 1 (the day of 8 days ago, rolled; the one of 40 days ago, gone)", full, rolled)
	}
	pts, err := e.st.MonitorSeries(ctx, e.st.Conn(), "host.cpu_pct", e.clk.now().Add(-30*24*time.Hour), e.clk.now())
	if err != nil || len(pts) != 1 || pts[0].V != 10 {
		t.Fatalf("the rolled day: %+v %v", pts, err)
	}
	// Not due again straight away.
	before := e.m.roll
	e.m.upkeep(ctx, e.clk.now().Add(time.Minute))
	if !e.m.roll.Equal(before) {
		t.Fatal("rolled twice within the period")
	}
}
