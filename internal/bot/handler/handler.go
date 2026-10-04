// Package handler is the bot's conversation logic: it turns Telegram updates
// into calls on core (the bot's only backend) and replies in the user's
// language. It holds no business rules; core enforces them.
package handler

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Telegram is the part of the Bot API the handler uses.
type Telegram interface {
	SendMessage(ctx context.Context, chatID int64, text string, kb *tg.Keyboard) (*tg.Message, error)
	EditMessageText(ctx context.Context, chatID int64, messageID int, text string, kb *tg.Keyboard) error
	EditMessageReplyMarkup(ctx context.Context, chatID int64, messageID int, kb *tg.Keyboard) error
	AnswerCallback(ctx context.Context, callbackID, text string, alert bool) error
	SendPhoto(ctx context.Context, chatID int64, p tg.Photo, caption string, kb *tg.Keyboard) (*tg.Message, error)
	SendInvoice(ctx context.Context, chatID int64, inv tg.Invoice) (*tg.Message, error)
	AnswerPreCheckoutQuery(ctx context.Context, queryID string, ok bool, errMsg string) error
}

// Limiter decides whether a user may be served now (nil: no limit).
type Limiter interface {
	Allow(ctx context.Context, key string, window time.Duration, max int) (bool, error)
}

// Config tunes the handler.
type Config struct {
	// AdminTelegramID gets new-payment and support notifications.
	AdminTelegramID int64
	// BotName is the brand shown until branding.name is set (from getMe).
	BotName string
}

// Handler serves updates.
type Handler struct {
	core    corev1.CoreServiceClient
	tg      Telegram
	state   state.Store
	cat     *i18n.Catalog
	limiter Limiter
	cfg     Config
	log     *slog.Logger

	mu       sync.RWMutex
	settings map[string]string

	gwMu      sync.Mutex
	gwMethods map[string]methodsEntry // currency -> usable automated methods
}

// New builds a Handler. limiter may be nil.
func New(core corev1.CoreServiceClient, t Telegram, st state.Store, cat *i18n.Catalog, limiter Limiter, cfg Config, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Handler{core: core, tg: t, state: st, cat: cat, limiter: limiter, cfg: cfg, log: log,
		settings: map[string]string{}, gwMethods: map[string]methodsEntry{}}
}

// RefreshSettings reloads branding, payment details and text overrides.
func (h *Handler) RefreshSettings(ctx context.Context) error {
	s, err := h.core.GetSettings(ctx, &corev1.GetSettingsRequest{})
	if err != nil {
		return err
	}
	m := make(map[string]string, len(s.GetValues()))
	for k, raw := range s.GetValues() {
		var v string
		if json.Unmarshal([]byte(raw), &v) == nil {
			m[k] = v
		}
	}
	h.mu.Lock()
	h.settings = m
	h.mu.Unlock()
	h.cat.SetOverrides(m)
	// Prices and rates may have changed (e.g. payments.stars_rate): the usable
	// payment methods are asked again on the next screen.
	h.gwMu.Lock()
	clear(h.gwMethods)
	h.gwMu.Unlock()
	return nil
}

func (h *Handler) setting(key string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.settings[key]
}

func (h *Handler) brand() string {
	if b := h.setting("branding.name"); b != "" {
		return b
	}
	if h.cfg.BotName != "" {
		return h.cfg.BotName
	}
	return "VPN"
}

// req is one update being served.
type req struct {
	ctx    context.Context
	h      *Handler
	from   *tg.User
	chatID int64
	msg    *tg.Message       // the incoming message, or the callback's message
	cb     *tg.CallbackQuery // nil for messages
	user   *corev1.User      // nil until registered
	lang   string
	toast  string // callback answer text
	alert  bool
}

func (r *req) t(key string, args ...any) string { return r.h.cat.T(r.lang, key, args...) }

func (r *req) money(amount int64, currency string) string {
	return i18n.Money(r.lang, amount, currency)
}

// show replaces the pressed menu (callbacks) or sends a new message.
func (r *req) show(text string, kb *tg.Keyboard) {
	if r.cb != nil && r.cb.Message != nil {
		err := r.h.tg.EditMessageText(r.ctx, r.chatID, r.cb.Message.MessageID, text, kb)
		if err == nil {
			return
		}
		// Photos (QR, receipts) cannot become text: send a new message.
		r.h.log.Debug("edit failed; sending instead", "err", err)
	}
	r.send(text, kb)
}

// send always sends a new message.
func (r *req) send(text string, kb *tg.Keyboard) {
	if _, err := r.h.tg.SendMessage(r.ctx, r.chatID, text, kb); err != nil {
		r.h.log.Warn("send failed", "chat", r.chatID, "err", err)
	}
}

func (r *req) setState(scene string, data map[string]string) {
	if err := r.h.state.Set(r.ctx, r.from.ID, state.State{Scene: scene, Data: data}); err != nil {
		r.h.log.Warn("state save failed", "err", err)
	}
}

func (r *req) clearState() {
	if err := r.h.state.Clear(r.ctx, r.from.ID); err != nil {
		r.h.log.Warn("state clear failed", "err", err)
	}
}

func (r *req) isStaff() bool {
	return r.user != nil && r.user.GetStatus() == "active" && (r.user.GetRole() == "admin" || r.user.GetRole() == "owner")
}

// fail tells the user what went wrong, in their language, and logs details.
func (r *req) fail(err error) {
	key := "error.generic"
	st, _ := status.FromError(err)
	msg := strings.ToLower(st.Message())
	switch {
	case st.Code() == codes.PermissionDenied && strings.Contains(msg, "banned"):
		key = "error.banned"
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "trial already used"):
		key = "trial.used"
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "trial"):
		key = "trial.unavailable"
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "still being prepared"):
		key = "sub.not_ready"
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "cannot change state"):
		key = "pay.already_submitted"
	case st.Code() == codes.AlreadyExists && strings.Contains(msg, "already submitted"):
		key = "pay.duplicate"
	case st.Code() == codes.Unavailable || st.Code() == codes.DeadlineExceeded:
		key = "error.unavailable"
	default:
		r.h.log.Warn("request failed", "user", r.from.ID, "code", st.Code().String(), "err", err)
	}
	if r.cb != nil {
		r.toast, r.alert = r.t(key), true
		return
	}
	r.send(r.t(key), homeKeyboard(r))
}

// Handle serves one update. It never returns an error for user mistakes;
// errors are logged and answered in the chat.
func (h *Handler) Handle(ctx context.Context, u tg.Update) {
	from := u.Sender()
	if from == nil || from.IsBot {
		return
	}
	if u.PreCheckoutQuery != nil {
		// Side-effect free and due within 10 seconds: no de-duplication, no rate limit.
		h.onPreCheckout(ctx, u.PreCheckoutQuery)
		return
	}
	if first, err := h.state.Once(ctx, "upd:"+strconv.FormatInt(u.UpdateID, 10), 24*time.Hour); err == nil && !first {
		return // Telegram re-delivered an update we already handled
	}
	r := &req{ctx: ctx, h: h, from: from, cb: u.CallbackQuery, msg: u.Message}
	if r.cb != nil {
		r.msg = r.cb.Message
	}
	if r.msg == nil || r.msg.Chat.Type != "private" {
		if r.cb != nil {
			_ = h.tg.AnswerCallback(ctx, r.cb.ID, "", false)
		}
		return // the bot only talks in private chats
	}
	r.chatID = r.msg.Chat.ID
	defer func() {
		if r.cb != nil {
			if err := h.tg.AnswerCallback(ctx, r.cb.ID, r.toast, r.alert); err != nil {
				h.log.Debug("answer callback failed", "err", err)
			}
		}
	}()

	if user, err := h.core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: from.ID}}); err == nil {
		r.user, r.lang = user, user.GetLanguage()
	} else if status.Code(err) != codes.NotFound {
		r.lang = i18n.Normalize(from.LanguageCode)
		r.fail(err)
		return
	} else {
		r.lang = i18n.Normalize(from.LanguageCode)
	}

	if r.cb == nil && r.msg.SuccessfulPayment != nil {
		h.onStarsPaid(r) // the Stars are already taken: never rate-limited
		return
	}
	if h.limiter != nil {
		if ok, err := h.limiter.Allow(ctx, "bot:rl:"+strconv.FormatInt(from.ID, 10), 10*time.Second, 20); err == nil && !ok {
			if r.cb != nil {
				r.toast = r.t("error.rate")
			}
			return
		}
	}

	if r.cb != nil {
		h.routeCallback(r, r.cb.Data)
		return
	}
	h.routeMessage(r)
}

func (h *Handler) routeCallback(r *req, data string) {
	verb, rest, _ := strings.Cut(data, ":")
	if r.user == nil && verb != "lang" {
		r.toast = r.t("error.expired")
		h.askLanguage(r)
		return
	}
	switch verb {
	case "lang":
		h.onLanguage(r, rest)
	case "home":
		r.clearState()
		h.showHome(r)
	case "cancel":
		r.clearState()
		r.toast = r.t("cancelled")
		h.showHome(r)
	case "buy":
		h.showPlans(r)
	case "plan":
		h.showPlan(r, rest)
	case "pay":
		h.onPay(r, rest)
	case "subs":
		h.showServices(r)
	case "sub":
		h.showService(r, rest)
	case "qr":
		h.sendQR(r, rest)
	case "wallet":
		h.showWallet(r)
	case "wtop":
		h.showTopupAmounts(r)
	case "wamt":
		h.showTopupMethods(r, rest)
	case "wpay":
		h.onTopupPay(r, rest)
	case "chk":
		h.onCheckPayment(r, rest)
	case "wcustom":
		r.setState(sceneTopupAmount, nil)
		r.show(r.t("wallet.enter_amount", "min", r.money(minTopup, "IRT"), "max", r.money(maxTopup, "IRT")), cancelKeyboard(r))
	case "whist":
		h.showHistory(r)
	case "trial":
		h.onTrial(r)
	case "support":
		h.onSupport(r)
	case "adm":
		h.routeAdmin(r, rest)
	default:
		r.toast = r.t("error.expired")
		h.showHome(r)
	}
}

func (h *Handler) routeMessage(r *req) {
	text := strings.TrimSpace(r.msg.Text)
	if strings.HasPrefix(text, "/") {
		cmd, args, _ := strings.Cut(text, " ")
		cmd = strings.ToLower(strings.SplitN(cmd, "@", 2)[0]) // /start@MyBot
		h.onCommand(r, cmd, strings.TrimSpace(args))
		return
	}
	if r.user == nil {
		h.askLanguage(r)
		return
	}
	st, err := h.state.Get(r.ctx, r.from.ID)
	if err != nil {
		h.log.Warn("state load failed", "err", err)
	}
	if st.Scene != "" {
		h.onScene(r, st)
		return
	}
	h.showHome(r)
}

func (h *Handler) onCommand(r *req, cmd, args string) {
	switch cmd {
	case "/start":
		h.onStart(r)
	case "/menu":
		r.clearState()
		h.showHome(r)
	case "/cancel":
		r.clearState()
		r.send(r.t("cancelled"), nil)
		h.showHome(r)
	case "/paysupport":
		h.onPaySupport(r)
	case "/admin", "/set", "/plan_add", "/trial":
		if r.user == nil {
			h.askLanguage(r)
			return
		}
		h.onAdminCommand(r, cmd, args)
	default:
		if r.user == nil {
			h.askLanguage(r)
			return
		}
		h.showHome(r)
	}
}

func (h *Handler) onScene(r *req, st state.State) {
	switch st.Scene {
	case sceneReceipt, sceneReference, sceneTXID:
		h.onProof(r, st)
	case sceneTopupAmount:
		h.onTopupAmount(r)
	case sceneTicket:
		h.onTicket(r)
	case sceneAdminFind, sceneAdminAdjust, sceneAdminReason:
		h.onAdminScene(r, st)
	default:
		r.clearState()
		h.showHome(r)
	}
}

// Scenes: what the user is expected to send next.
const (
	sceneReceipt     = "receipt"   // photo of a card receipt
	sceneReference   = "reference" // bank reference after a photo without caption
	sceneTXID        = "txid"      // crypto transaction hash
	sceneTopupAmount = "topup_amount"
	sceneTicket      = "ticket"
	sceneAdminFind   = "adm_find"
	sceneAdminAdjust = "adm_adjust"
	sceneAdminReason = "adm_reason"
)

// nonce makes each rendered payment menu unique: a double tap on one button
// repeats the same order (idempotent), while opening the plan again starts
// a new purchase.
func nonce() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func esc(s string) string { return html.EscapeString(s) }

// displayName is how a user is shown to admins.
func displayName(u *corev1.User) string {
	if u == nil {
		return "?"
	}
	if u.GetUsername() != "" {
		return "@" + u.GetUsername()
	}
	return fmt.Sprintf("id %d", u.GetTelegramId())
}

func homeKeyboard(r *req) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(tg.CB(r.t("btn.home"), "home"))
}

func cancelKeyboard(r *req) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(tg.CB(r.t("btn.cancel"), "cancel"))
}

func backHome(r *req, back string) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(tg.CB(r.t("btn.back"), back), tg.CB(r.t("btn.home"), "home"))
}
