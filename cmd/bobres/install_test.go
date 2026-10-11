package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

const goodBotToken = "123456789:AAH-abcdefghijklmnopqrstuvwxyz012345" //nolint:gosec // test fixture, gitleaks:allow

type fakeDocker struct {
	calls  [][]string
	health []string // successive `ps` outputs
	upErr  error
}

func (f *fakeDocker) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch {
	case contains(args, "up"):
		return []byte("started"), f.upErr
	case contains(args, "ps"):
		if len(f.health) == 0 {
			return []byte(`[]`), nil
		}
		out := f.health[0]
		if len(f.health) > 1 {
			f.health = f.health[1:]
		}
		return []byte(out), nil
	}
	return nil, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func testInstaller(stdinText string, env map[string]string, docker *fakeDocker) (*installer, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	if docker == nil {
		docker = &fakeDocker{}
	}
	return &installer{
		in: bufio.NewReader(strings.NewReader(stdinText)), out: &out, errOut: &errOut,
		getenv:   func(k string) string { return env[k] },
		run:      docker.run,
		checkBot: func(context.Context, string) (string, error) { return "test_bot", nil },
		lookupIP: func(_ context.Context, host string) ([]net.IP, error) {
			if host == "public.example.com" {
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			}
			return []net.IP{net.ParseIP("10.1.2.3")}, nil
		},
		preflight: func(string) []Result { return []Result{{"OS/arch", Pass, "linux/amd64"}} },
		sleep:     func(time.Duration) {},
	}, &out, &errOut
}

var secrets = map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken, "BOBRES_XUI_TOKEN": "panel$token"}

func baseArgs(dir string) []string {
	return []string{"--dir", dir, "--domain", "panel.example.com", "--admin-id", "4242",
		"--xui-url", "https://xui.example.com:2053/secret", "--xui-sub-url", "https://xui.example.com:2096/sub/"}
}

func TestFreshInstallWritesAWorkingStack(t *testing.T) {
	dir := t.TempDir()
	docker := &fakeDocker{health: []string{
		`[{"Service":"core","Health":"starting"}]`,
		`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`,
	}}
	ins, out, errOut := testInstaller("", secrets, docker)
	if code := ins.install(baseArgs(dir)); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out.String(), "All services are healthy") || !strings.Contains(out.String(), "/plan_add") {
		t.Fatalf("output: %s", out)
	}
	st, err := os.Stat(filepath.Join(dir, ".env"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode: %v %v", st.Mode(), err)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	optional := map[string]bool{"BOBRES_XUI_SUB_URL": true, "BOBRES_ZARINPAL_MERCHANT_ID": true,
		"BOBRES_ZARINPAL_PROXY": true, "BOBRES_ZARINPAL_PUBLIC_URL": true, "BOBRES_ZARINPAL_HOST": true}
	for _, k := range envOrder {
		if !optional[k] && env[k] == "" {
			t.Errorf("%s missing", k)
		}
		if strings.Contains(env[k], "CHANGE_ME") {
			t.Errorf("%s still a placeholder", k)
		}
	}
	if len(env["BOBRES_TOKEN_CORE"]) != 64 || env["BOBRES_TOKEN_CORE"] == env["BOBRES_TOKEN_BOT"] {
		t.Errorf("service tokens: %q %q", env["BOBRES_TOKEN_CORE"], env["BOBRES_TOKEN_BOT"])
	}
	if env["BOBRES_REDIS_URL"] != "redis://:"+env["REDIS_PASSWORD"]+"@redis:6379/0" {
		t.Errorf("redis url does not match the password")
	}
	if env["BOBRES_XUI_TOKEN"] != "panel$token" {
		t.Errorf("token with $ mangled: %q", env["BOBRES_XUI_TOKEN"])
	}
	compose, _ := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if strings.Contains(string(compose), "build:") || strings.Contains(string(compose), "x-build") {
		t.Fatal("installed compose file still builds from source")
	}
	if !contains(docker.calls[0], "up") {
		t.Fatalf("docker calls: %v", docker.calls)
	}

	// Backups: on (this CLI's own images have the service), the key shown
	// once, the read-only database role created in the running database.
	if !backupEnabled(env) || env["BOBRES_BACKUP_URL"] != "http://backup:8080" || env["BOBRES_BACKUP_TIME"] != "03:30" {
		t.Errorf("backups not enabled: %q %q", env["COMPOSE_PROFILES"], env["BOBRES_BACKUP_URL"])
	}
	key := env["BOBRES_BACKUP_KEY"]
	if len(key) != 64 || key == env["BOBRES_CORE_MASTER_KEY"] || len(env["DB_BACKUP_PASSWORD"]) != 48 {
		t.Errorf("backup key %d chars, db password %d chars", len(key), len(env["DB_BACKUP_PASSWORD"]))
	}
	if strings.Count(out.String(), key) != 1 || !strings.Contains(out.String(), "without it the backups cannot be read") ||
		!strings.Contains(out.String(), "bobres backup") {
		t.Errorf("backup key not shown exactly once:\n%s", out)
	}
	role := docker.calls[1]
	if !contains(role, "exec") || !contains(role, "postgres") || !strings.Contains(strings.Join(role, " "), "svc_backup") ||
		strings.Contains(strings.Join(role, " "), env["DB_BACKUP_PASSWORD"]) {
		t.Errorf("backup role call: %v", role)
	}
	if st, err := os.Stat(filepath.Join(dir, "backups")); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
		t.Errorf("backups directory: %v %v", st, err)
	}

	// The generated files must be a valid Compose project (no daemon needed).
	if _, err := exec.LookPath("docker"); err == nil {
		cmd := exec.Command("docker", "compose", "-f", "docker-compose.yml", "--env-file", ".env", "config", "--format", "json")
		cmd.Dir = dir
		got, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("docker compose config: %v\n%s", err, got)
		}
		// `config` prints a literal $ as $$; unquoted, Compose would have
		// expanded $token and passed just "panel".
		if !strings.Contains(string(got), `"panel$$token"`) {
			t.Fatalf("compose interpolated the $ inside the quoted token:\n%s", got)
		}
		// COMPOSE_PROFILES in .env turns the backup service on.
		if !strings.Contains(string(got), `/bobres-backup:`) || !strings.Contains(string(got), `"BOBRES_BACKUP_URL": "http://backup:8080"`) {
			t.Fatalf("the backup service is not in the installed project:\n%s", got)
		}
	}
}

func TestReinstallKeepsSecrets(t *testing.T) {
	dir := t.TempDir()
	ins, _, errOut := testInstaller("", secrets, nil)
	args := append(baseArgs(dir), "--no-start")
	if code := ins.install(args); code != 0 {
		t.Fatalf("first: %d %s", code, errOut)
	}
	first, _, _ := readEnv(filepath.Join(dir, ".env"))
	f, _ := os.OpenFile(filepath.Join(dir, ".env"), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("BOBRES_LOG_LEVEL=debug\nOPERATOR_NOTE=keep\n")
	_ = f.Close()

	ins2, out, errOut := testInstaller("", nil, nil) // no env: everything comes from the existing .env
	defer func() {
		if strings.Contains(out.String(), first["BOBRES_BACKUP_KEY"]) {
			t.Error("the backup key was shown again on a re-run")
		}
	}()
	if code := ins2.install([]string{"--dir", dir, "--no-start", "--domain", "new.example.com"}); code != 0 {
		t.Fatalf("rerun: %d %s", code, errOut)
	}
	second, _, _ := readEnv(filepath.Join(dir, ".env"))
	for k := range generated {
		if first[k] != second[k] {
			t.Errorf("%s changed on re-run (would lock services out of the database)", k)
		}
	}
	if second["BOBRES_DOMAIN"] != "new.example.com" || second["OPERATOR_NOTE"] != "keep" || second["BOBRES_LOG_LEVEL"] != "debug" {
		t.Errorf("re-run lost settings: %v", second)
	}
	if !strings.Contains(out.String(), "Existing install") {
		t.Errorf("output: %s", out)
	}
}

func TestInteractivePrompts(t *testing.T) {
	dir := t.TempDir()
	answers := "panel.example.com\n4242\n" + goodBotToken + "\nhttps://xui.example.com\npaneltoken\n"
	ins, out, errOut := testInstaller(answers, nil, nil)
	if code := ins.install([]string{"--dir", dir, "--no-start"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out.String(), "Telegram bot token from @BotFather") {
		t.Fatalf("prompts: %s", out)
	}
}

// A panel on the same server, typed as the owner knows it (localhost), is
// rewritten to the address the containers can reach.
func TestInteractivePanelOnThisServer(t *testing.T) {
	for _, c := range []struct {
		typed, answer, wantURL, wantPrivate string
	}{
		{"http://127.0.0.1:2053/abc", "", "http://host.docker.internal:2053/abc", "true"},
		{"http://localhost:2053/abc", "n", "", ""}, // declined: refused by validation, nothing written
		{"http://10.0.0.5:2053/abc", "y", "http://10.0.0.5:2053/abc", "true"},
		{"https://xui.example.com/abc", "", "https://xui.example.com/abc", "false"}, // not asked
	} {
		dir := t.TempDir()
		answers := "panel.example.com\n4242\n" + goodBotToken + "\n" + c.typed + "\n"
		if !strings.HasPrefix(c.typed, "https") {
			answers += c.answer + "\n"
		}
		answers += "paneltoken\n"
		ins, out, errOut := testInstaller(answers, nil, nil)
		code := ins.install([]string{"--dir", dir, "--no-start"})
		env, written, _ := readEnv(filepath.Join(dir, ".env"))
		if c.wantURL == "" {
			if code == 0 || written {
				t.Fatalf("%s declined must be refused before writing: %d %s", c.typed, code, errOut)
			}
			continue
		}
		if code != 0 || env["BOBRES_XUI_URL"] != c.wantURL || env["BOBRES_XUI_ALLOW_PRIVATE"] != c.wantPrivate {
			t.Fatalf("%s: %d url=%q private=%q\n%s%s", c.typed, code, env["BOBRES_XUI_URL"], env["BOBRES_XUI_ALLOW_PRIVATE"], out, errOut)
		}
	}
}

func TestInstallRejectsBadInput(t *testing.T) {
	cases := map[string][]string{
		"bad domain":   {"--domain", "not a domain"},
		"bad admin id": {"--admin-id", "12ab"},
		"http panel":   {"--xui-url", "http://xui.example.com"},
		"loopback":     {"--xui-url", "http://127.0.0.1:2053", "--xui-allow-private"},
		"bad merchant": {"--zarinpal-merchant-id", "12345"},
		"http zp url":  {"--zarinpal-merchant-id", "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b", "--zarinpal-public-url", "http://pay.example.ir"},
		"localhost":    {"--xui-url", "https://localhost:2053"},
	}
	for name, override := range cases {
		dir := t.TempDir()
		args := append(baseArgs(dir), "--no-start")
		args = append(args, override...)
		ins, _, errOut := testInstaller("", secrets, nil)
		if code := ins.install(args); code != 2 {
			t.Errorf("%s: exit %d (%s)", name, code, errOut)
		}
		if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
			t.Errorf("%s: files written despite invalid input", name)
		}
	}
	// Plain http is fine for a panel on this host or a private network, when declared.
	dir := t.TempDir()
	ins, _, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start", "--xui-url", "http://172.17.0.1:2053/p", "--xui-allow-private")); code != 0 {
		t.Errorf("http panel with --xui-allow-private: exit %d (%s)", code, errOut)
	}
	// Online, a plain-http panel name must resolve to private addresses only.
	ins, _, errOut = testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(t.TempDir()), "--no-start", "--xui-url", "http://public.example.com:2053", "--xui-allow-private")); code != 2 ||
		!strings.Contains(errOut.String(), "public address 93.184.216.34") {
		t.Errorf("public plain-http panel: exit %d (%s)", code, errOut)
	}
	ins, _, errOut = testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(t.TempDir()), "--no-start", "--xui-url", "http://panel.lan:2053", "--xui-allow-private")); code != 0 {
		t.Errorf("private plain-http panel: exit %d (%s)", code, errOut)
	}
	ins, _, errOut = testInstaller("", map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": "nope", "BOBRES_XUI_TOKEN": "t"}, nil)
	if code := ins.install(append(baseArgs(t.TempDir()), "--no-start")); code != 2 || !strings.Contains(errOut.String(), "BotFather") {
		t.Errorf("bad bot token: %d %s", code, errOut)
	}
	ins, _, errOut = testInstaller("", nil, nil)
	if code := ins.install([]string{"--dir", t.TempDir()}); code != 2 || !strings.Contains(errOut.String(), "required") {
		t.Errorf("missing input without a terminal: %d %s", code, errOut)
	}
}

func TestInstallStopsOnFailedChecksAndBadToken(t *testing.T) {
	ins, _, errOut := testInstaller("", secrets, nil)
	ins.preflight = func(string) []Result {
		return []Result{{"Docker", Fail, "daemon not reachable"}, {"DNS", Fail, "no record"}}
	}
	if code := ins.install(baseArgs(t.TempDir())); code != 1 || !strings.Contains(errOut.String(), "System checks failed") {
		t.Fatalf("failed checks: %d %s", code, errOut)
	}
	ins, _, _ = testInstaller("", secrets, nil)
	ins.preflight = func(string) []Result { return []Result{{"DNS", Fail, "no record"}} }
	if code := ins.install(append(baseArgs(t.TempDir()), "--no-start")); code != 0 {
		t.Fatalf("a DNS problem alone must not block the install: %d", code)
	}
	ins, _, errOut = testInstaller("", secrets, nil)
	ins.checkBot = func(context.Context, string) (string, error) { return "", errors.New("401 Unauthorized") }
	if code := ins.install(baseArgs(t.TempDir())); code != 1 || !strings.Contains(errOut.String(), "rejected the bot token") {
		t.Fatalf("bad token: %d %s", code, errOut)
	}
}

func TestUnhealthyStackReportsWhatIsWrong(t *testing.T) {
	docker := &fakeDocker{health: []string{`{"Service":"core","Health":"unhealthy"}` + "\n" + `{"Service":"bot","State":"restarting"}`}}
	ins, _, errOut := testInstaller("", secrets, docker)
	if code := ins.install(baseArgs(t.TempDir())); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "core=unhealthy") || !strings.Contains(errOut.String(), "bot=restarting") || !strings.Contains(errOut.String(), "bobres logs --dir") {
		t.Fatalf("message: %s", errOut)
	}
}

func TestImageTag(t *testing.T) {
	for in, want := range map[string]string{"v1.2.3": "1.2.3", "1.4.0": "1.4.0", "dev": "latest", "": "latest", "abc123-dirty": "latest"} {
		if got := imageTag(in); got != want {
			t.Errorf("imageTag(%q) = %q, want %q", in, got, want)
		}
	}
}

// The first 3x-ui panel is optional: servers are added in the dashboard. A
// fresh install asks for it once (Enter skips), a re-run never does, and a
// URL and a token only go together.
func TestInstallWithoutPanel(t *testing.T) {
	noPanel := []string{"--domain", "panel.example.com", "--admin-id", "4242", "--no-start"}
	bot := map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken}

	// Without a terminal, nothing to ask: the stack starts with no server.
	dir := t.TempDir()
	ins, out, errOut := testInstaller("", bot, nil)
	if code := ins.install(append([]string{"--dir", dir}, noPanel...)); code != 0 {
		t.Fatalf("no panel: %d %s", code, errOut)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	if env["BOBRES_XUI_URL"] != "" || env["BOBRES_XUI_TOKEN"] != "" || !strings.Contains(out.String(), "Enter to add servers later") {
		t.Fatalf("panel: %q %q\n%s", env["BOBRES_XUI_URL"], env["BOBRES_XUI_TOKEN"], out)
	}
	if len(env["BOBRES_PAYMENTS_MASTER_KEY"]) != 64 || env["BOBRES_PAYMENTS_MASTER_KEY"] == env["BOBRES_CORE_MASTER_KEY"] {
		t.Fatalf("payments master key: %q", env["BOBRES_PAYMENTS_MASTER_KEY"])
	}

	// A re-run does not ask again, and keeps the payments key.
	ins, out, errOut = testInstaller("https://xui.example.com\n", nil, nil)
	if code := ins.install([]string{"--dir", dir, "--no-start"}); code != 0 {
		t.Fatalf("re-run: %d %s", code, errOut)
	}
	again, _, _ := readEnv(filepath.Join(dir, ".env"))
	if strings.Contains(out.String(), "3x-ui panel URL") || again["BOBRES_XUI_URL"] != "" ||
		again["BOBRES_PAYMENTS_MASTER_KEY"] != env["BOBRES_PAYMENTS_MASTER_KEY"] {
		t.Fatalf("re-run asked for a panel or lost the key:\n%s", out)
	}

	// Interactively, Enter skips the panel and its token.
	ins, out, errOut = testInstaller("panel.example.com\n4242\n"+goodBotToken+"\n\n", nil, nil)
	if code := ins.install([]string{"--dir", t.TempDir(), "--no-start"}); code != 0 || strings.Contains(out.String(), "API token") {
		t.Fatalf("interactive skip: %d %s\n%s", code, errOut, out)
	}

	// A URL needs its token, and a token its URL.
	ins, _, errOut = testInstaller("", bot, nil)
	if code := ins.install(append([]string{"--dir", t.TempDir(), "--xui-url", "https://xui.example.com"}, noPanel...)); code != 2 ||
		!strings.Contains(errOut.String(), "BOBRES_XUI_TOKEN is required") {
		t.Fatalf("URL without token: %d %s", code, errOut)
	}
	ins, _, errOut = testInstaller("", map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken, "BOBRES_XUI_TOKEN": "t"}, nil)
	if code := ins.install(append([]string{"--dir", t.TempDir()}, noPanel...)); code != 2 || !strings.Contains(errOut.String(), "needs the panel URL") {
		t.Fatalf("token without URL: %d %s", code, errOut)
	}
}

// An install kept on a version from before the backup service runs without
// it; moving to this CLI's version turns it on.
func TestBackupsFollowTheImageVersion(t *testing.T) {
	old := version.Version
	version.Version = "v1.4.0"
	defer func() { version.Version = old }()

	dir := t.TempDir()
	docker := &fakeDocker{health: []string{`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},` +
		`{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"}]`}}
	ins, out, errOut := testInstaller("", secrets, docker)
	if code := ins.install(append(baseArgs(dir), "--version", "1.2.0")); code != 0 {
		t.Fatalf("pinned: %d %s", code, errOut)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	if backupEnabled(env) || env["BOBRES_BACKUP_URL"] != "" || !strings.Contains(out.String(), "Backups are off") ||
		!strings.Contains(out.String(), "--version 1.4.0") {
		t.Fatalf("pinned old version: profiles %q\n%s", env["COMPOSE_PROFILES"], out)
	}
	for _, c := range docker.calls {
		if contains(c, "exec") {
			t.Fatalf("backup role created without the backup service: %v", c)
		}
	}

	docker = &fakeDocker{health: []string{`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},` +
		`{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`}}
	ins, out, errOut = testInstaller("", nil, docker)
	if code := ins.install([]string{"--dir", dir, "--version", "1.4.0"}); code != 0 {
		t.Fatalf("upgrade: %d %s", code, errOut)
	}
	env, _, _ = readEnv(filepath.Join(dir, ".env"))
	// The key was shown by the run that created it (the first), not again.
	if !backupEnabled(env) || strings.Contains(out.String(), env["BOBRES_BACKUP_KEY"]) {
		t.Fatalf("upgrade: profiles %q\n%s", env["COMPOSE_PROFILES"], out)
	}
	if !backupImage("latest") || backupImage("1.3.9") || !backupImage("1.4.0") {
		t.Fatal("backupImage")
	}
}

func TestEnableBackupsKeepsOtherProfiles(t *testing.T) {
	env := map[string]string{"COMPOSE_PROFILES": "debug"}
	enableBackups(env)
	enableBackups(env)
	if env["COMPOSE_PROFILES"] != "debug,backup" || !backupEnabled(env) || backupEnabled(map[string]string{"COMPOSE_PROFILES": "backups"}) {
		t.Fatalf("profiles %q", env["COMPOSE_PROFILES"])
	}
}

// A failed role creation is a warning that never shows the password.
func TestBackupRoleFailureIsAWarning(t *testing.T) {
	ins, _, errOut := testInstaller("", nil, nil)
	ins.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("ERROR: something about pw-secret-123"), errors.New("exit status 3")
	}
	ins.ensureBackupRole(context.Background(), t.TempDir(), "pw-secret-123")
	if !strings.Contains(errOut.String(), "warning: the backup database role was not created") || strings.Contains(errOut.String(), "pw-secret-123") {
		t.Fatalf("warning: %s", errOut)
	}
}

// The started stack's summary points to the dashboard when no panel is set.
func TestSummaryWithoutPanel(t *testing.T) {
	ins, out, _ := testInstaller("", nil, nil)
	ins.summary(map[string]string{"BOBRES_DOMAIN": "panel.example.com"}, "/opt/bobres")
	if !strings.Contains(out.String(), "add your servers in the dashboard") {
		t.Fatalf("summary: %s", out)
	}
	ins, out, _ = testInstaller("", nil, nil)
	ins.summary(map[string]string{"BOBRES_DOMAIN": "panel.example.com", "BOBRES_XUI_URL": "https://xui.example.com"}, "/opt/bobres")
	if strings.Contains(out.String(), "No 3x-ui panel") {
		t.Fatalf("summary with a panel: %s", out)
	}
}
