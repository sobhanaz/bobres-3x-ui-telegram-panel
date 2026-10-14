package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// The daily summary's two settings are on the Settings page (group reports),
// off at 09:00 until the store says otherwise, checked like the others, and the
// Reports page can ask whether it is on and when, in which zone.
func TestDailySummarySettingsAndStatus(t *testing.T) {
	f, b, _ := setupFixture(t)
	// The test saves general.timezone and the two settings; other tests insert
	// those keys themselves and must find none (the order tests run in is free).
	t.Cleanup(func() {
		if err := testdb.Truncate(context.Background(), f.st.DB(), `core.settings`); err != nil {
			t.Error(err)
		}
	})
	code, out := b.do(http.MethodGet, "/api/v1/settings", nil)
	if code != 200 || !strings.Contains(asJSON(out["groups"]), `"reports"`) {
		t.Fatalf("groups: %d %v", code, out["groups"])
	}
	on, at := settingByKey(out, "reports.daily_summary"), settingByKey(out, "reports.daily_summary_time")
	if on == nil || on["group"] != "reports" || on["kind"] != "bool" || on["default"] != "false" || on["value"] != "" || on["editable"] != true {
		t.Errorf("the switch: %v", on)
	}
	if at == nil || at["group"] != "reports" || at["kind"] != "time" || at["default"] != "09:00" || at["value"] != "" || at["editable"] != true {
		t.Errorf("the time: %v", at)
	}

	// Off by default, at 09:00 in Tehran's zone.
	code, out = b.do(http.MethodGet, "/api/v1/reports/daily-summary", nil)
	if code != 200 || out["enabled"] != false || out["time"] != "09:00" || out["tz"] != "Asia/Tehran" {
		t.Fatalf("status by default: %d %v", code, out)
	}

	// Typed as people type it, stored as HH:MM; the zone follows the store's.
	code, out = b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{
		"reports.daily_summary": "yes", "reports.daily_summary_time": "٧:۳۰", "general.timezone": "America/New_York"}})
	if code != 200 || settingByKey(out, "reports.daily_summary")["value"] != "true" || settingByKey(out, "reports.daily_summary_time")["value"] != "07:30" {
		t.Fatalf("put: %d %v", code, out)
	}
	if code, out = b.do(http.MethodGet, "/api/v1/reports/daily-summary", nil); code != 200 || out["enabled"] != true || out["time"] != "07:30" || out["tz"] != "America/New_York" {
		t.Fatalf("status: %d %v", code, out)
	}
	if e := lastAudit(t, b, "entity_id=reports.daily_summary_time"); e["action"] != "setting.set" || asJSON(e["after"]) != `{"value":"07:30"}` {
		t.Errorf("audit: %v", e)
	}

	// A time that is not a time of day is refused, naming the field, and nothing changes.
	for _, bad := range []string{"24:00", "9", "09:60", "soon", "9:5"} {
		if code, out := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"reports.daily_summary_time": bad}}); code != 400 || out["field"] != "reports.daily_summary_time" {
			t.Errorf("%q: %d %v", bad, code, out)
		}
	}
	if code, out = b.do(http.MethodGet, "/api/v1/reports/daily-summary", nil); code != 200 || out["time"] != "07:30" {
		t.Errorf("after refused values: %d %v", code, out)
	}
	// Back to the defaults: an empty value.
	if code, out = b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"reports.daily_summary": "", "reports.daily_summary_time": ""}}); code != 200 {
		t.Fatalf("reset: %d %v", code, out)
	}
	if code, out = b.do(http.MethodGet, "/api/v1/reports/daily-summary", nil); code != 200 || out["enabled"] != false || out["time"] != "09:00" {
		t.Errorf("status after the reset: %d %v", code, out)
	}

	// Support may do neither: no settings page, no summary status.
	f.staffUser(8701, "support")
	sup := f.browser()
	if sup.loginWithLink(8701) != 200 {
		t.Fatal("login")
	}
	if code, _ := sup.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"reports.daily_summary": "true"}}); code != 403 {
		t.Errorf("support turned it on: %d", code)
	}
	if code, _ := sup.do(http.MethodGet, "/api/v1/reports/daily-summary", nil); code != 403 {
		t.Errorf("support read the status: %d", code)
	}
}
