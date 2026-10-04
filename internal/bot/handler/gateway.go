package handler

import (
	"context"
	"strconv"
	"strings"
	"time"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Automated payment methods (Phase 2): Zarinpal and Telegram Stars.

const methodsTTL = time.Minute // how stale the list of usable methods may be

type methodsEntry struct {
	at   time.Time
	list []string
}

// gatewayMethods lists the automated methods usable for a price (enabled in
// payments, priced in core and within the method's limits), cached for a minute.
func (h *Handler) gatewayMethods(ctx context.Context, currency string, amount int64) []string {
	key := currency + ":" + strconv.FormatInt(amount, 10)
	h.gwMu.Lock()
	e, ok := h.gwMethods[key]
	h.gwMu.Unlock()
	if ok && time.Since(e.at) < methodsTTL {
		return e.list
	}
	resp, err := h.core.ListPaymentMethods(ctx, &corev1.ListPaymentMethodsRequest{Currency: currency, Amount: amount})
	if err != nil {
		h.log.Debug("list payment methods", "err", err)
		return e.list // keep the last known list on a hiccup
	}
	h.gwMu.Lock()
	h.gwMethods[key] = methodsEntry{at: time.Now(), list: resp.GetProviders()}
	h.gwMu.Unlock()
	return resp.GetProviders()
}

// gatewayButtons are the automated payment buttons, "<verb>:<z|s>:<target>:<nonce>".
func (h *Handler) gatewayButtons(r *req, currency string, amount int64, verb, target, n string) []tg.Button {
	var out []tg.Button
	for _, m := range h.gatewayMethods(r.ctx, currency, amount) {
		switch m {
		case "zarinpal":
			out = append(out, tg.CB(r.t("btn.pay_zarinpal"), verb+":z:"+target+":"+n))
		case "stars":
			out = append(out, tg.CB(r.t("btn.pay_stars"), verb+":s:"+target+":"+n))
		}
	}
	return out
}

func truncRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// startGateway starts a Zarinpal payment (a link to our pay page) or sends a
// Telegram Stars invoice, for an order or a wallet top-up.
func (h *Handler) startGateway(r *req, order *corev1.Order, provider, title, key string, topup int64) {
	in := &corev1.CreatePaymentIntentRequest{UserId: r.user.GetId(), Provider: provider}
	if order != nil {
		in.OrderId = order.GetId()
		in.IdempotencyKey = "int:" + order.GetId() + ":" + provider
	} else {
		in.Amount = &commonv1.Money{Amount: topup, Currency: "IRT"}
		in.IdempotencyKey = key
	}
	intent, err := h.core.CreatePaymentIntent(r.ctx, in)
	if err != nil {
		r.fail(err)
		return
	}
	switch intent.GetStatus() {
	case "pending":
	case "succeeded":
		r.show(r.t("pay.check_paid"), homeKeyboard(r))
		return
	default: // failed or expired: this screen's payment is over, start again
		reason := intent.GetFailureReason()
		if intent.GetStatus() == "expired" {
			reason = "expired"
		}
		r.show(r.t("pay.check_failed", "reason", r.t(reasonKey(reason))),
			(&tg.Keyboard{}).Row(tg.CB(r.t("btn.buy"), "buy"), tg.CB(r.t("btn.home"), "home")))
		return
	}
	amount := r.money(intent.GetAmount().GetAmount(), intent.GetAmount().GetCurrency())
	switch provider {
	case "zarinpal":
		if intent.GetPayUrl() == "" {
			r.show(r.t("pay.not_configured"), homeKeyboard(r))
			return
		}
		kb := (&tg.Keyboard{}).Row(tg.Link(r.t("btn.open_payment"), intent.GetPayUrl())).
			Row(tg.CB(r.t("btn.check_payment"), "chk:"+intent.GetId())).
			Row(tg.CB(r.t("btn.home"), "home"))
		r.show(r.t("pay.zarinpal", "amount", amount), kb)
	case "stars":
		stars := intent.GetGatewayAmount().GetAmount()
		if title = truncRunes(title, 32); title == "" {
			title = truncRunes(r.t("pay.stars_title"), 32)
		}
		_, err := h.tg.SendInvoice(r.ctx, r.chatID, tg.Invoice{
			Title: title, Description: truncRunes(r.t("pay.stars_desc", "amount", amount), 255),
			Payload: intent.GetId(), Stars: stars, StartParameter: "buy",
		})
		if err != nil {
			h.log.Warn("send invoice failed", "err", err)
			r.fail(status.Error(codes.Unavailable, "telegram invoice"))
			return
		}
		r.show(r.t("pay.stars", "stars", i18n.Number(r.lang, stars), "amount", amount), homeKeyboard(r))
	}
}

// onCheckPayment handles "chk:<intent id>": the customer says they paid.
func (h *Handler) onCheckPayment(r *req, intentID string) {
	ref, err := h.core.CheckPayment(r.ctx, &corev1.CheckPaymentRequest{UserId: r.user.GetId(), IntentId: intentID})
	if err != nil {
		r.fail(err)
		return
	}
	switch ref.GetStatus() {
	case "succeeded":
		r.show(r.t("pay.check_paid"), homeKeyboard(r))
	case "failed", "expired":
		reason := ref.GetFailureReason()
		if ref.GetStatus() == "expired" {
			reason = "expired"
		}
		r.show(r.t("pay.check_failed", "reason", r.t(reasonKey(reason))),
			(&tg.Keyboard{}).Row(tg.CB(r.t("btn.buy"), "buy"), tg.CB(r.t("btn.home"), "home")))
	default:
		r.toast, r.alert = r.t("pay.check_pending"), true
	}
}

// gatewayReasons are the failure reasons automated gateways report.
var gatewayReasons = map[string]bool{
	"gateway_failed": true, "amount_mismatch": true, "expired": true,
	"payer_mismatch": true, "not_found": true, "reversed": true,
}

// reasonKey is the catalog key of a gateway failure reason.
func reasonKey(reason string) string {
	if gatewayReasons[reason] {
		return "reason." + reason
	}
	return "reason.gateway_failed"
}

// onPreCheckout answers Telegram's pre-checkout for a Stars invoice. Telegram
// cancels the payment unless it is answered within 10 seconds.
func (h *Handler) onPreCheckout(ctx context.Context, q *tg.PreCheckoutQuery) {
	checkCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
	ok, msg := h.precheck(checkCtx, q)
	cancel()
	answerCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := h.tg.AnswerPreCheckoutQuery(answerCtx, q.ID, ok, msg); err != nil {
		h.log.Warn("answer pre-checkout failed", "user", q.From.ID, "err", err)
	}
}

func (h *Handler) precheck(ctx context.Context, q *tg.PreCheckoutQuery) (bool, string) {
	lang := i18n.Normalize(q.From.LanguageCode)
	user, err := h.core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: q.From.ID}})
	if err != nil {
		return false, h.cat.T(lang, "pay.precheck_failed")
	}
	if l := user.GetLanguage(); l != "" {
		lang = l
	}
	if _, err := h.core.StarsPreCheckout(ctx, &corev1.StarsPreCheckoutRequest{
		UserId: user.GetId(), IntentId: q.InvoicePayload, TotalAmount: q.TotalAmount, Currency: q.Currency,
	}); err != nil {
		h.log.Info("pre-checkout refused", "user", q.From.ID, "err", err)
		return false, h.cat.T(lang, "pay.precheck_failed")
	}
	return true, ""
}

// onStarsPaid records a completed Stars payment. Telegram sends it once and
// has already taken the Stars, so it is logged before any call, recorded with
// retries on a context that survives shutdown, and anything but a confirmed
// payment alerts the admin with what is needed to fix it by hand.
func (h *Handler) onStarsPaid(parent context.Context, m *tg.Message) {
	sp, from := m.SuccessfulPayment, m.From
	if from == nil {
		h.log.Error("Stars payment without a sender", "charge_id", sp.TelegramPaymentChargeID, "payload", sp.InvoicePayload)
		return
	}
	h.log.Info("Stars payment received", "telegram_id", from.ID, "charge_id", sp.TelegramPaymentChargeID,
		"payload", sp.InvoicePayload, "stars", sp.TotalAmount)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Minute)
	defer cancel()
	var (
		user *corev1.User
		ref  *corev1.PaymentIntentRef
		err  error
	)
	for attempt, wait := 0, time.Second; attempt < 6; attempt, wait = attempt+1, wait*2 {
		if user == nil {
			user, err = h.core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: from.ID}})
		}
		if err == nil {
			ref, err = h.core.StarsPaid(ctx, &corev1.StarsPaidRequest{UserId: user.GetId(), IntentId: sp.InvoicePayload,
				TotalAmount: sp.TotalAmount, Currency: sp.Currency, TelegramPaymentChargeId: sp.TelegramPaymentChargeID})
		}
		if err == nil || !retryable(err) {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
	lang := i18n.Normalize(from.LanguageCode)
	if l := user.GetLanguage(); l != "" {
		lang = l
	}
	if err == nil && ref.GetStatus() == "succeeded" {
		h.sendTo(ctx, m.Chat.ID, h.cat.T(lang, "pay.stars_received"))
		return
	}
	h.log.Error("Stars payment not recorded as paid", "telegram_id", from.ID, "charge_id", sp.TelegramPaymentChargeID,
		"payload", sp.InvoicePayload, "stars", sp.TotalAmount, "status", ref.GetStatus(), "reason", ref.GetFailureReason(), "err", err)
	h.sendTo(ctx, m.Chat.ID, h.cat.T(lang, "pay.stars_unrecorded", "code", esc(sp.TelegramPaymentChargeID)))
	if h.cfg.AdminTelegramID != 0 {
		who := strconv.FormatInt(from.ID, 10)
		if from.Username != "" {
			who = "@" + from.Username + " (" + who + ")"
		}
		al := h.userLang(ctx, h.cfg.AdminTelegramID)
		h.sendTo(ctx, h.cfg.AdminTelegramID, h.cat.T(al, "admin.stars_unrecorded",
			"user", esc(who), "stars", i18n.Number(al, sp.TotalAmount),
			"charge", esc(sp.TelegramPaymentChargeID), "payload", esc(sp.InvoicePayload)))
	}
}

// userLang is a user's language, or the default when unknown.
func (h *Handler) userLang(ctx context.Context, telegramID int64) string {
	u, err := h.core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: telegramID}})
	if err != nil || u.GetLanguage() == "" {
		return i18n.Languages[0]
	}
	return u.GetLanguage()
}

// sendTo sends a message outside a request (no menu to edit).
func (h *Handler) sendTo(ctx context.Context, chatID int64, text string) {
	if _, err := h.tg.SendMessage(ctx, chatID, text, nil); err != nil {
		h.log.Warn("send failed", "chat", chatID, "err", err)
	}
}

func retryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Internal, codes.Unknown, codes.ResourceExhausted:
		return true
	}
	return false
}

// onPaySupport answers /paysupport (required by Telegram for bots that sell
// with Stars).
func (h *Handler) onPaySupport(r *req) {
	contact := h.setting("branding.support")
	if contact == "" {
		contact = r.t("paysupport.in_bot")
	}
	r.send(r.t("paysupport", "contact", esc(contact)), homeKeyboard(r))
}
