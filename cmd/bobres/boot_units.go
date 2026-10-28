package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
)

// The boot unit: bobres.service brings the store up at every boot (deploy/systemd/bobres.service).
// Docker's restart policy, unless-stopped, does not start a container that Docker has marked
// "manually stopped", and a power-off can leave that mark on one container: on 2026-10-28 the gateway
// (Caddy) stayed down for hours after a reboot, with the dashboard, the Mini App and the payment
// pages unreachable while the bot went on answering. The unit runs `bobres start` once the engine is
// up. It is managed like the dashboard updater's and the collector's units (helper_units.go,
// monitor_units.go), through the same systemUnits, so that a test changes nothing on the machine it
// runs on: written from the template in deploy/systemd only when its text differs, enabled, and taken
// away for an install that switched it off.
//
// A store stopped on purpose must stay stopped: `bobres stop` and a plain uninstall leave
// <install>/run/stopped, which the unit's ConditionPathExists reads, and every command that starts
// the store takes it away again.
const (
	bootServiceUnit = "bobres.service"
	// bootKey is the .env setting that keeps the unit off: "off", or on when unset.
	bootKey = "BOBRES_START_AT_BOOT"
	// bootLabel names the unit in the lines install, doctor and check print.
	bootLabel = "Start at boot"
	// stoppedName is the file in run/ that says the store was stopped on purpose.
	stoppedName = "stopped"
)

// bootOff reports whether the owner switched the boot unit off in .env.
func bootOff(env map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(env[bootKey]), "off")
}

// bootOptions are what the unit is made for.
type bootOptions struct {
	dir string // the install directory
	exe string // the bobres binary the service runs
	off bool   // the owner switched the unit off
}

// renderBootUnit fills the template in for an install.
func renderBootUnit(o bootOptions) (string, error) {
	for _, p := range []string{o.dir, o.exe} {
		if !unitSafePath(p) {
			return "", fmt.Errorf("the path %q cannot go into a systemd unit (an absolute path of letters, digits and . _ + - / only)", p)
		}
	}
	return strings.NewReplacer("@DIR@", o.dir, "@EXE@", o.exe).Replace(string(deploy.BootServiceUnit)), nil
}

// ensureBoot makes the unit what this bobres wants: written (only when its text differs) and
// enabled; or, for an install that switched it off, removed. It does not start it: the store is up
// when this runs (install runs it after the start), and the unit is for the next boot. It is safe to
// run again and again. A problem is a warning in the result: the store does not depend on the unit.
func (s *systemUnits) ensureBoot(ctx context.Context, o bootOptions) unitResult {
	if why := s.unavailable(); why != "" {
		return unitResult{kind: markSkip, text: "not set up: " + why}
	}
	if o.off {
		return s.removeBoot(ctx, "off, as "+bootKey+" says")
	}
	text, err := renderBootUnit(o)
	if err != nil {
		return unitWarn("not set up: %v", err)
	}
	changed := false
	p := filepath.Join(s.unitDir, bootServiceUnit)
	if cur, err := s.readFile(p); err != nil || string(cur) != text {
		if err := s.writeFile(p, []byte(text), 0o644); err != nil {
			return unitWarn("not set up: %v", err)
		}
		changed = true
	}
	if changed {
		if err := s.systemctl(ctx, "daemon-reload"); err != nil {
			return unitWarn("not set up: %v", err)
		}
	}
	if changed || s.stateOf(ctx, "is-enabled", bootServiceUnit) != "enabled" {
		if err := s.systemctl(ctx, "enable", bootServiceUnit); err != nil {
			return unitWarn("not set up: %v", err)
		}
	}
	if en := s.stateOf(ctx, "is-enabled", bootServiceUnit); en != "enabled" {
		return unitWarn("installed, but %s is %s (systemctl status %s)", bootServiceUnit, orDash(en), bootServiceUnit)
	}
	if changed {
		return unitResult{kind: markOK, text: "the store starts by itself after every boot (" + bootServiceUnit + " is on)", changed: true}
	}
	return unitResult{kind: markOK}
}

// removeBoot disables the unit and removes its file. A run that is starting the store at this
// moment is left to finish. why is what is said.
func (s *systemUnits) removeBoot(ctx context.Context, why string) unitResult {
	p := filepath.Join(s.unitDir, bootServiceUnit)
	if !s.exists(p) {
		return unitResult{kind: markOK, text: why}
	}
	_ = s.systemctl(ctx, "disable", bootServiceUnit) // not loaded, not enabled: nothing to undo
	if err := s.remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return unitWarn("%s was not removed: %v", bootServiceUnit, err)
	}
	if err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return unitWarn("the unit was removed, but %v", err)
	}
	_ = s.systemctl(ctx, "reset-failed", bootServiceUnit)
	return unitResult{kind: markOK, text: why + " (the unit is removed)", changed: true}
}

// bootExecPath is the program the installed unit runs ("" when its file is not readable or has no
// ExecStart).
func (s *systemUnits) bootExecPath() string {
	b, err := s.readFile(filepath.Join(s.unitDir, bootServiceUnit))
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

// bootReport is the line bobres doctor and bobres check show for the unit of the install in dir.
// It is never a FAIL: the unit is a safety net, and the store does not depend on it.
func (s *systemUnits) bootReport(ctx context.Context, dir string, off bool) Result {
	if s.goos != "linux" || !s.exists(s.runDir) {
		return Result{bootLabel, Skip, "needs systemd (a Linux server)"}
	}
	installed := s.exists(filepath.Join(s.unitDir, bootServiceUnit))
	const fix = " (sudo bobres update, or sudo bobres install, sets it up again)"
	switch {
	case off && !installed:
		return Result{bootLabel, Pass, "off, as " + bootKey + " says: after a reboot only Docker's restart policy brings the services back"}
	case off:
		return Result{bootLabel, Warn, bootKey + " is off but the unit is still installed (sudo bobres config set " + bootKey + " off removes it)"}
	case !installed:
		return Result{bootLabel, Warn, "not installed: after a reboot a container that Docker has marked stopped stays down, and the dashboard can be unreachable until someone notices. Update once with sudo bobres update (it installs the unit)"}
	}
	en, act := s.stateOf(ctx, "is-enabled", bootServiceUnit), s.stateOf(ctx, "is-active", bootServiceUnit)
	switch {
	case en != "enabled":
		return Result{bootLabel, Warn, bootServiceUnit + " is " + orDash(en) + ", not enabled" + fix}
	case act == "failed":
		return Result{bootLabel, Warn, bootServiceUnit + " failed when it last started the store: journalctl -u " + bootServiceUnit}
	}
	if exe := s.bootExecPath(); exe == "" || !s.exists(exe) {
		return Result{bootLabel, Warn, "the unit runs " + orDash(exe) + ", which is not there" + fix}
	}
	if s.exists(stoppedPath(dir)) {
		return Result{bootLabel, Pass, "on, and the store was stopped on purpose (sudo bobres stop): it stays stopped after a reboot until sudo bobres start"}
	}
	return Result{bootLabel, Pass, "on: " + bootServiceUnit + " starts the store at every boot"}
}

// stoppedPath is the file that says the store of the install in dir was stopped on purpose.
func stoppedPath(dir string) string { return filepath.Join(dir, runDirName, stoppedName) }

// markStopped records that the store was stopped on purpose, so that the boot unit leaves it
// stopped. run/ is root's, like the file.
func markStopped(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, runDirName), 0o755); err != nil { //nolint:gosec // dir is the install directory the operator named; run/ is root's, 0755 like everywhere else
		return err
	}
	return writeAtomic(stoppedPath(dir), []byte("The store was stopped on purpose (sudo bobres stop, or a plain uninstall).\n"+
		"It stays stopped after a reboot until: sudo bobres start\n"), 0o644)
}

// clearStopped takes the record away: the store is being started.
func clearStopped(dir string) error {
	if err := os.Remove(stoppedPath(dir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// noteStopped records, after a stop that was asked for, that the store stays stopped after a
// reboot. A failure is a note: the stop itself worked.
func (o *ops) noteStopped(dir string) {
	if err := markStopped(dir); err != nil {
		fmt.Fprintf(o.errOut, "note: could not record that the store was stopped on purpose (%v): a server with the boot unit may start it again at its next boot.\n", err)
	}
}

// noteStarting takes the record away before the store is started. A failure is a warning: until it
// is fixed the boot unit leaves the store stopped after a reboot.
func (o *ops) noteStarting(dir string) {
	if err := clearStopped(dir); err != nil {
		fmt.Fprintf(o.errOut, "warning: could not remove %s (%v): until it is removed the boot unit leaves the store stopped after a reboot.\n", stoppedPath(dir), err)
	}
}

// ensureBoot puts the boot unit in place with the install, once the store is up. `bobres update`
// runs the new bobres's install, so every update brings the unit of the release it installs. A
// problem is a warning, never a failed install: the store does not depend on the unit, and the next
// install or update tries again.
func (ins *installer) ensureBoot(ctx context.Context, dir string, env map[string]string) {
	if ins.bootUnits == nil {
		return
	}
	exe := ""
	if ins.exe != nil {
		var err error
		if exe, err = ins.exe(); err != nil {
			fmt.Fprintf(ins.errOut, "warning: starting at boot was not set up: cannot find the bobres binary: %v\n", err)
			return
		}
	}
	res := ins.bootUnits.ensureBoot(ctx, bootOptions{dir: dir, exe: exe, off: bootOff(env)})
	switch {
	case res.kind == markWarn:
		fmt.Fprintf(ins.errOut, "warning: starting at boot: %s\n", res.text)
	case res.kind == markSkip:
		if !ins.quiet {
			ins.prog.Note(markSkip, bootLabel, res.text)
		}
	case res.text != "" && res.changed:
		ins.prog.Note(markOK, bootLabel, res.text)
	}
}

// applyStartAtBoot puts BOBRES_START_AT_BOOT into effect once `bobres config set` has saved it: on
// installs the unit, off removes it. env is the install's new .env.
func applyStartAtBoot(o *ops, dir string, env map[string]string) int {
	off := bootOff(env)
	if o.bootUnits == nil {
		fmt.Fprintln(o.out, "Saved. The unit is set up by the next sudo bobres install or sudo bobres update.")
		return 0
	}
	exeOf := o.exe
	if exeOf == nil {
		exeOf = selfExe
	}
	exe, err := exeOf()
	if err != nil {
		fmt.Fprintf(o.errOut, "The setting is saved, but starting at boot was not set up: cannot find the bobres binary: %v\n", err)
		return 1
	}
	res := o.bootUnits.ensureBoot(context.Background(), bootOptions{dir: dir, exe: exe, off: off})
	switch res.kind {
	case markWarn:
		fmt.Fprintf(o.errOut, "The setting is saved, but starting at boot was not set up: %s\n", res.text)
		return 1
	case markSkip:
		fmt.Fprintf(o.out, "The setting is saved; starting at boot is not set up here: %s.\n", res.text)
		return 0
	}
	if off {
		fmt.Fprintln(o.out, "The store no longer starts by itself after a reboot: only Docker's restart policy brings the services back. Turn it on again with: sudo bobres config set "+bootKey+" on")
		return 0
	}
	fmt.Fprintln(o.out, "The store starts by itself after every boot: "+bootServiceUnit+" is on.")
	return 0
}
