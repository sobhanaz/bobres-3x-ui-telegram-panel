package backupclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
