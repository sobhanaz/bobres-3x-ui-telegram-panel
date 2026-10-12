package server_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/zarinpal"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/zpfake"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// Test fixtures, not secrets.
const (
	coreToken = "payments-test-core-token-0123456789"   //nolint:gosec // test fixture
	botToken  = "payments-test-bot-token-01234567890"   //nolint:gosec // test fixture
	masterKey = "payments-test-master-key-0123456789ab" //nolint:gosec // test fixture
	merchant  = "0b7e2f3a-1c9d-4e5f-8a6b-7c8d9e0f1a2b"
	merchant2 = "9f8e7d6c-5b4a-4321-8fed-cba987654321"
	proxy     = "http://relay:pr0xy-pw@127.0.0.1:1" //nolint:gosec // test fixture
	staff     = "00000000-0000-7000-8000-00000000f0a1"
	uid       = "00000000-0000-7000-8000-00000000f001"
)

type fixture struct {
	core, bot paymentsv1.PaymentsServiceClient
	svc       *domain.Service
	st        *store.Store
	fake      *zpfake.Server
}

// start serves payments over gRPC, accepting core only, with Zarinpal pointed
// at the fake. key "" runs without a master key.
func start(t *testing.T, key string, seed domain.ZarinpalSettings) *fixture {
	t.Helper()
	ctx := context.Background()
	dsn := testdb.DSN(t)
	if err := migrate.Up(ctx, dsn, "payments"); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if _, err := st.DB().Exec(ctx, `TRUNCATE payments.gateway_configs, payments.payment_intents, payments.outbox_payments CASCADE`); err != nil {
		t.Fatal(err)
	}
	fake := zpfake.New()
	t.Cleanup(fake.Close)
	fake.OnlyMerchant(merchant)
	o := domain.GatewayOptions{Env: "prod", Seed: seed, Build: func(z domain.ZarinpalSettings) (gateway.Gateway, error) {
		g, err := zarinpal.New(zarinpal.Config{MerchantID: z.MerchantID, Sandbox: z.Sandbox, Proxy: z.Proxy, BaseURL: fake.URL})
		if err != nil {
			return nil, err
		}
		return g, nil
	}}
	if key != "" {
		if o.Envelope, err = bcrypto.NewEnvelope([]byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	svc := domain.New(st)
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	gs, err := grpcx.NewServer(slog.New(slog.DiscardHandler), grpcauth.Peer{Name: "core", Token: coreToken})
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(svc, st)
	srv.SetPublicURL("https://shop.example.test")
	srv.Register(gs)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	dial := func(token string) paymentsv1.PaymentsServiceClient {
		c, err := grpcx.Dial(lis.Addr().String(), token)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return paymentsv1.NewPaymentsServiceClient(c)
	}
	return &fixture{core: dial(coreToken), bot: dial(botToken), svc: svc, st: st, fake: fake}
}

// noSecrets fails when a response or error carries the merchant id or proxy.
func noSecrets(t *testing.T, where string, m proto.Message, err error) {
	t.Helper()
	var text string
	if m != nil {
		text = protojson.Format(m)
	}
	if err != nil {
		text += err.Error()
	}
	for _, s := range []string{merchant, merchant2, proxy, "pr0xy-pw", "127.0.0.1:1"} {
		if strings.Contains(text, s) {
			t.Errorf("%s returns a secret (%q): %s", where, s, text)
		}
	}
}

// Only core reaches the gateway settings, like every payments call.
func TestGatewayRPCsAcceptOnlyCore(t *testing.T) {
	f := start(t, masterKey, domain.ZarinpalSettings{})
	ctx := context.Background()
	calls := map[string]func(paymentsv1.PaymentsServiceClient) error{
		"ListGatewayConfigs": func(c paymentsv1.PaymentsServiceClient) error {
			_, err := c.ListGatewayConfigs(ctx, &paymentsv1.ListGatewayConfigsRequest{})
			return err
		},
		"SetGatewayConfig": func(c paymentsv1.PaymentsServiceClient) error {
			_, err := c.SetGatewayConfig(ctx, &paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal", MerchantId: merchant2})
			return err
		},
		"TestGateway": func(c paymentsv1.PaymentsServiceClient) error {
			_, err := c.TestGateway(ctx, &paymentsv1.TestGatewayRequest{Provider: "zarinpal", Candidate: &paymentsv1.GatewayCandidate{MerchantId: merchant}})
			return err
		},
		"GatewayReport": func(c paymentsv1.PaymentsServiceClient) error {
			_, err := c.GatewayReport(ctx, &paymentsv1.GatewayReportRequest{Provider: "zarinpal"})
			return err
		},
	}
	for name, call := range calls {
		if err := call(f.bot); status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s with the bot's token: %v", name, err)
		}
	}
	if _, err := f.st.GetGatewayConfig(ctx, nil, "zarinpal"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("the bot changed the settings")
	}
	for name, call := range calls {
		if err := call(f.core); err != nil {
			t.Errorf("%s with core's token: %v", name, err)
		}
	}
}

func TestGatewayConfigRPCs(t *testing.T) {
	f := start(t, masterKey, domain.ZarinpalSettings{MerchantID: merchant, Proxy: proxy})
	ctx := context.Background()
	list, err := f.core.ListGatewayConfigs(ctx, &paymentsv1.ListGatewayConfigsRequest{})
	noSecrets(t, "ListGatewayConfigs", list, err)
	if err != nil || len(list.GetConfigs()) != 2 {
		t.Fatalf("list: %v %v", list, err)
	}
	// Zarinpal first, then automatic USDT (not set up here).
	if u := list.GetConfigs()[1]; u.GetProvider() != "usdt_trc20_auto" || u.GetEnabled() || u.GetConfigured() || u.GetAddress() != "" {
		t.Fatalf("automatic USDT config: %v", u)
	}
	c := list.GetConfigs()[0]
	if c.GetProvider() != "zarinpal" || !c.GetEnabled() || !c.GetConfigured() || !c.GetOffered() || c.GetMerchantHint() != "1a2b" ||
		!c.GetProxySet() || c.GetKeyMissing() || c.GetFromEnv() || c.GetProblem() != "" || c.GetUpdatedAt() == 0 || c.GetUpdatedBy() != "" {
		t.Fatalf("seeded config: %v", c)
	}
	if gw, _ := f.core.ListGateways(ctx, &paymentsv1.ListGatewaysRequest{}); len(gw.GetProviders()) != 2 {
		t.Fatalf("offered: %v", gw.GetProviders())
	}

	// Switch off, keeping the secrets; the actor is recorded.
	got, err := f.core.SetGatewayConfig(ctx, &paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal", ActorId: staff})
	noSecrets(t, "SetGatewayConfig", got, err)
	if err != nil || got.GetEnabled() || got.GetOffered() || !got.GetConfigured() || got.GetMerchantHint() != "1a2b" || !got.GetProxySet() || got.GetUpdatedBy() != staff {
		t.Fatalf("switch off: %v %v", got, err)
	}
	if gw, _ := f.core.ListGateways(ctx, &paymentsv1.ListGatewaysRequest{}); len(gw.GetProviders()) != 1 || gw.GetProviders()[0] != "stars" {
		t.Fatalf("still offered: %v", gw.GetProviders())
	}

	// Errors map to codes, and never echo the secrets sent.
	for name, c := range map[string]struct {
		req  *paymentsv1.SetGatewayConfigRequest
		code codes.Code
	}{
		"sandbox in prod":  {&paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal", Sandbox: true}, codes.InvalidArgument},
		"bad proxy":        {&paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal", Proxy: "socks5://relay:pr0xy-pw@127.0.0.1:1"}, codes.InvalidArgument},
		"proxy and clear":  {&paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal", Proxy: proxy, ClearProxy: true}, codes.InvalidArgument},
		"unknown provider": {&paymentsv1.SetGatewayConfigRequest{Provider: "stars"}, codes.InvalidArgument},
		"actor not an id":  {&paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal", ActorId: "owner"}, codes.InvalidArgument},
		"bad merchant id":  {&paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal", MerchantId: "1234"}, codes.InvalidArgument},
	} {
		_, err := f.core.SetGatewayConfig(ctx, c.req)
		noSecrets(t, name, nil, err)
		if status.Code(err) != c.code {
			t.Errorf("%s: %v", name, err)
		}
	}

	// An open Zarinpal payment blocks a merchant change, with the count.
	if _, err := f.core.SetGatewayConfig(ctx, &paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	authority := f.fake.AddPayment(1_500_000, "IN_BANK")
	if _, err := f.st.DB().Exec(ctx, `INSERT INTO payments.payment_intents
		(id, user_id, provider, amount, currency, status, idempotency_key, gateway_amount, gateway_currency, external_id)
		VALUES ('00000000-0000-7000-8000-00000000f0b1', $1, 'zarinpal', 150000, 'IRT', 'pending', 'open-1', 1500000, 'IRR', $2)`,
		uid, authority); err != nil {
		t.Fatal(err)
	}
	_, err = f.core.SetGatewayConfig(ctx, &paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal", Enabled: true, MerchantId: merchant2})
	noSecrets(t, "open payments", nil, err)
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "1 open zarinpal payments") {
		t.Fatalf("merchant change with an open payment: %v", err)
	}
	if c := f.svc.GatewayConfigs()[0]; c.MerchantHint != "1a2b" {
		t.Fatal("the refused change was applied")
	}
}

func TestGatewayConfigWithoutKey(t *testing.T) {
	f := start(t, "", domain.ZarinpalSettings{MerchantID: merchant})
	ctx := context.Background()
	list, err := f.core.ListGatewayConfigs(ctx, &paymentsv1.ListGatewayConfigsRequest{})
	if err != nil || !list.GetConfigs()[0].GetKeyMissing() || !list.GetConfigs()[0].GetFromEnv() || !list.GetConfigs()[0].GetOffered() {
		t.Fatalf("read-only from the environment: %v %v", list, err)
	}
	_, err = f.core.SetGatewayConfig(ctx, &paymentsv1.SetGatewayConfigRequest{Provider: "zarinpal"})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "bobres install") {
		t.Fatalf("changed without a key: %v", err)
	}
}

func TestTestGatewayRPC(t *testing.T) {
	f := start(t, masterKey, domain.ZarinpalSettings{MerchantID: merchant})
	ctx := context.Background()
	f.fake.AddPayment(2_000_000, "PAID")
	r, err := f.core.TestGateway(ctx, &paymentsv1.TestGatewayRequest{Provider: "zarinpal"})
	if err != nil || !r.GetOk() || r.GetUnverified() != 1 || r.GetDetail() != "" || r.GetLatencyMs() < 0 {
		t.Fatalf("stored settings: %v %v", r, err)
	}
	r, err = f.core.TestGateway(ctx, &paymentsv1.TestGatewayRequest{Provider: "zarinpal", Candidate: &paymentsv1.GatewayCandidate{MerchantId: merchant2}})
	noSecrets(t, "TestGateway", r, err)
	if err != nil || r.GetOk() || !strings.Contains(r.GetDetail(), "-10") {
		t.Fatalf("refused merchant: %v %v", r, err)
	}
	r, err = f.core.TestGateway(ctx, &paymentsv1.TestGatewayRequest{Provider: "zarinpal", Candidate: &paymentsv1.GatewayCandidate{Proxy: proxy}})
	noSecrets(t, "TestGateway through a dead proxy", r, err)
	if err != nil || r.GetOk() || r.GetDetail() == "" {
		t.Fatalf("dead proxy: %v %v", r, err)
	}
	if _, err := f.core.TestGateway(ctx, &paymentsv1.TestGatewayRequest{Provider: "zarinpal", Candidate: &paymentsv1.GatewayCandidate{Sandbox: true}}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("sandbox in prod: %v", err)
	}
	if f.fake.Calls("request") != 0 || f.fake.Calls("verify") != 0 {
		t.Fatal("a test created or verified a payment")
	}
	// Nothing was stored by testing.
	if c := f.svc.GatewayConfigs()[0]; c.MerchantHint != "1a2b" || c.ProxySet {
		t.Fatalf("a test changed the settings: %+v", c)
	}
}

func TestGatewayReportRPC(t *testing.T) {
	f := start(t, masterKey, domain.ZarinpalSettings{})
	ctx := context.Background()
	if _, err := f.st.DB().Exec(ctx, `INSERT INTO payments.payment_intents
		(id, user_id, provider, amount, currency, status, idempotency_key, gateway_amount, gateway_currency, external_id, created_at)
		VALUES ('00000000-0000-7000-8000-00000000f0c1', $1, 'zarinpal', 150000, 'IRT', 'succeeded', 'rep-1', 1500000, 'IRR', 'A1', now() - interval '1 day'),
		       ('00000000-0000-7000-8000-00000000f0c2', $1, 'zarinpal', 50000, 'IRT', 'pending', 'rep-2', 500000, 'IRR', 'A2', now() - interval '2 days')`,
		uid); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RecordGatewayEvent(ctx, nil, "00000000-0000-7000-8000-00000000f0c1", "zarinpal", "callback", "paid", nil); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	r, err := f.core.GatewayReport(ctx, &paymentsv1.GatewayReportRequest{Provider: "zarinpal", From: now.Add(-7 * 24 * time.Hour).Unix(), To: now.Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.GetTotals()) != 2 || r.GetOpenCount() != 1 || r.GetOldestOpenAt() == 0 || r.GetLastError() != nil ||
		r.GetLastPaid().GetIntentId() != "00000000-0000-7000-8000-00000000f0c1" || r.GetLastPaid().GetKind() != "callback" {
		t.Fatalf("report: %v", r)
	}
	for _, tt := range r.GetTotals() {
		if tt.GetStatus() == "succeeded" && (tt.GetCount() != 1 || tt.GetAmounts()[0].GetAmount() != 150000 || tt.GetGatewayAmounts()[0].GetAmount() != 1500000 || tt.GetGatewayAmounts()[0].GetCurrency() != "IRR") {
			t.Fatalf("succeeded total: %v", tt)
		}
	}
	// Defaults: the last 30 days up to now.
	if r, err := f.core.GatewayReport(ctx, &paymentsv1.GatewayReportRequest{Provider: "zarinpal"}); err != nil || r.GetTo()-r.GetFrom() != 30*24*3600 {
		t.Fatalf("default period: %v %v", r, err)
	}
	for name, req := range map[string]*paymentsv1.GatewayReportRequest{
		"negative":  {Provider: "zarinpal", From: -1},
		"backwards": {Provider: "zarinpal", From: now.Unix(), To: now.Add(-time.Hour).Unix()},
		"unknown":   {Provider: "wallet"},
	} {
		if _, err := f.core.GatewayReport(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
}
