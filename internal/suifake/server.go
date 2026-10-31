// Package suifake is an in-memory S-UI panel (API v2) for tests. It follows
// what S-UI v1.6.6 does where the adapter depends on it (api/apiV2Handler.go,
// service/client.go): a "Token" header or success=false "invalid token" inside
// HTTP 200; clients saved through POST apiv2/save (object=clients, action new,
// edit or del) with unique names; GET apiv2/clients listing them without their
// configuration and links and ?id=N giving one in full; an edit that keeps the
// stored traffic counters unless it sends both as zero, which resets them; and
// share links built from the request's host. The CI job "S-UI integration"
// checks the adapter against the real panel, which keeps this fake honest.
package suifake

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BasePath is the web path the fake serves under (S-UI's default).
const BasePath = "/app"

type client struct {
	ID         int             `json:"id"`
	Enable     bool            `json:"enable"`
	Name       string          `json:"name"`
	Config     json.RawMessage `json:"config,omitempty"`
	Inbounds   []int           `json:"inbounds"`
	Links      json.RawMessage `json:"links,omitempty"`
	Volume     int64           `json:"volume"`
	Expiry     int64           `json:"expiry"`
	Down       int64           `json:"down"`
	Up         int64           `json:"up"`
	Desc       string          `json:"desc"`
	Group      string          `json:"group"`
	Remark     string          `json:"remark"`
	CreatedAt  int64           `json:"createdAt"`
	OnlineAt   int64           `json:"onlineAt"`
	DelayStart bool            `json:"delayStart"`
	AutoReset  bool            `json:"autoReset"`
	ResetDays  int             `json:"resetDays"`
	NextReset  int64           `json:"nextReset"`
	TotalUp    int64           `json:"totalUp"`
	TotalDown  int64           `json:"totalDown"`
}

// Inbound is one inbound of the fake.
type Inbound struct {
	ID    int
	Type  string
	Tag   string
	Port  int
	Users bool // takes clients
}

// Server is the fake panel.
type Server struct {
	*httptest.Server
	URL string // the panel address with BasePath, as an operator gives it

	mu       sync.Mutex
	token    string
	version  string
	clients  map[int]*client
	nextID   int
	inbounds []Inbound
	settings map[string]string
	calls    map[string]int
	failNext int
	now      func() time.Time
}

// NewServer starts a fake that takes token.
func NewServer(token string) *Server {
	s := &Server{
		token: token, version: "1.6.6", clients: map[int]*client{}, nextID: 1, calls: map[string]int{}, now: time.Now,
		inbounds: []Inbound{
			{ID: 1, Type: "vless", Tag: "vless-in", Port: 2443, Users: true},
			{ID: 2, Type: "trojan", Tag: "trojan-in", Port: 2444, Users: true},
			{ID: 3, Type: "direct", Tag: "direct-in", Port: 2445},
		},
		settings: map[string]string{"subPort": "2096", "subPath": "/sub/", "subURI": "", "subDomain": "", "subKeyFile": "", "subCertFile": ""},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(BasePath+"/apiv2/", s.handle)
	s.Server = httptest.NewServer(mux)
	s.URL = s.Server.URL + BasePath
	return s
}

// SetVersion sets what the panel reports.
func (s *Server) SetVersion(v string) { s.mu.Lock(); s.version = v; s.mu.Unlock() }

// SetSetting sets one panel setting.
func (s *Server) SetSetting(k, v string) { s.mu.Lock(); s.settings[k] = v; s.mu.Unlock() }

// SetFailNext makes the next n API calls answer HTTP 500.
func (s *Server) SetFailNext(n int) { s.mu.Lock(); s.failNext = n; s.mu.Unlock() }

// SetClock replaces the fake's clock.
func (s *Server) SetClock(now func() time.Time) { s.mu.Lock(); s.now = now; s.mu.Unlock() }

// Calls counts the calls of one action ("clients", "save:edit"...).
func (s *Server) Calls(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

func (s *Server) byName(name string) *client {
	for _, c := range s.clients {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Has reports whether a client of that name exists.
func (s *Server) Has(name string) bool { s.mu.Lock(); defer s.mu.Unlock(); return s.byName(name) != nil }

// Snapshot is a client as the fake holds it.
type Snapshot struct {
	ID                  int
	Enable              bool
	Volume, Expiry      int64
	Up, Down            int64
	TotalUp, TotalDown  int64
	Desc, Group         string
	Inbounds            []int
	DelayStart          bool
	ResetDays           int
	HasConfig, HasLinks bool
}

// Snapshot returns a client by name.
func (s *Server) Snapshot(name string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.byName(name)
	if c == nil {
		return Snapshot{}, false
	}
	return Snapshot{ID: c.ID, Enable: c.Enable, Volume: c.Volume, Expiry: c.Expiry, Up: c.Up, Down: c.Down, TotalUp: c.TotalUp,
		TotalDown: c.TotalDown, Desc: c.Desc, Group: c.Group, Inbounds: slices.Clone(c.Inbounds), DelayStart: c.DelayStart,
		ResetDays: c.ResetDays, HasConfig: len(c.Config) > 2, HasLinks: len(c.Links) > 2}, true
}

// AddUsage counts traffic for a client (down).
func (s *Server) AddUsage(name string, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.byName(name); c != nil {
		c.Down += bytes
	}
}

// Seed adds a client as an operator would (no BOBRES description unless desc).
func (s *Server) Seed(name, desc, group string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[s.nextID] = &client{ID: s.nextID, Enable: true, Name: name, Desc: desc, Group: group, Inbounds: []int{1},
		Config: json.RawMessage(`{}`), Links: json.RawMessage(`[]`)}
	s.nextID++
}

// Remove deletes a client behind BOBRES's back, as an operator does.
func (s *Server) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.byName(name); c != nil {
		delete(s.clients, c.ID)
	}
}

// SetGroup changes a client's group as an operator does.
func (s *Server) SetGroup(name, group string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.byName(name); c != nil {
		c.Group = group
	}
}

// Review runs the panel's depletion job: a client over its quota or past its
// expiry is switched off.
func (s *Server) Review() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().Unix()
	for _, c := range s.clients {
		if c.Enable && ((c.Volume > 0 && c.Up+c.Down > c.Volume) || (c.Expiry > 0 && c.Expiry < now)) {
			c.Enable = false
		}
	}
}

func reply(w http.ResponseWriter, msg string, obj any, err error) {
	m := map[string]any{"success": err == nil, "msg": msg, "obj": obj}
	if err != nil {
		m["msg"] = msg + ": " + err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, BasePath+"/apiv2/")
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Token") != s.token {
		reply(w, "", nil, fmt.Errorf("invalid token"))
		return
	}
	if s.failNext > 0 {
		s.failNext--
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	switch {
	case r.Method == http.MethodGet && action == "clients":
		s.calls["clients"]++
		s.listClients(w, r)
	case r.Method == http.MethodGet && action == "inbounds":
		s.calls["inbounds"]++
		s.listInbounds(w)
	case r.Method == http.MethodGet && action == "settings":
		reply(w, "", s.settings, nil)
	case r.Method == http.MethodGet && action == "status":
		reply(w, "", map[string]any{"sys": map[string]any{"appVersion": s.version}}, nil)
	case r.Method == http.MethodPost && action == "save":
		s.save(w, r)
	default:
		reply(w, "failed", nil, fmt.Errorf("unknown action: %s", action))
	}
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query().Get("id")
	out := []any{}
	if ids == "" {
		keys := make([]int, 0, len(s.clients))
		for id := range s.clients {
			keys = append(keys, id)
		}
		slices.Sort(keys)
		for _, id := range keys {
			c := *s.clients[id]
			c.Config, c.Links = nil, nil // the listing has neither
			out = append(out, map[string]any{"id": c.ID, "enable": c.Enable, "name": c.Name, "desc": c.Desc, "group": c.Group,
				"remark": c.Remark, "inbounds": c.Inbounds, "up": c.Up, "down": c.Down, "volume": c.Volume, "expiry": c.Expiry,
				"createdAt": c.CreatedAt, "onlineAt": c.OnlineAt})
		}
	} else {
		for _, p := range strings.Split(ids, ",") {
			id, _ := strconv.Atoi(p)
			if c, ok := s.clients[id]; ok {
				cp := *c
				out = append(out, cp)
			}
		}
	}
	reply(w, "", map[string]any{"clients": out}, nil)
}

func (s *Server) listInbounds(w http.ResponseWriter) {
	out := []any{}
	for _, in := range s.inbounds {
		m := map[string]any{"id": in.ID, "type": in.Type, "tag": in.Tag, "tls_id": 0, "listen": "::", "listen_port": in.Port}
		if in.Users {
			users := []string{}
			for _, c := range s.clients {
				if slices.Contains(c.Inbounds, in.ID) {
					users = append(users, c.Name)
				}
			}
			m["users"] = users
		}
		out = append(out, m)
	}
	reply(w, "", map[string]any{"inbounds": out}, nil)
}

// links builds the client's local share links on its inbounds, from the host
// the request came to (as the panel does).
func (s *Server) links(c *client, host string) json.RawMessage {
	var cfg map[string]map[string]any
	_ = json.Unmarshal(c.Config, &cfg)
	ls := []map[string]string{}
	for _, in := range s.inbounds {
		if !in.Users || !slices.Contains(c.Inbounds, in.ID) {
			continue
		}
		var uri string
		switch in.Type {
		case "vless":
			uri = fmt.Sprintf("vless://%v@%s#%s", cfg["vless"]["uuid"], net.JoinHostPort(host, strconv.Itoa(in.Port)), in.Tag)
		case "trojan":
			uri = fmt.Sprintf("trojan://%v@%s#%s", cfg["trojan"]["password"], net.JoinHostPort(host, strconv.Itoa(in.Port)), in.Tag)
		}
		ls = append(ls, map[string]string{"remark": in.Tag, "type": "local", "uri": uri})
	}
	b, _ := json.Marshal(ls)
	return b
}

func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		reply(w, "save", nil, err)
		return
	}
	obj, act, data := r.PostFormValue("object"), r.PostFormValue("action"), r.PostFormValue("data")
	s.calls["save:"+act]++
	if obj != "clients" {
		reply(w, "save", nil, fmt.Errorf("unknown object: %s", obj))
		return
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	switch act {
	case "new", "edit":
		var c client
		if err := json.Unmarshal([]byte(data), &c); err != nil {
			reply(w, "save", nil, err)
			return
		}
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			reply(w, "save", nil, fmt.Errorf("client name must not be empty"))
			return
		}
		if other := s.byName(c.Name); other != nil && (act == "new" || other.ID != c.ID) {
			reply(w, "save", nil, fmt.Errorf("client name %q is already in use", c.Name))
			return
		}
		var probe []map[string]string
		if err := json.Unmarshal(c.Links, &probe); err != nil {
			reply(w, "save", nil, err) // the panel parses the links it is sent
			return
		}
		if act == "edit" {
			old, ok := s.clients[c.ID]
			if !ok {
				reply(w, "save", nil, fmt.Errorf("record not found"))
				return
			}
			c.CreatedAt, c.OnlineAt = old.CreatedAt, old.OnlineAt
			if c.Up == 0 && c.Down == 0 {
				c.TotalUp, c.TotalDown = old.TotalUp+old.Up, old.TotalDown+old.Down
			} else {
				c.Up, c.Down, c.TotalUp, c.TotalDown = old.Up, old.Down, old.TotalUp, old.TotalDown
			}
		} else {
			c.ID = s.nextID
			s.nextID++
			c.CreatedAt = s.now().Unix()
		}
		c.Links = s.links(&c, host)
		cp := c
		s.clients[c.ID] = &cp
		reply(w, "", map[string]any{"clients": []any{cp}}, nil)
	case "del":
		var id int
		if err := json.Unmarshal([]byte(data), &id); err != nil {
			reply(w, "save", nil, err)
			return
		}
		if _, ok := s.clients[id]; !ok {
			reply(w, "save", nil, fmt.Errorf("record not found"))
			return
		}
		delete(s.clients, id)
		reply(w, "", map[string]any{"clients": []any{}}, nil)
	default:
		reply(w, "save", nil, fmt.Errorf("unknown action: %s", act))
	}
}
