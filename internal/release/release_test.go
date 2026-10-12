package release

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

type keyPair struct {
	pub  string // base64 SPKI, as keys.txt and the installer carry it
	priv ed25519.PrivateKey
}

func newKey(t *testing.T) keyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return keyPair{pub: base64.StdEncoding.EncodeToString(der), priv: priv}
}

// signedRelease is a release's metadata files, as tools/sign and release.yml
// publish them.
type signedRelease struct {
	root, rel       keyPair
	keys, sums      []byte
	keysSig, sumSig []byte
}

func makeRelease(t *testing.T, version, keyExtra string) *signedRelease {
	t.Helper()
	return makeChannelRelease(t, version, ChannelStable, newKey(t), keyExtra)
}

// makeChannelRelease is makeRelease signed for channel, its key list by root.
func makeChannelRelease(t *testing.T, version, channel string, root keyPair, keyExtra string) *signedRelease {
	t.Helper()
	r := &signedRelease{root: root, rel: newKey(t)}
	r.keys = []byte("bobres-keys v1\nseq 3\nexpires 2027-09-30T15:15Z\nmin_version 0.0.1\nkey rel-a " + r.rel.pub + "\n" + keyExtra)
	r.sums = []byte("# bobres-release v1\n# version " + version + "\n# channel " + channel + "\n# key_id rel-a\n" +
		"# issued 2026-10-10T10:00Z\n# expires 2026-11-24T10:00Z\nabc123  bobres_linux_amd64\n")
	r.sign()
	return r
}

// files are r's files as a release publishes them.
func (r *signedRelease) files() map[string][]byte {
	return map[string][]byte{"checksums.txt": r.sums, "checksums.txt.sig": r.sumSig, "keys.txt": r.keys, "keys.txt.sig.2": r.keysSig}
}

func (r *signedRelease) sign() {
	r.keysSig = ed25519.Sign(r.root.priv, r.keys)
	r.sumSig = ed25519.Sign(r.rel.priv, r.sums)
}

// verify checks r with an unrelated root listed first, as with two roots.
func (r *signedRelease) verify() (*Release, error) {
	return Verify([]string{Roots[0], r.root.pub}, r.keys, [][]byte{r.keysSig}, r.sums, r.sumSig, testNow)
}

func TestVerify(t *testing.T) {
	r := makeRelease(t, "1.4.0", "")
	rel, err := r.verify()
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "1.4.0" || rel.Channel != "stable" || rel.KeyID != "rel-a" || rel.KeysSeq != 3 ||
		rel.Issued != "2026-10-10T10:00Z" || rel.Expires != "2026-11-24T10:00Z" {
		t.Fatalf("release: %+v", rel)
	}

	cases := map[string]struct {
		edit func(r *signedRelease)
		want string
	}{
		"tampered checksums": {func(r *signedRelease) { r.sums = append(r.sums, "evil  bobres_linux_arm64\n"...) }, "release signature is invalid"},
		"tampered key list":  {func(r *signedRelease) { r.keys = append(r.keys, "key rel-b x\n"...) }, "key list signature is invalid"},
		"unsigned":           {func(r *signedRelease) { r.sumSig = nil }, "release signature is invalid"},
		"no key list signature": {func(r *signedRelease) {
			r.keysSig = nil
		}, "key list signature is invalid"},
		"unknown root": {func(r *signedRelease) {
			other := newKey(t)
			r.keysSig = ed25519.Sign(other.priv, r.keys)
		}, "key list signature is invalid"},
		"signed by another release key": {func(r *signedRelease) {
			r.sumSig = ed25519.Sign(newKey(t).priv, r.sums)
		}, "release signature is invalid"},
		"revoked": {func(r *signedRelease) {
			r.keys = append(r.keys, "revoked rel-a\n"...)
			r.sign()
		}, "revoked"},
		"key not listed": {func(r *signedRelease) {
			r.sums = []byte(strings.Replace(string(r.sums), "key_id rel-a", "key_id rel-z", 1))
			r.sign()
		}, "not in the signed key list"},
		"key list expired": {func(r *signedRelease) {
			r.keys = []byte(strings.Replace(string(r.keys), "2027-09-30T15:15Z", "2026-10-01T00:00Z", 1))
			r.sign()
		}, "key list expired"},
		"release expired": {func(r *signedRelease) {
			r.sums = []byte(strings.Replace(string(r.sums), "expires 2026-11-24T10:00Z", "expires 2026-10-11T11:59Z", 1))
			r.sign()
		}, "release metadata expired"},
		"below min_version": {func(r *signedRelease) {
			r.keys = []byte(strings.Replace(string(r.keys), "min_version 0.0.1", "min_version 2.0.0", 1))
			r.sign()
		}, "below the minimum"},
		"bad format": {func(r *signedRelease) {
			r.keys = []byte(strings.Replace(string(r.keys), "bobres-keys v1", "bobres-keys v2", 1))
			r.sign()
		}, "unknown key list format"},
		"bad seq": {func(r *signedRelease) {
			r.keys = []byte(strings.Replace(string(r.keys), "seq 3", "seq -3", 1))
			r.sign()
		}, "bad key list seq"},
		"no header": {func(r *signedRelease) {
			r.sums = []byte("abc123  bobres_linux_amd64\n")
			r.sign()
		}, "missing version/key_id/expires"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := makeRelease(t, "1.4.0", "")
			tc.edit(r)
			if _, err := r.verify(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// The real key list in install/keys verifies against the embedded roots, and
// they are the installer's.
func TestEmbeddedRootsMatchInstaller(t *testing.T) {
	script, err := os.ReadFile("../../install/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range Roots {
		if !strings.Contains(string(script), root) {
			t.Errorf("root %s is not in install/install.sh", root)
		}
	}
	keys, err := os.ReadFile("../../install/keys/keys.txt")
	if err != nil {
		t.Fatal(err)
	}
	var sigs [][]byte
	for _, n := range []string{"1", "2"} {
		s, err := os.ReadFile("../../install/keys/keys.txt.sig." + n)
		if err != nil {
			t.Fatal(err)
		}
		sigs = append(sigs, s)
		verified := false
		for _, root := range Roots {
			pub, err := parseKey(root)
			if err != nil {
				t.Fatal(err)
			}
			verified = verified || ed25519.Verify(pub, keys, s)
		}
		if !verified {
			t.Errorf("keys.txt.sig.%s: no embedded root verifies it", n)
		}
	}
	// The key list is accepted; the made-up checksums.txt is not.
	_, err = Verify(Roots, keys, sigs, []byte("# version 1.0.0\n# key_id rel-2026a\n# expires 2027-01-01T00:00Z\n"), make([]byte, 64),
		time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "release signature is invalid") {
		t.Fatalf("got %v", err)
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0}, {"v1.2.3", "1.2.3", 0}, {"1.2.10", "1.2.9", 1}, {"1.10.0", "1.9.9", 1},
		{"2.0.0", "10.0.0", -1}, {"1.2.3-beta.1", "1.2.3", -1}, {"1.2.3-beta.2", "1.2.3-beta.1", 1},
		{"1.2.3+build5", "1.2.3", 0}, {"dev", "0.0.1", -1}, {"1.2", "1.2.0", -1},
		// numeric pre-release identifiers are numbers (semver 2.0, section 11)
		{"1.2.0-rc.10", "1.2.0-rc.9", 1}, {"1.2.0-rc.9", "1.2.0-rc.10", -1}, {"1.2.0-2", "1.2.0-10", -1},
		{"1.2.0-rc.1", "1.2.0-rc.1.1", -1}, {"1.2.0-1", "1.2.0-a", -1}, {"1.2.0-rc.a", "1.2.0-rc.1", 1},
		{"1.2.0-rc.99999999999999999999", "1.2.0-rc.5", 1}, {"1.2.0-rc.01", "1.2.0-rc.1", -1},
		{"1.2.0-rc.1+b2", "1.2.0-rc.1", 0}, {"1.1.9", "1.2.0-alpha", -1},
		// numbers too long for an int are still numbers: by their value, not their text
		// (tests/install_verify_test.sh holds the same pairs for install.sh's semver_lt)
		{"1.2.0-rc.100000000000000000000", "1.2.0-rc.9223372036854775807", 1},
		{"1.2.0-rc.9223372036854775807", "1.2.0-rc.100000000000000000000", -1},
		{"1.2.0-rc.99999999999999999999", "1.2.0-rc.100000000000000000000", -1},
		{"1.2.0-rc.100000000000000000000", "1.2.0-rc.99999999999999999999", 1},
		{"1.2.0-rc.0100000000000000000000", "1.2.0-rc.99999999999999999999", 1},
		{"1.2.0-rc.0", "1.2.0-rc.00", -1}, {"1.2.0-rc.00", "1.2.0-rc.1", -1},
	} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	// The order semver 2.0 gives as its example, each before the next.
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0"}
	for i, a := range order {
		for j, b := range order {
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%q, %q) = %d, want %d", a, b, got, want)
			}
		}
	}
	for v, want := range map[string]bool{"1.2.3": true, "v0.1.0": true, "dev": false, "": false, "1.2": false, "1.x.3": false, "1.-2.3": false} {
		if Valid(v) != want {
			t.Errorf("Valid(%q) != %v", v, want)
		}
	}
	for v, want := range map[string]bool{"1.2.3-rc.1": true, "v1.2.3-beta": true, "1.2.3": false, "1.2.3+b1": false, "dev": false, "1.2-rc.1": false} {
		if Prerelease(v) != want {
			t.Errorf("Prerelease(%q) != %v", v, want)
		}
	}
}

// install.sh's semver_lt orders releases as Compare does (the bot's check
// and bobres update must agree with the installer on what is newer and on
// the downgrade floor): every ordered pair of these, through bash.
func TestCompareMatchesInstaller(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	versions := []string{
		"1.2.0", "1.2.3", "v1.2.3+b5", "1.10.0", "1.2.0-alpha", "1.2.0-alpha.beta", "1.2.0-1", "1.2.0-rc.1", "1.2.0-rc.01",
		"1.2.0-rc.1.1", "1.2.0-rc.9", "1.2.0-rc.10", "1.2.0-rc.a", "1.2.0-rc.9223372036854775807",
		"1.2.0-rc.99999999999999999999", "1.2.0-rc.100000000000000000000",
	}
	script := `. "$1"; shift; for a in "$@"; do for b in "$@"; do if semver_lt "$a" "$b"; then echo 1; else echo 0; fi; done; done`
	cmd := exec.Command(bash, append([]string{"-c", script, "bash", "../../install/install.sh"}, versions...)...) //nolint:gosec // the repository's own script
	cmd.Env = append(os.Environ(), "BOBRES_INSTALL_SOURCE_ONLY=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	got := strings.Fields(string(out))
	if len(got) != len(versions)*len(versions) {
		t.Fatalf("bash printed %d answers, want %d", len(got), len(versions)*len(versions))
	}
	i := 0
	for _, a := range versions {
		for _, b := range versions {
			if lt := got[i] == "1"; lt != (Compare(a, b) < 0) {
				t.Errorf("semver_lt %s %s is %v, Compare is %d", a, b, lt, Compare(a, b))
			}
			i++
		}
	}
}

func TestParseHeader(t *testing.T) {
	h := ParseHeader([]byte("# bobres-release v1\n# version 1.5.0-rc.2\n# channel beta\n# key_id rel-a\n# issued 2026-10-10T10:00Z\n" +
		"# issued 2030-01-01T00:00Z\n# expires 2026-11-24T10:00Z\nabc  bobres_linux_amd64\n"))
	want := Header{Version: "1.5.0-rc.2", Channel: "beta", KeyID: "rel-a", Issued: "2026-10-10T10:00Z", Expires: "2026-11-24T10:00Z"}
	if h != want {
		t.Fatalf("got %+v", h)
	}
	if h := ParseHeader([]byte("# version 1.0.0\n")); h.Issued != "" {
		t.Fatalf("no issued line: %+v", h)
	}
}

// fakeGitHub serves a release's files; missing ones are 404.
func fakeGitHub(t *testing.T, files map[string][]byte) (*httptest.Server, *sync.Map) {
	t.Helper()
	var hits sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/releases/latest/download/")
		hits.Store(name, true)
		b, ok := files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// testSource is the Source of a fake GitHub at base: the latest stable
// release under /releases/latest/download, the API at /api/releases, each
// release under /releases/download/vX.
func testSource(base string) Source {
	return Source{
		Latest: base + "/releases/latest/download", API: base + "/api/releases",
		Download: func(v string) string { return base + "/releases/download/v" + v },
		HC:       &http.Client{Timeout: 5 * time.Second},
	}
}

func newTestChecker(current, base string, roots ...string) *Checker {
	return newChannelChecker(current, ChannelStable, base, roots...)
}

func newChannelChecker(current, channel, base string, roots ...string) *Checker {
	c := NewChecker(current, channel, nil)
	c.src = testSource(base)
	c.roots, c.now = roots, func() time.Time { return testNow }
	return c
}

// fakeReleases is GitHub with several releases: the API lists them (as
// tags) and each one's files are under /releases/download/vX; latest is
// what /releases/latest/download serves.
type fakeReleases struct {
	srv    *httptest.Server
	list   []byte // the API's answer
	status int    // the API's status (200 when 0)
	byTag  map[string]map[string][]byte
	latest map[string][]byte
	asked  sync.Map // request paths
}

func newFakeReleases(t *testing.T) *fakeReleases {
	t.Helper()
	f := &fakeReleases{byTag: map[string]map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.asked.Store(r.URL.Path, r.URL.RawQuery)
		switch {
		case r.URL.Path == "/api/releases":
			if r.Header.Get("Accept") != "application/vnd.github+json" {
				http.Error(w, "accept", http.StatusBadRequest)
				return
			}
			if f.status != 0 {
				w.WriteHeader(f.status)
			}
			_, _ = w.Write(f.list)
			return
		case strings.HasPrefix(r.URL.Path, "/releases/latest/download/"):
			if b, ok := f.latest[strings.TrimPrefix(r.URL.Path, "/releases/latest/download/")]; ok {
				_, _ = w.Write(b)
				return
			}
		case strings.HasPrefix(r.URL.Path, "/releases/download/v"):
			tag, name, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/releases/download/v"), "/")
			if b, ok := f.byTag[tag][name]; ok {
				_, _ = w.Write(b)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestChecker(t *testing.T) {
	r := makeRelease(t, "1.4.0", "")
	files := func() map[string][]byte {
		return map[string][]byte{"checksums.txt": r.sums, "checksums.txt.sig": r.sumSig, "keys.txt": r.keys, "keys.txt.sig.2": r.keysSig}
	}

	t.Run("newer and verified", func(t *testing.T) {
		srv, hits := fakeGitHub(t, files())
		c := newTestChecker("v1.3.2", srv.URL, r.root.pub)
		if st := c.Status(); st.Error != ErrNotChecked || st.Current != "v1.3.2" || st.CheckedAt != 0 {
			t.Fatalf("before: %+v", st)
		}
		st := c.Check(context.Background())
		want := Status{Current: "v1.3.2", Latest: "1.4.0", Channel: "stable", Issued: "2026-10-10T10:00Z", Newer: true, Verified: true, CheckedAt: testNow.Unix()}
		if st != want || c.Status() != want {
			t.Fatalf("got %+v", st)
		}
		if _, ok := hits.Load("keys.txt.sig.4"); !ok {
			t.Error("did not look for every key list signature")
		}
	})
	for current, newer := range map[string]bool{"1.4.0": false, "1.5.0": false, "dev": false, "1.4.0-beta.1": true} {
		t.Run("current "+current, func(t *testing.T) {
			srv, _ := fakeGitHub(t, files())
			st := newTestChecker(current, srv.URL, r.root.pub).Check(context.Background())
			if st.Newer != newer || !st.Verified || st.Latest != "1.4.0" || st.Error != "" {
				t.Fatalf("got %+v", st)
			}
		})
	}

	trust := map[string]func(f map[string][]byte){
		"tampered": func(f map[string][]byte) {
			f["checksums.txt"] = []byte(strings.Replace(string(r.sums), "abc123", "def456", 1))
		},
		"unsigned":    func(f map[string][]byte) { delete(f, "checksums.txt.sig") },
		"no key list": func(f map[string][]byte) { delete(f, "keys.txt") },
		"no key list signature": func(f map[string][]byte) {
			delete(f, "keys.txt.sig.2")
		},
	}
	for name, edit := range trust {
		t.Run(name, func(t *testing.T) {
			f := files()
			edit(f)
			srv, _ := fakeGitHub(t, f)
			st := newTestChecker("1.0.0", srv.URL, r.root.pub).Check(context.Background())
			if st.Error != ErrBadSignature || st.Verified || st.Newer || st.Latest != "1.4.0" || st.Detail == "" {
				t.Fatalf("got %+v", st)
			}
		})
	}

	t.Run("wrong root", func(t *testing.T) {
		srv, _ := fakeGitHub(t, files())
		st := newTestChecker("1.0.0", srv.URL, Roots...).Check(context.Background())
		if st.Error != ErrBadSignature || st.Verified || st.Newer {
			t.Fatalf("got %+v", st)
		}
	})
	t.Run("no release", func(t *testing.T) {
		srv, _ := fakeGitHub(t, nil)
		st := newTestChecker("1.0.0", srv.URL, r.root.pub).Check(context.Background())
		if st.Error != ErrNoRelease || st.Latest != "" || st.Verified {
			t.Fatalf("got %+v", st)
		}
	})
	t.Run("no version header", func(t *testing.T) {
		srv, _ := fakeGitHub(t, map[string][]byte{"checksums.txt": []byte("abc  x\n")})
		st := newTestChecker("1.0.0", srv.URL, r.root.pub).Check(context.Background())
		if st.Error != ErrInvalid || st.Latest != "" {
			t.Fatalf("got %+v", st)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		// Before any verified check: nothing is known.
		srv, _ := fakeGitHub(t, files())
		srv.Close()
		c := newTestChecker("1.0.0", srv.URL, r.root.pub)
		st := c.Check(context.Background())
		if st.Error != ErrUnreachable || st.Latest != "" || st.Verified || st.Newer || st.CheckedAt != testNow.Unix() {
			t.Fatalf("never verified: %+v", st)
		}

		// After one: the newer release stays known through an outage, with
		// the failure and when it happened.
		srv, _ = fakeGitHub(t, files())
		c = newTestChecker("1.0.0", srv.URL, r.root.pub)
		if st := c.Check(context.Background()); !st.Verified || !st.Newer {
			t.Fatalf("first: %+v", st)
		}
		srv.Close()
		later := testNow.Add(time.Hour)
		c.now = func() time.Time { return later }
		st = c.Check(context.Background())
		want := Status{Current: "1.0.0", Latest: "1.4.0", Channel: "stable", Issued: "2026-10-10T10:00Z", Newer: true, Verified: true,
			CheckedAt: later.Unix(), Error: ErrUnreachable, Detail: st.Detail}
		if st != want || st.Detail == "" {
			t.Fatalf("got %+v", st)
		}
		if c.Status() != st {
			t.Fatal("the failed check was not kept")
		}

		// A release that is gone or fails the checks is not kept.
		srv, _ = fakeGitHub(t, nil)
		c.src = testSource(srv.URL)
		if st := c.Check(context.Background()); st.Error != ErrNoRelease || st.Latest != "" || st.Newer || st.Verified {
			t.Fatalf("no release after a verified one: %+v", st)
		}
	})
	t.Run("unreachable after a bad signature", func(t *testing.T) {
		f := files()
		delete(f, "checksums.txt.sig")
		srv, _ := fakeGitHub(t, f)
		c := newTestChecker("1.0.0", srv.URL, r.root.pub)
		if st := c.Check(context.Background()); st.Error != ErrBadSignature || st.Latest != "1.4.0" {
			t.Fatalf("first: %+v", st)
		}
		srv.Close()
		// an unverified claim is not carried over as if it were known
		if st := c.Check(context.Background()); st.Error != ErrUnreachable || st.Latest != "" || st.Verified || st.Newer {
			t.Fatalf("got %+v", st)
		}
	})
	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		t.Cleanup(srv.Close)
		st := newTestChecker("1.0.0", srv.URL, r.root.pub).Check(context.Background())
		if st.Error != ErrUnreachable {
			t.Fatalf("got %+v", st)
		}
	})
	t.Run("run stops", func(t *testing.T) {
		srv, _ := fakeGitHub(t, files())
		c := newTestChecker("1.0.0", srv.URL, r.root.pub)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx) }()
		deadline := time.Now().Add(5 * time.Second)
		for c.Status().Error == ErrNotChecked && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		if err := <-done; err != nil || !c.Status().Verified {
			t.Fatalf("run: %v %+v", err, c.Status())
		}
	})
}

func TestDownloadURL(t *testing.T) {
	const want = "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/download/v1.4.0"
	for _, v := range []string{"1.4.0", "v1.4.0", " 1.4.0 "} {
		if got := DownloadURL(v); got != want {
			t.Errorf("DownloadURL(%q) = %q", v, got)
		}
	}
	if DefaultBaseURL != "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/latest/download" {
		t.Fatalf("DefaultBaseURL = %q", DefaultBaseURL)
	}
}

func TestChannels(t *testing.T) {
	for in, want := range map[string]string{"": "stable", "stable": "stable", " Beta ": "beta", "BETA": "beta"} {
		if got, err := ParseChannel(in); err != nil || got != want {
			t.Errorf("ParseChannel(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"nightly", "latest", "stable,beta"} {
		if _, err := ParseChannel(bad); err == nil {
			t.Errorf("ParseChannel(%q) accepted", bad)
		}
	}
	for _, tc := range []struct {
		channel, released string
		want              bool
	}{
		{"stable", "stable", true}, {"stable", "", true}, {"stable", "beta", false},
		{"beta", "beta", true}, {"beta", "stable", true}, {"beta", "", true},
	} {
		if got := ChannelAllows(tc.channel, tc.released); got != tc.want {
			t.Errorf("ChannelAllows(%q, %q) = %v", tc.channel, tc.released, got)
		}
	}
}

func TestCovered(t *testing.T) {
	for _, tc := range []struct {
		until, issued string
		want          bool
	}{
		{"2026-10-10", "2026-10-10T23:59Z", true}, // the last covered day
		{"2026-10-10", "2026-10-11T00:00Z", false},
		{"2026-10-09", "2026-10-10T10:00Z", false},
		{"2027-01-01", "2026-10-10T10:00Z", true},
		{"", "2026-10-10T10:00Z", true},           // no end
		{"2026-01-01", "", true},                  // the release does not say
		{"2026-01-01", "yesterday", true},         // nor in a form that reads
		{"01/01/2026", "2026-10-10T10:00Z", true}, // an end that is not a date gates nothing
	} {
		if got := Covered(tc.until, tc.issued); got != tc.want {
			t.Errorf("Covered(%q, %q) = %v", tc.until, tc.issued, got)
		}
	}
	if IssuedDay("2026-10-10T23:59Z") != "2026-10-10" || IssuedDay("") != "" || IssuedDay("2026-10-10") != "" {
		t.Error("IssuedDay")
	}
}

func TestResolve(t *testing.T) {
	f := newFakeReleases(t)
	src := testSource(f.srv.URL)
	ctx := context.Background()

	// Stable: latest/download, whatever version it holds; GitHub's API is
	// not asked.
	if base, want, err := src.Resolve(ctx, ChannelStable); err != nil || base != src.Latest || want != "" {
		t.Fatalf("stable: %q %q %v", base, want, err)
	}
	if _, ok := f.asked.Load("/api/releases"); ok {
		t.Fatal("stable asked the API")
	}

	// Beta: the highest published version, numeric pre-release parts as
	// numbers; drafts and tags that are not versions are left out.
	f.list = []byte(`[
		{"tag_name": "v2.0.0", "draft": true, "prerelease": false},
		{"tag_name": "v1.5.0-rc.9", "draft": false, "prerelease": true},
		{"tag_name": "v1.5.0-rc.10", "draft": false, "prerelease": true, "body": "notes", "assets": [{"name": "checksums.txt"}]},
		{"tag_name": "nightly", "draft": false, "prerelease": true},
		{"tag_name": "v9.9", "draft": false},
		{"tag_name": "vv3.0.0", "draft": false},
		{"tag_name": "v1.4.2", "draft": false, "prerelease": false}
	]`)
	base, want, err := src.Resolve(ctx, ChannelBeta)
	if err != nil || base != f.srv.URL+"/releases/download/v1.5.0-rc.10" || want != "1.5.0-rc.10" {
		t.Fatalf("beta: %q %q %v", base, want, err)
	}
	if q, _ := f.asked.Load("/api/releases"); q != "per_page=30" {
		t.Fatalf("query %v", q)
	}
	// A stable release above every pre-release is beta's newest too.
	f.list = []byte(`[{"tag_name": "v1.5.0"}, {"tag_name": "v1.5.0-rc.10", "prerelease": true}]`)
	if _, want, err := src.Resolve(ctx, ChannelBeta); err != nil || want != "1.5.0" {
		t.Fatalf("stable above the betas: %q %v", want, err)
	}

	// Nothing published, or no such repository: not found.
	for _, list := range []string{`[]`, `[{"tag_name": "v1.0.0", "draft": true}]`} {
		f.list = []byte(list)
		if _, _, err := src.Resolve(ctx, ChannelBeta); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: %v", list, err)
		}
	}
	f.status = http.StatusNotFound
	if _, _, err := src.Resolve(ctx, ChannelBeta); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
	// A refusal (the hourly limit), a server error or an answer that is not
	// a list is not "nothing published".
	f.list = []byte(`{"message": "API rate limit exceeded"}`)
	for status, want := range map[int]string{http.StatusForbidden: "60 requests an hour", http.StatusTooManyRequests: "60 requests an hour",
		http.StatusBadGateway: "answered 502", http.StatusOK: "cannot unmarshal"} {
		f.status = status
		if _, _, err := src.Resolve(ctx, ChannelBeta); err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: %v", status, err)
		}
	}
	f.srv.Close()
	if _, _, err := src.Resolve(ctx, ChannelBeta); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("unreachable: %v", err)
	}
}

func TestCheckerChannels(t *testing.T) {
	root := newKey(t)
	stable := makeChannelRelease(t, "1.4.2", ChannelStable, root, "")
	rc9 := makeChannelRelease(t, "1.5.0-rc.9", ChannelBeta, root, "")
	rc10 := makeChannelRelease(t, "1.5.0-rc.10", ChannelBeta, root, "")
	setup := func(t *testing.T) *fakeReleases {
		f := newFakeReleases(t)
		f.list = []byte(`[{"tag_name": "v1.5.0-rc.10", "prerelease": true}, {"tag_name": "v1.5.0-rc.9", "prerelease": true}, {"tag_name": "v1.4.2"}]`)
		f.latest = stable.files()
		f.byTag["1.4.2"], f.byTag["1.5.0-rc.9"], f.byTag["1.5.0-rc.10"] = stable.files(), rc9.files(), rc10.files()
		return f
	}

	t.Run("beta finds the newest pre-release", func(t *testing.T) {
		f := setup(t)
		c := newChannelChecker("1.4.2", ChannelBeta, f.srv.URL, root.pub)
		if st := c.Status(); st.Channel != ChannelBeta || st.Error != ErrNotChecked {
			t.Fatalf("before: %+v", st)
		}
		st := c.Check(context.Background())
		want := Status{Current: "1.4.2", Latest: "1.5.0-rc.10", Channel: "beta", Issued: "2026-10-10T10:00Z", Newer: true, Verified: true, CheckedAt: testNow.Unix()}
		if st != want {
			t.Fatalf("got %+v", st)
		}
		// on the newest beta already: nothing newer
		if st := newChannelChecker("v1.5.0-rc.10", ChannelBeta, f.srv.URL, root.pub).Check(context.Background()); st.Newer || !st.Verified {
			t.Fatalf("current: %+v", st)
		}
	})
	t.Run("stable stays on stable", func(t *testing.T) {
		f := setup(t)
		st := newChannelChecker("1.4.2", ChannelStable, f.srv.URL, root.pub).Check(context.Background())
		if st.Latest != "1.4.2" || st.Newer || !st.Verified || st.Channel != "stable" {
			t.Fatalf("got %+v", st)
		}
		if _, ok := f.asked.Load("/api/releases"); ok {
			t.Fatal("stable asked the API")
		}
	})
	t.Run("stable refuses a release signed for beta", func(t *testing.T) {
		f := setup(t)
		f.latest = rc10.files() // a pre-release published as the latest by mistake
		st := newChannelChecker("1.4.2", ChannelStable, f.srv.URL, root.pub).Check(context.Background())
		if st.Error != ErrBadSignature || st.Verified || st.Newer || st.Latest != "1.5.0-rc.10" || !strings.Contains(st.Detail, "signed for the beta channel") {
			t.Fatalf("got %+v", st)
		}
	})
	t.Run("beta refuses files signed for another version", func(t *testing.T) {
		f := setup(t)
		f.byTag["1.5.0-rc.10"] = rc9.files() // the API names rc.10, its files are rc.9's
		st := newChannelChecker("1.4.2", ChannelBeta, f.srv.URL, root.pub).Check(context.Background())
		if st.Error != ErrBadSignature || st.Verified || st.Newer || !strings.Contains(st.Detail, "signed for 1.5.0-rc.9") {
			t.Fatalf("got %+v", st)
		}
	})
	t.Run("beta without the API", func(t *testing.T) {
		f := setup(t)
		f.status = http.StatusForbidden
		c := newChannelChecker("1.4.2", ChannelBeta, f.srv.URL, root.pub)
		if st := c.Check(context.Background()); st.Error != ErrUnreachable || st.Latest != "" || st.Verified {
			t.Fatalf("refused: %+v", st)
		}
		// after a verified check the release stays known through an outage
		f.status = 0
		if st := c.Check(context.Background()); !st.Verified || st.Latest != "1.5.0-rc.10" {
			t.Fatalf("verified: %+v", st)
		}
		f.status = http.StatusBadGateway
		if st := c.Check(context.Background()); st.Error != ErrUnreachable || st.Latest != "1.5.0-rc.10" || st.Issued != "2026-10-10T10:00Z" || !st.Newer {
			t.Fatalf("outage: %+v", st)
		}
		f.status, f.list = 0, []byte(`[]`)
		if st := c.Check(context.Background()); st.Error != ErrNoRelease || st.Latest != "" || st.Verified {
			t.Fatalf("none: %+v", st)
		}
	})
	t.Run("beta release listed without files", func(t *testing.T) {
		f := setup(t)
		delete(f.byTag, "1.5.0-rc.10")
		if st := newChannelChecker("1.4.2", ChannelBeta, f.srv.URL, root.pub).Check(context.Background()); st.Error != ErrNoRelease || st.Verified {
			t.Fatalf("got %+v", st)
		}
	})
}
