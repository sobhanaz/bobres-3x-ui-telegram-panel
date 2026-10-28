package store

import (
	"math"
	"testing"
	"time"
)

// Slots come back with how long each stands for (30 seconds as sampled, 5 minutes once rolled up, with the average of
// the samples in it), only those that overlap the window, for each metric asked for, and a slot nobody sampled is not there.
func TestMonitorSlots(t *testing.T) {
	s := monitorStore(t)
	ctx := t.Context()
	now := time.Unix(1_800_000_000, 0).UTC()
	now = time.Unix(floorTo(now.Unix(), RolledSpan)+12*3600, 0).UTC() // noon of some UTC day

	// Ten days ago, ten samples fill one 5-minute slot of a service: seven up, three down. A neighbour slot has one sample.
	old := time.Unix(floorTo(now.Add(-10*24*time.Hour).Unix(), RolledStep), 0).UTC()
	for i, v := range []float64{1, 1, 1, 1, 1, 1, 1, 0, 0, 0} {
		if _, err := s.RecordSamples(ctx, old.Add(time.Duration(i)*30*time.Second), []MonitorSample{{Name: "service.up:core", Value: v}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RecordSamples(ctx, old.Add(10*time.Minute), []MonitorSample{{Name: "service.up:core", Value: 0}}); err != nil {
		t.Fatal(err)
	}
	// Recent samples: three 30-second slots with a gap (a slot nobody sampled), and another service.
	recent := time.Unix(floorTo(now.Add(-time.Hour).Unix(), FullStep), 0).UTC()
	for _, i := range []int{0, 1, 3} {
		if _, err := s.RecordSamples(ctx, recent.Add(time.Duration(i)*30*time.Second), []MonitorSample{{Name: "service.up:core", Value: 1}, {Name: "panel.up:p1", Value: 0}}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.RollupMonitor(ctx, now, 100); err != nil || n == 0 {
		t.Fatalf("rollup: %d %v", n, err)
	}

	got, err := s.MonitorSlots(ctx, s.Conn(), []string{"service.up:core", "panel.up:p1", "service.up:nobody"}, now.Add(-30*24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["service.up:nobody"]; ok || len(got) != 2 {
		t.Fatalf("metrics: %v", got)
	}
	core := got["service.up:core"]
	if len(core) != 5 {
		t.Fatalf("service.up:core: %+v", core)
	}
	// Rolled up: one 5-minute slot with the average, then the lone sample's slot, each marked 300 seconds.
	if core[0].T != old.Unix() || core[0].Step != RolledStep || math.Abs(core[0].V-0.7) > 1e-6 {
		t.Errorf("the rolled slot: %+v", core[0])
	}
	if core[1].T != old.Add(10*time.Minute).Unix() || core[1].Step != RolledStep || core[1].V != 0 {
		t.Errorf("the second rolled slot: %+v", core[1])
	}
	// As sampled: three 30-second slots, the slot between the second and the third missing.
	for i, off := range []int64{0, 30, 90} {
		if sl := core[2+i]; sl.T != recent.Unix()+off || sl.Step != FullStep || sl.V != 1 {
			t.Errorf("slot %d: %+v", i, sl)
		}
	}
	if p := got["panel.up:p1"]; len(p) != 3 || p[0].V != 0 {
		t.Errorf("panel.up:p1: %+v", p)
	}

	// The window: a slot that ends before it or starts at its end is left out; one that overlaps an edge stays.
	win, err := s.MonitorSlots(ctx, s.Conn(), []string{"service.up:core"}, time.Unix(recent.Unix()+30, 0), time.Unix(recent.Unix()+90, 0))
	if err != nil {
		t.Fatal(err)
	}
	if l := win["service.up:core"]; len(l) != 1 || l[0].T != recent.Unix()+30 {
		t.Errorf("the window [30, 90): %+v", l)
	}
	edge, _ := s.MonitorSlots(ctx, s.Conn(), []string{"service.up:core"}, time.Unix(old.Unix()+120, 0), time.Unix(old.Unix()+240, 0))
	if l := edge["service.up:core"]; len(l) != 1 || l[0].Step != RolledStep || l[0].T != old.Unix() {
		t.Errorf("a rolled slot that overlaps the window: %+v", l)
	}
	if none, err := s.MonitorSlots(ctx, s.Conn(), nil, now.Add(-time.Hour), now); err != nil || len(none) != 0 {
		t.Errorf("no metrics asked for: %v %v", none, err)
	}
}
