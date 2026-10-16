package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/s3/s3test"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// Low-entropy stand-ins for S3 keys (never real ones).
var (
	testS3Access = strings.Repeat("A", 20)
	testS3Secret = strings.Repeat("s", 40)
)

// The install never offers the optional S3 upload (the summary says how to
// turn it on); with an endpoint from a flag, the bucket and keys that are
// missing are asked (the secret key without echo), and the backup service gets
// the internet.
func TestInstallAsksForS3(t *testing.T) {
	head := "panel.example.com\n4242\n" + goodBotToken + "\n\n" // the panel skipped

	dir := t.TempDir()
	ins, out, errOut := testInstaller(head+"Shop-Backups\n"+testS3Access+"\n"+testS3Secret+"\n", nil, nil)
	if code := ins.install([]string{"--dir", dir, "--no-start", "--s3-endpoint", "https://s3.ir-thr-at1.arvanstorage.ir/",
		"--s3-region", "ir-thr-at1", "--s3-prefix", "shop/daily"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	for k, want := range map[string]string{
		"BOBRES_BACKUP_S3_ENDPOINT": "https://s3.ir-thr-at1.arvanstorage.ir", "BOBRES_BACKUP_S3_BUCKET": "shop-backups",
		"BOBRES_BACKUP_S3_REGION": "ir-thr-at1", "BOBRES_BACKUP_S3_PREFIX": "shop/daily", "BOBRES_BACKUP_S3_ACCESS_KEY": testS3Access,
		"BOBRES_BACKUP_S3_SECRET_KEY": testS3Secret, "BOBRES_BACKUP_NETWORK": "egress", "BOBRES_BACKUP_S3_KEEP": "",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
	if !strings.Contains(out.String(), "S3 bucket name:") || strings.Contains(out.String(), testS3Secret) {
		t.Fatalf("prompts:\n%s", out)
	}

	// A re-run never asks, and keeps the settings.
	ins, out, errOut = testInstaller("https://other.example.com\n", nil, nil)
	if code := ins.install([]string{"--dir", dir, "--no-start"}); code != 0 {
		t.Fatalf("re-run: %d %s", code, errOut)
	}
	again, _, _ := readEnv(filepath.Join(dir, ".env"))
	if strings.Contains(out.String(), "S3 ") || again["BOBRES_BACKUP_S3_ENDPOINT"] != env["BOBRES_BACKUP_S3_ENDPOINT"] ||
		again["BOBRES_BACKUP_S3_SECRET_KEY"] != testS3Secret {
		t.Fatalf("re-run asked or lost the settings:\n%s", out)
	}

	// Without a flag it is never offered: no upload, no internet for the backup
	// service, and nothing asked beyond the three answers (and the panel Enter).
	dir = t.TempDir()
	ins, out, errOut = testInstaller(head, nil, nil)
	if code := ins.install([]string{"--dir", dir, "--no-start"}); code != 0 {
		t.Fatalf("skip: %d %s", code, errOut)
	}
	env, _, _ = readEnv(filepath.Join(dir, ".env"))
	if env["BOBRES_BACKUP_S3_ENDPOINT"] != "" || env["BOBRES_BACKUP_NETWORK"] != "" || strings.Contains(out.String(), "S3 endpoint") {
		t.Fatalf("skipped: %q %q\n%s", env["BOBRES_BACKUP_S3_ENDPOINT"], env["BOBRES_BACKUP_NETWORK"], out)
	}
}

// Flags give the plain settings and the environment the keys (no prompt);
// an endpoint without its keys cannot go on without a terminal; settings are
// checked as the backup service checks them.
func TestInstallS3Flags(t *testing.T) {
	s3flags := []string{"--s3-endpoint", "https://shop.s3.example.com", "--s3-bucket", "shop", "--s3-prefix", "daily"}
	keys := map[string]string{"BOBRES_TELEGRAM_BOT_TOKEN": goodBotToken, "BOBRES_XUI_TOKEN": "t",
		"BOBRES_BACKUP_S3_ACCESS_KEY": testS3Access, "BOBRES_BACKUP_S3_SECRET_KEY": testS3Secret}
	healthy := `[{"Service":"core","Health":"healthy"},{"Service":"payments","Health":"healthy"},{"Service":"provisioner","Health":"healthy"},` +
		`{"Service":"bot","Health":"healthy"},{"Service":"tls-agent","Health":"healthy"},{"Service":"backup","Health":"healthy"}]`

	dir := t.TempDir()
	ins, out, errOut := testInstaller("", keys, &fakeDocker{health: []string{healthy}})
	if code := ins.install(append(append(baseArgs(dir), s3flags...), "--yes")); code != 0 {
		t.Fatalf("flags: %d %s", code, errOut)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	if env["BOBRES_BACKUP_S3_ENDPOINT"] != "https://shop.s3.example.com" || env["BOBRES_BACKUP_S3_SECRET_KEY"] != testS3Secret ||
		env["BOBRES_BACKUP_NETWORK"] != "egress" {
		t.Fatalf("env %v", env)
	}
	if !strings.Contains(out.String(), "Each backup is also copied to s3://shop/daily/ (the newest 30 are kept there).") {
		t.Fatalf("summary:\n%s", out)
	}

	// The keys are needed, and --yes cannot ask for them.
	ins, _, errOut = testInstaller("", secrets, nil)
	if code := ins.install(append(append(baseArgs(t.TempDir()), s3flags...), "--yes", "--no-start")); code != 2 ||
		!strings.Contains(errOut.String(), "BOBRES_BACKUP_S3_ACCESS_KEY is required with the S3 endpoint") {
		t.Fatalf("no keys: %d %s", code, errOut)
	}
	// So is the bucket.
	ins, _, errOut = testInstaller("", keys, nil)
	if code := ins.install(append(baseArgs(t.TempDir()), "--s3-endpoint", "https://s3.example.com", "--yes", "--no-start")); code != 2 ||
		!strings.Contains(errOut.String(), "BOBRES_BACKUP_S3_BUCKET is required") {
		t.Fatalf("no bucket: %d %s", code, errOut)
	}
	// Plain http is refused before anything is written.
	dir = t.TempDir()
	ins, _, errOut = testInstaller("", keys, nil)
	code := ins.install(append(baseArgs(dir), "--s3-endpoint", "http://s3.example.com", "--s3-bucket", "shop", "--no-start"))
	if _, written, _ := readEnv(filepath.Join(dir, ".env")); code != 2 || written || !strings.Contains(errOut.String(), "must be an https URL") {
		t.Fatalf("http endpoint: %d %v %s", code, written, errOut)
	}
}

// An install on a version without the backup service is not asked.
func TestInstallWithoutBackupsDoesNotAskForS3(t *testing.T) {
	old := version.Version
	version.Version = "v1.4.0"
	defer func() { version.Version = old }()
	ins, out, errOut := testInstaller("panel.example.com\n4242\n"+goodBotToken+"\n\nhttps://s3.example.com\n", nil, nil)
	if code := ins.install([]string{"--dir", t.TempDir(), "--no-start", "--version", "0.0.9"}); code != 0 || strings.Contains(out.String(), "S3 endpoint") {
		t.Fatalf("%d %s\n%s", code, errOut, out)
	}
}

func setEnv(t *testing.T, dir string, kv map[string]string) {
	t.Helper()
	env, _, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range kv {
		env[k] = v
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The S3 upload's plain settings are bobres config settings (the backup
// service is recreated, and its network follows the endpoint); its keys are
// secrets it never shows.
func TestConfigS3Settings(t *testing.T) {
	dir := fullInstall(t, true)
	f := &fakeDB{ps: func() string { return healthyWithBackup }}
	o, out, errOut := dbOps("", f)
	set := func(k, v string) int {
		f.steps = nil
		out.Reset()
		errOut.Reset()
		return o.configCmd([]string{"set", k, v, "--dir", dir})
	}

	// Without keys the endpoint is refused, and nothing changes.
	if code := set("BOBRES_BACKUP_S3_ENDPOINT", "https://s3.example.com"); code != 1 ||
		!strings.Contains(errOut.String(), "BOBRES_BACKUP_S3_ACCESS_KEY is required with BOBRES_BACKUP_S3_ENDPOINT") || len(f.steps) != 0 {
		t.Fatalf("no keys: %d %q %s", code, f.steps, errOut)
	}

	setEnv(t, dir, map[string]string{"BOBRES_BACKUP_S3_ACCESS_KEY": testS3Access, "BOBRES_BACKUP_S3_SECRET_KEY": testS3Secret})
	if code := set("BOBRES_BACKUP_S3_BUCKET", "Shop-Backups"); code != 0 || strings.Join(f.steps, "|") != "up -d --no-deps backup" {
		t.Fatalf("bucket: %d %q %s", code, f.steps, errOut)
	}
	if code := set("BOBRES_BACKUP_S3_ENDPOINT", "https://s3.example.com"); code != 0 || strings.Join(f.steps, "|") != "up -d --no-deps backup" {
		t.Fatalf("endpoint: %d %q %s", code, f.steps, errOut)
	}
	env, _ := readEnvFile(t, dir)
	if env["BOBRES_BACKUP_S3_BUCKET"] != "shop-backups" || env["BOBRES_BACKUP_NETWORK"] != "egress" {
		t.Fatalf("bucket %q network %q", env["BOBRES_BACKUP_S3_BUCKET"], env["BOBRES_BACKUP_NETWORK"])
	}
	for _, c := range []struct{ k, v, want string }{
		{"BOBRES_BACKUP_S3_KEEP", "0", "BOBRES_BACKUP_S3_KEEP must be a number from 1 to 1000"},
		{"BOBRES_BACKUP_S3_ENDPOINT", "http://s3.example.com", "must be an https URL"},
		{"BOBRES_BACKUP_S3_BUCKET", "", "BOBRES_BACKUP_S3_BUCKET is required"},
		{"BOBRES_BACKUP_S3_REGION", "ir thr", "not a region name"},
		{"BOBRES_BACKUP_S3_SECRET_KEY", "x", "is a secret"},
		{"BOBRES_BACKUP_NETWORK", "public", "follows BOBRES_BACKUP_S3_ENDPOINT"},
	} {
		if code := set(c.k, c.v); code != 1 || !strings.Contains(errOut.String(), c.want) || len(f.steps) != 0 {
			t.Errorf("set %s %q: %d %s", c.k, c.v, code, errOut)
		}
	}
	if code := set("BOBRES_BACKUP_S3_KEEP", "60"); code != 0 {
		t.Fatalf("keep: %d %s", code, errOut)
	}

	errOut.Reset()
	if code := o.configCmd([]string{"get", "BOBRES_BACKUP_S3_SECRET_KEY", "--dir", dir}); code != 1 ||
		!strings.Contains(errOut.String(), "bobres install --s3-endpoint <url> asks for it") {
		t.Fatalf("get the secret: %d %s", code, errOut)
	}
	out.Reset()
	if code := o.configCmd([]string{"list", "--dir", dir}); code != 0 || strings.Contains(out.String(), testS3Secret) ||
		strings.Contains(out.String(), testS3Access) || !strings.Contains(out.String(), "BOBRES_BACKUP_S3_ENDPOINT      https://s3.example.com") ||
		!strings.Contains(out.String(), "(follows BOBRES_BACKUP_S3_ENDPOINT)") {
		t.Fatalf("list:\n%s", out)
	}

	// Off again: the backup service loses the internet.
	if code := set("BOBRES_BACKUP_S3_ENDPOINT", ""); code != 0 {
		t.Fatalf("off: %d %s", code, errOut)
	}
	if env, _ := readEnvFile(t, dir); env["BOBRES_BACKUP_NETWORK"] != "" {
		t.Fatalf("network %q", env["BOBRES_BACKUP_NETWORK"])
	}
}

func TestSupportBundleS3(t *testing.T) {
	for _, k := range []string{"BOBRES_BACKUP_S3_ENDPOINT", "BOBRES_BACKUP_S3_BUCKET", "BOBRES_BACKUP_S3_REGION", "BOBRES_BACKUP_S3_PREFIX",
		"BOBRES_BACKUP_S3_KEEP", "BOBRES_BACKUP_NETWORK"} {
		if !notSecretEnv[k] {
			t.Errorf("%s is scrubbed from the bundle", k)
		}
	}
	for _, k := range []string{"BOBRES_BACKUP_S3_ACCESS_KEY", "BOBRES_BACKUP_S3_SECRET_KEY"} {
		if notSecretEnv[k] {
			t.Errorf("%s is kept in the bundle", k)
		}
	}
	dir := t.TempDir()
	at := time.Date(2026, 10, 11, 3, 31, 0, 0, time.UTC)
	for name, sc := range map[string]backup.Sidecar{
		"a.json": {File: "bobres-a.bkp", UploadedAt: &at, UploadedTo: "s3://shop/bobres-a.bkp"},
		"b.json": {File: "bobres-b.bkp", UploadError: "the storage answered HTTP 403: AccessDenied"},
	} {
		if err := backup.WriteSidecar(filepath.Join(dir, name), sc); err != nil {
			t.Fatal(err)
		}
	}
	if got := sidecarText(filepath.Join(dir, "a.json")); !strings.Contains(got, "copied to s3://shop/bobres-a.bkp 2026-10-11T03:31:00Z") {
		t.Errorf("uploaded: %s", got)
	}
	if got := sidecarText(filepath.Join(dir, "b.json")); !strings.Contains(got, "s3: the storage answered HTTP 403: AccessDenied") {
		t.Errorf("failed: %s", got)
	}
}

// s3Install is restoreInstall with S3 settings for the fake f.
func s3Install(t *testing.T, f *s3test.Fake) string {
	t.Helper()
	dir := restoreInstall(t)
	setEnv(t, dir, map[string]string{"BOBRES_BACKUP_S3_ENDPOINT": f.URL(), "BOBRES_BACKUP_S3_BUCKET": f.Bucket,
		"BOBRES_BACKUP_S3_REGION": f.Region, "BOBRES_BACKUP_S3_ACCESS_KEY": f.AccessKey, "BOBRES_BACKUP_S3_SECRET_KEY": f.SecretKey})
	return dir
}

func downloadsLeft(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "backups", ".s3-restore-*"))
	return m
}

// bobres restore s3://bucket/key downloads the backup into backups/ with the
// install's S3 settings, restores it as a file, and removes the download.
func TestRestoreFromS3(t *testing.T) {
	f := s3test.New("bobres-backups")
	defer f.Server.Close()
	raw, err := os.ReadFile(writeBackup(t, installKey, backupKeys(), "DUMP FROM THE BUCKET"))
	if err != nil {
		t.Fatal(err)
	}
	f.Put("shop/bobres-20261010T033000Z.bkp", raw, time.Now())
	dir := s3Install(t, f)
	db := &fakeDB{dump: "DUMP OF THE CURRENT DATA"}
	o, out, errOut := dbOps("", db)
	o.s3c = f.Client(false)
	src := "s3://bobres-backups/shop/bobres-20261010T033000Z.bkp"
	if code := o.restoreCmd([]string{src, "--dir", dir, "--yes"}); code != 0 {
		t.Fatalf("restore: %d\n%s\n%s", code, out, errOut)
	}
	if len(db.restored) != 1 || db.restored[0] != "DUMP FROM THE BUCKET" {
		t.Fatalf("restored %q", db.restored)
	}
	for _, w := range []string{"Downloading " + src, "Backup " + src + " (checked", "the data of bobres-20261010T033000Z.bkp"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output has no %q:\n%s", w, out)
		}
	}
	if left := downloadsLeft(t, dir); len(left) > 0 {
		t.Fatalf("download left: %v", left)
	}
	if bad := f.Refused(); len(bad) > 0 {
		t.Fatalf("signatures refused: %v", bad)
	}
	if strings.Contains(out.String()+errOut.String(), f.SecretKey) {
		t.Fatal("the output shows the secret key")
	}
}

// The downloaded backup is checked and restored through one open file: the
// backup service's user, who owns backups/, moving the download folder away
// and putting another valid backup in its place changes nothing.
func TestRestoreFromS3ReadsTheCheckedFile(t *testing.T) {
	f := s3test.New("bobres-backups")
	defer f.Server.Close()
	raw, err := os.ReadFile(writeBackup(t, installKey, backupKeys(), "DUMP FROM THE BUCKET"))
	if err != nil {
		t.Fatal(err)
	}
	f.Put("bobres-20261010T033000Z.bkp", raw, time.Now())
	planted, err := os.ReadFile(writeBackup(t, installKey, installMasterKeys, "AN OLDER BACKUP"))
	if err != nil {
		t.Fatal(err)
	}
	dir := s3Install(t, f)
	db := &fakeDB{dump: "CURRENT"}
	o, out, errOut := dbOps("", db)
	o.s3c = f.Client(false)
	o.in = &swapOnRead{Reader: strings.NewReader("y\n"), swap: func() {
		left := downloadsLeft(t, dir)
		if len(left) != 1 {
			t.Errorf("downloads %v", left)
			return
		}
		if err := os.Rename(left[0], filepath.Join(dir, "backups", "moved-away")); err != nil {
			t.Error(err)
		}
		if err := os.Mkdir(left[0], 0o700); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(filepath.Join(left[0], "bobres-20261010T033000Z.bkp"), planted, 0o600); err != nil { //nolint:gosec // the test's own folder
			t.Error(err)
		}
	}}
	if code := o.restoreCmd([]string{"s3://bobres-backups/bobres-20261010T033000Z.bkp", "--dir", dir}); code != 0 {
		t.Fatalf("restore: %d\n%s\n%s", code, out, errOut)
	}
	if len(db.restored) != 1 || db.restored[0] != "DUMP FROM THE BUCKET" {
		t.Fatalf("restored %q, want the downloaded backup's dump", db.restored)
	}
}

func TestRestoreFromS3Refusals(t *testing.T) {
	f := s3test.New("bobres-backups")
	defer f.Server.Close()
	raw, _ := os.ReadFile(writeBackup(t, otherKey, backupKeys(), "DUMP"))
	f.Put("bobres-20261010T033000Z.bkp", raw, time.Now())
	for _, c := range []struct {
		name, src string
		noS3      bool
		want      string
	}{
		{"no settings", "s3://bobres-backups/bobres-20261010T033000Z.bkp", true, "this install has no S3 settings"},
		{"missing", "s3://bobres-backups/bobres-20261009T033000Z.bkp", false, "there is no such backup in the bucket"},
		{"bad bucket", "s3://Bad_Bucket/bobres-20261010T033000Z.bkp", false, "is not a bucket name"},
		{"no key", "s3://bobres-backups", false, "no object key"},
		{"wrong backup key", "s3://bobres-backups/bobres-20261010T033000Z.bkp", false, "wrong backup key"},
	} {
		dir := restoreInstall(t)
		if !c.noS3 {
			dir = s3Install(t, f)
		}
		db := &fakeDB{}
		o, _, errOut := dbOps("", db)
		o.s3c = f.Client(false)
		if code := o.restoreCmd([]string{c.src, "--dir", dir, "--yes"}); code != 1 || !strings.Contains(errOut.String(), c.want) ||
			!strings.Contains(errOut.String(), "Nothing was changed") || len(db.steps) != 0 {
			t.Errorf("%s: %d %q %s", c.name, code, db.steps, errOut)
		}
		if left := downloadsLeft(t, dir); len(left) > 0 {
			t.Errorf("%s: download left: %v", c.name, left)
		}
	}
	// The key file of the other server opens it.
	dir := s3Install(t, f)
	keyFile := filepath.Join(t.TempDir(), "key")
	_ = os.WriteFile(keyFile, []byte(otherKey+"\n"), 0o600)
	db := &fakeDB{}
	o, out, errOut := dbOps("", db)
	o.s3c = f.Client(false)
	if code := o.restoreCmd([]string{"s3://bobres-backups/bobres-20261010T033000Z.bkp", "--dir", dir, "--yes", "--key-file", keyFile}); code != 0 ||
		!bytes.Equal([]byte(db.restored[0]), []byte("DUMP")) {
		t.Fatalf("with the key file: %d\n%s\n%s", code, out, errOut)
	}
}

func TestParseS3Source(t *testing.T) {
	b, k, err := parseS3Source("s3://shop/daily/bobres-20261010T033000Z.bkp")
	if err != nil || b != "shop" || k != "daily/bobres-20261010T033000Z.bkp" {
		t.Fatalf("%q %q %v", b, k, err)
	}
	for _, bad := range []string{"s3://", "s3://shop/", "s3://shop/daily/", "s3://a/k"} {
		if _, _, err := parseS3Source(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
