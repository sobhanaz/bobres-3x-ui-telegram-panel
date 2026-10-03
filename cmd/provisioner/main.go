// Command provisioner is the only BOBRES service that talks to 3x-ui panels.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	provisionserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
)

func main() {
	os.Exit(app.Run("provisioner", os.Args[1:], setup))
}

func setup(rt *app.Runtime) error {
	cfg, err := config.LoadProvisioner()
	if err != nil {
		return fmt.Errorf("load provisioner config: %w", err)
	}
	if err := migrate.Up(rt.Ctx, cfg.DatabaseURL, "provisioner"); err != nil {
		return fmt.Errorf("migrate provisioner schema: %w", err)
	}
	env, err := bcrypto.NewEnvelope([]byte(cfg.MasterKey))
	if err != nil {
		return fmt.Errorf("envelope: %w", err)
	}
	st, err := store.New(rt.Ctx, cfg.DatabaseURL, env)
	if err != nil {
		return err
	}
	rt.OnClose(st.Close)
	rt.Health.AddCheck("db", func(c context.Context) error { return st.DB().Ping(c) })

	srv := provisionserver.New(st)
	added, err := srv.EnsureDefaultServer(rt.Ctx, cfg.XUIURL, cfg.XUIToken, cfg.XUISubURL, cfg.XUIAllowPrivate)
	if err != nil {
		return err
	}
	if added {
		rt.Log.Info("registered the 3x-ui panel from BOBRES_XUI_URL")
	}

	// Only core may call the provisioner (the only holder of 3x-ui tokens).
	gs, err := grpcx.NewServer(rt.Log, grpcauth.Peer{Name: "core", Token: cfg.CoreToken})
	if err != nil {
		return err
	}
	srv.Register(gs)
	return rt.ServeGRPC(cfg.GRPCAddr, gs)
}
