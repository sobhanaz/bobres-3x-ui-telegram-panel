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
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/settings"
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
	SendDocument(ctx context.Context, chatID int64, fileID, caption string, kb *tg.Keyboard) (*tg.Message, error)
	SendInvoice(ctx context.Context, chatID int64, inv tg.Invoice) (*tg.Message, error)
	AnswerPreCheckoutQuery(ctx context.Context, queryID string, ok bool, errMsg string) error
	GetChat(ctx context.Context, chat string) (*tg.Chat, error)
	GetChatMember(ctx context.Context, chat string, userID int64) (*tg.ChatMember, error)
}

// Limiter decides whether a user may be served now (nil: no limit).
type Limiter interface {
	Allow(ctx context.Context, key string, window time.Duration, max int) (bool, error)
}

// Config tunes the handler.
type Config struct {
	// AdminTelegramID gets new-payment and support notifications (unless
	// notify.recipients sends them to every owner and admin).
	AdminTelegramID int64
	// BotName is the brand shown until branding.name is set (from getMe).
	BotName string
	// BotUsername (from getMe) builds invite links: t.me/<username>?start=r_<code>.
	BotUsername string
	// Location is the time zone of dates when general.timezone is not set
	// (BOBRES_TIMEZONE; nil: Asia/Tehran).
	Location *time.Location
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
	set     *settings.Store

	refreshMu sync.Mutex      // one refresh at a time, so an older read never wins
	dropped   map[string]bool // text overrides left out at the last refresh
	lastTZ    string          // general.timezone at the last refresh

	gwMu      sync.Mutex
	gwMethods map[string]methodsEntry // currency -> usable automated methods
}

// New builds a Handler. limiter may be nil.
func New(core corev1.CoreServiceClient, t Telegram, st state.Store, cat *i18n.Catalog, limiter Limiter, cfg Config, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if cfg.Location == nil {
		cfg.Location = defaultLocation()
	}
	return &Handler{core: core, tg: t, state: st, cat: cat, limiter: limiter, cfg: cfg, log: log,
		set: settings.New(), dropped: map[string]bool{}, gwMethods: map[string]methodsEntry{}}
}

func defaultLocation() *time.Location {
	if l, err := time.LoadLocation("Asia/Tehran"); err == nil {
		return l
	}
	return time.UTC
}

// Settings are the store settings the handler keeps up to date, for the
// notifier to share.
func (h *Handler) Settings() *settings.Store { return h.set }

// RefreshSettings reloads the settings (branding, payment details, switches
// and limits), the text overrides and the staff who get alerts, and applies
// the time zone and the currency names.
func (h *Handler) RefreshSettings(ctx context.Context) error {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
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
	h.set.Replace(m)
	dropped := map[string]bool{}
	for _, k := range h.cat.SetOverrides(m) {
		dropped[k] = true
		if !h.dropped[k] { // once, not at every refresh
			h.log.Warn("text override ignored: it would break the messages it is used in; the default text is used", "key", k)
		}
	}
	h.dropped = dropped
	h.applyTimezone(m["general.timezone"])
	i18n.SetCurrencyNames(map[string]string{"fa": m["branding.currency.fa"], "en": m["branding.currency.en"]})
	// Prices and rates may have changed (e.g. payments.stars_rate): the usable
	// payment methods are asked again on the next screen.
	h.gwMu.Lock()
	clear(h.gwMethods)
	h.gwMu.Unlock()

	if resp, err := h.core.ListStaffContacts(ctx, &corev1.ListStaffContactsRequest{}); err != nil {
		h.log.Warn("staff list not refreshed; alerts go to the last known one", "err", err)
	} else {
		staff := make([]settings.Contact, 0, len(resp.GetItems()))
		for _, c := range resp.GetItems() {
			staff = append(staff, settings.Contact{TelegramID: c.GetTelegramId(), Language: c.GetLanguage(), Role: c.GetRole()})
		}
		h.set.SetStaff(staff)
	}
	return nil
}

// applyTimezone shows dates in general.timezone, or in the configured zone
// when it is empty or a zone this bot does not know.
func (h *Handler) applyTimezone(name string) {
	loc := h.cfg.Location
	if name != "" && name != "Local" {
		if l, err := time.LoadLocation(name); err == nil {
			loc = l
		} else if name != h.lastTZ {
			h.log.Warn("general.timezone is unknown here; dates use the default zone", "value", name, "default", loc.String())
		}
	}
	h.lastTZ = name
	i18n.SetLocation(loc)
}

func (h *Handler) setting(key string) string { return h.set.Get(key) }

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

// isStaff: an owner or admin (the admin panel and its commands).
func (r *req) isStaff() bool {
	return r.user != nil && r.user.GetStatus() == "active" && (r.user.GetRole() == "admin" || r.user.GetRole() == "owner")
}

// isTeam: any staff role, support included (the dashboard; the gates let
// them through). Core's domain.IsStaffRole, which the bot cannot import.
func (r *req) isTeam() bool {
	if r.user == nil || r.user.GetStatus() != "active" {
		return false
	}
	switch r.user.GetRole() {
	case "owner", "admin", "support":
		return true
	}
	return false
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
	case st.Code() == codes.FailedPrecondition && strings.HasPrefix(msg, "discount: "):
		key = discountKey(err)
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "cannot be extended"):
		key = "sub.not_extendable"
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "plan unavailable"):
		key = "plan.unavailable"
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
	if u.Message != nil && u.Message.SuccessfulPayment != nil {
		// Sent once, and the Stars are already taken: recorded before anything
		// that can fail or be cancelled (de-duplication, user lookup, rate
		// limit, shutdown). Recording is idempotent per charge id.
		h.onStarsPaid(ctx, u.Message)
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
	if cb := r.cb; cb != nil {
		// Answered when served, even if r.cb was cleared on the way (a new
		// user's menu comes as a new message).
		defer func() {
			if err := h.tg.AnswerCallback(ctx, cb.ID, r.toast, r.alert); err != nil {
				h.log.Debug("answer callback failed", "err", err)
			}
		}()
	}

	if user, err := h.core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: from.ID}}); err == nil {
		r.user, r.lang = user, user.GetLanguage()
	} else if status.Code(err) != codes.NotFound {
		r.lang = i18n.Normalize(from.LanguageCode)
		r.fail(err)
		return
	} else {
		r.lang = i18n.Normalize(from.LanguageCode)
	}

	if h.limiter != nil {
		perTen := int(h.set.Int("limits.bot_per_10s", 20, 5, 200))
		if ok, err := h.limiter.Allow(ctx, "bot:rl:"+strconv.FormatInt(from.ID, 10), 10*time.Second, perTen); err == nil && !ok {
			if r.cb != nil {
				r.toast = r.t("error.rate")
			}
			return
		}
	}

	if h.gate(r) {
		return // maintenance, or the user has not joined the channel
	}

	if r.cb != nil {
		h.routeCallback(r, r.cb.Data)
		return
	}
	h.routeMessage(r)
}

func (h *Handler) routeCallback(r *req, data string) {
	verb, rest, _ := strings.Cut(data, ":")
	if verb == "jn" { // also before registering: then the language picker follows
		h.onJoinCheck(r)
		return
	}
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
	case "rnw":
		h.showRenewPlans(r, rest)
	case "tup":
		h.showTopups(r, rest)
	case "rp":
		h.onPickForSub(r, rest)
	case "dc":
		h.askDiscount(r, rest)
	case "ref":
		h.showReferral(r)
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
	case "dash":
		if !r.isTeam() {
			r.toast, r.alert = r.t("admin.forbidden"), true
			return
		}
		h.sendDashboardLink(r)
	case "adm":
		h.routeAdmin(r, rest)
	default:
		r.toast = r.t("error.expired")
		h.showHome(r)
	}
}

// command splits a message into a command ("/start", lower case, without a
// @BotName suffix) and its arguments; ok is false for other messages.
func command(text string) (cmd, args string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	cmd, args, _ = strings.Cut(text, " ")
	cmd = strings.ToLower(strings.SplitN(cmd, "@", 2)[0]) // /start@MyBot
	return cmd, strings.TrimSpace(args), true
}

func (h *Handler) routeMessage(r *req) {
	if cmd, args, ok := command(r.msg.Text); ok {
		h.onCommand(r, cmd, args)
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
		h.onStart(r, args)
	case "/menu":
		r.clearState()
		h.showHome(r)
	case "/cancel":
		r.clearState()
		r.send(r.t("cancelled"), nil)
		h.showHome(r)
	case "/paysupport":
		h.onPaySupport(r)
	case "/terms":
		h.onTerms(r)
	case "/dashboard":
		switch {
		case r.user == nil:
			h.askLanguage(r)
		case !r.isTeam():
			r.send(r.t("admin.forbidden"), homeKeyboard(r))
		default:
			r.clearState()
			h.sendDashboardLink(r)
		}
	case "/admin", "/set", "/plan_add", "/trial", "/topup_add", "/discount_add", "/discount_off", "/discounts":
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
	case sceneDiscount:
		h.onDiscountCode(r, st)
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
	sceneDiscount    = "discount" // a discount code for a payment menu
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
