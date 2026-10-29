package store

import (
	"testing"
	"time"

	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// A plan priced in USDT and paid from the Toman wallet is revenue in Toman (what the ledger charged), and the export keeps the USDT price it was listed at
// and the rate; an order paid in the currency it was listed in has none, and a license carries its own list price and rate.
func TestPaidOrdersExportKeepsTheListPrice(t *testing.T) {
	x := newSeed(t)
	day := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	q := ReportQuery{From: "2026-10-01", To: "2026-10-31", TZ: "UTC", Group: "month"}

	irtPlan := x.plan("ماهانه", "Monthly", "IRT", false)
	usdtPlan := x.plan("تتری", "Pro USDT", "USDT", false)

	// 1. an ordinary Toman order, 2. a USDT-priced plan paid by crypto in USDT, 3. a USDT-priced plan paid from the wallet: 4 USDT at 294,000
	x.paid(1, irtPlan, "new", 1000, day, "wallet")
	x.paid(2, usdtPlan, "new", 4_000_000, day.Add(time.Hour), "gateway")
	// (the order is charged in Toman: its amount and currency, the ledger entry and the paid event all say so)
	buyer := x.user(3)
	at := day.Add(2 * time.Hour)
	walletPaid := x.order(buyer, usdtPlan, "new", "active", 1_176_000, at, nil)
	x.exec(`UPDATE core.orders SET currency = 'IRT', list_amount = 4000000, list_currency = 'USDT', rate = 294000 WHERE id = $1`, walletPaid)
	x.ledger(buyer, "IRT", -1_176_000, "purchase", "order", walletPaid, "purchase:"+walletPaid, at)
	x.event("order.paid.v1", map[string]any{"order_id": walletPaid, "user_id": buyer, "plan_id": usdtPlan.id, "amount": 1_176_000, "currency": "IRT", "source": "wallet"}, at)

	// 4. a license paid from the wallet at 271,600: its list price and rate
	u := x.user(4)
	vo := buuid.MustV7().String()
	x.exec(`INSERT INTO core.vendor_orders (id, user_id, kind, tier, months, amount, currency, status, idempotency_key, created_at, updated_at, list_amount, list_currency, rate)
		VALUES ($1::uuid, $2, 'new', 'standard', 1, 1086400, 'IRT', 'done', 'lic-'||$1::text, $3, $3, 4000000, 'USDT', 271600)`, vo, u, day.Add(3*time.Hour))
	x.ledger(u, "IRT", -1_086_400, "license_purchase", "vendor_order", vo, "license_purchase:"+vo, day.Add(3*time.Hour))

	var rows []PaidOrderRow
	if _, err := x.s.EachPaidOrder(x.ctx, q, 100, func(r PaidOrderRow) error { rows = append(rows, r); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows: %+v", rows)
	}
	want := []struct {
		currency                string
		amount, list, rate      int64
		listCurrency, paidWith string
	}{
		{"IRT", 1000, 0, 0, "", "wallet"},
		{"USDT", 4_000_000, 0, 0, "", "gateway"},
		{"IRT", 1_176_000, 4_000_000, 294_000, "USDT", "wallet"},
		{"IRT", 1_086_400, 4_000_000, 271_600, "USDT", "wallet"},
	}
	for i, w := range want {
		r := rows[i]
		if r.Currency != w.currency || r.Amount != w.amount || r.ListAmount != w.list || r.ListCurrency != w.listCurrency || r.Rate != w.rate || r.Method != w.paidWith {
			t.Errorf("row %d: %+v, want %+v", i, r, w)
		}
	}

	// The revenue is the Toman that was charged, in Toman; the order is not also revenue in USDT.
	sum, err := x.s.SalesSummary(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	irt, usdt := sum.Cells["2026-10-01"]["IRT"], sum.Cells["2026-10-01"]["USDT"]
	if irt == nil || irt.Revenue != 1000+1_176_000+1_086_400 || irt.Orders != 3 || irt.NewOrders != 2 || irt.Licenses != (ReportFlow{1, 1_086_400}) {
		t.Errorf("Toman: %+v", irt)
	}
	if usdt == nil || usdt.Revenue != 4_000_000 || usdt.Orders != 1 {
		t.Errorf("USDT: %+v", usdt)
	}
	// Top plans: the USDT plan appears under Toman (the wallet order) and under USDT (the crypto order), each in its own currency.
	plans, err := x.s.TopPlans(x.ctx, q, 10)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]PlanRow{}
	for _, p := range plans {
		if p.PlanID == usdtPlan.id {
			seen[p.Currency] = p
		}
	}
	if seen["IRT"].Revenue != 1_176_000 || seen["IRT"].Orders != 1 || seen["USDT"].Revenue != 4_000_000 || seen["USDT"].Orders != 1 {
		t.Errorf("the USDT-priced plan: %+v", seen)
	}
}
