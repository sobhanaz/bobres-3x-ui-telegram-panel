package tlsprobe

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type ca struct {
	cert *x509.Certificate
	key  crypto.Signer
}

var serial int64

func issue(t *testing.T, tmpl *x509.Certificate, parent *ca) (*x509.Certificate, crypto.Signer) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial++
	tmpl.SerialNumber = big.NewInt(serial)
	signer, issuer := crypto.Signer(k), tmpl
	if parent != nil {
		signer, issuer = parent.key, parent.cert
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer, k.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c, k
}

func newCA(t *testing.T, now time.Time) *ca {
	t.Helper()
	c, k := issue(t, &x509.Certificate{Subject: pkix.Name{CommonName: "Probe Root"}, NotBefore: now.Add(-time.Hour),
		NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}, nil)
	return &ca{cert: c, key: k}
}

func (c *ca) serve(t *testing.T, now time.Time, dns []string, ips []string, until time.Time) tls.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{NotBefore: now.Add(-time.Hour), NotAfter: until, DNSNames: dns,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, ip := range ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
	}
	leaf, k := issue(t, tmpl, c)
	return tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: k, Leaf: leaf}
}

// server answers each handshake with the certificate for its SNI ("" for
// none) and records the SNI it got.
type server struct {
	addr  string
	mu    sync.Mutex
	snis  []string
	certs map[string]tls.Certificate
}

func newServer(t *testing.T, certs map[string]tls.Certificate) *server {
	t.Helper()
	s := &server{certs: certs}
	lis, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{MinVersion: tls.VersionTLS12,
		GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
			s.mu.Lock()
			s.snis = append(s.snis, h.ServerName)
			s.mu.Unlock()
			if c, ok := s.certs[h.ServerName]; ok {
				return &c, nil
			}
			return nil, errors.New("no certificate")
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lis.Close() })
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = c.(*tls.Conn).Handshake()
				_ = c.Close()
			}()
		}
	}()
	s.addr = lis.Addr().String()
	return s
}

// Each address is dialed at the edge address with its SNI (none for an IP
// address); the leaf is read, then verified on its own.
func TestProbe(t *testing.T) {
	now := time.Now()
	root := newCA(t, now)
	other := newCA(t, now)
	srv := newServer(t, map[string]tls.Certificate{
		"panel.example.com": root.serve(t, now, []string{"panel.example.com"}, nil, now.Add(12*time.Hour)),
		"":                  root.serve(t, now, nil, []string{"203.0.113.10"}, now.Add(12*time.Hour)),
		"pay.example.net":   root.serve(t, now, []string{"shop.example.net"}, nil, now.Add(12*time.Hour)),
	})
	pool := x509.NewCertPool()
	pool.AddCert(root.cert)
	opt := Options{Roots: pool}

	r := Probe(context.Background(), srv.addr, "panel.example.com", opt)
	if !r.Reachable || !r.Served || !r.NameOK || r.Trusted == nil || !*r.Trusted || r.Error != "" || strings.Join(r.Info.DNSNames, ",") != "panel.example.com" {
		t.Fatalf("a host name: %+v", r)
	}
	r = Probe(context.Background(), srv.addr, "203.0.113.10", opt)
	if !r.Served || !r.NameOK || r.Trusted == nil || !*r.Trusted || strings.Join(r.Info.IPs, ",") != "203.0.113.10" {
		t.Fatalf("an IP address: %+v", r)
	}
	srv.mu.Lock()
	snis := strings.Join(srv.snis, "|")
	srv.mu.Unlock()
	if snis != "panel.example.com|" {
		t.Fatalf("SNIs sent: %q", snis)
	}
	r = Probe(context.Background(), srv.addr, "pay.example.net", opt)
	if !r.Served || r.NameOK || r.Trusted == nil || *r.Trusted || r.Error == "" {
		t.Fatalf("another name: %+v", r)
	}
	r = Probe(context.Background(), srv.addr, "panel.example.com", Options{Roots: func() *x509.CertPool {
		p := x509.NewCertPool()
		p.AddCert(other.cert)
		return p
	}()})
	if !r.Served || !r.NameOK || r.Trusted == nil || *r.Trusted || !strings.Contains(r.Error, "unknown authority") {
		t.Fatalf("another root: %+v", r)
	}
	r = Probe(context.Background(), srv.addr, "panel.example.com", Options{Roots: pool, Now: now.Add(48 * time.Hour)})
	if !r.Served || r.Trusted == nil || *r.Trusted || !strings.Contains(r.Error, "expired") {
		t.Fatalf("expired: %+v", r)
	}
	r = Probe(context.Background(), srv.addr, "unknown.example.com", opt)
	if !r.Reachable || r.Served || r.Error == "" || r.Trusted != nil {
		t.Fatalf("no certificate for the name (the server is there, so it is reachable): %+v", r)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := lis.Addr().String()
	_ = lis.Close()
	if r := Probe(context.Background(), closed, "panel.example.com", Options{Roots: pool, Timeout: time.Second}); r.Reachable || r.Served || r.Error == "" {
		t.Fatalf("nothing listening: %+v", r)
	}
	// The dialer is the caller's: here it reaches the server whatever the
	// address says.
	dialed := ""
	r = Probe(context.Background(), "caddy:443", "panel.example.com", Options{Roots: pool,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialed = addr
			return (&net.Dialer{}).DialContext(ctx, network, srv.addr)
		}})
	if !r.Served || dialed != "caddy:443" {
		t.Fatalf("through the dialer: %+v %q", r, dialed)
	}
}
