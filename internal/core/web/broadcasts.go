package web

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
)

type broadcastButton struct {
	URL    string   `json:"url"`
	Labels langPair `json:"labels"`
}

type broadcastJSON struct {
	ID          string           `json:"id"`
	Status      string           `json:"status"`
	Segment     string           `json:"segment"`
	Language    string           `json:"language"`
	Texts       langPair         `json:"texts"`
	Button      *broadcastButton `json:"button"`
	ScheduledAt *int64           `json:"scheduled_at"`
	StartedAt   *int64           `json:"started_at"`
	FinishedAt  *int64           `json:"finished_at"`
	Counts      map[string]int   `json:"counts"`
	RatePerSec  int64            `json:"rate_per_sec"`
	ETASeconds  *int64           `json:"eta_seconds"`
	LastError   string           `json:"last_error"`
	CreatedBy   *userBrief       `json:"created_by"`
	CreatedAt   int64            `json:"created_at"`
	Version     int              `json:"version"`
}

func (s *Server) broadcastsJSON(r *http.Request, list []store.Broadcast) []broadcastJSON {
	var ids []string
	for _, b := range list {
		ids = append(ids, b.CreatedBy)
	}
	users := s.usersByID(r.Context(), ids)
	rate := s.broadcastRate(r)
	out := make([]broadcastJSON, 0, len(list))
	for _, b := range list {
		j := broadcastJSON{ID: b.ID, Status: b.Status, Segment: b.Segment, Language: b.Language,
			Texts: langPair{b.Texts["fa"], b.Texts["en"]}, ScheduledAt: unix(b.ScheduledAt), StartedAt: unix(b.StartedAt),
			FinishedAt: unix(b.FinishedAt), Counts: map[string]int{"total": b.Total, "pending": b.Pending, "sent": b.Sent,
				"blocked": b.Blocked, "failed": b.Failed, "unknown": b.Unknown},
			RatePerSec: rate, LastError: b.LastError, CreatedBy: users[b.CreatedBy], CreatedAt: b.CreatedAt.Unix(), Version: b.Version}
		if b.ButtonURL != "" {
			j.Button = &broadcastButton{URL: b.ButtonURL, Labels: langPair{b.ButtonLabels["fa"], b.ButtonLabels["en"]}}
		}
		if (b.Status == "running" || b.Status == "scheduled") && rate > 0 && b.Pending > 0 {
			eta := (int64(b.Pending) + rate - 1) / rate
			j.ETASeconds = &eta
		}
		out = append(out, j)
	}
	return out
}

// broadcastRate is limits.broadcast_per_sec (what the bot aims for).
func (s *Server) broadcastRate(r *http.Request) int64 {
	if v, err := s.cfg.Domain.Settings(r.Context()); err == nil {
		if n, err := strconv.ParseInt(v["limits.broadcast_per_sec"], 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 20
}

func (s *Server) writeBroadcast(w http.ResponseWriter, r *http.Request, b *store.Broadcast) {
	writeJSON(w, http.StatusOK, map[string]any{"item": s.broadcastsJSON(r, []store.Broadcast{*b})[0]})
}

// listBroadcasts: GET /api/v1/broadcasts?status=.
func (s *Server) listBroadcasts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !slices.Contains([]string{"draft", "scheduled", "running", "paused", "done", "cancelled"}, status) {
		writeError(w, http.StatusBadRequest, "invalid", "unknown status")
		return
	}
	p, page, size := pageOf(r)
	rows, total, err := s.cfg.Domain.Broadcasts(r.Context(), status, p)
	if err != nil {
		s.fail(w, "list broadcasts", err)
		return
	}
	writeJSON(w, http.StatusOK, list{s.broadcastsJSON(r, rows), total, page, size})
}

type broadcastReq struct {
	Segment  string           `json:"segment"`
	Language string           `json:"language"`
	Texts    langPair         `json:"texts"`
	Button   *broadcastButton `json:"button"`
	Version  int              `json:"version"`
}

func (b broadcastReq) input() domain.BroadcastInput {
	in := domain.BroadcastInput{Segment: b.Segment, Language: b.Language, Texts: map[string]string{"fa": b.Texts.FA, "en": b.Texts.EN}}
	if b.Button != nil && b.Button.URL != "" {
		in.ButtonURL = b.Button.URL
		in.ButtonLabels = map[string]string{"fa": b.Button.Labels.FA, "en": b.Button.Labels.EN}
	}
	return in
}

// createBroadcast: POST /api/v1/broadcasts, a draft.
func (s *Server) createBroadcast(w http.ResponseWriter, r *http.Request) {
	var req broadcastReq
	if !readJSON(w, r, &req) {
		return
	}
	b, err := s.cfg.Domain.CreateBroadcast(r.Context(), actor(r), req.input())
	if err != nil {
		s.fail(w, "create broadcast", err)
		return
	}
	s.writeBroadcast(w, r, b)
}

// getBroadcast: GET /api/v1/broadcasts/{id}.
func (s *Server) getBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	b, err := s.cfg.Store.GetBroadcast(r.Context(), s.cfg.Store.Conn(), id, false)
	if err != nil {
		s.fail(w, "broadcast", err)
		return
	}
	s.writeBroadcast(w, r, b)
}

// updateBroadcast: PUT /api/v1/broadcasts/{id} (draft or scheduled).
func (s *Server) updateBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req broadcastReq
	if !readJSON(w, r, &req) {
		return
	}
	b, err := s.cfg.Domain.UpdateBroadcast(r.Context(), actor(r), id, req.input(), req.Version)
	if err != nil {
		s.fail(w, "update broadcast", err)
		return
	}
	s.writeBroadcast(w, r, b)
}

// deleteBroadcast: DELETE /api/v1/broadcasts/{id} (drafts).
func (s *Server) deleteBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.cfg.Domain.DeleteBroadcast(r.Context(), actor(r), id); err != nil {
		s.fail(w, "delete broadcast", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// broadcastAudience: POST /api/v1/broadcasts/audience {segment, language}.
func (s *Server) broadcastAudience(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Segment  string `json:"segment"`
		Language string `json:"language"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	total, fa, en, err := s.cfg.Domain.BroadcastAudience(r.Context(), req.Segment, req.Language)
	if err != nil {
		s.fail(w, "audience", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "languages": map[string]int64{"fa": fa, "en": en}})
}

// checkBroadcastText: POST /api/v1/broadcasts/check {lang, text}, the
// composer's live check.
func (s *Server) checkBroadcastText(w http.ResponseWriter, r *http.Request) {
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
	value, probs := i18n.ValidateMessage(req.Text, i18n.MaxMessage)
	out := make([]textProblem, 0, len(probs))
	for _, p := range probs {
		out = append(out, textProblem{Code: p.Code, Arg: p.Arg})
	}
	if value == "" {
		value = i18n.NormalizeText(req.Text)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(probs) == 0, "value": value, "problems": out,
		"length": i18n.TextLength(value), "max": i18n.MaxMessage})
}

// testBroadcast: POST /api/v1/broadcasts/test {lang, text, button}, sent to
// the staff member's own Telegram.
func (s *Server) testBroadcast(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lang   string           `json:"lang"`
		Text   string           `json:"text"`
		Button *broadcastButton `json:"button"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	me := actor(r)
	if !slices.Contains(i18n.Languages, req.Lang) {
		writeError(w, http.StatusBadRequest, "invalid", "unknown language")
		return
	}
	text, probs := i18n.ValidateMessage(req.Text, i18n.MaxMessage)
	if len(probs) > 0 {
		settingError(w, &domain.TextError{Lang: req.Lang, Key: "texts." + req.Lang, Problems: probs})
		return
	}
	t := botfiles.TestBroadcast{ChatID: me.TelegramID, Text: text}
	if req.Button != nil && req.Button.URL != "" {
		if err := domain.CheckBroadcastURL(req.Button.URL); err != nil {
			s.fail(w, "test broadcast", err)
			return
		}
		label := req.Button.Labels.FA
		if req.Lang == "en" {
			label = req.Button.Labels.EN
		}
		label, probs := i18n.ValidateLabel(label)
		if len(probs) > 0 {
			writeFieldError(w, "button.labels."+req.Lang, "a short label without < > & (at most 64 characters)")
			return
		}
		t.Button = &botfiles.TestBroadcastBtn{Text: label, URL: req.Button.URL}
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
		s.cfg.Log.Warn("test broadcast", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot did not answer; try again in a minute")
		return
	}
	s.audit(r.Context(), me, "broadcast.test", map[string]any{"lang": req.Lang, "ok": res.OK, "problem": res.Problem})
	writeJSON(w, http.StatusOK, res)
}

// sendBroadcast: POST /api/v1/broadcasts/{id}/send {at: null|unix}.
func (s *Server) sendBroadcast(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		At *int64 `json:"at"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	var at *time.Time
	if req.At != nil {
		t := time.Unix(*req.At, 0)
		at = &t
	}
	b, err := s.cfg.Domain.SendBroadcast(r.Context(), actor(r), id, at)
	if err != nil {
		s.fail(w, "send broadcast", err)
		return
	}
	s.writeBroadcast(w, r, b)
}

func (s *Server) broadcastAction(w http.ResponseWriter, r *http.Request,
	do func(*Server, *http.Request, string) (*store.Broadcast, error)) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	b, err := do(s, r, id)
	if err != nil {
		s.fail(w, "broadcast", err)
		return
	}
	s.writeBroadcast(w, r, b)
}

// pauseBroadcast: POST /api/v1/broadcasts/{id}/pause.
func (s *Server) pauseBroadcast(w http.ResponseWriter, r *http.Request) {
	s.broadcastAction(w, r, func(s *Server, r *http.Request, id string) (*store.Broadcast, error) {
		return s.cfg.Domain.PauseBroadcast(r.Context(), actor(r), id)
	})
}

// resumeBroadcast: POST /api/v1/broadcasts/{id}/resume.
func (s *Server) resumeBroadcast(w http.ResponseWriter, r *http.Request) {
	s.broadcastAction(w, r, func(s *Server, r *http.Request, id string) (*store.Broadcast, error) {
		return s.cfg.Domain.ResumeBroadcast(r.Context(), actor(r), id)
	})
}

// cancelBroadcast: POST /api/v1/broadcasts/{id}/cancel.
func (s *Server) cancelBroadcast(w http.ResponseWriter, r *http.Request) {
	s.broadcastAction(w, r, func(s *Server, r *http.Request, id string) (*store.Broadcast, error) {
		return s.cfg.Domain.CancelBroadcast(r.Context(), actor(r), id)
	})
}

// broadcastRecipients: GET /api/v1/broadcasts/{id}/recipients?status=.
func (s *Server) broadcastRecipients(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	statuses := []string{"failed", "blocked", "unknown"}
	if st := r.URL.Query().Get("status"); st != "" {
		if !slices.Contains(statuses, st) {
			writeError(w, http.StatusBadRequest, "invalid", "status must be failed, blocked or unknown")
			return
		}
		statuses = []string{st}
	}
	p, page, size := pageOf(r)
	rows, total, err := s.cfg.Store.ListRecipients(r.Context(), s.cfg.Store.Conn(), id, statuses, p)
	if err != nil {
		s.fail(w, "recipients", err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, rc := range rows {
		items = append(items, map[string]any{"user": briefOf(rc.UserID, rc.TelegramID, rc.Username), "status": rc.Status,
			"error": rc.Error, "done_at": unix(rc.DoneAt)})
	}
	writeJSON(w, http.StatusOK, list{items, total, page, size})
}
