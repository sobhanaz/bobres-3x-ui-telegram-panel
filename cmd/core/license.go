package main

import (
	"context"
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
)

const licenseUsage = "usage: license status | license import  (the token on stdin)"

// licenseCmd: "license status" shows the installed license as this install
// sees it; "license import" installs the token read from stdin (audited as
// license.set from the CLI). `bobres license status|import` runs them inside
// the core container.
func licenseCmd(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) != 1 || (args[0] != "status" && args[0] != "import") {
		fmt.Fprintln(errOut, licenseUsage)
		return 2
	}
	var token string
	if args[0] == "import" {
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
	if token == "" {
		lic, err = dom.License(ctx)
	} else {
		lic, err = dom.SetLicense(store.WithSource(ctx, store.SourceCLI), nil, token, "imported with the command line")
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
	if token != "" {
		fmt.Fprintln(out, "License installed.")
	}
	printLicense(out, lic, time.Now())
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
