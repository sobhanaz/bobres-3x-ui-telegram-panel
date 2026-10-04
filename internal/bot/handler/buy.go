package handler

import (
	"fmt"
	"regexp"
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

// plans returns the purchasable (enabled, non-trial) plans, or all plans
// including disabled ones for admins.
func (h *Handler) plans(r *req, all bool) ([]*corev1.Plan, error) {
	resp, err := h.core.ListPlans(r.ctx, &corev1.ListPlansRequest{IncludeDisabled: all})
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

func (h *Handler) showPlans(r *req) {
	ps, err := h.plans(r, false)
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
	kb.Row(tg.CB(r.t("btn.home"), "home"))
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
	return currency == "IRT" && h.setting("payments.card_number") != ""
}

func (h *Handler) cryptoEnabled(currency string) bool {
	if h.setting("payments.usdt_trc20") == "" && h.setting("payments.usdt_erc20") == "" {
		return false
	}
	return currency == "USDT" || (currency == "IRT" && h.usdtRate() > 0)
}

func (h *Handler) showPlan(r *req, planID string) {
	p, err := h.findPlan(r, planID, false)
	if err != nil {
		r.fail(err)
		return
	}
	price, cur := p.GetPrice().GetAmount(), p.GetPrice().GetCurrency()
	w, err := h.core.GetWallet(r.ctx, &corev1.GetWalletRequest{UserId: r.user.GetId(), Currency: cur})
	if err != nil {
		r.fail(err)
		return
	}
	n := nonce()
	kb := (&tg.Keyboard{}).Row(tg.CB(r.t("btn.pay_wallet", "balance", r.money(w.GetBalance(), cur)), "pay:w:"+planID+":"+n))
	var manual []tg.Button
	if h.cardEnabled(cur) {
		manual = append(manual, tg.CB(r.t("btn.pay_card"), "pay:c:"+planID+":"+n))
	}
	if h.cryptoEnabled(cur) {
		manual = append(manual, tg.CB(r.t("btn.pay_crypto"), "pay:x:"+planID+":"+n))
	}
	if gw := h.gatewayButtons(r, cur, price, "pay", planID, n); len(gw) > 0 {
		kb.Row(gw...)
	}
	kb.Row(manual...).Row(tg.CB(r.t("btn.back"), "buy"), tg.CB(r.t("btn.home"), "home"))
	r.show(r.t("plan.detail", "name", esc(planName(r, p)), "limits", planLimits(r, p), "price", r.money(price, cur)), kb)
}

// onPay handles "pay:<w|c|x|z|s>:<plan id>:<nonce>".
func (h *Handler) onPay(r *req, rest string) {
	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		h.showPlans(r)
		return
	}
	method, planID, n := parts[0], parts[1], parts[2]
	p, err := h.findPlan(r, planID, false)
	if err != nil {
		r.fail(err)
		return
	}
	order, err := h.core.CreateOrder(r.ctx, &corev1.CreateOrderRequest{
		UserId: r.user.GetId(), PlanId: planID, IdempotencyKey: fmt.Sprintf("ord:%d:%s:%s", r.from.ID, planID, n),
	})
	if err != nil {
		r.fail(err)
		return
	}
	switch method {
	case "w":
		h.payWithWallet(r, p, order)
	case "c":
		h.startManual(r, order, "manual_card", "", 0)
	case "x":
		h.startManual(r, order, "manual_crypto", "", 0)
	case "z":
		h.startGateway(r, order, "zarinpal", planName(r, p), "", 0)
	case "s":
		h.startGateway(r, order, "stars", planName(r, p), "", 0)
	default:
		h.showPlans(r)
	}
}

func (h *Handler) payWithWallet(r *req, p *corev1.Plan, order *corev1.Order) {
	_, err := h.core.PayOrderWithWallet(r.ctx, &corev1.PayOrderWithWalletRequest{OrderId: order.GetId(), UserId: r.user.GetId()})
	if err == nil {
		r.show(r.t("pay.wallet_done"), homeKeyboard(r))
		return
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.FailedPrecondition || !strings.Contains(st.Message(), "insufficient") {
		r.fail(err)
		return
	}
	cur := p.GetPrice().GetCurrency()
	w, _ := h.core.GetWallet(r.ctx, &corev1.GetWalletRequest{UserId: r.user.GetId(), Currency: cur})
	n := nonce()
	kb := (&tg.Keyboard{}).Row(tg.CB(r.t("btn.topup_and_buy"), "wtop"))
	var manual []tg.Button
	if h.cardEnabled(cur) {
		manual = append(manual, tg.CB(r.t("btn.pay_card"), "pay:c:"+p.GetId()+":"+n))
	}
	if h.cryptoEnabled(cur) {
		manual = append(manual, tg.CB(r.t("btn.pay_crypto"), "pay:x:"+p.GetId()+":"+n))
	}
	if gw := h.gatewayButtons(r, cur, p.GetPrice().GetAmount(), "pay", p.GetId(), n); len(gw) > 0 {
		kb.Row(gw...)
	}
	kb.Row(manual...).Row(tg.CB(r.t("btn.back"), "plan:"+p.GetId()), tg.CB(r.t("btn.home"), "home"))
	r.show(r.t("pay.insufficient", "balance", r.money(w.GetBalance(), cur), "price", r.money(p.GetPrice().GetAmount(), cur)), kb)
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
	r.show(text, cancelKeyboard(r))
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
	}
	return "", false
}

var txidRe = regexp.MustCompile(`^(0x)?[0-9a-fA-F]{64}$`)

// onProof collects a receipt photo (+ bank reference) or a TXID.
func (h *Handler) onProof(r *req, st state.State) {
	intent := st.Get("intent")
	switch st.Scene {
	case sceneReceipt:
		file := r.msg.LargestPhoto()
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
	h.notifyAdmin(r, "admin.new_payment", "user", esc(displayName(r.user)),
		"amount", func(lang string) string { return i18n.Money(lang, amount, st.Get("currency")) })
}

// notifyAdmin messages the configured admin in their language. An argument
// value may be a func(lang) string, rendered in the admin's language.
func (h *Handler) notifyAdmin(r *req, key string, args ...any) {
	if h.cfg.AdminTelegramID == 0 || h.cfg.AdminTelegramID == r.from.ID {
		return
	}
	lang := "fa"
	if u, err := h.core.GetUser(r.ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: h.cfg.AdminTelegramID}}); err == nil {
		lang = u.GetLanguage()
	}
	for i := 1; i < len(args); i += 2 {
		if f, ok := args[i].(func(string) string); ok {
			args[i] = f(lang)
		}
	}
	text := h.cat.T(lang, key, args...)
	var kb *tg.Keyboard
	if key == "admin.new_payment" {
		kb = (&tg.Keyboard{}).Row(tg.CB(h.cat.T(lang, "btn.review_now"), "adm:pend"))
	}
	if _, err := h.tg.SendMessage(r.ctx, h.cfg.AdminTelegramID, text, kb); err != nil {
		h.log.Warn("admin notification failed", "err", err)
	}
}
