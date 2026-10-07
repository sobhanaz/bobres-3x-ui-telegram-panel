package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type installCall struct {
	args  []string
	env   map[string]string
	quiet bool
}

// testMenu builds a menu over fakes; stdin holds the typed lines.
func testMenu(dir, stdin string, f *fakeOps) (*menu, *bytes.Buffer, *bytes.Buffer, *[]installCall, *[][]string) {
	var out, errOut bytes.Buffer
	br := bufio.NewReader(strings.NewReader(stdin))
	o, _, _ := testOps("", f)
	o.in, o.out, o.errOut = br, &out, &errOut
	var installs []installCall
	var doctors [][]string
	m := &menu{in: br, out: &out, errOut: &errOut, dir: dir, ops: o,
		install: func(args []string, env map[string]string, quiet bool) int {
			installs = append(installs, installCall{args, env, quiet})
			return 0
		},
		doctor: func(args []string) int { doctors = append(doctors, args); return 0 },
		shield: func(fn func()) { fn() },
	}
	return m, &out, &errOut, &installs, &doctors
}

func TestMenuWithoutInstallOffersInstall(t *testing.T) {
	dir := t.TempDir()
	m, out, _, installs, doctors := testMenu(dir, "2\n\n1\n\n0\n", &fakeOps{})
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "No install in "+dir) || !strings.Contains(out.String(), "Install BOBRES") {
		t.Fatalf("menu: %s", out)
	}
	if len(*doctors) != 1 || len(*installs) != 1 || (*installs)[0].quiet || !contains((*installs)[0].args, dir) {
		t.Fatalf("doctor %v install %+v", *doctors, *installs)
	}
}

func TestMenuRunsTheCommands(t *testing.T) {
	dir := installDir(t)
	f := &fakeOps{ps: healthyPS}
	// status, live logs, start, stop (declined, then confirmed), restart,
	// dashboard link, server check, uninstall (declined), exit
	m, out, _, _, doctors := testMenu(dir, "1\n\n2\n\n3\n\n4\nn\n\n4\ny\n\n5\n\n10\n\n11\n\n12\nn\n\n0\n", f)
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"running (7/7 services)", "panel.example.com", "All services are running", "Cancelled.", "BOBRES is stopped", "All services are healthy"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("menu output missing %q", want)
		}
	}
	verbs := map[string]int{}
	for _, c := range f.calls {
		for _, v := range []string{"up", "stop", "restart", "down"} {
			if contains(c, v) {
				verbs[v]++
			}
		}
	}
	if verbs["up"] != 1 || verbs["stop"] != 1 || verbs["restart"] != 1 || verbs["down"] != 0 {
		t.Fatalf("compose verbs %v (calls %v)", verbs, f.calls)
	}
	if len(f.stream) != 2 || !contains(f.stream[0], "-f") || !contains(f.stream[0], "logs") || !contains(f.stream[1], "login-link") {
		t.Fatalf("live logs and the dashboard link: %v", f.stream)
	}
	if len(*doctors) != 1 || !contains((*doctors)[0], "panel.example.com") {
		t.Fatalf("doctor: %v", *doctors)
	}
}

func TestMenuChangesSettingsThroughTheInstaller(t *testing.T) {
	dir := installDir(t)
	in := strings.Join([]string{
		"7", "not-a-token", "", // refused before anything runs
		"7", goodBotToken, "",
		"8", "abc", "",
		"8", "4243", "",
		"9", "http://host.docker.internal:2053/p", "", "https://sub.example.com:2096/sub/", "y", "",
		"9", "", "", "", "", // all kept: nothing to do
		"0", "",
	}, "\n")
	m, out, errOut, installs, _ := testMenu(dir, in, &fakeOps{ps: healthyPS})
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := *installs
	if len(got) != 3 {
		t.Fatalf("installer runs: %+v\n%s\n%s", got, out, errOut)
	}
	for _, c := range got {
		if !c.quiet || !contains(c.args, "--skip-checks") || !contains(c.args, dir) {
			t.Fatalf("settings change must be a quiet re-install of this dir: %+v", c)
		}
	}
	if got[0].env["BOBRES_TELEGRAM_BOT_TOKEN"] != goodBotToken || contains(got[0].args, goodBotToken) {
		t.Fatalf("the bot token must go through the environment, not the arguments: %+v", got[0])
	}
	if !contains(got[1].args, "--admin-id") || !contains(got[1].args, "4243") {
		t.Fatalf("admin change: %+v", got[1])
	}
	if p := got[2]; !contains(p.args, "http://host.docker.internal:2053/p") || !contains(p.args, "--xui-allow-private") ||
		!contains(p.args, "https://sub.example.com:2096/sub/") || p.env != nil {
		t.Fatalf("panel change (token kept): %+v", p)
	}
	for _, want := range []string{"does not look like a @BotFather token", "positive number"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("missing error %q in %s", want, errOut)
		}
	}
	if !strings.Contains(out.String(), "Nothing changed.") || !strings.Contains(out.String(), "The change is live") {
		t.Fatalf("output: %s", out)
	}
}

func TestMenuShowsSettingsWithoutSecrets(t *testing.T) {
	dir := t.TempDir()
	env := "BOBRES_DOMAIN=panel.example.com\nBOBRES_VERSION=1.2.3\nBOBRES_ADMIN_TELEGRAM_ID=4242\n" +
		"BOBRES_TELEGRAM_BOT_TOKEN=" + goodBotToken + "\nBOBRES_XUI_URL=https://xui.example.com:2053/p\n" +
		"BOBRES_XUI_TOKEN=supersecretpaneltoken\nPOSTGRES_PASSWORD=dbpass-should-not-show\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	m, out, _, _, _ := testMenu(dir, "6\n\n0\n", &fakeOps{ps: healthyPS})
	m.loop()
	s := out.String()
	if strings.Contains(s, goodBotToken) || strings.Contains(s, "supersecretpaneltoken") || strings.Contains(s, "dbpass") {
		t.Fatalf("a secret is shown:\n%s", s)
	}
	for _, want := range []string{"123456789:••••", "••••oken", "4242", "https://xui.example.com:2053/p", "Zarinpal gateway:       off"} {
		if !strings.Contains(s, want) {
			t.Errorf("settings missing %q:\n%s", want, s)
		}
	}
}

func TestMenuInputEdges(t *testing.T) {
	dir := installDir(t)
	m, _, _, _, _ := testMenu(dir, "", &fakeOps{ps: healthyPS})
	if code := m.loop(); code != 0 { // end of input (Ctrl+D) leaves cleanly
		t.Fatalf("EOF: %d", code)
	}
	m, out, _, _, _ := testMenu(dir, "99\nabc\n\n0\n", &fakeOps{ps: `[{"Service":"core","State":"exited"}]`})
	if code := m.loop(); code != 0 || strings.Count(out.String(), "There is no option") != 2 || !strings.Contains(out.String(), "stopped") {
		t.Fatalf("bad choices: %d %s", code, out)
	}
	m, out, _, _, _ = testMenu(dir, "0\n", &fakeOps{ps: `[{"Service":"core","State":"running","Health":"healthy"}]`})
	if m.loop(); !strings.Contains(out.String(), "partly running (1/7") {
		t.Fatalf("partial: %s", out)
	}
}

func TestMask(t *testing.T) {
	for in, want := range map[string]string{"": "", "short": "••••", "0123456789abcdef": "••••cdef", goodBotToken: "123456789:••••"} {
		if got := mask(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNoCommandWithoutTerminalPrintsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	defer func(w io.Writer) { stdout = w }(stdout)
	stdout = &out // not a terminal: a script gets the usage, never a menu waiting for input
	if code := run(nil, &out, &errOut); code != 0 || !strings.Contains(out.String(), "management menu") {
		t.Fatalf("%d %s", code, out.String())
	}
}
