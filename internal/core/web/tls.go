package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tlsconf"
)

// HTTPS (P8): the System page's HTTPS tab. The state for system.read (owner
// and admin); changing the mode and detecting the server's IP address for
// system.write (the owner). A private key is never answered, logged or
// audited.

// PublicIPSource detects the server's public IP address through the bot
// (botfiles.Client): core has no internet access.
type PublicIPSource interface {
	PublicIP(ctx context.Context) (string, error)
}

// getTLS: GET /api/v1/system/tls, the HTTPS state with a live status of
// every served address (domain.TLSView.JSON).
func (s *Server) getTLS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.TLS == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "HTTPS is not managed by this core")
		return
	}
	v, err := s.cfg.TLS.View(r.Context(), true)
	if err != nil {
		s.fail(w, "https", err)
		return
	}
	writeJSON(w, http.StatusOK, v.JSON())
}

// putTLS: PUT /api/v1/system/tls {mode, ip, cert, key, confirm_ip} applies
// a mode: auto; ip with the server's public IP address (one that differs
// from the detected address needs confirm_ip); manual with a certificate
// chain and its private key as PEM text (64 KiB each). It answers once
// Caddy took the change (within 20 seconds, after at most 3 seconds waiting
// for another change to finish, and 5 for the bot to detect the address):
// the new state as GET gives it but without the live status
// (status.addresses is empty and nothing is dialed: poll GET while the
// certificate is issued), with the pair's warnings [{code, message}] and
// pending (Caddy was not running: auto is written, and Caddy loads it when
// it starts). Errors carry a code the dashboard translates (tlsFail). At
// most 10 a minute.
func (s *Server) putTLS(w http.ResponseWriter, r *http.Request) {
	if s.cfg.TLS == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "HTTPS is not managed by this core")
		return
	}
	var in struct {
		Mode      string `json:"mode"`
		IP        string `json:"ip"`
		Cert      string `json:"cert"`
		Key       string `json:"key"`
		ConfirmIP bool   `json:"confirm_ip"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !s.probes.allow("tls:"+actor(r).ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 10 changes a minute")
		return
	}
	// Once asked, the change finishes even if the page goes away: the agent
	// may be applying it, and a refusal must still roll back.
	ctx := context.WithoutCancel(r.Context())
	c := domain.TLSChange{Mode: tlsconf.Mode(in.Mode), IP: in.IP, CertPEM: []byte(in.Cert), KeyPEM: []byte(in.Key), ConfirmIP: in.ConfirmIP}
	if c.Mode == tlsconf.ModeIP && !c.ConfirmIP {
		c.DetectedIP = s.detectedIP(ctx)
	}
	res, err := s.cfg.TLS.Set(ctx, actor(r), c)
	if err != nil {
		s.tlsFail(w, err)
		return
	}
	v, err := s.cfg.TLS.View(ctx, false)
	if err != nil {
		s.fail(w, "https", err)
		return
	}
	out := v.JSON()
	warnings := make([]map[string]string, 0, len(res.Warnings))
	for _, wn := range res.Warnings {
		warnings = append(warnings, map[string]string{"code": wn.Code, "message": wn.Message})
	}
	out["warnings"], out["pending"] = warnings, res.Pending
	writeJSON(w, http.StatusOK, out)
}

// detectedIP is the address the bot detected within 10 minutes (the
// Detect button), or one it detects within 5 seconds now ("" when it
// cannot: the change then needs a confirmation). The short wait keeps the
// answer within core's write timeout.
func (s *Server) detectedIP(ctx context.Context) string {
	if ip := s.cfg.TLS.DetectedIP(); ip != "" || s.cfg.PublicIP == nil {
		return ip
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ip, err := s.cfg.PublicIP.PublicIP(ctx)
	if err != nil {
		s.cfg.Log.Warn("https: public IP address not detected", "err", err)
		return ""
	}
	s.cfg.TLS.NoteDetectedIP(ip)
	return ip
}

// detectIP: POST /api/v1/system/tls/detect-ip asks the bot for this
// server's public IP address: {ip}; 502 {code: detect_failed} when no
// public address came back; 503 without a bot. At most 10 a minute.
func (s *Server) detectIP(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PublicIP == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "the bot is not set up, so the address cannot be detected")
		return
	}
	if !s.probes.allow("tls-detect:"+actor(r).ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 10 detections a minute")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ip, err := s.cfg.PublicIP.PublicIP(ctx)
	if err != nil {
		s.cfg.Log.Warn("https: public IP address not detected", "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "unavailable", "code": "detect_failed",
			"message": "the bot could not detect this server's public IP address"})
		return
	}
	if s.cfg.TLS != nil {
		s.cfg.TLS.NoteDetectedIP(ip)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ip": ip})
}

// tlsFail answers a change that was not made, with the code the dashboard
// translates (domain.TLSError):
//   - 400 invalid {field, code}: tlsconf's codes for what was sent (mode,
//     ip, cert, key);
//   - 409 confirm {code: ip_unconfirmed, detected_ip}: send it again with
//     confirm_ip;
//   - 409 state {code: no_master_key};
//   - 409 refused {code: caddy_refused | agent_failed}: message holds
//     Caddy's or the agent's words;
//   - 503 unavailable {code: agent_unavailable | agent_timeout |
//     caddy_unreachable | busy}: busy means another change is being applied,
//     try again in a moment.
func (s *Server) tlsFail(w http.ResponseWriter, err error) {
	var te *domain.TLSError
	if !errors.As(err, &te) {
		s.fail(w, "https", err)
		return
	}
	status, kind := http.StatusBadRequest, "invalid"
	body := map[string]any{"code": te.Code, "message": te.Message}
	switch te.Code {
	case domain.TLSErrIPUnconfirmed:
		status, kind = http.StatusConflict, "confirm"
		var detected any
		if te.DetectedIP != "" {
			detected = te.DetectedIP
		}
		body["detected_ip"] = detected
	case domain.TLSErrNoMasterKey:
		status, kind = http.StatusConflict, "state"
	case domain.TLSErrRefused, domain.TLSErrAgentFailed:
		status, kind = http.StatusConflict, "refused"
	case domain.TLSErrNoAgent, domain.TLSErrAgentTimeout, domain.TLSErrUnreachable, domain.TLSErrBusy:
		status, kind = http.StatusServiceUnavailable, "unavailable"
	}
	if te.Field != "" {
		body["field"] = te.Field
	}
	body["error"] = kind
	writeJSON(w, status, body)
}
