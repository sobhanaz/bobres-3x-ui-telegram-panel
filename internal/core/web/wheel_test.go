package web

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// wheelOn switches the wheel on with one prize of 1,000 Toman that every spin wins (no empty segment), under a budget.
func (m *miniFix) wheelOn(budget string) {
	m.t.Helper()
	for k, v := range map[string]string{
		"wheel.enabled": "true", "wheel.prizes": `[{"amount":1000,"weight":1}]`, "wheel.no_prize_weight": "0", "wheel.daily_budget": budget, "wheel.min_orders": "1",
	} {
		m.setting(k, v)
	}
}

// wheelCustomer signs a customer in and gives them a paid order.
func (m *miniFix) wheelCustomer(tg int64, name string, orders int) (string, *store.User) {
	m.t.Helper()
	tok := m.signIn("tg:" + strconv.FormatInt(tg, 10) + ":" + name)
	u, err := m.st.GetUserByTelegramID(context.Background(), m.st.Conn(), tg)
	if err != nil {
		m.t.Fatal(err)
	}
	p := m.plan("Wheel plan", 30, 50<<30, 10_000)
	for range orders {
		m.orderSub(u, p, "active", "active", time.Now().Add(20*24*time.Hour), 50<<30)
	}
	return tok, u
}

// The wheel in the Mini App: the section shows only while the wheel can pay and only to customers; a customer with a paid order spins once, gets the
// same spin back when they ask again, and the page follows what core says.
func TestMiniAppWheel(t *testing.T) {
	m := newMiniFixture(t, true)
	tok, u := m.wheelCustomer(8871, "wheeler", 1)

	// Off by default: no section, no page, no spin.
	if f := m.features(tok); f["wheel"] != false {
		t.Fatalf("features while the wheel is off: %v", f)
	}
	if code, _ := m.call(http.MethodGet, "/api/mini/v1/wheel", tok, nil); code != 404 {
		t.Fatalf("the page while off: %d", code)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", tok, nil); code != 404 {
		t.Fatalf("a spin while off: %d", code)
	}
	// Switched on but with no budget it is still closed.
	m.wheelOn("")
	if f := m.features(tok); f["wheel"] != false {
		t.Fatalf("features without a budget: %v", f)
	}

	m.wheelOn("1500")
	if f := m.features(tok); f["wheel"] != true {
		t.Fatalf("features with the wheel on: %v", f)
	}
	code, page := m.call(http.MethodGet, "/api/mini/v1/wheel", tok, nil)
	prizes := jlist(page, "prizes")
	if code != 200 || page["spun"] != false || page["open"] != true || page["reason"] != "" || page["orders"] != float64(1) || page["min_orders"] != float64(1) ||
		page["total_weight"] != float64(1) || page["no_prize_weight"] != float64(0) || page["currency"] != "IRT" || page["scale"] != float64(0) ||
		len(prizes) != 1 || prizes[0]["amount"] != float64(1000) || prizes[0]["weight"] != float64(1) {
		t.Fatalf("the page: %d %v", code, page)
	}
	if ends, _ := page["resets_at"].(float64); ends <= float64(time.Now().Unix()) {
		t.Errorf("the next spin is at %v", page["resets_at"])
	}

	// The spin: the draw is core's, the answer says where it stopped.
	code, spin := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", tok, nil)
	if code != 200 || spin["status"] != "won" || spin["prize"] != float64(1000) || spin["index"] != float64(0) || spin["balance"] != float64(1000) ||
		spin["currency"] != "IRT" {
		t.Fatalf("the spin: %d %v", code, spin)
	}
	// Again: the same spin, no second prize.
	for range 2 {
		code, again := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", tok, nil)
		if code != 200 || again["status"] != "already" || again["prize"] != float64(1000) || again["balance"] != float64(1000) {
			t.Fatalf("asked again: %d %v", code, again)
		}
	}
	if code, page = m.call(http.MethodGet, "/api/mini/v1/wheel", tok, nil); code != 200 || page["spun"] != true || page["prize"] != float64(1000) {
		t.Fatalf("the page after the spin: %d %v", code, page)
	}
	var prizesPaid int
	if err := m.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.ledger_entries WHERE user_id = $1 AND kind = 'wheel_prize'`, u.ID).Scan(&prizesPaid); err != nil || prizesPaid != 1 {
		t.Fatalf("prizes in the ledger: %d %v", prizesPaid, err)
	}
	// The wallet the app shows has the prize and a name for it.
	code, wallet := m.call(http.MethodGet, "/api/mini/v1/wallet", tok, nil)
	wallets, history := jlist(wallet, "wallets"), jlist(wallet, "ledger")
	if code != 200 || len(wallets) != 1 || wallets[0]["balance"] != float64(1000) || len(history) == 0 || history[0]["kind"] != "wheel_prize" {
		t.Fatalf("the wallet: %d %v", code, wallet)
	}

	// A second customer: the budget (1,500) cannot cover another 1,000 after the first was paid... it can, 500 is left, and it cannot.
	tok2, _ := m.wheelCustomer(8872, "second", 1)
	code, closed := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", tok2, nil)
	if code != 200 || closed["status"] != "closed" || closed["prize"] != float64(0) || closed["balance"] != float64(0) {
		t.Fatalf("over the day's budget: %d %v", code, closed)
	}
	if _, page = m.call(http.MethodGet, "/api/mini/v1/wheel", tok2, nil); page["spun"] != false || page["open"] != false {
		t.Fatalf("the page of the customer sent away: %v", page)
	}

	// A customer without a paid order is told how many it takes, and nothing is written.
	tok3, _ := m.wheelCustomer(8873, "newcomer", 0)
	if _, page = m.call(http.MethodGet, "/api/mini/v1/wheel", tok3, nil); page["reason"] != "orders" || page["orders"] != float64(0) || page["min_orders"] != float64(1) {
		t.Fatalf("the page without an order: %v", page)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", tok3, nil); code != 200 || out["status"] != "orders" || out["orders"] != float64(0) || out["min_orders"] != float64(1) {
		t.Fatalf("a spin without an order: %d %v", code, out)
	}

	// Maintenance and the sign-in are in front of it like the rest of the app.
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", "", nil); code != 401 {
		t.Fatalf("without a session: %d", code)
	}
	m.setting("maintenance.enabled", "true")
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", tok2, nil); code != 503 {
		t.Fatalf("in maintenance: %d", code)
	}
	m.setting("maintenance.enabled", "false")

	// Switched off again: gone.
	m.setting("wheel.enabled", "false")
	if f := m.features(tok); f["wheel"] != false {
		t.Fatalf("features after switching it off: %v", f)
	}
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", tok, nil); code != 404 {
		t.Fatalf("a spin after switching it off: %d", code)
	}
}

// Resellers and staff do not spin: the section is not shown to them and a call is answered with the status "role".
func TestMiniAppWheelIsForCustomers(t *testing.T) {
	m := newMiniFixture(t, true)
	m.wheelOn("5000")
	tok, u := m.wheelCustomer(8881, "turned_reseller", 1)
	if f := m.features(tok); f["wheel"] != true {
		t.Fatalf("as a customer: %v", f)
	}
	m.sql(`UPDATE core.users SET role = 'reseller' WHERE id = $1`, u.ID)
	if f := m.features(tok); f["wheel"] != false {
		t.Fatalf("as a reseller: %v", f)
	}
	if code, out := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", tok, nil); code != 200 || out["status"] != "role" {
		t.Fatalf("a reseller's spin: %d %v", code, out)
	}
	if code, page := m.call(http.MethodGet, "/api/mini/v1/wheel", tok, nil); code != 200 || page["reason"] != "role" {
		t.Fatalf("a reseller's page: %d %v", code, page)
	}
	var spins int
	if err := m.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.wheel_spins`).Scan(&spins); err != nil || spins != 0 {
		t.Fatalf("spins: %d %v", spins, err)
	}
}

// A banned customer's session is closed, and nothing is paid to them.
func TestMiniAppWheelRefusesTheBanned(t *testing.T) {
	m := newMiniFixture(t, true)
	m.wheelOn("5000")
	tok, u := m.wheelCustomer(8891, "banned_soon", 1)
	m.sql(`UPDATE core.users SET status = 'banned' WHERE id = $1`, u.ID)
	if code, _ := m.call(http.MethodPost, "/api/mini/v1/wheel/spin", tok, nil); code != 403 {
		t.Fatalf("a banned spin: %d", code)
	}
	var spins int
	if err := m.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.wheel_spins WHERE user_id = $1`, u.ID).Scan(&spins); err != nil || spins != 0 {
		t.Fatalf("spins of a banned customer: %d %v", spins, err)
	}
}

// seedWheelSpin writes a spin of a day (a SQL expression for the date) the way the wheel does: with its ledger entry when it won.
func seedWheelSpin(t *testing.T, f *fixture, userID, day string, prize int64) {
	t.Helper()
	var entry *string
	if prize > 0 {
		id := buuid.MustV7().String()
		f.sql(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after)
			VALUES ($1, $2, 'IRT', $3, 'wheel_prize', $4, $3)`, id, userID, prize, "wheel:"+userID+":"+id)
		entry = &id
	}
	f.sql(`INSERT INTO core.wheel_spins (user_id, day, prize, ledger_entry_id, roll, total_weight) VALUES ($1, `+day+`, $2, $3, 0, 2)`, userID, prize, entry)
}

// The owner's small report of the wheel: today against the budget, the days of the last month with a spin, and what it paid in all.
func TestDashboardWheelReport(t *testing.T) {
	f, b, _, _ := salesFixture(t)
	f.sql(`DELETE FROM core.settings`)
	t.Cleanup(func() { f.sql(`DELETE FROM core.settings`) })
	for k, v := range map[string]string{"wheel.enabled": "true", "wheel.prizes": `[{"amount":1000,"weight":1}]`, "wheel.daily_budget": "10000"} {
		if code, out := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{k: v}}); code != 200 {
			t.Fatalf("setting %s: %d %v", k, code, out)
		}
	}
	a, c, d := f.customer(8901, "w_a"), f.customer(8902, "w_b"), f.customer(8903, "w_c")
	const today = `(now() AT TIME ZONE 'Asia/Tehran')::date`
	seedWheelSpin(t, f, a.ID, today, 1000)
	seedWheelSpin(t, f, c.ID, today, 0)
	seedWheelSpin(t, f, a.ID, today+` - 2`, 1000)
	seedWheelSpin(t, f, d.ID, today+` - 2`, 1000)
	seedWheelSpin(t, f, d.ID, today+` - 45`, 1000) // outside the 30 days

	code, out := b.do(http.MethodGet, "/api/v1/wheel/report", nil)
	if code != 200 || out["on"] != true || out["window_days"] != float64(30) {
		t.Fatalf("report: %d %v", code, out)
	}
	if asJSON(out["budget"]) != `{"amount":10000,"currency":"IRT"}` || asJSON(out["remaining"]) != `{"amount":9000,"currency":"IRT"}` {
		t.Errorf("budget %v, remaining %v", out["budget"], out["remaining"])
	}
	todayRow, _ := out["today"].(map[string]any)
	if todayRow["spins"] != float64(2) || todayRow["winners"] != float64(1) || asJSON(todayRow["paid"]) != `{"amount":1000,"currency":"IRT"}` {
		t.Errorf("today: %v", todayRow)
	}
	days := jlist(out, "days")
	if len(days) != 2 || days[0]["spins"] != float64(2) || days[1]["spins"] != float64(2) || days[1]["winners"] != float64(2) {
		t.Errorf("days: %v", days)
	}
	if total, _ := out["total"].(map[string]any); total["spins"] != float64(4) || total["winners"] != float64(3) || asJSON(total["paid"]) != `{"amount":3000,"currency":"IRT"}` {
		t.Errorf("total: %v", out["total"])
	}

	// Without the report permission there is none: the wheel's figures are revenue.
	f.staffUser(9911, "support")
	sb := f.browser()
	if code := sb.loginWithLink(9911); code != 200 {
		t.Fatalf("support login: %d", code)
	}
	if code, _ := sb.do(http.MethodGet, "/api/v1/wheel/report", nil); code != 403 {
		t.Errorf("support reads the wheel report: %d", code)
	}
}
