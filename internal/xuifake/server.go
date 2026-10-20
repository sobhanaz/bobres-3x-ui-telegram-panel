// Package xuifake is an in-memory fake of the 3x-ui panel API for tests.
// It mirrors the real response envelope {success,msg,obj}, Bearer auth, and the
// units used by 3x-ui v3.8.5 (expiryTime in unix milliseconds, totalGB in bytes).
//
// Client groups follow the real panels (v3.8.5 and v3.9.0, probed in CI by the
// adapter's integration tests): clients/add stores the client's "group";
// clients/update REPLACES the row, so a body without a group clears it
// (LostFields reports that); bulkAdjust and resetTraffic leave it; bulkAdd
// puts the listed clients in a group whatever group they have, counts only the
// ones that changed and ignores emails it does not know; a group made by
// adding clients to it vanishes from the list with its last client, while
// bulkAdd of a new name leaves a group that stays when empty.
package xuifake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
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
	Group      string `json:"group,omitempty"`
}

type record struct {
	client
	rowID      int64
	up, down   int64
	inboundIDs []int
}

// stored is the client as the real panel returns it from clients/get: the
// numeric row id in "id" and the protocol UUID in "uuid".
func (r *record) stored() map[string]any {
	return map[string]any{
		"id": r.rowID, "uuid": r.ID, "email": r.Email, "subId": r.SubID, "totalGB": r.TotalGB,
		"expiryTime": r.ExpiryTime, "limitIp": r.LimitIP, "enable": r.Enable, "tgId": r.TgID,
		"comment": r.Comment, "flow": r.Flow, "group": r.Group,
	}
}

// Server is a running fake panel. The embedded *httptest.Server gives URL and Close.
type Server struct {
	*httptest.Server
	token string

	mu           sync.Mutex
	clients      map[string]*record
	inbounds     []map[string]any
	nextRow      int64
	failNext     int
	latency      time.Duration
	calls        map[string]int
	lost         []string
	version      string          // server/status panelVersion
	failOn       map[string]int  // route name → calls left to fail
	groupRows    map[string]bool // groups that stay in the list while empty
	groupBatches []int           // emails of every bulkAdd call, in order
	noGroups     bool            // a panel without the group API: 404 on its routes
	failAfter    map[string]int  // route name → calls that succeed before failOn applies
	hidden       map[string]bool // clients the next paged listing leaves out
	ignoreAdd    bool            // bulkAdd answers but changes nothing
	reuseOwn     bool            // clients/add of a client the panel has (same subId) succeeds and changes nothing
}

// NewServer starts a fake panel that accepts only the given Bearer token.
func NewServer(token string) *Server {
	s := &Server{
		token:     token,
		clients:   map[string]*record{},
		calls:     map[string]int{},
		failOn:    map[string]int{},
		failAfter: map[string]int{},
		hidden:    map[string]bool{},
		groupRows: map[string]bool{},
		version:   "3.9.0",
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
	// The group routes answer 404 (a plain page, as an older panel does) when
	// SetNoGroups(true) was called.
	groupRoute := func(pattern, name string, h http.HandlerFunc) {
		route(pattern, name, func(w http.ResponseWriter, r *http.Request) {
			s.mu.Lock()
			gone := s.noGroups
			s.mu.Unlock()
			if gone {
				http.NotFound(w, r)
				return
			}
			h(w, r)
		})
	}
	route("/panel/api/clients/add", "add", s.add)
	route("/panel/api/clients/get/", "get", s.get)
	route("/panel/api/clients/del/", "del", s.del)
	route("/panel/api/clients/update/", "update", s.update)
	route("/panel/api/clients/bulkAdjust", "bulkAdjust", s.bulkAdjust)
	route("/panel/api/clients/resetTraffic/", "resetTraffic", s.reset)
	route("/panel/api/clients/traffic/", "traffic", s.traffic)
	route("/panel/api/clients/links/", "links", s.links)
	route("/panel/api/clients/subLinks/", "subLinks", s.subLinks)
	groupRoute("/panel/api/clients/list/paged", "listPaged", s.listPaged)
	groupRoute("/panel/api/clients/groups", "groups", s.groups)
	groupRoute("/panel/api/clients/groups/bulkAdd", "groupBulkAdd", s.groupBulkAdd)
	groupRoute("/panel/api/clients/groups/bulkRemove", "groupBulkRemove", s.groupBulkRemove)
	groupRoute("/panel/api/clients/groups/", "groupEmails", s.groupEmails)
	route("/panel/api/inbounds/options", "options", s.options)
	route("/panel/api/server/status", "status", s.status)
	s.Server = httptest.NewServer(mux)
	return s
}

// SetFailNext makes the next n authenticated calls fail with HTTP 500.
func (s *Server) SetFailNext(n int) { s.mu.Lock(); s.failNext = n; s.mu.Unlock() }

// FailRoute makes the next n authenticated calls to one route (e.g. "add")
// fail with HTTP 500, as a panel that drops the connection would.
func (s *Server) FailRoute(name string, n int) {
	s.mu.Lock()
	s.failAfter[name], s.failOn[name] = 0, n
	s.mu.Unlock()
}

// FailRouteAfter lets the next ok calls to a route succeed, then fails the n
// after them: a long job that breaks half way.
func (s *Server) FailRouteAfter(name string, ok, n int) {
	s.mu.Lock()
	s.failAfter[name], s.failOn[name] = ok, n
	s.mu.Unlock()
}

// HideOnce leaves a client out of the next paged listing, as a page that moved
// up when another client was deleted meanwhile does.
func (s *Server) HideOnce(email string) { s.mu.Lock(); s.hidden[email] = true; s.mu.Unlock() }

// IgnoreGroupAdds makes bulkAdd answer without changing anything.
func (s *Server) IgnoreGroupAdds(on bool) { s.mu.Lock(); s.ignoreAdd = on; s.mu.Unlock() }

// SetAddReusesClient makes clients/add behave as real v3.8.5 and v3.9.0 panels
// do for an email the panel has: with the same subscription id it SUCCEEDS and
// changes nothing but the inbounds the client is on (its group and limits stay
// as they are), with another one it is refused ("email already in use"). Off
// (the default), any duplicate is refused ("Duplicate email"), which older tests rely on.
func (s *Server) SetAddReusesClient(on bool) { s.mu.Lock(); s.reuseOwn = on; s.mu.Unlock() }

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

// SetPanelVersion sets the panelVersion server/status reports ("" = none).
func (s *Server) SetPanelVersion(v string) { s.mu.Lock(); s.version = v; s.mu.Unlock() }

// SetInboundsEnabled flips the enable flag of every inbound.
func (s *Server) SetInboundsEnabled(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, in := range s.inbounds {
		in["enable"] = on
	}
}

// Seed stores a client directly, as if created earlier (e.g. by another tool,
// or by an attempt whose response was lost). It is in no group.
func (s *Server) Seed(email, subID string, inboundIDs []int) {
	s.SeedGrouped(email, subID, "", inboundIDs)
}

// SeedGrouped is Seed with a group, as a panel operator (or an older BOBRES)
// left the client.
func (s *Server) SeedGrouped(email, subID, group string, inboundIDs []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextRow++
	s.clients[email] = &record{client: client{ID: fakeUUID(s.nextRow), Email: email, SubID: subID, Enable: true, Group: group}, rowID: s.nextRow, inboundIDs: inboundIDs}
}

// SetNoGroups makes the group routes (and the paged client listing) answer
// 404, as a panel without client groups does.
func (s *Server) SetNoGroups(on bool) { s.mu.Lock(); s.noGroups = on; s.mu.Unlock() }

// SetGroup puts a client in a group directly, the way an operator does in the
// panel's own page ("" takes it out of its group). It reports whether the
// client exists.
func (s *Server) SetGroup(email, group string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.clients[email]
	if ok {
		r.Group = group
	}
	return ok
}

// Group returns a client's group ("" when it has none); ok is false for an
// unknown client.
func (s *Server) Group(email string) (group string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, found := s.clients[email]
	if !found {
		return "", false
	}
	return r.Group, true
}

// GroupMembers returns the emails of the clients in a group, sorted.
func (s *Server) GroupMembers(group string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.members(group)
}

func (s *Server) members(group string) []string {
	out := []string{}
	for e, r := range s.clients {
		if group != "" && r.Group == group {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// GroupBatches lists how many emails each bulkAdd call carried, in order.
func (s *Server) GroupBatches() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.groupBatches...)
}

func fakeUUID(row int64) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", row) }

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
	Group      string
}, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, found := s.clients[email]
	if !found {
		return spec, false
	}
	spec.Enable, spec.ExpiryTime, spec.TotalGB, spec.Up, spec.Down, spec.Group = r.Enable, r.ExpiryTime, r.TotalGB, r.up, r.down, r.Group
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
		} else if s.failOn[name] > 0 {
			if s.failAfter[name] > 0 {
				s.failAfter[name]--
			} else {
				fail = true
				s.failOn[name]--
			}
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
	if existing, dup := s.clients[b.Client.Email]; dup {
		switch {
		case s.reuseOwn && b.Client.SubID == existing.SubID:
			for _, id := range b.InboundIDs {
				if !slices.Contains(existing.inboundIDs, id) {
					existing.inboundIDs = append(existing.inboundIDs, id)
				}
			}
			reply(w, http.StatusOK, true, "ok", nil)
		case s.reuseOwn:
			reply(w, http.StatusOK, false, "Something went wrong (email already in use: "+b.Client.Email+"\n)", nil)
		default:
			reply(w, http.StatusOK, false, "Duplicate email: "+b.Client.Email, nil)
		}
		return
	}
	s.nextRow++
	if b.Client.ID == "" { // the panel generates the protocol UUID
		b.Client.ID = fakeUUID(s.nextRow)
	}
	s.clients[b.Client.Email] = &record{client: b.Client, rowID: s.nextRow, inboundIDs: b.InboundIDs}
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
	reply(w, http.StatusOK, true, "", map[string]any{"client": rec.stored(), "inboundIds": rec.inboundIDs, "usedTraffic": rec.up + rec.down})
}

// update follows clients/update: the body is a flat client whose "id" is the
// protocol UUID. The whole row is replaced, so a body without the UUID, the
// subscription id or the group would lose them (the real panel writes the
// group of the body, and a body without one clears it): the fake records that
// as LostFields, which tests assert stays empty.
func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	var b map[string]json.RawMessage
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&b) != nil {
		reply(w, http.StatusOK, false, "bad request", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.clients[emailFrom(r, "/panel/api/clients/update/")]
	if !ok {
		notFound(w)
		return
	}
	var c client
	raw, _ := json.Marshal(b)
	if json.Unmarshal(raw, &c) != nil {
		reply(w, http.StatusOK, false, "bad client", nil)
		return
	}
	for _, k := range []string{"reverse", "allowedIPs"} { // structured on input
		if v, ok := b[k]; ok && len(v) > 0 && v[0] == '"' {
			reply(w, http.StatusOK, false, "json: cannot unmarshal string into Go struct field Client."+k, nil)
			return
		}
	}
	if c.ID != rec.ID {
		s.lost = append(s.lost, rec.Email+": id")
	}
	if c.SubID != rec.SubID {
		s.lost = append(s.lost, rec.Email+": subId")
	}
	if c.Group != rec.Group {
		s.lost = append(s.lost, rec.Email+": group")
	}
	c.Email = rec.Email
	rec.client = c
	reply(w, http.StatusOK, true, "Inbound client has been updated.", nil)
}

// LostFields lists identity fields an update replaced with something else.
func (s *Server) LostFields() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lost...)
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

// listPaged follows clients/list/paged: slim rows in the panel's row-id order
// (a client with no group has no "group" key), pages of 25 unless asked, 200 at
// most, and an empty page past the end.
func (s *Server) listPaged(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 25
	}
	size = min(size, 200)
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]*record, 0, len(s.clients))
	groups := map[string]bool{}
	for _, rec := range s.clients {
		if s.hidden[rec.Email] && page == 1 {
			delete(s.hidden, rec.Email)
			continue
		}
		rows = append(rows, rec)
		if rec.Group != "" {
			groups[rec.Group] = true
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].rowID < rows[j].rowID })
	start := min((page-1)*size, len(rows))
	end := min(start+size, len(rows))
	items := make([]map[string]any, 0, end-start)
	for _, rec := range rows[start:end] {
		ids := rec.inboundIDs
		if ids == nil {
			ids = []int{}
		}
		it := map[string]any{
			"email": rec.Email, "subId": rec.SubID, "enable": rec.Enable, "totalGB": rec.TotalGB,
			"expiryTime": rec.ExpiryTime, "limitIp": rec.LimitIP, "inboundIds": ids,
			"traffic": map[string]any{"email": rec.Email, "up": rec.up, "down": rec.down, "total": rec.TotalGB, "enable": rec.Enable},
		}
		if rec.Group != "" {
			it["group"] = rec.Group
		}
		items = append(items, it)
	}
	reply(w, http.StatusOK, true, "", map[string]any{
		"filtered": len(rows), "total": len(rows), "page": page, "pageSize": size, "items": items,
		"groups": sortedFold(groups), "summary": map[string]any{"total": len(rows)},
	})
}

// sortedFold returns the keys sorted case-insensitively, as the panel lists groups.
func sortedFold(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i]), strings.ToLower(out[j])
		if a != b {
			return a < b
		}
		return out[i] < out[j]
	})
	return out
}

// groups follows clients/groups: the groups clients are in, merged with the
// ones that stay while empty, with their client counts and traffic.
func (s *Server) groups(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type agg struct{ n, up, down int64 }
	by := map[string]*agg{}
	names := map[string]bool{}
	for name := range s.groupRows {
		by[name], names[name] = &agg{}, true
	}
	for _, rec := range s.clients {
		if rec.Group == "" {
			continue
		}
		a := by[rec.Group]
		if a == nil {
			a = &agg{}
			by[rec.Group], names[rec.Group] = a, true
		}
		a.n++
		a.up += rec.up
		a.down += rec.down
	}
	out := []map[string]any{}
	for _, name := range sortedFold(names) {
		a := by[name]
		out = append(out, map[string]any{"name": name, "clientCount": a.n, "trafficUsed": a.up + a.down, "up": a.up, "down": a.down})
	}
	reply(w, http.StatusOK, true, "", out)
}

// groupBulkAdd follows clients/groups/bulkAdd. A group name that no client
// carries and no placeholder holds becomes a placeholder (it stays in the list
// when it empties). Every listed client that exists and is not yet in the group
// is put in it, WHATEVER group it has now; "affected" counts those.
func (s *Server) groupBulkAdd(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Emails []string `json:"emails"`
		Group  string   `json:"group"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&b) != nil {
		reply(w, http.StatusOK, false, "bad request", nil)
		return
	}
	group := strings.TrimSpace(b.Group)
	if group == "" {
		reply(w, http.StatusOK, false, "Something went wrong (group name is required\n)", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groupBatches = append(s.groupBatches, len(b.Emails))
	if len(b.Emails) == 0 {
		reply(w, http.StatusOK, true, "", map[string]any{"affected": 0})
		return
	}
	if !s.groupRows[group] && len(s.members(group)) == 0 {
		s.groupRows[group] = true
	}
	affected := 0
	for _, e := range b.Emails {
		if rec, ok := s.clients[e]; ok && rec.Group != group && !s.ignoreAdd {
			rec.Group = group
			affected++
		}
	}
	reply(w, http.StatusOK, true, "", map[string]any{"affected": affected})
}

// groupBulkRemove follows clients/groups/bulkRemove: the listed clients leave
// their group.
func (s *Server) groupBulkRemove(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Emails []string `json:"emails"`
	}
	if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&b) != nil {
		reply(w, http.StatusOK, false, "bad request", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	affected := 0
	for _, e := range b.Emails {
		if rec, ok := s.clients[e]; ok && rec.Group != "" {
			rec.Group = ""
			affected++
		}
	}
	reply(w, http.StatusOK, true, "", map[string]any{"affected": affected})
}

// groupEmails follows clients/groups/{name}/emails.
func (s *Server) groupEmails(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/panel/api/clients/groups/"), "/emails")
	if !ok || name == "" {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reply(w, http.StatusOK, true, "", s.members(name))
}

func (s *Server) options(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply(w, http.StatusOK, true, "", s.inbounds)
}

// status is a trimmed server/status: the real one also reports cpu, memory,
// xray and more.
func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj := map[string]any{"cpu": 1.5, "xray": map[string]any{"state": "running", "version": "26.3.27"}}
	if s.version != "" {
		obj["panelVersion"] = s.version
	}
	reply(w, http.StatusOK, true, "", obj)
}
