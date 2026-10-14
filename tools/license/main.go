// Command license is the vendor's license tool: it makes a signing key and
// signs license tokens (docs/05-license.md).
//
//	go run ./tools/license keygen -kid vendor-v2-2028 -out ~/bobres-vendor-keys/vendor-v2-2028.key
//	go run ./tools/license sign -key ~/bobres-vendor-keys/vendor-v1-2026.key -in payload.json
//	go run ./tools/license sign -key KEY -tier standard -domain panel.example.com -customer "Acme VPN" -months 1
//	go run ./tools/license tiers
//	go run ./tools/license entitlements pro
//	go run ./tools/license fingerprint internal/license/keys/vendor-v1-2026.pub
//
// keygen writes the private key to a new file OUTSIDE any git checkout (it
// refuses a path inside one) and prints the public key to save as
// internal/license/keys/<kid>.pub. sign reads the payload JSON (a
// license.Token without kid), fills in kid, v and issued_at, checks it and
// prints the token; with -tier it builds the payload from a tier preset
// (internal/license/tiers.json) instead. sign warns, and still signs, when
// max_active_services is 0 or missing: an install floors it at the free tier's
// (license.Entitlements.AtLeast). The private key never belongs in the
// repository.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

const usage = `usage:
  license keygen -kid ID -out FILE   make a signing key (FILE outside any git checkout)
  license sign -key FILE [-in payload.json] [-now RFC3339]   sign a payload (stdin without -in)
  license sign -key FILE -tier NAME -domain D -customer C    sign a license of a tier preset
               (-months N | -days N | -expires YYYY-MM-DD)   how long it lasts (the trial has a default)
               [-grace-days N] [-updates-until YYYY-MM-DD] [-install-id ID] [-license-id ID] [-now RFC3339]
  license tiers [-json]              list the tiers and their prices (internal/license/tiers.json)
  license entitlements NAME          the tier's entitlements as JSON (for licensesrv key create -entitlements)
  license fingerprint FILE.pub       the fingerprint of a public key file
`

func run(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(errOut, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "keygen":
		err = keygen(args[1:], out, errOut)
	case "sign":
		err = sign(args[1:], in, out, errOut)
	case "tiers":
		err = tiers(args[1:], out, errOut)
	case "entitlements":
		err = entitlements(args[1:], out, errOut)
	case "fingerprint":
		err = fingerprint(args[1:], out, errOut)
	default:
		fmt.Fprint(errOut, usage)
		return 2
	}
	if errors.Is(err, flag.ErrHelp) || errors.Is(err, errUsage) {
		return 2
	}
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	return 0
}

var errUsage = errors.New("usage")

// keyFile is the private key file's content.
type keyFile struct {
	KeyID      string `json:"kid"`
	PrivateKey string `json:"private_key"` // standard base64 of the 64-byte Ed25519 key
}

func keygen(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(errOut)
	kid := fs.String("kid", "", "key id, e.g. v1-2026 (letters, digits, '.', '_', '-')")
	path := fs.String("out", "", "new file for the private key, outside any git checkout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !license.ValidKeyID(*kid) || *path == "" || fs.NArg() > 0 {
		fmt.Fprint(errOut, usage)
		return errUsage
	}
	abs, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	if repo := gitCheckout(filepath.Dir(abs)); repo != "" {
		return fmt.Errorf("%s is inside the git checkout %s: keep the private key outside any repository", abs, repo)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(keyFile{KeyID: *kid, PrivateKey: base64.StdEncoding.EncodeToString(priv)}, "", "  ")
	if err != nil {
		return err
	}
	// O_EXCL: never overwrite a key (tokens signed with it would die).
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(errOut, "Private key written to %s (mode 0600). Keep it offline; never commit it.\n", abs)
	fmt.Fprintf(errOut, "Save the next line as internal/license/keys/%s.pub:\n", *kid)
	fmt.Fprint(out, license.FormatPublicKey(pub))
	return nil
}

// gitCheckout is the git work tree dir lies in ("" when none): a directory
// on the way up holding .git (a directory, or a file in a worktree).
func gitCheckout(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// signFlags are the flags `license sign` reads.
type signFlags struct {
	key, in, now            string
	tier, domain, customer  string
	licenseID, installID    string
	months, days, graceDays int
	expires, updatesUntil   string
	graceGiven              bool
}

func sign(args []string, in io.Reader, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var f signFlags
	fs.StringVar(&f.key, "key", "", "the private key file from keygen")
	fs.StringVar(&f.in, "in", "", "the payload JSON file (default: stdin)")
	fs.StringVar(&f.now, "now", "", "issued_at when the payload has none (RFC3339; default: now)")
	fs.StringVar(&f.tier, "tier", "", "sign a license of this tier preset (standard, pro or trial) instead of a payload")
	fs.StringVar(&f.domain, "domain", "", "with -tier: the store's BOBRES_DOMAIN")
	fs.StringVar(&f.customer, "customer", "", "with -tier: the customer's name")
	fs.StringVar(&f.licenseID, "license-id", "", "with -tier: the license id (default: a new lic_...)")
	fs.StringVar(&f.installID, "install-id", "", "with -tier: bind the token to this install id (optional)")
	fs.IntVar(&f.months, "months", 0, "with -tier: valid for N calendar months from issue")
	fs.IntVar(&f.days, "days", 0, "with -tier: valid for N days from issue")
	fs.StringVar(&f.expires, "expires", "", "with -tier: the end, YYYY-MM-DD (00:00 UTC) or RFC3339")
	fs.IntVar(&f.graceDays, "grace-days", 0, "with -tier: days the install keeps working after expiry (0..90; default: the tier's)")
	fs.StringVar(&f.updatesUntil, "updates-until", "", "with -tier: releases issued up to this day, YYYY-MM-DD (default: no limit)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fs.Visit(func(fl *flag.Flag) { f.graceGiven = f.graceGiven || fl.Name == "grace-days" })
	if f.key == "" || fs.NArg() > 0 {
		fmt.Fprint(errOut, usage)
		return errUsage
	}
	kid, priv, err := readKey(f.key)
	if err != nil {
		return err
	}
	var t license.Token
	if f.tier != "" {
		if f.in != "" {
			return errors.New("-tier and -in are two ways to say what to sign: give one")
		}
		if t, err = tierToken(f, kid); err != nil {
			return err
		}
	} else if t, err = payloadToken(f, kid, in); err != nil {
		return err
	}
	if err := t.Validate(); err != nil {
		return err
	}
	token, err := license.Sign(priv, t)
	if err != nil {
		return err
	}
	// Check the result the way core will (signature and contents).
	pub, _ := priv.Public().(ed25519.PublicKey)
	if _, err := license.Parse(license.KeyRing{kid: pub}, token); err != nil {
		return fmt.Errorf("the signed token does not verify: %w", err)
	}
	fmt.Fprintf(errOut, "Signed %s for %s (%s), expires %s.\n", t.LicenseID, t.Domain, t.Tier, t.ExpiresAt.UTC().Format(time.DateOnly))
	for _, w := range t.Entitlements.FloorWarnings() {
		fmt.Fprintln(errOut, "WARNING:", w)
	}
	fmt.Fprintln(out, token)
	return nil
}

// payloadToken is the token a payload JSON describes: kid, v and issued_at
// are filled in.
func payloadToken(f signFlags, kid string, in io.Reader) (license.Token, error) {
	var t license.Token
	src := in
	if f.in != "" {
		fl, err := os.Open(f.in)
		if err != nil {
			return t, err
		}
		defer fl.Close()
		src = fl
	}
	raw, err := io.ReadAll(io.LimitReader(src, 1<<20))
	if err != nil {
		return t, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return t, fmt.Errorf("payload: %w", err)
	}
	switch {
	case t.KeyID != "" && t.KeyID != kid:
		return t, fmt.Errorf("payload kid %q is not this key's (%q): leave kid out", t.KeyID, kid)
	case t.V != 0 && t.V != license.Version:
		return t, fmt.Errorf("payload v must be %d (or left out)", license.Version)
	case t.Domain == "":
		// Core accepts only tokens bound to the store's domain.
		return t, errors.New("payload domain is required (the store's BOBRES_DOMAIN)")
	}
	t.KeyID, t.V = kid, license.Version
	if t.IssuedAt.IsZero() {
		if t.IssuedAt, err = issuedAt(f.now); err != nil {
			return t, err
		}
	}
	return t, nil
}

// issuedAt is -now, or the present.
func issuedAt(now string) (time.Time, error) {
	if now == "" {
		return time.Now().UTC().Truncate(time.Second), nil
	}
	t, err := time.Parse(time.RFC3339, now)
	if err != nil {
		return t, fmt.Errorf("-now: %w", err)
	}
	return t.UTC(), nil
}

// tierToken is the license of a tier preset for one customer: the preset's
// entitlements, the options the vendor gave, and a validity.
func tierToken(f signFlags, kid string) (license.Token, error) {
	var t license.Token
	presets, err := license.LoadPresets()
	if err != nil {
		return t, err
	}
	p, ok := presets.Get(f.tier)
	if !ok {
		return t, fmt.Errorf("-tier %q: the tiers are %s", f.tier, strings.Join(presets.Names(), ", "))
	}
	if f.domain == "" || strings.TrimSpace(f.customer) == "" {
		return t, errors.New("-tier needs -domain (the store's BOBRES_DOMAIN) and -customer")
	}
	if t.IssuedAt, err = issuedAt(f.now); err != nil {
		return t, err
	}
	if t.ExpiresAt, err = endOf(f, p, t.IssuedAt); err != nil {
		return t, err
	}
	t.GraceDays = p.GraceDays
	if f.graceGiven {
		t.GraceDays = f.graceDays
	}
	t.Entitlements = p.Entitlements
	t.Entitlements.UpdatesUntil = f.updatesUntil
	if f.updatesUntil != "" {
		if _, err := time.Parse(time.DateOnly, f.updatesUntil); err != nil {
			return t, fmt.Errorf("-updates-until %q: want YYYY-MM-DD", f.updatesUntil)
		}
	}
	t.V, t.KeyID, t.Tier = license.Version, kid, f.tier
	t.Customer, t.Domain, t.InstallID = strings.TrimSpace(f.customer), f.domain, f.installID
	if t.LicenseID = f.licenseID; t.LicenseID == "" {
		if t.LicenseID, err = newLicenseID(); err != nil {
			return t, err
		}
	}
	return t, nil
}

// endOf is when a license issued at issued ends: the one of -months, -days
// and -expires that was given, else the tier's fixed length (the trial).
func endOf(f signFlags, p license.Preset, issued time.Time) (time.Time, error) {
	n := 0
	for _, given := range []bool{f.months != 0, f.days != 0, f.expires != ""} {
		if given {
			n++
		}
	}
	switch {
	case n > 1:
		return time.Time{}, errors.New("give one of -months, -days and -expires")
	case f.months < 0 || f.months > 120:
		return time.Time{}, errors.New("-months must be 1..120")
	case f.days < 0 || f.days > 3650:
		return time.Time{}, errors.New("-days must be 1..3650")
	case f.months != 0:
		return license.AddMonths(issued, f.months), nil
	case f.days != 0:
		return issued.Add(time.Duration(f.days) * 24 * time.Hour), nil
	case f.expires != "":
		if d, err := time.Parse(time.DateOnly, f.expires); err == nil {
			return d, nil
		}
		t, err := time.Parse(time.RFC3339, f.expires)
		if err != nil {
			return t, fmt.Errorf("-expires %q: want YYYY-MM-DD or RFC3339", f.expires)
		}
		return t.UTC(), nil
	case p.Days > 0:
		return issued.Add(time.Duration(p.Days) * 24 * time.Hour), nil
	}
	return time.Time{}, errors.New("say how long it lasts: -months N, -days N or -expires YYYY-MM-DD")
}

// newLicenseID makes a license id as licensesrv does: "lic_" and 16 base32
// letters (80 random bits).
func newLicenseID() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "lic_" + strings.ToLower(base32.StdEncoding.EncodeToString(b)), nil
}

// tiers lists the presets and their prices.
func tiers(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("tiers", flag.ContinueOnError)
	fs.SetOutput(errOut)
	asJSON := fs.Bool("json", false, "print the whole presets file as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fmt.Fprint(errOut, usage)
		return errUsage
	}
	p, err := license.LoadPresets()
	if err != nil {
		return err
	}
	if *asJSON {
		raw, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(raw))
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIER\tSERVERS\tACTIVE SERVICES\tSTAFF SEATS\tRESELLERS\tAUTO USDT\tWHITE LABEL\tLENGTH\tGRACE\tPRICE")
	unset := false
	for _, name := range p.Names() {
		t, _ := p.Get(name)
		e := t.Entitlements
		length, price := "per license", "not sold"
		if t.Days > 0 {
			length = fmt.Sprintf("%d days", t.Days)
		}
		if t.Price != nil {
			price = fmt.Sprintf("%s/month, %s/year (%s)", t.Price.Monthly, t.Price.Yearly, t.Price.Currency)
			unset = unset || !t.Price.IsSet()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d days\t%s\n", name, limitText(e.MaxServers), limitText(e.MaxActiveServices),
			limitText(e.DashboardStaffSeat), yesNo(e.Resellers), yesNo(e.CryptoWatcher && e.HasGateway("usdt_trc20_auto")),
			yesNo(e.WhiteLabel), length, t.GraceDays, price)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if unset {
		fmt.Fprintf(errOut, "Some prices are still %q: fill them in internal/license/tiers.json.\n", license.PricePlaceholder)
	}
	return nil
}

func limitText(n int) string {
	if n == license.Unlimited {
		return "unlimited"
	}
	return fmt.Sprint(n)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// entitlements prints a tier's entitlements as the JSON `licensesrv key
// create -entitlements` reads.
func entitlements(args []string, out, errOut io.Writer) error {
	if len(args) != 1 {
		fmt.Fprint(errOut, usage)
		return errUsage
	}
	p, err := license.LoadPresets()
	if err != nil {
		return err
	}
	t, ok := p.Get(args[0])
	if !ok {
		return fmt.Errorf("no tier %q: the tiers are %s", args[0], strings.Join(p.Names(), ", "))
	}
	raw, err := json.MarshalIndent(t.Entitlements, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(raw))
	return err
}

// fingerprint prints the fingerprint of a public key file.
func fingerprint(args []string, out, errOut io.Writer) error {
	if len(args) != 1 {
		fmt.Fprint(errOut, usage)
		return errUsage
	}
	raw, err := os.ReadFile(args[0]) //nolint:gosec // the vendor names the key file
	if err != nil {
		return err
	}
	pub, err := license.ParsePublicKey(string(raw))
	if err != nil {
		return fmt.Errorf("%s: %w", args[0], err)
	}
	_, err = fmt.Fprintln(out, license.Fingerprint(pub))
	return err
}

func readKey(path string) (string, ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var k keyFile
	if err := json.Unmarshal(raw, &k); err != nil {
		return "", nil, fmt.Errorf("key file: %w", err)
	}
	priv, err := base64.StdEncoding.DecodeString(k.PrivateKey)
	if err != nil || len(priv) != ed25519.PrivateKeySize || !license.ValidKeyID(k.KeyID) {
		return "", nil, errors.New("key file: not a key made by license keygen")
	}
	return k.KeyID, ed25519.PrivateKey(priv), nil
}
