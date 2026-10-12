package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

const (
	// restorePointDir is the last update's restore point, in the install
	// directory: restorePointName, the backup of the database taken before the
	// update, the install files from then (in restorePointFiles) and the
	// previous CLI (restorePointCLI). It is root's alone and outside backups/:
	// the backup service's user controls that folder, and operators copy it
	// off the server, which must never carry .env (with the backup key) in
	// the clear.
	restorePointDir   = ".restore-point"
	restorePointName  = "restore-point.json"
	restorePointFiles = "files"
	restorePointCLI   = "bobres"
	// maxMetaSize bounds each release metadata file, as release.Checker does.
	maxMetaSize = 256 << 10
	// maxCLISize bounds the downloaded bobres binary.
	maxCLISize = 256 << 20
	// minUpdateGB is the free disk an update needs: the backup and the new
	// images.
	minUpdateGB = 2
	// installLimit bounds the new CLI's install run (pulling the images,
	// starting, its own five-minute health wait).
	installLimit = 20 * time.Minute
)

var (
	errInstallTimeout = errors.New("the install took too long")
	errInterrupted    = errors.New("interrupted")
)

// installFiles are the files `bobres install` writes that an update changes
// and a rollback puts back (.env carries BOBRES_VERSION).
var installFiles = []string{".env", "docker-compose.yml", "Caddyfile", "postgres-init.sh"}

// restorePoint is what `bobres update` leaves for `bobres rollback`.
type restorePoint struct {
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	// BackupFile is the backup of the database taken before the update.
	BackupFile string    `json:"backup_file"`
	CreatedAt  time.Time `json:"created_at"`
	// CLIPath is the bobres binary the update replaced; CLIBackupPath is a
	// copy of the previous one, in the restore point. (bobres.prev next to
	// CLIPath is for the operator: the next update replaces it.)
	CLIPath       string `json:"cli_path"`
	CLIBackupPath string `json:"cli_backup_path"`
	// FilesDir holds copies of installFiles from before the update.
	FilesDir string `json:"files_dir"`
	// SchemaVersions are the database's migration versions before the update
	// (nil: not known), so a rollback restores the database only when
	// migrations ran.
	SchemaVersions map[string]int64 `json:"schema_versions,omitempty"`
	UsedAt         *time.Time       `json:"used_at,omitempty"`
}

// updater holds what update and rollback reach beyond Docker: the release
// files on GitHub, the bobres binary on disk, install.sh's state and the new
// binary's install run, each replaceable in tests.
type updater struct {
	*ops
	current    string              // this CLI's version
	latestURL  string              // the latest release's files
	versionURL func(string) string // one release's files
	hc         *http.Client
	roots      []string // the release root keys
	platform   string   // the CLI asset's suffix, e.g. linux_amd64
	// stateDir is where install.sh records the last verified key list and
	// release (keys.seq, release.version); "" for none.
	stateDir string
	// exe is the running bobres binary, symlinks resolved.
	exe func() (string, error)
	// runCLI runs a bobres binary with its output going to the user. When
	// ctx ends, the binary and everything it started are killed.
	runCLI func(ctx context.Context, exe string, args ...string) error
	// installTimeout bounds the install run (installLimit).
	installTimeout time.Duration
	// interrupts delivers Ctrl+C, SIGTERM and SIGHUP while the install runs,
	// until stop is called.
	interrupts func() (c <-chan os.Signal, stop func())
}

func newUpdater(o *ops) *updater {
	return &updater{
		ops: o, current: version.Version,
		latestURL: release.DefaultBaseURL, versionURL: release.DownloadURL,
		hc:    &http.Client{}, // fetch sets each download's time limit
		roots: release.Roots, platform: runtime.GOOS + "_" + runtime.GOARCH,
		stateDir: defaultStateDir(),
		exe: func() (string, error) {
			p, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.EvalSymlinks(p)
		},
		runCLI: func(ctx context.Context, exe string, args ...string) error {
			return runInGroup(ctx, o.out, o.errOut, exe, args...)
		},
		installTimeout: installLimit,
		interrupts: func() (<-chan os.Signal, func()) {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			return c, func() { signal.Stop(c) }
		},
	}
}

// runInGroup runs a program in a process group of its own, its output going
// to out and errOut and no stdin (install --yes never asks). When ctx ends,
// the whole group is killed: the program and what it started, docker compose
// included, which could otherwise go on recreating containers after a
// rollback.
func runInGroup(ctx context.Context, out, errOut io.Writer, exe string, args ...string) error {
	cmd := exec.CommandContext(ctx, exe, args...) //nolint:gosec // our own binary, arguments built here
	cmd.Stdout, cmd.Stderr = out, errOut
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// A killed group can leave the output pipe open in a stray process.
	cmd.WaitDelay = 10 * time.Second
	return cmd.Run()
}

// defaultStateDir is install.sh's state directory: $BOBRES_STATE_DIR, else
// /var/lib/bobres.
func defaultStateDir() string {
	if d := strings.TrimSpace(os.Getenv("BOBRES_STATE_DIR")); d != "" {
		return d
	}
	return "/var/lib/bobres"
}

// updateCmd: `bobres update [--check] [--version X] [--yes] [--dir D]`.
// --check only prints whether a newer verified release exists. Otherwise it
// verifies the release, takes a backup, leaves a restore point, swaps in the
// new CLI, runs its install and waits until healthy; when that fails it
// rolls back on its own. Exit 0 when done (or nothing to do), 1 when refused
// or failed, 2 for usage errors.
func (u *updater) updateCmd(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(u.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	check := fs.Bool("check", false, "only check for a newer release (signature verified) and print it")
	ver := fs.String("version", "", "update to this release (default: the latest)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(u.errOut, "usage: bobres update [--check] [--version X] [--yes] [--dir D]")
		return 2
	}
	want := strings.TrimPrefix(strings.TrimSpace(*ver), "v")
	if want != "" && !release.Valid(want) {
		fmt.Fprintf(u.errOut, "error: --version must be a release version like 1.2.3 (got %q)\n", *ver)
		return 2
	}
	base := u.latestURL
	if want != "" {
		base = u.versionURL(want)
	}
	ctx := context.Background()
	if *check {
		return u.check(ctx, *dir, base, want)
	}
	env, ok := u.requireInstall(*dir)
	if !ok {
		return 2
	}
	if !release.Valid(u.current) {
		fmt.Fprintf(u.errOut, "error: this bobres is a development build (%s): it does not replace itself with a release.\n"+
			"Install the released CLI with install.sh (see the README), or move the images with: bobres install --version X\n", u.current)
		return 1
	}
	from := u.installed(env)

	fmt.Fprintln(u.out, "Checking the release signature...")
	t, err := u.verify(ctx, base, want)
	if err != nil {
		fmt.Fprintf(u.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	// As install.sh: never below a release this server installed before
	// (a replayed old release, even with this install's .env behind).
	floor, err := u.lastRelease()
	if err != nil {
		fmt.Fprintf(u.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	if floor != "" && release.Compare(t.Version, floor) < 0 {
		fmt.Fprintf(u.errOut, "error: release %s is older than release %s, which this server installed before: refusing a downgrade.\n"+
			"To return to the version before the last update: bobres rollback\n", t.Version, floor)
		return 1
	}
	switch c := release.Compare(t.Version, from); {
	case c < 0:
		fmt.Fprintf(u.errOut, "error: release %s is older than this install (%s): update never goes back.\n"+
			"To return to the version before the last update: bobres rollback\n", t.Version, from)
		return 1
	case c == 0:
		fmt.Fprintf(u.out, "BOBRES already runs %s: nothing to update.\n", from)
		return 0
	}
	if _, ok := checksum(t.sums, "bobres_"+u.platform); !ok {
		fmt.Fprintf(u.errOut, "error: release %s has no bobres_%s (no CLI for this system). Nothing was changed.\n", t.Version, u.platform)
		return 1
	}
	// The update's own check: room for the backup and the new images. Those
	// of bobres install (10 GB, RAM, ports, Telegram) are for a new server
	// and are skipped below.
	for _, p := range []string{*dir, "/var/lib/docker"} {
		if gb := u.freeGB(p); gb >= 0 && gb < minUpdateGB {
			fmt.Fprintf(u.errOut, "error: %.1f GB free in %s: an update needs at least %d GB there (the backup and the new images).\n"+
				"Free some space and try again. Nothing was changed.\n", gb, p, minUpdateGB)
			return 1
		}
	}
	exe, err := u.exe()
	if err != nil {
		fmt.Fprintf(u.errOut, "error: cannot find the bobres binary: %v. Nothing was changed.\n", err)
		return 1
	}
	if !*yes && !u.confirm(fmt.Sprintf("Update BOBRES from %s to %s? A backup is taken first, and the update rolls back\n"+
		"on its own if the store does not come back healthy. [y/N]: ", from, t.Version)) {
		fmt.Fprintln(u.out, "Cancelled. Nothing was changed.")
		return 1
	}
	fmt.Fprintf(u.out, "Downloading bobres %s...\n", t.Version)
	bin, err := u.downloadCLI(ctx, t)
	if err != nil {
		fmt.Fprintf(u.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	return u.apply(ctx, *dir, env, from, t, exe, bin)
}

// installed is the version the install runs (its images); the CLI's own
// when .env does not name a release.
func (u *updater) installed(env map[string]string) string {
	if v := env["BOBRES_VERSION"]; release.Valid(v) {
		return strings.TrimPrefix(v, "v")
	}
	return imageTag(u.current)
}

// check prints the current version, the release's and whether it is newer.
// It needs no install and works on development builds.
func (u *updater) check(ctx context.Context, dir, base, want string) int {
	_, code := u.checkNewer(ctx, dir, base, want)
	return code
}

// checkNewer is check, also saying whether the release is newer than the
// install.
func (u *updater) checkNewer(ctx context.Context, dir, base, want string) (newer bool, code int) {
	current := imageTag(u.current)
	if env, ok, err := readEnv(filepath.Join(dir, ".env")); err == nil && ok && env["BOBRES_VERSION"] != "" {
		current = env["BOBRES_VERSION"]
	}
	t, err := u.verify(ctx, base, want)
	if err != nil {
		fmt.Fprintf(u.errOut, "error: %v\n", err)
		return false, 1
	}
	label := "Latest: "
	if want != "" {
		label = "Release:"
	}
	fmt.Fprintf(u.out, "Current: %s (bobres CLI %s)\n%s %s (%s, signature verified)\n", current, u.current, label, t.Version, orDash(t.Channel))
	switch {
	case !release.Valid(current):
		fmt.Fprintf(u.out, "Newer:   unknown (this install runs %q, not a release version)\n", current)
	case release.Compare(t.Version, current) > 0:
		fmt.Fprintln(u.out, "Newer:   yes. Update with: sudo bobres update")
		return true, 0
	default:
		fmt.Fprintln(u.out, "Newer:   no, this install is up to date.")
	}
	return false, 0
}

// target is a release whose signature checked out.
type target struct {
	release.Release
	base string // where its files are
	sums []byte // its verified checksums.txt
}

var errNoFile = errors.New("not found")

// verify fetches a release's metadata from base and checks it the way
// install.sh does: an unsigned, tampered, revoked or expired release is an
// error, and so is a key list older than the last one this server saw
// (keys.seq: an old list may still name a key revoked since). With want,
// the release must be that version.
func (u *updater) verify(ctx context.Context, base, want string) (*target, error) {
	sums, err := u.fetch(ctx, base, "checksums.txt", maxMetaSize)
	if errors.Is(err, errNoFile) {
		if want != "" {
			return nil, fmt.Errorf("there is no release %s", want)
		}
		return nil, errors.New("no BOBRES release has been published yet")
	}
	if err != nil {
		return nil, err
	}
	sig, err := u.fetch(ctx, base, "checksums.txt.sig", maxMetaSize)
	if errors.Is(err, errNoFile) {
		return nil, errors.New("the release is not signed (no checksums.txt.sig): refusing it")
	}
	if err != nil {
		return nil, err
	}
	keys, err := u.fetch(ctx, base, "keys.txt", maxMetaSize)
	if errors.Is(err, errNoFile) {
		return nil, errors.New("the release has no keys.txt: refusing it")
	}
	if err != nil {
		return nil, err
	}
	var keySigs [][]byte
	for n := 1; n <= 4; n++ { // as many as install.sh looks for
		s, err := u.fetch(ctx, base, "keys.txt.sig."+strconv.Itoa(n), maxMetaSize)
		switch {
		case err == nil:
			keySigs = append(keySigs, s)
		case !errors.Is(err, errNoFile):
			return nil, err
		}
	}
	rel, err := release.Verify(u.roots, keys, keySigs, sums, sig, u.now())
	if err != nil {
		return nil, fmt.Errorf("the release did not pass the signature check: %w", err)
	}
	seen, err := u.lastKeysSeq()
	if err != nil {
		return nil, err
	}
	if rel.KeysSeq < seen {
		return nil, fmt.Errorf("the release's key list (seq %d) is older than the one this server has seen (seq %d): refusing it (a replayed old key list?)", rel.KeysSeq, seen)
	}
	if want != "" && rel.Version != want {
		return nil, fmt.Errorf("the files of release %s are signed for %s: refusing them", want, rel.Version)
	}
	return &target{Release: *rel, base: base, sums: sums}, nil
}

// readState reads one of install.sh's state files ("" when it has none).
func (u *updater) readState(name string) (string, error) {
	if u.stateDir == "" {
		return "", nil
	}
	b, err := os.ReadFile(filepath.Join(u.stateDir, name)) //nolint:gosec // install.sh's state, root's
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the release state: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// lastKeysSeq is the key list sequence number recorded last (0 for none;
// as in install.sh, a file that is not a number counts as 0).
func (u *updater) lastKeysSeq() (int, error) {
	s, err := u.readState("keys.seq")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, nil
	}
	return n, nil
}

// lastRelease is the release recorded last ("" for none).
func (u *updater) lastRelease() (string, error) {
	s, err := u.readState("release.version")
	if err != nil {
		return "", err
	}
	s = strings.TrimPrefix(s, "v")
	if !release.Valid(s) {
		return "", nil
	}
	return s, nil
}

// recordRelease writes what install.sh writes once it has installed a CLI:
// the key list's sequence number and the release, each never lowered (they
// are the floor later releases are checked against).
func (u *updater) recordRelease(t *target) error {
	if u.stateDir == "" {
		return nil
	}
	seq, err := u.lastKeysSeq()
	if err != nil {
		return err
	}
	ver, err := u.lastRelease()
	if err != nil {
		return err
	}
	if ver == "" || release.Compare(t.Version, ver) > 0 {
		ver = t.Version
	}
	if err := os.MkdirAll(u.stateDir, 0o755); err != nil { //nolint:gosec // as install.sh makes it
		return err
	}
	if err := writeAtomic(filepath.Join(u.stateDir, "keys.seq"), []byte(strconv.Itoa(max(seq, t.KeysSeq))+"\n"), 0o644); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(u.stateDir, "release.version"), []byte(ver+"\n"), 0o644)
}

// fetch downloads one release file, at most max bytes. A metadata file gets
// a minute (a filtered network tends to hang rather than refuse), the
// binary longer.
func (u *updater) fetch(ctx context.Context, base, name string, max int64) ([]byte, error) {
	limit := time.Minute
	if max > maxMetaSize {
		limit = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/"+name, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w (is GitHub reachable from this server?)", name, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read below
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("download %s: %w", name, errNoFile)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("download %s: HTTP %d", name, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", name, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("download %s: larger than %d bytes", name, max)
	}
	return data, nil
}

// checksum is the SHA-256 a verified checksums.txt lists for name.
func checksum(sums []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] != "#" && (f[1] == name || f[1] == "*"+name) {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// downloadCLI fetches the release's bobres binary for this system and checks
// it against the verified checksums.txt.
func (u *updater) downloadCLI(ctx context.Context, t *target) ([]byte, error) {
	name := "bobres_" + u.platform
	want, ok := checksum(t.sums, name)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s (no CLI for this system)", t.Version, name)
	}
	bin, err := u.fetch(ctx, t.base, name, maxCLISize)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bin)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("checksum mismatch for %s (expected %s, got %s)", name, want, got)
	}
	return bin, nil
}

// apply runs the update once the release, the binary and the operator said
// yes: restore point (staged), CLI swap, restore point in place, install,
// health; a rollback when the install or the health check fails.
func (u *updater) apply(ctx context.Context, dir string, env map[string]string, from string, t *target, exe string, bin []byte) int {
	rp, err := u.stageRestorePoint(ctx, dir, env, from, t.Version, exe)
	if err != nil {
		fmt.Fprintf(u.errOut, "error: %v. Nothing was changed.\n", err)
		return 1
	}
	active := restorePointPath(dir)
	// The CLI first, then the restore point: when either fails, the previous
	// update's restore point is still the current one.
	if err := swapCLI(exe, bin); err != nil {
		_ = os.RemoveAll(active + ".new")
		fmt.Fprintf(u.errOut, "error: replace %s: %v. Nothing was changed.\n", exe, err)
		return 1
	}
	if err := commitRestorePoint(dir); err != nil {
		_ = os.RemoveAll(active + ".new")
		if uerr := os.Rename(exe+".prev", exe); uerr != nil {
			fmt.Fprintf(u.errOut, "error: save the restore point: %v; and the previous bobres was not put back (%v): it is %s.prev\n", err, uerr, exe)
			return 1
		}
		fmt.Fprintf(u.errOut, "error: save the restore point: %v. Nothing was changed.\n", err)
		return 1
	}
	if err := u.recordRelease(t); err != nil {
		fmt.Fprintf(u.errOut, "warning: the release state in %s was not updated: %v\n", u.stateDir, err)
	}
	fmt.Fprintf(u.out, "Restore point saved in %s (the database as of %s, the install files and the previous bobres).\n"+
		"bobres %s installed (the previous one is also kept as %s.prev).\nInstalling BOBRES %s...\n",
		active, rp.CreatedAt.Format("2006-01-02 15:04 MST"), t.Version, exe, t.Version)
	// An existing install: bobres install's checks were for a new server (10
	// GB free, RAM, ports), and its Telegram check goes straight to
	// api.telegram.org, which a filtered network reaches only through
	// BOBRES_TELEGRAM_API_URL.
	if err := u.install(ctx, exe, dir, t.Version); err != nil {
		return u.autoRollback(ctx, dir, rp, err.Error())
	}
	newEnv, _, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil {
		return u.autoRollback(ctx, dir, rp, fmt.Sprintf("read .env: %v", err))
	}
	if waitHealthy(ctx, u.run, u.sleep, dir, appServices(newEnv), u.out, u.errOut) != 0 {
		return u.autoRollback(ctx, dir, rp, fmt.Sprintf("BOBRES %s did not become healthy", t.Version))
	}
	// The update holds: the restore point of the one before is spent.
	if err := os.RemoveAll(active + ".prev"); err != nil {
		fmt.Fprintf(u.errOut, "warning: %v\n", err)
	}
	fmt.Fprintf(u.out, "\nBOBRES is updated to %s.\nIf something is wrong with it: sudo bobres rollback   (back to %s, with the database as of %s)\n",
		t.Version, from, rp.CreatedAt.Format("2006-01-02 15:04 MST"))
	return 0
}

// install runs the new CLI's install of version, at most installTimeout. Its
// own process group does not get the terminal's Ctrl+C, so an interrupt
// (or SIGTERM, or a hang-up) ends it here, like the time limit; either way
// the whole group is killed and the update rolls back.
func (u *updater) install(ctx context.Context, exe, dir, version string) error {
	limit := u.installTimeout
	if limit <= 0 {
		limit = installLimit
	}
	ictx, interrupt := context.WithCancelCause(ctx)
	defer interrupt(nil)
	ictx, cancel := context.WithTimeoutCause(ictx, limit, errInstallTimeout)
	defer cancel()
	if u.interrupts != nil {
		sigs, stop := u.interrupts()
		defer stop()
		go func() {
			select {
			case <-sigs:
				interrupt(errInterrupted)
			case <-ictx.Done():
			}
		}()
	}
	err := u.runCLI(ictx, exe, "install", "--version", version, "--dir", dir, "--quiet", "--yes", "--skip-checks", "--offline")
	if err == nil {
		return nil
	}
	switch cause := context.Cause(ictx); {
	case errors.Is(cause, errInstallTimeout):
		return fmt.Errorf("the install of %s did not finish within %s: it was stopped, with the docker compose it ran", version, limit)
	case errors.Is(cause, errInterrupted):
		return fmt.Errorf("the install of %s was interrupted: it was stopped, with the docker compose it ran", version)
	}
	return fmt.Errorf("the install of %s failed (%w)", version, err)
}

// stageRestorePoint builds the update's restore point beside the current
// one, in <dir>/.restore-point.new: the backup of the database, its
// migration versions, the install files and a copy of the running CLI. The
// current one is not touched (commitRestorePoint puts this one in its place).
func (u *updater) stageRestorePoint(ctx context.Context, dir string, env map[string]string, from, to, exe string) (*restorePoint, error) {
	if len(env["BOBRES_BACKUP_KEY"]) < backup.MinKeyLen {
		return nil, errors.New("the backup before the update failed: this install has no backup key (BOBRES_BACKUP_KEY): run `bobres install` once to create it")
	}
	active := restorePointPath(dir)
	stage := active + ".new"
	if err := os.RemoveAll(stage); err != nil { // left by an update cut short
		return nil, err
	}
	if err := os.Mkdir(stage, 0o700); err != nil {
		return nil, err
	}
	fail := func(err error) (*restorePoint, error) {
		_ = os.RemoveAll(stage)
		return nil, err
	}
	versions, err := u.schemaVersions(ctx, dir)
	if err != nil {
		fmt.Fprintf(u.errOut, "warning: the database's migration versions were not read (%v): a rollback of this update restores the database in any case.\n", err)
	}
	created := u.now().UTC().Truncate(time.Second)
	name := backup.FileName(created)
	fmt.Fprintln(u.out, "Backing up the current database first...")
	if _, _, err := u.backupTo(ctx, dir, stage, name, env, -1); err != nil {
		return fail(fmt.Errorf("the backup before the update failed: %w", err))
	}
	rp := &restorePoint{
		FromVersion: from, ToVersion: to, BackupFile: filepath.Join(active, name), CreatedAt: created,
		CLIPath: exe, CLIBackupPath: filepath.Join(active, restorePointCLI),
		FilesDir: filepath.Join(active, restorePointFiles), SchemaVersions: versions,
	}
	if err := copyFiles(dir, filepath.Join(stage, restorePointFiles)); err != nil {
		return fail(fmt.Errorf("save the install files: %w", err))
	}
	cli, err := os.ReadFile(exe) //nolint:gosec // the running binary
	if err == nil {
		err = writeAtomic(filepath.Join(stage, restorePointCLI), cli, 0o700)
	}
	if err != nil {
		return fail(fmt.Errorf("save the current bobres: %w", err))
	}
	if err := writeRestorePoint(stage, rp); err != nil {
		return fail(fmt.Errorf("write the restore point: %w", err))
	}
	return rp, nil
}

// commitRestorePoint puts the staged restore point in place. The current one
// is set aside as .restore-point.prev: it comes back when this update is
// undone (retire) and goes once the update holds.
func commitRestorePoint(dir string) error {
	active := restorePointPath(dir)
	prev := active + ".prev"
	if err := os.RemoveAll(prev); err != nil {
		return err
	}
	had := lexists(active)
	if had {
		if err := os.Rename(active, prev); err != nil {
			return err
		}
	}
	if err := os.Rename(active+".new", active); err != nil {
		if had {
			_ = os.Rename(prev, active)
		}
		return err
	}
	_ = syncDir(dir) // the renames are done; this only makes them durable sooner
	return nil
}

// versionsScript prints the migration version (goose's highest version_id)
// of each service schema that has a version table, as "schema version"
// lines. It runs in the postgres container like dumpScript.
const versionsScript = `exec psql -X -A -t -q --no-password --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -c "` +
	`SELECT table_schema || ' ' || coalesce((xpath('/row/v/text()', query_to_xml(format(` +
	`'SELECT max(version_id) AS v FROM %I.goose_db_version', table_schema), false, true, '')))[1]::text, '0') ` +
	`FROM information_schema.tables WHERE table_name = 'goose_db_version' ` +
	`AND table_schema IN ('core', 'payments', 'provisioner') ORDER BY 1"`

// schemaVersions reads each service schema's migration version from the
// running database.
func (u *updater) schemaVersions(ctx context.Context, dir string) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	var out bytes.Buffer
	if err := u.pipe(ctx, dir, nil, &out, composeArgs("exec", "-T", "postgres", "sh", "-c", versionsScript)...); err != nil {
		return nil, fmt.Errorf("psql in the postgres container: %w", err)
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return nil, errors.New("the database has no migration tables")
	}
	v := map[string]int64{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("unexpected psql output %q", line)
		}
		n, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unexpected psql output %q", line)
		}
		v[f[0]] = n
	}
	return v, nil
}

// migrated says whether the database may have changed since the restore
// point in a way the previous version cannot read: its migration versions
// differ from the ones recorded then, or either is not known.
func (u *updater) migrated(ctx context.Context, dir string, rp *restorePoint) bool {
	if rp.SchemaVersions == nil {
		return true
	}
	now, err := u.schemaVersions(ctx, dir)
	if err != nil {
		fmt.Fprintf(u.errOut, "warning: the database's migration versions were not read (%v): it is restored to be safe.\n", err)
		return true
	}
	return !maps.Equal(now, rp.SchemaVersions)
}

// autoRollback undoes an update that failed, then says so. The database goes
// back to the restore point only when migrations ran (the previous version
// may not read it otherwise), after a backup of what it holds now; else it
// stays as it is, with every order the old version took while the new images
// were pulled. It always exits 1.
func (u *updater) autoRollback(ctx context.Context, dir string, rp *restorePoint, why string) int {
	fmt.Fprintf(u.errOut, "\nerror: %s. Rolling back to %s...\n", why, rp.FromVersion)
	restoreDB := u.migrated(ctx, dir, rp)
	code, settled := u.rollbackTo(ctx, dir, rp, restoreDB, "made by bobres update before it rolled back the update to "+rp.ToVersion)
	switch {
	case code != 0 && settled:
		fmt.Fprintf(u.errOut, "\nThe automatic rollback put back the install files and the data of %s, but the store did not start (see above).\n"+
			"Once fixed, start it with: sudo bobres start   (docs/runbooks/update-rollback.md)\n", rp.FromVersion)
		return 1
	case code != 0:
		fmt.Fprintf(u.errOut, "\nThe automatic rollback did not finish (see above). The data from before the update is in %s;\n"+
			"fix what failed and run: sudo bobres rollback   (docs/runbooks/update-rollback.md)\n", rp.BackupFile)
		return 1
	}
	data := "its data kept as it is (no migration had run)"
	if restoreDB {
		data = fmt.Sprintf("with the database as of %s (what was written since then is in the backup saved above)", rp.CreatedAt.Format("2006-01-02 15:04 MST"))
	}
	fmt.Fprintf(u.errOut, "\nThe update to %s did not go through: BOBRES runs %s again, %s.\n"+
		"Nothing needs to be done now; try again later, or report the messages above.\n", rp.ToVersion, rp.FromVersion, data)
	return 1
}

// rollbackTo returns the install to the restore point. With restoreDB: the
// app services stop, the current data is backed up (why goes in its record;
// when that fails nothing else changes and the services start again), then
// the previous CLI, the install files from before the update (BOBRES_VERSION
// with them) and the database from the restore point's backup come back.
// Without it: the previous CLI and the install files only. Then it starts and
// waits. settled says the files and the data are as they should be (only the
// start may have failed); the restore point is retired then.
func (u *updater) rollbackTo(ctx context.Context, dir string, rp *restorePoint, restoreDB bool, why string) (code int, settled bool) {
	cur, _, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil {
		fmt.Fprintf(u.errOut, "error: read .env: %v\n", err)
		return 1, false
	}
	if !restoreDB {
		u.putBackCLI(rp)
		if sameFiles(dir, rp.FilesDir) {
			fmt.Fprintf(u.out, "The install was not changed: the store kept running %s.\n", rp.FromVersion)
			u.retire(dir, rp)
			return 0, true
		}
		if err := copyFiles(rp.FilesDir, dir); err != nil {
			fmt.Fprintf(u.errOut, "error: put back the install files from %s: %v\n", rp.FilesDir, err)
			return 1, false
		}
		fmt.Fprintf(u.out, "The install files of %s are back; the database stays as it is (no migration ran).\n", rp.FromVersion)
		u.retire(dir, rp)
		code = u.startAndWait(ctx, dir, "--remove-orphans")
		u.healthHints(dir, code == 0, false)
		return code, true
	}

	// Stopped first, so that nothing is written after the backup is taken.
	if err := u.stopApp(ctx, dir, cur); err != nil {
		fmt.Fprintf(u.errOut, "error: %v. Nothing was changed; starting the services again...\n", err)
		u.startAndWait(ctx, dir)
		return 1, false
	}
	safety, err := u.safetyBackup(ctx, dir, cur, "before-rollback", why)
	if err != nil {
		fmt.Fprintf(u.errOut, "error: the backup of the current data failed: %v.\n"+
			"Nothing was changed (the database and the install files are as they were); starting the services again...\n", err)
		u.startAndWait(ctx, dir)
		return 1, false
	}
	fmt.Fprintf(u.out, "Current data saved in %s\n", filepath.Join(dir, "backups", safety))
	u.putBackCLI(rp)
	if err := copyFiles(rp.FilesDir, dir); err != nil {
		fmt.Fprintf(u.errOut, "error: put back the install files from %s: %v\n", rp.FilesDir, err)
		return 1, false
	}
	fmt.Fprintf(u.out, "The install files of %s are back.\n", rp.FromVersion)
	old, _, err := readEnv(filepath.Join(dir, ".env"))
	if err != nil {
		fmt.Fprintf(u.errOut, "error: read .env: %v\n", err)
		return 1, false
	}
	// The database must run (as the previous version had it) for the restore.
	if out, err := u.run(ctx, dir, composeArgs("up", "-d", "postgres", "redis")...); err != nil {
		fmt.Fprintf(u.errOut, "error: docker compose up postgres failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
		return 1, false
	}
	if waitHealthy(ctx, u.run, u.sleep, dir, []string{"postgres"}, io.Discard, u.errOut) != 0 {
		return 1, false
	}
	key, err := restoreKey(old, "")
	if err != nil {
		fmt.Fprintf(u.errOut, "error: %v\n", err)
		return 1, false
	}
	keys, err := u.loadDump(ctx, dir, rp.BackupFile, key)
	if err != nil {
		fmt.Fprintf(u.errOut, "error: the database was not restored: %v\nThe database is unchanged (one transaction). Retry with: bobres rollback\n", err)
		return 1, false
	}
	fmt.Fprintf(u.out, "Database restored to %s.\n", rp.CreatedAt.Format("2006-01-02 15:04 MST"))
	u.markUsed(dir, rp)
	u.retire(dir, rp)
	changed, err := applyMasterKeys(dir, keys, false)
	if err != nil {
		fmt.Fprintf(u.errOut, "error: .env could not take the backup's master keys: %v\n", err)
		return 1, true
	}
	if len(changed) > 0 {
		u.reportKeys(changed, false)
	}
	code = u.startAndWait(ctx, dir, "--remove-orphans")
	u.healthHints(dir, code == 0, false)
	return code, true
}

// putBackCLI writes the restore point's copy of the previous CLI over the
// bobres the update installed (the copy stays, so a rollback run again finds
// it) and drops bobres.prev, which held the same.
func (u *updater) putBackCLI(rp *restorePoint) {
	if cur, err := u.exe(); err == nil && cur != rp.CLIPath {
		fmt.Fprintf(u.errOut, "warning: the update replaced %s, not this bobres (%s): the CLI stays as it is.\n"+
			"The previous one is in %s\n", rp.CLIPath, cur, rp.CLIBackupPath)
		return
	}
	b, err := os.ReadFile(rp.CLIBackupPath) //nolint:gosec // root's restore point
	if err == nil {
		err = writeAtomic(rp.CLIPath, b, 0o755)
	}
	if err != nil {
		fmt.Fprintf(u.errOut, "warning: the previous bobres was not put back (%v): the bobres in %s stays.\n", err, rp.CLIPath)
		return
	}
	_ = os.Remove(rp.CLIPath + ".prev")
	fmt.Fprintf(u.out, "The previous bobres is back in %s.\n", rp.CLIPath)
}

// retire is called once an update is undone: its restore point has done its
// job. The restore point of the update before, set aside while this one ran,
// is the current one again. Without one, a restore point whose backup was
// restored stays (used), and one that changed nothing goes.
func (u *updater) retire(dir string, rp *restorePoint) {
	active := restorePointPath(dir)
	prev := active + ".prev"
	var err error
	switch {
	case lexists(prev):
		if err = os.RemoveAll(active); err == nil {
			err = os.Rename(prev, active)
		}
	case rp.UsedAt == nil:
		err = os.RemoveAll(active)
	}
	if err != nil {
		fmt.Fprintf(u.errOut, "warning: the restore points in %s were not put in order: %v\n", dir, err)
	}
}

// rollbackCmd: `bobres rollback [--yes] [--dir D]` undoes the last update
// with its restore point: the previous CLI and install files, and the
// database from the backup taken before the update (data since then is
// saved in a new backup first, with the services stopped).
func (u *updater) rollbackCmd(args []string) int {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	fs.SetOutput(u.errOut)
	dir := fs.String("dir", defaultDir(), dirUsage)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(u.errOut, "usage: bobres rollback [--yes] [--dir D]")
		return 2
	}
	if _, ok := u.requireInstall(*dir); !ok {
		return 2
	}
	rp, err := readRestorePoint(*dir)
	if err != nil {
		fmt.Fprintf(u.errOut, "error: %v\n", err)
		return 1
	}
	if rp == nil {
		fmt.Fprintf(u.errOut, "There is no restore point in %s: rollback undoes the last `bobres update`, which leaves one.\n"+
			"To go back to a backup instead: bobres restore <file>\n", restorePointPath(*dir))
		return 1
	}
	if rp.UsedAt != nil {
		fmt.Fprintf(u.errOut, "The restore point of the update from %s to %s was already used (%s).\n"+
			"To go back to a backup instead: bobres restore <file>\n", rp.FromVersion, rp.ToVersion, rp.UsedAt.Format("2006-01-02 15:04 MST"))
		return 1
	}
	// The backup must open before anything changes.
	old, _, err := readEnv(filepath.Join(rp.FilesDir, ".env"))
	if err != nil {
		fmt.Fprintf(u.errOut, "error: the restore point's files: %v\n", err)
		return 1
	}
	key, err := restoreKey(old, "")
	if err == nil {
		_, err = verifyBackupFile(rp.BackupFile, key)
	}
	if err != nil {
		fmt.Fprintf(u.errOut, "error: the restore point's backup %s: %v. Nothing was changed.\n", rp.BackupFile, err)
		return 1
	}
	fmt.Fprintf(u.out, "Restore point: the update from %s to %s on %s.\n", rp.FromVersion, rp.ToVersion, rp.CreatedAt.Format("2006-01-02 15:04 MST"))
	if !*yes && !u.confirm(fmt.Sprintf("Roll back to %s? The database goes back to %s: orders, payments and every change since then\n"+
		"are undone (they are saved in a backup first). [y/N]: ", rp.FromVersion, rp.CreatedAt.Format("2006-01-02 15:04 MST"))) {
		fmt.Fprintln(u.out, "Cancelled. Nothing was changed.")
		return 1
	}
	if code, _ := u.rollbackTo(context.Background(), *dir, rp, true, "made by bobres rollback before it undid the update to "+rp.ToVersion); code != 0 {
		fmt.Fprintln(u.errOut, "The rollback did not finish (see above); docs/runbooks/update-rollback.md says what to do.")
		return 1
	}
	fmt.Fprintf(u.out, "\nRolled back: BOBRES runs %s, with the database as of %s.\n"+
		"The data from before the rollback is in the backup saved above.\n", rp.FromVersion, rp.CreatedAt.Format("2006-01-02 15:04 MST"))
	return 0
}

// markUsed records that the restore point was used, so it is not used twice.
func (u *updater) markUsed(dir string, rp *restorePoint) {
	now := u.now().UTC().Truncate(time.Second)
	rp.UsedAt = &now
	if err := writeRestorePoint(restorePointPath(dir), rp); err != nil {
		fmt.Fprintf(u.errOut, "warning: the restore point was not marked as used: %v\n", err)
	}
}

// restorePointPath is the install's restore point folder.
func restorePointPath(dir string) string {
	return filepath.Join(dir, restorePointDir)
}

// readRestorePoint reads the install's restore point (nil when there is
// none). It trusts only a folder that root alone can change, and takes the
// backup and the install files from that folder, whatever the record says.
func readRestorePoint(dir string) (*restorePoint, error) {
	active := restorePointPath(dir)
	if prev := active + ".prev"; !lexists(active) && lexists(prev) {
		// An update cut short between setting the current one aside and
		// putting its own in place.
		if err := os.Rename(prev, active); err != nil {
			return nil, err
		}
	}
	st, err := os.Lstat(active)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode().Perm()&0o022 != 0 || !ownedByRoot(st) {
		return nil, fmt.Errorf("%s is not a folder that only root can change: not trusting it (fix its owner and mode, or remove it)", active)
	}
	path := filepath.Join(active, restorePointName)
	b, err := os.ReadFile(path) //nolint:gosec // root's own file in the install
	if err != nil {
		return nil, err
	}
	var rp restorePoint
	if err := json.Unmarshal(b, &rp); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	name := filepath.Base(rp.BackupFile)
	if !strings.HasSuffix(name, ".bkp") || !filepath.IsAbs(rp.CLIPath) {
		return nil, fmt.Errorf("read %s: unexpected backup_file or cli_path", path)
	}
	rp.BackupFile = filepath.Join(active, name)
	rp.FilesDir, rp.CLIBackupPath = filepath.Join(active, restorePointFiles), filepath.Join(active, restorePointCLI)
	return &rp, nil
}

// ownedByRoot reports whether a file is root's. When this CLI does not run
// as root (tests), its own user's files count as root's.
func ownedByRoot(st os.FileInfo) bool {
	if os.Geteuid() != 0 {
		return true
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && s.Uid == 0
}

// writeRestorePoint writes the restore point record into rpDir.
func writeRestorePoint(rpDir string, rp *restorePoint) error {
	b, err := json.MarshalIndent(rp, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(rpDir, restorePointName), append(b, '\n'), 0o600)
}

// copyFiles copies installFiles from src to dst (made, root only, when
// missing), keeping their modes. A file src does not have is skipped.
func copyFiles(src, dst string) error {
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, name := range installFiles {
		p := filepath.Join(src, name)
		b, err := os.ReadFile(p) //nolint:gosec // the install's own files
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dst, name), b, st.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

// sameFiles reports whether a and b hold the same installFiles (one that
// neither has counts as the same).
func sameFiles(a, b string) bool {
	for _, name := range installFiles {
		x, err1 := os.ReadFile(filepath.Join(a, name)) //nolint:gosec // the install's own files
		y, err2 := os.ReadFile(filepath.Join(b, name)) //nolint:gosec // the install's own files
		switch {
		case errors.Is(err1, os.ErrNotExist) && errors.Is(err2, os.ErrNotExist):
		case err1 != nil || err2 != nil || !bytes.Equal(x, y):
			return false
		}
	}
	return true
}

// lexists reports whether path exists, without following a symlink.
func lexists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// swapCLI replaces the binary at exe with bin in one rename, keeping the old
// one as exe.prev (a hard link, or a copy where links do not work).
func swapCLI(exe string, bin []byte) error {
	prev := exe + ".prev"
	if err := os.Remove(prev); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(exe, prev); err != nil {
		old, rerr := os.ReadFile(exe) //nolint:gosec // the running binary
		if rerr != nil {
			return rerr
		}
		if err := writeAtomic(prev, old, 0o755); err != nil {
			return err
		}
	}
	return writeAtomic(exe, bin, 0o755)
}
