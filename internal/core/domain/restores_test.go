package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

// Restoring a backup from the dashboard, as core runs it: an owner uploads a backup or picks one of
// the server's, confirms it (the store's domain typed, the loss acknowledged), core records the
// request (row, audit entry, notices) and only then writes the file for the server's updater; the
// worker ends it once, also when the restore replaced core's own database and the row is gone.

const (
	restoreFile  = "bobres-20261011T033000Z.bkp"
	restoreOther = "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f99"
)

var restoreSum = strings.Repeat("ab", 32)

func restoreWorld(t *testing.T) *updatesWorld {
	t.Helper()
	w := newUpdatesWorld(t)
	w.up.storeDomain = "panel.example.com"
	w.canRestore()
	return w
}

// canRestore makes the updater's status say that it can restore (a bobres of this release).
func (w *updatesWorld) canRestore() { w.status(map[string]any{"restore": true}) }

func (w *updatesWorld) restoreIn(mod func(*ApplyRestoreInput)) ApplyRestoreInput {
	in := ApplyRestoreInput{Backup: &RestoreBackup{File: restoreFile, Size: 4096, SHA256: restoreSum}, Confirm: "panel.example.com", Acknowledge: true}
	if mod != nil {
		mod(&in)
	}
	return in
}

func (w *updatesWorld) applyRestore(mod func(*ApplyRestoreInput)) (*store.SystemUpdate, error) {
	return w.up.ApplyRestore(w.ctx, w.owner, w.restoreIn(mod))
}

func (w *updatesWorld) backupNotices() []events.SystemBackupEvent {
	w.t.Helper()
	var out []events.SystemBackupEvent
	for _, raw := range outboxEvents(w.t, w.s, events.SystemBackup) {
		var e events.SystemBackupEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (w *updatesWorld) rawRequest() map[string]any {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join(w.dir.Path(), sysupdate.RequestFile))
	if err != nil {
		w.t.Fatalf("no request: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		w.t.Fatal(err)
	}
	return m
}

func (w *updatesWorld) nothingRestored() {
	w.t.Helper()
	if w.rows() != 0 {
		w.t.Fatalf("%d rows", w.rows())
	}
	w.noRequest()
	if n := len(w.audits("system.restore.requested")); n != 0 {
		w.t.Fatalf("%d audit entries", n)
	}
	if n := len(w.backupNotices()); n != 0 {
		w.t.Fatalf("%d notices", n)
	}
}

func TestRestoreApplyRecordsThenWritesTheRequest(t *testing.T) {
	w := restoreWorld(t)
	run, err := w.applyRestore(nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Action != store.ActionRestore || run.Subject != restoreFile || run.Source != "backup" || run.Channel != "" || run.From != "0.6.1" || run.To != "0.6.1" ||
		run.RequestedByName != "boss" || run.Outcome != "" {
		t.Fatalf("run: %+v", run)
	}
	req := w.rawRequest()
	if req["id"] != run.ID || req["action"] != "restore" || req["source"] != "backup" || req["file"] != restoreFile || req["upload"] != "" ||
		req["sha256"] != restoreSum || req["size"] != float64(4096) || req["by"] != "boss" || len(req) != 10 {
		t.Fatalf("request: %v", req)
	}
	// one open row (a restore), one audit entry by the owner, one notice for each owner
	open, err := w.s.OpenSystemUpdate(w.ctx, w.s.Conn())
	if err != nil || open.ID != run.ID || open.Action != store.ActionRestore {
		t.Fatalf("open: %+v %v", open, err)
	}
	a := w.audits("system.restore.requested")
	if len(a) != 1 || a[0].actor == nil || *a[0].actor != w.owner.ID || a[0].entityID != run.ID || a[0].after["backup"] != restoreFile ||
		a[0].after["sha256"] != restoreSum || a[0].after["by"] != "boss" {
		t.Fatalf("audit: %+v", a)
	}
	got := w.backupNotices()
	if len(got) != 2 {
		t.Fatalf("%d notices, want one for each of the two owners: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Kind != events.BackupRestore || e.State != events.UpdateRequested || e.File != restoreFile || e.Source != "backup" || e.By != "boss" ||
			(e.TelegramID != ownerTG && e.TelegramID != updOwnerTwoTG) {
			t.Errorf("notice: %+v", e)
		}
	}
	// the update tab's own notices are not used for a restore
	if len(w.notices()) != 0 {
		t.Errorf("update notices: %+v", w.notices())
	}
	// a second request, of either kind, is refused while this one is open
	if _, err := w.applyRestore(nil); updCode(err) != UpdateAlreadyRunning {
		t.Fatalf("second restore: %v", err)
	}
	if _, err := w.apply(ApplyUpdate{Version: "0.7.0"}); updCode(err) != UpdateAlreadyRunning {
		t.Fatalf("an update during a restore: %v", err)
	}
	if w.rows() != 1 || len(w.backupNotices()) != 2 {
		t.Fatalf("rows %d, notices %d", w.rows(), len(w.backupNotices()))
	}
}

func TestRestoreApplyRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		prep func(w *updatesWorld)
		who  func(w *updatesWorld) *store.User
		mod  func(*ApplyRestoreInput)
		code string
	}{
		"an admin": {who: func(w *updatesWorld) *store.User {
			u, err := w.s.GetUserByTelegramID(w.ctx, w.s.Conn(), updAdminTG)
			if err != nil {
				t.Fatal(err)
			}
			return u
		}, code: "forbidden"},
		"nobody":                                 {who: func(*updatesWorld) *store.User { return nil }, code: "forbidden"},
		"no acknowledgement":                     {mod: func(in *ApplyRestoreInput) { in.Acknowledge = false }, code: RestoreAckRequired},
		"the wrong domain":                       {mod: func(in *ApplyRestoreInput) { in.Confirm = "panel.example.org" }, code: RestoreConfirmWrong},
		"no domain typed":                        {mod: func(in *ApplyRestoreInput) { in.Confirm = "" }, code: RestoreConfirmWrong},
		"the domain with a path":                 {mod: func(in *ApplyRestoreInput) { in.Confirm = "https://panel.example.com" }, code: RestoreConfirmWrong},
		"core does not know the domain":          {prep: func(w *updatesWorld) { w.up.storeDomain = "" }, code: RestoreNoDomain},
		"the updater is not installed":           {prep: func(w *updatesWorld) { w.status(map[string]any{"installed": false}) }, code: UpdateHelperMissing},
		"the updater is from before the restore": {prep: func(w *updatesWorld) { w.status(map[string]any{"restore": false}) }, code: RestoreUnsupported},
		"the updater is busy": {prep: func(w *updatesWorld) {
			w.status(map[string]any{"restore": true, "state": "running", "id": restoreOther, "started_at": time.Now().UTC().Format(time.RFC3339)})
			w.wrote(time.Now())
		}, code: UpdateAlreadyRunning},
		"a name that is a path":   {mod: func(in *ApplyRestoreInput) { in.Backup.File = "../" + restoreFile }, code: RestoreBackupUnknown},
		"a sidecar":               {mod: func(in *ApplyRestoreInput) { in.Backup.File = "bobres-20261011T033000Z.json" }, code: RestoreBackupUnknown},
		"a backup of no size":     {mod: func(in *ApplyRestoreInput) { in.Backup.Size = 0 }, code: RestoreBackupUnknown},
		"a backup over the limit": {mod: func(in *ApplyRestoreInput) { in.Backup.Size = sysupdate.MaxUpload + 1 }, code: RestoreBackupUnknown},
		"a backup with no hash":   {mod: func(in *ApplyRestoreInput) { in.Backup.SHA256 = "" }, code: RestoreBackupUnknown},
		"an upload nobody sent":   {mod: func(in *ApplyRestoreInput) { in.Backup, in.UploadID = nil, restoreOther }, code: RestoreUploadMissing},
	} {
		t.Run(name, func(t *testing.T) {
			w := restoreWorld(t)
			if tc.prep != nil {
				tc.prep(w)
			}
			actor := w.owner
			if tc.who != nil {
				actor = tc.who(w)
			}
			_, err := w.up.ApplyRestore(w.ctx, actor, w.restoreIn(tc.mod))
			switch tc.code {
			case "forbidden":
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("got %v", err)
				}
			default:
				if updCode(err) != tc.code {
					t.Fatalf("got %v (%q), want %q", err, updCode(err), tc.code)
				}
				if !errors.Is(err, ErrState) {
					t.Fatalf("a refusal is an ErrState: %v", err)
				}
			}
			if tc.prep == nil || tc.code != UpdateAlreadyRunning {
				w.nothingRestored()
			}
		})
	}
}

func TestRestoreConfirmationIgnoresCaseAndSpaces(t *testing.T) {
	w := restoreWorld(t)
	if _, err := w.applyRestore(func(in *ApplyRestoreInput) { in.Confirm = "  Panel.Example.COM \n" }); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreAtMostOneAtATime(t *testing.T) {
	w := restoreWorld(t)
	var wg sync.WaitGroup
	results := make([]error, 6)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = w.applyRestore(nil)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case updCode(err) != UpdateAlreadyRunning:
			t.Errorf("a refusal other than 'already running': %v", err)
		}
	}
	if ok != 1 || w.rows() != 1 || len(w.audits("system.restore.requested")) != 1 || len(w.backupNotices()) != 2 {
		t.Fatalf("%d accepted, %d rows, %d audits, %d notices", ok, w.rows(), len(w.audits("system.restore.requested")), len(w.backupNotices()))
	}
}

func TestRestoreRequestThatCannotBeWrittenIsRecordedAsFailed(t *testing.T) {
	w := restoreWorld(t)
	w.up.helper = failingRestoreHelper{w.dir}
	_, err := w.applyRestore(nil)
	if updCode(err) != UpdateRequestFailed {
		t.Fatalf("got %v", err)
	}
	last, lerr := w.s.LastSystemRestore(w.ctx, w.s.Conn())
	if lerr != nil || last.Outcome != events.UpdateFailed || last.Action != store.ActionRestore {
		t.Fatalf("last: %+v %v", last, lerr)
	}
	if len(w.audits("system.restore.failed")) != 1 || len(w.backupNotices()) != 4 { // the request, its end, for two owners
		t.Fatalf("audit %d, notices %d", len(w.audits("system.restore.failed")), len(w.backupNotices()))
	}
	// and the next one can be asked for
	w.up.helper = w.dir
	if _, err := w.applyRestore(nil); err != nil {
		t.Fatal(err)
	}
}

type failingRestoreHelper struct{ *sysupdate.Dir }

func (failingRestoreHelper) WriteRestoreRequest(sysupdate.RestoreRequest) error {
	return errors.New("read-only file system")
}

func TestRestoreUploadFlow(t *testing.T) {
	w := restoreWorld(t)
	body := bytes.Repeat([]byte("backup bytes "), 1000)
	st, err := w.up.StageRestoreUpload(w.ctx, w.owner, "my\nbackup‮.bkp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if st.Name != "my backup.bkp" || st.Size != int64(len(body)) || len(st.SHA256) != 64 || st.By != "boss" {
		t.Fatalf("staged: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(w.dir.Path(), sysupdate.UploadDirName, st.ID+".bkp")); err != nil {
		t.Fatalf("the file is not there: %v", err)
	}
	a := w.audits("system.restore.upload")
	if len(a) != 1 || a[0].after["size"] != float64(st.Size) || a[0].after["sha256"] != st.SHA256 {
		t.Fatalf("audit: %+v", a)
	}
	if got := w.up.Staged(); got == nil || got.ID != st.ID {
		t.Fatalf("staged: %+v", got)
	}

	// a new upload replaces it, and its file goes
	st2, err := w.up.StageRestoreUpload(w.ctx, w.owner, "", bytes.NewReader([]byte("another")))
	if err != nil || st2.Name != "backup.bkp" {
		t.Fatalf("%+v %v", st2, err)
	}
	if _, err := os.Stat(filepath.Join(w.dir.Path(), sysupdate.UploadDirName, st.ID+".bkp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the replaced upload is still there: %v", err)
	}
	// the old one cannot be restored any more
	if _, err := w.applyRestore(func(in *ApplyRestoreInput) { in.Backup, in.UploadID = nil, st.ID }); updCode(err) != RestoreUploadMissing {
		t.Fatalf("the replaced upload: %v", err)
	}
	run, err := w.applyRestore(func(in *ApplyRestoreInput) { in.Backup, in.UploadID = nil, st2.ID })
	if err != nil {
		t.Fatal(err)
	}
	req := w.rawRequest()
	if run.Source != "upload" || run.Subject != "backup.bkp" || req["source"] != "upload" || req["upload"] != st2.ID || req["sha256"] != st2.SHA256 ||
		req["size"] != float64(st2.Size) || req["file"] != "backup.bkp" {
		t.Fatalf("run %+v, request %v", run, req)
	}
	// the updater owns the file now: core keeps no claim on it
	if w.up.Staged() != nil {
		t.Fatal("the upload is still staged")
	}
	if a := w.audits("system.restore.requested"); len(a) != 1 || a[0].after["source"] != "upload" || a[0].after["sha256"] != st2.SHA256 {
		t.Fatalf("audit: %+v", a)
	}
}

func TestRestoreUploadRefusals(t *testing.T) {
	w := restoreWorld(t)
	admin, _ := w.s.GetUserByTelegramID(w.ctx, w.s.Conn(), updAdminTG)
	if _, err := w.up.StageRestoreUpload(w.ctx, admin, "x", strings.NewReader("x")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an admin: %v", err)
	}
	if _, err := w.up.StageRestoreUpload(w.ctx, w.owner, "x", strings.NewReader("")); updCode(err) != RestoreUploadEmpty {
		t.Fatalf("empty: %v", err)
	}
	// nothing is received while the updater cannot restore, or is busy
	w.status(map[string]any{"restore": false})
	if _, err := w.up.StageRestoreUpload(w.ctx, w.owner, "x", strings.NewReader("x")); updCode(err) != RestoreUnsupported {
		t.Fatalf("an updater from before the restore: %v", err)
	}
	w.canRestore()
	run, err := w.applyRestore(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.up.StageRestoreUpload(w.ctx, w.owner, "x", strings.NewReader("x")); updCode(err) != UpdateAlreadyRunning {
		t.Fatalf("during a restore: %v", err)
	}
	_ = run
	if entries, _ := os.ReadDir(filepath.Join(w.dir.Path(), sysupdate.UploadDirName)); len(entries) != 0 {
		t.Fatalf("something is left in the upload folder: %v", entries)
	}
	if len(w.audits("system.restore.upload")) != 0 {
		t.Fatal("a refused upload was audited as received")
	}
}

// One upload is received at a time.
func TestRestoreOneUploadAtATime(t *testing.T) {
	w := restoreWorld(t)
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		_, err := w.up.StageRestoreUpload(w.ctx, w.owner, "slow.bkp", pr)
		errc <- err
	}()
	// wait until the first upload is being received
	deadline := time.Now().Add(5 * time.Second)
	for {
		if !w.up.receiving.TryLock() {
			break
		}
		w.up.receiving.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("the first upload never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := w.up.StageRestoreUpload(w.ctx, w.owner, "second.bkp", strings.NewReader("x")); updCode(err) != RestoreUploadBusy {
		t.Fatalf("the second upload: %v", err)
	}
	_, _ = pw.Write([]byte("some bytes"))
	_ = pw.Close()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if s := w.up.Staged(); s == nil || s.Name != "slow.bkp" {
		t.Fatalf("staged: %+v", s)
	}
}

func TestRestoreStagedUploadExpires(t *testing.T) {
	w := restoreWorld(t)
	st, err := w.up.StageRestoreUpload(w.ctx, w.owner, "x.bkp", strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	w.up.now = func() time.Time { return time.Now().Add(StagedTTL + time.Minute) }
	if w.up.Staged() != nil {
		t.Fatal("an expired upload is still offered")
	}
	if _, err := w.applyRestore(func(in *ApplyRestoreInput) { in.Backup, in.UploadID = nil, st.ID }); updCode(err) != RestoreUploadMissing {
		t.Fatalf("an expired upload: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.dir.Path(), sysupdate.UploadDirName, st.ID+".bkp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the expired file is still there: %v", err)
	}
	w.nothingRestored()
}

// The restore did not replace the database (it failed before that, or went back): the row is
// there, and ends as an update's does, once.
func TestRestoreStepEndsTheRowOnce(t *testing.T) {
	for _, state := range []string{"failed", "rolled_back"} {
		t.Run(state, func(t *testing.T) {
			w := restoreWorld(t)
			run, err := w.applyRestore(nil)
			if err != nil {
				t.Fatal(err)
			}
			// running: nothing ends
			w.status(map[string]any{"restore": true, "state": "running", "id": run.ID, "action": "restore", "step": "restore", "subject": restoreFile,
				"source": "backup", "by": "boss", "started_at": time.Now().UTC().Format(time.RFC3339)})
			w.wrote(time.Now())
			if err := w.up.Step(w.ctx); err != nil {
				t.Fatal(err)
			}
			if p, _ := w.up.Progress(w.ctx); p.Run == nil || p.Phase != PhaseRunning {
				t.Fatalf("progress: %+v", p)
			}
			w.status(map[string]any{"restore": true, "state": state, "id": run.ID, "action": "restore", "step": "rollback", "subject": restoreFile,
				"source": "backup", "by": "boss", "message": "The restore did not go through.", "finished_at": time.Now().UTC().Format(time.RFC3339)})
			for i := 0; i < 3; i++ { // the worker looks again and again
				if err := w.up.Step(w.ctx); err != nil {
					t.Fatal(err)
				}
			}
			last, err := w.s.LastSystemRestore(w.ctx, w.s.Conn())
			if err != nil || last.ID != run.ID || last.Outcome != state || last.Message != "The restore did not go through." {
				t.Fatalf("last: %+v %v", last, err)
			}
			if len(w.audits("system.restore."+state)) != 1 || len(w.backupNotices()) != 4 {
				t.Fatalf("audit %d, notices %d", len(w.audits("system.restore."+state)), len(w.backupNotices()))
			}
			w.noRequest()
			if last, err := w.s.LastSystemUpdate(w.ctx, w.s.Conn()); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("a restore is not the last update: %+v %v", last, err)
			}
		})
	}
}

// The restore worked: it replaced this database with a backup's, which holds no row for it. The
// end is found again from the updater's status, once.
func TestRestoreStepRecordsARestoreThatReplacedTheDatabase(t *testing.T) {
	w := restoreWorld(t)
	finished := time.Now().UTC().Truncate(time.Second)
	w.status(map[string]any{"restore": true, "state": "done", "id": restoreOther, "action": "restore", "step": "done", "subject": restoreFile, "source": "backup",
		"by": "boss", "message": "BOBRES now runs the data of x.", "requested_at": finished.Add(-5 * time.Minute).Format(time.RFC3339),
		"started_at": finished.Add(-4 * time.Minute).Format(time.RFC3339), "finished_at": finished.Format(time.RFC3339)})
	if w.rows() != 0 {
		t.Fatal("a row is there")
	}
	for i := 0; i < 3; i++ {
		if err := w.up.Step(w.ctx); err != nil {
			t.Fatal(err)
		}
	}
	last, err := w.s.LastSystemRestore(w.ctx, w.s.Conn())
	if err != nil || last.ID != restoreOther || last.Outcome != "done" || last.Subject != restoreFile || last.Source != "backup" || last.RequestedByName != "boss" ||
		!last.RequestedAt.Equal(finished.Add(-5*time.Minute)) || last.Message != "BOBRES now runs the data of x." || last.Action != store.ActionRestore {
		t.Fatalf("last: %+v %v", last, err)
	}
	a := w.audits("system.restore.done")
	if len(a) != 1 || a[0].actor != nil || a[0].after["backup"] != restoreFile || a[0].after["by"] != "boss" {
		t.Fatalf("audit: %+v", a)
	}
	notices := w.backupNotices()
	if len(notices) != 2 {
		t.Fatalf("notices: %+v", notices)
	}
	for _, e := range notices {
		if e.Kind != events.BackupRestore || e.State != events.UpdateDone || e.File != restoreFile || e.By != "boss" {
			t.Errorf("notice: %+v", e)
		}
	}
	// a core that has just started (no memory of it) writes nothing more: the id is the key
	fresh := w.svc.NewUpdates(UpdatesConfig{Source: w.src, Helper: w.dir, Version: "0.6.1", Domain: "panel.example.com"})
	if err := fresh.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.rows() != 1 || len(w.audits("system.restore.done")) != 1 || len(w.backupNotices()) != 2 {
		t.Fatalf("rows %d, audits %d, notices %d", w.rows(), len(w.audits("system.restore.done")), len(w.backupNotices()))
	}
	// and the page finds it as the restore that ended last
	p, err := w.up.Progress(w.ctx)
	if err != nil || p.Run != nil || p.LastRestore == nil || p.LastRestore.ID != restoreOther || p.Last != nil {
		t.Fatalf("progress: %+v %v", p, err)
	}
}

func TestRestoreStepRecordsOnlyWhatIsRecent(t *testing.T) {
	finished := time.Now().UTC().Truncate(time.Second)
	base := map[string]any{"restore": true, "state": "done", "id": restoreOther, "action": "restore", "step": "done", "subject": restoreFile, "source": "backup",
		"by": "boss", "requested_at": finished.Add(-5 * time.Minute).Format(time.RFC3339), "finished_at": finished.Format(time.RFC3339)}
	with := func(over map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	for name, st := range map[string]map[string]any{
		"a restore of another day":   with(map[string]any{"finished_at": finished.Add(-4 * time.Hour).Format(time.RFC3339)}),
		"one that has not ended":     with(map[string]any{"state": "running", "finished_at": ""}),
		"an update":                  with(map[string]any{"action": "update"}),
		"a status of no action":      with(map[string]any{"action": ""}),
		"no id":                      with(map[string]any{"id": ""}),
		"no backup":                  with(map[string]any{"subject": ""}),
		"no source":                  with(map[string]any{"source": ""}),
		"no time of the request":     with(map[string]any{"requested_at": ""}),
		"a finish in the far future": with(map[string]any{"finished_at": finished.Add(time.Hour).Format(time.RFC3339)}),
		"a source nobody knows":      with(map[string]any{"source": "url"}),
	} {
		t.Run(name, func(t *testing.T) {
			w := restoreWorld(t)
			w.status(st)
			if err := w.up.Step(w.ctx); err != nil {
				t.Fatal(err)
			}
			if w.rows() != 0 || len(w.backupNotices()) != 0 {
				t.Fatalf("recorded: %d rows", w.rows())
			}
		})
	}
	// an update's final status is not a restore's, and is not recorded as one
	w := restoreWorld(t)
	w.status(map[string]any{"restore": true, "state": "done", "id": restoreOther, "action": "update", "from": "0.6.1", "to": "0.7.0", "finished_at": finished.Format(time.RFC3339)})
	if err := w.up.Step(w.ctx); err != nil || w.rows() != 0 {
		t.Fatalf("%v %d", err, w.rows())
	}
}

// A restore row is not ended because core "now runs the release it asked for": a restore asks for none.
func TestRestoreStepDoesNotMistakeItForAnUpdate(t *testing.T) {
	w := restoreWorld(t)
	run, err := w.applyRestore(nil)
	if err != nil {
		t.Fatal(err)
	}
	w.helperIdle() // the status says nothing of it
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := w.up.Progress(w.ctx); p.Run == nil || p.Run.ID != run.ID {
		t.Fatalf("the restore was ended by the version: %+v", p)
	}
}

// The helper dies in the middle of the restore: the page does not wait for ever.
func TestRestoreThatTheHelperNeverFinishesEnds(t *testing.T) {
	w := restoreWorld(t)
	run, err := w.applyRestore(nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-5 * time.Minute)
	w.status(map[string]any{"restore": true, "state": "running", "id": run.ID, "action": "restore", "step": "restore", "subject": restoreFile, "source": "backup",
		"by": "boss", "started_at": started.UTC().Format(time.RFC3339)})
	w.wrote(started) // and never written again
	w.up.now = func() time.Time { return started.Add(11 * time.Minute) }
	p, err := w.up.Progress(w.ctx)
	if err != nil || p.Helper.Status.State != sysupdate.StateFailed || !strings.Contains(p.Helper.Status.Message, "interrupted") {
		t.Fatalf("progress: %+v %v", p.Helper, err)
	}
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	last, err := w.s.LastSystemRestore(w.ctx, w.s.Conn())
	if err != nil || last.ID != run.ID || last.Outcome != events.UpdateFailed || !strings.Contains(last.Message, "interrupted") {
		t.Fatalf("last: %+v %v", last, err)
	}
	if len(w.audits("system.restore.failed")) != 1 {
		t.Fatal("not audited")
	}
	// the dead helper's request does not stay for later
	w.noRequest()
}

func TestRestoreRequestNobodyTookUpIsWithdrawn(t *testing.T) {
	w := restoreWorld(t)
	run, err := w.applyRestore(nil)
	if err != nil {
		t.Fatal(err)
	}
	w.up.now = func() time.Time { return time.Now().Add(16 * time.Minute) }
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	last, err := w.s.LastSystemRestore(w.ctx, w.s.Conn())
	if err != nil || last.ID != run.ID || last.Outcome != events.UpdateNotStarted {
		t.Fatalf("last: %+v %v", last, err)
	}
	w.noRequest()
}

func TestRestoreStuckRestoreGetsLongerThanAnUpdate(t *testing.T) {
	w := restoreWorld(t)
	run, err := w.applyRestore(nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-90 * time.Minute) // an update would have been given up on
	w.status(map[string]any{"restore": true, "state": "running", "id": run.ID, "action": "restore", "step": "restore", "subject": restoreFile, "source": "backup",
		"by": "boss", "started_at": started.UTC().Format(time.RFC3339)})
	w.wrote(time.Now())
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := w.up.Progress(w.ctx); p.Run == nil {
		t.Fatal("a restore of an hour and a half was given up on")
	}
	w.up.now = func() time.Time { return time.Now().Add(4 * time.Hour) }
	w.wrote(w.up.now()) // still alive, still running
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := w.up.Progress(w.ctx); p.Run != nil {
		t.Fatal("a restore of over three hours was not given up on")
	}
}

func TestRestoreDownloadIsRecordedBeforeAnythingIsSent(t *testing.T) {
	w := restoreWorld(t)
	if err := w.up.RecordBackupDownload(w.ctx, w.owner, restoreFile, 4096); err != nil {
		t.Fatal(err)
	}
	a := w.audits("system.backup.download")
	if len(a) != 1 || a[0].actor == nil || *a[0].actor != w.owner.ID || a[0].entity != "backup" || a[0].entityID != restoreFile ||
		a[0].after["size"] != float64(4096) || a[0].after["by"] != "boss" {
		t.Fatalf("audit: %+v", a)
	}
	got := w.backupNotices()
	if len(got) != 2 {
		t.Fatalf("notices: %+v", got)
	}
	for _, e := range got {
		if e.Kind != events.BackupDownload || e.State != events.BackupDownloaded || e.File != restoreFile || e.By != "boss" || e.Size != 4096 {
			t.Errorf("notice: %+v", e)
		}
	}
	langs := map[string]bool{}
	for _, e := range got {
		langs[e.Language] = true
	}
	if !langs["fa"] || !langs["en"] {
		t.Errorf("languages: %v", langs)
	}
	admin, _ := w.s.GetUserByTelegramID(w.ctx, w.s.Conn(), updAdminTG)
	if err := w.up.RecordBackupDownload(w.ctx, admin, restoreFile, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an admin: %v", err)
	}
	if err := w.up.RecordBackupDownload(w.ctx, nil, restoreFile, 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("nobody: %v", err)
	}
	if len(w.audits("system.backup.download")) != 1 {
		t.Fatal("a refused download was audited")
	}
}

func TestRestorePurgesWhatIsLeftInTheUploadFolder(t *testing.T) {
	w := restoreWorld(t)
	st, err := w.up.StageRestoreUpload(w.ctx, w.owner, "x.bkp", strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(w.dir.Path(), sysupdate.UploadDirName)
	stale := filepath.Join(folder, restoreOther+".bkp")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := w.up.Step(w.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the leftover is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(folder, st.ID+".bkp")); err != nil {
		t.Fatalf("the upload waiting for confirmation was removed: %v", err)
	}
}

func TestRestoreCanRestoreSaysWhyNot(t *testing.T) {
	w := restoreWorld(t)
	check := func(want string) {
		t.Helper()
		p, err := w.up.Progress(w.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := w.up.CanRestore(p); got != want {
			t.Fatalf("CanRestore = %q, want %q", got, want)
		}
	}
	check("")
	w.status(map[string]any{"restore": false})
	check(RestoreUnsupported)
	w.status(map[string]any{"installed": false})
	check(UpdateHelperMissing)
	w.canRestore()
	w.up.storeDomain = ""
	check(RestoreNoDomain)
	w.up.storeDomain = "panel.example.com"
	if _, err := w.applyRestore(nil); err != nil {
		t.Fatal(err)
	}
	check(UpdateAlreadyRunning)
}
