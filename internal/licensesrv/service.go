// Package licensesrv is the vendor's license server (docs/13-license-server.md):
// it turns a secret activation key into a short-lived signed license token
// for one install, refreshes that token daily, limits how often a key moves
// to another install, and stops refreshing revoked licenses. It runs only on
// the vendor's own server, with its own Postgres; customer installs never
// carry it and reach it through the bot or the CLI (core has no egress).
package licensesrv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Token and activation rules (docs/13-license-server.md).
const (
	// TokenLifetime caps a token's expires_at; the license's own end can
	// make it earlier. Installs refresh daily, so a revoked license stops
	// working within this plus grace.
	TokenLifetime = 30 * 24 * time.Hour
	// DefaultGraceDays is a new license's grace_days.
	DefaultGraceDays = 14
	// IssuedBackdate moves issued_at into the past: installs refuse a token
	// issued more than 5 minutes ahead of their clock.
	IssuedBackdate = 5 * time.Minute
	// MaxReactivations is how many times a key may move to another
	// (install id, domain) pair inside ReactivationWindow; the first
	// activation is free.
	MaxReactivations   = 3
	ReactivationWindow = 365 * 24 * time.Hour
	// maxTokenLen matches the most core stores.
	maxTokenLen = 16384
)

// Error is a refusal, answered as {"error": Code, "message": Message}.
type Error struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration // 429 only
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func invalid(msg string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "invalid", Message: msg}
}

var (
	errUnknownKey = &Error{Status: http.StatusForbidden, Code: "invalid_key",
		Message: "this license key is not valid; check it, or ask the vendor"}
	errRevoked = &Error{Status: http.StatusForbidden, Code: "revoked",
		Message: "this license has been revoked; ask the vendor"}
	errNotActive = &Error{Status: http.StatusConflict, Code: "not_active",
		Message: "the license is active on another install now; activate it again here with the license key"}
)

func rateLimited(retry time.Duration) *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "rate_limited",
		Message: "too many requests; try again later", RetryAfter: retry}
}

// Options configure a Service.
type Options struct {
	Store  *Store
	Signer *Signer
	// Ring checks refresh tokens (VerifyRing).
	Ring license.KeyRing
	// KeyRate limits requests per license: activations with its key and
	// refreshes of its tokens share one budget.
	KeyRate Rate
	Now     func() time.Time
	Log     *slog.Logger
}

// Service activates and refreshes licenses.
type Service struct {
	store    *Store
	signer   *Signer
	ring     license.KeyRing
	keyLimit *limiter
	now      func() time.Time
	log      *slog.Logger
}

// NewService builds a Service.
func NewService(o Options) (*Service, error) {
	if o.Store == nil || o.Signer == nil || len(o.Ring) == 0 {
		return nil, errors.New("licensesrv: store, signer and ring are required")
	}
	if o.KeyRate.Max <= 0 || o.KeyRate.Window <= 0 {
		return nil, errors.New("licensesrv: the key rate must be positive")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: o.Store, signer: o.Signer, ring: o.Ring, keyLimit: newLimiter(o.KeyRate, o.Now), now: o.Now, log: o.Log}, nil
}

// ActivateRequest is POST /v1/activate's body.
type ActivateRequest struct {
	Key       string `json:"key"`
	InstallID string `json:"install_id"`
	Domain    string `json:"domain"`
	Version   string `json:"version"`
}

// RefreshRequest is POST /v1/refresh's body. Version is optional.
type RefreshRequest struct {
	Token   string `json:"token"`
	Version string `json:"version"`
}

// Activate turns a key into a token bound to (install id, domain). The pair
// already active is a refresh; another pair moves the license there,
// counted against MaxReactivations after the first activation.
func (s *Service) Activate(ctx context.Context, req ActivateRequest, ip string) (string, error) {
	key, ok := NormalizeKey(req.Key)
	if !ok {
		return "", invalid("the license key is not in the BOBRES-XXXX-XXXX-... form")
	}
	installID, err := canonicalInstallID(req.InstallID)
	if err != nil {
		return "", invalid("install_id must be this install's id (a UUID, from `bobres license status`)")
	}
	domain := license.NormalizeDomain(req.Domain)
	if !validDomain(domain) {
		return "", invalid("domain must be this install's domain (BOBRES_DOMAIN)")
	}
	version, err := cleanVersion(req.Version)
	if err != nil {
		return "", err
	}
	hash := HashKey(key)
	now := s.now().UTC()
	var token, licID string
	var refusal *Error
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lic, err := s.store.LicenseByKeyHash(ctx, tx, hash, true)
		if errors.Is(err, ErrNotFound) {
			refusal = errUnknownKey
			return nil
		}
		if err != nil {
			return err
		}
		licID = lic.ID
		// Counted per license once the key is known to be real: random keys
		// never fill the table, and the per-address limit covers guessing.
		if ok, retry := s.keyLimit.allow("lic:" + lic.ID); !ok {
			refusal = rateLimited(retry)
			return nil
		}
		if refusal = usable(lic, now); refusal != nil {
			return nil
		}
		act, err := s.store.ActiveActivation(ctx, tx, lic.ID)
		if err != nil {
			return err
		}
		if act != nil && act.InstallID == installID && act.Domain == domain {
			if err := s.store.TouchActivation(ctx, tx, act.ID, now, version); err != nil {
				return err
			}
		} else {
			if refusal, err = s.move(ctx, tx, lic, act, installID, domain, version, ip, now); err != nil || refusal != nil {
				return err
			}
		}
		token, err = s.issue(lic, installID, domain, now)
		return err
	})
	if err != nil {
		return "", err
	}
	if refusal != nil {
		s.log.Warn("activation refused", "code", refusal.Code, "license_id", licID, "domain", domain, "ip", ip)
		return "", refusal
	}
	s.log.Info("license activated", "license_id", licID, "domain", domain, "install_id", installID, "ip", ip)
	return token, nil
}

// move activates the license on a new (install id, domain) pair: the first
// activation is free, later ones count, and the old activation ends. A
// refusal over the limit is audited and returned (the transaction commits
// the audit row).
func (s *Service) move(ctx context.Context, tx pgx.Tx, lic *License, old *Activation, installID, domain, version, ip string, now time.Time) (*Error, error) {
	counted, err := s.store.HasActivations(ctx, tx, lic.ID)
	if err != nil {
		return nil, err
	}
	details := map[string]any{"install_id": installID, "domain": domain, "version": version}
	if old != nil {
		details["from_install_id"], details["from_domain"] = old.InstallID, old.Domain
	}
	if counted {
		n, oldest, err := s.store.CountedSince(ctx, tx, lic.ID, now.Add(-ReactivationWindow))
		if err != nil {
			return nil, err
		}
		if n >= MaxReactivations && oldest != nil {
			next := oldest.Add(ReactivationWindow)
			details["moves"], details["next_move"] = n, next.UTC().Format(time.RFC3339)
			if err := s.store.AddAudit(ctx, tx, AuditEntry{At: now, Actor: "api", Action: "activation.refused", LicenseID: lic.ID, IP: ip, Details: details}); err != nil {
				return nil, err
			}
			return &Error{Status: http.StatusConflict, Code: "reactivation_limit", Message: fmt.Sprintf(
				"this license key has moved to another install %d times in the last year (the most allowed); the next move is possible on %s, or ask the vendor",
				n, next.UTC().Format(time.DateOnly))}, nil
		}
		details["moves"] = n + 1
	}
	if old != nil {
		if err := s.store.Deactivate(ctx, tx, old.ID, now); err != nil {
			return nil, err
		}
	}
	if err := s.store.InsertActivation(ctx, tx, Activation{
		LicenseID: lic.ID, InstallID: installID, Domain: domain, Counted: counted, ActivatedAt: now, LastVersion: version,
	}); err != nil {
		return nil, err
	}
	action := "activation.create"
	if counted {
		action = "activation.move"
	}
	return nil, s.store.AddAudit(ctx, tx, AuditEntry{At: now, Actor: "api", Action: action, LicenseID: lic.ID, IP: ip, Details: details})
}

// Refresh exchanges a token this server issued for a fresh one. The token is
// the credential: it must verify, its binding must be the license's active
// activation, and the license must be usable.
func (s *Service) Refresh(ctx context.Context, req RefreshRequest, ip string) (string, error) {
	raw := strings.TrimSpace(req.Token)
	if raw == "" || len(raw) > maxTokenLen {
		return "", invalid("token is required")
	}
	version, err := cleanVersion(req.Version)
	if err != nil {
		return "", err
	}
	tok, err := license.Parse(s.ring, raw)
	if err != nil {
		return "", &Error{Status: http.StatusForbidden, Code: "invalid_token",
			Message: "this token was not issued by this server (" + license.Reason(err) + ")"}
	}
	installID, idErr := canonicalInstallID(tok.InstallID)
	domain := license.NormalizeDomain(tok.Domain)
	if idErr != nil || domain == "" {
		return "", &Error{Status: http.StatusForbidden, Code: "invalid_token",
			Message: "this token is not bound to an install; activate with the license key"}
	}
	// Counted per license (with its activations) only now: before the
	// signature verified, anyone could name someone else's license and use
	// up its budget.
	if ok, retry := s.keyLimit.allow("lic:" + tok.LicenseID); !ok {
		return "", rateLimited(retry)
	}
	now := s.now().UTC()
	var token string
	var refusal *Error
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		lic, err := s.store.LicenseByID(ctx, tx, tok.LicenseID, true)
		if errors.Is(err, ErrNotFound) {
			refusal = &Error{Status: http.StatusForbidden, Code: "invalid_token", Message: "this token names no license on this server"}
			return nil
		}
		if err != nil {
			return err
		}
		if refusal = usable(lic, now); refusal != nil {
			return nil
		}
		act, err := s.store.ActiveActivation(ctx, tx, lic.ID)
		if err != nil {
			return err
		}
		if act == nil || act.InstallID != installID || act.Domain != domain {
			refusal = errNotActive
			return nil
		}
		if err := s.store.TouchActivation(ctx, tx, act.ID, now, version); err != nil {
			return err
		}
		token, err = s.issue(lic, act.InstallID, act.Domain, now)
		return err
	})
	if err != nil {
		return "", err
	}
	if refusal != nil {
		s.log.Warn("refresh refused", "code", refusal.Code, "license_id", tok.LicenseID, "domain", domain, "ip", ip)
		return "", refusal
	}
	s.log.Info("license refreshed", "license_id", tok.LicenseID, "domain", domain, "ip", ip)
	return token, nil
}

// usable refuses a revoked or ended license.
func usable(lic *License, now time.Time) *Error {
	if lic.Status != StatusActive {
		return errRevoked
	}
	if lic.EndsAt != nil && !now.Before(*lic.EndsAt) {
		return &Error{Status: http.StatusForbidden, Code: "expired", Message: fmt.Sprintf(
			"this license ended on %s; renew it with the vendor", lic.EndsAt.UTC().Format(time.DateOnly))}
	}
	return nil
}

// issue signs a token for lic on (installID, domain): issued_at backdated,
// expires_at at most TokenLifetime ahead and never past the license's end.
func (s *Service) issue(lic *License, installID, domain string, now time.Time) (string, error) {
	now = now.UTC().Truncate(time.Second)
	exp := now.Add(TokenLifetime)
	var ends *time.Time
	if lic.EndsAt != nil {
		e := lic.EndsAt.UTC()
		ends = &e
		if e.Before(exp) {
			exp = e
		}
	}
	t := license.Token{
		V: license.Version, KeyID: s.signer.KeyID, LicenseID: lic.ID, Customer: lic.Customer,
		InstallID: installID, Domain: domain, Tier: lic.Tier,
		IssuedAt: now.Add(-IssuedBackdate), ExpiresAt: exp, GraceDays: lic.GraceDays,
		Entitlements: lic.Entitlements, LicenseEndsAt: ends,
	}
	if err := t.Validate(); err != nil {
		return "", fmt.Errorf("license %s: %w", lic.ID, err)
	}
	return s.signer.Sign(t)
}

// canonicalInstallID is an install id in core's form (lower-case UUID).
func canonicalInstallID(s string) (string, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// validDomain accepts a normalized host name (or IPv4 address): labels of
// letters, digits and '-', not starting or ending with '-'.
func validDomain(d string) bool {
	if d == "" || len(d) > 253 {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// cleanVersion checks the optional version an install sends.
func cleanVersion(v string) (string, error) {
	v = strings.TrimSpace(v)
	if len(v) > 64 {
		return "", invalid("version is too long")
	}
	for _, c := range v {
		if c < 0x21 || c > 0x7e {
			return "", invalid("version must be printable ASCII")
		}
	}
	return v, nil
}
