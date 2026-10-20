package monitor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/cicd"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

func (e *env) overview() *Overview {
	e.t.Helper()
	e.m.probeMu.Lock()
	e.m.probed = nil // each look asks again
	e.m.probeMu.Unlock()
	e.m.logMu.Lock()
	e.m.logs = nil
	e.m.logMu.Unlock()
	return e.m.Overview(context.Background())
}

func comp(o *Overview, id string) *Component {
	for i := range o.Components {
		if o.Components[i].ID == id {
			return &o.Components[i]
		}
	}
	return nil
}

func TestOverviewOfAHealthyStack(t *testing.T) {
	e := newEnv(t)
	e.servers.list = &domain.ServerList{Servers: []domain.Server{
		{ID: "s1", Name: "DE-1", Enabled: true, Healthy: true, CheckedAt: 1, Clients: 12, PanelVersion: "2.4.0"},
	}}
	e.gateways.list = []domain.GatewayConfig{{Provider: "zarinpal", Enabled: true, Offered: true}, {Provider: "idpay"}}
	e.payments.n = 2
	e.backups.raw = `{"backups":[{"name":"b1","created_at":"` + e.clk.now().Add(-3*time.Hour).Format(time.RFC3339) + `","size":1,"ok":true}],"running":false,"last_error":"","next_run":0,"disk_free":1}`
	e.bot.ci = &cicd.Status{Available: true, CheckedAt: 99, Repo: "x/y", Runs: []cicd.Run{ciRun("CI", "success")},
		Releases: []cicd.Release{{Tag: "v0.7.0", PublishedAt: 5}}}
	e.m.refreshCI(context.Background())
	e.snap(nil)
	e.clk.add(10 * time.Second)

	o := e.overview()
	if o.Status != OverallOK || o.GeneratedAt != e.clk.now().Unix() {
		t.Fatalf("status %s at %d", o.Status, o.GeneratedAt)
	}
	for id, kind := range map[string]string{"core": KindService, "bot": KindService, "payments": KindService, "provisioner": KindService,
		"postgres": KindDatastore, "redis": KindDatastore, "caddy": KindService, "tls-agent": KindService, "host": KindHost,
		"panel.s1": KindPanel, "gateway.zarinpal": KindGateway, "collector": KindJob, "backups": KindJob, "ci": KindJob} {
		c := comp(o, id)
		if c == nil || c.Kind != kind || c.Status != StatusOK || c.Name == "" {
			t.Errorf("%s: %+v", id, c)
		}
	}
	if comp(o, "gateway.idpay") != nil {
		t.Error("a gateway that is switched off is not a component")
	}
	if c := comp(o, "core"); c.Version != "0.7.0" || c.UptimeS == nil || c.Since == nil {
		t.Errorf("core: %+v", c)
	}
	if c := comp(o, "bot"); c.UptimeS == nil || *c.UptimeS != 2*3600+10 {
		t.Errorf("bot uptime from its container: %+v", c)
	}
	if c := comp(o, "panel.s1"); c.Version != "2.4.0" || !strings.Contains(c.Detail, "12 clients") {
		t.Errorf("panel: %+v", c)
	}
	if c := comp(o, "postgres"); c.Version != "0.7.0" || !strings.HasPrefix(c.Detail, "answers in ") {
		t.Errorf("postgres: %+v", c)
	}

	h := o.Host
	if !h.Available || h.Hostname != "srv1" || h.Cores != 4 || h.CPUPct == nil || *h.CPUPct != 12.5 || h.Load != [3]float64{0.5, 0.4, 0.3} ||
		h.Mem.Total != 8<<30 || h.Mem.Used != 2<<30 || h.Swap.Total != 1<<30 || h.UptimeS != 100000 || h.OS != "Ubuntu 24.04" || h.Kernel != "6.8.0" ||
		!h.Docker.OK || h.Docker.Version != "29.1.0" {
		t.Errorf("host: %+v", h)
	}
	if len(h.Disks) != 1 || h.Disks[0].Mount != "/" || h.Disks[0].Used != 40<<30 || h.Disks[0].Total != 100<<30 || h.Disks[0].Pct != 42.1 {
		t.Errorf("disks: %+v", h.Disks)
	}
	if len(o.Containers) != 8 {
		t.Fatalf("containers: %d", len(o.Containers))
	}
	if b := o.Containers[0]; b.Project != "bobres" || b.MemBytes != 100<<20 || b.MemLimit != 512<<20 || b.NetRxBytes != 1000 || b.Image == "" {
		t.Errorf("container: %+v", b)
	}
	if o.Kubernetes.Detected || o.Kubernetes.Reason != "not detected" || len(o.Kubernetes.Pods) != 0 {
		t.Errorf("kubernetes: %+v", o.Kubernetes)
	}
	c := o.Counters
	if c.OpenAlerts == nil || *c.OpenAlerts != 0 || c.PaymentsPending == nil || *c.PaymentsPending != 2 || c.ProvisionQueue == nil || c.CustomersTotal == nil ||
		c.OrdersPending == nil || c.OutboxBacklog == nil || c.TicketsOpen == nil || c.ServicesActive == nil {
		t.Errorf("counters: %+v", c)
	}
	if !o.CI.Available || o.CI.LatestRelease == nil || o.CI.LatestRelease.Tag != "v0.7.0" || o.CI.MainFailing == nil || *o.CI.MainFailing || len(o.CI.Runs) != 1 {
		t.Errorf("ci: %+v", o.CI)
	}
	if k := o.Collector; !k.Installed || !k.Fresh || k.AgeS != 10 || k.Version != "0.7.0" || k.IntervalS != 30 || k.Error != "" {
		t.Errorf("collector: %+v", k)
	}
	if len(o.Deployments) != 0 {
		t.Errorf("deployments: %+v", o.Deployments)
	}
}

func TestOverviewWithoutACollector(t *testing.T) {
	e := newEnv(t)
	o := e.overview()
	if o.Status != OverallOK {
		t.Fatalf("a stack with no collector is not degraded for that: %s", o.Status)
	}
	if o.Host.Available || !strings.Contains(o.Host.Reason, "not installed") || o.Host.CPUPct != nil || len(o.Host.Disks) != 0 {
		t.Errorf("host: %+v", o.Host)
	}
	if len(o.Containers) != 0 || o.Kubernetes.Detected {
		t.Errorf("containers or kubernetes without a collector: %d %+v", len(o.Containers), o.Kubernetes)
	}
	if c := comp(o, "host"); c.Status != StatusUnknown || c.Detail == "" {
		t.Errorf("host component: %+v", c)
	}
	if c := comp(o, "collector"); c.Status != StatusUnknown {
		t.Errorf("collector component: %+v", c)
	}
	for _, id := range []string{"redis", "caddy", "tls-agent"} {
		if comp(o, id) != nil {
			t.Errorf("%s is known only from the containers", id)
		}
	}
	// What core can ask itself is still there.
	if comp(o, "core").Status != StatusOK || comp(o, "postgres").Status != StatusOK {
		t.Errorf("core and the database: %+v %+v", comp(o, "core"), comp(o, "postgres"))
	}
	if o.Collector.Installed || o.Collector.Fresh {
		t.Errorf("collector: %+v", o.Collector)
	}
}

func TestOverviewWithAStaleCollector(t *testing.T) {
	e := newEnv(t)
	e.snap(nil)
	e.clk.add(4 * time.Minute)
	o := e.overview()
	if o.Host.Available || !strings.Contains(o.Host.Reason, "sent nothing for 4 minutes") || len(o.Containers) != 0 {
		t.Fatalf("host: %+v containers %d", o.Host, len(o.Containers))
	}
	if k := o.Collector; !k.Installed || k.Fresh || k.AgeS != 240 {
		t.Errorf("collector: %+v", k)
	}
	if c := comp(o, "collector"); c.Status != StatusWarn {
		t.Errorf("collector component: %+v", c)
	}
	if o.Status != OverallDegraded {
		t.Errorf("a collector that went quiet is a warning: %s", o.Status)
	}
}

func TestOverallStatusRules(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*env)
		alert string // severity of an open alert
		want  string
	}{
		{"all fine", func(*env) {}, "", OverallOK},
		{"a required service is down", func(e *env) {
			r := allHealthy()
			r.Services[1].Status = syshealth.StatusDown
			e.health.set(r)
		}, "", OverallDown},
		{"a required service is not ready", func(e *env) {
			r := allHealthy()
			r.Services[2].Status, r.Services[2].Checks = syshealth.StatusNotReady, map[string]string{"db": "fail"}
			e.health.set(r)
		}, "", OverallDegraded},
		{"the optional backup service is down", func(e *env) {
			r := allHealthy()
			r.Services = append(r.Services, syshealth.Service{Name: "backup", Status: syshealth.StatusDown})
			e.health.set(r)
		}, "", OverallDegraded},
		{"a panel is down", func(e *env) {
			e.servers.list = &domain.ServerList{Servers: []domain.Server{{ID: "s1", Name: "DE", Enabled: true, CheckedAt: 1, HealthError: "timeout"}}}
		}, "", OverallDegraded},
		{"a panel never checked is unknown, not bad", func(e *env) {
			e.servers.list = &domain.ServerList{Servers: []domain.Server{{ID: "s1", Name: "DE", Enabled: true}}}
		}, "", OverallOK},
		{"a gateway with a settings problem", func(e *env) {
			e.gateways.list = []domain.GatewayConfig{{Provider: "zarinpal", Enabled: true, Offered: false, Problem: "unreadable"}}
		}, "", OverallDegraded},
		{"a critical alert is open", func(*env) {}, "critical", OverallDegraded},
		{"a warning alert alone does not degrade", func(*env) {}, "warning", OverallOK},
		{"the database does not answer", func(e *env) { e.st.Close() }, "", OverallDown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tc.edit(e)
			if tc.alert != "" {
				if err := e.st.WithTx(context.Background(), func(tx pgx.Tx) error {
					return e.st.PutAlertState(context.Background(), tx, &store.AlertState{Key: "x.y", State: store.AlertProblem, Severity: tc.alert,
						TitleKey: "alert.critical", Since: e.clk.now(), Args: map[string]string{}})
				}); err != nil {
					t.Fatal(err)
				}
			}
			if got := e.overview().Status; got != tc.want {
				t.Fatalf("status %s, want %s", got, tc.want)
			}
		})
	}
}

func TestOverviewContainerStates(t *testing.T) {
	e := newEnv(t)
	e.snap(func(s *snapshot.Snapshot) {
		for i := range s.Docker.Containers {
			switch s.Docker.Containers[i].Service {
			case "redis":
				s.Docker.Containers[i].State = snapshot.StateRestarting
				s.Docker.Containers[i].Restarts = 9
			case "caddy":
				s.Docker.Containers[i].Health = snapshot.HealthUnhealthy
			case "tls-agent":
				code := 137
				s.Docker.Containers[i].State, s.Docker.Containers[i].ExitCode, s.Docker.Containers[i].OOMKilled = snapshot.StateExited, &code, true
			}
		}
		s.Docker.Compose.Missing = []string{"backup-x"}
	})
	e.clk.add(5 * time.Second)
	o := e.overview()
	if c := comp(o, "redis"); c.Status != StatusDown || !strings.Contains(c.Detail, "restarting (9") {
		t.Errorf("redis: %+v", c)
	}
	if c := comp(o, "caddy"); c.Status != StatusWarn || !strings.Contains(c.Detail, "health check fails") {
		t.Errorf("caddy: %+v", c)
	}
	if c := comp(o, "tls-agent"); c.Status != StatusDown || !strings.Contains(c.Detail, "exit code 137") || !strings.Contains(c.Detail, "memory") {
		t.Errorf("tls-agent: %+v", c)
	}
	if c := comp(o, "backup-x"); c == nil || c.Status != StatusDown {
		t.Errorf("a service the install runs with no container: %+v", c)
	}
	if o.Status != OverallDegraded {
		t.Errorf("status %s: only required components make it down", o.Status)
	}
}

func TestOverviewHostWarnsPastTheAlertThresholds(t *testing.T) {
	e := newEnv(t)
	e.snap(func(s *snapshot.Snapshot) {
		s.Host.Disks[0] = snapshot.Disk{Mount: "/", Total: 100 << 30, Used: 90 << 30, Avail: 8 << 30}
		s.Host.Mem.Available = 100 << 20
	})
	e.clk.add(time.Second)
	c := comp(e.overview(), "host")
	if c.Status != StatusWarn || !strings.Contains(c.Detail, "disk / 92% full") || !strings.Contains(c.Detail, "memory 99% used") {
		t.Fatalf("%+v", c)
	}
}

func TestOverviewKubernetes(t *testing.T) {
	e := newEnv(t)
	e.snap(func(s *snapshot.Snapshot) {
		s.Kubernetes = &snapshot.Kube{Detected: true, Via: "kubectl",
			Nodes: []snapshot.KubeNode{{Name: "n1", Ready: true, Roles: "control-plane", Version: "v1.30.1"}},
			Pods:  []snapshot.KubePod{{Namespace: "default", Name: "web-1", Phase: "Running", Ready: true, Restarts: 2, Node: "n1"}}}
	})
	e.clk.add(time.Second)
	k := e.overview().Kubernetes
	if !k.Detected || len(k.Nodes) != 1 || k.Nodes[0].Name != "n1" || !k.Nodes[0].Ready || len(k.Pods) != 1 || k.Pods[0].Restarts != 2 || k.Pods[0].Namespace != "default" {
		t.Fatalf("%+v", k)
	}
}

// Lists are lists and not null in the JSON, whatever is missing: a page iterates them.
func TestOverviewJSONShape(t *testing.T) {
	e := newEnv(t)
	raw, err := json.Marshal(e.overview())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"generated_at", "status", "components", "host", "containers", "kubernetes", "counters", "ci", "collector", "deployments"} {
		if _, ok := got[key]; !ok {
			t.Errorf("no %q", key)
		}
	}
	for _, key := range []string{"components", "containers", "deployments"} {
		if string(got[key]) == "null" {
			t.Errorf("%s is null", key)
		}
	}
	var host, kube, ci map[string]json.RawMessage
	_ = json.Unmarshal(got["host"], &host)
	_ = json.Unmarshal(got["kubernetes"], &kube)
	_ = json.Unmarshal(got["ci"], &ci)
	if string(host["disks"]) != "[]" || string(host["available"]) != "false" || string(host["cpu_pct"]) != "null" {
		t.Errorf("host: disks=%s available=%s cpu=%s", host["disks"], host["available"], host["cpu_pct"])
	}
	for _, key := range []string{"nodes", "pods"} {
		if string(kube[key]) != "[]" {
			t.Errorf("kubernetes.%s = %s", key, kube[key])
		}
	}
	for _, key := range []string{"runs", "release_runs"} {
		if string(ci[key]) != "[]" {
			t.Errorf("ci.%s = %s", key, ci[key])
		}
	}
	if string(ci["latest_release"]) != "null" || string(ci["error"]) != `"not_checked"` {
		t.Errorf("ci: %s %s", ci["latest_release"], ci["error"])
	}
}

func TestOverviewCountersFromLogsAndAlerts(t *testing.T) {
	e := newEnv(t)
	now := e.clk.now()
	e.logs["bot"] = &fakeLogs{lines: []logging.Line{
		{Time: now.Add(-10 * time.Minute).Unix(), Level: "ERROR", Msg: "recent"},
		{Time: now.Add(-3 * time.Hour).Unix(), Level: "ERROR", Msg: "old"},
		{Time: now.Add(-5 * time.Minute).Unix(), Level: "WARN", Msg: "only a warning"},
	}}
	e.logs["payments"] = &fakeLogs{err: errBoom}
	e.build()
	// Only the bot's recent error counts (and whatever core itself logged in this process); the old
	// error and the warning do not, and a service that does not answer adds nothing.
	own := 0
	for _, l := range logging.Recent() {
		if l.Level == "ERROR" && l.Time >= now.Add(-time.Hour).Unix() {
			own++
		}
	}
	if got := e.overview().Counters.Errors1h; got != own+1 {
		t.Fatalf("errors in the last hour: %d, want %d", got, own+1)
	}
}

func TestDeploymentsMergeTheDashboardAndTheServersLastUpdate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.st.WithTx(ctx, func(tx pgx.Tx) error {
		return e.st.CreateSystemUpdate(ctx, tx, &store.SystemUpdate{ID: "11111111-1111-4111-8111-111111111111", RequestedByName: "Sam <b>", From: "0.6.1", To: "0.7.0", Channel: "stable"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.st.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := e.st.EndSystemUpdate(ctx, tx, "11111111-1111-4111-8111-111111111111", "done", "updated")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// The restore point of that same update (the helper runs `bobres update`): not listed twice.
	real := time.Now()
	e.clk.t = real
	e.snap(func(s *snapshot.Snapshot) {
		s.Update = &snapshot.LastUpdate{From: "0.6.1", To: "0.7.0", At: real.Unix() + 5}
	})
	e.clk.add(time.Second)
	d := e.overview().Deployments
	if len(d) != 1 || d[0].Source != "dashboard" || d[0].Outcome != "done" || d[0].By != "Sam ‹b›" || d[0].FinishedAt == 0 || d[0].Message != "updated" {
		t.Fatalf("one update: %+v", d)
	}

	// An update run by hand on the server, earlier: only its restore point knows of it.
	byHand := real.Unix() - 3600
	e.snap(func(s *snapshot.Snapshot) {
		s.Update = &snapshot.LastUpdate{From: "0.7.0", To: "0.7.1", At: byHand}
	})
	e.clk.add(3 * time.Second)
	d = e.overview().Deployments
	if len(d) != 2 || d[0].Source != "dashboard" || d[1].Source != "cli" || d[1].To != "0.7.1" || d[1].Outcome != "unknown" || d[1].By != "" {
		t.Fatalf("two updates, newest first: %+v", d)
	}

	// Rolled back by hand.
	e.snap(func(s *snapshot.Snapshot) {
		s.Update = &snapshot.LastUpdate{From: "0.7.0", To: "0.7.1", At: byHand, RolledBackAt: byHand + 100}
	})
	e.clk.add(3 * time.Second)
	if d := e.overview().Deployments; d[1].Outcome != "rolled_back" || d[1].FinishedAt != byHand+100 {
		t.Fatalf("rolled back: %+v", d[1])
	}

	// An update that is open is "running".
	if err := e.st.WithTx(ctx, func(tx pgx.Tx) error {
		return e.st.CreateSystemUpdate(ctx, tx, &store.SystemUpdate{ID: "22222222-2222-4222-8222-222222222222", RequestedByName: "Sam", From: "0.7.1", To: "0.8.0", Channel: "beta"})
	}); err != nil {
		t.Fatal(err)
	}
	if d := e.overview().Deployments; d[0].ID != "22222222-2222-4222-8222-222222222222" || d[0].Outcome != "running" || d[0].Channel != "beta" {
		t.Fatalf("open: %+v", d[0])
	}
}
