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
)

// html reports whether the context is sent with the HTML parse mode.
func (c Context) html() bool { return c == CtxHTML || c == CtxCaption }

// maxLen is the longest text the context takes, in UTF-16 units of the text
// without tags. Messages and captions keep room for the inserted values.
func (c Context) maxLen() int {
	switch c {
	case CtxButton, CtxButtonHTML:
		return 64
	case CtxToast, CtxToastHTML, CtxPlain, CtxInternal:
		return 200
	case CtxInvoiceTitle:
		return 32
	case CtxInvoiceDesc:
		return 255
	case CtxCaption:
		return 900
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
	"lang.changed":           CtxToast,
	"admin.status_done":      CtxToast,
	"pay.check_pending":      CtxToast,
	"error.generic":          CtxToastHTML,
	"error.banned":           CtxToastHTML,
	"error.unavailable":      CtxToastHTML,
	"error.menu_expired":     CtxToastHTML,
	"trial.used":             CtxToastHTML,
	"trial.unavailable":      CtxToastHTML,
	"sub.not_ready":          CtxToastHTML,
	"pay.already_submitted":  CtxToastHTML,
	"sub.not_extendable":     CtxToastHTML,
	"plan.unavailable":       CtxToastHTML,
	"pay.duplicate":          CtxToastHTML,
	"cancelled":              CtxToastHTML,
	"admin.forbidden":        CtxToastHTML,
	"admin.reject_reason":    CtxToastHTML,
	"admin.approved":         CtxToastHTML,
	"admin.rejected":         CtxToastHTML,
	"admin.already_reviewed": CtxToastHTML,
	"maintenance":            CtxToastHTML,
	"join.not_yet":           CtxToast,

	"pay.stars_title":     CtxInvoiceTitle,
	"pay.topup_title":     CtxInvoiceTitle,
	"pay.stars_desc":      CtxInvoiceDesc,
	"pay.precheck_failed": CtxPlain,
	"ref.share_text":      CtxPlain,
	"paysupport.in_bot":   CtxPlain,
	"admin.status_reason": CtxInternal,

	"sub.qr_caption":     CtxCaption,
	"admin.pending_item": CtxCaption,
}

// contextPrefixes give whole families a context.
var contextPrefixes = map[string]Context{
	"btn.":          CtxButton,
	"sub.status.":   CtxButtonHTML,
	"discount.bad.": CtxToastHTML,
}

// markupArgs are placeholders whose values carry tags (other rendered
// texts), so a text must not put them where tags are not allowed.
var markupArgs = map[string][]string{
	"plan.detail":          {"limits"},
	"renew.detail":         {"limits"},
	"pay.crypto":           {"addresses"},
	"admin.pending_item":   {"proof", "dup", "method"},
	"admin.title":          {"panel"},
	"admin.settings_title": {"list"},
	"admin.discount_saved": {"line"},
	"support.prompt":       {"contact"},
	"pay.rejected":         {"reason"},
	"pay.check_failed":     {"reason"},
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
