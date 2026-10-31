package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/hosted"
)

// The owner's own store reads its hosted stores' /metrics (docs/18-hosted-and-affiliate-plans.md, section 1). Core has no internet access, so
// the bot does it, at POST /internal/hosted/metrics (core's service token): core names a store's domain and brings the store's metrics token;
// the bot calls https://<domain>/metrics and nothing else. It is not a general fetcher: the scheme, the path and the port are fixed, the name
// must be a domain (not an address), redirects are not followed, and the connection is refused when the name resolves to an address that is
// not public (loopback, private, link-local, shared, multicast...), so core cannot be made to reach the bot's own network. The token is used
// for that one request; the bot keeps and logs nothing of it.

const (
	maxHostedRequest = 4 << 10
	maxHostedBody    = 1 << 20
	hostedFetchWait  = 15 * time.Second
)

// HostedMetricsRequest is core's body.
type HostedMetricsRequest struct {
	Domain string `json:"domain"`
	Token  string `json:"token"`
}

// HostedMetrics serves POST /internal/hosted/metrics. It answers 200 {"status": <the store's status>, "body": "<its text, on a 200>"} when the
// store answered, else {"error","message"}: 400 bad_request, 502 unreachable.
type HostedMetrics struct {
	CoreToken string
	Log       *slog.Logger
	// Client, when set, is used instead of the guarded one (tests: a local TLS server).
	Client *http.Client
}

// publicAddr reports whether an address may be reached for a hosted store: a global unicast address that is not private or shared.
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),   // shared address space (carrier NAT)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64 (could reach an IPv4 inside)
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

// errNotPublic: the store's name resolved to an address that is not public.
var errNotPublic = errors.New("the store's name resolves to an address that is not public")

// guardedClient is the client of hosted reads: every connection is checked after the name was resolved (so a name that points at a private
// address, or changes to one between two lookups, is refused at the socket), and redirects are not followed.
func guardedClient() *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !publicAddr(ap.Addr()) {
			return errNotPublic
		}
		return nil
	}}
	tr := &http.Transport{
		Proxy:                 nil, // never through a proxy: the address check is on the store itself
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: hostedFetchWait,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{Timeout: hostedFetchWait, Transport: tr, CheckRedirect: noRedirect}
}

// Handler serves the endpoint.
func (h HostedMetrics) Handler() http.Handler {
	log := h.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	hc := guardedClient()
	if h.Client != nil {
		c := *h.Client
		c.CheckRedirect = noRedirect
		hc = &c
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, h.CoreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHostedRequest))
		var in HostedMetricsRequest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err != nil || dec.Decode(&in) != nil || !hosted.ValidDomain(in.Domain) || !hosted.TokenRe.MatchString(in.Token) {
			licenseError(w, http.StatusBadRequest, "bad_request", "a store's domain and its metrics token are required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), hostedFetchWait)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+in.Domain+"/metrics", nil)
		if err != nil {
			licenseError(w, http.StatusBadRequest, "bad_request", "not a usable domain")
			return
		}
		req.Header.Set("Authorization", "Bearer "+in.Token)
		req.Header.Set("Accept", "text/plain")
		resp, err := hc.Do(req)
		if err != nil {
			msg := "the store did not answer"
			if errors.Is(err, errNotPublic) {
				msg = errNotPublic.Error()
			}
			log.Warn("hosted store did not answer", "domain", in.Domain, "err", err)
			licenseError(w, http.StatusBadGateway, "unreachable", msg)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		out := map[string]any{"status": resp.StatusCode}
		if resp.StatusCode == http.StatusOK {
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxHostedBody+1))
			if err != nil || len(body) > maxHostedBody {
				licenseError(w, http.StatusBadGateway, "unreachable", "the store's answer is too long or broken")
				return
			}
			out["body"] = string(body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}
