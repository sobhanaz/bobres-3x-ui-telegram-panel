package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// The Top users board's switch is on the Settings page (group growth), on until
// the store says otherwise, checked like the other switches, and the store's
// staff who may change settings can turn it off.
func TestLeaderboardSettingOnTheSettingsPage(t *testing.T) {
	f, b, _ := setupFixture(t)
	t.Cleanup(func() {
		if err := testdb.Truncate(context.Background(), f.st.DB(), `core.settings`); err != nil {
			t.Error(err)
		}
	})
	code, out := b.do(http.MethodGet, "/api/v1/settings", nil)
	if code != 200 || !strings.Contains(asJSON(out["groups"]), `"growth"`) {
		t.Fatalf("groups: %d %v", code, out["groups"])
	}
	on := settingByKey(out, "growth.leaderboard.enabled")
	if on == nil || on["group"] != "growth" || on["kind"] != "bool" || on["default"] != "true" || on["value"] != "" || on["editable"] != true {
		t.Fatalf("the switch: %v", on)
	}

	code, out = b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"growth.leaderboard.enabled": "off"}})
	if code != 200 || settingByKey(out, "growth.leaderboard.enabled")["value"] != "false" {
		t.Fatalf("turned off: %d %v", code, out)
	}
	if e := lastAudit(t, b, "entity_id=growth.leaderboard.enabled"); e["action"] != "setting.set" || asJSON(e["after"]) != `{"value":"false"}` {
		t.Errorf("audit: %v", e)
	}
	if code, out := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"growth.leaderboard.enabled": "maybe"}}); code != 400 || out["field"] != "growth.leaderboard.enabled" {
		t.Errorf("a value that is no switch: %d %v", code, out)
	}
	code, out = b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"growth.leaderboard.enabled": ""}})
	if code != 200 || settingByKey(out, "growth.leaderboard.enabled")["value"] != "" {
		t.Fatalf("back to the default: %d %v", code, out)
	}

	// Support staff do not change settings.
	f.staffUser(8711, "support")
	sup := f.browser()
	if sup.loginWithLink(8711) != 200 {
		t.Fatal("login")
	}
	if code, _ := sup.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"growth.leaderboard.enabled": "false"}}); code != 403 {
		t.Errorf("support turned it off: %d", code)
	}
}
