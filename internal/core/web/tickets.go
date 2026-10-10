package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

type ticketUser struct {
	ID         string `json:"id"`
	TelegramID int64  `json:"telegram_id"`
	Username   string `json:"username"`
	Status     string `json:"status"`
	Language   string `json:"language"`
}

type ticketJSON struct {
	ID             string         `json:"id"`
	Number         int64          `json:"number"`
	Status         string         `json:"status"`
	Category       string         `json:"category"`
	Subject        string         `json:"subject"`
	User           ticketUser     `json:"user"`
	Assignee       *userBrief     `json:"assignee"`
	CreatedAt      int64          `json:"created_at"`
	UpdatedAt      int64          `json:"updated_at"`
	LastCustomerAt *int64         `json:"last_customer_at"`
	LastStaffAt    *int64         `json:"last_staff_at"`
	ClosedAt       *int64         `json:"closed_at"`
	CloseReason    string         `json:"close_reason"`
	LastMessage    map[string]any `json:"last_message"`
}

func ticketOf(r *store.TicketRow) ticketJSON {
	out := ticketJSON{ID: r.ID, Number: r.Number, Status: r.Status, Category: r.Category, Subject: r.Subject,
		User:      ticketUser{ID: r.UserID, TelegramID: r.TelegramID, Username: r.Username, Status: r.UserStatus, Language: r.Language},
		CreatedAt: r.CreatedAt.Unix(), UpdatedAt: r.UpdatedAt.Unix(), LastCustomerAt: unix(r.LastCustomerAt),
		LastStaffAt: unix(r.LastStaffAt), ClosedAt: unix(r.ClosedAt), CloseReason: r.CloseReason}
	if r.AssignedTo != nil {
		out.Assignee = briefOf(*r.AssignedTo, r.AssigneeTG, r.AssigneeUsername)
	}
	if r.LastAt != nil {
		preview := r.LastPreview
		if preview == "" && r.LastHasFile {
			preview = "📎"
		}
		out.LastMessage = map[string]any{"kind": r.LastKind, "preview": preview, "at": r.LastAt.Unix()}
	}
	return out
}

// ticketFilter reads the list's query; false after answering.
func ticketFilter(w http.ResponseWriter, r *http.Request) (store.TicketFilter, bool) {
	q := r.URL.Query()
	f := store.TicketFilter{Status: q.Get("status"), Category: q.Get("category"), Query: q.Get("q"), ActorID: actor(r).ID}
	switch f.Status {
	case "", "open", "answered", "closed", "active":
	default:
		writeError(w, http.StatusBadRequest, "invalid", "status must be open, answered, closed or active")
		return f, false
	}
	switch a := strings.ToLower(q.Get("assigned")); {
	case a == "" || a == "me" || a == "none":
		f.Assigned = a
	case uuidRe.MatchString(a):
		f.Assigned = a
	default:
		writeError(w, http.StatusBadRequest, "invalid", "assigned must be me, none or a staff member's id")
		return f, false
	}
	user, ok := queryID(r, "user")
	if !ok || utf8.RuneCountInString(f.Query) > 100 || len(f.Category) > 20 {
		writeError(w, http.StatusBadRequest, "invalid", "bad filter")
		return f, false
	}
	f.UserID = user
	return f, true
}

// listTickets: GET /api/v1/tickets.
func (s *Server) listTickets(w http.ResponseWriter, r *http.Request) {
	f, ok := ticketFilter(w, r)
	if !ok {
		return
	}
	p, page, size := pageOf(r)
	rows, total, err := s.cfg.Store.ListTickets(r.Context(), s.cfg.Store.Conn(), f, p)
	if err != nil {
		s.fail(w, "list tickets", err)
		return
	}
	items := make([]ticketJSON, 0, len(rows))
	for i := range rows {
		items = append(items, ticketOf(&rows[i]))
	}
	writeJSON(w, http.StatusOK, list{items, total, page, size})
}

// ticketCounts: GET /api/v1/tickets/counts, the inbox's figures and the badge.
func (s *Server) ticketCounts(w http.ResponseWriter, r *http.Request) {
	c, err := s.cfg.Store.CountTickets(r.Context(), s.cfg.Store.Conn(), actor(r).ID)
	if err != nil {
		s.fail(w, "count tickets", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"waiting": c.Waiting, "mine": c.Mine, "unassigned": c.Unassigned, "answered": c.Answered})
}

// ticketAssignees: GET /api/v1/tickets/assignees, who a ticket can be given to.
func (s *Server) ticketAssignees(w http.ResponseWriter, r *http.Request) {
	staff, err := s.cfg.Store.ActiveStaff(r.Context(), s.cfg.Store.Conn())
	if err != nil {
		s.fail(w, "assignees", err)
		return
	}
	items := make([]map[string]any, 0, len(staff))
	for _, u := range staff {
		items = append(items, map[string]any{"id": u.ID, "telegram_id": u.TelegramID, "username": u.Username, "role": u.Role})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type ticketMessageJSON struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Author    *userBrief     `json:"author"`
	Body      string         `json:"body"`
	File      map[string]any `json:"file"`
	Delivery  map[string]any `json:"delivery"`
	CreatedAt int64          `json:"created_at"`
}

func (s *Server) ticketMessagesJSON(r *http.Request, ticketID string, msgs []store.TicketMessage) []ticketMessageJSON {
	var ids []string
	for _, m := range msgs {
		if m.AuthorID != nil {
			ids = append(ids, *m.AuthorID)
		}
	}
	authors := s.usersByID(r.Context(), ids)
	out := make([]ticketMessageJSON, 0, len(msgs))
	for _, m := range msgs {
		j := ticketMessageJSON{ID: m.ID, Kind: m.Kind, Body: m.Body, CreatedAt: m.CreatedAt.Unix()}
		if m.AuthorID != nil {
			j.Author = authors[*m.AuthorID]
		}
		if m.FileID != "" {
			j.File = map[string]any{"type": m.FileType, "mime": m.FileMime, "name": m.FileName, "size": m.FileSize,
				"url": "/api/v1/tickets/" + ticketID + "/messages/" + m.ID + "/file"}
		}
		if m.Kind == "staff" {
			j.Delivery = map[string]any{"status": m.Delivery, "at": unix(m.DeliveredAt)}
		}
		out = append(out, j)
	}
	return out
}

// writeTicket answers a ticket with its messages.
func (s *Server) writeTicket(w http.ResponseWriter, r *http.Request, id string) {
	row, err := s.cfg.Store.GetTicketRow(r.Context(), s.cfg.Store.Conn(), id)
	if err != nil {
		s.fail(w, "ticket", err)
		return
	}
	msgs, err := s.cfg.Store.TicketMessages(r.Context(), s.cfg.Store.Conn(), id, true, 0)
	if err != nil {
		s.fail(w, "ticket messages", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticketOf(row), "messages": s.ticketMessagesJSON(r, id, msgs),
		"can_manage": Allowed(actor(r).Role, PermTicketsManage)})
}

// getTicket: GET /api/v1/tickets/{id}.
func (s *Server) getTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	s.writeTicket(w, r, id)
}

// replyTicket: POST /api/v1/tickets/{id}/messages {body, kind, close, key}.
func (s *Server) replyTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Body  string `json:"body"`
		Kind  string `json:"kind"`
		Close bool   `json:"close"`
		Key   string `json:"key"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Key != "" && !idemKeyRe.MatchString(req.Key) {
		writeError(w, http.StatusBadRequest, "invalid", "bad request key")
		return
	}
	if req.Kind == "note" && req.Close {
		writeError(w, http.StatusBadRequest, "invalid", "a note cannot close the ticket")
		return
	}
	_, m, err := s.cfg.Domain.ReplyTicket(r.Context(), actor(r), id, req.Body, req.Kind, req.Close, req.Key)
	if err != nil {
		s.fail(w, "reply ticket", err)
		return
	}
	row, err := s.cfg.Store.GetTicketRow(r.Context(), s.cfg.Store.Conn(), id)
	if err != nil {
		s.fail(w, "ticket", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticketOf(row), "message": s.ticketMessagesJSON(r, id, []store.TicketMessage{*m})[0]})
}

// setTicketStatus: POST /api/v1/tickets/{id}/status {status, reason}.
func (s *Server) setTicketStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if _, err := s.cfg.Domain.SetTicketStatus(r.Context(), actor(r), id, req.Status, req.Reason); err != nil {
		s.fail(w, "ticket status", err)
		return
	}
	s.writeTicketItem(w, r, id)
}

// assignTicket: PUT /api/v1/tickets/{id}/assignee {user_id|null}.
func (s *Server) assignTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		UserID *string `json:"user_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.UserID != nil {
		v := strings.ToLower(*req.UserID)
		if !uuidRe.MatchString(v) {
			writeError(w, http.StatusBadRequest, "invalid", "user_id must be a staff member's id or null")
			return
		}
		req.UserID = &v
	}
	me := actor(r)
	if _, err := s.cfg.Domain.AssignTicket(r.Context(), me, id, req.UserID, Allowed(me.Role, PermTicketsManage)); err != nil {
		s.fail(w, "assign ticket", err)
		return
	}
	s.writeTicketItem(w, r, id)
}

func (s *Server) writeTicketItem(w http.ResponseWriter, r *http.Request, id string) {
	row, err := s.cfg.Store.GetTicketRow(r.Context(), s.cfg.Store.Conn(), id)
	if err != nil {
		s.fail(w, "ticket", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticketOf(row)})
}

// ticketFile: GET /api/v1/tickets/{id}/messages/{mid}/file, a customer's
// attachment, through the bot (core has no Telegram access).
func (s *Server) ticketFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	mid, ok := pathID(w, r, "mid")
	if !ok {
		return
	}
	m, err := s.cfg.Store.GetTicketMessage(r.Context(), s.cfg.Store.Conn(), mid)
	if err != nil || m.TicketID != id || m.FileID == "" {
		writeError(w, http.StatusNotFound, "not_found", "no such file")
		return
	}
	if s.cfg.Files == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "files are not set up on this install (BOBRES_BOT_URL)")
		return
	}
	f, err := s.cfg.Files.Fetch(r.Context(), m.FileID)
	switch {
	case errors.Is(err, botfiles.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Telegram no longer has this file")
		return
	case errors.Is(err, botfiles.ErrRefused):
		writeError(w, http.StatusUnsupportedMediaType, "unsupported", "the file is too large or not an image or PDF; open it in Telegram")
		return
	case err != nil:
		s.cfg.Log.Warn("dashboard api: ticket file fetch", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot did not answer; try again in a minute")
		return
	}
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Length", strconv.Itoa(len(f.Data)))
	h.Set("Cache-Control", "private, max-age=600") // an attachment never changes
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	if f.ContentType == "application/pdf" {
		h.Set("Content-Disposition", `attachment; filename="ticket-`+mid[len(mid)-6:]+`.pdf"`)
	} else {
		h.Set("Content-Disposition", "inline")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(f.Data)
}

type cannedJSON struct {
	ID    string            `json:"id"`
	Title string            `json:"title"`
	Body  map[string]string `json:"body"`
	Sort  int               `json:"sort"`
}

func cannedOf(c *store.Canned) cannedJSON {
	body := map[string]string{"fa": c.Body["fa"], "en": c.Body["en"]}
	return cannedJSON{ID: c.ID, Title: c.Title, Body: body, Sort: c.Sort}
}

// listCanned: GET /api/v1/canned-replies.
func (s *Server) listCanned(w http.ResponseWriter, r *http.Request) {
	list, err := s.cfg.Store.ListCanned(r.Context(), s.cfg.Store.Conn())
	if err != nil {
		s.fail(w, "canned replies", err)
		return
	}
	items := make([]cannedJSON, 0, len(list))
	for i := range list {
		items = append(items, cannedOf(&list[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type cannedReq struct {
	Title string            `json:"title"`
	Body  map[string]string `json:"body"`
	Sort  int               `json:"sort"`
}

func (s *Server) saveCanned(w http.ResponseWriter, r *http.Request, id string) {
	var req cannedReq
	if !readJSON(w, r, &req) {
		return
	}
	c := &store.Canned{ID: id, Title: req.Title, Body: req.Body, Sort: req.Sort}
	if err := s.cfg.Domain.SaveCanned(r.Context(), actor(r), c); err != nil {
		s.fail(w, "save canned reply", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": cannedOf(c)})
}

// createCanned: POST /api/v1/canned-replies.
func (s *Server) createCanned(w http.ResponseWriter, r *http.Request) { s.saveCanned(w, r, "") }

// updateCanned: PUT /api/v1/canned-replies/{id}.
func (s *Server) updateCanned(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	s.saveCanned(w, r, id)
}

// deleteCanned: DELETE /api/v1/canned-replies/{id}.
func (s *Server) deleteCanned(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.DeleteCanned(r.Context(), actor(r), id); err != nil {
		s.fail(w, "delete canned reply", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
