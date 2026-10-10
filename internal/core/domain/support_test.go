package domain

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

func staffMember(t *testing.T, s *store.Store, tg int64, role string) *store.User {
	t.Helper()
	u := seedUser(t, s, tg)
	if err := s.SetUserRole(context.Background(), s.Conn(), u.ID, role, "active"); err != nil {
		t.Fatal(err)
	}
	u.Role = role
	return u
}

// A customer's conversation: one open ticket, alerts only when it starts
// and when the customer writes after an answer, repeats harmless.
func TestTicketConversation(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	c := seedUser(t, s, 7101)
	sup := staffMember(t, s, 7102, "support")

	r1, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Category: "payment", Text: "I paid\nbut nothing", TGMessageID: 10})
	if err != nil || !r1.NewTicket || !r1.Created || r1.Ticket.Number == 0 || r1.Ticket.Subject != "I paid" {
		t.Fatalf("first message: %+v %v", r1, err)
	}
	r2, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Text: "", FileID: "photo-1", FileType: "photo", TGMessageID: 11})
	if err != nil || r2.NewTicket || r2.Ticket.ID != r1.Ticket.ID {
		t.Fatalf("follow-up: %+v %v", r2, err)
	}
	again, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Text: "I paid", TGMessageID: 10})
	if err != nil || again.Created || again.MessageID != r1.MessageID {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	if n := len(outboxEvents(t, s, events.TicketOpened)); n != 1 {
		t.Fatalf("opened events: %d", n)
	}

	// Staff answer: the ticket is answered and given to the replier.
	tk, m, err := svc.ReplyTicket(ctx, sup, r1.Ticket.ID, "  We are checking.  ", "reply", false, "k1")
	if err != nil || tk.Status != "answered" || tk.AssignedTo == nil || *tk.AssignedTo != sup.ID || m.Delivery != "pending" {
		t.Fatalf("reply: %+v %+v %v", tk, m, err)
	}
	if _, m2, err := svc.ReplyTicket(ctx, sup, r1.Ticket.ID, "We are checking.", "reply", false, "k1"); err != nil || m2.ID != m.ID {
		t.Fatalf("a repeated request made a second reply: %v %v", m2, err)
	}
	if _, _, err := svc.ReplyTicket(ctx, sup, r1.Ticket.ID, "internal", "note", false, ""); err != nil {
		t.Fatal(err)
	}
	if n := len(outboxEvents(t, s, events.TicketReplied)); n != 1 {
		t.Fatalf("replied events: %d (notes and repeats publish nothing)", n)
	}
	// The bot delivers it and records the Telegram message; the customer
	// answers it with a reply-to: back to waiting, the assignee is alerted.
	if err := svc.ReportTicketDelivery(ctx, m.ID, "sent", 555); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReportTicketDelivery(ctx, m.ID, "failed", 0); err != nil { // a late repeat changes nothing
		t.Fatal(err)
	}
	v, _ := svc.TicketMessageView(ctx, m.ID)
	if v.Message.Delivery != "sent" || v.TelegramID != 7101 {
		t.Fatalf("delivery: %+v", v.Message)
	}
	r3, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, ReplyToTGID: 555, Text: "Thanks, still waiting", TGMessageID: 12})
	if err != nil || r3.Ticket.ID != r1.Ticket.ID {
		t.Fatalf("reply-to: %+v %v", r3, err)
	}
	if ev := outboxEvents(t, s, events.TicketCustomerReplied); len(ev) != 1 || !strings.Contains(string(ev[0]), `"assignee_telegram_id": 7102`) {
		t.Fatalf("customer replied: %s", ev)
	}
	// Notes are never shown to the customer.
	_, msgs, err := svc.MyTicket(ctx, c.ID, r1.Ticket.ID, 20)
	if err != nil || len(msgs) != 4 {
		t.Fatalf("customer's view: %d %v", len(msgs), err)
	}
	for _, m := range msgs {
		if m.Kind == "note" {
			t.Fatal("a note reached the customer")
		}
	}

	// Closing and answering the closed ticket starts a new one.
	if _, err := svc.SetTicketStatus(ctx, sup, r1.Ticket.ID, "closed", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ReplyTicket(ctx, sup, r1.Ticket.ID, "x", "reply", false, ""); !errors.Is(err, ErrState) {
		t.Fatalf("reply to a closed ticket: %v", err)
	}
	r4, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, TicketID: r1.Ticket.ID, Text: "one more thing", TGMessageID: 13})
	if err != nil || !r4.NewTicket || r4.ClosedNumber != r1.Ticket.Number {
		t.Fatalf("after close: %+v %v", r4, err)
	}
	if _, err := svc.SetTicketStatus(ctx, sup, r1.Ticket.ID, "open", ""); !errors.Is(err, ErrState) {
		t.Fatalf("reopen beside another open ticket: %v", err)
	}
	// Someone else's ticket is not theirs to write to.
	other := seedUser(t, s, 7103)
	if _, err := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: other.ID, TicketID: r4.Ticket.ID, Text: "x", TGMessageID: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign ticket: %v", err)
	}
	// Support may take a ticket but not hand it to someone else.
	adm := staffMember(t, s, 7104, "admin")
	if _, err := svc.AssignTicket(ctx, sup, r4.Ticket.ID, &adm.ID, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("support assigned someone else: %v", err)
	}
	if _, err := svc.AssignTicket(ctx, sup, r4.Ticket.ID, &other.ID, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("assigned a customer: %v", err)
	}
	if tk, err := svc.AssignTicket(ctx, sup, r4.Ticket.ID, &sup.ID, false); err != nil || *tk.AssignedTo != sup.ID {
		t.Fatalf("take: %v", err)
	}
	if _, _, err := svc.ReplyTicket(ctx, sup, r4.Ticket.ID, strings.Repeat("😀", MaxStaffReply/2+1), "reply", false, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too long a reply: %v", err)
	}
}

// Two first messages at once still make one ticket.
func TestFirstMessagesAtOnce(t *testing.T) {
	svc, s := testService(t)
	c := seedUser(t, s, 7111)
	var wg sync.WaitGroup
	ids := make([]string, 4)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := svc.AddCustomerMessage(context.Background(), CustomerMessage{UserID: c.ID, Text: "hi", TGMessageID: int64(100 + i)})
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = r.Ticket.ID
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("two tickets: %v", ids)
		}
	}
}

func TestTicketAutoClose(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	c := seedUser(t, s, 7121)
	sup := staffMember(t, s, 7122, "support")
	r, _ := svc.AddCustomerMessage(ctx, CustomerMessage{UserID: c.ID, Text: "hi"})
	if _, _, err := svc.ReplyTicket(ctx, sup, r.Ticket.ID, "answer", "reply", false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(ctx, `UPDATE core.tickets SET last_staff_at = now() - interval '8 days' WHERE id = $1`, r.Ticket.ID); err != nil {
		t.Fatal(err)
	}
	w := svc.NewTicketWorker(nil)
	if n, err := w.Step(ctx); err != nil || n != 1 {
		t.Fatalf("auto-close: %d %v", n, err)
	}
	tk, _ := s.GetTicket(ctx, s.Conn(), r.Ticket.ID, false)
	if tk.Status != "closed" || tk.CloseReason != "auto" || tk.ClosedBy != nil {
		t.Fatalf("closed ticket: %+v", tk)
	}
}

// A broadcast reaches its audience at most once: claims, results, blocked
// users, halts and the end.
func TestBroadcastLifecycle(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner := staffMember(t, s, 7201, "owner")
	for _, tg := range []int64{7202, 7203, 7204} {
		seedUser(t, s, tg)
	}
	banned := seedUser(t, s, 7205)
	if err := s.SetUserStatus(ctx, s.Conn(), banned.ID, "banned"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetUserBotBlocked(ctx, 7204, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateBroadcast(ctx, owner, BroadcastInput{Segment: "all", Texts: map[string]string{"fa": "سلام"}}); err == nil {
		t.Fatal("a broadcast to everyone without an English text was accepted")
	}
	b, err := svc.CreateBroadcast(ctx, owner, BroadcastInput{Segment: "all", Texts: map[string]string{"fa": "<b>سلام</b>", "en": "Hi"},
		ButtonURL: "https://example.com", ButtonLabels: map[string]string{"fa": "باز کن", "en": "Open"}})
	if err != nil || b.Status != "draft" {
		t.Fatalf("create: %+v %v", b, err)
	}
	if total, _, _, _ := svc.BroadcastAudience(ctx, "all", ""); total != 3 { // the owner and two customers
		t.Fatalf("audience: %d", total)
	}
	if _, err := svc.SendBroadcast(ctx, owner, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	w := svc.NewBroadcastWorker(nil)
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ = s.GetBroadcast(ctx, s.Conn(), b.ID, false)
	if b.Status != "running" || b.Total != 3 {
		t.Fatalf("started: %+v", b)
	}
	batch, err := svc.ClaimBroadcastBatch(ctx, 2)
	if err != nil || batch == nil || len(batch.Recipients) != 2 || batch.Broadcast.ButtonLabels["en"] != "Open" {
		t.Fatalf("claim: %+v %v", batch, err)
	}
	r := batch.Recipients
	st, err := svc.ReportBroadcastResults(ctx, b.ID, []store.Result{
		{UserID: r[0].UserID, Outcome: "sent", MessageID: 9}, {UserID: r[1].UserID, Outcome: "blocked"}}, "")
	if err != nil || st != "running" {
		t.Fatalf("report: %s %v", st, err)
	}
	// A repeated report changes nothing.
	if _, err := svc.ReportBroadcastResults(ctx, b.ID, []store.Result{{UserID: r[0].UserID, Outcome: "sent"}}, ""); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.GetUser(ctx, s.Conn(), r[1].UserID); u.BotBlockedAt == nil {
		t.Fatal("a blocked recipient was not marked")
	}
	// Pause: the bot gives back what it holds; nothing more is claimed.
	last, _ := svc.ClaimBroadcastBatch(ctx, 5)
	if _, err := svc.PauseBroadcast(ctx, owner, b.ID); err != nil {
		t.Fatal(err)
	}
	st, _ = svc.ReportBroadcastResults(ctx, b.ID, []store.Result{{UserID: last.Recipients[0].UserID, Outcome: "released"}}, "")
	if st != "paused" {
		t.Fatalf("status after pause: %s", st)
	}
	if none, _ := svc.ClaimBroadcastBatch(ctx, 5); none != nil {
		t.Fatal("a paused broadcast was claimed")
	}
	if _, err := svc.ResumeBroadcast(ctx, owner, b.ID); err != nil {
		t.Fatal(err)
	}
	last, _ = svc.ClaimBroadcastBatch(ctx, 5)
	if last == nil || len(last.Recipients) != 1 {
		t.Fatalf("released recipient not back in line: %+v", last)
	}
	// The bot halts it (Telegram refuses the button): paused with the reason.
	st, _ = svc.ReportBroadcastResults(ctx, b.ID, []store.Result{{UserID: last.Recipients[0].UserID, Outcome: "failed", Error: "Bad Request: BUTTON_URL_INVALID"}}, "Telegram refuses the button")
	b, _ = s.GetBroadcast(ctx, s.Conn(), b.ID, false)
	if st != "paused" || b.LastError == "" || b.Sent != 1 || b.Blocked != 1 || b.Failed != 1 || b.Pending != 0 {
		t.Fatalf("after halt: %s %+v", st, b)
	}
	if _, err := svc.ResumeBroadcast(ctx, owner, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ = s.GetBroadcast(ctx, s.Conn(), b.ID, false); b.Status != "done" || b.FinishedAt == nil {
		t.Fatalf("not done: %+v", b)
	}
	// The blocked user is left out next time.
	if total, _, _, _ := svc.BroadcastAudience(ctx, "all", ""); total != 2 {
		t.Fatalf("audience after a block: %d", total)
	}
}

// Claims the bot never reports are given up on (never sent twice); a
// cancelled broadcast takes back what waits in line.
func TestBroadcastLeaseAndCancel(t *testing.T) {
	svc, s := testService(t)
	ctx := context.Background()
	owner := staffMember(t, s, 7301, "owner")
	for _, tg := range []int64{7302, 7303, 7304} {
		seedUser(t, s, tg)
	}
	b, _ := svc.CreateBroadcast(ctx, owner, BroadcastInput{Segment: "all", Language: "fa", Texts: map[string]string{"fa": "x"}})
	at := time.Now().Add(time.Hour)
	if _, err := svc.SendBroadcast(ctx, owner, b.ID, &at); err != nil {
		t.Fatal(err)
	}
	w := svc.NewBroadcastWorker(nil)
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ = s.GetBroadcast(ctx, s.Conn(), b.ID, false); b.Status != "scheduled" {
		t.Fatalf("started before its time: %s", b.Status)
	}
	w.now = func() time.Time { return at.Add(time.Second) }
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	batch, _ := svc.ClaimBroadcastBatch(ctx, 1)
	if _, err := s.DB().Exec(ctx, `UPDATE core.broadcast_recipients SET claimed_at = now() - interval '11 minutes' WHERE status = 'claimed'`); err != nil {
		t.Fatal(err)
	}
	if err := w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ = s.GetBroadcast(ctx, s.Conn(), b.ID, false); b.Unknown != 1 {
		t.Fatalf("expired claim: %+v", b)
	}
	// A late report for the given-up recipient changes nothing.
	_, _ = svc.ReportBroadcastResults(ctx, b.ID, []store.Result{{UserID: batch.Recipients[0].UserID, Outcome: "sent"}}, "")
	if b, _ = s.GetBroadcast(ctx, s.Conn(), b.ID, false); b.Sent != 0 {
		t.Fatalf("late report counted: %+v", b)
	}
	held, _ := svc.ClaimBroadcastBatch(ctx, 1)
	if _, err := svc.CancelBroadcast(ctx, owner, b.ID); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.ReportBroadcastResults(ctx, b.ID, []store.Result{{UserID: held.Recipients[0].UserID, Outcome: "released"}}, "")
	if b, _ = s.GetBroadcast(ctx, s.Conn(), b.ID, false); st != "cancelled" || b.Pending != 0 {
		t.Fatalf("cancel: %s %+v", st, b)
	}
	if err := svc.DeleteBroadcast(ctx, owner, b.ID); !errors.Is(err, ErrState) {
		t.Fatalf("deleted a sent broadcast: %v", err)
	}
}
