// Package tlsconf is the HTTPS manager's engine: the three modes, the
// Caddyfile snippets each one renders, the checks on an operator's own
// certificate and key, and the dashboard's public address. Core and the
// tls-agent both use it: core validates a change before it asks for it, and
// the agent validates the request again before it writes anything (it
// trusts nothing core sends).
//
// Caddy's main Caddyfile is static. Inside its global options it imports
// /tls/global.caddy, at the top level /tls/sites.caddy; the site blocks in
// sites.caddy import the (bobres_site) snippet the main Caddyfile defines.
// Every value written into a snippet passes Check first: a host name as the
// installer accepts it, or an IP address in canonical form, never raw text.
//
// Apart from the request and result files (protocol.go), nothing here does
// I/O.
package tlsconf

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
)

// Mode is how the store's addresses get their certificates.
type Mode string

const (
	// ModeAuto: Caddy's automatic HTTPS for BOBRES_DOMAIN (and the Zarinpal
	// host).
	ModeAuto Mode = "auto"
	// ModeIP: the store is also served on the server's public IP address,
	// with a Let's Encrypt short-lived certificate (160 hours, renewed by
	// Caddy). A host name domain keeps its own automatic site.
	ModeIP Mode = "ip"
	// ModeManual: the operator's certificate chain and private key for
	// BOBRES_DOMAIN. The Zarinpal host keeps its own automatic site.
	ModeManual Mode = "manual"
)

// Modes lists the modes in the order they are offered.
var Modes = []Mode{ModeAuto, ModeIP, ModeManual}

// ParseMode reads a mode by its name.
func ParseMode(s string) (Mode, error) {
	for _, m := range Modes {
		if s == string(m) {
			return m, nil
		}
	}
	return "", problem(CodeBadMode, "the HTTPS mode must be auto, ip or manual")
}

// State is what the HTTPS files are rendered from.
type State struct {
	Mode Mode
	// IP is ip mode's public address (ignored in the other modes).
	IP string
	// Domain is BOBRES_DOMAIN: a host name, or an IP address. A private,
	// loopback or link-local address (a lab or LAN install the installer
	// accepts) is served with Caddy's own CA, which no browser trusts.
	Domain string
	// ZarinpalHost is BOBRES_ZARINPAL_HOST, the second domain the pay page
	// and the return URL are served on ("" = none).
	ZarinpalHost string
	// HasCert: manual mode's pair is written with the files.
	HasCert bool
}

// Problem is why a state, a certificate or a request cannot be used: a
// stable code the dashboard translates, and the same in English. A message
// never quotes a certificate or a key.
type Problem struct {
	Code    string
	Message string
}

func (p *Problem) Error() string { return p.Message }

func problem(code, msg string) *Problem { return &Problem{Code: code, Message: msg} }

// Problem codes of a state.
const (
	CodeBadMode         = "bad_mode"
	CodeNoDomain        = "no_domain"
	CodeBadDomain       = "bad_domain"
	CodeBadZarinpalHost = "bad_zarinpal_host"
	CodeIPRequired      = "ip_required"
	CodeBadIP           = "bad_ip"
	CodeIPNotPublic     = "ip_not_public"
	// CodeIPDomainMismatch: BOBRES_DOMAIN is an IP address and ip mode names
	// another one (only one address can answer clients that send no SNI).
	CodeIPDomainMismatch = "ip_domain_mismatch"
	CodeCertRequired     = "cert_required"
)

// Check validates st and returns it in the form the files use: host names
// in lower case, IP addresses in canonical form (IPv6 compressed, an
// IPv4-mapped address as IPv4), no IP outside ip mode, no Zarinpal host
// equal to the domain. The domain may be any IP address a client can
// connect to; ip mode's address must be a public one.
func (st State) Check() (State, error) {
	if _, err := ParseMode(string(st.Mode)); err != nil {
		return State{}, err
	}
	out := State{Mode: st.Mode, HasCert: st.HasCert}
	if strings.TrimSpace(st.Domain) == "" {
		return State{}, problem(CodeNoDomain, "this install has no domain (BOBRES_DOMAIN)")
	}
	d, err := host(st.Domain)
	if err != nil {
		return State{}, problem(CodeBadDomain, "the domain (BOBRES_DOMAIN) is neither a host name nor an IP address")
	}
	out.Domain = d
	if strings.TrimSpace(st.ZarinpalHost) != "" {
		z, err := host(st.ZarinpalHost)
		if err != nil || IsIP(z) {
			return State{}, problem(CodeBadZarinpalHost, "the Zarinpal domain (BOBRES_ZARINPAL_HOST) is not a host name")
		}
		if z != out.Domain {
			out.ZarinpalHost = z
		}
	}
	switch st.Mode {
	case ModeIP:
		if strings.TrimSpace(st.IP) == "" {
			return State{}, problem(CodeIPRequired, "ip mode needs the server's public IP address")
		}
		ip, err := PublicIP(st.IP)
		if err != nil {
			return State{}, err
		}
		if IsIP(out.Domain) && out.Domain != ip {
			return State{}, problem(CodeIPDomainMismatch,
				"the domain (BOBRES_DOMAIN) is the IP address "+out.Domain+": ip mode can only use that same address")
		}
		out.IP = ip
	case ModeManual:
		if !st.HasCert {
			return State{}, problem(CodeCertRequired, "manual mode needs a certificate and its private key")
		}
	}
	return out, nil
}

// Install returns the auto-mode state of an install: its BOBRES_DOMAIN and
// BOBRES_ZARINPAL_HOST, checked. It is what core asks for when nothing else
// is saved, and the only names the tls-agent accepts. A Zarinpal host that
// is not a host name is left out (zarinpalLeftOut): the rest is still
// served. A domain that cannot be served is an error.
func Install(domain, zarinpalHost string) (st State, zarinpalLeftOut bool, err error) {
	in := State{Mode: ModeAuto, Domain: domain, ZarinpalHost: zarinpalHost}
	st, err = in.Check()
	var p *Problem
	if errors.As(err, &p) && p.Code == CodeBadZarinpalHost {
		in.ZarinpalHost = ""
		st, err = in.Check()
		return st, err == nil, err
	}
	return st, false, err
}

// hostRe is the installer's host name rule (cmd/bobres domainRe), on a
// name already in lower case: labels of letters, digits and inner hyphens,
// at least two of them.
var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// host returns h as the files use it: an IP address in canonical form
// (hostAddr), or a host name in lower case that the installer accepts and
// whose last label is not all digits (that is an IP address, or nothing).
func host(h string) (string, error) {
	h = strings.TrimSpace(h)
	if a, err := netip.ParseAddr(h); err == nil {
		return hostAddr(a)
	}
	h = strings.ToLower(h)
	if len(h) > 253 || !hostRe.MatchString(h) {
		return "", problem(CodeBadDomain, "not a host name")
	}
	last := h[strings.LastIndexByte(h, '.')+1:]
	if strings.Trim(last, "0123456789") == "" {
		return "", problem(CodeBadDomain, "not a host name")
	}
	return h, nil
}

// hostAddr returns a as the domain's address in canonical form. Unlike ip
// mode's address (PublicIP) it need not be public: the installer accepts
// 192.168.1.10 for a lab or LAN install, and Caddy serves any address with
// its own CA. Refused are the addresses no client connects to: one with a
// zone, the unspecified and broadcast addresses, multicast.
func hostAddr(a netip.Addr) (string, error) {
	a = a.Unmap()
	if a.Zone() == "" && (a.IsGlobalUnicast() || a.IsLoopback() || a.IsLinkLocalUnicast()) {
		return a.String(), nil
	}
	return "", problem(CodeBadDomain, "not an IP address a client can connect to")
}

// IsIP reports whether s is an IP address (and not a host name).
func IsIP(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

// PublicIP returns s in canonical form when it is a public unicast address:
// not private, loopback, link-local, multicast, unspecified, carrier-grade
// NAT (100.64.0.0/10) or another special-purpose range. Let's Encrypt
// issues IP certificates only for such addresses.
func PublicIP(s string) (string, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "", problem(CodeBadIP, "not an IP address")
	}
	return publicAddr(a, allowTestNets)
}

// allowTestNets lets the documentation ranges pass as public: only this
// package's tests set it (their examples use those ranges).
var allowTestNets = false

func publicAddr(a netip.Addr, testNetsOK bool) (string, error) {
	if a.Zone() != "" {
		return "", problem(CodeBadIP, "an IP address with a zone is not a public address")
	}
	a = a.Unmap()
	notPublic := problem(CodeIPNotPublic, "the IP address "+a.String()+" is not a public address")
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return "", notPublic
	}
	for _, p := range specialNets {
		if p.Contains(a) {
			return "", notPublic
		}
	}
	if !testNetsOK {
		for _, p := range testNets {
			if p.Contains(a) {
				return "", notPublic
			}
		}
	}
	return a.String(), nil
}

// specialNets are ranges no public certificate can name, beyond those
// netip's IsGlobalUnicast and IsPrivate refuse (unspecified, loopback,
// multicast, link-local, broadcast; 10/8, 172.16/12, 192.168/16, fc00::/7).
var specialNets = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),  // carrier-grade NAT (RFC 6598)
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("100::/64"),       // discard-only
	netip.MustParsePrefix("2001::/23"),      // IETF protocol assignments (Teredo, benchmarking)
	netip.MustParsePrefix("5f00::/16"),      // SRv6 SIDs
	netip.MustParsePrefix("fec0::/10"),      // site-local (deprecated)
}

// testNets are the documentation ranges (RFC 5737, RFC 3849, RFC 9637).
var testNets = []netip.Prefix{
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("3fff::/20"),
}

// HostPort is h as a URL or site address writes it: an IPv6 address in
// brackets.
func HostPort(h string) string {
	if a, err := netip.ParseAddr(h); err == nil && a.Is6() {
		return "[" + h + "]"
	}
	return h
}
