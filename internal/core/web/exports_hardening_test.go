package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// ledger.csv and audit.csv are exports of the reports' kind: the same places,
// deadline, error answers, notes and audit entries (streamCSV).

// exportKinds are the two files, each with a query that selects its seeded rows.
var exportKinds = []struct {
	name, path, action string
}{
	{"ledger", "/api/v1/ledger.csv", "export.ledger"},
	{"audit", "/api/v1/audit.csv?action=seed.entry", "export.audit"},
}

const ledgerSeeded = 5 // reportFixture: three purchases, a top-up and a refund

// seedAudit writes n entries of one action, so audit.csv?action=seed.entry has
// exactly n rows (the export's own entries are not among them).
func seedAudit(t *testing.T, f *fixture, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id := "seed-" + strconv.Itoa(i)
		a := &store.Audit{Action: "seed.entry", Entity: "seed", EntityID: &id}
		if err := f.st.WriteAudit(context.Background(), f.st.Conn(), a); err != nil {
			t.Fatal(err)
		}
	}
}

// exportRows is how many data rows a complete file of the kind holds.
func exportRows(kind string) int {
	if kind == "ledger" {
		return ledgerSeeded
	}
	return 3
}

// lastExportAudit reads the newest audit entry of an action: outcome, rows and
// the whole detail.
func lastExportAudit(t *testing.T, f *fixture, action string) (outcome string, rows int, after string) {
	t.Helper()
	err := f.st.DB().QueryRow(context.Background(),
		`SELECT COALESCE(after->>'outcome', ''), COALESCE((after->>'rows')::int, -1), after::text
		 FROM core.audit_log WHERE action = $1 ORDER BY created_at DESC, id DESC LIMIT 1`, action).Scan(&outcome, &rows, &after)
	if err != nil {
		t.Fatalf("audit of %s: %v", action, err)
	}
	return outcome, rows, after
}

func countAudit(t *testing.T, f *fixture, action string) (n int) {
	t.Helper()
	if err := f.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// lockTable holds ACCESS EXCLUSIVE on a table, so every query on it waits until
// PostgreSQL cancels it, until the returned release is called.
func lockTable(t *testing.T, f *fixture, table string) (release func()) {
	t.Helper()
	tx, err := f.st.DB().Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = tx.Rollback(context.Background()) }) }
	t.Cleanup(release)
	if _, err := tx.Exec(context.Background(), `LOCK TABLE `+table+` IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	return release
}

func shortExportTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := store.ExportTimeout
	store.ExportTimeout = d
	t.Cleanup(func() { store.ExportTimeout = old })
}

// A complete file: header, rows, nothing after; audited as complete with its
// rows, and the request's filters stay in the entry as they always were.
func TestLedgerAndAuditExportsAreAuditedWhenTheyEnd(t *testing.T) {
	f, b, _ := reportFixture(t)
	seedAudit(t, f, 3)
	for _, k := range exportKinds {
		code, h, body := b.get(k.path)
		rows := parseCSV(t, body)
		if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/csv") || !strings.Contains(h.Get("Content-Disposition"), k.name+"-") ||
			len(rows) != exportRows(k.name)+1 || rows[0][0] != "time_utc" || strings.HasPrefix(rows[len(rows)-1][0], "#") {
			t.Fatalf("%s: %d %v %v", k.name, code, h, rows)
		}
		outcome, n, after := lastExportAudit(t, f, k.action)
		if outcome != "complete" || n != exportRows(k.name) {
			t.Errorf("%s audit: %q %d", k.name, outcome, n)
		}
		if k.name == "audit" && !strings.Contains(after, `"action": ["seed.entry"]`) {
			t.Errorf("the filter is lost from the audit entry: %s", after)
		}
	}
	// A filter is kept in the entry.
	b.get("/api/v1/ledger.csv?currency=USDT")
	if outcome, n, after := lastExportAudit(t, f, "export.ledger"); outcome != "complete" || n != 1 || !strings.Contains(after, `"currency": ["USDT"]`) {
		t.Errorf("filtered ledger export: %q %d %s", outcome, n, after)
	}
	// A bad filter is a 400 before anything else.
	if code, _, _ := b.get("/api/v1/ledger.csv?user=not-a-uuid"); code != 400 {
		t.Errorf("a bad filter: %d", code)
	}
}

// No match is a file with its header alone (not an error), audited with 0 rows.
func TestLedgerAndAuditExportsOfNothingAreAHeaderOnly(t *testing.T) {
	f, b, _ := reportFixture(t)
	for _, k := range []struct{ name, path, action string }{
		{"ledger", "/api/v1/ledger.csv?kind=no_such_kind", "export.ledger"},
		{"audit", "/api/v1/audit.csv?action=no.such.action", "export.audit"},
	} {
		code, _, body := b.get(k.path)
		rows := parseCSV(t, body)
		if code != 200 || len(rows) != 1 || rows[0][0] != "time_utc" {
			t.Fatalf("%s: %d %v", k.name, code, rows)
		}
		if outcome, n, _ := lastExportAudit(t, f, k.action); outcome != "complete" || n != 0 {
			t.Errorf("%s audit: %q %d", k.name, outcome, n)
		}
	}
}

// A failure before the first row is an error answer, not a 200 with a header
// that looks like "nothing matches"; it is audited as failed, and the same
// request works once the lock is gone.
func TestLedgerExportThatRunsOutOfTimeIsAnErrorAnswer(t *testing.T) {
	f, b, _ := reportFixture(t)
	shortExportTimeout(t, 300*time.Millisecond)
	release := lockTable(t, f, "core.ledger_entries")
	code, h, body := b.get("/api/v1/ledger.csv")
	if code != 503 || !strings.HasPrefix(h.Get("Content-Type"), "application/json") || bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) ||
		h.Get("Content-Disposition") != "" || !strings.Contains(string(body), `"timeout"`) || !strings.Contains(string(body), "narrow the filter") {
		t.Fatalf("%d %v %q", code, h, body)
	}
	release()
	if outcome, n, _ := lastExportAudit(t, f, "export.ledger"); outcome != "failed" || n != 0 {
		t.Errorf("audit: %q %d", outcome, n)
	}
	if len(f.api.exports) != 0 {
		t.Errorf("a failed export kept its place: %d", len(f.api.exports))
	}
	if code, _, body := b.get("/api/v1/ledger.csv"); code != 200 || len(parseCSV(t, body)) != ledgerSeeded+1 {
		t.Errorf("after the lock: %d %q", code, body)
	}
}

// Same for audit.csv. Its own failure entry needs the audit table, so the lock
// is let go a moment after the export has timed out.
func TestAuditExportThatRunsOutOfTimeIsAnErrorAnswer(t *testing.T) {
	f, b, _ := reportFixture(t)
	seedAudit(t, f, 3)
	shortExportTimeout(t, 300*time.Millisecond)
	release := lockTable(t, f, "core.audit_log")
	timer := time.AfterFunc(900*time.Millisecond, release)
	defer timer.Stop()
	code, h, body := b.get("/api/v1/audit.csv?action=seed.entry")
	if code != 503 || !strings.HasPrefix(h.Get("Content-Type"), "application/json") || bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) ||
		h.Get("Content-Disposition") != "" || !strings.Contains(string(body), `"timeout"`) {
		t.Fatalf("%d %v %q", code, h, body)
	}
	release()
	if outcome, n, _ := lastExportAudit(t, f, "export.audit"); outcome != "failed" || n != 0 {
		t.Errorf("audit: %q %d", outcome, n)
	}
	if code, _, body := b.get("/api/v1/audit.csv?action=seed.entry"); code != 200 || len(parseCSV(t, body)) != 4 {
		t.Errorf("after the lock: %d %q", code, body)
	}
}

// A file that dies after rows went out ends with a line that says so, and is
// audited as incomplete with the rows it holds. (The window is shortened and
// each row paused, so the deadline falls between rows.)
func TestLedgerAndAuditExportsThatDieHalfWayEndWithANote(t *testing.T) {
	f, b, _ := reportFixture(t)
	seedAudit(t, f, 5)
	f.api.exportTime, f.api.rowPause = 500*time.Millisecond, 300*time.Millisecond
	for _, k := range exportKinds {
		code, _, body := b.get(k.path)
		if code != 200 {
			t.Fatalf("%s: status %d", k.name, code)
		}
		rows := parseCSV(t, body)
		last := rows[len(rows)-1]
		if len(rows) < 3 || len(rows) > 5 || len(last) != 1 || !strings.HasPrefix(last[0], "# export incomplete: it took longer than 500ms: choose a shorter range") {
			t.Fatalf("%s: a short export must end with a note: %v", k.name, rows)
		}
		outcome, n, _ := lastExportAudit(t, f, k.action)
		if outcome != "incomplete" || n != len(rows)-2 {
			t.Errorf("%s audit: %q %d (file: %d lines)", k.name, outcome, n, len(rows))
		}
	}
	// Without the pause the same exports are whole again.
	f.api.exportTime, f.api.rowPause = 0, 0
	for _, k := range exportKinds {
		b.get(k.path)
		if outcome, n, _ := lastExportAudit(t, f, k.action); outcome != "complete" || n < 3 {
			t.Errorf("%s audit of a whole export: %q %d", k.name, outcome, n)
		}
	}
}

// streamCSV with failures that no real query makes on demand: another kind of
// error, the more-rows note, and an error before and after the first row.
func TestStreamCSVEndsEveryWayItCan(t *testing.T) {
	f, b, _ := reportFixture(t)
	boom := errors.New("boom")
	cases := map[string]struct {
		each    func(ctx context.Context, row func(cells ...string) error) (bool, error)
		code    int
		last    string
		outcome string
		rows    int
	}{
		"more": {func(_ context.Context, row func(...string) error) (bool, error) {
			_ = row("a", "1")
			_ = row("b", "2")
			return true, nil
		}, 200, "# more entries match: narrow the filter (at most 100000 rows per file)", "complete", 2},
		"after": {func(_ context.Context, row func(...string) error) (bool, error) {
			_ = row("a", "1")
			return false, boom
		}, 200, "# export incomplete: a server error: see core's log", "incomplete", 1},
		"before": {func(context.Context, func(...string) error) (bool, error) { return false, boom }, 500, "", "failed", 0},
		"closed": {func(_ context.Context, row func(...string) error) (bool, error) {
			_ = row("a", "1")
			return false, context.Canceled
		}, 200, "# export incomplete: the connection was closed", "incomplete", 1},
	}
	for name, c := range cases {
		api := New(f.api.cfg)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /x", api.session(api.require(PermLedgerRead, func(w http.ResponseWriter, r *http.Request) {
			api.streamCSV(w, r, csvExport{name: "x.csv", action: "export.test", query: r.URL.Query(), header: []string{"k", "v"},
				each: c.each, moreNote: moreEntriesNote(maxCSVRows)})
		})))
		srv := httptest.NewTLSServer(mux)
		resp, err := b.c.Get(srv.URL + "/x?q=1")
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		srv.Close()
		if resp.StatusCode != c.code {
			t.Errorf("%s: status %d %q", name, resp.StatusCode, body)
			continue
		}
		if c.code == 200 {
			rows := parseCSV(t, body)
			if rows[0][0] != "k" || (c.last != "" && rows[len(rows)-1][0] != c.last) {
				t.Errorf("%s: %v", name, rows)
			}
		} else if bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
			t.Errorf("%s: an error answer must not be a file: %q", name, body)
		}
		outcome, n, after := lastExportAudit(t, f, "export.test")
		if outcome != c.outcome || n != c.rows || !strings.Contains(after, `"q": ["1"]`) {
			t.Errorf("%s audit: %q %d %s", name, outcome, n, after)
		}
	}
}

// core's HTTP server cuts a response at its write limit: these exports lift it
// as the reports' do. The server has 300 ms limits, each row waits 250 ms, and
// the whole file arrives.
func TestASlowLedgerOrAuditExportIsNotCutByTheServersLimits(t *testing.T) {
	f, b, _ := reportFixture(t)
	seedAudit(t, f, 3)
	api := New(f.api.cfg)
	api.exportTime, api.rowPause = 5*time.Second, 250*time.Millisecond
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 300*time.Millisecond, 300*time.Millisecond
	srv.StartTLS()
	defer srv.Close()
	for _, k := range exportKinds {
		start := time.Now()
		resp, err := b.c.Get(srv.URL + k.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: a slow export was cut: %d %v", k.name, resp.StatusCode, err)
		}
		rows := parseCSV(t, body)
		if len(rows) != exportRows(k.name)+1 || strings.HasPrefix(rows[len(rows)-1][0], "#") || time.Since(start) < 700*time.Millisecond {
			t.Errorf("%s: %d rows after %s: %v", k.name, len(rows), time.Since(start), rows)
		}
	}
}

// All exports share two places. With none free, ledger.csv and audit.csv are
// told to wait (429, Retry-After, both languages) and write nothing; they use a
// place when one is free and give it back. A ledger export in flight holds a
// place that a reports export cannot have.
func TestLedgerAndAuditExportsShareThePlacesOfTheReportExports(t *testing.T) {
	f, b, _ := reportFixture(t)
	seedAudit(t, f, 3)
	f.api.exports <- struct{}{}
	f.api.exports <- struct{}{}
	before := map[string]int{"export.ledger": countAudit(t, f, "export.ledger"), "export.audit": countAudit(t, f, "export.audit")}
	for _, k := range exportKinds {
		code, h, body := b.get(k.path)
		if code != 429 || h.Get("Retry-After") != "5" || !strings.Contains(string(body), "rate_limited") ||
			!strings.Contains(string(body), "try again in a few seconds") || !strings.Contains(string(body), "چند ثانیه") ||
			bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
			t.Errorf("%s: %d %v %q", k.name, code, h, body)
		}
		if countAudit(t, f, k.action) != before[k.action] {
			t.Errorf("%s: a refused export was audited", k.name)
		}
	}
	// The JSON lists need no place.
	if code, _ := b.do(http.MethodGet, "/api/v1/ledger", nil); code != 200 {
		t.Errorf("ledger list: %d", code)
	}
	<-f.api.exports
	for _, k := range exportKinds {
		if code, _, body := b.get(k.path); code != 200 || len(parseCSV(t, body)) != exportRows(k.name)+1 {
			t.Errorf("%s with a free place: %d", k.name, code)
		}
	}
	<-f.api.exports
	if len(f.api.exports) != 0 {
		t.Fatalf("exports kept their places: %d", len(f.api.exports))
	}

	// Two slow ledger exports fill the places; a reports export and an audit
	// export (the third and fourth) are refused, not queued.
	f.api.rowPause = 300 * time.Millisecond
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if resp, err := b.c.Get(f.srv.URL + "/api/v1/ledger.csv"); err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				codes[i] = resp.StatusCode
			}
		}()
	}
	waitFor(t, "two ledger exports to run", func() bool { return len(f.api.exports) == 2 })
	for _, path := range []string{"/api/v1/reports/orders.csv" + reportQuery, "/api/v1/audit.csv", "/api/v1/ledger.csv"} {
		if code, h, _ := b.get(path); code != 429 || h.Get("Retry-After") != "5" {
			t.Errorf("%s while two exports run: %d", path, code)
		}
	}
	wg.Wait()
	f.api.rowPause = 0
	if codes[0] != 200 || codes[1] != 200 || len(f.api.exports) != 0 {
		t.Errorf("the running exports: %v, %d places taken", codes, len(f.api.exports))
	}
}

// Text cells go through reportText: a name that becomes a formula after
// spaces is quoted too (csvText alone looks at the first character only), and
// what csvText quoted is still quoted.
func TestLedgerAndAuditExportsQuoteWhatCouldRunAsAFormula(t *testing.T) {
	f, b, _ := reportFixture(t)
	r := newReportSeed(t, f)
	user := r.user(8120, "  =HYPERLINK(\"http://x\")")
	r.topup(user, "IRT", 1000, "2026-11-04T09:00:00Z")
	_, _, body := b.get("/api/v1/ledger.csv?user=" + user)
	rows := parseCSV(t, body)
	if len(rows) != 2 || rows[1][2] != "'  =HYPERLINK(\"http://x\")" {
		t.Fatalf("ledger: %v", rows)
	}
	id := "  -2+3"
	reason := " @SUM(A1)"
	if err := f.st.WriteAudit(context.Background(), f.st.Conn(), &store.Audit{Action: "seed.formula", Entity: "seed", EntityID: &id, Reason: &reason}); err != nil {
		t.Fatal(err)
	}
	_, _, body = b.get("/api/v1/audit.csv?action=seed.formula")
	rows = parseCSV(t, body)
	if len(rows) != 2 || rows[1][6] != "'  -2+3" || rows[1][7] != "' @SUM(A1)" {
		t.Fatalf("audit: %v", rows)
	}
}
