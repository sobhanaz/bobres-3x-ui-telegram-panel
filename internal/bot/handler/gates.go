package handler

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
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
// continuesTicket). There may be several required channels and groups (the
// setting join.channel and the extras core sends with the settings): a customer
// is held until they are in all of them, and the prompt lists the ones they
// lack. How they are looked up and cached is in joinchats.go.

// gate answers an update that maintenance mode or the forced join keeps from
// the menus, and reports whether it did.
func (h *Handler) gate(r *req) bool {
	maintenance := h.set.Bool("maintenance.enabled", false)
	joinOn := h.set.Get("join.channel") != ""
	// Staff and the configured owner (who may not have a user yet) pass both.
	if (!maintenance && !joinOn) || r.isTeam() || h.isOwnerID(r) {
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
	missing := h.missingChats(r.ctx, r.from.ID, false)
	if len(missing) == 0 {
		r.held = false
		return false
	}
	if h.payingNow(r) {
		return false
	}
	if h.continuesTicket(r) {
		r.gated = true // what turns out to start a ticket gets the answer below
		r.joinMissing = missing
		return false
	}
	h.askToJoin(r, missing)
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
	missing := r.joinMissing
	if len(missing) == 0 {
		// The gate did not say (or the user has joined since): the chats still missing now, else all.
		if missing = h.missingChats(r.ctx, r.from.ID, false); len(missing) == 0 {
			missing = h.joinList()
		}
	}
	h.askToJoin(r, missing)
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

// httpsURL: a link Telegram accepts on a button and the store meant (https
// with a host; core checks the settings the same way).
func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return s != "" && err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}
