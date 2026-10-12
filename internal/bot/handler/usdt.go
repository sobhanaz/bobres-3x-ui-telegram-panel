package handler

import (
	"regexp"
	"strconv"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// Automatic USDT (TRC20): the customer sends an exact amount, unique among
// the open payments, to the store's TRON address. The payments service
// watches the address and settles the payment itself, so the confirmation
// comes as for the other gateways (order paid, or the wallet credited).
// Manual USDT (a TXID or a screenshot, reviewed by staff) stays as it is.

// usdtAuto is the provider's name in core and payments.
const usdtAuto = "usdt_trc20_auto"

// defaultCheckWait is how long the Check button of an automatic USDT
// payment that was not seen yet answers "checked a moment ago" without
// asking core again. Payments reads the address at most every 10 seconds
// whatever the bot does; this keeps a tapping customer off core too.
const defaultCheckWait = 10 * time.Second

// tronAddressRe is the shape of a TRON address. Payments checks the
// checksum; the bot only refuses to show anything else as where to pay.
var tronAddressRe = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)

// showUSDT shows how to pay an automatic USDT payment: the exact amount
// (Latin digits, every decimal) and the address, each in <code> so a tap
// copies it, the time left and the warning, with a Check button. A payment
// whose details are missing or malformed is never shown as payable, and
// neither is one whose time ran out (its rate no longer holds).
func (h *Handler) showUSDT(r *req, in *corev1.PaymentIntentRef) {
	d, exact := in.GetDetails(), in.GetGatewayAmount()
	addr := d["pay_address"]
	expires, err := strconv.ParseInt(d["expires_at"], 10, 64)
	if exact.GetCurrency() != "USDT" || exact.GetAmount() <= 0 || !tronAddressRe.MatchString(addr) ||
		d["network"] != "TRC20" || err != nil || expires <= 0 {
		h.log.Warn("automatic USDT payment without its details", "intent", in.GetId())
		r.show(r.t("pay.not_configured"), homeKeyboard(r))
		return
	}
	until := time.Unix(expires, 0)
	left := time.Until(until)
	if left <= 0 {
		r.show(r.t("pay.check_failed", "reason", r.t(reasonKey("expired"))), retryKeyboard(r))
		return
	}
	kb := (&tg.Keyboard{}).Row(tg.CB(r.t("btn.check_payment"), "chk:"+in.GetId())).Row(tg.CB(r.t("btn.home"), "home"))
	r.show(r.t("pay.usdt_auto",
		"amount", i18n.Exact(exact.GetAmount(), "USDT"), "address", addr,
		"price", r.money(in.GetAmount().GetAmount(), in.GetAmount().GetCurrency()),
		"minutes", i18n.Number(r.lang, max(int64(left/time.Minute), 1)), "time", i18n.Clock(r.lang, until)), kb)
}

// paidText confirms a paid automated payment: an order is being delivered,
// a top-up is in the wallet.
func paidText(r *req, in *corev1.PaymentIntentRef) string {
	if in.GetOrderId() == "" {
		return r.t("pay.check_paid_topup")
	}
	return r.t("pay.check_paid")
}

// retryKeyboard follows a payment that is over: buy again, or home.
func retryKeyboard(r *req) *tg.Keyboard {
	return (&tg.Keyboard{}).Row(tg.CB(r.t("btn.buy"), "buy"), tg.CB(r.t("btn.home"), "home"))
}

// rows lays buttons out at most n to a row (long labels stay readable on a
// phone).
func rows(kb *tg.Keyboard, buttons []tg.Button, n int) {
	for len(buttons) > n {
		kb.Row(buttons[:n:n]...)
		buttons = buttons[n:]
	}
	kb.Row(buttons...)
}
