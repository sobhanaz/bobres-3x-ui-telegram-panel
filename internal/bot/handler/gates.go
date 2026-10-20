package handler

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// Gates (milestone 3): maintenance mode (maintenance.enabled) and joining a
// channel first (join.channel). Handle runs them once for every update, after
// the rate limit and before anything reads what the update means, so that no
// command, button, bottom-menu tap, scene or conversation ever sees an update a
// gate holds back. A Stars pre-checkout and a successful payment are served
// before that and are never held. The settings are read as the last reload left
// them, so switching a gate on or off takes hold when the bot reloads (core
// asks it to at once, and it does so by itself every minute), never at a restart.
//
// Maintenance mode holds every update but these (maintenancePasses is the
// whole list; TestMaintenanceAllowlist and TestMaintenanceAnswersEveryCustomerUpdate
// test both sides):
//   - staff (owner, admin or support) and the owners the server names, who may
//     not have an account yet;
//   - /paysupport, which Telegram requires of a bot that sells with Stars;
//   - a payment already under way: its proof (the receipt, the bank reference or
//     the TXID, as a message sent while the bot waits for it) and its check
//     (the chk button);
//   - a newcomer's /start and language choice, which make the account and keep
//     an invite code; they are then told about the maintenance like anyone.
//
// Everything else gets the maintenance text, whatever it was and whatever the
// customer was doing: a popup for a button, a message for anything the customer
// typed or sent, every time (an album, which Telegram delivers one part at a
// time, once). That includes the bottom menu, Support and the answers to a
// ticket. Maintenance comes first: while it is on, nobody is asked to join the
// channel.
//
// The required channel holds the same way, and lets a few more through (the
// language picker, and a customer going on with a ticket; see exempt and
// continuesTicket).

// joinCacheTTL is how long a confirmed channel member is not asked again.
const joinCacheTTL = 10 * time.Minute

// gate answers an update that maintenance mode or the forced join keeps from
// the menus, and reports whether it did.
func (h *Handler) gate(r *req) bool {
	maintenance := h.set.Bool("maintenance.enabled", false)
	channel := h.set.Get("join.channel")
	// Staff and the configured owner (who may not have a user yet) pass both.
	if (!maintenance && channel == "") || r.isTeam() || h.isOwnerID(r) {
		return false
	}
	r.held = true // what passes below passes for what it is, unless a member
	if maintenance {
		if h.maintenancePasses(r) {
			return false
		}
		h.sayMaintenance(r)
		return true
	}
	if h.exempt(r) {
		return false
	}
	if r.cb != nil && r.cb.Data == "jn" {
		return false // "I've joined" checks by itself
	}
	if h.inChannel(r.ctx, r.from.ID, channel) {
		r.held = false
		return false
	}
	if h.payingNow(r) {
		return false
	}
	if h.continuesTicket(r) {
		r.gated = true // what turns out to start a ticket gets the answer below
		return false
	}
	h.askToJoin(r, channel)
	return true
}

// maintenancePasses is what maintenance mode lets a customer do, besides what
// staff and the owners the server names do (gate lets them through before):
// nothing but this. A newcomer, who has no account yet, is one without a user.
func (h *Handler) maintenancePasses(r *req) bool {
	if r.cb != nil {
		verb, _, _ := strings.Cut(r.cb.Data, ":")
		switch verb {
		case "chk": // the check of a payment under way
			return r.user != nil
		case "lang": // a newcomer picks a language: the account is made
			return r.user == nil
		}
		return false
	}
	cmd, _, isCmd := command(r.msg.Text)
	switch {
	case !isCmd:
		return h.payingNow(r) // the proof of a payment under way
	case cmd == "/paysupport":
		return true
	}
	return cmd == "/start" && r.user == nil // a newcomer starts; the invite code is kept
}

// answerGate is the required channel's answer to an update let through to
// continue a ticket that turned out not to (r.gated). (Maintenance, which lets
// no ticket through, has answered already; it is checked again only because it
// may have come on since.)
func (h *Handler) answerGate(r *req) {
	if h.set.Bool("maintenance.enabled", false) {
		h.sayMaintenance(r)
		return
	}
	h.askToJoin(r, h.set.Get("join.channel"))
}

// continuesTicket: a customer going on with a ticket, which the required
// channel lets through (starting one it does not; maintenance lets neither):
// the buttons to answer, close, list or read tickets, a message in the ticket
// scene that names a ticket (Support is the only bottom-menu tap that counts),
// or a Telegram reply to a message of the bot (a staff answer, core checks).
// The state is read only when the gate would otherwise block.
func (h *Handler) continuesTicket(r *req) bool {
	if r.user == nil {
		return false
	}
	if r.cb != nil {
		verb, rest, _ := strings.Cut(r.cb.Data, ":")
		sub, _, _ := strings.Cut(rest, ":")
		return verb == "tk" && (sub == "r" || sub == "c" || sub == "l" || sub == "v")
	}
	if _, _, isCmd := command(r.msg.Text); isCmd {
		return false
	}
	if data, tap := h.menuTap(r); tap && data != "support" {
		return false
	}
	st, err := h.state.Get(r.ctx, r.from.ID)
	if err != nil {
		return false
	}
	switch st.Scene {
	case sceneTicket:
		return st.Get("tid") != ""
	case "":
		return replyToBot(r.msg)
	}
	return false
}

// exempt: the updates the required channel lets through before asking about
// the channel: /paysupport, which Telegram requires of bots selling Stars;
// payment checks; the language picker; and a new user's /start, which keeps an
// invite code until the language is picked. (Maintenance mode has a list of its
// own, shorter: maintenancePasses.)
func (h *Handler) exempt(r *req) bool {
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
	return r.from.ID != 0 && slices.Contains(h.cfg.AdminTelegramIDs, r.from.ID)
}

// payingNow: a message carrying the proof of a payment already under way (a
// receipt, its bank reference or a TXID), which may still finish. A tap on
// the bottom menu is never one. The state is read only when a gate would
// otherwise block.
func (h *Handler) payingNow(r *req) bool {
	if r.cb != nil || r.user == nil {
		return false
	}
	if _, _, isCmd := command(r.msg.Text); isCmd {
		return false
	}
	if _, tap := h.menuTap(r); tap {
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

// sayMaintenance answers a held update with the maintenance text (the owner's
// own, when they edited it): an alert for a tap, a message for anything the
// customer typed or sent. Every update gets it, so that a tap is never
// swallowed because another was answered a moment ago (a flood is the rate
// limit's business, before the gates). The parts of an album, which Telegram
// delivers as one update each, are answered once.
func (h *Handler) sayMaintenance(r *req) {
	if r.cb != nil {
		r.toast, r.alert = r.t("maintenance"), true
		return
	}
	if album := r.msg.MediaGroupID; album != "" {
		key := "mnt:" + strconv.FormatInt(r.from.ID, 10) + ":" + album
		if first, err := h.state.Once(r.ctx, key, time.Minute); err == nil && !first {
			return
		}
	}
	r.send(r.t("maintenance"), nil)
}

// askToJoin sends the channel's join button and "I've joined" (jn). Always
// a new message: the button pressed may sit under a notification (a
// delivered service and its link), which must stay as it is.
func (h *Handler) askToJoin(r *req, channel string) {
	kb := &tg.Keyboard{}
	if link := h.joinLink(channel); link != "" {
		kb.Row(r.linkButton("btn.join_channel", link))
	}
	kb.Row(r.button("btn.join_check", "jn"))
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
// picker itself is not gated): the picker becomes the welcome, without the
// bottom menu, and the maintenance message or the join prompt comes instead
// of the first home menu. It reports whether it answered.
func (h *Handler) gateNewUser(r *req) bool {
	if r.isTeam() || h.isOwnerID(r) {
		return false
	}
	maintenance := h.set.Bool("maintenance.enabled", false)
	channel := h.set.Get("join.channel")
	if !maintenance && (channel == "" || h.inChannel(r.ctx, r.from.ID, channel)) {
		return false
	}
	r.show(r.t("welcome", "brand", esc(h.brand())), nil)
	if maintenance {
		r.send(r.t("maintenance"), nil)
	} else {
		h.askToJoin(r, channel)
	}
	return true
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
