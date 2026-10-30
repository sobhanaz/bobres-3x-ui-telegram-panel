// Package hiddifyfake is an in-memory Hiddify Manager panel (API v2) for tests.
// It follows what hiddify-panel v14.0.5 does where the adapter depends on it
// (hiddifypanel/panel/commercial/restapi/v2): the admin API under the admin
// proxy path with the admin's UUID in Hiddify-API-Key (401 otherwise); users
// created with POST user/ (400 "The user exists" for a UUID a live user has; a
// deleted one is taken over), read, patched and soft-deleted under user/<uuid>/
// (404 "User not found" for a missing or deleted one); GET user/ answering 404
// "You have no user" for none; usage and quota in GiB; the users page giving the
// client proxy path; and a user's configurations under the client proxy path
// with the user's UUID as the key. The CI job "Hiddify integration" checks the
// adapter against the real panel.
package hiddifyfake

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
)

// Paths of the fake panel.
const (
	AdminPath  = "/AdminPath1"
	ClientPath = "/ClientPath1"
)

const oneGiB = 1 << 30

type user struct {
	UUID        string
	Name        string
	Comment     string
	LimitBytes  int64
	UsedBytes   int64
	PackageDays int64
	StartDate   *string
	Mode        string
	Enable      bool
	Deleted     bool
}

// Server is the fake panel.
type Server struct {
	*httptest.Server
	URL string // the panel address with the admin proxy path, as an operator gives it

	mu      sync.Mutex
	key     string
	version string
	users   []*user // in creation order
	calls   map[string]int
}

// NewServer starts a fake whose admin has the UUID key.
func NewServer(key string) *Server {
	s := &Server{key: strings.ToLower(key), version: "14.0.5", calls: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	s.URL = s.Server.URL + AdminPath
	return s
}

// SetVersion sets what the panel reports.
func (s *Server) SetVersion(v string) { s.mu.Lock(); s.version = v; s.mu.Unlock() }

// Calls counts calls by "METHOD path-kind" (such as "GET users", "PATCH user").
func (s *Server) Calls(name string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls[name] }

func (s *Server) live(name string) *user {
	for _, u := range s.users {
		if !u.Deleted && u.Name == name {
			return u
		}
	}
	return nil
}

func (s *Server) byUUID(id string, withDeleted bool) *user {
	for _, u := range s.users {
		if u.UUID == strings.ToLower(id) && (withDeleted || !u.Deleted) {
			return u
		}
	}
	return nil
}

// Has reports whether a live user of that name exists.
func (s *Server) Has(name string) bool { s.mu.Lock(); defer s.mu.Unlock(); return s.live(name) != nil }

// Snapshot is a user as the fake holds it.
type Snapshot struct {
	UUID, Comment, Mode   string
	LimitBytes, UsedBytes int64
	PackageDays           int64
	StartDate             string // "" for none
	Enable                bool
}

// Snapshot returns a live user by name.
func (s *Server) Snapshot(name string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.live(name)
	if u == nil {
		return Snapshot{}, false
	}
	sn := Snapshot{UUID: u.UUID, Comment: u.Comment, Mode: u.Mode, LimitBytes: u.LimitBytes, UsedBytes: u.UsedBytes,
		PackageDays: u.PackageDays, Enable: u.Enable}
	if u.StartDate != nil {
		sn.StartDate = *u.StartDate
	}
	return sn, true
}

// AddUsage counts traffic for a user.
func (s *Server) AddUsage(name string, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.live(name); u != nil {
		u.UsedBytes += bytes
	}
}

// Seed adds a user as an operator would.
func (s *Server) Seed(uuid, name, comment string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users = append(s.users, &user{UUID: strings.ToLower(uuid), Name: name, Comment: comment, LimitBytes: 100 * oneGiB, PackageDays: 30, Enable: true, Mode: "no_reset"})
}

// Remove deletes a user behind BOBRES's back, as an operator does (the panel
// keeps it marked deleted).
func (s *Server) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.live(name); u != nil {
		u.Deleted = true
	}
}

func abort(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{}, "message": msg})
}

func ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (u *user) schema() map[string]any {
	var start any
	if u.StartDate != nil {
		start = *u.StartDate
	}
	return map[string]any{
		"uuid": u.UUID, "name": u.Name, "comment": u.Comment, "usage_limit_GB": float64(u.LimitBytes) / oneGiB,
		"current_usage_GB": float64(u.UsedBytes) / oneGiB, "package_days": u.PackageDays, "start_date": start,
		"mode": u.Mode, "enable": u.Enable, "deleted": u.Deleted, "is_active": u.Enable && u.UsedBytes <= u.LimitBytes,
		"ed25519_private_key": "secret-key-material", "lang": "en",
	}
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(r.Header.Get("Hiddify-API-Key"))
	switch {
	case strings.HasPrefix(r.URL.Path, AdminPath+"/api/v2/"):
		if key != s.key {
			abort(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		s.admin(w, r, strings.TrimPrefix(r.URL.Path, AdminPath+"/api/v2/"))
	case r.URL.Path == ClientPath+"/api/v2/user/all-configs/":
		u := s.byUUID(key, false)
		if u == nil {
			abort(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		s.calls["GET all-configs"]++
		base := fmt.Sprintf("https://%s%s/%s/", strings.Split(r.Host, ":")[0], ClientPath, u.UUID)
		ok(w, []map[string]any{
			{"name": "Auto", "domain": "ALL", "link": base + "auto/?asn=unknown#" + u.Name, "category": "subscription"},
			{"name": "Subscription link", "domain": "ALL", "link": base + "sub/?asn=unknown#" + u.Name, "category": "subscription"},
			{"name": "vless ws", "domain": "direct", "protocol": "vless", "link": "vless://" + u.UUID + "@panel.example.com:443?type=ws#" + u.Name, "category": "config"},
			{"name": "trojan grpc", "domain": "direct", "protocol": "trojan", "link": "trojan://" + u.UUID + "@panel.example.com:443?type=grpc#" + u.Name, "category": "config"},
		})
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request, p string) {
	switch {
	case p == "panel/info/" && r.Method == http.MethodGet:
		ok(w, map[string]any{"version": s.version})
	case p == "admin/me/" && r.Method == http.MethodGet:
		ok(w, map[string]any{"uuid": s.key, "name": "owner", "mode": "super_admin", "can_add_admin": true, "lang": "en"})
	case p == "admin/users/" && r.Method == http.MethodGet:
		s.calls["GET users"]++
		rows := []any{}
		for _, u := range s.users {
			if !u.Deleted {
				rows = append(rows, map[string]any{"uuid": u.UUID, "name": u.Name})
			}
		}
		host := r.Host
		ok(w, map[string]any{"users": rows, "me_uuid": s.key, "link_domains": []any{
			map[string]any{"domain": host, "label": host, "kind": "current", "base": "https://" + host + ClientPath + "/"},
		}})
	case p == "admin/user/" && r.Method == http.MethodGet:
		s.calls["GET user list"]++
		out := []any{}
		for _, u := range s.users {
			if !u.Deleted {
				out = append(out, u.schema())
			}
		}
		if len(out) == 0 {
			abort(w, http.StatusNotFound, "You have no user")
			return
		}
		ok(w, out)
	case p == "admin/user/" && r.Method == http.MethodPost:
		s.calls["POST user"]++
		s.create(w, r)
	case strings.HasPrefix(p, "admin/user/") && strings.HasSuffix(p, "/"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "admin/user/"), "/")
		u := s.byUUID(id, false)
		if u == nil {
			abort(w, http.StatusNotFound, "User not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			s.calls["GET user"]++
			ok(w, u.schema())
		case http.MethodPatch:
			s.calls["PATCH user"]++
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				abort(w, http.StatusUnprocessableEntity, "Validation error")
				return
			}
			if err := apply(u, body); err != nil {
				abort(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
			ok(w, u.schema())
		case http.MethodDelete:
			s.calls["DELETE user"]++
			u.Deleted = true
			ok(w, map[string]any{"status": 200, "msg": "ok"})
		default:
			abort(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	default:
		abort(w, http.StatusNotFound, "Not found")
	}
}

// apply sets the fields a body carries, as the panel's upsert does.
func apply(u *user, body map[string]json.RawMessage) error {
	for k, raw := range body {
		switch k {
		case "name":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil || v == "" {
				return fmt.Errorf("invalid name")
			}
			u.Name = v
		case "comment":
			var v string
			_ = json.Unmarshal(raw, &v)
			if v != "" { // the panel keeps the old comment for an empty one
				u.Comment = v
			}
		case "usage_limit_GB":
			var v float64
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("invalid usage_limit_GB")
			}
			u.LimitBytes = int64(min(v, 1_000_000) * oneGiB)
		case "current_usage_GB":
			var v float64
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("invalid current_usage_GB")
			}
			u.UsedBytes = int64(v * oneGiB)
		case "package_days":
			var v int64
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("invalid package_days")
			}
			u.PackageDays = v
		case "start_date":
			if string(raw) == "null" {
				u.StartDate = nil
				continue
			}
			var v string
			if err := json.Unmarshal(raw, &v); err != nil || len(v) != 10 {
				return fmt.Errorf("invalid start_date")
			}
			u.StartDate = &v
		case "enable":
			var v bool
			if err := json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("invalid enable")
			}
			u.Enable = v
		case "mode":
			var v string
			_ = json.Unmarshal(raw, &v)
			if !slices.Contains([]string{"no_reset", "monthly", "weekly", "daily"}, v) {
				return fmt.Errorf("invalid mode")
			}
			u.Mode = v
		}
	}
	return nil
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		abort(w, http.StatusUnprocessableEntity, "Validation error")
		return
	}
	var id string
	_ = json.Unmarshal(body["uuid"], &id)
	id = strings.ToLower(id)
	if len(id) != 36 {
		abort(w, http.StatusUnprocessableEntity, "Validation error")
		return
	}
	if old := s.byUUID(id, true); old != nil {
		if !old.Deleted {
			abort(w, http.StatusBadRequest, "The user exists")
			return
		}
		s.users = slices.DeleteFunc(s.users, func(u *user) bool { return u == old }) // purged, the UUID taken over
	}
	u := &user{UUID: id, LimitBytes: 1000 * oneGiB, PackageDays: 90, Mode: "no_reset", Enable: true}
	if err := apply(u, body); err != nil || u.Name == "" {
		abort(w, http.StatusUnprocessableEntity, "Validation error")
		return
	}
	s.users = append(s.users, u)
	ok(w, u.schema())
}
