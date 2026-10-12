package web

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// The license (milestone 6b): its state for system.read (owner and admin),
// checking and installing a token for license.write (the owner). The token
// itself is never answered or audited; the audit keeps its ids.

// getLicense: GET /api/v1/license.
func (s *Server) getLicense(w http.ResponseWriter, r *http.Request) {
	st, err := s.cfg.Domain.License(r.Context())
	if err != nil {
		s.fail(w, "license", err)
		return
	}
	writeJSON(w, http.StatusOK, s.licenseView(r.Context(), st))
}

// checkLicense: POST /api/v1/license/check {token}, the state a token would
// have here, saving nothing.
func (s *Server) checkLicense(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	st, err := s.cfg.Domain.CheckLicense(r.Context(), in.Token)
	if err != nil {
		s.licenseFail(w, "check license", err)
		return
	}
	writeJSON(w, http.StatusOK, s.licenseView(r.Context(), st))
}

// putLicense: PUT /api/v1/license {token, reason} installs a token that
// verifies here and has not expired.
func (s *Server) putLicense(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token  string `json:"token"`
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	st, err := s.cfg.Domain.SetLicense(r.Context(), actor(r), in.Token, in.Reason)
	if err != nil {
		s.licenseFail(w, "set license", err)
		return
	}
	writeJSON(w, http.StatusOK, s.licenseView(r.Context(), st))
}

// licenseFail: a token that cannot be installed is a 400 on the token field
// with its reason code.
func (s *Server) licenseFail(w http.ResponseWriter, what string, err error) {
	var le *domain.LicenseError
	if errors.As(err, &le) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "field": "token", "code": le.Reason,
			"message": "token: " + domain.LicenseReasonText(le.Reason)})
		return
	}
	s.fail(w, what, err)
}

// licenseView is a license's state for the dashboard: never the token.
func (s *Server) licenseView(ctx context.Context, st *domain.LicenseStatus) map[string]any {
	out := map[string]any{
		"state": st.State, "reason": st.Reason, "install_id": st.InstallID, "domain": st.Domain, "keys": st.Keys,
		"white_label": st.WhiteLabel(), "powered_by": st.PoweredBy(),
		"license": nil, "binding": nil, "entitlements": nil, "updated_at": nil, "updated_by": nil,
	}
	if st.UpdatedAt != nil {
		out["updated_at"] = st.UpdatedAt.Unix()
	}
	if st.UpdatedBy != nil {
		out["updated_by"] = map[string]any{"id": *st.UpdatedBy, "username": st.UpdatedByName}
	}
	t := st.Token
	if t == nil {
		return out
	}
	out["license"] = map[string]any{
		"license_id": t.LicenseID, "customer": t.Customer, "tier": t.Tier, "kid": t.KeyID,
		"issued_at": t.IssuedAt.Unix(), "expires_at": t.ExpiresAt.Unix(), "grace_days": t.GraceDays,
		"grace_ends_at": st.GraceEndsAt.Unix(), "days_left": daysUntil(s.now(), t.ExpiresAt),
		"domain": t.Domain, "install_id": t.InstallID,
	}
	out["binding"] = map[string]string{
		"domain":     binding(license.NormalizeDomain(t.Domain), st.Domain),
		"install_id": binding(t.InstallID, st.InstallID),
	}
	ctx, cancel := context.WithTimeout(ctx, overviewTimeout)
	defer cancel()
	usage, err := s.cfg.Domain.LicenseUsage(ctx, s.cfg.Servers, s.cfg.Gateways)
	if err != nil {
		s.cfg.Log.Warn("license usage", "err", err)
		usage = &domain.LicenseUsage{Gateways: []string{}}
	}
	e := t.Entitlements
	licensed := e.Gateways
	if licensed == nil {
		licensed = []string{}
	}
	staff := usage.StaffSeats
	active := usage.ActiveServices
	out["entitlements"] = map[string]any{
		"white_label": e.WhiteLabel, "resellers": e.Resellers, "crypto_watcher": e.CryptoWatcher,
		"max_servers":           limitView(e.MaxServers, usage.Servers),
		"max_active_services":   limitView(e.MaxActiveServices, &active),
		"dashboard_staff_seats": limitView(e.DashboardStaffSeat, &staff),
		"gateways":              map[string]any{"licensed": licensed, "enabled": usage.Gateways},
		"updates_until":         e.UpdatesUntil,
	}
	return out
}

// binding compares a token's binding with this install's value: unbound
// when the token names none.
func binding(token, here string) string {
	switch token {
	case "":
		return "unbound"
	case here:
		return "match"
	}
	return "mismatch"
}

// limitView is a limit with its use: used null when unknown, near from 90 %,
// full when nothing more may be added (-1 is unlimited, 0 none).
func limitView(limit int, used *int) map[string]any {
	out := map[string]any{"limit": limit, "used": nil, "near": false, "full": false}
	if used == nil {
		return out
	}
	out["used"] = *used
	out["full"] = !license.WithinLimit(limit, *used)
	out["near"] = limit > 0 && *used*10 >= limit*9
	return out
}

// daysUntil counts whole days from now to t, rounded up (0 or less once t
// has passed).
func daysUntil(now, t time.Time) int {
	return int(math.Ceil(t.Sub(now).Hours() / 24))
}

// licenseBadge is the license as every page needs it (/me): its state and
// whether the store may hide BOBRES's name. nil when it cannot be read.
func (s *Server) licenseBadge(ctx context.Context) map[string]any {
	st, err := s.cfg.Domain.License(ctx)
	if err != nil {
		s.cfg.Log.Warn("license for /me", "err", err)
		return nil
	}
	return map[string]any{"state": st.State, "white_label": st.WhiteLabel(), "powered_by": st.PoweredBy()}
}

// poweredBy: the login page shows "Powered by BOBRES" (a verified license
// without white_label; never on an install without one).
func (s *Server) poweredBy(ctx context.Context) bool {
	st, err := s.cfg.Domain.License(ctx)
	if err != nil {
		s.cfg.Log.Warn("license for /brand", "err", err)
		return false
	}
	return st.PoweredBy()
}
