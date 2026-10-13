package tlsconf

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// Limits on what an operator hands over.
const (
	// MaxPEM bounds the certificate chain and the key, each.
	MaxPEM = 64 << 10
	// maxChain bounds the certificates in a chain.
	maxChain = 10
)

// ExpiryWarning: a certificate that ends sooner than this is accepted with
// a warning.
const ExpiryWarning = 14 * 24 * time.Hour

// Problem codes of a certificate and key.
const (
	CodeCertMissing      = "cert_missing"
	CodeCertNotPEM       = "cert_not_pem"
	CodeCertHasKey       = "cert_has_key"
	CodeCertParse        = "cert_parse"
	CodeCertTooLarge     = "cert_too_large"
	CodeKeyMissing       = "key_missing"
	CodeKeyNotPEM        = "key_not_pem"
	CodeKeyEncrypted     = "key_encrypted"
	CodeKeyParse         = "key_parse"
	CodeKeyTooLarge      = "key_too_large"
	CodeKeyCount         = "key_count"
	CodeKeyMismatch      = "key_mismatch"
	CodeChainOrder       = "chain_order"
	CodeChainTooLong     = "chain_too_long"
	CodeCertExpired      = "cert_expired"
	CodeCertNotYetValid  = "cert_not_yet_valid"
	CodeCertNameMismatch = "cert_name_mismatch"
)

// Warning codes: a pair is accepted with these.
const (
	WarnExpiresSoon     = "expires_soon"
	WarnSelfSigned      = "self_signed"
	WarnChainIncomplete = "chain_incomplete"
	WarnUntrusted       = "untrusted"
	// WarnCoversZarinpal: the pair also covers the Zarinpal host, so Caddy
	// serves it there too instead of an automatic certificate.
	WarnCoversZarinpal = "covers_zarinpal"
)

// CertInfo describes a certificate: safe to show, store and audit (it holds
// no key).
type CertInfo struct {
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer"`
	DNSNames  []string  `json:"dns_names"`
	IPs       []string  `json:"ips"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
}

// Info describes cert.
func Info(cert *x509.Certificate) CertInfo {
	ips := make([]string, 0, len(cert.IPAddresses))
	for _, ip := range cert.IPAddresses {
		ips = append(ips, ip.String())
	}
	dns := cert.DNSNames
	if dns == nil {
		dns = []string{}
	}
	return CertInfo{Subject: name(cert.Subject.String(), cert.Subject.CommonName), Issuer: name(cert.Issuer.String(), cert.Issuer.CommonName),
		DNSNames: dns, IPs: ips, NotBefore: cert.NotBefore.UTC(), NotAfter: cert.NotAfter.UTC()}
}

// name is a distinguished name, or its common name when it is very long.
func name(dn, cn string) string {
	if len(dn) > 200 && cn != "" {
		return cn
	}
	return dn
}

// Pair is a checked certificate chain and private key, normalized for
// Caddy: the chain's blocks alone with the leaf first, the key's block
// alone. Never log or show KeyPEM.
type Pair struct {
	CertPEM []byte
	KeyPEM  []byte
	Info    CertInfo
	// SelfSigned: the leaf signs itself.
	SelfSigned bool
	// Trusted: the chain verifies against the public roots for the name;
	// nil when no roots could be loaded.
	Trusted *bool
	// CoversZarinpal: the leaf also covers PairOptions.ZarinpalHost.
	CoversZarinpal bool
	Warnings       []Problem
}

// PairOptions are what a pair is checked against.
type PairOptions struct {
	// Name is the address the leaf must cover: a host name (wildcards of
	// one label count) or an IP address (an IP SAN).
	Name string
	// ZarinpalHost is reported when the leaf covers it too ("" = none).
	ZarinpalHost string
	// Now is the time validity is checked at.
	Now time.Time
	// Roots are the public roots (nil: the system's).
	Roots *x509.CertPool
}

// CheckPair checks a certificate chain and its private key as Caddy will
// load them (tls.X509KeyPair, PEM only) before anything is stored or
// written: certificate blocks only in the chain, the leaf first, then the
// intermediates; one unencrypted key (PKCS#1, PKCS#8 or SEC1) that matches
// the leaf; the leaf valid now and covering opt.Name. A leaf that ends
// within 14 days, a self-signed one, a chain that does not lead to a public
// root, and one that covers the Zarinpal host too are accepted with
// warnings. Errors are Problems; none quotes the input.
func CheckPair(certPEM, keyPEM []byte, opt PairOptions) (*Pair, error) {
	if opt.Name == "" {
		return nil, problem(CodeNoDomain, "this install has no domain (BOBRES_DOMAIN) to check the certificate against")
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	certs, certBlocks, err := readChain(certPEM)
	if err != nil {
		return nil, err
	}
	keyBlock, key, err := readKey(keyPEM)
	if err != nil {
		return nil, err
	}
	leafAt := -1
	for i, c := range certs {
		if sameKey(c.PublicKey, key) {
			leafAt = i
			break
		}
	}
	switch {
	case leafAt < 0:
		return nil, problem(CodeKeyMismatch, "the private key does not belong to any certificate in the chain")
	case leafAt > 0:
		return nil, problem(CodeChainOrder, "the certificate for this key must come first in the chain, then the intermediate certificates")
	}
	leaf, now := certs[0], opt.Now
	switch {
	case now.Before(leaf.NotBefore):
		return nil, problem(CodeCertNotYetValid, "the certificate is not valid yet (from "+leaf.NotBefore.UTC().Format(time.DateTime)+" UTC)")
	case now.After(leaf.NotAfter):
		return nil, problem(CodeCertExpired, "the certificate expired on "+leaf.NotAfter.UTC().Format(time.DateOnly))
	}
	if err := leaf.VerifyHostname(opt.Name); err != nil {
		return nil, problem(CodeCertNameMismatch, "the certificate does not cover "+opt.Name+" (its names: "+namesOf(leaf)+")")
	}
	p := &Pair{CertPEM: bytes.Join(certBlocks, nil), KeyPEM: pem.EncodeToMemory(&pem.Block{Type: keyBlock.Type, Bytes: keyBlock.Bytes}),
		Info: Info(leaf)}
	// The same check Caddy makes when it loads the files.
	if _, err := tls.X509KeyPair(p.CertPEM, p.KeyPEM); err != nil {
		return nil, problem(CodeKeyMismatch, "the certificate and the private key cannot be used together")
	}
	// Its own key verifies its signature (CheckSignatureFrom would also want
	// a CA, which a self-signed server certificate often is not).
	p.SelfSigned = bytes.Equal(leaf.RawIssuer, leaf.RawSubject) &&
		leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil
	p.Trusted = trusted(leaf, certs[1:], opt)
	if opt.ZarinpalHost != "" && leaf.VerifyHostname(opt.ZarinpalHost) == nil {
		p.CoversZarinpal = true
	}
	p.Warnings = pairWarnings(p, len(certs)-1, now)
	return p, nil
}

// pairWarnings says what is accepted but worth knowing about a pair.
func pairWarnings(p *Pair, intermediates int, now time.Time) []Problem {
	var out []Problem
	if left := p.Info.NotAfter.Sub(now); left < ExpiryWarning {
		out = append(out, Problem{Code: WarnExpiresSoon, Message: fmt.Sprintf("the certificate expires in %s, on %s: Caddy does not renew your own certificate",
			daysText(left), p.Info.NotAfter.Format(time.DateOnly))})
	}
	const needTrust = "Telegram webhooks, payment returns and some apps need a publicly trusted chain"
	switch {
	case p.SelfSigned:
		out = append(out, Problem{Code: WarnSelfSigned, Message: "the certificate is self-signed: browsers warn about it, and " + needTrust})
	case p.Trusted != nil && *p.Trusted:
	case intermediates == 0:
		out = append(out, Problem{Code: WarnChainIncomplete, Message: "the chain has no intermediate certificates (add them after the certificate): " + needTrust})
	case p.Trusted != nil:
		out = append(out, Problem{Code: WarnUntrusted, Message: "the chain does not lead to a publicly trusted root (an intermediate is missing, or a private CA issued it): " + needTrust})
	}
	if p.CoversZarinpal {
		out = append(out, Problem{Code: WarnCoversZarinpal, Message: "the certificate also covers the Zarinpal domain: Caddy serves it there too, with no automatic certificate, until it expires"})
	}
	return out
}

// trusted verifies leaf for opt.Name against the public roots, with the
// chain's intermediates (nil when the system has no roots to ask).
func trusted(leaf *x509.Certificate, intermediates []*x509.Certificate, opt PairOptions) *bool {
	roots := opt.Roots
	if roots == nil {
		var err error
		if roots, err = x509.SystemCertPool(); err != nil {
			return nil
		}
	}
	pool := x509.NewCertPool()
	for _, c := range intermediates {
		pool.AddCert(c)
	}
	_, err := leaf.Verify(x509.VerifyOptions{DNSName: opt.Name, Roots: roots, Intermediates: pool, CurrentTime: opt.Now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	ok := err == nil
	return &ok
}

// readChain reads the certificate blocks of a chain, in order, each also
// re-encoded alone.
func readChain(data []byte) ([]*x509.Certificate, [][]byte, error) {
	if len(data) > MaxPEM {
		return nil, nil, problem(CodeCertTooLarge, fmt.Sprintf("the certificate chain is larger than %d KiB", MaxPEM>>10))
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, problem(CodeCertMissing, "paste or choose the certificate (PEM)")
	}
	var (
		certs  []*x509.Certificate
		blocks [][]byte
	)
	for rest := data; ; {
		var b *pem.Block
		if b, rest = pem.Decode(rest); b == nil {
			break
		}
		switch {
		case b.Type == "CERTIFICATE":
		case strings.HasSuffix(b.Type, "PRIVATE KEY"):
			return nil, nil, problem(CodeCertHasKey, "the certificate field holds a private key: put the key in its own field")
		default:
			return nil, nil, problem(CodeCertNotPEM, "the certificate field holds a "+printable(b.Type)+" block: only CERTIFICATE blocks belong there")
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return nil, nil, problem(CodeCertParse, fmt.Sprintf("certificate %d of the chain cannot be read", len(certs)+1))
		}
		certs = append(certs, c)
		blocks = append(blocks, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b.Bytes}))
		if len(certs) > maxChain {
			return nil, nil, problem(CodeChainTooLong, fmt.Sprintf("the chain has more than %d certificates", maxChain))
		}
	}
	if len(certs) == 0 {
		return nil, nil, problem(CodeCertNotPEM, "no PEM certificate found (it starts with -----BEGIN CERTIFICATE-----)")
	}
	return certs, blocks, nil
}

// readKey reads the one private key block (EC PARAMETERS and certificate
// blocks beside it are ignored).
func readKey(data []byte) (*pem.Block, crypto.Signer, error) {
	if len(data) > MaxPEM {
		return nil, nil, problem(CodeKeyTooLarge, fmt.Sprintf("the private key is larger than %d KiB", MaxPEM>>10))
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, problem(CodeKeyMissing, "paste or choose the private key (PEM)")
	}
	var found *pem.Block
	for rest := data; ; {
		var b *pem.Block
		if b, rest = pem.Decode(rest); b == nil {
			break
		}
		switch {
		case b.Type == "EC PARAMETERS" || b.Type == "CERTIFICATE":
			continue
		case b.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(b.Headers["Proc-Type"], "ENCRYPTED"):
			return nil, nil, problem(CodeKeyEncrypted, "the private key is encrypted: decrypt it first (openssl pkey -in key.pem -out plain.pem)")
		case !isKeyLabel(b.Type):
			return nil, nil, problem(CodeKeyNotPEM, "the key field holds a "+printable(b.Type)+" block, not a private key")
		case found != nil:
			return nil, nil, problem(CodeKeyCount, "the key field holds more than one private key")
		}
		found = b
	}
	if found == nil {
		return nil, nil, problem(CodeKeyNotPEM, "no PEM private key found (it starts with -----BEGIN ... PRIVATE KEY-----)")
	}
	key, err := parseKey(found)
	if err != nil {
		return nil, nil, err
	}
	return found, key, nil
}

// parseKey reads a private key the way Go's tls.X509KeyPair, which is what
// Caddy loads the pair with, does: PKCS#1, then PKCS#8, then SEC1, whatever
// the PEM label says (a PKCS#8 key under "RSA PRIVATE KEY" loads in Caddy,
// so it must pass here too, and the other way round).
func parseKey(b *pem.Block) (crypto.Signer, error) {
	bad := problem(CodeKeyParse, "the private key cannot be read (RSA, ECDSA or Ed25519; PKCS#1, PKCS#8 or SEC1)")
	var k any
	if rk, err := x509.ParsePKCS1PrivateKey(b.Bytes); err == nil {
		k = rk
	} else if pk, err := x509.ParsePKCS8PrivateKey(b.Bytes); err == nil {
		k = pk
	} else if ek, err := x509.ParseECPrivateKey(b.Bytes); err == nil {
		k = ek
	} else {
		return nil, bad
	}
	switch k := k.(type) {
	case *rsa.PrivateKey:
		return k, nil
	case *ecdsa.PrivateKey:
		return k, nil
	case ed25519.PrivateKey:
		return k, nil
	}
	return nil, bad
}

// isKeyLabel: the PEM labels Go's tls.X509KeyPair takes for a private key
// ("PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY", ...).
func isKeyLabel(label string) bool {
	return label == "PRIVATE KEY" || strings.HasSuffix(label, " PRIVATE KEY")
}

// sameKey reports whether pub is key's public half.
func sameKey(pub crypto.PublicKey, key crypto.Signer) bool {
	type equaler interface{ Equal(crypto.PublicKey) bool }
	e, ok := key.Public().(equaler)
	return ok && e.Equal(pub)
}

// namesOf lists a certificate's names for a message.
func namesOf(c *x509.Certificate) string {
	names := append([]string{}, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	if len(names) == 0 {
		return "none"
	}
	if len(names) > 5 {
		names = append(names[:5], "…")
	}
	return strings.Join(names, ", ")
}

// printable keeps a PEM block type safe to quote.
func printable(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r > '~' {
			return -1
		}
		return r
	}, s)
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func daysText(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days < 1 {
		return "less than a day"
	}
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}

// SplitBundle separates a PEM bundle (the chain's certificate blocks and
// one private key block, in any order) into a chain and a key for
// CheckPair. Text around the blocks is ignored.
func SplitBundle(data []byte) (certPEM, keyPEM []byte, err error) {
	if len(data) > 2*MaxPEM {
		return nil, nil, problem(CodeCertTooLarge, fmt.Sprintf("the bundle is larger than %d KiB", 2*MaxPEM>>10))
	}
	var certs, keys [][]byte
	for rest := data; ; {
		var b *pem.Block
		if b, rest = pem.Decode(rest); b == nil {
			break
		}
		switch {
		case b.Type == "CERTIFICATE":
			certs = append(certs, pem.EncodeToMemory(b))
		case isKeyLabel(b.Type):
			keys = append(keys, pem.EncodeToMemory(b))
		case b.Type == "EC PARAMETERS":
		default:
			return nil, nil, problem(CodeCertNotPEM, "the bundle holds a "+printable(b.Type)+" block: only certificates and one private key belong there")
		}
	}
	switch {
	case len(certs) == 0:
		return nil, nil, problem(CodeCertMissing, "the bundle holds no certificate")
	case len(keys) == 0:
		return nil, nil, problem(CodeKeyMissing, "the bundle holds no private key")
	case len(keys) > 1:
		return nil, nil, problem(CodeKeyCount, "the bundle holds more than one private key")
	}
	return bytes.Join(certs, nil), keys[0], nil
}

// ExpiringSoon reports whether a certificate valid from notBefore to
// notAfter has less than min(14 days, a quarter of its lifetime) left at
// now: Caddy renews well before that (a third of the lifetime, or what the
// CA's renewal info says), so it means renewal is failing. A 160-hour IP
// certificate warns with 40 hours left, a 90-day one with 14 days.
func ExpiringSoon(notBefore, notAfter, now time.Time) bool {
	limit := min(ExpiryWarning, notAfter.Sub(notBefore)/4)
	return notAfter.Sub(now) < limit
}
