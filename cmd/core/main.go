// Command core is the BOBRES core service: users, plans, orders, wallet,
// subscriptions, settings, and the dashboard API.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/paymentsclient"
	coreserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	os.Exit(app.Run("core", os.Args[1:], setup))
}

func setup(mux *http.ServeMux, h *health.Handler) error {
	cfg, err := config.LoadCore()
	if err != nil {
		return fmt.Errorf("load core config: %w", err)
	}
	ctx := context.Background()

	if err := migrate.Up(ctx, cfg.DatabaseURL, "core"); err != nil {
		return fmt.Errorf("migrate core schema: %w", err)
	}
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}

	dom := domain.New(st)
	csrv := coreserver.New(st, dom)

	payConn, err := grpc.NewClient(cfg.PaymentsGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(grpcauth.ClientCredentials{Token: cfg.ServiceToken}))
	if err != nil {
		return fmt.Errorf("dial payments %s: %w", cfg.PaymentsGRPCAddr, err)
	}
	csrv.SetPayments(paymentsclient.New(paymentsv1.NewPaymentsServiceClient(payConn)))

	sqlDB, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open relay db: %w", err)
	}
	relay := eventbus.NewRelay(sqlDB, "outbox_payments", 500*time.Millisecond)
	relayCtx, relayCancel := context.WithCancel(context.Background())
	go relay.Start(relayCtx, func(c context.Context, m eventbus.Message) error {
		_, err := dom.HandlePaymentEvent(c, fmt.Sprintf("payments:%d", m.ID), m.Topic, m.Payload)
		return err
	})
	defer relayCancel()

	gs := grpc.NewServer(grpc.UnaryInterceptor(grpcauth.UnaryInterceptor(cfg.ServiceToken)))
	csrv.Register(gs)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", cfg.GRPCAddr, err)
	}
	h.AddCheck("db", func(c context.Context) error { return st.DB().Ping(c) })
	h.AddCheck("grpc", func(c context.Context) error {
		conn, err := net.DialTimeout("tcp", lis.Addr().String(), time.Second)
		if err != nil {
			return err
		}
		return conn.Close()
	})
	go func() {
		_ = gs.Serve(lis)
	}()
	return nil
}
