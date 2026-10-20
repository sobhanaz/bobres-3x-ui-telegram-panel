package web

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The Mini App in maintenance mode follows the bot's rule: customers get the
// maintenance answer to every call but the ones a payment already started
// needs, staff and the owners the server names are never held, and the
// dashboard and the service pages are not the store. These tests read the
// routes from the source, so a section added later is swept without being
// listed here, and fails until it is held (or, if a payment under way needs
// it, named in miniServedInMaintenance with the reason).

// miniServedInMaintenance are the calls a customer may make while the store is
// in maintenance: who is signed in, and what a payment already started needs
// (its order, its check, its proof, and the list of those waiting for one).
var miniServedInMaintenance = []string{
	"GET /api/mini/v1/me",
	"POST /api/mini/v1/logout",
	"GET /api/mini/v1/orders/{id}",
	"POST /api/mini/v1/payments/{id}/check",
	"POST /api/mini/v1/payments/{id}/proof",
	"GET /api/mini/v1/payments/pending",
}

// miniRoute is a call of the Mini App as the source registers it.
type miniRoute struct {
	pattern       string // "GET /api/mini/v1/me"
	wrapped       bool   // goes through miniSession
	inMaintenance bool   // miniSession's first argument
}

// miniRoutesInSource reads every api.HandleFunc of the Mini App's files.
func miniRoutesInSource(t *testing.T) []miniRoute {
	t.Helper()
	files, err := filepath.Glob("miniapp*.go")
	if err != nil {
		t.Fatal(err)
	}
	var routes []miniRoute
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "HandleFunc" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			pattern, err := strconv.Unquote(lit.Value)
			if err != nil || !strings.Contains(pattern, "/api/mini/v1/") {
				return true
			}
			r := miniRoute{pattern: pattern}
			if wrap, ok := call.Args[1].(*ast.CallExpr); ok && len(wrap.Args) == 2 {
				if sel, ok := wrap.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "miniSession" {
					flag, _ := wrap.Args[0].(*ast.Ident)
					r.wrapped, r.inMaintenance = true, flag != nil && flag.Name == "true"
				}
			}
			routes = append(routes, r)
			return true
		})
	}
	if len(routes) < 20 {
		t.Fatalf("only %d routes found in the source", len(routes))
	}
	return routes
}

// Every call of the Mini App but the sign-in goes through miniSession, which
// holds a customer in maintenance, and the calls it lets through are the
// documented ones and no others.
func TestMiniAppRoutesAreHeldInMaintenance(t *testing.T) {
	var served []string
	for _, r := range miniRoutesInSource(t) {
		switch {
		case r.pattern == "POST /api/mini/v1/auth":
			// The sign-in: it says whether the store is in maintenance (the app shows the screen).
		case !strings.Contains(r.pattern, " "):
			// The fallback of unknown addresses (404).
		case !r.wrapped:
			t.Errorf("%s does not go through miniSession, so maintenance does not hold it", r.pattern)
		case r.inMaintenance:
			served = append(served, r.pattern)
		}
	}
	slices.Sort(served)
	want := slices.Sorted(slices.Values(miniServedInMaintenance))
	if !slices.Equal(served, want) {
		t.Errorf("served in maintenance:\n got %q\nwant %q", served, want)
	}
}

var braces = regexp.MustCompile(`\{[a-z]+\}`)

// A customer of a store in maintenance gets the maintenance answer from every
// call but the allowed ones; staff and the configured owner are served by all
// of them; the flag in /me and in the sign-in says the same; and when the
// store opens again everything is served.
func TestMiniAppMaintenanceHoldsEveryRoute(t *testing.T) {
	m := newMiniFixture(t, true)
	routes := miniRoutesInSource(t)
	var swept []miniRoute
	for _, r := range routes {
		if r.wrapped && r.pattern != "POST /api/mini/v1/logout" { // logout ends the session the sweep uses
			swept = append(swept, r)
		}
	}
	held := func(code int, out map[string]any) bool {
		return code == http.StatusServiceUnavailable && out["error"] == "maintenance"
	}
	request := func(r miniRoute) (method, path string, body any) {
		method, path, _ = strings.Cut(r.pattern, " ")
		path = braces.ReplaceAllString(path, miniIntentID)
		if method != http.MethodGet {
			body = map[string]any{}
		}
		return method, path, body
	}

	// Signed in before maintenance, as customers with the app open are, and
	// one with a configured owner's Telegram id and no role.
	customers := make([]string, len(swept))
	for i := range swept {
		customers[i] = m.signIn(fmt.Sprintf("tg:%d:c%d", 40000+i, i))
	}
	m.sql(`UPDATE core.users SET role = 'user' WHERE telegram_id = $1`, ownerTG)
	staff := map[string]string{
		"owner the server names": m.signIn(fmt.Sprintf("tg:%d:named", ownerTG)),
		"owner":                  m.signIn("tg:40901:owner"),
		"admin":                  m.signIn("tg:40902:admin"),
		"support":                m.signIn("tg:40903:support"),
	}
	for tg, role := range map[int64]string{40901: "owner", 40902: "admin", 40903: "support"} {
		m.sql(`UPDATE core.users SET role = $2 WHERE telegram_id = $1`, tg, role)
	}

	m.setting("maintenance.enabled", "true")
	t.Cleanup(func() { m.setting("maintenance.enabled", "false") })

	for i, r := range swept {
		method, path, body := request(r)
		code, out := m.call(method, path, customers[i], body)
		switch isServed := slices.Contains(miniServedInMaintenance, r.pattern); {
		case !isServed && !held(code, out):
			t.Errorf("a customer: %s answered %d %v, want 503 maintenance", r.pattern, code, out)
		case isServed && held(code, out):
			t.Errorf("a customer: %s is held in maintenance, but a payment under way needs it", r.pattern)
		}
	}
	for who, token := range staff {
		for _, r := range swept {
			method, path, body := request(r)
			if code, out := m.call(method, path, token, body); held(code, out) {
				t.Errorf("%s: %s was held in maintenance", who, r.pattern)
			}
		}
		code, out := m.call(http.MethodGet, "/api/mini/v1/me", token, nil)
		if code != http.StatusOK || out["maintenance"] != false {
			t.Errorf("%s: /me in maintenance: %d %v", who, code, out)
		}
	}

	// The flag, from /me and from the sign-in, and the sign-in itself.
	for tg, want := range map[int]bool{50001: true, 40902: false, ownerTG: false} {
		code, out := m.call(http.MethodPost, "/api/mini/v1/auth", "", map[string]any{"init_data": fmt.Sprintf("tg:%d", tg)})
		if code != http.StatusOK || out["maintenance"] != want || out["token"] == nil {
			t.Errorf("sign-in of %d in maintenance: %d %v, want the flag %v", tg, code, out["maintenance"], want)
		}
	}

	// The store opens again: the same customers are served.
	m.setting("maintenance.enabled", "false")
	for _, path := range []string{"/api/mini/v1/plans", "/api/mini/v1/wallet", "/api/mini/v1/services"} {
		if code, out := m.call(http.MethodGet, path, customers[0], nil); held(code, out) || code != http.StatusOK {
			t.Errorf("%s after maintenance: %d %v", path, code, out)
		}
	}
	if _, out := m.call(http.MethodGet, "/api/mini/v1/me", customers[0], nil); out["maintenance"] != false {
		t.Errorf("/me after maintenance: %v", out)
	}
}

// The dashboard is for staff and a service's own page is the customer's link
// to their service: maintenance closes the store, not those. The owner can
// still log in and switch it off, a customer still has no dashboard, and an
// active service's page still opens.
func TestMaintenanceLeavesTheDashboardAndServicePagesOpen(t *testing.T) {
	f := newPageFixture(t)
	if _, err := f.dom.SetSettings(context.Background(), actor0(f.fixture), map[string]string{"maintenance.enabled": "true"}); err != nil {
		t.Fatal(err)
	}

	// The owner who logged in before, and staff who log in now.
	if code, out := f.b.do(http.MethodGet, "/api/v1/overview", nil); code != http.StatusOK {
		t.Fatalf("overview in maintenance: %d %v", code, out)
	}
	for tg, role := range map[int64]string{7301: "admin", 7302: "support"} {
		f.staffUser(tg, role)
		b := f.browser()
		if code := b.loginWithLink(tg); code != http.StatusOK {
			t.Fatalf("%s logging in during maintenance: %d", role, code)
		}
		if code, out := b.do(http.MethodGet, "/api/v1/me", nil); code != http.StatusOK {
			t.Errorf("%s: /me in maintenance: %d %v", role, code, out)
		}
	}
	// A customer has no dashboard, in maintenance or out of it.
	f.staffUser(7303, "user")
	if _, _, err := f.dom.CreateLoginLink(context.Background(), 7303); err == nil {
		t.Error("a customer got a dashboard login link in maintenance")
	}
	if code, _ := f.browser().do(http.MethodGet, "/api/v1/users", nil); code != http.StatusUnauthorized {
		t.Errorf("the staff API without a session in maintenance: %d", code)
	}
	// The page of an active service.
	if code, _, body := f.page("/sub/" + f.token + "?l=en"); code != http.StatusOK || !strings.Contains(body, "Active") {
		t.Errorf("the service page in maintenance: %d", code)
	}
	// The owner switches maintenance off from the dashboard.
	if code, out := f.b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"maintenance.enabled": "false"}}); code != http.StatusOK {
		t.Fatalf("switching maintenance off: %d %v", code, out)
	}
	if got, err := f.dom.Settings(context.Background()); err != nil || got["maintenance.enabled"] != "false" {
		t.Errorf("maintenance after the owner switched it off: %q %v", got["maintenance.enabled"], err)
	}
}
