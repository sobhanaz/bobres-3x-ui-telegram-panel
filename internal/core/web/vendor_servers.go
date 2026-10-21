package web

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Every license of the license server in the dashboard (docs/13-license-server.md): the store's own, the ones made by
// hand with `licensesrv key`, and the ones the bot sold. Owner only (license.write). The owner looks at them, makes
// one by hand, changes one, renews, revokes and re-keys. The activation key is in the answer that makes (or re-keys)
// a license and nowhere else.

var serverLicenseRe = regexp.MustCompile(`^lic_[a-z0-9_-]{1,36}$`)

func serverLicenseID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := strings.ToLower(r.PathValue("id"))
	if !serverLicenseRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "no such license")
		return "", false
	}
	return id, true
}

// vendorFail answers a failure of a call to the license server: its refusal keeps its meaning (400 invalid, 404, 409),
// anything else (it did not answer, the bot is down) is a 502 the owner can retry.
func (s *Server) vendorFail(w http.ResponseWriter, what string, err error) {
	var ve *domain.VendorError
	if errors.As(err, &ve) {
		switch {
		case ve.Status == http.StatusNotFound:
			writeError(w, http.StatusNotFound, "not_found", "the license server has no such license")
		case ve.Status == http.StatusBadRequest:
			writeError(w, http.StatusBadRequest, "invalid", ve.Message)
		case ve.Status == http.StatusConflict:
			writeError(w, http.StatusConflict, "state", ve.Message)
		case ve.Status == http.StatusUnauthorized:
			writeError(w, http.StatusConflict, "state", "the license server does not accept this admin key: check it, and that the license server has its public key")
		case ve.Status >= 400 && ve.Status < 500:
			writeError(w, http.StatusConflict, "state", "the license server refused: "+ve.Message)
		default:
			writeError(w, http.StatusBadGateway, "unavailable", "the license server did not answer; try again in a minute")
		}
		return
	}
	if errors.Is(err, domain.ErrState) || errors.Is(err, domain.ErrInvalid) || errors.Is(err, store.ErrNotFound) {
		s.fail(w, what, err) // core's own refusal (not set up, a bad field)
		return
	}
	if strings.Contains(err.Error(), "license server") || strings.Contains(err.Error(), "the bot") {
		writeError(w, http.StatusBadGateway, "unavailable", "the license server did not answer; try again in a minute")
		return
	}
	s.fail(w, what, err)
}

type serverActivationJSON struct {
	InstallID     string `json:"install_id"`
	Domain        string `json:"domain"`
	Counted       bool   `json:"counted"`
	ActivatedAt   int64  `json:"activated_at"`
	LastSeenAt    int64  `json:"last_seen_at"`
	Version       string `json:"version"`
	DeactivatedAt *int64 `json:"deactivated_at"`
}

type serverAuditJSON struct {
	At      int64          `json:"at"`
	Actor   string         `json:"actor"`
	Action  string         `json:"action"`
	IP      string         `json:"ip"`
	Details map[string]any `json:"details"`
}

type serverLicenseJSON struct {
	ID           string                 `json:"id"`
	Customer     string                 `json:"customer"`
	Tier         string                 `json:"tier"`
	Status       string                 `json:"status"`
	EndsAt       *int64                 `json:"ends_at"`
	CreatedAt    int64                  `json:"created_at"`
	Reason       string                 `json:"reason,omitempty"`
	ActiveDomain string                 `json:"active_domain,omitempty"`
	LastSeenAt   *int64                 `json:"last_seen_at"`
	GraceDays    int                    `json:"grace_days"`
	Moves        int                    `json:"moves"`
	Key          string                 `json:"key,omitempty"`
	Replayed     bool                   `json:"replayed,omitempty"`
	Entitlements any                    `json:"entitlements,omitempty"`
	Activations  []serverActivationJSON `json:"activations,omitempty"`
	Audit        []serverAuditJSON      `json:"audit,omitempty"`
}

func serverLicenseOf(v *domain.VendorLicenseInfo) serverLicenseJSON {
	out := serverLicenseJSON{ID: v.ID, Customer: v.Customer, Tier: v.Tier, Status: v.Status, EndsAt: unix(v.EndsAt), CreatedAt: v.CreatedAt.Unix(),
		Reason: v.Reason, ActiveDomain: v.ActiveDomain, LastSeenAt: unix(v.LastSeenAt), GraceDays: v.GraceDays, Moves: v.Moves, Key: v.Key, Replayed: v.Replayed}
	if len(v.Entitlements) > 0 {
		out.Entitlements = v.Entitlements
	}
	for _, a := range v.Activations {
		out.Activations = append(out.Activations, serverActivationJSON{InstallID: a.InstallID, Domain: a.Domain, Counted: a.Counted,
			ActivatedAt: a.ActivatedAt.Unix(), LastSeenAt: a.LastSeenAt.Unix(), Version: a.Version, DeactivatedAt: unix(a.DeactivatedAt)})
	}
	for _, e := range v.Audit {
		out.Audit = append(out.Audit, serverAuditJSON{At: e.At.Unix(), Actor: e.Actor, Action: e.Action, IP: e.IP, Details: e.Details})
	}
	return out
}

// listServerLicenses: GET /api/v1/vendor/server-licenses?status= (newest first, at most 500).
func (s *Server) listServerLicenses(w http.ResponseWriter, r *http.Request) {
	ls, err := s.cfg.Domain.ListServerLicenses(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")))
	if err != nil {
		s.vendorFail(w, "list the licenses", err)
		return
	}
	items := make([]serverLicenseJSON, 0, len(ls))
	for i := range ls {
		items = append(items, serverLicenseOf(&ls[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

// getServerLicense: GET /api/v1/vendor/server-licenses/{id}: what it grants, where it ran and its audit trail.
func (s *Server) getServerLicense(w http.ResponseWriter, r *http.Request) {
	id, ok := serverLicenseID(w, r)
	if !ok {
		return
	}
	v, err := s.cfg.Domain.GetServerLicense(r.Context(), id)
	if err != nil {
		s.vendorFail(w, "read a license", err)
		return
	}
	writeJSON(w, http.StatusOK, serverLicenseOf(v))
}

// createServerLicense: POST /api/v1/vendor/server-licenses {customer, tier, months | ends_at | no_end, request_id}.
func (s *Server) createServerLicense(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Customer  string `json:"customer"`
		Tier      string `json:"tier"`
		Months    int    `json:"months"`
		EndsAt    string `json:"ends_at"`
		NoEnd     bool   `json:"no_end"`
		RequestID string `json:"request_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	v, err := s.cfg.Domain.CreateServerLicense(r.Context(), actor(r), domain.ServerLicenseCreate{Customer: req.Customer, Tier: req.Tier,
		Months: req.Months, EndsAt: req.EndsAt, NoEnd: req.NoEnd, RequestID: req.RequestID})
	if err != nil {
		s.vendorFail(w, "make a license", err)
		return
	}
	writeJSON(w, http.StatusOK, serverLicenseOf(v))
}

// updateServerLicense: PUT /api/v1/vendor/server-licenses/{id} {extend_months | customer, tier, ends_at, no_end, request_id}.
func (s *Server) updateServerLicense(w http.ResponseWriter, r *http.Request) {
	id, ok := serverLicenseID(w, r)
	if !ok {
		return
	}
	var req struct {
		Customer     string `json:"customer"`
		Tier         string `json:"tier"`
		EndsAt       string `json:"ends_at"`
		NoEnd        bool   `json:"no_end"`
		ExtendMonths int    `json:"extend_months"`
		RequestID    string `json:"request_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	v, err := s.cfg.Domain.UpdateServerLicense(r.Context(), actor(r), id, domain.ServerLicenseUpdate{Customer: req.Customer, Tier: req.Tier,
		EndsAt: req.EndsAt, NoEnd: req.NoEnd, ExtendMonths: req.ExtendMonths, RequestID: req.RequestID})
	if err != nil {
		s.vendorFail(w, "change a license", err)
		return
	}
	writeJSON(w, http.StatusOK, serverLicenseOf(v))
}

// revokeServerLicense: POST /api/v1/vendor/server-licenses/{id}/revoke {reason}.
func (s *Server) revokeServerLicense(w http.ResponseWriter, r *http.Request) {
	id, ok := serverLicenseID(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	v, err := s.cfg.Domain.RevokeServerLicense(r.Context(), actor(r), id, req.Reason)
	if err != nil {
		s.vendorFail(w, "revoke a license", err)
		return
	}
	writeJSON(w, http.StatusOK, serverLicenseOf(v))
}

// rekeyServerLicense: POST /api/v1/vendor/server-licenses/{id}/rekey {request_id}: a new key, once, while nobody has
// activated the license.
func (s *Server) rekeyServerLicense(w http.ResponseWriter, r *http.Request) {
	id, ok := serverLicenseID(w, r)
	if !ok {
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	v, err := s.cfg.Domain.RekeyServerLicense(r.Context(), actor(r), id, req.RequestID)
	if err != nil {
		s.vendorFail(w, "issue a key", err)
		return
	}
	writeJSON(w, http.StatusOK, serverLicenseOf(v))
}
