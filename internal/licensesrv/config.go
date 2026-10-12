package licensesrv

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

// Default rates: an install refreshes once a day and activates rarely, but
// many installs may reach the server through one shared proxy.
const (
	DefaultIPRate  = "120/1h"
	DefaultKeyRate = "10/1h"
)

// Config is the server's environment (docs/13-license-server.md).
type Config struct {
	DatabaseURL string
	// SigningKeyFile is the online key, made by tools/license keygen and
	// kept outside any repository (serve only).
	SigningKeyFile string
	TrustedProxies []netip.Prefix
	IPRate         Rate
	KeyRate        Rate
	AllowHTTP      bool
}

// LoadConfig reads the environment. needKey is false for the admin
// commands, which only use the database.
func LoadConfig(needKey bool) (Config, error) {
	var errs []error
	var c Config
	var err error
	if c.DatabaseURL, err = config.Secret("BOBRES_DATABASE_URL"); err != nil {
		errs = append(errs, err)
	} else if c.DatabaseURL == "" {
		errs = append(errs, errors.New("BOBRES_DATABASE_URL (or BOBRES_DATABASE_URL_FILE) is required"))
	}
	c.SigningKeyFile = os.Getenv("BOBRES_LICENSESRV_SIGNING_KEY_FILE")
	if needKey && c.SigningKeyFile == "" {
		errs = append(errs, errors.New("BOBRES_LICENSESRV_SIGNING_KEY_FILE is required: the path of the online signing key (tools/license keygen)"))
	}
	if c.TrustedProxies, err = parsePrefixes(getenv("BOBRES_LICENSESRV_TRUSTED_PROXIES", "127.0.0.1/32,::1/128")); err != nil {
		errs = append(errs, fmt.Errorf("BOBRES_LICENSESRV_TRUSTED_PROXIES: %w", err))
	}
	if c.IPRate, err = ParseRate(getenv("BOBRES_LICENSESRV_RATE_IP", DefaultIPRate)); err != nil {
		errs = append(errs, fmt.Errorf("BOBRES_LICENSESRV_RATE_IP: %w", err))
	}
	if c.KeyRate, err = ParseRate(getenv("BOBRES_LICENSESRV_RATE_KEY", DefaultKeyRate)); err != nil {
		errs = append(errs, fmt.Errorf("BOBRES_LICENSESRV_RATE_KEY: %w", err))
	}
	switch v := os.Getenv("BOBRES_LICENSESRV_ALLOW_HTTP"); v {
	case "", "false", "0":
	case "true", "1":
		c.AllowHTTP = true
	default:
		errs = append(errs, fmt.Errorf("BOBRES_LICENSESRV_ALLOW_HTTP must be true or false, got %q", v))
	}
	return c, errors.Join(errs...)
}

// parsePrefixes reads a comma-separated list of CIDRs or single addresses.
func parsePrefixes(s string) ([]netip.Prefix, error) {
	out := []netip.Prefix{}
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not an address or CIDR", f)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
