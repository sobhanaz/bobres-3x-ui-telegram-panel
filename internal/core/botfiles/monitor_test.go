package botfiles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/release"
)

func TestStatsAndCIAreAuthenticatedGets(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/internal/stats":
			_, _ = w.Write([]byte(`{"started_at":1760000000,"updates_total":12,"updates_queued":1,"warnings_total":3,"errors_total":2,"goroutines":44,"heap_bytes":1048576}`))
		case "/internal/ci":
			_, _ = w.Write([]byte(`{"available":true,"checked_at":1760000100,"error":"","detail":"","repo":"evil/repo",
			 "runs":[{"workflow":"CI","branch":"main","status":"completed","conclusion":"failure","url":"https://github.com/` + release.Repo + `/actions/runs/1","sha":"abcdef0","at":1760000050},
			         {"workflow":"X","branch":"main","url":"https://evil.example/x"}],"release_runs":null,"releases":null}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	st, err := c.Stats(context.Background())
	if err != nil || st.UpdatesTotal != 12 || st.ErrorsTotal != 2 || st.Goroutines != 44 || st.StartedAt != 1760000000 {
		t.Fatalf("stats: %+v %v", st, err)
	}
	ci, err := c.CI(context.Background())
	if err != nil || !ci.Available || len(ci.Runs) != 2 {
		t.Fatalf("ci: %+v %v", ci, err)
	}
	// Core checks the bot's answer again: its own repository, no foreign link, no null lists.
	if ci.Repo != release.Repo || ci.Runs[1].URL != "" || ci.Runs[0].URL == "" || ci.ReleaseRuns == nil || ci.Releases == nil {
		t.Fatalf("not sanitized: %+v", ci)
	}
	if len(asked) != 2 {
		t.Fatalf("asked %v", asked)
	}

	if _, err := New(srv.URL, "wrong").Stats(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a refused call was taken as an answer: %v", err)
	}
	if _, err := New(srv.URL, "wrong").CI(context.Background()); err == nil {
		t.Fatal("a refused call was taken as an answer")
	}
	srv.Close()
	if _, err := New(srv.URL, "tok").Stats(context.Background()); err == nil {
		t.Fatal("a bot that is down answered")
	}
	if _, err := New(srv.URL, "tok").CI(context.Background()); err == nil {
		t.Fatal("a bot that is down answered")
	}
}

func TestStatsRefusesAnAnswerThatIsNotJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer srv.Close()
	if _, err := New(srv.URL, "tok").Stats(context.Background()); err == nil {
		t.Fatal("html taken as stats")
	}
	if _, err := New(srv.URL, "tok").CI(context.Background()); err == nil {
		t.Fatal("html taken as ci")
	}
}
