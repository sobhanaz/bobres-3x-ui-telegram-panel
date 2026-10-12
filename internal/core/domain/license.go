package domain

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// The license (milestone 6b). Policy for Phase 4: a missing or invalid
// license never stops anything; the dashboard says "Unlicensed". With a
// verified license, its entitlements gate only NEW actions (making a
// reseller, adding a server, staff or a payment method beyond what it
// grants); what exists keeps working. Grace keeps the entitlements with a
// warning; an expired license grants none of them.

// License states.
const (
	LicenseMissing = "missing"
	LicenseInvalid = "invalid"
	LicenseActive  = "active"
	LicenseGrace   = "grace"
	LicenseExpired = "expired"
)

// LicenseGateways are the payment methods a license names. The wallet is
// always allowed. Automatic USDT also needs the crypto_watcher feature.
var LicenseGateways = []string{Zarinpal, Stars, USDTAuto, "manual_card", "manual_crypto", "manual_zarinpal"}

// LicenseConfig is what a license is checked against.
type LicenseConfig struct {
	// Keys are the vendor's public keys; nil means the ones built into core
	// (license.Embedded).
	Keys license.KeyRing
	// Domain is this install's BOBRES_DOMAIN.
	Domain string
	// Now is the clock (nil: time.Now).
	Now func() time.Time
}

// LicenseStatus is a license token as this install sees it.
type LicenseStatus struct {
	State string
	// Reason says why a token is invalid (license.Reason codes) or, on
	// SetLicense, "expired".
	Reason string
	// Token is the payload when its signature verified, also when it is bound
	// to another install (to show whom it was made for).
	Token *license.Token
	// GraceEndsAt is when a verified token stops working.
	GraceEndsAt time.Time
	// This install's id and domain, as the token is checked against.
	InstallID, Domain string
	// Keys counts the public keys built into this core.
	Keys int
	// Who installed the stored token, and when (nil for a candidate).
	UpdatedAt     *time.Time
	UpdatedBy     *string
	UpdatedByName string
	// Source is how the stored token came: manual, activation or refresh
	// ("" for a candidate or when none is installed). RefreshedAt is when
	// the daily refresh installed it (nil unless Source is refresh).
	Source      string
	RefreshedAt *time.Time
}

// Valid: the token verified for this install, so its entitlements gate new
// actions (expired included: it then grants none).
func (l *LicenseStatus) Valid() bool {
	return l.State == LicenseActive || l.State == LicenseGrace || l.State == LicenseExpired
}

// Entitlements are what the license grants now: none once it has expired.
func (l *LicenseStatus) Entitlements() license.Entitlements {
	if l.Token == nil || (l.State != LicenseActive && l.State != LicenseGrace) {
		return license.Entitlements{}
	}
	return l.Token.Entitlements
}

// WhiteLabel: the store may hide BOBRES's name.
func (l *LicenseStatus) WhiteLabel() bool { return l.Entitlements().WhiteLabel }

// PoweredBy: show "Powered by BOBRES". Only a verified license without
// white_label asks for it; an install without a license (the owner's own)
// never shows it.
func (l *LicenseStatus) PoweredBy() bool { return l.Valid() && !l.WhiteLabel() }

// LicenseError refuses a token that cannot be installed; Reason is a
// license.Reason code or "expired".
type LicenseError struct{ Reason string }

func (e *LicenseError) Error() string { return "license: " + LicenseReasonText(e.Reason) }

// LicenseReasonText explains a reason code in a few words (English; the
// dashboard has its own words per code).
func LicenseReasonText(code string) string {
	switch code {
	case "unknown_key":
		return "this build has no public key that could verify a license yet"
	case "malformed":
		return "this is not a license token"
	case "bad_signature":
		return "the signature does not match a BOBRES key"
	case "version":
		return "this license format is not supported by this version"
	case "domain":
		return "the license is for another domain"
	case "install_id":
		return "the license is for another install"
	case "unbound":
		return "the license is not bound to a domain"
	case "not_yet_valid":
		return "the license starts in the future (check the server clock)"
	case "expired":
		return "the license has expired"
	}
	return "the license is not valid"
}

// LicenseRefusal is a new action the installed license does not allow (409).
// Gate names the entitlement: resellers, max_servers, dashboard_staff_seats,
// gateways or crypto_watcher.
type LicenseRefusal struct{ Gate, Msg string }

func (e *LicenseRefusal) Error() string { return ErrState.Error() + ": " + e.Msg }
func (e *LicenseRefusal) Unwrap() error { return ErrState }

func (s *Service) licenseNow() time.Time {
	if s.cfg.License.Now != nil {
		return s.cfg.License.Now()
	}
	return time.Now()
}

// licenseKeys is the key ring tokens are verified with.
func (s *Service) licenseKeys() license.KeyRing {
	if s.cfg.License.Keys != nil {
		return s.cfg.License.Keys
	}
	s.keysOnce.Do(func() {
		ring, err := license.Embedded()
		if err != nil {
			s.cfg.Log.Error("license keys built into core are unreadable", "err", err)
		}
		s.embedded = ring
	})
	return s.embedded
}

// evaluate checks raw against this install.
func (s *Service) evaluate(raw, installID string) *LicenseStatus {
	keys := s.licenseKeys()
	st := &LicenseStatus{InstallID: installID, Domain: license.NormalizeDomain(s.cfg.License.Domain), Keys: len(keys)}
	t, err := license.Parse(keys, raw)
	if err != nil {
		st.State, st.Reason = LicenseInvalid, license.Reason(err)
		return st
	}
	st.Token = &t
	st.GraceEndsAt = t.ExpiresAt.Add(time.Duration(t.GraceDays) * 24 * time.Hour)
	// A license is always bound to the store's domain; the install id when
	// the token names one.
	if err := t.CheckBinding(license.Options{Domain: st.Domain, InstallID: installID, RequireBound: true}); err != nil {
		st.State, st.Reason = LicenseInvalid, license.Reason(err)
		return st
	}
	state, err := t.State(s.licenseNow())
	if err != nil {
		st.State, st.Reason = LicenseInvalid, license.Reason(err)
		return st
	}
	st.State = state.String()
	return st
}

// License is the installed license as this install sees it now (state
// missing when none is installed).
func (s *Service) License(ctx context.Context) (*LicenseStatus, error) {
	id, err := s.st.InstallID(ctx, s.st.Conn())
	if err != nil {
		return nil, err
	}
	row, err := s.st.GetLicense(ctx, s.st.Conn())
	if errors.Is(err, store.ErrNotFound) {
		keys := s.licenseKeys()
		return &LicenseStatus{State: LicenseMissing, InstallID: id, Domain: license.NormalizeDomain(s.cfg.License.Domain),
			Keys: len(keys)}, nil
	}
	if err != nil {
		return nil, err
	}
	st := s.evaluate(row.Token, id)
	st.UpdatedAt, st.UpdatedBy, st.UpdatedByName = &row.UpdatedAt, row.UpdatedBy, row.UpdatedByName
	st.Source, st.RefreshedAt = row.Source, row.RefreshedAt
	return st, nil
}

// CheckLicense checks a token against this install without saving it.
func (s *Service) CheckLicense(ctx context.Context, raw string) (*LicenseStatus, error) {
	raw, err := licenseToken(raw)
	if err != nil {
		return nil, err
	}
	id, err := s.st.InstallID(ctx, s.st.Conn())
	if err != nil {
		return nil, err
	}
	return s.evaluate(raw, id), nil
}

// maxLicenseToken bounds a pasted token (a real one is well under 2 KiB).
const maxLicenseToken = 16384

func licenseToken(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return "", &FieldError{"token", "paste the license token"}
	case len(raw) > maxLicenseToken:
		return "", &FieldError{"token", "this is too long for a license token"}
	}
	return raw, nil
}

// licenseAudit is what an audit entry keeps of a license: its ids, never the
// token.
func licenseAudit(t *license.Token) map[string]any {
	if t == nil {
		return nil
	}
	return map[string]any{"license_id": t.LicenseID, "kid": t.KeyID, "tier": t.Tier,
		"expires_at": t.ExpiresAt.UTC().Format(time.RFC3339)}
}

// SetLicense installs a token that verifies for this install and has not
// expired, replacing any other (actor nil: the CLI). It is audited as
// license.set with the license's ids.
func (s *Service) SetLicense(ctx context.Context, actor *store.User, raw, reason string) (*LicenseStatus, error) {
	return s.installLicense(ctx, actor, raw, reason, store.LicenseManual)
}

// SetActivatedLicense is SetLicense for a token the license server answered
// to an activation (`bobres license activate`): the daily refresh renews it.
func (s *Service) SetActivatedLicense(ctx context.Context, actor *store.User, raw, reason string) (*LicenseStatus, error) {
	return s.installLicense(ctx, actor, raw, reason, store.LicenseActivation)
}

// installLicense installs raw as SetLicense says, noting how it came
// (source: store.LicenseManual or store.LicenseActivation).
func (s *Service) installLicense(ctx context.Context, actor *store.User, raw, reason, source string) (*LicenseStatus, error) {
	reason, err := needReasonText(reason)
	if err != nil {
		return nil, err
	}
	raw, err = licenseToken(raw)
	if err != nil {
		return nil, err
	}
	id, err := s.st.InstallID(ctx, s.st.Conn())
	if err != nil {
		return nil, err
	}
	st := s.evaluate(raw, id)
	switch st.State {
	case LicenseInvalid:
		return nil, &LicenseError{Reason: st.Reason}
	case LicenseExpired:
		return nil, &LicenseError{Reason: "expired"}
	}
	var by *string
	if actor != nil {
		by = &actor.ID
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var before map[string]any
		if old, err := s.st.GetLicense(ctx, tx); err == nil {
			if t, err := license.Parse(s.licenseKeys(), old.Token); err == nil {
				before = licenseAudit(&t)
			} else {
				before = map[string]any{"license_id": "unreadable"}
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := s.st.SetLicense(ctx, tx, raw, by, source); err != nil {
			return err
		}
		after := licenseAudit(st.Token)
		after["source"] = source
		return s.auditChange(ctx, tx, actor, "license.set", "license", st.Token.LicenseID, before, after, reason)
	})
	if err != nil {
		return nil, err
	}
	return s.License(ctx)
}

// LicenseUsage is how much of each limit this install uses (nil: unknown,
// its service did not answer).
type LicenseUsage struct {
	Servers        *int
	ActiveServices int
	StaffSeats     int
	// Gateways are the payment methods switched on now (wallet left out).
	Gateways []string
}

// LicenseUsage counts what the license's limits apply to. sa and ga may be
// nil (their counts are then unknown or left out).
func (s *Service) LicenseUsage(ctx context.Context, sa ServerAdmin, ga GatewayAdmin) (*LicenseUsage, error) {
	u := &LicenseUsage{Gateways: []string{}}
	var err error
	if u.ActiveServices, err = s.st.CountActiveServices(ctx, s.st.Conn()); err != nil {
		return nil, err
	}
	if u.StaffSeats, err = s.st.CountStaffSeats(ctx, s.st.Conn()); err != nil {
		return nil, err
	}
	if sa != nil {
		if list, err := sa.ListServers(ctx); err == nil {
			n := len(list.Servers)
			u.Servers = &n
		} else {
			s.cfg.Log.Warn("license usage: servers", "err", err)
		}
	}
	if ga != nil {
		if configs, err := ga.GatewayConfigs(ctx); err == nil {
			for _, c := range configs {
				if c.Enabled {
					u.Gateways = append(u.Gateways, c.Provider)
				}
			}
		} else {
			s.cfg.Log.Warn("license usage: gateways", "err", err)
		}
	}
	for _, p := range LicenseGateways {
		if methodToggles[p] != "" && s.methodEnabled(ctx, p) {
			u.Gateways = append(u.Gateways, p)
		}
	}
	slices.Sort(u.Gateways)
	return u, nil
}

// gatingLicense is the installed license when it gates new actions; nil
// when there is none or it does not verify (never gates), or when it cannot
// be read (logged: the action itself goes on).
func (s *Service) gatingLicense(ctx context.Context) *LicenseStatus {
	st, err := s.License(ctx)
	if err != nil {
		s.cfg.Log.Warn("license: read for a gate", "err", err)
		return nil
	}
	if !st.Valid() {
		return nil
	}
	return st
}

// refusal words a gate's refusal: the license expired, or what it lacks.
func refusal(st *LicenseStatus, gate, lacks string) error {
	if st.State == LicenseExpired {
		return &LicenseRefusal{Gate: gate, Msg: "the license has expired: renew it to " + gateAction[gate]}
	}
	return &LicenseRefusal{Gate: gate, Msg: lacks}
}

var gateAction = map[string]string{
	"resellers":             "add resellers",
	"max_servers":           "add servers",
	"dashboard_staff_seats": "add staff",
	"gateways":              "switch payment methods on",
	"crypto_watcher":        "switch automatic USDT on",
}

// requireResellers: making a reseller needs the resellers feature.
func (s *Service) requireResellers(ctx context.Context) error {
	st := s.gatingLicense(ctx)
	if st == nil || st.Entitlements().Resellers {
		return nil
	}
	return refusal(st, "resellers", "the license does not include resellers")
}

// requireServerSlot: one more server must fit max_servers.
func (s *Service) requireServerSlot(ctx context.Context, sa ServerAdmin) error {
	st := s.gatingLicense(ctx)
	if st == nil {
		return nil
	}
	limit := st.Entitlements().MaxServers
	if limit == license.Unlimited {
		return nil
	}
	n := 0
	if limit > 0 {
		list, err := sa.ListServers(ctx)
		if err != nil {
			return err
		}
		n = len(list.Servers)
	}
	if license.WithinLimit(limit, n) {
		return nil
	}
	return refusal(st, "max_servers", fmt.Sprintf("the license allows %d servers", max(limit, 0)))
}

// requireStaffSeat: one more admin or support member must fit the
// dashboard seats (owners take none; nobody is removed over a limit).
func (s *Service) requireStaffSeat(ctx context.Context) error {
	st := s.gatingLicense(ctx)
	if st == nil {
		return nil
	}
	limit := st.Entitlements().DashboardStaffSeat
	if limit == license.Unlimited {
		return nil
	}
	n, err := s.st.CountStaffSeats(ctx, s.st.Conn())
	if err != nil {
		return err
	}
	if license.WithinLimit(limit, n) {
		return nil
	}
	return refusal(st, "dashboard_staff_seats", fmt.Sprintf("the license allows %d staff seats (owners take none)", max(limit, 0)))
}

// requireGateway: switching a payment method on needs it in the license
// (the wallet is always allowed); automatic USDT also needs the
// crypto_watcher feature.
func (s *Service) requireGateway(ctx context.Context, provider string) error {
	if provider == "wallet" {
		return nil
	}
	return gatewayRefusal(s.gatingLicense(ctx), provider)
}

// gatewayRefusal: st (nil: no gating license) does not include provider.
func gatewayRefusal(st *LicenseStatus, provider string) error {
	if st == nil {
		return nil
	}
	if provider == USDTAuto && !st.Entitlements().HasFeature("crypto_watcher") {
		return refusal(st, "crypto_watcher", "the license does not include the automatic USDT watcher (crypto_watcher)")
	}
	if st.Entitlements().HasGateway(provider) {
		return nil
	}
	return refusal(st, "gateways", "the license does not include the payment method "+provider)
}

// toggledOn lists the payment methods a settings change switches on (from
// "false" to on; unset already counts as on).
func toggledOn(before, after map[string]string) []string {
	var out []string
	for _, p := range LicenseGateways {
		key := methodToggles[p]
		if key == "" {
			continue
		}
		if v, ok := after[key]; ok && before[key] == "false" && v != "false" {
			out = append(out, p)
		}
	}
	return out
}

// touchesToggle: a settings change sets a payment method's switch.
func touchesToggle(values map[string]string) bool {
	for _, key := range methodToggles {
		if _, ok := values[key]; ok {
			return true
		}
	}
	return false
}
