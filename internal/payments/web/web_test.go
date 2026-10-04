package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/zarinpal"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/web"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/zpfake"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const uid = "00000000-0000-7000-8000-00000000e001"

type fixture struct {
	svc  *domain.Service
	st   *store.Store
	fake *zpfake.Server
	site *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := testdb.DSN(t)
	ctx := context.Background()
	if err := migrate.Up(ctx, dsn, "payments"); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB().Exec(ctx, `TRUNCATE payments.payment_intents, payments.outbox_payments, payments.ledger_entries CASCADE`); err != nil {
		t.Fatal(err)
	}
	fake := zpfake.New()
	t.Cleanup(fake.Close)
	zp, err := zarinpal.New(zarinpal.Config{MerchantID: "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b", BaseURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	svc := domain.New(st)
	svc.SetGateways(zp)
	mux := http.NewServeMux()
	web.New(svc, st, nil).Register(mux)
	site := httptest.NewServer(mux)
	t.Cleanup(site.Close)
	return &fixture{svc: svc, st: st, fake: fake, site: site}
}

func (f *fixture) start(t *testing.T, key string) *store.Intent {
	t.Helper()
	ga, gc := int64(1_500_000), "IRR"
	in, err := f.svc.StartGateway(context.Background(), &store.Intent{
		UserID: uid, Provider: "zarinpal", Amount: 150_000, Currency: "IRT", IdempotencyKey: key,
		GatewayAmount: &ga, GatewayCurrency: &gc,
	}, "Monthly", f.site.URL+"/webhooks/zarinpal")
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func get(t *testing.T, u string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(u) //nolint:gosec,noctx // test server URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestPayPage(t *testing.T) {
	f := newFixture(t)
	in := f.start(t, "w1")
	code, body, h := get(t, f.site.URL+"/pay/"+in.ID)
	if code != 200 || !strings.Contains(body, *in.PayURL) || !strings.Contains(body, "۱۵۰٬۰۰۰ تومان") || !strings.Contains(body, "VPN") {
		t.Fatalf("pay page: %d %s", code, body)
	}
	if h.Get("Referrer-Policy") != "origin" || !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("headers: %v", h)
	}
	for _, path := range []string{"/pay/not-an-id", "/pay/00000000-0000-7000-8000-00000000ffff"} {
		if code, _, _ := get(t, f.site.URL+path); code != 404 {
			t.Errorf("%s: %d", path, code)
		}
	}
}

func TestReturnVerifiesAndSettles(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// The customer paid: the return verifies with Zarinpal and settles.
	paid := f.start(t, "w2")
	cb, err := f.fake.Pay(*paid.ExternalID, true)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(cb)
	code, body, _ := get(t, f.site.URL+"/webhooks/zarinpal?"+u.RawQuery)
	if code != 200 || !strings.Contains(body, "پرداخت انجام شد") {
		t.Fatalf("paid return: %d %s", code, body)
	}
	if got, _ := f.st.GetIntent(ctx, nil, paid.ID); got.Status != "succeeded" || got.ProviderRef == nil {
		t.Fatalf("intent after return: %+v", got)
	}

	// A forged Status=NOK cannot cancel a paid payment: the return verifies.
	paid2 := f.start(t, "w3")
	_, _ = f.fake.Pay(*paid2.ExternalID, true)
	_, body, _ = get(t, f.site.URL+"/webhooks/zarinpal?Authority="+*paid2.ExternalID+"&Status=NOK")
	if got, _ := f.st.GetIntent(ctx, nil, paid2.ID); got.Status != "succeeded" || !strings.Contains(body, "پرداخت انجام شد") {
		t.Fatalf("forged NOK on a paid payment: %+v", got)
	}

	// A real cancellation fails the intent (Zarinpal says FAILED).
	gone := f.start(t, "w4")
	cb, _ = f.fake.Pay(*gone.ExternalID, false)
	u, _ = url.Parse(cb)
	_, body, _ = get(t, f.site.URL+"/webhooks/zarinpal?"+u.RawQuery)
	if got, _ := f.st.GetIntent(ctx, nil, gone.ID); got.Status != "failed" || !strings.Contains(body, "ناموفق") {
		t.Fatalf("cancelled: %+v %s", got, body)
	}

	// Refreshing the return page does not hammer Zarinpal.
	before := f.fake.Calls("verify")
	open := f.start(t, "w5")
	for i := 0; i < 5; i++ {
		get(t, f.site.URL+"/webhooks/zarinpal?Authority="+*open.ExternalID+"&Status=OK")
	}
	if n := f.fake.Calls("verify") - before; n != 1 {
		t.Fatalf("verify calls for 5 refreshes: %d", n)
	}
	if code, _, _ := get(t, f.site.URL+"/webhooks/zarinpal?Authority=S"+strings.Repeat("0", 34)+"9&Status=OK"); code != 404 {
		t.Fatalf("unknown authority: %d", code)
	}
}
