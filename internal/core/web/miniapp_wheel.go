package web

import (
	"context"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// The daily wheel of fortune in the Mini App (docs/17-mini-app.md): the same wheel the bot has, with the same rules, which live in core. The app only
// plays the result core gives it: the draw is made here, on POST /wheel/spin, and the answer says where the wheel stopped.

func init() {
	registerMiniFeature(miniFeature{
		routes: func(s *Server, api *http.ServeMux) {
			api.HandleFunc("GET /api/mini/v1/wheel", s.miniSession(false, s.miniWheel))
			api.HandleFunc("POST /api/mini/v1/wheel/spin", s.miniSession(false, s.miniWheelSpin))
		},
		me: func(s *Server, ctx context.Context, u *store.User, features map[string]any) error {
			settings, err := s.cfg.Domain.Settings(ctx)
			if err != nil {
				return err
			}
			// Customers only, and only while the wheel can pay; the page itself says what a customer still needs (paid orders).
			features["wheel"] = domain.WheelConfigOf(settings).On() && u.Role == "user"
			return nil
		},
	})
}

// miniWheel: GET /api/mini/v1/wheel, the wheel as this customer sees it.
func (s *Server) miniWheel(w http.ResponseWriter, r *http.Request) {
	c := miniFrom(r.Context())
	st, err := s.cfg.Domain.Wheel(r.Context(), c.user.ID)
	if err != nil {
		s.miniFail(w, "mini app wheel", err)
		return
	}
	if !st.On {
		writeError(w, http.StatusNotFound, "not_found", "the wheel is not available")
		return
	}
	prizes := make([]map[string]any, 0, len(st.Prizes))
	for _, p := range st.Prizes {
		prizes = append(prizes, map[string]any{"amount": p.Amount, "weight": p.Weight})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"day": st.Day, "resets_at": st.ResetsAt.Unix(), "reason": st.Reason, "orders": st.Orders, "min_orders": st.MinOrders,
		"spun": st.Spun, "prize": st.Prize, "open": st.Open, "prizes": prizes, "no_prize_weight": st.NoPrizeWeight, "total_weight": st.TotalWeight,
		"currency": st.Currency, "scale": bmoney.Scale(st.Currency),
	})
}

// miniWheelSpin: POST /api/mini/v1/wheel/spin. The answer says where the wheel stopped (status won or lost), gives the earlier spin of the day back
// (already), or refuses without using the spin (closed: the day's budget cannot cover the prize; orders: not enough paid orders; role). Asking twice
// never draws twice.
func (s *Server) miniWheelSpin(w http.ResponseWriter, r *http.Request) {
	c := miniFrom(r.Context())
	res, err := s.cfg.Domain.SpinWheel(r.Context(), c.user.ID)
	if err != nil {
		s.miniFail(w, "mini app wheel", err)
		return
	}
	if res.Status == domain.WheelOff {
		writeError(w, http.StatusNotFound, "not_found", "the wheel is not available")
		return
	}
	var resets int64
	if !res.ResetsAt.IsZero() {
		resets = res.ResetsAt.Unix()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": res.Status, "prize": res.Prize, "index": res.Index, "balance": res.Balance, "day": res.Day, "resets_at": resets,
		"orders": res.Orders, "min_orders": res.MinOrders, "currency": domain.WalletCurrency, "scale": bmoney.Scale(domain.WalletCurrency),
	})
}
