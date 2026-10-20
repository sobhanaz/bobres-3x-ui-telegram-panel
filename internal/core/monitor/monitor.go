// Package monitor is core's side of the dashboard's Monitoring section. It
//
//   - reads the snapshot the host collector (`bobres monitor snapshot`, a root timer on the server)
//     leaves in BOBRES_MONITOR_DIR, as untrusted text (internal/monitor/snapshot),
//   - samples the host, the containers, core itself, the queues, the bot, the panels and the
//     gateways every 30 seconds into the time series store (internal/core/store/monitor.go),
//     rolls them up and prunes them,
//   - builds the overview, the series and the events the dashboard's API serves,
//   - and evaluates the alert rules for the watchdog (domain.MonitorProbe).
//
// Core has no Docker socket and no shell: the snapshot file is all it knows of the machine. A
// source that is unavailable (no collector, a stale snapshot, a bot that does not answer) records no
// sample: a missing value is never stored as a zero.
package monitor

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/cicd"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/logging"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/monitor/snapshot"
)

// Timing.
const (
	// SampleEvery is the sampling period: the collector's too.
	SampleEvery = 30 * time.Second
	// ProbeTTL is how long one asking of the services, servers, gateways and database is shared by
	// the sampler and the pages.
	ProbeTTL = 10 * time.Second
	// CIEvery is how often core asks the bot for the CI facts (the bot caches for 5 minutes too).
	CIEvery = 5 * time.Minute
	// rollEvery and pruneEvery are the maintenance periods.
	rollEvery  = 10 * time.Minute
	pruneEvery = time.Hour
	// rollBatch is how many (metric, day) groups one rollup call takes.
	rollBatch = 200
	// readEvery is how often the snapshot file is read at most.
	readEvery = 2 * time.Second
	// stepTimeout bounds one sampling step.
	stepTimeout = 25 * time.Second
	// rateGapMin and rateGapMax bound the time between two readings of a counter that a rate is
	// computed over.
	rateGapMin = 10 * time.Second
	rateGapMax = 5 * time.Minute
)

// Health asks every service's /healthz and /readyz (syshealth.Checker).
type Health interface {
	Check(ctx context.Context) syshealth.Report
}

// Gateways lists the payment gateways' settings and state (the payments service).
type Gateways interface {
	GatewayConfigs(ctx context.Context) ([]domain.GatewayConfig, error)
}

// Payments counts the manual payments waiting for a review.
type Payments interface {
	ListPending(ctx context.Context, limit int) ([]domain.PendingPayment, error)
}

// Bot is what core asks the bot for: its counters and the CI facts (botfiles.Client).
type Bot interface {
	Stats(ctx context.Context) (*botfiles.BotStats, error)
	CI(ctx context.Context) (*cicd.Status, error)
}

// LogSource reads one service's kept warnings and errors.
type LogSource interface {
	RecentLogs(ctx context.Context) ([]logging.Line, error)
}

// Config wires the monitor. Every source may be nil: what it would say is then unknown.
type Config struct {
	Store  *store.Store
	Domain *domain.Service
	// Dir is BOBRES_MONITOR_DIR, the folder the host collector writes (read only here); "" when
	// the install does not mount it.
	Dir      string
	Health   Health
	Servers  domain.ServerLister
	Gateways Gateways
	Payments Payments
	Backups  domain.BackupStatus
	Bot      Bot
	// Logs are the services' kept log lines by name (core's own is added).
	Logs    map[string]LogSource
	Log     *slog.Logger
	Version string
	// Now is the clock (tests).
	Now func() time.Time
}

// Monitor samples and serves the monitoring data.
type Monitor struct {
	cfg   Config
	log   *slog.Logger
	now   func() time.Time
	start time.Time

	// The collector's snapshots (mu guards them): the newest valid one and the one before it.
	mu       sync.Mutex
	cur      *snapshot.Snapshot
	prev     *snapshot.Snapshot
	rx, tx   *float64 // the host's network rates between prev and cur
	readAt   time.Time
	readView collectorView
	seen     bool // a snapshot existed at some time since the process started
	sampled  int64

	// The shared asking of the other services.
	probeMu sync.Mutex
	probed  *probe

	// Rates of counters (stateMu guards them).
	stateMu    sync.Mutex
	prevBot    *botfiles.BotStats
	prevBotAt  time.Time
	prevCore   [2]uint64
	prevCoreAt time.Time

	// The CI facts the bot fetched (ciMu guards them).
	ciMu      sync.Mutex
	ci        cicd.Status
	ciHave    bool      // one answer was ever read
	ciTried   time.Time // the last try
	ciVerdict *bool     // whether the newest completed CI run on main failed, as last known

	// The recent log lines of the services, shared by the overview and the events.
	logMu   sync.Mutex
	logs    *logSnapshot
	logsAt  time.Time
	roll    time.Time
	pruneAt time.Time
}

// New builds the monitor.
func New(cfg Config) *Monitor {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	m := &Monitor{cfg: cfg, log: cfg.Log.With("worker", "monitor"), now: now, start: now()}
	m.ci = cicd.Status{Error: cicd.ErrNotChecked, Repo: "", Runs: []cicd.Run{}, ReleaseRuns: []cicd.Run{}, Releases: []cicd.Release{}}
	return m
}

// Started is when this process began (core's own uptime).
func (m *Monitor) Started() time.Time { return m.start }

// collectorView is what the pages and the rules know of the collector.
type collectorView struct {
	// Configured: core has a monitor directory at all.
	Configured bool
	// Installed: a snapshot file exists (or did at some time since core started).
	Installed bool
	Fresh     bool
	AgeS      int64
	// Error is why the file cannot be used ("" when it can): invalid, too_large, unreadable.
	Error string
	// Snap is the newest valid snapshot, when it is fresh; Last is the newest even when it is not.
	Snap, Last *snapshot.Snapshot
	NetRx      *float64
	NetTx      *float64
}

// collector reads the snapshot (at most every readEvery seconds) and says what it is worth now.
func (m *Monitor) collector(now time.Time) collectorView {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg.Dir == "" {
		return collectorView{}
	}
	if m.readAt.IsZero() || now.Sub(m.readAt) >= readEvery || now.Before(m.readAt) {
		m.readAt = now
		m.read(now)
	}
	v := m.readView
	if m.cur != nil {
		v.Last = m.cur
		v.AgeS = int64(m.cur.Age(now).Seconds())
		if v.Error == "" && m.cur.Fresh(now) {
			v.Fresh, v.Snap, v.NetRx, v.NetTx = true, m.cur, m.rx, m.tx
		}
	}
	return v
}

// read loads the file and keeps the newest valid snapshot with the one before it.
func (m *Monitor) read(now time.Time) {
	v := collectorView{Configured: true}
	s, err := snapshot.Read(m.cfg.Dir, now)
	switch {
	case err == nil:
		v.Installed, m.seen = true, true
		switch {
		case m.cur == nil || s.GeneratedMS > m.cur.GeneratedMS:
			m.prev, m.cur = m.cur, s
			m.rx, m.tx = snapshot.NetRates(m.prev, m.cur)
		case s.GeneratedMS < m.cur.GeneratedMS:
			// The clock went back, or the file was replaced by an older one: start over.
			m.prev, m.cur, m.rx, m.tx = nil, s, nil, nil
		}
	case errors.Is(err, fs.ErrNotExist):
		// The collector was turned off (it removes its file) or never ran: not installed.
		m.prev, m.cur, m.rx, m.tx = nil, nil, nil, nil
		v.Installed = false
	case errors.Is(err, snapshot.ErrTooLarge):
		v.Installed, v.Error = true, "too_large"
	case errors.Is(err, snapshot.ErrInvalid), errors.Is(err, snapshot.ErrSchema):
		v.Installed, v.Error = true, "invalid"
	default:
		v.Installed, v.Error = true, "unreadable"
	}
	m.readView = v
}

// settings reads the monitoring settings; the defaults when they cannot be read.
func (m *Monitor) settings(ctx context.Context) domain.MonitorSettings {
	if m.cfg.Domain == nil {
		return domain.MonitorSettingsFrom(nil)
	}
	set, err := m.cfg.Domain.MonitorSettings(ctx)
	if err != nil {
		m.log.Warn("monitor settings", "err", err)
		return domain.MonitorSettingsFrom(nil)
	}
	return set
}
