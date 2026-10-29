package web

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// GET /metrics, the Prometheus endpoint (docs/runbooks/monitoring.md, "Prometheus and Grafana"): the figures the monitor
// knows now, in the text format 0.0.4, on the store's own domain behind the gateway.
//
// It does not exist until the owner makes a token (404 to everybody, so a store that does not use it shows nothing), and
// then it answers only a request that carries the token as a bearer credential in the Authorization header: a missing or
// wrong one is 401. The token is never read from the address (it would reach logs and history). Core keeps the token's
// SHA-256 and compares two hashes in constant time (domain.CheckMetricsToken). Wrong tries are counted per client address;
// past 30 in a minute they get 429 instead of 401, and a request that carries the right token is never counted.

// metricsFailsPerMinute is how many wrong tries one client address may make a minute before it is told to wait.
const metricsFailsPerMinute = 30

// maxBearer bounds the Authorization value that is hashed (a token is at most domain.MetricsTokenMax characters).
const maxBearer = 512

// metricsGate counts the wrong tries at /metrics; its zero value is ready.
type metricsGate struct {
	once  sync.Once
	fails *limiter
}

// tooMany counts one wrong try of a client and says whether it has made too many.
func (g *metricsGate) tooMany(key string, now time.Time) bool {
	g.once.Do(func() { g.fails = newLimiter(metricsFailsPerMinute, time.Minute) })
	return !g.fails.allow(key, now)
}

// serveMetrics answers GET /metrics.
func (s *Server) serveMetrics(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Monitor == nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), monitorTimeout)
	defer cancel()
	presented := bearer(r) // the credential of "Authorization: Bearer <token>", "" without one
	if len(presented) > maxBearer {
		presented = ""
	}
	enabled, ok, err := s.cfg.Domain.CheckMetricsToken(ctx, presented)
	switch {
	case err != nil:
		s.cfg.Log.Error("metrics token", "err", err)
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	case !enabled:
		http.NotFound(w, r)
		return
	case !ok:
		if s.metrics.tooMany(clientKey(r), s.now()) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many tries", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="BOBRES metrics"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body := s.cfg.Monitor.Exposition(ctx)
	h := w.Header()
	h.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// ---- the token, in the dashboard (Settings > Monitoring > Prometheus metrics)

type metricsStateJSON struct {
	Enabled bool `json:"enabled"`
}

// getMetricsToken: GET /api/v1/metrics-token (settings.write: owner and admin), whether /metrics answers. The token never
// comes back.
func (s *Server) getMetricsToken(w http.ResponseWriter, r *http.Request) {
	on, err := s.cfg.Domain.MetricsEnabled(r.Context())
	if err != nil {
		s.internal(w, "metrics token state", err)
		return
	}
	writeJSON(w, http.StatusOK, metricsStateJSON{Enabled: on})
}

// generateMetricsToken: POST /api/v1/metrics-token/generate (system.write: the owner). The answer is the only place the
// token is ever shown; it replaces any token there was.
func (s *Server) generateMetricsToken(w http.ResponseWriter, r *http.Request) {
	token, err := s.cfg.Domain.GenerateMetricsToken(r.Context(), actor(r))
	if err != nil {
		s.fail(w, "make the metrics token", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "token": token})
}

// putMetricsToken: PUT /api/v1/metrics-token {token} (system.write: the owner), a token of the owner's own choosing.
func (s *Server) putMetricsToken(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if err := s.cfg.Domain.SetMetricsToken(r.Context(), actor(r), b.Token); err != nil {
		s.fail(w, "set the metrics token", err)
		return
	}
	writeJSON(w, http.StatusOK, metricsStateJSON{Enabled: true})
}

// deleteMetricsToken: DELETE /api/v1/metrics-token (system.write: the owner); /metrics is a 404 again.
func (s *Server) deleteMetricsToken(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Domain.ClearMetricsToken(r.Context(), actor(r)); err != nil {
		s.fail(w, "turn off the metrics", err)
		return
	}
	writeJSON(w, http.StatusOK, metricsStateJSON{Enabled: false})
}
