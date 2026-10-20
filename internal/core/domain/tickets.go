package domain

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
)

// Ticket limits.
const (
	// MaxStaffReply leaves room for the reply's frame in a 4096-unit message.
	MaxStaffReply    = 3500
	maxCustomerChars = 4096
	maxSubjectRunes  = 100
)

// CategoryReseller is the category of a reseller application: a customer's
// request to become a reseller, which the bot's Become a reseller button files
// as a ticket of its own (see AddCustomerMessage).
const CategoryReseller = "reseller"

// TicketCategories are the categories a ticket can have; "general" is what
// older tickets have.
var TicketCategories = []string{"payment", "service", "other", CategoryReseller}

// SupportCategories are the topics a customer picks from when they write to
// support, in the bot and in the Mini App: a reseller application is filed by
// its own button, not picked.
var SupportCategories = []string{"payment", "service", "other"}

// The refusals of a reseller application that the bot words for the customer
// (they are part of the error text, like the other states the bot reads).
const (
	// ApplicationOpen: the customer has an application waiting already.
	ApplicationOpen = "a reseller application is open already"
	// ConversationOpen: another conversation with support is open: an
	// application is a ticket of its own, and a customer has one open ticket at
	// a time.
	ConversationOpen = "another support conversation is open"
)

// CustomerMessage is a customer's message for support (from the bot).
type CustomerMessage struct {
	UserID      string
	Category    string
	TicketID    string // the ticket the customer answers with a button
	ReplyToTGID int64  // the delivered staff reply they answered in the chat
	Text        string
	FileID      string
	FileType    string // photo | document
	FileMime    string
	FileName    string
	FileSize    int64
	TGMessageID int64 // makes a repeat harmless
}

// CustomerMessageResult says where the message went.
type CustomerMessageResult struct {
	Ticket       *store.Ticket
	MessageID    string
	Created      bool  // false: this Telegram message was recorded already
	NewTicket    bool  // the message started a ticket
	ClosedNumber int64 // the closed ticket the customer answered
}

func subjectOf(text, fileName string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		line = "📎 " + fileName
	}
	if r := []rune(line); len(r) > maxSubjectRunes {
		line = string(r[:maxSubjectRunes-1]) + "…"
	}
	return line
}

// RepliedTicket is the ticket of the customer's delivered staff reply with
// this Telegram message id (store.ErrNotFound when it is not one). Nothing is
// saved: the bot asks before it counts a reply against the daily limit.
func (s *Service) RepliedTicket(ctx context.Context, userID string, tgMessageID int64) (*store.Ticket, error) {
	if tgMessageID <= 0 {
		return nil, invalid("the replied message is missing")
	}
	return s.st.TicketByDelivered(ctx, s.st.Conn(), userID, tgMessageID)
}

// AddCustomerMessage records a customer's message in their open ticket, or
// starts one. Staff are alerted when a ticket starts and when the customer
// writes again after an answer (not for every message).
func (s *Service) AddCustomerMessage(ctx context.Context, p CustomerMessage) (*CustomerMessageResult, error) {
	p.Text = strings.TrimSpace(strings.ToValidUTF8(p.Text, ""))
	if p.Text == "" && p.FileID == "" {
		return nil, invalid("a message needs text or a file")
	}
	if utf8.RuneCountInString(p.Text) > maxCustomerChars {
		return nil, invalid("the message is too long")
	}
	if p.FileID != "" && p.FileType != "photo" && p.FileType != "document" {
		return nil, invalid("file type must be photo or document")
	}
	if !slices.Contains(TicketCategories, p.Category) {
		p.Category = "other"
	}
	// A reseller application is a conversation of its own: a new ticket, never
	// an answer in another one, and only from a customer who may apply.
	application := p.Category == CategoryReseller
	if application && (p.TicketID != "" || p.ReplyToTGID > 0) {
		return nil, invalid("a reseller application starts a ticket of its own")
	}
	u, err := s.st.GetUser(ctx, s.st.Conn(), p.UserID)
	if err != nil {
		return nil, err // banned customers may write: it is how they appeal
	}
	if application {
		if err := s.requireApplicant(ctx, u); err != nil {
			return nil, err
		}
	}
	var key *string
	if p.TGMessageID > 0 {
		k := "tg:" + strconv.FormatInt(u.TelegramID, 10) + ":" + strconv.FormatInt(p.TGMessageID, 10)
		key = &k
	}
	var res *CustomerMessageResult
	for attempt := 0; ; attempt++ {
		err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
			var e error
			res, e = s.addCustomerMessage(ctx, tx, u, p, key)
			return e
		})
		// Two first messages at once: the other one started the ticket.
		if errors.Is(err, store.ErrTicketOpen) && attempt < 2 {
			continue
		}
		break
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Service) addCustomerMessage(ctx context.Context, tx pgx.Tx, u *store.User, p CustomerMessage, key *string) (*CustomerMessageResult, error) {
	res := &CustomerMessageResult{}
	if key != nil {
		if m, err := s.st.TicketMessageByKey(ctx, tx, *key); err == nil {
			t, err := s.st.GetTicket(ctx, tx, m.TicketID, false)
			if err != nil {
				return nil, err
			}
			res.Ticket, res.MessageID = t, m.ID
			return res, nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	var t *store.Ticket
	var err error
	switch {
	case p.ReplyToTGID > 0:
		t, err = s.st.TicketByDelivered(ctx, tx, u.ID, p.ReplyToTGID)
	case p.TicketID != "":
		t, err = s.st.GetTicket(ctx, tx, p.TicketID, true)
		if err == nil && t.UserID != u.ID {
			t, err = nil, store.ErrNotFound
		}
	}
	if err != nil {
		return nil, err
	}
	if t != nil && t.Status == "closed" {
		res.ClosedNumber = t.Number // answering a closed ticket starts a new one
		t = nil
	}
	if t == nil {
		if t, err = s.st.OpenTicketOf(ctx, tx, u.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		if t != nil && p.Category == CategoryReseller {
			// One open ticket at a time: an application neither joins the open
			// conversation (it would not be an application any more) nor
			// starts a second one.
			if t.Category == CategoryReseller {
				return nil, stateErr("%s", ApplicationOpen)
			}
			return nil, stateErr("%s", ConversationOpen)
		}
	}
	if t == nil {
		t = &store.Ticket{UserID: u.ID, Category: p.Category, Subject: subjectOf(p.Text, p.FileName)}
		if err := s.st.InsertTicket(ctx, tx, t); err != nil {
			return nil, err
		}
		res.NewTicket = true
	}
	tg := p.TGMessageID
	m := &store.TicketMessage{TicketID: t.ID, Kind: "customer", AuthorID: &u.ID, Body: p.Text, FileID: p.FileID,
		FileType: p.FileType, FileMime: p.FileMime, FileName: p.FileName, FileSize: p.FileSize, IdempotencyKey: key}
	if tg > 0 {
		m.TGMessageID = &tg
	}
	m, created, err := s.st.AddTicketMessage(ctx, tx, m)
	if err != nil {
		return nil, err
	}
	res.Ticket, res.MessageID, res.Created = t, m.ID, created
	if !created {
		return res, nil
	}
	prev := t.Status
	if err := s.st.CustomerWrote(ctx, tx, t.ID); err != nil {
		return nil, err
	}
	t.Status = "open"
	switch {
	case res.NewTicket:
		err = s.publish(ctx, tx, events.TicketOpened, events.TicketOpenedEvent{TicketID: t.ID, Number: t.Number,
			MessageID: m.ID, UserID: u.ID, TelegramID: u.TelegramID, Category: t.Category})
	case prev == "answered":
		e := events.TicketCustomerRepliedEvent{TicketID: t.ID, Number: t.Number, MessageID: m.ID, UserID: u.ID, TelegramID: u.TelegramID}
		if t.AssignedTo != nil {
			if a, aerr := s.st.GetUser(ctx, tx, *t.AssignedTo); aerr == nil && a.Status == "active" && IsStaffRole(a.Role) {
				e.AssigneeTelegramID = a.TelegramID
			}
		}
		err = s.publish(ctx, tx, events.TicketCustomerReplied, e)
	}
	return res, err
}

// requireApplicant: only an active customer, one who is not staff and not a
// reseller already, may apply to become a reseller, and only while the license
// in effect includes resellers (the refusal that names the license is for
// staff: the bot tells a customer only that this is not available).
func (s *Service) requireApplicant(ctx context.Context, u *store.User) error {
	switch {
	case u.Status != "active":
		return ErrUserBanned
	case u.Role != "user":
		return stateErr("only a customer can apply to become a reseller")
	}
	return s.requireResellers(ctx)
}

// MyTickets is the customer's open ticket (if any) and their recent ones.
func (s *Service) MyTickets(ctx context.Context, userID string, limit int) (*store.Ticket, []store.Ticket, error) {
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	list, err := s.st.UserTickets(ctx, s.st.Conn(), userID, limit)
	if err != nil {
		return nil, nil, err
	}
	var open *store.Ticket
	for i := range list {
		if list[i].Status != "closed" {
			open = &list[i]
			break
		}
	}
	if open == nil {
		// The open one may be older than the recent ones shown.
		if t, err := s.st.OpenTicketOf(ctx, s.st.Conn(), userID); err == nil {
			open = t
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, nil, err
		}
	}
	return open, list, nil
}

// MyTicket is one of the customer's tickets with its last messages (never
// the staff's notes).
func (s *Service) MyTicket(ctx context.Context, userID, ticketID string, last int) (*store.Ticket, []store.TicketMessage, error) {
	if last <= 0 || last > 20 {
		last = 3
	}
	t, err := s.st.GetTicket(ctx, s.st.Conn(), ticketID, false)
	if err != nil {
		return nil, nil, err
	}
	if t.UserID != userID {
		return nil, nil, store.ErrNotFound
	}
	msgs, err := s.st.TicketMessages(ctx, s.st.Conn(), t.ID, false, last)
	return t, msgs, err
}

// CloseMyTicket lets the customer close their ticket (no message is sent).
func (s *Service) CloseMyTicket(ctx context.Context, userID, ticketID string) (*store.Ticket, error) {
	var t *store.Ticket
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		if t, err = s.st.GetTicket(ctx, tx, ticketID, true); err != nil {
			return err
		}
		if t.UserID != userID {
			return store.ErrNotFound
		}
		if _, err := s.st.CloseTicket(ctx, tx, t.ID, &userID, "customer"); err != nil {
			return err
		}
		t.Status = "closed"
		return nil
	})
	return t, err
}

// TicketMessageView is a message with what the bot needs to deliver it.
type TicketMessageView struct {
	Message    *store.TicketMessage
	Ticket     *store.Ticket
	TelegramID int64
	Language   string
	Username   string
}

// TicketMessageView loads a message for the notifier.
func (s *Service) TicketMessageView(ctx context.Context, messageID string) (*TicketMessageView, error) {
	m, err := s.st.GetTicketMessage(ctx, s.st.Conn(), messageID)
	if err != nil {
		return nil, err
	}
	t, err := s.st.GetTicket(ctx, s.st.Conn(), m.TicketID, false)
	if err != nil {
		return nil, err
	}
	u, err := s.st.GetUser(ctx, s.st.Conn(), t.UserID)
	if err != nil {
		return nil, err
	}
	return &TicketMessageView{Message: m, Ticket: t, TelegramID: u.TelegramID, Language: u.Language, Username: u.Username}, nil
}

// ReportTicketDelivery records the bot's delivery of a staff reply; a user
// who blocked the bot is marked.
func (s *Service) ReportTicketDelivery(ctx context.Context, messageID, status string, tgMessageID int64) error {
	switch status {
	case "sent", "blocked", "failed":
	default:
		return invalid("delivery status must be sent, blocked or failed")
	}
	var tg *int64
	if tgMessageID > 0 {
		tg = &tgMessageID
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		changed, err := s.st.RecordTicketDelivery(ctx, tx, messageID, status, tg)
		if err != nil || !changed || status != "blocked" {
			return err
		}
		v, err := s.TicketMessageView(ctx, messageID)
		if err != nil {
			return err
		}
		_, err = s.st.SetBotBlocked(ctx, tx, v.TelegramID, true)
		return err
	})
}

// SetUserBotBlocked records that a user blocked the bot, or came back.
func (s *Service) SetUserBotBlocked(ctx context.Context, telegramID int64, blocked bool) error {
	_, err := s.st.SetBotBlocked(ctx, s.st.Conn(), telegramID, blocked)
	return err
}

// staffBody checks a staff reply or note: one block of text, not empty,
// short enough for one Telegram message with its frame.
func staffBody(body string) (string, error) {
	body = i18n.NormalizeText(strings.ToValidUTF8(body, ""))
	if body == "" {
		return "", &FieldError{"body", "write something"}
	}
	for _, r := range body {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return "", &FieldError{"body", "contains a control character"}
		}
	}
	if len(utf16.Encode([]rune(body))) > MaxStaffReply {
		return "", &FieldError{"body", "at most " + strconv.Itoa(MaxStaffReply) + " characters"}
	}
	return body, nil
}

// requireTeam checks the actor is active staff (owner, admin or support).
func requireTeam(actor *store.User) error {
	if actor == nil || actor.Status != "active" || !IsStaffRole(actor.Role) {
		return ErrForbidden
	}
	return nil
}

// ReplyTicket adds a staff reply (sent to the customer through the bot) or an
// internal note. A reply may also close the ticket; it gives an unassigned
// ticket to the replier. key (from the dashboard) makes a retry harmless.
func (s *Service) ReplyTicket(ctx context.Context, actor *store.User, ticketID, body, kind string, closeIt bool, key string) (*store.Ticket, *store.TicketMessage, error) {
	if err := requireTeam(actor); err != nil {
		return nil, nil, err
	}
	if kind != "reply" && kind != "note" {
		return nil, nil, invalid("kind must be reply or note")
	}
	body, err := staffBody(body)
	if err != nil {
		return nil, nil, err
	}
	var idem *string
	if key != "" {
		k := "web:" + actor.ID + ":" + key
		idem = &k
	}
	var (
		t *store.Ticket
		m *store.TicketMessage
	)
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var err error
		if t, err = s.st.GetTicket(ctx, tx, ticketID, true); err != nil {
			return err
		}
		if idem != nil {
			if prev, err := s.st.TicketMessageByKey(ctx, tx, *idem); err == nil {
				m = prev // the same request again
				return nil
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		if t.Status == "closed" {
			return stateErr("the ticket is closed; reopen it to write")
		}
		msg := &store.TicketMessage{TicketID: t.ID, Kind: "note", AuthorID: &actor.ID, Body: body, IdempotencyKey: idem}
		if kind == "reply" {
			msg.Kind, msg.Delivery = "staff", "pending"
		}
		var created bool
		if m, created, err = s.st.AddTicketMessage(ctx, tx, msg); err != nil || !created {
			return err
		}
		if kind == "note" {
			if err := s.st.TouchTicket(ctx, tx, t.ID); err != nil {
				return err
			}
			return s.audit(ctx, tx, actor, "ticket.note", "ticket", t.ID, map[string]any{"message_id": m.ID}, "")
		}
		if err := s.st.StaffReplied(ctx, tx, t.ID, actor.ID, closeIt); err != nil {
			return err
		}
		u, err := s.st.GetUser(ctx, tx, t.UserID)
		if err != nil {
			return err
		}
		if err := s.audit(ctx, tx, actor, "ticket.reply", "ticket", t.ID,
			map[string]any{"message_id": m.ID, "chars": len(utf16.Encode([]rune(body))), "closed": closeIt}, ""); err != nil {
			return err
		}
		return s.publish(ctx, tx, events.TicketReplied, events.TicketRepliedEvent{TicketID: t.ID, Number: t.Number,
			MessageID: m.ID, TelegramID: u.TelegramID, Closed: closeIt})
	})
	if err != nil {
		return nil, nil, err
	}
	t, err = s.st.GetTicket(ctx, s.st.Conn(), ticketID, false)
	return t, m, err
}

// SetTicketStatus closes a ticket (the customer is told) or opens it again.
func (s *Service) SetTicketStatus(ctx context.Context, actor *store.User, ticketID, status, reason string) (*store.Ticket, error) {
	if err := requireTeam(actor); err != nil {
		return nil, err
	}
	if status != "closed" && status != "open" {
		return nil, invalid("status must be closed or open")
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 500 {
		return nil, invalid("the reason is too long (at most 500 characters)")
	}
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		t, err := s.st.GetTicket(ctx, tx, ticketID, true)
		if err != nil {
			return err
		}
		before := t.Status
		if status == "closed" {
			if before == "closed" {
				return stateErr("the ticket is closed already")
			}
			if _, err := s.st.CloseTicket(ctx, tx, t.ID, &actor.ID, "staff"); err != nil {
				return err
			}
			u, err := s.st.GetUser(ctx, tx, t.UserID)
			if err != nil {
				return err
			}
			if err := s.publish(ctx, tx, events.TicketClosed, events.TicketClosedEvent{TicketID: t.ID, Number: t.Number,
				TelegramID: u.TelegramID}); err != nil {
				return err
			}
		} else {
			if before != "closed" {
				return stateErr("the ticket is open already")
			}
			if err := s.st.ReopenTicket(ctx, tx, t.ID); errors.Is(err, store.ErrTicketOpen) {
				return stateErr("the customer has another open ticket; answer that one")
			} else if err != nil {
				return err
			}
		}
		return s.auditChange(ctx, tx, actor, "ticket.status", "ticket", t.ID,
			map[string]string{"status": before}, map[string]string{"status": status}, reason)
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetTicket(ctx, s.st.Conn(), ticketID, false)
}

// AssignTicket gives a ticket to a staff member, or to nobody (nil). Giving
// it to someone else needs canManage (tickets.manage).
func (s *Service) AssignTicket(ctx context.Context, actor *store.User, ticketID string, assignee *string, canManage bool) (*store.Ticket, error) {
	if err := requireTeam(actor); err != nil {
		return nil, err
	}
	if assignee != nil && *assignee != actor.ID && !canManage {
		return nil, ErrForbidden
	}
	if assignee != nil {
		a, err := s.st.GetUser(ctx, s.st.Conn(), *assignee)
		if errors.Is(err, store.ErrNotFound) || err == nil && (a.Status != "active" || !IsStaffRole(a.Role)) {
			return nil, invalid("tickets can only be given to active staff")
		} else if err != nil {
			return nil, err
		}
	}
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		t, err := s.st.GetTicket(ctx, tx, ticketID, true)
		if err != nil {
			return err
		}
		if !canManage && t.AssignedTo != nil && *t.AssignedTo != actor.ID {
			return ErrForbidden // taking a ticket from someone else is a manager's call
		}
		if err := s.st.AssignTicket(ctx, tx, t.ID, assignee); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "ticket.assign", "ticket", t.ID,
			map[string]any{"assignee": t.AssignedTo}, map[string]any{"assignee": assignee}, "")
	})
	if err != nil {
		return nil, err
	}
	return s.st.GetTicket(ctx, s.st.Conn(), ticketID, false)
}

// SaveCanned creates (empty ID) or updates a saved reply.
func (s *Service) SaveCanned(ctx context.Context, actor *store.User, c *store.Canned) error {
	c.Title = strings.TrimSpace(c.Title)
	if n := utf8.RuneCountInString(c.Title); n < 1 || n > 64 {
		return &FieldError{"title", "1 to 64 characters"}
	}
	body := map[string]string{}
	for _, l := range i18n.Languages {
		v := i18n.NormalizeText(c.Body[l])
		if v == "" {
			continue
		}
		if _, err := staffBody(v); err != nil {
			msg := err.Error()
			var fe *FieldError
			if errors.As(err, &fe) {
				msg = fe.Msg
			}
			return &FieldError{"body." + l, msg}
		}
		body[l] = v
	}
	if len(body) == 0 {
		return &FieldError{"body.fa", "write the reply in at least one language"}
	}
	c.Body, c.UpdatedBy = body, &actor.ID
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		var before any
		if c.ID != "" {
			old, err := s.st.GetCanned(ctx, tx, c.ID)
			if err != nil {
				return err
			}
			before = map[string]any{"title": old.Title, "body": old.Body, "sort": old.Sort}
		}
		if err := s.st.SaveCanned(ctx, tx, c); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "canned.save", "canned_reply", c.ID, before,
			map[string]any{"title": c.Title, "body": c.Body, "sort": c.Sort}, "")
	})
}

// DeleteCanned removes a saved reply.
func (s *Service) DeleteCanned(ctx context.Context, actor *store.User, id string) error {
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		old, err := s.st.GetCanned(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.st.DeleteCanned(ctx, tx, id); err != nil {
			return err
		}
		return s.auditChange(ctx, tx, actor, "canned.delete", "canned_reply", id,
			map[string]any{"title": old.Title, "body": old.Body, "sort": old.Sort}, nil, "")
	})
}

// TicketWorker closes tickets the customer did not answer within
// support.autoclose_days of the staff's reply.
type TicketWorker struct {
	s     *Service
	log   *slog.Logger
	every time.Duration
}

// NewTicketWorker builds the worker (ticking every 10 minutes).
func (s *Service) NewTicketWorker(log *slog.Logger) *TicketWorker {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &TicketWorker{s: s, log: log, every: 10 * time.Minute}
}

// Run ticks until ctx ends.
func (w *TicketWorker) Run(ctx context.Context) error {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		if n, err := w.Step(ctx); err != nil {
			w.log.Warn("ticket auto-close", "err", err)
		} else if n > 0 {
			w.log.Info("tickets closed after no answer", "count", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Step closes the stale answered tickets once.
func (w *TicketWorker) Step(ctx context.Context) (int, error) {
	days := w.s.settingInt(ctx, "support.autoclose_days")
	if v := w.s.setting(ctx, "support.autoclose_days"); v == "" {
		days = 7
	}
	if days <= 0 {
		return 0, nil
	}
	after := time.Duration(days) * 24 * time.Hour
	ids, err := w.s.st.StaleAnswered(ctx, w.s.st.Conn(), after, 200)
	if err != nil {
		return 0, err
	}
	return w.closeStale(ctx, ids, after)
}

// closeStale closes the listed tickets that are still stale: the customer
// may have written, or staff replied again, since they were listed.
func (w *TicketWorker) closeStale(ctx context.Context, ids []string, after time.Duration) (int, error) {
	n := 0
	for _, id := range ids {
		if ok, err := w.s.st.AutoCloseTicket(ctx, w.s.st.Conn(), id, after); err != nil {
			return n, err
		} else if ok {
			n++
		}
	}
	return n, nil
}
