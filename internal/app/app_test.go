package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
	"google.golang.org/grpc"
)

func TestProbeHealthyAndUnhealthy(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer ok.Close()
	if code := probe(strings.TrimPrefix(ok.URL, "http://")); code != 0 {
		t.Fatalf("healthy probe code %d", code)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	if code := probe(strings.TrimPrefix(bad.URL, "http://")); code != 1 {
		t.Fatalf("unhealthy probe code %d", code)
	}
	if code := probe("127.0.0.1:1"); code != 1 {
		t.Fatalf("closed port probe code %d", code)
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	t.Setenv("BOBRES_ENV", "nonsense")
	if code := Run("core", nil, nil); code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
}

func TestHealthcheckAndVersionNeedNoValidConfig(t *testing.T) {
	t.Setenv("BOBRES_ENV", "nonsense") // invalid on purpose
	if code := Run("core", []string{"--version"}, nil); code != 0 {
		t.Fatalf("--version must work with broken config, got %d", code)
	}
	t.Setenv("BOBRES_HTTP_ADDR", "127.0.0.1:1")
	if code := Run("core", []string{"--healthcheck"}, nil); code != 1 {
		t.Fatalf("--healthcheck must run (and fail: nothing listening), got %d", code)
	}
}

func TestRunSetupFailure(t *testing.T) {
	t.Setenv("BOBRES_ENV", "dev")
	closed := false
	failing := func(rt *Runtime) error {
		rt.OnClose(func() { closed = true })
		return errors.New("boom")
	}
	if code := Run("core", nil, failing); code != 1 {
		t.Fatalf("setup failure code %d", code)
	}
	if !closed {
		t.Fatal("resources opened before the failure were not released")
	}
}

// A failing background task must bring the whole service down (exit 1), after
// stop and close hooks ran: the original wiring cancelled background work as
// soon as setup returned, and nobody noticed.
func TestBackgroundFailureStopsServiceAndKeepsTasksAliveUntilThen(t *testing.T) {
	t.Setenv("BOBRES_ENV", "dev")
	t.Setenv("BOBRES_HTTP_ADDR", "127.0.0.1:0")
	var ticks atomic.Int32
	var order []string
	setup := func(rt *Runtime) error {
		rt.Go("ticker", func(ctx context.Context) error {
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(10 * time.Millisecond):
					ticks.Add(1)
				}
			}
		})
		rt.Go("doomed", func(ctx context.Context) error {
			time.Sleep(150 * time.Millisecond)
			return errors.New("lost connection")
		})
		rt.OnStop(func(context.Context) { order = append(order, "stop") })
		rt.OnClose(func() { order = append(order, "close") })
		return nil
	}
	if code := Run("core", nil, setup); code != 1 {
		t.Fatalf("want exit 1 after background failure, got %d", code)
	}
	if ticks.Load() < 5 {
		t.Fatalf("background task was not kept alive after setup returned (ticks=%d)", ticks.Load())
	}
	if strings.Join(order, ",") != "stop,close" {
		t.Fatalf("hook order = %v", order)
	}
}

func TestServeGRPCStopsGracefully(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	rt := &Runtime{Ctx: ctx, Health: health.New("t", nil), Log: slog.New(slog.DiscardHandler), cancel: cancel}
	gs := grpc.NewServer()
	if err := rt.ServeGRPC("127.0.0.1:0", gs); err != nil {
		t.Fatal(err)
	}
	if err := rt.ServeGRPC("256.0.0.1:1", grpc.NewServer()); err == nil {
		t.Fatal("bad listen address accepted")
	}
	done := make(chan struct{})
	go func() { rt.shutdown(2 * time.Second); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown hung")
	}
}

func TestWantsHealthcheck(t *testing.T) {
	if !wantsHealthcheck([]string{"x", "--healthcheck"}) || wantsHealthcheck([]string{"x"}) {
		t.Fatal("flag detection")
	}
}
