package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// signedChannelRelease is signedRelease signed for channel and issued at
// issued.
func signedChannelRelease(t *testing.T, root ed25519.PrivateKey, ver, channel, issued string, cli []byte) map[string][]byte {
	t.Helper()
	relPub, relPriv := testKey(t)
	keys := []byte("bobres-keys v1\nseq 3\nexpires 2027-09-30T15:15Z\nmin_version 0.0.1\nkey rel-a " + relPub + "\n")
	sum := sha256.Sum256(cli)
	sums := []byte("# bobres-release v1\n# version " + ver + "\n# channel " + channel + "\n# key_id rel-a\n" +
		"# issued " + issued + "\n# expires 2026-11-24T10:00Z\n" + hex.EncodeToString(sum[:]) + "  bobres_linux_amd64\n")
	return map[string][]byte{
		"checksums.txt": sums, "checksums.txt.sig": ed25519.Sign(relPriv, sums),
		"keys.txt": keys, "keys.txt.sig.1": ed25519.Sign(root, keys),
		"bobres_linux_amd64": cli,
	}
}

// releaseAPI stands in for GitHub's release list; status 0 answers 200.
type releaseAPI struct {
	srv    *httptest.Server
	list   string
	status int
	calls  int
}

func newReleaseAPI(t *testing.T, ut *updateTest, tags ...string) *releaseAPI {
	t.Helper()
	api := &releaseAPI{}
	var items []string
	for _, tag := range tags {
		items = append(items, fmt.Sprintf(`{"tag_name": %q, "draft": false, "prerelease": %v}`, tag, strings.Contains(tag, "-")))
	}
	api.list = "[" + strings.Join(items, ",") + "]"
	api.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.calls++
		if api.status != 0 {
			w.WriteHeader(api.status)
		}
		_, _ = w.Write([]byte(api.list))
	}))
	t.Cleanup(api.srv.Close)
	ut.u.apiURL = api.srv.URL + "/repos/x/y/releases"
	return api
}

// setEnv changes one .env line of the test install.
func (ut *updateTest) setEnv(t *testing.T, key, value string) {
	t.Helper()
	env, _, err := readEnv(filepath.Join(ut.dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	env[key] = value
	if err := writeAtomic(filepath.Join(ut.dir, ".env"), renderEnv(env), 0o600); err != nil {
		t.Fatal(err)
	}
}

const betaCLI = "#!new bobres 1.3.0-rc.10"

// The beta channel (BOBRES_UPDATE_CHANNEL=beta in .env, or --channel beta)
// takes the highest version GitHub lists, pre-releases included, and its
// files must be signed for that version.
func TestUpdateBetaChannel(t *testing.T) {
	newBeta := func(t *testing.T) (*updateTest, *releaseAPI) {
		ut := newUpdateTest(t, "")
		api := newReleaseAPI(t, ut, "v1.3.0-rc.9", "v1.3.0-rc.10", "v1.2.3")
		ut.gh.versions["1.3.0-rc.9"] = signedChannelRelease(t, ut.root, "1.3.0-rc.9", "beta", "2026-10-08T10:00Z", []byte("#!rc9"))
		ut.gh.versions["1.3.0-rc.10"] = signedChannelRelease(t, ut.root, "1.3.0-rc.10", "beta", "2026-10-10T10:00Z", []byte(betaCLI))
		return ut, api
	}

	t.Run("check", func(t *testing.T) {
		ut, api := newBeta(t)
		ut.setEnv(t, "BOBRES_UPDATE_CHANNEL", "beta")
		if code := ut.u.updateCmd([]string{"--check", "--dir", ut.dir}); code != 0 {
			t.Fatalf("check: %d %s", code, ut.errOut)
		}
		for _, w := range []string{"Channel: beta", "Latest:  1.3.0-rc.10 (beta, signature verified)", "Newer:   yes"} {
			if !strings.Contains(ut.out.String(), w) {
				t.Errorf("missing %q:\n%s", w, ut.out)
			}
		}
		// --channel stable overrides .env: GitHub's API is not asked.
		ut.out.Reset()
		api.calls = 0
		if code := ut.u.updateCmd([]string{"--check", "--channel", "stable", "--dir", ut.dir}); code != 0 ||
			!strings.Contains(ut.out.String(), "Channel: stable\nLatest:  1.3.0 (stable") || api.calls != 0 {
			t.Fatalf("stable: %d %d %s %s", code, api.calls, ut.out, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("update", func(t *testing.T) {
		ut, _ := newBeta(t)
		ut.cli.do = installs(ut.dir, "1.3.0-rc.10")
		if code := ut.u.updateCmd([]string{"--channel", "beta", "--dir", ut.dir, "--yes"}); code != 0 {
			t.Fatalf("update: %d\n%s\n%s", code, ut.out, ut.errOut)
		}
		want := [][]string{{"install", "--version", "1.3.0-rc.10", "--dir", ut.dir, "--quiet", "--yes", "--skip-checks", "--offline"}}
		if !reflect.DeepEqual(ut.cli.calls, want) || ut.cli.binary[0] != betaCLI {
			t.Fatalf("CLI runs %q with %q", ut.cli.calls, ut.cli.binary)
		}
		if ut.state("release.version") != "1.3.0-rc.10\n" {
			t.Fatalf("release.version %q", ut.state("release.version"))
		}
	})
	t.Run("files signed for another version", func(t *testing.T) {
		ut, _ := newBeta(t)
		ut.gh.versions["1.3.0-rc.10"] = ut.gh.versions["1.3.0-rc.9"] // the list is not signed
		if code := ut.u.updateCmd([]string{"--channel", "beta", "--dir", ut.dir, "--yes"}); code != 1 ||
			!strings.Contains(ut.errOut.String(), "the files of release 1.3.0-rc.10 are signed for 1.3.0-rc.9") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("GitHub's API refuses", func(t *testing.T) {
		ut, api := newBeta(t)
		api.status = http.StatusForbidden
		if code := ut.u.updateCmd([]string{"--channel", "beta", "--dir", ut.dir, "--yes"}); code != 1 ||
			!strings.Contains(ut.errOut.String(), "the beta channel's releases could not be listed") ||
			!strings.Contains(ut.errOut.String(), "60 requests an hour") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
	t.Run("nothing listed", func(t *testing.T) {
		ut, api := newBeta(t)
		api.list = "[]"
		if code := ut.u.updateCmd([]string{"--check", "--channel", "beta", "--dir", ut.dir}); code != 1 ||
			!strings.Contains(ut.errOut.String(), "no BOBRES release has been published yet") {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
	})
	t.Run("bad channel", func(t *testing.T) {
		ut, _ := newBeta(t)
		if code := ut.u.updateCmd([]string{"--channel", "nightly", "--dir", ut.dir, "--yes"}); code != 2 ||
			!strings.Contains(ut.errOut.String(), "unknown update channel") {
			t.Fatalf("flag: %d %s", code, ut.errOut)
		}
		ut.setEnv(t, "BOBRES_UPDATE_CHANNEL", "nightly")
		ut.errOut.Reset()
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 2 ||
			!strings.Contains(ut.errOut.String(), "BOBRES_UPDATE_CHANNEL in .env") {
			t.Fatalf(".env: %d %s", code, ut.errOut)
		}
		ut.unchanged(t)
	})
}

// The stable channel never takes a release signed for beta: not one that
// was published as the latest by mistake, nor one asked for by version.
func TestUpdateStableRefusesBeta(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.gh.latest = signedChannelRelease(t, ut.root, "1.3.0", "beta", "2026-10-10T10:00Z", []byte(newCLI))
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 ||
		!strings.Contains(ut.errOut.String(), "release 1.3.0 is a beta release and this install follows stable: to take it, add --channel beta") {
		t.Fatalf("latest: %d %s", code, ut.errOut)
	}
	ut.gh.versions["1.3.0-rc.10"] = signedChannelRelease(t, ut.root, "1.3.0-rc.10", "beta", "2026-10-10T10:00Z", []byte(betaCLI))
	ut.errOut.Reset()
	if code := ut.u.updateCmd([]string{"--check", "--version", "1.3.0-rc.10", "--dir", ut.dir}); code != 1 ||
		!strings.Contains(ut.errOut.String(), "add --channel beta") {
		t.Fatalf("version: %d %s", code, ut.errOut)
	}
	ut.unchanged(t)
	// With --channel beta the same release is taken.
	ut.out.Reset()
	if code := ut.u.updateCmd([]string{"--check", "--channel", "beta", "--version", "1.3.0-rc.10", "--dir", ut.dir}); code != 0 ||
		!strings.Contains(ut.out.String(), "Release: 1.3.0-rc.10 (beta, signature verified)") {
		t.Fatalf("beta: %d %s %s", code, ut.out, ut.errOut)
	}
}

// Back on stable after a beta: the install stays on its pre-release (no
// downgrade, no error) until a stable release is newer, then it updates.
func TestUpdateStableAfterBeta(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.setEnv(t, "BOBRES_VERSION", "1.3.0-rc.10")
	ut.setState(t, "release.version", "1.3.0-rc.10\n")
	ut.gh.latest = signedRelease(t, ut.root, "1.2.9", []byte(newCLI))
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 ||
		!strings.Contains(ut.out.String(), "BOBRES runs the pre-release 1.3.0-rc.10, newer than the latest stable release (1.2.9)") {
		t.Fatalf("update: %d %s %s", code, ut.out, ut.errOut)
	}
	ut.out.Reset()
	if code := ut.u.updateCmd([]string{"--check", "--dir", ut.dir}); code != 0 ||
		!strings.Contains(ut.out.String(), "Newer:   no, this install runs a pre-release newer than the latest stable release") {
		t.Fatalf("check: %d %s %s", code, ut.out, ut.errOut)
	}
	if len(ut.cli.calls) != 0 || len(ut.db.steps) != 0 {
		t.Fatalf("changed: %q %q", ut.cli.calls, ut.db.steps)
	}

	// 1.3.0 is newer than its release candidate.
	ut.gh.latest = signedRelease(t, ut.root, "1.3.0", []byte(newCLI))
	ut.cli.do = installs(ut.dir, "1.3.0")
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 || !strings.Contains(ut.out.String(), "BOBRES is updated to 1.3.0") {
		t.Fatalf("stable passed it: %d %s %s", code, ut.out, ut.errOut)
	}
}

// A beta that was tried and undone does not become the floor: once the
// install runs its stable release again, the next stable patch release (below
// the beta) is offered and installed.
func TestUpdateUndoneBetaIsNotTheFloor(t *testing.T) {
	const stableCLI = "#!new bobres 1.2.4"
	// tried is an install of 1.2.3 (install.sh recorded floor, "" for none)
	// with 1.3.0-rc.1 out on beta.
	tried := func(t *testing.T, floor string) *updateTest {
		ut := newUpdateTest(t, "")
		if floor != "" {
			ut.setState(t, "release.version", floor+"\n")
		}
		newReleaseAPI(t, ut, "v1.3.0-rc.1", "v1.2.3")
		ut.gh.versions["1.3.0-rc.1"] = signedChannelRelease(t, ut.root, "1.3.0-rc.1", "beta", "2026-10-10T10:00Z", []byte(betaCLI))
		return ut
	}
	// thenStable publishes 1.2.4 on stable: the check offers it and update
	// installs it.
	thenStable := func(t *testing.T, ut *updateTest) {
		t.Helper()
		ut.gh.latest = signedRelease(t, ut.root, "1.2.4", []byte(stableCLI))
		ut.out.Reset()
		ut.errOut.Reset()
		ut.db.steps, ut.db.restored, ut.db.ps = nil, nil, nil
		ut.cli.calls, ut.cli.binary, ut.cli.do = nil, nil, installs(ut.dir, "1.2.4")
		if code := ut.u.updateCmd([]string{"--check", "--dir", ut.dir}); code != 0 || !strings.Contains(ut.out.String(), "Newer:   yes. Update with") {
			t.Fatalf("check 1.2.4: %d %s %s", code, ut.out, ut.errOut)
		}
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 || !strings.Contains(ut.out.String(), "BOBRES is updated to 1.2.4") {
			t.Fatalf("update to 1.2.4: %d %s %s", code, ut.out, ut.errOut)
		}
		if got := ut.state("release.version"); got != "1.2.4\n" {
			t.Fatalf("release.version %q after 1.2.4", got)
		}
	}

	t.Run("rolled back on its own", func(t *testing.T) {
		ut := tried(t, "1.2.3")
		ut.cli.do = ut.installsAndMigrates("1.3.0-rc.1")
		ut.db.ps = func() string {
			if len(ut.db.restored) == 0 {
				return unhealthyCore
			}
			return healthyPS
		}
		if code := ut.u.updateCmd([]string{"--channel", "beta", "--dir", ut.dir, "--yes"}); code != 1 ||
			!strings.Contains(ut.errOut.String(), "BOBRES runs 1.2.3 again") {
			t.Fatalf("beta: %d %s", code, ut.errOut)
		}
		if got := ut.state("release.version"); got != "1.2.3\n" {
			t.Fatalf("release.version %q after the rollback", got)
		}
		thenStable(t, ut)
	})
	t.Run("install failed", func(t *testing.T) {
		ut := tried(t, "")
		ut.cli.do = func() error { return errors.New("exit status 1") }
		if code := ut.u.updateCmd([]string{"--channel", "beta", "--dir", ut.dir, "--yes"}); code != 1 {
			t.Fatalf("beta: %d %s", code, ut.errOut)
		}
		// The verified key list is recorded; the release that did not hold is not.
		if ut.state("release.version") != "" || ut.state("keys.seq") != "3\n" {
			t.Fatalf("state %q %q", ut.state("release.version"), ut.state("keys.seq"))
		}
		thenStable(t, ut)
	})
	for name, floor := range map[string]string{"rolled back by the owner": "1.2.3", "rolled back by the owner, no floor before": ""} {
		t.Run(name, func(t *testing.T) {
			ut := tried(t, floor)
			ut.cli.do = installs(ut.dir, "1.3.0-rc.1")
			if code := ut.u.updateCmd([]string{"--channel", "beta", "--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("beta: %d %s", code, ut.errOut)
			}
			if got := ut.state("release.version"); got != "1.3.0-rc.1\n" {
				t.Fatalf("release.version %q after the beta held", got)
			}
			ut.db.steps = nil
			if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("rollback: %d %s", code, ut.errOut)
			}
			want := ""
			if floor != "" {
				want = floor + "\n"
			}
			if got := ut.state("release.version"); got != want {
				t.Fatalf("release.version %q after the rollback, want %q", got, want)
			}
			thenStable(t, ut)
		})
	}
	// A floor raised since the update (install.sh installed a newer release)
	// is not lowered by undoing it.
	t.Run("floor raised since", func(t *testing.T) {
		ut := tried(t, "1.2.3")
		ut.cli.do = installs(ut.dir, "1.3.0-rc.1")
		if code := ut.u.updateCmd([]string{"--channel", "beta", "--dir", ut.dir, "--yes"}); code != 0 {
			t.Fatalf("beta: %d %s", code, ut.errOut)
		}
		ut.setState(t, "release.version", "1.3.0\n")
		ut.db.steps = nil
		if code := ut.u.rollbackCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
			t.Fatalf("rollback: %d %s", code, ut.errOut)
		}
		if got := ut.state("release.version"); got != "1.3.0\n" {
			t.Fatalf("release.version %q, want 1.3.0", got)
		}
	})
}

// The check and the menu offer no release that bobres update refuses as
// older than one this server installed before.
func TestUpdateCheckBelowFloor(t *testing.T) {
	ut := newUpdateTest(t, "")
	ut.setState(t, "release.version", "1.4.0\n")
	newer, code := ut.u.checkNewer(context.Background(), ut.dir, "", "")
	if code != 0 || newer || !strings.Contains(ut.out.String(), "Newer:   yes, but older than release 1.4.0, which this server installed before") {
		t.Fatalf("check: %v %d %s %s", newer, code, ut.out, ut.errOut)
	}
	m := opsMenu(ut.dir, "", ut.u.ops, ut.out, ut.errOut)
	m.upd = ut.u
	m.update()
	if len(ut.cli.calls) != 0 || m.quit {
		t.Fatalf("the menu updated: %q", ut.cli.calls)
	}
	if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 1 ||
		!strings.Contains(ut.errOut.String(), "release 1.3.0 is older than release 1.4.0, which this server installed before") {
		t.Fatalf("update: %d %s", code, ut.errOut)
	}
	_ = os.Remove(filepath.Join(ut.u.stateDir, "release.version"))
	ut.unchanged(t)
}

// licenseText is core's `license status` output with state and, when set,
// updates until (the rows printLicense writes).
func licenseText(state, until string) string {
	row := func(k, v string) string { return fmt.Sprintf("%-15s %s\n", k+":", v) }
	s := row("State", state) + row("Install id", "0d4c6c1e-0000-4000-8000-000000000001") + row("Domain", "panel.example.com") + row("Public keys", "1")
	if strings.HasPrefix(state, "missing") || strings.HasPrefix(state, "invalid") {
		return s
	}
	s += row("License", "lic_7 (pro)") + row("Expires", "2027-01-01 (81 days left)")
	if until != "" {
		s += row("Updates until", until)
	}
	return s
}

// A valid license whose update date (updates_until) is before the release
// was issued gets an advisory note, never a refusal: update installs the
// release, the check and the menu offer it. Security updates are never
// withheld.
func TestUpdateLicenseCoverage(t *testing.T) {
	// The release (1.3.0) was issued on 2026-10-10.
	withLicense := func(t *testing.T, text string, err error) *updateTest {
		ut := newUpdateTest(t, "")
		ut.u.licenseStatus = func(_ context.Context, dir string) ([]byte, error) {
			if dir != ut.dir {
				t.Errorf("license of %s", dir)
			}
			return []byte(text), err
		}
		return ut
	}
	const note = "Note:    this release (issued on 2026-10-10) is newer than your license's update date (2026-10-09)."
	for name, state := range map[string]string{
		"active":  "active",
		"grace":   "grace (expired; everything still works: renew it)",
		"expired": "expired (past its grace period: the trial or the free tier applies; renew it)",
	} {
		t.Run("noted "+name, func(t *testing.T) {
			ut := withLicense(t, licenseText(state, "2026-10-09"), nil)
			ut.cli.do = installs(ut.dir, "1.3.0")
			if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("got %d %s %s", code, ut.out, ut.errOut)
			}
			if !strings.Contains(ut.out.String(), note) || !strings.Contains(ut.out.String(), "installed all the same") {
				t.Fatalf("no note about the update date:\n%s", ut.out)
			}
			if strings.Contains(ut.errOut.String(), "newer than your license covers") || len(ut.cli.calls) == 0 {
				t.Fatalf("the update was refused: %s %q", ut.errOut, ut.cli.calls)
			}
			if got := ut.state("release.version"); got != "1.3.0\n" {
				t.Fatalf("release.version %q, want 1.3.0", got)
			}
		})
	}
	t.Run("check offers it with the note", func(t *testing.T) {
		ut := withLicense(t, licenseText("active", "2026-10-09"), nil)
		newer, code := ut.u.checkNewer(context.Background(), ut.dir, "", "")
		if code != 0 || !newer || !strings.Contains(ut.out.String(), "Newer:   yes. Update with: sudo bobres update") ||
			!strings.Contains(ut.out.String(), note) || strings.Contains(ut.out.String(), "not covered") {
			t.Fatalf("got %v %d %s %s", newer, code, ut.out, ut.errOut)
		}
		// The menu offers the update too: it asks, and nothing changes until
		// the owner says yes.
		ut.out.Reset()
		m := opsMenu(ut.dir, "", ut.u.ops, ut.out, ut.errOut)
		m.upd = ut.u
		m.update()
		if !strings.Contains(ut.out.String(), "Update BOBRES from 1.2.3 to 1.3.0?") || !strings.Contains(ut.out.String(), "Cancelled. Nothing was changed.") {
			t.Fatalf("the menu did not offer the update:\n%s %s", ut.out, ut.errOut)
		}
		ut.unchanged(t)
	})
	allowed := map[string]struct {
		text string
		err  error
		warn bool
	}{
		"issued on the last day": {text: licenseText("active", "2026-10-10")},
		"no end":                 {text: licenseText("active", "")},
		"no license":             {text: licenseText("missing (no license: the free tier applies when the trial is over)", "")},
		"another install's":      {text: licenseText("invalid: bound to another install (install_id); counts as no license", "2026-01-01")},
		"core down":              {text: "service \"core\" is not running\n", err: errors.New("exit status 1"), warn: true},
		"an old core":            {text: "usage: license status | license import\n", warn: true},
	}
	for name, tc := range allowed {
		t.Run("allowed "+name, func(t *testing.T) {
			ut := withLicense(t, tc.text, tc.err)
			ut.cli.do = installs(ut.dir, "1.3.0")
			if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
				t.Fatalf("got %d %s %s", code, ut.out, ut.errOut)
			}
			if warned := strings.Contains(ut.errOut.String(), "the license could not be read"); warned != tc.warn {
				t.Fatalf("warning %v: %s", warned, ut.errOut)
			}
			if strings.Contains(ut.out.String(), "Note:") {
				t.Fatalf("a note about the update date where the release is covered or there is no date: %s", ut.out)
			}
		})
	}
	// A release that does not say when it was issued is not refused.
	t.Run("allowed release without a date", func(t *testing.T) {
		ut := withLicense(t, licenseText("active", "2026-01-01"), nil)
		ut.gh.latest = signedChannelRelease(t, ut.root, "1.3.0", "stable", "", []byte(newCLI))
		ut.cli.do = installs(ut.dir, "1.3.0")
		if code := ut.u.updateCmd([]string{"--dir", ut.dir, "--yes"}); code != 0 {
			t.Fatalf("got %d %s", code, ut.errOut)
		}
	})
	// --check without an install asks no license.
	t.Run("check without an install", func(t *testing.T) {
		ut := withLicense(t, licenseText("active", "2026-01-01"), nil)
		ut.u.licenseStatus = func(context.Context, string) ([]byte, error) {
			t.Error("asked core without an install")
			return nil, nil
		}
		if code := ut.u.updateCmd([]string{"--check", "--dir", t.TempDir()}); code != 0 || !strings.Contains(ut.out.String(), "Newer:   yes. Update with") {
			t.Fatalf("got %d %s %s", code, ut.out, ut.errOut)
		}
	})
}

func TestParseUpdatesUntil(t *testing.T) {
	for name, tc := range map[string]struct {
		text  string
		until string
		known bool
	}{
		"active with an end":  {licenseText("active", "2027-09-30"), "2027-09-30", true},
		"expired with an end": {licenseText("expired (past its grace period: the trial or the free tier applies; renew it)", "2027-09-30"), "2027-09-30", true},
		"no end":              {licenseText("grace (expired; everything still works: renew it)", ""), "", true},
		"missing":             {licenseText("missing (no license: the free tier applies when the trial is over)", ""), "", true},
		"invalid":             {"State:          invalid: signature does not verify (bad_signature); counts as no license\nUpdates until:  2020-01-01\n", "", true},
		"not that output":     {"Error response from daemon: container is restarting\n", "", false},
		"empty":               {"", "", false},
	} {
		until, known := parseUpdatesUntil([]byte(tc.text))
		if until != tc.until || known != tc.known {
			t.Errorf("%s: %q %v", name, until, known)
		}
	}
}
