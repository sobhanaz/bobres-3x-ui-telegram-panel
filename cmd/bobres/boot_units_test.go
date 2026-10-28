package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
)

const bootUnitPath = systemUnitDir + "/" + bootServiceUnit

var testBootOpts = bootOptions{dir: "/opt/bobres", exe: testExe}

// fakeBootSystem plays a server's systemd for the boot unit: its unit file, what systemctl says of
// it, and every program run, in order. Nothing touches this machine.
type fakeBootSystem struct {
	files    map[string]string
	modes    map[string]os.FileMode
	extra    map[string]bool // other paths that exist: the bobres binary, the stopped record
	cmds     []string
	root     bool
	systemd  bool
	enabled  bool
	active   string // active, inactive or failed
	fail     map[string]error
	writeErr error
}

func newFakeBootSystem() *fakeBootSystem {
	return &fakeBootSystem{files: map[string]string{}, modes: map[string]os.FileMode{}, extra: map[string]bool{testExe: true},
		root: true, systemd: true, active: "inactive", fail: map[string]error{}}
}

func (f *fakeBootSystem) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.cmds = append(f.cmds, line)
	for prefix, err := range f.fail {
		if strings.HasPrefix(line, prefix) {
			return []byte("Failed: " + line + "\nmore"), err
		}
	}
	_, installed := f.files[bootUnitPath]
	switch {
	case line == "systemctl enable "+bootServiceUnit:
		if !installed {
			return []byte("Failed to enable unit: Unit file does not exist."), errors.New("exit status 1")
		}
		f.enabled = true
	case line == "systemctl disable "+bootServiceUnit:
		f.enabled = false
	case strings.HasPrefix(line, "systemctl reset-failed"):
		if f.active == "failed" {
			f.active = "inactive"
		}
	case line == "systemctl is-enabled "+bootServiceUnit:
		switch {
		case !installed:
			return []byte("Failed to get unit file state\n"), errors.New("exit status 1")
		case f.enabled:
			return []byte("enabled\n"), nil
		}
		return []byte("disabled\n"), errors.New("exit status 1")
	case line == "systemctl is-active "+bootServiceUnit:
		if f.active == "active" {
			return []byte("active\n"), nil
		}
		return []byte(f.active + "\n"), errors.New("exit status 3")
	}
	return nil, nil
}

func (f *fakeBootSystem) units() *systemUnits {
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
			return ok || f.extra[p]
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

func (f *fakeBootSystem) ran(prefix string) int {
	n := 0
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestRenderBootUnit(t *testing.T) {
	text, err := renderBootUnit(testBootOpts)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// It runs the command an operator types, once Docker is up.
		"\nExecStart=/usr/local/bin/bobres start --dir /opt/bobres\n", "\nType=oneshot\n", "\nRemainAfterExit=yes\n", "\nUser=root\n",
		"\nRequires=docker.service\n", "\nAfter=docker.service network-online.target\n", "\nWants=network-online.target\n",
		// The install gone: skipped quietly. A store stopped on purpose: left stopped.
		"\nConditionPathIsDirectory=/opt/bobres\n", "\nConditionPathExists=!/opt/bobres/run/stopped\n",
		// A start waits for slow health checks, but not for ever.
		"\nTimeoutStartSec=15min\n", "\nWantedBy=multi-user.target\n",
		"\nNoNewPrivileges=yes\n", "\nPrivateTmp=yes\n", "\nLockPersonality=yes\n", "\nRestrictSUIDSGID=yes\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the unit lacks %q:\n%s", want, text)
		}
	}
	for _, ph := range []string{"@DIR@", "@EXE@"} {
		if strings.Contains(text, ph) {
			t.Errorf("%s was left:\n%s", ph, text)
		}
	}
	// It reads no environment and no .env: nothing secret can reach it. It runs one program.
	if strings.Contains(text, "EnvironmentFile") || strings.Contains(text, "\nEnvironment=") || strings.Contains(text, "/.env") {
		t.Errorf("the unit reads the environment:\n%s", text)
	}
	if strings.Count(text, "ExecStart=") != 1 || strings.Contains(text, "ExecStop") || strings.Contains(text, "Restart=") {
		t.Errorf("the unit does more than start the store:\n%s", text)
	}
	// The record `bobres stop` leaves is the file the condition reads.
	if want := stoppedPath("/opt/bobres"); !strings.Contains(text, "ConditionPathExists=!"+want+"\n") {
		t.Errorf("the condition does not read %s:\n%s", want, text)
	}
	// An install elsewhere, and a bobres elsewhere.
	other, err := renderBootUnit(bootOptions{dir: "/srv/bob-res_1.0", exe: "/opt/bin/bobres"})
	if err != nil || !strings.Contains(other, "ExecStart=/opt/bin/bobres start --dir /srv/bob-res_1.0\n") ||
		!strings.Contains(other, "ConditionPathExists=!/srv/bob-res_1.0/run/stopped\n") {
		t.Errorf("another place: %v\n%s", err, other)
	}
	// A path systemd could read as something else is refused, not escaped.
	for _, bad := range []bootOptions{{dir: "/srv/my store", exe: testExe}, {dir: "/opt/bobres", exe: "/opt/my tools/bobres"},
		{dir: "relative", exe: testExe}, {dir: "/opt/bobres", exe: "/usr/bin/bob%res"}, {dir: "/opt/$HOME", exe: testExe}, {dir: "", exe: testExe}} {
		if _, err := renderBootUnit(bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	// The template is the file in deploy/systemd, embedded.
	if len(deploy.BootServiceUnit) == 0 {
		t.Error("the unit template is not embedded")
	}
}

func TestEnsureBootPutsTheUnitInPlace(t *testing.T) {
	f := newFakeBootSystem()
	res := f.units().ensureBoot(context.Background(), testBootOpts)
	if res.kind != markOK || !res.changed || !strings.Contains(res.text, bootServiceUnit+" is on") {
		t.Fatalf("%+v", res)
	}
	text, _ := renderBootUnit(testBootOpts)
	if f.files[bootUnitPath] != text || f.modes[bootUnitPath] != 0o644 || !f.enabled {
		t.Errorf("unit file %q mode %v enabled %v", f.files[bootUnitPath], f.modes[bootUnitPath], f.enabled)
	}
	// It is for the next boot: nothing is started now (the store is up when this runs).
	want := []string{"systemctl daemon-reload", "systemctl enable " + bootServiceUnit, "systemctl is-enabled " + bootServiceUnit}
	if strings.Join(f.cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("systemctl calls:\n%s\nwant\n%s", strings.Join(f.cmds, "\n"), strings.Join(want, "\n"))
	}
}

func TestEnsureBootTwiceChangesNothingTheSecondTime(t *testing.T) {
	f := newFakeBootSystem()
	u := f.units()
	u.ensureBoot(context.Background(), testBootOpts)
	f.cmds = nil
	writes := 0
	inner := u.writeFile
	u.writeFile = func(p string, b []byte, m os.FileMode) error { writes++; return inner(p, b, m) }
	res := u.ensureBoot(context.Background(), testBootOpts)
	if res.kind != markOK || res.changed || res.text != "" || writes != 0 {
		t.Fatalf("%+v, %d writes", res, writes)
	}
	for _, c := range f.cmds {
		if !strings.HasPrefix(c, "systemctl is-") {
			t.Errorf("a second run did %q", c)
		}
	}
}

func TestEnsureBootFollowsAMovedBinary(t *testing.T) {
	f := newFakeBootSystem()
	u := f.units()
	u.ensureBoot(context.Background(), testBootOpts)
	f.cmds = nil
	res := u.ensureBoot(context.Background(), bootOptions{dir: "/opt/bobres", exe: "/opt/bin/bobres"})
	if res.kind != markOK || !res.changed || !strings.Contains(f.files[bootUnitPath], "ExecStart=/opt/bin/bobres start") {
		t.Fatalf("%+v\n%s", res, f.files[bootUnitPath])
	}
	if f.ran("systemctl daemon-reload") != 1 || f.ran("systemctl start") != 0 || f.ran("systemctl restart") != 0 {
		t.Errorf("calls: %v", f.cmds)
	}
}

func TestEnsureBootTurnsADisabledUnitBackOn(t *testing.T) {
	f := newFakeBootSystem()
	u := f.units()
	u.ensureBoot(context.Background(), testBootOpts)
	f.cmds, f.enabled = nil, false // someone ran systemctl disable
	res := u.ensureBoot(context.Background(), testBootOpts)
	if res.kind != markOK || f.ran("systemctl enable "+bootServiceUnit) != 1 || f.ran("systemctl daemon-reload") != 0 || !f.enabled {
		t.Errorf("%+v %v", res, f.cmds)
	}
}

func TestEnsureBootNeedsRootAndSystemd(t *testing.T) {
	for name, tc := range map[string]func(*fakeBootSystem, *systemUnits){
		"not root":   func(f *fakeBootSystem, _ *systemUnits) { f.root = false },
		"no systemd": func(f *fakeBootSystem, _ *systemUnits) { f.systemd = false },
		"not linux":  func(_ *fakeBootSystem, u *systemUnits) { u.goos = "darwin" },
	} {
		f := newFakeBootSystem()
		u := f.units()
		tc(f, u)
		res := u.ensureBoot(context.Background(), testBootOpts)
		if res.kind != markSkip || !strings.HasPrefix(res.text, "not set up: ") || len(f.cmds) != 0 || len(f.files) != 0 {
			t.Errorf("%s: %+v %v %v", name, res, f.cmds, f.files)
		}
	}
}

func TestEnsureBootWhenTurnedOffTakesTheUnitAway(t *testing.T) {
	f := newFakeBootSystem()
	u := f.units()
	u.ensureBoot(context.Background(), testBootOpts)
	f.cmds = nil
	opts := testBootOpts
	opts.off = true
	res := u.ensureBoot(context.Background(), opts)
	if res.kind != markOK || !res.changed || len(f.files) != 0 || f.enabled {
		t.Fatalf("%+v files %v", res, f.files)
	}
	// A run that is starting the store now is not cut off: the unit is disabled, not stopped.
	if f.ran("systemctl disable "+bootServiceUnit) != 1 || f.ran("systemctl daemon-reload") != 1 || f.ran("systemctl stop") != 0 {
		t.Errorf("calls: %v", f.cmds)
	}
	// Again: nothing left to do, nothing run.
	f.cmds = nil
	if res := u.ensureBoot(context.Background(), opts); res.changed || len(f.cmds) != 0 {
		t.Errorf("%+v %v", res, f.cmds)
	}
}

func TestEnsureBootReportsWhatSystemdRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		prefix string
		want   string
	}{
		"daemon-reload": {"systemctl daemon-reload", "daemon-reload"},
		"enable":        {"systemctl enable", "systemctl enable"},
	} {
		f := newFakeBootSystem()
		f.fail[tc.prefix] = errors.New("exit status 1")
		res := f.units().ensureBoot(context.Background(), testBootOpts)
		if res.kind != markWarn || !strings.Contains(res.text, tc.want) || strings.Contains(res.text, "more") {
			t.Errorf("%s: %+v", name, res)
		}
	}
	f := newFakeBootSystem()
	f.writeErr = errors.New("read-only file system")
	if res := f.units().ensureBoot(context.Background(), testBootOpts); res.kind != markWarn || !strings.Contains(res.text, "read-only") {
		t.Errorf("write: %+v", res)
	}
	f = newFakeBootSystem()
	if res := f.units().ensureBoot(context.Background(), bootOptions{dir: "/srv/my store", exe: testExe}); res.kind != markWarn || len(f.files) != 0 {
		t.Errorf("an unsafe path: %+v", res)
	}
}

func TestBootReport(t *testing.T) {
	const dir = "/opt/bobres"
	set := func(mut func(*fakeBootSystem)) *fakeBootSystem {
		f := newFakeBootSystem()
		f.units().ensureBoot(context.Background(), testBootOpts)
		f.active = "active"
		mut(f)
		return f
	}
	for name, tc := range map[string]struct {
		f    *fakeBootSystem
		off  bool
		kind Status
		want string
	}{
		"on":                {set(func(*fakeBootSystem) {}), false, Pass, "starts the store at every boot"},
		"never started yet": {set(func(f *fakeBootSystem) { f.active = "inactive" }), false, Pass, "starts the store at every boot"},
		"stopped on purpose": {set(func(f *fakeBootSystem) { f.extra[stoppedPath(dir)] = true }), false, Pass,
			"stopped on purpose"},
		"not installed": {newFakeBootSystem(), false, Warn, "not installed"},
		"disabled":      {set(func(f *fakeBootSystem) { f.enabled = false }), false, Warn, "not enabled"},
		"failed":        {set(func(f *fakeBootSystem) { f.active = "failed" }), false, Warn, "journalctl -u bobres.service"},
		"binary gone":   {set(func(f *fakeBootSystem) { delete(f.extra, testExe) }), false, Warn, "which is not there"},
		"off":           {newFakeBootSystem(), true, Pass, "BOBRES_START_AT_BOOT says"},
		"off, installed": {set(func(*fakeBootSystem) {}), true, Warn,
			"still installed"},
	} {
		r := tc.f.units().bootReport(context.Background(), dir, tc.off)
		if r.Name != bootLabel || r.Status != tc.kind || !strings.Contains(r.Detail, tc.want) {
			t.Errorf("%s: %+v", name, r)
		}
	}
	f := newFakeBootSystem()
	f.systemd = false
	if r := f.units().bootReport(context.Background(), dir, false); r.Status != Skip || !strings.Contains(r.Detail, "needs systemd") {
		t.Errorf("no systemd: %+v", r)
	}
}

// The record that a store was stopped on purpose is a file in run/ that the boot unit's condition
// reads: stop and a plain uninstall leave it, every command that starts the store takes it away.
func TestStoppedRecord(t *testing.T) {
	dir := t.TempDir()
	if err := clearStopped(dir); err != nil {
		t.Fatalf("clearing what is not there: %v", err)
	}
	if err := markStopped(dir); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(stoppedPath(dir))
	if err != nil || st.Mode().Perm() != 0o644 || stoppedPath(dir) != filepath.Join(dir, "run", "stopped") {
		t.Fatalf("%v %v", st, err)
	}
	if b, _ := os.ReadFile(stoppedPath(dir)); !strings.Contains(string(b), "sudo bobres start") {
		t.Errorf("the record does not say how to start the store:\n%s", b)
	}
	if err := markStopped(dir); err != nil { // again
		t.Fatal(err)
	}
	if err := clearStopped(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stoppedPath(dir)); !os.IsNotExist(err) {
		t.Errorf("the record is still there: %v", err)
	}
}

func TestStopStartAndRestartKeepTheRecord(t *testing.T) {
	f := &fakeOps{ps: healthyPS}
	o, out, errOut := testOps("", f)
	dir := installDir(t)
	if code := o.stopCmd([]string{"--dir", dir}); code != 0 {
		t.Fatalf("stop: %d %s", code, errOut)
	}
	if _, err := os.Stat(stoppedPath(dir)); err != nil {
		t.Fatalf("stop left no record: %v", err)
	}
	if !strings.Contains(out.String(), "a reboot leaves it stopped too") {
		t.Errorf("stop does not say it: %s", out)
	}
	for name, run := range map[string]func([]string) int{"start": o.startCmd, "restart": o.restartCmd} {
		if err := markStopped(dir); err != nil {
			t.Fatal(err)
		}
		if code := run([]string{"--dir", dir}); code != 0 {
			t.Fatalf("%s: %d %s", name, code, errOut)
		}
		if _, err := os.Stat(stoppedPath(dir)); !os.IsNotExist(err) {
			t.Errorf("%s left the record: %v", name, err)
		}
	}
	// A stop that failed is no stop: nothing is recorded.
	o.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if contains(args, "stop") {
			return []byte("boom"), errors.New("exit status 1")
		}
		return nil, nil
	}
	if code := o.stopCmd([]string{"--dir", dir}); code != 1 {
		t.Fatalf("a failing stop: %d", code)
	}
	if _, err := os.Stat(stoppedPath(dir)); !os.IsNotExist(err) {
		t.Errorf("a failed stop left a record: %v", err)
	}
}

func TestPlainUninstallLeavesTheRecord(t *testing.T) {
	f := &fakeOps{ps: healthyPS}
	o, _, errOut := testOps("", f)
	dir := installDir(t)
	if code := o.uninstallContainers(purgeOptions{dir: dir, yes: true, dryRun: true}); code != 0 {
		t.Fatalf("dry run: %d %s", code, errOut)
	}
	if _, err := os.Stat(stoppedPath(dir)); !os.IsNotExist(err) {
		t.Errorf("a dry run left a record: %v", err)
	}
	if code := o.uninstallContainers(purgeOptions{dir: dir, yes: true}); code != 0 {
		t.Fatalf("uninstall: %d %s", code, errOut)
	}
	// The containers are gone on purpose: the boot unit must not bring them back.
	if _, err := os.Stat(stoppedPath(dir)); err != nil {
		t.Errorf("a plain uninstall left no record: %v", err)
	}
}

// The install puts the unit in place once the stack is up; a second run changes nothing and says
// nothing, a refusing systemd is a warning that does not fail the install, a store stopped on
// purpose is started and its record taken away, and BOBRES_START_AT_BOOT=off takes the unit away.
func TestInstallPutsTheBootUnitInPlace(t *testing.T) {
	dir := t.TempDir()
	f := newFakeBootSystem()
	ins, out, errOut := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	ins.bootUnits = f.units()
	ins.exe = func() (string, error) { return testExe, nil }
	if code := ins.install(append(baseArgs(dir), "--no-harden")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if f.files[bootUnitPath] == "" || !f.enabled {
		t.Fatalf("unit: %v", f.files)
	}
	if !strings.Contains(f.files[bootUnitPath], "ExecStart="+testExe+" start --dir "+dir+"\n") {
		t.Errorf("the unit:\n%s", f.files[bootUnitPath])
	}
	if !strings.Contains(out.String(), bootLabel) {
		t.Errorf("the install does not say it:\n%s", out)
	}

	// Once more: changes nothing and says nothing.
	f.cmds = nil
	ins2, out2, errOut2 := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	ins2.bootUnits = f.units()
	ins2.exe = func() (string, error) { return testExe, nil }
	if code := ins2.install(append(baseArgs(dir), "--no-harden")); code != 0 {
		t.Fatalf("second run exit %d: %s", code, errOut2)
	}
	if strings.Contains(out2.String(), bootLabel) || f.ran("systemctl daemon-reload") != 0 || f.ran("systemctl enable") != 0 {
		t.Errorf("a second install changed it: %v\n%s", f.cmds, out2)
	}

	// A store that was stopped on purpose is started by an install (an update runs one): the record goes.
	if err := markStopped(dir); err != nil {
		t.Fatal(err)
	}
	ins3, _, errOut3 := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	ins3.bootUnits = f.units()
	ins3.exe = func() (string, error) { return testExe, nil }
	if code := ins3.install(append(baseArgs(dir), "--no-harden")); code != 0 {
		t.Fatalf("third run exit %d: %s", code, errOut3)
	}
	if _, err := os.Stat(stoppedPath(dir)); !os.IsNotExist(err) {
		t.Errorf("an install left the record of a stopped store: %v", err)
	}

	// systemd refusing is a warning; the install still finishes.
	bad := newFakeBootSystem()
	bad.fail["systemctl daemon-reload"] = errors.New("exit status 1")
	ins4, _, errOut4 := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	ins4.bootUnits = bad.units()
	ins4.exe = func() (string, error) { return testExe, nil }
	if code := ins4.install(append(baseArgs(t.TempDir()), "--no-harden")); code != 0 || !strings.Contains(errOut4.String(), "warning: starting at boot: not set up") {
		t.Errorf("a refusing systemd: exit %d\n%s", code, errOut4)
	}

	// The owner switched it off in .env: the install takes the unit away and does not put it back.
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	env[bootKey] = "off"
	if err := writeEnv(filepath.Join(dir, ".env"), env); err != nil {
		t.Fatal(err)
	}
	ins5, _, errOut5 := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	ins5.bootUnits = f.units()
	ins5.exe = func() (string, error) { return testExe, nil }
	if code := ins5.install(append(baseArgs(dir), "--no-harden", "--quiet", "--yes", "--skip-checks", "--offline")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut5)
	}
	if len(f.files) != 0 || f.enabled {
		t.Errorf("unit left: %v", f.files)
	}
}

// bobres config set BOBRES_START_AT_BOOT puts the setting into effect at once, like the other two helpers.
func TestConfigSetStartAtBoot(t *testing.T) {
	dir := t.TempDir()
	writeInstalledCompose(t, dir)
	env := checkEnvMap()
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFakeBootSystem()
	o, out, errOut := testOps("", &fakeOps{})
	o.bootUnits, o.exe = f.units(), func() (string, error) { return testExe, nil }
	if code := o.configCmd([]string{"set", bootKey, "on", "--dir", dir}); code != 0 || f.files[bootUnitPath] == "" || !f.enabled {
		t.Fatalf("on: exit %d files %v\n%s%s", code, f.files, out, errOut)
	}
	if !strings.Contains(out.String(), "starts by itself after every boot") {
		t.Errorf("on says: %s", out)
	}
	out.Reset()
	if code := o.configCmd([]string{"set", bootKey, "OFF", "--dir", dir}); code != 0 || len(f.files) != 0 || !strings.Contains(out.String(), "no longer starts by itself") {
		t.Fatalf("off: exit %d files %v\n%s", code, f.files, out)
	}
	if got, _, _ := readEnv(filepath.Join(dir, ".env")); got[bootKey] != "off" {
		t.Errorf("saved %q", got[bootKey])
	}
	if code := o.configCmd([]string{"set", bootKey, "maybe", "--dir", dir}); code != 1 {
		t.Errorf("a bad value: exit %d", code)
	}
	// Without a host to change: saved, and said.
	o2, out2, _ := testOps("", &fakeOps{})
	if code := o2.configCmd([]string{"set", bootKey, "on", "--dir", dir}); code != 0 || !strings.Contains(out2.String(), "next sudo bobres install or sudo bobres update") {
		t.Errorf("no units: exit %d %s", code, out2)
	}
}

func TestCheckHasAStartAtBootLine(t *testing.T) {
	c, out, _, dir, sc := newCheckTest(t)
	sc.check = coreAllGood
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backups", "bobres-20261016T030000Z.bkp"), make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	// Not asked (a test, or a machine that is not the server): no line.
	c.o.bootUnits = nil
	if code := c.run([]string{"--dir", dir}); code != 0 || strings.Contains(out.String(), bootLabel) {
		t.Fatalf("exit %d\n%s", code, out)
	}
	// Asked, and the unit is not there: a warning that says how to fix it, never a failure.
	out.Reset()
	f := newFakeBootSystem()
	c.o.bootUnits = f.units()
	if code := c.run([]string{"--dir", dir}); code != 0 || !strings.Contains(out.String(), "WARN  "+bootLabel) || !strings.Contains(out.String(), "sudo bobres update") {
		t.Fatalf("unit missing: exit %d\n%s", code, out)
	}
	// In place: PASS.
	out.Reset()
	f.units().ensureBoot(context.Background(), bootOptions{dir: dir, exe: testExe})
	f.files = map[string]string{bootUnitPath: mustRender(t, bootOptions{dir: dir, exe: testExe})}
	f.enabled = true
	if code := c.run([]string{"--dir", dir}); code != 0 || !strings.Contains(out.String(), "PASS  "+bootLabel) {
		t.Errorf("a unit in place is not passed: exit %d\n%s", code, out)
	}
}

func mustRender(t *testing.T, o bootOptions) string {
	t.Helper()
	text, err := renderBootUnit(o)
	if err != nil {
		t.Fatal(err)
	}
	return text
}
