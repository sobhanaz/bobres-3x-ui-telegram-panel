package web

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// A plan's reward (the discount code a paid purchase gives its buyer) is set from the plan editor as an object: absent keeps it, kind "none"
// takes it away, a percent from 1 to 90 or an amount in the plan's currency sets it with the days it lasts and a text for the buyer, anything
// else is refused, and a trial or a traffic package never has one.
func TestPlanRewardAPI(t *testing.T) {
	_, b, _, _ := salesFixture(t)
	plan := map[string]any{"name": map[string]string{"en": "Unlimited"}, "kind": "time", "duration_days": 30, "price": "750,000", "currency": "IRT", "enabled": true,
		"reward": map[string]any{"kind": "amount", "value": "260,000", "days": 10, "text": map[string]string{"fa": "برای پلن حجمی", "en": "For a volume plan"}}}
	code, out := b.do(http.MethodPost, "/api/v1/plans", plan)
	saved, _ := out["plan"].(map[string]any)
	reward, _ := saved["reward"].(map[string]any)
	amount, _ := reward["amount"].(map[string]any)
	text, _ := reward["text"].(map[string]any)
	if code != http.StatusOK || reward["kind"] != "amount" || amount["amount"] != float64(260000) || amount["currency"] != "IRT" || reward["days"] != float64(10) ||
		text["en"] != "For a volume plan" || reward["percent"] != nil {
		t.Fatalf("create with an amount reward: %d %v", code, out)
	}
	id := saved["id"].(string)
	// An edit with none (the on/off switch sends none) keeps it.
	delete(plan, "reward")
	plan["enabled"] = false
	if code, out = b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != http.StatusOK || out["plan"].(map[string]any)["reward"].(map[string]any)["kind"] != "amount" {
		t.Fatalf("an edit without it: %d %v", code, out)
	}
	for name, bad := range map[string]map[string]any{
		"a percent over 90":  {"kind": "percent", "value": "91", "days": 10},
		"a percent of 0":     {"kind": "percent", "value": "0", "days": 10},
		"a percent as words": {"kind": "percent", "value": "ten", "days": 10},
		"an amount of 0":     {"kind": "amount", "value": "0", "days": 10},
		"an amount as words": {"kind": "amount", "value": "lots", "days": 10},
		"a kind of its own":  {"kind": "gold", "value": "5", "days": 10},
	} {
		plan["reward"] = bad
		if code, out := b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	plan["reward"] = map[string]any{"kind": "amount", "value": "5", "days": 366}
	if code, out := b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != http.StatusBadRequest || out["field"] != "reward_days" {
		t.Errorf("366 days: %d %v", code, out)
	}
	plan["reward"] = map[string]any{"kind": "percent", "value": "30", "days": 0}
	if code, out = b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != http.StatusOK {
		t.Fatalf("change: %d %v", code, out)
	}
	if r := out["plan"].(map[string]any)["reward"].(map[string]any); r["kind"] != "percent" || r["percent"] != float64(30) || r["days"] != float64(10) || r["amount"] != nil {
		t.Fatalf("a percent, with the default days: %v", r)
	}
	plan["reward"] = map[string]any{"kind": "none"}
	if code, out = b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != http.StatusOK || out["plan"].(map[string]any)["reward"] != nil {
		t.Fatalf("take away: %d %v", code, out)
	}
	// A trial never has one.
	trial := map[string]any{"name": map[string]string{"en": "Trial 2"}, "kind": "time", "duration_days": 1, "price": "0", "currency": "IRT", "enabled": true, "is_trial": true,
		"reward": map[string]any{"kind": "percent", "value": "10", "days": 5}}
	if code, out := b.do(http.MethodPost, "/api/v1/plans", trial); code != http.StatusOK || out["plan"].(map[string]any)["reward"] != nil {
		t.Fatalf("a trial with a reward: %d %v", code, out)
	}
}

// DELETE /api/v1/plans/{id}: the plan is taken off sale and out of the list (never erased, with orders or without); an unknown or already
// deleted plan is a 404.
func TestDeletePlanAPI(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	inList := func(id string) bool {
		code, out := b.do(http.MethodGet, "/api/v1/plans", nil)
		if code != http.StatusOK {
			t.Fatalf("list: %d %v", code, out)
		}
		for _, it := range out["items"].([]any) {
			if it.(map[string]any)["id"] == id {
				return true
			}
		}
		return false
	}
	body := map[string]any{"name": map[string]string{"en": "Short lived"}, "kind": "time", "duration_days": 30, "price": "1,000", "currency": "IRT", "enabled": true}
	_, out := b.do(http.MethodPost, "/api/v1/plans", body)
	id := out["plan"].(map[string]any)["id"].(string)
	if !inList(id) {
		t.Fatal("the new plan is not listed")
	}
	if code, out := b.do(http.MethodDelete, "/api/v1/plans/"+id, nil); code != http.StatusOK || out["result"] != "deleted" {
		t.Fatalf("delete: %d %v", code, out)
	}
	if inList(id) {
		t.Error("the deleted plan is still listed")
	}
	if code, _ := b.do(http.MethodDelete, "/api/v1/plans/"+id, nil); code != http.StatusNotFound {
		t.Errorf("delete again: %d", code)
	}

	// A plan with an order goes the same way, and the row stays.
	sold := f.plan("Sold", 30, 10<<30, 5000)
	f.order(f.customer(9941, "buyer"), sold, "paid", "", time.Now().Add(30*24*time.Hour), 0)
	if code, out := b.do(http.MethodDelete, "/api/v1/plans/"+sold.ID, nil); code != http.StatusOK || out["result"] != "deleted" {
		t.Fatalf("delete a sold plan: %d %v", code, out)
	}
	if inList(sold.ID) {
		t.Error("the deleted plan is still listed")
	}
	if code, out := b.do(http.MethodPut, "/api/v1/plans/"+sold.ID, body); code != http.StatusNotFound {
		t.Errorf("editing a deleted plan: %d %v", code, out)
	}
	for _, p := range []string{id, sold.ID} {
		var n int
		if err := f.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.plans WHERE id = $1 AND deleted_at IS NOT NULL`, p).Scan(&n); err != nil || n != 1 {
			t.Errorf("plan %s was erased: %d %v", p, n, err)
		}
	}
}

// Only the owner may delete a plan: an admin has the Plans page but no delete, and the plan stays.
func TestOnlyTheOwnerDeletesAPlan(t *testing.T) {
	f, owner, _, _ := salesFixture(t)
	admin := f.staffUser(9301, "admin")
	_ = admin
	ab := f.browser()
	if code := ab.loginWithLink(9301); code != 200 {
		t.Fatalf("admin login: %d", code)
	}
	body := map[string]any{"name": map[string]string{"en": "Keep me"}, "kind": "time", "duration_days": 30, "price": "1,000", "currency": "IRT", "enabled": true}
	_, out := owner.do(http.MethodPost, "/api/v1/plans", body)
	id := out["plan"].(map[string]any)["id"].(string)
	if code, out := ab.do(http.MethodDelete, "/api/v1/plans/"+id, nil); code != http.StatusForbidden {
		t.Fatalf("an admin deleted a plan: %d %v", code, out)
	}
	if code, _ := ab.do(http.MethodPut, "/api/v1/plans/"+id, body); code != http.StatusOK {
		t.Fatalf("an admin can still edit: %d", code)
	}
	if code, out := owner.do(http.MethodDelete, "/api/v1/plans/"+id, nil); code != http.StatusOK || out["result"] != "deleted" {
		t.Fatalf("the owner: %d %v", code, out)
	}
	_ = f
}

// The owner lists the deleted plans and restores one; an admin may do neither.
func TestRestoreAPI(t *testing.T) {
	f, owner, _, _ := salesFixture(t)
	f.staffUser(9302, "admin")
	ab := f.browser()
	if code := ab.loginWithLink(9302); code != 200 {
		t.Fatalf("admin login: %d", code)
	}
	body := map[string]any{"name": map[string]string{"en": "Bring me back"}, "kind": "time", "duration_days": 30, "price": "1,000", "currency": "IRT", "enabled": true}
	_, out := owner.do(http.MethodPost, "/api/v1/plans", body)
	id := out["plan"].(map[string]any)["id"].(string)
	if code, _ := owner.do(http.MethodDelete, "/api/v1/plans/"+id, nil); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	code, out := owner.do(http.MethodGet, "/api/v1/plans/deleted", nil)
	items, _ := out["items"].([]any)
	if code != http.StatusOK || len(items) < 1 || items[0].(map[string]any)["id"] != id || items[0].(map[string]any)["deleted_at"] == nil {
		t.Fatalf("deleted list: %d %v", code, out)
	}
	if code, _ := ab.do(http.MethodGet, "/api/v1/plans/deleted", nil); code != http.StatusForbidden {
		t.Errorf("an admin listed the deleted plans: %d", code)
	}
	if code, _ := ab.do(http.MethodPost, "/api/v1/plans/"+id+"/restore", nil); code != http.StatusForbidden {
		t.Errorf("an admin restored a plan: %d", code)
	}
	code, out = owner.do(http.MethodPost, "/api/v1/plans/"+id+"/restore", nil)
	if code != http.StatusOK || out["plan"].(map[string]any)["enabled"] != false {
		t.Fatalf("restore: %d %v", code, out)
	}
	if code, _ = owner.do(http.MethodPost, "/api/v1/plans/"+id+"/restore", nil); code != http.StatusNotFound {
		t.Errorf("restoring twice: %d", code)
	}
	if code, out := owner.do(http.MethodGet, "/api/v1/plans", nil); code != http.StatusOK {
		t.Fatalf("list: %d", code)
	} else {
		found := false
		for _, it := range out["items"].([]any) {
			found = found || it.(map[string]any)["id"] == id
		}
		if !found {
			t.Error("the restored plan is not in the plans list")
		}
	}
}
