package runner

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

const hostedTok = "bm_fakeMetricsTokenFakeMetricsTokenFake0123" //gitleaks:allow

// storeClient sends every connection to the test server, whatever name was asked for (the store's domain).
func storeClient(srv *httptest.Server) *http.Client {
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // a test server's own certificate
	return &http.Client{Transport: tr}
}

func hostedReq(domain, token string) string {
	b, _ := json.Marshal(HostedMetricsRequest{Domain: domain, Token: token})
	return string(b)
}

// Only core may call; the bot reads https://<domain>/metrics with the token and passes back the store's status and text.
func TestHostedMetricsReadsTheStore(t *testing.T) {
	var gotAuth, gotPath, gotHost string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotHost = r.Header.Get("Authorization"), r.URL.Path, r.Host
		if r.Header.Get("Authorization") != "Bearer "+hostedTok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("bobres_build_info{version=\"0.20.0\"} 1\n"))
	}))
	defer srv.Close()
	h := HostedMetrics{CoreToken: coreTok, Client: storeClient(srv)}.Handler()

	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if rec := post(t, h, "/internal/hosted/metrics", tok, hostedReq("shop.example.com", hostedTok)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, rec.Code)
		}
	}
	if gotPath != "" {
		t.Fatal("an unauthorized call reached the store")
	}
	rec := post(t, h, "/internal/hosted/metrics", coreTok, hostedReq("shop.example.com", hostedTok))
	var out struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Status != 200 || !strings.Contains(out.Body, "0.20.0") {
		t.Fatalf("read: %d %s", rec.Code, rec.Body)
	}
	if gotAuth != "Bearer "+hostedTok || gotPath != "/metrics" || gotHost != "shop.example.com" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("what the store got: %q %q %q", gotAuth, gotPath, gotHost)
	}
	// A refusal is the store's status, without a body.
	rec = post(t, h, "/internal/hosted/metrics", coreTok, hostedReq("shop.example.com", "bm_anotherFakeTokenAnotherFakeToken9876")) //gitleaks:allow
	out.Status, out.Body = 0, ""
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Status != 401 || out.Body != "" {
		t.Fatalf("refused: %d %s", rec.Code, rec.Body)
	}
}

// Core names a domain, never an address, a port or a path; anything else is refused before any call.
func TestHostedMetricsRefusesWhatIsNotAStore(t *testing.T) {
	called := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	h := HostedMetrics{CoreToken: coreTok, Client: storeClient(srv)}.Handler()
	for _, body := range []string{
		hostedReq("127.0.0.1", hostedTok),
		hostedReq("shop.example.com:8443", hostedTok),
		hostedReq("shop.example.com/admin", hostedTok),
		hostedReq("localhost", hostedTok),
		hostedReq("shop.example.com", "short"),
		hostedReq("shop.example.com", hostedTok+"\nX-Injected: 1"),
		`{"domain":"shop.example.com","token":"` + hostedTok + `","url":"http://169.254.169.254/"}`,
		`not json`,
	} {
		if rec := post(t, h, "/internal/hosted/metrics", coreTok, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, rec.Code)
		}
	}
	if called {
		t.Fatal("a refused request reached a server")
	}
}

// The guarded client refuses, at the socket, any address that is not public: a name that resolves to the bot's own network is not reached.
func TestHostedGuardRefusesPrivateAddresses(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false, "100.64.0.1": false,
		"0.0.0.0": false, "::1": false, "fc00::1": false, "fe80::1": false, "224.0.0.1": false, "::ffff:127.0.0.1": false, "198.18.0.1": false,
		"64:ff9b::7f00:1": false,
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
	tr := guardedClient().Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Error("the guarded client goes through a proxy")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close() //nolint:errcheck // test listener
	if _, err := tr.DialContext(context.Background(), "tcp", ln.Addr().String()); !errors.Is(err, errNotPublic) {
		t.Fatalf("dialing loopback: %v", err)
	}
}
