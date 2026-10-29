package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/monitor"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
)

// ---- fakes: what the monitor asks the other services

type statusHealth struct {
	mu     sync.Mutex
	status map[string]string // service -> status; the others are ok
	calls  int
}

func (h *statusHealth) Check(context.Context) syshealth.Report {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	r := syshealth.Report{Version: "9.9.9"}
	for _, n := range []string{"core", "bot", "payments", "provisioner"} {
		s := syshealth.StatusOK
		if v, ok := h.status[n]; ok {
			s = v
		}
		r.Services = append(r.Services, syshealth.Service{Name: n, Status: s, Version: "9.9.9", LatencyMS: 7, Checks: map[string]string{"database": "ok"}})
	}
	return r
}

func (h *statusHealth) set(service, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status = map[string]string{service: status}
}

func (h *statusHealth) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

type statusServers struct{ list []domain.Server }

func (s statusServers) ListServers(context.Context) (*domain.ServerList, error) {
	return &domain.ServerList{Servers: s.list}, nil
}

// withSources gives the fixture's monitor a health check and a list of servers to look at.
func (m *monFixture) withSources(h *statusHealth, servers ...domain.Server) {
	m.mon = monitor.New(monitor.Config{Store: m.st, Domain: m.dom, Dir: m.dir, Health: h, Servers: statusServers{servers}, Version: "0.7.0", Now: m.now})
	m.api.cfg.Monitor = m.mon
}

// turnStatusPage switches the public status page on or off, as the owner does in Settings.
func (m *monFixture) turnStatusPage(on bool) {
	m.t.Helper()
	v := "false"
	if on {
		v = "true"
	}
	m.staffUser(ownerTG, "owner")
	if _, err := m.dom.SetSettings(context.Background(), actor0(m.fixture), map[string]string{domain.SettingStatusPage: v}); err != nil {
		m.t.Fatal(err)
	}
}

type publicStatusJSON struct {
	Status     string `json:"status"`
	UpdatedAt  int64  `json:"updated_at"`
	Components []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"components"`
}

func (m *monFixture) statusJSON() (int, http.Header, publicStatusJSON, string) {
	m.t.Helper()
	code, h, body := m.page("/status.json")
	var out publicStatusJSON
	if code == 200 {
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			m.t.Fatalf("status.json: %v\n%s", err, body)
		}
	}
	return code, h, out, body
}

func (p publicStatusJSON) String() string {
	parts := []string{}
	for _, c := range p.Components {
		parts = append(parts, c.ID+"="+c.Status)
	}
	return p.Status + ": " + strings.Join(parts, " ")
}

// ---- the page and its switch

// Off until the owner turns it on, and then off again as soon as they say so: both addresses are a plain 404, the page the very same
// bytes as any address that is not there.
func TestStatusPageIsOffUntilTheOwnerTurnsItOn(t *testing.T) {
	m := newMonFixture(t)
	_, _, notThere := m.page("/sub/not-a-token")
	for _, path := range []string{"/status", "/status?l=en", "/status.json"} {
		code, h, body := m.page(path)
		if code != http.StatusNotFound || !strings.Contains(body, "404") || h.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s off: %d %q", path, code, body)
		}
		if path != "/status.json" && body != notThere {
			t.Errorf("%s off is not the page every missing address gets:\n%s", path, body)
		}
	}
	if v, err := m.dom.Settings(context.Background()); err != nil || domain.StatusPageFrom(v) {
		t.Fatalf("the default: %v %v", v, err)
	}

	m.turnStatusPage(true)
	if code, _, body := m.page("/status"); code != 200 || !strings.Contains(body, `class="verdict ok"`) {
		t.Fatalf("on: %d\n%s", code, body)
	}
	if code, _, st, _ := m.statusJSON(); code != 200 || st.Status != "ok" {
		t.Fatalf("on, json: %d %v", code, st)
	}

	// Off again at once, although a look at the stack is still cached.
	m.turnStatusPage(false)
	for _, path := range []string{"/status", "/status.json"} {
		if code, _, _ := m.page(path); code != http.StatusNotFound {
			t.Errorf("%s turned off again: %d", path, code)
		}
	}
	// Only a GET is a page.
	m.turnStatusPage(true)
	for _, path := range []string{"/status", "/status.json"} {
		if code, _, _ := m.pageFrom(path, "198.51.100.77", http.MethodPost); code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: %d", path, code)
		}
	}
}

// Without a monitor (an install that has none) there is nothing to say, switch or not.
func TestStatusPageNeedsTheMonitor(t *testing.T) {
	m := newMonFixture(t)
	m.turnStatusPage(true)
	m.api.cfg.Monitor = nil
	for _, path := range []string{"/status", "/status.json"} {
		if code, _, _ := m.page(path); code != http.StatusNotFound {
			t.Errorf("%s without a monitor: %d", path, code)
		}
	}
}

// The verdict is the worst of the parts, in both languages; the parts have general names and states.
func TestStatusPageSaysWhatIsDown(t *testing.T) {
	m := newMonFixture(t)
	h := &statusHealth{}
	m.withSources(h, domain.Server{ID: "id-7", Name: "Secret-Panel-NL", BaseURL: "https://panel.secret.example:2053", Enabled: true, Healthy: true, CheckedAt: 1})
	m.turnStatusPage(true)

	code, hdr, st, _ := m.statusJSON()
	if code != 200 || st.String() != "ok: dashboard=ok bot=ok payments=ok panels=ok" {
		t.Fatalf("all working: %d %v", code, st)
	}
	if hdr.Get("Content-Type") != "application/json; charset=utf-8" || hdr.Get("Access-Control-Allow-Origin") != "*" ||
		hdr.Get("Cache-Control") != "public, max-age=30" || hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("headers: %v", hdr)
	}
	if st.UpdatedAt < time.Now().Add(-time.Minute).Unix() {
		t.Errorf("updated at %d", st.UpdatedAt)
	}
	names := []string{}
	for _, c := range st.Components {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "Dashboard,Bot,Payments,Panels" {
		t.Errorf("names: %v", names)
	}

	// The page in Persian (the default) and in English.
	code, hdr, fa := m.page("/status")
	if code != 200 || !strings.Contains(fa, `lang="fa" dir="rtl"`) || !strings.Contains(fa, "همهٔ سیستم") || !strings.Contains(fa, "ربات") ||
		!strings.Contains(fa, `href="/status?l=en"`) {
		t.Fatalf("fa: %d\n%s", code, fa)
	}
	for k, want := range map[string]string{"Content-Type": "text/html; charset=utf-8", "X-Content-Type-Options": "nosniff", "X-Robots-Tag": "noindex, nofollow",
		"Cache-Control": "public, max-age=30"} {
		if hdr.Get(k) != want {
			t.Errorf("%s: %q, want %q", k, hdr.Get(k), want)
		}
	}
	if csp := hdr.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script") {
		t.Errorf("CSP: %q", csp)
	}
	if strings.Contains(fa, "<script") {
		t.Error("a script on the page")
	}
	_, _, en := m.page("/status?l=en")
	for _, want := range []string{`lang="en" dir="ltr"`, "All systems working", "Dashboard", "Bot", "Payments", "Panels", "Working", `href="/status"`} {
		if !strings.Contains(en, want) {
			t.Errorf("en lacks %q", want)
		}
	}

	// The bot goes down: the verdict changes in both languages and the bot is the part that says so.
	m.advance(time.Minute)
	m.api.status.snap = nil // a fresh look, as 30 seconds later
	h.set("bot", syshealth.StatusDown)
	_, _, st, _ = m.statusJSON()
	if st.String() != "degraded: dashboard=ok bot=down payments=ok panels=ok" {
		t.Fatalf("bot down: %v", st)
	}
	_, _, en = m.page("/status?l=en")
	if !strings.Contains(en, "Some systems are down") || !strings.Contains(en, `class="verdict degraded"`) || !strings.Contains(en, "Down") {
		t.Errorf("en, bot down:\n%s", en)
	}
	_, _, fa = m.page("/status")
	if !strings.Contains(fa, "برخی سیستم") || !strings.Contains(fa, "از کار افتاده") {
		t.Errorf("fa, bot down:\n%s", fa)
	}

	// Nothing of how the store is built, in either answer.
	for _, body := range []string{fa, en} {
		for _, leak := range []string{"Secret-Panel", "secret.example", "id-7", "9.9.9", "0.7.0", "provisioner", "latency", "database"} {
			if strings.Contains(body, leak) {
				t.Errorf("%q on the page", leak)
			}
		}
	}
	_, _, _, raw := m.statusJSON()
	for _, leak := range []string{"Secret-Panel", "secret.example", "id-7", "9.9.9", "0.7.0", "provisioner", "latency", "database"} {
		if strings.Contains(raw, leak) {
			t.Errorf("%q in %s", leak, raw)
		}
	}
}

// The branding of the store is on the page, a colour that is not a colour is not, and a name with markup is text.
func TestStatusPageCarriesTheStoresBranding(t *testing.T) {
	m := newMonFixture(t)
	m.turnStatusPage(true) // makes the owner too
	if _, err := m.dom.SetSettings(context.Background(), actor0(m.fixture), map[string]string{"branding.name": "Beaver <b>VPN</b>", "branding.color": "#112233",
		"branding.support": "@beaver_support"}); err != nil {
		t.Fatal(err)
	}
	_, _, en := m.page("/status?l=en")
	if !strings.Contains(en, "--brand:#112233") || !strings.Contains(en, "Beaver &lt;b&gt;VPN&lt;/b&gt;") || strings.Contains(en, "<b>VPN") ||
		!strings.Contains(en, `href="https://t.me/beaver_support"`) {
		t.Errorf("branding:\n%s", en)
	}
	// A colour that is not a colour never reaches the stylesheet, however it got into the setting.
	m.api.status.snap = nil
	if err := m.st.SetSetting(context.Background(), m.st.Conn(), "branding.color", json.RawMessage(`"red; background:url(x)"`)); err != nil {
		t.Fatal(err)
	}
	if _, _, en := m.page("/status?l=en"); strings.Contains(en, "url(x)") || !strings.Contains(en, "--brand:"+defaultColor) {
		t.Errorf("a colour that is not a colour:\n%s", en)
	}
}

// A crowd costs one look at the stack: for 30 seconds every visitor gets the same answer, then the next one looks again.
func TestStatusPageIsCachedForThirtySeconds(t *testing.T) {
	m := newMonFixture(t)
	h := &statusHealth{}
	m.withSources(h)
	m.turnStatusPage(true)
	var skew atomic.Int64
	m.api.now = func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }
	t.Cleanup(func() { m.api.now = time.Now })

	_, _, first, _ := m.statusJSON()
	if first.Status != "ok" || h.count() != 1 {
		t.Fatalf("first: %v after %d looks", first, h.count())
	}
	// The services change and the monitor's own short memory (10 seconds) is over, but the page is held for 30.
	h.set("payments", syshealth.StatusDown)
	m.advance(15 * time.Second)
	skew.Add(int64(15 * time.Second))
	for i := 0; i < 25; i++ {
		_, _, again, _ := m.statusJSON()
		if again.String() != first.String() || again.UpdatedAt != first.UpdatedAt {
			t.Fatalf("visitor %d got a new answer: %v", i, again)
		}
	}
	_, _, en := m.page("/status?l=en")
	if !strings.Contains(en, "All systems working") {
		t.Error("the page was drawn again inside the 30 seconds")
	}
	if h.count() != 1 {
		t.Fatalf("%d looks inside 30 seconds", h.count())
	}
	// Past 30 seconds the next visitor looks again.
	m.advance(20 * time.Second)
	skew.Add(int64(20 * time.Second))
	_, _, next, _ := m.statusJSON()
	if next.String() != "degraded: dashboard=ok bot=ok payments=down" || next.UpdatedAt <= first.UpdatedAt || h.count() != 2 {
		t.Errorf("after 35 seconds: %v after %d looks", next, h.count())
	}
}

// One client address may ask 60 times a minute; the 61st is told to wait, and another address is not affected.
func TestStatusPageIsRateLimited(t *testing.T) {
	m := newMonFixture(t)
	m.turnStatusPage(true)
	for i := 0; i < statusRate; i++ {
		if code, _, _ := m.pageFrom("/status.json", "203.0.113.50", ""); code != 200 {
			t.Fatalf("request %d: %d", i+1, code)
		}
	}
	code, h, _ := m.pageFrom("/status.json", "203.0.113.50", "")
	if code != http.StatusTooManyRequests || h.Get("Retry-After") != "60" {
		t.Errorf("the 61st: %d %v", code, h)
	}
	if code, _, _ := m.pageFrom("/status", "203.0.113.50", ""); code != http.StatusTooManyRequests {
		t.Errorf("the page shares the budget: %d", code)
	}
	if code, _, _ := m.pageFrom("/status.json", "203.0.113.51", ""); code != 200 {
		t.Errorf("another address: %d", code)
	}
}

// ---- the uptime endpoint

func TestMonitorUptimeEndpoint(t *testing.T) {
	m := newMonFixture(t)
	owner := m.login(ownerTG, "owner")

	// Nothing recorded: the services core asks are not there either (it has no health check here), the lists are empty arrays.
	code, out := owner.do(http.MethodGet, "/api/v1/monitor/uptime", nil)
	if code != 200 || out["retention_days"] != float64(30) || out["sample_every_s"] != float64(30) || out["since"] != nil || out["status_page"] != false {
		t.Fatalf("empty: %d %v", code, out)
	}
	if s, ok := out["services"].([]any); !ok || len(s) != 0 {
		t.Errorf("services: %v", out["services"])
	}
	if p, ok := out["panels"].([]any); !ok || len(p) != 0 {
		t.Errorf("panels: %v", out["panels"])
	}
	if w, _ := out["windows"].([]any); len(w) != 3 {
		t.Errorf("windows: %v", out["windows"])
	}

	// 20 slots of core, the last two down.
	now := m.now()
	for i := 0; i < 20; i++ {
		v := 1.0
		if i >= 18 {
			v = 0
		}
		at := now.Add(-time.Duration(20-i) * 30 * time.Second)
		if _, err := m.st.RecordSamples(context.Background(), at, []store.MonitorSample{{Name: "service.up:core", Value: v}}); err != nil {
			t.Fatal(err)
		}
	}
	m.turnStatusPage(true)
	_, out = owner.do(http.MethodGet, "/api/v1/monitor/uptime", nil)
	services, _ := out["services"].([]any)
	if len(services) != 1 || out["since"] == nil || out["status_page"] != true {
		t.Fatalf("with samples: %v", out)
	}
	core, _ := services[0].(map[string]any)
	if core["id"] != "core" || core["name"] != "core" || core["enabled"] != nil {
		t.Errorf("core: %v", core)
	}
	day, _ := core["windows"].(map[string]any)["24h"].(map[string]any)
	if day["availability_pct"] != float64(90) || day["outages"] != float64(1) || day["down_s"] != float64(60) || day["longest_s"] != float64(60) ||
		day["observed_s"] != float64(600) || day["window_s"] != float64(86400) {
		t.Errorf("24h: %v", day)
	}
	if bar, _ := day["bar"].([]any); len(bar) != 48 {
		t.Errorf("24h bar: %d cells", len(bar))
	}
	if bar, _ := core["windows"].(map[string]any)["30d"].(map[string]any)["bar"].([]any); len(bar) != 60 {
		t.Errorf("30d bar: %d cells", len(bar))
	}
}

func TestMonitorUptimeIsForOwnerAndAdminOnly(t *testing.T) {
	m := newMonFixture(t)
	if code, _ := m.browser().do(http.MethodGet, "/api/v1/monitor/uptime", nil); code != http.StatusUnauthorized {
		t.Errorf("without a session: %d", code)
	}
	if code, _ := m.login(7201, "support").do(http.MethodGet, "/api/v1/monitor/uptime", nil); code != http.StatusForbidden {
		t.Errorf("support: %d", code)
	}
	if code, _ := m.login(7202, "admin").do(http.MethodGet, "/api/v1/monitor/uptime", nil); code != 200 {
		t.Errorf("admin: %d", code)
	}
	m.api.cfg.Monitor = nil
	if code, out := m.login(ownerTG, "owner").do(http.MethodGet, "/api/v1/monitor/uptime", nil); code != http.StatusServiceUnavailable || out["error"] != "not_configured" {
		t.Errorf("no monitor: %d %v", code, out)
	}
}

// The settings page can switch the status page, and an admin may (it is a setting, not the server's own business).
func TestStatusPageSettingIsInTheSettingsList(t *testing.T) {
	m := newMonFixture(t)
	owner := m.login(ownerTG, "owner")
	code, out := owner.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{domain.SettingStatusPage: "true"}})
	if code != 200 {
		t.Fatalf("put: %d %v", code, out)
	}
	if code, _, _ := m.page("/status.json"); code != 200 {
		t.Errorf("after the owner turned it on: %d", code)
	}
	if code, _ := owner.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{domain.SettingStatusPage: "maybe"}}); code != http.StatusBadRequest {
		t.Errorf("a value that is not true or false: %d", code)
	}
}
