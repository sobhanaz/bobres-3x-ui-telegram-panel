package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/cicd"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	crypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// migrateOnce migrates the test database once for the whole package.
var migrateOnce sync.Once

// t0 is the fake clock's start.
var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// clock is a movable time source.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// ---- fake sources ----

type fakeHealth struct {
	mu     sync.Mutex
	report syshealth.Report
	calls  int
}

func (f *fakeHealth) Check(context.Context) syshealth.Report {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.report
}

func (f *fakeHealth) set(r syshealth.Report) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.report = r
}

type fakeServers struct {
	mu   sync.Mutex
	list *domain.ServerList
	err  error
}

func (f *fakeServers) ListServers(context.Context) (*domain.ServerList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list, f.err
}

type fakeGateways struct {
	list []domain.GatewayConfig
	err  error
}

func (f *fakeGateways) GatewayConfigs(context.Context) ([]domain.GatewayConfig, error) {
	return f.list, f.err
}

type fakePayments struct {
	n   int
	err error
}

func (f *fakePayments) ListPending(context.Context, int) ([]domain.PendingPayment, error) {
	return make([]domain.PendingPayment, f.n), f.err
}

type fakeBackups struct {
	raw string
	err error
}

func (f *fakeBackups) Status(context.Context) (json.RawMessage, error) {
	return json.RawMessage(f.raw), f.err
}

type fakeBot struct {
	mu      sync.Mutex
	stats   *botfiles.BotStats
	statErr error
	ci      *cicd.Status
	ciErr   error
	ciCalls int
}

func (f *fakeBot) Stats(context.Context) (*botfiles.BotStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statErr != nil {
		return nil, f.statErr
	}
	s := *f.stats
	return &s, nil
}

func (f *fakeBot) CI(context.Context) (*cicd.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ciCalls++
	if f.ciErr != nil {
		return nil, f.ciErr
	}
	c := *f.ci
	return &c, nil
}

func (f *fakeBot) setStats(s botfiles.BotStats) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats, f.statErr = &s, nil
}

type fakeLogs struct {
	lines []logging.Line
	err   error
}

func (f *fakeLogs) RecentLogs(context.Context) ([]logging.Line, error) { return f.lines, f.err }

// ---- the environment ----

type env struct {
	t        *testing.T
	st       *store.Store
	dom      *domain.Service
	dir      string
	clk      *clock
	m        *Monitor
	health   *fakeHealth
	servers  *fakeServers
	gateways *fakeGateways
	payments *fakePayments
	backups  *fakeBackups
	bot      *fakeBot
	logs     map[string]LogSource
}

// newEnv builds a monitor on a real database with fake sources, a movable clock and an empty
// collector directory. The monitoring tables, the settings and the alert states start empty.
func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.DSN(t)
	migrateOnce.Do(func() {
		if err := migrate.Up(ctx, dsn, "core"); err != nil {
			t.Fatal(err)
		}
	})
	st, err := store.New(ctx, testdb.WithPoolCap(t, dsn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := testdb.Truncate(ctx, st.DB(), `core.monitor_series, core.monitor_metric, core.settings, core.alert_state, core.outbox_core, core.system_updates`); err != nil {
		t.Fatal(err)
	}
	env32, err := crypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	dom := domain.New(st, domain.Config{OwnerTelegramIDs: []int64{9001}, Secrets: env32})
	e := &env{t: t, st: st, dom: dom, dir: t.TempDir(), clk: &clock{t: t0},
		health: &fakeHealth{}, servers: &fakeServers{list: &domain.ServerList{}}, gateways: &fakeGateways{}, payments: &fakePayments{},
		backups: &fakeBackups{raw: `{"backups":[]}`}, bot: &fakeBot{stats: &botfiles.BotStats{StartedAt: 1}, ci: &cicd.Status{}},
		logs: map[string]LogSource{}}
	e.health.set(allHealthy())
	e.build()
	return e
}

// build (re)creates the monitor from the env's current fakes.
func (e *env) build() {
	e.m = New(Config{Store: e.st, Domain: e.dom, Dir: e.dir, Health: e.health, Servers: e.servers, Gateways: e.gateways, Payments: e.payments,
		Backups: e.backups, Bot: e.bot, Logs: e.logs, Version: "0.7.0", Now: e.clk.now})
}

func allHealthy() syshealth.Report {
	r := syshealth.Report{Version: "0.7.0"}
	for _, n := range []string{"core", "bot", "payments", "provisioner"} {
		r.Services = append(r.Services, syshealth.Service{Name: n, Status: syshealth.StatusOK, Version: "0.7.0", LatencyMS: 5, Checks: map[string]string{}})
	}
	return r
}

// set stores a setting through the domain, as the dashboard would.
func (e *env) set(key, value string) {
	e.t.Helper()
	raw, _ := json.Marshal(value)
	if err := e.st.SetSetting(context.Background(), e.st.Conn(), key, raw); err != nil {
		e.t.Fatal(err)
	}
}

// snap writes a snapshot made at the clock's time, after the mutator changed the default one.
func (e *env) snap(mut func(*snapshot.Snapshot)) *snapshot.Snapshot {
	e.t.Helper()
	s := baseSnap(e.clk.now())
	if mut != nil {
		mut(s)
	}
	if err := snapshot.Write(e.dir, s); err != nil {
		e.t.Fatal(err)
	}
	e.forgetRead()
	return s
}

// forgetRead lets the monitor read the file again at its next look, as if time had passed.
func (e *env) forgetRead() {
	e.m.mu.Lock()
	e.m.readAt = time.Time{}
	e.m.mu.Unlock()
}

// series reads a metric's points over the last hour.
func (e *env) series(metric string) []store.MonitorPoint {
	e.t.Helper()
	pts, err := e.st.MonitorSeries(context.Background(), e.st.Conn(), metric, e.clk.now().Add(-time.Hour), e.clk.now().Add(time.Minute))
	if err != nil {
		e.t.Fatal(err)
	}
	return pts
}

func (e *env) metrics() []string {
	e.t.Helper()
	names, err := e.st.MonitorMetrics(context.Background(), e.st.Conn())
	if err != nil {
		e.t.Fatal(err)
	}
	return names
}

// checks runs the alert rules and indexes the checks by key.
func (e *env) checks() (map[string]domain.MonitorCheck, bool) {
	e.t.Helper()
	res, err := e.m.WatchdogChecks(context.Background(), e.clk.now())
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]domain.MonitorCheck{}
	for _, c := range res.Checks {
		if _, dup := out[c.Key]; dup {
			e.t.Fatalf("check %s given twice", c.Key)
		}
		out[c.Key] = c
	}
	return out, res.Covered
}

// ---- snapshots ----

func ctr(service, state, health string, restarts int, cpu float64) snapshot.Container {
	c := cpu
	id := fmt.Sprintf("%012x", hashOf(service))
	return snapshot.Container{ID: id, Name: "bobres-" + service + "-1", Project: "bobres", Service: service, Image: "ghcr.io/x/" + service + ":0.7.0",
		State: state, Health: health, Restarts: restarts, StartedAt: t0.Add(-2 * time.Hour).Unix(), CPUPct: &c, MemBytes: 100 << 20, MemLimit: 512 << 20,
		NetRx: 1000, NetTx: 2000}
}

func hashOf(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := range len(s) {
		h = (h ^ uint64(s[i])) * 1099511628211
	}
	return h & 0xffffffffffff
}

var stackServices = []string{"core", "bot", "payments", "provisioner", "postgres", "redis", "caddy", "tls-agent"}

func baseSnap(at time.Time) *snapshot.Snapshot {
	cpu := 12.5
	var boxes []snapshot.Container
	for _, svc := range stackServices {
		h := snapshot.HealthNone
		if svc == "postgres" || svc == "redis" {
			h = snapshot.HealthHealthy
		}
		boxes = append(boxes, ctr(svc, snapshot.StateRunning, h, 0, 3))
	}
	return &snapshot.Snapshot{Schema: snapshot.Schema, GeneratedAt: at.Unix(), GeneratedMS: at.UnixMilli(), IntervalS: 30, DurationMS: 120, Version: "0.7.0",
		Host: &snapshot.Host{Hostname: "srv1", OS: "Ubuntu 24.04", Kernel: "6.8.0", Arch: "amd64", UptimeS: 100000, Cores: 4,
			CPU: snapshot.CPU{Pct: &cpu, Busy: 100, Total: 1000}, Load: snapshot.Load{0.5, 0.4, 0.3},
			Mem:   snapshot.Mem{Total: 8 << 30, Available: 6 << 30},
			Swap:  snapshot.Swap{Total: 1 << 30, Used: 1 << 20},
			Disks: []snapshot.Disk{{Mount: "/", Total: 100 << 30, Used: 40 << 30, Avail: 55 << 30, Roles: []string{"install"}}},
			Net:   snapshot.Net{Ifaces: []snapshot.Iface{{Name: "eth0", RxBytes: 1000, TxBytes: 2000}}}},
		Docker: &snapshot.Docker{OK: true, Version: "29.1.0", API: "1.52", Containers: boxes,
			Compose: &snapshot.Compose{Expected: stackServices, Missing: []string{}}},
	}
}

var errBoom = errors.New("boom")
