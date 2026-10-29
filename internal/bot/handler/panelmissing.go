package handler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Services whose client is gone from the panel (an operator deleted it there; core marks them and the
// watchdog's alert.panel_missing names them once a day). Staff with services.write (owner and admin) decide
// for each: make the client again, or delete the service from the bot too. The callbacks, all under adm:
//
//	pm            the list, first page, as a new message (the button under the alert)
//	pm:<page>     a page of the list, in place (the admin menu, paging, No)
//	pmr:<page>:<id>, pmd:<page>:<id>   ask to make one again / delete one
//	pmy:<r|d>:<page>:<id>              the Yes of those
//	pma:<r|d>     ask to make all again / delete all
//	pmz:<r|d>:<n> the Yes of those, for the n services the question named
//
// Core checks the role and the service again on every call: a service that changed meanwhile (back on the
// panel, deleted) is skipped and said so, and a stale or forged button does nothing but show the list.

const (
	// pmPage is how many services one page of the list shows.
	pmPage = 5
	// pmBulkMax is the most services one "all" tap processes; the list shows what is left.
	pmBulkMax = 50
	// pmFetch is how many services one call lists when the bot looks for one or collects them all.
	pmFetch = 200
	// pmFailShown is how many failures the summary of an "all" names.
	pmFailShown = 10
)

// uuidRe is a service id as core writes it.
var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// routePanelMissing serves the pm* verbs of the admin menu (routeAdmin checked the role).
func (h *Handler) routePanelMissing(r *req, verb, arg string) {
	switch verb {
	case "pm":
		if arg == "" {
			h.showPanelMissing(r, 0, "", true)
			return
		}
		page, _ := strconv.Atoi(arg)
		h.showPanelMissing(r, page, "", false)
	case "pmr", "pmd":
		page, id, ok := pmPageID(arg)
		if !ok {
			h.showPanelMissing(r, page, r.t("admin.pm_stale"), false)
			return
		}
		h.askPanelMissing(r, map[string]string{"pmr": "r", "pmd": "d"}[verb], page, id)
	case "pmy":
		act, rest, _ := strings.Cut(arg, ":")
		page, id, ok := pmPageID(rest)
		if !ok || (act != "r" && act != "d") {
			h.showPanelMissing(r, page, r.t("admin.pm_stale"), false)
			return
		}
		h.resolvePanelMissing(r, act, page, id)
	case "pma":
		if arg != "r" && arg != "d" {
			h.showPanelMissing(r, 0, r.t("admin.pm_stale"), false)
			return
		}
		h.askPanelMissingAll(r, arg, "")
	case "pmz":
		act, ns, _ := strings.Cut(arg, ":")
		n, err := strconv.ParseInt(ns, 10, 64)
		if (act != "r" && act != "d") || err != nil || n <= 0 {
			h.showPanelMissing(r, 0, r.t("admin.pm_stale"), false)
			return
		}
		h.resolvePanelMissingAll(r, act, n)
	}
}

// pmPageID reads "<page>:<id>": the page (0 when unreadable) and the service id, if it is one.
func pmPageID(arg string) (page int, id string, ok bool) {
	ps, id, _ := strings.Cut(arg, ":")
	page, _ = strconv.Atoi(ps)
	return max(page, 0), id, uuidRe.MatchString(id)
}

// pmDenied tells a staff member core refused (no longer owner or admin) and reports whether it did.
func pmDenied(r *req, err error) bool {
	if status.Code(err) != codes.PermissionDenied {
		return false
	}
	r.toast, r.alert = r.t("admin.forbidden"), true
	return true
}

// listPanelMissing is one call for a page of the list.
func (h *Handler) listPanelMissing(r *req, offset, limit int) (*corev1.AdminListPanelMissingResponse, error) {
	return h.core.AdminListPanelMissing(r.ctx, &corev1.AdminListPanelMissingRequest{
		ActorTelegramId: h.actor(r), Offset: int32(min(offset, 1<<30)), Limit: int32(limit), //nolint:gosec // bounded
	})
}

// showPanelMissing shows a page of the services the panel lacks (from 0; clamped), with note above it (the
// outcome of what was just done, or why nothing was). fresh sends it as a new message, so the alert it was
// opened from stays; otherwise it replaces the pressed message.
func (h *Handler) showPanelMissing(r *req, page int, note string, fresh bool) {
	reply := r.show
	if fresh {
		reply = r.send
	}
	resp, err := h.listPanelMissing(r, max(page, 0)*pmPage, pmPage)
	if err != nil {
		if !pmDenied(r, err) {
			r.fail(err)
		}
		return
	}
	total := int(min(resp.GetTotal(), 1<<30))
	pages := max((total+pmPage-1)/pmPage, 1)
	if page >= pages || page < 0 {
		page = min(max(page, 0), pages-1)
		if resp, err = h.listPanelMissing(r, page*pmPage, pmPage); err != nil {
			r.fail(err)
			return
		}
	}
	head := ""
	if note != "" {
		head = note + "\n\n"
	}
	list := resp.GetServices()
	if total == 0 || len(list) == 0 {
		reply(head+r.t("admin.pm_none"), (&tg.Keyboard{}).Row(r.button("btn.back", "adm")))
		return
	}
	kb := &tg.Keyboard{}
	lines := make([]string, 0, len(list))
	for i, s := range list {
		n := i18n.Number(r.lang, int64(page*pmPage+i+1))
		lines = append(lines, r.t("admin.pm_item", "n", n, "email", esc(s.GetClientEmail()),
			"customer", esc(pmCustomer(s)), "plan", esc(pmPlanName(r, s.GetPlanName())),
			"expires", pmExpires(r, s.GetExpiresAt()), "since", i18n.DateTime(r.lang, time.Unix(s.GetMissingSince(), 0))))
		p := strconv.Itoa(page)
		kb.Row(r.button("btn.pm_recreate", "adm:pmr:"+p+":"+s.GetId(), "n", n),
			r.button("btn.pm_delete", "adm:pmd:"+p+":"+s.GetId(), "n", n))
	}
	paging := ""
	if pages > 1 {
		var nav []tg.Button
		if page > 0 {
			nav = append(nav, r.button("btn.prev", "adm:pm:"+strconv.Itoa(page-1)))
		}
		if page < pages-1 {
			nav = append(nav, r.button("btn.next", "adm:pm:"+strconv.Itoa(page+1)))
		}
		kb.Row(nav...)
		paging = r.t("admin.pending_page", "page", i18n.Number(r.lang, int64(page+1)), "pages", i18n.Number(r.lang, int64(pages)))
	}
	kb.Row(r.button("btn.pm_recreate_all", "adm:pma:r"), r.button("btn.pm_delete_all", "adm:pma:d"))
	kb.Row(r.button("btn.back", "adm"))
	reply(head+r.t("admin.pm_list", "n", i18n.Number(r.lang, int64(total)), "paging", paging, "items", strings.Join(lines, "\n\n")), kb)
}

// pmCustomer is the customer as staff know them: @username, else the Telegram id.
func pmCustomer(s *corev1.AdminPanelMissingService) string {
	if u := s.GetUsername(); u != "" {
		return "@" + u
	}
	return fmt.Sprintf("id %d", s.GetTelegramId())
}

// pmPlanName is the plan's name in the reader's language (another one when it has none).
func pmPlanName(r *req, names map[string]string) string {
	if n := names[r.lang]; n != "" {
		return n
	}
	for _, l := range i18n.Languages {
		if n := names[l]; n != "" {
			return n
		}
	}
	return "?"
}

func pmExpires(r *req, unix int64) string {
	if unix <= 0 {
		return r.t("sub.no_expiry")
	}
	return i18n.Date(r.lang, time.Unix(unix, 0))
}

// findPanelMissing looks a service up in the list (it is not there: made again, deleted, back on the panel).
func (h *Handler) findPanelMissing(r *req, id string) (*corev1.AdminPanelMissingService, error) {
	for offset := 0; ; offset += pmFetch {
		resp, err := h.listPanelMissing(r, offset, pmFetch)
		if err != nil {
			return nil, err
		}
		for _, s := range resp.GetServices() {
			if s.GetId() == id {
				return s, nil
			}
		}
		if len(resp.GetServices()) < pmFetch || int64(offset+pmFetch) >= resp.GetTotal() {
			return nil, nil
		}
	}
}

// askPanelMissing asks to make one service's client again (act r) or delete it (d), in place. A service no
// longer on the list is not asked about: the list is shown again, saying so.
func (h *Handler) askPanelMissing(r *req, act string, page int, id string) {
	s, err := h.findPanelMissing(r, id)
	if err != nil {
		if !pmDenied(r, err) {
			r.fail(err)
		}
		return
	}
	if s == nil {
		h.showPanelMissing(r, page, r.t("admin.pm_gone"), false)
		return
	}
	key := "admin.pm_ask_recreate"
	if act == "d" {
		key = "admin.pm_ask_delete"
	}
	p := strconv.Itoa(page)
	kb := (&tg.Keyboard{}).Row(r.button("btn.pm_yes", "adm:pmy:"+act+":"+p+":"+id), r.button("btn.pm_no", "adm:pm:"+p))
	r.show(r.t(key, "email", esc(s.GetClientEmail()), "customer", esc(pmCustomer(s)),
		"plan", esc(pmPlanName(r, s.GetPlanName())), "expires", pmExpires(r, s.GetExpiresAt())), kb)
}

// pmReason is what the audit log keeps as the reason: what was done, from the bot, by whom.
func pmReason(r *req, act string) string {
	who := fmt.Sprintf("id %d", r.from.ID)
	if r.from.Username != "" {
		who = "@" + r.from.Username
	}
	if act == "d" {
		return r.t("admin.pm_reason_delete", "who", who)
	}
	return r.t("admin.pm_reason_recreate", "who", who)
}

// pmAction is core's name of the action.
func pmAction(act string) string {
	if act == "d" {
		return "delete"
	}
	return "recreate"
}

// resolveOne runs the action on one service: the outcome ("done", "back", "deleted") and the client email
// core reports, or the error.
func (h *Handler) resolveOne(r *req, act, id string) (string, string, error) {
	resp, err := h.core.AdminResolvePanelMissing(r.ctx, &corev1.AdminResolvePanelMissingRequest{
		ActorTelegramId: h.actor(r), SubscriptionId: id, Action: pmAction(act), Reason: pmReason(r, act),
	})
	if err != nil {
		return "", "", err
	}
	return resp.GetOutcome(), resp.GetClientEmail(), nil
}

// pmFailReason is why core could not do it, for staff: the panel not answering in their words, else core's.
func pmFailReason(r *req, err error) string {
	st := status.Convert(err)
	switch st.Code() {
	case codes.Unavailable, codes.DeadlineExceeded:
		return r.t("admin.pm_why_unavailable")
	case codes.Internal, codes.Unknown:
		return r.t("admin.pm_why_internal")
	}
	return esc(st.Message())
}

// resolvePanelMissing is the Yes of one service: done, or skipped, or failed, said above the list it shows
// again in place.
func (h *Handler) resolvePanelMissing(r *req, act string, page int, id string) {
	outcome, email, err := h.resolveOne(r, act, id)
	if err != nil {
		if pmDenied(r, err) {
			return
		}
		failKey := "admin.pm_failed_recreate"
		if act == "d" {
			failKey = "admin.pm_failed_delete"
		}
		if email == "" {
			if s, ferr := h.findPanelMissing(r, id); ferr == nil && s != nil {
				email = s.GetClientEmail()
			}
		}
		h.showPanelMissing(r, page, r.t(failKey, "email", esc(email), "reason", pmFailReason(r, err)), false)
		return
	}
	var note string
	switch outcome {
	case "done":
		note = r.t("admin.pm_done_recreate", "email", esc(email))
		if act == "d" {
			note = r.t("admin.pm_done_delete", "email", esc(email))
		}
		r.toast = r.t("admin.pm_toast_done")
	case "back":
		note = r.t("admin.pm_skipped_back", "email", esc(email))
	default:
		note = r.t("admin.pm_skipped_deleted")
	}
	h.showPanelMissing(r, page, note, false)
}

// askPanelMissingAll asks to make every listed service again (r) or delete them all (d), naming how many;
// note says why it asks again (the list changed).
func (h *Handler) askPanelMissingAll(r *req, act, note string) {
	resp, err := h.listPanelMissing(r, 0, 1)
	if err != nil {
		if !pmDenied(r, err) {
			r.fail(err)
		}
		return
	}
	n := resp.GetTotal()
	if n <= 0 {
		h.showPanelMissing(r, 0, note, false)
		return
	}
	key := "admin.pm_ask_recreate_all"
	if act == "d" {
		key = "admin.pm_ask_delete_all"
	}
	text := r.t(key, "n", i18n.Number(r.lang, n))
	if n > pmBulkMax {
		text += "\n\n" + r.t("admin.pm_batch", "max", i18n.Number(r.lang, pmBulkMax))
	}
	if note != "" {
		text = note + "\n\n" + text
	}
	kb := (&tg.Keyboard{}).Row(r.button("btn.pm_yes", "adm:pmz:"+act+":"+strconv.FormatInt(n, 10)), r.button("btn.pm_no", "adm:pm:0"))
	r.show(text, kb)
}

// resolvePanelMissingAll is the Yes of "all": when the list still holds the n services the question named,
// it runs the action on each (at most pmBulkMax), oldest missing first, and shows what became of them above
// the list; a list that changed meanwhile is asked about again with its new count.
func (h *Handler) resolvePanelMissingAll(r *req, act string, n int64) {
	var ids []string
	emails := map[string]string{}
	var total int64
	for offset := 0; len(ids) < pmBulkMax; offset += pmFetch {
		resp, err := h.listPanelMissing(r, offset, pmFetch)
		if err != nil {
			if !pmDenied(r, err) {
				r.fail(err)
			}
			return
		}
		total = resp.GetTotal()
		for _, s := range resp.GetServices() {
			if len(ids) < pmBulkMax {
				ids = append(ids, s.GetId())
				emails[s.GetId()] = s.GetClientEmail()
			}
		}
		if len(resp.GetServices()) < pmFetch || int64(offset+pmFetch) >= total {
			break
		}
	}
	if total != n {
		h.askPanelMissingAll(r, act, r.t("admin.pm_changed", "n", i18n.Number(r.lang, total)))
		return
	}
	var done, skipped int64
	var failed []string
	for _, id := range ids {
		outcome, _, err := h.resolveOne(r, act, id)
		switch {
		case err != nil && pmDenied(r, err):
			return
		case err != nil:
			failed = append(failed, r.t("admin.pm_fail_line", "email", esc(emails[id]), "reason", pmFailReason(r, err)))
		case outcome == "done":
			done++
		default:
			skipped++
		}
	}
	key := "admin.pm_all_recreated"
	if act == "d" {
		key = "admin.pm_all_deleted"
	}
	note := r.t(key, "done", i18n.Number(r.lang, done), "skipped", i18n.Number(r.lang, skipped), "failed", i18n.Number(r.lang, int64(len(failed))))
	if len(failed) > 0 {
		shown := failed[:min(len(failed), pmFailShown)]
		note += "\n" + strings.Join(shown, "\n")
		if more := len(failed) - len(shown); more > 0 {
			note += "\n" + r.t("admin.pm_more", "n", i18n.Number(r.lang, int64(more)))
		}
	}
	r.toast = r.t("admin.pm_toast_done")
	h.showPanelMissing(r, 0, note, false)
}
