package handler

import (
	"context"
	"strings"
	"testing"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
)

// miniProof does what the Mini App's calls do in core, as the customer: an
// order, the card payment (with the bot's own idempotency key) and the proof,
// a receipt stored as a Telegram file id. It returns the payment's id.
func miniProof(t *testing.T, w *world, customer *person, receiptFile, reference string) string {
	t.Helper()
	ctx := context.Background()
	u, err := w.stack.Core.GetUser(ctx, &corev1.GetUserRequest{Lookup: &corev1.GetUserRequest_TelegramId{TelegramId: customer.id}})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := w.stack.Core.ListPlans(ctx, &corev1.ListPlansRequest{UserId: u.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	var planID string
	for _, p := range plans.GetPlans() {
		if p.GetEnabled() && !p.GetIsTrial() {
			planID = p.GetId()
		}
	}
	order, err := w.stack.Core.CreateOrder(ctx, &corev1.CreateOrderRequest{UserId: u.GetId(), PlanId: planID, IdempotencyKey: "mini:" + u.GetId() + ":order-1"})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := w.stack.Core.CreatePaymentIntent(ctx, &corev1.CreatePaymentIntentRequest{
		UserId: u.GetId(), OrderId: order.GetId(), Provider: "manual_card", IdempotencyKey: "int:" + order.GetId() + ":manual_card"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.stack.Core.SubmitPaymentProof(ctx, &corev1.SubmitPaymentProofRequest{
		UserId: u.GetId(), IntentId: intent.GetId(), ReceiptFile: receiptFile, ReferenceNumber: reference}); err != nil {
		t.Fatal(err)
	}
	return intent.GetId()
}

// A proof the Mini App sent reaches the staff as the bot's own does, with the
// Review button on that payment, and the card shows the receipt core stored
// (a Telegram file id the bot itself sent to the customer's chat): approving
// it delivers the service.
func TestMiniAppProofAlertsTheStaffAndOpensTheCard(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	buyer := w.person(801, "mimi")
	buyer.text("/start")
	buyer.press("lang:en")

	intent := miniProof(t, w, buyer, "miniapp-receipt-1", "998877")
	w.h.NotifyProofSubmitted(context.Background(), ProofNotice{TelegramID: 801, Username: "mimi", IntentID: intent, Amount: 150000, Currency: "IRT"})

	owner.sees("New payment to review from @mimi")
	owner.sees("150,000 Toman")
	owner.press("adm:pi:" + intent) // the alert's button opens that very payment
	item := owner.last()
	if !item.photo || item.fileID != "miniapp-receipt-1" || !strings.Contains(item.text, "998877") {
		t.Fatalf("review item: %+v", item)
	}
	owner.press("adm:ok:")
	owner.toast("Approved")
	buyer.eventuallySees("Payment confirmed")
	buyer.eventuallySees("Your service is ready")
}

// The customer who is staff is not alerted to their own payment, a name-less
// customer is shown by id, and the alert can be switched off.
func TestMiniAppProofAlertSkipsTheSenderAndFollowsTheSwitch(t *testing.T) {
	w := newWorld(t)
	owner := setupStore(t, w)
	ctx := context.Background()

	// The owner pays for something themselves.
	intent := miniProof(t, w, owner, "owner-receipt", "111")
	w.h.NotifyProofSubmitted(ctx, ProofNotice{TelegramID: ownerID, Username: "boss", IntentID: intent, Amount: 150000, Currency: "IRT"})
	if n := owner.count("New payment to review"); n != 0 {
		t.Fatalf("the owner was told of their own payment %d times", n)
	}

	// A customer without a username is shown by the Telegram id.
	anon := w.person(802, "")
	anon.text("/start")
	anon.press("lang:en")
	intent = miniProof(t, w, anon, "anon-receipt", "222")
	w.h.NotifyProofSubmitted(ctx, ProofNotice{TelegramID: 802, IntentID: intent, Amount: 150000, Currency: "IRT"})
	owner.sees("New payment to review from id 802")

	// notify.new_payment off: no alert (the payment still waits in the list).
	owner.text("/set notify.new_payment false")
	owner.sees("Saved")
	before := owner.count("New payment to review")
	w.h.NotifyProofSubmitted(ctx, ProofNotice{TelegramID: 802, IntentID: intent, Amount: 150000, Currency: "IRT"})
	if n := owner.count("New payment to review"); n != before {
		t.Fatalf("an alert was sent with the switch off: %d, was %d", n, before)
	}
}
