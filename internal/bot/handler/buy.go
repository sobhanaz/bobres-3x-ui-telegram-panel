package handler

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	commonv1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/common/v1"
	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func planName(r *req, p *corev1.Plan) string {
	names := p.GetNameI18N()
	if n := names[r.lang]; n != "" {
		return n
	}
	for _, l := range i18n.Languages {
		if n := names[l]; n != "" {
			return n
		}
	}
	return "Plan"
}

func planLimits(r *req, p *corev1.Plan) string {
	var lines []string
	if d := p.GetDurationDays(); d > 0 {
		lines = append(lines, r.t("plan.days", "n", i18n.Digits(r.lang, fmt.Sprint(d))))
	} else {
		lines = append(lines, r.t("plan.unlimited_time"))
	}
	if b := p.GetTrafficBytes(); b > 0 {
		lines = append(lines, r.t("plan.traffic", "size", i18n.Bytes(r.lang, b)))
	} else {
		lines = append(lines, r.t("plan.unlimited_traffic"))
	}
	return strings.Join(lines, "\n")
}

// plans returns the enabled, non-trial plans (regular plans and traffic
// packages) at the user's own prices (a reseller's tier prices), or all plans
// including disabled ones at store prices for admins.
func (h *Handler) plans(r *req, all bool) ([]*corev1.Plan, error) {
	in := &corev1.ListPlansRequest{IncludeDisabled: all}
	if !all {
		in.UserId = r.user.GetId()
	}
	resp, err := h.core.ListPlans(r.ctx, in)
	if err != nil {
		return nil, err
	}
	if all {
		return resp.GetPlans(), nil
	}
	var out []*corev1.Plan
	for _, p := range resp.GetPlans() {
		if !p.GetIsTrial() && p.GetEnabled() {
			out = append(out, p)
		}
	}
	return out, nil
}

func (h *Handler) findPlan(r *req, id string, all bool) (*corev1.Plan, error) {
	ps, err := h.plans(r, all)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.GetId() == id {
			return p, nil
		}
	}
	return nil, status.Error(codes.NotFound, "plan not found")
}

// regularPlans are the plans sold as a new service or a renewal (not the
// traffic packages, which are sold from a subscription's page).
func (h *Handler) regularPlans(r *req) ([]*corev1.Plan, error) {
	ps, err := h.plans(r, false)
	if err != nil {
		return nil, err
	}
	out := ps[:0:0]
	for _, p := range ps {
		if !p.GetIsTopup() {
			out = append(out, p)
		}
	}
	return out, nil
}

func (h *Handler) showPlans(r *req) {
	ps, err := h.regularPlans(r)
	if err != nil {
		r.fail(err)
		return
	}
	if len(ps) == 0 {
		r.show(r.t("plans.empty"), homeKeyboard(r))
		return
	}
	kb := &tg.Keyboard{}
	for _, p := range ps {
		kb.Row(tg.CB(r.t("plan.button", "name", planName(r, p), "price", r.money(p.GetPrice().GetAmount(), p.GetPrice().GetCurrency())), "plan:"+p.GetId()))
	}
	kb.Row(r.button("btn.home", "home"))
	r.show(r.t("plans.title"), kb)
}

func (h *Handler) usdtRate() int64 {
	n, ok := i18n.ParseNumber(h.setting("payments.usdt_rate"))
	if !ok || n <= 0 {
		return 0
	}
	return n
}

func (h *Handler) cardEnabled(currency string) bool {
	return currency == "IRT" && h.setting("payments.card_number") != "" && h.methodOn("manual_card")
}

// zarinpalLinkEnabled: the store's Zarinpal payment link, approved manually.
func (h *Handler) zarinpalLinkEnabled(currency string) bool {
	return currency == "IRT" && h.setting("payments.zarinpal_link") != "" && h.methodOn("manual_zarinpal")
}

// methodOn: staff did not switch a manual method off (core refuses it too).
func (h *Handler) methodOn(method string) bool {
	return h.setting("payments."+method+".enabled") != "false"
}

func (h *Handler) cryptoEnabled(currency string) bool {
	if !h.methodOn("manual_crypto") || h.setting("payments.usdt_trc20") == "" && h.setting("payments.usdt_erc20") == "" {
		return false
	}
	return currency == "USDT" || (currency == "IRT" && h.usdtRate() > 0)
}

func (h *Handler) showPlan(r *req, planID string) {
	h.showCheckout(r, state.Checkout{Type: "new", PlanID: planID})
}

// saveCheckout stores what a payment menu sells under a fresh nonce.
func (h *Handler) saveCheckout(r *req, c state.Checkout) (string, bool) {
	n := nonce()
	if err := h.state.SaveCheckout(r.ctx, r.from.ID, n, c); err != nil {
		r.fail(err)
		return "", false
	}
	return n, true
}

// payButtons are the payment methods for an amount: a wallet row, the
// automated gateways and the manual methods, all "pay:<method>:<nonce>".
func (h *Handler) payButtons(r *req, kb *tg.Keyboard, cur string, amount int64, n string, wallet *tg.Button) {
	if wallet != nil {
		kb.Row(*wallet)
	}
	var manual []tg.Button
	if h.cardEnabled(cur) {
		manual = append(manual, r.button("btn.pay_card", "pay:c:"+n))
	}
	if h.cryptoEnabled(cur) {
		manual = append(manual, r.button("btn.pay_crypto", "pay:x:"+n))
	}
	if h.zarinpalLinkEnabled(cur) {
		manual = append(manual, r.button("btn.pay_zarinpal_link", "pay:l:"+n))
	}
	rows(kb, h.gatewayButtons(r, cur, amount, "pay", "", n), 2)
	kb.Row(manual...)
}

// checkoutBack is where a payment menu's Back button leads.
func checkoutBack(c state.Checkout) string {
	switch {
	case c.SubscriptionID != "":
		return "sub:" + c.SubscriptionID
	case c.ForCustomer:
		return "rs:buy"
	}
	return "buy"
}

// showCheckout shows what is bought (a new service, a renewal or a traffic
// package, with any discount code) and how to pay for it. The menu is saved
// under a fresh nonce, so its buttons carry no ids. A reseller pays their
// tier's price, uses no discount codes, and pays from the wallet down to
// their credit limit.
func (h *Handler) showCheckout(r *req, c state.Checkout) {
	p, err := h.findPlan(r, c.PlanID, false)
	if err != nil {
		r.fail(err)
		return
	}
	price, cur := p.GetPrice().GetAmount(), p.GetPrice().GetCurrency()
	reseller := r.isReseller()
	var off int64
	if c.DiscountCode != "" {
		q, err := h.core.QuoteOrder(r.ctx, &corev1.QuoteOrderRequest{UserId: r.user.GetId(), PlanId: c.PlanID,
			Type: c.Type, SubscriptionId: c.SubscriptionID, DiscountCode: c.DiscountCode})
		if err != nil { // the code stopped working since it was entered
			r.send(r.t(discountKey(err)), nil)
			c.DiscountCode = ""
		} else {
			price, off = q.GetTotal().GetAmount(), q.GetDiscount().GetAmount()
		}
	}
	n, ok := h.saveCheckout(r, c)
	if !ok {
		return
	}
	w, err := h.core.GetWallet(r.ctx, &corev1.GetWalletRequest{UserId: r.user.GetId(), Currency: cur})
	if err != nil {
		r.fail(err)
		return
	}
	kb := &tg.Keyboard{}
	available := w.GetBalance() + w.GetCreditLimit()
	var wallet *tg.Button
	switch {
	case !reseller:
		b := r.button("btn.pay_wallet", "pay:w:"+n, "balance", r.money(w.GetBalance(), cur))
		wallet = &b
	case available >= price: // on credit, down to the limit
		b := r.button("btn.pay_credit", "pay:w:"+n, "available", r.money(available, cur))
		wallet = &b
	}
	h.payButtons(r, kb, cur, price, n, wallet)
	if !reseller && c.DiscountCode == "" && price > 0 {
		kb.Row(r.button("btn.discount", "dc:"+n))
	}
	kb.Row(r.button("btn.back", checkoutBack(c)), r.button("btn.home", "home"))
	var text string
	switch c.Type {
	case "renew":
		text = r.t("renew.detail", "id", shortID(c.SubscriptionID), "name", esc(planName(r, p)), "limits", planLimits(r, p), "price", r.money(price, cur))
	case "traffic_topup":
		text = r.t("topup.detail", "id", shortID(c.SubscriptionID), "name", esc(planName(r, p)),
			"size", i18n.Bytes(r.lang, p.GetTrafficBytes()), "price", r.money(price, cur))
	default:
		text = r.t("plan.detail", "name", esc(planName(r, p)), "limits", planLimits(r, p), "price", r.money(price, cur))
	}
	if off > 0 {
		text += "\n" + r.t("discount.applied", "code", esc(c.DiscountCode), "off", r.money(off, cur))
	}
	if reseller {
		text = r.labelled(c.Label, text) + "\n" + r.t("rs.checkout_available", "available", r.money(available, cur))
	}
	r.show(text, kb)
}

// onPay handles "pay:<w|c|x|l|z|i|n|s|u>:<nonce>" (a saved checkout), and the
// "pay:<method>:<plan id>:<nonce>" buttons of menus sent before checkouts.
func (h *Handler) onPay(r *req, rest string) {
	parts := strings.Split(rest, ":")
	var (
		c         state.Checkout
		method, n string
	)
	switch len(parts) {
	case 2:
		method, n = parts[0], parts[1]
		got, ok, err := h.state.LoadCheckout(r.ctx, r.from.ID, n)
		if err != nil {
			r.fail(err)
			return
		}
		if !ok {
			r.show(r.t("error.menu_expired"), (&tg.Keyboard{}).Row(r.button("btn.buy", "buy"), r.button("btn.home", "home")))
			return
		}
		c = got
	case 3:
		method, n = parts[0], parts[2]
		c = state.Checkout{Type: "new", PlanID: parts[1]}
	default:
		h.showPlans(r)
		return
	}
	p, err := h.findPlan(r, c.PlanID, false)
	if err != nil {
		r.fail(err)
		return
	}
	if r.isReseller() { // a code from a menu of before they became one: their price instead
		c.DiscountCode = ""
	}
	order, err := h.core.CreateOrder(r.ctx, &corev1.CreateOrderRequest{
		UserId: r.user.GetId(), PlanId: c.PlanID, Type: c.Type, SubscriptionId: c.SubscriptionID,
		DiscountCode: c.DiscountCode, IdempotencyKey: fmt.Sprintf("ord:%d:%s:%s", r.from.ID, c.PlanID, n),
		Label: c.Label, // kept by core for a reseller's new service only
	})
	if err != nil {
		r.fail(err)
		return
	}
	switch method {
	case "w":
		h.payWithWallet(r, c, p, order)
	case "c":
		h.startManual(r, order, "manual_card", "", 0)
	case "x":
		h.startManual(r, order, "manual_crypto", "", 0)
	case "l":
		h.startManual(r, order, "manual_zarinpal", "", 0)
	case "z":
		h.startGateway(r, order, "zarinpal", planName(r, p), "", 0)
	case "i":
		h.startGateway(r, order, "idpay", planName(r, p), "", 0)
	case "n":
		h.startGateway(r, order, "nextpay", planName(r, p), "", 0)
	case "s":
		h.startGateway(r, order, "stars", planName(r, p), "", 0)
	case "u":
		h.startGateway(r, order, usdtAuto, planName(r, p), "", 0)
	default:
		h.showPlans(r)
	}
}

func (h *Handler) payWithWallet(r *req, c state.Checkout, p *corev1.Plan, order *corev1.Order) {
	_, err := h.core.PayOrderWithWallet(r.ctx, &corev1.PayOrderWithWalletRequest{OrderId: order.GetId(), UserId: r.user.GetId()})
	if err == nil {
		kb := homeKeyboard(r)
		if r.isReseller() {
			kb = (&tg.Keyboard{}).Row(r.button("btn.reseller", "rs"), r.button("btn.home", "home"))
		}
		r.show(r.t("pay.wallet_done"), kb)
		return
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition || !strings.Contains(st.Message(), "insufficient") {
		r.fail(err)
		return
	}
	cur, amount := order.GetAmount().GetCurrency(), order.GetAmount().GetAmount()
	w, _ := h.core.GetWallet(r.ctx, &corev1.GetWalletRequest{UserId: r.user.GetId(), Currency: cur})
	n, ok := h.saveCheckout(r, c) // the same purchase, paid another way
	if !ok {
		return
	}
	if r.isReseller() { // past the credit limit
		kb := &tg.Keyboard{}
		h.payButtons(r, kb, cur, amount, n, nil)
		kb.Row(r.button("btn.reseller", "rs"), r.button("btn.home", "home"))
		r.show(r.t("rs.no_credit", "available", r.money(w.GetBalance()+w.GetCreditLimit(), cur), "price", r.money(amount, cur)), kb)
		return
	}
	kb := (&tg.Keyboard{}).Row(r.button("btn.topup_and_buy", "wtop"))
	h.payButtons(r, kb, cur, amount, n, nil)
	back := "plan:" + p.GetId()
	if c.SubscriptionID != "" {
		back = "sub:" + c.SubscriptionID
	}
	kb.Row(r.button("btn.back", back), r.button("btn.home", "home"))
	r.show(r.t("pay.insufficient", "balance", r.money(w.GetBalance(), cur), "price", r.money(amount, cur)), kb)
}

// startManual creates (or reuses) a manual intent for an order, or a top-up
// when order is nil, and asks for the proof.
func (h *Handler) startManual(r *req, order *corev1.Order, provider, key string, topup int64) {
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
	if intent.GetStatus() != "pending" {
		r.show(r.t("pay.already_submitted"), homeKeyboard(r))
		return
	}
	text, ok := h.instructions(r, intent)
	if !ok {
		r.show(r.t("pay.not_configured"), homeKeyboard(r))
		return
	}
	scene := sceneReceipt
	if provider == "manual_crypto" {
		scene = sceneTXID
	}
	r.setState(scene, map[string]string{
		"intent": intent.GetId(), "provider": provider,
		"amount": fmt.Sprint(intent.GetAmount().GetAmount()), "currency": intent.GetAmount().GetCurrency(),
	})
	kb := cancelKeyboard(r)
	if link := intent.GetDetails()["zarinpal_link"]; provider == "manual_zarinpal" && link != "" {
		kb = &tg.Keyboard{InlineKeyboard: append([][]tg.Button{{r.linkButton("btn.open_payment", link)}}, kb.InlineKeyboard...)}
	}
	r.show(text, kb)
}

// instructions renders how to pay an intent in the user's language.
func (h *Handler) instructions(r *req, in *corev1.PaymentIntentRef) (string, bool) {
	d := in.GetDetails()
	amount, cur := in.GetAmount().GetAmount(), in.GetAmount().GetCurrency()
	switch in.GetProvider() {
	case "manual_card":
		if d["card_number"] == "" {
			return "", false
		}
		return r.t("pay.card", "amount", r.money(amount, cur), "card", esc(d["card_number"]),
			"holder", esc(d["card_holder"]), "ref", esc(d["reference"])), true
	case "manual_crypto":
		var addrs []string
		if a := d["usdt_trc20"]; a != "" {
			addrs = append(addrs, r.t("pay.crypto_trc20", "addr", esc(a)))
		}
		if a := d["usdt_erc20"]; a != "" {
			addrs = append(addrs, r.t("pay.crypto_erc20", "addr", esc(a)))
		}
		if len(addrs) == 0 {
			return "", false
		}
		shown := r.money(amount, cur)
		if cur == "IRT" {
			rate := h.usdtRate()
			if rate == 0 {
				return "", false
			}
			cents := (amount*100 + rate - 1) / rate // round up to 0.01 USDT
			shown = r.t("pay.crypto_equiv", "usdt", i18n.Money(r.lang, cents*10_000, "USDT"), "amount", shown)
		}
		return r.t("pay.crypto", "amount", shown, "addresses", strings.Join(addrs, "\n"), "ref", esc(d["reference"])), true
	case "manual_zarinpal":
		if d["zarinpal_link"] == "" {
			return "", false
		}
		return r.t("pay.zarinpal_link", "amount", r.money(amount, cur), "ref", esc(d["reference"])), true
	}
	return "", false
}

var txidRe = regexp.MustCompile(`^(0x)?[0-9a-fA-F]{64}$`)

// onProof collects a receipt photo (+ bank reference) or a TXID.
func (h *Handler) onProof(r *req, st state.State) {
	intent := st.Get("intent")
	switch st.Scene {
	case sceneReceipt:
		file := r.msg.ImageFile()
		if file == "" {
			r.send(r.t("pay.need_photo"), cancelKeyboard(r))
			return
		}
		ref := strings.TrimSpace(r.msg.Caption)
		if ref == "" {
			next := map[string]string{"file": file}
			for k, v := range st.Data {
				next[k] = v
			}
			r.setState(sceneReference, next)
			r.send(r.t("pay.need_reference"), cancelKeyboard(r))
			return
		}
		h.submitProof(r, st, &corev1.SubmitPaymentProofRequest{IntentId: intent, ReceiptFile: file, ReferenceNumber: ref})
	case sceneReference:
		ref := strings.TrimSpace(r.msg.Text)
		if ref == "" {
			r.send(r.t("pay.need_reference"), cancelKeyboard(r))
			return
		}
		h.submitProof(r, st, &corev1.SubmitPaymentProofRequest{IntentId: intent, ReceiptFile: st.Get("file"), ReferenceNumber: ref})
	case sceneTXID:
		// A screenshot of the transfer is enough: an admin checks it, as with a
		// card receipt. A pasted transaction hash still works.
		if file := r.msg.ImageFile(); file != "" {
			h.submitProof(r, st, &corev1.SubmitPaymentProofRequest{IntentId: intent, ReceiptFile: file,
				ReferenceNumber: strings.TrimSpace(r.msg.Caption)})
			return
		}
		txid := strings.Join(strings.Fields(r.msg.Text), "")
		if !txidRe.MatchString(txid) {
			r.send(r.t("pay.bad_txid"), cancelKeyboard(r))
			return
		}
		network := "TRC20"
		if strings.HasPrefix(txid, "0x") || h.setting("payments.usdt_trc20") == "" {
			network = "ERC20"
		}
		h.submitProof(r, st, &corev1.SubmitPaymentProofRequest{IntentId: intent, Network: network, Txid: txid})
	}
}

func (h *Handler) submitProof(r *req, st state.State, in *corev1.SubmitPaymentProofRequest) {
	in.UserId = r.user.GetId()
	_, err := h.core.SubmitPaymentProof(r.ctx, in)
	r.clearState()
	if err != nil {
		r.fail(err)
		return
	}
	r.send(r.t("pay.submitted"), homeKeyboard(r))
	amount, _ := i18n.ParseNumber(st.Get("amount"))
	h.notifyStaffAbout(r.ctx, r.from.ID, "admin.new_payment", in.GetIntentId(), "user", esc(displayName(r.user)),
		"amount", func(lang string) string { return i18n.Money(lang, amount, st.Get("currency")) })
}

// alertToggles are the notify.* switches (on by default) of the staff alerts
// that have one; other alerts, such as admin.stars_unrecorded, always go out.
// (Ticket alerts are the notifier's, under notify.new_ticket.)
var alertToggles = map[string]string{
	"admin.new_payment": "notify.new_payment",
}

// notifyStaff messages everyone who gets staff alerts (notify.recipients),
// each in their language, except the Telegram user skip (0: nobody), unless
// the alert is switched off. An argument value may be a func(lang) string,
// rendered in each recipient's language.
func (h *Handler) notifyStaff(ctx context.Context, skip int64, key string, args ...any) {
	h.notifyStaffAbout(ctx, skip, key, "", args...)
}

// notifyStaffAbout is notifyStaff for an alert about one payment (reviewIntent,
// "" when it is about none): its Review button opens that payment's card, not
// the list, so whoever taps it sees the payment the alert is about.
func (h *Handler) notifyStaffAbout(ctx context.Context, skip int64, key, reviewIntent string, args ...any) {
	if toggle, ok := alertToggles[key]; ok && !h.set.Bool(toggle, true) {
		return
	}
	for _, c := range h.set.Recipients(h.cfg.AdminTelegramIDs) {
		if c.TelegramID == skip {
			continue
		}
		lang := c.Language
		if lang == "" {
			lang = h.userLang(ctx, c.TelegramID)
		}
		rendered := slices.Clone(args)
		for i := 1; i < len(rendered); i += 2 {
			if f, ok := rendered[i].(func(string) string); ok {
				rendered[i] = f(lang)
			}
		}
		var kb *tg.Keyboard
		if key == "admin.new_payment" {
			data := "adm:pend"
			if reviewIntent != "" {
				data = "adm:pi:" + reviewIntent
			}
			kb = (&tg.Keyboard{}).Row(h.button(lang, "btn.review_now", data))
		}
		if _, err := h.tg.SendMessage(ctx, c.TelegramID, h.cat.T(lang, key, rendered...), kb); err != nil {
			h.log.Warn("staff notification failed", "chat", c.TelegramID, "err", err)
		}
	}
}
