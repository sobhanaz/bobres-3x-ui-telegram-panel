// Package web is the dashboard's JSON API (/api/v1) and the dashboard itself
// (/admin), served by core behind Caddy.
//
// Security model: a session is a random cookie value (only its hash is
// stored) in a __Host- cookie (Secure, HttpOnly, SameSite=Strict), ending
// after SessionTTL or SessionIdle without use, and re-checked against the
// user's role on every request. Every request that changes something carries
// the session's CSRF token in X-CSRF-Token; logins check the request's
// origin. Permissions come from the staff member's role (perms.go).
package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
	bcrypto "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/crypto"
)

// Session limits.
const (
	SessionTTL  = 12 * time.Hour
	SessionIdle = 2 * time.Hour
	cookieName  = "__Host-bobres_session"
	csrfHeader  = "X-CSRF-Token"
	maxBody     = 1 << 20
)

// Config wires the API.
type Config struct {
	Store  *store.Store
	Domain *domain.Service
	// Payments and Provisioner feed the overview and the sales pages.
	Payments    domain.PaymentsClient
	Provisioner domain.Provisioner
	// Files fetches receipt photos through the bot (nil: not shown).
	Files ReceiptFiles
	// Secrets encrypts authenticator secrets; nil disables password logins.
	Secrets *bcrypto.Envelope
	// Dashboard serves the built app under /admin/ (nil: not mounted).
	Dashboard http.Handler
	Log       *slog.Logger
}

// Server is the dashboard API.
type Server struct {
	cfg     Config
	limiter *limiter
	now     func() time.Time
}

// New builds the API.
func New(cfg Config) *Server {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Server{cfg: cfg, limiter: newLimiter(20, time.Minute), now: time.Now}
}

// Register mounts the API on /api/v1/ and the dashboard on /admin/.
func (s *Server) Register(mux *http.ServeMux) {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/v1/auth/link", s.loginWithLink)
	api.HandleFunc("POST /api/v1/auth/login", s.loginWithPassword)
	api.HandleFunc("POST /api/v1/auth/logout", s.session(s.logout))
	api.HandleFunc("GET /api/v1/me", s.session(s.me))
	api.HandleFunc("POST /api/v1/me/password", s.session(s.startPassword))
	api.HandleFunc("POST /api/v1/me/password/confirm", s.session(s.confirmPassword))
	api.HandleFunc("DELETE /api/v1/me/password", s.session(s.removePassword))
	api.HandleFunc("GET /api/v1/overview", s.session(s.require(PermOverviewRead, s.overview)))

	// Customers.
	s.route(api, "GET /api/v1/users", PermUsersRead, s.listUsers)
	s.route(api, "GET /api/v1/users/{id}", PermUsersRead, s.getUser)
	s.route(api, "POST /api/v1/users/{id}/status", PermUsersWrite, s.setUserStatus)
	s.route(api, "POST /api/v1/users/{id}/balance", PermWalletAdjust, s.adjustBalance)
	s.route(api, "GET /api/v1/services", PermServicesRead, s.listServices)
	s.route(api, "GET /api/v1/services/{id}", PermServicesRead, s.getService)
	s.route(api, "POST /api/v1/services/{id}/extend", PermServicesExtend, s.extendService)
	s.route(api, "POST /api/v1/services/{id}/sync", PermServicesExtend, s.syncService)
	s.route(api, "POST /api/v1/services/{id}/reset-traffic", PermServicesWrite, s.resetService)
	s.route(api, "POST /api/v1/services/{id}/enabled", PermServicesWrite, s.setServiceEnabled)
	s.route(api, "POST /api/v1/services/{id}/delete", PermServicesWrite, s.deleteService)

	// Sales.
	s.route(api, "GET /api/v1/plans", PermPlansWrite, s.listPlans)
	s.route(api, "POST /api/v1/plans", PermPlansWrite, s.createPlan)
	s.route(api, "PUT /api/v1/plans/{id}", PermPlansWrite, s.updatePlan)
	s.route(api, "GET /api/v1/orders", PermPaymentsRead, s.listOrders)
	s.route(api, "GET /api/v1/orders/{id}", PermPaymentsRead, s.getOrder)
	s.route(api, "POST /api/v1/orders/{id}/retry", PermPaymentsReview, s.retryOrder)
	s.route(api, "POST /api/v1/orders/{id}/refund", PermPaymentsReview, s.refundOrder)
	s.route(api, "GET /api/v1/payments", PermPaymentsRead, s.listPayments)
	s.route(api, "GET /api/v1/payments/pending", PermPaymentsRead, s.pendingPayments)
	s.route(api, "GET /api/v1/payments/{id}/receipt", PermPaymentsRead, s.paymentReceipt)
	s.route(api, "POST /api/v1/payments/{id}/review", PermPaymentsReview, s.reviewPayment)
	s.route(api, "GET /api/v1/ledger", PermLedgerRead, s.listLedger)
	s.route(api, "GET /api/v1/ledger.csv", PermLedgerRead, s.exportLedger)
	s.route(api, "GET /api/v1/discounts", PermDiscountsWrite, s.listDiscounts)
	s.route(api, "POST /api/v1/discounts", PermDiscountsWrite, s.saveDiscount)
	s.route(api, "PUT /api/v1/discounts/{code}/enabled", PermDiscountsWrite, s.setDiscountEnabled)
	s.route(api, "GET /api/v1/referrals", PermDiscountsWrite, s.getReferrals)
	s.route(api, "PUT /api/v1/referrals", PermDiscountsWrite, s.setReferralReward)

	// Administration.
	s.route(api, "GET /api/v1/audit", PermAuditRead, s.listAudit)
	s.route(api, "GET /api/v1/audit/actions", PermAuditRead, s.auditActions)
	s.route(api, "GET /api/v1/audit.csv", PermAuditRead, s.exportAudit)
	api.HandleFunc("/api/v1/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	mux.Handle("/api/v1/", apiHeaders(api))
	if s.cfg.Dashboard != nil {
		mux.Handle("/admin/", s.cfg.Dashboard)
		mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
		})
	}
}

// route mounts a staff endpoint: a live session, then the permission.
func (s *Server) route(mux *http.ServeMux, pattern, perm string, h http.HandlerFunc) {
	mux.HandleFunc(pattern, s.session(s.require(perm, h)))
}

func apiHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		// Audit entries written for this request record where it came from.
		next.ServeHTTP(w, r.WithContext(store.WithClientIP(r.Context(), clientIP(r))))
	})
}

// staffKey carries the request's staff member and session.
type staffKey struct{}

type staff struct {
	user    *store.User
	session *store.WebSession
	idHash  []byte
}

func staffFrom(ctx context.Context) *staff { s, _ := ctx.Value(staffKey{}).(*staff); return s }

// session requires a live session of an active staff member; requests that
// change something must also carry the session's CSRF token.
func (s *Server) session(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "log in first")
			return
		}
		hash := webauth.HashToken(c.Value)
		sess, err := s.cfg.Store.ActiveSession(r.Context(), s.cfg.Store.Conn(), hash, SessionIdle)
		if errors.Is(err, store.ErrNotFound) {
			clearCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized", "the session ended; log in again")
			return
		}
		if err != nil {
			s.internal(w, "load session", err)
			return
		}
		user, err := s.cfg.Domain.StaffUser(r.Context(), sess.UserID)
		if err != nil { // demoted or banned since the login
			_ = s.cfg.Store.RevokeSession(r.Context(), s.cfg.Store.Conn(), hash)
			clearCookie(w)
			writeError(w, http.StatusUnauthorized, "unauthorized", "this account may no longer use the dashboard")
			return
		}
		if !safeMethod(r.Method) {
			got := r.Header.Get(csrfHeader)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
				writeError(w, http.StatusForbidden, "csrf", "missing or wrong CSRF token")
				return
			}
		}
		if s.now().Sub(sess.LastSeenAt) > time.Minute {
			_ = s.cfg.Store.TouchSession(r.Context(), s.cfg.Store.Conn(), hash)
		}
		ctx := context.WithValue(r.Context(), staffKey{}, &staff{user: user, session: sess, idHash: hash})
		next(w, r.WithContext(ctx))
	}
}

// require checks a permission of the staff member's role.
func (s *Server) require(perm string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !Allowed(staffFrom(r.Context()).user.Role, perm) {
			writeError(w, http.StatusForbidden, "forbidden", "your role may not do this")
			return
		}
		next(w, r)
	}
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sameOrigin guards the login endpoints (no session, so no CSRF token yet):
// a browser request must come from this site. Non-browser clients send
// neither header.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
	default:
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	return true
}

// clientIP is the address Caddy saw (it overwrites X-Forwarded-For; core's
// port is not reachable from outside).
func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		ip, _, _ := strings.Cut(f, ",")
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": code, "message": msg})
}

func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.cfg.Log.Error("dashboard api: "+what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "something went wrong; try again")
}

// readJSON decodes a small JSON body strictly.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "the request body is not valid JSON for this endpoint")
		return false
	}
	return true
}

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
