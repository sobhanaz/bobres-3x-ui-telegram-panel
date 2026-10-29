package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/toncenter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/tonprice"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonaddr"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonfake"
)

// Test fixtures, not secrets.
const (
	tonKey      = "ton-test-ton-test-ton-test" //nolint:gosec // test fixture
	tonKeyOther = "ton-other-ton-other-ton"    //nolint:gosec // test fixture
)

var tonHash = [32]byte{0x11, 0x22, 0x33, 0x44}

// tonStore is the store's address as the owner may paste it (bounceable), tonCanon how it is kept and shown.
func tonStore() string { return tonaddr.Address{Hash: tonHash}.Friendly(true) }
func tonCanon() string { return tonaddr.Address{Hash: tonHash}.Canonical() }

type tonRig struct {
	fake  *tonfake.Server
	price *tonprice.Feed
}

func newTONRig(t *testing.T) *tonRig {
	t.Helper()
	fake := tonfake.New()
	t.Cleanup(fake.Close)
	feed, err := tonprice.Default(tonprice.Config{BinanceURL: fake.BinanceURL(), OKXURL: fake.OKXURL()})
	if err != nil {
		t.Fatal(err)
	}
	return &tonRig{fake: fake, price: feed}
}

func (r *tonRig) options(env string) GatewayOptions {
	return GatewayOptions{Env: env, Build: (&builder{}).build, TONPrice: r.price,
		BuildTON: func(c TONSettings) (gateway.TONSource, error) {
			cl, err := toncenter.New(toncenter.Config{APIKey: c.APIKey, Proxy: c.Proxy, BaseURL: r.fake.URL})
			if err != nil {
				return nil, err
			}
			return cl, nil
		}}
}

// tonService is a service with automatic TON wired to the fake, switched on with the store's address, and a
// fresh price.
func tonService(t *testing.T, rig *tonRig) (*Service, *store.Store, *bytes.Buffer) {
	t.Helper()
	svc, st, logs := cfgService(t)
	ctx := context.Background()
	if _, err := st.DB().Exec(ctx, `TRUNCATE payments.ton_cursors, payments.ton_transfers CASCADE`); err != nil {
		t.Fatal(err)
	}
	o := rig.options("dev")
	o.Envelope = envelope(t, keyA)
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: true, Address: tonStore(), APIKey: tonKey, ActorID: actor}); err != nil {
		t.Fatal(err)
	}
	return svc, st, logs
}

func tonConfig(svc *Service) GatewayConfig {
	for _, c := range svc.GatewayConfigs() {
		if c.Provider == TONAuto {
			return c
		}
	}
	return GatewayConfig{}
}

// mistype changes one character of an address, so that its checksum is wrong.
func mistype(a string) string {
	c := "A"
	if a[20:21] == "A" {
		c = "B"
	}
	return a[:20] + c + a[21:]
}

func tonOrder(n int) *string {
	o := fmt.Sprintf("00000000-0000-7000-8000-%012d", 0xd0b100+n)
	return &o
}

// tonIntent is an automatic TON payment for a price in USDT (micro).
func tonIntent(key string, usdt int64, o *string) *store.Intent {
	ga, gc := usdt, "USDT"
	return &store.Intent{UserID: uid, OrderID: o, Provider: TONAuto, Amount: 150_000, Currency: "IRT", IdempotencyKey: key,
		GatewayAmount: &ga, GatewayCurrency: &gc}
}

const tonBase = 2_500_000 // 2.5 USDT

// nanoFor is the independent arithmetic of the amount: ceil(usdt * 1e9 / price).
func nanoFor(usdt, price int64) int64 { return (usdt*1_000_000_000 + price - 1) / price }

func startTON(t *testing.T, svc *Service, key string, o *string) *store.Intent {
	t.Helper()
	in, err := svc.StartTON(context.Background(), tonIntent(key, tonBase, o))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return in
}

func scanTON(t *testing.T, svc *Service) {
	t.Helper()
	if _, err := svc.ScanTONOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

func tonIntentOf(t *testing.T, st *store.Store, id string) *store.Intent {
	t.Helper()
	in, err := st.GetIntent(context.Background(), nil, id)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// tonUnmatchedEvents are the deposit_unmatched events in the outbox, oldest first.
func tonUnmatchedEvents(t *testing.T, st *store.Store) []DepositUnmatchedEvent {
	t.Helper()
	rows, err := st.DB().Query(context.Background(), `SELECT payload FROM payments.outbox_payments WHERE topic = $1 ORDER BY id`, TopicDepositUnmatched)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []DepositUnmatchedEvent
	for rows.Next() {
		var raw []byte
		var e DepositUnmatchedEvent
		if err := rows.Scan(&raw); err != nil || json.Unmarshal(raw, &e) != nil {
			t.Fatal("bad event")
		}
		out = append(out, e)
	}
	return out
}

func tonTransfers(t *testing.T, st *store.Store, outcome string) []store.ChainTransfer {
	t.Helper()
	list, _, err := st.ListTONTransfers(context.Background(), store.ChainTransferFilter{Outcome: outcome}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func tonSucceeded(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(context.Background(), `SELECT count(*) FROM payments.outbox_payments WHERE topic = $1`, events.PaymentsPaymentSucceeded).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTONMemoAndAmountArithmetic(t *testing.T) {
	id := "01a183fc-a8a7-7845-9d0c-b5cccdb4aac7"
	if got := tonMemoOf(id); got != "BOB-CDB4AAC7" { // the last 8 hex digits of the id, upper case
		t.Fatalf("memo %s", got)
	}
	for in, want := range map[string]string{"BOB-3F9A1C20": "BOB-3F9A1C20", " bob-3f9a1c20 ": "BOB-3F9A1C20", "BOB-3F9A1C2": "", "BOB-3F9A1C200": "",
		"BOB-3F9A1C2G": "", "3F9A1C20": "", "": "", "BOB-3F9A1C20 extra": "", "XBOB-3F9A1C20": ""} {
		if got := tonMemoIn(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	if n, ok := tonNano(2_500_000, 3_214_500); !ok || n != nanoFor(2_500_000, 3_214_500) {
		t.Fatalf("%d %v", n, ok)
	}
	// Rounded up, never down: one micro-USDT at a price that does not divide.
	if n, _ := tonNano(1, 3_000_000); n != 334 {
		t.Fatalf("1 micro-USDT at 3 USDT per TON is 333.33 nanoton, rounded up: %d", n)
	}
	if _, ok := tonNano(1, 0); ok {
		t.Fatal("no price, an amount")
	}
	if _, ok := tonNano(tonMaxUSDT*1_000_000, 1); ok {
		t.Fatal("an amount that does not fit")
	}
	// What arrived is worth, at a price, rounded down.
	if w := tonWorth(334, 3_000_000); w.Int64() != 1 {
		t.Fatalf("worth %v", w)
	}
}

// The settings: off until an address is set; any form of the address is kept as the non-bounceable one; the
// secrets are stored encrypted and never logged; and the method is offered only with a fresh price.
func TestTONSettingsAndOffer(t *testing.T) {
	rig := newTONRig(t)
	svc, st, logs := cfgService(t)
	ctx := context.Background()
	o := rig.options("prod")
	o.Envelope = envelope(t, keyA)
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	cfgs := svc.GatewayConfigs()
	if len(cfgs) != 3 || cfgs[2].Provider != TONAuto {
		t.Fatalf("configs: %+v", cfgs)
	}
	if got := tonConfig(svc); got.Enabled || got.Configured || got.Offered || got.Address != "" || got.PriceFresh {
		t.Fatalf("on without an address: %+v", got)
	}
	for name, u := range map[string]GatewayUpdate{
		"switched on without an address": {Enabled: true},
		"a TRON address":                 {Address: "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r"},
		"a mistyped character":           {Address: mistype(tonStore())},
		"the masterchain":                {Address: "-1:" + strings.Repeat("ab", 32)},
		"a key with a space":             {Address: tonStore(), APIKey: "has space in the key"},
		"a merchant id":                  {Address: tonStore(), MerchantID: merchantA},
		"a sandbox":                      {Address: tonStore(), Sandbox: true},
		"a key and a clear":              {Address: tonStore(), APIKey: tonKey, ClearAPIKey: true},
	} {
		u.Provider = TONAuto
		if _, err := svc.SetGatewayConfig(ctx, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := svc.StartTON(ctx, tonIntent("off-1", tonBase, tonOrder(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("started while off: %v", err)
	}
	got, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: true, Address: " " + tonStore() + " ", APIKey: tonKey, Proxy: proxyURL, ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	// On, readable, but with no price read yet: configured, not offered, and not in the list customers get.
	if !got.Enabled || !got.Configured || got.Offered || got.Address != tonCanon() || !got.APIKeySet || !got.ProxySet || got.UpdatedBy != actor || got.PriceFresh {
		t.Fatalf("after switching on: %+v", got)
	}
	if offers(svc, TONAuto) {
		t.Fatal("offered with no price")
	}
	if _, err := svc.StartTON(ctx, tonIntent("noprice-1", tonBase, tonOrder(1))); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("started with no price: %v", err)
	}
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	if got := tonConfig(svc); !got.Offered || !got.PriceFresh || got.PriceMicro != 3_214_500 || got.PriceSource != "binance" || time.Since(got.PriceAt) > time.Minute {
		t.Fatalf("with a price: %+v", got)
	}
	if !offers(svc, TONAuto) {
		t.Fatal("not offered with a fresh price")
	}
	// The price goes stale on its own: not offered again, and the last price stays visible.
	stale := *svc.tonQ.Load()
	stale.At = time.Now().Add(-tonQuoteFresh - time.Second)
	svc.tonQ.Store(&stale)
	if got := tonConfig(svc); got.Offered || got.PriceFresh || got.PriceMicro != 3_214_500 || offers(svc, TONAuto) {
		t.Fatalf("with a stale price: %+v", got)
	}
	if _, err := svc.StartTON(ctx, tonIntent("stale-1", tonBase, tonOrder(1))); !errors.Is(err, ErrTONUnavailable) {
		t.Fatalf("started with a stale price: %v", err)
	}
	// Stored encrypted, round trip, never logged.
	row, err := st.GetGatewayConfig(ctx, nil, TONAuto)
	if err != nil || len(row.AddressEnc) == 0 || strings.Contains(string(row.AddressEnc), tonCanon()) || strings.Contains(string(row.APIKeyEnc), tonKey) {
		t.Fatalf("stored row: %+v %v", row, err)
	}
	if dec, err := decryptTON(envelope(t, keyA), row); err != nil || dec.Address != tonCanon() || dec.APIKey != tonKey || dec.Proxy != proxyURL {
		t.Fatalf("round trip: %+v %v", dec, err)
	}
	_ = logs
	// A restart reads it back; another master key cannot.
	svc2, _ := testService(t)
	if err := svc2.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if got := tonConfig(svc2); !got.Enabled || !got.Configured || got.Address != tonCanon() || got.Offered {
		t.Fatalf("after a restart (no price yet): %+v", got)
	}
	svc3, _ := testService(t)
	o3 := rig.options("prod")
	o3.Envelope = envelope(t, keyB)
	if err := svc3.ConfigureGateways(ctx, o3); err != nil {
		t.Fatal(err)
	}
	if got := tonConfig(svc3); got.Problem != ProblemUnreadable || got.Configured || got.Offered {
		t.Fatalf("unreadable: %+v", got)
	}
	if _, err := svc3.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("re-enabled without entering the address: %v", err)
	}
}

func TestTONSecretsAreNeverLogged(t *testing.T) {
	rig := newTONRig(t)
	svc, _, logs := cfgService(t)
	ctx := context.Background()
	o := rig.options("dev")
	o.Envelope = envelope(t, keyA)
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: true, Address: tonStore(), APIKey: tonKey, Proxy: proxyURL}); err != nil {
		t.Fatal(err)
	}
	rig.fake.OnlyAPIKey("another-key-0123456789")
	_, _ = svc.ScanTONOnce(ctx)
	r, _ := svc.TestGateway(ctx, TONAuto, nil)
	for _, where := range []string{logs.String(), r.Detail, svc.ton.Load().settings.String(), fmt.Sprintf("%+v %#v", svc.ton.Load().settings, svc.ton.Load().settings)} {
		for _, secret := range []string{tonKey, "pr0xy-pw", "203.0.113.10"} {
			if strings.Contains(where, secret) {
				t.Errorf("a secret (%q) is in %q", secret, where)
			}
		}
	}
}

func TestStartTONCreatesTheExactPayment(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	in := startTON(t, svc, "t-1", tonOrder(1))
	want := nanoFor(tonBase, 3_214_500)
	if in.Status != "pending" || in.Provider != TONAuto || in.GatewayAmount == nil || *in.GatewayAmount != want || *in.GatewayCurrency != "TON" ||
		in.PayAddress == nil || *in.PayAddress != tonCanon() || in.PayMemo == nil || *in.PayMemo != tonMemoOf(in.ID) || !tonMemoRe.MatchString(*in.PayMemo) ||
		in.QuoteUSDT == nil || *in.QuoteUSDT != tonBase || *in.QuoteRate != 3_214_500 || *in.QuoteSource != "binance" {
		t.Fatalf("created: %+v", in)
	}
	if left := time.Until(*in.ExpiresAt); left > tonPayWindow || left < tonPayWindow-time.Minute {
		t.Fatalf("window: %v", left)
	}
	// The same key returns the same payment; the same customer asking again for the same order, a new key, too.
	if again, err := svc.StartTON(ctx, tonIntent("t-1", tonBase, tonOrder(1))); err != nil || again.ID != in.ID {
		t.Fatalf("retry: %+v %v", again, err)
	}
	if again := startTON(t, svc, "t-1b", tonOrder(1)); again.ID != in.ID {
		t.Fatalf("asking again made another payment: %s / %s", again.ID, in.ID)
	}
	// A key reused for another payment is refused; another order is another payment with its own comment.
	if _, err := svc.StartTON(ctx, tonIntent("t-1", tonBase, tonOrder(9))); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("a key reused for another order: %v", err)
	}
	other := startTON(t, svc, "t-2", tonOrder(2))
	if other.ID == in.ID || *other.PayMemo == *in.PayMemo {
		t.Fatalf("two payments, one comment: %+v %+v", in, other)
	}
	// The price moves: the payment already made keeps its amount.
	rig.fake.SetPrice("binance", "4.0")
	rig.fake.SetPrice("okx", "4.0")
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	if got := tonIntentOf(t, st, in.ID); *got.GatewayAmount != want {
		t.Fatalf("the amount moved with the price: %d", *got.GatewayAmount)
	}
	if later := startTON(t, svc, "t-3", tonOrder(3)); *later.GatewayAmount != nanoFor(tonBase, 4_000_000) {
		t.Fatalf("a new payment is at the new price: %d", *later.GatewayAmount)
	}
	// Bounds and kinds.
	for name, in := range map[string]*store.Intent{
		"under 0.1 USDT": tonIntent("b-1", 99_999, tonOrder(4)),
		"over 100,000":   tonIntent("b-2", tonMaxUSDT+1, tonOrder(5)),
	} {
		if _, err := svc.StartTON(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	noCur := tonIntent("b-3", tonBase, tonOrder(6))
	cur := "IRR"
	noCur.GatewayCurrency = &cur
	if _, err := svc.StartTON(ctx, noCur); !errors.Is(err, ErrInvalid) {
		t.Errorf("a gateway amount that is not USDT: %v", err)
	}
	if rows := tonTransfers(t, st, ""); len(rows) != 0 {
		t.Fatalf("starting recorded transfers: %v", rows)
	}
}

func TestStartTONLimitsUnpaidPayments(t *testing.T) {
	rig := newTONRig(t)
	svc, _, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	for i := range tonUserUnpaid {
		startTON(t, svc, fmt.Sprintf("cap-%d", i), tonOrder(100+i))
	}
	if _, err := svc.StartTON(ctx, tonIntent("cap-x", tonBase, tonOrder(200))); !errors.Is(err, ErrTONTooManyUnpaid) {
		t.Fatalf("an eleventh unpaid payment: %v", err)
	}
	// One of the ten, asked for again, is still shown, not refused.
	if again := startTON(t, svc, "cap-again", tonOrder(100)); again.PayMemo == nil {
		t.Fatalf("%+v", again)
	}
}

func TestTONPaymentSettlesOnceWithTheExactAmount(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	in := startTON(t, svc, "s-1", tonOrder(1))
	scanTON(t, svc) // nothing yet
	if got := tonIntentOf(t, st, in.ID); got.Status != "pending" {
		t.Fatalf("%+v", got)
	}
	// Spaces and the case of the comment do not matter.
	hash := rig.fake.Pay(tonStore(), *in.GatewayAmount, " "+strings.ToLower(*in.PayMemo)+" ")
	scanTON(t, svc)
	got := tonIntentOf(t, st, in.ID)
	if got.Status != "succeeded" || got.ExternalID == nil || *got.ExternalID != hash || got.ProviderRef == nil || *got.ProviderRef != hash {
		t.Fatalf("after the transfer: %+v", got)
	}
	if n := ledgerCount(t, st); n != 1 || tonSucceeded(t, st) != 1 {
		t.Fatalf("ledger %d, events %d", n, tonSucceeded(t, st))
	}
	ev := outbox(t, st)[events.PaymentsPaymentSucceeded][0]
	if ev.Amount != 150_000 || ev.Currency != "IRT" || ev.IntentID != in.ID {
		t.Fatalf("event: %+v", ev)
	}
	rows := tonTransfers(t, st, "matched")
	if len(rows) != 1 || rows[0].TxID != hash || rows[0].IntentID == nil || *rows[0].IntentID != in.ID || rows[0].Value != *in.GatewayAmount || rows[0].LT <= 0 {
		t.Fatalf("log: %+v", rows)
	}
	// The same transaction read again (the indexer repeats it), a check, a second scan: nothing changes.
	calls := rig.fake.Calls("getTransactions")
	scanTON(t, svc)
	scanTON(t, svc)
	if _, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil {
		t.Fatal(err)
	}
	if n := ledgerCount(t, st); n != 1 || tonSucceeded(t, st) != 1 || len(tonTransfers(t, st, "")) != 1 {
		t.Fatalf("a second read changed something: ledger %d", n)
	}
	_ = calls
	// A settled payment is not asked about by the reconciler.
	if _, err := svc.ReconcileOnce(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestTONOverpaymentCreditsThePriceOnly(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	if err := svc.RefreshTONPrice(context.Background()); err != nil {
		t.Fatal(err)
	}
	in := startTON(t, svc, "o-1", tonOrder(1))
	rig.fake.Pay(tonStore(), *in.GatewayAmount*3, *in.PayMemo)
	scanTON(t, svc)
	if got := tonIntentOf(t, st, in.ID); got.Status != "succeeded" {
		t.Fatalf("%+v", got)
	}
	ev := outbox(t, st)[events.PaymentsPaymentSucceeded]
	if len(ev) != 1 || ev[0].Amount != 150_000 || ledgerCount(t, st) != 1 {
		t.Fatalf("an overpayment credited more than the price: %+v", ev)
	}
	var credited int64
	if err := st.DB().QueryRow(context.Background(), `SELECT amount FROM payments.ledger_entries`).Scan(&credited); err != nil || credited != 150_000 {
		t.Fatalf("ledger amount %d %v", credited, err)
	}
	if rows := tonTransfers(t, st, "matched"); len(rows) != 1 || rows[0].Value != *in.GatewayAmount*3 {
		t.Fatalf("the transfer keeps what arrived: %+v", rows)
	}
}

func TestTONUnderpaymentIsNeverCredited(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	in := startTON(t, svc, "u-1", tonOrder(1))
	short := rig.fake.Pay(tonStore(), *in.GatewayAmount-1, *in.PayMemo) // one nanoton short
	scanTON(t, svc)
	if got := tonIntentOf(t, st, in.ID); got.Status != "pending" || ledgerCount(t, st) != 0 {
		t.Fatalf("a payment one nanoton short settled: %+v", got)
	}
	un := tonTransfers(t, st, "unmatched")
	if len(un) != 1 || un[0].TxID != short || un[0].Reason == nil || *un[0].Reason != UnmatchedUnderpaid || un[0].IntentID == nil || *un[0].IntentID != in.ID {
		t.Fatalf("log: %+v", un)
	}
	ev := tonUnmatchedEvents(t, st)
	if len(ev) != 1 || ev[0].TxID != short || ev[0].Network != "TON" || ev[0].Currency != "TON" || ev[0].Amount != *in.GatewayAmount-1 ||
		ev[0].Reason != UnmatchedUnderpaid || ev[0].Comment != *in.PayMemo || ev[0].IntentID != in.ID || ev[0].Expected != *in.GatewayAmount {
		t.Fatalf("event: %+v", ev)
	}
	// Read again: still one record and one alert. Then the full amount arrives: it settles, once.
	scanTON(t, svc)
	full := rig.fake.Pay(tonStore(), *in.GatewayAmount, *in.PayMemo)
	scanTON(t, svc)
	got := tonIntentOf(t, st, in.ID)
	if got.Status != "succeeded" || *got.ExternalID != full || ledgerCount(t, st) != 1 || len(tonUnmatchedEvents(t, st)) != 1 {
		t.Fatalf("after the full amount: %+v ledger=%d events=%d", got, ledgerCount(t, st), len(tonUnmatchedEvents(t, st)))
	}
	// The short transfer stays unmatched for staff, who credit it by hand and mark it handled, once.
	ctx2 := context.Background()
	r, err := svc.ResolveTONTransfer(ctx2, actor, short, "refunded")
	if err != nil || r.Outcome != "resolved" || r.Note == nil || *r.Note != "refunded" {
		t.Fatalf("resolve: %+v %v", r, err)
	}
	if _, err := svc.ResolveTONTransfer(ctx2, actor, short, "again"); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("resolved twice: %v", err)
	}
	if _, err := svc.ResolveTONTransfer(ctx2, actor, full, "x"); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("a matched transfer resolved: %v", err)
	}
	if _, err := svc.ResolveTONTransfer(ctx2, actor, "zz", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a bad hash: %v", err)
	}
	if _, err := svc.ResolveTONTransfer(ctx2, actor, strings.Repeat("ab", 32), ""); err == nil {
		t.Fatal("an unknown hash resolved")
	}
}

func TestTONUnmatchedTransfersAreRecordedAndAnnouncedNeverCredited(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	in := startTON(t, svc, "m-1", tonOrder(1))
	paid := startTON(t, svc, "m-2", tonOrder(2))
	rig.fake.Pay(tonStore(), *paid.GatewayAmount, *paid.PayMemo)
	scanTON(t, svc)
	if tonIntentOf(t, st, paid.ID).Status != "succeeded" {
		t.Fatal("setup")
	}

	nano := int64(5_000_000_000)
	noComment := rig.fake.Pay(tonStore(), nano, "")
	unknown := rig.fake.Pay(tonStore(), nano, "BOB-00000000")
	words := rig.fake.Pay(tonStore(), nano, "thanks for the vpn")
	second := rig.fake.Pay(tonStore(), *paid.GatewayAmount, *paid.PayMemo) // a second transfer for a paid payment
	dust := rig.fake.Pay(tonStore(), 1_000_000, "")                        // 0.001 TON
	zero := rig.fake.Add(tonfake.Tx{To: tonStore(), From: tonfake.Customer(), Nano: 0, Comment: *in.PayMemo})
	external := rig.fake.Add(tonfake.Tx{To: tonStore()})
	bounced := rig.fake.Add(tonfake.Tx{To: tonStore(), From: tonfake.Customer(), Nano: *in.GatewayAmount, Comment: *in.PayMemo, Bounce: true})
	binary := rig.fake.Add(tonfake.Tx{To: tonStore(), From: tonfake.Customer(), Nano: *in.GatewayAmount, Binary: true})
	elsewhere := rig.fake.Pay(tonaddr.Address{Hash: [32]byte{9}}.Canonical(), nano, *in.PayMemo) // to another account
	scanTON(t, svc)

	if got := tonIntentOf(t, st, in.ID); got.Status != "pending" || ledgerCount(t, st) != 1 || tonSucceeded(t, st) != 1 {
		t.Fatalf("something unmatched credited: %+v ledger=%d", got, ledgerCount(t, st))
	}
	reasons := map[string]string{}
	for _, r := range tonTransfers(t, st, "unmatched") {
		reasons[r.TxID] = *r.Reason
	}
	want := map[string]string{noComment: UnmatchedNoPayment, unknown: UnmatchedNoPayment, words: UnmatchedNoPayment, second: UnmatchedAlreadyPaid,
		binary: UnmatchedNoPayment}
	if fmt.Sprint(reasons) != fmt.Sprint(want) {
		t.Fatalf("unmatched: %v, want %v", reasons, want)
	}
	ignored := map[string]string{}
	for _, r := range tonTransfers(t, st, "ignored") {
		ignored[r.TxID] = *r.Reason
	}
	if len(ignored) != 2 || ignored[dust] != IgnoredDust || ignored[bounced] != IgnoredBounced {
		t.Fatalf("ignored: %v", ignored)
	}
	for _, h := range []string{zero, external, elsewhere} {
		if rows, _, _ := st.ListTONTransfers(ctx, store.ChainTransferFilter{TxID: h}, 10, 0); len(rows) != 0 {
			t.Errorf("%s is no transfer to the store and was recorded: %+v", h, rows)
		}
	}
	// Staff hear of the unmatched ones, once each, and not of the dust or the bounce.
	heard := map[string]bool{}
	for _, e := range tonUnmatchedEvents(t, st) {
		if heard[e.TxID] {
			t.Errorf("%s announced twice", e.TxID)
		}
		heard[e.TxID] = true
		if e.Network != "TON" || e.Currency != "TON" || e.To != tonCanon() {
			t.Errorf("event: %+v", e)
		}
	}
	for _, h := range []string{noComment, unknown, words, second, binary} {
		if !heard[h] {
			t.Errorf("%s was not announced", h)
		}
	}
	for _, h := range []string{dust, bounced} {
		if heard[h] {
			t.Errorf("%s was announced", h)
		}
	}
	scanTON(t, svc)
	if len(tonUnmatchedEvents(t, st)) != len(heard) {
		t.Fatal("a second read announced again")
	}
}

func TestTONWaitsForFinality(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	if err := svc.RefreshTONPrice(context.Background()); err != nil {
		t.Fatal(err)
	}
	in := startTON(t, svc, "f-1", tonOrder(1))
	hash := rig.fake.Add(tonfake.Tx{To: tonStore(), From: tonfake.Customer(), Nano: *in.GatewayAmount, Comment: *in.PayMemo, Age: 20 * time.Second})
	scanTON(t, svc)
	if got := tonIntentOf(t, st, in.ID); got.Status != "pending" || ledgerCount(t, st) != 0 || len(tonTransfers(t, st, "")) != 0 {
		t.Fatalf("a transaction 20 seconds old was acted on: %+v", got)
	}
	cur, _ := st.GetTONCursor(context.Background(), tonCanon())
	if cur.LastLT != 0 {
		t.Fatalf("the cursor moved past a transaction that is not final: %d", cur.LastLT)
	}
	rig.fake.SetAge(hash, 90*time.Second) // a minute later
	scanTON(t, svc)
	if got := tonIntentOf(t, st, in.ID); got.Status != "succeeded" {
		t.Fatalf("once final: %+v", got)
	}
}

// A late transfer is settled only if it still covers the price: after the pay window, at the price now.
func TestTONLateTransfers(t *testing.T) {
	late := func(t *testing.T, mutate func(rig *tonRig, svc *Service)) (*store.Intent, *store.Store, string) {
		rig := newTONRig(t)
		svc, st, _ := tonService(t, rig)
		ctx := context.Background()
		if err := svc.RefreshTONPrice(ctx); err != nil {
			t.Fatal(err)
		}
		in := startTON(t, svc, "l-1", tonOrder(1))
		// Its time ran out an hour ago, and the expiry has not run (the watcher was off, say).
		if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET expires_at = now() - interval '1 hour' WHERE id = $1`, in.ID); err != nil {
			t.Fatal(err)
		}
		hash := rig.fake.Pay(tonStore(), *in.GatewayAmount, *in.PayMemo) // two minutes ago: after the window
		mutate(rig, svc)
		scanTON(t, svc)
		return in, st, hash
	}
	t.Run("the price held: settled", func(t *testing.T) {
		in, st, hash := late(t, func(*tonRig, *Service) {})
		if got := tonIntentOf(t, st, in.ID); got.Status != "succeeded" || *got.ExternalID != hash || ledgerCount(t, st) != 1 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("the price rose: settled", func(t *testing.T) {
		in, st, _ := late(t, func(rig *tonRig, svc *Service) {
			rig.fake.SetPrice("binance", "4.0")
			rig.fake.SetPrice("okx", "4.0")
			if err := svc.RefreshTONPrice(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
		if got := tonIntentOf(t, st, in.ID); got.Status != "succeeded" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("the price fell: staff decide", func(t *testing.T) {
		in, st, hash := late(t, func(rig *tonRig, svc *Service) {
			rig.fake.SetPrice("binance", "2.5")
			rig.fake.SetPrice("okx", "2.5")
			if err := svc.RefreshTONPrice(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
		un := tonTransfers(t, st, "unmatched")
		if got := tonIntentOf(t, st, in.ID); got.Status == "succeeded" || ledgerCount(t, st) != 0 || len(un) != 1 || un[0].TxID != hash || *un[0].Reason != UnmatchedLatePrice {
			t.Fatalf("%+v %+v", got, un)
		}
		if ev := tonUnmatchedEvents(t, st); len(ev) != 1 || ev[0].Reason != UnmatchedLatePrice || ev[0].IntentID != in.ID {
			t.Fatalf("events: %+v", ev)
		}
	})
	t.Run("no fresh price: staff decide", func(t *testing.T) {
		in, st, _ := late(t, func(rig *tonRig, svc *Service) {
			q := *svc.tonQ.Load()
			q.At = time.Now().Add(-time.Hour)
			svc.tonQ.Store(&q)
		})
		if got := tonIntentOf(t, st, in.ID); got.Status == "succeeded" || ledgerCount(t, st) != 0 || len(tonTransfers(t, st, "unmatched")) != 1 {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("a transfer inside the window settles whatever the price", func(t *testing.T) {
		rig := newTONRig(t)
		svc, st, _ := tonService(t, rig)
		if err := svc.RefreshTONPrice(context.Background()); err != nil {
			t.Fatal(err)
		}
		in := startTON(t, svc, "l-2", tonOrder(2))
		rig.fake.Pay(tonStore(), *in.GatewayAmount, *in.PayMemo)
		rig.fake.SetPrice("binance", "1.0")
		rig.fake.SetPrice("okx", "1.0")
		if err := svc.RefreshTONPrice(context.Background()); err != nil {
			t.Fatal(err)
		}
		scanTON(t, svc)
		if got := tonIntentOf(t, st, in.ID); got.Status != "succeeded" {
			t.Fatalf("%+v", got)
		}
	})
	t.Run("too late: closed", func(t *testing.T) {
		rig := newTONRig(t)
		svc, st, _ := tonService(t, rig)
		ctx := context.Background()
		if err := svc.RefreshTONPrice(ctx); err != nil {
			t.Fatal(err)
		}
		in := startTON(t, svc, "l-3", tonOrder(3))
		if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET created_at = now() - interval '3 days', expires_at = now() - interval '3 days' WHERE id = $1`, in.ID); err != nil {
			t.Fatal(err)
		}
		rig.fake.Pay(tonStore(), *in.GatewayAmount, *in.PayMemo)
		scanTON(t, svc)
		un := tonTransfers(t, st, "unmatched")
		if tonIntentOf(t, st, in.ID).Status == "succeeded" || len(un) != 1 || *un[0].Reason != UnmatchedClosed {
			t.Fatalf("%+v", un)
		}
	})
}

// A payment whose window ended is expired by a complete read; a payment that cannot be paid any more (an
// amount mismatch) is closed for a transfer too.
func TestTONExpiry(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	live := startTON(t, svc, "e-1", tonOrder(1))
	old := startTON(t, svc, "e-2", tonOrder(2))
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET expires_at = now() - interval '10 minutes' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	scanTON(t, svc)
	if got := tonIntentOf(t, st, old.ID); got.Status != "expired" || got.FailureReason == nil || *got.FailureReason != ReasonExpired {
		t.Fatalf("%+v", got)
	}
	if got := tonIntentOf(t, st, live.ID); got.Status != "pending" {
		t.Fatalf("%+v", got)
	}
	if len(outbox(t, st)[events.PaymentsPaymentRejected]) != 1 {
		t.Fatalf("one rejection expected: %+v", outbox(t, st))
	}
	// A failed read does not expire anything.
	stale := startTON(t, svc, "e-3", tonOrder(3))
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET expires_at = now() - interval '10 minutes' WHERE id = $1`, stale.ID); err != nil {
		t.Fatal(err)
	}
	rig.fake.Down(1000)
	if _, err := svc.ScanTONOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := tonIntentOf(t, st, stale.ID); got.Status != "pending" {
		t.Fatalf("expired by a read that failed: %+v", got)
	}
	// The payment that was mismatched: a transfer for it is closed, not credited.
	rig.fake.Down(0)
	mm := startTON(t, svc, "e-4", tonOrder(4))
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET status = 'failed', failure_reason = 'amount_mismatch' WHERE id = $1`, mm.ID); err != nil {
		t.Fatal(err)
	}
	rig.fake.Pay(tonStore(), *mm.GatewayAmount, *mm.PayMemo)
	scanTON(t, svc)
	if got := tonIntentOf(t, st, mm.ID); got.Status != "failed" || ledgerCount(t, st) != 0 {
		t.Fatalf("%+v", got)
	}
}

// Reading twice at once, a customer's check and the watcher, settle one payment once.
func TestTONSettlesOnceUnderRace(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	in := startTON(t, svc, "r-1", tonOrder(1))
	hash := rig.fake.Pay(tonStore(), *in.GatewayAmount, *in.PayMemo)
	tt := &tonTransfer{hash: hash, lt: 1, from: "x", to: tonCanon(), value: *in.GatewayAmount, comment: *in.PayMemo, ts: time.Now().Add(-time.Hour)}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, err := svc.recordTON(ctx, tt); err != nil {
				t.Errorf("record: %v", err)
			}
		})
		wg.Go(func() { _, _ = svc.ScanTONOnce(ctx) })
		wg.Go(func() { _, _ = svc.CheckIntent(ctx, uid, in.ID, "check") })
	}
	wg.Wait()
	if got := tonIntentOf(t, st, in.ID); got.Status != "succeeded" || ledgerCount(t, st) != 1 || tonSucceeded(t, st) != 1 || len(tonTransfers(t, st, "matched")) != 1 {
		t.Fatalf("%+v ledger=%d events=%d matched=%d", got, ledgerCount(t, st), tonSucceeded(t, st), len(tonTransfers(t, st, "matched")))
	}
	// The same transaction under another logical time is still the same transaction.
	tt2 := *tt
	tt2.lt = 2
	if ok, err := svc.recordTON(ctx, &tt2); err != nil || ok {
		t.Fatalf("a hash seen under another logical time: %v %v", ok, err)
	}
}

// The service being down keeps the payment open, records one scan error, and the next read settles it.
func TestTONUnreachableIndexer(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	in := startTON(t, svc, "d-1", tonOrder(1))
	rig.fake.Pay(tonStore(), *in.GatewayAmount, *in.PayMemo)
	rig.fake.Down(1000)
	for range 3 {
		if _, err := svc.ScanTONOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := tonIntentOf(t, st, in.ID); got.Status != "pending" || ledgerCount(t, st) != 0 {
		t.Fatalf("%+v", got)
	}
	rep, err := svc.GatewayReport(ctx, TONAuto, time.Time{}, time.Time{})
	if err != nil || rep.Chain == nil || rep.Chain.Cursor == nil || rep.Chain.Cursor.LastError == nil || rep.Chain.Cursor.OKAt != nil || rep.OpenCount != 1 {
		t.Fatalf("report: %+v %v", rep, err)
	}
	var errorsKept int
	if err := st.DB().QueryRow(ctx, `SELECT count(*) FROM payments.gateway_events WHERE provider = $1 AND kind = 'scan' AND outcome = 'error'`, TONAuto).Scan(&errorsKept); err != nil || errorsKept != 1 {
		t.Fatalf("one scan error expected: %d %v", errorsKept, err)
	}
	// Rate limited: the same.
	rig.fake.Down(0)
	rig.fake.RateLimit(1)
	_, _ = svc.ScanTONOnce(ctx)
	if got := tonIntentOf(t, st, in.ID); got.Status != "pending" {
		t.Fatalf("%+v", got)
	}
	scanTON(t, svc)
	if got := tonIntentOf(t, st, in.ID); got.Status != "succeeded" {
		t.Fatalf("after the outage: %+v", got)
	}
	rep, _ = svc.GatewayReport(ctx, TONAuto, time.Time{}, time.Time{})
	if rep.Chain.Cursor.LastError != nil || rep.Chain.Cursor.OKAt == nil {
		t.Fatalf("the cursor still shows the error: %+v", rep.Chain.Cursor)
	}
}

// A new address does not report its history; a busy address is read page by page.
func TestTONCursorAndPaging(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	if _, err := st.DB().Exec(ctx, `TRUNCATE payments.ton_cursors, payments.ton_transfers CASCADE`); err != nil {
		t.Fatal(err)
	}
	// Transfers from before the address was switched on.
	for range 3 {
		rig.fake.Add(tonfake.Tx{To: tonStore(), From: tonfake.Customer(), Nano: 9_000_000_000, Comment: "old", Age: 5 * time.Hour})
	}
	o := rig.options("dev")
	o.Envelope = envelope(t, keyA)
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: true, Address: tonStore()}); err != nil {
		t.Fatal(err)
	}
	scanTON(t, svc)
	if rows := tonTransfers(t, st, ""); len(rows) != 0 || len(tonUnmatchedEvents(t, st)) != 0 {
		t.Fatalf("history was reported: %+v", rows)
	}
	// A payment, then 250 other transfers after it: the payment is found three pages down.
	in := startTON(t, svc, "p-1", tonOrder(1))
	rig.fake.Pay(tonStore(), *in.GatewayAmount, *in.PayMemo)
	for range 250 {
		rig.fake.Pay(tonStore(), 1_000, "")
	}
	scanTON(t, svc)
	if got := tonIntentOf(t, st, in.ID); got.Status != "succeeded" {
		t.Fatalf("a payment under 250 other transfers: %+v", got)
	}
	if rig.fake.Calls("getTransactions") < 4 {
		t.Fatalf("paging used %d calls", rig.fake.Calls("getTransactions"))
	}
	cur, _ := st.GetTONCursor(ctx, tonCanon())
	if cur.LastLT != rig.fake.LastLT() {
		t.Fatalf("the cursor is at %d, the chain at %d", cur.LastLT, rig.fake.LastLT())
	}
	// The next read asks once and finds nothing new.
	before := rig.fake.Calls("getTransactions")
	scanTON(t, svc)
	if rig.fake.Calls("getTransactions")-before != 1 {
		t.Fatalf("a quiet read used %d calls", rig.fake.Calls("getTransactions")-before)
	}
	// Switched off and on again: what came meanwhile is not history to report... unless it names a payment.
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	rig.fake.Add(tonfake.Tx{To: tonStore(), From: tonfake.Customer(), Nano: 7_000_000_000, Comment: "while off", Age: 10 * time.Minute})
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	n := len(tonTransfers(t, st, ""))
	scanTON(t, svc)
	if got := len(tonTransfers(t, st, "")); got != n {
		t.Fatalf("the time it was off was reported: %d -> %d", n, got)
	}
}

// The address cannot change under open payments, and the test is read-only.
func TestTONAddressChangeAndTest(t *testing.T) {
	rig := newTONRig(t)
	svc, st, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	in := startTON(t, svc, "a-1", tonOrder(1))
	other := tonaddr.Address{Hash: [32]byte{7}}.Canonical()
	_, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: true, Address: other})
	var open *OpenIntentsError
	if !errors.As(err, &open) || !errors.Is(err, ErrPrecondition) || open.Count != 1 || !strings.Contains(err.Error(), "address") {
		t.Fatalf("address change with an open payment: %v", err)
	}
	// The same address in another form is no change; the key can change.
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: true, Address: tonStore(), APIKey: tonKeyOther}); err != nil {
		t.Fatalf("same address, new key: %v", err)
	}
	// Switching off keeps watching the open payment.
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if offers(svc, TONAuto) {
		t.Fatal("offered while off")
	}
	rig.fake.Pay(tonStore(), *in.GatewayAmount, *in.PayMemo)
	scanTON(t, svc)
	if tonIntentOf(t, st, in.ID).Status != "succeeded" {
		t.Fatal("a switched-off gateway must still settle")
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: TONAuto, Enabled: true, Address: other}); err != nil {
		t.Fatalf("address change once nothing is open: %v", err)
	}

	// The test: the latest transaction's time, nothing created.
	rig.fake.OnlyAPIKey(tonKeyOther)
	r, err := svc.TestGateway(ctx, TONAuto, &GatewayCandidate{Address: tonStore(), APIKey: tonKeyOther})
	if err != nil || !r.OK || r.LatestTxAt <= 0 || time.Since(time.Unix(r.LatestTxAt, 0)) > time.Hour {
		t.Fatalf("test: %+v %v", r, err)
	}
	r, err = svc.TestGateway(ctx, TONAuto, &GatewayCandidate{Address: tonStore(), APIKey: tonKey})
	if err != nil || r.OK || r.Detail == "" || strings.Contains(r.Detail, tonKey) {
		t.Fatalf("a refused key: %+v %v", r, err)
	}
	r, err = svc.TestGateway(ctx, TONAuto, &GatewayCandidate{Address: other, APIKey: tonKeyOther})
	if err != nil || !r.OK || r.LatestTxAt != 0 {
		t.Fatalf("a new wallet has no transactions: %+v %v", r, err)
	}
	for _, bad := range []GatewayCandidate{{Address: "nope"}, {Address: tonStore(), Sandbox: true}} {
		if _, err := svc.TestGateway(ctx, TONAuto, &bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if n := len(tonTransfers(t, st, "")); n != 1 {
		t.Fatalf("a test recorded something: %d transfers", n)
	}
}

// The log, the filters and the report know TON.
func TestTONLogAndReport(t *testing.T) {
	rig := newTONRig(t)
	svc, _, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil {
		t.Fatal(err)
	}
	rig.fake.Pay(tonStore(), 5_000_000_000, "hello")
	scanTON(t, svc)
	list, total, err := svc.ListTONTransfers(ctx, store.ChainTransferFilter{Outcome: "unmatched"}, 10, 0)
	if err != nil || total != 1 || len(list) != 1 || list[0].Network != "TON" || list[0].Comment != "hello" || list[0].Value != 5_000_000_000 || list[0].LT <= 0 {
		t.Fatalf("%+v %d %v", list, total, err)
	}
	for _, bad := range []store.ChainTransferFilter{{Outcome: "paid"}, {TxID: "zz"}, {IntentID: "nope"}} {
		if _, _, err := svc.ListTONTransfers(ctx, bad, 10, 0); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	rep, err := svc.GatewayReport(ctx, TONAuto, time.Time{}, time.Time{})
	if err != nil || rep.Chain == nil || rep.Chain.UnmatchedCount != 1 || len(rep.Chain.Unmatched) != 1 || rep.Chain.Cursor == nil || rep.Chain.Cursor.OKAt == nil {
		t.Fatalf("report: %+v %v", rep, err)
	}
	in := startTON(t, svc, "g-1", tonOrder(1))
	rig.fake.Pay(tonStore(), *in.GatewayAmount, *in.PayMemo)
	scanTON(t, svc)
	rep, _ = svc.GatewayReport(ctx, TONAuto, time.Time{}, time.Time{})
	if len(rep.Totals) != 1 || rep.Totals[0].Status != "succeeded" || rep.LastPaid == nil {
		t.Fatalf("totals: %+v", rep.Totals)
	}
}

// Two prices that disagree, or none, leave the method hidden; one is enough.
func TestTONPriceSourcesDecideTheOffer(t *testing.T) {
	rig := newTONRig(t)
	svc, _, _ := tonService(t, rig)
	ctx := context.Background()
	if err := svc.RefreshTONPrice(ctx); err != nil || !offers(svc, TONAuto) {
		t.Fatalf("%v", err)
	}
	// The sources disagree: no new price; the last one is used until it goes stale.
	rig.fake.SetPrice("okx", "9.0")
	if err := svc.RefreshTONPrice(ctx); err == nil {
		t.Fatal("a price from sources that disagree")
	}
	if !offers(svc, TONAuto) {
		t.Fatal("the last fresh price should still be used")
	}
	old := *svc.tonQ.Load()
	old.At = time.Now().Add(-4 * time.Minute)
	svc.tonQ.Store(&old)
	if offers(svc, TONAuto) {
		t.Fatal("offered on a price older than three minutes")
	}
	rig.fake.SetPrice("okx", "3.2")
	rig.fake.FailPrice("binance", true)
	if err := svc.RefreshTONPrice(ctx); err != nil || !offers(svc, TONAuto) || svc.tonQ.Load().Source != "okx" {
		t.Fatalf("the second source alone: %v", err)
	}
	rig.fake.FailPrice("okx", true)
	if err := svc.RefreshTONPrice(ctx); err == nil {
		t.Fatal("a price from no source")
	}
}
