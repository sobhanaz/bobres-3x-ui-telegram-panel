package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/cicd"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
)

func TestCIOnlyForCore(t *testing.T) {
	asked := 0
	h := CI(func(context.Context) cicd.Status {
		asked++
		return cicd.Status{Available: true, CheckedAt: 1760000000, Repo: "o/r", Runs: []cicd.Run{{Workflow: "CI", Branch: "main", Status: "completed", Conclusion: "failure"}}}
	}, coreTok)
	for _, tok := range []string{"", "wrong-token-0123456789abcdef012"} {
		if code, _ := call(t, h, http.MethodGet, "/internal/ci", tok); code != http.StatusUnauthorized {
			t.Fatalf("token %q: %d", tok, code)
		}
	}
	if asked != 0 {
		t.Fatalf("GitHub was asked for a caller that is not core")
	}
	code, body := call(t, h, http.MethodGet, "/internal/ci", coreTok)
	var got cicd.Status
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if !got.Available || len(got.Runs) != 1 || got.Runs[0].Conclusion != "failure" {
		t.Fatalf("bad status: %s", body)
	}
}

// An offline or rate-limited GitHub is an answer (200 with the error), never an HTTP error.
func TestCIAnswersWhenGitHubDoesNot(t *testing.T) {
	h := CI(func(context.Context) cicd.Status {
		return cicd.Status{Error: cicd.ErrRateLimited, Detail: "limit", Runs: []cicd.Run{}, ReleaseRuns: []cicd.Run{}, Releases: []cicd.Release{}}
	}, coreTok)
	code, body := call(t, h, http.MethodGet, "/internal/ci", coreTok)
	var got cicd.Status
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK || got.Error != cicd.ErrRateLimited || got.Available {
		t.Fatalf("%d %s", code, body)
	}
}

func TestStatsOnlyForCore(t *testing.T) {
	h := Stats(func() BotStats { return BotStats{StartedAt: 5, UpdatesTotal: 9, ErrorsTotal: 2, Goroutines: 40} }, coreTok)
	if code, _ := call(t, h, http.MethodGet, "/internal/stats", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	code, body := call(t, h, http.MethodGet, "/internal/stats", coreTok)
	var got BotStats
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK || got.UpdatesTotal != 9 || got.ErrorsTotal != 2 || got.StartedAt != 5 {
		t.Fatalf("%d %s", code, body)
	}
}

// The counters come from the dispatcher and the log ring; they only grow.
func TestStatsSourceCountsUpdatesAndLogs(t *testing.T) {
	block := make(chan struct{})
	d := NewDispatcher(context.Background(), 1, func(context.Context, tg.Update) { <-block })
	defer func() { close(block); d.Stop(context.Background()) }()
	started := time.Unix(1760000000, 0)
	src := StatsSource(started, d)
	before := src()
	for i := range 3 {
		d.Dispatch(tg.Update{UpdateID: int64(i + 1)})
	}
	logging.NewWithWriter(&discard{}, "bot", "info", "json").Error("counted")
	after := src()
	if after.StartedAt != started.Unix() || after.UpdatesTotal != before.UpdatesTotal+3 || after.ErrorsTotal != before.ErrorsTotal+1 {
		t.Fatalf("before %+v after %+v", before, after)
	}
	// One update is being handled (it blocks); the other two wait.
	deadline := time.Now().Add(2 * time.Second)
	for d.Queued() != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if q := src().UpdatesQueued; q != 2 {
		t.Fatalf("queued %d", q)
	}
	if after.Goroutines < 1 || after.HeapBytes == 0 {
		t.Fatalf("gauges: %+v", after)
	}
}

type discard struct{}

func (*discard) Write(p []byte) (int, error) { return len(p), nil }
