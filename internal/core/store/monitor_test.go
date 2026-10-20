package store

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

func monitorStore(t *testing.T) *Store {
	t.Helper()
	s := testStore(t)
	if err := testdb.Truncate(context.Background(), s.DB(), `core.monitor_series, core.monitor_metric`); err != nil {
		t.Fatal(err)
	}
	return s
}

func monitorRows(t *testing.T, s *Store, step int) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.monitor_series WHERE step = $1`, step).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A sample lands in its 30-second slot, a missing slot gives no point (never a
// zero), writing a slot again overwrites it, and a bad value is skipped.
func TestMonitorRecordAndRead(t *testing.T) {
	s := monitorStore(t)
	ctx := context.Background()
	base := time.Unix(1_800_000_000, 0).UTC()
	base = time.Unix(floorTo(base.Unix(), FullSpan), 0).UTC()

	for i, v := range []float64{10, 20, 0, 40} { // slot 2 (i=2) holds a real 0
		if i == 1 {
			continue // nothing sampled: no point
		}
		n, err := s.RecordSamples(ctx, base.Add(time.Duration(i)*30*time.Second), []MonitorSample{
			{Name: "host.cpu_pct", Value: v}, {Name: "bad", Value: math.NaN()}, {Name: "inf", Value: math.Inf(1)}, {Name: "", Value: 1},
		})
		if err != nil || n != 1 {
			t.Fatalf("record %d: %d %v", i, n, err)
		}
	}
	// the same slot again: the last write wins
	if _, err := s.RecordSamples(ctx, base.Add(95*time.Second), []MonitorSample{{Name: "host.cpu_pct", Value: 41}}); err != nil {
		t.Fatal(err)
	}
	pts, err := s.MonitorSeries(ctx, s.Conn(), "host.cpu_pct", base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []MonitorPoint{{base.Unix(), 10}, {base.Unix() + 60, 0}, {base.Unix() + 90, 41}}
	if fmt.Sprint(pts) != fmt.Sprint(want) {
		t.Fatalf("points %v, want %v", pts, want)
	}
	// the window cuts
	pts, _ = s.MonitorSeries(ctx, s.Conn(), "host.cpu_pct", base.Add(45*time.Second), base.Add(75*time.Second))
	if len(pts) != 1 || pts[0].V != 0 {
		t.Fatalf("window: %v", pts)
	}
	if pts, err := s.MonitorSeries(ctx, s.Conn(), "nobody", base, base.Add(time.Hour)); err != nil || pts != nil {
		t.Fatalf("unknown metric: %v %v", pts, err)
	}
	names, _ := s.MonitorMetrics(ctx, s.Conn())
	if fmt.Sprint(names) != "[host.cpu_pct]" {
		t.Fatalf("metrics %v", names)
	}
	// a block across the hour edge
	if _, err := s.RecordSamples(ctx, base.Add(time.Hour+30*time.Second), []MonitorSample{{Name: "host.cpu_pct", Value: 7}}); err != nil {
		t.Fatal(err)
	}
	if monitorRows(t, s, FullStep) != 2 {
		t.Fatalf("rows %d, want 2", monitorRows(t, s, FullStep))
	}
}

// The number of metrics is bounded.
func TestMonitorMetricsAreBounded(t *testing.T) {
	s := monitorStore(t)
	ctx := context.Background()
	var in []MonitorSample
	for i := 0; i < MaxMetrics+25; i++ {
		in = append(in, MonitorSample{Name: fmt.Sprintf("m%03d", i), Value: 1})
	}
	n, err := s.RecordSamples(ctx, time.Unix(1_800_000_000, 0), in)
	if err != nil || n != MaxMetrics {
		t.Fatalf("wrote %d, %v", n, err)
	}
	// a known metric still records at the limit
	if n, err := s.RecordSamples(ctx, time.Unix(1_800_000_030, 0), in[:1]); err != nil || n != 1 {
		t.Fatalf("known: %d %v", n, err)
	}
}

// Days older than a week roll into 5-minute averages, the full blocks of that
// day go in the same step, newer days stay, rolling twice changes nothing, and
// the 5-minute rows are pruned after 30 days.
func TestMonitorRollupAndPrune(t *testing.T) {
	s := monitorStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	oldDay := floorTo(now.Add(-8*24*time.Hour).Unix(), RolledSpan)
	newDay := floorTo(now.Add(-2*24*time.Hour).Unix(), RolledSpan)
	// ten samples of 30 s fill the first 5-minute slot of the old day: 0..9
	for i := 0; i < 10; i++ {
		if _, err := s.RecordSamples(ctx, time.Unix(oldDay+int64(i)*30, 0), []MonitorSample{{Name: "a", Value: float64(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	// one sample late in the day, in the last slot
	if _, err := s.RecordSamples(ctx, time.Unix(oldDay+RolledSpan-30, 0), []MonitorSample{{Name: "a", Value: 100}, {Name: "b", Value: 5}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSamples(ctx, time.Unix(newDay+60, 0), []MonitorSample{{Name: "a", Value: 3}}); err != nil {
		t.Fatal(err)
	}
	n, err := s.RollupMonitor(ctx, now, 100)
	if err != nil || n != 2 { // a and b of the old day
		t.Fatalf("rolled %d %v", n, err)
	}
	if monitorRows(t, s, RolledStep) != 2 || monitorRows(t, s, FullStep) != 1 {
		t.Fatalf("rows rolled %d full %d", monitorRows(t, s, RolledStep), monitorRows(t, s, FullStep))
	}
	if n, err := s.RollupMonitor(ctx, now, 100); err != nil || n != 0 {
		t.Fatalf("second rollup %d %v", n, err)
	}
	pts, err := s.MonitorSeries(ctx, s.Conn(), "a", time.Unix(oldDay, 0), now)
	if err != nil {
		t.Fatal(err)
	}
	want := []MonitorPoint{{oldDay, 4.5}, {oldDay + RolledSpan - RolledStep, 100}, {newDay + 60, 3}}
	if fmt.Sprint(pts) != fmt.Sprint(want) {
		t.Fatalf("points %v, want %v", pts, want)
	}
	// the cap on one call
	if _, err := s.RecordSamples(ctx, time.Unix(oldDay-RolledSpan, 0), []MonitorSample{{Name: "a", Value: 1}, {Name: "b", Value: 1}}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RollupMonitor(ctx, now, 1); err != nil || n != 1 {
		t.Fatalf("capped rollup %d %v", n, err)
	}
	if n, err := s.RollupMonitor(ctx, now, 1); err != nil || n != 1 {
		t.Fatalf("second capped rollup %d %v", n, err)
	}

	// 29 days later the 5-minute rows of the old day are 37 days old: pruned
	removed, err := s.PruneMonitor(ctx, now.Add(29*24*time.Hour))
	if err != nil || removed != 4 {
		t.Fatalf("pruned %d %v", removed, err)
	}
	// the new day is 31 days old by then: its full block still stands (it was
	// never rolled, a rollup would have taken it) and its metric stays
	if monitorRows(t, s, FullStep) != 1 {
		t.Fatalf("full rows %d", monitorRows(t, s, FullStep))
	}
	names, _ := s.MonitorMetrics(ctx, s.Conn())
	if fmt.Sprint(names) != "[a]" {
		t.Fatalf("metrics after prune %v", names)
	}
}

// The counters read on an empty store are all zero.
func TestMonitorCountersEmpty(t *testing.T) {
	s := monitorStore(t)
	c, err := s.MonitorCounters(context.Background(), s.Conn())
	if err != nil {
		t.Fatal(err)
	}
	if c.OutboxBacklog < 0 || c.DBBytes <= 0 || c.ProvisionQueue < 0 {
		t.Fatalf("counters %+v", c)
	}
}
