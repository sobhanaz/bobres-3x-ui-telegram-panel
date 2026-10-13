package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
)

// testKey is an Ed25519 key pair; pub is base64 SPKI, as keys.txt has it.
func testKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der), priv
}

// signedRelease is a release's files as release.yml publishes them, its key
// list (seq 3) signed by root, with cli as bobres_linux_amd64.
func signedRelease(t *testing.T, root ed25519.PrivateKey, ver string, cli []byte) map[string][]byte {
	t.Helper()
	return signedReleaseSeq(t, root, ver, cli, 3)
}

// signedReleaseSeq is signedRelease with a key list of sequence number seq.
func signedReleaseSeq(t *testing.T, root ed25519.PrivateKey, ver string, cli []byte, seq int) map[string][]byte {
	t.Helper()
	relPub, relPriv := testKey(t)
	keys := []byte("bobres-keys v1\nseq " + strconv.Itoa(seq) + "\nexpires 2027-09-30T15:15Z\nmin_version 0.0.1\nkey rel-a " + relPub + "\n")
	sum := sha256.Sum256(cli)
	sums := []byte("# bobres-release v1\n# version " + ver + "\n# channel stable\n# key_id rel-a\n" +
		"# issued 2026-10-10T10:00Z\n# expires 2026-11-24T10:00Z\n" + hex.EncodeToString(sum[:]) + "  bobres_linux_amd64\n")
	return map[string][]byte{
		"checksums.txt": sums, "checksums.txt.sig": ed25519.Sign(relPriv, sums),
		"keys.txt": keys, "keys.txt.sig.1": ed25519.Sign(root, keys),
		"bobres_linux_amd64": cli,
	}
}

// fakeGitHub serves releases the way GitHub does: the latest under
// /latest/download, each version under /download/vX.Y.Z.
type fakeGitHub struct {
	srv      *httptest.Server
	latest   map[string][]byte
	versions map[string]map[string][]byte
}

func newFakeGitHub(t *testing.T, latest map[string][]byte) *fakeGitHub {
	gh := &fakeGitHub{latest: latest, versions: map[string]map[string][]byte{}}
	gh.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		files := gh.latest
		rest, ok := strings.CutPrefix(r.URL.Path, "/latest/download/")
		if !ok {
			var v string
			v, rest, _ = strings.Cut(strings.TrimPrefix(r.URL.Path, "/download/v"), "/")
			files = gh.versions[v]
		}
		b, found := files[rest]
		if !found {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(gh.srv.Close)
	return gh
}

// fakeCLI stands in for the new bobres binary's install run.
type fakeCLI struct {
	calls [][]string
	// binary is what the binary file held when it ran.
	binary []string
	do     func() error
	// hang: the run never ends on its own, only when its context does (as
	// the real one is killed); started is closed once it runs.
	hang    bool
	started chan struct{}
}

func (c *fakeCLI) run(ctx context.Context, exe string, args ...string) error {
	c.calls = append(c.calls, args)
	b, _ := os.ReadFile(exe) //nolint:gosec // the test's own file
	c.binary = append(c.binary, string(b))
	if c.hang {
		if c.started != nil {
			close(c.started)
		}
		<-ctx.Done()
		return errors.New("signal: killed")
	}
	if c.do != nil {
		return c.do()
	}
	return nil
}

// installs writes what the new version's install writes: its version in
// .env and its own compose file.
func installs(dir, ver string) func() error {
	return func() error {
		env, _, err := readEnv(filepath.Join(dir, ".env"))
		if err != nil {
			return err
		}
		env["BOBRES_VERSION"] = ver
		if err := writeAtomic(filepath.Join(dir, ".env"), renderEnv(env), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("compose of "+ver+"\n"), 0o600)
	}
}

// migratedSchema is the database's migration versions once the new
// version's migrations ran.
const migratedSchema = "core 4\npayments 2\nprovisioner 4\n"

// installsAndMigrates is installs, and the new version then migrates the
// database and writes to it.
func (ut *updateTest) installsAndMigrates(ver string) func() error {
	write := installs(ut.dir, ver)
	return func() error {
		ut.db.schema, ut.db.dump = migratedSchema, "DUMP WRITTEN BY "+ver
		return write()
	}
}

const oldCLI, newCLI = "#!old bobres 1.2.3", "#!new bobres 1.3.0"

type updateTest struct {
	dir, exe string
	root     ed25519.PrivateKey
	gh       *fakeGitHub
	db       *fakeDB
	cli      *fakeCLI
	u        *updater
	out      *bytes.Buffer
	errOut   *bytes.Buffer
}

// newUpdateTest is an install of 1.2.3 with a 1.2.3 CLI, and a signed
// 1.3.0 as the latest release.
func newUpdateTest(t *testing.T, stdin string) *updateTest {
	t.Helper()
	rootPub, root := testKey(t)
	ut := &updateTest{dir: restoreInstall(t), root: root, db: &fakeDB{dump: "DUMP BEFORE THE UPDATE"}, cli: &fakeCLI{}}
	ut.exe = filepath.Join(t.TempDir(), "bobres")
	if err := os.WriteFile(ut.exe, []byte(oldCLI), 0o755); err != nil { //nolint:gosec // a binary is executable
		t.Fatal(err)
	}
	ut.gh = newFakeGitHub(t, signedRelease(t, root, "1.3.0", []byte(newCLI)))
	o, out, errOut := dbOps(stdin, ut.db)
	ut.out, ut.errOut = out, errOut
	ut.u = &updater{
		ops: o, current: "v1.2.3",
		latestURL:  ut.gh.srv.URL + "/latest/download",
		versionURL: func(v string) string { return ut.gh.srv.URL + "/download/v" + v },
		hc:         ut.gh.srv.Client(), roots: []string{rootPub}, platform: "linux_amd64",
		stateDir: t.TempDir(),
		exe:      func() (string, error) { return ut.exe, nil },
		runCLI:   ut.cli.run,
	}
	return ut
}

func (ut *updateTest) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(ut.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// state is one of install.sh's state files ("" when missing).
func (ut *updateTest) state(name string) string {
	b, _ := os.ReadFile(filepath.Join(ut.u.stateDir, name))
	return string(b)
}

func (ut *updateTest) setState(t *testing.T, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ut.u.stateDir, name), []byte(value), 0o644); err != nil { //nolint:gosec // as install.sh writes it
		t.Fatal(err)
	}
}

// unchanged checks that a refused update left everything as it was.
func (ut *updateTest) unchanged(t *testing.T) {
	t.Helper()
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI {
		t.Errorf("the CLI changed: %q", b)
	}
	if exists(ut.exe + ".prev") {
		t.Error("bobres.prev written")
	}
	if len(ut.db.steps) != 0 || len(ut.cli.calls) != 0 {
		t.Errorf("steps %q, CLI runs %q", ut.db.steps, ut.cli.calls)
	}
	if entries, _ := os.ReadDir(filepath.Join(ut.dir, "backups")); len(entries) != 0 {
		t.Errorf("backups written: %v", entries)
	}
	if m, _ := filepath.Glob(filepath.Join(ut.dir, ".restore-point*")); len(m) != 0 {
		t.Errorf("restore point written: %v", m)
	}
	if ut.state("keys.seq") != "" || ut.state("release.version") != "" {
		t.Error("the release state was written")
	}
}

func TestUpdateCheck(t *testing.T) {
	ut := newUpdateTest(t, "")
	if code := ut.u.updateCmd([]string{"--check", "--dir", ut.dir}); code != 0 {
		t.Fatalf("check: %d %s", code, ut.errOut)
	}
	for _, w := range []string{"Current: 1.2.3 (bobres CLI v1.2.3)", "Latest:  1.3.0 (stable, signature verified)", "Newer:   yes. Update with: sudo bobres update"} {
		if !strings.Contains(ut.out.String(), w) {
			t.Errorf("missing %q:\n%s", w, ut.out)
		}
	}
	ut.unchanged(t)

	// A given version, and up to date.
	ut.gh.versions["1.2.3"] = signedRelease(t, ut.root, "1.2.3", []byte(oldCLI))
	ut.out.Reset()
	if code := ut.u.updateCmd([]string{"--check", "--version", "v1.2.3", "--dir", ut.dir}); code != 0 ||
		!strings.Contains(ut.out.String(), "Release: 1.2.3") || !strings.Contains(ut.out.String(), "Newer:   no") {
		t.Fatalf("check 1.2.3: %d %s %s", code, ut.out, ut.errOut)
	}

	// A development build checks too, without an install.
	ut.u.current = "dev"
	ut.out.Reset()
	if code := ut.u.updateCmd([]string{"--check", "--dir", t.TempDir()}); code != 0 ||
		!strings.Contains(ut.out.String(), "Current: latest (bobres CLI dev)") || !strings.Contains(ut.out.String(), "Newer:   unknown") {
		t.Fatalf("dev check: %d %s %s", code, ut.out, ut.errOut)
	}
	ut.u.current = "v1.2.3"

	// A key list older than the one this server saw is a replay.
	ut.setState(t, "keys.seq", "4\n")
	ut.errOut.Reset()
	if code := ut.u.updateCmd([]string{"--check", "--dir", ut.dir}); code != 1 || !strings.Contains(ut.errOut.String(), "key list (seq 3) is older than the one this server has seen (seq 4)") {
		t.Fatalf("old key list: %d %s", code, ut.errOut)
	}
	_ = os.Remove(filepath.Join(ut.u.stateDir, "keys.seq"))

	// Tampered, missing and unreachable releases are errors.
	ut.gh.latest["checksums.txt"] = append(ut.gh.latest["checksums.txt"], "evil  bobres_linux_arm64\n"...)
	ut.errOut.Reset()
	if code := ut.u.updateCmd([]string{"--check", "--dir", ut.dir}); code != 1 || !strings.Contains(ut.errOut.String(), "did not pass the signature check") {
		t.Fatalf("tampered: %d %s", code, ut.errOut)
	}
	ut.errOut.Reset()
	if code := ut.u.updateCmd([]string{"--check", "--version", "9.9.9", "--dir", ut.dir}); code != 1 || !strings.Contains(ut.errOut.String(), "there is no release 9.9.9") {
		t.Fatalf("missing: %d %s", code, ut.errOut)
	}
	ut.gh.srv.Close()
	ut.errOut.Reset()
	if code := ut.u.updateCmd([]string{"--check", "--dir", ut.dir}); code != 1 || !strings.Contains(ut.errOut.String(), "is GitHub reachable") {
		t.Fatalf("unreachable: %d %s", code, ut.errOut)
	}
	if code := ut.u.updateCmd([]string{"--check", "--version", "next"}); code != 2 {
		t.Fatalf("bad version: %d", code)
	}
}

func TestUpdate(t *testing.T) {
	ut := newUpdateTest(t, "")
	envBefore := ut.read(t, ".env")
	ut.cli.do = installs(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("update: %d\n%s\n%s", code, ut.out, ut.errOut)
	}
	// Backup first; no stop or restore when all goes well.
	if !reflect.DeepEqual(ut.db.steps, []string{"dump"}) {
		t.Fatalf("steps %q", ut.db.steps)
	}
	// The new CLI was in place when its install ran, with these flags: the
	// install's checks for a new server are skipped on an update.
	want := [][]string{{"install", "--version", "1.3.0", "--dir", ut.dir, "--quiet", "--yes", "--skip-checks", "--offline"}}
	if !reflect.DeepEqual(ut.cli.calls, want) || ut.cli.binary[0] != newCLI {
		t.Fatalf("CLI runs %q with %q", ut.cli.calls, ut.cli.binary)
	}
	if b, _ := os.ReadFile(ut.exe + ".prev"); string(b) != oldCLI {
		t.Fatalf("bobres.prev: %q", b)
	}
	if st, _ := os.Stat(ut.exe); st.Mode().Perm() != 0o755 {
		t.Fatalf("new CLI mode %v", st.Mode())
	}

	rp, err := readRestorePoint(ut.dir)
	if err != nil || rp == nil {
		t.Fatalf("restore point: %v %v", rp, err)
	}
	rpDir := filepath.Join(ut.dir, ".restore-point")
	noFloor := "" // install.sh had recorded no release before
	wantRP := restorePoint{
		FromVersion: "1.2.3", ToVersion: "1.3.0", BackupFile: filepath.Join(rpDir, backup.FileName(opsNow)),
		CreatedAt: opsNow, CLIPath: ut.exe, CLIBackupPath: filepath.Join(rpDir, "bobres"), FilesDir: filepath.Join(rpDir, "files"),
		SchemaVersions: map[string]int64{"core": 3, "payments": 2, "provisioner": 4}, ReleaseFloor: &noFloor,
	}
	if !reflect.DeepEqual(*rp, wantRP) {
		t.Fatalf("restore point\n got %+v\nwant %+v", *rp, wantRP)
	}
	if _, dump := openBackup(t, rp.BackupFile, installKey); dump != "DUMP BEFORE THE UPDATE" {
		t.Fatalf("backup: %q", dump)
	}
	if saved, _ := os.ReadFile(filepath.Join(rp.FilesDir, ".env")); string(saved) != envBefore {
		t.Fatal("the restore point's .env is not the one from before the update")
	}
	if b, _ := os.ReadFile(rp.CLIBackupPath); string(b) != oldCLI {
		t.Fatalf("the restore point's bobres: %q", b)
	}
	// The restore point is root's alone, and nothing of it (the .env with the
	// backup key above all) is in backups/, which the backup service's user
	// controls and operators copy off the server.
	for path, mode := range map[string]os.FileMode{rpDir: 0o700, filepath.Join(rp.FilesDir, ".env"): 0o600, rp.BackupFile: 0o600} {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", path, st, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(ut.dir, "backups")); len(entries) != 0 {
		t.Fatalf("backups/ holds %v", entries)
	}
	if m, _ := filepath.Glob(filepath.Join(ut.dir, ".restore-point.*")); len(m) != 0 {
		t.Fatalf("left over: %v", m)
	}
	// What install.sh records, for its own checks and the next update's.
	if ut.state("keys.seq") != "3\n" || ut.state("release.version") != "1.3.0\n" {
		t.Fatalf("state: %q %q", ut.state("keys.seq"), ut.state("release.version"))
	}
	if !strings.Contains(ut.out.String(), "BOBRES is updated to 1.3.0") || !strings.Contains(ut.out.String(), "sudo bobres rollback") {
		t.Fatalf("output:\n%s", ut.out)
	}
}

// The release state never goes down: a newer key list seen before stays.
func TestUpdateKeepsHigherState(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.gh.latest = signedReleaseSeq(t, ut.root, "1.3.0", []byte(newCLI), 7)
	ut.setState(t, "keys.seq", "6\n")
	ut.setState(t, "release.version", "1.2.3\n")
	ut.cli.do = installs(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("update: %d %s", code, ut.errOut)
	}
	if ut.state("keys.seq") != "7\n" || ut.state("release.version") != "1.3.0\n" {
		t.Fatalf("state: %q %q", ut.state("keys.seq"), ut.state("release.version"))
	}
}

func TestUpdateRefusals(t *testing.T) {
	t.Run("bad signature", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.gh.latest["checksums.txt.sig"][0] ^= 1
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "release signature is invalid") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("unsigned", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		delete(ut.gh.latest, "checksums.txt.sig")
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "not signed") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("other root", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		_, stranger := testKey(t)
		ut.gh.latest = signedRelease(t, stranger, "1.3.0", []byte(newCLI))
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "no embedded root verified it") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	// An older key list, still signed by the root and not expired, may name
	// a release key revoked since: install.sh refuses it, and so does update.
	t.Run("replayed key list", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.setState(t, "keys.seq", "5\n")
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 ||
			!strings.Contains(ut.errOut.String(), "key list (seq 3) is older than the one this server has seen (seq 5)") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		_ = os.Remove(filepath.Join(ut.u.stateDir, "keys.seq"))
		ut.unchanged(t)
	})
	t.Run("older than the release installed before", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.setState(t, "release.version", "1.4.0\n")
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 ||
			!strings.Contains(ut.errOut.String(), "release 1.3.0 is older than release 1.4.0, which this server installed before") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		_ = os.Remove(filepath.Join(ut.u.stateDir, "release.version"))
		ut.unchanged(t)
	})
	t.Run("downgrade", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.gh.latest = signedRelease(t, ut.root, "1.2.0", []byte(newCLI))
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 ||
			!strings.Contains(ut.errOut.String(), "older than this install (1.2.3)") || !strings.Contains(ut.errOut.String(), "bobres rollback") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("same version", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.gh.latest = signedRelease(t, ut.root, "1.2.3", []byte(oldCLI))
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 || !strings.Contains(ut.out.String(), "already runs 1.2.3") {
			t.Fatalf("got %d %s %s", code, ut.out, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("wrong version files", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.gh.versions["1.4.0"] = ut.gh.latest // signed as 1.3.0
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes", "--version", "1.4.0"}); code != 1 || !strings.Contains(ut.errOut.String(), "signed for 1.3.0") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("binary does not match its checksum", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.gh.latest["bobres_linux_amd64"] = []byte("#!evil")
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "checksum mismatch") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("no binary for this system", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.u.platform = "linux_riscv64"
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "no CLI for this system") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("not enough disk", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.u.freeGB = func(string) float64 { return 1.5 }
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 ||
			!strings.Contains(ut.errOut.String(), "1.5 GB free in "+ut.dir+": an update needs at least 2 GB") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("development build", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.u.current = "dev"
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "development build") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("not confirmed", func(t *testing.T) {
		ut := newUpdateTest(t, "n\n")
		if code := ut.u.updateCmd([]string{"--dir", ut.dir}); code != 1 || !strings.Contains(ut.out.String(), "Update BOBRES from 1.2.3 to 1.3.0?") {
			t.Fatalf("got %d %s", code, ut.out)
		}
		ut.unchanged(t)
	})
	t.Run("backup fails", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		ut.db.dumpErr = errors.New("exit status 1")
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "backup before the update failed") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.db.steps = nil
		ut.unchanged(t)
	})
	t.Run("no install", func(t *testing.T) {
		ut := newUpdateTest(t, "")
		if code := ut.u.updateCmd([]string{"--dir", t.TempDir(), "--yes"}); code != 2 {
			t.Fatalf("got %d", code)
		}
	})
}

// unhealthyCore is ps output with the database up and core failing.
const unhealthyCore = `[{"Service":"postgres","State":"running","Health":"healthy"},{"Service":"core","State":"running","Health":"unhealthy"}]`

// A new version that migrated the database and does not become healthy is
// rolled back on its own: the old CLI, the old install files, and the
// database from before the update once what it holds now is backed up, with
// the services stopped.
func TestUpdateRollsBackWhenUnhealthy(t *testing.T) {
	ut := newUpdateTest(t, "")
	envBefore, composeBefore := ut.read(t, ".env"), ut.read(t, "docker-compose.yml")
	ut.cli.do = ut.installsAndMigrates("1.3.0")
	// Unhealthy until the database is back to the backup.
	ut.db.ps = func() string {
		if len(ut.db.restored) == 0 {
			return unhealthyCore
		}
		return healthyPS
	}
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d\n%s\n%s", code, ut.out, ut.errOut)
	}
	want := []string{"dump", "stop core payments provisioner bot", "dump", "up -d postgres redis", "restore", "up -d --remove-orphans"}
	if !reflect.DeepEqual(ut.db.steps, want) {
		t.Fatalf("steps %q\nwant  %q", ut.db.steps, want)
	}
	if len(ut.db.restored) != 1 || ut.db.restored[0] != "DUMP BEFORE THE UPDATE" {
		t.Fatalf("restored %q", ut.db.restored)
	}
	// What the new version wrote is kept in a backup of its own.
	saved := filepath.Join(ut.dir, "backups", safetyName(opsNow, "before-rollback"))
	if _, dump := openBackup(t, saved, installKey); dump != "DUMP WRITTEN BY 1.3.0" {
		t.Fatalf("backup before the rollback: %q", dump)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI || exists(ut.exe+".prev") {
		t.Fatalf("the CLI was not swapped back: %q", b)
	}
	if ut.read(t, ".env") != envBefore || ut.read(t, "docker-compose.yml") != composeBefore {
		t.Fatal("the install files are not the ones from before the update")
	}
	if rp, _ := readRestorePoint(ut.dir); rp == nil || rp.UsedAt == nil {
		t.Fatalf("restore point not marked as used: %+v", rp)
	}
	for _, w := range []string{"BOBRES 1.3.0 did not become healthy. Rolling back to 1.2.3", "BOBRES runs 1.2.3 again, with the database as of"} {
		if !strings.Contains(ut.errOut.String(), w) {
			t.Errorf("missing %q:\n%s", w, ut.errOut)
		}
	}
	if !strings.Contains(ut.out.String(), "Current data saved in "+saved) {
		t.Errorf("output:\n%s", ut.out)
	}
}

// An install that fails after writing its files, but before any migration
// ran (an image pull that failed: the old version kept running and taking
// orders), gets its files back and keeps the database as it is.
func TestUpdateRollbackKeepsDataWithoutMigration(t *testing.T) {
	ut := newUpdateTest(t, "")
	envBefore, composeBefore := ut.read(t, ".env"), ut.read(t, "docker-compose.yml")
	write := installs(ut.dir, "1.3.0")
	ut.cli.do = func() error {
		if err := write(); err != nil {
			return err
		}
		return errors.New("exit status 1")
	}
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if want := []string{"dump", "up -d --remove-orphans"}; !reflect.DeepEqual(ut.db.steps, want) {
		t.Fatalf("steps %q, want %q", ut.db.steps, want)
	}
	if len(ut.db.restored) != 0 {
		t.Fatalf("the database was restored: %q", ut.db.restored)
	}
	if ut.read(t, ".env") != envBefore || ut.read(t, "docker-compose.yml") != composeBefore {
		t.Fatal("the install files are not the ones from before the update")
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI {
		t.Fatal("the CLI was not swapped back")
	}
	// Its restore point did nothing: gone, so rollback does not undo an
	// update that never happened.
	if rp, err := readRestorePoint(ut.dir); rp != nil || err != nil {
		t.Fatalf("restore point: %+v %v", rp, err)
	}
	for _, w := range []string{"the install of 1.3.0 failed (exit status 1)", "BOBRES runs 1.2.3 again, its data kept as it is (no migration had run)"} {
		if !strings.Contains(ut.errOut.String(), w) {
			t.Errorf("missing %q:\n%s", w, ut.errOut)
		}
	}
}

// An install that fails after a migration ran is rolled back with the
// database.
func TestUpdateRollsBackWhenInstallFails(t *testing.T) {
	ut := newUpdateTest(t, "")
	envBefore := ut.read(t, ".env")
	migrate := ut.installsAndMigrates("1.3.0")
	ut.cli.do = func() error {
		if err := migrate(); err != nil {
			return err
		}
		return errors.New("exit status 1")
	}
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !contains(ut.db.steps, "restore") || ut.read(t, ".env") != envBefore {
		t.Fatalf("steps %q", ut.db.steps)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI {
		t.Fatal("the CLI was not swapped back")
	}
	if !strings.Contains(ut.errOut.String(), "the install of 1.3.0 failed (exit status 1)") {
		t.Fatalf("output: %s", ut.errOut)
	}
}

// When the migration versions could not be read before the update, a
// rollback restores the database to be safe (after its backup).
func TestUpdateRollbackUnknownSchema(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.db.schemaErr = errors.New("exit status 2")
	write := installs(ut.dir, "1.3.0")
	ut.cli.do = func() error {
		ut.db.schemaErr = nil
		if err := write(); err != nil {
			return err
		}
		return errors.New("exit status 1")
	}
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if want := []string{"dump", "stop core payments provisioner bot", "dump", "up -d postgres redis", "restore", "up -d --remove-orphans"}; !reflect.DeepEqual(ut.db.steps, want) {
		t.Fatalf("steps %q", ut.db.steps)
	}
	if !strings.Contains(ut.errOut.String(), "migration versions were not read") {
		t.Fatalf("output: %s", ut.errOut)
	}
}

// When the backup before an automatic rollback fails, the database is not
// restored and nothing else changes: the services start again as they were
// and `bobres rollback` finishes the job once the cause is fixed.
func TestUpdateRollbackBackupFails(t *testing.T) {
	ut := newUpdateTest(t, "")
	migrate := ut.installsAndMigrates("1.3.0")
	ut.cli.do = func() error {
		ut.db.dumpErr = errors.New("exit status 1") // the disk filled up
		return migrate()
	}
	ut.db.ps = func() string { return unhealthyCore }
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if want := []string{"dump", "stop core payments provisioner bot", "dump", "up -d"}; !reflect.DeepEqual(ut.db.steps, want) {
		t.Fatalf("steps %q, want %q", ut.db.steps, want)
	}
	if len(ut.db.restored) != 0 || !strings.Contains(ut.read(t, ".env"), "BOBRES_VERSION=1.3.0") {
		t.Fatal("the rollback went on without its backup")
	}
	if rp, _ := readRestorePoint(ut.dir); rp == nil || rp.UsedAt != nil {
		t.Fatalf("restore point: %+v", rp)
	}
	for _, w := range []string{"the backup of the current data failed", "Nothing was changed", "run: sudo bobres rollback"} {
		if !strings.Contains(ut.errOut.String(), w) {
			t.Errorf("missing %q:\n%s", w, ut.errOut)
		}
	}

	ut.db.dumpErr, ut.db.ps, ut.db.steps = nil, nil, nil
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("rollback: %d\n%s", code, ut.errOut)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI || len(ut.db.restored) != 1 {
		t.Fatalf("CLI %q, restored %q", b, ut.db.restored)
	}
}

// An install that stops before writing anything (its checks failed) left
// the old version running: only the CLI goes back, and the database (which
// kept taking orders) is not restored.
func TestUpdateInstallChangedNothing(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.cli.do = func() error { return errors.New("exit status 1") }
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !reflect.DeepEqual(ut.db.steps, []string{"dump"}) {
		t.Fatalf("steps %q", ut.db.steps)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI {
		t.Fatal("the CLI was not swapped back")
	}
	if rp, err := readRestorePoint(ut.dir); rp != nil || err != nil {
		t.Fatalf("restore point: %+v %v", rp, err)
	}
	if !strings.Contains(ut.out.String(), "The install was not changed: the store kept running 1.2.3") {
		t.Fatalf("output: %s", ut.out)
	}
}

// An update attempt that fails, however it fails, leaves the restore point
// of the update before in place and unused: `bobres rollback` still goes
// back to the version before that one, with its database.
func TestFailedUpdateKeepsPreviousRestorePoint(t *testing.T) {
	const cli14 = "#!new bobres 1.4.0"
	for _, c := range []struct {
		name string
		// setup makes the update to 1.4.0 fail; it returns the exit code to expect.
		setup func(t *testing.T, ut *updateTest)
	}{
		{"CLI swap fails", func(t *testing.T, ut *updateTest) {
			if os.Geteuid() == 0 {
				t.Skip("root writes in a read-only directory")
			}
			d := filepath.Dir(ut.exe)
			if err := os.Chmod(d, 0o500); err != nil { //nolint:gosec // a test directory
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(d, 0o700) }) //nolint:gosec // a test directory
		}},
		{"install checks fail", func(_ *testing.T, ut *updateTest) {
			ut.cli.do = func() error { return errors.New("exit status 1") }
		}},
		{"image pull fails", func(_ *testing.T, ut *updateTest) {
			write := installs(ut.dir, "1.4.0")
			ut.cli.do = func() error { _ = write(); return errors.New("exit status 1") }
		}},
		{"migrated, then unhealthy", func(_ *testing.T, ut *updateTest) {
			ut.cli.do = ut.installsAndMigrates("1.4.0")
			ut.db.ps = func() string {
				if len(ut.db.restored) == 0 {
					return unhealthyCore
				}
				return healthyPS
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ut := newUpdateTest(t, "")
			envBefore := ut.read(t, ".env")
			ut.cli.do = installs(ut.dir, "1.3.0")
			if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("update to 1.3.0: %d %s", code, ut.errOut)
			}
			first, _ := readRestorePoint(ut.dir)
			env13, compose13 := ut.read(t, ".env"), ut.read(t, "docker-compose.yml")

			// bobres 1.3.0 now runs, and 1.4.0 is out.
			ut.u.current = "v1.3.0"
			ut.gh.latest = signedRelease(t, ut.root, "1.4.0", []byte(cli14))
			ut.db.dump, ut.db.steps, ut.db.restored, ut.cli.do = "DUMP OF 1.3.0", nil, nil, nil
			c.setup(t, ut)
			if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
				t.Fatalf("update to 1.4.0: %d", code)
			}
			if b, _ := os.ReadFile(ut.exe); string(b) != newCLI {
				t.Fatalf("CLI %q, want 1.3.0's", b)
			}
			if c.name != "migrated, then unhealthy" && len(ut.db.restored) != 0 {
				t.Fatalf("restored %q", ut.db.restored)
			}
			if ut.read(t, ".env") != env13 || ut.read(t, "docker-compose.yml") != compose13 {
				t.Fatal("the install files are not 1.3.0's")
			}
			rp, err := readRestorePoint(ut.dir)
			if err != nil || rp == nil || rp.UsedAt != nil || !reflect.DeepEqual(rp, first) {
				t.Fatalf("restore point\n got %+v %v\nwant %+v", rp, err, first)
			}
			if m, _ := filepath.Glob(filepath.Join(ut.dir, ".restore-point.*")); len(m) != 0 {
				t.Fatalf("left over: %v", m)
			}

			// ...and it still takes the store back to 1.2.3 with its database.
			_ = os.Chmod(filepath.Dir(ut.exe), 0o700) //nolint:gosec // a test directory
			ut.db.steps, ut.db.restored, ut.db.ps = nil, nil, nil
			if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("rollback: %d\n%s", code, ut.errOut)
			}
			if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI || ut.read(t, ".env") != envBefore ||
				len(ut.db.restored) != 1 || ut.db.restored[0] != "DUMP BEFORE THE UPDATE" {
				t.Fatalf("CLI %q, restored %q", b, ut.db.restored)
			}
		})
	}
}

func TestRollback(t *testing.T) {
	ut := newUpdateTest(t, "")
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "There is no restore point") {
		t.Fatalf("without a restore point: %d %s", code, ut.errOut)
	}
	if len(ut.db.steps) != 0 {
		t.Fatalf("steps %q", ut.db.steps)
	}

	envBefore, composeBefore := ut.read(t, ".env"), ut.read(t, "docker-compose.yml")
	ut.cli.do = installs(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("update: %d %s", code, ut.errOut)
	}
	ut.db.steps, ut.db.dump = nil, "DUMP AFTER THE UPDATE"

	// Not confirmed: nothing happens.
	ut.u.in = strings.NewReader("no\n")
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir}); code != 1 || len(ut.db.steps) != 0 ||
		!strings.Contains(ut.out.String(), "Roll back to 1.2.3?") {
		t.Fatalf("not confirmed: %d %q", code, ut.db.steps)
	}

	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("rollback: %d\n%s\n%s", code, ut.out, ut.errOut)
	}
	// The services stop before the backup of the data since the update.
	want := []string{"stop core payments provisioner bot", "dump", "up -d postgres redis", "restore", "up -d --remove-orphans"}
	if !reflect.DeepEqual(ut.db.steps, want) {
		t.Fatalf("steps %q\nwant  %q", ut.db.steps, want)
	}
	if ut.db.restored[0] != "DUMP BEFORE THE UPDATE" {
		t.Fatalf("restored %q", ut.db.restored)
	}
	// The data since the update is kept in a backup of its own.
	after := filepath.Join(ut.dir, "backups", safetyName(opsNow, "before-rollback"))
	if _, dump := openBackup(t, after, installKey); dump != "DUMP AFTER THE UPDATE" {
		t.Fatalf("backup before the rollback: %q", dump)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI || exists(ut.exe+".prev") {
		t.Fatalf("the CLI was not put back: %q", b)
	}
	if ut.read(t, ".env") != envBefore || ut.read(t, "docker-compose.yml") != composeBefore {
		t.Fatal("the install files were not put back")
	}
	if !strings.Contains(ut.out.String(), "Rolled back: BOBRES runs 1.2.3") {
		t.Fatalf("output:\n%s", ut.out)
	}

	ut.db.steps = nil
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "was already used") || len(ut.db.steps) != 0 {
		t.Fatalf("second rollback: %d %s", code, ut.errOut)
	}
}

// A restore point whose backup no longer opens is refused before anything
// changes.
func TestRollbackDamagedBackup(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.cli.do = installs(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("update: %d %s", code, ut.errOut)
	}
	rp, _ := readRestorePoint(ut.dir)
	b, _ := os.ReadFile(rp.BackupFile)
	b[len(b)-3] ^= 1
	_ = os.WriteFile(rp.BackupFile, b, 0o600) //nolint:gosec // the test's own file
	ut.db.steps = nil
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "damaged or incomplete") {
		t.Fatalf("got %d %s", code, ut.errOut)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != newCLI || len(ut.db.steps) != 0 {
		t.Fatalf("changed: CLI %q, steps %q", b, ut.db.steps)
	}
}

// A restore point others can change is not trusted: its compose file and
// .env would be started as root. The record's paths are not trusted either:
// the backup and the files are always those in the restore point's folder.
func TestRestorePointTrust(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.cli.do = installs(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("update: %d %s", code, ut.errOut)
	}
	rpDir := filepath.Join(ut.dir, ".restore-point")

	// Paths pointing elsewhere are ignored.
	b, _ := os.ReadFile(filepath.Join(rpDir, "restore-point.json"))
	b = bytes.Replace(b, []byte(filepath.Join(rpDir, "files")), []byte("/tmp/evil"), 1)
	b = bytes.Replace(b, []byte(rpDir+"/bobres-"), []byte("/tmp/evil/bobres-"), 1)
	if err := os.WriteFile(filepath.Join(rpDir, "restore-point.json"), b, 0o600); err != nil { //nolint:gosec // the test's own file
		t.Fatal(err)
	}
	rp, err := readRestorePoint(ut.dir)
	if err != nil || filepath.Dir(rp.BackupFile) != rpDir || rp.FilesDir != filepath.Join(rpDir, "files") || rp.CLIBackupPath != filepath.Join(rpDir, "bobres") {
		t.Fatalf("restore point %+v %v", rp, err)
	}

	// A folder others can write to is refused.
	if err := os.Chmod(rpDir, 0o777); err != nil { //nolint:gosec // the test's point
		t.Fatal(err)
	}
	ut.db.steps = nil
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 || !strings.Contains(ut.errOut.String(), "not a folder that only root can change") {
		t.Fatalf("got %d %s", code, ut.errOut)
	}
	if len(ut.db.steps) != 0 {
		t.Fatalf("steps %q", ut.db.steps)
	}
	// A link in its place is refused too.
	_ = os.Chmod(rpDir, 0o700) //nolint:gosec // a test directory
	if err := os.Rename(rpDir, rpDir+"-real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rpDir+"-real", rpDir); err != nil {
		t.Fatal(err)
	}
	if _, err := readRestorePoint(ut.dir); err == nil || !strings.Contains(err.Error(), "not trusting it") {
		t.Fatalf("symlink: %v", err)
	}
}

func TestChecksum(t *testing.T) {
	sums := []byte("# bobres-release v1\n# version 1.0.0\nABC123  bobres_linux_amd64\ndef456 *bobres_linux_arm64\n")
	for name, want := range map[string]string{"bobres_linux_amd64": "abc123", "bobres_linux_arm64": "def456", "bobres_darwin_arm64": ""} {
		got, ok := checksum(sums, name)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v", name, got, ok)
		}
	}
}

func TestSchemaVersions(t *testing.T) {
	ut := newUpdateTest(t, "")
	for _, c := range []struct {
		out  string
		want map[string]int64
		err  string
	}{
		{"core 20261005\npayments 0\n", map[string]int64{"core": 20261005, "payments": 0}, ""},
		{"", nil, "no migration tables"},
		{"core twelve\n", nil, "unexpected psql output"},
		{"WARNING: something\n", nil, "unexpected psql output"},
	} {
		ut.db.schema = c.out
		if c.out == "" {
			ut.db.schema = "\n"
		}
		got, err := ut.u.schemaVersions(context.Background(), ut.dir)
		if (c.err == "" && err != nil) || (c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err))) || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %v %v", c.out, got, err)
		}
	}
}

// An automatic rollback whose restore fails leaves the restore point unused,
// so `bobres rollback` can finish the job once the cause is fixed.
func TestUpdateRollbackFailsThenManualRollback(t *testing.T) {
	ut := newUpdateTest(t, "")
	envBefore := ut.read(t, ".env")
	ut.cli.do = ut.installsAndMigrates("1.3.0")
	ut.db.ps = func() string { return unhealthyCore }
	ut.db.restoreErr = errors.New("exit status 3")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(ut.errOut.String(), "The automatic rollback did not finish") || !strings.Contains(ut.errOut.String(), "run: sudo bobres rollback") {
		t.Fatalf("output: %s", ut.errOut)
	}
	if rp, _ := readRestorePoint(ut.dir); rp == nil || rp.UsedAt != nil {
		t.Fatalf("restore point: %+v", rp)
	}

	ut.db.restoreErr, ut.db.restored = nil, nil
	ut.db.ps = nil
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("rollback: %d\n%s", code, ut.errOut)
	}
	if len(ut.db.restored) != 1 || ut.db.restored[0] != "DUMP BEFORE THE UPDATE" || ut.read(t, ".env") != envBefore {
		t.Fatalf("restored %q", ut.db.restored)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI {
		t.Fatalf("CLI %q", b)
	}
	// Both backups taken before the rollbacks are kept, side by side.
	for _, d := range []time.Duration{0, time.Second} {
		if !exists(filepath.Join(ut.dir, "backups", safetyName(opsNow.Add(d), "before-rollback"))) {
			t.Errorf("backup of %v before the rollback missing", opsNow.Add(d))
		}
	}
}

// An install that hangs is stopped at the time limit, then rolled back.
func TestUpdateInstallTimeLimit(t *testing.T) {
	ut := newUpdateTest(t, "")
	envBefore := ut.read(t, ".env")
	ut.u.installTimeout = 50 * time.Millisecond
	ut.cli.hang = true
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d: %s", code, ut.errOut)
	}
	if !strings.Contains(ut.errOut.String(), "the install of 1.3.0 did not finish within 50ms: it was stopped, with the docker compose it ran. Rolling back to 1.2.3") {
		t.Fatalf("output: %s", ut.errOut)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI || ut.read(t, ".env") != envBefore {
		t.Fatalf("not rolled back: CLI %q", b)
	}
	if contains(ut.db.steps, "restore") { // no migration ran: the data stays
		t.Fatalf("steps %q", ut.db.steps)
	}
}

// Ctrl+C reaches only bobres (the install has its own process group): the
// install is stopped and the update rolls back.
func TestUpdateInterruptedInstallRollsBack(t *testing.T) {
	ut := newUpdateTest(t, "")
	sigs := make(chan os.Signal, 1)
	stopped := false
	ut.u.interrupts = func() (<-chan os.Signal, func()) { return sigs, func() { stopped = true } }
	ut.cli.hang, ut.cli.started = true, make(chan struct{})
	go func() {
		<-ut.cli.started
		sigs <- os.Interrupt
	}()
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 {
		t.Fatalf("exit %d: %s", code, ut.errOut)
	}
	if !strings.Contains(ut.errOut.String(), "the install of 1.3.0 was interrupted") || !stopped {
		t.Fatalf("stopped %v, output: %s", stopped, ut.errOut)
	}
	if b, _ := os.ReadFile(ut.exe); string(b) != oldCLI {
		t.Fatal("the CLI was not swapped back")
	}
}

// runInGroup kills the whole process group at the deadline: here a script
// and the process it left in the background, as docker compose would be.
func TestRunInGroupKillsTheGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "bobres")
	body := "#!/bin/sh\nsleep 60 &\necho $! > '" + pidFile + "'\necho started\nwait\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // a test script is executable
		t.Fatal(err)
	}
	// The deadline comes once the background process runs.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer cancel()
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if b, _ := os.ReadFile(pidFile); strings.HasSuffix(string(b), "\n") { //nolint:gosec // the test's own file
				return
			}
		}
	}()
	var out bytes.Buffer
	runErr := runInGroup(ctx, &out, &out, script)
	if runErr == nil {
		t.Fatalf("the run ended on its own: %s", out.String())
	}
	b, err := os.ReadFile(pidFile)
	pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || perr != nil || !strings.Contains(out.String(), "started") {
		t.Fatalf("the script did not start (%v): %v %v %q", runErr, err, perr, out.String())
	}
	for deadline := time.Now().Add(5 * time.Second); processAlive(pid); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the background process %d outlived the kill", pid)
		}
	}
}

// processAlive reports whether pid runs (a zombie waiting for its new parent
// to reap it does not count).
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // a test's own child
	if err != nil {
		return len(out) > 0
	}
	st := strings.TrimSpace(string(out))
	return st != "" && !strings.HasPrefix(st, "Z")
}

// installsNewLayout is installs as the new installer does it: besides the
// version it writes caddy/Caddyfile, and it leaves the top-level Caddyfile
// (if there is one) as it was.
func installsNewLayout(dir, ver string) func() error {
	write := installs(dir, ver)
	return func() error {
		if err := write(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dir, "caddy"), 0o755); err != nil { //nolint:gosec // as the installer makes it
			return err
		}
		return os.WriteFile(filepath.Join(dir, "caddy", "Caddyfile"), []byte("caddy of "+ver+"\n"), 0o644) //nolint:gosec // as the installer writes it
	}
}

// filesUnder lists the regular files below root, as paths relative to it.
func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			rel, rerr := filepath.Rel(root, path)
			out, err = append(out, rel), rerr
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// The layout change: an install with Caddy's file at the top level updates to
// one with caddy/Caddyfile and rolls back to the first. The restore point
// holds the old layout; the rollback puts it back, leaves the new files where
// they are (the old compose ignores them), and the top-level Caddyfile the old
// compose mounts is there the whole time.
func TestUpdateAndRollbackAcrossTheLayoutChange(t *testing.T) {
	for _, migrates := range []bool{false, true} {
		name := "no migration"
		if migrates {
			name = "migration"
		}
		t.Run(name, func(t *testing.T) {
			ut := newUpdateTest(t, "")
			envBefore, composeBefore, topBefore := ut.read(t, ".env"), ut.read(t, "docker-compose.yml"), ut.read(t, "Caddyfile")
			ut.cli.do = installsNewLayout(ut.dir, "1.3.0")
			if migrates {
				ut.cli.do = func() error {
					ut.db.schema, ut.db.dump = migratedSchema, "DUMP WRITTEN BY 1.3.0"
					return installsNewLayout(ut.dir, "1.3.0")()
				}
			}
			if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("update: %d\n%s\n%s", code, ut.out, ut.errOut)
			}
			point := filepath.Join(ut.dir, ".restore-point", "files")
			if got, want := strings.Join(filesUnder(t, point), " "), ".env Caddyfile docker-compose.yml postgres-init.sh"; got != want {
				t.Fatalf("the restore point holds %q, want the old layout %q", got, want)
			}
			if ut.read(t, "Caddyfile") != topBefore || ut.read(t, "caddy/Caddyfile") != "caddy of 1.3.0\n" {
				t.Fatalf("after the update: top %q, caddy/ %q", ut.read(t, "Caddyfile"), ut.read(t, "caddy/Caddyfile"))
			}

			ut.db.steps, ut.db.dump = nil, "DUMP AFTER THE UPDATE"
			if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("rollback: %d\n%s\n%s", code, ut.out, ut.errOut)
			}
			if ut.read(t, ".env") != envBefore || ut.read(t, "docker-compose.yml") != composeBefore || ut.read(t, "Caddyfile") != topBefore {
				t.Errorf("the old install files are not back:\n.env %q\ncompose %q\nCaddyfile %q", ut.read(t, ".env"), ut.read(t, "docker-compose.yml"), ut.read(t, "Caddyfile"))
			}
			if ut.read(t, "caddy/Caddyfile") != "caddy of 1.3.0\n" {
				t.Errorf("the new layout's file was removed or changed: %q", ut.read(t, "caddy/Caddyfile"))
			}
			if got := ut.db.steps[len(ut.db.steps)-1]; got != "up -d --remove-orphans" {
				t.Errorf("the old stack is started without the tls-agent that is no more: steps %q", ut.db.steps)
			}
		})
	}
}

// An install that already has caddy/Caddyfile and no top-level one stages
// both directories' files and gets caddy/Caddyfile back, without a top-level
// Caddyfile appearing.
func TestUpdateStagesTheNewLayout(t *testing.T) {
	ut := newUpdateTest(t, "")
	if err := os.Remove(filepath.Join(ut.dir, "Caddyfile")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(ut.dir, "caddy"), 0o755); err != nil { //nolint:gosec // as the installer makes it
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ut.dir, "caddy", "Caddyfile"), []byte("caddy of 1.2.3\n"), 0o644); err != nil { //nolint:gosec // as the installer writes it
		t.Fatal(err)
	}
	ut.cli.do = installsNewLayout(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("update: %d\n%s\n%s", code, ut.out, ut.errOut)
	}
	point := filepath.Join(ut.dir, ".restore-point", "files")
	if got, want := strings.Join(filesUnder(t, point), " "), ".env caddy/Caddyfile docker-compose.yml postgres-init.sh"; got != want {
		t.Fatalf("the restore point holds %q, want %q", got, want)
	}
	if b, _ := os.ReadFile(filepath.Join(point, "caddy", "Caddyfile")); string(b) != "caddy of 1.2.3\n" {
		t.Errorf("the staged caddy/Caddyfile: %q", b)
	}
	if st, err := os.Stat(filepath.Join(point, "caddy", "Caddyfile")); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("the staged file's mode: %v %v", st, err)
	}

	ut.db.steps, ut.db.dump = nil, "DUMP AFTER THE UPDATE"
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("rollback: %d\n%s\n%s", code, ut.out, ut.errOut)
	}
	if ut.read(t, "caddy/Caddyfile") != "caddy of 1.2.3\n" {
		t.Errorf("caddy/Caddyfile after the rollback: %q", ut.read(t, "caddy/Caddyfile"))
	}
	if lexists(filepath.Join(ut.dir, "Caddyfile")) {
		t.Error("a top-level Caddyfile appeared")
	}
	if st, err := os.Stat(filepath.Join(ut.dir, "caddy", "Caddyfile")); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("the restored file's mode: %v %v", st, err)
	}
}

// A rollback puts back every file in the restore point, whatever this bobres
// lists in installFiles: the point may have been made by another version's
// CLI, which listed other files.
func TestRollbackPutsBackEveryFileOfTheRestorePoint(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.cli.do = ut.installsAndMigrates("1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("update: %d\n%s\n%s", code, ut.out, ut.errOut)
	}
	point := filepath.Join(ut.dir, ".restore-point", "files")
	if err := os.MkdirAll(filepath.Join(point, "extra", "deeper"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(point, "extra", "deeper", "other.conf"), []byte("from another version\n"), 0o640); err != nil { //nolint:gosec // a fixture
		t.Fatal(err)
	}
	// A link in the point is not followed.
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("OUTSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, outside, filepath.Join(point, "linked.conf"))

	ut.db.steps, ut.db.dump = nil, "DUMP AFTER THE UPDATE"
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("rollback: %d\n%s\n%s", code, ut.out, ut.errOut)
	}
	if b, err := os.ReadFile(filepath.Join(ut.dir, "extra", "deeper", "other.conf")); err != nil || string(b) != "from another version\n" {
		t.Errorf("the extra file is not back: %q %v", b, err)
	}
	if st, err := os.Stat(filepath.Join(ut.dir, "extra", "deeper", "other.conf")); err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("the extra file's mode: %v %v", st, err)
	}
	if lexists(filepath.Join(ut.dir, "linked.conf")) {
		t.Error("a link in the restore point was put back")
	}
}

// copyFiles never goes through a link to make a directory, and a file it
// copies keeps its mode.
func TestCopyFilesMakesDirectoriesButNotThroughLinks(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "files")
	mustMkdir(t, filepath.Join(src, "caddy"))
	if err := os.WriteFile(filepath.Join(src, "caddy", "Caddyfile"), []byte("new"), 0o644); err != nil { //nolint:gosec // a fixture
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".env"), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFiles(src, dst, installFiles); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{".env": 0o600, "caddy/Caddyfile": 0o644} {
		st, err := os.Stat(filepath.Join(dst, name))
		if err != nil || st.Mode().Perm() != mode {
			t.Errorf("%s: %v %v, want mode %v", name, st, err, mode)
		}
	}
	if st, err := os.Stat(filepath.Join(dst, "caddy")); err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
		t.Errorf("the directory made for it: %v %v (want the source directory's mode)", st, err)
	}
	if lexists(filepath.Join(dst, "Caddyfile")) || lexists(filepath.Join(dst, "docker-compose.yml")) {
		t.Error("a file the source does not have was made")
	}

	// A link where the directory should be is refused, and nothing goes through it.
	out := t.TempDir()
	dst2 := t.TempDir()
	mustSymlink(t, out, filepath.Join(dst2, "caddy"))
	if err := copyFiles(src, dst2, installFiles); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("copy through a link: %v", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("the copy went through the link: %v", entries)
	}
}
