// Command core is the BOBRES core service: users, plans, orders, wallet,
// subscriptions, settings, and the dashboard API.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	coreserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"google.golang.org/grpc"
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

	gs := grpc.NewServer(grpc.UnaryInterceptor(grpcauth.UnaryInterceptor(cfg.ServiceToken)))
	coreserver.New(st, domain.New(st)).Register(gs)

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
