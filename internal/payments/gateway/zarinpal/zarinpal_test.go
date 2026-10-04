package zarinpal_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/zarinpal"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/zpfake"
)

const merchant = "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b"

func newGW(t *testing.T) (*zarinpal.Gateway, *zpfake.Server) {
	t.Helper()
	fake := zpfake.New()
	t.Cleanup(fake.Close)
	g, err := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	return g, fake
}

func charge(rial int64) gateway.Charge {
	return gateway.Charge{IntentID: "intent-1", Amount: rial, Currency: "IRR", Description: "Monthly 50GB",
		CallbackURL: "https://shop.example.test/webhooks/zarinpal"}
}

func TestConfig(t *testing.T) {
	for _, c := range []zarinpal.Config{
		{MerchantID: "not-a-uuid"},
		{MerchantID: merchant, Proxy: "socks5://1.2.3.4:1080"},
		{MerchantID: merchant, Proxy: "::::"},
	} {
		if _, err := zarinpal.New(c); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	if _, err := zarinpal.New(zarinpal.Config{MerchantID: strings.ToUpper(merchant), Proxy: "http://user:pw@10.0.0.2:3128"}); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	g, _ := zarinpal.New(zarinpal.Config{MerchantID: merchant, Sandbox: true})
	if got := g.StartPayURL("S123"); got != "https://sandbox.zarinpal.com/pg/StartPay/S123" {
		t.Fatalf("sandbox start URL: %s", got)
	}
}

func TestPaidFlowIsIdempotent(t *testing.T) {
	g, fake := newGW(t)
	ctx := context.Background()
	st, err := g.Create(ctx, charge(1_500_000))
	if err != nil || !strings.HasPrefix(st.ExternalID, "S") || !strings.HasSuffix(st.PayURL, "/pg/StartPay/"+st.ExternalID) {
		t.Fatalf("create: %+v %v", st, err)
	}
	if fake.Description(st.ExternalID) != "Monthly 50GB" {
		t.Fatalf("description: %q", fake.Description(st.ExternalID))
	}
	// Still at the bank: pending (verify -51, inquiry IN_BANK).
	if r, err := g.Check(ctx, st.ExternalID, charge(1_500_000)); err != nil || r.State != gateway.Pending {
		t.Fatalf("before paying: %+v %v", r, err)
	}
	if _, err := fake.Pay(st.ExternalID, true); err != nil {
		t.Fatal(err)
	}
	r1, err := g.Check(ctx, st.ExternalID, charge(1_500_000))
	if err != nil || r1.State != gateway.Paid || r1.Amount != 1_500_000 || r1.Currency != "IRR" || r1.Reference == "" {
		t.Fatalf("verify: %+v %v", r1, err)
	}
	// verify again answers 101 with the same reference: still paid.
	r2, err := g.Check(ctx, st.ExternalID, charge(1_500_000))
	if err != nil || r2.State != gateway.Paid || r2.Reference != r1.Reference {
		t.Fatalf("re-verify: %+v %v", r2, err)
	}
}

func TestFailuresAndMismatch(t *testing.T) {
	g, fake := newGW(t)
	ctx := context.Background()

	cancelled, _ := g.Create(ctx, charge(1_500_000))
	_, _ = fake.Pay(cancelled.ExternalID, false)
	if r, err := g.Check(ctx, cancelled.ExternalID, charge(1_500_000)); err != nil || r.State != gateway.Failed || r.Reason != "gateway_failed" {
		t.Fatalf("cancelled: %+v %v", r, err)
	}

	paid, _ := g.Create(ctx, charge(1_500_000))
	_, _ = fake.Pay(paid.ExternalID, true)
	if r, err := g.Check(ctx, paid.ExternalID, charge(150_000)); err != nil || r.State != gateway.Failed || r.Reason != "amount_mismatch" {
		t.Fatalf("verify with another amount: %+v %v", r, err)
	}

	if r, err := g.Check(ctx, "S"+strings.Repeat("9", 35), charge(1_500_000)); err != nil || r.State != gateway.Failed || r.Reason != "not_found" {
		t.Fatalf("unknown authority: %+v %v", r, err)
	}
	if r, err := g.Check(ctx, "../../etc", charge(1_500_000)); err != nil || r.State != gateway.Failed {
		t.Fatalf("malformed authority: %+v %v", r, err)
	}
	// Bad requests never reach Zarinpal.
	for _, c := range []gateway.Charge{
		{Amount: 1_500_000, Currency: "IRT", CallbackURL: "https://x"},
		{Amount: 9_999, Currency: "IRR", CallbackURL: "https://x"},
		{Amount: 1_500_000, Currency: "IRR"},
	} {
		if _, err := g.Create(ctx, c); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	// A configuration error is an error to retry later, not a verdict.
	var ze *zarinpal.Error
	down, err := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := down.Check(ctx, paid.ExternalID, charge(1_500_000)); err == nil || errors.As(err, &ze) {
		t.Fatalf("unreachable Zarinpal: %v", err)
	}
	if fake.Calls("request") != 2 {
		t.Fatalf("invalid requests reached Zarinpal: %d request calls", fake.Calls("request"))
	}
}
