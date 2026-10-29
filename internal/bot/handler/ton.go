package handler

import (
	"regexp"
	"strconv"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// Automatic TON (Toncoin): the customer sends the exact amount of TON, with the payment's own comment (its memo),
// to the store's address. The payments service watches the address and settles the payment itself, so the
// confirmation comes as for the other gateways (order paid, or the wallet credited). The amount is worked out when
// the payment is created, at the TON price then, and held for 30 minutes.

// tonAuto is the provider's name in core and payments.
const tonAuto = "ton_auto"

var (
	// tonAddressRe is the shape of a TON address as shown to customers (48 characters of base64, URL-safe).
	// Payments checks the checksum; the bot only refuses to show anything else as where to pay.
	tonAddressRe = regexp.MustCompile(`^[A-Za-z0-9_-]{48}$`)
	// tonMemoRe is a payment's comment: BOB- and the last 8 hex digits of its id.
	tonMemoRe = regexp.MustCompile(`^BOB-[0-9A-F]{8}$`)
)

// showTON shows how to pay an automatic TON payment: the exact amount (Latin digits, every decimal), the address and
// the comment, each in <code> so a tap copies it, the time left and the warning, with a Check button. A payment
// whose details are missing or malformed is never shown as payable, and neither is one whose time ran out (its
// price no longer holds).
func (h *Handler) showTON(r *req, in *corev1.PaymentIntentRef) {
	d, exact := in.GetDetails(), in.GetGatewayAmount()
	addr, memo := d["pay_address"], d["memo"]
	expires, err := strconv.ParseInt(d["expires_at"], 10, 64)
	if exact.GetCurrency() != "TON" || exact.GetAmount() <= 0 || !tonAddressRe.MatchString(addr) || !tonMemoRe.MatchString(memo) ||
		d["network"] != "TON" || err != nil || expires <= 0 {
		h.log.Warn("automatic TON payment without its details", "intent", in.GetId())
		r.show(r.t("pay.not_configured"), homeKeyboard(r))
		return
	}
	until := time.Unix(expires, 0)
	left := time.Until(until)
	if left <= 0 {
		r.show(r.t("pay.check_failed", "reason", r.t(reasonKey("expired"))), retryKeyboard(r))
		return
	}
	kb := (&tg.Keyboard{}).Row(r.button("btn.check_payment", "chk:"+in.GetId())).Row(r.button("btn.home", "home"))
	r.show(r.t("pay.ton_auto",
		"amount", i18n.Exact(exact.GetAmount(), "TON"), "address", addr, "memo", memo,
		"price", r.money(in.GetAmount().GetAmount(), in.GetAmount().GetCurrency()),
		"minutes", i18n.Number(r.lang, max(int64(left/time.Minute), 1)), "time", i18n.Clock(r.lang, until)), kb)
}
