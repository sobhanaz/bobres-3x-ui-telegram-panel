package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/idpayfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/nextpayfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/idpay"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/nextpay"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/web"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

const paidText = "پرداخت انجام شد"

type keyedFixture struct {
	svc  *domain.Service
	st   *store.Store
	idp  *idpayfake.Server
	np   *nextpayfake.Server
	site *httptest.Server
}

func newKeyedFixture(t *testing.T) *keyedFixture {
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
	f := &keyedFixture{st: st, idp: idpayfake.New(), np: nextpayfake.New()}
	t.Cleanup(f.idp.Close)
	t.Cleanup(f.np.Close)
	ip, err := idpay.New(idpay.Config{APIKey: "6a7f99eb-7c20-4412-a972-6dfb7cd253a4", BaseURL: f.idp.URL}) //nolint:gosec // a fake key for the test server
	if err != nil {
		t.Fatal(err)
	}
	np, err := nextpay.New(nextpay.Config{APIKey: "d1c2b3a4-5e6f-4789-8abc-0d1e2f3a4b5c", BaseURL: f.np.URL}) //nolint:gosec // a fake key for the test server
	if err != nil {
		t.Fatal(err)
	}
	f.svc = domain.New(st)
	f.svc.SetGateways(ip, np)
	mux := http.NewServeMux()
	web.New(f.svc, st, nil).Register(mux)
	f.site = httptest.NewServer(mux)
	t.Cleanup(f.site.Close)
	return f
}

func (f *keyedFixture) start(t *testing.T, provider, key string) *store.Intent {
	t.Helper()
	ga, gc := int64(1_500_000), "IRR"
	in, err := f.svc.StartGateway(context.Background(), &store.Intent{
		UserID: uid, Provider: provider, Amount: 150_000, Currency: "IRT", IdempotencyKey: key,
		GatewayAmount: &ga, GatewayCurrency: &gc,
	}, "Monthly", f.site.URL+"/webhooks/"+provider)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func (f *keyedFixture) status(t *testing.T, id string) string {
	t.Helper()
	in, err := f.st.GetIntent(context.Background(), nil, id)
	if err != nil {
		t.Fatal(err)
	}
	return in.Status
}

func postForm(t *testing.T, u string, form url.Values) (int, string) {
	t.Helper()
	resp, err := http.PostForm(u, form) //nolint:gosec,noctx // test server URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return resp.StatusCode, b.String()
}

// IDPay returns the customer by GET or by a form POST. Either way the page
// shows what the verify call says; a repeated callback settles nothing twice.
func TestIDPayReturn(t *testing.T) {
	f := newKeyedFixture(t)

	// GET, paid.
	in := f.start(t, "idpay", "i1")
	cb, err := f.idp.Pay(*in.ExternalID, true)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(cb)
	code, body, h := get(t, f.site.URL+"/webhooks/idpay?"+u.RawQuery)
	if code != 200 || !strings.Contains(body, paidText) || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("paid return: %d %s", code, body)
	}
	if f.status(t, in.ID) != "succeeded" {
		t.Fatal("the intent was not settled")
	}
	calls := f.idp.Calls("verify")
	for range 3 {
		if code, body, _ := get(t, f.site.URL+"/webhooks/idpay?"+u.RawQuery); code != 200 || !strings.Contains(body, paidText) {
			t.Fatalf("repeated callback: %d %s", code, body)
		}
	}
	if f.idp.Calls("verify") != calls {
		t.Fatalf("a settled payment was verified again: %d -> %d", calls, f.idp.Calls("verify"))
	}
	if n := ledgerEntries(t, f.st); n != 1 {
		t.Fatalf("%d ledger entries after a repeated callback, want 1", n)
	}

	// POST form, paid.
	in2 := f.start(t, "idpay", "i2")
	cb2, _ := f.idp.Pay(*in2.ExternalID, true)
	u2, _ := url.Parse(cb2)
	form := u2.Query()
	form.Set("amount", "1500000")
	form.Set("card_no", "603799******1234")
	if code, body := postForm(t, f.site.URL+"/webhooks/idpay", form); code != 200 || !strings.Contains(body, paidText) {
		t.Fatalf("POST return: %d %s", code, body)
	}
	if f.status(t, in2.ID) != "succeeded" {
		t.Fatal("the POST callback did not settle")
	}

	// The customer failed at the bank: never shown as paid.
	in3 := f.start(t, "idpay", "i3")
	cb3, _ := f.idp.Pay(*in3.ExternalID, false)
	u3, _ := url.Parse(cb3)
	if code, body, _ := get(t, f.site.URL+"/webhooks/idpay?"+u3.RawQuery); code != 200 || strings.Contains(body, paidText) {
		t.Fatalf("failed return: %d %s", code, body)
	}
	if got := f.status(t, in3.ID); got == "succeeded" {
		t.Fatal("a failed payment settled")
	}
}

// The callback is untrusted: a forged "paid" status settles nothing, and ids
// that are not ours are answered without asking anyone.
func TestIDPayReturnIsNotTrusted(t *testing.T) {
	f := newKeyedFixture(t)
	in := f.start(t, "idpay", "f1")
	forged := url.Values{"status": {"10"}, "id": {*in.ExternalID}, "order_id": {in.ID}, "track_id": {"1"}, "amount": {"1500000"}}
	code, body, _ := get(t, f.site.URL+"/webhooks/idpay?"+forged.Encode())
	if code != 200 || strings.Contains(body, paidText) {
		t.Fatalf("forged status: %d %s", code, body)
	}
	if got := f.status(t, in.ID); got != "pending" {
		t.Fatalf("a forged callback changed the intent to %q", got)
	}
	verifies := f.idp.Calls("verify") + f.idp.Calls("inquiry")
	for name, q := range map[string]url.Values{
		"unknown id":     {"id": {strings.Repeat("a", 32)}, "status": {"10"}},
		"malformed id":   {"id": {"../../etc/passwd"}},
		"no id":          {"status": {"10"}},
		"wrong order id": {"id": {*in.ExternalID}, "order_id": {"00000000-0000-7000-8000-00000000ffff"}, "status": {"10"}},
	} {
		if code, _, _ := get(t, f.site.URL+"/webhooks/idpay?"+q.Encode()); code != 404 {
			t.Errorf("%s: %d, want 404", name, code)
		}
	}
	if f.idp.Calls("verify")+f.idp.Calls("inquiry") != verifies {
		t.Fatal("a callback that is not ours reached the gateway")
	}
}

// An oversized callback body is refused; a flood is limited.
func TestCallbackBodyCapAndRateLimit(t *testing.T) {
	f := newKeyedFixture(t)
	big := url.Values{"id": {strings.Repeat("a", 64<<10)}}
	if code, _ := postForm(t, f.site.URL+"/webhooks/idpay", big); code != http.StatusBadRequest {
		t.Fatalf("a 64 KiB callback body: %d, want 400", code)
	}
	if code, _ := postForm(t, f.site.URL+"/webhooks/nextpay", big); code != http.StatusBadRequest {
		t.Fatalf("a 64 KiB callback body: %d, want 400", code)
	}
	limited := 0
	for range 200 {
		if code, _, _ := get(t, f.site.URL+"/webhooks/nextpay?trans_id=x"); code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("200 quick callbacks were never limited")
	}
}

func TestNextPayReturn(t *testing.T) {
	f := newKeyedFixture(t)
	in := f.start(t, "nextpay", "n1")
	cb, err := f.np.Pay(*in.ExternalID, true)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(cb)
	code, body, _ := get(t, f.site.URL+"/webhooks/nextpay?"+u.RawQuery)
	if code != 200 || !strings.Contains(body, paidText) {
		t.Fatalf("paid return: %d %s", code, body)
	}
	if f.status(t, in.ID) != "succeeded" {
		t.Fatal("the intent was not settled")
	}
	// NextPay answers a second verify with an error: the repeated callback must
	// not ask again, and must not undo the payment.
	calls := f.np.Calls("verify")
	for range 3 {
		if code, body, _ := get(t, f.site.URL+"/webhooks/nextpay?"+u.RawQuery); code != 200 || !strings.Contains(body, paidText) {
			t.Fatalf("repeated callback: %d %s", code, body)
		}
	}
	if f.np.Calls("verify") != calls {
		t.Fatalf("a settled payment was verified again: %d -> %d", calls, f.np.Calls("verify"))
	}
	if n := ledgerEntries(t, f.st); n != 1 {
		t.Fatalf("%d ledger entries, want 1", n)
	}

	// Rejected at the bank.
	bad := f.start(t, "nextpay", "n2")
	cb2, _ := f.np.Pay(*bad.ExternalID, false)
	u2, _ := url.Parse(cb2)
	if code, body, _ := get(t, f.site.URL+"/webhooks/nextpay?"+u2.RawQuery); code != 200 || strings.Contains(body, paidText) {
		t.Fatalf("rejected return: %d %s", code, body)
	}
	if f.status(t, bad.ID) == "succeeded" {
		t.Fatal("a rejected payment settled")
	}

	// The gateway is down while the customer returns: nothing settles, the page
	// is not an error, and the reconciler can settle it later.
	late := f.start(t, "nextpay", "n3")
	if _, err := f.np.Pay(*late.ExternalID, true); err != nil {
		t.Fatal(err)
	}
	f.np.Down(1000)
	code, body, _ = get(t, f.site.URL+"/webhooks/nextpay?trans_id="+*late.ExternalID)
	if code != 200 || strings.Contains(body, paidText) || f.status(t, late.ID) != "pending" {
		t.Fatalf("return while down: %d %s (%s)", code, body, f.status(t, late.ID))
	}
	f.np.Down(0)
	if _, err := f.st.DB().Exec(context.Background(), `UPDATE payments.payment_intents SET checked_at = NULL WHERE id = $1`, late.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CheckAfterReturn(context.Background(), late.ID); err != nil || f.status(t, late.ID) != "succeeded" {
		t.Fatalf("settling after the outage: %v %s", err, f.status(t, late.ID))
	}
}

func ledgerEntries(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(context.Background(), `SELECT count(*) FROM payments.ledger_entries`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
