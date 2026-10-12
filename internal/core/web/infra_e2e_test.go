package web

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/paymentsclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/provisionerclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	paydomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/trongrid"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/zarinpal"
	payserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/server"
	paystore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	provserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/server"
	provstore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tronfake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/zpfake"
	"google.golang.org/grpc"
)

// End to end: the dashboard API over the real provisioner and payments
// services (gRPC with core's token) against the fake panel and the fake
// Zarinpal, sharing the test database as production shares Postgres.

const (
	e2eCoreToken = "e2e-core-token-0123456789abcdef0123"  //nolint:gosec // test fixture
	e2ePanelTok  = "e2e-panel-token-SECRET-0123456789"    //nolint:gosec // test fixture
	e2eKey       = "e2e-master-key-0123456789abcdef01234" //nolint:gosec // test fixture
)

// serveCore serves a gRPC server that accepts core only and dials it.
func serveCore(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	gs, err := grpcx.NewServer(slog.New(slog.DiscardHandler), grpcauth.Peer{Name: "core", Token: e2eCoreToken})
	if err != nil {
		t.Fatal(err)
	}
	register(gs)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	conn, err := grpcx.Dial(lis.Addr().String(), e2eCoreToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// realProvisioner runs the provisioner against a fake panel.
func realProvisioner(t *testing.T) (*provisionerclient.Client, *xuifake.Server, *provstore.Store) {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.DSN(t)
	if err := migrate.Up(ctx, dsn, "provisioner"); err != nil {
		t.Fatal(err)
	}
	env, err := bcrypto.NewEnvelope([]byte(e2eKey))
	if err != nil {
		t.Fatal(err)
	}
	vs, err := provstore.New(ctx, dsn, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vs.Close)
	if err := testdb.Truncate(ctx, vs.DB(), `provisioner.server_inbounds, provisioner.client_map, provisioner.xui_servers`); err != nil {
		t.Fatal(err)
	}
	panel := xuifake.NewServer(e2ePanelTok)
	t.Cleanup(panel.Close)
	panel.SetPanelVersion("v3.9.0")
	prov := provserver.New(vs, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	conn := serveCore(t, prov.Register)
	return provisionerclient.New(provisionerv1.NewProvisionerServiceClient(conn)), panel, vs
}

// realPayments runs payments with a master key against a fake Zarinpal that
// knows one merchant.
func realPayments(t *testing.T) (*paymentsclient.Client, *paystore.Store) {
	t.Helper()
	pay, ps, _ := startPayments(t, nil)
	return pay, ps
}

// startPayments runs payments as realPayments does; with chain, automatic
// USDT reads that fake TronGrid through the real client.
func startPayments(t *testing.T, chain *tronfake.Server) (*paymentsclient.Client, *paystore.Store, *paydomain.Service) {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.DSN(t)
	if err := migrate.Up(ctx, dsn, "payments"); err != nil {
		t.Fatal(err)
	}
	ps, err := paystore.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ps.Close)
	if _, err := ps.DB().Exec(ctx, `TRUNCATE payments.gateway_configs, payments.payment_intents, payments.outbox_payments,
		payments.outbox_payments_cursors, payments.chain_transfers, payments.chain_cursors, payments.gateway_events CASCADE`); err != nil {
		t.Fatal(err)
	}
	zp := zpfake.New()
	t.Cleanup(zp.Close)
	zp.OnlyMerchant(zpMerchant)
	envl, err := bcrypto.NewEnvelope([]byte(e2eKey))
	if err != nil {
		t.Fatal(err)
	}
	svc := paydomain.New(ps)
	svc.SetUnmatchedHold(0) // unmatched deposits are announced at once, not held for a manual proof
	opts := paydomain.GatewayOptions{Env: "prod", Envelope: envl,
		Build: func(z paydomain.ZarinpalSettings) (gateway.Gateway, error) {
			return zarinpal.New(zarinpal.Config{MerchantID: z.MerchantID, Sandbox: z.Sandbox, Proxy: z.Proxy, BaseURL: zp.URL})
		}}
	if chain != nil {
		opts.BuildChain = func(c paydomain.ChainSettings) (gateway.ChainSource, error) {
			cl, err := trongrid.New(trongrid.Config{APIKey: c.APIKey, Proxy: c.Proxy, BaseURL: chain.URL})
			if err != nil {
				return nil, err
			}
			return cl, nil
		}
	}
	if err := svc.ConfigureGateways(ctx, opts); err != nil {
		t.Fatal(err)
	}
	srv := payserver.New(svc, ps)
	srv.SetPublicURL("https://shop.example.test")
	conn := serveCore(t, srv.Register)
	return paymentsclient.New(paymentsv1.NewPaymentsServiceClient(conn)), ps, svc
}

// Servers through the real provisioner: added after a real connection test,
// listed without the token, chosen for a plan's services, and a new
// subscription address reaches the stored links.
func TestServersEndToEnd(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	prov, panel, vs := realProvisioner(t)
	f.api.cfg.Servers, f.api.cfg.Provisioner = prov, prov
	ctx := context.Background()

	body := map[string]any{"name": "Local", "base_url": panel.URL, "sub_base_url": "https://old.example/sub/",
		"token": "not-the-token", "weight": 100, "enabled": true, "allow_private": true, "skip_check": false}
	code, out := b.do(http.MethodPost, "/api/v1/servers", body)
	if code != http.StatusConflict || !strings.Contains(out["message"].(string), "connection test failed") || strings.Contains(asJSON(out), "not-the-token") {
		t.Fatalf("a wrong token: %d %v", code, out)
	}
	body["token"] = e2ePanelTok
	code, out = b.do(http.MethodPost, "/api/v1/servers", body)
	sv := item(out)
	if code != http.StatusCreated || sv["panel_version"] != "v3.9.0" || sv["healthy"] != true || sv["has_token"] != true {
		t.Fatalf("add: %d %v", code, out)
	}
	id := sv["id"].(string)
	code, out = b.do(http.MethodGet, "/api/v1/servers", nil)
	if code != 200 || len(items(out)) != 1 || strings.Contains(asJSON(out), e2ePanelTok) {
		t.Fatalf("list: %d %v", code, out)
	}
	var enc []byte
	if err := vs.DB().QueryRow(ctx, `SELECT api_token_enc FROM provisioner.xui_servers WHERE id = $1`, id).Scan(&enc); err != nil ||
		strings.Contains(string(enc), e2ePanelTok) {
		t.Fatalf("token at rest: %v", err)
	}

	// Inbounds come from the panel; the switch is kept by the provisioner.
	code, out = b.do(http.MethodGet, "/api/v1/servers/"+id+"/inbounds", nil)
	if code != 200 || len(items(out)) == 0 {
		t.Fatalf("inbounds: %d %v", code, out)
	}

	// A plan sends its services to this server.
	days, traffic := int32(30), int64(10<<30)
	p, err := f.dom.UpsertPlan(ctx, owner(t, f), &store.Plan{NameI18n: map[string]string{"en": "Monthly"}, Kind: "both",
		DurationDays: &days, TrafficBytes: &traffic, Price: 0, Currency: "IRT", Enabled: true, ServerIDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	u := f.customer(8901, "buyer")
	o, err := f.dom.CreateOrder(ctx, domain.CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: "e2e-order-1"})
	if err != nil || o.Status != "paid" {
		t.Fatalf("free order: %v %+v", err, o)
	}
	if worked, err := f.dom.NewProvisionWorker(prov, nil).ProvisionNext(ctx); !worked || err != nil {
		t.Fatalf("provision: %v %v", worked, err)
	}
	sub, err := f.st.SubscriptionByOrder(ctx, f.st.Conn(), o.ID)
	if err != nil || sub.ServerID != id || !strings.HasPrefix(sub.SubLink, "https://old.example/sub/") || sub.SubID == nil {
		t.Fatalf("delivered: %v %+v", err, sub)
	}

	// A new subscription address: the stored link follows.
	body["token"], body["sub_base_url"] = "", "https://new.example/sub/"
	if code, out := b.do(http.MethodPut, "/api/v1/servers/"+id, body); code != 200 || item(out)["sub_base_url"] != "https://new.example/sub/" {
		t.Fatalf("edit: %d %v", code, out)
	}
	if n, err := f.dom.NewSubLinkWorker(prov, prov, nil).RefreshStale(ctx); n != 1 || err != nil {
		t.Fatalf("refresh: %d %v", n, err)
	}
	got, _ := f.st.GetSubscription(ctx, f.st.Conn(), sub.ID)
	if got.SubLink != "https://new.example/sub/"+*sub.SubID {
		t.Fatalf("stored link: %q", got.SubLink)
	}

	// A plan naming only a server that does not exist cannot deliver.
	p.ServerIDs = []string{"00000000-0000-7000-8000-00000000dead"}
	if _, err := f.dom.UpsertPlan(ctx, owner(t, f), p); err != nil {
		t.Fatal(err)
	}
	o2, err := f.dom.CreateOrder(ctx, domain.CreateOrderParams{UserID: f.customer(8902, "b2").ID, PlanID: p.ID, IdempotencyKey: "e2e-order-2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.dom.NewProvisionWorker(prov, nil).ProvisionNext(ctx); err != nil {
		t.Fatal(err)
	}
	if o2, _ = f.st.GetOrder(ctx, f.st.Conn(), o2.ID); o2.Status != "provision_failed" {
		t.Fatalf("order for a missing server: %s", o2.Status)
	}

	// Deleting is refused while the service is on it.
	if code, out := b.do(http.MethodDelete, "/api/v1/servers/"+id, nil); code != http.StatusConflict || !strings.Contains(out["message"].(string), "1 clients") {
		t.Fatalf("delete with a client: %d %v", code, out)
	}
	// The overview reads the server list.
	_, out = b.do(http.MethodGet, "/api/v1/overview", nil)
	if servers, _ := out["servers"].([]any); len(servers) != 1 || out["panel"].(map[string]any)["healthy"] != true {
		t.Fatalf("overview: %v %v", out["servers"], out["panel"])
	}
	var audit string
	if err := f.st.DB().QueryRow(ctx, `SELECT string_agg(coalesce(before::text,'') || coalesce(after::text,''), ' ') FROM core.audit_log WHERE entity = 'server'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, e2ePanelTok) || !strings.Contains(audit, `"token": "changed"`) {
		t.Fatalf("audit: %s", audit)
	}
}

// A server added switched off through the real provisioner is written
// switched off by the insert itself: its row was never changed afterwards,
// so it never took new services, even for a moment.
func TestServerAddedSwitchedOffEndToEnd(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	prov, panel, vs := realProvisioner(t)
	f.api.cfg.Servers, f.api.cfg.Provisioner = prov, prov
	ctx := context.Background()

	code, out := b.do(http.MethodPost, "/api/v1/servers", map[string]any{"name": "Spare", "base_url": panel.URL,
		"token": e2ePanelTok, "weight": 100, "enabled": false, "allow_private": true, "skip_check": false})
	sv := item(out)
	if code != http.StatusCreated || sv["enabled"] != false || sv["healthy"] != true {
		t.Fatalf("add switched off: %d %v", code, out)
	}
	var enabled, untouched bool
	if err := vs.DB().QueryRow(ctx, `SELECT enabled, updated_at = created_at FROM provisioner.xui_servers WHERE id = $1`,
		sv["id"]).Scan(&enabled, &untouched); err != nil || enabled || !untouched {
		t.Fatalf("row: enabled %v, never updated %v, %v", enabled, untouched, err)
	}
}

// Gateways through the real payments service: the merchant id is saved
// encrypted and comes back as a hint, the read-only test reaches the fake
// Zarinpal, an open payment blocks a merchant change (409 with the count)
// but not switching off, and the report counts it.
func TestGatewaysEndToEnd(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	pay, ps := realPayments(t)
	f.api.cfg.Gateways, f.api.cfg.Payments, f.api.cfg.Env = pay, pay, "prod"
	ctx := context.Background()

	code, out := b.do(http.MethodGet, "/api/v1/gateways", nil)
	zp := out["providers"].([]any)[0].(map[string]any)
	if code != 200 || zp["configured"] != false || zp["key_missing"] != false {
		t.Fatalf("before: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true, "sandbox": true, "merchant_id": zpMerchant}); code != 400 || out["field"] != "sandbox" {
		t.Fatalf("sandbox in prod: %d %v", code, out)
	}
	code, out = b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true, "sandbox": false,
		"merchant_id": zpMerchant, "proxy": "", "clear_proxy": false})
	if code != 200 || strings.Contains(asJSON(out), zpMerchant) {
		t.Fatalf("save: %d %v", code, out)
	}
	_, out = b.do(http.MethodGet, "/api/v1/gateways", nil)
	zp = out["providers"].([]any)[0].(map[string]any)
	if zp["offered"] != true || zp["merchant_hint"] != "1a2b" || strings.Contains(asJSON(out), zpMerchant) {
		t.Fatalf("after: %v", zp)
	}
	var enc []byte
	if err := ps.DB().QueryRow(ctx, `SELECT merchant_enc FROM payments.gateway_configs WHERE provider = 'zarinpal'`).Scan(&enc); err != nil ||
		strings.Contains(string(enc), zpMerchant) {
		t.Fatalf("merchant at rest: %v", err)
	}

	// The read-only test: the stored merchant passes, another is refused.
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/zarinpal/test", map[string]any{}); code != 200 || out["ok"] != true {
		t.Fatalf("test stored: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/gateways/zarinpal/test", map[string]any{"sandbox": false,
		"merchant_id": "9f8e7d6c-5b4a-4321-8fed-cba987654321"}); code != 200 || out["ok"] != false || out["detail"] == "" {
		t.Fatalf("test another merchant: %d %v", code, out)
	}

	// A customer starts paying: the payment is open at Zarinpal.
	u := f.customer(8951, "payer")
	res, err := f.dom.CreatePaymentIntent(ctx, pay, domain.CreatePaymentIntentParams{UserID: u.ID, Provider: domain.Zarinpal,
		Amount: 150_000, Currency: "IRT", IdempotencyKey: "e2e-topup-1"})
	if err != nil || res.PayURL == "" {
		t.Fatalf("start: %v %+v", err, res)
	}
	code, out = b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": true, "merchant_id": "9f8e7d6c-5b4a-4321-8fed-cba987654321"})
	if code != http.StatusConflict || out["open"] != float64(1) {
		t.Fatalf("merchant change with an open payment: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/gateways/zarinpal", map[string]any{"enabled": false}); code != 200 {
		t.Fatalf("switching off with an open payment: %d", code)
	}
	if ms, err := f.dom.PaymentMethods(ctx, pay, "IRT", 150_000); err != nil || len(ms) != 0 {
		t.Fatalf("methods while switched off: %v %v", ms, err)
	}
	// A check of it failed: payments kept it, and the report passes it on.
	if _, err := ps.RecordCheckError(ctx, res.IntentID, "zarinpal", "zarinpal: request failed: timed out", time.Hour); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	code, out = b.do(http.MethodGet, "/api/v1/gateways/report?provider=zarinpal&from="+itoa(now-86400)+"&to="+itoa(now+60), nil)
	if code != 200 || out["open"] != float64(1) || !strings.Contains(asJSON(out["totals"]), `"status":"pending"`) {
		t.Fatalf("report: %d %v", code, out)
	}
	checkErrs, _ := out["check_errors"].([]any)
	if out["check_error_count"] != float64(1) || len(checkErrs) != 1 ||
		checkErrs[0].(map[string]any)["intent_id"] != res.IntentID || checkErrs[0].(map[string]any)["detail"] != "zarinpal: request failed: timed out" {
		t.Fatalf("report check errors: %v %v", out["check_errors"], out["check_error_count"])
	}
	var audit string
	if err := f.st.DB().QueryRow(ctx, `SELECT string_agg(coalesce(before::text,'') || coalesce(after::text,''), ' ') FROM core.audit_log WHERE entity = 'gateway'`).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(audit, zpMerchant) || !strings.Contains(audit, `"merchant_id": "changed"`) {
		t.Fatalf("audit: %s", audit)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
