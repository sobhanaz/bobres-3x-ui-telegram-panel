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

// testSellerContact is the contact the owner set (BOBRES_SELLER_CONTACT): a
// test value, not a handle that exists.
const testSellerContact = "seller@example.test"

// effectiveOf is what is in effect for st when its trial began started before
// now (a token that did not verify here counts for nothing).
func effectiveOf(st *domain.LicenseStatus, started time.Duration, now time.Time) *domain.EffectiveLicense {
	tok, state := (*license.Token)(nil), license.Expired
	if st.Token != nil && st.Valid() {
		tok = st.Token
		switch st.State {
		case domain.LicenseActive:
			state = license.Active
		case domain.LicenseGrace:
			state = license.Grace
		}
	}
	return &domain.EffectiveLicense{Effective: license.Resolve(tok, state, now.Add(-started), now), License: st, Now: now, Contact: testSellerContact}
}

// license info is JSON for bobres: the install id and domain the license
// server binds to, core's version, the state and the payload (never the
// token), and what is in effect with its use.
func TestWriteInfo(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	servers := 2
	usage := &domain.LicenseUsage{ActiveServices: 7, StaffSeats: 1, Gateways: []string{"manual_card", "zarinpal"}}
	var out, errOut bytes.Buffer
	missing := &domain.LicenseStatus{State: domain.LicenseMissing, InstallID: "inst-1", Domain: "panel.example.com"}
	if code := writeInfo(&out, &errOut, effectiveOf(missing, 5*24*time.Hour, now), usage); code != 0 {
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
	eff, _ := got["effective"].(map[string]any)
	ents, _ := eff["entitlements"].(map[string]any)
	use, _ := eff["usage"].(map[string]any)
	if eff["basis"] != "trial" || eff["tier"] != "trial" || eff["trial_days_left"] != 25.0 || eff["trial_days"] != 30.0 || eff["contact"] != testSellerContact ||
		eff["trial_ends_at"] != float64(now.Add(25*24*time.Hour).Unix()) || ents["max_servers"] != -1.0 || ents["resellers"] != true ||
		use["active_services"] != 7.0 || use["staff_seats"] != 1.0 || use["servers"] != nil {
		t.Fatalf("trial:\n%s", out.String())
	}

	// The trial is over: the free tier, with the servers the caller knows.
	usage.Servers = &servers
	out.Reset()
	if code := writeInfo(&out, &errOut, effectiveOf(missing, 40*24*time.Hour, now), usage); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	got = nil
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	eff, _ = got["effective"].(map[string]any)
	ents, _ = eff["entitlements"].(map[string]any)
	use, _ = eff["usage"].(map[string]any)
	if eff["basis"] != "free" || eff["tier"] != "free" || eff["trial_days_left"] != 0.0 || ents["max_servers"] != 1.0 ||
		ents["max_active_services"] != 25.0 || ents["dashboard_staff_seats"] != 2.0 || ents["resellers"] != false || use["servers"] != 2.0 {
		t.Fatalf("free:\n%s", out.String())
	}

	ends := now.Add(365 * 24 * time.Hour)
	tok := &license.Token{LicenseID: "lic_9", Tier: "pro", KeyID: "srv-2026", Customer: "Shop", Domain: "panel.example.com",
		InstallID: "inst-1", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), GraceDays: 14,
		LicenseEndsAt: &ends, Entitlements: license.Entitlements{MaxServers: 3, UpdatesUntil: "2027-09-30", Gateways: []string{"zarinpal"}}}
	source, refreshed := "refresh", now.Add(-time.Minute)
	out.Reset()
	active := &domain.LicenseStatus{State: domain.LicenseActive, Token: tok, InstallID: "inst-1",
		Domain: "panel.example.com", GraceEndsAt: tok.ExpiresAt.Add(14 * 24 * time.Hour), UpdatedAt: &now, Source: source,
		RefreshedAt: &refreshed}
	if code := writeInfo(&out, &errOut, effectiveOf(active, 400*24*time.Hour, now), usage); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	got = nil
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	lic, _ := got["license"].(map[string]any)
	ent, _ := lic["entitlements"].(map[string]any)
	eff, _ = got["effective"].(map[string]any)
	if got["valid"] != true || got["source"] != "refresh" || got["refreshed_at"] != float64(refreshed.Unix()) ||
		got["updated_at"] != float64(now.Unix()) || lic["license_id"] != "lic_9" || lic["kid"] != "srv-2026" ||
		lic["expires_at"] != float64(tok.ExpiresAt.Unix()) || lic["license_ends_at"] != float64(ends.Unix()) ||
		lic["grace_ends_at"] != float64(tok.ExpiresAt.Add(14*24*time.Hour).Unix()) || ent["updates_until"] != "2027-09-30" ||
		ent["max_servers"] != 3.0 || eff["basis"] != "license" || eff["tier"] != "pro" {
		t.Fatalf("active:\n%s", out.String())
	}
	if strings.Contains(out.String(), "token") {
		t.Fatalf("a token field in:\n%s", out.String())
	}
}

// Without a contact (BOBRES_SELLER_CONTACT unset: the build ships none) the
// "Buy or renew" row is left out and the JSON contact is empty, never a
// made-up handle.
func TestNoSellerContact(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	missing := &domain.LicenseStatus{State: domain.LicenseMissing, InstallID: "inst-1"}
	eff := effectiveOf(missing, 40*24*time.Hour, now)
	eff.Contact = ""
	var b bytes.Buffer
	printLicense(&b, eff, &domain.LicenseUsage{}, now)
	if strings.Contains(b.String(), "Buy or renew") || !strings.Contains(b.String(), "In effect:      free tier") {
		t.Errorf("a Buy or renew row without a contact:\n%s", b.String())
	}
	var out, errOut bytes.Buffer
	if code := writeInfo(&out, &errOut, eff, &domain.LicenseUsage{}); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if e, _ := got["effective"].(map[string]any); e["contact"] != "" {
		t.Errorf("contact: %v", e["contact"])
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
	servers := 1
	usage := &domain.LicenseUsage{ActiveServices: 3, StaffSeats: 2}
	day := 24 * time.Hour
	var b bytes.Buffer
	invalid := &domain.LicenseStatus{State: domain.LicenseInvalid, Reason: "unknown_key", InstallID: "inst-1"}
	printLicense(&b, effectiveOf(invalid, 40*day, now), usage, now)
	if s := b.String(); !strings.Contains(s, "invalid: this build carries no vendor key") || !strings.Contains(s, "inst-1") ||
		!strings.Contains(s, "Domain:         -") || !strings.Contains(s, "In effect:      free tier") ||
		!strings.Contains(s, "Buy or renew:   "+testSellerContact) || !strings.Contains(s, "Trial ended:    2026-10-01") {
		t.Errorf("invalid:\n%s", s)
	}
	// No license and a running trial: its end and days, what the trial
	// allows, and the use of each limit.
	b.Reset()
	missing := &domain.LicenseStatus{State: domain.LicenseMissing, InstallID: "inst-1"}
	printLicense(&b, effectiveOf(missing, 5*day, now), &domain.LicenseUsage{Servers: &servers, ActiveServices: 3, StaffSeats: 2}, now)
	for _, want := range []string{"State:          missing (no license", "In effect:      trial (25 days left)", "Trial ends:     2026-11-05 (25 days left)",
		"Servers:        unlimited (1 in use)", "Services:       unlimited (3 in use)", "Staff seats:    5 (2 in use)",
		"Features:       resellers, crypto_watcher", "Buy or renew:   " + testSellerContact} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("trial: missing %q in:\n%s", want, b.String())
		}
	}
	// The free tier's numbers; the servers are not known to this command.
	b.Reset()
	printLicense(&b, effectiveOf(missing, 40*day, now), usage, now)
	for _, want := range []string{"In effect:      free tier", "Servers:        1\n", "Services:       25 (3 in use)", "Staff seats:    2 (2 in use)",
		"Features:       -", "Gateways:       zarinpal, idpay, nextpay, stars, manual_card, manual_crypto, manual_zarinpal"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("free: missing %q in:\n%s", want, b.String())
		}
	}
	b.Reset()
	tok := &license.Token{LicenseID: "lic_9", Tier: "pro", KeyID: "v1", Customer: "Shop", Domain: "panel.example.com",
		IssuedAt: now.Add(-24 * time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour),
		Entitlements: license.Entitlements{MaxServers: 3, MaxActiveServices: license.Unlimited, Resellers: true,
			Gateways: []string{"zarinpal", "stars"}, UpdatesUntil: "2027-09-30"}}
	active := &domain.LicenseStatus{State: domain.LicenseActive, Token: tok, Domain: "panel.example.com",
		GraceEndsAt: tok.ExpiresAt.Add(14 * 24 * time.Hour), Keys: 1}
	printLicense(&b, effectiveOf(active, 40*day, now), usage, now)
	for _, want := range []string{"State:          active", "lic_9 (pro)", "2026-11-10 (30 days left)", "Grace until:    2026-11-24",
		"In effect:      license (pro)", "Servers:        3", "Services:       unlimited (3 in use)", "Staff seats:    2 (2 in use)",
		"zarinpal, stars", "Features:       resellers", "Updates until:  2027-09-30", "Bound to:       panel.example.com"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in:\n%s", want, b.String())
		}
	}
	if strings.Contains(b.String(), "Buy or renew") || strings.Contains(b.String(), "Trial") {
		t.Errorf("a license in force mentions the trial or the seller:\n%s", b.String())
	}
	// An ended license puts no end on updates (bobres update reads the Updates
	// until row): the free tier is what applies, and security updates are
	// never withheld.
	b.Reset()
	ended := &domain.LicenseStatus{State: domain.LicenseExpired, Token: tok, Domain: "panel.example.com",
		GraceEndsAt: now.Add(-24 * time.Hour)}
	printLicense(&b, effectiveOf(ended, 40*day, now), usage, now)
	for _, want := range []string{"State:          expired (past its grace period", "In effect:      free tier"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("ended: missing %q in:\n%s", want, b.String())
		}
	}
	if strings.Contains(b.String(), "Updates until") {
		t.Errorf("an ended license names an end to updates:\n%s", b.String())
	}
	// The same ended license under a running trial: no end either.
	b.Reset()
	printLicense(&b, effectiveOf(ended, 5*day, now), usage, now)
	if !strings.Contains(b.String(), "In effect:      trial") || strings.Contains(b.String(), "Updates until") {
		t.Errorf("ended, trial running:\n%s", b.String())
	}
}

// How the token came, and when the license itself ends.
func TestPrintLicenseSource(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	ends := now.Add(200 * 24 * time.Hour)
	refreshed := now.Add(-2 * time.Hour)
	tok := &license.Token{LicenseID: "lic_9", Tier: "pro", KeyID: "srv-2026", Domain: "panel.example.com",
		IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(30 * 24 * time.Hour), LicenseEndsAt: &ends}
	usage := &domain.LicenseUsage{}
	for source, want := range map[string]string{
		"manual":     "Source:         pasted or imported (not renewed online)",
		"activation": "Source:         activated with a license key (renewed online daily)",
		"refresh":    "Source:         renewed online on 2026-10-11 (daily)",
	} {
		var b bytes.Buffer
		st := &domain.LicenseStatus{State: domain.LicenseActive, Token: tok, Source: source, RefreshedAt: &refreshed}
		printLicense(&b, effectiveOf(st, 0, now), usage, now)
		if !strings.Contains(b.String(), want) || !strings.Contains(b.String(), "License ends:   2027-04-29 (200 days left)") {
			t.Errorf("%s:\n%s", source, b.String())
		}
	}
	var b bytes.Buffer
	st := &domain.LicenseStatus{State: domain.LicenseMissing}
	printLicense(&b, effectiveOf(st, 0, now), usage, now)
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
