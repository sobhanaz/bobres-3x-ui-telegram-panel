package web

import (
	"context"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Config names in the Mini App (docs/17-mini-app.md): the checkout's "Choose a name" checks a name before the order (the
// order then carries it, miniOrderIn.ClientName), and a service's Rename (the customer's My services, and the reseller
// panel's services) renames it. Both are the signed-in user's own: core checks the setting of their role, the rules, that
// the name is free on every panel, and that the service is theirs. The /me answer says whether the option is on
// (features.config_names); each service says whether it can be renamed (renamable).

func init() {
	registerMiniFeature(miniFeature{routes: (*Server).miniNamesRoutes, me: (*Server).miniNamesFlag})
}

func (s *Server) miniNamesRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/mini/v1/config-name/check", s.miniSession(false, s.miniCheckConfigName))
	api.HandleFunc("POST /api/mini/v1/services/{id}/rename", s.miniSession(false, s.miniRenameService))
}

func (s *Server) miniNamesFlag(ctx context.Context, u *store.User, features map[string]any) error {
	features["config_names"] = s.cfg.Domain.ConfigNamesOn(ctx, u)
	return nil
}

// miniRenamable says, for the signed-in user's services, whether each can be renamed now.
func (s *Server) miniRenamable(ctx context.Context, u *store.User) func(*store.Subscription) bool {
	return s.cfg.Domain.RenamableFor(ctx, s.cfg.Servers, u)
}

// miniCheckConfigName: POST /api/mini/v1/config-name/check {name, subscription_id?} says whether a name can be used for
// a new service, or (subscription_id) as the new name of one of the user's services: {ok, reason, name}. A name the user
// cannot use is an answer (reason: off, length, chars, mix, automatic, taken, unchecked), not an error.
func (s *Server) miniCheckConfigName(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           string `json:"name"`
		SubscriptionID string `json:"subscription_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Name) > 200 || (in.SubscriptionID != "" && !miniUUIDRe.MatchString(in.SubscriptionID)) {
		s.miniFail(w, "mini app config name", errInvalid("a name and, for a rename, a service"))
		return
	}
	res, err := s.cfg.Domain.CheckConfigName(r.Context(), s.miniNames(), miniFrom(r.Context()).user.ID, in.Name, in.SubscriptionID)
	if err != nil {
		s.miniFail(w, "mini app config name", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": res.OK, "reason": res.Reason, "name": res.Name})
}

// miniRenameService: POST /api/mini/v1/services/{id}/rename {name} renames one of the user's services, keeping its
// subscription link: {ok, reason, name, service}. service is the customer's service (miniServiceBody), or the reseller
// panel's when the user is a reseller.
func (s *Server) miniRenameService(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Name) > 200 || !miniUUIDRe.MatchString(r.PathValue("id")) {
		s.miniFail(w, "mini app rename", store.ErrNotFound)
		return
	}
	if s.cfg.Provisioner == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the store cannot reach its servers")
		return
	}
	ctx, u := r.Context(), miniFrom(r.Context()).user
	res, err := s.cfg.Domain.RenameOwnService(ctx, s.cfg.Provisioner, s.cfg.Servers, u.ID, r.PathValue("id"), in.Name)
	if err != nil {
		s.miniFail(w, "mini app rename", err)
		return
	}
	out := map[string]any{"ok": res.OK, "reason": res.Reason, "name": res.Name}
	if res.Service != nil {
		renamable := s.miniRenamable(ctx, u)
		if u.Role == domain.ResellerRole {
			body := s.miniResellerServiceBody(r, res.Service)
			body["config_name"], body["renamable"] = res.Service.ClientEmail, renamable(res.Service)
			out["service"] = body
		} else {
			out["service"] = s.miniServiceBody(r, res.Service, renamable)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// miniNames is the provisioner as the name check asks it (nil without one: every name is then "unchecked").
func (s *Server) miniNames() domain.NameChecker {
	if s.cfg.Provisioner == nil {
		return nil
	}
	return s.cfg.Provisioner
}
