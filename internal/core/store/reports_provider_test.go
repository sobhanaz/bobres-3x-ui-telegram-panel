package store

import (
	"reflect"
	"testing"
)

// providerOf marks the order.paid.v1 event of a purchase with the provider
// that took the payment, as core now publishes it.
func (x *seed) providerOf(pu *mPurchase, provider string) {
	x.t.Helper()
	x.exec(`UPDATE core.outbox_core SET payload = payload || jsonb_build_object('provider', $1::text)
		WHERE topic = 'order.paid.v1' AND payload->>'order_id' = $2`, provider, pu.order)
}

// A scenario with providers on some of its events, and none on the others
// (events from before the provider was recorded).
func providerScenario(t *testing.T) (*seed, map[string]string) {
	t.Helper()
	x, _ := reportScenario(t)
	byOrder := map[string]string{}
	gateway, manual := 0, 0
	for _, p := range x.m.purchases {
		var provider string
		switch p.src {
		case "gateway":
			provider = []string{"zarinpal", "stars", "usdt_trc20_auto", ""}[gateway%4]
			gateway++
		case "manual":
			provider = []string{"manual_card", ""}[manual%2]
			manual++
		}
		if provider != "" {
			x.providerOf(p, provider)
			byOrder[p.order] = provider
		}
	}
	if gateway < 4 || manual < 2 {
		t.Fatalf("the scenario has %d gateway and %d manual purchases: too few to cover every provider", gateway, manual)
	}
	return x, byOrder
}

func TestSalesByMethodSplitsByProviderAndStillAddsUpToRevenue(t *testing.T) {
	x, byOrder := providerScenario(t)
	q := ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "Asia/Tehran"}
	rows, err := x.s.SalesByMethod(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]MethodRow{}
	revenue := map[string]int64{}
	for _, p := range x.m.purchases {
		if p.at.Before(utc("2026-10-01T00:00:00Z")) { // the purchase of 2026-09-20
			continue
		}
		prov := byOrder[p.order]
		k := p.src + "|" + prov + "|" + p.cur
		m := want[k]
		m.Method, m.Provider, m.Currency = p.src, prov, p.cur
		m.Orders++
		m.Revenue += p.amt
		want[k] = m
		revenue[p.cur] += p.amt
	}
	got := map[string]MethodRow{}
	sums := map[string]int64{}
	for _, r := range rows {
		got[r.Method+"|"+r.Provider+"|"+r.Currency] = r
		sums[r.Currency] += r.Revenue
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("by method and provider\n got: %+v\nwant: %+v", got, want)
	}
	if !reflect.DeepEqual(sums, revenue) {
		t.Errorf("rows add up to %v, revenue is %v", sums, revenue)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Provider] = true
		if r.Method == "wallet" && r.Provider != "" {
			t.Errorf("a wallet row with a provider: %+v", r)
		}
	}
	for _, p := range []string{"zarinpal", "stars", "usdt_trc20_auto", "manual_card", ""} {
		if !seen[p] {
			t.Errorf("no row for provider %q: %+v", p, rows)
		}
	}

	// A purchase whose event is missing is still "unknown", with no provider.
	var gone *mPurchase
	for _, p := range x.m.purchases {
		if byOrder[p.order] == "zarinpal" && p.cur == "IRT" && !p.at.Before(utc("2026-10-01T00:00:00Z")) {
			gone = p
			break
		}
	}
	if gone == nil {
		t.Fatal("no zarinpal purchase to delete the event of")
	}
	x.exec(`DELETE FROM core.outbox_core WHERE topic = 'order.paid.v1' AND payload->>'order_id' = $1`, gone.order)
	rows, err = x.s.SalesByMethod(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var total, unknown int64
	for _, r := range rows {
		if r.Currency == "IRT" {
			total += r.Revenue
			if r.Method == "unknown" {
				unknown += r.Revenue
				if r.Provider != "" {
					t.Errorf("an unknown row with a provider: %+v", r)
				}
			}
		}
	}
	if unknown != gone.amt || total != revenue["IRT"] {
		t.Errorf("unknown %d (want %d), IRT total %d (want %d)", unknown, gone.amt, total, revenue["IRT"])
	}
}

// Events published before the provider was recorded have no such key: they
// land in one row of their method, with an empty provider, beside the new ones.
func TestSalesByMethodKeepsOldEventsWithoutProvider(t *testing.T) {
	x, byOrder := providerScenario(t)
	// Strip every provider again: the whole history is "old".
	x.exec(`UPDATE core.outbox_core SET payload = payload - 'provider' WHERE topic = 'order.paid.v1'`)
	q := ReportQuery{From: "2026-10-01", To: "2026-11-30", TZ: "Asia/Tehran"}
	rows, err := x.s.SalesByMethod(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(byOrder) == 0 {
		t.Fatal("the scenario set no provider")
	}
	perKey := map[string]int{}
	for _, r := range rows {
		if r.Provider != "" {
			t.Errorf("a provider out of nothing: %+v", r)
		}
		perKey[r.Method+"|"+r.Currency]++
	}
	for k, n := range perKey {
		if n != 1 {
			t.Errorf("%s is in %d rows, want 1", k, n)
		}
	}
}

func TestEachPaidOrderHasTheProvider(t *testing.T) {
	x, byOrder := providerScenario(t)
	var got int
	if _, err := x.s.EachPaidOrder(x.ctx, ReportQuery{From: "2026-09-01", To: "2026-11-30", TZ: "Asia/Tehran"}, 100, func(r PaidOrderRow) error {
		got++
		if r.Provider != byOrder[r.OrderID] {
			t.Errorf("order %s (%s): provider %q, want %q", r.OrderID, r.Method, r.Provider, byOrder[r.OrderID])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != len(x.m.purchases) {
		t.Errorf("%d orders, want %d", got, len(x.m.purchases))
	}
}
