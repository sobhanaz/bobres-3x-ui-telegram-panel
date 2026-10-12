// Package tronfake is a fake TronGrid API for tests and local runs. It serves
// what BOBRES reads, as documented: an address's TRC20 transfers
// (GET /v1/accounts/{address}/transactions/trc20) with TronGrid's filters
// (only_to, only_confirmed, contract_address, min_timestamp in milliseconds,
// inclusive), order_by=block_timestamp,asc, limit (at most 200) and paging by
// an opaque meta.fingerprint sent back as the fingerprint parameter; the
// optional TRON-PRO-API-KEY header; and failures on demand.
//
// Transfers stand in for the chain: Pay sends USDT to an address now, Add
// records any transfer (another token, an unconfirmed one, an Approval).
package tronfake

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// USDT is Tether's USDT contract on TRON mainnet. It is written out here, not
// taken from the client, so a typo in either one fails the tests.
const USDT = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"

// Sender is the address Pay sends from (a valid mainnet address).
const Sender = "TAuD2W5vggcdy1cNpem4uH3HreRMyPQLHT"

// Transfer is one TRC20 transfer on the fake chain.
type Transfer struct {
	TxID    string // "" = generated (64 lower-case hex characters)
	From    string // "" = Sender
	To      string
	Token   string // the token contract; "" = USDT
	Symbol  string // "" = USDT (a fake token says USDT too)
	Type    string // "" = Transfer
	Value   string // the amount in the token's smallest unit
	BlockTS time.Time
	// Unconfirmed transfers are left out by only_confirmed=true.
	Unconfirmed bool
}

// Server is a running fake.
type Server struct {
	*httptest.Server
	mu         sync.Mutex
	transfers  []Transfer
	seq        int
	calls      int
	key        string // the only API key accepted ("" = any, or none)
	lastKey    string
	failSkip   int // requests let through before the failures start
	failNext   int
	failStatus int
	leak       bool // ignore contract_address, like a broken or hostile indexer
	pageSize   int  // at most this many per page, under the request's limit (0 = the limit)
	duplicate  bool // repeat every item on the next page too, like a buggy indexer
}

// New starts a fake.
func New() *Server {
	s := &Server{}
	s.Server = httptest.NewServer(s.Handler())
	return s
}

// Handler serves the fake (also usable without httptest).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/accounts/{address}/transactions/trc20", s.list)
	return mux
}

// Add records a transfer and returns its transaction id.
func (s *Server) Add(t Transfer) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	if t.TxID == "" {
		sum := sha256.Sum256([]byte("tronfake-" + strconv.Itoa(s.seq)))
		t.TxID = hex.EncodeToString(sum[:])
	}
	if t.From == "" {
		t.From = Sender
	}
	if t.Token == "" {
		t.Token = USDT
	}
	if t.Symbol == "" {
		t.Symbol = "USDT"
	}
	if t.Type == "" {
		t.Type = "Transfer"
	}
	if t.BlockTS.IsZero() {
		t.BlockTS = time.Now()
	}
	s.transfers = append(s.transfers, t)
	return t.TxID
}

// Pay sends value micro-USDT to an address now (confirmed) and returns the
// transaction id.
func (s *Server) Pay(to string, value int64) string {
	return s.Add(Transfer{To: to, Value: strconv.FormatInt(value, 10)})
}

// PayAt is Pay with a block time.
func (s *Server) PayAt(to string, value int64, at time.Time) string {
	return s.Add(Transfer{To: to, Value: strconv.FormatInt(value, 10), BlockTS: at})
}

// Confirm marks a transaction's transfers confirmed.
func (s *Server) Confirm(txid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.transfers {
		if s.transfers[i].TxID == txid {
			s.transfers[i].Unconfirmed = false
		}
	}
}

// Calls returns how many list requests were served (failed ones included).
func (s *Server) Calls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }

// RequireKey accepts only this API key (others get HTTP 401).
func (s *Server) RequireKey(k string) { s.mu.Lock(); defer s.mu.Unlock(); s.key = k }

// LastKey is the API key the last request carried ("" = none).
func (s *Server) LastKey() string { s.mu.Lock(); defer s.mu.Unlock(); return s.lastKey }

// Fail makes the next n requests answer HTTP status (e.g. 429, 503).
func (s *Server) Fail(n, status int) { s.FailAfter(0, n, status) }

// FailAfter lets the next skip requests through, then makes n answer HTTP
// status: a read that fails partway through its pages.
func (s *Server) FailAfter(skip, n, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failSkip, s.failNext, s.failStatus = skip, n, status
}

// LeakOtherTokens makes the fake ignore contract_address, so transfers of
// other tokens (a fake "USDT") come back too.
func (s *Server) LeakOtherTokens(on bool) { s.mu.Lock(); defer s.mu.Unlock(); s.leak = on }

// PageSize caps every page at n transfers (paging tests).
func (s *Server) PageSize(n int) { s.mu.Lock(); defer s.mu.Unlock(); s.pageSize = n }

// DuplicateAcrossPages repeats the last item of a page at the start of the
// next one.
func (s *Server) DuplicateAcrossPages(on bool) { s.mu.Lock(); defer s.mu.Unlock(); s.duplicate = on }

type tokenInfo struct {
	Symbol   string `json:"symbol"`
	Address  string `json:"address"`
	Decimals int    `json:"decimals"`
	Name     string `json:"name"`
}

type item struct {
	TransactionID  string    `json:"transaction_id"`
	TokenInfo      tokenInfo `json:"token_info"`
	BlockTimestamp int64     `json:"block_timestamp"`
	From           string    `json:"from"`
	To             string    `json:"to"`
	Type           string    `json:"type"`
	Value          string    `json:"value"`
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastKey = r.Header.Get("TRON-PRO-API-KEY")
	if s.failNext > 0 && s.failSkip > 0 {
		s.failSkip--
	} else if s.failNext > 0 {
		s.failNext--
		w.WriteHeader(s.failStatus)
		_, _ = fmt.Fprint(w, `{"success":false,"error":"fake failure"}`)
		return
	}
	if s.key != "" && s.lastKey != s.key {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"success":false,"error":"ApiKey not exists"}`)
		return
	}
	q := r.URL.Query()
	addr := r.PathValue("address")
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"success":false,"error":"limit must be at most 200"}`)
		return
	}
	if q.Get("order_by") != "block_timestamp,asc" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"success":false,"error":"the fake serves order_by=block_timestamp,asc only"}`)
		return
	}
	var minTS int64
	if v := q.Get("min_timestamp"); v != "" {
		if minTS, err = strconv.ParseInt(v, 10, 64); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	contract := q.Get("contract_address")
	onlyTo, onlyConfirmed := q.Get("only_to") == "true", q.Get("only_confirmed") == "true"
	var match []Transfer
	for _, t := range s.transfers {
		switch {
		case t.To != addr && (onlyTo || t.From != addr):
		case onlyConfirmed && t.Unconfirmed:
		case contract != "" && t.Token != contract && !s.leak:
		case t.BlockTS.UnixMilli() < minTS:
		default:
			match = append(match, t)
		}
	}
	sort.SliceStable(match, func(i, j int) bool { return match[i].BlockTS.UnixMilli() < match[j].BlockTS.UnixMilli() })
	offset := 0
	if fp := q.Get("fingerprint"); fp != "" {
		b, err := base64.RawURLEncoding.DecodeString(fp)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if offset, err = strconv.Atoi(strings.TrimPrefix(string(b), "offset:")); err != nil || offset < 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	size := limit
	if s.pageSize > 0 && s.pageSize < size {
		size = s.pageSize
	}
	start := min(offset, len(match))
	end := min(start+size, len(match))
	page := match[start:end]
	if s.duplicate && start > 0 {
		page = append([]Transfer{match[start-1]}, page...)
	}
	out := struct {
		Data    []item         `json:"data"`
		Success bool           `json:"success"`
		Meta    map[string]any `json:"meta"`
	}{Data: []item{}, Success: true, Meta: map[string]any{"at": time.Now().UnixMilli(), "page_size": len(page)}}
	for _, t := range page {
		out.Data = append(out.Data, item{
			TransactionID:  t.TxID,
			TokenInfo:      tokenInfo{Symbol: t.Symbol, Address: t.Token, Decimals: 6, Name: "Tether USD"},
			BlockTimestamp: t.BlockTS.UnixMilli(), From: t.From, To: t.To, Type: t.Type, Value: t.Value,
		})
	}
	if end < len(match) {
		fp := base64.RawURLEncoding.EncodeToString([]byte("offset:" + strconv.Itoa(end)))
		out.Meta["fingerprint"] = fp
		// TronGrid sends a full next URL too; clients must not follow it.
		out.Meta["links"] = map[string]string{"next": "https://evil.example.test/steal?fingerprint=" + fp}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
