package aqayepardakht_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/aqayepardakhtfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/aqayepardakht"
)

const pin = "Gm7QaV3xTz9LkR2WnD4P" //nolint:gosec // a fake pin for the test server

func newGW(t *testing.T) (*aqayepardakht.Gateway, *aqayepardakhtfake.Server) {
	t.Helper()
	fake := aqayepardakhtfake.New()
	t.Cleanup(fake.Close)
	g, err := aqayepardakht.New(aqayepardakht.Config{PIN: pin, BaseURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	return g, fake
}

func charge(rial int64) gateway.Charge {
	return gateway.Charge{IntentID: "intent-1", Amount: rial, Currency: "IRR", Description: "Monthly 50GB",
		CallbackURL: "https://shop.example.test/webhooks/aqayepardakht"}
}

// answers serves one fixed body for every call (a gateway that says something odd).
func answers(t *testing.T, code int, body string) *aqayepardakht.Gateway {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	g, err := aqayepardakht.New(aqayepardakht.Config{PIN: pin, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestConfig(t *testing.T) {
	for _, c := range []aqayepardakht.Config{
		{PIN: ""},
		{PIN: "abc"},                    // too short
		{PIN: "has space in it"},        // no spaces
		{PIN: strings.Repeat("a", 101)}, // too long
		{PIN: pin, Sandbox: true},       // the sandbox is the PIN "sandbox", not a switch
		{PIN: "sandbox"},                // ...and the PIN says so: the flag must agree
		{PIN: pin, Proxy: "socks5://1.2.3.4:1080"},
		{PIN: pin, Proxy: "::::"},
	} {
		if _, err := aqayepardakht.New(c); err == nil {
			t.Errorf("accepted %+v", c)
		}
	}
	if _, err := aqayepardakht.New(aqayepardakht.Config{PIN: " " + pin + " ", Proxy: "http://user:pw@10.0.0.2:3128"}); err != nil { //nolint:gosec // test fixture
		t.Fatalf("valid config: %v", err)
	}
	if _, err := aqayepardakht.New(aqayepardakht.Config{PIN: "sandbox", Sandbox: true}); err != nil {
		t.Fatalf("the sandbox: %v", err)
	}
}

func TestThePINKeepsItsCase(t *testing.T) {
	// A PIN is case-sensitive: the fake knows only this exact one, and the gateway sends it as it was given.
	g, fake := newGW(t)
	fake.OnlyPIN(pin)
	if _, err := g.Create(context.Background(), charge(1_500_000)); err != nil {
		t.Fatalf("the exact PIN: %v", err)
	}
	lower, _ := aqayepardakht.New(aqayepardakht.Config{PIN: strings.ToLower(pin), BaseURL: fake.URL})
	if _, err := lower.Create(context.Background(), charge(1_500_000)); err == nil {
		t.Fatal("a lowercased PIN was accepted")
	}
}

func TestCreateGivesTheRedirectURL(t *testing.T) {
	g, fake := newGW(t)
	st, err := g.Create(context.Background(), charge(1_500_000))
	if err != nil || !aqayepardakht.ValidID(st.ExternalID) || st.PayURL != g.PayURL(st.ExternalID) ||
		!strings.HasSuffix(st.PayURL, "/startpay/"+st.ExternalID) || strings.Contains(st.PayURL, "/sandbox/") {
		t.Fatalf("create: %+v %v", st, err)
	}
	if fake.Calls("create") != 1 {
		t.Fatalf("calls: %d", fake.Calls("create"))
	}
}

func TestCreateSendsTomanAndTheInvoice(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		if r.Header.Get("Content-Type") != "application/json" || r.URL.Path != "/api/v2/create" {
			t.Errorf("request: %s %s", r.Header.Get("Content-Type"), r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","transid":"abc123"}`))
	}))
	defer srv.Close()
	g, _ := aqayepardakht.New(aqayepardakht.Config{PIN: pin, BaseURL: srv.URL})
	if _, err := g.Create(context.Background(), charge(1_500_000)); err != nil {
		t.Fatal(err)
	}
	if got["amount"] != float64(150_000) || got["invoice_id"] != "intent-1" || got["pin"] != pin ||
		got["callback"] != "https://shop.example.test/webhooks/aqayepardakht" || got["callback_method"] != "GET" {
		t.Fatalf("body: %v", got)
	}
}

func TestSandboxUsesTheSandboxPage(t *testing.T) {
	fake := aqayepardakhtfake.New()
	defer fake.Close()
	g, err := aqayepardakht.New(aqayepardakht.Config{PIN: "sandbox", Sandbox: true, BaseURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	st, err := g.Create(context.Background(), charge(1_000_000))
	if err != nil || !strings.HasSuffix(st.PayURL, "/startpay/sandbox/"+st.ExternalID) {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestCreateRefusesWhatAqayepardakhtRefuses(t *testing.T) {
	g, fake := newGW(t)
	ctx := context.Background()
	noCB := charge(100_000)
	noCB.CallbackURL = ""
	longID := charge(100_000)
	longID.IntentID = strings.Repeat("a", 51)
	longCB := charge(100_000)
	longCB.CallbackURL = "https://shop.example.test/" + strings.Repeat("a", 500)
	toman := charge(100_000)
	toman.Currency = "IRT"
	for _, c := range []gateway.Charge{
		charge(9_990),         // 999 Toman: under the minimum
		charge(4_000_000_010), // over 400,000,000 Toman
		charge(1_000_005),     // not a whole number of Toman
		toman, noCB, longID, longCB,
	} {
		if _, err := g.Create(ctx, c); err == nil {
			t.Errorf("created %+v", c)
		}
	}
	if fake.Calls("create") != 0 {
		t.Fatal("a charge that cannot be valid reached the gateway")
	}
	// The limits themselves are fine.
	for _, rial := range []int64{10_000, 4_000_000_000} {
		if _, err := g.Create(ctx, charge(rial)); err != nil {
			t.Errorf("%d Rial: %v", rial, err)
		}
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
	cb, form, err := fake.Pay(st.ExternalID, true)
	if err != nil || !strings.Contains(cb, "transid="+st.ExternalID) || form.Get("status") != "1" || form.Get("invoice_id") != "intent-1" {
		t.Fatalf("callback %q %v %v", cb, form, err)
	}
	c.Returned = true
	r, err := g.Check(ctx, st.ExternalID, c)
	if err != nil || r.State != gateway.Paid || r.Amount != 1_500_000 || r.Currency != "IRR" {
		t.Fatalf("check: %+v %v", r, err)
	}
}

func TestVerifiedBeforeIsPaidAndNeverAnotherPayment(t *testing.T) {
	// A verify whose answer was lost: the next one says "verified before" (error 2). That is the same payment
	// paid, not another one: the caller settles once, in its database.
	g, fake := newGW(t)
	ctx := context.Background()
	st, _ := g.Create(ctx, charge(1_000_000))
	_, _, _ = fake.Pay(st.ExternalID, true)
	c := charge(1_000_000)
	c.Returned = true
	first, err1 := g.Check(ctx, st.ExternalID, c)
	second, err2 := g.Check(ctx, st.ExternalID, c)
	if err1 != nil || err2 != nil || first.State != gateway.Paid || second.State != gateway.Paid || second.Amount != 1_000_000 {
		t.Fatalf("%+v %v / %+v %v", first, err1, second, err2)
	}
	if fake.Calls("verify") != 2 {
		t.Fatalf("verify calls: %d", fake.Calls("verify"))
	}
}

func TestNegativeTwoIsAnErrorNotAPayment(t *testing.T) {
	// The error table's 2 is "the PIN is empty", its negative form -2: only the positive 2 of verify is
	// "verified before".
	g := answers(t, 422, `{"status":"error","code":"-2"}`)
	r, err := g.Check(context.Background(), "12345", charge(1_000_000))
	var ae *aqayepardakht.Error
	if !errors.As(err, &ae) || ae.Code != -2 || r.State == gateway.Paid || r.State == gateway.Failed {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestUnpaidAndCancelled(t *testing.T) {
	g, fake := newGW(t)
	ctx := context.Background()
	c := charge(100_000)
	st, _ := g.Create(ctx, c)
	_, form, _ := fake.Pay(st.ExternalID, false)
	if form.Get("status") != "0" || form.Get("cardnumber") != "" {
		t.Fatalf("a cancelled callback carries no card: %v", form)
	}
	// While the customer may still pay, "not completed" is pending...
	c.ExpiresAt = time.Now().Add(time.Hour)
	if r, err := g.Check(ctx, st.ExternalID, c); err != nil || r.State != gateway.Pending {
		t.Fatalf("within the window: %+v %v", r, err)
	}
	// ...and final once the customer came back, or the window ended.
	c.Returned = true
	if r, err := g.Check(ctx, st.ExternalID, c); err != nil || r.State != gateway.Failed || r.Reason != "gateway_failed" {
		t.Fatalf("after the return: %+v %v", r, err)
	}
	c.Returned, c.ExpiresAt = false, time.Now().Add(-time.Minute)
	if r, err := g.Check(ctx, st.ExternalID, c); err != nil || r.State != gateway.Failed {
		t.Fatalf("after the window: %+v %v", r, err)
	}
}

func TestAmountMismatchIsNeverPaid(t *testing.T) {
	// verify refuses an amount that differs from the payment's (error 10): an error to look at.
	g, fake := newGW(t)
	st, _ := g.Create(context.Background(), charge(1_000_000))
	_, _, _ = fake.Pay(st.ExternalID, true)
	c := charge(900_000)
	c.Returned = true
	r, err := g.Check(context.Background(), st.ExternalID, c)
	var ae *aqayepardakht.Error
	if !errors.As(err, &ae) || ae.Code != -10 || r.State == gateway.Paid || r.State == gateway.Failed {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSuccessNeedsStatusCodeAndHTTP200(t *testing.T) {
	for name, tc := range map[string]struct {
		code int
		body string
	}{
		"success without a code":  {200, `{"status":"success"}`},
		"success with code 0":     {200, `{"status":"success","code":"0"}`},
		"success with code 2":     {200, `{"status":"success","code":"2"}`},
		"success with a number 3": {200, `{"status":"success","code":3}`},
		"success but HTTP 201":    {201, `{"status":"success","code":"1"}`},
		"success but HTTP 422":    {422, `{"status":"success","code":"1"}`},
		"no status":               {200, `{"code":"1"}`},
		"status paid":             {200, `{"status":"paid","code":"1"}`},
		"an error page":           {200, `<html>oops</html>`},
		"empty":                   {200, ``},
		"an error without a code": {422, `{"status":"error"}`},
		"a server error":          {500, `{"status":"success","code":"1"}`},
		"a bad gateway":           {502, `<html>bad gateway</html>`},
	} {
		g := answers(t, tc.code, tc.body)
		c := charge(1_000_000)
		c.Returned = true
		r, err := g.Check(context.Background(), "12345", c)
		if r.State == gateway.Paid {
			t.Errorf("%s: settled on %q", name, tc.body)
		}
		if err == nil && r.State != gateway.Pending {
			t.Errorf("%s: neither an error nor pending: %+v", name, r)
		}
	}
	// A number is a code too (the documentation shows both forms).
	g := answers(t, 200, `{"status":"success","code":1}`)
	if r, err := g.Check(context.Background(), "12345", charge(1_000_000)); err != nil || r.State != gateway.Paid {
		t.Fatalf("code as a number: %+v %v", r, err)
	}
}

func TestCreateNeedsASuccessAndAnId(t *testing.T) {
	for _, body := range []string{`{}`, `<html>oops</html>`, `{"status":"success"}`, `{"status":"success","transid":""}`,
		`{"status":"success","transid":"../x"}`, `{"status":"success","transid":"a b"}`,
		`{"status":"success","transid":"` + strings.Repeat("a", 65) + `"}`, `{"status":"error","code":"-6"}`} {
		g := answers(t, 200, body)
		if st, err := g.Create(context.Background(), charge(1_000_000)); err == nil {
			t.Errorf("%s: created %+v", body, st)
		}
	}
	// A numeric transid is a transid.
	g := answers(t, 200, `{"status":"success","transid":482001}`)
	if st, err := g.Create(context.Background(), charge(1_000_000)); err != nil || st.ExternalID != "482001" {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestBadIDsNeverReachTheGateway(t *testing.T) {
	g, fake := newGW(t)
	for _, id := range []string{"", "../x", "a b", strings.Repeat("a", 65), "a/b", "a?b=c"} {
		r, err := g.Check(context.Background(), id, charge(100_000))
		if err != nil || r.State != gateway.Failed {
			t.Errorf("%q: %+v %v", id, r, err)
		}
	}
	if fake.Calls("verify") != 0 {
		t.Fatal("a malformed id reached the gateway")
	}
}

func TestAnAmountThatCannotBeThePaymentsNeverReachesVerify(t *testing.T) {
	g, fake := newGW(t)
	for _, rial := range []int64{0, 5, 9_990, 1_000_005, 4_000_000_010} {
		if r, err := g.Check(context.Background(), "12345", charge(rial)); err == nil || r.State == gateway.Paid {
			t.Errorf("%d Rial: %+v %v", rial, r, err)
		}
	}
	if fake.Calls("verify") != 0 {
		t.Fatal("verify was called with an amount that cannot be the payment's")
	}
}

func TestUnknownTransactionIsNotFound(t *testing.T) {
	g, _ := newGW(t)
	r, err := g.Check(context.Background(), "99999", charge(100_000))
	if err != nil || r.State != gateway.Failed || r.Reason != "not_found" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAPaymentOfAnotherPINIsNeverOurs(t *testing.T) {
	// A payment created with another PIN: verify answers error 9, which settles nothing.
	g, fake := newGW(t)
	other, _ := aqayepardakht.New(aqayepardakht.Config{PIN: "sandbox", Sandbox: true, BaseURL: fake.URL})
	st, _ := other.Create(context.Background(), charge(1_000_000))
	_, _, _ = fake.Pay(st.ExternalID, true)
	c := charge(1_000_000)
	c.Returned = true
	r, err := g.Check(context.Background(), st.ExternalID, c)
	var ae *aqayepardakht.Error
	if !errors.As(err, &ae) || ae.Code != -9 || r.State == gateway.Paid {
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

func TestErrorsNeverCarryThePIN(t *testing.T) {
	g, fake := newGW(t)
	fake.OnlyPIN("another-pin-entirely")
	_, err := g.Create(context.Background(), charge(100_000))
	var ae *aqayepardakht.Error
	if !errors.As(err, &ae) || ae.Code != -6 || strings.Contains(err.Error(), pin) {
		t.Fatalf("%v", err)
	}
	fake.Down(1)
	if _, err := g.Create(context.Background(), charge(100_000)); err == nil || strings.Contains(err.Error(), pin) {
		t.Fatalf("%v", err)
	}
}

func TestCallbackOnAnotherDomainIsRefused(t *testing.T) {
	g, fake := newGW(t)
	fake.OnlyDomain("pay.example.ir")
	_, err := g.Create(context.Background(), charge(100_000))
	var ae *aqayepardakht.Error
	if !errors.As(err, &ae) || ae.Code != -15 {
		t.Fatalf("%v", err)
	}
}

func TestRedirectsAreNeverFollowed(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	g, _ := aqayepardakht.New(aqayepardakht.Config{PIN: pin, BaseURL: srv.URL})
	if _, err := g.Create(context.Background(), charge(100_000)); err == nil || hit {
		t.Fatalf("a redirect was followed (hit=%v, err=%v)", hit, err)
	}
}

func TestTestChecksThePINAndIsReadOnly(t *testing.T) {
	g, fake := newGW(t)
	if _, err := g.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.Calls("create") != 0 {
		t.Fatal("the test created a payment")
	}
	fake.OnlyPIN("another-pin-entirely")
	_, err := g.Test(context.Background())
	var ae *aqayepardakht.Error
	if !errors.As(err, &ae) || ae.Code != -6 || !strings.Contains(err.Error(), "PIN") || strings.Contains(err.Error(), pin) {
		t.Fatalf("a wrong PIN passed the test, or the error is wrong: %v", err)
	}
	// An answer that is not an error answer is not a pass.
	odd := answers(t, 200, `{"status":"success","code":"1"}`)
	if _, err := odd.Test(context.Background()); err == nil {
		t.Fatal("an unexpected answer passed the test")
	}
}

func TestDescribeNamesCodesBothWays(t *testing.T) {
	for code, want := range map[int]string{-6: "PIN", 6: "PIN", -15: "callback", 15: "callback", -11: "pending approval", -16: "referrer", -14: "another site"} {
		if got := aqayepardakht.Describe(code); !strings.Contains(got, want) {
			t.Errorf("Describe(%d) = %q, want it to mention %q", code, got, want)
		}
	}
}

func TestTheGatewayIsAGatewayAndATester(t *testing.T) {
	var _ gateway.Gateway = (*aqayepardakht.Gateway)(nil)
	var _ gateway.Tester = (*aqayepardakht.Gateway)(nil)
}
