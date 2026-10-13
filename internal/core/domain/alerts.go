package domain

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/backupclient"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/syshealth"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// System alerts (Phase 7). Every minute core's watchdog checks what the
// store needs to work and keeps each check's state in core.alert_state. The
// staff who get alerts hear about it in Telegram (system.alert.v1, sent by
// the bot) when a check goes wrong and again when it is fixed; a problem
// still open after 6 hours is sent again then, and every 24 hours after
// that. A check seen failing once is only pending: a service restarting
// during an update is not announced. A problem is fixed only once it has
// passed for 5 minutes, and one back within 30 minutes of its fix is
// announced only if it lasts past those 30 minutes, so a check that flaps
// does not send a pair of messages every few minutes.

// Watchdog timing. probeTimeout bounds each source, so one that hangs costs
// only its own checks (a service's health is asked within 3 seconds).
const (
	watchdogEvery    = time.Minute
	probeTimeout     = 10 * time.Second
	alertRepeatAfter = 6 * time.Hour
	alertRepeatEvery = 24 * time.Hour
	alertFixAfter    = 5 * time.Minute  // a problem passing this long is fixed
	alertQuietAfter  = 30 * time.Minute // after a fix, the same check is not announced again for this long
)

// Watchdog limits.
const (
	// Payments keeps a Zarinpal payment open by design for its 1-hour pay
	// window, then 45 minutes of grace, then until its reconciler (backing
	// off to 32 minutes) expires it: one still open after 3 hours is stuck,
	// not a checkout the customer left.
	zarinpalOpenLimit = 3 * time.Hour
	// An automatic USDT payment is open for its 45 minutes, then until a
	// complete read of the address that started 5 minutes after them
	// expires it: one still open after 2 hours means the reads never get
	// through (cut short every time), even if each one answers.
	usdtOpenLimit     = 2 * time.Hour
	backupMaxAge      = 26 * time.Hour // the daily backup, with slack
	diskMinFree       = 2 << 30        // bytes
	provisionWindow   = time.Hour      // failed deliveries counted
	feedMaxLag        = 10 * time.Minute
	licenseWarnBefore = 14 * 24 * time.Hour
	maxAlertDetail    = 300 // characters
)

// BotFeedConsumer is the bot's cursor on core's event feed. The feed keeps
// a cursor per authenticated gRPC caller (eventbus.FeedServer), so it is the
// name core's gRPC server gives the bot (cmd/core's grpcauth.Peer), not the
// bot's own name for its consumer.
const BotFeedConsumer = "bot"

// backupService is the backup service's name among the services syshealth
// asks (cmd/core).
const backupService = "backup"

// HealthChecker asks every service's /healthz and /readyz
// (syshealth.Checker).
type HealthChecker interface {
	Check(ctx context.Context) syshealth.Report
}

// ServerLister lists the 3x-ui servers with their last health check.
type ServerLister interface {
	ListServers(ctx context.Context) (*ServerList, error)
}

// GatewayReporter reads the automated gateways' settings and reports.
type GatewayReporter interface {
	GatewayConfigs(ctx context.Context) ([]GatewayConfig, error)
	GatewayReport(ctx context.Context, provider string, from, to int64) (*GatewayReport, error)
}

// BackupStatus reads the backup service's state (backupclient.Client).
type BackupStatus interface {
	Status(ctx context.Context) (json.RawMessage, error)
}

// TLSWatcher reads the HTTPS status for the watchdog (TLS.Watch).
type TLSWatcher interface {
	Watch(ctx context.Context) (*TLSWatchReport, error)
}

// WatchdogSources are what the watchdog asks besides core's database. A nil
// source has nothing to check: Backups is nil when the backup service is
// not installed.
type WatchdogSources struct {
	Health   HealthChecker
	Servers  ServerLister
	Gateways GatewayReporter
	Backups  BackupStatus
	TLS      TLSWatcher
}

// alertCheck is one check's result in a run.
type alertCheck struct {
	key      string
	problem  bool
	severity string
	title    string // the bot's text (alert.*)
	args     map[string]string
	detail   string
	// hold: a problem that may last a while by design (an HTTPS
	// certificate being issued) is announced only once it has lasted this
	// long; until then it stays pending, as open since first seen (kept in
	// the store, so a restart of core does not start it over).
	hold time.Duration
}

// alertProbe is one source's checks. covered: the source answered, so a
// stored check under scope it no longer makes (a server removed or turned
// off, Zarinpal switched off, the license removed) is forgotten without a
// message. A source that did not answer leaves its checks as they were: the
// service check says it is down.
type alertProbe struct {
	scope   string
	covered bool
	checks  []alertCheck
}

// Watchdog runs the checks and announces what changed.
type Watchdog struct {
	s       *Service
	src     WatchdogSources
	log     *slog.Logger
	every   time.Duration
	timeout time.Duration // each source's
	now     func() time.Time
	// FixAfter: a problem is fixed once it has passed this long. QuietAfter:
	// a check fixed less than this long ago is not announced again until
	// then.
	FixAfter   time.Duration
	QuietAfter time.Duration

	// What the backup service last said, for when it does not answer (only
	// backups() uses them, once a run): the newest good backup it reported,
	// and since when it has not answered (zero while it answers).
	backupLastGood *time.Time
	backupUnread   time.Time
}

// NewWatchdog builds the watchdog (every minute).
func (s *Service) NewWatchdog(src WatchdogSources, log *slog.Logger) *Watchdog {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Watchdog{s: s, src: src, log: log.With("worker", "watchdog"), every: watchdogEvery, timeout: probeTimeout,
		now: time.Now, FixAfter: alertFixAfter, QuietAfter: alertQuietAfter}
}

// Run checks every minute until ctx ends.
func (w *Watchdog) Run(ctx context.Context) error {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		if err := w.Step(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("watchdog run failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step runs every check once and records (and announces) what changed.
func (w *Watchdog) Step(ctx context.Context) error {
	now := w.now()
	probes := w.probe(ctx, now)
	if err := ctx.Err(); err != nil {
		return err // shutting down: the answers may be cut short, record nothing
	}
	return w.record(ctx, now, probes)
}

// probe asks every source at once, each within its time limit.
func (w *Watchdog) probe(ctx context.Context, now time.Time) []alertProbe {
	fns := []func(context.Context, time.Time) alertProbe{
		w.services, w.servers, w.zarinpal, w.usdt, w.backups, w.deliveries, w.feed, w.license, w.tls,
	}
	out := make([]alertProbe, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, w.timeout)
			defer cancel()
			out[i] = fn(ctx, now)
		})
	}
	wg.Wait()
	return out
}

// record applies the checks to the stored states in one transaction, with
// the events they announce.
func (w *Watchdog) record(ctx context.Context, now time.Time, probes []alertProbe) error {
	st := w.s.st
	return st.WithTx(ctx, func(tx pgx.Tx) error {
		states, err := st.LockAlertStates(ctx, tx)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, p := range probes {
			for _, c := range p.checks {
				seen[c.key] = true
				if err := w.apply(ctx, tx, now, states[c.key], c); err != nil {
					return err
				}
			}
		}
		for key := range states {
			if seen[key] {
				continue
			}
			for _, p := range probes {
				if p.covered && strings.HasPrefix(key, p.scope) {
					if err := st.DeleteAlertState(ctx, tx, key); err != nil {
						return err
					}
					break
				}
			}
		}
		return nil
	})
}

// apply moves one check to its new state: ok → pending (seen failing once,
// quiet) → problem (seen failing again, announced) → ok (passing for 5
// minutes, announced as fixed).
//   - A pending check that passes goes back to ok quietly.
//   - A problem that passes stays open (and on the System page) until it has
//     passed for 5 minutes; failing again starts those 5 minutes over.
//   - A check that fails again within 30 minutes of its fix stays pending
//     until those 30 minutes are over, and is announced then if it still
//     fails.
//   - A check with a hold stays pending until it has failed that long.
func (w *Watchdog) apply(ctx context.Context, tx pgx.Tx, now time.Time, old *store.AlertState, c alertCheck) error {
	st := w.s.st
	was := store.AlertOK
	var lastSent *time.Time // for ok and pending: when the last fix was announced
	if old != nil {
		was, lastSent = old.State, old.LastSentAt
	}
	next := &store.AlertState{Key: c.key, Since: now, Detail: clipDetail(c.detail, maxAlertDetail),
		Severity: c.severity, TitleKey: c.title, Args: c.args}
	switch {
	case !c.problem && was == store.AlertOK:
		return nil
	case !c.problem && was == store.AlertPending:
		next.State, next.LastSentAt = store.AlertOK, lastSent
		return st.PutAlertState(ctx, tx, next)
	case !c.problem && old.PassingSince == nil:
		// First seen passing: still open, as it was announced.
		keep := *old
		keep.PassingSince = &now
		return st.PutAlertState(ctx, tx, &keep)
	case !c.problem && now.Sub(*old.PassingSince) < w.FixAfter:
		return nil
	case !c.problem:
		// Fixed: the message names the problem that was announced and says
		// how long it lasted, until it first passed.
		next.State, next.LastSentAt, next.Detail = store.AlertOK, &now, ""
		next.Severity, next.TitleKey, next.Args = old.Severity, old.TitleKey, old.Args
		if err := st.PutAlertState(ctx, tx, next); err != nil {
			return err
		}
		return w.announce(ctx, tx, old.Since, *old.PassingSince, next, events.AlertResolved, false)
	case was == store.AlertOK:
		next.State, next.LastSentAt = store.AlertPending, lastSent
		return st.PutAlertState(ctx, tx, next)
	case was == store.AlertPending && (lastSent != nil && now.Sub(*lastSent) < w.QuietAfter || now.Sub(old.Since) < c.hold):
		// Back soon after its fix, or not failing for long enough yet: held,
		// as open since first seen.
		next.State, next.Since, next.LastSentAt = store.AlertPending, old.Since, lastSent
		if sameAlert(old, next) {
			return nil
		}
		return st.PutAlertState(ctx, tx, next)
	case was == store.AlertPending:
		// Seen failing twice in a row: announced, as open since first seen.
		next.State, next.Since, next.LastSentAt = store.AlertProblem, old.Since, &now
	case old.TitleKey != c.title:
		// Another problem on the same check (the license went from expiring
		// to its grace period): announced as new.
		next.State, next.LastSentAt = store.AlertProblem, &now
	default:
		// The same problem: its new details are kept for the System page, and
		// it is sent again when a repeat is due. If it had passed meanwhile,
		// its 5 minutes start over.
		next.State, next.Since, next.LastSentAt = store.AlertProblem, old.Since, old.LastSentAt
		if !repeatDue(old.Since, old.LastSentAt, now) {
			if sameAlert(old, next) && old.PassingSince == nil {
				return nil
			}
			return st.PutAlertState(ctx, tx, next)
		}
		next.LastSentAt = &now
		if err := st.PutAlertState(ctx, tx, next); err != nil {
			return err
		}
		return w.announce(ctx, tx, next.Since, now, next, events.AlertProblem, true)
	}
	if err := st.PutAlertState(ctx, tx, next); err != nil {
		return err
	}
	return w.announce(ctx, tx, next.Since, now, next, events.AlertProblem, false)
}

// repeatDue: a problem open for 6 hours is sent again then, and every 24
// hours after that.
func repeatDue(since time.Time, lastSent *time.Time, now time.Time) bool {
	switch {
	case now.Sub(since) < alertRepeatAfter:
		return false
	case lastSent == nil || lastSent.Sub(since) < alertRepeatAfter:
		return true // the 6-hour reminder
	}
	return now.Sub(*lastSent) >= alertRepeatEvery
}

// clipDetail keeps the first n characters of a check's detail.
func clipDetail(s string, n int) string {
	if r := []rune(strings.TrimSpace(s)); len(r) > n {
		return string(r[:n]) + "…"
	}
	return strings.TrimSpace(s)
}

// sameAlert: nothing worth writing changed (the stored row stays as it is).
func sameAlert(old, next *store.AlertState) bool {
	return old.TitleKey == next.TitleKey && old.Detail == next.Detail && old.Severity == next.Severity &&
		maps.Equal(old.Args, next.Args)
}

// announce publishes an alert about a, open since since and seen so at at
// (for a fix, when it first passed).
func (w *Watchdog) announce(ctx context.Context, tx pgx.Tx, since, at time.Time, a *store.AlertState, state string, repeat bool) error {
	w.log.Info("system alert", "alert", a.Key, "state", state, "repeat", repeat, "detail", a.Detail)
	e := events.SystemAlertEvent{Key: a.Key, Severity: a.Severity, State: state, TitleKey: a.TitleKey, Args: a.Args,
		Since: since.Unix(), At: at.Unix(), Repeat: repeat}
	if state == events.AlertProblem {
		e.Detail = a.Detail
	}
	return w.s.publish(ctx, tx, events.SystemAlert, e)
}

// services: every BOBRES service answers and is ready. An optional service
// that is not installed, or one core has no address for, is not watched;
// but when core was given the backup service's address (Backups is set), a
// name Docker does not resolve means its container is stopped or
// restarting, so it is down.
func (w *Watchdog) services(ctx context.Context, _ time.Time) alertProbe {
	p := alertProbe{scope: "service.", covered: true}
	if w.src.Health == nil {
		return p
	}
	for _, sv := range w.src.Health.Check(ctx).Services {
		c := alertCheck{key: "service." + sv.Name, severity: events.SeverityCritical, title: "alert.service_down",
			args: map[string]string{"service": sv.Name}, detail: sv.Detail}
		switch sv.Status {
		case syshealth.StatusOK:
		case syshealth.StatusDown:
			c.problem = true
		case syshealth.StatusNotReady:
			c.problem, c.title = true, "alert.service_not_ready"
		case syshealth.StatusNotInstalled:
			if sv.Name != backupService || w.src.Backups == nil {
				continue
			}
			c.problem, c.detail = true, "unknown host: its container is stopped or restarting"
		default:
			continue
		}
		p.checks = append(p.checks, c)
	}
	return p
}

// servers: each enabled 3x-ui server answered its last health check (the
// provisioner checks them; a server not checked yet is skipped).
func (w *Watchdog) servers(ctx context.Context, _ time.Time) alertProbe {
	p := alertProbe{scope: "server.", covered: true}
	if w.src.Servers == nil {
		return p
	}
	list, err := w.src.Servers.ListServers(ctx)
	if err != nil {
		p.covered = false // the provisioner's own check reports it
		return p
	}
	for _, sv := range list.Servers {
		if !sv.Enabled || sv.CheckedAt == 0 {
			continue
		}
		p.checks = append(p.checks, alertCheck{key: "server." + sv.ID, problem: !sv.Healthy, severity: events.SeverityCritical,
			title: "alert.server_down", args: map[string]string{"server": sv.Name}, detail: sv.HealthError})
	}
	return p
}

// zarinpal: when the owner switched Zarinpal on, payments go through (no
// error since the last paid one, or that error is over an hour old) and no
// payment has been open for over 3 hours.
func (w *Watchdog) zarinpal(ctx context.Context, now time.Time) alertProbe {
	p := alertProbe{scope: "gateway.zarinpal."}
	if w.src.Gateways == nil {
		p.covered = true
		return p
	}
	configs, err := w.src.Gateways.GatewayConfigs(ctx)
	if err != nil {
		return p
	}
	enabled := false
	for _, c := range configs {
		enabled = enabled || (c.Provider == Zarinpal && c.Enabled)
	}
	if !enabled {
		p.covered = true
		return p
	}
	rep, err := w.src.Gateways.GatewayReport(ctx, Zarinpal, 0, 0)
	if err != nil {
		return p
	}
	p.covered = true
	failing := alertCheck{key: "gateway.zarinpal.error", problem: !rep.Healthy(now), severity: events.SeverityCritical,
		title: "alert.zarinpal_failing"}
	if rep.LastError != nil {
		failing.detail = rep.LastError.Detail
	}
	stuck := alertCheck{key: "gateway.zarinpal.open", severity: events.SeverityCritical, title: "alert.zarinpal_stuck",
		problem: rep.OldestOpenAt > 0 && now.Sub(time.Unix(rep.OldestOpenAt, 0)) > zarinpalOpenLimit,
		args:    map[string]string{"count": strconv.FormatInt(rep.OpenCount, 10)}}
	p.checks = append(p.checks, failing, stuck)
	return p
}

// usdt: when the owner switched automatic USDT on, the watcher reads the
// store's address (its last read worked, or one did within 5 minutes: the
// report's Healthy) and no automatic payment has been open for over 2
// hours. While the reads fail, customers who paid wait and their payments
// stay open (they expire only after a read), and no unmatched deposit is
// seen either.
func (w *Watchdog) usdt(ctx context.Context, now time.Time) alertProbe {
	p := alertProbe{scope: "gateway.usdt_auto."}
	if w.src.Gateways == nil {
		p.covered = true
		return p
	}
	configs, err := w.src.Gateways.GatewayConfigs(ctx)
	if err != nil {
		return p
	}
	enabled := false
	for _, c := range configs {
		enabled = enabled || (c.Provider == USDTAuto && c.Enabled)
	}
	if !enabled {
		p.covered = true
		return p
	}
	rep, err := w.src.Gateways.GatewayReport(ctx, USDTAuto, 0, 0)
	if err != nil {
		return p
	}
	p.covered = true
	reads := alertCheck{key: "gateway.usdt_auto.read", problem: !rep.Healthy(now), severity: events.SeverityCritical,
		title: "alert.usdt_failing", detail: rep.ScanError}
	stuck := alertCheck{key: "gateway.usdt_auto.open", severity: events.SeverityCritical, title: "alert.usdt_stuck",
		problem: rep.OldestOpenAt > 0 && now.Sub(time.Unix(rep.OldestOpenAt, 0)) > usdtOpenLimit,
		args:    map[string]string{"count": strconv.FormatInt(rep.OpenCount, 10)}}
	p.checks = append(p.checks, reads, stuck)
	return p
}

// backups: when the backup service is installed, the last run worked, the
// newest good backup is under 26 hours old, and the disk has 2 GB free.
// With an off-site (S3) copy set up, the newest good backup was copied, and
// the settings can be used.
// While the service does not answer (its own check reports it down), the
// other checks stay as they were, but no backup is made or seen either: the
// newest one it last reported (or, after a restart of core, the time it
// stopped answering) still counts towards the 26 hours, so backups do not
// stop silently.
func (w *Watchdog) backups(ctx context.Context, now time.Time) alertProbe {
	p := alertProbe{scope: "backup.", covered: true}
	if w.src.Backups == nil {
		return p
	}
	raw, err := w.src.Backups.Status(ctx)
	var sum backupclient.Summary
	if err == nil {
		sum, err = backupclient.Summarize(raw)
	}
	if err != nil {
		p.covered = false
		if w.backupUnread.IsZero() {
			w.backupUnread = now
		}
		last := w.backupUnread
		if w.backupLastGood != nil {
			last = *w.backupLastGood
		}
		// Only a problem is reported: a stale backup is not fixed by
		// silence.
		if age := now.Sub(last); age > backupMaxAge {
			p.checks = append(p.checks, staleBackup(age))
		}
		return p
	}
	w.backupLastGood, w.backupUnread = sum.LastCreatedAt, time.Time{}
	p.checks = append(p.checks, alertCheck{key: "backup.failed", problem: sum.LastError != "", severity: events.SeverityWarning,
		title: "alert.backup_failed", detail: sum.LastError})
	if sum.LastCreatedAt != nil {
		p.checks = append(p.checks, staleBackup(now.Sub(*sum.LastCreatedAt)))
	}
	if sum.DiskFree > 0 {
		p.checks = append(p.checks, alertCheck{key: "backup.disk", problem: sum.DiskFree < diskMinFree, severity: events.SeverityWarning,
			title: "alert.disk_low", args: map[string]string{"free": strconv.FormatUint(sum.DiskFree, 10)}})
	}
	if sum.OffsiteConfigured || sum.OffsiteError != "" {
		c := alertCheck{key: "backup.offsite", severity: events.SeverityWarning, title: "alert.backup_offsite"}
		switch {
		case sum.OffsiteError != "":
			c.problem, c.detail = true, sum.OffsiteError
		case sum.LastUploadError != "":
			c.problem, c.detail = true, sum.LastUploadError
		}
		p.checks = append(p.checks, c)
	}
	return p
}

// staleBackup: no good backup for age.
func staleBackup(age time.Duration) alertCheck {
	return alertCheck{key: "backup.stale", problem: age > backupMaxAge, severity: events.SeverityWarning,
		title: "alert.backup_stale", args: map[string]string{"hours": strconv.Itoa(int(age.Hours()))}}
}

// deliveries: no paid order failed to provision in the last hour (one being
// retried right now counts as failing).
func (w *Watchdog) deliveries(ctx context.Context, now time.Time) alertProbe {
	p := alertProbe{scope: "orders."}
	n, err := w.s.st.RecentProvisionFailures(ctx, w.s.st.Conn(), now.Add(-provisionWindow))
	if err != nil {
		w.log.Warn("watchdog: failed deliveries", "err", err)
		return p
	}
	p.covered = true
	p.checks = []alertCheck{{key: "orders.provision_failed", problem: n > 0, severity: events.SeverityCritical,
		title: "alert.provision_failed", args: map[string]string{"count": strconv.FormatInt(n, 10)}}}
	return p
}

// feed: the bot reads core's events (deliveries, receipts, alerts) within
// 10 minutes.
func (w *Watchdog) feed(ctx context.Context, now time.Time) alertProbe {
	p := alertProbe{scope: "events."}
	at, behind, err := w.s.st.OldestUnconsumedEvent(ctx, w.s.st.Conn(), BotFeedConsumer)
	if err != nil {
		w.log.Warn("watchdog: event feed", "err", err)
		return p
	}
	p.covered = true
	var lag time.Duration
	if behind {
		lag = now.Sub(at)
	}
	p.checks = []alertCheck{{key: "events.bot_lag", problem: lag > feedMaxLag, severity: events.SeverityCritical,
		title: "alert.bot_lag", args: map[string]string{"minutes": strconv.Itoa(int(lag.Minutes()))}}}
	return p
}

// license: with a license that verified for this install, it is not in its
// grace period or expired, and does not expire within 14 days. A missing or
// invalid license is the License tab's business.
func (w *Watchdog) license(ctx context.Context, now time.Time) alertProbe {
	p := alertProbe{scope: "license."}
	lic, err := w.s.License(ctx)
	if err != nil {
		w.log.Warn("watchdog: license", "err", err)
		return p
	}
	p.covered = true
	if !lic.Valid() || lic.Token == nil {
		return p
	}
	c := alertCheck{key: "license.expiry", severity: events.SeverityWarning}
	switch lic.State {
	case LicenseExpired:
		c.problem, c.title = true, "alert.license_expired"
	case LicenseGrace:
		c.problem, c.title = true, "alert.license_grace"
		c.args = map[string]string{"date": strconv.FormatInt(lic.GraceEndsAt.Unix(), 10)}
	default:
		left := lic.Token.ExpiresAt.Sub(now)
		days := max(int64(math.Ceil(left.Hours()/24)), 1)
		c.problem, c.title = left < licenseWarnBefore, "alert.license_expiring"
		c.args = map[string]string{"days": strconv.FormatInt(days, 10), "date": strconv.FormatInt(lic.Token.ExpiresAt.Unix(), 10)}
	}
	p.checks = append(p.checks, c)
	return p
}

// tls: every address the store is served on serves what it should (one
// that has served nothing valid for 30 minutes is announced: issuing a
// certificate takes minutes), no certificate is close to its end without
// being renewed (less than min(14 days, a quarter of its life) left), the
// saved HTTPS setting works as saved (in use, and loaded by Caddy), and the
// tls-agent answers core's requests (one waiting for 5 minutes is
// announced).
//
// Caddy not answering at all is a different problem, announced sooner (3
// minutes, its own critical check): the 30 minutes are for certificates
// being issued, and without a connection there is nothing to say about
// them. While no address can be reached, the per-address checks are left as
// they were (the source did not answer for them) rather than raised or
// cleared on no evidence.
func (w *Watchdog) tls(ctx context.Context, now time.Time) alertProbe {
	p := alertProbe{scope: "tls."}
	if w.src.TLS == nil {
		p.covered = true
		return p
	}
	rep, err := w.src.TLS.Watch(ctx)
	if err != nil {
		w.log.Warn("watchdog: https", "err", err)
		return p
	}
	p.covered = true
	down, why := len(rep.Addresses) > 0, ""
	for _, a := range rep.Addresses {
		if a.Reachable {
			down = false
		} else if why == "" {
			why = a.Error
		}
	}
	if down {
		p.covered = false
		p.checks = append(p.checks, alertCheck{key: "tls.caddy", severity: events.SeverityCritical, title: "alert.tls_caddy_down",
			problem: true, detail: why, hold: tlsCaddyDownAfter})
	} else if len(rep.Addresses) > 0 {
		p.checks = append(p.checks, alertCheck{key: "tls.caddy", severity: events.SeverityCritical, title: "alert.tls_caddy_down"})
	}
	for _, a := range rep.Addresses {
		if down {
			break
		}
		detail := a.Error
		if detail == "" && !a.Served {
			detail = "no certificate"
		}
		p.checks = append(p.checks, alertCheck{key: "tls." + a.Role + ".failing", severity: events.SeverityCritical,
			title: "alert.tls_failing", args: map[string]string{"address": a.Host}, detail: detail, problem: !a.Valid,
			hold: tlsFailAfter})
		c := alertCheck{key: "tls." + a.Role + ".expiring", severity: events.SeverityWarning, title: "alert.tls_expiring",
			problem: a.Expiring, args: map[string]string{"address": a.Host}}
		if a.Cert != nil {
			days := max(int64(math.Ceil(a.Cert.NotAfter.Sub(now).Hours()/24)), 1)
			c.args["days"], c.args["date"] = strconv.FormatInt(days, 10), strconv.FormatInt(a.Cert.NotAfter.Unix(), 10)
		}
		p.checks = append(p.checks, c)
	}
	p.checks = append(p.checks,
		alertCheck{key: "tls.setting", severity: events.SeverityWarning, title: "alert.tls_problem", problem: rep.Problem != "",
			detail: rep.Problem},
		alertCheck{key: "tls.agent", severity: events.SeverityWarning, title: "alert.tls_problem", problem: rep.AgentWaiting,
			detail: "the HTTPS applier (tls-agent) has not answered core's request: check it runs (sudo bobres status)",
			hold:   tlsAgentQuiet})
	return p
}

// escapeTags keeps a name's < and > through StripHTML, which undoes it.
var escapeTags = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// Alert is an announced problem that is still open, for the System page.
type Alert struct {
	Key      string
	Severity string // critical | warning
	TitleKey string // the bot's text (alert.*)
	Args     map[string]string
	// Title is the problem in English (the bot's text, without tags).
	Title  string
	Detail string
	Since  time.Time
}

// OpenAlerts lists the open problems, critical first, then the oldest.
func (s *Service) OpenAlerts(ctx context.Context) ([]Alert, error) {
	rows, err := s.st.OpenAlerts(ctx, s.st.Conn())
	if err != nil {
		return nil, err
	}
	cat := Texts()
	out := make([]Alert, 0, len(rows))
	for _, r := range rows {
		args := r.Args
		if args == nil {
			args = map[string]string{}
		}
		title := i18n.StripHTML(cat.T("en", r.TitleKey, i18n.AlertArgs("en", args, escapeTags.Replace)...))
		out = append(out, Alert{Key: r.Key, Severity: r.Severity, TitleKey: r.TitleKey, Args: args, Title: title,
			Detail: r.Detail, Since: r.Since})
	}
	return out, nil
}
