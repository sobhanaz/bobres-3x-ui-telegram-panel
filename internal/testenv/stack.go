// Package testenv starts the whole backend in-process for tests: payments,
// provisioner (against a fake 3x-ui panel) and core, each on real gRPC with
// its own service token, sharing one throwaway database the way production
// shares one Postgres. Callers' test binaries must use testdb.Main.
package testenv

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/paymentsclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/provisionerclient"
	coreserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/server"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	paydomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	payserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/server"
	paystore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	provserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/server"
	provstore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xuifake"
	"google.golang.org/grpc"
)

// Test credentials (fixtures, not secrets).
const (
	CoreToken = "testenv-core-token-0123456789abcdef" //nolint:gosec // test fixture, gitleaks:allow
	BotToken  = "testenv-bot-token-0123456789abcdef"  //nolint:gosec // test fixture, gitleaks:allow
	PanelTok  = "testenv-panel-token"                 //nolint:gosec // test fixture, gitleaks:allow
	SubBase   = "https://sub.example.test/sub/"
)

// Stack is a running backend, as the bot sees it.
type Stack struct {
	Core  corev1.CoreServiceClient // authenticated as the bot
	Feed  eventsv1.EventFeedServiceClient
	Panel *xuifake.Server
	Store *corestore.Store
}

// Start runs the backend until the test ends. ownerTelegramID becomes the
// owner on first contact.
func Start(t *testing.T, ownerTelegramID int64) *Stack {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	dsn := testdb.DSN(t)
	for _, schema := range migrate.Schemas() {
		if err := migrate.Up(ctx, dsn, schema); err != nil {
			t.Fatalf("migrate %s: %v", schema, err)
		}
	}
	reset(ctx, t, dsn)

	ps, err := paystore.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ps.Close)
	pgs := server(t, "core", CoreToken)
	payserver.New(paydomain.New(ps), ps).Register(pgs)
	eventsv1.RegisterEventFeedServiceServer(pgs, eventbus.NewFeedServer(eventbus.NewFeed(ps.DB(), "outbox_payments")))
	payAddr := serve(t, pgs)

	panel := xuifake.NewServer(PanelTok)
	t.Cleanup(panel.Close)
	env, err := bcrypto.NewEnvelope([]byte("testenv-master-key-0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	vs, err := provstore.New(ctx, dsn, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vs.Close)
	prov := provserver.New(vs, xui.AllowInsecureHTTP(), xui.AllowPrivateAddresses())
	if _, err := prov.EnsureDefaultServer(ctx, panel.URL, PanelTok, SubBase, true); err != nil {
		t.Fatal(err)
	}
	vgs := server(t, "core", CoreToken)
	prov.Register(vgs)
	provAddr := serve(t, vgs)

	cs, err := corestore.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cs.Close)
	dom := coredomain.New(cs, coredomain.Config{OwnerTelegramID: ownerTelegramID})
	payConn := Dial(t, payAddr, CoreToken)
	provClient := provisionerclient.New(provisionerv1.NewProvisionerServiceClient(Dial(t, provAddr, CoreToken)))
	csrv := coreserver.New(cs, dom)
	csrv.SetPayments(paymentsclient.New(paymentsv1.NewPaymentsServiceClient(payConn)))
	csrv.SetProvisioner(provClient)
	consumer, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
		Source:     eventbus.NewGRPCSource(eventsv1.NewEventFeedServiceClient(payConn)),
		Handle:     dom.HandlePaymentEvent,
		DeadLetter: dom.DeadLetter("payments"),
		Interval:   50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = consumer.Run(ctx) }()
	worker := dom.NewProvisionWorker(provClient, nil)
	worker.Interval = 50 * time.Millisecond
	go func() { _ = worker.Run(ctx) }()
	cgs := server(t, "bot", BotToken)
	csrv.Register(cgs)
	eventsv1.RegisterEventFeedServiceServer(cgs, eventbus.NewFeedServer(eventbus.NewFeed(cs.DB(), "outbox_core")))
	coreConn := Dial(t, serve(t, cgs), BotToken)
	return &Stack{
		Core:  corev1.NewCoreServiceClient(coreConn),
		Feed:  eventsv1.NewEventFeedServiceClient(coreConn),
		Panel: panel,
		Store: cs,
	}
}

// reset empties every table of the three schemas (not goose's bookkeeping),
// so each test starts from a fresh store even within one test binary.
func reset(ctx context.Context, t *testing.T, dsn string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx) //nolint:errcheck // test helper
	rows, err := conn.Query(ctx, `SELECT format('%I.%I', table_schema, table_name) FROM information_schema.tables
		WHERE table_schema IN ('core','payments','provisioner') AND table_type = 'BASE TABLE'
		AND table_name <> 'goose_db_version'`)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "TRUNCATE "+strings.Join(tables, ", ")+" CASCADE"); err != nil {
		t.Fatal(err)
	}
}

func server(t *testing.T, peer, token string) *grpc.Server {
	t.Helper()
	gs, err := grpcx.NewServer(nil, grpcauth.Peer{Name: peer, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	return gs
}

func serve(t *testing.T, gs *grpc.Server) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis.Addr().String()
}

// Dial connects with a service token and closes the connection at test end.
func Dial(t *testing.T, addr, token string) *grpc.ClientConn {
	t.Helper()
	c, err := grpcx.Dial(addr, token)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
