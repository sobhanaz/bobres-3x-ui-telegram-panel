package server

import (
	"context"
	"strings"
	"sync"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// A dashboard link takes the origin when it is made, so links follow the
// HTTPS mode (the IP address once its certificate is trusted, back to the
// domain otherwise); without an origin there is no link.
func TestDashboardLinksFollowTheOrigin(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u, err := f.st.UpsertUser(ctx, f.st.Conn(), 9001, "owner", "en", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetUserRole(ctx, f.st.Conn(), u.ID, "owner", "active"); err != nil {
		t.Fatal(err)
	}
	srv := New(f.st, domain.New(f.st, domain.Config{}))
	link := func() string {
		t.Helper()
		l, err := srv.CreateDashboardLink(ctx, &corev1.CreateDashboardLinkRequest{ActorTelegramId: 9001})
		if err != nil {
			t.Fatal(err)
		}
		return l.GetUrl()
	}
	if got := link(); got != "" {
		t.Fatalf("a link without an origin: %q", got)
	}
	var (
		mu     sync.Mutex
		origin = "https://panel.example.com"
	)
	srv.SetPublicURLFunc(func(context.Context) string {
		mu.Lock()
		defer mu.Unlock()
		return origin
	})
	if got := link(); !strings.HasPrefix(got, "https://panel.example.com/admin/login#t=") {
		t.Fatalf("domain: %q", got)
	}
	mu.Lock()
	origin = "https://1.2.3.4"
	mu.Unlock()
	if got := link(); !strings.HasPrefix(got, "https://1.2.3.4/admin/login#t=") {
		t.Fatalf("IP: %q", got)
	}
	srv.SetPublicURL("https://bobres.localhost:8443")
	if got := link(); !strings.HasPrefix(got, "https://bobres.localhost:8443/admin/login#t=") {
		t.Fatalf("fixed: %q", got)
	}
}

// The bot learns where the Mini App opens from with every settings read: the
// store's public HTTPS address plus /app/, only while the app is on, and none
// for an address Telegram cannot reach.
func TestSettingsCarryTheMiniAppAddress(t *testing.T) {
	f := newFixtureWith(t, func(s *Server) { s.SetPublicURL("https://shop.example.com") })
	ctx := context.Background()
	if _, err := f.st.DB().Exec(ctx, `DELETE FROM core.settings WHERE key = 'miniapp.enabled'`); err != nil {
		t.Fatal(err)
	}
	c := f.client(t, botToken)
	url := func() string {
		t.Helper()
		s, err := c.GetSettings(ctx, &corev1.GetSettingsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return s.GetMiniappUrl()
	}
	if got := url(); got != "https://shop.example.com/app/" { // never switched: on by default
		t.Fatalf("by default: %q", got)
	}
	set := func(v string) {
		t.Helper()
		if _, err := f.st.DB().Exec(ctx, `INSERT INTO core.settings (key, value) VALUES ('miniapp.enabled', $1)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, `"`+v+`"`); err != nil {
			t.Fatal(err)
		}
	}
	set("false")
	if got := url(); got != "" {
		t.Fatalf("switched off: %q", got)
	}
	set("true")
	if got := url(); got != "https://shop.example.com/app/" {
		t.Fatalf("switched on: %q", got)
	}
}

func TestSettingsHaveNoMiniAppAddressWithoutAPublicOne(t *testing.T) {
	for name, origin := range map[string]string{"none": "", "plain http": "http://shop.example.com", "local": "https://bobres.localhost:8443", "private IP": "https://10.0.0.2"} {
		t.Run(name, func(t *testing.T) {
			f := newFixtureWith(t, func(s *Server) {
				if origin != "" {
					s.SetPublicURL(origin)
				}
			})
			s, err := f.client(t, botToken).GetSettings(context.Background(), &corev1.GetSettingsRequest{})
			if err != nil || s.GetMiniappUrl() != "" {
				t.Fatalf("%q: %q %v", origin, s.GetMiniappUrl(), err)
			}
		})
	}
}
