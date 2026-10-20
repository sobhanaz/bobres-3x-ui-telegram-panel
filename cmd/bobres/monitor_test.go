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

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/collect"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

var monitorNow = time.Unix(1_760_000_000, 0)

// fakeUnits plays systemd and the unit directory: the files in it, the
// systemctl calls in order, and what the timer's state is.
type fakeUnits struct {
	files     map[string]string
	modes     map[string]os.FileMode
	cmds      []string
	root      bool
	systemd   bool
	exe       string
	enabled   bool
	active    bool
	fail      map[string]error // a systemctl command line prefix -> its error
	writeErr  error
	lastState string
}

func newFakeUnits() *fakeUnits {
	return &fakeUnits{files: map[string]string{}, modes: map[string]os.FileMode{}, root: true, systemd: true,
		exe: "/usr/local/bin/bobres", fail: map[string]error{}, lastState: "success"}
}

func (f *fakeUnits) host() *monitorHost {
	return &monitorHost{
		unitDir: "/etc/systemd/system",
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			line := name + " " + strings.Join(args, " ")
			f.cmds = append(f.cmds, line)
			for p, err := range f.fail {
				if strings.HasPrefix(line, p) {
					return []byte("Failed to " + line + "\nsecond line"), err
				}
			}
			switch line {
			case "systemctl is-enabled " + monitorTimer:
				if !f.enabled {
					return []byte("disabled\n"), errors.New("exit status 1")
				}
			case "systemctl is-active " + monitorTimer:
				if !f.active {
					return []byte("inactive\n"), errors.New("exit status 3")
				}
			case "systemctl enable --now " + monitorTimer:
				f.enabled, f.active = true, true
			case "systemctl disable --now " + monitorTimer, "systemctl stop " + monitorService:
				f.enabled, f.active = false, false
			case "systemctl show -p Result --value " + monitorService:
				return []byte(f.lastState + "\n"), nil
			}
			return nil, nil
		},
		readFile: func(p string) ([]byte, error) {
			if s, ok := f.files[p]; ok {
				return []byte(s), nil
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
		remove: func(p string) error { delete(f.files, p); return nil },
		exists: func(p string) bool { _, ok := f.files[p]; return ok },
		euid: func() int {
			if f.root {
				return 0
			}
			return 1000
		},
		systemd: func() bool { return f.systemd },
		exe:     func() (string, error) { return f.exe, nil },
	}
}

func (f *fakeUnits) ran(prefix string) int {
	n := 0
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

const unitService = "/etc/systemd/system/" + monitorService
const unitTimer = "/etc/systemd/system/" + monitorTimer

func TestMonitorServiceUnit(t *testing.T) {
	got, err := renderMonitorService("/usr/local/bin/bobres", "/opt/bobres")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"\nUser=root\n",
		"\nType=oneshot\n",
		"\nExecStart=/usr/local/bin/bobres monitor snapshot --dir /opt/bobres\n",
		// An older bobres (a rollback) does not know the command: its refusal skips the run, it does not fail.
		"\nExecCondition=/usr/local/bin/bobres monitor supported\n",
		// The install gone: skipped quietly.
		"\nConditionPathIsDirectory=/opt/bobres/run/monitor\n",
		// It may write the snapshot directory and nothing else.
		"\nProtectSystem=strict\n", "\nReadWritePaths=/opt/bobres/run/monitor\n", "\nNoNewPrivileges=yes\n", "\nPrivateTmp=yes\n",
		"\nProtectHome=read-only\n", "\nCPUQuota=25%\n", "\nMemoryMax=192M\n", "\nTimeoutStartSec=30\n", "\nUMask=0022\n",
		"\nRestrictSUIDSGID=yes\n", "\nLockPersonality=yes\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the service lacks %q:\n%s", want, got)
		}
	}
	// Nothing in it is a secret or the install's .env.
	if strings.Contains(got, ".env") || strings.Contains(got, "Environment") || strings.Contains(got, "EnvironmentFile") {
		t.Errorf("the unit reads the environment:\n%s", got)
	}
	if !strings.Contains(monitorTimerUnit, "OnUnitActiveSec=30s") || !strings.Contains(monitorTimerUnit, "WantedBy=timers.target") ||
		!strings.Contains(monitorTimerUnit, "OnBootSec=20s") {
		t.Errorf("the timer:\n%s", monitorTimerUnit)
	}
}

func TestUnitArgumentsAreQuotedForSystemd(t *testing.T) {
	for in, want := range map[string]string{
		"/opt/bobres":          "/opt/bobres",
		"/srv/my store":        `"/srv/my store"`,
		`/srv/we"ird`:          `"/srv/we\"ird"`,
		"/srv/50%":             `"/srv/50%%"`,
		"/srv/$HOME":           `"/srv/$$HOME"`,
		`/srv/back\slash`:      `"/srv/back\\slash"`,
		"/srv/it's":            `"/srv/it's"`,
		"":                     `""`,
	} {
		got, err := unitArg(in)
		if err != nil || got != want {
			t.Errorf("unitArg(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/srv/a\nb", "/srv/a\x00b", "/srv/a\rb"} {
		if _, err := unitArg(bad); err == nil {
			t.Errorf("unitArg(%q) was accepted", bad)
		}
		if _, err := unitPath(bad); err == nil {
			t.Errorf("unitPath(%q) was accepted", bad)
		}
	}
	if got, err := unitPath("/srv/my 100% store"); err != nil || got != `/srv/my\x20100%%\x20store` {
		t.Errorf("unitPath: %q %v", got, err)
	}
	if _, err := unitPath("relative"); err == nil {
		t.Error("a relative path")
	}
	if _, err := renderMonitorService("/usr/local/bin/bobres", "relative"); err == nil {
		t.Error("a relative install directory")
	}
	svc, err := renderMonitorService("/opt/my tools/bobres", "/srv/my store")
	if err != nil || !strings.Contains(svc, `ExecStart="/opt/my tools/bobres" monitor snapshot --dir "/srv/my store"`) ||
		!strings.Contains(svc, `ReadWritePaths=/srv/my\x20store/run/monitor`) {
		t.Errorf("%v\n%s", err, svc)
	}
}

func TestEnsureMakesTheDirectoryAndPutsTheUnitsInPlace(t *testing.T) {
	dir := t.TempDir()
	f := newFakeUnits()
	got := f.host().ensure(context.Background(), dir, map[string]string{})
	if got.State != "installed" || got.Err != nil {
		t.Fatalf("%+v", got)
	}
	st, err := os.Stat(monitorDir(dir))
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
		t.Fatalf("the snapshot directory: %v %v", st, err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "run")); st == nil || st.Mode().Perm() != 0o755 {
		t.Errorf("run/: %v", st)
	}
	svc, _ := renderMonitorService(f.exe, dir)
	if f.files[unitService] != svc || f.files[unitTimer] != monitorTimerUnit || f.modes[unitService] != 0o644 || f.modes[unitTimer] != 0o644 {
		t.Errorf("unit files: %v %v", f.files, f.modes)
	}
	want := []string{
		"systemctl daemon-reload", "systemctl is-enabled " + monitorTimer, "systemctl is-active " + monitorTimer,
		"systemctl enable --now " + monitorTimer, "systemctl start --no-block " + monitorService,
	}
	if strings.Join(f.cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("systemctl calls:\n%s\nwant\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestEnsureTwiceChangesNothingTheSecondTime(t *testing.T) {
	dir := t.TempDir()
	f := newFakeUnits()
	h := f.host()
	h.ensure(context.Background(), dir, nil)
	f.cmds = nil
	writes := 0
	inner := h.writeFile
	h.writeFile = func(p string, b []byte, m os.FileMode) error { writes++; return inner(p, b, m) }
	got := h.ensure(context.Background(), dir, nil)
	if got.State != "unchanged" || writes != 0 {
		t.Fatalf("%+v, %d writes", got, writes)
	}
	// Only the two questions: nothing is reloaded, enabled, restarted or started.
	if len(f.cmds) != 2 || f.ran("systemctl is-enabled") != 1 || f.ran("systemctl is-active") != 1 {
		t.Errorf("calls: %v", f.cmds)
	}
}

func TestEnsureFollowsAMovedBinaryAndRestartsTheTimer(t *testing.T) {
	dir := t.TempDir()
	f := newFakeUnits()
	h := f.host()
	h.ensure(context.Background(), dir, nil)
	f.cmds = nil
	f.exe = "/opt/bobres-bin/bobres"
	got := h.ensure(context.Background(), dir, nil)
	if got.State != "updated" || !strings.Contains(f.files[unitService], "ExecStart=/opt/bobres-bin/bobres monitor snapshot") {
		t.Fatalf("%+v\n%s", got, f.files[unitService])
	}
	// Only the service changed; the timer's text is as it was.
	if f.ran("systemctl daemon-reload") != 1 || f.ran("systemctl restart "+monitorTimer) != 1 || f.ran("systemctl start --no-block") != 1 {
		t.Errorf("calls: %v", f.cmds)
	}
}

func TestEnsureTurnsAStoppedTimerBackOn(t *testing.T) {
	dir := t.TempDir()
	f := newFakeUnits()
	h := f.host()
	h.ensure(context.Background(), dir, nil)
	f.cmds, f.enabled, f.active = nil, false, false // someone ran systemctl disable --now
	got := h.ensure(context.Background(), dir, nil)
	if got.State != "unchanged" || f.ran("systemctl enable --now "+monitorTimer) != 1 || f.ran("systemctl daemon-reload") != 0 || !f.enabled || !f.active {
		t.Errorf("%+v %v", got, f.cmds)
	}
}

func TestEnsureNeedsRootAndSystemdButStillMakesTheDirectory(t *testing.T) {
	for name, tc := range map[string]func(*fakeUnits){
		"not root":   func(f *fakeUnits) { f.root = false },
		"no systemd": func(f *fakeUnits) { f.systemd = false },
	} {
		dir := t.TempDir()
		f := newFakeUnits()
		tc(f)
		got := f.host().ensure(context.Background(), dir, nil)
		if got.State != "skipped" || got.Why == "" || got.Err != nil || len(f.cmds) != 0 || len(f.files) != 0 {
			t.Errorf("%s: %+v %v %v", name, got, f.cmds, f.files)
		}
		if _, err := os.Stat(monitorDir(dir)); err != nil {
			t.Errorf("%s: the directory core mounts was not made: %v", name, err)
		}
	}
}

func TestEnsureWhenTurnedOffTakesTheUnitsAway(t *testing.T) {
	dir := t.TempDir()
	f := newFakeUnits()
	h := f.host()
	h.ensure(context.Background(), dir, nil)
	if err := snapshot.Write(monitorDir(dir), &snapshot.Snapshot{Schema: 1, GeneratedAt: monitorNow.Unix(), GeneratedMS: monitorNow.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	f.cmds = nil
	off := map[string]string{monitorCollectorKey: "OFF"}
	got := h.ensure(context.Background(), dir, off)
	if got.State != "removed" || len(f.files) != 0 || snapshot.Exists(monitorDir(dir)) {
		t.Fatalf("%+v files %v", got, f.files)
	}
	want := []string{"systemctl disable --now " + monitorTimer, "systemctl stop " + monitorService, "systemctl daemon-reload"}
	if strings.Join(f.cmds, "|") != strings.Join(want, "|") {
		t.Errorf("calls: %v", f.cmds)
	}
	// Again: nothing left to do, nothing run.
	f.cmds = nil
	if got := h.ensure(context.Background(), dir, off); got.State != "off" || len(f.cmds) != 0 {
		t.Errorf("%+v %v", got, f.cmds)
	}
	if _, err := os.Stat(monitorDir(dir)); err != nil {
		t.Errorf("the directory core mounts must stay: %v", err)
	}
}

func TestEnsureReportsWhatSystemdRefuses(t *testing.T) {
	dir := t.TempDir()
	f := newFakeUnits()
	f.fail["systemctl daemon-reload"] = errors.New("exit status 1")
	got := newFakeUnitsHost(f).ensure(context.Background(), dir, nil)
	if got.Err == nil || !strings.Contains(got.Err.Error(), "daemon-reload") || !strings.Contains(got.Err.Error(), "Failed to systemctl daemon-reload") ||
		strings.Contains(got.Err.Error(), "second line") {
		t.Errorf("%+v", got)
	}
	f = newFakeUnits()
	f.fail["systemctl enable"] = errors.New("exit status 1")
	if got := f.host().ensure(context.Background(), t.TempDir(), nil); got.Err == nil || !strings.Contains(got.Err.Error(), "enable --now") {
		t.Errorf("enable: %+v", got)
	}
	f = newFakeUnits()
	f.writeErr = errors.New("read-only file system")
	if got := f.host().ensure(context.Background(), t.TempDir(), nil); got.Err == nil || !strings.Contains(got.Err.Error(), "read-only") {
		t.Errorf("write: %+v", got)
	}
}

func newFakeUnitsHost(f *fakeUnits) *monitorHost { return f.host() }

func TestEnsureRefusesALinkedRunDirectory(t *testing.T) {
	dir := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(dir, "run")); err != nil {
		t.Fatal(err)
	}
	got := newFakeUnits().host().ensure(context.Background(), dir, nil)
	if got.Err == nil || !strings.Contains(got.Err.Error(), "symbolic link") {
		t.Fatalf("%+v", got)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("something was written through the link: %v", entries)
	}
}

func TestMonitorVerdict(t *testing.T) {
	fresh := &snapshot.Snapshot{GeneratedAt: monitorNow.Add(-12 * time.Second).Unix(), Docker: &snapshot.Docker{OK: true, Version: "29.9.0",
		Containers: make([]snapshot.Container, 9)}}
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
		if r.Name != "Host monitor" || r.Status != tc.status || !strings.Contains(r.Detail, tc.want) {
			t.Errorf("%s: %+v, want %s containing %q", name, r, tc.status, tc.want)
		}
		if r.Status == Fail {
			t.Errorf("%s: host monitoring is optional and never a FAIL", name)
		}
	}
}

// collectorFor gives the snapshot command a machine of its own: a few /proc
// files and no engine, no systemd.
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

func newTestMonitorCLI(dir string, f *fakeUnits, stat string, now func() time.Time) (*monitorCLI, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &monitorCLI{out: &out, errOut: &errOut, host: f.host(), now: now, collector: collectorFor(stat, now)}, &out, &errOut
}

func TestMonitorSnapshotWritesTheFileAndComputesTheCPUShare(t *testing.T) {
	dir := installDir(t)
	if err := ensureMonitorDirs(dir); err != nil {
		t.Fatal(err)
	}
	clock := monitorNow
	now := func() time.Time { return clock }
	m, out, errOut := newTestMonitorCLI(dir, newFakeUnits(), "cpu  100 0 100 800 0 0 0 0 0 0", now)
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
	m, _, _ = newTestMonitorCLI(dir, newFakeUnits(), "cpu  125 0 125 850 0 0 0 0 0 0", now)
	if code := m.run([]string{"snapshot", "--dir", dir}); code != 0 {
		t.Fatalf("second run exit %d", code)
	}
	second, err := snapshot.Read(monitorDir(dir), clock)
	if err != nil || second.Host.CPU.Pct == nil || *second.Host.CPU.Pct != 50 {
		t.Fatalf("second snapshot: %v %+v", err, second.Host)
	}
}

func TestMonitorSnapshotPrintAndOut(t *testing.T) {
	dir := installDir(t)
	now := func() time.Time { return monitorNow }
	m, out, _ := newTestMonitorCLI(dir, newFakeUnits(), "cpu  1 1 1 1 1 1 1 1 1 1", now)
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
	m, _, _ = newTestMonitorCLI(dir, newFakeUnits(), "cpu  1 1 1 1 1 1 1 1 1 1", now)
	if code := m.run([]string{"snapshot", "--dir", dir, "--out", custom}); code != 0 || !snapshot.Exists(custom) {
		t.Fatalf("--out: exit %d", code)
	}
	// A directory that does not exist is an error, not a reason to make one somewhere unexpected.
	m, _, errOut := newTestMonitorCLI(dir, newFakeUnits(), "cpu  1 1 1 1 1 1 1 1 1 1", now)
	if code := m.run([]string{"snapshot", "--dir", dir, "--out", filepath.Join(custom, "missing", "deeper")}); code != 1 || errOut.Len() == 0 {
		t.Errorf("a missing output directory: exit %d %q", code, errOut)
	}
	for _, args := range [][]string{{"snapshot", "--bogus"}, {"snapshot", "extra"}, {"snapshot", "--timeout", "0s"}} {
		m, _, _ = newTestMonitorCLI(dir, newFakeUnits(), "cpu  1 1 1 1 1 1 1 1 1 1", now)
		if code := m.run(args); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestMonitorEnableAndDisableFollowTheEnvFile(t *testing.T) {
	dir := t.TempDir()
	env := checkEnvMap()
	env["OPERATOR_NOTE"] = "keep me"
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFakeUnits()
	m, out, errOut := newTestMonitorCLI(dir, f, "cpu 1 1 1 1 1 1 1 1", func() time.Time { return monitorNow })
	if code := m.run([]string{"enable", "--dir", dir}); code != 0 || f.files[unitTimer] == "" {
		t.Fatalf("enable: exit %d %s %s", code, out, errOut)
	}
	if !strings.Contains(out.String(), "Host monitoring is on") || !strings.Contains(out.String(), "sudo bobres monitor disable") {
		t.Errorf("enable output: %s", out)
	}
	out.Reset()
	if code := m.run([]string{"disable", "--dir", dir}); code != 0 || len(f.files) != 0 {
		t.Fatalf("disable: exit %d %s %s", code, out, errOut)
	}
	if !strings.Contains(out.String(), "Host monitoring is off") {
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
	if o := f.host().ensure(context.Background(), dir, got); o.State != "off" || len(f.files) != 0 {
		t.Errorf("ensure after disable: %+v", o)
	}
	out.Reset()
	if code := m.run([]string{"enable", "--dir", dir}); code != 0 || f.files[unitService] == "" {
		t.Fatalf("enable again: exit %d", code)
	}
	again, _, _ := readEnv(filepath.Join(dir, ".env"))
	if _, ok := again[monitorCollectorKey]; ok {
		t.Errorf("enable left %s in .env", monitorCollectorKey)
	}
	// No install, and a machine that cannot run the units.
	if code := m.run([]string{"enable", "--dir", t.TempDir()}); code != 2 {
		t.Errorf("no install: exit %d", code)
	}
	f.root = false
	if code := m.run([]string{"enable", "--dir", dir}); code != 1 || !strings.Contains(out.String(), "not put in place") {
		t.Errorf("not root: exit %d %s", code, out)
	}
}

func TestMonitorStatus(t *testing.T) {
	dir := installDir(t)
	now := func() time.Time { return monitorNow }
	f := newFakeUnits()
	m, out, _ := newTestMonitorCLI(dir, f, "cpu 1 1 1 1 1 1 1 1", now)
	// Nothing installed.
	if code := m.run([]string{"status", "--dir", dir}); code != 1 || !strings.Contains(out.String(), "WARN") || !strings.Contains(out.String(), "not installed") {
		t.Fatalf("not installed: exit %d %s", code, out)
	}
	// Installed and running with a fresh snapshot.
	m.host.ensure(context.Background(), dir, nil)
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
	m, out, errOut := newTestMonitorCLI(t.TempDir(), newFakeUnits(), "cpu 1 1 1 1 1 1 1 1", time.Now)
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

// The install step puts the collector in place and says so; a failure there
// is a warning, never a reason to stop.
func TestInstallPutsHostMonitoringInPlace(t *testing.T) {
	dir := t.TempDir()
	f := newFakeUnits()
	ins, out, errOut := testInstaller("", secrets, &fakeDocker{health: []string{
		`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`}})
	ins.monitor = f.host()
	if code := ins.install(append(baseArgs(dir), "--no-harden")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if f.files[unitService] == "" || f.files[unitTimer] == "" {
		t.Fatalf("units: %v", f.files)
	}
	if st, err := os.Stat(monitorDir(dir)); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("snapshot directory: %v %v", st, err)
	}
	if !strings.Contains(out.String(), "Host monitoring: the snapshot timer installed") || !strings.Contains(out.String(), "sudo bobres monitor disable") {
		t.Errorf("the install does not say it:\n%s", out)
	}
	// The directory exists before the stack starts: Docker would make it as root otherwise.
	// A second run changes nothing and says nothing.
	out.Reset()
	f.cmds = nil
	ins2, out2, errOut2 := testInstaller("", secrets, &fakeDocker{health: []string{`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`}})
	ins2.monitor = f.host()
	if code := ins2.install(append(baseArgs(dir), "--no-harden")); code != 0 {
		t.Fatalf("second run exit %d: %s", code, errOut2)
	}
	if strings.Contains(out2.String(), "Host monitoring") || f.ran("systemctl daemon-reload") != 0 || f.ran("systemctl enable") != 0 {
		t.Errorf("a second install changed it: %v\n%s", f.cmds, out2)
	}

	// systemd refusing is a warning; the install still finishes.
	dir3 := t.TempDir()
	bad := newFakeUnits()
	bad.fail["systemctl daemon-reload"] = errors.New("exit status 1")
	ins3, _, errOut3 := testInstaller("", secrets, &fakeDocker{health: []string{`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`}})
	ins3.monitor = bad.host()
	if code := ins3.install(append(baseArgs(dir3), "--no-harden")); code != 0 || !strings.Contains(errOut3.String(), "warning: host monitoring was not put in place") {
		t.Errorf("a refusing systemd: exit %d\n%s", code, errOut3)
	}
}

// With no host to change (the tests', or a machine that is not the server) the
// install still makes the directory core mounts, so Docker does not.
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
	// A recent backup, so the Backup line does not fail the whole check.
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backups", "bobres-20261016T030000Z.bkp"), make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := c.run([]string{"--dir", dir}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// Nothing installed: a warning (not a failure), saying what to do.
	if !strings.Contains(out.String(), "WARN  Host monitor") && !strings.Contains(out.String(), "SKIP  Host monitor") {
		t.Fatalf("no Host monitor line:\n%s", out)
	}
	out.Reset()
	if err := ensureMonitorDirs(dir); err != nil {
		t.Fatal(err)
	}
	now := c.o.clock()
	// Containers that pass the snapshot's own checks (a 12 hex id and a name); anything else is dropped on write.
	var cs []snapshot.Container
	for i := 0; i < 7; i++ {
		cs = append(cs, snapshot.Container{ID: fmt.Sprintf("%012x", i+1), Name: fmt.Sprintf("bobres-svc-%d", i)})
	}
	if err := snapshot.Write(monitorDir(dir), &snapshot.Snapshot{Schema: 1, GeneratedAt: now.Add(-3 * time.Second).Unix(), GeneratedMS: now.Add(-3 * time.Second).UnixMilli(),
		Docker: &snapshot.Docker{OK: true, Version: "29.9.0", Containers: cs}}); err != nil {
		t.Fatal(err)
	}
	if code := c.run([]string{"--dir", dir}); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out.String(), "PASS  Host monitor") || !strings.Contains(out.String(), "docker 29.9.0 with 7 containers") {
		t.Errorf("a fresh snapshot is not passed:\n%s", out)
	}
}

func TestUninstallTakesTheMonitoringUnitsAway(t *testing.T) {
	dir := installDir(t)
	f := newFakeUnits()
	h := f.host()
	h.ensure(context.Background(), dir, nil)
	o, _, errOut := testOps("y\n", &fakeOps{})
	o.monitor = h
	if code := o.uninstallCmd([]string{"--dir", dir}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if len(f.files) != 0 || f.ran("systemctl disable --now") != 1 {
		t.Errorf("units left: %v, calls %v", f.files, f.cmds)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Errorf("uninstall without --purge must keep the install directory: %v", err)
	}
}
