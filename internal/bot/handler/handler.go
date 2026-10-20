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
	"sync/atomic"
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
	SendMenu(ctx context.Context, chatID int64, text string, m *tg.Menu) (*tg.Message, error)
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
	// AdminTelegramIDs are the root admins (the owners the server's settings
	// name): they get the new-payment and support notifications (unless
	// notify.recipients sends them to every owner and admin).
	AdminTelegramIDs []int64
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

	// checkWait: how long an automatic USDT payment's Check button rests
	// after finding nothing (0: never).
	checkWait time.Duration

	// profileKick wakes RunProfile after a settings refresh, once the staff
	// list has come from core (staffKnown, under refreshMu): before that the
	// staff menus would look like every staff member had left.
	profileKick chan struct{}
	staffKnown  bool

	// menuLabels maps the bottom menu's labels to their actions (menuAction):
	// built from the built-in texts at start, again after each refresh,
	// keeping the labels it had.
	menuLabels atomic.Pointer[map[string]string]
	// settingsLoaded: a refresh has loaded the settings and the texts, so
	// the bottom menu is built as the store has it (refreshMenu).
	settingsLoaded atomic.Bool

	// photoVersion is Settings.bot_photo_version from the last refresh, for
	// RunProfile (nil before the first).
	photoVersion atomic.Pointer[string]
	// miniApp is Settings.miniapp_url from the last refresh: where the Mini
	// App opens from ("" until the first refresh, when it is off, or when the
	// store has no public HTTPS address).
	miniApp atomic.Pointer[string]
	// whiteLabel is Settings.white_label from the last refresh (false until
	// then: the credit shows unless core says otherwise).
	whiteLabel atomic.Bool
	// resellersIncluded is Settings.resellers_enabled from the last refresh:
	// the license in effect includes resellers, so customers are offered the
	// Become a reseller button (false until then, and for an older core).
	resellersIncluded atomic.Bool
	// joinExtras is Settings.join_chats from the last refresh: the required
	// channels and groups after the first one (the setting join.channel), as
	// far as the license allows (joinchats.go). Nil before the first refresh,
	// and for an older core.
	joinExtras atomic.Pointer[[]joinReq]
	// joinSem bounds the membership lookups in flight for all customers at
	// once, and joinMissTTL is how long a chat a customer has not joined is
	// not asked about again by an ordinary update (the "I've joined" button
	// always asks); a field so that a test can switch it off.
	joinSem     chan struct{}
	joinMissTTL time.Duration
	// helpFAQ and helpTutorials are Settings.help_faq and help_tutorials from the
	// last refresh: whether the owner has an enabled question and an enabled
	// tutorial to show. The FAQ button under Support and the Tutorials button of
	// the menus show only while they are true (false until the first refresh).
	helpFAQ       atomic.Bool
	helpTutorials atomic.Bool
}

// New builds a Handler. limiter may be nil.
func New(core corev1.CoreServiceClient, t Telegram, st state.Store, cat *i18n.Catalog, limiter Limiter, cfg Config, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if cfg.Location == nil {
		cfg.Location = defaultLocation()
	}
	h := &Handler{core: core, tg: t, state: st, cat: cat, limiter: limiter, cfg: cfg, log: log,
		set: settings.New(), dropped: map[string]bool{}, gwMethods: map[string]methodsEntry{}, checkWait: defaultCheckWait,
		profileKick: make(chan struct{}, 1), joinSem: make(chan struct{}, joinParallelAll), joinMissTTL: joinMissCacheTTL}
	h.loadMenuLabels() // a tap is known as such before settings ever load
	return h
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
	// The bot photo's version: RunProfile fetches the photo when it changes.
	photo := s.GetBotPhotoVersion()
	h.photoVersion.Store(&photo)
	miniApp := s.GetMiniappUrl()
	h.miniApp.Store(&miniApp)
	h.whiteLabel.Store(s.GetWhiteLabel())
	h.resellersIncluded.Store(s.GetResellersEnabled())
	extras := make([]joinReq, 0, len(s.GetJoinChats()))
	for _, c := range s.GetJoinChats() {
		if c.GetChat() != "" {
			extras = append(extras, joinReq{Chat: c.GetChat(), Link: c.GetLink(), Label: c.GetLabel()})
		}
	}
	h.joinExtras.Store(&extras)
	h.helpFAQ.Store(s.GetHelpFaq())
	h.helpTutorials.Store(s.GetHelpTutorials())
	dropped := map[string]bool{}
	for _, k := range h.cat.SetOverrides(m) {
		dropped[k] = true
		if !h.dropped[k] { // once, not at every refresh
			h.log.Warn("text override ignored: it would break the messages it is used in; the default text is used", "text", k)
		}
	}
	h.dropped = dropped
	h.loadMenuLabels() // the labels follow the texts
	h.settingsLoaded.Store(true)
	h.applyTimezone(m["general.timezone"])
	i18n.SetCurrencyNames(map[string]string{"fa": m["branding.currency.fa"], "en": m["branding.currency.en"]})
	// Prices and rates may have changed (e.g. payments.stars_rate): the usable
	// payment methods are asked again on the next screen.
	h.gwMu.Lock()
	clear(h.gwMethods)
	h.gwMu.Unlock()

	// Support staff too: they get ticket alerts (settings.TicketRecipients).
	if resp, err := h.core.ListStaffContacts(ctx, &corev1.ListStaffContactsRequest{IncludeSupport: true}); err != nil {
		h.log.Warn("staff list not refreshed; alerts go to the last known one", "err", err)
	} else {
		staff := make([]settings.Contact, 0, len(resp.GetItems()))
		for _, c := range resp.GetItems() {
			staff = append(staff, settings.Contact{TelegramID: c.GetTelegramId(), Language: c.GetLanguage(), Role: c.GetRole()})
		}
		h.set.SetStaff(staff)
		h.staffKnown = true
	}
	// The bot's Telegram profile follows the texts and the staff (RunProfile).
	if h.staffKnown {
		select {
		case h.profileKick <- struct{}{}:
		default:
		}
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
	// textTap: the update is a message (typed, or a tap on the bottom menu),
	// so there is no callback to answer: a toast is sent as a message.
	textTap bool
	// keep: the pressed message must stay as it is (a notification, or a
	// message now above a bottom menu just sent), so show sends a new message
	// instead of editing it.
	keep bool
	// gated: a gate let the update through only to continue a ticket; what
	// turns out to be anything else gets the gate's answer (answerGate).
	gated bool
	// joinMissing: the required chats the user has not joined, as the gate
	// found them when it let a ticket through (answerGate asks about those).
	joinMissing []joinReq
	// held: a gate is in force for the user and let the update through only
	// for what it is (an exempt command, a payment under way, a ticket), so
	// it gets no bottom menu.
	held bool
	// menu: the bottom menu the user had when the update came, once read
	// (sentMenu).
	menu     state.SentMenu
	menuErr  error
	menuRead bool
}

func (r *req) t(key string, args ...any) string { return r.h.cat.T(r.lang, key, args...) }

// sentMenu is the bottom menu the user had when the update came, as it was
// recorded when sent. It is read once: a menu sent on the way replaces the
// record, not the keyboard the update came from.
func (r *req) sentMenu() (state.SentMenu, error) {
	if !r.menuRead {
		r.menu, r.menuErr = r.h.state.LoadMenu(r.ctx, r.from.ID)
		r.menuRead = true
	}
	return r.menu, r.menuErr
}

func (r *req) money(amount int64, currency string) string {
	return i18n.Money(r.lang, amount, currency)
}

// balance shows a wallet balance. A reseller's negative balance is what they
// owe: shown as a debt, never with a bare minus (which reads wrongly in
// Persian).
func (r *req) balance(amount int64, currency string) string {
	if amount < 0 {
		return r.t("wallet.debt", "amount", r.money(-amount, currency))
	}
	return r.money(amount, currency)
}

// labelled puts the name a reseller gave a service above a text about it.
func (r *req) labelled(label, text string) string {
	if label == "" {
		return text
	}
	return r.t("sub.label_line", "label", esc(label)) + "\n" + text
}

// show replaces the pressed menu (callbacks) or sends a new message.
func (r *req) show(text string, kb *tg.Keyboard) {
	if r.cb != nil && r.cb.Message != nil && !r.keep {
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

// isReseller: an active reseller (the reseller panel, credit and tier
// prices). Not a staff role: the gates treat them as customers.
func (r *req) isReseller() bool {
	return r.user != nil && r.user.GetStatus() == "active" && r.user.GetRole() == "reseller"
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
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "new purchases are paused"):
		// core's domain.PurchasesPaused: a new service was refused for a
		// limit. The customer is told only that buying is unavailable (a
		// customer never sees a word about licensing).
		key = "error.purchases_paused"
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
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "unmatched deposit"):
		key = "pay.txid_announced" // staff were told to credit that transfer by hand
	case st.Code() == codes.InvalidArgument && (strings.Contains(msg, "payment method ") && strings.HasSuffix(msg, " is not enabled") ||
		strings.Contains(msg, "is not set up for this price")):
		key = "pay.not_configured" // switched off (or its rate removed) since the menu was shown
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
	if u.MyChatMember != nil {
		h.onMyChatMember(ctx, u.MyChatMember) // the user blocked the bot, or came back
		return
	}
	if first, err := h.state.Once(ctx, "upd:"+strconv.FormatInt(u.UpdateID, 10), 24*time.Hour); err == nil && !first {
		return // Telegram re-delivered an update we already handled
	}
	r := &req{ctx: ctx, h: h, from: from, cb: u.CallbackQuery, msg: u.Message, textTap: u.CallbackQuery == nil}
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
		if user.GetBotBlocked() { // writing again: they no longer block the bot
			h.setBotBlocked(ctx, from.ID, false)
		}
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
			return // a message over the limit stays unanswered
		}
	}
	// A message has no callback to carry a toast (a tap on the bottom menu
	// reaches the handlers that answer with one): it comes as a message.
	// Toast texts are plain, safe to send as HTML.
	defer func() {
		if r.textTap && r.toast != "" {
			r.send(r.toast, nil)
		}
	}()

	if h.gate(r) {
		return // maintenance, or the user has not joined the channel
	}
	if r.user != nil && !r.held && !sendsMenu(r) {
		h.refreshMenu(r)
	}

	if r.cb != nil {
		h.routeCallback(r, r.cb.Data)
		return
	}
	h.routeMessage(r)
}

func (h *Handler) routeCallback(r *req, data string) {
	verb, _, _ := strings.Cut(data, ":")
	if verb == "jn" { // also before registering: then the language picker follows
		h.onJoinCheck(r)
		return
	}
	if r.user == nil && verb != "lang" {
		r.toast = r.t("error.expired")
		h.askLanguage(r)
		return
	}
	h.dispatch(r, data)
}

// dispatch runs the action that callback data names: a button's, or a tap on
// the bottom menu (menuAction), so both pass the same role checks.
func (h *Handler) dispatch(r *req, data string) {
	verb, rest, _ := strings.Cut(data, ":")
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
	case "nop": // a plan table's header button: nothing to do, the tap is only answered
	case "bk":
		h.routeBulk(r, rest)
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
	case "top":
		h.showTopUsers(r, pageArg(rest))
	case "ap":
		h.onResellerApply(r)
	case "renew":
		h.showRenewServices(r)
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
	case "gift":
		h.askGiftCode(r)
	case "trial":
		h.onTrial(r)
	case "store":
		h.onStore(r)
	case "support":
		r.keep = r.keep || rest == "m" // under a notification, which must stay as it is
		h.onSupport(r)
	case "tk":
		h.onTicketButton(r, rest)
	case "faq": // the FAQ under Support (help.go)
		h.onFAQ(r, rest)
	case "tut": // the Tutorials menu
		h.onTutorials(r, rest)
	case "dash":
		if !r.isTeam() {
			r.toast, r.alert = r.t("admin.forbidden"), true
			return
		}
		h.sendDashboardLink(r)
	case "adm":
		h.routeAdmin(r, rest)
	case "rs":
		h.routeReseller(r, rest)
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
	// A tap on the bottom menu, whatever the user was in the middle of: a
	// label is never taken as an answer (a bank reference, a service name).
	if data, ok := h.menuTap(r); ok {
		// Let through only to go on with a ticket: Support, when there is
		// an open one to show (not the prompt to start one).
		if r.gated && (data != "support" || !h.hasOpenTicket(r)) {
			h.answerGate(r)
			return
		}
		r.clearState()
		h.dispatch(r, data)
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
	if replyToBot(r.msg) { // perhaps an answer to a staff reply
		h.onTicketReply(r)
		return
	}
	h.notForSupport(r)
}

func (h *Handler) onCommand(r *req, cmd, args string) {
	switch cmd {
	case "/start":
		h.onStart(r, args)
	case "/menu":
		r.clearState()
		if r.user != nil {
			h.sendMenu(r, r.t("menu.keyboard"))
		}
		h.showHome(r)
	case "/cancel":
		r.clearState()
		r.send(r.t("cancelled"), nil)
		h.showHome(r)
	case "/new": // the buttons Buy, Renew and My services, by their own commands
		h.onActionCommand(r, "buy")
	case "/renew":
		h.onActionCommand(r, "renew")
	case "/status":
		h.onActionCommand(r, "subs")
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
		h.onTicket(r, st)
	case sceneDiscount:
		h.onDiscountCode(r, st)
	case sceneGiftCode:
		h.onGiftCode(r)
	case sceneAdminFind, sceneAdminAdjust, sceneAdminReason, sceneAdminInput:
		h.onAdminScene(r, st)
	case sceneResellerLabel, sceneResellerFind, sceneResellerRename, sceneResellerNote:
		h.onResellerScene(r, st)
	case sceneResellerApply:
		h.onResellerApplication(r)
	case sceneBulkQuantity, sceneBulkName:
		h.onBulkScene(r, st)
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
	// A reseller's name for a service they buy for a customer, a search of
	// their services, a new name for one of them, and a private note on one.
	sceneResellerLabel  = "rs_label"
	sceneResellerFind   = "rs_find"
	sceneResellerRename = "rs_rename"
	sceneResellerNote   = "rs_note"
	// A bulk purchase: the number of services typed, and the name they share.
	sceneBulkQuantity = "bulk_qty"
	sceneBulkName     = "bulk_name"
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

// button is a callback button labelled with key's text in lang, in the color
// of what it does (i18n.ButtonStyle).
func (h *Handler) button(lang, key, data string, args ...any) tg.Button {
	b := tg.CB(h.cat.T(lang, key, args...), data)
	b.Style = i18n.ButtonStyle(key)
	return b
}

// button is a callback button in the user's language (Handler.button).
func (r *req) button(key, data string, args ...any) tg.Button {
	return r.h.button(r.lang, key, data, args...)
}

// linkButton is a URL button labelled with key's text, colored the same way.
func (r *req) linkButton(key, url string) tg.Button {
	b := tg.Link(r.t(key), url)
	b.Style = i18n.ButtonStyle(key)
	return b
}

// webAppButton is an inline button labelled with key's text that opens the
// Mini App at url, colored the same way.
func (r *req) webAppButton(key, url string) tg.Button {
	b := tg.WebApp(r.t(key), url)
	b.Style = i18n.ButtonStyle(key)
	return b
}

func homeKeyboard(r *req) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(r.button("btn.home", "home"))
}

func cancelKeyboard(r *req) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(r.button("btn.cancel", "cancel"))
}

func backHome(r *req, back string) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(r.button("btn.back", back), r.button("btn.home", "home"))
}
