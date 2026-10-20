package backupclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestDownloadStreamsTheBodyAndSaysItsSize(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 100_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Authorization") != "Bearer tok":
			http.Error(w, "no", http.StatusUnauthorized)
		case r.URL.Query().Get("name") == "bobres-20261011T033000Z.bkp":
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
		default:
			http.Error(w, "no such backup", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")
	rc, size, err := c.Download(context.Background(), "bobres-20261011T033000Z.bkp")
	if err != nil || size != int64(len(body)) {
		t.Fatalf("%d %v", size, err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("%d bytes: %v", len(got), err)
	}
	// A name that is not a backup's never reaches the service; one it does not have is ErrNoBackup.
	for _, name := range []string{"../etc/passwd", "bobres-20261011T033000Z.json", "", "bobres-20240101T000000Z.bkp"} {
		if _, _, err := c.Download(context.Background(), name); !errors.Is(err, ErrNoBackup) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, _, err := New(srv.URL, "wrong").Download(context.Background(), "bobres-20261011T033000Z.bkp"); err == nil || errors.Is(err, ErrNoBackup) {
		t.Errorf("a refused token: %v", err)
	}
	// The ten-second limit of the other calls does not cut a download off.
	if c.stream.Timeout != 0 {
		t.Errorf("the download client has a timeout of %v", c.stream.Timeout)
	}
}

// The backup service's answer reaches the dashboard as it is: the S3 upload's
// fields (each backup's uploaded_at, upload_error and uploaded_to, and the
// offsite object) included, and the summary still finds the newest good
// backup.
func TestStatusPassesTheUploadFieldsThrough(t *testing.T) {
	const answer = `{"backups":[` +
		`{"file":"bobres-20261012T033000Z.bkp","size":10,"sha256":"ab","created_at":"2026-10-12T03:30:00Z","sent_at":null,` +
		`"upload_error":"the storage answered HTTP 403: AccessDenied"},` +
		`{"file":"bobres-20261011T033000Z.bkp","size":10,"sha256":"cd","created_at":"2026-10-11T03:30:00Z","sent_at":null,` +
		`"uploaded_at":"2026-10-11T03:31:00Z","uploaded_to":"s3://shop/bobres-20261011T033000Z.bkp"}],` +
		`"running":false,"last_error":"","next_run":1760153400,"disk_free":1,"dir":"/backups",` +
		`"offsite":{"configured":true,"endpoint_host":"s3.example.com","bucket":"shop","keep":30}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Path != "/internal/backups" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(answer))
	}))
	defer srv.Close()
	raw, err := New(srv.URL, "tok").Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != answer {
		t.Fatalf("changed on the way:\n%s", raw)
	}
	var st struct {
		Backups []map[string]any `json:"backups"`
		Offsite map[string]any   `json:"offsite"`
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.Backups[1]["uploaded_at"] != "2026-10-11T03:31:00Z" ||
		st.Backups[0]["upload_error"] == nil || st.Offsite["configured"] != true {
		t.Fatalf("%+v %v", st, err)
	}
	sum, err := Summarize(raw)
	if err != nil || sum.LastCreatedAt == nil || sum.LastCreatedAt.Format("2006-01-02") != "2026-10-12" || sum.LastError != "" {
		t.Fatalf("summary %+v %v", sum, err)
	}
}
