// Command provisioner is the only BOBRES service that talks to 3x-ui panels.
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
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	provisionserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"google.golang.org/grpc"
)

func main() {
	os.Exit(app.Run("provisioner", os.Args[1:], setup))
}

func setup(mux *http.ServeMux, h *health.Handler) error {
	cfg, err := config.LoadProvisioner()
	if err != nil {
		return fmt.Errorf("load provisioner config: %w", err)
	}
	ctx := context.Background()

	if err := migrate.Up(ctx, cfg.DatabaseURL, "provisioner"); err != nil {
		return fmt.Errorf("migrate provisioner schema: %w", err)
	}
	env, err := bcrypto.NewEnvelope([]byte(cfg.MasterKey))
	if err != nil {
		return fmt.Errorf("envelope: %w", err)
	}
	st, err := store.New(ctx, cfg.DatabaseURL, env)
	if err != nil {
		return err
	}

	gs := grpc.NewServer(grpc.UnaryInterceptor(grpcauth.UnaryInterceptor(cfg.ServiceToken)))
	provisionserver.New(st).Register(gs)

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
