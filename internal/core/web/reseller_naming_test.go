package web

import (
	"net/http"
	"testing"
)

// What a reseller's configs are called and grouped as in the 3x-ui panel, from the dashboard: the owner writes the group name when
// making the reseller (an admin may not), sets or changes it and the prefix later, and has the existing configs moved again; the
// reseller's page shows all of it.
func TestResellerNamingAPI(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	code, out := b.do(http.MethodPost, "/api/v1/reseller-tiers", map[string]any{"name": "Gold", "discount_percent": 20, "commission_percent": 10})
	if code != http.StatusCreated {
		t.Fatalf("tier: %d %v", code, out)
	}
	tier := item(out)["id"].(string)
	shop, other, third := f.customer(8641, "shop"), f.customer(8642, "other"), f.customer(8643, "third")
	body := func(user, group string) map[string]any {
		m := map[string]any{"user_id": user, "tier_id": tier, "credit_limits": []map[string]string{}, "reason": "trusted"}
		if group != "" {
			m["panel_group"] = group
		}
		return m
	}

	// An admin makes resellers but does not give them a group name.
	f.staffUser(8644, "admin")
	adm := f.browser()
	adm.loginWithLink(8644)
	if code, _ := adm.do(http.MethodPost, "/api/v1/resellers", body(third.ID, "ADMINS")); code != http.StatusForbidden {
		t.Fatalf("an admin giving a group name: %d", code)
	}
	if code, out := adm.do(http.MethodPost, "/api/v1/resellers", body(third.ID, "")); code != http.StatusCreated || item(out)["panel_group"] != "" {
		t.Fatalf("an admin making a reseller: %d %v", code, out)
	}
	if code, _ := adm.do(http.MethodPut, "/api/v1/resellers/"+third.ID+"/naming", map[string]any{"config_prefix": "adm"}); code != http.StatusForbidden {
		t.Fatalf("an admin's prefix: %d", code)
	}
	if code, _ := adm.do(http.MethodPost, "/api/v1/resellers/"+third.ID+"/group-sync", map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("an admin's sync request: %d", code)
	}

	// The owner writes it when making the reseller (any case, with or without BOBRES-).
	for _, bad := range []string{"a", "has space", "BOBRES-"} {
		if code, out := b.do(http.MethodPost, "/api/v1/resellers", body(shop.ID, bad)); code != http.StatusBadRequest || out["field"] != "panel_group" {
			t.Errorf("group %q: %d %v", bad, code, out)
		}
	}
	code, out = b.do(http.MethodPost, "/api/v1/resellers", body(shop.ID, " bobres-Sobhanadmin "))
	it := item(out)
	if code != http.StatusCreated || it["panel_group"] != "SOBHANADMIN" || it["panel_group_full"] != "BOBRES-SOBHANADMIN" || it["config_prefix"] != "" || it["groups_synced"] != false {
		t.Fatalf("make: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/resellers", body(other.ID, "SOBHANADMIN")); code != http.StatusBadRequest || out["field"] != "panel_group" {
		t.Fatalf("a group name another reseller has: %d %v", code, out)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/resellers", body(other.ID, "")); code != http.StatusCreated {
		t.Fatalf("make without a group: %d %v", code, out)
	}

	// Later: the owner sets, changes and takes away the group and the prefix.
	put := func(user string, m map[string]any) (int, map[string]any) {
		return b.do(http.MethodPut, "/api/v1/resellers/"+user+"/naming", m)
	}
	if code, out := put(other.ID, map[string]any{}); code != http.StatusBadRequest {
		t.Errorf("nothing to change: %d %v", code, out)
	}
	if code, out := put(other.ID, map[string]any{"panel_group": "SOBHANADMIN"}); code != http.StatusBadRequest || out["field"] != "panel_group" {
		t.Errorf("a taken group name: %d %v", code, out)
	}
	if code, out := put(other.ID, map[string]any{"config_prefix": "u12"}); code != http.StatusBadRequest || out["field"] != "config_prefix" {
		t.Errorf("a bad prefix: %d %v", code, out)
	}
	code, out = put(other.ID, map[string]any{"panel_group": "sara", "config_prefix": "Sara"})
	it = item(out)
	if code != http.StatusOK || it["panel_group"] != "SARA" || it["config_prefix"] != "sara" || it["panel_group_full"] != "BOBRES-SARA" {
		t.Fatalf("set: %d %v", code, out)
	}
	if code, out := put(shop.ID, map[string]any{"config_prefix": "sara"}); code != http.StatusBadRequest || out["field"] != "config_prefix" {
		t.Errorf("a prefix another reseller has: %d %v", code, out)
	}
	code, out = put(other.ID, map[string]any{"panel_group": ""})
	if it = item(out); code != http.StatusOK || it["panel_group"] != "" || it["panel_group_full"] != "" || it["config_prefix"] != "sara" {
		t.Fatalf("take the group away: %d %v", code, out)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/resellers/"+other.ID+"/group-sync", map[string]any{}); code != http.StatusBadRequest {
		t.Errorf("a sync request without a group: %d", code)
	}
	if code, out := b.do(http.MethodPost, "/api/v1/resellers/"+shop.ID+"/group-sync", map[string]any{}); code != http.StatusOK || item(out)["groups_synced"] != false {
		t.Errorf("a sync request: %d %v", code, out)
	}
	if code, _ := put("00000000-0000-0000-0000-000000000099", map[string]any{"config_prefix": "zz"}); code != http.StatusNotFound {
		t.Errorf("an unknown reseller: %d", code)
	}
	// The page shows it (GET too).
	if code, out := b.do(http.MethodGet, "/api/v1/resellers/"+shop.ID, nil); code != http.StatusOK || item(out)["panel_group"] != "SOBHANADMIN" {
		t.Errorf("the reseller's page: %d %v", code, out)
	}
}
