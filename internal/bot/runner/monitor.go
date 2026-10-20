package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/cicd"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
)

// ciTimeout bounds one answer of GET /internal/ci (a refresh makes three requests, each bounded and
// retried a few times; this bounds them together).
const ciTimeout = 45 * time.Second

// CI gives core the repository's CI/CD facts for the Monitoring page, at GET /internal/ci: the latest
// runs on main, the Release workflow's runs and the latest releases, as cicd.Status JSON. The bot has
// internet access, core none; the client caches for about 5 minutes and asks GitHub conditionally, so
// core can ask as often as it likes. A GitHub that cannot be reached is an answer too (available
// false, with error and detail), never an HTTP error.
func CI(status func(context.Context) cicd.Status, coreToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), ciTimeout)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(status(ctx))
	})
}

// BotStats is what the bot says about itself at GET /internal/stats: counters that only grow (core
// turns them into rates, and reads a smaller value than before as a restart) and gauges.
type BotStats struct {
	StartedAt     int64  `json:"started_at"`     // unix seconds
	UpdatesTotal  uint64 `json:"updates_total"`  // Telegram updates received since start
	UpdatesQueued int    `json:"updates_queued"` // waiting for a worker now
	WarningsTotal uint64 `json:"warnings_total"` // warnings logged since start
	ErrorsTotal   uint64 `json:"errors_total"`   // errors logged since start
	Goroutines    int    `json:"goroutines"`
	HeapBytes     uint64 `json:"heap_bytes"`
}

// Stats gives core the bot's counters for the Monitoring page's time series, at GET /internal/stats.
func Stats(get func() BotStats, coreToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(get())
	})
}

// StatsSource builds the function Stats serves from the process's own counters: started is when the
// bot started, d its dispatcher.
func StatsSource(started time.Time, d *Dispatcher) func() BotStats {
	return func() BotStats {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		warns, errs := logging.Counts()
		return BotStats{
			StartedAt: started.Unix(), UpdatesTotal: d.Received(), UpdatesQueued: d.Queued(),
			WarningsTotal: warns, ErrorsTotal: errs, Goroutines: runtime.NumGoroutine(), HeapBytes: m.HeapAlloc,
		}
	}
}
