package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// The free trial in the Mini App: the same one-per-account trial the bot
// gives, on the same idempotency key (trial:<telegram id>), so starting it in
// either place is the same trial.

func init() {
	registerMiniFeature(miniFeature{
		routes: func(s *Server, api *http.ServeMux) {
			api.HandleFunc("GET /api/mini/v1/trial", s.miniSession(false, s.miniTrial))
			api.HandleFunc("POST /api/mini/v1/trial", s.miniSession(false, s.miniStartTrial))
		},
		me: func(s *Server, ctx context.Context, u *store.User, features map[string]any) error {
			plan, eligible, err := s.trialFor(ctx, u)
			features["trial"] = err == nil && plan != nil && eligible
			return err
		},
	})
}

// trialFor is the store's enabled free-trial plan (nil: none) and whether this
// customer may still take it.
func (s *Server) trialFor(ctx context.Context, u *store.User) (*store.Plan, bool, error) {
	plans, err := s.cfg.Store.ListPlans(ctx, s.cfg.Store.Conn(), false)
	if err != nil {
		return nil, false, err
	}
	var plan *store.Plan
	for i := range plans {
		if plans[i].IsTrial && plans[i].Enabled {
			plan = &plans[i]
			break
		}
	}
	if plan == nil {
		return nil, false, nil
	}
	ok, err := s.cfg.Domain.CanStartTrial(ctx, u.ID)
	return plan, ok, err
}

func trialKey(u *store.User) string { return fmt.Sprintf("trial:%d", u.TelegramID) }

// miniTrial: GET /api/mini/v1/trial, what the trial gives and whether this
// customer can still take it.
func (s *Server) miniTrial(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	plan, eligible, err := s.trialFor(ctx, c.user)
	if err != nil {
		s.miniFail(w, "mini app trial", err)
		return
	}
	if plan == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "used": false})
		return
	}
	var days, traffic any
	if plan.DurationDays != nil {
		days = *plan.DurationDays
	}
	if plan.TrafficBytes != nil {
		traffic = *plan.TrafficBytes
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": eligible, "used": !eligible,
		"plan": map[string]any{"names": plan.NameI18n, "kind": plan.Kind, "days": days, "traffic_bytes": traffic, "currency": plan.Currency, "scale": bmoney.Scale(plan.Currency)}})
}

// miniStartTrial: POST /api/mini/v1/trial. The order it answers is polled
// like a purchase's until the service is delivered.
func (s *Server) miniStartTrial(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	plan, eligible, err := s.trialFor(ctx, c.user)
	switch {
	case err != nil:
		s.miniFail(w, "mini app trial", err)
		return
	case plan == nil:
		writeError(w, http.StatusConflict, "plan_unavailable", "there is no free trial now")
		return
	case !eligible:
		writeError(w, http.StatusConflict, "trial_used", "the free trial was used already")
		return
	}
	o, err := s.cfg.Domain.StartTrial(ctx, c.user.ID, plan.ID, trialKey(c.user))
	if errors.Is(err, domain.ErrTrialAlreadyUsed) { // two taps at once: the other one won
		writeError(w, http.StatusConflict, "trial_used", "the free trial was used already")
		return
	}
	if err != nil {
		s.miniFail(w, "mini app trial", err)
		return
	}
	writeJSON(w, http.StatusOK, miniOrderBody(o))
}
