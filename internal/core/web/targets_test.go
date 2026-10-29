package web

import (
	"context"
	"net/http"
	"regexp"
	"testing"
	"time"
)

// The Mini App's reseller panel carries this month against the owner's targets while they are on: the month, the count, each tier with whether it
// is reached and paid, and what is left; nothing while they are off.
func TestMiniResellerPanelCarriesTheMonthlyTarget(t *testing.T) {
	m := newMiniReseller(t)
	tok, u := m.reseller("targets_shop", 500_000)
	panel := func() map[string]any {
		t.Helper()
		return m.call2(http.MethodGet, "/api/mini/v1/reseller", tok, nil)
	}
	if got := panel(); got["targets"] != nil {
		t.Fatalf("targets while they are off: %v", got["targets"])
	}

	m.setting("targets.enabled", "true")
	if got := panel(); got["targets"] != nil { // switched on but no tier to reach
		t.Fatalf("targets without tiers: %v", got["targets"])
	}
	m.setting("targets.tiers", `[{"threshold":4,"bonus":50000},{"threshold":2,"bonus":10000}]`)
	tg, _ := panel()["targets"].(map[string]any)
	if tg == nil {
		t.Fatal("no targets once they are on")
	}
	period, _ := tg["period"].(string)
	if !regexp.MustCompile(`^\d{4}-\d{2}$`).MatchString(period) || tg["calendar"] != "gregorian" || tg["sold"] != float64(0) || tg["remaining"] != float64(2) ||
		tg["bonus_paid"] != float64(0) || tg["currency"] != "IRT" || tg["scale"] != float64(0) {
		t.Fatalf("a month with no sale: %v", tg)
	}
	for _, k := range []string{"from", "to"} {
		if s, _ := tg[k].(string); !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(s) {
			t.Errorf("%s: %v", k, tg[k])
		}
	}
	if ends, _ := tg["ends_at"].(float64); ends <= float64(time.Now().Unix()) {
		t.Errorf("the month ends at %v", tg["ends_at"])
	}
	tiers := jlist(tg, "tiers")
	if len(tiers) != 2 || tiers[0]["threshold"] != float64(2) || tiers[0]["bonus"] != float64(10_000) || tiers[1]["threshold"] != float64(4) ||
		tiers[0]["reached"] != false || tiers[0]["paid"] != false {
		t.Fatalf("tiers: %v", tiers)
	}

	// Two services sold this month, and the first tier's bonus paid.
	for range 2 {
		sub := m.service(u, "")
		m.sql(`INSERT INTO core.reseller_sales (order_id, user_id, period) VALUES ($1, $2, $3)`, sub.OrderID, u.ID, period)
	}
	var entry string
	if err := m.st.DB().QueryRow(context.Background(), `INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after)
		VALUES (gen_random_uuid(), $1, 'IRT', 10000, 'target_bonus', 'user', $1, $2, 10000) RETURNING id`, u.ID, "target:"+u.ID+":"+period+":2").Scan(&entry); err != nil {
		t.Fatal(err)
	}
	m.sql(`INSERT INTO core.reseller_target_bonuses (user_id, period, threshold, amount, currency, sales, ledger_entry_id) VALUES ($1, $2, 2, 10000, 'IRT', 2, $3)`, u.ID, period, entry)
	tg, _ = panel()["targets"].(map[string]any)
	tiers = jlist(tg, "tiers")
	if tg["sold"] != float64(2) || tg["remaining"] != float64(2) || tg["bonus_paid"] != float64(10_000) || len(tiers) != 2 ||
		tiers[0]["reached"] != true || tiers[0]["paid"] != true || tiers[1]["reached"] != false || tiers[1]["paid"] != false {
		t.Fatalf("after two sales and a bonus: %v", tg)
	}

	// A customer sees no panel, so no targets; the owner switching them off takes the card away again.
	m.setting("targets.enabled", "false")
	if got := panel(); got["targets"] != nil {
		t.Fatalf("targets after switching them off: %v", got["targets"])
	}
}
