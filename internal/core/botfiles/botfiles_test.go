package botfiles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckUpdates(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/update-check" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"current":"1.0.0","latest":"","channel":"","newer":false,"verified":false,"checked_at":1760000000,"error":"unreachable","detail":"dial tcp: timeout"}`))
	}))
	defer srv.Close()

	u, err := New(srv.URL, "tok").CheckUpdates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := (UpdateCheck{Current: "1.0.0", CheckedAt: 1760000000, Error: "unreachable", Detail: "dial tcp: timeout"}); *u != want {
		t.Fatalf("got %+v", *u)
	}
	if _, err := New(srv.URL, "wrong").CheckUpdates(context.Background()); err == nil {
		t.Fatal("a refused call was taken as an answer")
	}
	status = http.StatusInternalServerError
	if _, err := New(srv.URL, "tok").CheckUpdates(context.Background()); err == nil {
		t.Fatal("a 500 was taken as an answer")
	}
	srv.Close()
	if _, err := New(srv.URL, "tok").CheckUpdates(context.Background()); err == nil {
		t.Fatal("a bot that is down answered")
	}
}

func TestRecentLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/logs" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"lines":[{"time":1760000000,"level":"ERROR","msg":"send failed","attrs":"{\"user_id\":7}"}]}`))
	}))
	defer srv.Close()

	lines, err := New(srv.URL, "tok").RecentLogs(context.Background())
	if err != nil || len(lines) != 1 {
		t.Fatalf("%v %v", lines, err)
	}
	if l := lines[0]; l.Time != 1760000000 || l.Level != "ERROR" || l.Msg != "send failed" || l.Attrs != `{"user_id":7}` {
		t.Fatalf("got %+v", l)
	}
	if _, err := New(srv.URL, "wrong").RecentLogs(context.Background()); err == nil {
		t.Fatal("a refused call was taken as an answer")
	}
	srv.Close()
	if _, err := New(srv.URL, "tok").RecentLogs(context.Background()); err == nil {
		t.Fatal("a bot that is down answered")
	}
}
