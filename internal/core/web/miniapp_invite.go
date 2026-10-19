package web

import (
	"context"
	"net/http"
	"sort"
	"strconv"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// Invite friends in the Mini App: the customer's own invite link, how many
// joined through it and what it earned, as in the bot. A reseller has a link of
// their own in their panel, so it is not offered to them here.

func init() {
	registerMiniFeature(miniFeature{
		routes: func(s *Server, api *http.ServeMux) {
			api.HandleFunc("GET /api/mini/v1/invite", s.miniSession(false, s.miniInvite))
		},
		me: func(s *Server, ctx context.Context, u *store.User, features map[string]any) error {
			settings, err := s.cfg.Domain.Settings(ctx)
			if err != nil {
				return err
			}
			features["invite"] = s.inviteOn(settings, u)
			return nil
		},
	})
}

// inviteOn: referral rewards are on, the store's bot is known (the link points
// at it) and the customer is not a reseller.
func (s *Server) inviteOn(settings map[string]string, u *store.User) bool {
	percent, _ := strconv.Atoi(settings["referral.reward_percent"])
	return percent > 0 && pageBotRe.MatchString(settings[domain.SettingBotUsername]) && u.Role != domain.ResellerRole
}

// miniInvite: GET /api/mini/v1/invite.
func (s *Server) miniInvite(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	settings, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		s.miniFail(w, "mini app invite", err)
		return
	}
	if !s.inviteOn(settings, c.user) {
		writeError(w, http.StatusNotFound, "not_found", "invitations are not available")
		return
	}
	info, err := s.cfg.Domain.Referral(ctx, c.user.ID)
	if err != nil {
		s.miniFail(w, "mini app invite", err)
		return
	}
	earned := make([]map[string]any, 0, len(info.Earned))
	for cur, amount := range info.Earned {
		earned = append(earned, map[string]any{"currency": cur, "amount": amount, "scale": bmoney.Scale(cur)})
	}
	sort.Slice(earned, func(i, j int) bool { return earned[i]["currency"].(string) < earned[j]["currency"].(string) })
	writeJSON(w, http.StatusOK, map[string]any{
		"link":    "https://t.me/" + settings[domain.SettingBotUsername] + "?start=r_" + info.Code,
		"percent": info.RewardPercent, "invited": info.Invited, "rewarded": info.Rewarded, "earned": earned,
	})
}
