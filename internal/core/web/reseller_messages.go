package web

import (
	"net/http"
	"slices"
	"strings"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
)

// Messages to resellers and the weekly settlement reminder, on the Resellers page (docs/04-dashboard.md). They are broadcasts, so they need
// broadcasts.write (the route's check), and they show resellers' balances, so resellers.write too (resellerMessages).

// resellerMessages lets a handler run only for a role that also holds resellers.write.
func resellerMessages(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !Allowed(actor(r).Role, PermResellersWrite) {
			writeError(w, http.StatusForbidden, "forbidden", "your role may not do this")
			return
		}
		h(w, r)
	}
}

type resellerAudienceReq struct {
	DebtOnly bool   `json:"debt_only"`
	TierID   string `json:"tier_id"`
	Language string `json:"language"`
}

// resellerMessageAudience: POST /api/v1/reseller-messages/audience {debt_only, tier_id, language}.
func (s *Server) resellerMessageAudience(w http.ResponseWriter, r *http.Request) {
	var req resellerAudienceReq
	if !readJSON(w, r, &req) {
		return
	}
	total, fa, en, err := s.cfg.Domain.ResellerAudience(r.Context(), req.DebtOnly, req.TierID, req.Language)
	if err != nil {
		s.fail(w, "reseller audience", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "languages": map[string]int64{"fa": fa, "en": en}})
}

// checkResellerText: POST /api/v1/reseller-messages/check {lang, text}, the composer's live check (placeholders included).
func (s *Server) checkResellerText(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lang string `json:"lang"`
		Text string `json:"text"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !slices.Contains(i18n.Languages, req.Lang) {
		writeError(w, http.StatusBadRequest, "invalid", "unknown language")
		return
	}
	value, probs := domain.ResellerTextProblems(req.Lang, req.Text)
	out := make([]textProblem, 0, len(probs))
	for _, p := range probs {
		out = append(out, textProblem{Code: p.Code, Arg: p.Arg})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(probs) == 0, "value": value, "problems": out,
		"length": i18n.TextLength(value), "max": i18n.MaxMessage, "placeholders": domain.ResellerPlaceholders})
}

type resellerPreviewReq struct {
	ResellerID string `json:"reseller_id"`
	Lang       string `json:"lang"`
	Text       string `json:"text"`
	resellerAudienceReq
}

func previewJSON(p *domain.ResellerPreview) map[string]any {
	out := map[string]any{"text": p.Text, "reseller": nil}
	if c := p.Reseller; c != nil {
		out["reseller"] = briefOf(c.UserID, c.TelegramID, c.Username)
	}
	return out
}

// previewResellerText: POST /api/v1/reseller-messages/preview {reseller_id, lang, text, debt_only, tier_id}: the text as that reseller (""
// = the first of the audience, or sample values when there is none) would get it.
func (s *Server) previewResellerText(w http.ResponseWriter, r *http.Request) {
	var req resellerPreviewReq
	if !readJSON(w, r, &req) {
		return
	}
	p, err := s.cfg.Domain.PreviewResellerText(r.Context(), req.ResellerID, req.Lang, req.Text, req.DebtOnly, req.TierID)
	if err != nil {
		s.fail(w, "reseller preview", err)
		return
	}
	writeJSON(w, http.StatusOK, previewJSON(p))
}

// sendTestToMe sends text (already written for a reseller) to the staff member's own Telegram, with the buttons a reseller would get.
func (s *Server) sendTestToMe(w http.ResponseWriter, r *http.Request, action, lang, text string, button *broadcastButton, settle bool, detail map[string]any) {
	me := actor(r)
	t := botfiles.TestBroadcast{ChatID: me.TelegramID, Text: text}
	if button != nil && button.URL != "" {
		if err := domain.CheckBroadcastURL(button.URL); err != nil {
			s.fail(w, "test message", err)
			return
		}
		label := button.Labels.FA
		if lang == "en" {
			label = button.Labels.EN
		}
		label, probs := i18n.ValidateLabel(label)
		if len(probs) > 0 {
			writeFieldError(w, "button.labels."+lang, "a short label without < > & (at most 64 characters)")
			return
		}
		t.Button = &botfiles.TestBroadcastBtn{Text: label, URL: button.URL}
	}
	if settle {
		t.Settle = s.cfg.Domain.SettleLabel(r.Context(), lang)
	}
	if s.cfg.Bot == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot's address is not configured (BOBRES_BOT_URL)")
		return
	}
	if !s.tests.allow("test:"+me.ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 5 test sends a minute")
		return
	}
	res, err := s.cfg.Bot.TestBroadcast(r.Context(), t)
	if err != nil {
		s.cfg.Log.Warn("test message to resellers", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot did not answer; try again in a minute")
		return
	}
	detail["lang"], detail["ok"], detail["problem"] = lang, res.OK, res.Problem
	s.audit(r.Context(), me, action, detail)
	writeJSON(w, http.StatusOK, res)
}

// testResellerMessage: POST /api/v1/reseller-messages/test {reseller_id, lang, text, button, settle_button, debt_only, tier_id}: the text as
// that reseller would get it, sent to the staff member's own Telegram.
func (s *Server) testResellerMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		resellerPreviewReq
		Button       *broadcastButton `json:"button"`
		SettleButton bool             `json:"settle_button"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	p, err := s.cfg.Domain.PreviewResellerText(r.Context(), req.ResellerID, req.Lang, req.Text, req.DebtOnly, req.TierID)
	if err != nil {
		s.fail(w, "test message", err)
		return
	}
	s.sendTestToMe(w, r, "reseller_message.test", req.Lang, p.Text, req.Button, req.SettleButton, map[string]any{"as": previewJSON(p)["reseller"]})
}

// sendResellerMessage: POST /api/v1/reseller-messages {debt_only, tier_id, language, texts, button, settle_button}: written and sent now.
func (s *Server) sendResellerMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		resellerAudienceReq
		Texts        langPair         `json:"texts"`
		Button       *broadcastButton `json:"button"`
		SettleButton bool             `json:"settle_button"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	in := domain.ResellerMessageInput{DebtOnly: req.DebtOnly, TierID: req.TierID, Language: req.Language,
		Texts: map[string]string{"fa": req.Texts.FA, "en": req.Texts.EN}, SettleButton: req.SettleButton}
	if req.Button != nil && req.Button.URL != "" {
		in.ButtonURL = req.Button.URL
		in.ButtonLabels = map[string]string{"fa": req.Button.Labels.FA, "en": req.Button.Labels.EN}
	}
	b, err := s.cfg.Domain.SendResellerMessage(r.Context(), actor(r), in)
	if err != nil {
		s.fail(w, "send reseller message", err)
		return
	}
	s.writeBroadcast(w, r, b)
}

// reminderKeys are the settings the reminder card saves.
var reminderKeys = []string{domain.SettingReminderEnabled, domain.SettingReminderWeekday, domain.SettingReminderTime,
	domain.SettingReminderDebtOnly, domain.SettingReminderSettle, domain.SettingReminderText + "fa", domain.SettingReminderText + "en"}

func (s *Server) writeReminder(w http.ResponseWriter, r *http.Request) {
	st, err := s.cfg.Domain.ResellerReminderStatus(r.Context())
	if err != nil {
		s.fail(w, "reseller reminder", err)
		return
	}
	c := st.Config
	out := map[string]any{
		"enabled": c.Enabled, "weekday": c.Weekday, "time": c.Time, "debt_only": c.DebtOnly, "settle_button": c.Settle,
		"texts":    langPair{c.Texts["fa"], c.Texts["en"]},
		"custom":   map[string]bool{"fa": c.Custom["fa"], "en": c.Custom["en"]},
		"defaults": langPair{domain.DefaultReminderTexts["fa"], domain.DefaultReminderTexts["en"]},
		"weekdays": domain.ReminderWeekdays, "zone": st.Zone, "next_due_at": unix(st.NextDue), "last": nil,
	}
	if l := st.Last; l != nil {
		last := map[string]any{"day": l.Day, "zone": l.Zone, "due_at": l.DueAt.Unix(), "queued_at": l.CreatedAt.Unix(), "broadcast": nil}
		if b := l.Broadcast; b != nil {
			last["broadcast"] = map[string]any{"id": b.ID, "status": b.Status, "started_at": unix(b.StartedAt), "finished_at": unix(b.FinishedAt),
				"counts": map[string]int{"total": b.Total, "pending": b.Pending, "sent": b.Sent, "blocked": b.Blocked, "failed": b.Failed, "unknown": b.Unknown}}
		}
		out["last"] = last
	}
	writeJSON(w, http.StatusOK, out)
}

// getReminder: GET /api/v1/reseller-messages/reminder, the reminder's card.
func (s *Server) getReminder(w http.ResponseWriter, r *http.Request) { s.writeReminder(w, r) }

// putReminder: PUT /api/v1/reseller-messages/reminder {values:{key:value}}, all or nothing; each changed setting is audited (setting.set).
// A text equal to the built-in one is stored as "" (the default), so a later change of the default reaches it.
func (s *Server) putReminder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Values map[string]string `json:"values"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	for k, v := range req.Values {
		if !slices.Contains(reminderKeys, k) {
			writeFieldError(w, k, "not a setting of the reminder")
			return
		}
		if lang, ok := strings.CutPrefix(k, domain.SettingReminderText); ok && i18n.NormalizeText(v) == i18n.NormalizeText(domain.DefaultReminderTexts[lang]) {
			req.Values[k] = ""
		}
	}
	if _, err := s.cfg.Domain.SetSettings(r.Context(), actor(r), req.Values); err != nil {
		s.fail(w, "save reminder", err)
		return
	}
	s.refreshBot()
	s.writeReminder(w, r)
}

// testReminder: POST /api/v1/reseller-messages/reminder/test {reseller_id}: the saved reminder, in the staff member's bot language, as that
// reseller (""= the first of its audience) would get it now, sent to the staff member's own Telegram.
func (s *Server) testReminder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResellerID string `json:"reseller_id"`
		Lang       string `json:"lang"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	lang := req.Lang
	if !slices.Contains(i18n.Languages, lang) {
		lang = i18n.Normalize(actor(r).Language)
	}
	p, cfg, err := s.cfg.Domain.ReminderTestText(r.Context(), req.ResellerID, lang)
	if err != nil {
		s.fail(w, "test reminder", err)
		return
	}
	s.sendTestToMe(w, r, "reseller_reminder.test", lang, p.Text, nil, cfg.Settle, map[string]any{"as": previewJSON(p)["reseller"]})
}
