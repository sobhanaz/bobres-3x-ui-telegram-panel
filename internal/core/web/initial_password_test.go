package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
)

const newPassword = "a brand new passphrase"

// issue gives the configured owner an initial password the way `bobres admin
// password` does: the owner never started the bot.
func (f *fixture) issue() (username, password string) {
	f.t.Helper()
	in, err := f.dom.IssueInitialPassword(context.Background(), 0)
	if err != nil {
		f.t.Fatal(err)
	}
	return in.Username, in.Password
}

func (b *browser) passwordLogin(user, password, code string) (int, map[string]any) {
	b.f.t.Helper()
	return b.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": user, "password": password, "code": code})
}

func TestInitialPasswordOpensARestrictedSessionWithoutStart(t *testing.T) {
	f := newFixture(t) // no user exists: the owner never sent /start
	user, pw := f.issue()
	if user != "owner" || len(pw) != 16 {
		t.Fatalf("issued %q %q", user, pw)
	}
	b := f.browser()
	code, me := b.passwordLogin("owner", pw, "")
	if code != http.StatusOK || me["must_change"] != true {
		t.Fatalf("initial login: %d %v", code, me)
	}
	if sess := me["session"].(map[string]any); sess["method"] != "password_initial" {
		t.Fatalf("session: %v", sess)
	}
	if u := me["user"].(map[string]any); u["role"] != "owner" {
		t.Fatalf("user: %v", u)
	}
	if p := me["password"].(map[string]any); p["enabled"] != false || p["initial"] != true || p["username"] != "owner" {
		t.Fatalf("password block: %v", p)
	}
	// An authenticator code sent along is ignored, not required.
	if code, _ := f.browser().passwordLogin("OWNER", pw, "123456"); code != http.StatusOK {
		t.Fatalf("login with a stray code: %d", code)
	}
}

func TestRestrictedSessionReachesOnlyTheChangeEndpoints(t *testing.T) {
	f := newFixture(t)
	_, pw := f.issue()
	b := f.browser()
	b.passwordLogin("owner", pw, "")
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/overview"},
		{http.MethodGet, "/api/v1/users"},
		{http.MethodGet, "/api/v1/me/sessions"},
		{http.MethodDelete, "/api/v1/me/password"},
		{http.MethodPost, "/api/v1/me/sessions/revoke-others"},
		{http.MethodGet, "/api/v1/staff"},
	} {
		code, out := b.do(c.method, c.path, nil)
		if code != http.StatusForbidden || out["error"] != "must_change_password" {
			t.Errorf("%s %s: %d %v", c.method, c.path, code, out)
		}
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/me", nil); code != http.StatusOK {
		t.Fatalf("me: %d", code)
	}
	// Confirming before a new password was chosen.
	if code, out := b.do(http.MethodPost, "/api/v1/me/password/confirm", map[string]string{"code": "123456"}); code != http.StatusBadRequest {
		t.Fatalf("confirm before start: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/auth/logout", nil); code != http.StatusNoContent {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/me", nil); code != http.StatusUnauthorized {
		t.Fatalf("session after logout: %d", code)
	}
}

func TestInitialPasswordFailuresCountAndLock(t *testing.T) {
	f := newFixture(t)
	_, pw := f.issue()
	b := f.browser()
	for i := 0; i < 5; i++ {
		code, out := b.passwordLogin("owner", "not the password!", "")
		if code != http.StatusUnauthorized || out["error"] != "login_failed" || out["message"] != "wrong username, password or code" {
			t.Fatalf("failure %d: %d %v", i+1, code, out)
		}
	}
	creds, err := f.st.CredentialsByUsername(context.Background(), f.st.Conn(), "owner")
	if err != nil || creds.FailedAttempts != 5 || creds.LockedUntil == nil {
		t.Fatalf("after five failures: %+v %v", creds, err)
	}
	// Locked even for the right password.
	if code, _ := b.passwordLogin("owner", pw, ""); code != http.StatusLocked {
		t.Fatalf("locked account logged in: %d", code)
	}
	// A reset from the command line lifts the lock.
	_, pw2 := f.issue()
	if code, _ := b.passwordLogin("owner", pw2, ""); code != http.StatusOK {
		t.Fatalf("login after reset: %d", code)
	}
}

func TestInitialPasswordIsAcceptedOnlyWhileMustChangeAndNoAuthenticator(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := f.staffUser(ownerTG, "owner")
	hash, _ := webauth.HashPassword("a long password")
	secret, _ := webauth.NewTOTPSecret()
	enc, _ := f.api.cfg.Secrets.Encrypt([]byte(secret))
	// A password login of the ordinary kind (not initial), still unconfirmed.
	if err := f.st.SetPendingCredentials(ctx, f.st.Conn(), owner.ID, "boss", hash, enc); err != nil {
		t.Fatal(err)
	}
	b := f.browser()
	if code, _ := b.passwordLogin("boss", "a long password", ""); code != http.StatusUnauthorized {
		t.Fatalf("an unconfirmed ordinary login passed without a code: %d", code)
	}
	if err := f.st.ConfirmCredentials(ctx, f.st.Conn(), owner.ID, 1); err != nil {
		t.Fatal(err)
	}
	if code, _ := b.passwordLogin("boss", "a long password", ""); code != http.StatusUnauthorized {
		t.Fatalf("a confirmed login passed without a code: %d", code)
	}
	// must_change with an authenticator already confirmed (a state the
	// flows do not make): the code is still needed.
	if _, err := f.st.Conn().Exec(ctx, `UPDATE core.staff_credentials SET must_change = true WHERE user_id = $1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := b.passwordLogin("boss", "a long password", ""); code != http.StatusUnauthorized {
		t.Fatalf("must_change + confirmed authenticator passed without a code: %d", code)
	}
	good, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now())+1)
	code, me := b.passwordLogin("boss", "a long password", good)
	if code != http.StatusOK || me["must_change"] != false || me["session"].(map[string]any)["method"] != "password" {
		t.Fatalf("full login: %d %v", code, me)
	}
}

func TestChangingTheInitialPasswordClearsTheFlagAndUpgradesTheSession(t *testing.T) {
	f := newFixture(t)
	_, pw := f.issue()
	b := f.browser()
	b.passwordLogin("owner", pw, "")

	if code, out := b.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "owner", "password": pw}); code != http.StatusBadRequest || out["error"] != "password_reused" {
		t.Fatalf("initial password reused: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "owner", "password": "short"}); code != http.StatusBadRequest {
		t.Fatalf("short password: %d", code)
	}
	code, out := b.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "Boss", "password": newPassword})
	secret, _ := out["secret"].(string)
	if code != http.StatusOK || secret == "" {
		t.Fatalf("start: %d %v", code, out)
	}
	// Still restricted until the first code is confirmed.
	if code, out := b.do(http.MethodGet, "/api/v1/overview", nil); code != http.StatusForbidden || out["error"] != "must_change_password" {
		t.Fatalf("restricted before confirming: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/me/password/confirm", map[string]string{"code": "000000"}); code != http.StatusBadRequest {
		t.Fatalf("wrong confirm code: %d", code)
	}
	c0, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now()))
	code, me := b.do(http.MethodPost, "/api/v1/me/password/confirm", map[string]string{"code": c0})
	if code != http.StatusOK || me["must_change"] != false || me["session"].(map[string]any)["method"] != "password" {
		t.Fatalf("confirm: %d %v", code, me)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/overview", nil); code != http.StatusOK {
		t.Fatalf("overview after the change: %d", code)
	}
	creds, _ := f.st.CredentialsByUsername(context.Background(), f.st.Conn(), "boss")
	if creds == nil || creds.MustChange || creds.TOTPConfirmedAt == nil {
		t.Fatalf("credentials after the change: %+v", creds)
	}
	var method string
	if err := f.st.Conn().QueryRow(context.Background(), `SELECT method FROM core.web_sessions WHERE revoked_at IS NULL`).Scan(&method); err != nil || method != "password" {
		t.Fatalf("stored session method: %q %v", method, err)
	}

	// From now on the ordinary rules: neither password alone nor the initial one.
	anon := f.browser()
	if code, _ := anon.passwordLogin("boss", newPassword, ""); code != http.StatusUnauthorized {
		t.Fatalf("new password without a code: %d", code)
	}
	if code, _ := anon.passwordLogin("boss", pw, c0); code != http.StatusUnauthorized {
		t.Fatalf("the initial password still works: %d", code)
	}
	next, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now())+1)
	if code, me := anon.passwordLogin("boss", newPassword, next); code != http.StatusOK || me["must_change"] != false {
		t.Fatalf("full login: %d %v", code, me)
	}
}

func TestLinkLoginIsFullEvenWithAnInitialPassword(t *testing.T) {
	f := newFixture(t)
	f.issue()
	b := f.browser()
	if code := b.loginWithLink(ownerTG); code != http.StatusOK {
		t.Fatalf("link login: %d", code)
	}
	code, me := b.do(http.MethodGet, "/api/v1/me", nil)
	if code != http.StatusOK || me["must_change"] != false || me["session"].(map[string]any)["method"] != "link" {
		t.Fatalf("me: %d %v", code, me)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/overview", nil); code != http.StatusOK {
		t.Fatalf("overview over a link login: %d", code)
	}
	// Setting a password from the full session ends the initial state too.
	_, out := b.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "owner", "password": newPassword})
	secret, _ := out["secret"].(string)
	c0, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now()))
	if code, _ := b.do(http.MethodPost, "/api/v1/me/password/confirm", map[string]string{"code": c0}); code != http.StatusOK {
		t.Fatalf("confirm: %d", code)
	}
	creds, _ := f.st.CredentialsByUsername(context.Background(), f.st.Conn(), "owner")
	if creds == nil || creds.MustChange {
		t.Fatalf("must_change after a link-session change: %+v", creds)
	}
}

func TestResetEndsSessionsAndStartsOver(t *testing.T) {
	f := newFixture(t)
	_, pw := f.issue()
	restricted := f.browser()
	restricted.passwordLogin("owner", pw, "")
	linked := f.browser()
	linked.loginWithLink(ownerTG)
	if code, _ := linked.do(http.MethodGet, "/api/v1/overview", nil); code != http.StatusOK {
		t.Fatalf("linked session before the reset: %d", code)
	}

	// The owner finished the setup once (a confirmed authenticator) ...
	_, out := linked.do(http.MethodPost, "/api/v1/me/password", map[string]string{"username": "chief", "password": newPassword})
	secret, _ := out["secret"].(string)
	c0, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now()))
	linked.do(http.MethodPost, "/api/v1/me/password/confirm", map[string]string{"code": c0})

	// ... and now lost the phone: bobres admin password again.
	user, pw2 := f.issue()
	if user != "chief" || pw2 == pw {
		t.Fatalf("reset kept the username and changed the password: %q %q", user, pw2)
	}
	for name, b := range map[string]*browser{"restricted": restricted, "linked": linked} {
		if code, _ := b.do(http.MethodGet, "/api/v1/me", nil); code != http.StatusUnauthorized {
			t.Errorf("%s session survived the reset: %d", name, code)
		}
	}
	anon := f.browser()
	if code, _ := anon.passwordLogin("chief", newPassword, c0); code != http.StatusUnauthorized {
		t.Fatalf("the old password works after the reset: %d", code)
	}
	if code, _ := anon.passwordLogin("chief", pw, ""); code != http.StatusUnauthorized {
		t.Fatalf("the first initial password works after the reset: %d", code)
	}
	code, me := anon.passwordLogin("chief", pw2, "")
	if code != http.StatusOK || me["must_change"] != true {
		t.Fatalf("login with the reset password: %d %v", code, me)
	}

	// The audit entry names no password.
	var n int
	var detail string
	if err := f.st.Conn().QueryRow(context.Background(), `SELECT count(*), COALESCE(max(after::text), '') FROM core.audit_log WHERE action = 'dashboard.password.reset_cli'`).Scan(&n, &detail); err != nil || n != 2 {
		t.Fatalf("reset audit entries: %d %v", n, err)
	}
	if strings.Contains(detail, pw) || strings.Contains(detail, pw2) {
		t.Fatalf("a password in the audit entry: %s", detail)
	}
}

func TestIssueInitialPasswordNeedsAnOwnerAndPicksAFreeUsername(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other := f.staffUser(ownerTG+1, "admin")
	hash, _ := webauth.HashPassword("a long password")
	if _, err := f.st.SetInitialCredentials(ctx, f.st.Conn(), other.ID, "owner", hash); err != nil {
		t.Fatal(err)
	}
	user, pw := f.issue()
	if user != "owner-2" {
		t.Fatalf("a taken username: got %q", user)
	}
	if code, _ := f.browser().passwordLogin("owner-2", pw, ""); code != http.StatusOK {
		t.Fatalf("login as %s: %d", user, code)
	}
}
