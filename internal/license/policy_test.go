package license

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

var policyNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// The free tier and the trial are what the owner decided on 2026-10-14.
func TestFreeAndTrialEntitlements(t *testing.T) {
	want := Entitlements{MaxServers: 1, MaxActiveServices: 25, DashboardStaffSeat: 2,
		Gateways: []string{"zarinpal", "idpay", "nextpay", "stars", "manual_card", "manual_crypto", "manual_zarinpal"}}
	if got := FreeEntitlements(); !reflect.DeepEqual(got, want) {
		t.Errorf("free tier:\n got %+v\nwant %+v", got, want)
	}
	wantTrial := Entitlements{MaxServers: Unlimited, MaxActiveServices: Unlimited, Resellers: true, CryptoWatcher: true,
		DashboardStaffSeat: 5, Gateways: []string{"zarinpal", "idpay", "nextpay", "stars", "usdt_trc20_auto", "manual_card", "manual_crypto", "manual_zarinpal"}}
	if got := TrialEntitlements(); !reflect.DeepEqual(got, wantTrial) {
		t.Errorf("trial:\n got %+v\nwant %+v", got, wantTrial)
	}
	if TrialDays != 30 {
		t.Errorf("the trial is %d days", TrialDays)
	}
	// Security updates are never withheld; the wallet is not a gateway to name;
	// white label is never part of either.
	for name, e := range map[string]Entitlements{"free": FreeEntitlements(), "trial": TrialEntitlements()} {
		if e.UpdatesUntil != "" || e.WhiteLabel || slices.Contains(e.Gateways, "wallet") {
			t.Errorf("%s: %+v", name, e)
		}
	}
	// Every call gives its own gateway list: changing one changes nothing else.
	a := FreeEntitlements()
	a.Gateways[0] = "x"
	if FreeEntitlements().Gateways[0] != "zarinpal" {
		t.Error("the free tier's gateways are shared")
	}
}

// Resolve tries a license, then its grace period, then the trial, then the
// free tier.
func TestResolve(t *testing.T) {
	day := 24 * time.Hour
	lic := &Token{Tier: TierPro, Entitlements: Entitlements{MaxServers: 7, WhiteLabel: true}}
	for _, c := range []struct {
		name    string
		lic     *Token
		state   State
		started time.Duration // the trial began this long ago (-1: unknown)
		basis   Basis
		tier    string
		servers int
	}{
		{"a license wins over a running trial", lic, Active, day, BasisLicense, "pro", 7},
		{"a license, the trial over", lic, Active, 90 * day, BasisLicense, "pro", 7},
		{"grace wins over a running trial", lic, Grace, day, BasisGrace, "pro", 7},
		{"grace, the trial over", lic, Grace, 90 * day, BasisGrace, "pro", 7},
		{"past grace, a running trial", lic, Expired, day, BasisTrial, "trial", Unlimited},
		{"past grace, the trial over", lic, Expired, 90 * day, BasisFree, "free", 1},
		{"no license, a running trial", nil, Expired, day, BasisTrial, "trial", Unlimited},
		{"no license, 29 days and 23 hours in", nil, Expired, 30*day - time.Hour, BasisTrial, "trial", Unlimited},
		{"no license, exactly 30 days in", nil, Expired, 30 * day, BasisFree, "free", 1},
		{"no license, an old install", nil, Expired, 400 * day, BasisFree, "free", 1},
		{"no license, the trial start unknown", nil, Expired, -1, BasisFree, "free", 1},
		{"a license that did not verify counts for nothing", nil, Active, 400 * day, BasisFree, "free", 1},
	} {
		var start time.Time
		if c.started >= 0 {
			start = policyNow.Add(-c.started)
		}
		got := Resolve(c.lic, c.state, start, policyNow)
		if got.Basis != c.basis || got.Tier != c.tier || got.Entitlements.MaxServers != c.servers {
			t.Errorf("%s: %s/%s with %d servers, want %s/%s with %d", c.name, got.Basis, got.Tier, got.Entitlements.MaxServers, c.basis, c.tier, c.servers)
		}
	}

	// White label comes only from a license; a license without it shows the mark.
	if e := Resolve(lic, Active, policyNow, policyNow); !e.WhiteLabel() || e.PoweredBy() {
		t.Errorf("white label license: %+v", e)
	}
	if e := Resolve(lic, Expired, policyNow.Add(-90*24*time.Hour), policyNow); e.WhiteLabel() || !e.PoweredBy() {
		t.Errorf("an ended white label license: %+v", e)
	}
	if e := Resolve(nil, Expired, policyNow, policyNow); e.WhiteLabel() || !e.PoweredBy() {
		t.Errorf("the trial hides the mark: %+v", e)
	}
	// Changing what Resolve returns changes nothing it returns later.
	e := Resolve(nil, Expired, policyNow, policyNow)
	e.Entitlements.Gateways[0] = "x"
	if Resolve(nil, Expired, policyNow, policyNow).Entitlements.Gateways[0] != "zarinpal" {
		t.Error("the trial's gateways are shared")
	}
}

func TestTrialDaysLeft(t *testing.T) {
	for _, c := range []struct {
		name    string
		started time.Duration
		left    int
	}{
		{"just started", 0, 30},
		{"a minute in", time.Minute, 30},
		{"a day in", 24 * time.Hour, 29},
		{"a day and an hour in", 25 * time.Hour, 29},
		{"the last day", 29*24*time.Hour + time.Hour, 1},
		{"the last minute", 30*24*time.Hour - time.Minute, 1},
		{"ended", 30 * 24 * time.Hour, 0},
		{"long ended", 100 * 24 * time.Hour, 0},
	} {
		e := Resolve(nil, Expired, policyNow.Add(-c.started), policyNow)
		if got := e.TrialDaysLeft(policyNow); got != c.left {
			t.Errorf("%s: %d days left, want %d", c.name, got, c.left)
		}
	}
	if (Effective{}).TrialDaysLeft(policyNow) != 0 {
		t.Error("an unknown trial has days left")
	}
	if got := TrialEnds(policyNow); !got.Equal(policyNow.Add(30 * 24 * time.Hour)) {
		t.Errorf("TrialEnds: %s", got)
	}
}

// A license never leaves less than the free tier, whatever it grants or
// omits: each limit is the larger of the two (-1 is infinite), the gateway
// lists are united and a flag is on when either has it. It can still be
// lower than the trial.
func TestResolveNeverBelowFree(t *testing.T) {
	free := FreeEntitlements()
	started := policyNow.Add(-90 * 24 * time.Hour)
	for _, c := range []struct {
		name string
		ents Entitlements
		want Entitlements
	}{
		{"an all-zero license", Entitlements{}, free},
		{"limits omitted, a feature granted", Entitlements{Resellers: true, MaxServers: 2, DashboardStaffSeat: 3, Gateways: []string{"zarinpal"}},
			Entitlements{Resellers: true, MaxServers: 2, MaxActiveServices: 25, DashboardStaffSeat: 3, Gateways: free.Gateways}},
		{"unlimited beats the free tier's number", Entitlements{MaxServers: Unlimited, MaxActiveServices: Unlimited, DashboardStaffSeat: Unlimited},
			Entitlements{MaxServers: Unlimited, MaxActiveServices: Unlimited, DashboardStaffSeat: Unlimited, Gateways: free.Gateways}},
		{"a limit below the free tier's is raised", Entitlements{MaxServers: 1, MaxActiveServices: 10, DashboardStaffSeat: 1},
			Entitlements{MaxServers: 1, MaxActiveServices: 25, DashboardStaffSeat: 2, Gateways: free.Gateways}},
		{"gateways are united, a name once, case aside", Entitlements{Gateways: []string{"Stars", "usdt_trc20_auto"}, CryptoWatcher: true},
			Entitlements{MaxServers: 1, MaxActiveServices: 25, DashboardStaffSeat: 2, CryptoWatcher: true,
				Gateways: []string{"Stars", "usdt_trc20_auto", "zarinpal", "idpay", "nextpay", "manual_card", "manual_crypto", "manual_zarinpal"}}},
		{"white label and updates_until are the license's", Entitlements{WhiteLabel: true, UpdatesUntil: "2027-09-30"},
			Entitlements{WhiteLabel: true, MaxServers: 1, MaxActiveServices: 25, DashboardStaffSeat: 2, Gateways: free.Gateways, UpdatesUntil: "2027-09-30"}},
	} {
		for state, basis := range map[State]Basis{Active: BasisLicense, Grace: BasisGrace} {
			got := Resolve(&Token{Tier: TierStandard, Entitlements: c.ents}, state, started, policyNow)
			if got.Basis != basis || !reflect.DeepEqual(got.Entitlements, c.want) {
				t.Errorf("%s (%s):\n got %s %+v\nwant %s %+v", c.name, state, got.Basis, got.Entitlements, basis, c.want)
			}
		}
	}
	// Still lower than the trial where the license says so.
	got := Resolve(&Token{Tier: TierStandard, Entitlements: Entitlements{MaxServers: 2, DashboardStaffSeat: 3, Gateways: []string{"stars"}}},
		Active, policyNow.Add(-24*time.Hour), policyNow)
	if got.Entitlements.MaxServers != 2 || got.Entitlements.MaxActiveServices != 25 || got.Entitlements.Resellers || got.Entitlements.CryptoWatcher {
		t.Errorf("a license under a running trial: %+v", got.Entitlements)
	}
	// AtLeast changes neither side.
	lic, floor := Entitlements{Gateways: []string{"stars"}}, FreeEntitlements()
	lic.AtLeast(floor)
	if !reflect.DeepEqual(lic.Gateways, []string{"stars"}) || !reflect.DeepEqual(floor, FreeEntitlements()) {
		t.Errorf("AtLeast changed its inputs: %+v %+v", lic, floor)
	}
}

// A trial that "began in the future" (a clock set back, a bad restore)
// began now: it never has more than TrialDays left. (Resolve is stateless:
// core writes the clamped start back, so the trial then really ends.)
func TestResolveClampsAFutureTrialStart(t *testing.T) {
	for _, ahead := range []time.Duration{time.Minute, 24 * time.Hour, 400 * 24 * time.Hour} {
		e := Resolve(nil, Expired, policyNow.Add(ahead), policyNow)
		if e.Basis != BasisTrial || !e.TrialEndsAt.Equal(policyNow.Add(TrialDays*24*time.Hour)) || e.TrialDaysLeft(policyNow) != TrialDays {
			t.Errorf("a start %s ahead: %s ends %s, %d days left", ahead, e.Basis, e.TrialEndsAt, e.TrialDaysLeft(policyNow))
		}
	}
}
