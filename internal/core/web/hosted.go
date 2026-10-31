package web

import (
	"encoding/json"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The Hosted stores page (docs/04-dashboard.md, Hosted stores; docs/18-hosted-and-affiliate-plans.md, section 1): the register of the customers'
// stores the owner runs, and their health read from each store's /metrics through the bot. Owner only (license.write), where the license grants
// the vendor's own features. The metrics token is write-only: an answer says whether one is set, never the token; the customer's bot token
// and backup key are never here.

type hostedJSON struct {
	ID          string          `json:"id"`
	Customer    string          `json:"customer"`
	Domain      string          `json:"domain"`
	Version     string          `json:"version"`
	LicenseID   string          `json:"license_id"`
	Channel     string          `json:"channel"`
	Notes       string          `json:"notes"`
	HasToken    bool            `json:"has_token"`
	CreatedAt   int64           `json:"created_at"`
	CheckedAt   *int64          `json:"checked_at"`
	Up          *bool           `json:"up"`
	LastVersion string          `json:"last_version"`
	Figures     json.RawMessage `json:"figures"`
	LastError   string          `json:"last_error"`
}

func hostedOf(h *store.HostedStore) hostedJSON {
	figures := h.Figures
	if len(figures) == 0 {
		figures = json.RawMessage(`{}`)
	}
	return hostedJSON{ID: h.ID, Customer: h.Customer, Domain: h.Domain, Version: h.Version, LicenseID: h.LicenseID, Channel: h.Channel,
		Notes: h.Notes, HasToken: h.MetricsTokenEnc != nil, CreatedAt: h.CreatedAt.Unix(), CheckedAt: unix(h.CheckedAt), Up: h.Up,
		LastVersion: h.LastVersion, Figures: figures, LastError: h.LastError}
}

type hostedBody struct {
	Registration string `json:"registration"`
	Customer     string `json:"customer"`
	Domain       string `json:"domain"`
	Version      string `json:"version"`
	LicenseID    string `json:"license_id"`
	Channel      string `json:"channel"`
	Notes        string `json:"notes"`
	MetricsToken string `json:"metrics_token"`
	ClearToken   bool   `json:"clear_token"`
}

func (b hostedBody) input() domain.HostedInput {
	return domain.HostedInput{Registration: b.Registration, Customer: b.Customer, Domain: b.Domain, Version: b.Version, LicenseID: b.LicenseID,
		Channel: b.Channel, Notes: b.Notes, MetricsToken: b.MetricsToken, ClearToken: b.ClearToken}
}

// listHosted: GET /api/v1/hosted -> {items}.
func (s *Server) listHosted(w http.ResponseWriter, r *http.Request) {
	all, err := s.cfg.Domain.ListHostedStores(r.Context())
	if err != nil {
		s.fail(w, "list hosted stores", err)
		return
	}
	items := make([]hostedJSON, 0, len(all))
	for i := range all {
		items = append(items, hostedOf(&all[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// postHosted: POST /api/v1/hosted {registration | customer, domain, ...}: register a store.
func (s *Server) postHosted(w http.ResponseWriter, r *http.Request) {
	var b hostedBody
	if !readJSON(w, r, &b) {
		return
	}
	h, err := s.cfg.Domain.AddHostedStore(r.Context(), actor(r), b.input())
	if err != nil {
		s.fail(w, "register a hosted store", err)
		return
	}
	writeJSON(w, http.StatusCreated, hostedOf(h))
}

// putHosted: PUT /api/v1/hosted/{id} {customer, version, license_id, channel, notes, metrics_token?, clear_token?}.
func (s *Server) putHosted(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var b hostedBody
	if !readJSON(w, r, &b) {
		return
	}
	h, err := s.cfg.Domain.UpdateHostedStore(r.Context(), actor(r), id, b.input())
	if err != nil {
		s.fail(w, "update a hosted store", err)
		return
	}
	writeJSON(w, http.StatusOK, hostedOf(h))
}

// deleteHosted: DELETE /api/v1/hosted/{id}: out of the register (the store itself is not touched).
func (s *Server) deleteHosted(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.DeleteHostedStore(r.Context(), actor(r), id); err != nil {
		s.fail(w, "delete a hosted store", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkHosted: POST /api/v1/hosted/{id}/check: read the store's /metrics now.
func (s *Server) checkHosted(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	h, err := s.cfg.Domain.CheckHostedStore(r.Context(), id)
	if err != nil {
		s.fail(w, "check a hosted store", err)
		return
	}
	writeJSON(w, http.StatusOK, hostedOf(h))
}

// postHostedLicense: POST /api/v1/hosted/{id}/license {months, request_id}: the store's "Hosted" license (pro). The key is in this answer only.
func (s *Server) postHostedLicense(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Months    int    `json:"months"`
		RequestID string `json:"request_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	info, h, err := s.cfg.Domain.CreateHostedLicense(r.Context(), actor(r), id, req.Months, req.RequestID)
	if err != nil {
		s.vendorFail(w, "make a hosted license", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"store": hostedOf(h), "license_id": info.ID, "key": info.Key, "ends_at": unix(info.EndsAt)})
}
