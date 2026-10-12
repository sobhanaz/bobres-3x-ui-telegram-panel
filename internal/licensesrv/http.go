package licensesrv

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// maxBody bounds a request body (a token is at most maxTokenLen).
const maxBody = 32 << 10

// APIOptions configure the HTTP API.
type APIOptions struct {
	// TrustedProxies are the reverse proxies whose X-Forwarded-For and
	// X-Forwarded-Proto are believed (DefaultTrustedProxies when nil).
	TrustedProxies []netip.Prefix
	// IPRate limits requests per client address (IPv6: per /64).
	IPRate Rate
	// AllowHTTP accepts requests that did not arrive over HTTPS (local
	// testing only): by default /v1 answers only what a trusted proxy
	// received over HTTPS.
	AllowHTTP bool
	Now       func() time.Time
	Log       *slog.Logger
}

// DefaultTrustedProxies is a proxy on the same host.
var DefaultTrustedProxies = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128"),
}

// API serves POST /v1/activate and POST /v1/refresh.
type API struct {
	svc       *Service
	ipLimit   *limiter
	trusted   []netip.Prefix
	allowHTTP bool
	log       *slog.Logger
}

// NewAPI builds the API around svc.
func NewAPI(svc *Service, o APIOptions) (*API, error) {
	if o.IPRate.Max <= 0 || o.IPRate.Window <= 0 {
		return nil, errors.New("licensesrv: the IP rate must be positive")
	}
	if o.TrustedProxies == nil {
		o.TrustedProxies = DefaultTrustedProxies
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &API{svc: svc, ipLimit: newLimiter(o.IPRate, o.Now), trusted: o.TrustedProxies, allowHTTP: o.AllowHTTP, log: o.Log}, nil
}

// Register mounts the API on mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/activate", a.activate)
	mux.HandleFunc("POST /v1/refresh", a.refresh)
}

func (a *API) activate(w http.ResponseWriter, r *http.Request) {
	var req ActivateRequest
	ip, ok := a.admit(w, r, &req)
	if !ok {
		return
	}
	token, err := a.svc.Activate(r.Context(), req, ip)
	a.answer(w, token, err)
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	ip, ok := a.admit(w, r, &req)
	if !ok {
		return
	}
	token, err := a.svc.Refresh(r.Context(), req, ip)
	a.answer(w, token, err)
}

// admit runs the checks every /v1 request passes: HTTPS, the per-address
// rate and a small JSON body. Unknown body fields are ignored, so newer
// installs can send more than this server reads.
func (a *API) admit(w http.ResponseWriter, r *http.Request, v any) (string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	peer := peerAddr(r)
	viaProxy := a.isTrusted(peer)
	ip := a.clientIP(r, peer, viaProxy)
	overHTTPS := r.TLS != nil || (viaProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
	if !overHTTPS && !a.allowHTTP {
		writeError(w, &Error{Status: http.StatusForbidden, Code: "https_required", Message: "use https"})
		return "", false
	}
	if ok, retry := a.ipLimit.allow("ip:" + ipKey(ip)); !ok {
		a.log.Warn("rate limited", "ip", ip, "path", r.URL.Path)
		writeError(w, rateLimited(retry))
		return "", false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, invalid("the request body is not valid JSON for this endpoint"))
		return "", false
	}
	return ip, true
}

func (a *API) answer(w http.ResponseWriter, token string, err error) {
	var e *Error
	switch {
	case errors.As(err, &e):
		writeError(w, e)
	case err != nil:
		a.log.Error("license request failed", "err", err)
		writeError(w, &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "something went wrong; try again later"})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"token": token})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, e *Error) {
	if e.Status == http.StatusTooManyRequests {
		secs := int(math.Ceil(e.RetryAfter.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	}
	writeJSON(w, e.Status, map[string]string{"error": e.Code, "message": e.Message})
}

// peerAddr is the address of whoever opened the connection.
func peerAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func (a *API) isTrusted(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	for _, p := range a.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// clientIP is the caller's address. Only a trusted proxy's X-Forwarded-For
// counts, read from the right: the first address that is not one of our
// proxies is the client (what is left of it, the client could have forged).
func (a *API) clientIP(r *http.Request, peer netip.Addr, viaProxy bool) string {
	if !viaProxy {
		if peer.IsValid() {
			return peer.String()
		}
		return r.RemoteAddr
	}
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		addr = addr.Unmap()
		if !a.isTrusted(addr) {
			return addr.String()
		}
	}
	return peer.String()
}
