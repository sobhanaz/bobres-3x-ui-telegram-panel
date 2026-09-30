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
	"net/url"
	"regexp"
	"sort"
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
var ErrInsecureURL = errors.New("xui: panel URL must use https")

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

// ClientDetail is what clients/get returns.
type ClientDetail struct {
	Client      ClientSpec `json:"client"`
	InboundIDs  []int      `json:"inboundIds"`
	UsedTraffic int64      `json:"usedTraffic"`
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
// still refused. The caller owns dial-time address filtering when using this.
func WithHTTPClient(hc *http.Client) Option { return func(o *settings) { o.hc = hc } }

// AllowInsecureHTTP permits http:// panel URLs. Only for tests/dev: the bearer token
// would cross the network in cleartext.
func AllowInsecureHTTP() Option { return func(o *settings) { o.allowInsecure = true } }

// AllowPrivateAddresses permits panels on loopback/private ranges (a panel on the same
// host or LAN is a legitimate setup that the operator must opt into).
func AllowPrivateAddresses() Option { return func(o *settings) { o.allowPrivate = true } }

// New creates a client. baseURL is the panel root including any web base path. It
// must be https unless AllowInsecureHTTP is given. Unless AllowPrivateAddresses is
// given, connections to loopback, private and link-local addresses are refused at
// dial time (after DNS resolution, so DNS rebinding cannot bypass it).
func New(baseURL, apiToken string, opts ...Option) (*Client, error) {
	var o settings
	for _, f := range opts {
		f(&o)
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("xui: invalid panel URL")
	}
	if u.Scheme == "http" && !o.allowInsecure {
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
		}
		if !o.allowPrivate {
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
