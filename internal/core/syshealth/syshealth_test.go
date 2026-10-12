package syshealth

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeService answers /healthz with version and /readyz with checks (ready
// when every check is ok).
func fakeService(t *testing.T, version string, checks map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "x", "version": version})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		code := http.StatusOK
		for _, v := range checks {
			if v != "ok" {
				code = http.StatusServiceUnavailable
			}
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "x", "checks": checks})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func byName(r Report) map[string]Service {
	out := map[string]Service{}
	for _, s := range r.Services {
		out[s.Name] = s
	}
	return out
}

// Every service is asked at once: ready, not ready (naming the failed
// check), down, an optional one that is not on the network, one with no
// address, and a version that is not core's.
func TestCheckAggregates(t *testing.T) {
	core := fakeService(t, "v1.4.0", map[string]string{"db": "ok", "grpc": "ok"})
	bot := fakeService(t, "1.4.0", map[string]string{"redis": "fail"})
	pay := fakeService(t, "1.3.9", map[string]string{"db": "ok"})
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close() // nothing listens there any more
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(broken.Close)

	c := New("v1.4.0", []Peer{
		{Name: "core", URL: core.URL},
		{Name: "bot", URL: bot.URL},
		{Name: "payments", URL: pay.URL},
		{Name: "provisioner", URL: goneURL},
		{Name: "backup", URL: "", Optional: true},
		{Name: "extra", URL: ""},
		{Name: "proxy", URL: broken.URL},
	})
	r := c.Check(context.Background())
	if r.Version != "v1.4.0" || len(r.Services) != 7 || r.Services[0].Name != "core" {
		t.Fatalf("report: %+v", r)
	}
	got := byName(r)
	if s := got["core"]; s.Status != StatusOK || s.Version != "v1.4.0" || s.Checks["db"] != "ok" || s.VersionMismatch {
		t.Errorf("core: %+v", s)
	}
	if s := got["bot"]; s.Status != StatusNotReady || s.Detail != "failing: redis" || s.VersionMismatch {
		t.Errorf("bot (1.4.0 is v1.4.0): %+v", s)
	}
	if s := got["payments"]; s.Status != StatusOK || !s.VersionMismatch {
		t.Errorf("payments: %+v", s)
	}
	if !r.VersionMismatch {
		t.Error("a mismatch is not flagged on the report")
	}
	if s := got["provisioner"]; s.Status != StatusDown || s.Detail != "does not answer" || s.Version != "" {
		t.Errorf("provisioner: %+v", s)
	}
	if s := got["backup"]; s.Status != StatusNotInstalled {
		t.Errorf("backup: %+v", s)
	}
	if s := got["extra"]; s.Status != StatusNotConfigured {
		t.Errorf("extra: %+v", s)
	}
	if s := got["proxy"]; s.Status != StatusDown || s.Detail != "answers HTTP 502" {
		t.Errorf("proxy: %+v", s)
	}
}

// An optional service whose name the network does not know is not
// installed; a required one is down ("unknown host").
func TestNoSuchHost(t *testing.T) {
	dnsFail := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "backup", IsNotFound: true}
		},
	}}
	c := New("dev", []Peer{{Name: "backup", URL: "http://backup:8080", Optional: true}, {Name: "payments", URL: "http://payments:8080"}})
	c.Client = dnsFail
	got := byName(c.Check(context.Background()))
	if s := got["backup"]; s.Status != StatusNotInstalled || s.Detail != "" {
		t.Errorf("backup: %+v", s)
	}
	if s := got["payments"]; s.Status != StatusDown || s.Detail != "unknown host" {
		t.Errorf("payments: %+v", s)
	}
}

// servfailDNS is a DNS server on a local UDP port that answers every query
// with SERVFAIL, as Docker's embedded DNS does on an internal-only network
// for a name it does not know. It returns the address to send queries to.
func servfailDNS(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			// The query echoed back as a response (QR) with RA and
			// RCODE 2 (SERVFAIL): same id and question, no answers.
			resp := append([]byte(nil), buf[:n]...)
			resp[2] |= 0x80
			resp[3] = 0x80 | 2
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().String()
}

// On Docker's internal networks a service that does not exist resolves to
// SERVFAIL, not "no such host". An optional one is still not installed; a
// required one is down.
func TestServfailIsNotInstalled(t *testing.T) {
	dnsAddr := servfailDNS(t)
	res := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", dnsAddr)
	}}
	const host = "bobres-backup-test"
	// The stub really gives a lookup error that is not "not found".
	_, err := res.LookupHost(context.Background(), host)
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || dnsErr.IsNotFound {
		t.Fatalf("stub lookup: %#v", err)
	}
	dialer := &net.Dialer{Resolver: res}
	c := New("dev", []Peer{
		{Name: "backup", URL: "http://" + host + ":8080", Optional: true},
		{Name: "payments", URL: "http://bobres-payments-test:8080"},
	})
	c.Client = &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}
	got := byName(c.Check(context.Background()))
	if s := got["backup"]; s.Status != StatusNotInstalled || s.Detail != "" || s.LatencyMS != 0 {
		t.Errorf("backup: %+v", s)
	}
	if s := got["payments"]; s.Status != StatusDown || s.Detail != "unknown host" {
		t.Errorf("payments: %+v", s)
	}
}

// A service that hangs is reported down within the timeout, and the others
// are not held up by it.
func TestSlowServiceTimesOut(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slow.Close)
	t.Cleanup(func() { close(release) })
	fast := fakeService(t, "dev", map[string]string{})
	c := New("dev", []Peer{{Name: "slow", URL: slow.URL}, {Name: "fast", URL: fast.URL}})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := byName(c.Check(ctx))
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	if s := got["slow"]; s.Status != StatusDown || !strings.Contains(s.Detail, "in time") {
		t.Errorf("slow: %+v", s)
	}
	if s := got["fast"]; s.Status != StatusOK {
		t.Errorf("fast: %+v", s)
	}
}

// A redirect is not followed (the answer is the redirect itself).
func TestRedirectNotFollowed(t *testing.T) {
	target := fakeService(t, "dev", nil)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/healthz", http.StatusFound) //nolint:gosec // test server, fixed target
	}))
	t.Cleanup(redir.Close)
	got := byName(New("dev", []Peer{{Name: "r", URL: redir.URL}}).Check(context.Background()))
	if s := got["r"]; s.Status != StatusDown || s.Detail != "answers HTTP 302" {
		t.Errorf("redirect: %+v", s)
	}
}

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{"v1.2.3": "1.2.3", "1.2.3": "1.2.3", " dev ": "dev", "": ""} {
		if got := NormalizeVersion(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
