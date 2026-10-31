// Package hiddify is a minimal, hardened client for the Hiddify Manager panel
// API v2 (https://github.com/hiddify/Hiddify-Manager, panel source in
// hiddify/hiddify-panel): the admin API under <panel address>/<admin proxy
// path>/api/v2/admin/, authenticated by an admin's UUID in the Hiddify-API-Key
// header, and the user API under the client proxy path, authenticated by the
// user's UUID. It builds its HTTP client with the same address policy as
// internal/xui (https unless private, no ambient proxy, dial-time address
// checks, no redirects), so one policy guards every credential the provisioner
// holds.
//
// Checked against Hiddify Manager v14.0.5 (hiddifypanel/panel/commercial/
// restapi/v2): users are created with POST user/, read, changed and deleted
// under user/<uuid>/ (a delete only marks the user deleted; a create with that
// UUID later takes it over), and GET user/ lists them, answering 404 "You have
// no user" for none.
package hiddify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

const (
	// maxResponseBytes bounds one answer: a user listing is about 1.5 KB a
	// user (it carries the users' keys).
	maxResponseBytes = 64 << 20
	maxMsgLen        = 200
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidUUID reports a UUID in the 8-4-4-4-12 form the panel uses.
func ValidUUID(s string) bool { return uuidRe.MatchString(s) }

// APIError is a panel answer other than success.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("hiddify: panel error (http %d): %s", e.Status, e.Msg)
}

// NotFoundError says the panel has no such user: it answered 404 for the
// user's UUID, or its listing did not have the name.
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string { return "hiddify: user " + e.Name + " not found" }

// IsNotFound reports the answer to a user the panel does not have.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

// Answered reports whether the panel answered (a refusal or "no such user").
func Answered(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) || IsNotFound(err)
}

// IsUnauthorized reports a refused API key (HTTP 401 or 403).
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden)
}

// IsExists reports the refusal of a create whose UUID a user has.
func IsExists(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusBadRequest && strings.Contains(strings.ToLower(ae.Msg), "exist")
}

// Client talks to one Hiddify panel. It is safe for concurrent use.
type Client struct {
	origin    string // scheme://host[:port]
	adminPath string // "/<admin proxy path>"
	key       string
	hc        *http.Client
}

// New returns a client for the panel at baseURL, the panel's address with the
// admin proxy path (https://panel.example.com/<admin path>; a pasted admin link
// that goes on with the admin's UUID or /admin/ is cut back to that), with
// apiKey, an admin's UUID. The options are internal/xui's (address policy).
func New(baseURL, apiKey string, opts ...xui.Option) (*Client, error) {
	hc, err := xui.HardenedHTTPClient(baseURL, opts...)
	if err != nil {
		return nil, err
	}
	apiKey = strings.ToLower(strings.TrimSpace(apiKey))
	if !ValidUUID(apiKey) {
		return nil, errors.New("hiddify: the API key is the admin's UUID (8-4-4-4-12 hexadecimal digits)")
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, errors.New("hiddify: invalid panel URL")
	}
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(segs) == 0 || segs[0] == "" {
		return nil, errors.New("hiddify: the panel address needs the admin proxy path, such as https://panel.example.com/AbCd123/")
	}
	return &Client{origin: u.Scheme + "://" + u.Host, adminPath: "/" + segs[0], key: apiKey, hc: hc}, nil
}

// Origin is the panel's scheme and host.
func (c *Client) Origin() string { return c.origin }

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

// messageOf reads apiflask's error body {"message": ..., "detail": ...}.
func messageOf(b []byte) string {
	var e struct {
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(b, &e) != nil {
		return ""
	}
	msg := e.Message
	if len(e.Detail) > 2 && !bytes.Equal(e.Detail, []byte("{}")) && !bytes.Equal(e.Detail, []byte("null")) {
		msg += " " + string(e.Detail)
	}
	return sanitize(msg)
}

// do sends one request (path absolute on the panel's host) with key as the API
// key; a JSON body for in != nil; out receives the answer when not nil.
func (c *Client) do(ctx context.Context, method, path, key string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Hiddify-API-Key", key)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(b) > maxResponseBytes {
		return errors.New("hiddify: response too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := messageOf(b)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			msg += " (use the panel's address with the admin proxy path, such as https://panel.example.com/AbCd123)"
		}
		return &APIError{Status: resp.StatusCode, Msg: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return &APIError{Status: resp.StatusCode, Msg: "unexpected response (not a Hiddify panel, or not its admin proxy path?)"}
	}
	return nil
}

func (c *Client) admin(ctx context.Context, method, path string, in, out any) error {
	return c.do(ctx, method, c.adminPath+"/api/v2/admin"+path, c.key, in, out)
}

// User is a Hiddify user as the API returns it (the fields BOBRES reads).
type User struct {
	UUID           string   `json:"uuid"`
	Name           string   `json:"name"`
	UsageLimitGB   *float64 `json:"usage_limit_GB"`
	PackageDays    *int64   `json:"package_days"`
	Mode           string   `json:"mode"`
	StartDate      *string  `json:"start_date"` // YYYY-MM-DD; null: starts on first use
	CurrentUsageGB *float64 `json:"current_usage_GB"`
	Comment        *string  `json:"comment"`
	Enable         *bool    `json:"enable"`
	IsActive       *bool    `json:"is_active"`
	Deleted        *bool    `json:"deleted"`
}

// String describes the user without its UUID (the user's secret).
func (u *User) String() string {
	if u == nil {
		return "<nil>"
	}
	f := func(p *float64) float64 {
		if p == nil {
			return 0
		}
		return *p
	}
	days, start := int64(0), ""
	if u.PackageDays != nil {
		days = *u.PackageDays
	}
	if u.StartDate != nil {
		start = *u.StartDate
	}
	return fmt.Sprintf("{%s limit=%gGB used=%gGB days=%d start=%q enable=%t}", u.Name, f(u.UsageLimitGB), f(u.CurrentUsageGB), days, start, u.Enabled())
}

// Enabled is the user's own switch.
func (u *User) Enabled() bool { return u.Enable != nil && *u.Enable }

// CommentText is the free-text comment.
func (u *User) CommentText() string {
	if u.Comment == nil {
		return ""
	}
	return *u.Comment
}

func userPath(id string) (string, error) {
	if !ValidUUID(id) {
		return "", errors.New("hiddify: invalid user UUID")
	}
	return "/user/" + strings.ToLower(id) + "/", nil
}

// Users lists the users of the admin and its sub-admins (GET user/).
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var out []User
	err := c.admin(ctx, http.MethodGet, "/user/", nil, &out)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound && strings.Contains(strings.ToLower(ae.Msg), "no user") {
		return nil, nil // the panel's answer to an admin with no user
	}
	return out, err
}

// User reads one user; a *NotFoundError when the panel has none (or only a
// deleted one).
func (c *Client) User(ctx context.Context, id string) (*User, error) {
	p, err := userPath(id)
	if err != nil {
		return nil, err
	}
	var u User
	err = c.admin(ctx, http.MethodGet, p, nil, &u)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return nil, &NotFoundError{Name: "with this UUID"}
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser adds a user; body is the user document (with its uuid).
func (c *Client) CreateUser(ctx context.Context, body map[string]any) (*User, error) {
	var u User
	if err := c.admin(ctx, http.MethodPost, "/user/", body, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// PatchUser changes a user; only the fields in body change.
func (c *Client) PatchUser(ctx context.Context, id string, body map[string]any) (*User, error) {
	p, err := userPath(id)
	if err != nil {
		return nil, err
	}
	var u User
	err = c.admin(ctx, http.MethodPatch, p, body, &u)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return nil, &NotFoundError{Name: "with this UUID"}
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteUser removes a user (the panel keeps it marked deleted).
func (c *Client) DeleteUser(ctx context.Context, id string) error {
	p, err := userPath(id)
	if err != nil {
		return err
	}
	err = c.admin(ctx, http.MethodDelete, p, nil, nil)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
		return &NotFoundError{Name: "with this UUID"}
	}
	return err
}

// Me reads the admin the key belongs to (GET me/): the call the health check
// makes.
func (c *Client) Me(ctx context.Context) error {
	var out struct {
		UUID string `json:"uuid"`
	}
	if err := c.admin(ctx, http.MethodGet, "/me/", nil, &out); err != nil {
		return err
	}
	if out.UUID == "" {
		return &APIError{Status: http.StatusOK, Msg: "unexpected response (not a Hiddify panel?)"}
	}
	return nil
}

// Version is the panel's version (GET <admin path>/api/v2/panel/info/).
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := c.do(ctx, http.MethodGet, c.adminPath+"/api/v2/panel/info/", c.key, nil, &out); err != nil {
		return "", err
	}
	return out.Version, nil
}

// ClientPath is the panel's client proxy path ("/<path>"), under which users
// reach their configurations and subscriptions. The users page of the panel's
// dashboard API (GET users/) gives it as the base of the users' links.
func (c *Client) ClientPath(ctx context.Context) (string, error) {
	var out struct {
		LinkDomains []struct {
			Kind string `json:"kind"`
			Base string `json:"base"`
		} `json:"link_domains"`
	}
	if err := c.admin(ctx, http.MethodGet, "/users/", nil, &out); err != nil {
		return "", err
	}
	for _, d := range out.LinkDomains {
		u, err := url.Parse(d.Base)
		if err != nil {
			continue
		}
		if p := strings.Trim(u.EscapedPath(), "/"); p != "" && !strings.Contains(p, "/") {
			return "/" + p, nil
		}
	}
	return "", &APIError{Status: http.StatusOK, Msg: "the panel did not say its client proxy path"}
}

// Config is one entry of a user's configurations.
type Config struct {
	Name     string `json:"name"`
	Link     string `json:"link"`
	Protocol string `json:"protocol"`
	Category string `json:"category"` // subscription or config
}

// UserConfigs lists a user's subscriptions and share links, as the user sees
// them (GET <client path>/api/v2/user/all-configs/ with the user's UUID).
func (c *Client) UserConfigs(ctx context.Context, clientPath, userUUID string) ([]Config, error) {
	if !ValidUUID(userUUID) || !strings.HasPrefix(clientPath, "/") || strings.Contains(clientPath[1:], "/") {
		return nil, errors.New("hiddify: invalid user configs request")
	}
	var out []Config
	if err := c.do(ctx, http.MethodGet, clientPath+"/api/v2/user/all-configs/", strings.ToLower(userUUID), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
