package domain

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
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
	if err := s.SetLicense(context.Background(), s.Conn(), raw, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLicenseStates(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	// No license, and no key built in: everything works, nothing says
	// "Powered by".
	st, err := svc.License(ctx)
	if err != nil || st.State != LicenseMissing || st.Valid() || st.PoweredBy() || st.InstallID == "" || st.Keys != 0 {
		t.Fatalf("missing: %+v %v", st, err)
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

	// Entitlements and the footer follow the state.
	install(t, s, v.token(func(t *license.Token) { t.Entitlements.WhiteLabel = true }))
	if st, _ := svc.License(ctx); st.State != LicenseActive || !st.WhiteLabel() || st.PoweredBy() || st.UpdatedAt == nil {
		t.Fatalf("white label: %+v", st)
	}
	install(t, s, v.token(nil))
	if st, _ := svc.License(ctx); !st.PoweredBy() || st.GraceEndsAt != now.Add(37*24*time.Hour) {
		t.Fatalf("powered by: %+v", st)
	}
	install(t, s, v.token(func(t *license.Token) {
		t.Entitlements.WhiteLabel = true
		t.ExpiresAt = now.Add(-30 * 24 * time.Hour)
	}))
	if st, _ := svc.License(ctx); st.State != LicenseExpired || st.WhiteLabel() || !st.PoweredBy() || st.Entitlements().Resellers {
		t.Fatalf("expired grants nothing: %+v", st)
	}
	install(t, s, "garbage")
	if st, _ := svc.License(ctx); st.State != LicenseInvalid || st.Reason != "malformed" || st.PoweredBy() {
		t.Fatalf("garbage: %+v", st)
	}
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

// Without a license (or with one that does not verify) nothing is gated;
// with one, new actions beyond it are refused and what exists stays.
func TestLicenseGates(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	own := owner(t, svc)
	tier := seedTier(t, svc, own, "Gold", 10, 5, nil)
	now := time.Now().UTC()
	fs := &fakeServers{}
	fg := &fakeGateways{cfg: GatewayConfig{Provider: Zarinpal}}
	addServer := func(name string) error {
		_, err := svc.AddServer(ctx, own, fs, ServerInput{Name: name, BaseURL: "https://" + name + ".example.com", Token: "t-" + name, Enabled: true})
		return err
	}
	tg := int64(920000)
	addStaff := func(role string) (*store.User, error) {
		tg++
		u := seedUser(t, s, tg)
		return svc.AddStaff(ctx, own, u.ID, role, "help")
	}
	makeReseller := func() error {
		tg++
		u := seedUser(t, s, tg)
		_, err := svc.MakeReseller(ctx, own, u.ID, ResellerInput{TierID: tier.ID}, "a shop")
		return err
	}
	gate := func(err error) string {
		var lr *LicenseRefusal
		if errors.As(err, &lr) && errors.Is(err, ErrState) {
			return lr.Gate
		}
		return ""
	}

	// Unlicensed, and an invalid token: no gate at all.
	v := licensed(t, svc, now)
	for _, raw := range []string{"", v.token(func(t *license.Token) { t.Domain = "other.example" })} {
		if raw != "" {
			install(t, s, raw)
		}
		if err := makeReseller(); err != nil {
			t.Fatalf("reseller (%q): %v", raw, err)
		}
	}

	// A license with one server, one seat, no resellers, Stars only.
	install(t, s, v.token(func(t *license.Token) {
		t.Entitlements = license.Entitlements{MaxServers: 1, MaxActiveServices: license.Unlimited, DashboardStaffSeat: 1,
			Gateways: []string{"Stars"}}
	}))
	if err := makeReseller(); gate(err) != "resellers" || !strings.Contains(err.Error(), "does not include resellers") {
		t.Fatalf("reseller: %v", err)
	}
	if err := addServer("de"); err != nil {
		t.Fatalf("first server: %v", err)
	}
	if err := addServer("nl"); gate(err) != "max_servers" || !strings.Contains(err.Error(), "allows 1 servers") || len(fs.servers) != 1 {
		t.Fatalf("second server: %v", err)
	}
	admin, err := addStaff("admin")
	if err != nil {
		t.Fatalf("first seat: %v", err)
	}
	if _, err := addStaff("support"); gate(err) != "dashboard_staff_seats" {
		t.Fatalf("second seat: %v", err)
	}
	second, err := addStaff("owner")
	if err != nil {
		t.Fatalf("owners take no seat: %v", err)
	}
	if _, err := svc.SetStaffRole(ctx, own, second.ID, "support", "less"); gate(err) != "dashboard_staff_seats" {
		t.Fatalf("an owner stepping down takes a seat: %v", err)
	}
	if _, err := svc.SetStaffRole(ctx, own, admin.ID, "support", "less"); err != nil {
		t.Fatalf("a seat changing role: %v", err)
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: Zarinpal, Enabled: true}); gate(err) != "gateways" || fg.sets != 0 {
		t.Fatalf("zarinpal on: %v", err)
	}
	if _, err := svc.SetGatewayConfig(ctx, own, fg, GatewayUpdate{Provider: Zarinpal, Sandbox: true}); err != nil {
		t.Fatalf("zarinpal staying off: %v", err)
	}
	// Methods on by default stay on; switching one off is always allowed,
	// back on only when licensed.
	if err := svc.SetSetting(ctx, own, "payments.manual_card.enabled", "false"); err != nil {
		t.Fatalf("card off: %v", err)
	}
	if err := svc.SetSetting(ctx, own, "payments.manual_card.enabled", "true"); gate(err) != "gateways" {
		t.Fatalf("card on: %v", err)
	}
	if _, err := svc.SetSettings(ctx, own, map[string]string{"payments.stars.enabled": "false", "payments.card_number": ""}); err != nil {
		t.Fatalf("stars off: %v", err)
	}
	if err := svc.SetSetting(ctx, own, "payments.stars.enabled", ""); err != nil {
		t.Fatalf("stars back on (licensed): %v", err)
	}

	// Expired: every gate refuses, saying so, whatever it granted.
	install(t, s, v.token(func(t *license.Token) { t.ExpiresAt = now.Add(-30 * 24 * time.Hour) }))
	for name, err := range map[string]error{
		"reseller": makeReseller(),
		"server":   addServer("fr"),
		"stars":    svc.SetSetting(ctx, own, "payments.manual_card.enabled", "true"),
	} {
		if gate(err) == "" || !strings.Contains(err.Error(), "the license has expired") {
			t.Errorf("expired %s: %v", name, err)
		}
	}
	// Grace keeps the entitlements.
	install(t, s, v.token(func(t *license.Token) { t.ExpiresAt = now.Add(-24 * time.Hour) }))
	if err := makeReseller(); err != nil {
		t.Fatalf("grace: %v", err)
	}
	// Unlimited servers allow any number.
	install(t, s, v.token(nil))
	if err := addServer("fr"); err != nil {
		t.Fatalf("unlimited: %v", err)
	}
}
