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
		`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"}]`,
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
	for _, k := range envOrder {
		if k != "BOBRES_XUI_SUB_URL" && env[k] == "" {
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

func TestInstallRejectsBadInput(t *testing.T) {
	cases := map[string][]string{
		"bad domain":   {"--domain", "not a domain"},
		"bad admin id": {"--admin-id", "12ab"},
		"http panel":   {"--xui-url", "http://xui.example.com"},
		"loopback":     {"--xui-url", "http://127.0.0.1:2053", "--xui-allow-private"},
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
	if !strings.Contains(errOut.String(), "core=unhealthy") || !strings.Contains(errOut.String(), "bot=restarting") || !strings.Contains(errOut.String(), "docker compose logs") {
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
