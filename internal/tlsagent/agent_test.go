package tlsagent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

const mainCaddyfile = "{\n\tadmin unix//run/caddy/admin.sock|0220\n\timport /tls/global.caddy\n}\n\nimport /tls/sites.caddy\n"

// world is an agent with its directories and a fake Caddy.
type world struct {
	t      *testing.T
	dir    string
	tlsDir string
	caddy  *fakeCaddy
	agent  *Agent
	logs   *bytes.Buffer
	now    time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	w := &world{t: t, dir: dir, tlsDir: filepath.Join(dir, "tls"), logs: &bytes.Buffer{},
		now: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	for _, d := range []string{w.tlsDir, filepath.Join(dir, "request"), filepath.Join(dir, "result"), filepath.Join(dir, "caddy")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "caddy", "Caddyfile"), []byte(mainCaddyfile), 0o600); err != nil {
		t.Fatal(err)
	}
	w.caddy = newFakeCaddy(t, w.tlsDir)
	w.agent = New(Config{
		RequestPath: filepath.Join(dir, "request", tlsconf.RequestFile), ResultPath: filepath.Join(dir, "result", tlsconf.ResultFile),
		TLSDir: w.tlsDir, Caddyfile: filepath.Join(dir, "caddy", "Caddyfile"), AdminSocket: w.caddy.sock,
		Heartbeat: filepath.Join(dir, "alive"), LoadTimeout: 2 * time.Second, Now: func() time.Time { return w.now },
		Log: slog.New(slog.NewTextHandler(w.logs, nil)),
		// the install's own domain, as the compose file gives the agent (a test
		// with a Zarinpal host sets it on the agent's config)
		Domain: "panel.example.com",
	})
	w.agent.Start()
	return w
}

// ask writes core's request.
func (w *world) ask(r tlsconf.Request) {
	w.t.Helper()
	if err := tlsconf.NewExchange(filepath.Join(w.dir, "request"), filepath.Join(w.dir, "result")).WriteRequest(r); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) step() { w.agent.Step(context.Background()) }

// result is the agent's answer (nil when none).
func (w *world) result() *tlsconf.Result {
	w.t.Helper()
	r, err := tlsconf.NewExchange(filepath.Join(w.dir, "request"), filepath.Join(w.dir, "result")).ReadResult()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func (w *world) file(parts ...string) string {
	b, err := os.ReadFile(filepath.Join(append([]string{w.tlsDir}, parts...)...))
	if err != nil {
		return ""
	}
	return string(b)
}

func (w *world) exists(parts ...string) bool {
	_, err := os.Stat(filepath.Join(append([]string{w.tlsDir}, parts...)...))
	return err == nil
}

// rendered is what Render gives for st.
func rendered(t *testing.T, st tlsconf.State) tlsconf.Files {
	t.Helper()
	f, err := tlsconf.Render(st)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// selfPair is a self-signed certificate for names with its key, as PEM,
// valid from from until until.
func selfPair(t *testing.T, names []string, from, until time.Time) (certPEM, keyPEM string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]},
		DNSNames: names, NotBefore: from, NotAfter: until, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, k.Public(), k)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
}

// A request is applied once: the files Render gives (mode 0640), then one
// forced load of the main Caddyfile with the source headers and no Origin,
// and the answer for its generation.
func TestApplyAuto(t *testing.T) {
	w := newWorld(t)
	w.agent.cfg.ZarinpalHost = "pay.example.net"
	w.step()
	if w.result() != nil || len(w.caddy.all()) != 0 {
		t.Fatal("something was done before any request")
	}
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.example.net"})
	w.step()
	want := rendered(t, tlsconf.State{Mode: tlsconf.ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.example.net"})
	if w.file("global.caddy") != want.Global || w.file("sites.caddy") != want.Sites {
		t.Fatalf("files:\n%s\n%s", w.file("global.caddy"), w.file("sites.caddy"))
	}
	for _, name := range []string{"global.caddy", "sites.caddy"} {
		if fi, err := os.Stat(filepath.Join(w.tlsDir, name)); err != nil || fi.Mode().Perm() != 0o640 {
			t.Fatalf("%s: %v %v", name, fi.Mode(), err)
		}
	}
	loads := w.caddy.all()
	if len(loads) != 1 {
		t.Fatalf("%d loads", len(loads))
	}
	l := loads[0]
	h := l.header
	if l.method != http.MethodPost || l.target != "/load" || l.body != mainCaddyfile || h.Get("Content-Type") != "text/caddyfile" ||
		h.Get("Cache-Control") != "must-revalidate" || h.Get("Caddy-Config-Source-File") != "/etc/caddy/Caddyfile" ||
		h.Get("Caddy-Config-Source-Adapter") != "caddyfile" || h.Get("Origin") != "" || h.Get("Sec-Fetch-Mode") != "" {
		t.Fatalf("load: %s %s %v %q", l.method, l.target, h, l.body)
	}
	if l.sites != want.Sites || l.global != want.Global {
		t.Fatal("Caddy was asked to load before the files were written")
	}
	if r := w.result(); r == nil || r.Generation != 1 || !r.OK || r.Code != "" || r.AppliedAt != w.now.Unix() {
		t.Fatalf("result: %+v", r)
	}
	w.step()
	w.step()
	if len(w.caddy.all()) != 1 {
		t.Fatal("the same generation was applied again")
	}
	if Healthcheck(filepath.Join(w.dir, "alive"), time.Now()) != 0 {
		t.Fatal("no heartbeat")
	}
}

// Manual mode writes the pair before the snippet that names it; leaving
// manual mode removes the pair once Caddy loaded the new files.
func TestApplyManualThenAuto(t *testing.T) {
	w := newWorld(t)
	cert, key := selfPair(t, []string{"panel.example.com"}, w.now.Add(-time.Hour), w.now.Add(90*24*time.Hour))
	w.ask(tlsconf.Request{Generation: 5, Mode: tlsconf.ModeManual, Domain: "panel.example.com", CertPEM: cert, KeyPEM: key})
	w.step()
	if r := w.result(); r == nil || !r.OK || r.Generation != 5 {
		t.Fatalf("manual: %+v", r)
	}
	l := w.caddy.all()[0]
	if !l.pairOnDisk || l.cert != cert || l.key != key || !strings.Contains(l.sites, "tls /tls/certs/cert.pem /tls/certs/key.pem") {
		t.Fatalf("at the load: pair %v\n%s", l.pairOnDisk, l.sites)
	}
	for _, name := range []string{"cert.pem", "key.pem"} {
		if fi, err := os.Stat(filepath.Join(w.tlsDir, "certs", name)); err != nil || fi.Mode().Perm() != 0o640 {
			t.Fatalf("%s: %v %v", name, fi.Mode(), err)
		}
	}
	w.ask(tlsconf.Request{Generation: 6, Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	w.step()
	loads := w.caddy.all()
	if r := w.result(); r == nil || !r.OK || r.Generation != 6 || len(loads) != 2 {
		t.Fatalf("auto: %+v %d", r, len(loads))
	}
	if !loads[1].pairOnDisk || strings.Contains(loads[1].sites, "certs/") {
		t.Fatalf("the pair went before the load, or the snippet still names it:\n%s", loads[1].sites)
	}
	if w.exists("certs", "cert.pem") || w.exists("certs", "key.pem") {
		t.Fatal("the old pair is still on disk")
	}
}

// Caddy refuses: the last good files go back byte for byte (the pair
// too), Caddy loads them again, and the answer carries Caddy's words.
func TestRefusedPutsTheLastGoodFilesBack(t *testing.T) {
	w := newWorld(t)
	certA, keyA := selfPair(t, []string{"panel.example.com"}, w.now.Add(-time.Hour), w.now.Add(90*24*time.Hour))
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeManual, Domain: "panel.example.com", CertPEM: certA, KeyPEM: keyA})
	w.step()
	good := [4]string{w.file("global.caddy"), w.file("sites.caddy"), w.file("certs", "cert.pem"), w.file("certs", "key.pem")}

	w.caddy.setAnswer(func(l load) (int, string) {
		if strings.Contains(l.sites, "shortlived") {
			return http.StatusBadRequest, `{"error":"loading config: unrecognized subdirective 'profile'"}`
		}
		return http.StatusOK, ""
	})
	w.ask(tlsconf.Request{Generation: 2, Mode: tlsconf.ModeIP, Domain: "panel.example.com", IP: "1.2.3.4"})
	w.step()
	r := w.result()
	if r == nil || r.OK || r.Generation != 2 || r.Code != tlsconf.ResultRefused || !strings.Contains(r.Error, "unrecognized subdirective 'profile'") {
		t.Fatalf("result: %+v", r)
	}
	now := [4]string{w.file("global.caddy"), w.file("sites.caddy"), w.file("certs", "cert.pem"), w.file("certs", "key.pem")}
	if now != good {
		t.Fatal("the last good files are not back")
	}
	loads := w.caddy.all()
	if len(loads) != 3 || !strings.Contains(loads[1].sites, "shortlived") || loads[2].sites != good[1] {
		t.Fatalf("%d loads; the second must be the refused files, the third the good ones", len(loads))
	}
	w.step()
	if len(w.caddy.all()) != 3 {
		t.Fatal("a refused generation was tried again")
	}

	// A new pair refused: the old pair is back.
	certB, keyB := selfPair(t, []string{"panel.example.com"}, w.now.Add(-time.Hour), w.now.Add(30*24*time.Hour))
	w.caddy.setAnswer(refuse("loading certificates: bad"))
	w.ask(tlsconf.Request{Generation: 3, Mode: tlsconf.ModeManual, Domain: "panel.example.com", CertPEM: certB, KeyPEM: keyB})
	w.step()
	if r := w.result(); r.Code != tlsconf.ResultRefused || w.file("certs", "cert.pem") != certA || w.file("certs", "key.pem") != keyA {
		t.Fatalf("the old pair is not back: %+v", r)
	}
}

// Caddy does not answer: the new files stay (Caddy reads them when it
// starts), the answer says so, and the load is tried again until Caddy
// takes it.
func TestUnreachableKeepsTheFilesAndRetries(t *testing.T) {
	w := newWorld(t)
	cert, key := selfPair(t, []string{"panel.example.com"}, w.now.Add(-time.Hour), w.now.Add(90*24*time.Hour))
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeManual, Domain: "panel.example.com", CertPEM: cert, KeyPEM: key})
	w.step()
	w.caddy.stop()
	waitClosed(t, w.caddy.sock)
	w.ask(tlsconf.Request{Generation: 2, Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	w.step()
	r := w.result()
	if r == nil || r.OK || r.Generation != 2 || r.Code != tlsconf.ResultUnreachable {
		t.Fatalf("result: %+v", r)
	}
	want := rendered(t, tlsconf.State{Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	if w.file("sites.caddy") != want.Sites || w.exists("certs", "key.pem") {
		t.Fatal("the new files are not in place, or the unused pair stayed")
	}
	w.step()
	w.step()
	if len(w.caddy.all()) != 1 {
		t.Fatal("loads while Caddy was down")
	}
	w.caddy.start()
	w.step()
	loads := w.caddy.all()
	if r := w.result(); r == nil || !r.OK || r.Generation != 2 || len(loads) != 2 || loads[1].sites != want.Sites {
		t.Fatalf("after Caddy came back: %+v %d", r, len(loads))
	}
	w.step()
	if len(w.caddy.all()) != 2 {
		t.Fatal("loaded again after it worked")
	}
}

// A load that takes longer than the agent waits counts as no answer.
func TestSlowCaddyIsUnreachable(t *testing.T) {
	w := newWorld(t)
	w.agent.admin = newAdmin(w.caddy.sock, 100*time.Millisecond)
	w.caddy.mu.Lock()
	w.caddy.delay = time.Second
	w.caddy.mu.Unlock()
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	w.step()
	if r := w.result(); r == nil || r.Code != tlsconf.ResultUnreachable {
		t.Fatalf("result: %+v", r)
	}
}

// The agent checks every request again: what core should never send is
// refused, nothing is written, Caddy is not asked, and the answer never
// quotes a key.
func TestInvalidRequests(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	cert, key := selfPair(t, []string{"panel.example.com"}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	oldCert, oldKey := selfPair(t, []string{"panel.example.com"}, now.Add(-100*24*time.Hour), now.Add(-time.Hour))
	_, otherKey := selfPair(t, []string{"panel.example.com"}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	shopCert, shopKey := selfPair(t, []string{"shop.example.com"}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	for _, tc := range []struct {
		name string
		req  tlsconf.Request
		want string
	}{
		{"an injected domain", tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "panel.example.com {\n\trespond hi\n}\nx.example.com"}, "neither a host name"},
		{"an unknown mode", tlsconf.Request{Mode: "proxy", Domain: "panel.example.com"}, "auto, ip or manual"},
		{"a private IP", tlsconf.Request{Mode: tlsconf.ModeIP, Domain: "panel.example.com", IP: "10.0.0.1"}, "not a public address"},
		{"an injected Zarinpal host", tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.example.net\nimport /etc/passwd"}, "Zarinpal domain"},
		{"manual without a pair", tlsconf.Request{Mode: tlsconf.ModeManual, Domain: "panel.example.com"}, "needs a certificate"},
		{"an expired pair", tlsconf.Request{Mode: tlsconf.ModeManual, Domain: "panel.example.com", CertPEM: oldCert, KeyPEM: oldKey}, "expired"},
		{"another name", tlsconf.Request{Mode: tlsconf.ModeManual, Domain: "panel.example.com", CertPEM: shopCert, KeyPEM: shopKey}, "does not cover panel.example.com"},
		{"another key", tlsconf.Request{Mode: tlsconf.ModeManual, Domain: "panel.example.com", CertPEM: cert, KeyPEM: otherKey}, "does not belong"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			tc.req.Generation = 9
			w.ask(tc.req)
			w.step()
			r := w.result()
			if r == nil || r.OK || r.Generation != 9 || r.Code != tlsconf.ResultInvalid || !strings.Contains(r.Error, tc.want) {
				t.Fatalf("result: %+v", r)
			}
			if w.exists("sites.caddy") || len(w.caddy.all()) != 0 {
				t.Fatal("something was written or loaded")
			}
			for _, secret := range []string{key, otherKey, oldKey, shopKey} {
				body := strings.Split(secret, "\n")[1]
				if strings.Contains(r.Error, body) || strings.Contains(w.logs.String(), body) {
					t.Fatal("a key reached the answer or the log")
				}
			}
		})
	}
}

// The files found on disk at start are the last good ones (the CLI's
// defaults before the first request): a refused first request puts them
// back. Temp files a crash left are removed.
func TestStartTakesTheFilesOnDisk(t *testing.T) {
	w := newWorld(t)
	def := rendered(t, tlsconf.State{Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	for name, content := range map[string]string{"global.caddy": def.Global, "sites.caddy": def.Sites, ".sites.caddy.tmp": "half"} {
		if err := os.WriteFile(filepath.Join(w.tlsDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.agent.Start()
	if w.exists(".sites.caddy.tmp") {
		t.Fatal("the temp file stayed")
	}
	w.caddy.setAnswer(refuse("no"))
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeIP, Domain: "panel.example.com", IP: "1.2.3.4"})
	w.step()
	if r := w.result(); r.Code != tlsconf.ResultRefused || w.file("sites.caddy") != def.Sites || w.file("global.caddy") != def.Global {
		t.Fatalf("the defaults are not back: %+v\n%s", r, w.file("sites.caddy"))
	}
}

// Without its main Caddyfile the agent changes nothing; an unreadable
// request is logged once and not answered (it has no generation).
func TestAgentProblems(t *testing.T) {
	w := newWorld(t)
	if err := os.Remove(filepath.Join(w.dir, "caddy", "Caddyfile")); err != nil {
		t.Fatal(err)
	}
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	w.step()
	if r := w.result(); r == nil || r.Code != tlsconf.ResultAgentError || w.exists("sites.caddy") || len(w.caddy.all()) != 0 {
		t.Fatalf("without a Caddyfile: %+v", r)
	}

	w = newWorld(t)
	if err := os.WriteFile(filepath.Join(w.dir, "request", tlsconf.RequestFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.step()
	w.step()
	if w.result() != nil || strings.Count(w.logs.String(), "cannot be read") != 1 {
		t.Fatalf("an unreadable request: %+v\n%s", w.result(), w.logs.String())
	}
}

// Files the agent cannot write: nothing loads, the answer says so. A
// directory standing where sites.caddy goes makes the atomic replace fail
// for any user, root included (a read-only directory would not stop root).
func TestWriteFailed(t *testing.T) {
	w := newWorld(t)
	if err := os.Mkdir(filepath.Join(w.tlsDir, "sites.caddy"), 0o750); err != nil {
		t.Fatal(err)
	}
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	w.step()
	r := w.result()
	if r == nil || r.OK || r.Generation != 1 || r.Code != tlsconf.ResultWriteFailed || len(w.caddy.all()) != 0 ||
		!strings.Contains(r.Error, "could not be written") || strings.Contains(r.Error, "put back") {
		t.Fatalf("result: %+v", r)
	}
	if w.exists(".sites.caddy.tmp") {
		t.Fatal("the temp file stayed")
	}
	w.step()
	w.step()
	if len(w.caddy.all()) != 0 || w.result().Generation != 1 {
		t.Fatal("a failed generation was tried again")
	}

	// With files Caddy loaded before, the agent tries to put them back (and
	// says when it cannot either).
	w = newWorld(t)
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	w.step()
	if err := os.Remove(filepath.Join(w.tlsDir, "sites.caddy")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(w.tlsDir, "sites.caddy"), 0o750); err != nil {
		t.Fatal(err)
	}
	w.ask(tlsconf.Request{Generation: 2, Mode: tlsconf.ModeIP, Domain: "panel.example.com", IP: "1.2.3.4"})
	w.step()
	if r := w.result(); r == nil || r.Code != tlsconf.ResultWriteFailed || r.Generation != 2 ||
		!strings.Contains(r.Error, "the previous files could not be put back either") || len(w.caddy.all()) != 1 {
		t.Fatalf("with files to put back: %+v, %d loads", r, len(w.caddy.all()))
	}
}

// A request for another domain or Zarinpal host than the install's own
// (BOBRES_DOMAIN, BOBRES_ZARINPAL_HOST in the agent's environment) is
// refused as invalid whatever else is right with it: a compromised core
// cannot have Caddy serve, or fetch certificates for, names the install
// does not own. Names are compared in canonical form.
func TestRequestsForOtherNamesAreRefused(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	evilCert, evilKey := selfPair(t, []string{"evil.example.org"}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	for _, tc := range []struct {
		name         string
		domain, zp   string // the agent's own
		req          tlsconf.Request
		want         string // "" = applied
		unpinned     bool
		noFilesWrote bool
	}{
		{"its own names", "panel.example.com", "pay.example.net",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.example.net"}, "", false, false},
		{"its own names in another case", "Panel.Example.COM", "pay.example.net",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: " PANEL.example.com ", ZarinpalHost: "PAY.example.net"}, "", false, false},
		{"another domain", "panel.example.com", "",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "evil.example.org"}, "not this install's domain", false, true},
		{"another domain in ip mode", "panel.example.com", "",
			tlsconf.Request{Mode: tlsconf.ModeIP, Domain: "evil.example.org", IP: "1.2.3.4"}, "not this install's domain", false, true},
		{"another domain with a pair that covers it", "panel.example.com", "",
			tlsconf.Request{Mode: tlsconf.ModeManual, Domain: "evil.example.org", CertPEM: evilCert, KeyPEM: evilKey}, "not this install's domain", false, true},
		{"an IP address for a host name", "panel.example.com", "",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "1.2.3.4"}, "not this install's domain", false, true},
		{"another Zarinpal host", "panel.example.com", "pay.example.net",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.evil.org"}, "not this install's (BOBRES_ZARINPAL_HOST)", false, true},
		{"a Zarinpal host the install has none of", "panel.example.com", "",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.evil.org"}, "not this install's (BOBRES_ZARINPAL_HOST)", false, true},
		{"the Zarinpal host left out", "panel.example.com", "pay.example.net",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "panel.example.com"}, "not this install's (BOBRES_ZARINPAL_HOST)", false, true},
		// core leaves a Zarinpal host that is not a host name out: so does the agent
		{"a bad own Zarinpal host, left out", "panel.example.com", "not a host name",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "panel.example.com"}, "", false, false},
		{"a private IP domain of a LAN install", "192.168.1.10", "",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "192.168.1.10"}, "", false, false},
		{"no domain of its own", "", "",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "panel.example.com"}, "no usable BOBRES_DOMAIN", false, true},
		{"an unusable domain of its own", "panel.example.com {", "",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "panel.example.com"}, "no usable BOBRES_DOMAIN", false, true},
		{"unpinned (tests only)", "", "",
			tlsconf.Request{Mode: tlsconf.ModeAuto, Domain: "evil.example.org"}, "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.agent.cfg.Domain, w.agent.cfg.ZarinpalHost, w.agent.cfg.Unpinned = tc.domain, tc.zp, tc.unpinned
			tc.req.Generation = 4
			w.ask(tc.req)
			w.step()
			r := w.result()
			if tc.want == "" {
				if r == nil || !r.OK || r.Generation != 4 || len(w.caddy.all()) != 1 {
					t.Fatalf("result: %+v", r)
				}
				return
			}
			if r == nil || r.OK || r.Generation != 4 || r.Code != tlsconf.ResultInvalid || !strings.Contains(r.Error, tc.want) {
				t.Fatalf("result: %+v, want invalid with %q", r, tc.want)
			}
			if w.exists("sites.caddy") || w.exists("global.caddy") || w.exists("certs", "key.pem") || len(w.caddy.all()) != 0 {
				t.Fatal("something was written or loaded for a name that is not the install's")
			}
			if strings.Contains(r.Error, strings.Split(evilKey, "\n")[1]) || strings.Contains(w.logs.String(), strings.Split(evilKey, "\n")[1]) {
				t.Fatal("a key reached the answer or the log")
			}
			// refused for good: the same generation is not tried again
			w.step()
			if len(w.caddy.all()) != 0 || w.exists("sites.caddy") {
				t.Fatal("a refused generation was tried again")
			}
		})
	}
}

// The heartbeat is touched after every load attempt: a load can take as
// long as LoadTimeout, and a refused one loads twice, so a beat only at the
// start of a round could be older than the health check allows when the
// round ends.
func TestHeartbeatAfterEveryLoad(t *testing.T) {
	w := newWorld(t)
	hb := filepath.Join(w.dir, "alive")
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	w.step()
	var (
		mu    sync.Mutex
		fresh []bool
	)
	old := time.Now().Add(-time.Hour)
	w.caddy.setAnswer(func(l load) (int, string) {
		mu.Lock()
		fresh = append(fresh, Healthcheck(hb, time.Now()) == 0)
		mu.Unlock()
		_ = os.Chtimes(hb, old, old) // as if this load had taken minutes
		if strings.Contains(l.sites, "shortlived") {
			return http.StatusBadRequest, `{"error":"no"}`
		}
		return http.StatusOK, ""
	})
	w.ask(tlsconf.Request{Generation: 2, Mode: tlsconf.ModeIP, Domain: "panel.example.com", IP: "1.2.3.4"})
	w.step()
	mu.Lock()
	defer mu.Unlock()
	if r := w.result(); r.Code != tlsconf.ResultRefused || len(fresh) != 2 || !fresh[0] || !fresh[1] {
		t.Fatalf("the heartbeat when each load began (the refused one, then the previous files again): %v, result %+v", fresh, r)
	}
	if Healthcheck(hb, time.Now()) != 0 {
		t.Fatal("the heartbeat is old when the round ends")
	}
}

// A heartbeat that cannot be written is said once, at warning level, and the agent goes on: the
// container will look unhealthy, and a person reading the log should see why. It was a debug line,
// and an agent whose /tmp was not writable after a reboot stayed "unhealthy" for 8 hours without a word.
func TestHeartbeatThatCannotBeWrittenIsSaidOnce(t *testing.T) {
	w := newWorld(t)
	w.agent.cfg.Heartbeat = filepath.Join(w.dir, "missing", "alive")
	w.step()
	w.step()
	w.step()
	if n := strings.Count(w.logs.String(), "the heartbeat cannot be written"); n != 1 || !strings.Contains(w.logs.String(), "level=WARN") {
		t.Fatalf("%d warnings:\n%s", n, w.logs)
	}
	// Writable again: no longer failing, and a later failure is said again.
	if err := os.MkdirAll(filepath.Dir(w.agent.cfg.Heartbeat), 0o750); err != nil {
		t.Fatal(err)
	}
	w.step()
	if Healthcheck(w.agent.cfg.Heartbeat, time.Now()) != 0 {
		t.Fatal("no heartbeat once it can be written")
	}
	if err := os.RemoveAll(filepath.Dir(w.agent.cfg.Heartbeat)); err != nil {
		t.Fatal(err)
	}
	w.step()
	if n := strings.Count(w.logs.String(), "the heartbeat cannot be written"); n != 2 {
		t.Fatalf("a second failure after a recovery was not said: %d\n%s", n, w.logs)
	}
}

// The default heartbeat is in /dev/shm, which every container has and every user may write: the
// image is built FROM scratch, has no /tmp, and after a restart its tmpfs is 0755 and root's.
func TestDefaultHeartbeatIsInDevShm(t *testing.T) {
	if !strings.HasPrefix(DefaultHeartbeat, "/dev/shm/") {
		t.Errorf("the heartbeat is %s: /tmp is not writable by the agent after a container restart", DefaultHeartbeat)
	}
}

// Caddy was down for generation 2 (its files are on disk), comes back and
// refuses them: the last good files go back, Caddy loads them again, and
// the answer for generation 2 turns from unreachable to refused.
func TestPendingThenRefused(t *testing.T) {
	w := newWorld(t)
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	w.step()
	good := rendered(t, tlsconf.State{Mode: tlsconf.ModeAuto, Domain: "panel.example.com"})
	w.caddy.stop()
	waitClosed(t, w.caddy.sock)
	w.ask(tlsconf.Request{Generation: 2, Mode: tlsconf.ModeIP, Domain: "panel.example.com", IP: "1.2.3.4"})
	w.step()
	if r := w.result(); r.Generation != 2 || r.Code != tlsconf.ResultUnreachable || !strings.Contains(w.file("sites.caddy"), "shortlived") {
		t.Fatalf("while Caddy was down: %+v\n%s", r, w.file("sites.caddy"))
	}
	w.caddy.start()
	w.caddy.setAnswer(func(l load) (int, string) {
		if strings.Contains(l.sites, "shortlived") {
			return http.StatusBadRequest, `{"error":"loading config: profile is not known"}`
		}
		return http.StatusOK, ""
	})
	w.step()
	r := w.result()
	if r.Generation != 2 || r.OK || r.Code != tlsconf.ResultRefused || !strings.Contains(r.Error, "profile is not known") {
		t.Fatalf("after Caddy came back and refused: %+v", r)
	}
	loads := w.caddy.all()
	if len(loads) != 3 || !strings.Contains(loads[1].sites, "shortlived") || loads[2].sites != good.Sites || w.file("sites.caddy") != good.Sites {
		t.Fatalf("%d loads: the refused files, then the good ones again; disk:\n%s", len(loads), w.file("sites.caddy"))
	}
	w.step()
	w.step()
	if len(w.caddy.all()) != 3 {
		t.Fatal("a refused generation was tried again")
	}
}

// A new request arrives while an older one still waits for Caddy: the old
// one is dropped (its files are replaced), the new one is answered, and
// when Caddy comes back it loads the new files only.
func TestNewRequestWhilePending(t *testing.T) {
	w := newWorld(t)
	w.agent.cfg.ZarinpalHost = "pay.example.net"
	w.ask(tlsconf.Request{Generation: 1, Mode: tlsconf.ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.example.net"})
	w.step()
	w.caddy.stop()
	waitClosed(t, w.caddy.sock)
	w.ask(tlsconf.Request{Generation: 2, Mode: tlsconf.ModeIP, Domain: "panel.example.com", IP: "1.2.3.4", ZarinpalHost: "pay.example.net"})
	w.step()
	if r := w.result(); r.Generation != 2 || r.Code != tlsconf.ResultUnreachable {
		t.Fatalf("generation 2: %+v", r)
	}
	w.ask(tlsconf.Request{Generation: 3, Mode: tlsconf.ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.example.net"})
	w.step()
	want := rendered(t, tlsconf.State{Mode: tlsconf.ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.example.net"})
	if r := w.result(); r.Generation != 3 || r.Code != tlsconf.ResultUnreachable || w.file("sites.caddy") != want.Sites {
		t.Fatalf("generation 3 while Caddy is down: %+v\n%s", w.result(), w.file("sites.caddy"))
	}
	w.step()
	w.step()
	if len(w.caddy.all()) != 1 {
		t.Fatal("loads while Caddy was down")
	}
	w.caddy.start()
	w.step()
	loads := w.caddy.all()
	if r := w.result(); r.Generation != 3 || !r.OK || len(loads) != 2 || loads[1].sites != want.Sites {
		t.Fatalf("after Caddy came back: %+v, %d loads", r, len(loads))
	}
	for _, l := range loads {
		if strings.Contains(l.sites, "shortlived") {
			t.Fatal("the dropped request was loaded")
		}
	}
	w.step()
	if len(w.caddy.all()) != 2 {
		t.Fatal("loaded again after it worked")
	}
}

func TestHealthcheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alive")
	now := time.Now()
	if Healthcheck(path, now) != 1 {
		t.Fatal("healthy without a heartbeat")
	}
	if err := os.WriteFile(path, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Healthcheck(path, now) != 0 {
		t.Fatal("a fresh heartbeat is unhealthy")
	}
	if Healthcheck(path, now.Add(HealthyAge+time.Second)) != 1 {
		t.Fatal("a stale heartbeat is healthy")
	}
}

// Caddy's words: its JSON error, or its text.
func TestCaddyMessage(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":"adapting config using caddyfile: bad"}`: "adapting config using caddyfile: bad",
		"plain words\x00\n": "plain words",
		"":                  "Bad Request",
	} {
		if got := caddyMessage([]byte(body), http.StatusBadRequest); got != want {
			t.Errorf("%q: %q", body, got)
		}
	}
}
