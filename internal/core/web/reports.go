package web

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Sales reports (docs/14-reports.md), read-only, for staff with reports.read
// (owner and admin). Every request names a range: ?range=today|last_7_days|
// last_30_days|this_month|last_month, or ?from=2026-10-01&to=2026-10-14 (local
// dates, both included); none means the last 30 days. ?tz= is an IANA zone
// (default: the store's general.timezone setting, else BOBRES_TIMEZONE, else
// Asia/Tehran), ?group=day|week|month sizes the summary's buckets and
// ?calendar=gregorian|jalali (default gregorian) says how weeks and months are
// cut: Gregorian weeks start on Monday and months on the 1st, Jalali weeks on
// Saturday and months on Jalali day 1 (a bucket's date is always its Gregorian
// start date).
// Amounts are integers in each currency's minor unit; currencies are never
// added together.

// reportGrace is how long past a report's own deadline the connection may stay
// open, so a timeout can still be answered or marked in a file.
const reportGrace = 10 * time.Second

// maxExports is how many CSV exports run at once: each holds a database
// connection (and a snapshot) for as long as it streams, and the pool is small.
const maxExports = 2

// stretch lifts the HTTP server's own write limit (30 seconds, internal/health)
// for a request that may legitimately take d: the server would cut a longer
// stream off at 30 seconds. (Its 15-second read limit is over once the request
// has been read; a test with both limits at 300 ms shows only the write one
// matters.) A response writer that cannot do it (a wrapper, a test recorder) is
// left alone.
func stretch(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}

// reportRange reads and validates the request's range, answering 400 itself,
// and lets the request run as long as a report may.
func (s *Server) reportRange(w http.ResponseWriter, r *http.Request) (*domain.ReportRange, bool) {
	stretch(w, s.exportWindow()+reportGrace)
	q := r.URL.Query()
	rng, err := s.cfg.Domain.ReportRange(r.Context(), domain.ReportRequest{
		Range: q.Get("range"), From: q.Get("from"), To: q.Get("to"), Group: q.Get("group"), Calendar: q.Get("calendar"), TZ: q.Get("tz"),
		ChurnGraceDays: q.Get("churn_grace_days"),
	}, s.now())
	if err != nil {
		s.fail(w, "report range", err)
		return nil, false
	}
	return rng, true
}

type flowJSON struct {
	Count  int64 `json:"count"`
	Amount int64 `json:"amount"`
}

// cellJSON is one bucket's numbers in one currency (amounts in its minor unit).
type cellJSON struct {
	Revenue       int64    `json:"revenue"`
	Refunds       flowJSON `json:"refunds"`
	Net           int64    `json:"net"` // revenue less refunds
	Orders        int64    `json:"orders"`
	NewOrders     int64    `json:"new_orders"`
	Renewals      int64    `json:"renewals"`
	TrafficTopups int64    `json:"traffic_topups"`
	Topups        flowJSON `json:"topups"`
	Rejected      flowJSON `json:"rejected"`       // manual payments a reviewer rejected
	GatewayFailed flowJSON `json:"gateway_failed"` // gateway payments that failed or expired
	Cancelled     flowJSON `json:"cancelled"`
}

func cellOf(c *store.ReportCell) cellJSON {
	return cellJSON{
		Revenue: c.Revenue, Refunds: flowJSON(c.Refunds), Net: domain.Net(c), Orders: c.Orders, NewOrders: c.NewOrders,
		Renewals: c.Renewals, TrafficTopups: c.TrafficTopups, Topups: flowJSON(c.Topups),
		Rejected: flowJSON(c.Rejected), GatewayFailed: flowJSON(c.GatewayFailed), Cancelled: flowJSON(c.Cancelled),
	}
}

type bucketJSON struct {
	Date         string `json:"date"` // local start date, "2026-10-14"
	Partial      bool   `json:"partial"`
	NewCustomers int64  `json:"new_customers"`
	Trials       int64  `json:"trials"`
	// Churn (no currency): services that ended and stayed ended past the grace
	// period, people whose last service did, and services still inside the grace.
	ChurnedServices  int64               `json:"churned_services"`
	ChurnedCustomers int64               `json:"churned_customers"`
	AtRisk           int64               `json:"at_risk"`
	Currencies       map[string]cellJSON `json:"currencies"`
}

func bucketOf(b *domain.ReportBucket) bucketJSON {
	out := bucketJSON{Date: b.Date, Partial: b.Partial, NewCustomers: b.NewCustomers, Trials: b.Trials,
		ChurnedServices: b.ChurnedServices, ChurnedCustomers: b.ChurnedCustomers, AtRisk: b.AtRisk,
		Currencies: make(map[string]cellJSON, len(b.Currencies))}
	for cur, c := range b.Currencies {
		out.Currencies[cur] = cellOf(c)
	}
	return out
}

// reportSummary: GET /api/v1/reports/summary
func (s *Server) reportSummary(w http.ResponseWriter, r *http.Request) {
	rng, ok := s.reportRange(w, r)
	if !ok {
		return
	}
	rep, err := s.cfg.Domain.SalesReport(r.Context(), rng)
	if err != nil {
		s.fail(w, "sales report", err)
		return
	}
	buckets := make([]bucketJSON, 0, len(rep.Buckets))
	for i := range rep.Buckets {
		buckets = append(buckets, bucketOf(&rep.Buckets[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": rng.From, "to": rng.To, "group": rng.Group, "calendar": rng.Calendar, "tz": rng.TZ,
		"churn_grace_days": rng.ChurnGraceDays, "currencies": rep.Currencies, "buckets": buckets, "totals": bucketOf(&rep.Totals),
	})
}

// reportPlans: GET /api/v1/reports/plans, the best-selling plans per currency.
func (s *Server) reportPlans(w http.ResponseWriter, r *http.Request) {
	rng, ok := s.reportRange(w, r)
	if !ok {
		return
	}
	rows, err := s.cfg.Domain.TopPlans(r.Context(), rng)
	if err != nil {
		s.fail(w, "report plans", err)
		return
	}
	type plan struct {
		PlanID   string            `json:"plan_id"`
		Name     map[string]string `json:"name"`
		Currency string            `json:"currency"`
		Orders   int64             `json:"orders"`
		Revenue  int64             `json:"revenue"`
	}
	plans := make([]plan, 0, len(rows))
	for _, p := range rows {
		plans = append(plans, plan{p.PlanID, p.Name, p.Currency, p.Orders, p.Revenue})
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": rng.From, "to": rng.To, "tz": rng.TZ, "plans": plans})
}

// reportMethods: GET /api/v1/reports/methods, revenue by how orders were paid.
func (s *Server) reportMethods(w http.ResponseWriter, r *http.Request) {
	rng, ok := s.reportRange(w, r)
	if !ok {
		return
	}
	rows, err := s.cfg.Domain.SalesByMethod(r.Context(), rng)
	if err != nil {
		s.fail(w, "report methods", err)
		return
	}
	type method struct {
		Method   string `json:"method"`
		Provider string `json:"provider"` // "" for wallet, unknown, and events from before it was recorded
		Currency string `json:"currency"`
		Orders   int64  `json:"orders"`
		Revenue  int64  `json:"revenue"`
	}
	methods := make([]method, 0, len(rows))
	for _, m := range rows {
		methods = append(methods, method{m.Method, m.Provider, m.Currency, m.Orders, m.Revenue})
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": rng.From, "to": rng.To, "tz": rng.TZ, "methods": methods})
}

// exportWindow is how long an export may run (a test shortens it).
func (s *Server) exportWindow() time.Duration {
	if s.exportTime > 0 {
		return s.exportTime
	}
	return domain.ExportDeadline()
}

// takeExport claims one of the maxExports places, or answers 429 with a
// Retry-After and a message in both languages (a file is a plain download, so
// the dashboard cannot translate what the browser would show).
func (s *Server) takeExport(w http.ResponseWriter) (release func(), ok bool) {
	select {
	case s.exports <- struct{}{}:
		return func() { <-s.exports }, true
	default:
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, "rate_limited",
			"too many exports are running: try again in a few seconds / چند خروجی هم\u200cزمان در حال اجراست: چند ثانیه بعد دوباره امتحان کنید")
		return nil, false
	}
}

// csvFile is a CSV answer. Nothing is sent (no header, no byte-order mark)
// until the file has something to say, so a failure before the first row is an
// error answer and never a 200 with an empty-looking file.
type csvFile struct {
	w       http.ResponseWriter
	name    string
	cw      *csv.Writer
	rows    int // data rows written, header and notes apart
	started bool
}

// start sends the headers and the byte-order mark that makes spreadsheet
// programs read Persian right.
func (f *csvFile) start() {
	if f.started {
		return
	}
	f.started = true
	h := f.w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+f.name+`"`)
	_, _ = f.w.Write([]byte{0xEF, 0xBB, 0xBF})
	f.cw = csv.NewWriter(f.w)
}

// write adds a row (starting the file if it has not started).
func (f *csvFile) write(cells ...string) error {
	f.start()
	return f.cw.Write(cells)
}

// data adds a data row and pushes the file out every few hundred rows, so a
// long export is a stream and the connection never sits idle.
func (f *csvFile) data(cells ...string) error {
	if err := f.write(cells...); err != nil {
		return err
	}
	if f.rows++; f.rows%500 == 0 {
		f.cw.Flush()
		_ = http.NewResponseController(f.w).Flush()
	}
	return nil
}

func (f *csvFile) flush() {
	if f.cw != nil {
		f.cw.Flush()
	}
}

// exportReason says in a few words why a file ended short.
func exportReason(err error, window time.Duration) string {
	switch {
	case errors.Is(err, domain.ErrReportTimeout):
		if window >= time.Second {
			return "it took longer than " + strconv.Itoa(int(window.Seconds())) + " seconds: choose a shorter range"
		}
		return "it took longer than " + window.String() + ": choose a shorter range"
	case errors.Is(err, context.Canceled):
		return "the connection was closed"
	}
	return "a server error: see core's log"
}

// auditExport records a finished export: what was exported, and how it ended
// (complete, incomplete when the file ended short, failed when nothing was
// sent). It is written even when the client has gone.
func (s *Server) auditExport(r *http.Request, kind string, rng *domain.ReportRange, outcome string, rows int) {
	s.audit(context.WithoutCancel(r.Context()), actor(r), "export.reports", map[string]any{
		"kind": kind, "from": rng.From, "to": rng.To, "group": rng.Group, "calendar": rng.Calendar, "tz": rng.TZ, "outcome": outcome, "rows": rows,
	})
}

// exportReportSummary: GET /api/v1/reports/summary.csv, the summary as one
// row per bucket and currency, plus one row per bucket for the people counts
// (empty currency), so no column ever adds up two currencies or counts a
// person twice.
func (s *Server) exportReportSummary(w http.ResponseWriter, r *http.Request) {
	rng, ok := s.reportRange(w, r)
	if !ok {
		return
	}
	release, ok := s.takeExport(w)
	if !ok {
		return
	}
	defer release()
	rep, err := s.cfg.Domain.SalesReport(r.Context(), rng)
	if err != nil {
		s.auditExport(r, "summary", rng, "failed", 0)
		s.fail(w, "sales report", err)
		return
	}
	name := "reports-summary-" + rng.From + "-to-" + rng.To + "-by-" + rng.Group
	if rng.Calendar == domain.CalendarJalali {
		name += "-jalali"
	}
	f := &csvFile{w: w, name: name + ".csv"}
	_ = f.write("period_start", "partial_period", "currency", "revenue", "refund_amount", "net", "paid_orders", "new_orders",
		"renewals", "traffic_topups", "topup_count", "topup_amount", "refund_count", "rejected_count", "rejected_amount",
		"gateway_failed_count", "gateway_failed_amount", "cancelled_count", "cancelled_amount", "new_customers", "free_trials",
		"churned_services", "churned_customers", "at_risk_services")
	cols := 24
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	for i := range rep.Buckets {
		b := &rep.Buckets[i]
		partial := strconv.FormatBool(b.Partial)
		// People counts: no currency, no money columns.
		row := make([]string, cols)
		row[0], row[1] = b.Date, partial
		row[cols-5], row[cols-4] = n(b.NewCustomers), n(b.Trials)
		row[cols-3], row[cols-2], row[cols-1] = n(b.ChurnedServices), n(b.ChurnedCustomers), n(b.AtRisk)
		_ = f.data(row...)
		for _, cur := range rep.Currencies {
			c := b.Currencies[cur]
			_ = f.data(b.Date, partial, reportText(cur), decimal(c.Revenue, cur), decimal(c.Refunds.Amount, cur), decimal(domain.Net(c), cur),
				n(c.Orders), n(c.NewOrders), n(c.Renewals), n(c.TrafficTopups), n(c.Topups.Count), decimal(c.Topups.Amount, cur),
				n(c.Refunds.Count), n(c.Rejected.Count), decimal(c.Rejected.Amount, cur), n(c.GatewayFailed.Count), decimal(c.GatewayFailed.Amount, cur),
				n(c.Cancelled.Count), decimal(c.Cancelled.Amount, cur), "", "", "", "", "")
		}
	}
	f.flush()
	s.auditExport(r, "summary", rng, "complete", f.rows)
}

// exportReportOrders: GET /api/v1/reports/orders.csv, every order paid in the
// range, oldest first, at most maxCSVRows of them. The file starts with the
// first row. A failure before it is an error answer; one after it ends the
// file with a line that says so.
func (s *Server) exportReportOrders(w http.ResponseWriter, r *http.Request) {
	rng, ok := s.reportRange(w, r)
	if !ok {
		return
	}
	release, ok := s.takeExport(w)
	if !ok {
		return
	}
	defer release()
	f := &csvFile{w: w, name: "reports-orders-" + rng.From + "-to-" + rng.To + ".csv"}
	header := []string{"paid_at_utc", "paid_at_local", "order_id", "type", "telegram_id", "username", "plan_fa", "plan_en",
		"amount", "currency", "paid_with", "refunded_amount", "provider"}
	ctx, cancel := context.WithTimeout(r.Context(), s.exportWindow())
	defer cancel()
	more, err := s.cfg.Domain.EachPaidOrder(ctx, rng, maxCSVRows, func(o store.PaidOrderRow) error {
		// Rows already received are read without a look at the deadline.
		if err := ctx.Err(); err != nil {
			return err
		}
		if !f.started {
			if err := f.write(header...); err != nil {
				return err
			}
		}
		if s.rowPause > 0 {
			time.Sleep(s.rowPause)
		}
		return f.data(o.PaidAt.UTC().Format("2006-01-02T15:04:05Z"), o.PaidLocal, o.OrderID, o.Type,
			strconv.FormatInt(o.TelegramID, 10), reportText(o.Username), reportText(o.PlanName["fa"]), reportText(o.PlanName["en"]),
			decimal(o.Amount, o.Currency), reportText(o.Currency), reportText(o.Method), decimal(o.Refunded, o.Currency),
			reportText(o.Provider))
	})
	switch {
	case err != nil && !f.started:
		// Nothing has been sent: answer with the error, not with an empty file.
		s.cfg.Log.Error("dashboard api: export report orders", "err", err)
		s.auditExport(r, "orders", rng, "failed", 0)
		s.fail(w, "export report orders", err)
		return
	case err != nil:
		// The file is out: it ends with a line that says it is not whole.
		s.cfg.Log.Error("dashboard api: export report orders", "err", err)
		_ = f.write("# export incomplete: " + exportReason(err, s.exportWindow()))
		f.flush()
		s.auditExport(r, "orders", rng, "incomplete", f.rows)
		return
	}
	if !f.started {
		_ = f.write(header...) // no order was paid in the range: a file with its header only
	}
	if more {
		_ = f.write("# more orders were paid in this range: choose a shorter one (at most " + strconv.Itoa(maxCSVRows) + " rows per file)")
	}
	f.flush()
	s.auditExport(r, "orders", rng, "complete", f.rows)
}

// reportText is csvText for the cells of the reports, with one more guard: a
// spreadsheet program ignores leading spaces and line breaks before a formula,
// so text that starts with them and then a formula character is quoted too.
// (csvText, used by the other exports, looks at the first character only.)
func reportText(s string) string {
	if t := strings.TrimLeft(s, " \t\r\n\v\f"); t != "" && strings.ContainsRune("=+-@", rune(t[0])) {
		return "'" + s
	}
	return csvText(s)
}
