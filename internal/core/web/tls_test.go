package web

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
)

// fakeAgent answers every new request as told (ok when code is "").
type fakeAgent struct {
	mu        sync.Mutex
	code, msg string
}

func runFakeAgent(t *testing.T, ex tlsconf.Exchange) *fakeAgent {
	t.Helper()
	a := &fakeAgent{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		var last int64
		for ctx.Err() == nil {
			if r, err := ex.ReadRequest(); err == nil && r.Generation != last {
				last = r.Generation
				a.mu.Lock()
				res := tlsconf.Result{Generation: r.Generation, OK: a.code == "", Code: a.code, Error: a.msg, AppliedAt: time.Now().Unix()}
				a.mu.Unlock()
				_ = tlsconf.WriteResult(ex.ResultPath, res)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	return a
}

func (a *fakeAgent) answer(code, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.code, a.msg = code, msg
}

// fakeIP is the bot's address detection.
type fakeIP struct {
	mu  sync.Mutex
	ip  string
	err error
	n   int
}

func (f *fakeIP) PublicIP(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return f.ip, f.err
}

// tlsProbes counts the live probes the fixture's manager made.
var tlsProbes atomic.Int64

// tlsFixture is the dashboard with the HTTPS manager on temp volumes, a
// fake agent and a fake bot, the owner logged in.
func tlsFixture(t *testing.T) (*fixture, *browser, *fakeAgent, *fakeIP) {
	t.Helper()
	f := newFixture(t)
	if err := testdb.Truncate(context.Background(), f.st.DB(), `core.tls_config`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, d := range []string{"request", "result"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ex := tlsconf.NewExchange(filepath.Join(dir, "request"), filepath.Join(dir, "result"))
	tlsProbes.Store(0)
	f.api.cfg.TLS = f.dom.NewTLS(domain.TLSConfig{Domain: "panel.example.com", Secrets: f.api.cfg.Secrets, Exchange: ex,
		Probe: func(context.Context, string) tlsprobe.Result {
			tlsProbes.Add(1)
			return tlsprobe.Result{Error: "no certificate yet"}
		},
		Wait: 3 * time.Second, Poll: 5 * time.Millisecond, BusyWait: 200 * time.Millisecond})
	ip := &fakeIP{ip: "1.2.3.4"}
	f.api.cfg.PublicIP = ip
	f.api.probes = newLimiter(100, time.Minute) // these tests make more than 10 changes
	agent := runFakeAgent(t, ex)
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	if code := b.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	return f, b, agent, ip
}

func testPair(t *testing.T, names ...string) (cert, key string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: names[0]},
		DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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

// The HTTPS tab: the state for owner and admin, not for support.
func TestTLSStateEndpoint(t *testing.T) {
	f, b, _, _ := tlsFixture(t)
	code, out := b.do(http.MethodGet, "/api/v1/system/tls", nil)
	st, _ := out["state"].(map[string]any)
	status, _ := out["status"].(map[string]any)
	if code != 200 || st["mode"] != "auto" || st["domain"] != "panel.example.com" || st["manual_allowed"] != true ||
		out["public_url"] != "https://panel.example.com" || status == nil || status["checked_at"] == nil {
		t.Fatalf("state: %d %v", code, out)
	}
	addrs, _ := status["addresses"].([]any)
	if len(addrs) != 1 || addrs[0].(map[string]any)["error"] != "no certificate yet" {
		t.Fatalf("addresses: %v", status)
	}
	f.staffUser(8601, "admin")
	admin := f.browser()
	admin.loginWithLink(8601)
	if code, _ := admin.do(http.MethodGet, "/api/v1/system/tls", nil); code != 200 {
		t.Fatalf("admin read: %d", code)
	}
	if code, _ := admin.do(http.MethodPut, "/api/v1/system/tls", map[string]any{"mode": "auto"}); code != 403 {
		t.Fatalf("admin change: %d", code)
	}
	if code, _ := admin.do(http.MethodPost, "/api/v1/system/tls/detect-ip", nil); code != 403 {
		t.Fatalf("admin detect: %d", code)
	}
	f.staffUser(8602, "support")
	sup := f.browser()
	sup.loginWithLink(8602)
	if code, _ := sup.do(http.MethodGet, "/api/v1/system/tls", nil); code != 403 {
		t.Fatalf("support read: %d", code)
	}
	f.api.cfg.TLS = nil
	if code, out := b.do(http.MethodGet, "/api/v1/system/tls", nil); code != 503 || out["error"] != "not_configured" {
		t.Fatalf("without the manager: %d %v", code, out)
	}
}

// Changing the mode: each answer with its code; the state comes back once
// the agent took it; the key never does, and the change is audited.
func TestTLSChangeEndpoint(t *testing.T) {
	f, b, agent, ip := tlsFixture(t)
	put := func(body map[string]any) (int, map[string]any) {
		return b.do(http.MethodPut, "/api/v1/system/tls", body)
	}

	code, out := put(map[string]any{"mode": "auto"})
	if code != 200 || out["pending"] != false || len(out["warnings"].([]any)) != 0 || out["state"].(map[string]any)["mode"] != "auto" {
		t.Fatalf("auto: %d %v", code, out)
	}

	// ip: the detected address is the same one: no confirmation needed.
	if code, out = put(map[string]any{"mode": "ip", "ip": "1.2.3.4"}); code != 200 || out["state"].(map[string]any)["ip"] != "1.2.3.4" {
		t.Fatalf("ip: %d %v", code, out)
	}
	ip.mu.Lock()
	ip.ip, ip.n = "5.6.7.8", 0
	ip.mu.Unlock()
	f.api.cfg.TLS.NoteDetectedIP("5.6.7.8")
	if code, out = put(map[string]any{"mode": "ip", "ip": "1.2.3.9"}); code != 409 || out["error"] != "confirm" ||
		out["code"] != "ip_unconfirmed" || out["detected_ip"] != "5.6.7.8" || out["field"] != "ip" {
		t.Fatalf("another IP: %d %v", code, out)
	}
	if code, out = put(map[string]any{"mode": "ip", "ip": "1.2.3.9", "confirm_ip": true}); code != 200 {
		t.Fatalf("confirmed: %d %v", code, out)
	}

	cert, key := testPair(t, "panel.example.com")
	code, out = put(map[string]any{"mode": "manual", "cert": cert, "key": key})
	if code != 200 || out["state"].(map[string]any)["mode"] != "manual" {
		t.Fatalf("manual: %d %v", code, out)
	}
	warnings := asJSON(out["warnings"])
	if !strings.Contains(warnings, `"code":"self_signed"`) || strings.Contains(asJSON(out), strings.Split(key, "\n")[1]) {
		t.Fatalf("manual answer: %s", asJSON(out))
	}
	e := lastAudit(t, b, "action=system.tls.set")
	if !strings.Contains(asJSON(e["after"]), `"mode":"manual"`) || strings.Contains(asJSON(e), strings.Split(key, "\n")[1]) {
		t.Fatalf("audit: %v", e)
	}

	for _, tc := range []struct {
		name  string
		body  map[string]any
		code  string
		field string
	}{
		{"an unknown mode", map[string]any{"mode": "proxy"}, "bad_mode", "mode"},
		{"a private IP", map[string]any{"mode": "ip", "ip": "10.1.2.3", "confirm_ip": true}, "ip_not_public", "ip"},
		{"the wrong key", map[string]any{"mode": "manual", "cert": cert, "key": func() string { _, k := testPair(t, "x.example.com"); return k }()},
			"key_mismatch", "key"},
		{"another name", func() map[string]any {
			c, k := testPair(t, "shop.example.com")
			return map[string]any{"mode": "manual", "cert": c, "key": k}
		}(), "cert_name_mismatch", "cert"},
	} {
		if code, out := put(tc.body); code != 400 || out["error"] != "invalid" || out["code"] != tc.code || out["field"] != tc.field {
			t.Errorf("%s: %d %v", tc.name, code, out)
		}
	}
	if code, _ := put(map[string]any{"mode": "auto", "extra": 1}); code != 400 {
		t.Fatalf("an unknown field: %d", code)
	}

	agent.answer(tlsconf.ResultRefused, "Caddy refused the change: loading config: bad")
	if code, out = put(map[string]any{"mode": "auto"}); code != 409 || out["error"] != "refused" || out["code"] != "caddy_refused" ||
		!strings.Contains(out["message"].(string), "loading config: bad") {
		t.Fatalf("refused: %d %v", code, out)
	}
	agent.answer(tlsconf.ResultUnreachable, "")
	if code, out = put(map[string]any{"mode": "ip", "ip": "1.2.3.9", "confirm_ip": true}); code != 503 || out["code"] != "caddy_unreachable" {
		t.Fatalf("Caddy down: %d %v", code, out)
	}
	if code, out = put(map[string]any{"mode": "auto"}); code != 200 || out["pending"] != true {
		t.Fatalf("auto while Caddy is down: %d %v", code, out)
	}
	f.api.probes = newLimiter(1, time.Minute)
	put(map[string]any{"mode": "auto"})
	if code, out = put(map[string]any{"mode": "auto"}); code != 429 || out["error"] != "rate_limited" {
		t.Fatalf("over the limit: %d %v", code, out)
	}
}

// Detecting the server's address through the bot.
func TestTLSDetectIPEndpoint(t *testing.T) {
	f, b, _, ip := tlsFixture(t)
	code, out := b.do(http.MethodPost, "/api/v1/system/tls/detect-ip", nil)
	if code != 200 || out["ip"] != "1.2.3.4" {
		t.Fatalf("detect: %d %v", code, out)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/system/tls", nil); out["detected_ip"] != "1.2.3.4" {
		t.Fatalf("the state does not show it: %v", out)
	}
	ip.mu.Lock()
	ip.err = errors.New("unreachable")
	ip.mu.Unlock()
	if code, out := b.do(http.MethodPost, "/api/v1/system/tls/detect-ip", nil); code != 502 || out["code"] != "detect_failed" {
		t.Fatalf("a failed detection: %d %v", code, out)
	}
	f.api.cfg.PublicIP = nil
	if code, out := b.do(http.MethodPost, "/api/v1/system/tls/detect-ip", nil); code != 503 || out["error"] != "not_configured" {
		t.Fatalf("without a bot: %d %v", code, out)
	}
	// The address detected before still counts: another one needs a
	// confirmation.
	if code, out := b.do(http.MethodPut, "/api/v1/system/tls", map[string]any{"mode": "ip", "ip": "1.2.3.9"}); code != 409 ||
		out["detected_ip"] != "1.2.3.4" {
		t.Fatalf("an unconfirmed IP without a bot: %d %v", code, out)
	}
}

// An agent that finds a request invalid, cannot write its files or cannot do
// its part fails the change as agent_failed (409 refused), with its words.
func TestTLSChangeAgentFailures(t *testing.T) {
	_, b, agent, _ := tlsFixture(t)
	for _, code := range []string{tlsconf.ResultInvalid, tlsconf.ResultWriteFailed, tlsconf.ResultAgentError} {
		agent.answer(code, "agent says "+code)
		code409, out := b.do(http.MethodPut, "/api/v1/system/tls", map[string]any{"mode": "auto"})
		if code409 != 409 || out["error"] != "refused" || out["code"] != "agent_failed" || !strings.Contains(out["message"].(string), "agent says "+code) {
			t.Errorf("%s: %d %v", code, code409, out)
		}
	}
}

// Another change in progress (here, `bobres ssl` holds the lock): a change
// waits a few seconds, then answers 503 busy, which the dashboard says as
// "try again" and which stays inside core's write timeout.
func TestTLSChangeBusy(t *testing.T) {
	f, b, _, _ := tlsFixture(t)
	ctx := context.Background()
	tx, err := f.st.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.LockTLS(ctx, tx); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	code, out := b.do(http.MethodPut, "/api/v1/system/tls", map[string]any{"mode": "auto"})
	_ = tx.Rollback(ctx)
	if code != 503 || out["error"] != "unavailable" || out["code"] != "busy" || time.Since(began) > 5*time.Second {
		t.Fatalf("busy: %d %v after %v", code, out, time.Since(began))
	}
	if code, out = b.do(http.MethodPut, "/api/v1/system/tls", map[string]any{"mode": "auto"}); code != 200 {
		t.Fatalf("once the lock is free: %d %v", code, out)
	}
}

// The answer to a change does not dial Caddy or ask the bot again (the page
// polls the state for the certificate): a change that waited for the agent
// would otherwise run past core's 30-second write timeout.
func TestTLSChangeAnswersWithoutProbing(t *testing.T) {
	_, b, _, ip := tlsFixture(t)
	if code, out := b.do(http.MethodPut, "/api/v1/system/tls", map[string]any{"mode": "ip", "ip": "1.2.3.4"}); code != 200 {
		t.Fatalf("ip: %d %v", code, out)
	}
	asked := tlsProbes.Load()
	ip.mu.Lock()
	n := ip.n
	ip.mu.Unlock()
	code, out := b.do(http.MethodPut, "/api/v1/system/tls", map[string]any{"mode": "ip", "ip": "1.2.3.4", "confirm_ip": true})
	status, _ := out["status"].(map[string]any)
	addrs, _ := status["addresses"].([]any)
	if code != 200 || len(addrs) != 0 || status["checked_at"] != nil || tlsProbes.Load() != asked {
		t.Fatalf("answer: %d %v, %d probes", code, out, tlsProbes.Load()-asked)
	}
	ip.mu.Lock()
	defer ip.mu.Unlock()
	if ip.n != n {
		t.Fatalf("the bot was asked %d more times for an answer", ip.n-n)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/system/tls", nil); tlsProbes.Load() == asked {
		t.Fatalf("GET does not probe: %v", out)
	}
}
