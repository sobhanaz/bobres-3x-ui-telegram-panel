package main

import (
	"net/url"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
)

// storeDomain is the name an owner types to confirm a restore of a backup from the dashboard: the
// store's domain (BOBRES_DOMAIN), or, on a local test setup that has none, the host of
// BOBRES_PUBLIC_URL. "" means core knows neither, and restoring from the dashboard is refused.
func storeDomain(cfg config.Core) string {
	if d := strings.ToLower(strings.TrimSpace(cfg.Domain)); d != "" {
		return d
	}
	if u, err := url.Parse(cfg.PublicURLEnv); err == nil {
		return strings.ToLower(u.Hostname())
	}
	return ""
}
