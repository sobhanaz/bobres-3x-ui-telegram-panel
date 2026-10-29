package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonfake"
)

// Automatic TON end to end, through the real payments service, the real TON Center client and the real price
// feed against the fakes: the owner sets it up on the dashboard (the key never comes back, the address is kept in
// the form customers are shown), a Toman price becomes USDT at the store's rate and TON at the price payments
// read, the Mini App shows the amount with its 9 decimals and the comment, the exact amount with the comment pays
// the order once however often it is read, less than asked reaches the staff as an unmatched deposit (once) and
// is resolved on the dashboard.
func TestTONEndToEnd(t *testing.T) {
	m := newMiniFixture(t, true)
	chain := tonfake.New()
	t.Cleanup(chain.Close)
	chain.OnlyAPIKey(tonAPIKey)
	pay, ps, svc := startPaymentsWith(t, nil, chain)
	m.api.cfg.Gateways, m.api.cfg.Payments, m.api.cfg.Env = pay, pay, "prod"
	ctx := context.Background()
	b := m.browser()
	if code := b.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	mark := outboxMark(t, m.fixture)
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}

	tonCard := func() map[string]any {
		_, out := b.do(http.MethodGet, "/api/v1/gateways", nil)
		for _, p := range out["providers"].([]any) {
			if c := p.(map[string]any); c["provider"] == "ton_auto" {
				return c
			}
		}
		t.Fatalf("no TON card: %v", out)
		return nil
	}
	if c := tonCard(); c["configured"] != false || c["address"] != "" || c["price"].(map[string]any)["fresh"] != true {
		t.Fatalf("before: %v", c)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/ton_auto", map[string]any{"enabled": true}); code != 400 || out["field"] != "address" {
		t.Fatalf("on without an address: %d %v", code, out)
	}
	code, out := b.do(http.MethodPut, "/api/v1/gateways/ton_auto", map[string]any{"enabled": true, "address": tonPasted(), "api_key": tonAPIKey})
	if code != 200 || strings.Contains(asJSON(out), tonAPIKey) {
		t.Fatalf("save: %d %v", code, out)
	}
	if c := tonCard(); c["offered"] != true || c["address"] != tonShown() || c["key_set"] != true {
		t.Fatalf("after: %v", c)
	}
	var keyEnc []byte
	if err := ps.DB().QueryRow(ctx, `SELECT api_key_enc FROM payments.gateway_configs WHERE provider = 'ton_auto'`).Scan(&keyEnc); err != nil ||
		len(keyEnc) == 0 || strings.Contains(string(keyEnc), tonAPIKey) {
		t.Fatalf("key at rest: %v", err)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/ton_auto/test", map[string]any{}); code != 200 || out["ok"] != true || out["latest_tx_at"] != nil {
		t.Fatalf("test stored: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/ton_auto/test", map[string]any{"api_key": "ton-wrong-ton-wrong"}); code != 200 ||
		out["ok"] != false || strings.Contains(asJSON(out), "ton-wrong") {
		t.Fatalf("test another key: %d %v", code, out)
	}

	// Offered for a Toman price only once the store has a USDT rate.
	m.setting("payments.usdt_rate", "60000")
	if ms, err := m.dom.PaymentMethods(ctx, pay, "IRT", 150_000); err != nil || !slices.Contains(ms, domain.TONAuto) {
		t.Fatalf("methods: %v %v", ms, err)
	}

	// The Mini App: 150,000 Toman at 60,000 is 2.5 USDT, at 3.2145 to 3.2150 USDT per TON.
	p := m.plan("TON plan", 30, 50<<30, 150_000)
	tok := m.signIn("tg:61:tonpayer")
	_, o := m.call(http.MethodPost, "/api/mini/v1/orders", tok, map[string]any{"plan_id": p.ID, "key": "tonorderkey1"})
	orderID, _ := o["id"].(string)
	code, res := m.call(http.MethodPost, "/api/mini/v1/orders/"+orderID+"/pay", tok, map[string]any{"method": "ton_auto"})
	details, _ := res["details"].(map[string]any)
	nano, _ := res["gateway_amount"].(float64)
	if code != 200 || res["gateway_currency"] != "TON" || res["scale_gateway"] != float64(9) || details["pay_address"] != tonShown() ||
		details["network"] != "TON" || nano < 777_604_976 || nano > 777_726_247 {
		t.Fatalf("mini app pay: %d %v", code, res)
	}
	memo, _ := details["memo"].(string)
	expires, _ := strconv.ParseInt(details["expires_at"].(string), 10, 64)
	if !strings.HasPrefix(memo, "BOB-") || len(memo) != 12 || time.Until(time.Unix(expires, 0)) < 25*time.Minute || time.Until(time.Unix(expires, 0)) > 31*time.Minute {
		t.Fatalf("memo %q, expires %v", memo, time.Unix(expires, 0))
	}
	intentID := res["intent_id"].(string)
	customer, err := m.st.GetUserByTelegramID(ctx, m.st.Conn(), 61)
	if err != nil {
		t.Fatal(err)
	}

	// Less than asked, with the comment: nothing is credited; the staff hear of it once.
	short := chain.Pay(tonShown(), int64(nano)-1, memo)
	if _, err := svc.ScanTONOnce(ctx); err != nil {
		t.Fatal(err)
	}
	deliverPayments(t, m.fixture, ps)
	alerts := coreOutbox(t, m.fixture, mark, events.DepositUnmatched)
	var dep events.DepositUnmatchedEvent
	if len(alerts) != 1 || json.Unmarshal(alerts[0], &dep) != nil || dep.TxID != short || dep.Network != "TON" || dep.Currency != "TON" ||
		dep.Amount != int64(nano)-1 || dep.Reason != events.UnmatchedUnderpaid || dep.Comment != memo {
		t.Fatalf("staff alert: %s", alerts)
	}
	if gi, err := m.dom.CheckPayment(ctx, pay, customer.ID, intentID); err != nil || gi.Status != "pending" {
		t.Fatalf("underpaid: %+v %v", gi, err)
	}

	// The exact amount with the comment, read three times: paid once.
	paidTx := chain.Pay(tonShown(), int64(nano), memo)
	for range 3 {
		if _, err := svc.ScanTONOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	gi, err := m.dom.CheckPayment(ctx, pay, customer.ID, intentID)
	if err != nil || gi.Status != "succeeded" || domain.GatewayDetails(gi)["txid"] != paidTx {
		t.Fatalf("paid: %+v %v", gi, err)
	}
	deliverPayments(t, m.fixture, ps)
	got, err := m.st.GetOrder(ctx, m.st.Conn(), orderID)
	if err != nil || !slices.Contains([]string{"paid", "provisioning", "active", "provision_failed"}, got.Status) {
		t.Fatalf("order: %+v %v", got, err)
	}
	paid := coreOutbox(t, m.fixture, mark, events.OrderPaid)
	var op events.OrderPaidEvent
	if len(paid) != 1 || json.Unmarshal(paid[0], &op) != nil || op.Provider != domain.TONAuto || op.Amount != 150_000 || op.Currency != "IRT" {
		t.Fatalf("order paid: %s", paid)
	}
	var credits int64
	if err := m.st.DB().QueryRow(ctx, `SELECT count(*) FROM core.ledger_entries WHERE user_id = $1 AND amount > 0`, customer.ID).Scan(&credits); err != nil || credits != 1 {
		t.Fatalf("credited %d times (%v)", credits, err)
	}

	// The dashboard: the short transfer to credit by hand, then resolved; the paid one in the TON log.
	code, out = b.do(http.MethodGet, "/api/v1/gateways/report?provider=ton_auto", nil)
	unmatched, _ := out["unmatched"].([]any)
	if code != 200 || len(unmatched) != 1 || unmatched[0].(map[string]any)["txid"] != short || unmatched[0].(map[string]any)["comment"] != memo ||
		out["healthy"] != true {
		t.Fatalf("report: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"network": "TON", "txid": short,
		"note": "refunded"}); code != 200 || out["deposit"].(map[string]any)["outcome"] != "resolved" {
		t.Fatalf("resolve: %d %v", code, out)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/gateways/report?provider=ton_auto", nil); out["unmatched_count"] != float64(0) {
		t.Fatalf("after resolving: %v", out)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/gateways/deposits?network=TON&txid="+paidTx, nil); code != 200 || len(items(out)) != 1 ||
		items(out)[0]["outcome"] != "matched" || items(out)[0]["intent_id"] != intentID {
		t.Fatalf("the paid transfer: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/ton_auto/test", map[string]any{}); code != 200 || out["latest_tx_at"] == nil {
		t.Fatalf("latest transaction: %d %v", code, out)
	}
	if n := len(coreOutbox(t, m.fixture, mark, events.DepositUnmatched)); n != 1 {
		t.Fatalf("staff alerts: %d", n)
	}
}
