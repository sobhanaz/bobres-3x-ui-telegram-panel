package monitor

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// Event levels, sources and kinds. The level of a fixed alert is the level it had when it was raised.
const (
	LevelWarn  = "warn"
	LevelError = "error"

	KindAlert   = "alert"
	KindLog     = "log"
	KindJournal = "journal"

	// Alert states: open (still open), closed (it was raised and has been fixed since) and resolved
	// (the entry that says it was fixed).
	AlertOpen     = "open"
	AlertClosed   = "closed"
	AlertResolved = "resolved"
)

// Bounds.
const (
	DefaultEvents = 100
	MaxEvents     = 200
	// alertHistory is how many alert announcements are read for the list.
	alertHistory = 300
	logsTTL      = 20 * time.Second
	logTimeout   = 3 * time.Second
	maxEventText = 240
	maxEventAttr = 200
)

// Event is one warning or error: a watchdog alert (raised or fixed), a line a service logged, or an
// error line of the host's journal. All text is plain and bounded.
type Event struct {
	At     int64  `json:"at"`
	Level  string `json:"level"`
	Source string `json:"source"`
	Kind   string `json:"kind"`
	// Key and State are those of an alert (its check, and AlertOpen, AlertClosed or AlertResolved).
	Key    string `json:"key,omitempty"`
	State  string `json:"state,omitempty"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// EventsQuery says what to list. Logs allows the services' own log lines (they carry Telegram ids:
// the owner's only, as on the System page).
type EventsQuery struct {
	Level string
	Limit int
	Logs  bool
}

// EventsResult is the list, newest first.
type EventsResult struct {
	Items []Event `json:"items"`
	// LogsVisible says the services' log lines are in the list (false for a role that may not read
	// them); Unreachable names the services whose lines could not be read; Truncated that more
	// matched than Limit.
	LogsVisible bool     `json:"logs_visible"`
	Unreachable []string `json:"unreachable"`
	Truncated   bool     `json:"truncated"`
}

// logSnapshot is the services' kept log lines, read together.
type logSnapshot struct {
	lines       map[string][]logging.Line
	unreachable []string
}

// recentLogs reads the services' log lines (core's own, then the others at once), kept for logsTTL so
// that the overview's error count and the events list cost one reading.
func (m *Monitor) recentLogs(ctx context.Context) *logSnapshot {
	m.logMu.Lock()
	defer m.logMu.Unlock()
	now := m.now()
	if m.logs != nil && now.Sub(m.logsAt) < logsTTL && !now.Before(m.logsAt) {
		return m.logs
	}
	out := &logSnapshot{lines: map[string][]logging.Line{"core": logging.Recent()}}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for name, src := range m.cfg.Logs {
		if src == nil || name == "core" {
			continue
		}
		wg.Go(func() {
			cctx, cancel := context.WithTimeout(ctx, logTimeout)
			defer cancel()
			lines, err := src.RecentLogs(cctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.unreachable = append(out.unreachable, name)
				return
			}
			out.lines[name] = lines
		})
	}
	wg.Wait()
	sort.Strings(out.unreachable)
	m.logs, m.logsAt = out, now
	return out
}

// errorsSince counts the error lines the services kept since a time (what their buffers still hold).
func (l *logSnapshot) errorsSince(t time.Time) int {
	n := 0
	for _, lines := range l.lines {
		for _, ln := range lines {
			if ln.Level == "ERROR" && ln.Time >= t.Unix() {
				n++
			}
		}
	}
	return n
}

func levelOfLog(level string) string {
	if level == "ERROR" {
		return LevelError
	}
	return LevelWarn
}

func levelOfSeverity(sev string) string {
	if sev == "critical" {
		return LevelError
	}
	return LevelWarn
}

// Events lists recent warnings and errors, newest first.
func (m *Monitor) Events(ctx context.Context, q EventsQuery) (*EventsResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultEvents
	}
	limit = min(limit, MaxEvents)
	errorsOnly := q.Level == LevelError
	res := &EventsResult{Items: []Event{}, LogsVisible: q.Logs, Unreachable: []string{}}
	var all []Event

	// Alerts: raised and fixed, from the outbox.
	if m.cfg.Domain != nil {
		history, err := m.cfg.Domain.AlertHistory(ctx, alertHistory)
		if err != nil {
			return nil, err
		}
		open := map[string]int64{}
		if list, err := m.cfg.Domain.OpenAlerts(ctx); err == nil {
			for _, a := range list {
				open[a.Key] = a.Since.Unix() + 1 // never 0, so "open" is a lookup that cannot miss
			}
		}
		openSeen := map[string]bool{}
		for _, a := range history { // newest first
			e := Event{At: a.At.Unix(), Level: levelOfSeverity(a.Severity), Source: "alert", Kind: KindAlert, Key: snapshot.Clean(a.Key, 120),
				Title: snapshot.Clean(a.Title, maxEventText), Detail: snapshot.Clean(a.Detail, maxEventText)}
			switch {
			case a.State == "resolved":
				e.State = AlertResolved
			case open[a.Key] != 0 && !openSeen[a.Key]:
				// The newest announcement of a check that is open now is the one that opened it.
				openSeen[a.Key] = true
				e.State = AlertOpen
			default:
				e.State = AlertClosed
			}
			all = append(all, e)
		}
	}

	// The host's journal: error lines the collector scrubbed.
	if cv := m.collector(m.now()); cv.Last != nil && cv.Last.Journal != nil {
		for _, src := range cv.Last.Journal.Sources {
			for _, l := range src.Lines {
				all = append(all, Event{At: l.At, Level: LevelError, Source: "host." + src.Name, Kind: KindJournal,
					Title: snapshot.Clean(l.Msg, maxEventText)})
			}
		}
	}

	// The services' own log lines, for those who may read them.
	if q.Logs {
		logs := m.recentLogs(ctx)
		res.Unreachable = append(res.Unreachable, logs.unreachable...)
		for _, name := range sortedKeys(logs.lines) {
			for _, ln := range logs.lines[name] {
				attrs := ""
				if ln.Attrs != "" && ln.Attrs != "{}" {
					attrs = snapshot.Clean(ln.Attrs, maxEventAttr)
				}
				all = append(all, Event{At: ln.Time, Level: levelOfLog(ln.Level), Source: name, Kind: KindLog,
					Title: snapshot.Clean(ln.Msg, maxEventText), Detail: attrs})
			}
		}
	}

	if errorsOnly {
		kept := all[:0]
		for _, e := range all {
			if e.Level == LevelError {
				kept = append(kept, e)
			}
		}
		all = kept
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At > all[j].At })
	if len(all) > limit {
		all, res.Truncated = all[:limit], true
	}
	res.Items = append(res.Items, all...)
	for i := range res.Items {
		res.Items[i].Title = strings.TrimSpace(res.Items[i].Title)
	}
	return res, nil
}
