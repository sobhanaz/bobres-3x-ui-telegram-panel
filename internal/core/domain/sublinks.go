package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SubLinkWorker fetches again the stored subscription links of the services
// on a server whose address changed: the bot shows the stored link, so it
// must follow the server. UpdateServer marks the services and wakes it; the
// marks survive a restart.
type SubLinkWorker struct {
	svc     *Service
	prov    Provisioner
	servers ServerAdmin
	log     *slog.Logger
	// Interval is the idle poll (marks left by a restart are picked up).
	Interval time.Duration
	// Batch links are fetched in a row, then the worker waits Pause, sparing
	// the panels.
	Batch int
	Pause time.Duration
	// A fetch the panel did not answer is tried again after Backoff, doubled
	// each time up to MaxBackoff; after MaxAttempts the stored link is kept.
	Backoff, MaxBackoff time.Duration
	MaxAttempts         int
}

// panelDownWait puts back, uncounted, the links of a server whose last
// health check failed (the provisioner checks every minute).
const panelDownWait = 5 * time.Minute

// NewSubLinkWorker builds a worker with production defaults. servers is the
// provisioner's registry: it tells a provisioner that does not answer from a
// panel that does not.
func (s *Service) NewSubLinkWorker(prov Provisioner, servers ServerAdmin, log *slog.Logger) *SubLinkWorker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &SubLinkWorker{svc: s, prov: prov, servers: servers, log: log.With("worker", "sub-links"),
		Interval: time.Minute, Batch: 25, Pause: time.Second,
		Backoff: time.Minute, MaxBackoff: time.Hour, MaxAttempts: 8}
}

// kickSubLinks wakes the worker after services were marked.
func (s *Service) kickSubLinks() {
	select {
	case s.lkick <- struct{}{}:
	default:
	}
}

// Run refreshes until ctx is done. Errors are logged; they never stop the loop.
func (w *SubLinkWorker) Run(ctx context.Context) error {
	for {
		n, err := w.RefreshStale(ctx)
		if ctx.Err() != nil {
			return nil
		}
		wait := w.Interval
		switch {
		case err != nil:
			w.log.Warn("subscription link refresh paused", "err", err)
		case n == w.Batch:
			wait = w.Pause // there may be more
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-w.svc.lkick:
			t.Stop()
		case <-t.C:
		}
	}
}

// RefreshStale fetches one batch of due links and returns how many it
// handled. Only a provisioner that does not answer ends the batch (tried
// again later, nothing counted). A panel that does not answer puts that
// service back for a growing wait, and after MaxAttempts its link is kept as
// it was, like any other refusal: one service or panel never holds up the
// rest.
func (w *SubLinkWorker) RefreshStale(ctx context.Context) (int, error) {
	st := w.svc.st
	stale, err := st.StaleSubLinks(ctx, st.Conn(), w.Batch)
	if err != nil || len(stale) == 0 {
		return 0, err
	}
	// The registry is the provisioner's own database: an answer shows the
	// provisioner is up, and which panels failed their last health check.
	list, err := w.servers.ListServers(ctx)
	if err != nil {
		return 0, fmt.Errorf("server list: %w", err)
	}
	down := panelsDown(list)
	for i, l := range stale {
		if down[strings.ToLower(l.ServerID)] {
			if _, err := st.PostponeSubLink(ctx, st.Conn(), l.ID, l.StaleAt, panelDownWait, false); err != nil {
				return i, err
			}
			continue
		}
		links, err := w.prov.GetLinks(ctx, l.ID)
		if err != nil && (ctx.Err() != nil || (retryLater(err) && !w.provisionerAnswers(ctx))) {
			return i, fmt.Errorf("links of subscription %s: %w", l.ID, err)
		}
		link := ""
		switch {
		case err == nil:
			link = links.SubscriptionLink
		case retryLater(err) && l.Attempts+1 < w.MaxAttempts:
			w.log.Info("subscription link not refreshed yet (tried again later)", "subscription_id", l.ID,
				"attempt", l.Attempts+1, "err", err)
			if _, err := st.PostponeSubLink(ctx, st.Conn(), l.ID, l.StaleAt, w.retryWait(l.Attempts+1), true); err != nil {
				return i, err
			}
			continue
		default:
			w.log.Warn("subscription link not refreshed (kept as it was)", "subscription_id", l.ID, "err", err)
		}
		if _, err := st.RefreshSubLink(ctx, st.Conn(), l.ID, link, l.StaleAt); err != nil {
			return i, err
		}
	}
	return len(stale), nil
}

// panelsDown names the servers whose links can only come from a panel that
// failed its last health check: switched on (a switched-off server is not
// checked, so its result may be old) and with no subscription address (the
// provisioner builds those links without the panel).
func panelsDown(list *ServerList) map[string]bool {
	out := map[string]bool{}
	for _, sv := range list.Servers {
		if sv.Enabled && sv.CheckedAt > 0 && !sv.Healthy && sv.SubBaseURL == "" {
			out[strings.ToLower(sv.ID)] = true
		}
	}
	return out
}

// provisionerAnswers: after a fetch that did not answer, whether it was the
// provisioner (stop) or only the panel behind it (that service waits).
func (w *SubLinkWorker) provisionerAnswers(ctx context.Context) bool {
	_, err := w.servers.ListServers(ctx)
	return err == nil
}

// retryWait is the wait after a service's n-th failed fetch (n from 1).
func (w *SubLinkWorker) retryWait(n int) time.Duration {
	d := w.Backoff
	for i := 1; i < n && d < w.MaxBackoff; i++ {
		d *= 2
	}
	return min(d, w.MaxBackoff)
}

// retryLater: the provisioner (or the panel behind it) did not answer.
func retryLater(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch status.Code(innermost(err)) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Aborted:
		return true
	}
	return false
}

// innermost is the bottom of an error chain (a gRPC status survives %w
// wrapping only there).
func innermost(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}
