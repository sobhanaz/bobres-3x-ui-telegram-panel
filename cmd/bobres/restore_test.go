package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
)

// Test values only: none of them is a real secret.
const (
	installKey = "install-backup-key-for-tests-only-0001"
	otherKey   = "another-server-backup-key-tests-only-02"
)

var opsNow = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// installMasterKeys are the master keys in the test install's .env.
var installMasterKeys = map[string]string{
	"BOBRES_CORE_MASTER_KEY":        "install-core-master-key-tests-only",
	"BOBRES_PROVISIONER_MASTER_KEY": "shared-provisioner-key-tests-only",
	"BOBRES_PAYMENTS_MASTER_KEY":    "install-payments-key-tests-only",
}

// fakeDB plays the Docker side of restore, update and rollback: compose
// commands are recorded in order, pg_dump writes dump, and the restore
// reads what it is given.
type fakeDB struct {
	steps      []string // dump, restore, and every compose command but ps
	dump       string
	dumpErr    error
	restored   []string
	restoreErr error
	stopErr    error
	// ps is the `compose ps` output; healthyPS when nil.
	ps func() string
	// schema is what versionsScript prints (defaultSchema when ""), or
	// schemaErr its failure.
	schema    string
	schemaErr error
}

// defaultSchema is the test database's migration versions.
const defaultSchema = "core 3\npayments 2\nprovisioner 4\n"

func (f *fakeDB) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	i := len(composeArgs())
	if len(args) < i {
		return nil, fmt.Errorf("not a compose command: %v", args)
	}
	cmd := strings.Join(args[i:], " ")
	switch args[i] {
	case "ps":
		if f.ps != nil {
			return []byte(f.ps()), nil
		}
		return []byte(healthyPS), nil
	case "stop":
		f.steps = append(f.steps, cmd)
		return nil, f.stopErr
	}
	f.steps = append(f.steps, cmd)
	return nil, nil
}

func (f *fakeDB) pipe(_ context.Context, _ string, in io.Reader, w io.Writer, args ...string) error {
	if got := strings.Join(args[len(composeArgs()):len(args)-1], " "); got != "exec -T postgres sh -c" {
		return fmt.Errorf("unexpected command %q", got)
	}
	switch args[len(args)-1] {
	case dumpScript:
		f.steps = append(f.steps, "dump")
		if f.dumpErr != nil {
			return f.dumpErr
		}
		_, err := io.WriteString(w, f.dump)
		return err
	case restoreScript:
		f.steps = append(f.steps, "restore")
		b, err := io.ReadAll(in)
		if err != nil {
			return err
		}
		f.restored = append(f.restored, string(b))
		return f.restoreErr
	case versionsScript: // a read: not a step
		if f.schemaErr != nil {
			return f.schemaErr
		}
		out := f.schema
		if out == "" {
			out = defaultSchema
		}
		_, err := io.WriteString(w, out)
		return err
	}
	return fmt.Errorf("unexpected script %q", args[len(args)-1])
}

func dbOps(stdin string, f *fakeDB) (*ops, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &ops{
		in: strings.NewReader(stdin), out: &out, errOut: &errOut,
		run: f.run, pipe: f.pipe,
		stream: func(context.Context, string, ...string) error { return errors.New("unexpected stream") },
		freeGB: func(string) float64 { return 42 }, sleep: func(time.Duration) {},
		now: func() time.Time { return opsNow },
	}, &out, &errOut
}

// restoreInstall is an install directory with a backup key and master keys.
func restoreInstall(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{
		"BOBRES_DOMAIN": "panel.example.com", "BOBRES_VERSION": "1.2.3", "BOBRES_BACKUP_KEY": installKey,
		"OPERATOR_NOTE": "keep me",
	}
	for k, v := range installMasterKeys {
		env[k] = v
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"docker-compose.yml": "compose of 1.2.3\n", "Caddyfile": "caddy 1.2.3\n", "postgres-init.sh": "init\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// writeBackup writes a backup file made with key, carrying keys and dump.
func writeBackup(t *testing.T, key string, keys map[string]string, dump string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bobres-20261010T033000Z.bkp")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := backup.Write(f, []byte(key), keys, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// backupKeys are a backup's master keys: two differ from the install's.
func backupKeys() map[string]string {
	return map[string]string{
		"BOBRES_CORE_MASTER_KEY":        "backup-core-master-key-tests-only",
		"BOBRES_PROVISIONER_MASTER_KEY": installMasterKeys["BOBRES_PROVISIONER_MASTER_KEY"],
		"BOBRES_PAYMENTS_MASTER_KEY":    "backup-payments-key-tests-only",
	}
}

// openBackup decrypts a backup file: its keys and dump.
func openBackup(t *testing.T, path, key string) (map[string]string, string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := backup.Open(f, []byte(key))
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	b, err := io.ReadAll(r.Dump())
	if err != nil {
		t.Fatal(err)
	}
	return r.Keys(), string(b)
}

func TestRestore(t *testing.T) {
	dir := restoreInstall(t)
	file := writeBackup(t, installKey, backupKeys(), "DUMP OF THE BACKUP")
	f := &fakeDB{dump: "DUMP OF THE CURRENT DATA"}
	o, out, errOut := dbOps("", f)
	if code := o.restoreCmd([]string{"--dir", dir, "--yes", file}); code != 0 {
		t.Fatalf("restore: %d\n%s\n%s", code, out, errOut)
	}
	// The services stop before the safety backup: nothing is written after it.
	want := []string{"stop core payments provisioner bot", "dump", "restore", "up -d"}
	if !reflect.DeepEqual(f.steps, want) {
		t.Fatalf("steps %q, want %q", f.steps, want)
	}
	if len(f.restored) != 1 || f.restored[0] != "DUMP OF THE BACKUP" {
		t.Fatalf("restored %q", f.restored)
	}

	// The safety backup holds the current data and this install's keys. Its
	// name keeps it out of the backup service's retention of 7.
	name := "bobres-20261011T120000Z-before-restore.bkp"
	if got := safetyName(opsNow, "before-restore"); got != name {
		t.Fatalf("safety backup name %q", got)
	}
	if del := backup.Prune([]string{name, backup.SidecarName(name)}, 0); len(del) != 0 {
		t.Fatalf("retention would delete the safety backup: %v", del)
	}
	keys, dump := openBackup(t, filepath.Join(dir, "backups", name), installKey)
	if dump != "DUMP OF THE CURRENT DATA" || !reflect.DeepEqual(keys, installMasterKeys) {
		t.Fatalf("safety backup: %q %v", dump, keys)
	}
	sc, err := backup.ReadSidecar(filepath.Join(dir, "backups", backup.SidecarName(name)))
	if err != nil || sc.File != name || sc.Size == 0 || len(sc.SHA256) != 64 || !sc.CreatedAt.Equal(opsNow) || sc.SentAt != nil ||
		sc.TelegramError != "made by bobres restore before it replaced the data; kept on this server only" {
		t.Fatalf("sidecar: %+v %v", sc, err)
	}
	if tmps, _ := filepath.Glob(filepath.Join(dir, "backups", ".*tmp*")); len(tmps) > 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
	for _, p := range []string{name, backup.SidecarName(name)} {
		if st, err := os.Lstat(filepath.Join(dir, "backups", p)); err != nil || st.Mode().Perm() != 0o600 || !st.Mode().IsRegular() {
			t.Fatalf("%s: %v %v", p, st, err)
		}
	}

	// The differing master keys are the backup's now; the rest of .env stays.
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	for k, v := range backupKeys() {
		if env[k] != v {
			t.Errorf("%s = %q, want the backup's", k, env[k])
		}
	}
	if env["OPERATOR_NOTE"] != "keep me" || env["BOBRES_BACKUP_KEY"] != installKey || env["BOBRES_DOMAIN"] != "panel.example.com" {
		t.Errorf(".env lost settings: %v", env)
	}
	if st, _ := os.Stat(filepath.Join(dir, ".env")); st.Mode().Perm() != 0o600 {
		t.Errorf(".env mode %v", st.Mode())
	}

	text := out.String()
	for _, w := range []string{"made:        2026-", "master keys: BOBRES_CORE_MASTER_KEY, BOBRES_PAYMENTS_MASTER_KEY, BOBRES_PROVISIONER_MASTER_KEY",
		"replaced in .env with the backup's: BOBRES_CORE_MASTER_KEY, BOBRES_PAYMENTS_MASTER_KEY\n", name, "Restore complete"} {
		if !strings.Contains(text, w) {
			t.Errorf("output missing %q:\n%s", w, text)
		}
	}
	for _, secret := range []string{installKey, "backup-core-master-key", "install-core-master-key"} {
		if strings.Contains(text+errOut.String(), secret) {
			t.Errorf("output shows a secret (%s)", secret)
		}
	}
}

// The backup service's user controls backups/: a link it plants where the
// safety backup or its record go is replaced, never followed, and nothing
// is changed or handed over by path there.
func TestSafetyBackupIgnoresLinks(t *testing.T) {
	dir := restoreInstall(t)
	victim := filepath.Join(t.TempDir(), "sudoers")
	if err := os.WriteFile(victim, []byte("root ALL=(ALL) ALL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bdir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := safetyName(opsNow, "before-restore")
	for _, n := range []string{".missing", backup.SidecarName(name)} {
		target := victim
		if n == ".missing" {
			n, target = name, filepath.Join(t.TempDir(), "nothing-here") // dangling: exists() says no
		}
		if err := os.Symlink(target, filepath.Join(bdir, n)); err != nil {
			t.Fatal(err)
		}
	}
	o, _, errOut := dbOps("", &fakeDB{dump: "CURRENT"})
	got, err := o.safetyBackup(context.Background(), dir, map[string]string{"BOBRES_BACKUP_KEY": installKey}, "before-restore", "test")
	if err != nil || got != name {
		t.Fatalf("safety backup %q %v %s", got, err, errOut)
	}
	for _, n := range []string{name, backup.SidecarName(name)} {
		if st, err := os.Lstat(filepath.Join(bdir, n)); err != nil || !st.Mode().IsRegular() {
			t.Fatalf("%s is not a plain file: %v %v", n, st, err)
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != "root ALL=(ALL) ALL\n" {
		t.Fatalf("the link's target was written: %q", b)
	}
}

func TestRestoreAsks(t *testing.T) {
	dir := restoreInstall(t)
	file := writeBackup(t, installKey, backupKeys(), "DUMP")
	f := &fakeDB{dump: "CURRENT"}
	o, out, _ := dbOps("n\n", f)
	if code := o.restoreCmd([]string{file, "--dir", dir}); code != 1 || len(f.steps) != 0 || !strings.Contains(out.String(), "REPLACES all current data of panel.example.com") {
		t.Fatalf("not confirmed: %d %q\n%s", code, f.steps, out)
	}
	o, out, _ = dbOps("y\n", f)
	if code := o.restoreCmd([]string{file, "--dir", dir}); code != 0 || len(f.restored) != 1 {
		t.Fatalf("confirmed: %d %q\n%s", code, f.steps, out)
	}
}

// A backup from another server opens with that server's key from a file;
// the safety backup still uses this install's key.
func TestRestoreKeyFile(t *testing.T) {
	dir := restoreInstall(t)
	file := writeBackup(t, otherKey, backupKeys(), "DUMP FROM THE OLD SERVER")
	keyFile := filepath.Join(t.TempDir(), "key.txt")
	if err := os.WriteFile(keyFile, []byte("\nBOBRES_BACKUP_KEY="+otherKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeDB{dump: "CURRENT"}
	o, out, errOut := dbOps("", f)
	// Flags after the file, as the CI drill and the runbook write it.
	if code := o.restoreCmd([]string{file, "--dir", dir, "--key-file", keyFile, "--yes"}); code != 0 {
		t.Fatalf("restore: %d\n%s\n%s", code, out, errOut)
	}
	if f.restored[0] != "DUMP FROM THE OLD SERVER" {
		t.Fatalf("restored %q", f.restored)
	}
	if _, dump := openBackup(t, filepath.Join(dir, "backups", safetyName(opsNow, "before-restore")), installKey); dump != "CURRENT" {
		t.Fatalf("safety backup: %q", dump)
	}
}

func TestRestoreKeepKeys(t *testing.T) {
	dir := restoreInstall(t)
	before, _ := os.ReadFile(filepath.Join(dir, ".env"))
	file := writeBackup(t, installKey, backupKeys(), "DUMP")
	o, out, errOut := dbOps("", &fakeDB{dump: "CURRENT"})
	if code := o.restoreCmd([]string{file, "--dir", dir, "--yes", "--keep-keys"}); code != 0 {
		t.Fatalf("restore: %d\n%s\n%s", code, out, errOut)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".env")); !bytes.Equal(before, after) {
		t.Fatalf(".env changed with --keep-keys:\n%s", after)
	}
	if !strings.Contains(out.String(), "differ (BOBRES_CORE_MASTER_KEY, BOBRES_PAYMENTS_MASTER_KEY) but stay") {
		t.Fatalf("output:\n%s", out)
	}
}

// Every refusal happens before anything is touched: no dump, no stop.
func TestRestoreRefusals(t *testing.T) {
	good := writeBackup(t, installKey, backupKeys(), "DUMP")
	damaged := filepath.Join(t.TempDir(), "damaged.bkp")
	b, _ := os.ReadFile(good)
	b[len(b)-5] ^= 0x01
	_ = os.WriteFile(damaged, b, 0o600) //nolint:gosec // the test's own file
	notBackup := filepath.Join(t.TempDir(), "notes.txt")
	_ = os.WriteFile(notBackup, []byte("just some text, not a backup at all, long enough for a header?"), 0o600)
	keyFile := func(s string) string {
		p := filepath.Join(t.TempDir(), "key")
		_ = os.WriteFile(p, []byte(s), 0o600)
		return p
	}

	for _, c := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"wrong key", []string{good, "--key-file", keyFile(otherKey)}, 1, "wrong backup key"},
		{"short key", []string{good, "--key-file", keyFile("short")}, 1, "too short"},
		{"two lines", []string{good, "--key-file", keyFile(otherKey + "\n" + installKey)}, 1, "on one line"},
		{"missing key file", []string{good, "--key-file", "/nonexistent/key"}, 1, "read the key file"},
		{"damaged", []string{damaged}, 1, "damaged or incomplete"},
		{"not a backup", []string{notBackup}, 1, "not a BOBRES backup"},
		{"missing file", []string{"/nonexistent/x.bkp"}, 1, "no such file"},
		{"no file", nil, 2, "usage: bobres restore"},
		{"two files", []string{good, good}, 2, "usage: bobres restore"},
		{"bad flag", []string{good, "--nope"}, 2, "flag provided but not defined"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := restoreInstall(t)
			f := &fakeDB{dump: "CURRENT"}
			o, _, errOut := dbOps("y\n", f)
			code := o.restoreCmd(append(c.args, "--dir", dir, "--yes"))
			if code != c.code || !strings.Contains(errOut.String(), c.want) || len(f.steps) != 0 {
				t.Fatalf("got %d %q, steps %q", code, errOut, f.steps)
			}
			if entries, _ := os.ReadDir(filepath.Join(dir, "backups")); len(entries) != 0 {
				t.Fatalf("backups written: %v", entries)
			}
		})
	}

	o, _, errOut := dbOps("", &fakeDB{})
	if code := o.restoreCmd([]string{good, "--dir", t.TempDir()}); code != 2 || !strings.Contains(errOut.String(), "no install found") {
		t.Fatalf("no install: %d %s", code, errOut)
	}

	// An install without a backup key cannot take the safety backup: it is
	// said before the file is read or the operator asked, with --key-file
	// too (that key opens the file, not this install's backups).
	dir := restoreInstall(t)
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	delete(env, "BOBRES_BACKUP_KEY")
	_ = os.WriteFile(filepath.Join(dir, ".env"), renderEnv(env), 0o600)
	other := writeBackup(t, otherKey, backupKeys(), "DUMP")
	kf := keyFile(otherKey)
	for _, args := range [][]string{{good, "--dir", dir, "--yes"}, {other, "--dir", dir, "--key-file", kf}} {
		f := &fakeDB{dump: "CURRENT"}
		o, out, errOut := dbOps("y\n", f)
		if code := o.restoreCmd(args); code != 1 || !strings.Contains(errOut.String(), "no backup key") ||
			!strings.Contains(errOut.String(), "Run `sudo bobres install --dir "+dir+"` once") || !strings.Contains(errOut.String(), "Nothing was changed") {
			t.Fatalf("no key %q: %d %s", args, code, errOut)
		}
		if len(f.steps) != 0 || strings.Contains(out.String(), "REPLACES") || strings.Contains(out.String(), "checked") {
			t.Fatalf("no key %q: went on: steps %q\n%s", args, f.steps, out)
		}
	}
}

// A safety backup that fails stops the restore: the database is not touched
// and the services start again.
func TestRestoreSafetyBackupFails(t *testing.T) {
	dir := restoreInstall(t)
	before, _ := os.ReadFile(filepath.Join(dir, ".env"))
	file := writeBackup(t, installKey, backupKeys(), "DUMP")
	f := &fakeDB{dumpErr: errors.New("exit status 1")}
	o, _, errOut := dbOps("", f)
	if code := o.restoreCmd([]string{file, "--dir", dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if want := []string{"stop core payments provisioner bot", "dump", "up -d"}; !reflect.DeepEqual(f.steps, want) ||
		!strings.Contains(errOut.String(), "backup of the current data failed") || !strings.Contains(errOut.String(), "Nothing was changed") {
		t.Fatalf("steps %q: %s", f.steps, errOut)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "backups")); len(entries) != 0 {
		t.Fatalf("left in backups: %v", entries)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".env")); !bytes.Equal(before, after) {
		t.Fatal(".env changed")
	}
}

// A restore that fails changes nothing: the services start again on the
// old data and keys.
func TestRestoreFailureStartsAgain(t *testing.T) {
	dir := restoreInstall(t)
	before, _ := os.ReadFile(filepath.Join(dir, ".env"))
	file := writeBackup(t, installKey, backupKeys(), "DUMP")
	f := &fakeDB{dump: "CURRENT", restoreErr: errors.New("exit status 3")}
	o, _, errOut := dbOps("", f)
	if code := o.restoreCmd([]string{file, "--dir", dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if want := []string{"stop core payments provisioner bot", "dump", "restore", "up -d"}; !reflect.DeepEqual(f.steps, want) {
		t.Fatalf("steps %q, want %q", f.steps, want)
	}
	if !strings.Contains(errOut.String(), "the restore failed") || !strings.Contains(errOut.String(), "was not changed") {
		t.Fatalf("output: %s", errOut)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".env")); !bytes.Equal(before, after) {
		t.Fatal(".env changed after a failed restore")
	}
}

func TestRestoreUnhealthy(t *testing.T) {
	dir := restoreInstall(t)
	file := writeBackup(t, installKey, backupKeys(), "DUMP")
	f := &fakeDB{dump: "CURRENT", ps: func() string { return `[{"Service":"core","State":"running","Health":"unhealthy"}]` }}
	o, _, errOut := dbOps("", f)
	if code := o.restoreCmd([]string{file, "--dir", dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, w := range []string{"not healthy after", "bobres status --dir", "To go back to the data from before: bobres restore " + filepath.Join(dir, "backups", safetyName(opsNow, "before-restore"))} {
		if !strings.Contains(errOut.String(), w) {
			t.Errorf("missing %q:\n%s", w, errOut)
		}
	}
}

func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		pos  []string
		yes  bool
	}{
		{[]string{"a.bkp"}, []string{"a.bkp"}, false},
		{[]string{"--yes", "a.bkp"}, []string{"a.bkp"}, true},
		{[]string{"a.bkp", "--yes"}, []string{"a.bkp"}, true},
		{[]string{"a.bkp", "--yes", "b"}, []string{"a.bkp", "b"}, true},
	} {
		fs := flag.NewFlagSet("restore", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		yes := fs.Bool("yes", false, "")
		pos, err := parseArgs(fs, c.args)
		if err != nil || !reflect.DeepEqual(pos, c.pos) || *yes != c.yes {
			t.Errorf("%q: %q %v %v", c.args, pos, *yes, err)
		}
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB"} {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}
