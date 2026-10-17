// Package nextpayfake is a fake NextPay direct-gateway API for tests and local
// runs. It mimics the behaviour BOBRES relies on, as documented: form posts with
// api_key, the token call answering {code: -1, trans_id}, amounts in Toman unless
// currency=IRR, verify answering code 0 for a paid payment (then -49 for a
// repeat), -1/-3 for a payment not paid yet, -2 for a rejected one and -4 for a
// cancelled one.
//
// Pay stands in for the customer at the bank: it marks the payment and returns
// the callback URL the customer's browser would be sent to.
package nextpayfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sync"
	"time"
)

var keyRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Verify codes.
const (
	Paid      = 0
	Waiting   = -1
	Rejected  = -2
	AtBank    = -3
	Cancelled = -4
)

type payment struct {
	id, orderID, callback string
	amount                int64 // Rial
	code                  int   // the verify answer while not verified
	verified              bool
	ref                   string
}

// Server is a running fake.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	payments map[string]*payment
	seq      int
	calls    map[string]int
	apiKey   string
	down     int
	delay    time.Duration
}

// New starts a fake.
func New() *Server {
	s := &Server{payments: map[string]*payment{}, calls: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/nx/gateway/token", s.guard("token", s.token))
	mux.HandleFunc("/nx/gateway/verify", s.guard("verify", s.verify))
	mux.HandleFunc("/nx/gateway/payment/", func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, "fake NextPay payment page") })
	s.Server = httptest.NewServer(mux)
	return s
}

// OnlyKey makes the fake accept only this API key.
func (s *Server) OnlyKey(k string) { s.mu.Lock(); defer s.mu.Unlock(); s.apiKey = k }

// Down makes the next n calls answer 500.
func (s *Server) Down(n int) { s.mu.Lock(); defer s.mu.Unlock(); s.down = n }

// Delay holds every call for d before it answers.
func (s *Server) Delay(d time.Duration) { s.mu.Lock(); defer s.mu.Unlock(); s.delay = d }

// Calls returns how often an API method was called ("token", "verify").
func (s *Server) Calls(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

// Pay marks a payment paid (ok) or rejected and returns the callback URL.
func (s *Server) Pay(id string, ok bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payments[id]
	if p == nil {
		return "", fmt.Errorf("nextpayfake: unknown token %q", id)
	}
	if ok {
		if !p.verified {
			p.code = Paid
		}
	} else {
		p.code = Rejected
	}
	u, err := url.Parse(p.callback)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("trans_id", id)
	q.Set("order_id", p.orderID)
	q.Set("amount", fmt.Sprint(p.amount/10))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// SetCode forces the verify answer of a payment that is not verified yet.
func (s *Server) SetCode(id string, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.payments[id]; p != nil {
		p.code = code
	}
}

// AddPayment registers a payment as if a token had been issued, and returns it.
func (s *Server) AddPayment(orderID string, rial int64, code int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.add(orderID, rial, "https://shop.example.test/webhooks/nextpay")
	p.code = code
	return p.id
}

func (s *Server) add(orderID string, rial int64, cb string) *payment {
	s.seq++
	p := &payment{id: fmt.Sprintf("f7c07568-c6d1-4bee-87b1-%012d", s.seq), orderID: orderID, amount: rial, callback: cb,
		code: Waiting, ref: fmt.Sprintf("1411965846%02d", s.seq)}
	s.payments[p.id] = p
	return p
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) guard(name string, h func(http.ResponseWriter, url.Values)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls[name]++
		down, delay, key := s.down, s.delay, s.apiKey
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
		if r.ParseForm() != nil || r.Method != http.MethodPost {
			reply(w, map[string]int{"code": -46})
			return
		}
		got := r.PostForm.Get("api_key")
		switch {
		case got == "":
			reply(w, map[string]int{"code": -20})
		case !keyRe.MatchString(got) || (key != "" && got != key):
			reply(w, map[string]int{"code": -33})
		default:
			h(w, r.PostForm)
		}
	}
}

// rial converts a posted amount to Rial: Toman unless currency is IRR.
func rial(f url.Values) int64 {
	var n int64
	_, _ = fmt.Sscan(f.Get("amount"), &n)
	if f.Get("currency") != "IRR" {
		n *= 10
	}
	return n
}

func (s *Server) token(w http.ResponseWriter, f url.Values) {
	amount := rial(f)
	u, err := url.Parse(f.Get("callback_uri"))
	switch {
	case f.Get("order_id") == "":
		reply(w, map[string]int{"code": -36})
	case amount < 10_000:
		reply(w, map[string]int{"code": -30})
	case len(f.Get("callback_uri")) > 100:
		reply(w, map[string]int{"code": -73})
	case err != nil || u.Host == "":
		reply(w, map[string]int{"code": -32})
	default:
		s.mu.Lock()
		p := s.add(f.Get("order_id"), amount, f.Get("callback_uri"))
		s.mu.Unlock()
		reply(w, map[string]any{"code": -1, "trans_id": p.id})
	}
}

func (s *Server) verify(w http.ResponseWriter, f url.Values) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payments[f.Get("trans_id")]
	if p == nil {
		reply(w, map[string]int{"code": -37})
		return
	}
	if f.Get("refund_request") != "" {
		reply(w, map[string]int{"code": -90}) // a refund: never a payment
		return
	}
	if rial(f) != p.amount {
		reply(w, map[string]int{"code": -24})
		return
	}
	if p.verified {
		reply(w, map[string]int{"code": -49})
		return
	}
	if p.code != Paid {
		reply(w, map[string]int{"code": p.code})
		return
	}
	p.verified = true
	amount := p.amount
	if f.Get("currency") != "IRR" {
		amount /= 10
	}
	reply(w, map[string]any{"code": 0, "amount": amount, "order_id": p.orderID, "Shaparak_Ref_Id": p.ref,
		"card_holder": "5022-29**-****-5020"})
}
