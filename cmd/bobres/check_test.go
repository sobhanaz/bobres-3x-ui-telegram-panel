package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkEnvMap is an .env as the installer makes it.
func checkEnvMap() map[string]string {
	env := map[string]string{
		"BOBRES_DOMAIN": "panel.example.com", "BOBRES_ADMIN_TELEGRAM_ID": "4242", "BOBRES_VERSION": "1.2.3",
		"BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken, "BOBRES_REDIS_URL": "redis://:pw@redis:6379/0",
	}
	for k, n := range generated {
		env[k] = hex.EncodeToString(make([]byte, n))
	}
	return env
}

func TestCheckEnv(t *testing.T) {
	if r := checkEnv(checkEnvMap()); r.Status != Pass {
		t.Fatalf("a complete .env: %+v", r)
	}
	env := checkEnvMap()
	delete(env, "BOBRES_CORE_MASTER_KEY")
	delete(env, "BOBRES_REDIS_URL")
	env["BOBRES_TOKEN_BOT"] = "short"
	env["BOBRES_DOMAIN"] = "not a domain"
	env["BOBRES_TELEGRAM_BOT_TOKEN"] = "secret-value-that-is-no-token"
	env["BOBRES_ADMIN_TELEGRAM_ID"] = "-5"
	r := checkEnv(env)
	if r.Status != Fail {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"missing: ", "BOBRES_CORE_MASTER_KEY", "BOBRES_REDIS_URL", "BOBRES_TOKEN_BOT (not the 64 hex", "BOBRES_DOMAIN (not a domain name)",
		"BOBRES_TELEGRAM_BOT_TOKEN (not shaped", "BOBRES_ADMIN_TELEGRAM_ID (not a positive number)"} {
		if !strings.Contains(r.Detail, want) {
			t.Errorf("detail lacks %q: %s", want, r.Detail)
		}
	}
	// a value is never printed
	for _, secret := range []string{"short", "secret-value-that-is-no-token"} {
		if strings.Contains(r.Detail, secret) {
			t.Errorf("detail repeats a value %q: %s", secret, r.Detail)
		}
	}
}

func TestCheckEnvMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	if r := checkEnvMode(p); r.Status != Skip {
		t.Fatalf("missing: %+v", r)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil { //nolint:gosec // the case under test
		t.Fatal(err)
	}
	if r := checkEnvMode(p); r.Status != Warn || !strings.Contains(r.Detail, "chmod 600") {
		t.Fatalf("0644: %+v", r)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := checkEnvMode(p); r.Status != Pass {
		t.Fatalf("0600: %+v", r)
	}
}

func backupFiles(t *testing.T, names ...string) []os.FileInfo {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), make([]byte, 1024), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return listBackups(dir)
}

func TestCheckBackupAge(t *testing.T) {
	if r := checkBackupAge(nil, screenNow); r.Status != Fail || !strings.Contains(r.Detail, "bobres backup") {
		t.Fatalf("none: %+v", r)
	}
	fresh := backupFiles(t, "bobres-20260101T000000Z.bkp", "bobres-20261016T030000Z.bkp")
	if r := checkBackupAge(fresh, screenNow); r.Status != Pass || !strings.Contains(r.Detail, "9 hours") {
		t.Fatalf("fresh: %+v", r)
	}
	stale := backupFiles(t, "bobres-20261012T030000Z.bkp")
	if r := checkBackupAge(stale, screenNow); r.Status != Warn || !strings.Contains(r.Detail, "4 days") {
		t.Fatalf("stale: %+v", r)
	}
}

func writeManifest(t *testing.T, expires string) string {
	t.Helper()
	dir := t.TempDir()
	body := "# version 1.2.3\n# channel stable\n# key_id a1\n# issued 2026-10-01T00:00Z\n"
	if expires != "" {
		body += "# expires " + expires + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCheckReleaseExpiry(t *testing.T) {
	for _, c := range []struct {
		name    string
		dir     string
		want    Status
		contain string
	}{
		{"no state dir", "", Skip, "no release state"},
		{"no manifest", t.TempDir(), Skip, "no release manifest"},
		{"no expiry", writeManifest(t, ""), Skip, "no expiry"},
		{"bad expiry", writeManifest(t, "soon"), Skip, "cannot read"},
		{"good", writeManifest(t, "2027-01-01T00:00Z"), Pass, "of 1.2.3 is good until 2027-01-01T00:00Z"},
		{"soon", writeManifest(t, "2026-10-20T00:00Z"), Warn, "expires 2026-10-20T00:00Z"},
		{"expired", writeManifest(t, "2026-10-01T00:00Z"), Fail, "expired on 2026-10-01T00:00Z"},
	} {
		r := checkReleaseExpiry(c.dir, screenNow)
		if r.Status != c.want || !strings.Contains(r.Detail, c.contain) {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
}

func TestWebhookVerdict(t *testing.T) {
	tok := goodBotToken
	if r := webhookVerdict(webhookInfo{}, nil, tok); r.Status != Pass || !strings.Contains(r.Detail, "long polling") {
		t.Fatalf("polling: %+v", r)
	}
	r := webhookVerdict(webhookInfo{URL: "https://hooks.example.com/secret-path-123"}, nil, tok)
	if r.Status != Pass || !strings.Contains(r.Detail, "hooks.example.com") || strings.Contains(r.Detail, "secret-path") {
		t.Fatalf("webhook shows only its host: %+v", r)
	}
	if r := webhookVerdict(webhookInfo{LastError: "Connection timed out"}, nil, tok); r.Status != Warn || !strings.Contains(r.Detail, "Connection timed out") {
		t.Fatalf("last error: %+v", r)
	}
	if r := webhookVerdict(webhookInfo{Pending: 500}, nil, tok); r.Status != Warn || !strings.Contains(r.Detail, "500 updates") {
		t.Fatalf("pending: %+v", r)
	}
	r = webhookVerdict(webhookInfo{}, errors.New("dial "+tok+" failed"), tok)
	if r.Status != Skip || strings.Contains(r.Detail, tok) {
		t.Fatalf("an error never carries the token: %+v", r)
	}
}

func TestParseCoreResults(t *testing.T) {
	good := `[{"name":"Database","status":"PASS","detail":"core is connected"},{"name":"License","status":"WARN","detail":"x"},{"name":"Odd","status":"MAYBE","detail":"y"}]`
	rs := parseCoreResults([]byte(good), nil)
	if len(rs) != 3 || rs[0].Status != Pass || rs[1].Status != Warn || rs[2].Status != Warn {
		t.Fatalf("%+v", rs)
	}
	// core exits 1 when a check failed, and still printed the array; docker's
	// own lines around it are ignored
	failed := "time=x\n" + strings.Replace(good, `"PASS"`, `"FAIL"`, 1) + "\n"
	if rs := parseCoreResults([]byte(failed), errors.New("exit status 1")); len(rs) != 3 || rs[0].Status != Fail {
		t.Fatalf("array with an error: %+v", rs)
	}
	for _, c := range []struct {
		out string
		err error
	}{{"", errors.New("service \"core\" is not running")}, {"no such service", nil}, {"[]", nil}} {
		rs := parseCoreResults([]byte(c.out), c.err)
		if len(rs) != 1 || rs[0].Name != "Core" || rs[0].Status != Fail || !strings.Contains(rs[0].Detail, "bobres logs core") {
			t.Errorf("%q: %+v", c.out, rs)
		}
	}
}

// newCheckTest builds a checker over a complete install, with Telegram and
// core answering as given.
func newCheckTest(t *testing.T) (*checker, *strings.Builder, *strings.Builder, string, *scriptedCore) {
	t.Helper()
	dir := installDir(t)
	env := checkEnvMap()
	env["BOBRES_VERSION"] = "1.2.3"
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	sc := &scriptedCore{scripted: scripted{ps: healthyPS}}
	o, out, errOut := screenOps(&sc.scripted, "")
	o.run = sc.run
	o.freeGB = func(string) float64 { return 42 }
	c := &checker{o: o, stateDir: writeManifest(t, "2027-01-01T00:00Z"),
		getMe:   func(context.Context, string, string) (string, error) { return "bobres_bot", nil },
		webhook: func(context.Context, string, string) (webhookInfo, error) { return webhookInfo{}, nil }}
	return c, out, errOut, dir, sc
}

// scriptedCore also answers `core check --json`.
type scriptedCore struct {
	scripted
	check    string
	checkErr error
}

func (s *scriptedCore) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if contains(args, "check") {
		s.calls = append(s.calls, args)
		return []byte(s.check), s.checkErr
	}
	return s.scripted.run(ctx, dir, args...)
}

const coreAllGood = `[{"name":"Database","status":"PASS","detail":"core is connected"},{"name":"Redis","status":"PASS","detail":"core is connected"},
{"name":"License","status":"PASS","detail":"trial, 20 days left"},{"name":"3x-ui servers","status":"PASS","detail":"1 of 1 healthy"},{"name":"Watchdog","status":"PASS","detail":"no open problem"}]`

func TestCheckCmdEndToEnd(t *testing.T) {
	c, out, errOut, dir, sc := newCheckTest(t)
	sc.check = coreAllGood
	if err := os.MkdirAll(filepath.Join(dir, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backups", "bobres-20261016T030000Z.bkp"), make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := c.run([]string{"--dir", dir}); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"PASS  Config", "PASS  Services", "PASS  Database", "PASS  License", "PASS  Backup", "PASS  Release signature",
		"PASS  Bot token", "@bobres_bot", "PASS  Bot updates", "0 failed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// core is asked once, as JSON, inside the core container
	n := 0
	for _, a := range sc.calls {
		if contains(a, "check") {
			n++
			if !contains(a, "core") || !contains(a, "--json") || !contains(a, "exec") {
				t.Errorf("core check args: %v", a)
			}
		}
	}
	if n != 1 {
		t.Errorf("core asked %d times", n)
	}
}

func TestCheckCmdFailuresAndJSON(t *testing.T) {
	c, out, errOut, dir, sc := newCheckTest(t)
	sc.check = strings.Replace(coreAllGood, `"PASS","detail":"1 of 1 healthy"`, `"FAIL","detail":"0 of 1 healthy; down: de1: timeout"`, 1)
	sc.checkErr = errors.New("exit status 1")
	c.getMe = func(_ context.Context, token, _ string) (string, error) {
		return "", errors.New("Unauthorized for " + token)
	}
	if code := c.run([]string{"--dir", dir, "--json"}); code != 1 {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	var v struct {
		Results []struct{ Name, Status, Detail string }
		Pass    int
		Warn    int
		Fail    int
	}
	if err := json.Unmarshal([]byte(out.String()), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if v.Fail < 2 || len(v.Results) < 10 {
		t.Fatalf("%+v", v)
	}
	got := map[string]string{}
	for _, r := range v.Results {
		got[r.Name] = r.Status
	}
	if got["3x-ui servers"] != "FAIL" || got["Bot token"] != "FAIL" || got["Bot updates"] != "SKIP" || got["Database"] != "PASS" {
		t.Fatalf("%v", got)
	}
	// the bot token is in no output, plain or JSON
	for _, s := range []string{out.String(), errOut.String()} {
		if strings.Contains(s, goodBotToken) {
			t.Fatalf("the bot token was printed:\n%s", s)
		}
	}
}

func TestCheckCmdUsage(t *testing.T) {
	c, _, errOut, dir, _ := newCheckTest(t)
	if code := c.run([]string{"--dir", dir, "extra"}); code != 2 || !strings.Contains(errOut.String(), "usage: bobres check") {
		t.Fatalf("%d %s", code, errOut)
	}
	if code := c.run([]string{"--dir", t.TempDir()}); code != 2 || !strings.Contains(errOut.String(), "no install found") {
		t.Fatalf("no install: %d %s", code, errOut)
	}
}

func TestCheckCmdCoreDown(t *testing.T) {
	c, out, _, dir, sc := newCheckTest(t)
	sc.checkErr = errors.New("service \"core\" is not running")
	if code := c.run([]string{"--dir", dir}); code != 1 || !strings.Contains(out.String(), "FAIL  Core") || !strings.Contains(out.String(), "bobres logs core") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestScrubAndHostOf(t *testing.T) {
	if got := scrub("bad "+goodBotToken+" and "+goodBotToken, goodBotToken); strings.Contains(got, goodBotToken) || strings.Count(got, "<token>") != 2 {
		t.Errorf("scrub: %q", got)
	}
	if hostOf("https://h.example.com:8443/x?y=1") != "h.example.com:8443" || hostOf("::bad") != "(address hidden)" {
		t.Errorf("hostOf")
	}
}

func TestTelegramWebhook(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"ok":true,"result":{"url":"https://hooks.example.com/x","pending_update_count":3,` +
			`"last_error_message":"old error","last_error_date":1}}`))
	}))
	defer srv.Close()
	w, err := telegramWebhook(context.Background(), goodBotToken, srv.URL)
	if err != nil || w.URL != "https://hooks.example.com/x" || w.Pending != 3 || path != "/bot"+goodBotToken+"/getWebhookInfo" {
		t.Fatalf("%+v %v %q", w, err, path)
	}
	// an error from long ago is history, not a state
	if w.LastError != "" {
		t.Errorf("stale error kept: %q", w.LastError)
	}
	// an unreachable Bot API: the error holds no address, so no token
	srv.Close()
	if _, err := telegramWebhook(context.Background(), goodBotToken, srv.URL); err == nil || strings.Contains(err.Error(), goodBotToken) {
		t.Fatalf("unreachable: %v", err)
	}
}
