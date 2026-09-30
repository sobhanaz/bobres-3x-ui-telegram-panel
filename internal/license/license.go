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

// Entitlements are the feature flags and limits a license grants.
type Entitlements struct {
	WhiteLabel         bool     `json:"white_label"`
	MaxServers         int      `json:"max_servers"`
	MaxActiveServices  int      `json:"max_active_services"` // 0 = unlimited
	Resellers          bool     `json:"resellers"`
	Gateways           []string `json:"gateways"`
	CryptoWatcher      bool     `json:"crypto_watcher"`
	DashboardStaffSeat int      `json:"dashboard_staff_seats"`
	UpdatesUntil       string   `json:"updates_until,omitempty"` // YYYY-MM-DD
}

// Token is the signed payload.
type Token struct {
	V            int          `json:"v"`
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
	ErrDomain       = errors.New("license: token is bound to a different domain")
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

// Verify checks the signature against pub and decodes the token. It does not
// check expiry; use State for that. If domain is non-empty and the token is
// bound to a domain, they must match (case-insensitive).
func Verify(pub ed25519.PublicKey, raw, domain string) (Token, error) {
	var zero Token
	if len(pub) != ed25519.PublicKeySize {
		return zero, errors.New("license: invalid public key")
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
	// Verify BEFORE parsing: never interpret unauthenticated data.
	if !ed25519.Verify(pub, payload, sig) {
		return zero, ErrBadSignature
	}
	var t Token
	if err := json.Unmarshal(payload, &t); err != nil {
		return zero, ErrMalformed
	}
	if t.V != Version {
		return zero, fmt.Errorf("%w: %d", ErrVersion, t.V)
	}
	if t.Domain != "" && domain != "" && !strings.EqualFold(t.Domain, domain) {
		return zero, ErrDomain
	}
	return t, nil
}

// State reports the license status at time now. A small clock-skew allowance
// (5 min) is applied to IssuedAt so fresh tokens are not rejected.
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

// WithinLimit reports whether current usage is below a numeric limit
// (0 means unlimited). Used for soft-warn at 90% and hard stop at 100%.
func WithinLimit(limit, current int) bool {
	return limit == 0 || current < limit
}
