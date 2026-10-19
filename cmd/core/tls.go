package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsagent"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsprobe"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// newTLS builds the HTTPS manager as every core process uses it: the
// committed setting in the store, the tls-agent's volumes when this
// container mounts them (managed: true), Caddy dialed over the edge
// network for the live status, and, when the bot is set, the bot asked for
// the server's public IP address (the links follow ip mode only while it
// confirms the address: login-link and the gRPC link paths get it from
// here). busy is how long a change waits for another one (0: 3 seconds).
func newTLS(cfg config.Core, dom *domain.Service, secrets *bcrypto.Envelope, log *slog.Logger, busy time.Duration) (tls *domain.TLS, managed bool) {
	var ex domain.TLSExchange
	if fi, err := os.Stat(cfg.TLSRequestDir); err == nil && fi.IsDir() {
		ex, managed = tlsconf.NewExchange(cfg.TLSRequestDir, cfg.TLSResultDir), true
	}
	probe := func(ctx context.Context, address string) tlsprobe.Result {
		return tlsprobe.Probe(ctx, cfg.TLSProbeAddr, address, tlsprobe.Options{})
	}
	return dom.NewTLS(domain.TLSConfig{Domain: cfg.Domain, ZarinpalHost: cfg.ZarinpalHost, PublicURLEnv: cfg.PublicURLEnv,
		Secrets: secrets, Exchange: ex, Probe: probe, DetectIP: botDetector(cfg), BusyWait: busy, Log: log}), managed
}

// botDetector asks the bot for the server's public IP address (core has no
// internet access); nil when the bot is not set (BOBRES_BOT_URL).
func botDetector(cfg config.Core) func(context.Context) (string, error) {
	if cfg.BotURL == "" {
		return nil
	}
	return botfiles.New(cfg.BotURL, cfg.BotInternalToken).PublicIP
}

// masterKey is core's envelope; without a master key it is nil, and manual
// mode is refused.
func masterKey(cfg config.Core) (*bcrypto.Envelope, error) {
	if cfg.MasterKey == "" {
		return nil, nil
	}
	return bcrypto.NewEnvelope([]byte(cfg.MasterKey))
}

// tlsAgentCmd runs the tls-agent (internal/tlsagent), the service that
// applies core's HTTPS requests to Caddy. It needs no database and no
// network; its compose service mounts the request volume read-only, the
// result volume, the install's tls/ directory, caddy/ read-only and
// Caddy's admin socket. It reads the install's own names from its
// environment (BOBRES_DOMAIN and BOBRES_ZARINPAL_HOST, as core has them) and
// refuses any request for other names. With --healthcheck it only checks the
// agent's heartbeat: 0 when its loop ran within the last 30 seconds.
func tlsAgentCmd(args []string, errOut io.Writer) int {
	c, health, ok := agentConfig(args, os.Getenv, errOut)
	if !ok {
		return 2
	}
	if health {
		return tlsagent.Healthcheck(c.Heartbeat, time.Now())
	}
	level, format := os.Getenv("BOBRES_LOG_LEVEL"), os.Getenv("BOBRES_LOG_FORMAT")
	if level == "" {
		level = "info"
	}
	if format == "" {
		format = "json"
	}
	c.Log = logging.New("tls-agent", level, format)
	c.Log.Info("starting", "version", version.Version, "request", c.RequestPath, "result", c.ResultPath, "tls_dir", c.TLSDir,
		"caddyfile", c.Caddyfile, "admin_socket", c.AdminSocket, "domain", c.Domain, "zarinpal_host", c.ZarinpalHost)
	if c.Domain == "" {
		c.Log.Warn("BOBRES_DOMAIN is not set in the tls-agent's environment: every request is refused")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	_ = tlsagent.New(c).Run(ctx)
	c.Log.Info("stopped")
	return 0
}

// agentConfig reads the tls-agent's flags and its environment: the
// install's own domain and Zarinpal host (BOBRES_DOMAIN and
// BOBRES_ZARINPAL_HOST, as core has them), which the agent holds every
// request to. ok is false for wrong use (usage is written to errOut);
// health: only the heartbeat is checked.
func agentConfig(args []string, getenv func(string) string, errOut io.Writer) (c tlsagent.Config, health, ok bool) {
	fs := flag.NewFlagSet("tls-agent", flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.StringVar(&c.RequestPath, "request", tlsagent.DefaultRequest, "core's request file")
	fs.StringVar(&c.ResultPath, "result", tlsagent.DefaultResult, "the agent's answer file")
	fs.StringVar(&c.TLSDir, "tls-dir", tlsagent.DefaultTLSDir, "where the HTTPS files are written (Caddy reads them at "+tlsconf.Dir+")")
	fs.StringVar(&c.Caddyfile, "caddyfile", tlsagent.DefaultCaddyfile, "Caddy's main Caddyfile, as the agent reads it")
	fs.StringVar(&c.SourceFile, "caddy-source", tlsagent.DefaultSourceFile, "the same Caddyfile, as Caddy names it")
	fs.StringVar(&c.AdminSocket, "admin-socket", tlsagent.DefaultAdminSocket, "Caddy's admin socket")
	fs.StringVar(&c.Heartbeat, "heartbeat", tlsagent.DefaultHeartbeat, "the file the loop touches every round")
	fs.BoolVar(&health, "healthcheck", false, "check the heartbeat and exit")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(errOut, "usage: tls-agent [--healthcheck] [--request F] [--result F] [--tls-dir D] [--caddyfile F] [--caddy-source F] [--admin-socket S] [--heartbeat F]")
		return c, false, false
	}
	c.Domain = strings.TrimSpace(getenv("BOBRES_DOMAIN"))
	c.ZarinpalHost = strings.TrimSpace(getenv("BOBRES_ZARINPAL_HOST"))
	return c, health, true
}

const sslUsage = "usage: ssl status [--json] | ssl auto | ssl ip <ip> [--detected <ip>] [--yes] | ssl manual  (the certificate chain and its key as PEM on stdin)"

// sslArgs is what `ssl` was asked.
type sslArgs struct {
	sub      string // status, auto, ip or manual
	json     bool
	ip       string
	detected string
	yes      bool
}

// parseSSL reads the subcommand and its flags (false: wrong use).
func parseSSL(args []string) (sslArgs, bool) {
	if len(args) == 0 {
		return sslArgs{}, false
	}
	a := sslArgs{sub: args[0]}
	rest := args[1:]
	switch a.sub {
	case "status":
		switch strings.Join(rest, " ") {
		case "":
		case "--json":
			a.json = true
		default:
			return a, false
		}
	case "auto", "manual":
		if len(rest) > 0 {
			return a, false
		}
	case "ip":
		for i := 0; i < len(rest); i++ {
			switch arg := rest[i]; {
			case arg == "--yes":
				a.yes = true
			case arg == "--detected" && i+1 < len(rest):
				i++
				a.detected = rest[i]
			case strings.HasPrefix(arg, "--detected="):
				a.detected = strings.TrimPrefix(arg, "--detected=")
			case strings.HasPrefix(arg, "-") || a.ip != "":
				return a, false
			default:
				a.ip = arg
			}
		}
		if a.ip == "" {
			return a, false
		}
	default:
		return a, false
	}
	return a, true
}

// sslCmd shows or changes the store's HTTPS setting, as the dashboard's
// HTTPS tab does (domain.TLS): "ssl status" shows the setting and what
// every served address serves now (--json: the dashboard API's JSON);
// "ssl auto", "ssl ip <ip>" and "ssl manual" apply a mode and answer once
// Caddy took it, or why it did not. An IP address that differs from the
// detected one (--detected, which bobres passes from the host, else the
// bot is asked) needs --yes. "ssl manual" reads the certificate chain and
// its private key, as PEM, on stdin. It is audited as system.tls.set from
// the command line. `bobres ssl ...` runs it inside the core container.
func sslCmd(args []string, in io.Reader, out, errOut io.Writer) int {
	a, ok := parseSSL(args)
	if !ok {
		fmt.Fprintln(errOut, sslUsage)
		return 2
	}
	var certPEM, keyPEM []byte
	if a.sub == "manual" {
		raw, err := io.ReadAll(io.LimitReader(in, 2*tlsconf.MaxPEM+1))
		if err != nil {
			fmt.Fprintln(errOut, "read the certificate and key:", err)
			return 2
		}
		if len(strings.TrimSpace(string(raw))) == 0 {
			fmt.Fprintln(errOut, "no PEM on stdin: cat cert.pem key.pem | ssl manual")
			return 2
		}
		if certPEM, keyPEM, err = tlsconf.SplitBundle(raw); err != nil {
			fmt.Fprintln(errOut, "not applied:", err)
			return 1
		}
	}
	cfg, err := config.LoadCore()
	if err != nil {
		fmt.Fprintln(errOut, "load core config:", err)
		return 1
	}
	secrets, err := masterKey(cfg)
	if err != nil {
		fmt.Fprintln(errOut, "master key:", err)
		return 1
	}
	// Ctrl-C or a termination signal ends the wait, and the change rolls
	// back before the command exits (a kill -9 cannot; core's reconcile puts
	// the saved setting back within a minute).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(errOut, "connect:", err)
		return 1
	}
	defer st.Close()
	dom := domain.New(st, domain.Config{OwnerTelegramIDs: cfg.AdminTelegramIDs})
	tlsMgr, _ := newTLS(cfg, dom, secrets, slog.New(slog.DiscardHandler), 30*time.Second)
	if a.sub != "status" {
		c := domain.TLSChange{Mode: tlsconf.Mode(a.sub), IP: a.ip, CertPEM: certPEM, KeyPEM: keyPEM, DetectedIP: a.detected, ConfirmIP: a.yes}
		if a.sub == "ip" && a.detected == "" && !a.yes && cfg.BotURL != "" {
			if ip, err := botfiles.New(cfg.BotURL, cfg.BotInternalToken).PublicIP(ctx); err == nil {
				c.DetectedIP = ip
			}
		}
		res, err := tlsMgr.Set(store.WithSource(ctx, store.SourceCLI), nil, c)
		var te *domain.TLSError
		switch {
		case errors.As(err, &te) && te.Code == domain.TLSErrIPUnconfirmed:
			fmt.Fprintf(errOut, "not applied: %s\nRun it again with --yes to use %s anyway.\n", te.Message, a.ip)
			return 1
		case errors.As(err, &te):
			fmt.Fprintf(errOut, "not applied: %s (%s)\n", te.Message, te.Code)
			return 1
		case err != nil:
			fmt.Fprintln(errOut, "https:", err)
			return 1
		}
		fmt.Fprintf(out, "HTTPS is now %s.\n", modeText(c.Mode))
		for _, w := range res.Warnings {
			fmt.Fprintf(out, "Warning: %s.\n", w.Message)
		}
		if res.Pending {
			fmt.Fprintln(out, "Caddy is not running: it loads the new setting when it starts.")
		}
		if c.Mode == tlsconf.ModeIP {
			fmt.Fprintln(out, "Let's Encrypt issues the certificate within a few minutes; links move to the IP address once it is trusted\n"+
				"and the bot detects this server's public address as that same address (BOBRES_PUBLIC_URL overrides).")
		}
		fmt.Fprintln(out)
	}
	v, err := tlsMgr.View(ctx, true)
	if err != nil {
		fmt.Fprintln(errOut, "https:", err)
		return 1
	}
	if a.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v.JSON()); err != nil {
			fmt.Fprintln(errOut, "https:", err)
			return 1
		}
		return 0
	}
	printTLS(out, v, time.Now())
	return 0
}

// modeText names a mode for people.
func modeText(m tlsconf.Mode) string {
	switch m {
	case tlsconf.ModeAuto:
		return "auto (an automatic certificate for the domain)"
	case tlsconf.ModeIP:
		return "ip (a Let's Encrypt certificate for the server's IP address: about 6 days, renewed automatically)"
	case tlsconf.ModeManual:
		return "manual (your own certificate)"
	}
	return string(m)
}

// printTLS writes the HTTPS state as `ssl status` shows it.
func printTLS(w io.Writer, v *domain.TLSView, now time.Time) {
	row := func(k, v string) { fmt.Fprintf(w, "%-15s %s\n", k+":", v) }
	row("Mode", modeText(v.Mode))
	if v.IP != "" {
		row("Server IP", v.IP)
	}
	if c := v.Cert; c != nil {
		row("Certificate", fmt.Sprintf("%s, by %s, until %s (%s)", orNone(c.Subject), orNone(c.Issuer), c.NotAfter.Format(time.DateOnly),
			daysLeft(now, c.NotAfter)))
	}
	row("Domain", orNone(v.Domain))
	if v.ZarinpalHost != "" {
		row("Zarinpal host", v.ZarinpalHost)
	}
	if v.Problem != nil {
		row("Not in use", v.Problem.Message+" ("+v.Problem.Code+")")
		if v.EffectiveMode != "" {
			row("Serving", string(v.EffectiveMode))
		}
	}
	row("Dashboard", orNone(v.PublicURL))
	switch a := v.Agent; {
	case a == nil:
		row("Applier", "not set up here (no tls-agent volume)")
	case a.Generation == 0:
		row("Applier", "nothing asked yet")
	case !a.Answered:
		row("Applier", fmt.Sprintf("request %d is waiting for the tls-agent", a.Generation))
	case a.OK:
		row("Applier", fmt.Sprintf("request %d applied at %s UTC", a.Generation, a.AppliedAt.UTC().Format(time.DateTime)))
	default:
		row("Applier", fmt.Sprintf("request %d not applied (%s): %s", a.Generation, a.Code, a.Error))
	}
	if len(v.Addresses) == 0 {
		return
	}
	fmt.Fprintln(w, "Addresses:")
	for _, a := range v.Addresses {
		fmt.Fprintf(w, "  %-30s %-9s %s\n", a.Host, addressState(a), addressDetail(a, now))
	}
}

func addressState(a domain.TLSAddress) string {
	switch {
	case !a.Served:
		return "none"
	case !a.Valid:
		return "invalid"
	case a.Expiring:
		return "expiring"
	}
	return "ok"
}

func addressDetail(a domain.TLSAddress, now time.Time) string {
	if a.Cert == nil {
		return a.Error
	}
	s := fmt.Sprintf("by %s, until %s (%s)", orNone(a.Cert.Issuer), a.Cert.NotAfter.Format(time.DateOnly), daysLeft(now, a.Cert.NotAfter))
	switch {
	case a.Trusted == nil:
	case *a.Trusted:
		s += ", publicly trusted"
	default:
		s += ", not publicly trusted"
	}
	if a.Error != "" {
		s += ": " + a.Error
	}
	return s
}
