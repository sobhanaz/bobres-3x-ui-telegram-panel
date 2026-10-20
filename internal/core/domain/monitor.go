package domain

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Monitoring (docs/04-dashboard.md, "Monitoring"). Core samples the host and the stack every 30
// seconds (internal/core/monitor) and raises its alerts through the watchdog below, so they are
// debounced, resolved, sent to staff in Telegram and listed on the System page like every other
// alert. The settings are declared with the others (SettingSpecs, group "monitor").

// The monitoring settings.
const (
	SettingMonitorEnabled          = "monitor.enabled"
	SettingMonitorDiskPct          = "monitor.disk_pct"
	SettingMonitorMemPct           = "monitor.mem_pct"
	SettingMonitorMemMinutes       = "monitor.mem_minutes"
	SettingMonitorRestarts         = "monitor.restarts"
	SettingMonitorCollectorMinutes = "monitor.collector_minutes"
	SettingMonitorCIAlert          = "monitor.ci_alert"
)

// MonitorRestartWindow is the time within which MonitorSettings.Restarts restarts of one
// container are a crash loop.
const MonitorRestartWindow = 15 * time.Minute

// MonitorSettings are the monitoring settings with their defaults and limits applied (the specs
// are the source of both): a value that is missing, is not a number or is out of range is the
// default.
type MonitorSettings struct {
	Enabled          bool
	DiskPct          int
	MemPct           int
	MemMinutes       int
	Restarts         int
	CollectorMinutes int
	CIAlert          bool
}

// MonitorSettingsFrom reads the settings from the stored values (Service.Settings).
func MonitorSettingsFrom(values map[string]string) MonitorSettings {
	flag := func(key string) bool {
		sp, _ := SpecOf(key)
		v := strings.TrimSpace(values[key])
		if v == "" {
			v = sp.Default
		}
		return v == "true"
	}
	num := func(key string) int {
		sp, _ := SpecOf(key)
		def, _ := strconv.Atoi(sp.Default)
		n, err := strconv.ParseInt(strings.TrimSpace(values[key]), 10, 64)
		if err != nil || n < sp.Min || n > sp.Max {
			return def
		}
		return int(n)
	}
	return MonitorSettings{
		Enabled: flag(SettingMonitorEnabled), DiskPct: num(SettingMonitorDiskPct), MemPct: num(SettingMonitorMemPct),
		MemMinutes: num(SettingMonitorMemMinutes), Restarts: num(SettingMonitorRestarts),
		CollectorMinutes: num(SettingMonitorCollectorMinutes), CIAlert: flag(SettingMonitorCIAlert),
	}
}

// MonitorSettings reads them.
func (s *Service) MonitorSettings(ctx context.Context) (MonitorSettings, error) {
	values, err := s.Settings(ctx)
	if err != nil {
		return MonitorSettings{}, err
	}
	return MonitorSettingsFrom(values), nil
}

// MonitorCheck is one monitoring rule's result in a watchdog run. Its Key starts with "monitor."
// (a rule whose key does not is dropped); TitleKey is the bot's text (alert.monitor_*) and Args its
// values (the same conventions as the other alerts, see events.SystemAlertEvent).
type MonitorCheck struct {
	Key      string
	Problem  bool
	Critical bool
	TitleKey string
	Args     map[string]string
	Detail   string
	// Hold is how long the rule must fail before it is announced (memory above its percent "for 10
	// minutes", a container down for longer than an update takes).
	Hold time.Duration
}

// MonitorChecks are the rules' results. Covered is false when the data they need was not
// available (the collector's snapshot is stale or missing): the states stored for the rules are
// then left as they were, not cleared on no evidence.
type MonitorChecks struct {
	Checks  []MonitorCheck
	Covered bool
}

// MonitorProbe evaluates the monitoring rules for the watchdog (internal/core/monitor).
type MonitorProbe interface {
	WatchdogChecks(ctx context.Context, now time.Time) (MonitorChecks, error)
}

// monitorScope is the prefix of every monitoring alert's key: switching monitoring off, or removing
// the collector, forgets them under it without a message.
const monitorScope = "monitor."

// monitor: the monitoring rules (disk, memory, containers, the collector, the CI of main).
func (w *Watchdog) monitor(ctx context.Context, now time.Time) alertProbe {
	p := alertProbe{scope: monitorScope}
	if w.src.Monitor == nil {
		p.covered = true
		return p
	}
	res, err := w.src.Monitor.WatchdogChecks(ctx, now)
	if err != nil {
		w.log.Warn("watchdog: monitoring", "err", err)
		return p
	}
	p.covered = res.Covered
	for _, c := range res.Checks {
		if !strings.HasPrefix(c.Key, monitorScope) || len(c.Key) > 200 || c.TitleKey == "" {
			continue
		}
		sev := events.SeverityWarning
		if c.Critical {
			sev = events.SeverityCritical
		}
		p.checks = append(p.checks, alertCheck{key: c.Key, problem: c.Problem, severity: sev, title: c.TitleKey, args: c.Args,
			detail: c.Detail, hold: c.Hold})
	}
	return p
}
