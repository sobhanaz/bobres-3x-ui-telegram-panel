package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/collect"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// Host monitoring. A root helper on the server, `bobres monitor snapshot`,
// reads the machine, Docker and systemd every 30 seconds (a systemd timer) and
// writes the result to <install>/run/monitor/snapshot.json; core mounts that
// directory read-only and turns the file into the dashboard's Monitoring
// section. Core has no Docker socket and must never get one: this file is its
// only view of the machine, and it holds only what internal/monitor/snapshot
// allows. `bobres install` and `bobres update` put the two systemd units in
// place; `bobres monitor disable` takes them away for good.

// The units and the places they live in.
const (
	monitorService = "bobres-monitor.service"
	monitorTimer   = "bobres-monitor.timer"
	systemdUnitDir = "/etc/systemd/system"
	// monitorCollectorKey in .env: "off" turns the collector off for good (the
	// install and update steps then take its units away instead of putting them
	// in place); anything else, or nothing, is on.
	monitorCollectorKey = "BOBRES_MONITOR_COLLECTOR"
	// monitorTimeout bounds one snapshot run.
	monitorTimeout = 10 * time.Second
)

// monitorDir is where the snapshot is written, inside the install.
func monitorDir(dir string) string { return filepath.Join(dir, "run", "monitor") }

// monitorOff: the install's .env turns the collector off.
func monitorOff(env map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(env[monitorCollectorKey]), "off")
}

// ensureMonitorDirs makes <dir>/run and <dir>/run/monitor (root's, mode 0755:
// core reads the snapshot there as another user, through a read-only mount).
// Docker would make the mount's source itself, as root, if it were missing;
// this makes it first and never through a link.
func ensureMonitorDirs(dir string) error {
	if err := mkdirBelow(dir, "run/monitor", 0o755); err != nil {
		return err
	}
	for _, rel := range []string{"run", "run/monitor"} {
		d, err := openDir(filepath.Join(dir, rel), 0o755)
		if err != nil {
			return err
		}
		err = d.Chmod(0o755)
		_ = d.Close()
		if err != nil {
			return fmt.Errorf("chmod %s: %w", filepath.Join(dir, rel), err)
		}
	}
	return nil
}

// unitArg writes a value for a systemd unit's command line (ExecStart): plain
// text as it is, anything else in double quotes with the characters systemd
// reads specially (\ " % $) escaped. A value with a control character is
// refused.
func unitArg(s string) (string, error) {
	if s == "" {
		return `""`, nil
	}
	plain := true
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			return "", fmt.Errorf("%q has a control character a systemd unit cannot take", s)
		case r == ' ' || r == '\\' || r == '"' || r == '\'' || r == '%' || r == '$' || r == ';':
			plain = false
		}
	}
	if plain {
		return s, nil
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`, nil
}

// unitPath writes an absolute path for a unit setting that takes a list of
// paths (ReadWritePaths, ConditionPathIsDirectory): spaces are the separator,
// so they are written \x20, and % is doubled.
func unitPath(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%q is not an absolute path", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%q has a control character a systemd unit cannot take", p)
		}
	}
	return strings.NewReplacer("%", "%%", " ", `\x20`).Replace(p), nil
}

// renderMonitorService is the unit that takes one snapshot. exe is the bobres
// binary (the one that runs now: bobres update swaps it in place, so the path
// stays), dir the install. It runs as root (the Docker socket and the journal
// are root's) with limits: it may write only the snapshot directory, gets a
// slice of the processor and a ceiling on memory, and is skipped quietly when
// the install is gone or when the binary is an older bobres that does not know
// `monitor` (a rollback): ExecCondition turns that into a skip, not a failure.
func renderMonitorService(exe, dir string) (string, error) {
	qexe, err := unitArg(exe)
	if err != nil {
		return "", err
	}
	qdir, err := unitArg(dir)
	if err != nil {
		return "", err
	}
	run, err := unitPath(monitorDir(dir))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(`# Written by "bobres install" (and kept in place by "bobres update"). Do not edit: it is rewritten.
# Takes a snapshot of this server for the dashboard's Monitoring section: docs/06-installer.md, "Host monitoring".
# Turn it off for good with: sudo bobres monitor disable

[Unit]
Description=BOBRES host monitoring snapshot
Documentation=https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/06-installer.md
After=docker.service
`)
	fmt.Fprintf(&b, "ConditionPathIsDirectory=%s\n", run)
	b.WriteString(`
[Service]
Type=oneshot
User=root
`)
	fmt.Fprintf(&b, "ExecCondition=%s monitor supported\n", qexe)
	fmt.Fprintf(&b, "ExecStart=%s monitor snapshot --dir %s\n", qexe, qdir)
	b.WriteString(`TimeoutStartSec=30
Nice=10
IOSchedulingClass=idle
CPUQuota=25%
MemoryMax=192M
TasksMax=64
UMask=0022
CacheDirectory=bobres-monitor
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
`)
	fmt.Fprintf(&b, "ReadWritePaths=%s\n", run)
	b.WriteString(`ProtectHome=read-only
ProtectControlGroups=yes
ProtectKernelModules=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
LockPersonality=yes
SystemCallArchitectures=native
`)
	return b.String(), nil
}

// monitorTimerUnit runs the service 20 seconds after boot and every 30 seconds
// after it last started.
const monitorTimerUnit = `# Written by "bobres install" (and kept in place by "bobres update"). Do not edit: it is rewritten.
# Runs bobres-monitor.service, the host snapshot for the dashboard's Monitoring section.

[Unit]
Description=Run the BOBRES host monitoring snapshot every 30 seconds
Documentation=https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/06-installer.md

[Timer]
OnBootSec=20s
OnUnitActiveSec=30s
AccuracySec=1s

[Install]
WantedBy=timers.target
`

// monitorHost holds the side effects of putting the collector in place,
// replaceable in tests: it changes the machine only through these.
type monitorHost struct {
	// run runs a system program (systemctl) and returns its combined output.
	run       func(ctx context.Context, name string, args ...string) ([]byte, error)
	readFile  func(path string) ([]byte, error)
	writeFile func(path string, data []byte, mode os.FileMode) error
	remove    func(path string) error
	exists    func(path string) bool
	euid      func() int
	// systemd says whether the machine runs systemd as its init.
	systemd func() bool
	// exe is the bobres binary that runs (symbolic links resolved).
	exe     func() (string, error)
	unitDir string
}

func newMonitorHost() *monitorHost {
	return &monitorHost{
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // systemctl, arguments built here
			cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "SYSTEMD_COLORS=0"}
			cmd.WaitDelay = 2 * time.Second
			return cmd.CombinedOutput()
		},
		readFile: os.ReadFile, writeFile: writeAtomic, remove: os.Remove, exists: exists, euid: os.Geteuid,
		systemd: func() bool { fi, err := os.Stat("/run/systemd/system"); return err == nil && fi.IsDir() },
		exe: func() (string, error) {
			p, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(p)
		},
		unitDir: systemdUnitDir,
	}
}

// monitorOutcome is how putting the collector in place went.
type monitorOutcome struct {
	// State: installed (new), updated (units rewritten), unchanged, off (the
	// install turns it off), removed (the units were taken away), skipped (not
	// possible here: Why says why).
	State string
	Why   string
	Err   error
}

// ensure makes the install ready for host monitoring and puts the collector in
// place: the snapshot directory always (the compose file mounts it into core),
// then the two systemd units when the machine runs systemd, bobres runs as
// root and the install does not turn the collector off. It is idempotent: a run
// that finds everything as it should be changes nothing and starts nothing.
func (h *monitorHost) ensure(ctx context.Context, dir string, env map[string]string) monitorOutcome {
	if err := ensureMonitorDirs(dir); err != nil {
		return monitorOutcome{Err: err}
	}
	if monitorOff(env) {
		removed, err := h.removeUnits(ctx, dir)
		switch {
		case err != nil:
			return monitorOutcome{State: "off", Err: err}
		case removed:
			return monitorOutcome{State: "removed", Why: monitorCollectorKey + "=off"}
		}
		return monitorOutcome{State: "off", Why: monitorCollectorKey + "=off"}
	}
	if h.euid() != 0 {
		return monitorOutcome{State: "skipped", Why: "it needs root (run: sudo bobres monitor enable)"}
	}
	if !h.systemd() {
		return monitorOutcome{State: "skipped", Why: "this machine does not run systemd"}
	}
	exe, err := h.exe()
	if err != nil {
		return monitorOutcome{Err: fmt.Errorf("find the bobres binary: %w", err)}
	}
	service, err := renderMonitorService(exe, dir)
	if err != nil {
		return monitorOutcome{Err: err}
	}
	changed, fresh := false, true
	for _, u := range []struct{ name, text string }{{monitorService, service}, {monitorTimer, monitorTimerUnit}} {
		path := filepath.Join(h.unitDir, u.name)
		old, err := h.readFile(path) //nolint:gosec // a unit file of ours
		if err == nil {
			fresh = false
			if bytes.Equal(old, []byte(u.text)) {
				continue
			}
		}
		if err := h.writeFile(path, []byte(u.text), 0o644); err != nil {
			return monitorOutcome{Err: err}
		}
		changed = true
	}
	if changed {
		if out, err := h.run(ctx, "systemctl", "daemon-reload"); err != nil {
			return monitorOutcome{Err: fmt.Errorf("systemctl daemon-reload: %v: %s", err, firstLine(string(out)))}
		}
	}
	enabled := h.systemctlOK(ctx, "is-enabled", monitorTimer)
	active := h.systemctlOK(ctx, "is-active", monitorTimer)
	if changed || !enabled || !active {
		if out, err := h.run(ctx, "systemctl", "enable", "--now", monitorTimer); err != nil {
			return monitorOutcome{Err: fmt.Errorf("systemctl enable --now %s: %v: %s", monitorTimer, err, firstLine(string(out)))}
		}
		if changed && active {
			// The running timer keeps the schedule it started with.
			if out, err := h.run(ctx, "systemctl", "restart", monitorTimer); err != nil {
				return monitorOutcome{Err: fmt.Errorf("systemctl restart %s: %v: %s", monitorTimer, err, firstLine(string(out)))}
			}
		}
		// The first snapshot now, so the dashboard has figures at once.
		_, _ = h.run(ctx, "systemctl", "start", "--no-block", monitorService)
	}
	switch {
	case fresh:
		return monitorOutcome{State: "installed"}
	case changed:
		return monitorOutcome{State: "updated"}
	}
	return monitorOutcome{State: "unchanged"}
}

// systemctlOK runs a systemctl query whose exit status is the answer.
func (h *monitorHost) systemctlOK(ctx context.Context, args ...string) bool {
	_, err := h.run(ctx, "systemctl", args...)
	return err == nil
}

// removeUnits stops and disables the timer, deletes the two unit files and the
// snapshot, and tells systemd. It reports whether there was anything to
// remove. Run without root or without systemd there is nothing of ours to
// find, and nothing is touched.
func (h *monitorHost) removeUnits(ctx context.Context, dir string) (bool, error) {
	removed := false
	if err := snapshot.Remove(monitorDir(dir)); err != nil {
		return false, fmt.Errorf("remove the snapshot: %w", err)
	}
	if h.euid() != 0 || !h.systemd() {
		return false, nil
	}
	present := false
	for _, u := range []string{monitorTimer, monitorService} {
		present = present || h.exists(filepath.Join(h.unitDir, u))
	}
	if !present {
		return false, nil
	}
	_, _ = h.run(ctx, "systemctl", "disable", "--now", monitorTimer)
	_, _ = h.run(ctx, "systemctl", "stop", monitorService)
	for _, u := range []string{monitorTimer, monitorService} {
		path := filepath.Join(h.unitDir, u)
		if h.exists(path) {
			if err := h.remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return removed, fmt.Errorf("remove %s: %w", path, err)
			}
			removed = true
		}
	}
	if out, err := h.run(ctx, "systemctl", "daemon-reload"); err != nil {
		return removed, fmt.Errorf("systemctl daemon-reload: %v: %s", err, firstLine(string(out)))
	}
	return removed, nil
}

// monitorUnitState is what systemd says about the collector.
type monitorUnitState struct {
	Installed bool // the unit files are there
	Enabled   bool
	Active    bool
	// LastResult is the service's result of its last run ("success", "exit-code"...), "" unknown.
	LastResult string
}

// unitState asks systemd (nil: nothing can be said).
func (h *monitorHost) unitState(ctx context.Context) *monitorUnitState {
	if h == nil || !h.systemd() {
		return nil
	}
	st := &monitorUnitState{}
	st.Installed = h.exists(filepath.Join(h.unitDir, monitorTimer)) && h.exists(filepath.Join(h.unitDir, monitorService))
	if !st.Installed {
		return st
	}
	st.Enabled = h.systemctlOK(ctx, "is-enabled", monitorTimer)
	st.Active = h.systemctlOK(ctx, "is-active", monitorTimer)
	if out, err := h.run(ctx, "systemctl", "show", "-p", "Result", "--value", monitorService); err == nil {
		st.LastResult = strings.TrimSpace(string(out))
	}
	return st
}

// ---- the command ----

const monitorUsage = `usage: bobres monitor status [--json] | enable | disable | snapshot [--print] | supported  [--dir D]

  status     whether the collector runs and how old its last snapshot is (exit 1 when it is not working)
  enable     put the collector in place (two systemd units) and take it off the "off" list
  disable    take the collector away for good: bobres install and bobres update leave it out from now on
  snapshot   take one snapshot now (what the timer runs); --print shows it instead of writing it
  supported  exit 0: this bobres has the collector (the unit asks before it runs)`

// monitorCLI is the `bobres monitor` command, its side effects replaceable.
type monitorCLI struct {
	out, errOut io.Writer
	host        *monitorHost
	now         func() time.Time
	// collector makes the collector for the install in dir (tests give it
	// fake sources).
	collector func(dir string, env map[string]string) *collect.Collector
}

func newMonitorCLI(out, errOut io.Writer) *monitorCLI {
	return &monitorCLI{out: out, errOut: errOut, host: newMonitorHost(), now: time.Now,
		collector: func(dir string, env map[string]string) *collect.Collector {
			c := collect.New(dir, version.Version)
			if len(env) > 0 {
				c.Expected = allServices(dir, env)
			}
			return c
		}}
}

func monitorCmd(args []string, out, errOut io.Writer) int {
	return newMonitorCLI(out, errOut).run(args)
}

func (m *monitorCLI) run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(m.errOut, monitorUsage)
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
		fmt.Fprintln(m.out, monitorUsage)
		return 0
	}
	fmt.Fprintln(m.errOut, monitorUsage)
	return 2
}

// snapshot takes one snapshot and writes it to <dir>/run/monitor (or --out).
func (m *monitorCLI) snapshot(args []string) int {
	flags := flag.NewFlagSet("monitor snapshot", flag.ContinueOnError)
	flags.SetOutput(m.errOut)
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
	if prev, err := snapshot.Read(target, m.now()); err == nil {
		c.Previous = prev
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	s := c.Collect(ctx)
	if *show {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(m.out, string(b))
		return 0
	}
	if *outDir == "" {
		if err := ensureMonitorDirs(*dir); err != nil {
			fmt.Fprintf(m.errOut, "error: %v\n", err)
			return 1
		}
	}
	if err := snapshot.Write(target, s); err != nil {
		fmt.Fprintf(m.errOut, "error: %v\n", err)
		return 1
	}
	// The timer runs this every 30 seconds and the journal keeps what it
	// prints: a person at a terminal gets a line, the timer gets silence.
	if detectUI(m.out, os.Getenv).tty {
		fmt.Fprintf(m.out, "Snapshot written to %s in %d ms", filepath.Join(target, snapshot.FileName), s.DurationMS)
		if len(s.Errors) > 0 {
			fmt.Fprintf(m.out, " (not read: %s)", strings.Join(s.Errors, "; "))
		}
		fmt.Fprintln(m.out)
	}
	return 0
}

// toggle turns the collector on (enable) or off (disable) for good: the
// choice is BOBRES_MONITOR_COLLECTOR in .env, which the install and update
// steps follow, and the units are put in place or taken away now.
func (m *monitorCLI) toggle(on bool, args []string) int {
	name := "disable"
	if on {
		name = "enable"
	}
	flags := flag.NewFlagSet("monitor "+name, flag.ContinueOnError)
	flags.SetOutput(m.errOut)
	dir := flags.String("dir", defaultDir(), dirUsage)
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		return 2
	}
	envPath := filepath.Join(*dir, ".env")
	env, ok, err := readEnv(envPath)
	if err != nil || !ok {
		fmt.Fprintf(m.errOut, "error: no install found in %s (run `bobres install`, or pass --dir): %v\n", *dir, err)
		return 2
	}
	if on {
		delete(env, monitorCollectorKey)
	} else {
		env[monitorCollectorKey] = "off"
	}
	if err := writeEnv(envPath, env); err != nil {
		fmt.Fprintf(m.errOut, "error: %v\n", err)
		return 1
	}
	return m.report(m.host.ensure(context.Background(), *dir, env), on)
}

// report prints how putting the collector in place went; the exit status is 1
// when it could not be done.
func (m *monitorCLI) report(o monitorOutcome, wanted bool) int {
	if o.Err != nil {
		fmt.Fprintf(m.errOut, "error: host monitoring: %v\n", o.Err)
		return 1
	}
	switch o.State {
	case "installed", "updated", "unchanged":
		fmt.Fprintf(m.out, "Host monitoring is on (%s, %s): a snapshot every 30 seconds, read by the dashboard's Monitoring section.\n"+
			"Turn it off for good: sudo bobres monitor disable\n", monitorTimer, o.State)
	case "removed", "off":
		fmt.Fprintf(m.out, "Host monitoring is off (%s): the dashboard's Monitoring section shows no host figures until sudo bobres monitor enable.\n", o.Why)
	case "skipped":
		fmt.Fprintf(m.out, "Host monitoring was not put in place: %s.\n", o.Why)
		if wanted {
			return 1
		}
	}
	return 0
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
	flags := flag.NewFlagSet("monitor status", flag.ContinueOnError)
	flags.SetOutput(m.errOut)
	dir := flags.String("dir", defaultDir(), dirUsage)
	asJSON := flags.Bool("json", false, "print the status as JSON")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		return 2
	}
	env, ok, _ := readEnv(filepath.Join(*dir, ".env"))
	if !ok {
		fmt.Fprintf(m.errOut, "error: no install found in %s (run `bobres install`, or pass --dir)\n", *dir)
		return 2
	}
	st := monitorStatus{Enabled: !monitorOff(env)}
	units := m.host.unitState(context.Background())
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
		fmt.Fprintln(m.out, string(b))
	} else {
		fmt.Fprintf(m.out, "%-5s %s\n", res.Status, res.Detail)
		if units != nil && units.LastResult != "" && units.LastResult != "success" {
			fmt.Fprintf(m.out, "      the last run of %s ended with: %s (journalctl -u %s)\n", monitorService, units.LastResult, monitorService)
		}
	}
	if res.Status == Pass || res.Status == Skip {
		return 0
	}
	return 1
}

// ---- the check and doctor line ----

// monitorFacts is what the "Host monitor" line of bobres check and doctor
// judges.
type monitorFacts struct {
	off          bool
	units        *monitorUnitState
	systemdKnown bool
	snap         *snapshot.Snapshot
	snapErr      error
	now          time.Time
}

// monitorVerdict says in one line whether the collector is installed and
// fresh. It is never a FAIL: host monitoring is optional.
func monitorVerdict(f monitorFacts) Result {
	const name = "Host monitor"
	switch {
	case f.off:
		return Result{name, Skip, "turned off (" + monitorCollectorKey + "=off); sudo bobres monitor enable turns it on"}
	case f.snapErr == nil:
		age := f.snap.Age(f.now)
		docker := ""
		if f.snap.Docker != nil && f.snap.Docker.OK {
			docker = fmt.Sprintf(", docker %s with %d containers", orDash(f.snap.Docker.Version), len(f.snap.Docker.Containers))
		}
		if !f.snap.Fresh(f.now) {
			return Result{name, Warn, fmt.Sprintf("the last snapshot is %s old: the timer may have stopped (systemctl status %s)", roundAge(age), monitorTimer)}
		}
		if len(f.snap.Errors) > 0 {
			return Result{name, Warn, fmt.Sprintf("fresh (%s old) but not everything could be read: %s", roundAge(age), strings.Join(f.snap.Errors, "; "))}
		}
		return Result{name, Pass, fmt.Sprintf("a snapshot %s old%s", roundAge(age), docker)}
	case !errors.Is(f.snapErr, fs.ErrNotExist):
		if errors.Is(f.snapErr, fs.ErrPermission) {
			return Result{name, Skip, "cannot read the snapshot (run it as root)"}
		}
		return Result{name, Warn, "the snapshot cannot be read: " + firstLine(f.snapErr.Error())}
	}
	// No snapshot at all.
	switch {
	case !f.systemdKnown:
		return Result{name, Skip, "no snapshot, and this machine does not run systemd: host monitoring is off here"}
	case f.units != nil && f.units.Installed && f.units.Active:
		return Result{name, Warn, "installed, but no snapshot yet (it appears within a minute): journalctl -u " + monitorService}
	case f.units != nil && f.units.Installed:
		return Result{name, Warn, "installed but the timer is not running: sudo systemctl enable --now " + monitorTimer}
	}
	return Result{name, Warn, "not installed: the dashboard's Monitoring section shows no host figures; sudo bobres monitor enable puts it in place"}
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

// monitorResult is the "Host monitor" line for the install in dir.
func monitorResult(ctx context.Context, h *monitorHost, dir string, env map[string]string, now time.Time) Result {
	snap, err := snapshot.Read(monitorDir(dir), now)
	units := h.unitState(ctx)
	return monitorVerdict(monitorFacts{off: monitorOff(env), units: units, systemdKnown: units != nil, snap: snap, snapErr: err, now: now})
}
