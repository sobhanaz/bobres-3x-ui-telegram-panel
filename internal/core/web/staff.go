package web

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"regexp"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// sessionIDRe: a session's public id is the hex of its stored hash (a one-way
// hash of the cookie, so it gives nothing away).
var sessionIDRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

type staffJSON struct {
	ID              string         `json:"id"`
	TelegramID      int64          `json:"telegram_id"`
	Username        string         `json:"username"`
	Language        string         `json:"language"`
	Role            string         `json:"role"`
	Status          string         `json:"status"`
	ConfiguredOwner bool           `json:"configured_owner"`
	Me              bool           `json:"me"`
	CreatedAt       int64          `json:"created_at"`
	Password        map[string]any `json:"password"`
	LastLogin       map[string]any `json:"last_login"`
	Sessions        int64          `json:"sessions"`
}

func (s *Server) staffOf(r *store.StaffRow, me *store.User) staffJSON {
	state := "off"
	switch {
	case r.HasPassword && r.TOTPConfirmedAt != nil:
		state = "on"
	case r.HasPassword:
		state = "pending"
	}
	var locked *int64
	if r.LockedUntil != nil && r.LockedUntil.After(s.now()) {
		locked = unix(r.LockedUntil)
	}
	var last map[string]any
	if r.LastLoginAt != nil {
		last = map[string]any{"at": r.LastLoginAt.Unix(), "method": r.LastLoginMethod, "ip": r.LastLoginIP}
	}
	return staffJSON{
		ID: r.ID, TelegramID: r.TelegramID, Username: r.Username, Language: r.Language, Role: r.Role, Status: r.Status,
		ConfiguredOwner: s.cfg.Domain.IsConfiguredOwner(&r.User), Me: r.ID == me.ID, CreatedAt: r.CreatedAt.Unix(),
		Password: map[string]any{"state": state, "username": r.PasswordUser, "locked_until": locked,
			"failed_attempts": r.FailedAttempts},
		LastLogin: last, Sessions: r.Sessions,
	}
}

// listStaff: GET /api/v1/staff.
func (s *Server) listStaff(w http.ResponseWriter, r *http.Request) {
	rows, err := s.cfg.Domain.ListStaff(r.Context(), SessionIdle)
	if err != nil {
		s.fail(w, "list staff", err)
		return
	}
	me := actor(r)
	items := make([]staffJSON, 0, len(rows))
	for i := range rows {
		items = append(items, s.staffOf(&rows[i], me))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items,
		"owner_configured": s.cfg.Domain.OwnerConfigured(), "can_grant_owner": s.cfg.Domain.CanGrantOwner(me)})
}

// writeStaff answers one member as the list shows them.
func (s *Server) writeStaff(w http.ResponseWriter, r *http.Request, id string) {
	row, err := s.cfg.Store.GetStaff(r.Context(), s.cfg.Store.Conn(), id, SessionIdle)
	if err != nil {
		s.fail(w, "load staff", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": s.staffOf(row, actor(r))})
}

// addStaff: POST /api/v1/staff {user_id, role, reason}.
func (s *Server) addStaff(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !uuidRe.MatchString(req.UserID) {
		writeError(w, http.StatusBadRequest, "invalid", "user_id must be a user's id")
		return
	}
	u, err := s.cfg.Domain.AddStaff(r.Context(), actor(r), req.UserID, req.Role, req.Reason)
	if err != nil {
		s.fail(w, "add staff", err)
		return
	}
	s.writeStaff(w, r, u.ID)
}

// setStaffRole: PUT /api/v1/staff/{id}/role {role, reason}.
func (s *Server) setStaffRole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Role   string `json:"role"`
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if _, err := s.cfg.Domain.SetStaffRole(r.Context(), actor(r), id, req.Role, req.Reason); err != nil {
		s.fail(w, "set staff role", err)
		return
	}
	s.writeStaff(w, r, id)
}

// readReason reads {reason}; false after answering.
func readReason(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return "", false
	}
	return req.Reason, true
}

// removeStaff: POST /api/v1/staff/{id}/remove {reason}.
func (s *Server) removeStaff(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	reason, ok := readReason(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Domain.RemoveStaff(r.Context(), actor(r), id, reason); err != nil {
		s.fail(w, "remove staff", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type sessionJSON struct {
	ID         string `json:"id"`
	Method     string `json:"method"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	ExpiresAt  int64  `json:"expires_at"`
	Current    bool   `json:"current"`
}

func sessionsOf(list []store.WebSession, current []byte) []sessionJSON {
	out := make([]sessionJSON, 0, len(list))
	for _, w := range list {
		out = append(out, sessionJSON{ID: hex.EncodeToString(w.IDHash), Method: w.Method, IP: w.IP, UserAgent: w.UserAgent,
			CreatedAt: w.CreatedAt.Unix(), LastSeenAt: w.LastSeenAt.Unix(), ExpiresAt: w.ExpiresAt.Unix(),
			Current: current != nil && bytes.Equal(w.IDHash, current)})
	}
	return out
}

// staffSessions: GET /api/v1/staff/{id}/sessions.
func (s *Server) staffSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	list, err := s.cfg.Domain.StaffSessions(r.Context(), actor(r), id, SessionIdle)
	if err != nil {
		s.fail(w, "staff sessions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": sessionsOf(list, staffFrom(r.Context()).idHash)})
}

// revokeStaffSessions: POST /api/v1/staff/{id}/sessions/revoke {reason}.
func (s *Server) revokeStaffSessions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	reason, ok := readReason(w, r)
	if !ok {
		return
	}
	n, err := s.cfg.Domain.RevokeStaffSessions(r.Context(), actor(r), id, reason)
	if err != nil {
		s.fail(w, "revoke staff sessions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": n})
}

// sessionID reads a session's public id from the path; false after answering.
func sessionID(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	sid := r.PathValue("sid")
	if !sessionIDRe.MatchString(sid) {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return nil, false
	}
	b, _ := hex.DecodeString(sid)
	return b, true
}

// revokeStaffSession: POST /api/v1/staff/{id}/sessions/{sid}/revoke.
func (s *Server) revokeStaffSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	sid, ok := sessionID(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Domain.RevokeStaffSession(r.Context(), actor(r), id, sid); err != nil {
		s.fail(w, "revoke staff session", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetStaffPassword: POST /api/v1/staff/{id}/password/reset {reason}.
func (s *Server) resetStaffPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	reason, ok := readReason(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Domain.ResetStaffPassword(r.Context(), actor(r), id, reason); err != nil {
		s.fail(w, "reset staff password", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unlockStaff: POST /api/v1/staff/{id}/unlock.
func (s *Server) unlockStaff(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.UnlockStaff(r.Context(), actor(r), id); err != nil {
		s.fail(w, "unlock staff", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mySessions: GET /api/v1/me/sessions, the staff member's own sessions.
func (s *Server) mySessions(w http.ResponseWriter, r *http.Request) {
	st := staffFrom(r.Context())
	list, err := s.cfg.Store.ListSessions(r.Context(), s.cfg.Store.Conn(), st.user.ID, SessionIdle)
	if err != nil {
		s.fail(w, "my sessions", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": sessionsOf(list, st.idHash)})
}

// revokeMySession: POST /api/v1/me/sessions/{sid}/revoke, logs another of
// my devices out.
func (s *Server) revokeMySession(w http.ResponseWriter, r *http.Request) {
	st := staffFrom(r.Context())
	sid, ok := sessionID(w, r)
	if !ok {
		return
	}
	if bytes.Equal(sid, st.idHash) {
		writeError(w, http.StatusConflict, "state", "this is the session you are using; log out instead")
		return
	}
	done, err := s.cfg.Store.RevokeUserSession(r.Context(), s.cfg.Store.Conn(), st.user.ID, sid)
	if err != nil {
		s.fail(w, "revoke session", err)
		return
	}
	if !done {
		writeError(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	s.audit(r.Context(), st.user, "dashboard.session.revoke", map[string]string{"session": hex.EncodeToString(sid)[:12]})
	w.WriteHeader(http.StatusNoContent)
}

// revokeMyOtherSessions: POST /api/v1/me/sessions/revoke-others.
func (s *Server) revokeMyOtherSessions(w http.ResponseWriter, r *http.Request) {
	st := staffFrom(r.Context())
	n, err := s.cfg.Store.RevokeOtherSessions(r.Context(), s.cfg.Store.Conn(), st.user.ID, st.idHash)
	if err != nil {
		s.fail(w, "revoke sessions", err)
		return
	}
	s.audit(r.Context(), st.user, "dashboard.sessions.revoke", map[string]int64{"count": n})
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": n})
}
