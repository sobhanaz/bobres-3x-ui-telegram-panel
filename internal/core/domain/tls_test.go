package domain

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsagent"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
)

const (
	tlsDomain   = "panel.example.com"
	tlsZarinpal = "pay.example.net"
	tlsIP       = "1.2.3.4" // a public address (the documentation ranges are refused outside tlsconf's tests)
)

// fakeAdmin is Caddy's admin socket: it answers every load as told.
type fakeAdmin struct {
	t    *testing.T
	sock string
	mu   sync.Mutex
	// status answers a load (200 when 0); msg is the body.
	status int
	msg    string
	// once holds answers given before the standing one, one per load: a refusal followed at once by the rollback's
	// load must not see the refusal twice.
	once  []fakeAnswer
	loads int
	srv   *http.Server
}

type fakeAnswer struct {
	status int
	msg    string
}

func newFakeAdmin(t *testing.T) *fakeAdmin {
	t.Helper()
	dir, err := os.MkdirTemp("", "ta")
	if err != nil {
		t.Fatal(err)
	}
	if len(dir) > 80 { // a unix socket path must stay short (macOS)
		_ = os.RemoveAll(dir)
		if dir, err = os.MkdirTemp("/tmp", "ta"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeAdmin{t: t, sock: filepath.Join(dir, "admin.sock")}
	f.start()
	t.Cleanup(f.stop)
	return f
}

func (f *fakeAdmin) start() {
	lis, err := net.Listen("unix", f.sock)
	if err != nil {
		f.t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		f.mu.Lock()
		f.loads++
		status, msg := f.status, f.msg
		if len(f.once) > 0 {
			status, msg = f.once[0].status, f.once[0].msg
			f.once = f.once[1:]
		}
		f.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, msg)
	})}
	f.srv = srv
	go func() { _ = srv.Serve(lis) }()
}

func (f *fakeAdmin) stop() {
	if f.srv != nil {
		_ = f.srv.Close()
		f.srv = nil
		_ = os.Remove(f.sock)
	}
}

func (f *fakeAdmin) answer(status int, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.msg = status, msg
}

// answerOnce answers the next load as told and the ones after it as answer set.
func (f *fakeAdmin) answerOnce(status int, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.once = append(f.once, fakeAnswer{status, msg})
}

// tlsWorld is the HTTPS manager on a fresh core schema, with the agent's
// volumes in a temp dir and, when started, a real tls-agent in front of a
// fake Caddy.
type tlsWorld struct {
	t      *testing.T
	svc    *Service
	st     *store.Store
	dir    string
	ex     tlsconf.Exchange
	admin  *fakeAdmin
	cfg    TLSConfig
	tls    *TLS
	probes map[string]tlsprobe.Result
	mu     sync.Mutex
	now    time.Time
	// the bot's detection of the server's address (cfg.DetectIP)
	ip      string
	ipErr   error
	ipCalls int
	probeN  int // live probes made
}

func testMasterKey(t *testing.T, key string) *bcrypto.Envelope {
	t.Helper()
	env, err := bcrypto.NewEnvelope([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func newTLSWorld(t *testing.T) *tlsWorld {
	t.Helper()
	svc, st := testService(t)
	if err := testdb.Truncate(context.Background(), st.DB(), `core.tls_config`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, d := range []string{"request", "result", "tls", "caddy"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "caddy", "Caddyfile"), []byte("{\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &tlsWorld{t: t, svc: svc, st: st, dir: dir, probes: map[string]tlsprobe.Result{},
		ex: tlsconf.NewExchange(filepath.Join(dir, "request"), filepath.Join(dir, "result")), now: time.Now()}
	w.ipErr = errors.New("the bot is not answering")
	w.cfg = TLSConfig{Domain: tlsDomain, ZarinpalHost: tlsZarinpal, Secrets: testMasterKey(t, "0123456789abcdef0123456789abcdef"),
		Exchange: w.ex, Probe: w.probe, DetectIP: w.detect, Wait: 5 * time.Second, Poll: 10 * time.Millisecond, Now: w.clock,
		Log: slog.New(slog.DiscardHandler)}
	w.tls = svc.NewTLS(w.cfg)
	return w
}

func (w *tlsWorld) clock() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now
}

func (w *tlsWorld) after(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
}

// detect is the bot's detection of the server's public address.
func (w *tlsWorld) detect(context.Context) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ipCalls++
	return w.ip, w.ipErr
}

// detected sets what the bot detects from now on (ip "" with err: it cannot).
func (w *tlsWorld) detected(ip string, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ip, w.ipErr = ip, err
}

func (w *tlsWorld) probeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.probeN
}

func (w *tlsWorld) detections() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ipCalls
}

func (w *tlsWorld) probe(_ context.Context, address string) tlsprobe.Result {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.probeN++
	if r, ok := w.probes[address]; ok {
		return r
	}
	return tlsprobe.Result{Error: "remote error: tls: internal error"}
}

func (w *tlsWorld) serve(address string, r tlsprobe.Result) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.probes[address] = r
}

// rebuild makes a new manager with cfg changed.
func (w *tlsWorld) rebuild(change func(*TLSConfig)) {
	change(&w.cfg)
	w.tls = w.svc.NewTLS(w.cfg)
}

// runAgent starts a tls-agent on the world's volumes in front of a fake
// Caddy, until the test ends.
func (w *tlsWorld) runAgent() *fakeAdmin {
	w.t.Helper()
	w.admin = newFakeAdmin(w.t)
	a := tlsagent.New(tlsagent.Config{RequestPath: w.ex.RequestPath, ResultPath: w.ex.ResultPath,
		TLSDir: filepath.Join(w.dir, "tls"), Caddyfile: filepath.Join(w.dir, "caddy", "Caddyfile"), AdminSocket: w.admin.sock,
		Every: 10 * time.Millisecond, LoadTimeout: time.Second,
		// the names the deployment gives both services (core's can be rebuilt with others)
		Domain: w.cfg.Domain, ZarinpalHost: w.cfg.ZarinpalHost})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Run(ctx); close(done) }()
	w.t.Cleanup(func() { cancel(); <-done })
	return w.admin
}

func (w *tlsWorld) request() *tlsconf.Request {
	w.t.Helper()
	r, err := w.ex.ReadRequest()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func (w *tlsWorld) row() *store.TLSRow {
	w.t.Helper()
	r, err := w.st.GetTLS(context.Background(), w.st.Conn())
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

func (w *tlsWorld) setRow(r *store.TLSRow) {
	w.t.Helper()
	if err := w.st.SetTLS(context.Background(), w.st.Conn(), r); err != nil {
		w.t.Fatal(err)
	}
}

// waitAnswer waits until the agent answered generation gen.
func (w *tlsWorld) waitAnswer(gen int64) *tlsconf.Result {
	w.t.Helper()
	for range 300 {
		if r, err := w.ex.ReadResult(); err == nil && r.Generation == gen {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.t.Fatalf("no answer for generation %d", gen)
	return nil
}

// leafPEM is a self-signed certificate for names, valid from from until
// until, with its key (PKCS#8).
func leafPEM(t *testing.T, names []string, from, until time.Time) (cert, key []byte) {
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
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})
}

// manualRow is a manual setting with a pair sealed by env.
func manualRow(t *testing.T, env *bcrypto.Envelope, cert, key []byte) *store.TLSRow {
	t.Helper()
	sealed, err := env.Encrypt(key)
	if err != nil {
		t.Fatal(err)
	}
	return &store.TLSRow{Mode: "manual", CertPEM: string(cert), KeySealed: sealed}
}

func tlsCode(err error) string {
	var te *TLSError
	if errors.As(err, &te) {
		return te.Code
	}
	return ""
}

// At start core asks for the saved setting, with the domain and Zarinpal
// host it has now.
func TestTLSStartAsksForTheSavedSetting(t *testing.T) {
	w := newTLSWorld(t)
	if !w.tls.Start(context.Background()) {
		t.Fatal("the lock was free")
	}
	r := w.request()
	if r == nil || r.Generation != 1 || r.Mode != tlsconf.ModeAuto || r.Domain != tlsDomain || r.ZarinpalHost != tlsZarinpal || r.KeyPEM != "" {
		t.Fatalf("request: %+v", r)
	}
	if fi, err := os.Stat(w.ex.RequestPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("request file: %v %v", fi.Mode(), err)
	}
	cert, key := leafPEM(t, []string{tlsDomain}, w.now.Add(-time.Hour), w.now.Add(90*24*time.Hour))
	w.setRow(manualRow(t, w.cfg.Secrets, cert, key))
	w.tls.Start(context.Background())
	r = w.request()
	if r.Generation != 2 || r.Mode != tlsconf.ModeManual || r.CertPEM != string(cert) || r.KeyPEM != string(key) {
		t.Fatalf("manual: %v", r)
	}
	w.setRow(&store.TLSRow{Mode: "ip", IP: tlsIP})
	w.tls.Start(context.Background())
	if r = w.request(); r.Generation != 3 || r.Mode != tlsconf.ModeIP || r.IP != tlsIP {
		t.Fatalf("ip: %v", r)
	}
}

// A saved setting that cannot be used any more is replaced by auto in the
// request; the row stays and the view says why.
func TestTLSStartFallsBackToAuto(t *testing.T) {
	now := time.Now()
	cert, key := leafPEM(t, []string{tlsDomain}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	other, otherKey := leafPEM(t, []string{"old.example.com"}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	old, oldKey := leafPEM(t, []string{tlsDomain}, now.Add(-100*24*time.Hour), now.Add(-time.Hour))
	for _, tc := range []struct {
		name   string
		row    func(w *tlsWorld) *store.TLSRow
		change func(*TLSConfig)
		code   string
	}{
		{"a key sealed with another master key", func(w *tlsWorld) *store.TLSRow {
			return manualRow(t, testMasterKey(t, "another-master-key-0123456789abcdef"), cert, key)
		}, nil, TLSProblemKeyUnreadable},
		{"no master key", func(w *tlsWorld) *store.TLSRow { return manualRow(t, w.cfg.Secrets, cert, key) },
			func(c *TLSConfig) { c.Secrets = nil }, TLSProblemNoMasterKey},
		{"the domain changed", func(w *tlsWorld) *store.TLSRow { return manualRow(t, w.cfg.Secrets, other, otherKey) },
			nil, tlsconf.CodeCertNameMismatch},
		{"expired", func(w *tlsWorld) *store.TLSRow { return manualRow(t, w.cfg.Secrets, old, oldKey) }, nil, tlsconf.CodeCertExpired},
		{"another IP than an IP domain", func(*tlsWorld) *store.TLSRow { return &store.TLSRow{Mode: "ip", IP: "1.2.3.5"} },
			func(c *TLSConfig) { c.Domain = tlsIP }, tlsconf.CodeIPDomainMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newTLSWorld(t)
			row := tc.row(w)
			w.setRow(row)
			if tc.change != nil {
				w.rebuild(tc.change)
			}
			w.tls.Start(context.Background())
			r := w.request()
			if r == nil || r.Mode != tlsconf.ModeAuto || r.KeyPEM != "" || r.CertPEM != "" || r.IP != "" {
				t.Fatalf("request: %v", r)
			}
			if got := w.row(); got.Mode != row.Mode {
				t.Fatalf("the row changed: %+v", got)
			}
			v, err := w.tls.View(context.Background(), false)
			if err != nil {
				t.Fatal(err)
			}
			if v.Problem == nil || v.Problem.Code != tc.code || v.EffectiveMode != tlsconf.ModeAuto || v.Mode != tlsconf.Mode(row.Mode) {
				t.Fatalf("view: %+v %+v", v, v.Problem)
			}
			rep, err := w.tls.Watch(context.Background())
			if err != nil || rep.Problem == "" {
				t.Fatalf("watch: %+v %v", rep, err)
			}
		})
	}
}

// Start never fails: a busy lock is tried again, no agent means nothing to
// write, a domain Caddy cannot serve is reported.
func TestTLSStartNeverFails(t *testing.T) {
	w := newTLSWorld(t)
	ctx := context.Background()
	tx, err := w.st.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.st.LockTLS(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if w.tls.Start(ctx) || w.request() != nil {
		t.Fatal("started while another process held the lock")
	}
	_ = tx.Rollback(ctx)
	if !w.tls.Start(ctx) || w.request() == nil {
		t.Fatal("not started once the lock was free")
	}

	w.rebuild(func(c *TLSConfig) { c.Exchange = nil })
	if !w.tls.Start(ctx) {
		t.Fatal("no agent is nothing to wait for")
	}

	w = newTLSWorld(t)
	w.rebuild(func(c *TLSConfig) { c.Domain = "1.2.3" })
	if !w.tls.Start(ctx) || w.request() != nil {
		t.Fatal("a request for a domain Caddy cannot serve")
	}
	v, err := w.tls.View(ctx, false)
	if err != nil || v.Problem == nil || v.Problem.Code != tlsconf.CodeBadDomain || v.PublicURL != "https://1.2.3" {
		t.Fatalf("view: %+v %v", v, err)
	}
}

// A change is committed, with its audit entry, only once the agent says
// Caddy loaded it; the key is sealed in the row and never in the audit.
func TestTLSSetCommitsWhatCaddyTook(t *testing.T) {
	w := newTLSWorld(t)
	w.runAgent()
	ctx := context.Background()
	owner := seedUser(t, w.st, ownerTG)

	if _, err := w.tls.Set(ctx, owner, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, DetectedIP: tlsIP}); err != nil {
		t.Fatal(err)
	}
	if r := w.row(); r.Mode != "ip" || r.IP != tlsIP {
		t.Fatalf("row: %+v", r)
	}
	gen := w.request().Generation
	if res := w.waitAnswer(gen); !res.OK {
		t.Fatalf("answer: %+v", res)
	}
	sites, _ := os.ReadFile(filepath.Join(w.dir, "tls", "sites.caddy"))
	if !strings.Contains(string(sites), tlsIP+" {") || !strings.Contains(string(sites), "profile shortlived") {
		t.Fatalf("the agent's files:\n%s", sites)
	}

	cert, key := leafPEM(t, []string{tlsDomain}, w.now.Add(-time.Hour), w.now.Add(90*24*time.Hour))
	res, err := w.tls.Set(ctx, owner, TLSChange{Mode: tlsconf.ModeManual, CertPEM: cert, KeyPEM: key})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending || len(res.Warnings) == 0 || res.Warnings[0].Code != tlsconf.WarnSelfSigned {
		t.Fatalf("result: %+v", res)
	}
	r := w.row()
	plain, err := w.cfg.Secrets.Decrypt(r.KeySealed)
	if r.Mode != "manual" || r.IP != "" || r.CertPEM != string(cert) || err != nil || string(plain) != string(key) ||
		strings.Contains(string(r.KeySealed), "PRIVATE KEY") {
		t.Fatalf("manual row: %+v %v", r, err)
	}
	// What was committed is what reconcile expects: it asks for nothing again.
	gen = w.request().Generation
	w.tls.reconcile(ctx)
	if got := w.request().Generation; got != gen {
		t.Fatalf("a committed manual setting was asked for again: generation %d -> %d", gen, got)
	}

	if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeAuto}); err != nil {
		t.Fatal(err)
	}
	if r := w.row(); r.Mode != "auto" || r.CertPEM != "" || r.KeySealed != nil {
		t.Fatalf("leaving manual mode keeps the pair: %+v", r)
	}

	var audits []string
	rows, err := w.st.DB().Query(ctx, `SELECT coalesce(actor_id::text, ''), before::text, after::text, source FROM core.audit_log
		WHERE action = 'system.tls.set' ORDER BY created_at, id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var actor, before, after, source string
		if err := rows.Scan(&actor, &before, &after, &source); err != nil {
			t.Fatal(err)
		}
		audits = append(audits, actor+" "+before+" -> "+after)
	}
	rows.Close()
	keyBody := strings.Split(string(key), "\n")[1]
	if len(audits) != 3 || !strings.HasPrefix(audits[0], owner.ID+` {"mode": "auto"} -> {"ip": "1.2.3.4", "mode": "ip"}`) ||
		!strings.Contains(audits[1], `"subject": "CN=panel.example.com"`) || !strings.HasPrefix(audits[2], ` {"cert": `) ||
		strings.Contains(strings.Join(audits, "\n"), keyBody) {
		t.Fatalf("audit:\n%s", strings.Join(audits, "\n"))
	}
}

// An IP address that differs from the detected one, or none detected,
// needs a confirmation; nothing is asked of the agent before.
func TestTLSSetConfirmsAnotherIP(t *testing.T) {
	w := newTLSWorld(t)
	w.runAgent()
	ctx := context.Background()
	for _, detected := range []string{"5.6.7.8", "", "not an ip"} {
		_, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, DetectedIP: detected})
		var te *TLSError
		if !errors.As(err, &te) || te.Code != TLSErrIPUnconfirmed || te.Field != "ip" {
			t.Fatalf("detected %q: %v", detected, err)
		}
		if want, _ := tlsconf.PublicIP(detected); te.DetectedIP != want {
			t.Fatalf("detected %q answered as %q", detected, te.DetectedIP)
		}
	}
	if w.request() != nil {
		t.Fatal("the agent was asked before the confirmation")
	}
	if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, DetectedIP: "5.6.7.8", ConfirmIP: true}); err != nil {
		t.Fatal(err)
	}
	if r := w.row(); r.Mode != "ip" || r.IP != tlsIP {
		t.Fatalf("row: %+v", r)
	}
}

// Caddy refuses: the row and the audit stay as they were, the committed
// setting is asked for again as a newer request, and Caddy's words come
// back.
func TestTLSSetRefusedByCaddy(t *testing.T) {
	w := newTLSWorld(t)
	admin := w.runAgent()
	ctx := context.Background()
	admin.answerOnce(http.StatusBadRequest, `{"error":"loading new config: tls: unknown issuer"}`)
	_, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true})
	var te *TLSError
	if !errors.As(err, &te) || te.Code != TLSErrRefused || !strings.Contains(te.Message, "tls: unknown issuer") {
		t.Fatalf("refused: %v", err)
	}
	if r := w.row(); r.Mode != "auto" {
		t.Fatalf("row: %+v", r)
	}
	r := w.request()
	if r.Generation != 2 || r.Mode != tlsconf.ModeAuto {
		t.Fatalf("the committed setting is not asked for again: %v", r)
	}
	var n int
	if err := w.st.DB().QueryRow(ctx, `SELECT count(*) FROM core.audit_log WHERE action = 'system.tls.set'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("audited: %d %v", n, err)
	}
	if res := w.waitAnswer(2); !res.OK {
		t.Fatalf("the rollback: %+v", res)
	}
}

// No agent answering, or Caddy not running: ip and manual are refused (the
// committed setting is asked for again); auto is committed anyway, Caddy
// reads its files when it starts.
func TestTLSSetWithoutAnswers(t *testing.T) {
	w := newTLSWorld(t)
	w.rebuild(func(c *TLSConfig) { c.Wait = 100 * time.Millisecond })
	ctx := context.Background()
	if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true}); tlsCode(err) != TLSErrAgentTimeout {
		t.Fatalf("no agent: %v", err)
	}
	if r := w.request(); r == nil || r.Generation != 2 || r.Mode != tlsconf.ModeAuto || w.row().Mode != "auto" {
		t.Fatalf("after the timeout: %v %+v", r, w.row())
	}

	w = newTLSWorld(t)
	admin := w.runAgent()
	admin.stop()
	if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true}); tlsCode(err) != TLSErrUnreachable {
		t.Fatalf("Caddy down: %v", err)
	}
	if w.row().Mode != "auto" {
		t.Fatal("committed while Caddy was down")
	}
	w.setRow(&store.TLSRow{Mode: "ip", IP: tlsIP})
	res, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeAuto})
	if err != nil || !res.Pending || w.row().Mode != "auto" {
		t.Fatalf("auto while Caddy is down: %+v %v %+v", res, err, w.row())
	}

	w.rebuild(func(c *TLSConfig) { c.Exchange = nil })
	if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeAuto}); tlsCode(err) != TLSErrNoAgent {
		t.Fatalf("no agent volumes: %v", err)
	}
}

// What is wrong with a change is refused before anything is asked, with
// the field at fault.
func TestTLSSetRefusesBadChanges(t *testing.T) {
	w := newTLSWorld(t)
	ctx := context.Background()
	now := time.Now()
	cert, _ := leafPEM(t, []string{tlsDomain}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	_, otherKey := leafPEM(t, []string{tlsDomain}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	shop, shopKey := leafPEM(t, []string{"shop.example.com"}, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	for _, tc := range []struct {
		name  string
		c     TLSChange
		code  string
		field string
	}{
		{"an unknown mode", TLSChange{Mode: "proxy"}, tlsconf.CodeBadMode, "mode"},
		{"ip mode without an IP", TLSChange{Mode: tlsconf.ModeIP}, tlsconf.CodeIPRequired, "ip"},
		{"a private IP", TLSChange{Mode: tlsconf.ModeIP, IP: "192.168.1.2", ConfirmIP: true}, tlsconf.CodeIPNotPublic, "ip"},
		{"not an IP", TLSChange{Mode: tlsconf.ModeIP, IP: "panel.example.com", ConfirmIP: true}, tlsconf.CodeBadIP, "ip"},
		{"no certificate", TLSChange{Mode: tlsconf.ModeManual, KeyPEM: otherKey}, tlsconf.CodeCertMissing, "cert"},
		{"the key of another certificate", TLSChange{Mode: tlsconf.ModeManual, CertPEM: cert, KeyPEM: otherKey}, tlsconf.CodeKeyMismatch, "key"},
		{"another name", TLSChange{Mode: tlsconf.ModeManual, CertPEM: shop, KeyPEM: shopKey}, tlsconf.CodeCertNameMismatch, "cert"},
	} {
		_, err := w.tls.Set(ctx, nil, tc.c)
		var te *TLSError
		if !errors.As(err, &te) || te.Code != tc.code || te.Field != tc.field {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	w.rebuild(func(c *TLSConfig) { c.Secrets = nil })
	if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeManual, CertPEM: shop, KeyPEM: shopKey}); tlsCode(err) != TLSErrNoMasterKey {
		t.Fatalf("manual without a master key: %v", err)
	}
	if w.request() != nil {
		t.Fatal("the agent was asked")
	}
}

// Two changes at once are applied one after the other.
func TestTLSSetsDoNotInterleave(t *testing.T) {
	w := newTLSWorld(t)
	w.runAgent()
	ctx := context.Background()
	other := w.svc.NewTLS(w.cfg) // as `/app ssl` in its own process
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, m := range []*TLS{w.tls, other} {
		wg.Go(func() {
			_, errs[i] = m.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true})
		})
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("%v %v", errs[0], errs[1])
	}
	if r := w.request(); r.Generation != 2 || r.Mode != tlsconf.ModeIP {
		t.Fatalf("request: %v", r)
	}
}

func trusted(ok bool) *bool { return &ok }

// servedFor is what Caddy serves for a host with a certificate covering it
// that ends at until (and began 160 hours before, or 90 days for one that
// ends later than that from now).
func servedFor(host string, ok bool, until time.Time) tlsprobe.Result {
	return servedAs(host, ok, true, until)
}

// servedAs is servedFor with the certificate's coverage of the host as told:
// nameOK false is a certificate for another name.
func servedAs(host string, ok, nameOK bool, until time.Time) tlsprobe.Result {
	from := until.Add(-160 * time.Hour)
	if time.Until(until) > 160*time.Hour {
		from = until.Add(-90 * 24 * time.Hour)
	}
	info := tlsconf.CertInfo{Subject: "", Issuer: "CN=R11", NotBefore: from, NotAfter: until}
	if tlsconf.IsIP(host) {
		info.IPs = []string{host}
	} else {
		info.DNSNames = []string{host}
	}
	r := tlsprobe.Result{Reachable: true, Served: true, Info: info, NameOK: nameOK, Trusted: trusted(ok)}
	if !ok {
		r.Error = "certificate signed by unknown authority"
	}
	return r
}

// Links move to the IP address only while this Caddy serves a publicly
// trusted certificate for it AND the bot, asked within the last 5 minutes,
// says that is this server's address (a trusted certificate alone outlives
// the address: whoever holds it next could read a login token from a link
// that still pointed there); BOBRES_PUBLIC_URL wins; no domain, no links.
func TestTLSPublicURL(t *testing.T) {
	w := newTLSWorld(t)
	ctx := context.Background()
	domainURL, ipURL := "https://"+tlsDomain, "https://"+tlsIP
	if got := w.tls.PublicURL(ctx); got != domainURL {
		t.Fatalf("auto: %q", got)
	}
	if w.detections() != 0 {
		t.Fatal("the bot was asked in auto mode")
	}
	w.setRow(&store.TLSRow{Mode: "ip", IP: tlsIP})
	w.detected(tlsIP, nil)
	if got := w.tls.PublicURL(ctx); got != domainURL {
		t.Fatalf("ip mode before its certificate: %q", got)
	}
	if w.detections() != 0 {
		t.Fatal("the bot was asked before the certificate checked out")
	}
	w.serve(tlsIP, servedFor(tlsIP, false, w.now.Add(100*time.Hour)))
	w.after(4 * time.Minute) // the last status is old: it dials again
	if got := w.tls.PublicURL(ctx); got != domainURL {
		t.Fatalf("an untrusted certificate: %q", got)
	}
	w.serve(tlsIP, servedFor(tlsIP, true, w.now.Add(100*time.Hour)))
	if got := w.tls.PublicURL(ctx); got != domainURL {
		t.Fatalf("a fresh status is reused: %q", got)
	}
	w.after(4 * time.Minute)
	if got := w.tls.PublicURL(ctx); got != ipURL {
		t.Fatalf("a trusted certificate and the bot agrees: %q", got)
	}
	v, err := w.tls.View(ctx, true)
	if err != nil || v.PublicURL != ipURL {
		t.Fatalf("view: %+v %v", v, err)
	}

	// The bot's answer is kept for 5 minutes, not asked for at every link.
	n := w.detections()
	w.after(time.Minute)
	for range 3 {
		if got := w.tls.PublicURL(ctx); got != ipURL {
			t.Fatalf("within the 5 minutes: %q", got)
		}
	}
	if w.detections() > n+1 {
		t.Fatalf("the bot was asked %d times for 3 links", w.detections()-n)
	}

	// The address changed hands: the certificate is still trusted (Caddy
	// serves it for days), but the bot now sees another address.
	w.detected("5.6.7.8", nil)
	w.after(6 * time.Minute)
	if got := w.tls.PublicURL(ctx); got != domainURL {
		t.Fatalf("a trusted certificate for %s while the server is at 5.6.7.8: %q", tlsIP, got)
	}
	if v, err = w.tls.View(ctx, true); err != nil || v.PublicURL != domainURL || !v.Addresses[1].Valid {
		t.Fatalf("view: %+v %v", v, err)
	}
	// and back
	w.detected(tlsIP, nil)
	w.after(6 * time.Minute)
	if got := w.tls.PublicURL(ctx); got != ipURL {
		t.Fatalf("back at %s: %q", tlsIP, got)
	}

	// A bot that cannot answer proves nothing: the detection already kept
	// counts until it is 5 minutes old, then the links go back to the domain.
	w.detected("", errors.New("the bot is down"))
	w.after(4 * time.Minute) // the status is old, the detection is 4 minutes old
	if got := w.tls.PublicURL(ctx); got != ipURL {
		t.Fatalf("a detection 4 minutes old: %q", got)
	}
	w.after(2 * time.Minute) // 6 minutes
	if got := w.tls.PublicURL(ctx); got != domainURL {
		t.Fatalf("a detection 6 minutes old and the bot down: %q", got)
	}
	w.detected("10.0.0.1", nil) // a private address is no detection either
	w.after(6 * time.Minute)
	if got := w.tls.PublicURL(ctx); got != domainURL {
		t.Fatalf("a private address detected: %q", got)
	}
	w.detected(tlsIP, nil)
	w.after(time.Minute)

	// The certificate must cover the address too.
	w.serve(tlsIP, servedAs(tlsIP, true, false, w.now.Add(100*time.Hour)))
	w.after(4 * time.Minute)
	if got := w.tls.PublicURL(ctx); got != domainURL {
		t.Fatalf("a trusted chain that does not cover the address: %q", got)
	}
	w.serve(tlsIP, servedFor(tlsIP, true, w.now.Add(100*time.Hour)))
	w.after(4 * time.Minute)
	if got := w.tls.PublicURL(ctx); got != ipURL {
		t.Fatalf("a covering certificate again: %q", got)
	}

	// No bot, no proof.
	w.rebuild(func(c *TLSConfig) { c.DetectIP = nil })
	if got := w.tls.PublicURL(ctx); got != domainURL {
		t.Fatalf("without a bot: %q", got)
	}
	w.rebuild(func(c *TLSConfig) { c.PublicURLEnv = "https://bobres.localhost:8443" })
	if got := w.tls.PublicURL(ctx); got != "https://bobres.localhost:8443" {
		t.Fatalf("BOBRES_PUBLIC_URL: %q", got)
	}
	w.rebuild(func(c *TLSConfig) { c.PublicURLEnv, c.Domain = "", "" })
	if got := w.tls.PublicURL(ctx); got != "" {
		t.Fatalf("no domain: %q", got)
	}
}

// Watch asks the bot for the server's address every minute while in ip mode
// (so a link is never made on an old answer), and never in the other modes;
// the dashboard's answer to a change (View without a probe) dials and asks
// nothing.
func TestTLSWatchRefreshesTheDetection(t *testing.T) {
	w := newTLSWorld(t)
	ctx := context.Background()
	w.detected(tlsIP, nil)
	if _, err := w.tls.Watch(ctx); err != nil || w.detections() != 0 {
		t.Fatalf("auto mode: %d detections, %v", w.detections(), err)
	}
	w.setRow(&store.TLSRow{Mode: "ip", IP: tlsIP})
	w.serve(tlsIP, servedFor(tlsIP, true, w.now.Add(100*time.Hour)))
	for i := range 3 {
		if _, err := w.tls.Watch(ctx); err != nil {
			t.Fatal(err)
		}
		if w.detections() != i+1 {
			t.Fatalf("watch %d: %d detections", i+1, w.detections())
		}
		w.after(time.Minute)
	}
	// Each minute's answer keeps the links on the IP address (no extra ask)
	n := w.detections()
	if got := w.tls.PublicURL(ctx); got != "https://"+tlsIP || w.detections() != n {
		t.Fatalf("link after the watch: %q, %d asks", got, w.detections()-n)
	}
	// ...and a changed address moves them back within the minute.
	w.detected("5.6.7.8", nil)
	if _, err := w.tls.Watch(ctx); err != nil {
		t.Fatal(err)
	}
	w.after(time.Second)
	if got := w.tls.PublicURL(ctx); got != "https://"+tlsDomain {
		t.Fatalf("link after the address changed: %q", got)
	}

	// View(ctx, false) neither dials nor asks, whatever is stale.
	w.after(time.Hour)
	probes, asks := w.probeCount(), w.detections()
	v, err := w.tls.View(ctx, false)
	if err != nil || w.probeCount() != probes || w.detections() != asks || v.PublicURL != "https://"+tlsDomain || len(v.Addresses) != 0 {
		t.Fatalf("view without a probe: %+v %v, %d probes, %d asks", v, err, w.probeCount()-probes, w.detections()-asks)
	}
}

// The view: the saved setting (never the key), what is asked, the agent's
// answer and every served address, as JSON for the dashboard and the CLI.
func TestTLSView(t *testing.T) {
	w := newTLSWorld(t)
	w.runAgent()
	ctx := context.Background()
	cert, key := leafPEM(t, []string{tlsDomain}, w.now.Add(-time.Hour), w.now.Add(90*24*time.Hour))
	if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeManual, CertPEM: cert, KeyPEM: key}); err != nil {
		t.Fatal(err)
	}
	w.waitAnswer(w.request().Generation)
	w.serve(tlsDomain, servedFor(tlsDomain, false, w.now.Add(90*24*time.Hour)))
	w.serve(tlsZarinpal, servedFor(tlsZarinpal, true, w.now.Add(10*24*time.Hour)))
	w.tls.NoteDetectedIP(tlsIP)
	v, err := w.tls.View(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(v.JSON())
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		State struct {
			Mode          string         `json:"mode"`
			Cert          map[string]any `json:"cert"`
			Domain        string         `json:"domain"`
			ZarinpalHost  string         `json:"zarinpal_host"`
			ManualAllowed bool           `json:"manual_allowed"`
			UpdatedAt     *int64         `json:"updated_at"`
		} `json:"state"`
		Effective map[string]any `json:"effective"`
		Problem   map[string]any `json:"problem"`
		Status    struct {
			CheckedAt *int64           `json:"checked_at"`
			Addresses []map[string]any `json:"addresses"`
			Agent     map[string]any   `json:"agent"`
		} `json:"status"`
		PublicURL  string `json:"public_url"`
		DetectedIP string `json:"detected_ip"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.State.Mode != "manual" || got.State.Cert["subject"] != "CN=panel.example.com" || got.State.Domain != tlsDomain ||
		got.State.ZarinpalHost != tlsZarinpal || !got.State.ManualAllowed || got.State.UpdatedAt == nil ||
		got.Effective["mode"] != "manual" || got.Problem != nil || got.Status.CheckedAt == nil || got.PublicURL != "https://"+tlsDomain ||
		got.DetectedIP != tlsIP {
		t.Fatalf("view:\n%s", raw)
	}
	if a := got.Status.Agent; a["answered"] != true || a["ok"] != true || a["generation"] != float64(1) {
		t.Fatalf("agent: %v", a)
	}
	addrs := got.Status.Addresses
	if len(addrs) != 2 || addrs[0]["address"] != tlsDomain || addrs[0]["expect"] != "manual" || addrs[0]["valid"] != true ||
		addrs[0]["trusted"] != false || addrs[0]["error"] != "" || addrs[1]["address"] != tlsZarinpal || addrs[1]["valid"] != true ||
		addrs[1]["expiring"] != true || addrs[1]["days_left"] != float64(10) {
		t.Fatalf("addresses: %v", addrs)
	}
	if strings.Contains(string(raw), "PRIVATE") || strings.Contains(string(raw), strings.Split(string(key), "\n")[1]) {
		t.Fatal("the key is in the view")
	}
}

// The watchdog's report: what every address serves, why the saved setting
// is not working as saved, and whether the agent has yet to answer.
func TestTLSWatch(t *testing.T) {
	w := newTLSWorld(t)
	ctx := context.Background()
	w.tls.Start(ctx)
	w.serve(tlsZarinpal, servedFor(tlsZarinpal, true, w.now.Add(60*24*time.Hour)))
	rep, err := w.tls.Watch(ctx)
	if err != nil || len(rep.Addresses) != 2 || rep.Addresses[0].Valid || !rep.Addresses[1].Valid || rep.Problem != "" || !rep.AgentWaiting {
		t.Fatalf("first: %+v %v", rep, err)
	}
	// The agent answers the request, refusing it.
	if err := tlsconf.WriteResult(w.ex.ResultPath, tlsconf.Result{Generation: 1, Code: tlsconf.ResultRefused, Error: "Caddy refused the change: x"}); err != nil {
		t.Fatal(err)
	}
	if rep, _ = w.tls.Watch(ctx); !strings.Contains(rep.Problem, "Caddy refused the change: x") || rep.AgentWaiting {
		t.Fatalf("a refused setting: %+v", rep)
	}
	if err := tlsconf.WriteResult(w.ex.ResultPath, tlsconf.Result{Generation: 1, Code: tlsconf.ResultUnreachable}); err != nil {
		t.Fatal(err)
	}
	if rep, _ = w.tls.Watch(ctx); rep.Problem != "" || rep.AgentWaiting {
		t.Fatalf("Caddy restarting is no problem of the setting: %+v", rep)
	}
	w.rebuild(func(c *TLSConfig) { c.Domain = "" })
	if rep, err = w.tls.Watch(ctx); err != nil || len(rep.Addresses) != 0 {
		t.Fatalf("no domain: %+v %v", rep, err)
	}
}

// fakeTLSWatch is the HTTPS report as set.
type fakeTLSWatch struct {
	rep *TLSWatchReport
	err error
}

func (f *fakeTLSWatch) Watch(context.Context) (*TLSWatchReport, error) { return f.rep, f.err }

// The watchdog announces an address that has served nothing valid for 30
// minutes (held pending until then, in the store, so a restart of core
// does not start the 30 minutes over), a certificate close to its end, a
// setting not working as saved, and an agent that has not answered for 5
// minutes; an address no longer served is forgotten.
func TestWatchdogTLS(t *testing.T) {
	src := &fakeTLSWatch{rep: &TLSWatchReport{}}
	a := newAlertWorld(t, WatchdogSources{TLS: src})
	until := a.now.Add(30 * time.Hour)
	src.rep = &TLSWatchReport{Addresses: []TLSAddress{
		{Host: tlsIP, Role: "ip", Reachable: true, Error: "remote error: tls: internal error"},
		{Host: tlsDomain, Role: "domain", Reachable: true, Served: true, Valid: true, Expiring: true,
			Cert: &tlsconf.CertInfo{NotBefore: until.Add(-160 * time.Hour), NotAfter: until}},
	}, AgentWaiting: true}
	p := probeOf(a, a.w.tls)
	if c := checkOf(t, p, "tls.ip.failing"); !c.problem || c.hold != 30*time.Minute || c.title != "alert.tls_failing" ||
		c.args["address"] != tlsIP || c.severity != "critical" || c.detail != "remote error: tls: internal error" {
		t.Fatalf("failing: %+v", c)
	}
	if c := checkOf(t, p, "tls.domain.expiring"); !c.problem || c.args["days"] != "2" || c.args["date"] != strconv.FormatInt(until.Unix(), 10) ||
		c.args["address"] != tlsDomain || c.severity != "warning" || c.hold != 0 {
		t.Fatalf("expiring: %+v", c)
	}
	if c := checkOf(t, p, "tls.domain.failing"); c.problem {
		t.Fatalf("a valid address fails: %+v", c)
	}
	if c := checkOf(t, p, "tls.setting"); c.problem {
		t.Fatalf("setting: %+v", c)
	}
	if c := checkOf(t, p, "tls.agent"); !c.problem || c.hold != 5*time.Minute || c.title != "alert.tls_problem" {
		t.Fatalf("agent: %+v", c)
	}

	// The expiring certificate is announced at the second sighting; the
	// waiting agent after 5 minutes; the failing address after 30, even
	// with a new watchdog (a restart of core) on the way.
	a.step()
	a.after(time.Minute)
	a.step()
	if got := a.expectSent(1)[0]; got.Key != "tls.domain.expiring" || got.TitleKey != "alert.tls_expiring" {
		t.Fatalf("sent: %+v", got)
	}
	a.after(4 * time.Minute)
	a.step()
	if got := a.expectSent(2)[1]; got.Key != "tls.agent" || got.TitleKey != "alert.tls_problem" {
		t.Fatalf("sent: %+v", got)
	}
	a.w = a.svc.NewWatchdog(WatchdogSources{TLS: src}, nil)
	a.w.now = func() time.Time { return a.now }
	a.after(24 * time.Minute) // 29 minutes failing
	a.step()
	a.expectSent(2)
	if st := a.state("tls.ip.failing"); st == nil || st.State != store.AlertPending || !st.Since.Equal(alertStart) {
		t.Fatalf("held: %+v", st)
	}
	a.after(time.Minute)
	a.step()
	if got := a.expectSent(3)[2]; got.Key != "tls.ip.failing" || got.TitleKey != "alert.tls_failing" || got.Since != alertStart.Unix() {
		t.Fatalf("sent: %+v", got)
	}

	// A setting not in use is announced at the second sighting.
	src.rep.Problem = "your certificate is not in use: expired"
	a.after(time.Minute)
	a.step()
	a.after(time.Minute)
	a.step()
	if got := a.expectSent(4)[3]; got.Key != "tls.setting" || got.Detail != "your certificate is not in use: expired" {
		t.Fatalf("sent: %+v", got)
	}

	// An address that served nothing for a few minutes only is never
	// announced.
	src.rep = &TLSWatchReport{Addresses: []TLSAddress{{Host: tlsDomain, Role: "domain", Reachable: true, Served: true, Valid: true}}}
	a.after(time.Minute)
	a.step()
	if a.state("tls.ip.failing") != nil {
		t.Fatal("an address no longer served is remembered")
	}
	if st := a.state("tls.domain.expiring"); st == nil || st.State != store.AlertProblem || st.PassingSince == nil {
		t.Fatalf("a renewed certificate: %+v", st)
	}
	src.rep.Addresses[0].Valid = false
	a.after(time.Minute)
	a.step()
	a.after(10 * time.Minute)
	a.step()
	src.rep.Addresses[0].Valid = true
	a.after(time.Minute)
	a.step()
	if st := a.state("tls.domain.failing"); st == nil || st.State != store.AlertOK {
		t.Fatalf("a short outage: %+v", st)
	}
	for _, e := range a.sent() {
		if e.Key == "tls.domain.failing" {
			t.Fatalf("a short outage was announced: %+v", e)
		}
	}

	src.err, src.rep = errors.New("db down"), nil
	if p := probeOf(a, a.w.tls); p.covered {
		t.Fatal("an unread report covers its checks")
	}
}

// Caddy not answering at all (stopped, crash-looping) is announced within
// minutes under its own check, not after the 30 minutes an address gets to
// have its certificate issued; while it is down the per-address checks are
// left as they were, and a reachable Caddy that serves no certificate still
// has its 30 minutes.
func TestWatchdogTLSCaddyDown(t *testing.T) {
	refused := "dial tcp 172.18.0.4:443: connect: connection refused"
	down := &TLSWatchReport{Addresses: []TLSAddress{
		{Host: tlsDomain, Role: "domain", Error: refused},
		{Host: tlsIP, Role: "ip", Error: refused},
	}}
	src := &fakeTLSWatch{rep: down}
	a := newAlertWorld(t, WatchdogSources{TLS: src})

	p := probeOf(a, a.w.tls)
	if c := checkOf(t, p, "tls.caddy"); !c.problem || c.hold != 3*time.Minute || c.severity != "critical" || c.title != "alert.tls_caddy_down" ||
		c.detail != refused || len(c.args) != 0 {
		t.Fatalf("down: %+v", c)
	}
	for _, c := range p.checks {
		if strings.HasSuffix(c.key, ".failing") || strings.HasSuffix(c.key, ".expiring") {
			t.Fatalf("a certificate check while nothing can be reached: %+v", c)
		}
	}
	if p.covered {
		t.Fatal("the per-address checks are forgotten while Caddy is down")
	}

	// Announced at 3 minutes, not before; the addresses are not announced.
	a.step()
	a.after(time.Minute)
	a.step()
	a.after(time.Minute)
	a.step()
	a.expectSent(0)
	a.after(time.Minute)
	a.step()
	if got := a.expectSent(1)[0]; got.Key != "tls.caddy" || got.TitleKey != "alert.tls_caddy_down" || got.Severity != "critical" {
		t.Fatalf("sent: %+v", got)
	}
	a.after(40 * time.Minute)
	a.step()
	a.expectSent(1) // nothing about the addresses, even after 30 minutes

	// Caddy answers again, serving no certificate yet: that is the 30
	// minutes' business, and the Caddy alert is fixed after 5 quiet minutes.
	src.rep = &TLSWatchReport{Addresses: []TLSAddress{
		{Host: tlsDomain, Role: "domain", Reachable: true, Error: "remote error: tls: internal error"},
		{Host: tlsIP, Role: "ip", Reachable: true, Error: "remote error: tls: internal error"},
	}}
	p = probeOf(a, a.w.tls)
	if c := checkOf(t, p, "tls.caddy"); c.problem || !p.covered {
		t.Fatalf("up: %+v covered %v", c, p.covered)
	}
	if c := checkOf(t, p, "tls.domain.failing"); !c.problem || c.hold != 30*time.Minute {
		t.Fatalf("reachable but no certificate: %+v", c)
	}
	a.after(time.Minute)
	a.step()
	a.after(6 * time.Minute)
	a.step()
	sent := a.sent()
	if len(sent) != 2 || sent[1].Key != "tls.caddy" || sent[1].State != events.AlertResolved {
		t.Fatalf("the Caddy alert is not fixed: %+v", sent)
	}
	a.after(10 * time.Minute)
	a.step()
	a.expectSent(2) // 11 minutes without a certificate: not announced yet

	// An alert about an address that is open stays open through an outage.
	a.after(25 * time.Minute)
	a.step()
	got := a.sent()
	if len(got) != 4 || got[2].TitleKey != "alert.tls_failing" || got[3].TitleKey != "alert.tls_failing" ||
		got[2].Key+got[3].Key != "tls.domain.failingtls.ip.failing" && got[2].Key+got[3].Key != "tls.ip.failingtls.domain.failing" {
		t.Fatalf("after 30 minutes without a certificate: %+v", got)
	}
	st := a.state("tls.domain.failing")
	if st == nil || st.State != store.AlertProblem {
		t.Fatalf("domain: %+v", st)
	}
	src.rep = down
	for range 8 {
		a.after(time.Minute)
		a.step()
	}
	if after := a.state("tls.domain.failing"); after == nil || after.State != store.AlertProblem || after.PassingSince != nil {
		t.Fatalf("an open alert changed while Caddy was down: %+v", after)
	}

	// No address to check (no domain, or nothing probed): no Caddy check.
	src.rep = &TLSWatchReport{}
	p = probeOf(a, a.w.tls)
	for _, c := range p.checks {
		if c.key == "tls.caddy" {
			t.Fatalf("a Caddy check without addresses: %+v", c)
		}
	}
	if !p.covered {
		t.Fatal("an empty report does not cover its scope")
	}
}

// answerError says why an agent's answer is not a commit: every code the
// agent can answer with has its own words or the generic failure.
func TestTLSAnswerError(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  *tlsconf.Result
		code string
		has  string
	}{
		{"no answer", nil, TLSErrAgentTimeout, "did not answer"},
		{"refused by Caddy", &tlsconf.Result{Code: tlsconf.ResultRefused, Error: "Caddy refused the change: bad"}, TLSErrRefused, "Caddy refused the change: bad"},
		{"Caddy not running", &tlsconf.Result{Code: tlsconf.ResultUnreachable, Error: "Caddy did not answer"}, TLSErrUnreachable, "not running"},
		{"the agent found the request invalid", &tlsconf.Result{Code: tlsconf.ResultInvalid, Error: "the request names the domain evil.example.org"},
			TLSErrAgentFailed, "the request names the domain evil.example.org"},
		{"the files could not be written", &tlsconf.Result{Code: tlsconf.ResultWriteFailed, Error: "the HTTPS files could not be written: disk full"},
			TLSErrAgentFailed, "disk full"},
		{"the agent cannot do its part", &tlsconf.Result{Code: tlsconf.ResultAgentError, Error: "cannot read Caddy's main Caddyfile"},
			TLSErrAgentFailed, "main Caddyfile"},
		{"a code from a newer agent", &tlsconf.Result{Code: "something_new", Error: "x"}, TLSErrAgentFailed, "x"},
	} {
		err := answerError(tc.res)
		var te *TLSError
		if !errors.As(err, &te) || te.Code != tc.code || !strings.Contains(te.Message, tc.has) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

// Through Set: an agent that finds the request invalid, cannot write, or
// cannot do its part fails the change as agent_failed, and the saved
// setting is asked for again.
func TestTLSSetWhenTheAgentFails(t *testing.T) {
	for _, code := range []string{tlsconf.ResultInvalid, tlsconf.ResultWriteFailed, tlsconf.ResultAgentError} {
		t.Run(code, func(t *testing.T) {
			w := newTLSWorld(t)
			ctx := context.Background()
			w.tls.Start(ctx)
			done := make(chan struct{})
			go func() { // a stand-in agent that answers the change with code, and the rollback with ok
				defer close(done)
				for range 500 {
					if r, err := w.ex.ReadRequest(); err == nil && r.Generation >= 2 {
						res := tlsconf.Result{Generation: r.Generation, AppliedAt: time.Now().Unix()}
						if r.Generation == 2 {
							res.Code, res.Error = code, "agent says "+code
						} else {
							res.OK = true
						}
						_ = tlsconf.WriteResult(w.ex.ResultPath, res)
						if r.Generation >= 3 {
							return
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
			}()
			_, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true})
			var te *TLSError
			if !errors.As(err, &te) || te.Code != TLSErrAgentFailed || !strings.Contains(te.Message, "agent says "+code) {
				t.Fatalf("%v", err)
			}
			<-done
			if r := w.request(); r.Generation != 3 || r.Mode != tlsconf.ModeAuto || w.row().Mode != "auto" {
				t.Fatalf("after the failure: %v %+v", r, w.row())
			}
		})
	}
}

// failTLSWrites makes writes to core.tls_config fail until the test ends:
// in the statement, or (atCommit) only when the transaction commits.
func failTLSWrites(t *testing.T, st *store.Store, atCommit bool) {
	t.Helper()
	ctx := context.Background()
	run := func(q string) {
		t.Helper()
		if _, err := st.DB().Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	run(`CREATE OR REPLACE FUNCTION core.tls_test_fail() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'tls test: this write is refused'; END $$`)
	if atCommit {
		run(`CREATE CONSTRAINT TRIGGER tls_test_fail AFTER INSERT OR UPDATE ON core.tls_config DEFERRABLE INITIALLY DEFERRED
			FOR EACH ROW EXECUTE FUNCTION core.tls_test_fail()`)
	} else {
		run(`CREATE TRIGGER tls_test_fail BEFORE INSERT OR UPDATE ON core.tls_config FOR EACH ROW EXECUTE FUNCTION core.tls_test_fail()`)
	}
	t.Cleanup(func() {
		run(`DROP TRIGGER IF EXISTS tls_test_fail ON core.tls_config`)
		run(`DROP FUNCTION IF EXISTS core.tls_test_fail()`)
	})
}

// The agent took a change but the store did not keep it (the row or its
// audit entry could not be written, or the commit failed): Caddy must not go
// on serving a setting the store does not hold, so the committed setting is
// asked for again before Set returns.
func TestTLSSetRollsBackWhenItCannotCommit(t *testing.T) {
	for _, atCommit := range []bool{false, true} {
		name := "the row cannot be written"
		if atCommit {
			name = "the commit fails"
		}
		t.Run(name, func(t *testing.T) {
			w := newTLSWorld(t)
			w.runAgent()
			ctx := context.Background()
			w.tls.Start(ctx)
			if res := w.waitAnswer(1); !res.OK {
				t.Fatalf("start: %+v", res)
			}
			failTLSWrites(t, w.st, atCommit)

			_, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true})
			if err == nil || !strings.Contains(err.Error(), "this write is refused") || atCommit != strings.Contains(err.Error(), "commit") {
				t.Fatalf("set: %v", err)
			}
			// generation 2 was the change, 3 puts the committed setting back
			r := w.request()
			if r.Generation != 3 || r.Mode != tlsconf.ModeAuto || r.IP != "" {
				t.Fatalf("the request after the failed commit: %v", r)
			}
			if res := w.waitAnswer(3); !res.OK {
				t.Fatalf("rollback: %+v", res)
			}
			if sites, _ := os.ReadFile(filepath.Join(w.dir, "tls", "sites.caddy")); strings.Contains(string(sites), "shortlived") {
				t.Fatalf("Caddy still has the setting the store refused:\n%s", sites)
			}
			if got := w.row(); got.Mode != "auto" {
				t.Fatalf("row: %+v", got)
			}
			var audits int
			if err := w.st.DB().QueryRow(ctx, `SELECT count(*) FROM core.audit_log WHERE action = 'system.tls.set'`).Scan(&audits); err != nil || audits != 0 {
				t.Fatalf("audited: %d %v", audits, err)
			}

			// A manual change leaves no key in the request it rolls back to.
			cert, key := leafPEM(t, []string{tlsDomain}, w.now.Add(-time.Hour), w.now.Add(90*24*time.Hour))
			if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeManual, CertPEM: cert, KeyPEM: key}); err == nil {
				t.Fatal("manual: no error")
			}
			if r := w.request(); r.Generation != 5 || r.Mode != tlsconf.ModeAuto || r.KeyPEM != "" || r.CertPEM != "" {
				t.Fatalf("the request after the failed manual change: %v", r)
			}
			// The lock is free again: with the failure gone a change goes through.
			if _, err := w.st.DB().Exec(ctx, `DROP TRIGGER tls_test_fail ON core.tls_config`); err != nil {
				t.Fatal(err)
			}
			if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true}); err != nil {
				t.Fatalf("after: %v", err)
			}
			if got := w.row(); got.Mode != "ip" {
				t.Fatalf("row: %+v", got)
			}
		})
	}
}

// A change that is interrupted (the caller gives up while it waits for the
// agent) is rolled back too, and says so.
func TestTLSSetInterrupted(t *testing.T) {
	w := newTLSWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.tls.Start(ctx)
	go func() { // the request is on the volume; nobody answers it
		for w.request() == nil || w.request().Generation < 2 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	_, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true})
	if err == nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("interrupted: %v", err)
	}
	if r := w.request(); r.Generation != 3 || r.Mode != tlsconf.ModeAuto {
		t.Fatalf("the interrupted change was not rolled back: %v", r)
	}
}

// A private IP address as BOBRES_DOMAIN (the installer accepts one for a lab
// or a LAN) is served with Caddy's own CA: the request is written, the
// agent's files have the IP site with its internal issuer and default_sni,
// the status expects an internal certificate (untrusted is fine), and
// nothing reads "not in use".
func TestTLSPrivateIPDomain(t *testing.T) {
	const lan = "192.168.1.10"
	w := newTLSWorld(t)
	w.rebuild(func(c *TLSConfig) { c.Domain, c.ZarinpalHost = lan, "" })
	w.runAgent()
	ctx := context.Background()
	if !w.tls.Start(ctx) {
		t.Fatal("the lock was free")
	}
	if r := w.request(); r == nil || r.Generation != 1 || r.Mode != tlsconf.ModeAuto || r.Domain != lan {
		t.Fatalf("request: %v", r)
	}
	if res := w.waitAnswer(1); !res.OK {
		t.Fatalf("answer: %+v", res)
	}
	sites, _ := os.ReadFile(filepath.Join(w.dir, "tls", "sites.caddy"))
	global, _ := os.ReadFile(filepath.Join(w.dir, "tls", "global.caddy"))
	if !strings.Contains(string(sites), "\n"+lan+" {\n\ttls internal\n\timport bobres_site\n}\n") ||
		!strings.Contains(string(global), "\ndefault_sni "+lan+"\n") {
		t.Fatalf("the agent's files:\n%s\n%s", global, sites)
	}
	v, err := w.tls.View(ctx, false)
	if err != nil || v.Problem != nil || v.EffectiveMode != tlsconf.ModeAuto || v.Domain != lan || v.PublicURL != "https://"+lan {
		t.Fatalf("view: %+v %v", v, err)
	}

	w.serve(lan, servedFor(lan, false, w.now.Add(30*24*time.Hour))) // Caddy's CA: no browser trusts it
	rep, err := w.tls.Watch(ctx)
	if err != nil || rep.Problem != "" || len(rep.Addresses) != 1 {
		t.Fatalf("watch: %+v %v", rep, err)
	}
	if a := rep.Addresses[0]; a.Host != lan || a.Expect != tlsconf.ExpectInternal || !a.Valid || a.Error != "" {
		t.Fatalf("address: %+v", a)
	}

	// ip mode cannot use another address for it; auto is still accepted.
	_, err = w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true})
	if tlsCode(err) != tlsconf.CodeIPDomainMismatch {
		t.Fatalf("ip mode: %v", err)
	}
	if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeAuto}); err != nil {
		t.Fatalf("auto: %v", err)
	}
}

// A Zarinpal host that is not a host name is left out: the rest is served,
// and the view and the watchdog say why.
func TestTLSZarinpalHostThatIsNotAHostName(t *testing.T) {
	w := newTLSWorld(t)
	w.rebuild(func(c *TLSConfig) { c.ZarinpalHost = "pay.example.net/x" })
	w.runAgent()
	ctx := context.Background()
	w.tls.Start(ctx)
	if r := w.request(); r.Domain != tlsDomain || r.ZarinpalHost != "" {
		t.Fatalf("request: %v", r)
	}
	if res := w.waitAnswer(1); !res.OK {
		t.Fatalf("answer: %+v", res)
	}
	for _, row := range []*store.TLSRow{{Mode: "auto"}, {Mode: "ip", IP: tlsIP}} {
		w.setRow(row)
		v, err := w.tls.View(ctx, false)
		if err != nil || v.Problem == nil || v.Problem.Code != TLSProblemZarinpalHost || v.EffectiveMode != tlsconf.Mode(row.Mode) ||
			v.ZarinpalHost != "" || v.Domain != tlsDomain {
			t.Fatalf("%s: %+v %+v %v", row.Mode, v, v.Problem, err)
		}
		w.serve(tlsDomain, servedFor(tlsDomain, true, w.now.Add(60*24*time.Hour)))
		rep, err := w.tls.Watch(ctx)
		if err != nil || !strings.Contains(rep.Problem, "BOBRES_ZARINPAL_HOST is not a host name") {
			t.Fatalf("%s watch: %+v %v", row.Mode, rep, err)
		}
	}
	// and a change is still made, without the Zarinpal site
	if _, err := w.tls.Set(ctx, nil, TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true}); err != nil {
		t.Fatal(err)
	}
	if r := w.request(); r.Mode != tlsconf.ModeIP || r.ZarinpalHost != "" {
		t.Fatalf("request: %v", r)
	}
}

// The request on the volume follows the saved setting: a pair that expires
// while core runs falls back to auto (the view already says "not in use"), a
// change that was interrupted before its rollback is undone, a lost or
// replaced request is written again; a change being applied is left alone.
func TestTLSReconcile(t *testing.T) {
	w := newTLSWorld(t)
	ctx := context.Background()
	cert, key := leafPEM(t, []string{tlsDomain}, w.now.Add(-time.Hour), w.now.Add(10*24*time.Hour))
	w.setRow(manualRow(t, w.cfg.Secrets, cert, key))
	w.tls.Start(ctx)
	if r := w.request(); r.Generation != 1 || r.Mode != tlsconf.ModeManual || r.KeyPEM != string(key) {
		t.Fatalf("start: %v", r)
	}
	w.tls.reconcile(ctx)
	if r := w.request(); r.Generation != 1 {
		t.Fatalf("a request that is the saved setting was written again: %v", r)
	}

	// The pair expires.
	w.after(11 * 24 * time.Hour)
	if v, err := w.tls.View(ctx, false); err != nil || v.Problem == nil || v.Problem.Code != tlsconf.CodeCertExpired || v.EffectiveMode != tlsconf.ModeAuto {
		t.Fatalf("view: %+v %v", v, err)
	}
	w.tls.reconcile(ctx)
	if r := w.request(); r.Generation != 2 || r.Mode != tlsconf.ModeAuto || r.KeyPEM != "" || r.CertPEM != "" {
		t.Fatalf("the expired pair is still asked for: %v", r)
	}
	w.tls.reconcile(ctx)
	if r := w.request(); r.Generation != 2 {
		t.Fatalf("written again: %v", r)
	}

	// A change that never got its rollback (its process was killed).
	w.setRow(&store.TLSRow{Mode: "auto"})
	if err := w.ex.WriteRequest(tlsconf.Request{Generation: 3, Mode: tlsconf.ModeIP, IP: tlsIP, Domain: tlsDomain, ZarinpalHost: tlsZarinpal}); err != nil {
		t.Fatal(err)
	}
	w.tls.reconcile(ctx)
	if r := w.request(); r.Generation != 4 || r.Mode != tlsconf.ModeAuto || r.IP != "" {
		t.Fatalf("the interrupted change stays: %v", r)
	}

	// A change being applied by another process (it holds the advisory
	// lock) or by this one (the process lock) is not touched.
	if err := w.ex.WriteRequest(tlsconf.Request{Generation: 5, Mode: tlsconf.ModeIP, IP: tlsIP, Domain: tlsDomain, ZarinpalHost: tlsZarinpal}); err != nil {
		t.Fatal(err)
	}
	tx, err := w.st.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.st.LockTLS(ctx, tx); err != nil {
		t.Fatal(err)
	}
	w.tls.reconcile(ctx)
	_ = tx.Rollback(ctx)
	if r := w.request(); r.Generation != 5 || r.Mode != tlsconf.ModeIP {
		t.Fatalf("a change being applied elsewhere was undone: %v", r)
	}
	w.tls.mu.Lock()
	w.tls.reconcile(ctx)
	w.tls.mu.Unlock()
	if r := w.request(); r.Generation != 5 {
		t.Fatalf("a change being applied here was undone: %v", r)
	}
	w.tls.reconcile(ctx)
	if r := w.request(); r.Generation != 6 || r.Mode != tlsconf.ModeAuto {
		t.Fatalf("once the lock is free: %v", r)
	}

	// A request that was removed, or is not readable, is written again.
	for _, content := range []string{"", "{not json"} {
		if content == "" {
			_ = os.Remove(w.ex.RequestPath)
		} else if err := os.WriteFile(w.ex.RequestPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		w.tls.reconcile(ctx)
		if r := w.request(); r == nil || r.Mode != tlsconf.ModeAuto || r.Domain != tlsDomain {
			t.Fatalf("after %q: %v", content, r)
		}
	}

	// The domain changed in core's environment (BOBRES_DOMAIN).
	w.rebuild(func(c *TLSConfig) { c.Domain = "shop.example.com" })
	w.tls.reconcile(ctx)
	if r := w.request(); r.Domain != "shop.example.com" {
		t.Fatalf("a new domain: %v", r)
	}
}

// Run keeps the request in step with the saved setting until it is stopped:
// it starts, then reconciles on its tick.
func TestTLSRunReconciles(t *testing.T) {
	w := newTLSWorld(t)
	w.rebuild(func(c *TLSConfig) { c.Reconcile = 20 * time.Millisecond })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.tls.Run(ctx) }()
	waitFor := func(what string, ok func(r *tlsconf.Request) bool) {
		t.Helper()
		for range 300 {
			if r := w.request(); r != nil && ok(r) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%s: the request is %v", what, w.request())
	}
	waitFor("start", func(r *tlsconf.Request) bool { return r.Mode == tlsconf.ModeAuto })
	if err := w.ex.WriteRequest(tlsconf.Request{Generation: 10, Mode: tlsconf.ModeIP, IP: tlsIP, Domain: tlsDomain, ZarinpalHost: tlsZarinpal}); err != nil {
		t.Fatal(err)
	}
	waitFor("reconcile", func(r *tlsconf.Request) bool { return r.Mode == tlsconf.ModeAuto && r.Generation > 10 })
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}

	// With nothing to manage it returns at once.
	w.rebuild(func(c *TLSConfig) { c.Exchange = nil })
	if err := w.tls.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A change waits only a few seconds for another one to finish (in another
// process, or in this one), then answers busy; the agent is not asked.
func TestTLSSetBusy(t *testing.T) {
	w := newTLSWorld(t)
	w.rebuild(func(c *TLSConfig) { c.BusyWait = 150 * time.Millisecond })
	w.runAgent()
	ctx := context.Background()
	change := TLSChange{Mode: tlsconf.ModeIP, IP: tlsIP, ConfirmIP: true}

	tx, err := w.st.DB().Begin(ctx) // `bobres ssl` in the middle of its change
	if err != nil {
		t.Fatal(err)
	}
	if err := w.st.LockTLS(ctx, tx); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	_, err = w.tls.Set(ctx, nil, change)
	if took := time.Since(began); tlsCode(err) != TLSErrBusy || took < 100*time.Millisecond || took > 3*time.Second {
		t.Fatalf("another process holds the lock: %v after %v", err, took)
	}
	_ = tx.Rollback(ctx)
	if w.request() != nil {
		t.Fatal("the agent was asked while the change was refused as busy")
	}

	w.tls.mu.Lock() // a change in this process
	began = time.Now()
	_, err = w.tls.Set(ctx, nil, change)
	if took := time.Since(began); tlsCode(err) != TLSErrBusy || took < 100*time.Millisecond || took > 3*time.Second {
		t.Fatalf("this process holds the lock: %v after %v", err, took)
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	if _, err = w.tls.Set(cctx, nil, change); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a caller that gave up while it waited: %v", err)
	}
	cancel()
	w.tls.mu.Unlock()

	if _, err := w.tls.Set(ctx, nil, change); err != nil {
		t.Fatalf("with the locks free: %v", err)
	}
}
