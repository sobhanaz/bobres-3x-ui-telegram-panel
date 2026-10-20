package web

import (
	"errors"
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/helpcontent"
)

// Help content (Branding, "Help content" tab): the FAQ and the tutorials the
// bot shows, written in Persian and English. Everything here needs the same
// permission as the bot's texts (branding.write). Every write is checked and
// audited by the domain, and tells the bot to reload, since the bot hides its
// FAQ and Tutorials buttons while there is nothing enabled to show.

type helpButtonJSON struct {
	URL   string            `json:"url"`
	Label map[string]string `json:"label"`
}

type helpItemJSON struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Title     map[string]string `json:"title"`
	Body      map[string]string `json:"body"`
	Buttons   []helpButtonJSON  `json:"buttons"`
	Sort      int               `json:"sort"`
	Enabled   bool              `json:"enabled"`
	Version   int               `json:"version"`
	UpdatedAt int64             `json:"updated_at"`
}

// both gives a by-language text a key for each language (empty when it has no
// text), so the editor has a field for each.
func both(m map[string]string) map[string]string {
	out := make(map[string]string, len(i18n.Languages))
	for _, l := range i18n.Languages {
		out[l] = m[l]
	}
	return out
}

func helpItemOf(e *store.HelpEntry) helpItemJSON {
	it := helpItemJSON{ID: e.ID, Kind: e.Kind, Title: both(e.Title), Body: both(e.Body), Buttons: []helpButtonJSON{},
		Sort: e.Sort, Enabled: e.Enabled, Version: e.Version, UpdatedAt: e.UpdatedAt.Unix()}
	for _, b := range e.Buttons {
		it.Buttons = append(it.Buttons, helpButtonJSON{URL: b.URL, Label: both(b.Label)})
	}
	return it
}

// helpLimits are the limits the editor shows (helpcontent's).
func helpLimits() map[string]int {
	return map[string]int{
		"max_faq": helpcontent.MaxFAQ, "max_tutorials": helpcontent.MaxTutorials,
		"question": helpcontent.MaxQuestion, "tutorial_title": helpcontent.MaxTutorialTitle,
		"body": helpcontent.MaxBody, "buttons": helpcontent.MaxButtons, "button_label": helpcontent.MaxButtonLabel,
		"url": helpcontent.MaxURL, "one_message_entries": helpcontent.OneMessageMaxEntries,
		"one_message_units": helpcontent.OneMessageMaxUnits,
	}
}

// helpList is the whole page: both lists, how the bot lays out the FAQ in each
// language, and the limits.
func helpList(entries []store.HelpEntry) map[string]any {
	faq, tutorials := []helpItemJSON{}, []helpItemJSON{}
	for i := range entries {
		switch it := helpItemOf(&entries[i]); it.Kind {
		case helpcontent.KindFAQ:
			faq = append(faq, it)
		case helpcontent.KindTutorial:
			tutorials = append(tutorials, it)
		}
	}
	layout := map[string]string{}
	for _, l := range i18n.Languages {
		layout[l] = domain.FAQLayout(entries, l)
	}
	return map[string]any{"faq": faq, "tutorials": tutorials, "faq_layout": layout, "limits": helpLimits(), "languages": i18n.Languages}
}

// helpError answers a help entry with problems (400, each with its field) and
// hands any other failure to fail.
func (s *Server) helpError(w http.ResponseWriter, what string, err error) {
	var he *domain.HelpError
	if errors.As(err, &he) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid", "message": he.Error(), "problems": he.Problems})
		return
	}
	s.fail(w, what, err)
}

// listHelp: GET /api/v1/help, every entry (switched off ones too) with the
// layout and the limits.
func (s *Server) listHelp(w http.ResponseWriter, r *http.Request) {
	entries, err := s.cfg.Domain.HelpEntries(r.Context())
	if err != nil {
		s.fail(w, "help content", err)
		return
	}
	writeJSON(w, http.StatusOK, helpList(entries))
}

type helpReq struct {
	Kind    string            `json:"kind"`
	Title   map[string]string `json:"title"`
	Body    map[string]string `json:"body"`
	Buttons []helpButtonJSON  `json:"buttons"`
	// Enabled left out means on (a new entry is shown at once).
	Enabled *bool `json:"enabled"`
	// Version is the one of the copy being edited (updates).
	Version int `json:"version"`
}

func (q helpReq) input() domain.HelpInput {
	in := domain.HelpInput{Kind: q.Kind, Title: q.Title, Body: q.Body, Enabled: q.Enabled == nil || *q.Enabled}
	for _, b := range q.Buttons {
		in.Buttons = append(in.Buttons, store.HelpButton{URL: b.URL, Label: b.Label})
	}
	return in
}

// checkHelp: POST /api/v1/help/check, the editor's live check (nothing saved).
func (s *Server) checkHelp(w http.ResponseWriter, r *http.Request) {
	var req helpReq
	if !readJSON(w, r, &req) {
		return
	}
	_, probs := domain.NormalizeHelp(req.input())
	if probs == nil {
		probs = []domain.HelpProblem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(probs) == 0, "problems": probs})
}

// createHelp: POST /api/v1/help.
func (s *Server) createHelp(w http.ResponseWriter, r *http.Request) {
	var req helpReq
	if !readJSON(w, r, &req) {
		return
	}
	e, err := s.cfg.Domain.CreateHelp(r.Context(), actor(r), req.input())
	if err != nil {
		s.helpError(w, "create help entry", err)
		return
	}
	s.refreshBot()
	writeJSON(w, http.StatusOK, map[string]any{"item": helpItemOf(e)})
}

// updateHelp: PUT /api/v1/help/{id}: the content of the copy at version.
func (s *Server) updateHelp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req helpReq
	if !readJSON(w, r, &req) {
		return
	}
	e, err := s.cfg.Domain.UpdateHelp(r.Context(), actor(r), id, req.input(), req.Version)
	if err != nil {
		s.helpError(w, "update help entry", err)
		return
	}
	s.refreshBot()
	writeJSON(w, http.StatusOK, map[string]any{"item": helpItemOf(e)})
}

// setHelpEnabled: PUT /api/v1/help/{id}/enabled {enabled, version}.
func (s *Server) setHelpEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
		Version int  `json:"version"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	e, err := s.cfg.Domain.SetHelpEnabled(r.Context(), actor(r), id, req.Enabled, req.Version)
	if err != nil {
		s.fail(w, "switch help entry", err)
		return
	}
	s.refreshBot()
	writeJSON(w, http.StatusOK, map[string]any{"item": helpItemOf(e)})
}

// deleteHelp: DELETE /api/v1/help/{id}.
func (s *Server) deleteHelp(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.DeleteHelp(r.Context(), actor(r), id); err != nil {
		s.fail(w, "delete help entry", err)
		return
	}
	s.refreshBot()
	w.WriteHeader(http.StatusNoContent)
}

// orderHelp: POST /api/v1/help/order {kind, ids}: the entries of a kind in the
// order the editor shows them.
func (s *Server) orderHelp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string   `json:"kind"`
		IDs  []string `json:"ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.cfg.Domain.ReorderHelp(r.Context(), actor(r), req.Kind, req.IDs); err != nil {
		s.fail(w, "order help entries", err)
		return
	}
	s.refreshBot()
	s.listHelp(w, r)
}

// defaultHelp: POST /api/v1/help/defaults {kind}: the default content in an
// empty list.
func (s *Server) defaultHelp(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if _, err := s.cfg.Domain.AddDefaultHelp(r.Context(), actor(r), req.Kind); err != nil {
		s.fail(w, "add default help entries", err)
		return
	}
	s.refreshBot()
	s.listHelp(w, r)
}
