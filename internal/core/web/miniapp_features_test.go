package web

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// withMiniFeature registers a section for one test.
func withMiniFeature(t *testing.T, f miniFeature) {
	t.Helper()
	saved := miniFeatures
	registerMiniFeature(f)
	t.Cleanup(func() { miniFeatures = saved })
}

// A section's routes are served behind the Mini App's own guard (a session
// is required) and its flags reach the sign-in and /me answers.
func TestMiniSectionsAddRoutesAndFlags(t *testing.T) {
	withMiniFeature(t, miniFeature{
		routes: func(s *Server, api *http.ServeMux) {
			api.HandleFunc("GET /api/mini/v1/ping", s.miniSession(false, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{"telegram_id": miniFrom(r.Context()).user.TelegramID})
			}))
		},
		me: func(_ *Server, _ context.Context, u *store.User, features map[string]any) error {
			features["ping"] = u.TelegramID == 4242
			return nil
		},
	})
	m := newMiniFixture(t, true)

	code, out := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:4242"})
	features, _ := out["features"].(map[string]any)
	if code != http.StatusOK || features["ping"] != true {
		t.Fatalf("sign-in: %d %v", code, out)
	}
	tok, _ := out["token"].(string)
	if code, out := m.call(http.MethodGet, "/api/mini/v1/ping", tok, nil); code != http.StatusOK || out["telegram_id"] != float64(4242) {
		t.Fatalf("the section's route: %d %v", code, out)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/ping", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("without a session: %d", code)
	}
	if code, out := m.call(http.MethodGet, "/api/mini/v1/me", tok, nil); code != http.StatusOK || out["features"].(map[string]any)["ping"] != true {
		t.Fatalf("/me: %d %v", code, out)
	}
	// Another customer is told it is not for them; a route nobody registered is a 404.
	tok2 := m.signIn("tg:77")
	if _, out := m.call(http.MethodGet, "/api/mini/v1/me", tok2, nil); out["features"].(map[string]any)["ping"] != false {
		t.Fatalf("another customer: %v", out)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/nothing-here", tok, nil); code != http.StatusNotFound {
		t.Fatalf("an unknown route: %d", code)
	}
}

// A section that cannot say what it shows fails the sign-in loudly instead of
// showing the customer a half-built app.
func TestMiniSectionFlagFailure(t *testing.T) {
	withMiniFeature(t, miniFeature{me: func(*Server, context.Context, *store.User, map[string]any) error { return errors.New("boom") }})
	m := newMiniFixture(t, true)
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": "tg:4242"}); code != http.StatusInternalServerError {
		t.Fatalf("sign-in with a failing section: %d", code)
	}
}
