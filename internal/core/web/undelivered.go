package web

import (
	"net/http"
)

// Undelivered orders (docs/04-dashboard.md, "Undelivered orders"): paid orders whose service could not be made or
// applied yet, the longest waiting first, and what staff may do with them. Retry and refund are the order's own
// (POST /orders/{id}/retry, /refund).

// undeliveredServer is a server the dashboard offers for "Deliver on another server": the ones that take new
// services, with their last health check.
type undeliveredServer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	Checked bool   `json:"checked"` // a health check has run (Healthy means nothing before)
	Error   string `json:"error"`   // the last check's error
	Full    bool   `json:"full"`    // at its client limit: it takes no new client
}

// listUndelivered: GET /api/v1/orders/undelivered?page=&size= is a page of the undelivered orders, the longest
// waiting first, with the servers a new service may be moved to (servers_error when the provisioner did not answer).
func (s *Server) listUndelivered(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	res, err := s.cfg.Domain.ListUndelivered(r.Context(), p)
	if err != nil {
		s.fail(w, "list undelivered orders", err)
		return
	}
	items := make([]orderJSON, 0, len(res.Orders))
	for i := range res.Orders {
		items = append(items, orderOf(&res.Orders[i]))
	}
	servers, serversErr := s.undeliveredServers(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "total": res.Total, "page": page, "size": size,
		"servers": servers, "servers_error": serversErr,
	})
}

// undeliveredServers are the servers that take new services, or why they could not be listed.
func (s *Server) undeliveredServers(r *http.Request) ([]undeliveredServer, string) {
	servers := []undeliveredServer{}
	if s.cfg.Servers == nil {
		return servers, "the provisioner is not configured"
	}
	list, err := s.cfg.Servers.ListServers(r.Context())
	if err != nil {
		s.cfg.Log.Warn("dashboard api: undelivered servers", "err", err)
		return servers, "the provisioner did not answer"
	}
	for _, sv := range list.Servers {
		if !sv.Enabled {
			continue
		}
		servers = append(servers, undeliveredServer{
			ID: sv.ID, Name: sv.Name, Healthy: sv.Healthy, Checked: sv.CheckedAt > 0, Error: sv.HealthError,
			Full: sv.MaxClients > 0 && sv.Clients >= int64(sv.MaxClients),
		})
	}
	return servers, ""
}

// retryUndelivered: POST /api/v1/orders/undelivered/retry tries every undelivered order again now.
func (s *Server) retryUndelivered(w http.ResponseWriter, r *http.Request) {
	n, err := s.cfg.Domain.RetryUndelivered(r.Context(), actor(r))
	if err != nil {
		s.fail(w, "retry undelivered orders", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

// moveBody is the server chosen for an undelivered order and the reason (for the audit log).
type moveBody struct {
	ServerID string `json:"server_id"`
	Reason   string `json:"reason"`
}

// moveOrder: POST /api/v1/orders/{id}/move {server_id, reason} delivers an undelivered new service on that server
// now, instead of the plan's servers. warning says a half-made client may be left on the old server's panel.
func (s *Server) moveOrder(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req moveBody
	if !readJSON(w, r, &req) {
		return
	}
	if s.cfg.Servers == nil || s.cfg.Provisioner == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the provisioner is not configured")
		return
	}
	res, err := s.cfg.Domain.MoveOrder(r.Context(), actor(r), s.cfg.Provisioner, s.cfg.Servers, id, req.ServerID, req.Reason)
	if err != nil {
		s.fail(w, "move order", err)
		return
	}
	o, err := s.order(r, id)
	if err != nil {
		s.fail(w, "reload order", err)
		return
	}
	out := map[string]any{"order": orderOf(o), "leftover": res.Leftover}
	if res.Leftover {
		out["warning"] = "moved; the old server's panel did not answer, so a half-made account may be left there: delete it there by hand"
	}
	writeJSON(w, http.StatusOK, out)
}
