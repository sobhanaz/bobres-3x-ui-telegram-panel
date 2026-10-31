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

// ChatPoster is what AnnounceChannel needs from the Telegram client.
type ChatPoster interface {
	SendChatMessage(ctx context.Context, chat string, text string, kb *tg.Keyboard) (*tg.Message, error)
}

// startPayloadRe is the start payload of a product card's link: p_<plan id>, s_<supplier product>, l_<tier>_<months> (Telegram takes up
// to 64 letters, digits, _ and -).
var startPayloadRe = regexp.MustCompile(`^(p_[0-9a-f]{32}|s_[a-z2-7]{10}|l_(standard|pro)_(1|12))$`)

// More problems AnnounceChannel reports (beside ProblemNotAdmin, ProblemBadRequest and ProblemError).
const ProblemNotFoundChat = "not_found"

// AnnounceChannel posts a product announcement in the store's channel, at POST /internal/announce/channel with {"chat", "text", "label",
// "start"}: the card, and a Buy button labelled label that opens t.me/<bot>?start=<start>, the product in this bot (botUsername). It
// answers 200 with {"ok":true,"message_id"} or {"ok":false,"problem":"not_admin"|"not_found"|"bad_request"|"error","detail"}.
func AnnounceChannel(bot ChatPoster, botUsername, coreToken string, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !fromCore(r, coreToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var in struct {
			Chat  string `json:"chat"`
			Text  string `json:"text"`
			Label string `json:"label"`
			Start string `json:"start"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTestBody)).Decode(&in); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, `body must be {"chat", "text", "label", "start"}`, http.StatusBadRequest)
			return
		}
		if !channelRe.MatchString(in.Chat) || !startPayloadRe.MatchString(in.Start) {
			http.Error(w, "chat must be @name or -100… id, start a product's payload", http.StatusBadRequest)
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
		if botUsername == "" {
			fail(ProblemError, "the bot's @username is not known")
			return
		}
		link := "https://t.me/" + botUsername + "?start=" + in.Start
		if p := broadcast.Problem(in.Text, link, in.Label); p != "" {
			fail(ProblemBadRequest, p)
			return
		}
		buy := tg.Link(in.Label, link)
		buy.Style = i18n.ButtonStyle(broadcast.BuyText)
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		m, err := bot.SendChatMessage(ctx, in.Chat, in.Text, (&tg.Keyboard{}).Row(buy))
		if err != nil {
			code, detail := telegramError(err)
			switch code {
			case http.StatusForbidden:
				fail(ProblemNotAdmin, detail)
			case http.StatusBadRequest:
				switch {
				case chatNotFoundRe.MatchString(detail):
					fail(ProblemNotFoundChat, detail)
				case noRightsRe.MatchString(detail):
					fail(ProblemNotAdmin, detail)
				default:
					fail(ProblemBadRequest, detail)
				}
			default:
				log.Warn("announcement not posted in the channel", "chat", in.Chat, "err", err)
				fail(ProblemError, detail)
			}
			return
		}
		answer(map[string]any{"ok": true, "message_id": m.MessageID})
	})
}

// chatNotFoundRe and noRightsRe read Telegram's refusals of a post: no such chat (or the bot is not in it), and not allowed to post.
var (
	chatNotFoundRe = regexp.MustCompile(`(?i)chat not found`)
	noRightsRe     = regexp.MustCompile(`(?i)administrator rights|not enough rights|have no rights`)
)
