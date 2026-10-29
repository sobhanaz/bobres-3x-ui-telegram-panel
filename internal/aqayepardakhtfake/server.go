// Package aqayepardakhtfake is a fake Aqayepardakht web service (version 2) for tests and local runs. It
// mimics the behaviour BOBRES relies on, as documented: JSON posts carrying a pin, the create call answering
// 200 {status: "success", transid} and an error 422 {status: "error", code}, verify answering code "1" for a
// paid payment (then error code "2" for a repeat), error code "0" for one not paid, the numbered errors of
// the documentation, amounts in Toman between 1,000 and 400,000,000, and the pin "sandbox" for the test system.
//
// Pay stands in for the customer at the bank: it marks the payment and returns the callback URL the
// customer's browser would be sent to.
package aqayepardakhtfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The state of a payment at the bank.
const (
	// Open: nobody has paid yet.
	Open = iota
	// Done: paid; the first verify answers success, later ones "verified before".
	Done
	// Cancelled: the customer gave up at the bank.
	Cancelled
)

type payment struct {
	id, invoiceID, callback, method, pin string
	toman                                int64
	state                                int
	verified                             bool
	tracking                             string
}

// Server is a running fake.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	payments map[string]*payment
	seq      int
	calls    map[string]int
	onlyPIN  string
	down     int
	delay    time.Duration
	// Domain, when set, is the only host a callback may be on (error 15).
	domain string
}

// New starts a fake.
func New() *Server {
	s := &Server{payments: map[string]*payment{}, calls: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/create", s.guard("create", s.create))
	mux.HandleFunc("/api/v2/verify", s.guard("verify", s.verify))
	mux.HandleFunc("/startpay/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls["startpay"]++
		s.mu.Unlock()
		_, _ = fmt.Fprint(w, "fake Aqayepardakht payment page")
	})
	s.Server = httptest.NewServer(mux)
	return s
}

// OnlyPIN makes the fake accept only this pin (and "sandbox").
func (s *Server) OnlyPIN(pin string) { s.mu.Lock(); defer s.mu.Unlock(); s.onlyPIN = pin }

// OnlyDomain makes the fake refuse a callback that is not on this host (error 15).
func (s *Server) OnlyDomain(host string) { s.mu.Lock(); defer s.mu.Unlock(); s.domain = host }

// Down makes the next n calls answer 502.
func (s *Server) Down(n int) { s.mu.Lock(); defer s.mu.Unlock(); s.down = n }

// Delay holds every call for d before it answers.
func (s *Server) Delay(d time.Duration) { s.mu.Lock(); defer s.mu.Unlock(); s.delay = d }

// Calls returns how often an API method was called ("create", "verify", "startpay").
func (s *Server) Calls(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

// Pay marks a payment paid or cancelled and returns the callback URL: the customer's browser is sent
// there with a GET or a POST, as the payment asked for; for a POST the values are in the returned form.
func (s *Server) Pay(id string, paid bool) (callback string, form url.Values, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payments[id]
	if p == nil {
		return "", nil, fmt.Errorf("aqayepardakhtfake: unknown transid %q", id)
	}
	if p.state == Open {
		p.state = Cancelled
		if paid {
			p.state = Done
		}
	}
	form = url.Values{"transid": {id}, "invoice_id": {p.invoiceID}, "bank": {"Melli"}, "status": {"0"}}
	if p.state == Done {
		form.Set("status", "1")
		form.Set("cardnumber", "6037-99**-****-1234")
		form.Set("tracking_number", p.tracking)
	}
	u, err := url.Parse(p.callback)
	if err != nil {
		return "", nil, err
	}
	if strings.EqualFold(p.method, "POST") {
		return u.String(), form, nil
	}
	q := u.Query()
	for k, v := range form {
		q[k] = v
	}
	u.RawQuery = q.Encode()
	return u.String(), form, nil
}

// SetState forces a payment's state at the bank (Open, Done, Cancelled) without a callback.
func (s *Server) SetState(id string, state int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.payments[id]; p != nil {
		p.state = state
	}
}

// SetToman changes the amount the bank holds for a payment (a payment created with another amount than the
// one verify is asked about).
func (s *Server) SetToman(id string, toman int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.payments[id]; p != nil {
		p.toman = toman
	}
}

// Verified reports whether a payment was verified (a verify answered success for it).
func (s *Server) Verified(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payments[id]
	return p != nil && p.verified
}

// AddPayment registers a payment as if create had been called, and returns its transid.
func (s *Server) AddPayment(invoiceID string, toman int64, state int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.add(invoiceID, toman, "https://shop.example.test/webhooks/aqayepardakht", "GET", "sandbox")
	p.state = state
	return p.id
}

func (s *Server) add(invoiceID string, toman int64, cb, method, pin string) *payment {
	s.seq++
	p := &payment{id: fmt.Sprintf("%d", 4_800_000+s.seq), invoiceID: invoiceID, toman: toman, callback: cb, method: method, pin: pin,
		tracking: fmt.Sprintf("1411965846%02d", s.seq)}
	s.payments[p.id] = p
	return p
}

// fail answers an error as the documentation shows it: 422 {status: "error", code}.
func fail(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "error", "code": strconv.Itoa(code)})
}

func succeed(w http.ResponseWriter, v map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	v["status"] = "success"
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) guard(name string, h func(http.ResponseWriter, map[string]any)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls[name]++
		down, delay := s.down, s.delay
		if down > 0 {
			s.down--
		}
		s.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if down > 0 {
			http.Error(w, "gateway down", http.StatusBadGateway)
			return
		}
		var body map[string]any
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil {
			fail(w, -2)
			return
		}
		h(w, body)
	}
}

// str reads a posted value as text: the documentation's samples post numbers and strings alike.
func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

// checkPIN answers the PIN errors, false when it did.
func (s *Server) checkPIN(w http.ResponseWriter, body map[string]any) (string, bool) {
	pin := str(body["pin"])
	s.mu.Lock()
	only := s.onlyPIN
	s.mu.Unlock()
	switch {
	case pin == "":
		fail(w, -2)
		return "", false
	case pin != "sandbox" && only != "" && pin != only:
		fail(w, -6)
		return "", false
	}
	return pin, true
}

// amount reads the Toman amount and answers the amount errors (-1, -4, -5), false when it did.
func amount(w http.ResponseWriter, body map[string]any) (int64, bool) {
	raw := str(body["amount"])
	if raw == "" {
		fail(w, -1)
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		fail(w, -4)
		return 0, false
	}
	if n < 1_000 || n > 400_000_000 {
		fail(w, -5)
		return 0, false
	}
	return n, true
}

func (s *Server) create(w http.ResponseWriter, body map[string]any) {
	pin, good := s.checkPIN(w, body)
	if !good {
		return
	}
	toman, good := amount(w, body)
	if !good {
		return
	}
	cb := str(body["callback"])
	if cb == "" {
		fail(w, -3)
		return
	}
	method := strings.ToUpper(str(body["callback_method"]))
	if method == "" {
		method = "POST" // the documented default
	}
	if method != "POST" && method != "GET" {
		fail(w, -17)
		return
	}
	u, err := url.Parse(cb)
	s.mu.Lock()
	domain := s.domain
	s.mu.Unlock()
	if err != nil || u.Host == "" || (domain != "" && u.Hostname() != domain) {
		fail(w, -15)
		return
	}
	s.mu.Lock()
	p := s.add(str(body["invoice_id"]), toman, cb, method, pin)
	s.mu.Unlock()
	succeed(w, map[string]any{"transid": p.id})
}

func (s *Server) verify(w http.ResponseWriter, body map[string]any) {
	pin, good := s.checkPIN(w, body)
	if !good {
		return
	}
	toman, good := amount(w, body)
	if !good {
		return
	}
	id := str(body["transid"])
	if id == "" {
		fail(w, -7)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payments[id]
	switch {
	case p == nil:
		fail(w, -8)
	case p.pin != pin:
		fail(w, -9)
	case p.toman != toman:
		fail(w, -10)
	case p.state != Done:
		fail(w, 0) // not completed
	case p.verified:
		fail(w, 2) // verified before
	default:
		p.verified = true
		succeed(w, map[string]any{"code": "1"})
	}
}
