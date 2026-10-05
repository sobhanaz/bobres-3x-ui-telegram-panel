// Package zpfake is a fake Zarinpal REST v4 API for tests and local runs. It
// mimics the behaviour BOBRES relies on, as documented: Rial amounts (or Toman
// with currency IRT), the success envelope ("errors": []) and both error
// envelopes ("errors": {"code", "message"} and, for an invalid authority, keyed
// by field), verify answering 100 once and 101 afterwards, and inquiry statuses.
//
// Pay/cancel stand in for the customer at the bank: POST /_pay/<authority>
// with ?status=OK or NOK marks the payment and returns the callback URL the
// customer's browser would be sent to.
package zpfake

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

var merchantRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type payment struct {
	amount   int64 // Rial
	currency string
	callback string
	status   string // IN_BANK, PAID, VERIFIED, FAILED
	refID    int64
	desc     string
	orderID  string
}

// Server is a running fake.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	payments map[string]*payment
	seq      int64
	calls    map[string]int
	unpaidAs string // what inquiry reports before the customer pays ("" = IN_BANK)
}

// New starts a fake.
func New() *Server {
	s := &Server{payments: map[string]*payment{}, calls: map[string]int{}}
	s.Server = httptest.NewServer(s.Handler())
	return s
}

// Handler serves the fake (also usable without httptest).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/pg/v4/payment/request.json", s.request)
	mux.HandleFunc("/pg/v4/payment/verify.json", s.verify)
	mux.HandleFunc("/pg/v4/payment/inquiry.json", s.inquiry)
	mux.HandleFunc("/pg/StartPay/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "<html><body>fake Zarinpal payment page %s</body></html>", html.EscapeString(strings.TrimPrefix(r.URL.Path, "/pg/StartPay/")))
	})
	mux.HandleFunc("/_pay/", s.pay)
	return mux
}

// Calls returns how often an API method was called ("request", "verify", "inquiry").
func (s *Server) Calls(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

// Pay marks a payment paid (ok) or failed and returns the callback URL.
func (s *Server) Pay(authority string, ok bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payments[authority]
	if p == nil {
		return "", fmt.Errorf("zpfake: unknown authority %q", authority)
	}
	status := "NOK"
	if ok {
		status = "OK"
		if p.status == "IN_BANK" {
			s.seq++
			p.status, p.refID = "PAID", 500000000+s.seq
		}
	} else if p.status == "IN_BANK" {
		p.status = "FAILED"
	}
	u, err := url.Parse(p.callback)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("Authority", authority)
	q.Set("Status", status)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ReportUnpaidAs makes inquiry answer status (e.g. "FAILED") for payments the
// customer has not paid or cancelled yet. The docs do not say what that is; the
// sandbox answered IN_BANK (the default here) for an unopened session.
func (s *Server) ReportUnpaidAs(status string) { s.mu.Lock(); defer s.mu.Unlock(); s.unpaidAs = status }

// Currency returns the currency field the merchant sent ("" when absent).
func (s *Server) Currency(authority string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.payments[authority]; p != nil {
		return p.currency
	}
	return ""
}

// Description returns what the merchant sent as the description.
func (s *Server) Description(authority string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.payments[authority]; p != nil {
		return p.desc
	}
	return ""
}

func ok(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "errors": []any{}})
}

func fail(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{},
		"errors": map[string]any{"code": code, "message": msg, "validations": []any{}}})
}

// invalidAuthority is the field-keyed validation error from the docs.
func invalidAuthority(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": "Invalid authority",
		"errors": map[string]any{"authority": []any{"Invalid authority.", "-54"}}})
}

func (s *Server) body(w http.ResponseWriter, r *http.Request, name string, into any) bool {
	s.mu.Lock()
	s.calls[name]++
	s.mu.Unlock()
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(into) != nil {
		fail(w, http.StatusUnprocessableEntity, -9, "The input params invalid, validation error.")
		return false
	}
	return true
}

func (s *Server) request(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MerchantID  string            `json:"merchant_id"`
		Amount      int64             `json:"amount"`
		Currency    string            `json:"currency"`
		Description string            `json:"description"`
		CallbackURL string            `json:"callback_url"`
		Metadata    map[string]string `json:"metadata"`
	}
	if !s.body(w, r, "request", &in) {
		return
	}
	rial := in.Amount
	if in.Currency == "IRT" {
		rial *= 10
	}
	switch {
	case in.Currency != "" && in.Currency != "IRR" && in.Currency != "IRT":
		fail(w, http.StatusUnprocessableEntity, -9, "The input params invalid, validation error.")
	case !merchantRe.MatchString(in.MerchantID):
		fail(w, http.StatusUnprocessableEntity, -10, "Invalid merchant_id.")
	case rial < 1000 || rial > 1_000_000_000 || in.CallbackURL == "" || in.Description == "" || len([]rune(in.Description)) > 500:
		fail(w, http.StatusUnprocessableEntity, -9, "The input params invalid, validation error.")
	default:
		s.mu.Lock()
		s.seq++
		auth := fmt.Sprintf("S%035d", s.seq)
		s.payments[auth] = &payment{amount: rial, currency: in.Currency, callback: in.CallbackURL, status: "IN_BANK",
			desc: in.Description, orderID: in.Metadata["order_id"]}
		s.mu.Unlock()
		ok(w, map[string]any{"code": 100, "message": "Success", "authority": auth, "fee_type": "Merchant", "fee": 100})
	}
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MerchantID string `json:"merchant_id"`
		Amount     int64  `json:"amount"`
		Authority  string `json:"authority"`
	}
	if !s.body(w, r, "verify", &in) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payments[in.Authority]
	switch {
	case !merchantRe.MatchString(in.MerchantID):
		fail(w, http.StatusUnprocessableEntity, -10, "Invalid merchant_id.")
	case p == nil:
		invalidAuthority(w)
	case p.status == "IN_BANK" || p.status == "FAILED":
		fail(w, http.StatusUnauthorized, -51, "Session is not valid, session is not active paid try.")
	case in.Amount != p.amount:
		fail(w, http.StatusUnauthorized, -50, "Amounts are not equal.")
	case p.status == "PAID":
		p.status = "VERIFIED"
		ok(w, map[string]any{"code": 100, "message": "Verified", "ref_id": p.refID, "card_pan": "502229******5995",
			"card_hash": "0866A6EA", "fee_type": "Merchant", "fee": 0, "order_id": p.orderID})
	default: // VERIFIED
		ok(w, map[string]any{"code": 101, "message": "Verified", "ref_id": p.refID, "card_pan": "502229******5995"})
	}
}

func (s *Server) inquiry(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Authority string `json:"authority"`
	}
	if !s.body(w, r, "inquiry", &in) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payments[in.Authority]
	if p == nil {
		invalidAuthority(w)
		return
	}
	status := p.status
	if status == "IN_BANK" && s.unpaidAs != "" {
		status = s.unpaidAs
	}
	ok(w, map[string]any{"code": 100, "message": "Success", "status": status})
}

func (s *Server) pay(w http.ResponseWriter, r *http.Request) {
	cb, err := s.Pay(strings.TrimPrefix(r.URL.Path, "/_pay/"), r.URL.Query().Get("status") == "OK")
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte(cb))
}
