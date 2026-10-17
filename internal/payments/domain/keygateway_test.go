package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/idpayfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/nextpayfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/idpay"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/nextpay"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

// Test fixtures, not secrets.
const (
	idpayKeyA   = "6a7f99eb-7c20-4412-a972-6dfb7cd253a4"
	idpayKeyB   = "7b8fa9fc-8d31-4523-b083-7efc8de364b5"
	nextpayKeyA = "d1c2b3a4-5e6f-4789-8abc-0d1e2f3a4b5c"
	nextpayKeyB = "e2d3c4b5-6f70-489a-9bcd-1e2f3a4b5c6d"
)

// keyedRig wires both API-key gateways to their fakes.
type keyedRig struct {
	idp *idpayfake.Server
	np  *nextpayfake.Server
}

func newKeyedRig(t *testing.T) *keyedRig {
	t.Helper()
	r := &keyedRig{idp: idpayfake.New(), np: nextpayfake.New()}
	t.Cleanup(r.idp.Close)
	t.Cleanup(r.np.Close)
	return r
}

func (r *keyedRig) options(t *testing.T, env string) GatewayOptions {
	t.Helper()
	return GatewayOptions{
		Env: env, Envelope: envelope(t, keyA),
		Build: (&builder{}).build,
		Keyed: map[string]KeyedOptions{
			providerIDPay: {Build: func(k KeySettings) (gateway.Gateway, error) {
				g, err := idpay.New(idpay.Config{APIKey: k.APIKey, Sandbox: k.Sandbox, Proxy: k.Proxy, BaseURL: r.idp.URL})
				if err != nil {
					return nil, err
				}
				return g, nil
			}},
			providerNextPay: {Build: func(k KeySettings) (gateway.Gateway, error) {
				g, err := nextpay.New(nextpay.Config{APIKey: k.APIKey, Sandbox: k.Sandbox, Proxy: k.Proxy, BaseURL: r.np.URL})
				if err != nil {
					return nil, err
				}
				return g, nil
			}},
		},
	}
}

type keyedCase struct {
	name, key, otherKey, callback string
}

var keyedCases = []keyedCase{
	{providerIDPay, idpayKeyA, idpayKeyB, "https://shop.example.test/webhooks/idpay"},
	{providerNextPay, nextpayKeyA, nextpayKeyB, "https://shop.example.test/webhooks/nextpay"},
}

func (r *keyedRig) pay(t *testing.T, name, externalID string, ok bool) {
	t.Helper()
	var err error
	if name == providerIDPay {
		_, err = r.idp.Pay(externalID, ok)
	} else {
		_, err = r.np.Pay(externalID, ok)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (r *keyedRig) verifyCalls(name string) int {
	if name == providerIDPay {
		return r.idp.Calls("verify")
	}
	return r.np.Calls("verify")
}

func keyedIntent(name, key string, rial int64) *store.Intent {
	in := gwIntent(key, rial)
	in.Provider = name
	return in
}

func enableKeyed(t *testing.T, svc *Service, c keyedCase) GatewayConfig {
	t.Helper()
	got, err := svc.SetGatewayConfig(context.Background(), GatewayUpdate{Provider: c.name, Enabled: true, APIKey: c.key, ActorID: actor})
	if err != nil {
		t.Fatalf("%s: switch on: %v", c.name, err)
	}
	return got
}

func keyedConfig(svc *Service, name string) GatewayConfig {
	for _, c := range svc.GatewayConfigs() {
		if c.Provider == name {
			return c
		}
	}
	return GatewayConfig{}
}

// The settings follow Zarinpal's rules: off until a key is set, stored encrypted
// and never shown, listed after Zarinpal and USDT.
func TestKeyedGatewaySettings(t *testing.T) {
	for _, c := range keyedCases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, logs := cfgService(t)
			ctx := context.Background()
			rig := newKeyedRig(t)
			if err := svc.ConfigureGateways(ctx, rig.options(t, "prod")); err != nil {
				t.Fatal(err)
			}
			cfgs := svc.GatewayConfigs()
			if len(cfgs) != 4 || cfgs[0].Provider != providerZarinpal || cfgs[1].Provider != USDTAuto ||
				cfgs[2].Provider != providerIDPay || cfgs[3].Provider != providerNextPay {
				t.Fatalf("configs: %+v", cfgs)
			}
			if got := keyedConfig(svc, c.name); got.Enabled || got.Configured || got.Offered || got.APIKeySet {
				t.Fatalf("on without a key: %+v", got)
			}
			if offers(svc, c.name) {
				t.Fatal("offered without a key")
			}
			if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: c.name, Enabled: true}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("switched on without a key: %v", err)
			}
			if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: c.name, APIKey: "not-a-key"}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("a malformed key was accepted: %v", err)
			}
			if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: c.name, MerchantID: merchantA}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("a merchant id was accepted: %v", err)
			}
			got := enableKeyed(t, svc, c)
			if !got.Enabled || !got.Configured || !got.Offered || !got.APIKeySet || got.MerchantHint != "" || got.UpdatedBy != actor {
				t.Fatalf("after switching on: %+v", got)
			}
			if !offers(svc, c.name) {
				t.Fatal("not offered once on")
			}
			row, err := st.GetGatewayConfig(ctx, nil, c.name)
			if err != nil || len(row.APIKeyEnc) == 0 || strings.Contains(string(row.APIKeyEnc), c.key) {
				t.Fatalf("stored row: %+v %v", row, err)
			}
			if dec, err := decryptKeyed(envelope(t, keyA), row); err != nil || dec.APIKey != c.key {
				t.Fatalf("round trip: %+v %v", dec, err)
			}
			assertNoKeys(t, "the log", logs.String())
			// Zarinpal's settings changing does not drop it.
			if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerZarinpal, MerchantID: merchantA}); err != nil {
				t.Fatal(err)
			}
			if !offers(svc, c.name) || svc.Gateway(c.name) == nil {
				t.Fatal("a Zarinpal change dropped the gateway")
			}
			// Switching off stops offering; the client stays for open payments.
			if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: c.name}); err != nil {
				t.Fatal(err)
			}
			if offers(svc, c.name) || svc.Gateway(c.name) == nil {
				t.Fatal("switching off must keep the client but stop offering")
			}
			if got := keyedConfig(svc, c.name); !got.APIKeySet || got.Enabled || got.Offered {
				t.Fatalf("after switching off: %+v", got)
			}
			// A restart reads the stored settings back.
			svc2, _ := testService(t)
			if err := svc2.ConfigureGateways(ctx, rig.options(t, "prod")); err != nil {
				t.Fatal(err)
			}
			if got := keyedConfig(svc2, c.name); !got.APIKeySet || got.Enabled || !got.Configured {
				t.Fatalf("after a restart: %+v", got)
			}
		})
	}
}

func assertNoKeys(t *testing.T, where, text string) {
	t.Helper()
	for _, k := range []string{idpayKeyA, idpayKeyB, nextpayKeyA, nextpayKeyB} {
		if strings.Contains(text, k) {
			t.Errorf("%s holds an API key (%q)", where, k)
		}
	}
}

// Create, the redirect URL, a successful callback, a failed one, and a callback
// delivered twice: the gateway is the only source of truth, and one payment
// credits once.
func TestKeyedGatewayPaymentFlow(t *testing.T) {
	for _, c := range keyedCases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, _ := cfgService(t)
			ctx := context.Background()
			rig := newKeyedRig(t)
			if err := svc.ConfigureGateways(ctx, rig.options(t, "dev")); err != nil {
				t.Fatal(err)
			}
			enableKeyed(t, svc, c)

			in, err := svc.StartGateway(ctx, keyedIntent(c.name, "kf-"+c.name+"-1", 1_500_000), "plan", c.callback)
			if err != nil {
				t.Fatal(err)
			}
			if in.Status != "pending" || in.ExternalID == nil || in.PayURL == nil || !strings.HasPrefix(*in.PayURL, "http") {
				t.Fatalf("created: %+v", in)
			}
			if again, err := svc.StartGateway(ctx, keyedIntent(c.name, "kf-"+c.name+"-1", 1_500_000), "plan", c.callback); err != nil ||
				again.ID != in.ID || *again.ExternalID != *in.ExternalID {
				t.Fatalf("retry: %+v %v", again, err)
			}

			// Back from the bank before paying: still pending, nothing credited.
			if got, err := svc.CheckAfterReturn(ctx, in.ID); err != nil || got.Status != "pending" {
				t.Fatalf("unpaid return: %+v %v", got, err)
			}
			rig.pay(t, c.name, *in.ExternalID, true)
			got, err := svc.CheckAfterReturn(ctx, in.ID)
			if err != nil || got.Status != "succeeded" {
				t.Fatalf("paid return: %+v %v", got, err)
			}
			if n := ledgerCount(t, st); n != 1 {
				t.Fatalf("ledger: %d entries, want 1", n)
			}
			calls := rig.verifyCalls(c.name)
			// The same callback again, and the reconciler: nothing changes.
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
			if rig.verifyCalls(c.name) != calls {
				t.Fatalf("a settled payment was verified again (%d -> %d)", calls, rig.verifyCalls(c.name))
			}

			// The customer abandons another payment at the bank.
			bad, err := svc.StartGateway(ctx, keyedIntent(c.name, "kf-"+c.name+"-2", 1_500_000), "plan", c.callback)
			if err != nil {
				t.Fatal(err)
			}
			rig.pay(t, c.name, *bad.ExternalID, false)
			if got, err := svc.CheckAfterReturn(ctx, bad.ID); err != nil || got.Status != "failed" {
				t.Fatalf("failed return: %+v %v", got, err)
			}
			if n := ledgerCount(t, st); n != 1 {
				t.Fatalf("a failed payment credited: %d entries", n)
			}
		})
	}
}

// Whatever the callback says, the amount the gateway reports is compared with
// the one we asked for.
func TestIDPayAmountMismatchFailsThePayment(t *testing.T) {
	svc, st, _ := cfgService(t)
	ctx := context.Background()
	rig := newKeyedRig(t)
	if err := svc.ConfigureGateways(ctx, rig.options(t, "dev")); err != nil {
		t.Fatal(err)
	}
	c := keyedCases[0]
	enableKeyed(t, svc, c)
	in, err := svc.StartGateway(ctx, keyedIntent(c.name, "kf-mismatch", 1_500_000), "plan", c.callback)
	if err != nil {
		t.Fatal(err)
	}
	rig.idp.SetStatus(*in.ExternalID, idpayfake.AwaitVerify, 1_000_000) // paid less than asked
	got, err := svc.CheckAfterReturn(ctx, in.ID)
	if err != nil || got.Status != "failed" || got.FailureReason == nil || *got.FailureReason != ReasonAmountMismatch {
		t.Fatalf("underpaid: %+v %v", got, err)
	}
	if n := ledgerCount(t, st); n != 0 {
		t.Fatalf("an underpaid payment credited %d entries", n)
	}
}

// A provider that is down fails the start cleanly, and a check that cannot
// reach it settles nothing and keeps the payment open for the next one.
func TestKeyedGatewayOutage(t *testing.T) {
	for _, c := range keyedCases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, _ := cfgService(t)
			ctx := context.Background()
			rig := newKeyedRig(t)
			if err := svc.ConfigureGateways(ctx, rig.options(t, "dev")); err != nil {
				t.Fatal(err)
			}
			enableKeyed(t, svc, c)
			down := func(n int) {
				rig.idp.Down(n)
				rig.np.Down(n)
			}
			down(1)
			if _, err := svc.StartGateway(ctx, keyedIntent(c.name, "ko-"+c.name+"-1", 1_500_000), "plan", c.callback); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("start while down: %v", err)
			}
			in, err := svc.StartGateway(ctx, keyedIntent(c.name, "ko-"+c.name+"-1", 1_500_000), "plan", c.callback)
			if err != nil {
				t.Fatalf("retry after the outage: %v", err)
			}
			rig.pay(t, c.name, *in.ExternalID, true)
			down(1000)
			got, _ := svc.CheckAfterReturn(ctx, in.ID)
			if got != nil && got.Status != "pending" {
				t.Fatalf("a check that failed settled the payment: %+v", got)
			}
			if n := ledgerCount(t, st); n != 0 {
				t.Fatalf("credited during the outage: %d", n)
			}
			down(0)
			if _, err := st.DB().Exec(ctx, `UPDATE payments.payment_intents SET checked_at = NULL WHERE id = $1`, in.ID); err != nil {
				t.Fatal(err)
			}
			if got, err := svc.CheckAfterReturn(ctx, in.ID); err != nil || got.Status != "succeeded" {
				t.Fatalf("after the outage: %+v %v", got, err)
			}
		})
	}
}

// The sandbox: refused in prod (at runtime and for stored settings), IDPay's
// goes out as the X-SANDBOX header, and NextPay has none at all.
func TestKeyedGatewaySandbox(t *testing.T) {
	ctx := context.Background()
	t.Run("prod refuses it", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newKeyedRig(t)
		if err := svc.ConfigureGateways(ctx, rig.options(t, "prod")); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerIDPay, Sandbox: true, Enabled: true, APIKey: idpayKeyA}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("prod sandbox: %v", err)
		}
		if _, err := svc.TestGateway(ctx, providerIDPay, &GatewayCandidate{Sandbox: true, APIKey: idpayKeyA}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("prod sandbox test: %v", err)
		}
	})
	t.Run("idpay sandbox header", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newKeyedRig(t)
		if err := svc.ConfigureGateways(ctx, rig.options(t, "dev")); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerIDPay, Sandbox: true, Enabled: true, APIKey: idpayKeyA}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.StartGateway(ctx, keyedIntent(providerIDPay, "ks-1", 1_500_000), "plan", keyedCases[0].callback); err != nil {
			t.Fatal(err)
		}
		if h := rig.idp.LastHeader("X-SANDBOX"); h != "1" {
			t.Fatalf("X-SANDBOX = %q", h)
		}
		if !keyedConfig(svc, providerIDPay).Sandbox {
			t.Fatal("the sandbox flag is not shown")
		}
	})
	t.Run("nextpay has none", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newKeyedRig(t)
		if err := svc.ConfigureGateways(ctx, rig.options(t, "dev")); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerNextPay, Sandbox: true, Enabled: true, APIKey: nextpayKeyA}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("nextpay sandbox: %v", err)
		}
	})
	t.Run("stored sandbox in prod is never used", func(t *testing.T) {
		svc, st, _ := cfgService(t)
		rig := newKeyedRig(t)
		dev := rig.options(t, "dev")
		if err := svc.ConfigureGateways(ctx, dev); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerIDPay, Sandbox: true, Enabled: true, APIKey: idpayKeyA}); err != nil {
			t.Fatal(err)
		}
		svc2, _ := testService(t)
		if err := svc2.ConfigureGateways(ctx, rig.options(t, "prod")); err != nil {
			t.Fatal(err)
		}
		got := keyedConfig(svc2, providerIDPay)
		if got.Problem != ProblemSandboxRefused || got.Offered || got.Configured || svc2.Gateway(providerIDPay) != nil {
			t.Fatalf("stored sandbox in prod: %+v", got)
		}
		_ = st
	})
}

// A key that open payments were created with cannot change under them.
func TestKeyedGatewayKeyChangeRefusedWhileIntentsOpen(t *testing.T) {
	for _, c := range keyedCases {
		t.Run(c.name, func(t *testing.T) {
			svc, _, _ := cfgService(t)
			ctx := context.Background()
			rig := newKeyedRig(t)
			if err := svc.ConfigureGateways(ctx, rig.options(t, "dev")); err != nil {
				t.Fatal(err)
			}
			enableKeyed(t, svc, c)
			in, err := svc.StartGateway(ctx, keyedIntent(c.name, "kk-"+c.name, 1_500_000), "plan", c.callback)
			if err != nil {
				t.Fatal(err)
			}
			_, err = svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: c.name, Enabled: true, APIKey: c.otherKey})
			var open *OpenIntentsError
			if !errors.As(err, &open) || !errors.Is(err, ErrPrecondition) || open.Count != 1 || !strings.Contains(err.Error(), "API key") {
				t.Fatalf("key change with an open payment: %v", err)
			}
			// Settings that are not the key still change.
			if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: c.name, Enabled: false}); err != nil {
				t.Fatalf("switching off: %v", err)
			}
			rig.pay(t, c.name, *in.ExternalID, true)
			if got, err := svc.CheckAfterReturn(ctx, in.ID); err != nil || got.Status != "succeeded" {
				t.Fatalf("a switched-off gateway must still settle: %+v %v", got, err)
			}
			if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: c.name, Enabled: true, APIKey: c.otherKey}); err != nil {
				t.Fatalf("key change once nothing is open: %v", err)
			}
		})
	}
}

// The connection test is read-only and tells a refused key from a good one.
func TestKeyedGatewayTestIsReadOnly(t *testing.T) {
	for _, c := range keyedCases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, _ := cfgService(t)
			ctx := context.Background()
			rig := newKeyedRig(t)
			rig.idp.OnlyKey(idpayKeyA)
			rig.np.OnlyKey(nextpayKeyA)
			if err := svc.ConfigureGateways(ctx, rig.options(t, "dev")); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.TestGateway(ctx, c.name, nil); !errors.Is(err, ErrPrecondition) {
				t.Fatalf("tested without settings: %v", err)
			}
			if _, err := svc.TestGateway(ctx, c.name, &GatewayCandidate{}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("candidate without a key: %v", err)
			}
			if r, err := svc.TestGateway(ctx, c.name, &GatewayCandidate{APIKey: c.key}); err != nil || !r.OK {
				t.Fatalf("good key: %+v %v", r, err)
			}
			r, err := svc.TestGateway(ctx, c.name, &GatewayCandidate{APIKey: c.otherKey})
			if err != nil || r.OK || r.Detail == "" {
				t.Fatalf("refused key: %+v %v", r, err)
			}
			assertNoKeys(t, "the test detail", r.Detail)
			if _, err := st.GetGatewayConfig(ctx, nil, c.name); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("a test stored the candidate")
			}
			if rig.idp.Calls("create")+rig.np.Calls("token") != 0 {
				t.Fatal("a test created a payment")
			}
		})
	}
}

// Another master key cannot read the stored key: the gateway is unusable until
// the key is entered again, and nothing is guessed.
func TestKeyedGatewayUnreadableWithAnotherMasterKey(t *testing.T) {
	c := keyedCases[0]
	svc, _, _ := cfgService(t)
	ctx := context.Background()
	rig := newKeyedRig(t)
	if err := svc.ConfigureGateways(ctx, rig.options(t, "dev")); err != nil {
		t.Fatal(err)
	}
	enableKeyed(t, svc, c)
	svc2, _ := testService(t)
	o := rig.options(t, "dev")
	o.Envelope = envelope(t, keyB)
	if err := svc2.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	got := keyedConfig(svc2, c.name)
	if got.Problem != ProblemUnreadable || got.Offered || got.Configured {
		t.Fatalf("unreadable settings: %+v", got)
	}
	if _, err := svc2.SetGatewayConfig(ctx, GatewayUpdate{Provider: c.name, Enabled: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("re-enabled without entering the key: %v", err)
	}
	if got, err := svc2.SetGatewayConfig(ctx, GatewayUpdate{Provider: c.name, Enabled: true, APIKey: c.key}); err != nil || !got.Offered {
		t.Fatalf("key entered again: %+v %v", got, err)
	}
}

// The environment seeds the database once, encrypted; without a master key it
// is used as it is and cannot be changed.
func TestKeyedGatewaySeed(t *testing.T) {
	ctx := context.Background()
	t.Run("seeded once", func(t *testing.T) {
		svc, st, logs := cfgService(t)
		rig := newKeyedRig(t)
		o := rig.options(t, "prod")
		ks := o.Keyed[providerNextPay]
		ks.Seed = KeySettings{APIKey: " " + strings.ToUpper(nextpayKeyA) + " "}
		o.Keyed[providerNextPay] = ks
		if err := svc.ConfigureGateways(ctx, o); err != nil {
			t.Fatal(err)
		}
		got := keyedConfig(svc, providerNextPay)
		if !got.Offered || !got.APIKeySet || got.FromEnv {
			t.Fatalf("seeded: %+v", got)
		}
		row, err := st.GetGatewayConfig(ctx, nil, providerNextPay)
		if err != nil || strings.Contains(string(row.APIKeyEnc), nextpayKeyA) {
			t.Fatalf("stored row: %+v %v", row, err)
		}
		assertNoKeys(t, "the log", logs.String())
		// A different seed on the next start does not overwrite the stored one.
		ks.Seed = KeySettings{APIKey: nextpayKeyB}
		o.Keyed[providerNextPay] = ks
		svc2, _ := testService(t)
		if err := svc2.ConfigureGateways(ctx, o); err != nil {
			t.Fatal(err)
		}
		row2, _ := st.GetGatewayConfig(ctx, nil, providerNextPay)
		if dec, err := decryptKeyed(envelope(t, keyA), row2); err != nil || dec.APIKey != nextpayKeyA {
			t.Fatalf("the seed overwrote the stored key: %+v %v", dec, err)
		}
	})
	t.Run("a bad seed stops the start", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newKeyedRig(t)
		o := rig.options(t, "prod")
		ks := o.Keyed[providerIDPay]
		ks.Seed = KeySettings{APIKey: "nope"}
		o.Keyed[providerIDPay] = ks
		if err := svc.ConfigureGateways(ctx, o); err == nil {
			t.Fatal("a malformed key from the environment was accepted")
		}
	})
	t.Run("no master key: read-only", func(t *testing.T) {
		svc, _, _ := cfgService(t)
		rig := newKeyedRig(t)
		o := rig.options(t, "prod")
		o.Envelope = nil
		ks := o.Keyed[providerIDPay]
		ks.Seed = KeySettings{APIKey: idpayKeyA}
		o.Keyed[providerIDPay] = ks
		if err := svc.ConfigureGateways(ctx, o); err != nil {
			t.Fatal(err)
		}
		got := keyedConfig(svc, providerIDPay)
		if !got.Offered || !got.FromEnv || !got.KeyMissing {
			t.Fatalf("from the environment: %+v", got)
		}
		if _, err := svc.SetGatewayConfig(ctx, GatewayUpdate{Provider: providerIDPay, APIKey: idpayKeyB}); !errors.Is(err, ErrKeyMissing) {
			t.Fatalf("changed without a master key: %v", err)
		}
	})
}
