package web

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Every CSV file core makes, checked as a spreadsheet will see it: the same
// headers, one kind of line break, numbers that stay numbers, text that cannot
// run as a formula, dates written by one rule, and nothing secret in them.

// csvKinds are the four files, each with a query that selects rows worth looking at.
var csvKinds = []struct {
	name, path string
	// header is the file's contract: these columns, in this order, in English. A
	// column is only ever added at the end; changing this list is a decision.
	header []string
	// numbers are the columns whose cells are numbers (or empty): they are never quoted.
	numbers []string
}{
	{
		"summary", "/api/v1/reports/summary.csv" + reportQuery,
		[]string{"period_start", "partial_period", "currency", "revenue", "refund_amount", "net", "paid_orders", "new_orders", "renewals",
			"traffic_topups", "topup_count", "topup_amount", "refund_count", "rejected_count", "rejected_amount", "gateway_failed_count",
			"gateway_failed_amount", "cancelled_count", "cancelled_amount", "new_customers", "free_trials", "churned_services",
			"churned_customers", "at_risk_services", "group", "calendar", "tz", "license_orders", "license_revenue", "digital_orders", "digital_revenue"},
		[]string{"revenue", "refund_amount", "net", "paid_orders", "new_orders", "renewals", "traffic_topups", "topup_count", "topup_amount",
			"refund_count", "rejected_count", "rejected_amount", "gateway_failed_count", "gateway_failed_amount", "cancelled_count",
			"cancelled_amount", "new_customers", "free_trials", "churned_services", "churned_customers", "at_risk_services",
			"license_orders", "license_revenue", "digital_orders", "digital_revenue"},
	},
	{
		"orders", "/api/v1/reports/orders.csv" + reportQuery,
		[]string{"paid_at_utc", "paid_at_local", "order_id", "type", "telegram_id", "username", "plan_fa", "plan_en", "amount", "currency",
			"paid_with", "refunded_amount", "provider", "tz", "list_amount", "list_currency", "rate"},
		[]string{"telegram_id", "amount", "refunded_amount", "list_amount", "rate"},
	},
	{
		"ledger", "/api/v1/ledger.csv",
		[]string{"time_utc", "telegram_id", "username", "kind", "amount", "currency", "balance_after", "ref_type", "ref_id", "entry_id",
			"time_local", "tz"},
		[]string{"telegram_id", "amount", "balance_after"},
	},
	{
		"audit", "/api/v1/audit.csv?action=seed.entry",
		[]string{"time_utc", "actor_telegram_id", "actor_username", "actor_role", "action", "entity", "entity_id", "reason", "ip", "source",
			"before", "after", "entry_id", "time_local", "tz"},
		[]string{"actor_telegram_id"},
	},
}

var (
	plainNumber = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	fileName    = regexp.MustCompile(`^attachment; filename="[A-Za-z0-9._-]+\.csv"$`)
)

// seedHostile writes audit entries whose text a spreadsheet could misread.
func seedHostile(t *testing.T, f *fixture) {
	t.Helper()
	id := "=1+1"
	reason := "first line\r\nsecond, with \"quotes\"\nthird\rfourth"
	if err := f.st.WriteAudit(context.Background(), f.st.Conn(), &store.Audit{Action: "seed.entry", Entity: "seed", EntityID: &id, Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	id2, reason2 := "\u200f-2+3", " @SUM(A1)"
	if err := f.st.WriteAudit(context.Background(), f.st.Conn(), &store.Audit{Action: "seed.entry", Entity: "seed", EntityID: &id2, Reason: &reason2}); err != nil {
		t.Fatal(err)
	}
}

func TestEveryCSVIsASpreadsheetFile(t *testing.T) {
	f, b, _ := reportFixture(t)
	seedHostile(t, f)
	for _, k := range csvKinds {
		code, h, body := b.get(k.path)
		if code != 200 {
			t.Fatalf("%s: %d %s", k.name, code, body)
		}
		// Excel and Persian: UTF-8 with a byte-order mark and a charset, a download with a name of safe characters
		if h.Get("Content-Type") != "text/csv; charset=utf-8" || !fileName.MatchString(h.Get("Content-Disposition")) {
			t.Errorf("%s headers: %v", k.name, h)
		}
		if h.Get("Cache-Control") != "no-store" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s may be cached or sniffed: %v", k.name, h)
		}
		if !bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
			t.Fatalf("%s: no byte-order mark", k.name)
		}
		// one kind of line break: \r\n after every row, and inside a cell
		text := string(body[3:])
		if strings.Count(text, "\n") != strings.Count(text, "\r\n") || strings.Count(text, "\r") != strings.Count(text, "\r\n") {
			t.Errorf("%s mixes line breaks: %d LF, %d CR, %d CRLF", k.name, strings.Count(text, "\n"), strings.Count(text, "\r"), strings.Count(text, "\r\n"))
		}
		rows := parseCSV(t, body)
		if len(rows) < 2 || !reflect.DeepEqual(rows[0], k.header) {
			t.Fatalf("%s: headers are the file's contract\n got %v\nwant %v", k.name, rows[0], k.header)
		}
		for i, r := range rows[1:] {
			if len(r) != len(k.header) {
				t.Fatalf("%s row %d has %d cells, the header %d: %v", k.name, i+1, len(r), len(k.header), r)
			}
		}
	}
}

// A number is a number: never quoted, no thousands separator, a dot for the
// decimal point, a minus sign kept. A text can never start like a formula.
func TestCSVNumbersStayNumbersAndTextsStayTexts(t *testing.T) {
	f, b, _ := reportFixture(t)
	seedHostile(t, f)
	sawNegative := false
	for _, k := range csvKinds {
		_, _, body := b.get(k.path)
		rows := parseCSV(t, body)
		isNumber := map[string]bool{}
		for _, n := range k.numbers {
			isNumber[n] = true
		}
		for _, r := range rows[1:] {
			for i, cell := range r {
				col := rows[0][i]
				switch {
				case isNumber[col]:
					if cell != "" && !plainNumber.MatchString(cell) {
						t.Errorf("%s.%s: %q is not a plain number", k.name, col, cell)
					}
					if strings.HasPrefix(cell, "-") {
						sawNegative = true
					}
				case cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])):
					t.Errorf("%s.%s: %q can run as a formula", k.name, col, cell)
				}
			}
		}
	}
	// the fixture does have a negative amount (a purchase in the ledger, a refund day's net), and it is not quoted
	if !sawNegative {
		t.Error("no negative amount was checked")
	}
	_, _, body := b.get("/api/v1/ledger.csv?kind=purchase&currency=IRT")
	if rows := parseCSV(t, body); len(rows) != 3 || rows[1][4] != "-250000" || rows[2][4] != "-100000" {
		t.Errorf("purchases are debits, written with their sign: %v", rows)
	}
	// a hostile text is quoted, and Persian stays Persian
	_, _, body = b.get(csvKinds[3].path)
	rows := parseCSV(t, body)
	byID := map[string][]string{}
	for _, r := range rows[1:] {
		byID[r[6]] = r
	}
	if r := byID["'=1+1"]; r == nil || r[7] != "first line\nsecond, with \"quotes\"\nthird\nfourth" {
		t.Errorf("a reason with line breaks, a comma and quotes: %v", byID)
	}
	if r := byID["'\u200f-2+3"]; r == nil || r[7] != "' @SUM(A1)" {
		t.Errorf("a formula behind a direction mark and spaces: %v", byID)
	}
	// on the wire the line breaks inside the reason are CRLF, the same as the rows
	if !bytes.Contains(body, []byte("\"first line\r\nsecond, with \"\"quotes\"\"\r\nthird\r\nfourth\"")) {
		t.Errorf("raw reason: %q", body)
	}
}

// Dates follow one rule in every file: instants in UTC, the same instants on the
// store's wall clocks with the zone named in the file, and, for a Jalali file,
// the Jalali date beside them in Latin digits.
func TestCSVDatesFollowOneRule(t *testing.T) {
	_, b, ids := reportFixture(t)
	// 20:29Z on 31 Oct is 23:59 in Tehran (UTC+3:30): 9 Aban 1405. Two minutes later it is 10 Aban.
	for _, c := range []struct {
		calendar          string
		wantUTC, wantHere string
		wantJalali        string
	}{
		{"", "2026-10-31T20:29:00Z", "2026-10-31 23:59:00", ""},
		{"&calendar=gregorian", "2026-10-31T20:29:00Z", "2026-10-31 23:59:00", ""},
		{"&calendar=jalali", "2026-10-31T20:29:00Z", "2026-10-31 23:59:00", "1405-08-09 23:59:00"},
	} {
		_, _, body := b.get("/api/v1/reports/orders.csv" + reportQuery + c.calendar)
		rows := parseCSV(t, body)
		want := csvKinds[1].header
		if c.wantJalali != "" { // after tz, before the list price columns that close every orders file
			want = append(append(append([]string{}, want[:14]...), "paid_at_jalali"), want[14:]...)
		}
		if !reflect.DeepEqual(rows[0], want) {
			t.Fatalf("orders%s header %v", c.calendar, rows[0])
		}
		r := rows[1]
		if r[2] != ids["o1"] || r[0] != c.wantUTC || r[1] != c.wantHere || r[13] != "Asia/Tehran" {
			t.Errorf("orders%s first row: %v", c.calendar, r)
		}
		if c.wantJalali != "" && (len(r) != 18 || r[14] != c.wantJalali || rows[2][14] != "1405-08-10 00:01:00") {
			t.Errorf("orders%s Jalali column: %v / %v", c.calendar, r, rows[2])
		}
	}
	// the summary: period_start stays the Gregorian day; the Jalali day is beside it
	_, _, body := b.get("/api/v1/reports/summary.csv" + reportQuery + "&calendar=jalali")
	rows := parseCSV(t, body)
	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	if len(rows[0]) != 32 || rows[0][27] != "period_start_jalali" || rows[0][28] != "license_orders" || col["calendar"] != 25 {
		t.Fatalf("Jalali summary header: %v", rows[0])
	}
	days := map[string]string{}
	for _, r := range rows[1:] {
		if len(r) != 32 || r[col["calendar"]] != "jalali" || r[col["group"]] != "day" || r[col["tz"]] != "Asia/Tehran" {
			t.Fatalf("a Jalali summary row: %v", r)
		}
		days[r[0]] = r[27]
	}
	if !reflect.DeepEqual(days, map[string]string{"2026-10-31": "1405-08-09", "2026-11-01": "1405-08-10", "2026-11-02": "1405-08-11", "2026-11-03": "1405-08-12"}) {
		t.Errorf("Jalali days: %v", days)
	}
	// a Jalali month: the bucket starts on 1 Aban (23 Oct), and says so in both calendars
	_, _, body = b.get("/api/v1/reports/summary.csv?from=2026-10-31&to=2026-11-03&tz=Asia/Tehran&group=month&calendar=jalali")
	rows = parseCSV(t, body)
	if len(rows) != 4 || rows[1][0] != "2026-10-23" || rows[1][27] != "1405-08-01" || rows[1][col["group"]] != "month" {
		t.Errorf("Jalali month: %v", rows)
	}
	// a Gregorian file has no Jalali column, and the same machine columns
	_, _, body = b.get("/api/v1/reports/summary.csv" + reportQuery)
	if rows = parseCSV(t, body); len(rows[0]) != 31 || rows[1][26] != "Asia/Tehran" {
		t.Errorf("Gregorian summary: %v", rows[0])
	}
}

// ledger.csv and audit.csv write the same dates: UTC, local, tz, and Jalali on request, in the zone asked for.
func TestLedgerAndAuditWriteTheirDatesLikeTheReports(t *testing.T) {
	f, b, _ := reportFixture(t)
	seedAudit(t, f, 2)
	// ledger, newest first: the first row of the newest, the last row of the oldest (o1 at 20:29:00Z on 31 Oct)
	_, _, body := b.get("/api/v1/ledger.csv?calendar=jalali")
	rows := parseCSV(t, body)
	if len(rows) != ledgerSeeded+1 || rows[0][12] != "time_jalali" || len(rows[0]) != 13 {
		t.Fatalf("Jalali ledger header: %v", rows[0])
	}
	oldest := rows[len(rows)-1]
	if oldest[0] != "2026-10-31T20:29:00Z" || oldest[10] != "2026-10-31 23:59:00" || oldest[11] != "Asia/Tehran" || oldest[12] != "1405-08-09 23:59:00" {
		t.Errorf("oldest ledger row: %v", oldest)
	}
	// the same instant in another zone, and a Gregorian file with no extra column
	_, _, body = b.get("/api/v1/ledger.csv?tz=America/New_York")
	rows = parseCSV(t, body)
	oldest = rows[len(rows)-1]
	if len(rows[0]) != 12 || oldest[0] != "2026-10-31T20:29:00Z" || oldest[10] != "2026-10-31 16:29:00" || oldest[11] != "America/New_York" {
		t.Errorf("New York ledger: %v", oldest)
	}
	// audit: every row's local time is its UTC time on the store's clocks, and the Jalali day is that local day's
	_, _, body = b.get("/api/v1/audit.csv?action=seed.entry&calendar=jalali&tz=Asia/Tehran")
	rows = parseCSV(t, body)
	if len(rows) != 3 || rows[0][15] != "time_jalali" {
		t.Fatalf("Jalali audit: %v", rows)
	}
	tehran, _ := time.LoadLocation("Asia/Tehran")
	for _, r := range rows[1:] {
		at, err := time.Parse(time.RFC3339, r[0])
		if err != nil {
			t.Fatalf("time_utc %q: %v", r[0], err)
		}
		local := at.In(tehran).Format("2006-01-02 15:04:05")
		if r[13] != local || r[14] != "Asia/Tehran" || r[15] != jalaliStamp(local) || !regexp.MustCompile(`^14\d\d-\d\d-\d\d \d\d:\d\d:\d\d$`).MatchString(r[15]) {
			t.Errorf("audit row dates: %v (local %s)", r, local)
		}
	}
	// a calendar or a zone that does not exist is a 400 before any file starts, everywhere
	for _, p := range []string{"/api/v1/ledger.csv", "/api/v1/audit.csv", "/api/v1/reports/summary.csv?from=2026-10-31&to=2026-11-03", "/api/v1/reports/orders.csv?from=2026-10-31&to=2026-11-03"} {
		sep := "?"
		if strings.Contains(p, "?") {
			sep = "&"
		}
		for _, bad := range []string{"calendar=julian", "tz=Not/AZone", "tz=Local"} {
			code, h, body := b.get(p + sep + bad)
			if code != 400 || bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) || !strings.Contains(h.Get("Content-Type"), "json") {
				t.Errorf("%s %s: %d %q", p, bad, code, body)
			}
		}
	}
}

// ledger.csv and audit.csv are named by the days they cover, in the file's zone and calendar.
func TestLedgerAndAuditFilesAreNamedByTheirDays(t *testing.T) {
	_, b, _ := reportFixture(t)
	fixed := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	b.f.api.now = func() time.Time { return fixed }
	from := time.Date(2026, 10, 30, 20, 30, 0, 0, time.UTC) // 00:00 on 31 Oct in Tehran
	name := func(path string) string {
		t.Helper()
		code, h, body := b.get(path)
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		return h.Get("Content-Disposition")
	}
	q := "?from=" + strconv.FormatInt(from.Unix(), 10)
	for path, want := range map[string]string{
		"/api/v1/ledger.csv":                                        `attachment; filename="ledger-all-to-2026-11-04.csv"`,
		"/api/v1/ledger.csv" + q:                                    `attachment; filename="ledger-2026-10-31-to-2026-11-04.csv"`,
		"/api/v1/ledger.csv" + q + "&calendar=jalali":               `attachment; filename="ledger-1405-08-09-to-1405-08-13-jalali.csv"`,
		"/api/v1/audit.csv" + q:                                     `attachment; filename="audit-2026-10-31-to-2026-11-04.csv"`,
		"/api/v1/audit.csv" + q + "&tz=Pacific/Kiritimati":          `attachment; filename="audit-2026-10-31-to-2026-11-05.csv"`,
		"/api/v1/audit.csv" + q + "&tz=Asia/Tehran&calendar=jalali": `attachment; filename="audit-1405-08-09-to-1405-08-13-jalali.csv"`,
	} {
		if got := name(path); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
}

// Who may export what: the permission of the thing exported, and nobody without a session.
func TestCSVExportsNeedTheirPermission(t *testing.T) {
	f, owner, _ := reportFixture(t)
	f.staffUser(7201, "admin")
	f.staffUser(7202, "support")
	admin, support, anon := f.browser(), f.browser(), f.browser()
	if admin.loginWithLink(7201) != 200 || support.loginWithLink(7202) != 200 {
		t.Fatal("login")
	}
	for _, k := range csvKinds {
		if code, _, _ := owner.get(k.path); code != 200 {
			t.Errorf("owner %s: %d", k.name, code)
		}
		if code, _, _ := admin.get(k.path); code != 200 {
			t.Errorf("admin %s: %d", k.name, code)
		}
		if code, h, body := support.get(k.path); code != 403 || bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) || !strings.Contains(h.Get("Content-Type"), "json") {
			t.Errorf("support %s: %d %q", k.name, code, body)
		}
		if code, _, body := anon.get(k.path); code != 401 || bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
			t.Errorf("anonymous %s: %d %q", k.name, code, body)
		}
		// a file is only made for a GET: nothing else is routed
		if code, _ := owner.do(http.MethodPost, strings.SplitN(k.path, "?", 2)[0], map[string]string{}); code == 200 {
			t.Errorf("POST %s answered 200", k.name)
		}
	}
}

// What the files hold about the people who run the store: no session, token or
// link of the dashboard, none of a bot or a payment, in any file.
func TestCSVFilesHoldNothingSecret(t *testing.T) {
	f, b, _ := reportFixture(t)
	// A second login by link writes its own audit entries; its token, the session cookies and the CSRF
	// token are all in play while the files are made.
	token, _, err := f.dom.CreateLoginLink(context.Background(), ownerTG)
	if err != nil {
		t.Fatal(err)
	}
	other := f.browser()
	if code, _ := other.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": token}); code != 200 {
		t.Fatalf("login: %d", code)
	}
	secrets := []string{token, b.csrf, other.csrf}
	for _, br := range []*browser{b, other} {
		u, _ := url.Parse(f.srv.URL + "/api/v1/")
		for _, c := range br.c.Jar.Cookies(u) {
			secrets = append(secrets, c.Value)
		}
	}
	for _, s := range secrets {
		if len(s) < 16 {
			t.Fatalf("a secret too short to look for: %q", s)
		}
	}
	for _, k := range csvKinds {
		path := k.path
		if k.name == "audit" {
			path = "/api/v1/audit.csv" // all entries, the logins among them
		}
		_, _, body := b.get(path)
		for _, s := range secrets {
			if bytes.Contains(body, []byte(s)) {
				t.Errorf("%s holds a secret of the dashboard", k.name)
			}
		}
		for _, word := range []string{"bot_token", "api_key", "password_hash", "BEGIN PRIVATE KEY", "Bearer "} {
			if bytes.Contains(bytes.ToLower(body), bytes.ToLower([]byte(word))) {
				t.Errorf("%s mentions %q", k.name, word)
			}
		}
	}
	// the audit file does list the logins, with where they came from and how, which is the point of it
	_, _, body := b.get("/api/v1/audit.csv?action=dashboard.login")
	if rows := parseCSV(t, body); len(rows) < 2 || !strings.Contains(rows[1][11], `"method"`) {
		t.Errorf("login entries: %v", rows)
	}
}
