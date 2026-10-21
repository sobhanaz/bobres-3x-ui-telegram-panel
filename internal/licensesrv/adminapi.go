package licensesrv

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// The signed admin API: the same things `licensesrv key` does (make, renew, change the tier of, and revoke a
// license), for a program: the owner's own BOBRES store sells licenses and asks this server for the keys.
//
// It is OFF unless an admin public key is configured (BOBRES_LICENSESRV_ADMIN_PUBKEY_FILE). Every request is
// signed (internal/license/adminsig.go): the signature covers the method, the URI, a timestamp, a one-time nonce
// and the body, and any failure to authenticate is answered with the same 401. Every change carries a caller
// chosen `ref`, and a ref works once: a retry gets the first answer back and changes nothing again (admin_ops).
// A license key is returned once, by create (and rekey), and never stored or logged; only its hash is kept.
//
//	POST /admin/v1/licenses/create   {ref, customer, tier, months}     tier standard|pro, months 1..120
//	POST /admin/v1/licenses/extend   {ref, id, months}                 renews: adds months to the end (or to now)
//	POST /admin/v1/licenses/tier     {ref, id, tier}                   takes the new tier's preset entitlements
//	POST /admin/v1/licenses/revoke   {ref, id, reason}
//	POST /admin/v1/licenses/rekey    {ref, id}                         a new key for a license nobody activated yet
//	POST /admin/v1/licenses/get      {id}
//	POST /admin/v1/licenses/list     {status}                          "", active or revoked; at most 500, newest first
const (
	adminMaxBody    = 16 << 10
	adminMaxMonths  = 120
	adminListLimit  = 500
	adminActor      = "api:admin"
	adminDefaultMin = 1
)

// DefaultAdminRate bounds the admin API per client address: a store sells a few licenses an hour at most.
const DefaultAdminRate = "60/1m"

var adminRefRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,100}$`)

// AdminAPIOptions configure the admin API.
type AdminAPIOptions struct {
	// PublicKey verifies the signed requests (required).
	PublicKey ed25519.PublicKey
	// Rate limits requests per client address (DefaultAdminRate when zero).
	Rate Rate
	Now  func() time.Time
	Log  *slog.Logger
}

// AdminAPI serves /admin/v1.
type AdminAPI struct {
	api   *API // its trusted-proxy rules and HTTPS check
	st    *Store
	pub   ed25519.PublicKey
	limit *limiter
	now   func() time.Time
	log   *slog.Logger
}

// NewAdminAPI builds the admin API next to the public one.
func NewAdminAPI(api *API, st *Store, o AdminAPIOptions) (*AdminAPI, error) {
	if len(o.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("licensesrv: the admin public key is not an Ed25519 public key")
	}
	if o.Rate.Max <= 0 || o.Rate.Window <= 0 {
		r, err := ParseRate(DefaultAdminRate)
		if err != nil {
			return nil, err
		}
		o.Rate = r
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &AdminAPI{api: api, st: st, pub: o.PublicKey, limit: newLimiter(o.Rate, o.Now), now: o.Now, log: o.Log}, nil
}

// Register mounts the admin API on mux.
func (a *AdminAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/v1/licenses/create", a.handle(a.create))
	mux.HandleFunc("POST /admin/v1/licenses/extend", a.handle(a.extend))
	mux.HandleFunc("POST /admin/v1/licenses/tier", a.handle(a.setTier))
	mux.HandleFunc("POST /admin/v1/licenses/revoke", a.handle(a.revoke))
	mux.HandleFunc("POST /admin/v1/licenses/rekey", a.handle(a.rekey))
	mux.HandleFunc("POST /admin/v1/licenses/get", a.handle(a.get))
	mux.HandleFunc("POST /admin/v1/licenses/list", a.handle(a.list))
}

// AdminRequest is every endpoint's body (an endpoint reads the fields it needs and refuses unknown ones).
type AdminRequest struct {
	Ref      string `json:"ref,omitempty"`
	ID       string `json:"id,omitempty"`
	Customer string `json:"customer,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Months   int    `json:"months,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Status   string `json:"status,omitempty"`
}

// AdminLicense is a license as the admin API shows it. Key is set only in the answer that made it.
type AdminLicense struct {
	ID           string     `json:"id"`
	Customer     string     `json:"customer"`
	Tier         string     `json:"tier"`
	Status       string     `json:"status"`
	EndsAt       *time.Time `json:"ends_at"`
	CreatedAt    time.Time  `json:"created_at"`
	Reason       string     `json:"reason,omitempty"`
	ActiveDomain string     `json:"active_domain,omitempty"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	Key          string     `json:"key,omitempty"`
	// Replayed: the ref had been used; nothing changed this time.
	Replayed bool `json:"replayed,omitempty"`
}

func viewOf(l License) AdminLicense {
	return AdminLicense{ID: l.ID, Customer: l.Customer, Tier: l.Tier, Status: l.Status, EndsAt: l.EndsAt, CreatedAt: l.CreatedAt, Reason: l.Reason}
}

type adminCall struct {
	ip   string
	body []byte
	now  time.Time
}

type adminHandler func(ctx context.Context, c adminCall) (any, *Error, error)

// handle authenticates a request, runs h and writes its answer.
func (a *AdminAPI) handle(h adminHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := a.authorize(w, r)
		if !ok {
			return
		}
		out, aerr, err := h(r.Context(), c)
		switch {
		case aerr != nil:
			writeError(w, aerr)
		case err != nil:
			a.log.Error("admin request failed", "path", r.URL.Path, "err", err)
			writeError(w, &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "something went wrong; try again later"})
		default:
			writeJSON(w, http.StatusOK, out)
		}
	}
}

func unauthorized() *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "the request is not signed by the admin key"}
}

func (a *AdminAPI) authorize(w http.ResponseWriter, r *http.Request) (adminCall, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	peer := peerAddr(r)
	viaProxy := a.api.isTrusted(peer)
	ip := a.api.clientIP(r, peer, viaProxy)
	overHTTPS := r.TLS != nil || (viaProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
	if !overHTTPS && !a.api.allowHTTP {
		writeError(w, &Error{Status: http.StatusForbidden, Code: "https_required", Message: "use https"})
		return adminCall{}, false
	}
	if ok, retry := a.limit.allow("admin:" + ipKey(ip)); !ok {
		a.log.Warn("admin rate limited", "ip", ip)
		writeError(w, rateLimited(retry))
		return adminCall{}, false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, adminMaxBody))
	now := a.now().UTC()
	if err != nil {
		writeError(w, invalid("the request body is too large or unreadable"))
		return adminCall{}, false
	}
	nonce := r.Header.Get(license.AdminHeaderNonce)
	if err := license.AdminVerify(a.pub, r.Method, r.URL.RequestURI(), r.Header.Get(license.AdminHeaderTS), nonce,
		r.Header.Get(license.AdminHeaderSig), body, now); err != nil {
		a.log.Warn("admin request refused", "ip", ip, "path", r.URL.Path)
		writeError(w, unauthorized())
		return adminCall{}, false
	}
	// Only a request that verified may leave a nonce behind, so strangers cannot fill the table.
	fresh, err := a.useNonce(r.Context(), nonce, now)
	if err != nil {
		a.log.Error("admin nonce", "err", err)
		writeError(w, &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "something went wrong; try again later"})
		return adminCall{}, false
	}
	if !fresh {
		a.log.Warn("admin request replayed", "ip", ip, "path", r.URL.Path)
		writeError(w, unauthorized())
		return adminCall{}, false
	}
	return adminCall{ip: ip, body: body, now: now}, true
}

// useNonce records the nonce and reports whether it was new; it also forgets the nonces whose time has passed.
func (a *AdminAPI) useNonce(ctx context.Context, nonce string, now time.Time) (bool, error) {
	if _, err := a.st.db.Exec(ctx, `DELETE FROM licensesrv.admin_nonces WHERE expires_at < $1`, now); err != nil {
		return false, err
	}
	tag, err := a.st.db.Exec(ctx, `INSERT INTO licensesrv.admin_nonces (nonce, expires_at) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		nonce, now.Add(license.AdminNonceTTL))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func decodeAdmin(body []byte) (AdminRequest, *Error) {
	var req AdminRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, invalid("the request body is not valid JSON for this endpoint")
	}
	return req, nil
}

func conflict(code, msg string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: msg}
}

func notFound() *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: "no such license"}
}

// claim reserves ref for action inside tx. It returns the first answer when the ref was used before (the caller
// then changes nothing), or nil when the ref is new.
func (a *AdminAPI) claim(ctx context.Context, tx pgx.Tx, ref, action string, now time.Time) (*AdminLicense, *Error, error) {
	tag, err := tx.Exec(ctx, `INSERT INTO licensesrv.admin_ops (ref, action, result, at) VALUES ($1, $2, '{}', $3) ON CONFLICT (ref) DO NOTHING`,
		ref, action, now)
	if err != nil {
		return nil, nil, err
	}
	if tag.RowsAffected() == 1 {
		return nil, nil, nil
	}
	var prevAction string
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT action, result FROM licensesrv.admin_ops WHERE ref = $1`, ref).Scan(&prevAction, &raw); err != nil {
		return nil, nil, err
	}
	if prevAction != action {
		return nil, conflict("ref_reused", "this ref was used for another action"), nil
	}
	var v AdminLicense
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, nil, fmt.Errorf("admin op %s: %w", ref, err)
	}
	v.Replayed = true
	return &v, nil, nil
}

// finish keeps the answer for ref (never a key).
func (a *AdminAPI) finish(ctx context.Context, tx pgx.Tx, ref string, v AdminLicense) error {
	v.Key = ""
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE licensesrv.admin_ops SET license_id = $2, result = $3 WHERE ref = $1`, ref, v.ID, raw)
	return err
}

func (a *AdminAPI) audit(ctx context.Context, tx pgx.Tx, c adminCall, action, id string, details map[string]any) error {
	return a.st.AddAudit(ctx, tx, AuditEntry{At: c.now, Actor: adminActor, Action: action, LicenseID: id, IP: c.ip, Details: details})
}

func months(n int) *Error {
	if n < adminDefaultMin || n > adminMaxMonths {
		return invalid(fmt.Sprintf("months is %d..%d", adminDefaultMin, adminMaxMonths))
	}
	return nil
}

// paidTier is a tier the API sells: the trial is the install's own.
func paidTier(name string) (license.Preset, *Error) {
	presets, err := license.LoadPresets()
	if err != nil {
		return license.Preset{}, invalid("the tiers could not be read")
	}
	p, ok := presets.Get(name)
	if !ok || (name != license.TierStandard && name != license.TierPro) {
		return license.Preset{}, invalid("tier is standard or pro")
	}
	return p, nil
}

func (a *AdminAPI) create(ctx context.Context, c adminCall) (any, *Error, error) {
	req, aerr := decodeAdmin(c.body)
	if aerr != nil {
		return nil, aerr, nil
	}
	customer := strings.TrimSpace(req.Customer)
	switch {
	case !adminRefRe.MatchString(req.Ref):
		return nil, invalid("ref is 8..100 letters, digits or . _ : -"), nil
	case customer == "" || len(customer) > 200 || strings.ContainsFunc(customer, func(r rune) bool { return r < ' ' || r == 0x7f }):
		return nil, invalid("customer is required (at most 200 characters, no control characters)"), nil
	}
	if aerr := months(req.Months); aerr != nil {
		return nil, aerr, nil
	}
	preset, aerr := paidTier(req.Tier)
	if aerr != nil {
		return nil, aerr, nil
	}
	ends := license.AddMonths(c.now, req.Months)
	l := License{Customer: customer, Tier: req.Tier, Entitlements: preset.Entitlements, EndsAt: &ends,
		GraceDays: preset.GraceDays, Status: StatusActive, CreatedAt: c.now, UpdatedAt: c.now}
	if err := checkLicense(l, c.now, true); err != nil {
		return nil, invalid(err.Error()), nil
	}
	var err error
	if l.ID, err = newLicenseID(); err != nil {
		return nil, nil, err
	}
	key, err := NewKey()
	if err != nil {
		return nil, nil, err
	}
	var out *AdminLicense
	var rejected *Error
	err = a.st.WithTx(ctx, func(tx pgx.Tx) error {
		prev, e, err := a.claim(ctx, tx, req.Ref, "create", c.now)
		if err != nil || e != nil || prev != nil {
			out, rejected = prev, e
			return err
		}
		if err := a.st.CreateLicense(ctx, tx, l, HashKey(key)); err != nil {
			return err
		}
		v := viewOf(l)
		if err := a.finish(ctx, tx, req.Ref, v); err != nil {
			return err
		}
		if err := a.audit(ctx, tx, c, "license.create", l.ID, licenseDetails(l)); err != nil {
			return err
		}
		v.Key = key
		out = &v
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if rejected != nil {
		return nil, rejected, nil
	}
	return out, nil, nil
}

// change runs one change to an existing license inside a transaction with the license locked. fn returns the
// license after the change (and what to audit), or an *Error that rolls nothing back but writes nothing either.
func (a *AdminAPI) change(ctx context.Context, c adminCall, action string, req AdminRequest,
	fn func(tx pgx.Tx, l *License) (License, string, map[string]any, *Error, error)) (any, *Error, error) {
	if !adminRefRe.MatchString(req.Ref) {
		return nil, invalid("ref is 8..100 letters, digits or . _ : -"), nil
	}
	var out *AdminLicense
	var rejected *Error
	err := a.st.WithTx(ctx, func(tx pgx.Tx) error {
		prev, e, err := a.claim(ctx, tx, req.Ref, action, c.now)
		if err != nil || e != nil || prev != nil {
			out, rejected = prev, e
			return err
		}
		l, err := a.st.LicenseByID(ctx, tx, req.ID, true)
		if errors.Is(err, ErrNotFound) {
			rejected = notFound()
			return errRollback
		}
		if err != nil {
			return err
		}
		after, audited, details, e, err := fn(tx, l)
		if err != nil {
			return err
		}
		if e != nil {
			rejected = e
			return errRollback
		}
		v := viewOf(after)
		if err := a.finish(ctx, tx, req.Ref, v); err != nil {
			return err
		}
		if err := a.audit(ctx, tx, c, audited, l.ID, details); err != nil {
			return err
		}
		out = &v
		return nil
	})
	if errors.Is(err, errRollback) {
		return nil, rejected, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if rejected != nil {
		return nil, rejected, nil
	}
	return out, nil, nil
}

// errRollback undoes the transaction (including the reserved ref, so the caller may retry with the same ref
// after fixing the request) for a refusal that is not a failure.
var errRollback = errors.New("rollback")

func (a *AdminAPI) extend(ctx context.Context, c adminCall) (any, *Error, error) {
	req, aerr := decodeAdmin(c.body)
	if aerr != nil {
		return nil, aerr, nil
	}
	if aerr := months(req.Months); aerr != nil {
		return nil, aerr, nil
	}
	return a.change(ctx, c, "extend", req, func(tx pgx.Tx, l *License) (License, string, map[string]any, *Error, error) {
		if l.Status == StatusRevoked {
			return License{}, "", nil, conflict("revoked", "a revoked license does not change"), nil
		}
		if l.EndsAt == nil {
			return License{}, "", nil, conflict("no_end", "this license has no end date to extend"), nil
		}
		after := *l
		base := l.EndsAt.UTC()
		if base.Before(c.now) {
			base = c.now
		}
		renewed := license.AddMonths(base, req.Months)
		after.EndsAt, after.UpdatedAt = &renewed, c.now
		if err := checkLicense(after, c.now, true); err != nil {
			return License{}, "", nil, invalid(err.Error()), nil
		}
		if err := a.st.UpdateLicense(ctx, tx, after); err != nil {
			return License{}, "", nil, nil, err
		}
		return after, "license.update", map[string]any{"via": "api", "extend_months": req.Months, "before": licenseDetails(*l), "after": licenseDetails(after)}, nil, nil
	})
}

func (a *AdminAPI) setTier(ctx context.Context, c adminCall) (any, *Error, error) {
	req, aerr := decodeAdmin(c.body)
	if aerr != nil {
		return nil, aerr, nil
	}
	preset, aerr := paidTier(req.Tier)
	if aerr != nil {
		return nil, aerr, nil
	}
	return a.change(ctx, c, "tier", req, func(tx pgx.Tx, l *License) (License, string, map[string]any, *Error, error) {
		if l.Status == StatusRevoked {
			return License{}, "", nil, conflict("revoked", "a revoked license does not change"), nil
		}
		after := *l
		after.Tier, after.Entitlements, after.UpdatedAt = req.Tier, preset.Entitlements, c.now
		if err := checkLicense(after, c.now, false); err != nil {
			return License{}, "", nil, invalid(err.Error()), nil
		}
		if err := a.st.UpdateLicense(ctx, tx, after); err != nil {
			return License{}, "", nil, nil, err
		}
		return after, "license.update", map[string]any{"via": "api", "before": licenseDetails(*l), "after": licenseDetails(after)}, nil, nil
	})
}

func (a *AdminAPI) revoke(ctx context.Context, c adminCall) (any, *Error, error) {
	req, aerr := decodeAdmin(c.body)
	if aerr != nil {
		return nil, aerr, nil
	}
	why := strings.TrimSpace(req.Reason)
	if why == "" || len(why) > 500 {
		return nil, invalid("reason is required (at most 500 characters)"), nil
	}
	return a.change(ctx, c, "revoke", req, func(tx pgx.Tx, l *License) (License, string, map[string]any, *Error, error) {
		if l.Status == StatusRevoked {
			return License{}, "", nil, conflict("revoked", "the license is already revoked"), nil
		}
		if err := a.st.RevokeLicense(ctx, tx, l.ID, why, c.now); err != nil {
			return License{}, "", nil, nil, err
		}
		after := *l
		after.Status, after.Reason, after.UpdatedAt, after.RevokedAt = StatusRevoked, why, c.now, &c.now
		return after, "license.revoke", map[string]any{"via": "api", "reason": why}, nil, nil
	})
}

// rekey gives a license a new key, for a store that lost the one create returned (a crash between the two). It
// works only while nobody has activated the license: a key that runs somewhere is not taken away.
func (a *AdminAPI) rekey(ctx context.Context, c adminCall) (any, *Error, error) {
	req, aerr := decodeAdmin(c.body)
	if aerr != nil {
		return nil, aerr, nil
	}
	key, err := NewKey()
	if err != nil {
		return nil, nil, err
	}
	var issued string
	out, aerr, err := a.change(ctx, c, "rekey", req, func(tx pgx.Tx, l *License) (License, string, map[string]any, *Error, error) {
		if l.Status == StatusRevoked {
			return License{}, "", nil, conflict("revoked", "a revoked license does not change"), nil
		}
		used, err := a.st.HasActivations(ctx, tx, l.ID)
		if err != nil {
			return License{}, "", nil, nil, err
		}
		if used {
			return License{}, "", nil, conflict("in_use", "the license has been activated; its key cannot be replaced"), nil
		}
		if _, err := tx.Exec(ctx, `UPDATE licensesrv.licenses SET key_hash = $2, updated_at = $3 WHERE id = $1`, l.ID, HashKey(key), c.now); err != nil {
			return License{}, "", nil, nil, err
		}
		issued = key
		after := *l
		after.UpdatedAt = c.now
		return after, "license.rekey", map[string]any{"via": "api"}, nil, nil
	})
	if v, ok := out.(*AdminLicense); ok && v != nil && issued != "" {
		v.Key = issued
	}
	return out, aerr, err
}

func (a *AdminAPI) get(ctx context.Context, c adminCall) (any, *Error, error) {
	req, aerr := decodeAdmin(c.body)
	if aerr != nil {
		return nil, aerr, nil
	}
	l, err := a.st.LicenseByID(ctx, a.st.db, req.ID, false)
	if errors.Is(err, ErrNotFound) {
		return nil, notFound(), nil
	}
	if err != nil {
		return nil, nil, err
	}
	v := viewOf(*l)
	if act, err := a.st.ActiveActivation(ctx, a.st.db, l.ID); err == nil && act != nil {
		v.ActiveDomain, v.LastSeenAt = act.Domain, &act.LastSeenAt
	}
	return v, nil, nil
}

func (a *AdminAPI) list(ctx context.Context, c adminCall) (any, *Error, error) {
	req, aerr := decodeAdmin(c.body)
	if aerr != nil {
		return nil, aerr, nil
	}
	if req.Status != "" && req.Status != StatusActive && req.Status != StatusRevoked {
		return nil, invalid("status is empty, active or revoked"), nil
	}
	rows, err := a.st.ListLicenses(ctx, a.st.db, req.Status, c.now.AddDate(0, 0, -30))
	if err != nil {
		return nil, nil, err
	}
	if len(rows) > adminListLimit {
		rows = rows[:adminListLimit]
	}
	out := make([]AdminLicense, 0, len(rows))
	for _, r := range rows {
		v := viewOf(r.License)
		v.ActiveDomain, v.LastSeenAt = r.ActiveDomain, r.LastSeenAt
		out = append(out, v)
	}
	return map[string]any{"licenses": out}, nil, nil
}
