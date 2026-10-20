package handler

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Bulk purchase (docs/03-bot-ux.md): a home-menu button where a customer, or a
// reseller, picks a plan, then how many services (a quick button or a typed
// number), sees the total, and pays for all of them at once from the wallet.
// Core does every rule (the limits, the price, the license's places, all or
// nothing, idempotency); the bot asks, shows what core answers, and keeps the
// conversation: what is being bought is saved under a nonce (state.Checkout,
// Type "bulk"), so the buttons carry no ids and a double tap on Pay repeats the
// same request key, which core answers once.

const (
	// checkoutBulk is the Type of a bulk purchase's saved checkout (it is never
	// a new, renew or traffic_topup order: onPay refuses it).
	checkoutBulk = "bulk"
	// bulkMin is the fewest services in a bulk purchase (core's MinBulkQuantity).
	bulkMin = 2
	// bulkDefaultMax is the most until the owner sets bulk.max_quantity (core's
	// DefaultBulkMax) and bulkHardMax the most the owner can allow (core's
	// MaxBulkQuantity); the bot cannot import core's domain.
	bulkDefaultMax = 20
	bulkHardMax    = 100
)

// bulkQuick are the quantities with a button of their own.
var bulkQuick = []int{2, 3, 5, 10}

// bulkOn: the owner has not switched bulk purchase off (core refuses it too).
func (h *Handler) bulkOn() bool { return h.setting("bulk.enabled") != "false" }

// bulkMax is the most services the owner allows in one purchase. Core checks
// the number again, so a stale value here is only a refusal that comes back
// with its reason.
func (h *Handler) bulkMax() int {
	n, ok := i18n.ParseNumber(h.setting("bulk.max_quantity"))
	if !ok || n < bulkMin {
		return bulkDefaultMax
	}
	return int(min(n, bulkHardMax))
}

// bulkPlans are the plans a bulk purchase can be made of: the regular plans
// that cost something to this customer (core refuses a free one).
func (h *Handler) bulkPlans(r *req) ([]*corev1.Plan, error) {
	ps, err := h.regularPlans(r)
	if err != nil {
		return nil, err
	}
	out := ps[:0:0]
	for _, p := range ps {
		if p.GetPrice().GetAmount() > 0 {
			out = append(out, p)
		}
	}
	return out, nil
}

// routeBulk handles "bk:<verb>:<arg>".
func (h *Handler) routeBulk(r *req, rest string) {
	r.clearState() // a question left open (a number, a name) no longer applies
	if !h.bulkOn() {
		r.toast, r.alert = r.t("bulk.unavailable"), true
		h.showHome(r)
		return
	}
	verb, arg, _ := strings.Cut(rest, ":")
	switch verb {
	case "p": // a plan was picked: how many?
		h.showBulkQuantity(r, arg)
	case "q": // "bk:q:<nonce>:<quantity>": a quick button
		h.onBulkQuantity(r, arg)
	case "o": // another number: type it
		h.askBulkQuantity(r, arg)
	case "c": // back to the total
		if c, ok := h.loadBulkCheckout(r, arg); ok {
			h.showBulkCheckout(r, c)
		}
	case "n": // name the services
		h.askBulkName(r, arg)
	case "nc": // no name
		if c, ok := h.loadBulkCheckout(r, arg); ok {
			c.Label = ""
			h.showBulkCheckout(r, c)
		}
	case "y": // pay
		h.payBulk(r, arg)
	default:
		h.showBulkPlans(r)
	}
}

// loadBulkCheckout reads a saved bulk checkout, or tells the user it expired.
func (h *Handler) loadBulkCheckout(r *req, nonce string) (state.Checkout, bool) {
	c, ok := h.loadCheckout(r, nonce)
	if ok && c.Type != checkoutBulk {
		r.show(r.t("error.menu_expired"), (&tg.Keyboard{}).Row(r.button("btn.bulk", "bk"), r.button("btn.home", "home")))
		return c, false
	}
	return c, ok
}

// showBulkPlans handles "bk": the plans, in the store's layout.
func (h *Handler) showBulkPlans(r *req) {
	ps, err := h.bulkPlans(r)
	if err != nil {
		r.fail(err)
		return
	}
	if len(ps) == 0 {
		r.show(r.t("plans.empty"), homeKeyboard(r))
		return
	}
	kb := &tg.Keyboard{}
	h.addPlans(r, kb, ps, func(p *corev1.Plan) string { return "bk:p:" + p.GetId() })
	kb.Row(r.button("btn.home", "home"))
	r.show(r.t("bulk.title"), kb)
}

// showBulkQuantity handles "bk:p:<plan id>": how many services?
func (h *Handler) showBulkQuantity(r *req, planID string) {
	p, err := h.findPlan(r, planID, false)
	if err != nil {
		r.fail(err)
		return
	}
	if p.GetPrice().GetAmount() <= 0 || p.GetIsTopup() {
		r.toast, r.alert = r.t("plan.unavailable"), true
		h.showBulkPlans(r)
		return
	}
	n, ok := h.saveCheckout(r, state.Checkout{Type: checkoutBulk, PlanID: p.GetId()})
	if !ok {
		return
	}
	max := h.bulkMax()
	var quick []tg.Button
	for _, q := range bulkQuick {
		if q <= max {
			quick = append(quick, tg.CB(i18n.Number(r.lang, int64(q)), "bk:q:"+n+":"+strconv.Itoa(q)))
		}
	}
	kb := (&tg.Keyboard{}).Row(quick...).
		Row(r.button("btn.bulk_other", "bk:o:"+n)).
		Row(r.button("btn.back", "bk"), r.button("btn.home", "home"))
	r.show(r.t("bulk.pick_quantity", "name", esc(planName(r, p)), "limits", planLimits(r, p),
		"price", r.money(p.GetPrice().GetAmount(), p.GetPrice().GetCurrency()),
		"min", i18n.Number(r.lang, bulkMin), "max", i18n.Number(r.lang, int64(max))), kb)
}

// onBulkQuantity handles "bk:q:<nonce>:<quantity>" (a quick button).
func (h *Handler) onBulkQuantity(r *req, arg string) {
	nonce, qs, _ := strings.Cut(arg, ":")
	c, ok := h.loadBulkCheckout(r, nonce)
	if !ok {
		return
	}
	q, err := strconv.Atoi(qs)
	if err != nil || q < bulkMin || q > h.bulkMax() {
		h.showBulkQuantity(r, c.PlanID)
		return
	}
	c.Quantity = int32(q) //nolint:gosec // 2 to 100
	h.showBulkCheckout(r, c)
}

// askBulkQuantity handles "bk:o:<nonce>": the next message is the number.
func (h *Handler) askBulkQuantity(r *req, nonce string) {
	if _, ok := h.loadBulkCheckout(r, nonce); !ok {
		return
	}
	r.setState(sceneBulkQuantity, map[string]string{"co": nonce})
	r.show(r.t("bulk.ask_quantity", "min", i18n.Number(r.lang, bulkMin), "max", i18n.Number(r.lang, int64(h.bulkMax()))), cancelKeyboard(r))
}

// askBulkName handles "bk:n:<nonce>": the next message is the name.
func (h *Handler) askBulkName(r *req, nonce string) {
	c, ok := h.loadBulkCheckout(r, nonce)
	if !ok {
		return
	}
	r.setState(sceneBulkName, map[string]string{"co": nonce})
	kb := (&tg.Keyboard{}).Row(r.button("btn.bulk_no_name", "bk:nc:"+nonce), r.button("btn.cancel", "cancel"))
	r.show(r.t("bulk.ask_name", "max", i18n.Number(r.lang, int64(bulkNameMax(int(c.Quantity))))), kb)
}

// bulkNameMax is the longest name that, with " <n>" for the biggest n, is a
// service name (core's rule: at most 64 characters in all).
func bulkNameMax(quantity int) int { return maxLabel - 1 - len(strconv.Itoa(quantity)) }

// onBulkScene takes what the customer typed: the number of services, or the
// name they share. A text that will not do is explained and asked again.
func (h *Handler) onBulkScene(r *req, st state.State) {
	nonce := st.Get("co")
	switch st.Scene {
	case sceneBulkQuantity:
		q, ok := i18n.ParseNumber(strings.TrimSpace(r.msg.Text))
		max := h.bulkMax()
		if !ok || q < bulkMin || q > int64(max) {
			r.send(r.t("bulk.bad_quantity", "min", i18n.Number(r.lang, bulkMin), "max", i18n.Number(r.lang, int64(max))), cancelKeyboard(r))
			return
		}
		c, ok := h.loadBulkCheckout(r, nonce)
		r.clearState()
		if !ok {
			return
		}
		c.Quantity = int32(q) //nolint:gosec // 2 to 100
		h.showBulkCheckout(r, c)
	case sceneBulkName:
		c, ok := h.loadBulkCheckout(r, nonce)
		if !ok {
			r.clearState()
			return
		}
		name, good := cleanLabel(r.msg.Text)
		if !good || utf8.RuneCountInString(name) > bulkNameMax(int(c.Quantity)) {
			kb := (&tg.Keyboard{}).Row(r.button("btn.bulk_no_name", "bk:nc:"+nonce), r.button("btn.cancel", "cancel"))
			r.send(r.t("bulk.bad_name", "max", i18n.Number(r.lang, int64(bulkNameMax(int(c.Quantity))))), kb)
			return
		}
		r.clearState()
		c.Label = name
		h.showBulkCheckout(r, c)
	}
}

// showBulkCheckout shows what is being bought, priced by core, with the wallet
// beside it: Pay when it covers the total, else what is missing and the way
// to top up. The checkout is saved again under a fresh nonce, so each screen's
// buttons are their own and a repeated tap on Pay is one purchase.
func (h *Handler) showBulkCheckout(r *req, c state.Checkout) {
	p, err := h.findPlan(r, c.PlanID, false)
	if err != nil {
		r.fail(err)
		return
	}
	q, err := h.core.QuoteBulkOrder(r.ctx, &corev1.QuoteBulkOrderRequest{UserId: r.user.GetId(), PlanId: c.PlanID, Quantity: c.Quantity})
	if err != nil {
		h.bulkFail(r, c, err)
		return
	}
	cur, total := q.GetTotal().GetCurrency(), q.GetTotal().GetAmount()
	c.Total, c.Currency = total, cur
	n, ok := h.saveCheckout(r, c)
	if !ok {
		return
	}
	w, err := h.core.GetWallet(r.ctx, &corev1.GetWalletRequest{UserId: r.user.GetId(), Currency: cur})
	if err != nil {
		r.fail(err)
		return
	}
	reseller := r.isReseller()
	available := w.GetBalance()
	if reseller {
		available += w.GetCreditLimit()
	}
	kb := &tg.Keyboard{}
	wallet := r.t("bulk.wallet_ok", "balance", r.balance(w.GetBalance(), cur))
	if reseller {
		wallet = r.t("rs.checkout_available", "available", r.money(available, cur))
	}
	if available >= total {
		kb.Row(r.button("btn.bulk_pay", "bk:y:"+n, "total", r.money(total, cur)))
	} else if reseller { // past their credit: they settle with the store, there is no top-up
		wallet = r.t("bulk.no_credit", "available", r.money(available, cur), "n", i18n.Number(r.lang, int64(c.Quantity)), "total", r.money(total, cur))
	} else {
		wallet = r.t("bulk.wallet_short", "balance", r.balance(w.GetBalance(), cur), "short", r.money(total-available, cur))
		if row := h.bulkTopupRow(r, cur, total-available); row != nil {
			kb.Row(row...)
		}
	}
	if c.Label == "" {
		kb.Row(r.button("btn.bulk_name", "bk:n:"+n))
	} else {
		kb.Row(r.button("btn.bulk_name_change", "bk:n:"+n), r.button("btn.bulk_no_name", "bk:nc:"+n))
	}
	kb.Row(r.button("btn.bulk_change_quantity", "bk:p:"+c.PlanID)).
		Row(r.button("btn.back", "bk"), r.button("btn.home", "home"))
	names := ""
	if c.Label != "" {
		names = r.t("bulk.names_line", "first", esc(c.Label+" 1"), "last", esc(c.Label+" "+strconv.Itoa(int(c.Quantity))))
	}
	r.show(r.t("bulk.checkout", "name", esc(planName(r, p)), "limits", planLimits(r, p),
		"n", i18n.Number(r.lang, int64(c.Quantity)), "price", r.money(q.GetUnitPrice().GetAmount(), cur),
		"total", r.money(total, cur), "names", names, "wallet", wallet), kb)
}

// bulkTopupRow is the button that tops the wallet up by what is missing (the
// amounts screen when that is not an amount a top-up takes, and nothing for a
// reseller, who buys on credit, or a currency the wallet cannot be topped up in).
func (h *Handler) bulkTopupRow(r *req, currency string, short int64) []tg.Button {
	if r.isReseller() || currency != "IRT" {
		return nil
	}
	data := "wtop"
	if short >= minTopup && short <= maxTopup {
		data = fmt.Sprintf("wamt:%d", short)
	}
	return []tg.Button{r.button("btn.topup_and_buy", data)}
}

// payBulk handles "bk:y:<nonce>": core pays for every service from the wallet,
// all or nothing. The request key is the nonce's, so a double tap (or a retry
// after an answer that never came) is the same purchase.
func (h *Handler) payBulk(r *req, nonce string) {
	c, ok := h.loadBulkCheckout(r, nonce)
	if !ok {
		return
	}
	b, err := h.core.CreateBulkOrder(r.ctx, &corev1.CreateBulkOrderRequest{
		UserId: r.user.GetId(), PlanId: c.PlanID, Quantity: c.Quantity,
		IdempotencyKey: fmt.Sprintf("bulk:%d:%s", r.from.ID, nonce), LabelPrefix: c.Label,
		ExpectedTotal: &commonv1.Money{Amount: c.Total, Currency: c.Currency},
	})
	if err != nil {
		h.bulkFail(r, c, err)
		return
	}
	kb := (&tg.Keyboard{}).Row(r.button("btn.services", "subs"))
	if r.isReseller() {
		kb.Row(r.button("btn.reseller", "rs"), r.button("btn.home", "home"))
	} else {
		kb.Row(r.button("btn.home", "home"))
	}
	r.show(r.t("bulk.paid", "n", i18n.Number(r.lang, int64(b.GetQuantity())),
		"total", r.money(b.GetTotal().GetAmount(), b.GetTotal().GetCurrency())), kb)
}

// bulkFail answers what core refused: a wallet that falls short (with the way
// to top up), a price that changed since it was shown (the new total follows),
// a switched-off store or a quantity beyond the limit now (back to the number),
// and anything else as every purchase's failure is answered.
func (h *Handler) bulkFail(r *req, c state.Checkout, err error) {
	st, _ := status.FromError(err)
	msg := strings.ToLower(st.Message())
	switch {
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "insufficient"):
		h.showBulkShort(r, c)
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "price changed"):
		r.toast, r.alert = r.t("bulk.price_changed"), true
		h.showBulkCheckout(r, c)
	case st.Code() == codes.FailedPrecondition && strings.Contains(msg, "turned off"):
		r.toast, r.alert = r.t("bulk.unavailable"), true
		h.showHome(r)
	case st.Code() == codes.InvalidArgument && strings.Contains(msg, "bulk purchase is"): // the limit moved
		r.toast, r.alert = r.t("bulk.bad_quantity", "min", i18n.Number(r.lang, bulkMin), "max", i18n.Number(r.lang, int64(h.bulkMax()))), true
		h.showBulkQuantity(r, c.PlanID)
	default:
		r.fail(err)
	}
}

// showBulkShort says the wallet cannot pay for it all, and what to do.
func (h *Handler) showBulkShort(r *req, c state.Checkout) {
	cur := c.Currency
	w, err := h.core.GetWallet(r.ctx, &corev1.GetWalletRequest{UserId: r.user.GetId(), Currency: cur})
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	n := strconv.Itoa(int(c.Quantity))
	if r.isReseller() { // past the credit limit: they settle with the store
		avail := w.GetBalance() + w.GetCreditLimit()
		kb.Row(r.button("btn.back", "bk"), r.button("btn.reseller", "rs"))
		r.show(r.t("bulk.no_credit", "available", r.money(avail, cur), "n", n, "total", r.money(c.Total, cur)), kb)
		return
	}
	short := c.Total - w.GetBalance()
	if row := h.bulkTopupRow(r, cur, short); row != nil {
		kb.Row(row...)
	}
	kb.Row(r.button("btn.back", "bk"), r.button("btn.home", "home"))
	r.show(r.t("bulk.insufficient", "balance", r.balance(w.GetBalance(), cur), "n", n,
		"total", r.money(c.Total, cur), "short", r.money(short, cur)), kb)
}
