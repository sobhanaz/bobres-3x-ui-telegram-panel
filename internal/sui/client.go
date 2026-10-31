// Package sui is a minimal, hardened client for the S-UI panel API
// (https://github.com/alireza0/s-ui, a sing-box panel): its API v2 under
// <panel address>/apiv2/, authenticated by an API token the panel makes
// (Settings > API tokens) in a "Token" header. Every answer is HTTP 200 with
// {"success", "msg", "obj"}; a refusal is success=false. It builds its HTTP
// client with the same address policy as internal/xui (https unless private, no
// ambient proxy, dial-time address checks, no redirects), so one policy guards
// every credential the provisioner holds.
//
// Checked against S-UI v1.6.6 (api/apiV2Handler.go, api/apiService.go,
// service/client.go): clients are saved through POST apiv2/save with the form
// fields object=clients, action=new|edit|del and data=<JSON>; GET apiv2/clients
// lists them without their configuration and links, and apiv2/clients?id=N gives
// one in full.
package sui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

const (
	// maxResponseBytes bounds one answer. A client listing is about 300 bytes
	// a client, so this holds well over 100 000 clients.
	maxResponseBytes = 64 << 20
	maxMsgLen        = 200
	// MaxTokenLen is the longest API token accepted (the panel makes 32
	// characters).
	MaxTokenLen = 256
)

// APIError is a panel answer other than success: an HTTP status other than
// 200, or success=false inside HTTP 200 (Status is then 200).
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("s-ui: panel error (http %d): %s", e.Status, e.Msg)
}

// NotFoundError says the panel has no client of that name: its client listing
// answered and did not have it.
type NotFoundError struct{ Name string }

func (e *NotFoundError) Error() string { return "s-ui: client " + e.Name + " not found" }

// IsNotFound reports the answer to a client the panel does not have.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}

// Answered reports whether the panel answered (a refusal or "no such client"),
// as opposed to a call that got no answer at all.
func Answered(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) || IsNotFound(err)
}

// IsUnauthorized reports a refused token: S-UI answers "invalid token" inside
// HTTP 200.
func IsUnauthorized(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden ||
		strings.Contains(strings.ToLower(ae.Msg), "invalid token")
}

// Client talks to one S-UI panel. It is safe for concurrent use.
type Client struct {
	base  string
	token string
	hc    *http.Client
}

// New returns a client for the panel at baseURL (the panel's address with its
// web path, such as https://panel.example.com:2095/app). The options are
// internal/xui's (address policy).
func New(baseURL, token string, opts ...xui.Option) (*Client, error) {
	hc, err := xui.HardenedHTTPClient(baseURL, opts...)
	if err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > MaxTokenLen || strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, errors.New("s-ui: an API token (one word) is required")
	}
	return &Client{base: strings.TrimRight(strings.TrimSpace(baseURL), "/"), token: token, hc: hc}, nil
}

// Base is the panel address the client was made with (no trailing slash).
func (c *Client) Base() string { return c.base }

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

// envelope is every API answer.
type envelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

// do sends one request to apiv2/<action> and returns the answer's obj.
func (c *Client) do(ctx context.Context, method, action string, query url.Values, form url.Values) (json.RawMessage, error) {
	u := c.base + "/apiv2/" + action
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Token", c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxResponseBytes {
		return nil, errors.New("s-ui: response too large")
	}
	if resp.StatusCode != http.StatusOK {
		msg := http.StatusText(resp.StatusCode)
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			msg += " (use the panel's address with its web path, such as https://panel.example.com:2095/app)"
		}
		return nil, &APIError{Status: resp.StatusCode, Msg: msg}
	}
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, &APIError{Status: resp.StatusCode, Msg: "unexpected response (not an S-UI panel, or not its address with the web path?)"}
	}
	if !env.Success {
		msg := sanitize(strings.TrimPrefix(strings.TrimPrefix(env.Msg, action), ": "))
		if msg == "" {
			msg = "refused"
		}
		return nil, &APIError{Status: http.StatusOK, Msg: msg}
	}
	return env.Obj, nil
}

// Link is one share link the panel keeps for a client.
type Link struct {
	Type   string `json:"type"` // local, external or sub
	Remark string `json:"remark"`
	URI    string `json:"uri"`
}

// ClientRow is a client as the listing gives it (no configuration, no links).
type ClientRow struct {
	ID       int    `json:"id"`
	Enable   bool   `json:"enable"`
	Name     string `json:"name"`
	Desc     string `json:"desc"`
	Group    string `json:"group"`
	Inbounds []int  `json:"inbounds"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Volume   int64  `json:"volume"`
	Expiry   int64  `json:"expiry"` // unix seconds; 0 = none
}

// Clients lists every client (GET apiv2/clients).
func (c *Client) Clients(ctx context.Context) ([]ClientRow, error) {
	obj, err := c.do(ctx, http.MethodGet, "clients", nil, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Clients []ClientRow `json:"clients"`
	}
	if err := json.Unmarshal(obj, &out); err != nil {
		return nil, &APIError{Status: http.StatusOK, Msg: "unexpected client listing"}
	}
	return out.Clients, nil
}

// Record is one client in full, as the panel stores it: the known fields are
// read from it, and an edit sends the whole of it back (with any field a newer
// panel adds), changed only where the caller changes it.
type Record map[string]json.RawMessage

// Int is a numeric field (0 when absent or not a number).
func (r Record) Int(k string) int64 {
	var n json.Number
	if json.Unmarshal(r[k], &n) != nil {
		return 0
	}
	if i, err := n.Int64(); err == nil {
		return i
	}
	f, err := n.Float64()
	if err != nil {
		return 0
	}
	return int64(f)
}

// Bool is a boolean field (false when absent).
func (r Record) Bool(k string) bool {
	var b bool
	_ = json.Unmarshal(r[k], &b)
	return b
}

// Str is a string field ("" when absent).
func (r Record) Str(k string) string {
	var s string
	_ = json.Unmarshal(r[k], &s)
	return s
}

// Links are the client's share links.
func (r Record) Links() []Link {
	var l []Link
	_ = json.Unmarshal(r["links"], &l)
	return l
}

// Set replaces a field.
func (r Record) Set(k string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	r[k] = b
}

// String describes the record without its credentials (configuration, links).
func (r Record) String() string {
	return fmt.Sprintf("{%d %s enable=%t volume=%d expiry=%d up=%d down=%d delay=%t days=%d group=%q}",
		r.Int("id"), r.Str("name"), r.Bool("enable"), r.Int("volume"), r.Int("expiry"), r.Int("up"), r.Int("down"),
		r.Bool("delayStart"), r.Int("resetDays"), r.Str("group"))
}

// Client reads one client in full (GET apiv2/clients?id=N); ok is false when
// the panel has no client with that id.
func (c *Client) Client(ctx context.Context, id int) (Record, bool, error) {
	if id <= 0 {
		return nil, false, nil
	}
	obj, err := c.do(ctx, http.MethodGet, "clients", url.Values{"id": {strconv.Itoa(id)}}, nil)
	if err != nil {
		return nil, false, err
	}
	var out struct {
		Clients []Record `json:"clients"`
	}
	if err := json.Unmarshal(obj, &out); err != nil {
		return nil, false, &APIError{Status: http.StatusOK, Msg: "unexpected client record"}
	}
	for _, r := range out.Clients {
		if int(r.Int("id")) == id {
			return r, true, nil
		}
	}
	return nil, false, nil
}

// Save sends one change of the clients (POST apiv2/save, object=clients):
// action new or edit with a client, del with its id.
func (c *Client) Save(ctx context.Context, action string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, http.MethodPost, "save", nil, url.Values{"object": {"clients"}, "action": {action}, "data": {string(b)}})
	return err
}

// Inbound is one of the panel's sing-box inbounds.
type Inbound struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	Tag  string `json:"tag"`
	Port int    `json:"-"`
	// TakesUsers: the inbound has users (the panel lists them), so clients can
	// be put on it.
	TakesUsers bool `json:"-"`
}

// Inbounds lists the inbounds (GET apiv2/inbounds).
func (c *Client) Inbounds(ctx context.Context) ([]Inbound, error) {
	obj, err := c.do(ctx, http.MethodGet, "inbounds", nil, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Inbounds []map[string]json.RawMessage `json:"inbounds"`
	}
	if err := json.Unmarshal(obj, &out); err != nil {
		return nil, &APIError{Status: http.StatusOK, Msg: "unexpected inbound listing"}
	}
	list := make([]Inbound, 0, len(out.Inbounds))
	for _, m := range out.Inbounds {
		r := Record(m)
		in := Inbound{ID: int(r.Int("id")), Type: r.Str("type"), Tag: r.Str("tag")}
		if p := r.Int("listen_port"); p > 0 && p <= 65535 {
			in.Port = int(p)
		}
		_, in.TakesUsers = m["users"]
		list = append(list, in)
	}
	return list, nil
}

// Settings reads the panel's settings (GET apiv2/settings): a map of name to
// value, all strings.
func (c *Client) Settings(ctx context.Context) (map[string]string, error) {
	obj, err := c.do(ctx, http.MethodGet, "settings", nil, nil)
	if err != nil {
		return nil, err
	}
	var out map[string]string
	if err := json.Unmarshal(obj, &out); err != nil {
		return nil, &APIError{Status: http.StatusOK, Msg: "unexpected settings"}
	}
	return out, nil
}

// Version is the panel's version (GET apiv2/status?r=sys, sys.appVersion).
func (c *Client) Version(ctx context.Context) (string, error) {
	obj, err := c.do(ctx, http.MethodGet, "status", url.Values{"r": {"sys"}}, nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Sys struct {
			AppVersion string `json:"appVersion"`
		} `json:"sys"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(obj), &out); err != nil {
		return "", nil
	}
	return out.Sys.AppVersion, nil
}
