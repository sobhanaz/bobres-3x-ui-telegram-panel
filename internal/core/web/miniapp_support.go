package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// Support in the Mini App: the customer's tickets, their conversation with
// staff, and a message to write. It is the bot's one-open-conversation support
// (the same domain functions, so staff see one inbox): staff replies are still
// delivered by the bot, and the thread here shows them too. Photos and files
// are sent in the bot.

func init() {
	registerMiniFeature(miniFeature{
		routes: func(s *Server, api *http.ServeMux) {
			api.HandleFunc("GET /api/mini/v1/support", s.miniSession(false, s.miniSupport))
			api.HandleFunc("GET /api/mini/v1/support/tickets/{id}", s.miniSession(false, s.miniSupportTicket))
			api.HandleFunc("POST /api/mini/v1/support/messages", s.miniSession(false, s.miniSupportSend))
			api.HandleFunc("POST /api/mini/v1/support/tickets/{id}/close", s.miniSession(false, s.miniSupportClose))
		},
		me: func(_ *Server, _ context.Context, _ *store.User, features map[string]any) error {
			features["support"] = true
			return nil
		},
	})
}

// supportLastMessages is how much of a conversation the app loads.
const supportLastMessages = 20

func miniTicketBody(t *store.Ticket) map[string]any {
	out := map[string]any{"id": t.ID, "number": t.Number, "category": t.Category, "subject": t.Subject, "status": t.Status,
		"created_at": t.CreatedAt.Unix(), "updated_at": t.UpdatedAt.Unix()}
	if t.LastStaffAt != nil {
		out["last_staff_at"] = t.LastStaffAt.Unix()
	}
	return out
}

// miniSupport: GET /api/mini/v1/support, the open ticket (if any) and the recent ones.
func (s *Server) miniSupport(w http.ResponseWriter, r *http.Request) {
	open, recent, err := s.cfg.Domain.MyTickets(r.Context(), miniFrom(r.Context()).user.ID, 10)
	if err != nil {
		s.miniFail(w, "mini app support", err)
		return
	}
	tickets := make([]map[string]any, 0, len(recent))
	for i := range recent {
		tickets = append(tickets, miniTicketBody(&recent[i]))
	}
	var openBody any
	if open != nil {
		openBody = miniTicketBody(open)
	}
	writeJSON(w, http.StatusOK, map[string]any{"open": openBody, "tickets": tickets, "categories": domain.TicketCategories})
}

// miniSupportTicket: GET /api/mini/v1/support/tickets/{id}, one ticket of the
// customer's with its last messages (staff notes are never included).
func (s *Server) miniSupportTicket(w http.ResponseWriter, r *http.Request) {
	if !miniUUIDRe.MatchString(r.PathValue("id")) {
		s.miniFail(w, "mini app ticket", store.ErrNotFound)
		return
	}
	t, msgs, err := s.cfg.Domain.MyTicket(r.Context(), miniFrom(r.Context()).user.ID, r.PathValue("id"), supportLastMessages)
	if err != nil {
		s.miniFail(w, "mini app ticket", err)
		return
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if m.Kind != "customer" && m.Kind != "staff" {
			continue
		}
		out = append(out, map[string]any{"id": m.ID, "kind": m.Kind, "body": m.Body, "has_file": m.FileID != "", "at": m.CreatedAt.Unix()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": miniTicketBody(t), "messages": out})
}

// miniSupportSend: POST /api/mini/v1/support/messages {text, category} adds
// the message to the customer's open ticket, or starts one.
func (s *Server) miniSupportSend(w http.ResponseWriter, r *http.Request) {
	ctx, c := r.Context(), miniFrom(r.Context())
	var in struct {
		Text     string `json:"text"`
		Category string `json:"category"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Text == "" {
		s.miniFail(w, "mini app support", errInvalid("a message needs text"))
		return
	}
	// limits.tickets_per_day: the messages a customer may write in 24 hours (0:
	// no limit). Here they are counted from the database, so the ones sent
	// through the bot count too; the bot keeps a count of its own (Redis).
	settings, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		s.miniFail(w, "mini app support", err)
		return
	}
	perDay := 10
	if v, ok := settings["limits.tickets_per_day"]; ok {
		perDay, _ = strconv.Atoi(v)
	}
	if perDay > 0 {
		n, err := s.cfg.Store.CountCustomerMessagesSince(ctx, s.cfg.Store.Conn(), c.user.ID, s.now().Add(-24*time.Hour))
		if err != nil {
			s.miniFail(w, "mini app support", err)
			return
		}
		if n >= int64(perDay) {
			w.Header().Set("Retry-After", "3600")
			writeError(w, http.StatusTooManyRequests, "daily_limit", "the daily limit of support messages is reached")
			return
		}
	}
	res, err := s.cfg.Domain.AddCustomerMessage(ctx, domain.CustomerMessage{UserID: c.user.ID, Category: in.Category, Text: in.Text})
	if err != nil {
		s.miniFail(w, "mini app support", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": miniTicketBody(res.Ticket), "message_id": res.MessageID, "new_ticket": res.NewTicket})
}

// miniSupportClose: POST /api/mini/v1/support/tickets/{id}/close.
func (s *Server) miniSupportClose(w http.ResponseWriter, r *http.Request) {
	if !miniUUIDRe.MatchString(r.PathValue("id")) {
		s.miniFail(w, "mini app ticket", store.ErrNotFound)
		return
	}
	t, err := s.cfg.Domain.CloseMyTicket(r.Context(), miniFrom(r.Context()).user.ID, r.PathValue("id"))
	if err != nil {
		s.miniFail(w, "mini app ticket", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": miniTicketBody(t)})
}
