// Command payments owns payment intents, manual receipts and the ledger mirror.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	paymentserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
)

func main() {
	os.Exit(app.Run("payments", os.Args[1:], setup))
}

func setup(mux *http.ServeMux, h *health.Handler) error {
	cfg, err := config.LoadPayments()
	if err != nil {
		return fmt.Errorf("load payments config: %w", err)
	}
	ctx := context.Background()

	if err := migrate.Up(ctx, cfg.DatabaseURL, "payments"); err != nil {
		return fmt.Errorf("migrate payments schema: %w", err)
	}
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	sqlDB, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open outbox db: %w", err)
	}
	ob := eventbus.NewOutbox(sqlDB, "outbox_payments")
	svc := domain.New(st, ob)

	gs := grpc.NewServer(grpc.UnaryInterceptor(grpcauth.UnaryInterceptor(cfg.ServiceToken)))
	paymentserver.New(svc, st).Register(gs)

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
	go func() { _ = gs.Serve(lis) }()
	return nil
}
