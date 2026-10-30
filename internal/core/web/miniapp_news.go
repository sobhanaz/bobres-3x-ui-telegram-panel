package web

import (
	"context"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Product news in the Mini App (docs/17-mini-app.md): the customer's own choice of whether they get a private message when the store
// announces a new product, the same switch as in the bot. It shows while the store has announcements on.

func init() {
	registerMiniFeature(miniFeature{
		routes: func(s *Server, api *http.ServeMux) {
			api.HandleFunc("PUT /api/mini/v1/news", s.miniSession(false, s.miniSetNews))
		},
		me: func(s *Server, ctx context.Context, u *store.User, features map[string]any) error {
			features["news"] = s.cfg.Domain.AnnounceEnabled(ctx)
			features["news_optin"] = u.AnnounceOptIn != nil && *u.AnnounceOptIn
			return nil
		},
	})
}

// miniSetNews: PUT /api/mini/v1/news {opt_in}: the customer's answer, which counts as the bot's question answered too.
func (s *Server) miniSetNews(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	var req struct {
		OptIn *bool `json:"opt_in"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.OptIn == nil {
		writeError(w, http.StatusBadRequest, "invalid", "opt_in: true or false")
		return
	}
	if !s.cfg.Domain.AnnounceEnabled(ctx) {
		writeError(w, http.StatusNotFound, "not_found", "product news are not available")
		return
	}
	u, err := s.cfg.Domain.SetAnnounceOptIn(ctx, c.user.ID, 0, *req.OptIn)
	if err != nil {
		s.miniFail(w, "mini app news", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"opt_in": u.AnnounceOptIn != nil && *u.AnnounceOptIn})
}
