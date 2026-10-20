package botfiles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// "Check again" asks the bot to look at GitHub now: a POST to its own endpoint
// with core's token, never the cached GET; the bot's answer is the new outcome.
func TestCheckUpdatesNow(t *testing.T) {
	var asked []string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodPost || r.URL.Path != "/internal/update-check/refresh" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"current":"0.6.1","latest":"0.7.0-rc.1","channel":"beta","issued":"2026-10-20T10:00Z","newer":true,"verified":true,"checked_at":1760000100,"error":"","detail":""}`))
	}))
	defer srv.Close()

	u, err := New(srv.URL, "tok").CheckUpdatesNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := UpdateCheck{Current: "0.6.1", Latest: "0.7.0-rc.1", Channel: "beta", Issued: "2026-10-20T10:00Z", Newer: true, Verified: true, CheckedAt: 1760000100}
	if *u != want {
		t.Fatalf("got %+v", *u)
	}
	if len(asked) != 1 || asked[0] != "POST /internal/update-check/refresh" {
		t.Fatalf("asked %v", asked)
	}
	// the cached read is the GET, which the bot answers on its other route
	if _, err := New(srv.URL, "tok").CheckUpdates(context.Background()); err == nil {
		t.Fatal("the cached read went to the refresh route")
	}
	if _, err := New(srv.URL, "wrong").CheckUpdatesNow(context.Background()); err == nil {
		t.Fatal("a refused call was taken as an answer")
	}
	status = http.StatusInternalServerError
	if _, err := New(srv.URL, "tok").CheckUpdatesNow(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("a 500 was taken as an answer: %v", err)
	}
	srv.Close()
	if _, err := New(srv.URL, "tok").CheckUpdatesNow(context.Background()); err == nil {
		t.Fatal("a bot that is down answered")
	}
}

// The look takes seconds (GitHub is asked several times), so unlike the
// cached read it is not cut at 3 seconds; the caller's own limit still holds.
func TestCheckUpdatesNowWaitsForTheLookButNotForEver(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3500 * time.Millisecond):
			_, _ = w.Write([]byte(`{"current":"1.0.0","latest":"1.1.0","channel":"stable","newer":true,"verified":true,"checked_at":1,"error":"","detail":""}`))
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	start := time.Now()
	u, err := New(slow.URL, "tok").CheckUpdatesNow(context.Background())
	if err != nil || u.Latest != "1.1.0" || time.Since(start) < 3*time.Second {
		t.Fatalf("a look of 3.5 seconds: %+v %v after %s", u, err, time.Since(start))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start = time.Now()
	if _, err := New(slow.URL, "tok").CheckUpdatesNow(ctx); err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("the caller's limit was not kept: %v after %s", err, time.Since(start))
	}
}
