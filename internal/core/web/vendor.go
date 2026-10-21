package web

import (
	"net/http"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// License sales in the dashboard (docs/13-license-server.md, "The signed admin API"): the owner of the store that
// sells BOBRES licenses sets where the license server is and the key that signs the requests to it (write-only: no
// answer ever carries the key), sees the licenses sold, gives months for nothing, and revokes one. Staff with
// license.write (the owner) only. The switch itself is the vendor.enabled setting.

type vendorTierJSON struct {
	Name    string `json:"name"`
	Monthly money  `json:"monthly"`
	Yearly  money  `json:"yearly"`
}

// getVendor: GET /api/v1/vendor.
func (s *Server) getVendor(w http.ResponseWriter, r *http.Request) {
	st, err := s.cfg.Domain.VendorStatus(r.Context())
	if err != nil {
		s.fail(w, "vendor status", err)
		return
	}
	tiers := make([]vendorTierJSON, 0, len(st.Tiers))
	for _, t := range st.Tiers {
		tiers = append(tiers, vendorTierJSON{Name: t.Name, Monthly: money{t.Monthly, t.Currency}, Yearly: money{t.Yearly, t.Currency}})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": st.Available, "on": st.On, "configured": st.Configured, "admin_url": st.AdminURL, "tiers": tiers,
	})
}

// putVendorConfig: PUT /api/v1/vendor/config {admin_url, admin_key}. The key is kept encrypted and never read back.
func (s *Server) putVendorConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AdminURL string `json:"admin_url"`
		AdminKey string `json:"admin_key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.cfg.Domain.SetVendorConfig(r.Context(), actor(r), req.AdminURL, req.AdminKey); err != nil {
		s.fail(w, "set the license server", err)
		return
	}
	s.getVendor(w, r)
}

type soldLicenseJSON struct {
	ID        string     `json:"id"`
	LicenseID string     `json:"license_id"`
	Tier      string     `json:"tier"`
	Status    string     `json:"status"`
	EndsAt    *int64     `json:"ends_at"`
	CreatedAt int64      `json:"created_at"`
	Buyer     *userBrief `json:"buyer"`
}

func soldLicenseOf(l *store.VendorLicense, buyer *userBrief) soldLicenseJSON {
	return soldLicenseJSON{ID: l.ID, LicenseID: l.LicenseID, Tier: l.Tier, Status: l.Status, EndsAt: unix(l.EndsAt),
		CreatedAt: l.CreatedAt.Unix(), Buyer: buyer}
}

// listSoldLicenses: GET /api/v1/vendor/licenses?status=&page=&size= (newest first).
func (s *Server) listSoldLicenses(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	rows, total, err := s.cfg.Domain.ListSoldLicenses(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")), p)
	if err != nil {
		s.fail(w, "list sold licenses", err)
		return
	}
	items := make([]soldLicenseJSON, 0, len(rows))
	for i := range rows {
		items = append(items, soldLicenseOf(&rows[i].VendorLicense, briefOf(rows[i].UserID, rows[i].TelegramID, rows[i].Username)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "size": size})
}

// grantSoldLicense: POST /api/v1/vendor/licenses/{id}/grant {months, request_id}: months for nothing.
func (s *Server) grantSoldLicense(w http.ResponseWriter, r *http.Request) {
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
	l, err := s.cfg.Domain.GrantVendorMonths(r.Context(), actor(r), id, req.Months, req.RequestID)
	if err != nil {
		s.fail(w, "grant months", err)
		return
	}
	writeJSON(w, http.StatusOK, soldLicenseOf(l, nil))
}

// revokeSoldLicense: POST /api/v1/vendor/licenses/{id}/revoke {reason}.
func (s *Server) revokeSoldLicense(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	l, err := s.cfg.Domain.RevokeVendorLicense(r.Context(), actor(r), id, req.Reason)
	if err != nil {
		s.fail(w, "revoke a license", err)
		return
	}
	writeJSON(w, http.StatusOK, soldLicenseOf(l, nil))
}
