package handler

import (
	"context"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// The required channels and groups (docs/03-bot-ux.md, "Required chats").
//
// The first is the setting join.channel (with join.link and join.label), as it
// always was; the others are the extra chats core sends with the settings
// (Settings.join_chats), already limited to what the license allows. A customer
// must be in every one of them. The prompt lists the chats they have not joined,
// each with its own Join button, and one "I've joined" button (verb jn) that
// asks about all of them again and says which are still missing.
//
// Telegram is asked as little as can be: a chat a customer is a member of is
// remembered for ten minutes, a customer who is a member of all of them for one
// lookup of the cache, and a chat they have not joined is not asked about again
// by an ordinary update for a couple of seconds (only the "I've joined" button
// always asks). The lookups of one customer run a few at a time, the lookups of
// all customers are bounded, and each has its own timeout, so a store with many
// chats cannot make one tap wait for long or send Telegram a storm.

const (
	// joinCacheTTL is how long a confirmed channel member is not asked again.
	joinCacheTTL = 10 * time.Minute
	// joinMissCacheTTL is how long a chat the customer has not joined is not
	// asked about again by an ordinary update (a message, a tap on a menu).
	joinMissCacheTTL = 2 * time.Second
	// joinCallTimeout is how long Telegram has to answer one lookup, and
	// joinCheckBudget how long all the lookups for one update may take.
	joinCallTimeout = 3 * time.Second
	joinCheckBudget = 8 * time.Second
	// joinParallelUser is how many chats one customer's check asks about at once;
	// joinParallelAll how many lookups are in flight for all customers.
	joinParallelUser = 4
	joinParallelAll  = 8
	// joinToastNames bounds the names in the popup that lists what is missing
	// (a popup holds 200 characters).
	joinToastNames = 110
)

// joinReq is a channel or group customers must join.
type joinReq struct {
	Chat  string // a public chat's @name, or a private chat's -100… id
	Link  string // the https://t.me/… link of the Join button; "" = the public chat's t.me page
	Label string // the short name on the button; "" = the @name
}

// key names the chat in the caches (the case of an @name does not matter).
func (c joinReq) key() string { return strings.ToLower(c.Chat) }

// url is where the Join button leads: the invite link, else a public chat's
// t.me page ("" for a private chat without a link).
func (c joinReq) url() string {
	if httpsURL(c.Link) {
		return c.Link
	}
	if name, ok := strings.CutPrefix(c.Chat, "@"); ok && name != "" {
		return "https://t.me/" + name
	}
	return ""
}

// joinList is every chat customers must join now, in the order they are
// asked: the setting join.channel and the extras core sent. Empty when the
// requirement is off (no first chat).
func (h *Handler) joinList() []joinReq {
	first := h.set.Get("join.channel")
	if first == "" {
		return nil
	}
	out := []joinReq{{Chat: first, Link: h.set.Get("join.link"), Label: h.set.Get("join.label")}}
	if extras := h.joinExtras.Load(); extras != nil {
		seen := map[string]bool{out[0].key(): true}
		for _, c := range *extras {
			if !seen[c.key()] {
				seen[c.key()] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// joinName is what the customer calls a chat: its label, else a public chat's
// @name, else "channel N" (N counts from 1 in the list). Never a private
// chat's id.
func (r *req) joinName(c joinReq, list []joinReq) string {
	if c.Label != "" {
		return c.Label
	}
	if strings.HasPrefix(c.Chat, "@") {
		return c.Chat
	}
	n := 0
	for i, x := range list {
		if x.key() == c.key() {
			n = i + 1
		}
	}
	return r.t("join.unnamed", "n", n)
}

// joinPrompt is the join message: the chats still missing, each with a Join
// button, and "I've joined". With one chat required it is the message it
// always was.
func (r *req) joinPrompt(missing []joinReq) (string, *tg.Keyboard) {
	list := r.h.joinList()
	kb := &tg.Keyboard{}
	for _, c := range missing {
		link := c.url()
		switch {
		case link == "":
		case len(list) <= 1:
			kb.Row(r.linkButton("btn.join_channel", link))
		default:
			b := tg.Link(r.t("btn.join_chat", "name", r.joinName(c, list)), link)
			b.Style = "primary"
			kb.Row(b)
		}
	}
	kb.Row(r.button("btn.join_check", "jn"))
	if len(list) <= 1 {
		return r.t("join.prompt"), kb
	}
	return r.t("join.prompt_many"), kb
}

// joinListHash identifies the list in the cache of customers who are members
// of all of it, so that a change of the list asks again.
func joinListHash(list []joinReq) string {
	h := fnv.New64a()
	for _, c := range list {
		_, _ = h.Write([]byte(c.key()))
		_, _ = h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// missingChats returns the required chats the user has not joined, in order
// (none: the user may go on). Members are remembered for a while; non-members
// are not, beyond the short cache of the ordinary update, so that "I've joined"
// (fresh) checks again at once. A chat Telegram cannot tell about (the bot is
// not its admin, a timeout) is skipped, as the single required channel always
// was: a chat set up wrongly must not lock every customer out, and the other
// chats still hold.
func (h *Handler) missingChats(ctx context.Context, userID int64, fresh bool) []joinReq {
	list := h.joinList()
	if len(list) == 0 {
		return nil
	}
	uid := strconv.FormatInt(userID, 10)
	allKey := "bot:join:" + uid + ":" + joinListHash(list)
	// A member of every chat of the list is one lookup.
	if ok, err := h.state.Recall(ctx, allKey); err == nil && ok {
		return nil
	}
	chatKey := func(c joinReq) string {
		if len(list) == 1 {
			return allKey
		}
		return "bot:join:" + uid + ":c:" + c.key()
	}
	missKey := func(c joinReq) string { return "bot:joinno:" + uid + ":" + c.key() }

	known := map[string]bool{} // chat -> still missing, without asking Telegram
	var ask []joinReq
	for _, c := range list {
		if ok, err := h.state.Recall(ctx, chatKey(c)); err == nil && ok {
			continue
		}
		if !fresh && h.joinMissTTL > 0 {
			if no, err := h.state.Recall(ctx, missKey(c)); err == nil && no {
				known[c.key()] = true
				continue
			}
		}
		ask = append(ask, c)
	}
	answers := h.askChats(ctx, userID, ask)
	var missing []joinReq
	confirmed := true // every chat is known to be joined (none was skipped for an error)
	for _, c := range list {
		switch {
		case known[c.key()]:
			missing = append(missing, c)
			confirmed = false
		case answers[c.key()] == joinNo:
			missing = append(missing, c)
			confirmed = false
			if h.joinMissTTL > 0 {
				if err := h.state.Remember(ctx, missKey(c), h.joinMissTTL); err != nil {
					h.log.Debug("join cache not saved", "err", err)
				}
			}
		case answers[c.key()] == joinUnknown:
			confirmed = false
		case answers[c.key()] == joinYes:
			if err := h.state.Remember(ctx, chatKey(c), joinCacheTTL); err != nil {
				h.log.Debug("join cache not saved", "err", err)
			}
		}
	}
	if confirmed && len(missing) == 0 {
		if err := h.state.Remember(ctx, allKey, joinCacheTTL); err != nil {
			h.log.Debug("join cache not saved", "err", err)
		}
	}
	return missing
}

// What Telegram said about a user in a chat.
type joinVerdict int

const (
	joinAsked   joinVerdict = iota // not asked (the zero value)
	joinYes                        // a member
	joinNo                         // not a member
	joinUnknown                    // Telegram could not tell
)

// askChats asks Telegram about the user in each chat, a few at a time, within
// a time budget, and says what it answered (by chat key). Chats not answered
// in time are unknown.
func (h *Handler) askChats(ctx context.Context, userID int64, chats []joinReq) map[string]joinVerdict {
	out := make(map[string]joinVerdict, len(chats))
	if len(chats) == 0 {
		return out
	}
	budget, cancel := context.WithTimeout(ctx, joinCheckBudget)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	local := make(chan struct{}, joinParallelUser)
	for _, c := range chats {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := joinUnknown
			defer func() {
				mu.Lock()
				out[c.key()] = v
				mu.Unlock()
			}()
			if !acquire(budget, local) {
				return
			}
			defer func() { <-local }()
			if !acquire(budget, h.joinSem) {
				return
			}
			defer func() { <-h.joinSem }()
			callCtx, stop := context.WithTimeout(budget, joinCallTimeout)
			m, err := h.tg.GetChatMember(callCtx, c.Chat, userID)
			stop()
			switch {
			case err != nil:
				// Once a minute at Warn: every customer's update would log it.
				if first, onceErr := h.state.Once(ctx, "join-check-failed", time.Minute); onceErr != nil || first {
					h.log.Warn("cannot check members of a required channel; letting users in for it (is the bot its admin?)",
						"channel", c.Chat, "err", err)
				}
			case m.Joined():
				v = joinYes
			default:
				v = joinNo
			}
		}()
	}
	wg.Wait()
	return out
}

// acquire takes a place in sem, or reports false when ctx ends first.
func acquire(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// joinNames lists the chats a user has not joined for the popup: their names,
// as many as fit.
func (r *req) joinNames(missing, list []joinReq) string {
	var names []string
	size := 0
	for _, c := range missing {
		n := r.joinName(c, list)
		if size+len([]rune(n)) > joinToastNames {
			names = append(names, "…")
			break
		}
		size += len([]rune(n)) + 2
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

// askToJoin sends the join prompt for the chats the user has not joined: a
// Join button for each and "I've joined" (jn). Always a new message: the button
// pressed may sit under a notification (a delivered service and its link),
// which must stay as it is.
func (h *Handler) askToJoin(r *req, missing []joinReq) {
	text, kb := r.joinPrompt(missing)
	r.send(text, kb)
}

// onJoinCheck handles "jn": the user says they joined. Every chat is asked
// about again, at once; when some are still missing the popup names them and
// the prompt under it keeps only their buttons.
func (h *Handler) onJoinCheck(r *req) {
	if list := h.joinList(); len(list) > 0 && !r.isTeam() && !h.isOwnerID(r) {
		if missing := h.missingChats(r.ctx, r.from.ID, true); len(missing) > 0 {
			if len(list) == 1 {
				r.toast, r.alert = r.t("join.not_yet"), true
				return
			}
			r.toast, r.alert = r.t("join.not_yet_many", "names", r.joinNames(missing, list)), true
			text, kb := r.joinPrompt(missing)
			r.show(text, kb)
			return
		}
	}
	h.showHome(r) // the language picker for a user not registered yet
}

// gateNewUser runs the gates for a user who just picked a language (the
// picker itself is not gated): the picker becomes the welcome, without the
// bottom menu, and the maintenance message or the join prompt comes instead
// of the first home menu. It reports whether it answered.
func (h *Handler) gateNewUser(r *req) bool {
	if r.isTeam() || h.isOwnerID(r) {
		return false
	}
	maintenance := h.set.Bool("maintenance.enabled", false)
	var missing []joinReq
	if !maintenance {
		if missing = h.missingChats(r.ctx, r.from.ID, false); len(missing) == 0 {
			return false
		}
	}
	r.show(r.t("welcome", "brand", esc(h.brand())), nil)
	if maintenance {
		r.send(r.t("maintenance"), nil)
	} else {
		h.askToJoin(r, missing)
	}
	return true
}
