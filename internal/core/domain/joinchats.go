package domain

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/license"
)

// The required chats (docs/03-bot-ux.md, "Required chats"): the channels and
// groups customers must join before they can use the bot or the Mini App.
//
// The FIRST one is what it has always been: the settings join.channel and
// join.link (and join.label, its optional name on the button). The bot's /set
// editor, the Mini App and every older bot read it there. The EXTRA ones are
// the rows of core.join_chats (migration 00029), in the order customers are
// asked. The dashboard edits the whole list at once (SetJoinChats); the first
// row is the setting.
//
// How many are enforced is the license's max_join_chats: the free tier and
// Standard have 1, Pro and the trial any number. Adding past the limit is
// refused; a limit that drops (a license that ended) deletes nothing: the
// first N chats of the list, the first one always, are enforced and the rest
// stay saved and count again when the limit allows.

const (
	// JoinLabelMax is the longest name of a required chat, in characters.
	JoinLabelMax = 32
	// MaxJoinChatsTotal bounds the list, the first chat included, whatever the
	// license says: every customer's update costs a lookup per chat they have
	// not joined, and a Telegram keyboard holds 100 buttons.
	MaxJoinChatsTotal = 50
	// GateJoinChats is the gate of a chat added past max_join_chats.
	GateJoinChats = "max_join_chats"
)

// JoinChat is one required channel or group.
type JoinChat struct {
	// Chat is a public chat's @name or a private chat's -100… id.
	Chat string
	// Link is the https://t.me/… address of the Join button; empty for a public
	// chat means its t.me page (JoinURL).
	Link string
	// Label is the short name customers see on the button; empty means the
	// chat's @name (a private chat then has none: JoinName is "").
	Label string
}

// JoinName is what customers call the chat: its label, else a public chat's
// @name, else "" (a private chat with no label). It never is a private chat's id.
func (c JoinChat) JoinName() string {
	if c.Label != "" {
		return c.Label
	}
	if strings.HasPrefix(c.Chat, "@") {
		return c.Chat
	}
	return ""
}

// JoinURL is where the Join button leads: the invite link, else a public
// chat's t.me page ("" for a private chat that has no link, which core does
// not accept). A telegram.me address is written with t.me, the place Telegram's
// script opens inside Telegram.
func (c JoinChat) JoinURL() string {
	if rest, ok := strings.CutPrefix(c.Link, "https://telegram.me"); ok {
		return "https://t.me" + rest
	}
	if c.Link != "" {
		return c.Link
	}
	if name, ok := strings.CutPrefix(c.Chat, "@"); ok && name != "" {
		return "https://t.me/" + name
	}
	return ""
}

// JoinChatItem is a chat of the list as the dashboard shows it.
type JoinChatItem struct {
	JoinChat
	// Primary: the setting join.channel, the first of the list.
	Primary bool
	// Active: customers are asked to join it under the entitlements in effect.
	Active bool
}

// JoinChatList is the required chats with what the license allows.
type JoinChatList struct {
	Items []JoinChatItem
	// On: the first chat is set, so customers must join. An extra chat saved
	// while the first one is empty (the bot's /set can empty it) does nothing.
	On bool
	// Limit is license.Unlimited or the number of chats the license allows.
	Limit int
	// Basis and Tier name what the limit comes from (license.Effective).
	Basis license.Basis
	Tier  string
}

// joinLimit is the number of chats the entitlements enforce: at least the
// first one, whatever a corrupt value says.
func joinLimit(eff *EffectiveLicense) int {
	if n := eff.Entitlements.MaxJoinChats; n == license.Unlimited || n >= 1 {
		return n
	}
	return license.FreeMaxJoinChats
}

// joinActive is how many of total chats are enforced under limit.
func joinActive(limit, total int) int {
	if limit == license.Unlimited || limit > total {
		return total
	}
	return limit
}

// primaryJoin is the first required chat as the settings have it (Chat is ""
// when the requirement is off).
func primaryJoin(settings map[string]string) JoinChat {
	return JoinChat{Chat: settings["join.channel"], Link: settings["join.link"], Label: settings["join.label"]}
}

func joinChatsOf(rows []store.JoinChat) []JoinChat {
	out := make([]JoinChat, len(rows))
	for i, r := range rows {
		out[i] = JoinChat{Chat: r.Chat, Link: r.Link, Label: r.Label}
	}
	return out
}

// JoinChats lists the required chats in order with what is enforced.
func (s *Service) JoinChats(ctx context.Context) (*JoinChatList, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.st.ListJoinChats(ctx, s.st.Conn())
	if err != nil {
		return nil, err
	}
	eff, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	out := &JoinChatList{Limit: joinLimit(eff), Basis: eff.Basis, Tier: eff.Tier}
	primary := primaryJoin(settings)
	if primary.Chat != "" {
		out.On = true
		out.Items = append(out.Items, JoinChatItem{JoinChat: primary, Primary: true})
	}
	for _, c := range joinChatsOf(rows) {
		out.Items = append(out.Items, JoinChatItem{JoinChat: c})
	}
	if out.On {
		for i := range joinActive(out.Limit, len(out.Items)) {
			out.Items[i].Active = true
		}
	}
	return out, nil
}

// RequiredChats is the list customers are held to now: the first chat (read
// from settings, the map the caller has) and the extra ones the entitlements
// enforce, in order, each chat once (the case of an @name aside). Empty when
// the requirement is off. The caller decides what an error means; the
// Mini App's gate lets customers in, the bot's settings go without extras.
func (s *Service) RequiredChats(ctx context.Context, settings map[string]string) ([]JoinChat, error) {
	primary := primaryJoin(settings)
	if primary.Chat == "" {
		return nil, nil
	}
	extras, err := s.EnforcedJoinExtras(ctx)
	if err != nil {
		return []JoinChat{primary}, err
	}
	out := []JoinChat{primary}
	seen := map[string]bool{strings.ToLower(primary.Chat): true}
	for _, c := range extras {
		if k := strings.ToLower(c.Chat); !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out, nil
}

// EnforcedJoinExtras is the extra required chats customers are held to under
// the entitlements in effect (the bot gets them with its settings). It is
// empty when the first chat is not set: the requirement is then off.
func (s *Service) EnforcedJoinExtras(ctx context.Context) ([]JoinChat, error) {
	rows, err := s.st.ListJoinChats(ctx, s.st.Conn())
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if settings["join.channel"] == "" {
		return nil, nil
	}
	eff, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	extras := joinChatsOf(rows)
	return extras[:max(0, joinActive(joinLimit(eff), len(extras)+1)-1)], nil
}

// joinField names a field of row i of the list in a FieldError.
func joinField(i int, part string) string { return "chats." + strconv.Itoa(i) + "." + part }

// checkJoinChat checks one row of the list as the settings check their keys and
// returns what is stored.
func checkJoinChat(i int, c JoinChat) (JoinChat, error) {
	field := func(part, key, value string) (string, error) {
		v, err := NormalizeSetting(key, value)
		var fe *FieldError
		if errors.As(err, &fe) {
			return "", &FieldError{joinField(i, part), fe.Msg}
		}
		return v, err
	}
	var out JoinChat
	var err error
	if out.Chat, err = field("chat", "join.channel", c.Chat); err != nil {
		return JoinChat{}, err
	}
	if out.Chat == "" {
		return JoinChat{}, &FieldError{joinField(i, "chat"), "a public channel or group's @name, or a private one's id starting with -100"}
	}
	if out.Link, err = field("link", "join.link", c.Link); err != nil {
		return JoinChat{}, err
	}
	if out.Label, err = field("label", "join.label", c.Label); err != nil {
		return JoinChat{}, err
	}
	if strings.HasPrefix(out.Chat, "-100") && out.Link == "" {
		return JoinChat{}, &FieldError{joinField(i, "link"), "a private channel or group needs its invite link"}
	}
	return out, nil
}

// SetJoinChats saves the whole list: the first row becomes the setting
// join.channel (with join.link and join.label), the others the extra chats,
// all in one transaction with an audit entry for each setting and each chat
// that changed. A chat can be added only up to max_join_chats (a *LicenseRefusal
// otherwise); a list that is not longer than the one saved is always accepted,
// so a store over its limit (its license ended) can still remove, edit and
// reorder what it has. An empty list turns the requirement off.
func (s *Service) SetJoinChats(ctx context.Context, actor *store.User, in []JoinChat) (*JoinChatList, error) {
	if len(in) > MaxJoinChatsTotal {
		return nil, &FieldError{"chats", "at most " + strconv.Itoa(MaxJoinChatsTotal) + " channels and groups"}
	}
	chats := make([]JoinChat, len(in))
	seen := map[string]int{}
	for i, c := range in {
		nc, err := checkJoinChat(i, c)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(nc.Chat)
		if j, dup := seen[key]; dup {
			return nil, &FieldError{joinField(i, "chat"), "already in the list (row " + strconv.Itoa(j+1) + ")"}
		}
		seen[key] = i
		chats[i] = nc
	}
	eff, err := s.Effective(ctx)
	if err != nil {
		return nil, err
	}
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.LockJoinChats(ctx, tx); err != nil {
			return err
		}
		raw, err := s.st.LockSettings(ctx, tx, []string{"join.channel", "join.link", "join.label"})
		if err != nil {
			return err
		}
		before := map[string]string{}
		for k, r := range raw {
			var v string
			if json.Unmarshal(r, &v) == nil {
				before[k] = v
			}
		}
		oldRows, err := s.st.ListJoinChats(ctx, tx)
		if err != nil {
			return err
		}
		oldTotal := len(oldRows)
		if before["join.channel"] != "" {
			oldTotal++
		}
		if limit := joinLimit(eff); limit != license.Unlimited && len(chats) > limit && len(chats) > oldTotal {
			return eff.refuse(GateJoinChats, "allows "+plural(limit, "required chat")+" (channels and groups customers must join; the Pro plan has no limit)")
		}
		// The first row is the setting.
		var first JoinChat
		if len(chats) > 0 {
			first = chats[0]
		}
		for _, kv := range [][2]string{{"join.channel", first.Chat}, {"join.link", first.Link}, {"join.label", first.Label}} {
			if before[kv[0]] == kv[1] {
				continue
			}
			if err := s.writeSetting(ctx, tx, actor, kv[0], before[kv[0]], kv[1]); err != nil {
				return err
			}
		}
		var extras []JoinChat
		if len(chats) > 1 {
			extras = chats[1:]
		}
		return s.saveJoinExtras(ctx, tx, actor, joinChatsOf(oldRows), extras)
	})
	if err != nil {
		return nil, err
	}
	return s.JoinChats(ctx)
}

// saveJoinExtras writes the extra chats and audits what changed, chat by chat:
// join_chat.add, .remove, .update (its link or name) and .move (the order of
// the chats that stayed). Nothing is written when nothing changed.
func (s *Service) saveJoinExtras(ctx context.Context, tx pgx.Tx, actor *store.User, old, next []JoinChat) error {
	same := len(old) == len(next)
	for i := 0; same && i < len(old); i++ {
		same = old[i] == next[i]
	}
	if same {
		return nil
	}
	rows := make([]store.JoinChat, len(next))
	for i, c := range next {
		rows[i] = store.JoinChat{Chat: c.Chat, Link: c.Link, Label: c.Label}
	}
	if err := s.st.ReplaceJoinChats(ctx, tx, rows); err != nil {
		return err
	}
	type entry struct {
		JoinChat
		Pos int
	}
	index := func(list []JoinChat) map[string]entry {
		m := make(map[string]entry, len(list))
		for i, c := range list {
			m[strings.ToLower(c.Chat)] = entry{c, i + 1}
		}
		return m
	}
	was, now := index(old), index(next)
	audit := func(action string, chat string, before, after any) error {
		return s.auditChange(ctx, tx, actor, action, "join_chat", chat, before, after, "")
	}
	view := func(e entry) map[string]any {
		return map[string]any{"chat": e.Chat, "link": e.Link, "label": e.Label, "position": e.Pos}
	}
	for _, c := range old {
		if _, kept := now[strings.ToLower(c.Chat)]; !kept {
			if err := audit("join_chat.remove", c.Chat, view(was[strings.ToLower(c.Chat)]), nil); err != nil {
				return err
			}
		}
	}
	// The order of the chats that stayed, as it was and as it is.
	var oldOrder, newOrder []string
	for _, c := range old {
		if _, kept := now[strings.ToLower(c.Chat)]; kept {
			oldOrder = append(oldOrder, strings.ToLower(c.Chat))
		}
	}
	for _, c := range next {
		if _, kept := was[strings.ToLower(c.Chat)]; kept {
			newOrder = append(newOrder, strings.ToLower(c.Chat))
		}
	}
	for _, c := range next {
		k := strings.ToLower(c.Chat)
		w, existed := was[k]
		switch {
		case !existed:
			if err := audit("join_chat.add", c.Chat, nil, view(now[k])); err != nil {
				return err
			}
		case w.JoinChat != c:
			if err := audit("join_chat.update", c.Chat, view(w), view(now[k])); err != nil {
				return err
			}
		}
	}
	for i, k := range newOrder {
		if oldOrder[i] != k {
			if err := audit("join_chat.move", now[k].Chat, map[string]any{"position": was[k].Pos}, map[string]any{"position": now[k].Pos}); err != nil {
				return err
			}
		}
	}
	return nil
}
