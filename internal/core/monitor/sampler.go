package monitor

import (
	"context"
	"runtime"
	"slices"
	"sort"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// Run samples every SampleEvery, rolls the old full-resolution blocks into 5-minute ones, prunes,
// and keeps the CI facts fresh, until ctx ends. It records nothing while monitoring is switched off
// (monitor.enabled); what was recorded stays readable.
func (m *Monitor) Run(ctx context.Context) error {
	if m.cfg.Store == nil {
		return nil
	}
	if m.cfg.Bot != nil {
		go m.ciLoop(ctx)
	}
	t := time.NewTicker(SampleEvery)
	defer t.Stop()
	for {
		m.Step(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// ciLoop asks the bot for the CI facts now and then.
func (m *Monitor) ciLoop(ctx context.Context) {
	t := time.NewTicker(CIEvery)
	defer t.Stop()
	for {
		if m.settings(ctx).Enabled {
			m.refreshCI(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Step takes one round of samples and does the upkeep that is due.
func (m *Monitor) Step(ctx context.Context) {
	if ctx.Err() != nil || !m.settings(ctx).Enabled {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()
	now := m.now()
	m.sampleHost(ctx, now)
	m.sampleStack(ctx, now)
	m.upkeep(ctx, now)
}

// sampleHost records the figures of a snapshot the first time core sees it, at the time the
// snapshot was made (so a snapshot read late still lands in its own slot). A stale or missing
// snapshot records nothing.
func (m *Monitor) sampleHost(ctx context.Context, now time.Time) {
	cv := m.collector(now)
	if cv.Snap == nil {
		return
	}
	m.mu.Lock()
	done := cv.Snap.GeneratedMS <= m.sampled
	m.mu.Unlock()
	if done {
		return
	}
	samples := hostSamples(cv)
	if len(samples) > 0 {
		at := time.Unix(cv.Snap.GeneratedAt, 0)
		if _, err := m.cfg.Store.RecordSamples(ctx, at, samples); err != nil {
			if ctx.Err() == nil {
				m.log.Warn("monitor: record host samples", "err", err) // tried again at the next step
			}
			return
		}
	}
	m.mu.Lock()
	m.sampled = max(m.sampled, cv.Snap.GeneratedMS)
	m.mu.Unlock()
}

// hostSamples are the figures a fresh snapshot gives: the host's, and the stack's containers by
// Compose service (a service with several containers is summed; its restarts are the largest).
func hostSamples(cv collectorView) []store.MonitorSample {
	s := cv.Snap
	var out []store.MonitorSample
	add := func(name string, v float64) { out = append(out, store.MonitorSample{Name: name, Value: v}) }
	if h := s.Host; h != nil {
		if h.CPU.Pct != nil {
			add(HostCPU, *h.CPU.Pct)
		}
		add(HostLoad1, h.Load[0])
		if h.Mem.Total > 0 {
			add(HostMem, float64(h.Mem.Used())/float64(h.Mem.Total)*100)
		}
		if h.Swap.Total > 0 {
			add(HostSwap, float64(h.Swap.Used)/float64(h.Swap.Total)*100)
		}
		for _, d := range h.Disks {
			add(withSubject(HostDisk, d.Mount), d.Pct())
		}
		if cv.NetRx != nil && cv.NetTx != nil {
			add(HostNetRx, *cv.NetRx)
			add(HostNetTx, *cv.NetTx)
		}
	}
	if s.Docker != nil && s.Docker.OK {
		type agg struct {
			cpu      float64
			haveCPU  bool
			mem      float64
			restarts int
			running  bool
		}
		by := map[string]*agg{}
		for _, c := range s.Docker.Containers {
			if c.Project != snapshot.BobresProject || c.Service == "" {
				continue
			}
			a := by[c.Service]
			if a == nil {
				a = &agg{}
				by[c.Service] = a
			}
			a.restarts = max(a.restarts, c.Restarts)
			if c.State != snapshot.StateRunning {
				continue
			}
			a.running = true
			a.mem += float64(c.MemBytes)
			if c.CPUPct != nil {
				a.cpu += *c.CPUPct
				a.haveCPU = true
			}
		}
		names := make([]string, 0, len(by))
		for n := range by {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			a := by[n]
			add(withSubject(ContainerRst, n), float64(a.restarts))
			if a.running {
				add(withSubject(ContainerMem, n), a.mem)
				if a.haveCPU {
					add(withSubject(ContainerCPU, n), a.cpu)
				}
			}
		}
	}
	return out
}

// sampleStack records core itself, the queues, the bot, and the state of the services, servers and
// gateways, all at the time of this step.
func (m *Monitor) sampleStack(ctx context.Context, now time.Time) {
	p := m.probe(ctx)
	var out []store.MonitorSample
	add := func(name string, v float64) { out = append(out, store.MonitorSample{Name: name, Value: v}) }
	b2f := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}

	// Core itself.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	add(CoreGoroutines, float64(runtime.NumGoroutine()))
	add(CoreHeap, float64(ms.HeapAlloc))
	if p.poolTotal > 0 {
		add(CoreDBConns, float64(p.poolInUse))
	}
	warns, errs := logging.Counts()
	if w, e, ok := m.coreRates(now, warns, errs); ok {
		add(CoreWarnings, w)
		add(CoreErrors, e)
	}

	// The queues.
	if c := p.counters; c != nil {
		add(CoreDBSize, float64(c.DBBytes))
		add(QueueProvisioning, float64(c.ProvisionQueue))
		add(QueueOutbox, float64(c.OutboxBacklog))
		add(QueueOrders, float64(c.OrdersPending))
	}
	if p.pending >= 0 {
		add(QueuePayments, float64(p.pending))
	}

	// The bot.
	if bs := p.botStats; bs != nil {
		add(BotQueued, float64(bs.UpdatesQueued))
		add(BotGoroutines, float64(bs.Goroutines))
		add(BotHeap, float64(bs.HeapBytes))
		if u, w, e, ok := m.botRates(now, bs); ok {
			add(BotUpdates, u)
			add(BotWarnings, w)
			add(BotErrors, e)
		}
	}

	// Services, servers, gateways, the license.
	if p.report != nil {
		for _, sv := range p.report.Services {
			switch sv.Status {
			case "ok":
				add(withSubject(ServiceUp, sv.Name), 1)
				add(withSubject(ServiceLatency, sv.Name), float64(sv.LatencyMS))
			case "not_ready", "down":
				add(withSubject(ServiceUp, sv.Name), 0)
			}
		}
	}
	if p.servers != nil {
		for _, sv := range p.servers.Servers {
			// A server that is switched off is no longer checked, so its last result is old news: nobody sampled it
			// (the uptime view reads that as no data, not as the downtime it had when it was switched off).
			if sv.CheckedAt > 0 && sv.Enabled {
				add(withSubject(PanelUp, sv.ID), b2f(sv.Healthy))
			}
		}
	}
	for _, g := range p.gateways {
		if g.Enabled {
			add(withSubject(GatewayUp, g.Provider), b2f(g.Offered && g.Problem == ""))
		}
	}
	if p.license != nil {
		add(LicenseOK, licenseOK(p.license))
	}
	if len(out) == 0 {
		return
	}
	if _, err := m.cfg.Store.RecordSamples(ctx, now, out); err != nil && ctx.Err() == nil {
		m.log.Warn("monitor: record samples", "err", err)
	}
}

// perMin is the change of a counter over the time between two readings, per minute; ok is false when
// the counter went backwards (the process restarted) or the readings are too close or too far apart.
func perMin(prev, cur uint64, gap time.Duration) (float64, bool) {
	if cur < prev || gap < rateGapMin || gap > rateGapMax {
		return 0, false
	}
	return float64(cur-prev) / gap.Minutes(), true
}

// coreRates are core's own warnings and errors per minute since the last step.
func (m *Monitor) coreRates(now time.Time, warns, errs uint64) (w, e float64, ok bool) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	prev, prevAt := m.prevCore, m.prevCoreAt
	m.prevCore, m.prevCoreAt = [2]uint64{warns, errs}, now
	if prevAt.IsZero() {
		return 0, 0, false
	}
	gap := now.Sub(prevAt)
	w, ok1 := perMin(prev[0], warns, gap)
	e, ok2 := perMin(prev[1], errs, gap)
	return w, e, ok1 && ok2
}

// botRates are the bot's updates, warnings and errors per minute since the last reading; none after
// a restart of the bot (its counters start again) or a long silence.
func (m *Monitor) botRates(now time.Time, cur *botfiles.BotStats) (u, w, e float64, ok bool) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	prev, prevAt := m.prevBot, m.prevBotAt
	c := *cur
	m.prevBot, m.prevBotAt = &c, now
	if prev == nil || prev.StartedAt != cur.StartedAt {
		return 0, 0, 0, false
	}
	gap := now.Sub(prevAt)
	u, ok1 := perMin(prev.UpdatesTotal, cur.UpdatesTotal, gap)
	w, ok2 := perMin(prev.WarningsTotal, cur.WarningsTotal, gap)
	e, ok3 := perMin(prev.ErrorsTotal, cur.ErrorsTotal, gap)
	return u, w, e, ok1 && ok2 && ok3
}

// upkeep rolls and prunes when it is time. Both are idempotent and safe after a restart: the due
// times are kept in memory, and a rollup that finds nothing to do costs one query.
func (m *Monitor) upkeep(ctx context.Context, now time.Time) {
	if m.roll.IsZero() || now.Sub(m.roll) >= rollEvery {
		m.roll = now
		for range 50 { // a long outage leaves many days to roll: a few batches at a time
			n, err := m.cfg.Store.RollupMonitor(ctx, now, rollBatch)
			if err != nil {
				if ctx.Err() == nil {
					m.log.Warn("monitor: rollup", "err", err)
				}
				break
			}
			if n < rollBatch {
				break
			}
		}
	}
	if m.pruneAt.IsZero() || now.Sub(m.pruneAt) >= pruneEvery {
		m.pruneAt = now
		if _, err := m.cfg.Store.PruneMonitor(ctx, now); err != nil && ctx.Err() == nil {
			m.log.Warn("monitor: prune", "err", err)
		}
	}
}

// sortedKeys is a helper for stable output.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
