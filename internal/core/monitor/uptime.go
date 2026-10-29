package monitor

import (
	"context"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
)

// The uptime view (docs/04-dashboard.md, "The uptime view"): how available each BOBRES service and each panel server was over
// 24 hours, 7 days and 30 days, computed from the samples core already keeps (service.up:<name> and panel.up:<id>, one
// reading every 30 seconds for 7 days and 5-minute averages for the rest of the 30 days: store.FullKeep and RolledKeep).
//
// The one rule that shapes the numbers: a moment nobody sampled is neither up nor down. Core samples, so while core itself
// is not running (a restart, a crash, a host that is off) or monitoring is switched off, nothing is recorded and that time
// is "no data". It is left out of the percentage's denominator and shown as a gap in the bar; it never counts as downtime.

// UptimeWindow is a span the view answers for, and the width of one cell of its bar.
type UptimeWindow struct {
	Name         string
	Span, Bucket time.Duration
}

// UptimeWindows are the spans of the view: a bar of 48, 56 and 60 cells.
var UptimeWindows = []UptimeWindow{
	{"24h", 24 * time.Hour, 30 * time.Minute},
	{"7d", 7 * 24 * time.Hour, 3 * time.Hour},
	{"30d", 30 * 24 * time.Hour, 12 * time.Hour},
}

// RetentionDays is how much history the view can have: what core keeps (store.RolledKeep).
const RetentionDays = int(store.RolledKeep / (24 * time.Hour))

// downEpsilon is the share of a slot below which it counts as fully up (a rolled-up slot holds an average, which is
// exactly 1 only when every sample in it was up).
const downEpsilon = 1e-6

// Availability is one component over one window.
type Availability struct {
	// Pct is the share of the observed time the component was up, in percent, rounded to three decimals; nil
	// when nothing was observed in the window (no data).
	Pct *float64 `json:"availability_pct"`
	// ObservedS is the seconds of the window somebody sampled, and WindowS the seconds of the window.
	ObservedS int64 `json:"observed_s"`
	WindowS   int64 `json:"window_s"`
	// Outages is the number of stretches of consecutive samples in which the component was down (an unobserved
	// stretch between two of them divides them), DownS their total length and LongestS the longest.
	Outages  int   `json:"outages"`
	DownS    int64 `json:"down_s"`
	LongestS int64 `json:"longest_s"`
	// Bar is the window in cells of the window's Bucket, oldest first: the seconds observed in the cell and the
	// seconds of them that were down. A cell with 0 observed is no data.
	Bar [][2]int64 `json:"bar"`
}

// availability computes a component's figures over [from, to) from its slots (sorted by start, a finer slot
// before a coarser one that starts with it). A slot counts for the part of it inside the window and not already
// counted; its value is the share of it that was up (0 or 1 as sampled, a fraction once rolled up).
func availability(slots []store.MonitorSlot, from, to, bucket int64) Availability {
	cells := int((to - from + bucket - 1) / bucket)
	a := Availability{WindowS: to - from, Bar: make([][2]int64, cells)}
	obs, dn := make([]float64, cells), make([]float64, cells)
	var observed, down, outage, longest float64
	cursor, prevEnd, inOutage := from, int64(-1), false
	for _, s := range slots {
		start, end := max(s.T, cursor), min(s.T+s.Step, to)
		cursor = max(cursor, s.T+s.Step)
		if end <= start {
			continue
		}
		dur := float64(end - start)
		share := 1 - math.Min(math.Max(s.V, 0), 1) // the share of the slot that was down
		if share <= downEpsilon {                  // rounding noise of an average is not downtime
			share = 0
		}
		lost := share * dur
		if share > 0 {
			if !inOutage || prevEnd != start { // a new stretch: the first, or after a gap, or after a slot that was up
				a.Outages++
				outage = 0
			}
			inOutage = true
			outage += lost
			longest = math.Max(longest, outage)
		} else {
			inOutage = false
		}
		prevEnd = end
		observed += dur
		down += lost
		for i := int((start - from) / bucket); i < cells && from+int64(i)*bucket < end; i++ {
			lo, hi := max(start, from+int64(i)*bucket), min(end, from+int64(i+1)*bucket)
			if hi > lo {
				obs[i] += float64(hi - lo)
				dn[i] += lost * float64(hi-lo) / dur
			}
		}
	}
	a.ObservedS, a.DownS, a.LongestS = int64(math.Round(observed)), int64(math.Round(down)), int64(math.Round(longest))
	for i := range a.Bar {
		o := int64(math.Round(obs[i]))
		a.Bar[i] = [2]int64{o, min(int64(math.Round(dn[i])), o)}
	}
	if observed > 0 {
		pct := 100.0
		if down > 0 {
			pct = math.Round((observed-down)/observed*100000) / 1000
			if pct >= 100 { // some downtime is never shown as 100%
				pct = 99.999
			}
		}
		a.Pct = &pct
	}
	return a
}

// UptimeComponent is a service or a panel server with its figures for each window.
type UptimeComponent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Enabled is whether a panel server takes new services (a server that is off is not checked, so its recent
	// past is no data); absent for a service.
	Enabled *bool                   `json:"enabled,omitempty"`
	Windows map[string]Availability `json:"windows"`
}

// UptimeWindowInfo describes a window in the answer.
type UptimeWindowInfo struct {
	Name    string `json:"name"`
	SpanS   int64  `json:"span_s"`
	BucketS int64  `json:"bucket_s"`
}

// Uptime is GET /api/v1/monitor/uptime.
type Uptime struct {
	GeneratedAt int64 `json:"generated_at"`
	// RetentionDays is how long core keeps the samples: the 30-day window is the longest view there can be. Since is
	// the start of the oldest sample of any component, null when there are none: before it (a young install, a
	// metric that is new) there is no data, and the page says so.
	RetentionDays int                `json:"retention_days"`
	SampleEveryS  int                `json:"sample_every_s"`
	Since         *int64             `json:"since"`
	Windows       []UptimeWindowInfo `json:"windows"`
	Services      []UptimeComponent  `json:"services"`
	Panels        []UptimeComponent  `json:"panels"`
	// PanelsUnavailable: the panel servers' names could not be read (the provisioner did not answer), so none is listed.
	PanelsUnavailable bool `json:"panels_unavailable"`
}

// serviceOrder is the order the BOBRES services are listed in; any other service follows by name.
var serviceOrder = []string{"core", "bot", "payments", "provisioner", "backup"}

func serviceRank(name string) int {
	if i := slices.Index(serviceOrder, name); i >= 0 {
		return i
	}
	return len(serviceOrder)
}

// Uptime computes the view.
func (m *Monitor) Uptime(ctx context.Context) (*Uptime, error) {
	now := m.now()
	u := &Uptime{GeneratedAt: now.Unix(), RetentionDays: RetentionDays, SampleEveryS: int(SampleEvery.Seconds()),
		Services: []UptimeComponent{}, Panels: []UptimeComponent{}}
	for _, w := range UptimeWindows {
		u.Windows = append(u.Windows, UptimeWindowInfo{Name: w.Name, SpanS: int64(w.Span.Seconds()), BucketS: int64(w.Bucket.Seconds())})
	}
	if m.cfg.Store == nil {
		return u, nil
	}
	names, err := m.cfg.Store.MonitorMetrics(ctx, m.cfg.Store.Conn())
	if err != nil {
		return nil, err
	}
	p := m.probe(ctx)

	// The services: those that have samples, and those core asks now (a new one has none yet: all no data).
	seen := map[string]bool{}
	var services []string
	addService := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			services = append(services, name)
		}
	}
	for _, n := range names {
		if sub, ok := strings.CutPrefix(n, ServiceUp+":"); ok {
			addService(sub)
		}
	}
	if p.report != nil {
		for _, sv := range p.report.Services {
			if sv.Status != syshealth.StatusNotInstalled {
				addService(sv.Name)
			}
		}
	}
	sort.SliceStable(services, func(i, j int) bool {
		if ri, rj := serviceRank(services[i]), serviceRank(services[j]); ri != rj {
			return ri < rj
		}
		return services[i] < services[j]
	})

	// The panel servers are the registry's (their names are the operator's); a server that was removed is gone from the view.
	type panel struct {
		id, name string
		enabled  bool
	}
	var panels []panel
	if p.servers != nil {
		for _, sv := range p.servers.Servers {
			panels = append(panels, panel{sv.ID, sv.Name, sv.Enabled})
		}
	} else if m.cfg.Servers != nil {
		u.PanelsUnavailable = true
	}

	metrics := make([]string, 0, len(services)+len(panels))
	for _, s := range services {
		metrics = append(metrics, withSubject(ServiceUp, s))
	}
	for _, pn := range panels {
		metrics = append(metrics, withSubject(PanelUp, pn.id))
	}
	longest := UptimeWindows[len(UptimeWindows)-1].Span
	slots, err := m.cfg.Store.MonitorSlots(ctx, m.cfg.Store.Conn(), metrics, now.Add(-longest), now)
	if err != nil {
		return nil, err
	}
	first := int64(math.MaxInt64)
	windows := func(metric string) map[string]Availability {
		list := slots[metric]
		if len(list) > 0 {
			first = min(first, list[0].T)
		}
		out := make(map[string]Availability, len(UptimeWindows))
		for _, w := range UptimeWindows {
			out[w.Name] = availability(list, now.Add(-w.Span).Unix(), now.Unix(), int64(w.Bucket.Seconds()))
		}
		return out
	}
	for _, s := range services {
		u.Services = append(u.Services, UptimeComponent{ID: s, Name: s, Windows: windows(withSubject(ServiceUp, s))})
	}
	for _, pn := range panels {
		on := pn.enabled
		u.Panels = append(u.Panels, UptimeComponent{ID: pn.id, Name: pn.name, Enabled: &on, Windows: windows(withSubject(PanelUp, pn.id))})
	}
	if first != math.MaxInt64 {
		u.Since = &first
	}
	return u, nil
}

// The statuses of the public status page's components and of the page as a whole.
const (
	PublicOK       = "ok"
	PublicDegraded = "degraded"
	PublicDown     = "down"
)

// PublicComponent is a part of the store as customers know it, under a generic name.
type PublicComponent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// PublicStatus is what the public status page may say: whether things work, per generic component, and nothing of how
// the store is built (no version, address, error, server name or count).
type PublicStatus struct {
	// Status is "ok" when every component works, else "degraded" (some systems are down).
	Status     string            `json:"status"`
	UpdatedAt  int64             `json:"updated_at"`
	Components []PublicComponent `json:"components"`
}

// PublicStatus reads the stack as it is now, through the same shared look as the overview (at most one asking every
// ProbeTTL): the dashboard (core), the bot, payments, and the panels as a group. A component that core cannot say anything
// about is left out rather than guessed.
func (m *Monitor) PublicStatus(ctx context.Context) *PublicStatus {
	now := m.now()
	p := m.probe(ctx)
	st := &PublicStatus{Status: PublicOK, UpdatedAt: now.Unix(), Components: []PublicComponent{}}
	add := func(id, name, status string) {
		st.Components = append(st.Components, PublicComponent{ID: id, Name: name, Status: status})
		if status != PublicOK {
			st.Status = PublicDegraded
		}
	}
	of := map[string]string{}
	if p.report != nil {
		for _, sv := range p.report.Services {
			switch sv.Status {
			case syshealth.StatusOK:
				of[sv.Name] = PublicOK
			case syshealth.StatusNotReady:
				of[sv.Name] = PublicDegraded
			case syshealth.StatusDown:
				of[sv.Name] = PublicDown
			}
		}
	}
	if _, ok := of["core"]; !ok {
		of["core"] = PublicOK // the dashboard is this process: it answers, so it works
	}
	for _, c := range []struct{ service, id, name string }{{"core", "dashboard", "Dashboard"}, {"bot", "bot", "Bot"}, {"payments", "payments", "Payments"}} {
		if status, ok := of[c.service]; ok {
			add(c.id, c.name, status)
		}
	}
	// The panels are one group: the servers that are switched on and were checked, and the provisioner that reaches them.
	panels := ""
	if p.servers != nil {
		var checked, healthy int
		for _, sv := range p.servers.Servers {
			if sv.Enabled && sv.CheckedAt > 0 {
				checked++
				if sv.Healthy {
					healthy++
				}
			}
		}
		switch {
		case checked == 0:
		case healthy == checked:
			panels = PublicOK
		case healthy == 0:
			panels = PublicDown
		default:
			panels = PublicDegraded
		}
	}
	switch of["provisioner"] {
	case PublicDown:
		panels = PublicDown
	case PublicDegraded:
		if panels == "" || panels == PublicOK {
			panels = PublicDegraded
		}
	}
	if panels != "" {
		add("panels", "Panels", panels)
	}
	return st
}
