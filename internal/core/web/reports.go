package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
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
	// Licenses and Digital are the part of Revenue and Orders that the licenses the store sells and the digital accounts it resells made;
	// NewOrders, Renewals and TrafficTopups split the rest (the VPN services).
	Licenses      flowJSON `json:"licenses"`
	Digital       flowJSON `json:"digital"`
	Topups        flowJSON `json:"topups"`
	Rejected      flowJSON `json:"rejected"`       // manual payments a reviewer rejected
	GatewayFailed flowJSON `json:"gateway_failed"` // gateway payments that failed or expired
	Cancelled     flowJSON `json:"cancelled"`
	// Cashback is the cashback given to wallets, less what refunds took back (count: the credits). It is not revenue and not in Net.
	Cashback flowJSON `json:"cashback"`
}

func cellOf(c *store.ReportCell) cellJSON {
	return cellJSON{
		Revenue: c.Revenue, Refunds: flowJSON(c.Refunds), Net: domain.Net(c), Orders: c.Orders, NewOrders: c.NewOrders,
		Renewals: c.Renewals, TrafficTopups: c.TrafficTopups, Licenses: flowJSON(c.Licenses), Digital: flowJSON(c.Digital), Topups: flowJSON(c.Topups),
		Rejected: flowJSON(c.Rejected), GatewayFailed: flowJSON(c.GatewayFailed), Cancelled: flowJSON(c.Cancelled), Cashback: flowJSON(c.Cashback),
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

// reportDailySummary: GET /api/v1/reports/daily-summary, whether the daily sales
// summary in Telegram is on, at what time (HH:MM) and in which time zone. The
// Reports page says so and, for staff who may change settings, links to them.
func (s *Server) reportDailySummary(w http.ResponseWriter, r *http.Request) {
	st, err := s.cfg.Domain.DailySummaryStatus(r.Context())
	if err != nil {
		s.fail(w, "daily summary status", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": st.Enabled, "time": st.Time, "tz": st.Zone})
}

// reportSince: GET /api/v1/reports/since, the instant (unix seconds) the reports and the overview count from, null when they count everything.
func (s *Server) reportSince(w http.ResponseWriter, r *http.Request) {
	t, err := s.cfg.Domain.ReportsSince(r.Context())
	if err != nil {
		s.fail(w, "reports since", err)
		return
	}
	writeJSON(w, http.StatusOK, sinceJSON(t))
}

func sinceJSON(t *time.Time) map[string]any {
	if t == nil {
		return map[string]any{"since": nil}
	}
	return map[string]any{"since": t.Unix()}
}

// reportReset: POST /api/v1/reports/reset {"confirm": true}, the owner starts the reports from zero: they count only what happens from now on.
// Nothing is deleted. Without the confirmation it does nothing.
func (s *Server) reportReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !in.Confirm {
		writeError(w, http.StatusBadRequest, "invalid", "send {\"confirm\": true} to start the reports from zero")
		return
	}
	t, err := s.cfg.Domain.ResetReports(r.Context(), actor(r))
	if err != nil {
		s.fail(w, "reset reports", err)
		return
	}
	writeJSON(w, http.StatusOK, sinceJSON(&t))
}

// reportResetClear: DELETE /api/v1/reports/reset, the reports count everything again.
func (s *Server) reportResetClear(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Domain.ClearReportsReset(r.Context(), actor(r)); err != nil {
		s.fail(w, "clear reports reset", err)
		return
	}
	writeJSON(w, http.StatusOK, sinceJSON(nil))
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

// reportPlanSeries: GET /api/v1/reports/plan-series, what each plan sold in
// every bucket of the report (the summary's buckets: the same group and
// calendar), per currency. Only the buckets where a plan sold are listed. Each
// currency's best plans come one by one and the rest together as plan_id "".
func (s *Server) reportPlanSeries(w http.ResponseWriter, r *http.Request) {
	rng, ok := s.reportRange(w, r)
	if !ok {
		return
	}
	rows, err := s.cfg.Domain.SalesByPlan(r.Context(), rng)
	if err != nil {
		s.fail(w, "report plan series", err)
		return
	}
	type point struct {
		Date    string `json:"date"`
		Orders  int64  `json:"orders"`
		Revenue int64  `json:"revenue"`
	}
	type series struct {
		PlanID   string            `json:"plan_id"` // "" for every plan beyond the currency's best
		Name     map[string]string `json:"name"`
		Currency string            `json:"currency"`
		Orders   int64             `json:"orders"`
		Revenue  int64             `json:"revenue"`
		Points   []point           `json:"points"`
	}
	out := make([]series, 0, len(rows))
	for _, p := range rows {
		name := p.Name
		if name == nil {
			name = map[string]string{}
		}
		sr := series{PlanID: p.PlanID, Name: name, Currency: p.Currency, Orders: p.Orders, Revenue: p.Revenue, Points: make([]point, 0, len(p.Points))}
		for _, pt := range p.Points {
			sr.Points = append(sr.Points, point{pt.Date, pt.Orders, pt.Revenue})
		}
		out = append(out, sr)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": rng.From, "to": rng.To, "group": rng.Group, "calendar": rng.Calendar, "tz": rng.TZ, "series": out,
	})
}

// reportHours: GET /api/v1/reports/hours, paid orders and their revenue by
// weekday (ISO: 1 Monday to 7 Sunday) and hour (0 to 23) of the local day, per
// currency. Only the hours where something was sold are listed.
func (s *Server) reportHours(w http.ResponseWriter, r *http.Request) {
	rng, ok := s.reportRange(w, r)
	if !ok {
		return
	}
	rows, err := s.cfg.Domain.SalesHours(r.Context(), rng)
	if err != nil {
		s.fail(w, "report hours", err)
		return
	}
	type hour struct {
		Weekday  int    `json:"weekday"`
		Hour     int    `json:"hour"`
		Currency string `json:"currency"`
		Orders   int64  `json:"orders"`
		Revenue  int64  `json:"revenue"`
	}
	hours := make([]hour, 0, len(rows))
	for _, h := range rows {
		hours = append(hours, hour{h.Weekday, h.Hour, h.Currency, h.Orders, h.Revenue})
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": rng.From, "to": rng.To, "tz": rng.TZ, "hours": hours})
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

// summaryColumns are the columns of summary.csv in the order they were added.
// Their names are the file's contract (English, never translated: a script or a
// pivot table keeps working in either language of the dashboard), and a new
// column goes at the end, so the earlier ones keep their positions. The last
// three say what the rows are about, the same on every row of a file: the size
// of a period, the calendar its weeks and months were cut by and the time zone
// of its days. A Jalali file has one more column after them (summaryJalali),
// and the columns of summarySales come after that, whatever the calendar.
var summaryColumns = []string{
	"period_start", "partial_period", "currency", "revenue", "refund_amount", "net", "paid_orders", "new_orders",
	"renewals", "traffic_topups", "topup_count", "topup_amount", "refund_count", "rejected_count", "rejected_amount",
	"gateway_failed_count", "gateway_failed_amount", "cancelled_count", "cancelled_amount", "new_customers", "free_trials",
	"churned_services", "churned_customers", "at_risk_services", "group", "calendar", "tz",
}

// summarySales are the columns added after the first release of the file, at its very end so no earlier column moves: how many licenses
// and digital accounts were sold and what they brought (both are already in paid_orders and revenue), and the cashback given (the number of
// credits, and their amount less what refunds took back).
var summarySales = []string{"license_orders", "license_revenue", "digital_orders", "digital_revenue", "cashback_count", "cashback_amount"}

// summaryMoney is how many columns of summary.csv are numbers about money or
// people (period_start to at_risk_services); the context columns follow.
const summaryMoney = 24

// summaryJalali is the column a Jalali summary adds: the Jalali date of
// period_start, with Latin digits.
const summaryJalali = "period_start_jalali"

// exportReportSummary: GET /api/v1/reports/summary.csv, the summary as one
// row per bucket and currency, plus one row per bucket for the people counts
// (empty currency), so no column ever adds up two currencies or counts a
// person twice. There is no totals row, for the same reason: a column of
// totals among the rows would be added twice by the first sum of the column.
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
	jalali := rng.Calendar == domain.CalendarJalali
	name := "reports-summary-" + fileDay(rng.From, jalali) + "-to-" + fileDay(rng.To, jalali) + "-by-" + rng.Group
	if jalali {
		name += "-jalali"
	}
	f := &csvFile{w: w, name: name + ".csv"}
	header := summaryColumns
	if jalali {
		header = append(append([]string{}, header...), summaryJalali)
	}
	header = append(append([]string{}, header...), summarySales...)
	_ = f.write(header...)
	n := func(v int64) string { return strconv.FormatInt(v, 10) }
	// What every row says about itself, after the numbers.
	tail := func(date string) []string {
		out := []string{rng.Group, rng.Calendar, rng.TZ}
		if jalali {
			out = append(out, jalaliDay(date))
		}
		return out
	}
	for i := range rep.Buckets {
		b := &rep.Buckets[i]
		partial := strconv.FormatBool(b.Partial)
		// People counts: no currency, no money columns.
		row := make([]string, summaryMoney, len(header))
		row[0], row[1] = b.Date, partial
		row[summaryMoney-5], row[summaryMoney-4] = n(b.NewCustomers), n(b.Trials)
		row[summaryMoney-3], row[summaryMoney-2], row[summaryMoney-1] = n(b.ChurnedServices), n(b.ChurnedCustomers), n(b.AtRisk)
		_ = f.data(append(append(row, tail(b.Date)...), "", "", "", "", "", "")...)
		for _, cur := range rep.Currencies {
			c := b.Currencies[cur]
			row := []string{b.Date, partial, textCell(cur), decimal(c.Revenue, cur), decimal(c.Refunds.Amount, cur), decimal(domain.Net(c), cur),
				n(c.Orders), n(c.NewOrders), n(c.Renewals), n(c.TrafficTopups), n(c.Topups.Count), decimal(c.Topups.Amount, cur),
				n(c.Refunds.Count), n(c.Rejected.Count), decimal(c.Rejected.Amount, cur), n(c.GatewayFailed.Count), decimal(c.GatewayFailed.Amount, cur),
				n(c.Cancelled.Count), decimal(c.Cancelled.Amount, cur), "", "", "", "", ""}
			sales := []string{n(c.Licenses.Count), decimal(c.Licenses.Amount, cur), n(c.Digital.Count), decimal(c.Digital.Amount, cur),
				n(c.Cashback.Count), decimal(c.Cashback.Amount, cur)}
			_ = f.data(append(append(row, tail(b.Date)...), sales...)...)
		}
	}
	f.flush()
	s.auditExport(r, "summary", rng, "complete", f.rows)
}

// ordersColumns are the columns of orders.csv, with the same rules as the
// summary's: English names, new columns at the end. paid_at_utc is the
// instant, paid_at_local the same instant on the wall clocks of the zone the
// last column (tz) names. A Jalali file has one more column after tz
// (ordersJalali): the local date and time with the date in the Jalali
// calendar and Latin digits.
var ordersColumns = []string{"paid_at_utc", "paid_at_local", "order_id", "type", "telegram_id", "username", "plan_fa", "plan_en",
	"amount", "currency", "paid_with", "refunded_amount", "provider", "tz"}

const ordersJalali = "paid_at_jalali"

// ordersListColumns come at the very end of the file, after tz and the Jalali column: for an order paid from the Toman wallet that was listed in USDT
// (and for a license), the USDT price it was listed at, its currency and the Toman per USDT it was charged at. amount and currency are what was
// charged. Empty for every other order.
var ordersListColumns = []string{"list_amount", "list_currency", "rate"}

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
	jalali := rng.Calendar == domain.CalendarJalali
	name := "reports-orders-" + fileDay(rng.From, jalali) + "-to-" + fileDay(rng.To, jalali)
	if jalali {
		name += "-jalali"
	}
	f := &csvFile{w: w, name: name + ".csv"}
	header := append([]string{}, ordersColumns...)
	if jalali {
		header = append(header, ordersJalali)
	}
	header = append(header, ordersListColumns...)
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
		cells := []string{o.PaidAt.UTC().Format(time.RFC3339), o.PaidLocal, o.OrderID, o.Type,
			strconv.FormatInt(o.TelegramID, 10), textCell(o.Username), textCell(o.PlanName["fa"]), textCell(o.PlanName["en"]),
			decimal(o.Amount, o.Currency), textCell(o.Currency), textCell(o.Method), decimal(o.Refunded, o.Currency),
			textCell(o.Provider), rng.TZ}
		if jalali {
			cells = append(cells, jalaliStamp(o.PaidLocal))
		}
		if o.ListAmount > 0 && o.ListCurrency != "" && o.Rate > 0 {
			cells = append(cells, decimal(o.ListAmount, o.ListCurrency), textCell(o.ListCurrency), strconv.FormatInt(o.Rate, 10))
		} else {
			cells = append(cells, "", "", "")
		}
		return f.data(cells...)
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
