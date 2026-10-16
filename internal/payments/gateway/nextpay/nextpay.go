// Package nextpay is the NextPay direct gateway web service (Iranian cards).
//
// Implemented to NextPay's public documentation (https://nextpay.org/nx/docs)
// and tested only against a fake of it, never against the real gateway. Facts it
// relies on: the token call (POST /nx/gateway/token) answers {code: -1, trans_id}
// when it issued a token; the customer is sent to /nx/gateway/payment/<trans_id>
// and returns to the callback URL by GET with trans_id, order_id and amount, none of
// which is trusted: only verify settles. verify (POST /nx/gateway/verify) answers
// code 0 for a paid payment; -1 (waiting), -3 (waiting for the bank) are not paid
// yet, -2 (rejected) and -4 (cancelled) are not paid, and a payment must be
// verified within 10 minutes of being paid or the money goes back to the payer.
// Amounts are Toman by default; BOBRES asks for Rial (currency IRR) like every
// other gateway. There is no sandbox, and a payment is never refunded here: the
// refund call (verify with refund_request) is never made and its answer (-90) is
// never read as paid.
package nextpay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"encoding/json"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
)

const (
	// Name is the provider name.
	Name = "nextpay"

	productionBase = "https://nextpay.org"

	minRial = 10_000        // 1,000 Toman
	maxRial = 1_000_000_000 // 100,000,000 Toman

	maxDescription = 150 // NextPay rejects longer text (-64)
	maxOrderID     = 50
	maxCallback    = 100 // -69/-73: the return address is at most 100 characters
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidID reports whether s can be a NextPay trans_id (the callback checks it
// before it asks the database).
func ValidID(s string) bool { return uuidRe.MatchString(s) }

// Config configures the gateway.
type Config struct {
	APIKey string // UUID from the NextPay panel
	// Sandbox is refused: NextPay has none, and a flag that silently did
	// nothing would let an owner think payments are test payments.
	Sandbox bool
	// Proxy routes every API call through this http(s) proxy.
	Proxy string
	// BaseURL overrides the API host (tests).
	BaseURL string
	// HTTPClient overrides the HTTP client (tests).
	HTTPClient *http.Client
}

// Gateway talks to NextPay.
type Gateway struct {
	key      string
	base     string
	override bool
	hc       *http.Client
}

// New validates the configuration.
func New(c Config) (*Gateway, error) {
	if c.Sandbox {
		return nil, errors.New("nextpay: NextPay has no sandbox")
	}
	k := strings.ToLower(strings.TrimSpace(c.APIKey))
	if !uuidRe.MatchString(k) {
		return nil, errors.New("nextpay: the API key must be the UUID from the NextPay panel")
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
	return &Gateway{key: k, base: base, override: c.BaseURL != "", hc: hc}, nil
}

// Name implements gateway.Gateway.
func (g *Gateway) Name() string { return Name }

// CloseIdleConnections drops the client's idle connections.
func (g *Gateway) CloseIdleConnections() { g.hc.CloseIdleConnections() }

// PayURL is where the customer pays a token.
func (g *Gateway) PayURL(transID string) string { return g.base + "/nx/gateway/payment/" + transID }

// Create requests a token. Every call creates a new payment, so callers store
// its trans_id per intent.
func (g *Gateway) Create(ctx context.Context, c gateway.Charge) (gateway.Started, error) {
	if c.Currency != "IRR" {
		return gateway.Started{}, fmt.Errorf("nextpay: amount must be in IRR, got %q", c.Currency)
	}
	if c.Amount < minRial || c.Amount > maxRial {
		return gateway.Started{}, fmt.Errorf("nextpay: amount %d Rial is outside %d..%d", c.Amount, minRial, maxRial)
	}
	if c.CallbackURL == "" || len(c.CallbackURL) > maxCallback {
		return gateway.Started{}, errors.New("nextpay: callback URL required, at most 100 characters")
	}
	if c.IntentID == "" || len(c.IntentID) > maxOrderID {
		return gateway.Started{}, errors.New("nextpay: the order id must be 1 to 50 characters")
	}
	desc := strings.TrimSpace(c.Description)
	if desc == "" {
		desc = "Payment"
	}
	if r := []rune(desc); len(r) > maxDescription {
		desc = string(r[:maxDescription])
	}
	var data struct {
		Code    *gateway.FlexInt `json:"code"`
		TransID string           `json:"trans_id"`
	}
	err := g.call(ctx, "/nx/gateway/token", url.Values{
		"order_id": {c.IntentID}, "amount": {fmt.Sprint(c.Amount)}, "currency": {"IRR"},
		"callback_uri": {c.CallbackURL}, "payer_desc": {desc},
	}, &data)
	if err != nil {
		return gateway.Started{}, err
	}
	if data.Code == nil {
		return gateway.Started{}, errors.New("nextpay: unexpected token response")
	}
	if *data.Code != -1 { // the token call answers -1 when it issued one
		return gateway.Started{}, &Error{Code: int(*data.Code), Message: Describe(int(*data.Code))}
	}
	id := strings.ToLower(data.TransID)
	if !uuidRe.MatchString(id) {
		return gateway.Started{}, errors.New("nextpay: unexpected token response")
	}
	return gateway.Started{ExternalID: id, PayURL: g.PayURL(id)}, nil
}

// Check verifies the payment (code 0 = paid). verify is also the only status
// call, so an unpaid payment is told by its code: -1 and -3 are in progress,
// -2 and -4 are not paid. A rejected or cancelled attempt may be retried on the
// same token, so it is final only once the customer came back (c.Returned) or
// the pay window ended.
func (g *Gateway) Check(ctx context.Context, id string, c gateway.Charge) (gateway.Result, error) {
	if !uuidRe.MatchString(id) {
		return gateway.Result{State: gateway.Failed, Reason: "not_found"}, nil
	}
	var data struct {
		Code    *gateway.FlexInt `json:"code"`
		Amount  gateway.FlexInt  `json:"amount"`
		OrderID string           `json:"order_id"`
		Ref     string           `json:"Shaparak_Ref_Id"`
	}
	err := g.call(ctx, "/nx/gateway/verify", url.Values{
		"trans_id": {id}, "amount": {fmt.Sprint(c.Amount)}, "currency": {"IRR"},
	}, &data)
	if err != nil {
		return gateway.Result{}, err // network or decoding: retry later
	}
	if data.Code == nil { // no code is not code 0
		return gateway.Result{}, errors.New("nextpay: unexpected verify response")
	}
	switch code := int(*data.Code); {
	case code == 0:
		if data.OrderID != c.IntentID {
			return gateway.Result{State: gateway.Failed, Reason: "not_found"}, nil
		}
		amount := int64(data.Amount)
		// Asked in Rial; if NextPay answers in Toman anyway, the same amount
		// shows as a tenth. Anything else is reported as it is, and the
		// caller refuses it as an amount mismatch.
		if amount*10 == c.Amount {
			amount = c.Amount
		}
		if amount <= 0 {
			return gateway.Result{}, errors.New("nextpay: verify answer without an amount")
		}
		return gateway.Result{State: gateway.Paid, Amount: amount, Currency: "IRR", Reference: gateway.Sanitize(data.Ref)}, nil
	case code == -1 || code == -3: // waiting to be paid, or for the bank
		return gateway.Result{State: gateway.Pending}, nil
	case code == -2 || code == -4: // rejected by the payer or the bank, cancelled
		if c.Returned || c.ExpiresAt.IsZero() || !time.Now().Before(c.ExpiresAt) {
			return gateway.Result{State: gateway.Failed, Reason: "gateway_failed"}, nil
		}
		return gateway.Result{State: gateway.Pending}, nil
	case code == -34 || code == -37 || code == -38: // no such token
		return gateway.Result{State: gateway.Failed, Reason: "not_found"}, nil
	default:
		// Configuration (-33, -35, -39, -40), a payment already verified or
		// refunded (-25, -49, -90), NextPay-side (-42, -45): nothing here may
		// settle a payment, and a human should see it.
		return gateway.Result{}, &Error{Code: code, Message: Describe(code)}
	}
}

// Test checks the API key, the proxy and that the host can be reached, by
// asking about a token that cannot exist. NextPay answers a key it does not
// know with -33/-35/-39/-40 and an unknown token with -34/-37/-38, so the key
// is told apart from the unknown token. Nothing is created or settled.
func (g *Gateway) Test(ctx context.Context) (gateway.TestResult, error) {
	var data struct {
		Code *gateway.FlexInt `json:"code"`
	}
	err := g.call(ctx, "/nx/gateway/verify", url.Values{
		"trans_id": {"00000000-0000-4000-8000-000000000000"}, "amount": {"10000"}, "currency": {"IRR"},
	}, &data)
	if err != nil {
		return gateway.TestResult{}, err
	}
	if data.Code == nil {
		return gateway.TestResult{}, errors.New("nextpay: unexpected response")
	}
	switch c := int(*data.Code); c {
	case -34, -37, -38, -1, -2, -3, -4, -25, -49: // the key was accepted; the token is not one of ours
		return gateway.TestResult{}, nil
	default:
		return gateway.TestResult{}, &Error{Code: c, Message: Describe(c)}
	}
}

// Error is an error code of NextPay.
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("nextpay: error %d: %s", e.Code, e.Message) }

// Describe names the codes an owner can act on; other codes are told by number.
func Describe(code int) string {
	switch code {
	case -24, -30:
		return "the amount is not accepted"
	case -27:
		return "the order id is not accepted"
	case -32, -69, -70, -73:
		return "the callback address is not accepted"
	case -33, -35, -39, -47:
		return "the API key is wrong"
	case -40:
		return "the API key is blocked"
	case -31, -43, -50:
		return "the account has no usable gateway"
	case -42, -44, -45, -72:
		return "NextPay or the bank is unavailable"
	case -52:
		return "the account is not verified"
	case -25, -49:
		return "the payment was already verified"
	}
	return "the gateway refused the request"
}

// call posts a form and decodes the JSON answer into out.
func (g *Gateway) call(ctx context.Context, path string, form url.Values, out any) error {
	form.Set("api_key", g.key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
		return fmt.Errorf("nextpay: the gateway failed (HTTP %d)", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		// A gateway or proxy error page: never settle on it.
		return fmt.Errorf("nextpay: unexpected non-JSON response (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// netError is a call whose connection failed, told by its kind only: the
// underlying error names the proxy's address (part of a secret URL).
type netError struct {
	op   string
	kind string
	err  error
}

func (e *netError) Error() string { return "nextpay: " + e.op + ": " + e.kind }
func (e *netError) Unwrap() error { return e.err }
