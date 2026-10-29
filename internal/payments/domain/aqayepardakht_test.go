package domain

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/aqayepardakhtfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/aqayepardakht"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

// Test fixtures, not secrets. A PIN is case-sensitive, so these have both cases.
const (
	aqPINA     = "Gm7QaV3xTz9LkR2WnD4P"
	aqPINB     = "Zk3PqW8mYb5NvC1XhT6R"
	aqCallback = "https://shop.example.test/webhooks/aqayepardakht"
)

// aqRig wires Aqayepardakht to its fake beside Zarinpal.
type aqRig struct{ fake *aqayepardakhtfake.Server }

func newAqRig(t *testing.T) *aqRig {
	t.Helper()
	r := &aqRig{fake: aqayepardakhtfake.New()}
	t.Cleanup(r.fake.Close)
	return r
}

func (r *aqRig) options(env string) GatewayOptions {
	return GatewayOptions{
		Env: env, Envelope: nil, Build: (&builder{}).build,
		Keyed: map[string]KeyedOptions{
			providerAqayepardakht: {Build: func(k KeySettings) (gateway.Gateway, error) {
				g, err := aqayepardakht.New(aqayepardakht.Config{PIN: k.APIKey, Sandbox: k.Sandbox, Proxy: k.Proxy, BaseURL: r.fake.URL})
				if err != nil {
					return nil, err
				}
				return g, nil
			}},
		},
	}
}

func (r *aqRig) configure(t *testing.T, svc *Service, env string) {
	t.Helper()
	o := r.options(env)
	o.Envelope = envelope(t, keyA)
	if err := svc.ConfigureGateways(context.Background(), o); err != nil {
		t.Fatal(err)
	}
}

func aqIntent(key string, rial int64) *store.Intent {
	return keyedIntent(providerAqayepardakht, key, rial)
}

// aqOn switches Aqayepardakht on with a PIN.
func aqOn(t *testing.T, svc *Service, pin string) GatewayConfig {
	t.Helper()
	got, err := svc.SetGatewayConfig(context.Background(), GatewayUpdate{Provider: providerAqayepardakht, Enabled: true, APIKey: pin, ActorID: actor})
	if err != nil {
		t.Fatalf("switch on: %v", err)
	}
	return got
}

func assertNoPINs(t *testing.T, where, text string) {
	t.Helper()
	for _, p := range []string{aqPINA, aqPINB} {
		if strings.Contains(text, p) {
			t.Errorf("%s holds a PIN (%q)", where, p)
		}
	}
}

// The settings follow the other API-key gateways' rules: off until a PIN is set, stored encrypted and never
// shown, listed after IDPay and NextPay; and the PIN keeps its case.
func TestAqayepardakhtSettings(t *testing.T) {
	svc, st, logs := cfgService(t)
	ctx := context.Background()
	rig := newAqRig(t)
	rig.configure(t, svc, "prod")
	cfgs := svc.GatewayConfigs()
	if len(cfgs) != 3 || cfgs[0].Provider != providerZarinpal || cfgs[1].Provider != USDTAuto || cfgs[2].Provider != providerAqayepardakht {
		t.Fatalf("configs: %+v", cfgs)
	}
	if got := keyedConfig(svc, providerAqayepardakht); got.Enabled || got.Configured || got.Offered || got.APIKeySet || got.Sandbox {
		t.Fatalf("on without a PIN: %+v", got)
	}
	for name, u := range map[string]GatewayUpdate{
		"switched on without a PIN": {Enabled: true},
		"a PIN that is too short":   {APIKey: "abc"},
		"a PIN with a space":        {APIKey: "has space in it"},
		"a merchant id":             {MerchantID: merchantA},
		"an address":                {Address: "TS3xQSYkuSNpuUzmCprGpTHHKq9XVTgu3r"},
		"a sandbox switch":          {APIKey: aqPINA, Sandbox: true},
		"a PIN and a clear":         {APIKey: aqPINA, ClearAPIKey: true},
	} {
		u.Provider = providerAqayepardakht
		if _, err := svc.SetGatewayConfig(ctx, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	got := aqOn(t, svc, " "+aqPINA+" ")
	if !got.Enabled || !got.Configured || !got.Offered || !got.APIKeySet || got.Sandbox || got.MerchantHint != "" || got.UpdatedBy != actor {
		t.Fatalf("after switching on: %+v", got)
	}
	if !offers(svc, providerAqayepardakht) {
		t.Fatal("not offered once on")
	}
	row, err := st.GetGatewayConfig(ctx, nil, providerAqayepardakht)
	if err != nil || len(row.APIKeyEnc) == 0 || strings.Contains(string(row.APIKeyEnc), aqPINA) || row.Sandbox {
		t.Fatalf("stored row: %+v %v", row, err)
	}
	if dec, err := decryptKeyed(envelope(t, keyA), row); err != nil || dec.APIKey != aqPINA {
		t.Fatalf("round trip, the case must stay: %+v %v", dec, err)
	}
	assertNoPINs(t, "the log", logs.String())
	// Zarinpal's settings changing does not drop it.
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, MerchantID: merchantA}); err != nil {
		t.Fatal(err)
	}
	if !offers(svc, providerAqayepardakht) || svc.Gateway(providerAqayepardakht) == nil {
		t.Fatal("a Zarinpal change dropped the gateway")
	}
	// Switching off stops offering; the client stays for open payments.
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerAqayepardakht}); err != nil {
		t.Fatal(err)
	}
	if offers(svc, providerAqayepardakht) || svc.Gateway(providerAqayepardakht) == nil {
		t.Fatal("switching off must keep the client but stop offering")
	}
	// A restart reads the stored settings back, and another master key cannot read them.
	svc2, _ := testService(t)
	rig.configure(t, svc2, "prod")
	if got := keyedConfig(svc2, providerAqayepardakht); !got.APIKeySet || got.Enabled || !got.Configured {
		t.Fatalf("after a restart: %+v", got)
	}
	svc3, _ := testService(t)
	o := rig.options("prod")
	o.Envelope = envelope(t, keyB)
	if err := svc3.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	if got := keyedConfig(svc3, providerAqayepardakht); got.Problem != ProblemUnreadable || got.Offered || got.Configured {
		t.Fatalf("unreadable settings: %+v", got)
	}
	if _, err := svc3.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerAqayepardakht, Enabled: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("re-enabled without entering the PIN: %v", err)
	}
	if got, err := svc3.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerAqayepardakht, Enabled: true, APIKey: aqPINA}); err != nil || !got.Offered {
		t.Fatalf("PIN entered again: %+v %v", got, err)
	}
}

// The sandbox is the PIN "sandbox": the flag follows it, and production refuses it everywhere.
func TestAqayepardakhtSandboxIsThePIN(t *testing.T) {
	ctx := context.Background()
	t.Run("dev", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newAqRig(t)
		rig.configure(t, svc, "dev")
		got := aqOn(t, svc, "sandbox")
		if !got.Sandbox || !got.Offered {
			t.Fatalf("sandbox PIN in dev: %+v", got)
		}
		in, err := svc.StartGateway(ctx, aqIntent("sb-1", 1_000_000), "plan", aqCallback)
		if err != nil || in.PayURL == nil || !strings.Contains(*in.PayURL, "/startpay/sandbox/") {
			t.Fatalf("sandbox pay URL: %+v %v", in, err)
		}
		// A real PIN switches the flag off (nothing is open on the sandbox PIN once it settles or expires).
		if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerAqayepardakht, Enabled: true, APIKey: aqPINA}); err == nil {
			t.Fatal("the PIN changed under an open sandbox payment")
		}
	})
	t.Run("prod refuses it", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newAqRig(t)
		rig.configure(t, svc, "prod")
		if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerAqayepardakht, Enabled: true, APIKey: "sandbox"}); !errors.Is(err, ErrInvalid) ||
			!strings.Contains(err.Error(), "BOBRES_ENV=prod") {
			t.Fatalf("prod sandbox PIN: %v", err)
		}
		if _, err := svc.TestGateway(ctx, providerAqayepardakht, &GatewayCandidate{APIKey: "sandbox"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("prod sandbox PIN test: %v", err)
		}
		if got := keyedConfig(svc, providerAqayepardakht); got.APIKeySet || got.Offered {
			t.Fatalf("a refused PIN was kept: %+v", got)
		}
	})
	t.Run("stored in prod, never used", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newAqRig(t)
		rig.configure(t, svc, "dev")
		aqOn(t, svc, "sandbox")
		svc2, _ := testService(t)
		rig.configure(t, svc2, "prod")
		got := keyedConfig(svc2, providerAqayepardakht)
		if got.Problem != ProblemSandboxRefused || got.Offered || got.Configured || svc2.Gateway(providerAqayepardakht) != nil {
			t.Fatalf("stored sandbox PIN in prod: %+v", got)
		}
	})
	t.Run("the environment's sandbox PIN stops a prod start", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newAqRig(t)
		o := rig.options("prod")
		o.Envelope = envelope(t, keyA)
		ks := o.Keyed[providerAqayepardakht]
		ks.Seed = KeySettings{APIKey: "sandbox"}
		o.Keyed[providerAqayepardakht] = ks
		if err := svc.ConfigureGateways(ctx, o); err == nil {
			t.Fatal("a sandbox PIN from the environment was accepted in prod")
		}
	})
}

// A payment from its creation to its settlement: the gateway is the only source of truth, and one payment
// credits once.
func TestAqayepardakhtPaymentFlow(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	rig := newAqRig(t)
	rig.configure(t, svc, "dev")
	aqOn(t, svc, aqPINA)

	in, err := svc.StartGateway(ctx, aqIntent("af-1", 1_500_000), "plan", aqCallback)
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != "pending" || in.ExternalID == nil || in.PayURL == nil || !strings.HasSuffix(*in.PayURL, "/startpay/"+*in.ExternalID) || in.Provider != providerAqayepardakht {
		t.Fatalf("created: %+v", in)
	}
	if again, err := svc.StartGateway(ctx, aqIntent("af-1", 1_500_000), "plan", aqCallback); err != nil || again.ID != in.ID || *again.ExternalID != *in.ExternalID {
		t.Fatalf("retry: %+v %v", again, err)
	}
	if rig.fake.Calls("create") != 1 {
		t.Fatalf("a retried start created %d payments", rig.fake.Calls("create"))
	}
	// The customer comes back before paying: Aqayepardakht has one "not completed" answer for a payment not
	// paid yet and for a cancelled one, and a customer who came back is done with the bank page, so it fails
	// (nothing is credited). A verify that later finds it paid reopens it, as for every gateway.
	if got, err := svc.CheckAfterReturn(ctx, in.ID); err != nil || got.Status != "failed" || ledgerCount(t, st) != 0 {
		t.Fatalf("unpaid return: %+v %v", got, err)
	}
	if _, _, err := rig.fake.Pay(*in.ExternalID, true); err != nil {
		t.Fatal(err)
	}
	// (A closed payment is asked about at most every few seconds, whoever asks.)
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET checked_at = NULL WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	got, err := svc.CheckAfterReturn(ctx, in.ID)
	if err != nil || got.Status != "succeeded" {
		t.Fatalf("paid return after an unpaid one reopens it: %+v %v", got, err)
	}
	if n := ledgerCount(t, st); n != 1 {
		t.Fatalf("ledger: %d entries, want 1", n)
	}
	ev := outbox(t, st)
	if len(ev[events.PaymentsPaymentSucceeded]) != 1 || ev[events.PaymentsPaymentSucceeded][0].Amount != 150_000 || ev[events.PaymentsPaymentSucceeded][0].Currency != "IRT" {
		t.Fatalf("event: %+v", ev)
	}
	calls := rig.fake.Calls("verify")
	// The same callback again, and the reconciler: nothing changes and nothing is asked again.
	for range 2 {
		if again, err := svc.CheckAfterReturn(ctx, in.ID); err != nil || again.Status != "succeeded" {
			t.Fatalf("second callback: %+v %v", again, err)
		}
	}
	if _, err := svc.ReconcileOnce(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if n := ledgerCount(t, st); n != 1 {
		t.Fatalf("double callback credited %d times", n)
	}
	if rig.fake.Calls("verify") != calls {
		t.Fatalf("a settled payment was verified again (%d -> %d)", calls, rig.fake.Calls("verify"))
	}

	// The customer abandons another payment at the bank.
	bad, err := svc.StartGateway(ctx, aqIntent("af-2", 1_500_000), "plan", aqCallback)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = rig.fake.Pay(*bad.ExternalID, false)
	if got, err := svc.CheckAfterReturn(ctx, bad.ID); err != nil || got.Status != "failed" {
		t.Fatalf("failed return: %+v %v", got, err)
	}
	if n := ledgerCount(t, st); n != 1 {
		t.Fatalf("a failed payment credited: %d entries", n)
	}
}

// A callback that arrives while nothing was paid yet does not close a payment the customer can still pay.
func TestAqayepardakhtOpenPaymentStaysOpenForTheReconciler(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	rig := newAqRig(t)
	rig.configure(t, svc, "dev")
	aqOn(t, svc, aqPINA)
	in, err := svc.StartGateway(ctx, aqIntent("ao-1", 1_500_000), "plan", aqCallback)
	if err != nil {
		t.Fatal(err)
	}
	// The reconciler asks while the customer is at the bank: not paid, still open.
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET checked_at = NULL WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReconcileOnce(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetIntent(ctx, nil, in.ID); got.Status != "pending" {
		t.Fatalf("the reconciler closed a payment still within its window: %+v", got)
	}
	// The customer pays and never comes back (a lost callback): the reconciler verifies it.
	_, _, _ = rig.fake.Pay(*in.ExternalID, true)
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET checked_at = NULL WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReconcileOnce(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetIntent(ctx, nil, in.ID); got.Status != "succeeded" || ledgerCount(t, st) != 1 {
		t.Fatalf("a lost callback was not recovered: %+v ledger=%d", got, ledgerCount(t, st))
	}
}

// Callback, return page and reconciler can race: one payment credits once, and a verify whose own answer was
// lost (the gateway then says "verified before") still settles it, once.
func TestAqayepardakhtSettlesOnceUnderRaceAndLostAnswers(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	rig := newAqRig(t)
	rig.configure(t, svc, "dev")
	aqOn(t, svc, aqPINA)
	in, err := svc.StartGateway(ctx, aqIntent("ar-1", 1_500_000), "plan", aqCallback)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = rig.fake.Pay(*in.ExternalID, true)
	// A verify that succeeded at the gateway, whose answer never reached us: the next one says "verified
	// before". (The real client is built from the same fake.)
	lost, err := aqayepardakht.New(aqayepardakht.Config{PIN: aqPINA, BaseURL: rig.fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	c := gateway.Charge{IntentID: in.ID, Amount: 1_500_000, Currency: "IRR", Returned: true}
	if r, err := lost.Check(ctx, *in.ExternalID, c); err != nil || r.State != gateway.Paid || !rig.fake.Verified(*in.ExternalID) {
		t.Fatalf("the verify that got lost: %+v %v", r, err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := svc.CheckAfterReturn(ctx, in.ID); err != nil {
				t.Errorf("check: %v", err)
			}
		})
		wg.Go(func() {
			if _, err := svc.CheckIntent(ctx, uid, in.ID, "check"); err != nil {
				t.Errorf("check: %v", err)
			}
		})
	}
	wg.Wait()
	got, _ := st.GetIntent(ctx, nil, in.ID)
	ev := outbox(t, st)
	if got.Status != "succeeded" || len(ev[events.PaymentsPaymentSucceeded]) != 1 || ledgerCount(t, st) != 1 {
		t.Fatalf("settled more than once, or not at all: %+v events=%d ledger=%d", got, len(ev[events.PaymentsPaymentSucceeded]), ledgerCount(t, st))
	}
}

// An amount that differs from the payment's is never settled: the gateway refuses the verify (error 10), the
// payment stays open for a human, and nothing is credited.
func TestAqayepardakhtAmountMismatchSettlesNothing(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	rig := newAqRig(t)
	rig.configure(t, svc, "dev")
	aqOn(t, svc, aqPINA)
	in, err := svc.StartGateway(ctx, aqIntent("am-1", 1_500_000), "plan", aqCallback)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = rig.fake.Pay(*in.ExternalID, true)
	rig.fake.SetToman(*in.ExternalID, 100_000) // the bank holds another amount
	got, err := svc.CheckAfterReturn(ctx, in.ID)
	if err != nil || got.Status != "pending" {
		t.Fatalf("mismatch: %+v %v", got, err)
	}
	if n := ledgerCount(t, st); n != 0 || rig.fake.Verified(*in.ExternalID) {
		t.Fatalf("an amount that differs credited %d entries (verified=%v)", n, rig.fake.Verified(*in.ExternalID))
	}
	if checkErrors(t, st, in.ID) != 1 {
		t.Fatal("the refusal is not kept in the gateway trail")
	}
}

// A provider that is down fails the start cleanly, and a check that cannot reach it settles nothing and keeps
// the payment open for the next one.
func TestAqayepardakhtOutage(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	rig := newAqRig(t)
	rig.configure(t, svc, "dev")
	aqOn(t, svc, aqPINA)
	rig.fake.Down(1)
	if _, err := svc.StartGateway(ctx, aqIntent("ou-1", 1_500_000), "plan", aqCallback); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("start while down: %v", err)
	}
	in, err := svc.StartGateway(ctx, aqIntent("ou-1", 1_500_000), "plan", aqCallback)
	if err != nil {
		t.Fatalf("retry after the outage: %v", err)
	}
	_, _, _ = rig.fake.Pay(*in.ExternalID, true)
	rig.fake.Down(1000)
	got, _ := svc.CheckAfterReturn(ctx, in.ID)
	if got != nil && got.Status != "pending" {
		t.Fatalf("a check that failed settled the payment: %+v", got)
	}
	if n := ledgerCount(t, st); n != 0 {
		t.Fatalf("credited during the outage: %d", n)
	}
	rig.fake.Down(0)
	if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET checked_at = NULL WHERE id = $1`, in.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.CheckAfterReturn(ctx, in.ID); err != nil || got.Status != "succeeded" {
		t.Fatalf("after the outage: %+v %v", got, err)
	}
}

// The PIN that open payments were created with cannot change under them.
func TestAqayepardakhtPINChangeRefusedWhileIntentsOpen(t *testing.T) {
	svc, _, _ := cfgService(t)
	ctx := context.Background()
	rig := newAqRig(t)
	rig.configure(t, svc, "dev")
	aqOn(t, svc, aqPINA)
	in, err := svc.StartGateway(ctx, aqIntent("pc-1", 1_500_000), "plan", aqCallback)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerAqayepardakht, Enabled: true, APIKey: aqPINB})
	var open *OpenIntentsError
	if !errors.As(err, &open) || !errors.Is(err, ErrPrecondition) || open.Count != 1 || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("PIN change with an open payment: %v", err)
	}
	// Settings that are not the PIN still change, and a switched-off gateway still settles.
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerAqayepardakht, Enabled: false}); err != nil {
		t.Fatalf("switching off: %v", err)
	}
	_, _, _ = rig.fake.Pay(*in.ExternalID, true)
	if got, err := svc.CheckAfterReturn(ctx, in.ID); err != nil || got.Status != "succeeded" {
		t.Fatalf("a switched-off gateway must still settle: %+v %v", got, err)
	}
	if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerAqayepardakht, Enabled: true, APIKey: aqPINB}); err != nil {
		t.Fatalf("PIN change once nothing is open: %v", err)
	}
}

// The connection test is read-only and tells a refused PIN from a good one, without repeating a PIN.
func TestAqayepardakhtTestIsReadOnly(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	rig := newAqRig(t)
	rig.fake.OnlyPIN(aqPINA)
	rig.configure(t, svc, "dev")
	if _, err := svc.TestGateway(ctx, providerAqayepardakht, nil); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("tested without settings: %v", err)
	}
	if _, err := svc.TestGateway(ctx, providerAqayepardakht, &GatewayCandidate{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("candidate without a PIN: %v", err)
	}
	if _, err := svc.TestGateway(ctx, providerAqayepardakht, &GatewayCandidate{APIKey: aqPINA, Sandbox: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("candidate with a sandbox switch: %v", err)
	}
	if r, err := svc.TestGateway(ctx, providerAqayepardakht, &GatewayCandidate{APIKey: aqPINA}); err != nil || !r.OK {
		t.Fatalf("good PIN: %+v %v", r, err)
	}
	r, err := svc.TestGateway(ctx, providerAqayepardakht, &GatewayCandidate{APIKey: aqPINB})
	if err != nil || r.OK || r.Detail == "" {
		t.Fatalf("refused PIN: %+v %v", r, err)
	}
	assertNoPINs(t, "the test detail", r.Detail)
	if _, err := st.GetGatewayConfig(ctx, nil, providerAqayepardakht); err == nil {
		t.Fatal("a test stored the candidate")
	}
	if rig.fake.Calls("create") != 0 {
		t.Fatal("a test created a payment")
	}
	// With the PIN stored, the stored settings are tested through the client in use.
	aqOn(t, svc, aqPINA)
	if r, err := svc.TestGateway(ctx, providerAqayepardakht, nil); err != nil || !r.OK {
		t.Fatalf("stored settings: %+v %v", r, err)
	}
}

// The environment seeds the database once, encrypted and with its case; without a master key it is used as
// it is and cannot be changed.
func TestAqayepardakhtSeed(t *testing.T) {
	ctx := context.Background()
	t.Run("seeded once", func(t *testing.T) {
		svc, st, logs := cfgService(t)
		rig := newAqRig(t)
		o := rig.options("prod")
		o.Envelope = envelope(t, keyA)
		ks := o.Keyed[providerAqayepardakht]
		ks.Seed = KeySettings{APIKey: " " + aqPINA + " "}
		o.Keyed[providerAqayepardakht] = ks
		if err := svc.ConfigureGateways(ctx, o); err != nil {
			t.Fatal(err)
		}
		got := keyedConfig(svc, providerAqayepardakht)
		if !got.Offered || !got.APIKeySet || got.FromEnv || got.Sandbox {
			t.Fatalf("seeded: %+v", got)
		}
		row, err := st.GetGatewayConfig(ctx, nil, providerAqayepardakht)
		if err != nil || strings.Contains(string(row.APIKeyEnc), aqPINA) {
			t.Fatalf("stored row: %+v %v", row, err)
		}
		assertNoPINs(t, "the log", logs.String())
		ks.Seed = KeySettings{APIKey: aqPINB}
		o.Keyed[providerAqayepardakht] = ks
		svc2, _ := testService(t)
		if err := svc2.ConfigureGateways(ctx, o); err != nil {
			t.Fatal(err)
		}
		row2, _ := st.GetGatewayConfig(ctx, nil, providerAqayepardakht)
		if dec, err := decryptKeyed(envelope(t, keyA), row2); err != nil || dec.APIKey != aqPINA {
			t.Fatalf("the seed overwrote the stored PIN: %+v %v", dec, err)
		}
	})
	t.Run("a bad seed stops the start", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newAqRig(t)
		o := rig.options("prod")
		o.Envelope = envelope(t, keyA)
		ks := o.Keyed[providerAqayepardakht]
		ks.Seed = KeySettings{APIKey: "no"}
		o.Keyed[providerAqayepardakht] = ks
		if err := svc.ConfigureGateways(ctx, o); err == nil {
			t.Fatal("a malformed PIN from the environment was accepted")
		}
	})
	t.Run("no master key: read-only", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newAqRig(t)
		o := rig.options("prod")
		ks := o.Keyed[providerAqayepardakht]
		ks.Seed = KeySettings{APIKey: aqPINA}
		o.Keyed[providerAqayepardakht] = ks
		if err := svc.ConfigureGateways(ctx, o); err != nil {
			t.Fatal(err)
		}
		got := keyedConfig(svc, providerAqayepardakht)
		if !got.Offered || !got.FromEnv || !got.KeyMissing {
			t.Fatalf("from the environment: %+v", got)
		}
		if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerAqayepardakht, APIKey: aqPINB}); !errors.Is(err, ErrKeyMissing) {
			t.Fatalf("changed without a master key: %v", err)
		}
	})
}

// The reconciler's queue and the reports know the provider.
func TestAqayepardakhtIsReportedAndReconciled(t *testing.T) {
	svc, _, _ := cfgService(t)
	ctx := context.Background()
	rig := newAqRig(t)
	rig.configure(t, svc, "dev")
	aqOn(t, svc, aqPINA)
	in, err := svc.StartGateway(ctx, aqIntent("rp-1", 1_500_000), "plan", aqCallback)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = rig.fake.Pay(*in.ExternalID, true)
	if n, err := svc.ReconcileOnce(ctx, time.Millisecond); err != nil || n != 1 {
		t.Fatalf("reconciler: %d %v", n, err)
	}
	rep, err := svc.GatewayReport(ctx, providerAqayepardakht, time.Time{}, time.Time{})
	if err != nil || rep.Provider != providerAqayepardakht || len(rep.Totals) != 1 || rep.Totals[0].Status != "succeeded" || rep.LastPaid == nil {
		t.Fatalf("report: %+v %v", rep, err)
	}
}
