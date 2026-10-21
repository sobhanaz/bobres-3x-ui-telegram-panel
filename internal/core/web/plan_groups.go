package web

import (
	"net/http"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

// planGroupJSON is a plan group as the dashboard shows it: the names and texts
// per language, and how many plans are in it.
type planGroupJSON struct {
	ID          string            `json:"id"`
	Name        map[string]string `json:"name"`
	Description map[string]string `json:"description"`
	Sort        int32             `json:"sort"`
	Plans       int               `json:"plans"`
}

func planGroupOf(g *store.PlanGroup) planGroupJSON {
	name, desc := g.NameI18n, g.DescriptionI18n
	if name == nil {
		name = map[string]string{}
	}
	if desc == nil {
		desc = map[string]string{}
	}
	return planGroupJSON{ID: g.ID, Name: name, Description: desc, Sort: g.Sort, Plans: g.Plans}
}

// listPlanGroups: GET /api/v1/plan-groups, every group in the order customers see them.
func (s *Server) listPlanGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.cfg.Domain.ListPlanGroups(r.Context())
	if err != nil {
		s.fail(w, "list plan groups", err)
		return
	}
	items := make([]planGroupJSON, 0, len(groups))
	for i := range groups {
		items = append(items, planGroupOf(&groups[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// planGroupBody is a group as the dashboard edits it.
type planGroupBody struct {
	Name        map[string]string `json:"name"`
	Description map[string]string `json:"description"`
	Sort        int32             `json:"sort"`
}

// createPlanGroup: POST /api/v1/plan-groups
func (s *Server) createPlanGroup(w http.ResponseWriter, r *http.Request) {
	var b planGroupBody
	if !readJSON(w, r, &b) {
		return
	}
	s.savePlanGroup(w, r, &store.PlanGroup{NameI18n: b.Name, DescriptionI18n: b.Description, Sort: b.Sort})
}

// updatePlanGroup: PUT /api/v1/plan-groups/{id}
func (s *Server) updatePlanGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var b planGroupBody
	if !readJSON(w, r, &b) {
		return
	}
	s.savePlanGroup(w, r, &store.PlanGroup{ID: id, NameI18n: b.Name, DescriptionI18n: b.Description, Sort: b.Sort})
}

func (s *Server) savePlanGroup(w http.ResponseWriter, r *http.Request, g *store.PlanGroup) {
	saved, err := s.cfg.Domain.UpsertPlanGroup(r.Context(), actor(r), g)
	if err != nil {
		s.fail(w, "save plan group", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": planGroupOf(saved)})
}

// deletePlanGroup: DELETE /api/v1/plan-groups/{id}; its plans stay on sale, ungrouped.
func (s *Server) deletePlanGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.DeletePlanGroup(r.Context(), actor(r), id); err != nil {
		s.fail(w, "delete plan group", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
