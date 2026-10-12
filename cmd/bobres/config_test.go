package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fullInstall is an install with every setting and secret bobres install
// writes (test values only: old-<key>), with or without the backup service.
func fullInstall(t *testing.T, backups bool) string {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{
		"BOBRES_ENV": "prod", "BOBRES_LOG_LEVEL": "info", "BOBRES_LOG_FORMAT": "json", "BOBRES_VERSION": "1.2.3",
		"BOBRES_DOMAIN": "panel.example.com", "BOBRES_ADMIN_TELEGRAM_ID": "4242", "BOBRES_TIMEZONE": "Asia/Tehran",
		"BOBRES_BACKUP_TIME": "03:30", "BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken, "BOBRES_XUI_ALLOW_PRIVATE": "false",
		"OPERATOR_NOTE": "keep me",
	}
	for k := range generated {
		env[k] = "old-" + strings.ToLower(k)
	}
	env["BOBRES_BACKUP_KEY"] = installKey
	for k, v := range installMasterKeys {
		env[k] = v
	}
	env["BOBRES_REDIS_URL"] = "redis://:" + env["REDIS_PASSWORD"] + "@redis:6379/0"
	if backups {
		env["COMPOSE_PROFILES"], env["BOBRES_BACKUP_URL"] = "backup", "http://backup:8080"
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readEnvFile(t *testing.T, dir string) (map[string]string, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	return env, string(b)
}

// healthyWithBackup is compose ps for a running install with the backup service.
var healthyWithBackup = strings.TrimSuffix(healthyPS, "]") + `,{"Service":"backup","State":"running","Health":"healthy"}]`

func TestConfigGetAndList(t *testing.T) {
	dir := fullInstall(t, true)
	o, out, errOut := dbOps("", &fakeDB{})
	if code := o.configCmd([]string{"get", "bobres_domain", "--dir", dir}); code != 0 || out.String() != "panel.example.com\n" {
		t.Fatalf("get: %d %q %s", code, out, errOut)
	}
	out.Reset()
	if code := o.configCmd([]string{"--dir", dir, "get", "BOBRES_ZARINPAL_HOST"}); code != 0 || out.String() != "\n" {
		t.Fatalf("get a derived key: %d %q", code, out)
	}
	for key, hint := range map[string]string{
		"POSTGRES_PASSWORD": "bobres secrets rotate db-passwords", "BOBRES_TELEGRAM_BOT_TOKEN": "bobres secrets rotate bot-token",
		"BOBRES_TOKEN_CORE": "bobres secrets rotate service-tokens", "REDIS_PASSWORD": "bobres secrets rotate redis-password",
		"BOBRES_CORE_MASTER_KEY": "never rotated", "BOBRES_BACKUP_KEY": "Backup key", "BOBRES_VERSION": "bobres update",
		"OPERATOR_NOTE": "bobres config list", "NO_SUCH_KEY": "not a setting bobres config knows",
	} {
		out.Reset()
		errOut.Reset()
		if code := o.configCmd([]string{"get", key, "--dir", dir}); code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), hint) {
			t.Errorf("get %s: %d %q %s", key, code, out, errOut)
		}
	}

	out.Reset()
	if code := o.configCmd([]string{"list", "--dir", dir}); code != 0 {
		t.Fatalf("list: %d", code)
	}
	s := out.String()
	for _, want := range []string{"BOBRES_DOMAIN                  panel.example.com", "BOBRES_LOG_LEVEL               info",
		"BOBRES_TELEGRAM_API_URL        -", "(follows BOBRES_ZARINPAL_PUBLIC_URL)", "123456789:••••   (bobres secrets rotate bot-token)",
		"POSTGRES_PASSWORD              ••••   (bobres secrets rotate db-passwords)", "BOBRES_VERSION                 1.2.3   (bobres update)",
		"COMPOSE_PROFILES               backup   (set by bobres install)"} {
		if !strings.Contains(s, want) {
			t.Errorf("list missing %q:\n%s", want, s)
		}
	}
	// No secret value (an operator's own key counts as one).
	env, _ := readEnvFile(t, dir)
	for k, v := range env {
		if isSecretKey(k) && v != "" && strings.Contains(s, v) {
			t.Errorf("list shows %s:\n%s", k, s)
		}
	}
	if !strings.Contains(s, "OPERATOR_NOTE                  ••••") {
		t.Fatalf("operator key:\n%s", s)
	}
}

func TestConfigSet(t *testing.T) {
	dir := fullInstall(t, true)
	before, _ := readEnvFile(t, dir)
	f := &fakeDB{ps: func() string { return healthyWithBackup }}
	o, out, errOut := dbOps("", f)
	if code := o.configCmd([]string{"set", "BOBRES_LOG_LEVEL", "DEBUG", "--dir", dir}); code != 0 {
		t.Fatalf("set: %d %s", code, errOut)
	}
	env, _ := readEnvFile(t, dir)
	if env["BOBRES_LOG_LEVEL"] != "debug" {
		t.Fatalf("value: %q", env["BOBRES_LOG_LEVEL"])
	}
	for k, v := range before { // every other key and secret is kept
		if k != "BOBRES_LOG_LEVEL" && env[k] != v {
			t.Errorf("%s changed: %q -> %q", k, v, env[k])
		}
	}
	if st, _ := os.Stat(filepath.Join(dir, ".env")); st.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode %v", st.Mode())
	}
	// Only the services that read it are recreated, nothing else.
	if want := []string{"up -d --no-deps core payments provisioner bot backup"}; strings.Join(f.steps, "|") != want[0] {
		t.Fatalf("compose: %q", f.steps)
	}
	if !strings.Contains(out.String(), "Saved BOBRES_LOG_LEVEL=debug") || !strings.Contains(out.String(), "All services are healthy") {
		t.Fatalf("output: %s", out)
	}

	// The same value again: nothing to do.
	f.steps = nil
	if code := o.configCmd([]string{"set", "BOBRES_LOG_LEVEL", "debug", "--dir", dir}); code != 0 || len(f.steps) != 0 {
		t.Fatalf("unchanged: %d %q", code, f.steps)
	}
	// The bot reads the local Bot API server's address; an empty value is the default.
	for _, v := range []string{"http://host.docker.internal:8081", ""} {
		f.steps = nil
		if code := o.configCmd([]string{"set", "BOBRES_TELEGRAM_API_URL", v, "--dir", dir}); code != 0 || strings.Join(f.steps, "|") != "up -d --no-deps bot" {
			t.Fatalf("api url %q: %d %q %s", v, code, f.steps, errOut)
		}
		if env, _ := readEnvFile(t, dir); env["BOBRES_TELEGRAM_API_URL"] != v {
			t.Fatalf("api url: %q", env["BOBRES_TELEGRAM_API_URL"])
		}
	}
	// The domain: Caddy too, and the Zarinpal host follows.
	f.steps = nil
	if code := o.configCmd([]string{"set", "BOBRES_ZARINPAL_PUBLIC_URL", "https://pay.example.com", "--dir", dir}); code != 0 ||
		strings.Join(f.steps, "|") != "up -d --no-deps caddy payments" {
		t.Fatalf("zarinpal: %d %q", code, f.steps)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_ZARINPAL_HOST"] != "pay.example.com" {
		t.Fatalf("zarinpal host: %q", env["BOBRES_ZARINPAL_HOST"])
	}
	f.steps = nil
	if code := o.configCmd([]string{"set", "BOBRES_DOMAIN", "Pay.Example.com", "--dir", dir}); code != 0 ||
		strings.Join(f.steps, "|") != "up -d --no-deps caddy core payments" || !strings.Contains(out.String(), "must point to this server") {
		t.Fatalf("domain: %d %q %s", code, f.steps, out)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_DOMAIN"] != "pay.example.com" || env["BOBRES_ZARINPAL_HOST"] != "" {
		t.Fatalf("domain %q, zarinpal host %q", env["BOBRES_DOMAIN"], env["BOBRES_ZARINPAL_HOST"])
	}
	for _, c := range [][2]string{{"BOBRES_TIMEZONE", "Europe/Berlin"}, {"BOBRES_BACKUP_TIME", "4:05"}, {"BOBRES_ADMIN_TELEGRAM_ID", "777"}, {"BOBRES_LOG_FORMAT", "text"}} {
		if code := o.configCmd([]string{"set", c[0], c[1], "--dir", dir}); code != 0 {
			t.Errorf("set %s %s: %d %s", c[0], c[1], code, errOut)
		}
	}
}

func TestConfigSetRefusals(t *testing.T) {
	dir := fullInstall(t, true)
	_, before := readEnvFile(t, dir)
	f := &fakeDB{}
	o, out, errOut := dbOps("", f)
	for _, c := range []struct {
		key, value, want string
	}{
		{"BOBRES_TIMEZONE", "Mars/Olympus", "unknown time zone"},
		{"BOBRES_TIMEZONE", "Local", "unknown time zone"},
		{"BOBRES_BACKUP_TIME", "25:00", "time of day like 03:30"},
		{"BOBRES_LOG_LEVEL", "loud", "must be one of debug, info, warn, error"},
		{"BOBRES_DOMAIN", "not a domain", "is not a valid host name"},
		{"BOBRES_DOMAIN", "", "cannot be empty"},
		{"BOBRES_ADMIN_TELEGRAM_ID", "0", "positive number"},
		{"BOBRES_TELEGRAM_API_URL", "ftp://x", "http(s) URL"},
		{"BOBRES_TELEGRAM_API_URL", "https://user:pw@x", "without credentials"},
		{"BOBRES_ZARINPAL_PUBLIC_URL", "http://pay.example.com", "Zarinpal public URL must be https"},
		{"BOBRES_TELEGRAM_API_URL", "http://x/'", "contains a quote"},
		{"POSTGRES_PASSWORD", "x", "is a secret"},
		{"BOBRES_VERSION", "9.9.9", "bobres update"},
		{"BOBRES_ZARINPAL_HOST", "x.example.com", "follows BOBRES_ZARINPAL_PUBLIC_URL"},
		{"COMPOSE_PROFILES", "", "set by bobres install"},
		{"NO_SUCH_KEY", "1", "not a setting bobres config knows"},
	} {
		errOut.Reset()
		if code := o.configCmd([]string{"set", c.key, c.value, "--dir", dir}); code != 1 || !strings.Contains(errOut.String(), c.want) {
			t.Errorf("set %s %q: %d %s", c.key, c.value, code, errOut)
		}
	}
	if _, after := readEnvFile(t, dir); after != before || len(f.steps) != 0 || strings.Contains(out.String(), "Saved") {
		t.Fatalf("a refused set changed something: steps %q\n%s", f.steps, out)
	}
	for _, args := range [][]string{nil, {"get"}, {"set", "BOBRES_DOMAIN"}, {"list", "x"}, {"remove", "x"}, {"get", "--bogus"}} {
		if code := o.configCmd(append(args, "--dir", dir)); code != 2 {
			t.Errorf("%v: %d", args, code)
		}
	}
	if code := o.configCmd([]string{"get", "BOBRES_DOMAIN", "--dir", t.TempDir()}); code != 2 || !strings.Contains(errOut.String(), "no install found") {
		t.Fatalf("no install: %d", code)
	}
}

func TestConfigSetWithoutRestart(t *testing.T) {
	dir := fullInstall(t, false)
	stopped := `[{"Service":"postgres","State":"exited","Health":""},{"Service":"core","State":"exited","Health":""}]`
	f := &fakeDB{ps: func() string { return stopped }}
	o, out, _ := dbOps("", f)
	// A stopped store stays stopped.
	if code := o.configCmd([]string{"set", "BOBRES_TIMEZONE", "UTC", "--dir", dir}); code != 0 || len(f.steps) != 0 || !strings.Contains(out.String(), "BOBRES is not running") {
		t.Fatalf("stopped: %d %q %s", code, f.steps, out)
	}
	// --no-restart writes only.
	f.ps = nil
	if code := o.configCmd([]string{"set", "BOBRES_LOG_FORMAT", "text", "--no-restart", "--dir", dir}); code != 0 || len(f.steps) != 0 || !strings.Contains(out.String(), "Not restarted (--no-restart)") {
		t.Fatalf("--no-restart: %d %q %s", code, f.steps, out)
	}
	// The backup time without the backup service: nothing reads it.
	if code := o.configCmd([]string{"set", "BOBRES_BACKUP_TIME", "05:00", "--dir", dir}); code != 0 || len(f.steps) != 0 || !strings.Contains(out.String(), "nothing to restart") {
		t.Fatalf("no backup service: %d %q %s", code, f.steps, out)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_TIMEZONE"] != "UTC" || env["BOBRES_LOG_FORMAT"] != "text" || env["BOBRES_BACKUP_TIME"] != "05:00" {
		t.Fatalf("env: %v", env)
	}
}

func TestConfigSetUnhealthy(t *testing.T) {
	dir := fullInstall(t, false)
	f := &fakeDB{ps: func() string {
		return strings.Replace(healthyPS, `"bot","State":"running","Health":"healthy"`, `"bot","State":"running","Health":"unhealthy"`, 1)
	}}
	o, _, errOut := dbOps("", f)
	if code := o.configCmd([]string{"set", "BOBRES_TELEGRAM_API_URL", "http://host.docker.internal:8081", "--dir", dir}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "bot=unhealthy") || !strings.Contains(errOut.String(), `To go back: bobres config set BOBRES_TELEGRAM_API_URL "" --dir `+dir) {
		t.Fatalf("errors: %s", errOut)
	}
}
