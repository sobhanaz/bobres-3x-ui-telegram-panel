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
