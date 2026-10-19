package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/notify"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	coredomain "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	corestore "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- what the dashboard does, through core's domain over the test store ---

func (w *world) dom() *coredomain.Service {
	return coredomain.New(w.stack.Store, coredomain.Config{OwnerTelegramIDs: []int64{ownerID}})
}

func (w *world) storeUser(telegramID int64) *corestore.User {
	w.t.Helper()
	u, err := w.stack.Store.GetUserByTelegramID(context.Background(), w.stack.Store.Conn(), telegramID)
	if err != nil {
		w.t.Fatal(err)
	}
	return u
}

// openTicket is the customer's ticket that is not closed.
func (w *world) openTicket(telegramID int64) *corestore.Ticket {
	w.t.Helper()
	t, err := w.stack.Store.OpenTicketOf(context.Background(), w.stack.Store.Conn(), w.storeUser(telegramID).ID)
	if err != nil {
		w.t.Fatalf("open ticket of %d: %v", telegramID, err)
	}
	return t
}

func (w *world) hasOpenTicket(telegramID int64) bool {
	_, err := w.stack.Store.OpenTicketOf(context.Background(), w.stack.Store.Conn(), w.storeUser(telegramID).ID)
	return err == nil
}

func (w *world) ticketMessages(ticketID string) []corestore.TicketMessage {
	w.t.Helper()
	ms, err := w.stack.Store.TicketMessages(context.Background(), w.stack.Store.Conn(), ticketID, true, 0)
	if err != nil {
		w.t.Fatal(err)
	}
	return ms
}

func (w *world) ticketMessage(id string) *corestore.TicketMessage {
	w.t.Helper()
	m, err := w.stack.Store.GetTicketMessage(context.Background(), w.stack.Store.Conn(), id)
	if err != nil {
		w.t.Fatal(err)
	}
	return m
}

// staffReply answers the customer's open ticket as the owner, as the
// dashboard does.
func (w *world) staffReply(customer int64, body string, closeIt bool) *corestore.TicketMessage {
	w.t.Helper()
	_, m, err := w.dom().ReplyTicket(context.Background(), w.storeUser(ownerID), w.openTicket(customer).ID, body, "reply", closeIt, "")
	if err != nil {
		w.t.Fatal(err)
	}
	return m
}

// closeTicket closes the customer's open ticket as the customer.
func (w *world) closeTicket(customer int64) {
	w.t.Helper()
	if _, err := w.dom().CloseMyTicket(context.Background(), w.storeUser(customer).ID, w.openTicket(customer).ID); err != nil {
		w.t.Fatal(err)
	}
}

// drain runs the notification feed until nothing is left.
func (w *world) drain() {
	w.t.Helper()
	for range 50 {
		n, err := w.feed.Step(context.Background())
		if err != nil {
			w.t.Fatalf("feed: %v", err)
		}
		if n == 0 {
			return
		}
	}
	w.t.Fatal("the feed never drained")
}

func (w *world) blockedAt(telegramID int64) bool { return w.storeUser(telegramID).BotBlockedAt != nil }

// --- more of what customers send ---

func (p *person) send(m *tg.Message) {
	m.MessageID, m.From, m.Chat = int(p.w.updateID()), p.from(), tg.Chat{ID: p.id, Type: "private"}
	p.w.h.Handle(context.Background(), tg.Update{UpdateID: p.w.updateID(), Message: m})
}

// replyTo answers one of the bot's messages with Telegram's reply.
func (p *person) replyTo(text string, messageID int) {
	p.send(&tg.Message{Text: text, ReplyToMessage: &tg.Message{MessageID: messageID, From: &tg.User{ID: 1000, IsBot: true},
		Chat: tg.Chat{ID: p.id, Type: "private"}}})
}

// album sends photos as one album (a caption on the first).
func (p *person) album(group string, fileIDs ...string) {
	for i, id := range fileIDs {
		m := &tg.Message{MediaGroupID: group, Photo: []tg.PhotoSize{{FileID: id, Width: 800, Height: 600, FileSize: 50_000}}}
		if i == 0 {
			m.Caption = "see these"
		}
		p.send(m)
	}
}

func (p *person) voice() {
	p.send(&tg.Message{Voice: &tg.Media{FileID: "voice-1", MimeType: "audio/ogg"}})
}

func (p *person) file(fileID, mime string, size int64) {
	p.send(&tg.Message{Document: &tg.Document{FileID: fileID, MimeType: mime, FileName: "file", FileSize: size}})
}

// button finds the newest button whose callback data starts with prefix.
func (p *person) button(prefix string) (tg.Button, *msg) {
	p.w.t.Helper()
	ms := p.w.tg.all(p.id)
	for i := len(ms) - 1; i >= 0; i-- {
		for _, b := range buttons(ms[i]) {
			if strings.HasPrefix(b.CallbackData, prefix) {
				return b, ms[i]
			}
		}
	}
	p.w.t.Fatalf("user %d: no button %q", p.id, prefix)
	return tg.Button{}, nil
}

func buttons(m *msg) []tg.Button {
	var out []tg.Button
	if m.kb != nil {
		for _, row := range m.kb.InlineKeyboard {
			out = append(out, row...)
		}
	}
	return out
}

// --- tests ---

// A new ticket under a topic alerts the staff through the feed; a
// follow-up joins it without another alert. Support staff are alerted only
// with notify.recipients = staff, and never about payments.
func TestNewTicketAlertsStaff(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	sup := w.customer(871, "sup")
	w.role(871, "support")
	u := w.customer(872, "cust")

	u.text("/menu")
	u.press("support")
	if labels := strings.Join(u.last().labels(), "|"); !strings.Contains(labels, "💳 Payment|📶 Service|💬 Other") {
		t.Fatalf("support screen: %s", labels)
	}
	u.press("tk:n:payment")
	u.lastHas("Write your message for support")
	if labels := strings.Join(u.last().labels(), "|"); !strings.Contains(labels, "✅ 💳 Payment") {
		t.Fatalf("the topic is not ticked: %s", labels)
	}
	u.text("my card was charged twice")
	tk := w.openTicket(872)
	u.lastHas(fmt.Sprintf("Your message was sent (ticket <b>#%d</b>)", tk.Number))
	if tk.Category != "payment" || tk.Subject != "my card was charged twice" {
		t.Fatalf("ticket: %+v", tk)
	}
	owner.eventuallySees(fmt.Sprintf("🆕 New ticket <b>#%d</b> from @cust · 💳 Payment\n\nmy card was charged twice", tk.Number))
	if buttons(owner.last()) != nil {
		t.Fatal("an Open button without a public https address")
	}

	u.text("it was on <Monday> & Tuesday")
	u.lastHas(fmt.Sprintf("Added to ticket <b>#%d</b>", tk.Number))
	w.drain()
	if n := len(w.ticketMessages(tk.ID)); n != 2 {
		t.Fatalf("%d messages in the ticket", n)
	}
	if owner.count("New ticket") != 1 || owner.count("wrote again") != 0 || sup.count("ticket") != 0 {
		t.Fatal("a follow-up before any answer was alerted, or support was alerted with recipients = owner")
	}

	owner.text("/set notify.recipients staff")
	w.closeTicket(872)
	u.text("/menu")
	u.press("support")
	u.lastHas("Write your message for support") // no open ticket: a new one
	u.text("another problem")
	sup.eventuallySees("New ticket <b>#")
	owner.eventuallySees("another problem")

	u.press("buy")
	u.press("plan:")
	u.press("pay:c:")
	u.photo("receipt-sup", "REF-9")
	u.lastHas("Received")
	owner.sees("New payment to review")
	if sup.count("New payment") != 0 {
		t.Fatal("support staff got a payment alert")
	}
}

// Photos, albums, PDFs: stored as files; what support does not take is
// refused; an album is confirmed once.
func TestTicketFiles(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(873, "pix")
	u.text("/menu")
	u.press("support")
	u.photo("screen-1", "")
	u.lastHas("Your message was sent")
	tk := w.openTicket(873)
	if ms := w.ticketMessages(tk.ID); len(ms) != 1 || ms[0].FileID != "screen-1" || ms[0].FileType != "photo" || ms[0].Body != "" {
		t.Fatalf("photo: %+v", ms)
	}
	owner.eventuallySees("📎")

	u.album("grp-1", "a1", "a2", "a3")
	if n := u.count("Added to ticket"); n != 1 {
		t.Fatalf("the album was confirmed %d times", n)
	}
	u.file("doc-pdf", "application/pdf", 2<<20)
	u.file("doc-png", "image/png", 300_000)
	if n := len(w.ticketMessages(tk.ID)); n != 6 {
		t.Fatalf("%d messages, want 6", n)
	}
	last := w.ticketMessages(tk.ID)[4]
	if last.FileID != "doc-pdf" || last.FileType != "document" || last.FileMime != "application/pdf" || last.FileSize != 2<<20 {
		t.Fatalf("pdf: %+v", last)
	}

	before := len(w.tg.all(873))
	u.voice()
	u.lastHas("Please send your message as text, a photo or a PDF")
	u.file("doc-zip", "application/zip", 1000)
	u.file("doc-big", "application/pdf", 11<<20)
	if n := len(w.ticketMessages(tk.ID)); n != 6 || len(w.tg.all(873)) != before+3 {
		t.Fatalf("refused content was recorded: %d messages", n)
	}
}

// A staff reply made in the dashboard reaches the customer with [Reply] and
// [Close]; pressing [Reply] keeps the reply in the chat, and the customer's
// next message joins the ticket (alerting the staff: it was answered).
func TestStaffReplyAndCustomerAnswers(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(874, "cust")
	u.text("/menu")
	u.press("support")
	u.text("the app does not connect")
	tk := w.openTicket(874)

	staff := w.staffReply(874, "Please restart <the app> & try again", false)
	want := fmt.Sprintf("💬 <b>Support</b> · ticket #%d\n\nPlease restart &lt;the app&gt; &amp; try again", tk.Number)
	u.eventuallySees(want)
	reply := u.last()
	if b := buttons(reply); len(b) != 2 || b[0].CallbackData != "tk:r:"+tk.ID || b[1].CallbackData != "tk:c:"+tk.ID {
		t.Fatalf("reply buttons: %+v", b)
	}
	if m := w.ticketMessage(staff.ID); m.Delivery != "sent" || m.TGMessageID == nil || *m.TGMessageID != int64(reply.id) {
		t.Fatalf("delivery: %+v", m)
	}

	u.press("tk:r:")
	if reply.text != want || buttons(reply) != nil {
		t.Fatalf("the reply was changed: %q %+v", reply.text, buttons(reply))
	}
	u.lastHas(fmt.Sprintf("Write your message for ticket <b>#%d</b>", tk.Number))
	u.text("it still fails")
	u.lastHas(fmt.Sprintf("Added to ticket <b>#%d</b>", tk.Number))
	owner.eventuallySees(fmt.Sprintf("💬 @cust wrote again on ticket <b>#%d</b>:\n\nit still fails", tk.Number))

	// Telegram's reply to the next answer joins the ticket too, outside any
	// scene; a reply to anything else is just a message.
	w.staffReply(874, "Try another server", false)
	u.eventuallySees("Try another server")
	second := u.last()
	u.text("/menu")
	u.replyTo("thanks, it works now", second.id)
	u.lastHas("Added to ticket")
	if ms := w.ticketMessages(tk.ID); len(ms) != 5 || ms[4].Body != "thanks, it works now" {
		t.Fatalf("messages: %d", len(ms))
	}
	u.text("/menu")
	u.replyTo("hello?", u.last().id) // the menu
	u.lastHas("Main menu")
	if n := len(w.ticketMessages(tk.ID)); n != 5 {
		t.Fatalf("a reply to the menu joined the ticket: %d", n)
	}

	// The customer closes it from the answer: a toast, the buttons go.
	u.press("tk:c:")
	u.toast("Your ticket is closed")
	if w.hasOpenTicket(874) || buttons(second) != nil || !strings.Contains(second.text, "Try another server") {
		t.Fatalf("close: open=%v buttons=%v", w.hasOpenTicket(874), buttons(second))
	}

	// Past tickets, read only.
	u.text("/menu")
	u.press("support")
	u.press("tk:l")
	u.lastHas("Your recent tickets")
	if b, _ := u.button("tk:v:"); !strings.Contains(b.Text, fmt.Sprintf("#%d · ", tk.Number)) || !strings.Contains(b.Text, "Closed ✅") {
		t.Fatalf("history item: %q", b.Text)
	}
	u.press("tk:v:")
	u.lastHas("thanks, it works now")
	u.lastHas("Closed ✅")

	// Answering the closed ticket's reply starts a new ticket.
	u.text("/menu")
	u.replyTo("one more question", second.id)
	nt := w.openTicket(874)
	u.lastHas(fmt.Sprintf("Ticket <b>#%d</b> was already closed, so your message started a new ticket: <b>#%d</b>", tk.Number, nt.Number))
}

// A reply that also closes the ticket offers [Support] (a new message, the
// reply stays); a close without a reply is announced.
func TestStaffClosesTickets(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(875, "cust")
	u.text("/menu")
	u.press("support")
	u.text("question one")
	tk := w.openTicket(875)
	w.staffReply(875, "Done, enjoy", true)
	u.eventuallySees("This ticket is now closed")
	reply := u.last()
	if b := buttons(reply); len(b) != 1 || b[0].CallbackData != "support:m" {
		t.Fatalf("closed reply buttons: %+v", b)
	}
	n := len(w.tg.all(875))
	u.press("support:m")
	if len(w.tg.all(875)) != n+1 || !strings.Contains(reply.text, "Done, enjoy") {
		t.Fatal("the Support button replaced the reply")
	}
	u.lastHas("Write your message for support")

	u.text("question two")
	tk2 := w.openTicket(875)
	if tk2.ID == tk.ID {
		t.Fatal("wrote into the closed ticket")
	}
	if _, err := w.dom().SetTicketStatus(context.Background(), w.storeUser(ownerID), tk2.ID, "closed", ""); err != nil {
		t.Fatal(err)
	}
	u.eventuallySees(fmt.Sprintf("✅ Ticket <b>#%d</b> was closed by support.", tk2.Number))
}

// A customer who blocked the bot: the reply is reported blocked and the user
// flagged; writing again clears the flag. A repeated event sends nothing;
// a failed send is retried.
func TestReplyDelivery(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(876, "gone")
	u.text("/menu")
	u.press("support")
	u.text("hello")
	w.tg.setBlocked(876, true)
	m := w.staffReply(876, "answer one", false)
	w.drain()
	if d := w.ticketMessage(m.ID).Delivery; d != "blocked" || !w.blockedAt(876) || u.count("answer one") != 0 {
		t.Fatalf("blocked: delivery %q, flag %v", d, w.blockedAt(876))
	}
	w.tg.setBlocked(876, false)
	u.text("/menu")
	if w.blockedAt(876) {
		t.Fatal("writing to the bot did not clear the flag")
	}

	// Telegram unreachable: the event is retried, then delivered once; the
	// same event again (a crash before the ack) sends nothing.
	m2 := w.staffReply(876, "answer two", false)
	payload, _ := json.Marshal(events.TicketRepliedEvent{TicketID: m2.TicketID, MessageID: m2.ID, TelegramID: 876})
	ev := eventbus.Message{EventID: "ev-1", Topic: events.TicketReplied, Payload: payload}
	w.tg.setDown(876, true)
	if err := w.notifier.Handle(context.Background(), ev); err == nil {
		t.Fatal("a failed send was not retried")
	}
	w.tg.setDown(876, false)
	for range 2 {
		if err := w.notifier.Handle(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	w.drain()
	if n := u.count("answer two"); n != 1 {
		t.Fatalf("the reply arrived %d times", n)
	}

	// Given up on (dead-lettered): reported failed, so the dashboard says so.
	m3 := w.staffReply(876, "answer three", false)
	payload, _ = json.Marshal(events.TicketRepliedEvent{TicketID: m3.TicketID, MessageID: m3.ID, TelegramID: 876})
	if err := w.notifier.DeadLetter(context.Background(), eventbus.Message{Topic: events.TicketReplied, Payload: payload}, fmt.Errorf("x")); err != nil {
		t.Fatal(err)
	}
	if d := w.ticketMessage(m3.ID).Delivery; d != "failed" {
		t.Fatalf("dead letter: %q", d)
	}
}

// A reply Telegram refuses (too long with a long text override) is reported
// failed at once: the notifications after it are not held up.
func TestRefusedReplyFailsAtOnce(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(882, "long")
	u.text("/menu")
	u.press("support")
	u.text("hello")
	w.drain()
	w.setInCore("texts.en.support.reply_closed", "💬 <b>Support</b> · ticket #{id}\n\n{text}\n\n"+strings.Repeat("Our hours are 9 to 5. ", 32))
	if err := w.h.RefreshSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	long := w.staffReply(882, strings.Repeat("a", 3500), true)
	u.text("/menu")
	u.press("support")
	u.text("one more question") // a new ticket
	w.staffReply(882, "a short answer", false)
	w.drain() // fails at an event the consumer would retry
	if d := w.ticketMessage(long.ID).Delivery; d != "failed" || u.count("Our hours") != 0 || u.count("a short answer") != 1 {
		t.Fatalf("long reply %q; the short one arrived %d times", d, u.count("a short answer"))
	}
}

// A reply that reached the customer is done even when core does not hear of
// it: a retry would send it again (core still says pending).
func TestReplySentButNotReported(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(883, "lost")
	u.text("/menu")
	u.press("support")
	u.text("hello")
	w.drain()

	core := &lossyCore{CoreServiceClient: w.stack.Core, fail: 1}
	n := notify.New(core, w.tg, w.h.cat, w.h.Settings(), []int64{ownerID}, nil)
	feed, err := eventbus.NewConsumer(eventbus.ConsumerConfig{Source: eventbus.NewGRPCSource(w.stack.Feed),
		Handle: n.Handle, DeadLetter: n.DeadLetter})
	if err != nil {
		t.Fatal(err)
	}
	w.staffReply(883, "answer once", false)
	var stepErr error
	for range 3 { // a failed event is tried again at the next step
		if _, err := feed.Step(context.Background()); err != nil {
			stepErr = err
		}
	}
	w.drain()
	if got := u.count("answer once"); got != 1 || stepErr != nil || core.failures() != 1 {
		t.Fatalf("the reply arrived %d times (feed: %v, failed reports: %d)", got, stepErr, core.failures())
	}

	// The ack is lost too, so the same event comes back: it is only reported.
	core.failNext(1)
	m := w.staffReply(883, "answer twice?", false)
	payload, err := json.Marshal(events.TicketRepliedEvent{TicketID: m.TicketID, MessageID: m.ID, TelegramID: 883})
	if err != nil {
		t.Fatal(err)
	}
	ev := eventbus.Message{EventID: "again-1", Topic: events.TicketReplied, Payload: payload}
	for range 2 {
		if err := n.Handle(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	if got, d := u.count("answer twice?"), w.ticketMessage(m.ID); got != 1 || d.Delivery != "sent" || d.TGMessageID == nil {
		t.Fatalf("the reply arrived %d times; delivery %q", got, d.Delivery)
	}
}

// lossyCore is core whose next reports of a delivery fail (core restarting).
type lossyCore struct {
	corev1.CoreServiceClient
	mu     sync.Mutex
	fail   int // reports still to fail
	failed int
}

func (c *lossyCore) ReportTicketDelivery(ctx context.Context, in *corev1.ReportTicketDeliveryRequest, opts ...grpc.CallOption) (*corev1.ReportTicketDeliveryResponse, error) {
	c.mu.Lock()
	if c.fail > 0 {
		c.fail--
		c.failed++
		c.mu.Unlock()
		return nil, status.Error(codes.Unavailable, "core is restarting")
	}
	c.mu.Unlock()
	return c.CoreServiceClient.ReportTicketDelivery(ctx, in, opts...)
}

func (c *lossyCore) failNext(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fail = n
}

func (c *lossyCore) failures() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed
}

// Maintenance and the forced join let a customer go on with their ticket,
// never start one.
func TestGatesLetATicketContinue(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	u := w.customer(877, "cont")
	n := w.customer(878, "newbie")
	u.text("/menu")
	u.press("support")
	u.text("before maintenance")
	w.staffReply(877, "an answer", false)
	u.eventuallySees("an answer")
	staffMsg := u.last()
	n.text("/menu")
	n.press("support") // the prompt, open before maintenance

	owner.text("/set maintenance.enabled true")
	owner.sees("Saved")
	u.press("tk:r:")
	u.lastHas("Write your message for ticket")
	u.text("during maintenance")
	u.lastHas("Added to ticket")
	if err := w.h.state.Clear(context.Background(), 877); err != nil {
		t.Fatal(err)
	}
	u.replyTo("by reply", staffMsg.id)
	u.lastHas("Added to ticket")
	if n := len(w.ticketMessages(w.openTicket(877).ID)); n != 4 {
		t.Fatalf("%d messages", n)
	}
	u.press("home")
	u.toast("under maintenance")

	n.text("a new problem")
	n.lastHas("under maintenance")
	if w.hasOpenTicket(878) {
		t.Fatal("a ticket was started during maintenance")
	}

	// Closed meanwhile: what the customer writes would start a ticket. (The
	// reply's buttons went with [Reply]; the scene still names the ticket.)
	w.closeTicket(877)
	u.text("in the scene")
	u.lastHas("under maintenance")
	if err := w.h.state.Clear(context.Background(), 877); err != nil {
		t.Fatal(err)
	}
	u.replyTo("again", staffMsg.id)
	if w.hasOpenTicket(877) {
		t.Fatal("a message to a closed ticket started one during maintenance")
	}

	owner.text("/set maintenance.enabled false")
	owner.text("/set join.channel @testchan")
	owner.sees("Saved")
	n.press("support")
	n.lastHas("Join our channel")
	w.tg.setMember(878, "member")
	n.press("jn")
	n.press("support")
	n.text("now a member")
	n.lastHas("Your message was sent")
}

// my_chat_member: the user blocked the bot (kicked) or came back (member);
// never answered.
func TestMyChatMemberFlipsTheFlag(t *testing.T) {
	w := newWorld(t)
	setupStore(t, w)
	u := w.customer(879, "flip")
	shown := len(w.tg.all(879))
	update := func(status string) {
		w.h.Handle(context.Background(), tg.Update{UpdateID: w.updateID(), MyChatMember: &tg.ChatMemberUpdated{
			Chat: tg.Chat{ID: 879, Type: "private"}, From: *u.from(),
			OldChatMember: tg.ChatMember{Status: "member"}, NewChatMember: tg.ChatMember{Status: status}}})
	}
	update("kicked")
	if !w.blockedAt(879) {
		t.Fatal("kicked: not flagged")
	}
	update("member")
	if w.blockedAt(879) {
		t.Fatal("member: still flagged")
	}
	if len(w.tg.all(879)) != shown {
		t.Fatal("my_chat_member was answered")
	}
}

// An alert links to the dashboard when core has a public https address; a
// reply on an assigned ticket alerts only the assignee.
func TestTicketAlertButtonAndAssignee(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	adm := w.person(880, "adm")
	adm.text("/start")
	adm.press("lang:fa")
	w.role(880, "admin")
	owner.text("/set notify.recipients staff")
	u := w.customer(881, "cust")
	u.text("/menu")
	u.press("support")
	u.text("first")
	tk := w.openTicket(881)

	core := &publicCore{CoreServiceClient: w.stack.Core, base: "https://panel.example.com"}
	n := notify.New(core, w.tg, w.h.cat, w.h.Settings(), []int64{ownerID}, nil)
	payload, _ := json.Marshal(events.TicketOpenedEvent{TicketID: tk.ID, Number: tk.Number,
		MessageID: w.ticketMessages(tk.ID)[0].ID, TelegramID: 881, Category: "other"})
	if err := n.Handle(context.Background(), eventbus.Message{Topic: events.TicketOpened, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if l := owner.link("Open in dashboard"); l != "https://panel.example.com/admin/support/"+tk.ID {
		t.Fatalf("dashboard link: %q", l)
	}
	w.drain()

	if _, err := w.dom().AssignTicket(context.Background(), w.storeUser(880), tk.ID, &w.storeUser(880).ID, false); err != nil {
		t.Fatal(err)
	}
	w.staffReply(881, "answer", false)
	u.eventuallySees("answer")
	before := owner.count("wrote again")
	u.text("thanks")
	adm.eventuallySees("@cust در تیکت")
	w.drain()
	if owner.count("wrote again") != before {
		t.Fatal("the owner was alerted about an assigned ticket")
	}
}

// publicCore is core with a public https address (GetTicketMessage gives
// the ticket's dashboard link).
type publicCore struct {
	corev1.CoreServiceClient
	base string
}

func (c *publicCore) GetTicketMessage(ctx context.Context, in *corev1.GetTicketMessageRequest, opts ...grpc.CallOption) (*corev1.TicketMessageView, error) {
	v, err := c.CoreServiceClient.GetTicketMessage(ctx, in, opts...)
	if err == nil {
		v.Url = c.base + "/admin/support/" + v.GetTicketId()
	}
	return v, err
}
