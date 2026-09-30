// Package health provides /healthz (liveness) and /readyz (readiness) plus a
// small HTTP server wrapper with graceful shutdown shared by all services.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// Check reports whether a dependency is usable.
type Check func(ctx context.Context) error

// Handler serves health endpoints and registers dependency checks.
type Handler struct {
	service string
	mu      sync.RWMutex
	checks  map[string]Check
}

// New creates a Handler for the named service.
func New(service string) *Handler {
	return &Handler{service: service, checks: map[string]Check{}}
}

// AddCheck registers a readiness check under name.
func (h *Handler) AddCheck(name string, c Check) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = c
}

// Register mounts /healthz and /readyz on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", h.live)
	mux.HandleFunc("/readyz", h.ready)
}

func (h *Handler) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok", "service": h.service, "version": version.Version,
	})
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	h.mu.RLock()
	defer h.mu.RUnlock()
	results := make(map[string]string, len(h.checks))
	code := http.StatusOK
	for name, c := range h.checks {
		if err := c(ctx); err != nil {
			results[name] = "fail"
			code = http.StatusServiceUnavailable
		} else {
			results[name] = "ok"
		}
	}
	status := "ready"
	if code != http.StatusOK {
		status = "not_ready"
	}
	// Failure details go to logs, not to the caller (avoid leaking internals).
	writeJSON(w, code, map[string]any{"status": status, "service": h.service, "checks": results})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Serve runs an HTTP server until ctx is cancelled, then shuts it down gracefully.
func Serve(ctx context.Context, log *slog.Logger, addr string, handler http.Handler, wait time.Duration) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info("http listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()
		log.Info("http shutting down")
		return srv.Shutdown(sctx)
	}
}
