package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/collect"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// Host monitoring. A root helper on the server, `bobres monitor snapshot`, reads the machine, Docker
// and systemd every 30 seconds (a systemd timer, monitor_units.go) and writes the result to
// <install>/run/monitor/snapshot.json. Core mounts that directory read-only and turns the file into
// the dashboard's Monitoring section. Core has no Docker socket and must never get one: this file is
// its only view of the machine, and it holds only what internal/monitor/snapshot allows.
//
// The safety model is the updater's (helper_request.go), the other way round. The updater is root
// reading a directory core can write, so it trusts nothing in it. Here nothing in the directory is
// core's to write (root's, mode 0755, mounted read-only), the snapshot carries no secret, and core
// reads it as untrusted text all the same (internal/monitor/snapshot). The helper checks the
// directory before it writes or reads there, without following links.

const (
	// monitorTimeout bounds one snapshot run.
	monitorTimeout = 10 * time.Second
	// maxMonitorFile bounds the previous snapshot the helper reads back (its own output).
	maxMonitorFile = snapshot.MaxFileBytes
)

// ensureMonitorDirs makes <dir>/run and <dir>/run/monitor: root's, mode 0755 (core reads the snapshot
// there as another user, through a read-only mount). Docker would make the mount's source itself,
// as root, if it were missing; this makes it first and never through a link, and puts both right on
// every install. A run that is not root cannot give them away, and leaves them as they are made.
func ensureMonitorDirs(dir string) error {
	for _, p := range []string{filepath.Join(dir, runDirName), monitorDir(dir)} {
		if fi, err := os.Lstat(p); err == nil && p != monitorDir(dir) && fi.IsDir() {
			continue // run/ is there: prepareUpdateDir put it right
		}
		d, err := openDir(p, 0o755)
		if err != nil {
			return err
		}
		if asRoot.euid() == 0 {
			if err = asRoot.chown(d, 0, 0); err != nil {
				_ = d.Close()
				return fmt.Errorf("chown %s: %w", p, err)
			}
		}
		err = d.Chmod(0o755)
		_ = d.Close()
		if err != nil {
			return fmt.Errorf("chmod %s: %w", p, err)
		}
	}
	return nil
}

// checkMonitorDir looks at the snapshot directory before the helper writes or reads there: run/ and
// run/monitor must be real directories that only this user (root, on a server) can change. Nothing
// of core's is in them, so a stranger is a sign something else wrote there.
func checkMonitorDir(dir string) error {
	me := os.Geteuid()
	for _, p := range []string{filepath.Join(dir, runDirName), monitorDir(dir)} {
		fi, err := os.Lstat(p)
		switch {
		case err != nil:
			return err
		case fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir():
			return fmt.Errorf("%s is not a directory (a link or a file in its place?)", p)
		case fi.Mode().Perm()&0o022 != 0:
			return fmt.Errorf("%s can be written by others (mode %04o)", p, fi.Mode().Perm())
		}
		if owner, ok := asRoot.owner(fi); ok && owner != me {
			return fmt.Errorf("%s belongs to uid %d", p, owner)
		}
	}
	return nil
}

// readPrevious reads back the snapshot this helper wrote last time, for the CPU shares and the
// cached `docker system df`. It trusts it as little as core does: the directory must pass
// checkMonitorDir, and the file be a regular file of this user's, that others cannot write. A
// snapshot that does not pass is not used (the figures that need it start over).
func readPrevious(dir string, now time.Time) *snapshot.Snapshot {
	if checkMonitorDir(dir) != nil {
		return nil
	}
	fi, err := os.Lstat(filepath.Join(monitorDir(dir), snapshot.FileName))
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o022 != 0 || fi.Size() > maxMonitorFile {
		return nil
	}
	if owner, ok := asRoot.owner(fi); ok && owner != os.Geteuid() {
		return nil
	}
	s, err := snapshot.Read(monitorDir(dir), now)
	if err != nil {
		return nil
	}
	return s
}

// ensureMonitor puts the collector in place with the install: its units, once the store is up.
// `bobres update` runs the new bobres's install, so every update brings the units of the release it
// installs. A problem is a warning, never a failed install: the store does not depend on the
// collector, and the next install or update tries again.
func (ins *installer) ensureMonitor(ctx context.Context, dir string, env map[string]string) {
	if ins.monitorUnits == nil {
		return
	}
	exe := ""
	if ins.exe != nil {
		var err error
		if exe, err = ins.exe(); err != nil {
			fmt.Fprintf(ins.errOut, "warning: host monitoring was not set up: cannot find the bobres binary: %v\n", err)
			return
		}
	}
	res := ins.monitorUnits.ensureMonitor(ctx, monitorOptions{dir: dir, exe: exe, off: monitorOff(env)})
	switch {
	case res.kind == markWarn:
		fmt.Fprintf(ins.errOut, "warning: host monitoring: %s\n", res.text)
	case res.kind == markSkip:
		if !ins.quiet {
			ins.prog.Note(markSkip, monitorLabel, res.text)
		}
	case res.text != "" && res.changed:
		ins.prog.Note(markOK, monitorLabel, res.text)
	}
}

// applyMonitorCollector puts BOBRES_MONITOR_COLLECTOR into effect once `bobres config set` has saved
// it: on installs the collector (and the directory it needs), off removes it. env is the install's new
// .env.
func applyMonitorCollector(o *ops, dir string, env map[string]string) int {
	off := monitorOff(env)
	if o.monitorUnits == nil {
		fmt.Fprintln(o.out, "Saved. The collector's units are set up by the next sudo bobres install or sudo bobres update.")
		return 0
	}
	exeOf := o.exe
	if exeOf == nil {
		exeOf = selfExe
	}
	exe, err := exeOf()
	if err != nil {
		fmt.Fprintf(o.errOut, "The setting is saved, but host monitoring was not set up: cannot find the bobres binary: %v\n", err)
		return 1
	}
	if err := ensureMonitorDirs(dir); err != nil {
		fmt.Fprintf(o.errOut, "The setting is saved, but host monitoring was not set up: %v\n", err)
		return 1
	}
	res := o.monitorUnits.ensureMonitor(context.Background(), monitorOptions{dir: dir, exe: exe, off: off})
	switch res.kind {
	case markWarn:
		fmt.Fprintf(o.errOut, "The setting is saved, but host monitoring was not set up: %s\n", res.text)
		return 1
	case markSkip:
		fmt.Fprintf(o.out, "The setting is saved; host monitoring is not set up here: %s.\n", res.text)
		return 0
	}
	if off {
		fmt.Fprintln(o.out, "Host monitoring is off: the dashboard's Monitoring section shows no host figures. Turn it on again with: sudo bobres monitor enable")
		return 0
	}
	fmt.Fprintln(o.out, "Host monitoring is on: "+monitorTimerUnit+" takes a snapshot every 30 seconds for the dashboard's Monitoring section. Turn it off for good: sudo bobres monitor disable")
	if b, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml")); err == nil && !strings.Contains(string(b), "/run/bobres-monitor") {
		fmt.Fprintln(o.out, "This install's docker-compose.yml is from before host monitoring: run sudo bobres update (or sudo bobres install) once, so that the dashboard can reach it.")
	}
	return 0
}

// ---- the command ----

const monitorUsage = `usage: bobres monitor status [--json] | enable | disable | snapshot [--print] | supported  [--dir D]

  status     whether the collector runs and how old its last snapshot is (exit 1 when it is not working)
  enable     put the collector in place (two systemd units) and keep it on through updates
  disable    take the collector away for good: bobres install and bobres update leave it out from now on
  snapshot   take one snapshot now (what the timer runs); --print shows it instead of writing it
  supported  exit 0: this bobres has the collector (the unit asks before it runs)`

// monitorCLI is the `bobres monitor` command, its side effects replaceable.
type monitorCLI struct {
	o   *ops
	now func() time.Time
	// collector makes the collector for the install in dir (tests give it fake sources).
	collector func(dir string, env map[string]string) *collect.Collector
}

func newMonitorCLI(o *ops) *monitorCLI {
	return &monitorCLI{o: o, now: time.Now, collector: func(dir string, env map[string]string) *collect.Collector {
		c := collect.New(dir, version.Version)
		if len(env) > 0 {
			c.Expected = allServices(dir, env)
		}
		return c
	}}
}

func monitorCmd(args []string, in io.Reader, out, errOut io.Writer) int {
	return newMonitorCLI(newOps(in, out, errOut)).run(args)
}

func (m *monitorCLI) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(m.o.errOut, monitorUsage)
		return 2
	}
	switch sub, rest := args[0], args[1:]; sub {
	case "supported":
		return 0
	case "snapshot":
		return m.snapshot(rest)
	case "status":
		return m.status(rest)
	case "enable", "disable":
		return m.toggle(sub == "enable", rest)
	case "-h", "--help", "help":
		fmt.Fprintln(m.o.out, monitorUsage)
		return 0
	}
	fmt.Fprintln(m.o.errOut, monitorUsage)
	return 2
}

// snapshot takes one snapshot and writes it to <dir>/run/monitor (or --out).
func (m *monitorCLI) snapshot(args []string) int {
	o := m.o
	flags := flag.NewFlagSet("monitor snapshot", flag.ContinueOnError)
	flags.SetOutput(o.errOut)
	dir := flags.String("dir", defaultDir(), dirUsage)
	outDir := flags.String("out", "", "the directory to write snapshot.json in (default: <dir>/run/monitor)")
	show := flags.Bool("print", false, "print the snapshot instead of writing it")
	timeout := flags.Duration("timeout", monitorTimeout, "the longest a run may take")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 || *timeout <= 0 {
		return 2
	}
	target := *outDir
	if target == "" {
		target = monitorDir(*dir)
	}
	env, _, _ := readEnv(filepath.Join(*dir, ".env")) // root's file: without it the expected services are not listed
	c := m.collector(*dir, env)
	if *outDir == "" && !*show {
		// The helper writes into the install's directory only after looking at it.
		if err := ensureMonitorDirs(*dir); err != nil {
			fmt.Fprintf(o.errOut, "error: %v\n", err)
			return 1
		}
		if err := checkMonitorDir(*dir); err != nil {
			fmt.Fprintf(o.errOut, "error: not writing the snapshot: %v\n", err)
			return 1
		}
		c.Previous = readPrevious(*dir, m.now())
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	s := c.Collect(ctx)
	if *show {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(o.out, string(b))
		return 0
	}
	if err := snapshot.Write(target, s); err != nil {
		fmt.Fprintf(o.errOut, "error: %v\n", err)
		return 1
	}
	// The timer runs this every 30 seconds and the journal keeps what it prints: a person at a
	// terminal gets a line, the timer gets silence.
	if detectUI(o.out, os.Getenv).tty {
		fmt.Fprintf(o.out, "Snapshot written to %s in %d ms", filepath.Join(target, snapshot.FileName), s.DurationMS)
		if len(s.Errors) > 0 {
			fmt.Fprintf(o.out, " (not read: %s)", strings.Join(s.Errors, "; "))
		}
		fmt.Fprintln(o.out)
	}
	return 0
}

// toggle turns the collector on (enable) or off (disable) for good: the choice is
// BOBRES_MONITOR_COLLECTOR in .env (the same as bobres config set), which the install and update
// steps follow, and the units are put in place or taken away now.
func (m *monitorCLI) toggle(on bool, args []string) int {
	name, want := "disable", "off"
	if on {
		name, want = "enable", "on"
	}
	flags := flag.NewFlagSet("monitor "+name, flag.ContinueOnError)
	flags.SetOutput(m.o.errOut)
	dir := flags.String("dir", defaultDir(), dirUsage)
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		return 2
	}
	env, ok := m.o.requireInstall(*dir)
	if !ok {
		return 2
	}
	// Already as asked in .env: the units are still made to match (they may be missing, or someone
	// stopped the timer).
	if cur := strings.ToLower(strings.TrimSpace(env[monitorCollectorKey])); cur == want || (on && cur == "") {
		return applyMonitorCollector(m.o, *dir, env)
	}
	return m.o.configSet(*dir, monitorCollectorKey, want, false)
}

// monitorStatus is `bobres monitor status --json`.
type monitorStatus struct {
	Enabled      bool     `json:"enabled"`
	UnitsInPlace bool     `json:"units_installed"`
	TimerActive  bool     `json:"timer_active"`
	Snapshot     bool     `json:"snapshot"`
	AgeS         int64    `json:"age_s"`
	Fresh        bool     `json:"fresh"`
	Docker       string   `json:"docker,omitempty"`
	Containers   int      `json:"containers"`
	Errors       []string `json:"errors,omitempty"`
	Problem      string   `json:"problem,omitempty"`
}

func (m *monitorCLI) status(args []string) int {
	o := m.o
	flags := flag.NewFlagSet("monitor status", flag.ContinueOnError)
	flags.SetOutput(o.errOut)
	dir := flags.String("dir", defaultDir(), dirUsage)
	asJSON := flags.Bool("json", false, "print the status as JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		return 2
	}
	env, ok, _ := readEnv(filepath.Join(*dir, ".env"))
	if !ok {
		fmt.Fprintf(o.errOut, "error: no install found in %s (run `bobres install`, or pass --dir)\n", *dir)
		return 2
	}
	st := monitorStatus{Enabled: !monitorOff(env)}
	units := o.monitorUnits.monitorState(context.Background())
	if units != nil {
		st.UnitsInPlace, st.TimerActive = units.Installed, units.Active
	}
	now := m.now()
	snap, err := snapshot.Read(monitorDir(*dir), now)
	switch {
	case err == nil:
		st.Snapshot, st.AgeS, st.Fresh = true, int64(snap.Age(now).Seconds()), snap.Fresh(now)
		st.Errors = snap.Errors
		if snap.Docker != nil {
			st.Docker, st.Containers = snap.Docker.Version, len(snap.Docker.Containers)
		}
	case !errors.Is(err, fs.ErrNotExist):
		st.Problem = "the snapshot cannot be read: " + firstLine(err.Error())
	}
	res := monitorVerdict(monitorFacts{off: !st.Enabled, units: units, systemdKnown: units != nil, snap: snap, snapErr: err, now: now})
	if *asJSON {
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Fprintln(o.out, string(b))
	} else {
		fmt.Fprintf(o.out, "%-5s %s\n", res.Status, res.Detail)
		if units != nil && units.LastResult != "" && units.LastResult != "success" {
			fmt.Fprintf(o.out, "      the last run of %s ended with: %s (journalctl -u %s)\n", monitorServiceUnit, units.LastResult, monitorServiceUnit)
		}
	}
	if res.Status == Pass || res.Status == Skip {
		return 0
	}
	return 1
}

// ---- the check and doctor line ----

// monitorFacts is what the "Host monitor" line of bobres check and doctor judges.
type monitorFacts struct {
	off          bool
	units        *monitorUnitState
	systemdKnown bool
	snap         *snapshot.Snapshot
	snapErr      error
	now          time.Time
}

// monitorVerdict says in one line whether the collector is installed and fresh. It is never a FAIL:
// host monitoring is optional.
func monitorVerdict(f monitorFacts) Result {
	switch {
	case f.off:
		return Result{monitorLabel, Skip, "turned off (" + monitorCollectorKey + "=off); sudo bobres monitor enable turns it on"}
	case f.snapErr == nil:
		age := f.snap.Age(f.now)
		docker := ""
		if f.snap.Docker != nil && f.snap.Docker.OK {
			docker = fmt.Sprintf(", docker %s with %d containers", orDash(f.snap.Docker.Version), len(f.snap.Docker.Containers))
		}
		if !f.snap.Fresh(f.now) {
			return Result{monitorLabel, Warn, fmt.Sprintf("the last snapshot is %s old: the timer may have stopped (systemctl status %s)", roundAge(age), monitorTimerUnit)}
		}
		if len(f.snap.Errors) > 0 {
			return Result{monitorLabel, Warn, fmt.Sprintf("fresh (%s old) but not everything could be read: %s", roundAge(age), strings.Join(f.snap.Errors, "; "))}
		}
		return Result{monitorLabel, Pass, fmt.Sprintf("a snapshot %s old%s", roundAge(age), docker)}
	case !errors.Is(f.snapErr, fs.ErrNotExist):
		if errors.Is(f.snapErr, fs.ErrPermission) {
			return Result{monitorLabel, Skip, "cannot read the snapshot (run it as root)"}
		}
		return Result{monitorLabel, Warn, "the snapshot cannot be read: " + firstLine(f.snapErr.Error())}
	}
	// No snapshot at all.
	switch {
	case !f.systemdKnown:
		return Result{monitorLabel, Skip, "no snapshot, and this machine does not run systemd: host monitoring is off here"}
	case f.units != nil && f.units.Installed && f.units.Active:
		return Result{monitorLabel, Warn, "installed, but no snapshot yet (it appears within a minute): journalctl -u " + monitorServiceUnit}
	case f.units != nil && f.units.Installed:
		return Result{monitorLabel, Warn, "installed but the timer is not running: sudo systemctl enable --now " + monitorTimerUnit}
	}
	return Result{monitorLabel, Warn, "not installed: the dashboard's Monitoring section shows no host figures; sudo bobres monitor enable puts it in place"}
}

// roundAge writes a duration the way a status line wants it.
func roundAge(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// monitorResult is the "Host monitor" line for the install in dir (units nil: only the snapshot is
// looked at).
func monitorResult(ctx context.Context, units *systemUnits, dir string, env map[string]string, now time.Time) Result {
	snap, err := snapshot.Read(monitorDir(dir), now)
	st := units.monitorState(ctx)
	return monitorVerdict(monitorFacts{off: monitorOff(env), units: st, systemdKnown: st != nil, snap: snap, snapErr: err, now: now})
}
