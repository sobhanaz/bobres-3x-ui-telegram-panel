package runner

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func supplierReq(action, key, body string) string {
	b, _ := json.Marshal(SupplierRequest{Action: action, Key: key, Body: body})
	return string(b)
}

const supKey = "supkey-xxxxxxxxxxxxxxxx"

// Only core may call; the key goes to the ONE configured supplier as a bearer token, the order's JSON as the body, and the
// answer (data or a refusal) comes back as it was.
func TestSupplierProxyPassesThrough(t *testing.T) {
	var got struct{ method, path, query, auth, ctype, body string }
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.query, got.auth, got.ctype, got.body = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(b)
		if r.URL.Query().Get("action") == "order" {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":"insufficient_balance"}`))
			return
		}
		_, _ = w.Write([]byte(`{"products":[{"id":"p1"}]}`))
	}))
	defer srv.Close()
	h := SupplierProxy{URL: srv.URL + "/api/reseller", Client: srv.Client(), CoreToken: coreTok}.Handler()

	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if rec := post(t, h, "/internal/supplier", tok, supplierReq("products", supKey, "")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, rec.Code)
		}
	}
	if got.method != "" {
		t.Fatalf("an unauthorized call reached the supplier: %+v", got)
	}
	rec := post(t, h, "/internal/supplier", coreTok, supplierReq("products", supKey, ""))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"products":[{"id":"p1"}]}` || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("products: %d %q", rec.Code, rec.Body)
	}
	if got.method != "GET" || got.path != "/api/reseller" || got.query != "action=products" || got.auth != "Bearer "+supKey || got.body != "" {
		t.Fatalf("what the supplier got: %+v", got)
	}
	rec = post(t, h, "/internal/supplier", coreTok, supplierReq("order", supKey, `{"product_id":"p1","quantity":1,"external_order_id":"o1"}`))
	if rec.Code != http.StatusPaymentRequired || !strings.Contains(rec.Body.String(), "insufficient_balance") {
		t.Fatalf("a refusal: %d %q", rec.Code, rec.Body)
	}
	if got.method != "POST" || got.query != "action=order" || got.ctype != "application/json" || got.body != `{"product_id":"p1","quantity":1,"external_order_id":"o1"}` {
		t.Fatalf("the order the supplier got: %+v", got)
	}
}

// Core names an action and brings a key; nothing else gets through, and nothing is sent to an address core names.
func TestSupplierProxyRefusesWhatIsNotASupplierCall(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	h := SupplierProxy{URL: srv.URL, Client: srv.Client(), CoreToken: coreTok}.Handler()
	bad := map[string]string{
		"another action":                        supplierReq("refund", supKey, `{}`),
		"an action with a query":                supplierReq("products&x=1", supKey, ""),
		"an order without a body":               supplierReq("order", supKey, ""),
		"an order with a body that is not JSON": supplierReq("order", supKey, "nope"),
		"a body on a GET":                       supplierReq("balance", supKey, `{}`),
		"no key":                                supplierReq("balance", "", ""),
		"a short key":                           supplierReq("balance", "abc", ""),
		"a key with a space":                    supplierReq("balance", "supkey 0123456789", ""),
		"a key with a newline":                  supplierReq("balance", "supkey-0123456789\nX-Evil: 1", ""),
		"not JSON":                              `[]`,
		"an address":                            `{"action":"products","key":"` + supKey + `","url":"https://evil.example"}`,
		"a long order":                          supplierReq("order", supKey, `{"a":"`+strings.Repeat("x", 17<<10)+`"}`),
	}
	for name, body := range bad {
		if rec := post(t, h, "/internal/supplier", coreTok, body); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "bad_request" {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Body)
		}
	}
	if calls != 0 {
		t.Fatalf("%d refused calls reached the supplier", calls)
	}
}

func TestSupplierProxyWhenTheSupplierIsMissingOrBroken(t *testing.T) {
	good := supplierReq("balance", supKey, "")
	if rec := post(t, SupplierProxy{CoreToken: coreTok}.Handler(), "/internal/supplier", coreTok, good); rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "not_configured" {
		t.Fatalf("no supplier: %d %q", rec.Code, rec.Body)
	}
	if rec := post(t, SupplierProxy{URL: "http://plain.example", CoreToken: coreTok}.Handler(), "/internal/supplier", coreTok, good); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a plain http supplier: %d %q", rec.Code, rec.Body)
	}
	for name, c := range map[string]struct {
		status int
		body   string
	}{"a 500": {500, `{"error":"x"}`}, "html": {200, `<html>`}, "a redirect": {302, ``}} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		rec := post(t, SupplierProxy{URL: srv.URL, Client: srv.Client(), CoreToken: coreTok}.Handler(), "/internal/supplier", coreTok, good)
		srv.Close()
		if rec.Code != http.StatusBadGateway || errorCode(t, rec) != "server_error" {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Body)
		}
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := srv.Client()
	srv.Close()
	if rec := post(t, SupplierProxy{URL: srv.URL, Client: client, CoreToken: coreTok}.Handler(), "/internal/supplier", coreTok, good); rec.Code != http.StatusBadGateway || errorCode(t, rec) != "unreachable" {
		t.Fatalf("unreachable: %d %q", rec.Code, rec.Body)
	}
}
