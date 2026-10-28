package handler

import (
	"math"
	"math/big"
	"strings"
	"time"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A plan priced in USDT paid from the wallet (docs/03-bot-ux.md, "Plans priced in USDT"). The wallet is in Toman, so core prices the plan in Toman at the
// store's USDT rate (QuoteWalletPayment) and the customer is shown the USDT price, the rate and the Toman that will be taken before anything is charged.
// The quote holds for two minutes: the confirming button sends its token back and core charges exactly that; a payment that comes later is refused
// ("the price quote expired") and the customer is shown a new quote. The bot holds no rule: the price, the rate, the rounding and the charge are core's.
//
// The callbacks are "pay:w:<nonce>" (the wallet button of the payment menu: makes the order and shows the quote) and "pq:<nonce>" (the confirming
// button: the nonce keeps the order and the quote's token, so a double tap is one payment).

// quoteMinutes is how long a quote still holds, in whole minutes, at least 1: what the screen says.
func quoteMinutes(expires int64) int64 {
	left := time.Until(time.Unix(expires, 0))
	return max(int64(math.Ceil(left.Minutes())), 1)
}

// tomanForUsdt is a price in USDT (minor units) in Toman at a rate, rounded up as core does: an estimate for the payment menu, before the quote.
func tomanForUsdt(micro, rate int64) int64 {
	if micro <= 0 || rate <= 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(micro), big.NewInt(rate))
	q, rem := new(big.Int).QuoRem(n, big.NewInt(1_000_000), new(big.Int))
	if rem.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0
	}
	return q.Int64()
}

// usdtHint is the line under a USDT price in the payment menu: about what the wallet would be charged, at the rate the bot knows now. "" with no rate.
func (h *Handler) usdtHint(r *req, micro int64) string {
	rate := h.usdtRate()
	toman := tomanForUsdt(micro, rate)
	if toman <= 0 {
		return ""
	}
	return r.t("plan.usdt_hint", "toman", r.money(toman, "IRT"), "rate", r.money(rate, "IRT"))
}

// walletMessageHas reports whether core refused with a FAILED_PRECONDITION whose message says what (lower case).
func walletMessageHas(err error, what string) bool {
	st, _ := status.FromError(err)
	return st.Code() == codes.FailedPrecondition && strings.Contains(strings.ToLower(st.Message()), what)
}

// quoteBack is where the Back button of the quote screens leads: the payment menu of the purchase.
func quoteBack(c state.Checkout, p *corev1.Plan) string {
	if c.SubscriptionID != "" {
		return "sub:" + c.SubscriptionID
	}
	return "plan:" + p.GetId()
}

// showWalletQuote asks core for the price of a USDT order in Toman and shows it with the button that pays it. A wallet that cannot cover it is told how
// much is missing, and the other ways to pay stay; with no USDT rate the customer is told nothing can be priced now. renewed says this quote replaces
// one that ran out, which the screen says first.
func (h *Handler) showWalletQuote(r *req, c state.Checkout, p *corev1.Plan, orderID string, renewed bool) {
	c.OrderID, c.Quote = "", ""
	q, err := h.core.QuoteWalletPayment(r.ctx, &corev1.QuoteWalletPaymentRequest{OrderId: orderID, UserId: r.user.GetId()})
	if err != nil {
		switch {
		case walletMessageHas(err, "usdt rate is not known"):
			h.walletUnpriced(r, c, p)
		case walletMessageHas(err, "not payable"):
			r.show(r.t("pay.check_paid"), homeKeyboard(r)) // paid meanwhile, by this screen's other tap or another way
		default:
			r.fail(err)
		}
		return
	}
	list, price := q.GetListPrice(), q.GetPrice()
	balance, credit := q.GetBalance().GetAmount(), q.GetCreditLimit()
	text := r.t("quote.screen", "name", esc(planName(r, p)), "usdt", r.money(list.GetAmount(), list.GetCurrency()), "rate", r.money(q.GetRate(), "IRT"),
		"price", r.money(price.GetAmount(), price.GetCurrency()), "balance", r.balance(balance, "IRT"),
		"minutes", i18n.Number(r.lang, quoteMinutes(q.GetExpiresAt())), "time", i18n.Clock(r.lang, time.Unix(q.GetExpiresAt(), 0)))
	if renewed {
		text = r.t("quote.updated") + "\n\n" + text
	}
	if r.isReseller() {
		text += "\n" + r.t("rs.checkout_available", "available", r.money(balance+credit, "IRT"))
	}
	back := quoteBack(c, p)
	kb := &tg.Keyboard{}
	if q.GetEnough() {
		// What this screen is about, kept under the nonce of its button: the tap on it is the customer's yes to this price.
		pay := c
		pay.OrderID, pay.Quote = orderID, q.GetToken()
		n, ok := h.saveCheckout(r, pay)
		if !ok {
			return
		}
		kb.Row(r.button("btn.quote_pay", "pq:"+n, "price", r.money(price.GetAmount(), price.GetCurrency())))
		kb.Row(r.button("btn.back", back), r.button("btn.home", "home"))
		r.show(text, kb)
		return
	}
	// Short: the same purchase can be paid another way, or after a top-up.
	n, ok := h.saveCheckout(r, c)
	if !ok {
		return
	}
	text += "\n\n" + r.t("quote.short", "missing", r.money(price.GetAmount()-balance-credit, "IRT"))
	if !r.isReseller() {
		kb.Row(r.button("btn.topup_and_buy", "wtop"))
	}
	h.payButtons(r, kb, list.GetCurrency(), list.GetAmount(), n, nil)
	kb.Row(r.button("btn.back", back), r.button("btn.home", "home"))
	r.show(text, kb)
}

// walletUnpriced answers a wallet payment of a USDT plan when the store knows no USDT rate: the plan cannot be priced in Toman now, so the customer is
// told so and offered the ways that need no rate (crypto), without a word on how the store works inside.
func (h *Handler) walletUnpriced(r *req, c state.Checkout, p *corev1.Plan) {
	n, ok := h.saveCheckout(r, c)
	if !ok {
		return
	}
	kb := &tg.Keyboard{}
	h.payButtons(r, kb, p.GetPrice().GetCurrency(), p.GetPrice().GetAmount(), n, nil)
	kb.Row(r.button("btn.back", quoteBack(c, p)), r.button("btn.home", "home"))
	r.show(r.t("quote.no_rate"), kb)
}

// onQuotePay handles "pq:<nonce>": the customer confirmed the price they were shown. Core charges exactly that quote while it holds; one that ran out
// shows a new quote (the customer confirms again), a wallet that no longer covers it shows the shortfall, and a repeat of the tap finds the order paid.
func (h *Handler) onQuotePay(r *req, n string) {
	c, ok, err := h.state.LoadCheckout(r.ctx, r.from.ID, n)
	if err != nil {
		r.fail(err)
		return
	}
	if !ok || c.OrderID == "" || c.Quote == "" {
		r.show(r.t("error.menu_expired"), (&tg.Keyboard{}).Row(r.button("btn.buy", "buy"), r.button("btn.home", "home")))
		return
	}
	paid, err := h.core.PayOrderWithWallet(r.ctx, &corev1.PayOrderWithWalletRequest{OrderId: c.OrderID, UserId: r.user.GetId(), Quote: c.Quote})
	switch {
	case err == nil:
	case walletMessageHas(err, "quote expired"):
		h.requote(r, c, true)
		return
	case walletMessageHas(err, "insufficient"):
		h.requote(r, c, false) // the wallet is not what it was: the new screen says what is short
		return
	case walletMessageHas(err, "not payable"):
		r.show(r.t("pay.check_paid"), homeKeyboard(r))
		return
	default:
		r.fail(err)
		return
	}
	kb := homeKeyboard(r)
	if r.isReseller() {
		kb = (&tg.Keyboard{}).Row(r.button("btn.reseller", "rs"), r.button("btn.home", "home"))
	}
	if paid.GetListPrice() == nil || paid.GetRate() <= 0 {
		r.show(r.t("pay.wallet_done"), kb)
		return
	}
	r.show(r.t("quote.paid", "price", r.money(paid.GetAmount().GetAmount(), paid.GetAmount().GetCurrency()),
		"usdt", r.money(paid.GetListPrice().GetAmount(), paid.GetListPrice().GetCurrency()), "rate", r.money(paid.GetRate(), "IRT")), kb)
}

// requote shows a new quote for the order a confirm button was about.
func (h *Handler) requote(r *req, c state.Checkout, renewed bool) {
	p, err := h.findPlan(r, c.PlanID, false)
	if err != nil {
		r.fail(err)
		return
	}
	h.showWalletQuote(r, c, p, c.OrderID, renewed)
}
