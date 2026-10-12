package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Real TRON mainnet addresses (base58check) and a TronGrid-shaped key.
const (
	usdtStore  = "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r"
	usdtOther  = "TSbS3MrjbYDWGmAjvqzwTVCpiqsu7uR9Fb"
	usdtSender = "TK6wsfvEmv3S45xFxQXE2DqEtu8SVAXTut"
	usdtKey    = "25f66928-0b70-48cd-9ac6-da6f8247c663" //nolint:gosec // test fixture
	usdtTXID   = "8f0c1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7"
)

func usdtFixture(t *testing.T) (*fixture, *browser, *fakeGateways) {
	t.Helper()
	f, b, fg := gatewaysFixture(t)
	fg.usdt = &domain.GatewayConfig{Provider: domain.USDTAuto}
	return f, b, fg
}

// The automatic USDT card is the owner's, like the rest of the page.
func TestUSDTCardPermissions(t *testing.T) {
	f, _, _ := usdtFixture(t)
	f.staffUser(8821, "admin")
	f.staffUser(8822, "support")
	for _, tg := range []int64{8821, 8822} {
		b := f.browser()
		b.loginWithLink(tg)
		for _, c := range []struct{ method, path string }{
			{http.MethodPut, "/api/v1/gateways/usdt_trc20_auto"},
			{http.MethodPost, "/api/v1/gateways/usdt_trc20_auto/test"},
			{http.MethodGet, "/api/v1/gateways/report?provider=usdt_trc20_auto"},
			{http.MethodGet, "/api/v1/gateways/deposits"},
			{http.MethodPost, "/api/v1/gateways/deposits/resolve"},
		} {
			if code, _ := b.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
				t.Errorf("%d %s %s: %d", tg, c.method, c.path, code)
			}
		}
	}
}

// Settings: the address comes back (customers see it), the key never; the
// audit keeps the address and "changed" for the key; payments' refusals keep
// their field and the open count.
func TestUSDTCardSettings(t *testing.T) {
	f, b, fg := usdtFixture(t)
	f.api.cfg.Env = "prod"
	now := f.api.now().Unix()
	fg.usdtReport = domain.GatewayReport{OpenCount: 1, OldestOpenAt: now - 600, LastScanAt: now - 20, LastScanOKAt: now - 20}

	code, out := b.do(http.MethodGet, "/api/v1/gateways", nil)
	providers, _ := out["providers"].([]any)
	if code != 200 || len(providers) != 2 {
		t.Fatalf("get: %d %v", code, out)
	}
	zp, usdt := providers[0].(map[string]any), providers[1].(map[string]any)
	if _, has := zp["address"]; has || zp["key_set"] != nil {
		t.Fatalf("zarinpal has USDT fields: %v", zp)
	}
	health, _ := usdt["health"].(map[string]any)
	if usdt["provider"] != "usdt_trc20_auto" || usdt["address"] != "" || usdt["key_set"] != false || usdt["sandbox_allowed"] != nil ||
		health["open"] != float64(1) || health["healthy"] != true || health["last_scan_ok_at"] != float64(now-20) {
		t.Fatalf("usdt: %v", usdt)
	}

	// What the dashboard sends: the address typed (spaces and marks dropped)
	// and a new key.
	code, out = b.do(http.MethodPut, "/api/v1/gateways/usdt_trc20_auto", map[string]any{"enabled": true,
		"address": " \u200f" + usdtStore + " ", "api_key": usdtKey, "clear_api_key": false})
	if code != 200 || fg.usdt.Address != usdtStore || fg.apiKey != usdtKey || fg.actor != owner(t, f).ID {
		t.Fatalf("save: %d %v (payments got %q %q)", code, out, fg.usdt.Address, fg.apiKey)
	}
	if s := asJSON(out); strings.Contains(s, usdtKey) || !strings.Contains(s, `"address":"`+usdtStore+`"`) || !strings.Contains(s, `"key_set":true`) {
		t.Fatalf("answer: %s", s)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/gateways", nil); code != 200 || strings.Contains(asJSON(out), usdtKey) {
		t.Fatalf("get after save: %d %v", code, out)
	}
	e := lastAudit(t, b, "action=gateway.update")
	if e["entity_id"] != "usdt_trc20_auto" || asJSON(e["before"]) != `{"address":"","enabled":false}` ||
		asJSON(e["after"]) != `{"address":"`+usdtStore+`","api_key":"changed","enabled":true}` {
		t.Fatalf("audit: %v", e)
	}
	// Nothing new: no audit entry.
	if code, _ := b.do(http.MethodPut, "/api/v1/gateways/usdt_trc20_auto", map[string]any{"enabled": true, "address": usdtStore}); code != 200 {
		t.Fatalf("same again: %d", code)
	}
	if e := lastAudit(t, b, "action=gateway.update"); asJSON(e["after"]) != `{"address":"`+usdtStore+`","api_key":"changed","enabled":true}` {
		t.Fatalf("a save that changes nothing was audited: %v", e)
	}

	// Field errors, never repeating a key.
	for field, body := range map[string]map[string]any{
		"address":     {"enabled": true, "address": usdtStore[:10] + "z" + usdtStore[11:]},
		"api_key":     {"enabled": true, "address": usdtStore, "api_key": "s3cr3t key"},
		"merchant_id": {"enabled": true, "merchant_id": zpMerchant},
	} {
		if code, out := b.do(http.MethodPut, "/api/v1/gateways/usdt_trc20_auto", body); code != 400 || out["field"] != field ||
			strings.Contains(asJSON(out), "s3cr3t") {
			t.Errorf("%s: %d %v", field, code, out)
		}
	}
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true, "address": usdtStore}); code != 400 || out["field"] != "address" {
		t.Fatalf("an address for zarinpal: %d %v", code, out)
	}
	// Payments' refusals: open payments to the old address, and its checks.
	fg.failNext = status.Error(codes.FailedPrecondition, "2 open usdt_trc20_auto payments: the address cannot change until they close")
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/usdt_trc20_auto", map[string]any{"enabled": true, "address": usdtOther}); code != 409 ||
		out["open"] != float64(2) || out["error"] != "state" {
		t.Fatalf("open payments: %d %v", code, out)
	}
	for msg, field := range map[string]string{
		"payments: invalid argument: set the TRC20 address before switching automatic USDT on":    "address",
		"payments: invalid argument: the API key must be the key from the TronGrid dashboard":     "api_key",
		"payments: invalid argument: that is the USDT contract's address, not a wallet's":         "address",
		"payments: invalid argument: usdt_trc20_auto has no sandbox and no merchant id":           "merchant_id",
		"payments: invalid argument: set a proxy or clear it, not both":                           "proxy",
		"payments: invalid argument: enter the address again: the stored one cannot be decrypted": "address",
	} {
		fg.failNext = status.Error(codes.InvalidArgument, msg)
		if code, out := b.do(http.MethodPut, "/api/v1/gateways/usdt_trc20_auto", map[string]any{"enabled": true}); code != 400 ||
			out["field"] != field || out["message"] != strings.TrimPrefix(msg, "payments: invalid argument: ") {
			t.Errorf("%s: %d %v", msg, code, out)
		}
	}

	// Tests: {} is the stored settings; the card sends what is typed.
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/usdt_trc20_auto/test", map[string]any{}); code != 200 || out["ok"] != true {
		t.Fatalf("test stored: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/usdt_trc20_auto/test", map[string]any{"address": usdtOther,
		"api_key": "00000000-dead-beef-0000-000000000000", "clear_api_key": false}); code != 200 || out["ok"] != false ||
		out["detail"] != "trongrid: the API key was refused (HTTP 401)" {
		t.Fatalf("test candidate: %d %v", code, out)
	}
	if n := len(fg.tested); n != 2 || fg.tested[0] != nil || fg.tested[1].Address != usdtOther || fg.testedFor[1] != domain.USDTAuto {
		t.Fatalf("tested: %+v %v", fg.tested, fg.testedFor)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/usdt_trc20_auto/test", map[string]any{"address": "TXYZ"}); code != 400 ||
		out["field"] != "address" || len(fg.tested) != 2 {
		t.Fatalf("test a bad address: %d %v", code, out)
	}
}

// The report: totals as for any method, the unmatched deposits (with why,
// in English and as a code), their count and the watcher's reads; a read
// failing for over 5 minutes is unhealthy and says why.
func TestUSDTCardReportAndDeposits(t *testing.T) {
	f, b, fg := usdtFixture(t)
	now := f.api.now().Unix()
	fg.transfers = []domain.ChainTransfer{
		{TxID: usdtTXID, EventIndex: 1, Network: "TRC20", From: usdtSender, To: usdtStore, Amount: 12_340_000, Currency: "USDT",
			BlockTime: now - 7200, Outcome: "unmatched", Reason: "no_payment", RecordedAt: now - 7100},
		{TxID: strings.Repeat("ab", 32), EventIndex: 0, Network: "TRC20", From: usdtSender, To: usdtStore, Amount: 2_500_007, Currency: "USDT",
			BlockTime: now - 600, Outcome: "matched", IntentID: "0192d9e4-7b1a-7c3e-9f00-0000000000a1", RecordedAt: now - 590},
	}
	fg.usdtReport = domain.GatewayReport{OpenCount: 0, Unmatched: fg.transfers[:1], UnmatchedCount: 1,
		LastScanAt: now - 10, LastScanOKAt: now - 400, ScanError: "trongrid: too many requests (HTTP 429)",
		LastPaid: &domain.GatewayEvent{At: now - 600, Kind: "match"},
		Totals:   []domain.GatewayStatusTotal{{Status: "succeeded", Count: 1, Amounts: map[string]int64{"IRT": 150_000}}}}

	code, out := b.do(http.MethodGet, "/api/v1/gateways/report?provider=usdt_trc20_auto&from=1&to="+itoa(now), nil)
	unmatched, _ := out["unmatched"].([]any)
	if code != 200 || len(unmatched) != 1 || out["unmatched_count"] != float64(1) || out["healthy"] != false ||
		out["last_error"] != "trongrid: too many requests (HTTP 429)" || out["last_error_at"] != float64(now-10) ||
		out["scan_error"] != "trongrid: too many requests (HTTP 429)" || out["last_scan_ok_at"] != float64(now-400) {
		t.Fatalf("report: %d %v", code, out)
	}
	d := unmatched[0].(map[string]any)
	if d["txid"] != usdtTXID || d["event_index"] != float64(1) || d["from"] != usdtSender || d["at"] != float64(now-7200) ||
		asJSON(d["amount"]) != `{"amount":12340000,"currency":"USDT"}` || d["reason_code"] != "no_payment" ||
		d["reason"] != "no open payment has exactly this amount" || d["outcome"] != "unmatched" {
		t.Fatalf("deposit: %v", d)
	}
	// Zarinpal's report has no such list.
	if _, out := b.do(http.MethodGet, "/api/v1/gateways/report?provider=zarinpal", nil); out["unmatched"] != nil || out["scan_error"] != nil {
		t.Fatalf("zarinpal report: %v", out)
	}
	// The same health on the page and the overview.
	_, out = b.do(http.MethodGet, "/api/v1/gateways", nil)
	if h := out["providers"].([]any)[1].(map[string]any)["health"].(map[string]any); h["healthy"] != false ||
		h["last_error"] != "trongrid: too many requests (HTTP 429)" || h["unmatched_count"] != float64(1) {
		t.Fatalf("page health: %v", h)
	}
	fg.usdt.Enabled, fg.usdt.Offered = true, true
	_, out = b.do(http.MethodGet, "/api/v1/overview", nil)
	got := map[string]string{}
	for _, g := range out["gateways"].([]any) {
		m := g.(map[string]any)
		got[m["provider"].(string)] = fmt.Sprintf("%v/%v", m["enabled"], m["healthy"])
	}
	if got["usdt_trc20_auto"] != "true/false" {
		t.Fatalf("overview: %v", got)
	}
	fg.usdtReport.LastScanOKAt = now - 10
	_, out = b.do(http.MethodGet, "/api/v1/overview", nil)
	for _, g := range out["gateways"].([]any) {
		if m := g.(map[string]any); m["provider"] == "usdt_trc20_auto" && m["healthy"] != true {
			t.Fatalf("overview after a good read: %v", m)
		}
	}

	// The log, filtered and checked.
	code, out = b.do(http.MethodGet, "/api/v1/gateways/deposits?outcome=unmatched", nil)
	if list := items(out); code != 200 || len(list) != 1 || list[0]["txid"] != usdtTXID || out["total"] != float64(1) ||
		fg.filters[len(fg.filters)-1].Outcome != "unmatched" {
		t.Fatalf("deposits: %d %v", code, out)
	}
	code, out = b.do(http.MethodGet, "/api/v1/gateways/deposits?txid=0x"+strings.ToUpper(strings.Repeat("ab", 32)), nil)
	if list := items(out); code != 200 || len(list) != 1 || list[0]["intent_id"] != "0192d9e4-7b1a-7c3e-9f00-0000000000a1" || list[0]["reason"] != nil {
		t.Fatalf("by txid: %d %v", code, out)
	}
	for q, field := range map[string]string{"?outcome=lost": "outcome", "?txid=nope": "txid", "?intent_id=x": "intent_id"} {
		if code, out := b.do(http.MethodGet, "/api/v1/gateways/deposits"+q, nil); code != 400 || out["field"] != field {
			t.Errorf("%s: %d %v", q, code, out)
		}
	}

	// Resolving: once, audited with the note, then refused.
	code, out = b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"txid": strings.ToUpper(usdtTXID), "event_index": 1,
		"note": "credited 12.34 USDT to @payer"})
	dep, _ := out["deposit"].(map[string]any)
	who, _ := dep["resolved_by"].(map[string]any)
	if code != 200 || dep["outcome"] != "resolved" || who["telegram_id"] != float64(ownerTG) || dep["note"] != "credited 12.34 USDT to @payer" ||
		len(fg.resolved) != 1 || fg.resolved[0] != usdtTXID+"#1|"+owner(t, f).ID+"|credited 12.34 USDT to @payer" {
		t.Fatalf("resolve: %d %v %v", code, out, fg.resolved)
	}
	e := lastAudit(t, b, "action=gateway.deposit_resolve")
	if e["entity"] != "deposit" || e["entity_id"] != usdtTXID+"#1" || e["reason"] != "credited 12.34 USDT to @payer" ||
		!strings.Contains(asJSON(e["after"]), `"amount":12340000`) {
		t.Fatalf("audit: %v", e)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"txid": usdtTXID, "event_index": 1}); code != 409 ||
		out["message"] != "this transfer is resolved, not unmatched" {
		t.Fatalf("again: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"txid": usdtTXID, "event_index": 7}); code != 404 {
		t.Fatalf("unknown: %d", code)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"txid": "abc", "event_index": 0}); code != 400 || out["field"] != "txid" {
		t.Fatalf("bad txid: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/gateways/deposits/resolve", map[string]any{"txid": usdtTXID, "credit": 5}); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}
	fg.down = true
	if code, _ := b.do(http.MethodGet, "/api/v1/gateways/deposits", nil); code != 503 {
		t.Fatalf("payments down: %d", code)
	}
	if n := auditRows(t, f, "gateway.deposit_resolve"); n != 1 {
		t.Fatalf("audit rows: %d", n)
	}
}

func auditRows(t *testing.T, f *fixture, action string) int {
	t.Helper()
	var n int
	if err := f.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
