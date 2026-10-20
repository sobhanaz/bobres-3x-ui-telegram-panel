package monitor

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"
)

// Range is a time window of a graph and the width of the buckets its points are made of, so that
// no window has more than about 360 points whatever the resolution it is stored at.
type Range struct {
	Name  string
	Span  time.Duration
	StepS int64
}

// Ranges are the windows the API serves.
var Ranges = []Range{
	{"1h", time.Hour, 30},
	{"6h", 6 * time.Hour, 60},
	{"24h", 24 * time.Hour, 300},
	{"7d", 7 * 24 * time.Hour, 1800},
	{"30d", 30 * 24 * time.Hour, 7200},
}

// ParseRange finds a window by name ("" is 1h).
func ParseRange(name string) (Range, bool) {
	if name == "" {
		return Ranges[0], true
	}
	for _, r := range Ranges {
		if r.Name == name {
			return r, true
		}
	}
	return Range{}, false
}

// MaxSeriesPerRequest bounds how many metrics one request may ask for.
const MaxSeriesPerRequest = 12

// ErrUnknownMetric: the name is not a metric this build records.
var ErrUnknownMetric = errors.New("monitor: unknown metric")

// Series is one metric over a window: unix seconds and values, oldest first, one point per bucket
// that has any sample (a gap is a gap, never a zero).
type Series struct {
	Metric string       `json:"metric"`
	Unit   string       `json:"unit"`
	StepS  int64        `json:"step_s"`
	Agg    string       `json:"agg"`
	Points [][2]float64 `json:"points"`
}

// Series reads a metric over the window ending now. The points are the buckets' averages (minimum
// for an up/down metric, maximum for a counter or a queue depth), at most about 360.
func (m *Monitor) Series(ctx context.Context, metric string, r Range) (*Series, error) {
	if !ValidMetric(metric) {
		return nil, ErrUnknownMetric
	}
	now := m.now()
	pts, err := m.cfg.Store.MonitorSeries(ctx, m.cfg.Store.Conn(), metric, now.Add(-r.Span), now)
	if err != nil {
		return nil, err
	}
	s := &Series{Metric: metric, Unit: Unit(metric), StepS: r.StepS, Agg: Agg(metric), Points: [][2]float64{}}
	type acc struct {
		sum, min, max float64
		n             int
	}
	buckets := map[int64]*acc{}
	for _, p := range pts {
		at := p.T - ((p.T%r.StepS)+r.StepS)%r.StepS
		a := buckets[at]
		if a == nil {
			a = &acc{min: math.Inf(1), max: math.Inf(-1)}
			buckets[at] = a
		}
		a.sum += p.V
		a.min, a.max = math.Min(a.min, p.V), math.Max(a.max, p.V)
		a.n++
	}
	ats := make([]int64, 0, len(buckets))
	for at := range buckets {
		ats = append(ats, at)
	}
	sort.Slice(ats, func(i, j int) bool { return ats[i] < ats[j] })
	for _, at := range ats {
		a := buckets[at]
		v := a.sum / float64(a.n)
		switch s.Agg {
		case "min":
			v = a.min
		case "max":
			v = a.max
		}
		s.Points = append(s.Points, [2]float64{float64(at), math.Round(v*1000) / 1000})
	}
	return s, nil
}

// MetricInfo is one recorded metric for the listing.
type MetricInfo struct {
	Metric string `json:"metric"`
	Unit   string `json:"unit"`
	Agg    string `json:"agg"`
}

// Metrics lists the metrics that have any data, sorted, with their units (a name this build does not
// know is left out).
func (m *Monitor) Metrics(ctx context.Context) ([]MetricInfo, error) {
	names, err := m.cfg.Store.MonitorMetrics(ctx, m.cfg.Store.Conn())
	if err != nil {
		return nil, err
	}
	out := make([]MetricInfo, 0, len(names))
	for _, n := range names {
		if ValidMetric(n) {
			out = append(out, MetricInfo{Metric: n, Unit: Unit(n), Agg: Agg(n)})
		}
	}
	return out, nil
}
