package web

import (
	"context"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// My codes in the Mini App: the customer's own reward codes and the gift codes they applied, newest first, a page at a time (the bot's
// My codes and /codes are the same list). Every call is the signed-in customer's own: the customer comes from the session, never from
// the request, so there is no way to ask for another's. A gift code is never stored, so a gift entry carries the last characters of
// the code only; a reward code is shown whole to its owner, who can copy it.

func init() {
	registerMiniFeature(miniFeature{
		routes: func(s *Server, api *http.ServeMux) {
			api.HandleFunc("GET /api/mini/v1/codes", s.miniSession(false, s.miniCodes))
		},
		me: func(_ *Server, _ context.Context, _ *store.User, features map[string]any) error {
			features["codes"] = true
			return nil
		},
	})
}

// miniCodes: GET /api/mini/v1/codes?page=&size=.
func (s *Server) miniCodes(w http.ResponseWriter, r *http.Request) {
	page, size, ok := miniPaging(w, r)
	if !ok {
		return
	}
	codes, total, err := s.cfg.Domain.CustomerCodes(r.Context(), miniFrom(r.Context()).user.ID, miniStorePage(page, size))
	if err != nil {
		s.miniFail(w, "mini app codes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"codes": customerCodesOf(codes), "total": total, "page": page, "size": size})
}
