package domain

import (
	"net/netip"
	"net/url"
	"strings"
)

// MiniAppOn reads the Telegram Mini App store's switch (miniapp.enabled): on
// unless the owner turned it off, so a store that never set it has it.
func MiniAppOn(settings map[string]string) bool { return settings["miniapp.enabled"] != "false" }

// MiniAppURL is the address Telegram opens the Mini App from, built from the
// store's public origin (https://host[:port]), or "" when Telegram could not
// reach it: not https, no host, or a local or private one (localhost, a .local
// name, a private or loopback IP).
func MiniAppURL(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || !publicHost(strings.ToLower(u.Hostname())) {
		return ""
	}
	return "https://" + u.Host + "/app/"
}

// cgnat is the shared address space of carrier-grade NAT, which is not public either.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// publicHost reports a host name or IP address that is meant to be reached
// from the internet.
func publicHost(h string) bool {
	if h == "" {
		return false
	}
	if ip, err := netip.ParseAddr(h); err == nil {
		ip = ip.Unmap()
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() || cgnat.Contains(ip))
	}
	if !strings.Contains(h, ".") || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") {
		return false // a single label, or malformed
	}
	for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".test", ".invalid", ".home.arpa"} {
		if strings.HasSuffix(h, suffix) {
			return false
		}
	}
	return h != "localhost"
}
