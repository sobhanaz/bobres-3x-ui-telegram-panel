package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

// The master keys of the test install, as the installer makes them (32 random bytes, hex).
var rotateOldKeys = map[string]string{
	"BOBRES_CORE_MASTER_KEY":        strings.Repeat("c0", 32),
	"BOBRES_PROVISIONER_MASTER_KEY": strings.Repeat("a1", 32),
	"BOBRES_PAYMENTS_MASTER_KEY":    strings.Repeat("b2", 32),
}

func serviceNamed(name string) (keySvc, bool) {
	for _, k := range keyServices {
		if k.service == name {
			return k, true
		}
	}
	return keySvc{}, false
}

// keyWorld plays the Docker side of a rotation and keeps a model of the database: for each service, the key its encrypted
// values are sealed with. The rekey command of a service reads the keys from .env as the container would, and moves the model
// the way rekey.Run moves the data: it reads the current key and the previous one, and seals with the current one.
type keyWorld struct {
	t       *testing.T
	dir     string
	fake    *fakeDB
	db      map[string]string
	running map[string]keyPair
	rekeys  []string
	// failRekey, when set, may fail a rekey run before it does anything.
	failRekey func(service string, args []string) error
}

type keyPair struct{ cur, prev string }

func (w *keyWorld) readEnv() map[string]string {
	w.t.Helper()
	env, _, err := readEnv(filepath.Join(w.dir, ".env"))
	if err != nil {
		w.t.Fatal(err)
	}
	return env
}

// assertReadable: every running service can read the data with the keys it was started with.
func (w *keyWorld) assertReadable() {
	w.t.Helper()
	for svc, k := range w.running {
		if have := w.db[svc]; have != k.cur && (k.prev == "" || have != k.prev) {
			w.t.Errorf("%s runs with keys that do not read its data", svc)
		}
	}
}

func (w *keyWorld) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	i := len(composeArgs())
	switch args[i] {
	case "stop":
		for _, s := range args[i+1:] {
			delete(w.running, s)
		}
	case "up":
		env := w.readEnv()
		for _, s := range args[i+1:] {
			if k, ok := serviceNamed(s); ok {
				w.running[s] = keyPair{env[k.master], env[k.prev]}
			}
		}
	}
	out, err := w.fake.run(ctx, dir, args...)
	w.assertReadable()
	return out, err
}

func (w *keyWorld) stream(_ context.Context, dir string, args ...string) error {
	w.t.Helper()
	i := len(composeArgs())
	if len(args) < i+6 || strings.Join(args[i:i+4], " ") != "run --rm --no-deps -T" || args[i+5] != "rekey" {
		w.t.Fatalf("unexpected stream %v", args[i:])
	}
	svc, extra := args[i+4], args[i+6:]
	dry := len(extra) == 1 && extra[0] == "--dry-run"
	w.rekeys = append(w.rekeys, strings.TrimSpace(svc+" "+strings.Join(extra, " ")))
	if !dry && len(w.running) > 0 {
		w.t.Errorf("services were running while %s sealed its values again: %v", svc, w.running)
	}
	if w.failRekey != nil {
		if err := w.failRekey(svc, extra); err != nil {
			return err
		}
	}
	env := w.readEnv()
	k, _ := serviceNamed(svc)
	cur, prev, have := env[k.master], env[k.prev], w.db[svc]
	switch {
	case have == cur:
	case prev != "" && have == prev:
		if !dry {
			w.db[svc] = cur
		}
	default:
		return fmt.Errorf("%s: a value is not readable with the keys it has", svc)
	}
	w.assertReadable()
	return nil
}

// rotateInstall is a test install with hex master keys, the backup service on, and a world around it.
func rotateInstall(t *testing.T) (*keyWorld, *rotator, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := fullInstall(t, true)
	env, _, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range rotateOldKeys {
		env[k] = v
	}
	if err := writeEnv(filepath.Join(dir, ".env"), env); err != nil {
		t.Fatal(err)
	}
	w := &keyWorld{t: t, dir: dir, db: map[string]string{}, running: map[string]keyPair{},
		fake: &fakeDB{dump: "DUMP BEFORE THE ROTATION", ps: func() string { return healthyWithBackup }}}
	for _, k := range keyServices {
		w.db[k.service] = env[k.master]
		w.running[k.service] = keyPair{cur: env[k.master]}
	}
	o, out, errOut := dbOps("y\n", w.fake)
	o.run, o.stream = w.run, w.stream
	n := 0
	r := &rotator{ops: o, random: func(size int) string {
		n++
		sum := sha256.Sum256([]byte(fmt.Sprintf("new key %d", n)))
		return hex.EncodeToString(sum[:size])
	}}
	return w, r, out, errOut
}

func rotateKey(r *rotator, w *keyWorld, args ...string) int {
	return r.rotateKeyCmd(append(args, "--dir", w.dir))
}

// requireNoKeys fails when an output shows a key.
func requireNoKeys(t *testing.T, w *keyWorld, env map[string]string, outputs ...fmt.Stringer) {
	t.Helper()
	for _, o := range outputs {
		for name, v := range env {
			if (strings.Contains(name, "MASTER_KEY") || strings.HasSuffix(name, "TAG_KEY") || name == "BOBRES_BACKUP_KEY") && v != "" && strings.Contains(o.String(), v) {
				t.Fatalf("the output shows %s:\n%s", name, o)
			}
		}
		for _, v := range rotateOldKeys {
			if strings.Contains(o.String(), v) {
				t.Fatalf("the output shows an old key:\n%s", o)
			}
		}
	}
}

func TestRotateKeyFull(t *testing.T) {
	w, r, out, errOut := rotateInstall(t)
	before, beforeRaw := readEnvFile(t, w.dir)
	if code := rotateKey(r, w); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	env, raw := readEnvFile(t, w.dir)
	for _, k := range keyServices {
		old, fresh := before[k.master], env[k.master]
		if fresh == old || len(fresh) != 64 {
			t.Errorf("%s: %q", k.master, fresh)
		}
		if _, err := hex.DecodeString(fresh); err != nil {
			t.Errorf("%s is not hex: %v", k.master, err)
		}
		if env[k.prev] != "" {
			t.Errorf("%s is still set after the rotation", k.prev)
		}
		// The tag key is what the OLD master key made: identifiers made with Tag do not change.
		wantTag, _ := crypto.TagKeyHex([]byte(old))
		if env[k.tag] != wantTag {
			t.Errorf("%s = %q, want the tag key of the old master key", k.tag, env[k.tag])
		}
		// The data is sealed with the new key, in the model.
		if w.db[k.service] != fresh {
			t.Errorf("%s: the data is sealed with %q, not the new key", k.service, w.db[k.service])
		}
		// The old key stays as a comment, never as a setting.
		if !strings.Contains(raw, "# "+k.prev+"="+old+"\n") {
			t.Errorf("the old %s is not kept as a comment", k.service)
		}
		if running := w.running[k.service]; running.cur != fresh || running.prev != "" {
			t.Errorf("%s runs with %+v", k.service, running)
		}
	}
	// Nothing else changed, and the backup key least of all.
	for k, v := range before {
		if strings.Contains(k, "MASTER_KEY") {
			continue
		}
		if env[k] != v {
			t.Errorf("%s changed", k)
		}
	}
	if env["BOBRES_BACKUP_KEY"] != installKey {
		t.Error("the backup key changed")
	}
	// The retired keys are comments: nothing reads them.
	if parsed := parseEnv([]byte(raw)); parsed["BOBRES_CORE_MASTER_KEY_PREVIOUS"] != "" {
		t.Error("a retired key is a setting")
	}
	if fi, err := os.Stat(filepath.Join(w.dir, ".env")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf(".env mode: %v %v", fi, err)
	}
	if exists(filepath.Join(w.dir, ".env.before-key-rotation")) {
		t.Error("the copy of .env was left behind")
	}
	// The backup before it exists, reads back, and holds the OLD keys.
	matches, _ := filepath.Glob(filepath.Join(w.dir, "backups", "*-before-key-rotation.bkp"))
	if len(matches) != 1 {
		t.Fatalf("backups: %v", matches)
	}
	sum, err := verifyBackupFile(matches[0], []byte(installKey))
	if err != nil || len(sum.KeyNames) != 3 {
		t.Fatalf("the backup: %+v %v", sum, err)
	}
	// The order: backup, stop, then the services in the order they depend on each other.
	if got := strings.Join(w.fake.steps, "|"); got != "dump|stop core payments provisioner bot backup|up -d --no-deps provisioner payments|up -d --no-deps core|up -d --no-deps bot backup" {
		t.Fatalf("steps: %q", got)
	}
	if got := strings.Join(w.rekeys, "|"); got != "provisioner --dry-run|payments --dry-run|core --dry-run|provisioner|payments|core" {
		t.Fatalf("rekeys: %q", got)
	}
	requireNoKeys(t, w, env, out, errOut)
	requireNoKeys(t, w, before, out, errOut)
	if !strings.Contains(out.String(), "Done: the master keys were replaced") || !strings.Contains(out.String(), "BOBRES_BACKUP_KEY did not change") {
		t.Fatalf("output: %s", out)
	}
	_ = beforeRaw
}

func TestRotateKeyDryRunChangesNothing(t *testing.T) {
	w, r, out, errOut := rotateInstall(t)
	_, before := readEnvFile(t, w.dir)
	if code := rotateKey(r, w, "--dry-run"); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if _, after := readEnvFile(t, w.dir); after != before {
		t.Fatalf(".env changed:\n%s", after)
	}
	if len(w.fake.steps) != 0 {
		t.Errorf("a dry run touched Docker: %q", w.fake.steps)
	}
	if got := strings.Join(w.rekeys, "|"); got != "provisioner --dry-run|payments --dry-run|core --dry-run" {
		t.Errorf("rekeys: %q", got)
	}
	if m, _ := filepath.Glob(filepath.Join(w.dir, "backups", "*")); len(m) != 0 {
		t.Errorf("a dry run made a backup: %v", m)
	}
	if exists(filepath.Join(w.dir, ".env.before-key-rotation")) {
		t.Error("a dry run kept a copy of .env")
	}
	for _, k := range keyServices {
		if w.db[k.service] != rotateOldKeys[k.master] {
			t.Errorf("%s: the data changed", k.service)
		}
	}
	if !strings.Contains(out.String(), "Dry run: nothing was changed") {
		t.Fatalf("output: %s", out)
	}
	// --dry-run with --forget-old is a mistake, and neither of them does anything with an argument.
	if code := rotateKey(r, w, "--dry-run", "--forget-old"); code != 2 {
		t.Errorf("--dry-run --forget-old: %d", code)
	}
	if code := rotateKey(r, w, "core"); code != 2 {
		t.Errorf("an argument: %d", code)
	}
}

func TestRotateKeyRefusesWhenAKeyDoesNotReadTheData(t *testing.T) {
	w, r, out, errOut := rotateInstall(t)
	// .env holds a core key that is not the one the data is sealed with (a wrong key put there by hand, say); the running
	// container still has the right one.
	env := w.readEnv()
	env["BOBRES_CORE_MASTER_KEY"] = strings.Repeat("ee", 32)
	if err := writeEnv(filepath.Join(w.dir, ".env"), env); err != nil {
		t.Fatal(err)
	}
	_, before := readEnvFile(t, w.dir)
	if code := rotateKey(r, w); code == 0 {
		t.Fatalf("exit 0\n%s", out)
	}
	if _, after := readEnvFile(t, w.dir); after != before {
		t.Fatal(".env changed")
	}
	if len(w.fake.steps) != 0 {
		t.Errorf("Docker was touched: %q", w.fake.steps)
	}
	if m, _ := filepath.Glob(filepath.Join(w.dir, "backups", "*")); len(m) != 0 {
		t.Errorf("a backup was made: %v", m)
	}
	if !strings.Contains(errOut.String(), "Nothing was changed") || !strings.Contains(errOut.String(), "core:") {
		t.Fatalf("errors: %s", errOut)
	}
	for _, k := range keyServices {
		if len(w.running) != 3 || w.running[k.service].cur != rotateOldKeys[k.master] {
			t.Errorf("the services were disturbed: %v", w.running)
		}
	}
}

func TestRotateKeyRefusals(t *testing.T) {
	// A store that is not healthy is not rotated.
	w, r, _, errOut := rotateInstall(t)
	w.fake.ps = func() string {
		return strings.Replace(healthyWithBackup, `"Service":"core","State":"running","Health":"healthy"`, `"Service":"core","State":"running","Health":"unhealthy"`, 1)
	}
	_, before := readEnvFile(t, w.dir)
	if code := rotateKey(r, w); code != 1 || !strings.Contains(errOut.String(), "core=unhealthy") || !strings.Contains(errOut.String(), "Nothing was changed") {
		t.Fatalf("unhealthy: %d %s", code, errOut)
	}
	if _, after := readEnvFile(t, w.dir); after != before || len(w.fake.steps) != 0 || len(w.rekeys) != 0 {
		t.Fatal("something changed")
	}
	// A dry run wants the same.
	if code := rotateKey(r, w, "--dry-run"); code != 1 {
		t.Fatalf("unhealthy dry run: %d", code)
	}

	// No core key: the installer makes it.
	w, r, _, errOut = rotateInstall(t)
	env := w.readEnv()
	delete(env, "BOBRES_CORE_MASTER_KEY")
	if err := writeEnv(filepath.Join(w.dir, ".env"), env); err != nil {
		t.Fatal(err)
	}
	if code := rotateKey(r, w); code != 1 || !strings.Contains(errOut.String(), "BOBRES_CORE_MASTER_KEY is not set") {
		t.Fatalf("no core key: %d %s", code, errOut)
	}

	// Another bobres command holds the install.
	w, r, _, errOut = rotateInstall(t)
	unlock, err := lockInstall(w.dir, opsNow)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if code := rotateKey(r, w); code != 1 || !strings.Contains(errOut.String(), "Nothing was changed") {
		t.Fatalf("locked: %d %s", code, errOut)
	}

	// Not confirmed.
	w, r, out, _ := rotateInstall(t)
	r.in = strings.NewReader("n\n")
	_, before = readEnvFile(t, w.dir)
	if code := rotateKey(r, w); code != 1 || !strings.Contains(out.String(), "Cancelled. Nothing was changed.") {
		t.Fatalf("declined: %d %s", code, out)
	}
	if _, after := readEnvFile(t, w.dir); after != before || len(w.fake.steps) != 0 {
		t.Fatal("something changed")
	}
}

func TestRotateKeyStopFailureChangesNothing(t *testing.T) {
	w, r, out, errOut := rotateInstall(t)
	// A copy left by some earlier run is not trusted: a fresh run writes its own, and removes it again.
	if err := os.WriteFile(filepath.Join(w.dir, ".env.before-key-rotation"), []byte("STALE=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.fake.stopErr = errors.New("the Docker daemon did not answer")
	_, before := readEnvFile(t, w.dir)
	if code := rotateKey(r, w); code != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if _, after := readEnvFile(t, w.dir); after != before {
		t.Fatalf(".env changed:\n%s", after)
	}
	if exists(filepath.Join(w.dir, ".env.before-key-rotation")) {
		t.Error("a copy of .env was left behind")
	}
	for _, k := range keyServices {
		if w.db[k.service] != rotateOldKeys[k.master] || w.running[k.service].cur != rotateOldKeys[k.master] {
			t.Errorf("%s was disturbed: %v %v", k.service, w.db[k.service], w.running[k.service])
		}
	}
	if !strings.Contains(errOut.String(), "Nothing was changed") {
		t.Fatalf("errors: %s", errOut)
	}
	if got := strings.Join(w.rekeys, "|"); got != "provisioner --dry-run|payments --dry-run|core --dry-run" {
		t.Errorf("rekeys: %q", got)
	}
}

func TestRotateKeyWithoutPayments(t *testing.T) {
	// An install from before payments had a key: the other two rotate, and payments is left out.
	w, r, out, errOut := rotateInstall(t)
	env := w.readEnv()
	delete(env, "BOBRES_PAYMENTS_MASTER_KEY")
	if err := writeEnv(filepath.Join(w.dir, ".env"), env); err != nil {
		t.Fatal(err)
	}
	delete(w.db, "payments")
	delete(w.running, "payments")
	if code := rotateKey(r, w, "--yes"); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if got := strings.Join(w.rekeys, "|"); got != "provisioner --dry-run|core --dry-run|provisioner|core" {
		t.Fatalf("rekeys: %q", got)
	}
	if after := w.readEnv(); after["BOBRES_PAYMENTS_MASTER_KEY"] != "" || after["BOBRES_PAYMENTS_TAG_KEY"] != "" {
		t.Error("payments got a key it did not have")
	}
}

// A failure at any point puts the old keys back: .env byte for byte, the data sealed with the old key again, the services
// running with it, the copy gone.
func TestRotateKeyFailureLeavesTheInstallAsItWas(t *testing.T) {
	for _, c := range []struct {
		name string
		fail func(w *keyWorld) // arms the failure
	}{
		{"payments cannot be re-encrypted", func(w *keyWorld) {
			w.failRekey = func(svc string, args []string) error {
				if svc == "payments" && len(args) == 0 && !w.reversing() {
					return errors.New("payments: the database refused")
				}
				return nil
			}
		}},
		{"the last service cannot be re-encrypted", func(w *keyWorld) {
			w.failRekey = func(svc string, args []string) error {
				if svc == "core" && len(args) == 0 && !w.reversing() {
					return errors.New("core: the database went away")
				}
				return nil
			}
		}},
		{"the services are not healthy with the new keys", func(w *keyWorld) {
			w.fake.ps = func() string {
				env := w.readEnv()
				if env["BOBRES_CORE_MASTER_KEY"] != rotateOldKeys["BOBRES_CORE_MASTER_KEY"] && env["BOBRES_CORE_MASTER_KEY_PREVIOUS"] == "" {
					return strings.Replace(healthyWithBackup, `"Service":"core","State":"running","Health":"healthy"`, `"Service":"core","State":"running","Health":"starting"`, 1)
				}
				return healthyWithBackup
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, r, out, errOut := rotateInstall(t)
			_, before := readEnvFile(t, w.dir)
			c.fail(w)
			if code := rotateKey(r, w); code != 1 {
				t.Fatalf("exit %d\n%s%s", code, out, errOut)
			}
			if _, after := readEnvFile(t, w.dir); after != before {
				t.Fatalf(".env is not as it was:\n%s", after)
			}
			for _, k := range keyServices {
				if w.db[k.service] != rotateOldKeys[k.master] {
					t.Errorf("%s: the data is sealed with %q, not the old key", k.service, w.db[k.service])
				}
				if got := w.running[k.service]; got.cur != rotateOldKeys[k.master] || got.prev != "" {
					t.Errorf("%s runs with %+v", k.service, got)
				}
			}
			if exists(filepath.Join(w.dir, ".env.before-key-rotation")) {
				t.Error("the copy of .env was left behind")
			}
			if !strings.Contains(errOut.String(), "nothing was rotated") {
				t.Fatalf("errors: %s", errOut)
			}
			requireNoKeys(t, w, w.readEnv(), out, errOut)
		})
	}
}

// reversing: the keys in .env are the old ones as the current key: the run that puts them back.
func (w *keyWorld) reversing() bool {
	return w.readEnv()["BOBRES_CORE_MASTER_KEY"] == rotateOldKeys["BOBRES_CORE_MASTER_KEY"]
}

// A rotation cut short, and one that cannot be put back either, leaves both keys in .env and the services running with
// them: every value is readable. Running the command again finishes it, with the same new keys.
func TestRotateKeyCutShortThenFinished(t *testing.T) {
	w, r, out, errOut := rotateInstall(t)
	// The second service fails going forward, and the first one cannot be put back going back: the data is left half on the new
	// key (provisioner) and half on the old one.
	w.failRekey = func(svc string, args []string) error {
		real := len(args) == 0
		switch {
		case real && svc == "payments" && !w.reversing():
			return errors.New("payments: the connection dropped")
		case real && svc == "provisioner" && w.reversing():
			return errors.New("provisioner: the connection dropped")
		}
		return nil
	}
	if code := rotateKey(r, w); code != 1 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	cut := w.readEnv()
	if len(w.running) != len(keyServices) {
		t.Fatalf("the services were not started again: %v", w.running)
	}
	for _, k := range keyServices {
		if cut[k.prev] != rotateOldKeys[k.master] || cut[k.master] == rotateOldKeys[k.master] || cut[k.master] == "" {
			t.Fatalf("%s: master %q previous %q", k.service, cut[k.master], cut[k.prev])
		}
		// The services run with both keys, and every value reads (assertReadable ran at every step).
		if got := w.running[k.service]; got.cur != cut[k.master] || got.prev != cut[k.prev] {
			t.Errorf("%s runs with %+v", k.service, got)
		}
	}
	if w.db["provisioner"] != cut["BOBRES_PROVISIONER_MASTER_KEY"] || w.db["payments"] != rotateOldKeys["BOBRES_PAYMENTS_MASTER_KEY"] {
		t.Errorf("the model: %v", w.db)
	}
	if !strings.Contains(errOut.String(), "run `sudo bobres rotate-key` again") {
		t.Fatalf("errors: %s", errOut)
	}
	// bobres check says so.
	if note := keyHealthNote(cut); !strings.Contains(note, "left unfinished") {
		t.Errorf("check note: %q", note)
	}
	if parsed := config.BackupKeyNames(); len(parsed) != 9 {
		t.Errorf("backup key names: %v", parsed)
	}
	// A backup made now carries the previous keys too.
	keys := masterKeys(cut)
	for _, k := range keyServices {
		if keys[k.prev] != rotateOldKeys[k.master] || keys[k.master] != cut[k.master] {
			t.Errorf("a backup made now would not carry %s and %s", k.master, k.prev)
		}
	}

	// Run it again: no new key, the same ones are finished.
	w.failRekey = nil
	w.rekeys = nil
	out.Reset()
	errOut.Reset()
	r.in = strings.NewReader("y\n")
	if code := rotateKey(r, w); code != 0 {
		t.Fatalf("second run: exit %d\n%s%s", code, out, errOut)
	}
	done := w.readEnv()
	for _, k := range keyServices {
		if done[k.master] != cut[k.master] {
			t.Errorf("%s: the second run made another key", k.master)
		}
		if done[k.prev] != "" {
			t.Errorf("%s still set", k.prev)
		}
		if w.db[k.service] != done[k.master] {
			t.Errorf("%s: the data is sealed with %q", k.service, w.db[k.service])
		}
		if got := w.running[k.service]; got.cur != done[k.master] || got.prev != "" {
			t.Errorf("%s runs with %+v", k.service, got)
		}
	}
	if note := keyHealthNote(done); note != "" {
		t.Errorf("check note after the rotation: %q", note)
	}
	if !strings.Contains(out.String(), "An earlier rotation was cut short") {
		t.Errorf("the run did not say it finishes an earlier one:\n%s", out)
	}
	_, raw := readEnvFile(t, w.dir)
	if !strings.Contains(raw, "# BOBRES_CORE_MASTER_KEY_PREVIOUS="+rotateOldKeys["BOBRES_CORE_MASTER_KEY"]) {
		t.Error("the old core key is not kept")
	}
	if exists(filepath.Join(w.dir, ".env.before-key-rotation")) {
		t.Error("the copy of .env was left behind")
	}
}

func TestRotateKeyForgetOldKeepsNoOldKey(t *testing.T) {
	w, r, out, errOut := rotateInstall(t)
	if code := rotateKey(r, w, "--forget-old", "--yes"); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	_, raw := readEnvFile(t, w.dir)
	for _, v := range rotateOldKeys {
		if strings.Contains(raw, v) {
			t.Fatalf(".env still holds an old key:\n%s", raw)
		}
	}
	if len(retiredKeyLines([]byte(raw))) != 0 {
		t.Error("retired lines are left")
	}
	if !strings.Contains(out.String(), "not kept in .env (--forget-old)") {
		t.Fatalf("output: %s", out)
	}
}

func TestSecondRotationKeepsTheTagKeyAndBothRetiredKeys(t *testing.T) {
	w, r, out, errOut := rotateInstall(t)
	if code := rotateKey(r, w, "--yes"); code != 0 {
		t.Fatalf("first: exit %d\n%s%s", code, out, errOut)
	}
	first := w.readEnv()
	r.in = strings.NewReader("y\n")
	if code := rotateKey(r, w); code != 0 {
		t.Fatalf("second: exit %d\n%s%s", code, out, errOut)
	}
	second, raw := readEnvFile(t, w.dir)
	for _, k := range keyServices {
		if second[k.master] == first[k.master] {
			t.Errorf("%s did not change", k.master)
		}
		// Gift code hashes and 3x-ui subscription ids were made with the tag key of the first key, and keep working.
		if second[k.tag] != first[k.tag] {
			t.Errorf("%s changed in the second rotation", k.tag)
		}
		// Both retired keys are kept, the first one and the second one.
		if !strings.Contains(raw, "# "+k.prev+"="+rotateOldKeys[k.master]+"\n") || !strings.Contains(raw, "# "+k.prev+"="+first[k.master]+"\n") {
			t.Errorf("the retired keys of %s: not both kept", k.service)
		}
	}
	if got := retiredKeys([]byte(raw)); len(got["BOBRES_CORE_MASTER_KEY_PREVIOUS"]) != 2 {
		t.Errorf("retired keys: %v", got)
	}
	// A tag made with the new key's envelope + the tag key is the one the very first key made.
	oldEnv, _ := crypto.NewEnvelope([]byte(rotateOldKeys["BOBRES_CORE_MASTER_KEY"]))
	newEnv, err := crypto.NewKeyring(crypto.Keys{Master: second["BOBRES_CORE_MASTER_KEY"], Tag: second["BOBRES_CORE_TAG_KEY"]})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(oldEnv.Tag([]byte("gift")), newEnv.Tag([]byte("gift"))) {
		t.Error("a tag changed through two rotations")
	}
}

func TestRetiredKeysSurviveEveryOtherRewriteOfEnv(t *testing.T) {
	w, r, out, errOut := rotateInstall(t)
	if code := rotateKey(r, w, "--yes"); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	_, raw := readEnvFile(t, w.dir)
	lines := retiredKeyLines([]byte(raw))
	if len(lines) != 4 { // the explanation and the three keys
		t.Fatalf("retired lines: %q", lines)
	}
	o, _, errOut2 := dbOps("", w.fake)
	if code := o.configSet(w.dir, "BOBRES_TIMEZONE", "Europe/Berlin", true); code != 0 {
		t.Fatalf("config set: %d %s", code, errOut2)
	}
	env, raw := readEnvFile(t, w.dir)
	if env["BOBRES_TIMEZONE"] != "Europe/Berlin" || len(retiredKeyLines([]byte(raw))) != 4 {
		t.Fatalf("config set lost the retired keys:\n%s", raw)
	}
	// and a backup of the install carries the keys in use, not the retired ones
	if keys := masterKeys(env); len(keys) < 6 || keys["BOBRES_CORE_MASTER_KEY_PREVIOUS"] != "" {
		t.Errorf("carried keys: %v", keys)
	}
	if got := masterKeys(env); got["BOBRES_CORE_TAG_KEY"] == "" {
		t.Error("a backup would not carry the tag key")
	}
}

// A restore brings back the keys of the backup it restores, the tag key and a previous key too.
func TestRestoreAppliesTheTagAndPreviousKeys(t *testing.T) {
	dir := restoreInstall(t)
	archive := map[string]string{
		"BOBRES_CORE_MASTER_KEY": strings.Repeat("d1", 32), "BOBRES_CORE_TAG_KEY": strings.Repeat("e2", 32),
		"BOBRES_CORE_MASTER_KEY_PREVIOUS": strings.Repeat("f3", 32),
	}
	changed, err := applyMasterKeys(dir, archive, false)
	if err != nil || len(changed) != 3 {
		t.Fatalf("applyMasterKeys: %v %v", changed, err)
	}
	env, _, _ := readEnv(filepath.Join(dir, ".env"))
	for k, v := range archive {
		if env[k] != v {
			t.Errorf("%s: %q", k, env[k])
		}
	}
	// A backup that does not carry them leaves the install's alone.
	if _, err := applyMasterKeys(dir, map[string]string{"BOBRES_CORE_MASTER_KEY": archive["BOBRES_CORE_MASTER_KEY"]}, false); err != nil {
		t.Fatal(err)
	}
	if env2, _, _ := readEnv(filepath.Join(dir, ".env")); env2["BOBRES_CORE_TAG_KEY"] != archive["BOBRES_CORE_TAG_KEY"] {
		t.Error("the tag key was dropped by a backup without one")
	}
}

func TestBackupKeysAreTheOnesTheCLIKnows(t *testing.T) {
	names := config.BackupKeyNames()
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Errorf("%s twice", n)
		}
		seen[n] = true
	}
	for _, k := range keyServices {
		for _, n := range []string{k.master, k.prev, k.tag} {
			if !seen[n] {
				t.Errorf("a backup does not carry %s", n)
			}
		}
	}
}
