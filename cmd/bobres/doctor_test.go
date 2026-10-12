package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestCheckOS(t *testing.T) {
	cases := []struct {
		os, arch string
		want     Status
	}{
		{"linux", "amd64", Pass}, {"linux", "arm64", Pass},
		{"darwin", "arm64", Fail}, {"windows", "amd64", Fail}, {"linux", "386", Fail},
	}
	for _, c := range cases {
		if got := checkOS(c.os, c.arch).Status; got != c.want {
			t.Errorf("%s/%s: got %s want %s", c.os, c.arch, got, c.want)
		}
	}
}

func TestCheckRAMThresholds(t *testing.T) {
	cases := []struct {
		mb   int
		want Status
	}{
		{0, Skip}, {512, Fail}, {899, Fail},
		// A "1 GB" server reports a little less than 1024 MiB: supported, with a warning.
		{900, Warn}, {961, Warn}, {1024, Warn}, {1799, Warn},
		// A "2 GB" server reports a little less than 2048 MiB: recommended, no warning.
		{1800, Pass}, {1950, Pass}, {2048, Pass}, {8192, Pass},
	}
	for _, c := range cases {
		if got := checkRAM(c.mb).Status; got != c.want {
			t.Errorf("%d MB: got %s want %s", c.mb, got, c.want)
		}
	}
	if d := checkRAM(512).Detail; !strings.Contains(d, "at least 1 GB") {
		t.Errorf("refusal: %q", d)
	}
	if d := checkRAM(961).Detail; !strings.Contains(d, "2 GB+ is recommended") {
		t.Errorf("warning: %q", d)
	}
}

// The installer's preflight refuses only what doctor fails: a 1 GB server
// passes it with the RAM warning, a smaller one is refused.
func TestPreflightOnA1GBServer(t *testing.T) {
	for _, c := range []struct {
		mb   int
		pass bool
	}{{961, true}, {1990, true}, {600, false}} {
		ins, _, errOut := testInstaller("", secrets, nil)
		ins.preflight = func(string) []Result { return []Result{{"OS/arch", Pass, "linux/amd64"}, checkRAM(c.mb)} }
		code := ins.install(append(baseArgs(t.TempDir()), "--no-start"))
		if (code == 0) != c.pass || (!c.pass && !strings.Contains(errOut.String(), "System checks failed")) {
			t.Errorf("%d MB: exit %d %s", c.mb, code, errOut)
		}
	}
}

func TestCheckDiskThresholds(t *testing.T) {
	cases := []struct {
		gb   float64
		want Status
	}{{-1, Skip}, {2, Fail}, {9.9, Fail}, {10, Pass}, {200, Pass}}
	for _, c := range cases {
		if got := checkDisk(c.gb).Status; got != c.want {
			t.Errorf("%.1f GB: got %s want %s", c.gb, got, c.want)
		}
	}
}

func TestCheckPortDockerCompose(t *testing.T) {
	if checkPort(443, true).Status != Fail || checkPort(443, false).Status != Pass {
		t.Error("port")
	}
	if checkDocker(false, false).Status != Warn || checkDocker(true, false).Status != Fail || checkDocker(true, true).Status != Pass {
		t.Error("docker")
	}
	if checkCompose(false).Status != Warn || checkCompose(true).Status != Pass {
		t.Error("compose")
	}
}

func TestCheckDNS(t *testing.T) {
	cases := []struct {
		name     string
		domain   string
		resolved []string
		pub      string
		want     Status
	}{
		{"no domain", "", nil, "1.2.3.4", Skip},
		{"unresolved", "a.example", nil, "1.2.3.4", Fail},
		{"match", "a.example", []string{"9.9.9.9", "1.2.3.4"}, "1.2.3.4", Pass},
		{"mismatch", "a.example", []string{"9.9.9.9"}, "1.2.3.4", Fail},
		{"unknown public ip", "a.example", []string{"9.9.9.9"}, "", Warn},
	}
	for _, c := range cases {
		if got := checkDNS(c.domain, c.resolved, c.pub).Status; got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

func TestSummarize(t *testing.T) {
	rs := []Result{{"a", Pass, ""}, {"b", Warn, ""}, {"c", Skip, ""}, {"d", Fail, ""}, {"e", Pass, ""}}
	p, w, f, failed := Summarize(rs)
	if p != 2 || w != 1 || f != 1 || !failed {
		t.Fatalf("got %d %d %d %v", p, w, f, failed)
	}
	if _, _, _, failed := Summarize([]Result{{"a", Warn, ""}, {"b", Skip, ""}}); failed {
		t.Fatal("warnings and skips must not fail the run")
	}
}

func TestRunDispatch(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "bobres ") {
		t.Fatalf("version: %d %q", code, out.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"secrets"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: bobres secrets rotate") {
		t.Fatalf("secrets: %d %q", code, errOut.String())
	}
	errOut.Reset()
	if code := run([]string{"config"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: bobres config") {
		t.Fatalf("config: %d %q", code, errOut.String())
	}
	errOut.Reset()
	// Usage errors only: a real run would change this machine.
	if code := run([]string{"harden", "extra"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: bobres harden") {
		t.Fatalf("harden: %d %q", code, errOut.String())
	}
	errOut.Reset()
	if code := run([]string{"bogus"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("bogus: %d %q", code, errOut.String())
	}
	out.Reset()
	if code := run(nil, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("help: %d", code)
	}
}

func TestEveryDocumentedCommandIsHandled(t *testing.T) {
	// docs/06-installer.md command list: none may fall through to "unknown command".
	for _, c := range []string{"update", "rollback", "backup", "restore", "config", "secrets", "license", "admin", "support-bundle"} {
		var out, errOut bytes.Buffer
		if code := run([]string{c}, &out, &errOut); code != 2 || strings.Contains(errOut.String(), "unknown command") {
			t.Errorf("%s: code=%d err=%q", c, code, errOut.String())
		}
	}
}

func TestDoctorOfflineRuns(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"doctor", "--offline"}, &out, &errOut)
	if code != 0 && code != 1 {
		t.Fatalf("unexpected exit %d: %s", code, errOut.String())
	}
	wants := []string{"OS/arch", "RAM", "Disk", "Port 80", "Port 443", "Docker", "DNS", "passed"}
	if runtime.GOOS != "linux" { // the hardening state: skipped off Linux, without running anything
		wants = append(wants, "SKIP  Hardening")
	}
	for _, want := range wants {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}
