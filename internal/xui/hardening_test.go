package xui_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func TestNewRejectsInsecureAndBadURLs(t *testing.T) {
	cases := []struct {
		name, url, tok string
		want           error
	}{
		{"http without opt-in", "http://panel.example.com", "t", xui.ErrInsecureURL},
		{"no host", "https://", "t", nil},
		{"ftp", "ftp://panel.example.com", "t", nil},
		{"creds in url", "https://u:p@panel.example.com", "t", nil},
		{"empty token", "https://panel.example.com", "", nil},
		{"garbage", "::::", "t", nil},
	}
	for _, c := range cases {
		_, err := xui.New(c.url, c.tok)
		if err == nil {
			t.Errorf("%s: want error", c.name)
		} else if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	if _, err := xui.New("https://panel.example.com/secretpath", "t"); err != nil {
		t.Fatalf("valid https URL rejected: %v", err)
	}
	if _, err := xui.New("http://10.0.0.5:2053/path", "t", xui.AllowPrivateAddresses()); err != nil {
		t.Fatalf("plain http with allow-private rejected: %v", err)
	}
	// A custom client would skip the private-only dial check.
	if _, err := xui.New("http://10.0.0.5:2053", "t", xui.AllowPrivateAddresses(), xui.WithHTTPClient(http.DefaultClient)); !errors.Is(err, xui.ErrInsecureURL) {
		t.Fatalf("plain http with a custom client: %v", err)
	}
}

func TestPlainHTTPReachesOnlyPrivateAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"obj":[]}`))
	}))
	t.Cleanup(srv.Close)
	// Loopback over plain http with allow-private: allowed.
	c, err := xui.New(srv.URL, token, xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.InboundOptions(context.Background()); err != nil || hits.Load() != 1 {
		t.Fatalf("private plain-http panel: err=%v hits=%d", err, hits.Load())
	}
	// A public address over plain http is refused before any byte is sent
	// (192.0.2.1 is TEST-NET-1: public, never routed).
	c, err = xui.New("http://192.0.2.1:2053", token, xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.InboundOptions(ctx); !errors.Is(err, xui.ErrPublicPlaintext) {
		t.Fatalf("want ErrPublicPlaintext, got %v", err)
	}
}

func TestRedirectsAreNeverFollowedAndTokenNeverForwarded(t *testing.T) {
	var evilHits atomic.Int32
	var evilSawAuth atomic.Bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits.Add(1)
		if r.Header.Get("Authorization") != "" {
			evilSawAuth.Store(true)
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(evil.Close)
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusTemporaryRedirect) // 307 replays POST + headers
	}))
	t.Cleanup(panel.Close)

	err := newClient(t, panel.URL, token).AddClient(context.Background(), spec("x"), []int{1})
	var ae *xui.APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusTemporaryRedirect {
		t.Fatalf("want redirect APIError, got %v", err)
	}
	if evilHits.Load() != 0 || evilSawAuth.Load() {
		t.Fatalf("redirect was followed (hits=%d, token forwarded=%v)", evilHits.Load(), evilSawAuth.Load())
	}
}

func TestPrivateAddressesBlockedByDefault(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	t.Cleanup(srv.Close)
	// insecure allowed (server is http) but private addresses NOT allowed: must refuse to connect.
	c, err := xui.New(srv.URL, token, xui.AllowInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	err = c.AddClient(context.Background(), spec("x"), []int{1})
	if !errors.Is(err, xui.ErrBlockedAddress) {
		t.Fatalf("want ErrBlockedAddress, got %v", err)
	}
	if hit.Load() {
		t.Fatal("request reached a loopback server despite the block")
	}
}

func TestErrorsDoNotLeakURLPathOrEmail(t *testing.T) {
	c, err := xui.New("http://127.0.0.1:1/very-secret-base-path", token, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetClient(context.Background(), "customer@example.com")
	if err == nil {
		t.Fatal("want connection error")
	}
	for _, leak := range []string{"very-secret-base-path", "customer@example.com", token} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error leaks %q: %v", leak, err)
		}
	}
}

func TestPanelMessageIsSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"msg":"bad\nINJECTED log line ` + strings.Repeat("A", 500) + `"}`))
	}))
	t.Cleanup(srv.Close)
	err := newClient(t, srv.URL, token).AddClient(context.Background(), spec("x"), []int{1})
	var ae *xui.APIError
	if !errors.As(err, &ae) {
		t.Fatal(err)
	}
	if strings.ContainsAny(ae.Msg, "\n\r") || len(ae.Msg) > 210 {
		t.Fatalf("message not sanitized (len=%d): %q", len(ae.Msg), ae.Msg)
	}
}

func TestPathTraversalIdentifiersRejectedBeforeAnyRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	t.Cleanup(srv.Close)
	c := newClient(t, srv.URL, token)
	ctx := context.Background()
	for _, bad := range []string{"", ".", "..", "a/b", "a b", "a?x=1", "a#b", "a\nb", "a%2fb", strings.Repeat("a", 129)} {
		if _, err := c.GetClient(ctx, bad); !errors.Is(err, xui.ErrInvalidIdentifier) {
			t.Errorf("GetClient(%q): want ErrInvalidIdentifier, got %v", bad, err)
		}
		if err := c.DeleteClient(ctx, bad, false); !errors.Is(err, xui.ErrInvalidIdentifier) {
			t.Errorf("DeleteClient(%q): got %v", bad, err)
		}
		if _, err := c.SubLinks(ctx, bad); !errors.Is(err, xui.ErrInvalidIdentifier) {
			t.Errorf("SubLinks(%q): got %v", bad, err)
		}
	}
	if _, err := c.BulkAdjust(ctx, xui.AdjustRequest{Emails: []string{"ok", ".."}, AddDays: 1}); !errors.Is(err, xui.ErrInvalidIdentifier) {
		t.Errorf("BulkAdjust: got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("%d requests were sent for invalid identifiers", hits.Load())
	}
}

func TestLockMapDoesNotGrow(t *testing.T) {
	c, _ := setup(t)
	ctx := context.Background()
	for i := 0; i < 200; i++ {
		email := "user" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + "@x.io"
		if err := c.AddClient(ctx, spec(email), []int{1}); err != nil {
			t.Fatal(err)
		}
	}
	if n := xui.LockCountForTest(c); n != 0 {
		t.Fatalf("lock map leaked %d entries after all calls finished", n)
	}
}

// Real 3x-ui v3 panels return the client's numeric row id in "id" and the
// protocol UUID in "uuid" from clients/get (found by the real-panel CI job).
func TestGetClientDecodesTheRealPanelShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"msg":"","obj":{"client":{"id":14825,"uuid":"0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b",` +
			`"email":"u1-o1","subId":"abc123","totalGB":1073741824,"expiryTime":1735689600000,"limitIp":0,"enable":true,` +
			`"tgId":0,"comment":"","flow":""},"inboundIds":[1,2]}}`))
	}))
	t.Cleanup(srv.Close)
	d, err := newClient(t, srv.URL, token).GetClient(context.Background(), "u1-o1")
	if err != nil {
		t.Fatal(err)
	}
	if string(d.Client.ID) != "14825" || d.Client.UUID != "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b" || d.Client.SubID != "abc123" ||
		len(d.InboundIDs) != 2 || d.Client.TotalGB != 1<<30 {
		t.Fatalf("decoded %+v", d)
	}
}
