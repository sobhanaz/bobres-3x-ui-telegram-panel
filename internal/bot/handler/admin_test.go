package handler

import (
	"context"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
)

// The admin screen sums up the servers taking new services: how many are
// healthy, and the others by name (escaped; the first few, then how many
// more). No server at all is told apart from servers all switched off.
func TestServersLine(t *testing.T) {
	cat := i18n.MustLoad()
	down := []string{"FR", "<NL>"}
	many := []string{"a", "b", "c", "d", "e", "f", "g"}
	for _, c := range []struct {
		name, lang string
		st         *corev1.AdminStats
		want       string
	}{
		{"provisioner down", "en", &corev1.AdminStats{PanelDetail: "provisioner unreachable"}, "unreachable ❌ provisioner unreachable"},
		{"no server yet", "en", &corev1.AdminStats{ServersKnown: true}, "none added yet ❌ Add a 3x-ui server in the dashboard."},
		{"no server yet, Persian", "fa", &corev1.AdminStats{ServersKnown: true},
			"هنوز سروری اضافه نشده ❌ از داشبورد یک سرور 3x-ui اضافه کنید."},
		{"none on", "en", &corev1.AdminStats{ServersKnown: true, ServersListed: 2}, "none takes new services ❌"},
		{"all healthy", "en", &corev1.AdminStats{ServersKnown: true, ServersTotal: 3, ServersHealthy: 3}, "3/3 healthy ✅"},
		{"some down", "en", &corev1.AdminStats{ServersKnown: true, ServersTotal: 3, ServersHealthy: 1, ServersDown: down},
			"1/3 healthy ❌ Not answering: <b>FR</b> · <b>&lt;NL&gt;</b>"},
		{"many down", "en", &corev1.AdminStats{ServersKnown: true, ServersTotal: 8, ServersHealthy: 1, ServersDown: many},
			"1/8 healthy ❌ Not answering: <b>a</b> · <b>b</b> · <b>c</b> · <b>d</b> · <b>e</b> · +2 more"},
		{"some down, Persian", "fa", &corev1.AdminStats{ServersKnown: true, ServersTotal: 3, ServersHealthy: 1, ServersDown: down},
			"۱ از ۳ سالم ❌ بی\u200cپاسخ: <b>FR</b> · <b>&lt;NL&gt;</b>"},
	} {
		r := &req{h: &Handler{cat: cat}, lang: c.lang}
		if got := serversLine(r, c.st); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The admin screen reads the provisioner's server list through core, so a
// server whose last health check failed is named; one not checked yet
// counts as healthy, as on the dashboard.
func TestAdminScreenShowsServerHealth(t *testing.T) {
	w := newWorld(t)
	owner := w.person(ownerID, "boss")
	owner.text("/start")
	owner.press("lang:en")
	owner.text("/admin")
	if got := owner.last().text; !strings.Contains(got, "Servers: 1/1 healthy ✅") {
		t.Fatalf("admin screen: %q", got)
	}
	if _, err := w.stack.Store.DB().Exec(context.Background(), `UPDATE provisioner.xui_servers
		SET last_health_ok = false, last_error = 'connection refused', last_checked_at = now()`); err != nil {
		t.Fatal(err)
	}
	owner.text("/admin")
	if got := owner.last().text; !strings.Contains(got, "Servers: 0/1 healthy ❌ Not answering: <b>default</b>") {
		t.Fatalf("admin screen with the panel down: %q", got)
	}

	db := w.stack.Store.DB()
	if _, err := db.Exec(context.Background(), `UPDATE provisioner.xui_servers SET enabled = false`); err != nil {
		t.Fatal(err)
	}
	owner.text("/admin")
	if got := owner.last().text; !strings.Contains(got, "Servers: none takes new services ❌") {
		t.Fatalf("admin screen with the server switched off: %q", got)
	}
	if _, err := db.Exec(context.Background(), `DELETE FROM provisioner.xui_servers`); err != nil {
		t.Fatal(err)
	}
	owner.text("/admin")
	if got := owner.last().text; !strings.Contains(got, "Servers: none added yet ❌") {
		t.Fatalf("admin screen with no server: %q", got)
	}
}
