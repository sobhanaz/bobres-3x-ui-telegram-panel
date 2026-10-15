package web

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// reportSeed writes sales with explicit times (the ledger is append-only, but
// an INSERT may carry any created_at).
type reportSeed struct {
	t   *testing.T
	f   *fixture
	sub map[string]string
}

func newReportSeed(t *testing.T, f *fixture) *reportSeed {
	t.Helper()
	if err := testdb.Truncate(context.Background(), f.st.DB(), `core.outbox_core`); err != nil {
		t.Fatal(err)
	}
	return &reportSeed{t: t, f: f, sub: map[string]string{}}
}

func (r *reportSeed) exec(sql string, args ...any) {
	r.t.Helper()
	if _, err := r.f.st.DB().Exec(context.Background(), sql, args...); err != nil {
		r.t.Fatalf("seed: %v\n%s", err, sql)
	}
}

func (r *reportSeed) user(tg int64, username string) string {
	r.t.Helper()
	id := buuid.MustV7().String()
	r.exec(`INSERT INTO core.users (id, telegram_id, username) VALUES ($1, $2, NULLIF($3, ''))`, id, tg, username)
	return id
}

func (r *reportSeed) plan(fa, en, cur string) string {
	r.t.Helper()
	id := buuid.MustV7().String()
	name, _ := json.Marshal(map[string]string{"fa": fa, "en": en})
	r.exec(`INSERT INTO core.plans (id, name_i18n, kind, price, currency) VALUES ($1, $2, 'both', 1000, $3)`, id, string(name), cur)
	return id
}

// paid writes a new, delivered, paid order: its row, purchase entry and event.
func (r *reportSeed) paid(user, plan, cur string, amt int64, at, source string) string {
	return r.paidWith(user, plan, cur, amt, at, source, "")
}

// paidWith is paid for an order whose event names the provider that took the
// payment ("" writes an event from before the provider was recorded).
func (r *reportSeed) paidWith(user, plan, cur string, amt int64, at, source, provider string) string {
	r.t.Helper()
	when, err := time.Parse(time.RFC3339, at)
	if err != nil {
		r.t.Fatal(err)
	}
	id := buuid.MustV7().String()
	r.exec(`INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key, created_at, updated_at)
		VALUES ($1, $2, $3, 'new', 'active', $4, $5, $6, $7, $7)`, id, user, plan, amt, cur, "k-"+id, when)
	r.exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at)
		VALUES ($1, $2, $3, $4, 'purchase', 'order', $5, $6, 0, $7)`, buuid.MustV7().String(), user, cur, -amt, id, "purchase:"+id, when)
	ev := map[string]any{"order_id": id, "user_id": user, "plan_id": plan, "amount": amt, "currency": cur, "source": source}
	if provider != "" {
		ev["provider"] = provider
	}
	payload, _ := json.Marshal(ev)
	r.exec(`INSERT INTO core.outbox_core (topic, payload, event_id, created_at) VALUES ('order.paid.v1', $1::text::jsonb, $2, $3)`,
		string(payload), buuid.MustV7().String(), when)
	return id
}

func (r *reportSeed) refund(user, order, cur string, amt int64, at string) {
	r.t.Helper()
	when, _ := time.Parse(time.RFC3339, at)
	r.exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at)
		VALUES ($1, $2, $3, $4, 'refund', 'order', $5, $6, 0, $7)`, buuid.MustV7().String(), user, cur, amt, order, "refund:"+order, when)
	r.exec(`UPDATE core.orders SET refunded_at = $2, refund_amount = $3, updated_at = $2 WHERE id = $1`, order, when, amt)
}

func (r *reportSeed) topup(user, cur string, amt int64, at string) {
	r.t.Helper()
	when, _ := time.Parse(time.RFC3339, at)
	id := buuid.MustV7().String()
	r.exec(`INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, ref_type, ref_id, idempotency_key, balance_after, created_at)
		VALUES ($1, $2, $3, $4, 'topup', 'intent', $5, $6, 0, $7)`, buuid.MustV7().String(), user, cur, amt, id, "pay-in:"+id, when)
}

// reportFixture seeds: Tehran is UTC+3:30, so 20:29:00Z on 31 Oct is 23:59 there
// and 20:31:00Z is 00:01 on 1 Nov.
func reportFixture(t *testing.T) (*fixture, *browser, map[string]string) {
	t.Helper()
	f := newFixture(t)
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	if code := b.loginWithLink(ownerTG); code != 200 {
		t.Fatalf("login: %d", code)
	}
	r := newReportSeed(t, f)
	ids := map[string]string{}
	ali := r.user(8101, "=cmd|' /C calc'!A0") // a hostile @username
	sara := r.user(8102, "sara")
	basic := r.plan("پایه", "Basic", "IRT")
	odd := r.plan("@ویژه", "=SUM(1,2)", "IRT")
	usdt := r.plan("تتر", "Pro USDT", "USDT")
	ids["basic"], ids["odd"], ids["usdt"] = basic, odd, usdt
	ids["o1"] = r.paidWith(ali, basic, "IRT", 100_000, "2026-10-31T20:29:00Z", "manual", "manual_card")
	ids["o2"] = r.paid(sara, odd, "IRT", 250_000, "2026-10-31T20:31:00Z", "wallet")
	ids["o3"] = r.paidWith(ali, usdt, "USDT", 2_500_000, "2026-11-02T10:00:00Z", "gateway", "usdt_trc20_auto")
	r.topup(sara, "IRT", 500_000, "2026-11-01T09:00:00Z")
	r.refund(ali, ids["o1"], "IRT", 100_000, "2026-11-03T09:00:00Z") // a day with no sales: net is negative
	r.exec(`INSERT INTO core.outbox_core (topic, payload, event_id, created_at) VALUES ('payment.rejected.v1',
		'{"amount":90000,"currency":"IRT","provider":"manual_card","reason":"blurry"}', $1, $2)`,
		buuid.MustV7().String(), time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC))
	r.exec(`INSERT INTO core.outbox_core (topic, payload, event_id, created_at) VALUES ('payment.rejected.v1',
		'{"amount":40000,"currency":"IRT","provider":"zarinpal","reason":"expired"}', $1, $2)`,
		buuid.MustV7().String(), time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC))
	return f, b, ids
}

const reportQuery = "?from=2026-10-31&to=2026-11-03&group=day&tz=Asia/Tehran"

func num(v any) float64 { n, _ := v.(float64); return n }

func TestReportsSummaryJSON(t *testing.T) {
	_, b, _ := reportFixture(t)
	code, out := b.do(http.MethodGet, "/api/v1/reports/summary"+reportQuery, nil)
	if code != 200 || out["tz"] != "Asia/Tehran" || out["from"] != "2026-10-31" || out["to"] != "2026-11-03" || out["group"] != "day" {
		t.Fatalf("summary: %d %v", code, out)
	}
	if cur, _ := out["currencies"].([]any); len(cur) != 2 || cur[0] != "IRT" || cur[1] != "USDT" {
		t.Errorf("currencies %v", out["currencies"])
	}
	buckets, _ := out["buckets"].([]any)
	if len(buckets) != 4 {
		t.Fatalf("%d buckets", len(buckets))
	}
	day := func(i int) (map[string]any, map[string]any, map[string]any) {
		bk := buckets[i].(map[string]any)
		c := bk["currencies"].(map[string]any)
		return bk, c["IRT"].(map[string]any), c["USDT"].(map[string]any)
	}
	// 31 Oct: the 23:59 order only (100000 IRT).
	d0, irt0, _ := day(0)
	if d0["date"] != "2026-10-31" || num(irt0["revenue"]) != 100_000 || num(irt0["orders"]) != 1 || num(d0["new_customers"]) != 1 {
		t.Errorf("31 Oct: %v", d0)
	}
	// 1 Nov: the 00:01 order, the top-up and the rejected payment; USDT is zero, not missing.
	d1, irt1, usd1 := day(1)
	if d1["date"] != "2026-11-01" || num(irt1["revenue"]) != 250_000 || num(irt1["topups"].(map[string]any)["amount"]) != 500_000 ||
		num(irt1["rejected"].(map[string]any)["count"]) != 1 || num(irt1["rejected"].(map[string]any)["amount"]) != 90_000 ||
		num(irt1["gateway_failed"].(map[string]any)["count"]) != 1 || num(irt1["gateway_failed"].(map[string]any)["amount"]) != 40_000 ||
		num(usd1["revenue"]) != 0 || num(d1["new_customers"]) != 1 {
		t.Errorf("1 Nov: %v", d1)
	}
	// 2 Nov: USDT only; IRT is a zero bucket.
	_, irt2, usd2 := day(2)
	if num(usd2["revenue"]) != 2_500_000 || num(irt2["revenue"]) != 0 || num(irt2["orders"]) != 0 {
		t.Errorf("2 Nov: irt %v usdt %v", irt2, usd2)
	}
	// 3 Nov: the refund, no sales: net is negative.
	_, irt3, _ := day(3)
	if num(irt3["refunds"].(map[string]any)["amount"]) != 100_000 || num(irt3["net"]) != -100_000 {
		t.Errorf("3 Nov: %v", irt3)
	}
	totals := out["totals"].(map[string]any)
	tc := totals["currencies"].(map[string]any)
	tirt, tusd := tc["IRT"].(map[string]any), tc["USDT"].(map[string]any)
	if num(tirt["revenue"]) != 350_000 || num(tirt["net"]) != 250_000 || num(tusd["revenue"]) != 2_500_000 || num(totals["new_customers"]) != 2 {
		t.Errorf("totals: %v", totals)
	}
	// Money in one currency never leaks into another, and amounts stay integers.
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), `"total_revenue"`) || strings.Contains(string(raw), ".0,") {
		t.Errorf("a summed or fractional amount: %s", raw)
	}
}

func TestReportsPlansAndMethodsJSON(t *testing.T) {
	f, b, ids := reportFixture(t)
	code, out := b.do(http.MethodGet, "/api/v1/reports/plans"+reportQuery, nil)
	plans, _ := out["plans"].([]any)
	if code != 200 || len(plans) != 3 {
		t.Fatalf("plans: %d %v", code, out)
	}
	first := plans[0].(map[string]any)
	if first["plan_id"] != ids["odd"] || first["currency"] != "IRT" || num(first["revenue"]) != 250_000 || num(first["orders"]) != 1 ||
		first["name"].(map[string]any)["fa"] != "@ویژه" {
		t.Errorf("first plan: %v", first)
	}
	if last := plans[2].(map[string]any); last["currency"] != "USDT" || num(last["revenue"]) != 2_500_000 {
		t.Errorf("last plan: %v", last)
	}
	code, out = b.do(http.MethodGet, "/api/v1/reports/methods"+reportQuery, nil)
	methods, _ := out["methods"].([]any)
	got := map[string]float64{}
	for _, m := range methods {
		mm := m.(map[string]any)
		prov, ok := mm["provider"].(string) // always present, "" when none
		if !ok {
			t.Errorf("a method row without a provider field: %v", mm)
		}
		got[mm["currency"].(string)+" "+mm["method"].(string)+" "+prov] = num(mm["revenue"])
	}
	if code != 200 || len(got) != 3 || got["IRT manual manual_card"] != 100_000 || got["IRT wallet "] != 250_000 ||
		got["USDT gateway usdt_trc20_auto"] != 2_500_000 {
		t.Errorf("methods: %d %v", code, got)
	}

	// Events from before the provider was recorded give the same rows, with an empty provider.
	if _, err := f.st.DB().Exec(context.Background(), `UPDATE core.outbox_core SET payload = payload - 'provider' WHERE topic = 'order.paid.v1'`); err != nil {
		t.Fatal(err)
	}
	_, out = b.do(http.MethodGet, "/api/v1/reports/methods"+reportQuery, nil)
	got = map[string]float64{}
	methods, _ = out["methods"].([]any)
	for _, m := range methods {
		mm := m.(map[string]any)
		got[mm["currency"].(string)+" "+mm["method"].(string)+" "+mm["provider"].(string)] = num(mm["revenue"])
	}
	if len(got) != 3 || got["IRT manual "] != 100_000 || got["IRT wallet "] != 250_000 || got["USDT gateway "] != 2_500_000 {
		t.Errorf("methods without providers: %v", got)
	}
}

func TestReportsRangesAndLimits(t *testing.T) {
	f, b, _ := reportFixture(t)
	// A named range is read in the store's zone: at 21:00 UTC it is already tomorrow in Tehran.
	f.api.now = func() time.Time { return time.Date(2026, 11, 2, 21, 0, 0, 0, time.UTC) }
	code, out := b.do(http.MethodGet, "/api/v1/reports/summary?range=today", nil)
	if code != 200 || out["from"] != "2026-11-03" || out["to"] != "2026-11-03" || out["tz"] != "Asia/Tehran" {
		t.Errorf("today: %d %v", code, out)
	}
	// The store's own zone (general.timezone) is the default.
	if _, err := f.st.DB().Exec(context.Background(), `INSERT INTO core.settings (key, value) VALUES ('general.timezone', '"America/New_York"')`); err != nil {
		t.Fatal(err)
	}
	if _, out = b.do(http.MethodGet, "/api/v1/reports/summary?range=today", nil); out["from"] != "2026-11-02" || out["tz"] != "America/New_York" {
		t.Errorf("setting zone: %v", out)
	}
	if _, out = b.do(http.MethodGet, "/api/v1/reports/summary?range=today&tz=Asia/Tehran", nil); out["from"] != "2026-11-03" {
		t.Errorf("request zone: %v", out)
	}
	for name, q := range map[string]string{
		"401 days":      "?from=2026-01-01&to=2027-02-05",
		"from after to": "?from=2026-11-03&to=2026-11-01",
		"bad date":      "?from=nope&to=2026-11-01",
		"bad group":     "?from=2026-10-01&to=2026-11-01&group=year",
		"bad zone":      "?from=2026-10-01&to=2026-11-01&tz=Mars/Base",
		"bad range":     "?range=forever",
		"both":          "?range=today&from=2026-10-01&to=2026-11-01",
		"half":          "?from=2026-10-01",
	} {
		for _, path := range []string{"summary", "plans", "methods", "summary.csv", "orders.csv"} {
			if code, out := b.do(http.MethodGet, "/api/v1/reports/"+path+q, nil); code != 400 || out["error"] != "invalid" {
				t.Errorf("%s %s: %d %v", name, path, code, out)
			}
		}
	}
	// 400 days exactly is allowed.
	if code, out := b.do(http.MethodGet, "/api/v1/reports/summary?from=2026-01-01&to=2027-02-04", nil); code != 200 || len(out["buckets"].([]any)) != 400 {
		t.Errorf("400 days: %d", code)
	}
	// Weeks start on Monday.
	_, out = b.do(http.MethodGet, "/api/v1/reports/summary?from=2026-10-31&to=2026-11-03&group=week&tz=Asia/Tehran", nil)
	weeks := out["buckets"].([]any)
	if len(weeks) != 2 || weeks[0].(map[string]any)["date"] != "2026-10-26" || weeks[0].(map[string]any)["partial"] != true ||
		weeks[1].(map[string]any)["date"] != "2026-11-02" {
		t.Errorf("weeks: %v", weeks)
	}
}

func TestReportsNeedThePermission(t *testing.T) {
	f, owner, _ := reportFixture(t)
	f.staffUser(7101, "admin")
	f.staffUser(7102, "support")
	admin, support, anon := f.browser(), f.browser(), f.browser()
	if admin.loginWithLink(7101) != 200 || support.loginWithLink(7102) != 200 {
		t.Fatal("login")
	}
	paths := []string{"summary", "plans", "methods", "summary.csv", "orders.csv"}
	for _, p := range paths {
		url := "/api/v1/reports/" + p + reportQuery
		if code, _ := owner.do(http.MethodGet, url, nil); code != 200 {
			t.Errorf("owner %s: %d", p, code)
		}
		if code, _ := admin.do(http.MethodGet, url, nil); code != 200 {
			t.Errorf("admin %s: %d", p, code)
		}
		if code, out := support.do(http.MethodGet, url, nil); code != 403 {
			t.Errorf("support %s: %d %v", p, code, out)
		}
		if code, _ := anon.do(http.MethodGet, url, nil); code != 401 {
			t.Errorf("anonymous %s: %d", p, code)
		}
	}
	// Support's own permission list does not hold it, so the dashboard hides the page.
	if _, me := support.do(http.MethodGet, "/api/v1/me", nil); strings.Contains(asJSON(me["permissions"]), "reports.read") {
		t.Errorf("support sees reports.read: %v", me["permissions"])
	}
	if _, me := admin.do(http.MethodGet, "/api/v1/me", nil); !strings.Contains(asJSON(me["permissions"]), "reports.read") {
		t.Errorf("admin lacks reports.read: %v", me["permissions"])
	}
	// A reseller is no staff: it has no permission and cannot even log in.
	for role, want := range map[string]bool{"owner": true, "admin": true, "support": false, "reseller": false, "user": false} {
		if Allowed(role, PermReportsRead) != want {
			t.Errorf("%s: Allowed = %v", role, !want)
		}
	}
	f.staffUser(7103, "reseller")
	if _, _, err := f.dom.CreateLoginLink(context.Background(), 7103); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("a reseller got a dashboard login link: %v", err)
	}
	// Reports read, they never write: no POST route.
	if code, _ := owner.do(http.MethodPost, "/api/v1/reports/summary"+reportQuery, map[string]string{}); code == 200 {
		t.Error("POST reports answered 200")
	}
}

// get fetches a CSV with the browser's session.
func (b *browser) get(path string) (int, http.Header, []byte) {
	b.f.t.Helper()
	resp, err := b.c.Get(b.f.srv.URL + path)
	if err != nil {
		b.f.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

func parseCSV(t *testing.T, body []byte) [][]string {
	t.Helper()
	if !bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("no byte-order mark: %q", body[:min(len(body), 20)])
	}
	r := csv.NewReader(bytes.NewReader(body[3:]))
	r.FieldsPerRecord = -1 // a note line is one cell
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv: %v\n%s", err, body)
	}
	return rows
}

func TestReportsOrdersCSV(t *testing.T) {
	f, b, ids := reportFixture(t)
	code, h, body := b.get("/api/v1/reports/orders.csv" + reportQuery)
	if code != 200 || h.Get("Content-Type") != "text/csv; charset=utf-8" ||
		h.Get("Content-Disposition") != `attachment; filename="reports-orders-2026-10-31-to-2026-11-03.csv"` || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %d %v", code, h)
	}
	rows := parseCSV(t, body)
	if len(rows) != 4 || strings.Join(rows[0], ",") !=
		"paid_at_utc,paid_at_local,order_id,type,telegram_id,username,plan_fa,plan_en,amount,currency,paid_with,refunded_amount,provider" {
		t.Fatalf("rows: %v", rows)
	}
	// Oldest first, local times either side of midnight, money as plain decimals.
	r1, r2, r3 := rows[1], rows[2], rows[3]
	if r1[0] != "2026-10-31T20:29:00Z" || r1[1] != "2026-10-31 23:59:00" || r1[2] != ids["o1"] || r1[8] != "100000" || r1[9] != "IRT" ||
		r1[10] != "manual" || r1[11] != "100000" || r1[12] != "manual_card" {
		t.Errorf("first order: %v", r1)
	}
	if r2[1] != "2026-11-01 00:01:00" || r2[10] != "wallet" || r2[11] != "0" || r2[12] != "" {
		t.Errorf("second order: %v", r2)
	}
	if r3[8] != "2.500000" || r3[9] != "USDT" || r3[10] != "gateway" || r3[12] != "usdt_trc20_auto" {
		t.Errorf("third order: %v", r3)
	}
	// Persian text survives; a cell that could run as a formula gets a quote in front.
	if r1[6] != "پایه" || r1[7] != "Basic" {
		t.Errorf("plan names: %q %q", r1[6], r1[7])
	}
	if r1[5] != "'=cmd|' /C calc'!A0" || r2[6] != "'@ویژه" || r2[7] != "'=SUM(1,2)" {
		t.Errorf("formulas not quoted: %q %q %q", r1[5], r2[6], r2[7])
	}
	for _, row := range rows {
		for _, c := range row {
			if c != "" && strings.ContainsRune("=+-@\t\r", rune(c[0])) { // an export of orders has no negative number
				t.Errorf("a cell can run as a formula: %q", c)
			}
		}
	}
	// The export is audited with its range.
	var detail string
	if err := f.st.DB().QueryRow(context.Background(), `SELECT after::text FROM core.audit_log WHERE action = 'export.reports' ORDER BY created_at DESC LIMIT 1`).Scan(&detail); err != nil {
		t.Fatalf("no audit entry: %v", err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(detail), &d); err != nil || d["kind"] != "orders" || d["from"] != "2026-10-31" || d["to"] != "2026-11-03" || d["tz"] != "Asia/Tehran" ||
		d["outcome"] != "complete" || d["rows"] != float64(3) {
		t.Errorf("audit detail %s", detail)
	}
}

func TestReportsSummaryCSV(t *testing.T) {
	f, b, _ := reportFixture(t)
	code, h, body := b.get("/api/v1/reports/summary.csv" + reportQuery)
	if code != 200 || h.Get("Content-Type") != "text/csv; charset=utf-8" ||
		h.Get("Content-Disposition") != `attachment; filename="reports-summary-2026-10-31-to-2026-11-03-by-day.csv"` {
		t.Fatalf("headers: %d %v", code, h)
	}
	rows := parseCSV(t, body)
	// 4 days: one people row and one row per currency (IRT, USDT) each.
	if len(rows) != 1+4*3 || rows[0][0] != "period_start" || len(rows[0]) != 21 {
		t.Fatalf("%d rows: %v", len(rows), rows)
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	byKey := map[string][]string{}
	for _, r := range rows[1:] {
		if len(r) != 21 {
			t.Fatalf("a row has %d cells: %v", len(r), r)
		}
		byKey[r[0]+"|"+r[2]] = r
	}
	// People rows have no currency and no money; currency rows have money and no people.
	if p := byKey["2026-10-31|"]; p == nil || p[col["new_customers"]] != "1" || p[col["free_trials"]] != "0" || p[col["revenue"]] != "" {
		t.Errorf("people row: %v", p)
	}
	if c := byKey["2026-10-31|IRT"]; c == nil || c[col["revenue"]] != "100000" || c[col["new_customers"]] != "" || c[col["paid_orders"]] != "1" {
		t.Errorf("IRT row: %v", c)
	}
	if c := byKey["2026-11-02|USDT"]; c == nil || c[col["revenue"]] != "2.500000" || c[col["paid_orders"]] != "1" {
		t.Errorf("USDT row: %v", c)
	}
	// Empty buckets are rows of zeros; a negative net stays a plain number (it is no text to quote).
	if c := byKey["2026-11-02|IRT"]; c == nil || c[col["revenue"]] != "0" || c[col["paid_orders"]] != "0" {
		t.Errorf("empty bucket: %v", c)
	}
	if c := byKey["2026-11-03|IRT"]; c == nil || c[col["net"]] != "-100000" || c[col["refund_amount"]] != "100000" || c[col["refund_count"]] != "1" {
		t.Errorf("refund day: %v", c)
	}
	if c := byKey["2026-11-01|IRT"]; c[col["topup_amount"]] != "500000" || c[col["rejected_count"]] != "1" || c[col["rejected_amount"]] != "90000" ||
		c[col["gateway_failed_count"]] != "1" || c[col["gateway_failed_amount"]] != "40000" {
		t.Errorf("1 Nov IRT: %v", c)
	}
	var kind string
	if err := f.st.DB().QueryRow(context.Background(), `SELECT after->>'kind' FROM core.audit_log WHERE action = 'export.reports' ORDER BY created_at DESC LIMIT 1`).Scan(&kind); err != nil || kind != "summary" {
		t.Errorf("audit: %q %v", kind, err)
	}
}

func TestCSVTextQuotesWhatCouldRunAsAFormula(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1": "'=1+1", "+1": "'+1", "-1": "'-1", "@a": "'@a", "\tx": "'\tx", "\rx": "'\rx",
		"": "", "plain": "plain", "پایه": "پایه", "a=b": "a=b", "1+1": "1+1",
	} {
		if got := csvText(in); got != want {
			t.Errorf("csvText(%q) = %q, want %q", in, got, want)
		}
	}
}

// What the domain says about an empty store: a 200 with zeros, not an error.
func TestReportsOnAnEmptyStore(t *testing.T) {
	f := newFixture(t)
	f.staffUser(ownerTG, "owner")
	b := f.browser()
	if b.loginWithLink(ownerTG) != 200 {
		t.Fatal("login")
	}
	if err := testdb.Truncate(context.Background(), f.st.DB(), `core.outbox_core`); err != nil {
		t.Fatal(err)
	}
	code, out := b.do(http.MethodGet, "/api/v1/reports/summary?from=2026-10-01&to=2026-10-03", nil)
	if code != 200 || len(out["buckets"].([]any)) != 3 || len(out["currencies"].([]any)) != 0 {
		t.Errorf("summary: %d %v", code, out)
	}
	if code, out = b.do(http.MethodGet, "/api/v1/reports/plans?from=2026-10-01&to=2026-10-03", nil); code != 200 || len(out["plans"].([]any)) != 0 {
		t.Errorf("plans: %d %v", code, out)
	}
	_, _, body := b.get("/api/v1/reports/orders.csv?from=2026-10-01&to=2026-10-03")
	if rows := parseCSV(t, body); len(rows) != 1 {
		t.Errorf("orders csv: %v", rows)
	}
}

// Revenue is not for support: the overview leaves it out of their answer.
func TestOverviewRevenueIsForThoseWhoReadReports(t *testing.T) {
	f, owner, _ := reportFixture(t)
	f.api.cfg.Payments, f.api.cfg.Provisioner = &fakePay{}, &fakeProv{}
	f.staffUser(7201, "support")
	f.staffUser(7202, "admin")
	support, admin := f.browser(), f.browser()
	if support.loginWithLink(7201) != 200 || admin.loginWithLink(7202) != 200 {
		t.Fatal("login")
	}
	for name, b := range map[string]*browser{"owner": owner, "admin": admin} {
		code, out := b.do(http.MethodGet, "/api/v1/overview", nil)
		rev, _ := out["revenue"].(map[string]any)
		if code != 200 || rev == nil || rev["day"] == nil || rev["month"] == nil {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	code, out := support.do(http.MethodGet, "/api/v1/overview", nil)
	if code != 200 || out["users"] == nil || out["orders"] == nil {
		t.Fatalf("support's overview: %d %v", code, out)
	}
	if _, has := out["revenue"]; has || strings.Contains(asJSON(out), "revenue") {
		t.Errorf("support sees revenue: %v", out["revenue"])
	}
}

// An export that fails before its first row is an error answer, not a 200 with
// a header that looks like "no orders"; a report that cannot finish in time says
// to choose a shorter range. A lock on the ledger stops every query until
// PostgreSQL cancels it.
func TestReportsThatRunOutOfTime(t *testing.T) {
	f, b, _ := reportFixture(t)
	oldReport, oldExport := store.ReportTimeout, store.ExportTimeout
	store.ReportTimeout, store.ExportTimeout = 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { store.ReportTimeout, store.ExportTimeout = oldReport, oldExport })
	tx, err := f.st.DB().Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = tx.Rollback(context.Background())
		}
	}
	defer release()
	if _, err := tx.Exec(context.Background(), `LOCK TABLE core.ledger_entries IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"summary", "plans", "methods"} {
		if code, out := b.do(http.MethodGet, "/api/v1/reports/"+path+reportQuery, nil); code != 503 || out["error"] != "timeout" || !strings.Contains(asJSON(out["message"]), "shorter range") {
			t.Errorf("%s: %d %v", path, code, out)
		}
	}
	for _, path := range []string{"orders.csv", "summary.csv"} {
		code, h, body := b.get("/api/v1/reports/" + path + reportQuery)
		if code != 503 || !strings.HasPrefix(h.Get("Content-Type"), "application/json") || bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) ||
			h.Get("Content-Disposition") != "" || !strings.Contains(string(body), `"timeout"`) {
			t.Errorf("%s: %d %v %q", path, code, h, body)
		}
	}
	release()
	// The failures were audited as failed, and the same requests work once the lock is gone.
	var failed int
	if err := f.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.audit_log WHERE action = 'export.reports' AND after->>'outcome' = 'failed'`).Scan(&failed); err != nil || failed != 2 {
		t.Errorf("failed exports audited: %d %v", failed, err)
	}
	if code, _, body := b.get("/api/v1/reports/orders.csv" + reportQuery); code != 200 || len(parseCSV(t, body)) != 4 {
		t.Errorf("after the lock: %d %q", code, body)
	}
}

// An export that dies after rows went out ends with a line that says so, and is
// audited as incomplete. (The export window is shortened and each row paused, so
// the deadline falls between rows.)
func TestAnExportThatDiesHalfWayEndsWithANote(t *testing.T) {
	f, b, _ := reportFixture(t)
	f.api.exportTime, f.api.rowPause = 500*time.Millisecond, 300*time.Millisecond
	code, _, body := b.get("/api/v1/reports/orders.csv" + reportQuery)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	rows := parseCSV(t, body)
	last := rows[len(rows)-1]
	if len(rows) < 3 || len(rows) >= 5 || len(last) != 1 || !strings.HasPrefix(last[0], "# export incomplete: it took longer than 500ms: choose a shorter range") {
		t.Fatalf("a short export must end with a note: %v", rows)
	}
	var outcome string
	var n int
	if err := f.st.DB().QueryRow(context.Background(), `SELECT after->>'outcome', (after->>'rows')::int FROM core.audit_log WHERE action = 'export.reports' ORDER BY created_at DESC LIMIT 1`).Scan(&outcome, &n); err != nil || outcome != "incomplete" || n < 1 {
		t.Errorf("audit: %q %d %v", outcome, n, err)
	}
	// A complete export is audited as complete, with its rows.
	f.api.exportTime, f.api.rowPause = 0, 0
	b.get("/api/v1/reports/orders.csv" + reportQuery)
	if err := f.st.DB().QueryRow(context.Background(), `SELECT after->>'outcome', (after->>'rows')::int FROM core.audit_log WHERE action = 'export.reports' ORDER BY created_at DESC LIMIT 1`).Scan(&outcome, &n); err != nil || outcome != "complete" || n != 3 {
		t.Errorf("audit of a whole export: %q %d %v", outcome, n, err)
	}
}

// core's HTTP server cuts a response at 30 seconds and a request's context at
// its read limit (internal/health): an export may take longer. The server
// here has 300 ms limits, the export is paused 250 ms per row (three rows), and
// the whole file arrives.
func TestASlowExportIsNotCutByTheServersLimits(t *testing.T) {
	f, b, _ := reportFixture(t)
	api := New(f.api.cfg)
	api.exportTime, api.rowPause = 5*time.Second, 250*time.Millisecond
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 300*time.Millisecond, 300*time.Millisecond
	srv.StartTLS()
	defer srv.Close()
	start := time.Now()
	resp, err := b.c.Get(srv.URL + "/api/v1/reports/orders.csv" + reportQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("a slow export was cut: %d %v", resp.StatusCode, err)
	}
	rows := parseCSV(t, body)
	if len(rows) != 4 || strings.HasPrefix(rows[len(rows)-1][0], "#") || time.Since(start) < 700*time.Millisecond {
		t.Errorf("%d rows after %s: %v", len(rows), time.Since(start), rows)
	}
}

// Two exports run at once; a third is told to wait (429, Retry-After, in both
// languages), and goes through when a place is free.
func TestAtMostTwoExportsRunAtOnce(t *testing.T) {
	f, b, _ := reportFixture(t)
	f.api.exports <- struct{}{}
	f.api.exports <- struct{}{}
	for _, path := range []string{"orders.csv", "summary.csv"} {
		code, h, body := b.get("/api/v1/reports/" + path + reportQuery)
		if code != 429 || h.Get("Retry-After") != "5" || !strings.Contains(string(body), "rate_limited") ||
			!strings.Contains(string(body), "try again in a few seconds") || !strings.Contains(string(body), "چند ثانیه") {
			t.Errorf("%s: %d %v %q", path, code, h, body)
		}
	}
	// The JSON reports are not exports: no place is needed.
	if code, _ := b.do(http.MethodGet, "/api/v1/reports/summary"+reportQuery, nil); code != 200 {
		t.Errorf("summary: %d", code)
	}
	<-f.api.exports
	if code, _, body := b.get("/api/v1/reports/orders.csv" + reportQuery); code != 200 || len(parseCSV(t, body)) != 4 {
		t.Errorf("with a free place: %d", code)
	}
	if len(f.api.exports) != 1 {
		t.Errorf("an export kept its place: %d taken", len(f.api.exports))
	}
}

// The days of America/Havana's 1 Nov 2026 (00:xx twice) cannot make a report
// answer 500: every number lands in a bucket the range holds.
func TestReportsAnswerOnAnAmbiguousMidnight(t *testing.T) {
	f, b, _ := reportFixture(t)
	r := newReportSeed(t, f)
	u := r.user(8190, "havana")
	plan := r.plan("پایه", "Basic", "IRT")
	r.paid(u, plan, "IRT", 1000, "2026-11-01T04:30:00Z", "wallet") // 1 Nov 00:30, the first time
	r.paid(u, plan, "IRT", 2000, "2026-11-01T05:30:00Z", "wallet") // 1 Nov 00:30, the second time
	for _, q := range []string{"?from=2026-10-31&to=2026-10-31", "?from=2026-11-01&to=2026-11-01", "?from=2026-10-30&to=2026-11-02&group=week"} {
		code, out := b.do(http.MethodGet, "/api/v1/reports/summary"+q+"&tz=America/Havana", nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", q, code, out)
		}
		if q == "?from=2026-11-01&to=2026-11-01" {
			irt := out["totals"].(map[string]any)["currencies"].(map[string]any)["IRT"].(map[string]any)
			if num(irt["revenue"]) != 3000 || num(irt["orders"]) != 2 {
				t.Errorf("1 Nov in Havana: %v", irt)
			}
		}
	}
}

func TestReportTextQuotesWhatCouldRunAsAFormulaEvenAfterSpaces(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1": "'=1+1", "+1": "'+1", "-1": "'-1", "@a": "'@a", "\tx": "'\tx", "\rx": "'\rx",
		" =1+1": "' =1+1", "  @SUM(A1)": "'  @SUM(A1)", "\n-2": "'\n-2", " \t +1": "' \t +1",
		"": "", "plain": "plain", "پایه": "پایه", "a=b": "a=b", " 1+1": " 1+1", " plain": " plain", "  ": "  ",
	} {
		if got := reportText(in); got != want {
			t.Errorf("reportText(%q) = %q, want %q", in, got, want)
		}
	}
	// ledger.csv keeps its own rule.
	if csvText(" =1+1") != " =1+1" {
		t.Error("csvText changed")
	}
}

// A sale with no record of how it was paid is listed as "unknown", so the
// methods always add up to the revenue.
func TestReportsMethodsListAnUnknownSale(t *testing.T) {
	f, b, ids := reportFixture(t)
	if _, err := f.st.DB().Exec(context.Background(), `DELETE FROM core.outbox_core WHERE topic = 'order.paid.v1' AND payload->>'order_id' = $1`, ids["o2"]); err != nil {
		t.Fatal(err)
	}
	_, out := b.do(http.MethodGet, "/api/v1/reports/methods"+reportQuery, nil)
	got := map[string]float64{}
	var irt float64
	for _, m := range out["methods"].([]any) {
		mm := m.(map[string]any)
		got[mm["currency"].(string)+" "+mm["method"].(string)] = num(mm["revenue"])
		if mm["currency"] == "IRT" {
			irt += num(mm["revenue"])
		}
	}
	if got["IRT unknown"] != 250_000 || got["IRT manual"] != 100_000 || irt != 350_000 || got["IRT wallet"] != 0 {
		t.Errorf("methods: %v", got)
	}
}

// ?calendar=jalali cuts weeks on Saturday and months on Jalali day 1; a bucket's
// date stays its Gregorian start date, and day buckets do not change.
func TestReportsJalaliCalendar(t *testing.T) {
	f, b, _ := reportFixture(t)
	// 31 Oct 2026 is a Saturday and 9 Aban 1405; 1 Aban is 23 Oct, 1 Azar 22 Nov.
	get := func(q string) map[string]any {
		t.Helper()
		code, out := b.do(http.MethodGet, "/api/v1/reports/summary?tz=Asia/Tehran&"+q, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", q, code, out)
		}
		return out
	}
	// The default is Gregorian, and the answer says which calendar it used.
	if out := get("from=2026-10-31&to=2026-11-03&group=month"); out["calendar"] != "gregorian" {
		t.Errorf("default calendar %v", out["calendar"])
	}
	out := get("from=2026-10-31&to=2026-11-03&group=month&calendar=jalali")
	buckets := out["buckets"].([]any)
	if out["calendar"] != "jalali" || out["group"] != "month" || len(buckets) != 1 {
		t.Fatalf("month: %v", out)
	}
	m := buckets[0].(map[string]any)
	irt, usd := m["currencies"].(map[string]any)["IRT"].(map[string]any), m["currencies"].(map[string]any)["USDT"].(map[string]any)
	if m["date"] != "2026-10-23" || m["partial"] != true || num(irt["revenue"]) != 350_000 || num(irt["net"]) != 250_000 ||
		num(irt["topups"].(map[string]any)["amount"]) != 500_000 || num(usd["revenue"]) != 2_500_000 || num(m["new_customers"]) != 2 {
		t.Errorf("Aban: %v", m)
	}
	// A whole Jalali month is not partial; the same month in the Gregorian calendar is cut in two.
	out = get("from=2026-10-23&to=2026-11-21&group=month&calendar=jalali")
	if bs := out["buckets"].([]any); len(bs) != 1 || bs[0].(map[string]any)["partial"] != false || bs[0].(map[string]any)["date"] != "2026-10-23" {
		t.Errorf("whole Aban: %v", out["buckets"])
	}
	if bs := get("from=2026-10-23&to=2026-11-21&group=month")["buckets"].([]any); len(bs) != 2 {
		t.Errorf("Gregorian: %v", bs)
	}
	// Weeks start on Saturday (the 31st is one; the Monday of its week is the 26th).
	weeks := get("from=2026-10-30&to=2026-11-03&group=week&calendar=jalali")["buckets"].([]any)
	if len(weeks) != 2 || weeks[0].(map[string]any)["date"] != "2026-10-24" || weeks[1].(map[string]any)["date"] != "2026-10-31" ||
		weeks[0].(map[string]any)["partial"] != true || weeks[1].(map[string]any)["partial"] != true {
		t.Errorf("weeks: %v", weeks)
	}
	w1 := weeks[1].(map[string]any)["currencies"].(map[string]any)["IRT"].(map[string]any)
	w0 := weeks[0].(map[string]any)["currencies"].(map[string]any)["IRT"].(map[string]any)
	if num(w1["revenue"]) != 350_000 || num(w1["refunds"].(map[string]any)["amount"]) != 100_000 || num(w0["revenue"]) != 0 {
		t.Errorf("the week of 31 Oct: %v, the week before: %v", w1, w0)
	}
	// Days are the same in both calendars.
	jd, gd := get("from=2026-10-31&to=2026-11-03&group=day&calendar=jalali"), get("from=2026-10-31&to=2026-11-03&group=day")
	if !reflect.DeepEqual(jd["buckets"], gd["buckets"]) || !reflect.DeepEqual(jd["totals"], gd["totals"]) {
		t.Errorf("day buckets differ:\n%v\n%v", jd["buckets"], gd["buckets"])
	}
	// Named months follow the calendar: now is 2 Nov 2026 (11 Aban).
	f.api.now = func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }
	if out = get("range=this_month&group=month&calendar=jalali"); out["from"] != "2026-10-23" || out["to"] != "2026-11-02" {
		t.Errorf("this month: %v %v", out["from"], out["to"])
	}
	if out = get("range=last_month&group=month&calendar=jalali"); out["from"] != "2026-09-23" || out["to"] != "2026-10-22" {
		t.Errorf("last month: %v %v", out["from"], out["to"])
	}
	// An unknown calendar is a 400 everywhere, with the reason.
	for _, path := range []string{"summary", "plans", "methods", "summary.csv", "orders.csv"} {
		code, out := b.do(http.MethodGet, "/api/v1/reports/"+path+"?from=2026-10-01&to=2026-11-01&calendar=julian", nil)
		if code != 400 || out["error"] != "invalid" || !strings.Contains(out["message"].(string), "calendar must be gregorian or jalali") {
			t.Errorf("%s: %d %v", path, code, out)
		}
	}
}

func TestReportsJalaliCSVNameAndAudit(t *testing.T) {
	f, b, _ := reportFixture(t)
	disposition := func(path string) string {
		t.Helper()
		code, h, body := b.get(path)
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		return h.Get("Content-Disposition")
	}
	last := func() (calendar, group string) {
		t.Helper()
		if err := f.st.DB().QueryRow(context.Background(), `SELECT after->>'calendar', after->>'group' FROM core.audit_log WHERE action = 'export.reports' ORDER BY created_at DESC LIMIT 1`).Scan(&calendar, &group); err != nil {
			t.Fatal(err)
		}
		return
	}
	q := "?from=2026-10-31&to=2026-11-03&tz=Asia/Tehran"
	if got := disposition("/api/v1/reports/summary.csv" + q + "&group=month&calendar=jalali"); got != `attachment; filename="reports-summary-2026-10-31-to-2026-11-03-by-month-jalali.csv"` {
		t.Errorf("Jalali name: %s", got)
	}
	if cal, group := last(); cal != "jalali" || group != "month" {
		t.Errorf("audit calendar %q group %q", cal, group)
	}
	// Gregorian (named or not) keeps the old name.
	for _, extra := range []string{"&group=month", "&group=month&calendar=gregorian"} {
		if got := disposition("/api/v1/reports/summary.csv" + q + extra); got != `attachment; filename="reports-summary-2026-10-31-to-2026-11-03-by-month.csv"` {
			t.Errorf("Gregorian name: %s", got)
		}
		if cal, _ := last(); cal != "gregorian" {
			t.Errorf("audit calendar %q", cal)
		}
	}
	// The file holds the Jalali bucket: one people row and one row per currency.
	_, _, body := b.get("/api/v1/reports/summary.csv" + q + "&group=week&calendar=jalali")
	rows := parseCSV(t, body)
	if len(rows) != 1+1*3 || rows[1][0] != "2026-10-31" || rows[1][1] != "true" {
		t.Errorf("Jalali weeks CSV: %v", rows)
	}
	// The orders file has no buckets: its name is unchanged, and its audit entry names the calendar.
	if got := disposition("/api/v1/reports/orders.csv" + q + "&calendar=jalali"); got != `attachment; filename="reports-orders-2026-10-31-to-2026-11-03.csv"` {
		t.Errorf("orders name: %s", got)
	}
	if cal, _ := last(); cal != "jalali" {
		t.Errorf("orders audit calendar %q", cal)
	}
}
