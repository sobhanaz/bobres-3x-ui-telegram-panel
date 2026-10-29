// Package toncenter reads an address's transactions from TON Center's HTTP API, version 2
// (https://toncenter.com/api/v2/).
//
// Implemented to TON Center's public documentation and tested only against a fake of it, never against the real
// service. Facts it relies on: getTransactions (GET, address, limit up to 100, and lt with hash to continue from
// a transaction, which is returned first) answers {ok: true, result: [transactions, newest first]} or {ok:
// false, error, code}; a transaction has utime, transaction_id {lt, hash} (the hash in base64), in_msg {source,
// destination, value, message, msg_data} and out_msgs; an incoming transfer has a source, an external message
// has an empty one; value is nanoton as a decimal string; a text comment is in message and, in base64, in
// msg_data when its @type is msg.dataText. The API key goes in the X-API-Key header, never in the URL. The
// account is read as of the newest masterchain block, so what it returns is already referred to by the
// masterchain, which is TON's finality; the domain still waits a minute before acting on a transaction.
package toncenter

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
)

const (
	productionBase = "https://toncenter.com"

	// MaxLimit is the most transactions one call returns.
	MaxLimit = 100

	maxComment = 256 // bytes kept of a comment
)

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// ValidAPIKey reports whether s can be a TON Center API key (it goes in a header, so nothing else is allowed).
func ValidAPIKey(s string) bool { return keyRe.MatchString(s) }

// Config configures the client.
type Config struct {
	// APIKey is TON Center's key, optional (without one the service allows about one call a second).
	APIKey string
	// Proxy routes every call through this http(s) proxy.
	Proxy string
	// BaseURL overrides the API host (tests).
	BaseURL string
	// HTTPClient overrides the HTTP client (tests).
	HTTPClient *http.Client
}

// Client reads TON Center.
type Client struct {
	key  string
	base string
	hc   *http.Client
}

// New validates the configuration.
func New(c Config) (*Client, error) {
	key := strings.TrimSpace(c.APIKey)
	if key != "" && !keyRe.MatchString(key) {
		return nil, errors.New("toncenter: the API key must be the key from TON Center (letters, digits, - and _)")
	}
	base := productionBase
	if c.BaseURL != "" {
		base = strings.TrimRight(c.BaseURL, "/")
	}
	hc := c.HTTPClient
	if hc == nil {
		var err error
		if hc, err = gateway.NewHTTPClient("toncenter", c.Proxy); err != nil {
			return nil, err
		}
	}
	return &Client{key: key, base: base, hc: hc}, nil
}

// CloseIdleConnections drops the client's idle connections.
func (c *Client) CloseIdleConnections() { c.hc.CloseIdleConnections() }

// text reads a JSON string or number as text.
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		*t = ""
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*t = text(v)
	default:
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return fmt.Errorf("toncenter: not a text or a number: %q", s)
		}
		*t = text(s)
	}
	return nil
}

type msgJSON struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Value       text   `json:"value"`
	Message     string `json:"message"`
	MsgData     struct {
		Type string `json:"@type"`
		Text string `json:"text"`
	} `json:"msg_data"`
}

type txJSON struct {
	UTime         gateway.FlexInt `json:"utime"`
	TransactionID struct {
		LT   gateway.FlexInt `json:"lt"`
		Hash string          `json:"hash"`
	} `json:"transaction_id"`
	InMsg   *msgJSON  `json:"in_msg"`
	OutMsgs []msgJSON `json:"out_msgs"`
}

type answer struct {
	OK     bool              `json:"ok"`
	Result []json.RawMessage `json:"result"`
	Error  string            `json:"error"`
	Code   gateway.FlexInt   `json:"code"`
}

// hashHex reads a transaction hash as the API reports it (base64, standard or URL-safe, or hex) and returns it as
// 64 lower-case hex digits, or "" when it is none of these.
func hashHex(h string) string {
	if len(h) == 64 {
		if b, err := hex.DecodeString(h); err == nil && len(b) == 32 {
			return strings.ToLower(h)
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		if b, err := enc.DecodeString(h); err == nil && len(b) == 32 {
			return hex.EncodeToString(b)
		}
	}
	return ""
}

// comment reads a transfer's text comment: the raw text of msg_data when it is a text, else message. A comment
// that is not valid text is none. It is cut to maxComment bytes (on a character).
func (m *msgJSON) comment() string {
	c := m.Message
	if m.MsgData.Type == "msg.dataText" && m.MsgData.Text != "" {
		if b, err := base64.StdEncoding.DecodeString(m.MsgData.Text); err == nil && validText(string(b)) {
			c = string(b)
		}
	}
	if !validText(c) {
		return ""
	}
	if len(c) > maxComment {
		r := []rune(c)
		for len(string(r)) > maxComment {
			r = r[:len(r)-1]
		}
		c = string(r)
	}
	return c
}

// validText: valid UTF-8 without the replacement character or control characters (a comment that is not
// plain text is none; the database could not keep a NUL anyway).
func validText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r == utf8.RuneError || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// Transactions implements gateway.TONSource.
func (c *Client) Transactions(ctx context.Context, q gateway.TONQuery) ([]gateway.TONTx, error) {
	limit := q.Limit
	if limit <= 0 || limit > MaxLimit {
		limit = MaxLimit
	}
	v := url.Values{"address": {q.Address}, "limit": {strconv.Itoa(limit)}, "archival": {"false"}}
	if q.BeforeLT > 0 && q.BeforeHash != "" {
		v.Set("lt", strconv.FormatInt(q.BeforeLT, 10))
		v.Set("hash", q.BeforeHash)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v2/getTransactions?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.key != "" {
		req.Header.Set("X-API-Key", c.key)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // never echo the URL
			err = ue.Err
		}
		return nil, &netError{op: "request failed", kind: gateway.DescribeTransport(err), err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, &netError{op: "read response", kind: gateway.DescribeTransport(err), err: err}
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("toncenter: rate limited (an API key raises the limit)")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("toncenter: the API key was refused (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("toncenter: the service failed (HTTP %d)", resp.StatusCode)
	}
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("toncenter: unexpected non-JSON response (HTTP %d)", resp.StatusCode)
	}
	if !a.OK {
		msg := gateway.Sanitize(a.Error)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("toncenter: %s (HTTP %d)", msg, resp.StatusCode)
	}
	out := make([]gateway.TONTx, 0, len(a.Result))
	for _, item := range a.Result {
		var t txJSON
		if err := json.Unmarshal(item, &t); err != nil {
			continue // one odd transaction does not hide the others; the domain counts only what it can read
		}
		tx := gateway.TONTx{Hash: hashHex(t.TransactionID.Hash), RawHash: t.TransactionID.Hash,
			LT: int64(t.TransactionID.LT), Time: time.Unix(int64(t.UTime), 0)}
		if m := t.InMsg; m != nil {
			tx.HasMsg, tx.Source, tx.Destination, tx.Value, tx.Comment = true, m.Source, m.Destination, string(m.Value), m.comment()
		}
		for _, o := range t.OutMsgs {
			tx.OutMsgs = append(tx.OutMsgs, gateway.TONOutMsg{Destination: o.Destination, Value: string(o.Value)})
		}
		out = append(out, tx)
	}
	return out, nil
}

// netError is a call whose connection failed, told by its kind only: the underlying error names the proxy's
// address (part of a secret URL).
type netError struct {
	op   string
	kind string
	err  error
}

func (e *netError) Error() string { return "toncenter: " + e.op + ": " + e.kind }
func (e *netError) Unwrap() error { return e.err }
