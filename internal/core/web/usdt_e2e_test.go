package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	paystore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tronfake"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// deliverPayments hands core the payments feed as cmd/core does, twice (a
// redelivery must change nothing), and moves the cursor past it.
func deliverPayments(t *testing.T, f *fixture, ps *paystore.Store) []eventbus.Message {
	t.Helper()
	ctx := context.Background()
	feed := eventbus.NewFeed(ps.DB(), "outbox_payments")
	msgs, err := feed.Pending(ctx, "core-e2e", 100)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		for _, m := range msgs {
			if err := f.dom.HandlePaymentEvent(ctx, m); err != nil {
				t.Fatalf("%s: %v", m.Topic, err)
			}
		}
	}
	if len(msgs) > 0 {
		if err := feed.Ack(ctx, "core-e2e", msgs[len(msgs)-1].Seq); err != nil {
			t.Fatal(err)
		}
	}
	return msgs
}

// outboxMark is where core's outbox ends now: the package's other tests
// (and earlier runs of this one, with the fake chain's same transaction ids)
// published before it.
func outboxMark(t *testing.T, f *fixture) int64 {
	t.Helper()
	var id int64
	if err := f.st.DB().QueryRow(context.Background(), `SELECT coalesce(max(id), 0) FROM core.outbox_core`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// coreOutbox is what core published on topic after mark.
func coreOutbox(t *testing.T, f *fixture, mark int64, topic string) []json.RawMessage {
	t.Helper()
	rows, err := f.st.DB().Query(context.Background(), `SELECT payload FROM core.outbox_core WHERE id > $1 AND topic = $2 ORDER BY id`,
		mark, topic)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var p json.RawMessage
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// Automatic USDT end to end, through the real payments service and the real
// TronGrid client against the fake chain: the owner sets it up on the
// dashboard (the key never comes back), two customers paying the same price
// get different exact amounts, a fake "USDT" token pays nothing, the exact
// amount pays the order once however often it is seen, a wrong amount
// reaches the staff as an unmatched deposit (once) and is resolved on the
// dashboard, and neither transaction can be used again as a manual proof.
func TestUSDTEndToEnd(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	chain := tronfake.New()
	t.Cleanup(chain.Close)
	chain.RequireKey(usdtKey)
	pay, ps, svc := startPayments(t, chain)
	f.api.cfg.Gateways, f.api.cfg.Payments, f.api.cfg.Env = pay, pay, "prod"
	ctx := context.Background()
	own := owner(t, f)
	mark := outboxMark(t, f)

	// Set up on the dashboard.
	_, out := b.do(http.MethodGet, "/api/v1/gateways", nil)
	usdt := out["providers"].([]any)[1].(map[string]any)
	if usdt["provider"] != "usdt_trc20_auto" || usdt["configured"] != false || usdt["address"] != "" {
		t.Fatalf("before: %v", usdt)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/usdt_trc20_auto", map[string]any{"enabled": true}); code != 400 || out["field"] != "address" {
		t.Fatalf("on without an address: %d %v", code, out)
	}
	code, out := b.do(http.MethodPut, "/api/v1/gateways/usdt_trc20_auto", map[string]any{"enabled": true, "address": usdtStore,
		"api_key": usdtKey, "clear_api_key": false})
	if code != 200 || strings.Contains(asJSON(out), usdtKey) {
		t.Fatalf("save: %d %v", code, out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/gateways", nil)
	usdt = out["providers"].([]any)[1].(map[string]any)
	if usdt["offered"] != true || usdt["address"] != usdtStore || usdt["key_set"] != true || strings.Contains(asJSON(out), usdtKey) {
		t.Fatalf("after: %v", usdt)
	}
	var keyEnc []byte
	if err := ps.DB().QueryRow(ctx, `SELECT api_key_enc FROM payments.gateway_configs WHERE provider = 'usdt_trc20_auto'`).Scan(&keyEnc); err != nil ||
		len(keyEnc) == 0 || strings.Contains(string(keyEnc), usdtKey) {
		t.Fatalf("key at rest: %v", err)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/usdt_trc20_auto/test", map[string]any{}); code != 200 || out["ok"] != true {
		t.Fatalf("test stored: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/usdt_trc20_auto/test", map[string]any{"address": usdtStore,
		"api_key": "00000000-dead-beef-0000-000000000000"}); code != 200 || out["ok"] != false || strings.Contains(asJSON(out), "dead-beef") {
		t.Fatalf("test another key: %d %v", code, out)
	}
	var audit string
	if err := f.st.DB().QueryRow(ctx, `SELECT string_agg(coalesce(before::text,'') || coalesce(after::text,''), ' ') FROM core.audit_log WHERE entity = 'gateway'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, usdtKey) || !strings.Contains(audit, `"api_key": "changed"`) || !strings.Contains(audit, usdtStore) {
		t.Fatalf("audit: %s", audit)
	}

	// Priced from the rate; offered for a Toman plan only once it is set.
	if ms, err := f.dom.PaymentMethods(ctx, pay, "IRT", 150_000); err != nil || len(ms) != 0 {
		t.Fatalf("methods without a rate: %v %v", ms, err)
	}
	if err := f.dom.SetSetting(ctx, own, "payments.usdt_rate", "60000"); err != nil {
		t.Fatal(err)
	}
	if ms, err := f.dom.PaymentMethods(ctx, pay, "IRT", 150_000); err != nil || len(ms) != 1 || ms[0] != domain.USDTAuto {
		t.Fatalf("methods: %v %v", ms, err)
	}

	// Two customers pay the same price: one exact amount each.
	u1, u2 := f.customer(8961, "payer1"), f.customer(8962, "payer2")
	plan := f.plan("Monthly", 30, 0, 150_000)
	o1, err := f.dom.CreateOrder(ctx, domain.CreateOrderParams{UserID: u1.ID, PlanID: plan.ID, IdempotencyKey: "usdt-o1"})
	if err != nil {
		t.Fatal(err)
	}
	r1, err := f.dom.CreatePaymentIntent(ctx, pay, domain.CreatePaymentIntentParams{UserID: u1.ID, OrderID: o1.ID, Provider: domain.USDTAuto,
		IdempotencyKey: "usdt-i1"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.dom.CreatePaymentIntent(ctx, pay, domain.CreatePaymentIntentParams{UserID: u2.ID, Provider: domain.USDTAuto,
		Amount: 150_000, Currency: "IRT", IdempotencyKey: "usdt-i2"})
	if err != nil {
		t.Fatal(err)
	}
	expires, _ := strconv.ParseInt(r1.Details["expires_at"], 10, 64)
	for _, r := range []*domain.IntentResult{r1, r2} {
		if r.GatewayCurrency != "USDT" || r.GatewayAmount <= 2_500_000 || r.GatewayAmount > 2_509_999 || r.GatewayAmount%10_000 == 0 ||
			r.Details["pay_address"] != usdtStore ||
			r.Details["network"] != "TRC20" || r.Amount != 150_000 || r.Currency != "IRT" {
			t.Fatalf("intent: %+v", r)
		}
	}
	if r1.GatewayAmount == r2.GatewayAmount || time.Until(time.Unix(expires, 0)) < 40*time.Minute || time.Until(time.Unix(expires, 0)) > 46*time.Minute {
		t.Fatalf("amounts %d/%d, expires %v", r1.GatewayAmount, r2.GatewayAmount, time.Unix(expires, 0))
	}
	again, err := f.dom.CreatePaymentIntent(ctx, pay, domain.CreatePaymentIntentParams{UserID: u1.ID, OrderID: o1.ID, Provider: domain.USDTAuto,
		IdempotencyKey: "usdt-i1"})
	if err != nil || again.IntentID != r1.IntentID || again.GatewayAmount != r1.GatewayAmount {
		t.Fatalf("retry: %+v %v", again, err)
	}

	// A token that calls itself USDT, with the exact amount: nothing.
	chain.LeakOtherTokens(true)
	chain.Add(tronfake.Transfer{To: usdtStore, Token: usdtOther, Value: strconv.FormatInt(r1.GatewayAmount, 10)})
	if _, err := svc.ScanChainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if gi, err := f.dom.CheckPayment(ctx, pay, u1.ID, r1.IntentID); err != nil || gi.Status != "pending" {
		t.Fatalf("fake token: %+v %v", gi, err)
	}

	// The exact amount, seen by reads running at once: paid once.
	paidTx := chain.Pay(usdtStore, r1.GatewayAmount)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() { _, _ = svc.ScanChainOnce(ctx) })
	}
	wg.Wait()
	gi, err := f.dom.CheckPayment(ctx, pay, u1.ID, r1.IntentID)
	if err != nil || gi.Status != "succeeded" || gi.ExternalID != paidTx || domain.GatewayDetails(gi)["txid"] != paidTx {
		t.Fatalf("paid: %+v %v", gi, err)
	}
	deliverPayments(t, f, ps)
	got, err := f.st.GetOrder(ctx, f.st.Conn(), o1.ID)
	if err != nil || !slices.Contains([]string{"paid", "provisioning", "active", "provision_failed"}, got.Status) {
		t.Fatalf("order: %+v %v", got, err)
	}
	paid := coreOutbox(t, f, mark, events.OrderPaid)
	var op events.OrderPaidEvent
	if len(paid) != 1 || json.Unmarshal(paid[0], &op) != nil || op.Source != events.PaidByGateway || op.Amount != 150_000 || op.Currency != "IRT" {
		t.Fatalf("order paid: %s", paid)
	}
	var credits, sum int64
	if err := f.st.DB().QueryRow(ctx, `SELECT count(*), coalesce(sum(amount), 0) FROM core.ledger_entries WHERE user_id = $1 AND amount > 0`, u1.ID).
		Scan(&credits, &sum); err != nil || credits != 1 || sum != 150_000 {
		t.Fatalf("credited %d times, %d in all (%v)", credits, sum, err)
	}

	// The second customer overpays by a micro-USDT: no exact match, so no
	// credit; staff hear of it once, and see it on the dashboard.
	wrong := r2.GatewayAmount + 1 // offsets are random: never the first customer's (paid) amount
	if wrong == r1.GatewayAmount {
		wrong = r2.GatewayAmount - 1
	}
	wrongTx := chain.Pay(usdtStore, wrong)
	if _, err := svc.ScanChainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	deliverPayments(t, f, ps)
	alerts := coreOutbox(t, f, mark, events.DepositUnmatched)
	var dep events.DepositUnmatchedEvent
	if len(alerts) != 1 || json.Unmarshal(alerts[0], &dep) != nil || dep.TxID != wrongTx || dep.Amount != wrong ||
		dep.Currency != "USDT" || dep.To != usdtStore || dep.From != tronfake.Sender || dep.Reason != events.UnmatchedNoPayment {
		t.Fatalf("staff alert: %s", alerts)
	}
	if gi, err := f.dom.CheckPayment(ctx, pay, u2.ID, r2.IntentID); err != nil || gi.Status != "pending" {
		t.Fatalf("overpaid intent: %+v %v", gi, err)
	}
	var w2 int64
	if err := f.st.DB().QueryRow(ctx, `SELECT count(*) FROM core.ledger_entries WHERE user_id = $1`, u2.ID).Scan(&w2); err != nil || w2 != 0 {
		t.Fatalf("u2 ledger rows: %d %v", w2, err)
	}
	now := time.Now().Unix()
	code, out = b.do(http.MethodGet, "/api/v1/gateways/report?provider=usdt_trc20_auto&from="+itoa(now-86400)+"&to="+itoa(now+60), nil)
	unmatched, _ := out["unmatched"].([]any)
	if code != 200 || len(unmatched) != 1 || out["unmatched_count"] != float64(1) || out["healthy"] != true || out["last_scan_ok_at"] == nil ||
		unmatched[0].(map[string]any)["txid"] != wrongTx || unmatched[0].(map[string]any)["reason_code"] != "no_payment" {
		t.Fatalf("report: %d %v", code, out)
	}
	if !strings.Contains(asJSON(out["totals"]), `"status":"succeeded"`) {
		t.Fatalf("totals: %v", out["totals"])
	}
	code, out = b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"txid": wrongTx, "event_index": 0,
		"note": "refunded to the sender"})
	if code != 200 || out["deposit"].(map[string]any)["outcome"] != "resolved" {
		t.Fatalf("resolve: %d %v", code, out)
	}
	if _, out := b.do(http.MethodGet, "/api/v1/gateways/report?provider=usdt_trc20_auto", nil); out["unmatched_count"] != float64(0) {
		t.Fatalf("after resolving: %v", out)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/gateways/deposits?txid="+paidTx, nil); code != 200 || len(items(out)) != 1 ||
		items(out)[0]["outcome"] != "matched" || items(out)[0]["intent_id"] != r1.IntentID {
		t.Fatalf("the paid transfer: %d %v", code, out)
	}

	// Neither transaction pays anything again as a manual proof.
	man, err := f.dom.CreatePaymentIntent(ctx, pay, domain.CreatePaymentIntentParams{UserID: u2.ID, Provider: "manual_crypto",
		Amount: 150_000, Currency: "IRT", IdempotencyKey: "usdt-man-1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tx := range []string{paidTx, "0x" + strings.ToUpper(paidTx), wrongTx} {
		_, err := f.dom.SubmitPaymentProof(ctx, pay, domain.ProofParams{UserID: u2.ID, IntentID: man.IntentID, Network: "TRC20", TXID: tx})
		if status.Code(unwrapAll(err)) != codes.AlreadyExists {
			t.Errorf("manual proof %s: %v", tx, err)
		}
	}

	// The second customer's exact amount still pays their top-up, once.
	chain.Pay(usdtStore, r2.GatewayAmount)
	if _, err := svc.ScanChainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	deliverPayments(t, f, ps)
	wal, err := f.st.GetWallet(ctx, f.st.Conn(), u2.ID, "IRT")
	if err != nil || wal.Balance != 150_000 {
		t.Fatalf("top-up: %+v %v", wal, err)
	}
	if n := len(coreOutbox(t, f, mark, events.DepositUnmatched)); n != 1 {
		t.Fatalf("staff alerts: %d", n)
	}
}
