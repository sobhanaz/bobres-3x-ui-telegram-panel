package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/monitor"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

type monLogs struct{ lines []logging.Line }

func (m monLogs) RecentLogs(context.Context) ([]logging.Line, error) { return m.lines, nil }

type monFixture struct {
	*fixture
	dir    string
	mon    *monitor.Monitor
	offset atomic.Int64 // the monitor's clock runs this many nanoseconds ahead of the wall clock
}

func (m *monFixture) now() time.Time { return time.Now().Add(time.Duration(m.offset.Load())) }

// advance moves the monitor's clock (it reads the collector's file at most every two seconds).
func (m *monFixture) advance(d time.Duration) { m.offset.Add(int64(d)) }

func newMonFixture(t *testing.T) *monFixture {
	t.Helper()
	f := newFixture(t)
	if err := testdb.Truncate(context.Background(), f.st.DB(), `core.monitor_series, core.monitor_metric, core.alert_state, core.outbox_core, core.system_updates, core.settings`); err != nil {
		t.Fatal(err)
	}
	m := &monFixture{fixture: f, dir: t.TempDir()}
	m.mon = monitor.New(monitor.Config{Store: f.st, Domain: f.dom, Dir: m.dir, Version: "0.7.0", Now: m.now,
		Logs: map[string]monitor.LogSource{"bot": monLogs{lines: []logging.Line{{Time: time.Now().Unix(), Level: "ERROR", Msg: "send failed", Attrs: `{"user_id":7}`}}}}})
	f.api.cfg.Monitor = m.mon
	return m
}

func (m *monFixture) login(tg int64, role string) *browser {
	m.t.Helper()
	m.staffUser(tg, role)
	b := m.browser()
	if code := b.loginWithLink(tg); code != 200 {
		m.t.Fatalf("login %s: %d", role, code)
	}
	return b
}

func (m *monFixture) snapshot(mut func(*snapshot.Snapshot)) {
	m.t.Helper()
	cpu := 20.0
	at := m.now()
	s := &snapshot.Snapshot{Schema: snapshot.Schema, GeneratedAt: at.Unix(), GeneratedMS: at.UnixMilli(), IntervalS: 30, Version: "0.7.0",
		Host: &snapshot.Host{Hostname: "srv", Cores: 2, UptimeS: 1000, CPU: snapshot.CPU{Pct: &cpu}, Mem: snapshot.Mem{Total: 4 << 30, Available: 3 << 30},
			Disks: []snapshot.Disk{{Mount: "/", Total: 10 << 30, Used: 4 << 30, Avail: 5 << 30}}}}
	if mut != nil {
		mut(s)
	}
	if err := snapshot.Write(m.dir, s); err != nil {
		m.t.Fatal(err)
	}
}

var monPaths = []string{"/api/v1/monitor/overview", "/api/v1/monitor/series?metric=host.cpu_pct", "/api/v1/monitor/metrics", "/api/v1/monitor/events"}

func TestMonitorEndpointsNeedAStaffSessionOfOwnerOrAdmin(t *testing.T) {
	m := newMonFixture(t)
	anon := m.browser()
	for _, p := range monPaths {
		if code, _ := anon.do(http.MethodGet, p, nil); code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d", p, code)
		}
	}
	support := m.login(7101, "support")
	customer := m.staffUser(7102, "user")
	_ = customer
	for _, p := range monPaths {
		if code, out := support.do(http.MethodGet, p, nil); code != http.StatusForbidden || out["error"] != "forbidden" {
			t.Errorf("%s for support: %d %v", p, code, out)
		}
	}
	for role, tg := range map[string]int64{"owner": ownerTG, "admin": 7103} {
		b := m.login(tg, role)
		for _, p := range monPaths {
			if code, out := b.do(http.MethodGet, p, nil); code != http.StatusOK {
				t.Errorf("%s for %s: %d %v", p, role, code, out)
			}
		}
	}
	// A staff member whose role was taken away is refused at once, session or not.
	b := m.login(7104, "admin")
	if code, _ := b.do(http.MethodGet, monPaths[0], nil); code != 200 {
		t.Fatal("setup")
	}
	m.staffUser(7104, "support")
	if code, _ := b.do(http.MethodGet, monPaths[0], nil); code != http.StatusForbidden {
		t.Errorf("after the role was lowered: %d", code)
	}
}

func TestMonitorWritesAreNotAllowed(t *testing.T) {
	m := newMonFixture(t)
	b := m.login(ownerTG, "owner")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		code, _ := b.do(method, "/api/v1/monitor/overview", map[string]string{"x": "y"})
		if code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", method, code)
		}
	}
}

func TestMonitorNotConfigured(t *testing.T) {
	m := newMonFixture(t)
	m.api.cfg.Monitor = nil
	b := m.login(ownerTG, "owner")
	for _, p := range monPaths {
		if code, out := b.do(http.MethodGet, p, nil); code != http.StatusServiceUnavailable || out["error"] != "not_configured" {
			t.Errorf("%s: %d %v", p, code, out)
		}
	}
}

func TestMonitorOverviewWithAndWithoutACollector(t *testing.T) {
	m := newMonFixture(t)
	b := m.login(ownerTG, "owner")

	code, out := b.do(http.MethodGet, "/api/v1/monitor/overview", nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	host := out["host"].(map[string]any)
	coll := out["collector"].(map[string]any)
	if host["available"] != false || !strings.Contains(fmt.Sprint(host["reason"]), "not installed") || coll["installed"] != false || coll["fresh"] != false {
		t.Fatalf("no collector: host %v collector %v", host, coll)
	}
	if out["status"] != "ok" || len(out["components"].([]any)) == 0 || out["containers"] == nil {
		t.Fatalf("overview: %v", out)
	}

	m.snapshot(nil)
	m.advance(3 * time.Second)
	_, out = b.do(http.MethodGet, "/api/v1/monitor/overview", nil)
	host = out["host"].(map[string]any)
	if host["available"] != true || host["hostname"] != "srv" || host["cores"] != float64(2) || host["cpu_pct"] != 20.0 {
		t.Fatalf("collector: %v", host)
	}
	disks := host["disks"].([]any)
	if len(disks) != 1 || disks[0].(map[string]any)["mount"] != "/" || disks[0].(map[string]any)["total"] != float64(10<<30) {
		t.Fatalf("disks: %v", disks)
	}
	coll = out["collector"].(map[string]any)
	if coll["installed"] != true || coll["fresh"] != true {
		t.Fatalf("collector: %v", coll)
	}
}

func TestMonitorSeriesRequests(t *testing.T) {
	m := newMonFixture(t)
	b := m.login(ownerTG, "owner")
	base := time.Now().Add(-10 * time.Minute)
	for i, v := range []float64{10, 20, 30} {
		if _, err := m.st.RecordSamples(context.Background(), base.Add(time.Duration(i)*30*time.Second), []store.MonitorSample{
			{Name: "host.cpu_pct", Value: v}, {Name: "host.disk_pct:/", Value: v + 1}}); err != nil {
			t.Fatal(err)
		}
	}

	code, out := b.do(http.MethodGet, "/api/v1/monitor/series?metric=host.cpu_pct&range=1h", nil)
	pts, _ := out["points"].([]any)
	if code != 200 || out["metric"] != "host.cpu_pct" || out["unit"] != "percent" || out["step_s"] != float64(30) || len(pts) != 3 {
		t.Fatalf("%d %v", code, out)
	}
	if p := pts[0].([]any); len(p) != 2 || p[1] != 10.0 || p[0].(float64) < float64(base.Unix()-30) {
		t.Fatalf("point: %v", p)
	}
	// The default range is an hour; a longer one buckets.
	if _, out := b.do(http.MethodGet, "/api/v1/monitor/series?metric=host.cpu_pct", nil); out["step_s"] != float64(30) {
		t.Errorf("default range: %v", out["step_s"])
	}
	if _, out := b.do(http.MethodGet, "/api/v1/monitor/series?metric=host.cpu_pct&range=24h", nil); out["step_s"] != float64(300) || len(out["points"].([]any)) != 1 {
		t.Errorf("24h: %v", out)
	}

	// A metric that has a subject, the subject being a mount point.
	code, out = b.do(http.MethodGet, "/api/v1/monitor/series?metric="+urlEscape("host.disk_pct:/")+"&range=6h", nil)
	if code != 200 || out["unit"] != "percent" || len(out["points"].([]any)) == 0 {
		t.Fatalf("subject: %d %v", code, out)
	}

	// A metric nobody recorded yet: empty, not an error, and never null.
	code, out = b.do(http.MethodGet, "/api/v1/monitor/series?metric=queue.outbox&range=7d", nil)
	if code != 200 || out["points"] == nil || len(out["points"].([]any)) != 0 || out["step_s"] != float64(1800) {
		t.Fatalf("no data: %d %v", code, out)
	}

	// Several at once.
	code, out = b.do(http.MethodGet, "/api/v1/monitor/series?metrics="+urlEscape("host.cpu_pct,host.disk_pct:/,host.cpu_pct")+"&range=1h", nil)
	list, _ := out["series"].([]any)
	if code != 200 || out["range"] != "1h" || len(list) != 2 || list[1].(map[string]any)["metric"] != "host.disk_pct:/" {
		t.Fatalf("several: %d %v", code, out)
	}
}

func TestMonitorSeriesRefusesBadRequests(t *testing.T) {
	m := newMonFixture(t)
	b := m.login(ownerTG, "owner")
	many := make([]string, 13)
	for i := range many {
		many[i] = "host.cpu_pct"
		if i%2 == 1 {
			many[i] = "queue.outbox"
		}
	}
	// 13 names, but only two distinct: the limit is on what is asked, not on what is left.
	bad := map[string]string{
		"no metric":        "/api/v1/monitor/series?range=1h",
		"both forms":       "/api/v1/monitor/series?metric=host.cpu_pct&metrics=host.cpu_pct",
		"unknown metric":   "/api/v1/monitor/series?metric=host.nothing",
		"missing subject":  "/api/v1/monitor/series?metric=host.disk_pct",
		"sql":              "/api/v1/monitor/series?metric=" + urlEscape("host.cpu_pct' OR 1=1 --"),
		"path":             "/api/v1/monitor/series?metric=" + urlEscape("../../etc/passwd"),
		"bad range":        "/api/v1/monitor/series?metric=host.cpu_pct&range=2h",
		"range upper case": "/api/v1/monitor/series?metric=host.cpu_pct&range=1H",
		"too many":         "/api/v1/monitor/series?metrics=" + urlEscape(strings.Join(many, ",")),
		"long name":        "/api/v1/monitor/series?metric=" + strings.Repeat("a", 500),
		"empty entry":      "/api/v1/monitor/series?metrics=" + urlEscape("host.cpu_pct,"),
	}
	for name, p := range bad {
		if code, out := b.do(http.MethodGet, p, nil); code != http.StatusBadRequest || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
}

func urlEscape(s string) string { return url.QueryEscape(s) }

func TestMonitorMetricsListing(t *testing.T) {
	m := newMonFixture(t)
	b := m.login(7120, "admin")
	m.mon.Step(context.Background())
	code, out := b.do(http.MethodGet, "/api/v1/monitor/metrics", nil)
	list, _ := out["metrics"].([]any)
	if code != 200 || len(list) == 0 {
		t.Fatalf("%d %v", code, out)
	}
	first := list[0].(map[string]any)
	if first["metric"] == "" || first["unit"] == "" || first["agg"] == "" {
		t.Fatalf("%v", first)
	}
}

// alertEvent writes a watchdog announcement into the outbox as the watchdog does.
func (m *monFixture) alertEvent(key, state, detail string, at time.Time) {
	m.t.Helper()
	payload, _ := json.Marshal(map[string]any{"key": key, "severity": "critical", "state": state, "title_key": "alert.server_down",
		"args": map[string]string{"service": "x"}, "detail": detail, "since": at.Unix(), "at": at.Unix()})
	if _, err := m.st.DB().Exec(context.Background(), `INSERT INTO core.outbox_core (topic, payload, event_id) VALUES ('system.alert.v1', $1, gen_random_uuid())`, payload); err != nil {
		m.t.Fatal(err)
	}
}

func TestMonitorEventsByRole(t *testing.T) {
	m := newMonFixture(t)
	now := time.Now()
	m.alertEvent("server.abc", "problem", "dial tcp 10.0.0.5:2053: i/o timeout", now.Add(-time.Minute))
	m.alertEvent("gateway.zarinpal", "problem", "merchant rejected the request", now.Add(-2*time.Minute))
	owner := m.login(ownerTG, "owner")
	admin := m.login(7110, "admin")

	read := func(b *browser, query string) map[string]any {
		t.Helper()
		code, out := b.do(http.MethodGet, "/api/v1/monitor/events"+query, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", query, code, out)
		}
		return out
	}
	byKey := func(out map[string]any, key string) map[string]any {
		for _, it := range out["items"].([]any) {
			if it.(map[string]any)["key"] == key {
				return it.(map[string]any)
			}
		}
		return nil
	}
	count := func(out map[string]any, kind string) (n int) {
		for _, it := range out["items"].([]any) {
			if it.(map[string]any)["kind"] == kind {
				n++
			}
		}
		return n
	}

	o := read(owner, "")
	if o["logs_visible"] != true || count(o, "log") < 1 || count(o, "alert") != 2 {
		t.Fatalf("owner: %v", o)
	}
	if g := byKey(o, "gateway.zarinpal"); g == nil || g["detail"] != "merchant rejected the request" || g["level"] != "error" || g["source"] != "alert" {
		t.Fatalf("owner sees the gateway detail: %v", g)
	}
	a := read(admin, "?level=warn&limit=50")
	if a["logs_visible"] != false || count(a, "log") != 0 || count(a, "alert") != 2 {
		t.Fatalf("admin: %v", a)
	}
	// An admin manages servers and reads their errors elsewhere, but the gateway pages are the owner's.
	if g := byKey(a, "gateway.zarinpal"); g == nil || g["detail"] != "" {
		t.Fatalf("admin must not see the gateway detail: %v", g)
	}
	if s := byKey(a, "server.abc"); s == nil || s["detail"] != "dial tcp 10.0.0.5:2053: i/o timeout" {
		t.Fatalf("admin sees the server detail: %v", s)
	}
	items := o["items"].([]any)
	for i := 1; i < len(items); i++ {
		if items[i].(map[string]any)["at"].(float64) > items[i-1].(map[string]any)["at"].(float64) {
			t.Fatal("not newest first")
		}
	}
	if e := read(owner, "?level=error&limit=1"); len(e["items"].([]any)) != 1 || e["truncated"] != true {
		t.Fatalf("limit: %v", e)
	}
}

func TestMonitorEventsRefusesBadParameters(t *testing.T) {
	m := newMonFixture(t)
	b := m.login(ownerTG, "owner")
	for _, q := range []string{"?level=info", "?level=ERROR", "?level=", "?limit=0", "?limit=201", "?limit=-1", "?limit=abc", "?limit=1e2", "?limit=10.5"} {
		code, out := b.do(http.MethodGet, "/api/v1/monitor/events"+q, nil)
		if q == "?level=" { // an empty value is the default
			if code != 200 {
				t.Errorf("%s: %d %v", q, code, out)
			}
			continue
		}
		if code != http.StatusBadRequest || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", q, code, out)
		}
	}
	for _, q := range []string{"", "?limit=1", "?limit=200", "?level=warn", "?level=error&limit=100"} {
		if code, out := b.do(http.MethodGet, "/api/v1/monitor/events"+q, nil); code != 200 {
			t.Errorf("%s: %d %v", q, code, out)
		}
	}
}

// The permission is its own flag: owner and admin hold it, support does not, nobody else.
func TestMonitorPermission(t *testing.T) {
	for role, want := range map[string]bool{"owner": true, "admin": true, "support": false, "user": false, "": false} {
		if got := Allowed(role, PermMonitorRead); got != want {
			t.Errorf("%q: %v", role, got)
		}
	}
}
