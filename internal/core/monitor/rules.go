package monitor

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// The alert rules (docs/04-dashboard.md, "Monitoring, alert rules"). The watchdog calls
// WatchdogChecks once a minute and does the rest: a rule seen failing is pending, announced when it
// is seen failing again (and, for the rules with a hold, once it has failed that long), repeated
// after 6 hours, and announced as fixed after it has passed for 5 minutes.
//
//	monitor.disk:<mount>                  a disk past monitor.disk_pct
//	monitor.memory                        memory past monitor.mem_pct for monitor.mem_minutes
//	monitor.container.<service>.down      a stack container that is not running (not for the services
//	                                      the watchdog already checks by their health endpoints)
//	monitor.container.<service>.unhealthy a container whose Docker health check fails
//	monitor.container.<service>.restarting  monitor.restarts restarts within 15 minutes
//	monitor.collector                     no snapshot for monitor.collector_minutes
//	monitor.ci.main                       the newest finished CI run on main failed (monitor.ci_alert)
const (
	keyMemory    = "monitor.memory"
	keyCollector = "monitor.collector"
	keyCI        = "monitor.ci.main"

	// downHold is how long a container must be down before it is announced: an update restarts
	// every container, and the watchdog's own two-sighting rule only covers a minute.
	downHold = 2 * time.Minute
	// unhealthyHold is how long a failing health check must last: a container is unhealthy for a
	// while after it starts before it turns healthy.
	unhealthyHold = 3 * time.Minute
)

// watchedByHealth are the services the watchdog already checks through their health endpoints
// (service.<name>): a container rule for them would announce the same outage twice.
var watchedByHealth = map[string]bool{"core": true, "bot": true, "payments": true, "provisioner": true, "backup": true}

// WatchdogChecks implements domain.MonitorProbe.
func (m *Monitor) WatchdogChecks(ctx context.Context, _ time.Time) (domain.MonitorChecks, error) {
	// The monitor's own clock decides what is fresh (it is the watchdog's, but a test moves it).
	now := m.now()
	set := m.settings(ctx)
	if !set.Enabled {
		// Switched off: the rules are forgotten without a message (Covered, no checks).
		return domain.MonitorChecks{Covered: true}, nil
	}
	res := domain.MonitorChecks{Covered: true}

	// The CI of main, only when the owner wants it.
	if set.CIAlert {
		if failed := m.ciVerdictKnown(); failed != nil {
			c := domain.MonitorCheck{Key: keyCI, Problem: *failed, TitleKey: "alert.monitor_ci", Args: map[string]string{"workflow": "CI"}}
			if _, run := ciVerdict(m.ciStatus()); run != nil && *failed {
				c.Detail = "commit " + run.SHA
				if run.URL != "" {
					c.Detail += ", " + run.URL
				}
			}
			res.Checks = append(res.Checks, c)
		}
	}

	cv := m.collector(now)
	if !cv.Configured || !cv.Installed {
		return res, nil // there is no collector: no host rules, and nothing to say about it
	}
	// A collector that has gone quiet.
	quiet := time.Duration(set.CollectorMinutes) * time.Minute
	stale := cv.Error != "" || cv.Last == nil || now.Sub(time.Unix(cv.Last.GeneratedAt, 0)) > quiet
	res.Checks = append(res.Checks, domain.MonitorCheck{Key: keyCollector, Problem: stale, TitleKey: "alert.monitor_collector_stale",
		Args:   map[string]string{"minutes": strconv.Itoa(set.CollectorMinutes)},
		Detail: collectorWhy(cv)})
	if cv.Snap == nil {
		// Not fresh: what the rules would say is unknown, so their stored states stay as they were.
		res.Covered = false
		return res, nil
	}
	res.Checks = append(res.Checks, m.hostChecks(cv.Snap, set)...)
	res.Checks = append(res.Checks, m.containerChecks(ctx, cv.Snap, set, now)...)
	return res, nil
}

func (m *Monitor) hostChecks(s *snapshot.Snapshot, set domain.MonitorSettings) []domain.MonitorCheck {
	var out []domain.MonitorCheck
	h := s.Host
	if h == nil {
		return nil
	}
	for _, d := range h.Disks {
		pct := d.Pct()
		out = append(out, domain.MonitorCheck{Key: "monitor.disk:" + d.Mount, Problem: pct > float64(set.DiskPct), Critical: pct >= 95,
			TitleKey: "alert.monitor_disk",
			Args:     map[string]string{"mount": d.Mount, "percent": strconv.Itoa(int(pct + 0.5)), "free": strconv.FormatUint(d.Avail, 10)},
			Detail:   fmt.Sprintf("%.1f%% of %s used", pct, humanBytes(d.Used+d.Avail))})
	}
	if h.Mem.Total > 0 {
		pct := float64(h.Mem.Used()) / float64(h.Mem.Total) * 100
		out = append(out, domain.MonitorCheck{Key: keyMemory, Problem: pct > float64(set.MemPct), Critical: pct >= 98,
			TitleKey: "alert.monitor_memory", Hold: time.Duration(set.MemMinutes) * time.Minute,
			Args:   map[string]string{"percent": strconv.Itoa(int(pct + 0.5)), "minutes": strconv.Itoa(set.MemMinutes)},
			Detail: fmt.Sprintf("%.1f%% of %s in use", pct, humanBytes(h.Mem.Total))})
	}
	return out
}

func (m *Monitor) containerChecks(ctx context.Context, s *snapshot.Snapshot, set domain.MonitorSettings, now time.Time) []domain.MonitorCheck {
	if s.Docker == nil || !s.Docker.OK {
		return nil
	}
	boxes := bobresContainers(s)
	var out []domain.MonitorCheck
	for _, name := range sortedKeys(boxes) {
		c := boxes[name]
		args := map[string]string{"container": name}
		if !watchedByHealth[name] {
			down := c.State != snapshot.StateRunning && c.State != snapshot.StateRestarting // a crash loop is the restarting rule's
			out = append(out, domain.MonitorCheck{Key: "monitor.container." + name + ".down", Problem: down, Critical: true,
				TitleKey: "alert.monitor_container_down", Args: args, Hold: downHold, Detail: "state: " + c.State})
		}
		out = append(out, domain.MonitorCheck{Key: "monitor.container." + name + ".unhealthy", Problem: c.State == snapshot.StateRunning && c.Health == snapshot.HealthUnhealthy,
			TitleKey: "alert.monitor_container_unhealthy", Args: args, Hold: unhealthyHold, Detail: "its Docker health check fails"})
		n := m.recentRestarts(ctx, name, now)
		out = append(out, domain.MonitorCheck{Key: "monitor.container." + name + ".restarting", Problem: n >= set.Restarts, Critical: n >= 2*set.Restarts,
			TitleKey: "alert.monitor_container_restarting",
			Args:     map[string]string{"container": name, "count": strconv.Itoa(n), "minutes": strconv.Itoa(int(domain.MonitorRestartWindow.Minutes()))},
			Detail:   fmt.Sprintf("%d restarts in %s", n, domain.MonitorRestartWindow)})
	}
	// Services the install is meant to run that have no container at all.
	if s.Docker.Compose != nil {
		for _, name := range s.Docker.Compose.Missing {
			if _, ok := boxes[name]; ok || watchedByHealth[name] {
				continue
			}
			out = append(out, domain.MonitorCheck{Key: "monitor.container." + name + ".down", Problem: true, Critical: true,
				TitleKey: "alert.monitor_container_down", Args: map[string]string{"container": name}, Hold: downHold, Detail: "no container is running"})
		}
	}
	return out
}

// recentRestarts counts the restarts of a service within MonitorRestartWindow from the recorded
// restart counter: the rises of the counter (a drop is a container made anew, which starts it
// again, and counts nothing).
func (m *Monitor) recentRestarts(ctx context.Context, service string, now time.Time) int {
	if m.cfg.Store == nil {
		return 0
	}
	pts, err := m.cfg.Store.MonitorSeries(ctx, m.cfg.Store.Conn(), withSubject(ContainerRst, service), now.Add(-domain.MonitorRestartWindow), now)
	if err != nil {
		m.log.Warn("monitor: restart history", "service", service, "err", err)
		return 0
	}
	n := 0
	for i := 1; i < len(pts); i++ {
		if d := pts[i].V - pts[i-1].V; d > 0 {
			n += int(d)
		}
	}
	return n
}
