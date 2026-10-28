package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
)

// ---- the calculation ----

// winStart is the start of the windows below, on the 30 second grid.
const winStart = int64(900_000)

// pattern makes one 30 second slot of each character: u is up, d is down, "." was not sampled.
func pattern(from int64, p string) []store.MonitorSlot {
	var out []store.MonitorSlot
	for i, c := range p {
		t := from + int64(i)*30
		switch c {
		case 'u':
			out = append(out, store.MonitorSlot{T: t, Step: 30, V: 1})
		case 'd':
			out = append(out, store.MonitorSlot{T: t, Step: 30, V: 0})
		}
	}
	return out
}

func pct(t *testing.T, a Availability) float64 {
	t.Helper()
	if a.Pct == nil {
		t.Fatalf("no percentage: %+v", a)
	}
	return *a.Pct
}

// Twenty slots of 30 seconds in two cells of 300 seconds.
func window20(p string) Availability {
	return availability(pattern(winStart, p), winStart, winStart+600, 300)
}

func TestAvailabilityAllUp(t *testing.T) {
	a := window20("uuuuuuuuuuuuuuuuuuuu")
	if pct(t, a) != 100 || a.Outages != 0 || a.DownS != 0 || a.LongestS != 0 || a.ObservedS != 600 || a.WindowS != 600 {
		t.Errorf("%+v", a)
	}
	if len(a.Bar) != 2 || a.Bar[0] != [2]int64{300, 0} || a.Bar[1] != [2]int64{300, 0} {
		t.Errorf("bar: %v", a.Bar)
	}
}

// No sample at all is no data: no percentage, never 0 and never 100.
func TestAvailabilityNoDataIsNeitherUpNorDown(t *testing.T) {
	a := window20("")
	if a.Pct != nil || a.ObservedS != 0 || a.Outages != 0 || a.DownS != 0 {
		t.Errorf("empty: %+v", a)
	}
	for i, c := range a.Bar {
		if c != [2]int64{} {
			t.Errorf("cell %d of an empty window: %v", i, c)
		}
	}
	// Time nobody sampled is left out of the denominator: up for the 4 slots there are, so 100%, over 120 seconds observed.
	a = window20("uu..............uu..")
	if pct(t, a) != 100 || a.ObservedS != 120 || a.WindowS != 600 || a.Outages != 0 {
		t.Errorf("gaps: %+v", a)
	}
	// A gap is not downtime: 1 of 4 observed slots down is 75%, not 1 of 20.
	a = window20("u...........d.uu....")
	if pct(t, a) != 75 || a.ObservedS != 120 || a.DownS != 30 {
		t.Errorf("down among gaps: %+v", a)
	}
	// The bar tells a gap from an outage: a cell with nothing observed is [0, 0].
	a = window20("..........dddddddddd")
	if a.Bar[0] != [2]int64{0, 0} || a.Bar[1] != [2]int64{300, 300} {
		t.Errorf("bar: %v", a.Bar)
	}
	if pct(t, a) != 0 {
		t.Errorf("all down: %+v", a)
	}
}

func TestAvailabilityOutages(t *testing.T) {
	// One stretch of three down slots.
	a := window20("uuuddduuuuuuuuuuuuuu")
	if pct(t, a) != 85 || a.Outages != 1 || a.DownS != 90 || a.LongestS != 90 || a.ObservedS != 600 {
		t.Errorf("one outage: %+v", a)
	}
	if a.Bar[0] != [2]int64{300, 90} || a.Bar[1] != [2]int64{300, 0} {
		t.Errorf("bar: %v", a.Bar)
	}
	// Two stretches divided by an up slot, the longer one counts for the longest.
	a = window20("dduddduuuuuuuuuuuuuu")
	if a.Outages != 2 || a.DownS != 150 || a.LongestS != 90 {
		t.Errorf("two outages: %+v", a)
	}
	// An unobserved stretch divides two outages too: it is not known that nothing was up in between.
	a = window20("dd...ddd.dduuuuuuuuu")
	if a.Outages != 3 || a.DownS != 210 || a.LongestS != 90 {
		t.Errorf("outages around gaps: %+v", a)
	}
	// An outage at the first and the last slot of the window.
	a = window20("duuuuuuuuuuuuuuuuuud")
	if a.Outages != 2 || a.DownS != 60 || a.LongestS != 30 {
		t.Errorf("edges: %+v", a)
	}
	// All down is 0%, one outage as long as the window.
	a = window20("dddddddddddddddddddd")
	if pct(t, a) != 0 || a.Outages != 1 || a.DownS != 600 || a.LongestS != 600 {
		t.Errorf("all down: %+v", a)
	}
}

// A slot that begins before the window or ends after it counts for the part inside, and a slot outside it for nothing.
func TestAvailabilityBoundaries(t *testing.T) {
	slots := []store.MonitorSlot{
		{T: winStart - 300, Step: 30, V: 0}, // ends long before the window
		{T: winStart - 30, Step: 30, V: 0},  // ends exactly where the window starts
		{T: winStart + 45, Step: 30, V: 0},  // half before, half in: the window starts at winStart+60
		{T: winStart + 75, Step: 30, V: 1},
		{T: winStart + 105, Step: 30, V: 0}, // starts inside, ends after the window (ends at winStart+120)
		{T: winStart + 120, Step: 30, V: 0}, // starts where the window ends
		{T: winStart + 600, Step: 30, V: 0},
	}
	a := availability(slots, winStart+60, winStart+120, 30)
	// 15 s of the first, 30 s up (winStart+75 .. winStart+105), 15 s of the last that fit.
	if a.ObservedS != 60 || a.DownS != 30 || a.WindowS != 60 {
		t.Fatalf("%+v", a)
	}
	if want := 50.0; pct(t, a) != want {
		t.Errorf("pct %v, want %v", pct(t, a), want)
	}
	if len(a.Bar) != 2 || a.Bar[0] != [2]int64{30, 15} || a.Bar[1] != [2]int64{30, 15} {
		t.Errorf("bar: %v", a.Bar)
	}
	if a.Outages != 2 {
		t.Errorf("outages %d", a.Outages)
	}
}

// A rolled-up slot holds the share of its 5 minutes that were up: only that part is downtime, spread over the cells it covers.
func TestAvailabilityRolledUpSlots(t *testing.T) {
	slots := []store.MonitorSlot{{T: winStart + 150, Step: 300, V: 0.7}}
	a := availability(slots, winStart, winStart+600, 300)
	if a.ObservedS != 300 || a.DownS != 90 || a.Outages != 1 || a.LongestS != 90 {
		t.Fatalf("%+v", a)
	}
	if pct(t, a) != 70 {
		t.Errorf("pct %v", pct(t, a))
	}
	// The slot straddles the cells: each gets half of it, with its share of the loss.
	if a.Bar[0] != [2]int64{150, 45} || a.Bar[1] != [2]int64{150, 45} {
		t.Errorf("bar: %v", a.Bar)
	}
	// Followed straight away by down slots, the loss continues one outage; after an up slot it is another.
	slots = []store.MonitorSlot{{T: winStart, Step: 300, V: 0.5}, {T: winStart + 300, Step: 30, V: 0}, {T: winStart + 330, Step: 30, V: 1}, {T: winStart + 360, Step: 30, V: 0}}
	a = availability(slots, winStart, winStart+600, 300)
	if a.Outages != 2 || a.DownS != 150+30+30 || a.LongestS != 180 {
		t.Errorf("%+v", a)
	}
}

// A finer slot that starts a coarser one is not counted twice.
func TestAvailabilityOverlappingSlotsCountOnce(t *testing.T) {
	slots := []store.MonitorSlot{{T: winStart, Step: 30, V: 0}, {T: winStart, Step: 300, V: 1}}
	a := availability(slots, winStart, winStart+300, 300)
	if a.ObservedS != 300 || a.DownS != 30 || pct(t, a) != 90 {
		t.Errorf("%+v", a)
	}
}

// A little downtime is never shown as 100%, and the noise of an average is not downtime.
func TestAvailabilityNeverRoundsDowntimeUpToAHundred(t *testing.T) {
	month := int64(30 * 86400)
	a := availability([]store.MonitorSlot{{T: winStart, Step: month, V: 1 - 2e-6}}, winStart, winStart+month, 12*3600)
	if got := pct(t, a); got != 99.999 {
		t.Errorf("a little downtime: %v", got)
	}
	if a.Outages != 1 {
		t.Errorf("outages: %d", a.Outages)
	}
	a = availability([]store.MonitorSlot{{T: winStart, Step: month, V: 1 - 1e-12}}, winStart, winStart+month, 12*3600)
	if pct(t, a) != 100 || a.Outages != 0 || a.DownS != 0 {
		t.Errorf("noise: %+v", a)
	}
	// Three decimals.
	a = availability([]store.MonitorSlot{{T: winStart, Step: 30, V: 0}, {T: winStart + 30, Step: 6970, V: 1}}, winStart, winStart+7000, 7000)
	if got := pct(t, a); math.Abs(got-99.571) > 1e-9 {
		t.Errorf("30 s of 7000: %v", got)
	}
}

// The bar has one cell per bucket (the last may be shorter), and what the cells hold adds up to the whole.
func TestAvailabilityBarAddsUp(t *testing.T) {
	to := winStart + 1000 // 4 cells of 300 seconds, the last 100
	var slots []store.MonitorSlot
	for at := winStart; at < to; at += 30 {
		v := 1.0
		if (at-winStart)/30%7 == 0 {
			v = 0
		}
		slots = append(slots, store.MonitorSlot{T: at, Step: 30, V: v})
	}
	a := availability(slots, winStart, to, 300)
	if len(a.Bar) != 4 {
		t.Fatalf("cells: %d", len(a.Bar))
	}
	var obs, down int64
	for _, c := range a.Bar {
		obs += c[0]
		down += c[1]
		if c[1] > c[0] {
			t.Errorf("more down than observed: %v", c)
		}
	}
	if obs != a.ObservedS || down != a.DownS || a.ObservedS != 1000 {
		t.Errorf("sums %d %d: %+v", obs, down, a)
	}
}

// ---- from the stored samples ----

func (e *env) uptime() *Uptime {
	e.t.Helper()
	u, err := e.m.Uptime(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func ids(list []UptimeComponent) []string {
	out := []string{}
	for _, c := range list {
		out = append(out, c.ID)
	}
	return out
}

func TestUptimeFromTheStoredSamples(t *testing.T) {
	e := newEnv(t)
	e.clk.t = time.Unix(1_800_000_000/30*30, 0).UTC()
	now := e.clk.now()
	e.servers.list = &domain.ServerList{Servers: []domain.Server{
		{ID: "srv-1", Name: "DE-1", Enabled: true, Healthy: true, CheckedAt: 1},
		{ID: "srv-2", Name: "NL-1", Enabled: false},
		{ID: "srv-3", Name: "Brand new", Enabled: true},
	}}
	// The 20 slots from 20 to 10 minutes ago. Core: 10 up, 2 down, 3 not sampled, 5 up. Then nothing (it was not running).
	at := now.Add(-20 * time.Minute)
	slot := func(i int) time.Time { return at.Add(time.Duration(i) * 30 * time.Second) }
	for i := 0; i < 20; i++ {
		switch {
		case i < 10, i >= 15:
			e.record("service.up:core", slot(i), 1)
		case i < 12:
			e.record("service.up:core", slot(i), 0)
		}
	}
	for i := 0; i < 20; i++ {
		e.record("service.up:bot", slot(i), 1)
		e.record("panel.up:srv-1", slot(i), 1)
	}
	e.record("panel.up:srv-gone", slot(0), 0) // a server that was removed
	e.record("service.up:backup", slot(0), 1) // a service that is no longer reported

	u := e.uptime()
	if u.RetentionDays != 30 || u.SampleEveryS != 30 || len(u.Windows) != 3 || u.Windows[0].Name != "24h" || u.Windows[2].BucketS != 12*3600 {
		t.Errorf("header: %+v", u)
	}
	if u.Since == nil || *u.Since != slot(0).Unix() {
		t.Errorf("since: %v want %d", u.Since, slot(0).Unix())
	}
	// Services in the order of the stack, those with samples and those reported now; panels as the registry lists them.
	if got := strings.Join(ids(u.Services), ","); got != "core,bot,payments,provisioner,backup" {
		t.Errorf("services: %s", got)
	}
	if got := strings.Join(ids(u.Panels), ","); got != "srv-1,srv-2,srv-3" {
		t.Errorf("panels: %s", got)
	}
	if u.PanelsUnavailable {
		t.Error("panels unavailable")
	}

	core := u.Services[0].Windows
	// 10 + 2 + 5 = 17 slots observed, 2 of them down.
	for _, w := range []string{"24h", "7d", "30d"} {
		a := core[w]
		if a.ObservedS != 17*30 || a.DownS != 60 || a.Outages != 1 || a.LongestS != 60 || pct(t, a) != 88.235 {
			t.Errorf("core %s: %+v", w, a)
		}
	}
	if n := len(core["24h"].Bar); n != 48 {
		t.Errorf("24h bar: %d cells", n)
	}
	if n, m := len(core["7d"].Bar), len(core["30d"].Bar); n != 56 || m != 60 {
		t.Errorf("bars: %d %d", n, m)
	}
	// The newest cell holds the last 20 minutes (the 30-minute cell that ends now).
	if last := core["24h"].Bar[47]; last[0] != 17*30 || last[1] != 60 {
		t.Errorf("newest cell: %v", last)
	}
	if bot := u.Services[1].Windows["24h"]; pct(t, bot) != 100 || bot.ObservedS != 600 || bot.Outages != 0 {
		t.Errorf("bot: %+v", bot)
	}
	// A service reported now that has no sample yet: no data.
	if pay := u.Services[2].Windows["30d"]; pay.Pct != nil || pay.ObservedS != 0 || pay.WindowS != 30*86400 {
		t.Errorf("payments: %+v", pay)
	}
	if u.Services[0].Enabled != nil {
		t.Error("a service has no enabled flag")
	}
	// Panels: the one with samples, one that is switched off (never sampled), one that was added just now.
	if p := u.Panels[0]; p.Name != "DE-1" || p.Enabled == nil || !*p.Enabled || pct(t, p.Windows["24h"]) != 100 {
		t.Errorf("panel 1: %+v", p)
	}
	if p := u.Panels[1]; p.Name != "NL-1" || p.Enabled == nil || *p.Enabled || p.Windows["7d"].Pct != nil {
		t.Errorf("panel 2: %+v", p)
	}
	if p := u.Panels[2]; p.Windows["24h"].Pct != nil || p.Windows["24h"].Outages != 0 {
		t.Errorf("panel 3: %+v", p)
	}
	// The payload is JSON with the percentage null for no data.
	raw, err := json.Marshal(u.Panels[2])
	if err != nil || !strings.Contains(string(raw), `"availability_pct":null`) {
		t.Errorf("json: %s %v", raw, err)
	}
}

// A sample older than the window is not in it; the longer windows keep what the shorter ones lost.
func TestUptimeWindowsDifferByAge(t *testing.T) {
	e := newEnv(t)
	e.clk.t = time.Unix(1_800_000_000/30*30, 0).UTC()
	now := e.clk.now()
	// Down three days ago for a minute, up since.
	for i := 0; i < 2; i++ {
		e.record("service.up:core", now.Add(-72*time.Hour).Add(time.Duration(i)*30*time.Second), 0)
	}
	for i := 0; i < 10; i++ {
		e.record("service.up:core", now.Add(-10*time.Minute).Add(time.Duration(i)*30*time.Second), 1)
	}
	u := e.uptime()
	w := u.Services[0].Windows
	if a := w["24h"]; pct(t, a) != 100 || a.Outages != 0 || a.ObservedS != 300 {
		t.Errorf("24h: %+v", a)
	}
	for _, name := range []string{"7d", "30d"} {
		if a := w[name]; a.Outages != 1 || a.DownS != 60 || a.ObservedS != 360 || pct(t, a) != 83.333 {
			t.Errorf("%s: %+v", name, a)
		}
	}
}

func TestUptimeWithoutAnySample(t *testing.T) {
	e := newEnv(t)
	u := e.uptime()
	if u.Since != nil {
		t.Errorf("since %v", *u.Since)
	}
	if len(u.Services) != 4 {
		t.Errorf("the services reported now are listed: %v", ids(u.Services))
	}
	for _, c := range u.Services {
		for name, a := range c.Windows {
			if a.Pct != nil || a.ObservedS != 0 {
				t.Errorf("%s %s: %+v", c.ID, name, a)
			}
		}
	}
	if u.Panels == nil || len(u.Panels) != 0 {
		t.Errorf("panels: %v", u.Panels)
	}
	raw, _ := json.Marshal(u)
	if !strings.Contains(string(raw), `"since":null`) || !strings.Contains(string(raw), `"panels":[]`) {
		t.Errorf("json: %s", raw)
	}
}

// Monitoring being switched off, or core being down, leaves no samples: that stretch is no data, not downtime.
func TestUptimeCoreBeingDownIsNoData(t *testing.T) {
	e := newEnv(t)
	e.clk.t = time.Unix(1_800_000_000/30*30, 0).UTC()
	now := e.clk.now()
	for i := 0; i < 10; i++ {
		e.record("service.up:core", now.Add(-5*time.Hour).Add(time.Duration(i)*30*time.Second), 1)
		e.record("service.up:core", now.Add(-time.Minute).Add(time.Duration(i)*3*time.Second), 1)
	}
	a := e.uptime().Services[0].Windows["24h"]
	if pct(t, a) != 100 || a.Outages != 0 || a.DownS != 0 {
		t.Errorf("%+v", a)
	}
	if a.ObservedS >= 24*3600/100 {
		t.Errorf("hours of silence counted as observed: %d", a.ObservedS)
	}
}

func TestUptimePanelsUnavailable(t *testing.T) {
	e := newEnv(t)
	e.servers.list, e.servers.err = nil, errors.New("provisioner is down")
	u := e.uptime()
	if !u.PanelsUnavailable || len(u.Panels) != 0 {
		t.Errorf("%+v", u.Panels)
	}
	// With no provisioner configured at all there is nothing to be unavailable.
	e.m = New(Config{Store: e.st, Now: e.clk.now})
	if u := e.uptime(); u.PanelsUnavailable {
		t.Error("unavailable without a provisioner")
	}
}

// ---- the public status ----

func serviceReport(statuses map[string]string) *syshealth.Report {
	r := syshealth.Report{Version: "9.9.9"}
	for _, n := range []string{"core", "bot", "payments", "provisioner"} {
		s, ok := statuses[n]
		if !ok {
			s = syshealth.StatusOK
		}
		r.Services = append(r.Services, syshealth.Service{Name: n, Status: s, Version: "9.9.9", LatencyMS: 5, Checks: map[string]string{"db": "ok"}})
	}
	return &r
}

func (e *env) publicStatus(statuses map[string]string, servers ...domain.Server) *PublicStatus {
	e.t.Helper()
	e.health.set(*serviceReport(statuses))
	e.servers.list = &domain.ServerList{Servers: servers}
	e.clk.add(time.Minute) // past the shared look
	return e.m.PublicStatus(context.Background())
}

func statusOf(st *PublicStatus) string {
	var parts []string
	for _, c := range st.Components {
		parts = append(parts, c.ID+"="+c.Status)
	}
	return st.Status + ": " + strings.Join(parts, " ")
}

func TestPublicStatusAllWorking(t *testing.T) {
	e := newEnv(t)
	st := e.publicStatus(nil, domain.Server{ID: "s1", Name: "DE-1", Enabled: true, Healthy: true, CheckedAt: 1})
	if got := statusOf(st); got != "ok: dashboard=ok bot=ok payments=ok panels=ok" {
		t.Errorf("%s", got)
	}
	if st.UpdatedAt != e.clk.now().Unix() {
		t.Errorf("updated at %d", st.UpdatedAt)
	}
	names := []string{}
	for _, c := range st.Components {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "Dashboard,Bot,Payments,Panels" {
		t.Errorf("names: %v", names)
	}
}

func TestPublicStatusSomethingDown(t *testing.T) {
	e := newEnv(t)
	healthy := domain.Server{ID: "s1", Name: "DE-1", Enabled: true, Healthy: true, CheckedAt: 1}
	sick := domain.Server{ID: "s2", Name: "NL-1", Enabled: true, Healthy: false, CheckedAt: 1}
	off := domain.Server{ID: "s3", Name: "off", Enabled: false, Healthy: false, CheckedAt: 1}
	unchecked := domain.Server{ID: "s4", Name: "new", Enabled: true}
	for name, tc := range map[string]struct {
		statuses map[string]string
		servers  []domain.Server
		want     string
	}{
		"bot down":                {map[string]string{"bot": syshealth.StatusDown}, []domain.Server{healthy}, "degraded: dashboard=ok bot=down payments=ok panels=ok"},
		"payments not ready":      {map[string]string{"payments": syshealth.StatusNotReady}, []domain.Server{healthy}, "degraded: dashboard=ok bot=ok payments=degraded panels=ok"},
		"the dashboard itself":    {map[string]string{"core": syshealth.StatusNotReady}, []domain.Server{healthy}, "degraded: dashboard=degraded bot=ok payments=ok panels=ok"},
		"one of two panels down":  {nil, []domain.Server{healthy, sick}, "degraded: dashboard=ok bot=ok payments=ok panels=degraded"},
		"all panels down":         {nil, []domain.Server{sick}, "degraded: dashboard=ok bot=ok payments=ok panels=down"},
		"a switched off panel":    {nil, []domain.Server{healthy, off}, "ok: dashboard=ok bot=ok payments=ok panels=ok"},
		"no panel checked yet":    {nil, []domain.Server{unchecked}, "ok: dashboard=ok bot=ok payments=ok"},
		"no panels":               {nil, nil, "ok: dashboard=ok bot=ok payments=ok"},
		"the provisioner is down": {map[string]string{"provisioner": syshealth.StatusDown}, []domain.Server{healthy}, "degraded: dashboard=ok bot=ok payments=ok panels=down"},
		"the provisioner is slow": {map[string]string{"provisioner": syshealth.StatusNotReady}, []domain.Server{healthy}, "degraded: dashboard=ok bot=ok payments=ok panels=degraded"},
		"the provisioner alone":   {map[string]string{"provisioner": syshealth.StatusDown}, nil, "degraded: dashboard=ok bot=ok payments=ok panels=down"},
	} {
		if got := statusOf(e.publicStatus(tc.statuses, tc.servers...)); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// Without the other services to ask, the dashboard (this process) answers for itself and nothing else is said.
func TestPublicStatusWithoutSources(t *testing.T) {
	e := newEnv(t)
	e.m = New(Config{Now: e.clk.now})
	if got := statusOf(e.m.PublicStatus(context.Background())); got != "ok: dashboard=ok" {
		t.Errorf("%s", got)
	}
}

// The answer holds a status and generic names, nothing of how the store is built.
func TestPublicStatusRevealsNothing(t *testing.T) {
	e := newEnv(t)
	st := e.publicStatus(map[string]string{"bot": syshealth.StatusDown},
		domain.Server{ID: "secret-id-1", Name: "secret-panel-name", Enabled: true, Healthy: false, CheckedAt: 1, BaseURL: "https://panel.secret.example:2053"})
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"9.9.9", "secret", "provisioner", "example", "db", "latency", "version"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("%q in %s", leak, raw)
		}
	}
	var generic struct {
		Status     string `json:"status"`
		UpdatedAt  int64  `json:"updated_at"`
		Components []struct {
			ID, Name, Status string
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil || len(generic.Components) != 4 || generic.Status != PublicDegraded {
		t.Errorf("%s %v", raw, err)
	}
}
