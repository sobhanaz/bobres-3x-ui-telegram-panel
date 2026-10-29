package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/broadcast"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
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

// channelRe is a required chat as core checks it (join.channel and the extra
// chats): a public channel or group's @name, or a private one's -100… id.
var channelRe = regexp.MustCompile(`^(@[A-Za-z][A-Za-z0-9_]{4,31}|-100[0-9]{5,15})$`)

// Problems ChannelCheck reports.
const (
	ProblemNotFound   = "not_found"
	ProblemNotAdmin   = "not_admin"
	ProblemNotChannel = "not_channel"
	ProblemError      = "error"
)

// ChannelCheck tells the dashboard whether the bot can enforce joining a
// channel or group, at GET /internal/channel-check?chat=<@name|-100id>: the
// chat exists and the bot is one of its administrators (Telegram shows other
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
	// A required chat is a channel or a group (a supergroup is a group).
	if ch.Type != "channel" && ch.Type != "supergroup" && ch.Type != "group" {
		return fail(ProblemNotChannel, "this chat is a "+ch.Type+", not a channel or a group")
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

// MessageSender is what BroadcastTest needs from the Telegram client.
type MessageSender interface {
	SendMessage(ctx context.Context, chatID int64, text string, kb *tg.Keyboard) (*tg.Message, error)
}

// More problems BroadcastTest reports (beside ProblemError).
const (
	ProblemBlocked    = "blocked"
	ProblemBadRequest = "bad_request"
)

// maxTestBody bounds a test broadcast request (a 4096-character text fits).
const maxTestBody = 16 << 10

// BroadcastTest sends a broadcast's message to one chat, the staff member's
// own Telegram, for the dashboard's "send to me", at POST
// /internal/broadcasts/test with {"chat_id", "text", "button": {"text",
// "url"} | null, "settle": label} (settle: a Settle / Top up button, as under
// a message to resellers). It checks the message again and answers 200 with
// {"ok":true,"message_id"} or {"ok":false,"problem":"blocked"|"bad_request"|
// "error","detail"}.
func BroadcastTest(bot MessageSender, coreToken string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
			Button *struct {
				Text string `json:"text"`
				URL  string `json:"url"`
			} `json:"button"`
			Settle string `json:"settle"` // the label of a Settle / Top up button ("": none)
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTestBody)).Decode(&in); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, `body must be {"chat_id", "text", "button"}`, http.StatusBadRequest)
			return
		}
		if in.ChatID <= 0 {
			http.Error(w, "chat_id must be a user's Telegram id", http.StatusBadRequest)
			return
		}
		answer := func(v map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(v)
		}
		fail := func(problem, detail string) {
			answer(map[string]any{"ok": false, "problem": problem, "detail": detail})
		}
		var label, link string
		var kb *tg.Keyboard
		if in.Button != nil {
			label, link = in.Button.Text, in.Button.URL
			kb = (&tg.Keyboard{}).Row(tg.Link(label, link))
		}
		if p := broadcast.Problem(in.Text, link, label); p != "" {
			fail(ProblemBadRequest, p)
			return
		}
		if in.Settle != "" {
			if _, probs := i18n.ValidateLabel(in.Settle); len(probs) > 0 {
				fail(ProblemBadRequest, "the settle button label cannot be used")
				return
			}
			if kb == nil {
				kb = &tg.Keyboard{}
			}
			kb.Row(tg.CB(in.Settle, broadcast.SettleCallback))
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		m, err := bot.SendMessage(ctx, in.ChatID, in.Text, kb)
		if err != nil {
			code, detail := telegramError(err)
			switch {
			case tg.IsBlocked(err):
				fail(ProblemBlocked, detail)
			case code == http.StatusBadRequest:
				fail(ProblemBadRequest, detail)
			default:
				log.Warn("test broadcast not sent", "chat", in.ChatID, "err", err)
				fail(ProblemError, detail)
			}
			return
		}
		answer(map[string]any{"ok": true, "message_id": m.MessageID})
	})
}
