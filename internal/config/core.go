package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Core is the core service configuration.
type Core struct {
	Common
	// ServiceToken is core's own token, presented to payments
	// (BOBRES_TOKEN_CORE in .env). Core presents a different token to each
	// peer, so no peer holds a token another one accepts as core.
	ServiceToken string
	// ProvisionerToken is what core presents to provisioner
	// (BOBRES_PROVISIONER_TOKEN; BOBRES_TOKEN_CORE_PROVISIONER in .env), and
	// BotInternalToken what it presents to the bot's /internal endpoints
	// (BOBRES_BOT_INTERNAL_TOKEN; BOBRES_TOKEN_CORE_BOT in .env). Unset, each
	// is ServiceToken (a configuration from before them; the compose file
	// always sets both, and core warns while one is missing).
	ProvisionerToken string
	BotInternalToken string
	// BotToken is the bot's service token; core accepts calls carrying it.
	BotToken            string
	PaymentsGRPCAddr    string
	ProvisionerGRPCAddr string
	// MasterKey encrypts secrets core stores at rest (dashboard TOTP secrets).
	// Optional until the dashboard needs it; 32+ characters when set.
	MasterKey string
	// AdminTelegramIDs are the root admins (BOBRES_ADMIN_TELEGRAM_ID, one id or
	// several separated by commas): each becomes an owner on first contact
	// with the bot or when core starts. The first one is the primary.
	AdminTelegramIDs []int64
	// PublicURL is where staff open the dashboard (login links point there):
	// https://<BOBRES_DOMAIN>, or BOBRES_PUBLIC_URL for a local test setup.
	// Core's links follow the HTTPS mode (domain.TLS.PublicURL); this is the
	// fallback without it.
	PublicURL string
	// PublicURLEnv is BOBRES_PUBLIC_URL as set (checked), "" when unset:
	// when set it is every dashboard link, whatever the HTTPS mode.
	PublicURLEnv string
	// BotURL is the bot's internal HTTP address (http://bot:8080): core
	// fetches receipt photos through it for the dashboard. Empty = none.
	BotURL string
	// PaymentsURL, ProvisionerURL and BackupURL are the other services'
	// internal HTTP addresses; the System page asks their /healthz and
	// /readyz. The defaults match the compose file. BackupURL has none: the
	// backup service is optional (a compose profile), and compose sets it
	// only where the service runs; unset, it shows as not installed without
	// a network lookup.
	PaymentsURL    string
	ProvisionerURL string
	BackupURL      string
	// BackupToken is what core presents to the backup service
	// (BOBRES_BACKUP_TOKEN; BOBRES_TOKEN_CORE_BACKUP in .env): a token for
	// that service only, so the backup container, which holds the master
	// keys, never holds core's own token. Empty = no backups tab.
	BackupToken string
	// Domain is BOBRES_DOMAIN as it is (no scheme): a license is bound to it.
	Domain string
	// SellerContact is BOBRES_SELLER_CONTACT: whom the owner is told to ask to
	// buy or renew a license (a Telegram handle, an address, a few words;
	// "" = "ask the seller you got BOBRES from"). It is a setting, never a
	// secret.
	SellerContact string
	// Timezone is BOBRES_TIMEZONE as set ("" when unset): the store's time
	// zone when the general.timezone setting is empty. The sales reports read
	// their days in it; compose gives the same value to the bot and the backup
	// service. It is not checked here: a name core cannot load is skipped with
	// a warning where it is used, and never stops core.
	Timezone string
	// ZarinpalHost is BOBRES_ZARINPAL_HOST, the second domain the HTTPS
	// files serve the pay page on ("" = none).
	ZarinpalHost string
	// TLSRequestDir and TLSResultDir are the tls-agent's volumes as core
	// mounts them: BOBRES_TLS_REQUEST_DIR (default /request, read-write:
	// core writes its request there) and BOBRES_TLS_RESULT_DIR (default
	// /tls-result, read-only: the agent's answer). HTTPS changes are off
	// when the request directory does not exist.
	TLSRequestDir string
	TLSResultDir  string
	// TLSProbeAddr is where core reads the certificates Caddy serves, for
	// the HTTPS status and alerts: BOBRES_TLS_PROBE_ADDR (default caddy:443,
	// over the edge network).
	TLSProbeAddr string
	// MonitorDir is where core reads the host collector's snapshot
	// (BOBRES_MONITOR_DIR; compose mounts the install's run/monitor there,
	// read-only). Empty: the host is not monitored (an install from before the
	// collector, or a development run).
	MonitorDir string
}

// LoadCore reads the core service configuration, failing closed on missing
// tokens or peer addresses.
func LoadCore() (Core, error) {
	var errs []error
	keep := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	c, err := Load("core")
	keep(err)
	co := Core{Common: c}
	keep(requireNonEmpty("BOBRES_DATABASE_URL", co.DatabaseURL))
	co.ServiceToken, err = requireToken("BOBRES_SERVICE_TOKEN", c.Env)
	keep(err)
	co.BotToken, err = requireToken("BOBRES_PEER_BOT_TOKEN", c.Env)
	keep(err)
	co.BackupToken, err = optionalToken("BOBRES_BACKUP_TOKEN", c.Env)
	keep(err)
	co.ProvisionerToken, err = optionalToken("BOBRES_PROVISIONER_TOKEN", c.Env)
	keep(err)
	co.BotInternalToken, err = optionalToken("BOBRES_BOT_INTERNAL_TOKEN", c.Env)
	keep(err)
	keep(requireDistinct(map[string]string{"BOBRES_SERVICE_TOKEN": co.ServiceToken, "BOBRES_PEER_BOT_TOKEN": co.BotToken,
		"BOBRES_BACKUP_TOKEN": co.BackupToken, "BOBRES_PROVISIONER_TOKEN": co.ProvisionerToken,
		"BOBRES_BOT_INTERNAL_TOKEN": co.BotInternalToken}))
	if co.ProvisionerToken == "" {
		co.ProvisionerToken = co.ServiceToken
	}
	if co.BotInternalToken == "" {
		co.BotInternalToken = co.ServiceToken
	}
	co.PaymentsGRPCAddr = getenv("BOBRES_PAYMENTS_GRPC_ADDR", "")
	keep(requireNonEmpty("BOBRES_PAYMENTS_GRPC_ADDR", co.PaymentsGRPCAddr))
	co.ProvisionerGRPCAddr = getenv("BOBRES_PROVISIONER_GRPC_ADDR", "")
	keep(requireNonEmpty("BOBRES_PROVISIONER_GRPC_ADDR", co.ProvisionerGRPCAddr))
	co.MasterKey, err = optionalMasterKey()
	keep(err)
	co.AdminTelegramIDs, err = telegramIDs("BOBRES_ADMIN_TELEGRAM_ID")
	keep(err)
	co.PublicURLEnv, err = publicURLEnv()
	keep(err)
	co.PublicURL = publicURL(co.PublicURLEnv)
	co.BotURL, err = peerURL("BOBRES_BOT_URL", "", "http://bot:8080")
	keep(err)
	co.PaymentsURL, err = peerURL("BOBRES_PAYMENTS_URL", "http://payments:8080", "http://payments:8080")
	keep(err)
	co.ProvisionerURL, err = peerURL("BOBRES_PROVISIONER_URL", "http://provisioner:8080", "http://provisioner:8080")
	keep(err)
	co.BackupURL, err = peerURL("BOBRES_BACKUP_URL", "", "http://backup:8080")
	keep(err)
	co.Domain = strings.TrimSpace(getenv("BOBRES_DOMAIN", ""))
	co.SellerContact = strings.TrimSpace(getenv("BOBRES_SELLER_CONTACT", ""))
	co.Timezone = strings.TrimSpace(getenv("BOBRES_TIMEZONE", ""))
	co.ZarinpalHost = strings.TrimSpace(getenv("BOBRES_ZARINPAL_HOST", ""))
	co.TLSRequestDir = getenv("BOBRES_TLS_REQUEST_DIR", "/request")
	co.TLSResultDir = getenv("BOBRES_TLS_RESULT_DIR", "/tls-result")
	co.MonitorDir = strings.TrimSpace(getenv("BOBRES_MONITOR_DIR", ""))
	if co.MonitorDir != "" && !strings.HasPrefix(co.MonitorDir, "/") {
		keep(errors.New("BOBRES_MONITOR_DIR must be an absolute path like /run/bobres-monitor"))
	}
	co.TLSProbeAddr = getenv("BOBRES_TLS_PROBE_ADDR", "caddy:443")
	if _, _, err := net.SplitHostPort(co.TLSProbeAddr); err != nil {
		keep(errors.New("BOBRES_TLS_PROBE_ADDR must be host:port like caddy:443"))
	}
	return co, errors.Join(errs...)
}

// peerURL reads another service's internal address (def when unset): an
// http(s) URL with a host, no trailing slash.
func peerURL(name, def, example string) (string, error) {
	v := strings.TrimRight(strings.TrimSpace(getenv(name, def)), "/")
	if v == "" {
		return "", nil
	}
	if u, err := url.Parse(v); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%s must be an http(s) URL like %s", name, example)
	}
	return v, nil
}

// publicURLEnv is BOBRES_PUBLIC_URL when set (an http(s) origin), else
// empty.
func publicURLEnv() (string, error) {
	v := strings.TrimRight(strings.TrimSpace(getenv("BOBRES_PUBLIC_URL", "")), "/")
	if v == "" {
		return "", nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("BOBRES_PUBLIC_URL must be an http(s) origin like https://panel.example.com")
	}
	return v, nil
}

// publicURL is BOBRES_PUBLIC_URL when set, else https://BOBRES_DOMAIN,
// else empty (no dashboard links).
func publicURL(env string) string {
	if env != "" {
		return env
	}
	if d := strings.TrimSpace(getenv("BOBRES_DOMAIN", "")); d != "" {
		return "https://" + d
	}
	return ""
}

// Payments is the payments service configuration.
type Payments struct {
	Common
	// CoreToken is core's service token; payments accepts only core.
	CoreToken string
	// Zarinpal is enabled when ZarinpalMerchantID is set.
	ZarinpalMerchantID string
	ZarinpalSandbox    bool
	// ZarinpalProxy routes Zarinpal API calls through an http(s) proxy whose IP
	// is registered in the Zarinpal panel (Zarinpal accepts registered IPs only).
	ZarinpalProxy string
	// ZarinpalPublicURL is the domain registered with Zarinpal, serving the pay
	// page and the callback; it must be reachable from Iran without a VPN.
	ZarinpalPublicURL string
	// IDPay and NextPay (API-key gateways, like Zarinpal off until the key is
	// set). They return the customer to the same public URL; the callback
	// domain must also be the one registered in the gateway's panel. NextPay
	// has no sandbox.
	IDPayAPIKey, IDPayProxy, NextPayAPIKey, NextPayProxy string
	IDPaySandbox                                         bool
	// MasterKey encrypts the gateway settings stored in the database (the
	// merchant id, the API keys and the proxy URLs). Optional: without it the Zarinpal* values
	// above are used as they are and cannot be changed from the dashboard. With
	// it they only seed the database once, when it has no settings yet.
	MasterKey string
}

// LoadPayments reads the payments service configuration.
func LoadPayments() (Payments, error) {
	var errs []error
	c, err := Load("payments")
	if err != nil {
		errs = append(errs, err)
	}
	p := Payments{Common: c}
	if err := requireNonEmpty("BOBRES_DATABASE_URL", p.DatabaseURL); err != nil {
		errs = append(errs, err)
	}
	if p.CoreToken, err = requireToken("BOBRES_PEER_CORE_TOKEN", c.Env); err != nil {
		errs = append(errs, err)
	}
	p.ZarinpalMerchantID = strings.TrimSpace(getenv("BOBRES_ZARINPAL_MERCHANT_ID", ""))
	p.ZarinpalSandbox = getenv("BOBRES_ZARINPAL_SANDBOX", "") == "true"
	if p.ZarinpalSandbox && c.Env == "prod" {
		// In the sandbox any merchant id works and the pay page needs no card:
		// customers would get service for free.
		errs = append(errs, errors.New("BOBRES_ZARINPAL_SANDBOX is refused when BOBRES_ENV=prod"))
	}
	p.ZarinpalProxy = getenv("BOBRES_ZARINPAL_PROXY", "")
	p.ZarinpalPublicURL = strings.TrimRight(getenv("BOBRES_ZARINPAL_PUBLIC_URL", getenv("BOBRES_PUBLIC_URL", "")), "/")
	p.IDPayAPIKey = strings.TrimSpace(getenv("BOBRES_IDPAY_API_KEY", ""))
	p.IDPayProxy = getenv("BOBRES_IDPAY_PROXY", "")
	p.IDPaySandbox = getenv("BOBRES_IDPAY_SANDBOX", "") == "true"
	if p.IDPaySandbox && c.Env == "prod" {
		// In the sandbox any payment "succeeds": customers would get service for free.
		errs = append(errs, errors.New("BOBRES_IDPAY_SANDBOX is refused when BOBRES_ENV=prod"))
	}
	p.NextPayAPIKey = strings.TrimSpace(getenv("BOBRES_NEXTPAY_API_KEY", ""))
	p.NextPayProxy = getenv("BOBRES_NEXTPAY_PROXY", "")
	if p.ZarinpalMerchantID != "" || p.IDPayAPIKey != "" || p.NextPayAPIKey != "" {
		if err := CheckZarinpalPublicURL(p.ZarinpalPublicURL, c.Env); err != nil {
			errs = append(errs, err)
		}
	}
	if p.MasterKey, err = optionalMasterKey(); err != nil {
		errs = append(errs, err)
	}
	return p, errors.Join(errs...)
}

// CheckZarinpalPublicURL reports why u cannot be where Zarinpal returns the
// customer: it must be https://host[:port] (plain http only in dev). Payments
// also asks it at runtime, when Zarinpal is switched on from the dashboard.
func CheckZarinpalPublicURL(u, env string) error {
	pu, err := url.Parse(u)
	switch {
	case err != nil || pu.Host == "" || (pu.Scheme != "https" && (pu.Scheme != "http" || env != "dev")):
		return errors.New("BOBRES_ZARINPAL_PUBLIC_URL (or BOBRES_PUBLIC_URL) must be the https URL of the domain registered with Zarinpal")
	case pu.Path != "" || pu.RawQuery != "":
		return errors.New("BOBRES_ZARINPAL_PUBLIC_URL must be just https://host[:port]")
	}
	return nil
}

// Provisioner is the provisioner service configuration.
type Provisioner struct {
	Common
	// CoreToken is core's service token; provisioner accepts only core.
	CoreToken string
	// MasterKey encrypts 3x-ui API tokens at rest (envelope).
	MasterKey string
	// XUI* register the first 3x-ui panel on startup when no panel exists yet
	// (installer input). Later changes go through the AddServer RPC.
	XUIURL          string
	XUIToken        string
	XUISubURL       string
	XUIAllowPrivate bool
}

// LoadProvisioner reads the provisioner service configuration.
func LoadProvisioner() (Provisioner, error) {
	var errs []error
	keep := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	c, err := Load("provisioner")
	keep(err)
	p := Provisioner{Common: c}
	keep(requireNonEmpty("BOBRES_DATABASE_URL", p.DatabaseURL))
	p.CoreToken, err = requireToken("BOBRES_PEER_CORE_TOKEN", c.Env)
	keep(err)
	p.MasterKey, err = optionalMasterKey()
	keep(err)
	if p.MasterKey == "" {
		keep(errors.New("BOBRES_MASTER_KEY is required (32+ characters)"))
	}
	p.XUIURL = getenv("BOBRES_XUI_URL", "")
	p.XUIToken, err = Secret("BOBRES_XUI_TOKEN")
	keep(err)
	if (p.XUIURL == "") != (p.XUIToken == "") {
		keep(errors.New("BOBRES_XUI_URL and BOBRES_XUI_TOKEN must be set together"))
	}
	p.XUISubURL = getenv("BOBRES_XUI_SUB_URL", "")
	p.XUIAllowPrivate = getenv("BOBRES_XUI_ALLOW_PRIVATE", "") == "true"
	return p, errors.Join(errs...)
}
