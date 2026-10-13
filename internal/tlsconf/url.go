package tlsconf

import "strings"

// PublicURL is where staff open the dashboard: every login link and ticket
// link starts with it.
//   - env (BOBRES_PUBLIC_URL, already checked) wins when set.
//   - In ip mode, https://<ip>, but only while ipProven. The caller proves
//     two things: the live status shows a publicly trusted certificate,
//     served by this store's Caddy, whose IP SAN is the configured address
//     (Let's Encrypt issued it after reaching that address), and the bot
//     has just detected the server's public address and it is that same one.
//     The certificate alone is not enough: Caddy keeps serving the
//     short-lived certificate for days after the address changed hands, and
//     the next holder of a recycled address could read a one-time login
//     token from a link that still pointed at it. A typo, an address brought
//     back by a restore on another server, a certificate not issued yet, or
//     an address that has since moved keep the links on the domain.
//   - Otherwise https://<domain> (an IP address in brackets when IPv6).
//   - "" when there is no domain: no links are sent.
//
// st should be checked (State.Check).
func PublicURL(st State, env string, ipProven bool) string {
	if env = strings.TrimRight(strings.TrimSpace(env), "/"); env != "" {
		return env
	}
	if st.Mode == ModeIP && st.IP != "" && ipProven {
		return "https://" + HostPort(st.IP)
	}
	if st.Domain == "" {
		return ""
	}
	return "https://" + HostPort(st.Domain)
}
