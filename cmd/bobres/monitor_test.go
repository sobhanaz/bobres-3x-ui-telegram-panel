package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/collect"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

var monitorNow = time.Unix(1_760_000_000, 0)

const testExe = "/usr/local/bin/bobres"

// fakeMonitorSystem plays a server's systemd for the collector's units: the unit files, what systemctl
// says of the timer, and every program run, in order. Nothing touches this machine.
type fakeMonitorSystem struct {
	files     map[string]string
	modes     map[string]os.FileMode
	cmds      []string
	root      bool
	systemd   bool
	enabled   bool
	active    string // active, inactive or failed
	fail      map[string]error
	writeErr  error
	lastState string
}

func newFakeMonitorSystem() *fakeMonitorSystem {
	return &fakeMonitorSystem{files: map[string]string{}, modes: map[string]os.FileMode{}, root: true, systemd: true, active: "inactive",
		fail: map[string]error{}, lastState: "success"}
}

func (f *fakeMonitorSystem) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.cmds = append(f.cmds, line)
	for prefix, err := range f.fail {
		if strings.HasPrefix(line, prefix) {
			return []byte("Failed: " + line + "\nmore"), err
		}
	}
	_, installed := f.files[systemUnitDir+"/"+monitorTimerUnit]
	switch {
	case line == "systemctl enable "+monitorTimerUnit:
		if !installed {
			return []byte("Failed to enable unit: Unit file does not exist."), errors.New("exit status 1")
		}
		f.enabled = true
	case line == "systemctl start "+monitorTimerUnit, line == "systemctl restart "+monitorTimerUnit:
		f.active = "active"
	case line == "systemctl disable --now "+monitorTimerUnit:
		f.enabled, f.active = false, "inactive"
	case strings.HasPrefix(line, "systemctl reset-failed"):
		if f.active == "failed" {
			f.active = "inactive"
		}
	case line == "systemctl is-enabled "+monitorTimerUnit:
		switch {
		case !installed:
			return []byte("Failed to get unit file state\n"), errors.New("exit status 1")
		case f.enabled:
			return []byte("enabled\n"), nil
		}
		return []byte("disabled\n"), errors.New("exit status 1")
	case line == "systemctl is-active "+monitorTimerUnit:
		if f.active == "active" {
			return []byte("active\n"), nil
		}
		return []byte(f.active + "\n"), errors.New("exit status 3")
	case line == "systemctl show -p Result --value "+monitorServiceUnit:
		return []byte(f.lastState + "\n"), nil
	}
	return nil, nil
}

func (f *fakeMonitorSystem) units() *systemUnits {
	return &systemUnits{
		run: f.run,
		readFile: func(p string) ([]byte, error) {
			if v, ok := f.files[p]; ok {
				return []byte(v), nil
			}
			return nil, os.ErrNotExist
		},
		writeFile: func(p string, b []byte, mode os.FileMode) error {
			if f.writeErr != nil {
				return f.writeErr
			}
			f.files[p], f.modes[p] = string(b), mode
			return nil
		},
		remove: func(p string) error {
			if _, ok := f.files[p]; !ok {
				return os.ErrNotExist
			}
			delete(f.files, p)
			return nil
		},
		exists: func(p string) bool {
			if p == systemdRunDir {
				return f.systemd
			}
			_, ok := f.files[p]
			return ok
		},
		lookPath: func(string) (string, error) { return "/usr/bin/systemctl", nil },
		euid: func() int {
			if f.root {
				return 0
			}
			return 1000
		},
		goos: "linux", unitDir: systemUnitDir, runDir: systemdRunDir,
	}
}

func (f *fakeMonitorSystem) ran(prefix string) int {
	n := 0
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

var testMonitorOpts = monitorOptions{dir: "/opt/bobres", exe: testExe}

const (
	unitServicePath = systemUnitDir + "/" + monitorServiceUnit
	unitTimerPath   = systemUnitDir + "/" + monitorTimerUnit
)

func TestRenderMonitorUnits(t *testing.T) {
	service, timer, err := renderMonitorUnits(testMonitorOpts)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\nUser=root\n", "\nType=oneshot\n",
		"\nExecStart=/usr/local/bin/bobres monitor snapshot --dir /opt/bobres\n",
		// An older bobres (a rollback) does not know the command: its refusal skips the run, it does not fail it.
		"\nExecCondition=/usr/local/bin/bobres monitor supported\n",
		// The install gone: skipped quietly.
		"\nConditionPathIsDirectory=/opt/bobres/run/monitor\n",
		// It may write the snapshot directory and nothing else.
		"\nProtectSystem=strict\n", "\nReadWritePaths=/opt/bobres/run/monitor\n", "\nNoNewPrivileges=yes\n", "\nPrivateTmp=yes\n",
		"\nProtectHome=read-only\n", "\nCPUQuota=25%\n", "\nMemoryMax=192M\n", "\nTimeoutStartSec=30\n", "\nUMask=0022\n",
		"\nRestrictSUIDSGID=yes\n", "\nLockPersonality=yes\n", "\nProtectKernelTunables=yes\n",
	} {
		if !strings.Contains(service, want) {
			t.Errorf("the service lacks %q:\n%s", want, service)
		}
	}
	for _, want := range []string{"\nOnUnitActiveSec=30s\n", "\nOnBootSec=20s\n", "\nUnit=bobres-monitor.service\n", "\nWantedBy=timers.target\n"} {
		if !strings.Contains(timer, want) {
			t.Errorf("the timer lacks %q:\n%s", want, timer)
		}
	}
	for _, text := range []string{service, timer} {
		for _, ph := range []string{"@DIR@", "@EXE@"} {
			if strings.Contains(text, ph) {
				t.Errorf("%s was left:\n%s", ph, text)
			}
		}
		// The unit reads no environment and no .env: nothing secret can reach it.
		if strings.Contains(text, "EnvironmentFile") || strings.Contains(text, "\nEnvironment=") || strings.Contains(text, "/.env") {
			t.Errorf("the unit reads the environment:\n%s", text)
		}
	}
	// It runs nothing but bobres, and writes only the snapshot directory.
	if strings.Count(service, "ReadWritePaths=") != 1 || strings.Count(service, "ExecStart=") != 1 {
		t.Errorf("service:\n%s", service)
	}
	// An install elsewhere, and a bobres elsewhere.
	other, _, err := renderMonitorUnits(monitorOptions{dir: "/srv/bob-res_1.0", exe: "/opt/bin/bobres"})
	if err != nil || !strings.Contains(other, "ExecStart=/opt/bin/bobres monitor snapshot --dir /srv/bob-res_1.0") ||
		!strings.Contains(other, "ReadWritePaths=/srv/bob-res_1.0/run/monitor") {
		t.Errorf("another place: %v\n%s", err, other)
	}
	// A path systemd could read as something else is refused, not escaped.
	for _, bad := range []monitorOptions{{dir: "/srv/my store", exe: testExe}, {dir: "/opt/bobres", exe: "/opt/my tools/bobres"},
		{dir: "relative", exe: testExe}, {dir: "/opt/bobres", exe: "/usr/bin/bob%res"}, {dir: "/opt/$HOME", exe: testExe}, {dir: "", exe: testExe}} {
		if _, _, err := renderMonitorUnits(bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	// The templates are the files in deploy/systemd, embedded.
	if len(deploy.MonitorServiceUnit) == 0 || len(deploy.MonitorTimerUnit) == 0 {
		t.Error("the unit templates are not embedded")
	}
}

func TestEnsureMonitorPutsTheUnitsInPlace(t *testing.T) {
	f := newFakeMonitorSystem()
	res := f.units().ensureMonitor(context.Background(), testMonitorOpts)
	if res.kind != markOK || !res.changed || !strings.Contains(res.text, monitorTimerUnit+" is on") {
		t.Fatalf("%+v", res)
	}
	service, timer, _ := renderMonitorUnits(testMonitorOpts)
	if f.files[unitServicePath] != service || f.files[unitTimerPath] != timer || f.modes[unitServicePath] != 0o644 || f.modes[unitTimerPath] != 0o644 {
		t.Errorf("unit files: %v %v", f.files, f.modes)
	}
	if !f.enabled || f.active != "active" {
		t.Errorf("enabled %v, active %s", f.enabled, f.active)
	}
	want := []string{
		"systemctl daemon-reload", "systemctl is-active " + monitorTimerUnit, "systemctl is-enabled " + monitorTimerUnit,
		"systemctl enable " + monitorTimerUnit, "systemctl start " + monitorTimerUnit, "systemctl start --no-block " + monitorServiceUnit,
		"systemctl is-enabled " + monitorTimerUnit, "systemctl is-active " + monitorTimerUnit,
	}
	if strings.Join(f.cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("systemctl calls:\n%s\nwant\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestEnsureMonitorTwiceChangesNothingTheSecondTime(t *testing.T) {
	f := newFakeMonitorSystem()
	u := f.units()
	u.ensureMonitor(context.Background(), testMonitorOpts)
	f.cmds = nil
	writes := 0
	inner := u.writeFile
	u.writeFile = func(p string, b []byte, m os.FileMode) error { writes++; return inner(p, b, m) }
	res := u.ensureMonitor(context.Background(), testMonitorOpts)
	if res.kind != markOK || res.changed || res.text != "" || writes != 0 {
		t.Fatalf("%+v, %d writes", res, writes)
	}
	// Only the questions: nothing is reloaded, enabled, restarted or started.
	for _, c := range f.cmds {
		if !strings.HasPrefix(c, "systemctl is-") {
			t.Errorf("a second run did %q", c)
		}
	}
}

func TestEnsureMonitorFollowsAMovedBinaryAndRestartsTheTimer(t *testing.T) {
	f := newFakeMonitorSystem()
	u := f.units()
	u.ensureMonitor(context.Background(), testMonitorOpts)
	f.cmds = nil
	res := u.ensureMonitor(context.Background(), monitorOptions{dir: "/opt/bobres", exe: "/opt/bin/bobres"})
	if res.kind != markOK || !res.changed || !strings.Contains(f.files[unitServicePath], "ExecStart=/opt/bin/bobres monitor snapshot") {
		t.Fatalf("%+v\n%s", res, f.files[unitServicePath])
	}
	if f.ran("systemctl daemon-reload") != 1 || f.ran("systemctl restart "+monitorTimerUnit) != 1 || f.ran("systemctl start --no-block") != 1 {
		t.Errorf("calls: %v", f.cmds)
	}
}

func TestEnsureMonitorTurnsAStoppedOrFailedTimerBackOn(t *testing.T) {
	for _, state := range []string{"inactive", "failed"} {
		f := newFakeMonitorSystem()
		u := f.units()
		u.ensureMonitor(context.Background(), testMonitorOpts)
		f.cmds, f.enabled, f.active = nil, false, state // someone ran systemctl disable --now, or a start limit was hit
		res := u.ensureMonitor(context.Background(), testMonitorOpts)
		if res.kind != markOK || f.ran("systemctl enable "+monitorTimerUnit) != 1 || f.ran("systemctl daemon-reload") != 0 || !f.enabled || f.active != "active" {
			t.Errorf("%s: %+v %v", state, res, f.cmds)
		}
		if state == "failed" && f.ran("systemctl reset-failed") != 1 {
			t.Errorf("a failed timer was not reset: %v", f.cmds)
		}
	}
}

func TestEnsureMonitorNeedsRootAndSystemd(t *testing.T) {
	for name, tc := range map[string]func(*fakeMonitorSystem, *systemUnits){
		"not root":   func(f *fakeMonitorSystem, _ *systemUnits) { f.root = false },
		"no systemd": func(f *fakeMonitorSystem, _ *systemUnits) { f.systemd = false },
		"not linux":  func(_ *fakeMonitorSystem, u *systemUnits) { u.goos = "darwin" },
	} {
		f := newFakeMonitorSystem()
		u := f.units()
		tc(f, u)
		res := u.ensureMonitor(context.Background(), testMonitorOpts)
		if res.kind != markSkip || !strings.HasPrefix(res.text, "not set up: ") || len(f.cmds) != 0 || len(f.files) != 0 {
			t.Errorf("%s: %+v %v %v", name, res, f.cmds, f.files)
		}
	}
}

func TestEnsureMonitorWhenTurnedOffTakesTheUnitsAndTheSnapshotAway(t *testing.T) {
	dir := t.TempDir()
	if err := ensureMonitorDirs(dir); err != nil {
		t.Fatal(err)
	}
	f := newFakeMonitorSystem()
	u := f.units()
	opts := monitorOptions{dir: dir, exe: testExe}
	u.ensureMonitor(context.Background(), opts)
	if err := snapshot.Write(monitorDir(dir), &snapshot.Snapshot{Schema: 1, GeneratedAt: monitorNow.Unix(), GeneratedMS: monitorNow.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	f.cmds = nil
	opts.off = true
	res := u.ensureMonitor(context.Background(), opts)
	if res.kind != markOK || !res.changed || len(f.files) != 0 || snapshot.Exists(monitorDir(dir)) || f.enabled {
		t.Fatalf("%+v files %v", res, f.files)
	}
	if f.ran("systemctl disable --now "+monitorTimerUnit) != 1 || f.ran("systemctl daemon-reload") != 1 {
		t.Errorf("calls: %v", f.cmds)
	}
	// Again: nothing left to do, nothing run.
	f.cmds = nil
	if res := u.ensureMonitor(context.Background(), opts); res.changed || len(f.cmds) != 0 {
		t.Errorf("%+v %v", res, f.cmds)
	}
	if _, err := os.Stat(monitorDir(dir)); err != nil {
		t.Errorf("the directory core mounts must stay: %v", err)
	}
}

func TestEnsureMonitorReportsWhatSystemdRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		prefix string
		kind   markKind
		want   string
	}{
		"daemon-reload": {"systemctl daemon-reload", markWarn, "daemon-reload"},
		"enable":        {"systemctl enable", markWarn, "systemctl enable"},
		"start":         {"systemctl start bobres-monitor.timer", markWarn, "installed, but"},
	} {
		f := newFakeMonitorSystem()
		f.fail[tc.prefix] = errors.New("exit status 1")
		res := f.units().ensureMonitor(context.Background(), testMonitorOpts)
		if res.kind != tc.kind || !strings.Contains(res.text, tc.want) || strings.Contains(res.text, "more") {
			t.Errorf("%s: %+v", name, res)
		}
	}
	f := newFakeMonitorSystem()
	f.writeErr = errors.New("read-only file system")
	if res := f.units().ensureMonitor(context.Background(), testMonitorOpts); res.kind != markWarn || !strings.Contains(res.text, "read-only") {
		t.Errorf("write: %+v", res)
	}
	f = newFakeMonitorSystem()
	if res := f.units().ensureMonitor(context.Background(), monitorOptions{dir: "/srv/my store", exe: testExe}); res.kind != markWarn || len(f.files) != 0 {
		t.Errorf("an unsafe path: %+v", res)
	}
}

func TestEnsureMonitorDirs(t *testing.T) {
	dir := t.TempDir()
	if err := ensureMonitorDirs(dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"run", "run/monitor"} {
		if st, err := os.Stat(filepath.Join(dir, p)); err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v %v", p, st, err)
		}
	}
	// Again is fine, and puts a mode that drifted right.
	if err := os.Chmod(monitorDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ensureMonitorDirs(dir); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(monitorDir(dir)); st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", st.Mode())
	}
	// Never through a link.
	linked := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(linked, "run")); err != nil {
		t.Fatal(err)
	}
	if err := ensureMonitorDirs(linked); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a linked run/: %v", err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("something was made through the link: %v", entries)
	}
	linked2 := t.TempDir()
	if err := os.Mkdir(filepath.Join(linked2, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(linked2, "run", "monitor")); err != nil {
		t.Fatal(err)
	}
	if err := ensureMonitorDirs(linked2); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a linked run/monitor: %v", err)
	}
}

// The helper looks at the directory before it writes or reads there, and reads back only its own file.
func TestTheHelperDistrustsAStrangersDirectory(t *testing.T) {
	good := func(t *testing.T) string {
		dir := t.TempDir()
		if err := ensureMonitorDirs(dir); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	dir := good(t)
	if err := checkMonitorDir(dir); err != nil {
		t.Fatalf("a good directory: %v", err)
	}
	prev := &snapshot.Snapshot{Schema: 1, GeneratedAt: monitorNow.Unix(), GeneratedMS: monitorNow.UnixMilli()}
	if err := snapshot.Write(monitorDir(dir), prev); err != nil {
		t.Fatal(err)
	}
	if readPrevious(dir, monitorNow) == nil {
		t.Fatal("its own snapshot was not read back")
	}
	for name, mutate := range map[string]func(string){
		"others can write the directory": func(d string) { _ = os.Chmod(monitorDir(d), 0o777) },
		"others can write run/":          func(d string) { _ = os.Chmod(filepath.Join(d, "run"), 0o775) },
		"the file is world-writable": func(d string) {
			_ = os.Chmod(filepath.Join(monitorDir(d), snapshot.FileName), 0o666)
		},
		"the file is a link": func(d string) {
			p := filepath.Join(monitorDir(d), snapshot.FileName)
			_ = os.Remove(p)
			_ = os.Symlink("/etc/hostname", p)
		},
	} {
		d := good(t)
		if err := snapshot.Write(monitorDir(d), prev); err != nil {
			t.Fatal(err)
		}
		mutate(d)
		if name != "the file is world-writable" && name != "the file is a link" && checkMonitorDir(d) == nil {
			t.Errorf("%s: the directory was accepted", name)
		}
		if readPrevious(d, monitorNow) != nil {
			t.Errorf("%s: the previous snapshot was used", name)
		}
	}
	// A directory that is another user's.
	f := withFakeRoot(t)
	d := good(t)
	f.owners["run"], f.owners["monitor"] = os.Geteuid(), 4242
	if err := checkMonitorDir(d); err == nil || !strings.Contains(err.Error(), "belongs to uid 4242") {
		t.Errorf("a foreign directory: %v", err)
	}
	// A link in place of the directory, and a missing one.
	l := t.TempDir()
	_ = os.Symlink(t.TempDir(), filepath.Join(l, "run"))
	if checkMonitorDir(l) == nil || checkMonitorDir(t.TempDir()) == nil {
		t.Error("a linked or missing directory was accepted")
	}
}

func TestMonitorVerdict(t *testing.T) {
	fresh := &snapshot.Snapshot{GeneratedAt: monitorNow.Add(-12 * time.Second).Unix(), Docker: &snapshot.Docker{OK: true, Version: "29.9.0",
		Containers: containers(9)}}
	stale := &snapshot.Snapshot{GeneratedAt: monitorNow.Add(-14 * time.Minute).Unix()}
	withErrors := &snapshot.Snapshot{GeneratedAt: monitorNow.Add(-5 * time.Second).Unix(), Errors: []string{"docker: permission denied on the docker socket"}}
	up := &monitorUnitState{Installed: true, Enabled: true, Active: true}
	for name, tc := range map[string]struct {
		f      monitorFacts
		status Status
		want   string
	}{
		"off":              {monitorFacts{off: true}, Skip, "turned off"},
		"fresh":            {monitorFacts{snap: fresh, units: up, systemdKnown: true}, Pass, "a snapshot 12s old, docker 29.9.0 with 9 containers"},
		"stale":            {monitorFacts{snap: stale, units: up, systemdKnown: true}, Warn, "the last snapshot is 14m old"},
		"fresh with gaps":  {monitorFacts{snap: withErrors}, Warn, "permission denied on the docker socket"},
		"unreadable":       {monitorFacts{snapErr: errors.New("snapshot: not a valid snapshot")}, Warn, "cannot be read"},
		"not root":         {monitorFacts{snapErr: fmt.Errorf("open: %w", os.ErrPermission)}, Skip, "run it as root"},
		"no systemd":       {monitorFacts{snapErr: os.ErrNotExist}, Skip, "does not run systemd"},
		"not installed":    {monitorFacts{snapErr: os.ErrNotExist, systemdKnown: true, units: &monitorUnitState{}}, Warn, "sudo bobres monitor enable"},
		"no snapshot yet":  {monitorFacts{snapErr: os.ErrNotExist, systemdKnown: true, units: up}, Warn, "no snapshot yet"},
		"timer not active": {monitorFacts{snapErr: os.ErrNotExist, systemdKnown: true, units: &monitorUnitState{Installed: true}}, Warn, "timer is not running"},
	} {
		tc.f.now = monitorNow
		r := monitorVerdict(tc.f)
		if r.Name != monitorLabel || r.Status != tc.status || !strings.Contains(r.Detail, tc.want) {
			t.Errorf("%s: %+v, want %s containing %q", name, r, tc.status, tc.want)
		}
		if r.Status == Fail {
			t.Errorf("%s: host monitoring is optional and never a FAIL", name)
		}
	}
}

// collectorFor gives the snapshot command a machine of its own: a few /proc files and no engine, no systemd.
func collectorFor(statLine string, now func() time.Time) func(string, map[string]string) *collect.Collector {
	return func(dir string, env map[string]string) *collect.Collector {
		fs := fakeProc{
			"/proc/stat":    statLine + "\ncpu0 1 1 1 1 1 1 1 1\ncpu1 1 1 1 1 1 1 1 1\n",
			"/proc/meminfo": "MemTotal: 2000000 kB\nMemAvailable: 500000 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n",
			"/proc/loadavg": "0.5 0.4 0.3 1/100 1234\n", "/proc/uptime": "1000.5 900.0\n", "/proc/net/dev": "Inter-|\n face |\n eth0: 100 1 0 0 0 0 0 0 200 1 0 0 0 0 0 0\n",
			"/proc/self/mountinfo": "22 1 8:1 / / rw - ext4 /dev/sda1 rw\n",
		}
		c := collect.New(dir, "9.9.9")
		c.FS, c.Docker, c.Run, c.Now = fs, nil, nil, now
		c.Statfs = func(string) (collect.Statfs, error) { return collect.Statfs{Total: 1000, Used: 400, Avail: 500}, nil }
		c.Resolve = func(p string) (string, error) { return p, nil }
		c.Hostname = func() (string, error) { return "testhost", nil }
		c.Expected = nil
		return c
	}
}

type fakeProc map[string]string

func (f fakeProc) ReadFile(name string) ([]byte, error) {
	if s, ok := f[name]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}

func newTestMonitorCLI(f *fakeMonitorSystem, stat string, now func() time.Time) (*monitorCLI, *bytes.Buffer, *bytes.Buffer) {
	o, out, errOut := testOps("", &fakeOps{})
	o.monitorUnits = f.units()
	o.exe = func() (string, error) { return testExe, nil }
	o.sleep = func(time.Duration) {}
	return &monitorCLI{o: o, now: now, collector: collectorFor(stat, now)}, out, errOut
}

func TestMonitorSnapshotWritesTheFileAndComputesTheCPUShare(t *testing.T) {
	dir := installDir(t)
	clock := monitorNow
	now := func() time.Time { return clock }
	m, out, errOut := newTestMonitorCLI(newFakeMonitorSystem(), "cpu  100 0 100 800 0 0 0 0 0 0", now)
	if code := m.run([]string{"snapshot", "--dir", dir}); code != 0 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	// Run by a timer (no terminal), it prints nothing.
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("output: %q %q", out, errOut)
	}
	first, err := snapshot.Read(monitorDir(dir), clock)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != "9.9.9" || first.Host == nil || first.Host.Hostname != "testhost" || first.Host.Cores != 2 || first.Host.CPU.Pct != nil || first.Host.Mem.Total != 2000000*1024 {
		t.Errorf("first snapshot: %+v %+v", first, first.Host)
	}
	fi, err := os.Stat(filepath.Join(monitorDir(dir), snapshot.FileName))
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("snapshot file: %v %v", fi, err)
	}

	// 30 seconds and 50 busy jiffies of 100 later: half the processor.
	clock = clock.Add(30 * time.Second)
	m, _, _ = newTestMonitorCLI(newFakeMonitorSystem(), "cpu  125 0 125 850 0 0 0 0 0 0", now)
	if code := m.run([]string{"snapshot", "--dir", dir}); code != 0 {
		t.Fatalf("second run exit %d", code)
	}
	second, err := snapshot.Read(monitorDir(dir), clock)
	if err != nil || second.Host.CPU.Pct == nil || *second.Host.CPU.Pct != 50 {
		t.Fatalf("second snapshot: %v %+v", err, second.Host)
	}
}

func TestMonitorSnapshotRefusesAnUntrustworthyDirectory(t *testing.T) {
	dir := installDir(t)
	if err := ensureMonitorDirs(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(monitorDir(dir), 0o777); err != nil {
		t.Fatal(err)
	}
	m, _, errOut := newTestMonitorCLI(newFakeMonitorSystem(), "cpu  1 1 1 1 1 1 1 1 1 1", func() time.Time { return monitorNow })
	if code := m.run([]string{"snapshot", "--dir", dir}); code != 0 {
		// ensureMonitorDirs puts the mode right first, so the run goes on.
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if st, _ := os.Stat(monitorDir(dir)); st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", st.Mode())
	}
	// A link in place of run/monitor is not followed.
	dir2 := installDir(t)
	if err := os.Mkdir(filepath.Join(dir2, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, monitorDir(dir2)); err != nil {
		t.Fatal(err)
	}
	if code := m.run([]string{"snapshot", "--dir", dir2}); code != 1 || !strings.Contains(errOut.String(), "symbolic link") {
		t.Errorf("a link: exit %d %s", code, errOut)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("a snapshot was written through the link: %v", entries)
	}
}

func TestMonitorSnapshotPrintAndOut(t *testing.T) {
	dir := installDir(t)
	now := func() time.Time { return monitorNow }
	m, out, _ := newTestMonitorCLI(newFakeMonitorSystem(), "cpu  1 1 1 1 1 1 1 1 1 1", now)
	if code := m.run([]string{"snapshot", "--dir", dir, "--print"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var s snapshot.Snapshot
	if err := json.Unmarshal(out.Bytes(), &s); err != nil || s.Schema != 1 || s.Host == nil {
		t.Fatalf("printed: %v\n%s", err, out)
	}
	if snapshot.Exists(monitorDir(dir)) {
		t.Error("--print wrote the file")
	}
	custom := t.TempDir()
	m, _, _ = newTestMonitorCLI(newFakeMonitorSystem(), "cpu  1 1 1 1 1 1 1 1 1 1", now)
	if code := m.run([]string{"snapshot", "--dir", dir, "--out", custom}); code != 0 || !snapshot.Exists(custom) {
		t.Fatalf("--out: exit %d", code)
	}
	// A directory that does not exist is an error, not a reason to make one somewhere unexpected.
	m, _, errOut := newTestMonitorCLI(newFakeMonitorSystem(), "cpu  1 1 1 1 1 1 1 1 1 1", now)
	if code := m.run([]string{"snapshot", "--dir", dir, "--out", filepath.Join(custom, "missing", "deeper")}); code != 1 || errOut.Len() == 0 {
		t.Errorf("a missing output directory: exit %d %q", code, errOut)
	}
	for _, args := range [][]string{{"snapshot", "--bogus"}, {"snapshot", "extra"}, {"snapshot", "--timeout", "0s"}} {
		m, _, _ = newTestMonitorCLI(newFakeMonitorSystem(), "cpu  1 1 1 1 1 1 1 1 1 1", now)
		if code := m.run(args); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestMonitorEnableAndDisableFollowTheEnvFile(t *testing.T) {
	dir := t.TempDir()
	writeInstalledCompose(t, dir)
	env := checkEnvMap()
	env["OPERATOR_NOTE"] = "keep me"
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFakeMonitorSystem()
	m, out, errOut := newTestMonitorCLI(f, "cpu 1 1 1 1 1 1 1 1", func() time.Time { return monitorNow })
	// On is the default: enable puts the units in place and writes nothing to .env.
	if code := m.run([]string{"enable", "--dir", dir}); code != 0 || f.files[unitTimerPath] == "" {
		t.Fatalf("enable: exit %d %s %s", code, out, errOut)
	}
	if !strings.Contains(out.String(), "Host monitoring is on") || !strings.Contains(out.String(), "sudo bobres monitor disable") {
		t.Errorf("enable output: %s", out)
	}
	out.Reset()
	if code := m.run([]string{"disable", "--dir", dir}); code != 0 || len(f.files) != 0 {
		t.Fatalf("disable: exit %d %s %s", code, out, errOut)
	}
	if !strings.Contains(out.String(), "Saved "+monitorCollectorKey+"=off") || !strings.Contains(out.String(), "Host monitoring is off") {
		t.Errorf("disable output: %s", out)
	}
	got, _, _ := readEnv(filepath.Join(dir, ".env"))
	if got[monitorCollectorKey] != "off" || got["OPERATOR_NOTE"] != "keep me" || got["BOBRES_TOKEN_BOT"] != env["BOBRES_TOKEN_BOT"] {
		t.Errorf("the .env after disable: %q, %q", got[monitorCollectorKey], got["OPERATOR_NOTE"])
	}
	if st, _ := os.Stat(filepath.Join(dir, ".env")); st == nil || st.Mode().Perm() != 0o600 {
		t.Errorf(".env mode: %v", st)
	}
	// It stays off through an install or update, which run the same step.
	if res := f.units().ensureMonitor(context.Background(), monitorOptions{dir: dir, exe: testExe, off: monitorOff(got)}); res.changed || len(f.files) != 0 {
		t.Errorf("ensure after disable: %+v", res)
	}
	out.Reset()
	if code := m.run([]string{"enable", "--dir", dir}); code != 0 || f.files[unitServicePath] == "" {
		t.Fatalf("enable again: exit %d %s", code, out)
	}
	if again, _, _ := readEnv(filepath.Join(dir, ".env")); again[monitorCollectorKey] != "on" {
		t.Errorf("enable left %q in .env", again[monitorCollectorKey])
	}
	// Units that went missing while the .env says on come back with enable.
	f.files = map[string]string{}
	if code := m.run([]string{"enable", "--dir", dir}); code != 0 || f.files[unitServicePath] == "" {
		t.Fatalf("enable after the units were lost: exit %d", code)
	}
	// The setting is also one of bobres config's.
	out.Reset()
	o, out2, _ := testOps("", &fakeOps{})
	o.monitorUnits, o.exe = f.units(), func() (string, error) { return testExe, nil }
	if code := o.configCmd([]string{"set", monitorCollectorKey, "off", "--dir", dir}); code != 0 || len(f.files) != 0 || !strings.Contains(out2.String(), "Host monitoring is off") {
		t.Errorf("config set: exit %d files %v\n%s", code, f.files, out2)
	}
	if code := o.configCmd([]string{"set", monitorCollectorKey, "maybe", "--dir", dir}); code != 1 {
		t.Errorf("a bad value: exit %d", code)
	}
	// No install, and a machine that cannot run the units.
	if code := m.run([]string{"enable", "--dir", t.TempDir()}); code != 2 {
		t.Errorf("no install: exit %d", code)
	}
	f.root = false
	out.Reset()
	if code := m.run([]string{"enable", "--dir", dir}); code != 0 || !strings.Contains(out.String(), "not set up here") {
		t.Errorf("not root: exit %d %s", code, out)
	}
}

func TestMonitorEnableWithoutUnitsToChange(t *testing.T) {
	dir := installDir(t)
	m, out, _ := newTestMonitorCLI(newFakeMonitorSystem(), "cpu 1 1 1 1 1 1 1 1", func() time.Time { return monitorNow })
	m.o.monitorUnits = nil
	if code := m.run([]string{"enable", "--dir", dir}); code != 0 || !strings.Contains(out.String(), "next sudo bobres install") {
		t.Errorf("exit %d %s", code, out)
	}
}

func TestMonitorStatus(t *testing.T) {
	dir := installDir(t)
	now := func() time.Time { return monitorNow }
	f := newFakeMonitorSystem()
	m, out, _ := newTestMonitorCLI(f, "cpu 1 1 1 1 1 1 1 1", now)
	// Nothing installed.
	if code := m.run([]string{"status", "--dir", dir}); code != 1 || !strings.Contains(out.String(), "WARN") || !strings.Contains(out.String(), "not installed") {
		t.Fatalf("not installed: exit %d %s", code, out)
	}
	// Installed and running with a fresh snapshot.
	if err := ensureMonitorDirs(dir); err != nil {
		t.Fatal(err)
	}
	m.o.monitorUnits.ensureMonitor(context.Background(), monitorOptions{dir: dir, exe: testExe})
	if err := snapshot.Write(monitorDir(dir), &snapshot.Snapshot{Schema: 1, GeneratedAt: monitorNow.Add(-10 * time.Second).Unix(),
		GeneratedMS: monitorNow.Add(-10 * time.Second).UnixMilli(), Docker: &snapshot.Docker{OK: true, Version: "29.9.0"}}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := m.run([]string{"status", "--dir", dir}); code != 0 || !strings.Contains(out.String(), "PASS") || !strings.Contains(out.String(), "10s old") {
		t.Fatalf("fresh: exit %d %s", code, out)
	}
	out.Reset()
	if code := m.run([]string{"status", "--dir", dir, "--json"}); code != 0 {
		t.Fatalf("json: exit %d", code)
	}
	var st monitorStatus
	if err := json.Unmarshal(out.Bytes(), &st); err != nil || !st.Enabled || !st.UnitsInPlace || !st.TimerActive || !st.Snapshot || !st.Fresh || st.AgeS != 10 || st.Docker != "29.9.0" {
		t.Fatalf("status JSON: %v %+v", err, st)
	}
	// The service's last run failed.
	f.lastState = "exit-code"
	out.Reset()
	m.run([]string{"status", "--dir", dir})
	if !strings.Contains(out.String(), "ended with: exit-code") {
		t.Errorf("a failing service is not said: %s", out)
	}
	// Stale.
	m.now = func() time.Time { return monitorNow.Add(20 * time.Minute) }
	out.Reset()
	if code := m.run([]string{"status", "--dir", dir}); code != 1 || !strings.Contains(out.String(), "the last snapshot is 20m old") {
		t.Errorf("stale: exit %d %s", code, out)
	}
	if code := m.run([]string{"status", "--dir", t.TempDir()}); code != 2 {
		t.Errorf("no install: exit %d", code)
	}
}

func TestMonitorUsageAndSupported(t *testing.T) {
	m, out, errOut := newTestMonitorCLI(newFakeMonitorSystem(), "cpu 1 1 1 1 1 1 1 1", time.Now)
	if code := m.run(nil); code != 2 || !strings.Contains(errOut.String(), "usage: bobres monitor") {
		t.Errorf("no arguments: %d %s", code, errOut)
	}
	if code := m.run([]string{"nonsense"}); code != 2 {
		t.Errorf("nonsense: %d", code)
	}
	if code := m.run([]string{"supported"}); code != 0 || out.Len() != 0 {
		t.Errorf("supported: %d %q", code, out)
	}
	if code := m.run([]string{"help"}); code != 0 || !strings.Contains(out.String(), "snapshot") {
		t.Errorf("help: %d", code)
	}
	if code := run([]string{"monitor", "supported"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Errorf("through main: %d", code)
	}
}

// updaterAndMonitorInstall is an installer with the collector's units on a fake systemd (the updater's
// are left out: its own tests cover them).
func monitorInstall(t *testing.T, f *fakeMonitorSystem) (*installer, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ins, out, errOut := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	ins.monitorUnits = f.units()
	ins.exe = func() (string, error) { return testExe, nil }
	return ins, out, errOut
}

// The install makes the snapshot directory before the stack starts (Docker would make it as root) and
// puts the collector in place once the stack is up; a second run changes nothing, and a refusing
// systemd is a warning, never a failed install.
func TestInstallPutsHostMonitoringInPlace(t *testing.T) {
	dir := t.TempDir()
	f := newFakeMonitorSystem()
	ins, out, errOut := monitorInstall(t, f)
	if code := ins.install(append(baseArgs(dir), "--no-harden")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if f.files[unitServicePath] == "" || f.files[unitTimerPath] == "" || !f.enabled {
		t.Fatalf("units: %v", f.files)
	}
	if !strings.Contains(f.files[unitServicePath], "ExecStart="+testExe+" monitor snapshot --dir "+dir) {
		t.Errorf("the service:\n%s", f.files[unitServicePath])
	}
	if st, err := os.Stat(monitorDir(dir)); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("snapshot directory: %v %v", st, err)
	}
	if !strings.Contains(out.String(), monitorLabel) {
		t.Errorf("the install does not say it:\n%s", out)
	}

	// Once more: changes nothing and says nothing.
	f.cmds = nil
	ins2, out2, errOut2 := monitorInstall(t, f)
	if code := ins2.install(append(baseArgs(dir), "--no-harden")); code != 0 {
		t.Fatalf("second run exit %d: %s", code, errOut2)
	}
	if strings.Contains(out2.String(), monitorLabel) || f.ran("systemctl daemon-reload") != 0 || f.ran("systemctl enable") != 0 {
		t.Errorf("a second install changed it: %v\n%s", f.cmds, out2)
	}

	// systemd refusing is a warning; the install still finishes.
	bad := newFakeMonitorSystem()
	bad.fail["systemctl daemon-reload"] = errors.New("exit status 1")
	ins3, _, errOut3 := monitorInstall(t, bad)
	if code := ins3.install(append(baseArgs(t.TempDir()), "--no-harden")); code != 0 || !strings.Contains(errOut3.String(), "warning: host monitoring: not set up") {
		t.Errorf("a refusing systemd: exit %d\n%s", code, errOut3)
	}

	// The owner switched it off in .env: the install takes the units away and does not put them back.
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	env[monitorCollectorKey] = "off"
	if err := writeEnv(filepath.Join(dir, ".env"), env); err != nil {
		t.Fatal(err)
	}
	ins4, _, errOut4 := monitorInstall(t, f)
	if code := ins4.install(append(baseArgs(dir), "--no-harden", "--quiet", "--yes", "--skip-checks", "--offline")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut4)
	}
	if len(f.files) != 0 || f.enabled {
		t.Errorf("units left: %v", f.files)
	}
}

// Without a host to change (the installer of a test, or a machine that is not the server) the install
// still makes the directory core mounts, and writes the compose file that mounts it.
func TestInstallWithoutAHostStillMakesTheSnapshotDirectory(t *testing.T) {
	dir := t.TempDir()
	ins, _, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start", "--no-harden")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if st, err := os.Stat(monitorDir(dir)); err != nil || !st.IsDir() {
		t.Errorf("%v %v", st, err)
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if !strings.Contains(string(compose), "./run/monitor:/run/bobres-monitor:ro") {
		t.Error("the installed compose file does not mount the snapshot directory")
	}
}

func TestCheckHasAHostMonitorLine(t *testing.T) {
	c, out, _, dir, sc := newCheckTest(t)
	sc.check = coreAllGood
	// A backup, so that the only line that could fail is not the one under test.
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backups", "bobres-20261016T030000Z.bkp"), make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := c.run([]string{"--dir", dir}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// Nothing installed (and no systemd here): a line, never a failure.
	if !strings.Contains(out.String(), "SKIP  "+monitorLabel) {
		t.Fatalf("no Host monitor line:\n%s", out)
	}
	out.Reset()
	f := newFakeMonitorSystem()
	c.o.monitorUnits = f.units()
	if code := c.run([]string{"--dir", dir}); code != 0 || !strings.Contains(out.String(), "WARN  "+monitorLabel) || !strings.Contains(out.String(), "not installed") {
		t.Fatalf("units missing: exit %d\n%s", code, out)
	}
	out.Reset()
	if err := ensureMonitorDirs(dir); err != nil {
		t.Fatal(err)
	}
	f.files[unitServicePath], f.files[unitTimerPath] = "x", "x"
	now := c.o.clock()
	if err := snapshot.Write(monitorDir(dir), &snapshot.Snapshot{Schema: 1, GeneratedAt: now.Add(-3 * time.Second).Unix(), GeneratedMS: now.Add(-3 * time.Second).UnixMilli(),
		Docker: &snapshot.Docker{OK: true, Version: "29.9.0", Containers: containers(7)}}); err != nil {
		t.Fatal(err)
	}
	if code := c.run([]string{"--dir", dir}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out.String(), "PASS  "+monitorLabel) || !strings.Contains(out.String(), "docker 29.9.0 with 7 containers") {
		t.Errorf("a fresh snapshot is not passed:\n%s", out)
	}
}

// containers makes n valid container entries.
func containers(n int) []snapshot.Container {
	out := make([]snapshot.Container, n)
	for i := range out {
		out[i] = snapshot.Container{ID: fmt.Sprintf("%012x", i+1), Name: fmt.Sprintf("c%d", i), State: snapshot.StateRunning, Health: snapshot.HealthNone}
	}
	return out
}
