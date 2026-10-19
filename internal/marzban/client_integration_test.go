//go:build integration

package marzban

// Runs the client against a REAL Marzban panel; see
// internal/provisioner/adapter/marzban_integration_test.go for the variables
// (MARZBAN_TEST_URL, MARZBAN_TEST_USER, MARZBAN_TEST_PASSWORD,
// MARZBAN_TEST_ALLOW_PRIVATE, MARZBAN_TEST_REQUIRE).
//
//	go test -tags integration -run Integration ./internal/marzban/

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

func realClient(t *testing.T, passwordSuffix string) (*Client, context.Context) {
	t.Helper()
	baseURL, user, pass := os.Getenv("MARZBAN_TEST_URL"), os.Getenv("MARZBAN_TEST_USER"), os.Getenv("MARZBAN_TEST_PASSWORD")
	if baseURL == "" || user == "" || pass == "" {
		if os.Getenv("MARZBAN_TEST_REQUIRE") == "1" {
			t.Fatal("MARZBAN_TEST_URL, MARZBAN_TEST_USER and MARZBAN_TEST_PASSWORD not set and MARZBAN_TEST_REQUIRE=1")
		}
		t.Skip("MARZBAN_TEST_URL, MARZBAN_TEST_USER and MARZBAN_TEST_PASSWORD not set")
	}
	var opts []xui.Option
	if os.Getenv("MARZBAN_TEST_ALLOW_PRIVATE") == "1" {
		opts = append(opts, xui.AllowPrivateAddresses())
	}
	c, err := New(baseURL, user, pass+passwordSuffix, opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return c, ctx
}

// anInbound picks one inbound of the panel for the users these tests make.
func anInbound(t *testing.T, ctx context.Context, c *Client) (protocol, tag string) {
	t.Helper()
	by, err := c.Inbounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for p, list := range by {
		if len(list) > 0 {
			return p, list[0].Tag
		}
	}
	t.Fatal("the panel has no inbound")
	return "", ""
}

func newBody(name, protocol, tag string) map[string]any {
	return map[string]any{
		"username": name, "proxies": map[string]any{protocol: map[string]any{}},
		"inbounds": map[string][]string{protocol: {tag}}, "status": StatusActive,
	}
}

// The username rule is the panel's, checked name by name against the panel
// itself (not through CreateUser, which would refuse locally first). BOBRES
// is stricter in one way only: at most 32 characters.
func TestIntegrationUsernameRule(t *testing.T) {
	c, ctx := realClient(t, "")
	protocol, tag := anInbound(t, ctx, c)
	names := []string{
		// accepted
		"u10-abcd1234", "u42-abcd1234", "u123456789-abcd1234", "abc", "abc-def", "ABC_def.ghi", "user@example.com",
		"a_b", "abc@x.y", strings.Repeat("a", 32),
		// refused: the run of letters, digits and underscores the name starts with is under 3 or over 32
		"u1-abcd1234", "u9-abcd1234", "ab", "a-bcdef", "x@y.z", "-abc", ".abc", "u-100123-abc", strings.Repeat("a", 33),
		// refused: characters
		"abc def", "abc/def", "abc%41", "über", "abcé", "abc+d", "abc,def",
	}
	for _, name := range names {
		local := ValidateUsername(name) == nil
		err := c.do(ctx, "POST", "/api/user", newBody(name, protocol, tag), nil)
		if err == nil {
			t.Cleanup(func() { _ = c.do(context.Background(), "DELETE", "/api/user/"+url.PathEscape(name), nil, nil) })
		}
		var ae *APIError
		panelOK := err == nil || IsConflict(err) // a name left by an earlier run exists, so it is a valid one
		switch {
		case panelOK && !local:
			t.Errorf("%q: the panel accepts it, ValidateUsername refuses it", name)
		case !panelOK && local:
			t.Errorf("%q: ValidateUsername accepts it, the panel refuses it: %v", name, err)
		case !panelOK && (!errors.As(err, &ae) || ae.Status != 422):
			t.Errorf("%q: the panel answered %v, want a 422 refusal", name, err)
		}
	}
}

// An expired or invalid token is answered with 401 and the client signs in
// again by itself.
func TestIntegrationTokenRelogin(t *testing.T) {
	c, ctx := realClient(t, "")
	if _, err := c.Version(ctx); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"not.a.valid.jwt", "x", ""} {
		c.mu.Lock()
		c.token = token
		c.mu.Unlock()
		if v, err := c.Version(ctx); err != nil || v == "" {
			t.Fatalf("with the cached token %q: %q %v", token, v, err)
		}
	}
	// A refused sign-in is reported as such, with the panel's words.
	bad, ctx2 := realClient(t, "-wrong")
	_, err := bad.Version(ctx2)
	if !IsUnauthorized(err) || !strings.Contains(err.Error(), "Incorrect username or password") {
		t.Fatalf("a wrong password: %v", err)
	}
}

// A token that has really expired is refused with 401 and the client signs in
// again by itself. Needs a panel started with JWT_ACCESS_TOKEN_EXPIRE_MINUTES=1
// (CI does) and MARZBAN_TEST_SHORT_TOKENS=1; it waits for the minute to pass.
func TestIntegrationTokenExpiry(t *testing.T) {
	if os.Getenv("MARZBAN_TEST_SHORT_TOKENS") != "1" {
		if os.Getenv("MARZBAN_TEST_REQUIRE") == "1" {
			t.Fatal("MARZBAN_TEST_SHORT_TOKENS=1 not set and MARZBAN_TEST_REQUIRE=1")
		}
		t.Skip("MARZBAN_TEST_SHORT_TOKENS=1 not set (the panel needs JWT_ACCESS_TOKEN_EXPIRE_MINUTES=1)")
	}
	c, ctx := realClient(t, "")
	if _, err := c.Version(ctx); err != nil {
		t.Fatal(err)
	}
	old := c.current()
	if old == "" {
		t.Fatal("no token after the first call")
	}
	time.Sleep(63 * time.Second)
	code, body, err := c.send(ctx, http.MethodGet, "/api/system", "", old, nil)
	if err != nil || code != http.StatusUnauthorized {
		t.Fatalf("the panel answered the expired token with HTTP %d (%v), want 401: %.100s", code, err, body)
	}
	if got := detailOf(body); got != "Could not validate credentials" {
		t.Fatalf("detail of the refusal: %q", got)
	}
	if v, err := c.Version(ctx); err != nil || v == "" {
		t.Fatalf("a call with the expired token: %q %v", v, err)
	}
	if c.current() == old {
		t.Fatal("the client kept the expired token")
	}
}

// What the panel says when it refuses, in the shapes it says it.
func TestIntegrationRefusals(t *testing.T) {
	c, ctx := realClient(t, "")
	protocol, tag := anInbound(t, ctx, c)
	name := fmt.Sprintf("bobres-it-%d", time.Now().UnixNano()/1000)
	if err := c.CreateUser(ctx, newBody(name, protocol, tag)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.DeleteUser(context.Background(), name) })

	// A missing user: 404 "User not found", on every route that names one.
	for what, err := range map[string]error{
		"get":    func() error { _, e := c.GetUser(ctx, name+"x"); return e }(),
		"put":    c.ModifyUser(ctx, name+"x", map[string]any{"note": "n"}),
		"delete": c.DeleteUser(ctx, name+"x"),
		"reset":  c.ResetUsage(ctx, name+"x"),
	} {
		if !IsNotFound(err) || !strings.Contains(err.Error(), "User not found") {
			t.Errorf("%s of a missing user: %v", what, err)
		}
	}
	// The same username again: 409.
	if err := c.CreateUser(ctx, newBody(name, protocol, tag)); !IsConflict(err) || !strings.Contains(err.Error(), "User already exists") {
		t.Errorf("duplicate: %v", err)
	}
	// A body the panel validates: 422 with the detail as an object of field to
	// message, which the error must carry.
	err := c.ModifyUser(ctx, name, map[string]any{"status": StatusOnHold})
	if err == nil || !strings.Contains(err.Error(), "http 422") || !strings.Contains(err.Error(), "status: User cannot be on hold") {
		t.Errorf("on hold without a duration: %v", err)
	}
	err = c.ModifyUser(ctx, name, map[string]any{"data_limit": -1})
	if err == nil || !strings.Contains(err.Error(), "http 422") || !strings.Contains(err.Error(), "data_limit:") {
		t.Errorf("a negative quota: %v", err)
	}
	bodyBad := newBody(name+"2", protocol, "no such inbound tag")
	err = c.CreateUser(ctx, bodyBad)
	if err == nil || !strings.Contains(err.Error(), "http 422") || !strings.Contains(err.Error(), "inbounds:") {
		t.Errorf("an unknown inbound tag: %v", err)
	}
	// A protocol the panel does not serve: 400.
	by, _ := c.Inbounds(ctx)
	for _, p := range []string{"vmess", "vless", "trojan", "shadowsocks"} {
		if len(by[p]) > 0 {
			continue
		}
		err := c.CreateUser(ctx, newBody(name+"3", p, "x"))
		if err == nil || !strings.Contains(err.Error(), "http 400") || !strings.Contains(err.Error(), "disabled on your server") {
			t.Errorf("a protocol the panel does not serve (%s): %v", p, err)
		}
		break
	}
	// Usernames are case-sensitive on the panel: the model declares the column
	// NOCASE but the table the migrations make does not have it, so "Abc" is a
	// user of its own beside "abc" (the fake does the same).
	upper := strings.ToUpper(name)
	if _, err := c.GetUser(ctx, upper); !IsNotFound(err) {
		t.Errorf("a lookup in another case: %v, want not found", err)
	}
	if err := c.CreateUser(ctx, newBody(upper, protocol, tag)); err != nil {
		t.Errorf("a name that differs in case only: %v", err)
	} else {
		t.Cleanup(func() { _ = c.DeleteUser(context.Background(), upper) })
	}
	// The user is what the panel says it is.
	u, err := c.GetUser(ctx, name)
	if err != nil || u.Username != name || u.Status != StatusActive || !u.HasOwner() || u.SubscriptionURL == "" {
		t.Fatalf("user: %+v %v", u, err)
	}
}
