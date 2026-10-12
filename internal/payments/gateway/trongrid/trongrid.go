// Package trongrid reads a TRON address's incoming TRC20 transfers from
// TronGrid (https://developers.tron.network/reference/get-trc20-transaction-info-by-account-address).
//
// Facts this code relies on: GET /v1/accounts/{address}/transactions/trc20
// lists an address's TRC20 transfers; only_to=true keeps the incoming ones,
// only_confirmed=true the confirmed ones (about 19 blocks, a minute),
// contract_address one token, min_timestamp (milliseconds, inclusive) and
// order_by=block_timestamp,asc read forward from a point, limit is at most 200
// per page, and meta.fingerprint, sent back as the fingerprint parameter, is
// the next page. Each item has transaction_id, token_info.address (the token
// contract), from, to, type ("Transfer" or "Approval"), value (a decimal
// string in the token's smallest unit) and block_timestamp (milliseconds).
// The list has no log index. An API key (TRON-PRO-API-KEY header) is optional
// and raises the rate limit.
//
// Nothing in an answer is trusted here beyond its shape: the payments domain
// validates every transfer (token contract, receiver, type, amount) before it
// counts. The client never follows redirects (the API key would go along)
// and never builds a page URL from the answer, only the fingerprint.
package trongrid

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
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
)

const (
	// DefaultBaseURL is TronGrid's mainnet API.
	DefaultBaseURL = "https://api.trongrid.io"
	// USDTContract is Tether's USDT token contract on TRON mainnet. A token
	// is USDT only when its contract is this one: anyone can deploy a token
	// named "USDT".
	USDTContract = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	// MaxLimit is the most transfers TronGrid returns per page.
	MaxLimit = 200
)

var (
	// apiKeyRe: TronGrid keys are UUIDs; anything in this set is safe in a header.
	apiKeyRe      = regexp.MustCompile(`^[A-Za-z0-9-]{8,128}$`)
	fingerprintRe = regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`)
)

// ValidAPIKey reports whether k can be a TronGrid API key.
func ValidAPIKey(k string) bool { return apiKeyRe.MatchString(k) }

// Config configures the client.
type Config struct {
	APIKey string // TRON-PRO-API-KEY, optional
	// Proxy routes every call through this http(s) proxy (it may carry a password).
	Proxy string
	// BaseURL overrides the API host (tests).
	BaseURL string
	// HTTPClient overrides the HTTP client (tests).
	HTTPClient *http.Client
}

// Client talks to TronGrid.
type Client struct {
	key  string
	base string
	hc   *http.Client
}

// New validates the configuration.
func New(c Config) (*Client, error) {
	key := strings.TrimSpace(c.APIKey)
	if key != "" && !ValidAPIKey(key) {
		return nil, errors.New("trongrid: the API key must be the key from the TronGrid dashboard")
	}
	base := DefaultBaseURL
	if c.BaseURL != "" {
		base = strings.TrimRight(c.BaseURL, "/")
	}
	hc := c.HTTPClient
	if hc == nil {
		tr := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
			IdleConnTimeout: 90 * time.Second}
		if p := strings.TrimSpace(c.Proxy); p != "" {
			pu, err := url.Parse(p)
			if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
				return nil, errors.New("trongrid: the proxy must be an http(s) URL")
			}
			tr.Proxy = http.ProxyURL(pu)
		}
		hc = &http.Client{Timeout: 20 * time.Second, Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Client{key: key, base: base, hc: hc}, nil
}

// CloseIdleConnections drops the client's idle connections (replaced or
// tested settings that will not be used again).
func (c *Client) CloseIdleConnections() { c.hc.CloseIdleConnections() }

// Transfers implements gateway.ChainSource: one page of the address's
// confirmed incoming transfers of the token, oldest first.
func (c *Client) Transfers(ctx context.Context, q gateway.ChainQuery) (gateway.ChainPage, error) {
	if q.Address == "" || q.Token == "" {
		return gateway.ChainPage{}, errors.New("trongrid: address and token required")
	}
	limit := q.Limit
	if limit <= 0 || limit > MaxLimit {
		limit = MaxLimit
	}
	v := url.Values{}
	v.Set("only_to", "true")
	v.Set("only_confirmed", "true")
	v.Set("contract_address", q.Token)
	v.Set("order_by", "block_timestamp,asc")
	v.Set("limit", strconv.Itoa(limit))
	if !q.Since.IsZero() {
		v.Set("min_timestamp", strconv.FormatInt(max(q.Since.UnixMilli(), 0), 10))
	}
	if q.Next != "" {
		if len(q.Next) > 1024 || !fingerprintRe.MatchString(q.Next) {
			return gateway.ChainPage{}, errors.New("trongrid: unexpected page cursor")
		}
		v.Set("fingerprint", q.Next)
	}
	u := c.base + "/v1/accounts/" + url.PathEscape(q.Address) + "/transactions/trc20?" + v.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return gateway.ChainPage{}, errors.New("trongrid: bad request")
	}
	req.Header.Set("Accept", "application/json")
	if c.key != "" {
		req.Header.Set("TRON-PRO-API-KEY", c.key)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // never echo the URL
			err = ue.Err
		}
		return gateway.ChainPage{}, &netError{op: "request failed", kind: gateway.DescribeTransport(err), err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return gateway.ChainPage{}, &netError{op: "read response", kind: gateway.DescribeTransport(err), err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return gateway.ChainPage{}, statusError(resp.StatusCode)
	}
	var body struct {
		Data    []item `json:"data"`
		Success *bool  `json:"success"`
		Meta    struct {
			Fingerprint string `json:"fingerprint"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return gateway.ChainPage{}, errors.New("trongrid: unexpected response (not the transfers list)")
	}
	if body.Success == nil || !*body.Success {
		return gateway.ChainPage{}, errors.New("trongrid: the request was not successful")
	}
	page := gateway.ChainPage{Transfers: make([]gateway.ChainTransfer, 0, len(body.Data))}
	for _, it := range body.Data {
		page.Transfers = append(page.Transfers, gateway.ChainTransfer{
			TxID: it.TransactionID, From: it.From, To: it.To, Token: it.TokenInfo.Address,
			Type: it.Type, Value: string(it.Value), BlockTS: time.UnixMilli(it.BlockTimestamp),
		})
	}
	if len(body.Data) > 0 {
		page.Next = body.Meta.Fingerprint
	}
	return page, nil
}

type item struct {
	TransactionID string `json:"transaction_id"`
	TokenInfo     struct {
		Address string `json:"address"`
	} `json:"token_info"`
	BlockTimestamp int64      `json:"block_timestamp"`
	From           string     `json:"from"`
	To             string     `json:"to"`
	Type           string     `json:"type"`
	Value          flexDigits `json:"value"`
}

// flexDigits keeps a JSON string or number as its text ("" for anything
// else); the domain decides whether it is an amount.
type flexDigits string

func (f *flexDigits) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = flexDigits(s)
		return nil
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		*f = flexDigits(n.String())
		return nil
	}
	*f = ""
	return nil
}

// statusError explains an HTTP error without echoing the body.
func statusError(code int) error {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("trongrid: the API key was refused (HTTP %d)", code)
	case code == http.StatusTooManyRequests:
		return fmt.Errorf("trongrid: rate limited (HTTP %d): set a TronGrid API key", code)
	case code >= 300 && code < 400:
		return fmt.Errorf("trongrid: unexpected redirect (HTTP %d)", code)
	default:
		return fmt.Errorf("trongrid: HTTP %d", code)
	}
}

// netError is a call whose connection failed, told by its kind only: the
// underlying error names the proxy's address (part of a secret URL).
type netError struct {
	op   string // "request failed" or "read response"
	kind string
	err  error
}

func (e *netError) Error() string { return "trongrid: " + e.op + ": " + e.kind }
func (e *netError) Unwrap() error { return e.err }
