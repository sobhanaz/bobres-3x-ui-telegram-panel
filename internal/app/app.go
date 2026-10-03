// Package app is the tiny runtime shared by every service binary: it loads
// config, builds the logger, wires health endpoints, handles signals and the
// `--healthcheck` flag used by distroless containers (no shell/curl available).
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/config"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/version"
	"google.golang.org/grpc"
)

// Setup wires a service: routes, readiness checks, gRPC servers and
// background tasks. It must not block; long-running work goes through rt.Go.
type Setup func(rt *Runtime) error

// Runtime is what a service's Setup receives.
type Runtime struct {
	// Ctx lives as long as the service: it is cancelled on SIGINT/SIGTERM or
	// when a background task fails. Use it for every background goroutine.
	Ctx    context.Context
	Mux    *http.ServeMux
	Health *health.Handler
	Log    *slog.Logger

	cancel context.CancelCauseFunc
	mu     sync.Mutex
	stops  []func(context.Context)
	closes []func()
	wg     sync.WaitGroup
}

// OnStop registers f to stop taking work at shutdown (e.g. a gRPC server's
// graceful stop). Stop hooks run in reverse order, before background tasks
// are awaited. f must give up when its context expires.
func (r *Runtime) OnStop(f func(context.Context)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops = append(r.stops, f)
}

// OnClose registers f to release a resource (e.g. a DB pool) after every
// background task has returned. Close hooks run in reverse order.
func (r *Runtime) OnClose(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes = append(r.closes, f)
}

// Go runs f in the background with Ctx. If f returns an error while the
// service is still running, the service shuts down and exits non-zero, so the
// container restarts it instead of running half-broken.
func (r *Runtime) Go(name string, f func(ctx context.Context) error) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if err := f(r.Ctx); err != nil && r.Ctx.Err() == nil {
			r.Log.Error("background task failed", "task", name, "err", err)
			r.cancel(fmt.Errorf("%s: %w", name, err))
		}
	}()
}

// ServeGRPC listens on addr immediately (so a port clash fails setup), serves
// gs in the background, adds a "grpc" readiness check, and stops gs gracefully
// at shutdown (forcefully if the shutdown budget runs out).
func (r *Runtime) ServeGRPC(addr string, gs *grpc.Server) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("grpc listen %s: %w", addr, err)
	}
	r.Health.AddCheck("grpc", func(ctx context.Context) error {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", lis.Addr().String())
		if err != nil {
			return err
		}
		return conn.Close()
	})
	r.Go("grpc", func(context.Context) error {
		if err := gs.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return err
		}
		return nil
	})
	r.OnStop(func(ctx context.Context) {
		done := make(chan struct{})
		go func() { gs.GracefulStop(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			gs.Stop()
		}
	})
	return nil
}

// shutdown stops intake, waits for background tasks, then releases resources,
// all within wait.
func (r *Runtime) shutdown(wait time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	r.cancel(nil)

	r.mu.Lock()
	stops := append(([]func(context.Context))(nil), r.stops...)
	closes := append(([]func())(nil), r.closes...)
	r.mu.Unlock()

	for i := len(stops) - 1; i >= 0; i-- {
		stops[i](ctx)
	}
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		r.Log.Warn("shutdown budget exceeded; some background tasks did not stop")
	}
	for i := len(closes) - 1; i >= 0; i-- {
		closes[i]()
	}
}

// Run executes the standard service lifecycle and returns a process exit code.
func Run(service string, args []string, setup Setup) int {
	// These need no secrets: a container healthcheck must work even if config is broken.
	if wantsHealthcheck(args) {
		return probe(config.HTTPAddrFromEnv())
	}
	if len(args) > 0 && (args[0] == "--version" || args[0] == "version") {
		fmt.Println(service, version.Version)
		return 0
	}

	cfg, err := config.Load(service)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: invalid configuration: %v\n", service, err)
		return 2
	}

	log := logging.New(service, cfg.LogLevel, cfg.LogFormat)
	log.Info("starting", "version", version.Version, "config", cfg)

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancelCause(sigCtx)
	defer cancel(nil)

	mux := http.NewServeMux()
	h := health.New(service, log)
	h.Register(mux)
	rt := &Runtime{Ctx: ctx, Mux: mux, Health: h, Log: log, cancel: cancel}

	if setup != nil {
		if err := setup(rt); err != nil {
			log.Error("setup failed", "err", err)
			rt.shutdown(cfg.ShutdownWait)
			return 1
		}
	}
	code := 0
	if err := health.Serve(ctx, log, cfg.HTTPAddr, mux, cfg.ShutdownWait); err != nil {
		log.Error("server error", "err", err)
		code = 1
	}
	rt.shutdown(cfg.ShutdownWait)
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		log.Error("stopped after failure", "err", cause)
		code = 1
	}
	log.Info("stopped")
	return code
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
