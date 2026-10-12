package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/backupclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// SystemHealth asks every BOBRES service's /healthz and /readyz.
type SystemHealth interface {
	Check(ctx context.Context) syshealth.Report
}

// system: GET /api/v1/system, the System page's health tab: every service
// with its version and readiness (a version other than core's is flagged),
// the 3x-ui servers, the payment methods, the bot's last update check (core
// has no internet access), the newest backup and the watchdog's open
// problems, all asked at once.
func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	role := actor(r).Role
	full := Allowed(role, PermServersWrite)
	var (
		wg       sync.WaitGroup
		report   syshealth.Report
		servers  []map[string]any
		panel    map[string]any
		gateways []map[string]any
		updates  any = map[string]string{"error": "bot unreachable"}
		backups      = map[string]any{"installed": false}
		alerts   []map[string]any
	)
	if s.cfg.System != nil {
		wg.Go(func() { report = s.cfg.System.Check(ctx) })
	}
	if s.cfg.Servers != nil {
		wg.Go(func() { servers, panel = s.overviewServers(ctx, full) })
	}
	wg.Go(func() { alerts = s.openAlerts(ctx, role) })
	wg.Go(func() { gateways = s.overviewGateways(ctx) })
	if s.cfg.Bot != nil {
		wg.Go(func() {
			// Within 3 seconds; a bot that is down is shown as such.
			if u, err := s.cfg.Bot.CheckUpdates(ctx); err == nil {
				updates = u
			}
		})
	}
	if s.cfg.Backups != nil {
		wg.Go(func() { backups = s.backupSummary(ctx) })
	}
	wg.Wait()
	if report.Version == "" {
		report.Version = version.Version
	}
	if report.Services == nil {
		report.Services = []syshealth.Service{}
	}
	if servers == nil {
		servers = []map[string]any{}
	}
	if gateways == nil {
		gateways = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"checked_at": s.now().Unix(), "version": report.Version, "version_mismatch": report.VersionMismatch,
		"services": report.Services, "servers": servers, "panel": panel, "gateways": gateways, "updates": updates,
		"backups": backups, "alerts": alerts,
	})
}

// alertDetailPerms: a check's detail (its error) is shown only to staff who
// may read it elsewhere: a server's to those who manage servers (as on the
// overview), Zarinpal's to the owner (the gateway pages are owner-only).
var alertDetailPerms = map[string]string{"server.": PermServersWrite, "gateway.": PermGatewaysWrite}

// openAlerts are the watchdog's open problems, critical first, then the
// oldest: {key, severity (critical | warning), title_key (the bot's alert.*
// text), args, title (that text in English), detail, since (unix)}; null
// when they could not be read, so the page does not say "all clear". The
// detail is "" for a role that may not read it (alertDetailPerms).
func (s *Server) openAlerts(ctx context.Context, role string) []map[string]any {
	list, err := s.cfg.Domain.OpenAlerts(ctx)
	if err != nil {
		s.cfg.Log.Warn("system: open problems", "err", err)
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		detail := a.Detail
		for prefix, perm := range alertDetailPerms {
			if strings.HasPrefix(a.Key, prefix) && !Allowed(role, perm) {
				detail = ""
			}
		}
		out = append(out, map[string]any{"key": a.Key, "severity": a.Severity, "title_key": a.TitleKey, "args": a.Args,
			"title": a.Title, "detail": detail, "since": a.Since.Unix()})
	}
	return out
}

// Backups lists and starts backups on the backup service
// (backupclient.Client).
type Backups interface {
	Status(ctx context.Context) (json.RawMessage, error)
	Run(ctx context.Context) error
}

// backupSummary is the health tab's line about backups: {installed,
// last_created_at (unix, null before the first), last_error, next_run}, or
// {installed, error: "unreachable"}.
func (s *Server) backupSummary(ctx context.Context) map[string]any {
	st, err := s.cfg.Backups.Status(ctx)
	var sum backupclient.Summary
	if err == nil {
		sum, err = backupclient.Summarize(st)
	}
	if err != nil {
		s.cfg.Log.Warn("system: backups", "err", err)
		return map[string]any{"installed": true, "error": "unreachable"}
	}
	var last *int64
	if sum.LastCreatedAt != nil {
		u := sum.LastCreatedAt.Unix()
		last = &u
	}
	return map[string]any{"installed": true, "last_created_at": last, "last_error": sum.LastError, "next_run": sum.NextRun}
}

// systemBackups: GET /api/v1/system/backups (owner and admin), the backups
// tab: the backup service's list as it answers it ({backups (newest first),
// running, last_error, next_run, disk_free, dir}) with installed: true,
// {installed: false} when the service is not installed, {installed: true,
// error: "unreachable"} when it does not answer within 3 seconds (the
// details go to core's log).
func (s *Server) systemBackups(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Backups == nil {
		writeJSON(w, http.StatusOK, map[string]any{"installed": false})
		return
	}
	st, err := s.cfg.Backups.Status(r.Context())
	var list map[string]json.RawMessage
	if err == nil {
		err = json.Unmarshal(st, &list)
	}
	if err == nil && list == nil {
		err = errors.New("the backup list is null")
	}
	if err != nil {
		s.cfg.Log.Warn("system backups", "err", err)
		writeJSON(w, http.StatusOK, map[string]any{"installed": true, "error": "unreachable"})
		return
	}
	// The service's own answer has no installed field; the backups tab reads it.
	list["installed"] = json.RawMessage("true")
	writeJSON(w, http.StatusOK, list)
}

// runBackup: POST /api/v1/system/backups/run (owner), "Back up now": 202
// {started: true} (audited system.backup), 409 {started: false, reason:
// "running"} while one runs, 503 when the service is not installed or does
// not answer. The backup itself goes on in the background.
func (s *Server) runBackup(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Backups == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "backups are not installed on this server")
		return
	}
	switch err := s.cfg.Backups.Run(r.Context()); {
	case errors.Is(err, backupclient.ErrRunning):
		writeJSON(w, http.StatusConflict, map[string]any{"started": false, "reason": "running"})
	case err != nil:
		s.cfg.Log.Warn("system backups: run", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the backup service did not answer; try again in a minute")
	default:
		s.audit(r.Context(), actor(r), "system.backup", nil)
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
	}
}

// LogSource reads one service's kept warnings and errors (botfiles.Client,
// paymentsclient.Client, provisionerclient.Client).
type LogSource interface {
	RecentLogs(ctx context.Context) ([]logging.Line, error)
}

// logServices are the services whose logs core can read. Postgres, Redis
// and Caddy stay with `bobres logs`.
var logServices = []string{"core", "bot", "payments", "provisioner"}

// ownLogs is core's own buffer.
type ownLogs struct{}

func (ownLogs) RecentLogs(context.Context) ([]logging.Line, error) { return logging.Recent(), nil }

// systemLogs: GET /api/v1/system/logs?service= (owner), the logs tab: the
// newest 500 warnings and errors the service kept since it started, already
// redacted, oldest first. A service that does not answer within 3 seconds
// gives error "unreachable" (the details go to core's log).
func (s *Server) systemLogs(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if service == "" {
		service = "core"
	}
	if !slices.Contains(logServices, service) {
		writeError(w, http.StatusBadRequest, "invalid", "unknown service")
		return
	}
	src := s.cfg.Logs[service]
	if src == nil && service == "core" {
		src = ownLogs{}
	}
	lines, errText := []logging.Line{}, ""
	if src == nil {
		errText = "not configured"
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		got, err := src.RecentLogs(ctx)
		cancel()
		switch {
		case err != nil:
			s.cfg.Log.Warn("system logs", "service", service, "err", err)
			errText = "unreachable"
		case got != nil:
			lines = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"service": service, "lines": lines, "error": errText})
}
