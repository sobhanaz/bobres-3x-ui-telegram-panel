package handler

import (
	"context"

	corev1 "github.com/sobhanaz/bobres-3x-ui-telegram-panel/gen/proto/core/v1"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/bot/i18n"
)

// ProofNotice is the proof of a manual payment (a receipt or a transaction
// id) that a customer sent through the Mini App store instead of the bot's
// chat, and that core has recorded.
type ProofNotice struct {
	// TelegramID and Username are the customer's.
	TelegramID int64
	Username   string
	// IntentID is the payment, Amount and Currency what it is for.
	IntentID string
	Amount   int64
	Currency string
}

// NotifyProofSubmitted alerts the staff to a proof that came through the Mini
// App: the same alert, with the same Review button, that submitProof sends
// when the proof comes in the bot's own chat (a customer who is staff is not
// told about their own payment, and notify.new_payment switches it off). The
// alert is the bot's, because the staff are reached through its chats; core
// records the proof and then asks for this (POST /internal/miniapp/proof-alert).
func (h *Handler) NotifyProofSubmitted(ctx context.Context, n ProofNotice) {
	who := displayName(&corev1.User{Username: n.Username, TelegramId: n.TelegramID})
	h.notifyStaffAbout(ctx, n.TelegramID, "admin.new_payment", n.IntentID, "user", esc(who),
		"amount", func(lang string) string { return i18n.Money(lang, n.Amount, n.Currency) })
}
