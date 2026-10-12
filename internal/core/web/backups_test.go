package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/backupclient"
)

// fakeBackupService is the backup service's HTTP for core.
type fakeBackupService struct {
	mu      sync.Mutex
	running bool
	runs    int
	status  int // GET answer's status (0: 200)
}

const backupList = `{"backups":[` +
	`{"file":"","size":0,"sha256":"","created_at":"2026-10-11T03:30:00Z","sent_at":null,"error":"pg_dump: connection refused"},` +
	`{"file":"bobres-20261010T033000Z.bkp","size":52311,"sha256":"ab12","created_at":"2026-10-10T03:30:00Z","sent_at":"2026-10-10T03:30:04Z"}],` +
	`"running":false,"last_error":"pg_dump: connection refused","next_run":1760153400,"disk_free":9876543210,"dir":"/backups"}`

func (f *fakeBackupService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer core-token" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /internal/backups":
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		_, _ = w.Write([]byte(backupList))
	case "POST /internal/backups/run":
		if f.running {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"started":false,"reason":"running"}`))
			return
		}
		f.running = true
		f.runs++
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"started":true}`))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeBackupService) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

// GET /api/v1/system/backups (owner and admin) and POST
// /api/v1/system/backups/run (the owner, audited system.backup).
func TestSystemBackups(t *testing.T) {
	f, owner, _ := licenseFixture(t)
	f.staffUser(8921, "support")
	f.staffUser(8922, "admin")
	sup, adm := f.browser(), f.browser()
	sup.loginWithLink(8921)
	adm.loginWithLink(8922)

	// Not installed: the list says so, a run is 503.
	for name, b := range map[string]*browser{"owner": owner, "admin": adm} {
		if code, out := b.do(http.MethodGet, "/api/v1/system/backups", nil); code != 200 || asJSON(out) != `{"installed":false}` {
			t.Fatalf("%s not installed: %d %v", name, code, out)
		}
	}
	if code, out := owner.do(http.MethodPost, "/api/v1/system/backups/run", nil); code != http.StatusServiceUnavailable || out["error"] != "not_configured" {
		t.Fatalf("run not installed: %d %v", code, out)
	}
	if _, out := owner.do(http.MethodGet, "/api/v1/system", nil); asJSON(out["backups"]) != `{"installed":false}` {
		t.Fatalf("system not installed: %v", out["backups"])
	}

	svc := &fakeBackupService{}
	srv := httptest.NewServer(svc)
	defer srv.Close()
	f.api.cfg.Backups = backupclient.New(srv.URL+"/", "core-token")

	// Permissions: support sees nothing, admin reads but cannot run.
	for _, req := range [][2]string{{http.MethodGet, "/api/v1/system/backups"}, {http.MethodPost, "/api/v1/system/backups/run"}} {
		if code, _ := sup.do(req[0], req[1], nil); code != http.StatusForbidden {
			t.Errorf("support %s %s: %d", req[0], req[1], code)
		}
	}
	if code, _ := adm.do(http.MethodPost, "/api/v1/system/backups/run", nil); code != http.StatusForbidden {
		t.Errorf("admin run: %d", code)
	}

	// The list as the service answers it, with installed: true (the
	// service's own answer has no such field; the backups tab reads it).
	for name, b := range map[string]*browser{"owner": owner, "admin": adm} {
		code, out := b.do(http.MethodGet, "/api/v1/system/backups", nil)
		var want map[string]any
		if err := json.Unmarshal([]byte(backupList), &want); err != nil {
			t.Fatal(err)
		}
		want["installed"] = true
		if code != 200 || out["installed"] != true || asJSON(out) != asJSON(want) {
			t.Fatalf("%s list: %d %v", name, code, out)
		}
	}
	// The health tab's summary: the newest backup that worked.
	if _, out := owner.do(http.MethodGet, "/api/v1/system", nil); asJSON(out["backups"]) !=
		`{"installed":true,"last_created_at":1791603000,"last_error":"pg_dump: connection refused","next_run":1760153400}` {
		t.Fatalf("system summary: %v", out["backups"])
	}

	// Back up now, then again while it runs.
	if code, out := owner.do(http.MethodPost, "/api/v1/system/backups/run", nil); code != http.StatusAccepted || asJSON(out) != `{"started":true}` {
		t.Fatalf("run: %d %v", code, out)
	}
	e := lastAudit(t, owner, "action=system.backup")
	if e["source"] != "dashboard" || obj(e["actor"])["telegram_id"] != float64(ownerTG) || e["after"] != nil ||
		strings.Contains(asJSON(e), "core-token") {
		t.Fatalf("audit: %v", e)
	}
	if code, out := owner.do(http.MethodPost, "/api/v1/system/backups/run", nil); code != http.StatusConflict ||
		asJSON(out) != `{"reason":"running","started":false}` {
		t.Fatalf("running: %d %v", code, out)
	}
	if svc.runCount() != 1 {
		t.Fatalf("runs: %d", svc.runCount())
	}

	// A service that fails or is gone: unreachable, and a run is 503.
	svc.mu.Lock()
	svc.status = http.StatusInternalServerError
	svc.mu.Unlock()
	if _, out := adm.do(http.MethodGet, "/api/v1/system/backups", nil); asJSON(out) != `{"error":"unreachable","installed":true}` {
		t.Fatalf("failing list: %v", out)
	}
	srv.Close()
	if _, out := owner.do(http.MethodGet, "/api/v1/system/backups", nil); asJSON(out) != `{"error":"unreachable","installed":true}` {
		t.Fatalf("down list: %v", out)
	}
	if _, out := owner.do(http.MethodGet, "/api/v1/system", nil); asJSON(out["backups"]) != `{"error":"unreachable","installed":true}` {
		t.Fatalf("system down: %v", out["backups"])
	}
	if code, out := owner.do(http.MethodPost, "/api/v1/system/backups/run", nil); code != http.StatusServiceUnavailable || out["error"] != "unavailable" {
		t.Fatalf("run down: %d %v", code, out)
	}

	// The wrong token is refused by the service: unreachable, not a list.
	srv2 := httptest.NewServer(svc)
	defer srv2.Close()
	f.api.cfg.Backups = backupclient.New(srv2.URL, "other-token")
	if _, out := owner.do(http.MethodGet, "/api/v1/system/backups", nil); asJSON(out) != `{"error":"unreachable","installed":true}` {
		t.Fatalf("wrong token: %v", out)
	}
}
