// Package idpay is the IDPay REST v1.1 payment gateway (Iranian cards).
//
// Implemented to IDPay's public documentation (https://idpay.ir/web-service/v1.1/)
// and tested only against a fake of it, never against the real gateway. Facts it
// relies on: the request carries the API key in X-API-KEY and, for the sandbox,
// X-SANDBOX: 1 on the same host; amounts are in Rial (1,000 to 500,000,000);
// creating a payment answers 201 with {id, link} and the customer goes to link;
// the customer returns to our callback with the payment's id (and order_id, status,
// track_id) by GET or POST, whatever the owner chose in the IDPay panel, and none of
// it is trusted: only verify settles. A payment must be verified within 10 minutes
// of being paid or IDPay returns the money to the payer (error 54). verify answers
// status 100 once and 101 on later calls (both paid); inquiry reports a payment's
// status without changing it (1 not paid, 2 failed, 3 error, 4 blocked, 5 and 6
// returned to the payer, 7 cancelled by the payer, 8 sent to the bank, 10 paid and
// waiting for verify, 100 and 101 verified, 200 settled to the merchant); errors come
// with HTTP 4xx/5xx and {error_code, error_message}; and the transactions list, filtered
// to status 10, is a read-only call that doubles as the connection test.
package idpay

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
	// Name is the provider name.
	Name = "idpay"

	productionBase = "https://api.idpay.ir"

	minRial = 1_000       // documented limits
	maxRial = 500_000_000 // 50,000,000 Toman

	maxDescription = 255
	maxOrderID     = 50
	maxCallback    = 2048
)

var (
	keyRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// idRe is a payment id: the docs show 32 hex characters; a wider shape is
	// accepted so a format change does not strand payments, but nothing that
	// could be part of a path or a query.
	idRe = regexp.MustCompile(`^[0-9A-Za-z]{16,64}$`)
)

// ValidID reports whether s can be an IDPay payment id (the callback checks it
// before it asks the database).
func ValidID(s string) bool { return idRe.MatchString(s) }

// Config configures the gateway.
type Config struct {
	APIKey  string // 36-character UUID from the IDPay panel
	Sandbox bool   // X-SANDBOX: 1 on every call
	// Proxy routes every API call through this http(s) proxy, so they leave from
	// the IP registered in the IDPay web service settings.
	Proxy string
	// BaseURL overrides the API host (tests).
	BaseURL string
	// HTTPClient overrides the HTTP client (tests).
	HTTPClient *http.Client
}

// Gateway talks to IDPay.
type Gateway struct {
	key      string
	sandbox  bool
	base     string
	override bool
	hc       *http.Client
}

// New validates the configuration.
func New(c Config) (*Gateway, error) {
	k := strings.ToLower(strings.TrimSpace(c.APIKey))
	if !keyRe.MatchString(k) {
		return nil, errors.New("idpay: the API key must be the 36-character key from the IDPay panel")
	}
	base := productionBase
	if c.BaseURL != "" {
		base = strings.TrimRight(c.BaseURL, "/")
	}
	hc := c.HTTPClient
	if hc == nil {
		var err error
		if hc, err = gateway.NewHTTPClient(Name, c.Proxy); err != nil {
			return nil, err
		}
	}
	return &Gateway{key: k, sandbox: c.Sandbox, base: base, override: c.BaseURL != "", hc: hc}, nil
}

// Name implements gateway.Gateway.
func (g *Gateway) Name() string { return Name }

// CloseIdleConnections drops the client's idle connections (a tested
// candidate or replaced settings that will not be used again).
func (g *Gateway) CloseIdleConnections() { g.hc.CloseIdleConnections() }

// Create requests a payment. Every call creates a new payment, so callers
// store its id per intent.
func (g *Gateway) Create(ctx context.Context, c gateway.Charge) (gateway.Started, error) {
	if c.Currency != "IRR" {
		return gateway.Started{}, fmt.Errorf("idpay: amount must be in IRR, got %q", c.Currency)
	}
	if c.Amount < minRial || c.Amount > maxRial {
		return gateway.Started{}, fmt.Errorf("idpay: amount %d Rial is outside %d..%d", c.Amount, minRial, maxRial)
	}
	if c.CallbackURL == "" || len(c.CallbackURL) > maxCallback {
		return gateway.Started{}, errors.New("idpay: callback URL required, at most 2048 characters")
	}
	if c.IntentID == "" || len(c.IntentID) > maxOrderID {
		return gateway.Started{}, errors.New("idpay: the order id must be 1 to 50 characters")
	}
	desc := strings.TrimSpace(c.Description)
	if desc == "" {
		desc = "Payment"
	}
	if r := []rune(desc); len(r) > maxDescription {
		desc = string(r[:maxDescription])
	}
	var data struct {
		ID   string `json:"id"`
		Link string `json:"link"`
	}
	err := g.call(ctx, "/v1.1/payment", nil, map[string]any{
		"order_id": c.IntentID, "amount": c.Amount, "desc": desc, "callback": c.CallbackURL,
	}, &data)
	if err != nil {
		return gateway.Started{}, err
	}
	if !idRe.MatchString(data.ID) || !g.trustedLink(data.Link) {
		return gateway.Started{}, errors.New("idpay: unexpected payment response")
	}
	return gateway.Started{ExternalID: data.ID, PayURL: data.Link}, nil
}

// trustedLink: the pay link must be https on IDPay's own domain (the fake's
// host when the base URL is overridden), so a bad answer cannot send a
// customer elsewhere.
func (g *Gateway) trustedLink(link string) bool {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if g.override {
		b, err := url.Parse(g.base)
		return err == nil && u.Host == b.Host
	}
	h := u.Hostname()
	return u.Scheme == "https" && (h == "idpay.ir" || strings.HasSuffix(h, ".idpay.ir"))
}

// Check verifies the payment (status 100 or 101 = paid). An unpaid payment
// stays pending while the customer may still pay: until the pay window ends,
// unless the customer already came back from the bank (c.Returned). Only then
// is the inquiry asked, which tells a payment still at the bank (pending) from a
// failed or cancelled one, and if it says the payment went through meanwhile,
// verify runs once more.
func (g *Gateway) Check(ctx context.Context, id string, c gateway.Charge) (gateway.Result, error) {
	if !idRe.MatchString(id) {
		return gateway.Result{State: gateway.Failed, Reason: "not_found"}, nil
	}
	r, unpaid, err := g.verify(ctx, id, c)
	if !unpaid {
		return r, err
	}
	if !c.Returned && !c.ExpiresAt.IsZero() && time.Now().Before(c.ExpiresAt) {
		return gateway.Result{State: gateway.Pending}, nil
	}
	final := c.Returned || c.ExpiresAt.IsZero() || !time.Now().Before(c.ExpiresAt)
	switch g.inquiry(ctx, id, c) {
	case 2, 3, 7: // failed, error, cancelled by the payer
		if final {
			return gateway.Result{State: gateway.Failed, Reason: "gateway_failed"}, nil
		}
	case 5, 6: // returned to the payer
		return gateway.Result{State: gateway.Failed, Reason: "reversed"}, nil
	case 10, 100, 101, 200: // paid between the two calls
		if r, unpaid, err := g.verify(ctx, id, c); !unpaid {
			return r, err
		}
	}
	return gateway.Result{State: gateway.Pending}, nil // not paid, at the bank, blocked or unknown
}

// verifyReply is verify's and inquiry's answer.
type verifyReply struct {
	Status  gateway.FlexInt `json:"status"`
	TrackID text            `json:"track_id"`
	ID      string          `json:"id"`
	OrderID text            `json:"order_id"`
	Amount  gateway.FlexInt `json:"amount"`
	Payment struct {
		TrackID text            `json:"track_id"`
		Amount  gateway.FlexInt `json:"amount"`
	} `json:"payment"`
}

// verify calls IDPay's verify; unpaid reports that it is not paid (yet).
func (g *Gateway) verify(ctx context.Context, id string, c gateway.Charge) (gateway.Result, bool, error) {
	var data verifyReply
	err := g.call(ctx, "/v1.1/payment/verify", nil, map[string]any{"id": id, "order_id": c.IntentID}, &data)
	var ie *Error
	switch {
	case err == nil:
		return g.paid(id, c, &data)
	case !errors.As(err, &ie):
		return gateway.Result{}, false, err // network or decoding: retry later
	}
	switch ie.Code {
	case 54: // the verify window passed: IDPay gave the money back to the payer
		return gateway.Result{State: gateway.Failed, Reason: "reversed"}, false, nil
	case 51, 52: // no such payment
		return gateway.Result{State: gateway.Failed, Reason: "not_found"}, false, nil
	case 53: // not payable to verify: not paid (yet), or already closed; the inquiry says which
		return gateway.Result{State: gateway.Pending}, true, nil
	default: // configuration (11-15, 21-24), validation (3x), IDPay-side (-1)
		return gateway.Result{}, false, err
	}
}

// paid reads a verify answer. 100 and 101 are paid; anything else is not
// trusted. The answer must be about this payment and this order.
func (g *Gateway) paid(id string, c gateway.Charge, d *verifyReply) (gateway.Result, bool, error) {
	if d.Status != 100 && d.Status != 101 {
		return gateway.Result{}, false, fmt.Errorf("idpay: unexpected verify status %d", d.Status)
	}
	if d.ID != id || string(d.OrderID) != c.IntentID {
		// The answer is about another payment: nothing here is ours to settle.
		return gateway.Result{State: gateway.Failed, Reason: "not_found"}, false, nil
	}
	// amount is what was registered at creation; payment.amount what the payer
	// paid (it may include a fee the payer chose to bear, so it is only
	// trusted when it is less than the registered amount).
	if d.Amount <= 0 {
		return gateway.Result{}, false, errors.New("idpay: verify answer without an amount")
	}
	paid := int64(d.Amount)
	if p := int64(d.Payment.Amount); p > 0 && p < paid {
		paid = p
	}
	ref := string(d.Payment.TrackID)
	if ref == "" {
		ref = string(d.TrackID)
	}
	return gateway.Result{State: gateway.Paid, Amount: paid, Currency: "IRR", Reference: ref}, false, nil
}

// inquiry returns IDPay's status code of a payment, or 0 when it cannot tell.
// It never confirms a payment.
func (g *Gateway) inquiry(ctx context.Context, id string, c gateway.Charge) int {
	var data verifyReply
	if err := g.call(ctx, "/v1.1/payment/inquiry", nil, map[string]any{"id": id, "order_id": c.IntentID}, &data); err != nil {
		return 0
	}
	if data.ID != "" && data.ID != id {
		return 0
	}
	return int(data.Status)
}

// Test checks the API key, the sandbox flag, the proxy and that the calls come
// from the IP registered with IDPay, by listing the payments paid and still
// waiting to be verified. That call is read-only: nothing is created or settled.
func (g *Gateway) Test(ctx context.Context) (gateway.TestResult, error) {
	var data struct {
		Attachment struct {
			Total gateway.FlexInt `json:"total_count"`
		} `json:"attachment"`
		Records []json.RawMessage `json:"records"`
	}
	if err := g.call(ctx, "/v1.1/payment/transactions", url.Values{"page": {"0"}, "page_size": {"1"}},
		map[string]any{"status": []int{10}}, &data); err != nil {
		return gateway.TestResult{}, err
	}
	n := int(data.Attachment.Total)
	if n == 0 {
		n = len(data.Records)
	}
	return gateway.TestResult{Unverified: n}, nil
}

// Error is an error answer from IDPay.
type Error struct {
	Status  int
	Code    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("idpay: error %d (HTTP %d): %s", e.Code, e.Status, e.Message)
}

// call posts JSON and decodes the answer into out. An error answer of IDPay's
// ({error_code, error_message}) is returned as *Error.
func (g *Gateway) call(ctx context.Context, path string, query url.Values, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	target := g.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-KEY", g.key)
	if g.sandbox {
		req.Header.Set("X-SANDBOX", "1")
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) { // never echo the URL
			err = ue.Err
		}
		return &netError{op: "request failed", kind: gateway.DescribeTransport(err), err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		// A connection cut after the headers names the proxy's address too.
		return &netError{op: "read response", kind: gateway.DescribeTransport(err), err: err}
	}
	if resp.StatusCode == http.StatusNoContent || len(bytes.TrimSpace(raw)) == 0 {
		if resp.StatusCode >= 300 {
			return &Error{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		return nil
	}
	var env struct {
		Code    gateway.FlexInt `json:"error_code"`
		Message string          `json:"error_message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("idpay: unexpected non-JSON response (HTTP %d)", resp.StatusCode)
	}
	if env.Code != 0 || resp.StatusCode >= 300 {
		msg := gateway.Sanitize(env.Message)
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &Error{Status: resp.StatusCode, Code: int(env.Code), Message: msg}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("idpay: decode response: %w", err)
		}
	}
	return nil
}

// netError is a call whose connection failed, told by its kind only: the
// underlying error names the proxy's address (part of a secret URL), and this
// text ends up in logs, gateway events and the dashboard.
type netError struct {
	op   string // "request failed" or "read response"
	kind string
	err  error
}

func (e *netError) Error() string { return "idpay: " + e.op + ": " + e.kind }
func (e *netError) Unwrap() error { return e.err }

// text reads a JSON string or number as text (IDPay's samples quote numbers).
type text string

func (t *text) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*t = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*t = text(gateway.Sanitize(v))
		return nil
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return fmt.Errorf("idpay: not a text or a number: %q", s)
	}
	*t = text(s)
	return nil
}
