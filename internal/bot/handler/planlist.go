package handler

import (
	"strings"
	"unicode"
	"unicode/utf8"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// The plan lists (Buy, a reseller's Buy for a customer, the renewal and traffic
// package pickers, the bulk purchase's picker) are drawn by one helper in one of
// two layouts, chosen by the owner (bot.plan_layout):
//
//   - table (the default): a header row (Plan | Price), then each plan as ONE
//     ROW OF TWO BUTTONS, its name and its price, both opening that plan;
//   - list: one button per plan, "name · price" (the text plan.button).
//
// Telegram's limits shape it: at most 100 buttons in a keyboard (the plan list
// leaves room for the rows a screen adds after it) and 8 in a row (a table row
// has two). A name is cut with an ellipsis in a table button, so one long name
// cannot make the keyboard ugly; whatever the layout, a plan's buttons carry the
// same callback data the list always did, so nothing else changes.
const (
	// planLayoutTable and planLayoutList are the values of bot.plan_layout
	// (core's domain.PlanLayoutTable and PlanLayoutList, which the bot cannot import).
	planLayoutTable = "table"
	planLayoutList  = "list"

	// maxPlanButtons is how many buttons a plan list may use: Telegram's 100
	// less the Back, Home and pager rows that follow it on every screen.
	maxPlanButtons = 94
	// tableNameWidth is how many characters of a plan's name a table button
	// shows (half a row on a phone); a longer name ends in an ellipsis.
	tableNameWidth = 24
)

// planEntry is one plan as a list or table needs it.
type planEntry struct {
	name  string // the plan's name in the customer's language
	price string // what this customer pays, formatted
	data  string // the callback data of the plan's buttons
}

// planLayout is the layout in force: "list" only when the owner chose it,
// the table otherwise (the default, and anything core would not have saved).
func (h *Handler) planLayout() string {
	if h.setting("bot.plan_layout") == planLayoutList {
		return planLayoutList
	}
	return planLayoutTable
}

// addPlans appends the plans to kb in the store's layout. data gives the
// callback data of a plan's buttons.
func (h *Handler) addPlans(r *req, kb *tg.Keyboard, plans []*corev1.Plan, data func(*corev1.Plan) string) {
	entries := make([]planEntry, 0, len(plans))
	for _, p := range plans {
		entries = append(entries, planEntry{name: planName(r, p), price: r.money(p.GetPrice().GetAmount(), p.GetPrice().GetCurrency()), data: data(p)})
	}
	rows := planRows(h.planLayout(), [2]string{r.t("plans.head_name"), r.t("plans.head_price")}, entries,
		func(e planEntry) string { return r.t("plan.button", "name", e.name, "price", e.price) })
	for _, row := range rows {
		kb.Row(row...)
	}
}

// planRows lays out the plans as rows of buttons. head is the table's header
// (plan, price); listText is the text of a list button. A table that would not
// fit Telegram's limit (more than 46 plans) is drawn as a list, and a list of
// more than 94 plans shows the first 94: a keyboard Telegram would refuse
// helps nobody.
func planRows(layout string, head [2]string, entries []planEntry, listText func(planEntry) string) [][]tg.Button {
	if layout != planLayoutList && 2+2*len(entries) > maxPlanButtons {
		layout = planLayoutList
	}
	if len(entries) > maxPlanButtons {
		entries = entries[:maxPlanButtons]
	}
	var rows [][]tg.Button
	if layout == planLayoutList {
		for _, e := range entries {
			rows = append(rows, []tg.Button{tg.CB(listText(e), e.data)})
		}
		return rows
	}
	// The header buttons do nothing when tapped ("nop"); they are buttons
	// because an inline keyboard has no other way to hold a label in a column.
	rows = append(rows, []tg.Button{tg.CB(head[0], "nop"), tg.CB(head[1], "nop")})
	for _, e := range entries {
		rows = append(rows, []tg.Button{tg.CB(ellipsize(e.name, tableNameWidth), e.data), tg.CB(e.price, e.data)})
	}
	return rows
}

// ellipsize cuts s to at most width characters, ending in "…" when it was
// longer. A cut name never ends with a space, a joiner (the zero-width
// non-joiner is the half-space of Persian, the joiner binds an emoji sequence) or a
// direction mark before the ellipsis, which would show as a stray gap or
// symbol; the combining marks of the characters kept stay with them.
func ellipsize(s string, width int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	cut := []rune(s)[:width-1]
	for len(cut) > 0 && (unicode.IsSpace(cut[len(cut)-1]) || isJoinerOrDirection(cut[len(cut)-1])) {
		cut = cut[:len(cut)-1]
	}
	return string(cut) + "…"
}

// isJoinerOrDirection reports a zero-width joiner or non-joiner or a direction
// control: characters that mean nothing at the end of a cut text.
func isJoinerOrDirection(r rune) bool {
	switch r {
	case '\u200c', '\u200d', '\u200e', '\u200f', '\u061c', '\u2066', '\u2067', '\u2068', '\u2069':
		return true
	}
	return r >= '\u202a' && r <= '\u202e'
}
