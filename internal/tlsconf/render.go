package tlsconf

import (
	"fmt"
	"strings"
)

// The HTTPS files as Caddy sees them. Caddy mounts the install's tls/
// directory at Dir read-only; the tls-agent mounts it read-write and is
// its only writer. The main Caddyfile imports GlobalPath inside its global
// options and SitesPath at the top level, as exact files: the agent always
// writes both (the CLI writes auto-mode defaults when they are missing), so
// a missing sites file stops Caddy loudly instead of serving nothing.
const (
	Dir        = "/tls"
	GlobalFile = "global.caddy"
	SitesFile  = "sites.caddy"
	// CertsDir holds manual mode's pair.
	CertsDir = "certs"
	CertFile = "cert.pem"
	KeyFile  = "key.pem"

	GlobalPath = Dir + "/" + GlobalFile
	SitesPath  = Dir + "/" + SitesFile
	// CertPath and KeyPath are what a manual mode snippet names.
	CertPath = Dir + "/" + CertsDir + "/" + CertFile
	KeyPath  = Dir + "/" + CertsDir + "/" + KeyFile

	// SiteSnippet is the snippet every site imports: the main Caddyfile
	// defines it with everything a BOBRES site serves.
	SiteSnippet = "bobres_site"
)

// Files are the rendered HTTPS files.
type Files struct {
	// Global goes into GlobalFile (inside Caddy's global options); it may
	// hold only comments.
	Global string
	// Sites goes into SitesFile (top level).
	Sites string
	// Manual: Sites names CertPath and KeyPath, so the pair is written
	// with the snippets (before them).
	Manual bool
}

const fileHeader = "# Written by the BOBRES tls-agent from the store's HTTPS setting (the dashboard's\n" +
	"# System page, or sudo bobres ssl). Changes made here are overwritten.\n"

// Render returns the HTTPS files for st, which it checks first (Check).
//   - Every mode: a site for the domain, and one for the Zarinpal host that
//     answers only the pay page and the return URL (the rest is 404).
//   - auto mode with an IP address as the domain (a public one, or a private
//     address of a lab or LAN install): its site asks Caddy's own CA for the
//     certificate (tls internal), the only one such an address can get
//     there.
//   - ip mode: a site for the IP address with a Let's Encrypt short-lived
//     certificate; when the domain is that IP, it is the only site for it.
//   - manual mode: the domain's site loads CertPath and KeyPath.
//   - Whenever an IP address is served (ip mode, or a domain that is an IP
//     address), the global options get default_sni with it: clients send no
//     SNI to an IP address, and behind Docker's port publishing Caddy would
//     otherwise find no certificate for the connection.
func Render(st State) (Files, error) {
	st, err := st.Check()
	if err != nil {
		return Files{}, err
	}
	var g, s strings.Builder
	g.WriteString(fileHeader)
	fmt.Fprintf(&g, "# Mode: %s\n", st.Mode)
	if sni := st.defaultSNI(); sni != "" {
		fmt.Fprintf(&g, "default_sni %s\n", sni)
	}

	s.WriteString(fileHeader)
	fmt.Fprintf(&s, "# Mode: %s\n", st.Mode)
	switch {
	case st.Mode == ModeIP && st.Domain == st.IP:
		ipSite(&s, st.IP)
	case st.Mode == ModeIP:
		site(&s, "", st.Domain, nil)
		ipSite(&s, st.IP)
	case st.Mode == ModeManual:
		site(&s, "Your own certificate and key (manual mode).", st.Domain, []string{"tls " + CertPath + " " + KeyPath})
	case IsIP(st.Domain):
		site(&s, "An IP address: Caddy's own CA signs its certificate (browsers do not trust it).", st.Domain, []string{"tls internal"})
	default:
		site(&s, "", st.Domain, nil)
	}
	if st.ZarinpalHost != "" {
		site(&s, "The Zarinpal domain: only the pay page and the return URL.", st.ZarinpalHost, []string{
			"@bobres_not_zarinpal {",
			"\tnot path /pay/* /webhooks/zarinpal",
			"}",
			"handle @bobres_not_zarinpal {",
			"\trespond \"Not found\" 404",
			"}",
		})
	}
	return Files{Global: g.String(), Sites: s.String(), Manual: st.Mode == ModeManual}, nil
}

// defaultSNI is the IP address served to clients that send no SNI, if any.
func (st State) defaultSNI() string {
	switch {
	case st.Mode == ModeIP:
		return st.IP
	case IsIP(st.Domain):
		return st.Domain
	}
	return ""
}

// ipSite writes the site for ip mode's address.
func ipSite(b *strings.Builder, ip string) {
	site(b, "The server's IP address: a Let's Encrypt short-lived certificate (160 hours), renewed by Caddy.", ip, []string{
		"tls {",
		"\tissuer acme {",
		"\t\tprofile shortlived",
		"\t}",
		"}",
	})
}

// site writes one site block: a comment, the address, its own lines (one
// tab in), then the shared snippet.
func site(b *strings.Builder, comment, addr string, lines []string) {
	b.WriteString("\n")
	if comment != "" {
		fmt.Fprintf(b, "# %s\n", comment)
	}
	fmt.Fprintf(b, "%s {\n", HostPort(addr))
	for _, l := range lines {
		fmt.Fprintf(b, "\t%s\n", l)
	}
	fmt.Fprintf(b, "\timport %s\n}\n", SiteSnippet)
}

// Addresses are the addresses st serves: the domain, the IP address in ip
// mode (unless it is the domain), the Zarinpal host. st must be checked.
func (st State) Addresses() []Address {
	out := []Address{{Host: st.Domain, Role: RoleDomain, Expect: st.expect(st.Domain, st.Mode == ModeManual)}}
	if st.Mode == ModeIP {
		if st.IP == st.Domain {
			out[0].Expect = ExpectPublic
		} else {
			out = append(out, Address{Host: st.IP, Role: RoleIP, Expect: ExpectPublic})
		}
	}
	if st.ZarinpalHost != "" {
		out = append(out, Address{Host: st.ZarinpalHost, Role: RoleZarinpal, Expect: st.expect(st.ZarinpalHost, false)})
	}
	return out
}

// Address is one address the store is served on.
type Address struct {
	Host string
	// Role is RoleDomain, RoleIP or RoleZarinpal.
	Role string
	// Expect is the certificate Caddy should serve there: ExpectPublic,
	// ExpectInternal or ExpectManual.
	Expect string
}

// Address roles.
const (
	RoleDomain   = "domain"
	RoleIP       = "ip"
	RoleZarinpal = "zarinpal"
)

// What an address should be served with.
const (
	// ExpectPublic: a publicly trusted certificate Caddy gets from a public
	// CA (a host name, or ip mode's address).
	ExpectPublic = "public"
	// ExpectInternal: a certificate from Caddy's own local CA, which no
	// browser trusts: what Caddy gives an IP address outside ip mode, and
	// names such as *.localhost.
	ExpectInternal = "internal"
	// ExpectManual: the operator's own certificate (manual mode).
	ExpectManual = "manual"
)

// expect is what Caddy serves h with (outside ip mode's own address):
// manual for the domain in manual mode, its local CA for an IP address and
// the names it never asks a public CA for, else a public certificate.
func (st State) expect(h string, manual bool) string {
	switch {
	case manual:
		return ExpectManual
	case IsIP(h) || internalName(h):
		return ExpectInternal
	}
	return ExpectPublic
}

// internalName: names Caddy never asks a public CA for (certmagic's
// SubjectIsInternal).
func internalName(h string) bool {
	for _, suffix := range []string{".localhost", ".local", ".internal", ".home.arpa"} {
		if strings.HasSuffix(h, suffix) {
			return true
		}
	}
	return h == "localhost"
}
