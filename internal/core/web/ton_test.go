package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonaddr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Fixtures, not secrets: made-up accounts (the store's, as an owner pastes it from a wallet and as it is kept and
// shown, and another), a transaction hash and a low-entropy TON Center key.
var (
	tonStoreHash = [32]byte{0x11, 0x22, 0x33, 0x44}
	tonOtherHash = [32]byte{0x55, 0x66, 0x77, 0x88}
)

const (
	tonAPIKey = "ton-test-ton-test-ton-test" //nolint:gosec // test fixture
	tonTXID   = "5a1c0de05a1c0de05a1c0de05a1c0de05a1c0de05a1c0de05a1c0de05a1c0de0"
	tonMemo   = "BOB-1A2B3C4D"
)

func tonPasted() string { return tonaddr.Address{Hash: tonStoreHash}.Friendly(true) }
func tonShown() string  { return tonaddr.Address{Hash: tonStoreHash}.Canonical() }
func tonOther() string  { return tonaddr.Address{Hash: tonOtherHash}.Canonical() }

func tonFixture(t *testing.T) (*fixture, *browser, *fakeGateways) {
	t.Helper()
	f, b, fg := gatewaysFixture(t)
	fg.ton = &domain.GatewayConfig{Provider: domain.TONAuto}
	return f, b, fg
}

// The automatic TON card is the owner's, like the rest of the page.
func TestTONCardPermissions(t *testing.T) {
	f, _, _ := tonFixture(t)
	f.staffUser(8831, "admin")
	f.staffUser(8832, "support")
	for _, tg := range []int64{8831, 8832} {
		b := f.browser()
		b.loginWithLink(tg)
		for _, c := range []struct{ method, path string }{
			{http.MethodPut, "/api/v1/gateways/ton_auto"},
			{http.MethodPost, "/api/v1/gateways/ton_auto/test"},
			{http.MethodGet, "/api/v1/gateways/report?provider=ton_auto"},
			{http.MethodGet, "/api/v1/gateways/deposits?network=TON"},
			{http.MethodPost, "/api/v1/gateways/deposits/resolve"},
		} {
			if code, _ := b.do(c.method, c.path, map[string]any{"network": "TON"}); code != http.StatusForbidden {
				t.Errorf("%d %s %s: %d", tg, c.method, c.path, code)
			}
		}
	}
}

// Settings: the address comes back in the form customers are shown (whatever form was pasted), the key never;
// the price block says what payments read; the audit keeps the address and "changed" for the key; the test
// says when the address last had a transaction.
func TestTONCardSettings(t *testing.T) {
	f, b, fg := tonFixture(t)
	f.api.cfg.Env = "prod"
	now := f.api.now().Unix()

	code, out := b.do(http.MethodGet, "/api/v1/gateways", nil)
	providers, _ := out["providers"].([]any)
	if code != 200 || len(providers) != 2 {
		t.Fatalf("get: %d %v", code, out)
	}
	ton := providers[1].(map[string]any)
	if ton["provider"] != "ton_auto" || ton["address"] != "" || ton["key_set"] != false || ton["sandbox_allowed"] != nil ||
		ton["price"] != nil {
		t.Fatalf("ton before: %v", ton)
	}
	for micro, text := range map[int64]string{3_214_500: "3.2145", 3_000_000: "3", 1: "0.000001", 12_345_678: "12.345678"} {
		fg.ton.PriceMicro, fg.ton.PriceSource, fg.ton.PriceAt, fg.ton.PriceFresh = micro, "binance", now-20, true
		_, out = b.do(http.MethodGet, "/api/v1/gateways", nil)
		p, _ := out["providers"].([]any)[1].(map[string]any)["price"].(map[string]any)
		if p["usdt_per_ton"] != text || p["source"] != "binance" || p["at"] != float64(now-20) || p["fresh"] != true {
			t.Errorf("price %d: %v", micro, p)
		}
	}
	fg.ton.PriceMicro = 12_345_678

	// What the dashboard sends: the bounceable form a wallet shows, with paste junk, and a key.
	code, out = b.do(http.MethodPut, "/api/v1/gateways/ton_auto", map[string]any{"enabled": true,
		"address": " \u200f" + tonPasted() + " ", "api_key": tonAPIKey, "clear_api_key": false})
	if code != 200 || fg.ton.Address != tonShown() || fg.tonKey != tonAPIKey || fg.actor != owner(t, f).ID {
		t.Fatalf("save: %d %v (payments got %q)", code, out, fg.ton.Address)
	}
	if s := asJSON(out); strings.Contains(s, tonAPIKey) || !strings.Contains(s, `"address":"`+tonShown()+`"`) ||
		!strings.Contains(s, `"key_set":true`) || !strings.Contains(s, `"usdt_per_ton":"12.345678"`) {
		t.Fatalf("answer: %s", s)
	}
	e := lastAudit(t, b, "action=gateway.update")
	if e["entity_id"] != "ton_auto" || asJSON(e["before"]) != `{"address":"","enabled":false}` ||
		asJSON(e["after"]) != `{"address":"`+tonShown()+`","api_key":"changed","enabled":true}` || strings.Contains(asJSON(e), tonAPIKey) {
		t.Fatalf("audit: %v", e)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/gateways/ton_auto", map[string]any{"enabled": true, "address": tonShown()}); code != 200 {
		t.Fatalf("same again: %d", code)
	}
	if n := auditRows(t, f, "gateway.update"); n != 1 {
		t.Fatalf("a save that changes nothing was audited: %d rows", n)
	}

	// Field errors, never repeating a key.
	bad := tonShown()
	bad = bad[:20] + map[bool]string{true: "B", false: "A"}[bad[20] == 'A'] + bad[21:]
	for field, body := range map[string]map[string]any{
		"address":     {"enabled": true, "address": bad},
		"api_key":     {"enabled": true, "api_key": "s3cr3t key"},
		"merchant_id": {"enabled": true, "merchant_id": zpMerchant},
		"sandbox":     {"enabled": true, "sandbox": true},
	} {
		if code, out := b.do(http.MethodPut, "/api/v1/gateways/ton_auto", body); code != 400 || out["field"] != field ||
			strings.Contains(asJSON(out), "s3cr3t") {
			t.Errorf("%s: %d %v", field, code, out)
		}
	}
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/ton_auto", map[string]any{"enabled": true, "address": usdtStore}); code != 400 ||
		out["field"] != "address" {
		t.Fatalf("a TRON address for TON: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true, "address": tonShown()}); code != 400 ||
		out["field"] != "address" {
		t.Fatalf("an address for zarinpal: %d %v", code, out)
	}
	fg.failNext = status.Error(codes.FailedPrecondition, "1 open ton_auto payments: the address cannot change until they close")
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/ton_auto", map[string]any{"enabled": true, "address": tonOther()}); code != 409 ||
		out["open"] != float64(1) {
		t.Fatalf("open payments: %d %v", code, out)
	}

	// Tests: {} is the stored settings, with when the address last had a transaction (null: never).
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/ton_auto/test", map[string]any{}); code != 200 || out["ok"] != true ||
		out["latest_tx_at"] != nil {
		t.Fatalf("test stored: %d %v", code, out)
	}
	fg.tonLatest = now - 3600
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/ton_auto/test", map[string]any{"address": tonOther(),
		"api_key": "BadKeyBadKey1234"}); code != 200 || out["ok"] != false || out["detail"] != "toncenter: the API key was refused (HTTP 401)" ||
		strings.Contains(asJSON(out), "BadKeyBadKey") {
		t.Fatalf("test candidate: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/ton_auto/test", map[string]any{}); code != 200 || out["latest_tx_at"] != float64(now-3600) {
		t.Fatalf("latest transaction: %d %v", code, out)
	}
	if n := len(fg.tested); n != 3 || fg.tested[1].Address != tonOther() || fg.testedFor[1] != domain.TONAuto {
		t.Fatalf("tested: %+v %v", fg.tested, fg.testedFor)
	}
	// Zarinpal's test has no such field.
	if _, out := b.do(http.MethodPost, "/api/v1/gateways/zarinpal/test", map[string]any{}); out["latest_tx_at"] != nil {
		t.Fatalf("zarinpal test: %v", out)
	}
}

// The report lists the TON deposits to credit by hand, with their comment; the TON log is apart from the USDT
// one; resolving takes network TON and the hash alone, once, audited.
func TestTONCardReportAndDeposits(t *testing.T) {
	f, b, fg := tonFixture(t)
	now := f.api.now().Unix()
	fg.transfers = []domain.ChainTransfer{
		{TxID: tonTXID, Network: "TON", From: tonOther(), To: tonShown(), Amount: 700_000_000, Currency: "TON", BlockTime: now - 7200,
			Outcome: "unmatched", Reason: "underpaid", Comment: tonMemo, LT: 40_000_001_000_003, RecordedAt: now - 7100},
		{TxID: usdtTXID, EventIndex: 1, Network: "TRC20", From: usdtSender, To: usdtStore, Amount: 12_340_000, Currency: "USDT",
			BlockTime: now - 7200, Outcome: "unmatched", Reason: "no_payment", RecordedAt: now - 7100},
	}
	fg.tonReport = domain.GatewayReport{Unmatched: fg.transfers[:1], UnmatchedCount: 1, LastScanAt: now - 10, LastScanOKAt: now - 400,
		ScanError: "toncenter: too many requests (HTTP 429)"}

	code, out := b.do(http.MethodGet, "/api/v1/gateways/report?provider=ton_auto", nil)
	unmatched, _ := out["unmatched"].([]any)
	if code != 200 || len(unmatched) != 1 || out["unmatched_count"] != float64(1) || out["healthy"] != false ||
		out["scan_error"] != "toncenter: too many requests (HTTP 429)" {
		t.Fatalf("report: %d %v", code, out)
	}
	d := unmatched[0].(map[string]any)
	if d["txid"] != tonTXID || d["network"] != "TON" || d["comment"] != tonMemo || d["lt"] != float64(40_000_001_000_003) ||
		asJSON(d["amount"]) != `{"amount":700000000,"currency":"TON"}` || d["reason_code"] != "underpaid" ||
		d["reason"] != "the comment names a payment, and less than it asked for arrived" {
		t.Fatalf("deposit: %v", d)
	}

	code, out = b.do(http.MethodGet, "/api/v1/gateways/deposits?network=ton&outcome=unmatched", nil)
	if list := items(out); code != 200 || len(list) != 1 || list[0]["txid"] != tonTXID || fg.filters[len(fg.filters)-1].Network != "TON" {
		t.Fatalf("TON deposits: %d %v", code, out)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/gateways/deposits", nil); code != 200 || len(items(out)) != 1 || items(out)[0]["txid"] != usdtTXID {
		t.Fatalf("USDT deposits: %d %v", code, out)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/gateways/deposits?network=ERC20", nil); code != 400 || out["field"] != "network" {
		t.Fatalf("a network nobody watches: %d %v", code, out)
	}

	code, out = b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"network": "TON", "txid": strings.ToUpper(tonTXID),
		"note": "refunded to the sender"})
	dep, _ := out["deposit"].(map[string]any)
	if code != 200 || dep["outcome"] != "resolved" || len(fg.tonDone) != 1 || fg.tonDone[0] != tonTXID+"|"+owner(t, f).ID+"|refunded to the sender" ||
		len(fg.resolved) != 0 {
		t.Fatalf("resolve: %d %v %v", code, out, fg.tonDone)
	}
	e := lastAudit(t, b, "action=gateway.deposit_resolve")
	if e["entity_id"] != tonTXID || e["reason"] != "refunded to the sender" || !strings.Contains(asJSON(e["after"]), `"currency":"TON"`) {
		t.Fatalf("audit: %v", e)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"network": "TON", "txid": tonTXID}); code != 409 ||
		out["message"] != "this transfer is resolved, not unmatched" {
		t.Fatalf("again: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"network": "BTC", "txid": tonTXID}); code != 400 ||
		out["field"] != "network" {
		t.Fatalf("bad network: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"network": "TON", "txid": "abc"}); code != 400 ||
		out["field"] != "txid" {
		t.Fatalf("bad hash: %d %v", code, out)
	}
}
