// Package marzbanfake is an in-memory fake of the Marzban panel API for
// tests. It models Marzban v0.8.4 as the real image answers (checked against
// gozargah/marzban:v0.8.4 by the CI job "Marzban integration" and by hand):
// the form login that returns a bearer token, users keyed by username with
// expiry in unix seconds, the status rules a modification applies, the
// validation errors of UserCreate and UserModify (HTTP 422 with the detail
// as an object of field to message), and the quirks BOBRES has to meet: a
// user that names no inbounds gets none, "on_hold" needs its duration in the
// same request, a reset turns a disabled or on-hold user on, and a user
// without an owner admin cannot be deleted without a 500. It is a model, not
// a real Marzban: that job is the check for drift.
package marzbanfake

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The panel's username rule is ^(?=\w{3,32}\b)[a-zA-Z0-9-_@.]+...$: only
// those characters, and the name starts with 3 to 32 letters, digits or
// underscores before the first character that is not one.
var (
	usernameChars = regexp.MustCompile(`^[a-zA-Z0-9\-_@.]+$`)
	usernameLead  = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}(?:[^a-zA-Z0-9_]|$)`)
)

// protoNames are the panel's enum members, as its error messages print them.
var protoNames = map[string]string{"vmess": "VMess", "vless": "VLESS", "trojan": "Trojan", "shadowsocks": "Shadowsocks"}

type user struct {
	username     string
	status       string
	expire       int64 // unix seconds, 0 unlimited
	dataLimit    int64
	used         int64
	holdDuration int64 // seconds, 0 none
	note         string
	proxies      map[string]bool
	inbounds     map[string][]string // the tags attached, per protocol; an empty list attaches none
	owner        string              // the admin that owns it; "" for a user made through SUDO_USERNAME
	createdAt    time.Time
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

	mu        sync.Mutex
	users     map[string]*user
	ghosts    map[string]bool // deleted rows whose account the running core still holds
	inbounds  []inbound
	tokens    map[string]bool
	logins    int
	failNext  int
	calls     map[string]int
	version   string
	subPref   string
	ownerless bool
	now       func() time.Time
}

// NewServer starts a fake panel with one admin (a database admin, as made by
// `marzban cli admin create`).
func NewServer(admin, password string) *Server {
	s := &Server{
		admin: admin, password: password,
		users:  map[string]*user{},
		ghosts: map[string]bool{},
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
	mux.HandleFunc("/sub/", s.subscription)
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

// SetOwnerless makes the users created from now on belong to no admin, as the
// ones made through the panel's SUDO_USERNAME environment account do. Such a
// user's deletion removes the row and then answers 500, and the running core
// keeps the account (see Ghosts).
func (s *Server) SetOwnerless(on bool) { s.mu.Lock(); s.ownerless = on; s.mu.Unlock() }

// Ghosts lists the deleted users whose account the running core still holds,
// the ones that can still connect.
func (s *Server) Ghosts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.ghosts))
	for n := range s.ghosts {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// SetClock replaces the clock the panel reads (subscription tokens, expiry).
func (s *Server) SetClock(now func() time.Time) { s.mu.Lock(); s.now = now; s.mu.Unlock() }

// AddUsage simulates traffic, as the panel's jobs book it: an active user
// past its quota becomes limited, and an on-hold user that connects becomes
// active with its clock started.
func (s *Server) AddUsage(username string, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[username]
	if !ok {
		return
	}
	u.used += bytes
	if u.status == "on_hold" && bytes > 0 {
		u.status = "active"
		if u.holdDuration > 0 {
			u.expire = s.now().Unix() + u.holdDuration
		}
		u.holdDuration = 0
	}
	if u.dataLimit > 0 && u.used >= u.dataLimit && u.status == "active" {
		u.status = "limited"
	}
}

// Review runs the panel's periodic review of active users (every 10 s there):
// the ones past their expiry become expired.
func (s *Server) Review() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.status == "active" && u.expire != 0 && u.expire <= s.now().Unix() {
			u.status = "expired"
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
		proxies: map[string]bool{"vless": true}, inbounds: map[string][]string{"vless": {"VLESS TCP REALITY"}},
		owner: s.admin, createdAt: s.now(),
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
	Owner        string
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
		HoldDuration: u.holdDuration, Note: u.note, Protocols: p, Tags: u.inbounds, Owner: u.owner}, true
}

// subToken is the panel's subscription token: the username and the time of
// the call (rounded up to a second) in base64, and ten characters of a keyed
// hash. It is made afresh for every response, so two reads a second apart
// give different, equally valid, URLs.
func (s *Server) subToken(username string) string {
	now := s.now()
	ts := now.Unix()
	if now.Nanosecond() != 0 {
		ts++
	}
	data := base64.RawURLEncoding.EncodeToString([]byte(username + "," + strconv.FormatInt(ts, 10)))
	return data + signature(data)
}

func signature(data string) string {
	h := sha256.Sum256([]byte(data + "marzbanfake-secret"))
	return base64.RawURLEncoding.EncodeToString(h[:])[:10]
}

// userOfToken resolves a subscription token like the panel: a valid
// signature, an existing user, and a token made after the user was.
func (s *Server) userOfToken(token string) (*user, bool) {
	if len(token) < 15 {
		return nil, false
	}
	data, sig := token[:len(token)-10], token[len(token)-10:]
	if sig != signature(data) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return nil, false
	}
	name, ts, ok := strings.Cut(string(raw), ",")
	n, err := strconv.ParseInt(ts, 10, 64)
	if !ok || err != nil {
		return nil, false
	}
	u, ok := s.users[name]
	if !ok || u.createdAt.After(time.Unix(n, 0)) {
		return nil, false
	}
	return u, true
}

// detail answers like FastAPI's HTTPException: {"detail": "message"}.
func detail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	if code == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": msg})
}

// invalid answers a body the panel rejects: Marzban's own handler turns
// pydantic's errors into HTTP 422 {"detail": {"<field>": "<message>"}}.
func invalid(w http.ResponseWriter, fields map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]any{"detail": fields})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		detail(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	if err := r.ParseForm(); err != nil || r.PostForm.Get("username") == "" || r.PostForm.Get("password") == "" {
		// A JSON body, or a form without the fields (FastAPI's OAuth2 form).
		invalid(w, map[string]string{"username": "Field required", "password": "Field required"})
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
		auth := r.Header.Get("Authorization")
		tok := strings.TrimPrefix(auth, "Bearer ")
		switch {
		case auth == "":
			s.mu.Unlock()
			detail(w, http.StatusUnauthorized, "Not authenticated")
			return
		case !s.tokens[tok]:
			s.mu.Unlock()
			detail(w, http.StatusUnauthorized, "Could not validate credentials")
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

// body is the shared shape of UserCreate and UserModify; a pointer or a nil
// map is "omitted or null", which a modification reads as "no change".
type body struct {
	Username     *string             `json:"username"`
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

func (s *Server) tagsOf(protocol string) []string {
	var tags []string
	for _, in := range s.inbounds {
		if in.Protocol == protocol {
			tags = append(tags, in.Tag)
		}
	}
	return tags
}

// checkShared applies the validators UserCreate and UserModify have in
// common, filling errs by field.
func (s *Server) checkShared(b *body, errs map[string]string) {
	for p := range b.Proxies {
		if _, ok := protoNames[p]; !ok {
			errs["proxies"] = "Input should be 'vmess', 'vless', 'trojan' or 'shadowsocks'"
		}
	}
	if b.DataLimit != nil && *b.DataLimit < 0 {
		errs["data_limit"] = "Input should be greater than or equal to 0"
	}
	if b.Note != nil && len(*b.Note) > 500 {
		errs["note"] = "Value error, User's note can be a maximum of 500 character"
	}
	for _, tags := range b.Inbounds {
		for _, t := range tags {
			if !s.hasTag(t) {
				errs["inbounds"] = fmt.Sprintf("Value error, Inbound %s doesn't exist", t)
			}
		}
	}
}

// onHoldRules is the status validator both bodies share.
func onHoldRules(b *body, errs map[string]string) {
	if b.Status == nil || *b.Status != "on_hold" {
		return
	}
	switch {
	case b.HoldDuration == nil || *b.HoldDuration == 0:
		errs["status"] = "Value error, User cannot be on hold without a valid on_hold_expire_duration."
	case b.Expire != nil && *b.Expire != 0:
		errs["status"] = "Value error, User cannot be on hold with specified expire."
	}
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		detail(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	var b body
	if json.NewDecoder(r.Body).Decode(&b) != nil {
		invalid(w, map[string]string{"body": "JSON decode error"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	errs := map[string]string{}
	switch {
	case b.Username == nil:
		errs["username"] = "Field required"
	case !usernameChars.MatchString(*b.Username) || !usernameLead.MatchString(*b.Username):
		errs["username"] = "Value error, Username only can be 3 to 32 characters and contain a-z, 0-9, and underscores in between."
	}
	if len(b.Proxies) == 0 {
		errs["proxies"] = "Value error, Each user needs at least one proxy"
	}
	s.checkShared(&b, errs)
	if b.Status != nil && *b.Status != "active" && *b.Status != "on_hold" {
		errs["status"] = "Input should be 'active' or 'on_hold'"
	}
	onHoldRules(&b, errs)
	if len(errs) > 0 {
		invalid(w, errs)
		return
	}
	for p := range b.Proxies {
		if !s.hasProtocol(p) {
			detail(w, http.StatusBadRequest, fmt.Sprintf("Protocol ProxyTypes.%s is disabled on your server", protoNames[p]))
			return
		}
	}
	if s.byName(*b.Username) != nil {
		detail(w, http.StatusConflict, "User already exists")
		return
	}
	u := &user{username: *b.Username, status: "active", proxies: map[string]bool{}, inbounds: map[string][]string{}, createdAt: s.now()}
	if !s.ownerless {
		u.owner = s.admin
	}
	if b.Status != nil {
		u.status = *b.Status
	}
	if b.HoldDuration != nil {
		u.holdDuration = *b.HoldDuration
	}
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
		// The inbounds validator runs only when the body has an "inbounds"
		// key. Without it (v0.8, pydantic 2) the user gets no inbound at all;
		// with it, a protocol that lists no tag gets all of its tags.
		u.inbounds[p] = []string{}
		if b.Inbounds != nil {
			if tags := b.Inbounds[p]; len(tags) > 0 {
				u.inbounds[p] = append([]string{}, tags...)
			} else {
				u.inbounds[p] = s.tagsOf(p)
			}
		}
	}
	s.users[u.username] = u
	reply(w, s.render(u))
}

// byName finds a user by its exact username. The model declares the column
// NOCASE, but the table the migrations make does not have it: on the real
// panel "Abc" and "abc" are two users, and a lookup is case-sensitive.
func (s *Server) byName(name string) *user { return s.users[name] }

func (s *Server) userRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/user/")
	name, sub, _ := strings.Cut(rest, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.byName(name)
	if u == nil {
		detail(w, http.StatusNotFound, "User not found")
		return
	}
	switch {
	case sub == "" && r.Method == http.MethodGet:
		reply(w, s.render(u))
	case sub == "" && r.Method == http.MethodDelete:
		s.remove(w, u)
	case sub == "" && r.Method == http.MethodPut:
		s.modify(w, r, u)
	case sub == "reset" && r.Method == http.MethodPost:
		// The panel tests "status not in (UserStatus.expired or UserStatus.disabled)",
		// which Python reads as "not expired": a disabled or on-hold user is
		// turned on by a reset, and a limited one is active again.
		u.used = 0
		if u.status != "expired" {
			u.status = "active"
		}
		reply(w, s.render(u))
	default:
		detail(w, http.StatusNotFound, "Not Found")
	}
}

// remove deletes the row. For a user with no owner the panel then fails on
// the report it builds (Admin.model_validate(None)) before it takes the
// account out of the running core: the answer is a 500 and the account lives
// on, unless the user was disabled first (which does reach the core).
func (s *Server) remove(w http.ResponseWriter, u *user) {
	delete(s.users, u.username)
	if u.owner == "" {
		if u.status == "active" || u.status == "on_hold" {
			s.ghosts[u.username] = true
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
		return
	}
	reply(w, map[string]string{"detail": "User successfully deleted"})
}

// modify follows crud.update_user: omitted fields stay, and a new quota or
// expiry re-evaluates the status of an active, limited or expired user.
func (s *Server) modify(w http.ResponseWriter, r *http.Request, u *user) {
	var b body
	if json.NewDecoder(r.Body).Decode(&b) != nil {
		invalid(w, map[string]string{"body": "JSON decode error"})
		return
	}
	errs := map[string]string{}
	s.checkShared(&b, errs)
	if b.Status != nil && *b.Status != "active" && *b.Status != "disabled" && *b.Status != "on_hold" {
		errs["status"] = "Input should be 'active', 'disabled' or 'on_hold'"
	}
	onHoldRules(&b, errs)
	if len(errs) > 0 {
		invalid(w, errs)
		return
	}
	for p := range b.Proxies {
		if !s.hasProtocol(p) {
			detail(w, http.StatusBadRequest, fmt.Sprintf("Protocol ProxyTypes.%s is disabled on your server", protoNames[p]))
			return
		}
	}
	if len(b.Proxies) > 0 {
		u.proxies = map[string]bool{}
		for p := range b.Proxies {
			u.proxies[p] = true
			if _, ok := u.inbounds[p]; !ok {
				u.inbounds[p] = []string{}
			}
		}
	}
	for p, tags := range b.Inbounds {
		u.inbounds[p] = append([]string{}, tags...)
	}
	if b.Status != nil {
		u.status = *b.Status
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
	if b.HoldDuration != nil {
		u.holdDuration = *b.HoldDuration
	}
	reply(w, s.render(u))
}

func (s *Server) links(u *user) []string {
	var links []string
	for _, in := range s.inbounds {
		if !u.proxies[in.Protocol] || !contains(u.inbounds[in.Protocol], in.Tag) {
			continue
		}
		links = append(links, fmt.Sprintf("%s://%s@fake.example:%d#%s", in.Protocol, u.username, in.Port, url.QueryEscape(in.Tag)))
	}
	return links
}

// subscription serves GET /sub/<token>/: the user's share links, base64 in
// one text body. A token that does not verify is a plain 404.
func (s *Server) subscription(w http.ResponseWriter, r *http.Request) {
	token := strings.Trim(strings.TrimPrefix(r.URL.Path, "/sub/"), "/")
	s.mu.Lock()
	u, ok := s.userOfToken(token)
	var links []string
	if ok {
		links = s.links(u)
	}
	s.mu.Unlock()
	if !ok || r.Method != http.MethodGet || strings.Contains(token, "/") {
		detail(w, http.StatusNotFound, "Not Found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))))
}

func (s *Server) render(u *user) map[string]any {
	sub := "/sub/" + s.subToken(u.username)
	if s.subPref != "" {
		sub = strings.TrimRight(s.subPref, "/") + sub
	}
	var expire, limit, hold, admin any
	if u.expire != 0 {
		expire = u.expire
	}
	if u.dataLimit != 0 {
		limit = u.dataLimit
	}
	if u.holdDuration != 0 {
		hold = u.holdDuration
	}
	if u.owner != "" {
		admin = map[string]any{"username": u.owner, "is_sudo": true}
	}
	proxies := map[string]any{}
	excluded := map[string][]string{}
	for p := range u.proxies {
		proxies[p] = map[string]any{}
		excluded[p] = []string{}
		for _, t := range s.tagsOf(p) {
			if !contains(u.inbounds[p], t) {
				excluded[p] = append(excluded[p], t)
			}
		}
	}
	links := s.links(u)
	if links == nil {
		links = []string{}
	}
	return map[string]any{
		"username": u.username, "status": u.status, "expire": expire, "data_limit": limit,
		"data_limit_reset_strategy": "no_reset", "used_traffic": u.used, "lifetime_used_traffic": u.used,
		"on_hold_expire_duration": hold, "on_hold_timeout": nil, "note": nilIfEmpty(u.note),
		"proxies": proxies, "inbounds": u.inbounds, "excluded_inbounds": excluded,
		"created_at": u.createdAt.UTC().Format("2006-01-02T15:04:05.000000"),
		"links":      links, "subscription_url": sub, "admin": admin,
	}
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}
