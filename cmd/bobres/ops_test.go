package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeOps struct {
	calls  [][]string
	ps     string
	psErr  error
	stream [][]string
}

func (f *fakeOps) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if contains(args, "ps") {
		return []byte(f.ps), f.psErr
	}
	return nil, nil
}

func (f *fakeOps) streamf(_ context.Context, _ string, args ...string) error {
	f.stream = append(f.stream, args)
	return nil
}

func installDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := "BOBRES_DOMAIN=panel.example.com\nBOBRES_VERSION=1.2.3\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testOps(stdin string, f *fakeOps) (*ops, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &ops{
		in: strings.NewReader(stdin), out: &out, errOut: &errOut,
		run: f.run, stream: f.streamf, freeGB: func(string) float64 { return 42 },
	}, &out, &errOut
}

const healthyPS = `[{"Service":"caddy","State":"running","Health":""},{"Service":"postgres","State":"running","Health":"healthy"},
{"Service":"redis","State":"running","Health":"healthy"},{"Service":"core","State":"running","Health":"healthy"},
{"Service":"payments","State":"running","Health":"healthy"},{"Service":"provisioner","State":"running","Health":"healthy"},
{"Service":"bot","State":"running","Health":"healthy"}]`

func TestStatus(t *testing.T) {
	f := &fakeOps{ps: healthyPS}
	o, out, errOut := testOps("", f)
	if code := o.statusCmd([]string{"--dir", t.TempDir()}); code != 2 || !strings.Contains(errOut.String(), "no install found") {
		t.Fatalf("no install: %d %s", code, errOut)
	}
	dir := installDir(t)
	if code := o.statusCmd([]string{"--dir", dir}); code != 0 {
		t.Fatalf("healthy: %d %s %s", code, out, errOut)
	}
	for _, want := range []string{"Version:  1.2.3", "panel.example.com", "42.0 GB free", "core          running      healthy", "All services are running"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
	if !contains(f.calls[0], "--project-name") || !contains(f.calls[0], "bobres") || !contains(f.calls[0], "ps") {
		t.Fatalf("compose args: %v", f.calls[0])
	}

	// One service unhealthy (newline-delimited format) and one missing.
	f.ps = "{\"Service\":\"core\",\"State\":\"running\",\"Health\":\"unhealthy\"}\n{\"Service\":\"postgres\",\"State\":\"exited\",\"Health\":\"\"}\n"
	o, out, _ = testOps("", f)
	if code := o.statusCmd([]string{"--dir", dir}); code != 1 {
		t.Fatalf("unhealthy: %d %s", code, out)
	}
	for _, want := range []string{"core          running      unhealthy", "postgres      exited", "bot           missing", "need attention"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestLogs(t *testing.T) {
	f := &fakeOps{}
	o, _, errOut := testOps("", f)
	dir := installDir(t)
	if code := o.logsCmd([]string{"--dir", dir, "-f", "--tail", "50", "core", "bot"}); code != 0 {
		t.Fatalf("logs: %d %s", code, errOut)
	}
	got := strings.Join(f.stream[0], " ")
	for _, want := range []string{"compose --project-name bobres", "logs --tail 50 --no-color -f core bot"} {
		if !strings.Contains(got, want) {
			t.Errorf("logs args %q missing %q", got, want)
		}
	}
	if code := o.logsCmd([]string{"--dir", t.TempDir()}); code != 2 {
		t.Fatalf("logs without install: %d", code)
	}
}

func TestUninstall(t *testing.T) {
	// Declined.
	f := &fakeOps{}
	o, out, _ := testOps("n\n", f)
	dir := installDir(t)
	if code := o.uninstallCmd([]string{"--dir", dir}); code != 1 || !strings.Contains(out.String(), "Cancelled") || len(f.calls) != 0 {
		t.Fatalf("declined: %d %s calls=%v", code, out, f.calls)
	}
	// Confirmed: containers down, data and directory kept.
	o, out, _ = testOps("y\n", f)
	if code := o.uninstallCmd([]string{"--dir", dir}); code != 0 {
		t.Fatalf("uninstall: %d %s", code, out)
	}
	if args := strings.Join(f.calls[0], " "); !strings.Contains(args, "down --remove-orphans") || strings.Contains(args, "--volumes") {
		t.Fatalf("down args: %s", args)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err != nil {
		t.Fatal("install directory removed without --purge")
	}
	if !strings.Contains(out.String(), "Kept") {
		t.Fatalf("output: %s", out)
	}
	// Purge needs the domain typed in; a wrong answer does nothing.
	f = &fakeOps{}
	o, out, _ = testOps("yes\n", f)
	if code := o.uninstallCmd([]string{"--dir", dir, "--purge"}); code != 1 || len(f.calls) != 0 {
		t.Fatalf("purge with wrong confirmation: %d %s", code, out)
	}
	o, out, _ = testOps("panel.example.com\n", f)
	if code := o.uninstallCmd([]string{"--dir", dir, "--purge"}); code != 0 {
		t.Fatalf("purge: %d %s", code, out)
	}
	if args := strings.Join(f.calls[0], " "); !strings.Contains(args, "down --remove-orphans --volumes") {
		t.Fatalf("purge args: %s", args)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("install directory still exists after --purge")
	}
	// --yes skips every prompt.
	dir = installDir(t)
	f = &fakeOps{}
	o, _, _ = testOps("", f)
	if code := o.uninstallCmd([]string{"--dir", dir, "--purge", "--yes"}); code != 0 || len(f.calls) != 1 {
		t.Fatalf("--yes: %d calls=%v", code, f.calls)
	}
}

func TestParseServicesFormats(t *testing.T) {
	arr := parseServices([]byte(`[{"Service":"core","State":"running","Health":"healthy"}]`))
	lines := parseServices([]byte("{\"Service\":\"core\",\"State\":\"running\",\"Health\":\"healthy\"}\n{\"Service\":\"bot\",\"State\":\"restarting\"}\n"))
	if arr["core"].Health != "healthy" || lines["core"].Health != "healthy" || lines["bot"].State != "restarting" {
		t.Fatalf("arr=%v lines=%v", arr, lines)
	}
	if len(parseServices([]byte("garbage"))) != 0 {
		t.Fatal("garbage parsed as services")
	}
}
