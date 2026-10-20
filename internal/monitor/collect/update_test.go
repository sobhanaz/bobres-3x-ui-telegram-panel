package collect

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

const restorePointJSON = `{"from_version":"0.6.1","to_version":"0.7.0","backup_file":"/opt/bobres/backups/pre-update.dump.enc",
 "created_at":"2026-10-19T08:00:00Z","cli_path":"/usr/local/bin/bobres","cli_backup_path":"/opt/bobres/.restore-point/bobres",
 "files_dir":"/opt/bobres/.restore-point/files","schema_versions":{"core":29},"release_floor":"0.6.1","used_at":null}`

func TestLastUpdateKeepsOnlyVersionsAndTimes(t *testing.T) {
	c := baseCollector(t)
	fs := labFS(t, serverMountinfo)
	fs["/opt/bobres/.restore-point/restore-point.json"] = []byte(restorePointJSON)
	c.FS = fs
	u := c.lastUpdate()
	want := snapshot.LastUpdate{From: "0.6.1", To: "0.7.0", At: time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC).Unix()}
	if u == nil || *u != want {
		t.Fatalf("got %+v, want %+v", u, want)
	}
	// Nothing of the files and paths the restore point lists reaches the snapshot.
	s := c.Collect(t.Context())
	raw, err := json.Marshal(s.Update)
	if err != nil || strings.Contains(string(raw), "/opt/bobres") || strings.Contains(string(raw), "pre-update") || s.Update == nil {
		t.Fatalf("leaked: %s %v", raw, err)
	}

	// A restore point that was used says when.
	fs["/opt/bobres/.restore-point/restore-point.json"] = []byte(strings.Replace(restorePointJSON, `"used_at":null`, `"used_at":"2026-10-19T09:30:00Z"`, 1))
	if u := c.lastUpdate(); u == nil || u.RolledBackAt != time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC).Unix() {
		t.Fatalf("rolled back: %+v", u)
	}
}

func TestLastUpdateWhenThereIsNone(t *testing.T) {
	c := baseCollector(t) // no restore point file
	if u := c.lastUpdate(); u != nil {
		t.Fatalf("invented %+v", u)
	}
	fs := labFS(t, serverMountinfo)
	for name, body := range map[string]string{
		"not json":       `<html>`,
		"no target":      `{"from_version":"0.6.1","created_at":"2026-10-19T08:00:00Z"}`,
		"no time":        `{"from_version":"0.6.1","to_version":"0.7.0"}`,
		"an oversized":   `{"to_version":"0.7.0","created_at":"2026-10-19T08:00:00Z","x":"` + strings.Repeat("a", maxRestorePoint) + `"}`,
		"bad timestamps": `{"to_version":"0.7.0","created_at":"yesterday"}`,
	} {
		fs["/opt/bobres/.restore-point/restore-point.json"] = []byte(body)
		c.FS = fs
		if u := c.lastUpdate(); u != nil {
			t.Errorf("%s: invented %+v", name, u)
		}
	}
	c.InstallDir = ""
	if u := c.lastUpdate(); u != nil {
		t.Errorf("no install directory: %+v", u)
	}
}

// What the file says is untrusted on the reader's side: a version that is not shaped like one is dropped.
func TestLastUpdateIsCheckedByTheReader(t *testing.T) {
	s := &snapshot.Snapshot{Schema: snapshot.Schema, GeneratedAt: now0.Unix(), GeneratedMS: now0.UnixMilli(),
		Update: &snapshot.LastUpdate{From: "0.6.1; rm -rf /", To: "0.7.0", At: now0.Unix()}}
	if err := s.Normalize(now0); err != nil {
		t.Fatal(err)
	}
	if s.Update == nil || s.Update.From != "" || s.Update.To != "0.7.0" {
		t.Fatalf("%+v", s.Update)
	}
	s.Update = &snapshot.LastUpdate{From: "1", To: "<b>2</b>", At: 5}
	if err := s.Normalize(now0); err != nil {
		t.Fatal(err)
	}
	if s.Update != nil {
		t.Fatalf("an update with no usable target kept: %+v", s.Update)
	}
}
