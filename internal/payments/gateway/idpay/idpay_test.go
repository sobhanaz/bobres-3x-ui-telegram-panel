package idpay_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/idpayfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/idpay"
)

const apiKey = "6a7f99eb-7c20-4412-a972-6dfb7cd253a4" //nolint:gosec // a fake key for the test server

func newGW(t *testing.T, sandbox bool) (*idpay.Gateway, *idpayfake.Server) {
	t.Helper()
	fake := idpayfake.New()
	t.Cleanup(fake.Close)
	g, err := idpay.New(idpay.Config{APIKey: apiKey, Sandbox: sandbox, BaseURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	return g, fake
}

func charge(rial int64) gateway.Charge {
	return gateway.Charge{IntentID: "intent-1", Amount: rial, Currency: "IRR", Description: "Monthly 50GB",
		CallbackURL: "https://shop.example.test/webhooks/idpay"}
}

func TestConfig(t *testing.T) {
	for _, c := range []idpay.Config{
		{APIKey: "nope"},
		{APIKey: ""},
		{APIKey: apiKey, Proxy: "socks5://1.2.3.4:1080"},
		{APIKey: apiKey, Proxy: "::::"},
	} {
		if _, err := idpay.New(c); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	if _, err := idpay.New(idpay.Config{APIKey: strings.ToUpper(apiKey), Proxy: "http://user:pw@10.0.0.2:3128"}); err != nil { //nolint:gosec // test fixture
		t.Fatalf("valid config: %v", err)
	}
}

func TestCreateGivesTheRedirectURL(t *testing.T) {
	g, fake := newGW(t, false)
	st, err := g.Create(context.Background(), charge(1_500_000))
	if err != nil || !idpay.ValidID(st.ExternalID) || !strings.HasSuffix(st.PayURL, "/p/ws/"+st.ExternalID) {
		t.Fatalf("create: %+v %v", st, err)
	}
	if fake.Amount(st.ExternalID) != 1_500_000 || fake.Description(st.ExternalID) != "Monthly 50GB" {
		t.Fatal("IDPay got another amount or description")
	}
	if fake.LastHeader("X-API-KEY") != apiKey || fake.LastHeader("X-SANDBOX") != "" {
		t.Fatalf("headers: key %q sandbox %q", fake.LastHeader("X-API-KEY"), fake.LastHeader("X-SANDBOX"))
	}
}

func TestCreateRefusesWhatIDPayRefuses(t *testing.T) {
	g, fake := newGW(t, false)
	ctx := context.Background()
	bad := []gateway.Charge{charge(999), charge(500_000_001), charge(10_000), charge(10_000)}
	bad[2].Currency = "IRT"
	bad[3].IntentID = strings.Repeat("x", 51)
	for _, c := range bad {
		if _, err := g.Create(ctx, c); err == nil {
			t.Errorf("created %+v", c)
		}
	}
	if fake.Calls("create") != 0 {
		t.Fatal("a charge that cannot be valid reached IDPay")
	}
	if _, err := g.Create(ctx, charge(10_000)); err != nil {
		t.Fatalf("the minimum: %v", err)
	}
}

func TestSandboxFlag(t *testing.T) {
	g, fake := newGW(t, true)
	st, err := g.Create(context.Background(), charge(20_000))
	if err != nil || !strings.Contains(st.PayURL, "/p/ws-sandbox/") {
		t.Fatalf("create: %+v %v", st, err)
	}
	if fake.LastHeader("X-SANDBOX") != "1" {
		t.Fatal("the sandbox header was not sent")
	}
}

func TestPaidFlowIsIdempotent(t *testing.T) {
	g, fake := newGW(t, false)
	ctx := context.Background()
	c := charge(1_500_000)
	st, _ := g.Create(ctx, c)
	// Not paid yet: pending while the window is open.
	c.ExpiresAt = time.Now().Add(time.Hour)
	if r, err := g.Check(ctx, st.ExternalID, c); err != nil || r.State != gateway.Pending {
		t.Fatalf("unpaid: %+v %v", r, err)
	}
	if _, err := fake.Pay(st.ExternalID, true); err != nil {
		t.Fatal(err)
	}
	c.Returned = true
	for i := 0; i < 3; i++ { // verify answers 100, then 101: always paid
		r, err := g.Check(ctx, st.ExternalID, c)
		if err != nil || r.State != gateway.Paid || r.Amount != 1_500_000 || r.Currency != "IRR" || r.Reference == "" {
			t.Fatalf("check %d: %+v %v", i, r, err)
		}
	}
}

func TestCallbackFailureIsFinalOnlyOnceTheCustomerCameBack(t *testing.T) {
	g, fake := newGW(t, false)
	ctx := context.Background()
	c := charge(100_000)
	st, _ := g.Create(ctx, c)
	_, _ = fake.Pay(st.ExternalID, false)
	c.ExpiresAt = time.Now().Add(time.Hour)
	if r, _ := g.Check(ctx, st.ExternalID, c); r.State != gateway.Pending {
		t.Fatalf("the customer may still retry: %+v", r)
	}
	c.Returned = true
	if r, err := g.Check(ctx, st.ExternalID, c); err != nil || r.State != gateway.Failed || r.Reason != "gateway_failed" {
		t.Fatalf("after the return: %+v %v", r, err)
	}
}

func TestCancelledByThePayer(t *testing.T) {
	g, fake := newGW(t, false)
	st, _ := g.Create(context.Background(), charge(100_000))
	fake.SetStatus(st.ExternalID, idpayfake.Cancelled, 0)
	c := charge(100_000)
	c.Returned = true
	if r, err := g.Check(context.Background(), st.ExternalID, c); err != nil || r.State != gateway.Failed {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestUnpaidAfterTheWindowAsksTheInquiry(t *testing.T) {
	g, fake := newGW(t, false)
	st, _ := g.Create(context.Background(), charge(100_000))
	c := charge(100_000)
	c.IntentID = "intent-1"
	c.ExpiresAt = time.Now().Add(-time.Minute)
	// At the bank (inquiry 8): not final.
	fake.ReportUnpaidAs(idpayfake.AtBank)
	if r, _ := g.Check(context.Background(), st.ExternalID, c); r.State != gateway.Pending {
		t.Fatalf("at the bank: %+v", r)
	}
	// Reversed to the payer: final.
	fake.SetStatus(st.ExternalID, idpayfake.Reversed, 0)
	if r, _ := g.Check(context.Background(), st.ExternalID, c); r.State != gateway.Failed || r.Reason != "reversed" {
		t.Fatalf("reversed: %+v", r)
	}
}

func TestAmountMismatchIsReportedNotSettled(t *testing.T) {
	g, fake := newGW(t, false)
	st, _ := g.Create(context.Background(), charge(1_000_000))
	fake.SetStatus(st.ExternalID, idpayfake.AwaitVerify, 400_000) // the payer paid less
	c := charge(1_000_000)
	c.Returned = true
	r, err := g.Check(context.Background(), st.ExternalID, c)
	if err != nil || r.State != gateway.Paid || r.Amount != 400_000 {
		t.Fatalf("the paid amount must be reported so the caller can refuse it: %+v %v", r, err)
	}
	// A fee the payer bears is paid on top: never more than registered.
	st2, _ := g.Create(context.Background(), charge(1_000_000))
	fake.SetStatus(st2.ExternalID, idpayfake.AwaitVerify, 1_025_000)
	c2 := charge(1_000_000)
	if r, _ := g.Check(context.Background(), st2.ExternalID, c2); r.Amount != 1_000_000 {
		t.Fatalf("paid amount %d", r.Amount)
	}
}

func TestAnswerAboutAnotherOrderIsNotOurs(t *testing.T) {
	g, fake := newGW(t, false)
	id := fake.AddPayment("someone-else", 1_000_000, idpayfake.AwaitVerify)
	// The fake only answers a matching order_id, so ask as that order.
	c := charge(1_000_000)
	c.IntentID = "someone-else"
	if r, err := g.Check(context.Background(), id, c); err != nil || r.State != gateway.Paid {
		t.Fatalf("%+v %v", r, err)
	}
	c.IntentID = "intent-1"
	if r, _ := g.Check(context.Background(), id, c); r.State == gateway.Paid {
		t.Fatalf("settled another order's payment: %+v", r)
	}
}

func TestBadIDsNeverReachIDPay(t *testing.T) {
	g, fake := newGW(t, false)
	for _, id := range []string{"", "../x", "a b", "short"} {
		r, err := g.Check(context.Background(), id, charge(10_000))
		if err != nil || r.State != gateway.Failed {
			t.Errorf("%q: %+v %v", id, r, err)
		}
	}
	if fake.Calls("verify") != 0 {
		t.Fatal("a malformed id reached IDPay")
	}
}

func TestProviderDownAndTimeouts(t *testing.T) {
	g, fake := newGW(t, false)
	ctx := context.Background()
	st, _ := g.Create(ctx, charge(100_000))
	fake.Down(1)
	if r, err := g.Check(ctx, st.ExternalID, charge(100_000)); err == nil || r.State == gateway.Paid || r.State == gateway.Failed {
		t.Fatalf("an outage must be an error to retry, got %+v %v", r, err)
	}
	fake.Down(1)
	if _, err := g.Create(ctx, charge(100_000)); err == nil {
		t.Fatal("created during an outage")
	}
	// A slow gateway: the caller's deadline ends the call.
	fake.Delay(600 * time.Millisecond)
	tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := g.Check(tctx, st.ExternalID, charge(100_000))
	if err == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("slow call: %v after %v", err, time.Since(start))
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("the error names the address: %v", err)
	}
}

func TestAnswerWithoutAStatusIsNotPaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	g, _ := idpay.New(idpay.Config{APIKey: apiKey, BaseURL: srv.URL})
	id := strings.Repeat("a", 32)
	if r, err := g.Check(context.Background(), id, charge(10_000)); err == nil || r.State == gateway.Paid {
		t.Fatalf("%+v %v", r, err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`<html>oops</html>`)) }))
	defer srv2.Close()
	g2, _ := idpay.New(idpay.Config{APIKey: apiKey, BaseURL: srv2.URL})
	if r, err := g2.Check(context.Background(), id, charge(10_000)); err == nil || r.State == gateway.Paid {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestWrongKeyIsAnErrorNotAPaymentState(t *testing.T) {
	g, fake := newGW(t, false)
	fake.OnlyKey("00000000-0000-4000-8000-000000000000")
	st, err := g.Create(context.Background(), charge(10_000))
	var ie *idpay.Error
	if !errors.As(err, &ie) || ie.Code != 12 || st.ExternalID != "" {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := g.Test(context.Background()); err == nil {
		t.Fatal("a wrong key passed the test")
	}
}

func TestTestIsReadOnlyAndCountsUnverified(t *testing.T) {
	g, fake := newGW(t, false)
	res, err := g.Test(context.Background())
	if err != nil || res.Unverified != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	fake.AddPayment("o1", 10_000, idpayfake.AwaitVerify)
	fake.AddPayment("o2", 10_000, idpayfake.NotPaid)
	res, err = g.Test(context.Background())
	if err != nil || res.Unverified != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if fake.Calls("create") != 0 || fake.Calls("verify") != 0 {
		t.Fatal("the test created or verified a payment")
	}
}
