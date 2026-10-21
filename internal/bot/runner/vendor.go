package runner

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
)

// The owner's own store sells BOBRES licenses by asking the license server's signed admin API for keys. Core has no
// internet access, so the bot passes core's already-signed request on, at POST /internal/vendor/admin (core's service
// token), to the ONE license server the bot is configured for (BOBRES_LICENSE_SERVER): core names a path and brings
// the signature, never an address, so it cannot make the bot call anywhere else. The bot keeps and logs nothing of
// what passes: the admin key never reaches it (core signs), and a license key in an answer is only passed back.

// VendorAdminRequest is core's body: the endpoint (one of the admin API's), the three signature values and the
// request's body as the text that was signed.
type VendorAdminRequest struct {
	Path  string `json:"path"`
	TS    string `json:"ts"`
	Nonce string `json:"nonce"`
	Sig   string `json:"sig"`
	Body  string `json:"body"`
}

const (
	maxVendorRequest = 24 << 10
	maxVendorAnswer  = 1 << 20
)

var (
	vendorPathRe  = regexp.MustCompile(`^/admin/v1/licenses/(create|extend|tier|revoke|rekey|get|list)$`)
	vendorTSRe    = regexp.MustCompile(`^[0-9]{9,12}$`)
	vendorNonceRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
	vendorSigRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{80,100}$`)
)

// VendorAdminHandler serves POST /internal/vendor/admin. The server's answer passes through when it is JSON with a
// 2xx or 4xx status (a license, or its refusal); otherwise the bot answers {"error","message"} itself, as the license
// proxy does: 503 not_configured, 502 unreachable or 502 server_error.
func (p LicenseProxy) VendorAdminHandler() http.Handler {
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
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxVendorRequest))
		var in VendorAdminRequest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err != nil || dec.Decode(&in) != nil || !vendorPathRe.MatchString(in.Path) || !vendorTSRe.MatchString(in.TS) ||
			!vendorNonceRe.MatchString(in.Nonce) || !vendorSigRe.MatchString(in.Sig) || len(in.Body) > 16<<10 || !jsonObject([]byte(in.Body)) {
			licenseError(w, http.StatusBadRequest, "bad_request", "the request is not a signed admin call")
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.Server+in.Path, bytes.NewReader([]byte(in.Body)))
		if err != nil {
			licenseError(w, http.StatusServiceUnavailable, "not_configured", "the license server address is not usable")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Bobres-Ts", in.TS)
		req.Header.Set("X-Bobres-Nonce", in.Nonce)
		req.Header.Set("X-Bobres-Sig", in.Sig)
		resp, err := hc.Do(req)
		if err != nil {
			log.Warn("license server did not answer an admin call", "path", in.Path, "err", err)
			licenseError(w, http.StatusBadGateway, "unreachable", "the license server did not answer")
			return
		}
		defer func() { _ = resp.Body.Close() }()
		answer, err := io.ReadAll(io.LimitReader(resp.Body, maxVendorAnswer+1))
		ok := err == nil && len(answer) <= maxVendorAnswer && jsonObject(answer) &&
			(resp.StatusCode/100 == 2 || resp.StatusCode/100 == 4)
		if !ok {
			log.Warn("license server answered an admin call unusably", "path", in.Path, "status", resp.StatusCode)
			licenseError(w, http.StatusBadGateway, "server_error", "the license server answered "+strconv.Itoa(resp.StatusCode))
			return
		}
		if ra := resp.Header.Get("Retry-After"); ra != "" && resp.StatusCode == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", ra)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(answer)
	})
}
