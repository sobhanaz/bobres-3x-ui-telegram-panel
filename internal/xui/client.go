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
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
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
	locks sync.Map // email -> *sync.Mutex (serializes writes per client in this process)
}

// Option customizes a Client.
type Option func(*Client)

// WithHTTPClient replaces the default HTTP client (tests, custom TLS).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.hc = hc } }

// New creates a client. baseURL is the panel root including any web base path.
func New(baseURL, apiToken string, opts ...Option) *Client {
	c := &Client{
		base:  strings.TrimRight(baseURL, "/"),
		token: apiToken,
		hc:    &http.Client{Timeout: 15 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Client) lock(emails ...string) (unlock func()) {
	seen := map[string]struct{}{}
	uniq := make([]string, 0, len(emails))
	for _, e := range emails {
		if _, ok := seen[e]; !ok {
			seen[e] = struct{}{}
			uniq = append(uniq, e)
		}
	}
	sort.Strings(uniq) // consistent order prevents deadlock between overlapping calls
	held := make([]*sync.Mutex, 0, len(uniq))
	for _, e := range uniq {
		v, _ := c.locks.LoadOrStore(e, &sync.Mutex{})
		m := v.(*sync.Mutex)
		m.Lock()
		held = append(held, m)
	}
	return func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i].Unlock()
		}
	}
}

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
		// Do not wrap err verbatim if it could echo the URL with credentials; net errors carry only host.
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
	if resp.StatusCode >= 400 || !env.Success {
		msg := strings.TrimSpace(env.Msg)
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
	if spec.Email == "" || len(inboundIDs) == 0 {
		return errors.New("xui: AddClient needs an email and at least one inbound")
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
	var d ClientDetail
	if err := c.do(ctx, http.MethodGet, "/panel/api/clients/get/"+esc(email), nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DeleteClient removes a client from every inbound. keepTraffic retains its usage row.
func (c *Client) DeleteClient(ctx context.Context, email string, keepTraffic bool) error {
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
	defer c.lock(req.Emails...)()
	var r AdjustResult
	if err := c.do(ctx, http.MethodPost, "/panel/api/clients/bulkAdjust", req, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ResetTraffic zeroes a client's counters and re-enables it.
func (c *Client) ResetTraffic(ctx context.Context, email string) error {
	defer c.lock(email)()
	return c.do(ctx, http.MethodPost, "/panel/api/clients/resetTraffic/"+esc(email), nil, nil)
}

// Traffic returns usage counters for a client.
func (c *Client) Traffic(ctx context.Context, email string) (*Traffic, error) {
	var t Traffic
	if err := c.do(ctx, http.MethodGet, "/panel/api/clients/traffic/"+esc(email), nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Links returns every share URL for a client.
func (c *Client) Links(ctx context.Context, email string) ([]string, error) {
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
	var l []string
	if err := c.do(ctx, http.MethodGet, "/panel/api/clients/subLinks/"+esc(subID), nil, &l); err != nil {
		return nil, err
	}
	return l, nil
}
