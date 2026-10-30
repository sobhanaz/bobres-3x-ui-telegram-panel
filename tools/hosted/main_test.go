package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hosted"
)

// The made-up secrets of these tests.
const (
	testBotToken   = "123456789:AAFakeFakeFakeFakeFakeFakeFakeFake12" //gitleaks:allow
	testLicenseKey = "BOBRES-AAAA-BBBB-CCCC-DDDD-EEEE-FFFF-GGGG-HHHH" //gitleaks:allow
	testMetrics    = "bm_fakeMetricsTokenFakeMetricsTokenFake0123"    //gitleaks:allow
	testBackupKey  = "fake-backup-key-0123456789abcdef0123456789"     //gitleaks:allow
)

// fakeServer is the server over SSH: it answers the probe, records the scripts it was sent, and answers the finishing script with what a
// store prints.
type fakeServer struct {
	installed bool
	uid       string
	scripts   []string
	failAt    int // 1-based script that fails (0: none)
	after     string
}

func (f *fakeServer) Run(_ context.Context, script string) (string, error) {
	f.scripts = append(f.scripts, script)
	if f.failAt == len(f.scripts) {
		return "", errors.New("exit status 1")
	}
	switch {
	case strings.Contains(script, "bobres=installed"):
		state := "none"
		if f.installed {
			state = "installed"
		}
		return "uid=" + f.uid + "\nos=ubuntu 24.04\narch=x86_64\nbobres=" + state + "\n", nil
	case strings.Contains(script, "metrics-token generate"):
		return f.after, nil
	}
	return "", nil
}

type testEnv struct {
	a       *app
	srv     *fakeServer
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	dir     string
	host    string
	written map[string][]byte
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	e := &testEnv{srv: &fakeServer{uid: "0", after: "metrics_token=" + testMetrics + "\nversion=0.20.0\nbackup_key=" + testBackupKey + "\n"},
		out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, dir: dir, written: map[string][]byte{}}
	for name, body := range map[string]string{"bot.token": testBotToken + "\n", "lic.key": testLicenseKey + "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.a = &app{out: e.out, errOut: e.errOut, readFile: os.ReadFile, stat: os.Stat, now: func() time.Time { return time.Unix(1_790_000_000, 0) },
		writeFile: func(p string, b []byte, m os.FileMode) error {
			if m != 0o600 {
				t.Errorf("%s written with mode %v", p, m)
			}
			e.written[p] = b
			return nil
		}}
	e.a.newRunner = func(host, _, _ string) Runner { e.host = host; return e.srv }
	return e
}

func (e *testEnv) args(extra ...string) []string {
	return append([]string{"install", "--host", "root@203.0.113.7", "--domain", "Shop.Example.com", "--customer", "Acme VPN",
		"--admin-id", "123456789", "--bot-token-file", filepath.Join(e.dir, "bot.token"), "--out", e.dir}, extra...)
}

func TestInstallFreshServer(t *testing.T) {
	e := newTestEnv(t)
	if code := e.a.run(e.args("--channel", "beta", "--license-server", "https://license.example.com",
		"--license-key-file", filepath.Join(e.dir, "lic.key"), "--license-id", "lic_abc123")); code != 0 {
		t.Fatalf("install: %d\n%s", code, e.errOut)
	}
	if len(e.srv.scripts) != 3 {
		t.Fatalf("scripts sent: %d", len(e.srv.scripts))
	}
	install, after := e.srv.scripts[1], e.srv.scripts[2]
	// The installer runs non-interactively with the store's values; the bot token is in the script on ssh's stdin.
	for _, want := range []string{"export BOBRES_TELEGRAM_BOT_TOKEN='" + testBotToken + "'", "export BOBRES_UPDATE_CHANNEL='beta'",
		"--yes --domain 'shop.example.com' --admin-id '123456789'", DefaultInstaller} {
		if !strings.Contains(install, want) {
			t.Errorf("install script lacks %q:\n%s", want, install)
		}
	}
	for _, want := range []string{"bobres config set BOBRES_UPDATE_CHANNEL 'beta'", "bobres harden --yes --ssh-port '22'",
		"bobres config set BOBRES_LICENSE_SERVER 'https://license.example.com'", "'" + testLicenseKey + "' | bobres license activate",
		"bobres admin metrics-token generate"} {
		if !strings.Contains(after, want) {
			t.Errorf("finishing script lacks %q:\n%s", want, after)
		}
	}
	if strings.Contains(after, testBotToken) {
		t.Error("the bot token went to the server twice")
	}
	// The registration code: what the dashboard needs, never the bot token or the backup key.
	saved := e.written[filepath.Join(e.dir, "hosted-shop.example.com.txt")]
	if len(saved) == 0 {
		t.Fatalf("no registration file: %v", e.written)
	}
	reg, err := hosted.Decode(string(saved))
	if err != nil {
		t.Fatal(err)
	}
	want := hosted.Registration{Customer: "Acme VPN", Domain: "shop.example.com", Version: "0.20.0", Channel: "beta", LicenseID: "lic_abc123",
		MetricsToken: testMetrics, Created: 1_790_000_000}
	if *reg != want {
		t.Fatalf("registration: %+v", reg)
	}
	for _, secret := range []string{testBotToken, testBackupKey, testLicenseKey} {
		if bytes.Contains(saved, []byte(secret)) {
			t.Errorf("the registration file holds a secret %q", secret)
		}
	}
	// The backup key is shown once, for the customer; the bot token is never printed.
	out := e.out.String()
	if strings.Count(out, testBackupKey) != 1 || !strings.Contains(out, "Give it to the customer") {
		t.Errorf("backup key not shown once:\n%s", out)
	}
	if strings.Contains(out+e.errOut.String(), testBotToken) || strings.Contains(out+e.errOut.String(), testLicenseKey) {
		t.Error("a secret was printed")
	}
	if !strings.Contains(out, strings.TrimSpace(string(saved))) {
		t.Error("the registration code was not printed")
	}
}

func TestInstallRefusesAnInstalledServer(t *testing.T) {
	e := newTestEnv(t)
	e.srv.installed = true
	if code := e.a.run(e.args()); code != 1 || !strings.Contains(e.errOut.String(), "installed on this server already") {
		t.Fatalf("installed server: %d %s", code, e.errOut)
	}
	if len(e.srv.scripts) != 1 {
		t.Fatalf("it went on after the probe: %d scripts", len(e.srv.scripts))
	}
	e2 := newTestEnv(t)
	e2.srv.installed = true
	if code := e2.a.run(e2.args("--rerun")); code != 0 {
		t.Fatalf("--rerun: %d %s", code, e2.errOut)
	}
}

func TestInstallNeedsRoot(t *testing.T) {
	e := newTestEnv(t)
	e.srv.uid = "1000"
	if code := e.a.run(e.args()); code != 1 || !strings.Contains(e.errOut.String(), "root") {
		t.Fatalf("not root: %d %s", code, e.errOut)
	}
}

func TestInstallFailures(t *testing.T) {
	e := newTestEnv(t)
	e.srv.failAt = 2
	if code := e.a.run(e.args()); code != 1 || !strings.Contains(e.errOut.String(), "the installer failed") {
		t.Fatalf("installer failure: %d %s", code, e.errOut)
	}
	if len(e.written) != 0 {
		t.Error("a registration was saved for a failed install")
	}
	e2 := newTestEnv(t)
	e2.srv.after = "version=0.20.0\n"
	if code := e2.a.run(e2.args()); code != 1 || !strings.Contains(e2.errOut.String(), "metrics token") {
		t.Fatalf("no metrics token: %d %s", code, e2.errOut)
	}
}

func TestInstallChecksItsInputs(t *testing.T) {
	e := newTestEnv(t)
	bad := filepath.Join(e.dir, "bad.token")
	if err := os.WriteFile(bad, []byte("not a token"), 0o600); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(e.dir, "loose.token")
	if err := os.WriteFile(loose, []byte(testBotToken), 0o644); err != nil { //nolint:gosec // the test checks the warning
		t.Fatal(err)
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no host", []string{"install", "--domain", "a.example.com"}, "--host"},
		{"bad domain", e.args("--domain", "10.0.0.1"), "domain"},
		{"domain with port", e.args("--domain", "shop.example.com:8443"), "domain"},
		{"bad admin", e.args("--admin-id", "abc"), "--admin-id"},
		{"no customer", e.args("--customer", " "), "customer"},
		{"bad channel", e.args("--channel", "nightly"), "channel"},
		{"bad token", e.args("--bot-token-file", bad), "bot token"},
		{"missing token", e.args("--bot-token-file", filepath.Join(e.dir, "none")), "--bot-token-file"},
		{"plain installer", e.args("--installer", "http://example.com/install.sh"), "https"},
		{"key without server", e.args("--license-key-file", filepath.Join(e.dir, "lic.key")), "--license-server"},
		{"server without key", e.args("--license-server", "https://license.example.com"), "--license-key-file"},
		{"host as an option", e.args("--host", "-oProxyCommand=x"), "--host"},
		{"bad license id", e.args("--license-id", "nope"), "license_id"},
	}
	for _, c := range cases {
		e.errOut.Reset()
		e.srv.scripts = nil
		if code := e.a.run(c.args); code != 2 || !strings.Contains(e.errOut.String(), c.want) {
			t.Errorf("%s: %d %q", c.name, code, e.errOut)
		}
		if len(e.srv.scripts) != 0 {
			t.Errorf("%s: reached the server", c.name)
		}
	}
	e.errOut.Reset()
	if code := e.a.run(e.args("--bot-token-file", loose)); code != 0 || !strings.Contains(e.errOut.String(), "chmod 600") {
		t.Errorf("a token file others can read: %d %q", code, e.errOut)
	}
}

func TestSSHArgs(t *testing.T) {
	root := sshRunner{host: "root@203.0.113.7", port: "2222", identity: "/k"}.args()
	if got := strings.Join(root, " "); !strings.Contains(got, "BatchMode=yes") || !strings.Contains(got, "-p 2222") ||
		!strings.Contains(got, "-i /k") || !strings.HasSuffix(got, "-- root@203.0.113.7 bash -s") {
		t.Fatalf("root: %s", got)
	}
	user := sshRunner{host: "ops@203.0.113.7", port: "22"}.args()
	if got := strings.Join(user, " "); !strings.HasSuffix(got, "-- ops@203.0.113.7 sudo -n bash -s") {
		t.Fatalf("user: %s", got)
	}
	for _, a := range append(root, user...) {
		if strings.Contains(a, testBotToken) {
			t.Fatal("a secret on the ssh command line")
		}
	}
}

func TestCheck(t *testing.T) {
	e := newTestEnv(t)
	if code := e.a.run([]string{"check", "--host", "root@203.0.113.7"}); code != 0 || !strings.Contains(e.out.String(), "reachable as root") {
		t.Fatalf("check: %d %s %s", code, e.out, e.errOut)
	}
	if e.host != "root@203.0.113.7" {
		t.Fatalf("host: %s", e.host)
	}
}

func TestQuoting(t *testing.T) {
	if got := sq("it's"); got != `'it'"'"'s'` {
		t.Fatalf("sq: %s", got)
	}
}
