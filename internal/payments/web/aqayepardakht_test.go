package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/aqayepardakhtfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/aqayepardakht"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/web"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

type aqFixture struct {
	svc  *domain.Service
	st   *store.Store
	fake *aqayepardakhtfake.Server
	site *httptest.Server
}

func newAqFixture(t *testing.T) *aqFixture {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "payments"); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(ctx, testdb.WithPoolCap(t, dsn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB().Exec(ctx, `TRUNCATE payments.payment_intents, payments.outbox_payments, payments.ledger_entries CASCADE`); err != nil {
		t.Fatal(err)
	}
	f := &aqFixture{st: st, fake: aqayepardakhtfake.New()}
	t.Cleanup(f.fake.Close)
	g, err := aqayepardakht.New(aqayepardakht.Config{PIN: "Gm7QaV3xTz9LkR2WnD4P", BaseURL: f.fake.URL}) //nolint:gosec // a fake PIN for the test server
	if err != nil {
		t.Fatal(err)
	}
	f.svc = domain.New(st)
	f.svc.SetGateways(g)
	mux := http.NewServeMux()
	web.New(f.svc, st, nil).Register(mux)
	f.site = httptest.NewServer(mux)
	t.Cleanup(f.site.Close)
	return f
}

func (f *aqFixture) start(t *testing.T, key string) *store.Intent {
	t.Helper()
	ga, gc := int64(1_500_000), "IRR"
	in, err := f.svc.StartGateway(context.Background(), &store.Intent{
		UserID: uid, Provider: "aqayepardakht", Amount: 150_000, Currency: "IRT", IdempotencyKey: key,
		GatewayAmount: &ga, GatewayCurrency: &gc,
	}, "Monthly", f.site.URL+"/webhooks/aqayepardakht")
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func (f *aqFixture) status(t *testing.T, id string) string {
	t.Helper()
	in, err := f.st.GetIntent(context.Background(), nil, id)
	if err != nil {
		t.Fatal(err)
	}
	return in.Status
}

// The pay page: Aqayepardakht refuses a payment that does not start from the registered domain, so the
// customer is sent to a page of ours that links to the gateway with the Referer an origin carries.
func TestAqayepardakhtPayPage(t *testing.T) {
	f := newAqFixture(t)
	in := f.start(t, "aqw-1")
	code, body, h := get(t, f.site.URL+"/pay/"+in.ID)
	if code != 200 || !strings.Contains(body, *in.PayURL) || !strings.Contains(body, "۱۵۰٬۰۰۰ تومان") ||
		!strings.Contains(body, "Pay with Aqayepardakht") || !strings.Contains(body, "آقای\u200cپرداخت") || strings.Contains(body, "زرین") {
		t.Fatalf("pay page: %d %s", code, body)
	}
	if !strings.Contains(body, `referrerpolicy="origin"`) || h.Get("Referrer-Policy") != "origin" {
		t.Fatalf("the page must send the origin as the Referer: %v", h)
	}
	if !strings.Contains(*in.PayURL, "/startpay/"+*in.ExternalID) {
		t.Fatalf("pay URL: %s", *in.PayURL)
	}
	// Once the customer paid, the page shows the result, not the link.
	_, _, _ = f.fake.Pay(*in.ExternalID, true)
	if _, err := f.svc.CheckAfterReturn(context.Background(), in.ID); err != nil {
		t.Fatal(err)
	}
	if _, body, _ := get(t, f.site.URL+"/pay/"+in.ID); strings.Contains(body, *in.PayURL) || !strings.Contains(body, paidText) {
		t.Fatalf("pay page of a paid payment: %s", body)
	}
}

// Aqayepardakht returns the customer by GET (as asked) or by a form POST. Either way the page shows what the
// verify call says; a repeated callback settles nothing twice.
func TestAqayepardakhtReturn(t *testing.T) {
	f := newAqFixture(t)

	// GET, paid.
	in := f.start(t, "aqr-1")
	cb, _, err := f.fake.Pay(*in.ExternalID, true)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(cb)
	code, body, h := get(t, f.site.URL+"/webhooks/aqayepardakht?"+u.RawQuery)
	if code != 200 || !strings.Contains(body, paidText) || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("paid return: %d %s", code, body)
	}
	if f.status(t, in.ID) != "succeeded" {
		t.Fatal("the intent was not settled")
	}
	calls := f.fake.Calls("verify")
	for range 3 {
		if code, body, _ := get(t, f.site.URL+"/webhooks/aqayepardakht?"+u.RawQuery); code != 200 || !strings.Contains(body, paidText) {
			t.Fatalf("repeated callback: %d %s", code, body)
		}
	}
	if f.fake.Calls("verify") != calls {
		t.Fatalf("a settled payment was verified again: %d -> %d", calls, f.fake.Calls("verify"))
	}
	if n := ledgerEntries(t, f.st); n != 1 {
		t.Fatalf("%d ledger entries after a repeated callback, want 1", n)
	}

	// POST form (the gateway's default), paid.
	in2 := f.start(t, "aqr-2")
	_, form, _ := f.fake.Pay(*in2.ExternalID, true)
	if code, body := postForm(t, f.site.URL+"/webhooks/aqayepardakht", form); code != 200 || !strings.Contains(body, paidText) {
		t.Fatalf("POST return: %d %s", code, body)
	}
	if f.status(t, in2.ID) != "succeeded" {
		t.Fatal("the POST callback did not settle")
	}

	// The customer gave up at the bank: never shown as paid.
	in3 := f.start(t, "aqr-3")
	cb3, _, _ := f.fake.Pay(*in3.ExternalID, false)
	u3, _ := url.Parse(cb3)
	if code, body, _ := get(t, f.site.URL+"/webhooks/aqayepardakht?"+u3.RawQuery); code != 200 || strings.Contains(body, paidText) {
		t.Fatalf("cancelled return: %d %s", code, body)
	}
	if got := f.status(t, in3.ID); got == "succeeded" {
		t.Fatal("a cancelled payment settled")
	}
}

// The callback is untrusted: a forged "paid" status settles nothing, a forged "failed" one cancels nothing
// that was paid, and ids that are not ours are answered without asking anyone.
func TestAqayepardakhtReturnIsNotTrusted(t *testing.T) {
	f := newAqFixture(t)
	in := f.start(t, "aqf-1")
	forged := url.Values{"status": {"1"}, "transid": {*in.ExternalID}, "invoice_id": {in.ID}, "tracking_number": {"1"}, "cardnumber": {"6037-99**-****-1234"}}
	code, body, _ := get(t, f.site.URL+"/webhooks/aqayepardakht?"+forged.Encode())
	if code != 200 || strings.Contains(body, paidText) {
		t.Fatalf("forged status: %d %s", code, body)
	}
	if got := f.status(t, in.ID); got == "succeeded" {
		t.Fatalf("a forged callback settled the intent (%q)", got)
	}

	// A real payment whose callback says "not paid": the verify decides, and it is paid.
	paid := f.start(t, "aqf-2")
	_, _, _ = f.fake.Pay(*paid.ExternalID, true)
	lying := url.Values{"status": {"0"}, "transid": {*paid.ExternalID}, "invoice_id": {paid.ID}}
	if _, body, _ := get(t, f.site.URL+"/webhooks/aqayepardakht?"+lying.Encode()); !strings.Contains(body, paidText) || f.status(t, paid.ID) != "succeeded" {
		t.Fatalf("a callback that denies a paid payment: %s (%s)", body, f.status(t, paid.ID))
	}

	// Another payment's invoice id with this payment's transid, an unknown transid, a malformed one, none.
	other := f.start(t, "aqf-3")
	verifies := f.fake.Calls("verify")
	for name, q := range map[string]url.Values{
		"another payment's invoice id": {"transid": {*other.ExternalID}, "invoice_id": {in.ID}, "status": {"1"}},
		"unknown transid":              {"transid": {"99999999"}, "status": {"1"}},
		"malformed transid":            {"transid": {"../../etc/passwd"}},
		"no transid":                   {"status": {"1"}},
		"a long transid":               {"transid": {strings.Repeat("a", 65)}},
	} {
		if code, _, _ := get(t, f.site.URL+"/webhooks/aqayepardakht?"+q.Encode()); code != 404 {
			t.Errorf("%s: %d, want 404", name, code)
		}
	}
	if f.fake.Calls("verify") != verifies {
		t.Fatal("a callback that is not ours reached the gateway")
	}
	if got := f.status(t, other.ID); got != "pending" {
		t.Fatalf("a refused callback changed the other payment to %q", got)
	}
}

// An oversized callback body is refused, and a flood is limited.
func TestAqayepardakhtCallbackBodyCapAndRateLimit(t *testing.T) {
	f := newAqFixture(t)
	big := url.Values{"transid": {strings.Repeat("a", 64<<10)}}
	if code, _ := postForm(t, f.site.URL+"/webhooks/aqayepardakht", big); code != http.StatusBadRequest {
		t.Fatalf("a 64 KiB callback body: %d, want 400", code)
	}
	limited := 0
	for range 200 {
		if code, _, _ := get(t, f.site.URL+"/webhooks/aqayepardakht?transid=x"); code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("200 quick callbacks were never limited")
	}
}

// The gateway is down while the customer returns: nothing settles, the page is not an error, and the
// reconciler or the next check settles it later.
func TestAqayepardakhtReturnWhileTheGatewayIsDown(t *testing.T) {
	f := newAqFixture(t)
	late := f.start(t, "aqd-1")
	_, _, _ = f.fake.Pay(*late.ExternalID, true)
	f.fake.Down(1000)
	code, body, _ := get(t, f.site.URL+"/webhooks/aqayepardakht?transid="+*late.ExternalID+"&status=1")
	if code != 200 || strings.Contains(body, paidText) || f.status(t, late.ID) != "pending" {
		t.Fatalf("return while down: %d %s (%s)", code, body, f.status(t, late.ID))
	}
	f.fake.Down(0)
	if _, err := f.st.DB().Exec(context.Background(), `UPDATE payments.payment_intents SET checked_at = NULL WHERE id = $1`, late.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CheckAfterReturn(context.Background(), late.ID); err != nil || f.status(t, late.ID) != "succeeded" {
		t.Fatalf("settling after the outage: %v %s", err, f.status(t, late.ID))
	}
}
