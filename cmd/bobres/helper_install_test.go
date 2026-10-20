package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// updaterInstall runs bobres install over fake Docker and a fake server's systemd, which both write
// what they are asked to into one list, so that the order of things can be told.
type updaterInstall struct {
	ins    *installer
	out    *bytes.Buffer
	errOut *bytes.Buffer
	host   *fakeUnitHost
	docker *fakeDocker
	events *[]string
}

func newUpdaterInstall(t *testing.T) *updaterInstall {
	t.Helper()
	docker := &fakeDocker{health: []string{allHealthy}}
	ins, out, errOut := testInstaller("", secrets, docker)
	host := newFakeUnitHost()
	var events []string
	run := ins.run
	ins.run = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		events = append(events, "docker "+composeVerbs([][]string{args})[0])
		return run(ctx, dir, args...)
	}
	units := host.units()
	hostRun := units.run
	units.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "systemctl" && len(args) > 0 && !strings.HasPrefix(args[0], "is-") {
			events = append(events, "systemctl "+args[0])
		}
		return hostRun(ctx, name, args...)
	}
	ins.units, ins.stateDir = units, "/var/lib/bobres"
	ins.exe = func() (string, error) { return "/usr/local/bin/bobres", nil }
	return &updaterInstall{ins: ins, out: out, errOut: errOut, host: host, docker: docker, events: &events}
}

func TestInstallSetsUpTheUpdaterOnceTheStackIsUp(t *testing.T) {
	u := newUpdaterInstall(t)
	dir := t.TempDir()
	if code := u.ins.install(baseArgs(dir)); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, u.out, u.errOut)
	}
	// The units, in place and on.
	for _, f := range []string{updaterPathUnit, updaterServiceUnit} {
		if _, ok := u.host.files[systemUnitDir+"/"+f]; !ok {
			t.Errorf("%s was not written", f)
		}
	}
	if svc := u.host.files[systemUnitDir+"/"+updaterServiceUnit]; !strings.Contains(svc, "ExecStart=/usr/local/bin/bobres update --from-request --dir "+dir) {
		t.Errorf("the service unit:\n%s", svc)
	}
	if !u.host.enabled || u.host.active != "active" {
		t.Errorf("enabled %v, active %s", u.host.enabled, u.host.active)
	}
	// The dashboard learns it from status.json.
	st, ok := readHelperStatus(statusPath(dir))
	if !ok || !st.Installed || st.State != helperIdle || st.Helper != helperVersion() {
		t.Errorf("status %+v %v", st, ok)
	}
	// After the stack is up, not before: the stack is started, then its health waited for, then the units.
	up, ps, enable := -1, -1, -1
	for i, e := range *u.events {
		switch {
		case e == "docker up" && up < 0:
			up = i
		case e == "docker ps" && up >= 0 && ps < 0:
			ps = i
		case e == "systemctl enable":
			enable = i
		}
	}
	if up < 0 || ps < up || enable < ps {
		t.Errorf("order of events: %v", *u.events)
	}
	if !strings.Contains(u.out.String(), "Dashboard updates") || !strings.Contains(u.out.String(), "the dashboard can update this server") {
		t.Errorf("the install does not say so:\n%s", u.out)
	}
	// run/update/ is there for the stack to mount.
	if fi, err := os.Stat(updateDirPath(dir)); err != nil || !fi.IsDir() {
		t.Errorf("run/update: %v %v", fi, err)
	}

	// An install again (a settings change, a repair): nothing is rewritten, restarted or said.
	u2 := newUpdaterInstall(t)
	u2.host.files, u2.host.enabled, u2.host.active = u.host.files, true, "active"
	u2.docker.health = []string{allHealthy}
	if code := u2.ins.install(append(baseArgs(dir), "--quiet", "--yes", "--skip-checks", "--offline")); code != 0 {
		t.Fatalf("second install: exit %d\n%s", code, u2.errOut)
	}
	if len(u2.host.writes) != 0 {
		t.Errorf("rewrote %v", u2.host.writes)
	}
	for _, e := range *u2.events {
		if e == "systemctl daemon-reload" || e == "systemctl restart" {
			t.Errorf("event %q on an install that was in place", e)
		}
	}
	if strings.Contains(u2.out.String(), "Dashboard updates") {
		t.Errorf("said something about units that were in place:\n%s", u2.out)
	}
}

func TestInstallDoesNotSetTheUpdaterUpWithoutAStartedStack(t *testing.T) {
	t.Run("--no-start", func(t *testing.T) {
		u := newUpdaterInstall(t)
		dir := t.TempDir()
		if code := u.ins.install(append(baseArgs(dir), "--no-start")); code != 0 {
			t.Fatalf("exit %d\n%s", code, u.errOut)
		}
		if len(u.host.files) != 0 || len(u.host.cmds) != 0 {
			t.Errorf("units were set up: %v %v", u.host.files, u.host.cmds)
		}
		if _, err := os.Stat(statusPath(dir)); err == nil {
			t.Error("a status was written")
		}
	})
	t.Run("the stack does not start", func(t *testing.T) {
		u := newUpdaterInstall(t)
		u.docker.upErr = errors.New("exit status 1")
		dir := t.TempDir()
		if code := u.ins.install(baseArgs(dir)); code == 0 {
			t.Fatal("the install of a stack that does not start succeeded")
		}
		if len(u.host.files) != 0 || len(u.host.cmds) != 0 {
			t.Errorf("units were set up: %v %v", u.host.files, u.host.cmds)
		}
	})
}

// The updater is never a reason for an install or an update to fail.
func TestInstallGoesOnWhenTheUpdaterCannotBeSetUp(t *testing.T) {
	u := newUpdaterInstall(t)
	u.host.fail["systemctl enable"] = errors.New("exit status 1")
	dir := t.TempDir()
	if code := u.ins.install(baseArgs(dir)); code != 0 {
		t.Fatalf("exit %d\n%s", code, u.errOut)
	}
	if !strings.Contains(u.errOut.String(), "warning: the dashboard's updater: not set up: systemctl enable") {
		t.Errorf("no warning:\n%s", u.errOut)
	}
	if _, err := os.Stat(statusPath(dir)); err == nil {
		t.Error("status.json says the helper is installed when it is not")
	}
}

func TestInstallSaysWhyTheUpdaterIsNotSetUp(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(u *updaterInstall)
		args  []string
		note  string
	}{
		"not root, asked":  {func(u *updaterInstall) { u.host.root = false }, nil, "Dashboard updates"},
		"no systemd":       {func(u *updaterInstall) { u.host.systemd = false }, nil, "systemd is not running"},
		"quiet is silent":  {func(u *updaterInstall) { u.host.root = false }, []string{"--quiet"}, ""},
		"no bobres binary": {func(u *updaterInstall) { u.ins.exe = func() (string, error) { return "", errors.New("no exe") } }, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			u := newUpdaterInstall(t)
			tc.setup(u)
			dir := t.TempDir()
			if code := u.ins.install(append(baseArgs(dir), tc.args...)); code != 0 {
				t.Fatalf("exit %d\n%s", code, u.errOut)
			}
			if tc.note != "" && !strings.Contains(u.out.String(), tc.note) {
				t.Errorf("no note %q:\n%s", tc.note, u.out)
			}
			if tc.note == "" && strings.Contains(u.out.String(), "Dashboard updates") {
				t.Errorf("a note when there should be none:\n%s", u.out)
			}
			if len(u.host.files) != 0 || len(u.host.cmds) != 0 {
				t.Errorf("the system was changed: %v %v", u.host.files, u.host.cmds)
			}
			if name == "no bobres binary" && !strings.Contains(u.errOut.String(), "cannot find the bobres binary") {
				t.Errorf("no warning:\n%s", u.errOut)
			}
		})
	}
}

func TestInstallFollowsTheSettingThatSwitchesTheUpdaterOff(t *testing.T) {
	u := newUpdaterInstall(t)
	dir := t.TempDir()
	if code := u.ins.install(baseArgs(dir)); code != 0 {
		t.Fatalf("exit %d\n%s", code, u.errOut)
	}
	if len(u.host.files) != 2 {
		t.Fatalf("units: %v", u.host.files)
	}
	// The owner turns it off (bobres config set does this to .env): the next install removes the units
	// and says the helper is not installed.
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	env[dashboardUpdatesKey] = "off"
	if err := writeEnv(filepath.Join(dir, ".env"), env); err != nil {
		t.Fatal(err)
	}
	u2 := newUpdaterInstall(t)
	u2.host.files, u2.host.enabled, u2.host.active = u.host.files, true, "active"
	u2.docker.health = []string{allHealthy}
	if code := u2.ins.install(append(baseArgs(dir), "--quiet", "--yes", "--skip-checks", "--offline")); code != 0 {
		t.Fatalf("second install: exit %d\n%s", code, u2.errOut)
	}
	if len(u2.host.files) != 0 || u2.host.enabled {
		t.Errorf("units left: %v %v", u2.host.files, u2.host.enabled)
	}
	if st, ok := readHelperStatus(statusPath(dir)); !ok || st.Installed {
		t.Errorf("status %+v %v, want installed false", st, ok)
	}
}

func TestPrepareUpdateDir(t *testing.T) {
	f := withFakeRoot(t)
	t.Run("as root", func(t *testing.T) {
		f.euid, f.chowned = 0, nil
		dir := t.TempDir()
		var notes bytes.Buffer
		if err := prepareUpdateDir(dir, &notes); err != nil {
			t.Fatal(err)
		}
		// run/ is root's, update/ is core's, by their descriptors; no one else can write to update/.
		if want := []string{"run 0:0", "update 65532:65532"}; !reflect.DeepEqual(f.chowned, want) {
			t.Errorf("chown calls %q, want %q", f.chowned, want)
		}
		for path, mode := range map[string]os.FileMode{"run": 0o755, filepath.Join("run", "update"): 0o700} {
			if st, err := os.Stat(filepath.Join(dir, path)); err != nil || !st.IsDir() || st.Mode().Perm() != mode {
				t.Errorf("%s: %v %v, want mode %04o", path, st, err, mode)
			}
		}
		if notes.Len() != 0 {
			t.Errorf("notes as root: %q", &notes)
		}
	})
	t.Run("put right on every install", func(t *testing.T) {
		// Docker makes a missing bind-mount source as root with 0755: core could not write to it.
		f.euid, f.chowned = 0, nil
		dir := t.TempDir()
		if err := os.MkdirAll(updateDirPath(dir), 0o755); err != nil { //nolint:gosec // what Docker leaves
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(updateDirPath(dir), statusFileName), []byte("{}"), 0o644); err != nil { //nolint:gosec // a fixture
			t.Fatal(err)
		}
		if err := prepareUpdateDir(dir, bytes.NewBuffer(nil)); err != nil {
			t.Fatal(err)
		}
		if st, _ := os.Stat(updateDirPath(dir)); st.Mode().Perm() != 0o700 {
			t.Errorf("mode %v", st.Mode())
		}
		if want := []string{"run 0:0", "update 65532:65532"}; !reflect.DeepEqual(f.chowned, want) {
			t.Errorf("chown calls %q", f.chowned)
		}
		if _, err := os.Stat(filepath.Join(updateDirPath(dir), statusFileName)); err != nil {
			t.Errorf("what was in the directory is gone: %v", err)
		}
	})
	t.Run("not root", func(t *testing.T) {
		f.euid, f.chowned = 1000, nil
		dir := t.TempDir()
		var notes bytes.Buffer
		if err := prepareUpdateDir(dir, &notes); err != nil {
			t.Fatal(err)
		}
		if len(f.chowned) != 0 {
			t.Errorf("chown calls %q without root", f.chowned)
		}
		if st, err := os.Stat(updateDirPath(dir)); err != nil || st.Mode().Perm() != 0o777 {
			t.Errorf("update/ without root: %v %v (want 0777: core could not write otherwise)", st, err)
		}
		if st, _ := os.Stat(filepath.Join(dir, "run")); st.Mode().Perm() != 0o755 {
			t.Errorf("run/ %v", st.Mode())
		}
		if !strings.Contains(notes.String(), "writable by everyone (mode 0777)") {
			t.Errorf("no note:\n%s", &notes)
		}
	})
	t.Run("a chown that fails", func(t *testing.T) {
		f.euid, f.chownErr = 0, errors.New("operation not permitted")
		defer func() { f.chownErr = nil }()
		if err := prepareUpdateDir(t.TempDir(), bytes.NewBuffer(nil)); err == nil || !strings.Contains(err.Error(), "operation not permitted") {
			t.Errorf("%v", err)
		}
	})
	t.Run("links are not followed", func(t *testing.T) {
		f.euid, f.chowned = 0, nil
		for _, which := range []string{"run", filepath.Join("run", "update")} {
			dir := t.TempDir()
			elsewhere := t.TempDir()
			if which == "run" {
				if err := os.Symlink(elsewhere, filepath.Join(dir, "run")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(dir, "run"), 0o755); err != nil { //nolint:gosec // as install makes it
					t.Fatal(err)
				}
				if err := os.Symlink(elsewhere, updateDirPath(dir)); err != nil {
					t.Fatal(err)
				}
			}
			if err := prepareUpdateDir(dir, bytes.NewBuffer(nil)); err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Errorf("%s as a link: %v", which, err)
			}
			if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
				t.Errorf("%s: something was made behind the link: %v", which, entries)
			}
		}
	})
}

// bobres config set BOBRES_UPDATE_FROM_DASHBOARD puts the setting into effect on the spot.
func TestConfigSetsTheUpdaterOnAndOff(t *testing.T) {
	dir := fullInstall(t, true)
	host := newFakeUnitHost()
	f := &fakeDB{ps: func() string { return healthyWithBackup }}
	o, out, errOut := dbOps("", f)
	o.units = host.units()
	o.exe = func() (string, error) { return "/usr/local/bin/bobres", nil }

	if code := o.configCmd([]string{"set", dashboardUpdatesKey, "off", "--dir", dir}); code != 0 {
		t.Fatalf("off: %d %s", code, errOut)
	}
	env, _ := readEnvFile(t, dir)
	if env[dashboardUpdatesKey] != "off" {
		t.Errorf("saved %q", env[dashboardUpdatesKey])
	}
	if len(host.files) != 0 || !strings.Contains(out.String(), "can no longer update this server") {
		t.Errorf("off: %v\n%s", host.files, out)
	}
	// No container is recreated for it.
	if len(f.steps) != 0 {
		t.Errorf("docker was asked: %q", f.steps)
	}

	out.Reset()
	if code := o.configCmd([]string{"set", dashboardUpdatesKey, "ON", "--dir", dir}); code != 0 {
		t.Fatalf("on: %d %s", code, errOut)
	}
	if env, _ = readEnvFile(t, dir); env[dashboardUpdatesKey] != "on" {
		t.Errorf("saved %q", env[dashboardUpdatesKey])
	}
	if len(host.files) != 2 || !host.enabled || host.active != "active" {
		t.Errorf("on: %v %v %s", host.files, host.enabled, host.active)
	}
	if !strings.Contains(out.String(), "The dashboard can update this server") {
		t.Errorf("output:\n%s", out)
	}
	if fi, err := os.Stat(updateDirPath(dir)); err != nil || !fi.IsDir() {
		t.Errorf("run/update was not made: %v %v", fi, err)
	}
	if st, ok := readHelperStatus(statusPath(dir)); !ok || !st.Installed {
		t.Errorf("status %+v %v", st, ok)
	}
	// This install's compose file has the mount for the dashboard: nothing to add.
	if strings.Contains(out.String(), "is from before the updater") {
		t.Errorf("a warning about a compose file that is current:\n%s", out)
	}
	// The compose file of an install from before the updater has no mount for it: said.
	compose := filepath.Join(dir, "docker-compose.yml")
	cur, _ := os.ReadFile(compose)
	if err := os.WriteFile(compose, []byte(strings.ReplaceAll(string(cur), "/run/bobres-update", "/nowhere")), 0o644); err != nil { //nolint:gosec // a fixture
		t.Fatal(err)
	}
	out.Reset()
	env2, _ := readEnvFile(t, dir)
	if code := applyDashboardUpdates(o, dir, env2); code != 0 || !strings.Contains(out.String(), "docker-compose.yml is from before the updater") {
		t.Errorf("an old compose file: %d\n%s", code, out)
	}

	// A value that is not on or off is refused, and nothing changes.
	errOut.Reset()
	if code := o.configCmd([]string{"set", dashboardUpdatesKey, "maybe", "--dir", dir}); code != 1 || !strings.Contains(errOut.String(), "must be one of on, off") {
		t.Errorf("maybe: %d %s", code, errOut)
	}
	// A server where it cannot be set up still keeps the setting.
	host2 := newFakeUnitHost()
	host2.systemd = false
	o.units = host2.units()
	out.Reset()
	if code := o.configCmd([]string{"set", dashboardUpdatesKey, "off", "--dir", dir}); code != 0 || !strings.Contains(out.String(), "not set up here") {
		t.Errorf("no systemd: %d\n%s", code, out)
	}
	// And one that fails says so.
	host3 := newFakeUnitHost()
	host3.fail["systemctl enable"] = errors.New("exit status 1")
	o.units = host3.units()
	if code := o.configCmd([]string{"set", dashboardUpdatesKey, "on", "--dir", dir}); code != 1 || !strings.Contains(errOut.String(), "The setting is saved, but the updater was not set up") {
		t.Errorf("enable fails: %d\n%s", code, errOut)
	}
}

func TestUninstallPurgeRemovesTheUpdater(t *testing.T) {
	dir := installDir(t)
	host := newFakeUnitHost()
	host.install(t, testUnitOpts)
	host.enabled, host.active = true, "active"

	// Taking the containers away keeps the install, and with it the updater: the dashboard is not up
	// to ask for anything, and start/install bring the store back.
	f := &fakeOps{}
	o, out, _ := testOps("y\n", f)
	o.units = host.units()
	if code := o.uninstallCmd([]string{"--dir", dir}); code != 0 || len(host.files) != 2 {
		t.Fatalf("uninstall: %d %v\n%s", code, host.files, out)
	}
	// The day-one purge (uninstall.go) removes the updater first, with the remover the setting
	// uses, and then everything else named bobres*. Only the updater's own host holds its units here.
	l := newLab(t)
	l.installBobres()
	for _, n := range []string{"bobres-updater.path", "bobres-updater.service", "bobres-monitor.timer", "bobres-monitor.service"} {
		_ = os.RemoveAll(l.p("/etc/systemd/system/" + n))
	}
	l.o.units = host.units()
	if code := l.run("", l.yesArgs()...); code != 0 {
		t.Fatalf("purge: %d\n%s%s", code, l.out, l.err)
	}
	if len(host.files) != 0 || host.enabled {
		t.Errorf("units left: %v %v", host.files, host.enabled)
	}
	if !strings.Contains(l.out.String(), "Dashboard updater: the dashboard can no longer update this server (the units are removed).") {
		t.Errorf("output:\n%s", l.out)
	}
	// With nothing installed, the purge says nothing about it.
	l2 := newLab(t)
	l2.installBobres()
	l2.o.units = newFakeUnitHost().units()
	if code := l2.run("", l2.yesArgs()...); code != 0 || strings.Contains(l2.out.String(), "Dashboard updater") {
		t.Errorf("purge without units: %d\n%s", code, l2.out)
	}
}

func TestCheckHasALineForTheUpdater(t *testing.T) {
	c, out, errOut, dir, sc := newCheckTest(t)
	sc.check = coreAllGood
	host := newFakeUnitHost()
	c.o.units = host.units()
	exists := map[string]bool{"/usr/local/bin/bobres": true}
	base := c.o.units.exists
	c.o.units.exists = func(p string) bool { return exists[p] || base(p) }
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backups", "bobres-20261016T030000Z.bkp"), make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}

	// Not installed: a warning, never a failure (the exit code stays 0).
	if code := c.run([]string{"--dir", dir}); code != 0 || !strings.Contains(out.String(), "WARN  Dashboard updates") || !strings.Contains(out.String(), "not installed") {
		t.Fatalf("not installed: %d\n%s\n%s", code, out, errOut)
	}
	host.install(t, testUnitOpts)
	host.enabled, host.active = true, "active"
	out.Reset()
	if code := c.run([]string{"--dir", dir}); code != 0 || !strings.Contains(out.String(), "PASS  Dashboard updates") || !strings.Contains(out.String(), "enabled and waiting") {
		t.Fatalf("installed: %d\n%s\n%s", code, out, errOut)
	}
	// The setting that switches it off.
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	env[dashboardUpdatesKey] = "off"
	if err := writeEnv(filepath.Join(dir, ".env"), env); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := c.run([]string{"--dir", dir}); code != 0 || !strings.Contains(out.String(), "WARN  Dashboard updates") || !strings.Contains(out.String(), "still installed") {
		t.Fatalf("off but installed: %d\n%s", code, out)
	}
}

func TestDoctorHasALineForTheUpdater(t *testing.T) {
	ctx := context.Background()
	host := newFakeUnitHost()
	o := &ops{units: host.units()}

	rs := updaterResults(ctx, o, installDir(t))
	if len(rs) != 1 || rs[0].Name != dashboardUpdatesLabel || rs[0].Status != Warn || !strings.Contains(rs[0].Detail, "not installed") {
		t.Errorf("an install without the units: %+v", rs)
	}
	// The setting that switches it off is read from .env.
	dir := installDir(t)
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	env[dashboardUpdatesKey] = "off"
	if err := writeEnv(filepath.Join(dir, ".env"), env); err != nil {
		t.Fatal(err)
	}
	if rs := updaterResults(ctx, o, dir); len(rs) != 1 || rs[0].Status != Pass || !strings.Contains(rs[0].Detail, "off") {
		t.Errorf("switched off: %+v", rs)
	}
	// A user who cannot read .env (it is root's) still gets the line.
	if os.Geteuid() != 0 {
		if err := os.Chmod(filepath.Join(dir, ".env"), 0); err != nil {
			t.Fatal(err)
		}
		if rs := updaterResults(ctx, o, dir); len(rs) != 1 || rs[0].Status != Warn {
			t.Errorf("an unreadable .env: %+v", rs)
		}
	}
	// Without an install in the directory, or without units to ask, there is nothing to say.
	if rs := updaterResults(ctx, o, t.TempDir()); len(rs) != 0 {
		t.Errorf("without an install: %+v", rs)
	}
	if rs := updaterResults(ctx, &ops{}, installDir(t)); len(rs) != 0 {
		t.Errorf("without units to ask: %+v", rs)
	}
}
