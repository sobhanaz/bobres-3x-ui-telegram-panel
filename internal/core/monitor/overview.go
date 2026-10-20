package monitor

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/cicd"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// Overview is GET /api/v1/monitor/overview (docs/04-dashboard.md has every field). Lists are always
// lists, never null; a figure that is not known is null.
type Overview struct {
	GeneratedAt int64       `json:"generated_at"`
	Status      string      `json:"status"`
	Components  []Component `json:"components"`
	Host        HostView    `json:"host"`
	Containers  []Box       `json:"containers"`
	Kubernetes  KubeView    `json:"kubernetes"`
	Counters    Counters    `json:"counters"`
	CI          CIView      `json:"ci"`
	Collector   CollectorV  `json:"collector"`
	Deployments []Deploy    `json:"deployments"`
}

// HostView is the server. While the collector's snapshot is missing or stale Available is false,
// Reason says why, and nothing else is filled.
type HostView struct {
	Available bool        `json:"available"`
	Reason    string      `json:"reason,omitempty"`
	Hostname  string      `json:"hostname,omitempty"`
	CPUPct    *float64    `json:"cpu_pct"`
	Load      [3]float64  `json:"load"`
	Cores     int         `json:"cores"`
	Mem       UsedTotal   `json:"mem"`
	Swap      UsedTotal   `json:"swap"`
	Disks     []DiskView  `json:"disks"`
	Net       NetView     `json:"net"`
	UptimeS   int64       `json:"uptime_s"`
	OS        string      `json:"os"`
	Kernel    string      `json:"kernel"`
	Docker    DockerView  `json:"docker"`
	Clock     *ClockView  `json:"clock"`
	DockerDF  *DockerDisk `json:"docker_disk"`
}

// UsedTotal is bytes in use of a total.
type UsedTotal struct {
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

// DiskView is a filesystem: used and total as the filesystem counts them, pct as df shows it.
type DiskView struct {
	Mount string   `json:"mount"`
	Used  uint64   `json:"used"`
	Total uint64   `json:"total"`
	Avail uint64   `json:"avail"`
	Pct   float64  `json:"pct"`
	Roles []string `json:"roles"`
}

// NetView is the host's traffic in bytes per second (null until two snapshots are known).
type NetView struct {
	RxBps *float64 `json:"rx_bps"`
	TxBps *float64 `json:"tx_bps"`
}

// DockerView is the container engine.
type DockerView struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	API     string `json:"api"`
	Error   string `json:"error"`
}

// ClockView says whether the host's clock is kept right (null: not known).
type ClockView struct {
	Synchronized *bool `json:"synchronized"`
	NTP          *bool `json:"ntp"`
}

// DockerDisk is what Docker keeps on the disk, in bytes, as of CollectedAt (unix seconds).
type DockerDisk struct {
	Images      uint64 `json:"images"`
	Reclaimable uint64 `json:"images_reclaimable"`
	Containers  uint64 `json:"containers"`
	Volumes     uint64 `json:"volumes"`
	BuildCache  uint64 `json:"build_cache"`
	CollectedAt int64  `json:"collected_at"`
}

// Box is a container.
type Box struct {
	Name       string   `json:"name"`
	Project    string   `json:"project"`
	Service    string   `json:"service"`
	Image      string   `json:"image"`
	State      string   `json:"state"`
	Health     string   `json:"health"`
	Restarts   int      `json:"restarts"`
	StartedAt  int64    `json:"started_at"`
	CPUPct     *float64 `json:"cpu_pct"`
	MemBytes   uint64   `json:"mem_bytes"`
	MemLimit   uint64   `json:"mem_limit"`
	NetRxBytes uint64   `json:"net_rx_bytes"`
	NetTxBytes uint64   `json:"net_tx_bytes"`
	OOMKilled  bool     `json:"oom_killed"`
	ExitCode   *int     `json:"exit_code"`
}

// KubeView is the optional Kubernetes section.
type KubeView struct {
	Detected  bool       `json:"detected"`
	Reason    string     `json:"reason"`
	Nodes     []KubeNode `json:"nodes"`
	Pods      []KubePod  `json:"pods"`
	Truncated bool       `json:"truncated"`
}

// KubeNode is a node.
type KubeNode struct {
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Roles   string `json:"roles"`
	Version string `json:"version"`
}

// KubePod is a pod.
type KubePod struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Phase     string `json:"phase"`
	Ready     bool   `json:"ready"`
	Restarts  int    `json:"restarts"`
	Node      string `json:"node"`
}

// Counters are the store's own figures. A figure core could not read is null.
type Counters struct {
	OpenAlerts      *int   `json:"open_alerts"`
	Errors1h        int    `json:"errors_1h"`
	OrdersPending   *int64 `json:"orders_pending"`
	PaymentsPending *int   `json:"payments_pending"`
	ProvisionQueue  *int64 `json:"provision_queue"`
	OutboxBacklog   *int64 `json:"outbox_backlog"`
	TicketsOpen     *int64 `json:"tickets_open"`
	ServicesActive  *int64 `json:"services_active"`
	CustomersTotal  *int64 `json:"customers_total"`
}

// CIView is the CI/CD panel.
type CIView struct {
	Available     bool          `json:"available"`
	CheckedAt     int64         `json:"checked_at"`
	Error         string        `json:"error"`
	Detail        string        `json:"detail"`
	Repo          string        `json:"repo"`
	Runs          []cicd.Run    `json:"runs"`
	ReleaseRuns   []cicd.Run    `json:"release_runs"`
	LatestRelease *cicd.Release `json:"latest_release"`
	RateResetAt   int64         `json:"rate_reset_at"`
	MainFailing   *bool         `json:"main_failing"`
}

// CollectorV is the host collector.
type CollectorV struct {
	Installed bool `json:"installed"`
	Fresh     bool `json:"fresh"`
	AgeS      int  `json:"age_s"`
	// Error is why the file cannot be read ("" when it can).
	Error      string   `json:"error"`
	Version    string   `json:"version"`
	GeneratedS int64    `json:"generated_at"`
	IntervalS  int      `json:"interval_s"`
	DurationMS int64    `json:"duration_ms"`
	Notes      []string `json:"notes"`
}

// Deploy is one update of the install: asked for in the dashboard, or run by hand on the server.
type Deploy struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	To         string `json:"to"`
	Channel    string `json:"channel"`
	At         int64  `json:"at"`
	FinishedAt int64  `json:"finished_at"`
	// Outcome is done, failed, rolled_back, not_started, or "running" while it is open; "unknown" for
	// an update run by hand (the server keeps only its versions and time); "rolled_back" once
	// `bobres rollback` has undone it.
	Outcome string `json:"outcome"`
	// Source is "dashboard" or "cli".
	Source  string `json:"source"`
	By      string `json:"by"`
	Message string `json:"message"`
}

// maxDeploys is how many updates the overview lists.
const maxDeploys = 10

// Overview builds the page.
func (m *Monitor) Overview(ctx context.Context) *Overview {
	now := m.now()
	set := m.settings(ctx)
	p := m.probe(ctx)
	cv := m.collector(now)
	comps := m.components(p, cv, set, now)
	o := &Overview{GeneratedAt: now.Unix(), Components: comps, Status: overall(comps, p)}
	o.Host = hostView(cv)
	o.Containers = boxViews(cv.Snap)
	o.Kubernetes = kubeView(cv.Snap)
	o.Counters = m.counters(ctx, p, now)
	o.CI = m.ciView()
	o.Collector = collectorView2(cv)
	o.Deployments = m.deployments(ctx, cv)
	return o
}

func hostView(cv collectorView) HostView {
	v := HostView{Disks: []DiskView{}, Docker: DockerView{}}
	h := hostOf(cv.Snap)
	if h == nil {
		v.Reason = collectorWhy(cv)
		return v
	}
	v.Available, v.Hostname = true, h.Hostname
	v.CPUPct, v.Load, v.Cores = h.CPU.Pct, [3]float64(h.Load), h.Cores
	v.Mem = UsedTotal{Used: h.Mem.Used(), Total: h.Mem.Total}
	v.Swap = UsedTotal{Used: h.Swap.Used, Total: h.Swap.Total}
	for _, d := range h.Disks {
		roles := d.Roles
		if roles == nil {
			roles = []string{}
		}
		v.Disks = append(v.Disks, DiskView{Mount: d.Mount, Used: d.Used, Total: d.Total, Avail: d.Avail, Pct: round1(d.Pct()), Roles: roles})
	}
	v.Net = NetView{RxBps: cv.NetRx, TxBps: cv.NetTx}
	v.UptimeS, v.OS, v.Kernel = h.UptimeS, h.OS, h.Kernel
	if h.Clock != nil {
		v.Clock = &ClockView{Synchronized: h.Clock.Synchronized, NTP: h.Clock.NTP}
	}
	if d := cv.Snap.Docker; d != nil {
		v.Docker = DockerView{OK: d.OK, Version: d.Version, API: d.API, Error: d.Error}
		if d.DF != nil && d.DF.CollectedAt > 0 {
			v.DockerDF = &DockerDisk{Images: d.DF.ImagesBytes, Reclaimable: d.DF.ImagesReclaimable, Containers: d.DF.ContainersBytes,
				Volumes: d.DF.VolumesBytes, BuildCache: d.DF.BuildCacheBytes, CollectedAt: d.DF.CollectedAt}
		}
	}
	return v
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func boxViews(s *snapshot.Snapshot) []Box {
	out := []Box{}
	if s == nil || s.Docker == nil {
		return out
	}
	for _, c := range s.Docker.Containers {
		out = append(out, Box{Name: c.Name, Project: c.Project, Service: c.Service, Image: c.Image, State: c.State, Health: c.Health,
			Restarts: c.Restarts, StartedAt: c.StartedAt, CPUPct: c.CPUPct, MemBytes: c.MemBytes, MemLimit: c.MemLimit,
			NetRxBytes: c.NetRx, NetTxBytes: c.NetTx, OOMKilled: c.OOMKilled, ExitCode: c.ExitCode})
	}
	return out
}

func kubeView(s *snapshot.Snapshot) KubeView {
	v := KubeView{Nodes: []KubeNode{}, Pods: []KubePod{}}
	if s == nil || s.Kubernetes == nil {
		v.Reason = "not detected"
		return v
	}
	k := s.Kubernetes
	v.Detected, v.Reason, v.Truncated = k.Detected, k.Reason, k.Truncated
	for _, n := range k.Nodes {
		v.Nodes = append(v.Nodes, KubeNode{Name: n.Name, Ready: n.Ready, Roles: n.Roles, Version: n.Version})
	}
	for _, p := range k.Pods {
		v.Pods = append(v.Pods, KubePod{Namespace: p.Namespace, Name: p.Name, Phase: p.Phase, Ready: p.Ready, Restarts: p.Restarts, Node: p.Node})
	}
	return v
}

func (m *Monitor) counters(ctx context.Context, p *probe, now time.Time) Counters {
	var c Counters
	if p.alertsOK {
		n := len(p.alerts)
		c.OpenAlerts = &n
	}
	if p.counters != nil {
		pc := p.counters
		c.OrdersPending, c.ProvisionQueue, c.OutboxBacklog = &pc.OrdersPending, &pc.ProvisionQueue, &pc.OutboxBacklog
		c.TicketsOpen, c.ServicesActive, c.CustomersTotal = &pc.TicketsOpen, &pc.ServicesActive, &pc.CustomersTotal
	}
	if p.pending >= 0 {
		n := p.pending
		c.PaymentsPending = &n
	}
	c.Errors1h = m.recentLogs(ctx).errorsSince(now.Add(-time.Hour))
	return c
}

func (m *Monitor) ciView() CIView {
	st := m.ciStatus()
	v := CIView{Available: st.Available, CheckedAt: st.CheckedAt, Error: st.Error, Detail: st.Detail, Repo: st.Repo,
		Runs: st.Runs, ReleaseRuns: st.ReleaseRuns, RateResetAt: st.RateResetAt}
	if len(st.Releases) > 0 {
		r := st.Releases[0]
		v.LatestRelease = &r
	}
	if failed, _ := ciVerdict(st); failed != nil {
		v.MainFailing = failed
	}
	return v
}

func collectorView2(cv collectorView) CollectorV {
	v := CollectorV{Installed: cv.Installed, Fresh: cv.Fresh, AgeS: int(cv.AgeS), Error: cv.Error, Notes: []string{}}
	if cv.Last != nil {
		v.Version, v.GeneratedS, v.IntervalS, v.DurationMS = cv.Last.Version, cv.Last.GeneratedAt, cv.Last.IntervalS, cv.Last.DurationMS
		v.Notes = append(v.Notes, cv.Last.Errors...)
	}
	return v
}

// deployments lists the updates of the install, newest first: those asked for in the dashboard (the
// store keeps each), and the newest one the server's restore point knows of when it is not one of
// those (an update run by hand with `bobres update`).
func (m *Monitor) deployments(ctx context.Context, cv collectorView) []Deploy {
	out := []Deploy{}
	if m.cfg.Store != nil {
		rows, err := m.cfg.Store.ListSystemUpdates(ctx, m.cfg.Store.Conn(), maxDeploys)
		if err != nil {
			m.log.Warn("monitor: update history", "err", err)
		}
		for _, u := range rows {
			d := Deploy{ID: u.ID, From: u.From, To: u.To, Channel: u.Channel, At: u.RequestedAt.Unix(), Outcome: u.Outcome, Source: "dashboard",
				By: snapshot.Clean(u.RequestedByName, 80), Message: snapshot.Clean(u.Message, 300)}
			if u.Outcome == "" {
				d.Outcome = "running"
			}
			if u.OutcomeAt != nil {
				d.FinishedAt = u.OutcomeAt.Unix()
			}
			out = append(out, d)
		}
	}
	if cv.Last != nil && cv.Last.Update != nil {
		u := cv.Last.Update
		covered := false
		for _, d := range out {
			// The dashboard's update leaves the same restore point: the same target, started within
			// the hour (the helper begins right after the request).
			if d.To == u.To && abs64(d.At-u.At) <= 3600 {
				covered = true
			}
		}
		if !covered {
			d := Deploy{ID: "cli-" + strconv.FormatInt(u.At, 10), From: u.From, To: u.To, At: u.At, Outcome: "unknown", Source: "cli"}
			if u.RolledBackAt > 0 {
				d.Outcome, d.FinishedAt = "rolled_back", u.RolledBackAt
			}
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	if len(out) > maxDeploys {
		out = out[:maxDeploys]
	}
	return out
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
