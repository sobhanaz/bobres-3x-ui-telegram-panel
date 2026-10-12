package web

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// GET /api/v1/system lists the watchdog's open problems: announced ones
// only, critical first, each with the bot's text key, its values, the
// English title, the detail and since when; [] when all is well. Zarinpal's
// error stays with the owner, as on the owner-only gateway pages.
func TestSystemOpenAlerts(t *testing.T) {
	f, owner, _ := licenseFixture(t)
	ctx := context.Background()
	if _, err := f.st.DB().Exec(ctx, `TRUNCATE core.alert_state`); err != nil {
		t.Fatal(err)
	}
	f.staffUser(8931, "admin")
	adm := f.browser()
	adm.loginWithLink(8931)

	if code, out := owner.do(http.MethodGet, "/api/v1/system", nil); code != 200 || asJSON(out["alerts"]) != "[]" {
		t.Fatalf("all clear: %d %v", code, out["alerts"])
	}

	since := time.Unix(1760000000, 0)
	for _, a := range []*store.AlertState{
		{Key: "backup.failed", State: store.AlertProblem, Since: since.Add(-time.Hour), Severity: "warning",
			TitleKey: "alert.backup_failed", Detail: "pg_dump: connection refused"},
		{Key: "server.s1", State: store.AlertProblem, Since: since, Severity: "critical", TitleKey: "alert.server_down",
			Args: map[string]string{"server": "DE <1>"}, Detail: "dial tcp 10.0.0.5:443"},
		{Key: "gateway.zarinpal.error", State: store.AlertProblem, Since: since.Add(time.Minute), Severity: "critical",
			TitleKey: "alert.zarinpal_failing", Detail: "verify: merchant 1234 refused (-11)"},
		{Key: "service.bot", State: store.AlertPending, Since: since, Severity: "critical", TitleKey: "alert.service_down",
			Args: map[string]string{"service": "bot"}},
		{Key: "service.core", State: store.AlertOK, Since: since, Severity: "critical", TitleKey: "alert.service_down"},
	} {
		if err := f.st.PutAlertState(ctx, f.st.Conn(), a); err != nil {
			t.Fatal(err)
		}
	}
	want := func(zarinpalDetail string) []any {
		return []any{
			map[string]any{"key": "server.s1", "severity": "critical", "title_key": "alert.server_down",
				"args": map[string]any{"server": "DE <1>"}, "title": "The 3x-ui server DE <1> does not answer.",
				"detail": "dial tcp 10.0.0.5:443", "since": float64(1760000000)},
			map[string]any{"key": "gateway.zarinpal.error", "severity": "critical", "title_key": "alert.zarinpal_failing",
				"args": map[string]any{}, "title": "Zarinpal payments are failing.", "detail": zarinpalDetail,
				"since": float64(1760000060)},
			map[string]any{"key": "backup.failed", "severity": "warning", "title_key": "alert.backup_failed",
				"args": map[string]any{}, "title": "The last backup failed.", "detail": "pg_dump: connection refused",
				"since": float64(1759996400)},
		}
	}
	for name, c := range map[string]struct {
		b    *browser
		want []any
	}{
		"owner": {owner, want("verify: merchant 1234 refused (-11)")},
		"admin": {adm, want("")},
	} {
		if code, out := c.b.do(http.MethodGet, "/api/v1/system", nil); code != 200 || !reflect.DeepEqual(out["alerts"], c.want) {
			t.Fatalf("%s: %d\n got %v\nwant %v", name, code, out["alerts"], c.want)
		}
	}
}
