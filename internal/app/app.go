// Package app is the tiny runtime shared by every service binary: it loads
// config, builds the logger, wires health endpoints, handles signals and the
// `--healthcheck` flag used by distroless containers (no shell/curl available).
package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
)

// Setup lets a service register routes and readiness checks before serving.
type Setup func(mux *http.ServeMux, h *health.Handler) error

// Run executes the standard service lifecycle and returns a process exit code.
func Run(service string, args []string, setup Setup) int {
	cfg, err := config.Load(service)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: invalid configuration: %v\n", service, err)
		return 2
	}
	if wantsHealthcheck(args) {
		return probe(cfg.HTTPAddr)
	}
	if len(args) > 0 && (args[0] == "--version" || args[0] == "version") {
		fmt.Println(service, version.Version)
		return 0
	}

	log := logging.New(service, cfg.LogLevel, cfg.LogFormat)
	log.Info("starting", "version", version.Version, "env", cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	h := health.New(service)
	h.Register(mux)
	if setup != nil {
		if err := setup(mux, h); err != nil {
			log.Error("setup failed", "err", err)
			return 1
		}
	}
	if err := health.Serve(ctx, log, cfg.HTTPAddr, mux, cfg.ShutdownWait); err != nil {
		log.Error("server error", "err", err)
		return 1
	}
	log.Info("stopped")
	return 0
}

func wantsHealthcheck(args []string) bool {
	for _, a := range args {
		if a == "--healthcheck" {
			return true
		}
	}
	return false
}

// probe performs the container healthcheck: GET /healthz on the local listener.
func probe(addr string) int {
	host := addr
	if len(host) > 0 && host[0] == ':' {
		host = "127.0.0.1" + host
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + host + "/healthz") //nolint:noctx // short-lived CLI probe with client timeout
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
