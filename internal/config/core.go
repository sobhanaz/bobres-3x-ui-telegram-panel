package config

import (
	"errors"
	"net/url"
	"strings"
)

// Core is the core service configuration.
type Core struct {
	Common
	// ServiceToken is core's own token, presented to payments and provisioner.
	ServiceToken string
	// BotToken is the bot's service token; core accepts calls carrying it.
	BotToken            string
	PaymentsGRPCAddr    string
	ProvisionerGRPCAddr string
	// MasterKey encrypts secrets core stores at rest (dashboard TOTP secrets).
	// Optional until the dashboard needs it; 32+ characters when set.
	MasterKey string
	// AdminTelegramID becomes the owner on first contact with the bot.
	AdminTelegramID int64
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
	keep(requireDistinct(map[string]string{"BOBRES_SERVICE_TOKEN": co.ServiceToken, "BOBRES_PEER_BOT_TOKEN": co.BotToken}))
	co.PaymentsGRPCAddr = getenv("BOBRES_PAYMENTS_GRPC_ADDR", "")
	keep(requireNonEmpty("BOBRES_PAYMENTS_GRPC_ADDR", co.PaymentsGRPCAddr))
	co.ProvisionerGRPCAddr = getenv("BOBRES_PROVISIONER_GRPC_ADDR", "")
	keep(requireNonEmpty("BOBRES_PROVISIONER_GRPC_ADDR", co.ProvisionerGRPCAddr))
	co.MasterKey, err = optionalMasterKey()
	keep(err)
	co.AdminTelegramID, err = telegramID("BOBRES_ADMIN_TELEGRAM_ID")
	keep(err)
	return co, errors.Join(errs...)
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
	p.ZarinpalProxy = getenv("BOBRES_ZARINPAL_PROXY", "")
	p.ZarinpalPublicURL = strings.TrimRight(getenv("BOBRES_ZARINPAL_PUBLIC_URL", getenv("BOBRES_PUBLIC_URL", "")), "/")
	if p.ZarinpalMerchantID != "" {
		u, err := url.Parse(p.ZarinpalPublicURL)
		switch {
		case err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && c.Env == "dev")):
			errs = append(errs, errors.New("BOBRES_ZARINPAL_PUBLIC_URL (or BOBRES_PUBLIC_URL) must be the https URL of the domain registered with Zarinpal"))
		case u.Path != "" || u.RawQuery != "":
			errs = append(errs, errors.New("BOBRES_ZARINPAL_PUBLIC_URL must be just https://host[:port]"))
		}
	}
	return p, errors.Join(errs...)
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
