package tlsconf

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"testing"
	"time"
)

// The tests use the documentation ranges (203.0.113.0/24, 2001:db8::/32)
// as public addresses, as every example does.
func TestMain(m *testing.M) {
	allowTestNets = true
	os.Exit(m.Run())
}

// Certificates and keys are made here, at test time: none is committed.

// testCA is a certificate authority for tests.
type testCA struct {
	cert *x509.Certificate
	key  crypto.Signer
}

// testPair is an issued certificate with its key, each as PEM alone.
type testPair struct {
	cert    *x509.Certificate
	key     crypto.Signer
	certPEM []byte
	keyPEM  []byte
}

var serial int64

func ecKey(t *testing.T) crypto.Signer {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// issue signs a certificate for key (a new one when nil) with parent (self-
// signed when nil).
func issue(t *testing.T, tmpl *x509.Certificate, key crypto.Signer, parent *testCA) (*x509.Certificate, crypto.Signer) {
	t.Helper()
	if key == nil {
		key = ecKey(t)
	}
	serial++
	tmpl.SerialNumber = big.NewInt(serial)
	signer, issuer := key, tmpl
	if parent != nil {
		signer, issuer = parent.key, parent.cert
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer, key.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c, key
}

func newCA(t *testing.T, cn string, now time.Time) *testCA {
	t.Helper()
	c, k := issue(t, &x509.Certificate{Subject: pkix.Name{CommonName: cn}, NotBefore: now.Add(-24 * time.Hour),
		NotAfter: now.Add(10 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}, nil, nil)
	return &testCA{cert: c, key: k}
}

// sub is an intermediate CA under ca.
func (ca *testCA) sub(t *testing.T, cn string, now time.Time) *testCA {
	t.Helper()
	c, k := issue(t, &x509.Certificate{Subject: pkix.Name{CommonName: cn}, NotBefore: now.Add(-24 * time.Hour),
		NotAfter: now.Add(5 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}, nil, ca)
	return &testCA{cert: c, key: k}
}

// leaf is a server certificate from ca, valid from an hour ago for 90 days.
func (ca *testCA) leaf(t *testing.T, now time.Time, dns, ips []string) testPair {
	t.Helper()
	return ca.leafFor(t, dns, ips, now.Add(-time.Hour), now.Add(90*24*time.Hour), nil)
}

func (ca *testCA) leafFor(t *testing.T, dns, ips []string, from, until time.Time, key crypto.Signer) testPair {
	t.Helper()
	tmpl := serverTemplate(dns, ips, from, until)
	c, k := issue(t, tmpl, key, ca)
	return pairOf(t, c, k)
}

func serverTemplate(dns, ips []string, from, until time.Time) *x509.Certificate {
	tmpl := &x509.Certificate{NotBefore: from, NotAfter: until, DNSNames: dns,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if len(dns) > 0 {
		tmpl.Subject = pkix.Name{CommonName: dns[0]}
	}
	for _, ip := range ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
	}
	return tmpl
}

// selfSigned is a server certificate that signs itself.
func selfSigned(t *testing.T, now time.Time, dns []string) testPair {
	t.Helper()
	c, k := issue(t, serverTemplate(dns, nil, now.Add(-time.Hour), now.Add(365*24*time.Hour)), nil, nil)
	return pairOf(t, c, k)
}

func pairOf(t *testing.T, c *x509.Certificate, k crypto.Signer) testPair {
	t.Helper()
	return testPair{cert: c, key: k, certPEM: certsPEM(c), keyPEM: pkcs8(t, k)}
}

// certsPEM is a chain of certificates as PEM, in the order given.
func certsPEM(certs ...*x509.Certificate) []byte {
	var out []byte
	for _, c := range certs {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return out
}

func pkcs8(t *testing.T, k crypto.Signer) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func roots(cas ...*testCA) *x509.CertPool {
	p := x509.NewCertPool()
	for _, ca := range cas {
		p.AddCert(ca.cert)
	}
	return p
}

func join(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
