package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func TestValidMetric(t *testing.T) {
	good := []string{"host.cpu_pct", "host.disk_pct:/", "host.disk_pct:/var/lib/docker", "container.cpu_pct:tls-agent", "service.up:core",
		"panel.up:8f14e45f-ceea-467a-9575-1c0f0e3a3a11", "gateway.up:zarinpal", "license.ok", "queue.pending_payments", "bot.updates_per_min"}
	for _, n := range good {
		if !ValidMetric(n) {
			t.Errorf("%q refused", n)
		}
	}
	bad := []string{"", "host.cpu_pct:", "host.cpu_pct:x" /* has no subject */, "host.disk_pct" /* needs one */, "nope.metric", "host.cpu_pct; DROP TABLE x",
		"HOST.CPU_PCT", "host.disk_pct:/a b", "host.disk_pct:" + string(make([]byte, 200)), "container.cpu_pct:a:b", "../etc/passwd", "service.up:‮core"}
	for _, n := range bad {
		if ValidMetric(n) {
			t.Errorf("%q accepted", n)
		}
	}
	if Unit("host.cpu_pct") != "percent" || Unit("host.disk_pct:/") != "percent" || Unit("container.mem_bytes:core") != "bytes" || Unit("service.up:core") != "bool" ||
		Unit("host.net_rx_bps") != "bytes_per_s" || Unit("nope") != "" {
		t.Error("units")
	}
	if Agg("service.up:core") != "min" || Agg("container.restarts:core") != "max" || Agg("host.cpu_pct") != "avg" || Agg("nope") != "avg" {
		t.Error("aggregators")
	}
}

func TestParseRange(t *testing.T) {
	for name, step := range map[string]int64{"": 30, "1h": 30, "6h": 60, "24h": 300, "7d": 1800, "30d": 7200} {
		r, ok := ParseRange(name)
		if !ok || r.StepS != step {
			t.Errorf("%q: %+v %v", name, r, ok)
		}
		// No window has more points than a page can draw.
		if n := int64(r.Span.Seconds()) / r.StepS; n > 400 {
			t.Errorf("%q: %d points", name, n)
		}
	}
	for _, bad := range []string{"2h", "1H", "30", "1h ", "all", "-1h"} {
		if _, ok := ParseRange(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func (e *env) record(metric string, at time.Time, v float64) {
	e.t.Helper()
	if _, err := e.st.RecordSamples(context.Background(), at, []store.MonitorSample{{Name: metric, Value: v}}); err != nil {
		e.t.Fatal(err)
	}
}

func TestSeriesBucketsAndAggregates(t *testing.T) {
	e := newEnv(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	e.clk.t = now
	r1h, _ := ParseRange("1h")
	r6h, _ := ParseRange("6h")

	// Four samples in 30 s slots, the 4th and 5th in one 60 s bucket of the 6h window.
	base := time.Unix(now.Unix()/3600*3600-1800, 0)
	for i, v := range []float64{10, 20, 30, 40, 50} {
		e.record("host.cpu_pct", base.Add(time.Duration(i)*30*time.Second), v)
	}
	s, err := e.m.Series(context.Background(), "host.cpu_pct", r1h)
	if err != nil || len(s.Points) != 5 || s.StepS != 30 || s.Unit != "percent" || s.Metric != "host.cpu_pct" || s.Agg != "avg" {
		t.Fatalf("1h: %+v %v", s, err)
	}
	for i, p := range s.Points {
		if int64(p[0]) != base.Unix()+int64(i)*30 || p[1] != float64(10*(i+1)) {
			t.Fatalf("point %d: %v", i, p)
		}
	}
	s, err = e.m.Series(context.Background(), "host.cpu_pct", r6h)
	if err != nil || s.StepS != 60 || len(s.Points) != 3 {
		t.Fatalf("6h: %+v %v", s, err)
	}
	// Pairs are averaged: (10+20)/2, (30+40)/2, 50.
	if s.Points[0][1] != 15 || s.Points[1][1] != 35 || s.Points[2][1] != 50 {
		t.Fatalf("6h values: %v", s.Points)
	}

	// An up/down metric keeps the worst sample of its bucket, a counter the largest.
	for i, v := range []float64{1, 0, 1, 1} {
		e.record("service.up:bot", base.Add(time.Duration(i)*30*time.Second), v)
	}
	for i, v := range []float64{3, 5, 4, 4} {
		e.record("container.restarts:bot", base.Add(time.Duration(i)*30*time.Second), v)
	}
	up, _ := e.m.Series(context.Background(), "service.up:bot", r6h)
	if up.Agg != "min" || len(up.Points) != 2 || up.Points[0][1] != 0 || up.Points[1][1] != 1 {
		t.Fatalf("up: %+v", up)
	}
	rs, _ := e.m.Series(context.Background(), "container.restarts:bot", r6h)
	if rs.Agg != "max" || rs.Points[0][1] != 5 || rs.Points[1][1] != 4 {
		t.Fatalf("restarts: %+v", rs)
	}
}

func TestSeriesWithNoDataIsEmptyNotAnError(t *testing.T) {
	e := newEnv(t)
	r, _ := ParseRange("24h")
	s, err := e.m.Series(context.Background(), "host.cpu_pct", r)
	if err != nil || s.Points == nil || len(s.Points) != 0 || s.Unit != "percent" {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := e.m.Series(context.Background(), "host.disk_pct", r); !errors.Is(err, ErrUnknownMetric) {
		t.Fatalf("a metric that needs a subject: %v", err)
	}
	if _, err := e.m.Series(context.Background(), "nope", r); !errors.Is(err, ErrUnknownMetric) {
		t.Fatalf("unknown: %v", err)
	}
}

// A gap is a gap: slots nobody sampled give no point, and a recorded zero is a point.
func TestSeriesKeepsGapsAndRealZeros(t *testing.T) {
	e := newEnv(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	e.clk.t = now
	base := time.Unix(now.Unix()/3600*3600, 0).Add(-time.Hour)
	e.record("queue.outbox", base, 0)
	e.record("queue.outbox", base.Add(30*time.Minute), 7)
	r, _ := ParseRange("1h")
	s, _ := e.m.Series(context.Background(), "queue.outbox", r)
	if len(s.Points) != 2 || s.Points[0][1] != 0 || s.Points[1][1] != 7 || int64(s.Points[1][0])-int64(s.Points[0][0]) != 1800 {
		t.Fatalf("%+v", s.Points)
	}
}

// Over 30 days there are the 5-minute rows, grouped into two-hour points: at most 360 whatever is stored.
func TestSeriesOverThirtyDaysStaysBounded(t *testing.T) {
	e := newEnv(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	e.clk.t = now
	ctx := context.Background()
	// 12 days of rolled-up rows (written directly: recording 3,000 samples one by one would be slow).
	if _, err := e.st.DB().Exec(ctx, `INSERT INTO core.monitor_metric (name) VALUES ('host.load1')`); err != nil {
		t.Fatal(err)
	}
	first := now.Unix()/86400*86400 - 20*86400
	for d := range int64(12) {
		if _, err := e.st.DB().Exec(ctx, `INSERT INTO core.monitor_series (metric_id, step, bucket, vals)
			SELECT id, 300, $1, (SELECT array_agg((s % 10)::real ORDER BY s) FROM generate_series(0, 287) s) FROM core.monitor_metric WHERE name = 'host.load1'`,
			first+d*86400); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := ParseRange("30d")
	s, err := e.m.Series(ctx, "host.load1", r)
	if err != nil {
		t.Fatal(err)
	}
	// 12 days in two-hour buckets.
	if len(s.Points) != 12*12 || s.StepS != 7200 {
		t.Fatalf("%d points at %d s", len(s.Points), s.StepS)
	}
	for i := 1; i < len(s.Points); i++ {
		if s.Points[i][0] <= s.Points[i-1][0] {
			t.Fatalf("not in order at %d", i)
		}
	}
	// Every day-long window of the rolled data has the same mean of 0..9 repeating (4.5 or near it).
	if v := s.Points[0][1]; v < 3 || v > 6 {
		t.Fatalf("average %v", v)
	}
}

func TestMetricsListsWhatIsRecorded(t *testing.T) {
	e := newEnv(t)
	e.step()
	list, err := e.m.Metrics(context.Background())
	if err != nil || len(list) == 0 {
		t.Fatalf("%v %v", list, err)
	}
	var seen bool
	for _, m := range list {
		if !ValidMetric(m.Metric) || m.Unit == "" || m.Agg == "" {
			t.Errorf("%+v", m)
		}
		seen = seen || m.Metric == "core.goroutines"
	}
	if !seen {
		t.Errorf("core.goroutines missing from %v", list)
	}
}
