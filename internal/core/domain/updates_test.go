package domain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
)

// Updating from the dashboard, as core runs it: the bot's check says what is
// newest, an owner confirms, core records the request (row, audit entry,
// notices to every owner) and only then writes the file for the server's
// updater, and a worker ends the update, once, when the updater says it is
// over (core is restarted by the update, so it looks again when it is back).

type fakeUpdateSource struct {
	mu       sync.Mutex
	check    *UpdateCheck
	err      error
	nowCheck *UpdateCheck // what "check now" finds (nil: the cached one)
	now      int
}

func (f *fakeUpdateSource) CheckUpdates(context.Context) (*UpdateCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	c := *f.check
	return &c, nil
}

func (f *fakeUpdateSource) CheckUpdatesNow(ctx context.Context) (*UpdateCheck, error) {
	f.mu.Lock()
	f.now++
	if f.nowCheck != nil {
		f.check = f.nowCheck
	}
	f.mu.Unlock()
	return f.CheckUpdates(ctx)
}

// failingHelper is a folder core cannot write a request into.
type failingHelper struct{ *sysupdate.Dir }

func (failingHelper) WriteRequest(sysupdate.Request) error {
	return errors.New("read-only file system")
}

type updatesWorld struct {
	t     *testing.T
	ctx   context.Context
	svc   *Service
	s     *store.Store
	up    *Updates
	src   *fakeUpdateSource
	dir   *sysupdate.Dir
	owner *store.User
}

const (
	updOwnerTwoTG = 9601
	updAdminTG    = 9602
	updSupportTG  = 9603
)

func newUpdatesWorld(t *testing.T) *updatesWorld {
	t.Helper()
	svc, s := testService(t)
	w := &updatesWorld{t: t, ctx: context.Background(), svc: svc, s: s, dir: sysupdate.NewDir(t.TempDir())}
	w.owner = owner(t, svc)
	// the configured owner has a dashboard name and speaks Persian; the second owner, an admin and a
	// support member speak English
	if _, err := s.UpsertUser(w.ctx, s.Conn(), ownerTG, "boss", "fa", ""); err != nil {
		t.Fatal(err)
	}
	var err error
	if w.owner, err = s.GetUserByTelegramID(w.ctx, s.Conn(), ownerTG); err != nil || w.owner.Role != "owner" {
		t.Fatalf("owner: %+v %v", w.owner, err)
	}
	for tg, role := range map[int64]string{updOwnerTwoTG: "owner", updAdminTG: "admin", updSupportTG: "support"} {
		u, err := s.UpsertUser(w.ctx, s.Conn(), tg, "u"+role, "en", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetUserRole(w.ctx, s.Conn(), u.ID, role, "active"); err != nil {
			t.Fatal(err)
		}
	}
	w.src = &fakeUpdateSource{check: &UpdateCheck{Current: "0.6.1", Latest: "0.7.0", Channel: "stable", Issued: "2026-10-20T10:00Z", Newer: true, Verified: true, CheckedAt: 1760000000}}
	w.up = svc.NewUpdates(UpdatesConfig{Source: w.src, Helper: w.dir, Version: "0.6.1"})
	w.helperIdle()
	return w
}

// status writes the updater's status.json.
func (w *updatesWorld) status(over map[string]any) {
	w.t.Helper()
	m := map[string]any{"v": 1, "installed": true, "helper": "0.7.0", "state": "idle", "id": "", "from": "", "to": "", "channel": "", "step": "",
		"message": "", "started_at": "", "finished_at": "", "log": []string{}}
	for k, v := range over {
		m[k] = v
	}
	raw, err := json.Marshal(m)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.dir.Path(), sysupdate.StatusFile), raw, 0o600); err != nil {
		w.t.Fatal(err)
	}
}

func (w *updatesWorld) helperIdle() { w.status(nil) }

func (w *updatesWorld) apply(in ApplyUpdate) (*store.SystemUpdate, error) {
	return w.up.Apply(w.ctx, w.owner, in)
}

func (w *updatesWorld) applyOK() *store.SystemUpdate {
	w.t.Helper()
	run, err := w.apply(ApplyUpdate{Version: "0.7.0", Channel: "stable"})
	if err != nil {
		w.t.Fatalf("apply: %v", err)
	}
	return run
}

func (w *updatesWorld) rows() int {
	w.t.Helper()
	var n int
	if err := w.s.DB().QueryRow(w.ctx, `SELECT count(*) FROM core.system_updates`).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

func (w *updatesWorld) notices() []events.SystemUpdateEvent {
	w.t.Helper()
	var out []events.SystemUpdateEvent
	for _, raw := range outboxEvents(w.t, w.s, events.SystemUpdate) {
		var e events.SystemUpdateEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (w *updatesWorld) request() *sysupdate.Request {
	w.t.Helper()
	r, err := w.dir.ReadRequest()
	if err != nil {
		w.t.Fatalf("no request: %v", err)
	}
	return r
}

func (w *updatesWorld) noRequest() {
	w.t.Helper()
	if _, err := os.Stat(filepath.Join(w.dir.Path(), sysupdate.RequestFile)); !errors.Is(err, os.ErrNotExist) {
		w.t.Fatalf("a request file is there: %v", err)
	}
}

type updAuditRow struct {
	actor    *string
	entity   string
	entityID string
	after    map[string]any
	source   string
}

func (w *updatesWorld) audits(action string) []updAuditRow {
	w.t.Helper()
	rows, err := w.s.DB().Query(w.ctx, `SELECT actor_id::text, entity, COALESCE(entity_id, ''), COALESCE(after, 'null'::jsonb), COALESCE(source, '')
		FROM core.audit_log WHERE action = $1 ORDER BY created_at, id`, action)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []updAuditRow
	for rows.Next() {
		var a updAuditRow
		var after []byte
		if err := rows.Scan(&a.actor, &a.entity, &a.entityID, &after, &a.source); err != nil {
			w.t.Fatal(err)
		}
		_ = json.Unmarshal(after, &a.after)
		out = append(out, a)
	}
	return out
}

// nothingHappened: a refused update leaves no trace: no row, no request, no
// audit entry, no notice.
func (w *updatesWorld) nothingHappened() {
	w.t.Helper()
	if w.rows() != 0 {
		w.t.Fatalf("%d rows", w.rows())
	}
	w.noRequest()
	if n := len(w.audits("system.update.requested")); n != 0 {
		w.t.Fatalf("%d audit entries", n)
	}
	if n := len(w.notices()); n != 0 {
		w.t.Fatalf("%d notices", n)
	}
}

func updCode(err error) string {
	var ue *UpdateError
	if errors.As(err, &ue) {
		return ue.Code
	}
	return ""
}

func TestUpdatesApplyRecordsThenWritesTheRequest(t *testing.T) {
	w := newUpdatesWorld(t)
	run := w.applyOK()
	if run.From != "0.6.1" || run.To != "0.7.0" || run.Channel != "stable" || run.RequestedByName != "boss" || run.Outcome != "" {
		t.Fatalf("run: %+v", run)
	}

	// the request file carries the release the owner confirmed, who asked and when, and nothing else
	r := w.request()
	if r.ID != run.ID || r.Action != "update" || r.Channel != "stable" || r.Version != "0.7.0" || r.By != run.RequestedByName ||
		r.At != run.RequestedAt.UTC().Format(time.RFC3339) || r.V != 1 {
		t.Fatalf("request: %+v for %+v", r, run)
	}
	fi, err := os.Stat(filepath.Join(w.dir.Path(), sysupdate.RequestFile))
	if err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("request file mode: %v %v", fi, err)
	}

	// recorded: one open row, one audit entry made by the owner, one notice for each owner (never admin or support)
	if w.rows() != 1 {
		t.Fatalf("%d rows", w.rows())
	}
	open, err := w.s.OpenSystemUpdate(w.ctx, w.s.Conn())
	if err != nil || open.ID != run.ID {
		t.Fatalf("open: %+v %v", open, err)
	}
	a := w.audits("system.update.requested")
	if len(a) != 1 || a[0].actor == nil || *a[0].actor != w.owner.ID || a[0].entity != "system_update" || a[0].entityID != run.ID ||
		a[0].after["from"] != "0.6.1" || a[0].after["to"] != "0.7.0" || a[0].after["channel"] != "stable" || a[0].after["by"] != run.RequestedByName {
		t.Fatalf("audit: %+v", a)
	}
	got := w.notices()
	if len(got) != 2 {
		t.Fatalf("%d notices, want one for each of the two owners: %+v", len(got), got)
	}
	byTG := map[int64]events.SystemUpdateEvent{}
	for _, e := range got {
		byTG[e.TelegramID] = e
		if e.State != events.UpdateRequested || e.From != "0.6.1" || e.To != "0.7.0" || e.Channel != "stable" || e.By != run.RequestedByName || e.At != run.RequestedAt.Unix() {
			t.Errorf("notice: %+v", e)
		}
	}
	// each owner in their own language; an admin and a support member are not told
	if byTG[ownerTG].TelegramID != ownerTG || byTG[ownerTG].Language != "fa" || byTG[updOwnerTwoTG].TelegramID != updOwnerTwoTG || byTG[updOwnerTwoTG].Language != "en" {
		t.Errorf("the owners: %+v", byTG)
	}
	if _, ok := byTG[updAdminTG]; ok {
		t.Errorf("an admin was told: %+v", byTG)
	}
	if _, ok := byTG[updSupportTG]; ok {
		t.Errorf("a support member was told: %+v", byTG)
	}

	// Progress: queued until the updater takes it up
	p, err := w.up.Progress(w.ctx)
	if err != nil || p.Run == nil || p.Run.ID != run.ID || p.Phase != PhaseQueued || !p.Helper.Available || p.Last != nil {
		t.Fatalf("progress: %+v %v", p, err)
	}
	// a second request while this one is open is refused, and writes nothing more
	if _, err := w.apply(ApplyUpdate{Version: "0.7.0"}); updCode(err) != UpdateAlreadyRunning {
		t.Fatalf("second request: %v", err)
	}
	if w.rows() != 1 || len(w.notices()) != 2 || len(w.audits("system.update.requested")) != 1 {
		t.Fatal("the refused request left a trace")
	}
}

func TestUpdatesApplyRefusals(t *testing.T) {
	prerelease := &UpdateCheck{Current: "0.6.1", Latest: "0.7.0-rc.1", Channel: "beta", Newer: true, Verified: true, CheckedAt: 1760000000}
	for name, c := range map[string]struct {
		prep func(w *updatesWorld)
		in   ApplyUpdate
		want string
	}{
		"the updater is not installed": {func(w *updatesWorld) { w.status(map[string]any{"installed": false}) }, ApplyUpdate{Version: "0.7.0"}, UpdateHelperMissing},
		"the updater's folder is missing": {func(w *updatesWorld) {
			w.up.helper = sysupdate.NewDir(filepath.Join(t.TempDir(), "nope"))
		}, ApplyUpdate{Version: "0.7.0"}, UpdateHelperMissing},
		"the updater has written no status": {func(w *updatesWorld) {
			_ = os.Remove(filepath.Join(w.dir.Path(), sysupdate.StatusFile))
		}, ApplyUpdate{Version: "0.7.0"}, UpdateHelperMissing},
		"its status is garbage": {func(w *updatesWorld) {
			_ = os.WriteFile(filepath.Join(w.dir.Path(), sysupdate.StatusFile), []byte("nope"), 0o600)
		}, ApplyUpdate{Version: "0.7.0"}, UpdateHelperMissing},
		"its status is a link": {func(w *updatesWorld) {
			p := filepath.Join(w.dir.Path(), sysupdate.StatusFile)
			_ = os.Remove(p)
			other := filepath.Join(t.TempDir(), "s.json")
			_ = os.WriteFile(other, []byte(`{"v":1,"installed":true,"state":"idle"}`), 0o600)
			_ = os.Symlink(other, p)
		}, ApplyUpdate{Version: "0.7.0"}, UpdateHelperMissing},
		"it speaks another protocol": {func(w *updatesWorld) { w.status(map[string]any{"v": 2}) }, ApplyUpdate{Version: "0.7.0"}, UpdateHelperMissing},
		"the updater says it is running": {func(w *updatesWorld) {
			w.status(map[string]any{"state": "running", "id": "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99", "step": "pull", "started_at": time.Now().UTC().Format(time.RFC3339)})
		}, ApplyUpdate{Version: "0.7.0"}, UpdateAlreadyRunning},
		"the bot does not answer": {func(w *updatesWorld) { w.src.err = errors.New("bot down") }, ApplyUpdate{Version: "0.7.0"}, UpdateCheckUnavailable},
		"no verified release": {func(w *updatesWorld) {
			w.src.check = &UpdateCheck{Current: "0.6.1", Channel: "stable", Error: "unreachable", CheckedAt: 1760000000}
		}, ApplyUpdate{Version: "0.7.0"}, UpdateNoRelease},
		"a release that failed the signature check": {func(w *updatesWorld) {
			w.src.check = &UpdateCheck{Current: "0.6.1", Latest: "0.7.0", Channel: "stable", Verified: false, Error: "bad_signature", CheckedAt: 1760000000}
		}, ApplyUpdate{Version: "0.7.0"}, UpdateNoRelease},
		"a latest that is not a release number":  {func(w *updatesWorld) { w.src.check.Latest = "0.7.0; rm -rf /" }, ApplyUpdate{Version: "0.7.0; rm -rf /"}, UpdateNoRelease},
		"the same version":                       {func(w *updatesWorld) { w.src.check.Latest = "0.6.1" }, ApplyUpdate{Version: "0.6.1"}, UpdateNotNewer},
		"an older version":                       {func(w *updatesWorld) { w.src.check.Latest = "0.6.0" }, ApplyUpdate{Version: "0.6.0"}, UpdateNotNewer},
		"a pre-release of what runs":             {func(w *updatesWorld) { w.src.check.Latest = "0.6.1-rc.1" }, ApplyUpdate{Version: "0.6.1-rc.1", AcceptPrerelease: true}, UpdateNotNewer},
		"a development build":                    {func(w *updatesWorld) { w.up.version = "dev" }, ApplyUpdate{Version: "0.7.0"}, UpdateDevBuild},
		"another release than the one confirmed": {func(w *updatesWorld) { w.src.check.Latest = "0.7.1" }, ApplyUpdate{Version: "0.7.0"}, UpdateVersionChanged},
		"no release named":                       {func(*updatesWorld) {}, ApplyUpdate{}, UpdateVersionChanged},
		"a release that is not the newest":       {func(*updatesWorld) {}, ApplyUpdate{Version: "0.6.5"}, UpdateVersionChanged},
		"another channel than the one seen":      {func(*updatesWorld) {}, ApplyUpdate{Version: "0.7.0", Channel: "beta"}, UpdateChannelChanged},
		"the bot names no channel":               {func(w *updatesWorld) { w.src.check.Channel = "" }, ApplyUpdate{Version: "0.7.0"}, UpdateCheckUnavailable},
		"a pre-release without its own yes": {func(w *updatesWorld) {
			w.src.check = prerelease
		}, ApplyUpdate{Version: "0.7.0-rc.1", Channel: "beta"}, UpdatePrereleaseUnconfirmed},
	} {
		t.Run(name, func(t *testing.T) {
			w := newUpdatesWorld(t)
			c.prep(w)
			run, err := w.apply(c.in)
			if run != nil || updCode(err) != c.want {
				t.Fatalf("run %+v, error %v (code %q), want %q", run, err, updCode(err), c.want)
			}
			if !errors.Is(err, ErrState) {
				t.Errorf("a refusal is an ErrState: %v", err)
			}
			w.nothingHappened()
		})
	}

	t.Run("version_changed names the release that is newest now", func(t *testing.T) {
		w := newUpdatesWorld(t)
		w.src.check.Latest = "0.7.1"
		_, err := w.apply(ApplyUpdate{Version: "0.7.0"})
		var ue *UpdateError
		if !errors.As(err, &ue) || ue.Latest != "0.7.1" {
			t.Fatalf("%v", err)
		}
	})

	t.Run("only an owner", func(t *testing.T) {
		w := newUpdatesWorld(t)
		admin, err := w.s.GetUserByTelegramID(w.ctx, w.s.Conn(), updAdminTG)
		if err != nil {
			t.Fatal(err)
		}
		support, _ := w.s.GetUserByTelegramID(w.ctx, w.s.Conn(), updSupportTG)
		for _, actor := range []*store.User{admin, support, nil, {ID: w.owner.ID, Role: "user"}} {
			if _, err := w.up.Apply(w.ctx, actor, ApplyUpdate{Version: "0.7.0"}); !errors.Is(err, ErrForbidden) {
				t.Errorf("%+v: %v", actor, err)
			}
		}
		w.nothingHappened()
	})
}

// A pre-release is installed only with its own explicit yes.
func TestUpdatesApplyAPrerelease(t *testing.T) {
	w := newUpdatesWorld(t)
	w.src.check = &UpdateCheck{Current: "0.6.1", Latest: "0.7.0-rc.1", Channel: "beta", Newer: true, Verified: true, CheckedAt: 1760000000}
	run, err := w.apply(ApplyUpdate{Version: "0.7.0-rc.1", Channel: "beta", AcceptPrerelease: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := w.request(); r.Channel != "beta" || r.Version != "0.7.0-rc.1" || r.ID != run.ID {
		t.Fatalf("request: %+v", r)
	}
	if got := w.notices(); len(got) != 2 || got[0].To != "0.7.0-rc.1" || got[0].Channel != "beta" {
		t.Fatalf("notices: %+v", got)
	}
}

// Two owners clicking at the same moment: one update, one request, one audit
// entry, one notice for each owner.
func TestUpdatesApplyAtTheSameMoment(t *testing.T) {
	w := newUpdatesWorld(t)
	const n = 8
	var wg sync.WaitGroup
	results := make([]error, n)
	wins := make([]*store.SystemUpdate, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wins[i], results[i] = w.apply(ApplyUpdate{Version: "0.7.0", Channel: "stable"})
		}()
	}
	wg.Wait()
	ok := 0
	for i, err := range results {
		switch {
		case err == nil && wins[i] != nil:
			ok++
		case updCode(err) == UpdateAlreadyRunning:
		default:
			t.Errorf("request %d: %v", i, err)
		}
	}
	if ok != 1 || w.rows() != 1 || len(w.audits("system.update.requested")) != 1 || len(w.notices()) != 2 {
		t.Fatalf("%d accepted, %d rows, %d audit entries, %d notices", ok, w.rows(), len(w.audits("system.update.requested")), len(w.notices()))
	}
	_ = w.request()
}

func TestUpdatesApplyThatCannotWriteTheRequest(t *testing.T) {
	w := newUpdatesWorld(t)
	w.up.helper = failingHelper{w.dir}
	run, err := w.apply(ApplyUpdate{Version: "0.7.0", Channel: "stable"})
	if run != nil || updCode(err) != UpdateRequestFailed {
		t.Fatalf("%+v %v", run, err)
	}
	// it was recorded and announced, so its end is too: failed, with its audit entry and notices
	if w.rows() != 1 {
		t.Fatalf("%d rows", w.rows())
	}
	last, err := w.s.LastSystemUpdate(w.ctx, w.s.Conn())
	if err != nil || last.Outcome != events.UpdateFailed || !strings.Contains(last.Message, "server's updater") {
		t.Fatalf("last: %+v %v", last, err)
	}
	if len(w.audits("system.update.requested")) != 1 || len(w.audits("system.update.failed")) != 1 {
		t.Fatalf("audit: requested %d, failed %d", len(w.audits("system.update.requested")), len(w.audits("system.update.failed")))
	}
	var states []string
	for _, e := range w.notices() {
		states = append(states, e.State)
	}
	if len(states) != 4 || strings.Count(strings.Join(states, ","), "failed") != 2 || strings.Count(strings.Join(states, ","), "requested") != 2 {
		t.Fatalf("notices: %v", states)
	}
	w.noRequest()
	// nothing is open any more: a request can be made again
	w.up.helper = w.dir
	if _, err := w.apply(ApplyUpdate{Version: "0.7.0", Channel: "stable"}); err != nil {
		t.Fatalf("after a failed request: %v", err)
	}
}

// The update ends when the updater says so: once, with the audit entry made by
// the system and a notice for each owner, whichever way it ended.
func TestUpdatesStepEndsTheUpdateOnce(t *testing.T) {
	for _, state := range []string{"done", "failed", "rolled_back"} {
		t.Run(state, func(t *testing.T) {
			w := newUpdatesWorld(t)
			run := w.applyOK()
			if err := w.up.Step(w.ctx); err != nil {
				t.Fatal(err)
			}
			// the updater has not taken it up, or runs it: still open
			w.status(map[string]any{"state": "running", "id": run.ID, "from": "0.6.1", "to": "0.7.0", "channel": "stable", "step": "backup",
				"started_at": time.Now().UTC().Format(time.RFC3339), "message": "Backing up the database."})
			if err := w.up.Step(w.ctx); err != nil {
				t.Fatal(err)
			}
			p, _ := w.up.Progress(w.ctx)
			if p.Run == nil || p.Phase != PhaseRunning || p.Helper.Status.Step != "backup" {
				t.Fatalf("running: %+v", p)
			}
			if len(w.audits("system.update."+state)) != 0 || len(w.notices()) != 2 {
				t.Fatal("ended too early")
			}

			message := "the new version did not become healthy"
			w.status(map[string]any{"state": state, "id": run.ID, "from": "0.6.1", "to": "0.7.0", "channel": "stable", "step": "done", "message": message,
				"started_at": time.Now().UTC().Format(time.RFC3339), "finished_at": time.Now().UTC().Format(time.RFC3339)})
			// the update restarts core: a worker of a core that just started finds the end the same way
			restarted := w.svc.NewUpdates(UpdatesConfig{Source: w.src, Helper: w.dir, Version: "0.6.1"})
			for i := 0; i < 3; i++ {
				for _, up := range []*Updates{w.up, restarted} {
					if err := up.Step(w.ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			last, err := w.s.LastSystemUpdate(w.ctx, w.s.Conn())
			if err != nil || last.ID != run.ID || last.Outcome != state || last.OutcomeAt == nil || last.Message != message {
				t.Fatalf("last: %+v %v", last, err)
			}
			if _, err := w.s.OpenSystemUpdate(w.ctx, w.s.Conn()); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("still open: %v", err)
			}
			a := w.audits("system.update." + state)
			if len(a) != 1 || a[0].actor != nil || a[0].source != "system" || a[0].entityID != run.ID || a[0].after["to"] != "0.7.0" || a[0].after["message"] != message {
				t.Fatalf("audit: %+v", a)
			}
			got := w.notices()
			if len(got) != 4 { // two for the request, two for its end
				t.Fatalf("%d notices: %+v", len(got), got)
			}
			ended := 0
			for _, e := range got[2:] {
				if e.State == state && e.From == "0.6.1" && e.To == "0.7.0" && e.Message == message && e.By == run.RequestedByName {
					ended++
				}
			}
			if ended != 2 {
				t.Fatalf("the owners' notices of the end: %+v", got[2:])
			}
			w.noRequest() // the request file is cleared up
			// and the next update can be asked for
			p, _ = w.up.Progress(w.ctx)
			if p.Run != nil || p.Last == nil || p.Last.Outcome != state {
				t.Fatalf("progress after: %+v", p)
			}
		})
	}
}

// What another run left in the updater's status ends nothing of ours.
func TestUpdatesStepIgnoresAnotherRunsStatus(t *testing.T) {
	w := newUpdatesWorld(t)
	run := w.applyOK()
	other := "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99"
	for _, st := range []map[string]any{
		{"state": "done", "id": other, "from": "0.6.0", "to": "0.6.1", "finished_at": time.Now().UTC().Format(time.RFC3339)},
		{"state": "rolled_back", "id": other},
		{"state": "idle"},
		{"state": "done", "id": "not-an-id"},
		{"state": "done", "id": run.ID + "0"}, // only the exact id is ours
	} {
		w.status(st)
		if err := w.up.Step(w.ctx); err != nil {
			t.Fatal(err)
		}
		if p, _ := w.up.Progress(w.ctx); p.Run == nil || p.Phase != PhaseQueued {
			t.Fatalf("%v ended or moved our update: %+v", st, p)
		}
	}
	if len(w.audits("system.update.done"))+len(w.audits("system.update.rolled_back")) != 0 || len(w.notices()) != 2 {
		t.Fatal("something was announced")
	}
}

// If the status was lost (the updater cleared it) but core runs the release it
// asked for, the update plainly happened.
func TestUpdatesStepNoticesThatTheNewVersionRuns(t *testing.T) {
	w := newUpdatesWorld(t)
	run := w.applyOK()
	w.status(nil) // idle, no trace of the run
	newCore := w.svc.NewUpdates(UpdatesConfig{Source: w.src, Helper: w.dir, Version: "0.7.0"})
	if err := newCore.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	last, err := w.s.LastSystemUpdate(w.ctx, w.s.Conn())
	if err != nil || last.ID != run.ID || last.Outcome != events.UpdateDone || !strings.Contains(last.Message, "0.7.0") {
		t.Fatalf("last: %+v %v", last, err)
	}
	if len(w.audits("system.update.done")) != 1 || len(w.notices()) != 4 {
		t.Fatalf("audit %d, notices %d", len(w.audits("system.update.done")), len(w.notices()))
	}
	// a dev build compares nothing
	w2 := newUpdatesWorld(t)
	w2.applyOK()
	dev := w2.svc.NewUpdates(UpdatesConfig{Source: w2.src, Helper: w2.dir, Version: "dev"})
	if err := dev.Step(w2.ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := dev.Progress(w2.ctx); p.Run == nil {
		t.Fatal("a development build ended an update by its version")
	}
}

// A request the updater never takes up is withdrawn after a while (it must not
// run at some later moment nobody expects) and the owners are told.
func TestUpdatesStepWithdrawsARequestNobodyTookUp(t *testing.T) {
	w := newUpdatesWorld(t)
	run := w.applyOK()
	later := func(d time.Duration) { w.up.now = func() time.Time { return time.Now().Add(d) } }

	later(14 * time.Minute)
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := w.up.Progress(w.ctx); p.Run == nil {
		t.Fatal("withdrawn too early")
	}
	// the updater is busy with something else: it is not dead, so keep waiting
	w.status(map[string]any{"state": "running", "id": "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99", "started_at": time.Now().UTC().Format(time.RFC3339)})
	later(20 * time.Minute)
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := w.up.Progress(w.ctx); p.Run == nil {
		t.Fatal("withdrawn while the updater was busy with another update")
	}
	w.request() // still there

	w.helperIdle()
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	last, err := w.s.LastSystemUpdate(w.ctx, w.s.Conn())
	if err != nil || last.ID != run.ID || last.Outcome != events.UpdateNotStarted {
		t.Fatalf("last: %+v %v", last, err)
	}
	w.noRequest()
	if len(w.audits("system.update.not_started")) != 1 {
		t.Fatalf("audit: %d", len(w.audits("system.update.not_started")))
	}
	got := w.notices()
	if len(got) != 4 || got[2].State != events.UpdateNotStarted || got[3].State != events.UpdateNotStarted {
		t.Fatalf("notices: %+v", got)
	}
	// another request is possible now
	w.up.now = time.Now
	if _, err := w.apply(ApplyUpdate{Version: "0.7.0", Channel: "stable"}); err != nil {
		t.Fatalf("after a withdrawn request: %v", err)
	}
}

// An update the updater says it has been running for over an hour is recorded as
// failed (its own status keeps saying running, and keeps another from starting).
func TestUpdatesStepGivesUpOnAnUpdateThatNeverEnds(t *testing.T) {
	w := newUpdatesWorld(t)
	run := w.applyOK()
	w.status(map[string]any{"state": "running", "id": run.ID, "step": "pull", "started_at": time.Now().Add(-61 * time.Minute).UTC().Format(time.RFC3339)})
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	last, err := w.s.LastSystemUpdate(w.ctx, w.s.Conn())
	if err != nil || last.Outcome != events.UpdateFailed || !strings.Contains(last.Message, "sudo bobres status") {
		t.Fatalf("last: %+v %v", last, err)
	}
	if _, err := w.apply(ApplyUpdate{Version: "0.7.0"}); updCode(err) != UpdateAlreadyRunning {
		t.Fatalf("the updater still says running: %v", err)
	}
	// an update that has run for 59 minutes is still running
	w2 := newUpdatesWorld(t)
	run2 := w2.applyOK()
	w2.status(map[string]any{"state": "running", "id": run2.ID, "started_at": time.Now().Add(-59 * time.Minute).UTC().Format(time.RFC3339)})
	if err := w2.up.Step(w2.ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := w2.up.Progress(w2.ctx); p.Run == nil {
		t.Fatal("ended at 59 minutes")
	}
}

// Run steps until it is stopped, and ends an update by itself.
func TestUpdatesRunEndsTheUpdate(t *testing.T) {
	w := newUpdatesWorld(t)
	w.up.Every = 10 * time.Millisecond
	run := w.applyOK()
	ctx, cancel := context.WithCancel(w.ctx)
	done := make(chan error, 1)
	go func() { done <- w.up.Run(ctx) }()
	w.status(map[string]any{"state": "done", "id": run.ID, "from": "0.6.1", "to": "0.7.0", "finished_at": time.Now().UTC().Format(time.RFC3339)})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := w.s.OpenSystemUpdate(w.ctx, w.s.Conn()); errors.Is(err, store.ErrNotFound) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if last, err := w.s.LastSystemUpdate(w.ctx, w.s.Conn()); err != nil || last.Outcome != events.UpdateDone {
		t.Fatalf("last: %+v %v", last, err)
	}
}

func TestUpdatesChannelSetting(t *testing.T) {
	w := newUpdatesWorld(t)
	ch := func() string {
		t.Helper()
		v, err := w.up.Channel(w.ctx)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if ch() != "" {
		t.Fatalf("a fresh install follows the server's default: %q", ch())
	}
	if changed, err := w.up.SetChannel(w.ctx, w.owner, "beta"); err != nil || !changed || ch() != "beta" {
		t.Fatalf("beta: %v %v %q", changed, err, ch())
	}
	// the setting is audited as any setting is, and the bot reads it with its other settings
	a := w.audits("setting.set")
	if len(a) != 1 || a[0].entityID != settingChannelKey || a[0].after["value"] != "beta" {
		t.Fatalf("audit: %+v", a)
	}
	if v, _ := w.svc.Settings(w.ctx); v[settingChannelKey] != "beta" {
		t.Fatalf("settings: %v", v)
	}
	if changed, err := w.up.SetChannel(w.ctx, w.owner, " beta "); err != nil || changed {
		t.Fatalf("the same again: %v %v", changed, err)
	}
	if changed, err := w.up.SetChannel(w.ctx, w.owner, "stable"); err != nil || !changed || ch() != "stable" {
		t.Fatalf("stable: %v %v", changed, err)
	}
	if changed, err := w.up.SetChannel(w.ctx, w.owner, ""); err != nil || !changed || ch() != "" {
		t.Fatalf("back to the server's default: %v %v %q", changed, err, ch())
	}
	for _, bad := range []string{"nightly", "latest", "Beta", "stable,beta", "x\ny"} {
		if _, err := w.up.SetChannel(w.ctx, w.owner, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	// an admin never sets it, here or with the bot's /set
	admin, _ := w.s.GetUserByTelegramID(w.ctx, w.s.Conn(), updAdminTG)
	if _, err := w.up.SetChannel(w.ctx, admin, "beta"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin: %v", err)
	}
	if _, err := w.up.SetChannel(w.ctx, nil, "beta"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("nobody: %v", err)
	}
	if err := w.svc.AdminSetSetting(w.ctx, updAdminTG, settingChannelKey, "beta"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "only the store owner") {
		t.Fatalf("admin /set: %v", err)
	}
	if err := w.svc.AdminSetSetting(w.ctx, ownerTG, settingChannelKey, "beta"); err != nil {
		t.Fatalf("owner /set: %v", err)
	}
	// admins do not even see it in the bot's list; the owner does
	list := func(tg int64) bool {
		views, err := w.svc.AdminSettingSpecs(w.ctx, tg)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range views {
			if v.Key == settingChannelKey {
				return true
			}
		}
		return false
	}
	if list(updAdminTG) || !list(ownerTG) {
		t.Fatalf("listed for the admin %v, for the owner %v", list(updAdminTG), list(ownerTG))
	}
}

// settingChannelKey is the key, spelled out so the tests fail if it is renamed.
const settingChannelKey = "updates.channel"

func TestUpdatesCanApplyAndProgress(t *testing.T) {
	w := newUpdatesWorld(t)
	check, _ := w.up.Check(w.ctx)
	p, _ := w.up.Progress(w.ctx)
	if got := w.up.CanApply(check, p); got != "" {
		t.Fatalf("ready: %q", got)
	}
	// the updater is missing
	w.status(map[string]any{"installed": false})
	p, _ = w.up.Progress(w.ctx)
	if got := w.up.CanApply(check, p); got != UpdateHelperMissing || p.Helper.Problem != HelperNotInstalled || p.Helper.Available {
		t.Fatalf("not installed: %q %+v", got, p.Helper)
	}
	for file, want := range map[string]string{"": HelperNoStatus, "nope": HelperBadStatus, `{"v":3,"installed":true,"state":"idle"}`: HelperUnsupported} {
		_ = os.Remove(filepath.Join(w.dir.Path(), sysupdate.StatusFile))
		if file != "" {
			_ = os.WriteFile(filepath.Join(w.dir.Path(), sysupdate.StatusFile), []byte(file), 0o600)
		}
		if h := w.up.Helper(); h.Available || h.Problem != want {
			t.Errorf("%q: %+v, want %s", file, h, want)
		}
	}
	w.up.helper = sysupdate.NewDir(filepath.Join(t.TempDir(), "gone"))
	if h := w.up.Helper(); h.Available || h.Problem != HelperNoDir {
		t.Fatalf("no folder: %+v", h)
	}
	w.up.helper = nil
	if h := w.up.Helper(); h.Available || h.Problem != HelperNoDir {
		t.Fatalf("no helper at all: %+v", h)
	}
	// up to date
	w.up.helper = w.dir
	w.helperIdle()
	check.Latest = "0.6.1"
	p, _ = w.up.Progress(w.ctx)
	if got := w.up.CanApply(check, p); got != UpdateNotNewer {
		t.Fatalf("up to date: %q", got)
	}
	// no bot
	if got := w.up.CanApply(nil, p); got != UpdateCheckUnavailable {
		t.Fatalf("no check: %q", got)
	}
	noBot := w.svc.NewUpdates(UpdatesConfig{Helper: w.dir, Version: "0.6.1"})
	if _, err := noBot.Check(w.ctx); err == nil {
		t.Fatal("a check with no bot")
	}
	if _, err := noBot.CheckNow(w.ctx); err == nil {
		t.Fatal("a check now with no bot")
	}
	// check now goes to the bot
	w.src.check = &UpdateCheck{Current: "0.6.1", Latest: "0.7.0", Channel: "stable", Newer: true, Verified: true}
	if c, err := w.up.CheckNow(w.ctx); err != nil || c.Latest != "0.7.0" || w.src.now != 1 {
		t.Fatalf("check now: %+v %v (%d)", c, err, w.src.now)
	}
	if w.up.Version() != "0.6.1" {
		t.Fatal(w.up.Version())
	}
}

// The notices name the person as the audit log does: their dashboard name, else
// their Telegram id; never more than 64 characters on one line.
func TestUpdatesRequesterName(t *testing.T) {
	for _, tc := range []struct {
		u    store.User
		want string
	}{
		{store.User{Username: "sara_k", TelegramID: 5}, "sara_k"},
		{store.User{Username: "", TelegramID: 77}, "tg:77"},
		{store.User{Username: "  two\twords  ", TelegramID: 5}, "two words"},
		{store.User{Username: strings.Repeat("a", 100), TelegramID: 5}, strings.Repeat("a", 63) + "…"},
		{store.User{Username: "\x00\x01", TelegramID: 9}, "tg:9"},
	} {
		if got := requesterName(&tc.u); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc.u, got, tc.want)
		}
	}
}
