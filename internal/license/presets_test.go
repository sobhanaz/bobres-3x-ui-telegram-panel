package license

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// The presets built into the binary are the tiers docs/pricing.md sells.
func TestEmbeddedPresets(t *testing.T) {
	p, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Names(), ","); got != "standard,pro,trial" {
		t.Fatalf("tiers: %s", got)
	}
	all := []string{"zarinpal", "stars", "usdt_trc20_auto", "manual_card", "manual_crypto", "manual_zarinpal"}
	std, _ := p.Get(TierStandard)
	e := std.Entitlements
	if e.MaxServers != 3 || e.MaxActiveServices != 500 || !e.Resellers || !e.CryptoWatcher || e.DashboardStaffSeat != 5 ||
		e.WhiteLabel || strings.Join(e.Gateways, ",") != strings.Join(all, ",") {
		t.Errorf("standard: %+v", e)
	}
	pro, _ := p.Get(TierPro)
	e = pro.Entitlements
	if e.MaxServers != Unlimited || e.MaxActiveServices != Unlimited || !e.Resellers || !e.CryptoWatcher ||
		e.DashboardStaffSeat != Unlimited || !e.WhiteLabel || strings.Join(e.Gateways, ",") != strings.Join(all, ",") {
		t.Errorf("pro: %+v", e)
	}
	trial, _ := p.Get(TierTrial)
	if trial.Days != 30 || trial.GraceDays != 0 || trial.Price != nil {
		t.Errorf("trial: %+v", trial)
	}
	// The trial license is the implicit trial (TrialEntitlements): the pro
	// limits without white label, five staff seats.
	if got, want := trial.Entitlements, TrialEntitlements(); !reflect.DeepEqual(got, want) {
		t.Errorf("trial entitlements: %+v, the implicit trial has %+v", got, want)
	}
	// The sold tiers carry a price block (a placeholder until the owner fills it).
	for _, n := range []string{TierStandard, TierPro} {
		if tier, _ := p.Get(n); tier.Price == nil {
			t.Errorf("%s has no price block", n)
		}
	}
	if len(p.Readme) == 0 || !strings.Contains(strings.Join(p.Readme, "\n"), "FREE TIER") {
		t.Error("the header must say what the free tier is")
	}
}

// A tier is never worth less than no license: every preset names every
// gateway and grants at least the free tier in each field, so no tier depends
// on the policy's floor (Entitlements.AtLeast) to match what it was sold as.
func TestPresetsAreNotBelowTheFreeTier(t *testing.T) {
	p, err := LoadPresets()
	if err != nil {
		t.Fatal(err)
	}
	free := FreeEntitlements()
	for _, name := range p.Names() {
		tier, _ := p.Get(name)
		e := tier.Entitlements
		atLeast := func(have, floor int) bool { return have == Unlimited || (floor != Unlimited && have >= floor) }
		if !atLeast(e.MaxServers, free.MaxServers) || !atLeast(e.MaxActiveServices, free.MaxActiveServices) ||
			!atLeast(e.DashboardStaffSeat, free.DashboardStaffSeat) {
			t.Errorf("%s grants less than the free tier: %+v", name, e)
		}
		for _, g := range AllGateways() {
			if !slices.Contains(e.Gateways, g) {
				t.Errorf("%s does not name the gateway %s", name, g)
			}
		}
		if !reflect.DeepEqual(e.AtLeast(free), e) {
			t.Errorf("%s would be changed by the free tier's floor: %+v", name, e)
		}
		if free.WhiteLabel && !e.WhiteLabel || free.Resellers && !e.Resellers || free.CryptoWatcher && !e.CryptoWatcher {
			t.Errorf("%s lacks a feature the free tier has: %+v", name, e)
		}
	}
}

func TestParsePresetsIsStrict(t *testing.T) {
	const ok = `{"tiers":{"pro":{"grace_days":14,"entitlements":{"max_servers":-1,"max_active_services":-1,"dashboard_staff_seats":-1}}}}`
	if _, err := ParsePresets(strings.NewReader(ok)); err != nil {
		t.Fatalf("a good file: %v", err)
	}
	for name, raw := range map[string]string{
		"empty":                 `{}`,
		"misspelt field":        `{"tiers":{"pro":{"entitlements":{"max_server":1}}}}`,
		"unknown tier":          `{"tiers":{"gold":{"entitlements":{}}}}`,
		"limit below -1":        `{"tiers":{"pro":{"entitlements":{"max_servers":-2}}}}`,
		"grace too long":        `{"tiers":{"pro":{"grace_days":91,"entitlements":{}}}}`,
		"updates_until in file": `{"tiers":{"pro":{"entitlements":{"updates_until":"2027-01-01"}}}}`,
		"negative days":         `{"tiers":{"trial":{"days":-1,"entitlements":{}}}}`,
		"not json":              `tiers`,
	} {
		if _, err := ParsePresets(strings.NewReader(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPriceIsSet(t *testing.T) {
	for p, want := range map[Price]bool{
		{Currency: "USD", Monthly: "9", Yearly: "90"}:                             true,
		{Currency: "SET ME", Monthly: "9", Yearly: "90"}:                          false,
		{Currency: "USD", Monthly: "set me", Yearly: "90"}:                        false,
		{Currency: "USD", Monthly: "9", Yearly: ""}:                               false,
		{Currency: PricePlaceholder, Monthly: PricePlaceholder, Yearly: "SET ME"}: false,
	} {
		if got := p.IsSet(); got != want {
			t.Errorf("%+v: %v", p, got)
		}
	}
}

func TestAddMonths(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		from string
		n    int
		want string
	}{
		{"2026-10-14T09:30:00Z", 1, "2026-11-14T09:30:00Z"},
		{"2026-10-14T09:30:00Z", 12, "2027-10-14T09:30:00Z"},
		{"2026-01-31T09:30:00Z", 1, "2026-02-28T09:30:00Z"}, // clamped, not March
		{"2028-01-31T09:30:00Z", 1, "2028-02-29T09:30:00Z"}, // leap year
		{"2026-03-31T00:00:00Z", 1, "2026-04-30T00:00:00Z"},
		{"2026-12-15T00:00:00Z", 2, "2027-02-15T00:00:00Z"}, // across the new year
		{"2026-10-14T09:30:00Z", 0, "2026-10-14T09:30:00Z"},
	} {
		if got := AddMonths(at(c.from), c.n); !got.Equal(at(c.want)) {
			t.Errorf("%s + %d months = %s, want %s", c.from, c.n, got.Format(time.RFC3339), c.want)
		}
	}
}
