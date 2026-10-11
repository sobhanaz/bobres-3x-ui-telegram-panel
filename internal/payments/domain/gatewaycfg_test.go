package domain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/zarinpal"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/zpfake"
)

// Test fixtures, not secrets.
const (
	merchantA = "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b"
	merchantB = "9f8e7d6c-5b4a-4321-8fed-cba987654321"
	proxyURL  = "http://relay:pr0xy-pw@203.0.113.10:3128" //nolint:gosec // test fixture
	actor     = "00000000-0000-7000-8000-00000000d0c1"
)

func envelope(t *testing.T, key string) *bcrypto.Envelope {
	t.Helper()
	e, err := bcrypto.NewEnvelope([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

var keyA, keyB = "payments-master-key-A-0123456789abcdef", "payments-master-key-B-0123456789abcdef" //nolint:gosec // test fixtures

// settingsGW is a scriptable gateway that remembers the settings it was built
// with; its external ids start with the merchant id's last 4 characters, so
// a test can tell which merchant created a payment.
type settingsGW struct {
	*fakeGW
	settings ZarinpalSettings
}

func (g *settingsGW) Create(ctx context.Context, c gateway.Charge) (gateway.Started, error) {
	st, err := g.fakeGW.Create(ctx, c)
	if err == nil {
		st.ExternalID = g.settings.MerchantID[len(g.settings.MerchantID)-4:] + "-" + st.ExternalID
	}
	return st, err
}

// builder records every client it builds; with fake set, calls go to the
// Zarinpal fake through the real client instead.
type builder struct {
	mu    sync.Mutex
	built []*settingsGW
	fake  *zpfake.Server
}

func (b *builder) build(z ZarinpalSettings) (gateway.Gateway, error) {
	if b.fake != nil {
		g, err := zarinpal.New(zarinpal.Config{MerchantID: z.MerchantID, Sandbox: z.Sandbox, Proxy: z.Proxy, BaseURL: b.fake.URL})
		if err != nil {
			return nil, err
		}
		return g, nil
	}
	if _, err := zarinpal.New(zarinpal.Config{MerchantID: z.MerchantID, Proxy: z.Proxy}); err != nil {
		return nil, err // the real client's validation
	}
	g := &settingsGW{fakeGW: &fakeGW{name: providerZarinpal}, settings: z}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.built = append(b.built, g)
	return g, nil
}

func (b *builder) last() *settingsGW {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.built) == 0 {
		return nil
	}
	return b.built[len(b.built)-1]
}

func cfgService(t *testing.T) (*Service, *store.Store, *bytes.Buffer) {
	t.Helper()
	svc, st := testService(t)
	if _, err := st.DB().Exec(context.Background(), `TRUNCATE payments.gateway_configs, payments.gateway_events`); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	svc.SetLogger(slog.New(slog.NewTextHandler(&lockedWriter{w: &logs}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return svc, st, &logs
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func zp(svc *Service) GatewayConfig { return svc.GatewayConfigs()[0] }

func offers(svc *Service, name string) bool { return slices.Contains(svc.Gateways(), name) }

func assertNoSecrets(t *testing.T, where, text string) {
	t.Helper()
	for _, secret := range []string{merchantA, merchantB, proxyURL, "pr0xy-pw", "203.0.113.10"} {
		if strings.Contains(text, secret) {
			t.Errorf("%s holds a secret (%q)", where, secret)
		}
	}
}

// The environment seeds the database once; the stored secrets are encrypted
// with the payments master key, and the database wins from then on.
func TestGatewaySettingsSeededOnceAndEncrypted(t *testing.T) {
	svc, st, logs := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	o := GatewayOptions{Env: "prod", Envelope: envelope(t, keyA), Build: b.build,
		Seed: ZarinpalSettings{MerchantID: " " + strings.ToUpper(merchantA) + " ", Proxy: proxyURL}}
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	row, err := st.GetGatewayConfig(ctx, nil, providerZarinpal)
	if err != nil || !row.Enabled || row.Sandbox || row.UpdatedBy != nil {
		t.Fatalf("seeded row: %+v %v", row, err)
	}
	for _, enc := range [][]byte{row.MerchantEnc, row.ProxyEnc} {
		assertNoSecrets(t, "the stored ciphertext", string(enc))
	}
	got, err := decryptSettings(envelope(t, keyA), row)
	if err != nil || got.MerchantID != merchantA || got.Proxy != proxyURL {
		t.Fatalf("decrypted: %v %v", got, err) // %v prints no secret
	}
	c := zp(svc)
	if !c.Enabled || !c.Configured || !c.Offered || c.MerchantHint != "1a2b" || !c.ProxySet || c.KeyMissing || c.FromEnv || c.Problem != "" {
		t.Fatalf("view: %+v", c)
	}
	if !offers(svc, providerZarinpal) || b.last().settings.MerchantID != merchantA {
		t.Fatalf("not offered with the seeded merchant: %v", svc.Gateways())
	}

	// A restart with other values in the environment changes nothing.
	o.Seed = ZarinpalSettings{MerchantID: merchantB}
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if c := zp(svc); c.MerchantHint != "1a2b" || !c.ProxySet {
		t.Fatalf("the environment overwrote the stored settings: %+v", c)
	}
	// Nor does an invalid environment value once settings are stored.
	o.Seed = ZarinpalSettings{MerchantID: "not-a-merchant"}
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatalf("ignored seed refused: %v", err)
	}
	assertNoSecrets(t, "the log", logs.String())
}

// Without a master key the environment's settings are used as they are and
// cannot be changed, as before; nothing is stored.
func TestGatewaySettingsWithoutKeyAreReadOnly(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "prod", Build: b.build, Seed: ZarinpalSettings{MerchantID: merchantA}}); err != nil {
		t.Fatal(err)
	}
	if c := zp(svc); !c.Offered || !c.FromEnv || !c.KeyMissing || c.MerchantHint != "1a2b" || !c.UpdatedAt.IsZero() {
		t.Fatalf("from the environment: %+v", c)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: false}); !errors.Is(err, ErrKeyMissing) {
		t.Fatalf("changed without a key: %v", err)
	}
	if _, err := st.GetGatewayConfig(ctx, nil, providerZarinpal); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stored without a key: %v", err)
	}
	if !offers(svc, providerZarinpal) {
		t.Fatal("a refused change switched Zarinpal off")
	}
	// An invalid environment still stops the service before it serves.
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "prod", Build: b.build, Seed: ZarinpalSettings{MerchantID: "nope"}}); err == nil {
		t.Fatal("invalid merchant id from the environment accepted")
	}
	// Nothing anywhere: Zarinpal is simply not configured.
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "prod", Build: b.build}); err != nil {
		t.Fatal(err)
	}
	if c := zp(svc); c.Configured || c.Enabled || offers(svc, providerZarinpal) {
		t.Fatalf("configured from nothing: %+v", c)
	}
}

// Stored settings need the key they were saved with: with another key, or
// none, Zarinpal is neither offered nor guessed from the environment.
func TestGatewaySettingsWithAnotherKeyAreUnreadable(t *testing.T) {
	svc, _, _ := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyA), Build: b.build,
		Seed: ZarinpalSettings{MerchantID: merchantA, Proxy: proxyURL}}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []GatewayOptions{
		{Env: "dev", Envelope: envelope(t, keyB), Build: b.build, Seed: ZarinpalSettings{MerchantID: merchantB}},
		{Env: "dev", Build: b.build, Seed: ZarinpalSettings{MerchantID: merchantB}},
	} {
		if err := svc.ConfigureGateways(ctx, o); err != nil {
			t.Fatal(err)
		}
		c := zp(svc)
		if c.Problem != ProblemUnreadable || c.Configured || c.Offered || c.MerchantHint != "" || c.FromEnv || offers(svc, providerZarinpal) {
			t.Fatalf("unreadable settings: %+v", c)
		}
	}
	// With the new key, the owner enters the merchant id again.
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyB), Build: b.build}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("kept an unreadable merchant id: %v", err)
	}
	c, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, MerchantID: merchantB})
	if err != nil || !c.Offered || c.Problem != "" || c.ProxySet || c.MerchantHint != "4321" {
		t.Fatalf("re-entered: %+v %v", c, err)
	}
}

// Settings change at runtime: offering follows enabled, and a disabled
// gateway keeps checking the payments it already took.
func TestGatewaySettingsSwapAtRuntime(t *testing.T) {
	svc, st, logs := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyA), Build: b.build}); err != nil {
		t.Fatal(err)
	}
	if offers(svc, providerZarinpal) {
		t.Fatal("offered before it is set up")
	}
	if _, err := svc.StartGateway(ctx, gwIntent("s0", 1_500_000), "Monthly", "https://shop.test/webhooks/zarinpal"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("started without settings: %v", err)
	}
	c, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, MerchantID: merchantA, Proxy: proxyURL, ActorID: actor})
	if err != nil || !c.Offered || c.UpdatedBy != actor || c.UpdatedAt.IsZero() {
		t.Fatalf("switch on: %+v %v", c, err)
	}
	if got := b.last().settings; got.MerchantID != merchantA || got.Proxy != proxyURL {
		t.Fatal("the client was not built with the new settings")
	}
	in, err := svc.StartGateway(ctx, gwIntent("s1", 1_500_000), "Monthly", "https://shop.test/webhooks/zarinpal")
	if err != nil || !strings.HasPrefix(*in.ExternalID, "1a2b-") {
		t.Fatalf("start: %+v %v", in, err)
	}

	// Switched off: no new payments, but the open one is still checked, by the
	// customer's button and by the reconciler, and still settles.
	if c, err = svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: false}); err != nil || c.Offered || !c.Configured || c.MerchantHint != "1a2b" || !c.ProxySet {
		t.Fatalf("switch off: %+v %v", c, err)
	}
	if offers(svc, providerZarinpal) {
		t.Fatal("still offered")
	}
	if _, err := svc.StartGateway(ctx, gwIntent("s2", 1_500_000), "Monthly", "https://shop.test/webhooks/zarinpal"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("started while switched off: %v", err)
	}
	g := b.last()
	if _, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil || g.checks.Load() != 1 {
		t.Fatalf("customer's check while switched off: %v checks=%d", err, g.checks.Load())
	}
	g.set(*in.ExternalID, gateway.Result{State: gateway.Paid, Amount: 1_500_000, Currency: "IRR", Reference: "R1"})
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET checked_at = NULL WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ReconcileOnce(ctx, time.Second); err != nil || n != 1 {
		t.Fatalf("reconciler while switched off: %d %v", n, err)
	}
	if got, _ := st.GetIntent(ctx, nil, in.ID); got.Status != "succeeded" {
		t.Fatalf("an open payment was stranded: %s", got.Status)
	}

	// The proxy can be cleared; switching on again uses the stored merchant.
	if c, err = svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, ClearProxy: true}); err != nil || !c.Offered || c.ProxySet {
		t.Fatalf("clear proxy: %+v %v", c, err)
	}
	if got := b.last().settings; got.MerchantID != merchantA || got.Proxy != "" {
		t.Fatal("cleared proxy still used")
	}
	assertNoSecrets(t, "the log", logs.String())
}

// The merchant id and the sandbox pick where payments are verified: they
// cannot change while a payment is open there.
func TestMerchantChangeRefusedWhileIntentsOpen(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyA), Build: b.build,
		Seed: ZarinpalSettings{MerchantID: merchantA}}); err != nil {
		t.Fatal(err)
	}
	// A payment whose creation failed at the gateway has no authority: it
	// never blocks a change.
	b.last().failErr = errors.New("timeout")
	if _, err := svc.StartGateway(ctx, gwIntent("dead", 1_500_000), "", "https://shop.test/webhooks/zarinpal"); err == nil {
		t.Fatal("outage hidden")
	}
	b.last().failErr = nil
	in, err := svc.StartGateway(ctx, gwIntent("o1", 1_500_000), "", "https://shop.test/webhooks/zarinpal")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := st.GetGatewayConfig(ctx, nil, providerZarinpal)
	for name, u := range map[string]GatewayUpdate{
		"merchant": {Provider: providerZarinpal, Enabled: true, MerchantID: merchantB},
		"sandbox":  {Provider: providerZarinpal, Enabled: true, Sandbox: true},
	} {
		_, err := svc.SetGatewayConfig(ctx, u)
		var open *OpenIntentsError
		if !errors.As(err, &open) || open.Count != 1 || !errors.Is(err, ErrPrecondition) || !strings.Contains(err.Error(), "1 open") {
			t.Fatalf("%s change with an open payment: %v", name, err)
		}
	}
	after, _ := st.GetGatewayConfig(ctx, nil, providerZarinpal)
	if !after.UpdatedAt.Equal(before.UpdatedAt) || zp(svc).MerchantHint != "1a2b" || !strings.HasPrefix(*in.ExternalID, "1a2b-") {
		t.Fatal("a refused change was applied")
	}
	// Switching off, a new proxy or the same merchant id typed again are fine.
	for _, u := range []GatewayUpdate{
		{Provider: providerZarinpal, Enabled: false},
		{Provider: providerZarinpal, Enabled: true, Proxy: proxyURL},
		{Provider: providerZarinpal, Enabled: true, MerchantID: strings.ToUpper(merchantA)},
	} {
		if _, err := svc.SetGatewayConfig(ctx, u); err != nil {
			t.Fatalf("%v: %v", u.Enabled, err)
		}
	}
	// Once the payment is settled the merchant id can change.
	b.last().set(*in.ExternalID, gateway.Result{State: gateway.Paid, Amount: 1_500_000, Currency: "IRR"})
	if _, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil {
		t.Fatal(err)
	}
	if c, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, MerchantID: merchantB}); err != nil || c.MerchantHint != "4321" {
		t.Fatalf("change after the payment settled: %+v %v", c, err)
	}
}

// Open payments pin the merchant id only while something checks them. Once
// the master key is lost nothing can, and entering the merchant id again is
// the only way they are ever checked: it must not be refused because of them.
func TestMerchantReenteredAfterKeyLossWithIntentsOpen(t *testing.T) {
	svc, st, logs := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyA), Build: b.build,
		Seed: ZarinpalSettings{MerchantID: merchantA, Proxy: proxyURL}}); err != nil {
		t.Fatal(err)
	}
	in, err := svc.StartGateway(ctx, gwIntent("lost-key", 1_500_000), "", "https://shop.test/webhooks/zarinpal")
	if err != nil {
		t.Fatal(err)
	}
	// Restored without the key the settings were saved with: nothing checks
	// the payment, neither the reconciler nor the customer's button.
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyB), Build: b.build}); err != nil {
		t.Fatal(err)
	}
	if c := zp(svc); c.Problem != ProblemUnreadable || c.Configured {
		t.Fatalf("after the key changed: %+v", c)
	}
	if n, err := svc.ReconcileOnce(ctx, time.Second); err != nil || n != 0 {
		t.Fatalf("reconciled without a gateway: %d %v", n, err)
	}
	if got, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil || got.Status != "pending" {
		t.Fatalf("checked without a gateway: %+v %v", got, err)
	}
	// The owner types the same merchant id again: the payment is checked
	// again and settles.
	c, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, MerchantID: merchantA, ActorID: actor})
	if err != nil || !c.Offered || !c.Configured || c.Problem != "" || c.MerchantHint != "1a2b" || c.ProxySet {
		t.Fatalf("merchant id re-entered with a payment open: %+v %v", c, err)
	}
	b.last().set(*in.ExternalID, gateway.Result{State: gateway.Paid, Amount: 1_500_000, Currency: "IRR", Reference: "R7"})
	if got, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil || got.Status != "succeeded" {
		t.Fatalf("the payment once the merchant id is back: %+v %v", got, err)
	}

	// Another open payment, the key lost again, and another merchant id this
	// time: accepted too. It cannot settle the payment, which stays open
	// until its checks close it.
	in2, err := svc.StartGateway(ctx, gwIntent("lost-key-2", 1_500_000), "", "https://shop.test/webhooks/zarinpal")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyA), Build: b.build}); err != nil {
		t.Fatal(err)
	}
	if c, err = svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, MerchantID: merchantB}); err != nil || !c.Offered || c.MerchantHint != "4321" {
		t.Fatalf("another merchant id after the key changed: %+v %v", c, err)
	}
	if got, _ := st.GetIntent(ctx, nil, in2.ID); got.Status != "pending" {
		t.Fatalf("saving the settings touched the open payment: %s", got.Status)
	}
	// Checked again, the payment pins the merchant id once more.
	_, err = svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, MerchantID: merchantA})
	var open *OpenIntentsError
	if !errors.As(err, &open) || open.Count != 1 {
		t.Fatalf("merchant change with a checked payment open: %v", err)
	}
	assertNoSecrets(t, "the log", logs.String())
}

// The same for a sandbox stored in dev once the install is prod: the sandbox
// payments are never checked there, and switching the sandbox off must not
// be refused because of them.
func TestSandboxSwitchedOffInProdWithIntentsOpen(t *testing.T) {
	svc, _, _ := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	env := envelope(t, keyA)
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: env, Build: b.build}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, Sandbox: true, MerchantID: merchantA}); err != nil {
		t.Fatal(err)
	}
	in, err := svc.StartGateway(ctx, gwIntent("sandbox", 1_500_000), "", "https://shop.test/webhooks/zarinpal")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "prod", Envelope: env, Build: b.build}); err != nil {
		t.Fatal(err)
	}
	if c := zp(svc); c.Problem != ProblemSandboxRefused || c.Configured {
		t.Fatalf("sandbox in prod: %+v", c)
	}
	sandboxGW := b.last()
	if got, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil || got.Status != "pending" || sandboxGW.checks.Load() != 0 {
		t.Fatalf("a sandbox payment checked in prod: %+v %v", got, err)
	}
	// Still refused: the sandbox itself.
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, Sandbox: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sandbox kept in prod: %v", err)
	}
	c, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true})
	if err != nil || !c.Offered || c.Sandbox || c.Problem != "" || c.MerchantHint != "1a2b" {
		t.Fatalf("sandbox switched off with a sandbox payment open: %+v %v", c, err)
	}
	if g := b.last(); g.settings.Sandbox || g == sandboxGW {
		t.Fatal("not rebuilt without the sandbox")
	}
}

// prod never uses the sandbox: not when saving, not from stored settings
// (not even to check payments: a sandbox "payment" would give service for
// free), not from the environment.
func TestProdRefusesSandboxAtRuntime(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	env := envelope(t, keyA)
	o := GatewayOptions{Env: "prod", Envelope: env, Build: b.build}
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, Sandbox: true, MerchantID: merchantA}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sandbox saved in prod: %v", err)
	}
	if _, err := svc.TestGateway(ctx, providerZarinpal, &GatewayCandidate{Sandbox: true, MerchantID: merchantA}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sandbox tested in prod: %v", err)
	}
	// Settings saved in dev, then the install switched to prod.
	row, err := encryptSettings(env, ZarinpalSettings{MerchantID: merchantA, Sandbox: true})
	if err != nil {
		t.Fatal(err)
	}
	row.Enabled = true
	if _, err := st.SaveGatewayConfig(ctx, nil, row); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if c := zp(svc); c.Problem != ProblemSandboxRefused || c.Configured || c.Offered || svc.Gateway(providerZarinpal) != nil {
		t.Fatalf("sandbox used in prod: %+v", c)
	}
	if _, err := st.DB().Exec(ctx, `TRUNCATE payments.gateway_configs`); err != nil {
		t.Fatal(err)
	}
	o.Seed = ZarinpalSettings{MerchantID: merchantA, Sandbox: true}
	if err := svc.ConfigureGateways(ctx, o); err == nil {
		t.Fatal("sandbox seeded in prod")
	}
}

// Switching on needs a merchant id and a public URL Zarinpal can send the
// customer back to; switching off never does.
func TestSwitchingOnNeedsMerchantAndPublicURL(t *testing.T) {
	svc, _, _ := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	o := GatewayOptions{Env: "prod", Envelope: envelope(t, keyA), Build: b.build, PublicURLErr: errors.New("BOBRES_ZARINPAL_PUBLIC_URL must be https")}
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("switched on without a merchant id: %v", err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, MerchantID: merchantA}); !errors.Is(err, ErrPrecondition) || !strings.Contains(err.Error(), "PUBLIC_URL") {
		t.Fatalf("switched on without a public URL: %v", err)
	}
	c, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, MerchantID: merchantA})
	if err != nil || c.Enabled || !c.Configured || c.Offered {
		t.Fatalf("saved switched off: %+v %v", c, err)
	}
	// Stored as on, but the public URL broke since: still checking, not offered.
	o.PublicURLErr = nil
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	o.PublicURLErr = errors.New("broken")
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if c := zp(svc); c.Problem != ProblemPublicURL || !c.Configured || c.Offered || !c.Enabled || offers(svc, providerZarinpal) {
		t.Fatalf("broken public URL: %+v", c)
	}
}

func TestSetGatewayConfigValidates(t *testing.T) {
	svc, st, logs := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyA), Build: b.build,
		Seed: ZarinpalSettings{MerchantID: merchantA, Proxy: proxyURL}}); err != nil {
		t.Fatal(err)
	}
	before, _ := st.GetGatewayConfig(ctx, nil, providerZarinpal)
	for name, u := range map[string]GatewayUpdate{
		"unknown gateway":   {Provider: "paypal", Enabled: true},
		"actor not an id":   {Provider: providerZarinpal, Enabled: true, ActorID: "admin'; --"},
		"proxy and clear":   {Provider: providerZarinpal, Enabled: true, Proxy: proxyURL, ClearProxy: true},
		"merchant format":   {Provider: providerZarinpal, Enabled: true, MerchantID: "12345"},
		"socks proxy":       {Provider: providerZarinpal, Enabled: true, Proxy: "socks5://relay:pr0xy-pw@203.0.113.10:1080"},
		"proxy not a URL":   {Provider: providerZarinpal, Enabled: true, Proxy: "pr0xy-pw@203.0.113.10"},
		"merchant whitespa": {Provider: providerZarinpal, Enabled: true, MerchantID: merchantB + " x"},
	} {
		_, err := svc.SetGatewayConfig(ctx, u)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		assertNoSecrets(t, name+" error", err.Error())
		if strings.Contains(err.Error(), "203.0.113.10") || strings.Contains(err.Error(), "pr0xy-pw") {
			t.Errorf("%s: the error echoes the proxy: %v", name, err)
		}
	}
	after, _ := st.GetGatewayConfig(ctx, nil, providerZarinpal)
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatal("a refused change was stored")
	}
	assertNoSecrets(t, "the log", logs.String())
}

// The swap is safe under -race: payments start, checks run, the list is read
// and the reconciler runs while the settings keep changing. No payment ever
// starts with a merchant id that is being replaced: every open payment was
// created by the merchant id stored at the end.
func TestGatewaySwapIsRaceFree(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	b := &builder{}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyA), Build: b.build,
		Seed: ZarinpalSettings{MerchantID: merchantA}}); err != nil {
		t.Fatal(err)
	}
	var (
		wg      sync.WaitGroup
		stop    atomic.Bool
		started atomic.Int32
	)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; !stop.Load(); i++ {
				in, err := svc.StartGateway(ctx, gwIntent(fmt.Sprintf("race-%d-%d", w, i), 1_500_000), "", "https://shop.test/webhooks/zarinpal")
				if err == nil {
					started.Add(1)
					_, _ = svc.CheckIntent(ctx, uid, in.ID, "check")
				}
				_ = svc.Gateways()
				_ = svc.GatewayConfigs()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_, _ = svc.ReconcileOnce(ctx, time.Millisecond)
		}
	}()
	merchants := []string{merchantB, merchantA}
	for i := 0; i < 40; i++ {
		_, _ = svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: i%3 != 0, MerchantID: merchants[i%2], Proxy: proxyURL})
		if i == 31 {
			for started.Load() == 0 {
				time.Sleep(time.Millisecond) // make sure payments are open before the last changes
			}
		}
	}
	stop.Store(true)
	wg.Wait()
	if started.Load() == 0 {
		t.Fatal("no payment started")
	}
	hint := zp(svc).MerchantHint
	rows, err := st.DB().Query(ctx, `SELECT external_id FROM payments.payment_intents
		WHERE provider = 'zarinpal' AND status IN ('pending','confirming') AND external_id IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var ext string
		if err := rows.Scan(&ext); err != nil {
			t.Fatal(err)
		}
		n++
		if !strings.HasPrefix(ext, hint+"-") {
			t.Fatalf("payment %s is open under another merchant id than the stored one (%s)", ext, hint)
		}
	}
	if n == 0 {
		t.Fatal("no open payment to compare")
	}
}

// The reconciler runs without any gateway and picks up one configured later.
func TestReconcilerRunsWithoutGateway(t *testing.T) {
	svc, _, _ := cfgService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &builder{}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "dev", Envelope: envelope(t, keyA), Build: b.build}); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.ReconcileOnce(ctx, time.Second); err != nil || n != 0 {
		t.Fatalf("no gateway: %d %v", n, err)
	}
	done := make(chan error, 1)
	go func() { done <- svc.RunReconciler(ctx, 20*time.Millisecond) }()
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, Enabled: true, MerchantID: merchantA}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartGateway(ctx, gwIntent("late", 1_500_000), "", "https://shop.test/webhooks/zarinpal"); err != nil {
		t.Fatal(err)
	}
	g := b.last()
	deadline := time.Now().Add(10 * time.Second)
	for g.checks.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the reconciler never checked the payment")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the reconciler did not stop")
	}
}

// The connection test is read-only: through the client in use (proxy
// included) or a candidate built the same way, it asks Zarinpal's unVerified
// list and never requests or verifies a payment.
func TestGatewayTestIsReadOnly(t *testing.T) {
	svc, st, logs := cfgService(t)
	ctx := context.Background()
	fake := zpfake.New()
	t.Cleanup(fake.Close)
	fake.OnlyMerchant(merchantA)
	b := &builder{fake: fake}
	if err := svc.ConfigureGateways(ctx, GatewayOptions{Env: "prod", Envelope: envelope(t, keyA), Build: b.build}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TestGateway(ctx, providerZarinpal, nil); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("tested without settings: %v", err)
	}
	if _, err := svc.TestGateway(ctx, "paypal", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown gateway: %v", err)
	}
	if _, err := svc.TestGateway(ctx, providerZarinpal, &GatewayCandidate{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("candidate without a merchant id: %v", err)
	}
	r, err := svc.TestGateway(ctx, providerZarinpal, &GatewayCandidate{MerchantID: merchantA})
	if err != nil || !r.OK || r.Detail != "" || r.Unverified != 0 {
		t.Fatalf("good candidate: %+v %v", r, err)
	}
	if _, err := st.GetGatewayConfig(ctx, nil, providerZarinpal); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("a test stored the candidate")
	}
	r, err = svc.TestGateway(ctx, providerZarinpal, &GatewayCandidate{MerchantID: merchantB})
	if err != nil || r.OK || !strings.Contains(r.Detail, "-10") {
		t.Fatalf("refused merchant: %+v %v", r, err)
	}
	assertNoSecrets(t, "the test detail", r.Detail)

	// The stored settings are tested through the very client in use.
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, MerchantID: merchantA}); err != nil {
		t.Fatal(err)
	}
	fake.AddPayment(1_500_000, "PAID")
	if r, err := svc.TestGateway(ctx, providerZarinpal, nil); err != nil || !r.OK || r.Unverified != 1 {
		t.Fatalf("stored settings: %+v %v", r, err)
	}
	// A candidate keeps the stored merchant id and tries a proxy that is down.
	r, err = svc.TestGateway(ctx, providerZarinpal, &GatewayCandidate{Proxy: "http://relay:pr0xy-pw@127.0.0.1:1"}) //nolint:gosec // test fixture
	if err != nil || r.OK || !strings.Contains(r.Detail, "proxy") {
		t.Fatalf("proxy down: %+v %v", r, err)
	}
	for _, leak := range []string{"pr0xy-pw", "127.0.0.1", "relay"} {
		if strings.Contains(r.Detail, leak) {
			t.Fatalf("the detail leaks %q: %s", leak, r.Detail)
		}
	}
	if fake.Calls("request") != 0 || fake.Calls("verify") != 0 || fake.Calls("inquiry") != 0 || fake.Calls("unverified") != 3 {
		t.Fatalf("calls: request=%d verify=%d inquiry=%d unverified=%d", fake.Calls("request"), fake.Calls("verify"),
			fake.Calls("inquiry"), fake.Calls("unverified"))
	}
	if !strings.Contains(logs.String(), "gateway test failed") {
		t.Fatal("failed tests are not logged")
	}
	for _, leak := range []string{"pr0xy-pw", "127.0.0.1:1"} {
		if strings.Contains(logs.String(), leak) {
			t.Fatalf("the log leaks %q", leak)
		}
	}
	assertNoSecrets(t, "the log", logs.String())
}

func TestGatewayReportValidates(t *testing.T) {
	svc, _, _ := cfgService(t)
	ctx := context.Background()
	now := time.Now()
	for name, c := range map[string]struct {
		provider string
		from, to time.Time
	}{
		"unknown provider": {"paypal", time.Time{}, time.Time{}},
		"wallet":           {"wallet", time.Time{}, time.Time{}},
		"backwards":        {providerZarinpal, now, now.Add(-time.Hour)},
		"empty":            {providerZarinpal, now, now},
		"too long":         {providerZarinpal, now.Add(-400 * 24 * time.Hour), now},
	} {
		if _, err := svc.GatewayReport(ctx, c.provider, c.from, c.to); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	r, err := svc.GatewayReport(ctx, providerZarinpal, time.Time{}, time.Time{})
	if err != nil || r.To.Sub(r.From) != 30*24*time.Hour || time.Since(r.To) > time.Minute {
		t.Fatalf("default period: %+v %v", r, err)
	}
	if _, err := svc.GatewayReport(ctx, Stars, now.Add(-7*24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
}
