package domain

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

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
// last 30 days. Group is day (the default), week or month; TZ an IANA name
// (empty: the store's general.timezone, else Asia/Tehran).
type ReportRequest struct {
	Range    string
	From, To string
	Group    string
	TZ       string
}

// ReportSpan is one bucket of a report: the local date it starts on (a
// Monday for a week, the 1st for a month), and whether the range cuts it, so
// it holds only part of its days.
type ReportSpan struct {
	Start   string
	Partial bool
}

// ReportRange is a validated request: the dates it covers, the zone they are
// read in and the buckets, oldest first. Weeks start on Monday (ISO) and
// months on the 1st, in both languages.
type ReportRange struct {
	From, To string
	Group    string
	TZ       string
	Buckets  []ReportSpan
}

// Query is the range as the store reads it.
func (r *ReportRange) Query() store.ReportQuery {
	return store.ReportQuery{From: r.From, To: r.To, TZ: r.TZ, Group: r.Group}
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
		if from, to, err = namedRange(req.Range, today); err != nil {
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
	rng := &ReportRange{From: from.Format(dateLayout), To: to.Format(dateLayout), Group: group, TZ: loc.String()}
	for start := bucketStart(from, group); !start.After(to); start = nextBucket(start, group) {
		if len(rng.Buckets) == ReportMaxBuckets {
			return nil, invalid("a report has at most %d buckets", ReportMaxBuckets)
		}
		last := nextBucket(start, group).AddDate(0, 0, -1)
		rng.Buckets = append(rng.Buckets, ReportSpan{Start: start.Format(dateLayout), Partial: start.Before(from) || last.After(to)})
	}
	return rng, nil
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

func namedRange(name string, today time.Time) (from, to time.Time, err error) {
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
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
		return monthStart.AddDate(0, -1, 0), monthStart.AddDate(0, 0, -1), nil
	}
	return from, to, invalid("range must be one of %v", reportRanges)
}

// bucketStart is the first day of the bucket a date falls in.
func bucketStart(d time.Time, group string) time.Time {
	switch group {
	case GroupWeek:
		return d.AddDate(0, 0, -int((d.Weekday()+6)%7)) // Monday
	case GroupMonth:
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return d
}

func nextBucket(start time.Time, group string) time.Time {
	switch group {
	case GroupWeek:
		return start.AddDate(0, 0, 7)
	case GroupMonth:
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
	Currencies   map[string]*store.ReportCell
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
			Trials: data.Trials[span.Start], Currencies: map[string]*store.ReportCell{}}
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
	for _, people := range []map[string]int64{data.NewCustomers, data.Trials} {
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

// addCell adds b into a (both in one currency).
func addCell(a, b *store.ReportCell) {
	a.Revenue += b.Revenue
	a.Orders += b.Orders
	a.NewOrders += b.NewOrders
	a.Renewals += b.Renewals
	a.TrafficTopups += b.TrafficTopups
	for _, p := range [][2]*store.ReportFlow{{&a.Topups, &b.Topups}, {&a.Refunds, &b.Refunds}, {&a.Rejected, &b.Rejected}, {&a.GatewayFailed, &b.GatewayFailed}, {&a.Cancelled, &b.Cancelled}} {
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
