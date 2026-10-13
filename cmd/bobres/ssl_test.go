package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
)

// fakeSSL stands in for docker in the ssl command: core's `ssl` runs are
// recorded (their stdin too), and `ssl status --json` answers with json.
type fakeSSL struct {
	streams [][]string
	pipes   []pipeCall
	runs    [][]string
	json    string
	runErr  error
	// failWith is the error of the streamed and piped runs.
	failWith error
}

type pipeCall struct {
	args  []string
	stdin string
}

func (f *fakeSSL) ops(stdin string) (*ops, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &ops{
		in: strings.NewReader(stdin), out: &out, errOut: &errOut,
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			f.runs = append(f.runs, args)
			if contains(args, "--json") {
				return []byte(f.json), f.runErr
			}
			return nil, nil
		},
		stream: func(_ context.Context, _ string, args ...string) error {
			f.streams = append(f.streams, args)
			return f.failWith
		},
		pipe: func(_ context.Context, _ string, in io.Reader, w io.Writer, args ...string) error {
			b, _ := io.ReadAll(in)
			f.pipes = append(f.pipes, pipeCall{args, string(b)})
			_, _ = io.WriteString(w, "HTTPS is now manual.\n")
			return f.failWith
		},
		now:      func() time.Time { return time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC) },
		detectIP: func() string { return "93.184.216.34" },
	}, &out, &errOut
}

// coreArgs are the words after the compose project options.
func coreArgs(args []string) string {
	i := slicesIndex(args, "exec")
	if i < 0 {
		return strings.Join(args, " ")
	}
	return strings.Join(args[i:], " ")
}

func slicesIndex(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

const coreJSON = `{"state":{"mode":"auto"},"status":{"addresses":[
 {"address":"panel.example.com","role":"domain"},{"address":"pay.example.net","role":"zarinpal"}]}}`

func TestSSLStatusShowsCoresStatusAndWhatThisHostServes(t *testing.T) {
	dir := installDir(t)
	f := &fakeSSL{json: "WARN something\n" + coreJSON}
	o, out, errOut := f.ops("")
	trusted := true
	o.probeTLS = func(_ context.Context, address string) tlsprobe.Result {
		if address == "pay.example.net" {
			return tlsprobe.Result{Error: "tls: internal error"}
		}
		return tlsprobe.Result{Served: true, NameOK: true, Trusted: &trusted, Info: tlsconf.CertInfo{
			Issuer: "CN=E7,O=Let's Encrypt", NotBefore: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2026, 12, 30, 0, 0, 0, 0, time.UTC)}}
	}
	for _, args := range [][]string{{"status", "--dir", dir}, {"--dir", dir}} {
		f.streams, f.runs = nil, nil
		out.Reset()
		if code := o.sslCmd(args); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut)
		}
		if len(f.streams) != 1 || coreArgs(f.streams[0]) != "exec -T core /app ssl status" {
			t.Fatalf("%v: core runs %v", args, f.streams)
		}
		if len(f.runs) != 1 || coreArgs(f.runs[0]) != "exec -T core /app ssl status --json" {
			t.Fatalf("%v: JSON runs %v", args, f.runs)
		}
		for _, want := range []string{"What this server answers on 127.0.0.1:443", "panel.example.com", "PASS", "publicly trusted", "77 days left",
			"pay.example.net", "FAIL", "no certificate is served for pay.example.net: tls: internal error"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v: output lacks %q:\n%s", args, want, out)
			}
		}
	}

	// Core does not answer: the install's own domain is looked at.
	f.runErr, f.json = errors.New("service core is not running"), ""
	var probed []string
	o.probeTLS = func(_ context.Context, address string) tlsprobe.Result {
		probed = append(probed, address)
		return tlsprobe.Result{}
	}
	out.Reset()
	if code := o.sslCmd([]string{"--dir", dir}); code != 0 || len(probed) != 1 || probed[0] != "panel.example.com" {
		t.Errorf("core down: exit %d, probed %v", code, probed)
	}

	// A refused status keeps core's exit code.
	f.failWith = exec.Command("sh", "-c", "exit 1").Run()
	if code := o.sslCmd([]string{"status", "--dir", dir}); code != 1 {
		t.Errorf("failing core: exit %d", code)
	}
	if strings.Contains(errOut.String(), "docker compose exec failed") {
		t.Errorf("core's own refusal was reported as a docker failure: %s", errOut)
	}
	errOut.Reset()
	f.failWith = errors.New("fork/exec docker: no such file")
	if code := o.sslCmd([]string{"status", "--dir", dir}); code != 1 || !strings.Contains(errOut.String(), "docker compose exec failed") {
		t.Errorf("docker missing: exit %d: %s", code, errOut)
	}
}

func TestSSLAuto(t *testing.T) {
	dir := installDir(t)
	f := &fakeSSL{}
	o, _, errOut := f.ops("")
	if code := o.sslCmd([]string{"auto", "--dir", dir}); code != 0 || len(f.streams) != 1 || coreArgs(f.streams[0]) != "exec -T core /app ssl auto" {
		t.Fatalf("auto: %d %v %s", code, f.streams, errOut)
	}
}

func TestSSLIP(t *testing.T) {
	const detected = "93.184.216.34"
	for _, tc := range []struct {
		name    string
		args    []string
		stdin   string
		detect  string // "" = detection fails
		code    int
		core    string // "" = core is not run
		message string
	}{
		{"the detected address", []string{"ip"}, "", detected, 0, "ssl ip " + detected + " --detected " + detected, ""},
		{"the same address given", []string{"ip", "--ip", detected}, "", detected, 0, "ssl ip " + detected + " --detected " + detected, ""},
		{"another one, confirmed", []string{"ip", "--ip", "8.8.4.4"}, "y\n", detected, 0, "ssl ip 8.8.4.4 --detected " + detected + " --yes", "is not the address this server reports"},
		{"another one, declined", []string{"ip", "--ip", "8.8.4.4"}, "n\n", detected, 1, "", "Cancelled. Nothing was changed."},
		{"another one, no answer", []string{"ip", "--ip", "8.8.4.4"}, "", detected, 1, "", "Cancelled"},
		{"another one with --yes", []string{"ip", "--ip", "8.8.4.4", "--yes"}, "", detected, 0, "ssl ip 8.8.4.4 --detected " + detected + " --yes", ""},
		{"a private address", []string{"ip", "--ip", "10.0.0.5", "--yes"}, "", detected, 2, "", "not a public address"},
		{"a CGNAT address", []string{"ip", "--ip", "100.64.1.1"}, "", detected, 2, "", "not a public address"},
		{"not an address", []string{"ip", "--ip", "example.com"}, "", detected, 2, "", "not an IP address"},
		{"detection fails", []string{"ip"}, "", "", 1, "", "could not be detected"},
		{"detection fails, address given", []string{"ip", "--ip", "8.8.4.4"}, "", "", 0, "ssl ip 8.8.4.4", ""},
		{"an IPv6 address is written canonically", []string{"ip", "--ip", "2606:2800:0220:0001:0248:1893:25C8:1946", "--yes"}, "", detected, 0,
			"ssl ip 2606:2800:220:1:248:1893:25c8:1946 --detected " + detected + " --yes", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := installDir(t)
			f := &fakeSSL{}
			o, out, errOut := f.ops(tc.stdin)
			o.detectIP = func() string { return tc.detect }
			if code := o.sslCmd(append(tc.args, "--dir", dir)); code != tc.code {
				t.Fatalf("exit %d, want %d\n%s\n%s", code, tc.code, out, errOut)
			}
			if tc.core == "" && len(f.streams) != 0 {
				t.Errorf("core was run: %v", f.streams)
			}
			if tc.core != "" && (len(f.streams) != 1 || coreArgs(f.streams[0]) != "exec -T core /app "+tc.core) {
				t.Errorf("core runs %v, want %q", f.streams, tc.core)
			}
			if !strings.Contains(out.String()+errOut.String(), tc.message) {
				t.Errorf("no %q in\n%s\n%s", tc.message, out, errOut)
			}
		})
	}
}

// pemPair is a fresh certificate and key, made for the test.
func pemPair(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "panel.example.com"}, DNSNames: []string{"panel.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
}

func TestSSLManual(t *testing.T) {
	certPEM, keyPEM := pemPair(t)
	files := t.TempDir()
	certFile, keyFile := filepath.Join(files, "fullchain.pem"), filepath.Join(files, "privkey.pem")
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil { //nolint:gosec // a certificate
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	// Let's Encrypt's live/ files are links.
	linkedKey := filepath.Join(files, "live-privkey.pem")
	mustSymlink(t, keyFile, linkedKey)

	dir := installDir(t)
	f := &fakeSSL{}
	o, out, errOut := f.ops("")
	for _, key := range []string{keyFile, linkedKey} {
		f.pipes = nil
		if code := o.sslCmd([]string{"manual", "--cert", certFile, "--key", key, "--dir", dir}); code != 0 {
			t.Fatalf("manual: %d %s", code, errOut)
		}
		if len(f.pipes) != 1 || coreArgs(f.pipes[0].args) != "exec -T core /app ssl manual" {
			t.Fatalf("core runs %v", f.pipes)
		}
		// The key goes in on stdin, after the chain, and nowhere else.
		got := f.pipes[0].stdin
		if !strings.HasPrefix(got, strings.TrimSpace(string(certPEM))+"\n") || !strings.HasSuffix(got, strings.TrimSpace(string(keyPEM))+"\n") {
			t.Errorf("stdin is not the chain and then the key:\n%s", got)
		}
		if _, _, err := tlsconf.SplitBundle([]byte(got)); err != nil {
			t.Errorf("core could not split the bundle: %v", err)
		}
		for _, a := range f.pipes[0].args {
			if strings.Contains(a, "PRIVATE") || strings.Contains(a, "BEGIN") {
				t.Errorf("key material on the command line: %q", a)
			}
		}
	}
	if len(f.streams) != 0 {
		t.Errorf("manual used the streamed run (stdin is the terminal's there): %v", f.streams)
	}

	big := filepath.Join(files, "big.pem")
	if err := os.WriteFile(big, bytes.Repeat([]byte("A"), tlsconf.MaxPEM+1), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(files, "empty.pem")
	if err := os.WriteFile(empty, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		args       []string
		code       int
		wantInErr  string
		leakedText string
	}{
		{"no flags", []string{"manual"}, 2, "needs --cert FILE and --key FILE", ""},
		{"no key", []string{"manual", "--cert", certFile}, 2, "needs --cert FILE and --key FILE", ""},
		{"missing file", []string{"manual", "--cert", filepath.Join(files, "nope.pem"), "--key", keyFile}, 1, "the certificate file", ""},
		{"too big", []string{"manual", "--cert", certFile, "--key", big}, 1, "larger than 64 KiB", "AAAAAAAAAA"},
		{"empty", []string{"manual", "--cert", empty, "--key", keyFile}, 1, "is empty", ""},
		{"a directory", []string{"manual", "--cert", files, "--key", keyFile}, 1, "is not a regular file", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSSL{}
			o, out, errOut := f.ops("")
			if code := o.sslCmd(append(tc.args, "--dir", dir)); code != tc.code || !strings.Contains(errOut.String(), tc.wantInErr) {
				t.Fatalf("exit %d, want %d with %q:\n%s", code, tc.code, tc.wantInErr, errOut)
			}
			if len(f.pipes)+len(f.streams) != 0 {
				t.Errorf("core was run: %v %v", f.pipes, f.streams)
			}
			if tc.leakedText != "" && strings.Contains(out.String()+errOut.String(), tc.leakedText) {
				t.Errorf("a file's text is in the output:\n%s%s", out, errOut)
			}
		})
	}
	_ = out
}

func TestSSLUsage(t *testing.T) {
	dir := installDir(t)
	f := &fakeSSL{}
	o, out, errOut := f.ops("")
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"renew", "--dir", dir}, 2, "unknown command \"renew\""},
		{[]string{"status", "extra", "--dir", dir}, 2, "unexpected argument"},
		{[]string{"auto", "--ip", "1.2.3.4", "--dir", dir}, 2, ""},
		{[]string{"status", "--dir", t.TempDir()}, 2, "no install found"},
	} {
		errOut.Reset()
		if code := o.sslCmd(tc.args); code != tc.code || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%v: exit %d, want %d with %q: %s", tc.args, code, tc.code, tc.want, errOut)
		}
	}
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {"ip", "-h"}} {
		out.Reset()
		if code := o.sslCmd(args); code != 0 || !strings.Contains(out.String(), "bobres ssl manual --cert FILE --key FILE") {
			t.Errorf("%v: %d %s", args, code, out)
		}
	}
	if len(f.streams)+len(f.pipes)+len(f.runs) != 0 {
		t.Errorf("docker was run: %v %v %v", f.streams, f.pipes, f.runs)
	}
	// And through run(): the command is wired.
	var rout, rerr bytes.Buffer
	if code := run([]string{"ssl", "help"}, &rout, &rerr); code != 0 || !strings.Contains(rout.String(), "bobres ssl [status]") {
		t.Errorf("run ssl help: %d %s %s", code, rout.String(), rerr.String())
	}
}

// What a probe found, as doctor and `ssl status` judge it.
func TestTLSVerdict(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	yes, no := true, false
	cert := func(days int, lifetimeDays int) tlsconf.CertInfo {
		after := now.Add(time.Duration(days) * 24 * time.Hour)
		return tlsconf.CertInfo{Subject: "CN=panel.example.com", Issuer: "CN=E7", NotBefore: after.Add(-time.Duration(lifetimeDays) * 24 * time.Hour), NotAfter: after}
	}
	for _, tc := range []struct {
		name string
		r    tlsprobe.Result
		want Status
		text string
	}{
		{"fresh and trusted", tlsprobe.Result{Served: true, NameOK: true, Trusted: &yes, Info: cert(60, 90)}, Pass, "publicly trusted"},
		{"trust not checked", tlsprobe.Result{Served: true, NameOK: true, Info: cert(60, 90)}, Pass, "trust not checked"},
		{"a 90 day certificate with 14 days left is the warning line", tlsprobe.Result{Served: true, NameOK: true, Trusted: &yes, Info: cert(13, 90)}, Warn, "about to expire"},
		{"14 days left is fine", tlsprobe.Result{Served: true, NameOK: true, Trusted: &yes, Info: cert(15, 90)}, Pass, ""},
		{"a 6 day certificate with 1 day left", tlsprobe.Result{Served: true, NameOK: true, Trusted: &yes, Info: cert(1, 6)}, Warn, "about to expire"},
		{"a 6 day certificate with 3 days left", tlsprobe.Result{Served: true, NameOK: true, Trusted: &yes, Info: cert(3, 6)}, Pass, ""},
		{"self-signed", tlsprobe.Result{Served: true, NameOK: true, Trusted: &no, Info: cert(60, 90), Error: "certificate signed by unknown authority"}, Warn, "not publicly trusted"},
		{"another name", tlsprobe.Result{Served: true, NameOK: false, Trusted: &no, Info: cert(60, 90)}, Fail, "does not cover panel.example.com"},
		{"expired", tlsprobe.Result{Served: true, NameOK: true, Trusted: &no, Info: cert(-2, 90)}, Fail, "has expired"},
		{"no handshake", tlsprobe.Result{Error: "connection refused"}, Fail, "no certificate is served for panel.example.com: connection refused"},
		{"no handshake, no reason", tlsprobe.Result{}, Fail, "no answer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, text := tlsVerdict(tc.r, "panel.example.com", now)
			if st != tc.want || !strings.Contains(text, tc.text) {
				t.Errorf("= %s %q, want %s with %q", st, text, tc.want, tc.text)
			}
		})
	}
}

// Doctor's HTTPS line: one per served address, Skip while the gateway is
// stopped, Fail while it keeps restarting.
func TestDoctorTLSLines(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	yes, no := true, false
	good := tlsprobe.Result{Served: true, NameOK: true, Trusted: &yes, Info: tlsconf.CertInfo{
		Issuer: "CN=E7", NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(60 * 24 * time.Hour)}}
	internal := good
	internal.Trusted = &no
	probe := func(byAddress map[string]tlsprobe.Result) func(context.Context, string) tlsprobe.Result {
		return func(_ context.Context, a string) tlsprobe.Result { return byAddress[a] }
	}
	addrs := []served{{"panel.example.com", "domain"}, {"93.184.216.34", "ip"}, {"pay.example.net", "zarinpal"}}
	for _, tc := range []struct {
		name  string
		d     tlsDoctor
		want  []string // "name status", one per line
		match string
	}{
		{"no container", tlsDoctor{state: ""}, []string{"TLS SKIP"}, "not running"},
		{"stopped", tlsDoctor{state: "exited", addrs: addrs}, []string{"TLS SKIP"}, "not running"},
		{"a crash loop", tlsDoctor{state: "restarting", addrs: addrs}, []string{"TLS FAIL"}, "keeps restarting"},
		{"no domain known", tlsDoctor{state: "running"}, []string{"TLS SKIP"}, "no domain known"},
		{"all good", tlsDoctor{state: "running", addrs: addrs[:1], probe: probe(map[string]tlsprobe.Result{"panel.example.com": good})}, []string{"TLS PASS"}, "panel.example.com: by CN=E7"},
		{"three addresses", tlsDoctor{state: "running", addrs: addrs, probe: probe(map[string]tlsprobe.Result{
			"panel.example.com": good, "93.184.216.34": internal, "pay.example.net": {Error: "tls: internal error"}})},
			[]string{"TLS PASS", "TLS ip WARN", "TLS zarinpal FAIL"}, "pay.example.net: no certificate is served"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.d.now = now
			rs := tc.d.results(context.Background())
			var got []string
			var all strings.Builder
			for _, r := range rs {
				got = append(got, r.Name+" "+string(r.Status))
				all.WriteString(r.Detail + "\n")
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") || !strings.Contains(all.String(), tc.match) {
				t.Errorf("got %v\n%s\nwant %v with %q", got, all.String(), tc.want, tc.match)
			}
		})
	}
}
