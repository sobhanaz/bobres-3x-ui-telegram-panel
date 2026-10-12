package server_test

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/trongrid"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tronfake"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	tronStore  = "TCKR37NfLumfScqBQX6qrnJR3mZkv6xGqi"
	tronStore2 = "TWxtFn4btLxwjWcZJvyS6T5iQaHztZnxcF"
	tronAPIKey = "4a6e2c1d-7b8f-4e3a-9c5d-1f2e3d4c5b6a" //nolint:gosec // test fixture
)

type chainFixture struct {
	core, bot paymentsv1.PaymentsServiceClient
	svc       *domain.Service
	st        *store.Store
	fake      *tronfake.Server
}

// startChain serves payments over gRPC with the USDT watcher reading the
// TronGrid fake.
func startChain(t *testing.T) *chainFixture {
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
	if _, err := st.DB().Exec(ctx, `TRUNCATE payments.gateway_configs, payments.payment_intents, payments.outbox_payments,
		payments.chain_cursors, payments.gateway_events CASCADE`); err != nil {
		t.Fatal(err)
	}
	fake := tronfake.New()
	t.Cleanup(fake.Close)
	env, err := bcrypto.NewEnvelope([]byte(masterKey))
	if err != nil {
		t.Fatal(err)
	}
	o := domain.GatewayOptions{Env: "prod", Envelope: env,
		Build: func(domain.ZarinpalSettings) (gateway.Gateway, error) { return nil, nil },
		BuildChain: func(c domain.ChainSettings) (gateway.ChainSource, error) {
			cl, err := trongrid.New(trongrid.Config{APIKey: c.APIKey, Proxy: c.Proxy, BaseURL: fake.URL})
			if err != nil {
				return nil, err
			}
			return cl, nil
		}}
	svc := domain.New(st)
	if err := svc.ConfigureGateways(ctx, o); err != nil {
		t.Fatal(err)
	}
	gs, err := grpcx.NewServer(slog.New(slog.DiscardHandler), grpcauth.Peer{Name: "core", Token: coreToken})
	if err != nil {
		t.Fatal(err)
	}
	server.New(svc, st).Register(gs)
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
	return &chainFixture{core: dial(coreToken), bot: dial(botToken), svc: svc, st: st, fake: fake}
}

// Core sets automatic USDT up, starts a payment, reads the report with its
// unmatched deposits and resolves one, and checks the payment once paid; a
// TXID the watcher used is refused as a manual proof. Only core gets in.
func TestUSDTAutoRPCs(t *testing.T) {
	f := startChain(t)
	ctx := context.Background()
	cfg, err := f.core.SetGatewayConfig(ctx, &paymentsv1.SetGatewayConfigRequest{Provider: "usdt_trc20_auto", Enabled: true,
		Address: tronStore, ApiKey: tronAPIKey, ActorId: staff})
	if err != nil || !cfg.GetEnabled() || !cfg.GetOffered() || !cfg.GetConfigured() || cfg.GetAddress() != tronStore ||
		!cfg.GetApiKeySet() || cfg.GetUpdatedBy() != staff {
		t.Fatalf("set: %v %v", cfg, err)
	}
	list, err := f.core.ListGatewayConfigs(ctx, &paymentsv1.ListGatewayConfigsRequest{})
	if err != nil || strings.Contains(protojson.Format(list), tronAPIKey) || list.GetConfigs()[1].GetAddress() != tronStore {
		t.Fatalf("list: %v %v", list, err)
	}
	if gw, _ := f.core.ListGateways(ctx, &paymentsv1.ListGatewaysRequest{}); !strings.Contains(strings.Join(gw.GetProviders(), ","), "usdt_trc20_auto") {
		t.Fatalf("offered: %v", gw.GetProviders())
	}
	if r, err := f.core.TestGateway(ctx, &paymentsv1.TestGatewayRequest{Provider: "usdt_trc20_auto"}); err != nil || !r.GetOk() {
		t.Fatalf("test: %v %v", r, err)
	}
	if _, err := f.core.SetGatewayConfig(ctx, &paymentsv1.SetGatewayConfigRequest{Provider: "usdt_trc20_auto", Address: "TNotAnAddress"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad address: %v", err)
	}

	in, err := f.core.CreateIntent(ctx, &paymentsv1.CreateIntentRequest{UserId: uid, Provider: "usdt_trc20_auto",
		Amount: &commonv1.Money{Amount: 500_000, Currency: "IRT"}, IdempotencyKey: "rpc-u1",
		GatewayAmount: &commonv1.Money{Amount: 5_000_000, Currency: "USDT"}})
	if err != nil || in.GetProvider() != "usdt_trc20_auto" || in.GetStatus() != "pending" || in.GetPayAddress() != tronStore ||
		in.GetGatewayAmount().GetAmount() != 5_000_001 || in.GetGatewayAmount().GetCurrency() != "USDT" || in.GetExpiresAt() == 0 ||
		in.GetExternalId() != "" || in.GetPayUrl() != "" {
		t.Fatalf("create: %v %v", in, err)
	}
	_, err = f.core.SetGatewayConfig(ctx, &paymentsv1.SetGatewayConfigRequest{Provider: "usdt_trc20_auto", Enabled: true, Address: tronStore2})
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "1 open usdt_trc20_auto payments: the address cannot change") {
		t.Fatalf("address change with an open payment: %v", err)
	}

	f.fake.Pay(tronStore, 1_000_000) // nobody's
	if _, err := f.svc.ScanChainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// Held at first: a manual proof may still claim it. The log shows until when.
	rep, err := f.core.GatewayReport(ctx, &paymentsv1.GatewayReportRequest{Provider: "usdt_trc20_auto"})
	if err != nil || rep.GetUnmatchedCount() != 0 {
		t.Fatalf("report during the hold: %v %v", rep, err)
	}
	held, err := f.core.ListChainTransfers(ctx, &paymentsv1.ListChainTransfersRequest{Outcome: "unmatched", Value: 1_000_000})
	if err != nil || len(held.GetTransfers()) != 1 || held.GetTransfers()[0].GetHeldUntil() < time.Now().Add(15*time.Minute).Unix() {
		t.Fatalf("held deposit: %v %v", held, err)
	}
	if other, err := f.core.ListChainTransfers(ctx, &paymentsv1.ListChainTransfersRequest{Value: 1_000_001}); err != nil || len(other.GetTransfers()) != 0 {
		t.Fatalf("by another value: %v %v", other, err)
	}
	if _, err := f.st.DB().Exec(ctx, `UPDATE payments.chain_transfers SET announce_at = now() - interval '1 second' WHERE announce_at IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ScanChainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	rep, err = f.core.GatewayReport(ctx, &paymentsv1.GatewayReportRequest{Provider: "usdt_trc20_auto"})
	if err != nil || rep.GetUnmatchedCount() != 1 || len(rep.GetUnmatched()) != 1 || rep.GetLastScanAt() == 0 || rep.GetLastScanOkAt() == 0 ||
		rep.GetScanError() != "" || rep.GetOpenCount() != 1 {
		t.Fatalf("report: %v %v", rep, err)
	}
	u := rep.GetUnmatched()[0]
	if u.GetOutcome() != "unmatched" || u.GetReason() != "no_payment" || u.GetAmount().GetAmount() != 1_000_000 ||
		u.GetAmount().GetCurrency() != "USDT" || u.GetToAddress() != tronStore || u.GetFromAddress() != tronfake.Sender ||
		u.GetNetwork() != "TRC20" || u.GetBlockTime() == 0 || u.GetIntentId() != "" || u.GetHeldUntil() != 0 {
		t.Fatalf("unmatched deposit: %v", u)
	}
	logs, err := f.core.ListChainTransfers(ctx, &paymentsv1.ListChainTransfersRequest{Outcome: "unmatched"})
	if err != nil || len(logs.GetTransfers()) != 1 || logs.GetPageInfo().GetTotal() != 1 {
		t.Fatalf("log: %v %v", logs, err)
	}
	res, err := f.core.ResolveChainTransfer(ctx, &paymentsv1.ResolveChainTransferRequest{Txid: u.GetTxid(), ActorId: staff, Note: "refunded"})
	if err != nil || res.GetOutcome() != "resolved" || res.GetResolvedBy() != staff || res.GetResolvedAt() == 0 || res.GetNote() != "refunded" {
		t.Fatalf("resolve: %v %v", res, err)
	}
	if _, err := f.core.ResolveChainTransfer(ctx, &paymentsv1.ResolveChainTransferRequest{Txid: u.GetTxid(), ActorId: staff}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("resolve twice: %v", err)
	}
	for name, req := range map[string]*paymentsv1.ResolveChainTransferRequest{
		"bad txid":  {Txid: "abc", ActorId: staff},
		"no actor":  {Txid: u.GetTxid()},
		"bad index": {Txid: u.GetTxid(), EventIndex: -1, ActorId: staff},
	} {
		if _, err := f.core.ResolveChainTransfer(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.core.ResolveChainTransfer(ctx, &paymentsv1.ResolveChainTransferRequest{Txid: strings.Repeat("0", 64), ActorId: staff}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown transfer: %v", err)
	}
	if _, err := f.core.ListChainTransfers(ctx, &paymentsv1.ListChainTransfersRequest{Outcome: "credited"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown outcome: %v", err)
	}

	txid := f.fake.Pay(tronStore, 5_000_001)
	// The watcher read the address a moment ago; a check reads it again only
	// after a gap.
	if got, err := f.core.CheckIntent(ctx, &paymentsv1.CheckIntentRequest{IntentId: in.GetId(), UserId: uid}); err != nil || got.GetStatus() != "pending" {
		t.Fatalf("check within the gap: %v %v", got, err)
	}
	if _, err := f.st.DB().Exec(ctx, `UPDATE payments.chain_cursors SET scanned_at = now() - interval '11 seconds'`); err != nil {
		t.Fatal(err)
	}
	got, err := f.core.CheckIntent(ctx, &paymentsv1.CheckIntentRequest{IntentId: in.GetId(), UserId: uid})
	if err != nil || got.GetStatus() != "succeeded" || got.GetExternalId() != txid || got.GetProviderRef() != txid {
		t.Fatalf("check after paying: %v %v", got, err)
	}
	if byTx, err := f.core.ListChainTransfers(ctx, &paymentsv1.ListChainTransfersRequest{Txid: "0x" + txid}); err != nil ||
		len(byTx.GetTransfers()) != 1 || byTx.GetTransfers()[0].GetIntentId() != in.GetId() || byTx.GetTransfers()[0].GetOutcome() != "matched" {
		t.Fatalf("log of the payment: %v %v", byTx, err)
	}

	m, err := f.core.CreateIntent(ctx, &paymentsv1.CreateIntentRequest{UserId: uid, Provider: "manual_crypto",
		Amount: &commonv1.Money{Amount: 500_000, Currency: "IRT"}, IdempotencyKey: "rpc-m1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, spelled := range []string{txid, "0x" + txid, u.GetTxid()} {
		_, err = f.core.SubmitTXID(ctx, &paymentsv1.SubmitTXIDRequest{IntentId: m.GetId(), UserId: uid, Network: "TRC20", Txid: spelled})
		if status.Code(err) != codes.AlreadyExists || !strings.Contains(status.Convert(err).Message(), "already submitted") {
			t.Fatalf("manual proof %s of a used transaction: %v", spelled, err)
		}
	}

	for name, call := range map[string]func(paymentsv1.PaymentsServiceClient) error{
		"ListChainTransfers": func(c paymentsv1.PaymentsServiceClient) error {
			_, err := c.ListChainTransfers(ctx, &paymentsv1.ListChainTransfersRequest{})
			return err
		},
		"ResolveChainTransfer": func(c paymentsv1.PaymentsServiceClient) error {
			_, err := c.ResolveChainTransfer(ctx, &paymentsv1.ResolveChainTransferRequest{Txid: txid, ActorId: staff})
			return err
		},
	} {
		if err := call(f.bot); status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s with the bot's token: %v", name, err)
		}
	}
}
