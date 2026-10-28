package web

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// Resellers (milestone 5): owner and admin manage them here; resellers
// themselves work only in the bot. Balances are minor units like every
// amount the API returns; amounts sent in are decimals in major units.

type resellerUser struct {
	userBrief
	Status string `json:"status"`
}

type tierRef struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	DiscountPercent   int32  `json:"discount_percent"`
	CommissionPercent int32  `json:"commission_percent"`
}

type resellerBalance struct {
	Currency    string `json:"currency"`
	Balance     int64  `json:"balance"` // negative: what they owe
	CreditLimit int64  `json:"credit_limit"`
}

type resellerJSON struct {
	User               resellerUser      `json:"user"`
	Tier               tierRef           `json:"tier"`
	CommissionPercent  int32             `json:"commission_percent"`  // the effective rate
	CommissionOverride *int32            `json:"commission_override"` // null: the tier's
	Balances           []resellerBalance `json:"balances"`
	Customers          int64             `json:"customers"`
	CommissionEarned   []money           `json:"commission_earned"`
	CreatedAt          int64             `json:"created_at"`
}

// resellerDetail is one reseller as their page shows them.
type resellerDetail struct {
	resellerJSON
	Note      string `json:"note"`
	Version   int    `json:"version"`
	CanRemove bool   `json:"can_remove"` // nothing owed
	// What their configs are called and grouped as in the 3x-ui panel: the prefix of the names (""
	// until the reseller chooses it), the NAME of the group "BOBRES-NAME" ("" until the owner gives
	// one) and the group itself, and whether the configs made before the group were moved into it.
	ConfigPrefix   string `json:"config_prefix"`
	PanelGroup     string `json:"panel_group"`
	PanelGroupFull string `json:"panel_group_full"`
	GroupsSynced   bool   `json:"groups_synced"`
	// Targets is this month against the owner's monthly targets (absent while they are off).
	Targets map[string]any `json:"targets,omitempty"`
}

func resellerOf(r *store.ResellerRow) resellerJSON {
	out := resellerJSON{
		User:               resellerUser{*briefOf(r.User.ID, r.User.TelegramID, r.User.Username), r.User.Status},
		Tier:               tierRef{r.Reseller.TierID, r.TierName, r.TierDiscount, r.TierCommission},
		CommissionPercent:  r.Commission(),
		CommissionOverride: r.Reseller.CommissionPercent,
		Balances:           []resellerBalance{},
		Customers:          r.Customers,
		CommissionEarned:   moneyList(r.Earned),
		CreatedAt:          r.Reseller.CreatedAt.Unix(),
	}
	for _, w := range r.Wallets {
		out.Balances = append(out.Balances, resellerBalance{w.Currency, w.Balance, w.CreditLimit})
	}
	return out
}

func resellerDetailOf(r *store.ResellerRow, naming *store.ResellerNaming) resellerDetail {
	d := resellerDetail{resellerJSON: resellerOf(r), Note: r.Reseller.Note, Version: r.Reseller.Version, CanRemove: true}
	if naming != nil {
		d.ConfigPrefix, d.PanelGroup, d.PanelGroupFull = naming.ConfigPrefix, naming.PanelGroup, domain.PanelGroupOf(naming.PanelGroup)
		d.GroupsSynced = naming.PanelGroup != "" && naming.GroupsSyncedAt != nil
	}
	for _, w := range r.Wallets {
		if w.Balance < 0 {
			d.CanRemove = false
		}
	}
	return d
}

// listResellers: GET /api/v1/resellers?q=&tier=&in_debt=&page=&size=
func (s *Server) listResellers(w http.ResponseWriter, r *http.Request) {
	p, page, size := pageOf(r)
	tier, ok := queryID(r, "tier")
	if !ok {
		writeJSON(w, http.StatusOK, list{[]resellerJSON{}, 0, page, size})
		return
	}
	q := r.URL.Query()
	inDebt, _ := strconv.ParseBool(q.Get("in_debt"))
	rows, total, err := s.cfg.Store.ListResellers(r.Context(), s.cfg.Store.Conn(),
		store.ResellerFilter{Query: q.Get("q"), TierID: tier, InDebt: inDebt}, p)
	if err != nil {
		s.fail(w, "list resellers", err)
		return
	}
	items := make([]resellerJSON, 0, len(rows))
	for i := range rows {
		items = append(items, resellerOf(&rows[i]))
	}
	writeJSON(w, http.StatusOK, list{items, total, page, size})
}

// writeReseller answers one reseller as their page shows them.
func (s *Server) writeReseller(w http.ResponseWriter, r *http.Request, status int, id string) {
	row, err := s.cfg.Store.GetResellerRow(r.Context(), s.cfg.Store.Conn(), id)
	if err != nil {
		s.fail(w, "load reseller", err)
		return
	}
	naming, err := s.cfg.Store.GetResellerNaming(r.Context(), s.cfg.Store.Conn(), id)
	if err != nil {
		s.fail(w, "load reseller naming", err)
		return
	}
	item := resellerDetailOf(row, naming)
	if t, err := s.cfg.Domain.ResellerTargets(r.Context(), id); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.cfg.Log.Warn("dashboard api: reseller targets", "err", err)
		}
	} else if t.On {
		item.Targets = targetsJSON(t)
	}
	writeJSON(w, status, map[string]any{"item": item})
}

// getReseller: GET /api/v1/resellers/{id}; their services, customers,
// statement and orders come from the lists (?user=, ?referred_by=).
func (s *Server) getReseller(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	s.writeReseller(w, r, http.StatusOK, id)
}

// creditLimitBody is one currency's credit limit, a decimal in major units.
type creditLimitBody struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

// resellerBody is a reseller's terms as the dashboard sends them.
type resellerBody struct {
	TierID            string            `json:"tier_id"`
	CreditLimits      []creditLimitBody `json:"credit_limits"`
	CommissionPercent *int32            `json:"commission_percent"`
	Note              string            `json:"note"`
	Reason            string            `json:"reason"`
	// PanelGroup (a new reseller only, the owner's): the NAME of their 3x-ui group "BOBRES-NAME".
	PanelGroup string `json:"panel_group"`
}

// input reads the terms; false after answering a field error.
func (b *resellerBody) input(w http.ResponseWriter) (domain.ResellerInput, bool) {
	in := domain.ResellerInput{TierID: strings.ToLower(strings.TrimSpace(b.TierID)), CreditLimits: map[string]int64{},
		CommissionPercent: b.CommissionPercent, Note: b.Note, PanelGroup: b.PanelGroup}
	if !uuidRe.MatchString(in.TierID) {
		writeFieldError(w, "tier_id", "pick a tier")
		return in, false
	}
	for _, l := range b.CreditLimits {
		cur := strings.ToUpper(strings.TrimSpace(l.Currency))
		if bmoney.Scale(cur) < 0 {
			writeFieldError(w, "credit_limits", "unknown currency")
			return in, false
		}
		if _, dup := in.CreditLimits[cur]; dup {
			writeFieldError(w, "credit_limit."+cur, "given twice")
			return in, false
		}
		amount := l.Amount
		if strings.TrimSpace(amount) == "" {
			amount = "0"
		}
		v, ok := parseAmount(amount, cur, false)
		if !ok {
			writeFieldError(w, "credit_limit."+cur, "a number (0 for no credit)")
			return in, false
		}
		in.CreditLimits[cur] = v
	}
	return in, true
}

// createReseller: POST /api/v1/resellers {user_id, tier_id, credit_limits,
// commission_percent, note, reason}.
func (s *Server) createReseller(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
		resellerBody
	}
	if !readJSON(w, r, &req) {
		return
	}
	userID := strings.ToLower(strings.TrimSpace(req.UserID))
	if !uuidRe.MatchString(userID) {
		writeFieldError(w, "user_id", "a user's id")
		return
	}
	in, ok := req.input(w)
	if !ok {
		return
	}
	if _, err := s.cfg.Domain.MakeReseller(r.Context(), actor(r), userID, in, req.Reason); err != nil {
		s.fail(w, "make reseller", err)
		return
	}
	s.writeReseller(w, r, http.StatusCreated, userID)
}

// updateReseller: PUT /api/v1/resellers/{id} {version, tier_id,
// credit_limits, commission_percent, note, reason}. Currencies left out of
// credit_limits keep their limit.
func (s *Server) updateReseller(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Version int `json:"version"`
		resellerBody
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Version <= 0 {
		writeFieldError(w, "version", "required: the version of the reseller you read")
		return
	}
	in, ok := req.input(w)
	if !ok {
		return
	}
	if _, err := s.cfg.Domain.UpdateReseller(r.Context(), actor(r), id, req.Version, in, req.Reason); err != nil {
		s.fail(w, "update reseller", err)
		return
	}
	s.writeReseller(w, r, http.StatusOK, id)
}

// resellerNamingBody changes what a reseller's configs are called and grouped as: a field left out stays.
type resellerNamingBody struct {
	PanelGroup   *string `json:"panel_group"`   // the NAME of "BOBRES-NAME"; "" takes it away
	ConfigPrefix *string `json:"config_prefix"` // "" takes it away
}

// updateResellerNaming: PUT /api/v1/resellers/{id}/naming {panel_group, config_prefix}: the owner's. A new group name
// moves the reseller's earlier configs into it in the background; a new prefix is for the configs made after it.
func (s *Server) updateResellerNaming(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req resellerNamingBody
	if !readJSON(w, r, &req) {
		return
	}
	if req.PanelGroup == nil && req.ConfigPrefix == nil {
		writeFieldError(w, "panel_group", "give a group name or a prefix")
		return
	}
	if req.PanelGroup != nil {
		if err := s.cfg.Domain.SetResellerPanelGroup(r.Context(), actor(r), id, *req.PanelGroup); err != nil {
			s.fail(w, "set the reseller's group", err)
			return
		}
	}
	if req.ConfigPrefix != nil {
		if err := s.cfg.Domain.SetResellerConfigPrefix(r.Context(), actor(r), id, *req.ConfigPrefix); err != nil {
			s.fail(w, "set the reseller's prefix", err)
			return
		}
	}
	s.writeReseller(w, r, http.StatusOK, id)
}

// resellerGroupSync: POST /api/v1/resellers/{id}/group-sync: the owner has the reseller's existing configs moved
// into their group again (they were made before the group, or the group has a new name).
func (s *Server) resellerGroupSync(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.RequestResellerGroupSync(r.Context(), actor(r), id); err != nil {
		s.fail(w, "move the reseller's configs", err)
		return
	}
	s.writeReseller(w, r, http.StatusOK, id)
}

// resellerPayment: POST /api/v1/resellers/{id}/payments {amount, currency,
// reason, key}: money the reseller paid the store. A replay with the same key
// answers the first entry.
func (s *Server) resellerPayment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
		Reason   string `json:"reason"`
		Key      string `json:"key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	cur := strings.ToUpper(strings.TrimSpace(req.Currency))
	amount, ok := parseAmount(req.Amount, cur, false)
	if !ok || amount <= 0 {
		writeFieldError(w, "amount", "a positive amount in a supported currency")
		return
	}
	if !idemKeyRe.MatchString(req.Key) {
		writeError(w, http.StatusBadRequest, "invalid", "missing request key")
		return
	}
	ctx := r.Context()
	_, e, err := s.cfg.Domain.RecordResellerPayment(ctx, actor(r), id, amount, cur, req.Reason, req.Key)
	if err != nil {
		s.fail(w, "reseller payment", err)
		return
	}
	row, err := s.cfg.Store.GetResellerRow(ctx, s.cfg.Store.Conn(), id)
	if err != nil {
		s.fail(w, "load reseller", err)
		return
	}
	naming, err := s.cfg.Store.GetResellerNaming(ctx, s.cfg.Store.Conn(), id)
	if err != nil {
		s.fail(w, "load reseller naming", err)
		return
	}
	entry := ledgerOf(&store.LedgerRow{LedgerEntry: *e, TelegramID: row.User.TelegramID, Username: row.User.Username})
	writeJSON(w, http.StatusOK, map[string]any{"item": resellerDetailOf(row, naming), "entry": entry})
}

// removeReseller: POST /api/v1/resellers/{id}/remove {reason}: a customer
// again; refused while they owe money.
func (s *Server) removeReseller(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	reason, ok := readReason(w, r)
	if !ok {
		return
	}
	if err := s.cfg.Domain.RemoveReseller(r.Context(), actor(r), id, reason); err != nil {
		s.fail(w, "remove reseller", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"user_id": id, "role": "user"})
}

type tierPriceJSON struct {
	PlanID   string `json:"plan_id"`
	Price    string `json:"price"` // a decimal in major units of the plan's currency
	Currency string `json:"currency"`
}

type tierJSON struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	DiscountPercent   int32           `json:"discount_percent"`
	CommissionPercent int32           `json:"commission_percent"`
	Prices            []tierPriceJSON `json:"prices"`
	Resellers         int64           `json:"resellers"`
	Version           int             `json:"version"`
}

// tierOf is a tier for the dashboard; currencies maps plan ids to their
// currency.
func tierOf(t *store.ResellerTier, resellers int64, currencies map[string]string) tierJSON {
	out := tierJSON{ID: t.ID, Name: t.Name, DiscountPercent: t.DiscountPercent, CommissionPercent: t.CommissionPercent,
		Prices: []tierPriceJSON{}, Resellers: resellers, Version: t.Version}
	for plan, price := range t.Prices {
		cur := currencies[plan]
		out.Prices = append(out.Prices, tierPriceJSON{PlanID: plan, Price: decimal(price, cur), Currency: cur})
	}
	sort.Slice(out.Prices, func(i, j int) bool { return out.Prices[i].PlanID < out.Prices[j].PlanID })
	return out
}

// planCurrencies maps every plan's id to its currency.
func (s *Server) planCurrencies(r *http.Request) (map[string]string, error) {
	plans, err := s.cfg.Store.ListPlans(r.Context(), s.cfg.Store.Conn(), true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(plans))
	for _, p := range plans {
		out[p.ID] = p.Currency
	}
	return out, nil
}

// listTiers: GET /api/v1/reseller-tiers.
func (s *Server) listTiers(w http.ResponseWriter, r *http.Request) {
	tiers, err := s.cfg.Domain.ListTiers(r.Context())
	if err != nil {
		s.fail(w, "list tiers", err)
		return
	}
	currencies, err := s.planCurrencies(r)
	if err != nil {
		s.fail(w, "list tiers", err)
		return
	}
	items := make([]tierJSON, 0, len(tiers))
	for i := range tiers {
		items = append(items, tierOf(&tiers[i].ResellerTier, tiers[i].Resellers, currencies))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// tierBody is a tier as the dashboard edits it: prices are decimals in
// major units of each plan's currency (all of them: a plan left out has no
// fixed price).
type tierBody struct {
	Name              string `json:"name"`
	DiscountPercent   int32  `json:"discount_percent"`
	CommissionPercent int32  `json:"commission_percent"`
	Prices            []struct {
		PlanID string `json:"plan_id"`
		Price  string `json:"price"`
	} `json:"prices"`
	Version int    `json:"version"`
	Reason  string `json:"reason"`
}

// input reads the tier; false after answering a field error.
func (b *tierBody) input(w http.ResponseWriter, currencies map[string]string) (domain.TierInput, bool) {
	in := domain.TierInput{Name: b.Name, DiscountPercent: b.DiscountPercent, CommissionPercent: b.CommissionPercent,
		Prices: map[string]int64{}, Currencies: map[string]string{}}
	for _, p := range b.Prices {
		id := strings.ToLower(strings.TrimSpace(p.PlanID))
		field := "prices." + id
		cur, known := currencies[id]
		if !known {
			writeFieldError(w, field, "no such plan")
			return in, false
		}
		if _, dup := in.Prices[id]; dup {
			writeFieldError(w, field, "given twice")
			return in, false
		}
		if strings.TrimSpace(p.Price) == "" {
			continue // no fixed price: the tier's discount applies
		}
		v, ok := parseAmount(p.Price, cur, false)
		if !ok {
			writeFieldError(w, field, "a price in the plan's currency")
			return in, false
		}
		in.Prices[id], in.Currencies[id] = v, cur
	}
	return in, true
}

// saveTier creates a tier (id "") or updates one.
func (s *Server) saveTier(w http.ResponseWriter, r *http.Request, id string) {
	var req tierBody
	if !readJSON(w, r, &req) {
		return
	}
	if id != "" && req.Version <= 0 {
		writeFieldError(w, "version", "required: the version of the tier you read")
		return
	}
	currencies, err := s.planCurrencies(r)
	if err != nil {
		s.fail(w, "save tier", err)
		return
	}
	in, ok := req.input(w, currencies)
	if !ok {
		return
	}
	var (
		t      *store.ResellerTier
		status = http.StatusOK
	)
	if id == "" {
		t, err = s.cfg.Domain.CreateTier(r.Context(), actor(r), in, req.Reason)
		status = http.StatusCreated
	} else {
		t, err = s.cfg.Domain.UpdateTier(r.Context(), actor(r), id, req.Version, in, req.Reason)
	}
	if err != nil {
		s.fail(w, "save tier", err)
		return
	}
	var resellers int64
	if id != "" {
		tiers, err := s.cfg.Domain.ListTiers(r.Context())
		if err != nil {
			s.fail(w, "save tier", err)
			return
		}
		for _, row := range tiers {
			if row.ID == id {
				resellers = row.Resellers
			}
		}
	}
	writeJSON(w, status, map[string]any{"item": tierOf(t, resellers, currencies)})
}

// createTier: POST /api/v1/reseller-tiers {name, discount_percent,
// commission_percent, prices: [{plan_id, price}], reason?}.
func (s *Server) createTier(w http.ResponseWriter, r *http.Request) { s.saveTier(w, r, "") }

// updateTier: PUT /api/v1/reseller-tiers/{id}, the same with the version.
func (s *Server) updateTier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	s.saveTier(w, r, id)
}

// deleteTier: DELETE /api/v1/reseller-tiers/{id} (409 while resellers are on
// it).
func (s *Server) deleteTier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.DeleteTier(r.Context(), actor(r), id, ""); err != nil {
		s.fail(w, "delete tier", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
