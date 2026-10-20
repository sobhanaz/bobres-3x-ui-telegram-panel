package handler

import (
	"strconv"
	"strings"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Growth tools (docs/03-bot-ux.md): the Top users board of the customers who
// invited the most people, the invite promo card a customer can forward, the
// account lines on the wallet screen, the /new, /renew and /status commands
// and the reseller application. Core keeps every rule (who is ranked, what a
// name shows, who may apply); the bot words the answers.

// sceneResellerApply is the question after "Become a reseller": the next
// message is the application.
const sceneResellerApply = "rs_apply"

// applicationOpenMsg and conversationOpenMsg are what core's refusals of a
// reseller application say (domain.ApplicationOpen, domain.ConversationOpen):
// the bot cannot import core's domain, and reads them as the other states.
const (
	applicationOpenMsg  = "reseller application is open already"
	conversationOpenMsg = "another support conversation is open"
)

// lri and pdi isolate a run of text that reads left to right (a masked
// username or id) inside a Persian line, so its stars and the brackets round it
// stay where they are written whatever the language around it.
const (
	lri = "\u2066"
	pdi = "\u2069"
)

func isolated(s string) string { return lri + s + pdi }

// --- Top users ---

// topPageSize is the places the board shows on a page.
const topPageSize = 5

// topUsersOn: the Top users button is offered: the referral program is on and
// the owner has not switched the board off (growth.leaderboard.enabled, on by
// default). Core answers the same way to a tap that comes later.
func (h *Handler) topUsersOn() bool {
	return h.referralsOn() && h.set.Bool("growth.leaderboard.enabled", true)
}

// showTopUsers handles "top[:<page>]": a page of the board of the customers
// who invited the most people, five places a page, with each name and Telegram
// id masked by core. A button shows the viewer's own invites (and opens their
// invite screen), another pages on to the next places.
func (h *Handler) showTopUsers(r *req, page int32) {
	if !h.topUsersOn() {
		h.topUsersOff(r)
		return
	}
	res, err := h.core.GetTopInviters(r.ctx, &corev1.GetTopInvitersRequest{UserId: r.user.GetId(), Page: page, PageSize: topPageSize})
	if status.Code(err) == codes.Unimplemented { // a core older than this bot
		h.topUsersOff(r)
		return
	}
	if err != nil {
		r.fail(err)
		return
	}
	if !res.GetEnabled() { // switched off since the bot last read the settings
		h.topUsersOff(r)
		return
	}
	kb := (&tg.Keyboard{}).Row(tg.CB(r.t("ref.top_mine", "n", i18n.Number(r.lang, int64(res.GetYourInvited()))), "ref"))
	if res.GetPage() == 1 {
		if res.GetMore() {
			kb.Row(r.button("btn.top_others", "top:2"))
		}
	} else {
		kb.Row(pager(r, res.GetPage(), res.GetMore(), func(p int32) string { return "top:" + strconv.Itoa(int(p)) })...)
	}
	kb.Row(r.button("btn.home", "home"))
	if len(res.GetEntries()) == 0 {
		r.show(r.t("ref.top_empty"), kb)
		return
	}
	entries := make([]string, 0, len(res.GetEntries()))
	for _, e := range res.GetEntries() {
		you := ""
		if e.GetYou() {
			you = r.t("ref.top_you")
		}
		entries = append(entries, r.t("ref.top_entry", "rank", i18n.Number(r.lang, int64(e.GetRank())),
			"name", isolated(esc(e.GetName())), "id", isolated(esc(e.GetTelegramId())), "you", you,
			"count", i18n.Number(r.lang, int64(e.GetInvited()))))
	}
	places := ""
	if res.GetPage() > 1 { // which places the page shows
		first := int64(res.GetEntries()[0].GetRank())
		places = r.t("ref.top_range", "from", i18n.Number(r.lang, first), "to", i18n.Number(r.lang, first+int64(len(entries))-1))
	}
	percent, _ := i18n.ParseNumber(h.setting("referral.reward_percent"))
	r.show(r.t("ref.top_screen", "range", places, "entries", strings.Join(entries, "\n\n"),
		"percent", i18n.Number(r.lang, percent)), kb)
}

// topUsersOff answers a tap on a board that is off: a popup, and the home
// screen again (without the button, once the settings say so).
func (h *Handler) topUsersOff(r *req) {
	r.toast, r.alert = r.t("ref.top_off"), true
	h.showHome(r)
}

// --- The invite promo card ---

// sendPromoCard sends the customer's invite as a card to forward, apart from
// the invite screen: the store's promo text ending in their own invite link as
// a text link, then a line that tells them to forward the message above. The
// card is a message of its own so that forwarding it shows a friend only the
// promotion, never the screen's buttons or the instructions.
func (h *Handler) sendPromoCard(r *req, link string) {
	anchor := `<a href="` + esc(link) + `">` + r.t("ref.promo_link") + `</a>`
	r.send(r.t("ref.promo", "brand", esc(h.brand()), "link", anchor), nil)
	r.send(r.t("ref.promo_hint"), nil)
}

// --- The account lines on the wallet screen ---

// accountInfo is the lines about the customer's account above the balance:
// their numeric Telegram id, how many services they have and the day they
// joined the bot (Jalali for Persian, Gregorian for English). A figure that
// cannot be read shows a dash, rather than costing the customer their wallet.
func (h *Handler) accountInfo(r *req) string {
	services := "—"
	if resp, err := h.core.ListSubscriptions(r.ctx, &corev1.ListSubscriptionsRequest{UserId: r.user.GetId(),
		Pagination: &commonv1.Pagination{Page: 1, PageSize: 1}}); err == nil {
		services = i18n.Number(r.lang, resp.GetPageInfo().GetTotal())
	} else {
		h.log.Debug("account lines: the number of services was not read", "err", err)
	}
	joined := "—"
	if at := r.user.GetCreatedAt(); at > 0 {
		joined = i18n.Date(r.lang, time.Unix(at, 0))
	}
	return r.t("wallet.account", "id", r.user.GetTelegramId(), "services", services, "joined", joined)
}

// --- /new, /renew and /status ---

// onActionCommand runs a command that does what a button does (/new is Buy,
// /renew is Renew, /status is My services) by the same dispatch, so it has the
// same role checks, and the gates in front of it are the buttons' gates (join
// channel, maintenance: Handle runs them for every update). Someone who has
// not picked a language yet picks it first. An open question is closed, as a
// tap on the bottom menu closes it: a command is never the answer to one.
func (h *Handler) onActionCommand(r *req, data string) {
	if r.user == nil {
		h.askLanguage(r)
		return
	}
	r.clearState()
	h.dispatch(r, data)
}

// showRenewServices handles "renew" (the /renew command): the services that can
// be renewed, each a button that opens its plans to renew with. A reseller
// picks from their own list of services, as everywhere, and renews from the
// service's page.
func (h *Handler) showRenewServices(r *req) {
	if r.isReseller() {
		h.showCustomerServices(r, 1, "")
		return
	}
	subs, err := h.subscriptions(r)
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	n := 0
	for _, s := range subs {
		if !renewable(s) {
			continue
		}
		if n == 20 { // as the list of services
			break
		}
		kb.Row(tg.CB(h.serviceButton(r, s), "rnw:"+s.GetId()))
		n++
	}
	if n == 0 {
		r.show(r.t("renew.none"), (&tg.Keyboard{}).Row(r.button("btn.buy", "buy")).Row(r.button("btn.home", "home")))
		return
	}
	kb.Row(r.button("btn.home", "home"))
	r.show(r.t("renew.list"), kb)
}

// --- The reseller application ---

// canApply: the user is a customer who could become a reseller: active, and
// neither staff nor a reseller already.
func (r *req) canApply() bool {
	return r.user != nil && r.user.GetStatus() == "active" && r.user.GetRole() == "user"
}

// resellerApplyOn: the Become a reseller button is offered to this user while
// the license in effect includes resellers (core says so with every settings
// refresh; a customer is never told about the license).
func (h *Handler) resellerApplyOn(r *req) bool {
	return h.resellersIncluded.Load() && r.canApply()
}

// growthButtons are the home screen's Become a reseller and Top users buttons,
// each only while it is on for this user.
func (h *Handler) growthButtons(r *req) []tg.Button {
	var out []tg.Button
	if h.resellerApplyOn(r) {
		out = append(out, r.button("btn.reseller_apply", "ap"))
	}
	if h.topUsersOn() {
		out = append(out, r.button("btn.top_users", "top"))
	}
	return out
}

// onResellerApply handles "ap": it asks for one message about the applicant,
// unless the customer has an open ticket (they have one at a time) or cannot
// apply, then says why.
func (h *Handler) onResellerApply(r *req) {
	switch {
	case r.isReseller():
		r.toast, r.alert = r.t("rs.apply_already"), true
		h.showHome(r)
		return
	case !h.resellerApplyOn(r):
		r.toast, r.alert = r.t("rs.unavailable"), true
		h.showHome(r)
		return
	}
	mine, err := h.core.GetMyTickets(r.ctx, &corev1.GetMyTicketsRequest{UserId: r.user.GetId(), Limit: 1})
	if err != nil {
		r.fail(err)
		return
	}
	if t := mine.GetOpen(); t != nil {
		key := "rs.apply_busy"
		if t.GetCategory() == "reseller" { // this customer applied already
			key = "rs.apply_exists"
		}
		kb := (&tg.Keyboard{}).Row(r.button("btn.support", "support")).Row(r.button("btn.home", "home"))
		r.show(r.t(key, "id", t.GetNumber()), kb)
		return
	}
	r.setState(sceneResellerApply, nil)
	r.show(r.t("rs.apply_ask"), cancelKeyboard(r))
}

// onResellerApplication handles the message after "Become a reseller": the
// application, filed as a ticket in the reseller category. Staff are alerted
// through the ticket alert (core's ticket.opened event), with the applicant
// and a button that opens the ticket in the dashboard. It counts against the
// daily limit of support messages, as any message to support does.
func (h *Handler) onResellerApplication(r *req) {
	if !h.resellerApplyOn(r) { // a reseller, banned or out of the license since the question was asked
		r.clearState()
		if r.isReseller() {
			r.send(r.t("rs.apply_already"), homeKeyboard(r))
		} else {
			r.send(r.t("rs.unavailable"), homeKeyboard(r))
		}
		return
	}
	text := strings.TrimSpace(r.msg.Text)
	if text == "" { // a photo, a voice message, a sticker: the application is a text
		r.send(r.t("rs.apply_need_text"), cancelKeyboard(r))
		return
	}
	if !h.allowTicket(r) {
		r.send(r.t("support.limit"), homeKeyboard(r))
		return
	}
	resp, err := h.core.AddTicketMessage(r.ctx, &corev1.AddTicketMessageRequest{UserId: r.user.GetId(),
		Category: "reseller", Text: text, TgMessageId: int64(r.msg.MessageID)})
	if err != nil {
		h.giveTicket(r) // nothing was recorded: it does not count
		h.applicationRefused(r, err)
		return
	}
	r.clearState()
	if !resp.GetCreated() {
		return // this Telegram message was recorded before
	}
	r.send(r.t("rs.apply_sent", "id", resp.GetTicket().GetNumber()), homeKeyboard(r))
}

// applicationRefused words core's refusal of an application: another open
// ticket (or an application already waiting) is shown as the button shows it;
// anything about the license or the customer's role is only "not available".
func (h *Handler) applicationRefused(r *req, err error) {
	st := status.Convert(err)
	msg := strings.ToLower(st.Message())
	switch {
	case st.Code() == codes.FailedPrecondition && (strings.Contains(msg, applicationOpenMsg) || strings.Contains(msg, conversationOpenMsg)):
		r.clearState()
		h.onResellerApply(r) // says which ticket is open
	case st.Code() == codes.PermissionDenied && strings.Contains(msg, "banned"):
		r.clearState()
		r.fail(err)
	case st.Code() == codes.FailedPrecondition || st.Code() == codes.PermissionDenied:
		r.clearState()
		r.send(r.t("rs.unavailable"), homeKeyboard(r))
	case st.Code() == codes.InvalidArgument: // too long, or empty after cleaning
		r.send(r.t("rs.apply_need_text"), cancelKeyboard(r))
	default:
		r.fail(err)
	}
}
