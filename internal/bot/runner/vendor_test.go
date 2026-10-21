package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	vNonce = "abcdefghijklmnop012345"
	// 86 characters: a base64url Ed25519 signature
	vSig = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

func vendorReq(path, body string) string {
	b, _ := json.Marshal(VendorAdminRequest{Path: path, TS: "1792533257", Nonce: vNonce, Sig: vSig, Body: body})
	return string(b)
}

// Only core may call; the signature headers and the body go to the ONE configured server's admin path untouched,
// and its answer (a license, or a refusal) comes back as it was.
func TestVendorAdminPassesThrough(t *testing.T) {
	var got struct {
		path, ts, nonce, sig, ctype, body string
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(r.Body)
		got.path, got.ts, got.nonce, got.sig, got.ctype, got.body = r.Method+" "+r.URL.Path, r.Header.Get("X-Bobres-Ts"),
			r.Header.Get("X-Bobres-Nonce"), r.Header.Get("X-Bobres-Sig"), r.Header.Get("Content-Type"), b.String()
		if r.URL.Path == "/admin/v1/licenses/revoke" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"revoked","message":"already"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"lic_x","key":"BOBRES-SECRET"}`))
	}))
	defer srv.Close()
	h := LicenseProxy{Server: srv.URL, Client: srv.Client(), CoreToken: coreTok}.VendorAdminHandler()

	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if rec := post(t, h, "/internal/vendor/admin", tok, vendorReq("/admin/v1/licenses/create", `{"ref":"x"}`)); rec.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, rec.Code)
		}
	}
	if got.path != "" {
		t.Fatalf("an unauthorized call reached the server: %q", got.path)
	}
	rec := post(t, h, "/internal/vendor/admin", coreTok, vendorReq("/admin/v1/licenses/create", `{"ref":"abcdefgh"}`))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"id":"lic_x","key":"BOBRES-SECRET"}` || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	if got.path != "POST /admin/v1/licenses/create" || got.ts != "1792533257" || got.nonce != vNonce || got.sig != vSig ||
		got.ctype != "application/json" || got.body != `{"ref":"abcdefgh"}` {
		t.Fatalf("what the server got: %+v", got)
	}
	// A refusal passes through with its status.
	rec = post(t, h, "/internal/vendor/admin", coreTok, vendorReq("/admin/v1/licenses/revoke", `{}`))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"revoked"`) {
		t.Fatalf("a refusal: %d %q", rec.Code, rec.Body)
	}
}

// Core names a path of the admin API and brings the signature; nothing else gets through.
func TestVendorAdminRefusesWhatIsNotAnAdminCall(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	h := LicenseProxy{Server: srv.URL, Client: srv.Client(), CoreToken: coreTok}.VendorAdminHandler()
	bad := map[string]string{
		"another path":       vendorReq("/v1/activate", `{}`),
		"a path with a host": vendorReq("//evil.example/admin/v1/licenses/list", `{}`),
		"a query":            vendorReq("/admin/v1/licenses/list?x=1", `{}`),
		"a traversal":        vendorReq("/admin/v1/licenses/../../x", `{}`),
		"a body not JSON":    vendorReq("/admin/v1/licenses/list", `nope`),
		"a body too long":    vendorReq("/admin/v1/licenses/list", `{"a":"`+strings.Repeat("x", 17<<10)+`"}`),
		"not JSON":           `[]`,
		"an unknown field":   `{"path":"/admin/v1/licenses/list","ts":"1792533257","nonce":"` + vNonce + `","sig":"` + vSig + `","body":"{}","url":"https://evil.example"}`,
		"a short signature":  strings.Replace(vendorReq("/admin/v1/licenses/list", `{}`), vSig, "AAAA", 1),
		"a bad nonce":        strings.Replace(vendorReq("/admin/v1/licenses/list", `{}`), vNonce, "no spaces allowed!", 1),
	}
	for name, body := range bad {
		if rec := post(t, h, "/internal/vendor/admin", coreTok, body); rec.Code != http.StatusBadRequest || errorCode(t, rec) != "bad_request" {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Body)
		}
	}
	if calls != 0 {
		t.Fatalf("%d refused calls reached the license server", calls)
	}
}

func TestVendorAdminWhenTheServerIsMissingOrBroken(t *testing.T) {
	good := vendorReq("/admin/v1/licenses/list", `{}`)
	if rec := post(t, LicenseProxy{CoreToken: coreTok}.VendorAdminHandler(), "/internal/vendor/admin", coreTok, good); rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "not_configured" {
		t.Fatalf("no server: %d %q", rec.Code, rec.Body)
	}
	for name, c := range map[string]struct {
		status int
		body   string
	}{"a 500": {500, `{"error":"internal","message":"x"}`}, "html": {200, `<html>`}, "a redirect": {302, ``}} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		rec := post(t, LicenseProxy{Server: srv.URL, Client: srv.Client(), CoreToken: coreTok}.VendorAdminHandler(), "/internal/vendor/admin", coreTok, good)
		srv.Close()
		if rec.Code != http.StatusBadGateway || errorCode(t, rec) != "server_error" {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Body)
		}
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := srv.Client()
	srv.Close() // nothing listens any more
	if rec := post(t, LicenseProxy{Server: srv.URL, Client: client, CoreToken: coreTok}.VendorAdminHandler(), "/internal/vendor/admin", coreTok, good); rec.Code != http.StatusBadGateway || errorCode(t, rec) != "unreachable" {
		t.Fatalf("unreachable: %d %q", rec.Code, rec.Body)
	}
}
