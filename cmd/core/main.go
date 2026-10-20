// Command core is the BOBRES core service: users, plans, orders, wallet,
// subscriptions, settings, and the dashboard API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	paymentsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/payments/v1"
	provisionerv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/provisioner/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/backupclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/paymentsclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/provisionerclient"
	coreserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/web"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/web/dashboard"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/web/miniapp"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "login-link" {
		os.Exit(loginLink(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "admin-password" {
		os.Exit(adminPasswordCmd(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "check" {
		os.Exit(checkCmd(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "license" {
		os.Exit(licenseCmd(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "ssl" {
		os.Exit(sslCmd(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	// The tls-agent is its own compose service from this image.
	if len(os.Args) > 1 && os.Args[1] == "tls-agent" {
		os.Exit(tlsAgentCmd(os.Args[2:], os.Stderr))
	}
	os.Exit(app.Run("core", os.Args[1:], setup))
}

// exitNotStaff is login-link's exit status for a Telegram id that is not an
// active staff member (yet).
const exitNotStaff = 3

// loginLink prints a one-time dashboard login link for the owner, or for the
// staff member with the given Telegram id: the way in when the bot is down.
// `bobres admin link` runs it inside the core container.
func loginLink(args []string) int {
	cfg, err := config.LoadCore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load core config:", err)
		return 1
	}
	noAddress := "this install has no public domain (BOBRES_DOMAIN), so the dashboard has no address"
	if cfg.PublicURL == "" {
		fmt.Fprintln(os.Stderr, noAddress)
		return 1
	}
	var tg int64 // no argument: the main owner, the first of the server's owners
	if len(cfg.AdminTelegramIDs) > 0 {
		tg = cfg.AdminTelegramIDs[0]
	}
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
	dom := domain.New(st, domain.Config{OwnerTelegramIDs: cfg.AdminTelegramIDs})
	ctx = store.WithSource(ctx, store.SourceCLI)
	if err := dom.EnsureOwner(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "owner role:", err)
	}
	// The address follows the HTTPS mode, as the bot's links do.
	tlsMgr, _ := newTLS(cfg, dom, nil, slog.New(slog.DiscardHandler), 0)
	origin := tlsMgr.PublicURL(ctx)
	if origin == "" {
		fmt.Fprintln(os.Stderr, noAddress)
		return 1
	}
	token, exp, err := dom.CreateLoginLink(ctx, tg)
	if errors.Is(err, domain.ErrForbidden) {
		// Exit 3 is what bobres admin link turns into "open the bot and send /start"
		// (it knows the bot's name; core does not).
		fmt.Fprintf(os.Stderr, "Telegram id %d is not an active staff member yet: the owner becomes staff by sending /start to the bot once (or run: bobres admin password)\n", tg)
		return exitNotStaff
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "create link:", err)
		return 1
	}
	fmt.Println(coreserver.DashboardLoginURL(origin, token))
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

	// Core's master key seals the dashboard's authenticator secrets and the
	// private key of the store's own HTTPS certificate, and keys the hashes
	// gift codes are stored as.
	secrets, err := masterKey(cfg)
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}
	dom := domain.New(st, domain.Config{OwnerTelegramIDs: cfg.AdminTelegramIDs, Timezone: cfg.Timezone, License: domain.LicenseConfig{Domain: cfg.Domain, SellerContact: cfg.SellerContact}, Secrets: secrets})
	// A new owner set with bobres menu gets the role now, not at their next /start.
	if err := dom.EnsureOwner(rt.Ctx); err != nil {
		rt.Log.Warn("owner role", "err", err)
	}
	// A new store gets the BOBRES logo as the bot's profile photo, once; the
	// dashboard's photo (or its removal) is never overridden afterwards.
	if applied, err := dom.EnsureDefaultBotPhoto(rt.Ctx); err != nil {
		rt.Log.Warn("default bot photo", "err", err)
	} else if applied {
		rt.Log.Info("default bot photo set (replace it in the dashboard: Branding)")
	}
	if secrets == nil {
		rt.Log.Warn("no master key: dashboard password logins, your own HTTPS certificate and gift codes are off (login links from the bot still work)")
	}

	// HTTPS: core asks the tls-agent (its own service, the only one that
	// writes Caddy's TLS files and reaches Caddy's admin socket) for the
	// committed setting now, so Caddy, which starts after core is healthy,
	// starts on current files. Nothing here can stop core from starting:
	// what goes wrong shows on the System page and in the alerts.
	tlsMgr, managed := newTLS(cfg, dom, secrets, rt.Log, 0)
	if !managed {
		rt.Log.Info("no tls-agent volume: the HTTPS setting is not managed here", "dir", cfg.TLSRequestDir)
	}
	tlsMgr.Start(rt.Ctx)
	rt.Go("tls", tlsMgr.Run)

	csrv := coreserver.New(st, dom)
	// Login and ticket links follow the HTTPS mode (the server's IP address
	// once its certificate is trusted).
	csrv.SetPublicURLFunc(tlsMgr.PublicURL)

	payConn, err := grpcx.Dial(cfg.PaymentsGRPCAddr, cfg.ServiceToken)
	if err != nil {
		return fmt.Errorf("dial payments %s: %w", cfg.PaymentsGRPCAddr, err)
	}
	rt.OnClose(func() { _ = payConn.Close() })
	pay := paymentsclient.New(paymentsv1.NewPaymentsServiceClient(payConn))
	csrv.SetPayments(pay)

	// Each peer gets a token of its own from core: with one shared token, any
	// peer could act as core toward the others.
	if cfg.ProvisionerToken == cfg.ServiceToken || cfg.BotInternalToken == cfg.ServiceToken {
		rt.Log.Warn("BOBRES_PROVISIONER_TOKEN or BOBRES_BOT_INTERNAL_TOKEN is not set: core presents one token to several services (re-run bobres install)")
	}
	provConn, err := grpcx.Dial(cfg.ProvisionerGRPCAddr, cfg.ProvisionerToken)
	if err != nil {
		return fmt.Errorf("dial provisioner %s: %w", cfg.ProvisionerGRPCAddr, err)
	}
	rt.OnClose(func() { _ = provConn.Close() })
	prov := provisionerclient.New(provisionerv1.NewProvisionerServiceClient(provConn))
	csrv.SetProvisioner(prov)

	// The dashboard (/admin) and its API (/api/v1), behind Caddy.
	var (
		files    web.ReceiptFiles
		bot      web.BotPeer
		licSrv   domain.LicenseServer
		publicIP web.PublicIPSource
		miniAuth web.MiniAppVerifier
		miniBot  web.MiniAppBot
		miniJoin web.MiniAppMembers
	)
	// The System page's logs tab: core reads its own buffer, asks the others.
	logs := map[string]web.LogSource{"payments": pay, "provisioner": prov}
	if cfg.BotURL != "" {
		peer := botfiles.New(cfg.BotURL, cfg.BotInternalToken)
		files, bot, licSrv, publicIP, miniAuth, miniBot, miniJoin = peer, peer, peer, peer, peer, peer, peer
		logs["bot"] = peer
	} else {
		rt.Log.Warn("BOBRES_BOT_URL is not set: the dashboard cannot show receipt photos or check channels")
	}
	// The System page asks every service's /healthz and /readyz (core its
	// own through its HTTP port); the backup service may not be installed.
	system := syshealth.New(version.Version, []syshealth.Peer{
		{Name: "core", URL: selfURL(cfg.HTTPAddr)},
		{Name: "bot", URL: cfg.BotURL},
		{Name: "payments", URL: cfg.PaymentsURL},
		{Name: "provisioner", URL: cfg.ProvisionerURL},
		{Name: "backup", URL: cfg.BackupURL, Optional: true},
	})
	// The System page's backups tab (BOBRES_BACKUP_URL is set only when the
	// install turned the backup profile on), with core's token for the backup
	// service only: core's own token never goes to that container.
	var backups web.Backups
	switch {
	case cfg.BackupURL == "":
	case cfg.BackupToken == "":
		rt.Log.Warn("BOBRES_BACKUP_TOKEN is not set: the dashboard cannot list or start backups (re-run bobres install)")
	default:
		backups = backupclient.New(cfg.BackupURL, cfg.BackupToken)
	}
	web.New(web.Config{Store: st, Domain: dom, Payments: pay, Provisioner: prov, Servers: prov, Gateways: pay,
		Env: cfg.Env, Secrets: secrets, Files: files, Bot: bot, System: system, Logs: logs, Backups: backups,
		LicenseServer: licSrv, TLS: tlsMgr, PublicIP: publicIP, Dashboard: dashboard.Handler(), MiniApp: miniapp.Handler(), MiniAppVerify: miniAuth, MiniAppBot: miniBot, MiniAppMembers: miniJoin, Log: rt.Log}).Register(rt.Mux)
	// An activated license is renewed once a day through the bot (core has
	// no internet access); a pasted or imported one is left alone.
	if licSrv != nil {
		rt.Go("license-refresh", dom.NewLicenseRefresher(licSrv, rt.Log).Run)
	}

	// Paid orders become VPN accounts here (retried with backoff, alerting once).
	rt.Go("provisioning", dom.NewProvisionWorker(prov, rt.Log).Run)
	rt.Go("usage-sync", dom.NewUsageWorker(prov, rt.Log).Run)
	// Stored subscription links follow a server's new address.
	rt.Go("sub-links", dom.NewSubLinkWorker(prov, prov, rt.Log).Run)
	// Support tickets nobody answered back close themselves; broadcasts start on time.
	rt.Go("tickets", dom.NewTicketWorker(rt.Log).Run)
	rt.Go("broadcasts", dom.NewBroadcastWorker(rt.Log).Run)
	// System alerts: every minute the watchdog checks the services, the 3x-ui
	// servers, Zarinpal, backups and the disk (backups is nil when the backup
	// service is not installed), deliveries, the bot's event feed, the
	// license and the HTTPS certificates; the bot tells the staff when one
	// goes wrong and when it is fixed (system.alert.v1).
	watch := domain.WatchdogSources{Health: system, Servers: prov, Gateways: pay, Backups: backups, TLS: tlsMgr}
	rt.Go("watchdog", dom.NewWatchdog(watch, rt.Log).Run)
	// The daily sales summary (off until the store turns it on): once a local
	// day, yesterday's sales go to the staff who hold reports.read (the
	// dashboard's permission table says who) as report.daily_summary.v1.
	rt.Go("daily-summary", dom.NewDailySummary(mayReadReports, rt.Log).Run)

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

	// The bot is core's only gRPC client; it also pulls core's event feed,
	// whose cursor is kept under this name (the watchdog reads its lag).
	gs, err := grpcx.NewServer(rt.Log, grpcauth.Peer{Name: domain.BotFeedConsumer, Token: cfg.BotToken})
	if err != nil {
		return err
	}
	csrv.Register(gs)
	eventsv1.RegisterEventFeedServiceServer(gs, eventbus.NewFeedServer(eventbus.NewFeed(st.DB(), "outbox_core")))
	return rt.ServeGRPC(cfg.GRPCAddr, gs)
}

// mayReadReports says whether a staff role holds reports.read: the daily sales
// summary goes only to those roles. The dashboard's permission table decides,
// so a change there is a change of who is told.
func mayReadReports(role string) bool { return web.Allowed(role, web.PermReportsRead) }
