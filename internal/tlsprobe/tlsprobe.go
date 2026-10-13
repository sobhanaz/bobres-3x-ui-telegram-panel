// Package tlsprobe reads the certificate an HTTPS server serves for one
// address, and checks separately whether the public would trust it. Core's
// HTTPS status and its watchdog dial Caddy over the edge network
// (caddy:443); the bobres CLI can dial the host's 127.0.0.1:443 the same
// way, to see what the outside sees.
package tlsprobe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

// DefaultTimeout bounds a probe: the dial and the handshake.
const DefaultTimeout = 5 * time.Second

// Result is what one address serves.
type Result struct {
	// Reachable: the dial succeeded, so something listens at the address
	// (whatever the handshake then did). False means the server is stopped,
	// restarting or unreachable, which is not about any certificate.
	Reachable bool
	// Served: a TLS handshake succeeded and a certificate came back.
	Served bool
	Info   tlsconf.CertInfo
	// NameOK: the certificate covers the address.
	NameOK bool
	// Trusted: the chain the server sent verifies for the address against
	// the public roots (nil when no roots could be loaded, or nothing was
	// served).
	Trusted *bool
	// Error is why nothing was served, or why the certificate does not
	// verify ("" when it does).
	Error string
}

// Options tune a probe.
type Options struct {
	// Timeout bounds the dial and the handshake (DefaultTimeout when 0).
	Timeout time.Duration
	// Roots are the public roots (nil: the system's).
	Roots *x509.CertPool
	// Now is the time the chain is verified at (zero: now).
	Now time.Time
	// Dial connects (nil: a plain TCP dialer).
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Probe dials addr (host:port) and does a TLS handshake for address, a host
// name (sent as SNI) or an IP address (no SNI: clients send none to an IP
// address, and Caddy answers with its default_sni). It reads the leaf
// without trusting it, then verifies the chain on its own.
func Probe(ctx context.Context, addr, address string, o Options) Result {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	dial := o.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	raw, err := dial(ctx, "tcp", addr)
	if err != nil {
		return Result{Error: clean(err)}
	}
	defer func() { _ = raw.Close() }()
	sni := address
	if tlsconf.IsIP(address) {
		sni = ""
	}
	conn := tls.Client(raw, &tls.Config{ServerName: sni, MinVersion: tls.VersionTLS12,
		InsecureSkipVerify: true}) //nolint:gosec // the chain is verified below, on its own
	if err := conn.HandshakeContext(ctx); err != nil {
		return Result{Reachable: true, Error: clean(err)}
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return Result{Reachable: true, Error: "the server sent no certificate"}
	}
	leaf := certs[0]
	r := Result{Reachable: true, Served: true, Info: tlsconf.Info(leaf), NameOK: leaf.VerifyHostname(address) == nil}
	roots := o.Roots
	if roots == nil {
		if roots, err = x509.SystemCertPool(); err != nil {
			if !r.NameOK {
				r.Error = "the certificate does not cover " + address
			}
			return r
		}
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: address, Roots: roots, Intermediates: inter, CurrentTime: o.Now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	ok := err == nil
	r.Trusted = &ok
	if err != nil {
		r.Error = clean(err)
	}
	return r
}

// clean is an error on one line, without Go's prefixes.
func clean(err error) string {
	s := strings.Join(strings.Fields(err.Error()), " ")
	s = strings.TrimPrefix(s, "x509: ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
