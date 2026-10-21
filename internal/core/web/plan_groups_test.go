package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The plan groups of the dashboard: any staff who may edit plans manages them, a plan is put into a group from its editor,
// an edit that does not name a group keeps it, and deleting a group frees its plans.
func TestPlanGroupsEndpoints(t *testing.T) {
	f, b, _, _ := salesFixture(t)

	// Nobody without plans.write.
	f.staffUser(8941, "support")
	sup := f.browser()
	sup.loginWithLink(8941)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/plan-groups"}, {http.MethodPost, "/api/v1/plan-groups"},
		{http.MethodPut, "/api/v1/plan-groups/00000000-0000-0000-0000-000000000000"}, {http.MethodDelete, "/api/v1/plan-groups/00000000-0000-0000-0000-000000000000"},
	} {
		if code, _ := sup.do(c.method, c.path, map[string]any{}); code != http.StatusForbidden {
			t.Errorf("support %s %s: %d, want 403", c.method, c.path, code)
		}
	}

	// A group needs a name; the text is cut off nowhere but at the limit.
	if code, _ := b.do(http.MethodPost, "/api/v1/plan-groups", map[string]any{"name": map[string]string{}}); code != http.StatusBadRequest {
		t.Fatalf("a group with no name: %d", code)
	}
	if code, _ := b.do(http.MethodPost, "/api/v1/plan-groups", map[string]any{"name": map[string]string{"en": "G"},
		"description": map[string]string{"en": strings.Repeat("x", 1501)}}); code != http.StatusBadRequest {
		t.Fatalf("a text over the limit: %d", code)
	}
	code, out := b.do(http.MethodPost, "/api/v1/plan-groups", map[string]any{
		"name": map[string]string{"fa": " شروع ", "en": "Starter"}, "description": map[string]string{"en": "For light use", "fa": ""}, "sort": 2})
	g, _ := out["group"].(map[string]any)
	if code != 200 || g["id"] == "" || g["sort"] != float64(2) {
		t.Fatalf("create group: %d %v", code, out)
	}
	gid := g["id"].(string)
	if name := g["name"].(map[string]any); name["fa"] != "شروع" || g["description"].(map[string]any)["fa"] != nil {
		t.Fatalf("names are trimmed and an empty text is not kept: %v", g)
	}

	// A plan goes into the group from its editor; the list shows it; the group counts it.
	plan := map[string]any{"name": map[string]string{"en": "Monthly"}, "kind": "time", "duration_days": 30, "price": "120,000", "currency": "IRT",
		"enabled": true, "group_id": gid}
	code, out = b.do(http.MethodPost, "/api/v1/plans", plan)
	saved, _ := out["plan"].(map[string]any)
	if code != 200 || saved["group_id"] != gid {
		t.Fatalf("a plan in the group: %d %v", code, out)
	}
	planID := saved["id"].(string)
	if code, out = b.do(http.MethodGet, "/api/v1/plan-groups", nil); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if items, _ := out["items"].([]any); len(items) != 1 || items[0].(map[string]any)["plans"] != float64(1) {
		t.Fatalf("the group's plans: %v", out)
	}

	// An edit with no group_id (the on/off switch sends none) keeps the group; "" takes the plan out of it; a group that
	// does not exist is refused.
	delete(plan, "group_id")
	plan["enabled"] = false
	if code, out = b.do(http.MethodPut, "/api/v1/plans/"+planID, plan); code != 200 || out["plan"].(map[string]any)["group_id"] != gid {
		t.Fatalf("an edit without a group dropped it: %d %v", code, out)
	}
	plan["group_id"] = "00000000-0000-7000-8000-0000000007ff"
	if code, _ = b.do(http.MethodPut, "/api/v1/plans/"+planID, plan); code != http.StatusBadRequest {
		t.Fatalf("a group that does not exist: %d", code)
	}
	plan["group_id"] = "not an id"
	if code, _ = b.do(http.MethodPut, "/api/v1/plans/"+planID, plan); code != http.StatusBadRequest {
		t.Fatalf("a bad group id: %d", code)
	}
	plan["group_id"] = ""
	if code, out = b.do(http.MethodPut, "/api/v1/plans/"+planID, plan); code != 200 || out["plan"].(map[string]any)["group_id"] != nil {
		t.Fatalf("an empty group id: %d %v", code, out)
	}
	plan["group_id"] = gid
	plan["enabled"] = true
	if code, _ = b.do(http.MethodPut, "/api/v1/plans/"+planID, plan); code != 200 {
		t.Fatalf("back into the group: %d", code)
	}

	// Edit the group; an unknown one is a 404.
	if code, out = b.do(http.MethodPut, "/api/v1/plan-groups/"+gid, map[string]any{"name": map[string]string{"en": "Starter 2"}}); code != 200 ||
		out["group"].(map[string]any)["name"].(map[string]any)["en"] != "Starter 2" {
		t.Fatalf("edit group: %d %v", code, out)
	}
	if code, _ = b.do(http.MethodPut, "/api/v1/plan-groups/00000000-0000-7000-8000-0000000007ff", map[string]any{"name": map[string]string{"en": "X"}}); code != http.StatusNotFound {
		t.Fatalf("edit a group that is not there: %d", code)
	}

	// Deleting frees the plan (it stays, on sale); the audit log names it.
	if code, _ = b.do(http.MethodDelete, "/api/v1/plan-groups/"+gid, nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ = b.do(http.MethodDelete, "/api/v1/plan-groups/"+gid, nil); code != http.StatusNotFound {
		t.Fatalf("delete twice: %d", code)
	}
	code, out = b.do(http.MethodGet, "/api/v1/plans", nil)
	var after map[string]any // other tests of the package leave plans in the store: find this one
	items, _ := out["items"].([]any)
	for _, it := range items {
		if m, _ := it.(map[string]any); m["id"] == planID {
			after = m
		}
	}
	if code != 200 || after == nil || after["group_id"] != nil || after["enabled"] != true {
		t.Fatalf("the plan after its group was deleted: %v", after)
	}
	var n int
	if err := f.st.DB().QueryRow(t.Context(), `SELECT count(*) FROM core.audit_log WHERE entity_id = $1 AND action IN ('plan_group.upsert', 'plan_group.delete')`, gid).Scan(&n); err != nil || n != 3 {
		t.Fatalf("audit entries %d %v", n, err)
	}
}

// The Mini App's plans carry the groups and each plan's group, so a customer picks a group and reads its text first.
func TestMiniPlansCarryTheGroups(t *testing.T) {
	m := newMiniReseller(t)
	tok, _ := m.reseller("grp", 0)
	ctx := t.Context()
	g, err := m.dom.UpsertPlanGroup(ctx, actor0(m.fixture), &store.PlanGroup{NameI18n: map[string]string{"en": "Starter"}, DescriptionI18n: map[string]string{"en": "For light use"}, Sort: 1})
	if err != nil {
		t.Fatal(err)
	}
	p := *m.plan
	p.GroupID = &g.ID
	if _, err := m.dom.UpsertPlan(ctx, actor0(m.fixture), &p); err != nil {
		t.Fatal(err)
	}
	_, out := m.call(http.MethodGet, "/api/mini/v1/plans", tok, nil)
	groups := jlist(out, "groups")
	if len(groups) != 1 || groups[0]["id"] != g.ID || groups[0]["names"].(map[string]any)["en"] != "Starter" || groups[0]["texts"].(map[string]any)["en"] != "For light use" {
		t.Fatalf("groups: %v", out["groups"])
	}
	var seen map[string]any
	for _, pl := range jlist(out, "plans") {
		if pl["id"] == m.plan.ID {
			seen = pl
		}
	}
	if seen == nil || seen["group"] != g.ID {
		t.Fatalf("the plan's group: %v", seen)
	}
}
