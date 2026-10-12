package backupsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/backup"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/s3"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/s3/s3test"
)

// fakeS3 is an S3 fake and a client for it (path style).
func fakeS3(t *testing.T, mod func(*s3.Config)) (*s3test.Fake, *s3.Client) {
	t.Helper()
	f := s3test.New("bobres-backups")
	t.Cleanup(f.Server.Close)
	c := s3.Config{Endpoint: f.URL(), Bucket: f.Bucket, Region: f.Region, AccessKey: f.AccessKey, SecretKey: f.SecretKey,
		HTTPClient: f.Client(false), Sleep: func(context.Context, time.Duration) error { return nil }}
	if mod != nil {
		mod(&c)
	}
	cl, err := s3.New(c)
	if err != nil {
		t.Fatal(err)
	}
	return f, cl
}

// Each backup goes to the bucket under the prefix, its sidecar says when and
// where, and the bucket keeps the newest Keep of the service's own backups:
// other names and folders there are never touched.
func TestBackupsAreCopiedToS3(t *testing.T) {
	f, cl := fakeS3(t, nil)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, k := range []string{"shop/notes.txt", "shop/bobres-20250101T000000Z.json", "shop/sub/bobres-20250101T000000Z.bkp",
		"other/bobres-20250101T000000Z.bkp", "shop/bobres-20250101T000000Z-before-restore.bkp"} {
		f.Put(k, []byte("not ours"), old)
	}
	f.Put("shop/bobres-20250101T000000Z.bkp", []byte("an old backup"), old) // the oldest of ours
	r, dir := newRunner(t, func(o *Options) { o.Offsite = Offsite{Client: cl, Prefix: "shop/", Keep: 2} })

	var made []string
	for range 3 {
		sc, err := r.RunOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, sc.File)
		raw, _ := os.ReadFile(filepath.Join(dir, sc.File))
		if got, ok := f.Object("shop/" + sc.File); !ok || !bytes.Equal(got, raw) {
			t.Fatalf("%s in the bucket: %v (%d of %d bytes)", sc.File, ok, len(got), len(raw))
		}
		disk, err := backup.ReadSidecar(filepath.Join(dir, backup.SidecarName(sc.File)))
		if err != nil || disk.UploadedAt == nil || disk.UploadedTo != "s3://bobres-backups/shop/"+sc.File || disk.UploadError != "" ||
			sc.UploadedAt == nil || sc.Error != "" {
			t.Fatalf("sidecar %+v %v", disk, err)
		}
	}
	want := []string{"other/bobres-20250101T000000Z.bkp", "shop/bobres-20250101T000000Z-before-restore.bkp",
		"shop/bobres-20250101T000000Z.json", "shop/" + made[1], "shop/" + made[2], "shop/notes.txt", "shop/sub/bobres-20250101T000000Z.bkp"}
	if got := f.Keys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("bucket after three backups (keep 2):\n%v\nwant\n%v", got, want)
	}
	if bad := f.Refused(); len(bad) > 0 {
		t.Fatalf("signatures refused: %v", bad)
	}
	st := r.Status()
	if want := (OffsiteStatus{Configured: true, EndpointHost: strings.TrimPrefix(f.URL(), "https://"), Bucket: "bobres-backups",
		Prefix: "shop/", Keep: 2}); st.Offsite != want || st.LastError != "" {
		t.Fatalf("status offsite %+v, last error %q", st.Offsite, st.LastError)
	}
}

// A file over the multipart threshold goes up in parts and arrives whole.
func TestLargeBackupGoesUpInParts(t *testing.T) {
	f, cl := fakeS3(t, func(c *s3.Config) { c.Threshold, c.PartSize = 64<<10, 32<<10 })
	r, dir := newRunner(t, func(o *Options) {
		o.Offsite = Offsite{Client: cl, Keep: 5}
		o.Dump = DumpFunc(func(_ context.Context, w io.Writer) error {
			_, err := io.WriteString(w, strings.Repeat("PGDMP big table row ", 20000)) // ~400 KB
			return err
		})
	})
	sc, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, sc.File))
	if got, _ := f.Object(sc.File); !bytes.Equal(got, raw) || sc.UploadedTo != "s3://bobres-backups/"+sc.File {
		t.Fatalf("assembled %d of %d bytes, %+v", len(got), len(raw), sc)
	}
	parts := 0
	for _, req := range f.Requests() {
		if strings.Contains(req, "partNumber=") {
			parts++
		}
	}
	if parts < 2 || f.Uploads() != 0 {
		t.Fatalf("%d parts, %d uploads open", parts, f.Uploads())
	}
}

// A failed upload is recorded and nothing else: the backup is kept, the
// Telegram delivery happens, the run is a success, and the bucket's copies
// are left alone.
func TestFailedUploadKeepsTheBackup(t *testing.T) {
	f, cl := fakeS3(t, nil)
	f.Put("bobres-20250101T000000Z.bkp", []byte("old"), time.Now())
	f.Fail(func(r *http.Request) int {
		if r.Method == http.MethodPut {
			return http.StatusForbidden
		}
		return 0
	})
	delivered := false
	bot := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		delivered = true
		_, _ = io.WriteString(w, `{"sent":true,"message_id":1}`)
	}))
	defer bot.Close()
	r, dir := newRunner(t, func(o *Options) {
		o.BotURL, o.Token = bot.URL, botToken
		o.Offsite = Offsite{Client: cl, Keep: 1}
	})
	sc, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("a failed upload failed the backup: %v", err)
	}
	disk, _ := backup.ReadSidecar(filepath.Join(dir, backup.SidecarName(sc.File)))
	if !delivered || disk.SentAt == nil || disk.UploadedAt != nil || disk.Error != "" ||
		!strings.Contains(disk.UploadError, "HTTP 403") || !strings.Contains(disk.UploadError, "injected failure") {
		t.Fatalf("delivered %v, sidecar %+v", delivered, disk)
	}
	if got := files(t, dir, ".bkp"); len(got) != 1 {
		t.Fatalf("local backups %v", got)
	}
	if st := r.Status(); st.LastError != "" || st.Backups[0].UploadError == "" {
		t.Fatalf("status %+v", st)
	}
	if keys := f.Keys(); !reflect.DeepEqual(keys, []string{"bobres-20250101T000000Z.bkp"}) {
		t.Fatalf("the bucket was pruned after a failed upload: %v", keys)
	}

	// The storage cannot be reached at all: the same.
	f.Server.Close()
	sc, err = r.RunOnce(context.Background())
	if err != nil || sc.UploadError == "" || sc.UploadedAt != nil {
		t.Fatalf("unreachable storage: %v %+v", err, sc)
	}
}

// Settings that cannot be used (a bad value) are recorded on each backup.
func TestUploadNotSetUpIsRecorded(t *testing.T) {
	r, dir := newRunner(t, func(o *Options) {
		o.Offsite = Offsite{Problem: "BOBRES_BACKUP_S3_BUCKET is required with BOBRES_BACKUP_S3_ENDPOINT"}
	})
	sc, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	disk, _ := backup.ReadSidecar(filepath.Join(dir, backup.SidecarName(sc.File)))
	if disk.UploadError != "the S3 upload is not set up correctly: BOBRES_BACKUP_S3_BUCKET is required with BOBRES_BACKUP_S3_ENDPOINT" {
		t.Fatalf("sidecar %+v", disk)
	}
	if st := r.Status().Offsite; st.Configured || !strings.Contains(st.Error, "BOBRES_BACKUP_S3_BUCKET") {
		t.Fatalf("status %+v", st)
	}
}

// Without an upload the sidecar is as before (no upload fields), and the
// status says the upload is off.
func TestNoUploadLeavesTheSidecarAsBefore(t *testing.T) {
	r, dir := newRunner(t, nil)
	sc, err := r.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, backup.SidecarName(sc.File)))
	if strings.Contains(string(raw), "upload") {
		t.Fatalf("sidecar %s", raw)
	}
	b, _ := json.Marshal(r.Status())
	if !strings.Contains(string(b), `"offsite":{"configured":false}`) {
		t.Fatalf("status %s", b)
	}
	if _, err := New(Options{Dir: dir, Key: testKey, Dump: DumpFunc(nil), Offsite: Offsite{Client: &s3.Client{}, Keep: 0}}); err == nil {
		t.Fatal("a bucket keeping nothing was accepted")
	}
}

func TestReportSaysWhereTheCopyWent(t *testing.T) {
	at := time.Date(2026, 10, 11, 3, 31, 0, 0, time.UTC)
	var out bytes.Buffer
	_ = report(&out, backup.Sidecar{File: "bobres-x.bkp", Size: 10, UploadedAt: &at, UploadedTo: "s3://b/shop/bobres-x.bkp"}, "/backups")
	_ = report(&out, backup.Sidecar{File: "bobres-y.bkp", Size: 10, UploadError: "the storage answered HTTP 403: AccessDenied"}, "/backups")
	for _, w := range []string{"Copied off the server to s3://b/shop/bobres-x.bkp.",
		"Not copied off the server (the storage answered HTTP 403: AccessDenied); it is kept here."} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("report has no %q:\n%s", w, &out)
		}
	}
}
