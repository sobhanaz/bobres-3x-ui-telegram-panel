package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// The host collector's systemd units: bobres-monitor.timer starts bobres-monitor.service every 30
// seconds, which runs `bobres monitor snapshot` as root and writes <install>/run/monitor/snapshot.json
// (monitor.go). They are managed like the updater's (helper_units.go), through the same systemUnits,
// so a test changes nothing on the machine it runs on: written from the templates in deploy/systemd
// only when their text differs, enabled and started, and taken away for an install that switched
// the collector off.
const (
	monitorServiceUnit = "bobres-monitor.service"
	monitorTimerUnit   = "bobres-monitor.timer"
	// monitorCollectorKey is the .env setting that keeps the collector off: "off", or on when unset.
	monitorCollectorKey = "BOBRES_MONITOR_COLLECTOR"
	// monitorLabel names the collector in the lines install, doctor and check print.
	monitorLabel = "Host monitor"
	// monitorDirName is the snapshot directory under run/.
	monitorDirName = "monitor"
)

// monitorDir is where the snapshot is written, inside the install: the one host directory core reads
// as the host's account of itself, read-only (deploy/docker-compose.yml).
func monitorDir(dir string) string { return filepath.Join(dir, runDirName, monitorDirName) }

// monitorOff reports whether the owner switched the collector off in .env.
func monitorOff(env map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(env[monitorCollectorKey]), "off")
}

// monitorOptions are what the collector's units are made for.
type monitorOptions struct {
	dir string // the install directory
	exe string // the bobres binary the service runs
	off bool   // the owner switched the collector off
}

// renderMonitorUnits fills the templates in for an install.
func renderMonitorUnits(o monitorOptions) (serviceUnit, timerUnit string, err error) {
	for _, p := range []string{o.dir, o.exe} {
		if !unitSafePath(p) {
			return "", "", fmt.Errorf("the path %q cannot go into a systemd unit (an absolute path of letters, digits and . _ + - / only)", p)
		}
	}
	r := strings.NewReplacer("@DIR@", o.dir, "@EXE@", o.exe)
	return r.Replace(string(deploy.MonitorServiceUnit)), r.Replace(string(deploy.MonitorTimerUnit)), nil
}

// stateOf is the first word of what systemctl is-enabled or is-active says of a unit.
func (s *systemUnits) stateOf(ctx context.Context, verb, unit string) string {
	out, _ := s.run(ctx, "systemctl", verb, unit)
	return strings.TrimSpace(firstLine(string(out)))
}

// ensureMonitor makes the collector's units what this bobres wants: written (only when their text
// differs), enabled and running, a first snapshot asked for when anything was started; or, for an
// install that switched the collector off, removed. It is safe to run again and again. A problem
// is a warning in the result: the store does not depend on the collector.
func (s *systemUnits) ensureMonitor(ctx context.Context, o monitorOptions) unitResult {
	if why := s.unavailable(); why != "" {
		return unitResult{kind: markSkip, text: "not set up: " + why}
	}
	if o.off {
		return s.removeMonitor(ctx, o.dir, "off, as "+monitorCollectorKey+" says")
	}
	serviceText, timerText, err := renderMonitorUnits(o)
	if err != nil {
		return unitWarn("not set up: %v", err)
	}
	changed := false
	// The service first: the timer names it.
	for _, f := range []struct{ name, text string }{{monitorServiceUnit, serviceText}, {monitorTimerUnit, timerText}} {
		p := filepath.Join(s.unitDir, f.name)
		if cur, err := s.readFile(p); err == nil && string(cur) == f.text {
			continue
		}
		if err := s.writeFile(p, []byte(f.text), 0o644); err != nil {
			return unitWarn("not set up: %v", err)
		}
		changed = true
	}
	if changed {
		if err := s.systemctl(ctx, "daemon-reload"); err != nil {
			return unitWarn("not set up: %v", err)
		}
	}
	active := s.stateOf(ctx, "is-active", monitorTimerUnit)
	if active == "failed" {
		_ = s.systemctl(ctx, "reset-failed", monitorTimerUnit, monitorServiceUnit)
	}
	enabled := s.stateOf(ctx, "is-enabled", monitorTimerUnit)
	if changed || enabled != "enabled" || active != "active" {
		if err := s.systemctl(ctx, "enable", monitorTimerUnit); err != nil {
			return unitWarn("not set up: %v", err)
		}
		verb := "start"
		if changed && active == "active" {
			verb = "restart" // a running timer keeps the schedule it started with
		}
		if err := s.systemctl(ctx, verb, monitorTimerUnit); err != nil {
			return unitWarn("installed, but %v", err)
		}
		// The first snapshot now, so that the dashboard has figures at once; it does not wait for it.
		_ = s.systemctl(ctx, "start", "--no-block", monitorServiceUnit)
	}
	if en, act := s.stateOf(ctx, "is-enabled", monitorTimerUnit), s.stateOf(ctx, "is-active", monitorTimerUnit); en != "enabled" || act != "active" {
		return unitWarn("installed, but %s is %s and %s (systemctl status %s)", monitorTimerUnit, orDash(en), orDash(act), monitorTimerUnit)
	}
	if changed {
		return unitResult{kind: markOK, text: "the dashboard's Monitoring section reads this server (" + monitorTimerUnit + " is on)", changed: true}
	}
	return unitResult{kind: markOK}
}

// removeMonitor stops the timer, removes the units and the snapshot. A service that is running a
// snapshot is left to finish. why is what is said.
func (s *systemUnits) removeMonitor(ctx context.Context, dir, why string) unitResult {
	if err := snapshot.Remove(monitorDir(dir)); err != nil {
		return unitWarn("the snapshot was not removed: %v", err)
	}
	var present []string
	for _, n := range []string{monitorTimerUnit, monitorServiceUnit} {
		if s.exists(filepath.Join(s.unitDir, n)) {
			present = append(present, n)
		}
	}
	if len(present) == 0 {
		return unitResult{kind: markOK, text: why}
	}
	if s.exists(filepath.Join(s.unitDir, monitorTimerUnit)) {
		_ = s.systemctl(ctx, "disable", "--now", monitorTimerUnit) // not loaded, not enabled: nothing to undo
	}
	for _, n := range present {
		if err := s.remove(filepath.Join(s.unitDir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return unitWarn("%s was not removed: %v", n, err)
		}
	}
	if err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return unitWarn("the units were removed, but %v", err)
	}
	_ = s.systemctl(ctx, "reset-failed", monitorTimerUnit, monitorServiceUnit)
	return unitResult{kind: markOK, text: why + " (the units are removed)", changed: true}
}

// uninstallMonitor is removeMonitor for an uninstall: silent when there is nothing to remove, and
// nothing when the units cannot be managed from here.
func (s *systemUnits) uninstallMonitor(ctx context.Context, dir string) unitResult {
	if s.unavailable() != "" {
		return unitResult{kind: markSkip}
	}
	res := s.removeMonitor(ctx, dir, "host monitoring is off")
	if !res.changed && res.kind == markOK {
		res.text = ""
	}
	return res
}

// monitorUnitState is what systemd says about the collector.
type monitorUnitState struct {
	Installed bool // both unit files are there
	Enabled   bool
	Active    bool
	// LastResult is the service's result of its last run ("success", "exit-code"...), "" unknown.
	LastResult string
}

// monitorState asks systemd about the collector; nil when this is no systemd server (nothing can be
// said).
func (s *systemUnits) monitorState(ctx context.Context) *monitorUnitState {
	if s == nil || s.goos != "linux" || !s.exists(s.runDir) {
		return nil
	}
	st := &monitorUnitState{
		Installed: s.exists(filepath.Join(s.unitDir, monitorTimerUnit)) && s.exists(filepath.Join(s.unitDir, monitorServiceUnit)),
	}
	if !st.Installed {
		return st
	}
	st.Enabled = s.stateOf(ctx, "is-enabled", monitorTimerUnit) == "enabled"
	st.Active = s.stateOf(ctx, "is-active", monitorTimerUnit) == "active"
	if out, err := s.run(ctx, "systemctl", "show", "-p", "Result", "--value", monitorServiceUnit); err == nil {
		st.LastResult = strings.TrimSpace(firstLine(string(out)))
	}
	return st
}
