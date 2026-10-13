package config

import (
	"strings"
	"testing"
)

// The HTTPS manager's settings: the Zarinpal host, the tls-agent's volumes
// and where Caddy is dialed (the compose file's names by default), and
// BOBRES_PUBLIC_URL kept apart from the domain's address.
func TestLoadCoreTLS(t *testing.T) {
	baseEnv(t)
	t.Setenv("BOBRES_SERVICE_TOKEN", coreTok)
	t.Setenv("BOBRES_PEER_BOT_TOKEN", botTok)
	t.Setenv("BOBRES_PAYMENTS_GRPC_ADDR", "payments:9090")
	t.Setenv("BOBRES_PROVISIONER_GRPC_ADDR", "provisioner:9090")
	t.Setenv("BOBRES_DOMAIN", "panel.example.com")
	c, err := LoadCore()
	if err != nil {
		t.Fatal(err)
	}
	if c.TLSRequestDir != "/request" || c.TLSResultDir != "/tls-result" || c.TLSProbeAddr != "caddy:443" || c.ZarinpalHost != "" ||
		c.PublicURLEnv != "" || c.PublicURL != "https://panel.example.com" {
		t.Fatalf("defaults: %+v", c)
	}
	t.Setenv("BOBRES_ZARINPAL_HOST", " pay.example.net ")
	t.Setenv("BOBRES_TLS_REQUEST_DIR", "/tmp/req")
	t.Setenv("BOBRES_TLS_RESULT_DIR", "/tmp/res")
	t.Setenv("BOBRES_TLS_PROBE_ADDR", "127.0.0.1:8443")
	t.Setenv("BOBRES_PUBLIC_URL", "https://bobres.localhost:8443/")
	if c, err = LoadCore(); err != nil || c.ZarinpalHost != "pay.example.net" || c.TLSRequestDir != "/tmp/req" ||
		c.TLSResultDir != "/tmp/res" || c.TLSProbeAddr != "127.0.0.1:8443" || c.PublicURLEnv != "https://bobres.localhost:8443" ||
		c.PublicURL != "https://bobres.localhost:8443" {
		t.Fatalf("set: %+v %v", c, err)
	}
	t.Setenv("BOBRES_TLS_PROBE_ADDR", "caddy")
	if _, err := LoadCore(); err == nil || !strings.Contains(err.Error(), "BOBRES_TLS_PROBE_ADDR") {
		t.Fatalf("a probe address without a port: %v", err)
	}
	t.Setenv("BOBRES_TLS_PROBE_ADDR", "")
	t.Setenv("BOBRES_PUBLIC_URL", "ftp://x")
	if _, err := LoadCore(); err == nil || !strings.Contains(err.Error(), "BOBRES_PUBLIC_URL") {
		t.Fatalf("a bad public URL: %v", err)
	}
}
