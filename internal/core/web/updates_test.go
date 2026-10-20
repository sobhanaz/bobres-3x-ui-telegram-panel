package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
)

// System > Updates: the state, "Check again", the channel and "Update now".
// The temp directory stands in for the folder core shares with the server's
// updater, and updRelease for the bot.

type updRelease struct {
	mu       sync.Mutex
	check    domain.UpdateCheck
	err      error
	nowCheck *domain.UpdateCheck // what a look finds now (nil: what there is)
	looks    int
}

func (u *updRelease) CheckUpdates(context.Context) (*domain.UpdateCheck, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.err != nil {
		return nil, u.err
	}
	c := u.check
	return &c, nil
}

func (u *updRelease) CheckUpdatesNow(ctx context.Context) (*domain.UpdateCheck, error) {
	u.mu.Lock()
	u.looks++
	if u.nowCheck != nil && u.err == nil {
		u.check = *u.nowCheck
	}
	u.mu.Unlock()
	return u.CheckUpdates(ctx)
}

func (u *updRelease) looked() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.looks
}

type updFixture struct {
	*fixture
	owner *browser
	src   *updRelease
	dir   string
	up    *domain.Updates
}

func newUpdFixture(t *testing.T) *updFixture {
	t.Helper()
	f, b, _ := setupFixture(t)
	u := &updFixture{fixture: f, owner: b, dir: t.TempDir(),
		src: &updRelease{check: domain.UpdateCheck{Current: "0.6.1", Latest: "0.7.0", Channel: "stable", Issued: "2026-10-20T10:00Z", Newer: true, Verified: true, CheckedAt: 1760000000}}}
	u.up = f.dom.NewUpdates(domain.UpdatesConfig{Source: u.src, Helper: sysupdate.NewDir(u.dir), Version: "0.6.1"})
	f.api.cfg.Updates = u.up
	u.status(nil)
	return u
}

func (u *updFixture) status(over map[string]any) {
	u.t.Helper()
	m := map[string]any{"v": 1, "installed": true, "helper": "0.7.0", "state": "idle", "id": "", "from": "", "to": "", "channel": "", "step": "",
		"message": "", "started_at": "", "finished_at": "", "log": []string{}}
	for k, v := range over {
		m[k] = v
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(u.dir, sysupdate.StatusFile), raw, 0o600); err != nil {
		u.t.Fatal(err)
	}
}

func (u *updFixture) login(tg int64, role string) *browser {
	u.t.Helper()
	u.staffUser(tg, role)
	b := u.browser()
	if code := b.loginWithLink(tg); code != 200 {
		u.t.Fatalf("login %s: %d", role, code)
	}
	return b
}

func (u *updFixture) hasRequest() bool {
	_, err := os.Stat(filepath.Join(u.dir, sysupdate.RequestFile))
	return err == nil
}

// notices are the SystemUpdate events written since this fixture began.
func (u *updFixture) notices() []events.SystemUpdateEvent {
	u.t.Helper()
	rows, err := u.st.DB().Query(context.Background(), `SELECT payload FROM core.outbox_core WHERE topic = $1 ORDER BY id`, events.SystemUpdate)
	if err != nil {
		u.t.Fatal(err)
	}
	defer rows.Close()
	var out []events.SystemUpdateEvent
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			u.t.Fatal(err)
		}
		var e events.SystemUpdateEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			u.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (u *updFixture) clean() {
	u.t.Helper()
	if _, err := u.st.DB().Exec(context.Background(), `TRUNCATE core.outbox_core CASCADE`); err != nil {
		u.t.Fatal(err)
	}
}

func applyBody(over map[string]any) map[string]any {
	m := map[string]any{"version": "0.7.0", "channel": "stable", "confirm": true}
	for k, v := range over {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return m
}

func TestUpdatesState(t *testing.T) {
	u := newUpdFixture(t)
	code, out := u.owner.do(http.MethodGet, "/api/v1/system/updates", nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	check := obj(out["check"])
	if out["version"] != "0.6.1" || check["current"] != "0.6.1" || check["latest"] != "0.7.0" || check["newer"] != true || check["verified"] != true ||
		check["channel"] != "stable" || check["issued"] != "2026-10-20T10:00Z" || check["checked_at"] != float64(1760000000) || check["error"] != "" ||
		check["prerelease"] != false || check["notes_url"] != "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.7.0" {
		t.Fatalf("check: %v", check)
	}
	if _, has := check["covered"]; has {
		t.Errorf("a license that says nothing gave an answer: %v", check)
	}
	if ch := obj(out["channel"]); ch["setting"] != "" || ch["effective"] != "stable" || ch["source"] != "server" {
		t.Fatalf("channel: %v", ch)
	}
	helper := obj(out["helper"])
	if helper["available"] != true || helper["problem"] != "" || helper["version"] != "0.7.0" || helper["state"] != "idle" || helper["id"] != "" ||
		helper["started_at"] != nil || helper["finished_at"] != nil {
		t.Fatalf("helper: %v", helper)
	}
	if log, ok := helper["log"].([]any); !ok || len(log) != 0 {
		t.Fatalf("the owner's log: %v", helper["log"])
	}
	if out["run"] != nil || out["last"] != nil {
		t.Fatalf("run %v, last %v", out["run"], out["last"])
	}
	if ap := obj(out["apply"]); ap["allowed"] != true || ap["ready"] != true || ap["reason"] != "" || ap["target"] != "0.7.0" {
		t.Fatalf("apply: %v", ap)
	}

	// an admin reads the state, with no log and no way to update
	adm := u.login(8801, "admin")
	_, out = adm.do(http.MethodGet, "/api/v1/system/updates", nil)
	if _, has := obj(out["helper"])["log"]; has {
		t.Errorf("an admin saw the updater's log: %v", out["helper"])
	}
	if ap := obj(out["apply"]); ap["allowed"] != false || ap["ready"] != false || ap["target"] != "0.7.0" {
		t.Fatalf("admin apply: %v", ap)
	}
	if obj(out["check"])["latest"] != "0.7.0" {
		t.Fatalf("admin check: %v", out["check"])
	}
	// support sees nothing of it, nor a visitor without a session
	sup := u.login(8802, "support")
	for _, p := range []string{"/api/v1/system/updates", "/api/v1/system/updates/progress"} {
		if code, _ := sup.do(http.MethodGet, p, nil); code != 403 {
			t.Errorf("support %s: %d", p, code)
		}
		if code, _ := u.browser().do(http.MethodGet, p, nil); code != 401 {
			t.Errorf("visitor %s: %d", p, code)
		}
	}
}

// What the tab says follows the bot's answer: a release that is a pre-release,
// one the license does not cover, the bot not answering, nothing found yet.
func TestUpdatesStateFollowsTheBot(t *testing.T) {
	u := newUpdFixture(t)
	get := func() map[string]any {
		t.Helper()
		code, out := u.owner.do(http.MethodGet, "/api/v1/system/updates", nil)
		if code != 200 {
			t.Fatalf("%d %v", code, out)
		}
		return out
	}
	u.src.check = domain.UpdateCheck{Current: "0.6.1", Latest: "0.7.0-rc.1", Channel: "beta", Newer: true, Verified: true, CheckedAt: 1760000000}
	out := get()
	if c := obj(out["check"]); c["prerelease"] != true || c["notes_url"] != "https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/tag/v0.7.0-rc.1" ||
		obj(out["channel"])["effective"] != "beta" || obj(out["apply"])["target"] != "0.7.0-rc.1" {
		t.Fatalf("beta: %v", out)
	}

	// the bot does not answer
	u.src.err = errors.New("bot down")
	out = get()
	if c := obj(out["check"]); c["error"] != "bot unreachable" || c["current"] != "0.6.1" || c["latest"] != "" || c["verified"] != false {
		t.Fatalf("bot down: %v", c)
	}
	if ap := obj(out["apply"]); ap["ready"] != false || ap["reason"] != "check_unavailable" || ap["target"] != "" {
		t.Fatalf("bot down apply: %v", ap)
	}
	u.src.err = nil

	// nothing known: GitHub did not answer; no release to update to, the failure says why
	u.src.check = domain.UpdateCheck{Current: "0.6.1", Channel: "stable", Error: "unreachable", Detail: "checksums.txt: Get \"https://github.com/x\": i/o timeout", CheckedAt: 1760000000}
	out = get()
	if c := obj(out["check"]); c["error"] != "unreachable" || c["detail"] == "" || c["latest"] != "" || c["notes_url"] != nil {
		t.Fatalf("unreachable: %v", c)
	}
	if ap := obj(out["apply"]); ap["reason"] != "no_release" || ap["ready"] != false {
		t.Fatalf("unreachable apply: %v", ap)
	}

	// up to date
	u.src.check = domain.UpdateCheck{Current: "0.6.1", Latest: "0.6.1", Channel: "stable", Verified: true, CheckedAt: 1760000000}
	if ap := obj(get()["apply"]); ap["reason"] != "not_newer" || ap["ready"] != false || ap["target"] != "" {
		t.Fatalf("up to date: %v", ap)
	}

	// what the bot says about a release is never taken as a link or as markup
	u.src.check = domain.UpdateCheck{Current: "0.6.1", Latest: "0.7.0\"><script>", Channel: "stable", Verified: true, CheckedAt: 1760000000,
		Error: "bad_signature", Detail: "line\none\u202e\x00 " + strings.Repeat("d", 500)}
	c := obj(get()["check"])
	if c["notes_url"] != nil || c["prerelease"] != false {
		t.Fatalf("a link from a latest that is no release: %v", c)
	}
	if d, _ := c["detail"].(string); len([]rune(d)) > 300 || strings.ContainsAny(d, "\n\x00\u202e") {
		t.Fatalf("detail: %q", d)
	}

	// the updater's states
	u.src.check = domain.UpdateCheck{Current: "0.6.1", Latest: "0.7.0", Channel: "stable", Newer: true, Verified: true, CheckedAt: 1760000000}
	for name, c := range map[string]struct {
		prep    func()
		problem string
		reason  string
	}{
		"not installed": {func() { u.status(map[string]any{"installed": false}) }, "not_installed", "helper_missing"},
		"no status":     {func() { _ = os.Remove(filepath.Join(u.dir, sysupdate.StatusFile)) }, "no_status", "helper_missing"},
		"garbage":       {func() { _ = os.WriteFile(filepath.Join(u.dir, sysupdate.StatusFile), []byte("garbage"), 0o600) }, "bad_status", "helper_missing"},
		"another protocol": {func() {
			u.status(map[string]any{"v": 2})
		}, "unsupported", "helper_missing"},
		"no folder": {func() { u.up2(filepath.Join(u.dir, "gone")) }, "no_dir", "helper_missing"},
	} {
		c.prep()
		out := get()
		h := obj(out["helper"])
		if h["available"] != false || h["problem"] != c.problem || obj(out["apply"])["reason"] != c.reason || obj(out["apply"])["ready"] != false {
			t.Errorf("%s: helper %v apply %v", name, h, out["apply"])
		}
		u.status(nil)
		u.up2(u.dir)
	}
	// running
	u.status(map[string]any{"state": "running", "id": "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99", "step": "pull", "from": "0.6.1", "to": "0.7.0",
		"started_at": time.Now().UTC().Format(time.RFC3339)})
	out = get()
	if obj(out["helper"])["state"] != "running" || obj(out["apply"])["reason"] != "already_running" {
		t.Fatalf("running: %v", out)
	}
}

// up2 points the update service at another folder.
func (u *updFixture) up2(dir string) {
	u.up = u.dom.NewUpdates(domain.UpdatesConfig{Source: u.src, Helper: sysupdate.NewDir(dir), Version: "0.6.1"})
	u.api.cfg.Updates = u.up
}

func TestUpdatesNotConfigured(t *testing.T) {
	u := newUpdFixture(t)
	u.api.cfg.Updates = nil
	for _, c := range []struct{ m, p string }{{"GET", "/api/v1/system/updates"}, {"GET", "/api/v1/system/updates/progress"},
		{"POST", "/api/v1/system/updates/check"}, {"PUT", "/api/v1/system/updates/channel"}, {"POST", "/api/v1/system/updates/apply"}} {
		code, out := u.owner.do(c.m, c.p, map[string]any{"channel": "beta", "confirm": true, "version": "0.7.0"})
		if code != 503 || out["error"] != "not_configured" {
			t.Errorf("%s %s: %d %v", c.m, c.p, code, out)
		}
	}
}

func TestUpdatesRoutesAreGuarded(t *testing.T) {
	u := newUpdFixture(t)
	adm := u.login(8811, "admin")
	sup := u.login(8812, "support")
	anon := u.browser()
	type route struct {
		method, path string
		body         any
		write        bool
	}
	routes := []route{
		{"GET", "/api/v1/system/updates", nil, false},
		{"GET", "/api/v1/system/updates/progress", nil, false},
		{"POST", "/api/v1/system/updates/check", map[string]any{}, true},
		{"PUT", "/api/v1/system/updates/channel", map[string]any{"channel": "beta"}, true},
		{"POST", "/api/v1/system/updates/apply", applyBody(nil), true},
	}
	for _, r := range routes {
		if code, _ := anon.do(r.method, r.path, r.body); code != 401 {
			t.Errorf("visitor %s %s: %d", r.method, r.path, code)
		}
		if code, _ := sup.do(r.method, r.path, r.body); code != 403 {
			t.Errorf("support %s %s: %d", r.method, r.path, code)
		}
		code, _ := adm.do(r.method, r.path, r.body)
		if want := map[bool]int{false: 200, true: 403}[r.write]; code != want {
			t.Errorf("admin %s %s: %d, want %d", r.method, r.path, code, want)
		}
		if r.write {
			// the owner's session without its CSRF token does nothing
			csrf := u.owner.csrf
			u.owner.csrf = ""
			if code, out := u.owner.do(r.method, r.path, r.body); code != 403 || out["error"] != "csrf" {
				t.Errorf("owner %s %s without the CSRF token: %d %v", r.method, r.path, code, out)
			}
			u.owner.csrf = "wrong"
			if code, _ := u.owner.do(r.method, r.path, r.body); code != 403 {
				t.Errorf("owner %s %s with a wrong CSRF token: %d", r.method, r.path, code)
			}
			u.owner.csrf = csrf
		}
	}
	// none of those refusals did anything
	if u.hasRequest() || u.src.looked() != 0 {
		t.Fatalf("a refused call acted: request %v, looks %d", u.hasRequest(), u.src.looked())
	}
	if v, _ := u.dom.Settings(context.Background()); v["updates.channel"] != "" {
		t.Fatalf("the channel was set: %v", v)
	}
}

func TestUpdatesCheckAgain(t *testing.T) {
	u := newUpdFixture(t)
	// "Check again" makes the bot look, and shows what it found
	found := domain.UpdateCheck{Current: "0.6.1", Latest: "0.7.1", Channel: "stable", Issued: "2026-10-21T08:00Z", Newer: true, Verified: true, CheckedAt: 1760001000}
	u.src.nowCheck = &found
	code, out := u.owner.do(http.MethodPost, "/api/v1/system/updates/check", map[string]any{})
	if code != 200 || obj(out["check"])["latest"] != "0.7.1" || obj(out["check"])["checked_at"] != float64(1760001000) || u.src.looked() != 1 ||
		obj(out["apply"])["target"] != "0.7.1" {
		t.Fatalf("%d %v (looks %d)", code, out, u.src.looked())
	}
	// a look that failed says so, with its reason
	u.src.nowCheck = &domain.UpdateCheck{Current: "0.6.1", Channel: "stable", Error: "unreachable", Detail: "timeout", CheckedAt: 1760002000}
	code, out = u.owner.do(http.MethodPost, "/api/v1/system/updates/check", map[string]any{})
	if c := obj(out["check"]); code != 200 || c["error"] != "unreachable" || c["detail"] != "timeout" || c["checked_at"] != float64(1760002000) {
		t.Fatalf("a failed look: %d %v", code, out)
	}
	// a bot that does not answer
	u.src.err = errors.New("bot down")
	code, out = u.owner.do(http.MethodPost, "/api/v1/system/updates/check", map[string]any{})
	if c := obj(out["check"]); code != 200 || c["error"] != "bot unreachable" {
		t.Fatalf("bot down: %d %v", code, out)
	}
	u.src.err = nil
	// at most ten a minute
	var last int
	for i := 0; i < 12; i++ {
		last, _ = u.owner.do(http.MethodPost, "/api/v1/system/updates/check", map[string]any{})
		if last == http.StatusTooManyRequests {
			if i < 6 {
				t.Fatalf("limited after %d more", i+3)
			}
			break
		}
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("not limited: %d", last)
	}
}

func TestUpdatesChannel(t *testing.T) {
	u := newUpdFixture(t)
	beta := domain.UpdateCheck{Current: "0.6.1", Latest: "0.7.0-rc.1", Channel: "beta", Issued: "2026-10-22T08:00Z", Newer: true, Verified: true, CheckedAt: 1760003000}
	u.src.nowCheck = &beta
	code, out := u.owner.do(http.MethodPut, "/api/v1/system/updates/channel", map[string]any{"channel": "beta"})
	ch := obj(out["channel"])
	if code != 200 || ch["setting"] != "beta" || ch["effective"] != "beta" || ch["source"] != "setting" || obj(out["check"])["latest"] != "0.7.0-rc.1" ||
		obj(out["check"])["prerelease"] != true || u.src.looked() != 1 {
		t.Fatalf("%d %v (looks %d)", code, out, u.src.looked())
	}
	// stored as a setting the bot reads with its others, and audited as any setting is
	if v, _ := u.dom.Settings(context.Background()); v["updates.channel"] != "beta" {
		t.Fatalf("settings: %v", v)
	}
	e := lastAudit(t, u.owner, "entity_id=updates.channel")
	if e["action"] != "setting.set" || asJSON(e["before"]) != `{"value":""}` || asJSON(e["after"]) != `{"value":"beta"}` || e["source"] != "dashboard" {
		t.Fatalf("audit: %v", e)
	}
	// the same choice again looks at nothing and changes nothing
	if code, out := u.owner.do(http.MethodPut, "/api/v1/system/updates/channel", map[string]any{"channel": "beta"}); code != 200 || obj(out["channel"])["setting"] != "beta" || u.src.looked() != 1 {
		t.Fatalf("again: %d %v (looks %d)", code, out, u.src.looked())
	}
	// back to the server's default: "", and a new look
	u.src.nowCheck = &domain.UpdateCheck{Current: "0.6.1", Latest: "0.7.0", Channel: "stable", Newer: true, Verified: true, CheckedAt: 1760004000}
	code, out = u.owner.do(http.MethodPut, "/api/v1/system/updates/channel", map[string]any{"channel": ""})
	if ch := obj(out["channel"]); code != 200 || ch["setting"] != "" || ch["source"] != "server" || ch["effective"] != "stable" || u.src.looked() != 2 {
		t.Fatalf("default: %d %v", code, out)
	}
	// a bot that does not answer still lets the choice be saved
	u.src.err = errors.New("bot down")
	code, out = u.owner.do(http.MethodPut, "/api/v1/system/updates/channel", map[string]any{"channel": "stable"})
	if obj(out["channel"])["setting"] != "stable" || obj(out["check"])["error"] != "bot unreachable" || code != 200 {
		t.Fatalf("bot down: %d %v", code, out)
	}
	u.src.err = nil

	// what is no channel is refused, naming the field
	for _, bad := range []any{"nightly", "Beta", "BETA", "stable,beta", "x\ny", "latest", strings.Repeat("b", 2000)} {
		code, out := u.owner.do(http.MethodPut, "/api/v1/system/updates/channel", map[string]any{"channel": bad})
		if code != 400 || out["field"] != "updates.channel" {
			t.Errorf("%q: %d %v", bad, code, out)
		}
	}
	for name, body := range map[string]any{"a number": map[string]any{"channel": 5}, "null channel": map[string]any{"channel": nil}, "no channel": map[string]any{},
		"an extra field": map[string]any{"channel": "beta", "force": true}, "a list": []any{"beta"}} {
		code, _ := u.owner.do(http.MethodPut, "/api/v1/system/updates/channel", body)
		if name == "null channel" || name == "no channel" {
			// an absent or null channel is "" (the server's default), as the setting itself reads it
			if code != 200 {
				t.Errorf("%s: %d", name, code)
			}
			continue
		}
		if code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	// it is not a setting of the Settings page, nor one an admin or the bot's /set gets to change
	if _, out := u.owner.do(http.MethodGet, "/api/v1/settings", nil); settingByKey(out, "updates.channel") != nil || strings.Contains(asJSON(out["groups"]), "updates") {
		t.Fatalf("the channel is on the Settings page: %v", out)
	}
	if code, out := u.owner.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"updates.channel": "beta"}}); code != 400 || out["field"] != "updates.channel" {
		t.Fatalf("saved through the Settings page: %d %v", code, out)
	}
	adm := u.login(8821, "admin")
	if code, _ := adm.do(http.MethodPut, "/api/v1/settings", map[string]any{"values": map[string]string{"updates.channel": "beta"}}); code != 400 && code != 403 {
		t.Fatalf("admin through the Settings page: %d", code)
	}
	if err := u.dom.AdminSetSetting(context.Background(), 8821, "updates.channel", "beta"); err == nil || !strings.Contains(err.Error(), "only the store owner") {
		t.Fatalf("admin /set: %v", err)
	}
}

func TestUpdatesApply(t *testing.T) {
	u := newUpdFixture(t)
	u.clean()
	other := u.notices()
	if len(other) != 0 {
		t.Fatalf("%d notices before", len(other))
	}

	// without confirm nothing is done
	for _, body := range []map[string]any{applyBody(map[string]any{"confirm": false}), applyBody(map[string]any{"confirm": nil})} {
		code, out := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", body)
		if code != 400 || out["error"] != "invalid" || out["code"] != "confirm_required" {
			t.Fatalf("no confirm: %d %v", code, out)
		}
	}
	if u.hasRequest() || len(u.notices()) != 0 {
		t.Fatal("an update that was not confirmed was requested")
	}

	code, out := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil))
	if code != http.StatusAccepted {
		t.Fatalf("apply: %d %v", code, out)
	}
	run := obj(out["run"])
	if run["from"] != "0.6.1" || run["to"] != "0.7.0" || run["channel"] != "stable" || run["phase"] != "queued" || run["outcome"] != "" || run["id"] == "" {
		t.Fatalf("run: %v", run)
	}
	if h := obj(out["helper"]); h["available"] != true {
		t.Fatalf("helper: %v", h)
	}
	// the request the updater reads: the release the owner confirmed, who asked, nothing else
	req, err := sysupdate.NewDir(u.dir).ReadRequest()
	if err != nil || req.ID != run["id"] || req.Version != "0.7.0" || req.Channel != "stable" || req.Action != "update" || req.By == "" {
		t.Fatalf("request: %+v %v", req, err)
	}
	raw, _ := os.ReadFile(filepath.Join(u.dir, sysupdate.RequestFile))
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil || len(asMap) != 7 {
		t.Fatalf("the request carries more than its seven fields: %s", raw)
	}
	// audited, with who, where from and what; and every owner told
	e := lastAudit(t, u.owner, "action=system.update.requested")
	if e["entity"] != "system_update" || e["entity_id"] != run["id"] || e["source"] != "dashboard" || e["ip"] != "127.0.0.1" || e["actor_role"] != "owner" ||
		obj(e["after"])["from"] != "0.6.1" || obj(e["after"])["to"] != "0.7.0" || obj(e["after"])["channel"] != "stable" {
		t.Fatalf("audit: %v", e)
	}
	notices := u.notices()
	if len(notices) != 1 || notices[0].TelegramID != ownerTG || notices[0].State != events.UpdateRequested || notices[0].To != "0.7.0" {
		t.Fatalf("notices: %+v", notices)
	}
	// a second request while this one is open
	code, out = u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil))
	if code != 409 || out["error"] != "state" || out["code"] != "already_running" {
		t.Fatalf("second: %d %v", code, out)
	}
	if len(u.notices()) != 1 {
		t.Fatal("the refused request was announced")
	}
	// the page's state now shows it
	_, out = u.owner.do(http.MethodGet, "/api/v1/system/updates", nil)
	if obj(out["run"])["id"] != run["id"] || obj(out["run"])["phase"] != "queued" || obj(out["apply"])["reason"] != "already_running" || obj(out["apply"])["ready"] != false {
		t.Fatalf("state: %v", out)
	}
}

// Every way an update is refused leaves nothing behind and says why in a code
// the page translates.
func TestUpdatesApplyRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		prep   func(u *updFixture)
		body   map[string]any
		status int
		kind   string
		code   string
	}{
		"the updater is not installed": {func(u *updFixture) { u.status(map[string]any{"installed": false}) }, applyBody(nil), 409, "state", "helper_missing"},
		"the updater is busy": {func(u *updFixture) {
			u.status(map[string]any{"state": "running", "id": "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99", "started_at": time.Now().UTC().Format(time.RFC3339)})
		}, applyBody(nil), 409, "state", "already_running"},
		"another release than the one confirmed": {func(u *updFixture) { u.src.check.Latest = "0.7.1" }, applyBody(nil), 409, "state", "version_changed"},
		"another channel":                        {func(*updFixture) {}, applyBody(map[string]any{"channel": "beta"}), 409, "state", "channel_changed"},
		"no version":                             {func(*updFixture) {}, applyBody(map[string]any{"version": nil}), 409, "state", "version_changed"},
		"a version nobody released":              {func(*updFixture) {}, applyBody(map[string]any{"version": "9.9.9"}), 409, "state", "version_changed"},
		"nothing newer":                          {func(u *updFixture) { u.src.check.Latest = "0.6.1" }, applyBody(map[string]any{"version": "0.6.1"}), 409, "state", "not_newer"},
		"no release is verified": {func(u *updFixture) {
			u.src.check = domain.UpdateCheck{Current: "0.6.1", Channel: "stable", Error: "unreachable"}
		}, applyBody(nil), 409, "state", "no_release"},
		"the bot is down": {func(u *updFixture) { u.src.err = errors.New("down") }, applyBody(nil), 503, "unavailable", "check_unavailable"},
		"a pre-release without its yes": {func(u *updFixture) {
			u.src.check = domain.UpdateCheck{Current: "0.6.1", Latest: "0.7.0-rc.1", Channel: "beta", Newer: true, Verified: true}
		}, applyBody(map[string]any{"version": "0.7.0-rc.1", "channel": "beta"}), 400, "invalid", "prerelease_unconfirmed"},
	} {
		t.Run(name, func(t *testing.T) {
			u := newUpdFixture(t)
			c.prep(u)
			code, out := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", c.body)
			if code != c.status || out["error"] != c.kind || out["code"] != c.code || out["message"] == "" {
				t.Fatalf("%d %v", code, out)
			}
			if u.hasRequest() {
				t.Fatal("a request was written")
			}
			if _, err := u.st.OpenSystemUpdate(context.Background(), u.st.Conn()); err == nil {
				t.Fatal("a refused update was recorded")
			}
		})
	}
	t.Run("version_changed names the newest release", func(t *testing.T) {
		u := newUpdFixture(t)
		u.src.check.Latest = "0.7.1"
		_, out := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil))
		if out["latest"] != "0.7.1" {
			t.Fatalf("%v", out)
		}
	})
	t.Run("a pre-release with its yes", func(t *testing.T) {
		u := newUpdFixture(t)
		u.src.check = domain.UpdateCheck{Current: "0.6.1", Latest: "0.7.0-rc.1", Channel: "beta", Newer: true, Verified: true}
		code, out := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(map[string]any{"version": "0.7.0-rc.1", "channel": "beta", "accept_prerelease": true}))
		if code != http.StatusAccepted {
			t.Fatalf("%d %v", code, out)
		}
		if req, err := sysupdate.NewDir(u.dir).ReadRequest(); err != nil || req.Channel != "beta" || req.Version != "0.7.0-rc.1" {
			t.Fatalf("%+v %v", req, err)
		}
	})
	t.Run("a request the page makes badly", func(t *testing.T) {
		u := newUpdFixture(t)
		for name, body := range map[string]any{"an unknown field": applyBody(map[string]any{"url": "https://evil.example/bobres"}), "a flag": applyBody(map[string]any{"flags": "--no-verify"}),
			"confirm as text": applyBody(map[string]any{"confirm": "yes"}), "version as a number": applyBody(map[string]any{"version": 7}), "a list": []any{1}} {
			if code, out := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", body); code != 400 {
				t.Errorf("%s: %d %v", name, code, out)
			}
		}
		if u.hasRequest() {
			t.Fatal("a request was written")
		}
	})
}

// The same limit as the other costly actions: ten tries a minute.
func TestUpdatesApplyIsRateLimited(t *testing.T) {
	u := newUpdFixture(t)
	u.src.check.Latest = "0.6.1" // every try is refused, quickly
	var limited bool
	for i := 0; i < 14; i++ {
		code, _ := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(map[string]any{"version": "0.6.1"}))
		if code == http.StatusTooManyRequests {
			limited = i >= 10
			break
		}
	}
	if !limited {
		t.Fatal("not limited after ten tries")
	}
}

// An owner with a password login and an authenticator app proves who is there
// with a current code, as for replacing the password; one who came in with a
// fresh login link is not asked; a wrong code counts towards the lock.
func TestUpdatesApplyAsksForTheAuthenticatorCode(t *testing.T) {
	u := newUpdFixture(t)
	secret := confirmPassword(t, u.owner, "boss")
	pass := u.browser()
	c1, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now()))
	if code, _ := pass.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "a long password", "code": c1}); code != 200 {
		t.Fatalf("password login: %d", code)
	}
	if code, out := pass.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil)); code != 403 || out["error"] != "code_required" {
		t.Fatalf("no code: %d %v", code, out)
	}
	if u.hasRequest() {
		t.Fatal("a request was written without the code")
	}
	if code, out := pass.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(map[string]any{"code": "000000"})); code != 400 || out["error"] != "code_wrong" {
		t.Fatalf("a wrong code: %d %v", code, out)
	}
	if u.hasRequest() {
		t.Fatal("a request was written with a wrong code")
	}
	c2, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now())+1)
	if code, out := pass.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(map[string]any{"code": c2})); code != http.StatusAccepted {
		t.Fatalf("the right code: %d %v", code, out)
	}
	if !u.hasRequest() {
		t.Fatal("no request after the right code")
	}

	// a fresh login link is fresh proof of the Telegram account: no code for ten minutes
	u2 := newUpdFixture(t)
	confirmPassword(t, u2.owner, "boss")
	fresh := u2.browser()
	token, _, _ := u2.dom.CreateLoginLink(context.Background(), ownerTG)
	if code, _ := fresh.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": token}); code != 200 {
		t.Fatalf("link login: %d", code)
	}
	if code, out := fresh.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil)); code != http.StatusAccepted {
		t.Fatalf("a fresh link: %d %v", code, out)
	}
	// and after the window it is asked again
	u3 := newUpdFixture(t)
	confirmPassword(t, u3.owner, "boss")
	old := u3.browser()
	token, _, _ = u3.dom.CreateLoginLink(context.Background(), ownerTG)
	_, _ = old.do(http.MethodPost, "/api/v1/auth/link", map[string]string{"token": token})
	u3.api.now = func() time.Time { return time.Now().Add(11 * time.Minute) }
	defer func() { u3.api.now = time.Now }()
	if code, out := old.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil)); code != 403 || out["error"] != "code_required" {
		t.Fatalf("after the window: %d %v", code, out)
	}
}

// The folder is the updater's: when core cannot write a request into it the
// update is recorded as failed and the owner is told so, plainly.
func TestUpdatesApplyWhenTheFolderCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	u := newUpdFixture(t)
	u.clean()
	if err := os.Chmod(u.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(u.dir, 0o700) }()
	code, out := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil))
	if code != 503 || out["error"] != "unavailable" || out["code"] != "request_failed" {
		t.Fatalf("%d %v", code, out)
	}
	last, err := u.st.LastSystemUpdate(context.Background(), u.st.Conn())
	if err != nil || last.Outcome != events.UpdateFailed {
		t.Fatalf("last: %+v %v", last, err)
	}
	if _, err := u.st.OpenSystemUpdate(context.Background(), u.st.Conn()); err == nil {
		t.Fatal("the failed request is still open")
	}
	// made by the system, as a consequence of what the owner asked in the dashboard
	e := lastAudit(t, u.owner, "action=system.update.failed")
	if e["actor"] != nil || e["source"] != "dashboard" {
		t.Fatalf("audit: %v", e)
	}
	var states []string
	for _, n := range u.notices() {
		states = append(states, n.State)
	}
	if strings.Join(states, ",") != "requested,failed" {
		t.Fatalf("notices: %v", states)
	}
}

// The light state the page polls while an update runs, and after core came
// back from restarting in the middle of it.
func TestUpdatesProgress(t *testing.T) {
	u := newUpdFixture(t)
	u.clean()
	code, out := u.owner.do(http.MethodGet, "/api/v1/system/updates/progress", nil)
	if code != 200 || out["version"] != "0.6.1" || out["run"] != nil || out["last"] != nil || obj(out["helper"])["state"] != "idle" {
		t.Fatalf("idle: %d %v", code, out)
	}
	if _, has := out["check"]; has {
		t.Fatal("the light state asked the bot")
	}
	_, applied := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil))
	id, _ := obj(applied["run"])["id"].(string)
	if id == "" {
		t.Fatalf("apply: %v", applied)
	}

	// the bot is restarting too: the state still answers, at once
	u.src.err = errors.New("bot down")
	start := time.Now()
	_, out = u.owner.do(http.MethodGet, "/api/v1/system/updates/progress", nil)
	if time.Since(start) > 2*time.Second || obj(out["run"])["phase"] != "queued" {
		t.Fatalf("queued: %v (%s)", out, time.Since(start))
	}

	// the updater takes it up, and goes through its steps
	u.status(map[string]any{"state": "running", "id": id, "from": "0.6.1", "to": "0.7.0", "channel": "stable", "step": "backup",
		"message": "Backing up the database.", "started_at": time.Now().UTC().Format(time.RFC3339),
		"log": []string{"verified the release signature", "backup written: 12.4 MB"}})
	_, out = u.owner.do(http.MethodGet, "/api/v1/system/updates/progress", nil)
	h := obj(out["helper"])
	if obj(out["run"])["phase"] != "running" || h["step"] != "backup" || h["message"] != "Backing up the database." || h["state"] != "running" ||
		asJSON(h["log"]) != `["verified the release signature","backup written: 12.4 MB"]` || h["started_at"] == nil {
		t.Fatalf("running: %v", out)
	}
	// an admin sees that an update runs and where, not its log
	adm := u.login(8831, "admin")
	_, out = adm.do(http.MethodGet, "/api/v1/system/updates/progress", nil)
	if _, has := obj(out["helper"])["log"]; has || obj(out["helper"])["step"] != "backup" {
		t.Fatalf("admin: %v", out)
	}

	// it ends: the worker (here one step of it) ends the update once
	u.status(map[string]any{"state": "rolled_back", "id": id, "from": "0.6.1", "to": "0.7.0", "channel": "stable", "step": "rollback",
		"message": "The new version did not become healthy; went back to 0.6.1.", "started_at": time.Now().UTC().Format(time.RFC3339), "finished_at": time.Now().UTC().Format(time.RFC3339)})
	for i := 0; i < 3; i++ {
		if err := u.up.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	_, out = u.owner.do(http.MethodGet, "/api/v1/system/updates/progress", nil)
	last := obj(out["last"])
	if out["run"] != nil || last["id"] != id || last["outcome"] != "rolled_back" || last["outcome_at"] == nil || !strings.Contains(last["message"].(string), "went back to 0.6.1") {
		t.Fatalf("ended: %v", out)
	}
	e := lastAudit(t, u.owner, "action=system.update.rolled_back")
	if e["actor"] != nil || e["entity_id"] != id || e["source"] != "system" || obj(e["after"])["to"] != "0.7.0" {
		t.Fatalf("audit: %v", e)
	}
	var states []string
	for _, n := range u.notices() {
		states = append(states, n.State)
	}
	if strings.Join(states, ",") != "requested,rolled_back" {
		t.Fatalf("notices: %v", states)
	}
	// the full state shows it as the last update, and an update may be asked for again
	u.src.err = nil
	_, out = u.owner.do(http.MethodGet, "/api/v1/system/updates", nil)
	if obj(out["last"])["outcome"] != "rolled_back" || obj(out["apply"])["ready"] != true || out["run"] != nil {
		t.Fatalf("state after: %v", out)
	}
}

// Whatever sits at status.json, core shows it as the updater being unusable and
// never as what the file says.
func TestUpdatesProgressNeverTrustsTheStatusFile(t *testing.T) {
	u := newUpdFixture(t)
	statusPath := filepath.Join(u.dir, sysupdate.StatusFile)
	for name, put := range map[string]func(){
		"a link to a good status": func() {
			other := filepath.Join(t.TempDir(), "status.json")
			_ = os.WriteFile(other, []byte(`{"v":1,"installed":true,"state":"running","id":"0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99","message":"from elsewhere"}`), 0o600)
			_ = os.Symlink(other, statusPath)
		},
		"a file far too big": func() {
			_ = os.WriteFile(statusPath, []byte(`{"v":1,"installed":true,"state":"idle","pad":"`+strings.Repeat("x", 200000)+`"}`), 0o600)
		},
		"garbage":  func() { _ = os.WriteFile(statusPath, []byte("\x00\x01 not json"), 0o600) },
		"a folder": func() { _ = os.Mkdir(statusPath, 0o700) },
		"an unknown state": func() {
			_ = os.WriteFile(statusPath, []byte(`{"v":1,"installed":true,"state":"on fire","message":"<script>x</script>"}`), 0o600)
		},
	} {
		_ = os.RemoveAll(statusPath)
		put()
		code, out := u.owner.do(http.MethodGet, "/api/v1/system/updates/progress", nil)
		h := obj(out["helper"])
		if code != 200 || h["available"] != false || h["problem"] != "bad_status" || h["state"] != "" || h["message"] != "" {
			t.Errorf("%s: %d %v", name, code, out)
		}
		if code, out := u.owner.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil)); code != 409 || out["code"] != "helper_missing" {
			t.Errorf("%s: an update was accepted: %d %v", name, code, out)
		}
	}
	if u.hasRequest() {
		t.Fatal("a request was written for an updater that cannot be read")
	}
}
