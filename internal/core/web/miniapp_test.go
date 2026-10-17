package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// fakeVerifier stands in for the bot: init data "tg:<id>[:<username>[:<lang>]]"
// is signed for that user, anything else is refused.
type fakeVerifier struct{ err error }

func (v fakeVerifier) VerifyMiniApp(_ context.Context, initData string) (*botfiles.MiniAppUser, error) {
	if v.err != nil {
		return nil, v.err
	}
	parts := strings.Split(initData, ":")
	if len(parts) < 2 || parts[0] != "tg" {
		return nil, fmt.Errorf("%w: signature", botfiles.ErrMiniAppAuth)
	}
	var id int64
	if _, err := fmt.Sscan(parts[1], &id); err != nil {
		return nil, fmt.Errorf("%w: id", botfiles.ErrMiniAppAuth)
	}
	u := &botfiles.MiniAppUser{ID: id}
	if len(parts) > 2 {
		u.Username = parts[2]
	}
	if len(parts) > 3 {
		u.LanguageCode = parts[3]
	}
	return u, nil
}

// miniPay is a payments service with one gateway.
type miniPay struct {
	fakePay
	started []domain.GatewayStart
}

func (p *miniPay) ListGateways(context.Context) ([]string, error) {
	return []string{domain.Zarinpal}, nil
}
func (p *miniPay) StartGateway(_ context.Context, g domain.GatewayStart) (*domain.GatewayIntent, error) {
	p.started = append(p.started, g)
	return &domain.GatewayIntent{ID: "int-1", Status: "pending", OrderID: g.OrderID, Provider: g.Provider, Amount: g.Amount,
		Currency: g.Currency, GatewayAmount: g.GatewayAmount, GatewayCurrency: g.GatewayCurrency, PayURL: "https://pay.example/x"}, nil
}
func (p *miniPay) CheckIntent(_ context.Context, _, id string) (*domain.GatewayIntent, error) {
	return &domain.GatewayIntent{ID: id, Status: "succeeded", Provider: domain.Zarinpal}, nil
}

type miniFix struct {
	*fixture
	pay  *miniPay
	prov *fakeProv
}

func newMiniFixture(t *testing.T, on bool) *miniFix {
	t.Helper()
	f := newFixture(t)
	f.sql(`DELETE FROM core.settings`)
	t.Cleanup(func() { f.sql(`DELETE FROM core.settings`) })
	f.staffUser(ownerTG, "owner")
	m := &miniFix{fixture: f, pay: &miniPay{}, prov: &fakeProv{}}
	f.api.cfg.Payments, f.api.cfg.Provisioner = m.pay, m.prov
	f.api.cfg.MiniAppVerify = fakeVerifier{}
	if on {
		m.setting("miniapp.enabled", "true")
	}
	return m
}

func (m *miniFix) setting(k, v string) {
	m.t.Helper()
	if _, err := m.dom.SetSettings(context.Background(), actor0(m.fixture), map[string]string{k: v}); err != nil {
		m.t.Fatal(err)
	}
}

var miniClients int

// call makes a request as a client of its own address.
func (m *miniFix) call(method, path, token string, body any) (int, map[string]any) {
	m.t.Helper()
	miniClients++
	return m.callFrom(method, path, token, body, fmt.Sprintf("203.0.113.%d", miniClients%250+1))
}

func (m *miniFix) callFrom(method, path, token string, body any, ip string) (int, map[string]any) {
	m.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, m.srv.URL+path, rd)
	req.Header.Set("X-Forwarded-For", ip)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := m.srv.Client().Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	defer resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		m.t.Errorf("%s %s: Cache-Control %q", method, path, cc)
	}
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (m *miniFix) signIn(initData string) string {
	m.t.Helper()
	code, out := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": initData})
	if code != 200 {
		m.t.Fatalf("sign in %q: %d %v", initData, code, out)
	}
	tok, _ := out["token"].(string)
	if tok == "" {
		m.t.Fatalf("no token: %v", out)
	}
	return tok
}

func (m *miniFix) fund(userID string, amount int64) {
	m.t.Helper()
	m.sql(`INSERT INTO core.wallets (user_id, currency, balance) VALUES ($1, 'IRT', $2)
		ON CONFLICT (user_id, currency) DO UPDATE SET balance = $2`, userID, amount)
}

func TestMiniAppIsOffUntilEnabled(t *testing.T) {
	m := newMiniFixture(t, false)
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:1"}); code != 404 {
		t.Fatalf("auth while off: %d", code)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/plans", "x", nil); code != 404 {
		t.Fatalf("plans while off: %d", code)
	}
	m.setting("miniapp.enabled", "true")
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:1"}); code != 200 {
		t.Fatalf("auth when on: %d", code)
	}
}

func TestMiniAppSignIn(t *testing.T) {
	m := newMiniFixture(t, true)
	// No or bad init data: refused; no account made.
	for _, bad := range []string{"", "garbage", strings.Repeat("a", 9<<10)} {
		if code, _ := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": bad}); code != 401 {
			t.Errorf("init data %.10q: %d", bad, code)
		}
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:5", "extra": 1}); code != 400 {
		t.Errorf("unknown field: %d", code)
	}
	if _, err := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 5); err == nil {
		t.Error("a refused sign-in must not create an account")
	}
	// First visit creates the account in the client's language.
	tok := m.signIn("tg:5:newbie:en-GB")
	u, err := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 5)
	if err != nil || u.Language != "en" || u.Username != "newbie" {
		t.Fatalf("created user: %+v %v", u, err)
	}
	// A later visit leaves the language the customer chose in the bot.
	m.sql(`UPDATE core.users SET language = 'fa' WHERE id = $1`, u.ID)
	m.signIn("tg:5:newbie:en")
	if u, _ = m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 5); u.Language != "fa" {
		t.Errorf("language overwritten: %q", u.Language)
	}
	code, out := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil)
	if code != 200 || out["wallets"] == nil || out["brand"] == nil {
		t.Fatalf("me: %d %v", code, out)
	}
	// Logout ends the session.
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/logout", tok, map[string]any{}); code != 200 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
	// Without a bot to ask, sign-in is unavailable, not open.
	m.api.cfg.MiniAppVerify = nil
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:5"}); code != 503 {
		t.Errorf("no verifier: %d", code)
	}
	m.api.cfg.MiniAppVerify = fakeVerifier{err: fmt.Errorf("bot down")}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:5"}); code != 503 {
		t.Errorf("verifier down: %d", code)
	}
}

func TestMiniAppSignInIsRateLimited(t *testing.T) {
	m := newMiniFixture(t, true)
	last := 0
	for i := 0; i < miniAuthRate+5; i++ {
		last, _ = m.callFrom(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "bad"}, "198.51.100.77")
	}
	if last != 429 {
		t.Fatalf("after %d sign-ins from one address: %d", miniAuthRate+5, last)
	}
}

func TestMiniAppBannedAndMaintenance(t *testing.T) {
	m := newMiniFixture(t, true)
	tok := m.signIn("tg:9:bob")
	u, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 9)

	m.setting("maintenance.enabled", "true")
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 503 {
		t.Errorf("plans in maintenance: %d", code)
	}
	if code, out := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil); code != 200 || out["maintenance"] != true {
		t.Errorf("me in maintenance: %d %v", code, out)
	}
	m.setting("maintenance.enabled", "false")

	m.sql(`UPDATE core.users SET status = 'banned' WHERE id = $1`, u.ID)
	if code, out := m.call(http.MethodGet, "/api/mini/v1/plans", tok, nil); code != 403 || out["error"] != "banned" {
		t.Errorf("banned session: %d %v", code, out)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:9"}); code != 403 {
		t.Errorf("banned sign-in: %d", code)
	}
}

func TestMiniAppRequiresAToken(t *testing.T) {
	m := newMiniFixture(t, true)
	for _, p := range []string{"/api/mini/v1/me", "/api/mini/v1/plans", "/api/mini/v1/services", "/api/mini/v1/wallet", "/api/mini/v1/orders/x"} {
		if code, _ := m.call(http.MethodGet, p, "", nil); code != 401 {
			t.Errorf("GET %s without token: %d", p, code)
		}
		if code, _ := m.call(http.MethodGet, p, "not-a-token", nil); code != 401 {
			t.Errorf("GET %s with a wrong token: %d", p, code)
		}
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/topup", "", map[string]any{}); code != 401 {
		t.Errorf("topup without token: %d", code)
	}
	// A staff dashboard cookie or token is not a Mini App session.
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/nope", "x", nil); code != 404 {
		t.Errorf("unknown endpoint: %d", code)
	}
}

func TestMiniAppPlansServicesWallet(t *testing.T) {
	m := newMiniFixture(t, true)
	shown := m.plan("Shown", 30, 50<<30, 100000)
	hidden := m.plan("Hidden", 30, 50<<30, 100000)
	m.sql(`UPDATE core.plans SET enabled = false WHERE id = $1`, hidden.ID)
	trial := m.plan("Trial", 1, 1<<30, 0)
	m.sql(`UPDATE core.plans SET is_trial = true WHERE id = $1`, trial.ID)

	tok := m.signIn("tg:20:carol")
	me, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 20)
	_, sub := m.orderSub(me, shown, "active", "active", time.Now().Add(10*24*time.Hour), 50<<30)
	other := m.customer(21, "dave")
	_, otherSub := m.orderSub(other, shown, "active", "active", time.Now().Add(10*24*time.Hour), 50<<30)
	m.fund(me.ID, 12345)

	code, out := m.call(http.MethodGet, "/api/mini/v1/plans", tok, nil)
	// Other tests of the package leave plans behind: look at the ones made here.
	plans, _ := out["plans"].([]any)
	var seen map[string]any
	for _, p := range plans {
		switch p.(map[string]any)["id"] {
		case shown.ID:
			seen = p.(map[string]any)
		case hidden.ID, trial.ID:
			t.Fatalf("a hidden or trial plan is for sale: %v", p)
		}
	}
	if code != 200 || seen == nil || seen["price"] != float64(100000) {
		t.Fatalf("plans: %d %v", code, out)
	}

	code, out = m.call(http.MethodGet, "/api/mini/v1/services", tok, nil)
	svcs, _ := out["services"].([]any)
	if code != 200 || len(svcs) != 1 {
		t.Fatalf("services: %d %v", code, out)
	}
	if s := svcs[0].(map[string]any); s["id"] != sub.ID || s["can_renew"] != true || s["link"] == "" {
		t.Fatalf("service: %v", s)
	}
	if strings.Contains(fmt.Sprint(out), otherSub.ID) {
		t.Fatal("another customer's service listed")
	}

	code, out = m.call(http.MethodGet, "/api/mini/v1/wallet", tok, nil)
	ws, _ := out["wallets"].([]any)
	if code != 200 || len(ws) == 0 || ws[0].(map[string]any)["balance"] != float64(12345) {
		t.Fatalf("wallet: %d %v", code, out)
	}
}

func TestMiniAppBuyWithWallet(t *testing.T) {
	m := newMiniFixture(t, true)
	p := m.plan("Gold", 30, 50<<30, 100000)
	tok := m.signIn("tg:30:erin")
	me, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 30)
	order := map[string]any{"plan_id": p.ID, "key": "abcdef123456"}

	code, o := m.call(http.MethodPost, "/api/mini/v1/orders", tok, order)
	if code != 200 || o["status"] == "" || o["amount"] != float64(100000) {
		t.Fatalf("create order: %d %v", code, o)
	}
	id := o["id"].(string)
	// The same key is the same order (a double tap).
	if _, again := m.call(http.MethodPost, "/api/mini/v1/orders", tok, order); again["id"] != id {
		t.Fatalf("repeated key made another order: %v", again)
	}
	// Missing or malformed key, a bad plan, a bad type.
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": p.ID}); code != 400 {
		t.Errorf("no key: %d", code)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": p.ID, "key": "abcdef123456", "type": "wat"}); code != 400 {
		t.Errorf("bad type: %d", code)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": "nope", "key": "zzzzzzzz1"}); code != 400 {
		t.Errorf("unknown plan: %d", code)
	}

	// Not enough in the wallet.
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{}); code != 409 || out["error"] != "insufficient_funds" {
		t.Fatalf("pay with an empty wallet: %d %v", code, out)
	}
	m.fund(me.ID, 150000)
	code, paid := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{})
	if code != 200 || paid["status"] == "pending_payment" {
		t.Fatalf("pay: %d %v", code, paid)
	}
	// Paying again does not charge twice.
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{}); code != 200 && code != 409 {
		t.Fatalf("pay again: %d", code)
	}
	var bal int64
	if err := m.st.DB().QueryRow(context.Background(), `SELECT balance FROM core.wallets WHERE user_id=$1 AND currency='IRT'`, me.ID).Scan(&bal); err != nil || bal != 50000 {
		t.Fatalf("balance after paying: %d %v", bal, err)
	}
	if code, o := m.call(http.MethodGet, "/api/mini/v1/orders/"+id, tok, nil); code != 200 || o["id"] != id {
		t.Fatalf("poll: %d %v", code, o)
	}
}

func TestMiniAppOrdersAreTheCustomersOwn(t *testing.T) {
	m := newMiniFixture(t, true)
	p := m.plan("Gold", 30, 50<<30, 100000)
	alice, bob := m.signIn("tg:40:alice"), m.signIn("tg:41:bob")
	_, o := m.call(http.MethodPost, "/api/mini/v1/orders", alice, map[string]any{"plan_id": p.ID, "key": "alicekey123"})
	id := o["id"].(string)
	bobUser, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 41)
	m.fund(bobUser.ID, 500000)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/mini/v1/orders/" + id},
		{http.MethodPost, "/api/mini/v1/orders/" + id + "/pay-wallet"},
		{http.MethodPost, "/api/mini/v1/orders/" + id + "/pay"},
	} {
		var body any
		if c.method == http.MethodPost {
			body = map[string]any{"method": "zarinpal"}
		}
		if code, _ := m.call(c.method, c.path, bob, body); code != 404 {
			t.Errorf("%s %s as another customer: %d", c.method, c.path, code)
		}
	}
	// Nor can one renew another customer's service.
	other := m.customer(42, "zed")
	_, sub := m.orderSub(other, p, "active", "active", time.Now().Add(5*24*time.Hour), 50<<30)
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders", bob, map[string]any{"plan_id": p.ID, "type": "renew", "subscription_id": sub.ID, "key": "stealkey123"}); code == 200 {
		t.Fatalf("renewed someone else's service: %v", out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/quote", bob, map[string]any{"plan_id": p.ID, "type": "renew", "subscription_id": sub.ID}); code == 200 {
		t.Fatalf("quoted someone else's service: %v", out)
	}
}

func TestMiniAppRenewOwnService(t *testing.T) {
	m := newMiniFixture(t, true)
	p := m.plan("Gold", 30, 50<<30, 100000)
	tok := m.signIn("tg:50:fay")
	me, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 50)
	_, sub := m.orderSub(me, p, "active", "active", time.Now().Add(3*24*time.Hour), 50<<30)
	code, q := m.call(http.MethodPost, "/api/mini/v1/quote", tok, map[string]any{"plan_id": p.ID, "type": "renew", "subscription_id": sub.ID})
	if code != 200 || q["total"] != float64(100000) {
		t.Fatalf("quote: %d %v", code, q)
	}
	code, o := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": p.ID, "type": "renew", "subscription_id": sub.ID, "key": "renewkey123"})
	if code != 200 || o["type"] != "renew" || o["subscription_id"] != sub.ID {
		t.Fatalf("renew order: %d %v", code, o)
	}
	// A renewal must name a service, a new order must not.
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": p.ID, "type": "renew", "key": "renewkey124"}); code != 400 {
		t.Errorf("renew without a service: %d", code)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": p.ID, "subscription_id": sub.ID, "key": "renewkey125"}); code != 400 {
		t.Errorf("new order naming a service: %d", code)
	}
}

func TestMiniAppGatewayAndTopup(t *testing.T) {
	m := newMiniFixture(t, true)
	p := m.plan("Gold", 30, 50<<30, 100000)
	tok := m.signIn("tg:60:gil")
	_, o := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": p.ID, "key": "gatewaykey1"})
	id := o["id"].(string)

	// Methods the app cannot finish itself are refused, not started.
	for _, method := range []string{"manual_card", "manual_crypto", "stars", "nonsense", ""} {
		if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay", tok, map[string]any{"method": method}); code != 400 {
			t.Errorf("method %q: %d", method, code)
		}
	}
	if len(m.pay.started) != 0 {
		t.Fatalf("a refused method reached the payments service: %v", m.pay.started)
	}

	code, res := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay", tok, map[string]any{"method": "zarinpal"})
	if code != 200 {
		t.Skipf("Zarinpal is not offered in this license state: %d %v", code, res)
	}
	if res["pay_url"] != "https://pay.example/x" || res["intent_id"] != "int-1" {
		t.Fatalf("intent: %v", res)
	}
	if g := m.pay.started[0]; g.OrderID != id || g.IdempotencyKey != "int:"+id+":zarinpal" || g.GatewayCurrency != "IRR" {
		t.Fatalf("started: %+v", g)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/payments/int-1/check", tok, map[string]any{}); code != 200 || out["status"] != "succeeded" {
		t.Fatalf("check: %d %v", code, out)
	}

	// Top-up: Toman bounds and a key.
	for _, c := range []map[string]any{
		{"amount": 5000, "method": "zarinpal", "key": "topupkey123"},
		{"amount": 60_000_000, "method": "zarinpal", "key": "topupkey123"},
		{"amount": 100000, "method": "zarinpal", "key": "x"},
		{"amount": 100000, "method": "manual_card", "key": "topupkey123"},
	} {
		if code, _ := m.call(http.MethodPost, "/api/mini/v1/topup", tok, c); code != 400 {
			t.Errorf("top-up %v: %d", c, code)
		}
	}
	code, res = m.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 200000, "method": "zarinpal", "key": "topupkey123"})
	if code != 200 || res["order_id"] != "" && res["order_id"] != nil {
		t.Fatalf("top-up: %d %v", code, res)
	}
}

func TestMiniAppBodyLimit(t *testing.T) {
	m := newMiniFixture(t, true)
	big := map[string]any{"init_data": strings.Repeat("a", 20<<10)}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/auth", "", big); code != 400 && code != 413 {
		t.Fatalf("oversized body: %d", code)
	}
}

func TestMiniAppStaticFollowsTheSwitch(t *testing.T) {
	m := newMiniFixture(t, false)
	m.api.cfg.MiniApp = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("app")) })
	mux := http.NewServeMux()
	m.api.registerMiniApp(mux)
	srv := mux
	get := func() int {
		rec := newRecorder()
		srv.ServeHTTP(rec, mustReq(http.MethodGet, "/app/"))
		return rec.Code
	}
	if c := get(); c != 404 {
		t.Fatalf("off: %d", c)
	}
	m.setting("miniapp.enabled", "true")
	if c := get(); c != 200 {
		t.Fatalf("on: %d", c)
	}
}

func newRecorder() *httptest.ResponseRecorder   { return httptest.NewRecorder() }
func mustReq(method, path string) *http.Request { return httptest.NewRequest(method, path, nil) }

// orderSub is fixture.order, spaced out: its client e-mails come from the
// first characters of a time-ordered id, which clash within 16 milliseconds.
func (m *miniFix) orderSub(u *store.User, p *store.Plan, status, subStatus string, expires time.Time, traffic int64) (*store.Order, *store.Subscription) {
	time.Sleep(20 * time.Millisecond)
	return m.order(u, p, status, subStatus, expires, traffic)
}
