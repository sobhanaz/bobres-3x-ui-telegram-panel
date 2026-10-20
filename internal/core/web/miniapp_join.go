package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// The store's required channel (join.channel) in the Mini App
// (docs/17-mini-app.md): the app holds it as the bot's gate does
// (internal/bot/handler/gates.go). A customer who is not in the channel signs
// in to a 403 join_required that carries the channel's public link, and a
// session that outlives the customer's membership (or the owner turning the
// channel on) is stopped the same way at its next call. Only the bot can ask
// Telegram who is in a channel, so core asks it.

// MiniAppMembers says whether a customer is in the store's required channel
// (botfiles.Client asks the bot, which is the channel's administrator).
type MiniAppMembers interface {
	ChannelMember(ctx context.Context, channel string, telegramID int64) (bool, error)
}

const (
	// miniJoinMemberTTL is how long a confirmed member is not asked about again:
	// as long as the bot remembers one (handler.joinCacheTTL). A non-member is
	// never remembered, so that "I've joined" checks again at once.
	miniJoinMemberTTL = 10 * time.Minute
	// miniJoinLetInTTL is how long a customer who was let in because Telegram
	// could not tell is not asked about again. The bot asks again at every
	// message; every call of the app would, and each could wait for the bot's
	// three seconds with Telegram.
	miniJoinLetInTTL = time.Minute
	// miniJoinMax bounds the table.
	miniJoinMax = 100_000
)

// errNoMembers: the app has no bot to ask.
var errNoMembers = errors.New("the bot is not connected")

// miniJoin remembers who need not be asked about the channel for a while.
type miniJoin struct {
	mu      sync.Mutex
	letIn   map[int64]miniLetIn // by Telegram id
	lastLog atomic.Int64        // when a failed check was last logged (Unix seconds)
}

// miniLetIn: a customer let in for this channel until a time.
type miniLetIn struct {
	channel string
	until   time.Time
}

func newMiniJoin() *miniJoin { return &miniJoin{letIn: map[int64]miniLetIn{}} }

// allowed reports whether the customer was let in for this channel and need not
// be asked yet (the channel changing starts the asking again).
func (j *miniJoin) allowed(telegramID int64, channel string, now time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, ok := j.letIn[telegramID]
	if ok && !now.Before(e.until) {
		delete(j.letIn, telegramID)
		return false
	}
	return ok && e.channel == channel
}

// allow lets the customer in without asking for ttl. A full table forgets what
// has run out first and, when it is still full, this customer: asking again is
// the safe way to be wrong.
func (j *miniJoin) allow(telegramID int64, channel string, ttl time.Duration, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.letIn[telegramID]; !ok && len(j.letIn) >= miniJoinMax {
		for id, e := range j.letIn {
			if !now.Before(e.until) {
				delete(j.letIn, id)
			}
		}
		if len(j.letIn) >= miniJoinMax {
			return
		}
	}
	j.letIn[telegramID] = miniLetIn{channel: channel, until: now.Add(ttl)}
}

// miniJoinNeeded is what a customer who has not joined is told.
type miniJoinNeeded struct {
	// url is where the join button leads ("" for a private channel with no link).
	url string
}

// miniJoinGate is the store's required channel for one customer: nil lets them
// in, else they must join first. The bot's rules, copied:
//   - no channel set: everyone is let in;
//   - staff and the configured owners are never asked (the bot's gate lets
//     them through, whether or not they have an account there);
//   - in maintenance the maintenance message comes first and nobody is asked
//     about the channel (the bot's gate asks about one of the two);
//   - a confirmed member is not asked again for ten minutes, a non-member
//     every time;
//   - when Telegram cannot tell (the bot is not the channel's administrator, a
//     timeout, a bot too old for the question) the customer is let in and the
//     failure is logged: a channel set up wrongly must not lock every customer
//     out.
func (s *Server) miniJoinGate(ctx context.Context, settings map[string]string, u *store.User) *miniJoinNeeded {
	channel := settings["join.channel"]
	switch {
	case channel == "":
		return nil
	case domain.IsStaffRole(u.Role) || s.cfg.Domain.IsConfiguredOwner(u):
		return nil
	case s.miniMaintenance(settings, u):
		return nil
	}
	now := s.now()
	if s.mini.join.allowed(u.TelegramID, channel, now) {
		return nil
	}
	member, err := s.askMember(ctx, channel, u.TelegramID)
	if err != nil {
		if ctx.Err() != nil {
			// The customer gave up on this request while the bot was asked: nothing was
			// learned, so nothing is remembered (that would let a customer outside the channel
			// in for a minute by hanging up at the right moment) and the channel is not at fault.
			return &miniJoinNeeded{url: miniJoinLink(settings)}
		}
		s.logJoinCheck(err, now)
		s.mini.join.allow(u.TelegramID, channel, miniJoinLetInTTL, now)
		return nil
	}
	if member {
		s.mini.join.allow(u.TelegramID, channel, miniJoinMemberTTL, now)
		return nil
	}
	return &miniJoinNeeded{url: miniJoinLink(settings)}
}

func (s *Server) askMember(ctx context.Context, channel string, telegramID int64) (bool, error) {
	if s.cfg.MiniAppMembers == nil {
		return false, errNoMembers
	}
	return s.cfg.MiniAppMembers.ChannelMember(ctx, channel, telegramID)
}

// logJoinCheck logs a check that could not be made, once a minute: it fails
// for every customer when it fails.
func (s *Server) logJoinCheck(err error, now time.Time) {
	at := now.Unix()
	if last := s.mini.join.lastLog.Load(); at-last >= 60 && s.mini.join.lastLog.CompareAndSwap(last, at) {
		s.cfg.Log.Warn("mini app: cannot check members of the join channel; letting customers in (is the bot its admin?)", "err", err)
	}
}

// miniJoinLink is where the join button leads, as in the bot's: join.link, else
// a public channel's t.me page, and "" for a private channel without a link.
// It is a link meant for customers and nothing else of the channel's: never its
// id. Telegram's script opens only t.me links inside Telegram, so an address on
// telegram.me (the same place) is written with t.me.
func miniJoinLink(settings map[string]string) string {
	link := settings["join.link"]
	if u, err := url.Parse(link); err == nil && u.Scheme == "https" && u.User == nil && (u.Host == "t.me" || u.Host == "telegram.me") {
		if rest, ok := strings.CutPrefix(link, "https://telegram.me"); ok {
			return "https://t.me" + rest
		}
		return link
	}
	if name, ok := strings.CutPrefix(settings["join.channel"], "@"); ok && name != "" {
		return "https://t.me/" + name
	}
	return ""
}

// miniJoinRequired answers a customer who must join the channel first. The code
// is the app's cue for its join screen; join_url is the public link to the
// channel, always present (empty when there is none).
func miniJoinRequired(w http.ResponseWriter, n *miniJoinNeeded) {
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": "join_required", "message": "join the store's channel to continue", "join_url": n.url,
	})
}
