package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

type discountJSON struct {
	Code      string `json:"code"`
	Percent   *int32 `json:"percent"`
	Amount    *money `json:"amount"`
	MaxUses   *int32 `json:"max_uses"`
	Used      int32  `json:"used"`
	ExpiresAt *int64 `json:"expires_at"`
	Enabled   bool   `json:"enabled"`
}

func discountOf(d *store.Discount) discountJSON {
	out := discountJSON{Code: d.Code, Percent: d.Percent, MaxUses: d.MaxUses, Used: d.Used,
		ExpiresAt: unix(d.ExpiresAt), Enabled: d.Enabled}
	if d.Amount != nil && d.Currency != nil {
		out.Amount = &money{*d.Amount, *d.Currency}
	}
	return out
}

// listDiscounts: GET /api/v1/discounts
func (s *Server) listDiscounts(w http.ResponseWriter, r *http.Request) {
	ds, err := s.cfg.Store.ListDiscounts(r.Context(), s.cfg.Store.Conn())
	if err != nil {
		s.fail(w, "list discounts", err)
		return
	}
	items := make([]discountJSON, 0, len(ds))
	for i := range ds {
		items = append(items, discountOf(&ds[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// saveDiscount: POST /api/v1/discounts creates a code or replaces its terms:
// {code, percent | amount+currency, max_uses, expires_at, enabled}.
func (s *Server) saveDiscount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code      string `json:"code"`
		Percent   *int32 `json:"percent"`
		Amount    string `json:"amount"`
		Currency  string `json:"currency"`
		MaxUses   *int32 `json:"max_uses"`
		ExpiresAt *int64 `json:"expires_at"`
		Enabled   bool   `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	d := &store.Discount{Code: req.Code, Percent: req.Percent, MaxUses: req.MaxUses, Enabled: req.Enabled}
	if strings.TrimSpace(req.Amount) != "" {
		cur := strings.ToUpper(strings.TrimSpace(req.Currency))
		amt, ok := parseAmount(req.Amount, cur, false)
		if !ok || amt <= 0 {
			writeError(w, http.StatusBadRequest, "invalid", "the amount must be a positive number in a supported currency")
			return
		}
		d.Amount, d.Currency = &amt, &cur
	}
	if req.ExpiresAt != nil {
		t := time.Unix(*req.ExpiresAt, 0)
		d.ExpiresAt = &t
	}
	saved, err := s.cfg.Domain.UpsertDiscount(r.Context(), actor(r), d)
	if err != nil {
		s.fail(w, "save discount", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"discount": discountOf(saved)})
}

// setDiscountEnabled: PUT /api/v1/discounts/{code}/enabled {enabled}
func (s *Server) setDiscountEnabled(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	saved, err := s.cfg.Domain.SetDiscountEnabled(r.Context(), actor(r), r.PathValue("code"), req.Enabled)
	if err != nil {
		s.fail(w, "set discount enabled", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"discount": discountOf(saved)})
}

// getReferrals: GET /api/v1/referrals, the reward and the top inviters.
func (s *Server) getReferrals(w http.ResponseWriter, r *http.Request) {
	settings, err := s.cfg.Store.GetSettings(r.Context(), s.cfg.Store.Conn())
	if err != nil {
		s.fail(w, "referral settings", err)
		return
	}
	pct := 0
	if raw, ok := settings["referral.reward_percent"]; ok {
		var v string
		if json.Unmarshal(raw, &v) == nil {
			pct, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	top, err := s.cfg.Store.TopReferrers(r.Context(), s.cfg.Store.Conn(), 20)
	if err != nil {
		s.fail(w, "top referrers", err)
		return
	}
	items := make([]map[string]any, 0, len(top))
	for _, t := range top {
		items = append(items, map[string]any{
			"user": briefOf(t.UserID, t.TelegramID, t.Username), "invited": t.Invited, "rewarded": t.Rewarded,
			"earned": moneyList(t.Earned),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"reward_percent": pct, "top": items})
}

// setReferralReward: PUT /api/v1/referrals {reward_percent} (0 turns the
// program off).
func (s *Server) setReferralReward(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RewardPercent int `json:"reward_percent"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	value := ""
	if req.RewardPercent != 0 {
		value = strconv.Itoa(req.RewardPercent)
	}
	if err := s.cfg.Domain.SetSetting(r.Context(), actor(r), "referral.reward_percent", value); err != nil {
		s.fail(w, "set referral reward", err)
		return
	}
	s.refreshBot()
	writeJSON(w, http.StatusOK, map[string]int{"reward_percent": req.RewardPercent})
}
