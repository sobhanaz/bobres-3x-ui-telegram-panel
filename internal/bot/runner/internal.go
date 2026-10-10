package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// More /internal/ endpoints for core, beside Files and with the same
// protection: core's service token, never routed from outside.

// Refresh reloads the store settings at POST /internal/settings/refresh,
// which core calls after the dashboard changed settings or texts: 204 once
// they are reloaded, 503 when they could not be read.
func Refresh(refresh func(context.Context) error, coreToken string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// Core waits only briefly; the reload finishes even if it gave up.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer cancel()
		if err := refresh(ctx); err != nil {
			log.Warn("settings refresh asked by core failed", "err", err)
			http.Error(w, "settings not reloaded", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// ChatChecker is what ChannelCheck needs from the Telegram client.
type ChatChecker interface {
	GetChat(ctx context.Context, chat string) (*tg.Chat, error)
	GetChatMember(ctx context.Context, chat string, userID int64) (*tg.ChatMember, error)
}

// channelRe is a join.channel value, as core checks it: a public channel's
// @name or a private channel's -100… id.
var channelRe = regexp.MustCompile(`^(@[A-Za-z][A-Za-z0-9_]{4,31}|-100[0-9]{5,15})$`)

// Problems ChannelCheck reports.
const (
	ProblemNotFound   = "not_found"
	ProblemNotAdmin   = "not_admin"
	ProblemNotChannel = "not_channel"
	ProblemError      = "error"
)

// ChannelCheck tells the dashboard whether the bot can enforce joining a
// channel, at GET /internal/channel-check?chat=<@name|-100id>: the channel
// exists and the bot is one of its administrators (Telegram shows other
// members to admins only). It answers 200 with {"ok":true,"title","bot_admin"}
// or {"ok":false,"problem","detail"}.
func ChannelCheck(bot ChatChecker, botID int64, coreToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		chat := r.URL.Query().Get("chat")
		if !channelRe.MatchString(chat) {
			http.Error(w, "chat must be @name or -100… id", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(checkChannel(ctx, bot, botID, chat))
	})
}

func checkChannel(ctx context.Context, bot ChatChecker, botID int64, chat string) map[string]any {
	fail := func(problem, detail string) map[string]any {
		return map[string]any{"ok": false, "problem": problem, "detail": detail}
	}
	ch, err := bot.GetChat(ctx, chat)
	if err != nil {
		switch code, detail := telegramError(err); code {
		case http.StatusBadRequest:
			return fail(ProblemNotFound, detail)
		case http.StatusForbidden:
			return fail(ProblemNotAdmin, detail)
		default:
			return fail(ProblemError, detail)
		}
	}
	if ch.Type != "channel" {
		return fail(ProblemNotChannel, "this chat is a "+ch.Type+", not a channel")
	}
	m, err := bot.GetChatMember(ctx, chat, botID)
	if err != nil {
		switch code, detail := telegramError(err); code {
		case http.StatusBadRequest, http.StatusForbidden:
			return fail(ProblemNotAdmin, detail)
		default:
			return fail(ProblemError, detail)
		}
	}
	if !m.IsAdmin() {
		return fail(ProblemNotAdmin, "the bot is not an administrator of this channel (its status: "+m.Status+")")
	}
	return map[string]any{"ok": true, "title": ch.Title, "bot_admin": true}
}

// telegramError is the HTTP code and text of a Bot API error (0 and the
// error's text when Telegram did not answer). Neither contains the token.
func telegramError(err error) (int, string) {
	var ae *tg.APIError
	if errors.As(err, &ae) {
		return ae.Code, ae.Description
	}
	return 0, err.Error()
}
