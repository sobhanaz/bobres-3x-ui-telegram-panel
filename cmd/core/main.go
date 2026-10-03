// Command core is the BOBRES core service: users, plans, orders, wallet,
// subscriptions, settings, and the dashboard API.
package main

import (
	"context"
	"fmt"
	"os"

	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/paymentsclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/provisionerclient"
	coreserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
)

func main() {
	os.Exit(app.Run("core", os.Args[1:], setup))
}

func setup(rt *app.Runtime) error {
	cfg, err := config.LoadCore()
	if err != nil {
		return fmt.Errorf("load core config: %w", err)
	}
	if err := migrate.Up(rt.Ctx, cfg.DatabaseURL, "core"); err != nil {
		return fmt.Errorf("migrate core schema: %w", err)
	}
	st, err := store.New(rt.Ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	rt.OnClose(st.Close)
	rt.Health.AddCheck("db", func(c context.Context) error { return st.DB().Ping(c) })

	dom := domain.New(st, domain.Config{OwnerTelegramID: cfg.AdminTelegramID})
	csrv := coreserver.New(st, dom)

	payConn, err := grpcx.Dial(cfg.PaymentsGRPCAddr, cfg.ServiceToken)
	if err != nil {
		return fmt.Errorf("dial payments %s: %w", cfg.PaymentsGRPCAddr, err)
	}
	rt.OnClose(func() { _ = payConn.Close() })
	csrv.SetPayments(paymentsclient.New(paymentsv1.NewPaymentsServiceClient(payConn)))

	provConn, err := grpcx.Dial(cfg.ProvisionerGRPCAddr, cfg.ServiceToken)
	if err != nil {
		return fmt.Errorf("dial provisioner %s: %w", cfg.ProvisionerGRPCAddr, err)
	}
	rt.OnClose(func() { _ = provConn.Close() })
	prov := provisionerclient.New(provisionerv1.NewProvisionerServiceClient(provConn))
	csrv.SetProvisioner(prov)

	// Paid orders become VPN accounts here (retried with backoff, alerting once).
	rt.Go("provisioning", dom.NewProvisionWorker(prov, rt.Log).Run)

	// Apply payment outcomes from the payments feed (pulled over gRPC: core
	// never reads the payments database).
	payEvents, err := eventbus.NewConsumer(eventbus.ConsumerConfig{
		Name:       "payments-events",
		Source:     eventbus.NewGRPCSource(eventsv1.NewEventFeedServiceClient(payConn)),
		Handle:     dom.HandlePaymentEvent,
		DeadLetter: dom.DeadLetter("payments"),
		Log:        rt.Log,
	})
	if err != nil {
		return err
	}
	rt.Go("payments-events", payEvents.Run)

	// The bot is core's only gRPC client; it also pulls core's event feed.
	gs, err := grpcx.NewServer(rt.Log, grpcauth.Peer{Name: "bot", Token: cfg.BotToken})
	if err != nil {
		return err
	}
	csrv.Register(gs)
	eventsv1.RegisterEventFeedServiceServer(gs, eventbus.NewFeedServer(eventbus.NewFeed(st.DB(), "outbox_core")))
	return rt.ServeGRPC(cfg.GRPCAddr, gs)
}
