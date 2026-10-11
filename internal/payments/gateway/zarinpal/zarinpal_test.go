package zarinpal_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if _, err := zarinpal.New(zarinpal.Config{MerchantID: strings.ToUpper(merchant), Proxy: "http://user:pw@10.0.0.2:3128"}); err != nil { //nolint:gosec // test fixture
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

func TestRequestStatesTheCurrency(t *testing.T) {
	g, fake := newGW(t)
	st, err := g.Create(context.Background(), charge(1_500_000))
	if err != nil {
		t.Fatal(err)
	}
	if got := fake.Currency(st.ExternalID); got != "IRR" {
		t.Fatalf("currency sent: %q (amounts are Rial and must say so)", got)
	}
}

// An unpaid payment is not failed while the customer may still pay, even if
// Zarinpal's inquiry calls an unopened session FAILED (undocumented): only the
// customer's return or the end of the pay window make the answer final.
func TestUnpaidStaysPendingDuringThePayWindow(t *testing.T) {
	g, fake := newGW(t)
	fake.ReportUnpaidAs("FAILED")
	ctx := context.Background()
	open := charge(1_500_000)
	open.ExpiresAt = time.Now().Add(time.Hour)

	st, _ := g.Create(ctx, open)
	if r, err := g.Check(ctx, st.ExternalID, open); err != nil || r.State != gateway.Pending {
		t.Fatalf("in the pay window: %+v %v", r, err)
	}
	if n := fake.Calls("inquiry"); n != 0 {
		t.Fatalf("inquiry asked in the pay window: %d", n)
	}
	if _, err := fake.Pay(st.ExternalID, true); err != nil {
		t.Fatal(err)
	}
	if r, err := g.Check(ctx, st.ExternalID, open); err != nil || r.State != gateway.Paid {
		t.Fatalf("paid later in the window: %+v %v", r, err)
	}

	back := open
	back.Returned = true // the customer came back from the bank without paying
	st2, _ := g.Create(ctx, open)
	if r, err := g.Check(ctx, st2.ExternalID, back); err != nil || r.State != gateway.Failed || r.Reason != "gateway_failed" {
		t.Fatalf("after the customer's return: %+v %v", r, err)
	}
	closed := open
	closed.ExpiresAt = time.Now().Add(-time.Minute)
	st3, _ := g.Create(ctx, open)
	if r, err := g.Check(ctx, st3.ExternalID, closed); err != nil || r.State != gateway.Failed {
		t.Fatalf("after the pay window: %+v %v", r, err)
	}
}

// scripted answers verify with a fixed body, to cover Zarinpal's error shapes.
func scripted(t *testing.T, status int, body string) *zarinpal.Gateway {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/inquiry.json") {
			_, _ = w.Write([]byte(`{"data":{"code":100,"message":"Success","status":"IN_BANK"},"errors":[]}`))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	g, err := zarinpal.New(zarinpal.Config{MerchantID: merchant, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestVerifyAnswers(t *testing.T) {
	auth := "A" + strings.Repeat("0", 29) + "wwOGYp"
	open := charge(1_500_000)
	open.ExpiresAt = time.Now().Add(time.Hour)
	for _, c := range []struct {
		name, body string
		status     int
		state      gateway.State
		reason     string
		code       int // when nonzero: want a *zarinpal.Error with this code (retry later)
	}{
		{"another merchant's session", `{"data":[],"errors":{"code":-53,"message":"Session is not this merchant_id session","validations":[]}}`, 401, gateway.Failed, "not_found", 0},
		{"invalid authority, keyed by field", `{"message":"Invalid authority","errors":{"authority":["Invalid authority.","-54"]}}`, 422, gateway.Failed, "not_found", 0},
		{"not found yet", `{"data":[],"errors":{"code":"-55","message":"manual payment request not found.","validations":[]}}`, 401, gateway.Pending, "", 0},
		{"not paid", `{"data":[],"errors":{"code":-51,"message":"Session is not valid, session is not active paid try.","validations":[]}}`, 401, gateway.Pending, "", 0},
		{"suspended terminal", `{"data":[],"errors":{"code":-15,"message":"Terminal user is suspend","validations":[]}}`, 401, gateway.Pending, "", -15},
	} {
		r, err := scripted(t, c.status, c.body).Check(context.Background(), auth, open)
		if c.code != 0 {
			var ze *zarinpal.Error
			if !errors.As(err, &ze) || ze.Code != c.code {
				t.Errorf("%s: want error %d, got %+v %v", c.name, c.code, r, err)
			}
			continue
		}
		if err != nil || r.State != c.state || r.Reason != c.reason {
			t.Errorf("%s: %+v %v", c.name, r, err)
		}
	}
	// Paid without a ref_id: no made-up receipt number 0.
	r, err := scripted(t, 200, `{"data":{"code":101,"message":"Verified"},"errors":[]}`).Check(context.Background(), auth, open)
	if err != nil || r.State != gateway.Paid || r.Reference != "" {
		t.Fatalf("paid without ref_id: %+v %v", r, err)
	}
}
