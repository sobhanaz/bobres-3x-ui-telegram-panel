// Package aqayepardakht is the Aqayepardakht web service, version 2 (Iranian cards).
//
// Implemented to the gateway's public documentation (https://aqayepardakht.ir/api/, the page dated 5
// Shahrivar 1405) and tested only against a fake of it, never against the real gateway. Facts it relies on:
// every call is a JSON POST carrying the merchant's PIN; creating a payment (POST /api/v2/create) answers
// 200 {status: "success", transid}, an error answers 422 {status: "error", code}; the customer goes to
// /startpay/<transid> (/startpay/sandbox/<transid> for the PIN "sandbox", the test system), and the page
// refuses a customer whose browser does not send the terminal's registered domain as the Referer (error 16),
// so BOBRES opens it from its own pay page like Zarinpal's; the customer returns to the callback URL by POST
// (the default) or GET (callback_method, which BOBRES sets to GET as the documentation recommends) with
// transid, cardnumber, tracking_number, invoice_id, bank and status, none of which is trusted: only verify
// settles. verify (POST /api/v2/verify {pin, amount, transid}) answers 200 {status: "success", code: "1"} for
// a paid payment; an error answer with code 0 is a payment not completed, and code 2 a payment that was
// verified before (and paid). The same numbers 1 to 17 are also the errors of the error table (2 is "the PIN
// is empty"), whose negative form is -1 to -17: BOBRES always sends a PIN, so a positive 2 is the verify
// result, and -2 is the error. verify refuses an amount that differs from the payment's (error 10): its
// success answer is the gateway confirming the amount BOBRES sent, which is the amount the payment was
// created with; it carries no amount of its own. Amounts are Toman, 1,000 to 400,000,000; BOBRES holds Rial
// like every other gateway and converts (Rial / 10) on the wire. A payment is never refunded here.
package aqayepardakht

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
	Name = "aqayepardakht"

	productionBase = "https://panel.aqayepardakht.ir"

	// SandboxPIN is the PIN of the gateway's test system: a payment made with it is never real money.
	SandboxPIN = "sandbox"

	minToman = 1_000       // documented limits
	maxToman = 400_000_000 // 400,000,000 Toman

	maxDescription = 150 // the documentation names no limit; short is safe
	maxInvoiceID   = 50
	maxCallback    = 500
)

var (
	// pinRe is a PIN: printable ASCII without spaces. The documentation shows no format, so nothing narrower
	// is assumed; the case is kept (a PIN is not lowercased).
	pinRe = regexp.MustCompile(`^[\x21-\x7e]{4,100}$`)
	// idRe is a payment's transid. The documentation shows no format; this is a superset of the numeric and
	// the alphanumeric ids, and nothing that could be part of a path or a query.
	idRe = regexp.MustCompile(`^[0-9A-Za-z_-]{1,64}$`)
)

// ValidID reports whether s can be an Aqayepardakht transid (the callback checks it before it asks the
// database).
func ValidID(s string) bool { return idRe.MatchString(s) }

// ValidPIN reports whether s can be a PIN.
func ValidPIN(s string) bool { return pinRe.MatchString(s) }

// Config configures the gateway.
type Config struct {
	PIN string // the merchant's PIN from the Aqayepardakht panel, or "sandbox"
	// Sandbox says the settings use the test system. It is the PIN "sandbox" and nothing else, so the two
	// must agree: a flag that disagreed with the PIN would let an owner think a payment is a test payment.
	Sandbox bool
	// Proxy routes every API call through this http(s) proxy.
	Proxy string
	// BaseURL overrides the API host (tests).
	BaseURL string
	// HTTPClient overrides the HTTP client (tests).
	HTTPClient *http.Client
}

// Gateway talks to Aqayepardakht.
type Gateway struct {
	pin     string
	sandbox bool
	base    string
	hc      *http.Client
}

// New validates the configuration.
func New(c Config) (*Gateway, error) {
	pin := strings.TrimSpace(c.PIN)
	if !pinRe.MatchString(pin) {
		return nil, errors.New("aqayepardakht: the PIN must be the gateway's PIN from the Aqayepardakht panel")
	}
	if sandbox := pin == SandboxPIN; sandbox != c.Sandbox {
		return nil, errors.New(`aqayepardakht: the sandbox is the PIN "sandbox", not a switch`)
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
	return &Gateway{pin: pin, sandbox: c.Sandbox, base: base, hc: hc}, nil
}

// Name implements gateway.Gateway.
func (g *Gateway) Name() string { return Name }

// CloseIdleConnections drops the client's idle connections (a tested candidate or replaced settings that
// will not be used again).
func (g *Gateway) CloseIdleConnections() { g.hc.CloseIdleConnections() }

// PayURL is where the customer pays a transaction. It is built here from the transid, never taken from an
// answer.
func (g *Gateway) PayURL(transID string) string {
	if g.sandbox {
		return g.base + "/startpay/sandbox/" + transID
	}
	return g.base + "/startpay/" + transID
}

// toman converts a charge's Rial into the Toman the gateway takes, or says why it cannot.
func toman(c gateway.Charge) (int64, error) {
	if c.Currency != "IRR" {
		return 0, fmt.Errorf("aqayepardakht: amount must be in IRR, got %q", c.Currency)
	}
	if c.Amount%10 != 0 {
		return 0, fmt.Errorf("aqayepardakht: amount %d Rial is not a whole number of Toman", c.Amount)
	}
	t := c.Amount / 10
	if t < minToman || t > maxToman {
		return 0, fmt.Errorf("aqayepardakht: amount %d Toman is outside %d..%d", t, minToman, maxToman)
	}
	return t, nil
}

// Create requests a transaction. Every call creates a new one, so callers store its transid per intent.
func (g *Gateway) Create(ctx context.Context, c gateway.Charge) (gateway.Started, error) {
	t, err := toman(c)
	if err != nil {
		return gateway.Started{}, err
	}
	if c.CallbackURL == "" || len(c.CallbackURL) > maxCallback {
		return gateway.Started{}, errors.New("aqayepardakht: callback URL required, at most 500 characters")
	}
	if c.IntentID == "" || len(c.IntentID) > maxInvoiceID {
		return gateway.Started{}, errors.New("aqayepardakht: the invoice id must be 1 to 50 characters")
	}
	desc := strings.TrimSpace(c.Description)
	if desc == "" {
		desc = "Payment"
	}
	if r := []rune(desc); len(r) > maxDescription {
		desc = string(r[:maxDescription])
	}
	var data reply
	err = g.call(ctx, "/api/v2/create", map[string]any{
		"pin": g.pin, "amount": t, "callback": c.CallbackURL, "callback_method": "GET",
		"invoice_id": c.IntentID, "description": desc,
	}, &data)
	if err != nil {
		return gateway.Started{}, err
	}
	if data.Status != "success" || !idRe.MatchString(string(data.TransID)) {
		if data.Status == "error" && data.Code != nil {
			return gateway.Started{}, &Error{Code: int(*data.Code), Message: Describe(int(*data.Code))}
		}
		return gateway.Started{}, errors.New("aqayepardakht: unexpected create response")
	}
	id := string(data.TransID)
	return gateway.Started{ExternalID: id, PayURL: g.PayURL(id)}, nil
}

// Check verifies the payment: success with code 1 is paid. Code 2 (verified before) is paid too: this is the
// answer to a verify whose own answer was lost, and the caller settles once, in its database. An unpaid
// payment (code 0) stays pending while the customer may still pay: until the pay window ends, unless the
// customer already came back from the bank (c.Returned), when it failed.
func (g *Gateway) Check(ctx context.Context, id string, c gateway.Charge) (gateway.Result, error) {
	if !idRe.MatchString(id) {
		return gateway.Result{State: gateway.Failed, Reason: "not_found"}, nil
	}
	t, err := toman(c)
	if err != nil {
		return gateway.Result{}, err // never verify with an amount that cannot be the payment's
	}
	var data reply
	if err := g.call(ctx, "/api/v2/verify", map[string]any{"pin": g.pin, "amount": t, "transid": id}, &data); err != nil {
		return gateway.Result{}, err // network or decoding: retry later
	}
	switch {
	case data.Status == "success":
		if data.Code == nil || *data.Code != 1 {
			return gateway.Result{}, errors.New("aqayepardakht: unexpected verify response")
		}
		return paid(c), nil
	case data.Status == "error" && data.Code != nil:
		code := int(*data.Code)
		switch code {
		case 0: // payment not completed
			if c.Returned || c.ExpiresAt.IsZero() || !time.Now().Before(c.ExpiresAt) {
				return gateway.Result{State: gateway.Failed, Reason: "gateway_failed"}, nil
			}
			return gateway.Result{State: gateway.Pending}, nil
		case 2: // verified before, and paid
			return paid(c), nil
		case 8, -8: // no such transaction
			return gateway.Result{State: gateway.Failed, Reason: "not_found"}, nil
		default:
			// Configuration (PIN, domain, a merchant that cannot take requests), an amount that differs from
			// the payment's (10), Aqayepardakht-side: nothing here may settle a payment, and a human should
			// see it.
			return gateway.Result{}, &Error{Code: code, Message: Describe(code)}
		}
	}
	return gateway.Result{}, errors.New("aqayepardakht: unexpected verify response")
}

// paid is a verified payment. The answer has no amount: verify takes ours and refuses (error 10) one that
// differs from the payment's, so a success is the gateway confirming it, and Rial is what the charge held.
func paid(c gateway.Charge) gateway.Result {
	return gateway.Result{State: gateway.Paid, Amount: c.Amount, Currency: "IRR"}
}

// Test checks the PIN, the proxy and that the host can be reached, by verifying a transaction that cannot
// exist. The gateway answers an unknown transaction with error 8 and a PIN it does not know with error 6, so
// the PIN is told apart from the unknown transaction. Nothing is created or settled. (The documentation does
// not say which of the two checks runs first; if the gateway looks the transaction up first, a wrong PIN is
// not caught by this test, and the first real payment shows it.)
func (g *Gateway) Test(ctx context.Context) (gateway.TestResult, error) {
	var data reply
	if err := g.call(ctx, "/api/v2/verify", map[string]any{"pin": g.pin, "amount": minToman, "transid": "0"}, &data); err != nil {
		return gateway.TestResult{}, err
	}
	if data.Status != "error" || data.Code == nil {
		return gateway.TestResult{}, errors.New("aqayepardakht: unexpected response")
	}
	switch c := int(*data.Code); c {
	case 8, -8: // the PIN was accepted; the transaction is not one of ours
		return gateway.TestResult{}, nil
	default:
		return gateway.TestResult{}, &Error{Code: c, Message: Describe(c)}
	}
}

// Error is an error code of Aqayepardakht.
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("aqayepardakht: error %d: %s", e.Code, e.Message) }

// Describe names the codes an owner can act on; other codes are told by number. The table lists each code
// both negative and positive; a code is described by its size.
func Describe(code int) string {
	if code < 0 {
		code = -code
	}
	switch code {
	case 3, 15:
		return "the callback address is not accepted"
	case 4, 5:
		return "the amount is not accepted"
	case 2, 6:
		return "the PIN is wrong"
	case 7, 8:
		return "the payment is not known to the gateway"
	case 9:
		return "the PIN belongs to another gateway"
	case 10:
		return "the amount differs from the payment's"
	case 11:
		return "the gateway is pending approval or inactive"
	case 12:
		return "the gateway cannot take requests for this merchant"
	case 14:
		return "the gateway is in use on another site"
	case 16:
		return "the payment page was opened without the registered domain as its referrer"
	case 17:
		return "the callback method is not accepted"
	}
	return "the gateway refused the request"
}

// reply is the answer of every call: {status, transid} for a created payment, {status, code} otherwise.
type reply struct {
	Status  string           `json:"status"`
	Code    *gateway.FlexInt `json:"code"`
	TransID text             `json:"transid"`
}

// call posts JSON and decodes the answer into out. The documentation gives an error as HTTP 422 with
// {status: "error", code}; any status with such a body is read by the body, and a body that is not JSON (a
// gateway or proxy error page) or a server error is an error to retry, never a verdict.
func (g *Gateway) call(ctx context.Context, path string, body map[string]any, out *reply) error {
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
		return &netError{op: "request failed", kind: gateway.DescribeTransport(err), err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return &netError{op: "read response", kind: gateway.DescribeTransport(err), err: err}
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("aqayepardakht: the gateway failed (HTTP %d)", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("aqayepardakht: unexpected non-JSON response (HTTP %d)", resp.StatusCode)
	}
	// A success is only a success with HTTP 200: anything else that claims one is not believed.
	if out.Status == "success" && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("aqayepardakht: unexpected response (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// netError is a call whose connection failed, told by its kind only: the underlying error names the proxy's
// address (part of a secret URL), and this text ends up in logs, gateway events and the dashboard.
type netError struct {
	op   string // "request failed" or "read response"
	kind string
	err  error
}

func (e *netError) Error() string { return "aqayepardakht: " + e.op + ": " + e.kind }
func (e *netError) Unwrap() error { return e.err }

// text reads a JSON string or number as text (the documentation shows the transid as text; a numeric one is
// not ruled out).
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
		*t = text(v)
		return nil
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return fmt.Errorf("aqayepardakht: not a text or a number: %q", s)
	}
	*t = text(s)
	return nil
}
