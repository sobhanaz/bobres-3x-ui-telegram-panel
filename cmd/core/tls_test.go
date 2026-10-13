package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsagent"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

func TestParseSSL(t *testing.T) {
	for in, want := range map[string]sslArgs{
		"status":                              {sub: "status"},
		"status --json":                       {sub: "status", json: true},
		"auto":                                {sub: "auto"},
		"manual":                              {sub: "manual"},
		"ip 1.2.3.4":                          {sub: "ip", ip: "1.2.3.4"},
		"ip --yes 1.2.3.4":                    {sub: "ip", ip: "1.2.3.4", yes: true},
		"ip 1.2.3.4 --detected 5.6.7.8":       {sub: "ip", ip: "1.2.3.4", detected: "5.6.7.8"},
		"ip --detected=5.6.7.8 1.2.3.4 --yes": {sub: "ip", ip: "1.2.3.4", detected: "5.6.7.8", yes: true},
	} {
		if got, ok := parseSSL(strings.Fields(in)); !ok || got != want {
			t.Errorf("%q: %+v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "status --yaml", "auto now", "manual --cert x", "ip", "ip 1.2.3.4 5.6.7.8", "ip 1.2.3.4 --force",
		"ip 1.2.3.4 --detected", "renew", "--json status"} {
		if got, ok := parseSSL(strings.Fields(in)); ok {
			t.Errorf("%q passed: %+v", in, got)
		}
	}
}

// Wrong use answers with the usage (exit 2), and a bad bundle is refused
// (exit 1), before the database is touched; neither ever echoes the key.
func TestSSLCmdUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"renew"}, {"ip"}, {"status", "extra"}} {
		var out, errOut bytes.Buffer
		if code := sslCmd(args, strings.NewReader(""), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage:") {
			t.Errorf("%v: %d %q", args, code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := sslCmd([]string{"manual"}, strings.NewReader(" \n"), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "no PEM") {
		t.Fatalf("empty stdin: %d %q", code, errOut.String())
	}
	_, key := sslPair(t, "panel.example.com")
	errOut.Reset()
	if code := sslCmd([]string{"manual"}, strings.NewReader(key), &out, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "no certificate") || strings.Contains(errOut.String(), strings.Split(key, "\n")[1]) {
		t.Fatalf("a key alone: %d %q", code, errOut.String())
	}
}

// sslPair is a self-signed certificate for names and its key, as PEM.
func sslPair(t *testing.T, names ...string) (cert, key string) {
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

// sslAgent answers every new request in the volumes as told.
type sslAgent struct {
	mu   sync.Mutex
	code string
	msg  string
}

func runSSLAgent(t *testing.T, ex tlsconf.Exchange) *sslAgent {
	t.Helper()
	a := &sslAgent{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		var last int64
		for ctx.Err() == nil {
			if r, err := ex.ReadRequest(); err == nil && r.Generation != last {
				last = r.Generation
				a.mu.Lock()
				_ = tlsconf.WriteResult(ex.ResultPath, tlsconf.Result{Generation: r.Generation, OK: a.code == "", Code: a.code, Error: a.msg,
					AppliedAt: time.Now().Unix()})
				a.mu.Unlock()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	return a
}

// `ssl` changes the setting the way the dashboard does, through the agent's
// volumes, and shows it; an IP address nobody detected needs --yes.
func TestSSLCmdWithTheStore(t *testing.T) {
	coreEnv(t)
	dir := t.TempDir()
	reqDir, resDir := filepath.Join(dir, "request"), filepath.Join(dir, "result")
	for _, d := range []string{reqDir, resDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BOBRES_TLS_REQUEST_DIR", reqDir)
	t.Setenv("BOBRES_TLS_RESULT_DIR", resDir)
	t.Setenv("BOBRES_TLS_PROBE_ADDR", "127.0.0.1:1") // nothing answers: no certificate yet
	t.Setenv("BOBRES_MASTER_KEY", strings.Repeat("k", 32))
	agent := runSSLAgent(t, tlsconf.NewExchange(reqDir, resDir))
	run := func(stdin string, args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := sslCmd(args, strings.NewReader(stdin), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	if code, out, errOut := run("", "auto"); code != 0 || !strings.Contains(out, "HTTPS is now auto") ||
		!strings.Contains(out, "Domain:         panel.example.com") || !strings.Contains(out, "Applier:        request 1 applied") {
		t.Fatalf("auto: %d\n%s%s", code, out, errOut)
	}
	if code, _, errOut := run("", "ip", "1.2.3.4"); code != 1 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("an IP nobody detected: %d %q", code, errOut)
	}
	if code, out, errOut := run("", "ip", "1.2.3.4", "--detected", "1.2.3.4"); code != 0 || !strings.Contains(out, "HTTPS is now ip") ||
		!strings.Contains(out, "Server IP:      1.2.3.4") || !strings.Contains(out, "Dashboard:      https://panel.example.com") {
		t.Fatalf("ip: %d\n%s%s", code, out, errOut)
	}
	code, out, errOut := run("", "status", "--json")
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); code != 0 || err != nil || v["state"].(map[string]any)["mode"] != "ip" {
		t.Fatalf("status --json: %d %v\n%s%s", code, err, out, errOut)
	}
	cert, key := sslPair(t, "panel.example.com")
	if code, out, errOut := run(key+cert, "manual"); code != 0 || !strings.Contains(out, "HTTPS is now manual") ||
		!strings.Contains(out, "Warning: the certificate is self-signed") || strings.Contains(out+errOut, strings.Split(key, "\n")[1]) {
		t.Fatalf("manual: %d\n%s%s", code, out, errOut)
	}
	agent.mu.Lock()
	agent.code, agent.msg = tlsconf.ResultRefused, "Caddy refused the change: bad"
	agent.mu.Unlock()
	if code, _, errOut := run("", "auto"); code != 1 || !strings.Contains(errOut, "not applied: Caddy refused the change: bad (caddy_refused)") {
		t.Fatalf("refused: %d %q", code, errOut)
	}
	if code, out, _ := run("", "status"); code != 0 || !strings.Contains(out, "Mode:           manual") ||
		!strings.Contains(out, "panel.example.com              none") {
		t.Fatalf("status after the refusal:\n%s", out)
	}
}

// The agent's health check reads its heartbeat; it takes flags only.
func TestTLSAgentCmd(t *testing.T) {
	beat := filepath.Join(t.TempDir(), "alive")
	var errOut bytes.Buffer
	if code := tlsAgentCmd([]string{"--healthcheck", "--heartbeat", beat}, &errOut); code != 1 {
		t.Fatalf("no heartbeat: %d", code)
	}
	if err := os.WriteFile(beat, []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := tlsAgentCmd([]string{"--healthcheck", "--heartbeat", beat}, &errOut); code != 0 {
		t.Fatalf("a fresh heartbeat: %d", code)
	}
	if code := tlsAgentCmd([]string{"now"}, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage: tls-agent") {
		t.Fatalf("an argument: %d %q", code, errOut.String())
	}
}

// The agent takes the install's own domain and Zarinpal host from its
// environment and refuses requests for others; with no BOBRES_DOMAIN it
// refuses every request (never a default that accepts any name).
func TestAgentConfigPinsTheInstallsNames(t *testing.T) {
	dir := t.TempDir()
	flags := []string{"--request", filepath.Join(dir, "tls.json"), "--result", filepath.Join(dir, "result.json"),
		"--tls-dir", filepath.Join(dir, "tls"), "--caddyfile", filepath.Join(dir, "no-such-Caddyfile")}
	exchange := tlsconf.NewExchange(dir, dir)
	exchange.RequestPath, exchange.ResultPath = flags[1], flags[3]
	answer := func(env map[string]string, r tlsconf.Request) *tlsconf.Result {
		t.Helper()
		var errOut bytes.Buffer
		c, health, ok := agentConfig(flags, func(k string) string { return env[k] }, &errOut)
		if !ok || health || c.Unpinned {
			t.Fatalf("config: %+v %v %v %q", c, health, ok, errOut.String())
		}
		_ = os.Remove(flags[3])
		if err := exchange.WriteRequest(r); err != nil {
			t.Fatal(err)
		}
		tlsagent.New(c).Step(context.Background())
		res, err := exchange.ReadResult()
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	own := map[string]string{"BOBRES_DOMAIN": " Panel.Example.com\n", "BOBRES_ZARINPAL_HOST": "pay.example.net "}
	ask := func(domain, zp string) tlsconf.Request {
		return tlsconf.Request{Generation: 1, Mode: tlsconf.ModeAuto, Domain: domain, ZarinpalHost: zp}
	}
	// Its own names pass the check (the agent then stops at the missing
	// main Caddyfile, which is no concern here).
	if r := answer(own, ask("panel.example.com", "pay.example.net")); r.Code != tlsconf.ResultAgentError {
		t.Fatalf("its own names: %+v", r)
	}
	for name, req := range map[string]tlsconf.Request{"another domain": ask("evil.example.org", "pay.example.net"),
		"another Zarinpal host": ask("panel.example.com", "pay.evil.org"), "no Zarinpal host": ask("panel.example.com", "")} {
		if r := answer(own, req); r.Code != tlsconf.ResultInvalid {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r := answer(map[string]string{}, ask("panel.example.com", "")); r.Code != tlsconf.ResultInvalid ||
		!strings.Contains(r.Error, "BOBRES_DOMAIN") {
		t.Fatalf("no BOBRES_DOMAIN: %+v", r)
	}
	var errOut bytes.Buffer
	if _, _, ok := agentConfig([]string{"now"}, os.Getenv, &errOut); ok || !strings.Contains(errOut.String(), "usage: tls-agent") {
		t.Fatalf("an argument: %v %q", ok, errOut.String())
	}
}

// The manager every core process builds asks the bot for the server's
// address (the links follow ip mode only while the bot confirms it) when
// the bot is set, with core's service token; without a bot there is no
// detection.
func TestBotDetector(t *testing.T) {
	if botDetector(config.Core{}) != nil {
		t.Fatal("a detector without a bot")
	}
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/internal/public-ip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"ip":"1.2.3.4"}`))
	}))
	defer srv.Close()
	detect := botDetector(config.Core{BotURL: srv.URL, BotInternalToken: strings.Repeat("t", 40)})
	if detect == nil {
		t.Fatal("no detector with a bot")
	}
	if ip, err := detect(context.Background()); err != nil || ip != "1.2.3.4" || auth != "Bearer "+strings.Repeat("t", 40) {
		t.Fatalf("detect: %q %v (%q)", ip, err, auth)
	}
}

func TestPrintTLS(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	until := now.Add(49 * 24 * time.Hour)
	yes, no := true, false
	applied := now.Add(-time.Minute)
	v := &domain.TLSView{Mode: tlsconf.ModeIP, IP: "1.2.3.4", Domain: "panel.example.com", ZarinpalHost: "pay.example.net",
		EffectiveMode: tlsconf.ModeIP, EffectiveIP: "1.2.3.4", PublicURL: "https://1.2.3.4",
		Agent: &domain.TLSAgentView{Generation: 4, Answered: true, OK: true, AppliedAt: &applied},
		Addresses: []domain.TLSAddress{
			{Host: "panel.example.com", Served: true, Valid: true, Trusted: &yes, Cert: &tlsconf.CertInfo{Issuer: "CN=R11", NotAfter: until}},
			{Host: "1.2.3.4", Error: "remote error: tls: internal error"},
			{Host: "pay.example.net", Served: true, Trusted: &no, Error: "certificate signed by unknown authority",
				Cert: &tlsconf.CertInfo{Issuer: "CN=Caddy Local Authority", NotAfter: now.Add(time.Hour)}},
		}}
	var b bytes.Buffer
	printTLS(&b, v, now)
	for _, want := range []string{
		"Mode:           ip (a Let's Encrypt certificate for the server's IP address: about 6 days, renewed automatically)",
		"Server IP:      1.2.3.4", "Zarinpal host:  pay.example.net", "Dashboard:      https://1.2.3.4",
		"Applier:        request 4 applied at 2026-10-13 11:59:00 UTC",
		"  panel.example.com              ok        by CN=R11, until 2026-12-01 (49 days left), publicly trusted",
		"  1.2.3.4                        none      remote error: tls: internal error",
		"  pay.example.net                invalid   by CN=Caddy Local Authority, until 2026-10-13 (1 day left), not publicly trusted: certificate signed by unknown authority",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in:\n%s", want, b.String())
		}
	}
	b.Reset()
	v.Problem = &domain.TLSProblem{Code: "key_unreadable", Message: "your certificate is not in use"}
	v.EffectiveMode, v.Agent = tlsconf.ModeAuto, nil
	printTLS(&b, v, now)
	if !strings.Contains(b.String(), "Not in use:     your certificate is not in use (key_unreadable)") ||
		!strings.Contains(b.String(), "Serving:        auto") || !strings.Contains(b.String(), "Applier:        not set up here") {
		t.Errorf("a problem:\n%s", b.String())
	}
}
