package web

import (
	"net/http"
	"testing"
)

// A plan's reward (the percent of the discount code a paid purchase gives its buyer) is set from the plan editor: absent keeps it, 0
// takes it away, a number from 1 to 90 sets it, anything else is refused, and a trial or a traffic package never has one.
func TestPlanRewardPercentAPI(t *testing.T) {
	_, b, _, _ := salesFixture(t)
	plan := map[string]any{"name": map[string]string{"en": "Unlimited"}, "kind": "time", "duration_days": 30, "price": "120,000", "currency": "IRT", "enabled": true, "reward_percent": 15}
	code, out := b.do(http.MethodPost, "/api/v1/plans", plan)
	saved, _ := out["plan"].(map[string]any)
	if code != http.StatusOK || saved["reward_percent"] != float64(15) {
		t.Fatalf("create with a reward: %d %v", code, out)
	}
	id := saved["id"].(string)
	// An edit with none (the on/off switch sends none) keeps it.
	delete(plan, "reward_percent")
	plan["enabled"] = false
	if code, out = b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != http.StatusOK || out["plan"].(map[string]any)["reward_percent"] != float64(15) {
		t.Fatalf("an edit without it: %d %v", code, out)
	}
	for _, bad := range []int{-1, 91, 500} {
		plan["reward_percent"] = bad
		if code, out := b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != http.StatusBadRequest || out["field"] != "reward_percent" {
			t.Errorf("percent %d: %d %v", bad, code, out)
		}
	}
	plan["reward_percent"] = 30
	if code, out = b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != http.StatusOK || out["plan"].(map[string]any)["reward_percent"] != float64(30) {
		t.Fatalf("change: %d %v", code, out)
	}
	plan["reward_percent"] = 0
	if code, out = b.do(http.MethodPut, "/api/v1/plans/"+id, plan); code != http.StatusOK || out["plan"].(map[string]any)["reward_percent"] != nil {
		t.Fatalf("take away: %d %v", code, out)
	}
	// A trial never has one.
	trial := map[string]any{"name": map[string]string{"en": "Trial 2"}, "kind": "time", "duration_days": 1, "price": "0", "currency": "IRT", "enabled": true, "is_trial": true, "reward_percent": 10}
	if code, out := b.do(http.MethodPost, "/api/v1/plans", trial); code != http.StatusOK || out["plan"].(map[string]any)["reward_percent"] != nil {
		t.Fatalf("a trial with a reward: %d %v", code, out)
	}
}
