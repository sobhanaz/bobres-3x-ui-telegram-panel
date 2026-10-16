package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
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
	// the header's lines start in one column beside the ASCII beaver
	if !strings.Contains(out.String(), "\n   n____n   BOBRES manager  bobres ") || !strings.Contains(out.String(), "\n  ( o  o )  No install in "+dir+" yet.\n   \\_||_/\n") {
		t.Fatalf("menu header: %s", out)
	}
	if len(*doctors) != 1 || len(*installs) != 1 || (*installs)[0].quiet || !contains((*installs)[0].args, dir) {
		t.Fatalf("doctor %v install %+v", *doctors, *installs)
	}
}

func TestMenuRunsTheCommands(t *testing.T) {
	dir := installDir(t)
	f := &fakeOps{ps: healthyPS}
	// status, live logs, start, stop (declined, then confirmed), restart,
	// dashboard link, server check, the HTTPS menu (and back), uninstall
	// (declined), exit
	m, out, _, _, doctors := testMenu(dir, "1\n\n2\n\n3\n\n4\nn\n\n4\ny\n\n5\n\n15\n\n17\n\n19\n0\n\n20\nn\n\n0\n", f)
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"running (8/8 services)", "panel.example.com", "All services are running", "Cancelled.", "BOBRES is stopped", "All services are healthy"} {
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
	if len(*doctors) != 1 || !contains((*doctors)[0], "panel.example.com") || !contains((*doctors)[0], dir) {
		t.Fatalf("doctor: %v", *doctors)
	}
	if !strings.Contains(out.String(), "19. HTTPS certificate") || !strings.Contains(out.String(), "20. Uninstall") ||
		!strings.Contains(out.String(), "Your own certificate files") || !strings.Contains(out.String(), "Server IP address (Let's Encrypt, 6 days") {
		t.Errorf("the HTTPS item and its menu are missing:\n%s", out)
	}
}

func TestMenuBackups(t *testing.T) {
	dir := t.TempDir()
	env := "BOBRES_DOMAIN=panel.example.com\nBOBRES_VERSION=1.4.0\nCOMPOSE_PROFILES=backup\nBOBRES_BACKUP_TIME=03:30\n" +
		"BOBRES_BACKUP_KEY=feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	writeInstalledCompose(t, dir)
	ps := strings.TrimSuffix(healthyPS, "]") + `,{"Service":"backup","State":"running","Health":"healthy"}]`
	f := &fakeOps{ps: ps}
	// backup key (declined, then confirmed), back up now, settings, exit
	m, out, _, _, _ := testMenu(dir, "11\nn\n\n11\ny\n\n10\n\n6\n\n0\n", f)
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Count(out.String(), "feedface") != 8 || !strings.Contains(out.String(), "Cancelled.") ||
		!strings.Contains(out.String(), "running (9/9 services)") || !strings.Contains(out.String(), "daily at 03:30") {
		t.Fatalf("menu: %s", out)
	}
	if len(f.stream) != 1 || !contains(f.stream[0], "backup") || !contains(f.stream[0], "run-now") {
		t.Fatalf("back up now: %v", f.stream)
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
	const merchant = "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b"
	for _, c := range []struct {
		key, zarinpal string
	}{
		{"", "Zarinpal gateway:       set in .env (re-run bobres install"},
		{"BOBRES_PAYMENTS_MASTER_KEY=" + strings.Repeat("k", 64) + "\n", "Zarinpal gateway:       managed in the dashboard (Payment gateways)"},
	} {
		dir := t.TempDir()
		env := "BOBRES_DOMAIN=panel.example.com\nBOBRES_VERSION=1.2.3\nBOBRES_ADMIN_TELEGRAM_ID=4242\n" +
			"BOBRES_TELEGRAM_BOT_TOKEN=" + goodBotToken + "\nBOBRES_XUI_URL=https://xui.example.com:2053/p\n" +
			"BOBRES_XUI_TOKEN=supersecretpaneltoken\nPOSTGRES_PASSWORD=dbpass-should-not-show\n" +
			"BOBRES_ZARINPAL_MERCHANT_ID=" + merchant + "\n" + c.key
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
			t.Fatal(err)
		}
		m, out, _, _, _ := testMenu(dir, "6\n\n0\n", &fakeOps{ps: healthyPS})
		m.loop()
		s := out.String()
		if strings.Contains(s, goodBotToken) || strings.Contains(s, "supersecretpaneltoken") || strings.Contains(s, "dbpass") ||
			strings.Contains(s, merchant[len(merchant)-4:]) || strings.Contains(s, strings.Repeat("k", 8)) {
			t.Fatalf("a secret is shown:\n%s", s)
		}
		// The gateway is not reported from .env: the dashboard holds what is live.
		for _, want := range []string{"123456789:••••", "••••oken", "4242", "https://xui.example.com:2053/p", c.zarinpal, "Servers"} {
			if !strings.Contains(s, want) {
				t.Errorf("settings missing %q:\n%s", want, s)
			}
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
	if m.loop(); !strings.Contains(out.String(), "partly running (1/8") {
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

// opsMenu is a menu over o, whose reader it shares.
func opsMenu(dir, stdin string, o *ops, out, errOut *bytes.Buffer) *menu {
	br := bufio.NewReader(strings.NewReader(stdin))
	o.in = br
	return &menu{in: br, out: out, errOut: errOut, dir: dir, ops: o,
		install: func([]string, map[string]string, bool) int { return 0 },
		doctor:  func([]string) int { return 0 },
		shield:  func(fn func()) { fn() },
	}
}

func TestMenuRestore(t *testing.T) {
	dir := restoreInstall(t)
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		t.Fatal(err)
	}
	for stamp, dump := range map[string]string{"20261010T033000Z": "DUMP OF THE 10TH", "20261011T033000Z": "DUMP OF THE 11TH"} {
		b, _ := os.ReadFile(writeBackup(t, installKey, backupKeys(), dump))
		if err := os.WriteFile(filepath.Join(bdir, "bobres-"+stamp+".bkp"), b, 0o600); err != nil { //nolint:gosec // the test's own folder
			t.Fatal(err)
		}
	}
	other := writeBackup(t, otherKey, backupKeys(), "DUMP FROM ANOTHER SERVER")
	// A link in the backup service's folder is not offered.
	if err := os.Symlink(other, filepath.Join(bdir, "bobres-20261012T033000Z.bkp")); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "key.txt")
	if err := os.WriteFile(keyFile, []byte(otherKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeDB{}
	o, out, errOut := dbOps("", f)
	// cancel; a number that is not there; #1 (the newest), confirmed; a
	// path with its key file, declined at the confirmation; exit
	in := strings.Join([]string{"12", "", "", "12", "9", "", "12", "1", "y", "", "12", other, keyFile, "n", "", "0", ""}, "\n")
	m := opsMenu(dir, in, o, out, errOut)
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	s := out.String()
	if strings.Contains(s, "20261012") || !strings.Contains(s, "1. bobres-20261011T033000Z.bkp") || !strings.Contains(s, "2. bobres-20261010T033000Z.bkp") {
		t.Fatalf("listing:\n%s", s)
	}
	for _, want := range []string{"Cancelled.", "There is no backup 9.", "Restore complete", "Cancelled. Nothing was changed."} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	// One restore ran, of the newest; the other server's backup opened with its key file.
	if len(f.restored) != 1 || f.restored[0] != "DUMP OF THE 11TH" {
		t.Fatalf("restored %q", f.restored)
	}
	if strings.Count(s, "(checked: key and contents are good)") != 2 {
		t.Fatalf("both files must be verified first:\n%s", s)
	}
}

func TestMenuUpdateAndRollback(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.cli.do = installs(ut.dir, "1.3.0")
	m := opsMenu(ut.dir, "13\ny\n", ut.u.ops, ut.out, ut.errOut)
	m.upd = ut.u
	// The update asks, runs, then the menu ends: its bobres was replaced.
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d: %s", code, ut.errOut)
	}
	for _, want := range []string{"Newer:   yes", "Update BOBRES from 1.2.3 to 1.3.0?", "BOBRES is updated to 1.3.0", "run  bobres  again for the new menu"} {
		if !strings.Contains(ut.out.String(), want) {
			t.Errorf("missing %q:\n%s", want, ut.out)
		}
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != newCLI || strings.Contains(ut.out.String(), "Press Enter") {
		t.Fatalf("CLI %q\n%s", b, ut.out)
	}

	// Nothing newer: no question, the menu goes on.
	ut.out.Reset()
	ut.u.current = "v1.3.0"
	m = opsMenu(ut.dir, "13\n\n0\n", ut.u.ops, ut.out, ut.errOut)
	m.upd = ut.u
	if code := m.loop(); code != 0 || !strings.Contains(ut.out.String(), "Newer:   no") || strings.Contains(ut.out.String(), "[y/N]") {
		t.Fatalf("up to date: %d\n%s", code, ut.out)
	}

	// Roll back: asked, done, and the menu ends.
	ut.out.Reset()
	m = opsMenu(ut.dir, "14\ny\n", ut.u.ops, ut.out, ut.errOut)
	m.upd = ut.u
	if code := m.loop(); code != 0 {
		t.Fatalf("rollback: %d %s", code, ut.errOut)
	}
	if !strings.Contains(ut.out.String(), "Rolled back: BOBRES runs 1.2.3") || !strings.Contains(ut.out.String(), "The previous bobres is back") {
		t.Fatalf("rollback:\n%s\n%s", ut.out, ut.errOut)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI {
		t.Fatalf("CLI %q", b)
	}
	// No restore point left to use: said, and the menu goes on.
	ut.errOut.Reset()
	m = opsMenu(ut.dir, "14\n\n0\n", ut.u.ops, ut.out, ut.errOut)
	m.upd = ut.u
	if code := m.loop(); code != 0 || !strings.Contains(ut.errOut.String(), "already used") {
		t.Fatalf("used restore point: %d %s", code, ut.errOut)
	}
}

// The HTTPS certificate item has a menu of its own; each choice runs the
// command bobres ssl runs (core's `ssl` in its container).
func TestMenuHTTPS(t *testing.T) {
	dir := installDir(t)
	files := t.TempDir()
	certPEM, keyPEM := pemPair(t)
	certFile, keyFile := filepath.Join(files, "cert.pem"), filepath.Join(files, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil { //nolint:gosec // a certificate
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeOps{ps: healthyPS}
	in := strings.Join([]string{
		"19", "1", "", // status
		"19", "2", "", // domain
		"19", "3", "", "", // the server's IP: Enter detects it
		"19", "3", "8.8.4.4", "y", "", // another address, confirmed
		"19", "4", certFile, keyFile, "", // own certificate
		"19", "4", "", "", // own certificate, cancelled at the first question
		"19", "9", "", // no such choice
		"19", "0", "", // back
		"0", "",
	}, "\n")
	m, out, errOut, _, _ := testMenu(dir, in, f)
	var piped []pipeCall
	m.ops.probeTLS = func(context.Context, string) tlsprobe.Result { return tlsprobe.Result{} }
	m.ops.detectIP = func() string { return "93.184.216.34" }
	m.ops.pipe = func(_ context.Context, _ string, stdin io.Reader, w io.Writer, args ...string) error {
		b, _ := io.ReadAll(stdin)
		piped = append(piped, pipeCall{args, string(b)})
		_, _ = io.WriteString(w, "HTTPS is now manual.\n")
		return nil
	}
	if code := m.loop(); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	var core []string
	for _, c := range f.stream {
		core = append(core, coreArgs(c))
	}
	want := []string{
		"exec -T core /app ssl status",
		"exec -T core /app ssl auto",
		"exec -T core /app ssl ip 93.184.216.34 --detected 93.184.216.34",
		"exec -T core /app ssl ip 8.8.4.4 --detected 93.184.216.34 --yes",
	}
	if strings.Join(core, "\n") != strings.Join(want, "\n") {
		t.Fatalf("core runs\n%s\nwant\n%s", strings.Join(core, "\n"), strings.Join(want, "\n"))
	}
	if len(piped) != 1 || coreArgs(piped[0].args) != "exec -T core /app ssl manual" || !strings.Contains(piped[0].stdin, "PRIVATE KEY") {
		t.Fatalf("the own certificate: %+v", piped)
	}
	for _, s := range []string{"Let's Encrypt must reach this server", "The certificate must cover the domain", "There is no option \"9\"", "Cancelled."} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("menu output lacks %q", s)
		}
	}
	if strings.Contains(out.String(), "PRIVATE KEY") {
		t.Error("the key is in the menu's output")
	}
}
