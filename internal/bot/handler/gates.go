package handler

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// Gates (milestone 3): maintenance mode (maintenance.enabled) and joining a
// channel first (join.channel). Handle checks them after the rate limit and
// before routing; Stars pre-checkouts and successful payments are served
// before that and are never gated.

// joinCacheTTL is how long a confirmed channel member is not asked again.
const joinCacheTTL = 10 * time.Minute

// gate answers an update that maintenance mode or the forced join keeps from
// the menus, and reports whether it did.
func (h *Handler) gate(r *req) bool {
	maintenance := h.set.Bool("maintenance.enabled", false)
	channel := h.set.Get("join.channel")
	if (!maintenance && channel == "") || h.exempt(r) {
		return false
	}
	if !maintenance {
		if r.cb != nil && r.cb.Data == "jn" {
			return false // "I've joined" checks by itself
		}
		if h.inChannel(r.ctx, r.from.ID, channel) {
			return false
		}
	}
	if h.payingNow(r) {
		return false
	}
	if maintenance {
		h.sayMaintenance(r)
	} else {
		h.askToJoin(r, channel)
	}
	return true
}

// exempt: what both gates let through. Staff and the configured owner (who
// may not have a user yet); /paysupport, which Telegram requires of bots
// selling Stars; payment checks; the language picker; and a new user's
// /start, which keeps an invite code until the language is picked.
func (h *Handler) exempt(r *req) bool {
	if r.isTeam() || h.isOwnerID(r) {
		return true
	}
	if r.cb != nil {
		verb, _, _ := strings.Cut(r.cb.Data, ":")
		return verb == "chk" || verb == "lang"
	}
	switch cmd, _, _ := command(r.msg.Text); cmd {
	case "/paysupport":
		return true
	case "/start":
		return r.user == nil
	}
	return false
}

func (h *Handler) isOwnerID(r *req) bool {
	return h.cfg.AdminTelegramID != 0 && r.from.ID == h.cfg.AdminTelegramID
}

// payingNow: a message carrying the proof of a payment already under way (a
// receipt, its bank reference or a TXID), which may still finish. The state
// is read only when a gate would otherwise block.
func (h *Handler) payingNow(r *req) bool {
	if r.cb != nil || r.user == nil {
		return false
	}
	if _, _, isCmd := command(r.msg.Text); isCmd {
		return false
	}
	st, err := h.state.Get(r.ctx, r.from.ID)
	if err != nil {
		return false
	}
	switch st.Scene {
	case sceneReceipt, sceneReference, sceneTXID:
		return true
	}
	return false
}

// sayMaintenance: an alert for a tap; for messages, the text at most once a
// minute, so a customer who keeps writing is not answered every time.
func (h *Handler) sayMaintenance(r *req) {
	if r.cb != nil {
		r.toast, r.alert = r.t("maintenance"), true
		return
	}
	if first, err := h.state.Once(r.ctx, "mnt:"+strconv.FormatInt(r.from.ID, 10), time.Minute); err == nil && !first {
		return
	}
	r.send(r.t("maintenance"), nil)
}

// askToJoin sends the channel's join button and "I've joined" (jn). Always
// a new message: the button pressed may sit under a notification (a
// delivered service and its link), which must stay as it is.
func (h *Handler) askToJoin(r *req, channel string) {
	kb := &tg.Keyboard{}
	if link := h.joinLink(channel); link != "" {
		kb.Row(tg.Link(r.t("btn.join_channel"), link))
	}
	kb.Row(tg.CB(r.t("btn.join_check"), "jn"))
	r.send(r.t("join.prompt"), kb)
}

// joinLink is where the join button leads: join.link, else the public
// channel's t.me page ("" for a private channel without a link).
func (h *Handler) joinLink(channel string) string {
	if l := h.set.Get("join.link"); httpsURL(l) {
		return l
	}
	if name, ok := strings.CutPrefix(channel, "@"); ok && name != "" {
		return "https://t.me/" + name
	}
	return ""
}

// onJoinCheck handles "jn": the user says they joined the channel.
func (h *Handler) onJoinCheck(r *req) {
	if ch := h.set.Get("join.channel"); ch != "" && !r.isTeam() && !h.isOwnerID(r) && !h.inChannel(r.ctx, r.from.ID, ch) {
		r.toast, r.alert = r.t("join.not_yet"), true
		return
	}
	h.showHome(r) // the language picker for a user not registered yet
}

// gateNewUser runs the gates for a user who just picked a language (the
// picker itself is not gated): instead of the first home menu, the
// maintenance message or the join prompt. It reports whether it answered.
func (h *Handler) gateNewUser(r *req) bool {
	if r.isTeam() || h.isOwnerID(r) {
		return false
	}
	if h.set.Bool("maintenance.enabled", false) {
		r.send(r.t("maintenance"), nil)
		return true
	}
	if ch := h.set.Get("join.channel"); ch != "" && !h.inChannel(r.ctx, r.from.ID, ch) {
		h.askToJoin(r, ch)
		return true
	}
	return false
}

// inChannel reports whether the user is a member of the channel. Members are
// remembered for a while; non-members are not, so "I've joined" checks again
// at once. When Telegram cannot tell (the bot is not the channel's admin, a
// timeout), the user is let in: a misconfigured channel must not lock every
// customer out.
func (h *Handler) inChannel(ctx context.Context, userID int64, channel string) bool {
	key := "bot:join:" + strconv.FormatInt(userID, 10)
	if ok, err := h.state.Recall(ctx, key); err == nil && ok {
		return true
	}
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	m, err := h.tg.GetChatMember(checkCtx, channel, userID)
	cancel()
	if err != nil {
		// Once a minute at Warn: every customer's update would log it.
		if first, onceErr := h.state.Once(ctx, "join-check-failed", time.Minute); onceErr != nil || first {
			h.log.Warn("cannot check members of the join channel; letting users in (is the bot its admin?)",
				"channel", channel, "err", err)
		}
		return true
	}
	if !m.Joined() {
		return false
	}
	if err := h.state.Remember(ctx, key, joinCacheTTL); err != nil {
		h.log.Debug("join cache not saved", "err", err)
	}
	return true
}

// httpsURL: a link Telegram accepts on a button and the store meant (https
// with a host; core checks the settings the same way).
func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return s != "" && err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
