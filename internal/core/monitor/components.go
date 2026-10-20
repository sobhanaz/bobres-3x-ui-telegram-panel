package monitor

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/cicd"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// Component kinds and statuses.
const (
	KindService   = "service"
	KindHost      = "host"
	KindPanel     = "panel"
	KindGateway   = "gateway"
	KindDatastore = "datastore"
	KindJob       = "job"

	StatusOK      = "ok"
	StatusWarn    = "warn"
	StatusDown    = "down"
	StatusUnknown = "unknown"
)

// Overall statuses of the stack.
const (
	OverallOK       = "ok"
	OverallDegraded = "degraded"
	OverallDown     = "down"
)

// Component is one part of the stack and how it is. ID is stable (a page may translate it); Name is
// its English label. Version, UptimeS and Since are empty (null) where they are not known.
type Component struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Detail  string `json:"detail"`
	Version string `json:"version"`
	UptimeS *int64 `json:"uptime_s"`
	Since   *int64 `json:"since"`
}

// required are the components without which the store does not work: when one is down the whole
// stack is down.
var required = map[string]bool{"core": true, "bot": true, "payments": true, "provisioner": true, "postgres": true}

// serviceNames are the English labels of the services core asks (syshealth) and of the usual
// containers of the stack.
var serviceNames = map[string]string{
	"core": "Core", "bot": "Telegram bot", "payments": "Payments", "provisioner": "Provisioner", "backup": "Backup service",
	"postgres": "Postgres", "redis": "Redis", "caddy": "Caddy (web gateway)", "tls-agent": "TLS agent",
}

func nameOf(id string) string {
	if n, ok := serviceNames[id]; ok {
		return n
	}
	return id
}

func i64(v int64) *int64 { return &v }

// bobresContainers indexes the stack's containers by Compose service (the first of a service wins;
// containers of other projects are not the stack's).
func bobresContainers(s *snapshot.Snapshot) map[string]snapshot.Container {
	out := map[string]snapshot.Container{}
	if s == nil || s.Docker == nil {
		return out
	}
	for _, c := range s.Docker.Containers {
		if c.Project != snapshot.BobresProject || c.Service == "" {
			continue
		}
		if _, ok := out[c.Service]; !ok {
			out[c.Service] = c
		}
	}
	return out
}

// imageTag is the tag of an image reference ("" when it has none or is a digest).
func imageTag(image string) string {
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") && i >= 0 {
		return image[i+1:]
	}
	return ""
}

// startedAt sets Since and UptimeS from a start time (unix seconds).
func (c *Component) startedAt(at int64, now time.Time) {
	if at <= 0 || at > now.Unix() {
		return
	}
	c.Since, c.UptimeS = i64(at), i64(now.Unix()-at)
}

// containerStatus judges a container: running and not unhealthy is fine, an unhealthy or paused one
// is a warning, and one that is not running (or is restarting over and over) is down.
func containerStatus(c snapshot.Container) (status, detail string) {
	switch {
	case c.State == snapshot.StateRunning && c.Health == snapshot.HealthUnhealthy:
		return StatusWarn, "running, but its health check fails"
	case c.State == snapshot.StateRunning && c.Health == snapshot.HealthStarting:
		return StatusOK, "running, starting up"
	case c.State == snapshot.StateRunning && c.Health == snapshot.HealthHealthy:
		return StatusOK, "running, healthy"
	case c.State == snapshot.StateRunning:
		return StatusOK, "running"
	case c.State == snapshot.StatePaused:
		return StatusWarn, "paused"
	case c.State == snapshot.StateRestarting:
		return StatusDown, fmt.Sprintf("restarting (%d restarts so far)", c.Restarts)
	}
	d := c.State
	if c.ExitCode != nil {
		d = fmt.Sprintf("%s (exit code %d)", c.State, *c.ExitCode)
	}
	if c.OOMKilled {
		d += ", killed for lack of memory"
	}
	return StatusDown, d
}

// components lists every part of the stack with its state.
func (m *Monitor) components(p *probe, cv collectorView, set domain.MonitorSettings, now time.Time) []Component {
	var out []Component
	boxes := bobresContainers(cv.Snap)
	known := map[string]bool{}

	// Services core asks itself (their health endpoints).
	if p.report != nil {
		for _, sv := range p.report.Services {
			if sv.Status == syshealth.StatusNotInstalled {
				continue
			}
			known[sv.Name] = true
			c := Component{ID: sv.Name, Name: nameOf(sv.Name), Kind: KindService, Version: sv.Version}
			switch sv.Status {
			case syshealth.StatusOK:
				c.Status, c.Detail = StatusOK, fmt.Sprintf("answers in %d ms", sv.LatencyMS)
			case syshealth.StatusNotReady:
				c.Status, c.Detail = StatusWarn, "alive, but not ready: "+failedChecks(sv)
			case syshealth.StatusDown:
				c.Status, c.Detail = StatusDown, firstNonEmpty(sv.Detail, "does not answer")
			default:
				c.Status, c.Detail = StatusUnknown, "not configured"
			}
			if sv.VersionMismatch && c.Status == StatusOK {
				c.Status, c.Detail = StatusWarn, fmt.Sprintf("runs %s, core runs %s", sv.Version, p.report.Version)
			}
			if sv.Name == "core" {
				c.startedAt(m.start.Unix(), now)
			} else if box, ok := boxes[sv.Name]; ok && box.State == snapshot.StateRunning {
				c.startedAt(box.StartedAt, now)
			}
			out = append(out, c)
		}
	} else {
		c := Component{ID: "core", Name: nameOf("core"), Kind: KindService, Status: StatusOK, Detail: "this process", Version: m.cfg.Version}
		c.startedAt(m.start.Unix(), now)
		out = append(out, c)
		known["core"] = true
	}

	// The database: core asks it itself.
	pg := Component{ID: "postgres", Name: nameOf("postgres"), Kind: KindDatastore}
	switch {
	case p.dbOK:
		pg.Status, pg.Detail = StatusOK, fmt.Sprintf("answers in %d ms", p.dbLatency.Milliseconds())
		if p.counters != nil {
			pg.Detail += ", " + humanBytes(uint64(max(p.counters.DBBytes, 0))) + " of data"
		}
	case m.cfg.Store == nil:
		pg.Status, pg.Detail = StatusUnknown, "not asked"
	default:
		pg.Status, pg.Detail = StatusDown, "does not answer"
	}
	if box, ok := boxes["postgres"]; ok {
		pg.Version = imageTag(box.Image)
		if box.State == snapshot.StateRunning {
			pg.startedAt(box.StartedAt, now)
		}
	}
	known["postgres"] = true
	out = append(out, pg)

	// The rest of the stack, from the containers (only while the snapshot is fresh).
	if cv.Snap != nil && cv.Snap.Docker != nil && cv.Snap.Docker.OK {
		names := make([]string, 0, len(boxes))
		for name := range boxes {
			names = append(names, name)
		}
		if cv.Snap.Docker.Compose != nil {
			for _, name := range cv.Snap.Docker.Compose.Missing {
				if _, ok := boxes[name]; !ok {
					names = append(names, name)
				}
			}
		}
		sort.Strings(names)
		names = slices.Compact(names)
		for _, name := range names {
			if known[name] {
				continue
			}
			known[name] = true
			kind := KindService
			if name == "redis" {
				kind = KindDatastore
			}
			c := Component{ID: name, Name: nameOf(name), Kind: kind}
			if box, ok := boxes[name]; ok {
				c.Status, c.Detail = containerStatus(box)
				c.Version = imageTag(box.Image)
				if box.State == snapshot.StateRunning {
					c.startedAt(box.StartedAt, now)
				}
			} else {
				c.Status, c.Detail = StatusDown, "no container is running"
			}
			out = append(out, c)
		}
	}

	out = append(out, m.hostComponent(cv, set, now))

	// The 3x-ui servers.
	if p.servers != nil {
		for _, sv := range p.servers.Servers {
			c := Component{ID: "panel." + sv.ID, Name: sv.Name, Kind: KindPanel, Version: sv.PanelVersion}
			switch {
			case sv.CheckedAt == 0:
				c.Status, c.Detail = StatusUnknown, "not checked yet"
			case sv.Healthy && sv.Enabled:
				c.Status, c.Detail = StatusOK, fmt.Sprintf("%d clients", sv.Clients)
			case sv.Healthy:
				c.Status, c.Detail = StatusOK, "answers; takes no new services"
			default:
				c.Status, c.Detail = StatusDown, firstNonEmpty(sv.HealthError, "does not answer")
			}
			out = append(out, c)
		}
	}

	// The payment gateways that are switched on.
	for _, g := range p.gateways {
		if !g.Enabled {
			continue
		}
		c := Component{ID: "gateway." + g.Provider, Name: g.Provider, Kind: KindGateway}
		switch {
		case g.Problem != "":
			c.Status, c.Detail = StatusWarn, "settings problem: "+g.Problem
		case !g.Offered:
			c.Status, c.Detail = StatusWarn, "switched on, but not offered to customers"
		default:
			c.Status, c.Detail = StatusOK, "offered to customers"
		}
		out = append(out, c)
	}

	// Jobs: the host collector, the backups, the CI of main.
	out = append(out, m.collectorComponent(cv))
	if p.backupsOK {
		out = append(out, backupComponent(p, now))
	}
	out = append(out, m.ciComponent())
	return out
}

func failedChecks(sv syshealth.Service) string {
	var names []string
	for name, v := range sv.Checks {
		if v != "ok" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return firstNonEmpty(sv.Detail, "a dependency fails")
	}
	return strings.Join(names, ", ")
}

func firstNonEmpty(list ...string) string {
	for _, s := range list {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// hostComponent is the server itself: a warning when a disk or the memory is past its alert
// threshold, unknown while the collector says nothing.
func (m *Monitor) hostComponent(cv collectorView, set domain.MonitorSettings, now time.Time) Component {
	c := Component{ID: "host", Name: "Server", Kind: KindHost}
	h := hostOf(cv.Snap)
	if h == nil {
		c.Status, c.Detail = StatusUnknown, collectorWhy(cv)
		return c
	}
	c.Version = h.OS
	if h.UptimeS > 0 {
		c.Since, c.UptimeS = i64(now.Unix()-h.UptimeS), i64(h.UptimeS)
	}
	var problems []string
	for _, d := range h.Disks {
		if d.Pct() >= float64(set.DiskPct) {
			problems = append(problems, fmt.Sprintf("disk %s %.0f%% full", d.Mount, d.Pct()))
		}
	}
	if h.Mem.Total > 0 {
		if pct := float64(h.Mem.Used()) / float64(h.Mem.Total) * 100; pct >= float64(set.MemPct) {
			problems = append(problems, fmt.Sprintf("memory %.0f%% used", pct))
		}
	}
	if len(problems) > 0 {
		c.Status, c.Detail = StatusWarn, strings.Join(problems, "; ")
		return c
	}
	c.Status = StatusOK
	parts := []string{}
	if h.CPU.Pct != nil {
		parts = append(parts, fmt.Sprintf("cpu %.0f%%", *h.CPU.Pct))
	}
	if h.Mem.Total > 0 {
		parts = append(parts, fmt.Sprintf("memory %.0f%%", float64(h.Mem.Used())/float64(h.Mem.Total)*100))
	}
	for _, d := range h.Disks {
		parts = append(parts, fmt.Sprintf("disk %s %.0f%%", d.Mount, d.Pct()))
	}
	c.Detail = strings.Join(parts, ", ")
	return c
}

func hostOf(s *snapshot.Snapshot) *snapshot.Host {
	if s == nil {
		return nil
	}
	return s.Host
}

// collectorWhy says in a few words why there are no host figures.
func collectorWhy(cv collectorView) string {
	switch {
	case !cv.Configured:
		return "no monitor directory is mounted in core (BOBRES_MONITOR_DIR)"
	case !cv.Installed:
		return "the host collector is not installed or is switched off: sudo bobres monitor enable"
	case cv.Error != "":
		return "the collector's file cannot be read (" + cv.Error + ")"
	case !cv.Fresh:
		return fmt.Sprintf("the collector sent nothing for %s: sudo bobres monitor status", roundDur(cv.AgeS))
	}
	return "the collector's snapshot has no host section"
}

func roundDur(s int64) string {
	d := time.Duration(s) * time.Second
	switch {
	case d < 2*time.Minute:
		return fmt.Sprintf("%d seconds", s)
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}

// collectorComponent is the host collector as a job.
func (m *Monitor) collectorComponent(cv collectorView) Component {
	c := Component{ID: "collector", Name: "Host collector", Kind: KindJob}
	switch {
	case !cv.Configured || !cv.Installed:
		c.Status, c.Detail = StatusUnknown, collectorWhy(cv)
	case cv.Fresh:
		c.Status, c.Detail = StatusOK, fmt.Sprintf("last snapshot %d seconds ago", cv.AgeS)
		if cv.Snap != nil {
			c.Version = cv.Snap.Version
		}
	default:
		c.Status, c.Detail = StatusWarn, collectorWhy(cv)
	}
	return c
}

// backupComponent is the backup job: the newest backup and its age.
func backupComponent(p *probe, now time.Time) Component {
	c := Component{ID: "backups", Name: "Backups", Kind: KindJob}
	switch {
	case p.backups == nil:
		c.Status, c.Detail = StatusUnknown, "the backup service did not answer"
	case p.backups.LastCreatedAt == nil:
		// A new install has none until its first daily run: not a fault by itself (the watchdog's
		// own backup check raises it when it lasts).
		c.Status, c.Detail = StatusUnknown, "no backup has been made yet"
	default:
		age := now.Sub(*p.backups.LastCreatedAt)
		c.Status, c.Detail = StatusOK, "last backup "+ago(age)
		if age > 26*time.Hour {
			c.Status, c.Detail = StatusWarn, "the last backup is "+ago(age)+" old"
		}
		if p.backups.LastError != "" {
			c.Status = StatusWarn
		}
		c.Since = i64(p.backups.LastCreatedAt.Unix())
	}
	return c
}

func ago(d time.Duration) string {
	switch {
	case d < 0:
		return "just now"
	case d < 2*time.Minute:
		return "a minute ago"
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

// ciComponent is the CI of the main branch as a job.
func (m *Monitor) ciComponent() Component {
	c := Component{ID: "ci", Name: "CI of main", Kind: KindJob}
	st := m.ciStatus()
	if !st.Available {
		c.Status, c.Detail = StatusUnknown, ciWhy(st)
		return c
	}
	failed, run := ciVerdict(st)
	switch {
	case failed == nil:
		c.Status, c.Detail = StatusUnknown, "no finished CI run on main yet"
	case *failed:
		c.Status, c.Detail = StatusWarn, "the latest CI run on main failed"
	default:
		c.Status, c.Detail = StatusOK, "the latest CI run on main passed"
	}
	if run != nil && run.At > 0 {
		c.Since = i64(run.At)
	}
	return c
}

func ciWhy(st cicd.Status) string {
	switch st.Error {
	case cicd.ErrRateLimited:
		return "GitHub's request limit is used up for now"
	case cicd.ErrUnreachable:
		return "GitHub could not be reached"
	case cicd.ErrNotChecked:
		return "not checked yet"
	case cicd.ErrInvalid:
		return "GitHub's answer was not understood"
	}
	return "not available"
}

// ciVerdict says whether the newest finished CI run on main failed (nil: there is none), with that
// run. A cancelled or skipped run does not decide.
func ciVerdict(st cicd.Status) (failed *bool, run *cicd.Run) {
	for i := range st.Runs {
		r := &st.Runs[i]
		if r.Workflow != "CI" || r.Branch != "main" || r.Status != "completed" {
			continue
		}
		switch r.Conclusion {
		case "success":
			f := false
			return &f, r
		case "failure", "timed_out", "startup_failure":
			f := true
			return &f, r
		}
	}
	return nil, nil
}

// overall is the stack's status: down when a required component is down, degraded when anything
// else is not fine (or an alert is open at the critical level), else ok. A component whose state is
// unknown never worsens it.
func overall(comps []Component, p *probe) string {
	status := OverallOK
	for _, c := range comps {
		switch {
		case c.Status == StatusDown && required[c.ID]:
			return OverallDown
		case c.Status == StatusDown || c.Status == StatusWarn:
			status = OverallDegraded
		}
	}
	for _, a := range p.alerts {
		if a.Severity == "critical" {
			status = OverallDegraded
		}
	}
	return status
}
