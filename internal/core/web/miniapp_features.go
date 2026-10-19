package web

import (
	"context"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// A section of the Mini App beyond the shop (a free trial, invites, support,
// the reseller panel...) is a miniFeature: its API routes under
// /api/mini/v1/, and what it adds to the signed-in customer's /me answer
// (under "features"), which is how the app knows what to show. Each section
// lives in its own file and registers itself in init, so adding one touches no
// shared file. Its Vue side lives in web/miniapp/src/sections/<name>/ (see
// docs/17-mini-app.md).
type miniFeature struct {
	// routes registers the section's API on the Mini App's mux; its handlers
	// go through s.miniSession like the shop's.
	routes func(s *Server, api *http.ServeMux)
	// me adds the section's flags to features, which /me and the sign-in
	// answer carry; the keys are the section's own. May be nil.
	me func(s *Server, ctx context.Context, u *store.User, features map[string]any) error
}

var miniFeatures []miniFeature

// registerMiniFeature adds a section; call it from an init function.
func registerMiniFeature(f miniFeature) { miniFeatures = append(miniFeatures, f) }

// miniFeatureFlags is what the sections say about this customer.
func (s *Server) miniFeatureFlags(ctx context.Context, u *store.User) (map[string]any, error) {
	flags := map[string]any{}
	for _, f := range miniFeatures {
		if f.me == nil {
			continue
		}
		if err := f.me(s, ctx, u, flags); err != nil {
			return nil, err
		}
	}
	return flags, nil
}
