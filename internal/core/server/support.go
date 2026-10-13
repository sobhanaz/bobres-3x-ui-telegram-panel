package server

import (
	"context"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

func ticketToProto(t *store.Ticket) *corev1.Ticket {
	if t == nil {
		return nil
	}
	return &corev1.Ticket{Id: t.ID, UserId: t.UserID, Category: t.Category, Text: t.Subject, Status: t.Status,
		CreatedAt: t.CreatedAt.Unix(), Number: t.Number, UpdatedAt: t.UpdatedAt.Unix(), LastFrom: store.LastFrom(t)}
}

// CreateSupportTicket is AddTicketMessage for older bots (text only, no
// Telegram message id).
func (s *Server) CreateSupportTicket(ctx context.Context, req *corev1.CreateSupportTicketRequest) (*corev1.Ticket, error) {
	ctx = fromBot(ctx)
	res, err := s.dom.AddCustomerMessage(ctx, domain.CustomerMessage{UserID: req.GetUserId(), Category: req.GetCategory(),
		Text: req.GetText()})
	if err != nil {
		return nil, fail(err)
	}
	return ticketToProto(res.Ticket), nil
}

// AddTicketMessage records a customer's support message.
func (s *Server) AddTicketMessage(ctx context.Context, req *corev1.AddTicketMessageRequest) (*corev1.AddTicketMessageResponse, error) {
	ctx = fromBot(ctx)
	if req.GetCheckOnly() {
		t, err := s.dom.RepliedTicket(ctx, req.GetUserId(), req.GetReplyToTgMessageId())
		if err != nil {
			return nil, fail(err)
		}
		return &corev1.AddTicketMessageResponse{Ticket: ticketToProto(t)}, nil
	}
	res, err := s.dom.AddCustomerMessage(ctx, domain.CustomerMessage{
		UserID: req.GetUserId(), Category: req.GetCategory(), TicketID: req.GetTicketId(),
		ReplyToTGID: req.GetReplyToTgMessageId(), Text: req.GetText(), FileID: req.GetFileId(), FileType: req.GetFileType(),
		FileMime: req.GetFileMime(), FileName: req.GetFileName(), FileSize: req.GetFileSize(), TGMessageID: req.GetTgMessageId(),
	})
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.AddTicketMessageResponse{Ticket: ticketToProto(res.Ticket), MessageId: res.MessageID, Created: res.Created,
		NewTicket: res.NewTicket, ClosedNumber: res.ClosedNumber}, nil
}

// GetMyTickets is a customer's open ticket and recent ones.
func (s *Server) GetMyTickets(ctx context.Context, req *corev1.GetMyTicketsRequest) (*corev1.MyTickets, error) {
	open, recent, err := s.dom.MyTickets(ctx, req.GetUserId(), int(req.GetLimit()))
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.MyTickets{Open: ticketToProto(open)}
	for i := range recent {
		out.Recent = append(out.Recent, ticketToProto(&recent[i]))
	}
	return out, nil
}

// GetMyTicket is one of a customer's tickets with its last messages.
func (s *Server) GetMyTicket(ctx context.Context, req *corev1.GetMyTicketRequest) (*corev1.TicketThread, error) {
	t, msgs, err := s.dom.MyTicket(ctx, req.GetUserId(), req.GetTicketId(), int(req.GetLast()))
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.TicketThread{Ticket: ticketToProto(t)}
	for _, m := range msgs {
		out.Messages = append(out.Messages, &corev1.TicketMessage{Id: m.ID, Kind: m.Kind, Body: m.Body,
			HasFile: m.FileID != "", CreatedAt: m.CreatedAt.Unix()})
	}
	return out, nil
}

// CloseMyTicket lets a customer close their ticket.
func (s *Server) CloseMyTicket(ctx context.Context, req *corev1.CloseMyTicketRequest) (*corev1.Ticket, error) {
	t, err := s.dom.CloseMyTicket(fromBot(ctx), req.GetUserId(), req.GetTicketId())
	if err != nil {
		return nil, fail(err)
	}
	return ticketToProto(t), nil
}

// GetTicketMessage is what the notifier needs to deliver a reply or alert
// staff.
func (s *Server) GetTicketMessage(ctx context.Context, req *corev1.GetTicketMessageRequest) (*corev1.TicketMessageView, error) {
	v, err := s.dom.TicketMessageView(ctx, req.GetMessageId())
	if err != nil {
		return nil, fail(err)
	}
	out := &corev1.TicketMessageView{TicketId: v.Ticket.ID, TicketNumber: v.Ticket.Number, TelegramId: v.TelegramID,
		Language: v.Language, Username: v.Username, Kind: v.Message.Kind, Body: v.Message.Body,
		HasFile: v.Message.FileID != "", Delivery: v.Message.Delivery, Closed: v.Ticket.Status == "closed",
		Category: v.Ticket.Category, FileId: v.Message.FileID, FileType: v.Message.FileType}
	// Telegram link buttons must be https.
	if origin := s.dashboardURL(ctx); strings.HasPrefix(origin, "https://") {
		out.Url = origin + "/admin/support/" + v.Ticket.ID
	}
	return out, nil
}

// ReportTicketDelivery records how a staff reply's delivery went.
func (s *Server) ReportTicketDelivery(ctx context.Context, req *corev1.ReportTicketDeliveryRequest) (*corev1.ReportTicketDeliveryResponse, error) {
	if err := s.dom.ReportTicketDelivery(fromBot(ctx), req.GetMessageId(), req.GetStatus(), req.GetTgMessageId()); err != nil {
		return nil, fail(err)
	}
	return &corev1.ReportTicketDeliveryResponse{}, nil
}

// SetUserBotBlocked records that a user blocked the bot, or came back.
func (s *Server) SetUserBotBlocked(ctx context.Context, req *corev1.SetUserBotBlockedRequest) (*corev1.SetUserBotBlockedResponse, error) {
	if err := s.dom.SetUserBotBlocked(fromBot(ctx), req.GetTelegramId(), req.GetBlocked()); err != nil {
		return nil, fail(err)
	}
	return &corev1.SetUserBotBlockedResponse{}, nil
}

// ClaimBroadcastBatch hands the bot the next broadcast recipients.
func (s *Server) ClaimBroadcastBatch(ctx context.Context, req *corev1.ClaimBroadcastBatchRequest) (*corev1.BroadcastBatch, error) {
	b, err := s.dom.ClaimBroadcastBatch(fromBot(ctx), int(req.GetLimit()))
	if err != nil {
		return nil, fail(err)
	}
	if b == nil {
		return &corev1.BroadcastBatch{}, nil
	}
	out := &corev1.BroadcastBatch{BroadcastId: b.Broadcast.ID, Texts: b.Broadcast.Texts, ButtonUrl: b.Broadcast.ButtonURL,
		ButtonLabels: b.Broadcast.ButtonLabels}
	for _, r := range b.Recipients {
		out.Recipients = append(out.Recipients, &corev1.BroadcastRecipient{UserId: r.UserID, TelegramId: r.TelegramID, Language: r.Language})
	}
	return out, nil
}

var outcomeNames = map[corev1.BroadcastOutcome]string{
	corev1.BroadcastOutcome_BROADCAST_OUTCOME_SENT:     "sent",
	corev1.BroadcastOutcome_BROADCAST_OUTCOME_BLOCKED:  "blocked",
	corev1.BroadcastOutcome_BROADCAST_OUTCOME_FAILED:   "failed",
	corev1.BroadcastOutcome_BROADCAST_OUTCOME_UNKNOWN:  "unknown",
	corev1.BroadcastOutcome_BROADCAST_OUTCOME_RELEASED: "released",
}

// ReportBroadcastResults records the bot's results and answers the
// broadcast's status.
func (s *Server) ReportBroadcastResults(ctx context.Context, req *corev1.ReportBroadcastResultsRequest) (*corev1.BroadcastState, error) {
	results := make([]store.Result, 0, len(req.GetResults()))
	for _, r := range req.GetResults() {
		results = append(results, store.Result{UserID: r.GetUserId(), Outcome: outcomeNames[r.GetOutcome()],
			MessageID: r.GetMessageId(), Error: r.GetError()})
	}
	st, err := s.dom.ReportBroadcastResults(fromBot(ctx), req.GetBroadcastId(), results, req.GetHaltReason())
	if err != nil {
		return nil, fail(err)
	}
	return &corev1.BroadcastState{Status: st}, nil
}
