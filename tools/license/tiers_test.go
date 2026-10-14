package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// tierKey makes a throwaway signing key (runtime only: no key is ever
// committed) and returns its path and the ring that verifies its tokens.
func tierKey(t *testing.T) (string, license.KeyRing) {
	t.Helper()
	dir := t.TempDir()
	if gitCheckout(dir) != "" {
		t.Skip("the temporary directory is inside a git checkout")
	}
	path := filepath.Join(dir, "tier.key")
	var out, errOut bytes.Buffer
	if code := run([]string{"keygen", "-kid", "v1-tier", "-out", path}, nil, &out, &errOut); code != 0 {
		t.Fatalf("keygen: %d %s", code, errOut.String())
	}
	pub, err := license.ParsePublicKey(out.String())
	if err != nil {
		t.Fatal(err)
	}
	return path, license.KeyRing{"v1-tier": pub}
}

func signTier(t *testing.T, key string, ring license.KeyRing, args ...string) (license.Token, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(append([]string{"sign", "-key", key}, args...), nil, &out, &errOut)
	if code != 0 {
		return license.Token{}, errOut.String(), code
	}
	tok, err := license.Verify(ring, strings.TrimSpace(out.String()), license.Options{Domain: "panel.example.com", InstallID: "inst_1", RequireBound: true})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return tok, errOut.String(), code
}

// Each tier signs a token with its preset's entitlements, and only what the
// vendor gave changes.
func TestSignATier(t *testing.T) {
	key, ring := tierKey(t)
	presets, err := license.LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	const now = "2026-10-14T09:00:00Z"
	common := []string{"-domain", "Panel.Example.com", "-customer", "Acme VPN", "-now", now}

	// standard, monthly: the preset's entitlements, one calendar month, its default grace.
	tok, _, code := signTier(t, key, ring, append([]string{"--tier", "standard", "--months", "1"}, common...)...)
	std, _ := presets.Get(license.TierStandard)
	if code != 0 || tok.Tier != "standard" || tok.Customer != "Acme VPN" || !reflect.DeepEqual(tok.Entitlements, std.Entitlements) ||
		!tok.ExpiresAt.Equal(time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)) || tok.GraceDays != 14 ||
		!strings.HasPrefix(tok.LicenseID, "lic_") || tok.KeyID != "v1-tier" || tok.InstallID != "" {
		t.Fatalf("standard: %d %+v", code, tok)
	}

	// pro, yearly, with the options: grace, updates_until, an install binding, a chosen id.
	tok, _, code = signTier(t, key, ring, append([]string{"-tier", "pro", "-months", "12", "-grace-days", "30",
		"-updates-until", "2027-10-31", "-install-id", "inst_1", "-license-id", "lic_fixed1"}, common...)...)
	pro, _ := presets.Get(license.TierPro)
	want := pro.Entitlements
	want.UpdatesUntil = "2027-10-31"
	if code != 0 || (!reflect.DeepEqual(tok.Entitlements, want) || !tok.Entitlements.WhiteLabel ||
		!tok.ExpiresAt.Equal(time.Date(2027, 10, 14, 9, 0, 0, 0, time.UTC)) || tok.GraceDays != 30 ||
		tok.InstallID != "inst_1" || tok.LicenseID != "lic_fixed1") {
		t.Fatalf("pro: %d %+v", code, tok)
	}

	// trial: 30 days by default, no grace, pro limits without white label.
	tok, _, code = signTier(t, key, ring, append([]string{"-tier", "trial"}, common...)...)
	if code != 0 || tok.Tier != "trial" || !tok.ExpiresAt.Equal(time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)) ||
		tok.GraceDays != 0 || tok.Entitlements.WhiteLabel || tok.Entitlements.MaxServers != license.Unlimited {
		t.Fatalf("trial: %d %+v", code, tok)
	}
	// ... or a different length when the vendor says so (an extended trial); grace 0 given is honoured.
	tok, _, code = signTier(t, key, ring, append([]string{"-tier", "trial", "-days", "14", "-grace-days", "0"}, common...)...)
	if code != 0 || !tok.ExpiresAt.Equal(time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("short trial: %d %+v", code, tok)
	}
	// An end date.
	tok, _, code = signTier(t, key, ring, append([]string{"-tier", "standard", "-expires", "2027-01-01"}, common...)...)
	if code != 0 || !tok.ExpiresAt.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expires: %d %+v", code, tok)
	}

	// The core's own check of a token of every tier: it verifies for its domain.
	for _, name := range presets.Names() {
		args := append([]string{"-tier", name, "-months", "1"}, common...)
		if _, msg, code := signTier(t, key, ring, args...); code != 0 {
			t.Errorf("%s: %d %s", name, code, msg)
		}
	}
}

func TestSignATierRefusals(t *testing.T) {
	key, ring := tierKey(t)
	ok := []string{"-domain", "panel.example.com", "-customer", "Acme"}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no duration":       {append([]string{"-tier", "standard"}, ok...), "how long"},
		"two durations":     {append([]string{"-tier", "standard", "-months", "1", "-days", "30"}, ok...), "one of"},
		"unknown tier":      {append([]string{"-tier", "gold", "-months", "1"}, ok...), "the tiers are standard, pro, trial"},
		"no domain":         {[]string{"-tier", "standard", "-months", "1", "-customer", "Acme"}, "-domain"},
		"no customer":       {[]string{"-tier", "standard", "-months", "1", "-domain", "panel.example.com"}, "-customer"},
		"months too long":   {append([]string{"-tier", "standard", "-months", "121"}, ok...), "-months"},
		"negative days":     {append([]string{"-tier", "standard", "-days", "-1"}, ok...), "-days"},
		"bad expires":       {append([]string{"-tier", "standard", "-expires", "soon"}, ok...), "-expires"},
		"expires in past":   {append([]string{"-tier", "standard", "-expires", "2020-01-01", "-now", "2026-10-14T00:00:00Z"}, ok...), "expires_at"},
		"grace too long":    {append([]string{"-tier", "standard", "-months", "1", "-grace-days", "91"}, ok...), "grace_days"},
		"bad updates-until": {append([]string{"-tier", "standard", "-months", "1", "-updates-until", "2027"}, ok...), "-updates-until"},
		"tier with payload": {append([]string{"-tier", "standard", "-months", "1", "-in", "payload.json"}, ok...), "give one"},
		"bad now":           {append([]string{"-tier", "standard", "-months", "1", "-now", "yesterday"}, ok...), "-now"},
	} {
		_, msg, code := signTier(t, key, ring, c.args...)
		if code != 1 || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %d %q (want %q)", name, code, msg, c.want)
		}
	}
}

// A license whose max_active_services is 0 or missing is still signed, and
// the vendor is told that the install will floor it at the free tier's 25
// (a payload, and not a tier, is the only way to make one).
func TestSignWarnsWhenTheFloorApplies(t *testing.T) {
	key, ring := tierKey(t)
	const head = `{"license_id":"lic_w","customer":"Shop","domain":"panel.example.com","tier":"standard","expires_at":"2027-10-11T00:00:00Z","grace_days":14,`
	const warning = "WARNING: max_active_services is 0 or missing: the install will floor it at the free tier's 25"
	for name, c := range map[string]struct {
		payload string
		warns   bool
	}{
		"left out": {head + `"entitlements":{"max_servers":2,"dashboard_staff_seats":3}}`, true},
		"zero":     {head + `"entitlements":{"max_servers":2,"max_active_services":0,"dashboard_staff_seats":3}}`, true},
		"a count":  {head + `"entitlements":{"max_servers":2,"max_active_services":500,"dashboard_staff_seats":3}}`, false},
		"no limit": {head + `"entitlements":{"max_servers":2,"max_active_services":-1,"dashboard_staff_seats":3}}`, false},
	} {
		var out, errOut bytes.Buffer
		code := run([]string{"sign", "-key", key, "-now", "2026-10-11T00:00:00Z"}, strings.NewReader(c.payload), &out, &errOut)
		if code != 0 {
			t.Errorf("%s: it must still sign: %d %s", name, code, errOut.String())
			continue
		}
		if got := strings.Contains(errOut.String(), warning); got != c.warns {
			t.Errorf("%s: warning %v, want %v: %q", name, got, c.warns, errOut.String())
		}
		if strings.Contains(out.String(), "WARNING") {
			t.Errorf("%s: the warning went to stdout, where the token is: %q", name, out.String())
		}
		if _, err := license.Parse(ring, strings.TrimSpace(out.String())); err != nil {
			t.Errorf("%s: the token does not verify: %v", name, err)
		}
	}
	// A tier preset never warns.
	for _, name := range []string{"standard", "pro", "trial"} {
		_, msg, code := signTier(t, key, ring, "-tier", name, "-months", "1", "-domain", "panel.example.com", "-customer", "Acme")
		if code != 0 || strings.Contains(msg, "WARNING") {
			t.Errorf("%s: %d %q", name, code, msg)
		}
	}
}

// The presets' gateways are every gateway core knows: a gateway added to
// core without a preset update would leave the paid tiers short of it.
func TestPresetsNameEveryGateway(t *testing.T) {
	presets, err := license.LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(domain.LicenseGateways)
	slices.Sort(want)
	for _, name := range presets.Names() {
		p, _ := presets.Get(name)
		got := slices.Clone(p.Entitlements.Gateways)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s gateways %v, core knows %v", name, got, want)
		}
	}
}

func TestTiersEntitlementsAndFingerprint(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"tiers"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("tiers: %d %s", code, errOut.String())
	}
	for _, want := range []string{"standard", "pro", "trial", "unlimited", "30 days", "not sold"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("tiers lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := run([]string{"tiers", "-json"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("tiers -json: %d", code)
	}
	if _, err := license.ParsePresets(&out); err != nil {
		t.Fatalf("tiers -json is not a presets file: %v", err)
	}

	// entitlements prints what `licensesrv key create -entitlements` reads.
	presets, _ := license.LoadPresets()
	for _, name := range presets.Names() {
		out.Reset()
		if code := run([]string{"entitlements", name}, nil, &out, &errOut); code != 0 {
			t.Fatalf("entitlements %s: %d", name, code)
		}
		var e license.Entitlements
		dec := json.NewDecoder(&out)
		dec.DisallowUnknownFields()
		p, _ := presets.Get(name)
		if err := dec.Decode(&e); err != nil || !reflect.DeepEqual(e, p.Entitlements) {
			t.Errorf("%s: %+v %v", name, e, err)
		}
	}
	if code := run([]string{"entitlements", "gold"}, nil, &out, &errOut); code != 1 {
		t.Errorf("entitlements gold: %d", code)
	}
	if code := run([]string{"entitlements"}, nil, &out, &errOut); code != 2 {
		t.Errorf("entitlements: %d", code)
	}

	// fingerprint reads a key file; the vendor's embedded key has the pinned one.
	out.Reset()
	pubs, err := filepath.Glob(filepath.Join("..", "..", "internal", "license", "keys", "*.pub"))
	if err != nil || len(pubs) != 1 {
		t.Fatalf("the embedded public key files: %v %v", pubs, err)
	}
	if code := run([]string{"fingerprint", pubs[0]}, nil, &out, &errOut); code != 0 ||
		strings.TrimSpace(out.String()) != "2E02 AB14 5379 C040 2DBD 7BC2 51A3 C9D7" {
		t.Errorf("fingerprint: %d %q %s", code, out.String(), errOut.String())
	}
	if code := run([]string{"fingerprint", filepath.Join(t.TempDir(), "none.pub")}, nil, &out, &errOut); code != 1 {
		t.Errorf("fingerprint of nothing: %d", code)
	}
}
