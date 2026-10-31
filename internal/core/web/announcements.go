package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
)

// New product announcements (docs/04-dashboard.md, "Announcing a product"): the Announce dialog of the plan, the supplier product and the
// license pages. Everything is broadcasts.write, like the broadcasts the private messages are.

type announceProductJSON struct {
	Kind        string            `json:"kind"`
	ID          string            `json:"id"`
	Names       map[string]string `json:"names"`
	Kinds       map[string]string `json:"kinds"`
	Price       int64             `json:"price"`
	Currency    string            `json:"currency"`
	Scale       int               `json:"scale"`
	Toman       int64             `json:"toman"` // the Toman a price in another currency costs now (0: unknown, or the price is in Toman)
	Days        *int32            `json:"days"`
	Months      int               `json:"months"`
	Traffic     *int64            `json:"traffic_bytes"`
	Stock       *int32            `json:"stock"`
	StartParam  string            `json:"start_param"`
	BuyCallback string            `json:"buy_callback"`
}

func announceProductOf(p *domain.AnnounceProduct) announceProductJSON {
	return announceProductJSON{Kind: p.Kind, ID: p.ID, Names: p.Names, Kinds: p.Kinds, Price: p.Price, Currency: p.Currency,
		Scale: bmoney.Scale(p.Currency), Toman: p.Toman, Days: p.Days, Months: p.Months, Traffic: p.Traffic, Stock: p.Stock, StartParam: p.Start,
		BuyCallback: p.Callback}
}

type announcementJSON struct {
	ID               string            `json:"id"`
	ProductKind      string            `json:"product_kind"`
	ProductID        string            `json:"product_id"`
	ProductName      string            `json:"product_name"`
	Texts            map[string]string `json:"texts"`
	ToChannel        bool              `json:"to_channel"`
	ToPrivate        bool              `json:"to_private"`
	ChannelLangs     string            `json:"channel_langs"`
	Forced           bool              `json:"forced"`
	Status           string            `json:"status"`
	DueAt            int64             `json:"due_at"`
	SentAt           *int64            `json:"sent_at"`
	BroadcastID      string            `json:"broadcast_id"`
	ChannelChat      string            `json:"channel_chat"`
	ChannelStatus    string            `json:"channel_status"`
	ChannelMessageID int64             `json:"channel_message_id"`
	ChannelError     string            `json:"channel_error"`
	CreatedBy        *userBrief        `json:"created_by"`
	CreatedAt        int64             `json:"created_at"`
}

func (s *Server) announcementsJSON(r *http.Request, list []store.Announcement) []announcementJSON {
	ids := make([]string, 0, len(list))
	for _, a := range list {
		ids = append(ids, a.CreatedBy)
	}
	users := s.usersByID(r.Context(), ids)
	out := make([]announcementJSON, 0, len(list))
	for _, a := range list {
		out = append(out, announcementJSON{ID: a.ID, ProductKind: a.ProductKind, ProductID: a.ProductID, ProductName: a.ProductName, Texts: a.Texts,
			ToChannel: a.ToChannel, ToPrivate: a.ToPrivate, ChannelLangs: a.ChannelLangs, Forced: a.Forced, Status: a.Status, DueAt: a.DueAt.Unix(),
			SentAt: unix(a.SentAt), BroadcastID: a.BroadcastID, ChannelChat: a.ChannelChat, ChannelStatus: a.ChannelStatus,
			ChannelMessageID: a.ChannelMessageID, ChannelError: a.ChannelError, CreatedBy: users[a.CreatedBy], CreatedAt: a.CreatedAt.Unix()})
	}
	return out
}

// clockText writes minutes since midnight as HH:MM.
func clockText(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// announceDraft: GET /api/v1/announcements/draft?kind=&id=: what the Announce dialog starts from: the product as a card shows it now, the
// cards prefilled from it, the placeholders, the settings and limits, who the private messages would reach and the product's last
// announcements.
func (s *Server) announceDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	kind, id := r.URL.Query().Get("kind"), r.URL.Query().Get("id")
	p, err := s.cfg.Domain.AnnounceProductOf(ctx, kind, id)
	if err != nil {
		s.fail(w, "announcement draft", err)
		return
	}
	cfg, err := s.cfg.Domain.AnnounceSettings(ctx)
	if err != nil {
		s.fail(w, "announcement draft", err)
		return
	}
	total, fa, en, err := s.cfg.Domain.AnnounceAudience(ctx)
	if err != nil {
		s.fail(w, "announcement draft", err)
		return
	}
	recent, _, err := s.cfg.Domain.Announcements(ctx, p.Kind, p.ID, store.Page{Limit: 5})
	if err != nil {
		s.fail(w, "announcement draft", err)
		return
	}
	labels := map[string]string{}
	for _, l := range i18n.Languages {
		labels[l] = s.cfg.Domain.AnnounceBuyLabel(ctx, l)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"product":      announceProductOf(p),
		"texts":        domain.DefaultAnnounceTexts(p),
		"placeholders": domain.AnnouncePlaceholders,
		"buy_labels":   labels,
		"settings": map[string]any{"enabled": cfg.Enabled, "channel": cfg.Channel, "channel_langs": cfg.ChannelLangs, "default_language": domain.DefaultLanguage(),
			"daily_max": cfg.DailyMax, "quiet_start": clockText(cfg.QuietStart), "quiet_end": clockText(cfg.QuietEnd)},
		"audience": map[string]any{"total": total, "languages": map[string]int64{"fa": fa, "en": en}},
		"recent":   s.announcementsJSON(r, recent),
	})
}

// checkAnnounceText: POST /api/v1/announcements/check {lang, text}, the composer's live check (placeholders included).
func (s *Server) checkAnnounceText(w http.ResponseWriter, r *http.Request) {
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
	value, probs := domain.AnnounceTextProblems(req.Lang, req.Text)
	out := make([]textProblem, 0, len(probs))
	for _, p := range probs {
		out = append(out, textProblem{Code: p.Code, Arg: p.Arg})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(probs) == 0, "value": value, "problems": out,
		"length": i18n.TextLength(value), "max": i18n.MaxMessage, "placeholders": domain.AnnouncePlaceholders})
}

type announcePreviewReq struct {
	Kind  string            `json:"kind"`
	ID    string            `json:"id"`
	Lang  string            `json:"lang"`
	Texts map[string]string `json:"texts"`
}

// previewAnnouncement: POST /api/v1/announcements/preview {kind, id, lang, texts}: the private message in lang and the channel post, written
// with the product's values now.
func (s *Server) previewAnnouncement(w http.ResponseWriter, r *http.Request) {
	var req announcePreviewReq
	if !readJSON(w, r, &req) {
		return
	}
	p, err := s.cfg.Domain.PreviewAnnouncement(r.Context(), req.Kind, req.ID, req.Lang, req.Texts)
	if err != nil {
		s.fail(w, "announcement preview", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": p.Text, "channel": p.Channel, "channel_lang": p.ChannelLang, "buy_label": p.BuyLabel})
}

// testAnnouncement: POST /api/v1/announcements/test {kind, id, lang, texts}: the private message in lang, with its Buy button, sent to the
// staff member's own Telegram.
func (s *Server) testAnnouncement(w http.ResponseWriter, r *http.Request) {
	var req announcePreviewReq
	if !readJSON(w, r, &req) {
		return
	}
	p, err := s.cfg.Domain.PreviewAnnouncement(r.Context(), req.Kind, req.ID, req.Lang, req.Texts)
	if err != nil {
		s.fail(w, "announcement test", err)
		return
	}
	if s.cfg.Bot == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot's address is not configured (BOBRES_BOT_URL)")
		return
	}
	me := actor(r)
	if !s.tests.allow("test:"+me.ID, s.now()) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "at most 5 test sends a minute")
		return
	}
	res, err := s.cfg.Bot.TestBroadcast(r.Context(), botfiles.TestBroadcast{ChatID: me.TelegramID, Text: p.Text,
		Buy: &botfiles.TestBroadcastCB{Text: p.BuyLabel, Data: p.Product.Callback}})
	if err != nil {
		s.cfg.Log.Warn("test announcement", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot did not answer; try again in a minute")
		return
	}
	s.audit(r.Context(), me, "announcement.test", map[string]any{"product_kind": p.Product.Kind, "product_id": p.Product.ID, "lang": req.Lang,
		"ok": res.OK, "problem": res.Problem})
	writeJSON(w, http.StatusOK, res)
}

// announceChannelCheck is whether the bot may post in the announcement channel: nil and an answer written when it cannot be known.
func (s *Server) announceChannelCheck(w http.ResponseWriter, r *http.Request) (string, *botfiles.ChannelCheck, bool) {
	cfg, err := s.cfg.Domain.AnnounceSettings(r.Context())
	if err != nil {
		s.fail(w, "announcement channel", err)
		return "", nil, false
	}
	if cfg.Channel == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "channel", "problem": "no_channel",
			"message": "no channel is set: set the announcement channel (or the required channel) in Settings"})
		return "", nil, false
	}
	if s.cfg.Bot == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot's address is not configured (BOBRES_BOT_URL)")
		return "", nil, false
	}
	res, err := s.cfg.Bot.CheckChannel(r.Context(), cfg.Channel)
	if err != nil {
		s.cfg.Log.Warn("announcement channel check", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the bot did not answer; try again in a minute")
		return "", nil, false
	}
	return cfg.Channel, res, true
}

// checkAnnounceChannel: POST /api/v1/announcements/channel-check: whether the bot is an admin of the announcement channel who may post.
func (s *Server) checkAnnounceChannel(w http.ResponseWriter, r *http.Request) {
	chat, res, ok := s.announceChannelCheck(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat": chat, "ok": res.OK && res.CanPost, "title": res.Title, "bot_admin": res.BotAdmin,
		"can_post": res.CanPost, "problem": res.Problem, "detail": res.Detail})
}

// sendAnnouncement: POST /api/v1/announcements {kind, id, texts, channel, private, force}: checked and put in line (now, or at the end of the
// quiet hours). The channel is checked first: the bot must be its admin, allowed to post. 409 {error: "repeat", last_at} for a product
// announced in the last 24 hours (send again with force), {error: "limit", max, day} past the daily limit, {error: "channel", problem,
// detail} when the bot cannot post in the channel.
func (s *Server) sendAnnouncement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind    string            `json:"kind"`
		ID      string            `json:"id"`
		Texts   map[string]string `json:"texts"`
		Channel bool              `json:"channel"`
		Private bool              `json:"private"`
		Force   bool              `json:"force"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Channel {
		chat, res, ok := s.announceChannelCheck(w, r)
		if !ok {
			return
		}
		if !res.OK || !res.CanPost {
			problem := res.Problem
			if res.OK {
				problem = "cannot_post"
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": "channel", "problem": problem, "detail": res.Detail, "chat": chat,
				"message": "the bot cannot post in " + chat + ": make it an admin of the channel allowed to post messages"})
			return
		}
	}
	res, err := s.cfg.Domain.SendAnnouncement(r.Context(), actor(r), domain.AnnounceInput{Kind: req.Kind, ProductID: req.ID, Texts: req.Texts,
		Channel: req.Channel, Private: req.Private, Force: req.Force})
	var (
		rep *domain.AnnounceRepeatError
		lim *domain.AnnounceLimitError
	)
	switch {
	case errors.As(err, &rep):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "repeat", "last_at": rep.Last.Unix(),
			"message": "this product was announced less than 24 hours ago"})
		return
	case errors.As(err, &lim):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "limit", "max": lim.Max, "day": lim.Day,
			"message": "the store's daily limit of announcements is reached"})
		return
	case err != nil:
		s.fail(w, "send announcement", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"announcement": s.announcementsJSON(r, []store.Announcement{*res.Announcement})[0],
		"queued": res.Queued, "audience": res.Audience})
}

// listAnnouncements: GET /api/v1/announcements?kind=&id=&page=&size=: announcements, newest first (of one product, a kind, or all).
func (s *Server) listAnnouncements(w http.ResponseWriter, r *http.Request) {
	kind, id := r.URL.Query().Get("kind"), r.URL.Query().Get("id")
	if kind != "" && !slices.Contains(domain.AnnounceKinds, kind) {
		writeError(w, http.StatusBadRequest, "invalid", "kind: plan, supplier or license")
		return
	}
	p, page, size := pageOf(r)
	rows, total, err := s.cfg.Domain.Announcements(r.Context(), kind, id, p)
	if err != nil {
		s.fail(w, "list announcements", err)
		return
	}
	writeJSON(w, http.StatusOK, list{s.announcementsJSON(r, rows), total, page, size})
}

// cancelAnnouncement: POST /api/v1/announcements/{id}/cancel: one still waiting for the end of the quiet hours.
func (s *Server) cancelAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	a, err := s.cfg.Domain.CancelAnnouncement(r.Context(), actor(r), id)
	if err != nil {
		s.fail(w, "cancel announcement", err)
		return
	}
	writeJSON(w, http.StatusOK, s.announcementsJSON(r, []store.Announcement{*a})[0])
}
