package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
)

// fakeApprovalBot stands in for the bot's approval endpoints.
type fakeApprovalBot struct {
	mu      sync.Mutex
	prompts []domain.LoginPrompt
	notices []domain.ApprovalNotice
	resolve []domain.LoginResolution
	down    bool
	blocked map[int64]bool
	nextMsg int64
}

func (b *fakeApprovalBot) SendLoginApproval(_ context.Context, p domain.LoginPrompt) ([]domain.PromptResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.down {
		return nil, errors.New("bot down")
	}
	b.prompts = append(b.prompts, p)
	var out []domain.PromptResult
	for _, t := range p.Targets {
		if b.blocked[t.TelegramID] {
			out = append(out, domain.PromptResult{TelegramID: t.TelegramID, Error: domain.DeliverBlocked})
			continue
		}
		b.nextMsg++
		out = append(out, domain.PromptResult{TelegramID: t.TelegramID, MessageID: 5000 + b.nextMsg})
	}
	return out, nil
}

func (b *fakeApprovalBot) ResolveLoginApproval(_ context.Context, r domain.LoginResolution) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.down {
		return errors.New("bot down")
	}
	b.resolve = append(b.resolve, r)
	return nil
}

func (b *fakeApprovalBot) NoticeLoginApproval(_ context.Context, n domain.ApprovalNotice) ([]domain.PromptResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.down {
		return nil, errors.New("bot down")
	}
	b.notices = append(b.notices, n)
	var out []domain.PromptResult
	for _, t := range n.Targets {
		if b.blocked[t.TelegramID] {
			out = append(out, domain.PromptResult{TelegramID: t.TelegramID, Error: domain.DeliverBlocked})
			continue
		}
		out = append(out, domain.PromptResult{TelegramID: t.TelegramID})
	}
	return out, nil
}

func (b *fakeApprovalBot) last(t *testing.T) domain.LoginPrompt {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.prompts) == 0 {
		t.Fatal("the bot was asked for no prompt")
	}
	return b.prompts[len(b.prompts)-1]
}

func (b *fakeApprovalBot) promptCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.prompts)
}

// approvalFixture is newFixture for a test of the approval: the owner's switches are settings, which newFixture leaves alone, so they are
// cleared before the test and after it, whatever it did to them (the tests that follow sign in with a link and expect a session).
func approvalFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	clear := func() {
		if _, err := f.st.DB().Exec(context.Background(), `DELETE FROM core.settings WHERE key LIKE 'security.%'`); err != nil {
			t.Error(err)
		}
	}
	clear()
	t.Cleanup(clear)
	return f
}

// approvalSetup has an owner and an admin, a bot that works, and the approval of sign-ins on (turned on as the owner would, so the owner
// was told first).
func approvalSetup(t *testing.T) (*fixture, *fakeApprovalBot, *store.User, *store.User) {
	t.Helper()
	f := approvalFixture(t)
	bot := &fakeApprovalBot{blocked: map[int64]bool{}}
	f.dom.SetLoginApprovalBot(bot)
	own := f.staffUser(ownerTG, "owner")
	adm := f.staffUser(7010, "admin")
	if err := f.dom.SetLoginApproval(context.Background(), own, "dashboard", true, nil); err != nil {
		t.Fatalf("turn the approval on: %v", err)
	}
	return f, bot, own, adm
}

func (f *fixture) cookie(b *browser, name string) string {
	f.t.Helper()
	u, err := url.Parse(f.srv.URL)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// reply is what a test needs of a response whose body is read and closed: the status and the cookies it set.
type reply struct {
	StatusCode int
	Header     http.Header
	cookies    []*http.Cookie
}

func (r reply) Cookies() []*http.Cookie { return r.cookies }

// rawJSON does a request and returns its status and cookies with the JSON body.
func (b *browser) rawJSON(method, path string, body any) (reply, map[string]any) {
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
	resp, err := b.c.Do(req)
	if err != nil {
		b.f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return reply{StatusCode: resp.StatusCode, Header: resp.Header, cookies: resp.Cookies()}, out
}

const (
	signInPendingPath = "/api/v1/auth/pending"
	sessionName       = "__Host-bobres_session"
)

// answer is a tap of the person with that Telegram id on the buttons of the last prompt.
func (f *fixture) answer(bot *fakeApprovalBot, tg int64, approve bool) *domain.ApprovalDecision {
	f.t.Helper()
	p := bot.last(f.t)
	nonce := ""
	for _, t := range p.Targets {
		if t.TelegramID == tg {
			nonce = t.Nonce
		}
	}
	d, err := f.dom.DecideLoginApproval(context.Background(), tg, p.ApprovalID, nonce, approve)
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

// With the approval off (the default) a sign-in opens a session at once and nothing else changes.
func TestApprovalOffChangesNothing(t *testing.T) {
	f := approvalFixture(t)
	bot := &fakeApprovalBot{}
	f.dom.SetLoginApprovalBot(bot)
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	resp, out := b.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, ownerTG)})
	if resp.StatusCode != 200 || out["csrf"] == "" || out["status"] != nil {
		t.Fatalf("link login: %d %v", resp.StatusCode, out)
	}
	if f.cookie(b, sessionName) == "" || f.cookie(b, pendingCookie) != "" {
		t.Fatal("a session cookie and no pending cookie were expected")
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/me", nil); code != 200 {
		t.Fatalf("me: %d", code)
	}
	if bot.promptCount() != 0 {
		t.Fatal("the bot was asked about a sign-in that needs no approval")
	}
	// The poll of a browser that waits for nothing says so.
	if code, out := f.browser().do(http.MethodPost, signInPendingPath, nil); code != 200 || out["status"] != domain.PollNone {
		t.Fatalf("poll without a cookie: %d %v", code, out)
	}
}

func mustLink(t *testing.T, f *fixture, tg int64) string {
	t.Helper()
	token, _, err := f.dom.CreateLoginLink(context.Background(), tg)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// The whole way with a login link: 202 and a pending cookie that is no session, the polls say pending, an approval in Telegram lets the
// next poll collect the session once, and the entries are in the audit log.
func TestLinkSignInWaitsForApproval(t *testing.T) {
	f, bot, _, adm := approvalSetup(t)
	b := f.browser()
	resp, out := b.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)})
	if resp.StatusCode != http.StatusAccepted || out["status"] != "pending" || out["delivered"] != true || out["ttl_s"] != float64(300) ||
		out["command"] != "sudo bobres admin approval off" || asJSON(out["approvers"]) != `["self","owner"]` {
		t.Fatalf("link login: %d %v", resp.StatusCode, out)
	}
	if out["csrf"] != nil || out["user"] != nil {
		t.Fatalf("a pending sign-in answered like a session: %v", out)
	}
	if f.cookie(b, sessionName) != "" {
		t.Fatal("a session cookie was set before the approval")
	}
	ticket := f.cookie(b, pendingCookie)
	if ticket == "" {
		t.Fatal("no pending cookie")
	}
	var set *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == pendingCookie {
			set = c
		}
	}
	if set == nil || !set.Secure || !set.HttpOnly || set.SameSite != http.SameSiteStrictMode || set.Path != "/" || set.MaxAge <= 0 {
		t.Fatalf("pending cookie: %+v", set)
	}

	// Nothing opens with it: it is no session, whichever way it is presented.
	if code, _ := b.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("me before the approval: %d", code)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/overview", nil); code != 401 {
		t.Fatalf("overview before the approval: %d", code)
	}
	req, _ := http.NewRequest(http.MethodGet, f.srv.URL+"/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionName, Value: ticket, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	r2, err := (&http.Client{Transport: f.srv.Client().Transport}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != 401 {
		t.Fatalf("the pending cookie as a session cookie: %d", r2.StatusCode)
	}

	prompt := bot.last(t)
	if prompt.Account.TelegramID != 7010 || prompt.Method != "link" || prompt.IP != "127.0.0.1" || prompt.Browser == "" || len(prompt.Targets) != 2 {
		t.Fatalf("prompt: %+v", prompt)
	}
	if code, out := b.do(http.MethodPost, signInPendingPath, nil); code != 200 || out["status"] != "pending" || out["delivered"] != true {
		t.Fatalf("poll: %d %v", code, out)
	}

	if d := f.answer(bot, 7010, true); d.Outcome != domain.OutcomeApproved {
		t.Fatalf("approve: %+v", d)
	}
	resp, out = b.rawJSON(http.MethodPost, signInPendingPath, nil)
	user, _ := out["user"].(map[string]any)
	if resp.StatusCode != 200 || out["status"] != "approved" || out["csrf"] == "" || user["role"] != "admin" || user["telegram_id"] != float64(7010) {
		t.Fatalf("the approved poll: %d %v", resp.StatusCode, out)
	}
	if f.cookie(b, sessionName) == "" || f.cookie(b, pendingCookie) != "" {
		t.Fatal("the session cookie was not set, or the pending cookie was not cleared")
	}
	if code, me := b.do(http.MethodGet, "/api/v1/me", nil); code != 200 || me["user"].(map[string]any)["id"] != adm.ID {
		t.Fatalf("me after the approval: %d %v", code, me)
	}
	// The sign-in is spent: the same cookie never makes a second session.
	if code, out := b.do(http.MethodPost, signInPendingPath, nil); code != 200 || out["status"] != domain.PollNone {
		t.Fatalf("a second poll: %d %v", code, out)
	}
	other := f.browser()
	other.c.Jar.SetCookies(mustURL(t, f.srv.URL), []*http.Cookie{{Name: pendingCookie, Value: ticket, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}})
	if code, out := other.do(http.MethodPost, signInPendingPath, nil); code != 200 || out["status"] != domain.PollNone || out["csrf"] != nil {
		t.Fatalf("the ticket used again elsewhere: %d %v", code, out)
	}

	// The audit log tells the story: asked, approved by the person themself, signed in with the approval's number.
	if e := lastAudit(t, b, "action=dashboard.login_requested"); e["entity_id"] != adm.ID {
		t.Fatalf("requested: %v", e)
	}
	ap := lastAudit(t, b, "action=dashboard.login_approved")
	if ap["entity_id"] != adm.ID || ap["actor"].(map[string]any)["telegram_id"] != float64(7010) || ap["source"] != "bot" && ap["source"] != "system" {
		t.Logf("approved entry: %v", ap)
	}
	login := lastAudit(t, b, "action=dashboard.login")
	if after := asJSON(login["after"]); !strings.Contains(after, `"approval"`) || !strings.Contains(after, `"approved_by":"self"`) {
		t.Fatalf("login entry: %v", login)
	}
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// A password with its authenticator code waits for the approval too, and the session it becomes is a password session.
func TestPasswordSignInWaitsForApproval(t *testing.T) {
	f := approvalFixture(t)
	bot := &fakeApprovalBot{}
	f.dom.SetLoginApprovalBot(bot)
	own := f.staffUser(ownerTG, "owner")
	setup := f.browser()
	if code := setup.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	secret := confirmPassword(t, setup, "boss")
	if err := f.dom.SetLoginApproval(context.Background(), own, "dashboard", true, nil); err != nil {
		t.Fatal(err)
	}

	b := f.browser()
	code := webauth.TOTPStep(time.Now()) + 1
	good, _ := webauth.TOTPCode(secret, code)
	resp, out := b.rawJSON(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "a long password", "code": good})
	if resp.StatusCode != http.StatusAccepted || out["status"] != "pending" || out["csrf"] != nil {
		t.Fatalf("password login: %d %v", resp.StatusCode, out)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/me", nil); code != 401 {
		t.Fatalf("me before the approval: %d", code)
	}
	if p := bot.last(t); p.Method != "password" || len(p.Targets) != 1 || p.Targets[0].TelegramID != ownerTG {
		t.Fatalf("prompt: %+v", p)
	}
	// A wrong code still fails like before, and does not ask anyone.
	asked := bot.promptCount()
	wrong := f.browser()
	if code, out := wrong.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "a long password", "code": "000000"}); code != 401 || out["error"] != "login_failed" {
		t.Fatalf("wrong code: %d %v", code, out)
	}
	if bot.promptCount() != asked {
		t.Fatal("a failed password login asked for an approval")
	}

	f.answer(bot, ownerTG, true)
	resp, out = b.rawJSON(http.MethodPost, signInPendingPath, nil)
	if resp.StatusCode != 200 || out["status"] != "approved" || out["session"].(map[string]any)["method"] != "password" {
		t.Fatalf("approved poll: %d %v", resp.StatusCode, out)
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/overview", nil); code != 200 {
		t.Fatalf("overview after the approval: %d", code)
	}
}

// Deny, the clock and the person at the browser giving up each end the wait without a session.
func TestSignInEndsWithoutASession(t *testing.T) {
	f, bot, _, _ := approvalSetup(t)

	// Denied.
	b := f.browser()
	if resp, _ := b.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)}); resp.StatusCode != 202 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	if d := f.answer(bot, 7010, false); d.Outcome != domain.OutcomeDenied {
		t.Fatalf("deny: %+v", d)
	}
	if code, out := b.do(http.MethodPost, signInPendingPath, nil); code != 200 || out["status"] != "denied" || out["csrf"] != nil {
		t.Fatalf("poll after a deny: %d %v", code, out)
	}
	if f.cookie(b, pendingCookie) != "" || f.cookie(b, sessionName) != "" {
		t.Fatal("cookies after a deny")
	}

	// Expired.
	b = f.browser()
	if resp, _ := b.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)}); resp.StatusCode != 202 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	if _, err := f.st.DB().Exec(context.Background(), `UPDATE core.login_approvals
		SET created_at = created_at - interval '10 minutes', expires_at = now() - interval '1 second' WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
	if code, out := b.do(http.MethodPost, signInPendingPath, nil); code != 200 || out["status"] != "expired" || out["csrf"] != nil {
		t.Fatalf("poll after the time was up: %d %v", code, out)
	}
	if d := f.answer(bot, 7010, true); d.Outcome != domain.OutcomeExpired {
		t.Fatalf("a late approval: %+v", d)
	}

	// Given up.
	b = f.browser()
	if resp, _ := b.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)}); resp.StatusCode != 202 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	if code, _ := b.do(http.MethodDelete, signInPendingPath, nil); code != 204 {
		t.Fatalf("cancel: %d", code)
	}
	if f.cookie(b, pendingCookie) != "" {
		t.Fatal("pending cookie after cancel")
	}
	if d := f.answer(bot, 7010, true); d.Outcome != domain.OutcomeCancelled {
		t.Fatalf("approval of a cancelled sign-in: %+v", d)
	}
	// Cancelling again, or with no cookie, is harmless.
	if code, _ := b.do(http.MethodDelete, signInPendingPath, nil); code != 204 {
		t.Fatalf("second cancel: %d", code)
	}
	for _, action := range []string{"dashboard.login_denied", "dashboard.login_expired", "dashboard.login_cancelled"} {
		if e := lastAudit(t, f.ownerBrowser(t), "action="+action); e == nil {
			t.Fatalf("no %s entry", action)
		}
	}
}

// ownerBrowser signs the owner in with the approval switched off for a moment (a test reading the audit log).
func (f *fixture) ownerBrowser(t *testing.T) *browser {
	t.Helper()
	ctx := context.Background()
	on, _ := f.dom.LoginApprovalOn(ctx)
	if on {
		if _, err := f.st.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = 'security.login_approval'`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := f.st.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('security.login_approval', '"true"')`); err != nil {
				t.Fatal(err)
			}
		}()
	}
	b := f.browser()
	if code := b.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("owner login: %d", code)
	}
	return b
}

// With nobody to ask, the sign-in fails at once and says how to get in.
func TestSignInFailsFastWhenNobodyCanBeAsked(t *testing.T) {
	f, bot, _, _ := approvalSetup(t)

	bot.down = true
	b := f.browser()
	resp, out := b.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)})
	if resp.StatusCode != http.StatusServiceUnavailable || out["error"] != "approval_unavailable" || out["reason"] != domain.DeliverBotDown ||
		out["command"] != "sudo bobres admin approval off" || !strings.Contains(out["message"].(string), "sudo bobres admin approval off") {
		t.Fatalf("bot down: %d %v", resp.StatusCode, out)
	}
	if f.cookie(b, pendingCookie) != "" || f.cookie(b, sessionName) != "" {
		t.Fatal("cookies after a sign-in that cannot be approved")
	}
	bot.down = false

	// Everyone asked blocked the bot.
	bot.blocked[7010], bot.blocked[ownerTG] = true, true
	resp, out = b.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)})
	if resp.StatusCode != http.StatusServiceUnavailable || out["reason"] != domain.DeliverBlocked {
		t.Fatalf("blocked: %d %v", resp.StatusCode, out)
	}
	bot.blocked = map[int64]bool{}

	// No bot at all.
	f.dom.SetLoginApprovalBot(nil)
	resp, out = b.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)})
	if resp.StatusCode != http.StatusServiceUnavailable || out["reason"] != domain.DeliverNoBot {
		t.Fatalf("no bot: %d %v", resp.StatusCode, out)
	}
	// The login link was spent by each try, as any login link is.
	if code, out := b.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": "x"}); code != 401 || out["error"] != "link_invalid" {
		t.Fatalf("junk link: %d %v", code, out)
	}
}

// An account that asks too often is told to wait, with a Retry-After.
func TestSignInApprovalRateLimit(t *testing.T) {
	f, _, _, _ := approvalSetup(t)
	var last reply
	var out map[string]any
	for i := 0; i < 4; i++ {
		last, out = f.browser().rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)})
		if i < 3 && last.StatusCode != 202 {
			t.Fatalf("sign-in %d: %d %v", i+1, last.StatusCode, out)
		}
	}
	if last.StatusCode != http.StatusTooManyRequests || out["error"] != "approval_rate_limited" || last.Header.Get("Retry-After") == "" {
		t.Fatalf("the fourth waiting sign-in: %d %v %q", last.StatusCode, out, last.Header.Get("Retry-After"))
	}
}

// Polling is bounded per address.
func TestPendingPollRateLimit(t *testing.T) {
	f := newFixture(t)
	b := f.browser()
	limited := 0
	for i := 0; i < 130; i++ {
		if code, _ := b.do(http.MethodPost, signInPendingPath, nil); code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("130 polls in a moment were all answered")
	}
}

// The poll and the cancel are for the login page's own origin only.
func TestPendingEndpointsRefuseOtherSites(t *testing.T) {
	f, _, _, _ := approvalSetup(t)
	b := f.browser()
	if code, out := b.do(http.MethodPost, signInPendingPath, nil, "Sec-Fetch-Site", "cross-site"); code != 403 || out["error"] != "origin" {
		t.Fatalf("poll: %d %v", code, out)
	}
	if code, out := b.do(http.MethodDelete, signInPendingPath, nil, "Sec-Fetch-Site", "cross-site"); code != 403 || out["error"] != "origin" {
		t.Fatalf("cancel: %d %v", code, out)
	}
}

// An account that lost its access between the approval and the poll gets no session.
func TestApprovedSignInOfAnAccountThatWasRemoved(t *testing.T) {
	f, bot, _, adm := approvalSetup(t)
	b := f.browser()
	if resp, _ := b.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)}); resp.StatusCode != 202 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	if d := f.answer(bot, 7010, true); d.Outcome != domain.OutcomeApproved {
		t.Fatal(d)
	}
	if _, err := f.st.DB().Exec(context.Background(), `UPDATE core.users SET status = 'banned' WHERE id = $1`, adm.ID); err != nil {
		t.Fatal(err)
	}
	if code, out := b.do(http.MethodPost, signInPendingPath, nil); code != 403 || out["error"] != "forbidden" || out["csrf"] != nil {
		t.Fatalf("poll: %d %v", code, out)
	}
	if f.cookie(b, sessionName) != "" {
		t.Fatal("a session for an account that was banned")
	}
}

// Settings > Sign-in security: the state for owner and admin, the switch for the owner alone, with a confirmation to turn it on and a bot that can
// reach the owner; and the generic settings form and the bot's /set do not carry it.
func TestSecuritySettings(t *testing.T) {
	f := approvalFixture(t)
	bot := &fakeApprovalBot{blocked: map[int64]bool{}}
	f.dom.SetLoginApprovalBot(bot)
	f.staffUser(ownerTG, "owner")
	f.staffUser(7010, "admin")
	f.staffUser(7011, "support")
	ob, ab, sb := f.browser(), f.browser(), f.browser()
	for tg, b := range map[int64]*browser{ownerTG: ob, 7010: ab, 7011: sb} {
		if code := b.loginWithLink(tg); code != 200 {
			t.Fatalf("login %d: %d", tg, code)
		}
	}

	code, out := ob.do(http.MethodGet, "/api/v1/security", nil)
	la, _ := out["login_approval"].(map[string]any)
	if code != 200 || la["enabled"] != false || la["notify_owner"] != true || la["bot_ready"] != true || la["ttl_s"] != float64(300) ||
		la["waiting"] != float64(0) || out["can_change"] != true {
		t.Fatalf("owner reads: %d %v", code, out)
	}
	if code, out := ab.do(http.MethodGet, "/api/v1/security", nil); code != 200 || out["can_change"] != false {
		t.Fatalf("admin reads: %d %v", code, out)
	}
	if code, _ := sb.do(http.MethodGet, "/api/v1/security", nil); code != 403 {
		t.Fatalf("support reads: %d", code)
	}
	body := map[string]any{"enabled": true, "acknowledge": true}
	for name, b := range map[string]*browser{"admin": ab, "support": sb} {
		if code, _ := b.do(http.MethodPut, "/api/v1/security/login-approval", body); code != 403 {
			t.Fatalf("%s turned it on: %d", name, code)
		}
	}
	// The owner must say they read what it does, and say what they want.
	if code, out := ob.do(http.MethodPut, "/api/v1/security/login-approval", map[string]any{"enabled": true}); code != 400 || out["error"] != "acknowledge_required" {
		t.Fatalf("without the confirmation: %d %v", code, out)
	}
	if code, _ := ob.do(http.MethodPut, "/api/v1/security/login-approval", map[string]any{"acknowledge": true}); code != 400 {
		t.Fatalf("without a choice: %d", code)
	}
	csrf := ob.csrf
	ob.csrf = ""
	if code, out := ob.do(http.MethodPut, "/api/v1/security/login-approval", body); code != 403 || out["error"] != "csrf" {
		t.Fatalf("without the CSRF token: %d %v", code, out)
	}
	ob.csrf = csrf
	on := func() bool {
		t.Helper()
		v, err := f.dom.LoginApprovalOn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if on() {
		t.Fatal("it was turned on by refused requests")
	}

	// The bot cannot reach the owner: it stays off and says why.
	bot.blocked[ownerTG] = true
	code, out = ob.do(http.MethodPut, "/api/v1/security/login-approval", body)
	if code != http.StatusConflict || out["error"] != "approval_unavailable" || out["reason"] != domain.DeliverBlocked || out["command"] == nil || on() {
		t.Fatalf("owner unreachable: %d %v", code, out)
	}
	bot.blocked = map[int64]bool{}
	bot.down = true
	if code, out = ob.do(http.MethodPut, "/api/v1/security/login-approval", body); code != http.StatusConflict || out["reason"] != domain.DeliverBotDown || on() {
		t.Fatalf("bot down: %d %v", code, out)
	}
	bot.down = false

	// Turned on: the state is answered, the owner was told, and the change is audited.
	bot.notices = nil // the attempt that failed for a blocked owner was offered to the bot
	code, out = ob.do(http.MethodPut, "/api/v1/security/login-approval", map[string]any{"enabled": true, "acknowledge": true, "notify_owner": false})
	la, _ = out["login_approval"].(map[string]any)
	if code != 200 || la["enabled"] != true || la["notify_owner"] != false || !on() {
		t.Fatalf("turn on: %d %v", code, out)
	}
	if len(bot.notices) != 1 || bot.notices[0].Kind != domain.NoticeEnabled || bot.notices[0].Source != "dashboard" {
		t.Fatalf("notices: %+v", bot.notices)
	}
	if e := lastAudit(t, ob, "action=setting.set&entity_id=security.login_approval"); asJSON(e["after"]) != `{"value":"true"}` {
		t.Fatalf("audit: %v", e)
	}

	// A waiting sign-in ends when the owner turns it off, and the owner is told.
	other := f.browser()
	if resp, _ := other.rawJSON(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": mustLink(t, f, 7010)}); resp.StatusCode != 202 {
		t.Fatalf("a sign-in while it is on: %d", resp.StatusCode)
	}
	if _, out := ob.do(http.MethodGet, "/api/v1/security", nil); out["login_approval"].(map[string]any)["waiting"] != float64(1) {
		t.Fatalf("waiting: %v", out)
	}
	if code, out := ob.do(http.MethodPut, "/api/v1/security/login-approval", map[string]any{"enabled": false}); code != 200 || on() || out["login_approval"].(map[string]any)["enabled"] != false {
		t.Fatalf("turn off: %d %v", code, out)
	}
	if code, out := other.do(http.MethodPost, signInPendingPath, nil); code != 200 || out["status"] != "cancelled" {
		t.Fatalf("the waiting sign-in: %d %v", code, out)
	}
	if last := bot.notices[len(bot.notices)-1]; last.Kind != domain.NoticeDisabled || last.Source != "dashboard" || last.By == nil {
		t.Fatalf("notices: %+v", bot.notices)
	}

	// The generic settings form and the bot's /set do not carry the switches.
	if _, out := ob.do(http.MethodGet, "/api/v1/settings", nil); settingByKey(out, domain.SettingLoginApproval) != nil || strings.Contains(asJSON(out["groups"]), "security") {
		t.Fatalf("settings page lists them: %v", out["groups"])
	}
	if code, out := ob.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{domain.SettingLoginApproval: "true"}}); code != 400 || on() {
		t.Fatalf("generic settings: %d %v", code, out)
	}
}

// An owner with a password login gives the authenticator code to change the switch once their session is not fresh; a wrong one counts as
// any wrong code does.
func TestSecuritySwitchAsksForTheAuthenticatorCode(t *testing.T) {
	f := approvalFixture(t)
	bot := &fakeApprovalBot{}
	f.dom.SetLoginApprovalBot(bot)
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	if code := b.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	secret := confirmPassword(t, b, "boss")
	// Eleven minutes on, a link session is no longer fresh proof.
	f.api.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	body := map[string]any{"enabled": true, "acknowledge": true}
	if code, out := b.do(http.MethodPut, "/api/v1/security/login-approval", body); code != 403 || out["error"] != "code_required" {
		t.Fatalf("without the code: %d %v", code, out)
	}
	body["code"] = "000000"
	if code, out := b.do(http.MethodPut, "/api/v1/security/login-approval", body); code != 400 || out["error"] != "code_wrong" {
		t.Fatalf("a wrong code: %d %v", code, out)
	}
	if on, _ := f.dom.LoginApprovalOn(context.Background()); on {
		t.Fatal("turned on by a wrong code")
	}
	// The server's clock was moved on, and its window of codes with it.
	good, _ := webauth.TOTPCode(secret, webauth.TOTPStep(f.api.now()))
	body["code"] = good
	if code, out := b.do(http.MethodPut, "/api/v1/security/login-approval", body); code != 200 {
		t.Fatalf("the right code: %d %v", code, out)
	}
	if on, _ := f.dom.LoginApprovalOn(context.Background()); !on {
		t.Fatal("not turned on")
	}
}
