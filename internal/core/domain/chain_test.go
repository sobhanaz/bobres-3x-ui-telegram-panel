package domain

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// Real TRON mainnet addresses (base58check): a store's, another one, and
// the USDT contract.
const (
	testTronAddress  = "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r"
	otherTronAddress = "TSbS3MrjbYDWGmAjvqzwTVCpiqsu7uR9Fb"
	testTronKey      = "11111111-2222-4333-8444-555555555555" //nolint:gosec // test fixture
	testTXID         = "8f0c1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7"
)

func TestTronAddresses(t *testing.T) {
	for _, a := range []string{testTronAddress, otherTronAddress, "TK6wsfvEmv3S45xFxQXE2DqEtu8SVAXTut", usdtContract} {
		if !validTronAddress(a) {
			t.Errorf("%s refused", a)
		}
	}
	typo := testTronAddress[:10] + "z" + testTronAddress[11:]
	for _, a := range []string{typo, "T" + strings.Repeat("1", 33), testTronAddress[:33], "0x" + testTronAddress, "AS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r",
		"TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3O"} {
		if validTronAddress(a) {
			t.Errorf("%s accepted", a)
		}
	}
	// What a paste adds is dropped; the case is kept (base58 is case sensitive).
	if got := normalizeTronAddress(" \u200f" + testTronAddress + "\u200e\n"); got != testTronAddress {
		t.Fatalf("normalized %q", got)
	}
	if _, _, err := checkChainSecrets(strings.ToLower(testTronAddress), "", false); fieldOf(err) != "address" {
		t.Fatalf("lower case: %v", err)
	}
}

func TestUSDTCharge(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	if _, _, err := svc.gatewayCharge(ctx, USDTAuto, 150_000, "IRT"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a Toman price without a rate: %v", err)
	}
	setSetting(t, s, "payments.usdt_rate", "60,000") // Toman per USDT
	for _, c := range []struct {
		amount   int64
		currency string
		want     int64
	}{
		{150_000, "IRT", 2_500_000},          // 2.50 USDT exactly
		{150_001, "IRT", 2_510_000},          // 2.5000166 → 2.51: rounded up to the cent, never short
		{6_000, "IRT", 100_000},              // 0.10 USDT, the least
		{5_123_456, "USDT", 5_123_456},       // a USDT price as it is
		{100_000, "USDT", 100_000},           // 0.1 USDT
		{1_000_000_000_000, "USDT", usdtMax}, // 1,000,000 USDT
	} {
		got, unit, err := svc.gatewayCharge(ctx, USDTAuto, c.amount, c.currency)
		if err != nil || got != c.want || unit != "USDT" {
			t.Errorf("%d %s: %d %s %v, want %d USDT", c.amount, c.currency, got, unit, err, c.want)
		}
	}
	for _, c := range []struct {
		amount   int64
		currency string
	}{
		{99_999, "USDT"}, {5_400, "IRT"}, {1_000_000_000_001, "USDT"}, {60_000_000_001, "IRT"},
		{math.MaxInt64, "IRT"}, {math.MaxInt64 / 100, "IRT"}, {1000, "EUR"}, {0, "USDT"}, {-5, "IRT"},
	} {
		if got, _, err := svc.gatewayCharge(ctx, USDTAuto, c.amount, c.currency); !errors.Is(err, ErrInvalid) {
			t.Errorf("%d %s accepted: %d", c.amount, c.currency, got)
		}
	}
}

// Automatic USDT is offered when payments offers it and the price can be
// converted; the intent asks payments for the converted amount and returns
// payments' unique amount, the address and the window.
func TestUSDTPaymentMethodsAndIntent(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	pay := &fakePayments{gateways: []string{USDTAuto}}
	if ms, _ := svc.PaymentMethods(ctx, pay, "IRT", 150_000); len(ms) != 0 {
		t.Fatalf("a Toman plan without a rate: %v", ms)
	}
	if ms, _ := svc.PaymentMethods(ctx, pay, "USDT", 0); len(ms) != 1 || ms[0] != USDTAuto {
		t.Fatalf("a USDT plan: %v", ms)
	}
	if ms, _ := svc.PaymentMethods(ctx, pay, "USDT", 50_000); len(ms) != 0 {
		t.Fatalf("0.05 USDT: %v", ms)
	}
	setSetting(t, s, "payments.usdt_rate", "60000")
	if ms, _ := svc.PaymentMethods(ctx, pay, "IRT", 150_000); len(ms) != 1 {
		t.Fatalf("a Toman plan with a rate: %v", ms)
	}

	u := seedUser(t, s, 2201)
	pl := seedPlan(t, s, false, 150_001)
	o, err := svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: pl.ID, IdempotencyKey: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.CreatePaymentIntent(ctx, pay, CreatePaymentIntentParams{UserID: u.ID, OrderID: o.ID, Provider: USDTAuto,
		IdempotencyKey: "u-int-1"})
	if err != nil {
		t.Fatal(err)
	}
	st := pay.started[0]
	if len(pay.started) != 1 || st.GatewayAmount != 2_510_000 || st.GatewayCurrency != "USDT" || st.Amount != 150_001 || st.Currency != "IRT" {
		t.Fatalf("asked payments for %+v", pay.started)
	}
	// What the customer must send is payments' amount, not core's base.
	if res.GatewayAmount != 2_510_007 || res.GatewayCurrency != "USDT" || res.Amount != 150_001 || res.Currency != "IRT" ||
		res.Details["pay_address"] != testTronAddress || res.Details["network"] != "TRC20" ||
		res.Details["expires_at"] != "1800000000" || res.Details["reference"] == "" || res.Details["txid"] != "" {
		t.Fatalf("intent: %+v", res)
	}

	// A paid automatic USDT payment pays the order like any gateway: the
	// intent's own amount, in the order's currency.
	m := payEvent(t, "u-paid", events.PaymentsPaymentSucceeded, events.PaymentEvent{IntentID: res.IntentID, OrderID: o.ID,
		UserID: u.ID, Provider: USDTAuto, Amount: 150_001, Currency: "IRT"})
	for range 2 {
		if err := svc.HandlePaymentEvent(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	paid := outboxEvents(t, s, events.OrderPaid)
	var e events.OrderPaidEvent
	if len(paid) != 1 || json.Unmarshal(paid[0], &e) != nil || e.Source != events.PaidByGateway || e.Provider != USDTAuto || e.Amount != 150_001 {
		t.Fatalf("order paid: %s", paid)
	}
	if bal, sum, rows := ledger(t, s, u.ID); bal != 0 || sum != 0 || rows != 2 {
		t.Fatalf("balance=%d ledger=%d rows=%d", bal, sum, rows)
	}
}

func TestGatewayDetails(t *testing.T) {
	gi := &GatewayIntent{Provider: USDTAuto, Status: "succeeded", PayAddress: testTronAddress, ExpiresAt: 42,
		ExternalID: testTXID + "#1"}
	if d := GatewayDetails(gi); d["txid"] != testTXID || d["pay_address"] != testTronAddress || d["expires_at"] != "42" {
		t.Fatalf("details: %v", d)
	}
	gi.Status = "pending"
	if d := GatewayDetails(gi); d["txid"] != "" {
		t.Fatalf("a txid before it paid: %v", d)
	}
	if d := GatewayDetails(&GatewayIntent{Provider: Zarinpal, ExpiresAt: 42}); len(d) != 0 {
		t.Fatalf("zarinpal: %v", d)
	}
}

// The owner's USDT settings reach payments normalised; the audit keeps the
// address (public) before and after, and only "changed" for the key.
func TestUSDTSettingsAreAuditedWithoutTheKey(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	fg := &fakeGateways{cfg: GatewayConfig{Provider: USDTAuto}}

	for field, u := range map[string]GatewayUpdate{
		"address":     {Address: testTronAddress[:10] + "z" + testTronAddress[11:]},
		"api_key":     {Address: testTronAddress, APIKey: "s3cr3t key!"},
		"merchant_id": {Address: testTronAddress, MerchantID: testMerchant},
		"sandbox":     {Address: testTronAddress, Sandbox: true},
	} {
		u.Provider, u.Enabled = USDTAuto, true
		if _, err := svc.SetGatewayConfig(ctx, own, fg, u); fieldOf(err) != field || strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("%s: %v", field, err)
		}
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: USDTAuto, Address: usdtContract}); fieldOf(err) != "address" {
		t.Fatalf("the contract: %v", err)
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: Zarinpal, Address: testTronAddress}); fieldOf(err) != "address" {
		t.Fatalf("an address for zarinpal: %v", err)
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: Zarinpal, APIKey: testTronKey}); fieldOf(err) != "api_key" {
		t.Fatalf("a key for zarinpal: %v", err)
	}
	if fg.sets != 0 {
		t.Fatalf("payments asked %d times", fg.sets)
	}

	// Persian digits and a stray space are what an owner may paste.
	c, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: "USDT_TRC20_AUTO", Enabled: true,
		Address: " " + testTronAddress + " ", APIKey: " " + testTronKey + " ", Proxy: testProxy})
	if err != nil || !c.Offered || c.Address != testTronAddress || !c.APIKeySet {
		t.Fatalf("set: %+v %v", c, err)
	}
	if fg.cfg.Address != testTronAddress || fg.apiKey != testTronKey || fg.proxy != testProxy || fg.actor != own.ID {
		t.Fatalf("payments got %q %q %q from %q", fg.cfg.Address, fg.apiKey, fg.proxy, fg.actor)
	}
	// A new address, then clearing the key, then nothing new.
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: USDTAuto, Enabled: true, Address: otherTronAddress}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: USDTAuto, Enabled: true, ClearAPIKey: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: USDTAuto, Enabled: true, Address: otherTronAddress}); err != nil {
		t.Fatal(err)
	}
	entries := auditJSON(t, s, "gateway.update")
	if len(entries) != 3 {
		t.Fatalf("entries: %v", entries)
	}
	if e := entries[0]; !strings.Contains(e, `"address": ""`) || !strings.Contains(e, `"address": "`+testTronAddress+`"`) ||
		!strings.Contains(e, `"api_key": "changed"`) || !strings.Contains(e, `"proxy": "changed"`) || strings.Contains(e, "sandbox") {
		t.Fatalf("first entry: %s", e)
	}
	if e := entries[1]; !strings.Contains(e, `"address": "`+testTronAddress+`"`) || !strings.Contains(e, `"address": "`+otherTronAddress+`"`) ||
		strings.Contains(e, "api_key") {
		t.Fatalf("address change: %s", e)
	}
	if e := entries[2]; !strings.Contains(e, `"api_key": "cleared"`) {
		t.Fatalf("clearing the key: %s", e)
	}
	var all string
	if err := s.DB().QueryRow(ctx, `SELECT string_agg(coalesce(before::text,'') || coalesce(after::text,''), ' ') FROM core.audit_log`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testTronKey, "25f66928", "pr0xy-passw0rd", "203.0.113.10"} {
		if strings.Contains(all, secret) {
			t.Fatalf("%q reached the audit log: %s", secret, all)
		}
	}

	// A test passes the candidate on, normalised and checked.
	if _, err := svc.TestGateway(ctx, fg, USDTAuto, &GatewayCandidate{Address: "\u200f" + otherTronAddress, APIKey: testTronKey}); err != nil ||
		fg.tested[0].Address != otherTronAddress || fg.tested[0].APIKey != testTronKey {
		t.Fatalf("test: %v %+v", err, fg.tested)
	}
	if _, err := svc.TestGateway(ctx, fg, USDTAuto, &GatewayCandidate{APIKey: testTronKey, ClearAPIKey: true}); fieldOf(err) != "api_key" {
		t.Fatalf("set and clear: %v", err)
	}
	if _, err := svc.TestGateway(ctx, fg, USDTAuto, &GatewayCandidate{Address: "TXYZ"}); fieldOf(err) != "address" || len(fg.tested) != 1 {
		t.Fatalf("a bad address tested: %v", err)
	}
}

// Switching automatic USDT on needs the crypto_watcher feature and the
// gateway in what is in effect: a license's, the trial's (it has both), and
// never the free tier's.
func TestUSDTLicenseGate(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	gate := func(err error) string {
		var lr *LicenseRefusal
		if errors.As(err, &lr) && errors.Is(err, ErrState) {
			return lr.Gate
		}
		return ""
	}
	on := func() (*fakeGateways, error) {
		fg := &fakeGateways{cfg: GatewayConfig{Provider: USDTAuto}}
		_, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: USDTAuto, Enabled: true, Address: testTronAddress})
		return fg, err
	}
	if _, err := on(); err != nil {
		t.Fatalf("unlicensed, in the trial: %v", err)
	}
	v := licensed(t, svc, time.Now().UTC())
	install(t, s, v.token(func(t *license.Token) { t.Domain = "other.example" }))
	if _, err := on(); err != nil {
		t.Fatalf("an invalid license, in the trial: %v", err)
	}
	// The trial is over: the free tier has no automatic USDT, with no license
	// or one that is not valid here.
	endTrial(t, s, time.Now().UTC())
	if fg, err := on(); gate(err) != "crypto_watcher" || fg.sets != 0 || !strings.Contains(err.Error(), "the free tier") {
		t.Fatalf("an invalid license, on the free tier: %v", err)
	}
	if _, err := s.DB().Exec(ctx, `DELETE FROM core.license`); err != nil {
		t.Fatal(err)
	}
	if fg, err := on(); gate(err) != "crypto_watcher" || fg.sets != 0 {
		t.Fatalf("unlicensed, on the free tier: %v", err)
	}

	install(t, s, v.token(func(t *license.Token) { t.Entitlements.CryptoWatcher = false }))
	if fg, err := on(); gate(err) != "crypto_watcher" || fg.sets != 0 || !strings.Contains(err.Error(), "crypto_watcher") {
		t.Fatalf("without crypto_watcher: %v", err)
	}
	install(t, s, v.token(func(t *license.Token) {
		t.Entitlements.CryptoWatcher, t.Entitlements.Gateways = true, []string{Zarinpal}
	}))
	if _, err := on(); gate(err) != "gateways" {
		t.Fatalf("without the gateway: %v", err)
	}
	install(t, s, v.token(func(t *license.Token) { t.Entitlements.CryptoWatcher = true }))
	if fg, err := on(); err != nil || fg.sets != 1 {
		t.Fatalf("licensed: %v", err)
	}
	// What is on stays on: an address change while on is not gated.
	install(t, s, v.token(func(t *license.Token) { t.Entitlements.CryptoWatcher = false }))
	fg := &fakeGateways{cfg: GatewayConfig{Provider: USDTAuto, Enabled: true, Address: testTronAddress}}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: USDTAuto, Enabled: true, Address: otherTronAddress}); err != nil {
		t.Fatalf("already on: %v", err)
	}
	// An expired license grants nothing.
	install(t, s, v.token(func(t *license.Token) {
		t.Entitlements.CryptoWatcher = true
		t.ExpiresAt, t.GraceDays = time.Now().Add(-30*24*time.Hour), 1
	}))
	if _, err := on(); gate(err) != "crypto_watcher" || !strings.Contains(err.Error(), "the free tier") {
		t.Fatalf("expired: %v", err)
	}
}

// Report health: automatic USDT is healthy while the watcher reads the
// chain; failing reads count after 5 minutes.
func TestUSDTReportHealth(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := &GatewayReport{Provider: USDTAuto, LastError: &GatewayEvent{At: now.Unix() - 60, Kind: "scan"}}
	if !r.Healthy(now) {
		t.Fatal("an old scan error with reads working since")
	}
	r.ScanError, r.LastScanAt, r.LastScanOKAt = "trongrid: HTTP 503", now.Unix()-10, now.Unix()-120
	if !r.Healthy(now) {
		t.Fatal("one failed read")
	}
	r.LastScanOKAt = now.Unix() - 301
	if r.Healthy(now) {
		t.Fatal("reads failing for 5 minutes")
	}
	r.LastScanOKAt = 0
	if r.Healthy(now) {
		t.Fatal("never read")
	}
}

func depositEvent(t *testing.T, eventID string, e events.DepositUnmatchedEvent) eventbus.Message {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return eventbus.Message{EventID: eventID, Topic: events.PaymentsDepositUnmatched, Payload: b}
}

// Payments' "unmatched deposit" reaches the bot (the staff alert) exactly
// once, as it was; nothing is credited.
func TestUnmatchedDepositIsPassedOnOnce(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	e := events.DepositUnmatchedEvent{TxID: testTXID, EventIndex: 1, Network: "TRC20", From: otherTronAddress, To: testTronAddress,
		Amount: 12_340_000, Currency: "USDT", BlockTime: 1_800_000_000, Reason: events.UnmatchedNoPayment}
	m := depositEvent(t, "d-1", e)
	for range 3 {
		if err := svc.HandlePaymentEvent(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	got := outboxEvents(t, s, events.DepositUnmatched)
	var out events.DepositUnmatchedEvent
	if len(got) != 1 || json.Unmarshal(got[0], &out) != nil || out != e {
		t.Fatalf("passed on: %s", got)
	}
	// Another deposit is another alert.
	e2 := e
	e2.EventIndex, e2.Reason = 0, events.UnmatchedAlreadyPaid
	if err := svc.HandlePaymentEvent(ctx, depositEvent(t, "d-2", e2)); err != nil || len(outboxEvents(t, s, events.DepositUnmatched)) != 2 {
		t.Fatalf("second deposit: %v", err)
	}
	var credits int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM core.ledger_entries`).Scan(&credits); err != nil || credits != 0 {
		t.Fatalf("ledger rows: %d %v", credits, err)
	}
	// What cannot be a USDT deposit is dead-lettered, not retried for ever.
	for _, bad := range []events.DepositUnmatchedEvent{
		{TxID: "nope", Amount: 1, Currency: "USDT"}, {TxID: testTXID, Amount: 0, Currency: "USDT"},
		{TxID: testTXID, Amount: 5, Currency: "IRT"}, {TxID: strings.ToUpper(testTXID), Amount: 5, Currency: "USDT"},
		{TxID: testTXID, EventIndex: -1, Amount: 5, Currency: "USDT"},
	} {
		if err := svc.HandlePaymentEvent(ctx, depositEvent(t, "d-bad", bad)); !eventbus.IsPermanent(err) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if err := svc.HandlePaymentEvent(ctx, eventbus.Message{EventID: "d-x", Topic: events.PaymentsDepositUnmatched, Payload: []byte(`{`)}); !eventbus.IsPermanent(err) {
		t.Fatalf("malformed: %v", err)
	}
}

// Payments' "a manual proof took a deposit the watcher saw" reaches the bot
// (the staff alert) exactly once, as it was; nothing is credited.
func TestClaimedDepositIsPassedOnOnce(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	const intent = "0192f0c8-7b1a-7c3d-9e4f-5a6b7c8d9e0f"
	e := events.DepositClaimedEvent{TxID: testTXID, EventIndex: 1, Network: "TRC20", From: otherTronAddress, To: testTronAddress,
		Amount: 12_340_000, Currency: "USDT", BlockTime: 1_800_000_000, Reason: events.UnmatchedNoPayment,
		IntentID: intent, Proof: "waiting"}
	claim := func(eventID string, e events.DepositClaimedEvent) eventbus.Message {
		t.Helper()
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		return eventbus.Message{EventID: eventID, Topic: events.PaymentsDepositClaimed, Payload: b}
	}
	m := claim("c-1", e)
	for range 3 {
		if err := svc.HandlePaymentEvent(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	got := outboxEvents(t, s, events.DepositClaimed)
	var out events.DepositClaimedEvent
	if len(got) != 1 || json.Unmarshal(got[0], &out) != nil || out != e {
		t.Fatalf("passed on: %s", got)
	}
	// Another claim is another alert.
	e2 := e
	e2.EventIndex, e2.Reason, e2.Proof = 0, "dust", "approved"
	if err := svc.HandlePaymentEvent(ctx, claim("c-2", e2)); err != nil || len(outboxEvents(t, s, events.DepositClaimed)) != 2 {
		t.Fatalf("second claim: %v", err)
	}
	if n := len(outboxEvents(t, s, events.DepositUnmatched)); n != 0 {
		t.Fatalf("unmatched alerts: %d", n)
	}
	var credits int
	if err := s.DB().QueryRow(ctx, `SELECT count(*) FROM core.ledger_entries`).Scan(&credits); err != nil || credits != 0 {
		t.Fatalf("ledger rows: %d %v", credits, err)
	}
	// What cannot be a claimed USDT deposit is dead-lettered, not retried for
	// ever.
	for _, bad := range []events.DepositClaimedEvent{
		{TxID: "nope", Amount: 1, Currency: "USDT", IntentID: intent}, {TxID: testTXID, Amount: 0, Currency: "USDT", IntentID: intent},
		{TxID: testTXID, Amount: 5, Currency: "IRT", IntentID: intent}, {TxID: strings.ToUpper(testTXID), Amount: 5, Currency: "USDT", IntentID: intent},
		{TxID: testTXID, EventIndex: -1, Amount: 5, Currency: "USDT", IntentID: intent},
		{TxID: testTXID, Amount: 5, Currency: "USDT"}, {TxID: testTXID, Amount: 5, Currency: "USDT", IntentID: "not-a-uuid"},
		{TxID: testTXID, Amount: 5, Currency: "USDT", IntentID: strings.ToUpper(intent)},
	} {
		if err := svc.HandlePaymentEvent(ctx, claim("c-bad", bad)); !eventbus.IsPermanent(err) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if err := svc.HandlePaymentEvent(ctx, eventbus.Message{EventID: "c-x", Topic: events.PaymentsDepositClaimed, Payload: []byte(`{`)}); !eventbus.IsPermanent(err) {
		t.Fatalf("malformed: %v", err)
	}
	if n := len(outboxEvents(t, s, events.DepositClaimed)); n != 2 {
		t.Fatalf("claimed alerts after the bad ones: %d", n)
	}
}

// Staff mark an unmatched deposit handled: checked, passed on, audited with
// the note; only unmatched ones.
func TestResolveDeposit(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	fg := &fakeGateways{transfers: []ChainTransfer{
		{TxID: testTXID, EventIndex: 0, From: otherTronAddress, To: testTronAddress, Amount: 12_340_000, Currency: "USDT", Outcome: "unmatched", Reason: "no_payment"},
		{TxID: testTXID, EventIndex: 1, Amount: 5_000_000, Currency: "USDT", Outcome: "matched"},
	}}
	for field, c := range map[string]struct {
		txid  string
		index int
		note  string
	}{
		"txid":        {"zz", 0, ""},
		"event_index": {testTXID, -1, ""},
		"note":        {testTXID, 0, strings.Repeat("ن", 501)},
	} {
		if _, err := svc.ResolveDeposit(ctx, own, fg, c.txid, c.index, c.note); fieldOf(err) != field {
			t.Errorf("%s: %v", field, err)
		}
	}
	got, err := svc.ResolveDeposit(ctx, own, fg, "0x"+strings.ToUpper(testTXID), 0, "  credited 12.34 to @payer  ")
	if err != nil || got.Outcome != "resolved" || len(fg.resolved) != 1 || fg.resolved[0] != testTXID+"#0|"+own.ID+"|credited 12.34 to @payer" {
		t.Fatalf("resolve: %+v %v %v", got, err, fg.resolved)
	}
	entries := auditJSON(t, s, "gateway.deposit_resolve")
	var reason, entityID string
	if err := s.DB().QueryRow(ctx, `SELECT reason, entity_id FROM core.audit_log WHERE action = 'gateway.deposit_resolve'`).Scan(&reason, &entityID); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0], `"amount": 12340000`) || !strings.Contains(entries[0], `"outcome": "resolved"`) ||
		reason != "credited 12.34 to @payer" || entityID != testTXID+"#0" {
		t.Fatalf("audit: %v %q %q", entries, reason, entityID)
	}
	// Not unmatched (any more): payments refuses, nothing is audited.
	for _, i := range []int{0, 1} {
		if _, err := svc.ResolveDeposit(ctx, own, fg, testTXID, i, ""); err == nil {
			t.Fatalf("transfer %d resolved again", i)
		}
	}
	if n := auditCount(t, s, "gateway.deposit_resolve"); n != 1 {
		t.Fatalf("audits: %d", n)
	}

	// The log: an outcome and a transaction id are checked and normalised.
	if _, _, err := svc.ChainTransfers(ctx, fg, ChainTransferFilter{Outcome: "lost"}, 1, 25); fieldOf(err) != "outcome" {
		t.Fatalf("bad outcome: %v", err)
	}
	if _, _, err := svc.ChainTransfers(ctx, fg, ChainTransferFilter{TxID: "xyz"}, 1, 25); fieldOf(err) != "txid" {
		t.Fatalf("bad txid: %v", err)
	}
	list, total, err := svc.ChainTransfers(ctx, fg, ChainTransferFilter{Outcome: " Resolved ", TxID: "0X" + strings.ToUpper(testTXID)}, 1, 25)
	if err != nil || total != 1 || len(list) != 1 || fg.filters[len(fg.filters)-1].TxID != testTXID {
		t.Fatalf("list: %v %d %v", list, total, err)
	}
}
