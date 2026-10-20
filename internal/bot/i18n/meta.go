package i18n

import (
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Context is where a text is shown, which decides what an override may contain.
type Context string

const (
	// CtxHTML is a message sent with Telegram's HTML parse mode.
	CtxHTML Context = "html"
	// CtxCaption is a photo caption, also HTML, with a shorter limit.
	CtxCaption Context = "caption"
	// CtxButton is a button label: plain text, tags would show literally.
	CtxButton Context = "button"
	// CtxButtonHTML is shown on a button and inside a message: plain text
	// that is also safe inside HTML.
	CtxButtonHTML Context = "button_html"
	// CtxToast is the short popup after a button tap: plain text.
	CtxToast Context = "toast"
	// CtxToastHTML is a popup after a tap or a message after typing.
	CtxToastHTML Context = "toast_html"
	// CtxInvoiceTitle and CtxInvoiceDesc are Telegram Stars invoice fields.
	CtxInvoiceTitle Context = "invoice_title"
	CtxInvoiceDesc  Context = "invoice_desc"
	// CtxPlain is plain text sent somewhere other than a message.
	CtxPlain Context = "plain"
	// CtxInternal is never shown to customers (stored as a reason).
	CtxInternal Context = "internal"
	// CtxCommand describes a command in the bot's menu: one line of plain text.
	CtxCommand Context = "command"
	// CtxBotDescription is the text of an empty chat with the bot ("What can
	// this bot do?"): plain text.
	CtxBotDescription Context = "bot_description"
	// CtxBotAbout is the bot's short description (its profile page and the
	// preview when it is shared): one line of plain text.
	CtxBotAbout Context = "bot_about"
	// CtxPlaceholder is the hint in the empty message field while the bottom
	// menu is shown: one line of plain text.
	CtxPlaceholder Context = "placeholder"
)

// html reports whether the context is sent with the HTML parse mode.
func (c Context) html() bool { return c == CtxHTML || c == CtxCaption }

// oneLine reports whether Telegram shows the text on a single line.
func (c Context) oneLine() bool { return c == CtxCommand || c == CtxBotAbout || c == CtxPlaceholder }

// maxLen is the longest text the context takes, in UTF-16 units of the text
// without tags. Messages and captions keep room for the inserted values.
func (c Context) maxLen() int {
	switch c {
	case CtxButton, CtxButtonHTML, CtxPlaceholder:
		return 64
	case CtxToast, CtxToastHTML, CtxPlain, CtxInternal:
		return 200
	case CtxInvoiceTitle:
		return 32
	case CtxInvoiceDesc:
		return 255
	case CtxCaption:
		return 900
	case CtxCommand: // Telegram's limits
		return 256
	case CtxBotDescription:
		return 512
	case CtxBotAbout:
		return 120
	}
	return 3500
}

// contexts lists every text that is not an HTML message; btn.* are buttons.
var contexts = map[string]Context{
	"plan.button":    CtxButton,
	"subs.item":      CtxButton,
	"admin.plan_on":  CtxButton,
	"admin.plan_off": CtxButton,

	"sub.no_expiry":          CtxButtonHTML,
	"reason.amount":          CtxButtonHTML,
	"reason.unreadable":      CtxButtonHTML,
	"reason.not_received":    CtxButtonHTML,
	"admin.unknown":          CtxButtonHTML,
	"error.rate":             CtxToast,
	"error.expired":          CtxToast,
	"lang.changed":           CtxToastHTML, // a popup, or a new user's language picker edited into it
	"admin.status_done":      CtxToast,
	"pay.check_pending":      CtxToast,
	"pay.usdt_check_pending": CtxToast,
	"pay.check_wait":         CtxToast,
	"admin.txid_used":        CtxToastHTML,
	"admin.unmatched_part":   CtxButtonHTML,
	"error.generic":          CtxToastHTML,
	"error.banned":           CtxToastHTML,
	"error.unavailable":      CtxToastHTML,
	"error.purchases_paused": CtxToastHTML,
	"error.menu_expired":     CtxToastHTML,
	"trial.used":             CtxToastHTML,
	"trial.unavailable":      CtxToastHTML,
	"sub.not_ready":          CtxToastHTML,
	"pay.already_submitted":  CtxToastHTML,
	"sub.not_extendable":     CtxToastHTML,
	"plan.unavailable":       CtxToastHTML,
	"pay.duplicate":          CtxToastHTML,
	"pay.txid_announced":     CtxToastHTML,
	"cancelled":              CtxToastHTML,
	"admin.forbidden":        CtxToastHTML,
	"admin.reject_reason":    CtxToastHTML,
	"admin.approved":         CtxToastHTML,
	"admin.rejected":         CtxToastHTML,
	"admin.already_reviewed": CtxToastHTML,
	"maintenance":            CtxToastHTML,
	"join.not_yet":           CtxToast,
	"support.closed":         CtxToast,
	"rs.forbidden":           CtxToastHTML,
	"rs.label_removed":       CtxToast,
	"wallet.debt":            CtxButtonHTML,

	"pay.stars_title":     CtxInvoiceTitle,
	"pay.topup_title":     CtxInvoiceTitle,
	"pay.stars_desc":      CtxInvoiceDesc,
	"pay.precheck_failed": CtxPlain,
	"ref.share_text":      CtxPlain,
	"paysupport.in_bot":   CtxPlain,
	"admin.status_reason": CtxInternal,
	// A backup file's caption is sent without a parse mode.
	"backup.caption": CtxPlain,

	// The subscription page's app guides (/sub/<token>) are plain text.
	"subpage.guide.v2rayng":   CtxPlain,
	"subpage.guide.hiddify":   CtxPlain,
	"subpage.guide.streisand": CtxPlain,
	"subpage.guide.v2box":     CtxPlain,

	"sub.qr_caption":     CtxCaption,
	"admin.pending_item": CtxCaption,

	"profile.description": CtxBotDescription,
	"profile.about":       CtxBotAbout,
	"profile.menu_button": CtxButton, // the text of Telegram's menu button, which opens the Mini App
	"menu.placeholder":    CtxPlaceholder,
}

// buttonStyles colors buttons by what they do, the same on every keyboard
// (the bottom menu too), as the Bot API names the colors: green ("success")
// pays or confirms, blue ("primary") opens a main action, red ("danger")
// cancels or refuses. Every other button keeps the app's own look:
// navigation, lists to choose from, links to read.
var buttonStyles = map[string]string{
	"btn.buy":               "success",
	"btn.trial":             "success",
	"btn.pay_wallet":        "success",
	"btn.pay_credit":        "success",
	"btn.pay_card":          "success",
	"btn.pay_crypto":        "success",
	"btn.pay_zarinpal_link": "success",
	"btn.pay_zarinpal":      "success",
	"btn.pay_idpay":         "success",
	"btn.pay_nextpay":       "success",
	"btn.pay_stars":         "success",
	"btn.pay_usdt_auto":     "success",
	"btn.open_payment":      "success",
	"btn.approve":           "success",
	"btn.adm_unban":         "success",
	"btn.adm_confirm":       "success",
	"btn.adm_enable":        "success",
	"btn.join_check":        "success",

	"btn.services":      "primary",
	"btn.wallet":        "primary",
	"btn.support":       "primary",
	"btn.renew":         "primary",
	"btn.add_traffic":   "primary",
	"btn.topup":         "primary",
	"btn.topup_and_buy": "primary",
	"btn.check_payment": "primary",
	"btn.try_again":     "primary",
	"btn.rs_buy":        "primary",
	"btn.adm_pending":   "primary",
	"btn.store":         "primary",
	"btn.review_now":    "primary",
	"btn.ticket_more":   "primary",
	"btn.ticket_reply":  "primary",
	"btn.share":         "primary",
	"btn.join_channel":  "primary",
	"btn.invite":        "primary",
	"btn.reseller":      "primary",

	"btn.cancel":          "danger",
	"btn.reject":          "danger",
	"reason.amount":       "danger", // the preset reasons to reject a payment
	"reason.unreadable":   "danger",
	"reason.not_received": "danger",
	"btn.reject_other":    "danger",
	"btn.adm_ban":         "danger",
	"btn.remove_name":     "danger",
}

// ButtonStyle is the Bot API style of a button labelled with the text key:
// "success", "primary", "danger", or "" for the app's own look.
func ButtonStyle(key string) string { return buttonStyles[key] }

// contextPrefixes give whole families a context.
var contextPrefixes = map[string]Context{
	"btn.":            CtxButton,
	"sub.status.":     CtxButtonHTML,
	"discount.bad.":   CtxToastHTML,
	"support.status.": CtxButtonHTML,
	"support.cat.":    CtxButtonHTML,
	"profile.cmd.":    CtxCommand,
}

// markupArgs are placeholders whose values carry tags (other rendered
// texts), so a text must not put them where tags are not allowed.
var markupArgs = map[string][]string{
	"plan.detail":          {"limits"},
	"renew.detail":         {"limits"},
	"pay.crypto":           {"addresses"},
	"admin.pending_item":   {"proof", "dup", "method"},
	"admin.title":          {"panel"},
	"admin.servers_down":   {"names"},
	"admin.discount_saved": {"line"},
	"support.prompt":       {"contact"},
	"support.thread":       {"messages"},
	"pay.rejected":         {"reason"},
	"pay.check_failed":     {"reason"},
	"rs.panel":             {"balances"},
	"alert.critical":       {"title"},
	"alert.warning":        {"title"},
	"alert.repeat":         {"title"},
	"alert.resolved":       {"title"},

	// An unmatched deposit's reason is a rendered text.
	"admin.unmatched_deposit": {"reason"},
}

// onlyLangs are texts read in some languages only.
var onlyLangs = map[string][]string{
	"lang.prompt": {"en"}, // the language picker is shown before a language is known
}

// groupOf files a text under a heading for the editor.
func groupOf(key string) string {
	first, _, _ := strings.Cut(key, ".")
	switch first {
	case "welcome", "menu", "lang", "cancelled", "error", "maintenance", "join", "terms":
		return "general"
	case "reason", "paysupport":
		return "pay"
	case "subs":
		return "sub"
	case "plans":
		return "plan"
	case "backup", "alert", "report": // sent to staff only, beside the admin alerts
		return "admin"
	}
	return first
}

var placeholderRe = regexp.MustCompile(`\{([a-z_]+)\}`)

// TextInfo describes one overridable text.
type TextInfo struct {
	Key          string
	Group        string
	Context      Context
	Placeholders []string // sorted
	Max          int
	Langs        []string
}

// ContextOf is where the text with this key is shown.
func ContextOf(key string) Context {
	if c, ok := contexts[key]; ok {
		return c
	}
	for p, c := range contextPrefixes {
		if strings.HasPrefix(key, p) {
			return c
		}
	}
	return CtxHTML
}

// Info describes a text of the catalog; false for an unknown key.
func (c *Catalog) Info(key string) (TextInfo, bool) {
	def, ok := c.base["en"][key]
	if !ok {
		return TextInfo{}, false
	}
	ctx := ContextOf(key)
	langs := onlyLangs[key]
	if langs == nil {
		langs = Languages
	}
	return TextInfo{
		Key: key, Group: groupOf(key), Context: ctx,
		Placeholders: placeholders(def), Max: ctx.maxLen(),
		Langs: slices.Clone(langs),
	}, true
}

// Infos describes every text, by group then key.
func (c *Catalog) Infos() []TextInfo {
	out := make([]TextInfo, 0, len(c.base["en"]))
	for k := range c.base["en"] {
		info, _ := c.Info(k)
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Default is the built-in text of key in lang ("" for an unknown key).
func (c *Catalog) Default(lang, key string) string { return c.base[lang][key] }

// placeholders lists the distinct {names} of a text, sorted.
func placeholders(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}
