// Command payments owns payment intents, manual receipts and the ledger mirror.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcx"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/migrate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/aqayepardakht"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/idpay"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/nextpay"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/trongrid"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/zarinpal"
	paymentserver "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/server"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/web"
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
	svc := domain.New(st)
	svc.SetLogger(rt.Log)
	// The gateway settings live in the database, encrypted with this service's
	// own master key; the environment only seeds them. Without a key they are
	// the environment's, read-only, as before.
	opts := domain.GatewayOptions{
		Env:          cfg.Env,
		PublicURLErr: config.CheckZarinpalPublicURL(cfg.ZarinpalPublicURL, cfg.Env),
		Seed:         domain.ZarinpalSettings{MerchantID: cfg.ZarinpalMerchantID, Sandbox: cfg.ZarinpalSandbox, Proxy: cfg.ZarinpalProxy},
		Build: func(z domain.ZarinpalSettings) (gateway.Gateway, error) {
			g, err := zarinpal.New(zarinpal.Config{MerchantID: z.MerchantID, Sandbox: z.Sandbox, Proxy: z.Proxy})
			if err != nil {
				return nil, err
			}
			return g, nil
		},
		// IDPay, NextPay and Aqayepardakht: one API key each, like Zarinpal's merchant id.
		Keyed: map[string]domain.KeyedOptions{
			"idpay": {
				Seed: domain.KeySettings{APIKey: cfg.IDPayAPIKey, Sandbox: cfg.IDPaySandbox, Proxy: cfg.IDPayProxy},
				Build: func(k domain.KeySettings) (gateway.Gateway, error) {
					g, err := idpay.New(idpay.Config{APIKey: k.APIKey, Sandbox: k.Sandbox, Proxy: k.Proxy})
					if err != nil {
						return nil, err
					}
					return g, nil
				},
			},
			"nextpay": {
				Seed: domain.KeySettings{APIKey: cfg.NextPayAPIKey, Proxy: cfg.NextPayProxy},
				Build: func(k domain.KeySettings) (gateway.Gateway, error) {
					g, err := nextpay.New(nextpay.Config{APIKey: k.APIKey, Sandbox: k.Sandbox, Proxy: k.Proxy})
					if err != nil {
						return nil, err
					}
					return g, nil
				},
			},
			// Aqayepardakht's key is its PIN, and its sandbox is the PIN "sandbox".
			"aqayepardakht": {
				Seed: domain.KeySettings{APIKey: cfg.AqayepardakhtPIN, Proxy: cfg.AqayepardakhtProxy},
				Build: func(k domain.KeySettings) (gateway.Gateway, error) {
					g, err := aqayepardakht.New(aqayepardakht.Config{PIN: k.APIKey, Sandbox: k.Sandbox, Proxy: k.Proxy})
					if err != nil {
						return nil, err
					}
					return g, nil
				},
			},
		},
		// Automatic USDT reads the store's address on TronGrid, through the
		// optional proxy set with it.
		BuildChain: func(c domain.ChainSettings) (gateway.ChainSource, error) {
			cl, err := trongrid.New(trongrid.Config{APIKey: c.APIKey, Proxy: c.Proxy})
			if err != nil {
				return nil, err
			}
			return cl, nil
		},
	}
	if cfg.MasterKey != "" {
		if opts.Envelope, err = bcrypto.NewEnvelope([]byte(cfg.MasterKey)); err != nil {
			return err
		}
	} else {
		rt.Log.Warn("no payments master key (BOBRES_PAYMENTS_MASTER_KEY): gateway settings come from the environment and cannot be changed from the dashboard; re-run bobres install to add it")
	}
	if err := svc.ConfigureGateways(rt.Ctx, opts); err != nil {
		return err
	}
	// After a restore from a backup, automatic USDT holds back the transfers
	// that could be an erased payment's (StartChain notes it again).
	if err := svc.NoteRestore(rt.Ctx, nil); err != nil {
		rt.Log.Warn("could not check for a database restore", "err", err)
	}
	srv := paymentserver.New(svc, st)
	// Always set: Zarinpal may be switched on from the dashboard at any time.
	srv.SetPublicURL(cfg.ZarinpalPublicURL)
	srv.Register(gs)
	// Public pages behind Caddy: the Zarinpal pay page and return URL.
	web.New(svc, st, rt.Log).Register(rt.Mux)
	// Catches payments whose return was lost (closed browser, no callback).
	rt.Go("gateway-reconciler", func(ctx context.Context) error { return svc.RunReconciler(ctx, 30*time.Second) })
	// Reads the store's TRON address for automatic USDT payments.
	rt.Go("chain-watcher", func(ctx context.Context) error { return svc.RunChainWatcher(ctx, 30*time.Second) })
	eventsv1.RegisterEventFeedServiceServer(gs, eventbus.NewFeedServer(eventbus.NewFeed(st.DB(), "outbox_payments")))
	return rt.ServeGRPC(cfg.GRPCAddr, gs)
}
