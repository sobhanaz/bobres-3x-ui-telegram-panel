package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The store's required channel in the Mini App (miniapp_join.go): the bot's
// gate, held at sign-in and at every call.

type memberAsk struct {
	channel string
	id      int64
}

// fakeMembers stands in for the bot's channel check: who is in the channel,
// and what was asked of it.
type fakeMembers struct {
	mu    sync.Mutex
	in    map[int64]bool
	err   error
	asked []memberAsk
}

func (f *fakeMembers) ChannelMember(_ context.Context, channel string, id int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, memberAsk{channel, id})
	if f.err != nil {
		return false, f.err
	}
	return f.in[id], nil
}

func (f *fakeMembers) set(id int64, member bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.in[id] = member
}

func (f *fakeMembers) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeMembers) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

func (f *fakeMembers) last() memberAsk {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.asked) == 0 {
		return memberAsk{}
	}
	return f.asked[len(f.asked)-1]
}

// joinClock is the server's clock, moved by the test.
type joinClock struct {
	base time.Time
	add  atomic.Int64
}

func (c *joinClock) Now() time.Time          { return c.base.Add(time.Duration(c.add.Load())) }
func (c *joinClock) advance(d time.Duration) { c.add.Add(int64(d)) }

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// joinFix is a store with the Mini App on, the channel @mynews required, and
// nobody in it yet.
type joinFix struct {
	*miniFix
	members *fakeMembers
	clock   *joinClock
	logs    *syncBuffer
}

const joinChannel = "@mynews"

func newJoinFixture(t *testing.T) *joinFix {
	t.Helper()
	m := newMiniFixture(t, true)
	j := &joinFix{miniFix: m, members: &fakeMembers{in: map[int64]bool{}}, clock: &joinClock{base: time.Now()}, logs: &syncBuffer{}}
	m.api.cfg.MiniAppMembers = j.members
	m.api.now = j.clock.Now
	m.api.cfg.Log = slog.New(slog.NewTextHandler(j.logs, nil))
	t.Cleanup(func() { m.api.now = time.Now })
	m.setting("join.channel", joinChannel)
	return j
}

// auth signs a customer in as Telegram's init data would.
func (j *joinFix) auth(tg int64) (int, map[string]any) {
	j.t.Helper()
	return j.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": fmt.Sprintf("tg:%d:name%d", tg, tg)})
}

// in signs a customer in, who must be let in.
func (j *joinFix) in(tg int64) string {
	j.t.Helper()
	code, out := j.auth(tg)
	if code != 200 || out["token"] == nil {
		j.t.Fatalf("sign in of %d: %d %v", tg, code, out)
	}
	return out["token"].(string)
}

// mustJoin: the answer is the 403 that sends the customer to the channel.
func mustJoin(t *testing.T, what string, code int, out map[string]any) {
	t.Helper()
	if code != http.StatusForbidden || out["error"] != "join_required" {
		t.Fatalf("%s: %d %v (want 403 join_required)", what, code, out)
	}
}

func TestMiniJoinIsOffWithoutAChannel(t *testing.T) {
	j := newJoinFixture(t)
	j.sql(`DELETE FROM core.settings WHERE key = 'join.channel'`)
	tok := j.in(101)
	if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 {
		t.Fatalf("plans: %d", code)
	}
	if j.members.count() != 0 {
		t.Fatalf("the bot was asked about a channel that is not required: %v", j.members.asked)
	}
}

func TestMiniJoinMemberAndNonMemberAtSignIn(t *testing.T) {
	j := newJoinFixture(t)
	sessions := func() int {
		j.api.mini.sessions.mu.Lock()
		defer j.api.mini.sessions.mu.Unlock()
		return len(j.api.mini.sessions.byID)
	}

	// Not in the channel: the 403, the channel's link, and no session.
	code, out := j.auth(102)
	mustJoin(t, "a customer outside the channel", code, out)
	if out["join_url"] != "https://t.me/mynews" || out["token"] != nil || out["message"] == "" || len(out) != 3 {
		t.Fatalf("the refusal: %v", out)
	}
	if sessions() != 0 {
		t.Fatalf("a refused sign-in left a session")
	}
	if got := j.members.last(); got != (memberAsk{joinChannel, 102}) {
		t.Fatalf("the bot was asked %+v", got)
	}

	// Joined: "I've joined" signs in again, and a non-member was not remembered, so it is let in at once.
	j.members.set(102, true)
	tok := j.in(102)
	if j.members.count() != 2 {
		t.Fatalf("asked %d times, want 2 (a non-member is never remembered)", j.members.count())
	}
	if code, out := j.call(http.MethodGet, "/api/mini/v1/me", tok, nil); code != 200 || out["wallets"] == nil {
		t.Fatalf("me: %d %v", code, out)
	}

	// A member is remembered: later sign-ins and calls do not ask again.
	j.in(102)
	for range 3 {
		if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 {
			t.Fatalf("plans: %d", code)
		}
	}
	if j.members.count() != 2 {
		t.Fatalf("a confirmed member was asked about again: %v", j.members.asked)
	}

	// Another customer is asked for themselves.
	mustJoinAuth := func(tg int64) {
		code, out := j.auth(tg)
		mustJoin(t, fmt.Sprintf("customer %d", tg), code, out)
	}
	mustJoinAuth(103)
	if got := j.members.last(); got != (memberAsk{joinChannel, 103}) || j.members.count() != 3 {
		t.Fatalf("the second customer: %v", j.members.asked)
	}
}

// A refused sign-in is the bot's rule for a newcomer too: the app asks before
// anything is bought, and a customer who joined later is let in as before.
func TestMiniJoinNewcomer(t *testing.T) {
	j := newJoinFixture(t)
	code, out := j.auth(104)
	mustJoin(t, "a newcomer outside the channel", code, out)
	j.members.set(104, true)
	j.in(104)
	if u, err := j.st.GetUserByTelegramID(context.Background(), j.st.Conn(), 104); err != nil || u.Status != "active" {
		t.Fatalf("the customer after joining: %+v %v", u, err)
	}
}

func TestMiniJoinLinkInTheRefusal(t *testing.T) {
	j := newJoinFixture(t)
	ask := func() map[string]any {
		t.Helper()
		code, out := j.auth(105)
		mustJoin(t, "the refusal", code, out)
		return out
	}

	// A public channel's page; the link the owner set wins over it; telegram.me is t.me for Telegram's script.
	if out := ask(); out["join_url"] != "https://t.me/mynews" {
		t.Fatalf("public channel: %v", out)
	}
	j.setting("join.link", "https://t.me/mynews_invite")
	if out := ask(); out["join_url"] != "https://t.me/mynews_invite" {
		t.Fatalf("join.link: %v", out)
	}
	j.setting("join.link", "https://telegram.me/mynews_invite")
	if out := ask(); out["join_url"] != "https://t.me/mynews_invite" {
		t.Fatalf("telegram.me: %v", out)
	}

	// A private channel: the invite link, never the channel's id.
	j.setting("join.channel", "-1001234567890")
	j.setting("join.link", "https://t.me/+AbCdEfGhIjKlMnOp")
	out := ask()
	if out["join_url"] != "https://t.me/+AbCdEfGhIjKlMnOp" {
		t.Fatalf("private channel: %v", out)
	}
	if strings.Contains(fmt.Sprint(out), "1234567890") {
		t.Fatalf("the refusal has the channel's id: %v", out)
	}
	if got := j.members.last(); got.channel != "-1001234567890" {
		t.Fatalf("the bot was asked about %q", got.channel)
	}
}

func TestMiniJoinLinkRules(t *testing.T) {
	for name, c := range map[string]struct {
		settings map[string]string
		want     string
	}{
		"a public channel":                  {map[string]string{"join.channel": "@mynews"}, "https://t.me/mynews"},
		"the link wins":                     {map[string]string{"join.channel": "@mynews", "join.link": "https://t.me/other"}, "https://t.me/other"},
		"an invite link":                    {map[string]string{"join.channel": "-1001234567890", "join.link": "https://t.me/+AbCd-EfGh_12"}, "https://t.me/+AbCd-EfGh_12"},
		"telegram.me is written t.me":       {map[string]string{"join.channel": "-1001234567890", "join.link": "https://telegram.me/joinchat/AAAA"}, "https://t.me/joinchat/AAAA"},
		"a private channel with no link":    {map[string]string{"join.channel": "-1001234567890"}, ""},
		"a link that is not https":          {map[string]string{"join.channel": "@mynews", "join.link": "http://t.me/other"}, "https://t.me/mynews"},
		"a link to another site":            {map[string]string{"join.channel": "@mynews", "join.link": "https://example.com/other"}, "https://t.me/mynews"},
		"a link with a user":                {map[string]string{"join.channel": "@mynews", "join.link": "https://x@t.me/other"}, "https://t.me/mynews"},
		"a private channel and a bad link":  {map[string]string{"join.channel": "-1001234567890", "join.link": "javascript:alert(1)"}, ""},
		"a link that is not a link at all":  {map[string]string{"join.channel": "@mynews", "join.link": "%%"}, "https://t.me/mynews"},
		"nothing is set":                    {map[string]string{}, ""},
		"an @ with no name":                 {map[string]string{"join.channel": "@"}, ""},
		"a name that is not a public name":  {map[string]string{"join.channel": "mynews"}, ""},
		"the id of a channel is not a link": {map[string]string{"join.channel": "-1001234567890", "join.link": "-1001234567890"}, ""},
	} {
		if got := miniJoinLink(c.settings); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// Staff and the configured owners are never asked to join, in the bot or here;
// nor is the bot asked about them.
func TestMiniJoinExemptions(t *testing.T) {
	j := newJoinFixture(t)
	for i, role := range []string{"owner", "admin", "support"} {
		tg := int64(2000 + i)
		j.staffUser(tg, role)
		tok := j.in(tg)
		if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 {
			t.Errorf("%s: plans %d", role, code)
		}
	}
	// The owner the server names, even when the account does not say so (the bot's gate lets them through too).
	j.sql(`UPDATE core.users SET role = 'user' WHERE telegram_id = $1`, ownerTG)
	tok := j.in(ownerTG)
	if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 {
		t.Errorf("the configured owner: plans %d", code)
	}
	if j.members.count() != 0 {
		t.Fatalf("the bot was asked about staff: %v", j.members.asked)
	}

	// A reseller is a customer to the gate: the bot's rule is the same.
	j.customer(2100, "reseller")
	j.sql(`UPDATE core.users SET role = 'reseller' WHERE telegram_id = 2100`)
	code, out := j.auth(2100)
	mustJoin(t, "a reseller outside the channel", code, out)
}

// Maintenance comes first, as in the bot: while it is on nobody is asked about the channel (the app shows
// the maintenance screen), and when it ends the channel is asked of whoever is still outside it.
func TestMiniJoinMaintenanceComesFirst(t *testing.T) {
	j := newJoinFixture(t)
	j.setting("maintenance.enabled", "true")
	code, out := j.auth(106)
	if code != 200 || out["maintenance"] != true || out["token"] == nil {
		t.Fatalf("sign-in in maintenance: %d %v", code, out)
	}
	tok := out["token"].(string)
	if code, out := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 503 || out["error"] != "maintenance" {
		t.Fatalf("plans in maintenance: %d %v", code, out)
	}
	if j.members.count() != 0 {
		t.Fatalf("the bot was asked in maintenance: %v", j.members.asked)
	}

	j.setting("maintenance.enabled", "false")
	code, out = j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil)
	mustJoin(t, "plans after maintenance", code, out)
	if j.members.count() != 1 {
		t.Fatalf("asked %d times", j.members.count())
	}
}

// A banned customer is told so and nothing about the channel.
func TestMiniJoinBannedComesFirst(t *testing.T) {
	j := newJoinFixture(t)
	j.members.set(107, true)
	tok := j.in(107)
	j.sql(`UPDATE core.users SET status = 'banned' WHERE telegram_id = 107`)
	j.members.set(107, false)
	j.clock.advance(time.Hour)
	if code, out := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 403 || out["error"] != "banned" {
		t.Fatalf("banned session: %d %v", code, out)
	}
	if code, out := j.auth(107); code != 403 || out["error"] != "banned" {
		t.Fatalf("banned sign-in: %d %v", code, out)
	}
	if j.members.count() != 1 {
		t.Fatalf("the bot was asked about a banned customer: %v", j.members.asked)
	}
}

// When Telegram cannot tell, the customer is let in, as the bot lets them in; the failure is logged
// once a minute, and the customer is not asked about again for a minute (every call would otherwise
// wait for a channel that does not answer).
func TestMiniJoinLetsInWhenTelegramCannotTell(t *testing.T) {
	j := newJoinFixture(t)
	j.members.fail(errors.New("botfiles: channel member: the bot answered 502 error: Bad Request: chat not found"))

	tok := j.in(108)
	for range 3 {
		if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 {
			t.Fatalf("plans: %d", code)
		}
	}
	if j.members.count() != 1 {
		t.Fatalf("asked %d times within a minute, want 1", j.members.count())
	}
	// A second customer: asked, let in, and the log has one line for the minute.
	j.in(109)
	if n := strings.Count(j.logs.String(), "cannot check members of the join channel"); n != 1 {
		t.Fatalf("%d warnings for two failures within a minute:\n%s", n, j.logs.String())
	}
	for _, leak := range []string{"108", "109"} {
		if strings.Contains(j.logs.String(), leak) {
			t.Fatalf("the log has a customer's id:\n%s", j.logs.String())
		}
	}
	if !strings.Contains(j.logs.String(), "chat not found") {
		t.Fatalf("the log does not say why:\n%s", j.logs.String())
	}

	// A minute on, the customer is asked again; the channel is readable now, and they have not joined.
	j.clock.advance(61 * time.Second)
	j.members.fail(nil)
	code, out := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil)
	mustJoin(t, "a call once Telegram answers", code, out)
	if j.members.count() != 3 {
		t.Fatalf("asked %d times", j.members.count())
	}

	// Another failure, past the minute, is logged again.
	j.members.fail(errors.New("timeout"))
	j.in(110)
	if n := strings.Count(j.logs.String(), "cannot check members of the join channel"); n != 2 {
		t.Fatalf("%d warnings, want 2:\n%s", n, j.logs.String())
	}
}

// An app with no bot to ask (no sign-in could have happened without one, but a test can) lets everyone in.
func TestMiniJoinWithoutABotToAsk(t *testing.T) {
	j := newJoinFixture(t)
	j.api.cfg.MiniAppMembers = nil
	tok := j.in(111)
	if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 {
		t.Fatalf("plans: %d", code)
	}
	if !strings.Contains(j.logs.String(), "the bot is not connected") {
		t.Fatalf("not logged:\n%s", j.logs.String())
	}
}

// A member is remembered for ten minutes, and then asked about again: a customer who left the channel
// is stopped at their next call, though their session is alive.
func TestMiniJoinCacheExpires(t *testing.T) {
	j := newJoinFixture(t)
	j.members.set(112, true)
	tok := j.in(112)
	j.clock.advance(9*time.Minute + 50*time.Second)
	if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 || j.members.count() != 1 {
		t.Fatalf("just before ten minutes: %d, asked %d times", code, j.members.count())
	}
	j.members.set(112, true)
	j.clock.advance(20 * time.Second)
	if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 || j.members.count() != 2 {
		t.Fatalf("after ten minutes, still a member: %d, asked %d times", code, j.members.count())
	}
	j.members.set(112, false) // leaves the channel
	if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 || j.members.count() != 2 {
		t.Fatalf("a member who just left is remembered for ten minutes: %d, asked %d times", code, j.members.count())
	}
	j.clock.advance(10*time.Minute + time.Second)
	code, out := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil)
	mustJoin(t, "a call after leaving the channel", code, out)
	// ... and so is the next one, for a non-member is not remembered.
	code, out = j.call(http.MethodGet, "/api/mini/v1/wallet", tok, nil)
	mustJoin(t, "the next call", code, out)
	if j.members.count() != 4 {
		t.Fatalf("asked %d times, want 4", j.members.count())
	}
}

// What was remembered is about a channel: another channel starts the asking again.
func TestMiniJoinCacheFollowsTheChannel(t *testing.T) {
	j := newJoinFixture(t)
	j.members.set(113, true)
	tok := j.in(113)
	j.setting("join.channel", "@another")
	j.members.set(113, false)
	code, out := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil)
	mustJoin(t, "a call after the channel changed", code, out)
	if got := j.members.last(); got != (memberAsk{"@another", 113}) {
		t.Fatalf("asked %+v", got)
	}
	// Switching the requirement off lets everyone in without a question.
	j.sql(`DELETE FROM core.settings WHERE key = 'join.channel'`)
	n := j.members.count()
	if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 || j.members.count() != n {
		t.Fatalf("with no channel: %d, asked %d times more", code, j.members.count()-n)
	}
}

// The owner turning the channel on stops the sessions already open, except for what a payment under way
// needs (as in maintenance, and as the bot lets a payment finish). A customer who then joins goes on with the same
// session.
func TestMiniJoinStopsAnOpenSession(t *testing.T) {
	j := newJoinFixture(t)
	j.sql(`DELETE FROM core.settings WHERE key = 'join.channel'`)
	tok := j.in(114)
	j.setting("join.channel", joinChannel)

	const id = "0000000b-0000-4000-8000-000000000001"
	gated := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/mini/v1/plans", nil},
		{http.MethodGet, "/api/mini/v1/services", nil},
		{http.MethodGet, "/api/mini/v1/wallet", nil},
		{http.MethodGet, "/api/mini/v1/methods?currency=IRT&amount=100000", nil},
		{http.MethodPost, "/api/mini/v1/quote", map[string]any{}},
		{http.MethodPost, "/api/mini/v1/orders", map[string]any{}},
		{http.MethodPost, "/api/mini/v1/orders/" + id + "/pay-wallet", map[string]any{}},
		{http.MethodPost, "/api/mini/v1/orders/" + id + "/pay", map[string]any{"method": "zarinpal"}},
		{http.MethodPost, "/api/mini/v1/topup", map[string]any{}},
		{http.MethodGet, "/api/mini/v1/trial", nil},
		{http.MethodPost, "/api/mini/v1/trial", map[string]any{}},
		{http.MethodGet, "/api/mini/v1/invite", nil},
		{http.MethodGet, "/api/mini/v1/support", nil},
		{http.MethodGet, "/api/mini/v1/support/tickets/" + id, nil},
		{http.MethodPost, "/api/mini/v1/support/messages", map[string]any{}},
		{http.MethodGet, "/api/mini/v1/reseller", nil},
		{http.MethodGet, "/api/mini/v1/reseller/services", nil},
		{http.MethodGet, "/api/mini/v1/reseller/customers", nil},
		{http.MethodGet, "/api/mini/v1/reseller/statement", nil},
	}
	for _, c := range gated {
		code, out := j.call(c.method, c.path, tok, c.body)
		mustJoin(t, c.method+" "+c.path, code, out)
	}
	// Left open: who the customer is, an order, a payment's check and its proof, signing out.
	for _, c := range []struct {
		method, path string
		body         any
		want         int
	}{
		{http.MethodGet, "/api/mini/v1/me", nil, 200},
		{http.MethodGet, "/api/mini/v1/orders/" + id, nil, 404},
		{http.MethodPost, "/api/mini/v1/payments/" + id + "/check", map[string]any{}, 200}, // the fake payments service knows every id
	} {
		if code, out := j.call(c.method, c.path, tok, c.body); code != c.want || out["error"] == "join_required" {
			t.Errorf("%s %s: %d %v (want %d)", c.method, c.path, code, out, c.want)
		}
	}
	if code, out := j.call(http.MethodPost, "/api/mini/v1/payments/"+id+"/proof", tok, map[string]any{"txid": "x"}); code == http.StatusForbidden || out["error"] == "join_required" {
		t.Errorf("a proof was stopped: %d %v", code, out)
	}

	// The customer joins and presses "I've joined": the app signs in again, and the old session works as well.
	j.members.set(114, true)
	j.in(114)
	if code, _ := j.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 200 {
		t.Fatalf("the open session after joining: %d", code)
	}
	if code, _ := j.call(http.MethodPost, "/api/mini/v1/logout", tok, map[string]any{}); code != 200 {
		t.Fatalf("logout: %d", code)
	}
}

// Many calls at once, as the app makes when it opens, with the answer run out: no data race, and a
// member is served by every one of them.
func TestMiniJoinConcurrentCalls(t *testing.T) {
	j := newJoinFixture(t)
	j.members.set(115, true)
	tok := j.in(115)
	j.clock.advance(11 * time.Minute)

	var wg sync.WaitGroup
	codes := make(chan int, 24)
	for range cap(codes) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, j.srv.URL+"/api/mini/v1/plans", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			req.Header.Set("X-Forwarded-For", "203.0.113.250")
			resp, err := j.srv.Client().Do(req)
			if err != nil {
				codes <- -1
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != 200 {
			t.Errorf("a concurrent call answered %d", c)
		}
	}
	if n := j.members.count(); n < 2 || n > 25 {
		t.Errorf("the bot was asked %d times", n)
	}
}

// The cache is bounded and forgets what has run out first.
func TestMiniJoinCacheIsBounded(t *testing.T) {
	now := time.Now()
	c := newMiniJoin()
	for i := range miniJoinMax {
		c.allow(int64(i+1), "@c", time.Minute, now)
	}
	if len(c.letIn) != miniJoinMax {
		t.Fatalf("%d entries", len(c.letIn))
	}
	// Full of live entries: another customer is not kept, and is asked next time.
	c.allow(int64(miniJoinMax+1), "@c", time.Minute, now)
	if len(c.letIn) != miniJoinMax || c.allowed(int64(miniJoinMax+1), "@c", now) {
		t.Fatalf("a full table took another customer: %d entries", len(c.letIn))
	}
	if !c.allowed(1, "@c", now) {
		t.Fatal("a live entry was dropped")
	}
	// Once they have run out they make room.
	later := now.Add(2 * time.Minute)
	c.allow(int64(miniJoinMax+1), "@c", time.Minute, later)
	if len(c.letIn) != 1 || !c.allowed(int64(miniJoinMax+1), "@c", later) {
		t.Fatalf("after the entries ran out: %d entries", len(c.letIn))
	}
	// An entry is for one channel and one moment.
	if c.allowed(int64(miniJoinMax+1), "@other", later) || c.allowed(int64(miniJoinMax+1), "@c", later.Add(time.Minute)) {
		t.Fatal("an entry served another channel or a later time")
	}
}
