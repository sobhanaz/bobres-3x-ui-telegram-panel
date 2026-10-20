package domain

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

const licDomain = "panel.example.com"

// vendor signs tokens with a key this core trusts (the test's ring).
type vendor struct {
	t    *testing.T
	priv ed25519.PrivateKey
	now  time.Time
}

// licensed gives svc a key ring with one vendor key, the store's domain and
// a clock at now; it returns the vendor.
func licensed(t *testing.T, svc *Service, now time.Time) *vendor {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clock := now
	svc.cfg.License = LicenseConfig{Keys: license.KeyRing{"k1": pub}, Domain: "Panel.Example.com", Now: func() time.Time { return clock }}
	return &vendor{t: t, priv: priv, now: now}
}

// token signs a license for this store, changed by edit.
func (v *vendor) token(edit func(*license.Token)) string {
	v.t.Helper()
	tok := license.Token{KeyID: "k1", LicenseID: "lic_1", Customer: "Shop", Domain: licDomain, Tier: license.TierStandard,
		IssuedAt: v.now.Add(-90 * 24 * time.Hour), ExpiresAt: v.now.Add(30 * 24 * time.Hour), GraceDays: 7,
		Entitlements: license.Entitlements{MaxServers: license.Unlimited, MaxActiveServices: license.Unlimited,
			DashboardStaffSeat: license.Unlimited, Resellers: true, Gateways: LicenseGateways}}
	if edit != nil {
		edit(&tok)
	}
	raw, err := license.Sign(v.priv, tok)
	if err != nil {
		v.t.Fatal(err)
	}
	return raw
}

// install stores a token directly (as an older version might have).
func install(t *testing.T, s *store.Store, raw string) {
	t.Helper()
	if err := s.SetLicense(context.Background(), s.Conn(), raw, nil, store.LicenseManual); err != nil {
		t.Fatal(err)
	}
}

func TestLicenseStates(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	// No license, and a build with no key at all (the real build carries the
	// vendor's): the install is in its trial.
	svc.cfg.License.Keys = license.KeyRing{}
	st, err := svc.License(ctx)
	if err != nil || st.State != LicenseMissing || st.Valid() || st.InstallID == "" || st.Keys != 0 {
		t.Fatalf("missing: %+v %v", st, err)
	}
	if eff := effectiveOf(t, svc); eff.Basis != license.BasisTrial || !eff.PoweredBy() {
		t.Fatalf("missing, effective: %+v", eff)
	}
	other, err := svc.CheckLicense(ctx, "  eyJ2IjoxfQ.c2ln ")
	if err != nil || other.State != LicenseInvalid || other.Reason != "unknown_key" || other.Token != nil {
		t.Fatalf("no keys: %+v %v", other, err)
	}
	if _, err := svc.CheckLicense(ctx, " "); fieldOf(err) != "token" {
		t.Fatalf("empty token: %v", err)
	}

	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	v := licensed(t, svc, now)
	id := st.InstallID
	for name, c := range map[string]struct {
		edit         func(*license.Token)
		state, cause string
		token        bool
	}{
		"active":           {nil, LicenseActive, "", true},
		"bound to this id": {func(t *license.Token) { t.InstallID = id }, LicenseActive, "", true},
		"grace":            {func(t *license.Token) { t.ExpiresAt = now.Add(-24 * time.Hour) }, LicenseGrace, "", true},
		"expired":          {func(t *license.Token) { t.ExpiresAt = now.Add(-8 * 24 * time.Hour) }, LicenseExpired, "", true},
		"another domain":   {func(t *license.Token) { t.Domain = "shop.example.org" }, LicenseInvalid, "domain", true},
		"another install":  {func(t *license.Token) { t.InstallID = "inst_other" }, LicenseInvalid, "install_id", true},
		"unbound":          {func(t *license.Token) { t.Domain = "" }, LicenseInvalid, "unbound", true},
		"from the future":  {func(t *license.Token) { t.IssuedAt = now.Add(time.Hour) }, LicenseInvalid, "not_yet_valid", true},
		"unsafe contents":  {func(t *license.Token) { t.Tier = "gold" }, LicenseInvalid, "invalid", false},
	} {
		got, err := svc.CheckLicense(ctx, v.token(c.edit))
		if err != nil || got.State != c.state || got.Reason != c.cause || (got.Token != nil) != c.token {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	// A key the store does not trust.
	stranger := &vendor{t: t, now: now}
	_, stranger.priv, _ = ed25519.GenerateKey(rand.Reader)
	if got, _ := svc.CheckLicense(ctx, stranger.token(nil)); got.State != LicenseInvalid || got.Reason != "bad_signature" {
		t.Errorf("stranger: %+v", got)
	}
	// Checking saves nothing.
	if st, _ := svc.License(ctx); st.State != LicenseMissing || st.Keys != 1 || st.Domain != licDomain {
		t.Fatalf("after checks: %+v", st)
	}

	// What is in effect and the footer follow the state.
	install(t, s, v.token(func(t *license.Token) { t.Entitlements.WhiteLabel = true }))
	if st, _ := svc.License(ctx); st.State != LicenseActive || st.UpdatedAt == nil {
		t.Fatalf("white label: %+v", st)
	}
	if eff := effectiveOf(t, svc); eff.Basis != license.BasisLicense || !eff.WhiteLabel() || eff.PoweredBy() {
		t.Fatalf("white label, effective: %+v", eff)
	}
	install(t, s, v.token(nil))
	if st, _ := svc.License(ctx); st.GraceEndsAt != now.Add(37*24*time.Hour) {
		t.Fatalf("grace ends: %+v", st)
	}
	if eff := effectiveOf(t, svc); !eff.PoweredBy() {
		t.Fatalf("powered by: %+v", eff)
	}
	endTrial(t, s, now)
	install(t, s, v.token(func(t *license.Token) {
		t.Entitlements.WhiteLabel = true
		t.ExpiresAt = now.Add(-30 * 24 * time.Hour)
	}))
	if st, _ := svc.License(ctx); st.State != LicenseExpired {
		t.Fatalf("expired: %+v", st)
	}
	if eff := effectiveOf(t, svc); eff.Basis != license.BasisFree || eff.WhiteLabel() || !eff.PoweredBy() || eff.Entitlements.Resellers {
		t.Fatalf("expired grants nothing: %+v", eff)
	}
	install(t, s, "garbage")
	if st, _ := svc.License(ctx); st.State != LicenseInvalid || st.Reason != "malformed" {
		t.Fatalf("garbage: %+v", st)
	}
	if eff := effectiveOf(t, svc); eff.Basis != license.BasisFree || !eff.PoweredBy() {
		t.Fatalf("garbage, effective: %+v", eff)
	}
}

// effectiveOf is what is in effect on svc now.
func effectiveOf(t *testing.T, svc *Service) *EffectiveLicense {
	t.Helper()
	eff, err := svc.Effective(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return eff
}

// setTrialStart sets when the install's implicit trial began.
func setTrialStart(t *testing.T, s *store.Store, at time.Time) {
	t.Helper()
	if _, err := s.DB().Exec(context.Background(), `UPDATE core.install SET trial_started_at = $1`, at); err != nil {
		t.Fatal(err)
	}
}

// endTrial makes the implicit trial over at now (it began 31 days before).
func endTrial(t *testing.T, s *store.Store, now time.Time) {
	t.Helper()
	setTrialStart(t, s, now.Add(-31*24*time.Hour))
}

func TestSetLicense(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	now := time.Now().UTC()
	v := licensed(t, svc, now)
	raw := v.token(nil)

	if _, err := svc.SetLicense(ctx, own, raw, " "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no reason: %v", err)
	}
	var le *LicenseError
	if _, err := svc.SetLicense(ctx, own, v.token(func(t *license.Token) { t.Domain = "x.example" }), "renewal"); !errors.As(err, &le) || le.Reason != "domain" {
		t.Fatalf("another domain: %v", err)
	}
	if _, err := svc.SetLicense(ctx, own, v.token(func(t *license.Token) { t.ExpiresAt = now.Add(-60 * 24 * time.Hour) }), "renewal"); !errors.As(err, &le) || le.Reason != "expired" {
		t.Fatalf("expired: %v", err)
	}
	if st, _ := svc.License(ctx); st.State != LicenseMissing {
		t.Fatalf("a refused token was stored: %+v", st)
	}
	st, err := svc.SetLicense(ctx, own, raw, "bought the standard plan")
	if err != nil || st.State != LicenseActive || st.UpdatedBy == nil || *st.UpdatedBy != own.ID {
		t.Fatalf("set: %+v %v", st, err)
	}
	// A grace token may replace it (from the CLI: no actor).
	cli := store.WithSource(ctx, store.SourceCLI)
	grace := v.token(func(t *license.Token) {
		t.LicenseID = "lic_2"
		t.ExpiresAt = now.Add(-24 * time.Hour)
	})
	if st, err = svc.SetLicense(cli, nil, grace, "imported with the command line"); err != nil || st.State != LicenseGrace || st.UpdatedBy != nil {
		t.Fatalf("replace: %+v %v", st, err)
	}
	entries := auditJSON(t, s, "license.set")
	if len(entries) != 2 {
		t.Fatalf("audit: %v", entries)
	}
	for _, e := range entries {
		if strings.Contains(e, raw) || strings.Contains(e, grace) || strings.Contains(e, strings.Split(raw, ".")[1]) {
			t.Fatalf("the token reached the audit: %s", e)
		}
	}
	if !strings.Contains(entries[1], `"license_id": "lic_2"`) || !strings.Contains(entries[1], `"license_id": "lic_1"`) ||
		!strings.Contains(entries[1], `"kid": "k1"`) {
		t.Fatalf("second entry (before lic_1, after lic_2): %s", entries[1])
	}
	var source, entityID, reason string
	var actorID *string
	if err := s.DB().QueryRow(ctx, `SELECT source, entity_id, reason, actor_id::text FROM core.audit_log
		WHERE action = 'license.set' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&source, &entityID, &reason, &actorID); err != nil ||
		source != "cli" || entityID != "lic_2" || reason != "imported with the command line" || actorID != nil {
		t.Fatalf("CLI entry: %s %s %q %v %v", source, entityID, reason, actorID, err)
	}
}

// What the license limits and how much is used.
func TestLicenseUsage(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	for i, role := range []string{"admin", "support"} {
		u := seedUser(t, s, int64(910001+i))
		if _, err := svc.AddStaff(ctx, own, u.ID, role, "help"); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.SetSetting(ctx, own, "payments.manual_crypto.enabled", "false"); err != nil {
		t.Fatal(err)
	}
	fs := &fakeServers{servers: []Server{{ID: "a"}, {ID: "b"}}}
	fg := &fakeGateways{cfg: GatewayConfig{Provider: Zarinpal, Enabled: true}}
	u, err := svc.LicenseUsage(ctx, fs, fg)
	if err != nil || u.Servers == nil || *u.Servers != 2 || u.StaffSeats != 2 || u.ActiveServices != 0 ||
		strings.Join(u.Gateways, ",") != "manual_card,manual_zarinpal,stars,zarinpal" {
		t.Fatalf("usage: %+v %v", u, err)
	}
	fs.setListErr(errors.New("down"))
	if u, err = svc.LicenseUsage(ctx, fs, nil); err != nil || u.Servers != nil {
		t.Fatalf("provisioner down: %+v %v", u, err)
	}
}

// What is in effect: a valid license, a license in its grace period, the
// implicit trial or the free tier, in that order.
func TestEffectiveEntitlements(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	v := licensed(t, svc, now)
	day := 24 * time.Hour
	paid := license.Entitlements{MaxServers: 3, MaxActiveServices: 500, DashboardStaffSeat: 4, Gateways: []string{"stars"}}
	withPaid := func(edit func(*license.Token)) func() string {
		return func() string {
			return v.token(func(t *license.Token) {
				t.Entitlements = paid
				if edit != nil {
					edit(t)
				}
			})
		}
	}
	free, trial := license.FreeEntitlements(), license.TrialEntitlements()
	// What a license in force gives: its own, joined to the free tier's
	// payment methods (it never leaves less than the free tier).
	inForce := func(e license.Entitlements) license.Entitlements {
		e.MaxJoinChats = max(e.MaxJoinChats, license.FreeMaxJoinChats)
		e.Gateways = append(slices.Clone(e.Gateways), "zarinpal", "idpay", "nextpay", "manual_card", "manual_crypto", "manual_zarinpal")
		return e
	}
	shown := inForce(paid)
	cases := []struct {
		name      string
		token     func() string // nil: no license installed
		started   time.Duration // the trial began this long before now
		basis     license.Basis
		tier      string
		ents      license.Entitlements
		daysLeft  int
		poweredBy bool
	}{
		{"missing, first day of the trial", nil, time.Hour, license.BasisTrial, "trial", trial, 30, true},
		{"missing, a fresh install", nil, 0, license.BasisTrial, "trial", trial, 30, true},
		{"missing, 29 days in", nil, 29 * day, license.BasisTrial, "trial", trial, 1, true},
		{"missing, the last hours of the trial", nil, 30*day - time.Hour, license.BasisTrial, "trial", trial, 1, true},
		{"missing, the trial ended this second", nil, 30 * day, license.BasisFree, "free", free, 0, true},
		{"missing, an old install", nil, 400 * day, license.BasisFree, "free", free, 0, true},
		{"upgraded install, 10 days into its trial", nil, 10 * day, license.BasisTrial, "trial", trial, 20, true},
		{"invalid (another domain), trial running", withPaid(func(t *license.Token) { t.Domain = "other.example" }), day, license.BasisTrial, "trial", trial, 29, true},
		{"invalid (another domain), trial over", withPaid(func(t *license.Token) { t.Domain = "other.example" }), 40 * day, license.BasisFree, "free", free, 0, true},
		{"malformed, trial over", func() string { return "garbage" }, 40 * day, license.BasisFree, "free", free, 0, true},
		{"valid, trial running: the license wins", withPaid(nil), day, license.BasisLicense, "standard", shown, 0, true},
		{"valid, trial over", withPaid(nil), 400 * day, license.BasisLicense, "standard", shown, 0, true},
		{"valid with white label", withPaid(func(t *license.Token) { t.Entitlements.WhiteLabel = true }), 400 * day, license.BasisLicense, "standard",
			inForce(license.Entitlements{WhiteLabel: true, MaxServers: 3, MaxActiveServices: 500, DashboardStaffSeat: 4, Gateways: []string{"stars"}}), 0, false},
		{"a vendor trial license is a license", withPaid(func(t *license.Token) { t.Tier = license.TierTrial }), 400 * day, license.BasisLicense, "trial", shown, 0, true},
		{"grace, trial over", withPaid(func(t *license.Token) { t.ExpiresAt = now.Add(-day) }), 400 * day, license.BasisGrace, "standard", shown, 0, true},
		{"grace, trial running: the license wins", withPaid(func(t *license.Token) { t.ExpiresAt = now.Add(-day) }), day, license.BasisGrace, "standard", shown, 0, true},
		{"a license that grants nothing is the free tier", withPaid(func(t *license.Token) { t.Entitlements = license.Entitlements{} }), 400 * day,
			license.BasisLicense, "standard", free, 0, true},
		{"expired, trial over", withPaid(func(t *license.Token) { t.ExpiresAt = now.Add(-30 * day); t.Entitlements.WhiteLabel = true }), 400 * day, license.BasisFree, "free", free, 0, true},
		{"expired, trial running", withPaid(func(t *license.Token) { t.ExpiresAt = now.Add(-30 * day) }), 10 * day, license.BasisTrial, "trial", trial, 20, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.token == nil {
				if _, err := s.DB().Exec(ctx, `DELETE FROM core.license`); err != nil {
					t.Fatal(err)
				}
			} else {
				install(t, s, c.token())
			}
			setTrialStart(t, s, now.Add(-c.started))
			eff := effectiveOf(t, svc)
			if eff.Basis != c.basis || eff.Tier != c.tier || !reflect.DeepEqual(eff.Entitlements, c.ents) ||
				eff.TrialDaysLeft() != c.daysLeft || eff.PoweredBy() != c.poweredBy {
				t.Fatalf("got %s/%s days %d poweredBy %v\n%+v\nwant %s/%s days %d poweredBy %v\n%+v", eff.Basis, eff.Tier, eff.TrialDaysLeft(), eff.PoweredBy(),
					eff.Entitlements, c.basis, c.tier, c.daysLeft, c.poweredBy, c.ents)
			}
		})
	}
}

// The free tier and the trial are what the owner decided; the list of
// payment methods a license names is the one core knows.
func TestPolicyConstants(t *testing.T) {
	free := license.FreeEntitlements()
	if free.MaxServers != 1 || free.MaxActiveServices != 25 || free.DashboardStaffSeat != 2 || free.Resellers || free.WhiteLabel ||
		free.CryptoWatcher || free.UpdatesUntil != "" ||
		strings.Join(free.Gateways, ",") != "zarinpal,idpay,nextpay,stars,manual_card,manual_crypto,manual_zarinpal" {
		t.Errorf("free tier: %+v", free)
	}
	trial := license.TrialEntitlements()
	if license.TrialDays != 30 || trial.MaxServers != license.Unlimited || trial.MaxActiveServices != license.Unlimited || !trial.Resellers ||
		trial.WhiteLabel || !trial.CryptoWatcher || trial.DashboardStaffSeat != 5 || trial.UpdatesUntil != "" {
		t.Errorf("trial: %+v", trial)
	}
	if !slices.Equal(LicenseGateways, license.AllGateways()) || !slices.Equal(trial.Gateways, LicenseGateways) {
		t.Errorf("gateways: %v, license has %v", LicenseGateways, license.AllGateways())
	}
	for _, g := range free.Gateways {
		if !slices.Contains(LicenseGateways, g) {
			t.Errorf("the free tier names %q, which no payment method is called", g)
		}
	}
}
