package nextpay_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/nextpayfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/nextpay"
)

const apiKey = "b11ee9c3-d23d-414e-8b6e-f2370baac97b"

func newGW(t *testing.T) (*nextpay.Gateway, *nextpayfake.Server) {
	t.Helper()
	fake := nextpayfake.New()
	t.Cleanup(fake.Close)
	g, err := nextpay.New(nextpay.Config{APIKey: apiKey, BaseURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	return g, fake
}

func charge(rial int64) gateway.Charge {
	return gateway.Charge{IntentID: "intent-1", Amount: rial, Currency: "IRR", Description: "Monthly 50GB",
		CallbackURL: "https://shop.example.test/webhooks/nextpay"}
}

func TestConfig(t *testing.T) {
	for _, c := range []nextpay.Config{
		{APIKey: "nope"},
		{APIKey: apiKey, Sandbox: true}, // NextPay has no sandbox
		{APIKey: apiKey, Proxy: "socks5://1.2.3.4:1080"},
		{APIKey: apiKey, Proxy: "::::"},
	} {
		if _, err := nextpay.New(c); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	if _, err := nextpay.New(nextpay.Config{APIKey: strings.ToUpper(apiKey), Proxy: "http://user:pw@10.0.0.2:3128"}); err != nil { //nolint:gosec // test fixture
		t.Fatalf("valid config: %v", err)
	}
}

func TestCreateGivesTheRedirectURL(t *testing.T) {
	g, fake := newGW(t)
	st, err := g.Create(context.Background(), charge(1_500_000))
	if err != nil || !nextpay.ValidID(st.ExternalID) || !strings.HasSuffix(st.PayURL, "/nx/gateway/payment/"+st.ExternalID) {
		t.Fatalf("create: %+v %v", st, err)
	}
	if st.PayURL != g.PayURL(st.ExternalID) || fake.Calls("token") != 1 {
		t.Fatalf("%+v", st)
	}
}

func TestCreateRefusesWhatNextPayRefuses(t *testing.T) {
	g, fake := newGW(t)
	ctx := context.Background()
	long := charge(100_000)
	long.CallbackURL = "https://shop.example.test/" + strings.Repeat("a", 100)
	toman := charge(100_000)
	toman.Currency = "IRT"
	for _, c := range []gateway.Charge{charge(9_999), charge(1_000_000_001), long, toman} {
		if _, err := g.Create(ctx, c); err == nil {
			t.Errorf("created %+v", c)
		}
	}
	if fake.Calls("token") != 0 {
		t.Fatal("a charge that cannot be valid reached NextPay")
	}
}

func TestPaidFlow(t *testing.T) {
	g, fake := newGW(t)
	ctx := context.Background()
	c := charge(1_500_000)
	st, _ := g.Create(ctx, c)
	c.ExpiresAt = time.Now().Add(time.Hour)
	if r, err := g.Check(ctx, st.ExternalID, c); err != nil || r.State != gateway.Pending {
		t.Fatalf("unpaid: %+v %v", r, err)
	}
	cb, err := fake.Pay(st.ExternalID, true)
	if err != nil || !strings.Contains(cb, "trans_id="+st.ExternalID) {
		t.Fatalf("callback %q %v", cb, err)
	}
	c.Returned = true
	r, err := g.Check(ctx, st.ExternalID, c)
	if err != nil || r.State != gateway.Paid || r.Amount != 1_500_000 || r.Currency != "IRR" || r.Reference == "" {
		t.Fatalf("check: %+v %v", r, err)
	}
}

func TestRepeatedVerifyIsNeverCountedAsAPaymentAgain(t *testing.T) {
	// NextPay answers a second verify with -49 ("duplicate"): an error to look
	// at, not a settled payment. The caller settles once, in its database.
	g, fake := newGW(t)
	st, _ := g.Create(context.Background(), charge(100_000))
	_, _ = fake.Pay(st.ExternalID, true)
	c := charge(100_000)
	c.Returned = true
	if r, _ := g.Check(context.Background(), st.ExternalID, c); r.State != gateway.Paid {
		t.Fatalf("first: %+v", r)
	}
	r, err := g.Check(context.Background(), st.ExternalID, c)
	var ne *nextpay.Error
	if !errors.As(err, &ne) || ne.Code != -49 || r.State == gateway.Paid || r.State == gateway.Failed {
		t.Fatalf("second: %+v %v", r, err)
	}
}

func TestCallbackFailure(t *testing.T) {
	g, fake := newGW(t)
	ctx := context.Background()
	c := charge(100_000)
	st, _ := g.Create(ctx, c)
	_, _ = fake.Pay(st.ExternalID, false)
	c.ExpiresAt = time.Now().Add(time.Hour)
	if r, _ := g.Check(ctx, st.ExternalID, c); r.State != gateway.Pending {
		t.Fatalf("the customer may retry: %+v", r)
	}
	c.Returned = true
	if r, err := g.Check(ctx, st.ExternalID, c); err != nil || r.State != gateway.Failed || r.Reason != "gateway_failed" {
		t.Fatalf("after the return: %+v %v", r, err)
	}
	fake.SetCode(st.ExternalID, nextpayfake.Cancelled)
	if r, _ := g.Check(ctx, st.ExternalID, c); r.State != gateway.Failed {
		t.Fatalf("cancelled: %+v", r)
	}
	fake.SetCode(st.ExternalID, nextpayfake.AtBank)
	if r, _ := g.Check(ctx, st.ExternalID, c); r.State != gateway.Pending {
		t.Fatalf("at the bank: %+v", r)
	}
}

func TestAmountMismatch(t *testing.T) {
	// NextPay checks the amount we send against the one registered (-24).
	g, fake := newGW(t)
	st, _ := g.Create(context.Background(), charge(1_000_000))
	_, _ = fake.Pay(st.ExternalID, true)
	c := charge(900_000)
	c.Returned = true
	r, err := g.Check(context.Background(), st.ExternalID, c)
	var ne *nextpay.Error
	if !errors.As(err, &ne) || ne.Code != -24 || r.State == gateway.Paid {
		t.Fatalf("%+v %v", r, err)
	}
	// An answer carrying another amount is reported, so the caller refuses it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"amount":50,"order_id":"intent-1","Shaparak_Ref_Id":"1"}`))
	}))
	defer srv.Close()
	g2, _ := nextpay.New(nextpay.Config{APIKey: apiKey, BaseURL: srv.URL})
	id := "f7c07568-c6d1-4bee-87b1-4a9e5ed2e4c1"
	if r, err := g2.Check(context.Background(), id, charge(1_000_000)); err != nil || r.State != gateway.Paid || r.Amount != 50 {
		t.Fatalf("%+v %v", r, err)
	}
	// The same amount told in Toman is the same amount.
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"amount":100000,"order_id":"intent-1","Shaparak_Ref_Id":"1"}`))
	}))
	defer srv3.Close()
	g3, _ := nextpay.New(nextpay.Config{APIKey: apiKey, BaseURL: srv3.URL})
	if r, _ := g3.Check(context.Background(), id, charge(1_000_000)); r.Amount != 1_000_000 {
		t.Fatalf("%+v", r)
	}
}

func TestAnswerAboutAnotherOrderIsNotOurs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"amount":100000,"order_id":"another","Shaparak_Ref_Id":"1"}`))
	}))
	defer srv.Close()
	g, _ := nextpay.New(nextpay.Config{APIKey: apiKey, BaseURL: srv.URL})
	r, _ := g.Check(context.Background(), "f7c07568-c6d1-4bee-87b1-4a9e5ed2e4c1", charge(1_000_000))
	if r.State == gateway.Paid {
		t.Fatalf("settled another order's payment: %+v", r)
	}
}

func TestRefundAnswerIsNeverPaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":-90,"amount":100000,"order_id":"intent-1"}`))
	}))
	defer srv.Close()
	g, _ := nextpay.New(nextpay.Config{APIKey: apiKey, BaseURL: srv.URL})
	r, err := g.Check(context.Background(), "f7c07568-c6d1-4bee-87b1-4a9e5ed2e4c1", charge(1_000_000))
	if err == nil || r.State == gateway.Paid {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAnswersWithoutACodeAreNotPaid(t *testing.T) {
	for _, body := range []string{`{}`, `<html>oops</html>`, `{"amount":100000,"order_id":"intent-1"}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		g, _ := nextpay.New(nextpay.Config{APIKey: apiKey, BaseURL: srv.URL})
		r, err := g.Check(context.Background(), "f7c07568-c6d1-4bee-87b1-4a9e5ed2e4c1", charge(1_000_000))
		if err == nil || r.State == gateway.Paid || r.State == gateway.Failed {
			t.Errorf("%s: %+v %v", body, r, err)
		}
		if _, err := g.Create(context.Background(), charge(1_000_000)); err == nil {
			t.Errorf("%s: created", body)
		}
		srv.Close()
	}
}

func TestBadIDsNeverReachNextPay(t *testing.T) {
	g, fake := newGW(t)
	for _, id := range []string{"", "../x", "a b", "short"} {
		r, err := g.Check(context.Background(), id, charge(100_000))
		if err != nil || r.State != gateway.Failed {
			t.Errorf("%q: %+v %v", id, r, err)
		}
	}
	if fake.Calls("verify") != 0 {
		t.Fatal("a malformed id reached NextPay")
	}
}

func TestUnknownTokenIsNotFound(t *testing.T) {
	g, _ := newGW(t)
	r, err := g.Check(context.Background(), "f7c07568-c6d1-4bee-87b1-4a9e5ed2e4c1", charge(100_000))
	if err != nil || r.State != gateway.Failed || r.Reason != "not_found" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestProviderDownAndTimeouts(t *testing.T) {
	g, fake := newGW(t)
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

func TestWrongKey(t *testing.T) {
	g, fake := newGW(t)
	fake.OnlyKey("00000000-0000-4000-8000-000000000000")
	_, err := g.Create(context.Background(), charge(100_000))
	var ne *nextpay.Error
	if !errors.As(err, &ne) || ne.Code != -33 {
		t.Fatalf("%v", err)
	}
	if _, err := g.Test(context.Background()); err == nil {
		t.Fatal("a wrong key passed the test")
	}
}

func TestTestIsReadOnly(t *testing.T) {
	g, fake := newGW(t)
	if _, err := g.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.Calls("token") != 0 {
		t.Fatal("the test created a payment")
	}
}
