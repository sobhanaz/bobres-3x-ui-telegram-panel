package web

import (
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

type planRef struct {
	ID   string            `json:"id"`
	Name map[string]string `json:"name"`
}

type serviceJSON struct {
	ID           string     `json:"id"`
	User         *userBrief `json:"user"`
	Plan         planRef    `json:"plan"`
	Status       string     `json:"status"`
	ClientEmail  string     `json:"client_email"`
	SubLink      string     `json:"sub_link"`
	ExpiresAt    *int64     `json:"expires_at"`
	TrafficTotal *int64     `json:"traffic_total"`
	TrafficUsed  int64      `json:"traffic_used"`
	LastSyncedAt *int64     `json:"last_synced_at"`
	CreatedAt    int64      `json:"created_at"`
}

func serviceOf(r *store.SubscriptionRow) serviceJSON {
	return serviceJSON{
		ID: r.ID, User: briefOf(r.UserID, r.TelegramID, r.Username), Plan: planRef{r.PlanID, r.PlanName},
		Status: r.Status, ClientEmail: r.ClientEmail, SubLink: r.SubLink, ExpiresAt: unix(r.ExpiresAt),
		TrafficTotal: r.TrafficTotal, TrafficUsed: r.TrafficUsed, LastSyncedAt: unix(r.LastSyncedAt), CreatedAt: r.CreatedAt.Unix(),
	}
}

// listServices: GET /api/v1/services?q=&status=&user=&page=&size=
func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	userID, ok := queryID(r, "user")
	if !ok {
		writeJSON(w, http.StatusOK, list{[]serviceJSON{}, 0, page, size})
		return
	}
	f := store.SubscriptionFilter{Query: r.URL.Query().Get("q"), Status: r.URL.Query().Get("status"), UserID: userID}
	rows, total, err := s.cfg.Store.ListAllSubscriptions(r.Context(), s.cfg.Store.Conn(), f, p)
	if err != nil {
		s.fail(w, "list services", err)
		return
	}
	items := make([]serviceJSON, 0, len(rows))
	for i := range rows {
		items = append(items, serviceOf(&rows[i]))
	}
	writeJSON(w, http.StatusOK, list{items, total, page, size})
}

// service loads one service row.
func (s *Server) service(r *http.Request, id string) (*store.SubscriptionRow, error) {
	rows, _, err := s.cfg.Store.ListAllSubscriptions(r.Context(), s.cfg.Store.Conn(), store.SubscriptionFilter{ID: id}, store.Page{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, store.ErrNotFound
	}
	return &rows[0], nil
}

// getService: GET /api/v1/services/{id}, with the orders that made or
// changed it.
func (s *Server) getService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	row, err := s.service(r, id)
	if err != nil {
		s.fail(w, "get service", err)
		return
	}
	orders, _, err := s.cfg.Store.ListOrders(r.Context(), s.cfg.Store.Conn(), store.OrderFilter{SubscriptionID: id}, store.Page{Limit: 100})
	if err != nil {
		s.fail(w, "get service orders", err)
		return
	}
	items := make([]orderJSON, 0, len(orders))
	for i := range orders {
		items = append(items, orderOf(&orders[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"service": serviceOf(row), "orders": items})
}

// serviceResult answers a service operation with the service as it is now.
func (s *Server) serviceResult(w http.ResponseWriter, r *http.Request, id string, extra map[string]any) {
	row, err := s.service(r, id)
	if err != nil {
		s.fail(w, "reload service", err)
		return
	}
	out := map[string]any{"service": serviceOf(row)}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// extendService: POST /api/v1/services/{id}/extend {days, traffic_gb, reason, key}
func (s *Server) extendService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Days      int32  `json:"days"`
		TrafficGB string `json:"traffic_gb"`
		Reason    string `json:"reason"`
		Key       string `json:"key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	bytes, ok := gbToBytes(req.TrafficGB)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "traffic must be a number of GB")
		return
	}
	if !idemKeyRe.MatchString(req.Key) {
		writeError(w, http.StatusBadRequest, "invalid", "missing request key")
		return
	}
	o, err := s.cfg.Domain.ExtendSubscription(r.Context(), actor(r), domain.ExtendParams{
		SubscriptionID: id, Days: req.Days, Bytes: bytes, Reason: req.Reason, IdempotencyKey: req.Key,
	})
	if err != nil {
		s.fail(w, "extend service", err)
		return
	}
	s.serviceResult(w, r, id, map[string]any{"order_id": o.ID})
}

// syncService: POST /api/v1/services/{id}/sync reads the panel now.
func (s *Server) syncService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.SyncSubscription(r.Context(), s.cfg.Provisioner, id); err != nil {
		s.fail(w, "sync service", err)
		return
	}
	s.serviceResult(w, r, id, nil)
}

type reasonBody struct {
	Reason string `json:"reason"`
}

// resetService: POST /api/v1/services/{id}/reset-traffic {reason}
func (s *Server) resetService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req reasonBody
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.cfg.Domain.ResetSubscriptionTraffic(r.Context(), actor(r), s.cfg.Provisioner, id, req.Reason); err != nil {
		s.fail(w, "reset service traffic", err)
		return
	}
	s.serviceResult(w, r, id, nil)
}

// setServiceEnabled: POST /api/v1/services/{id}/enabled {enabled, reason}
func (s *Server) setServiceEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if _, err := s.cfg.Domain.SetSubscriptionEnabled(r.Context(), actor(r), s.cfg.Provisioner, id, req.Enabled, req.Reason); err != nil {
		s.fail(w, "set service enabled", err)
		return
	}
	var extra map[string]any
	if req.Enabled { // the right status (expired, used up...) comes from the panel
		if err := s.cfg.Domain.SyncSubscription(r.Context(), s.cfg.Provisioner, id); err != nil {
			extra = map[string]any{"warning": "turned on; reading its usage from the panel failed, the next sync will"}
		}
	}
	s.serviceResult(w, r, id, extra)
}

// deleteService: POST /api/v1/services/{id}/delete {reason}
func (s *Server) deleteService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req reasonBody
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.cfg.Domain.DeleteSubscription(r.Context(), actor(r), s.cfg.Provisioner, id, req.Reason); err != nil {
		s.fail(w, "delete service", err)
		return
	}
	s.serviceResult(w, r, id, nil)
}
