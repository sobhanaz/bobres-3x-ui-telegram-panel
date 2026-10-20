package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
)

// The helper's systemd units: bobres-updater.path watches run/update/request.json and starts
// bobres-updater.service, which runs `bobres update --from-request` as root. Both come from the
// templates in deploy/systemd, embedded in this binary; `bobres install` (so every `bobres update`,
// which runs the new bobres's install) writes them, enables the path unit and starts it, and says
// "installed" in status.json. Everything it does to the system goes through systemUnits, so that
// tests change nothing on the machine they run on.
const (
	updaterPathUnit    = "bobres-updater.path"
	updaterServiceUnit = "bobres-updater.service"
	systemUnitDir      = "/etc/systemd/system"
	// systemdRunDir exists when systemd is the init system that is running.
	systemdRunDir = "/run/systemd/system"
	// dashboardUpdatesKey is the .env setting that keeps the helper off: "off", or on when unset.
	dashboardUpdatesKey = "BOBRES_UPDATE_FROM_DASHBOARD"
	// dashboardUpdatesLabel names the helper in the lines install, doctor and check print.
	dashboardUpdatesLabel = "Dashboard updates"
)

// dashboardUpdatesOff reports whether the owner switched the helper off in .env.
func dashboardUpdatesOff(env map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(env[dashboardUpdatesKey]), "off")
}

// systemUnits installs, removes and looks at the helper's units.
type systemUnits struct {
	// run runs a system program and returns its combined output.
	run       func(ctx context.Context, name string, args ...string) ([]byte, error)
	readFile  func(path string) ([]byte, error)
	writeFile func(path string, data []byte, mode os.FileMode) error
	remove    func(path string) error
	exists    func(path string) bool
	lookPath  func(file string) (string, error)
	euid      func() int
	goos      string
	// unitDir is where local units live; runDir exists while systemd runs.
	unitDir string
	runDir  string
}

func newSystemUnits() *systemUnits {
	return &systemUnits{
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // systemctl, arguments built here
			cmd.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=")
			cmd.WaitDelay = 2 * time.Second
			return cmd.CombinedOutput()
		},
		readFile: os.ReadFile, writeFile: writeAtomic, remove: os.Remove, exists: exists,
		lookPath: exec.LookPath, euid: os.Geteuid, goos: runtime.GOOS,
		unitDir: systemUnitDir, runDir: systemdRunDir,
	}
}

// unitOptions are what the units are made for.
type unitOptions struct {
	dir   string // the install directory
	exe   string // the bobres binary the service runs
	state string // the release state directory (install.sh's)
	off   bool   // the owner switched the helper off
}

// unitResult is how ensuring or removing the units went. text is "" when there is nothing to say
// (the units were in place already).
type unitResult struct {
	kind    markKind
	text    string
	changed bool
}

// unitSafePath reports whether p can be written into a unit file as it is: an absolute, clean path
// of plain characters (no space, quote, % or $ for systemd to read as something else).
func unitSafePath(p string) bool {
	if p == "" || p == "/" || len(p) > 200 || !path.IsAbs(p) || path.Clean(p) != p {
		return false
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '/', r == '.', r == '_', r == '-', r == '+':
		default:
			return false
		}
	}
	return true
}

// renderUnits fills the templates in for an install.
func renderUnits(o unitOptions) (pathUnit, serviceUnit string, err error) {
	for _, p := range []string{o.dir, o.exe, o.state} {
		if !unitSafePath(p) {
			return "", "", fmt.Errorf("the path %q cannot go into a systemd unit (an absolute path of letters, digits and . _ + - / only)", p)
		}
	}
	r := strings.NewReplacer("@DIR@", o.dir, "@EXE@", o.exe, "@EXEDIR@", filepath.Dir(o.exe), "@STATE@", o.state)
	return r.Replace(string(deploy.UpdaterPathUnit)), r.Replace(string(deploy.UpdaterServiceUnit)), nil
}

// unavailable says why units cannot be managed here ("" when they can).
func (s *systemUnits) unavailable() string {
	switch {
	case s.goos != "linux":
		return "it needs systemd, so it is for Linux servers"
	case s.euid() != 0:
		return "installing it changes the system: run bobres as root (sudo bobres install, or sudo bobres update)"
	case !s.exists(s.runDir):
		return "systemd is not running on this server"
	}
	if _, err := s.lookPath("systemctl"); err != nil {
		return "systemctl was not found"
	}
	return ""
}

// systemctl runs one systemctl command; its output is in the error when it fails.
func (s *systemUnits) systemctl(ctx context.Context, args ...string) error {
	out, err := s.run(ctx, "systemctl", args...)
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, firstLine(string(out)))
	}
	return nil
}

// state is the first word of what systemctl is-enabled or is-active says of the path unit.
func (s *systemUnits) state(ctx context.Context, verb string) string {
	out, _ := s.run(ctx, "systemctl", verb, updaterPathUnit)
	return strings.TrimSpace(firstLine(string(out)))
}

func unitWarn(format string, args ...any) unitResult {
	return unitResult{kind: markWarn, text: fmt.Sprintf(format, args...)}
}

// ensure makes the helper's units what this bobres wants: written (only when their text differs),
// enabled and running; or, for an install that switched the helper off, removed. It is safe to
// run again and again. A problem is a warning in the result: the store does not depend on the helper.
func (s *systemUnits) ensure(ctx context.Context, o unitOptions) unitResult {
	if why := s.unavailable(); why != "" {
		return unitResult{kind: markSkip, text: "not set up: " + why}
	}
	if o.off {
		return s.removeAll(ctx, "off, as BOBRES_UPDATE_FROM_DASHBOARD says: only sudo bobres update updates this server")
	}
	pathText, serviceText, err := renderUnits(o)
	if err != nil {
		return unitWarn("not set up: %v", err)
	}
	changed := false
	// The service first: the path unit names it.
	for _, f := range []struct{ name, text string }{{updaterServiceUnit, serviceText}, {updaterPathUnit, pathText}} {
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
	if err := s.systemctl(ctx, "enable", updaterPathUnit); err != nil {
		return unitWarn("not set up: %v", err)
	}
	active := s.state(ctx, "is-active")
	if active == "failed" {
		// A start limit that was hit stays until it is reset.
		_ = s.systemctl(ctx, "reset-failed", updaterPathUnit, updaterServiceUnit)
	}
	if changed || active != "active" {
		if err := s.systemctl(ctx, "restart", updaterPathUnit); err != nil {
			return unitWarn("installed, but %v", err)
		}
	}
	if en, act := s.state(ctx, "is-enabled"), s.state(ctx, "is-active"); en != "enabled" || act != "active" {
		return unitWarn("installed, but %s is %s and %s (systemctl status %s)", updaterPathUnit, orDash(en), orDash(act), updaterPathUnit)
	}
	if changed {
		return unitResult{kind: markOK, text: "the dashboard can update this server (" + updaterPathUnit + " is on)", changed: true}
	}
	return unitResult{kind: markOK}
}

// removeAll stops watching for requests and removes the units. A service that is running an update
// is left to finish. why is what is said.
func (s *systemUnits) removeAll(ctx context.Context, why string) unitResult {
	var present []string
	for _, n := range []string{updaterPathUnit, updaterServiceUnit} {
		if s.exists(filepath.Join(s.unitDir, n)) {
			present = append(present, n)
		}
	}
	if len(present) == 0 {
		return unitResult{kind: markOK, text: why}
	}
	if s.exists(filepath.Join(s.unitDir, updaterPathUnit)) {
		_ = s.systemctl(ctx, "disable", "--now", updaterPathUnit) // not loaded, not enabled: nothing to undo
	}
	for _, n := range present {
		if err := s.remove(filepath.Join(s.unitDir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return unitWarn("%s was not removed: %v", n, err)
		}
	}
	if err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return unitWarn("the units were removed, but %v", err)
	}
	_ = s.systemctl(ctx, "reset-failed", updaterPathUnit, updaterServiceUnit)
	return unitResult{kind: markOK, text: why + " (the units are removed)", changed: true}
}

// uninstall is removeAll for an uninstall: silent when there is nothing to remove, and nothing
// when the units cannot be managed from here.
func (s *systemUnits) uninstall(ctx context.Context) unitResult {
	if s.unavailable() != "" {
		return unitResult{kind: markSkip}
	}
	res := s.removeAll(ctx, "the dashboard can no longer update this server")
	if !res.changed && res.kind == markOK {
		res.text = ""
	}
	return res
}

// execPath is the program the installed service runs ("" when its file is not readable or has no
// ExecStart).
func (s *systemUnits) execPath() string {
	b, err := s.readFile(filepath.Join(s.unitDir, updaterServiceUnit))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok {
			f := strings.Fields(strings.TrimLeft(v, "-@:+!"))
			if len(f) > 0 {
				return f[0]
			}
		}
	}
	return ""
}

// report is the line bobres doctor and bobres check show for the helper. It never fails: the
// helper is optional, and the store does not depend on it.
func (s *systemUnits) report(ctx context.Context, off bool) Result {
	if s.goos != "linux" || !s.exists(s.runDir) {
		return Result{dashboardUpdatesLabel, Skip, "needs systemd (a Linux server)"}
	}
	installed := s.exists(filepath.Join(s.unitDir, updaterPathUnit)) && s.exists(filepath.Join(s.unitDir, updaterServiceUnit))
	const fix = " (sudo bobres update, or sudo bobres install, sets it up again)"
	switch {
	case off && !installed:
		return Result{dashboardUpdatesLabel, Pass, "off, as " + dashboardUpdatesKey + " says: only sudo bobres update updates this server"}
	case off:
		return Result{dashboardUpdatesLabel, Warn, dashboardUpdatesKey + " is off but the units are still installed (sudo bobres config set " + dashboardUpdatesKey + " off removes them)"}
	case !installed:
		return Result{dashboardUpdatesLabel, Warn, "not installed: the dashboard cannot update this server. Update once with sudo bobres update (it installs the helper)"}
	}
	en, act := s.state(ctx, "is-enabled"), s.state(ctx, "is-active")
	switch {
	case en != "enabled":
		return Result{dashboardUpdatesLabel, Warn, updaterPathUnit + " is " + orDash(en) + ", not enabled" + fix}
	case act != "active":
		return Result{dashboardUpdatesLabel, Warn, updaterPathUnit + " is " + orDash(act) + " (systemctl status " + updaterPathUnit + ")" + fix}
	}
	if exe := s.execPath(); exe == "" || !s.exists(exe) {
		return Result{dashboardUpdatesLabel, Warn, "the service runs " + orDash(exe) + ", which is not there" + fix}
	}
	return Result{dashboardUpdatesLabel, Pass, "on: " + updaterPathUnit + " is enabled and waiting for the dashboard's requests (it runs " + s.execPath() + ")"}
}
