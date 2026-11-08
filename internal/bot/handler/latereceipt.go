package handler

import (
	"fmt"
	"strings"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/state"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/tg"
)

// A receipt that comes when the bot is not waiting for one (docs/03-bot-ux.md, "Payments"): the step that asked for
// it closed (it lasts state.TTL, and a bank transfer often takes longer, or the customer tapped something else
// meanwhile), or the customer sent it without starting a payment. A picture was answered with the home screen and
// the receipt was lost. Now the customer's own manual payments still waiting for their proof are offered, one
// button each (with the reference the payment screen showed), and a tap sends the picture as that payment's
// receipt, exactly as the step would have (with the bank reference asked for when the caption had none). With no
// payment waiting, the customer is told how to send one. The gates let it through as the proof of a payment under
// way (payingNow, maintenancePasses, exempt).

// sceneReceiptPick: a picture came with no payment step; the customer picks the payment it is the receipt for.
const sceneReceiptPick = "receipt_pick"

// pendingOffered is how many payments waiting for a receipt are offered at most.
const pendingOffered = 5

// onLateReceipt offers the customer's payments waiting for a receipt for the picture file (caption ref: the bank
// reference, if they wrote one), or says how to pay when none waits.
func (h *Handler) onLateReceipt(r *req, file, ref string) {
	resp, err := h.core.ListMyPendingPayments(r.ctx, &corev1.ListMyPendingPaymentsRequest{UserId: r.user.GetId(), Limit: pendingOffered})
	if err != nil {
		r.fail(err)
		return
	}
	if len(resp.GetPayments()) == 0 {
		r.clearState()
		if r.held { // let through as a receipt, but none is waiting: the gate's answer
			h.answerGate(r)
			return
		}
		kb := (&tg.Keyboard{}).Row(r.button("btn.topup", "wtop"), r.button("btn.support", "support")).Row(r.button("btn.home", "home"))
		r.send(r.t("pay.late_none"), kb)
		return
	}
	r.setState(sceneReceiptPick, map[string]string{"file": file, "ref": ref})
	kb := &tg.Keyboard{}
	for _, p := range resp.GetPayments() {
		kb.Row(tg.CB(h.pendingLabel(r, p), "rcp:"+p.GetIntentId()))
	}
	kb.Row(r.button("btn.cancel", "cancel"))
	r.send(r.t("pay.late_pick"), kb)
}

// onReceiptPickScene: a message while the customer picks the payment: another picture replaces the one kept, and
// a text is taken as the bank reference; the choice is offered again.
func (h *Handler) onReceiptPickScene(r *req, st state.State) {
	if file := r.msg.ImageFile(); file != "" {
		h.onLateReceipt(r, file, strings.TrimSpace(r.msg.Caption))
		return
	}
	ref := strings.TrimSpace(r.msg.Text)
	if ref == "" {
		ref = st.Get("ref")
	}
	h.onLateReceipt(r, st.Get("file"), ref)
}

// onReceiptPick handles "rcp:<intent id>": the kept picture is the receipt of that payment, if it still waits for
// one and is the customer's own (core lists only those).
func (h *Handler) onReceiptPick(r *req, intentID string) {
	st, err := h.state.Get(r.ctx, r.from.ID)
	if err != nil || st.Scene != sceneReceiptPick || st.Get("file") == "" {
		r.toast = r.t("error.expired")
		h.showHome(r)
		return
	}
	resp, err := h.core.ListMyPendingPayments(r.ctx, &corev1.ListMyPendingPaymentsRequest{UserId: r.user.GetId(), Limit: pendingOffered})
	if err != nil {
		r.fail(err)
		return
	}
	var p *corev1.MyPendingPayment
	for _, it := range resp.GetPayments() {
		if it.GetIntentId() == intentID {
			p = it
		}
	}
	if p == nil {
		r.clearState()
		r.show(r.t("pay.late_gone"), homeKeyboard(r))
		return
	}
	data := map[string]string{"intent": p.GetIntentId(), "provider": p.GetProvider(),
		"amount": fmt.Sprint(p.GetAmount().GetAmount()), "currency": p.GetAmount().GetCurrency()}
	file, ref := st.Get("file"), st.Get("ref")
	if ref != "" || p.GetProvider() == "manual_crypto" { // a crypto screenshot needs no bank reference
		h.submitProof(r, state.State{Scene: sceneReceipt, Data: data},
			&corev1.SubmitPaymentProofRequest{IntentId: p.GetIntentId(), ReceiptFile: file, ReferenceNumber: ref})
		return
	}
	data["file"] = file
	r.setState(sceneReference, data)
	r.show(r.t("pay.need_reference"), cancelKeyboard(r))
}

// pendingLabel names a payment waiting for a receipt on its button: what it pays for, how much, and the
// reference its payment screen showed (the same PaymentReference core gives it).
func (h *Handler) pendingLabel(r *req, p *corev1.MyPendingPayment) string {
	key := "pay.late_topup"
	if p.GetOrderId() != "" {
		key = "pay.late_order"
	}
	return r.t(key, "amount", r.money(p.GetAmount().GetAmount(), p.GetAmount().GetCurrency()), "ref", paymentReference(p.GetIntentId()))
}

// paymentReference is the reference a payment's screen shows (core's PaymentReference): the last 8 characters of
// its id without dashes, in capitals.
func paymentReference(intentID string) string {
	ref := strings.ReplaceAll(intentID, "-", "")
	if len(ref) > 8 {
		ref = ref[len(ref)-8:]
	}
	return strings.ToUpper(ref)
}
