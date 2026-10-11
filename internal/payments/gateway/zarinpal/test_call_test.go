package zarinpal_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/zarinpal"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/zpfake"
)

// relay is a plain HTTP proxy for http:// targets. It counts the calls it
// relays and refuses those without the expected Proxy-Authorization.
func relay(t *testing.T, user, password string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host == "" {
			http.Error(w, "not a proxy request", http.StatusBadRequest)
			return
		}
		probe := &http.Request{Header: http.Header{"Authorization": r.Header["Proxy-Authorization"]}}
		if u, p, ok := probe.BasicAuth(); !ok || u != user || p != password {
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		n.Add(1)
		out, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body) //nolint:gosec // a test proxy relays to the test fake
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		out.Header = r.Header.Clone()
		out.Header.Del("Proxy-Authorization")
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close() //nolint:errcheck // test relay
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func withAuth(t *testing.T, raw, user, password string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

// Test asks only the read-only unVerified list: it never requests or
// verifies a payment.
func TestTestIsReadOnly(t *testing.T) {
	g, fake := newGW(t)
	ctx := context.Background()
	r, err := g.Test(ctx)
	if err != nil || r.Unverified != 0 {
		t.Fatalf("empty terminal: %+v %v", r, err)
	}
	fake.AddPayment(1_500_000, "PAID")
	fake.AddPayment(2_000_000, "PAID")
	fake.AddPayment(1_000_000, "VERIFIED")
	fake.AddPayment(1_000_000, "IN_BANK")
	if r, err := g.Test(ctx); err != nil || r.Unverified != 2 {
		t.Fatalf("two paid, not verified: %+v %v", r, err)
	}
	if fake.Calls("unverified") != 2 || fake.Calls("request") != 0 || fake.Calls("verify") != 0 || fake.Calls("inquiry") != 0 {
		t.Fatalf("calls: unverified=%d request=%d verify=%d inquiry=%d", fake.Calls("unverified"),
			fake.Calls("request"), fake.Calls("verify"), fake.Calls("inquiry"))
	}
	var _ gateway.Tester = g // the domain finds the test through this interface
}

func TestTestReportsZarinpalsRefusal(t *testing.T) {
	g, fake := newGW(t)
	fake.OnlyMerchant("11111111-2222-4333-8444-555555555555")
	_, err := g.Test(context.Background())
	var ze *zarinpal.Error
	if !errors.As(err, &ze) || ze.Code != -10 {
		t.Fatalf("unknown terminal: %v", err)
	}
	if strings.Contains(err.Error(), merchant) {
		t.Fatalf("the merchant id is in the error: %v", err)
	}
	// An unexpected success code is not a pass.
	odd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"code":-1},"errors":[]}`))
	}))
	t.Cleanup(odd.Close)
	og, _ := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: odd.URL})
	if _, err := og.Test(context.Background()); err == nil {
		t.Fatal("code -1 accepted")
	}
}

// The test goes through the proxy, with its credentials, like every call.
func TestTestGoesThroughTheProxy(t *testing.T) {
	fake := zpfake.New()
	t.Cleanup(fake.Close)
	proxy, relayed := relay(t, "relay", "s3cret-pw")
	g, err := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: fake.URL, Proxy: withAuth(t, proxy.URL, "relay", "s3cret-pw")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Test(context.Background()); err != nil || relayed.Load() != 1 || fake.Calls("unverified") != 1 {
		t.Fatalf("through the proxy: %v relayed=%d", err, relayed.Load())
	}
	// Wrong proxy credentials: the proxy's 407 is the answer, not a pass.
	bad, _ := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: fake.URL, Proxy: withAuth(t, proxy.URL, "relay", "wrong")})
	_, err = bad.Test(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 407") || strings.Contains(err.Error(), "wrong") || fake.Calls("unverified") != 1 {
		t.Fatalf("wrong proxy password: %v", err)
	}
}

// A call that never reaches Zarinpal is described by its kind only: the
// proxy's address and password are part of a secret URL, and the error text
// ends up in logs, gateway events and the dashboard.
func TestNetworkErrorsNameNoAddress(t *testing.T) {
	fake := zpfake.New()
	t.Cleanup(fake.Close)
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()
	host := strings.TrimPrefix(goneURL, "http://")

	refusing, _ := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: fake.URL, Proxy: withAuth(t, goneURL, "relay", "s3cret-pw")})
	connectRefused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired) // answers every CONNECT with 407
	}))
	t.Cleanup(connectRefused.Close)
	// An https target makes the client open a CONNECT tunnel through the proxy.
	tunnel, _ := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: "https://zarinpal.example.test",
		Proxy: withAuth(t, connectRefused.URL, "relay", "s3cret-pw")})
	direct, _ := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: goneURL})
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(release) }) // runs first
	slowGW, _ := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: slow.URL})

	for _, c := range []struct {
		name string
		gw   *zarinpal.Gateway
		ctx  time.Duration
		want string
	}{
		{"proxy down", refusing, time.Second * 5, "zarinpal: request failed: the proxy refused the connection"},
		{"proxy refuses the tunnel", tunnel, time.Second * 5, "zarinpal: request failed: the proxy wants other credentials (HTTP 407)"},
		{"zarinpal down", direct, time.Second * 5, "zarinpal: request failed: connection refused"},
		{"too slow", slowGW, 100 * time.Millisecond, "zarinpal: request failed: timed out"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), c.ctx)
		_, err := c.gw.Test(ctx)
		cancel()
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
			continue
		}
		for _, secret := range []string{"s3cret-pw", "relay", host, strings.TrimPrefix(connectRefused.URL, "http://")} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: %q leaks %q", c.name, err, secret)
			}
		}
	}
	// The cause is kept for errors.Is.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := slowGW.Test(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cause lost: %v", err)
	}
}

// cutter is an HTTP proxy that answers with the headers and the start of a
// body, then drops the connection: reset when rst, else closed.
func cutter(t *testing.T, rst bool) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
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
				defer c.Close() //nolint:errcheck // test proxy
				req, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, req.Body) // unread input would reset the connection anyway
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 400\r\n\r\n"+`{"data":{"code":`)
				time.Sleep(200 * time.Millisecond) // the client is reading the body by now
				if rst {
					_ = c.(*net.TCPConn).SetLinger(0)
				}
			}()
		}
	}()
	return "http://" + lis.Addr().String()
}

// A connection cut while the answer is read is described by its kind too:
// the read error names the proxy's address.
func TestCutResponseNamesNoAddress(t *testing.T) {
	for _, c := range []struct {
		name string
		rst  bool
		want string
		is   error
	}{
		{"reset", true, "zarinpal: read response: connection reset", syscall.ECONNRESET},
		{"closed", false, "zarinpal: read response: connection closed", io.ErrUnexpectedEOF},
	} {
		proxy := cutter(t, c.rst)
		g, err := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: "http://zarinpal.example.test",
			Proxy: withAuth(t, proxy, "relay", "s3cret-pw")})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err = g.Test(ctx)
		cancel()
		if err == nil || err.Error() != c.want || !errors.Is(err, c.is) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
			continue
		}
		for _, secret := range []string{"s3cret-pw", "relay", strings.TrimPrefix(proxy, "http://"), "127.0.0.1"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: %q leaks %q", c.name, err, secret)
			}
		}
	}
}
