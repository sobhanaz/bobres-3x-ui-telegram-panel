package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgauth"
)

// MiniAppMaxAge is how old the init data of a Mini App login may be.
const MiniAppMaxAge = time.Hour

// MiniAppVerify checks a Telegram Mini App's init data for core, at POST
// /internal/miniapp/verify with {"init_data"}. Only the bot holds the bot
// token (core keeps none), so core, which serves the Mini App, asks it. It
// answers 200 with {"ok":true,"user":{"id","username","first_name",
// "language_code"},"auth_date"} or {"ok":false,"problem":"malformed"|
// "signature"|"expired"}; the init data is not logged. An ok answer also
// carries "bot_username": the bot's own @username (from getMe), which core
// uses for the app's links when the owner has not set one in Branding.
func MiniAppVerify(botToken, botUsername, coreToken string, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in struct {
			InitData string `json:"init_data"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, tgauth.MaxInitData+1024)).Decode(&in); err != nil {
			http.Error(w, `body must be {"init_data"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		d, err := tgauth.Validate(in.InitData, botToken, MiniAppMaxAge, now())
		if err != nil {
			problem := "malformed"
			switch {
			case errors.Is(err, tgauth.ErrSignature):
				problem = "signature"
			case errors.Is(err, tgauth.ErrExpired):
				problem = "expired"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "problem": problem})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "auth_date": d.AuthDate.Unix(), "bot_username": botUsername, "user": map[string]any{
			"id": d.User.ID, "username": d.User.Username, "first_name": d.User.FirstName, "language_code": d.User.LanguageCode,
		}})
	})
}

// MemberChecker is what MiniAppChannelMember needs from the Telegram client.
type MemberChecker interface {
	GetChatMember(ctx context.Context, chat string, userID int64) (*tg.ChatMember, error)
}

const (
	maxMemberBody = 1 << 10
	// memberTimeout is how long the bot gives Telegram to answer: as long as
	// its own join gate does (handler.inChannel).
	memberTimeout = 3 * time.Second
)

// MiniAppChannelMember says whether a Telegram user is a member of the
// channel the store requires (join.channel), for the Mini App, at POST
// /internal/miniapp/channel-member with {"chat","user_id"}: chat is the channel
// as core has it in the settings (an @name or a -100… id, checked here as
// ChannelCheck does), user_id the customer's Telegram id. Core, which serves the
// Mini App, has no bot token, and only the channel's administrators see its
// members, which the bot is, so core asks. It answers 200 with
// {"ok":true,"member":true|false}, a member being anyone Telegram counts as one
// (the rule of the bot's own gate, tg.ChatMember.Joined). When Telegram cannot
// tell (the bot is not the channel's administrator, a timeout) it answers 429
// (flood) or 502 and {"ok":false,"problem","detail"}: what to do then is the
// caller's, and both the bot's gate and core let the customer in. The customer's
// id is not logged.
func MiniAppChannelMember(bot MemberChecker, coreToken string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	var lastWarn atomic.Int64 // when the failure was last logged (Unix seconds)
	warn := func(chat string, err error) {
		// A failing channel fails for every customer: once a minute, as the bot's gate logs it.
		now := time.Now().Unix()
		if last := lastWarn.Load(); now-last >= 60 && lastWarn.CompareAndSwap(last, now) {
			log.Warn("cannot check members of the join channel for the Mini App; core lets customers in (is the bot its admin?)",
				"channel", chat, "err", err)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in struct {
			Chat   string `json:"chat"`
			UserID int64  `json:"user_id"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMemberBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			problem(w, http.StatusBadRequest, ProblemBadRequest, `body must be {"chat","user_id"}`)
			return
		}
		if !channelRe.MatchString(in.Chat) || in.UserID <= 0 {
			problem(w, http.StatusBadRequest, ProblemBadRequest, "chat must be @name or -100… id and user_id a user's Telegram id")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), memberTimeout)
		defer cancel()
		m, err := bot.GetChatMember(ctx, in.Chat, in.UserID)
		if err != nil {
			warn(in.Chat, err)
			_, detail := telegramError(err)
			if wait, ok := tg.IsFlood(err); ok {
				w.Header().Set("Retry-After", strconv.FormatInt(int64(wait/time.Second), 10))
				problem(w, http.StatusTooManyRequests, ProblemFlood, detail)
				return
			}
			problem(w, http.StatusBadGateway, ProblemError, detail)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "member": m.Joined()})
	})
}
