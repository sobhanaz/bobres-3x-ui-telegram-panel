// Package idpayfake is a fake IDPay REST v1.1 API for tests and local runs. It
// mimics the behaviour BOBRES relies on, as documented: X-API-KEY, X-SANDBOX,
// Rial amounts, create answering 201 {id, link}, errors as HTTP 4xx with
// {error_code, error_message}, verify answering status 100 once and 101 after,
// inquiry statuses, and the transactions list filtered by status.
//
// Pay stands in for the customer at the bank: it marks the payment and returns
// the callback URL the customer's browser would be sent to.
package idpayfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"
)

// Payment statuses, as IDPay's table.
const (
	NotPaid       = 1
	Failed        = 2
	Cancelled     = 7
	AtBank        = 8
	AwaitVerify   = 10
	Verified      = 100
	AlreadyVerify = 101
	Reversed      = 5
)

type payment struct {
	id, orderID, callback, desc string
	amount                      int64 // Rial, as registered
	paidAmount                  int64
	status                      int
	track                       int
	sandbox                     bool
}

// Server is a running fake.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	payments map[string]*payment
	seq      int
	calls    map[string]int
	apiKey   string        // the only key accepted ("" = any)
	down     int           // answer 500 this many calls
	delay    time.Duration // hold each call this long
	headers  []http.Header // headers of every call, newest last
	unpaidAs int           // inquiry status of an unpaid payment
}

// New starts a fake.
func New() *Server {
	s := &Server{payments: map[string]*payment{}, calls: map[string]int{}, unpaidAs: AtBank}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.1/payment", s.guard("create", s.create))
	mux.HandleFunc("/v1.1/payment/verify", s.guard("verify", s.verify))
	mux.HandleFunc("/v1.1/payment/inquiry", s.guard("inquiry", s.inquiry))
	mux.HandleFunc("/v1.1/payment/transactions", s.guard("transactions", s.transactions))
	mux.HandleFunc("/p/", func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, "fake IDPay payment page") })
	s.Server = httptest.NewServer(mux)
	return s
}

// OnlyKey makes the fake accept only this API key.
func (s *Server) OnlyKey(k string) { s.mu.Lock(); defer s.mu.Unlock(); s.apiKey = k }

// Down makes the next n calls answer 500.
func (s *Server) Down(n int) { s.mu.Lock(); defer s.mu.Unlock(); s.down = n }

// Delay holds every call for d before it answers.
func (s *Server) Delay(d time.Duration) { s.mu.Lock(); defer s.mu.Unlock(); s.delay = d }

// Calls returns how often an API method was called.
func (s *Server) Calls(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

// LastHeader returns a header of the latest call.
func (s *Server) LastHeader(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.headers) == 0 {
		return ""
	}
	return s.headers[len(s.headers)-1].Get(name)
}

// ReportUnpaidAs sets the inquiry status of a payment nobody has paid.
func (s *Server) ReportUnpaidAs(status int) { s.mu.Lock(); defer s.mu.Unlock(); s.unpaidAs = status }

// Pay marks a payment paid (ok) or failed and returns the callback URL.
func (s *Server) Pay(id string, ok bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.payments[id]
	if p == nil {
		return "", fmt.Errorf("idpayfake: unknown payment %q", id)
	}
	if ok && p.status != Verified {
		p.status, p.paidAmount = AwaitVerify, p.amount
	} else if !ok {
		p.status = Failed
	}
	u, err := url.Parse(p.callback)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("status", fmt.Sprint(p.status))
	q.Set("track_id", fmt.Sprint(p.track))
	q.Set("id", id)
	q.Set("order_id", p.orderID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// SetStatus forces a payment's status (and its paid amount, 0 = registered).
func (s *Server) SetStatus(id string, status int, paid int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.payments[id]; p != nil {
		p.status = status
		if paid == 0 {
			paid = p.amount
		}
		p.paidAmount = paid
	}
}

// Description and Amount return what a payment was created with.
func (s *Server) Description(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.payments[id]; p != nil {
		return p.desc
	}
	return ""
}

// Amount returns the registered amount of a payment.
func (s *Server) Amount(id string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.payments[id]; p != nil {
		return p.amount
	}
	return 0
}

// AddPayment registers a payment as if it had been created, and returns its id.
func (s *Server) AddPayment(orderID string, rial int64, status int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.add(orderID, rial, "https://shop.example.test/webhooks/idpay", "added", false)
	p.status = status
	if status == AwaitVerify || status == Verified {
		p.paidAmount = rial
	}
	return p.id
}

func (s *Server) add(orderID string, rial int64, cb, desc string, sandbox bool) *payment {
	s.seq++
	p := &payment{id: fmt.Sprintf("%032x", 0x1b2c3d4e5f600000+int64(s.seq)), orderID: orderID, amount: rial,
		callback: cb, desc: desc, status: NotPaid, track: 10000 + s.seq, sandbox: sandbox}
	s.payments[p.id] = p
	return p
}

func fail(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error_code": code, "error_message": msg})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// guard counts the call and applies the key check, the outage and the delay.
func (s *Server) guard(name string, h func(http.ResponseWriter, *http.Request, bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls[name]++
		s.headers = append(s.headers, r.Header.Clone())
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
			fail(w, 500, 15, "service unavailable")
			return
		}
		got := r.Header.Get("X-API-KEY")
		switch {
		case r.Method != http.MethodPost:
			fail(w, 405, 51, "method not allowed")
		case got == "":
			fail(w, 403, 12, "API key not found")
		case key != "" && got != key:
			fail(w, 403, 12, "API key not found")
		default:
			h(w, r, r.Header.Get("X-SANDBOX") == "1")
		}
	}
}

func (s *Server) create(w http.ResponseWriter, r *http.Request, sandbox bool) {
	var b struct {
		OrderID  string `json:"order_id"`
		Amount   int64  `json:"amount"`
		Desc     string `json:"desc"`
		Callback string `json:"callback"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || b.OrderID == "" {
		fail(w, 406, 32, "order_id must not be empty")
		return
	}
	if b.Amount < 1000 {
		fail(w, 406, 34, "amount below the minimum")
		return
	}
	if b.Amount > 500_000_000 {
		fail(w, 406, 35, "amount above the maximum")
		return
	}
	if u, err := url.Parse(b.Callback); err != nil || u.Host == "" {
		fail(w, 406, 39, "invalid callback")
		return
	}
	s.mu.Lock()
	p := s.add(b.OrderID, b.Amount, b.Callback, b.Desc, sandbox)
	s.mu.Unlock()
	base := "http://" + r.Host + "/p/"
	if sandbox {
		base += "ws-sandbox/"
	} else {
		base += "ws/"
	}
	reply(w, 201, map[string]string{"id": p.id, "link": base + p.id})
}

func (s *Server) find(w http.ResponseWriter, r *http.Request) *payment {
	var b struct {
		ID      string `json:"id"`
		OrderID string `json:"order_id"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || b.ID == "" || b.OrderID == "" {
		fail(w, 406, 31, "id and order_id are required")
		return nil
	}
	p := s.payments[b.ID]
	if p == nil || p.orderID != b.OrderID {
		fail(w, 405, 51, "transaction not found")
		return nil
	}
	return p
}

func (p *payment) body() map[string]any {
	return map[string]any{
		"status": fmt.Sprint(p.status), "track_id": fmt.Sprint(p.track), "id": p.id, "order_id": p.orderID,
		"amount": fmt.Sprint(p.amount),
		"payment": map[string]any{"track_id": fmt.Sprint(p.track + 1000), "amount": fmt.Sprint(p.paidAmount),
			"card_no": "123456******1234"},
	}
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request, _ bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.find(w, r)
	if p == nil {
		return
	}
	switch p.status {
	case AwaitVerify:
		p.status = Verified
		reply(w, 200, p.body())
	case Verified, AlreadyVerify:
		p.status = AlreadyVerify
		reply(w, 200, p.body())
	case Reversed:
		fail(w, 405, 54, "the verify window passed")
	default:
		fail(w, 405, 53, "verify is not possible")
	}
}

func (s *Server) inquiry(w http.ResponseWriter, r *http.Request, _ bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.find(w, r)
	if p == nil {
		return
	}
	b := p.body()
	if p.status == NotPaid {
		b["status"] = fmt.Sprint(s.unpaidAs)
	}
	reply(w, 200, b)
}

func (s *Server) transactions(w http.ResponseWriter, r *http.Request, _ bool) {
	var b struct {
		Status []int `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	s.mu.Lock()
	defer s.mu.Unlock()
	var recs []map[string]any
	for _, p := range s.payments {
		for _, st := range b.Status {
			if p.status == st {
				recs = append(recs, p.body())
			}
		}
	}
	if len(recs) == 0 {
		w.WriteHeader(204)
		return
	}
	reply(w, 200, map[string]any{"attachment": map[string]any{"total_count": len(recs)}, "records": recs})
}
