package deploy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

// Test values for the secrets the bot checks (never real ones).
const (
	testCoreTok   = "core-token-0123456789abcdef0123456789"  //gitleaks:allow test fixture
	testBotTok    = "bot-token-0123456789abcdef01234567890"  //gitleaks:allow test fixture
	testBackupTok = "backup-token-0123456789abcdef012345678" //gitleaks:allow test fixture
)

// Each setting the bot reads from .env reaches it through the compose file
// (it has no env_file): config.LoadBot, given the environment compose builds
// for the bot, sees BOBRES_LICENSE_SERVER (without it the dashboard's "Activate
// with a license key" and core's daily license refresh answer "not set up",
// and an activated license runs into grace and expiry) and the other optional
// settings. An unset line stays optional: compose does not refuse it.
func TestComposeGivesTheBotItsSettings(t *testing.T) {
	set := installEnv(map[string]string{
		"BOBRES_LICENSE_SERVER":   "https://license.example.com/",
		"BOBRES_UPDATE_CHANNEL":   "beta",
		"BOBRES_TELEGRAM_API_URL": "http://host.docker.internal:8081",
	})
	c := loadBot(t, set)
	if c.LicenseServer != "https://license.example.com" {
		t.Errorf("the bot's license server is %q: BOBRES_LICENSE_SERVER from .env does not reach it", c.LicenseServer)
	}
	if c.UpdateChannel != "beta" || c.TelegramAPIURL != "http://host.docker.internal:8081" || c.AdminTelegramID != 4242 ||
		c.PeerCoreToken != testCoreTok || c.PeerBackupToken != testBackupTok {
		t.Errorf("the bot's settings from .env: channel %q, API %q, owner %d, peer tokens set %v %v", c.UpdateChannel,
			c.TelegramAPIURL, c.AdminTelegramID, c.PeerCoreToken == testCoreTok, c.PeerBackupToken == testBackupTok)
	}

	unset := installEnv(nil)
	if c := loadBot(t, unset); c.LicenseServer != "" || c.UpdateChannel != "stable" {
		t.Errorf("without the lines: license server %q, channel %q", c.LicenseServer, c.UpdateChannel)
	}

	// The template documents the line (commented: online activation is off
	// until the seller gives the address).
	if !regexp.MustCompile(`(?m)^#BOBRES_LICENSE_SERVER=https://`).Match(EnvExample) {
		t.Error(".env.example does not document BOBRES_LICENSE_SERVER")
	}

	// Docker's own reading of the file agrees with composeEnv's (where docker
	// is installed; no daemon is needed).
	if _, err := exec.LookPath("docker"); err == nil {
		for _, env := range []map[string]string{set, unset} {
			if got, want := dockerComposeEnv(t, "bot", env), composeEnv(t, "bot", env); !reflect.DeepEqual(got, want) {
				t.Errorf("docker compose config gives the bot\n%v\ncomposeEnv\n%v", got, want)
			}
		}
	}
}

// installEnv is an install's .env: the template's lines with test secrets in
// place of its CHANGE_ME values, then extra.
func installEnv(extra map[string]string) map[string]string {
	env := map[string]string{}
	for _, line := range strings.Split(string(EnvExample), "\n") {
		line = strings.TrimSpace(line)
		if k, v, ok := strings.Cut(line, "="); ok && line[0] != '#' {
			env[k] = v
		}
	}
	for k, v := range map[string]string{
		"BOBRES_TOKEN_CORE": testCoreTok, "BOBRES_TOKEN_BOT": testBotTok, "BOBRES_TOKEN_BACKUP": testBackupTok,
		"BOBRES_TELEGRAM_BOT_TOKEN": "123:abc", "BOBRES_ADMIN_TELEGRAM_ID": "4242", "BOBRES_REDIS_URL": "redis://redis:6379/0",
	} {
		env[k] = v
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// loadBot runs config.LoadBot with only the environment compose gives the
// bot for the .env values env.
func loadBot(t *testing.T, env map[string]string) config.Bot {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "BOBRES_") {
			t.Setenv(k, "")
		}
	}
	for k, v := range composeEnv(t, "bot", env) {
		t.Setenv(k, v)
	}
	c, err := config.LoadBot()
	if err != nil {
		t.Fatalf("the bot does not start with the compose environment: %v", err)
	}
	return c
}

var (
	composeKV  = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*|<<): (.*)$`)
	composeVar = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-?])([^}]*))?\}`)
)

// composeEnv is the environment compose gives service with the .env values
// env: the service's environment block, with `<<: *anchor` merged in from
// the file's top-level anchors and ${VAR}, ${VAR:-default}, ${VAR-default},
// ${VAR:?message} and ${VAR?message} filled in (a missing required value
// fails the test, as compose refuses to start).
func composeEnv(t *testing.T, service string, env map[string]string) map[string]string {
	t.Helper()
	lines := strings.Split(string(ComposeFile), "\n")
	// block reads the "KEY: value" lines indented by indent after lines[from].
	block := func(from int, indent string) [][2]string {
		var out [][2]string
		for _, line := range lines[from+1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if !strings.HasPrefix(line, indent) || line[len(indent)] == ' ' {
				break
			}
			m := composeKV.FindStringSubmatch(trimmed)
			if m == nil {
				t.Fatalf("compose line not understood: %q", line)
			}
			out = append(out, [2]string{m[1], m[2]})
		}
		return out
	}
	anchors := map[string]int{} // a top-level x-… block's anchor: its line
	svc, inServices := -1, false
	for i, line := range lines {
		if name, ok := strings.CutPrefix(line, "x-"); ok {
			if _, anchor, ok := strings.Cut(name, ": &"); ok {
				anchors[anchor] = i
			}
		}
		if line == "services:" {
			inServices = true
		}
		if inServices && line == "  "+service+":" {
			svc = i
			break
		}
	}
	if svc < 0 {
		t.Fatalf("no service %q in docker-compose.yml", service)
	}
	envAt := -1
	for i := svc + 1; i < len(lines) && (lines[i] == "" || strings.HasPrefix(lines[i], "    ")); i++ {
		if lines[i] == "    environment:" {
			envAt = i
			break
		}
	}
	if envAt < 0 {
		t.Fatalf("service %q has no environment block", service)
	}
	out, merged := map[string]string{}, map[string]string{}
	for _, kv := range block(envAt, "      ") {
		if kv[0] == "<<" {
			at, ok := anchors[strings.TrimPrefix(kv[1], "*")]
			if !ok {
				t.Fatalf("service %q merges %s, which is not a top-level anchor", service, kv[1])
			}
			for _, a := range block(at, "  ") {
				merged[a[0]] = a[1]
			}
			continue
		}
		out[kv[0]] = kv[1]
	}
	for k, v := range merged { // the service's own keys win, as in YAML
		if _, own := out[k]; !own {
			out[k] = v
		}
	}
	for k, v := range out {
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = composeVar.ReplaceAllStringFunc(v, func(ref string) string {
			m := composeVar.FindStringSubmatch(ref)
			val, ok := env[m[1]]
			empty := !ok || (strings.HasPrefix(m[2], ":") && val == "")
			switch {
			case !empty:
				return val
			case strings.HasSuffix(m[2], "-"):
				return m[3]
			case strings.HasSuffix(m[2], "?"):
				t.Fatalf("%s: compose refuses to start without %s (%s)", k, m[1], m[3])
			}
			return val
		})
	}
	return out
}

// dockerComposeEnv is the environment `docker compose config` gives service
// with the .env values env.
func dockerComposeEnv(t *testing.T, service string, env map[string]string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	var dotenv strings.Builder
	for k, v := range env {
		dotenv.WriteString(k + "=" + v + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), ComposeFile, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", "docker-compose.yml", "--env-file", ".env",
		"config", "--format", "json")
	cmd.Dir = dir
	// Only the .env file counts, not this process's BOBRES_* variables.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "BOBRES_") && !strings.HasPrefix(kv, "COMPOSE_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	var project struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(got, &project); err != nil {
		t.Fatalf("docker compose config: %v", err)
	}
	s, ok := project.Services[service]
	if !ok {
		t.Fatalf("docker compose config has no service %q", service)
	}
	if s.Environment == nil {
		s.Environment = map[string]string{}
	}
	return s.Environment
}
