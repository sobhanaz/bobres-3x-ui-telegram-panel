// Package marzbanfake is an in-memory fake of the Marzban panel API for
// tests. It follows the real panel's source (app/routers, app/db/crud.py):
// the form login that returns a bearer token, users keyed by username with
// expiry in unix seconds, the status rules a modification applies, and the
// validation errors of UserCreate. It is a model of that source, not a real
// Marzban: a CI job against a container is the check for drift.
package marzbanfake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9\-_@.]{3,32}$`)

type user struct {
	username       string
	status         string
	expire         int64 // unix seconds, 0 unlimited
	dataLimit      int64
	used           int64
	holdDuration   int64
	note           string
	proxies        map[string]bool
	inbounds       map[string][]string // explicit tags per protocol; empty = all
	subscriptionAt string
}

type inbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Network  string `json:"network"`
	TLS      string `json:"tls"`
	Port     int    `json:"port"`
}

// Server is a running fake panel. The embedded *httptest.Server gives URL and Close.
type Server struct {
	*httptest.Server
	admin, password string

	mu       sync.Mutex
	users    map[string]*user
	inbounds []inbound
	tokens   map[string]bool
	logins   int
	failNext int
	calls    map[string]int
	version  string
	subPref  string
	now      func() time.Time
}

// NewServer starts a fake panel with one admin.
func NewServer(admin, password string) *Server {
	s := &Server{
		admin: admin, password: password,
		users:  map[string]*user{},
		tokens: map[string]bool{},
		calls:  map[string]int{},
		inbounds: []inbound{
			{Tag: "VLESS TCP REALITY", Protocol: "vless", Network: "tcp", TLS: "reality", Port: 443},
			{Tag: "VMess WS", Protocol: "vmess", Network: "ws", TLS: "none", Port: 8443},
			{Tag: "Trojan TCP", Protocol: "trojan", Network: "tcp", TLS: "tls", Port: 8444},
		},
		version: "0.8.4",
		now:     time.Now,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/token", s.login)
	route := func(pattern, name string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.wrap(name, h))
	}
	route("/api/system", "system", s.system)
	route("/api/inbounds", "inbounds", s.listInbounds)
	route("/api/user", "create", s.create)
	route("/api/user/", "user", s.userRoute)
	s.Server = httptest.NewServer(mux)
	return s
}

// ExpireTokens invalidates every issued token (the panel's 24 h JWT expiry).
func (s *Server) ExpireTokens() { s.mu.Lock(); s.tokens = map[string]bool{}; s.mu.Unlock() }

// Logins is how many successful logins the panel has seen.
func (s *Server) Logins() int { s.mu.Lock(); defer s.mu.Unlock(); return s.logins }

// SetFailNext makes the next n authenticated calls fail with HTTP 500.
func (s *Server) SetFailNext(n int) { s.mu.Lock(); s.failNext = n; s.mu.Unlock() }

// Calls is how many authenticated calls hit a route ("create", "user", ...).
func (s *Server) Calls(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

// SetVersion changes what /api/system reports.
func (s *Server) SetVersion(v string) { s.mu.Lock(); s.version = v; s.mu.Unlock() }

// SetSubscriptionPrefix makes subscription_url absolute (the panel's
// XRAY_SUBSCRIPTION_URL_PREFIX); empty keeps it relative.
func (s *Server) SetSubscriptionPrefix(p string) { s.mu.Lock(); s.subPref = p; s.mu.Unlock() }

// AddUsage simulates traffic; a user past the quota becomes limited.
func (s *Server) AddUsage(username string, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[username]; ok {
		u.used += bytes
		if u.dataLimit > 0 && u.used >= u.dataLimit && u.status == "active" {
			u.status = "limited"
		}
		if u.status == "on_hold" && bytes > 0 {
			u.status = "active"
			if u.holdDuration > 0 {
				u.expire = s.now().Unix() + u.holdDuration
			}
		}
	}
}

// Has reports whether a user exists.
func (s *Server) Has(username string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.users[username]
	return ok
}

// Seed adds a user the way an operator would have, with a note and no BOBRES tag.
func (s *Server) Seed(username, note string, expire, dataLimit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[username] = &user{
		username: username, status: "active", expire: expire, dataLimit: dataLimit, note: note,
		proxies: map[string]bool{"vless": true}, subscriptionAt: token(username),
	}
}

// Snapshot is a user's state as the tests assert it.
type Snapshot struct {
	Status       string
	Expire       int64
	DataLimit    int64
	Used         int64
	HoldDuration int64
	Note         string
	Protocols    []string
	Tags         map[string][]string
}

// Snapshot returns a user's state; ok is false when it does not exist.
func (s *Server) Snapshot(username string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[username]
	if !ok {
		return Snapshot{}, false
	}
	var p []string
	for k := range u.proxies {
		p = append(p, k)
	}
	sort.Strings(p)
	return Snapshot{Status: u.status, Expire: u.expire, DataLimit: u.dataLimit, Used: u.used,
		HoldDuration: u.holdDuration, Note: u.note, Protocols: p, Tags: u.inbounds}, true
}

func token(username string) string {
	h := sha256.Sum256([]byte("sub:" + username))
	return hex.EncodeToString(h[:])[:24]
}

func detail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": msg})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		detail(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	if err := r.ParseForm(); err != nil {
		detail(w, http.StatusUnprocessableEntity, "bad form")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.PostForm.Get("username") != s.admin || r.PostForm.Get("password") != s.password {
		detail(w, http.StatusUnauthorized, "Incorrect username or password")
		return
	}
	s.logins++
	tok := fmt.Sprintf("jwt-%d-%d", s.logins, s.now().UnixNano())
	s.tokens[tok] = true
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"access_token": tok, "token_type": "bearer"})
}

func (s *Server) wrap(name string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !s.tokens[tok] || tok == "" {
			s.mu.Unlock()
			detail(w, http.StatusUnauthorized, "Not authenticated")
			return
		}
		s.calls[name]++
		fail := s.failNext > 0
		if fail {
			s.failNext--
		}
		s.mu.Unlock()
		if fail {
			detail(w, http.StatusInternalServerError, "boom")
			return
		}
		h(w, r)
	}
}

func (s *Server) system(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	v := s.version
	s.mu.Unlock()
	reply(w, map[string]any{"version": v, "mem_total": 1, "total_user": 0})
}

func (s *Server) listInbounds(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]inbound{}
	for _, in := range s.inbounds {
		out[in.Protocol] = append(out[in.Protocol], in)
	}
	reply(w, out)
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// body is the shared shape of UserCreate and UserModify; a pointer is
// "omitted or null", which a modification reads as "no change".
type body struct {
	Username     string              `json:"username"`
	Proxies      map[string]any      `json:"proxies"`
	Inbounds     map[string][]string `json:"inbounds"`
	Status       *string             `json:"status"`
	Expire       *int64              `json:"expire"`
	DataLimit    *int64              `json:"data_limit"`
	Note         *string             `json:"note"`
	HoldDuration *int64              `json:"on_hold_expire_duration"`
	Strategy     *string             `json:"data_limit_reset_strategy"`
}

func (s *Server) hasProtocol(p string) bool {
	for _, in := range s.inbounds {
		if in.Protocol == p {
			return true
		}
	}
	return false
}

func (s *Server) hasTag(tag string) bool {
	for _, in := range s.inbounds {
		if in.Tag == tag {
			return true
		}
	}
	return false
}

// check applies the validation both UserCreate and UserModify share.
func (s *Server) check(b *body) (int, string) {
	for p := range b.Proxies {
		if !s.hasProtocol(p) {
			return http.StatusBadRequest, fmt.Sprintf("Protocol %s is disabled on your server", p)
		}
	}
	for p, tags := range b.Inbounds {
		if _, ok := b.Proxies[p]; !ok && b.Proxies != nil {
			return http.StatusUnprocessableEntity, "Protocol " + p + " is disabled in your proxies"
		}
		for _, t := range tags {
			if !s.hasTag(t) {
				return http.StatusUnprocessableEntity, fmt.Sprintf("Inbound %s doesn't exist", t)
			}
		}
	}
	return 0, ""
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		detail(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	var b body
	if json.NewDecoder(r.Body).Decode(&b) != nil {
		detail(w, http.StatusUnprocessableEntity, "bad json")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case !usernameRe.MatchString(b.Username):
		detail(w, http.StatusUnprocessableEntity, "Username only can be 3 to 32 characters and contain a-z, 0-9, and underscores in between.")
		return
	case len(b.Proxies) == 0:
		detail(w, http.StatusUnprocessableEntity, "Each user needs at least one proxy")
		return
	}
	if code, msg := s.check(&b); code != 0 {
		detail(w, code, msg)
		return
	}
	st := "active"
	if b.Status != nil {
		st = *b.Status
	}
	if st != "active" && st != "on_hold" {
		detail(w, http.StatusUnprocessableEntity, "invalid status")
		return
	}
	hold := int64(0)
	if b.HoldDuration != nil {
		hold = *b.HoldDuration
	}
	if st == "on_hold" {
		if hold <= 0 {
			detail(w, http.StatusUnprocessableEntity, "User cannot be on hold without a valid on_hold_expire_duration.")
			return
		}
		if b.Expire != nil && *b.Expire != 0 {
			detail(w, http.StatusUnprocessableEntity, "User cannot be on hold with specified expire.")
			return
		}
	}
	if _, ok := s.users[b.Username]; ok {
		detail(w, http.StatusConflict, "User already exists")
		return
	}
	u := &user{username: b.Username, status: st, holdDuration: hold, proxies: map[string]bool{}, inbounds: b.Inbounds, subscriptionAt: token(b.Username)}
	if b.Expire != nil {
		u.expire = *b.Expire
	}
	if b.DataLimit != nil {
		u.dataLimit = *b.DataLimit
	}
	if b.Note != nil {
		u.note = *b.Note
	}
	for p := range b.Proxies {
		u.proxies[p] = true
	}
	s.users[u.username] = u
	reply(w, s.render(u))
}

func (s *Server) userRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/user/")
	name, sub, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		detail(w, http.StatusNotFound, "User not found")
		return
	}
	switch {
	case sub == "" && r.Method == http.MethodGet:
		reply(w, s.render(u))
	case sub == "" && r.Method == http.MethodDelete:
		delete(s.users, name)
		reply(w, map[string]string{"detail": "User successfully deleted"})
	case sub == "" && r.Method == http.MethodPut:
		s.modify(w, r, u)
	case sub == "reset" && r.Method == http.MethodPost:
		u.used = 0
		if u.status == "limited" {
			u.status = "active"
		}
		reply(w, s.render(u))
	default:
		detail(w, http.StatusNotFound, "Not Found")
	}
}

// modify follows crud.update_user: omitted fields stay, and a new quota or
// expiry re-evaluates the status of an active, limited or expired user.
func (s *Server) modify(w http.ResponseWriter, r *http.Request, u *user) {
	var b body
	if json.NewDecoder(r.Body).Decode(&b) != nil {
		detail(w, http.StatusUnprocessableEntity, "bad json")
		return
	}
	if code, msg := s.check(&b); code != 0 {
		detail(w, code, msg)
		return
	}
	if b.Status != nil {
		switch *b.Status {
		case "active", "disabled", "on_hold":
			u.status = *b.Status
		default:
			detail(w, http.StatusUnprocessableEntity, "invalid status")
			return
		}
	}
	if b.HoldDuration != nil {
		u.holdDuration = *b.HoldDuration
	}
	if b.DataLimit != nil {
		u.dataLimit = *b.DataLimit
		if u.status != "expired" && u.status != "disabled" {
			if u.dataLimit == 0 || u.used < u.dataLimit {
				if u.status != "on_hold" {
					u.status = "active"
				}
			} else {
				u.status = "limited"
			}
		}
	}
	if b.Expire != nil {
		u.expire = *b.Expire
		if u.status == "active" || u.status == "expired" {
			if u.expire == 0 || u.expire > s.now().Unix() {
				u.status = "active"
			} else {
				u.status = "expired"
			}
		}
	}
	if b.Note != nil {
		u.note = *b.Note
	}
	if len(b.Proxies) > 0 {
		u.proxies = map[string]bool{}
		for p := range b.Proxies {
			u.proxies[p] = true
		}
	}
	if len(b.Inbounds) > 0 {
		u.inbounds = b.Inbounds
	}
	reply(w, s.render(u))
}

func (s *Server) render(u *user) map[string]any {
	var links []string
	for _, in := range s.inbounds {
		if !u.proxies[in.Protocol] {
			continue
		}
		if tags := u.inbounds[in.Protocol]; len(tags) > 0 && !contains(tags, in.Tag) {
			continue
		}
		links = append(links, fmt.Sprintf("%s://%s@fake.example:%d#%s", in.Protocol, u.username, in.Port, in.Tag))
	}
	sub := "/sub/" + u.subscriptionAt
	if s.subPref != "" {
		sub = strings.TrimRight(s.subPref, "/") + sub
	}
	var expire, limit any
	if u.expire != 0 {
		expire = u.expire
	}
	if u.dataLimit != 0 {
		limit = u.dataLimit
	}
	return map[string]any{
		"username": u.username, "status": u.status, "expire": expire, "data_limit": limit,
		"used_traffic": u.used, "on_hold_expire_duration": u.holdDuration, "note": u.note,
		"links": links, "subscription_url": sub,
	}
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
