package sysupdate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testRestoreID = "0192f9e0-7a1b-7c3d-8e4f-123456789abc"
	testUploadID  = "0192f9e0-7a1b-7c3d-8e4f-aaaaaaaaaaaa"
	testBackup    = "bobres-20261011T033000Z.bkp"
)

var testSum = strings.Repeat("ab", 32)

func TestRestoreRequestIsOneValidLine(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	r, err := NewRestoreRequest(testRestoreID, SourceBackup, testBackup, "", testSum, 4096, "  sobhan\n", now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"v":1,"id":"` + testRestoreID + `","action":"restore","source":"backup","file":"` + testBackup + `","upload":"","sha256":"` + testSum +
		`","size":4096,"by":"sobhan","at":"2026-10-20T12:00:00Z"}` + "\n"
	if string(b) != want {
		t.Fatalf("%s\nwant %s", b, want)
	}
	if bytes.Count(b, []byte("\n")) != 1 {
		t.Fatal("not one line")
	}
	// An upload's name is made a plain line of at most 120 characters, and it is never a path component of the request.
	u, err := NewRestoreRequest(testRestoreID, SourceUpload, "a‮b\nc"+strings.Repeat("x", 300), testUploadID, testSum, 1, "", now)
	if err != nil || strings.ContainsAny(u.File, "\n‮") || len([]rune(u.File)) > MaxFileName || u.By != "dashboard" {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestRestoreRequestRefusesWhatTheUpdaterWould(t *testing.T) {
	now := time.Now()
	ok := func(mod func(*RestoreRequest)) error {
		r, err := NewRestoreRequest(testRestoreID, SourceBackup, testBackup, "", testSum, 10, "o", now)
		if err != nil {
			t.Fatal(err)
		}
		mod(&r)
		return r.Validate()
	}
	for name, mod := range map[string]func(*RestoreRequest){
		"another version":          func(r *RestoreRequest) { r.V = 2 },
		"an id in capitals":        func(r *RestoreRequest) { r.ID = strings.ToUpper(r.ID) },
		"another action":           func(r *RestoreRequest) { r.Action = "update" },
		"a path":                   func(r *RestoreRequest) { r.File = "../" + testBackup },
		"a sidecar":                func(r *RestoreRequest) { r.File = "bobres-20261011T033000Z.json" },
		"an upload id on a backup": func(r *RestoreRequest) { r.Upload = testUploadID },
		"an upload without id":     func(r *RestoreRequest) { r.Source, r.File, r.Upload = SourceUpload, "x.bkp", "" },
		"another source":           func(r *RestoreRequest) { r.Source = "url" },
		"a short hash":             func(r *RestoreRequest) { r.SHA256 = "ab" },
		"a hash in capitals":       func(r *RestoreRequest) { r.SHA256 = strings.ToUpper(testSum) },
		"size 0":                   func(r *RestoreRequest) { r.Size = 0 },
		"over the limit":           func(r *RestoreRequest) { r.Size = MaxUpload + 1 },
		"a name with a break":      func(r *RestoreRequest) { r.By = "a\nb" },
		"a time with an offset":    func(r *RestoreRequest) { r.At = "2026-10-20T12:00:00+03:30" },
	} {
		if err := ok(mod); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := ok(func(*RestoreRequest) {}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteRestoreRequestAndRemoveByID(t *testing.T) {
	d := NewDir(t.TempDir())
	r, err := NewRestoreRequest(testRestoreID, SourceBackup, testBackup, "", testSum, 10, "o", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.WriteRestoreRequest(r); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(filepath.Join(d.Path(), RequestFile))
	if err != nil || fi.Mode().Perm() != RequestMode || !fi.Mode().IsRegular() {
		t.Fatalf("%v %v", fi, err)
	}
	// A request of another id is left; this one's is removed, whatever its action.
	if ok, err := d.RemoveRequest("0192f9e0-7a1b-7c3d-8e4f-000000000001"); ok || err != nil {
		t.Fatalf("another id: %v %v", ok, err)
	}
	if ok, err := d.RemoveRequest(testRestoreID); !ok || err != nil {
		t.Fatalf("its own id: %v %v", ok, err)
	}
	if ok, err := d.RemoveRequest(testRestoreID); ok || err != nil {
		t.Fatalf("a second time: %v %v", ok, err)
	}
}

func TestStageUploadWritesAFileOfItsOwnAndReportsItsHash(t *testing.T) {
	d := NewDir(t.TempDir())
	body := bytes.Repeat([]byte("backup bytes "), 5000)
	up, err := d.StageUpload(testUploadID, bytes.NewReader(body), MaxUpload)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if up.ID != testUploadID || up.Size != int64(len(body)) || up.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("%+v", up)
	}
	fi, err := os.Lstat(filepath.Join(d.Path(), UploadDirName, testUploadID+".bkp"))
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != UploadMode || fi.Size() != up.Size {
		t.Fatalf("%v %v", fi, err)
	}
	if st, err := os.Lstat(filepath.Join(d.Path(), UploadDirName)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("the upload folder: %v %v", st, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(d.Path(), UploadDirName)); len(entries) != 1 {
		t.Fatalf("%d entries: only the upload should be there", len(entries))
	}
}

func TestStageUploadRefusesAndLeavesNothing(t *testing.T) {
	left := func(d *Dir) int {
		entries, _ := os.ReadDir(filepath.Join(d.Path(), UploadDirName))
		return len(entries)
	}
	d := NewDir(t.TempDir())
	if _, err := d.StageUpload(testUploadID, bytes.NewReader(make([]byte, 2001)), 2000); !errors.Is(err, ErrTooLarge) || left(d) != 0 {
		t.Fatalf("too large: %v, %d left", err, left(d))
	}
	// One byte over is too large, the limit itself is not.
	if _, err := d.StageUpload(testUploadID, bytes.NewReader(make([]byte, 2000)), 2000); err != nil {
		t.Fatalf("exactly the limit: %v", err)
	}
	_ = d.RemoveUpload(testUploadID)
	if _, err := d.StageUpload(testUploadID, bytes.NewReader(nil), 2000); !errors.Is(err, ErrEmptyUpload) || left(d) != 0 {
		t.Fatalf("empty: %v, %d left", err, left(d))
	}
	// A connection that breaks half way (the browser went away) leaves nothing.
	broken := io.MultiReader(bytes.NewReader(make([]byte, 100)), errReader{})
	if _, err := d.StageUpload(testUploadID, broken, 2000); err == nil || left(d) != 0 {
		t.Fatalf("broken: %v, %d left", err, left(d))
	}
	for _, id := range []string{"", "../x", strings.ToUpper(testUploadID), testUploadID + "/../x"} {
		if _, err := d.StageUpload(id, bytes.NewReader([]byte("x")), 10); err == nil {
			t.Errorf("the id %q was accepted", id)
		}
		if err := d.RemoveUpload(id); err == nil {
			t.Errorf("the id %q was removed", id)
		}
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// The upload folder is core's, but whatever is found there is not followed.
func TestStageUploadNeverFollowsALink(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := NewDir(t.TempDir())
	folder := filepath.Join(d.Path(), UploadDirName)
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	// Links at the temporary name and at the final name are replaced, not written through.
	if err := os.Symlink(victim, filepath.Join(folder, "."+testUploadID+".part")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(folder, testUploadID+".bkp")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.StageUpload(testUploadID, bytes.NewReader([]byte("new backup")), 100); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me" {
		t.Fatalf("the link's target was written: %q", b)
	}
	if fi, err := os.Lstat(filepath.Join(folder, testUploadID+".bkp")); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the upload is %v (%v)", fi, err)
	}

	// An upload folder that is itself a link is refused.
	d2 := NewDir(t.TempDir())
	real := t.TempDir()
	if err := os.Symlink(real, filepath.Join(d2.Path(), UploadDirName)); err != nil {
		t.Fatal(err)
	}
	if _, err := d2.StageUpload(testUploadID, bytes.NewReader([]byte("x")), 10); err == nil {
		t.Fatal("an upload folder that is a link was used")
	}
	if entries, _ := os.ReadDir(real); len(entries) != 0 {
		t.Fatalf("something was written through the link: %v", entries)
	}
	// So is a folder of core's that is a link.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDir(link).StageUpload(testUploadID, bytes.NewReader([]byte("x")), 10); !errors.Is(err, ErrNoDir) {
		t.Fatalf("a folder that is a link: %v", err)
	}
}

// Uploads staged at the same time do not mix: each is its own file with its own hash.
func TestStageUploadInParallel(t *testing.T) {
	d := NewDir(t.TempDir())
	var wg sync.WaitGroup
	ids := []string{"0192f9e0-7a1b-7c3d-8e4f-000000000001", "0192f9e0-7a1b-7c3d-8e4f-000000000002", "0192f9e0-7a1b-7c3d-8e4f-000000000003"}
	sums := make([]string, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := bytes.Repeat([]byte{byte('a' + i)}, 70_000+i)
			up, err := d.StageUpload(id, bytes.NewReader(body), MaxUpload)
			if err != nil {
				t.Error(err)
				return
			}
			sums[i] = up.SHA256
		}()
	}
	wg.Wait()
	for i, id := range ids {
		b, err := os.ReadFile(filepath.Join(d.Path(), UploadDirName, id+".bkp"))
		sum := sha256.Sum256(b)
		if err != nil || len(b) != 70_000+i || hex.EncodeToString(sum[:]) != sums[i] {
			t.Errorf("%s: %d bytes %v", id, len(b), err)
		}
	}
}

func TestPurgeUploadsRemovesOldLeftoversOnly(t *testing.T) {
	d := NewDir(t.TempDir())
	folder := filepath.Join(d.Path(), UploadDirName)
	if n, err := d.PurgeUploads(nil, time.Hour, time.Now()); n != 0 || err != nil {
		t.Fatalf("no folder: %d %v", n, err)
	}
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	old, fresh, kept := "0192f9e0-7a1b-7c3d-8e4f-000000000001", "0192f9e0-7a1b-7c3d-8e4f-000000000002", "0192f9e0-7a1b-7c3d-8e4f-000000000003"
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{old + ".bkp", "." + old + ".part", kept + ".bkp", fresh + ".bkp", "junk"} {
		if err := os.WriteFile(filepath.Join(folder, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(folder, "link")); err != nil {
		t.Fatal(err)
	}
	then := time.Now().Add(-3 * time.Hour)
	for _, n := range []string{old + ".bkp", "." + old + ".part", kept + ".bkp", "junk"} {
		if err := os.Chtimes(filepath.Join(folder, n), then, then); err != nil {
			t.Fatal(err)
		}
	}
	n, err := d.PurgeUploads(map[string]bool{kept: true}, 2*time.Hour, time.Now())
	if err != nil || n != 3 { // old, its part and junk
		t.Fatalf("removed %d: %v", n, err)
	}
	var left []string
	entries, _ := os.ReadDir(folder)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if len(left) != 3 { // the kept one, the fresh one and the link (as new as it is)
		t.Fatalf("left %v", left)
	}
	// Once the link is old enough it is removed itself, never followed.
	n, err = d.PurgeUploads(map[string]bool{kept: true}, 2*time.Hour, time.Now().Add(3*time.Hour))
	if err != nil || n != 2 { // the fresh one and the link
		t.Fatalf("removed %d: %v", n, err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep me" {
		t.Fatal("what the link points at was removed")
	}
}

func TestReadStatusOfARestore(t *testing.T) {
	dir := t.TempDir()
	status := `{"v":1,"installed":true,"helper":"0.8.0","state":"done","id":"` + testRestoreID + `","from":"0.8.0","to":"0.8.0","channel":"","step":"done",` +
		`"message":"BOBRES now runs the data of x.","action":"restore","restore":true,"subject":"my\nbackup‮.bkp","source":"upload","by":"sobhan",` +
		`"requested_at":"2026-10-20T11:59:00Z","started_at":"2026-10-20T12:00:00Z","finished_at":"2026-10-20T12:05:00Z","log":[]}`
	if err := os.WriteFile(filepath.Join(dir, StatusFile), []byte(status), 0o644); err != nil { //nolint:gosec // as the updater writes it
		t.Fatal(err)
	}
	st, err := NewDir(dir).ReadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.Action != ActionRestore || !st.Restore || st.Source != SourceUpload || st.By != "sobhan" || st.Step != StepDone || st.RequestedAt == nil ||
		st.RequestedAt.Format(time.RFC3339) != "2026-10-20T11:59:00Z" || strings.ContainsAny(st.Subject, "\n‮") || st.Subject != "my backup.bkp" {
		t.Fatalf("%+v", st)
	}
	// What the status says that core does not know is dropped, not shown.
	weird := strings.NewReplacer(`"action":"restore"`, `"action":"rm -rf"`, `"source":"upload"`, `"source":"http://x"`, `"step":"done"`, `"step":"format"`).Replace(status)
	if err := os.WriteFile(filepath.Join(dir, StatusFile), []byte(weird), 0o644); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}
	st, err = NewDir(dir).ReadStatus()
	if err != nil || st.Action != "" || st.Source != "" || st.Step != "" {
		t.Fatalf("%+v %v", st, err)
	}
}
