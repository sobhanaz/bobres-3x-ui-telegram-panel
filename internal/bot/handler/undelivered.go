package handler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// Undelivered orders (docs/03-bot-ux.md, "Undelivered orders"): paid orders whose service could not be made or
// applied yet because a server did not answer (core's status provision_failed). Core tries each again on its own,
// at most every 30 minutes; owners and admins see them here, the longest waiting first, and try one or all of them
// again now, or refund one to the buyer's wallet. The callbacks, all under adm:
//
//	ud:<page>          a page of the list, in place (the admin menu, paging, Back); ud alone is the first page
//	udr:<page>:<id>    try one again now
//	uda:<page>         try every one again now
//	udf:<page>:<id>    ask to refund one
//	udy:<page>:<id>    the Refund of that question
//
// Core checks the role and the order again on every call: an order that is not waiting any more (delivered,
// refunded, being tried right now) is said so and nothing is done, and a stale or forged button does nothing but
// show the list.

const (
	// udPage is how many orders one page of the list shows.
	udPage = 5
	// udFetch is how many orders one call lists when the bot looks for one.
	udFetch = 200
	// udErrorShown is the most characters of an order's last error the list shows.
	udErrorShown = 120
)

// routeUndelivered serves the ud* verbs of the admin menu (routeAdmin checked the role).
func (h *Handler) routeUndelivered(r *req, verb, arg string) {
	switch verb {
	case "ud":
		page, _ := strconv.Atoi(arg)
		h.showUndelivered(r, max(page, 0), "")
	case "uda":
		page, _ := strconv.Atoi(arg)
		h.retryUndelivered(r, max(page, 0), "")
	case "udr", "udf", "udy":
		page, id, ok := pmPageID(arg)
		if !ok {
			h.showUndelivered(r, page, r.t("admin.ud_stale"))
			return
		}
		switch verb {
		case "udr":
			h.retryUndelivered(r, page, id)
		case "udf":
			h.askRefundUndelivered(r, page, id)
		default:
			h.refundUndelivered(r, page, id)
		}
	}
}

// listUndelivered is one call for a page of the list.
func (h *Handler) listUndelivered(r *req, offset, limit int) (*corev1.AdminListUndeliveredResponse, error) {
	return h.core.AdminListUndelivered(r.ctx, &corev1.AdminListUndeliveredRequest{
		ActorTelegramId: h.actor(r), Offset: int32(min(offset, 1<<30)), Limit: int32(limit), //nolint:gosec // bounded
	})
}

// showUndelivered shows a page of the undelivered orders (from 0; clamped) in place, with note above it (the
// outcome of what was just done, or why nothing was).
func (h *Handler) showUndelivered(r *req, page int, note string) {
	page = min(max(page, 0), 1<<24) // a forged page number must not overflow the offset
	resp, err := h.listUndelivered(r, page*udPage, udPage)
	if err == nil && page > 0 && len(resp.GetOrders()) == 0 {
		// Past the end (core counts the orders with the ones it returns, so it said none): the last page.
		if resp, err = h.listUndelivered(r, 0, 1); err == nil {
			page = max(int((min(resp.GetTotal(), 1<<30)+udPage-1)/udPage)-1, 0)
			resp, err = h.listUndelivered(r, page*udPage, udPage)
		}
	}
	if err != nil {
		if !pmDenied(r, err) {
			r.fail(err)
		}
		return
	}
	total := int(min(resp.GetTotal(), 1<<30))
	pages := max((total+udPage-1)/udPage, 1)
	head := ""
	if note != "" {
		head = note + "\n\n"
	}
	list := resp.GetOrders()
	if total == 0 || len(list) == 0 {
		r.show(head+r.t("admin.ud_none"), (&tg.Keyboard{}).Row(r.button("btn.back", "adm")))
		return
	}
	p := strconv.Itoa(page)
	kb := &tg.Keyboard{}
	lines := make([]string, 0, len(list))
	for i, o := range list {
		n := i18n.Number(r.lang, int64(page*udPage+i+1))
		line := r.t("admin.ud_item", append([]any{"n", n}, udArgs(r, o)...)...)
		if e := strings.TrimSpace(o.GetLastError()); e != "" {
			line += "\n" + r.t("admin.ud_error", "error", esc(udShorten(e, udErrorShown)))
		}
		lines = append(lines, line)
		kb.Row(r.button("btn.ud_retry", "adm:udr:"+p+":"+o.GetId(), "n", n),
			r.button("btn.ud_refund", "adm:udf:"+p+":"+o.GetId(), "n", n))
	}
	paging := ""
	if pages > 1 {
		var nav []tg.Button
		if page > 0 {
			nav = append(nav, r.button("btn.prev", "adm:ud:"+strconv.Itoa(page-1)))
		}
		if page < pages-1 {
			nav = append(nav, r.button("btn.next", "adm:ud:"+strconv.Itoa(page+1)))
		}
		kb.Row(nav...)
		paging = r.t("admin.pending_page", "page", i18n.Number(r.lang, int64(page+1)), "pages", i18n.Number(r.lang, int64(pages)))
	}
	kb.Row(r.button("btn.ud_retry_all", "adm:uda:"+p))
	kb.Row(r.button("btn.back", "adm"))
	r.show(head+r.t("admin.ud_list", "n", i18n.Number(r.lang, int64(total)), "paging", paging, "items", strings.Join(lines, "\n\n")), kb)
}

// udArgs are the values an order's summary names: the plan in the reader's language and what was bought, the
// amount, who bought it (and the reseller behind it), how long it has waited and how often it was tried.
func udArgs(r *req, o *corev1.AdminUndeliveredOrder) []any {
	kind := ""
	switch o.GetType() {
	case "renew":
		kind = " · " + r.t("admin.ud_kind_renew")
	case "traffic_topup":
		kind = " · " + r.t("admin.ud_kind_topup")
	}
	return []any{"plan", esc(pmPlanName(r, o.GetPlanName())), "kind", kind, "amount", r.money(o.GetAmount(), o.GetCurrency()),
		"who", udWho(r, o), "waited", udWaited(r, time.Since(time.Unix(o.GetCreatedAt(), 0))),
		"attempts", i18n.Number(r.lang, int64(o.GetAttempts()))}
}

// udWho is who bought an order, as staff know them: the buyer; a reseller buying for a customer of theirs; or a
// customer a reseller brought (the reseller is paid a commission on it). A reseller's name for the service follows.
func udWho(r *req, o *corev1.AdminUndeliveredOrder) string {
	buyer := esc(udUser(o.GetUsername(), o.GetTelegramId()))
	var who string
	switch o.GetSellerKind() {
	case "reseller":
		who = r.t("admin.ud_who_reseller", "buyer", buyer)
	case "referrer":
		who = r.t("admin.ud_who_referred", "buyer", buyer, "seller", esc(udUser(o.GetSellerUsername(), o.GetSellerTelegramId())))
	default:
		who = r.t("admin.ud_who", "buyer", buyer)
	}
	if l := strings.TrimSpace(o.GetLabel()); l != "" {
		who += " · 🏷 " + esc(l)
	}
	return who
}

// udUser is a user as staff know them: @username, else the Telegram id.
func udUser(username string, telegramID int64) string {
	if username != "" {
		return "@" + username
	}
	return fmt.Sprintf("id %d", telegramID)
}

// udWaited names how long an order has waited: minutes under an hour, hours under two days, days from there (as
// the staff alerts name how long a problem lasted).
func udWaited(r *req, d time.Duration) string {
	switch {
	case d < time.Hour:
		return r.t("alert.duration_minutes", "n", i18n.Number(r.lang, max(int64(d/time.Minute), 1)))
	case d < 48*time.Hour:
		return r.t("alert.duration_hours", "n", i18n.Number(r.lang, int64(d/time.Hour)))
	}
	return r.t("alert.duration_days", "n", i18n.Number(r.lang, int64(d/(24*time.Hour))))
}

// udShorten cuts s to n characters (never splitting one), marking the cut.
func udShorten(s string, n int) string {
	if c := []rune(s); len(c) > n {
		return string(c[:n]) + "…"
	}
	return s
}

// findUndelivered looks an order up in the list (nil: not there, so not waiting any more).
func (h *Handler) findUndelivered(r *req, id string) (*corev1.AdminUndeliveredOrder, error) {
	for offset := 0; ; offset += udFetch {
		resp, err := h.listUndelivered(r, offset, udFetch)
		if err != nil {
			return nil, err
		}
		for _, o := range resp.GetOrders() {
			if o.GetId() == id {
				return o, nil
			}
		}
		if len(resp.GetOrders()) < udFetch || int64(offset+udFetch) >= resp.GetTotal() {
			return nil, nil
		}
	}
}

// retryUndelivered tries one order (id) or every one ("") again now, says so in a popup and shows the list again.
func (h *Handler) retryUndelivered(r *req, page int, id string) {
	resp, err := h.core.AdminRetryUndelivered(r.ctx, &corev1.AdminRetryUndeliveredRequest{ActorTelegramId: h.actor(r), OrderId: id})
	if err != nil {
		if !pmDenied(r, err) {
			r.fail(err)
		}
		return
	}
	switch {
	case id == "":
		r.toast = r.t("admin.ud_toast_retried_all", "n", i18n.Number(r.lang, int64(resp.GetCount())))
	case resp.GetCount() > 0:
		r.toast = r.t("admin.ud_toast_retried")
	default:
		r.toast = r.t("admin.ud_toast_gone")
	}
	h.showUndelivered(r, page, "")
}

// askRefundUndelivered asks to refund one order, in place, saying what it does. An order no longer on the list is
// not asked about: the list is shown again, saying so.
func (h *Handler) askRefundUndelivered(r *req, page int, id string) {
	o, err := h.findUndelivered(r, id)
	if err != nil {
		if !pmDenied(r, err) {
			r.fail(err)
		}
		return
	}
	if o == nil {
		h.showUndelivered(r, page, r.t("admin.ud_gone"))
		return
	}
	p := strconv.Itoa(page)
	kb := (&tg.Keyboard{}).Row(r.button("btn.ud_refund_yes", "adm:udy:"+p+":"+id), r.button("btn.back", "adm:ud:"+p))
	r.show(r.t("admin.ud_ask_refund", udArgs(r, o)...), kb)
}

// refundUndelivered is the Refund of the question: the money goes back to the buyer's wallet (core tells them) and
// the order is cancelled; what became of it is said above the list it shows again in place.
func (h *Handler) refundUndelivered(r *req, page int, id string) {
	resp, err := h.core.AdminRefundUndelivered(r.ctx, &corev1.AdminRefundUndeliveredRequest{
		ActorTelegramId: h.actor(r), OrderId: id, Reason: r.t("admin.ud_reason_refund"),
	})
	if err != nil {
		if !pmDenied(r, err) {
			h.showUndelivered(r, page, r.t("admin.ud_refund_failed", "reason", pmFailReason(r, err)))
		}
		return
	}
	if resp.GetOutcome() != "done" {
		h.showUndelivered(r, page, r.t("admin.ud_gone"))
		return
	}
	note := r.t("admin.ud_refunded", "amount", r.money(resp.GetAmount(), resp.GetCurrency()))
	if c := resp.GetCommissionTaken(); c > 0 {
		note += "\n" + r.t("admin.ud_commission_taken", "amount", r.money(c, resp.GetCurrency()))
	}
	r.toast = r.t("admin.ud_toast_refunded")
	h.showUndelivered(r, page, note)
}
