package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
)

// Test values only: none of them is a real secret. They have the shapes the
// bundle must catch.
var (
	hexKey64 = strings.Repeat("a1b2c3d4", 8) // generated .env secrets: hex
	hexKey48 = strings.Repeat("0f9e8d", 8)   // generated database passwords
	// Built from parts so the repository's secret scanner does not take the
	// test values for real keys.
	b64Left      = strings.Repeat("Ab3", 7)
	b64Right     = strings.Repeat("Zx9", 7) + "="
	b64Std       = b64Left + "+/" + b64Right                                                          // standard base64, with a slash
	b64URL       = base64.RawURLEncoding.EncodeToString([]byte("foobar-test-only-0123456789")) + "_x" // base64url
	merchantID   = "11111111-2222-3333-4444-555555555555"
	orderID      = "0b9f6c2e-4d1a-4e5b-9c3d-7a8b9c0d1e2f"
	licenseToken = base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"kid":"test","license_id":"lic_test","domain":"panel.example.com"}`)) +
		"." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a, 0xa5, 0x3c}, 22))
)

func TestRedactSecretShapes(t *testing.T) {
	r := newRedactor(nil)
	for _, c := range []struct {
		name, in string
		secrets  []string
	}{
		{"bot token", `{"msg":"getUpdates failed","err":"Post \"https://api.telegram.org/bot` + goodBotToken + `/getUpdates\": timeout"}`, []string{goodBotToken, "AAH-abcdefghijklmnopqrstuvwxyz012345"}},
		{"bot token in text", "token " + goodBotToken + " was revoked", []string{goodBotToken}},
		{"hex key", "BOBRES_CORE_MASTER_KEY " + hexKey64, []string{hexKey64}},
		{"hex password", `error="auth failed for ` + hexKey48 + `"`, []string{hexKey48}},
		{"base64 key", "key material " + b64Std + " end", []string{b64Std, b64Left, strings.TrimSuffix(b64Right, "=")}},
		{"base64url key", "webhook " + b64URL, []string{b64URL}},
		{"DSN", "connect: postgres://svc_core:test-db-password-1@postgres:5432/bobres?sslmode=disable", []string{"test-db-password-1", "svc_core:"}},
		{"Redis URL", `{"err":"dial redis://:redis-pw-test-77@redis:6379/0"}`, []string{"redis-pw-test-77"}},
		{"Zarinpal proxy", "proxy http://user:proxy-pw-test@203.0.113.10:3128 refused", []string{"proxy-pw-test"}},
		{"key=value DSN", "host=postgres user=svc_core password=short-pw dbname=bobres", []string{"short-pw"}},
		{"quoted DSN", "host=postgres password='two words' dbname=bobres", []string{"two words", "words"}},
		{"escaped value", `{"err":"connect: password=\"two words\" refused"}`, []string{"two words", "words"}},
		{"Bearer", "Authorization: Bearer test-xui-token-0123", []string{"test-xui-token-0123"}},
		{"Bearer in JSON", `{"header":"bearer abcdEFGH1234"}`, []string{"abcdEFGH1234"}},
		{"Basic", "Proxy-Authorization: Basic dXNlcjpwYXNzd29yZA==", []string{"dXNlcjpwYXNzd29yZA=="}},
		{"cookie", "Cookie: bobres_session=s3ss10n-v4lue", []string{"s3ss10n-v4lue"}},
		{"header list", `{"headers":{"Cookie":["sid=c00k1e-one", "x=c00k1e-two"],"Accept":["*/*"]}}`, []string{"c00k1e-one", "c00k1e-two"}},
		{"merchant id field", `{"msg":"zarinpal on","merchant_id":"` + merchantID + `"}`, []string{merchantID}},
		{"merchant id text", "zarinpal: merchant id " + merchantID + " is not active", []string{merchantID}},
		{"merchant id escaped", `{"err":"request {\"merchant_id\":\"` + merchantID + `\",\"amount\":1000}"}`, []string{merchantID}},
		{"3x-ui token field", `{"api_token":"xui-tok-1","apiToken":"xui-tok-2"}`, []string{"xui-tok-1", "xui-tok-2"}},
		{"3x-ui token slog text", `level=WARN msg="panel" token=xui-tok-3 secret="two words"`, []string{"xui-tok-3", "two words"}},
		{"env line", "BOBRES_TOKEN_CORE=abc123 BOBRES_DATABASE_URL=x", []string{"abc123", "=x"}},
		{"query string", "GET /cb?Authority=A0001&token=q-tok-1&x=1", []string{"q-tok-1"}},
		{"license token", "license " + licenseToken, []string{licenseToken, strings.Split(licenseToken, ".")[0], strings.Split(licenseToken, ".")[1]}},
		{"license field", `{"license":"anything"}`, []string{"anything"}},
	} {
		got := r.line(c.in)
		for _, s := range c.secrets {
			if strings.Contains(got, s) {
				t.Errorf("%s: %q survived:\n  in:  %s\n  out: %s", c.name, s, c.in, got)
			}
		}
		if !strings.Contains(got, redacted) {
			t.Errorf("%s: nothing redacted: %s", c.name, got)
		}
	}
}

// caddyAccessLine is an entry of Caddy's access log (deploy/Caddyfile logs
// every request as JSON), as `compose logs --timestamps` prints it: a
// customer opening the pay page. Test addresses only (RFC 5737 and 3849).
var caddyAccessLine = `caddy-1  | 2026-10-12T10:00:00.123456789Z {"level":"info","ts":1760263200.1234,"logger":"http.log.access.log0","msg":"handled request",` +
	`"request":{"remote_ip":"198.51.100.7","remote_port":"52344","client_ip":"198.51.100.7","proto":"HTTP/2.0","method":"GET","host":"panel.example.com",` +
	`"uri":"/pay/` + orderID + `","headers":{"User-Agent":["Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"],` +
	`"Accept":["text/html"],"X-Forwarded-For":["203.0.113.9, 10.0.0.2"],"X-Real-Ip":["203.0.113.9"]},` +
	`"tls":{"resumed":false,"version":772,"proto":"h2","server_name":"panel.example.com"}},"bytes_read":0,"user_id":"","duration":0.0042,"size":5120,"status":200}`

func TestRedactVisitorsInCaddyAccessLog(t *testing.T) {
	r := newRedactor(nil)
	got := r.text(strings.Join([]string{
		caddyAccessLine,
		`caddy-1  | {"logger":"http.log.access.log0","request":{"remote_ip":"2001:db8:85a3::8a2e:370:7334","client_ip":"::ffff:198.51.100.7"}}`,
		`core-1  | level=WARN msg="login failed" ip=192.0.2.44 user_agent="curl/8.5.0"`,
		`bot-1  | User-Agent: TelegramBot (like TwitterBot)`,
	}, "\n"))
	for _, s := range []string{"198.51.100.7", "203.0.113.9", "192.0.2.44", "2001:db8:85a3", "8a2e:370:7334",
		"Mozilla", "iPhone", "Safari", "curl/8.5.0", "TelegramBot"} {
		if strings.Contains(got, s) {
			t.Errorf("%q survived:\n%s", s, got)
		}
	}
	lines := strings.Split(got, "\n")
	// The same visitor keeps one number: in both fields, and as an IPv4-mapped address.
	if !strings.Contains(lines[0], `"remote_ip":"[IP-1]","remote_port":"52344","client_ip":"[IP-1]"`) ||
		!strings.Contains(lines[1], `"client_ip":"[IP-1]"`) {
		t.Errorf("one address, not one number:\n%s", got)
	}
	// What support needs stays: the page, the status, the time, private addresses.
	for _, w := range []string{`"X-Forwarded-For":["[IP-2], 10.0.0.2"]`, `"X-Real-Ip":["[IP-2]"]`, `"uri":"/pay/` + orderID + `"`,
		`"status":200`, "2026-10-12T10:00:00.123456789Z", `"User-Agent":["` + redacted + `"]`, `user_agent=` + redacted} {
		if !strings.Contains(got, w) {
			t.Errorf("no %s:\n%s", w, got)
		}
	}
}

func TestRedactKeepsWhatSupportNeeds(t *testing.T) {
	r := newRedactor(nil)
	for _, in := range []string{
		`2026-10-12T10:00:00.123456789Z core-1  | {"time":"2026-10-12T10:00:00Z","level":"WARN","msg":"provisioning failed; will retry","service":"core","order_id":"` + orderID + `","attempt":3}`,
		`{"msg":"backup written","file":"bobres-20261011T033000Z.bkp","size":123456}`,
		`{"msg":"config","database_url_set":true,"redis_url_set":true}`,
		`level=INFO msg="startup" database_url_set=true webhook=false`,
		`error: telegram rejected the bot token or is unreachable (check BOBRES_TELEGRAM_BOT_TOKEN)`,
		`{"msg":"3x-ui server failed its health check","server":"Germany 1","detail":"the panel refused the API token"}`,
		`/opt/bobres/backups/bobres-20261011T033000Z-before-restore.bkp`,
		`image ghcr.io/sobhanaz/bobres-core:1.2.3 tg=123456789 email=u123456789-` + orderID,
		`/bobres.core.v1.CoreService/CreateOrderFromWallet took 12ms`,
		`NAME                IMAGE                                   SERVICE   STATUS`,
		`TestSupportBundleRedactsEverythingItShouldHide`,
		// Addresses of no visitor: Docker's networks, loopback, link-local, any.
		`dial tcp 172.18.0.4:5432: connect: connection refused (10.0.0.2, 192.168.1.10)`,
		`listening on [::]:8080 and 127.0.0.1:9090, [::1]:6060, fe80::1%eth0, 0.0.0.0:80`,
		// Not addresses at all: times, versions and numbers that look a little like one.
		`10:00:00.123 v1.2.3.4 1.2.3.4.5 build 2026.10.12.1 duration 0.0042 std::string`,
	} {
		if got := r.line(in); got != in {
			t.Errorf("changed:\n  in:  %s\n  out: %s", in, got)
		}
	}
}

func TestRedactInstallSecretsAnywhere(t *testing.T) {
	env := map[string]string{ //nolint:gosec // test values, not secrets
		"BOBRES_XUI_TOKEN": "tiny-xui-token", "REDIS_PASSWORD": "redispw99", "BOBRES_REDIS_URL": "redis://:redispw99@redis:6379/0",
		"BOBRES_DOMAIN": "panel.example.com", "BOBRES_ENV": "prod", "OPERATOR_SECRET_NOTE": "my-own-value",
		"BOBRES_LOG_LEVEL": "info", "SHORT": "abc",
	}
	r := newRedactor(env)
	got := r.text("panel said tiny-xui-token\nAUTH redispw99 failed\nmy-own-value here\nhttps://panel.example.com prod info abc")
	for _, s := range []string{"tiny-xui-token", "redispw99", "my-own-value"} {
		if strings.Contains(got, s) {
			t.Errorf("%q survived: %s", s, got)
		}
	}
	if !strings.Contains(got, "https://panel.example.com prod info abc") {
		t.Errorf("settings were scrubbed: %s", got)
	}
}

// bundleRunner plays Docker for the bundle: the commands it ran, and their
// output (logs carry secrets the bundle must scrub).
type bundleRunner struct {
	calls  []string
	failPS bool
	down   bool // the daemon does not answer at all
}

func (f *bundleRunner) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if f.down {
		return []byte("Cannot connect to the Docker daemon"), errors.New("exit status 1")
	}
	if len(args) > 1 && args[0] == "compose" && args[1] == "--project-name" {
		args = args[len(composeArgs()):]
		cmd = strings.Join(args, " ")
	}
	switch {
	case cmd == "version --format {{.Server.Version}}":
		return []byte("27.3.1\n"), nil
	case cmd == "compose version --short":
		return []byte("2.29.7\n"), nil
	case cmd == "images":
		return []byte("CONTAINER  REPOSITORY  TAG\nbobres-core-1  ghcr.io/sobhanaz/bobres-core  1.2.3\n"), nil
	case cmd == "ps --all":
		if f.failPS {
			return []byte("boom"), errors.New("exit status 1")
		}
		return []byte("NAME  SERVICE  STATUS\nbobres-core-1  core  Up 2 hours (healthy)\n"), nil
	case cmd == "system df":
		return []byte("TYPE  TOTAL  SIZE\nImages  9  1.2GB\n"), nil
	case strings.HasPrefix(cmd, "logs --no-color --timestamps --tail 300 "):
		s := args[len(args)-1]
		return []byte(fmt.Sprintf(`%s-1  | {"level":"INFO","msg":"started","service":"%s","order_id":"%s"}
%s-1  | {"level":"WARN","msg":"getUpdates failed","err":"https://api.telegram.org/bot%s/getUpdates: 401"}
%s-1  | {"level":"ERROR","msg":"db","err":"postgres://svc_core:%s@postgres:5432/bobres refused"}
%s-1  | Authorization: Bearer %s
%s-1  | merchant_id=%s key=%s
%s
`, s, s, orderID, s, goodBotToken, s, hexKey48, s, "xui-test-panel-token", s, merchantID, b64Std, caddyAccessLine)), nil
	}
	return nil, fmt.Errorf("unexpected docker command %q", cmd)
}

// bundleInstall is an install with secrets in .env, a backup and its
// sidecar, and a link the bundle must not follow.
func bundleInstall(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{ //nolint:gosec // test values, not secrets
		"BOBRES_DOMAIN": "panel.example.com", "BOBRES_VERSION": "1.2.3", "BOBRES_ENV": "prod",
		"POSTGRES_PASSWORD": hexKey48 + "00", "DB_CORE_PASSWORD": hexKey48, "REDIS_PASSWORD": "redis-test-pw-1",
		"BOBRES_REDIS_URL": "redis://:redis-test-pw-1@redis:6379/0", "BOBRES_TOKEN_CORE": hexKey64,
		"BOBRES_BACKUP_KEY": installKey, "BOBRES_CORE_MASTER_KEY": "install-core-master-key-tests-only",
		"BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken, "BOBRES_XUI_TOKEN": "xui-test-panel-token",
		"BOBRES_ZARINPAL_MERCHANT_ID": merchantID, "BOBRES_ZARINPAL_PROXY": "http://u:zp-proxy-pw@203.0.113.10:3128",
		"COMPOSE_PROFILES": "backup", "BOBRES_XUI_URL": "", "OPERATOR_NOTE": "CHANGE_ME_later",
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	bk := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bk, 0o700); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 11, 3, 30, 0, 0, time.UTC)
	name := backup.FileName(created)
	if err := os.WriteFile(filepath.Join(bk, name), []byte("BACKUP-CONTENT-MUST-NOT-LEAK"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := backup.WriteSidecar(filepath.Join(bk, backup.SidecarName(name)), backup.Sidecar{
		File: name, Size: 28, CreatedAt: created, TelegramError: "blocked: the owner has blocked the bot",
	}); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(secretFile, []byte(`{"error":"LINKED-FILE-MUST-NOT-LEAK"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secretFile, filepath.Join(bk, "bobres-20261010T033000Z.json")); err != nil {
		t.Fatal(err)
	}
	return dir, env
}

func testBundler(f *bundleRunner) (*bundler, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	o := &ops{
		in: strings.NewReader(""), out: &out, errOut: &errOut, run: f.run,
		stream: func(context.Context, string, ...string) error { return errors.New("unexpected stream") },
		pipe: func(context.Context, string, io.Reader, io.Writer, ...string) error {
			return errors.New("the bundle must never dump the database")
		},
		freeGB: func(string) float64 { return 42 }, sleep: func(time.Duration) {},
		now: func() time.Time { return opsNow },
	}
	b := &bundler{
		ops: o,
		doctor: func(domain string, offline bool) []Result {
			return []Result{{"Disk", Pass, "42.0 GB free"}, {"Port 80", Fail, "already in use"},
				{"DNS", Pass, fmt.Sprintf("%s -> 203.0.113.5 (offline=%v)", domain, offline)}}
		},
		readFile: func(p string) ([]byte, error) {
			switch p {
			case "/proc/meminfo":
				return []byte("MemTotal:        2035560 kB\nMemFree:  1000 kB\nMemAvailable:    1024000 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"), nil
			case "/etc/os-release":
				return []byte("NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n"), nil
			}
			return nil, os.ErrNotExist
		},
		disk: func(string) (float64, float64, bool) { return 80, 42, true },
	}
	return b, &out, &errOut
}

// readBundle returns the bundle's files by name (without the top folder).
func readBundle(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // the test's own file
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // read-only
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	files := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if h.Mode != 0o600 {
			t.Errorf("%s: mode %o", h.Name, h.Mode)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		_, name, _ := strings.Cut(h.Name, "/")
		files[name] = string(b)
	}
}

func TestSupportBundle(t *testing.T) {
	dir, env := bundleInstall(t)
	f := &bundleRunner{}
	b, out, errOut := testBundler(f)
	if code := b.supportBundleCmd([]string{"--dir", dir, "--offline"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	path := filepath.Join(dir, "bobres-support-20261011T120000Z.tar.gz")
	if !strings.Contains(out.String(), "Support bundle: "+path) {
		t.Fatalf("path not printed:\n%s", out.String())
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("bundle mode %o, want 600", st.Mode().Perm())
	}
	files := readBundle(t, path)
	want := []string{"README.txt", "versions.txt", "compose-ps.txt", "doctor.txt", "env-keys.txt", "system.txt", "backups.txt"}
	for _, s := range allServices(env) {
		want = append(want, "logs/"+s+".log")
	}
	for _, n := range want {
		if _, ok := files[n]; !ok {
			t.Errorf("bundle has no %s (has %v)", n, bundleNames(files))
		}
	}
	if len(files) != len(want) {
		t.Errorf("bundle has %d files, want %d: %v", len(files), len(want), bundleNames(files))
	}

	// No .env value but the plain settings, no backup, no linked file: anywhere.
	all := strings.Join(bundleTexts(files), "\n")
	for k, v := range env {
		if v == "" || notSecretEnv[k] || k == "OPERATOR_NOTE" {
			continue
		}
		if strings.Contains(all, v) {
			t.Errorf("the value of %s is in the bundle", k)
		}
	}
	for _, s := range []string{"BACKUP-CONTENT-MUST-NOT-LEAK", "LINKED-FILE-MUST-NOT-LEAK", goodBotToken, b64Std, "zp-proxy-pw", "redis-test-pw-1",
		"198.51.100.7", "203.0.113.9", "203.0.113.5", "Mozilla"} { // a visitor's address and browser, the server's address
		if strings.Contains(all, s) {
			t.Errorf("%q is in the bundle", s)
		}
	}

	// What support needs is there.
	for name, wants := range map[string][]string{
		"versions.txt":   {"Install version: 1.2.3", "Ubuntu 24.04.1 LTS", "Docker:          27.3.1", "Docker Compose:  2.29.7", "bobres-core  1.2.3"},
		"compose-ps.txt": {"Up 2 hours (healthy)"},
		"doctor.txt":     {"panel.example.com -> [IP-1] (offline=true)", "Port 80", "2 passed, 0 warnings, 1 failed"},
		"logs/core.log":  {`"msg":"started"`, orderID, "getUpdates failed", "Authorization: " + redacted, "merchant_id=" + redacted, "key=" + redacted, `"remote_ip":"[IP-2]"`},
		"env-keys.txt":   {"BOBRES_TELEGRAM_BOT_TOKEN          set", "BOBRES_XUI_URL                     empty", "OPERATOR_NOTE                      placeholder", "File mode: 0600"},
		"system.txt":     {"42.0 GB free of 80.0 GB", "MemTotal:     1987 MB", "MemAvailable: 1000 MB", "Images  9  1.2GB", "Backups folder"},
		"backups.txt":    {"bobres-20261011T033000Z.bkp", "not sent to Telegram", "telegram: blocked: the owner has blocked the bot", "bobres-20261010T033000Z.json (not a regular file)", "Restore point: none"},
		"README.txt":     {"Not included: the database, the backups", "every public IP address, replaced with [IP-1]"},
	} {
		for _, w := range wants {
			if !strings.Contains(files[name], w) {
				t.Errorf("%s has no %q:\n%s", name, w, files[name])
			}
		}
	}
	for _, c := range f.calls {
		if strings.Contains(c, "config") || strings.Contains(c, "inspect") || strings.Contains(c, "exec") {
			t.Errorf("the bundle ran %q (it could print secrets)", c)
		}
	}
}

func TestSupportBundleWhenDockerIsDown(t *testing.T) {
	dir, _ := bundleInstall(t)
	b, out, errOut := testBundler(&bundleRunner{down: true})
	if code := b.supportBundleCmd([]string{"--dir", dir}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Some parts could not be collected") || !strings.Contains(out.String(), "docker compose ps") {
		t.Errorf("missing parts not reported:\n%s", out.String())
	}
	files := readBundle(t, filepath.Join(dir, "bobres-support-20261011T120000Z.tar.gz"))
	if !strings.Contains(files["README.txt"], "Could not be collected") || !strings.Contains(files["compose-ps.txt"], "Cannot connect to the Docker daemon") {
		t.Errorf("README:\n%s\ncompose-ps:\n%s", files["README.txt"], files["compose-ps.txt"])
	}
	// The parts that need no Docker are still there.
	if !strings.Contains(files["env-keys.txt"], "BOBRES_TOKEN_CORE") || !strings.Contains(files["doctor.txt"], "passed") {
		t.Errorf("env-keys:\n%s\ndoctor:\n%s", files["env-keys.txt"], files["doctor.txt"])
	}
}

func TestSupportBundleRestorePoint(t *testing.T) {
	dir, _ := bundleInstall(t)
	rpDir := restorePointPath(dir)
	used := opsNow
	if err := os.MkdirAll(rpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeRestorePoint(rpDir, &restorePoint{FromVersion: "1.2.2", ToVersion: "1.2.3", CreatedAt: opsNow.Add(-time.Hour), UsedAt: &used}); err != nil {
		t.Fatal(err)
	}
	b, _, errOut := testBundler(&bundleRunner{})
	if code := b.supportBundleCmd([]string{"--dir", dir}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	files := readBundle(t, filepath.Join(dir, "bobres-support-20261011T120000Z.tar.gz"))
	if w := "Restore point: the update from 1.2.2 to 1.2.3, made 2026-10-11T11:00:00Z, used 2026-10-11T12:00:00Z."; !strings.Contains(files["backups.txt"], w) {
		t.Errorf("backups.txt has no %q:\n%s", w, files["backups.txt"])
	}
}

// The backup service's user owns the backups folder: a sidecar swapped for a
// named pipe after backups() looked must not hang the bundle (a plain open
// waits for a writer that never comes).
func TestSidecarTextNamedPipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bobres-20261011T033000Z.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	done := make(chan string, 1)
	go func() { done <- sidecarText(path) }()
	select {
	case got := <-done:
		if !strings.Contains(got, "(not a regular file)") {
			t.Errorf("a named pipe: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sidecarText blocked opening a named pipe")
	}
}

func TestSupportBundleUsage(t *testing.T) {
	b, _, errOut := testBundler(&bundleRunner{})
	if code := b.supportBundleCmd([]string{"--dir", t.TempDir()}); code != 2 || !strings.Contains(errOut.String(), "no install found") {
		t.Errorf("no install: %d %q", code, errOut.String())
	}
	errOut.Reset()
	dir, _ := bundleInstall(t)
	if code := b.supportBundleCmd([]string{"--dir", dir, "extra"}); code != 2 || !strings.Contains(errOut.String(), supportUsage) {
		t.Errorf("extra argument: %d %q", code, errOut.String())
	}
	if code := b.supportBundleCmd([]string{"--bogus"}); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bobres-support-") {
			t.Errorf("a refused run wrote %s", e.Name())
		}
	}
}

func TestSupportBundleCapsALongLog(t *testing.T) {
	big := strings.Repeat("x", 100) + "\n"
	f := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		return []byte(strings.Repeat(big, maxBundleLog/len(big)+100) + "the newest line\n"), nil
	}
	b, _, _ := testBundler(&bundleRunner{})
	b.run = f
	c := &collector{b: b, dir: t.TempDir(), red: newRedactor(nil)}
	got := c.logs("core")
	if len(got) > maxBundleLog+100 || !strings.HasPrefix(got, "[older lines left out") || !strings.HasSuffix(got, "the newest line\n") {
		t.Errorf("log of %d bytes, starts %q", len(got), got[:40])
	}
}

func bundleNames(files map[string]string) []string {
	var ns []string
	for n := range files {
		ns = append(ns, n)
	}
	return ns
}

func bundleTexts(files map[string]string) []string {
	var ts []string
	for _, t := range files {
		ts = append(ts, t)
	}
	return ts
}
