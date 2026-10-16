package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// 3x-ui servers (milestone 6a): owner and admin. The provisioner keeps the
// panels and their API tokens (encrypted under its own key); a token goes in
// here and never comes back, and the audit says only "changed".

// serverTimeout bounds a call that may test a panel's connection (the
// provisioner gives a panel 20 seconds).
const serverTimeout = 30 * time.Second

type serverJSON struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	BaseURL         string    `json:"base_url"`
	SubBaseURL      string    `json:"sub_base_url"`
	Enabled         bool      `json:"enabled"`
	AllowPrivate    bool      `json:"allow_private"`
	Weight          int32     `json:"weight"`
	Managed         string    `json:"managed"`
	PanelVersion    string    `json:"panel_version"`
	VersionUntested bool      `json:"version_untested"`
	Healthy         bool      `json:"healthy"`
	HealthError     string    `json:"health_error"`
	CheckedAt       *int64    `json:"checked_at"` // null before the first check
	Clients         int64     `json:"clients"`
	HasToken        bool      `json:"has_token"`
	Plans           []planRef `json:"plans"`
}

func serverOf(sv *domain.Server, plans []planRef) serverJSON {
	out := serverJSON{ID: sv.ID, Name: sv.Name, BaseURL: sv.BaseURL, SubBaseURL: sv.SubBaseURL, Enabled: sv.Enabled,
		AllowPrivate: sv.AllowPrivate, Weight: sv.Weight, Managed: sv.Managed, PanelVersion: sv.PanelVersion,
		VersionUntested: sv.VersionUntested, Healthy: sv.Healthy, HealthError: sv.HealthError, Clients: sv.Clients,
		HasToken: sv.HasToken, Plans: plans}
	if sv.CheckedAt > 0 {
		out.CheckedAt = &sv.CheckedAt
	}
	if out.Plans == nil {
		out.Plans = []planRef{}
	}
	return out
}

// servers answers 503 while core has no provisioner to ask.
func (s *Server) servers(w http.ResponseWriter) (domain.ServerAdmin, bool) {
	if s.cfg.Servers == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the provisioner is not connected")
		return nil, false
	}
	return s.cfg.Servers, true
}

// plansByServer names the plans that send new services to each server.
func (s *Server) plansByServer(ctx context.Context) map[string][]planRef {
	out := map[string][]planRef{}
	plans, err := s.cfg.Store.ListPlans(ctx, s.cfg.Store.Conn(), true)
	if err != nil {
		s.cfg.Log.Warn("servers: plans", "err", err)
		return out
	}
	for _, p := range plans {
		for _, id := range p.ServerIDs {
			out[id] = append(out[id], planRef{ID: p.ID, Name: p.NameI18n})
		}
	}
	return out
}

// listServers: GET /api/v1/servers, every panel with its health and plans.
func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	sa, ok := s.servers(w)
	if !ok {
		return
	}
	list, err := sa.ListServers(r.Context())
	if err != nil {
		s.serverFail(w, "list servers", err)
		return
	}
	plans := s.plansByServer(r.Context())
	items := make([]serverJSON, 0, len(list.Servers))
	for i := range list.Servers {
		items = append(items, serverOf(&list.Servers[i], plans[list.Servers[i].ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items,
		"tested_min_version": list.TestedMin, "tested_max_version": list.TestedMax})
}

// serverBody is a server as the dashboard edits it; token "" keeps the
// stored one.
type serverBody struct {
	Name         string `json:"name"`
	BaseURL      string `json:"base_url"`
	SubBaseURL   string `json:"sub_base_url"`
	Token        string `json:"token"`
	Weight       int32  `json:"weight"`
	Enabled      bool   `json:"enabled"`
	AllowPrivate bool   `json:"allow_private"`
	SkipCheck    bool   `json:"skip_check"`
}

func (b *serverBody) input() domain.ServerInput {
	return domain.ServerInput{Name: b.Name, BaseURL: b.BaseURL, SubBaseURL: b.SubBaseURL, Token: b.Token,
		Weight: b.Weight, Enabled: b.Enabled, AllowPrivate: b.AllowPrivate, SkipCheck: b.SkipCheck}
}

// addServer: POST /api/v1/servers
func (s *Server) addServer(w http.ResponseWriter, r *http.Request) {
	sa, ok := s.servers(w)
	if !ok {
		return
	}
	var b serverBody
	if !readJSON(w, r, &b) {
		return
	}
	if s.subBaseCollides(r, b.SubBaseURL) {
		writeFieldError(w, "sub_base_url", "this is BOBRES's own address: its /sub/ path shows the customers' subscription pages, so the panel's links cannot be served there; use the panel's own address and port")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), serverTimeout)
	defer cancel()
	sv, err := s.cfg.Domain.AddServer(ctx, actor(r), sa, b.input())
	if err != nil {
		s.serverFail(w, "add server", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"item": serverOf(sv, nil)})
}

// updateServer: PUT /api/v1/servers/{id}, every setting at once.
func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	sa, ok := s.servers(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var b serverBody
	if !readJSON(w, r, &b) {
		return
	}
	if s.subBaseCollides(r, b.SubBaseURL) {
		writeFieldError(w, "sub_base_url", "this is BOBRES's own address: its /sub/ path shows the customers' subscription pages, so the panel's links cannot be served there; use the panel's own address and port")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), serverTimeout)
	defer cancel()
	sv, err := s.cfg.Domain.UpdateServer(ctx, actor(r), sa, id, b.input())
	if err != nil {
		s.serverFail(w, "update server", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": serverOf(sv, s.plansByServer(r.Context())[sv.ID])})
}

// deleteServer: DELETE /api/v1/servers/{id}; refused (409) while services
// are on it.
func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	sa, ok := s.servers(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.DeleteServer(r.Context(), actor(r), sa, id); err != nil {
		s.serverFail(w, "delete server", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testServer: POST /api/v1/servers/test {id?, base_url, token, allow_private}
// tries a connection and saves nothing. A panel that does not answer is
// 200 {ok:false, detail}.
func (s *Server) testServer(w http.ResponseWriter, r *http.Request) {
	sa, ok := s.servers(w)
	if !ok {
		return
	}
	var b struct {
		ID           string `json:"id"`
		BaseURL      string `json:"base_url"`
		Token        string `json:"token"`
		AllowPrivate bool   `json:"allow_private"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if b.ID = strings.ToLower(strings.TrimSpace(b.ID)); b.ID != "" && !uuidRe.MatchString(b.ID) {
		writeError(w, http.StatusNotFound, "not_found", "no such server")
		return
	}
	if !s.probes.allow("server-test:"+actor(r).ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 10 connection tests a minute")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), serverTimeout)
	defer cancel()
	res, err := s.cfg.Domain.TestServer(ctx, actor(r), sa,
		domain.ServerProbe{ID: b.ID, BaseURL: b.BaseURL, Token: b.Token, AllowPrivate: b.AllowPrivate})
	if err != nil {
		s.serverFail(w, "test server", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.OK, "panel_version": res.PanelVersion,
		"inbounds": res.Inbounds, "detail": res.Detail})
}

type inboundJSON struct {
	ID       int32  `json:"id"`
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
	Enabled  bool   `json:"enabled"`
	Attach   bool   `json:"attach"`
}

// listInbounds: GET /api/v1/servers/{id}/inbounds, read from the panel.
func (s *Server) listInbounds(w http.ResponseWriter, r *http.Request) {
	sa, ok := s.servers(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), serverTimeout)
	defer cancel()
	list, err := sa.ListInbounds(ctx, id)
	if err != nil {
		s.serverFail(w, "list inbounds", err)
		return
	}
	items := make([]inboundJSON, 0, len(list))
	for _, in := range list {
		items = append(items, inboundJSON{ID: in.ID, Tag: in.Tag, Protocol: in.Protocol, Port: in.Port,
			Enabled: in.Enabled, Attach: in.Attach})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// setInboundAttach: PUT /api/v1/servers/{id}/inbounds/{inbound} {attach};
// new services only.
func (s *Server) setInboundAttach(w http.ResponseWriter, r *http.Request) {
	sa, ok := s.servers(w)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	inbound, err := strconv.ParseInt(r.PathValue("inbound"), 10, 32)
	if err != nil || inbound <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "no such inbound")
		return
	}
	var b struct {
		Attach *bool `json:"attach"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if b.Attach == nil {
		writeFieldError(w, "attach", "true or false")
		return
	}
	if err := s.cfg.Domain.SetInboundAttach(r.Context(), actor(r), sa, id, int32(inbound), *b.Attach); err != nil {
		s.serverFail(w, "set inbound attach", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// serverFail answers the provisioner's refusals with their reason (it never
// puts a token in one); the rest goes to fail.
func (s *Server) serverFail(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, domain.ErrPrivateOwnerOnly) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden", "field": "allow_private",
			"message": reason(err, domain.ErrForbidden)})
		return
	}
	st, isStatus := status.FromError(unwrapAll(err))
	switch {
	case !isStatus:
	case st.Code() == codes.NotFound:
		writeError(w, http.StatusNotFound, "not_found", "no such server")
		return
	case st.Code() == codes.AlreadyExists:
		writeFieldError(w, "name", "another server already has this name")
		return
	case st.Code() == codes.InvalidArgument:
		out := map[string]any{"error": "invalid", "message": st.Message()}
		if f := serverField(st.Message()); f != "" {
			out["field"] = f
		}
		writeJSON(w, http.StatusBadRequest, out)
		return
	}
	s.fail(w, what, err)
}

// serverField is the form field a provisioner refusal is about, "" when it
// names none.
func serverField(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "token"):
		return "token"
	case strings.Contains(m, "sub_base_url"):
		return "sub_base_url"
	case strings.Contains(m, "weight"):
		return "weight"
	case strings.HasPrefix(m, "name"):
		return "name"
	case strings.Contains(m, "url") || strings.Contains(m, "address") || strings.Contains(m, "http"):
		return "base_url"
	}
	return ""
}
