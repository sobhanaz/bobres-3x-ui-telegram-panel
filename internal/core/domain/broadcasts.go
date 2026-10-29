package domain

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Broadcast limits.
const (
	// BroadcastLease is how long the bot may hold claimed recipients without
	// reporting; after it they count as unknown and are never sent again.
	BroadcastLease  = 10 * time.Minute
	maxScheduleAway = 30 * 24 * time.Hour
	maxButtonURL    = 1024
)

// BroadcastSegments are the audiences staff pick from.
var BroadcastSegments = []string{"all", "active", "expired", "never_bought"}

// BroadcastInput is what staff compose.
type BroadcastInput struct {
	Segment      string
	Language     string // "" (everyone) | fa | en
	Texts        map[string]string
	ButtonURL    string // "" = no button
	ButtonLabels map[string]string
}

// broadcastLangs are the languages a broadcast needs texts in.
func broadcastLangs(language string) []string {
	if language == "" {
		return i18n.Languages
	}
	return []string{language}
}

// CheckBroadcastURL accepts an https link for a broadcast button.
func CheckBroadcastURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(v) > maxButtonURL {
		return &FieldError{"button.url", "must be an https:// link"}
	}
	return nil
}

// normalizeBroadcast checks a broadcast's audience, texts and button and
// keeps only the languages its audience needs.
func normalizeBroadcast(in BroadcastInput) (*store.Broadcast, error) {
	if !slices.Contains(BroadcastSegments, in.Segment) {
		return nil, &FieldError{"segment", "one of all, active, expired, never_bought"}
	}
	if in.Language != "" && !slices.Contains(i18n.Languages, in.Language) {
		return nil, &FieldError{"language", "empty (everyone), fa or en"}
	}
	b := &store.Broadcast{Segment: in.Segment, Language: in.Language, Texts: map[string]string{}}
	for _, l := range broadcastLangs(in.Language) {
		v, probs := i18n.ValidateMessage(in.Texts[l], i18n.MaxMessage)
		if len(probs) > 0 {
			return nil, &TextError{Lang: l, Key: "texts." + l, Problems: probs}
		}
		b.Texts[l] = v
	}
	if in.ButtonURL != "" {
		if err := CheckBroadcastURL(in.ButtonURL); err != nil {
			return nil, err
		}
		b.ButtonURL, b.ButtonLabels = in.ButtonURL, map[string]string{}
		for _, l := range broadcastLangs(in.Language) {
			v, probs := i18n.ValidateLabel(in.ButtonLabels[l])
			if len(probs) > 0 {
				return nil, &FieldError{"button.labels." + l, "a short label without < > & (at most 64 characters)"}
			}
			b.ButtonLabels[l] = v
		}
	}
	return b, nil
}

func broadcastAudit(b *store.Broadcast) map[string]any {
	out := map[string]any{"segment": b.Segment, "language": b.Language, "texts": b.Texts}
	if b.ButtonURL != "" {
		out["button"] = map[string]any{"url": b.ButtonURL, "labels": b.ButtonLabels}
	}
	return out
}

// CreateBroadcast saves a draft.
func (s *Service) CreateBroadcast(ctx context.Context, actor *store.User, in BroadcastInput) (*store.Broadcast, error) {
	b, err := normalizeBroadcast(in)
	if err != nil {
		return nil, err
	}
	b.CreatedBy = actor.ID
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.InsertBroadcast(ctx, tx, b); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "broadcast.create", "broadcast", b.ID, broadcastAudit(b), "")
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetBroadcast(ctx, s.st.Conn(), b.ID, false)
}

// UpdateBroadcast changes a draft or scheduled broadcast read at version.
func (s *Service) UpdateBroadcast(ctx context.Context, actor *store.User, id string, in BroadcastInput, version int) (*store.Broadcast, error) {
	b, err := normalizeBroadcast(in)
	if err != nil {
		return nil, err
	}
	b.ID = id
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetBroadcast(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if old.Status != "draft" && old.Status != "scheduled" {
			return stateErr("only a draft or a scheduled broadcast can be changed")
		}
		if old.Segment == store.SegmentResellers {
			return stateErr("a message to resellers cannot be changed; cancel it and write a new one")
		}
		if err := s.st.UpdateBroadcast(ctx, tx, b, version); errors.Is(err, store.ErrStale) {
			return stateErr("someone changed this broadcast meanwhile; reload it")
		} else if err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "broadcast.update", "broadcast", id, broadcastAudit(old), broadcastAudit(b), "")
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetBroadcast(ctx, s.st.Conn(), id, false)
}

// DeleteBroadcast deletes a draft.
func (s *Service) DeleteBroadcast(ctx context.Context, actor *store.User, id string) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetBroadcast(ctx, tx, id, true)
		if err != nil {
			return err
		}
		ok, err := s.st.DeleteBroadcast(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			return stateErr("only a draft can be deleted; cancel it instead")
		}
		return s.auditChange(ctx, tx, actor, "broadcast.delete", "broadcast", id, broadcastAudit(old), nil, "")
	})
}

// BroadcastAudience counts who a broadcast would reach now.
func (s *Service) BroadcastAudience(ctx context.Context, segment, language string) (total, fa, en int64, err error) {
	if !slices.Contains(BroadcastSegments, segment) {
		return 0, 0, 0, &FieldError{"segment", "one of all, active, expired, never_bought"}
	}
	if language != "" && !slices.Contains(i18n.Languages, language) {
		return 0, 0, 0, &FieldError{"language", "empty (everyone), fa or en"}
	}
	return s.st.AudienceCount(ctx, s.st.Conn(), segment, language)
}

// SendBroadcast schedules a draft (or reschedules a scheduled broadcast) for
// at, or now when at is nil, if it is still at the version the sender read.
// The worker starts it when the time comes.
func (s *Service) SendBroadcast(ctx context.Context, actor *store.User, id string, at *time.Time, version int) (*store.Broadcast, error) {
	now := time.Now()
	when := now
	if at != nil {
		if at.Before(now.Add(-time.Minute)) || at.After(now.Add(maxScheduleAway)) {
			return nil, &FieldError{"at", "pick a time from now to 30 days ahead"}
		}
		if at.After(now) {
			when = *at
		}
	}
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		b, err := s.st.GetBroadcast(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if b.Version != version { // what goes out must be what the sender saw
			return stateErr("someone changed this broadcast meanwhile; reload it")
		}
		ok, err := s.st.SetBroadcastStatus(ctx, tx, id, []string{"draft", "scheduled"}, "scheduled", &when)
		if err != nil {
			return err
		}
		if !ok {
			return stateErr("this broadcast was sent already")
		}
		total, err := s.audienceNow(ctx, tx, b)
		if err != nil {
			return err
		}
		after := broadcastAudit(b)
		after["scheduled_at"], after["audience_now"] = when.Unix(), total
		return s.audit(ctx, tx, actor, "broadcast.send", "broadcast", id, after, "")
	})
	if err != nil {
		return nil, err
	}
	s.KickBroadcasts()
	return s.st.GetBroadcast(ctx, s.st.Conn(), id, false)
}

// audienceNow counts who a broadcast would reach now.
func (s *Service) audienceNow(ctx context.Context, q pgx.Tx, b *store.Broadcast) (int64, error) {
	if b.Segment == store.SegmentResellers {
		total, _, _, err := s.st.CountResellerAudience(ctx, q, store.ResellerAudience{DebtOnly: b.ResellerDebtOnly, TierID: b.ResellerTierID, Language: b.Language})
		return total, err
	}
	total, _, _, err := s.st.AudienceCount(ctx, q, b.Segment, b.Language)
	return total, err
}

// changeBroadcast moves a broadcast between statuses, with an audit entry.
func (s *Service) changeBroadcast(ctx context.Context, actor *store.User, id, action, to string, from []string, refuse string) (*store.Broadcast, error) {
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		b, err := s.st.GetBroadcast(ctx, tx, id, true)
		if err != nil {
			return err
		}
		ok, err := s.st.SetBroadcastStatus(ctx, tx, id, from, to, nil)
		if err != nil {
			return err
		}
		if !ok {
			return stateErr("%s", refuse)
		}
		if to == "cancelled" {
			if err := s.st.CancelPending(ctx, tx, id); err != nil {
				return err
			}
		}
		return s.auditChange(ctx, tx, actor, action, "broadcast", id,
			map[string]string{"status": b.Status}, map[string]string{"status": to}, "")
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetBroadcast(ctx, s.st.Conn(), id, false)
}

// PauseBroadcast stops sending (recipients wait in line).
func (s *Service) PauseBroadcast(ctx context.Context, actor *store.User, id string) (*store.Broadcast, error) {
	return s.changeBroadcast(ctx, actor, id, "broadcast.pause", "paused", []string{"running"}, "only a running broadcast can be paused")
}

// ResumeBroadcast sends again after a pause.
func (s *Service) ResumeBroadcast(ctx context.Context, actor *store.User, id string) (*store.Broadcast, error) {
	b, err := s.changeBroadcast(ctx, actor, id, "broadcast.resume", "running", []string{"paused"}, "only a paused broadcast can be resumed")
	if err == nil {
		s.KickBroadcasts()
	}
	return b, err
}

// CancelBroadcast stops a broadcast for good; who has not got it yet won't.
func (s *Service) CancelBroadcast(ctx context.Context, actor *store.User, id string) (*store.Broadcast, error) {
	return s.changeBroadcast(ctx, actor, id, "broadcast.cancel", "cancelled", []string{"scheduled", "running", "paused"},
		"this broadcast is not waiting or sending")
}

// BroadcastBatch is what the bot claims.
type BroadcastBatch struct {
	Broadcast  *store.Broadcast
	Recipients []store.Recipient
}

// ClaimBroadcastBatch hands the bot the next recipients (nil: nothing to send).
func (s *Service) ClaimBroadcastBatch(ctx context.Context, limit int) (*BroadcastBatch, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var out *BroadcastBatch
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		b, rs, err := s.st.ClaimRecipients(ctx, tx, limit)
		if err != nil || b == nil {
			return err
		}
		out = &BroadcastBatch{Broadcast: b, Recipients: rs}
		return nil
	})
	return out, err
}

// ReportBroadcastResults records the bot's results; a halt reason pauses the
// broadcast (shown to staff). It answers the broadcast's status, so the bot
// stops when staff paused or cancelled it.
func (s *Service) ReportBroadcastResults(ctx context.Context, id string, results []store.Result, halt string) (string, error) {
	for _, r := range results {
		switch r.Outcome {
		case "sent", "blocked", "failed", "unknown", "released":
		default:
			return "", invalid("unknown outcome %q", r.Outcome)
		}
	}
	var status string
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.RecordResults(ctx, tx, id, results); err != nil {
			return err
		}
		if halt != "" {
			ok, err := s.st.HaltBroadcast(ctx, tx, id, halt)
			if err != nil {
				return err
			}
			if ok {
				if err := s.audit(ctx, tx, nil, "broadcast.halt", "broadcast", id, map[string]string{"reason": halt}, ""); err != nil {
					return err
				}
			}
		}
		b, err := s.st.GetBroadcast(ctx, tx, id, false)
		if err != nil {
			return err
		}
		status = b.Status
		return nil
	})
	if err == nil && status == "running" {
		s.KickBroadcasts() // finish it promptly when nobody is left
	}
	return status, err
}

// KickBroadcasts wakes the broadcast worker (a send now, a resume).
func (s *Service) KickBroadcasts() {
	select {
	case s.bkick <- struct{}{}:
	default:
	}
}

// BroadcastWorker starts scheduled broadcasts when their time comes, gives up
// on claims the bot never reported, and finishes broadcasts nobody is left in.
type BroadcastWorker struct {
	s     *Service
	log   *slog.Logger
	every time.Duration
	now   func() time.Time
}

// NewBroadcastWorker builds the worker (every 5 seconds, and when kicked).
func (s *Service) NewBroadcastWorker(log *slog.Logger) *BroadcastWorker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &BroadcastWorker{s: s, log: log, every: 5 * time.Second, now: time.Now}
}

// Run works until ctx ends.
func (w *BroadcastWorker) Run(ctx context.Context) error {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		if err := w.Step(ctx); err != nil {
			w.log.Warn("broadcast worker", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-w.s.bkick:
		}
	}
}

// Step does one round.
func (w *BroadcastWorker) Step(ctx context.Context) error {
	due, err := w.s.st.DueBroadcasts(ctx, w.s.st.Conn(), w.now())
	if err != nil {
		return err
	}
	for _, id := range due {
		err := w.s.st.WithTx(ctx, func(tx pgx.Tx) error {
			b, ok, err := w.s.st.StartBroadcast(ctx, tx, id, w.now(), w.s.personalize)
			if err != nil || !ok || b.Status != "done" {
				return err
			}
			return w.s.audit(ctx, tx, nil, "broadcast.finish", "broadcast", id, broadcastCounts(b), "")
		})
		if err != nil {
			return err
		}
	}
	if n, err := w.s.st.ExpireClaims(ctx, w.s.st.Conn(), BroadcastLease); err != nil {
		return err
	} else if n > 0 {
		w.log.Warn("broadcast recipients never reported by the bot", "count", n)
	}
	return w.s.st.WithTx(ctx, func(tx pgx.Tx) error {
		done, err := w.s.st.FinishBroadcasts(ctx, tx)
		if err != nil {
			return err
		}
		for i := range done {
			if err := w.s.audit(ctx, tx, nil, "broadcast.finish", "broadcast", done[i].ID, broadcastCounts(&done[i]), ""); err != nil {
				return err
			}
		}
		return nil
	})
}

func broadcastCounts(b *store.Broadcast) map[string]int {
	return map[string]int{"total": b.Total, "sent": b.Sent, "blocked": b.Blocked, "failed": b.Failed, "unknown": b.Unknown}
}

// Broadcasts lists broadcasts.
func (s *Service) Broadcasts(ctx context.Context, status string, p store.Page) ([]store.Broadcast, int64, error) {
	return s.st.ListBroadcasts(ctx, s.st.Conn(), status, p)
}
