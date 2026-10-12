package config

import (
	"strings"
	"testing"
)

// BOBRES_LICENSE_SERVER is optional, https only, kept without a trailing
// slash; anything else stops the bot from starting.
func TestBotLicenseServer(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_SERVICE_TOKEN", botTok)
	t.Setenv("BOBRES_CORE_GRPC_ADDR", "core:9090")
	t.Setenv("BOBRES_TELEGRAM_BOT_TOKEN", "123:abc")
	for value, want := range map[string]string{
		"":                                     "",
		" ":                                    "",
		"https://license.example.com":          "https://license.example.com",
		" https://license.example.com/ ":       "https://license.example.com",
		"https://example.com:8443/bobres/lic/": "https://example.com:8443/bobres/lic",
	} {
		t.Setenv("BOBRES_LICENSE_SERVER", value)
		if c, err := LoadBot(); err != nil || c.LicenseServer != want {
			t.Errorf("%q: %q %v", value, c.LicenseServer, err)
		}
	}
	for _, bad := range []string{"http://license.example.com", "license.example.com", "https://", "ftp://x",
		"https://user:pw@license.example.com", "https://license.example.com/?k=1", "https://license.example.com/#x",
		"https://license.example.com?"} {
		t.Setenv("BOBRES_LICENSE_SERVER", bad)
		if _, err := LoadBot(); err == nil || !strings.Contains(err.Error(), "BOBRES_LICENSE_SERVER must be an https URL") {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}
