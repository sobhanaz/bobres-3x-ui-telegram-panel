package trongrid

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tronfake"
)

const (
	store   = "TCKR37NfLumfScqBQX6qrnJR3mZkv6xGqi"
	apiKey  = "4a6e2c1d-7b8f-4e3a-9c5d-1f2e3d4c5b6a" //nolint:gosec // test fixture
	proxyPW = "pr0xy-pw"                             //nolint:gosec // test fixture
)

// The contract address is the well-known one, checksum and all; addresses
// are checked by their checksum, not only their shape.
func TestValidAddress(t *testing.T) {
	if USDTContract != tronfake.USDT {
		t.Fatalf("USDT contract %q differs from the fake's %q", USDTContract, tronfake.USDT)
	}
	for _, a := range []string{USDTContract, store, tronfake.Sender} {
		if !ValidAddress(a) {
			t.Errorf("%s refused", a)
		}
	}
	for _, a := range []string{
		"", "T", store[:33], store + "1", "A" + store[1:], // shape
		store[:33] + "j",                             // one letter off: checksum
		"TCKR37NfLumfScqBQX6qrnJR3mZkv6xGq0",         // 0 is not base58
		"0x71C7656EC7ab88b098defB751B7401B5f6d8976F", // an Ethereum address
	} {
		if ValidAddress(a) {
			t.Errorf("%q accepted", a)
		}
	}
}

// A read asks TronGrid for confirmed incoming transfers of the token from a
// point, oldest first, with the key in its header, and pages by fingerprint
// only (never the next URL in the answer).
func TestTransfersRequestAndPaging(t *testing.T) {
	fake := tronfake.New()
	t.Cleanup(fake.Close)
	fake.RequireKey(apiKey)
	fake.PageSize(2)
	at := time.UnixMilli(1_790_000_000_000)
	for i := range 3 {
		fake.PayAt(store, int64(1_000_001+i), at.Add(time.Duration(i)*time.Second))
	}
	fake.PayAt(store, 9, at.Add(-time.Hour)) // before the point asked for
	c, err := New(Config{APIKey: apiKey, BaseURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	q := gateway.ChainQuery{Address: store, Token: USDTContract, Since: at}
	p1, err := c.Transfers(context.Background(), q)
	if err != nil || len(p1.Transfers) != 2 || p1.Next == "" || p1.Transfers[0].Value != "1000001" ||
		p1.Transfers[0].Token != USDTContract || p1.Transfers[0].To != store || p1.Transfers[0].Type != "Transfer" ||
		!p1.Transfers[0].BlockTS.Equal(at) || len(p1.Transfers[0].TxID) != 64 {
		t.Fatalf("first page: %+v %v", p1, err)
	}
	q.Next = p1.Next
	p2, err := c.Transfers(context.Background(), q)
	if err != nil || len(p2.Transfers) != 1 || p2.Next != "" || p2.Transfers[0].Value != "1000003" {
		t.Fatalf("second page: %+v %v", p2, err)
	}
	if fake.LastKey() != apiKey {
		t.Fatalf("key sent: %q", fake.LastKey())
	}

	var seen atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.URL.String())
		_, _ = w.Write([]byte(`{"success":true,"data":[],"meta":{}}`))
	}))
	t.Cleanup(srv.Close)
	c2, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Transfers(context.Background(), gateway.ChainQuery{Address: store, Token: USDTContract, Since: at, Next: "b2Zmc2V0OjI"}); err != nil {
		t.Fatal(err)
	}
	u := seen.Load().(string)
	for _, want := range []string{"/v1/accounts/" + store + "/transactions/trc20?", "only_to=true", "only_confirmed=true",
		"contract_address=" + USDTContract, "order_by=block_timestamp%2Casc", "limit=200", "min_timestamp=1790000000000", "fingerprint=b2Zmc2V0OjI"} {
		if !strings.Contains(u, want) {
			t.Errorf("request %s lacks %s", u, want)
		}
	}
	if _, err := c2.Transfers(context.Background(), gateway.ChainQuery{Address: store, Token: USDTContract, Next: "../../x?y=1"}); err == nil {
		t.Fatal("a page cursor with path characters was sent")
	}
}

// Errors say what went wrong without the body, the key or the proxy's
// address; redirects are not followed (the key would go along).
func TestTransfersErrors(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
	}))
	t.Cleanup(elsewhere.Close)
	answer := func(code int, body string, header map[string]string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(s.Close)
		return s
	}
	cases := map[string]struct {
		srv  *httptest.Server
		want string
	}{
		"bad key":      {answer(401, `{"Error":"ApiKey `+apiKey+` not exists"}`, nil), "trongrid: the API key was refused (HTTP 401)"},
		"rate limited": {answer(429, `slow down`, nil), "trongrid: rate limited (HTTP 429): set a TronGrid API key"},
		"down":         {answer(503, `<html>`, nil), "trongrid: HTTP 503"},
		"redirect":     {answer(302, ``, map[string]string{"Location": elsewhere.URL}), "trongrid: unexpected redirect (HTTP 302)"},
		"not json":     {answer(200, `<html>`, nil), "trongrid: unexpected response (not the transfers list)"},
		"not success":  {answer(200, `{"success":false,"data":[]}`, nil), "trongrid: the request was not successful"},
	}
	for name, c := range cases {
		cl, err := New(Config{APIKey: apiKey, BaseURL: c.srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = cl.Transfers(context.Background(), gateway.ChainQuery{Address: store, Token: USDTContract})
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v", name, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("a redirect was followed")
	}

	// A dead proxy: the error names the kind of failure only.
	cl, err := New(Config{APIKey: apiKey, Proxy: "http://relay:" + proxyPW + "@127.0.0.1:1", BaseURL: "http://203.0.113.9:80"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cl.Transfers(context.Background(), gateway.ChainQuery{Address: store, Token: USDTContract})
	if err == nil || strings.Contains(err.Error(), proxyPW) || strings.Contains(err.Error(), "127.0.0.1") ||
		strings.Contains(err.Error(), "203.0.113.9") || !strings.HasPrefix(err.Error(), "trongrid: request failed: ") {
		t.Fatalf("proxy error: %v", err)
	}
	var ne *netError
	if !errors.As(err, &ne) {
		t.Fatalf("not a network error: %T", err)
	}

	for name, c := range map[string]Config{
		"key with spaces": {APIKey: "a key"},
		"key with CRLF":   {APIKey: "abcdefgh\r\nX-Evil: 1"},
		"socks proxy":     {Proxy: "socks5://127.0.0.1:1080"},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// value may come as a string (documented) or a number.
func TestValueAsStringOrNumber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":[
			{"transaction_id":"aa","token_info":{"address":"` + USDTContract + `"},"block_timestamp":1,"from":"f","to":"t","type":"Transfer","value":"12"},
			{"transaction_id":"bb","token_info":{"address":"` + USDTContract + `"},"block_timestamp":1,"from":"f","to":"t","type":"Transfer","value":34},
			{"transaction_id":"cc","token_info":{"address":"` + USDTContract + `"},"block_timestamp":1,"from":"f","to":"t","type":"Transfer","value":{"x":1}}
		],"meta":{"fingerprint":"next"}}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Transfers(context.Background(), gateway.ChainQuery{Address: store, Token: USDTContract})
	if err != nil || len(p.Transfers) != 3 || p.Transfers[0].Value != "12" || p.Transfers[1].Value != "34" || p.Transfers[2].Value != "" || p.Next != "next" {
		t.Fatalf("page: %+v %v", p, err)
	}
}
