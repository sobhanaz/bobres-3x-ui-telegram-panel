package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// License calls: core has no internet access, so it activates and renews
// its license with the vendor's license server through the bot, at POST
// /internal/license/activate and /internal/license/refresh (core's service
// token). The bot passes core's JSON body to the server's /v1/activate or
// /v1/refresh and the server's answer back, and keeps nothing: neither the
// license key nor a token is stored or logged.

// LicenseProxy is the way to the license server.
type LicenseProxy struct {
	// Server is BOBRES_LICENSE_SERVER (https, no trailing slash); "" answers
	// 503 not_configured.
	Server string
	// Client calls it (nil: a 30-second client). It never follows a
	// redirect.
	Client *http.Client
	// CoreToken is core's service token.
	CoreToken string
	Log       *slog.Logger
}

// License calls.
const (
	LicenseActivate = "activate"
	LicenseRefresh  = "refresh"
)

// Limits on what passes through: core's body (a key, or a token of at most
// 16 KiB, with the install's id, domain and version) and the server's
// answer.
const (
	maxLicenseRequest = 32 << 10
	maxLicenseAnswer  = 64 << 10
)

// licenseClient follows no redirects: the license server answers itself
// (and an https URL never turns into a plain one).
func licenseClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: noRedirect}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// Handler serves POST /internal/license/<call> (LicenseActivate or
// LicenseRefresh). The server's answer passes through when it is JSON with
// a 2xx or 4xx status (its token, or its refusal: {"error","message"}, with
// Retry-After on a 429). Otherwise the bot answers {"error","message"}
// itself: 503 not_configured, 502 unreachable (no answer) or 502
// server_error (a 5xx, a redirect, or something unreadable).
func (p LicenseProxy) Handler(call string) http.Handler {
	log := p.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	hc := licenseClient()
	if p.Client != nil {
		c := *p.Client
		c.CheckRedirect = noRedirect
		hc = &c
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, p.CoreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if p.Server == "" {
			licenseError(w, http.StatusServiceUnavailable, "not_configured",
				"no license server is set (BOBRES_LICENSE_SERVER on the bot)")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLicenseRequest))
		if err != nil || !jsonObject(body) {
			licenseError(w, http.StatusBadRequest, "bad_request", "the request is not a small JSON object")
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.Server+"/v1/"+call, bytes.NewReader(body))
		if err != nil {
			licenseError(w, http.StatusServiceUnavailable, "not_configured", "the license server address is not usable")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			// The error names the server, never the body.
			log.Warn("license server did not answer", "call", call, "err", err)
			licenseError(w, http.StatusBadGateway, "unreachable", "the license server did not answer")
			return
		}
		defer func() { _ = resp.Body.Close() }()
		answer, err := io.ReadAll(io.LimitReader(resp.Body, maxLicenseAnswer+1))
		ok := err == nil && len(answer) <= maxLicenseAnswer && jsonObject(answer) &&
			(resp.StatusCode/100 == 2 || resp.StatusCode/100 == 4)
		if !ok {
			log.Warn("license server answered unusably", "call", call, "status", resp.StatusCode)
			licenseError(w, http.StatusBadGateway, "server_error",
				"the license server answered "+strconv.Itoa(resp.StatusCode))
			return
		}
		if resp.StatusCode/100 == 4 {
			log.Info("license server refused", "call", call, "status", resp.StatusCode)
		}
		if ra := resp.Header.Get("Retry-After"); ra != "" && resp.StatusCode == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", ra)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(answer)
	})
}

// jsonObject: b is one JSON object.
func jsonObject(b []byte) bool {
	var v map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&v); err != nil || v == nil {
		return false
	}
	_, err := dec.Token()
	return errors.Is(err, io.EOF)
}

// licenseError is the bot's own answer, shaped as the license server's.
func licenseError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": msg})
}
