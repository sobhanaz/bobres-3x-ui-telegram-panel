package tlsconf

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

type renderCase struct {
	name string
	st   State
}

// renderCases are every mode with a host name, a public IPv4 and IPv6 and a
// private IPv4 and IPv6 domain (a lab or LAN install: auto and manual only,
// ip mode needs a public address), with and without a Zarinpal host; ip mode
// with an IPv4 and an IPv6 address (another than a host name domain's, or
// the domain itself).
func renderCases() []renderCase {
	var out []renderCase
	domains := []string{"panel.example.com", "203.0.113.10", "2001:db8::10", "192.168.1.10", "fd00::5"}
	for _, mode := range Modes {
		for _, d := range domains {
			if _, err := PublicIP(d); mode == ModeIP && IsIP(d) && err != nil {
				continue
			}
			for _, zp := range []string{"", "pay.example.net"} {
				st := State{Mode: mode, Domain: d, ZarinpalHost: zp, HasCert: mode == ModeManual}
				ips := []string{""}
				if mode == ModeIP {
					ips = []string{"198.51.100.7", "2001:db8::20"}
					if IsIP(d) {
						ips = []string{d} // only the domain's own address
					}
				}
				for _, ip := range ips {
					st.IP = ip
					out = append(out, renderCase{name: fmt.Sprintf("%s/%s/zp=%q/ip=%q", mode, d, zp, ip), st: st})
				}
			}
		}
	}
	return out
}

// Every combination renders what it should: the sites, default_sni when an
// IP address is served, the pair in manual mode, the Zarinpal restriction.
func TestRenderEveryCombination(t *testing.T) {
	for _, tc := range renderCases() {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Render(tc.st)
			if err != nil {
				t.Fatal(err)
			}
			st := tc.st
			sni := ""
			switch {
			case st.Mode == ModeIP:
				sni = st.IP
			case IsIP(st.Domain):
				sni = st.Domain
			}
			if got := strings.Contains(f.Global, "default_sni"); got != (sni != "") {
				t.Fatalf("default_sni %v, want %q:\n%s", got, sni, f.Global)
			}
			if sni != "" && !strings.Contains(f.Global, "\ndefault_sni "+sni+"\n") {
				t.Fatalf("default_sni is not the bare %s:\n%s", sni, f.Global)
			}
			want := 1
			if st.Mode == ModeIP && st.IP != st.Domain {
				want++
			}
			if st.ZarinpalHost != "" {
				want++
			}
			if got := strings.Count(f.Sites, "\timport bobres_site\n}\n"); got != want {
				t.Fatalf("%d sites, want %d:\n%s", got, want, f.Sites)
			}
			if !strings.Contains(f.Sites, "\n"+HostPort(st.Domain)+" {\n") {
				t.Fatalf("no site for the domain:\n%s", f.Sites)
			}
			if st.Mode == ModeAuto && IsIP(st.Domain) {
				// An IP address gets its certificate from Caddy's own CA.
				if block := siteBlock(t, f.Sites, HostPort(st.Domain)); !strings.Contains(block, "\n\ttls internal\n") {
					t.Fatalf("the IP domain's site has no internal issuer:\n%s", block)
				}
			} else if strings.Contains(f.Sites, "tls internal") {
				t.Fatalf("an internal issuer outside an IP domain in auto mode:\n%s", f.Sites)
			}
			if st.Mode == ModeIP {
				block := siteBlock(t, f.Sites, HostPort(st.IP))
				if !strings.Contains(block, "\ttls {\n\t\tissuer acme {\n\t\t\tprofile shortlived\n\t\t}\n\t}\n") {
					t.Fatalf("the IP site has no short-lived issuer:\n%s", block)
				}
			} else if strings.Contains(f.Sites, "shortlived") {
				t.Fatalf("a short-lived issuer outside ip mode:\n%s", f.Sites)
			}
			hasPair := strings.Contains(f.Sites, "tls "+CertPath+" "+KeyPath)
			if hasPair != (st.Mode == ModeManual) || f.Manual != (st.Mode == ModeManual) {
				t.Fatalf("pair %v, manual %v:\n%s", hasPair, f.Manual, f.Sites)
			}
			if st.Mode == ModeManual && !strings.Contains(siteBlock(t, f.Sites, HostPort(st.Domain)), "tls "+CertPath) {
				t.Fatalf("the pair is not in the domain's site:\n%s", f.Sites)
			}
			if st.ZarinpalHost != "" {
				block := siteBlock(t, f.Sites, st.ZarinpalHost)
				if !strings.Contains(block, "not path /pay/* /webhooks/zarinpal") || strings.Contains(block, "tls ") {
					t.Fatalf("the Zarinpal site:\n%s", block)
				}
			} else if strings.Contains(f.Sites, "zarinpal") {
				t.Fatalf("a Zarinpal site without a host:\n%s", f.Sites)
			}
			for _, l := range strings.Split(f.Global+f.Sites, "\n") {
				if strings.Contains(l, "[") && !strings.HasPrefix(l, "[") && !strings.HasPrefix(l, "#") {
					t.Fatalf("a bracket inside a line: %q", l)
				}
			}
		})
	}
}

// siteBlock is the site block for addr in sites.
func siteBlock(t *testing.T, sites, addr string) string {
	t.Helper()
	i := strings.Index(sites, "\n"+addr+" {\n")
	if i < 0 {
		t.Fatalf("no site %s in:\n%s", addr, sites)
	}
	rest := sites[i+1:]
	return rest[:strings.Index(rest, "\n}\n")+3]
}

// The exact text of a few files: what Caddy reads.
func TestRenderText(t *testing.T) {
	header := fileHeader
	for _, tc := range []struct {
		name          string
		st            State
		global, sites string
	}{
		{"auto", State{Mode: ModeAuto, Domain: "Panel.Example.com"},
			header + "# Mode: auto\n",
			header + "# Mode: auto\n\npanel.example.com {\n\timport bobres_site\n}\n"},
		{"ip with a host name and Zarinpal", State{Mode: ModeIP, Domain: "panel.example.com", IP: "2001:DB8:0:0::20", ZarinpalHost: "pay.example.net"},
			header + "# Mode: ip\ndefault_sni 2001:db8::20\n",
			header + "# Mode: ip\n\npanel.example.com {\n\timport bobres_site\n}\n" +
				"\n# The server's IP address: a Let's Encrypt short-lived certificate (160 hours), renewed by Caddy.\n" +
				"[2001:db8::20] {\n\ttls {\n\t\tissuer acme {\n\t\t\tprofile shortlived\n\t\t}\n\t}\n\timport bobres_site\n}\n" +
				"\n# The Zarinpal domain: only the pay page and the return URL.\n" +
				"pay.example.net {\n\t@bobres_not_zarinpal {\n\t\tnot path /pay/* /webhooks/zarinpal\n\t}\n" +
				"\thandle @bobres_not_zarinpal {\n\t\trespond \"Not found\" 404\n\t}\n\timport bobres_site\n}\n"},
		{"ip on the domain's own address", State{Mode: ModeIP, Domain: "203.0.113.10", IP: "203.0.113.10"},
			header + "# Mode: ip\ndefault_sni 203.0.113.10\n",
			header + "# Mode: ip\n" +
				"\n# The server's IP address: a Let's Encrypt short-lived certificate (160 hours), renewed by Caddy.\n" +
				"203.0.113.10 {\n\ttls {\n\t\tissuer acme {\n\t\t\tprofile shortlived\n\t\t}\n\t}\n\timport bobres_site\n}\n"},
		{"manual on an IP domain", State{Mode: ModeManual, Domain: "203.0.113.10", HasCert: true},
			header + "# Mode: manual\ndefault_sni 203.0.113.10\n",
			header + "# Mode: manual\n\n# Your own certificate and key (manual mode).\n" +
				"203.0.113.10 {\n\ttls /tls/certs/cert.pem /tls/certs/key.pem\n\timport bobres_site\n}\n"},
		{"auto on an IP domain", State{Mode: ModeAuto, Domain: "2001:db8::10"},
			header + "# Mode: auto\ndefault_sni 2001:db8::10\n",
			header + "# Mode: auto\n\n# An IP address: Caddy's own CA signs its certificate (browsers do not trust it).\n" +
				"[2001:db8::10] {\n\ttls internal\n\timport bobres_site\n}\n"},
		// A lab or LAN install: the installer accepts a private address as the domain.
		{"auto on a private IP domain with Zarinpal", State{Mode: ModeAuto, Domain: "192.168.1.10", ZarinpalHost: "pay.example.net"},
			header + "# Mode: auto\ndefault_sni 192.168.1.10\n",
			header + "# Mode: auto\n\n# An IP address: Caddy's own CA signs its certificate (browsers do not trust it).\n" +
				"192.168.1.10 {\n\ttls internal\n\timport bobres_site\n}\n" +
				"\n# The Zarinpal domain: only the pay page and the return URL.\n" +
				"pay.example.net {\n\t@bobres_not_zarinpal {\n\t\tnot path /pay/* /webhooks/zarinpal\n\t}\n" +
				"\thandle @bobres_not_zarinpal {\n\t\trespond \"Not found\" 404\n\t}\n\timport bobres_site\n}\n"},
		{"manual on a private IPv6 domain", State{Mode: ModeManual, Domain: "FD00:0::5", HasCert: true},
			header + "# Mode: manual\ndefault_sni fd00::5\n",
			header + "# Mode: manual\n\n# Your own certificate and key (manual mode).\n" +
				"[fd00::5] {\n\ttls /tls/certs/cert.pem /tls/certs/key.pem\n\timport bobres_site\n}\n"},
	} {
		f, err := Render(tc.st)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if f.Global != tc.global {
			t.Errorf("%s: global\n%s\nwant\n%s", tc.name, f.Global, tc.global)
		}
		if f.Sites != tc.sites {
			t.Errorf("%s: sites\n%s\nwant\n%s", tc.name, f.Sites, tc.sites)
		}
	}
}

// Nothing reaches a file unchecked: every value is a host name, a public
// IP address or refused, with a code the dashboard translates.
func TestRenderRefuses(t *testing.T) {
	host := "panel.example.com"
	for _, tc := range []struct {
		name string
		st   State
		code string
	}{
		{"an unknown mode", State{Mode: "both", Domain: host}, CodeBadMode},
		{"no mode", State{Domain: host}, CodeBadMode},
		{"no domain", State{Mode: ModeAuto}, CodeNoDomain},
		{"a domain with a newline", State{Mode: ModeAuto, Domain: "panel.example.com\n}\nevil.example {"}, CodeBadDomain},
		{"a domain with a space", State{Mode: ModeAuto, Domain: "panel.example.com respond"}, CodeBadDomain},
		{"a domain with braces", State{Mode: ModeAuto, Domain: "{$HOME}"}, CodeBadDomain},
		{"one label", State{Mode: ModeAuto, Domain: "localhost"}, CodeBadDomain},
		{"a hyphen first", State{Mode: ModeAuto, Domain: "-a.example.com"}, CodeBadDomain},
		{"a numeric last label", State{Mode: ModeAuto, Domain: "1.2.3"}, CodeBadDomain},
		{"a wildcard", State{Mode: ModeAuto, Domain: "*.example.com"}, CodeBadDomain},
		{"the unspecified address as the domain", State{Mode: ModeAuto, Domain: "0.0.0.0"}, CodeBadDomain},
		{"the unspecified IPv6 address as the domain", State{Mode: ModeAuto, Domain: "::"}, CodeBadDomain},
		{"the broadcast address as the domain", State{Mode: ModeAuto, Domain: "255.255.255.255"}, CodeBadDomain},
		{"a multicast domain", State{Mode: ModeAuto, Domain: "224.0.0.1"}, CodeBadDomain},
		{"an IPv6 multicast domain", State{Mode: ModeAuto, Domain: "ff02::1"}, CodeBadDomain},
		{"an IP domain with a zone", State{Mode: ModeAuto, Domain: "fe80::1%eth0"}, CodeBadDomain},
		{"a Zarinpal private IP address", State{Mode: ModeAuto, Domain: host, ZarinpalHost: "192.168.1.11"}, CodeBadZarinpalHost},
		{"a bracketed IPv6 domain", State{Mode: ModeAuto, Domain: "[2001:db8::10]"}, CodeBadDomain},
		{"a domain with a port", State{Mode: ModeAuto, Domain: "panel.example.com:8443"}, CodeBadDomain},
		{"a long label", State{Mode: ModeAuto, Domain: strings.Repeat("a", 64) + ".example.com"}, CodeBadDomain},
		{"a long name", State{Mode: ModeAuto, Domain: strings.Repeat("abcdefghi.", 26) + "com"}, CodeBadDomain},
		{"a Zarinpal IP address", State{Mode: ModeAuto, Domain: host, ZarinpalHost: "203.0.113.11"}, CodeBadZarinpalHost},
		{"a Zarinpal host with a path", State{Mode: ModeAuto, Domain: host, ZarinpalHost: "pay.example.net/x"}, CodeBadZarinpalHost},
		{"ip mode without an IP", State{Mode: ModeIP, Domain: host}, CodeIPRequired},
		{"not an IP", State{Mode: ModeIP, Domain: host, IP: "203.0.113.10 {"}, CodeBadIP},
		{"an IP with a zone", State{Mode: ModeIP, Domain: host, IP: "fe80::1%eth0"}, CodeBadIP},
		{"a private IP", State{Mode: ModeIP, Domain: host, IP: "192.168.1.10"}, CodeIPNotPublic},
		{"a CGNAT IP", State{Mode: ModeIP, Domain: host, IP: "100.64.12.1"}, CodeIPNotPublic},
		{"a loopback IP", State{Mode: ModeIP, Domain: host, IP: "127.0.0.1"}, CodeIPNotPublic},
		{"a link-local IP", State{Mode: ModeIP, Domain: host, IP: "169.254.10.1"}, CodeIPNotPublic},
		{"the unspecified address", State{Mode: ModeIP, Domain: host, IP: "::"}, CodeIPNotPublic},
		{"a unique local IPv6", State{Mode: ModeIP, Domain: host, IP: "fd00::1"}, CodeIPNotPublic},
		{"multicast", State{Mode: ModeIP, Domain: host, IP: "224.0.0.1"}, CodeIPNotPublic},
		{"another IP than an IP domain", State{Mode: ModeIP, Domain: "203.0.113.10", IP: "203.0.113.11"}, CodeIPDomainMismatch},
		{"ip mode on a private IP domain", State{Mode: ModeIP, Domain: "192.168.1.10", IP: "203.0.113.10"}, CodeIPDomainMismatch},
		{"ip mode on a private IP domain's own address", State{Mode: ModeIP, Domain: "192.168.1.10", IP: "192.168.1.10"}, CodeIPNotPublic},
		{"manual without a pair", State{Mode: ModeManual, Domain: host}, CodeCertRequired},
	} {
		_, err := Render(tc.st)
		var p *Problem
		if !errors.As(err, &p) || p.Code != tc.code {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
		}
	}
}

// Check returns the canonical form: lower case, compressed IPv6, unmapped
// IPv4, no IP outside ip mode, no Zarinpal host equal to the domain.
func TestCheckCanonical(t *testing.T) {
	for _, tc := range []struct {
		in, want State
	}{
		{State{Mode: ModeAuto, Domain: " Panel.EXAMPLE.com ", ZarinpalHost: "PAY.example.net", IP: "203.0.113.10"},
			State{Mode: ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.example.net"}},
		{State{Mode: ModeIP, Domain: "panel.example.com", IP: "::ffff:203.0.113.10"},
			State{Mode: ModeIP, Domain: "panel.example.com", IP: "203.0.113.10"}},
		{State{Mode: ModeIP, Domain: "2001:DB8::10", IP: "2001:db8:0::10"},
			State{Mode: ModeIP, Domain: "2001:db8::10", IP: "2001:db8::10"}},
		{State{Mode: ModeManual, Domain: "panel.example.com", ZarinpalHost: "panel.example.com", HasCert: true},
			State{Mode: ModeManual, Domain: "panel.example.com", HasCert: true}},
		{State{Mode: ModeAuto, Domain: "bobres.localhost"}, State{Mode: ModeAuto, Domain: "bobres.localhost"}},
		// A private, loopback or link-local address stands as the domain
		// (lab and LAN installs), in canonical form.
		{State{Mode: ModeAuto, Domain: " 192.168.1.10 "}, State{Mode: ModeAuto, Domain: "192.168.1.10"}},
		{State{Mode: ModeAuto, Domain: "10.0.0.5", IP: "203.0.113.10"}, State{Mode: ModeAuto, Domain: "10.0.0.5"}},
		{State{Mode: ModeAuto, Domain: "::ffff:172.16.0.9"}, State{Mode: ModeAuto, Domain: "172.16.0.9"}},
		{State{Mode: ModeAuto, Domain: "127.0.0.1"}, State{Mode: ModeAuto, Domain: "127.0.0.1"}},
		{State{Mode: ModeAuto, Domain: "::1"}, State{Mode: ModeAuto, Domain: "::1"}},
		{State{Mode: ModeAuto, Domain: "169.254.10.1"}, State{Mode: ModeAuto, Domain: "169.254.10.1"}},
		{State{Mode: ModeAuto, Domain: "FD00:0:0::5"}, State{Mode: ModeAuto, Domain: "fd00::5"}},
		{State{Mode: ModeManual, Domain: "100.64.12.1", HasCert: true}, State{Mode: ModeManual, Domain: "100.64.12.1", HasCert: true}},
	} {
		got, err := tc.in.Check()
		if err != nil || got != tc.want {
			t.Errorf("%+v: %+v %v, want %+v", tc.in, got, err, tc.want)
		}
	}
}

// Public addresses only; the documentation ranges pass only in tests.
func TestPublicIP(t *testing.T) {
	for in, want := range map[string]string{"1.2.3.4": "1.2.3.4", " 8.8.8.8\n": "8.8.8.8", "2a01:4f8:0:0::1": "2a01:4f8::1",
		"::ffff:1.2.3.4": "1.2.3.4"} {
		if got, err := PublicIP(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"", "1.2.3", "example.com", "0.1.2.3", "10.1.1.1", "172.16.0.1", "192.168.0.1", "100.64.0.1",
		"100.127.255.254", "127.0.0.1", "169.254.1.1", "192.0.0.1", "198.18.0.1", "240.0.0.1", "255.255.255.255", "224.1.1.1",
		"::1", "::", "fe80::1", "fc00::1", "fec0::1", "ff02::1", "100::1", "2001::1", "64:ff9b:1::1"} {
		if got, err := PublicIP(in); err == nil {
			t.Errorf("%q passed: %q", in, got)
		}
	}
	for _, in := range []string{"192.0.2.1", "198.51.100.1", "203.0.113.1", "2001:db8::1", "3fff::1"} {
		a, err := netip.ParseAddr(in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := publicAddr(a, false); err == nil {
			t.Errorf("the documentation address %s passed outside tests", in)
		}
		if _, err := publicAddr(a, true); err != nil {
			t.Errorf("%s in tests: %v", in, err)
		}
	}
}

// What each address should be served with, for the status and the alerts.
func TestAddresses(t *testing.T) {
	for _, tc := range []struct {
		st   State
		want string
	}{
		{State{Mode: ModeAuto, Domain: "panel.example.com", ZarinpalHost: "pay.example.net"}, "panel.example.com domain public|pay.example.net zarinpal public"},
		{State{Mode: ModeIP, Domain: "panel.example.com", IP: "203.0.113.10"}, "panel.example.com domain public|203.0.113.10 ip public"},
		{State{Mode: ModeIP, Domain: "203.0.113.10", IP: "203.0.113.10"}, "203.0.113.10 domain public"},
		{State{Mode: ModeAuto, Domain: "203.0.113.10"}, "203.0.113.10 domain internal"},
		{State{Mode: ModeAuto, Domain: "192.168.1.10", ZarinpalHost: "pay.example.net"}, "192.168.1.10 domain internal|pay.example.net zarinpal public"},
		{State{Mode: ModeManual, Domain: "10.0.0.5", HasCert: true}, "10.0.0.5 domain manual"},
		{State{Mode: ModeAuto, Domain: "bobres.localhost"}, "bobres.localhost domain internal"},
		{State{Mode: ModeManual, Domain: "panel.example.com", ZarinpalHost: "pay.example.net", HasCert: true}, "panel.example.com domain manual|pay.example.net zarinpal public"},
	} {
		st, err := tc.st.Check()
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, a := range st.Addresses() {
			got = append(got, a.Host+" "+a.Role+" "+a.Expect)
		}
		if strings.Join(got, "|") != tc.want {
			t.Errorf("%+v: %s, want %s", tc.st, strings.Join(got, "|"), tc.want)
		}
	}
}

func TestPublicURL(t *testing.T) {
	ip := State{Mode: ModeIP, Domain: "panel.example.com", IP: "203.0.113.10"}
	v6 := State{Mode: ModeIP, Domain: "panel.example.com", IP: "2001:db8::10"}
	for _, tc := range []struct {
		name   string
		st     State
		env    string
		proven bool
		want   string
	}{
		{"BOBRES_PUBLIC_URL wins", ip, "https://bobres.localhost:8443/", true, "https://bobres.localhost:8443"},
		{"a proven IP", ip, "", true, "https://203.0.113.10"},
		{"a proven IPv6", v6, "", true, "https://[2001:db8::10]"},
		{"an IP not proven", ip, "", false, "https://panel.example.com"},
		{"auto", State{Mode: ModeAuto, Domain: "panel.example.com"}, "", true, "https://panel.example.com"},
		{"an IP domain", State{Mode: ModeAuto, Domain: "2001:db8::10"}, "", false, "https://[2001:db8::10]"},
		{"no domain", State{Mode: ModeAuto}, "", false, ""},
		{"no domain, ip mode not proven", State{Mode: ModeIP, IP: "203.0.113.10"}, "", false, ""},
	} {
		if got := PublicURL(tc.st, tc.env, tc.proven); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestParseMode(t *testing.T) {
	for _, m := range Modes {
		if got, err := ParseMode(string(m)); err != nil || got != m {
			t.Errorf("%s: %v %v", m, got, err)
		}
	}
	if _, err := ParseMode("Auto"); err == nil {
		t.Error("a mode in another case passed")
	}
}
