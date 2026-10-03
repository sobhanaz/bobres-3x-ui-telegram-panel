// Package xuifake is an in-memory fake of the 3x-ui panel API for tests.
// It mirrors the real response envelope {success,msg,obj}, Bearer auth, and the
// units used by 3x-ui v3.8.5 (expiryTime in unix milliseconds, totalGB in bytes).
package xuifake

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const dayMs = 24 * 60 * 60 * 1000

type client struct {
	ID         string `json:"id,omitempty"`
	Email      string `json:"email"`
	SubID      string `json:"subId,omitempty"`
	TotalGB    int64  `json:"totalGB"`
	ExpiryTime int64  `json:"expiryTime"`
	LimitIP    int    `json:"limitIp"`
	Enable     bool   `json:"enable"`
	TgID       int64  `json:"tgId,omitempty"`
	Comment    string `json:"comment,omitempty"`
	Flow       string `json:"flow,omitempty"`
}

type record struct {
	client
	up, down   int64
	inboundIDs []int
}

// Server is a running fake panel. The embedded *httptest.Server gives URL and Close.
type Server struct {
	*httptest.Server
	token string

	mu       sync.Mutex
	clients  map[string]*record
	inbounds []map[string]any
	failNext int
	latency  time.Duration
	calls    map[string]int
}

// NewServer starts a fake panel that accepts only the given Bearer token.
func NewServer(token string) *Server {
	s := &Server{
		token:   token,
		clients: map[string]*record{},
		calls:   map[string]int{},
		inbounds: []map[string]any{
			{"id": 1, "remark": "reality", "protocol": "vless", "port": 443, "enable": true},
			{"id": 2, "remark": "ws", "protocol": "vmess", "port": 8443, "enable": true},
			{"id": 3, "remark": "disabled", "protocol": "trojan", "port": 8444, "enable": false},
		},
	}
	mux := http.NewServeMux()
	route := func(pattern, name string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.wrap(name, h))
	}
	route("/panel/api/clients/add", "add", s.add)
	route("/panel/api/clients/get/", "get", s.get)
	route("/panel/api/clients/del/", "del", s.del)
	route("/panel/api/clients/bulkAdjust", "bulkAdjust", s.bulkAdjust)
	route("/panel/api/clients/resetTraffic/", "resetTraffic", s.reset)
	route("/panel/api/clients/traffic/", "traffic", s.traffic)
	route("/panel/api/clients/links/", "links", s.links)
	route("/panel/api/clients/subLinks/", "subLinks", s.subLinks)
	route("/panel/api/inbounds/options", "options", s.options)
	s.Server = httptest.NewServer(mux)
	return s
}

// SetFailNext makes the next n authenticated calls fail with HTTP 500.
func (s *Server) SetFailNext(n int) { s.mu.Lock(); s.failNext = n; s.mu.Unlock() }

// SetLatency delays every call by d (respects request cancellation).
func (s *Server) SetLatency(d time.Duration) { s.mu.Lock(); s.latency = d; s.mu.Unlock() }

// Calls returns how many authenticated calls hit a route name (e.g. "add").
func (s *Server) Calls(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

// AddUsage simulates traffic usage for a client.
func (s *Server) AddUsage(email string, up, down int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.clients[email]; ok {
		r.up += up
		r.down += down
	}
}

// SetInboundsEnabled flips the enable flag of every inbound.
func (s *Server) SetInboundsEnabled(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, in := range s.inbounds {
		in["enable"] = on
	}
}

// Seed stores a client directly, as if created earlier (e.g. by another tool,
// or by an attempt whose response was lost).
func (s *Server) Seed(email, subID string, inboundIDs []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[email] = &record{client: client{Email: email, SubID: subID, Enable: true}, inboundIDs: inboundIDs}
}

// Has reports whether a client exists.
func (s *Server) Has(email string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.clients[email]
	return ok
}

// Snapshot returns a copy of a client's stored spec fields for assertions.
func (s *Server) Snapshot(email string) (spec struct {
	Enable     bool
	ExpiryTime int64
	TotalGB    int64
	Up, Down   int64
}, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, found := s.clients[email]
	if !found {
		return spec, false
	}
	spec.Enable, spec.ExpiryTime, spec.TotalGB, spec.Up, spec.Down = r.Enable, r.ExpiryTime, r.TotalGB, r.up, r.down
	return spec, true
}

func reply(w http.ResponseWriter, status int, ok bool, msg string, obj any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": ok, "msg": msg, "obj": obj})
}

func (s *Server) wrap(name string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		lat := s.latency
		s.mu.Unlock()
		if lat > 0 {
			select {
			case <-time.After(lat):
			case <-r.Context().Done():
				return
			}
		}
		if r.Header.Get("Authorization") != "Bearer "+s.token {
			reply(w, http.StatusUnauthorized, false, "unauthorized", nil)
			return
		}
		s.mu.Lock()
		s.calls[name]++
		fail := s.failNext > 0
		if fail {
			s.failNext--
		}
		s.mu.Unlock()
		if fail {
			reply(w, http.StatusInternalServerError, false, "injected failure", nil)
			return
		}
		h(w, r)
	}
}

func emailFrom(r *http.Request, prefix string) string {
	e, _ := url.PathUnescape(strings.TrimPrefix(r.URL.Path, prefix))
	return e
}

// The real panel reports business errors as HTTP 200 with success=false.
func notFound(w http.ResponseWriter) { reply(w, http.StatusOK, false, "record not found", nil) }

func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Client     client `json:"client"`
		InboundIDs []int  `json:"inboundIds"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&b) != nil || b.Client.Email == "" || len(b.InboundIDs) == 0 {
		reply(w, http.StatusOK, false, "bad request", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.clients[b.Client.Email]; dup {
		reply(w, http.StatusOK, false, "Duplicate email: "+b.Client.Email, nil)
		return
	}
	s.clients[b.Client.Email] = &record{client: b.Client, inboundIDs: b.InboundIDs}
	reply(w, http.StatusOK, true, "ok", nil)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.clients[emailFrom(r, "/panel/api/clients/get/")]
	if !ok {
		notFound(w)
		return
	}
	reply(w, http.StatusOK, true, "", map[string]any{"client": rec.client, "inboundIds": rec.inboundIDs, "usedTraffic": rec.up + rec.down})
}

func (s *Server) del(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := emailFrom(r, "/panel/api/clients/del/")
	if _, ok := s.clients[e]; !ok {
		notFound(w)
		return
	}
	delete(s.clients, e)
	reply(w, http.StatusOK, true, "ok", nil)
}

// bulkAdjust follows 3x-ui semantics: unlimited (0) fields are skipped, and a
// client disabled only because it was depleted is re-enabled once lifted out.
func (s *Server) bulkAdjust(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Emails   []string `json:"emails"`
		AddDays  int      `json:"addDays"`
		AddBytes int64    `json:"addBytes"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || len(b.Emails) == 0 {
		reply(w, http.StatusOK, false, "bad request", nil)
		return
	}
	type skip struct {
		Email  string `json:"email"`
		Reason string `json:"reason"`
	}
	adjusted, skipped := 0, []skip{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range b.Emails {
		rec, ok := s.clients[e]
		if !ok {
			skipped = append(skipped, skip{e, "not found"})
			continue
		}
		changed := false
		if b.AddDays != 0 {
			if rec.ExpiryTime == 0 {
				skipped = append(skipped, skip{e, "unlimited expiry"})
			} else {
				rec.ExpiryTime += int64(b.AddDays) * dayMs
				changed = true
			}
		}
		if b.AddBytes != 0 {
			if rec.TotalGB == 0 {
				skipped = append(skipped, skip{e, "unlimited traffic"})
			} else {
				rec.TotalGB += b.AddBytes
				changed = true
			}
		}
		if changed {
			adjusted++
			depleted := (rec.TotalGB > 0 && rec.up+rec.down >= rec.TotalGB) ||
				(rec.ExpiryTime > 0 && rec.ExpiryTime <= time.Now().UnixMilli())
			if !depleted {
				rec.Enable = true
			}
		}
	}
	reply(w, http.StatusOK, true, "", map[string]any{"adjusted": adjusted, "skipped": skipped})
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.clients[emailFrom(r, "/panel/api/clients/resetTraffic/")]
	if !ok {
		notFound(w)
		return
	}
	rec.up, rec.down, rec.Enable = 0, 0, true
	reply(w, http.StatusOK, true, "ok", nil)
}

func (s *Server) traffic(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.clients[emailFrom(r, "/panel/api/clients/traffic/")]
	if !ok {
		notFound(w)
		return
	}
	reply(w, http.StatusOK, true, "", map[string]any{
		"email": rec.Email, "up": rec.up, "down": rec.down, "total": rec.TotalGB,
		"expiryTime": rec.ExpiryTime, "enable": rec.Enable,
	})
}

func (s *Server) links(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.clients[emailFrom(r, "/panel/api/clients/links/")]
	if !ok {
		notFound(w)
		return
	}
	out := make([]string, 0, len(rec.inboundIDs))
	for _, id := range rec.inboundIDs {
		out = append(out, "vless://"+rec.ID+"@fake.example:443?inbound="+strconv.Itoa(id)+"#"+rec.Email)
	}
	reply(w, http.StatusOK, true, "", out)
}

func (s *Server) subLinks(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := emailFrom(r, "/panel/api/clients/subLinks/")
	out := []string{}
	for _, rec := range s.clients {
		if rec.SubID == sub && rec.Enable {
			out = append(out, "vless://"+rec.ID+"@fake.example:443#"+rec.Email)
		}
	}
	reply(w, http.StatusOK, true, "", out)
}

func (s *Server) options(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply(w, http.StatusOK, true, "", s.inbounds)
}
