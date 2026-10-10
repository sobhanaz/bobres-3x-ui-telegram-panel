package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// maxImportTexts bounds an import (the catalog has a few hundred texts).
const maxImportTexts = 2000

type textJSON struct {
	Key          string   `json:"key"`
	Group        string   `json:"group"`
	Context      string   `json:"context"`
	Placeholders []string `json:"placeholders"`
	Max          int      `json:"max"`
	Langs        []string `json:"langs"`
	Default      langPair `json:"default"`
	Override     langPair `json:"override"`
	// Editable is false for the payment screens when the viewer is not the owner.
	Editable bool `json:"editable"`
}

func textOf(info i18n.TextInfo, settings map[string]string, role string) textJSON {
	cat := domain.Texts()
	ph := info.Placeholders
	if ph == nil {
		ph = []string{}
	}
	return textJSON{
		Key: info.Key, Group: info.Group, Context: string(info.Context), Placeholders: ph, Max: info.Max, Langs: info.Langs,
		Default:  langPair{cat.Default("fa", info.Key), cat.Default("en", info.Key)},
		Override: langPair{settings["texts.fa."+info.Key], settings["texts.en."+info.Key]},
		Editable: Allowed(role, domain.TextPerm(info.Key)),
	}
}

// writeTextForbidden answers a change to a text only the owner may make.
func writeTextForbidden(w http.ResponseWriter, key string) {
	writeJSON(w, http.StatusForbidden, map[string]any{"error": "forbidden", "field": key,
		"message": "only the store owner can change the payment texts"})
}

// listTexts: GET /api/v1/texts, every bot text with its default and override.
func (s *Server) listTexts(w http.ResponseWriter, r *http.Request) {
	settings, err := s.cfg.Domain.Settings(r.Context())
	if err != nil {
		s.fail(w, "texts", err)
		return
	}
	infos := domain.Texts().Infos()
	items := make([]textJSON, 0, len(infos))
	role := actor(r).Role
	for _, info := range infos {
		items = append(items, textOf(info, settings, role))
	}
	writeJSON(w, http.StatusOK, map[string]any{"languages": i18n.Languages, "items": items})
}

type textReq struct {
	Lang  string `json:"lang"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// textInfo checks a request's language and key; false after answering.
func textInfo(w http.ResponseWriter, req textReq) (i18n.TextInfo, bool) {
	info, ok := domain.Texts().Info(req.Key)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no such text")
		return info, false
	}
	if !slices.Contains(i18n.Languages, req.Lang) {
		writeError(w, http.StatusBadRequest, "invalid", "unknown language")
		return info, false
	}
	return info, true
}

// checkText: POST /api/v1/texts/check, the editor's live check (nothing saved).
func (s *Server) checkText(w http.ResponseWriter, r *http.Request) {
	var req textReq
	if !readJSON(w, r, &req) {
		return
	}
	info, ok := textInfo(w, req)
	if !ok {
		return
	}
	value, probs := domain.Texts().ValidateOverride(req.Lang, req.Key, req.Value)
	out := make([]textProblem, 0, len(probs))
	for _, p := range probs {
		out = append(out, textProblem{Code: p.Code, Arg: p.Arg})
	}
	if value == "" && len(probs) == 0 {
		value = i18n.NormalizeText(req.Value)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(probs) == 0, "value": value, "problems": out,
		"length": i18n.TextLength(value), "max": info.Max})
}

// putText: PUT /api/v1/texts {lang,key,value}; an empty value goes back to
// the default.
func (s *Server) putText(w http.ResponseWriter, r *http.Request) {
	var req textReq
	if !readJSON(w, r, &req) {
		return
	}
	info, ok := textInfo(w, req)
	if !ok {
		return
	}
	if !Allowed(actor(r).Role, domain.TextPerm(req.Key)) {
		writeTextForbidden(w, req.Key)
		return
	}
	if _, err := s.cfg.Domain.SetSettings(r.Context(), actor(r), map[string]string{"texts." + req.Lang + "." + req.Key: req.Value}); err != nil {
		s.fail(w, "save text", err)
		return
	}
	s.refreshBot()
	settings, err := s.cfg.Domain.Settings(r.Context())
	if err != nil {
		s.fail(w, "texts", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": textOf(info, settings, actor(r).Role)})
}

// exportTexts: GET /api/v1/texts.json?lang=fa, a language's overrides as a
// file that importTexts takes back.
func (s *Server) exportTexts(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if !slices.Contains(i18n.Languages, lang) {
		writeError(w, http.StatusBadRequest, "invalid", "lang must be fa or en")
		return
	}
	settings, err := s.cfg.Domain.Settings(r.Context())
	if err != nil {
		s.fail(w, "texts", err)
		return
	}
	texts := map[string]string{}
	for k, v := range settings {
		if l, key, ok := i18n.SplitTextKey(k); ok && l == lang && v != "" {
			texts[key] = v
		}
	}
	w.Header().Set("Content-Disposition", `attachment; filename="bobres-texts-`+lang+"-"+s.now().UTC().Format("20060102")+`.json"`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{"lang": lang, "exported_at": s.now().UTC().Format(time.RFC3339), "texts": texts})
}

// importTexts: POST /api/v1/texts/import {lang, texts}: every text is
// checked and nothing is saved unless all are fine.
func (s *Server) importTexts(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lang  string            `json:"lang"`
		Texts map[string]string `json:"texts"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Lang = strings.TrimSpace(req.Lang)
	if len(req.Texts) == 0 || len(req.Texts) > maxImportTexts {
		writeError(w, http.StatusBadRequest, "invalid", "the file has no texts (or far too many)")
		return
	}
	for k := range req.Texts {
		if perm := domain.TextPerm(k); perm != "" && !Allowed(actor(r).Role, perm) {
			writeTextForbidden(w, k)
			return
		}
	}
	n, err := s.cfg.Domain.ImportTexts(r.Context(), actor(r), req.Lang, req.Texts)
	if err != nil {
		s.fail(w, "import texts", err)
		return
	}
	if n > 0 {
		s.refreshBot()
	}
	writeJSON(w, http.StatusOK, map[string]int{"changed": n})
}
