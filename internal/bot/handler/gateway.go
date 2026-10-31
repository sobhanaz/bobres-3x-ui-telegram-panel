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

// Automated payment methods: Zarinpal, IDPay, NextPay, Aqayepardakht, Telegram Stars, automatic USDT
// (TRC20, usdt.go) and automatic TON (ton.go).

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

// gatewayButtons are the automated payment buttons, "<verb>:<z|i|n|a|s|u|t>:<target>:<nonce>"
// ("<verb>:<z|i|n|a|s|u|t>:<nonce>" without a target).
func (h *Handler) gatewayButtons(r *req, currency string, amount int64, verb, target, n string) []tg.Button {
	suffix := ":" + n
	if target != "" {
		suffix = ":" + target + suffix
	}
	var out []tg.Button
	for _, m := range h.gatewayMethods(r.ctx, currency, amount) {
		switch m {
		case "zarinpal":
			out = append(out, r.button("btn.pay_zarinpal", verb+":z"+suffix))
		case "idpay":
			out = append(out, r.button("btn.pay_idpay", verb+":i"+suffix))
		case "nextpay":
			out = append(out, r.button("btn.pay_nextpay", verb+":n"+suffix))
		case "aqayepardakht":
			out = append(out, r.button("btn.pay_aqayepardakht", verb+":a"+suffix))
		case "stars":
			out = append(out, r.button("btn.pay_stars", verb+":s"+suffix))
		case usdtAuto:
			out = append(out, r.button("btn.pay_usdt_auto", verb+":u"+suffix))
		case tonAuto:
			out = append(out, r.button("btn.pay_ton", verb+":t"+suffix))
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

// startGateway starts a Zarinpal, IDPay, NextPay or Aqayepardakht payment (a link to the pay page), sends a
// Telegram Stars invoice or shows where to send automatic USDT, for an order
// or a wallet top-up.
func (h *Handler) startGateway(r *req, order *corev1.Order, provider, title, key string, topup int64) {
	h.startGatewayIn(r, order, provider, title, key, topup, "IRT")
}

// startGatewayIn is startGateway for a top-up in any currency (an order's own currency comes from the order).
func (h *Handler) startGatewayIn(r *req, order *corev1.Order, provider, title, key string, topup int64, topupCurrency string) {
	in := &corev1.CreatePaymentIntentRequest{UserId: r.user.GetId(), Provider: provider}
	if order != nil {
		in.OrderId = order.GetId()
		in.IdempotencyKey = "int:" + order.GetId() + ":" + provider
	} else {
		in.Amount = &commonv1.Money{Amount: topup, Currency: topupCurrency}
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
		r.show(paidText(r, intent), homeKeyboard(r))
		return
	default: // failed or expired: this screen's payment is over, start again
		reason := intent.GetFailureReason()
		if intent.GetStatus() == "expired" {
			reason = "expired"
		}
		r.show(r.t("pay.check_failed", "reason", r.t(reasonKey(reason))), retryKeyboard(r))
		return
	}
	amount := r.money(intent.GetAmount().GetAmount(), intent.GetAmount().GetCurrency())
	switch provider {
	case "zarinpal", "idpay", "nextpay", "aqayepardakht":
		if intent.GetPayUrl() == "" {
			r.show(r.t("pay.not_configured"), homeKeyboard(r))
			return
		}
		kb := (&tg.Keyboard{}).Row(r.linkButton("btn.open_payment", intent.GetPayUrl())).
			Row(r.button("btn.check_payment", "chk:"+intent.GetId())).
			Row(r.button("btn.home", "home"))
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
	case usdtAuto:
		h.showUSDT(r, intent)
	case tonAuto:
		h.showTON(r, intent)
	}
}

// onCheckPayment handles "chk:<intent id>": the customer says they paid.
// An automatic USDT payment not seen yet is not asked about again for a few
// seconds (checkWait): a TRC20 transfer takes about a minute to confirm.
func (h *Handler) onCheckPayment(r *req, intentID string) {
	waitKey := "chk:wait:" + intentID
	if h.checkWait > 0 {
		if recent, err := h.state.Recall(r.ctx, waitKey); err == nil && recent {
			r.toast, r.alert = r.t("pay.check_wait"), true
			return
		}
	}
	ref, err := h.core.CheckPayment(r.ctx, &corev1.CheckPaymentRequest{UserId: r.user.GetId(), IntentId: intentID})
	if err != nil {
		r.fail(err)
		return
	}
	switch ref.GetStatus() {
	case "succeeded":
		r.show(paidText(r, ref), homeKeyboard(r))
	case "failed", "expired":
		reason := ref.GetFailureReason()
		if ref.GetStatus() == "expired" {
			reason = "expired"
		}
		r.show(r.t("pay.check_failed", "reason", r.t(reasonKey(reason))), retryKeyboard(r))
	default:
		if ref.GetProvider() != usdtAuto && ref.GetProvider() != tonAuto {
			r.toast, r.alert = r.t("pay.check_pending"), true
			return
		}
		if h.checkWait > 0 {
			if err := h.state.Remember(r.ctx, waitKey, h.checkWait); err != nil {
				h.log.Debug("check cooldown not saved", "err", err)
			}
		}
		pending := "pay.usdt_check_pending"
		if ref.GetProvider() == tonAuto {
			pending = "pay.ton_check_pending"
		}
		r.toast, r.alert = r.t(pending), true
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
		if err == nil || !retryable(err) || attempt == 5 {
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
		text := "pay.stars_received"
		if ref.GetOrderId() == "" { // a wallet top-up (the Mini App takes them by Stars): no service is being prepared
			text = "pay.check_paid_topup"
		}
		h.sendTo(ctx, m.Chat.ID, h.cat.T(lang, text))
		return
	}
	h.log.Error("Stars payment not recorded as paid", "telegram_id", from.ID, "charge_id", sp.TelegramPaymentChargeID,
		"payload", sp.InvoicePayload, "stars", sp.TotalAmount, "status", ref.GetStatus(), "reason", ref.GetFailureReason(), "err", err)
	h.sendTo(ctx, m.Chat.ID, h.cat.T(lang, "pay.stars_unrecorded", "code", esc(sp.TelegramPaymentChargeID)))
	who := strconv.FormatInt(from.ID, 10)
	if from.Username != "" {
		who = "@" + from.Username + " (" + who + ")"
	}
	// Always sent, also to a staff member who paid: money was taken.
	h.notifyStaff(ctx, 0, "admin.stars_unrecorded",
		"user", esc(who), "stars", func(l string) string { return i18n.Number(l, sp.TotalAmount) },
		"charge", esc(sp.TelegramPaymentChargeID), "payload", esc(sp.InvoicePayload))
}

// userLang is a user's language, or the default when unknown.
func (h *Handler) userLang(ctx context.Context, telegramID int64) string {
	u, err := h.core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: telegramID}})
	if err != nil || u.GetLanguage() == "" {
		return i18n.Default()
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
