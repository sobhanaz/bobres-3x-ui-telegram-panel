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
	dom := domain.New(st, domain.Config{OwnerTelegramIDs: cfg.AdminTelegramIDs, License: domain.LicenseConfig{Domain: cfg.Domain, SellerContact: cfg.SellerContact}})
	cli := store.WithSource(ctx, store.SourceCLI)
	switch {
	case token == "":
	case sub == "import --activated":
		_, err = dom.SetActivatedLicense(cli, nil, token, "activated with a license key from the command line")
	default:
		_, err = dom.SetLicense(cli, nil, token, "imported with the command line")
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
	// What is in effect (a license, the trial or the free tier) and what it
	// is used for; the servers are the provisioner's, which the CLI cannot ask.
	eff, err := dom.Effective(ctx)
	if err != nil {
		fmt.Fprintln(errOut, "license:", err)
		return 1
	}
	usage, err := dom.LicenseUsage(ctx, nil, nil)
	if err != nil {
		fmt.Fprintln(errOut, "license:", err)
		return 1
	}
	if sub == "info" {
		return writeInfo(out, errOut, eff, usage)
	}
	if token != "" {
		fmt.Fprintln(out, "License installed.")
	}
	printLicense(out, eff, usage, time.Now())
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
	// Effective is what applies now: a license, its grace period, the
	// implicit trial or the free tier.
	Effective licenseInfoEffective `json:"effective"`
}

// licenseInfoEffective is what is in effect on this install.
type licenseInfoEffective struct {
	// Basis is license, grace, trial or free; Tier names it (a license's
	// tier, trial or free).
	Basis string `json:"basis"`
	Tier  string `json:"tier"`
	// TrialEndsAt is when the implicit trial ends or ended; TrialDaysLeft
	// counts the days it still has (0 when it is not running).
	TrialEndsAt   *int64 `json:"trial_ends_at"`
	TrialDaysLeft int    `json:"trial_days_left"`
	// TrialDays is how long the implicit trial lasts.
	TrialDays    int                  `json:"trial_days"`
	Entitlements license.Entitlements `json:"entitlements"`
	Usage        licenseInfoUsage     `json:"usage"`
	// Contact is whom to ask to buy or renew a license: BOBRES_SELLER_CONTACT,
	// "" when the owner set none.
	Contact string `json:"contact"`
}

// licenseInfoUsage is what the limits are used for. Servers is null here:
// the provisioner knows them, and this command does not ask it.
type licenseInfoUsage struct {
	Servers        *int     `json:"servers"`
	ActiveServices int      `json:"active_services"`
	StaffSeats     int      `json:"staff_seats"`
	Gateways       []string `json:"gateways"`
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

func unixOf(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

// writeInfo prints eff as licenseInfo JSON.
func writeInfo(out, errOut io.Writer, eff *domain.EffectiveLicense, usage *domain.LicenseUsage) int {
	st := eff.License
	info := licenseInfo{InstallID: st.InstallID, Domain: st.Domain, Version: version.Version, State: st.State,
		Reason: st.Reason, Valid: st.Valid(), UpdatedAt: unixOf(st.UpdatedAt), RefreshedAt: unixOf(st.RefreshedAt)}
	if st.Source != "" {
		info.Source = &st.Source
	}
	if t := st.Token; t != nil {
		info.License = &licenseInfoToken{LicenseID: t.LicenseID, Customer: t.Customer, Tier: t.Tier, KeyID: t.KeyID,
			IssuedAt: t.IssuedAt.Unix(), ExpiresAt: t.ExpiresAt.Unix(), GraceDays: t.GraceDays, GraceEndsAt: st.GraceEndsAt.Unix(),
			Domain: t.Domain, InstallID: t.InstallID, Entitlements: t.Entitlements, LicenseEndsAt: unixOf(st.Token.LicenseEndsAt)}
	}
	info.Effective = licenseInfoEffective{Basis: string(eff.Basis), Tier: eff.Tier, TrialDaysLeft: eff.TrialDaysLeft(),
		TrialDays: license.TrialDays, Entitlements: eff.Entitlements, Contact: eff.Contact,
		Usage: licenseInfoUsage{Servers: usage.Servers, ActiveServices: usage.ActiveServices, StaffSeats: usage.StaffSeats,
			Gateways: usage.Gateways}}
	if !eff.TrialEndsAt.IsZero() {
		end := eff.TrialEndsAt.Unix()
		info.Effective.TrialEndsAt = &end
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

// printLicense writes the license as `license status` shows it: the
// installed token, then what is in effect (a license, its grace period, the
// implicit trial or the free tier) with the use of each limit. The State row
// starts with the state's name, and the Updates until row is the end of the
// updates the license in effect covers (`bobres update` reads both).
func printLicense(w io.Writer, eff *domain.EffectiveLicense, usage *domain.LicenseUsage, now time.Time) {
	st := eff.License
	state := st.State
	switch st.State {
	case domain.LicenseMissing:
		state = "missing (no license: the free tier applies when the trial is over)"
	case domain.LicenseInvalid:
		state = fmt.Sprintf("invalid: %s (%s); counts as no license", domain.LicenseReasonText(st.Reason), st.Reason)
	case domain.LicenseGrace:
		state = "grace (expired; its entitlements still hold: renew it)"
	case domain.LicenseExpired:
		state = "expired (past its grace period: the trial or the free tier applies; renew it)"
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
	if t := st.Token; t != nil {
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
	}
	row("In effect", inEffect(eff))
	if !eff.TrialEndsAt.IsZero() && eff.Basis != license.BasisLicense {
		end := eff.TrialEndsAt.UTC().Format(time.DateOnly)
		if eff.TrialRunning() {
			row("Trial ends", fmt.Sprintf("%s (%s)", end, daysLeft(now, eff.TrialEndsAt)))
		} else {
			row("Trial ended", end)
		}
	}
	e := eff.Entitlements
	row("Servers", limitUse(e.MaxServers, usage.Servers))
	active, seats := usage.ActiveServices, usage.StaffSeats
	row("Services", limitUse(e.MaxActiveServices, &active))
	row("Staff seats", limitUse(e.DashboardStaffSeat, &seats))
	row("Gateways", orNone(strings.Join(e.Gateways, ", ")))
	row("Features", orNone(strings.Join(features(e), ", ")))
	// The end of the updates the license in effect covers: none on the free
	// tier, in the trial and after a license has ended (security updates are
	// never withheld).
	if e.UpdatesUntil != "" {
		row("Updates until", e.UpdatesUntil)
	}
	// Whom to ask to buy or renew, when the owner set one (BOBRES_SELLER_CONTACT).
	if eff.Basis != license.BasisLicense && eff.Contact != "" {
		row("Buy or renew", eff.Contact)
	}
}

// inEffect says what applies, in words.
func inEffect(eff *domain.EffectiveLicense) string {
	switch eff.Basis {
	case license.BasisLicense:
		return "license (" + eff.Tier + ")"
	case license.BasisGrace:
		return "license (" + eff.Tier + ") in its grace period"
	case license.BasisTrial:
		return fmt.Sprintf("trial (%d days left): the free tier's limits apply when it ends", eff.TrialDaysLeft())
	}
	return "free tier (no license, and the trial is over)"
}

// limitUse is a limit with its use ("25 (3 in use)"); the use is left out
// when it is not known here.
func limitUse(limitN int, used *int) string {
	if used == nil {
		return limit(limitN)
	}
	return fmt.Sprintf("%s (%d in use)", limit(limitN), *used)
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
