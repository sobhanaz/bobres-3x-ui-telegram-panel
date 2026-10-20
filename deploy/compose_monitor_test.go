package deploy

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

// Host monitoring reaches core through one read-only directory and nothing
// else: a root helper on the host (a systemd timer runs `bobres monitor
// snapshot`) writes <install>/run/monitor/snapshot.json, and core reads it.
// Core has no Docker socket, and no other service sees the directory.
func TestComposeMountsTheMonitorSnapshotIntoCoreReadOnly(t *testing.T) {
	m := mountedBy(t, "./run/monitor")
	if got, want := names(m), []string{"core"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("./run/monitor is mounted by %v, want %v", got, want)
	}
	if c := m["core"]; c.target != "/run/bobres-monitor" || !c.readOnly {
		t.Errorf("core mounts ./run/monitor at %s (read-only %v), want /run/bobres-monitor read-only: the collector is the only writer",
			c.target, c.readOnly)
	}

	// The directory core is told to read is the one that is mounted.
	env := composeEnv(t, "core", installEnv(nil))
	if env["BOBRES_MONITOR_DIR"] != m["core"].target {
		t.Errorf("core's BOBRES_MONITOR_DIR is %q, the mount is %q", env["BOBRES_MONITOR_DIR"], m["core"].target)
	}
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "BOBRES_") {
			t.Setenv(k, "")
		}
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	c, err := config.LoadCore()
	if err != nil {
		t.Fatalf("core does not start with the compose environment: %v", err)
	}
	if c.MonitorDir != "/run/bobres-monitor" {
		t.Errorf("core reads the snapshot from %q", c.MonitorDir)
	}
	for svc := range composeEnvServices(t) {
		if svc != "core" {
			if _, ok := composeEnv(t, svc, installEnv(nil))["BOBRES_MONITOR_DIR"]; ok {
				t.Errorf("%s is given BOBRES_MONITOR_DIR: only core reads the snapshot", svc)
			}
		}
	}

	// No container gets the Docker socket, directly or as a host path that holds it; core in
	// particular stays on networks without a way out, so what it learns of the host it cannot send.
	for _, svc := range composeServices() {
		for _, mt := range serviceMounts(t, svc) {
			if strings.Contains(mt.source, "docker.sock") || mt.source == "/var/run" || mt.source == "/run" || mt.source == "/" {
				t.Errorf("%s mounts %s: no container may reach the Docker socket", svc, mt.source)
			}
		}
	}
	if got := strings.Join(serviceNetworks(t, "core"), ","); got != "edge,backend" {
		t.Errorf("core is on the networks %s: it has no internet access, and must not get any", got)
	}
}

// composeEnvServices lists the services that have an environment block.
func composeEnvServices(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, svc := range composeServices() {
		for _, line := range serviceBlock(t, svc) {
			if strings.TrimSpace(line) == "environment:" {
				out[svc] = true
			}
		}
	}
	return out
}

// serviceNetworks reads a service's networks in the flow style
// ("networks: [edge, backend]").
func serviceNetworks(t *testing.T, name string) []string {
	t.Helper()
	v := serviceScalar(t, name, "networks")
	v = strings.Trim(v, "[]")
	var out []string
	for _, n := range strings.Split(v, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Docker, when installed, reads the monitor mount the same way.
func TestDockerComposeReadsTheMonitorMount(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	got := dockerComposeEnv(t, "core", installEnv(nil))
	if got["BOBRES_MONITOR_DIR"] != "/run/bobres-monitor" {
		t.Errorf("docker compose gives core BOBRES_MONITOR_DIR=%q", got["BOBRES_MONITOR_DIR"])
	}
}
