package web

import (
	"context"
	"net/http"
	"sync"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
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
