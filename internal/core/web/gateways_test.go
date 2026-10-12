package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeGateways is payments' gateway settings: it keeps the secrets it is
// given and answers hints only. failNext makes the next call fail.
type fakeGateways struct {
	mu       sync.Mutex
	cfg      domain.GatewayConfig
	merchant string
	proxy    string
	actor    string
	tested   []*domain.GatewayCandidate
	report   domain.GatewayReport
	reports  []string // provider|from|to
	failNext error
	down     bool
	// automatic USDT, listed after Zarinpal when set: its settings, its
	// report, its transfer log and what was resolved.
	usdt       *domain.GatewayConfig
	apiKey     string
	usdtReport domain.GatewayReport
	transfers  []domain.ChainTransfer
	filters    []domain.ChainTransferFilter
	resolved   []string // txid#index|actor|note
	testedFor  []string
}

func (f *fakeGateways) fail() error {
	if f.down {
		return status.Error(codes.Unavailable, "payments down")
	}
	err := f.failNext
	f.failNext = nil
	return err
}

func (f *fakeGateways) GatewayConfigs(context.Context) ([]domain.GatewayConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, fmt.Errorf("payments.ListGatewayConfigs: %w", status.Error(codes.Unavailable, "down"))
	}
	if f.usdt != nil {
		return []domain.GatewayConfig{f.cfg, *f.usdt}, nil
	}
	return []domain.GatewayConfig{f.cfg}, nil
}

func (f *fakeGateways) SetGatewayConfig(_ context.Context, u domain.GatewayUpdate, actorID string) (*domain.GatewayConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, fmt.Errorf("payments.SetGatewayConfig: %w", err)
	}
	f.actor = actorID
	if u.Provider == domain.USDTAuto && f.usdt != nil {
		if u.Address != "" {
			f.usdt.Address = u.Address
		}
		switch {
		case u.ClearAPIKey:
			f.apiKey = ""
		case u.APIKey != "":
			f.apiKey = u.APIKey
		}
		f.usdt.Enabled, f.usdt.APIKeySet, f.usdt.Configured = u.Enabled, f.apiKey != "", f.usdt.Address != ""
		f.usdt.Offered = u.Enabled && f.usdt.Configured
		out := *f.usdt
		return &out, nil
	}
	if u.MerchantID != "" {
		f.merchant = u.MerchantID
		f.cfg.MerchantHint = u.MerchantID[len(u.MerchantID)-4:]
	}
	switch {
	case u.ClearProxy:
		f.proxy = ""
	case u.Proxy != "":
		f.proxy = u.Proxy
	}
	f.cfg.Enabled, f.cfg.Sandbox, f.cfg.ProxySet = u.Enabled, u.Sandbox, f.proxy != ""
	f.cfg.Configured = f.merchant != ""
	f.cfg.Offered = u.Enabled && f.cfg.Configured
	out := f.cfg
	return &out, nil
}

func (f *fakeGateways) TestGateway(_ context.Context, provider string, c *domain.GatewayCandidate) (*domain.GatewayTest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, fmt.Errorf("payments.TestGateway: %w", err)
	}
	f.tested, f.testedFor = append(f.tested, c), append(f.testedFor, provider)
	if provider == domain.USDTAuto {
		if c != nil && c.APIKey == "00000000-dead-beef-0000-000000000000" {
			return &domain.GatewayTest{Detail: "trongrid: the API key was refused (HTTP 401)"}, nil
		}
		return &domain.GatewayTest{OK: true, LatencyMS: 80}, nil
	}
	if c != nil && c.MerchantID == "9f8e7d6c-5b4a-4321-8fed-cba987654321" {
		return &domain.GatewayTest{Detail: "Zarinpal refused the merchant id (-10)"}, nil
	}
	return &domain.GatewayTest{OK: true, Unverified: 1, LatencyMS: 40}, nil
}

func (f *fakeGateways) GatewayReport(_ context.Context, provider string, from, to int64) (*domain.GatewayReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, fmt.Errorf("payments.GatewayReport: %w", err)
	}
	f.reports = append(f.reports, fmt.Sprintf("%s|%d|%d", provider, from, to))
	r := f.report
	if provider == domain.USDTAuto {
		r = f.usdtReport
	}
	r.Provider = provider
	return &r, nil
}

func (f *fakeGateways) ChainTransfers(_ context.Context, flt domain.ChainTransferFilter, _, _ int) ([]domain.ChainTransfer, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, 0, fmt.Errorf("payments.ListChainTransfers: %w", err)
	}
	f.filters = append(f.filters, flt)
	var out []domain.ChainTransfer
	for _, t := range f.transfers {
		if (flt.Outcome == "" || t.Outcome == flt.Outcome) && (flt.TxID == "" || t.TxID == flt.TxID) &&
			(flt.IntentID == "" || t.IntentID == flt.IntentID) {
			out = append(out, t)
		}
	}
	return out, int64(len(out)), nil
}

func (f *fakeGateways) ResolveChainTransfer(_ context.Context, txid string, index int, actorID, note string) (*domain.ChainTransfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail(); err != nil {
		return nil, fmt.Errorf("payments.ResolveChainTransfer: %w", err)
	}
	for i := range f.transfers {
		t := &f.transfers[i]
		if t.TxID != txid || t.EventIndex != index {
			continue
		}
		if t.Outcome != "unmatched" {
			return nil, fmt.Errorf("payments.ResolveChainTransfer: %w",
				status.Error(codes.FailedPrecondition, "this transfer is "+t.Outcome+", not unmatched"))
		}
		t.Outcome, t.ResolvedBy, t.ResolvedAt, t.Note = "resolved", actorID, 1_800_000_500, note
		f.resolved = append(f.resolved, fmt.Sprintf("%s#%d|%s|%s", txid, index, actorID, note))
		out := *t
		return &out, nil
	}
	return nil, fmt.Errorf("payments.ResolveChainTransfer: %w", status.Error(codes.NotFound, "no such transfer"))
}

const (
	zpMerchant = "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b"
	zpProxy    = "http://relay:pr0xy-pw@203.0.113.10:3128" //nolint:gosec // test fixture
)

func gatewaysFixture(t *testing.T) (*fixture, *browser, *fakeGateways) {
	t.Helper()
	f, b, _, _ := salesFixture(t)
	if _, err := f.st.DB().Exec(context.Background(), `TRUNCATE core.settings`); err != nil {
		t.Fatal(err)
	}
	fg := &fakeGateways{cfg: domain.GatewayConfig{Provider: domain.Zarinpal}}
	f.api.cfg.Gateways = fg
	return f, b, fg
}

// The Payment gateways page is the owner's: admins and support get 403.
func TestGatewaysPermissions(t *testing.T) {
	f, _, _ := gatewaysFixture(t)
	f.staffUser(8801, "admin")
	f.staffUser(8802, "support")
	for _, tg := range []int64{8801, 8802} {
		b := f.browser()
		b.loginWithLink(tg)
		for _, c := range []struct{ method, path string }{
			{http.MethodGet, "/api/v1/gateways"},
			{http.MethodGet, "/api/v1/gateways/report"},
			{http.MethodPut, "/api/v1/gateways/zarinpal"},
			{http.MethodPost, "/api/v1/gateways/zarinpal/test"},
		} {
			if code, _ := b.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
				t.Errorf("%d %s %s: %d", tg, c.method, c.path, code)
			}
		}
	}
	// The manual methods' switches are payment settings: the owner's too.
	adm := f.browser()
	adm.loginWithLink(8801)
	if code, out := adm.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"payments.manual_card.enabled": "false"}}); code != 403 ||
		out["field"] != "payments.manual_card.enabled" {
		t.Fatalf("admin switches a payment method: %d %v", code, out)
	}
	f.api.cfg.Gateways = nil
	b := f.browser()
	b.loginWithLink(ownerTG)
	if code, _ := b.do(http.MethodGet, "/api/v1/gateways", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("no payments service: %d", code)
	}
}

// The page, its saves and tests: secrets go in and never come back, the
// audit names what changed, and payments' refusals keep their reason.
func TestGatewaysAPI(t *testing.T) {
	f, b, fg := gatewaysFixture(t)
	f.api.cfg.Env = "prod"
	fg.report = domain.GatewayReport{OpenCount: 2, OldestOpenAt: 1_700_000_000,
		LastError: &domain.GatewayEvent{At: 1_700_000_100, Kind: "create", Detail: "the proxy refused the connection"},
		Totals:    []domain.GatewayStatusTotal{{Status: "succeeded", Count: 3, Amounts: map[string]int64{"IRT": 450_000}}},
		CheckErrors: []domain.GatewayEvent{{At: 1_700_000_090, Kind: "check", IntentID: "00000000-0000-7000-8000-00000000c0e1",
			Detail: "zarinpal: request failed: timed out"}},
		CheckErrorCount: 4}

	code, out := b.do(http.MethodGet, "/api/v1/gateways", nil)
	providers, _ := out["providers"].([]any)
	if code != 200 || len(providers) != 1 {
		t.Fatalf("get: %d %v", code, out)
	}
	zp := providers[0].(map[string]any)
	health, _ := zp["health"].(map[string]any)
	if zp["provider"] != "zarinpal" || zp["sandbox_allowed"] != false || health["open"] != float64(2) || health["healthy"] != true ||
		health["last_error"] != "the proxy refused the connection" || health["last_paid_at"] != nil {
		t.Fatalf("zarinpal: %v", zp)
	}
	settings := items(map[string]any{"items": out["settings"]})
	var keys []string
	for _, it := range settings {
		keys = append(keys, it["key"].(string))
	}
	if !strings.Contains(strings.Join(keys, ","), "payments.manual_card.enabled,payments.card_number") ||
		!strings.Contains(strings.Join(keys, ","), "payments.stars.enabled,payments.stars_rate") {
		t.Fatalf("payment settings: %v", keys)
	}
	if sw := settings[0]; sw["kind"] != "bool" || sw["default"] != "true" || sw["editable"] != true {
		t.Fatalf("switch: %v", sw)
	}

	// Save: merchant and proxy go to payments, the answer has hints only.
	code, out = b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true, "sandbox": false,
		"merchant_id": strings.ToUpper(zpMerchant), "proxy": zpProxy, "clear_proxy": false})
	if code != 200 || fg.merchant != zpMerchant || fg.proxy != zpProxy || fg.actor != owner(t, f).ID {
		t.Fatalf("save: %d %v (payments got %q %q %q)", code, out, fg.merchant, fg.proxy, fg.actor)
	}
	if s := asJSON(out); strings.Contains(s, zpMerchant) || strings.Contains(s, "pr0xy") || !strings.Contains(s, `"merchant_hint":"1a2b"`) {
		t.Fatalf("answer: %s", s)
	}
	if code, out := b.do(http.MethodGet, "/api/v1/gateways", nil); code != 200 || strings.Contains(asJSON(out), zpMerchant) || strings.Contains(asJSON(out), "pr0xy") {
		t.Fatalf("get after save: %d %v", code, out)
	}
	e := lastAudit(t, b, "action=gateway.update")
	if e["entity"] != "gateway" || e["entity_id"] != "zarinpal" ||
		asJSON(e["after"]) != `{"enabled":true,"merchant_id":"changed","proxy":"changed","sandbox":false}` ||
		asJSON(e["before"]) != `{"enabled":false,"sandbox":false}` {
		t.Fatalf("audit: %v", e)
	}

	// Field errors, never repeating what was typed.
	for field, body := range map[string]map[string]any{
		"merchant_id": {"enabled": true, "merchant_id": "12345-s3cr3t"},
		"proxy":       {"enabled": true, "proxy": "socks5://u:s3cr3t@203.0.113.10:1"},
	} {
		if code, out := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", body); code != 400 || out["field"] != field || strings.Contains(asJSON(out), "s3cr3t") {
			t.Errorf("%s: %d %v", field, code, out)
		}
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true, "secret": "x"}); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/gateways/paypal", map[string]any{"enabled": true}); code != 404 {
		t.Fatalf("unknown gateway: %d", code)
	}

	// Payments' refusals: open payments (with their count), the key, inputs.
	fg.failNext = status.Error(codes.FailedPrecondition, "3 open zarinpal payments: the merchant id and the sandbox cannot change until they close")
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true, "merchant_id": "9f8e7d6c-5b4a-4321-8fed-cba987654321"}); code != 409 ||
		out["open"] != float64(3) || out["error"] != "state" {
		t.Fatalf("open payments: %d %v", code, out)
	}
	fg.failNext = status.Error(codes.FailedPrecondition, "the payments master key is not set: add it (re-run bobres install) to change gateway settings")
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": false}); code != 409 || out["open"] != nil ||
		!strings.Contains(out["message"].(string), "re-run bobres install") {
		t.Fatalf("key missing: %d %v", code, out)
	}
	fg.failNext = status.Error(codes.InvalidArgument, "payments: invalid argument: the Zarinpal sandbox is refused when BOBRES_ENV=prod")
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true, "sandbox": true}); code != 400 ||
		out["field"] != "sandbox" || out["message"] != "the Zarinpal sandbox is refused when BOBRES_ENV=prod" {
		t.Fatalf("sandbox: %d %v", code, out)
	}
	fg.failNext = status.Error(codes.Unavailable, "down")
	if code, _ := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true}); code != 503 {
		t.Fatalf("payments down: %d", code)
	}

	// Tests: {} is the stored settings, a candidate is passed on checked.
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/zarinpal/test", map[string]any{}); code != 200 || out["ok"] != true || out["unverified"] != float64(1) {
		t.Fatalf("test stored: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/zarinpal/test", map[string]any{"sandbox": false,
		"merchant_id": "9f8e7d6c-5b4a-4321-8fed-cba987654321", "proxy": "", "clear_proxy": true}); code != 200 || out["ok"] != false ||
		out["detail"] != "Zarinpal refused the merchant id (-10)" {
		t.Fatalf("test candidate: %d %v", code, out)
	}
	if len(fg.tested) != 2 || fg.tested[0] != nil || fg.tested[1] == nil || !fg.tested[1].ClearProxy {
		t.Fatalf("tested: %+v", fg.tested)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/zarinpal/test", map[string]any{"merchant_id": "nope"}); code != 400 || out["field"] != "merchant_id" {
		t.Fatalf("test bad merchant: %d %v", code, out)
	}

	// The report: the period passed on, totals per status, health.
	code, out = b.do(http.MethodGet, "/api/v1/gateways/report?provider=zarinpal&from=1700000000&to=1700604800", nil)
	if code != 200 || out["open"] != float64(2) || out["healthy"] != true || out["last_error_at"] != float64(1_700_000_100) ||
		asJSON(out["totals"]) != `[{"amounts":[{"amount":450000,"currency":"IRT"}],"count":3,"status":"succeeded"}]` {
		t.Fatalf("report: %d %v", code, out)
	}
	if last := fg.reports[len(fg.reports)-1]; last != "zarinpal|1700000000|1700604800" {
		t.Fatalf("report asked: %s", last)
	}
	// The failed payment checks payments kept in the period.
	if asJSON(out["check_errors"]) != `[{"at":1700000090,"detail":"zarinpal: request failed: timed out","intent_id":"00000000-0000-7000-8000-00000000c0e1"}]` ||
		out["check_error_count"] != float64(4) {
		t.Fatalf("report check errors: %v %v", out["check_errors"], out["check_error_count"])
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/gateways/report", nil); code != 200 || fg.reports[len(fg.reports)-1] != "zarinpal|0|0" {
		t.Fatalf("default report: %d %v", code, fg.reports)
	}
	for _, q := range []string{"?provider=paypal", "?from=-1", "?to=x"} {
		if code, _ := b.do(http.MethodGet, "/api/v1/gateways/report"+q, nil); code != 400 {
			t.Errorf("report %s: %d", q, code)
		}
	}
	if code, _ := b.do(http.MethodGet, "/api/v1/gateways/report?provider=manual_card", nil); code != 200 {
		t.Fatalf("manual report: %d", code)
	}

	// The switches and details are saved through the settings endpoint.
	if code, _ := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{
		"payments.manual_card.enabled": "off", "payments.card_number": "6037991122223333"}}); code != 200 {
		t.Fatalf("save a method: %d", code)
	}
	_, out = b.do(http.MethodGet, "/api/v1/gateways", nil)
	got := map[string]string{}
	for _, it := range items(map[string]any{"items": out["settings"]}) {
		got[it["key"].(string)] = it["value"].(string)
	}
	if got["payments.manual_card.enabled"] != "false" || got["payments.card_number"] != "6037991122223333" {
		t.Fatalf("saved method: %v", got)
	}

	fg.down = true
	if code, out := b.do(http.MethodGet, "/api/v1/gateways", nil); code != 503 || out["error"] != "unavailable" {
		t.Fatalf("payments down: %d %v", code, out)
	}
}

// Tests are limited per staff member, like server tests.
func TestGatewayTestsAreRateLimited(t *testing.T) {
	_, b, _ := gatewaysFixture(t)
	for i := 0; i < 10; i++ {
		if code, _ := b.do(http.MethodPost, "/api/v1/gateways/zarinpal/test", map[string]any{}); code != 200 {
			t.Fatalf("test %d: %d", i, code)
		}
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/gateways/zarinpal/test", map[string]any{}); code != http.StatusTooManyRequests {
		t.Fatalf("11th test: %d", code)
	}
}

// The overview tells, per payment method, whether it is offered and works:
// Zarinpal from payments and its report, the others from their settings.
func TestOverviewGateways(t *testing.T) {
	f, b, fg := gatewaysFixture(t)
	fg.cfg = domain.GatewayConfig{Provider: domain.Zarinpal, Enabled: true, Configured: true, Offered: true}
	fg.report = domain.GatewayReport{LastError: &domain.GatewayEvent{At: f.api.now().Unix() - 60}}
	ctx := context.Background()
	if _, err := f.dom.SetSettings(ctx, owner(t, f), map[string]string{"payments.card_number": "6037991122223333",
		"payments.manual_crypto.enabled": "false"}); err != nil {
		t.Fatal(err)
	}
	_, out := b.do(http.MethodGet, "/api/v1/overview", nil)
	got := map[string]string{}
	for _, g := range out["gateways"].([]any) {
		m := g.(map[string]any)
		got[m["provider"].(string)] = fmt.Sprintf("%v/%v", m["enabled"], m["healthy"])
	}
	want := map[string]string{"zarinpal": "true/false", "manual_card": "true/true", "manual_crypto": "false/false",
		"manual_zarinpal": "true/false", "stars": "true/false"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("gateways: %v, want %v", got, want)
	}
	// Support sees the states (no details).
	f.staffUser(8811, "support")
	sup := f.browser()
	sup.loginWithLink(8811)
	if code, out := sup.do(http.MethodGet, "/api/v1/overview", nil); code != 200 || strings.Contains(asJSON(out), "6037") || out["gateways"] == nil {
		t.Fatalf("support overview: %d %v", code, out["gateways"])
	}
	fg.down = true
	_, out = b.do(http.MethodGet, "/api/v1/overview", nil)
	for _, g := range out["gateways"].([]any) {
		if g.(map[string]any)["provider"] == "zarinpal" {
			t.Fatal("zarinpal listed while payments did not answer")
		}
	}
}
