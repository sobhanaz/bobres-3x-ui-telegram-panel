package monitor

import (
	"context"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/backupclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// probeTimeout bounds asking all the other services together.
const probeTimeout = 8 * time.Second

// pendingLimit is how many manual payments awaiting a review are counted (the review queue's own
// limit).
const pendingLimit = 200

// probe is one asking of everything the overview and the sampler need besides the snapshot. A
// field is nil (or its ok flag false) when that source is not configured or did not answer: the
// pages say "unknown" and the sampler records nothing for it.
type probe struct {
	at time.Time

	report *syshealth.Report

	servers *domain.ServerList

	gateways   []domain.GatewayConfig
	gatewaysOK bool

	backups   *backupclient.Summary
	backupsOK bool // the backup service is installed (a summary or an error)

	dbOK      bool
	dbLatency time.Duration

	counters *store.MonitorCounters

	pending   int // manual payments awaiting a review; -1 unknown
	botStats  *botfiles.BotStats
	license   *domain.EffectiveLicense
	alerts    []domain.Alert
	alertsOK  bool
	poolInUse int32
	poolTotal int32
}

// probe asks everything at once, each source inside the shared time limit, and keeps the answer for
// ProbeTTL so that the sampler and many open pages cost one asking.
func (m *Monitor) probe(ctx context.Context) *probe {
	m.probeMu.Lock()
	defer m.probeMu.Unlock()
	now := m.now()
	if m.probed != nil && now.Sub(m.probed.at) < ProbeTTL && !now.Before(m.probed.at) {
		return m.probed
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	p := &probe{at: now, pending: -1}
	var wg sync.WaitGroup
	if m.cfg.Health != nil {
		wg.Go(func() {
			r := m.cfg.Health.Check(ctx)
			p.report = &r
		})
	}
	if m.cfg.Servers != nil {
		wg.Go(func() {
			if l, err := m.cfg.Servers.ListServers(ctx); err == nil {
				p.servers = l
			} else {
				m.log.Warn("monitor: servers", "err", err)
			}
		})
	}
	if m.cfg.Gateways != nil {
		wg.Go(func() {
			if g, err := m.cfg.Gateways.GatewayConfigs(ctx); err == nil {
				p.gateways, p.gatewaysOK = g, true
			} else {
				m.log.Warn("monitor: gateways", "err", err)
			}
		})
	}
	if m.cfg.Backups != nil {
		wg.Go(func() {
			raw, err := m.cfg.Backups.Status(ctx)
			p.backupsOK = true
			if err == nil {
				if sum, serr := backupclient.Summarize(raw); serr == nil {
					p.backups = &sum
				}
			}
		})
	}
	if m.cfg.Payments != nil {
		wg.Go(func() {
			if l, err := m.cfg.Payments.ListPending(ctx, pendingLimit); err == nil {
				p.pending = len(l)
			}
		})
	}
	if m.cfg.Bot != nil {
		wg.Go(func() {
			sctx, scancel := context.WithTimeout(ctx, 3*time.Second)
			defer scancel()
			if s, err := m.cfg.Bot.Stats(sctx); err == nil {
				p.botStats = s
			}
		})
	}
	if m.cfg.Store != nil {
		wg.Go(func() {
			start := time.Now()
			if err := m.cfg.Store.DB().Ping(ctx); err == nil {
				p.dbOK, p.dbLatency = true, time.Since(start)
			}
		})
		wg.Go(func() {
			if c, err := m.cfg.Store.MonitorCounters(ctx, m.cfg.Store.Conn()); err == nil {
				p.counters = c
			} else {
				m.log.Warn("monitor: counters", "err", err)
			}
		})
		p.poolInUse, p.poolTotal, _ = m.cfg.Store.PoolStats()
	}
	if m.cfg.Domain != nil {
		wg.Go(func() {
			if l, err := m.cfg.Domain.OpenAlerts(ctx); err == nil {
				p.alerts, p.alertsOK = l, true
			}
		})
		wg.Go(func() {
			if e, err := m.cfg.Domain.Effective(ctx); err == nil {
				p.license = e
			}
		})
	}
	wg.Wait()
	m.probed = p
	return p
}

// licenseOK is 0 only while the license is in its grace period (a renewal is overdue); the free
// tier and the trial are fine.
func licenseOK(e *domain.EffectiveLicense) float64 {
	if e != nil && e.Basis == license.BasisGrace {
		return 0
	}
	return 1
}
