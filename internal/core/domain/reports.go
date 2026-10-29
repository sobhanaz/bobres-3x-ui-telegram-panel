package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Sales reports (docs/14-reports.md defines every number). Read-only: nothing
// here changes a table. Money is never added across currencies: every amount
// is kept under its currency from the query to the answer.

// Limits of a report.
const (
	// ReportMaxDays is the longest range a report covers, and ReportMaxBuckets
	// the most buckets it has (a day group of the longest range fills it).
	ReportMaxDays    = 400
	ReportMaxBuckets = 400
	// DefaultReportTimezone is used when neither the request nor the store's
	// general.timezone setting names a zone. Core does not see BOBRES_TIMEZONE
	// (only the bot and the backup service get it), so the setting is how a
	// store with another zone makes reports follow it.
	DefaultReportTimezone = "Asia/Tehran"
	// DefaultChurnGraceDays is how many days a service may stay expired before
	// the report counts it as churned; a request may ask for 0 to MaxChurnGraceDays.
	DefaultChurnGraceDays = 7
	MaxChurnGraceDays     = 90
	// ReportTopPlans is how many plans a currency's top list holds.
	ReportTopPlans = 20
	// reportFirstYear and reportLastYear bound the dates a report accepts.
	reportFirstYear = 2000
	reportLastYear  = 2100
)

// Report groups (the size of a bucket) and ranges (named stretches of days).
const (
	GroupDay   = "day"
	GroupWeek  = "week"
	GroupMonth = "month"
)

var reportGroups = []string{GroupDay, GroupWeek, GroupMonth}

// Report calendars: how weeks and months are cut. Gregorian weeks start on
// Monday and months on the 1st; Jalali (Persian) weeks start on Saturday and
// months on Jalali day 1. Day buckets are the same in both.
const (
	CalendarGregorian = "gregorian"
	CalendarJalali    = "jalali"
)

// Named ranges, read in the report's time zone.
const (
	RangeToday      = "today"
	RangeLast7Days  = "last_7_days"
	RangeLast30Days = "last_30_days"
	RangeThisMonth  = "this_month"
	RangeLastMonth  = "last_month"
)

var reportRanges = []string{RangeToday, RangeLast7Days, RangeLast30Days, RangeThisMonth, RangeLastMonth}

const dateLayout = "2006-01-02"

// ReportRequest is what a caller asks for. Either Range (a named stretch) or
// From and To (local dates, both included, "2006-01-02"); neither means the
// last 30 days. Group is day (the default), week or month; Calendar
// gregorian (the default) or jalali; TZ an IANA name (empty: the store's
// general.timezone, else Asia/Tehran).
type ReportRequest struct {
	Range    string
	From, To string
	Group    string
	Calendar string
	TZ       string
	// ChurnGraceDays is the churn grace period in days, as text ("" for the
	// default of DefaultChurnGraceDays; "0" counts a service as churned as soon as it ends).
	ChurnGraceDays string
}

// ReportSpan is one bucket of a report: the local (Gregorian) date it starts
// on (a Monday, or in the Jalali calendar a Saturday, for a week; the 1st, or
// Jalali day 1, for a month), and whether the range cuts it, so it holds only
// part of its days.
type ReportSpan struct {
	Start   string
	Partial bool
}

// ReportRange is a validated request: the dates it covers, the zone they are
// read in and the buckets, oldest first. In the Gregorian calendar weeks start
// on Monday (ISO) and months on the 1st; in the Jalali one weeks start on
// Saturday and months on Jalali day 1.
type ReportRange struct {
	From, To string
	Group    string
	Calendar string
	TZ       string
	Buckets  []ReportSpan
	// ChurnGraceDays and AsOf define churn: a service that ended more than
	// ChurnGraceDays before AsOf (the moment the request was made) has churned.
	ChurnGraceDays int
	AsOf           time.Time
}

// folds is true when the store's buckets are not the report's: the store cuts
// weeks and months the Gregorian way only, so a Jalali week or month is read as
// days and added up by SalesReport.
func (r *ReportRange) folds() bool {
	return r.Calendar == CalendarJalali && r.Group != GroupDay
}

// Query is the range as the store reads it (a Jalali week or month is asked
// for as days: at most ReportMaxDays rows).
func (r *ReportRange) Query() store.ReportQuery {
	group := r.Group
	if r.folds() {
		group = GroupDay
	}
	return store.ReportQuery{From: r.From, To: r.To, TZ: r.TZ, Group: group, ChurnAsOf: r.AsOf, ChurnGraceDays: r.ChurnGraceDays}
}

// NewReportRange validates a request. defaultTZ is the zone used when the
// request names none; now decides what "today" is.
func NewReportRange(req ReportRequest, defaultTZ string, now time.Time) (*ReportRange, error) {
	group := req.Group
	if group == "" {
		group = GroupDay
	}
	if !contains(reportGroups, group) {
		return nil, invalid("group must be day, week or month")
	}
	calendar, err := NormalizeCalendar(req.Calendar)
	if err != nil {
		return nil, err
	}
	grace := DefaultChurnGraceDays
	if req.ChurnGraceDays != "" {
		n, err := strconv.Atoi(req.ChurnGraceDays)
		if err != nil || n < 0 || n > MaxChurnGraceDays {
			return nil, invalid("churn_grace_days must be a number of days from 0 to %d", MaxChurnGraceDays)
		}
		grace = n
	}
	tz := req.TZ
	if tz == "" {
		tz = defaultTZ
	}
	loc, err := loadZone(tz)
	if err != nil {
		return nil, err
	}
	today := dateOf(now.In(loc))
	var from, to time.Time
	switch {
	case req.Range != "" && (req.From != "" || req.To != ""):
		return nil, invalid("name a range or give from and to, not both")
	case req.Range != "":
		if from, to, err = namedRange(req.Range, today, calendar); err != nil {
			return nil, err
		}
	case req.From == "" && req.To == "":
		from, to = today.AddDate(0, 0, -29), today
	case req.From == "" || req.To == "":
		return nil, invalid("give both from and to")
	default:
		if from, err = parseDate("from", req.From); err != nil {
			return nil, err
		}
		if to, err = parseDate("to", req.To); err != nil {
			return nil, err
		}
	}
	if to.Before(from) {
		return nil, invalid("from is after to")
	}
	if days := int(to.Sub(from).Hours()/24) + 1; days > ReportMaxDays {
		return nil, invalid("a report covers at most %d days (this one covers %d)", ReportMaxDays, days)
	}
	rng := &ReportRange{From: from.Format(dateLayout), To: to.Format(dateLayout), Group: group, Calendar: calendar, TZ: loc.String(), ChurnGraceDays: grace, AsOf: now}
	for start := bucketStart(from, group, calendar); !start.After(to); start = nextBucket(start, group, calendar) {
		if len(rng.Buckets) == ReportMaxBuckets {
			return nil, invalid("a report has at most %d buckets", ReportMaxBuckets)
		}
		last := nextBucket(start, group, calendar).AddDate(0, 0, -1)
		rng.Buckets = append(rng.Buckets, ReportSpan{Start: start.Format(dateLayout), Partial: start.Before(from) || last.After(to)})
	}
	return rng, nil
}

// NormalizeCalendar reads the calendar a request names: "" is Gregorian, and
// anything but gregorian and jalali is refused with the reason the API gives.
func NormalizeCalendar(s string) (string, error) {
	switch s {
	case "":
		return CalendarGregorian, nil
	case CalendarGregorian, CalendarJalali:
		return s, nil
	}
	return "", invalid("calendar must be gregorian or jalali")
}

// loadZone checks a time zone name: a zone Go knows (the name goes to
// PostgreSQL as a parameter, never into SQL, and the store tells a zone
// PostgreSQL lacks).
func loadZone(name string) (*time.Location, error) {
	if name == "Local" || len(name) > 64 {
		return nil, invalid("unknown time zone; use a name such as Asia/Tehran or Europe/Berlin")
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, invalid("unknown time zone; use a name such as Asia/Tehran or Europe/Berlin")
	}
	return loc, nil
}

// dateOf is the calendar date of t as a UTC midnight (dates are calendar
// arithmetic: no zone, no daylight saving).
func dateOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func parseDate(name, s string) (time.Time, error) {
	d, err := time.Parse(dateLayout, s)
	if err != nil || d.Year() < reportFirstYear || d.Year() > reportLastYear {
		return time.Time{}, invalid("%s must be a date like 2026-10-14 (years %d to %d)", name, reportFirstYear, reportLastYear)
	}
	return d, nil
}

func namedRange(name string, today time.Time, calendar string) (from, to time.Time, err error) {
	monthStart := bucketStart(today, GroupMonth, calendar)
	switch name {
	case RangeToday:
		return today, today, nil
	case RangeLast7Days:
		return today.AddDate(0, 0, -6), today, nil
	case RangeLast30Days:
		return today.AddDate(0, 0, -29), today, nil
	case RangeThisMonth:
		return monthStart, today, nil
	case RangeLastMonth:
		last := monthStart.AddDate(0, 0, -1)
		return bucketStart(last, GroupMonth, calendar), last, nil
	}
	return from, to, invalid("range must be one of %v", reportRanges)
}

// bucketStart is the first day of the bucket a date falls in.
func bucketStart(d time.Time, group, calendar string) time.Time {
	jalali := calendar == CalendarJalali
	switch group {
	case GroupWeek:
		if jalali {
			return d.AddDate(0, 0, -int((d.Weekday()+1)%7)) // Saturday
		}
		return d.AddDate(0, 0, -int((d.Weekday()+6)%7)) // Monday
	case GroupMonth:
		if jalali {
			return jalaliMonthStart(d)
		}
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return d
}

// nextBucket is the first day of the bucket after the one starting on start.
func nextBucket(start time.Time, group, calendar string) time.Time {
	switch group {
	case GroupWeek:
		return start.AddDate(0, 0, 7)
	case GroupMonth:
		if calendar == CalendarJalali {
			return nextJalaliMonth(start)
		}
		return start.AddDate(0, 1, 0)
	}
	return start.AddDate(0, 0, 1)
}

// ReportRange resolves a request: the zone is the request's, else the store's
// zone (storeZone).
func (s *Service) ReportRange(ctx context.Context, req ReportRequest, now time.Time) (*ReportRange, error) {
	def := DefaultReportTimezone
	if req.TZ == "" {
		set, err := s.Settings(ctx)
		if err != nil {
			return nil, err
		}
		def = s.storeZone(set, func(source, zone string) {
			s.cfg.Log.Warn("reports: not a time zone, skipped", "source", source, "value", zone)
		})
	}
	return NewReportRange(req, def, now)
}

// ExportZone is the time zone a CSV file writes its local times in (the ledger
// and audit exports): the request's, else the store's, as the reports read it.
// A name Go does not know is refused.
func (s *Service) ExportZone(ctx context.Context, requested string) (*time.Location, error) {
	name := requested
	if name == "" {
		set, err := s.Settings(ctx)
		if err != nil {
			return nil, err
		}
		name = s.storeZone(set, func(source, zone string) {
			s.cfg.Log.Warn("exports: not a time zone, skipped", "source", source, "value", zone)
		})
	}
	return loadZone(name)
}

// storeZone is the time zone the store's days are read in: the general.timezone
// setting (set holds the settings), else BOBRES_TIMEZONE, else Asia/Tehran. A
// setting or an environment value that is no zone core can load (the setting was
// valid when it was saved; the variable is not checked at all) is passed to
// skipped and left out, so one bad value never stops the reports.
func (s *Service) storeZone(set map[string]string, skipped func(source, zone string)) string {
	for _, c := range []struct{ source, zone string }{{"general.timezone", set["general.timezone"]}, {"BOBRES_TIMEZONE", s.cfg.Timezone}} {
		if c.zone == "" {
			continue
		}
		if _, err := loadZone(c.zone); err != nil {
			skipped(c.source, c.zone)
			continue
		}
		return c.zone
	}
	return DefaultReportTimezone
}

// ReportBucket is one bucket of a sales report. Currencies holds an entry
// for every currency the report saw, zero where nothing happened, so a chart
// has no gaps.
type ReportBucket struct {
	Date    string
	Partial bool
	// NewCustomers and Trials count people, so they have no currency.
	NewCustomers int64
	Trials       int64
	// Churn, by the bucket the service's expiry (the customer's last expiry)
	// falls in; no currency (docs/14-reports.md): services that ended and
	// stayed ended past the grace period, people whose last service did, and
	// services that ended inside the grace period and may still renew.
	ChurnedServices  int64
	ChurnedCustomers int64
	AtRisk           int64
	Currencies       map[string]*store.ReportCell
}

// SalesReport is a range's numbers per bucket and in total.
type SalesReport struct {
	Range *ReportRange
	// Currencies lists every currency with any number in the range, sorted.
	Currencies []string
	Buckets    []ReportBucket
	// Totals sums the buckets, currency by currency (Date and Partial unset).
	Totals ReportBucket
}

// ReportDeadline bounds a whole report (its statements have their own limit).
func ReportDeadline() time.Duration { return store.ReportTimeout + 10*time.Second }

// ErrReportTimeout: the range holds more than a report can read in time.
// Staff are told to choose a shorter one.
var ErrReportTimeout = errors.New("domain: the report took too long")

// SalesReport computes the range's summary.
func (s *Service) SalesReport(ctx context.Context, rng *ReportRange) (*SalesReport, error) {
	ctx, cancel := context.WithTimeout(ctx, ReportDeadline())
	defer cancel()
	data, err := s.st.SalesSummary(ctx, rng.Query())
	if err != nil {
		return nil, reportError(err)
	}
	if rng.folds() {
		if data, err = foldDays(rng, data); err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	for _, cells := range data.Cells {
		for cur := range cells {
			seen[cur] = true
		}
	}
	rep := &SalesReport{Range: rng, Currencies: []string{}, Totals: ReportBucket{Currencies: map[string]*store.ReportCell{}}}
	for cur := range seen {
		rep.Currencies = append(rep.Currencies, cur)
	}
	sort.Strings(rep.Currencies)
	for _, cur := range rep.Currencies {
		rep.Totals.Currencies[cur] = &store.ReportCell{}
	}
	for _, span := range rng.Buckets {
		b := ReportBucket{Date: span.Start, Partial: span.Partial, NewCustomers: data.NewCustomers[span.Start],
			Trials: data.Trials[span.Start], ChurnedServices: data.ChurnedServices[span.Start],
			ChurnedCustomers: data.ChurnedCustomers[span.Start], AtRisk: data.AtRisk[span.Start], Currencies: map[string]*store.ReportCell{}}
		for _, cur := range rep.Currencies {
			c := &store.ReportCell{}
			if got := data.Cells[span.Start][cur]; got != nil {
				*c = *got
			}
			b.Currencies[cur] = c
			addCell(rep.Totals.Currencies[cur], c)
		}
		rep.Totals.NewCustomers += b.NewCustomers
		rep.Totals.Trials += b.Trials
		rep.Totals.ChurnedServices += b.ChurnedServices
		rep.Totals.ChurnedCustomers += b.ChurnedCustomers
		rep.Totals.AtRisk += b.AtRisk
		rep.Buckets = append(rep.Buckets, b)
	}
	// Every row the store found must have landed in a bucket: the buckets
	// here and the SQL's date_trunc agree, or numbers would vanish quietly.
	starts := make(map[string]bool, len(rng.Buckets))
	for _, span := range rng.Buckets {
		starts[span.Start] = true
	}
	stray := 0
	for b, cells := range data.Cells {
		if !starts[b] {
			stray += len(cells)
		}
	}
	for _, people := range []map[string]int64{data.NewCustomers, data.Trials, data.ChurnedServices, data.ChurnedCustomers, data.AtRisk} {
		for b := range people {
			if !starts[b] {
				stray++
			}
		}
	}
	if stray > 0 {
		return nil, fmt.Errorf("sales report: %d numbers fell outside the report's buckets", stray)
	}
	return rep, nil
}

// foldDays turns the store's day rows into the buckets of a Jalali week or
// month: a day's cells are added into its bucket's, and its people counts too
// (every counted person is counted on one day only, so the sums are the
// bucket's; a churned customer is counted on the day of their last expiry
// only). A day outside the range is an error, as a stray bucket is.
func foldDays(rng *ReportRange, data *store.SalesData) (*store.SalesData, error) {
	out := store.NewSalesData()
	bucketOf := func(day string) (string, error) {
		d, err := time.Parse(dateLayout, day)
		if err != nil || day < rng.From || day > rng.To {
			return "", fmt.Errorf("sales report: a number fell on %q, outside %s to %s", day, rng.From, rng.To)
		}
		return bucketStart(d, rng.Group, rng.Calendar).Format(dateLayout), nil
	}
	for day, cells := range data.Cells {
		start, err := bucketOf(day)
		if err != nil {
			return nil, err
		}
		if out.Cells[start] == nil {
			out.Cells[start] = map[string]*store.ReportCell{}
		}
		for cur, c := range cells {
			if out.Cells[start][cur] == nil {
				out.Cells[start][cur] = &store.ReportCell{}
			}
			addCell(out.Cells[start][cur], c)
		}
	}
	for _, p := range []struct{ in, out map[string]int64 }{{data.NewCustomers, out.NewCustomers}, {data.Trials, out.Trials},
		{data.ChurnedServices, out.ChurnedServices}, {data.ChurnedCustomers, out.ChurnedCustomers}, {data.AtRisk, out.AtRisk}} {
		for day, n := range p.in {
			start, err := bucketOf(day)
			if err != nil {
				return nil, err
			}
			p.out[start] += n
		}
	}
	return out, nil
}

// addCell adds b into a (both in one currency).
func addCell(a, b *store.ReportCell) {
	a.Revenue += b.Revenue
	a.Orders += b.Orders
	a.NewOrders += b.NewOrders
	a.Renewals += b.Renewals
	a.TrafficTopups += b.TrafficTopups
	for _, p := range [][2]*store.ReportFlow{{&a.Topups, &b.Topups}, {&a.Refunds, &b.Refunds}, {&a.Rejected, &b.Rejected}, {&a.GatewayFailed, &b.GatewayFailed}, {&a.Cancelled, &b.Cancelled}, {&a.Licenses, &b.Licenses}, {&a.Digital, &b.Digital}, {&a.Cashback, &b.Cashback}} {
		p[0].Count += p[1].Count
		p[0].Amount += p[1].Amount
	}
}

// Net is what the store kept in a currency: revenue less refunds.
func Net(c *store.ReportCell) int64 { return c.Revenue - c.Refunds.Amount }

// SalesByMethod splits the range's revenue by how orders were paid, per
// currency.
func (s *Service) SalesByMethod(ctx context.Context, rng *ReportRange) ([]store.MethodRow, error) {
	ctx, cancel := context.WithTimeout(ctx, ReportDeadline())
	defer cancel()
	rows, err := s.st.SalesByMethod(ctx, rng.Query())
	return rows, reportError(err)
}

// TopPlans lists the best-selling plans of the range, per currency.
func (s *Service) TopPlans(ctx context.Context, rng *ReportRange) ([]store.PlanRow, error) {
	ctx, cancel := context.WithTimeout(ctx, ReportDeadline())
	defer cancel()
	rows, err := s.st.TopPlans(ctx, rng.Query(), ReportTopPlans)
	return rows, reportError(err)
}

// PlanPoint is what a plan sold in one bucket of the report.
type PlanPoint struct {
	// Date is the bucket's start, as in SalesReport.
	Date    string
	Orders  int64
	Revenue int64
}

// PlanSeries is one plan's sales in one currency across the report's buckets.
// PlanID is empty for the row that holds every plan beyond the currency's top
// ReportTopPlans ("other"), which has no name.
type PlanSeries struct {
	PlanID   string
	Name     map[string]string
	Currency string
	// Orders and Revenue are the totals over the range; they are the figures
	// TopPlans gives for the plan (for "other", the sum of the plans it holds).
	Orders  int64
	Revenue int64
	// Points are the buckets where the plan sold something, oldest first.
	Points []PlanPoint
}

// SalesByPlan splits the range's revenue by plan and by bucket, per currency:
// the best ReportTopPlans plans of each currency one by one and the rest
// together as "other", so a bucket's rows add up to its revenue (when every
// purchase has its order and plan, as TopPlans assumes). The buckets are the
// report's (a Jalali week or month is read as days and added up, like
// SalesReport does).
func (s *Service) SalesByPlan(ctx context.Context, rng *ReportRange) ([]PlanSeries, error) {
	ctx, cancel := context.WithTimeout(ctx, ReportDeadline())
	defer cancel()
	rows, err := s.st.PlanSeries(ctx, rng.Query())
	if err != nil {
		return nil, reportError(err)
	}
	starts := make(map[string]bool, len(rng.Buckets))
	for _, span := range rng.Buckets {
		starts[span.Start] = true
	}
	type key struct{ cur, plan string }

	totals := map[key]*PlanSeries{}
	points := map[key]map[string]*planCell{}
	for _, r := range rows {
		bucket := r.Bucket
		if rng.folds() {
			d, err := time.Parse(dateLayout, r.Bucket)
			if err != nil || r.Bucket < rng.From || r.Bucket > rng.To {
				return nil, fmt.Errorf("plan series: a number fell on %q, outside %s to %s", r.Bucket, rng.From, rng.To)
			}
			bucket = bucketStart(d, rng.Group, rng.Calendar).Format(dateLayout)
		}
		if !starts[bucket] {
			return nil, fmt.Errorf("plan series: a number fell outside the report's buckets (%s)", bucket)
		}
		k := key{r.Currency, r.PlanID}
		if totals[k] == nil {
			totals[k] = &PlanSeries{PlanID: r.PlanID, Name: r.Name, Currency: r.Currency}
			points[k] = map[string]*planCell{}
		}
		totals[k].Orders += r.Orders
		totals[k].Revenue += r.Revenue
		c := points[k][bucket]
		if c == nil {
			c = &planCell{}
			points[k][bucket] = c
		}
		c.orders += r.Orders
		c.revenue += r.Revenue
	}
	byCurrency := map[string][]*PlanSeries{}
	for k, p := range totals {
		byCurrency[k.cur] = append(byCurrency[k.cur], p)
	}
	currencies := make([]string, 0, len(byCurrency))
	for cur := range byCurrency {
		currencies = append(currencies, cur)
	}
	sort.Strings(currencies)
	out := []PlanSeries{}
	for _, cur := range currencies {
		plans := byCurrency[cur]
		// The same order as TopPlans: revenue, then orders, then the id.
		sort.Slice(plans, func(i, j int) bool {
			a, b := plans[i], plans[j]
			if a.Revenue != b.Revenue {
				return a.Revenue > b.Revenue
			}
			if a.Orders != b.Orders {
				return a.Orders > b.Orders
			}
			return a.PlanID < b.PlanID
		})
		other := &PlanSeries{Currency: cur}
		otherCells := map[string]*planCell{}
		for i, p := range plans {
			cells := points[key{cur, p.PlanID}]
			if i >= ReportTopPlans {
				other.Orders += p.Orders
				other.Revenue += p.Revenue
				for b, c := range cells {
					if otherCells[b] == nil {
						otherCells[b] = &planCell{}
					}
					otherCells[b].orders += c.orders
					otherCells[b].revenue += c.revenue
				}
				continue
			}
			p.Points = pointsOf(cells)
			out = append(out, *p)
		}
		if len(otherCells) > 0 {
			other.Points = pointsOf(otherCells)
			out = append(out, *other)
		}
	}
	return out, nil
}

// planCell is what a plan sold in one bucket while SalesByPlan adds it up.
type planCell struct{ orders, revenue int64 }

// pointsOf lists a plan's buckets oldest first.
func pointsOf(cells map[string]*planCell) []PlanPoint {
	out := make([]PlanPoint, 0, len(cells))
	for date, c := range cells {
		out = append(out, PlanPoint{Date: date, Orders: c.orders, Revenue: c.revenue})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// SalesHours counts the range's paid orders by weekday and hour of the local
// day, per currency.
func (s *Service) SalesHours(ctx context.Context, rng *ReportRange) ([]store.HourRow, error) {
	ctx, cancel := context.WithTimeout(ctx, ReportDeadline())
	defer cancel()
	rows, err := s.st.SalesHours(ctx, rng.Query())
	return rows, reportError(err)
}

// ExportDeadline bounds a CSV export: one minute for the whole of it, the same
// number as each statement's limit (store.ExportTimeout) and the docs'.
func ExportDeadline() time.Duration { return store.ExportTimeout }

// EachPaidOrder streams the orders paid in the range, oldest first, up to
// max of them; it reports whether more were paid.
func (s *Service) EachPaidOrder(ctx context.Context, rng *ReportRange, max int, fn func(store.PaidOrderRow) error) (bool, error) {
	more, err := s.st.EachPaidOrder(ctx, rng.Query(), max, fn)
	return more, reportError(err)
}

// reportError tells staff a zone PostgreSQL lacks is their input's fault, and
// a report that ran out of time (PostgreSQL's limit, or the deadline of the
// whole report) from a failure.
func reportError(err error) error {
	switch {
	case errors.Is(err, store.ErrBadTimezone):
		return invalid("unknown time zone; use a name such as Asia/Tehran or Europe/Berlin")
	case errors.Is(err, store.ErrReportTimeout), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: choose a shorter range", ErrReportTimeout)
	}
	return err
}

// ReportsSince is the instant the reports and the overview's revenue count from; nil when they count everything.
func (s *Service) ReportsSince(ctx context.Context) (*time.Time, error) {
	t, ok, err := s.st.ReportsSince(ctx, s.st.Conn())
	if err != nil || !ok {
		return nil, err
	}
	return &t, nil
}

// ResetReports starts the reports from zero: from this moment on they count only what happens next. Nothing is deleted: orders, payments,
// wallets, the ledger and the audit log stay as they are, and ClearReportsReset brings every earlier sale back into the reports. It is for the
// day the store goes live after tests, so it is the owner's (the web layer checks the permission) and it is audited.
func (s *Service) ResetReports(ctx context.Context, actor *store.User) (time.Time, error) {
	now := time.Now().UTC().Truncate(time.Second)
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		before, hadBefore, err := s.st.ReportsSince(ctx, tx)
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(now.Format(time.RFC3339))
		if err := s.st.SetSetting(ctx, tx, store.ReportsSinceKey, raw); err != nil {
			return err
		}
		var was any
		if hadBefore {
			was = map[string]string{"since": before.UTC().Format(time.RFC3339)}
		}
		return s.auditChange(ctx, tx, actor, "reports.reset", "settings", store.ReportsSinceKey, was, map[string]string{"since": now.Format(time.RFC3339)}, "")
	})
	return now, err
}

// ClearReportsReset makes the reports count everything again (what ResetReports hid is still there).
func (s *Service) ClearReportsReset(ctx context.Context, actor *store.User) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		before, hadBefore, err := s.st.ReportsSince(ctx, tx)
		if err != nil {
			return err
		}
		if !hadBefore {
			return nil
		}
		if err := s.st.DeleteSetting(ctx, tx, store.ReportsSinceKey); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "reports.reset_clear", "settings", store.ReportsSinceKey,
			map[string]string{"since": before.UTC().Format(time.RFC3339)}, nil, "")
	})
}
