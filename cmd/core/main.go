// Command core is the BOBRES core service: users, plans, orders, wallet,
// subscriptions, settings, and the dashboard API.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/paymentsclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/provisionerclient"
	coreserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/web"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/web/dashboard"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "login-link" {
		os.Exit(loginLink(os.Args[2:]))
	}
	os.Exit(app.Run("core", os.Args[1:], setup))
}

// loginLink prints a one-time dashboard login link for the owner, or for the
// staff member with the given Telegram id: the way in when the bot is down.
// `bobres admin link` runs it inside the core container.
func loginLink(args []string) int {
	cfg, err := config.LoadCore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load core config:", err)
		return 1
	}
	if cfg.PublicURL == "" {
		fmt.Fprintln(os.Stderr, "this install has no public domain (BOBRES_DOMAIN), so the dashboard has no address")
		return 1
	}
	tg := cfg.AdminTelegramID
	if len(args) > 0 {
		if tg, err = strconv.ParseInt(args[0], 10, 64); err != nil || tg <= 0 {
			fmt.Fprintln(os.Stderr, "usage: login-link [telegram id]")
			return 2
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		return 1
	}
	defer st.Close()
	dom := domain.New(st, domain.Config{OwnerTelegramID: cfg.AdminTelegramID})
	ctx = store.WithSource(ctx, store.SourceCLI)
	if err := dom.EnsureOwner(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "owner role:", err)
	}
	token, exp, err := dom.CreateLoginLink(ctx, tg)
	if errors.Is(err, domain.ErrForbidden) {
		fmt.Fprintf(os.Stderr, "Telegram id %d is not an active staff member (the owner must have started the bot once)\n", tg)
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "create link:", err)
		return 1
	}
	fmt.Println(coreserver.DashboardLoginURL(cfg.PublicURL, token))
	fmt.Fprintf(os.Stderr, "Works once, until %s.\n", exp.Local().Format("15:04:05"))
	return 0
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
	// A new owner set with bobres menu gets the role now, not at their next /start.
	if err := dom.EnsureOwner(rt.Ctx); err != nil {
		rt.Log.Warn("owner role", "err", err)
	}
	csrv := coreserver.New(st, dom)
	csrv.SetPublicURL(cfg.PublicURL)

	payConn, err := grpcx.Dial(cfg.PaymentsGRPCAddr, cfg.ServiceToken)
	if err != nil {
		return fmt.Errorf("dial payments %s: %w", cfg.PaymentsGRPCAddr, err)
	}
	rt.OnClose(func() { _ = payConn.Close() })
	pay := paymentsclient.New(paymentsv1.NewPaymentsServiceClient(payConn))
	csrv.SetPayments(pay)

	provConn, err := grpcx.Dial(cfg.ProvisionerGRPCAddr, cfg.ServiceToken)
	if err != nil {
		return fmt.Errorf("dial provisioner %s: %w", cfg.ProvisionerGRPCAddr, err)
	}
	rt.OnClose(func() { _ = provConn.Close() })
	prov := provisionerclient.New(provisionerv1.NewProvisionerServiceClient(provConn))
	csrv.SetProvisioner(prov)

	// The dashboard (/admin) and its API (/api/v1), behind Caddy.
	var secrets *bcrypto.Envelope
	if cfg.MasterKey != "" {
		if secrets, err = bcrypto.NewEnvelope([]byte(cfg.MasterKey)); err != nil {
			return fmt.Errorf("master key: %w", err)
		}
	} else {
		rt.Log.Warn("no master key: dashboard password logins are off (login links from the bot still work)")
	}
	var (
		files web.ReceiptFiles
		bot   web.BotPeer
	)
	if cfg.BotURL != "" {
		peer := botfiles.New(cfg.BotURL, cfg.ServiceToken)
		files, bot = peer, peer
	} else {
		rt.Log.Warn("BOBRES_BOT_URL is not set: the dashboard cannot show receipt photos or check channels")
	}
	web.New(web.Config{Store: st, Domain: dom, Payments: pay, Provisioner: prov, Secrets: secrets, Files: files,
		Bot: bot, Dashboard: dashboard.Handler(), Log: rt.Log}).Register(rt.Mux)

	// Paid orders become VPN accounts here (retried with backoff, alerting once).
	rt.Go("provisioning", dom.NewProvisionWorker(prov, rt.Log).Run)
	rt.Go("usage-sync", dom.NewUsageWorker(prov, rt.Log).Run)

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
