package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// ---- a strict reader of the text format 0.0.4, to check what is written the way a scraper would

type promSeries struct {
	name   string
	labels map[string]string
	value  float64
}

type promFamily struct {
	help, typ string
	series    []promSeries
}

var (
	promNameRe  = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	promLabelRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// parseProm reads a body; every rule of the format that a scraper depends on is a failure here: a trailing line feed,
// HELP and TYPE before the samples of a metric (once each), a valid name, labels in braces with escaped values, a value
// that is a number, no series twice.
func parseProm(t *testing.T, body []byte) map[string]*promFamily {
	t.Helper()
	text := string(body)
	if text == "" {
		return map[string]*promFamily{}
	}
	if !strings.HasSuffix(text, "\n") {
		t.Fatalf("the body does not end with a line feed: %q", text[max(0, len(text)-40):])
	}
	if !utf8.ValidString(text) {
		t.Fatal("the body is not UTF-8")
	}
	fams := map[string]*promFamily{}
	seen := map[string]bool{}
	for n, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		fail := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("line %d %q: %s", n+1, line, fmt.Sprintf(format, args...))
		}
		switch {
		case line == "":
			fail("an empty line")
		case strings.HasPrefix(line, "# HELP "):
			name, help, ok := strings.Cut(strings.TrimPrefix(line, "# HELP "), " ")
			if !ok || !promNameRe.MatchString(name) {
				fail("a bad HELP line")
			}
			if fams[name] != nil {
				fail("HELP twice")
			}
			fams[name] = &promFamily{help: help}
		case strings.HasPrefix(line, "# TYPE "):
			name, typ, ok := strings.Cut(strings.TrimPrefix(line, "# TYPE "), " ")
			f := fams[name]
			if !ok || f == nil || f.typ != "" || typ != "gauge" {
				fail("a bad TYPE line (it follows its HELP, once, and every metric is a gauge)")
			}
			f.typ = typ
		case strings.HasPrefix(line, "#"):
			fail("a comment the format does not know")
		default:
			name, labels, rest := splitSample(t, line)
			f := fams[name]
			if f == nil || f.typ == "" {
				fail("a sample before its HELP and TYPE")
			}
			if key := name + fmt.Sprint(sortedPairs(labels)); seen[key] {
				fail("the same series twice")
			} else {
				seen[key] = true
			}
			if strings.Contains(rest, " ") { // no timestamp is written
				fail("more than a value after the labels")
			}
			v, err := strconv.ParseFloat(rest, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				fail("the value %q is not a finite number", rest)
			}
			f.series = append(f.series, promSeries{name: name, labels: labels, value: v})
		}
	}
	return fams
}

func sortedPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// splitSample reads name{labels} value, undoing the escapes of a label value.
func splitSample(t *testing.T, line string) (name string, labels map[string]string, rest string) {
	t.Helper()
	labels = map[string]string{}
	i := strings.IndexAny(line, "{ ")
	if i < 0 {
		t.Fatalf("no value in %q", line)
	}
	name = line[:i]
	if !promNameRe.MatchString(name) {
		t.Fatalf("bad metric name in %q", line)
	}
	if line[i] == ' ' {
		return name, labels, line[i+1:]
	}
	s := line[i+1:]
	for {
		eq := strings.Index(s, `="`)
		if eq < 0 || !promLabelRe.MatchString(s[:eq]) {
			t.Fatalf("bad label in %q", line)
		}
		key := s[:eq]
		s = s[eq+2:]
		var val strings.Builder
		for {
			if s == "" {
				t.Fatalf("an unterminated label value in %q", line)
			}
			c := s[0]
			s = s[1:]
			if c == '"' {
				break
			}
			if c == '\\' {
				if s == "" {
					t.Fatalf("a dangling escape in %q", line)
				}
				switch s[0] {
				case '\\', '"':
					val.WriteByte(s[0])
				case 'n':
					val.WriteByte('\n')
				default:
					t.Fatalf("an escape the format does not have in %q", line)
				}
				s = s[1:]
				continue
			}
			if c == '\n' || c < 0x20 {
				t.Fatalf("a raw control character in %q", line)
			}
			val.WriteByte(c)
		}
		labels[key] = val.String()
		switch {
		case strings.HasPrefix(s, ","):
			s = s[1:]
		case strings.HasPrefix(s, "} "):
			return name, labels, s[2:]
		default:
			t.Fatalf("bad end of labels in %q", line)
		}
	}
}

func (f *promFamily) value(labels ...string) (float64, bool) {
	for _, s := range f.series {
		if len(s.labels) != len(labels)/2 {
			continue
		}
		match := true
		for i := 0; i+1 < len(labels); i += 2 {
			if s.labels[labels[i]] != labels[i+1] {
				match = false
			}
		}
		if match {
			return s.value, true
		}
	}
	return 0, false
}

// exposition asks the monitor afresh and reads the answer.
func (e *env) exposition() (map[string]*promFamily, []byte) {
	e.t.Helper()
	e.m.probeMu.Lock()
	e.m.probed = nil
	e.m.probeMu.Unlock()
	body := e.m.Exposition(context.Background())
	return parseProm(e.t, body), body
}

func want(t *testing.T, fams map[string]*promFamily, name string, v float64, labels ...string) {
	t.Helper()
	f := fams[name]
	if f == nil {
		t.Errorf("%s: not written", name)
		return
	}
	if got, ok := f.value(labels...); !ok || math.Abs(got-v) > 1e-9 {
		t.Errorf("%s%v: %v (%v), want %v", name, labels, got, ok, v)
	}
}

func absent(t *testing.T, fams map[string]*promFamily, name string) {
	t.Helper()
	if fams[name] != nil {
		t.Errorf("%s was written: %+v", name, fams[name].series)
	}
}

// A healthy stack: every figure the monitor knows is a gauge with the value the overview shows.
func TestExpositionOfAHealthyStack(t *testing.T) {
	e := newEnv(t)
	e.servers.list = &domain.ServerList{Servers: []domain.Server{
		{ID: "s1", Name: "DE-1", Enabled: true, Healthy: true, CheckedAt: 1, Clients: 90, MaxClients: 100},
		{ID: "s2", Name: "NL-1", Enabled: true, Healthy: false, CheckedAt: 1, Clients: 12},
		{ID: "s3", Name: "spare", Enabled: false, Healthy: true, CheckedAt: 1, Clients: 3},
		{ID: "s4", Name: "never checked", Enabled: true, Clients: 0},
	}}
	e.payments.n = 2
	e.backups.raw = `{"backups":[{"name":"b1","created_at":"` + e.clk.now().Add(-3*time.Hour).Format(time.RFC3339) + `","size":1,"ok":true}],"running":false,"last_error":"","next_run":0,"disk_free":1}`
	r := allHealthy()
	r.Services[1].Status = syshealth.StatusDown
	r.Services = append(r.Services, syshealth.Service{Name: "backup", Status: syshealth.StatusNotReady}, syshealth.Service{Name: "extra", Status: syshealth.StatusNotInstalled})
	e.health.set(r)
	e.snap(nil)
	e.clk.add(10 * time.Second)

	fams, body := e.exposition()
	if len(body) == 0 {
		t.Fatal("empty answer")
	}
	// Every family has its help and type, and only the metrics of the closed list are written.
	known := map[string]bool{}
	for _, d := range promDefs {
		known[d.name] = true
	}
	for name, f := range fams {
		if !known[name] || f.help == "" || f.typ != "gauge" || len(f.series) == 0 {
			t.Errorf("%s: %+v", name, f)
		}
	}

	want(t, fams, promBuildInfo, 1, "version", "0.7.0")
	want(t, fams, promStartTime, float64(e.m.Started().Unix()))
	for svc, up := range map[string]float64{"core": 1, "bot": 0, "payments": 1, "provisioner": 1, "backup": 0} {
		want(t, fams, promServiceUp, up, "service", svc)
	}
	if _, ok := fams[promServiceUp].value("service", "extra"); ok {
		t.Error("a service that is not installed is written")
	}
	want(t, fams, promServiceResponse, 0.005, "service", "core")
	if _, ok := fams[promServiceResponse].value("service", "bot"); ok {
		t.Error("a response time for a service that does not answer")
	}
	want(t, fams, promDatabaseUp, 1)
	if v, ok := fams[promDatabaseResponse].value(); !ok || v < 0 || v > 5 {
		t.Errorf("database response: %v %v", v, ok)
	}
	want(t, fams, promQueue, 2, "queue", "pending_payments")
	for _, q := range []string{"outbox", "provisioning", "pending_orders"} {
		if _, ok := fams[promQueue].value("queue", q); !ok {
			t.Errorf("queue %s missing", q)
		}
	}
	for _, w := range []string{"1h", "24h", "7d"} {
		if _, ok := fams[promOrdersPaid].value("window", w); !ok {
			t.Errorf("orders paid %s missing", w)
		}
	}
	want(t, fams, promAlertsOpen, 0, "severity", "critical")
	want(t, fams, promAlertsOpen, 0, "severity", "warning")

	// The panel servers: up only for those that are on and were checked; the limit only where there is one.
	want(t, fams, promPanelUp, 1, "server", "DE-1")
	want(t, fams, promPanelUp, 0, "server", "NL-1")
	if len(fams[promPanelUp].series) != 2 {
		t.Errorf("panel up for a server that is off or never checked: %+v", fams[promPanelUp].series)
	}
	want(t, fams, promPanelClients, 90, "server", "DE-1")
	want(t, fams, promPanelClients, 3, "server", "spare")
	want(t, fams, promPanelClients, 0, "server", "never checked")
	want(t, fams, promPanelLimit, 100, "server", "DE-1")
	if len(fams[promPanelLimit].series) != 1 {
		t.Errorf("a limit for a server without one: %+v", fams[promPanelLimit].series)
	}

	// The trial of a new install, the backup, and the server.
	if v, ok := fams[promLicenseDays].value("basis", "trial"); !ok || v < 1 {
		t.Errorf("trial days: %v %v", v, ok)
	}
	want(t, fams, promBackupAge, 3*3600+10)
	want(t, fams, promBackupOK, 1)
	want(t, fams, promCollectorUp, 1)
	want(t, fams, promHostCPU, 0.125)
	want(t, fams, promHostLoad1, 0.5)
	want(t, fams, promHostMemUsed, 2<<30)
	want(t, fams, promHostMemTotal, 8<<30)
	want(t, fams, promHostDiskUsed, 40.0/95.0, "mount", "/") // df's share: used / (used + available)
	want(t, fams, promHostDiskFree, 55<<30, "mount", "/")
	want(t, fams, promHostDiskTotal, 100<<30, "mount", "/")
	want(t, fams, promHostUptime, 100000)

	// Two scrapes of the same state are the same bytes (the database's own response time is the one figure that moves).
	steady := func(b []byte) string {
		var keep []string
		for _, l := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(l, promDatabaseResponse) {
				keep = append(keep, l)
			}
		}
		return strings.Join(keep, "\n")
	}
	if _, again := e.exposition(); steady(again) != steady(body) {
		t.Error("the same state gave different bytes")
	}
}

// A figure that is not known is left out, never written as 0.
func TestExpositionLeavesOutWhatItDoesNotKnow(t *testing.T) {
	e := newEnv(t)
	e.m.cfg.Dir = "" // no monitor directory is mounted
	e.m.cfg.Backups = nil
	e.m.cfg.Servers = nil
	e.m.cfg.Payments = nil
	e.m.cfg.Health = nil
	e.m.cfg.Bot = nil
	fams, _ := e.exposition()
	for _, name := range []string{promServiceUp, promServiceResponse, promPanelUp, promPanelClients, promPanelLimit, promBackupAge, promBackupOK,
		promCollectorUp, promHostCPU, promHostLoad1, promHostMemUsed, promHostMemTotal, promHostDiskUsed, promHostDiskFree, promHostDiskTotal, promHostUptime} {
		absent(t, fams, name)
	}
	if f := fams[promQueue]; f == nil {
		t.Error("core's own queues are known")
	} else if _, ok := f.value("queue", "pending_payments"); ok {
		t.Error("pending payments were not asked, and are written")
	}
	want(t, fams, promBuildInfo, 1, "version", "0.7.0")

	// A collector that is installed but quiet: up is 0 and the host is not written.
	e2 := newEnv(t)
	e2.snap(nil)
	e2.clk.add(5 * time.Minute)
	fams, _ = e2.exposition()
	want(t, fams, promCollectorUp, 0)
	absent(t, fams, promHostCPU)
	absent(t, fams, promHostDiskUsed)

	// A backup service that has made nothing yet says nothing about a run; one whose last run failed says so.
	e3 := newEnv(t)
	e3.backups.raw = `{"backups":[],"running":false,"last_error":"","next_run":0,"disk_free":1}`
	fams, _ = e3.exposition()
	absent(t, fams, promBackupAge)
	absent(t, fams, promBackupOK)
	e3.backups.raw = `{"backups":[],"running":false,"last_error":"pg_dump failed: password authentication failed for user bobres","next_run":0,"disk_free":1}`
	fams, body := e3.exposition()
	want(t, fams, promBackupOK, 0)
	absent(t, fams, promBackupAge)
	if strings.Contains(string(body), "pg_dump") || strings.Contains(string(body), "password") {
		t.Error("the text of a failure is in the answer")
	}
}

// Open alerts are counted by severity, and the sales by window from the ledger.
func TestExpositionCountsAlertsAndSales(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i, sev := range []string{"critical", "warning", "warning"} {
		if err := e.st.PutAlertState(ctx, e.st.Conn(), &store.AlertState{Key: fmt.Sprintf("service.test%d", i), State: store.AlertProblem,
			Since: e.clk.now(), Severity: sev, TitleKey: "alert.service_down", Args: map[string]string{"service": "x"}, Detail: "secret detail 10.0.0.9"}); err != nil {
			t.Fatal(err)
		}
	}
	// Four purchases and a refund, made on a user of this test.
	uid := buuid.MustV7().String()
	if _, err := e.st.DB().Exec(ctx, `INSERT INTO core.users (id, telegram_id, username) VALUES ($1, $2, 'promtest')`, uid, 5_000_000_000+time.Now().UnixNano()%1_000_000_000); err != nil {
		t.Fatal(err)
	}
	now := e.clk.now()
	var base [3]int64
	if c, err := e.st.PaidOrderCounts(ctx, e.st.Conn(), now); err != nil {
		t.Fatal(err)
	} else {
		base = [3]int64{c.Hour, c.Day, c.Week}
	}
	for i, c := range []struct {
		kind string
		ago  time.Duration
	}{{"purchase", 10 * time.Minute}, {"license_purchase", 5 * time.Hour}, {"purchase", 30 * time.Hour}, {"supplier_purchase", 5 * 24 * time.Hour}, {"refund", time.Minute}} {
		amount := int64(-1000)
		if c.kind == "refund" {
			amount = 1000
		}
		if _, err := e.st.DB().Exec(ctx, `INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after, created_at)
			VALUES ($1, $2, 'IRT', $3, $4, $5, 0, $6)`, buuid.MustV7().String(), uid, amount, c.kind, fmt.Sprintf("prom-%s-%d", uid, i), now.Add(-c.ago)); err != nil {
			t.Fatal(err)
		}
	}
	fams, body := e.exposition()
	want(t, fams, promAlertsOpen, 1, "severity", "critical")
	want(t, fams, promAlertsOpen, 2, "severity", "warning")
	want(t, fams, promOrdersPaid, float64(base[0]+1), "window", "1h")
	want(t, fams, promOrdersPaid, float64(base[1]+2), "window", "24h")
	want(t, fams, promOrdersPaid, float64(base[2]+4), "window", "7d")
	if strings.Contains(string(body), "secret detail") || strings.Contains(string(body), "10.0.0.9") {
		t.Error("an alert's detail is in the answer")
	}

	// The count is kept for 30 seconds, so a scrape every few seconds asks the database once; then it is read again.
	if _, err := e.st.DB().Exec(ctx, `INSERT INTO core.ledger_entries (id, user_id, currency, amount, kind, idempotency_key, balance_after, created_at)
		VALUES ($1, $2, 'IRT', -1000, 'purchase', $3, 0, $4)`, buuid.MustV7().String(), uid, "prom-late-"+uid, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	e.clk.add(10 * time.Second)
	fams, _ = e.exposition()
	want(t, fams, promOrdersPaid, float64(base[0]+1), "window", "1h")
	e.clk.add(25 * time.Second)
	fams, _ = e.exposition()
	want(t, fams, promOrdersPaid, float64(base[0]+2), "window", "1h")
}

// A license shows its days to the end (rounded up); one in its grace period shows zero or a negative number; the trial shows
// its days; the free tier shows none.
func TestExpositionLicenseDays(t *testing.T) {
	e := newEnv(t)
	now := e.clk.now()
	days := func(l *domain.EffectiveLicense) *promFamily {
		e.m.probeMu.Lock()
		e.m.probed = &probe{at: now, pending: -1, license: l}
		e.m.probeMu.Unlock()
		return parseProm(t, e.m.Exposition(context.Background()))[promLicenseDays]
	}
	eff := func(basis license.Basis, expires, trialEnds time.Time) *domain.EffectiveLicense {
		l := &domain.EffectiveLicense{License: &domain.LicenseStatus{Token: &license.Token{ExpiresAt: expires}}}
		l.Basis, l.TrialEndsAt = basis, trialEnds
		return l
	}
	for _, c := range []struct {
		name  string
		l     *domain.EffectiveLicense
		basis string
		days  float64
		none  bool
	}{
		{"free tier", eff(license.BasisFree, time.Time{}, now.Add(-time.Hour)), "", 0, true},
		{"trial", eff(license.BasisTrial, time.Time{}, now.Add(30*24*time.Hour-time.Hour)), "trial", 30, false},
		{"last hours of a trial", eff(license.BasisTrial, time.Time{}, now.Add(3*time.Hour)), "trial", 1, false},
		{"a trial of unknown end", eff(license.BasisTrial, time.Time{}, time.Time{}), "", 0, true},
		{"a license", eff(license.BasisLicense, now.Add(10*24*time.Hour+time.Hour), time.Time{}), "license", 11, false},
		{"a license that ends tomorrow", eff(license.BasisLicense, now.Add(20*time.Hour), time.Time{}), "license", 1, false},
		{"a license in its grace period", eff(license.BasisGrace, now.Add(-60*time.Hour), time.Time{}), "license", -2, false},
		{"a license that expired a minute ago", eff(license.BasisGrace, now.Add(-time.Minute), time.Time{}), "license", 0, false},
	} {
		f := days(c.l)
		switch {
		case c.none && f != nil:
			t.Errorf("%s: written: %+v", c.name, f.series)
		case c.none:
		case f == nil:
			t.Errorf("%s: not written", c.name)
		default:
			if got, ok := f.value("basis", c.basis); !ok || got != c.days {
				t.Errorf("%s: %v (%v), want %v", c.name, got, ok, c.days)
			}
		}
	}
}

// What a label value may hold is made safe, and the names of the panel servers are the one thing an operator types.
func TestExpositionEscapesLabelValues(t *testing.T) {
	e := newEnv(t)
	names := []string{`say "hi"`, `back\slash`, "two\nlines", "tab\there", "ünïcode — نام", "bad\xffutf8", `a{b}`, "x,y=z"}
	var servers []domain.Server
	for i, n := range names {
		servers = append(servers, domain.Server{ID: fmt.Sprintf("s%d", i), Name: n, Enabled: true, Healthy: true, CheckedAt: 1, Clients: int64(i)})
	}
	e.servers.list = &domain.ServerList{Servers: servers}
	fams, body := e.exposition() // parseProm fails on a raw control character, a bad escape or a bad UTF-8 byte
	if !strings.Contains(string(body), `server="say \"hi\""`) || !strings.Contains(string(body), `server="back\\slash"`) || !strings.Contains(string(body), `server="two\nlines"`) {
		t.Errorf("escapes:\n%s", body)
	}
	got := map[string]float64{}
	for _, s := range fams[promPanelClients].series {
		got[s.labels["server"]] = s.value
	}
	for i, n := range []string{`say "hi"`, `back\slash`, "two\nlines", "tabhere", "ünïcode — نام", "bad�utf8", `a{b}`, "x,y=z"} {
		if got[n] != float64(i) {
			t.Errorf("server %q: %v", n, got)
		}
	}
	if promEscape("a\x00b\rc") != "abc" {
		t.Errorf("control characters: %q", promEscape("a\x00b\rc"))
	}
}

// The closed list is the whole surface: no label but the fixed ones, no customer, no secret, and the number of series stays
// small whatever the store holds.
func TestExpositionHoldsNoCustomerDataAndHasBoundedLabels(t *testing.T) {
	e := newEnv(t)
	secret := "Tok3n-S3cret-10.9.8.7-user@example.com-/sub/abcdef"
	e.servers.list = &domain.ServerList{Servers: []domain.Server{
		{ID: "s1", Name: "DE-1", BaseURL: "https://panel.example.com:2053/" + secret, SubBaseURL: "https://sub.example.com/" + secret, Enabled: true,
			Healthy: false, HealthError: "dial tcp 10.9.8.7: " + secret, CheckedAt: 1, Clients: 5, PanelVersion: "v3.9.0"},
	}}
	e.gateways.list = []domain.GatewayConfig{{Provider: "zarinpal", Enabled: true, Offered: false, Problem: secret}}
	e.bot.setStats(botfiles.BotStats{StartedAt: 1})
	r := allHealthy()
	r.Services[0].Detail, r.Services[0].Version = secret, "9.9.9"
	e.health.set(r)
	e.snap(func(s *snapshot.Snapshot) {
		s.Host.Hostname = secret
		s.Host.OS, s.Host.Kernel = secret, secret
	})
	e.clk.add(5 * time.Second)
	fams, body := e.exposition()
	for _, bad := range []string{"Tok3n", "S3cret", "10.9.8.7", "example.com", "/sub/", "srv1", "9.9.9", "zarinpal", "Ubuntu", "6.8.0", "v3.9.0", "tls-agent", "bobres-core"} {
		if strings.Contains(string(body), bad) {
			t.Errorf("%q is in the answer", bad)
		}
	}
	allowed := map[string]bool{"version": true, "service": true, "queue": true, "window": true, "severity": true, "server": true, "basis": true, "mount": true}
	series := 0
	for _, f := range fams {
		for _, s := range f.series {
			series++
			for k := range s.labels {
				if !allowed[k] {
					t.Errorf("%s has the label %q", s.name, k)
				}
			}
		}
	}
	if series > 80 {
		t.Errorf("%d series for one server and one disk", series)
	}
}

// The runbook is the user's way in, so it must not drift from the code: every metric the endpoint writes is in it, it names no
// metric the endpoint does not write, and the Grafana dashboard it carries is valid JSON that asks only for those metrics.
func TestRunbookNamesTheMetricsTheEndpointWrites(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/runbooks/monitoring.md")
	if err != nil {
		t.Skipf("the docs are not in this checkout: %v", err)
	}
	doc := string(raw)
	known := map[string]bool{}
	for _, d := range promDefs {
		known[d.name] = true
		if !strings.Contains(doc, "`"+d.name+"`") {
			t.Errorf("the runbook does not describe %s", d.name)
		}
	}
	re := regexp.MustCompile(`\bbobres_[a-z0-9_]+`)
	for _, name := range re.FindAllString(doc, -1) {
		if !known[name] {
			t.Errorf("the runbook names %s, which the endpoint does not write", name)
		}
	}

	_, after, ok := strings.Cut(doc, "<!-- grafana-dashboard -->\n```json\n")
	if !ok {
		t.Fatal("the runbook has no Grafana dashboard after its marker")
	}
	block, _, _ := strings.Cut(after, "\n```")
	var dash struct {
		Title  string `json:"title"`
		UID    string `json:"uid"`
		Panels []struct {
			ID      int    `json:"id"`
			Type    string `json:"type"`
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal([]byte(block), &dash); err != nil {
		t.Fatalf("the dashboard is not valid JSON: %v", err)
	}
	if dash.Title == "" || dash.UID == "" || len(dash.Panels) < 8 {
		t.Fatalf("the dashboard: %+v", dash)
	}
	ids := map[int]bool{}
	for _, p := range dash.Panels {
		if ids[p.ID] || p.Title == "" || p.Type == "" || len(p.Targets) == 0 {
			t.Errorf("panel %+v", p)
		}
		ids[p.ID] = true
		for _, tg := range p.Targets {
			names := re.FindAllString(tg.Expr, -1)
			if len(names) == 0 {
				t.Errorf("panel %q asks for no BOBRES metric: %q", p.Title, tg.Expr)
			}
			for _, n := range names {
				if !known[n] {
					t.Errorf("panel %q asks for %s, which the endpoint does not write", p.Title, n)
				}
			}
		}
	}
}

// promDefs is a closed list without repeats, valid names, and one help line each.
func TestPromDefsAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range promDefs {
		if !promNameRe.MatchString(d.name) || !strings.HasPrefix(d.name, "bobres_") || seen[d.name] || strings.TrimSpace(d.help) == "" ||
			strings.Contains(d.help, "\n") || !strings.HasSuffix(d.help, ".") {
			t.Errorf("bad definition %+v", d)
		}
		seen[d.name] = true
	}
}
