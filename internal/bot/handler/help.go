package handler

import (
	"fmt"
	"strconv"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/helpcontent"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Help content: the FAQ under Support and the tutorials behind the Tutorials
// button (docs/03-bot-ux.md, "Help content"). The owner writes them in the
// dashboard; core hands the bot the enabled ones in the customer's language
// (the other where a translation is missing). Both go through every gate like
// any other screen, since they are reached by a callback or a tap on the bottom
// menu and nothing here is exempt.
//
//	faq                     the questions: one numbered message when the whole FAQ fits in one,
//	                        else the numbered list of them
//	faq:p:<page>            a page of that list
//	faq:q:<page>:<id>       one answer, with its way back to that page
//	tut                     the tutorials, a button for each
//	tut:p:<page>            a page of that list
//	tut:v:<page>:<id>       one tutorial, with its URL buttons and its way back to that page
//
// Every callback is under 64 bytes (a page is at most six digits, an id 36 characters).

const (
	// faqPageSize is how many questions a page of the list holds; their numbers
	// are the buttons, faqNumbersPerRow to a row.
	faqPageSize      = 10
	faqNumbersPerRow = 5
	// tutorialPageSize is how many tutorials a page of the list holds.
	tutorialPageSize = 8
	// pathologicalClip shortens an entry that cannot be sent as written (core
	// checks every entry, so this is for rows written by hand): its text without
	// markup, in runes.
	pathologicalClip = 2000
)

// helpEntries asks core for the enabled entries of a kind in the customer's
// language. ok is false once the failure has been reported. A core older than
// the bot does not know the call: it has nothing to show.
func (h *Handler) helpEntries(r *req, kind string) (entries []*corev1.HelpEntry, ok bool) {
	resp, err := h.core.ListHelpEntries(r.ctx, &corev1.ListHelpEntriesRequest{UserId: r.user.GetId(), Kind: kind, Language: r.lang})
	if status.Code(err) == codes.Unimplemented {
		return nil, true
	}
	if err != nil {
		r.fail(err)
		return nil, false
	}
	return resp.GetEntries(), true
}

// helpEntry asks core for one entry; found is false when it is gone or switched
// off (the customer is told the menu is old), and ok once a failure has been
// reported.
func (h *Handler) helpEntry(r *req, id string) (e *corev1.HelpEntry, found, ok bool) {
	e, err := h.core.GetHelpEntry(r.ctx, &corev1.GetHelpEntryRequest{UserId: r.user.GetId(), Id: id, Language: r.lang})
	switch {
	case status.Code(err) == codes.NotFound || status.Code(err) == codes.InvalidArgument || status.Code(err) == codes.Unimplemented:
		r.toast = r.t("error.expired")
		return nil, false, true
	case err != nil:
		r.fail(err)
		return nil, false, false
	}
	return e, true, true
}

// number writes a number in the customer's digits (Persian digits in Persian).
func number(lang string, n int) string { return i18n.Digits(lang, strconv.Itoa(n)) }

// pageOf cuts the page (counted from 1, held to the pages there are) out of a
// list, and says which page that is and how many there are.
func pageOf[T any](list []T, page int32, size int) (items []T, at, pages int32) {
	pages = int32(max(1, (len(list)+size-1)/size)) //nolint:gosec // a list of at most a few dozen entries
	at = min(max(page, 1), pages)
	from := int(at-1) * size
	return list[from:min(len(list), from+size)], at, pages
}

// pageLine is "Page 2 of 3", or nothing for a list of one page.
func pageLine(r *req, at, pages int32) string {
	if pages <= 1 {
		return ""
	}
	return "\n\n" + r.t("help.page", "page", i18n.Number(r.lang, int64(at)), "pages", i18n.Number(r.lang, int64(pages)))
}

// fitMessage returns a text that Telegram will take: as it is, or, for an entry
// too long for one message (core's checks keep it from happening), its text
// without markup, shortened.
func fitMessage(text string) string {
	if i18n.TextLength(text) <= i18n.MaxMessage {
		return text
	}
	return clip(i18n.StripHTML(text), pathologicalClip)
}

// --- the FAQ ---

// onFAQ handles "faq" and "faq:<p|q>:...".
func (h *Handler) onFAQ(r *req, rest string) {
	sub, arg, _ := strings.Cut(rest, ":")
	if sub == "q" {
		page, id, _ := strings.Cut(arg, ":")
		h.showFAQAnswer(r, pageArg(page), id)
		return
	}
	h.showFAQ(r, pageArg(arg))
}

// faqKeyboard is the foot of every FAQ screen: the way on to support, which is
// always there, and the way back (to where the screen was reached from) and
// home.
func (h *Handler) faqKeyboard(r *req, kb *tg.Keyboard, back string) *tg.Keyboard {
	return kb.Row(r.button("btn.faq_support", "support")).Row(r.button("btn.back", back), r.button("btn.home", "home"))
}

// showFAQ shows the questions: the whole FAQ as one numbered message while it
// fits in one (helpcontent.FitsOneMessage), else a numbered list of the
// questions, a page at a time, each number opening its answer.
func (h *Handler) showFAQ(r *req, page int32) {
	entries, ok := h.helpEntries(r, helpcontent.KindFAQ)
	if !ok {
		return
	}
	if len(entries) == 0 {
		r.show(r.t("help.faq_empty"), h.faqKeyboard(r, &tg.Keyboard{}, "support"))
		return
	}
	if text, ok := h.faqOneMessage(r, entries); ok {
		r.show(text, h.faqKeyboard(r, &tg.Keyboard{}, "support"))
		return
	}
	items, at, pages := pageOf(entries, page, faqPageSize)
	first := int(at-1) * faqPageSize
	var b strings.Builder
	b.WriteString(r.t("help.faq_title") + "\n" + r.t("help.faq_hint") + "\n")
	kb := &tg.Keyboard{}
	var row []tg.Button
	for i, e := range items {
		n := number(r.lang, first+i+1)
		b.WriteString("\n<b>" + n + ".</b> " + esc(e.GetTitle()))
		row = append(row, tg.CB(n, fmt.Sprintf("faq:q:%d:%s", at, e.GetId())))
		if len(row) == faqNumbersPerRow || i == len(items)-1 {
			kb.Row(row...)
			row = nil
		}
	}
	b.WriteString(pageLine(r, at, pages))
	b.WriteString("\n\n" + r.t("help.faq_footer"))
	kb.Row(pager(r, at, at < pages, func(p int32) string { return fmt.Sprintf("faq:p:%d", p) })...)
	r.show(fitMessage(b.String()), h.faqKeyboard(r, kb, "support"))
}

// faqOneMessage writes the whole FAQ as one message, every question numbered
// and bold with its answer under it, and a footer that points to the button
// below; false when the FAQ is too long for one (the rule is core's and the
// dashboard's too, so the owner is told what customers see), or when this text,
// with the store's own wording of the heading and the footer, would not be
// taken by Telegram.
func (h *Handler) faqOneMessage(r *req, entries []*corev1.HelpEntry) (string, bool) {
	units := 0
	for _, e := range entries {
		units += helpcontent.Units(e.GetTitle()) + i18n.TextLength(e.GetBody())
	}
	if !helpcontent.FitsOneMessage(len(entries), units) {
		return "", false
	}
	var b strings.Builder
	b.WriteString(r.t("help.faq_title"))
	for i, e := range entries {
		b.WriteString("\n\n<b>" + number(r.lang, i+1) + ". " + esc(e.GetTitle()) + "</b>\n" + e.GetBody())
	}
	b.WriteString("\n\n" + r.t("help.faq_footer"))
	text := b.String()
	return text, i18n.TextLength(text) <= i18n.MaxMessage
}

// showFAQAnswer shows one question with its answer; back goes to the page of the
// list it was opened from (or to the single message, if the FAQ has become one).
func (h *Handler) showFAQAnswer(r *req, page int32, id string) {
	e, found, ok := h.helpEntry(r, id)
	if !ok {
		return
	}
	if !found {
		h.showFAQ(r, page)
		return
	}
	text := "❓ <b>" + esc(e.GetTitle()) + "</b>\n\n" + e.GetBody()
	r.show(fitMessage(text), h.faqKeyboard(r, &tg.Keyboard{}, fmt.Sprintf("faq:p:%d", page)))
}

// --- the tutorials ---

// onTutorials handles "tut" and "tut:<p|v>:...".
func (h *Handler) onTutorials(r *req, rest string) {
	sub, arg, _ := strings.Cut(rest, ":")
	if sub == "v" {
		page, id, _ := strings.Cut(arg, ":")
		h.showTutorial(r, pageArg(page), id)
		return
	}
	h.showTutorials(r, pageArg(arg))
}

// showTutorials lists the tutorials, a button for each, a page at a time.
func (h *Handler) showTutorials(r *req, page int32) {
	entries, ok := h.helpEntries(r, helpcontent.KindTutorial)
	if !ok {
		return
	}
	if len(entries) == 0 {
		r.show(r.t("help.tutorials_empty"), homeKeyboard(r))
		return
	}
	items, at, pages := pageOf(entries, page, tutorialPageSize)
	kb := &tg.Keyboard{}
	for _, e := range items {
		kb.Row(tg.CB(e.GetTitle(), fmt.Sprintf("tut:v:%d:%s", at, e.GetId())))
	}
	kb.Row(pager(r, at, at < pages, func(p int32) string { return fmt.Sprintf("tut:p:%d", p) })...)
	kb.Row(r.button("btn.home", "home"))
	r.show(r.t("help.tutorials_title")+pageLine(r, at, pages), kb)
}

// showTutorial shows one tutorial: its text, a URL button for each link it has
// (an address the bot cannot put on a button is left out, as Telegram would
// refuse the whole message for it), and the way back to its page of the list.
func (h *Handler) showTutorial(r *req, page int32, id string) {
	e, found, ok := h.helpEntry(r, id)
	if !ok {
		return
	}
	if !found {
		h.showTutorials(r, page)
		return
	}
	kb := &tg.Keyboard{}
	for _, b := range e.GetButtons() {
		if httpsURL(b.GetUrl()) && b.GetLabel() != "" {
			kb.Row(tg.Link(b.GetLabel(), b.GetUrl()))
		}
	}
	kb.Row(r.button("btn.back", fmt.Sprintf("tut:p:%d", page)), r.button("btn.home", "home"))
	r.show(fitMessage("<b>"+esc(e.GetTitle())+"</b>\n\n"+e.GetBody()), kb)
}
