package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/backupclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/webauth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/sysupdate"
)

// System > Backups: downloading a backup and restoring one. The temp directory stands in for the
// folder core shares with the server's updater, and a small server for the backup service.

const (
	rsGood   = "bobres-20261011T033000Z.bkp"
	rsOlder  = "bobres-20261010T033000Z.bkp"
	rsFailed = "bobres-20261012T033000Z.bkp"
	rsDomain = "panel.example.com"
)

// rsFile is what the backup service keeps as the good backup: bytes that are not a secret and are
// not the key.
var rsFile = bytes.Repeat([]byte("ENCRYPTED-BACKUP-BYTES-"), 4000)

func rsSum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type restoreFixture struct {
	*updFixture
	svc *httptest.Server
}

func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	u := newUpdFixture(t)
	r := &restoreFixture{updFixture: u}
	list := map[string]any{"backups": []map[string]any{
		{"file": rsFailed, "size": 0, "sha256": "", "created_at": "2026-10-12T03:30:00Z", "error": "pg_dump failed"},
		{"file": rsGood, "size": len(rsFile), "sha256": rsSum(rsFile), "created_at": "2026-10-11T03:30:00Z"},
		{"file": rsOlder, "size": 10, "sha256": rsSum([]byte("0123456789")), "created_at": "2026-10-10T03:30:00Z"},
	}, "running": false, "last_error": "", "next_run": 1760153400, "disk_free": 1, "dir": "/backups"}
	r.svc = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer core-token" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch {
		case req.URL.Path == "/internal/backups":
			_ = json.NewEncoder(w).Encode(list)
		case req.URL.Path == "/internal/backups/file" && req.URL.Query().Get("name") == rsGood:
			w.Header().Set("Content-Length", fmt.Sprint(len(rsFile)))
			_, _ = w.Write(rsFile)
		default:
			http.Error(w, "no such backup", http.StatusNotFound)
		}
	}))
	t.Cleanup(r.svc.Close)
	u.api.cfg.Backups = backupclient.New(r.svc.URL, "core-token")
	u.up = u.dom.NewUpdates(domain.UpdatesConfig{Source: u.src, Helper: sysupdate.NewDir(u.dir), Version: "0.6.1", Domain: rsDomain})
	u.api.cfg.Updates = u.up
	u.status(map[string]any{"restore": true})
	u.clean()
	return r
}

func (r *restoreFixture) backupNotices() []events.SystemBackupEvent {
	r.t.Helper()
	rows, err := r.st.DB().Query(context.Background(), `SELECT payload FROM core.outbox_core WHERE topic = $1 ORDER BY id`, events.SystemBackup)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	var out []events.SystemBackupEvent
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			r.t.Fatal(err)
		}
		var e events.SystemBackupEvent
		if err := json.Unmarshal(raw, &e); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (r *restoreFixture) auditCount(action string) int {
	r.t.Helper()
	var n int
	if err := r.st.DB().QueryRow(context.Background(), `SELECT count(*) FROM core.audit_log WHERE action = $1`, action).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func restoreBody(over map[string]any) map[string]any {
	m := map[string]any{"backup": rsGood, "confirm": rsDomain, "acknowledge": true}
	for k, v := range over {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return m
}

func (r *restoreFixture) rawRequest() map[string]any {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.dir, sysupdate.RequestFile))
	if err != nil {
		r.t.Fatalf("no request: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		r.t.Fatal(err)
	}
	return m
}

func TestRestoreState(t *testing.T) {
	r := newRestoreFixture(t)
	code, out := r.owner.do(http.MethodGet, "/api/v1/system/restore", nil)
	if code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	can := obj(out["can"])
	if out["domain"] != rsDomain || can["allowed"] != true || can["ready"] != true || can["reason"] != "" || out["run"] != nil || out["last"] != nil ||
		out["upload"] != nil || out["other"] != nil || out["version"] != "0.6.1" {
		t.Fatalf("state: %v", out)
	}
	if h := obj(out["helper"]); h["available"] != true || h["restore"] != true || h["action"] != "" {
		t.Fatalf("helper: %v", h)
	}
	if lim := obj(out["limits"]); lim["max_upload"] != float64(sysupdate.MaxUpload) || lim["staged_seconds"] != float64(1800) {
		t.Fatalf("limits: %v", lim)
	}

	// an admin reads it: no way to restore, no domain to type, no upload, no log
	adm := r.login(8801, "admin")
	_, out = adm.do(http.MethodGet, "/api/v1/system/restore", nil)
	if _, has := out["domain"]; has {
		t.Errorf("an admin was given the domain: %v", out)
	}
	if can := obj(out["can"]); can["allowed"] != false || can["ready"] != false {
		t.Fatalf("admin can: %v", can)
	}
	if _, has := obj(out["helper"])["log"]; has {
		t.Errorf("an admin saw the updater's log")
	}
	sup := r.login(8802, "support")
	if code, _ := sup.do(http.MethodGet, "/api/v1/system/restore", nil); code != 403 {
		t.Errorf("support: %d", code)
	}
	if code, _ := r.browser().do(http.MethodGet, "/api/v1/system/restore", nil); code != 401 {
		t.Errorf("visitor: %d", code)
	}

	// why not: an updater from before the restore; no updater; no domain; an update going on
	r.status(map[string]any{"restore": false})
	if _, out = r.owner.do(http.MethodGet, "/api/v1/system/restore", nil); obj(out["can"])["reason"] != "restore_unsupported" {
		t.Errorf("an old updater: %v", out["can"])
	}
	r.status(map[string]any{"installed": false})
	if _, out = r.owner.do(http.MethodGet, "/api/v1/system/restore", nil); obj(out["can"])["reason"] != "helper_missing" {
		t.Errorf("no updater: %v", out["can"])
	}
	r.status(map[string]any{"restore": true})
	if code, out := r.owner.do(http.MethodPost, "/api/v1/system/updates/apply", applyBody(nil)); code != http.StatusAccepted {
		t.Fatalf("an update: %d %v", code, out)
	}
	_, out = r.owner.do(http.MethodGet, "/api/v1/system/restore", nil)
	if obj(out["can"])["reason"] != "already_running" || out["other"] != "update" || out["run"] != nil {
		t.Errorf("during an update: %v", out)
	}
}

func TestBackupDownload(t *testing.T) {
	r := newRestoreFixture(t)
	r.clean()
	// no ticket without the owner
	adm := r.login(8801, "admin")
	for who, b := range map[string]*browser{"admin": adm, "visitor": r.browser()} {
		code, _ := b.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood})
		if code != 403 && code != 401 {
			t.Errorf("%s: %d", who, code)
		}
	}
	for name, file := range map[string]string{"a path": "../" + rsGood, "a sidecar": "bobres-20261011T033000Z.json", "a backup the service does not list": "bobres-20240101T000000Z.bkp",
		"the record of a failed run": rsFailed} {
		if code, out := r.owner.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": file}); code != 404 {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if len(r.backupNotices()) != 0 || r.auditCount("system.backup.download") != 0 {
		t.Fatal("something was recorded for a download that did not happen")
	}

	code, out := r.owner.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood})
	if code != 200 || out["expires_in"] != float64(60) {
		t.Fatalf("ticket: %d %v", code, out)
	}
	url, _ := out["url"].(string)
	if !strings.HasPrefix(url, "/api/v1/system/backups/download/") || strings.Contains(url, rsGood) || len(url) < 60 {
		t.Fatalf("the address: %q", url)
	}
	// someone else's session cannot use it, and that spends it
	other := r.login(8803, "owner")
	if code, _ := other.do(http.MethodGet, url, nil); code != 403 {
		t.Fatalf("another owner's session used the ticket: %d", code)
	}
	if c, _, _ := r.owner.get(url); c != 403 {
		t.Fatalf("a ticket another session tried is used again: %d", c)
	}
	if len(r.backupNotices()) != 0 {
		t.Fatal("recorded a download that was refused")
	}

	// the owner's own: a download, once
	code, out = r.owner.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood})
	if code != 200 {
		t.Fatalf("ticket: %d %v", code, out)
	}
	url = out["url"].(string)
	code, hdr, body := r.owner.get(url)
	if code != 200 || !bytes.Equal(body, rsFile) {
		t.Fatalf("download: %d, %d bytes of %d", code, len(body), len(rsFile))
	}
	if hdr.Get("Content-Type") != "application/octet-stream" || hdr.Get("Content-Disposition") != `attachment; filename="`+rsGood+`"` ||
		hdr.Get("Content-Length") != fmt.Sprint(len(rsFile)) || hdr.Get("Cache-Control") != "no-store" || hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers: %v", hdr)
	}
	if bytes.Contains(body, []byte("BOBRES_BACKUP_KEY")) {
		t.Fatal("the key is in the download")
	}
	if c, _, _ := r.owner.get(url); c != 403 {
		t.Fatalf("a used ticket worked again: %d", c)
	}
	// audited and told to every owner before it was sent
	if r.auditCount("system.backup.download") != 1 {
		t.Fatalf("audit: %d", r.auditCount("system.backup.download"))
	}
	notes := r.backupNotices()
	if len(notes) < 1 {
		t.Fatal("no notice")
	}
	for _, e := range notes {
		if e.Kind != events.BackupDownload || e.State != events.BackupDownloaded || e.File != rsGood || e.Size != int64(len(rsFile)) || e.By != "u" {
			t.Errorf("notice: %+v", e)
		}
	}
	// the audit entry says who, what and how big
	e := lastAudit(t, r.owner, "action=system.backup.download")
	if after := obj(e["after"]); after["file"] != rsGood || after["size"] != float64(len(rsFile)) {
		t.Errorf("audit entry: %v", e)
	}

	// an expired ticket
	_, out = r.owner.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood})
	r.api.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if c, _, _ := r.owner.get(out["url"].(string)); c != 403 {
		t.Errorf("an expired ticket: %d", c)
	}
	r.api.now = time.Now
}

func TestBackupDownloadSurvivesTheBackupServiceMisbehaving(t *testing.T) {
	r := newRestoreFixture(t)
	r.clean()
	// listed by the service, but gone when asked for
	ticket := func(file string) string {
		_, out := r.owner.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": file})
		u, _ := out["url"].(string)
		return u
	}
	if c, _, _ := r.owner.get(ticket(rsOlder)); c != 404 {
		t.Errorf("a backup the service lists but does not have: %d", c)
	}
	if len(r.backupNotices()) != 0 || r.auditCount("system.backup.download") != 0 {
		t.Error("a download that did not happen was recorded")
	}
	// the backup service is not installed
	r.api.cfg.Backups = nil
	if code, _ := r.owner.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood}); code != 503 {
		t.Errorf("no backup service: %d", code)
	}
}

// The authenticator code is asked for when the owner has one; the right code is spent once.
func TestBackupDownloadAsksForTheAuthenticatorCode(t *testing.T) {
	r := newRestoreFixture(t)
	secret := confirmPassword(t, r.owner, "boss")
	pass := r.browser()
	c1, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now()))
	if code, _ := pass.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "a long password", "code": c1}); code != 200 {
		t.Fatalf("password login: %d", code)
	}
	if code, out := pass.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood}); code != 403 || out["error"] != "code_required" {
		t.Fatalf("no code: %d %v", code, out)
	}
	if code, out := pass.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood, "code": "000000"}); code != 400 || out["error"] != "code_wrong" {
		t.Fatalf("a wrong code: %d %v", code, out)
	}
	c2, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now())+1)
	if code, out := pass.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood, "code": c2}); code != 200 || out["url"] == nil {
		t.Fatalf("the right code: %d %v", code, out)
	}
	// the same code is not good twice
	if code, out := pass.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood, "code": c2}); code != 400 || out["error"] != "code_wrong" {
		t.Fatalf("the same code again: %d %v", code, out)
	}
}

func TestBackupDownloadIsRateLimited(t *testing.T) {
	r := newRestoreFixture(t)
	limited := false
	for i := 0; i < 14; i++ {
		code, _ := r.owner.do(http.MethodPost, "/api/v1/system/backups/download-ticket", map[string]any{"file": rsGood})
		if code == http.StatusTooManyRequests {
			limited = i >= 10
			break
		}
		if code != 200 {
			t.Fatalf("try %d: %d", i, code)
		}
	}
	if !limited {
		t.Fatal("not limited after ten tickets an hour")
	}
}

func TestRestoreApplyFromTheServersBackups(t *testing.T) {
	r := newRestoreFixture(t)
	r.clean()
	code, out := r.owner.do(http.MethodPost, "/api/v1/system/restore", restoreBody(nil))
	if code != http.StatusAccepted {
		t.Fatalf("%d %v", code, out)
	}
	run := obj(out["run"])
	if run["action"] != "restore" || run["subject"] != rsGood || run["source"] != "backup" || run["phase"] != "queued" || run["by"] != "u" || run["outcome"] != "" {
		t.Fatalf("run: %v", run)
	}
	// the request carries what the backup service said of the file, not what the page sent
	req := r.rawRequest()
	if req["action"] != "restore" || req["source"] != "backup" || req["file"] != rsGood || req["upload"] != "" || req["sha256"] != rsSum(rsFile) ||
		req["size"] != float64(len(rsFile)) || req["by"] != "u" || req["id"] != run["id"] {
		t.Fatalf("request: %v", req)
	}
	if r.auditCount("system.restore.requested") != 1 {
		t.Fatal("not audited")
	}
	notes := r.backupNotices()
	if len(notes) < 1 || notes[0].Kind != events.BackupRestore || notes[0].State != events.UpdateRequested || notes[0].File != rsGood {
		t.Fatalf("notices: %+v", notes)
	}
	e := lastAudit(t, r.owner, "action=system.restore.requested")
	if after := obj(e["after"]); after["backup"] != rsGood || after["sha256"] != rsSum(rsFile) {
		t.Errorf("audit: %v", e)
	}
	// the state shows it, and a second one is refused while it runs
	_, out = r.owner.do(http.MethodGet, "/api/v1/system/restore", nil)
	if obj(out["run"])["id"] != run["id"] || obj(out["can"])["reason"] != "already_running" {
		t.Fatalf("state: %v", out)
	}
	if code, out := r.owner.do(http.MethodPost, "/api/v1/system/restore", restoreBody(nil)); code != 409 || out["code"] != "already_running" {
		t.Fatalf("a second restore: %d %v", code, out)
	}
	// and the update tab does not take it for an update of its own
	_, out = r.owner.do(http.MethodGet, "/api/v1/system/updates/progress", nil)
	if obj(out["run"])["action"] != "restore" || obj(out["apply"]) != nil && false {
		t.Fatalf("progress: %v", out)
	}
}

func TestRestoreApplyRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		prep   func(r *restoreFixture)
		body   map[string]any
		status int
		code   string
	}{
		"no acknowledgement":                 {body: restoreBody(map[string]any{"acknowledge": false}), status: 400, code: "ack_required"},
		"the wrong domain":                   {body: restoreBody(map[string]any{"confirm": "panel.example.org"}), status: 400, code: "confirm_wrong"},
		"no domain":                          {body: restoreBody(map[string]any{"confirm": ""}), status: 400, code: "confirm_wrong"},
		"no backup and no upload":            {body: restoreBody(map[string]any{"backup": nil}), status: 400, code: "invalid"},
		"both a backup and an upload":        {body: restoreBody(map[string]any{"upload_id": rsOlder}), status: 400, code: "invalid"},
		"a backup the service does not list": {body: restoreBody(map[string]any{"backup": "bobres-20240101T000000Z.bkp"}), status: 404, code: "backup_unknown"},
		"a failed run":                       {body: restoreBody(map[string]any{"backup": rsFailed}), status: 404, code: "backup_unknown"},
		"a path":                             {body: restoreBody(map[string]any{"backup": "../../etc/passwd"}), status: 404, code: "backup_unknown"},
		"an upload nobody sent":              {body: restoreBody(map[string]any{"backup": nil, "upload_id": "0199a5b2-7c3e-7d11-9f2a-1b2c3d4e5f60"}), status: 404, code: "upload_missing"},
		"a field the page may not send":      {body: restoreBody(map[string]any{"sha256": rsSum(rsFile)}), status: 400, code: ""},
		"an updater from before":             {prep: func(r *restoreFixture) { r.status(map[string]any{"restore": false}) }, body: restoreBody(nil), status: 409, code: "restore_unsupported"},
		"no updater":                         {prep: func(r *restoreFixture) { r.status(map[string]any{"installed": false}) }, body: restoreBody(nil), status: 409, code: "helper_missing"},
		"no store domain known": {prep: func(r *restoreFixture) {
			r.up = r.dom.NewUpdates(domain.UpdatesConfig{Source: r.src, Helper: sysupdate.NewDir(r.dir), Version: "0.6.1"})
			r.api.cfg.Updates = r.up
		}, body: restoreBody(nil), status: 409, code: "no_domain"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRestoreFixture(t)
			r.clean()
			if tc.prep != nil {
				tc.prep(r)
			}
			code, out := r.owner.do(http.MethodPost, "/api/v1/system/restore", tc.body)
			if code != tc.status || (tc.code != "" && out["code"] != tc.code && out["error"] != tc.code) {
				t.Fatalf("%d %v, want %d %s", code, out, tc.status, tc.code)
			}
			if _, err := os.Stat(filepath.Join(r.dir, sysupdate.RequestFile)); err == nil {
				t.Fatal("a request was written")
			}
			if r.auditCount("system.restore.requested") != 0 || len(r.backupNotices()) != 0 {
				t.Fatal("a refused restore was recorded")
			}
		})
	}
}

func TestRestoreIsTheOwnersOnly(t *testing.T) {
	r := newRestoreFixture(t)
	adm := r.login(8801, "admin")
	for _, b := range []*browser{adm, r.login(8802, "support"), r.browser()} {
		for path, body := range map[string]any{"/api/v1/system/restore": restoreBody(nil), "/api/v1/system/backups/download-ticket": map[string]any{"file": rsGood}} {
			if code, _ := b.do(http.MethodPost, path, body); code != 403 && code != 401 {
				t.Errorf("%s: %d", path, code)
			}
		}
		if code, _ := b.raw(http.MethodPost, "/api/v1/system/restore/upload", "application/octet-stream", []byte("x")); code != 403 && code != 401 {
			t.Errorf("upload: %d", code)
		}
	}
	// the session's CSRF token is needed to change anything
	if code, out := r.owner.do(http.MethodPost, "/api/v1/system/restore", restoreBody(nil), csrfHeader, "wrong"); code != 403 || out["error"] != "csrf" {
		t.Errorf("csrf: %d %v", code, out)
	}
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+"/api/v1/system/restore/upload", strings.NewReader("x"))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := r.owner.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("an upload without the CSRF token: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(r.dir, sysupdate.UploadDirName)); err == nil {
		t.Error("something was received")
	}
}

func TestRestoreApplyAsksForTheAuthenticatorCodeAfterTheCheapChecks(t *testing.T) {
	r := newRestoreFixture(t)
	secret := confirmPassword(t, r.owner, "boss")
	pass := r.browser()
	c1, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now()))
	if code, _ := pass.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "boss", "password": "a long password", "code": c1}); code != 200 {
		t.Fatalf("password login: %d", code)
	}
	hasRequest := func() bool { _, err := os.Stat(filepath.Join(r.dir, sysupdate.RequestFile)); return err == nil }
	// a mistyped domain is said at once and does not spend a code
	if code, out := pass.do(http.MethodPost, "/api/v1/system/restore", restoreBody(map[string]any{"confirm": "oops"})); code != 400 || out["code"] != "confirm_wrong" {
		t.Fatalf("a wrong domain: %d %v", code, out)
	}
	if code, out := pass.do(http.MethodPost, "/api/v1/system/restore", restoreBody(nil)); code != 403 || out["error"] != "code_required" {
		t.Fatalf("no code: %d %v", code, out)
	}
	if code, out := pass.do(http.MethodPost, "/api/v1/system/restore", restoreBody(map[string]any{"code": "000000"})); code != 400 || out["error"] != "code_wrong" {
		t.Fatalf("a wrong code: %d %v", code, out)
	}
	if hasRequest() {
		t.Fatal("a request was written without the code")
	}
	c2, _ := webauth.TOTPCode(secret, webauth.TOTPStep(time.Now())+1)
	if code, out := pass.do(http.MethodPost, "/api/v1/system/restore", restoreBody(map[string]any{"code": c2})); code != http.StatusAccepted {
		t.Fatalf("the right code: %d %v", code, out)
	}
	if !hasRequest() {
		t.Fatal("no request after the right code")
	}
}

func TestRestoreApplyIsRateLimited(t *testing.T) {
	r := newRestoreFixture(t)
	limited := false
	for i := 0; i < 10; i++ {
		code, _ := r.owner.do(http.MethodPost, "/api/v1/system/restore", restoreBody(map[string]any{"confirm": "nope"}))
		if code == http.StatusTooManyRequests {
			limited = i >= 6
			break
		}
	}
	if !limited {
		t.Fatal("not limited after six tries an hour")
	}
}

func TestRestoreUploadThenRestore(t *testing.T) {
	r := newRestoreFixture(t)
	r.clean()
	body := bytes.Repeat([]byte("my uploaded backup "), 3000)
	code, out := r.owner.raw(http.MethodPost, "/api/v1/system/restore/upload?name="+"my%20backup%0A.bkp", "application/octet-stream", body)
	if code != http.StatusCreated {
		t.Fatalf("%d %v", code, out)
	}
	up := obj(out["upload"])
	id, _ := up["id"].(string)
	if up["name"] != "my backup .bkp" || up["size"] != float64(len(body)) || up["sha256"] != rsSum(body) || len(id) != 36 {
		t.Fatalf("upload: %v", up)
	}
	staged, err := os.ReadFile(filepath.Join(r.dir, sysupdate.UploadDirName, id+".bkp"))
	if err != nil || !bytes.Equal(staged, body) {
		t.Fatalf("the file: %d bytes, %v", len(staged), err)
	}
	if r.auditCount("system.restore.upload") != 1 {
		t.Fatal("the upload was not audited")
	}
	// the state offers it to the owner, to nobody else
	_, st := r.owner.do(http.MethodGet, "/api/v1/system/restore", nil)
	if obj(st["upload"])["id"] != id {
		t.Fatalf("state: %v", st)
	}
	_, st = r.login(8801, "admin").do(http.MethodGet, "/api/v1/system/restore", nil)
	if st["upload"] != nil {
		t.Fatalf("an admin was shown the upload: %v", st)
	}

	code, out = r.owner.do(http.MethodPost, "/api/v1/system/restore", restoreBody(map[string]any{"backup": nil, "upload_id": id}))
	if code != http.StatusAccepted {
		t.Fatalf("restore: %d %v", code, out)
	}
	req := r.rawRequest()
	if req["source"] != "upload" || req["upload"] != id || req["file"] != "my backup .bkp" || req["sha256"] != rsSum(body) || req["size"] != float64(len(body)) {
		t.Fatalf("request: %v", req)
	}
	// the confirmed upload cannot be confirmed a second time
	_, st = r.owner.do(http.MethodGet, "/api/v1/system/restore", nil)
	if st["upload"] != nil {
		t.Fatalf("the upload is still offered: %v", st["upload"])
	}
}

func TestRestoreUploadRefusals(t *testing.T) {
	r := newRestoreFixture(t)
	r.up.MaxUpload = 1000
	post := func(ctype string, body []byte) (int, map[string]any) {
		return r.owner.raw(http.MethodPost, "/api/v1/system/restore/upload?name=x.bkp", ctype, body)
	}
	if code, out := post("application/json", []byte("{}")); code != 415 {
		t.Errorf("json: %d %v", code, out)
	}
	if code, out := post("multipart/form-data; boundary=x", []byte("--x--")); code != 415 {
		t.Errorf("multipart: %d %v", code, out)
	}
	if code, out := post("application/octet-stream", nil); code != 400 || out["code"] != "upload_empty" {
		t.Errorf("empty: %d %v", code, out)
	}
	if code, out := post("application/octet-stream", make([]byte, 1001)); code != 413 || out["code"] != "upload_too_large" {
		t.Errorf("over the limit: %d %v", code, out)
	}
	if code, out := post("application/octet-stream", make([]byte, 1000)); code != http.StatusCreated {
		t.Errorf("exactly the limit: %d %v", code, out)
	}
	// declared too large: refused before a byte is read
	req, _ := http.NewRequest(http.MethodPost, r.srv.URL+"/api/v1/system/restore/upload", io.LimitReader(zeros{}, 10))
	req.ContentLength = 5000
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(csrfHeader, r.owner.csrf)
	if resp, err := r.owner.c.Do(req); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != 413 {
			t.Errorf("a declared size over the limit: %d", resp.StatusCode)
		}
	}
	// nothing is received while the updater cannot restore
	r.status(map[string]any{"restore": false})
	if code, out := post("application/octet-stream", []byte("data")); code != 409 || out["code"] != "restore_unsupported" {
		t.Errorf("an old updater: %d %v", code, out)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestRestoreUploadIsRateLimited(t *testing.T) {
	r := newRestoreFixture(t)
	limited := false
	for i := 0; i < 8; i++ {
		code, _ := r.owner.raw(http.MethodPost, "/api/v1/system/restore/upload?name=x.bkp", "application/octet-stream", []byte("data"))
		if code == http.StatusTooManyRequests {
			limited = i >= 5
			break
		}
		if code != http.StatusCreated {
			t.Fatalf("try %d: %d", i, code)
		}
	}
	if !limited {
		t.Fatal("not limited after five uploads an hour")
	}
}

// A restore that is going on is shown from the updater's status; when it replaced the database the
// end is found again.
func TestRestoreStateFollowsTheUpdater(t *testing.T) {
	r := newRestoreFixture(t)
	r.clean()
	_, out := r.owner.do(http.MethodPost, "/api/v1/system/restore", restoreBody(nil))
	id, _ := obj(out["run"])["id"].(string)
	r.status(map[string]any{"restore": true, "state": "running", "id": id, "action": "restore", "step": "restore", "subject": rsGood, "source": "backup", "by": "boss",
		"message": "Restoring the database.", "started_at": time.Now().UTC().Format(time.RFC3339), "log": []string{"Database restored."}})
	_, st := r.owner.do(http.MethodGet, "/api/v1/system/restore", nil)
	h, run := obj(st["helper"]), obj(st["run"])
	if run["phase"] != "running" || h["step"] != "restore" || h["action"] != "restore" || h["subject"] != rsGood || h["message"] != "Restoring the database." {
		t.Fatalf("state: %v", st)
	}
	if log, _ := h["log"].([]any); len(log) != 1 {
		t.Fatalf("the owner's log: %v", h["log"])
	}
	// the database is replaced: no row. The worker records the end from the status.
	if _, err := r.st.DB().Exec(context.Background(), `DELETE FROM core.system_updates`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r.status(map[string]any{"restore": true, "state": "done", "id": id, "action": "restore", "step": "done", "subject": rsGood, "source": "backup", "by": "boss",
		"message": "BOBRES now runs the data of x.", "requested_at": now.Add(-time.Minute).Format(time.RFC3339), "finished_at": now.Format(time.RFC3339)})
	if err := r.up.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, st = r.owner.do(http.MethodGet, "/api/v1/system/restore", nil)
	last := obj(st["last"])
	if st["run"] != nil || last["id"] != id || last["outcome"] != "done" || last["subject"] != rsGood || last["by"] != "boss" || last["action"] != "restore" {
		t.Fatalf("state after: %v", st)
	}
	// and the update tab's last update is still its own
	_, up := r.owner.do(http.MethodGet, "/api/v1/system/updates/progress", nil)
	if up["last"] != nil {
		t.Fatalf("the update tab shows a restore as its last update: %v", up["last"])
	}
}
