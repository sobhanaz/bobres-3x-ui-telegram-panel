package handler

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Support tickets (milestone 4): one open conversation per customer. The
// Support screen shows it, or starts one; messages in the ticket scene and
// Telegram replies to a delivered staff answer go into it. Staff answer
// from the dashboard; their replies and the staff's alerts are sent by the
// notifier (core's ticket.* events).

const (
	// maxTicketFile is the largest file a customer can attach (what the bot
	// hands the dashboard at /internal/files).
	maxTicketFile = 10 << 20
	// threadPreview is how much of each message the chat shows, in runes.
	threadPreview = 500
	// threadLast and viewLast are the messages shown on the Support screen
	// and when reading a past ticket.
	threadLast = 3
	viewLast   = 5
	// ticketHistory is how many past tickets are listed.
	ticketHistory = 5
)

// ticketCategories a new ticket can be filed under (core's).
var ticketCategories = []string{"payment", "service", "other"}

// ticketFileTypes are the documents support takes: images the dashboard can
// show and PDFs.
var ticketFileTypes = []string{"image/jpeg", "image/png", "image/webp", "image/gif", "application/pdf"}

// onSupport shows the customer's open ticket, or the prompt to start one.
// Either way, what the customer types next goes to support.
func (h *Handler) onSupport(r *req) {
	mine, err := h.core.GetMyTickets(r.ctx, &corev1.GetMyTicketsRequest{UserId: r.user.GetId(), Limit: 1})
	if err != nil {
		r.fail(err)
		return
	}
	if t := mine.GetOpen(); t != nil {
		th, err := h.core.GetMyTicket(r.ctx, &corev1.GetMyTicketRequest{UserId: r.user.GetId(), TicketId: t.GetId(), Last: threadLast})
		if err != nil {
			r.fail(err)
			return
		}
		r.setState(sceneTicket, map[string]string{"tid": t.GetId()})
		kb := (&tg.Keyboard{}).
			Row(tg.CB(r.t("btn.ticket_more"), "tk:r:"+t.GetId()), tg.CB(r.t("btn.ticket_close"), "tk:c:"+t.GetId())).
			Row(tg.CB(r.t("btn.ticket_history"), "tk:l")).
			Row(tg.CB(r.t("btn.home"), "home"))
		r.show(h.threadText(r, th), kb)
		return
	}
	r.setState(sceneTicket, nil) // typing at once files it under "other"
	h.showSupportPrompt(r, "")
}

// showSupportPrompt asks for a new ticket's message, with the topics (cat,
// the one picked, is ticked).
func (h *Handler) showSupportPrompt(r *req, cat string) {
	contact := ""
	if c := h.setting("branding.support"); c != "" {
		contact = r.t("support.contact", "contact", esc(c))
	}
	cats := make([]tg.Button, 0, len(ticketCategories))
	for _, c := range ticketCategories {
		label := r.t("support.cat." + c)
		if c == cat {
			label = "✅ " + label
		}
		cats = append(cats, tg.CB(label, "tk:n:"+c))
	}
	kb := (&tg.Keyboard{}).Row(cats...).Row(h.legalButtons(r)...).
		Row(tg.CB(r.t("btn.ticket_history"), "tk:l")).
		Row(tg.CB(r.t("btn.cancel"), "cancel"))
	r.show(r.t("support.prompt", "contact", contact), kb)
}

// onTicketButton handles "tk:<n|r|c|l|v>[:<arg>]".
func (h *Handler) onTicketButton(r *req, rest string) {
	sub, arg, _ := strings.Cut(rest, ":")
	switch sub {
	case "n": // a topic for the new ticket
		if !slices.Contains(ticketCategories, arg) {
			arg = "other"
		}
		r.setState(sceneTicket, map[string]string{"cat": arg})
		h.showSupportPrompt(r, arg)
	case "r":
		h.onTicketWrite(r, arg)
	case "c":
		h.onTicketClose(r, arg)
	case "l":
		h.showTicketHistory(r)
	case "v":
		h.showTicketView(r, arg)
	default: // perhaps under a staff answer: nothing is replaced
		r.toast = r.t("error.expired")
	}
}

// onTicketWrite: [Reply] under a staff answer, or [Write more]. The answer
// stays in the chat (only its buttons go); the prompt is a new message.
func (h *Handler) onTicketWrite(r *req, tid string) {
	th, err := h.core.GetMyTicket(r.ctx, &corev1.GetMyTicketRequest{UserId: r.user.GetId(), TicketId: tid, Last: 1})
	if status.Code(err) == codes.NotFound {
		r.toast = r.t("error.expired")
		return
	}
	if err != nil {
		r.fail(err)
		return
	}
	h.dropButtons(r)
	r.setState(sceneTicket, map[string]string{"tid": tid})
	r.send(r.t("support.write", "id", th.GetTicket().GetNumber()), cancelKeyboard(r))
}

// onTicketClose: the customer closes their ticket (again: no harm).
func (h *Handler) onTicketClose(r *req, tid string) {
	_, err := h.core.CloseMyTicket(r.ctx, &corev1.CloseMyTicketRequest{UserId: r.user.GetId(), TicketId: tid})
	if status.Code(err) == codes.NotFound {
		r.toast = r.t("error.expired")
		return
	}
	if err != nil {
		r.fail(err)
		return
	}
	h.dropButtons(r)
	if st, err := h.state.Get(r.ctx, r.from.ID); err == nil && st.Scene == sceneTicket && st.Get("tid") == tid {
		r.clearState()
	}
	r.toast = r.t("support.closed")
}

// dropButtons removes the pressed message's buttons, leaving its text (a
// staff answer must stay readable in the chat).
func (h *Handler) dropButtons(r *req) {
	if r.cb == nil || r.cb.Message == nil {
		return
	}
	if err := h.tg.EditMessageReplyMarkup(r.ctx, r.chatID, r.cb.Message.MessageID, nil); err != nil {
		h.log.Debug("removing buttons failed", "err", err)
	}
}

// showTicketHistory lists the customer's recent tickets.
func (h *Handler) showTicketHistory(r *req) {
	mine, err := h.core.GetMyTickets(r.ctx, &corev1.GetMyTicketsRequest{UserId: r.user.GetId(), Limit: ticketHistory})
	if err != nil {
		r.fail(err)
		return
	}
	if len(mine.GetRecent()) == 0 {
		r.show(r.t("support.history_empty"), backHome(r, "support"))
		return
	}
	kb := &tg.Keyboard{}
	for _, t := range mine.GetRecent() {
		label := fmt.Sprintf("#%d · %s · %s", t.GetNumber(), i18n.Date(r.lang, time.Unix(t.GetCreatedAt(), 0)),
			r.t("support.status."+t.GetStatus()))
		kb.Row(tg.CB(label, "tk:v:"+t.GetId()))
	}
	kb.Row(tg.CB(r.t("btn.back"), "support"), tg.CB(r.t("btn.home"), "home"))
	r.show(r.t("support.history"), kb)
}

// showTicketView shows a ticket's last messages, read only.
func (h *Handler) showTicketView(r *req, tid string) {
	th, err := h.core.GetMyTicket(r.ctx, &corev1.GetMyTicketRequest{UserId: r.user.GetId(), TicketId: tid, Last: viewLast})
	if status.Code(err) == codes.NotFound {
		h.showTicketHistory(r)
		return
	}
	if err != nil {
		r.fail(err)
		return
	}
	r.show(h.threadText(r, th), backHome(r, "tk:l"))
}

// threadText renders a ticket with its last messages (each shortened).
func (h *Handler) threadText(r *req, th *corev1.TicketThread) string {
	parts := make([]string, 0, len(th.GetMessages()))
	for _, m := range th.GetMessages() {
		icon := "👤"
		if m.GetKind() == "staff" {
			icon = "💬"
		}
		body := esc(clip(m.GetBody(), threadPreview))
		if m.GetHasFile() {
			body = strings.TrimSpace("📎 " + body)
		}
		parts = append(parts, icon+" <i>"+when(r.lang, m.GetCreatedAt())+"</i>\n"+body)
	}
	t := th.GetTicket()
	return r.t("support.thread", "id", t.GetNumber(), "status", r.t("support.status."+t.GetStatus()),
		"messages", strings.Join(parts, "\n\n"))
}

// when is a moment as the chat shows it: the day and the time.
func when(lang string, unix int64) string {
	t := time.Unix(unix, 0).In(i18n.CurrentLocation())
	return i18n.Date(lang, t) + " " + i18n.Digits(lang, t.Format("15:04"))
}

// clip shortens s to n runes, marking the cut.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ticketContent is what a customer's message gives support: text and at
// most one file.
type ticketContent struct {
	text                         string
	fileID, fileType, mime, name string
	size                         int64
}

// ticketContentOf reads a message for support; ok is false for what support
// does not take (voice messages, videos, other files, files over 10 MB).
func ticketContentOf(m *tg.Message) (ticketContent, bool) {
	c := ticketContent{text: strings.TrimSpace(m.Text)}
	if c.text == "" {
		c.text = strings.TrimSpace(m.Caption)
	}
	if m.HasOtherMedia() {
		return c, false
	}
	switch {
	case len(m.Photo) > 0:
		p := m.LargestPhotoSize()
		c.fileID, c.fileType, c.mime, c.size = p.FileID, "photo", "image/jpeg", int64(p.FileSize)
	case m.Document != nil:
		d := m.Document
		mime := strings.ToLower(d.MimeType)
		if !slices.Contains(ticketFileTypes, mime) {
			return c, false
		}
		c.fileID, c.fileType, c.mime, c.name, c.size = d.FileID, "document", mime, clip(d.FileName, 200), d.FileSize
	}
	if c.size > maxTicketFile {
		return c, false
	}
	return c, c.text != "" || c.fileID != ""
}

// ticketTarget is where a support message goes: a ticket the customer
// named (a button), the staff answer they replied to, or else their open
// ticket or a new one under cat.
type ticketTarget struct {
	tid, cat string
	replyTo  int64
}

// onTicket handles a message in the ticket scene.
func (h *Handler) onTicket(r *req, st state.State) {
	h.addToTicket(r, ticketTarget{tid: st.Get("tid"), cat: st.Get("cat")})
}

// replyToBot reports a message answering (Telegram's reply) one of the
// bot's messages.
func replyToBot(m *tg.Message) bool {
	re := m.ReplyToMessage
	return re != nil && re.MessageID > 0 && (re.From == nil || re.From.IsBot)
}

// onTicketReply handles a message outside any scene that replies to one of
// the bot's messages: it goes to the ticket when it answers a delivered
// staff reply; anything else shows the menu, as other text does.
func (h *Handler) onTicketReply(r *req) {
	h.addToTicket(r, ticketTarget{replyTo: int64(r.msg.ReplyToMessage.MessageID)})
}

// addToTicket records a customer's message for support and confirms it
// (once per album).
func (h *Handler) addToTicket(r *req, to ticketTarget) {
	content, ok := ticketContentOf(r.msg)
	if !ok {
		if to.replyTo != 0 { // not known to be for support: as any other message
			h.notForSupport(r)
			return
		}
		r.send(r.t("support.need_content"), cancelKeyboard(r))
		return
	}
	if r.gated && !h.hasOpenTicket(r) {
		h.answerGate(r) // continuing a ticket passes the gates, starting one does not
		return
	}
	album := r.msg.MediaGroupID
	uid := strconv.FormatInt(r.from.ID, 10)
	limitKey := "bot:tkmg:limit:" + uid + ":" + album
	first := true // an album's first part (the album counts once)
	if album != "" {
		if ok, err := h.state.Once(r.ctx, "tkmg:"+uid+":"+album, time.Minute); err == nil {
			first = ok
		}
	}
	counted := false
	if first {
		if !h.allowTicket(r) {
			if album != "" {
				_ = h.state.Remember(r.ctx, limitKey, time.Minute) // the album's other parts too
			}
			r.send(r.t("support.limit"), homeKeyboard(r))
			return
		}
		counted = true
	} else if refused, err := h.state.Recall(r.ctx, limitKey); err == nil && refused {
		return
	}

	in := &corev1.AddTicketMessageRequest{UserId: r.user.GetId(), Category: to.cat, TicketId: to.tid,
		ReplyToTgMessageId: to.replyTo, Text: content.text, FileId: content.fileID, FileType: content.fileType,
		FileMime: content.mime, FileName: content.name, FileSize: content.size, TgMessageId: int64(r.msg.MessageID)}
	resp, err := h.core.AddTicketMessage(r.ctx, in)
	if status.Code(err) == codes.NotFound && to.tid != "" && !r.gated {
		// The ticket the scene named is gone: the open one, or a new one.
		in.TicketId, in.Category = "", "other"
		resp, err = h.core.AddTicketMessage(r.ctx, in)
	}
	if err != nil {
		if counted {
			h.giveTicket(r) // nothing was recorded: it does not count
		}
		if status.Code(err) == codes.NotFound { // not a reply to a delivered staff answer
			h.notForSupport(r)
			return
		}
		r.fail(err)
		return
	}
	t := resp.GetTicket()
	r.setState(sceneTicket, map[string]string{"tid": t.GetId()}) // more details attach to it
	if !resp.GetCreated() {
		return // this Telegram message was recorded before
	}
	if album != "" {
		if ok, err := h.state.Once(r.ctx, "tkmc:"+uid+":"+album, time.Minute); err == nil && !ok {
			return // the album was confirmed already
		}
	}
	var text string
	switch {
	case resp.GetNewTicket() && resp.GetClosedNumber() > 0:
		text = r.t("support.reopened_new", "old", resp.GetClosedNumber(), "id", t.GetNumber())
	case resp.GetNewTicket():
		text = r.t("support.sent", "id", t.GetNumber())
	default:
		text = r.t("support.added", "id", t.GetNumber())
	}
	r.send(text, homeKeyboard(r))
}

// notForSupport answers a message that turned out not to be for support:
// the menu, or what the gates say.
func (h *Handler) notForSupport(r *req) {
	if r.gated {
		h.answerGate(r)
		return
	}
	h.showHome(r)
}

// hasOpenTicket reports whether the customer has a ticket that is not
// closed (asked only when a gate would otherwise block them).
func (h *Handler) hasOpenTicket(r *req) bool {
	mine, err := h.core.GetMyTickets(r.ctx, &corev1.GetMyTicketsRequest{UserId: r.user.GetId(), Limit: 1})
	return err == nil && mine.GetOpen() != nil
}

// allowTicket counts a support message against limits.tickets_per_day per
// user and day (0: no limit). A limiter failure lets the message through.
func (h *Handler) allowTicket(r *req) bool {
	perDay := h.set.Int("limits.tickets_per_day", 10, 0, 100)
	if h.limiter == nil || perDay == 0 {
		return true
	}
	ok, err := h.limiter.Allow(r.ctx, ticketKey(r), 24*time.Hour, int(perDay))
	return err != nil || ok
}

func ticketKey(r *req) string { return "bot:tk:" + strconv.FormatInt(r.from.ID, 10) }

// giveTicket returns a counted support message that was not recorded.
func (h *Handler) giveTicket(r *req) {
	g, ok := h.limiter.(interface {
		Give(ctx context.Context, key string) error
	})
	if !ok {
		return
	}
	if err := g.Give(r.ctx, ticketKey(r)); err != nil {
		h.log.Warn("ticket limit give back", "err", err)
	}
}

// onMyChatMember records that a user blocked the bot (kicked) or started it
// again (member). It is never answered.
func (h *Handler) onMyChatMember(ctx context.Context, m *tg.ChatMemberUpdated) {
	if m.Chat.Type != "private" {
		return
	}
	var blocked bool
	switch m.NewChatMember.Status {
	case "kicked":
		blocked = true
	case "member":
	default:
		return
	}
	h.setBotBlocked(ctx, m.Chat.ID, blocked)
}

// setBotBlocked tells core whether a user blocked the bot (best effort).
func (h *Handler) setBotBlocked(ctx context.Context, telegramID int64, blocked bool) {
	if _, err := h.core.SetUserBotBlocked(ctx, &corev1.SetUserBotBlockedRequest{TelegramId: telegramID, Blocked: blocked}); err != nil &&
		status.Code(err) != codes.NotFound {
		h.log.Warn("bot blocked flag not saved", "chat", telegramID, "blocked", blocked, "err", err)
	}
}
