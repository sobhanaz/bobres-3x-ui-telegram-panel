// Command licensesrv is the vendor's license server (docs/13-license-server.md).
// It is vendor-only: not in the release images, not in the customer compose.
//
//	licensesrv serve      the API: POST /v1/activate and POST /v1/refresh
//	licensesrv migrate    apply its database migrations
//	licensesrv key ...    create, revoke, list, show and update licenses
//	licensesrv adminkey   make the key pair of the signed admin API (/admin/v1)
//
// Environment: BOBRES_DATABASE_URL (or _FILE), BOBRES_LICENSESRV_SIGNING_KEY_FILE
// (serve), BOBRES_HTTP_ADDR (default 127.0.0.1:8080, behind an HTTPS proxy),
// BOBRES_LICENSESRV_TRUSTED_PROXIES, BOBRES_LICENSESRV_RATE_IP and
// BOBRES_LICENSESRV_RATE_KEY. BOBRES_LICENSESRV_ADMIN_PUBKEY_FILE turns the signed admin API on.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/app"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/licensesrv"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

const usage = `usage:
  licensesrv serve       run the API (BOBRES_HTTP_ADDR, default 127.0.0.1:8080)
  licensesrv migrate     apply the database migrations
  licensesrv key ...     manage licenses (licensesrv key for help)
  licensesrv adminkey    make a key pair for the signed admin API; prints both keys once
  licensesrv version
`

// defaultAddr keeps the plain-HTTP listener off the network: the vendor's
// HTTPS proxy on the same host forwards to it.
const defaultAddr = "127.0.0.1:8080"

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, in io.Reader, out, errOut io.Writer) int {
	if os.Getenv("BOBRES_HTTP_ADDR") == "" {
		_ = os.Setenv("BOBRES_HTTP_ADDR", defaultAddr)
	}
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return app.Run("licensesrv", args[1:], setup)
	case "--healthcheck":
		return app.Run("licensesrv", args, nil)
	case "version", "--version":
		fmt.Fprintln(out, "licensesrv", version.Version)
		return 0
	case "adminkey":
		priv, pub, err := license.NewAdminKey()
		if err != nil {
			fmt.Fprintln(errOut, "error:", err)
			return 1
		}
		fmt.Fprintln(errOut, "The private key signs the requests (it goes into the store that sells licenses: its dashboard setting);")
		fmt.Fprintln(errOut, "the public key goes into the file BOBRES_LICENSESRV_ADMIN_PUBKEY_FILE names, on this server. It is shown only now.")
		fmt.Fprintln(out, "private:", priv)
		fmt.Fprintln(out, "public:", pub)
		return 0
	case "migrate", "key":
	default:
		fmt.Fprint(errOut, usage)
		return 2
	}
	cfg, err := licensesrv.LoadConfig(false)
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := licensesrv.Migrate(ctx, cfg.DatabaseURL, nil); err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	if args[0] == "migrate" {
		fmt.Fprintln(errOut, "The license database is up to date.")
		return 0
	}
	st, err := licensesrv.OpenStore(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	defer st.Close()
	return licensesrv.RunKey(ctx, st, args[1:], in, out, errOut, licensesrv.AdminOptions{Now: time.Now, Actor: actor()})
}

// actor names who ran an admin command, for the audit: the person behind
// sudo when there is one (`sudo -u licensesrv licensesrv key ...`).
func actor() string {
	if n := os.Getenv("SUDO_USER"); n != "" {
		return "cli:" + n
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return "cli:" + u.Username
	}
	if n := os.Getenv("USER"); n != "" {
		return "cli:" + n
	}
	return "cli"
}

// setup wires `licensesrv serve`.
func setup(rt *app.Runtime) error {
	cfg, err := licensesrv.LoadConfig(true)
	if err != nil {
		return err
	}
	signer, err := licensesrv.LoadSigner(cfg.SigningKeyFile, func(msg string) { rt.Log.Warn(msg) })
	if err != nil {
		return err
	}
	embedded, err := license.Embedded()
	if err != nil {
		return fmt.Errorf("embedded license keys: %w", err)
	}
	ring, err := licensesrv.VerifyRing(signer, embedded)
	if err != nil {
		return err
	}
	if _, ok := embedded[signer.KeyID]; !ok {
		rt.Log.Warn("this build does not carry the signing key's public key: installs refuse its tokens until a release ships internal/license/keys/<kid>.pub",
			"kid", signer.KeyID)
	}
	if err := licensesrv.Migrate(rt.Ctx, cfg.DatabaseURL, rt.Log); err != nil {
		return err
	}
	st, err := licensesrv.OpenStore(rt.Ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	rt.OnClose(st.Close)
	rt.Health.AddCheck("db", st.Ping)
	svc, err := licensesrv.NewService(licensesrv.Options{Store: st, Signer: signer, Ring: ring, KeyRate: cfg.KeyRate, Log: rt.Log})
	if err != nil {
		return err
	}
	api, err := licensesrv.NewAPI(svc, licensesrv.APIOptions{
		TrustedProxies: cfg.TrustedProxies, IPRate: cfg.IPRate, AllowHTTP: cfg.AllowHTTP, Log: rt.Log,
	})
	if err != nil {
		return err
	}
	api.Register(rt.Mux)
	if cfg.AdminPubKeyFile != "" {
		raw, err := os.ReadFile(cfg.AdminPubKeyFile)
		if err != nil {
			return fmt.Errorf("BOBRES_LICENSESRV_ADMIN_PUBKEY_FILE: %w", err)
		}
		pub, err := license.ParseAdminPublicKey(string(raw))
		if err != nil {
			return fmt.Errorf("BOBRES_LICENSESRV_ADMIN_PUBKEY_FILE: %w", err)
		}
		admin, err := licensesrv.NewAdminAPI(api, st, licensesrv.AdminAPIOptions{PublicKey: pub, Rate: cfg.AdminRate, Log: rt.Log})
		if err != nil {
			return err
		}
		admin.Register(rt.Mux)
		rt.Log.Info("signed admin API on", "rate", cfg.AdminRate.String())
	}
	rt.Log.Info("license server ready", "kid", signer.KeyID, "rate_ip", cfg.IPRate.String(), "rate_key", cfg.KeyRate.String(),
		"trusted_proxies", len(cfg.TrustedProxies), "allow_http", cfg.AllowHTTP)
	return nil
}
