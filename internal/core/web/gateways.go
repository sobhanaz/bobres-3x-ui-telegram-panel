package web

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// Payment gateways (milestone 6a): the owner only. Zarinpal's merchant id
// and proxy stay encrypted in the payments service and never come back; the
// manual methods and Telegram Stars are settings (the payments group), saved
// through PUT /api/v1/settings.

// gatewayTimeout: saving may wait for Zarinpal payments being created, and a
// test may wait for Zarinpal (payments gives each up to 25 seconds).
const gatewayTimeout = 40 * time.Second

// reportProviders are the payment methods a report covers.
var reportProviders = []string{domain.Zarinpal, domain.Stars, "manual_card", "manual_crypto", "manual_zarinpal"}

type gatewayHealthJSON struct {
	Healthy      bool   `json:"healthy"`
	Open         int64  `json:"open"`
	OldestOpenAt *int64 `json:"oldest_open_at"`
	LastPaidAt   *int64 `json:"last_paid_at"`
	LastErrorAt  *int64 `json:"last_error_at"`
	LastError    string `json:"last_error"`
}

type gatewayJSON struct {
	Provider       string             `json:"provider"`
	Enabled        bool               `json:"enabled"`
	Configured     bool               `json:"configured"`
	Offered        bool               `json:"offered"`
	Problem        string             `json:"problem"`
	Sandbox        bool               `json:"sandbox"`
	SandboxAllowed *bool              `json:"sandbox_allowed,omitempty"`
	MerchantHint   string             `json:"merchant_hint"`
	ProxySet       bool               `json:"proxy_set"`
	KeyMissing     bool               `json:"key_missing"`
	FromEnv        bool               `json:"from_env"`
	UpdatedAt      *int64             `json:"updated_at"`
	Health         *gatewayHealthJSON `json:"health"`
}

func unixOrNull(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

func healthOf(r *domain.GatewayReport, now time.Time) *gatewayHealthJSON {
	h := &gatewayHealthJSON{Healthy: r.Healthy(now), Open: r.OpenCount, OldestOpenAt: unixOrNull(r.OldestOpenAt)}
	if r.LastPaid != nil {
		h.LastPaidAt = unixOrNull(r.LastPaid.At)
	}
	if r.LastError != nil {
		h.LastErrorAt, h.LastError = unixOrNull(r.LastError.At), r.LastError.Detail
	}
	return h
}

func (s *Server) gatewayOf(c *domain.GatewayConfig, health *gatewayHealthJSON) gatewayJSON {
	out := gatewayJSON{Provider: c.Provider, Enabled: c.Enabled, Configured: c.Configured, Offered: c.Offered,
		Problem: c.Problem, Sandbox: c.Sandbox, MerchantHint: c.MerchantHint, ProxySet: c.ProxySet,
		KeyMissing: c.KeyMissing, FromEnv: c.FromEnv, UpdatedAt: unixOrNull(c.UpdatedAt), Health: health}
	if s.cfg.Env != "" {
		allowed := s.cfg.Env != "prod" // payments refuses the sandbox in production
		out.SandboxAllowed = &allowed
	}
	return out
}

// gateways answers 503 while core has no payments service to ask.
func (s *Server) gateways(w http.ResponseWriter) (domain.GatewayAdmin, bool) {
	if s.cfg.Gateways == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the payments service is not connected")
		return nil, false
	}
	return s.cfg.Gateways, true
}

// gatewayHealth is a gateway's health from its report, nil when payments
// cannot say.
func (s *Server) gatewayHealth(ctx context.Context, ga domain.GatewayAdmin, provider string) *gatewayHealthJSON {
	r, err := ga.GatewayReport(ctx, provider, 0, 0)
	if err != nil {
		s.cfg.Log.Warn("gateway report", "provider", provider, "err", err)
		return nil
	}
	return healthOf(r, s.now())
}

// paymentSettings are the payments group of the settings, in the Settings
// page's item shape.
func (s *Server) paymentSettings(ctx context.Context, role string) ([]settingJSON, error) {
	values, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		return nil, err
	}
	return settingItems(values, role, []string{"payments"}), nil
}

// listGateways: GET /api/v1/gateways, the automated gateways with their
// health and the payments settings (manual methods, Stars, their switches).
func (s *Server) listGateways(w http.ResponseWriter, r *http.Request) {
	ga, ok := s.gateways(w)
	if !ok {
		return
	}
	configs, err := ga.GatewayConfigs(r.Context())
	if err != nil {
		s.fail(w, "gateway configs", err)
		return
	}
	providers := make([]gatewayJSON, 0, len(configs))
	for i := range configs {
		providers = append(providers, s.gatewayOf(&configs[i], s.gatewayHealth(r.Context(), ga, configs[i].Provider)))
	}
	settings, err := s.paymentSettings(r.Context(), actor(r).Role)
	if err != nil {
		s.fail(w, "payment settings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers, "settings": settings})
}

// putGateway: PUT /api/v1/gateways/{provider} {enabled, sandbox, merchant_id,
// proxy, clear_proxy}; "" keeps a stored secret.
func (s *Server) putGateway(w http.ResponseWriter, r *http.Request) {
	ga, ok := s.gateways(w)
	if !ok {
		return
	}
	var b struct {
		Enabled    bool   `json:"enabled"`
		Sandbox    bool   `json:"sandbox"`
		MerchantID string `json:"merchant_id"`
		Proxy      string `json:"proxy"`
		ClearProxy bool   `json:"clear_proxy"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gatewayTimeout)
	defer cancel()
	c, err := s.cfg.Domain.SetGatewayConfig(ctx, actor(r), ga, domain.GatewayUpdate{Provider: r.PathValue("provider"),
		Enabled: b.Enabled, Sandbox: b.Sandbox, MerchantID: b.MerchantID, Proxy: b.Proxy, ClearProxy: b.ClearProxy})
	if err != nil {
		s.gatewayFail(w, "save gateway", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": s.gatewayOf(c, nil)})
}

// testGateway: POST /api/v1/gateways/{provider}/test with candidate settings
// ("" = the stored value), or {} for the stored settings. Nothing is saved; a
// gateway that refuses is 200 {ok:false, detail}.
func (s *Server) testGateway(w http.ResponseWriter, r *http.Request) {
	ga, ok := s.gateways(w)
	if !ok {
		return
	}
	var b struct {
		Sandbox    *bool  `json:"sandbox"`
		MerchantID string `json:"merchant_id"`
		Proxy      string `json:"proxy"`
		ClearProxy bool   `json:"clear_proxy"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if !s.probes.allow("gateway-test:"+actor(r).ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 10 connection tests a minute")
		return
	}
	var cand *domain.GatewayCandidate
	if b.Sandbox != nil || b.MerchantID != "" || b.Proxy != "" || b.ClearProxy {
		cand = &domain.GatewayCandidate{Sandbox: b.Sandbox != nil && *b.Sandbox, MerchantID: b.MerchantID, Proxy: b.Proxy,
			ClearProxy: b.ClearProxy}
	}
	ctx, cancel := context.WithTimeout(r.Context(), gatewayTimeout)
	defer cancel()
	res, err := s.cfg.Domain.TestGateway(ctx, ga, r.PathValue("provider"), cand)
	if err != nil {
		s.gatewayFail(w, "test gateway", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.OK, "detail": res.Detail, "unverified": res.Unverified,
		"latency_ms": res.LatencyMS})
}

// gatewayReport: GET /api/v1/gateways/report?provider=&from=&to= (unix
// seconds; 0 or missing: the last 30 days up to now).
func (s *Server) gatewayReport(w http.ResponseWriter, r *http.Request) {
	ga, ok := s.gateways(w)
	if !ok {
		return
	}
	q := r.URL.Query()
	provider := q.Get("provider")
	if provider == "" {
		provider = domain.Zarinpal
	}
	if !slices.Contains(reportProviders, provider) {
		writeFieldError(w, "provider", "one of "+strings.Join(reportProviders, ", "))
		return
	}
	var period [2]int64
	for i, k := range []string{"from", "to"} {
		if v := q.Get(k); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				writeFieldError(w, k, "a time in unix seconds")
				return
			}
			period[i] = n
		}
	}
	rep, err := ga.GatewayReport(r.Context(), provider, period[0], period[1])
	if err != nil {
		s.gatewayFail(w, "gateway report", err)
		return
	}
	totals := make([]map[string]any, 0, len(rep.Totals))
	for _, t := range rep.Totals {
		totals = append(totals, map[string]any{"status": t.Status, "count": t.Count, "amounts": moneyList(t.Amounts)})
	}
	h := healthOf(rep, s.now())
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": rep.Provider, "from": rep.From, "to": rep.To, "totals": totals,
		"healthy": h.Healthy, "open": h.Open, "oldest_open_at": h.OldestOpenAt,
		"last_paid_at": h.LastPaidAt, "last_error_at": h.LastErrorAt, "last_error": h.LastError,
	})
}

// openRe reads the count payments puts first when open payments block a
// change of the merchant id or the sandbox.
var openRe = regexp.MustCompile(`^(\d+) open `)

// gatewayFail answers payments' refusals with their reason (it never puts a
// secret in one), naming the field when it can; the rest goes to fail.
func (s *Server) gatewayFail(w http.ResponseWriter, what string, err error) {
	st, isStatus := status.FromError(unwrapAll(err))
	if isStatus {
		msg := strings.TrimPrefix(st.Message(), "payments: invalid argument: ")
		switch st.Code() {
		case codes.InvalidArgument:
			out := map[string]any{"error": "invalid", "message": msg}
			if f := gatewayField(msg); f != "" {
				out["field"] = f
			}
			writeJSON(w, http.StatusBadRequest, out)
			return
		case codes.FailedPrecondition:
			out := map[string]any{"error": "state", "message": msg}
			if m := openRe.FindStringSubmatch(msg); m != nil {
				if n, err := strconv.ParseInt(m[1], 10, 64); err == nil {
					out["open"] = n
				}
			}
			writeJSON(w, http.StatusConflict, out)
			return
		case codes.NotFound:
			writeError(w, http.StatusNotFound, "not_found", "no such gateway")
			return
		}
	}
	s.fail(w, what, err)
}

// gatewayField is the form field a payments refusal is about, "" when none.
func gatewayField(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "merchant"):
		return "merchant_id"
	case strings.Contains(m, "proxy"):
		return "proxy"
	case strings.Contains(m, "sandbox"):
		return "sandbox"
	}
	return ""
}
