package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeUnitHost plays a server's systemd for the helper's units: its unit files and what systemctl
// says, and every program run, in order. Nothing touches this machine.
type fakeUnitHost struct {
	files   map[string]string
	cmds    []string
	writes  []string
	removed []string
	enabled bool
	active  string // active, inactive or failed
	fail    map[string]error
	root    bool
	systemd bool
	goos    string
	noCtl   bool
	// onlyWriteErr makes writing a unit file fail.
	writeErr error
}

func newFakeUnitHost() *fakeUnitHost {
	return &fakeUnitHost{files: map[string]string{}, root: true, systemd: true, goos: "linux", active: "inactive", fail: map[string]error{}}
}

func (f *fakeUnitHost) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.cmds = append(f.cmds, line)
	for prefix, err := range f.fail {
		if strings.HasPrefix(line, prefix) {
			return []byte("Failed: " + line + "\nmore"), err
		}
	}
	has := func() bool { _, ok := f.files[systemUnitDir+"/"+updaterPathUnit]; return ok }
	switch {
	case line == "systemctl enable "+updaterPathUnit:
		if !has() {
			return []byte("Failed to enable unit: Unit file bobres-updater.path does not exist."), errors.New("exit status 1")
		}
		f.enabled = true
	case line == "systemctl restart "+updaterPathUnit:
		f.active = "active"
	case line == "systemctl disable --now "+updaterPathUnit:
		f.enabled, f.active = false, "inactive"
	case strings.HasPrefix(line, "systemctl reset-failed"):
		if f.active == "failed" {
			f.active = "inactive"
		}
	case line == "systemctl is-enabled "+updaterPathUnit:
		switch {
		case !has():
			return []byte("Failed to get unit file state: No such file or directory\n"), errors.New("exit status 1")
		case f.enabled:
			return []byte("enabled\n"), nil
		}
		return []byte("disabled\n"), errors.New("exit status 1")
	case line == "systemctl is-active "+updaterPathUnit:
		if f.active == "active" {
			return []byte("active\n"), nil
		}
		return []byte(f.active + "\n"), errors.New("exit status 3")
	}
	return nil, nil
}

func (f *fakeUnitHost) units() *systemUnits {
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
			if mode != 0o644 {
				return errors.New("a unit file is mode 0644")
			}
			f.files[p] = string(b)
			f.writes = append(f.writes, filepath.Base(p))
			return nil
		},
		remove: func(p string) error {
			if _, ok := f.files[p]; !ok {
				return os.ErrNotExist
			}
			delete(f.files, p)
			f.removed = append(f.removed, filepath.Base(p))
			return nil
		},
		exists: func(p string) bool {
			if p == systemdRunDir {
				return f.systemd
			}
			_, ok := f.files[p]
			return ok
		},
		lookPath: func(string) (string, error) {
			if f.noCtl {
				return "", errors.New("not found")
			}
			return "/usr/bin/systemctl", nil
		},
		euid: func() int {
			if f.root {
				return 0
			}
			return 1000
		},
		goos: f.goos, unitDir: systemUnitDir, runDir: systemdRunDir,
	}
}

func (f *fakeUnitHost) install(t *testing.T, o unitOptions) {
	t.Helper()
	pathText, serviceText, err := renderUnits(o)
	if err != nil {
		t.Fatal(err)
	}
	f.files[systemUnitDir+"/"+updaterPathUnit], f.files[systemUnitDir+"/"+updaterServiceUnit] = pathText, serviceText
}

var testUnitOpts = unitOptions{dir: "/opt/bobres", exe: "/usr/local/bin/bobres", state: "/var/lib/bobres"}

func TestRenderUnits(t *testing.T) {
	pathUnit, serviceUnit, err := renderUnits(testUnitOpts)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PathExists=/opt/bobres/run/update/request.json", "Unit=bobres-updater.service", "ConditionPathIsDirectory=/opt/bobres/run/update",
	} {
		if !strings.Contains(pathUnit, want) {
			t.Errorf("the path unit lacks %q:\n%s", want, pathUnit)
		}
	}
	for _, want := range []string{
		"ExecStart=/usr/local/bin/bobres update --from-request --dir /opt/bobres", "Environment=BOBRES_STATE_DIR=/var/lib/bobres",
		"ReadWritePaths=/opt/bobres /usr/local/bin /etc/systemd/system -/var/lib/bobres -/var/log/bobres-install.log",
		`ExecStopPost=-/bin/sh -c '[ "$$SERVICE_RESULT" = success ] || exec /bin/rm -f -- /opt/bobres/run/update/request.json'`,
		"Type=oneshot", "User=root",
	} {
		if !strings.Contains(serviceUnit, want) {
			t.Errorf("the service unit lacks %q:\n%s", want, serviceUnit)
		}
	}
	for _, text := range []string{pathUnit, serviceUnit} {
		for _, ph := range []string{"@DIR@", "@EXE@", "@EXEDIR@", "@STATE@"} {
			if strings.Contains(text, ph) {
				t.Errorf("%s was left:\n%s", ph, text)
			}
		}
	}
	// An install elsewhere, and a bobres elsewhere.
	_, other, err := renderUnits(unitOptions{dir: "/srv/bob-res_1.0", exe: "/opt/bin/bobres", state: "/srv/state"})
	if err != nil || !strings.Contains(other, "ExecStart=/opt/bin/bobres update --from-request --dir /srv/bob-res_1.0") ||
		!strings.Contains(other, "ReadWritePaths=/srv/bob-res_1.0 /opt/bin ") {
		t.Errorf("another place: %v\n%s", err, other)
	}
}

func TestUnitSafePath(t *testing.T) {
	for _, p := range []string{"/opt/bobres", "/usr/local/bin/bobres", "/srv/a-b_c.d+e/x1"} {
		if !unitSafePath(p) {
			t.Errorf("%q refused", p)
		}
	}
	for _, p := range []string{
		"", "/", "opt/bobres", "./bobres", "/opt/my bobres", "/opt/bob\"res", "/opt/bob'res", "/opt/100%", "/opt/$HOME", "/opt/a;b",
		"/opt/a\nb", "/opt/../etc", "/opt//bobres", "/opt/bobres/", "/opt/ß", strings.Repeat("/a", 120),
	} {
		if unitSafePath(p) {
			t.Errorf("%q accepted", p)
		}
	}
	for _, o := range []unitOptions{{dir: "/opt/b b", exe: "/x/bobres", state: "/s"}, {dir: "/opt/b", exe: "bobres", state: "/s"}, {dir: "/opt/b", exe: "/x/bobres", state: ""}} {
		if _, _, err := renderUnits(o); err == nil {
			t.Errorf("rendered %+v", o)
		}
	}
}

func TestEnsureInstallsTheUnits(t *testing.T) {
	f := newFakeUnitHost()
	res := f.units().ensure(context.Background(), testUnitOpts)
	if res.kind != markOK || !res.changed || !strings.Contains(res.text, "the dashboard can update this server") {
		t.Fatalf("result %+v", res)
	}
	// The service first (the path unit names it), both as root's files.
	if want := []string{updaterServiceUnit, updaterPathUnit}; !reflect.DeepEqual(f.writes, want) {
		t.Errorf("writes %v, want %v", f.writes, want)
	}
	want := []string{
		"systemctl daemon-reload",
		"systemctl enable " + updaterPathUnit,
		"systemctl is-active " + updaterPathUnit,
		"systemctl restart " + updaterPathUnit,
		"systemctl is-enabled " + updaterPathUnit,
		"systemctl is-active " + updaterPathUnit,
	}
	if !reflect.DeepEqual(f.cmds, want) {
		t.Errorf("commands\n%v\nwant\n%v", f.cmds, want)
	}
	if !f.enabled || f.active != "active" {
		t.Errorf("enabled %v, active %s", f.enabled, f.active)
	}
	if !strings.Contains(f.files[systemUnitDir+"/"+updaterServiceUnit], "ExecStart=/usr/local/bin/bobres update --from-request --dir /opt/bobres") {
		t.Errorf("the service unit:\n%s", f.files[systemUnitDir+"/"+updaterServiceUnit])
	}

	// Again: nothing is written, reloaded or restarted, and there is nothing to say.
	f.writes, f.cmds = nil, nil
	res = f.units().ensure(context.Background(), testUnitOpts)
	if res.kind != markOK || res.changed || res.text != "" {
		t.Errorf("second run %+v", res)
	}
	if len(f.writes) != 0 {
		t.Errorf("rewrote %v", f.writes)
	}
	for _, c := range f.cmds {
		if strings.Contains(c, "daemon-reload") || strings.Contains(c, "restart") {
			t.Errorf("second run ran %q", c)
		}
	}
}

func TestEnsureFollowsAChangedUnit(t *testing.T) {
	f := newFakeUnitHost()
	f.install(t, testUnitOpts)
	f.enabled, f.active = true, "active"
	// bobres moved: the service unit is rewritten, the path unit (the same text) is not.
	moved := testUnitOpts
	moved.exe = "/usr/bin/bobres"
	res := f.units().ensure(context.Background(), moved)
	if res.kind != markOK || !res.changed {
		t.Fatalf("result %+v", res)
	}
	if want := []string{updaterServiceUnit}; !reflect.DeepEqual(f.writes, want) {
		t.Errorf("writes %v, want %v", f.writes, want)
	}
	if !contains(f.cmds, "systemctl daemon-reload") || !contains(f.cmds, "systemctl restart "+updaterPathUnit) {
		t.Errorf("commands %v", f.cmds)
	}
	if !strings.Contains(f.files[systemUnitDir+"/"+updaterServiceUnit], "ExecStart=/usr/bin/bobres update") {
		t.Error("the service unit still runs the old bobres")
	}
}

func TestEnsureStartsAnUnitThatStopped(t *testing.T) {
	for _, state := range []string{"inactive", "failed"} {
		f := newFakeUnitHost()
		f.install(t, testUnitOpts)
		f.enabled, f.active = true, state
		res := f.units().ensure(context.Background(), testUnitOpts)
		if res.kind != markOK || res.changed {
			t.Errorf("%s: result %+v", state, res)
		}
		if len(f.writes) != 0 || contains(f.cmds, "systemctl daemon-reload") {
			t.Errorf("%s: touched the files: %v %v", state, f.writes, f.cmds)
		}
		if !contains(f.cmds, "systemctl restart "+updaterPathUnit) || f.active != "active" {
			t.Errorf("%s: not restarted: %v", state, f.cmds)
		}
		// A start limit that was hit is reset first.
		if reset := contains(f.cmds, "systemctl reset-failed "+updaterPathUnit+" "+updaterServiceUnit); reset != (state == "failed") {
			t.Errorf("%s: reset-failed ran: %v", state, reset)
		}
	}
	// Installed but never enabled: enabled now.
	f := newFakeUnitHost()
	f.install(t, testUnitOpts)
	f.active = "active"
	if res := f.units().ensure(context.Background(), testUnitOpts); res.kind != markOK || !f.enabled {
		t.Errorf("not enabled: %+v %v", res, f.enabled)
	}
}

func TestEnsureReportsWhatWentWrong(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(f *fakeUnitHost)
		want  string
	}{
		"a unit file that cannot be written": {func(f *fakeUnitHost) { f.writeErr = errors.New("read-only file system") }, "read-only file system"},
		"daemon-reload fails":                {func(f *fakeUnitHost) { f.fail["systemctl daemon-reload"] = errors.New("exit status 1") }, "daemon-reload"},
		"enable fails":                       {func(f *fakeUnitHost) { f.fail["systemctl enable"] = errors.New("exit status 1") }, "systemctl enable"},
		"restart fails":                      {func(f *fakeUnitHost) { f.fail["systemctl restart"] = errors.New("exit status 1") }, "installed, but systemctl restart"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeUnitHost()
			tc.setup(f)
			res := f.units().ensure(context.Background(), testUnitOpts)
			if res.kind != markWarn || !strings.Contains(res.text, tc.want) {
				t.Errorf("result %+v, want a warning with %q", res, tc.want)
			}
		})
	}
}

// A unit that restart "starts" but that is not up afterwards (its condition fails: the run/update
// directory is missing) is told, with where to look.
func TestEnsureTellsAnUnitThatDoesNotComeUp(t *testing.T) {
	f := newFakeUnitHost()
	u := f.units()
	run := u.run
	u.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		out, err := run(ctx, name, args...)
		if strings.Join(args, " ") == "restart "+updaterPathUnit {
			f.active = "inactive" // the condition failed: nothing was started
		}
		return out, err
	}
	res := u.ensure(context.Background(), testUnitOpts)
	if res.kind != markWarn || !strings.Contains(res.text, updaterPathUnit+" is enabled and inactive") {
		t.Errorf("result %+v", res)
	}
	if !strings.Contains(res.text, "systemctl status "+updaterPathUnit) {
		t.Errorf("no pointer to systemctl status: %q", res.text)
	}
}

func TestEnsureIsSkippedWhereItCannotWork(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(f *fakeUnitHost)
		want  string
	}{
		"not root":     {func(f *fakeUnitHost) { f.root = false }, "run bobres as root"},
		"not linux":    {func(f *fakeUnitHost) { f.goos = "darwin" }, "Linux"},
		"no systemd":   {func(f *fakeUnitHost) { f.systemd = false }, "systemd is not running"},
		"no systemctl": {func(f *fakeUnitHost) { f.noCtl = true }, "systemctl was not found"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeUnitHost()
			tc.setup(f)
			res := f.units().ensure(context.Background(), testUnitOpts)
			if res.kind != markSkip || !strings.Contains(res.text, tc.want) {
				t.Errorf("result %+v, want a skip with %q", res, tc.want)
			}
			if len(f.cmds) != 0 || len(f.writes) != 0 {
				t.Errorf("it changed the system: %v %v", f.cmds, f.writes)
			}
		})
	}
}

func TestEnsureRefusesPathsAUnitCannotHold(t *testing.T) {
	f := newFakeUnitHost()
	o := testUnitOpts
	o.dir = "/opt/my bobres"
	res := f.units().ensure(context.Background(), o)
	if res.kind != markWarn || !strings.Contains(res.text, "cannot go into a systemd unit") {
		t.Errorf("result %+v", res)
	}
	if len(f.cmds) != 0 || len(f.writes) != 0 {
		t.Errorf("it changed the system: %v %v", f.cmds, f.writes)
	}
}

func TestEnsureOffRemovesTheUnits(t *testing.T) {
	off := testUnitOpts
	off.off = true
	f := newFakeUnitHost()
	f.install(t, testUnitOpts)
	f.enabled, f.active = true, "active"
	res := f.units().ensure(context.Background(), off)
	if res.kind != markOK || !res.changed || !strings.Contains(res.text, "BOBRES_UPDATE_FROM_DASHBOARD") {
		t.Fatalf("result %+v", res)
	}
	if len(f.files) != 0 || f.enabled || f.active == "active" {
		t.Errorf("left: %v %v %s", f.files, f.enabled, f.active)
	}
	if !reflect.DeepEqual(f.removed, []string{updaterPathUnit, updaterServiceUnit}) {
		t.Errorf("removed %v", f.removed)
	}
	if !contains(f.cmds, "systemctl disable --now "+updaterPathUnit) || !contains(f.cmds, "systemctl daemon-reload") {
		t.Errorf("commands %v", f.cmds)
	}
	// Off and nothing installed: nothing is run.
	f = newFakeUnitHost()
	res = f.units().ensure(context.Background(), off)
	if res.kind != markOK || res.changed || len(f.cmds) != 0 {
		t.Errorf("off, nothing there: %+v %v", res, f.cmds)
	}
}

func TestUninstallIsSilentWithoutUnits(t *testing.T) {
	f := newFakeUnitHost()
	if res := f.units().uninstall(context.Background()); res.text != "" || len(f.cmds) != 0 {
		t.Errorf("nothing installed: %+v %v", res, f.cmds)
	}
	f.install(t, testUnitOpts)
	f.enabled, f.active = true, "active"
	res := f.units().uninstall(context.Background())
	if !res.changed || len(f.files) != 0 {
		t.Errorf("installed: %+v %v", res, f.files)
	}
	// Not root, or no systemd: it leaves the system alone and says nothing.
	g := newFakeUnitHost()
	g.root = false
	g.install(t, testUnitOpts)
	if res := g.units().uninstall(context.Background()); res.text != "" || len(g.files) != 2 || len(g.cmds) != 0 {
		t.Errorf("not root: %+v %v %v", res, g.files, g.cmds)
	}
}

func TestUnitReport(t *testing.T) {
	ctx := context.Background()
	f := newFakeUnitHost()
	exists := map[string]bool{"/usr/local/bin/bobres": true}
	report := func(off bool) Result {
		u := f.units()
		base := u.exists
		u.exists = func(p string) bool { return exists[p] || base(p) }
		return u.report(ctx, off)
	}
	check := func(name string, got Result, st Status, text string) {
		t.Helper()
		if got.Name != dashboardUpdatesLabel || got.Status != st || !strings.Contains(got.Detail, text) {
			t.Errorf("%s: %+v, want %s with %q", name, got, st, text)
		}
	}

	check("nothing installed", report(false), Warn, "not installed")
	check("off and nothing installed", report(true), Pass, "off")

	f.install(t, testUnitOpts)
	check("off but installed", report(true), Warn, "still installed")
	check("not enabled", report(false), Warn, "is disabled, not enabled")
	f.enabled = true
	check("not running", report(false), Warn, "is inactive")
	f.active = "failed"
	check("failed", report(false), Warn, "is failed")
	f.active = "active"
	check("on", report(false), Pass, "enabled and waiting")
	exists["/usr/local/bin/bobres"] = false
	check("bobres gone", report(false), Warn, "/usr/local/bin/bobres, which is not there")
	exists["/usr/local/bin/bobres"] = true

	f.systemd = false
	check("no systemd", report(false), Skip, "needs systemd")
	f.systemd, f.goos = true, "darwin"
	check("not linux", report(false), Skip, "needs systemd")
}

func TestExecPath(t *testing.T) {
	f := newFakeUnitHost()
	u := f.units()
	if got := u.execPath(); got != "" {
		t.Errorf("without a unit: %q", got)
	}
	f.files[systemUnitDir+"/"+updaterServiceUnit] = "[Service]\nExecStart=-@/usr/bin/bobres update --from-request\n"
	if got := u.execPath(); got != "/usr/bin/bobres" {
		t.Errorf("got %q", got)
	}
	f.files[systemUnitDir+"/"+updaterServiceUnit] = "[Service]\nType=oneshot\n"
	if got := u.execPath(); got != "" {
		t.Errorf("without ExecStart: %q", got)
	}
}

func TestDashboardUpdatesOff(t *testing.T) {
	for v, want := range map[string]bool{"off": true, "OFF": true, " off ": true, "on": false, "": false, "no": false, "false": false} {
		if got := dashboardUpdatesOff(map[string]string{dashboardUpdatesKey: v}); got != want {
			t.Errorf("%q: %v, want %v", v, got, want)
		}
	}
	if dashboardUpdatesOff(nil) {
		t.Error("off by default")
	}
}
