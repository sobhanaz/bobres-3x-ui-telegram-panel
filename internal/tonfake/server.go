// Package tonfake is a fake TON Center (getTransactions of the HTTP API, version 2) and a fake of the two price
// pages (Binance and OKX) for tests and local runs. It mimics the behaviour BOBRES relies on, as documented:
// transactions newest first, a page continued with lt and hash (the named transaction is returned first), a
// transaction's hash in base64, values in nanoton as text, a text comment in message and in msg_data, an
// external message with an empty source, the X-API-Key header, and the rate limit answer.
//
// Pay stands in for a customer's wallet: it adds a transfer to an account.
package tonfake

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonaddr"
)

// Tx is a transaction to add.
type Tx struct {
	To      string // the receiving account, in any form
	From    string // the sender; "" is an external message (not a transfer)
	Nano    int64
	Comment string
	// Age is how long ago it happened; zero is 90 seconds ago (final: a minute is enough).
	Age time.Duration
	// Bounce makes the account send the value back to the sender in the same transaction (a bounce).
	Bounce bool
	// Binary makes the message body not a text comment.
	Binary bool
}

type tx struct {
	Tx
	lt   int64
	hash [32]byte
	at   time.Time
}

// Server is a running fake.
type Server struct {
	*httptest.Server
	mu         sync.Mutex
	txs        []*tx
	lt         int64
	seq        int
	calls      map[string]int
	apiKey     string
	down       int
	limited    int
	delay      time.Duration
	price      map[string]string
	priceDown  map[string]bool
	priceStale bool
}

// New starts a fake.
func New() *Server {
	s := &Server{calls: map[string]int{}, lt: 40_000_000_000_000, price: map[string]string{"binance": "3.2145", "okx": "3.2150"},
		priceDown: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/getTransactions", s.getTransactions)
	mux.HandleFunc("/binance/api/v3/ticker/price", s.binance)
	mux.HandleFunc("/okx/api/v5/market/ticker", s.okx)
	s.Server = httptest.NewServer(mux)
	return s
}

// BinanceURL and OKXURL are the bases of the two price pages.
func (s *Server) BinanceURL() string { return s.URL + "/binance" }

// OKXURL is the base of the OKX page.
func (s *Server) OKXURL() string { return s.URL + "/okx" }

// OnlyAPIKey makes the fake answer 401 to any other key (a missing one included).
func (s *Server) OnlyAPIKey(k string) { s.mu.Lock(); defer s.mu.Unlock(); s.apiKey = k }

// Down makes the next n calls of getTransactions answer 502.
func (s *Server) Down(n int) { s.mu.Lock(); defer s.mu.Unlock(); s.down = n }

// RateLimit makes the next n calls of getTransactions answer 429.
func (s *Server) RateLimit(n int) { s.mu.Lock(); defer s.mu.Unlock(); s.limited = n }

// Delay holds every getTransactions for d before it answers.
func (s *Server) Delay(d time.Duration) { s.mu.Lock(); defer s.mu.Unlock(); s.delay = d }

// Calls returns how often a method was called ("getTransactions", "binance", "okx").
func (s *Server) Calls(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

// SetPrice sets what an exchange ("binance" or "okx") answers.
func (s *Server) SetPrice(source, price string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.price[source] = price
}

// FailPrice makes an exchange answer 503 (or answer again).
func (s *Server) FailPrice(source string, fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.priceDown[source] = fail
}

// StaleOKX makes OKX's ticker an hour old.
func (s *Server) StaleOKX(stale bool) { s.mu.Lock(); defer s.mu.Unlock(); s.priceStale = stale }

// Add adds a transaction and returns its hash in lower-case hex.
func (s *Server) Add(t Tx) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.lt += 1_000_003
	age := t.Age
	if age == 0 {
		age = 90 * time.Second
	}
	x := &tx{Tx: t, lt: s.lt, hash: sha256.Sum256([]byte(fmt.Sprintf("bobres-tonfake-%d", s.seq))), at: time.Now().Add(-age)}
	s.txs = append(s.txs, x)
	return hex.EncodeToString(x.hash[:])
}

// Pay adds a transfer of nano nanoton with a comment from a customer's wallet, final.
func (s *Server) Pay(to string, nano int64, comment string) string {
	return s.Add(Tx{To: to, From: Customer(), Nano: nano, Comment: comment})
}

// SetAge changes how long ago a transaction (by its hash) happened.
func (s *Server) SetAge(hash string, age time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.txs {
		if hex.EncodeToString(x.hash[:]) == hash {
			x.at = time.Now().Add(-age)
		}
	}
}

// Customer is the address of the wallet Pay sends from.
func Customer() string {
	return tonaddr.Address{Hash: sha256.Sum256([]byte("bobres-tonfake-customer"))}.Canonical()
}

// LastLT is the logical time of the newest transaction.
func (s *Server) LastLT() int64 { s.mu.Lock(); defer s.mu.Unlock(); return s.lt }

func b64(h [32]byte) string { return base64.StdEncoding.EncodeToString(h[:]) }

func (s *Server) encode(x *tx, account string) map[string]any {
	acct, _ := tonaddr.Parse(account)
	dest := acct.Friendly(true)
	in := map[string]any{"@type": "raw.message", "source": "", "destination": dest, "value": "0"}
	if x.From != "" {
		src, _ := tonaddr.Parse(x.From)
		in["source"] = src.Friendly(true)
		in["value"] = strconv.FormatInt(x.Nano, 10)
		if x.Comment != "" && !x.Binary {
			in["message"] = x.Comment
			in["msg_data"] = map[string]any{"@type": "msg.dataText", "text": base64.StdEncoding.EncodeToString([]byte(x.Comment))}
		} else if x.Binary {
			in["msg_data"] = map[string]any{"@type": "msg.dataRaw", "body": "te6cckEBAQEAAgAAAEysuc0="}
		}
	}
	out := []map[string]any{}
	if x.Bounce && x.From != "" {
		src, _ := tonaddr.Parse(x.From)
		out = append(out, map[string]any{"@type": "raw.message", "source": dest, "destination": src.Friendly(true),
			"value": strconv.FormatInt(max(x.Nano-5_000_000, 1), 10)})
	}
	return map[string]any{"@type": "raw.transaction", "utime": x.at.Unix(),
		"transaction_id": map[string]any{"@type": "internal.transactionId", "lt": strconv.FormatInt(x.lt, 10), "hash": b64(x.hash)},
		"in_msg":         in, "out_msgs": out}
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) getTransactions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls["getTransactions"]++
	down, limited, delay, key := s.down, s.limited, s.delay, s.apiKey
	if down > 0 {
		s.down--
	}
	if limited > 0 {
		s.limited--
	}
	s.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	switch {
	case down > 0:
		http.Error(w, "service down", http.StatusBadGateway)
		return
	case limited > 0:
		reply(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "Ratelimit exceed", "code": 429})
		return
	case key != "" && r.Header.Get("X-API-Key") != key:
		reply(w, http.StatusUnauthorized, map[string]any{"ok": false, "error": "Invalid API key", "code": 401})
		return
	}
	q := r.URL.Query()
	acct, err := tonaddr.Parse(q.Get("address"))
	if err != nil {
		reply(w, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": "Incorrect address", "code": 416})
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	var fromLT int64
	if q.Get("lt") != "" {
		fromLT, _ = strconv.ParseInt(q.Get("lt"), 10, 64)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for i := len(s.txs) - 1; i >= 0 && len(out) < limit; i-- {
		x := s.txs[i]
		if to, err := tonaddr.Parse(x.To); err != nil || to.Hash != acct.Hash {
			continue
		}
		if fromLT > 0 && x.lt > fromLT {
			continue
		}
		out = append(out, s.encode(x, q.Get("address")))
	}
	if out == nil {
		out = []map[string]any{}
	}
	reply(w, http.StatusOK, map[string]any{"ok": true, "result": out})
}

func (s *Server) binance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls["binance"]++
	price, down := s.price["binance"], s.priceDown["binance"]
	s.mu.Unlock()
	if down {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	reply(w, http.StatusOK, map[string]string{"symbol": r.URL.Query().Get("symbol"), "price": price})
}

func (s *Server) okx(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls["okx"]++
	price, down, stale := s.price["okx"], s.priceDown["okx"], s.priceStale
	s.mu.Unlock()
	if down {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	at := time.Now()
	if stale {
		at = at.Add(-time.Hour)
	}
	reply(w, http.StatusOK, map[string]any{"code": "0", "msg": "", "data": []map[string]string{
		{"instId": r.URL.Query().Get("instId"), "last": price, "ts": strconv.FormatInt(at.UnixMilli(), 10)}}})
}
