package web

import (
	"net/http"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

type planJSON struct {
	ID           string            `json:"id"`
	Name         map[string]string `json:"name"`
	Kind         string            `json:"kind"`
	DurationDays *int32            `json:"duration_days"`
	TrafficBytes *int64            `json:"traffic_bytes"`
	Price        money             `json:"price"`
	Enabled      bool              `json:"enabled"`
	IsTrial      bool              `json:"is_trial"`
	IsTopup      bool              `json:"is_topup"`
	Sort         int32             `json:"sort"`
	Sales        int64             `json:"sales"`
}

func planOf(p *store.Plan, sales int64) planJSON {
	return planJSON{ID: p.ID, Name: p.NameI18n, Kind: p.Kind, DurationDays: p.DurationDays, TrafficBytes: p.TrafficBytes,
		Price: money{p.Price, p.Currency}, Enabled: p.Enabled, IsTrial: p.IsTrial, IsTopup: p.IsTopup, Sort: p.Sort, Sales: sales}
}

// listPlans: GET /api/v1/plans, every plan (also turned off) with its sales.
func (s *Server) listPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.cfg.Store.ListPlans(r.Context(), s.cfg.Store.Conn(), true)
	if err != nil {
		s.fail(w, "list plans", err)
		return
	}
	sales, err := s.cfg.Store.PlanSales(r.Context(), s.cfg.Store.Conn())
	if err != nil {
		s.fail(w, "plan sales", err)
		return
	}
	items := make([]planJSON, 0, len(plans))
	for i := range plans {
		items = append(items, planOf(&plans[i], sales[plans[i].ID]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// planBody is a plan as the dashboard edits it: traffic in GB, the price in
// major units.
type planBody struct {
	Name         map[string]string `json:"name"`
	Kind         string            `json:"kind"`
	DurationDays *int32            `json:"duration_days"`
	TrafficGB    string            `json:"traffic_gb"`
	Price        string            `json:"price"`
	Currency     string            `json:"currency"`
	Enabled      bool              `json:"enabled"`
	IsTrial      bool              `json:"is_trial"`
	IsTopup      bool              `json:"is_topup"`
	Sort         int32             `json:"sort"`
}

func (b *planBody) plan(w http.ResponseWriter) (*store.Plan, bool) {
	cur := strings.ToUpper(strings.TrimSpace(b.Currency))
	price, ok := parseAmount(b.Price, cur, false)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "the price must be a number in a supported currency")
		return nil, false
	}
	bytes, ok := gbToBytes(b.TrafficGB)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "traffic must be a number of GB")
		return nil, false
	}
	name := map[string]string{}
	for _, lang := range []string{"fa", "en"} {
		if v := strings.TrimSpace(b.Name[lang]); v != "" {
			if len(v) > 100 {
				writeError(w, http.StatusBadRequest, "invalid", "a plan name is at most 100 characters")
				return nil, false
			}
			name[lang] = v
		}
	}
	p := &store.Plan{NameI18n: name, Kind: b.Kind, DurationDays: b.DurationDays, Price: price, Currency: cur,
		Enabled: b.Enabled, IsTrial: b.IsTrial, IsTopup: b.IsTopup, Sort: b.Sort}
	if p.DurationDays != nil && *p.DurationDays == 0 {
		p.DurationDays = nil
	}
	if bytes > 0 {
		p.TrafficBytes = &bytes
	}
	return p, true
}

// createPlan: POST /api/v1/plans
func (s *Server) createPlan(w http.ResponseWriter, r *http.Request) {
	var b planBody
	if !readJSON(w, r, &b) {
		return
	}
	p, ok := b.plan(w)
	if !ok {
		return
	}
	s.savePlan(w, r, p)
}

// updatePlan: PUT /api/v1/plans/{id}
func (s *Server) updatePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var b planBody
	if !readJSON(w, r, &b) {
		return
	}
	p, ok := b.plan(w)
	if !ok {
		return
	}
	p.ID = id
	s.savePlan(w, r, p)
}

func (s *Server) savePlan(w http.ResponseWriter, r *http.Request, p *store.Plan) {
	saved, err := s.cfg.Domain.UpsertPlan(r.Context(), actor(r), p)
	if err != nil {
		s.fail(w, "save plan", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": planOf(saved, 0)})
}
