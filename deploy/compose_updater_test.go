package deploy

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// updateRequestDir is the host directory the dashboard's update requests go through
// (docs/06-installer.md, "Updating from the dashboard"), and where core sees it.
const (
	updateRequestDir    = "./run/update"
	updateRequestTarget = "/run/bobres-update"
)

// Core never gets the Docker socket or any other power over the host. The one thing of the host it
// can write is the directory of the dashboard's update requests: only core mounts it, read-write,
// and nothing else of the install is mounted into core.
func TestComposeUpdateRequestMount(t *testing.T) {
	got := mountedBy(t, updateRequestDir)
	if names := names(got); !reflect.DeepEqual(names, []string{"core"}) {
		t.Fatalf("%s is mounted by %v, want core alone", updateRequestDir, names)
	}
	if m := got["core"]; m.target != updateRequestTarget || m.readOnly {
		t.Errorf("core mounts %s at %s (read-only %v), want %s read-write", updateRequestDir, m.target, m.readOnly, updateRequestTarget)
	}
	for _, m := range serviceMounts(t, "core") {
		if m.source == updateRequestDir {
			continue
		}
		if strings.HasPrefix(m.source, "/") || strings.HasPrefix(m.source, ".") {
			t.Errorf("core mounts %s: the update directory is the only part of the host it may reach", m.source)
		}
	}
	// No service mounts the Docker socket, or the install directory itself.
	for _, svc := range composeServices() {
		for _, m := range serviceMounts(t, svc) {
			if strings.Contains(m.source, "docker.sock") || strings.Contains(m.target, "docker.sock") || m.source == "." || m.source == "./" {
				t.Errorf("%s mounts %s:%s", svc, m.source, m.target)
			}
		}
	}
}

var unitPlaceholderRe = regexp.MustCompile(`@[A-Z]+@`)

// The units are templates (four placeholders `bobres install` fills in) of the helper that runs
// `bobres update --from-request`: they name the request the dashboard writes, run as root and
// stay inside the sandbox of an update.
func TestUpdaterUnitTemplates(t *testing.T) {
	path, service := string(UpdaterPathUnit), string(UpdaterServiceUnit)
	lines := func(text string) map[string][]string {
		m := map[string][]string{}
		for _, l := range strings.Split(text, "\n") {
			if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "[") {
				continue
			}
			k, v, ok := strings.Cut(l, "=")
			if !ok {
				t.Errorf("a unit line that is not Key=value: %q", l)
				continue
			}
			m[k] = append(m[k], v)
		}
		return m
	}
	p, s := lines(path), lines(service)

	// The path unit watches the file core writes, in the directory compose shares with core, and
	// starts the service.
	if got := p["PathExists"]; !reflect.DeepEqual(got, []string{"@DIR@/run/update/request.json"}) {
		t.Errorf("PathExists = %v", got)
	}
	if got := p["Unit"]; !reflect.DeepEqual(got, []string{"bobres-updater.service"}) {
		t.Errorf("Unit = %v", got)
	}
	if got := p["ConditionPathIsDirectory"]; !reflect.DeepEqual(got, []string{"@DIR@/run/update"}) {
		t.Errorf("ConditionPathIsDirectory = %v", got)
	}
	if got := p["WantedBy"]; !reflect.DeepEqual(got, []string{"multi-user.target"}) {
		t.Errorf("WantedBy = %v", got)
	}
	if watched := "./" + strings.TrimSuffix(strings.TrimPrefix(p["PathExists"][0], "@DIR@/"), "/request.json"); watched != updateRequestDir {
		t.Errorf("the path unit watches the directory %s, but compose shares %s with core", watched, updateRequestDir)
	}

	// The service: one run of the same update, as root, nothing else.
	one := func(k, want string) {
		t.Helper()
		if got := s[k]; !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%s = %v, want %q", k, got, want)
		}
	}
	one("Type", "oneshot")
	one("User", "root")
	one("ExecStart", "@EXE@ update --from-request --dir @DIR@")
	one("Environment", "BOBRES_STATE_DIR=@STATE@")
	one("NoNewPrivileges", "yes")
	one("ProtectSystem", "strict")
	one("PrivateTmp", "yes")
	for _, k := range []string{"TimeoutStartSec", "TimeoutStopSec", "ReadWritePaths", "ExecStopPost", "ProtectHome"} {
		if len(s[k]) != 1 {
			t.Errorf("%s = %v, want one line", k, s[k])
		}
	}
	// What an update writes, and nothing more: the install, the folder bobres is replaced in, the
	// units the new bobres writes, the release state and the install log.
	var rw []string
	for _, f := range strings.Fields(strings.Join(s["ReadWritePaths"], " ")) {
		rw = append(rw, strings.TrimPrefix(f, "-"))
	}
	if want := []string{"@DIR@", "@EXEDIR@", "/etc/systemd/system", "@STATE@", "/var/log/bobres-install.log"}; !reflect.DeepEqual(rw, want) {
		t.Errorf("ReadWritePaths = %v, want %v", rw, want)
	}
	// The request is removed whatever became of the run, or the path unit would start it for ever.
	if cleanup := strings.Join(s["ExecStopPost"], " "); !strings.Contains(cleanup, "@DIR@/run/update/request.json") || !strings.Contains(cleanup, "SERVICE_RESULT") {
		t.Errorf("ExecStopPost = %q", cleanup)
	}

	// Only the four placeholders; no systemd specifier (a literal % would be read as one) and no $
	// that systemd would expand, but the $$ that passes one to the shell.
	for name, text := range map[string]string{"path": path, "service": service} {
		for _, ph := range unitPlaceholderRe.FindAllString(text, -1) {
			switch ph {
			case "@DIR@", "@EXE@", "@EXEDIR@", "@STATE@":
			default:
				t.Errorf("%s unit: unknown placeholder %s", name, ph)
			}
		}
		if strings.Contains(text, "%") {
			t.Errorf("%s unit has a %%: systemd would read it as a specifier", name)
		}
		if strings.Contains(strings.ReplaceAll(text, "$$", ""), "$") {
			t.Errorf("%s unit has a $ that systemd would expand", name)
		}
	}
}
