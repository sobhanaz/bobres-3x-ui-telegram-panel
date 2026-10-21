package runner

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

// The owner's own store resells a supplier's digital accounts (docs/supplier.md). Core has no internet access, so it
// asks the supplier through the bot, at POST /internal/supplier (core's service token): the bot calls the ONE supplier
// address it is configured for (BOBRES_SUPPLIER_URL, default the reseller API the owner uses), with the API key core
// brings for that call. The bot keeps and logs nothing of what passes, and core names an action, never an address,
// so it cannot make the bot call anywhere else.

// DefaultSupplierURL is the reseller API of the supplier the owner buys from.
const DefaultSupplierURL = "https://ssondigitalworks.online/api/reseller"

// SupplierRequest is core's body: the call (GET products or balance, POST order), the API key and the order's JSON.
type SupplierRequest struct {
	Action string `json:"action"`
	Key    string `json:"key"`
	Body   string `json:"body,omitempty"`
}

// SupplierProxy is the way to the supplier.
type SupplierProxy struct {
	// URL is BOBRES_SUPPLIER_URL (https, no query); "" answers 503 not_configured.
	URL string
	// Client calls it (nil: a 30-second client). It never follows a redirect.
	Client *http.Client
	// CoreToken is core's service token.
	CoreToken string
	Log       *slog.Logger
}

const (
	maxSupplierRequest = 16 << 10
	maxSupplierAnswer  = 1 << 20
)

var supplierKeyRe = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{8,300}$`)

// supplierMethods are the supplier's calls: the method each action takes.
var supplierMethods = map[string]string{"products": http.MethodGet, "balance": http.MethodGet, "order": http.MethodPost}

// Handler serves POST /internal/supplier. The supplier's answer passes through when it is JSON with a 2xx or 4xx status
// (its data, or its refusal); otherwise the bot answers {"error","message"} itself: 503 not_configured, 502 unreachable
// or 502 server_error.
func (p SupplierProxy) Handler() http.Handler {
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
		if p.URL == "" {
			licenseError(w, http.StatusServiceUnavailable, "not_configured", "no supplier is set (BOBRES_SUPPLIER_URL on the bot)")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSupplierRequest))
		var in SupplierRequest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err != nil || dec.Decode(&in) != nil {
			licenseError(w, http.StatusBadRequest, "bad_request", "the request is not a supplier call")
			return
		}
		method, known := supplierMethods[in.Action]
		if !known || !supplierKeyRe.MatchString(in.Key) || (method == http.MethodPost && !jsonObject([]byte(in.Body))) || (method == http.MethodGet && in.Body != "") {
			licenseError(w, http.StatusBadRequest, "bad_request", "the request is not a supplier call")
			return
		}
		base, err := url.Parse(p.URL)
		if err != nil || base.Scheme != "https" || base.Host == "" {
			licenseError(w, http.StatusServiceUnavailable, "not_configured", "the supplier address is not usable")
			return
		}
		q := base.Query()
		q.Set("action", in.Action)
		base.RawQuery = q.Encode()
		var body io.Reader
		if method == http.MethodPost {
			body = bytes.NewReader([]byte(in.Body))
		}
		req, err := http.NewRequestWithContext(r.Context(), method, base.String(), body)
		if err != nil {
			licenseError(w, http.StatusServiceUnavailable, "not_configured", "the supplier address is not usable")
			return
		}
		req.Header.Set("Authorization", "Bearer "+in.Key)
		req.Header.Set("Accept", "application/json")
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := hc.Do(req)
		if err != nil {
			// The error names the host, never the key (the key is in a header, which an error does not carry).
			log.Warn("the supplier did not answer", "action", in.Action)
			licenseError(w, http.StatusBadGateway, "unreachable", "the supplier did not answer")
			return
		}
		defer func() { _ = resp.Body.Close() }()
		answer, err := io.ReadAll(io.LimitReader(resp.Body, maxSupplierAnswer+1))
		ok := err == nil && len(answer) <= maxSupplierAnswer && json.Valid(answer) && (resp.StatusCode/100 == 2 || resp.StatusCode/100 == 4)
		if !ok {
			log.Warn("the supplier answered unusably", "action", in.Action, "status", resp.StatusCode)
			licenseError(w, http.StatusBadGateway, "server_error", "the supplier answered "+strconv.Itoa(resp.StatusCode))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(answer)
	})
}
