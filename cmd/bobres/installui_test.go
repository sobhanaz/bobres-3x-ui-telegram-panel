package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
)

const allHealthy = `[{"Service":"core","State":"running","Health":"healthy"},{"Service":"payments","State":"running","Health":"healthy"},` +
	`{"Service":"provisioner","State":"running","Health":"healthy"},{"Service":"bot","State":"running","Health":"healthy"},` +
	`{"Service":"tls-agent","State":"running","Health":"healthy"},{"Service":"backup","State":"running","Health":"healthy"}]`

var percentRe = regexp.MustCompile(`(?m)^\[ *(\d+)%\]`)

func TestInstallShowsBannerStepsAndALogInPlainMode(t *testing.T) {
	dir := t.TempDir()
	ins, out, errOut := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	logPath := filepath.Join(t.TempDir(), "install.log")
	ins.logPath = logPath
	survived := 0
	ins.survive = func() { survived++ }
	if code := ins.install(baseArgs(dir)); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	s := out.String()
	if strings.ContainsAny(s, "\x1b\r") {
		t.Fatalf("escape or carriage-return bytes in plain mode")
	}
	for _, r := range s {
		if r > 126 {
			t.Fatalf("plain output is not ASCII: %q", r)
		}
	}
	if strings.Count(s, brandTagline) != 1 || !strings.Contains(s, "(c) 2026 BOBRES, all rights reserved") {
		t.Errorf("the banner once:\n%s", s)
	}
	for _, want := range []string{
		"== Setup ==", "== Install ==", "== Done ==",
		"[  0%] Step 1/8: Asking for your settings", "Step 1/8: OK Asking for your settings",
		"Step 4/8: Pulling images", "Step 6/8: Waiting for the services to be healthy (6/6 healthy)",
		"All services are healthy.", "BOBRES is ready", "[100%] Done in ",
		// the lines the tests and the people before kept
		"Bot token OK: @test_bot", "Pulling images and starting the stack", "Wrote " + dir,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	prev := -1
	for _, m := range percentRe.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		if n < prev {
			t.Fatalf("the percent went down to %d after %d", n, prev)
		}
		prev = n
	}
	if prev != 100 || strings.Count(s, "[100%]") != 1 {
		t.Errorf("ends at %d", prev)
	}
	if survived != 1 {
		t.Errorf("survive ran %d times, want once (after the questions)", survived)
	}

	// the log: mode 600, the steps and the programs' output, no secret
	st, err := os.Stat(logPath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("log: %v %v", st, err)
	}
	b, _ := os.ReadFile(logPath)
	for _, want := range []string{"==== bobres install", "Step 4/8: Pulling images", "started"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("log misses %q:\n%s", want, b)
		}
	}
	for _, secret := range []string{goodBotToken, "panel$token"} {
		if strings.Contains(string(b), secret) || strings.Contains(s, secret) {
			t.Errorf("a secret reached the log or the screen")
		}
	}
	if strings.ContainsAny(string(b), "\x1b\r") {
		t.Errorf("escape bytes in the log")
	}
}

// The card ends the install: the owner's /start is the first step.
func TestSummaryCard(t *testing.T) {
	ins, out, _ := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	ins.botName = "shop_bot"
	ins.logPath = "/var/log/bobres-install.log"
	ins.summary(map[string]string{"BOBRES_DOMAIN": "panel.example.com", "BOBRES_ADMIN_TELEGRAM_ID": "4242", "BOBRES_VERSION": "1.2.3",
		"BOBRES_XUI_URL": "https://xui.example.com", "COMPOSE_PROFILES": "backup,tls", "BOBRES_BACKUP_TIME": "03:30", "BOBRES_TIMEZONE": "Asia/Tehran"}, "/opt/bobres")
	s := out.String()
	for _, want := range []string{
		"BOBRES is ready", "https://panel.example.com", "@shop_bot", "sudo bobres admin link", "/opt/bobres  (version 1.2.3)", "/var/log/bobres-install.log",
		"Health", "healthy", "License", "30-day trial", "Docs", brandIssues, brandSecurity, "(c) 2026 BOBRES, all rights reserved\nMade by TECSO-DEV",
		"Off-site backups (optional): sudo bobres install --s3-endpoint",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the card misses %q:\n%s", want, s)
		}
	}
	first := strings.Index(s, "1. Open @shop_bot in Telegram and send /start from the owner's account (id 4242)")
	second := strings.Index(s, "2. Open the dashboard: send /dashboard to the bot, or run  sudo bobres admin link")
	if first < 0 || second < first {
		t.Errorf("the first next step is /start to the bot, then the dashboard:\n%s", s)
	}
}

func TestSummaryLicenseLine(t *testing.T) {
	docker := &licenseDocker{out: `{"effective":{"basis":"trial","tier":"trial","trial_days_left":27,"trial_days":30}}`}
	ins, _, _ := testInstaller("", nil, nil)
	ins.run = docker.run
	if got := ins.licenseLine("/opt/bobres"); !strings.HasPrefix(got, "30-day trial, 27 days left") {
		t.Errorf("trial: %q", got)
	}
	docker.out = `{"effective":{"basis":"license","tier":"pro"}}`
	if got := ins.licenseLine("/opt/bobres"); got != "license active (pro)" {
		t.Errorf("license: %q", got)
	}
	docker.out = "not json"
	if got := ins.licenseLine("/opt/bobres"); !strings.Contains(got, "30-day trial from the first start") {
		t.Errorf("fallback: %q", got)
	}
}

type licenseDocker struct{ out string }

func (l *licenseDocker) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	if contains(args, "license") {
		return []byte(l.out), nil
	}
	return nil, nil
}

func TestInstallFailureKeepsThePercentAndShowsTheLog(t *testing.T) {
	dir := t.TempDir()
	docker := &fakeDocker{upErr: errors.New("exit status 1")}
	ins, out, errOut := testInstaller("", secrets, docker)
	ins.logPath = filepath.Join(t.TempDir(), "install.log")
	ins.survive = func() {}
	if code := ins.install(append(baseArgs(dir), "--no-harden")); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	// wizard 8 + checks 7 + write 5 = 20% when the pull step fails
	if !strings.Contains(out.String(), "[ 20%] Step 4/8: FAIL Pulling images (exit 1)") {
		t.Errorf("the failure line:\n%s", out)
	}
	if strings.Contains(out.String(), "[100%]") || strings.Contains(out.String(), "Done in") || strings.Contains(out.String(), "BOBRES is ready") {
		t.Errorf("a failed install said it was done:\n%s", out)
	}
	for _, want := range []string{"error: docker compose up failed", "The last lines of ", "What to do: fix the cause named above, then run  sudo bobres install  again"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("errOut misses %q:\n%s", want, errOut)
		}
	}
}

func TestInstallContinuesAScriptsBar(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"BOBRES_PROGRESS": "30:70:5:13", "BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken, "BOBRES_XUI_TOKEN": "panel$token"}
	ins, out, errOut := testInstaller("", env, &fakeDocker{health: []string{allHealthy}})
	ins.survive = func() {}
	if code := ins.install(append(baseArgs(dir), "--no-harden")); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	s := out.String()
	if strings.Contains(s, brandTagline) {
		t.Errorf("a second banner after install.sh's:\n%s", s)
	}
	for _, want := range []string{"[ 30%] Step 6/13: Asking for your settings", "Step 13/13: ", "[100%] Done in "} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if m := percentRe.FindStringSubmatch(s); m == nil || m[1] != "30" {
		t.Errorf("the bar must start at 30%%: %v", m)
	}
}

func TestInstallBannerSwitches(t *testing.T) {
	run := func(env map[string]string, extra ...string) string {
		t.Helper()
		for k, v := range secrets {
			env[k] = v
		}
		ins, out, errOut := testInstaller("", env, nil)
		ins.survive = func() {}
		if code := ins.install(append(append(baseArgs(t.TempDir()), "--no-start"), extra...)); code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		return out.String()
	}
	if s := run(map[string]string{}); !strings.Contains(s, brandTagline) {
		t.Errorf("the banner is shown for a plain install log:\n%s", s)
	}
	if s := run(map[string]string{"BOBRES_NO_BANNER": "1"}); strings.Contains(s, brandTagline) || !strings.Contains(s, "Step 1/8") {
		t.Errorf("BOBRES_NO_BANNER:\n%s", s)
	}
	uiOpts = uiOptions{noBanner: true}
	if s := run(map[string]string{}); strings.Contains(s, brandTagline) {
		t.Errorf("--no-banner:\n%s", s)
	}
	uiOpts = uiOptions{}
	if s := run(map[string]string{}, "--quiet"); strings.Contains(s, brandTagline) {
		t.Errorf("--quiet:\n%s", s)
	}
}

// On a terminal the install draws the bar, never over a question, and leaves the cursor shown.
func TestInstallOnATerminal(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"BOBRES_FORCE_TTY": "1", "TERM": "xterm-256color", "LANG": "en_US.UTF-8", "COLUMNS": "100"}
	ins, out, errOut := testInstaller("panel.example.com\n4242\n"+goodBotToken+"\n\n", env, &fakeDocker{health: []string{allHealthy}})
	ins.survive = func() {}
	if code := ins.install([]string{"--dir", dir, "--no-harden"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	s := out.String()
	for _, want := range []string{"\x1b[K", "█", "✔", "©"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if lastCursorCommand(s) == "hide" {
		t.Errorf("the cursor was left hidden")
	}
	// a question is printed as it is: no redraw directly after it, and the next text starts where the answer's line ends
	for _, q := range []string{"Domain of this install (e.g. panel.example.com): ", "Telegram bot token from @BotFather: "} {
		i := strings.Index(s, q)
		if i < 0 {
			t.Fatalf("no prompt %q", q)
		}
		if strings.HasPrefix(s[i+len(q):], "\r\x1b[K") {
			t.Errorf("the bar was drawn over the prompt %q", q)
		}
	}
	for _, seg := range strings.FieldsFunc(stripSGR(s), func(r rune) bool { return r == '\n' || r == '\r' }) {
		if strings.HasPrefix(seg, "[") && strings.Contains(seg, "%") && width(seg) > 99 {
			t.Errorf("a bar line of %d columns: %q", width(seg), seg)
		}
	}
}

// The terminal going away (a dropped SSH connection) must not stop the install:
// every write fails, and the stack still comes up and the files are written.
type deadTerminal struct{}

func (deadTerminal) Write([]byte) (int, error) { return 0, syscall.EIO }

func TestInstallSurvivesADeadTerminal(t *testing.T) {
	dir := t.TempDir()
	ins, _, _ := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	ins.out, ins.errOut = deadTerminal{}, deadTerminal{}
	survived := 0
	ins.survive = func() { survived++ }
	if code := ins.install(append(baseArgs(dir), "--no-harden")); code != 0 || survived != 1 {
		t.Fatalf("exit %d, survive %d", code, survived)
	}
	if _, ok, _ := readEnv(filepath.Join(dir, ".env")); !ok {
		t.Fatal("the install did not write its files")
	}
}

func TestKeepGoingIgnoresHangupsAndBrokenPipes(t *testing.T) {
	keepGoing() // must not panic; the signals stay ignored for the test binary, which is fine
}

// Hardening: automatic by default, questions only with --ask-harden, never with --yes or --no-harden.
func TestInstallHardeningModes(t *testing.T) {
	for _, c := range []struct {
		args    []string
		called  bool
		auto    bool
		skipped string
	}{
		{nil, true, true, ""},
		{[]string{"--ask-harden"}, true, false, ""},
		{[]string{"--no-harden"}, false, false, "SKIP Hardening the server (--no-harden)"},
		{[]string{"--yes"}, false, false, "SKIP Hardening the server (--yes never asks)"},
	} {
		ins, out, errOut := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
		ins.survive = func() {}
		var got hardenOptions
		called := false
		ins.harden = func(_ context.Context, opt hardenOptions) int { called, got = true, opt; return 0 }
		if code := ins.install(append(baseArgs(t.TempDir()), c.args...)); code != 0 {
			t.Fatalf("%v: exit %d: %s", c.args, code, errOut)
		}
		if called != c.called || got.auto != c.auto {
			t.Errorf("%v: called %v auto %v, want %v %v", c.args, called, got.auto, c.called, c.auto)
		}
		if c.skipped != "" && !strings.Contains(out.String(), c.skipped) {
			t.Errorf("%v: no %q in:\n%s", c.args, c.skipped, out)
		}
	}
	// and the install's own hardening, run for real on a fake system, shows one line per step
	ins, out, _ := testInstaller("", secrets, &fakeDocker{health: []string{allHealthy}})
	ins.survive = func() {}
	sys := freshServer()
	ins.harden = func(ctx context.Context, opt hardenOptions) int {
		h, _, _ := testHardener(sys, "")
		h.out, h.errOut, h.notify = ins.logOut(), ins.logOut(), ins.prog.Note
		return h.apply(ctx, opt)
	}
	if code := ins.install(baseArgs(t.TempDir())); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Step 7/8: OK Firewall (ufw): on: SSH 22", "Step 7/8: OK SSH guard (fail2ban): on:", "Step 7/8: OK Security updates: on:", "Step 7/8: OK Swap file:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), "> ufw") || strings.Contains(out.String(), "apt-get") {
		t.Errorf("the commands' output is for the log:\n%s", out)
	}
}

func TestInstallTakesTheDomainAndOwnerFromTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"BOBRES_DOMAIN": "env.example.com", "BOBRES_ADMIN_TELEGRAM_ID": "777", "BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken}
	ins, out, errOut := testInstaller("\n", env, nil) // Enter at the optional panel question
	ins.survive = func() {}
	if code := ins.install([]string{"--dir", dir, "--no-start", "--offline"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	got, _, _ := readEnv(filepath.Join(dir, ".env"))
	if got["BOBRES_DOMAIN"] != "env.example.com" || got["BOBRES_ADMIN_TELEGRAM_ID"] != "777" || strings.Contains(out.String(), "Domain of this install") || strings.Contains(out.String(), "numeric Telegram user id") {
		t.Fatalf("env %v\n%s", got, out)
	}
	// a flag wins over the environment
	ins, _, _ = testInstaller("\n", env, nil)
	ins.survive = func() {}
	dir = t.TempDir()
	if code := ins.install([]string{"--dir", dir, "--no-start", "--offline", "--domain", "flag.example.com"}); code != 0 {
		t.Fatal(code)
	}
	if got, _, _ := readEnv(filepath.Join(dir, ".env")); got["BOBRES_DOMAIN"] != "flag.example.com" {
		t.Fatalf("the flag lost: %v", got["BOBRES_DOMAIN"])
	}
}

// ---- the panel check in the wizard

type probeCall struct {
	url, token string
	private    bool
}

// fakeProbe plays the panel: it takes the token good, answers version, and fails
// with err otherwise.
func fakeProbe(good, version string, err error, calls *[]probeCall) func(context.Context, string, string, bool) (panelInfo, error) {
	return func(_ context.Context, u, token string, private bool) (panelInfo, error) {
		*calls = append(*calls, probeCall{u, token, private})
		switch {
		case err != nil:
			return panelInfo{}, err
		case token != good:
			return panelInfo{}, &xui.APIError{Status: 401, Msg: "unauthorized"}
		}
		return panelInfo{version: version, inbounds: 2}, nil
	}
}

func panelArgs(dir string, extra ...string) []string {
	return append([]string{"--dir", dir, "--domain", "panel.example.com", "--admin-id", "4242", "--no-start",
		"--xui-url", "https://xui.example.com:2053/secret"}, extra...)
}

func panelEnv(token string) map[string]string {
	return map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken, "BOBRES_XUI_TOKEN": token}
}

func TestWizardChecksThePanelBeforeWriting(t *testing.T) {
	// accepted
	var calls []probeCall
	dir := t.TempDir()
	ins, out, errOut := testInstaller("", panelEnv("good"), nil)
	ins.survive, ins.panelProbe = func() {}, fakeProbe("good", "v3.9.0", nil, &calls)
	if code := ins.install(panelArgs(dir)); code != 0 || len(calls) != 1 || calls[0].url != "https://xui.example.com:2053/secret" || calls[0].private {
		t.Fatalf("accepted: %d %+v\n%s", code, calls, errOut)
	}
	if !strings.Contains(out.String(), "3x-ui panel OK: v3.9.0, API token accepted, 2 inbound(s)") {
		t.Errorf("the OK line:\n%s", out)
	}

	// a refused token is never accepted silently: what to do is printed, the owner is asked again
	calls = nil
	dir = t.TempDir()
	ins, out, errOut = testInstaller("good\n", panelEnv("wrong"), nil)
	_ = out
	ins.survive, ins.panelProbe = func() {}, fakeProbe("good", "v3.9.0", nil, &calls)
	if code := ins.install(panelArgs(dir)); code != 0 || len(calls) != 2 || calls[1].token != "good" {
		t.Fatalf("retyped token: %d %+v\n%s", code, calls, errOut)
	}
	for _, want := range []string{"3x-ui panel check failed: the panel refused the API token.", panelTokenFix} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("errOut misses %q:\n%s", want, errOut)
		}
	}
	if got, _, _ := readEnv(filepath.Join(dir, ".env")); got["BOBRES_XUI_TOKEN"] != "good" {
		t.Errorf("the token that was written: %q", got["BOBRES_XUI_TOKEN"])
	}

	// refused again and again: three new tries, then it stops and nothing is written
	calls = nil
	dir = t.TempDir()
	ins, _, errOut = testInstaller("w2\nw3\nw4\n", panelEnv("wrong"), nil)
	ins.survive, ins.panelProbe = func() {}, fakeProbe("good", "v3.9.0", nil, &calls)
	if code := ins.install(panelArgs(dir)); code != 2 || len(calls) != 4 || !strings.Contains(errOut.String(), "refused 4 times, so nothing was written") {
		t.Fatalf("four refusals: %d %d calls\n%s", code, len(calls), errOut)
	}
	if _, written, _ := readEnv(filepath.Join(dir, ".env")); written {
		t.Error("a refused panel left an install behind")
	}

	// Enter skips the panel: the install goes on without it
	calls = nil
	dir = t.TempDir()
	ins, out, _ = testInstaller("\n", panelEnv("wrong"), nil)
	ins.survive, ins.panelProbe = func() {}, fakeProbe("good", "v3.9.0", nil, &calls)
	if code := ins.install(panelArgs(dir)); code != 0 || !strings.Contains(out.String(), "Skipped: the install goes on without a 3x-ui panel") {
		t.Fatalf("skip: %d\n%s", code, out)
	}
	if got, _, _ := readEnv(filepath.Join(dir, ".env")); got["BOBRES_XUI_URL"] != "" || got["BOBRES_XUI_TOKEN"] != "" {
		t.Errorf("a skipped panel was written: %v", got)
	}

	// nobody to ask (--yes, or no input): an error, not a silent accept
	dir = t.TempDir()
	ins, _, errOut = testInstaller("", panelEnv("wrong"), nil)
	ins.survive, ins.panelProbe = func() {}, fakeProbe("good", "v3.9.0", nil, &calls)
	if code := ins.install(append(panelArgs(dir), "--yes")); code != 2 || !strings.Contains(errOut.String(), "no one to ask again") {
		t.Fatalf("--yes: %d\n%s", code, errOut)
	}
	if _, written, _ := readEnv(filepath.Join(dir, ".env")); written {
		t.Error("--yes with a refused token wrote files")
	}

	// a wrong URL is asked for again too
	calls = nil
	probe := func(_ context.Context, u, token string, private bool) (panelInfo, error) {
		calls = append(calls, probeCall{u, token, private})
		if strings.Contains(u, "typo") {
			return panelInfo{}, &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "typo.example.com"}}
		}
		return panelInfo{version: "v3.8.5", inbounds: 1}, nil
	}
	dir = t.TempDir()
	ins, _, errOut = testInstaller("https://xui.example.com:2053/secret\n", panelEnv("t"), nil)
	ins.survive, ins.panelProbe = func() {}, probe
	if code := ins.install(append(panelArgs(dir)[:7:7], "--xui-url", "https://typo.example.com/x")); code != 0 || len(calls) != 2 ||
		!strings.Contains(errOut.String(), "the name typo.example.com does not resolve (DNS)") {
		t.Fatalf("typo: %d %+v\n%s", code, calls, errOut)
	}

	// not checked: --offline, and a re-run that changes nothing
	calls = nil
	dir = t.TempDir()
	ins, _, _ = testInstaller("", panelEnv("t"), nil)
	ins.survive, ins.panelProbe = func() {}, fakeProbe("good", "v3.9.0", nil, &calls)
	if code := ins.install(append(panelArgs(dir), "--offline")); code != 0 || len(calls) != 0 {
		t.Fatalf("--offline: %d, %d probes", code, len(calls))
	}
}

// A panel on this server is reached by the containers as host.docker.internal,
// which only resolves in them: the check goes to 127.0.0.1 and says so.
func TestWizardChecksALocalPanelFromTheHost(t *testing.T) {
	var calls []probeCall
	dir := t.TempDir()
	ins, out, _ := testInstaller("", panelEnv("good"), nil)
	ins.survive, ins.panelProbe = func() {}, fakeProbe("good", "v3.9.0", nil, &calls)
	args := append(panelArgs(dir)[:7:7], "--xui-url", "http://127.0.0.1:2053/abc")
	if code := ins.install(args); code != 0 || len(calls) != 1 || calls[0].url != "http://127.0.0.1:2053/abc" || !calls[0].private {
		t.Fatalf("%d %+v", code, calls)
	}
	if !strings.Contains(out.String(), "checked from this server at 127.0.0.1:2053; the containers reach it as host.docker.internal") {
		t.Errorf("the note about the container's view:\n%s", out)
	}
	if got, _, _ := readEnv(filepath.Join(dir, ".env")); got["BOBRES_XUI_URL"] != "http://host.docker.internal:2053/abc" {
		t.Errorf("written URL %q", got["BOBRES_XUI_URL"])
	}
	if u, on := panelCheckURL("http://host.docker.internal:2053/abc"); u != "http://127.0.0.1:2053/abc" || !on {
		t.Errorf("panelCheckURL: %q %v", u, on)
	}
	if u, on := panelCheckURL("https://xui.example.com/x"); u != "https://xui.example.com/x" || on {
		t.Errorf("panelCheckURL leaves a remote panel alone: %q %v", u, on)
	}
}

func TestPanelProblemsAreSaidInPlainWords(t *testing.T) {
	timeout := &net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}
	for name, c := range map[string]struct {
		info       panelInfo
		err        error
		kind, want string
	}{
		"fine":         {panelInfo{version: "v3.9.0"}, nil, "", ""},
		"unknown ver.": {panelInfo{version: "v3.x"}, nil, "", ""},
		"token":        {panelInfo{}, fmt.Errorf("xui: %w", &xui.APIError{Status: 401, Msg: "x"}), "token", "the panel refused the API token"},
		"forbidden":    {panelInfo{}, &xui.APIError{Status: 403, Msg: "x"}, "token", "the panel refused the API token"},
		"dns":          {panelInfo{}, &net.OpError{Err: &net.DNSError{Err: "no such host", Name: "x.example.com"}}, "url", "the name x.example.com does not resolve (DNS)"},
		"refused":      {panelInfo{}, &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, "url", "nothing listens on h:2053"},
		"timeout":      {panelInfo{}, context.DeadlineExceeded, "url", "no answer from h:2053 in time"},
		"timeout net":  {panelInfo{}, timeout, "url", "no answer from h:2053 in time"},
		"tls":          {panelInfo{}, x509.UnknownAuthorityError{}, "url", "HTTPS certificate is not trusted"},
		"plain http":   {panelInfo{}, errors.New("tls: first record does not look like a TLS handshake"), "url", "speaks plain http"},
		"base path":    {panelInfo{}, &xui.APIError{Status: 404, Msg: "unexpected non-JSON response"}, "url", "not as the 3x-ui API (HTTP 404)"},
		"insecure":     {panelInfo{}, xui.ErrInsecureURL, "url", "https"},
		"old version":  {panelInfo{version: "v3.7.0"}, nil, "", ""}, // untested, not refused: verifyPanel notes it
		"new version":  {panelInfo{version: "v3.10.1"}, nil, "", ""},
		"other":        {panelInfo{}, errors.New("boom"), "url", "could not talk to the panel: boom"},
	} {
		got := panelProblemOf(c.info, c.err, "h:2053")
		switch {
		case c.kind == "" && got != nil:
			t.Errorf("%s: %+v", name, got)
		case c.kind != "" && (got == nil || got.kind != c.kind || !strings.Contains(got.msg, c.want) || got.fix == ""):
			t.Errorf("%s: %+v, want %s %q", name, got, c.kind, c.want)
		}
	}
}

// The real probe against the provisioner's fake panel: the same client, the
// same calls. A good token passes; a wrong one, a wrong version and a closed
// port are told apart.
func TestProbePanelAgainstAFakePanel(t *testing.T) {
	fake := xuifake.NewServer("secret-token")
	defer fake.Close()
	ctx := context.Background()
	info, err := probePanel(ctx, fake.URL, "secret-token", true)
	if err != nil || info.version != "v3.9.0" || info.inbounds != 3 || panelProblemOf(info, err, "x") != nil {
		t.Fatalf("good token: %+v %v", info, err)
	}
	_, err = probePanel(ctx, fake.URL, "wrong-token", true)
	if p := panelProblemOf(panelInfo{}, err, "x"); p == nil || p.kind != "token" || p.fix != panelTokenFix {
		t.Fatalf("wrong token: %v %+v", err, p)
	}
	fake.SetPanelVersion("3.7.0")
	info, err = probePanel(ctx, fake.URL, "secret-token", true)
	if p := panelProblemOf(info, err, "x"); p != nil || err != nil || info.version != "v3.7.0" {
		t.Fatalf("old version is noted, not refused: %+v %v %+v", info, err, p)
	}
	_, err = probePanel(ctx, fake.URL+"/wrong/base/path", "secret-token", true)
	if p := panelProblemOf(panelInfo{}, err, "x"); p == nil || !strings.Contains(p.msg, "not as the 3x-ui API") {
		t.Fatalf("wrong base path: %v %+v", err, p)
	}
	// plain http to a private address needs allow-private: the same rule the provisioner applies
	if _, err = probePanel(ctx, fake.URL, "secret-token", false); !errors.Is(err, xui.ErrInsecureURL) {
		t.Fatalf("plain http without allow-private: %v", err)
	}
	url := fake.URL
	fake.Close()
	_, err = probePanel(ctx, url, "secret-token", true)
	if p := panelProblemOf(panelInfo{}, err, "x"); p == nil || !strings.Contains(p.msg, "nothing listens on x") {
		t.Fatalf("closed port: %v %+v", err, p)
	}
}

// admin link: a Telegram id that is not staff yet gets one actionable sentence.
type exitErr struct{ code int }

func (e exitErr) Error() string { return "exit status " + strconv.Itoa(e.code) }
func (e exitErr) ExitCode() int { return e.code }

func TestAdminLinkBeforeTheOwnerStartedTheBot(t *testing.T) {
	f := &fakeOps{}
	o, out, errOut := testOps("", f)
	o.lookupBot = func(context.Context, string) string { return "shop_bot" }
	o.stream = func(context.Context, string, ...string) error { return exitErr{coreExitNotStaff} }
	if code := o.adminCmd([]string{"link", "--dir", installDir(t)}); code != 1 || out.Len() != 0 ||
		errOut.String() != "Open @shop_bot in Telegram and send /start once, then run this again.\nNo Telegram at all? Use: bobres admin password\n" {
		t.Fatalf("exit %d out %q err %q", code, out, errOut)
	}
	// the bot's name unknown (Telegram unreachable): still one sentence
	o, _, errOut = testOps("", f)
	o.lookupBot = func(context.Context, string) string { return "" }
	o.stream = func(context.Context, string, ...string) error { return exitErr{coreExitNotStaff} }
	if code := o.adminCmd([]string{"link", "--dir", installDir(t)}); code != 1 ||
		errOut.String() != "Open your bot in Telegram and send /start once, then run this again.\nNo Telegram at all? Use: bobres admin password\n" {
		t.Fatalf("no name: %d %q", code, errOut)
	}
	// any other failure keeps its own message
	o, _, errOut = testOps("", f)
	o.stream = func(context.Context, string, ...string) error { return exitErr{1} }
	if code := o.adminCmd([]string{"link", "--dir", installDir(t)}); code != 1 || !strings.Contains(errOut.String(), "is core running?") {
		t.Fatalf("other failure: %d %q", code, errOut)
	}
	if got := notStaffAdvice("@x"); got != "Open @x in Telegram and send /start once, then run this again." {
		t.Fatalf("%q", got)
	}
}

// bobres update: the banner, its five steps, and the share of the bar it gives
// the install it starts (that one is another process, with its own steps).
func TestUpdateShowsProgressAndHandsTheBarToTheInstall(t *testing.T) {
	ut := newUpdateTest(t, "")
	var childEnv []string
	run := ut.u.runCLI
	ut.u.runCLI = func(ctx context.Context, exe string, args ...string) error {
		childEnv = append([]string(nil), ut.u.childEnv...)
		return run(ctx, exe, args...)
	}
	ut.cli.do = installs(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("update: %d\n%s\n%s", code, ut.out, ut.errOut)
	}
	s := ut.out.String()
	if strings.ContainsAny(s, "\x1b\r") || strings.Count(s, brandTagline) != 1 {
		t.Fatalf("plain output with one banner:\n%s", s)
	}
	for _, want := range []string{
		"Step 1/5: Checking the release signature", "Step 2/5: Downloading the new bobres", "Step 3/5: Backing up the database",
		"[ 35%] Step 4/5: Installing the new release", "Step 4/5: OK Installing the new release (", "[ 85%] Step 5/5: Waiting for the services to be healthy",
		"[100%] Done in ", "BOBRES is updated to 1.3.0",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if len(childEnv) < 2 || childEnv[0] != "BOBRES_PROGRESS=35:50:0:0" || childEnv[1] != "BOBRES_PLAIN=1" {
		t.Errorf("the install's share: %v", childEnv)
	}
	if ut.u.childEnv != nil || ut.u.prog != nil {
		t.Errorf("progress state left behind")
	}
}

func TestUpdateNothingToDoAndCancelEndWithoutAResultLine(t *testing.T) {
	ut := newUpdateTest(t, "n\n")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir}); code != 1 {
		t.Fatalf("cancel: %d", code)
	}
	if s := ut.out.String(); strings.Contains(s, "FAIL") || strings.Contains(s, "[100%]") || !strings.Contains(s, "Cancelled. Nothing was changed.") {
		t.Fatalf("a cancelled update:\n%s", s)
	}
	ut = newUpdateTest(t, "")
	ut.u.current = "v1.3.0"
	setEnv(t, ut.dir, map[string]string{"BOBRES_VERSION": "1.3.0"})
	if code := ut.u.updateCmd([]string{"--dir", ut.dir}); code != 0 || strings.Contains(ut.out.String(), "[100%]") ||
		!strings.Contains(ut.out.String(), "nothing to update") {
		t.Fatalf("nothing to update: %d\n%s", code, ut.out)
	}
}

func TestUpdateFailureLeavesAFailLine(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.cli.do = func() error { return errors.New("exit status 1") }
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("update: %d", code)
	}
	s := ut.out.String()
	if !strings.Contains(s, "Step 4/5: FAIL Installing the new release (rolling back)") || strings.Contains(s, "[100%]") {
		t.Fatalf("the failure line:\n%s", s)
	}
}
