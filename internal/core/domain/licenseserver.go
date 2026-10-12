package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// Online activation (Phase 6b). The owner enters a license key; core asks
// the vendor's license server for a token bound to this install and
// installs it as if pasted. Core has no internet access, so the calls go
// through the bot, which passes them on and keeps nothing. Once a day core
// renews an activated license the same way, with the installed token as the
// credential. Policy is unchanged: a missing or invalid license gates
// nothing, and a refresh that fails changes nothing (the token runs into
// its grace and expiry as before).

// LicenseServer reaches the vendor's license server through the bot
// (botfiles.Client). Each call answers the new token.
type LicenseServer interface {
	ActivateLicense(ctx context.Context, a LicenseActivation) (string, error)
	RefreshLicense(ctx context.Context, token string) (string, error)
}

// LicenseActivation is what the license server binds a license key to (the
// JSON body of its POST /v1/activate).
type LicenseActivation struct {
	Key       string `json:"key"`
	InstallID string `json:"install_id"`
	Domain    string `json:"domain"`
	Version   string `json:"version"`
}

// Codes of a LicenseServerError the license server itself never sends.
const (
	// LicenseNotConfigured: no license server is set (BOBRES_LICENSE_SERVER
	// on the bot), or core has no bot to ask.
	LicenseNotConfigured = "not_configured"
	// LicenseUnreachable: the bot or the license server did not answer.
	LicenseUnreachable = "unreachable"
	// LicenseServerFailed: the license server (or the bot) answered with an
	// error that is not a refusal, or with something unreadable.
	LicenseServerFailed = "server_error"
)

// LicenseServerError: the license server refused a call (Status 4xx, Code
// its reason: a wrong key, a revoked license, too many activations), or it,
// or the bot on the way, could not be reached (Status 502 or 503, Code one
// of the License* codes above).
type LicenseServerError struct {
	Status  int
	Code    string
	Message string
}

func (e *LicenseServerError) Error() string {
	return fmt.Sprintf("license server: %s (%s, %d)", e.Message, e.Code, e.Status)
}

// Refused: the license server answered no (as opposed to not answering, or
// asking to slow down).
func (e *LicenseServerError) Refused() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != 429
}

// licenseCallTimeout bounds one call to the license server through the bot.
const licenseCallTimeout = 45 * time.Second

// maxLicenseKey bounds a license key (BOBRES-XXXX-…, far shorter).
const maxLicenseKey = 200

// licenseKey checks an entered license key: printable ASCII without spaces
// (surrounding space dropped).
func licenseKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	switch {
	case key == "":
		return "", &FieldError{"key", "enter the license key"}
	case len(key) > maxLicenseKey:
		return "", &FieldError{"key", "this is too long for a license key"}
	}
	for _, r := range key {
		if r <= ' ' || r > '~' {
			return "", &FieldError{"key", "this is not a license key (letters, digits and dashes)"}
		}
	}
	return key, nil
}

// ActivateLicense asks the license server, through srv, for a token for key
// bound to this install and its domain, and installs it as SetLicense does
// (audited as license.set with source activation). The daily refresh then
// renews it. srv nil: core has no bot to ask.
func (s *Service) ActivateLicense(ctx context.Context, srv LicenseServer, actor *store.User, key, reason string) (*LicenseStatus, error) {
	reason, err := needReasonText(reason)
	if err != nil {
		return nil, err
	}
	if key, err = licenseKey(key); err != nil {
		return nil, err
	}
	dom := license.NormalizeDomain(s.cfg.License.Domain)
	if dom == "" {
		return nil, stateErr("this install has no domain (BOBRES_DOMAIN), and a license is bound to one")
	}
	if srv == nil {
		return nil, &LicenseServerError{Status: 503, Code: LicenseNotConfigured,
			Message: "core has no bot to reach the license server through (BOBRES_BOT_URL)"}
	}
	id, err := s.st.InstallID(ctx, s.st.Conn())
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, licenseCallTimeout)
	raw, err := srv.ActivateLicense(cctx, LicenseActivation{Key: key, InstallID: id, Domain: dom, Version: version.Version})
	cancel()
	if err != nil {
		return nil, err
	}
	return s.installLicense(ctx, actor, raw, reason, store.LicenseActivation)
}

// Refresh timing: the refresher looks every hour after a short wait at
// start; a token installed or refreshed a day ago is renewed; after a
// failed or refused refresh it waits three hours before asking again.
const (
	licenseRefreshFirst = 2 * time.Minute
	licenseRefreshLook  = time.Hour
	licenseRefreshAfter = 24 * time.Hour
	licenseRefreshRetry = 3 * time.Hour
)

// LicenseRefresher renews an activated license once a day: it sends the
// installed token to the license server (through the bot) and installs the
// answer only when it verifies here, is for the same license and was issued
// after the installed one, so neither the server nor the bot can install an
// older token or another license. A license pasted or imported by hand is
// left alone. It is audited (license.refresh) only when the tier or the
// entitlements change, not for every new expiry date.
type LicenseRefresher struct {
	s   *Service
	srv LicenseServer
	log *slog.Logger
	now func() time.Time
	// first is the wait at start, every how often it looks, after the age of
	// a token it renews, retry the pause after a failure.
	first, every, after, retry time.Duration
	// wait: no call before then (after a failure or a refused answer).
	wait time.Time
}

// NewLicenseRefresher builds the daily refresh through srv.
func (s *Service) NewLicenseRefresher(srv LicenseServer, log *slog.Logger) *LicenseRefresher {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &LicenseRefresher{s: s, srv: srv, log: log.With("worker", "license-refresh"), now: time.Now,
		first: licenseRefreshFirst, every: licenseRefreshLook, after: licenseRefreshAfter, retry: licenseRefreshRetry}
}

// Run looks every hour (after a short wait for the bot) until ctx ends.
func (w *LicenseRefresher) Run(ctx context.Context) error {
	first := time.NewTimer(w.first)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return nil
	case <-first.C:
	}
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		if _, err := w.Step(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("license refresh failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step renews the installed license once when it is due: true when a new
// token was installed.
func (w *LicenseRefresher) Step(ctx context.Context) (bool, error) {
	now := w.now()
	if now.Before(w.wait) {
		return false, nil
	}
	st := w.s.st
	row, err := st.GetLicense(ctx, st.Conn())
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if row.Source != store.LicenseActivation && row.Source != store.LicenseRefresh {
		return false, nil
	}
	last := row.UpdatedAt
	if row.RefreshedAt != nil && row.RefreshedAt.After(last) {
		last = *row.RefreshedAt
	}
	if now.Sub(last) < w.after {
		return false, nil
	}
	id, err := st.InstallID(ctx, st.Conn())
	if err != nil {
		return false, err
	}
	// A token that does not verify here (the domain changed, a key this
	// build lacks) is sent nowhere: the server would not renew it for here.
	cur := w.s.evaluate(row.Token, id)
	if !cur.Valid() {
		return false, nil
	}
	cctx, cancel := context.WithTimeout(ctx, licenseCallTimeout)
	raw, err := w.srv.RefreshLicense(cctx, row.Token)
	cancel()
	if err != nil {
		w.wait = now.Add(w.retry)
		return false, err
	}
	next, why := w.renewal(cur, raw, id)
	if why != "" {
		w.wait = now.Add(w.retry)
		return false, errors.New("license refresh: the answer was not installed: " + why)
	}
	var done bool
	err = st.WithTx(ctx, func(tx pgx.Tx) error {
		// Only over the token that was sent: one installed meanwhile stays.
		ok, err := st.RefreshLicense(ctx, tx, row.Token, next.raw)
		if err != nil || !ok {
			return err
		}
		done = true
		if sameGrant(cur.Token, next.Token) {
			return nil
		}
		return w.s.auditChange(ctx, tx, nil, "license.refresh", "license", next.Token.LicenseID,
			grantAudit(cur.Token), grantAudit(next.Token), "renewed by the license server")
	})
	if err != nil {
		return false, err
	}
	if done {
		w.log.Info("license refreshed", "license_id", next.Token.LicenseID, "tier", next.Token.Tier,
			"expires_at", next.Token.ExpiresAt.UTC().Format(time.RFC3339))
	}
	return done, nil
}

// refreshedToken is a refreshed token that may replace the installed one.
type refreshedToken struct {
	*LicenseStatus
	raw string
}

// renewal checks the license server's answer against the installed license
// cur: it must verify here and not have expired, be for the same license and
// have been issued after it. why says what is wrong ("" when nothing).
func (w *LicenseRefresher) renewal(cur *LicenseStatus, raw, installID string) (*refreshedToken, string) {
	raw, err := licenseToken(raw)
	if err != nil {
		return nil, "it is not a license token"
	}
	next := w.s.evaluate(raw, installID)
	switch {
	case next.State == LicenseInvalid:
		return nil, LicenseReasonText(next.Reason) + " (" + next.Reason + ")"
	case next.State == LicenseExpired:
		return nil, "it has expired"
	case next.Token.LicenseID != cur.Token.LicenseID:
		return nil, "it is for another license (" + next.Token.LicenseID + ")"
	case !next.Token.IssuedAt.After(cur.Token.IssuedAt):
		return nil, "it is not newer than the installed one"
	}
	return &refreshedToken{LicenseStatus: next, raw: raw}, ""
}

// sameGrant: a and b give the same tier and entitlements (the order of the
// payment methods aside).
func sameGrant(a, b *license.Token) bool {
	sorted := func(e license.Entitlements) license.Entitlements {
		e.Gateways = slices.Sorted(slices.Values(e.Gateways))
		return e
	}
	return a.Tier == b.Tier && reflect.DeepEqual(sorted(a.Entitlements), sorted(b.Entitlements))
}

// grantAudit is what a refresh's audit keeps of a license: its ids, tier and
// entitlements (never the token).
func grantAudit(t *license.Token) map[string]any {
	out := licenseAudit(t)
	out["entitlements"] = t.Entitlements
	return out
}
