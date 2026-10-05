// Package zarinpal is the Zarinpal REST v4 payment gateway (Iranian cards).
//
// Facts this code relies on (see docs/superpowers/specs/2026-10-04-phase2-payments-design.md
// and https://www.zarinpal.com/docs/paymentGateway/): amounts are in Rial, and the
// request says so (currency IRR) so they can never be read as Toman; verify answers
// 100 once and 101 on every later call (both mean paid, so it is safe to retry);
// errors come with HTTP 401/422 and an "errors" OBJECT, either {"code", "message"}
// or keyed by field ({"authority": ["Invalid authority.", "-54"]}), success with an
// "errors" ARRAY; a payment that is not verified in time goes back to the payer
// when the terminal verifies manually; the customer must start from a page on the
// merchant's registered domain; and since Sep 2026 calls are accepted only from
// registered static egress IPs, so an optional proxy can route them through a
// registered (Iranian) server.
package zarinpal

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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
)

const (
	// Name is the provider name.
	Name = "zarinpal"

	productionBase = "https://payment.zarinpal.com"
	sandboxBase    = "https://sandbox.zarinpal.com"

	minRial = 10_000        // the docs disagree (1,000 vs 10,000 Rial); use the stricter
	maxRial = 1_000_000_000 // 100,000,000 Toman (error -41)

	maxDescription = 500
)

var (
	merchantRe  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	authorityRe = regexp.MustCompile(`^[AS][0-9a-zA-Z]{35}$`)
)

// Config configures the gateway.
type Config struct {
	MerchantID string // 36-character UUID from the Zarinpal panel
	Sandbox    bool
	// Proxy routes every API call through this http(s) proxy, so they leave from
	// an IP registered in the Zarinpal panel (e.g. a small Iranian relay).
	Proxy string
	// BaseURL overrides the API host (tests).
	BaseURL string
	// HTTPClient overrides the HTTP client (tests).
	HTTPClient *http.Client
}

// Gateway talks to Zarinpal.
type Gateway struct {
	merchant string
	base     string
	hc       *http.Client
}

// New validates the configuration.
func New(c Config) (*Gateway, error) {
	m := strings.ToLower(strings.TrimSpace(c.MerchantID))
	if !merchantRe.MatchString(m) {
		return nil, errors.New("zarinpal: the merchant id must be the 36-character id from the Zarinpal panel")
	}
	base := productionBase
	if c.Sandbox {
		base = sandboxBase
	}
	if c.BaseURL != "" {
		base = strings.TrimRight(c.BaseURL, "/")
	}
	hc := c.HTTPClient
	if hc == nil {
		tr := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second}
		if c.Proxy != "" {
			pu, err := url.Parse(c.Proxy)
			if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
				return nil, errors.New("zarinpal: the proxy must be an http(s) URL")
			}
			tr.Proxy = http.ProxyURL(pu)
		}
		hc = &http.Client{Timeout: 25 * time.Second, Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Gateway{merchant: m, base: base, hc: hc}, nil
}

// Name implements gateway.Gateway.
func (g *Gateway) Name() string { return Name }

// StartPayURL is where the customer pays an authority.
func (g *Gateway) StartPayURL(authority string) string { return g.base + "/pg/StartPay/" + authority }

// Create requests a payment (Zarinpal "request"). Every call creates a new
// authority, so callers store it per intent.
func (g *Gateway) Create(ctx context.Context, c gateway.Charge) (gateway.Started, error) {
	if c.Currency != "IRR" {
		return gateway.Started{}, fmt.Errorf("zarinpal: amount must be in IRR, got %q", c.Currency)
	}
	if c.Amount < minRial || c.Amount > maxRial {
		return gateway.Started{}, fmt.Errorf("zarinpal: amount %d Rial is outside %d..%d", c.Amount, minRial, maxRial)
	}
	if c.CallbackURL == "" {
		return gateway.Started{}, errors.New("zarinpal: callback URL required")
	}
	desc := strings.TrimSpace(c.Description)
	if desc == "" {
		desc = "Payment"
	}
	if r := []rune(desc); len(r) > maxDescription {
		desc = string(r[:maxDescription])
	}
	var data struct {
		Code      flexInt `json:"code"`
		Authority string  `json:"authority"`
	}
	err := g.call(ctx, "/pg/v4/payment/request.json", map[string]any{
		"merchant_id": g.merchant, "amount": c.Amount, "currency": "IRR", "description": desc,
		"callback_url": c.CallbackURL, "metadata": map[string]string{"order_id": c.IntentID},
	}, &data)
	if err != nil {
		return gateway.Started{}, err
	}
	if data.Code != 100 || !authorityRe.MatchString(data.Authority) {
		return gateway.Started{}, fmt.Errorf("zarinpal: unexpected request response (code %d)", data.Code)
	}
	return gateway.Started{ExternalID: data.Authority, PayURL: g.StartPayURL(data.Authority)}, nil
}

// Check verifies the payment (100 or 101 = paid). An unpaid payment stays
// pending while the customer may still pay: until the pay window ends, unless
// the customer already came back from the bank (c.Returned). Only then is the
// inquiry asked, which tells a payment still at the bank (pending) from a
// failed one, and if it says the payment went through meanwhile, verify runs
// once more. (The sandbox reports IN_BANK for a session nobody has opened yet,
// checked 2026-10-05, but the docs do not say what a session reports between a
// failed attempt at the bank and a retry; not asking earlier also halves the
// calls while the reconciler checks often.)
func (g *Gateway) Check(ctx context.Context, authority string, c gateway.Charge) (gateway.Result, error) {
	if !authorityRe.MatchString(authority) {
		return gateway.Result{State: gateway.Failed, Reason: "not_found"}, nil
	}
	r, unpaid, err := g.verify(ctx, authority, c)
	if !unpaid {
		return r, err
	}
	if !c.Returned && !c.ExpiresAt.IsZero() && time.Now().Before(c.ExpiresAt) {
		return gateway.Result{State: gateway.Pending}, nil
	}
	switch g.inquiry(ctx, authority) {
	case "FAILED":
		return gateway.Result{State: gateway.Failed, Reason: "gateway_failed"}, nil
	case "REVERSED":
		return gateway.Result{State: gateway.Failed, Reason: "reversed"}, nil
	case "PAID", "VERIFIED": // paid between the two calls
		if r, unpaid, err := g.verify(ctx, authority, c); !unpaid {
			return r, err
		}
	}
	return gateway.Result{State: gateway.Pending}, nil // IN_BANK or unknown
}

// verify calls Zarinpal's verify; unpaid reports that it is not paid (yet).
func (g *Gateway) verify(ctx context.Context, authority string, c gateway.Charge) (gateway.Result, bool, error) {
	var data struct {
		Code  flexInt `json:"code"`
		RefID flexInt `json:"ref_id"`
	}
	err := g.call(ctx, "/pg/v4/payment/verify.json", map[string]any{
		"merchant_id": g.merchant, "amount": c.Amount, "authority": authority,
	}, &data)
	var ze *Error
	switch {
	case err == nil && (data.Code == 100 || data.Code == 101):
		ref := ""
		if data.RefID != 0 { // a missing ref_id must not show as receipt number 0
			ref = strconv.FormatInt(int64(data.RefID), 10)
		}
		return gateway.Result{State: gateway.Paid, Amount: c.Amount, Currency: "IRR", Reference: ref}, false, nil
	case err == nil:
		return gateway.Result{}, false, fmt.Errorf("zarinpal: unexpected verify code %d", data.Code)
	case !errors.As(err, &ze):
		return gateway.Result{}, false, err // network or decoding: retry later
	}
	switch ze.Code {
	case -50: // the paid amount differs from ours
		return gateway.Result{State: gateway.Failed, Reason: "amount_mismatch"}, false, nil
	case -53, -54: // another merchant's session, or an invalid authority
		return gateway.Result{State: gateway.Failed, Reason: "not_found"}, false, nil
	case -51, -55: // not (yet) paid; -55 "payment not found" is not final until the window ends
		return gateway.Result{State: gateway.Pending}, true, nil
	default: // configuration (-10, -11, -15, -19), rate limit (-12), Zarinpal-side (-52)
		return gateway.Result{}, false, err
	}
}

// inquiry returns Zarinpal's status of an authority (IN_BANK, PAID, VERIFIED,
// FAILED, REVERSED), or "" when it cannot tell. It never confirms a payment.
func (g *Gateway) inquiry(ctx context.Context, authority string) string {
	var data struct {
		Code   flexInt `json:"code"`
		Status string  `json:"status"`
	}
	if err := g.call(ctx, "/pg/v4/payment/inquiry.json", map[string]any{
		"merchant_id": g.merchant, "authority": authority,
	}, &data); err != nil {
		return ""
	}
	return strings.ToUpper(data.Status)
}

// Error is an error answer from Zarinpal.
type Error struct {
	Status  int
	Code    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("zarinpal: error %d (HTTP %d): %s", e.Code, e.Status, e.Message)
}

// call posts JSON and decodes data into out. Errors are returned as *Error
// when Zarinpal answered with its error envelope.
func (g *Gateway) call(ctx context.Context, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := g.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // never echo the URL
			err = ue.Err
		}
		return fmt.Errorf("zarinpal: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("zarinpal: read response: %w", err)
	}
	var env struct {
		Data    json.RawMessage `json:"data"`
		Errors  json.RawMessage `json:"errors"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("zarinpal: unexpected non-JSON response (HTTP %d)", resp.StatusCode)
	}
	if e := bytes.TrimSpace(env.Errors); len(e) > 0 && e[0] == '{' {
		code, msg := errorDetail(e)
		if msg == "" {
			msg = env.Message
		}
		return &Error{Status: resp.StatusCode, Code: code, Message: sanitize(msg)}
	}
	if resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("zarinpal: decode response: %w", err)
		}
	}
	return nil
}

// errorDetail reads the code and message of either error shape:
// {"code": -51, "message": "..."} or, for validation errors, keyed by field:
// {"authority": ["Invalid authority.", "-54"]}.
func errorDetail(raw json.RawMessage) (int, string) {
	var flat struct {
		Code    flexInt `json:"code"`
		Message string  `json:"message"`
	}
	if err := json.Unmarshal(raw, &flat); err == nil && flat.Code != 0 {
		return int(flat.Code), flat.Message
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return 0, flat.Message
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var items []json.RawMessage
		if json.Unmarshal(fields[k], &items) != nil {
			continue
		}
		code, text := 0, ""
		for _, it := range items {
			var n flexInt
			var s string
			switch {
			case json.Unmarshal(it, &n) == nil && n < 0:
				code = int(n)
			case json.Unmarshal(it, &s) == nil && text == "":
				text = s
			}
		}
		if code != 0 {
			return code, text
		}
	}
	return 0, flat.Message
}

// flexInt accepts 100, "100" and null (Zarinpal's samples mix them).
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(bytes.TrimSpace(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("zarinpal: not a number: %q", s)
	}
	*f = flexInt(n)
	return nil
}

func sanitize(m string) string {
	m = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, m)
	if r := []rune(m); len(r) > 200 {
		m = string(r[:200])
	}
	return m
}
