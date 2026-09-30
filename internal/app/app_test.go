package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/health"
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
	t.Setenv("BOBRES_SERVICE_TOKEN", "")
	if code := Run("core", nil, nil); code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
}

func TestHealthcheckAndVersionNeedNoValidConfig(t *testing.T) {
	t.Setenv("BOBRES_ENV", "nonsense") // invalid on purpose
	t.Setenv("BOBRES_SERVICE_TOKEN", "")
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
	t.Setenv("BOBRES_SERVICE_TOKEN", "0123456789abcdef0123456789abcdef")
	failing := func(*http.ServeMux, *health.Handler) error { return http.ErrAbortHandler }
	if code := Run("core", nil, failing); code != 1 {
		t.Fatalf("setup failure code %d", code)
	}
}

func TestWantsHealthcheck(t *testing.T) {
	if !wantsHealthcheck([]string{"x", "--healthcheck"}) || wantsHealthcheck([]string{"x"}) {
		t.Fatal("flag detection")
	}
}
