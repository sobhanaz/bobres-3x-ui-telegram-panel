package store

import (
	"encoding/json"
	"testing"
	"time"

	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// The licenses the store sells and the digital accounts it resells are revenue like the VPN orders: the summary, the methods, the hours and the
// paid-orders export count them (in the currency they were paid in), they are told apart from the VPN orders, a refund of either is a refund,
// and the reports count only from the moment the owner started them from zero.
func TestReportsCountLicensesAndDigitalAccountsAndTheStart(t *testing.T) {
	x := newSeed(t)
	day := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	q := ReportQuery{From: "2026-10-01", To: "2026-10-31", TZ: "UTC", Group: "month"}

	plan := x.plan("ماهانه", "Monthly", "IRT", false)
	x.paid(1, plan, "new", 1000, day, "wallet")

	// a license (USDT) and a digital account (Toman), paid from the wallet; the license is refunded later
	u := x.user(1)
	vo, so := buuid.MustV7().String(), buuid.MustV7().String()
	x.exec(`INSERT INTO core.vendor_orders (id, user_id, kind, tier, months, amount, currency, status, idempotency_key, created_at, updated_at)
		VALUES ($1::uuid, $2, 'new', 'standard', 1, 4000000, 'USDT', 'refunded', 'lic-'||$1::text, $3, $3)`, vo, u, day.Add(time.Hour))
	x.ledger(u, "USDT", -4_000_000, "license_purchase", "vendor_order", vo, "license_purchase:"+vo, day.Add(time.Hour))
	x.ledger(u, "USDT", 4_000_000, "license_refund", "vendor_order", vo, "license_refund:"+vo, day.Add(2*time.Hour))
	x.exec(`INSERT INTO core.supplier_products (id, name, title_i18n) VALUES ('gem-1m', 'Gemini Pro', $1::text::jsonb)`, `{"fa":"جمینای"}`)
	x.exec(`INSERT INTO core.supplier_orders (id, user_id, product_id, quantity, amount, currency, status, idempotency_key, created_at, updated_at)
		VALUES ($1::uuid, $2, 'gem-1m', 2, 900000, 'IRT', 'done', 'sup-'||$1::text, $3, $3)`, so, u, day.Add(3*time.Hour))
	x.ledger(u, "IRT", -900_000, "supplier_purchase", "supplier_order", so, "supplier_purchase:"+so, day.Add(3*time.Hour))

	sum, err := x.s.SalesSummary(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	irt, usdt := sum.Cells["2026-10-01"]["IRT"], sum.Cells["2026-10-01"]["USDT"]
	if irt == nil || usdt == nil {
		t.Fatalf("cells: %+v", sum.Cells)
	}
	if irt.Revenue != 1000+900_000 || irt.Orders != 2 || irt.NewOrders != 1 || irt.Digital != (ReportFlow{1, 900_000}) || irt.Licenses != (ReportFlow{}) {
		t.Errorf("Toman: %+v", irt)
	}
	if usdt.Revenue != 4_000_000 || usdt.Orders != 1 || usdt.NewOrders != 0 || usdt.Licenses != (ReportFlow{1, 4_000_000}) || usdt.Refunds != (ReportFlow{1, 4_000_000}) {
		t.Errorf("USDT: %+v", usdt)
	}

	// how they were paid: all of it wallet, nothing left over as "unknown"
	methods, err := x.s.SalesByMethod(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]MethodRow{}
	for _, m := range methods {
		got[m.Method+"/"+m.Currency] = m
	}
	if got["wallet/IRT"].Revenue != 1000+900_000 || got["wallet/IRT"].Orders != 2 || got["wallet/USDT"].Revenue != 4_000_000 || got["unknown/IRT"].Orders != 0 || got["unknown/USDT"].Orders != 0 {
		t.Errorf("methods: %+v", methods)
	}

	// the hours of the week and the export count them too
	hours, err := x.s.SalesHours(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var hourRevenue, hourOrders int64
	for _, h := range hours {
		if h.Currency == "IRT" {
			hourRevenue += h.Revenue
			hourOrders += h.Orders
		}
	}
	if hourRevenue != 1000+900_000 || hourOrders != 2 {
		t.Errorf("hours: %+v", hours)
	}
	var rows []PaidOrderRow
	if _, err := x.s.EachPaidOrder(x.ctx, q, 100, func(r PaidOrderRow) error { rows = append(rows, r); return nil }); err != nil {
		t.Fatal(err)
	}
	byType := map[string]PaidOrderRow{}
	var rowRevenue = map[string]int64{}
	for _, r := range rows {
		byType[r.Type] = r
		rowRevenue[r.Currency] += r.Amount
	}
	if len(rows) != 3 || rowRevenue["IRT"] != 1000+900_000 || rowRevenue["USDT"] != 4_000_000 {
		t.Fatalf("export rows add up wrong: %+v", rows)
	}
	if l := byType["license"]; l.Method != "wallet" || l.Refunded != 4_000_000 || l.PlanName["en"] != "BOBRES license (standard, 1 months)" {
		t.Errorf("license row: %+v", l)
	}
	if d := byType["digital"]; d.Method != "wallet" || d.Refunded != 0 || d.PlanName["fa"] != "جمینای × 2" || d.PlanName["en"] != "Gemini Pro x 2" {
		t.Errorf("digital row: %+v", d)
	}

	// Started from zero between the VPN order and the digital account: only what came after is counted, everywhere, and nothing is deleted.
	raw, _ := json.Marshal(day.Add(150 * time.Minute).Format(time.RFC3339))
	if err := x.s.SetSetting(x.ctx, x.s.Conn(), ReportsSinceKey, raw); err != nil {
		t.Fatal(err)
	}
	after, err := x.s.SalesSummary(x.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	irt = after.Cells["2026-10-01"]["IRT"]
	if irt == nil || irt.Revenue != 900_000 || irt.Orders != 1 || irt.NewOrders != 0 || after.Cells["2026-10-01"]["USDT"] != nil {
		t.Errorf("after the start: %+v", after.Cells["2026-10-01"])
	}
	rows = nil
	if _, err := x.s.EachPaidOrder(x.ctx, q, 100, func(r PaidOrderRow) error { rows = append(rows, r); return nil }); err != nil || len(rows) != 1 || rows[0].Type != "digital" {
		t.Errorf("export after the start: %v %+v", err, rows)
	}
	var ledger int
	if err := x.s.DB().QueryRow(x.ctx, `SELECT count(*) FROM core.ledger_entries`).Scan(&ledger); err != nil || ledger != 4 {
		t.Errorf("the ledger lost entries: %d %v", ledger, err)
	}
	// Taking the start away counts everything again.
	if err := x.s.DeleteSetting(x.ctx, x.s.Conn(), ReportsSinceKey); err != nil {
		t.Fatal(err)
	}
	if again, err := x.s.SalesSummary(x.ctx, q); err != nil || again.Cells["2026-10-01"]["IRT"].Revenue != 1000+900_000 {
		t.Errorf("counting everything again: %v %+v", err, again.Cells)
	}

	// The overview's revenue follows it too (a sale 2 hours ago, a start 1 hour ago, a sale now).
	now := time.Now().UTC()
	x.ledger(u, "IRT", -300, "purchase", "order", buuid.MustV7().String(), "ov-1", now.Add(-2*time.Hour))
	x.ledger(u, "IRT", -50, "purchase", "order", buuid.MustV7().String(), "ov-2", now.Add(-10*time.Minute))
	x.ledger(u, "USDT", -2_000_000, "license_purchase", "vendor_order", buuid.MustV7().String(), "ov-3", now.Add(-5*time.Minute))
	ov, err := x.s.OverviewStats(x.ctx, x.s.Conn(), now)
	if err != nil || ov.Revenue24h["IRT"] < 350 || ov.Revenue24h["USDT"] != 2_000_000 {
		t.Fatalf("overview before the start: %v %+v", err, ov)
	}
	raw, _ = json.Marshal(now.Add(-time.Hour).Format(time.RFC3339))
	if err := x.s.SetSetting(x.ctx, x.s.Conn(), ReportsSinceKey, raw); err != nil {
		t.Fatal(err)
	}
	ov, err = x.s.OverviewStats(x.ctx, x.s.Conn(), now)
	if err != nil || ov.Revenue24h["IRT"] != 50 || ov.Revenue24h["USDT"] != 2_000_000 || ov.Revenue30d["IRT"] != 50 {
		t.Fatalf("overview after the start: %v %+v", err, ov)
	}
}
