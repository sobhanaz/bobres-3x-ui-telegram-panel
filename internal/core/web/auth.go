package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
)

// lockFor is how long five failed password logins in a row lock an account.
const lockFor = 15 * time.Minute

var usernameRe = regexp.MustCompile(`^[a-z0-9_.-]{3,32}$`)

// dummyHash is checked when the username is unknown, so a wrong username
// takes as long as a wrong password.
var dummyHash = sync.OnceValue(func() string {
	h, _ := webauth.HashPassword("not a real password, never matches")
	return h
})

// loginWithLink exchanges a one-time link token (from the bot or the server
// CLI) for a session.
func (s *Server) loginWithLink(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("auth:"+clientIP(r), s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; wait a minute")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin", "cross-site login refused")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	userID, err := s.cfg.Store.ConsumeLoginLink(r.Context(), s.cfg.Store.Conn(), webauth.HashToken(strings.TrimSpace(in.Token)))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "link_invalid", "this login link was used already or has expired; ask the bot for a new one")
		return
	}
	if err != nil {
		s.internal(w, "consume login link", err)
		return
	}
	s.startSession(w, r, userID, "link")
}

// loginWithPassword checks username, password and authenticator code
// together, so a failure never tells which one was wrong.
func (s *Server) loginWithPassword(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("auth:"+clientIP(r), s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; wait a minute")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin", "cross-site login refused")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if s.cfg.Secrets == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "password logins are off on this install (no master key)")
		return
	}
	const wrong = "wrong username, password or code"
	ctx, conn := r.Context(), s.cfg.Store.Conn()
	creds, err := s.cfg.Store.CredentialsByUsername(ctx, conn, strings.ToLower(strings.TrimSpace(in.Username)))
	if errors.Is(err, store.ErrNotFound) {
		webauth.CheckPassword(dummyHash(), in.Password)
		writeError(w, http.StatusUnauthorized, "login_failed", wrong)
		return
	}
	if err != nil {
		s.internal(w, "load credentials", err)
		return
	}
	if creds.LockedUntil != nil && creds.LockedUntil.After(s.now()) {
		writeError(w, http.StatusLocked, "locked", "too many failed logins; try again in 15 minutes or use a link from the bot")
		return
	}
	passOK := webauth.CheckPassword(creds.PasswordHash, in.Password)
	step, codeOK := int64(0), false
	if secret, err := s.cfg.Secrets.Decrypt(creds.TOTPSecretEnc); err == nil && creds.TOTPConfirmedAt != nil {
		step, codeOK = webauth.CheckTOTP(string(secret), in.Code, s.now(), creds.TOTPLastStep)
	}
	if !passOK || !codeOK {
		locked, err := s.cfg.Store.RecordLoginFailure(ctx, conn, creds.UserID, lockFor)
		if err != nil {
			s.cfg.Log.Warn("record login failure", "err", err)
		}
		// Not the member's doing: the entry names the account, with no actor.
		s.writeAudit(ctx, nil, "dashboard.login_failed", creds.UserID, map[string]any{"method": "password", "locked": locked})
		writeError(w, http.StatusUnauthorized, "login_failed", wrong)
		return
	}
	if err := s.cfg.Store.RecordLoginSuccess(ctx, conn, creds.UserID, step); err != nil {
		s.internal(w, "record login", err)
		return
	}
	s.startSession(w, r, creds.UserID, "password")
}

// startSession opens a session for an active staff member.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID, method string) {
	ctx := r.Context()
	user, err := s.cfg.Domain.StaffUser(ctx, userID)
	if err != nil {
		writeError(w, http.StatusForbidden, "forbidden", "this account may not use the dashboard")
		return
	}
	cookie, hash, err := webauth.NewToken()
	if err != nil {
		s.internal(w, "new session", err)
		return
	}
	csrf, _, err := webauth.NewToken()
	if err != nil {
		s.internal(w, "new session", err)
		return
	}
	now := s.now()
	sess := &store.WebSession{IDHash: hash, UserID: user.ID, CSRFToken: csrf, Method: method,
		IP: clientIP(r), UserAgent: truncate(r.UserAgent(), 256), CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(SessionTTL)}
	if err := s.cfg.Store.CreateSession(ctx, s.cfg.Store.Conn(), sess); err != nil {
		s.internal(w, "create session", err)
		return
	}
	s.audit(ctx, user, "dashboard.login", map[string]string{"method": method, "ip": sess.IP})
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: cookie, Path: "/", MaxAge: int(SessionTTL.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	s.writeMe(w, r.WithContext(context.WithValue(ctx, staffKey{}, &staff{user: user, session: sess, idHash: hash})))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	st := staffFrom(r.Context())
	if err := s.cfg.Store.RevokeSession(r.Context(), s.cfg.Store.Conn(), st.idHash); err != nil {
		s.internal(w, "logout", err)
		return
	}
	s.audit(r.Context(), st.user, "dashboard.logout", nil)
	clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) { s.writeMe(w, r) }

// writeMe answers who is logged in, what they may do, and the CSRF token
// the dashboard sends back with every change.
func (s *Server) writeMe(w http.ResponseWriter, r *http.Request) {
	st := staffFrom(r.Context())
	u := st.user
	settings, err := s.cfg.Domain.Settings(r.Context())
	if err != nil {
		s.cfg.Log.Warn("settings for /me", "err", err)
	}
	var logo any
	if u := s.logoURL(r.Context()); u != "" {
		logo = u
	}
	password := map[string]any{"enabled": false, "reauth": false}
	if c, err := s.cfg.Store.CredentialsFor(r.Context(), s.cfg.Store.Conn(), u.ID); err == nil && c.TOTPConfirmedAt != nil {
		password = map[string]any{"enabled": true, "username": c.Username, "reauth": s.needsReauth(st, c)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{"id": u.ID, "telegram_id": u.TelegramID, "username": u.Username,
			"role": u.Role, "language": u.Language},
		"permissions":        Permissions(u.Role),
		"csrf":               st.session.CSRFToken,
		"session":            map[string]any{"method": st.session.Method, "expires_at": st.session.ExpiresAt.Unix()},
		"password":           password,
		"password_available": s.cfg.Secrets != nil,
		"brand":              brandName(settings),
		"branding": map[string]any{"color": settings["branding.color"], "logo": logo,
			"currency": langPair{settings["branding.currency.fa"], settings["branding.currency.en"]}},
	})
}

// startPassword sets a new password and authenticator secret for the staff
// member, unconfirmed until confirmPassword sees the first code.
func (s *Server) startPassword(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Secrets == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "password logins are off on this install (no master key)")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !s.reauth(w, r, in.Code) {
		return
	}
	username := strings.ToLower(strings.TrimSpace(in.Username))
	if !usernameRe.MatchString(username) {
		writeError(w, http.StatusBadRequest, "invalid", "the username is 3-32 lower-case letters, digits, '.', '_' or '-'")
		return
	}
	if err := webauth.ValidPassword(in.Password); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	hash, err := webauth.HashPassword(in.Password)
	if err != nil {
		s.internal(w, "hash password", err)
		return
	}
	secret, err := webauth.NewTOTPSecret()
	if err != nil {
		s.internal(w, "totp secret", err)
		return
	}
	enc, err := s.cfg.Secrets.Encrypt([]byte(secret))
	if err != nil {
		s.internal(w, "encrypt secret", err)
		return
	}
	st := staffFrom(r.Context())
	err = s.cfg.Store.SetPendingCredentials(r.Context(), s.cfg.Store.Conn(), st.user.ID, username, hash, enc)
	if errors.Is(err, store.ErrUsernameTaken) {
		writeError(w, http.StatusConflict, "username_taken", "another staff member uses this username")
		return
	}
	if err != nil {
		s.internal(w, "save credentials", err)
		return
	}
	// The issuer and the account are split on ':' in the authenticator app.
	otpURL := webauth.TOTPURL(strings.ReplaceAll(s.brand(r.Context()), ":", " "), username, secret)
	png, err := qrcode.Encode(otpURL, qrcode.Medium, 256)
	if err != nil {
		s.internal(w, "qr code", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"otpauth_url": otpURL, "secret": secret,
		"qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

// confirmPassword turns the password login on once the authenticator app
// produced a valid code; the staff member's other sessions end.
func (s *Server) confirmPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if s.cfg.Secrets == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "password logins are off on this install (no master key)")
		return
	}
	st := staffFrom(r.Context())
	ctx, conn := r.Context(), s.cfg.Store.Conn()
	creds, err := s.cfg.Store.CredentialsFor(ctx, conn, st.user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusBadRequest, "invalid", "set a password first")
		return
	}
	if err != nil {
		s.internal(w, "load credentials", err)
		return
	}
	secret, err := s.cfg.Secrets.Decrypt(creds.TOTPSecretEnc)
	if err != nil {
		s.internal(w, "decrypt secret", err)
		return
	}
	step, ok := webauth.CheckTOTP(string(secret), in.Code, s.now(), creds.TOTPLastStep)
	if !ok {
		writeError(w, http.StatusBadRequest, "code_wrong", "that code is not right; check the time on your phone and try the next code")
		return
	}
	if err := s.cfg.Store.ConfirmCredentials(ctx, conn, st.user.ID, step); err != nil {
		s.internal(w, "confirm credentials", err)
		return
	}
	s.revokeOtherSessions(ctx, st)
	s.audit(ctx, st.user, "dashboard.password.set", map[string]string{"username": creds.Username})
	s.writeMe(w, r)
}

func (s *Server) removePassword(w http.ResponseWriter, r *http.Request) {
	st := staffFrom(r.Context())
	var in struct {
		Code string `json:"code"`
	}
	if body, _ := io.ReadAll(r.Body); len(bytes.TrimSpace(body)) > 0 && json.Unmarshal(body, &in) != nil {
		writeError(w, http.StatusBadRequest, "invalid", "the request body is not valid JSON for this endpoint")
		return
	}
	if !s.reauth(w, r, in.Code) {
		return
	}
	if err := s.cfg.Store.DeleteCredentials(r.Context(), s.cfg.Store.Conn(), st.user.ID); err != nil {
		s.internal(w, "delete credentials", err)
		return
	}
	s.audit(r.Context(), st.user, "dashboard.password.removed", nil)
	s.writeMe(w, r)
}

// revokeOtherSessions ends every session of the staff member but this one.
func (s *Server) revokeOtherSessions(ctx context.Context, st *staff) {
	if _, err := s.cfg.Store.RevokeOtherSessions(ctx, s.cfg.Store.Conn(), st.user.ID, st.idHash, 0); err != nil {
		s.cfg.Log.Warn("revoke sessions", "err", err)
	}
}

// reauthWindow: a login link this recent is fresh proof of who is there
// (their Telegram account), so no authenticator code is asked again.
const reauthWindow = 10 * time.Minute

// needsReauth: replacing or removing a working password login needs the
// current authenticator code, so a stolen session cannot take it over.
func (s *Server) needsReauth(st *staff, c *store.StaffCredentials) bool {
	if c == nil || c.TOTPConfirmedAt == nil {
		return false
	}
	return st.session.Method != "link" || s.now().Sub(st.session.CreatedAt) > reauthWindow
}

// reauth checks the current code when needsReauth; false after answering.
// Wrong codes count towards the password login's lock.
func (s *Server) reauth(w http.ResponseWriter, r *http.Request, code string) bool {
	st := staffFrom(r.Context())
	ctx, conn := r.Context(), s.cfg.Store.Conn()
	creds, err := s.cfg.Store.CredentialsFor(ctx, conn, st.user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return true
	}
	if err != nil {
		s.internal(w, "load credentials", err)
		return false
	}
	if !s.needsReauth(st, creds) {
		return true
	}
	if s.cfg.Secrets == nil {
		// Without the master key password logins are off and no code can be
		// checked: the leftover login can only be removed (startPassword
		// already refuses to set one).
		return true
	}
	if creds.LockedUntil != nil && creds.LockedUntil.After(s.now()) {
		writeError(w, http.StatusLocked, "locked", "too many wrong codes; try again in 15 minutes or use a fresh link from the bot")
		return false
	}
	if strings.TrimSpace(code) == "" {
		writeError(w, http.StatusForbidden, "code_required", "enter the current code from your authenticator app")
		return false
	}
	secret, err := s.cfg.Secrets.Decrypt(creds.TOTPSecretEnc)
	if err != nil {
		s.internal(w, "decrypt secret", err)
		return false
	}
	step, ok := webauth.CheckTOTP(string(secret), code, s.now(), creds.TOTPLastStep)
	if !ok {
		if _, err := s.cfg.Store.RecordLoginFailure(ctx, conn, st.user.ID, lockFor); err != nil {
			s.cfg.Log.Warn("record login failure", "err", err)
		}
		writeError(w, http.StatusBadRequest, "code_wrong", "that code is not right; check the time on your phone and try the next code")
		return false
	}
	if err := s.cfg.Store.RecordLoginSuccess(ctx, conn, st.user.ID, step); err != nil {
		s.internal(w, "record code", err)
		return false
	}
	return true
}

// audit records a staff member's own dashboard action.
func (s *Server) audit(ctx context.Context, actor *store.User, action string, detail any) {
	s.writeAudit(ctx, actor, action, actor.ID, detail)
}

// writeAudit records a dashboard event about a staff account (entityID); a
// nil actor means nobody logged in did it (a failed login).
func (s *Server) writeAudit(ctx context.Context, actor *store.User, action, entityID string, detail any) {
	a := &store.Audit{Action: action, Entity: "dashboard", EntityID: &entityID}
	if detail != nil {
		a.After, _ = json.Marshal(detail)
	}
	if actor != nil {
		a.ActorID = &actor.ID
	}
	if err := s.cfg.Store.WriteAudit(ctx, s.cfg.Store.Conn(), a); err != nil {
		s.cfg.Log.Warn("audit", "action", action, "err", err)
	}
}

// brand is the store's name (branding.name), shown in the dashboard and as
// the authenticator app's issuer.
func (s *Server) brand(ctx context.Context) string {
	m, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		return defaultBrand
	}
	return brandName(m)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return string(bytes.ToValidUTF8([]byte(s[:n]), nil))
}
