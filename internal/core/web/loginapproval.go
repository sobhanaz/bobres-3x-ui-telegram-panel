package web

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
)

// Approving sign-ins in Telegram (docs/04-dashboard.md, "Signing in"). While the owner's switch is on, a staff sign-in that passed its
// checks answers 202 and a pending cookie instead of a session: the browser polls POST /api/v1/auth/pending, which turns an approved
// sign-in into the session (once) and answers what became of the others. The pending cookie is a random value of its own, kept hashed by
// core, and good for nothing else: it is not a session, and an old core that never heard of approvals never takes it for one.

// pendingCookie is the cookie of a sign-in that waits.
const pendingCookie = "__Host-bobres_pending"

// approvalCommand is what the owner types on the server to sign in without approval; the dashboard shows it wherever an approval cannot
// come.
const approvalCommand = "sudo bobres admin approval off"

func setPendingCookie(w http.ResponseWriter, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: pendingCookie, Value: value, Path: "/", MaxAge: int(ttl.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func clearPendingCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: pendingCookie, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// pendingBody tells a browser how the sign-in it waits for stands.
func pendingBody(status string, expires time.Time, approvers []string, delivered, failed bool) map[string]any {
	if approvers == nil {
		approvers = []string{}
	}
	return map[string]any{"status": status, "expires_at": expires.Unix(), "approvers": approvers, "delivered": delivered, "failed": failed,
		"ttl_s": int(domain.LoginApprovalTTL / time.Second), "command": approvalCommand}
}

// startPendingLogin starts the wait for an approver's yes for a staff member who just passed their sign-in checks.
func (s *Server) startPendingLogin(w http.ResponseWriter, r *http.Request, user *store.User, method string) {
	ticket, hash, err := webauth.NewToken()
	if err != nil {
		s.internal(w, "new sign-in ticket", err)
		return
	}
	p, err := s.cfg.Domain.RequestLoginApproval(r.Context(), domain.LoginRequest{User: user, Method: method, IP: clientIP(r),
		UserAgent: truncate(r.UserAgent(), 256), TicketHash: hash})
	var (
		limit *domain.ApprovalLimitError
		und   *domain.UndeliverableError
	)
	switch {
	case errors.As(err, &limit):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(limit.RetryAfter.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "approval_rate_limited", "too many sign-in requests for this account; wait a few minutes")
	case errors.As(err, &und):
		writeApprovalUnavailable(w, http.StatusServiceUnavailable, und.Reason)
	case err != nil:
		s.internal(w, "request a sign-in approval", err)
	default:
		setPendingCookie(w, ticket, domain.LoginApprovalTTL+time.Minute)
		// The first answer: delivered, since a sign-in nobody could be asked about does not get here.
		writeJSON(w, http.StatusAccepted, pendingBody(store.ApprovalPending, p.ExpiresAt, p.Approvers, true, false))
	}
}

// writeApprovalUnavailable answers that no approval can come, and what to do: the reason is a short code the page words, and the command is
// the way in that needs no approval.
func writeApprovalUnavailable(w http.ResponseWriter, status int, reason string) {
	writeJSON(w, status, map[string]any{"error": "approval_unavailable", "reason": reason, "command": approvalCommand,
		"message": "the approval cannot be asked for in Telegram (" + reason + "); on the server: " + approvalCommand})
}

// pollPending is POST /api/v1/auth/pending: how the sign-in this browser waits for stands. It makes the session when the sign-in was
// approved (once), and clears the pending cookie when the sign-in is over, whichever way.
func (s *Server) pollPending(w http.ResponseWriter, r *http.Request) {
	if !s.polls.allow("poll:"+clientKey(r), s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "polling too fast; wait a moment")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin", "cross-site request refused")
		return
	}
	c, err := r.Cookie(pendingCookie)
	if err != nil || c.Value == "" {
		writeJSON(w, http.StatusOK, map[string]any{"status": domain.PollNone})
		return
	}
	ctx := r.Context()
	res, err := s.cfg.Domain.PollLoginApproval(ctx, webauth.HashToken(c.Value))
	if err != nil {
		s.internal(w, "poll a sign-in approval", err)
		return
	}
	switch res.Status {
	case store.ApprovalPending:
		writeJSON(w, http.StatusOK, pendingBody(res.Status, res.ExpiresAt, res.Approvers, res.Delivered, res.Failed))
	case store.ApprovalApproved:
		// The sign-in is spent: the session is made now or never. An account that lost its access meanwhile gets nothing.
		clearPendingCookie(w)
		user, err := s.cfg.Domain.StaffUser(ctx, res.Approval.UserID)
		if err != nil {
			writeError(w, http.StatusForbidden, "forbidden", "this account may not use the dashboard")
			return
		}
		st, ok := s.openSession(w, r, user, res.Approval.Method, res.Approval)
		if !ok {
			return
		}
		out := s.meBody(r.WithContext(context.WithValue(ctx, staffKey{}, st)))
		out["status"] = store.ApprovalApproved
		writeJSON(w, http.StatusOK, out)
	default:
		clearPendingCookie(w)
		writeJSON(w, http.StatusOK, map[string]any{"status": res.Status, "command": approvalCommand})
	}
}

// cancelPending is DELETE /api/v1/auth/pending: the person at the browser gives up waiting.
func (s *Server) cancelPending(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin", "cross-site request refused")
		return
	}
	if c, err := r.Cookie(pendingCookie); err == nil && c.Value != "" {
		if _, err := s.cfg.Domain.CancelLoginApproval(r.Context(), webauth.HashToken(c.Value)); err != nil {
			s.internal(w, "cancel a sign-in approval", err)
			return
		}
	}
	clearPendingCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// securityJSON is the state of the settings of Settings > Sign-in security.
func (s *Server) securityJSON(ctx context.Context, role string) (map[string]any, error) {
	st, err := s.cfg.Domain.LoginApprovalState(ctx)
	if err != nil {
		return nil, err
	}
	waiting, err := s.cfg.Domain.WaitingLoginApprovals(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"login_approval": map[string]any{"enabled": st.Enabled, "notify_owner": st.NotifyOwner, "ttl_s": int(domain.LoginApprovalTTL / time.Second),
			"bot_ready": s.cfg.Domain.LoginApprovalReady(), "waiting": waiting, "command": approvalCommand},
		"can_change": Allowed(role, PermSecurityWrite),
	}, nil
}

// getSecurity is GET /api/v1/security: how staff sign in. Owner and admin read it; the owner changes it.
func (s *Server) getSecurity(w http.ResponseWriter, r *http.Request) {
	out, err := s.securityJSON(r.Context(), staffFrom(r.Context()).user.Role)
	if err != nil {
		s.internal(w, "security settings", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// putLoginApproval is PUT /api/v1/security/login-approval: the owner turns the approval of sign-ins on or off. It needs the owner's current
// authenticator code where they have one, and, to turn it on, their word that they know what it does (the page shows it). The bot must be
// able to message them, or it stays off: a switch nobody can answer would lock the dashboard.
func (s *Server) putLoginApproval(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled     *bool  `json:"enabled"`
		NotifyOwner *bool  `json:"notify_owner"`
		Acknowledge bool   `json:"acknowledge"`
		Code        string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		writeFieldError(w, "enabled", "true or false")
		return
	}
	if !s.reauth(w, r, in.Code) {
		return
	}
	ctx, st := r.Context(), staffFrom(r.Context())
	cur, err := s.cfg.Domain.LoginApprovalState(ctx)
	if err != nil {
		s.internal(w, "security settings", err)
		return
	}
	if *in.Enabled && !cur.Enabled && !in.Acknowledge {
		writeError(w, http.StatusBadRequest, "acknowledge_required", "turning on the approval of sign-ins needs your confirmation that you read what it does")
		return
	}
	err = s.cfg.Domain.SetLoginApproval(ctx, st.user, "dashboard", *in.Enabled, in.NotifyOwner)
	var und *domain.UndeliverableError
	switch {
	case errors.As(err, &und):
		// Nothing was changed: the owner could not be told.
		writeApprovalUnavailable(w, http.StatusConflict, und.Reason)
		return
	case err != nil:
		s.fail(w, "security settings", err)
		return
	}
	s.getSecurity(w, r)
}
