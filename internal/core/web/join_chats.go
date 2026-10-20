package web

import (
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// The required channels and groups (docs/03-bot-ux.md, "Required chats"): the
// Settings page's Join card reads and saves the whole list at once. Its first
// row is the settings join.channel, join.link and join.label; the others are
// the extra chats (domain/joinchats.go).

type joinChatJSON struct {
	Chat    string `json:"chat"`
	Link    string `json:"link"`
	Label   string `json:"label"`
	Primary bool   `json:"primary"`
	// Active: customers are asked to join it under the license in effect.
	Active bool `json:"active"`
}

// joinChatsView is GET and PUT /api/v1/join-chats' answer: the chats in order,
// whether the requirement is on, the limit (-1 = unlimited) and what it comes
// from, the most the list can hold, and whether this role may change it.
func joinChatsView(l *domain.JoinChatList, role string) map[string]any {
	chats := make([]joinChatJSON, len(l.Items))
	for i, it := range l.Items {
		chats[i] = joinChatJSON{Chat: it.Chat, Link: it.Link, Label: it.Label, Primary: it.Primary, Active: it.Active}
	}
	return map[string]any{
		"chats": chats, "on": l.On, "limit": l.Limit, "used": len(l.Items), "max": domain.MaxJoinChatsTotal,
		"basis": string(l.Basis), "tier": l.Tier, "editable": Allowed(role, PermJoinWrite),
	}
}

// getJoinChats: GET /api/v1/join-chats.
func (s *Server) getJoinChats(w http.ResponseWriter, r *http.Request) {
	l, err := s.cfg.Domain.JoinChats(r.Context())
	if err != nil {
		s.fail(w, "join chats", err)
		return
	}
	writeJSON(w, http.StatusOK, joinChatsView(l, actor(r).Role))
}

// putJoinChats: PUT /api/v1/join-chats {chats:[{chat,link,label}]}, all or
// nothing: the first row becomes join.channel (with its link and label), the
// others the extra chats, and an empty list turns the requirement off. A chat
// added past what the license allows is a 409 with license "max_join_chats".
func (s *Server) putJoinChats(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Chats []struct {
			Chat  string `json:"chat"`
			Link  string `json:"link"`
			Label string `json:"label"`
		} `json:"chats"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	list := make([]domain.JoinChat, len(req.Chats))
	for i, c := range req.Chats {
		list[i] = domain.JoinChat{Chat: c.Chat, Link: c.Link, Label: c.Label}
	}
	l, err := s.cfg.Domain.SetJoinChats(r.Context(), actor(r), list)
	if err != nil {
		s.fail(w, "save join chats", err)
		return
	}
	s.mini.join.forget() // the Mini App holds the new list at its next call
	s.refreshBot()
	writeJSON(w, http.StatusOK, joinChatsView(l, actor(r).Role))
}
