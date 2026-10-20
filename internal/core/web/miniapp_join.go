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

// The store's required channels and groups in the Mini App
// (docs/17-mini-app.md): the app holds them as the bot's gate does
// (internal/bot/handler/gates.go and joinchats.go). The first is the setting
// join.channel; the others the extra chats the license allows (domain.JoinChats).
// A customer who has not joined one of them signs in to a 403 join_required that
// carries the public link of the first they still lack (join_url) and, when the
// store requires more than one chat, every chat they still lack (join_chats),
// and a session that outlives the customer's membership (or the owner turning
// the requirement on) is stopped the same way at its next call. Only the bot can
// ask Telegram who is in a chat, so core asks it, one chat at a time.

// MiniAppMembers says whether a customer is in a required chat
// (botfiles.Client asks the bot, which is the chat's administrator).
type MiniAppMembers interface {
	ChannelMember(ctx context.Context, channel string, telegramID int64) (bool, error)
}

const (
	// miniJoinMemberTTL is how long a confirmed member of a chat is not asked
	// about again: as long as the bot remembers one (handler.joinCacheTTL). A
	// non-member is never remembered, so that "I've joined" checks again at once.
	miniJoinMemberTTL = 10 * time.Minute
	// miniJoinLetInTTL is how long a customer who was let in because Telegram
	// could not tell is not asked about that chat again. The bot asks again at
	// every message; every call of the app would, and each could wait for the
	// bot's three seconds with Telegram.
	miniJoinLetInTTL = time.Minute
	// miniJoinMax bounds the table (one entry for each customer and chat).
	miniJoinMax = 100_000
	// miniJoinParallel is how many chats one customer's check asks the bot about
	// at once, and miniJoinBudget how long the whole check may take: a store with
	// many chats must not make one call wait, or the bot answer, without end. A
	// chat unanswered by then counts as one Telegram could not tell about.
	miniJoinParallel = 4
	miniJoinBudget   = 10 * time.Second
	// miniJoinListTTL is how long the extra chats the license allows are kept
	// before they are read again (an owner's change forgets them at once).
	miniJoinListTTL = 3 * time.Second
)

// errNoMembers: the app has no bot to ask.
var errNoMembers = errors.New("the bot is not connected")

// miniLetKey is a customer in one chat.
type miniLetKey struct {
	id   int64 // Telegram id
	chat string
}

// miniJoin remembers who need not be asked about a chat for a while, and the
// extra chats that are required.
type miniJoin struct {
	mu      sync.Mutex
	letIn   map[miniLetKey]time.Time // until
	lastLog atomic.Int64             // when a failed check was last logged (Unix seconds)

	listMu  sync.Mutex
	extras  []domain.JoinChat
	listAt  time.Time
	listSet bool
}

func newMiniJoin() *miniJoin { return &miniJoin{letIn: map[miniLetKey]time.Time{}} }

func letKey(telegramID int64, chat string) miniLetKey {
	return miniLetKey{telegramID, strings.ToLower(chat)}
}

// allowed reports whether the customer was let in for this chat and need not
// be asked yet.
func (j *miniJoin) allowed(telegramID int64, chat string, now time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	k := letKey(telegramID, chat)
	until, ok := j.letIn[k]
	if ok && !now.Before(until) {
		delete(j.letIn, k)
		return false
	}
	return ok
}

// allow lets the customer in for this chat without asking for ttl. A full table
// forgets what has run out first and, when it is still full, this customer:
// asking again is the safe way to be wrong.
func (j *miniJoin) allow(telegramID int64, chat string, ttl time.Duration, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	k := letKey(telegramID, chat)
	if _, ok := j.letIn[k]; !ok && len(j.letIn) >= miniJoinMax {
		for key, until := range j.letIn {
			if !now.Before(until) {
				delete(j.letIn, key)
			}
		}
		if len(j.letIn) >= miniJoinMax {
			return
		}
	}
	j.letIn[k] = now.Add(ttl)
}

// forget drops the extra chats kept, so that the next call reads them again
// (the owner saved the list).
func (j *miniJoin) forget() {
	j.listMu.Lock()
	defer j.listMu.Unlock()
	j.listSet = false
}

// miniJoinChat is a chat a customer still has to join.
type miniJoinChat struct {
	// url is where the join button leads ("" for a private chat with no link).
	url string
	// label is the name to show: the store's own, else a public chat's @name,
	// else "" (the app then numbers it).
	label string
}

// miniJoinNeeded is what a customer who has not joined is told.
type miniJoinNeeded struct {
	// chats are the chats the customer has not joined, in the order they are asked.
	chats []miniJoinChat
	// several: the store requires more than one chat, so the answer names them.
	several bool
}

// miniRequiredChats is the list a customer is held to: the first chat from the
// settings the caller read, and the extras the license allows, each chat once.
// When the extras cannot be read the first chat alone is enforced (the floor of
// every license) and the failure is logged.
func (s *Server) miniRequiredChats(ctx context.Context, settings map[string]string) []domain.JoinChat {
	primary := domain.JoinChat{Chat: settings["join.channel"], Link: settings["join.link"], Label: settings["join.label"]}
	if primary.Chat == "" {
		return nil
	}
	out := []domain.JoinChat{primary}
	seen := map[string]bool{strings.ToLower(primary.Chat): true}
	for _, c := range s.miniJoinExtras(ctx) {
		if k := strings.ToLower(c.Chat); !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out
}

// miniJoinExtras reads the extra chats now enforced, at most every few seconds.
func (s *Server) miniJoinExtras(ctx context.Context) []domain.JoinChat {
	j := s.mini.join
	now := s.now()
	j.listMu.Lock()
	defer j.listMu.Unlock()
	if age := now.Sub(j.listAt); j.listSet && age >= 0 && age < miniJoinListTTL {
		return j.extras
	}
	extras, err := s.cfg.Domain.EnforcedJoinExtras(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.cfg.Log.Warn("mini app: cannot read the extra required chats; only the first is enforced", "err", err)
		}
		return nil // not remembered: the next call tries again
	}
	j.extras, j.listAt, j.listSet = extras, now, true
	return extras
}

// miniJoinGate is the store's required chats for one customer: nil lets them
// in, else they must join first. The bot's rules, copied:
//   - no first chat set: everyone is let in;
//   - staff and the configured owners are never asked (the bot's gate lets
//     them through, whether or not they have an account there);
//   - in maintenance the maintenance message comes first and nobody is asked
//     about the chats (the bot's gate asks about one of the two);
//   - a confirmed member of a chat is not asked about it again for ten minutes,
//     a non-member every time;
//   - when Telegram cannot tell about a chat (the bot is not its administrator,
//     a timeout, a bot too old for the question) the customer is let in for that
//     chat and the failure is logged: a chat set up wrongly must not lock every
//     customer out, and the others still hold.
func (s *Server) miniJoinGate(ctx context.Context, settings map[string]string, u *store.User) *miniJoinNeeded {
	switch {
	case settings["join.channel"] == "":
		return nil
	case domain.IsStaffRole(u.Role) || s.cfg.Domain.IsConfiguredOwner(u):
		return nil
	case s.miniMaintenance(settings, u):
		return nil
	}
	chats := s.miniRequiredChats(ctx, settings)
	now := s.now()
	var todo []int // the chats the customer is not remembered for
	for i, c := range chats {
		if !s.mini.join.allowed(u.TelegramID, c.Chat, now) {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return nil
	}
	type answer struct {
		member bool
		err    error
	}
	answers := make([]answer, len(todo))
	askCtx, cancel := context.WithTimeout(ctx, miniJoinBudget)
	defer cancel()
	var wg sync.WaitGroup
	sem := make(chan struct{}, miniJoinParallel)
	for k, i := range todo {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			answers[k].member, answers[k].err = s.askMember(askCtx, chats[i].Chat, u.TelegramID)
		}()
	}
	wg.Wait()
	need := &miniJoinNeeded{several: len(chats) > 1}
	for k, i := range todo {
		a, c := answers[k], chats[i]
		switch {
		case a.err != nil && ctx.Err() != nil:
			// The customer gave up on this request while the bot was asked: nothing was
			// learned, so nothing is remembered (that would let a customer outside the chat
			// in for a minute by hanging up at the right moment) and the chat is not at fault.
			need.chats = append(need.chats, miniJoinChat{url: miniJoinURL(c), label: c.JoinName()})
		case a.err != nil:
			s.logJoinCheck(a.err, now)
			s.mini.join.allow(u.TelegramID, c.Chat, miniJoinLetInTTL, now)
		case a.member:
			s.mini.join.allow(u.TelegramID, c.Chat, miniJoinMemberTTL, now)
		default:
			need.chats = append(need.chats, miniJoinChat{url: miniJoinURL(c), label: c.JoinName()})
		}
	}
	if len(need.chats) == 0 {
		return nil
	}
	return need
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

// miniJoinLink is where the join button of the first chat leads, from the
// settings (see miniJoinURL).
func miniJoinLink(settings map[string]string) string {
	return miniJoinURL(domain.JoinChat{Chat: settings["join.channel"], Link: settings["join.link"]})
}

// miniJoinURL is where a chat's join button leads, as in the bot's: its invite
// link, else a public chat's t.me page, and "" for a private chat without a
// link. It is a link meant for customers and nothing else of the chat's: never
// its id. Telegram's script opens only t.me links inside Telegram, so an address
// on telegram.me (the same place) is written with t.me.
func miniJoinURL(c domain.JoinChat) string {
	if u, err := url.Parse(c.Link); err == nil && u.Scheme == "https" && u.User == nil && (u.Host == "t.me" || u.Host == "telegram.me") {
		if rest, ok := strings.CutPrefix(c.Link, "https://telegram.me"); ok {
			return "https://t.me" + rest
		}
		return c.Link
	}
	if name, ok := strings.CutPrefix(c.Chat, "@"); ok && name != "" {
		return "https://t.me/" + name
	}
	return ""
}

// miniJoinRequired answers a customer who must join first. The code is the
// app's cue for its join screen; join_url is the public link to the first
// chat they lack, always present (empty when there is none). When the store
// requires several chats, join_chats lists every chat they lack, each with its
// link and the name to show (both may be empty).
func miniJoinRequired(w http.ResponseWriter, n *miniJoinNeeded) {
	out := map[string]any{
		"error": "join_required", "message": "join the store's channel to continue", "join_url": n.chats[0].url,
	}
	if n.several {
		chats := make([]map[string]string, len(n.chats))
		for i, c := range n.chats {
			chats[i] = map[string]string{"url": c.url, "label": c.label}
		}
		out["join_chats"] = chats
	}
	writeJSON(w, http.StatusForbidden, out)
}
