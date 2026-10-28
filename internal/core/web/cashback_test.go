package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// seedCashback writes what a delivered order's cashback leaves: the ledger entry and the record of how it was worked out (the way the delivery
// does, in one go). It returns the ledger entry's id.
func seedCashback(t *testing.T, f *fixture, o *store.Order, amount, percentBP int64, capped bool) string {
	t.Helper()
	entry := buuid.MustV7().String()
	f.sql(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after)
		VALUES ($1, $2, 'IRT', $3, 'cashback', 'order', $4, $5, $3)`, entry, o.UserID, amount, o.ID, "cashback:"+o.ID)
	f.sql(`INSERT INTO core.cashback_credits (order_id, user_id, currency, base_amount, percent_bp, amount, capped, ledger_entry_id)
		VALUES ($1, $2, 'IRT', $3, $4, $5, $6, $7)`, o.ID, o.UserID, o.Amount, percentBP, amount, capped, entry)
	return entry
}

// reverseSeededCashback is what a refund leaves: the reversal entry, and the record marked.
func reverseSeededCashback(t *testing.T, f *fixture, o *store.Order, taken int64) {
	t.Helper()
	entry := buuid.MustV7().String()
	f.sql(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after)
		VALUES ($1, $2, 'IRT', $3, 'cashback_reversal', 'order', $4, $5, 0)`, entry, o.UserID, -taken, o.ID, "cashback_reversal:"+o.ID)
	f.sql(`UPDATE core.cashback_credits SET reversed_amount = $2, reversed_at = now(), reversal_entry_id = $3 WHERE order_id = $1`, o.ID, taken, entry)
}

// The order page of the dashboard says what the order earned, how it was worked out, and what a refund took back; the list does not carry it.
func TestDashboardOrderShowsTheCashback(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	u := f.customer(8851, "cashback_shopper")
	p := f.plan("Plan", 30, 50<<30, 10_000)
	earned, _ := f.order(u, p, "active", "active", time.Now().Add(20*24*time.Hour), 50<<30)
	time.Sleep(20 * time.Millisecond) // the fixture names a client after the clock's millisecond
	plain, _ := f.order(u, p, "active", "active", time.Now().Add(20*24*time.Hour), 50<<30)
	seedCashback(t, f, earned, 250, 250, false)

	code, out := b.do(http.MethodGet, "/api/v1/orders/"+earned.ID, nil)
	order, _ := out["order"].(map[string]any)
	cb, _ := order["cashback"].(map[string]any)
	if code != 200 || cb == nil || asJSON(cb["amount"]) != `{"amount":250,"currency":"IRT"}` || cb["percent_bp"] != float64(250) || cb["capped"] != false || cb["reversed"] != nil {
		t.Fatalf("order with cashback: %d %v", code, out)
	}
	code, out = b.do(http.MethodGet, "/api/v1/orders/"+plain.ID, nil)
	if order, _ = out["order"].(map[string]any); code != 200 || order["cashback"] != nil {
		t.Fatalf("order without cashback: %d %v", code, out)
	}
	// The list stays light: no cashback in it.
	code, out = b.do(http.MethodGet, "/api/v1/orders?user="+u.ID, nil)
	if code != 200 || strings.Contains(asJSON(out), `"cashback"`) {
		t.Fatalf("the list carries cashback: %d %v", code, asJSON(out))
	}

	reverseSeededCashback(t, f, earned, 250)
	_, out = b.do(http.MethodGet, "/api/v1/orders/"+earned.ID, nil)
	order, _ = out["order"].(map[string]any)
	cb, _ = order["cashback"].(map[string]any)
	if asJSON(cb["reversed"]) != `{"amount":250,"currency":"IRT"}` || cb["reversed_at"] == nil {
		t.Fatalf("after the refund: %v", cb)
	}
}

// The Mini App's order carries the cashback of a delivered order (the result screen says "you earned"), and not one a refund took back.
func TestMiniAppOrderCarriesTheCashback(t *testing.T) {
	m := newMiniFixture(t, true)
	tok := m.signIn("tg:8861:shopper")
	me, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 8861)
	p := m.plan("Plan", 30, 50<<30, 10_000)
	o, _ := m.orderSub(me, p, "active", "active", time.Now().Add(20*24*time.Hour), 50<<30)
	plain, _ := m.orderSub(me, p, "active", "active", time.Now().Add(20*24*time.Hour), 50<<30)
	other := m.customer(8862, "someone_else")
	theirs, _ := m.orderSub(other, p, "active", "active", time.Now().Add(20*24*time.Hour), 50<<30)
	seedCashback(t, m.fixture, o, 250, 250, false)
	seedCashback(t, m.fixture, theirs, 500, 500, false)

	code, out := m.call(http.MethodGet, "/api/mini/v1/orders/"+o.ID, tok, nil)
	cb, _ := out["cashback"].(map[string]any)
	if code != 200 || cb == nil || cb["amount"] != float64(250) || cb["currency"] != "IRT" || cb["scale"] != float64(0) {
		t.Fatalf("order with cashback: %d %v", code, out)
	}
	if code, out = m.call(http.MethodGet, "/api/mini/v1/orders/"+plain.ID, tok, nil); code != 200 || out["cashback"] != nil {
		t.Fatalf("order without cashback: %d %v", code, out)
	}
	// Another customer's order is not found, whatever it earned.
	if code, _ = m.call(http.MethodGet, "/api/mini/v1/orders/"+theirs.ID, tok, nil); code != 404 {
		t.Fatalf("another customer's order: %d", code)
	}
	reverseSeededCashback(t, m.fixture, o, 250)
	if code, out = m.call(http.MethodGet, "/api/mini/v1/orders/"+o.ID, tok, nil); code != 200 || out["cashback"] != nil {
		t.Fatalf("after the refund: %d %v", code, out)
	}
}

// The Mini App's wallet states the cashback while it is on, and what the customer earned; nothing while it is off and nothing was earned.
func TestMiniAppWalletStatesTheCashback(t *testing.T) {
	m := newMiniFixture(t, true)
	tok := m.signIn("tg:8871:wallet_owner")
	me, _ := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), 8871)

	wallet := func() map[string]any {
		t.Helper()
		code, out := m.call(http.MethodGet, "/api/mini/v1/wallet", tok, nil)
		if code != 200 {
			t.Fatalf("wallet: %d %v", code, out)
		}
		cb, _ := out["cashback"].(map[string]any)
		return cb
	}
	if cb := wallet(); cb != nil {
		t.Fatalf("cashback off and unearned: %v", cb)
	}
	m.setting("cashback.enabled", "true")
	if cb := wallet(); cb != nil { // on but without a percent it pays nothing
		t.Fatalf("cashback on without a percent: %v", cb)
	}
	m.setting("cashback.percent", "2.5")
	m.setting("cashback.max_per_order", "20000")
	m.setting("cashback.renewals", "false")
	cb := wallet()
	if cb == nil || cb["on"] != true || cb["percent"] != "2.5" || cb["max_per_order"] != float64(20000) || cb["min_order"] != float64(0) || cb["renewals"] != false ||
		cb["earned"] != float64(0) || cb["currency"] != "IRT" {
		t.Fatalf("cashback on: %v", cb)
	}
	p := m.plan("Plan", 30, 50<<30, 10_000)
	o, _ := m.orderSub(me, p, "active", "active", time.Now().Add(20*24*time.Hour), 50<<30)
	seedCashback(t, m.fixture, o, 250, 250, false)
	if cb = wallet(); cb["earned"] != float64(250) {
		t.Fatalf("earned: %v", cb)
	}
	reverseSeededCashback(t, m.fixture, o, 100)
	if cb = wallet(); cb["earned"] != float64(150) {
		t.Fatalf("earned after a partial reversal: %v", cb)
	}
	// Switched off, what was earned still shows, without the terms.
	m.setting("cashback.enabled", "false")
	cb = wallet()
	if cb == nil || cb["on"] != false || cb["percent"] != "" || cb["earned"] != float64(150) {
		t.Fatalf("cashback off with earnings: %v", cb)
	}
}

// The reports carry the cashback given, less what refunds took back, per bucket and currency; it is not revenue and does not touch the net.
func TestReportsCountTheCashback(t *testing.T) {
	f := newFixture(t)
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	if code := b.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	r := newReportSeed(t, f)
	ali, sara := r.user(8101, "ali"), r.user(8102, "sara")
	plan := r.plan("پایه", "Basic", "IRT")
	o1 := r.paid(ali, plan, "IRT", 100_000, "2026-11-01T09:00:00Z", "wallet")
	o2 := r.paid(sara, plan, "IRT", 40_000, "2026-11-01T10:00:00Z", "wallet")
	at := func(ts string) time.Time { w, _ := time.Parse(time.RFC3339, ts); return w }
	credit := func(user, order, kind string, amount int64, ts string) {
		r.exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at)
			VALUES ($1, $2, 'IRT', $3, $4, 'order', $5, $6, 0, $7)`, buuid.MustV7().String(), user, amount, kind, order, kind+":"+order, at(ts))
	}
	credit(ali, o1, "cashback", 2_500, "2026-11-01T09:00:30Z")
	credit(sara, o2, "cashback", 1_000, "2026-11-01T10:00:30Z")
	credit(ali, o1, "cashback_reversal", -2_500, "2026-11-02T09:00:00Z") // a refund on the next day takes the first back

	const q = "?from=2026-11-01&to=2026-11-02&group=day&tz=UTC"
	code, out := b.do(http.MethodGet, "/api/v1/reports/summary"+q, nil)
	buckets, _ := out["buckets"].([]any)
	if code != 200 || len(buckets) != 2 {
		t.Fatalf("summary: %d %v", code, out)
	}
	irt := func(i int) map[string]any {
		return buckets[i].(map[string]any)["currencies"].(map[string]any)["IRT"].(map[string]any)
	}
	d1, d2 := irt(0), irt(1)
	if cb := d1["cashback"].(map[string]any); cb["count"] != float64(2) || cb["amount"] != float64(3_500) {
		t.Errorf("1 Nov cashback: %v", cb)
	}
	if cb := d2["cashback"].(map[string]any); cb["count"] != float64(0) || cb["amount"] != float64(-2_500) {
		t.Errorf("2 Nov cashback (the reversal alone): %v", cb)
	}
	// Not revenue, not in the net.
	if d1["revenue"] != float64(140_000) || d1["net"] != float64(140_000) {
		t.Errorf("revenue and net moved: %v", d1)
	}
	totals := out["totals"].(map[string]any)["currencies"].(map[string]any)["IRT"].(map[string]any)
	if cb := totals["cashback"].(map[string]any); cb["count"] != float64(2) || cb["amount"] != float64(1_000) {
		t.Errorf("totals cashback: %v", cb)
	}

	// The CSV file has the two columns at its end, in the rows of the currency.
	code, _, body := b.get("/api/v1/reports/summary.csv" + q)
	if code != 200 {
		t.Fatalf("summary.csv: %d", code)
	}
	rows := parseCSV(t, body)
	head := rows[0]
	if n := len(head); head[n-2] != "cashback_count" || head[n-1] != "cashback_amount" {
		t.Fatalf("the file's last columns: %v", head)
	}
	got := map[string][2]string{}
	for _, row := range rows[1:] {
		if row[2] == "IRT" {
			got[row[0]] = [2]string{row[len(row)-2], row[len(row)-1]}
		}
	}
	if got["2026-11-01"] != [2]string{"2", "3500"} || got["2026-11-02"] != [2]string{"0", "-2500"} {
		t.Errorf("cashback in the file: %v", got)
	}
}
