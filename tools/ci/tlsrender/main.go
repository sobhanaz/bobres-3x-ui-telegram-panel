// Command tlsrender writes a Caddy configuration the way an install has it,
// for one HTTPS mode, so CI can run `caddy validate` on every mode with the
// Caddy image compose pins:
//
//	tlsrender -list
//	tlsrender <dir> <mode>
//
// <dir> gets caddy/Caddyfile (the embedded main file), tls/global.caddy and
// tls/sites.caddy (what internal/tlsconf renders for the mode), for the manual
// modes tls/certs/cert.pem and key.pem (a self-signed pair made now: no key
// is ever committed), and validate.env, the environment compose gives Caddy.
// The paths in the files are the container's: mount <dir>/caddy at
// /etc/caddy and <dir>/tls at /tls, as the compose file does.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/deploy"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

// Any public address will do: nothing connects to it.
const (
	hostDomain = "panel.example.com"
	hostZP     = "pay.example.net"
	publicV4   = "8.8.8.8"
	publicV6   = "2606:4700:4700::1111"
)

// modes are the states CI validates: every mode, with a host name and with an
// IP address as the domain, with and without the Zarinpal domain, with an
// IPv4 and an IPv6 address.
var modes = map[string]tlsconf.State{
	"auto":                  {Mode: tlsconf.ModeAuto, Domain: hostDomain},
	"auto-zarinpal":         {Mode: tlsconf.ModeAuto, Domain: hostDomain, ZarinpalHost: hostZP},
	"auto-ip-domain":        {Mode: tlsconf.ModeAuto, Domain: publicV4},
	"auto-ipv6-domain":      {Mode: tlsconf.ModeAuto, Domain: publicV6},
	"ip":                    {Mode: tlsconf.ModeIP, Domain: hostDomain, IP: publicV4},
	"ip-zarinpal":           {Mode: tlsconf.ModeIP, Domain: hostDomain, IP: publicV4, ZarinpalHost: hostZP},
	"ip-v6":                 {Mode: tlsconf.ModeIP, Domain: hostDomain, IP: publicV6},
	"ip-is-the-domain":      {Mode: tlsconf.ModeIP, Domain: publicV4, IP: publicV4, ZarinpalHost: hostZP},
	"manual":                {Mode: tlsconf.ModeManual, Domain: hostDomain, HasCert: true},
	"manual-zarinpal":       {Mode: tlsconf.ModeManual, Domain: hostDomain, ZarinpalHost: hostZP, HasCert: true},
	"manual-ip-domain":      {Mode: tlsconf.ModeManual, Domain: publicV4, HasCert: true},
	"manual-ipv6-domain-zp": {Mode: tlsconf.ModeManual, Domain: publicV6, ZarinpalHost: hostZP, HasCert: true},
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "-list" {
		var names []string
		for n := range modes {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Println(n)
		}
		return
	}
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: tlsrender -list | tlsrender <dir> <mode>")
		os.Exit(2)
	}
	if err := render(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "tlsrender:", err)
		os.Exit(1)
	}
}

func render(dir, mode string) error {
	st, ok := modes[mode]
	if !ok {
		return fmt.Errorf("unknown mode %q (see -list)", mode)
	}
	f, err := tlsconf.Render(st)
	if err != nil {
		return fmt.Errorf("render %s: %w", mode, err)
	}
	files := map[string][]byte{
		filepath.Join("caddy", "Caddyfile"):      deploy.Caddyfile,
		filepath.Join("tls", tlsconf.GlobalFile): []byte(f.Global),
		filepath.Join("tls", tlsconf.SitesFile):  []byte(f.Sites),
		"validate.env":                           []byte("BOBRES_DOMAIN=" + st.Domain + "\nBOBRES_ZARINPAL_HOST=" + st.ZarinpalHost + "\n"),
	}
	if f.Manual {
		certPEM, keyPEM, err := selfSigned(st.Domain)
		if err != nil {
			return err
		}
		files[filepath.Join("tls", tlsconf.CertsDir, tlsconf.CertFile)] = certPEM
		files[filepath.Join("tls", tlsconf.CertsDir, tlsconf.KeyFile)] = keyPEM
	}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // a throwaway directory
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // read by a container's root
			return err
		}
	}
	return nil
}

// selfSigned is a certificate and key for host (a name or an IP address),
// valid for a day.
func selfSigned(host string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), nil
}
