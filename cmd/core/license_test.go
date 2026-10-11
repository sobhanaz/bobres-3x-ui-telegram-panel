package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// Wrong use answers with the usage (exit 2) before touching the database.
func TestLicenseCmdUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"activate"}, {"status", "extra"}} {
		var out, errOut bytes.Buffer
		if code := licenseCmd(args, strings.NewReader(""), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage:") {
			t.Errorf("%v: %d %q", args, code, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	if code := licenseCmd([]string{"import"}, strings.NewReader(" \n"), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "no token") {
		t.Errorf("empty import: %d %q", code, errOut.String())
	}
}

func TestReadToken(t *testing.T) {
	got, err := readToken(strings.NewReader("  eyJhIjoxfQ.\r\nc2ln \n"))
	if err != nil || got != "eyJhIjoxfQ.c2ln" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := readToken(strings.NewReader(strings.Repeat("a", maxTokenInput+1))); err == nil {
		t.Fatal("an oversized token was read")
	}
}

func TestPrintLicense(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	var b bytes.Buffer
	printLicense(&b, &domain.LicenseStatus{State: domain.LicenseInvalid, Reason: "unknown_key", InstallID: "inst-1"}, now)
	if s := b.String(); !strings.Contains(s, "invalid: this build has no public key") || !strings.Contains(s, "inst-1") ||
		!strings.Contains(s, "Domain:         -") {
		t.Errorf("invalid:\n%s", s)
	}
	b.Reset()
	tok := &license.Token{LicenseID: "lic_9", Tier: "pro", KeyID: "v1", Customer: "Shop", Domain: "panel.example.com",
		IssuedAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour),
		Entitlements: license.Entitlements{MaxServers: 3, MaxActiveServices: license.Unlimited, Resellers: true,
			Gateways: []string{"zarinpal", "stars"}, UpdatesUntil: "2027-09-30"}}
	printLicense(&b, &domain.LicenseStatus{State: domain.LicenseActive, Token: tok, Domain: "panel.example.com",
		GraceEndsAt: tok.ExpiresAt.Add(14 * 24 * time.Hour), Keys: 1}, now)
	for _, want := range []string{"State:          active", "lic_9 (pro)", "2026-11-10 (30 days left)", "Grace until:    2026-11-24",
		"Servers:        3", "Services:       unlimited", "Staff seats:    none", "zarinpal, stars", "Features:       resellers",
		"Updates until:  2027-09-30", "Bound to:       panel.example.com"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in:\n%s", want, b.String())
		}
	}
}

func TestSelfURL(t *testing.T) {
	for addr, want := range map[string]string{":8080": "http://127.0.0.1:8080", "0.0.0.0:9000": "http://127.0.0.1:9000",
		"[::]:8080": "http://127.0.0.1:8080", "10.0.0.2:8080": "http://10.0.0.2:8080", "bad": ""} {
		if got := selfURL(addr); got != want {
			t.Errorf("%q: %q", addr, got)
		}
	}
}
