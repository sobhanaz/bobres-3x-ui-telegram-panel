// Package license verifies signed license tokens and exposes entitlements.
//
// A token is "<base64url(payload JSON)>.<base64url(Ed25519 signature)>". The
// vendor signs with a private key kept offline; binaries embed only the public
// key. Verification is fully local (no network per request).
//
// Honest limit: software running on a customer's server can be modified by
// that customer. The license enables billing and deters casual sharing.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Version is the only supported token format version.
const Version = 1

// Tier names.
const (
	TierTrial    = "trial"
	TierStandard = "standard"
	TierPro      = "pro"
)

// Unlimited marks a numeric limit as intentionally unlimited. Zero means NONE:
// a token that omits a limit must never grant unlimited use (fail closed).
const Unlimited = -1

// Entitlements are the feature flags and limits a license grants.
type Entitlements struct {
	WhiteLabel         bool     `json:"white_label"`
	MaxServers         int      `json:"max_servers"`
	MaxActiveServices  int      `json:"max_active_services"` // Unlimited (-1) or a count; 0 = none
	Resellers          bool     `json:"resellers"`
	Gateways           []string `json:"gateways"`
	CryptoWatcher      bool     `json:"crypto_watcher"`
	DashboardStaffSeat int      `json:"dashboard_staff_seats"`
	UpdatesUntil       string   `json:"updates_until,omitempty"` // YYYY-MM-DD
}

// Token is the signed payload.
type Token struct {
	V            int          `json:"v"`
	KeyID        string       `json:"kid"` // which vendor signing key made this token (rotation)
	LicenseID    string       `json:"license_id"`
	Customer     string       `json:"customer"`
	InstallID    string       `json:"install_id,omitempty"`
	Domain       string       `json:"domain,omitempty"`
	Tier         string       `json:"tier"`
	IssuedAt     time.Time    `json:"issued_at"`
	ExpiresAt    time.Time    `json:"expires_at"`
	GraceDays    int          `json:"grace_days"`
	Entitlements Entitlements `json:"entitlements"`
}

// State is the runtime status of a verified license.
type State int

// License states.
const (
	Active  State = iota // before expires_at
	Grace                // expired but inside grace: features intact, warn loudly
	Expired              // past grace: degrade (no new sales), never delete data
)

func (s State) String() string {
	switch s {
	case Active:
		return "active"
	case Grace:
		return "grace"
	default:
		return "expired"
	}
}

// Errors returned by Verify.
var (
	ErrMalformed    = errors.New("license: malformed token")
	ErrBadSignature = errors.New("license: signature verification failed")
	ErrVersion      = errors.New("license: unsupported token version")
	ErrDomain       = errors.New("license: token domain binding does not match this install")
	ErrInstallID    = errors.New("license: token install binding does not match this install")
	ErrUnbound      = errors.New("license: token is not bound to a domain but binding is required")
	ErrInvalid      = errors.New("license: invalid token contents")
	ErrNotYetValid  = errors.New("license: token issued in the future")
)

// Sign creates a token string. Used by the vendor license server and tests.
func Sign(priv ed25519.PrivateKey, t Token) (string, error) {
	if t.V == 0 {
		t.V = Version
	}
	payload, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(priv, payload)
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(sig), nil
}

// KeyRing maps key IDs to vendor public keys so signing keys can be rotated:
// ship the new public key alongside the old one, then retire the old one.
type KeyRing map[string]ed25519.PublicKey

// Options describe the install a token is being checked against.
type Options struct {
	// Domain is this install's public domain (BOBRES_DOMAIN).
	Domain string
	// InstallID is this install's soft identifier.
	InstallID string
	// RequireBound rejects tokens carrying no domain binding. Use in production.
	RequireBound bool
}

// NormalizeDomain lowercases, drops a port and trailing dot. Internationalized
// names must be given in punycode.
func NormalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	if h, _, err := net.SplitHostPort(d); err == nil {
		d = h
	}
	return strings.TrimSuffix(d, ".")
}

// Verify checks the signature against every key in the ring, then validates
// the payload and its bindings. It does NOT check expiry: use Check (or State)
// for that. The payload is never parsed before a signature verifies.
//
// Binding is fail-closed: if the token names a domain or install ID, the
// caller must supply a matching one. An empty value never matches a bound token.
func Verify(keys KeyRing, raw string, o Options) (Token, error) {
	var zero Token
	if len(keys) == 0 {
		return zero, errors.New("license: no public keys configured")
	}
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 2 {
		return zero, ErrMalformed
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(parts[0])
	if err != nil {
		return zero, ErrMalformed
	}
	sig, err := enc.DecodeString(parts[1])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return zero, ErrMalformed
	}
	signedBy := ""
	for id, pub := range keys {
		if len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, payload, sig) {
			signedBy = id
			break
		}
	}
	if signedBy == "" {
		return zero, ErrBadSignature
	}
	var t Token
	if err := json.Unmarshal(payload, &t); err != nil {
		return zero, ErrMalformed
	}
	if t.V != Version {
		return zero, fmt.Errorf("%w: %d", ErrVersion, t.V)
	}
	if t.KeyID != signedBy {
		return zero, fmt.Errorf("%w: kid %q does not match signing key %q", ErrInvalid, t.KeyID, signedBy)
	}
	if err := t.Validate(); err != nil {
		return zero, err
	}
	if t.Domain == "" {
		if o.RequireBound {
			return zero, ErrUnbound
		}
	} else if d := NormalizeDomain(o.Domain); d == "" || d != NormalizeDomain(t.Domain) {
		return zero, ErrDomain
	}
	if t.InstallID != "" && (o.InstallID == "" || o.InstallID != t.InstallID) {
		return zero, ErrInstallID
	}
	return t, nil
}

// Check is Verify plus State, so callers cannot forget the expiry check.
func Check(keys KeyRing, raw string, o Options, now time.Time) (Token, State, error) {
	t, err := Verify(keys, raw, o)
	if err != nil {
		return Token{}, Expired, err
	}
	st, err := t.State(now)
	return t, st, err
}

const maxGraceDays = 90

// Validate rejects structurally unsafe tokens (even if correctly signed).
func (t Token) Validate() error {
	var errs []error
	if t.LicenseID == "" {
		errs = append(errs, errors.New("license_id is required"))
	}
	switch t.Tier {
	case TierTrial, TierStandard, TierPro:
	default:
		errs = append(errs, fmt.Errorf("unknown tier %q", t.Tier))
	}
	if t.ExpiresAt.IsZero() || !t.ExpiresAt.After(t.IssuedAt) {
		errs = append(errs, errors.New("expires_at must be after issued_at"))
	}
	if t.GraceDays < 0 || t.GraceDays > maxGraceDays {
		errs = append(errs, fmt.Errorf("grace_days must be 0..%d", maxGraceDays))
	}
	if t.Entitlements.MaxServers < Unlimited || t.Entitlements.MaxActiveServices < Unlimited || t.Entitlements.DashboardStaffSeat < Unlimited {
		errs = append(errs, errors.New("limits must be >= -1"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
	}
	return nil
}

// State reports the license status at time now. A 5 minute clock-skew allowance
// is applied to IssuedAt only, so freshly issued tokens are not rejected; expiry is
// exact. The clock itself is trusted: rolling it back can extend a license, which
// is an accepted limit of local verification (mitigated by periodic refresh).
func (t Token) State(now time.Time) (State, error) {
	if t.IssuedAt.After(now.Add(5 * time.Minute)) {
		return Expired, ErrNotYetValid
	}
	if now.Before(t.ExpiresAt) {
		return Active, nil
	}
	grace := t.ExpiresAt.Add(time.Duration(t.GraceDays) * 24 * time.Hour)
	if now.Before(grace) {
		return Grace, nil
	}
	return Expired, nil
}

// HasFeature reports whether a boolean entitlement is granted. Unknown names
// are denied (fail closed).
func (e Entitlements) HasFeature(name string) bool {
	switch name {
	case "white_label":
		return e.WhiteLabel
	case "resellers":
		return e.Resellers
	case "crypto_watcher":
		return e.CryptoWatcher
	default:
		return false
	}
}

// HasGateway reports whether a payment gateway is licensed.
func (e Entitlements) HasGateway(name string) bool {
	for _, g := range e.Gateways {
		if strings.EqualFold(g, name) {
			return true
		}
	}
	return false
}

// UpdatesAllowed reports whether a release built on releaseDate (YYYY-MM-DD)
// may be installed. Empty UpdatesUntil means always. Running versions are never
// affected; this only gates installing newer builds.
func (e Entitlements) UpdatesAllowed(releaseDate string) bool {
	if e.UpdatesUntil == "" {
		return true
	}
	return releaseDate <= e.UpdatesUntil // ISO dates sort lexicographically
}

// WithinLimit reports whether current usage is below limit. Unlimited (-1)
// always passes; 0 allows nothing. Use for soft-warn at 90% and a hard stop at 100%.
func WithinLimit(limit, current int) bool {
	return limit == Unlimited || (limit > 0 && current < limit)
}
