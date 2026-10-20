package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperTest is a run of the dashboard's helper over the update tests' install: a fake release
// server, a fake Docker and a fake new bobres, with the helper's output going where the real
// one's goes (the status log).
type helperTest struct {
	*updateTest
	tb     *testing.T
	st     *statusFile
	h      *helperRun
	tee    *logTee
	errTee *logTee
	snaps  []helperStatus
	n      int
}

func newHelperTest(t *testing.T) *helperTest {
	t.Helper()
	// The service's output is plain lines without the banner, which would fill the log.
	saved := uiOpts
	uiOpts = uiOptions{plain: true, noBanner: true}
	t.Cleanup(func() { uiOpts = saved })
	ut := newUpdateTest(t, "")
	if err := os.MkdirAll(updateDirPath(ut.dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(ut.dir, runDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	env, _, err := readEnv(filepath.Join(ut.dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	st := newStatusFile(statusPath(ut.dir), newRedactor(env).line)
	st.now = func() time.Time { return opsNow }
	ht := &helperTest{updateTest: ut, tb: t, st: st}
	st.observe = func(s helperStatus) { ht.snaps = append(ht.snaps, s) }
	ht.tee, ht.errTee = &logTee{w: ut.out, st: st}, &logTee{w: ut.errOut, st: st}
	ut.u.out, ut.u.errOut = ht.tee, ht.errTee
	ht.h = &helperRun{
		u: ut.u, dir: ut.dir, coreUID: os.Getuid(), now: ut.u.clock, st: st, helper: "1.2.3",
		flush: func() { ht.tee.flush(); ht.errTee.flush() },
	}
	return ht
}

// request is what core writes: a new request (a new id) at the install's clock, changed by mods.
// It returns the id.
func (ht *helperTest) request(mods ...func(map[string]any)) string {
	ht.n++
	id := fmt.Sprintf("0192f9e0-7a1b-7c3d-8e4f-%012d", ht.n)
	all := append([]func(map[string]any){set("id", id), set("at", opsNow.Add(-time.Minute).Format(time.RFC3339))}, mods...)
	ht.write(reqJSON(all...))
	return id
}

func (ht *helperTest) write(body []byte) {
	ht.tb.Helper()
	if err := os.WriteFile(ht.requestPath(), body, 0o600); err != nil { //nolint:gosec // the test's own temp directory
		ht.tb.Fatal(err)
	}
}

func (ht *helperTest) requestPath() string {
	return filepath.Join(updateDirPath(ht.dir), requestFileName)
}

func (ht *helperTest) status() helperStatus {
	ht.tb.Helper()
	st, ok := readHelperStatus(statusPath(ht.dir))
	if !ok {
		ht.tb.Fatal("there is no status.json")
	}
	return st
}

// steps are the steps the status went through while running, in order, each once in a row.
func (ht *helperTest) steps() []string {
	var out []string
	for _, s := range ht.snaps {
		if s.Step != "" && (len(out) == 0 || out[len(out)-1] != s.Step) {
			out = append(out, s.Step)
		}
	}
	return out
}

func (ht *helperTest) gone() {
	ht.tb.Helper()
	if _, err := os.Lstat(ht.requestPath()); !errors.Is(err, os.ErrNotExist) {
		ht.tb.Errorf("request.json is still there: %v", err)
	}
}

// childOutput is what the new bobres's install prints (its plain progress), then what it writes.
func (ht *helperTest) childOutput(ver string) func() error {
	write := installs(ht.dir, ver)
	return func() error {
		fmt.Fprintln(ht.tee, "[ 55%] "+installStepName(stepPull))
		fmt.Fprintln(ht.tee, "[ 60%] OK "+installStepName(stepPull)+" (38s)")
		fmt.Fprintln(ht.tee, "[ 62%] "+installStepName(stepStart))
		return write()
	}
}

func TestHelperRunsTheSameUpdateAndReportsEachStep(t *testing.T) {
	ht := newHelperTest(t)
	ht.cli.do = ht.childOutput("1.3.0")
	id := ht.request()

	if code := ht.h.exec(); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, ht.out, ht.errOut)
	}
	ht.gone()
	st := ht.status()
	if st.State != helperDone || st.Step != stepFinished || st.ID != id || st.From != "1.2.3" || st.To != "1.3.0" || st.Channel != "stable" ||
		st.Message != "BOBRES was updated from 1.2.3 to 1.3.0." || !st.Installed || st.V != 1 {
		t.Errorf("final status: %+v", st)
	}
	// The bobres on disk is the new one's now, and the status says so.
	if st.Helper != "1.3.0" {
		t.Errorf("helper %q after the update, want 1.3.0", st.Helper)
	}
	if st.StartedAt == nil || st.FinishedAt == nil || *st.StartedAt != "2026-10-11T12:00:00Z" || *st.FinishedAt != "2026-10-11T12:00:00Z" {
		t.Errorf("times %v %v", st.StartedAt, st.FinishedAt)
	}
	if want := []string{"verify", "backup", "pull", "restart", "health", "done"}; !reflect.DeepEqual(ht.steps(), want) {
		t.Errorf("steps %v, want %v", ht.steps(), want)
	}
	for i, s := range ht.snaps {
		if i < len(ht.snaps)-1 && s.State != helperRunning {
			t.Errorf("write %d says %q before the end", i, s.State)
		}
	}
	// The release the helper found is told as soon as it is known, before the backup.
	foundEarly := false
	for _, s := range ht.snaps {
		if s.Step == stepBackingUp && s.To == "1.3.0" {
			foundEarly = true
		}
	}
	if !foundEarly {
		t.Error("the target release was not reported by the backup step")
	}
	log := strings.Join(st.Log, "\n")
	for _, w := range []string{"Update requested by owner from the dashboard", "Checking the release signature", "Backing up the current database first", "BOBRES is updated to 1.3.0"} {
		if !strings.Contains(log, w) {
			t.Errorf("the log lacks %q:\n%s", w, log)
		}
	}

	// The same update as `bobres update --yes --channel stable`: the new bobres's install with the same
	// flags, a backup first, the restore point and the release state.
	want := [][]string{{"install", "--version", "1.3.0", "--dir", ht.dir, "--quiet", "--yes", "--skip-checks", "--offline"}}
	if !reflect.DeepEqual(ht.cli.calls, want) || ht.cli.binary[0] != newCLI {
		t.Errorf("CLI runs %q with %q", ht.cli.calls, ht.cli.binary)
	}
	if !reflect.DeepEqual(ht.db.steps, []string{"dump"}) {
		t.Errorf("steps %q", ht.db.steps)
	}
	if rp, err := readRestorePoint(ht.dir); err != nil || rp == nil || rp.FromVersion != "1.2.3" || rp.ToVersion != "1.3.0" {
		t.Errorf("restore point: %+v %v", rp, err)
	}
	if ht.state("keys.seq") != "3\n" || ht.state("release.version") != "1.3.0\n" {
		t.Errorf("state: %q %q", ht.state("keys.seq"), ht.state("release.version"))
	}
	// The channel was the one in .env already (stable): nothing to save, no bot recreated.
	if contains(ht.db.steps, "up -d --no-deps bot") {
		t.Errorf("the bot was recreated for nothing: %q", ht.db.steps)
	}
	if env, _, _ := readEnv(filepath.Join(ht.dir, ".env")); env["BOBRES_UPDATE_CHANNEL"] != "" {
		t.Errorf("BOBRES_UPDATE_CHANNEL = %q", env["BOBRES_UPDATE_CHANNEL"])
	}
	if b, _ := os.ReadFile(filepath.Join(ht.dir, handledFileName)); !strings.Contains(string(b), id) {
		t.Errorf("the id was not recorded: %q", b)
	}
	if !exists(filepath.Join(ht.dir, installLockName)) {
		t.Error("no lock file")
	}
}

// The helper and the command line make the same update.
func TestHelperAndCommandLineUpdateTheSame(t *testing.T) {
	ht := newHelperTest(t)
	ht.cli.do = installs(ht.dir, "1.3.0")
	ht.request()
	if code := ht.h.exec(); code != 0 {
		t.Fatalf("helper: %d %s", code, ht.errOut)
	}

	ut := newUpdateTest(t, "")
	ut.cli.do = installs(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes", "--channel", "stable"}); code != 0 {
		t.Fatalf("command line: %d %s", code, ut.errOut)
	}
	norm := func(x [][]string, dir string) [][]string {
		var out [][]string
		for _, c := range x {
			var r []string
			for _, a := range c {
				r = append(r, strings.ReplaceAll(a, dir, "<dir>"))
			}
			out = append(out, r)
		}
		return out
	}
	if a, b := norm(ht.cli.calls, ht.dir), norm(ut.cli.calls, ut.dir); !reflect.DeepEqual(a, b) {
		t.Errorf("CLI runs differ:\n%v\n%v", a, b)
	}
	if !reflect.DeepEqual(ht.db.steps, ut.db.steps) {
		t.Errorf("Docker steps differ:\n%v\n%v", ht.db.steps, ut.db.steps)
	}
	rpA, _ := readRestorePoint(ht.dir)
	rpB, _ := readRestorePoint(ut.dir)
	if rpA == nil || rpB == nil || rpA.FromVersion != rpB.FromVersion || rpA.ToVersion != rpB.ToVersion || !reflect.DeepEqual(rpA.SchemaVersions, rpB.SchemaVersions) {
		t.Errorf("restore points differ: %+v %+v", rpA, rpB)
	}
	for _, f := range []string{"keys.seq", "release.version", "checksums.txt"} {
		if ht.state(f) != ut.state(f) {
			t.Errorf("state %s differs: %q %q", f, ht.state(f), ut.state(f))
		}
	}
	if a, b := ht.read(t, ".env"), ut.read(t, ".env"); a != b {
		t.Errorf(".env differs:\n%s\n%s", a, b)
	}
}

// The channel the dashboard asked for is kept in .env, with the bot (which checks for releases)
// recreated, so that sudo bobres update and the menu follow it.
func TestHelperSavesTheChannel(t *testing.T) {
	setup := func(t *testing.T, channel string) *helperTest {
		ht := newHelperTest(t)
		newReleaseAPI(t, ht.updateTest, "v1.3.0-rc.10", "v1.2.3")
		ht.gh.versions["1.3.0-rc.10"] = signedChannelRelease(t, ht.root, "1.3.0-rc.10", "beta", "2026-10-10T10:00Z", []byte(betaCLI))
		ht.cli.do = installs(ht.dir, "1.3.0-rc.10")
		ht.request(set("channel", channel))
		return ht
	}
	envChannel := func(ht *helperTest) string {
		env, _, _ := readEnv(filepath.Join(ht.dir, ".env"))
		return env["BOBRES_UPDATE_CHANNEL"]
	}

	t.Run("beta, saved", func(t *testing.T) {
		ht := setup(t, "beta")
		if code := ht.h.exec(); code != 0 {
			t.Fatalf("exit %d\n%s\n%s", code, ht.out, ht.errOut)
		}
		st := ht.status()
		if st.State != helperDone || st.To != "1.3.0-rc.10" || st.Channel != "beta" || !strings.HasSuffix(st.Message, "The update channel is now beta.") {
			t.Errorf("status %+v", st)
		}
		if envChannel(ht) != "beta" {
			t.Errorf("BOBRES_UPDATE_CHANNEL = %q", envChannel(ht))
		}
		if !contains(ht.db.steps, "up -d --no-deps bot") {
			t.Errorf("the bot was not recreated: %q", ht.db.steps)
		}
	})
	t.Run("back to stable", func(t *testing.T) {
		ht := setup(t, "stable")
		ht.setEnv(t, "BOBRES_UPDATE_CHANNEL", "beta")
		// The newest stable release is 1.3.0 (installs it): the release list is not asked on stable.
		ht.cli.do = installs(ht.dir, "1.3.0")
		if code := ht.h.exec(); code != 0 {
			t.Fatalf("exit %d\n%s\n%s", code, ht.out, ht.errOut)
		}
		if envChannel(ht) != "stable" || !strings.HasSuffix(ht.status().Message, "The update channel is now stable.") {
			t.Errorf("channel %q, message %q", envChannel(ht), ht.status().Message)
		}
	})
	t.Run("already the channel", func(t *testing.T) {
		ht := setup(t, "beta")
		ht.setEnv(t, "BOBRES_UPDATE_CHANNEL", "beta")
		if code := ht.h.exec(); code != 0 {
			t.Fatalf("exit %d\n%s", code, ht.errOut)
		}
		if contains(ht.db.steps, "up -d --no-deps bot") || strings.Contains(ht.status().Message, "channel") {
			t.Errorf("saved a channel that was set: %q %q", ht.db.steps, ht.status().Message)
		}
	})
	// The release is installed all the same: the update is done, with a word about the channel.
	t.Run("the bot does not come back healthy", func(t *testing.T) {
		ht := setup(t, "beta")
		ht.db.ps = func() string {
			if contains(ht.db.steps, "up -d --no-deps bot") {
				return unhealthyCore
			}
			return healthyPS
		}
		if code := ht.h.exec(); code != 0 {
			t.Fatalf("exit %d\n%s", code, ht.errOut)
		}
		st := ht.status()
		if st.State != helperDone || !strings.Contains(st.Message, "BOBRES was updated from 1.2.3 to 1.3.0-rc.10.") ||
			!strings.Contains(st.Message, "The update channel is beta, but the bot did not restart cleanly") || envChannel(ht) != "beta" {
			t.Errorf("status %+v, channel %q", st, envChannel(ht))
		}
	})
	t.Run("the setting cannot be written", func(t *testing.T) {
		ht := setup(t, "beta")
		install := ht.cli.do
		ht.cli.do = func() error {
			if err := install(); err != nil {
				return err
			}
			// From here the install directory takes no new file.
			if err := os.Chmod(ht.dir, 0o500); err != nil { //nolint:gosec // the point of the test
				return err
			}
			t.Cleanup(func() { _ = os.Chmod(ht.dir, 0o700) }) //nolint:gosec // so that the temp dir can be removed
			return nil
		}
		if code := ht.h.exec(); code != 0 {
			t.Fatalf("exit %d\n%s", code, ht.errOut)
		}
		st := ht.status()
		if st.State != helperDone || !strings.Contains(st.Message, "The update channel could not be saved: sudo bobres config set BOBRES_UPDATE_CHANNEL beta") {
			t.Errorf("status %+v", st)
		}
		if envChannel(ht) == "beta" {
			t.Error("the channel was written to a directory that takes no file")
		}
	})
}

func TestHelperWithNothingToUpdate(t *testing.T) {
	ht := newHelperTest(t)
	ht.setEnv(t, "BOBRES_VERSION", "1.3.0")
	ht.request()
	if code := ht.h.exec(); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, ht.out, ht.errOut)
	}
	st := ht.status()
	if st.State != helperDone || st.Message != "BOBRES already runs 1.3.0: nothing to update." || st.From != "1.3.0" || st.To != "1.3.0" {
		t.Errorf("status %+v", st)
	}
	if len(ht.cli.calls) != 0 || len(ht.db.steps) != 0 {
		t.Errorf("something ran: %q %q", ht.cli.calls, ht.db.steps)
	}
	ht.gone()
}

func TestHelperPinsAReleaseAndFollowsTheChannelRules(t *testing.T) {
	ht := newHelperTest(t)
	ht.gh.versions["1.2.4"] = signedRelease(t, ht.root, "1.2.4", []byte("#!1.2.4"))
	ht.cli.do = installs(ht.dir, "1.2.4")
	ht.request(set("version", "1.2.4"))
	if code := ht.h.exec(); code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, ht.out, ht.errOut)
	}
	if st := ht.status(); st.To != "1.2.4" || st.State != helperDone {
		t.Errorf("status %+v", st)
	}
	if got := ht.cli.calls[0][2]; got != "1.2.4" {
		t.Errorf("installed %q", got)
	}

	// A pre-release on the stable channel is refused, as it is on the command line.
	ht2 := newHelperTest(t)
	ht2.gh.versions["1.4.0-rc.1"] = signedChannelRelease(t, ht2.root, "1.4.0-rc.1", "beta", "2026-10-10T10:00Z", []byte("#!rc"))
	ht2.request(set("version", "1.4.0-rc.1"))
	if code := ht2.h.exec(); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if st := ht2.status(); st.State != helperFailed || st.Step != stepVerifying || !strings.Contains(st.Message, "release 1.4.0-rc.1 is a beta release and this install follows stable") {
		t.Errorf("status %+v", st)
	}
	ht2.unchanged(t)
}

func TestHelperFailsBeforeChangingAnything(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(ht *helperTest)
		why   string
	}{
		"an older release": {func(ht *helperTest) {
			ht.gh.latest = signedRelease(t, ht.root, "1.2.0", []byte(newCLI))
		}, "release 1.2.0 is older than this install (1.2.3)"},
		"a bad signature": {func(ht *helperTest) {
			ht.gh.latest["checksums.txt.sig"][0] ^= 1
		}, "did not pass the signature check"},
		"an unknown release": {func(ht *helperTest) {
			delete(ht.gh.latest, "checksums.txt")
		}, "no BOBRES release has been published yet"},
		"a downgrade of the floor": {func(ht *helperTest) {
			ht.setState(t, "release.version", "1.4.0\n")
		}, "older than release 1.4.0, which this server installed before"},
		"GitHub does not answer": {func(ht *helperTest) {
			ht.gh.srv.Close()
		}, "is GitHub reachable"},
	} {
		t.Run(name, func(t *testing.T) {
			ht := newHelperTest(t)
			tc.setup(ht)
			ht.request()
			if code := ht.h.exec(); code != 1 {
				t.Fatalf("exit %d\n%s", code, ht.errOut)
			}
			st := ht.status()
			if st.State != helperFailed || st.Step != stepVerifying || st.FinishedAt == nil || !strings.Contains(st.Message, tc.why) {
				t.Errorf("status %+v, want a failure with %q", st, tc.why)
			}
			if strings.Contains(st.Message, "\n") || len(st.Message) > 240 {
				t.Errorf("the message is not one short sentence: %q", st.Message)
			}
			ht.gone()
			_ = os.Remove(filepath.Join(ht.u.stateDir, "release.version")) // the case that sets it
			ht.unchanged(t)
		})
	}
}

func TestHelperRollsBackAndSaysSo(t *testing.T) {
	t.Run("the install fails", func(t *testing.T) {
		ht := newHelperTest(t)
		envBefore := ht.read(t, ".env")
		write := ht.childOutput("1.3.0")
		ht.cli.do = func() error {
			if err := write(); err != nil {
				return err
			}
			return errors.New("exit status 1")
		}
		id := ht.request()
		if code := ht.h.exec(); code != 1 {
			t.Fatalf("exit %d\n%s", code, ht.errOut)
		}
		st := ht.status()
		if st.State != helperRolledBack || st.Step != stepRollingBk || st.ID != id || st.From != "1.2.3" || st.To != "1.3.0" ||
			st.Message != "The update to 1.3.0 did not go through: BOBRES runs 1.2.3 again." || st.Helper != "1.2.3" {
			t.Errorf("status %+v", st)
		}
		if want := []string{"verify", "backup", "pull", "restart", "rollback"}; !reflect.DeepEqual(ht.steps(), want) {
			t.Errorf("steps %v, want %v", ht.steps(), want)
		}
		if b, _ := os.ReadFile(ht.exe); string(b) != oldCLI || ht.read(t, ".env") != envBefore {
			t.Errorf("not rolled back: %q", b)
		}
		if log := strings.Join(st.Log, "\n"); !strings.Contains(log, "the install of 1.3.0 failed") || !strings.Contains(log, "Rolling back to 1.2.3") {
			t.Errorf("the log:\n%s", log)
		}
		// The release that was tried does not become the floor.
		if ht.state("release.version") != "" {
			t.Errorf("release.version %q", ht.state("release.version"))
		}
		ht.gone()
	})
	t.Run("the store does not come back healthy", func(t *testing.T) {
		ht := newHelperTest(t)
		ht.cli.do = ht.installsAndMigrates("1.3.0")
		ht.db.ps = func() string {
			if len(ht.db.restored) == 0 {
				return unhealthyCore
			}
			return healthyPS
		}
		ht.request()
		if code := ht.h.exec(); code != 1 {
			t.Fatalf("exit %d\n%s", code, ht.errOut)
		}
		if st := ht.status(); st.State != helperRolledBack || st.Step != stepRollingBk {
			t.Errorf("status %+v", st)
		}
		if len(ht.db.restored) != 1 || ht.db.restored[0] != "DUMP BEFORE THE UPDATE" {
			t.Errorf("restored %q", ht.db.restored)
		}
		if want := []string{"verify", "backup", "pull", "health", "rollback"}; !reflect.DeepEqual(ht.steps(), want) {
			t.Errorf("steps %v, want %v", ht.steps(), want)
		}
	})
	t.Run("the rollback does not finish either", func(t *testing.T) {
		ht := newHelperTest(t)
		ht.cli.do = ht.installsAndMigrates("1.3.0")
		ht.db.ps = func() string { return unhealthyCore }
		ht.db.restoreErr = errors.New("exit status 3")
		ht.request()
		if code := ht.h.exec(); code != 1 {
			t.Fatalf("exit %d", code)
		}
		st := ht.status()
		if st.State != helperFailed || st.Step != stepRollingBk || !strings.Contains(st.Message, "the automatic rollback did not finish") ||
			!strings.Contains(st.Message, "docs/runbooks/update-rollback.md") {
			t.Errorf("status %+v", st)
		}
	})
	t.Run("the backup before the update fails", func(t *testing.T) {
		ht := newHelperTest(t)
		ht.db.dumpErr = errors.New("pg_dump: connection refused")
		ht.request()
		if code := ht.h.exec(); code != 1 {
			t.Fatalf("exit %d", code)
		}
		st := ht.status()
		if st.State != helperFailed || st.Step != stepBackingUp || !strings.Contains(st.Message, "the backup before the update failed") {
			t.Errorf("status %+v", st)
		}
		if len(ht.cli.calls) != 0 {
			t.Errorf("the install ran: %q", ht.cli.calls)
		}
		if b, _ := os.ReadFile(ht.exe); string(b) != oldCLI {
			t.Error("the CLI was replaced")
		}
	})
}

// Whatever is wrong with the request, it is deleted, nothing is changed and the status says why,
// under the request's id when the file showed one.
func TestHelperRefusesBadRequests(t *testing.T) {
	good := string(reqJSON())
	for name, tc := range map[string]struct {
		plant func(ht *helperTest) string // returns the id the status should carry
		why   string
	}{
		"older than 15 minutes": {func(ht *helperTest) string {
			return ht.request(set("at", opsNow.Add(-16*time.Minute).Format(time.RFC3339)))
		}, "older than 15 minutes"},
		"from the future": {func(ht *helperTest) string {
			return ht.request(set("at", opsNow.Add(5*time.Minute).Format(time.RFC3339)))
		}, "in the future"},
		"another field": {func(ht *helperTest) string {
			return ht.request(set("url", "https://evil.example"))
		}, `unknown field "url"`},
		"a bad channel":  {func(ht *helperTest) string { return ht.request(set("channel", "nightly")) }, "neither stable nor beta"},
		"a bad version":  {func(ht *helperTest) string { return ht.request(set("version", "1.2.3-../x")) }, "not a release"},
		"another action": {func(ht *helperTest) string { return ht.request(set("action", "rollback")) }, "is not update"},
		"not JSON": {func(ht *helperTest) string {
			ht.write([]byte("update now please"))
			return ""
		}, "not the JSON object"},
		"too big": {func(ht *helperTest) string {
			ht.write(append([]byte(good), bytes.Repeat([]byte(" "), 3000)...))
			return ""
		}, "larger than 2048"},
		"a link": {func(ht *helperTest) string {
			target := filepath.Join(t.TempDir(), "elsewhere.json")
			if err := os.WriteFile(target, []byte(good), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, ht.requestPath()); err != nil {
				t.Fatal(err)
			}
			return ""
		}, "symbolic link"},
		"a pipe": {func(ht *helperTest) string {
			if err := syscall.Mkfifo(ht.requestPath(), 0o600); err != nil {
				t.Fatal(err)
			}
			return ""
		}, "not a regular file"},
		"someone else's file": {func(ht *helperTest) string {
			id := ht.request()
			ht.h.coreUID++ // the helper expects core to be another user than the one who wrote it
			return id
		}, "not owned by core"},
	} {
		t.Run(name, func(t *testing.T) {
			ht := newHelperTest(t)
			id := tc.plant(ht)
			if code := ht.h.exec(); code != 1 {
				t.Fatalf("exit %d\n%s", code, ht.errOut)
			}
			ht.gone()
			st := ht.status()
			if st.State != helperFailed || st.Step != stepVerifying || !strings.Contains(st.Message, "The update request was refused: ") ||
				!strings.Contains(st.Message, tc.why) || st.FinishedAt == nil {
				t.Errorf("status %+v, want a refusal with %q", st, tc.why)
			}
			// Only the owner's own request carries an id into the status.
			if name != "someone else's file" && st.ID != id {
				t.Errorf("id %q, want %q", st.ID, id)
			}
			ht.unchanged(t)
			if b, _ := os.ReadFile(filepath.Join(ht.dir, handledFileName)); len(b) != 0 {
				t.Errorf("a refused request was recorded as handled: %q", b)
			}
		})
	}
}

// A request that comes back runs once.
func TestHelperIgnoresARequestItHandled(t *testing.T) {
	ht := newHelperTest(t)
	ht.cli.do = installs(ht.dir, "1.3.0")
	id := ht.request()
	body, err := os.ReadFile(ht.requestPath())
	if err != nil {
		t.Fatal(err)
	}
	if code := ht.h.exec(); code != 0 {
		t.Fatalf("exit %d\n%s", code, ht.errOut)
	}
	final, _ := os.ReadFile(statusPath(ht.dir))

	// The same file again (a copy someone kept, a file that was not deleted): ignored, and the result
	// of the first run stands.
	ht.write(body)
	ht.errOut.Reset()
	if code := ht.h.exec(); code != 0 {
		t.Fatalf("replay: exit %d\n%s", code, ht.errOut)
	}
	ht.gone()
	if !strings.Contains(ht.errOut.String(), "was handled before: ignored") || !strings.Contains(ht.errOut.String(), id) {
		t.Errorf("output: %s", ht.errOut)
	}
	if after, _ := os.ReadFile(statusPath(ht.dir)); !bytes.Equal(final, after) {
		t.Errorf("the replay changed the status:\n%s\n%s", final, after)
	}
	if len(ht.cli.calls) != 1 {
		t.Errorf("the install ran %d times", len(ht.cli.calls))
	}
}

func TestHelperIsSwitchedOffInEnv(t *testing.T) {
	ht := newHelperTest(t)
	ht.setEnv(t, dashboardUpdatesKey, "off")
	ht.request()
	if code := ht.h.exec(); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if st := ht.status(); st.State != helperFailed || !strings.Contains(st.Message, "switched off on this server") {
		t.Errorf("status %+v", st)
	}
	ht.gone()
	ht.unchanged(t)
}

func TestHelperWaitsForNobodyAndNeverOverlaps(t *testing.T) {
	t.Run("another update is running", func(t *testing.T) {
		ht := newHelperTest(t)
		unlock, err := lockInstall(ht.dir, opsNow)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		ht.request()
		if code := ht.h.exec(); code != 1 {
			t.Fatalf("exit %d", code)
		}
		ht.gone()
		st := ht.status()
		if st.State != helperFailed || !strings.Contains(st.Message, "Another update, a rollback or a restore is running") {
			t.Errorf("status %+v", st)
		}
		ht.unchanged(t)

		// When that update reports itself as running, its report is not replaced by this refusal.
		running := idleStatus(true, "1.2.3")
		running.State, running.ID, running.Message = helperRunning, "0192f9e0-7a1b-7c3d-8e4f-00000000beef", "Backing up."
		if err := writeHelperStatus(statusPath(ht.dir), &running); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(statusPath(ht.dir))
		ht.request()
		if code := ht.h.exec(); code != 1 {
			t.Fatalf("exit %d", code)
		}
		ht.gone()
		if after, _ := os.ReadFile(statusPath(ht.dir)); !bytes.Equal(before, after) {
			t.Errorf("the report of the running update was replaced:\n%s", after)
		}
	})
	t.Run("the lock is free again afterwards", func(t *testing.T) {
		ht := newHelperTest(t)
		ht.cli.do = installs(ht.dir, "1.3.0")
		ht.request()
		if code := ht.h.exec(); code != 0 {
			t.Fatalf("exit %d", code)
		}
		unlock, err := lockInstall(ht.dir, opsNow)
		if err != nil {
			t.Fatalf("the helper kept the lock: %v", err)
		}
		unlock()
	})
	t.Run("a run that was never finished", func(t *testing.T) {
		ht := newHelperTest(t)
		stale := idleStatus(true, "1.2.3")
		stale.State, stale.ID, stale.Step = helperRunning, "0192f9e0-7a1b-7c3d-8e4f-00000000dead", stepPulling
		if err := writeHelperStatus(statusPath(ht.dir), &stale); err != nil {
			t.Fatal(err)
		}
		// Nothing asked for: the lock is taken, the stale report is told apart, nothing else changes.
		if code := ht.h.exec(); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if st := ht.status(); st.State != helperFailed || st.ID != stale.ID || !strings.Contains(st.Message, "interrupted") {
			t.Errorf("status %+v", st)
		}
		// A new request is answered under its own id.
		ht.cli.do = installs(ht.dir, "1.3.0")
		id := ht.request()
		if code := ht.h.exec(); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if st := ht.status(); st.State != helperDone || st.ID != id {
			t.Errorf("status %+v", st)
		}
	})
}

func TestHelperWithoutARequestDoesNothing(t *testing.T) {
	ht := newHelperTest(t)
	if code := ht.h.exec(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(ht.out.String(), "nothing to do") {
		t.Errorf("output %q", ht.out)
	}
	if _, err := os.Stat(statusPath(ht.dir)); err == nil {
		t.Error("a status was written for no request")
	}
	ht.unchanged(t)
}

func TestHelperDistrustsAnUpdateDirectoryThatIsNotRight(t *testing.T) {
	ht := newHelperTest(t)
	ht.request()
	if err := os.Chmod(updateDirPath(ht.dir), 0o777); err != nil { //nolint:gosec // the point of the test
		t.Fatal(err)
	}
	if code := ht.h.exec(); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(ht.errOut.String(), "can be written by others") {
		t.Errorf("output %q", ht.errOut)
	}
	// Nothing was read, deleted or written there.
	if _, err := os.Lstat(ht.requestPath()); err != nil {
		t.Errorf("the request was taken: %v", err)
	}
	if _, err := os.Stat(statusPath(ht.dir)); err == nil {
		t.Error("a status was written")
	}
	ht.unchanged(t)
}

// The hidden command, as the service runs it.
func TestHelperCommand(t *testing.T) {
	dir := restoreInstall(t)
	if err := os.MkdirAll(updateDirPath(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, runDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run([]string{"update", "--from-request", "--dir", dir}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("no request: %d\n%s\n%s", code, &out, &errOut)
	}
	if code := run([]string{"update", "--from-request", "--dir", dir, "extra"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: bobres update --from-request") {
		t.Errorf("an argument: %d %s", code, &errOut)
	}
	errOut.Reset()
	if code := run([]string{"update", "--from-request", "--dir", t.TempDir()}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "no install found") {
		t.Errorf("no install: %d %s", code, &errOut)
	}
	if code := run([]string{"update", "--from-request", "--bogus"}, &out, &errOut); code != 2 {
		t.Errorf("a flag it does not have: %d", code)
	}
	// The flag is not for people: it is not in the help, and update's own flags do not take it.
	if strings.Contains(usageText, "from-request") {
		t.Error("the help names --from-request")
	}
	// And the UI options it set are put back.
	if uiOpts.plain || uiOpts.noBanner {
		t.Errorf("uiOpts left as %+v", uiOpts)
	}
}

// bobres update, rollback and restore take the one lock the helper takes.
func TestUpdateRollbackAndRestoreTakeTheLock(t *testing.T) {
	ut := newUpdateTest(t, "")
	unlock, err := lockInstall(ut.dir, opsNow)
	if err != nil {
		t.Fatal(err)
	}
	busy := "another bobres update, rollback or restore is running on this install"

	for name, run := range map[string]func() int{
		"update":   func() int { return ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}) },
		"rollback": func() int { return ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}) },
		"restore": func() int {
			return ut.u.restoreCmd([]string{"--dir", ut.dir, "--yes", filepath.Join(ut.dir, "nothing.bkp")})
		},
	} {
		ut.errOut.Reset()
		if code := run(); code != 1 || !strings.Contains(ut.errOut.String(), busy) || !strings.Contains(ut.errOut.String(), "Nothing was changed") {
			t.Errorf("%s while the lock is held: %d %s", name, code, ut.errOut)
		}
	}
	ut.unchanged(t)
	// Looking is not changing: --check does not wait for the lock.
	if code := ut.u.updateCmd([]string{"--check", "--dir", ut.dir}); code != 0 {
		t.Errorf("check while locked: %d %s", code, ut.errOut)
	}

	unlock()
	ut.cli.do = installs(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("update after the lock was released: %d\n%s", code, ut.errOut)
	}
	// And it let go again when it was done.
	unlock, err = lockInstall(ut.dir, opsNow)
	if err != nil {
		t.Fatalf("update kept the lock: %v", err)
	}
	unlock()
	if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
		t.Fatalf("rollback: %d %s", code, ut.errOut)
	}
	if unlock, err = lockInstall(ut.dir, opsNow); err != nil {
		t.Fatalf("rollback kept the lock: %v", err)
	}
	unlock()
}

func TestLockInstallHoldsOneAtATime(t *testing.T) {
	dir := t.TempDir()
	one, err := lockInstall(dir, opsNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockInstall(dir, opsNow); !errors.Is(err, errInstallBusy) {
		t.Errorf("a second lock: %v", err)
	}
	one()
	two, err := lockInstall(dir, opsNow)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	two()
	if st, err := os.Stat(filepath.Join(dir, installLockName)); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("lock file: %v %v", st, err)
	}
	// A link in its place is not followed.
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir2 := t.TempDir()
	if err := os.Symlink(other, filepath.Join(dir2, installLockName)); err != nil {
		t.Fatal(err)
	}
	if _, err := lockInstall(dir2, opsNow); err == nil {
		t.Error("locked through a link")
	}
	if _, err := lockInstall(filepath.Join(dir, "missing"), opsNow); err == nil {
		t.Error("locked in a directory that is not there")
	}
}
