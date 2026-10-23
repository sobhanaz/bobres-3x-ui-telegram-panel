package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
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
	// ServerIDs are the 3x-ui servers its new services go to ([] = any).
	ServerIDs []string `json:"server_ids"`
	// Inbounds is its choice of panel inbounds per server ([] = every
	// attachable inbound of whichever server it goes to).
	Inbounds []store.PlanInbound `json:"inbounds"`
	// GroupID is the plan group it is listed under (null: none).
	GroupID *string `json:"group_id"`
	// Reward: a paid purchase gives the buyer a discount code for any plan (null: none).
	Reward *rewardJSON `json:"reward"`
	// DeletedAt (unix seconds) is set only on a deleted plan, in the list of deleted plans.
	DeletedAt *int64 `json:"deleted_at,omitempty"`
}

// rewardJSON is a plan's reward as the dashboard reads it: a percent or an amount in the plan's currency, the days the code lasts and the
// text the buyer reads with it.
type rewardJSON struct {
	Kind    string            `json:"kind"` // percent | amount
	Percent *int32            `json:"percent,omitempty"`
	Amount  *money            `json:"amount,omitempty"`
	Days    int32             `json:"days"`
	Text    map[string]string `json:"text"`
}

func rewardOf(p *store.Plan) *rewardJSON {
	text := p.RewardTextI18n
	if text == nil {
		text = map[string]string{}
	}
	switch {
	case p.RewardPercent != nil:
		return &rewardJSON{Kind: "percent", Percent: p.RewardPercent, Days: p.RewardDays, Text: text}
	case p.RewardAmount != nil:
		return &rewardJSON{Kind: "amount", Amount: &money{*p.RewardAmount, p.Currency}, Days: p.RewardDays, Text: text}
	}
	return nil
}

func planOf(p *store.Plan, sales int64) planJSON {
	ids := p.ServerIDs
	if ids == nil {
		ids = []string{}
	}
	inbounds := make([]store.PlanInbound, 0, len(p.Inbounds))
	for _, in := range p.Inbounds {
		if in.InboundIDs == nil {
			in.InboundIDs = []int32{}
		}
		inbounds = append(inbounds, in)
	}
	return planJSON{ID: p.ID, Name: p.NameI18n, Kind: p.Kind, DurationDays: p.DurationDays, TrafficBytes: p.TrafficBytes,
		Price: money{p.Price, p.Currency}, Enabled: p.Enabled, IsTrial: p.IsTrial, IsTopup: p.IsTopup, Sort: p.Sort, Sales: sales,
		ServerIDs: ids, Inbounds: inbounds, GroupID: p.GroupID, Reward: rewardOf(p)}
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
	// ServerIDs: absent keeps the plan's servers (a new plan: any server).
	ServerIDs *[]string `json:"server_ids"`
	// Inbounds: absent keeps the plan's choice (a new plan: none); [] clears it.
	Inbounds *[]store.PlanInbound `json:"inbounds"`
	// GroupID: absent keeps the plan's group (a new plan: none); "" takes it out of its group.
	GroupID *string `json:"group_id"`
	// Reward: absent keeps the plan's reward; kind "none" takes it away.
	Reward *rewardBody `json:"reward"`
}

// rewardBody is a reward as the dashboard sends it: Value is a whole percent (1 to 90) or an amount in major units of the plan's currency.
type rewardBody struct {
	Kind  string            `json:"kind"` // none | percent | amount
	Value string            `json:"value"`
	Days  int32             `json:"days"`
	Text  map[string]string `json:"text"`
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
	if b.ServerIDs != nil {
		p.ServerIDs = append([]string{}, *b.ServerIDs...)
	}
	if b.Inbounds != nil {
		p.Inbounds = append([]store.PlanInbound{}, *b.Inbounds...)
	}
	p.GroupID = b.GroupID
	if !b.reward(w, p) {
		return nil, false
	}
	return p, true
}

// reward copies a plan body's reward onto the plan: absent keeps the stored one, "none" takes it away.
func (b *planBody) reward(w http.ResponseWriter, p *store.Plan) bool {
	if b.Reward == nil {
		p.KeepReward = true
		return true
	}
	r := b.Reward
	p.RewardDays, p.RewardTextI18n = r.Days, r.Text
	switch r.Kind {
	case "", "none":
	case "percent":
		n, err := strconv.Atoi(strings.TrimSpace(r.Value))
		if err != nil || n < 1 || n > domain.MaxRewardPercent {
			writeError(w, http.StatusBadRequest, "invalid", "the reward percent must be a whole number from 1 to "+strconv.Itoa(domain.MaxRewardPercent))
			return false
		}
		pct := int32(n) //nolint:gosec // 1 to 90
		p.RewardPercent = &pct
	case "amount":
		amt, ok := parseAmount(r.Value, p.Currency, false)
		if !ok || amt <= 0 {
			writeError(w, http.StatusBadRequest, "invalid", "the reward amount must be a number in the plan's currency")
			return false
		}
		p.RewardAmount = &amt
	default:
		writeError(w, http.StatusBadRequest, "invalid", "the reward must be none, a percent or an amount")
		return false
	}
	return true
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

// deletePlan: DELETE /api/v1/plans/{id}. The plan is taken off sale and out of every list; it is never erased (its orders, reports and refunds
// need it). The answer is {"result": "deleted"}.
func (s *Server) deletePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.DeletePlan(r.Context(), actor(r), id); err != nil {
		s.fail(w, "delete plan", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "deleted"})
}

// listDeletedPlans: GET /api/v1/plans/deleted, the plans that were deleted (the owner's: they can restore them).
func (s *Server) listDeletedPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.cfg.Domain.DeletedPlans(r.Context())
	if err != nil {
		s.fail(w, "list deleted plans", err)
		return
	}
	items := make([]planJSON, 0, len(plans))
	for i := range plans {
		it := planOf(&plans[i], 0)
		it.DeletedAt = unix(plans[i].DeletedAt)
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// restorePlan: POST /api/v1/plans/{id}/restore brings a deleted plan back, off sale.
func (s *Server) restorePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := s.cfg.Domain.RestorePlan(r.Context(), actor(r), id)
	if err != nil {
		s.fail(w, "restore plan", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": planOf(p, 0)})
}
