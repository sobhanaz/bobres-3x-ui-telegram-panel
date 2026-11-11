package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Internal orders (docs/14-reports.md, "Internal orders") through the dashboard API: the Reports page's card, the figures and the files that
// leave internal rows out or flag them, and the owner accounts' earlier purchases counted as internal with a dry run first.

func (f *fixture) execSQL(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.st.DB().Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
}

func numberOf(v any) int64 {
	n, _ := v.(float64)
	return int64(n)
}

// o2 (Sara's 250,000 order) is made internal, the way a stamped owner's purchase is.
func internalFixture(t *testing.T) (*fixture, *browser, map[string]string) {
	t.Helper()
	f, b, ids := reportFixture(t)
	f.execSQL(t, `UPDATE core.orders SET internal = true WHERE id = $1`, ids["o2"])
	f.execSQL(t, `UPDATE core.ledger_entries SET internal = true WHERE ref_id = $1`, ids["o2"])
	return f, b, ids
}

func TestReportsLeaveInternalOrdersOutAndShowThemApart(t *testing.T) {
	f, b, ids := internalFixture(t)
	t.Cleanup(func() { f.execSQL(t, `DELETE FROM core.settings WHERE key = 'reports.exclude_owner_orders'`) })

	code, out := b.do(http.MethodGet, "/api/v1/reports/summary"+reportQuery, nil)
	if code != 200 {
		t.Fatalf("summary: %d %v", code, out)
	}
	totals, _ := out["totals"].(map[string]any)
	irt := totals["currencies"].(map[string]any)["IRT"].(map[string]any)
	if numberOf(irt["revenue"]) != 100_000 || numberOf(irt["orders"]) != 1 {
		t.Errorf("the summary counts the internal order: %v", irt)
	}

	// The card: Sara's order, in Toman, with her named.
	code, card := b.do(http.MethodGet, "/api/v1/reports/internal"+reportQuery, nil)
	if code != 200 || numberOf(card["orders"]) != 1 || card["enabled"] != false {
		t.Fatalf("internal card: %d %v", code, card)
	}
	cur := card["currencies"].([]any)
	if len(cur) != 1 {
		t.Fatalf("currencies: %v", cur)
	}
	c0 := cur[0].(map[string]any)
	if c0["currency"] != "IRT" || numberOf(c0["orders"]) != 1 || numberOf(c0["amount"]) != 250_000 || numberOf(c0["refunds"]) != 0 {
		t.Errorf("currency row: %v", c0)
	}
	accounts := card["accounts"].([]any)
	if len(accounts) != 1 || accounts[0].(map[string]any)["user"].(map[string]any)["username"] != "sara" || numberOf(accounts[0].(map[string]any)["orders"]) != 1 {
		t.Errorf("accounts: %v", accounts)
	}
	// With the switch on the card says so.
	b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"reports.exclude_owner_orders": "true"}})
	if _, card = b.do(http.MethodGet, "/api/v1/reports/internal"+reportQuery, nil); card["enabled"] != true {
		t.Errorf("the card does not say the switch is on: %v", card)
	}

	// orders.csv lists every paid order and flags the internal one in its last column; summary.csv has no internal rows in its sums.
	code, _, body := b.get("/api/v1/reports/orders.csv" + reportQuery)
	if code != 200 {
		t.Fatalf("orders.csv: %d", code)
	}
	rows := parseCSV(t, body)
	last := len(rows[0]) - 1
	if rows[0][last] != "internal" || len(rows) != 4 {
		t.Fatalf("orders.csv: %v", rows)
	}
	for _, r := range rows[1:] {
		want := "false"
		if r[2] == ids["o2"] {
			want = "true"
		}
		if r[last] != want {
			t.Errorf("order %s: internal %q, want %q", r[2], r[last], want)
		}
	}
	code, _, body = b.get("/api/v1/reports/summary.csv" + reportQuery)
	rows = parseCSV(t, body)
	var revenue, orders int64
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	for _, r := range rows[1:] {
		if r[col["currency"]] == "IRT" {
			revenue += parseInt(t, r[col["revenue"]])
			orders += parseInt(t, r[col["paid_orders"]])
		}
	}
	if code != 200 || revenue != 100_000 || orders != 1 {
		t.Errorf("summary.csv counts the internal order: %d revenue %d orders %d", code, revenue, orders)
	}

	// The ledger file and list flag the entries.
	_, _, body = b.get("/api/v1/ledger.csv")
	rows = parseCSV(t, body)
	flagged := 0
	for _, r := range rows[1:] {
		if r[len(r)-1] == "true" {
			flagged++
			if r[3] != "purchase" {
				t.Errorf("a %s entry is internal", r[3])
			}
		}
	}
	if rows[0][len(rows[0])-1] != "internal" || flagged != 1 {
		t.Errorf("ledger.csv: header %v, %d flagged", rows[0], flagged)
	}
	code, list := b.do(http.MethodGet, "/api/v1/ledger?size=50", nil)
	n := 0
	for _, it := range items(list) {
		if it["internal"] == true {
			n++
		}
	}
	if code != 200 || n != 1 {
		t.Errorf("ledger list: %d, %d internal", code, n)
	}
}

func parseInt(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			t.Fatalf("not a whole number: %q", s)
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

func TestInternalCardAndActionsNeedTheirPermissions(t *testing.T) {
	f, owner, _ := internalFixture(t)
	f.staffUser(7101, "admin")
	f.staffUser(7102, "support")
	admin, support, anon := f.browser(), f.browser(), f.browser()
	if admin.loginWithLink(7101) != 200 || support.loginWithLink(7102) != 200 {
		t.Fatal("login")
	}
	card := "/api/v1/reports/internal" + reportQuery
	for name, c := range map[string]struct {
		b    *browser
		want int
	}{"owner": {owner, 200}, "admin": {admin, 200}, "support": {support, 403}, "anonymous": {anon, 401}} {
		if code, _ := c.b.do(http.MethodGet, card, nil); code != c.want {
			t.Errorf("%s card: %d, want %d", name, code, c.want)
		}
		for _, p := range []string{"backfill", "undo"} {
			body := map[string]any{"dry_run": true}
			want := c.want
			if code, out := c.b.do(http.MethodPost, "/api/v1/reports/internal/"+p, body); code != want {
				t.Errorf("%s %s: %d %v, want %d", name, p, code, out, want)
			}
		}
	}
}

func TestOwnerAccountsEarlierPurchasesAreCountedInternalAfterConfirming(t *testing.T) {
	f, b, _ := reportFixture(t)
	// Sara is an owner account: her order and top-up are the rows to move.
	f.execSQL(t, `UPDATE core.users SET role = 'owner' WHERE telegram_id = 8102`)

	revenue := func() int64 {
		_, out := b.do(http.MethodGet, "/api/v1/reports/summary"+reportQuery, nil)
		irt := out["totals"].(map[string]any)["currencies"].(map[string]any)["IRT"].(map[string]any)
		return numberOf(irt["revenue"])
	}
	if revenue() != 350_000 {
		t.Fatalf("revenue before: %d", revenue())
	}

	// The dry run says what would move and moves nothing.
	code, out := b.do(http.MethodPost, "/api/v1/reports/internal/backfill", map[string]any{"dry_run": true})
	if code != 200 || out["dry_run"] != true || numberOf(out["orders"]) != 1 || numberOf(out["ledger_entries"]) != 2 {
		t.Fatalf("dry run: %d %v", code, out)
	}
	cur := out["currencies"].([]any)[0].(map[string]any)
	if cur["currency"] != "IRT" || numberOf(cur["orders"]) != 1 || numberOf(cur["amount"]) != 250_000 {
		t.Errorf("dry run money: %v", cur)
	}
	if revenue() != 350_000 || countAudit(t, f, "reports.internal_backfill") != 0 {
		t.Fatal("the dry run changed something")
	}

	// The action itself.
	code, out = b.do(http.MethodPost, "/api/v1/reports/internal/backfill", map[string]any{})
	if code != 200 || out["dry_run"] != false || numberOf(out["orders"]) != 1 || numberOf(out["ledger_entries"]) != 2 {
		t.Fatalf("backfill: %d %v", code, out)
	}
	if revenue() != 100_000 || countAudit(t, f, "reports.internal_backfill") != 1 {
		t.Errorf("after: revenue %d", revenue())
	}
	e := lastAudit(t, b, "action=reports.internal_backfill")
	if !strings.Contains(asJSON(e["after"]), `"orders":1`) || !strings.Contains(asJSON(e["after"]), `"ledger_entries":2`) {
		t.Errorf("audit entry: %v", e)
	}
	// Asked again it finds nothing, and writes nothing.
	if _, again := b.do(http.MethodPost, "/api/v1/reports/internal/backfill", map[string]any{}); numberOf(again["orders"]) != 0 || numberOf(again["ledger_entries"]) != 0 || countAudit(t, f, "reports.internal_backfill") != 1 {
		t.Errorf("second backfill: %v", again)
	}
	if _, card := b.do(http.MethodGet, "/api/v1/reports/internal"+reportQuery, nil); numberOf(card["orders"]) != 1 {
		t.Errorf("the card: %v", card)
	}

	// Undo.
	code, out = b.do(http.MethodPost, "/api/v1/reports/internal/undo", map[string]any{})
	if code != 200 || numberOf(out["orders"]) != 1 || revenue() != 350_000 || countAudit(t, f, "reports.internal_undo") != 1 {
		t.Fatalf("undo: %d %v revenue %d", code, out, revenue())
	}
	if _, again := b.do(http.MethodPost, "/api/v1/reports/internal/undo", map[string]any{}); numberOf(again["orders"]) != 0 || countAudit(t, f, "reports.internal_undo") != 1 {
		t.Errorf("second undo: %v", again)
	}
}

func TestTheSwitchIsOnTheReportsCardOfTheSettingsPage(t *testing.T) {
	f, b, _ := reportFixture(t)
	f.execSQL(t, `DELETE FROM core.settings WHERE key = 'reports.exclude_owner_orders'`)
	t.Cleanup(func() { f.execSQL(t, `DELETE FROM core.settings WHERE key = 'reports.exclude_owner_orders'`) })
	code, out := b.do(http.MethodGet, "/api/v1/settings", nil)
	if code != 200 {
		t.Fatalf("settings: %d", code)
	}
	it := settingByKey(out, "reports.exclude_owner_orders")
	if it == nil || it["group"] != "reports" || it["kind"] != "bool" || it["default"] != "false" || it["editable"] != true {
		t.Fatalf("the switch: %v", it)
	}
	if code, _ := b.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"reports.exclude_owner_orders": "true"}}); code != 200 {
		t.Fatalf("save: %d", code)
	}
	e := lastAudit(t, b, "entity_id=reports.exclude_owner_orders")
	if e["action"] != "setting.set" || asJSON(e["after"]) != `{"value":"true"}` {
		t.Errorf("audit: %v", e)
	}
}
