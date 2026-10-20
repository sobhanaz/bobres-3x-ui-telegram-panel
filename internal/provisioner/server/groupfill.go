package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/adapter"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/provisioner/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/xui"
)

// GroupBackfillInterval is how often RunGroupBackfill looks again for a 3x-ui
// server whose clients are not all in the bot group, when nothing woke it
// sooner: a start, a new server, or a server that answers again.
const GroupBackfillInterval = 5 * time.Minute

// groupBackfillTimeout bounds the work on one server; a panel with many
// clients is listed a page at a time.
const groupBackfillTimeout = 10 * time.Minute

var (
	// errNotSettled: the panel's clients kept changing under the backfill, so a
	// listing after the tagging still showed clients to tag.
	errNotSettled = errors.New("the panel's clients kept changing during the backfill")
	errBadConfig  = errors.New("panel configuration is invalid")
	errNoGroups   = errors.New("this kind of panel has no client groups")
)

// groupFill is the state of the group backfill.
type groupFill struct {
	kick chan struct{} // wakes RunGroupBackfill; one pending wake-up is enough

	mu      sync.Mutex
	lastErr map[string]string // server id -> the failure last logged for it
}

func newGroupFill() groupFill {
	return groupFill{kick: make(chan struct{}, 1), lastErr: map[string]string{}}
}

// kickGroupBackfill wakes RunGroupBackfill without waiting for it.
func (s *Server) kickGroupBackfill() {
	select {
	case s.fill.kick <- struct{}{}:
	default:
	}
}

// RunGroupBackfill puts the clients BOBRES made on a 3x-ui panel before it
// grouped them into the panel's bot group (xui.BotGroup), once per server,
// until ctx ends. It runs at start, when a server is added or answers again
// (those wake it), and every interval for a server it could not finish (the
// panel was down): see BackfillGroups.
func (s *Server) RunGroupBackfill(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		s.BackfillGroups(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-s.fill.kick:
		}
	}
}

// BackfillGroups goes through every 3x-ui server (switched on or not) that has
// not had it yet, in parallel, and records each one it finished. For a server
// it takes the clients BOBRES holds there (its client map), looks at the
// panel's own listing, and puts into the bot group only those of them that are
// in NO group: a client an operator moved to another group keeps it, and a
// client BOBRES does not hold is never touched. Safe to repeat: a client that
// has a group is never changed, and a server that is done is not visited.
func (s *Server) BackfillGroups(ctx context.Context) {
	pending, err := s.st.GroupBackfillPending(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("3x-ui groups: listing the servers to go through failed", "err", err)
		}
		return
	}
	var wg sync.WaitGroup
	for _, ref := range pending {
		wg.Go(func() { s.backfillServer(ctx, ref) })
	}
	wg.Wait()
}

func (s *Server) backfillServer(ctx context.Context, ref store.ServerRef) {
	ctx, cancel := context.WithTimeout(ctx, groupBackfillTimeout)
	defer cancel()
	res, err := s.tagServer(ctx, ref.ID)
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, store.ErrNotFound):
		return // shutting down, or the server was deleted meanwhile
	case err != nil:
		s.groupFillFailed(ref, err)
		return
	}
	if err := s.st.MarkGroupBackfilled(ctx, ref.ID); err != nil {
		if ctx.Err() == nil {
			s.log.Warn("3x-ui groups: recording the backfill failed", "server", ref.Name, "err", err)
		}
		return
	}
	s.fill.mu.Lock()
	delete(s.fill.lastErr, ref.ID)
	s.fill.mu.Unlock()
	s.log.Info("3x-ui groups: backfill finished", "server", ref.Name, "group", xui.BotGroup,
		"managed", res.Managed, "tagged", res.Tagged, "already_in_a_group", res.Grouped, "missing", res.Missing)
}

// tagServer does the panel side of the backfill for one server: the result,
// or why the server cannot be finished now. With no client of ours there it
// does not call the panel at all.
func (s *Server) tagServer(ctx context.Context, id string) (adapter.TagResult, error) {
	sv, err := s.st.GetServer(ctx, id)
	if err != nil {
		return adapter.TagResult{}, err
	}
	a, err := s.adapterFor(sv)
	if err != nil {
		return adapter.TagResult{}, errBadConfig
	}
	tagger, ok := a.(adapter.GroupTagger)
	if !ok {
		return adapter.TagResult{}, errNoGroups
	}
	clients, err := s.st.ServerClients(ctx, sv.ID)
	if err != nil {
		return adapter.TagResult{}, err
	}
	managed := make(map[string]string, len(clients))
	for _, c := range clients {
		managed[c.Email] = c.XUISubID
	}
	res, err := tagger.TagUngrouped(ctx, managed)
	if err != nil {
		return res, err
	}
	if !res.Converged {
		return res, errNotSettled
	}
	return res, nil
}

// groupFillFailed logs why a server is not finished, once per distinct reason:
// the next attempt, minutes later, would say the same.
func (s *Server) groupFillFailed(ref store.ServerRef, err error) {
	var detail string
	switch {
	case errors.Is(err, errNotSettled), errors.Is(err, errBadConfig), errors.Is(err, errNoGroups):
		detail = err.Error()
	case errors.Is(err, store.ErrTokenUnreadable):
		detail = "the stored API token cannot be decrypted (was the master key changed?)"
	default:
		detail = panelDetail(err)
	}
	s.fill.mu.Lock()
	again := s.fill.lastErr[ref.ID] == detail
	s.fill.lastErr[ref.ID] = detail
	s.fill.mu.Unlock()
	if !again {
		s.log.Warn("3x-ui groups: backfill not finished, it will be tried again", "server", ref.Name, "detail", detail)
	}
}

// ensureTagged puts a client in the bot group when it was left in none, for a
// create that is the retry of an earlier attempt, made before BOBRES grouped
// its clients, whose answer was lost (the client may be on the panel already).
// A group an operator chose is kept. Best effort: the client works without the label.
func (s *Server) ensureTagged(ctx context.Context, a adapter.PanelAdapter, email string) {
	t, ok := a.(adapter.GroupTagger)
	if !ok {
		return
	}
	if err := t.EnsureTagged(ctx, email); err != nil && ctx.Err() == nil {
		s.log.Warn("3x-ui groups: could not group an adopted client", "detail", panelDetail(err))
	}
}
