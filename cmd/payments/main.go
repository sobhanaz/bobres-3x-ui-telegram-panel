// Command payments owns payment intents, manual receipts and the ledger mirror.
package main

import (
	"context"
	"fmt"
	"os"

	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	paymentserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
)

func main() {
	os.Exit(app.Run("payments", os.Args[1:], setup))
}

func setup(rt *app.Runtime) error {
	cfg, err := config.LoadPayments()
	if err != nil {
		return fmt.Errorf("load payments config: %w", err)
	}
	if err := migrate.Up(rt.Ctx, cfg.DatabaseURL, "payments"); err != nil {
		return fmt.Errorf("migrate payments schema: %w", err)
	}
	st, err := store.New(rt.Ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	rt.OnClose(st.Close)
	rt.Health.AddCheck("db", func(c context.Context) error { return st.DB().Ping(c) })

	// Only core may call payments; core also pulls the payments event feed.
	gs, err := grpcx.NewServer(rt.Log, grpcauth.Peer{Name: "core", Token: cfg.CoreToken})
	if err != nil {
		return err
	}
	paymentserver.New(domain.New(st), st).Register(gs)
	eventsv1.RegisterEventFeedServiceServer(gs, eventbus.NewFeedServer(eventbus.NewFeed(st.DB(), "outbox_payments")))
	return rt.ServeGRPC(cfg.GRPCAddr, gs)
}
