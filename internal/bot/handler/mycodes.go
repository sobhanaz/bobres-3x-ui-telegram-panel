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

// My codes (docs/03-bot-ux.md): the Wallet screen's button and the /codes command list the customer's own reward codes and the gift codes
// they applied, newest first, five to a page. The bot holds no rule: core answers for the customer it is told (the one who sent the update),
// never for another, and says each code's state. A reward code is shown whole, in a code span that a tap copies; a gift code is never kept
// by core, so its entry has the last characters of the code and what it credited. A code is never logged here.

// codesPageSize is how many codes a page holds.
const codesPageSize = 5

// codeMarks open a reward code's entry, by its state (the states core names).
var codeMarks = map[string]string{"unused": "🟢", "used": "✅", "expired": "⌛", "off": "⛔"}

// namesIn is a name in the customer's language, else in any language it has ("" when it has none).
func namesIn(r *req, names map[string]string) string {
	if n := names[r.lang]; n != "" {
		return n
	}
	for _, l := range i18n.ContentOrder(r.lang) {
		if n := names[l]; n != "" {
			return n
		}
	}
	return ""
}

// showMyCodes handles "cds[:<page>]" (and the /codes command): a page of the customer's own codes. A core older than this bot has no such
// call: the customer is told it is not available yet, and sees the wallet.
func (h *Handler) showMyCodes(r *req, page int32) {
	ask := func(page int32) (*corev1.ListMyCodesResponse, error) {
		return h.core.ListMyCodes(r.ctx, &corev1.ListMyCodesRequest{UserId: r.user.GetId(),
			Pagination: &commonv1.Pagination{Page: page, PageSize: codesPageSize}})
	}
	resp, err := ask(page)
	if status.Code(err) == codes.Unimplemented {
		r.toast, r.alert = r.t("codes.unavailable"), true
		h.showWallet(r)
		return
	}
	if err != nil {
		r.fail(err)
		return
	}
	total := resp.GetPageInfo().GetTotal()
	pages := (total + codesPageSize - 1) / codesPageSize
	if len(resp.GetCodes()) == 0 && total > 0 {
		// A page past the end (an old button): the last page instead.
		page = int32(pages) //nolint:gosec // pages <= total / 5, and a page asked for was below maxPage
		if resp, err = ask(page); err != nil {
			r.fail(err)
			return
		}
	}
	if len(resp.GetCodes()) == 0 {
		r.show(r.t("codes.empty"), backHome(r, "wallet"))
		return
	}

	entries := make([]string, 0, len(resp.GetCodes()))
	usable := false
	for _, c := range resp.GetCodes() {
		entries = append(entries, r.codeEntry(c))
		usable = usable || c.GetKind() == "reward" && c.GetState() == "unused"
	}
	pageText, hint := "", ""
	if pages > 1 {
		pageText = r.t("codes.page", "page", i18n.Number(r.lang, int64(page)), "pages", i18n.Number(r.lang, pages))
	}
	kb := &tg.Keyboard{}
	kb.Row(pager(r, page, int64(page)*codesPageSize < total, func(p int32) string { return "cds:" + strconv.Itoa(int(p)) })...)
	if usable { // a code that can still be used: the way to spend it, and how
		kb.Row(r.button("btn.buy", "buy"))
		hint = "\n\n" + r.t("codes.hint")
	}
	kb.Row(r.button("btn.back", "wallet"), r.button("btn.home", "home"))
	r.show(r.t("codes.screen", "page", pageText, "entries", strings.Join(entries, "\n\n"), "hint", hint), kb)
}

// codeEntry is one code as the list shows it. A reward code: its text, what it takes off and its state, when it ends or was used, and the
// purchase that gave it. A gift code: its last characters, what it added to the wallet and when.
func (r *req) codeEntry(c *corev1.MyCode) string {
	date := func(unix int64) string { return i18n.Date(r.lang, time.Unix(unix, 0)) }
	if c.GetKind() == "gift" {
		at := c.GetUsedAt()
		if at == 0 {
			at = c.GetReceivedAt()
		}
		a := c.GetAmount()
		return r.t("codes.item_gift", "tail", esc(c.GetTail()), "amount", r.money(a.GetAmount(), a.GetCurrency()), "date", date(at))
	}

	// What it takes off, and where it works.
	value := i18n.Percent(r.lang, int64(c.GetPercent()))
	if a := c.GetAmount(); a.GetAmount() > 0 {
		value = r.money(a.GetAmount(), a.GetCurrency())
	}
	worth := r.t("codes.worth_any", "value", value)
	if only := namesIn(r, c.GetOnlyPlanNames()); only != "" {
		worth = r.t("codes.worth_plan", "value", value, "plan", isolated(esc(only)))
	}

	// Its state: a word of ours for the four core names, and none for one a newer core adds.
	state := c.GetState()
	mark, ok := codeMarks[state]
	if !ok {
		mark = "🎟"
	} else {
		worth += " · <b>" + r.t("codes.state."+state) + "</b>"
	}

	// When it ends, was used or was switched off.
	var when string
	switch {
	case state == "unused" && c.GetExpiresAt() > 0:
		when = r.t("codes.when.unused", "date", date(c.GetExpiresAt()))
	case state == "unused":
		when = r.t("codes.when.unused_open")
	case state == "used" && c.GetUsedAt() > 0:
		when = r.t("codes.when.used", "date", date(c.GetUsedAt()))
	case state == "expired" && c.GetExpiresAt() > 0:
		when = r.t("codes.when.expired", "date", date(c.GetExpiresAt()))
	case state == "off":
		when = r.t("codes.when.off")
	}

	// Where it came from.
	from := r.t("codes.from_none", "date", date(c.GetReceivedAt()))
	if plan := namesIn(r, c.GetPlanNames()); plan != "" {
		from = r.t("codes.from_plan", "plan", isolated(esc(plan)), "date", date(c.GetReceivedAt()))
	}

	lines := []string{r.t("codes.head", "mark", mark, "code", esc(c.GetCode())), worth}
	if when != "" {
		lines = append(lines, when)
	}
	return strings.Join(append(lines, from), "\n")
}
