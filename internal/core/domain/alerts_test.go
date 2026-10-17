package domain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	eventsv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/events/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/grpcauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/testdb"
)

// fakeHealth answers the services' health as set.
type fakeHealth struct{ services []syshealth.Service }

func (f *fakeHealth) Check(context.Context) syshealth.Report {
	return syshealth.Report{Services: f.services}
}

func (f *fakeHealth) set(name, status, detail string) {
	for i := range f.services {
		if f.services[i].Name == name {
			f.services[i].Status, f.services[i].Detail = status, detail
			return
		}
	}
	f.services = append(f.services, syshealth.Service{Name: name, Status: status, Detail: detail})
}

// fakeLister is the provisioner's server list.
type fakeLister struct {
	servers []Server
	err     error
}

func (f *fakeLister) ListServers(context.Context) (*ServerList, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &ServerList{Servers: f.servers}, nil
}

// fakeReporter is payments' gateway settings and the reports of Zarinpal
// and automatic USDT.
type fakeReporter struct {
	enabled     bool
	report      GatewayReport
	usdtEnabled bool
	usdtReport  GatewayReport
	configErr   error
	reportErr   error
	reportAsks  int
	usdtAsks    int
}

func (f *fakeReporter) GatewayConfigs(context.Context) ([]GatewayConfig, error) {
	if f.configErr != nil {
		return nil, f.configErr
	}
	return []GatewayConfig{{Provider: Stars, Enabled: true}, {Provider: Zarinpal, Enabled: f.enabled},
		{Provider: USDTAuto, Enabled: f.usdtEnabled}}, nil
}

func (f *fakeReporter) GatewayReport(_ context.Context, provider string, _, _ int64) (*GatewayReport, error) {
	var r GatewayReport
	switch provider {
	case Zarinpal:
		f.reportAsks++
		r = f.report
	case USDTAuto:
		f.usdtAsks++
		r = f.usdtReport
		r.Provider = USDTAuto
	default:
		return nil, fmt.Errorf("asked for %s", provider)
	}
	if f.reportErr != nil {
		return nil, f.reportErr
	}
	return &r, nil
}

// fakeBackups is the backup service's status answer.
type fakeBackups struct {
	raw string
	err error
}

func (f *fakeBackups) Status(context.Context) (json.RawMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	return json.RawMessage(f.raw), nil
}

var alertStart = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

// alertWorld is a watchdog on a fresh core schema with a clock the test
// moves.
type alertWorld struct {
	t   *testing.T
	svc *Service
	st  *store.Store
	w   *Watchdog
	now time.Time
}

func newAlertWorld(t *testing.T, src WatchdogSources) *alertWorld {
	t.Helper()
	svc, s := testService(t)
	if err := testdb.Truncate(context.Background(), s.DB(), `core.alert_state, core.license`); err != nil {
		t.Fatal(err)
	}
	a := &alertWorld{t: t, svc: svc, st: s, now: alertStart}
	a.w = svc.NewWatchdog(src, nil)
	a.w.now = func() time.Time { return a.now }
	return a
}

// step runs the watchdog once at the current time, then lets the bot read
// every event (so the feed check stays quiet unless a test wants it).
func (a *alertWorld) step() {
	a.t.Helper()
	if err := a.w.Step(context.Background()); err != nil {
		a.t.Fatalf("step at %s: %v", a.now.Format(time.Kitchen), err)
	}
	ctx := botCaller()
	feed := coreFeed(a.st)
	for {
		got, err := feed.PullEvents(ctx, &eventsv1.PullEventsRequest{Limit: eventbus.MaxBatch})
		if err != nil {
			a.t.Fatal(err)
		}
		if len(got.GetEvents()) == 0 {
			return
		}
		if _, err := feed.AckEvents(ctx, &eventsv1.AckEventsRequest{UpToSeq: got.GetEvents()[len(got.GetEvents())-1].GetSeq()}); err != nil {
			a.t.Fatal(err)
		}
	}
}

// coreFeed is core's event feed as cmd/core serves it over gRPC.
func coreFeed(st *store.Store) *eventbus.FeedServer {
	return eventbus.NewFeedServer(eventbus.NewFeed(st.DB(), "outbox_core"))
}

// botCaller is a call from the bot as core's gRPC server knows it: the peer
// cmd/core names "bot" (not the bot's own name for its consumer).
func botCaller() context.Context { return grpcauth.WithCaller(context.Background(), "bot") }

func (a *alertWorld) after(d time.Duration) { a.now = a.now.Add(d) }

// sent decodes every system alert published so far.
func (a *alertWorld) sent() []events.SystemAlertEvent {
	a.t.Helper()
	var out []events.SystemAlertEvent
	for _, raw := range outboxEvents(a.t, a.st, events.SystemAlert) {
		var e events.SystemAlertEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			a.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

// state is a check's stored row (nil: none).
func (a *alertWorld) state(key string) *store.AlertState {
	a.t.Helper()
	tx, err := a.st.DB().Begin(context.Background())
	if err != nil {
		a.t.Fatal(err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck // read only
	states, err := a.st.LockAlertStates(context.Background(), tx)
	if err != nil {
		a.t.Fatal(err)
	}
	return states[key]
}

func (a *alertWorld) expectSent(n int) []events.SystemAlertEvent {
	a.t.Helper()
	got := a.sent()
	if len(got) != n {
		a.t.Fatalf("%d alerts sent, want %d: %+v", len(got), n, got)
	}
	return got
}

// probeOf runs one probe at the world's time.
func probeOf(a *alertWorld, fn func(context.Context, time.Time) alertProbe) alertProbe {
	return fn(context.Background(), a.now)
}

// checkOf finds a probe's check by key.
func checkOf(t *testing.T, p alertProbe, key string) alertCheck {
	t.Helper()
	for _, c := range p.checks {
		if c.key == key {
			return c
		}
	}
	t.Fatalf("no check %s in %+v", key, p.checks)
	return alertCheck{}
}

// A failure seen once is pending; seen again it is announced once, as open
// since it was first seen; it is not repeated every minute; its fix is
// announced once it has passed for 5 minutes; a blip that passes before the
// next run is never announced.
func TestWatchdogAnnouncesChangesOnce(t *testing.T) {
	health := &fakeHealth{}
	health.set("core", syshealth.StatusOK, "")
	health.set("bot", syshealth.StatusOK, "")
	health.set("backup", syshealth.StatusNotInstalled, "")
	a := newAlertWorld(t, WatchdogSources{Health: health})

	a.step()
	a.expectSent(0)
	if a.state("service.bot") != nil || a.state("service.backup") != nil {
		t.Fatal("a row was written for a check that is fine")
	}

	health.set("bot", syshealth.StatusDown, "does not answer in time")
	a.after(time.Minute)
	first := a.now
	a.step()
	a.expectSent(0)
	if st := a.state("service.bot"); st == nil || st.State != store.AlertPending || !st.Since.Equal(first) {
		t.Fatalf("seen once: %+v", st)
	}

	a.after(time.Minute)
	a.step()
	got := a.expectSent(1)[0]
	if got.Key != "service.bot" || got.State != events.AlertProblem || got.Severity != events.SeverityCritical ||
		got.TitleKey != "alert.service_down" || got.Args["service"] != "bot" || got.Detail != "does not answer in time" ||
		got.Since != first.Unix() || got.At != a.now.Unix() || got.Repeat {
		t.Fatalf("problem: %+v", got)
	}
	if st := a.state("service.bot"); st.State != store.AlertProblem || st.LastSentAt == nil || !st.LastSentAt.Equal(a.now) {
		t.Fatalf("announced: %+v", st)
	}

	// Still down, also after a restart (a new watchdog on the same store):
	// nothing new is sent; a new detail is kept for the dashboard.
	for range 3 {
		a.after(time.Minute)
		a.step()
	}
	w2 := a.svc.NewWatchdog(WatchdogSources{Health: health}, nil)
	w2.now = a.w.now
	a.w = w2
	health.set("bot", syshealth.StatusDown, "answers HTTP 502")
	a.after(time.Minute)
	a.step()
	a.expectSent(1)
	if st := a.state("service.bot"); st.Detail != "answers HTTP 502" || !st.Since.Equal(first) {
		t.Fatalf("detail not kept: %+v", st)
	}

	// Passing: still open (and on the System page) for 5 minutes, then fixed,
	// announced once, naming what was wrong and how long it lasted (until it
	// first passed).
	health.set("bot", syshealth.StatusOK, "")
	a.after(time.Minute)
	passing := a.now
	for range 5 {
		a.step()
		a.expectSent(1)
		if st := a.state("service.bot"); st.State != store.AlertProblem || st.PassingSince == nil || !st.PassingSince.Equal(passing) ||
			st.Detail != "answers HTTP 502" {
			t.Fatalf("passing for %s: %+v", a.now.Sub(passing), st)
		}
		a.after(time.Minute)
	}
	a.step()
	got = a.expectSent(2)[1]
	if got.Key != "service.bot" || got.State != events.AlertResolved || got.TitleKey != "alert.service_down" ||
		got.Args["service"] != "bot" || got.Detail != "" || got.Since != first.Unix() || got.At != passing.Unix() {
		t.Fatalf("resolved: %+v", got)
	}
	if st := a.state("service.bot"); st.State != store.AlertOK || st.PassingSince != nil {
		t.Fatalf("after the fix: %+v", st)
	}

	// A blip: down for one run only, never announced.
	health.set("bot", syshealth.StatusNotReady, "failing: redis")
	a.after(time.Minute)
	a.step()
	health.set("bot", syshealth.StatusOK, "")
	a.after(time.Minute)
	a.step()
	a.expectSent(2)
	if st := a.state("service.bot"); st.State != store.AlertOK {
		t.Fatalf("after a blip: %+v", st)
	}

	// Not ready counts too, with its own words (once 30 minutes have passed
	// since the fix).
	health.set("bot", syshealth.StatusNotReady, "failing: redis")
	a.after(alertQuietAfter)
	a.step()
	a.after(time.Minute)
	a.step()
	if got := a.expectSent(3)[2]; got.TitleKey != "alert.service_not_ready" || got.Detail != "failing: redis" {
		t.Fatalf("not ready: %+v", got)
	}
}

// sentFor is the alerts sent so far about one check.
func (a *alertWorld) sentFor(key string) []events.SystemAlertEvent {
	a.t.Helper()
	var out []events.SystemAlertEvent
	for _, e := range a.sent() {
		if e.Key == key {
			out = append(out, e)
		}
	}
	return out
}

// A check that flaps does not send a pair of messages every few minutes: a
// problem that passes for less than 5 minutes stays open, and one back
// within 30 minutes of its fix is held until those 30 minutes are over.
func TestWatchdogDoesNotFlap(t *testing.T) {
	servers := &fakeLister{servers: []Server{{ID: "s1", Name: "DE", Enabled: true, CheckedAt: 1, HealthError: "i/o timeout"}}}
	a := newAlertWorld(t, WatchdogSources{Servers: servers})
	up := func(ok bool) { servers.servers[0].Healthy = ok }

	// A filtered link: the provisioner's checks fail, fail, pass... for a
	// day. Announced once, reminded at 6 hours, never "fixed".
	since := a.now
	for i := range 24*60 + 1 {
		up(i%3 == 2)
		a.step()
		if st := a.state("server.s1"); i > 1 && (st.State != store.AlertProblem || (st.PassingSince != nil) != (i%3 == 2)) {
			t.Fatalf("minute %d: %+v", i, st)
		}
		a.after(time.Minute)
	}
	got := a.expectSent(2)
	if got[0].State != events.AlertProblem || got[0].Repeat || got[0].Since != since.Unix() || !got[1].Repeat {
		t.Fatalf("a day of flapping: %+v", got)
	}

	// Up for good: fixed after 5 minutes.
	up(true)
	for range 6 {
		a.step()
		a.after(time.Minute)
	}
	fixedAt := a.now.Add(-time.Minute)
	if got := a.expectSent(3)[2]; got.State != events.AlertResolved || got.At != fixedAt.Add(-alertFixAfter).Unix() {
		t.Fatalf("fixed: %+v", got)
	}

	// Down again 10 minutes later, for 15 minutes, then up: held, never
	// announced (and not on the System page).
	a.now = fixedAt.Add(10 * time.Minute)
	up(false)
	held := a.now
	for range 15 {
		a.step()
		a.after(time.Minute)
	}
	if st := a.state("server.s1"); st.State != store.AlertPending || !st.Since.Equal(held) {
		t.Fatalf("held: %+v", st)
	}
	if list, err := a.svc.OpenAlerts(context.Background()); err != nil || len(list) != 0 {
		t.Fatalf("a held problem is listed: %v %v", list, err)
	}
	up(true)
	a.step()
	a.expectSent(3)

	// Down again 26 minutes after the fix and still down when the 30 minutes
	// are over: announced then, as open since first seen.
	a.now = fixedAt.Add(26 * time.Minute)
	up(false)
	held = a.now
	for a.now.Before(fixedAt.Add(alertQuietAfter)) {
		a.step()
		a.expectSent(3)
		a.after(time.Minute)
	}
	a.step()
	if got := a.expectSent(4)[3]; got.State != events.AlertProblem || got.Repeat || got.Since != held.Unix() || got.At != a.now.Unix() {
		t.Fatalf("back after the quiet time: %+v", got)
	}
}

// A problem still open after 6 hours is sent again then, and every 24 hours
// after that; never in between.
func TestWatchdogRepeatsLongProblems(t *testing.T) {
	health := &fakeHealth{}
	health.set("payments", syshealth.StatusDown, "does not answer")
	a := newAlertWorld(t, WatchdogSources{Health: health})
	since := a.now
	a.step()
	a.after(time.Minute)
	a.step()
	a.expectSent(1)

	var repeats []time.Duration
	for range 6 * 60 { // 60 hours, a run every 10 minutes
		a.after(10 * time.Minute)
		before := len(a.sent())
		a.step()
		if got := a.sent(); len(got) > before {
			e := got[len(got)-1]
			if !e.Repeat || e.State != events.AlertProblem || e.Since != since.Unix() || e.At != a.now.Unix() {
				t.Fatalf("repeat: %+v", e)
			}
			repeats = append(repeats, a.now.Sub(since))
		}
	}
	want := []time.Duration{6*time.Hour + time.Minute, 30*time.Hour + time.Minute, 54*time.Hour + time.Minute}
	if fmt.Sprint(repeats) != fmt.Sprint(want) {
		t.Fatalf("repeated after %v, want %v", repeats, want)
	}
}

func TestRepeatDue(t *testing.T) {
	since := alertStart
	at := func(d time.Duration) *time.Time { v := since.Add(d); return &v }
	for _, c := range []struct {
		name     string
		lastSent *time.Time
		now      time.Duration
		want     bool
	}{
		{"just announced", at(time.Minute), 5 * time.Hour, false},
		{"six hours", at(time.Minute), 6 * time.Hour, true},
		{"never sent (written by an older core)", nil, 7 * time.Hour, true},
		{"reminded at six hours", at(6 * time.Hour), 29 * time.Hour, false},
		{"a day after the reminder", at(6 * time.Hour), 30 * time.Hour, true},
		{"a day after a late reminder", at(8 * time.Hour), 31 * time.Hour, false},
	} {
		if got := repeatDue(since, c.lastSent, since.Add(c.now), false); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	// A daily notice has no 6-hour reminder: only a day after it was sent.
	for _, c := range []struct {
		name     string
		lastSent *time.Time
		now      time.Duration
		want     bool
	}{
		{"six hours", at(time.Minute), 6 * time.Hour, false},
		{"23 hours later", at(time.Minute), 24 * time.Hour, false},
		{"a day later", at(time.Minute), 24*time.Hour + time.Minute, true},
		{"never sent", nil, 48 * time.Hour, false},
	} {
		if got := repeatDue(since, c.lastSent, since.Add(c.now), true); got != c.want {
			t.Errorf("daily %s: %v", c.name, got)
		}
	}
}

// A check its source no longer makes is forgotten quietly; one whose source
// did not answer keeps its state.
func TestWatchdogForgetsChecksThatAreGone(t *testing.T) {
	servers := &fakeLister{servers: []Server{
		{ID: "s1", Name: "DE <1>", Enabled: true, Healthy: false, CheckedAt: 1, HealthError: "dial tcp: connection refused"},
		{ID: "s2", Name: "NL", Enabled: true, Healthy: true, CheckedAt: 1},
	}}
	a := newAlertWorld(t, WatchdogSources{Servers: servers})
	a.step()
	a.after(time.Minute)
	a.step()
	got := a.expectSent(1)[0]
	if got.Key != "server.s1" || got.TitleKey != "alert.server_down" || got.Args["server"] != "DE <1>" ||
		got.Detail != "dial tcp: connection refused" {
		t.Fatalf("server down: %+v", got)
	}

	// The provisioner does not answer: nothing is known, nothing changes.
	servers.err = errors.New("unavailable")
	a.after(time.Minute)
	a.step()
	if st := a.state("server.s1"); st == nil || st.State != store.AlertProblem {
		t.Fatalf("kept while the provisioner is away: %+v", st)
	}

	// Staff turned the server off: forgotten, no "fixed" message.
	servers.err = nil
	servers.servers[0].Enabled = false
	a.after(time.Minute)
	a.step()
	a.expectSent(1)
	if st := a.state("server.s1"); st != nil {
		t.Fatalf("a server turned off is still watched: %+v", st)
	}
}

// A check whose problem changes (the license from expiring to its grace
// period) announces the new problem, not a fix.
func TestWatchdogAnnouncesAChangedProblem(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	v := licensed(t, a.svc, a.now)
	a.svc.cfg.License.Now = func() time.Time { return a.now }
	expires := a.now.Add(10 * 24 * time.Hour)
	install(t, a.st, v.token(func(tok *license.Token) { tok.ExpiresAt = expires }))

	a.step()
	a.after(time.Minute)
	a.step()
	got := a.expectSent(1)[0]
	if got.Key != "license.expiry" || got.TitleKey != "alert.license_expiring" || got.Severity != events.SeverityWarning ||
		got.Args["days"] != "10" || got.Args["date"] != fmt.Sprint(expires.Unix()) {
		t.Fatalf("expiring: %+v", got)
	}

	// The days left go down; the daily repeat says how many are left.
	a.after(24 * time.Hour)
	a.step()
	if got = a.expectSent(2)[1]; !got.Repeat || got.Args["days"] != "9" || got.TitleKey != "alert.license_expiring" {
		t.Fatalf("a day later: %+v", got)
	}
	sentBefore := len(a.sent())

	a.now = expires.Add(time.Hour)
	a.step()
	got = a.sent()[len(a.sent())-1]
	if len(a.sent()) != sentBefore+1 || got.State != events.AlertProblem || got.TitleKey != "alert.license_grace" ||
		got.Args["date"] != fmt.Sprint(expires.Add(7*24*time.Hour).Unix()) || got.Since != a.now.Unix() || got.Repeat {
		t.Fatalf("grace: %+v", got)
	}
	a.now = expires.Add(8 * 24 * time.Hour)
	a.step()
	if got = a.sent()[len(a.sent())-1]; got.TitleKey != "alert.license_expired" || got.State != events.AlertProblem {
		t.Fatalf("expired: %+v", got)
	}

	// Renewed: fixed, 5 minutes later.
	install(t, a.st, v.token(func(tok *license.Token) {
		tok.IssuedAt, tok.ExpiresAt = a.now.Add(-time.Hour), a.now.Add(365*24*time.Hour)
	}))
	a.after(time.Minute)
	a.step()
	a.after(alertFixAfter)
	a.step()
	if got = a.sent()[len(a.sent())-1]; got.State != events.AlertResolved || got.TitleKey != "alert.license_expired" {
		t.Fatalf("renewed: %+v", got)
	}

	// Removed: a missing license is not watched.
	if _, err := a.st.DB().Exec(context.Background(), `DELETE FROM core.license`); err != nil {
		t.Fatal(err)
	}
	a.after(time.Minute)
	a.step()
	if st := a.state("license.expiry"); st != nil {
		t.Fatalf("no license, still watched: %+v", st)
	}
}

func TestServiceChecks(t *testing.T) {
	health := &fakeHealth{services: []syshealth.Service{
		{Name: "core", Status: syshealth.StatusOK},
		{Name: "bot", Status: syshealth.StatusDown, Detail: "unknown host"},
		{Name: "payments", Status: syshealth.StatusNotReady, Detail: "failing: db"},
		{Name: "provisioner", Status: syshealth.StatusNotConfigured},
		{Name: "backup", Status: syshealth.StatusNotInstalled},
	}}
	a := newAlertWorld(t, WatchdogSources{Health: health})
	p := probeOf(a, a.w.services)
	if !p.covered || len(p.checks) != 3 {
		t.Fatalf("checks: %+v", p)
	}
	if c := checkOf(t, p, "service.core"); c.problem {
		t.Fatalf("core: %+v", c)
	}
	if c := checkOf(t, p, "service.bot"); !c.problem || c.title != "alert.service_down" || c.detail != "unknown host" || c.severity != events.SeverityCritical {
		t.Fatalf("bot: %+v", c)
	}
	if c := checkOf(t, p, "service.payments"); !c.problem || c.title != "alert.service_not_ready" || c.args["service"] != "payments" {
		t.Fatalf("payments: %+v", c)
	}

	// Core was given the backup service's address: a name Docker does not
	// resolve is a stopped or restarting container, so it is down.
	a.w.src.Backups = &fakeBackups{err: errors.New("lookup backup: no such host")}
	p = probeOf(a, a.w.services)
	if c := checkOf(t, p, "service.backup"); !c.problem || c.title != "alert.service_down" || c.args["service"] != "backup" ||
		c.detail == "" || c.severity != events.SeverityCritical {
		t.Fatalf("backup stopped: %+v", c)
	}
	if len(p.checks) != 4 {
		t.Fatalf("checks: %+v", p)
	}
}

func TestServerChecks(t *testing.T) {
	servers := &fakeLister{servers: []Server{
		{ID: "a", Name: "up", Enabled: true, Healthy: true, CheckedAt: 1},
		{ID: "b", Name: "down", Enabled: true, Healthy: false, CheckedAt: 1, HealthError: "token refused"},
		{ID: "c", Name: "off", Enabled: false, Healthy: false, CheckedAt: 1},
		{ID: "d", Name: "new", Enabled: true, CheckedAt: 0},
	}}
	a := newAlertWorld(t, WatchdogSources{Servers: servers})
	p := probeOf(a, a.w.servers)
	if !p.covered || len(p.checks) != 2 || checkOf(t, p, "server.a").problem {
		t.Fatalf("checks: %+v", p)
	}
	if c := checkOf(t, p, "server.b"); !c.problem || c.args["server"] != "down" || c.detail != "token refused" {
		t.Fatalf("down: %+v", c)
	}
	servers.err = errors.New("no provisioner")
	if p := probeOf(a, a.w.servers); p.covered || len(p.checks) != 0 {
		t.Fatalf("provisioner away: %+v", p)
	}
}

func TestZarinpalChecks(t *testing.T) {
	gw := &fakeReporter{}
	a := newAlertWorld(t, WatchdogSources{Gateways: gw})
	now := a.now.Unix()

	// Switched off: covered, nothing asked.
	if p := probeOf(a, a.w.zarinpal); !p.covered || len(p.checks) != 0 || gw.reportAsks != 0 {
		t.Fatalf("off: %+v (asked %d)", p, gw.reportAsks)
	}

	gw.enabled = true
	p := probeOf(a, a.w.zarinpal)
	if !p.covered || checkOf(t, p, "gateway.zarinpal.error").problem || checkOf(t, p, "gateway.zarinpal.open").problem {
		t.Fatalf("quiet: %+v", p)
	}

	// An error after the last paid one, within the hour: failing.
	gw.report = GatewayReport{LastPaid: &GatewayEvent{At: now - 600}, LastError: &GatewayEvent{At: now - 60, Detail: "verify: -51"}}
	if c := checkOf(t, probeOf(a, a.w.zarinpal), "gateway.zarinpal.error"); !c.problem || c.detail != "verify: -51" || c.title != "alert.zarinpal_failing" {
		t.Fatalf("failing: %+v", c)
	}
	// Paid since: fine again.
	gw.report.LastPaid.At = now - 10
	if c := checkOf(t, probeOf(a, a.w.zarinpal), "gateway.zarinpal.error"); c.problem {
		t.Fatalf("paid since the error: %+v", c)
	}

	// A checkout the customer left stays open by design: its hour to pay,
	// 45 minutes of grace, then until payments' reconciler (backing off to
	// 32 minutes) expires it. Only one open for over 3 hours is stuck.
	gw.report = GatewayReport{OpenCount: 3}
	for _, open := range []time.Duration{61 * time.Minute, time.Hour + 45*time.Minute + 32*time.Minute, 179 * time.Minute} {
		gw.report.OldestOpenAt = now - int64(open/time.Second)
		if c := checkOf(t, probeOf(a, a.w.zarinpal), "gateway.zarinpal.open"); c.problem {
			t.Fatalf("open for %s: %+v", open, c)
		}
	}
	gw.report.OldestOpenAt = now - 181*60
	if c := checkOf(t, probeOf(a, a.w.zarinpal), "gateway.zarinpal.open"); !c.problem || c.args["count"] != "3" || c.title != "alert.zarinpal_stuck" {
		t.Fatalf("stuck: %+v", c)
	}

	// Payments not answering: nothing known.
	gw.reportErr = errors.New("unavailable")
	if p := probeOf(a, a.w.zarinpal); p.covered {
		t.Fatalf("report failed: %+v", p)
	}
	gw.reportErr, gw.configErr = nil, errors.New("unavailable")
	if p := probeOf(a, a.w.zarinpal); p.covered {
		t.Fatalf("configs failed: %+v", p)
	}
}

// keyedReporter answers for IDPay and NextPay only: the one named in on is
// switched on, with the report given.
type keyedReporter struct {
	on     string
	report GatewayReport
}

func (k *keyedReporter) GatewayConfigs(context.Context) ([]GatewayConfig, error) {
	return []GatewayConfig{{Provider: IDPay, Enabled: k.on == IDPay}, {Provider: NextPay, Enabled: k.on == NextPay}}, nil
}

func (k *keyedReporter) GatewayReport(_ context.Context, provider string, _, _ int64) (*GatewayReport, error) {
	if provider != k.on {
		return nil, fmt.Errorf("asked for %s", provider)
	}
	r := k.report
	return &r, nil
}

// IDPay and NextPay are watched like Zarinpal, under their own names.
func TestKeyedGatewayChecks(t *testing.T) {
	for _, provider := range []string{IDPay, NextPay} {
		k := &keyedReporter{}
		a := newAlertWorld(t, WatchdogSources{Gateways: k})
		probe := a.w.idpay
		if provider == NextPay {
			probe = a.w.nextpay
		}
		now := a.now.Unix()
		// Off: covered, and the report is not asked for.
		if p := probeOf(a, probe); !p.covered || len(p.checks) != 0 {
			t.Fatalf("%s off: %+v", provider, p)
		}
		k.on = provider
		k.report = GatewayReport{LastError: &GatewayEvent{At: now - 60, Detail: "verify: -49"}, OpenCount: 2, OldestOpenAt: now - 181*60}
		p := probeOf(a, probe)
		if f := checkOf(t, p, "gateway."+provider+".error"); !f.problem || f.title != "alert."+provider+"_failing" || f.detail != "verify: -49" {
			t.Fatalf("%s failing: %+v", provider, f)
		}
		if c := checkOf(t, p, "gateway."+provider+".open"); !c.problem || c.title != "alert."+provider+"_stuck" || c.args["count"] != "2" {
			t.Fatalf("%s stuck: %+v", provider, c)
		}
		k.report = GatewayReport{}
		p = probeOf(a, probe)
		if checkOf(t, p, "gateway."+provider+".error").problem || checkOf(t, p, "gateway."+provider+".open").problem {
			t.Fatalf("%s quiet: %+v", provider, p)
		}
	}
}

// Automatic USDT: while it is switched on, the watcher must read the
// store's address (a failing read is forgiven for 5 minutes), and no
// automatic payment may stay open for over 2 hours.
func TestUSDTChecks(t *testing.T) {
	gw := &fakeReporter{}
	a := newAlertWorld(t, WatchdogSources{Gateways: gw})
	now := a.now.Unix()

	// Switched off: covered, nothing asked.
	if p := probeOf(a, a.w.usdt); !p.covered || len(p.checks) != 0 || gw.usdtAsks != 0 {
		t.Fatalf("off: %+v (asked %d)", p, gw.usdtAsks)
	}

	gw.usdtEnabled = true
	gw.usdtReport = GatewayReport{LastScanAt: now - 20, LastScanOKAt: now - 20}
	p := probeOf(a, a.w.usdt)
	if !p.covered || checkOf(t, p, "gateway.usdt_auto.read").problem || checkOf(t, p, "gateway.usdt_auto.open").problem {
		t.Fatalf("quiet: %+v", p)
	}

	// TronGrid refuses the key: a blip within 5 minutes of a good read is
	// fine, then it is a problem, with the read's error.
	gw.usdtReport = GatewayReport{LastScanAt: now - 10, LastScanOKAt: now - 4*60, ScanError: "trongrid: HTTP 401"}
	if c := checkOf(t, probeOf(a, a.w.usdt), "gateway.usdt_auto.read"); c.problem {
		t.Fatalf("a failed read within the grace: %+v", c)
	}
	gw.usdtReport.LastScanOKAt = now - 6*60
	c := checkOf(t, probeOf(a, a.w.usdt), "gateway.usdt_auto.read")
	if !c.problem || c.detail != "trongrid: HTTP 401" || c.title != "alert.usdt_failing" || c.severity != events.SeverityCritical {
		t.Fatalf("failing reads: %+v", c)
	}
	gw.usdtReport.LastScanOKAt = 0 // it never read the address
	if c := checkOf(t, probeOf(a, a.w.usdt), "gateway.usdt_auto.read"); !c.problem {
		t.Fatalf("never read: %+v", c)
	}

	// An open payment: its 45 minutes, then until a complete read expires it.
	gw.usdtReport = GatewayReport{LastScanOKAt: now, OpenCount: 2, OldestOpenAt: now - 119*60}
	if c := checkOf(t, probeOf(a, a.w.usdt), "gateway.usdt_auto.open"); c.problem {
		t.Fatalf("open for 119 minutes: %+v", c)
	}
	gw.usdtReport.OldestOpenAt = now - 121*60
	if c := checkOf(t, probeOf(a, a.w.usdt), "gateway.usdt_auto.open"); !c.problem || c.args["count"] != "2" || c.title != "alert.usdt_stuck" {
		t.Fatalf("stuck: %+v", c)
	}

	// Payments not answering: nothing known.
	gw.reportErr = errors.New("unavailable")
	if p := probeOf(a, a.w.usdt); p.covered {
		t.Fatalf("report failed: %+v", p)
	}
	gw.reportErr, gw.configErr = nil, errors.New("unavailable")
	if p := probeOf(a, a.w.usdt); p.covered {
		t.Fatalf("configs failed: %+v", p)
	}

	// Through the watchdog: announced, and forgotten once switched off.
	gw.configErr, gw.usdtReport = nil, GatewayReport{LastScanAt: now, ScanError: "trongrid: HTTP 401"}
	a.step()
	a.after(time.Minute)
	a.step()
	if got := a.expectSent(1); got[0].Key != "gateway.usdt_auto.read" || got[0].Severity != events.SeverityCritical {
		t.Fatalf("announced: %+v", got)
	}
	gw.usdtEnabled = false
	a.after(time.Minute)
	a.step()
	if st := a.state("gateway.usdt_auto.read"); st != nil {
		t.Fatalf("kept after switching off: %+v", st)
	}
}

// backupStatus is the backup service's answer at now: a failed run, then a
// good backup made at lastGood.
func backupStatus(now, lastGood time.Time, lastErr string, free uint64) string {
	return fmt.Sprintf(`{"backups":[{"file":"","created_at":%q,"error":"x"},{"file":"b.bkp","created_at":%q}],`+
		`"running":false,"last_error":%q,"next_run":0,"disk_free":%d,"dir":"/backups"}`,
		now.Format(time.RFC3339), lastGood.Format(time.RFC3339), lastErr, free)
}

func TestBackupChecks(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	// Not installed: covered, nothing to check.
	if p := probeOf(a, a.w.backups); !p.covered || len(p.checks) != 0 {
		t.Fatalf("not installed: %+v", p)
	}

	status := func(lastGood time.Time, lastErr string, free uint64) string {
		return backupStatus(a.now, lastGood, lastErr, free)
	}
	bk := &fakeBackups{raw: status(a.now.Add(-25*time.Hour), "", 10<<30)}
	a.w.src.Backups = bk
	p := probeOf(a, a.w.backups)
	if !p.covered || len(p.checks) != 3 {
		t.Fatalf("checks: %+v", p)
	}
	for _, key := range []string{"backup.failed", "backup.stale", "backup.disk"} {
		if checkOf(t, p, key).problem {
			t.Fatalf("%s on a good day: %+v", key, p)
		}
	}

	bk.raw = status(a.now.Add(-27*time.Hour), "pg_dump: connection refused", 1<<30)
	p = probeOf(a, a.w.backups)
	if c := checkOf(t, p, "backup.failed"); !c.problem || c.detail != "pg_dump: connection refused" || c.severity != events.SeverityWarning {
		t.Fatalf("failed: %+v", c)
	}
	if c := checkOf(t, p, "backup.stale"); !c.problem || c.args["hours"] != "27" {
		t.Fatalf("stale: %+v", c)
	}
	if c := checkOf(t, p, "backup.disk"); !c.problem || c.args["free"] != fmt.Sprint(1<<30) {
		t.Fatalf("disk: %+v", c)
	}

	// No good backup yet, free space unknown: neither is checked.
	bk.raw = `{"backups":[],"running":false,"last_error":"","next_run":0,"disk_free":0,"dir":"/backups"}`
	if p = probeOf(a, a.w.backups); len(p.checks) != 1 || p.checks[0].key != "backup.failed" {
		t.Fatalf("first day: %+v", p)
	}

	// The service does not answer, or answers nonsense: nothing known.
	for _, b := range []*fakeBackups{{err: errors.New("timeout")}, {raw: `[1]`}} {
		a.w.src.Backups = b
		if p = probeOf(a, a.w.backups); p.covered || len(p.checks) != 0 {
			t.Fatalf("unreachable: %+v", p)
		}
	}
}

// With an off-site (S3) copy set up, the newest good backup must have been
// copied, and the settings must be usable; without one nothing is checked.
// A backup still being copied (no outcome yet) is not a problem.
func TestBackupOffsiteCheck(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	bk := &fakeBackups{}
	a.w.src.Backups = bk
	status := func(newest, offsite string) string {
		return fmt.Sprintf(`{"backups":[{"file":"","created_at":%q,"error":"x"},{"file":"b.bkp","created_at":%q%s},`+
			`{"file":"a.bkp","created_at":%q,"uploaded_at":%q}],"running":false,"last_error":"","next_run":0,`+
			`"disk_free":%d,"dir":"/backups","offsite":%s}`,
			a.now.Format(time.RFC3339), a.now.Add(-time.Hour).Format(time.RFC3339), newest,
			a.now.Add(-25*time.Hour).Format(time.RFC3339), a.now.Add(-25*time.Hour).Format(time.RFC3339), uint64(10<<30), offsite)
	}
	const on = `{"configured":true,"endpoint_host":"s3.example.com","bucket":"b"}`
	uploaded := fmt.Sprintf(`,"uploaded_at":%q,"uploaded_to":"s3://b/b.bkp"`, a.now.Add(-time.Hour).Format(time.RFC3339))

	for name, c := range map[string]struct {
		newest, offsite string
		problem         bool
		detail          string
	}{
		"copied":              {uploaded, on, false, ""},
		"being copied":        {``, on, false, ""},
		"copy failed":         {`,"upload_error":"the storage answered HTTP 403"`, on, true, "the storage answered HTTP 403"},
		"settings unusable":   {`,"upload_error":"the S3 upload is not set up correctly: no bucket"`, `{"configured":false,"error":"no bucket"}`, true, "no bucket"},
		"unusable, no backup": {``, `{"configured":false,"error":"no bucket"}`, true, "no bucket"},
	} {
		bk.raw = status(c.newest, c.offsite)
		got := checkOf(t, probeOf(a, a.w.backups), "backup.offsite")
		if got.problem != c.problem || got.detail != c.detail || got.title != "alert.backup_offsite" || got.severity != events.SeverityWarning {
			t.Errorf("%s: %+v", name, got)
		}
	}

	// No S3 set up (or a backup service from before it): no such check.
	for _, raw := range []string{status(``, `{"configured":false}`), backupStatus(a.now, a.now.Add(-time.Hour), "", 10<<30)} {
		bk.raw = raw
		for _, c := range probeOf(a, a.w.backups).checks {
			if c.key == "backup.offsite" {
				t.Fatalf("checked without S3: %+v", c)
			}
		}
	}
}

// While the backup service does not answer, the newest backup it last
// reported still ages: past 26 hours it is stale, although the service
// cannot be asked. After a restart of core, the time it stopped answering
// counts instead.
func TestBackupChecksWhileUnreachable(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	lastGood := a.now.Add(-25 * time.Hour)
	bk := &fakeBackups{raw: backupStatus(a.now, lastGood, "", 10<<30)}
	a.w.src.Backups = bk
	if p := probeOf(a, a.w.backups); !p.covered || checkOf(t, p, "backup.stale").problem {
		t.Fatalf("answering: %+v", p)
	}

	bk.err = errors.New("dial tcp: lookup backup: no such host")
	if p := probeOf(a, a.w.backups); p.covered || len(p.checks) != 0 {
		t.Fatalf("25 hours: %+v", p)
	}
	a.after(2 * time.Hour)
	p := probeOf(a, a.w.backups)
	if c := checkOf(t, p, "backup.stale"); p.covered || len(p.checks) != 1 || !c.problem || c.args["hours"] != "27" {
		t.Fatalf("27 hours, unreachable: %+v", p)
	}

	// Core restarted meanwhile: it knows only since when the service has not
	// answered.
	a.w = a.svc.NewWatchdog(WatchdogSources{Backups: bk}, nil)
	a.w.now = func() time.Time { return a.now }
	unread := a.now
	if p := probeOf(a, a.w.backups); p.covered || len(p.checks) != 0 {
		t.Fatalf("after a restart: %+v", p)
	}
	a.now = unread.Add(backupMaxAge)
	if p := probeOf(a, a.w.backups); len(p.checks) != 0 {
		t.Fatalf("26 hours unread: %+v", p)
	}
	a.after(time.Hour)
	if c := checkOf(t, probeOf(a, a.w.backups), "backup.stale"); !c.problem || c.args["hours"] != "27" {
		t.Fatalf("27 hours unread: %+v", c)
	}

	// It answers again: its own list counts.
	bk.err, bk.raw = nil, backupStatus(a.now, a.now.Add(-time.Hour), "", 10<<30)
	if p := probeOf(a, a.w.backups); !p.covered || checkOf(t, p, "backup.stale").problem {
		t.Fatalf("back: %+v", p)
	}
}

// A backup service that stops (its container OOM-killed, or crash-looping)
// is announced as down within two minutes, and once its newest backup is 26
// hours old, as stale too, although it cannot be asked.
func TestWatchdogBackupServiceStopped(t *testing.T) {
	health := &fakeHealth{}
	health.set("backup", syshealth.StatusOK, "")
	lastGood := alertStart.Add(-time.Hour)
	bk := &fakeBackups{raw: backupStatus(alertStart, lastGood, "", 10<<30)}
	a := newAlertWorld(t, WatchdogSources{Health: health, Backups: bk})
	a.step()
	a.expectSent(0)

	// Stopped: Docker no longer resolves its name.
	health.set("backup", syshealth.StatusNotInstalled, "")
	bk.err = errors.New("dial tcp: lookup backup: no such host")
	a.after(time.Minute)
	a.step()
	a.after(time.Minute)
	a.step()
	got := a.expectSent(1)[0]
	if got.Key != "service.backup" || got.State != events.AlertProblem || got.TitleKey != "alert.service_down" || got.Detail == "" {
		t.Fatalf("stopped: %+v", got)
	}

	a.now = lastGood.Add(backupMaxAge + time.Minute)
	a.step()
	a.after(time.Minute)
	a.step()
	stale := a.sentFor("backup.stale")
	if len(stale) != 1 || stale[0].State != events.AlertProblem || stale[0].Args["hours"] != "26" {
		t.Fatalf("stale: %+v", stale)
	}
	if st := a.state("service.backup"); st == nil || st.State != store.AlertProblem {
		t.Fatalf("the service: %+v", st)
	}
}

func TestDeliveryCheck(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	a.now = time.Now() // the provisioning worker's steps stamp orders with the database's clock
	ctx := context.Background()
	if c := checkOf(t, probeOf(a, a.w.deliveries), "orders.provision_failed"); c.problem || c.args["count"] != "0" {
		t.Fatalf("none: %+v", c)
	}
	u := seedUser(t, a.st, 7101)
	p := seedPlan(t, a.st, false, 1000)
	order := func(key, status string, updated time.Time) string {
		t.Helper()
		o, err := a.svc.CreateOrder(ctx, CreateOrderParams{UserID: u.ID, PlanID: p.ID, IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.st.DB().Exec(ctx, `UPDATE core.orders SET status = $2, updated_at = $3 WHERE id = $1`, o.ID, status, updated); err != nil {
			t.Fatal(err)
		}
		return o.ID
	}
	order("old-failure", "provision_failed", a.now.Add(-61*time.Minute))
	order("delivered", "active", a.now.Add(-time.Minute))
	if c := checkOf(t, probeOf(a, a.w.deliveries), "orders.provision_failed"); c.problem {
		t.Fatalf("only an old failure: %+v", c)
	}
	order("failure-1", "provision_failed", a.now.Add(-59*time.Minute))
	order("failure-2", "provision_failed", a.now.Add(-time.Minute))
	c := checkOf(t, probeOf(a, a.w.deliveries), "orders.provision_failed")
	if !c.problem || c.args["count"] != "2" || c.title != "alert.provision_failed" || c.severity != events.SeverityCritical {
		t.Fatalf("two failures: %+v", c)
	}

	// A first attempt under way has not failed; a retry under way (the
	// worker's own steps: its order is 'provisioning' for the whole panel
	// call) has, and is still counted.
	count := func() string { return checkOf(t, probeOf(a, a.w.deliveries), "orders.provision_failed").args["count"] }
	retry := order("retrying", "paid", a.now)
	attempt := func() {
		t.Helper()
		if err := a.st.WithTx(ctx, func(tx pgx.Tx) error {
			_, err := a.st.MarkProvisioning(ctx, tx, retry)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	attempt()
	if got := count(); got != "2" {
		t.Fatalf("first attempt: %s", got)
	}
	if _, err := a.st.RecordProvisionFailure(ctx, a.st.Conn(), retry, "panel: i/o timeout", a.now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := count(); got != "3" {
		t.Fatalf("failed: %s", got)
	}
	attempt()
	if got := count(); got != "3" {
		t.Fatalf("retrying: %s", got)
	}
}

// The feed check reads the cursor core's gRPC feed keeps for the bot: the
// one its acknowledgements move, under the name core gives that peer.
func TestFeedCheck(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	ctx := context.Background()
	feed := coreFeed(a.st)
	lag := func() alertCheck { return checkOf(t, probeOf(a, a.w.feed), "events.bot_lag") }
	if c := lag(); c.problem || c.args["minutes"] != "0" {
		t.Fatalf("empty feed: %+v", c)
	}
	add := func(age time.Duration) int64 {
		t.Helper()
		var id int64
		if err := a.st.DB().QueryRow(ctx, `INSERT INTO core.outbox_core (event_id, topic, payload, created_at)
			VALUES (gen_random_uuid(), 'order.paid.v1', '{}', $1) RETURNING id`, a.now.Add(-age)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// ack acknowledges events over core's gRPC feed as the caller.
	ack := func(ctx context.Context, id int64) {
		t.Helper()
		if _, err := feed.AckEvents(ctx, &eventsv1.AckEventsRequest{UpToSeq: id}); err != nil {
			t.Fatal(err)
		}
	}
	old := add(30 * time.Minute)
	recent := add(9 * time.Minute)
	// The bot never read the feed: the oldest event counts.
	if c := lag(); !c.problem || c.args["minutes"] != "30" || c.title != "alert.bot_lag" {
		t.Fatalf("never read: %+v", c)
	}
	// Another caller's cursor does not count; nor does the bot's own name
	// for its consumer (cmd/bot), which the feed never sees.
	for _, other := range []string{"someone-else", "core-events"} {
		ack(grpcauth.WithCaller(ctx, other), recent)
	}
	if c := lag(); !c.problem {
		t.Fatalf("another cursor: %+v", c)
	}
	// The bot pulls the oldest event and acknowledges it.
	got, err := feed.PullEvents(botCaller(), &eventsv1.PullEventsRequest{Limit: 1})
	if err != nil || len(got.GetEvents()) != 1 || got.GetEvents()[0].GetSeq() != old {
		t.Fatalf("pull: %v %v", got, err)
	}
	ack(botCaller(), old)
	if c := lag(); c.problem || c.args["minutes"] != "9" {
		t.Fatalf("behind by 9 minutes: %+v", c)
	}
	ack(botCaller(), recent)
	if c := lag(); c.problem || c.args["minutes"] != "0" {
		t.Fatalf("all read: %+v", c)
	}
}

func TestLicenseCheck(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	if p := probeOf(a, a.w.license); !p.covered || len(p.checks) != 0 {
		t.Fatalf("no license: %+v", p)
	}
	v := licensed(t, a.svc, a.now)
	a.svc.cfg.License.Now = func() time.Time { return a.now }

	// A token for another domain is invalid: the License tab says so, the
	// watchdog does not.
	install(t, a.st, v.token(func(tok *license.Token) { tok.Domain = "other.example.com" }))
	if p := probeOf(a, a.w.license); !p.covered || len(p.checks) != 0 {
		t.Fatalf("invalid license: %+v", p)
	}

	for _, c := range []struct {
		name    string
		expires time.Duration
		title   string
		problem bool
		days    string
	}{
		{"a month left", 30 * 24 * time.Hour, "alert.license_expiring", false, "30"},
		{"14 days and a minute", 14*24*time.Hour + time.Minute, "alert.license_expiring", false, "15"},
		{"13.5 days", 13*24*time.Hour + 12*time.Hour, "alert.license_expiring", true, "14"},
		{"an hour", time.Hour, "alert.license_expiring", true, "1"},
		{"grace", -time.Hour, "alert.license_grace", true, ""},
		{"expired", -8 * 24 * time.Hour, "alert.license_expired", true, ""},
	} {
		expires := a.now.Add(c.expires)
		install(t, a.st, v.token(func(tok *license.Token) {
			tok.IssuedAt, tok.ExpiresAt = a.now.Add(-60*24*time.Hour), expires
		}))
		p := probeOf(a, a.w.license)
		got := checkOf(t, p, "license.expiry")
		if got.title != c.title || got.problem != c.problem || got.args["days"] != c.days || got.severity != events.SeverityWarning {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// The limit notice: absent until a purchase was refused, open for 24 hours
// after one while the install is still at the limit, and it never says
// "fixed" (the daily flag).
func TestLimitCheck(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	v := licensed(t, a.svc, a.now)
	a.svc.cfg.License.Now = func() time.Time { return a.now }
	// The trial ended long ago: its own notice is not what this test is about.
	setTrialStart(t, a.st, a.now.Add(-90*24*time.Hour))
	w := &gateWorld{t: t, svc: a.svc, s: a.st, now: a.now, tg: 980000, v: v}
	w.activeServices(license.FreeMaxActiveServices)
	if p := probeOf(a, a.w.license); !p.covered || len(p.checks) != 0 {
		t.Fatalf("no refused purchase: %+v", p)
	}
	if err := a.st.NoteLimitHit(context.Background(), a.st.Conn(), a.now.Add(-23*time.Hour)); err != nil {
		t.Fatal(err)
	}
	c := checkOf(t, probeOf(a, a.w.license), "license.limit")
	if !c.problem || !c.daily || c.title != "alert.license_limit" || c.severity != events.SeverityWarning ||
		c.args["limit"] != "25" || c.args["name"] != "active_services" {
		t.Fatalf("a refused purchase: %+v", c)
	}
	a.after(2 * time.Hour)
	if c = checkOf(t, probeOf(a, a.w.license), "license.limit"); c.problem {
		t.Fatalf("a day later: %+v", c)
	}
	// Refused again, but a license lifted the limit: no problem.
	if err := a.st.NoteLimitHit(context.Background(), a.st.Conn(), a.now); err != nil {
		t.Fatal(err)
	}
	install(t, a.st, v.token(func(tok *license.Token) { tok.Entitlements.MaxActiveServices = 500 }))
	if c = checkOf(t, probeOf(a, a.w.license), "license.limit"); c.problem {
		t.Fatalf("under a license that allows more: %+v", c)
	}
}

// The System page lists the announced problems only, critical first, then
// the oldest, each with its text in English.
func TestOpenAlerts(t *testing.T) {
	a := newAlertWorld(t, WatchdogSources{})
	ctx := context.Background()
	sent := a.now
	put := func(key, state, severity, title string, since time.Duration, args map[string]string) {
		t.Helper()
		if err := a.st.PutAlertState(ctx, a.st.Conn(), &store.AlertState{Key: key, State: state, Since: a.now.Add(since),
			LastSentAt: &sent, Severity: severity, TitleKey: title, Args: args, Detail: "why"}); err != nil {
			t.Fatal(err)
		}
	}
	put("backup.disk", store.AlertProblem, events.SeverityWarning, "alert.disk_low", -3*time.Hour, map[string]string{"free": fmt.Sprint(1536 << 20)})
	put("server.x", store.AlertProblem, events.SeverityCritical, "alert.server_down", -time.Hour, map[string]string{"server": "DE <b>"})
	put("orders.provision_failed", store.AlertProblem, events.SeverityCritical, "alert.provision_failed", -2*time.Hour, map[string]string{"count": "4"})
	put("service.bot", store.AlertPending, events.SeverityCritical, "alert.service_down", 0, map[string]string{"service": "bot"})
	put("service.core", store.AlertOK, events.SeverityCritical, "alert.service_down", 0, nil)
	put("license.expiry", store.AlertProblem, events.SeverityWarning, "alert.license_grace", -time.Minute,
		map[string]string{"date": fmt.Sprint(time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC).Unix())})

	list, err := a.svc.OpenAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var keys, titles []string
	for _, al := range list {
		keys = append(keys, al.Key)
		titles = append(titles, al.Title)
	}
	if strings.Join(keys, ",") != "orders.provision_failed,server.x,backup.disk,license.expiry" {
		t.Fatalf("order: %v", keys)
	}
	want := []string{
		"4 paid orders could not be delivered in the last hour.",
		"The 3x-ui server DE <b> does not answer.",
		"The disk is almost full: 1.5 GB free.",
		"The license has expired; its grace period ends on 2 Nov 2026.",
	}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("titles:\n%s\nwant\n%s", strings.Join(titles, "\n"), strings.Join(want, "\n"))
	}
	if list[1].Detail != "why" || list[1].Severity != events.SeverityCritical || list[1].TitleKey != "alert.server_down" ||
		list[1].Args["server"] != "DE <b>" || !list[1].Since.Equal(a.now.Add(-time.Hour)) {
		t.Fatalf("an alert: %+v", list[1])
	}
}

// Every title the watchdog can send has a text in both languages, with the
// values it is given.
func TestAlertTitlesExist(t *testing.T) {
	cat := Texts()
	titles := map[string][]string{
		"alert.service_down": {"service"}, "alert.service_not_ready": {"service"}, "alert.server_down": {"server"},
		"alert.zarinpal_failing": nil, "alert.zarinpal_stuck": {"count"},
		"alert.idpay_failing": nil, "alert.idpay_stuck": {"count"}, "alert.nextpay_failing": nil, "alert.nextpay_stuck": {"count"},
		"alert.backup_failed": nil,
		"alert.usdt_failing":  nil, "alert.usdt_stuck": {"count"}, "alert.backup_offsite": nil,
		"alert.backup_stale": {"hours"}, "alert.disk_low": {"free"}, "alert.provision_failed": {"count"},
		"alert.bot_lag": {"minutes"}, "alert.license_expiring": {"date", "days"}, "alert.license_grace": {"date"},
		"alert.license_expired": nil, "alert.license_limit": {"limit", "name"}, "alert.trial_ending": {"date", "days"}, "alert.trial_ended": nil, "alert.tls_expiring": {"address", "date", "days"}, "alert.tls_failing": {"address"},
		"alert.tls_problem": nil, "alert.tls_caddy_down": nil,
	}
	for key, args := range titles {
		info, ok := cat.Info(key)
		if !ok {
			t.Errorf("%s: no text", key)
			continue
		}
		if fmt.Sprint(info.Placeholders) != fmt.Sprint(args) {
			t.Errorf("%s: placeholders %v, want %v", key, info.Placeholders, args)
		}
	}
}

// hangingLister never answers before its time limit.
type hangingLister struct{}

func (hangingLister) ListServers(ctx context.Context) (*ServerList, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A source that hangs costs only its own checks: the others are recorded,
// and its stored problems stay as they were.
func TestWatchdogSurvivesAHangingSource(t *testing.T) {
	health := &fakeHealth{}
	health.set("bot", syshealth.StatusDown, "does not answer")
	a := newAlertWorld(t, WatchdogSources{Health: health, Servers: hangingLister{}})
	a.w.timeout = 50 * time.Millisecond
	if err := a.st.PutAlertState(context.Background(), a.st.Conn(), &store.AlertState{Key: "server.s1", State: store.AlertProblem,
		Since: a.now, Severity: events.SeverityCritical, TitleKey: "alert.server_down"}); err != nil {
		t.Fatal(err)
	}
	a.step()
	a.after(time.Minute)
	a.step()
	if got := a.expectSent(1)[0]; got.Key != "service.bot" {
		t.Fatalf("sent: %+v", got)
	}
	if st := a.state("server.s1"); st == nil || st.State != store.AlertProblem {
		t.Fatalf("the hanging source's problem: %+v", st)
	}
}
