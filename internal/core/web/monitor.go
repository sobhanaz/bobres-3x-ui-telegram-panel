package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/monitor"
)

// MonitorSource is what the Monitoring endpoints read (monitor.Monitor).
type MonitorSource interface {
	Overview(ctx context.Context) *monitor.Overview
	Series(ctx context.Context, metric string, r monitor.Range) (*monitor.Series, error)
	Metrics(ctx context.Context) ([]monitor.MetricInfo, error)
	Events(ctx context.Context, q monitor.EventsQuery) (*monitor.EventsResult, error)
	// Exposition is GET /metrics, the Prometheus text format (metrics.go).
	Exposition(ctx context.Context) []byte
	// Uptime is the availability of the services and the panel servers over 24 hours, 7 days and 30 days.
	Uptime(ctx context.Context) (*monitor.Uptime, error)
	// PublicStatus is what the public status page says (status.go).
	PublicStatus(ctx context.Context) *monitor.PublicStatus
}

// monitorTimeout bounds one request of the Monitoring endpoints.
const monitorTimeout = 20 * time.Second

func (s *Server) monitorOr503(w http.ResponseWriter) bool {
	if s.cfg.Monitor == nil {
		writeError(w, http.StatusServiceUnavailable, "not_configured", "monitoring is not set up on this install")
		return false
	}
	return true
}

// monitorOverview: GET /api/v1/monitor/overview (owner and admin), everything the Monitoring page
// shows at once: the stack's status and components, the host, the containers, Kubernetes, the store's
// counters, the CI/CD panel, the collector and the updates. docs/04-dashboard.md lists every field.
func (s *Server) monitorOverview(w http.ResponseWriter, r *http.Request) {
	if !s.monitorOr503(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), monitorTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, s.cfg.Monitor.Overview(ctx))
}

// monitorUptime: GET /api/v1/monitor/uptime (owner and admin), the availability of every BOBRES service and every panel
// server over 24 hours, 7 days and 30 days from the samples core keeps: the percentage of the time that was observed (null
// when nothing was: no data), the outages and a bar of cells for each window. A moment nobody sampled is neither up nor
// down. docs/04-dashboard.md lists every field. status_page tells whether the public status page is on.
func (s *Server) monitorUptime(w http.ResponseWriter, r *http.Request) {
	if !s.monitorOr503(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), monitorTimeout)
	defer cancel()
	u, err := s.cfg.Monitor.Uptime(ctx)
	if err != nil {
		s.internal(w, "monitor uptime", err)
		return
	}
	settings, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		s.internal(w, "monitor uptime settings", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		*monitor.Uptime
		StatusPage bool `json:"status_page"`
	}{u, domain.StatusPageFrom(settings)})
}

// monitorMetrics: GET /api/v1/monitor/metrics, the metrics that have any data with their units, so a
// page can offer the containers and disks this install has.
func (s *Server) monitorMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.monitorOr503(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), monitorTimeout)
	defer cancel()
	list, err := s.cfg.Monitor.Metrics(ctx)
	if err != nil {
		s.internal(w, "monitor metrics", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metrics": list})
}

// monitorSeries: GET /api/v1/monitor/series?metric=<id>&range=1h|6h|24h|7d|30d (owner and admin).
// One metric answers {metric, unit, step_s, agg, points: [[unix_s, value], ...]}, oldest first, at
// most about 360 points (buckets of step_s seconds; a bucket nobody sampled has no point). With
// metrics=<id>,<id> (at most 12) it answers {range, series: [that, ...]}. A metric this build does
// not record, or a malformed name, is 400; one that has no data yet is an empty list of points.
func (s *Server) monitorSeries(w http.ResponseWriter, r *http.Request) {
	if !s.monitorOr503(w) {
		return
	}
	q := r.URL.Query()
	rng, ok := monitor.ParseRange(q.Get("range"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "range is one of 1h, 6h, 24h, 7d, 30d")
		return
	}
	single, multi := q.Get("metric"), q.Get("metrics")
	var names []string
	switch {
	case single != "" && multi == "":
		names = []string{single}
	case single == "" && multi != "":
		names = strings.Split(multi, ",")
	default:
		writeError(w, http.StatusBadRequest, "invalid", "ask for metric=<id> or metrics=<id>,<id>")
		return
	}
	if len(names) > monitor.MaxSeriesPerRequest {
		writeError(w, http.StatusBadRequest, "invalid", "at most "+strconv.Itoa(monitor.MaxSeriesPerRequest)+" metrics at a time")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), monitorTimeout)
	defer cancel()
	out := make([]*monitor.Series, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if seen[name] {
			continue
		}
		seen[name] = true
		ser, err := s.cfg.Monitor.Series(ctx, name, rng)
		switch {
		case errors.Is(err, monitor.ErrUnknownMetric):
			writeError(w, http.StatusBadRequest, "invalid", "unknown metric")
			return
		case err != nil:
			s.internal(w, "monitor series", err)
			return
		}
		out = append(out, ser)
	}
	if single != "" {
		writeJSON(w, http.StatusOK, out[0])
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rng.Name, "series": out})
}

// monitorEvents: GET /api/v1/monitor/events?level=warn|error&limit=100 (owner and admin), recent
// warnings and errors, newest first: the watchdog's alerts (raised and fixed), the error lines of
// the host's journal, and, for the owner only (they carry Telegram ids, as on the System page's logs
// tab), the lines the services logged. level=warn (the default) lists warnings and errors,
// level=error errors only; limit is 1 to 200.
func (s *Server) monitorEvents(w http.ResponseWriter, r *http.Request) {
	if !s.monitorOr503(w) {
		return
	}
	q := r.URL.Query()
	level := q.Get("level")
	if level == "" {
		level = monitor.LevelWarn
	}
	if level != monitor.LevelWarn && level != monitor.LevelError {
		writeError(w, http.StatusBadRequest, "invalid", "level is warn or error")
		return
	}
	limit := monitor.DefaultEvents
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > monitor.MaxEvents {
			writeError(w, http.StatusBadRequest, "invalid", "limit is 1 to "+strconv.Itoa(monitor.MaxEvents))
			return
		}
		limit = n
	}
	role := actor(r).Role
	ctx, cancel := context.WithTimeout(r.Context(), monitorTimeout)
	defer cancel()
	res, err := s.cfg.Monitor.Events(ctx, monitor.EventsQuery{Level: level, Limit: limit, Logs: Allowed(role, PermSystemWrite)})
	if err != nil {
		s.internal(w, "monitor events", err)
		return
	}
	// An alert's detail (its error text) is shown only to those who may read it elsewhere.
	for i := range res.Items {
		e := &res.Items[i]
		if e.Kind != monitor.KindAlert {
			continue
		}
		for prefix, perm := range alertDetailPerms {
			if strings.HasPrefix(e.Key, prefix) && !Allowed(role, perm) {
				e.Detail = ""
			}
		}
	}
	writeJSON(w, http.StatusOK, res)
}
