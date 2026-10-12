package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
)

// DescribeTransport names a transport failure without any address in it: the
// underlying error names the proxy's address (part of a secret URL), and the
// description ends up in logs, gateway events and the dashboard.
func DescribeTransport(err error) string {
	var (
		dns  *net.DNSError
		op   *net.OpError
		ne   net.Error
		cert *tls.CertificateVerificationError
		rec  tls.RecordHeaderError
	)
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timed out"
	case errors.As(err, &dns):
		return "DNS lookup failed"
	case errors.As(err, &op) && op.Op == "proxyconnect":
		if errors.Is(err, syscall.ECONNREFUSED) {
			return "the proxy refused the connection"
		}
		return "cannot connect to the proxy"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.As(err, &op):
		if errors.Is(err, syscall.ECONNREFUSED) {
			return "connection refused"
		}
		return "cannot connect"
	case errors.As(err, &cert), errors.As(err, &rec):
		return "TLS handshake failed"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed"
	}
	// A proxy that refuses the CONNECT leaves only its reason phrase.
	for code := 400; code < 600; code++ {
		if err.Error() != http.StatusText(code) {
			continue
		}
		if code == http.StatusProxyAuthRequired {
			return "the proxy wants other credentials (HTTP 407)"
		}
		return fmt.Sprintf("the proxy refused the connection (HTTP %d)", code)
	}
	return "connection failed"
}
