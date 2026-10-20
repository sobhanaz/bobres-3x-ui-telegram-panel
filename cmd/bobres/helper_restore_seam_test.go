package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Where core's side of the restore (internal/sysupdate: the request it writes, the upload it
// stages, the status it reads) meets the helper's (this package): what one side writes, the other
// reads, byte for byte, with the checks of both sides in place.

func TestSeamTheLimitsAreTheSameOnBothSides(t *testing.T) {
	if sysupdate.MaxUpload != maxRestoreBytes || sysupdate.MaxRequest != maxRequestBytes || sysupdate.MaxFileName != maxRestoreFile ||
		sysupdate.MaxBy != maxRequestBy || sysupdate.UploadDirName != uploadDirName || sysupdate.ActionRestore != actionRestore ||
		sysupdate.SourceBackup != sourceBackup || sysupdate.SourceUpload != sourceUpload || sysupdate.ProtocolVersion != requestProtocol {
		t.Fatal("core and the helper disagree about the protocol's constants")
	}
	// The steps the helper reports are the steps core knows.
	for helper, core := range map[string]string{stepVerifying: sysupdate.StepVerify, stepSafety: sysupdate.StepSafety, stepStopping: sysupdate.StepStop,
		stepRestoring: sysupdate.StepRestore, stepStarting: sysupdate.StepStart, stepWaiting: sysupdate.StepHealth, stepRollingBk: sysupdate.StepRollback,
		stepFinished: sysupdate.StepDone} {
		if helper != core {
			t.Errorf("step %q is %q on core's side", helper, core)
		}
	}
}

// coreRestoreRequest writes a restore request the way core does and reads it back the way the helper does.
func coreRestoreRequest(t *testing.T, dir string, r sysupdate.RestoreRequest, now time.Time) (*restoreRequest, error) {
	t.Helper()
	if err := sysupdate.NewDir(dir).WriteRestoreRequest(r); err != nil {
		t.Fatalf("core cannot write the request: %v", err)
	}
	raw, err := readRequestFile(dir, os.Geteuid())
	if err != nil {
		t.Fatalf("the helper cannot read the file core wrote: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, requestFileName)); err != nil {
		t.Fatal(err)
	}
	if action := requestAction(raw); action != actionRestore {
		t.Fatalf("the helper takes a restore request for a %q", action)
	}
	return parseRestoreRequest(raw, now.Add(time.Second))
}

func TestSeamEveryRestoreRequestCoreWritesIsAccepted(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	sum := strings.Repeat("0f", 32)
	for _, tc := range []struct {
		name, source, file, by string
		size                   int64
	}{
		{"a daily backup", "backup", "bobres-20261011T033000Z.bkp", "sobhan", 12345},
		{"a safety backup", "backup", "bobres-20261011T120000Z-before-restore.bkp", "tg:7102607089", 1},
		{"a rotation backup", "backup", "bobres-20261011T120000Z-before-rotation.bkp", "owner", sysupdate.MaxUpload},
		{"an upload", "upload", "my backup (3).bkp", "owner", 999},
		{"an upload with a Persian name", "upload", "پشتیبان فروشگاه.bkp", "سبحان آذرزاده", 999},
		{"an upload name at the limit", "upload", strings.Repeat("ب", sysupdate.MaxFileName), "owner", 999},
		{"an upload name with controls and bidi", "upload", "a\u202eb\nc\x00d.bkp", "a\nb", 999},
		{"an upload name too long", "upload", strings.Repeat("x", 500), "owner", 999},
		{"no name", "backup", "bobres-20261011T033000Z.bkp", "", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			id, _ := uuid.NewV7()
			up, _ := uuid.NewV7()
			upload := ""
			if tc.source == "upload" {
				upload = up.String()
			}
			req, err := sysupdate.NewRestoreRequest(id.String(), tc.source, tc.file, upload, sum, tc.size, tc.by, now)
			if err != nil {
				t.Fatalf("core cannot make the request: %v", err)
			}
			got, err := coreRestoreRequest(t, dir, req, now)
			if err != nil {
				t.Fatalf("the helper refuses a request core makes: %v", err)
			}
			if got.ID != id.String() || got.Source != tc.source || got.Upload != upload || got.SHA256 != sum || got.Size != tc.size || !got.At.Equal(now) {
				t.Fatalf("the request changed on the way: %+v", got)
			}
			if tc.source == "backup" && got.File != tc.file {
				t.Fatalf("the file name changed: %q", got.File)
			}
			if strings.ContainsAny(got.By+got.File, "\n\x00\u202e") {
				t.Fatalf("not plain lines: %q %q", got.By, got.File)
			}
		})
	}
}

func TestSeamCoreWillNotMakeARequestTheHelperWouldRefuse(t *testing.T) {
	now := time.Now().UTC()
	id, _ := uuid.NewV7()
	sum := strings.Repeat("0f", 32)
	for name, args := range map[string][]any{
		"a path":          {"backup", "../bobres-20261011T033000Z.bkp", "", sum, int64(10)},
		"a sidecar":       {"backup", "bobres-20261011T033000Z.json", "", sum, int64(10)},
		"an upload id":    {"backup", "bobres-20261011T033000Z.bkp", id.String(), sum, int64(10)},
		"no upload id":    {"upload", "x.bkp", "", sum, int64(10)},
		"a bad hash":      {"backup", "bobres-20261011T033000Z.bkp", "", "xyz", int64(10)},
		"no size":         {"backup", "bobres-20261011T033000Z.bkp", "", sum, int64(0)},
		"too large":       {"backup", "bobres-20261011T033000Z.bkp", "", sum, int64(sysupdate.MaxUpload) + 1},
		"another source":  {"url", "bobres-20261011T033000Z.bkp", "", sum, int64(10)},
		"an empty upload": {"upload", "", id.String(), sum, int64(10)},
	} {
		if _, err := sysupdate.NewRestoreRequest(id.String(), args[0].(string), args[1].(string), args[2].(string), args[3].(string), args[4].(int64), "owner", now); err == nil {
			t.Errorf("%s: core made a request", name)
		}
	}
}

func TestSeamAnUploadCoreStagesIsTheFileTheHelperCopiesAndChecks(t *testing.T) {
	rt := newRestoreTest(t)
	src := writeBackup(t, installKey, backupKeys(), "DUMP OF THE UPLOAD")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := uuid.NewV7()
	rid, _ := uuid.NewV7()
	core := sysupdate.NewDir(updateDirPath(rt.dir))
	up, err := core.StageUpload(id.String(), bytes.NewReader(body), sysupdate.MaxUpload)
	if err != nil {
		t.Fatalf("core cannot stage the upload: %v", err)
	}
	// What core left: the upload and nothing else, in a folder only it can write.
	fi, err := os.Lstat(uploadPath(rt.dir, id.String()))
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || fi.Size() != up.Size {
		t.Fatalf("the upload is %v (%v)", fi, err)
	}
	if entries, _ := os.ReadDir(uploadDirPath(rt.dir)); len(entries) != 1 {
		t.Fatalf("core left %d entries", len(entries))
	}
	if err := checkUploadDir(rt.dir, os.Geteuid()); err != nil {
		t.Fatalf("the helper does not trust the folder core made: %v", err)
	}
	req, err := sysupdate.NewRestoreRequest(rid.String(), "upload", "backup.bkp", id.String(), up.SHA256, up.Size, "owner", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := coreRestoreRequest(t, updateDirPath(rt.dir), req, time.Now())
	if err != nil {
		t.Fatalf("the helper refuses the request: %v", err)
	}
	copyFile, done, err := rt.h.stageBackup(parsed)
	if err != nil {
		t.Fatalf("the helper cannot take the upload: %v", err)
	}
	defer done()
	if _, err := verifyBackup(copyFile, []byte(installKey)); err != nil {
		t.Fatalf("the copy is not the backup: %v", err)
	}
	// Core's size limit stops an upload before the helper's would.
	_, err = core.StageUpload(id.String(), bytes.NewReader(make([]byte, 2000)), 1000)
	if !errors.Is(err, sysupdate.ErrTooLarge) {
		t.Fatalf("an upload over the limit: %v", err)
	}
	if _, err := os.Lstat(uploadPath(rt.dir, id.String()+".part")); err == nil {
		t.Fatal("a part of the upload was left")
	}
}

func TestSeamTheStatusOfARestoreIsUnderstoodByCore(t *testing.T) {
	id, _ := uuid.NewV7()
	started := time.Now().UTC().Add(-3 * time.Minute).Truncate(time.Second)
	asked := started.Add(-time.Minute)
	finished := started.Add(2 * time.Minute)
	for _, state := range []string{helperRunning, helperDone, helperFailed, helperRolledBack} {
		for _, step := range []string{stepVerifying, stepSafety, stepStopping, stepRestoring, stepStarting, stepWaiting, stepRollingBk, stepFinished} {
			st := helperStatus{V: requestProtocol, Installed: true, Helper: "0.8.0", State: state, ID: id.String(), From: "0.8.0", To: "0.8.0", Step: step,
				Message: "Restoring the database.", Action: actionRestore, Subject: "bobres-20261011T033000Z.bkp", Source: sourceBackup, By: "sobhan",
				RequestedAt: stamp(asked), StartedAt: stamp(started)}
			if state != helperRunning {
				st.FinishedAt = stamp(finished)
			}
			got, err := helperStatusFor(t, st)
			if err != nil {
				t.Fatalf("core cannot read the %s status at step %s: %v", state, step, err)
			}
			if got.State != state || got.Step != step || got.ID != id.String() || got.Action != "restore" || got.Subject != "bobres-20261011T033000Z.bkp" ||
				got.Source != "backup" || got.By != "sobhan" || !got.Restore || got.RequestedAt == nil || !got.RequestedAt.Equal(asked) {
				t.Fatalf("core reads %+v, not what the helper wrote (%s, %s)", got, state, step)
			}
		}
	}
	// An idle status already says the helper can restore, and an update's says it is an update.
	got, err := helperStatusFor(t, idleStatus(true, "0.8.0"))
	if err != nil || !got.Restore || got.Action != "" {
		t.Fatalf("idle: %+v %v", got, err)
	}
}

func TestSeamAStatusFromBeforeTheRestoreCannotRestore(t *testing.T) {
	dir := t.TempDir()
	old := `{"v":1,"installed":true,"helper":"0.7.0","state":"idle","id":"","from":"","to":"","channel":"","step":"","message":"","started_at":null,"finished_at":null,"log":[]}`
	if err := os.WriteFile(filepath.Join(dir, statusFileName), []byte(old), 0o644); err != nil { //nolint:gosec // as the helper writes it
		t.Fatal(err)
	}
	got, err := sysupdate.NewDir(dir).ReadStatus()
	if err != nil || got.Restore || got.Action != "" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSeamBackupNamesTheBackupServiceMakesAreAcceptedByTheHelper(t *testing.T) {
	for _, name := range []string{backup.FileName(time.Now()), safetyName(time.Now(), "before-restore"), safetyName(time.Now(), "before-rotation")} {
		if _, err := parseRestoreRequest(restoreReq(set("file", name)), reqNow); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
