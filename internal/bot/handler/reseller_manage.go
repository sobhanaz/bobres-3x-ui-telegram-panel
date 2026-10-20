package handler

import (
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// What a reseller does with the services they bought (docs/03-bot-ux.md):
// pause and resume them, delete them after a confirmation, keep a private note
// on each, and see the customers who joined through their link. Core keeps
// every rule (whose service it is, who may); the bot only asks and words the
// answer.

// statusText is a service's status in the user's words. A reseller's service
// that is turned off is "paused", the word of the button that does it.
func statusText(r *req, st string) string {
	if st == "disabled" && r.isReseller() {
		return r.t("rs.status_paused")
	}
	return r.t("sub.status." + st)
}

// canPause: a delivered service that is running (or ran out) can be paused.
func canPause(s *corev1.Subscription) bool {
	switch s.GetStatus() {
	case "active", "expiring_soon", "expired", "depleted":
		return s.GetSubscriptionLink() != ""
	}
	return false
}

// manageButtons are the reseller's buttons on a service's page: pause or
// resume, the note, and delete.
func manageButtons(r *req, s *corev1.Subscription) []tg.Button {
	id := s.GetId()
	var out []tg.Button
	switch {
	case s.GetStatus() == "disabled":
		out = append(out, r.button("btn.rs_resume", "rs:rz:"+id))
	case canPause(s):
		out = append(out, r.button("btn.rs_pause", "rs:pz:"+id))
	}
	return append(out, r.button("btn.rs_note", "rs:nt:"+id), r.button("btn.rs_delete", "rs:dl:"+id))
}

// manageFail words a refused management call as a popup and reports whether
// the service is gone (the caller then shows the list). Anything unexpected is
// told as any other failure.
func (h *Handler) manageFail(r *req, err error) (gone bool) {
	st := status.Convert(err)
	msg := strings.ToLower(st.Message())
	key := ""
	switch {
	case st.Code() == codes.NotFound:
		key, gone = "rs.service_gone", true
	case st.Code() == codes.PermissionDenied && strings.Contains(msg, "banned"):
		r.fail(err)
		return false
	case st.Code() == codes.PermissionDenied || st.Code() == codes.Unimplemented:
		// resellers are no longer in the license (nothing about why), or this core is older than the bot
		key = "rs.unavailable"
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "paused by support"):
		key = "rs.paused_by_support"
	case st.Code() == codes.FailedPrecondition:
		key = "rs.state_blocked"
	default:
		r.fail(err)
		return false
	}
	r.toast, r.alert = r.t(key), true
	return gone
}

// setPaused handles "rs:pz:<id>" (pause) and "rs:rz:<id>" (resume): the
// service is turned off or on in the panel, and its page is shown again.
func (h *Handler) setPaused(r *req, id string, pause bool) {
	_, err := h.core.SetResellerServiceEnabled(r.ctx, &corev1.SetResellerServiceEnabledRequest{UserId: r.user.GetId(),
		SubscriptionId: id, Enabled: !pause})
	if err != nil {
		if h.manageFail(r, err) {
			h.showServices(r)
		}
		return
	}
	r.toast = r.t("rs.resumed_done")
	if pause {
		r.toast = r.t("rs.paused_done")
	}
	h.showService(r, id)
}

// askDelete handles "rs:dl:<id>": nothing is deleted yet; the reseller is
// asked to confirm, and told what deleting means.
func (h *Handler) askDelete(r *req, id string) {
	s, ok := h.findSubscription(r, id)
	if !ok {
		return
	}
	kb := (&tg.Keyboard{}).Row(r.button("btn.rs_delete_yes", "rs:dy:"+s.GetId())).
		Row(r.button("btn.rs_delete_no", "sub:"+s.GetId()))
	r.show(r.labelled(s.GetLabel(), r.t("rs.delete_confirm", "id", shortID(s.GetId()))), kb)
}

// deleteService handles "rs:dy:<id>", the confirmed delete.
func (h *Handler) deleteService(r *req, id string) {
	if _, err := h.core.DeleteResellerService(r.ctx, &corev1.DeleteResellerServiceRequest{UserId: r.user.GetId(), SubscriptionId: id}); err != nil {
		if h.manageFail(r, err) {
			h.showServices(r)
		} else {
			h.showService(r, id)
		}
		return
	}
	r.toast = r.t("rs.deleted_done")
	h.showServices(r)
}

// cleanNote is a service's note as typed: runs of spaces and line breaks
// become one space. False when it is empty, longer than maxNote characters or
// holds other control characters (core refuses the last two).
func cleanNote(s string) (string, bool) {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || !utf8.ValidString(s) || strings.ContainsFunc(s, unicode.IsControl) || utf8.RuneCountInString(s) > maxNote {
		return "", false
	}
	return s, true
}

// askNote handles "rs:nt:<id>": the next message is the service's note (one
// with a note can also lose it).
func (h *Handler) askNote(r *req, id string) {
	s, ok := h.findSubscription(r, id)
	if !ok {
		return
	}
	r.setState(sceneResellerNote, map[string]string{"sub": s.GetId()})
	text := r.t("rs.note_ask", "id", shortID(s.GetId()), "max", i18n.Number(r.lang, maxNote))
	kb := &tg.Keyboard{}
	if s.GetNote() != "" {
		text += "\n\n" + r.t("sub.note_line", "note", esc(s.GetNote()))
		kb.Row(r.button("btn.rs_note_clear", "rs:nc:"+s.GetId()))
	}
	kb.Row(r.button("btn.cancel", "cancel"))
	r.show(r.labelled(s.GetLabel(), text), kb)
}

// setNote saves the note of one of the reseller's services ("" removes it)
// and shows the service again.
func (h *Handler) setNote(r *req, id, note string) {
	_, err := h.core.SetSubscriptionNote(r.ctx, &corev1.SetSubscriptionNoteRequest{UserId: r.user.GetId(), SubscriptionId: id, Note: note})
	switch status.Code(err) {
	case codes.OK:
	case codes.InvalidArgument: // a note core refuses: asked again
		r.send(r.t("rs.note_bad", "max", i18n.Number(r.lang, maxNote)), cancelKeyboard(r))
		return
	default:
		r.clearState()
		if h.manageFail(r, err) {
			h.showServices(r)
		}
		return
	}
	r.clearState()
	if note == "" {
		r.toast = r.t("rs.note_removed")
	}
	h.showService(r, id)
}

// customerWho names a customer who joined through the link: their @username,
// or their Telegram id when they have none.
func customerWho(r *req, c *corev1.InvitedCustomer) string {
	if c.GetUsername() != "" {
		return "@" + esc(c.GetUsername())
	}
	return r.t("rs.customer_id", "id", strconv.FormatInt(c.GetTelegramId(), 10))
}

// showInvitedCustomers handles "rs:cu:<page>": the people who joined through
// the reseller's invite link, newest first, with what they bought and what it
// paid the reseller. Nobody else and nothing else about them.
func (h *Handler) showInvitedCustomers(r *req, page int32) {
	resp, err := h.core.ListInvitedCustomers(r.ctx, &corev1.ListInvitedCustomersRequest{UserId: r.user.GetId(),
		Pagination: &commonv1.Pagination{Page: page, PageSize: customersPage}})
	if status.Code(err) == codes.NotFound { // not a reseller (any more)
		h.notReseller(r)
		return
	}
	if err != nil {
		h.manageFail(r, err)
		return
	}
	if len(resp.GetCustomers()) == 0 && page > 1 { // past the end: the first page
		h.showInvitedCustomers(r, 1)
		return
	}
	total := resp.GetPageInfo().GetTotal()
	if total == 0 {
		r.show(r.t("rs.customers_empty"), (&tg.Keyboard{}).Row(r.button("btn.rs_link", "rs:link")).
			Row(r.button("btn.back", "rs"), r.button("btn.home", "home")))
		return
	}
	pages := max((total+customersPage-1)/customersPage, 1)
	num := func(n int64) string { return i18n.Number(r.lang, n) }
	lines := make([]string, 0, len(resp.GetCustomers()))
	for _, c := range resp.GetCustomers() {
		lines = append(lines, r.t("rs.customer_line", "who", customerWho(r, c), "joined", i18n.Date(r.lang, time.Unix(c.GetJoinedAt(), 0)),
			"orders", num(c.GetPaidOrders()), "spent", earned(r, c.GetSpent()), "commission", earned(r, c.GetCommission())))
	}
	kb := (&tg.Keyboard{}).Row(pager(r, page, int64(page) < pages, func(p int32) string { return "rs:cu:" + strconv.Itoa(int(p)) })...).
		Row(r.button("btn.back", "rs"), r.button("btn.home", "home"))
	r.show(r.t("rs.customers_title", "total", num(total), "page", num(int64(page)), "pages", num(pages),
		"lines", strings.Join(lines, "\n\n")), kb)
}
