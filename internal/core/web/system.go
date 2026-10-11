package web

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

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
// the 3x-ui servers, the payment methods and the bot's last update check
// (core has no internet access), all asked at once.
func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var (
		wg       sync.WaitGroup
		report   syshealth.Report
		servers  []map[string]any
		panel    map[string]any
		gateways []map[string]any
		updates  any = map[string]string{"error": "bot unreachable"}
	)
	if s.cfg.System != nil {
		wg.Go(func() { report = s.cfg.System.Check(ctx) })
	}
	if s.cfg.Servers != nil {
		full := Allowed(actor(r).Role, PermServersWrite)
		wg.Go(func() { servers, panel = s.overviewServers(ctx, full) })
	}
	wg.Go(func() { gateways = s.overviewGateways(ctx) })
	if s.cfg.Bot != nil {
		wg.Go(func() {
			// Within 3 seconds; a bot that is down is shown as such.
			if u, err := s.cfg.Bot.CheckUpdates(ctx); err == nil {
				updates = u
			}
		})
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
	})
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
