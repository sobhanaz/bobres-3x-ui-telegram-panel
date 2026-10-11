package release

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
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
	r := &signedRelease{root: newKey(t), rel: newKey(t)}
	r.keys = []byte("bobres-keys v1\nseq 3\nexpires 2027-09-30T15:15Z\nmin_version 0.0.1\nkey rel-a " + r.rel.pub + "\n" + keyExtra)
	r.sums = []byte("# bobres-release v1\n# version " + version + "\n# channel stable\n# key_id rel-a\n" +
		"# issued 2026-10-10T10:00Z\n# expires 2026-11-24T10:00Z\nabc123  bobres_linux_amd64\n")
	r.sign()
	return r
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
	if rel.Version != "1.4.0" || rel.Channel != "stable" || rel.KeyID != "rel-a" || rel.KeysSeq != 3 {
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
	} {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	for v, want := range map[string]bool{"1.2.3": true, "v0.1.0": true, "dev": false, "": false, "1.2": false, "1.x.3": false, "1.-2.3": false} {
		if Valid(v) != want {
			t.Errorf("Valid(%q) != %v", v, want)
		}
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

func newTestChecker(current, base string, roots ...string) *Checker {
	c := NewChecker(current, base+"/releases/latest/download", nil)
	c.roots, c.now = roots, func() time.Time { return testNow }
	return c
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
		want := Status{Current: "v1.3.2", Latest: "1.4.0", Channel: "stable", Newer: true, Verified: true, CheckedAt: testNow.Unix()}
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
		srv, _ := fakeGitHub(t, files())
		c := newTestChecker("1.0.0", srv.URL, r.root.pub)
		if st := c.Check(context.Background()); !st.Verified {
			t.Fatalf("first: %+v", st)
		}
		srv.Close()
		st := c.Check(context.Background())
		if st.Error != ErrUnreachable || st.Latest != "" || st.Verified || st.Newer || st.CheckedAt != testNow.Unix() {
			t.Fatalf("got %+v", st)
		}
		if c.Status() != st {
			t.Fatal("the failed check was not kept")
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
