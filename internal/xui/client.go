// Package xui is a typed, stdlib-only client for the 3x-ui panel HTTP API.
//
// Field names, units and request shapes were verified against 3x-ui v3.8.5
// source (internal/web/controller/client.go, internal/database/model/model.go):
//   - expiryTime is unix MILLISECONDS; 0 means unlimited.
//   - totalGB is a byte count despite its name; 0 means unlimited.
//   - clients/add takes {"client": {...}, "inboundIds": [...]}.
//
// Only the provisioner service may hold the panel API token and use this package.
package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxResponseBytes = 8 << 20

// APIError is returned when the panel rejects a call. Msg comes from the
// panel's own envelope; the request token is never included.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("xui: panel error (http %d): %s", e.Status, e.Msg)
}

// ErrInvalidIdentifier is returned for emails/sub IDs that could alter the request path.
var ErrInvalidIdentifier = errors.New("xui: invalid identifier")

// ErrInsecureURL is returned when the panel URL is not https (or not allowed).
var ErrInsecureURL = errors.New("xui: panel URL must use https (plain http only for a private panel, with allow-private)")

// ErrPublicPlaintext is returned when a plain-http panel URL resolves to a
// public address: the API token must never cross the internet unencrypted.
var ErrPublicPlaintext = errors.New("xui: plain http is allowed only to private, loopback or link-local addresses; use https")

// ErrBlockedAddress is returned when the panel resolves to a private/loopback address.
var ErrBlockedAddress = errors.New("xui: panel address is not allowed (private, loopback or link-local)")

const maxMsgLen = 200

// sanitizeMsg bounds and cleans panel-controlled text before it can reach logs or users.
func sanitizeMsg(m string) string {
	m = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, strings.TrimSpace(m))
	if len(m) > maxMsgLen {
		m = m[:maxMsgLen] + "..."
	}
	return m
}

var identRe = regexp.MustCompile(`^[A-Za-z0-9._@+\-]{1,128}$`)

// ValidateIdentifier reports whether s can be used as a client email or
// subscription id (it ends up in request paths).
func ValidateIdentifier(s string) error { return validIdent(s) }

func validIdent(s string) error {
	if s == "." || s == ".." || !identRe.MatchString(s) {
		return ErrInvalidIdentifier
	}
	return nil
}

// IsUnauthorized reports whether err means the API token was rejected.
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden)
}

// ClientSpec is the panel's client object (subset we manage).
type ClientSpec struct {
	ID         string `json:"id,omitempty"` // UUID for vless/vmess
	Email      string `json:"email"`
	SubID      string `json:"subId,omitempty"`
	TotalGB    int64  `json:"totalGB"`    // bytes, 0 = unlimited
	ExpiryTime int64  `json:"expiryTime"` // unix ms, 0 = unlimited
	LimitIP    int    `json:"limitIp"`
	Enable     bool   `json:"enable"`
	TgID       int64  `json:"tgId,omitempty"`
	Comment    string `json:"comment,omitempty"`
	Flow       string `json:"flow,omitempty"`
}

// ClientRecord is a client as the panel stores it (clients/get). Unlike
// ClientSpec, "id" is the panel's row id (a number in v3.8.5 and v3.9.0) and the
// protocol UUID is in "uuid". ID stays raw: nothing reads it, and a type change
// there must not break decoding again.
type ClientRecord struct {
	ID         json.RawMessage `json:"id,omitempty"`
	UUID       string          `json:"uuid"`
	Email      string          `json:"email"`
	SubID      string          `json:"subId"`
	TotalGB    int64           `json:"totalGB"`
	ExpiryTime int64           `json:"expiryTime"`
	LimitIP    int             `json:"limitIp"`
	Enable     bool            `json:"enable"`
	TgID       int64           `json:"tgId"`
	Comment    string          `json:"comment"`
	Flow       string          `json:"flow"`
}

// ClientDetail is what clients/get returns.
type ClientDetail struct {
	Client      ClientRecord `json:"client"`
	InboundIDs  []int        `json:"inboundIds"`
	UsedTraffic int64        `json:"usedTraffic"`
}

// Traffic is the per-client usage record.
type Traffic struct {
	Email      string `json:"email"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	Total      int64  `json:"total"`
	ExpiryTime int64  `json:"expiryTime"`
	Enable     bool   `json:"enable"`
	LastOnline int64  `json:"lastOnline"`
}

// Used returns upload + download bytes.
func (t Traffic) Used() int64 { return t.Up + t.Down }

// InboundOption is one entry of inbounds/options.
type InboundOption struct {
	ID       int    `json:"id"`
	Remark   string `json:"remark"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	Enable   bool   `json:"enable"`
}

// AdjustSkip explains why bulkAdjust skipped a client (e.g. unlimited field).
type AdjustSkip struct {
	Email  string `json:"email"`
	Reason string `json:"reason"`
}

// AdjustResult is the bulkAdjust outcome.
type AdjustResult struct {
	Adjusted int          `json:"adjusted"`
	Skipped  []AdjustSkip `json:"skipped,omitempty"`
}

// Client talks to one 3x-ui panel.
type Client struct {
	base  string
	token string
	hc    *http.Client

	mu    sync.Mutex
	locks map[string]*emailLock // refcounted, removed when idle
}

type emailLock struct {
	mu   sync.Mutex
	refs int
}

// Option customizes a Client.
type Option func(*settings)

type settings struct {
	hc            *http.Client
	allowInsecure bool
	allowPrivate  bool
}

// WithHTTPClient replaces the default HTTP client (tests, custom TLS). Redirects are
// still refused. The caller owns dial-time address filtering when using this, so it
// is refused for a plain-http panel URL, whose private-only check lives in the
// default transport.
func WithHTTPClient(hc *http.Client) Option { return func(o *settings) { o.hc = hc } }

// AllowInsecureHTTP permits http:// panel URLs. Only for tests/dev: the bearer token
// would cross the network in cleartext.
func AllowInsecureHTTP() Option { return func(o *settings) { o.allowInsecure = true } }

// AllowPrivateAddresses permits panels on loopback/private ranges (a panel on the same
// host or LAN is a legitimate setup that the operator must opt into). It also permits a
// plain-http panel URL, which may then reach ONLY private addresses.
func AllowPrivateAddresses() Option { return func(o *settings) { o.allowPrivate = true } }

// New creates a client. baseURL is the panel root including any web base path. It
// must be https, except that AllowPrivateAddresses also permits plain http, which
// may then reach only loopback, private and link-local addresses (AllowInsecureHTTP
// lifts that for tests). Unless AllowPrivateAddresses is given, connections to
// loopback, private and link-local addresses are refused. Both checks run at dial
// time, after DNS resolution, so DNS rebinding cannot bypass them.
func New(baseURL, apiToken string, opts ...Option) (*Client, error) {
	var o settings
	for _, f := range opts {
		f(&o)
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("xui: invalid panel URL")
	}
	plaintext := u.Scheme == "http"
	if plaintext && !o.allowInsecure && (!o.allowPrivate || o.hc != nil) {
		return nil, ErrInsecureURL
	}
	if u.User != nil {
		return nil, errors.New("xui: credentials in the panel URL are not allowed")
	}
	if apiToken == "" {
		return nil, errors.New("xui: API token is required")
	}
	hc := o.hc
	if hc == nil {
		tr := &http.Transport{
			Proxy:                 nil, // never route the token through an ambient proxy
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			MaxIdleConnsPerHost:   4,
			// Without this an idle keep-alive connection lives until the panel
			// closes it; callers should also reuse one Client per panel.
			IdleConnTimeout: 90 * time.Second,
		}
		switch {
		case plaintext && !o.allowInsecure:
			// A private panel (BOBRES on the same host or LAN) may speak plain
			// http, but then ONLY to private addresses, checked after DNS on
			// every connection.
			tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: blockPublic}).DialContext
		case !o.allowPrivate:
			tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: blockPrivate}).DialContext
		}
		hc = &http.Client{Timeout: 15 * time.Second, Transport: tr}
	}
	// Copy so we never mutate a caller-owned client, then refuse ALL redirects:
	// Go would replay POST bodies and the Authorization header on 307/308.
	cp := *hc
	cp.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		base: strings.TrimRight(baseURL, "/"), token: apiToken, hc: &cp,
		locks: map[string]*emailLock{},
	}, nil
}

// blockPrivate runs on every outgoing connection after DNS resolution.
func blockPrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ErrBlockedAddress
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return ErrBlockedAddress
	}
	return nil
}

// blockPublic is the inverse of blockPrivate, for plain-http panels: only
// loopback, private (RFC 1918 / ULA) and link-local unicast addresses pass.
func blockPublic(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ErrPublicPlaintext
	}
	// netip, not net.ParseIP: a link-local IPv6 address is dialed with a zone
	// ("fe80::1%eth0"), and IPv4-mapped forms must be judged as IPv4.
	a, err := netip.ParseAddr(host)
	if err != nil {
		return ErrPublicPlaintext
	}
	a = a.Unmap().WithZone("")
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() {
		return nil
	}
	return ErrPublicPlaintext
}

// lock acquires per-email mutexes in sorted order (deadlock-free) and returns an
// unlock func. Entries are refcounted and deleted when idle, so the map cannot grow
// without bound. This only serializes calls inside ONE process; cross-process
// safety must come from idempotency in the caller (see docs).
func (c *Client) lock(emails ...string) (unlock func()) {
	seen := make(map[string]struct{}, len(emails))
	uniq := make([]string, 0, len(emails))
	for _, e := range emails {
		if _, ok := seen[e]; !ok {
			seen[e] = struct{}{}
			uniq = append(uniq, e)
		}
	}
	sort.Strings(uniq)
	held := make([]string, 0, len(uniq))
	locks := make([]*emailLock, 0, len(uniq))
	for _, e := range uniq {
		c.mu.Lock()
		l := c.locks[e]
		if l == nil {
			l = &emailLock{}
			c.locks[e] = l
		}
		l.refs++
		c.mu.Unlock()
		l.mu.Lock()
		held = append(held, e)
		locks = append(locks, l)
	}
	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].mu.Unlock()
			c.mu.Lock()
			locks[i].refs--
			if locks[i].refs == 0 {
				delete(c.locks, held[i])
			}
			c.mu.Unlock()
		}
	}
}

// lockCount reports tracked lock entries (tests).
func (c *Client) lockCount() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.locks) }

type envelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("xui: encode request: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return fmt.Errorf("xui: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		// *url.Error embeds the full URL (secret web base path, customer email). Keep only
		// the underlying cause; context and blocked-address sentinels stay matchable.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("xui: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("xui: read response: %w", err)
	}
	var env envelope
	if jerr := json.Unmarshal(raw, &env); jerr != nil {
		// Non-JSON (proxy page, 404 hiding the API, ...). Never echo the body.
		return &APIError{Status: resp.StatusCode, Msg: "unexpected non-JSON response"}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return &APIError{Status: resp.StatusCode, Msg: "unexpected redirect from panel (check the panel URL and base path)"}
	}
	if resp.StatusCode >= 400 || !env.Success {
		msg := sanitizeMsg(env.Msg)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Msg: msg}
	}
	if out != nil && len(env.Obj) > 0 && string(env.Obj) != "null" {
		if err := json.Unmarshal(env.Obj, out); err != nil {
			return fmt.Errorf("xui: decode response: %w", err)
		}
	}
	return nil
}

func esc(s string) string { return url.PathEscape(s) }

// AddClient creates a client and attaches it to the given inbounds.
func (c *Client) AddClient(ctx context.Context, spec ClientSpec, inboundIDs []int) error {
	if err := validIdent(spec.Email); err != nil || len(inboundIDs) == 0 {
		return fmt.Errorf("xui: AddClient needs a valid email and at least one inbound: %w", ErrInvalidIdentifier)
	}
	if spec.SubID != "" {
		if err := validIdent(spec.SubID); err != nil {
			return err
		}
	}
	defer c.lock(spec.Email)()
	body := struct {
		Client     ClientSpec `json:"client"`
		InboundIDs []int      `json:"inboundIds"`
	}{spec, inboundIDs}
	return c.do(ctx, http.MethodPost, "/panel/api/clients/add", body, nil)
}

// GetClient fetches one client by email.
func (c *Client) GetClient(ctx context.Context, email string) (*ClientDetail, error) {
	if err := validIdent(email); err != nil {
		return nil, err
	}
	var d ClientDetail
	if err := c.do(ctx, http.MethodGet, "/panel/api/clients/get/"+esc(email), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DeleteClient removes a client from every inbound. keepTraffic retains its usage row.
func (c *Client) DeleteClient(ctx context.Context, email string, keepTraffic bool) error {
	if err := validIdent(email); err != nil {
		return err
	}
	defer c.lock(email)()
	p := "/panel/api/clients/del/" + esc(email)
	if keepTraffic {
		p += "?keepTraffic=1"
	}
	return c.do(ctx, http.MethodPost, p, nil, nil)
}

// AdjustRequest shifts expiry and/or quota for many clients. AddDays and
// AddBytes may be negative. The panel skips clients whose field is unlimited
// and re-enables clients that were disabled only because they were depleted.
type AdjustRequest struct {
	Emails   []string `json:"emails"`
	AddDays  int      `json:"addDays"`
	AddBytes int64    `json:"addBytes"`
}

// BulkAdjust is the renewal / traffic top-up primitive.
func (c *Client) BulkAdjust(ctx context.Context, req AdjustRequest) (*AdjustResult, error) {
	if len(req.Emails) == 0 {
		return nil, errors.New("xui: BulkAdjust needs at least one email")
	}
	for _, e := range req.Emails {
		if err := validIdent(e); err != nil {
			return nil, err
		}
	}
	defer c.lock(req.Emails...)()
	var r AdjustResult
	if err := c.do(ctx, http.MethodPost, "/panel/api/clients/bulkAdjust", req, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SetLimits sets a client's expiry (unix ms) and quota (bytes) to these
// absolute values, 0 meaning unlimited, and enables it: renewals and traffic
// top-ups, where a retry must not add twice. clients/update replaces the whole
// row, so the stored client is read first and sent back with only those fields
// changed; fields this package does not model (protocol keys, passwords) pass
// through untouched.
func (c *Client) SetLimits(ctx context.Context, email string, expiryMs, totalBytes int64) error {
	if err := validIdent(email); err != nil {
		return err
	}
	if expiryMs < 0 || totalBytes < 0 {
		return errors.New("xui: SetLimits needs limits >= 0")
	}
	defer c.lock(email)()
	stored, err := c.storedClient(ctx, email)
	if err != nil {
		return err
	}
	body, err := updateBody(stored, expiryMs, totalBytes)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "/panel/api/clients/update/"+esc(email), body, nil)
}

// SetEnabled turns a client off (it cannot connect) or back on, keeping its
// limits and usage. The caller holds no lock; this takes the email's lock.
func (c *Client) SetEnabled(ctx context.Context, email string, enable bool) error {
	if err := validIdent(email); err != nil {
		return err
	}
	defer c.lock(email)()
	stored, err := c.storedClient(ctx, email)
	if err != nil {
		return err
	}
	body, err := asUpdate(stored)
	if err != nil {
		return err
	}
	body["enable"] = json.RawMessage(strconv.FormatBool(enable))
	return c.do(ctx, http.MethodPost, "/panel/api/clients/update/"+esc(email), body, nil)
}

// storedClient reads a client's stored record (clients/get) as raw fields.
func (c *Client) storedClient(ctx context.Context, email string) (map[string]json.RawMessage, error) {
	var d struct {
		Client map[string]json.RawMessage `json:"client"`
	}
	if err := c.do(ctx, http.MethodGet, "/panel/api/clients/get/"+esc(email), nil, &d); err != nil {
		return nil, err
	}
	if len(d.Client) == 0 {
		return nil, &APIError{Status: http.StatusOK, Msg: "client " + email + " not found"}
	}
	return d.Client, nil
}

// updateBody is the clients/update input that sets these absolute limits and
// enables the client.
func updateBody(stored map[string]json.RawMessage, expiryMs, totalBytes int64) (map[string]json.RawMessage, error) {
	out, err := asUpdate(stored)
	if err != nil {
		return nil, err
	}
	out["totalGB"] = json.RawMessage(strconv.FormatInt(totalBytes, 10))
	out["expiryTime"] = json.RawMessage(strconv.FormatInt(expiryMs, 10))
	out["enable"] = json.RawMessage("true")
	return out, nil
}

// asUpdate turns a stored client (clients/get) into clients/update input
// that changes nothing: the protocol UUID moves from "uuid" to "id" (where
// the stored form has the panel's row id), bookkeeping fields are dropped,
// and the two fields stored as text but taken as structures are converted
// ("reverse" holds a JSON object, "allowedIPs" a comma-separated list).
func asUpdate(stored map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(stored))
	for k, v := range stored {
		switch k {
		case "id", "uuid", "createdAt", "updatedAt":
			continue
		case "reverse", "allowedIPs":
			var s string
			if json.Unmarshal(v, &s) != nil {
				out[k] = v // already structured
				continue
			}
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			if k == "reverse" {
				if !json.Valid([]byte(s)) {
					return nil, fmt.Errorf("xui: stored reverse settings are not JSON")
				}
				out[k] = json.RawMessage(s)
				continue
			}
			ips := strings.Split(s, ",")
			for i := range ips {
				ips[i] = strings.TrimSpace(ips[i])
			}
			b, _ := json.Marshal(ips)
			out[k] = b
			continue
		}
		out[k] = v
	}
	var uuid string
	if v, ok := stored["uuid"]; ok && json.Unmarshal(v, &uuid) == nil && uuid != "" {
		out["id"] = stored["uuid"]
	}
	return out, nil
}

// ResetTraffic zeroes a client's counters and re-enables it.
func (c *Client) ResetTraffic(ctx context.Context, email string) error {
	if err := validIdent(email); err != nil {
		return err
	}
	defer c.lock(email)()
	return c.do(ctx, http.MethodPost, "/panel/api/clients/resetTraffic/"+esc(email), nil, nil)
}

// Traffic returns usage counters for a client.
func (c *Client) Traffic(ctx context.Context, email string) (*Traffic, error) {
	if err := validIdent(email); err != nil {
		return nil, err
	}
	var t Traffic
	if err := c.do(ctx, http.MethodGet, "/panel/api/clients/traffic/"+esc(email), nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Links returns every share URL for a client.
func (c *Client) Links(ctx context.Context, email string) ([]string, error) {
	if err := validIdent(email); err != nil {
		return nil, err
	}
	var l []string
	if err := c.do(ctx, http.MethodGet, "/panel/api/clients/links/"+esc(email), nil, &l); err != nil {
		return nil, err
	}
	return l, nil
}

// InboundOptions lists inbounds (use Enable to pick "all enabled inbounds").
func (c *Client) InboundOptions(ctx context.Context) ([]InboundOption, error) {
	var o []InboundOption
	if err := c.do(ctx, http.MethodGet, "/panel/api/inbounds/options", nil, &o); err != nil {
		return nil, err
	}
	return o, nil
}

// SubLinks returns share URLs for a subscription ID.
func (c *Client) SubLinks(ctx context.Context, subID string) ([]string, error) {
	if err := validIdent(subID); err != nil {
		return nil, err
	}
	var l []string
	if err := c.do(ctx, http.MethodGet, "/panel/api/clients/subLinks/"+esc(subID), nil, &l); err != nil {
		return nil, err
	}
	return l, nil
}
