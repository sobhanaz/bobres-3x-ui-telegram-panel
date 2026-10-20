package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/botfiles"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/domain"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	bmoney "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/money"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tgauth"
)

// The Telegram Mini App store (docs/17-mini-app.md): the app itself under
// /app/ and its JSON API under /api/mini/v1/. A customer signs in with the
// init data Telegram gives the Mini App (the bot checks its signature: only
// the bot holds the bot token) and gets a bearer token; every call then acts
// on that customer's own account, through the same domain functions the bot
// uses. It is on unless the owner sets miniapp.enabled to false.

// MiniAppVerifier checks a Mini App's init data (botfiles.Client asks the bot).
type MiniAppVerifier interface {
	VerifyMiniApp(ctx context.Context, initData string) (*botfiles.MiniAppUser, error)
}

const (
	miniBody      = 16 << 10
	miniAuthRate  = 20  // sign-ins a minute per client address
	miniAPIRate   = 120 // calls a minute per customer
	miniWriteRate = 20  // calls that change something, a minute per customer
	// The bot's own top-up bounds (Toman).
	miniMinTopup = 10_000
	miniMaxTopup = 50_000_000
	miniLedger   = 20
)

// miniMethods are the payment methods the app can take a customer through: a
// payment page to open, an address to send USDT to, a Telegram Stars invoice
// to open, or the card, USDT address or Zarinpal link to pay and then send a
// receipt for. Which of them a customer is offered is the store's decision
// (domain.OfferedMethods); a method outside this list is refused whatever.
var miniMethods = map[string]bool{
	domain.Zarinpal: true, domain.IDPay: true, domain.NextPay: true, domain.USDTAuto: true,
	domain.Stars: true, domain.ManualCard: true, domain.ManualCrypto: true, domain.ManualZarinpal: true,
}

var miniUUIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

var miniKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type miniState struct {
	sessions *miniSessions
	auth     *limiter
	api      *limiter
	write    *limiter
	// bot is the bot's own @username as it told core at a sign-in: the app's
	// links use it until the owner sets one in Branding.
	bot atomic.Pointer[string]
	// upload limits the receipts a customer sends (on top of write), uploads
	// holds a place for each receipt being checked and sent, senders the
	// customers who have one on its way (one each, so that no one account
	// holds every place) and proofs the payments a proof is being sent for
	// (miniapp_pay.go).
	upload  *limiter
	uploads chan struct{}
	senders *miniInflight
	proofs  *miniInflight
}

// noteBot remembers the bot's username from a sign-in (a malformed one is ignored).
func (m *miniState) noteBot(name string) {
	if pageBotRe.MatchString(name) {
		m.bot.Store(&name)
	}
}

func newMiniState() *miniState {
	return &miniState{sessions: newMiniSessions(), auth: newLimiter(miniAuthRate, time.Minute),
		api: newLimiter(miniAPIRate, time.Minute), write: newLimiter(miniWriteRate, time.Minute),
		upload: newLimiter(miniUploadRate, miniUploadWindow), uploads: make(chan struct{}, miniUploadSlots),
		senders: newMiniInflight(), proofs: newMiniInflight()}
}

type miniKey struct{}

type miniCaller struct {
	user        *store.User
	maintenance bool
	token       string
}

func miniFrom(ctx context.Context) *miniCaller { c, _ := ctx.Value(miniKey{}).(*miniCaller); return c }

func (s *Server) registerMiniApp(mux *http.ServeMux) {
	api := http.NewServeMux()
	api.HandleFunc("POST /api/mini/v1/auth", s.miniAuth)
	// Allowed while the store is in maintenance: who is signed in, and
	// payments and orders already started.
	api.HandleFunc("GET /api/mini/v1/me", s.miniSession(true, s.miniMe))
	api.HandleFunc("POST /api/mini/v1/logout", s.miniSession(true, s.miniLogout))
	api.HandleFunc("GET /api/mini/v1/orders/{id}", s.miniSession(true, s.miniGetOrder))
	api.HandleFunc("POST /api/mini/v1/payments/{id}/check", s.miniSession(true, s.miniCheckPayment))
	// The proof of a manual payment (a receipt image, or a transaction id) too: a
	// customer who has paid by hand must be able to say so, as the bot lets a
	// payment under way finish. It only ever concerns a payment already started.
	api.HandleFunc("POST /api/mini/v1/payments/{id}/proof", s.miniSession(true, s.miniProof))

	api.HandleFunc("GET /api/mini/v1/plans", s.miniSession(false, s.miniPlans))
	api.HandleFunc("GET /api/mini/v1/services", s.miniSession(false, s.miniServices))
	api.HandleFunc("GET /api/mini/v1/wallet", s.miniSession(false, s.miniWallet))
	api.HandleFunc("GET /api/mini/v1/methods", s.miniSession(false, s.miniPaymentMethods))
	api.HandleFunc("POST /api/mini/v1/quote", s.miniSession(false, s.miniQuote))
	api.HandleFunc("POST /api/mini/v1/orders", s.miniSession(false, s.miniCreateOrder))
	api.HandleFunc("POST /api/mini/v1/orders/{id}/pay-wallet", s.miniSession(false, s.miniPayWallet))
	api.HandleFunc("POST /api/mini/v1/orders/{id}/pay", s.miniSession(false, s.miniPayOrder))
	api.HandleFunc("POST /api/mini/v1/topup", s.miniSession(false, s.miniTopup))
	for _, f := range miniFeatures { // the sections beyond the shop (miniapp_features.go)
		if f.routes != nil {
			f.routes(s, api)
		}
	}
	api.HandleFunc("/api/mini/v1/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	mux.Handle("/api/mini/v1/", s.miniGuard(api))
	if s.cfg.MiniApp != nil {
		mux.Handle("/app/", s.miniStatic(s.cfg.MiniApp))
		mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/app/", http.StatusMovedPermanently)
		})
	}
}

// miniOn reads the switch: on unless the owner turned it off (domain.MiniAppOn).
func (s *Server) miniOn(ctx context.Context) (settings map[string]string, on bool, err error) {
	settings, err = s.cfg.Domain.Settings(ctx)
	return settings, err == nil && domain.MiniAppOn(settings), err
}

// miniStatic serves the app, or the same answer as for any unknown address
// while the Mini App is off.
func (s *Server) miniStatic(app http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, on, err := s.miniOn(r.Context())
		switch {
		case err != nil:
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		case !on:
			http.NotFound(w, r)
		default:
			app.ServeHTTP(w, r)
		}
	})
}

// miniGuard is every API call's common start: nothing cached, no sniffing,
// small bodies, no cross-origin use (there is no CORS), the switch, and audit
// entries that name the Mini App's customers' channel (the bot's, "bot").
func (s *Server) miniGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		limit := int64(miniBody)
		if isMiniUpload(r) { // the one call that may carry a receipt image
			limit = miniUploadBody
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		if _, on, err := s.miniOn(r.Context()); err != nil {
			s.internal(w, "mini app settings", err)
			return
		} else if !on {
			writeError(w, http.StatusNotFound, "disabled", "the store is not available")
			return
		}
		ctx := store.WithSource(store.WithClientIP(r.Context(), clientIP(r)), store.SourceBot)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// miniSession requires a live session of an active customer. During
// maintenance only the calls marked inMaintenance are served (to staff and
// the configured owner everything is).
func (s *Server) miniSession(inMaintenance bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		token := bearer(r)
		sess := s.mini.sessions.get(token, s.now())
		if sess == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "sign in again")
			return
		}
		if !s.mini.api.allow(sess.userID, s.now()) || (r.Method != http.MethodGet && !s.mini.write.allow(sess.userID, s.now())) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests; wait a minute")
			return
		}
		u, err := s.cfg.Store.GetUser(ctx, s.cfg.Store.Conn(), sess.userID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				s.mini.sessions.dropUser(sess.userID)
				writeError(w, http.StatusUnauthorized, "unauthorized", "sign in again")
				return
			}
			s.internal(w, "mini app user", err)
			return
		}
		if u.Status != "active" {
			s.mini.sessions.dropUser(u.ID)
			writeError(w, http.StatusForbidden, "banned", "this account cannot use the store")
			return
		}
		settings, err := s.cfg.Domain.Settings(ctx)
		if err != nil {
			s.internal(w, "mini app settings", err)
			return
		}
		maintenance := settings["maintenance.enabled"] == "true" && !domain.IsStaffRole(u.Role) && !s.cfg.Domain.IsConfiguredOwner(u)
		if maintenance && !inMaintenance {
			writeError(w, http.StatusServiceUnavailable, "maintenance", "the store is under maintenance")
			return
		}
		next(w, r.WithContext(context.WithValue(ctx, miniKey{}, &miniCaller{user: u, maintenance: maintenance, token: token})))
	}
}

// miniFail answers a domain or backend error with a code the app words in the
// customer's language (and an English message for logs and support).
func (s *Server) miniFail(w http.ResponseWriter, what string, err error) {
	var (
		de *domain.DiscountError
		lr *domain.LicenseRefusal
	)
	switch {
	case errors.As(err, &de):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "discount", "reason": de.Reason, "message": "discount code: " + de.Reason})
	case errors.As(err, &lr):
		writeError(w, http.StatusConflict, "unavailable_now", "the store cannot sell this right now")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such item")
	case status.Code(unwrapAll(err)) == codes.PermissionDenied:
		// Payments says a payment is another customer's: for this customer it does not exist.
		writeError(w, http.StatusNotFound, "not_found", "no such item")
	case errors.Is(err, domain.ErrUserBanned):
		writeError(w, http.StatusForbidden, "banned", "this account cannot use the store")
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "this is not yours")
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid", reason(err, domain.ErrInvalid))
	case errors.Is(err, domain.ErrPlanUnavailable):
		writeError(w, http.StatusConflict, "plan_unavailable", "this plan is not available")
	case errors.Is(err, domain.ErrNotExtendable):
		writeError(w, http.StatusConflict, "not_extendable", reason(err, domain.ErrNotExtendable))
	case errors.Is(err, domain.ErrOrderNotPayable):
		writeError(w, http.StatusConflict, "not_payable", "this order cannot be paid now")
	case errors.Is(err, domain.ErrState):
		writeError(w, http.StatusConflict, "state", reason(err, domain.ErrState))
	case errors.Is(err, store.ErrInsufficientFunds):
		writeError(w, http.StatusConflict, "insufficient_funds", "the wallet balance is too low")
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "conflict", "this request clashes with an earlier one")
	case backendDown(err):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the store is busy; try again in a minute")
	case status.Code(unwrapAll(err)) == codes.NotFound:
		writeError(w, http.StatusNotFound, "not_found", "no such item")
	case status.Code(unwrapAll(err)) == codes.InvalidArgument || status.Code(unwrapAll(err)) == codes.FailedPrecondition:
		// The payments service refuses a request by policy (a limit, a method not set up).
		writeError(w, http.StatusConflict, "state", status.Convert(unwrapAll(err)).Message())
	case status.Code(unwrapAll(err)) == codes.AlreadyExists:
		// The payments service saw this before (a key used for another payment, a payment settled).
		writeError(w, http.StatusConflict, "conflict", status.Convert(unwrapAll(err)).Message())
	default:
		s.internal(w, what, err)
	}
}

// ---- sign-in ----

// miniAuth: POST /api/mini/v1/auth {init_data}. The one call without a token.
func (s *Server) miniAuth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !s.mini.auth.allow(clientKey(r), s.now()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many sign-ins; wait a minute")
		return
	}
	var in struct {
		InitData string `json:"init_data"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.InitData == "" || len(in.InitData) > tgauth.MaxInitData {
		writeError(w, http.StatusUnauthorized, "auth", "open the store from Telegram")
		return
	}
	if s.cfg.MiniAppVerify == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the store cannot check Telegram sign-ins (the bot is not connected)")
		return
	}
	tu, err := s.cfg.MiniAppVerify.VerifyMiniApp(ctx, in.InitData)
	if err == nil {
		s.mini.noteBot(tu.BotUsername)
	}
	switch {
	case errors.Is(err, botfiles.ErrMiniAppAuth):
		writeError(w, http.StatusUnauthorized, "auth", "open the store from Telegram")
		return
	case err != nil:
		s.cfg.Log.Warn("mini app: init data check", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the store is busy; try again in a minute")
		return
	}
	u, err := s.cfg.Store.GetUserByTelegramID(ctx, s.cfg.Store.Conn(), tu.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// A first visit makes the account like the bot's /start does. Only a
		// missing one is created: an upsert would overwrite the language the
		// customer chose in the bot.
		lang := "fa"
		if strings.HasPrefix(strings.ToLower(tu.LanguageCode), "en") {
			lang = "en"
		}
		u, err = s.cfg.Domain.UpsertUser(ctx, domain.UpsertUserParams{TelegramID: tu.ID, Username: tu.Username, Language: lang})
		if err != nil {
			s.miniFail(w, "mini app create user", err)
			return
		}
	case err != nil:
		s.internal(w, "mini app user", err)
		return
	}
	if u.Status != "active" {
		writeError(w, http.StatusForbidden, "banned", "this account cannot use the store")
		return
	}
	token, ok := s.mini.sessions.open(u.ID, u.TelegramID, s.now())
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the store is busy; try again in a minute")
		return
	}
	body, err := s.miniMeBody(ctx, u)
	if err != nil {
		s.internal(w, "mini app me", err)
		return
	}
	body["token"] = token
	body["expires_in"] = int(miniSessionIdle / time.Second)
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) miniLogout(w http.ResponseWriter, r *http.Request) {
	s.mini.sessions.close(miniFrom(r.Context()).token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) miniMe(w http.ResponseWriter, r *http.Request) {
	body, err := s.miniMeBody(r.Context(), miniFrom(r.Context()).user)
	if err != nil {
		s.internal(w, "mini app me", err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// miniMeBody is who the customer is, their balances and the store's look.
// miniBot is the bot's @username for the app's links: the one the owner set in
// Branding, else the one the bot told at a sign-in ("" while neither is known).
func (s *Server) miniBot(settings map[string]string) string {
	if b := settings[domain.SettingBotUsername]; pageBotRe.MatchString(b) {
		return b
	}
	if b := s.mini.bot.Load(); b != nil {
		return *b
	}
	return ""
}

func (s *Server) miniMeBody(ctx context.Context, u *store.User) (map[string]any, error) {
	settings, err := s.cfg.Domain.Settings(ctx)
	if err != nil {
		return nil, err
	}
	wallets, err := s.miniWallets(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	color := strings.ToLower(settings["branding.color"])
	if !pageColorRe.MatchString(color) {
		color = defaultColor
	}
	bot := ""
	if b := s.miniBot(settings); b != "" {
		bot = "https://t.me/" + b
	}
	support, supportURL := supportContact(settings["branding.support"])
	features, err := s.miniFeatureFlags(ctx, u)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"user":        map[string]any{"id": u.ID, "username": u.Username, "language": u.Language},
		"wallets":     wallets,
		"brand":       map[string]any{"name": brandName(settings), "color": color, "support": support, "support_url": supportURL, "bot_url": bot},
		"maintenance": settings["maintenance.enabled"] == "true" && !domain.IsStaffRole(u.Role) && !s.cfg.Domain.IsConfiguredOwner(u),
		"top_up":      map[string]any{"currency": "IRT", "min": miniMinTopup, "max": miniMaxTopup},
		"features":    features,
	}, nil
}

func (s *Server) miniWallets(ctx context.Context, userID string) ([]map[string]any, error) {
	ws, err := s.cfg.Store.ListWallets(ctx, s.cfg.Store.Conn(), userID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	hasIRT := false
	for _, w := range ws {
		if w.Currency == "IRT" {
			hasIRT = true
		}
		if w.Currency == "IRT" || w.Balance != 0 {
			// credit_limit is a reseller's: the balance may go down to minus
			// that (0 for everyone else), so what they can pay with is the two.
			out = append(out, map[string]any{"currency": w.Currency, "balance": w.Balance, "credit_limit": w.CreditLimit, "scale": bmoney.Scale(w.Currency)})
		}
	}
	if !hasIRT { // a customer without a wallet yet sees an empty one
		out = append([]map[string]any{{"currency": "IRT", "balance": 0, "credit_limit": 0, "scale": 0}}, out...)
	}
	return out, nil
}
