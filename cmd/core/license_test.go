package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// Wrong use answers with the usage (exit 2) before touching the database.
func TestLicenseCmdUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"activate"}, {"status", "extra"}, {"info", "--yaml"}, {"import", "--activated", "x"},
		{"status", "--json"}, {"--activated", "import"}} {
		var out, errOut bytes.Buffer
		if code := licenseCmd(args, strings.NewReader(""), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage:") {
			t.Errorf("%v: %d %q", args, code, errOut.String())
		}
	}
	for _, args := range [][]string{{"import"}, {"import", "--activated"}} {
		var out, errOut bytes.Buffer
		if code := licenseCmd(args, strings.NewReader(" \n"), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "no token") {
			t.Errorf("empty %v: %d %q", args, code, errOut.String())
		}
	}
}

func TestLicenseArgs(t *testing.T) {
	for in, want := range map[string]string{"status": "status", "info": "info", "info --json": "info", "import": "import",
		"import --activated": "import --activated"} {
		if got, ok := licenseArgs(strings.Fields(in)); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
}

// license info is JSON for bobres: the install id and domain the license
// server binds to, core's version, the state and the payload (never the
// token).
func TestWriteInfo(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	var out, errOut bytes.Buffer
	if code := writeInfo(&out, &errOut, &domain.LicenseStatus{State: domain.LicenseMissing, InstallID: "inst-1",
		Domain: "panel.example.com"}); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"install_id": "inst-1", "domain": "panel.example.com", "version": version.Version,
		"state": "missing", "reason": "", "valid": false, "source": nil, "updated_at": nil, "refreshed_at": nil, "license": nil} {
		if v, ok := got[k]; !ok || v != want {
			t.Errorf("missing: %s = %v, want %v", k, v, want)
		}
	}

	ends := now.Add(365 * 24 * time.Hour)
	tok := &license.Token{LicenseID: "lic_9", Tier: "pro", KeyID: "srv-2026", Customer: "Shop", Domain: "panel.example.com",
		InstallID: "inst-1", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), GraceDays: 14,
		LicenseEndsAt: &ends, Entitlements: license.Entitlements{MaxServers: 3, UpdatesUntil: "2027-09-30", Gateways: []string{"zarinpal"}}}
	source, refreshed := "refresh", now.Add(-time.Minute)
	out.Reset()
	if code := writeInfo(&out, &errOut, &domain.LicenseStatus{State: domain.LicenseActive, Token: tok, InstallID: "inst-1",
		Domain: "panel.example.com", GraceEndsAt: tok.ExpiresAt.Add(14 * 24 * time.Hour), UpdatedAt: &now, Source: source,
		RefreshedAt: &refreshed}); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	got = nil
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	lic, _ := got["license"].(map[string]any)
	ent, _ := lic["entitlements"].(map[string]any)
	if got["valid"] != true || got["source"] != "refresh" || got["refreshed_at"] != float64(refreshed.Unix()) ||
		got["updated_at"] != float64(now.Unix()) || lic["license_id"] != "lic_9" || lic["kid"] != "srv-2026" ||
		lic["expires_at"] != float64(tok.ExpiresAt.Unix()) || lic["license_ends_at"] != float64(ends.Unix()) ||
		lic["grace_ends_at"] != float64(tok.ExpiresAt.Add(14*24*time.Hour).Unix()) || ent["updates_until"] != "2027-09-30" ||
		ent["max_servers"] != 3.0 {
		t.Fatalf("active:\n%s", out.String())
	}
	if strings.Contains(out.String(), "token") {
		t.Fatalf("a token field in:\n%s", out.String())
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

// How the token came, and when the license itself ends.
func TestPrintLicenseSource(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ends := now.Add(200 * 24 * time.Hour)
	refreshed := now.Add(-2 * time.Hour)
	tok := &license.Token{LicenseID: "lic_9", Tier: "pro", KeyID: "srv-2026", Domain: "panel.example.com",
		IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), LicenseEndsAt: &ends}
	for source, want := range map[string]string{
		"manual":     "Source:         pasted or imported (not renewed online)",
		"activation": "Source:         activated with a license key (renewed online daily)",
		"refresh":    "Source:         renewed online on 2026-10-11 (daily)",
	} {
		var b bytes.Buffer
		printLicense(&b, &domain.LicenseStatus{State: domain.LicenseActive, Token: tok, Source: source, RefreshedAt: &refreshed}, now)
		if !strings.Contains(b.String(), want) || !strings.Contains(b.String(), "License ends:   2027-04-29 (200 days left)") {
			t.Errorf("%s:\n%s", source, b.String())
		}
	}
	var b bytes.Buffer
	printLicense(&b, &domain.LicenseStatus{State: domain.LicenseMissing}, now)
	if strings.Contains(b.String(), "Source") {
		t.Errorf("missing:\n%s", b.String())
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
