package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The Mini App pays a plan priced in USDT from the Toman wallet at a quote, and tops the wallet up in USDT (docs/17-mini-app.md).

const miniTronAddress = "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf"

// miniUsdtPay is a payments service whose one gateway is automatic USDT: it hands out the store's address and a unique amount.
type miniUsdtPay struct {
	miniPay
	mu    sync.Mutex
	asked []domain.GatewayStart
}

func (p *miniUsdtPay) ListGateways(context.Context) ([]string, error) {
	return []string{domain.USDTAuto}, nil
}

func (p *miniUsdtPay) StartGateway(_ context.Context, g domain.GatewayStart) (*domain.GatewayIntent, error) {
	p.mu.Lock()
	p.asked = append(p.asked, g)
	p.mu.Unlock()
	// Payments makes the amount unique among the open payments: 1 to 999 micro-USDT more.
	return &domain.GatewayIntent{ID: miniIntentID, Status: "pending", OrderID: g.OrderID, Provider: g.Provider, Amount: g.Amount, Currency: g.Currency,
		GatewayAmount: g.GatewayAmount + 417, GatewayCurrency: "USDT", PayAddress: miniTronAddress, ExpiresAt: time.Now().Add(45 * time.Minute).Unix()}, nil
}

func (m *miniFix) usdtPlan(name string, price int64) *store.Plan {
	m.t.Helper()
	days, traffic := int32(30), int64(50<<30)
	p := &store.Plan{NameI18n: map[string]string{"en": name}, Kind: "both", DurationDays: &days, TrafficBytes: &traffic, Price: price, Currency: "USDT", Enabled: true}
	if err := m.st.UpsertPlan(context.Background(), m.st.Conn(), p); err != nil {
		m.t.Fatal(err)
	}
	return p
}

// usdtRate makes the store's USDT rate the owner's own (the live price off), so that a test knows what a USDT costs.
func (m *miniFix) usdtRate(rate string) {
	m.t.Helper()
	m.setting("payments.usdt_rate_auto", "false")
	m.setting("payments.usdt_rate", rate)
}

func (m *miniFix) wallet(userID string) int64 {
	m.t.Helper()
	var bal int64
	if err := m.st.DB().QueryRow(context.Background(), `SELECT COALESCE(max(balance), 0) FROM core.wallets WHERE user_id = $1 AND currency = 'IRT'`, userID).Scan(&bal); err != nil {
		m.t.Fatal(err)
	}
	return bal
}

func (m *miniFix) user(tg int64) *store.User {
	m.t.Helper()
	u, err := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), tg)
	if err != nil {
		m.t.Fatal(err)
	}
	return u
}

func TestMiniAppPaysAUsdtPlanFromTheWalletAtAQuote(t *testing.T) {
	m := newMiniFixture(t, true)
	m.usdtRate("300000")
	plan := m.usdtPlan("Gold USDT", 4_000_000)
	tok := m.signIn("tg:7001:ali")
	me := m.user(7001)

	// The shop lists the plan in USDT; an order is made in USDT.
	_, plans := m.call(http.MethodGet, "/api/mini/v1/plans", tok, nil)
	listed := false
	list, _ := plans["plans"].([]any)
	for _, p := range list { // other tests of the package leave plans behind: find ours
		if p, _ := p.(map[string]any); p["id"] == plan.ID {
			listed = p["currency"] == "USDT" && p["scale"] == float64(6) && p["price"] == float64(4_000_000)
		}
	}
	if !listed {
		t.Fatalf("the plan is not listed in USDT: %v", list)
	}
	code, o := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": plan.ID, "key": "usdtorder001"})
	if code != 200 || o["currency"] != "USDT" || o["amount"] != float64(4_000_000) || o["list_amount"] != nil {
		t.Fatalf("create order: %d %v", code, o)
	}
	id := o["id"].(string)

	// The quote: the USDT price, the rate, the Toman, the wallet, and how long it holds. It changes nothing.
	code, q := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", tok, nil)
	if code != 200 || q["order_id"] != id || q["list_amount"] != float64(4_000_000) || q["list_currency"] != "USDT" || q["list_scale"] != float64(6) ||
		q["rate"] != float64(300_000) || q["amount"] != float64(1_200_000) || q["currency"] != "IRT" || q["scale"] != float64(0) ||
		q["expires_in"] != float64(120) || q["balance"] != float64(0) || q["credit_limit"] != float64(0) || q["enough"] != false {
		t.Fatalf("quote: %d %v", code, q)
	}
	token, _ := q["quote"].(string)
	if token == "" || q["expires_at"].(float64) < float64(time.Now().Unix()) {
		t.Fatalf("quote token and time: %v", q)
	}
	if _, o2 := m.call(http.MethodGet, "/api/mini/v1/orders/"+id, tok, nil); o2["currency"] != "USDT" || o2["amount"] != float64(4_000_000) {
		t.Fatalf("a quote changed the order: %v", o2)
	}

	// No quote, or a made-up one, charges nothing; neither does an empty wallet.
	m.fund(me.ID, 1_000_000)
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{}); code != 409 || out["error"] != "quote_expired" {
		t.Fatalf("pay with no quote: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, nil); code != 409 || out["error"] != "quote_expired" {
		t.Fatalf("pay with no body: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{"quote": "made.up"}); code != 400 || out["error"] != "invalid" {
		t.Fatalf("pay with a made-up quote: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{"quote": token, "price": 1}); code != 400 {
		t.Fatalf("a field the call does not take: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{"quote": token}); code != 409 || out["error"] != "insufficient_funds" {
		t.Fatalf("pay with too little: %d %v", code, out)
	}
	if m.wallet(me.ID) != 1_000_000 {
		t.Fatalf("the wallet moved: %d", m.wallet(me.ID))
	}
	if _, o2 := m.call(http.MethodGet, "/api/mini/v1/orders/"+id, tok, nil); o2["currency"] != "USDT" || o2["status"] != "awaiting_payment" || o2["list_amount"] != nil {
		t.Fatalf("a payment that failed changed the order: %v", o2)
	}

	// With the money, the quote pays what it said, in Toman, and the order says what it was listed at.
	m.fund(me.ID, 2_000_000)
	code, paid := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{"quote": token})
	if code != 200 || paid["status"] != "paid" || paid["amount"] != float64(1_200_000) || paid["currency"] != "IRT" ||
		paid["list_amount"] != float64(4_000_000) || paid["list_currency"] != "USDT" || paid["rate"] != float64(300_000) || paid["list_scale"] != float64(6) {
		t.Fatalf("pay: %d %v", code, paid)
	}
	// A second tap, with the quote or without, charges nothing and answers the same.
	for _, body := range []any{map[string]any{"quote": token}, map[string]any{}, nil} {
		if code, again := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, body); code != 200 || again["status"] != "paid" || again["amount"] != float64(1_200_000) {
			t.Fatalf("pay again with %v: %d %v", body, code, again)
		}
	}
	if m.wallet(me.ID) != 800_000 {
		t.Fatalf("the wallet after one purchase of 1,200,000: %d", m.wallet(me.ID))
	}
	// The quote of a paid order is refused.
	if code, out := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", tok, nil); code != 409 || out["error"] != "not_payable" {
		t.Fatalf("quote of a paid order: %d %v", code, out)
	}
	if code, o2 := m.call(http.MethodGet, "/api/mini/v1/orders/"+id, tok, nil); code != 200 || o2["amount"] != float64(1_200_000) || o2["list_amount"] != float64(4_000_000) {
		t.Fatalf("poll: %d %v", code, o2)
	}
}

// Without a USDT rate nothing is quoted: the customer is told, and the order is untouched.
func TestMiniAppQuotesNothingWithoutARate(t *testing.T) {
	m := newMiniFixture(t, true)
	plan := m.usdtPlan("Gold USDT", 4_000_000)
	tok := m.signIn("tg:7010:sara")
	_, o := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": plan.ID, "key": "norateorder1"})
	id := o["id"].(string)
	m.fund(m.user(7010).ID, 5_000_000)
	if code, out := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", tok, nil); code != 409 || out["error"] != "no_rate" {
		t.Fatalf("quote without a rate: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{}); code != 409 || out["error"] != "quote_expired" {
		t.Fatalf("pay without a rate: %d %v", code, out)
	}
	if m.wallet(m.user(7010).ID) != 5_000_000 {
		t.Fatal("the wallet moved")
	}
	// The rate comes back: the same order is quoted and paid.
	m.usdtRate("280000")
	code, q := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", tok, nil)
	if code != 200 || q["amount"] != float64(1_120_000) || q["enough"] != true {
		t.Fatalf("quote with a rate: %d %v", code, q)
	}
	if code, paid := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{"quote": q["quote"]}); code != 200 || paid["amount"] != float64(1_120_000) {
		t.Fatalf("pay: %d %v", code, paid)
	}
}

// A quote is the customer's own, for a USDT order only, and not while the store is closed.
func TestMiniAppQuoteIsTheCustomersOwn(t *testing.T) {
	m := newMiniFixture(t, true)
	m.usdtRate("300000")
	usdt := m.usdtPlan("Gold USDT", 4_000_000)
	irt := m.plan("Gold", 30, 50<<30, 100000)
	alice, bob := m.signIn("tg:7020:alice"), m.signIn("tg:7021:bob")
	_, o := m.call(http.MethodPost, "/api/mini/v1/orders", alice, map[string]any{"plan_id": usdt.ID, "key": "alice-usdt-1"})
	id := o["id"].(string)
	_, q := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", alice, nil)
	m.fund(m.user(7021).ID, 5_000_000)
	// Bob can neither quote nor pay Alice's order, with her quote or his own.
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", bob, nil); code != 404 {
		t.Errorf("another customer's quote: %d", code)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", bob, map[string]any{"quote": q["quote"]}); code != 404 {
		t.Errorf("another customer's payment: %d", code)
	}
	// A quote belongs to its order: Bob's own order cannot be paid with Alice's quote.
	_, bo := m.call(http.MethodPost, "/api/mini/v1/orders", bob, map[string]any{"plan_id": usdt.ID, "key": "bob-usdt-001"})
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+bo["id"].(string)+"/pay-wallet", bob, map[string]any{"quote": q["quote"]}); code != 400 || out["error"] != "invalid" {
		t.Errorf("another order's quote: %d %v", code, out)
	}
	if m.wallet(m.user(7021).ID) != 5_000_000 {
		t.Error("Bob's wallet moved")
	}
	// An order priced in Toman has no quote.
	_, io := m.call(http.MethodPost, "/api/mini/v1/orders", alice, map[string]any{"plan_id": irt.ID, "key": "alice-irt-001"})
	if code, out := m.call(http.MethodGet, "/api/mini/v1/orders/"+io["id"].(string)+"/wallet-quote", alice, nil); code != 409 || out["error"] != "state" {
		t.Errorf("a quote of a Toman order: %d %v", code, out)
	}
	// Bad ids are not found, and a quote needs a session.
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/orders/nope/wallet-quote", alice, nil); code != 404 {
		t.Errorf("a bad id: %d", code)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", "", nil); code != 401 {
		t.Errorf("no session: %d", code)
	}
	// While the store is closed for maintenance, no quote and no payment.
	m.setting("maintenance.enabled", "true")
	if code, out := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", alice, nil); code != 503 || out["error"] != "maintenance" {
		t.Errorf("quote in maintenance: %d %v", code, out)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", alice, map[string]any{"quote": q["quote"]}); code != 503 || out["error"] != "maintenance" {
		t.Errorf("pay in maintenance: %d %v", code, out)
	}
}

// The quote the customer saw is honoured for two minutes whatever the rate does, and refused after that with the code that has the app ask again.
func TestMiniAppQuoteHoldsForTwoMinutes(t *testing.T) {
	m := newMiniFixture(t, true)
	clock := &miniClock{t: time.Now()}
	m.dom.SetClock(clock.now)
	m.usdtRate("300000")
	plan := m.usdtPlan("Gold USDT", 4_000_000)
	tok := m.signIn("tg:7030:neda")
	m.fund(m.user(7030).ID, 5_000_000)
	order := func(key string) string {
		_, o := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": plan.ID, "key": key})
		return o["id"].(string)
	}
	quote := func(id string) map[string]any {
		code, q := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", tok, nil)
		if code != 200 {
			t.Fatalf("quote: %d %v", code, q)
		}
		return q
	}
	a, b := order("hold-order-a"), order("hold-order-b")
	qa, qb := quote(a), quote(b)
	m.usdtRate("330000") // the rate goes up 10% between the preview and the payment
	clock.advance(119 * time.Second)
	if code, paid := m.call(http.MethodPost, "/api/mini/v1/orders/"+a+"/pay-wallet", tok, map[string]any{"quote": qa["quote"]}); code != 200 || paid["amount"] != float64(1_200_000) || paid["rate"] != float64(300_000) {
		t.Fatalf("the previewed price within two minutes: %d %v", code, paid)
	}
	clock.advance(2 * time.Second)
	if code, out := m.call(http.MethodPost, "/api/mini/v1/orders/"+b+"/pay-wallet", tok, map[string]any{"quote": qb["quote"]}); code != 409 || out["error"] != "quote_expired" {
		t.Fatalf("a quote after two minutes: %d %v", code, out)
	}
	if m.wallet(m.user(7030).ID) != 3_800_000 {
		t.Fatalf("the wallet: %d", m.wallet(m.user(7030).ID))
	}
	// The app asks again and is shown the price at the new rate.
	q2 := quote(b)
	if q2["rate"] != float64(330_000) || q2["amount"] != float64(1_320_000) || q2["quote"] == qb["quote"] {
		t.Fatalf("the new quote: %v", q2)
	}
	if code, paid := m.call(http.MethodPost, "/api/mini/v1/orders/"+b+"/pay-wallet", tok, map[string]any{"quote": q2["quote"]}); code != 200 || paid["amount"] != float64(1_320_000) {
		t.Fatalf("pay at the new quote: %d %v", code, paid)
	}
}

type miniClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *miniClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *miniClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// Many taps at once on the same quote charge once.
func TestMiniAppQuoteDoubleTapChargesOnce(t *testing.T) {
	m := newMiniFixture(t, true)
	m.usdtRate("300000")
	plan := m.usdtPlan("Gold USDT", 4_000_000)
	tok := m.signIn("tg:7040:omid")
	m.fund(m.user(7040).ID, 3_000_000)
	_, o := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": plan.ID, "key": "tap-order-001"})
	id := o["id"].(string)
	_, q := m.call(http.MethodGet, "/api/mini/v1/orders/"+id+"/wallet-quote", tok, nil)
	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// five clients of their own: the limit on changes is per customer, 20 a minute
			codes[i], _ = m.callFrom(http.MethodPost, "/api/mini/v1/orders/"+id+"/pay-wallet", tok, map[string]any{"quote": q["quote"]}, fmt.Sprintf("198.51.100.%d", i+1))
		}()
	}
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Errorf("tap %d: %d", i, c)
		}
	}
	if got := m.wallet(m.user(7040).ID); got != 1_800_000 {
		t.Fatalf("charged more than once: the wallet holds %d, want 1,800,000", got)
	}
	var purchases int
	if err := m.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.ledger_entries WHERE kind = 'purchase' AND ref_id = $1`, id).Scan(&purchases); err != nil || purchases != 1 {
		t.Fatalf("purchase entries: %d %v", purchases, err)
	}
}

// A top-up in USDT: the amount is a whole number of USDT in micro-USDT, within the bot's bounds; the answer carries the address, the exact amount, a QR code
// of the address and when the window ends; and the Toman top-up is as it was.
func TestMiniAppTopsUpInUsdt(t *testing.T) {
	m := newMiniFixture(t, true)
	pay := &miniUsdtPay{}
	m.api.cfg.Payments = pay
	tok := m.signIn("tg:7050:reza")

	// The app learns the bounds from /me.
	_, me := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil)
	top, _ := me["top_up"].(map[string]any)
	usdt, _ := top["usdt"].(map[string]any)
	if top["currency"] != "IRT" || top["min"] != float64(10_000) || usdt["currency"] != "USDT" || usdt["min"] != float64(1_000_000) ||
		usdt["max"] != float64(10_000_000_000) || usdt["step"] != float64(1_000_000) || usdt["scale"] != float64(6) {
		t.Fatalf("top_up: %v", top)
	}
	// The methods for USDT: automatic USDT.
	if code, out := m.call(http.MethodGet, "/api/mini/v1/methods?currency=USDT&amount=10000000", tok, nil); code != 200 || len(out["methods"].([]any)) != 1 || out["methods"].([]any)[0] != "usdt_trc20_auto" {
		t.Fatalf("methods for USDT: %d %v", code, out)
	}

	code, in := m.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 10_000_000, "currency": "USDT", "method": "usdt_trc20_auto", "key": "usdt-topup-1"})
	if code != 200 || in["intent_id"] != miniIntentID || in["status"] != "pending" || in["provider"] != "usdt_trc20_auto" || in["order_id"] != "" ||
		in["amount"] != float64(10_000_000) || in["currency"] != "USDT" || in["scale"] != float64(6) ||
		in["gateway_amount"] != float64(10_000_417) || in["gateway_currency"] != "USDT" || in["scale_gateway"] != float64(6) {
		t.Fatalf("top-up: %d %v", code, in)
	}
	details, _ := in["details"].(map[string]any)
	if details["pay_address"] != miniTronAddress || details["network"] != "" && details["network"] != "TRC20" {
		t.Fatalf("details: %v", details)
	}
	if qr, _ := in["qr"].(string); !strings.HasPrefix(qr, "data:image/png;base64,") || len(qr) < 400 {
		t.Fatalf("qr: %q", in["qr"])
	}
	pay.mu.Lock()
	asked := append([]domain.GatewayStart(nil), pay.asked...)
	pay.mu.Unlock()
	if len(asked) != 1 || asked[0].Amount != 10_000_000 || asked[0].Currency != "USDT" || asked[0].OrderID != "" || asked[0].GatewayCurrency != "USDT" ||
		asked[0].GatewayAmount != 10_000_000 || asked[0].UserID != m.user(7050).ID {
		t.Fatalf("what payments was asked: %+v", asked)
	}

	// Amounts the bot would not take, and things this call does not do.
	for name, body := range map[string]map[string]any{
		"under 1 USDT":        {"amount": 500_000, "currency": "USDT", "method": "usdt_trc20_auto", "key": "usdt-topup-2"},
		"zero":                {"amount": 0, "currency": "USDT", "method": "usdt_trc20_auto", "key": "usdt-topup-2"},
		"over 10,000 USDT":    {"amount": 10_001_000_000, "currency": "USDT", "method": "usdt_trc20_auto", "key": "usdt-topup-2"},
		"not a whole USDT":    {"amount": 1_500_000, "currency": "USDT", "method": "usdt_trc20_auto", "key": "usdt-topup-2"},
		"negative":            {"amount": -1_000_000, "currency": "USDT", "method": "usdt_trc20_auto", "key": "usdt-topup-2"},
		"another currency":    {"amount": 10_000_000, "currency": "EUR", "method": "usdt_trc20_auto", "key": "usdt-topup-2"},
		"a bad key":           {"amount": 10_000_000, "currency": "USDT", "method": "usdt_trc20_auto", "key": "x"},
	} {
		if code, out := m.call(http.MethodPost, "/api/mini/v1/topup", tok, body); code != 400 {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	// A method the store does not offer for USDT (Zarinpal takes Toman only) is refused.
	if code, out := m.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 10_000_000, "currency": "USDT", "method": "zarinpal", "key": "usdt-topup-3"}); code != 400 || out["error"] != "method_off" {
		t.Errorf("Zarinpal for USDT: %d %v", code, out)
	}
	pay.mu.Lock()
	n := len(pay.asked)
	pay.mu.Unlock()
	if n != 1 {
		t.Fatalf("payments was asked %d times; the refused top-ups must not reach it", n)
	}
}

// The Toman top-up needs no currency (the app of before sends none) and still has the Toman bounds.
func TestMiniAppTomanTopupIsUnchanged(t *testing.T) {
	m := newMiniFixture(t, true)
	tok := m.signIn("tg:7060:mina")
	code, in := m.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 100_000, "method": "zarinpal", "key": "toman-topup-1"})
	if code != 200 || in["amount"] != float64(100_000) || in["currency"] != "IRT" || in["pay_url"] == "" || in["qr"] != nil {
		t.Fatalf("top-up: %d %v", code, in)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/topup", tok, map[string]any{"amount": 5_000, "currency": "IRT", "method": "zarinpal", "key": "toman-topup-2"}); code != 400 {
		t.Errorf("under the minimum: %d", code)
	}
	if len(m.pay.started) != 1 || m.pay.started[0].Currency != "IRT" {
		t.Fatalf("started: %+v", m.pay.started)
	}
}
