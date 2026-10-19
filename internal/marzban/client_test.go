package marzban

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func TestParseCredentials(t *testing.T) {
	u, p, err := ParseCredentials("admin:pa:ss")
	if err != nil || u != "admin" || p != "pa:ss" {
		t.Fatalf("got %q %q %v", u, p, err)
	}
	for _, bad := range []string{"", "x", ":p", "u:"} {
		if _, _, err := ParseCredentials(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// The accepted and refused names are the real panel's answers (v0.8.4): the
// integration test TestIntegrationUsernameRule asks the panel the same
// questions.
func TestValidateUsername(t *testing.T) {
	for _, ok := range []string{
		"u10-abcd1234", "u42-abcd1234", "u123456789-abcd1234", "abc", "a_b", "ABC_def.ghi", "abc-def", "abc@x.y",
		"user@example.com", strings.Repeat("a", 32),
	} {
		if err := ValidateUsername(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "ab", "u1-abcd1234", "u9-abcd1234", "a-bcdef", "x@y.z", "-abc", ".abc", "u-100123-abc", // lead run under 3
		"has space", "a+b", "../x", "u/1", "abc/def", "abc%41", "über", "abcé", "abc\n", // characters
		strings.Repeat("a", 33), "abc-" + strings.Repeat("x", 40), // longer than BOBRES sends
	} {
		if ValidateUsername(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDetailOf(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"string":    {`{"detail":"User not found"}`, "User not found"},
		"list":      {`{"detail":[{"msg":"field required","loc":["body"]}]}`, "field required"},
		"no detail": {`{"x":1}`, ""},
		"not json":  {`Internal Server Error`, ""},
		// Marzban's own handler: {"field": "message"} with pydantic's prefix.
		"object": {`{"detail":{"username":"Value error, Username only can be 3 to 32 characters."}}`,
			"username: Username only can be 3 to 32 characters."},
		"object, fields in order": {`{"detail":{"status":"Value error, B","data_limit":"A"}}`, "data_limit: A; status: B"},
		"control characters":      {"{\"detail\":\"a\\u0007b\\nc\"}", "abc"},
	} {
		if got := detailOf([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: detailOf = %q, want %q", name, got, tc.want)
		}
	}
}

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "admin", "pw", xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A refused body is reported with the panel's message, whatever shape it has.
func TestRefusalMessageFromAnObjectDetail(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/token" {
			_, _ = w.Write([]byte(`{"access_token":"t","token_type":"bearer"}`))
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":{"status":"Value error, User cannot be on hold without a valid on_hold_expire_duration."}}`))
	})
	err := c.ModifyUser(context.Background(), "abc-def", map[string]any{"status": "on_hold"})
	if err == nil || !strings.Contains(err.Error(), "http 422") || !strings.Contains(err.Error(), "status: User cannot be on hold") {
		t.Fatalf("error = %v", err)
	}
}

// The dashboard's address answers 405 to the login; the error says what to enter.
func TestLoginHintForTheDashboardAddress(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"detail":"Method Not Allowed"}`))
	})
	_, err := c.Version(context.Background())
	if err == nil || !strings.Contains(err.Error(), "http 405") || !strings.Contains(err.Error(), "without /dashboard/") {
		t.Fatalf("error = %v", err)
	}
	// A refused password does not get the hint.
	c2 := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Incorrect username or password"}`))
	})
	_, err = c2.Version(context.Background())
	if !IsUnauthorized(err) || strings.Contains(err.Error(), "/dashboard/") {
		t.Fatalf("error = %v", err)
	}
}

// Marzban 1.0 answers the inbounds as a list of tags, not a map by protocol;
// the error says it is another generation rather than "not a Marzban panel".
func TestInboundsOfAnotherGeneration(t *testing.T) {
	answer := `["Shadowsocks TCP"]`
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/token" {
			_, _ = w.Write([]byte(`{"access_token":"t","token_type":"bearer"}`))
			return
		}
		_, _ = w.Write([]byte(answer))
	})
	if _, err := c.Inbounds(context.Background()); err == nil || !strings.Contains(err.Error(), "Marzban 1.x") {
		t.Fatalf("a list of tags: %v", err)
	}
	answer = `"just a string"`
	if _, err := c.Inbounds(context.Background()); err == nil || !strings.Contains(err.Error(), "not a Marzban panel") {
		t.Fatalf("a string: %v", err)
	}
	answer = `{"vless":[{"tag":"VLESS TCP","protocol":"vless","network":"tcp","tls":"none","port":443}]}`
	by, err := c.Inbounds(context.Background())
	if err != nil || len(by["vless"]) != 1 || by["vless"][0].PortNumber() != 443 {
		t.Fatalf("the 0.8 shape: %v %v", by, err)
	}
}

func TestUserOwner(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/admin/token":
			_, _ = w.Write([]byte(`{"access_token":"t","token_type":"bearer"}`))
		case "/api/user/owned-user":
			_, _ = w.Write([]byte(`{"username":"owned-user","status":"active","admin":{"username":"ops","is_sudo":true}}`))
		case "/api/user/ownerless-user":
			_, _ = w.Write([]byte(`{"username":"ownerless-user","status":"active","admin":null}`))
		}
	})
	ctx := context.Background()
	if u, err := c.GetUser(ctx, "owned-user"); err != nil || !u.HasOwner() {
		t.Fatalf("owned user: %+v %v", u, err)
	}
	if u, err := c.GetUser(ctx, "ownerless-user"); err != nil || u.HasOwner() {
		t.Fatalf("ownerless user: %+v %v", u, err)
	}
}

// A user printed in a log or a failed test shows no credentials.
func TestUserStringHidesCredentials(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/token" {
			_, _ = w.Write([]byte(`{"access_token":"t","token_type":"bearer"}`))
			return
		}
		_, _ = w.Write([]byte(`{"username":"abc-def","status":"active","expire":1795024136,"data_limit":1073741824,
			"proxies":{"shadowsocks":{"password":"SECRET-PASSWORD","method":"chacha20-ietf-poly1305"}},
			"links":["ss://SECRET-LINK@host:1080"],"subscription_url":"/sub/SECRET-TOKEN","admin":{"username":"ops"}}`))
	})
	u, err := c.GetUser(context.Background(), "abc-def")
	if err != nil {
		t.Fatal(err)
	}
	for _, shown := range []string{fmt.Sprintf("%v", u), fmt.Sprintf("%+v", u), u.String()} {
		if strings.Contains(shown, "SECRET") || !strings.Contains(shown, "abc-def") || !strings.Contains(shown, "links=1") {
			t.Errorf("printed as %q", shown)
		}
	}
	if len(u.Proxies["shadowsocks"]) == 0 {
		t.Error("the credentials are not kept for the tests that connect with them")
	}
	var none *User
	if none.String() != "<nil>" {
		t.Errorf("nil user printed as %q", none.String())
	}
}

func TestIsServerError(t *testing.T) {
	if !IsServerError(&APIError{Status: 500}) || !IsServerError(&APIError{Status: 502}) {
		t.Error("a 5xx is a server error")
	}
	if IsServerError(&APIError{Status: 404}) || IsServerError(nil) {
		t.Error("a 404 or nil is not")
	}
}
