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

// The license (milestone 6b) and the policy "free core, paid premium"
// (decided 2026-10-14). An install without a valid license is not blocked:
// it runs on the free tier (license.FreeEntitlements), and a 30-day implicit
// trial with pro-like entitlements protects every install when it first runs
// a version with this policy. A license unlocks more. Effective says which
// of the four applies (a valid license, a license in its grace period, the
// trial, the free tier); the gates below read only that, and only refuse NEW
// actions (a new server, service, reseller, staff member or payment method
// beyond it). What exists keeps working and renewing.

// License states.
const (
	LicenseMissing = "missing"
	LicenseInvalid = "invalid"
	LicenseActive  = "active"
	LicenseGrace   = "grace"
	LicenseExpired = "expired"
)

// LicenseGateways are the payment methods a license names (license.AllGateways:
// a test keeps the two lists equal). The wallet is always allowed. Automatic
// USDT also needs the crypto_watcher feature.
var LicenseGateways = []string{Zarinpal, Stars, USDTAuto, "manual_card", "manual_crypto", "manual_zarinpal"}

// LicenseConfig is what a license is checked against.
type LicenseConfig struct {
	// Keys are the vendor's public keys; nil means the ones built into core
	// (license.Embedded).
	Keys license.KeyRing
	// Domain is this install's BOBRES_DOMAIN.
	Domain string
	// SellerContact is whom the owner is told to ask to buy or renew a
	// license (BOBRES_SELLER_CONTACT; "" falls back to
	// license.DefaultSellerContact, which is empty unless this build sets
	// one).
	SellerContact string
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

// Valid: the token verified for this install (expired included: it then
// grants nothing, and the install falls back to the trial or the free tier).
func (l *LicenseStatus) Valid() bool {
	return l.State == LicenseActive || l.State == LicenseGrace || l.State == LicenseExpired
}

// licenseState is the token's state for license.Resolve; the token is nil
// when it did not verify for this install.
func (l *LicenseStatus) licenseState() (*license.Token, license.State) {
	if l.Token == nil || !l.Valid() {
		return nil, license.Expired
	}
	switch l.State {
	case LicenseActive:
		return l.Token, license.Active
	case LicenseGrace:
		return l.Token, license.Grace
	}
	return l.Token, license.Expired
}

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

// LicenseRefusal is a new action the effective entitlements do not allow
// (409): the license, the trial or the free tier says no. Gate names the
// entitlement: resellers, max_servers, max_active_services,
// dashboard_staff_seats, gateways or crypto_watcher (or GateResellerPurchase).
// Msg is for staff (it names the limit and says to buy or renew a license); a
// customer is never shown it (PurchasesPaused is what the bot gets for the two
// gates a purchase can meet).
type LicenseRefusal struct{ Gate, Msg string }

// GateActiveServices is the gate of a new service refused for the
// active-services limit.
const GateActiveServices = "max_active_services"

// GateResellerPurchase is the gate of a reseller's purchase of a new service
// refused because the effective entitlements no longer include resellers
// (the reseller exists; what they already bought is untouched).
const GateResellerPurchase = "reseller_purchase"

// IsPurchaseRefusal: err is a customer's (or a reseller's) purchase of a new
// service refused by a license gate, the refusals the bot turns into
// PurchasesPaused.
func IsPurchaseRefusal(err error) bool {
	var lr *LicenseRefusal
	return errors.As(err, &lr) && (lr.Gate == GateActiveServices || lr.Gate == GateResellerPurchase)
}

// PurchasesPaused is what a customer's request gets when a NEW purchase is
// refused for the active-services limit: the bot turns it into its "new
// purchases are temporarily unavailable, please contact support" text. It
// says nothing about licensing.
const PurchasesPaused = "new purchases are paused"

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
	if u.StaffSeats, err = s.st.CountStaffSeats(ctx, s.st.Conn(), s.cfg.OwnerTelegramID); err != nil {
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

// EffectiveLicense is what this install may do now: the answer of
// license.Resolve for the installed token, the trial start and the clock,
// with the token's own status beside it.
type EffectiveLicense struct {
	license.Effective
	// License is the installed token as this install sees it.
	License *LicenseStatus
	// Now is the time the answer is for.
	Now time.Time
	// Contact is whom to ask to buy or renew a license ("" when this
	// install's owner did not set one: BOBRES_SELLER_CONTACT).
	Contact string
}

// Effective is the one answer to "what may this install do now": a valid
// license, a license in its grace period, the implicit trial, or the free
// tier, in that order. Every gate, the dashboard and the CLI read it.
func (s *Service) Effective(ctx context.Context) (*EffectiveLicense, error) {
	st, err := s.License(ctx)
	if err != nil {
		return nil, err
	}
	start, err := s.st.TrialStartedAt(ctx, s.st.Conn())
	if err != nil {
		return nil, err
	}
	now := s.licenseNow()
	if start.After(now) {
		// A trial that "began in the future" began now (Resolve clamps it
		// too); written back, so it really ends TrialDays from now.
		if err := s.st.ClampTrialStart(ctx, s.st.Conn(), now); err != nil {
			s.cfg.Log.Warn("license: clamp the trial start", "err", err)
		}
	}
	tok, state := st.licenseState()
	return &EffectiveLicense{Effective: license.Resolve(tok, state, start, now), License: st, Now: now, Contact: s.sellerContact()}, nil
}

// sellerContact is whom the owner is told to ask for a license.
func (s *Service) sellerContact() string {
	if c := strings.TrimSpace(s.cfg.License.SellerContact); c != "" {
		return c
	}
	return license.DefaultSellerContact
}

// TrialDaysLeft: whole days left of the implicit trial (0 when it is not
// running).
func (e *EffectiveLicense) TrialDaysLeft() int {
	if !e.TrialRunning() {
		return 0
	}
	return e.Effective.TrialDaysLeft(e.Now)
}

// subject is who says no, for a refusal's words.
func (e *EffectiveLicense) subject() string {
	switch e.Basis {
	case license.BasisTrial:
		return "the trial"
	case license.BasisFree:
		return "the free tier"
	}
	return "the license"
}

// plural writes n with its noun ("1 server", "2 servers").
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// refuse words a gate's refusal for staff: what the effective entitlements
// lack, then what to do.
func (e *EffectiveLicense) refuse(gate, lacks string) error {
	return &LicenseRefusal{Gate: gate, Msg: e.subject() + " " + lacks + ": buy or renew a license to " + gateAction[gate]}
}

var gateAction = map[string]string{
	"resellers":             "add resellers",
	"max_servers":           "add servers",
	GateActiveServices:      "sell more services",
	"dashboard_staff_seats": "add staff",
	GateResellerPurchase:    "let resellers buy",
	"gateways":              "switch payment methods on",
	"crypto_watcher":        "switch automatic USDT on",
}

// requireResellers: making a reseller needs the resellers feature.
func (s *Service) requireResellers(ctx context.Context) error {
	eff, err := s.Effective(ctx)
	if err != nil {
		return err
	}
	if eff.Entitlements.Resellers {
		return nil
	}
	return eff.refuse("resellers", "does not include resellers")
}

// requireServerSlot: one more server must fit max_servers.
func (s *Service) requireServerSlot(ctx context.Context, sa ServerAdmin) error {
	eff, err := s.Effective(ctx)
	if err != nil {
		return err
	}
	limit := eff.Entitlements.MaxServers
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
	return eff.refuse("max_servers", "allows "+plural(max(limit, 0), "server"))
}

// requireStaffSeat: one more staff member (admin, support or another owner:
// only the owner the server names takes no seat) must fit the dashboard
// seats. Nobody is removed over a limit, and a role change between the three
// never takes a seat.
func (s *Service) requireStaffSeat(ctx context.Context) error {
	eff, err := s.Effective(ctx)
	if err != nil {
		return err
	}
	limit := eff.Entitlements.DashboardStaffSeat
	if limit == license.Unlimited {
		return nil
	}
	n, err := s.st.CountStaffSeats(ctx, s.st.Conn(), s.cfg.OwnerTelegramID)
	if err != nil {
		return err
	}
	if license.WithinLimit(limit, n) {
		return nil
	}
	return eff.refuse("dashboard_staff_seats", "allows "+plural(max(limit, 0), "staff seat")+" (the store owner takes none)")
}

// slotGate is what a purchase of a new service is checked against: the
// effective entitlements, read before the order's transaction begins (the
// transaction then only counts, so it never needs a second connection while
// it waits for the lock).
type slotGate struct{ eff *EffectiveLicense }

// serviceGate reads what a NEW service (a customer's order, a free trial or
// a reseller's purchase) may do: nil error means go on to check the slot
// inside the order's transaction.
func (s *Service) serviceGate(ctx context.Context) (*slotGate, error) {
	eff, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	return &slotGate{eff: eff}, nil
}

// requireReseller: a reseller's purchase of a NEW service needs the
// resellers feature in effect. The reseller stays a reseller and what they
// bought stays theirs; only buying more stops, which the bot tells them as
// it tells any customer ("purchases are temporarily unavailable"). A retry of
// a request that already made its order (the same idempotency key) is not a
// new purchase.
func (s *Service) requireReseller(ctx context.Context, g *slotGate, u *store.User, idempotencyKey string) error {
	if !isReseller(u) || g.eff.Entitlements.Resellers {
		return nil
	}
	if idempotencyKey != "" {
		if _, err := s.st.OrderByIdempotencyKey(ctx, s.st.Conn(), idempotencyKey); err == nil {
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	return g.eff.refuse(GateResellerPurchase, "does not include resellers")
}

// requireServiceSlot: one more NEW service (a new order, a free trial or a
// reseller's purchase) must fit max_active_services, counting the services
// on their way too (store.CountActiveServices). It runs INSIDE the
// transaction that inserts the order, under an advisory lock, so
// concurrent purchases take turns: the one that passes has inserted its
// order (and is counted) before the next one counts. A retry of a request
// that already made its order (the same idempotency key) is not a new
// service. Renewals, top-ups and payments of orders already made never come
// here: what exists keeps working and renewing over the limit. The caller
// notes a refusal for the staff alert after the transaction has ended
// (noteRefusedPurchase): a write inside it would be rolled back.
func (s *Service) requireServiceSlot(ctx context.Context, tx pgx.Tx, g *slotGate, idempotencyKey string) error {
	limit := g.eff.Entitlements.MaxActiveServices
	if limit == license.Unlimited {
		return nil
	}
	if err := s.st.LockServiceSlots(ctx, tx); err != nil {
		return err
	}
	if idempotencyKey != "" {
		if _, err := s.st.OrderByIdempotencyKey(ctx, tx, idempotencyKey); err == nil {
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
	}
	n, err := s.st.CountActiveServices(ctx, tx)
	if err != nil {
		return err
	}
	if license.WithinLimit(limit, n) {
		return nil
	}
	return g.eff.refuse(GateActiveServices, "allows "+plural(max(limit, 0), "active service"))
}

// noteRefusedPurchase records a purchase refused for the active-services
// limit (the staff alert license_limit reads it), once its transaction has
// ended.
func (s *Service) noteRefusedPurchase(ctx context.Context, err error, at time.Time) {
	var lr *LicenseRefusal
	if !errors.As(err, &lr) || lr.Gate != GateActiveServices {
		return
	}
	if err := s.st.NoteLimitHit(ctx, s.st.Conn(), at); err != nil {
		s.cfg.Log.Warn("license: note a refused purchase", "err", err)
	}
}

// requireGateway: switching a payment method on needs it in the effective
// entitlements (the wallet is always allowed); automatic USDT also needs the
// crypto_watcher feature.
func (s *Service) requireGateway(ctx context.Context, provider string) error {
	if provider == "wallet" {
		return nil
	}
	eff, err := s.Effective(ctx)
	if err != nil {
		return err
	}
	return gatewayRefusal(eff, provider)
}

// gatewayRefusal: eff (nil: nothing to check) does not include provider.
func gatewayRefusal(eff *EffectiveLicense, provider string) error {
	if eff == nil {
		return nil
	}
	if provider == USDTAuto && !eff.Entitlements.HasFeature("crypto_watcher") {
		return eff.refuse("crypto_watcher", "does not include the automatic USDT watcher (crypto_watcher)")
	}
	if eff.Entitlements.HasGateway(provider) {
		return nil
	}
	return eff.refuse("gateways", "does not include the payment method "+provider)
}

// methodOffered: a payment method may be offered to customers now: the
// effective entitlements allow it (automatic USDT needs crypto_watcher, and
// every gateway must be in the gateways list; the wallet is always allowed).
// A license that ended, or a trial that is over, stops the OFFER only:
// payments already started settle, and what is on is not switched off.
func (e *EffectiveLicense) methodOffered(provider string) bool {
	return provider == "wallet" || gatewayRefusal(e, provider) == nil
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
