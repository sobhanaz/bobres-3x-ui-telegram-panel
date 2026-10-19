// Package marzban is a minimal, hardened client for the Marzban panel API
// (https://github.com/Gozargah/Marzban): an admin logs in with a username and
// password for a short-lived bearer token, and users are managed under
// /api/user. It builds its HTTP client with the same address policy as
// internal/xui (https unless private, no ambient proxy, dial-time address
// checks, no redirects), so one policy guards every credential the
// provisioner holds.
package marzban

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
	"unicode"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

const (
	maxResponseBytes = 8 << 20
	maxMsgLen        = 200
)

// APIError is a panel answer other than success.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("marzban: panel error (http %d): %s", e.Status, e.Msg)
}

// IsUnauthorized reports a refused login or token (HTTP 401 or 403).
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden)
}

// IsNotFound reports the panel's answer to a user it does not have.
func IsNotFound(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

// IsConflict reports the panel's answer to creating a user that exists (409).
func IsConflict(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusConflict
}

// IsServerError reports a failure inside the panel (HTTP 5xx).
func IsServerError(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status >= http.StatusInternalServerError
}

// MaxUsernameLen is the longest username BOBRES sends. The panel's own rule
// has no limit of that kind, but its column holds 34 characters, and its
// documentation promises 32.
const MaxUsernameLen = 32

// ErrInvalidUsername is the answer to a name the panel would refuse.
var ErrInvalidUsername = errors.New("marzban: invalid username (3 to 32 of a-z, 0-9, - _ @ ., and at least " +
	"3 letters, digits or underscores before the first - . or @)")

// ValidateUsername reports a username Marzban accepts (and that is safe in a
// URL path). The panel's rule (v0.8.4, app/models/user.py) is the regular
// expression ^(?=\w{3,32}\b)[a-zA-Z0-9-_@.]+(?:_[a-zA-Z0-9-_@.]+)*$, which
// reads: only those characters, and the run of letters, digits and underscores
// the name starts with is 3 to 32 long. So "abc-d" is fine but "u1-abcd" and
// "a-bcdef" are refused, whatever follows. BOBRES also keeps the whole name to
// 32 characters.
func ValidateUsername(u string) error {
	if len(u) > MaxUsernameLen {
		return ErrInvalidUsername
	}
	lead := 0
	for i := 0; i < len(u); i++ {
		c := u[i]
		word := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
		if !word && c != '-' && c != '@' && c != '.' {
			return ErrInvalidUsername
		}
		if word && lead == i {
			lead++
		}
	}
	if lead < 3 {
		return ErrInvalidUsername
	}
	return nil
}

// ParseCredentials splits the stored "username:password" of a panel admin. A
// Marzban admin name has no colon, so the first one separates the two.
func ParseCredentials(s string) (user, pass string, err error) {
	user, pass, ok := strings.Cut(s, ":")
	if !ok || user == "" || pass == "" {
		return "", "", errors.New("marzban: credentials must be username:password")
	}
	return user, pass, nil
}

// Client talks to one Marzban panel. It is safe for concurrent use.
type Client struct {
	base       string
	user, pass string
	hc         *http.Client

	mu    sync.Mutex
	token string
}

// New returns a client for the panel at baseURL, logging in as the admin on
// first use. The options are internal/xui's (address policy).
func New(baseURL, username, password string, opts ...xui.Option) (*Client, error) {
	hc, err := xui.HardenedHTTPClient(baseURL, opts...)
	if err != nil {
		return nil, err
	}
	if username == "" || password == "" {
		return nil, errors.New("marzban: admin username and password are required")
	}
	return &Client{base: strings.TrimRight(strings.TrimSpace(baseURL), "/"), user: username, pass: password, hc: hc}, nil
}

func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > maxMsgLen {
		s = s[:maxMsgLen]
	}
	return strings.TrimSpace(s)
}

// detailOf reads {"detail": ...}: a string (most refusals), an object of
// field name to message (Marzban's own handler for a body it rejects: HTTP 422
// {"detail": {"username": "Value error, ..."}}), or FastAPI's list of errors.
func detailOf(body []byte) string {
	var d struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &d) != nil || len(d.Detail) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(d.Detail, &s) == nil {
		return sanitize(s)
	}
	var fields map[string]string
	if json.Unmarshal(d.Detail, &fields) == nil && len(fields) > 0 {
		names := make([]string, 0, len(fields))
		for k := range fields {
			names = append(names, k)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, k := range names {
			parts = append(parts, k+": "+strings.TrimPrefix(fields[k], "Value error, "))
		}
		return sanitize(strings.Join(parts, "; "))
	}
	var list []struct {
		Msg string `json:"msg"`
	}
	if json.Unmarshal(d.Detail, &list) == nil && len(list) > 0 {
		return sanitize(list[0].Msg)
	}
	return ""
}

// send performs one request and returns the status and the bounded body.
func (c *Client) send(ctx context.Context, method, path, contentType, bearer string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return 0, nil, err
	}
	if len(b) > maxResponseBytes {
		return 0, nil, errors.New("marzban: response too large")
	}
	return resp.StatusCode, b, nil
}

// login obtains a fresh token unless another caller already replaced stale.
func (c *Client) login(ctx context.Context, stale string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.token != stale {
		return c.token, nil
	}
	form := url.Values{"username": {c.user}, "password": {c.pass}}
	code, b, err := c.send(ctx, http.MethodPost, "/api/admin/token", "application/x-www-form-urlencoded", "", []byte(form.Encode()))
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		msg := detailOf(b)
		if msg == "" {
			msg = "login refused"
		}
		if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
			// The address operators paste is often the dashboard's, which
			// answers 405 to the login under its /dashboard/ path.
			msg += " (use the panel's own address, such as https://panel.example.com:8000, without /dashboard/)"
		}
		return "", &APIError{Status: code, Msg: msg}
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(b, &out) != nil || out.AccessToken == "" {
		return "", &APIError{Status: code, Msg: "login returned no token (not a Marzban panel?)"}
	}
	c.token = out.AccessToken
	return c.token, nil
}

func (c *Client) current() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// do calls the API as the admin, logging in first and once more when the
// token has expired. A JSON body is sent for in != nil; out receives the
// answer when not nil.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = b
	}
	tok := c.current()
	if tok == "" {
		var err error
		if tok, err = c.login(ctx, ""); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		ct := ""
		if body != nil {
			ct = "application/json"
		}
		code, b, err := c.send(ctx, method, path, ct, tok, body)
		if err != nil {
			return err
		}
		if code == http.StatusUnauthorized && attempt == 0 {
			if tok, err = c.login(ctx, tok); err != nil {
				return err
			}
			continue
		}
		if code < 200 || code > 299 {
			msg := detailOf(b)
			if msg == "" {
				msg = http.StatusText(code)
			}
			return &APIError{Status: code, Msg: msg}
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(b, out); err != nil {
			return &APIError{Status: code, Msg: "unexpected response (not a Marzban panel?)"}
		}
		return nil
	}
}

// User is a Marzban user as the API returns it (the fields BOBRES reads).
type User struct {
	Username             string   `json:"username"`
	Status               string   `json:"status"`
	Expire               *int64   `json:"expire"` // unix seconds; null or 0 = unlimited
	DataLimit            *int64   `json:"data_limit"`
	UsedTraffic          int64    `json:"used_traffic"`
	OnHoldExpireDuration *int64   `json:"on_hold_expire_duration"`
	Note                 *string  `json:"note"`
	Links                []string `json:"links"`
	SubscriptionURL      string   `json:"subscription_url"`
	// Proxies holds the credentials the panel made for each protocol, as it
	// sends them (shadowsocks: password and method; vless: id). BOBRES never
	// reads them; the integration tests connect with them.
	Proxies map[string]json.RawMessage `json:"proxies"`
	// Admin is the admin that owns the user; null for a user created by the
	// panel's SUDO_USERNAME environment account, which has no row of its own
	// (see HasOwner).
	Admin *struct {
		Username string `json:"username"`
	} `json:"admin"`
}

// Statuses a user can be in.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusLimited  = "limited"
	StatusExpired  = "expired"
	StatusOnHold   = "on_hold"
)

// ExpireSeconds is the expiry, 0 when unlimited.
func (u *User) ExpireSeconds() int64 {
	if u.Expire == nil {
		return 0
	}
	return *u.Expire
}

// DataLimitBytes is the quota, 0 when unlimited.
func (u *User) DataLimitBytes() int64 {
	if u.DataLimit == nil {
		return 0
	}
	return *u.DataLimit
}

// HoldSeconds is how long an on-hold user's clock runs once it first
// connects, 0 when none.
func (u *User) HoldSeconds() int64 {
	if u.OnHoldExpireDuration == nil {
		return 0
	}
	return *u.OnHoldExpireDuration
}

// String describes the user without its credentials (Proxies, share links,
// subscription address), so a user that is printed in a log or a failed test
// does not show them.
func (u *User) String() string {
	if u == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{%s %s expire=%d limit=%d used=%d hold=%d owner=%t links=%d}", u.Username, u.Status,
		u.ExpireSeconds(), u.DataLimitBytes(), u.UsedTraffic, u.HoldSeconds(), u.HasOwner(), len(u.Links))
}

// HasOwner reports whether an admin owns the user. A user created through the
// environment sudo account has none, and v0.8.x then answers HTTP 500 to its
// deletion: after removing the row, before dropping the account from the
// running core.
func (u *User) HasOwner() bool { return u.Admin != nil }

// NoteText is the free-text note.
func (u *User) NoteText() string {
	if u.Note == nil {
		return ""
	}
	return *u.Note
}

// Inbound is one inbound of the panel's Xray core.
type Inbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Network  string `json:"network"`
	TLS      string `json:"tls"`
	Port     any    `json:"port"`
}

// PortNumber is the inbound's port (0 when the panel gives a range or none).
func (i Inbound) PortNumber() int {
	switch p := i.Port.(type) {
	case float64:
		return int(p)
	case string:
		n := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				return 0
			}
			n = n*10 + int(r-'0')
			if n > 65535 {
				return 0
			}
		}
		return n
	}
	return 0
}

// Version is the panel's version (GET /api/system).
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/system", nil, &out); err != nil {
		return "", err
	}
	return out.Version, nil
}

// Inbounds lists the Xray inbounds by protocol (GET /api/inbounds).
func (c *Client) Inbounds(ctx context.Context) (map[string][]Inbound, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/inbounds", nil, &raw); err != nil {
		return nil, err
	}
	var out map[string][]Inbound
	if err := json.Unmarshal(raw, &out); err != nil {
		if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
			// Marzban 1.0 (checked with v1.0.0-beta-3) answers a list of tags.
			return nil, &APIError{Status: http.StatusOK, Msg: "this panel lists its inbounds the way Marzban 1.x does; BOBRES works with Marzban 0.8"}
		}
		return nil, &APIError{Status: http.StatusOK, Msg: "unexpected response (not a Marzban panel?)"}
	}
	return out, nil
}

func userPath(username string) (string, error) {
	if err := ValidateUsername(username); err != nil {
		return "", err
	}
	return "/api/user/" + url.PathEscape(username), nil
}

// GetUser reads a user; a missing one is an *APIError with status 404.
func (c *Client) GetUser(ctx context.Context, username string) (*User, error) {
	p, err := userPath(username)
	if err != nil {
		return nil, err
	}
	var u User
	if err := c.do(ctx, http.MethodGet, p, nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser adds a user; body is the UserCreate document.
func (c *Client) CreateUser(ctx context.Context, body map[string]any) error {
	name, _ := body["username"].(string)
	if err := ValidateUsername(name); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "/api/user", body, nil)
}

// ModifyUser changes a user; only the fields in body change.
func (c *Client) ModifyUser(ctx context.Context, username string, body map[string]any) error {
	p, err := userPath(username)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, p, body, nil)
}

// DeleteUser removes a user.
func (c *Client) DeleteUser(ctx context.Context, username string) error {
	p, err := userPath(username)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, p, nil, nil)
}

// ResetUsage zeroes a user's used traffic.
func (c *Client) ResetUsage(ctx context.Context, username string) error {
	p, err := userPath(username)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, p+"/reset", nil, nil)
}
