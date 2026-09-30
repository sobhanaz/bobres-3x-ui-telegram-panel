package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMux(h *Handler) *http.ServeMux {
	m := http.NewServeMux()
	h.Register(m)
	return m
}

func TestLiveness(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(New("core", nil)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"service":"core"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestReadinessFailsWhenCheckFails(t *testing.T) {
	h := New("core", nil)
	h.AddCheck("db", func(context.Context) error { return errors.New("secret dsn leaked?") })
	h.AddCheck("redis", func(context.Context) error { return nil })
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "dsn") {
		t.Fatalf("error detail leaked to caller: %s", body)
	}
	if !strings.Contains(body, `"db":"fail"`) || !strings.Contains(body, `"redis":"ok"`) {
		t.Fatalf("bad body: %s", body)
	}
}

func TestReadinessOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(New("bot", nil)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != 200 {
		t.Fatalf("code %d", rec.Code)
	}
}
