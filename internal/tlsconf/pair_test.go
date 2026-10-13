package tlsconf

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"
)

// pairWorld is a public root, an intermediate under it and a time.
type pairWorld struct {
	now   time.Time
	root  *testCA
	inter *testCA
}

func newPairWorld(t *testing.T) *pairWorld {
	t.Helper()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	root := newCA(t, "Test Public Root", now)
	return &pairWorld{now: now, root: root, inter: root.sub(t, "Test Issuing CA", now)}
}

// opts checks for name at the world's time, against its root.
func (w *pairWorld) opts(name string) PairOptions {
	return PairOptions{Name: name, Now: w.now, Roots: roots(w.root)}
}

func codeOf(err error) string {
	var p *Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

func warningCodes(p *Pair) string {
	var out []string
	for _, w := range p.Warnings {
		out = append(out, w.Code)
	}
	return strings.Join(out, ",")
}

// A leaf with its intermediate, for the name, trusted: accepted with no
// warning, normalized (the blocks alone, leaf first), described.
func TestCheckPairGood(t *testing.T) {
	w := newPairWorld(t)
	leaf := w.inter.leaf(t, w.now, []string{"panel.example.com", "www.example.com"}, []string{"203.0.113.10"})
	chain := join([]byte("leaf:\n"), leaf.certPEM, []byte("\nintermediate (from the CA's mail)\n"), certsPEM(w.inter.cert), []byte("\n"))
	key := join([]byte("Bag Attributes\n"), leaf.keyPEM)
	p, err := CheckPair(chain, key, w.opts("panel.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Trusted == nil || !*p.Trusted || p.SelfSigned || len(p.Warnings) != 0 {
		t.Fatalf("trusted %v self-signed %v warnings %v", p.Trusted, p.SelfSigned, p.Warnings)
	}
	if !bytes.Equal(p.CertPEM, certsPEM(leaf.cert, w.inter.cert)) || !bytes.Equal(p.KeyPEM, leaf.keyPEM) {
		t.Fatalf("not normalized:\n%s\n%s", p.CertPEM, p.KeyPEM)
	}
	i := p.Info
	if i.Subject != "CN=panel.example.com" || i.Issuer != "CN=Test Issuing CA" || strings.Join(i.DNSNames, ",") != "panel.example.com,www.example.com" ||
		strings.Join(i.IPs, ",") != "203.0.113.10" || !i.NotAfter.Equal(leaf.cert.NotAfter) || !i.NotBefore.Equal(leaf.cert.NotBefore) {
		t.Fatalf("info: %+v", i)
	}
	for _, name := range []string{"www.example.com", "203.0.113.10"} {
		if _, err := CheckPair(chain, key, w.opts(name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The names a leaf covers: its DNS names, a wildcard for one label, its IP
// addresses for an IP name.
func TestCheckPairNames(t *testing.T) {
	w := newPairWorld(t)
	wild := w.inter.leaf(t, w.now, []string{"*.example.com"}, nil)
	byIP := w.inter.leaf(t, w.now, nil, []string{"203.0.113.10", "2001:db8::10"})
	plain := w.inter.leaf(t, w.now, []string{"panel.example.com"}, nil)
	for _, tc := range []struct {
		name string
		pair testPair
		host string
		ok   bool
	}{
		{"a wildcard covers one label", wild, "panel.example.com", true},
		{"a wildcard does not cover the apex", wild, "example.com", false},
		{"a wildcard does not cover two labels", wild, "a.panel.example.com", false},
		{"an IP SAN", byIP, "203.0.113.10", true},
		{"an IPv6 SAN", byIP, "2001:db8::10", true},
		{"another IP", byIP, "203.0.113.11", false},
		{"a host name on an IP-only leaf", byIP, "panel.example.com", false},
		{"an IP on a name-only leaf", plain, "203.0.113.10", false},
		{"another name", plain, "shop.example.com", false},
	} {
		_, err := CheckPair(join(tc.pair.certPEM, certsPEM(w.inter.cert)), tc.pair.keyPEM, w.opts(tc.host))
		if (err == nil) != tc.ok || (!tc.ok && codeOf(err) != CodeCertNameMismatch) {
			t.Errorf("%s: %v", tc.name, err)
		}
		if err != nil && !strings.Contains(err.Error(), tc.host) {
			t.Errorf("%s: the message does not name %s: %v", tc.name, tc.host, err)
		}
	}
}

// What is refused, each with its code; no message quotes the input.
func TestCheckPairRefuses(t *testing.T) {
	w := newPairWorld(t)
	good := w.inter.leaf(t, w.now, []string{"panel.example.com"}, nil)
	other := w.inter.leaf(t, w.now, []string{"panel.example.com"}, nil)
	expired := w.inter.leafFor(t, []string{"panel.example.com"}, nil, w.now.Add(-100*24*time.Hour), w.now.Add(-time.Hour), nil)
	early := w.inter.leafFor(t, []string{"panel.example.com"}, nil, w.now.Add(time.Hour), w.now.Add(90*24*time.Hour), nil)
	chain := join(good.certPEM, certsPEM(w.inter.cert))
	encrypted := pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: randomBytes(t, 200)})
	block, _ := pem.Decode(good.keyPEM)
	legacy := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED",
		"DEK-Info": "AES-256-CBC,00112233445566778899AABBCCDDEEFF"}, Bytes: block.Bytes})
	tooMany := good.certPEM
	for range maxChain {
		tooMany = join(tooMany, certsPEM(w.inter.cert))
	}
	for _, tc := range []struct {
		name      string
		cert, key []byte
		host      string
		code      string
	}{
		{"no certificate", nil, good.keyPEM, "panel.example.com", CodeCertMissing},
		{"no key", chain, []byte(" \n"), "panel.example.com", CodeKeyMissing},
		{"no PEM in the certificate", []byte("MIIB...not pem"), good.keyPEM, "panel.example.com", CodeCertNotPEM},
		{"no PEM in the key", chain, []byte("just text"), "panel.example.com", CodeKeyNotPEM},
		{"another block in the certificate", join(chain, pem.EncodeToMemory(&pem.Block{Type: "PKCS7", Bytes: []byte{1}})), good.keyPEM, "panel.example.com", CodeCertNotPEM},
		{"the key in the certificate field", join(chain, good.keyPEM), good.keyPEM, "panel.example.com", CodeCertHasKey},
		{"a certificate that cannot be read", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("nope")}), good.keyPEM, "panel.example.com", CodeCertParse},
		{"a key that cannot be read", chain, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("nope")}), "panel.example.com", CodeKeyParse},
		{"a public key in the key field", chain, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("x")}), "panel.example.com", CodeKeyNotPEM},
		{"two keys", chain, join(good.keyPEM, other.keyPEM), "panel.example.com", CodeKeyCount},
		{"an encrypted PKCS#8 key", chain, encrypted, "panel.example.com", CodeKeyEncrypted},
		{"an encrypted legacy key", chain, legacy, "panel.example.com", CodeKeyEncrypted},
		{"the key of another certificate", chain, other.keyPEM, "panel.example.com", CodeKeyMismatch},
		{"the intermediate first", join(certsPEM(w.inter.cert), good.certPEM), good.keyPEM, "panel.example.com", CodeChainOrder},
		{"expired", join(expired.certPEM, certsPEM(w.inter.cert)), expired.keyPEM, "panel.example.com", CodeCertExpired},
		{"not valid yet", join(early.certPEM, certsPEM(w.inter.cert)), early.keyPEM, "panel.example.com", CodeCertNotYetValid},
		{"another name", chain, good.keyPEM, "shop.example.com", CodeCertNameMismatch},
		{"no name to check", chain, good.keyPEM, "", CodeNoDomain},
		{"a huge certificate", append(bytes.Repeat([]byte("a"), MaxPEM), chain...), good.keyPEM, "panel.example.com", CodeCertTooLarge},
		{"a huge key", chain, append(bytes.Repeat([]byte("a"), MaxPEM), good.keyPEM...), "panel.example.com", CodeKeyTooLarge},
		{"a long chain", tooMany, good.keyPEM, "panel.example.com", CodeChainTooLong},
	} {
		_, err := CheckPair(tc.cert, tc.key, w.opts(tc.host))
		if codeOf(err) != tc.code {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
			continue
		}
		for _, secret := range []string{bodyLine(good.keyPEM), bodyLine(good.certPEM), bodyLine(other.keyPEM)} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: the message quotes the input: %s", tc.name, err)
			}
		}
	}
}

// Every key format Caddy loads: PKCS#1 and PKCS#8 RSA, SEC1 and PKCS#8
// ECDSA (with or without its EC PARAMETERS block), PKCS#8 Ed25519, whatever
// the PEM label says: tls.X509KeyPair, which Caddy loads the pair with,
// tries PKCS#1, PKCS#8 and SEC1 in turn, so a PKCS#8 key labelled
// "RSA PRIVATE KEY" loads there and must pass here too.
func TestCheckPairKeyFormats(t *testing.T) {
	w := newPairWorld(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sec1, err := x509.MarshalECPrivateKey(ecKey)
	if err != nil {
		t.Fatal(err)
	}
	pk8 := func(k crypto.Signer) []byte {
		der, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		return der
	}
	block := func(label string, der []byte) []byte { return pem.EncodeToMemory(&pem.Block{Type: label, Bytes: der}) }
	ecParams := pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{0x06, 0x05, 0x2b, 0x81, 0x04, 0x00, 0x22}})
	for _, tc := range []struct {
		name string
		key  crypto.Signer
		pem  []byte
	}{
		{"PKCS#1 RSA", rsaKey, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})},
		{"PKCS#8 RSA", rsaKey, pkcs8(t, rsaKey)},
		{"SEC1 ECDSA", ecKey, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1})},
		{"SEC1 ECDSA with its parameters", ecKey, join(ecParams, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1}))},
		{"PKCS#8 ECDSA", ecKey, pkcs8(t, ecKey)},
		{"PKCS#8 Ed25519", edKey, pkcs8(t, edKey)},
		{"PKCS#8 RSA labelled RSA PRIVATE KEY", rsaKey, block("RSA PRIVATE KEY", pk8(rsaKey))},
		{"PKCS#8 ECDSA labelled EC PRIVATE KEY", ecKey, block("EC PRIVATE KEY", pk8(ecKey))},
		{"PKCS#8 Ed25519 labelled RSA PRIVATE KEY", edKey, block("RSA PRIVATE KEY", pk8(edKey))},
		{"PKCS#1 RSA labelled PRIVATE KEY", rsaKey, block("PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey))},
		{"SEC1 ECDSA labelled PRIVATE KEY", ecKey, block("PRIVATE KEY", sec1)},
		{"SEC1 ECDSA labelled RSA PRIVATE KEY", ecKey, block("RSA PRIVATE KEY", sec1)},
		{"PKCS#1 RSA labelled EC PRIVATE KEY", rsaKey, block("EC PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey))},
	} {
		leaf := w.inter.leafFor(t, []string{"panel.example.com"}, nil, w.now.Add(-time.Hour), w.now.Add(90*24*time.Hour), tc.key)
		p, err := CheckPair(join(leaf.certPEM, certsPEM(w.inter.cert)), tc.pem, w.opts("panel.example.com"))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if bytes.Contains(p.KeyPEM, []byte("EC PARAMETERS")) || bytes.Count(p.KeyPEM, []byte("-----BEGIN")) != 1 {
			t.Errorf("%s: the key is not alone:\n%s", tc.name, p.KeyPEM)
		}
		// What passes here is what Caddy loads.
		if _, err := tls.X509KeyPair(p.CertPEM, p.KeyPEM); err != nil {
			t.Errorf("%s: passes here but does not load in Go's TLS: %v", tc.name, err)
		}
	}
}

// A label Go's tls.X509KeyPair does not take for a key is not a key here
// either (it would pass and then not load in Caddy).
func TestCheckPairKeyLabels(t *testing.T) {
	w := newPairWorld(t)
	good := w.inter.leaf(t, w.now, []string{"panel.example.com"}, nil)
	chain := join(good.certPEM, certsPEM(w.inter.cert))
	der, _ := pem.Decode(good.keyPEM)
	for label, ok := range map[string]bool{"PRIVATE KEY": true, "RSA PRIVATE KEY": true, "EC PRIVATE KEY": true, "OPENSSH PRIVATE KEY": true,
		"FOOPRIVATE KEY": false, "PRIVATE KEYS": false, "KEY": false} {
		_, err := CheckPair(chain, pem.EncodeToMemory(&pem.Block{Type: label, Bytes: der.Bytes}), w.opts("panel.example.com"))
		if ok != (err == nil) {
			t.Errorf("label %q: %v", label, err)
		}
	}
}

// Accepted with warnings: ending soon, self-signed, no intermediates, a
// chain that leads nowhere public, covering the Zarinpal host too.
func TestCheckPairWarnings(t *testing.T) {
	w := newPairWorld(t)
	soon := w.inter.leafFor(t, []string{"panel.example.com"}, nil, w.now.Add(-80*24*time.Hour), w.now.Add(10*24*time.Hour), nil)
	self := selfSigned(t, w.now, []string{"panel.example.com"})
	direct := w.inter.leaf(t, w.now, []string{"panel.example.com"}, nil)
	private := newCA(t, "Private Root", w.now)
	privInter := private.sub(t, "Private Issuing", w.now)
	priv := privInter.leaf(t, w.now, []string{"panel.example.com"}, nil)
	both := w.inter.leaf(t, w.now, []string{"panel.example.com", "pay.example.net"}, nil)
	for _, tc := range []struct {
		name    string
		cert    []byte
		key     []byte
		zp      string
		want    string
		trusted string
	}{
		{"ending in 10 days", join(soon.certPEM, certsPEM(w.inter.cert)), soon.keyPEM, "", WarnExpiresSoon, "true"},
		{"self-signed", self.certPEM, self.keyPEM, "", WarnSelfSigned, "false"},
		{"no intermediate", direct.certPEM, direct.keyPEM, "", WarnChainIncomplete, "false"},
		{"a private CA", join(priv.certPEM, certsPEM(privInter.cert)), priv.keyPEM, "", WarnUntrusted, "false"},
		{"the Zarinpal host too", join(both.certPEM, certsPEM(w.inter.cert)), both.keyPEM, "pay.example.net", WarnCoversZarinpal, "true"},
		{"not the Zarinpal host", join(direct.certPEM, certsPEM(w.inter.cert)), direct.keyPEM, "pay.example.net", "", "true"},
	} {
		o := w.opts("panel.example.com")
		o.ZarinpalHost = tc.zp
		p, err := CheckPair(tc.cert, tc.key, o)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		trusted := "nil"
		if p.Trusted != nil {
			trusted = map[bool]string{true: "true", false: "false"}[*p.Trusted]
		}
		if warningCodes(p) != tc.want || trusted != tc.trusted || p.CoversZarinpal != (tc.want == WarnCoversZarinpal) ||
			p.SelfSigned != (tc.want == WarnSelfSigned) {
			t.Errorf("%s: warnings %q trusted %s, want %q %s", tc.name, warningCodes(p), trusted, tc.want, tc.trusted)
		}
		for _, wn := range p.Warnings {
			if wn.Message == "" {
				t.Errorf("%s: %s has no message", tc.name, wn.Code)
			}
		}
	}
}

// A bundle on stdin: certificates and one key, in any order.
func TestSplitBundle(t *testing.T) {
	w := newPairWorld(t)
	leaf := w.inter.leaf(t, w.now, []string{"panel.example.com"}, nil)
	inter := certsPEM(w.inter.cert)
	for _, bundle := range [][]byte{
		join(leaf.certPEM, inter, leaf.keyPEM),
		join(leaf.keyPEM, leaf.certPEM, []byte("\nsome text\n"), inter),
	} {
		cert, key, err := SplitBundle(bundle)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(cert, join(leaf.certPEM, inter)) || !bytes.Equal(key, leaf.keyPEM) {
			t.Fatalf("split:\n%s\n%s", cert, key)
		}
		if _, err := CheckPair(cert, key, w.opts("panel.example.com")); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		bundle []byte
		code   string
	}{
		"no certificate":  {leaf.keyPEM, CodeCertMissing},
		"no key":          {join(leaf.certPEM, inter), CodeKeyMissing},
		"two keys":        {join(leaf.certPEM, leaf.keyPEM, leaf.keyPEM), CodeKeyCount},
		"another block":   {join(leaf.certPEM, leaf.keyPEM, pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: []byte{1}})), CodeCertNotPEM},
		"too large":       {bytes.Repeat([]byte("a"), 2*MaxPEM+1), CodeCertTooLarge},
		"nothing in PEM":  {[]byte("hello"), CodeCertMissing},
		"empty":           {nil, CodeCertMissing},
		"only parameters": {pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{1}}), CodeCertMissing},
	} {
		if _, _, err := SplitBundle(tc.bundle); codeOf(err) != tc.code {
			t.Errorf("%s: %v, want %s", name, err, tc.code)
		}
	}
}

// Expiring: less than min(14 days, a quarter of the lifetime) left.
func TestExpiringSoon(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		lifetime time.Duration
		left     time.Duration
		want     bool
	}{
		{"a 160-hour certificate with 41 hours left", 160 * time.Hour, 41 * time.Hour, false},
		{"a 160-hour certificate with 39 hours left", 160 * time.Hour, 39 * time.Hour, true},
		{"a 90-day certificate with 15 days left", 90 * 24 * time.Hour, 15 * 24 * time.Hour, false},
		{"a 90-day certificate with 13 days left", 90 * 24 * time.Hour, 13 * 24 * time.Hour, true},
		{"a year with 20 days left", 365 * 24 * time.Hour, 20 * 24 * time.Hour, false},
		{"a year with 13 days left", 365 * 24 * time.Hour, 13 * 24 * time.Hour, true},
		{"expired", 90 * 24 * time.Hour, -time.Hour, true},
	} {
		until := now.Add(tc.left)
		if got := ExpiringSoon(until.Add(-tc.lifetime), until, now); got != tc.want {
			t.Errorf("%s: %v", tc.name, got)
		}
	}
}

// bodyLine is the first line of a PEM block's base64 body.
func bodyLine(b []byte) string {
	lines := strings.Split(string(b), "\n")
	return lines[1]
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}
