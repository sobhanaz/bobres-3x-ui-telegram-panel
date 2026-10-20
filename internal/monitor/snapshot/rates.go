package snapshot

import "time"

// MaxRateGap is the longest time between two snapshots a rate is still
// computed over: further apart than this, the counters say little about now
// (the collector was stopped, or the host rebooted).
const MaxRateGap = 10 * time.Minute

// span is the time between two snapshots in seconds, or 0 when it is not a
// usable span for a rate (not forward, more than MaxRateGap, under a second).
func span(prev, cur *Snapshot) float64 {
	if prev == nil || cur == nil {
		return 0
	}
	dt := float64(cur.GeneratedMS-prev.GeneratedMS) / 1000
	if dt < 1 || dt > MaxRateGap.Seconds() {
		return 0
	}
	return dt
}

// NetRates is the host's receive and transmit rate in bytes per second
// between two snapshots, over the interfaces both list. An interface whose
// counter went backwards (it was reset) is left out; when none is usable, or
// the snapshots are not a usable span apart, both rates are nil: unknown, not
// zero.
func NetRates(prev, cur *Snapshot) (rx, tx *float64) {
	dt := span(prev, cur)
	if dt == 0 || prev.Host == nil || cur.Host == nil {
		return nil, nil
	}
	before := make(map[string]Iface, len(prev.Host.Net.Ifaces))
	for _, i := range prev.Host.Net.Ifaces {
		before[i.Name] = i
	}
	var drx, dtx uint64
	used := 0
	for _, i := range cur.Host.Net.Ifaces {
		p, ok := before[i.Name]
		if !ok || i.RxBytes < p.RxBytes || i.TxBytes < p.TxBytes {
			continue
		}
		drx += i.RxBytes - p.RxBytes
		dtx += i.TxBytes - p.TxBytes
		used++
	}
	if used == 0 {
		return nil, nil
	}
	r, t := float64(drx)/dt, float64(dtx)/dt
	return &r, &t
}

// CPUPct is the share of the processor that was busy between two readings of
// the cumulative jiffies; nil when the counters did not move forward.
func CPUPct(prevBusy, prevTotal, busy, total uint64) *float64 {
	if total <= prevTotal || busy < prevBusy {
		return nil
	}
	v := float64(busy-prevBusy) / float64(total-prevTotal) * 100
	v = min(max(v, 0), 100)
	return &v
}

// ContainerCPUPct is a container's CPU use, in percent of one core, between
// two readings of its cumulative CPU time (nanoseconds) taken dt apart; nil
// when the counter went backwards (the container restarted) or dt is not a
// usable span.
func ContainerCPUPct(prevNS, ns uint64, dt time.Duration) *float64 {
	if ns < prevNS || dt < time.Second || dt > MaxRateGap {
		return nil
	}
	v := float64(ns-prevNS) / float64(dt.Nanoseconds()) * 100
	return &v
}

// PrevContainers indexes a snapshot's containers by id (nil snapshot: none).
func PrevContainers(s *Snapshot) map[string]Container {
	out := map[string]Container{}
	if s == nil || s.Docker == nil {
		return out
	}
	for _, c := range s.Docker.Containers {
		out[c.ID] = c
	}
	return out
}

// Gap is the time between two snapshots; 0 when either is missing or they are
// not in order.
func Gap(prev, cur *Snapshot) time.Duration {
	if prev == nil || cur == nil || cur.GeneratedMS <= prev.GeneratedMS {
		return 0
	}
	return time.Duration(cur.GeneratedMS-prev.GeneratedMS) * time.Millisecond
}
