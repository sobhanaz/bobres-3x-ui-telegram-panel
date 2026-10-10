package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const ownerTG = 7001

type fixture struct {
	t   *testing.T
	st  *store.Store
	dom *domain.Service
	srv *httptest.Server
	api *Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "core"); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := testdb.Truncate(ctx, st.DB(), `core.web_sessions, core.login_links, core.staff_credentials, core.audit_log, core.users`); err != nil {
		t.Fatal(err)
	}
	env, err := bcrypto.NewEnvelope([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	dom := domain.New(st, domain.Config{OwnerTelegramID: ownerTG})
	api := New(Config{Store: st, Domain: dom, Secrets: env})
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return &fixture{t: t, st: st, dom: dom, srv: srv, api: api}
}

// staffUser creates a user with a role.
func (f *fixture) staffUser(tg int64, role string) *store.User {
	f.t.Helper()
	ctx := context.Background()
	u, err := f.st.UpsertUser(ctx, f.st.Conn(), tg, "u", "en", "")
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.st.SetUserRole(ctx, f.st.Conn(), u.ID, role, "active"); err != nil {
		f.t.Fatal(err)
	}
	return u
}

// browser is a client with its own cookie jar and CSRF token.
type browser struct {
	f    *fixture
	c    *http.Client
	csrf string
}

// browser returns a client with its own cookie jar (srv.Client() is shared,
// so only its TLS transport is reused).
func (f *fixture) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{f: f, c: &http.Client{Transport: f.srv.Client().Transport, Jar: jar}}
}

func (b *browser) do(method, path string, body any, hdr ...string) (int, map[string]any) {
	b.f.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, b.f.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if b.csrf != "" {
		req.Header.Set(csrfHeader, b.csrf)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if c, ok := out["csrf"].(string); ok {
		b.csrf = c
	}
	return resp.StatusCode, out
}

func (b *browser) loginWithLink(tg int64) int {
	b.f.t.Helper()
	token, _, err := b.f.dom.CreateLoginLink(context.Background(), tg)
	if err != nil {
		b.f.t.Fatal(err)
	}
	code, _ := b.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": token})
	return code
}

func TestLoginLinkWorksOnceAndOnlyForStaff(t *testing.T) {
	f := newFixture(t)
	f.staffUser(ownerTG, "owner")
	customer := f.staffUser(7002, "user")
	_ = customer

	token, exp, err := f.dom.CreateLoginLink(context.Background(), ownerTG)
	if err != nil || time.Until(exp) > domain.LoginLinkTTL {
		t.Fatalf("link: %v %v", err, exp)
	}
	b := f.browser()
	if code, out := b.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": token}); code != 200 || out["csrf"] == "" {
		t.Fatalf("link login: %d %v", code, out)
	}
	code, me := b.do(http.MethodGet, "/api/v1/me", nil)
	user, _ := me["user"].(map[string]any)
	if code != 200 || user["role"] != "owner" || !strings.Contains(asJSON(me["permissions"]), PermStaffWrite) {
		t.Fatalf("me: %d %v", code, me)
	}
	other := f.browser()
	if code, out := other.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": token}); code != 401 || out["error"] != "link_invalid" {
		t.Fatalf("a link worked twice: %d %v", code, out)
	}
	if _, _, err := f.dom.CreateLoginLink(context.Background(), 7002); err == nil {
		t.Fatal("a customer got a login link")
	}
	if code, _ := other.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("no session: %d", code)
	}
}

func TestCSRFAndLogout(t *testing.T) {
	f := newFixture(t)
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	if code := b.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	csrf := b.csrf
	b.csrf = ""
	if code, out := b.do(http.MethodPost, "/api/v1/auth/logout", nil); code != 403 || out["error"] != "csrf" {
		t.Fatalf("logout without the CSRF token: %d %v", code, out)
	}
	b.csrf = "wrong"
	if code, _ := b.do(http.MethodPost, "/api/v1/auth/logout", nil); code != 403 {
		t.Fatalf("wrong CSRF token: %d", code)
	}
	b.csrf = csrf
	if code, _ := b.do(http.MethodPost, "/api/v1/auth/logout", nil); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
}

func TestPasswordWithAuthenticatorCode(t *testing.T) {
	f := newFixture(t)
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	b.loginWithLink(ownerTG)

	if code, out := b.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "Boss", "password": "short"}); code != 400 {
		t.Fatalf("short password: %d %v", code, out)
	}
	code, out := b.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "Boss", "password": "a long password"})
	secret, _ := out["secret"].(string)
	if code != 200 || secret == "" || !strings.HasPrefix(out["qr"].(string), "data:image/png;base64,") {
		t.Fatalf("start: %d %v", code, out)
	}
	// Not usable before the first code.
	anon := f.browser()
	now := time.Now()
	c0, _ := webauth.TOTPCode(secret, webauth.TOTPStep(now))
	if code, _ := anon.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "a long password", "code": c0}); code != 401 {
		t.Fatalf("login before confirming: %d", code)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/me/password/confirm", map[string]string{"code": "000000"}); code != 400 {
		t.Fatalf("wrong confirm code: %d %v", code, out)
	}
	code, me := b.do(http.MethodPost, "/api/v1/me/password/confirm", map[string]string{"code": c0})
	if code != 200 || asJSON(me["password"]) != `{"enabled":true,"reauth":false,"username":"boss"}` {
		t.Fatalf("confirm: %d %v", code, me)
	}

	// The confirm code cannot log in again (used); the next one can.
	if code, _ := anon.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "a long password", "code": c0}); code != 401 {
		t.Fatalf("a used code logged in: %d", code)
	}
	next, _ := webauth.TOTPCode(secret, webauth.TOTPStep(now)+1)
	if code, out := anon.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "BOSS", "password": "a long password", "code": next}); code != 200 {
		t.Fatalf("password login: %d %v", code, out)
	}
	if code, _ := anon.do(http.MethodGet, "/api/v1/overview", nil); code != 200 {
		t.Fatalf("overview after a password login: %d", code)
	}
}

func TestLockoutAfterFiveFailures(t *testing.T) {
	f := newFixture(t)
	owner := f.staffUser(ownerTG, "owner")
	hash, _ := webauth.HashPassword("a long password")
	secret, _ := webauth.NewTOTPSecret()
	enc, _ := f.api.cfg.Secrets.Encrypt([]byte(secret))
	ctx := context.Background()
	if err := f.st.SetPendingCredentials(ctx, f.st.Conn(), owner.ID, "boss", hash, enc); err != nil {
		t.Fatal(err)
	}
	if err := f.st.ConfirmCredentials(ctx, f.st.Conn(), owner.ID, 1); err != nil {
		t.Fatal(err)
	}
	b := f.browser()
	for i := 0; i < 5; i++ {
		if code, _ := b.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "wrong password!", "code": "123456"}); code != 401 {
			t.Fatalf("failure %d: %d", i+1, code)
		}
	}
	good, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now()))
	if code, out := b.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "a long password", "code": good}); code != http.StatusLocked {
		t.Fatalf("locked account logged in: %d %v", code, out)
	}
	// Unknown usernames answer like wrong passwords.
	if code, out := b.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "nobody", "password": "x", "code": "1"}); code != 401 || out["error"] != "login_failed" {
		t.Fatalf("unknown user: %d %v", code, out)
	}
}

func TestRateLimitAndCrossSite(t *testing.T) {
	f := newFixture(t)
	b := f.browser()
	if code, _ := b.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": "x"}, "Sec-Fetch-Site", "cross-site"); code != 403 {
		t.Fatalf("cross-site login: %d", code)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": "x"}, "Origin", "https://evil.example"); code != 403 {
		t.Fatalf("foreign origin: %d", code)
	}
	last := 0
	for i := 0; i < 25; i++ {
		last, _ = b.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": "x"})
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("no rate limit after 25 tries: %d", last)
	}
}

func TestSessionEndsOnDemotionAndIdle(t *testing.T) {
	f := newFixture(t)
	owner := f.staffUser(ownerTG, "owner")
	f.staffUser(7003, "support")
	ctx := context.Background()

	support := f.browser()
	support.loginWithLink(7003)
	if code, _ := support.do(http.MethodGet, "/api/v1/overview", nil); code != 200 {
		t.Fatalf("support sees the overview: %d", code)
	}
	_, me := support.do(http.MethodGet, "/api/v1/me", nil)
	if strings.Contains(asJSON(me["permissions"]), PermWalletAdjust) {
		t.Fatalf("support may adjust wallets: %v", me["permissions"])
	}

	b := f.browser()
	b.loginWithLink(ownerTG)
	if err := f.st.SetUserRole(ctx, f.st.Conn(), owner.ID, "user", "active"); err != nil {
		t.Fatal(err)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("a demoted user kept the session: %d", code)
	}
	if _, err := f.st.DB().Exec(ctx, `UPDATE core.web_sessions SET last_seen_at = now() - interval '3 hours'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := support.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("an idle session survived: %d", code)
	}
}

func TestPermissions(t *testing.T) {
	for _, c := range []struct {
		role, perm string
		want       bool
	}{
		{"owner", PermStaffWrite, true},
		{"admin", PermWalletAdjust, true},
		{"admin", PermStaffWrite, false},
		{"admin", PermGatewaysWrite, false},
		{"support", PermUsersRead, true},
		{"support", PermUsersWrite, false},
		{"user", PermOverviewRead, false},
		{"admin", "made.up", false},
	} {
		if got := Allowed(c.role, c.perm); got != c.want {
			t.Errorf("%s %s: %v", c.role, c.perm, got)
		}
	}
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
