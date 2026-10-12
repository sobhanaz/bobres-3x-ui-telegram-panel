package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

const licenseUsage = "usage: license status | license info [--json] | license import [--activated]  (the token on stdin)"

// licenseCmd: "license status" shows the installed license as this install
// sees it; "license info" prints it as JSON (the install id and domain
// `bobres license activate` sends the license server, and updates_until for
// `bobres update`); "license import" installs the token read from stdin
// (audited as license.set from the CLI), with --activated one the license
// server answered to an activation, which core then renews once a day.
// `bobres license ...` runs them inside the core container.
func licenseCmd(args []string, in io.Reader, out, errOut io.Writer) int {
	sub, ok := licenseArgs(args)
	if !ok {
		fmt.Fprintln(errOut, licenseUsage)
		return 2
	}
	var token string
	if sub == "import" || sub == "import --activated" {
		var err error
		if token, err = readToken(in); err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
	}
	cfg, err := config.LoadCore()
	if err != nil {
		fmt.Fprintln(errOut, "load core config:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(errOut, "connect:", err)
		return 1
	}
	defer st.Close()
	dom := domain.New(st, domain.Config{OwnerTelegramID: cfg.AdminTelegramID, License: domain.LicenseConfig{Domain: cfg.Domain}})
	var lic *domain.LicenseStatus
	cli := store.WithSource(ctx, store.SourceCLI)
	switch {
	case token == "":
		lic, err = dom.License(ctx)
	case sub == "import --activated":
		lic, err = dom.SetActivatedLicense(cli, nil, token, "activated with a license key from the command line")
	default:
		lic, err = dom.SetLicense(cli, nil, token, "imported with the command line")
	}
	var le *domain.LicenseError
	switch {
	case errors.As(err, &le):
		fmt.Fprintf(errOut, "not installed: %s (%s)\n", domain.LicenseReasonText(le.Reason), le.Reason)
		return 1
	case err != nil:
		fmt.Fprintln(errOut, "license:", err)
		return 1
	}
	if sub == "info" {
		return writeInfo(out, errOut, lic)
	}
	if token != "" {
		fmt.Fprintln(out, "License installed.")
	}
	printLicense(out, lic, time.Now())
	return 0
}

// licenseArgs names the subcommand: status, info, import or
// "import --activated" (false: wrong use).
func licenseArgs(args []string) (string, bool) {
	switch strings.Join(args, " ") {
	case "status":
		return "status", true
	case "info", "info --json":
		return "info", true
	case "import":
		return "import", true
	case "import --activated":
		return "import --activated", true
	}
	return "", false
}

// licenseInfo is `license info`: the license as this install sees it, as
// JSON (times in unix seconds; never the token).
type licenseInfo struct {
	InstallID string `json:"install_id"`
	// Domain is this install's BOBRES_DOMAIN as a license is bound to it.
	Domain string `json:"domain"`
	// Version is core's.
	Version string `json:"version"`
	// State is missing, invalid, active, grace or expired; Reason says why a
	// token is invalid; Valid: it verified here (expired included).
	State  string `json:"state"`
	Reason string `json:"reason"`
	Valid  bool   `json:"valid"`
	// Source is manual, activation or refresh (null when none is installed).
	Source      *string           `json:"source"`
	UpdatedAt   *int64            `json:"updated_at"`
	RefreshedAt *int64            `json:"refreshed_at"`
	License     *licenseInfoToken `json:"license"`
}

// licenseInfoToken is the installed token's payload (when it verified).
type licenseInfoToken struct {
	LicenseID    string               `json:"license_id"`
	Customer     string               `json:"customer"`
	Tier         string               `json:"tier"`
	KeyID        string               `json:"kid"`
	IssuedAt     int64                `json:"issued_at"`
	ExpiresAt    int64                `json:"expires_at"`
	GraceDays    int                  `json:"grace_days"`
	GraceEndsAt  int64                `json:"grace_ends_at"`
	Domain       string               `json:"domain"`
	InstallID    string               `json:"install_id"`
	Entitlements license.Entitlements `json:"entitlements"`
	// LicenseEndsAt is when the license itself ends (a token the license
	// server renews says so); null when the token does not.
	LicenseEndsAt *int64 `json:"license_ends_at"`
}

// writeInfo prints st as licenseInfo JSON.
func writeInfo(out, errOut io.Writer, st *domain.LicenseStatus) int {
	unix := func(t *time.Time) *int64 {
		if t == nil {
			return nil
		}
		v := t.Unix()
		return &v
	}
	info := licenseInfo{InstallID: st.InstallID, Domain: st.Domain, Version: version.Version, State: st.State,
		Reason: st.Reason, Valid: st.Valid(), UpdatedAt: unix(st.UpdatedAt), RefreshedAt: unix(st.RefreshedAt)}
	if st.Source != "" {
		info.Source = &st.Source
	}
	if t := st.Token; t != nil {
		info.License = &licenseInfoToken{LicenseID: t.LicenseID, Customer: t.Customer, Tier: t.Tier, KeyID: t.KeyID,
			IssuedAt: t.IssuedAt.Unix(), ExpiresAt: t.ExpiresAt.Unix(), GraceDays: t.GraceDays, GraceEndsAt: st.GraceEndsAt.Unix(),
			Domain: t.Domain, InstallID: t.InstallID, Entitlements: t.Entitlements, LicenseEndsAt: unix(t.LicenseEndsAt)}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(info); err != nil {
		fmt.Fprintln(errOut, "license:", err)
		return 1
	}
	return 0
}

// maxTokenInput bounds what import reads.
const maxTokenInput = 64 << 10

// readToken reads a pasted token: surrounding space and line breaks are
// dropped (a token is one line).
func readToken(in io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(in, maxTokenInput+1))
	if err != nil {
		return "", fmt.Errorf("read the token: %w", err)
	}
	if len(raw) > maxTokenInput {
		return "", errors.New("this is too long for a license token")
	}
	token := strings.Join(strings.Fields(string(raw)), "")
	if token == "" {
		return "", errors.New("no token on stdin: echo TOKEN | license import")
	}
	return token, nil
}

// printLicense writes the license as `license status` shows it.
func printLicense(w io.Writer, st *domain.LicenseStatus, now time.Time) {
	state := st.State
	switch st.State {
	case domain.LicenseMissing:
		state = "missing (unlicensed: everything works)"
	case domain.LicenseInvalid:
		state = fmt.Sprintf("invalid: %s (%s); unlicensed: everything works", domain.LicenseReasonText(st.Reason), st.Reason)
	case domain.LicenseGrace:
		state = "grace (expired; everything still works: renew it)"
	case domain.LicenseExpired:
		state = "expired (adding resellers, servers, staff or payment methods is refused: renew it)"
	}
	row := func(k, v string) { fmt.Fprintf(w, "%-15s %s\n", k+":", v) }
	row("State", state)
	row("Install id", st.InstallID)
	row("Domain", orNone(st.Domain))
	row("Public keys", fmt.Sprint(st.Keys))
	switch st.Source {
	case store.LicenseManual:
		row("Source", "pasted or imported (not renewed online)")
	case store.LicenseActivation:
		row("Source", "activated with a license key (renewed online daily)")
	case store.LicenseRefresh:
		at := "-"
		if st.RefreshedAt != nil {
			at = st.RefreshedAt.UTC().Format(time.DateOnly)
		}
		row("Source", "renewed online on "+at+" (daily)")
	}
	t := st.Token
	if t == nil {
		return
	}
	row("License", t.LicenseID+" ("+t.Tier+")")
	row("Customer", orNone(t.Customer))
	row("Key", t.KeyID)
	row("Issued", t.IssuedAt.UTC().Format(time.DateOnly))
	row("Expires", fmt.Sprintf("%s (%s)", t.ExpiresAt.UTC().Format(time.DateOnly), daysLeft(now, t.ExpiresAt)))
	row("Grace until", st.GraceEndsAt.UTC().Format(time.DateOnly))
	if t.LicenseEndsAt != nil {
		row("License ends", fmt.Sprintf("%s (%s)", t.LicenseEndsAt.UTC().Format(time.DateOnly), daysLeft(now, *t.LicenseEndsAt)))
	}
	row("Bound to", orNone(strings.TrimSpace(t.Domain+" "+t.InstallID)))
	e := t.Entitlements
	row("Servers", limit(e.MaxServers))
	row("Services", limit(e.MaxActiveServices))
	row("Staff seats", limit(e.DashboardStaffSeat))
	row("Gateways", orNone(strings.Join(e.Gateways, ", ")))
	row("Features", orNone(strings.Join(features(e), ", ")))
	if e.UpdatesUntil != "" {
		row("Updates until", e.UpdatesUntil)
	}
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func limit(n int) string {
	switch {
	case n == license.Unlimited:
		return "unlimited"
	case n <= 0:
		return "none"
	}
	return fmt.Sprint(n)
}

func features(e license.Entitlements) []string {
	var out []string
	for _, f := range []string{"white_label", "resellers", "crypto_watcher"} {
		if e.HasFeature(f) {
			out = append(out, f)
		}
	}
	return out
}

func daysLeft(now, t time.Time) string {
	d := int(math.Ceil(t.Sub(now).Hours() / 24))
	if d <= 0 {
		return "passed"
	}
	if d == 1 {
		return "1 day left"
	}
	return fmt.Sprintf("%d days left", d)
}

// selfURL is core's own HTTP address for its health check: the listen
// address with an empty or any-address host asked on loopback.
func selfURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
