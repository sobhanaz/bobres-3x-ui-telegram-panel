package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

const goodBotToken = "123456789:AAH-abcdefghijklmnopqrstuvwxyz012345" //nolint:gosec // test fixture, gitleaks:allow

type fakeDocker struct {
	calls  [][]string
	health []string // successive `ps` outputs
	upErr  error
	// caddy are the successive answers to `ps ... caddy` (the wait for the
	// gateway), the last one repeating; running when empty.
	caddy []string
	// killErr is the error of `kill` (the SIGUSR1 to Caddy).
	killErr error
	// adminPW is what `exec core /app admin-password` prints (a sample
	// login when empty); adminPWErr makes it fail.
	adminPW    string
	adminPWErr error
}

func (f *fakeDocker) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch {
	case contains(args, "admin-password"):
		if f.adminPWErr != nil {
			return []byte("Error: core is not running"), f.adminPWErr
		}
		if f.adminPW != "" {
			return []byte(f.adminPW), nil
		}
		return []byte(sampleLogin), nil
	case contains(args, "up"):
		return []byte("started"), f.upErr
	case contains(args, "kill"):
		return []byte("killed"), f.killErr
	case contains(args, "ps") && contains(args, "caddy"):
		if len(f.caddy) == 0 {
			return []byte(`[{"Service":"caddy","State":"running"}]`), nil
		}
		out := f.caddy[0]
		if len(f.caddy) > 1 {
			f.caddy = f.caddy[1:]
		}
		return []byte(out), nil
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

// sampleLogin is the output of core's admin-password.
const sampleLogin = "Username: owner\nPassword: Kq7mWz3NpR9xTv2H\nShown once, never stored in the clear: at the first login you must choose a new password and set up two-factor authentication.\n"

// composeVerbs are the compose subcommands of the calls (the word after
// --env-file .env).
func composeVerbs(calls [][]string) []string {
	var out []string
	for _, c := range calls {
		i := slices.Index(c, "--env-file")
		if i < 0 || i+2 >= len(c) {
			out = append(out, c[0])
			continue
		}
		out = append(out, c[i+2])
	}
	return out
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
		checkBot: func(context.Context, string, string) (string, error) { return "test_bot", nil },
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
		`[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`,
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
		"BOBRES_ZARINPAL_PROXY": true, "BOBRES_ZARINPAL_PUBLIC_URL": true, "BOBRES_ZARINPAL_HOST": true,
		"BOBRES_BACKUP_NETWORK": true} // the S3 upload is optional too
	for _, k := range config.BackupS3Keys {
		optional[k] = true
	}
	for _, svc := range imageServices { // a development build pins no images
		optional[digestKey(svc)] = true
	}
	for _, k := range envOrder {
		if !optional[k] && env[k] == "" {
			t.Errorf("%s missing", k)
		}
		if strings.Contains(env[k], "CHANGE_ME") {
			t.Errorf("%s still a placeholder", k)
		}
	}
	tokens := map[string]bool{}
	for _, k := range serviceTokens {
		if len(env[k]) != 64 || tokens[env[k]] {
			t.Errorf("service token %s: %q (64 hex characters, each different)", k, env[k])
		}
		tokens[env[k]] = true
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
	// After `up`: Caddy is told to read its Caddyfile again, its state is
	// looked at twice, and only then does the backup role get made.
	if got := composeVerbs(docker.calls[:5]); strings.Join(got, " ") != "up kill ps ps exec" {
		t.Fatalf("docker calls: %v", docker.calls)
	}
	if kill := strings.Join(docker.calls[1], " "); !strings.HasSuffix(kill, " kill -s SIGUSR1 caddy") {
		t.Errorf("the signal to Caddy: %s", kill)
	}
	assertHTTPSLayout(t, dir, "panel.example.com")

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
	role := docker.calls[4]
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
		// Core presents a token of its own to each service it calls, which
		// that service accepts and no other container holds: none of them
		// can act as core toward another (the backup container holds the
		// master keys, the bot parses all Telegram input).
		var project struct {
			Services map[string]struct {
				Environment map[string]string `json:"environment"`
			} `json:"services"`
		}
		if err := json.Unmarshal(got, &project); err != nil {
			t.Fatalf("compose config: %v", err)
		}
		coreEnv := project.Services["core"].Environment
		// The tls-agent gets the install's two names from .env, as core does: it refuses a request for
		// any other, and answers every one invalid without the domain.
		agentEnv := project.Services["tls-agent"].Environment
		for _, k := range []string{"BOBRES_DOMAIN", "BOBRES_ZARINPAL_HOST"} {
			if agentEnv[k] != env[k] || agentEnv[k] != coreEnv[k] {
				t.Errorf("tls-agent %s = %q, .env %q, core %q", k, agentEnv[k], env[k], coreEnv[k])
			}
		}
		if agentEnv["BOBRES_DOMAIN"] == "" {
			t.Error("the installed tls-agent has no BOBRES_DOMAIN")
		}
		for peer, tok := range map[string]struct{ core, dotenv string }{
			"payments":    {"BOBRES_SERVICE_TOKEN", "BOBRES_TOKEN_CORE"},
			"provisioner": {"BOBRES_PROVISIONER_TOKEN", "BOBRES_TOKEN_CORE_PROVISIONER"},
			"bot":         {"BOBRES_BOT_INTERNAL_TOKEN", "BOBRES_TOKEN_CORE_BOT"},
			"backup":      {"BOBRES_BACKUP_TOKEN", "BOBRES_TOKEN_CORE_BACKUP"},
		} {
			v := env[tok.dotenv]
			if coreEnv[tok.core] != v || project.Services[peer].Environment["BOBRES_PEER_CORE_TOKEN"] != v {
				t.Errorf("core to %s: %s must accept %s, and core send it as %s", peer, peer, tok.dotenv, tok.core)
			}
			for svc, s := range project.Services {
				for k, val := range s.Environment {
					if svc != "core" && svc != peer && val == v {
						t.Errorf("%s holds %s (what %s accepts as core) as %s", svc, tok.dotenv, peer, k)
					}
				}
			}
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

// The panel values only seed the first server, so a re-run that changes
// them says where a panel is changed instead.
func TestReinstallPanelChangeNote(t *testing.T) {
	dir := t.TempDir()
	ins, _, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start")); code != 0 {
		t.Fatalf("first: %d %s", code, errOut)
	}
	const note = "edit it in the dashboard (Servers)"
	ins2, out, errOut := testInstaller("", nil, nil)
	if code := ins2.install([]string{"--dir", dir, "--no-start", "--domain", "new.example.com"}); code != 0 {
		t.Fatalf("rerun: %d %s", code, errOut)
	}
	if strings.Contains(out.String(), note) {
		t.Errorf("note without a panel change: %s", out)
	}
	ins3, out, errOut := testInstaller("", nil, nil)
	if code := ins3.install([]string{"--dir", dir, "--no-start", "--xui-sub-url", "https://xui.example.com:2096/x9k2/"}); code != 0 {
		t.Fatalf("rerun with a new prefix: %d %s", code, errOut)
	}
	if !strings.Contains(out.String(), note) {
		t.Errorf("no note after a panel change: %s", out)
	}
}

// An install from before core had a token for each peer gets the missing
// ones on its next install run (bobres update runs one), keeping the rest.
func TestReinstallAddsTheMissingServiceTokens(t *testing.T) {
	for _, k := range serviceTokens {
		if n, ok := generated[k]; !ok || n < 32 || !slices.Contains(envOrder, k) {
			t.Errorf("%s is not a generated .env key of 32+ random bytes", k)
		}
	}
	dir := t.TempDir()
	ins, _, errOut := testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start")); code != 0 {
		t.Fatalf("first: %d %s", code, errOut)
	}
	first, _, _ := readEnv(filepath.Join(dir, ".env"))
	old := map[string]string{}
	for k, v := range first {
		if k != "BOBRES_TOKEN_CORE_PROVISIONER" && k != "BOBRES_TOKEN_CORE_BOT" {
			old[k] = v
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(old), 0o600); err != nil {
		t.Fatal(err)
	}
	ins, _, errOut = testInstaller("", nil, nil)
	if code := ins.install([]string{"--dir", dir, "--no-start"}); code != 0 {
		t.Fatalf("rerun: %d %s", code, errOut)
	}
	second, _, _ := readEnv(filepath.Join(dir, ".env"))
	seen := map[string]bool{}
	for _, k := range serviceTokens {
		if v := second[k]; len(v) != 64 || seen[v] || (old[k] != "" && v != old[k]) {
			t.Errorf("%s after the re-run: %q (kept when set, else new; all different)", k, v)
		}
		seen[second[k]] = true
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
	ins.checkBot = func(context.Context, string, string) (string, error) { return "", errors.New("401 Unauthorized") }
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
// it; moving to a version that has it turns it on, and rolling back to one
// without it turns it off again (its image cannot be pulled).
func TestBackupsFollowTheImageVersion(t *testing.T) {
	old := version.Version
	version.Version = "v1.4.0"
	defer func() { version.Version = old }()
	const older = "0.0.9" // before firstBackupRelease
	withoutBackup := `[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},` +
		`{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"}]`
	withBackup := strings.TrimSuffix(withoutBackup, "]") + `,{"Service":"backup","Health":"healthy"}]`

	dir := t.TempDir()
	docker := &fakeDocker{health: []string{withoutBackup}}
	ins, out, errOut := testInstaller("", secrets, docker)
	if code := ins.install(append(baseArgs(dir), "--version", older)); code != 0 {
		t.Fatalf("pinned: %d %s", code, errOut)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	if backupEnabled(env) || env["BOBRES_BACKUP_URL"] != "" || !strings.Contains(out.String(), "Backups are off") ||
		!strings.Contains(out.String(), "--version 1.4.0") {
		t.Fatalf("pinned old version: profiles %q\n%s", env["COMPOSE_PROFILES"], out)
	}
	for _, c := range docker.calls {
		if contains(c, "exec") && !contains(c, "admin-password") {
			t.Fatalf("backup role created without the backup service: %v", c)
		}
	}

	docker = &fakeDocker{health: []string{withBackup}}
	ins, out, errOut = testInstaller("", nil, docker)
	if code := ins.install([]string{"--dir", dir, "--version", "1.4.0"}); code != 0 {
		t.Fatalf("upgrade: %d %s", code, errOut)
	}
	env, _, _ = readEnv(filepath.Join(dir, ".env"))
	// The key was shown by the run that created it (the first), not again.
	if !backupEnabled(env) || env["BOBRES_BACKUP_URL"] != "http://backup:8080" || strings.Contains(out.String(), env["BOBRES_BACKUP_KEY"]) {
		t.Fatalf("upgrade: profiles %q\n%s", env["COMPOSE_PROFILES"], out)
	}

	// The rollback the Updates tab's command allows: the profile goes off
	// (another one stays), so compose up does not pull a missing image, and
	// the stack is waited for without the backup service.
	env["COMPOSE_PROFILES"] = "debug,backup"
	if err := writeFiles(dir, env, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	docker = &fakeDocker{health: []string{withoutBackup}}
	ins, out, errOut = testInstaller("", nil, docker)
	if code := ins.install([]string{"--dir", dir, "--version", older}); code != 0 {
		t.Fatalf("rollback: %d %s", code, errOut)
	}
	env, _, _ = readEnv(filepath.Join(dir, ".env"))
	if backupEnabled(env) || env["COMPOSE_PROFILES"] != "debug" || env["BOBRES_BACKUP_URL"] != "" || env["BOBRES_VERSION"] != older ||
		!strings.Contains(out.String(), "Backups are off") {
		t.Fatalf("rollback: profiles %q url %q\n%s", env["COMPOSE_PROFILES"], env["BOBRES_BACKUP_URL"], out)
	}

	for v, want := range map[string]bool{
		"latest": true, "1.4.0": true, firstBackupRelease: true, "0.1.0-rc.1": true, "v1.3.9": true,
		"1.5.0": true, older: false, "0.0.9-rc.1": false, "sha-1a2b3c": false, "": false, // 1.5.0: newer than this CLI
	} {
		if got := backupImage(v); got != want {
			t.Errorf("backupImage(%q) = %v, want %v", v, got, want)
		}
	}
}

// The backup key is shown by the run that saves it, whatever happens next:
// no later run shows it, and without it the backups cannot be read.
func TestBackupKeyShownByTheRunThatSavesIt(t *testing.T) {
	healthy := `[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},` +
		`{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`

	// The start fails (compose up): the key was saved, so it is shown now.
	dir := t.TempDir()
	ins, out, errOut := testInstaller("", secrets, &fakeDocker{upErr: errors.New("pull failed")})
	if code := ins.install(baseArgs(dir)); code != 1 || !strings.Contains(errOut.String(), "docker compose up failed") {
		t.Fatalf("failed start: %d %s", code, errOut)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	key := env["BOBRES_BACKUP_KEY"]
	if len(key) != 64 || strings.Count(out.String(), key) != 1 || !strings.Contains(out.String(), "without it the backups cannot be read") {
		t.Fatalf("key not shown after a failed start:\n%s", out)
	}
	// The repair run that works keeps the key and does not show it again,
	// but its summary says where it is.
	ins, out, errOut = testInstaller("", nil, &fakeDocker{health: []string{healthy}})
	if code := ins.install([]string{"--dir", dir}); code != 0 {
		t.Fatalf("repair: %d %s", code, errOut)
	}
	if strings.Contains(out.String(), key) || !strings.Contains(out.String(), `bobres menu > "Backup key"`) {
		t.Fatalf("repair run:\n%s", out)
	}

	// A quiet run from the menu (an older install's first run of this CLI)
	// creates the key: shown too.
	dir = t.TempDir()
	ins, _, errOut = testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(dir), "--no-start")); code != 0 {
		t.Fatalf("first: %d %s", code, errOut)
	}
	env, _, _ = readEnv(filepath.Join(dir, ".env"))
	delete(env, "BOBRES_BACKUP_KEY") // as an install from before backups has it
	if err := writeFiles(dir, env, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	ins, out, errOut = testInstaller("", nil, &fakeDocker{health: []string{healthy}})
	ins.quiet = true
	if code := ins.install([]string{"--dir", dir, "--skip-checks", "--admin-id", "5151"}); code != 0 {
		t.Fatalf("menu change: %d %s", code, errOut)
	}
	env, _, _ = readEnv(filepath.Join(dir, ".env"))
	if len(env["BOBRES_BACKUP_KEY"]) != 64 || strings.Count(out.String(), env["BOBRES_BACKUP_KEY"]) != 1 || strings.Contains(out.String(), "/plan_add") {
		t.Fatalf("quiet run that created the key:\n%s", out)
	}

	// A run refused before writing anything shows no key: none was saved.
	ins, out, _ = testInstaller("", secrets, nil)
	if code := ins.install(append(baseArgs(t.TempDir()), "--domain", "not a domain")); code != 2 || strings.Contains(out.String(), "Backup key") {
		t.Fatalf("refused run: %d\n%s", code, out)
	}
}

func TestEnableBackupsKeepsOtherProfiles(t *testing.T) {
	env := map[string]string{"COMPOSE_PROFILES": "debug"}
	enableBackups(env)
	enableBackups(env)
	if env["COMPOSE_PROFILES"] != "debug,backup" || !backupEnabled(env) || backupEnabled(map[string]string{"COMPOSE_PROFILES": "backups"}) {
		t.Fatalf("profiles %q", env["COMPOSE_PROFILES"])
	}
	env["COMPOSE_PROFILES"] = " backup , debug,backups"
	disableBackups(env)
	disableBackups(env)
	if env["COMPOSE_PROFILES"] != "debug,backups" || backupEnabled(env) || env["BOBRES_BACKUP_URL"] != "" {
		t.Fatalf("off: profiles %q url %q", env["COMPOSE_PROFILES"], env["BOBRES_BACKUP_URL"])
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
	// the bot sets its own photo from the dashboard: the summary says where to upload it
	if !strings.Contains(out.String(), "Bot photo:          the BOBRES logo is set; to use your own, upload it in the dashboard: Branding & texts > Store > Telegram bot") ||
		strings.Contains(out.String(), "/setuserpic") {
		t.Fatalf("summary without the bot photo hint: %s", out)
	}
	ins, out, _ = testInstaller("", nil, nil)
	ins.summary(map[string]string{"BOBRES_DOMAIN": "panel.example.com", "BOBRES_XUI_URL": "https://xui.example.com"}, "/opt/bobres")
	if strings.Contains(out.String(), "No 3x-ui panel") {
		t.Fatalf("summary with a panel: %s", out)
	}
}

// bobres update runs the new CLI's install with --quiet --yes: no summary,
// and never a prompt (a value still missing is an error, not a question).
func TestInstallQuietYes(t *testing.T) {
	dir := t.TempDir()
	answers := "panel.example.com\n4242\n" + goodBotToken + "\n\n"
	ins, _, errOut := testInstaller(answers, nil, nil)
	if code := ins.install([]string{"--dir", dir, "--no-start", "--yes"}); code != 2 || !strings.Contains(errOut.String(), "BOBRES_DOMAIN is required") {
		t.Fatalf("--yes asked instead of failing: %d %s", code, errOut)
	}

	healthy := `[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`
	ins, out, errOut := testInstaller("", secrets, &fakeDocker{health: []string{healthy}})
	if code := ins.install(append(baseArgs(dir), "--quiet", "--yes", "--version", "1.2.3")); code != 0 {
		t.Fatalf("quiet install: %d %s", code, errOut)
	}
	if strings.Contains(out.String(), "/plan_add") || !strings.Contains(out.String(), "All services are healthy") {
		t.Fatalf("--quiet still printed the first steps:\n%s", out)
	}
	if env, _, _ := readEnv(filepath.Join(dir, ".env")); env["BOBRES_VERSION"] != "1.2.3" {
		t.Fatalf("version: %q", env["BOBRES_VERSION"])
	}
}

// The installer's bot token check, which the menu's settings changes run
// too, goes where the bot goes: the Bot API server in
// BOBRES_TELEGRAM_API_URL (a server that reaches Telegram only through it),
// else api.telegram.org.
func TestInstallChecksTheTokenThroughTheBotAPIServer(t *testing.T) {
	token := "123456789:" + strings.Repeat("a", 35)
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/bot"+token+"/getMe" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":123456789,"is_bot":true,"first_name":"Relay","username":"relay_bot"}}`))
	}))
	defer srv.Close()
	requests := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(asked)
	}

	dir := fullInstall(t, false)
	env, _ := readEnvFile(t, dir)
	env["BOBRES_TELEGRAM_API_URL"] = srv.URL
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}

	// The menu's "Change the bot token": the installer with the new token.
	ins, out, errOut := testInstaller("", map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": token}, nil)
	ins.checkBot = botUsername
	if code := ins.install([]string{"--dir", dir, "--skip-checks", "--no-start"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := requests(); !strings.Contains(out.String(), "Bot token OK: @relay_bot") || len(got) != 1 || got[0] != "/bot"+token+"/getMe" {
		t.Fatalf("getMe through the server: %v\n%s", got, out)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_TELEGRAM_BOT_TOKEN"] != token || env["BOBRES_TELEGRAM_API_URL"] != srv.URL {
		t.Fatalf("saved: %q %q", env["BOBRES_TELEGRAM_BOT_TOKEN"], env["BOBRES_TELEGRAM_API_URL"])
	}

	// A token the server refuses changes nothing, and the error names it.
	_, before := readEnvFile(t, dir)
	ins, _, errOut = testInstaller("", map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": "123456789:" + strings.Repeat("b", 35)}, nil)
	ins.checkBot = botUsername
	if code := ins.install([]string{"--dir", dir, "--skip-checks", "--no-start"}); code != 1 ||
		!strings.Contains(errOut.String(), "through the Bot API server "+srv.URL) || !strings.Contains(errOut.String(), "401 Unauthorized") {
		t.Fatalf("refused token: %d %s", code, errOut)
	}
	if _, after := readEnvFile(t, dir); after != before || len(requests()) != 2 {
		t.Fatalf(".env changed or the server was not asked: %v", requests())
	}
}

func TestTelegramAPIRoot(t *testing.T) {
	for v, want := range map[string]string{
		"":                                 "", // api.telegram.org
		"http://host.docker.internal:8081": "http://127.0.0.1:8081",
		"http://HOST.docker.internal":      "http://127.0.0.1",
		"https://tg.example.com:8443/":     "https://tg.example.com:8443/",
		" http://10.0.0.5:8081 ":           "http://10.0.0.5:8081",
		// Not a URL the bot could use: the check asks api.telegram.org.
		"ftp://tg.example.com": "",
		"https://user:pw@x":    "",
		"tg.example.com:8081":  "",
		"https://":             "",
	} {
		if got := telegramAPIRoot(map[string]string{"BOBRES_TELEGRAM_API_URL": v}); got != want {
			t.Errorf("%q: %q, want %q", v, got, want)
		}
	}
}

// .env lines with a comment are read as Compose reads them: an unquoted
// value ends before " #", and a quoted one keeps its # and ends at its
// closing quote.
func TestParseEnvComments(t *testing.T) {
	pw := strings.Repeat("ab", 8) + "#" + strings.Repeat("cd", 8)
	src := "# a comment line\n" +
		"  # an indented one\n" +
		"DOMAIN=panel.example.com # the store\n" +
		"HASH=ab#cd\n" +
		"SINGLE='" + pw + "' # quoted: the # is the password's\n" +
		"DOUBLE=\"" + pw + "\"#right after the quote\n" +
		"INSIDE='" + pw + " # still the value'\n" +
		"ESCAPED=\"a\\\"b\" # c\n" +
		"SPACES=   info   \n" +
		"EMPTY='' # none yet\n" +
		"BARE=\n" +
		"HASHONLY= # Compose reads this as the value\n" +
		"NOTE=keep me\r\n" +
		"UNCLOSED='abc\n" +
		"AFTER='abc' def\n"
	want := map[string][2]string{
		"DOMAIN":   {"panel.example.com", "# the store"},
		"HASH":     {"ab#cd", ""},
		"SINGLE":   {pw, "# quoted: the # is the password's"},
		"DOUBLE":   {pw, "#right after the quote"},
		"INSIDE":   {pw + " # still the value", ""},
		"ESCAPED":  {`a\"b`, "# c"},
		"SPACES":   {"info", ""},
		"EMPTY":    {"", "# none yet"},
		"BARE":     {"", ""},
		"HASHONLY": {"# Compose reads this as the value", ""},
		"NOTE":     {"keep me", ""},
		"UNCLOSED": {"'abc", ""},
		"AFTER":    {"'abc' def", ""},
	}
	env, comments := parseEnv([]byte(src)), envComments([]byte(src))
	if len(env) != len(want) {
		t.Errorf("keys: %v", env)
	}
	for k, w := range want {
		if env[k] != w[0] || comments[k] != w[1] {
			t.Errorf("%s: value %q comment %q, want %q %q", k, env[k], comments[k], w[0], w[1])
		}
	}
}

// Rewriting .env (bobres config set, the installer's re-run) keeps each value
// without its comment, and the comment at the end of its line.
func TestEnvRewriteKeepsComments(t *testing.T) {
	dir := fullInstall(t, false)
	path := filepath.Join(dir, ".env")
	b, err := os.ReadFile(path) //nolint:gosec // the test's own folder
	if err != nil {
		t.Fatal(err)
	}
	pw := strings.Repeat("ab", 8) + "#" + strings.Repeat("cd", 8)
	s := regexp.MustCompile(`(?m)^BOBRES_TIMEZONE=.*$`).ReplaceAllString(string(b), "BOBRES_TIMEZONE=Asia/Tehran   # the owner's time zone")
	s = regexp.MustCompile(`(?m)^DB_CORE_PASSWORD=.*$`).ReplaceAllString(s, "DB_CORE_PASSWORD='"+pw+"' # set by hand")
	s += "OPERATOR_PROXY=http://10.0.0.1:3128 # the office proxy\n"
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil { //nolint:gosec // the test's own folder
		t.Fatal(err)
	}
	check := func(what string) {
		t.Helper()
		env, raw := readEnvFile(t, dir)
		if env["BOBRES_TIMEZONE"] != "Asia/Tehran" || env["DB_CORE_PASSWORD"] != pw || env["OPERATOR_PROXY"] != "http://10.0.0.1:3128" {
			t.Fatalf("%s: values %q %q %q", what, env["BOBRES_TIMEZONE"], env["DB_CORE_PASSWORD"], env["OPERATOR_PROXY"])
		}
		for _, line := range []string{
			"\nBOBRES_TIMEZONE=Asia/Tehran # the owner's time zone\n",
			"\nDB_CORE_PASSWORD='" + pw + "' # set by hand\n",
			"\nOPERATOR_PROXY=http://10.0.0.1:3128 # the office proxy\n",
		} {
			if !strings.Contains(raw, line) {
				t.Fatalf("%s: no line %q in\n%s", what, line, raw)
			}
		}
	}

	o, _, errOut := dbOps("", &fakeDB{})
	if code := o.configSet(dir, "BOBRES_LOG_LEVEL", "debug", true); code != 0 {
		t.Fatalf("config set: %d %s", code, errOut)
	}
	check("config set")
	if env, _ := readEnvFile(t, dir); env["BOBRES_LOG_LEVEL"] != "debug" {
		t.Fatalf("log level: %q", env["BOBRES_LOG_LEVEL"])
	}

	ins, _, errOut := testInstaller("", nil, nil)
	if code := ins.install([]string{"--dir", dir, "--skip-checks", "--no-start", "--offline"}); code != 0 {
		t.Fatalf("re-run: %d %s", code, errOut)
	}
	check("installer re-run")

	// An empty value keeps its comment quoted, or Compose would read the
	// comment as the value.
	env := map[string]string{"BOBRES_XUI_SUB_URL": ""}
	out := string(renderEnvComments(env, map[string]string{"BOBRES_XUI_SUB_URL": "# later"}))
	if !strings.Contains(out, "\nBOBRES_XUI_SUB_URL='' # later\n") || parseEnv([]byte(out))["BOBRES_XUI_SUB_URL"] != "" {
		t.Fatalf("empty value with a comment:\n%s", out)
	}
}

// A fresh install issues the owner's dashboard login once the stack is up and
// prints it in the summary, so no Telegram step is needed; the password goes
// to the terminal only.
func TestFreshInstallIssuesTheOwnerLogin(t *testing.T) {
	dir := t.TempDir()
	healthy := `[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`
	docker := &fakeDocker{health: []string{healthy}}
	ins, out, errOut := testInstaller("", secrets, docker)
	if code := ins.install(baseArgs(dir)); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"https://panel.example.com/admin", "Username:  owner", "Password:  Kq7mWz3NpR9xTv2H",
		"you must change it", "two-factor", "/start", "bobres admin password"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary misses %q:\n%s", want, out)
		}
	}
	if strings.Count(out.String(), "Kq7mWz3NpR9xTv2H") != 1 || strings.Contains(errOut.String(), "Kq7mWz3NpR9xTv2H") {
		t.Errorf("the password must appear once, on stdout only:\n%s\n%s", out, errOut)
	}
	last := docker.calls[len(docker.calls)-1]
	if !strings.HasSuffix(strings.Join(last, " "), "exec -T core /app admin-password") {
		t.Errorf("last call: %v", last)
	}
	// Not in any file the install wrote.
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error { //nolint:errcheck // a test scan
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(path); strings.Contains(string(b), "Kq7mWz3NpR9xTv2H") {
				t.Errorf("the password is in %s", path)
			}
		}
		return nil
	})

	// core unreachable: the install still succeeds and points to the command.
	ins, out, errOut = testInstaller("", secrets, &fakeDocker{health: []string{healthy}, adminPWErr: errors.New("exit status 1")})
	if code := ins.install(baseArgs(t.TempDir())); code != 0 {
		t.Fatalf("with core unreachable: %d %s", code, errOut)
	}
	if strings.Contains(out.String(), "Password:  ") || !strings.Contains(out.String(), "bobres admin password") ||
		!strings.Contains(errOut.String(), "was not created") {
		t.Fatalf("degraded summary:\n%s\n%s", out, errOut)
	}

	// A re-run of an existing install never resets the owner's login.
	docker = &fakeDocker{health: []string{healthy}}
	ins, out, errOut = testInstaller("", nil, docker)
	if code := ins.install([]string{"--dir", dir}); code != 0 {
		t.Fatalf("re-run: %d %s", code, errOut)
	}
	for _, c := range docker.calls {
		if contains(c, "admin-password") {
			t.Fatalf("a re-run issued a password: %v", c)
		}
	}
	if strings.Contains(out.String(), "Password:  ") {
		t.Fatalf("a re-run showed a password:\n%s", out)
	}

	// A quiet run (a settings change from the menu) shows no summary and issues nothing.
	docker = &fakeDocker{health: []string{healthy}}
	ins, out, _ = testInstaller("", secrets, docker)
	ins.quiet = true
	if code := ins.install(baseArgs(t.TempDir())); code != 0 || contains(docker.calls[len(docker.calls)-1], "admin-password") ||
		strings.Contains(out.String(), "Password:") {
		t.Fatalf("quiet run: %d\n%s", code, out)
	}
}
