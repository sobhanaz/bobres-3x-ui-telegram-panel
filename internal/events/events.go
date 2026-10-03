// Package events defines the payloads of events exchanged between services:
// what payments publishes (consumed by core) and what core publishes
// (consumed by the bot). Producers and consumers share these types so field
// names cannot drift apart.
//
// Rules: topics are versioned past-tense facts; within a version, changes are
// additive only. Every user-facing event carries telegram_id so the bot can
// deliver it without another lookup.
package events

// Topics core publishes.
const (
	OrderPaid               = "order.paid.v1"
	WalletCredited          = "wallet.credited.v1"
	PaymentRejected         = "payment.rejected.v1"
	SubscriptionProvisioned = "subscription.provisioned.v1"
	ProvisionFailed         = "subscription.provision_failed.v1"
)

// Topics core consumes from the payments feed.
const (
	PaymentsPaymentSucceeded = "payment.succeeded.v1"
	PaymentsPaymentRejected  = "payment.rejected.v1"
	// PaymentsLegacyWalletCredited was emitted for top-ups next to
	// payment.succeeded; it is applied with the same idempotency key, so
	// whichever arrives first credits the wallet and the other is a no-op.
	PaymentsLegacyWalletCredited = "wallet.credited.v1"
)

// PaymentEvent is the payload of payments' payment.* events.
type PaymentEvent struct {
	IntentID string `json:"intent_id"`
	OrderID  string `json:"order_id,omitempty"` // empty for wallet top-ups
	UserID   string `json:"user_id"`
	Provider string `json:"provider"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Reason   string `json:"reason,omitempty"` // reviewer's note on rejection
}

// Sources of order.paid.v1.
const (
	PaidByWallet = "wallet"
	PaidManually = "manual"
	PaidTrial    = "trial"
	PaidFree     = "free"
)

// OrderPaidEvent: an order's money is in; provisioning follows.
type OrderPaidEvent struct {
	OrderID    string `json:"order_id"`
	UserID     string `json:"user_id"`
	TelegramID int64  `json:"telegram_id"`
	PlanID     string `json:"plan_id"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	Source     string `json:"source"`
}

// Reasons on wallet.credited.v1.
const (
	CreditTopup = "topup"
	// CreditOrderNotPayable: a manual payment for an order arrived after the
	// order was paid another way (or cancelled); the money stays in the wallet.
	CreditOrderNotPayable = "order_not_payable" //nolint:gosec // an event reason, not a credential
	// CreditAdminAdjust: an admin added balance by hand.
	CreditAdminAdjust = "admin_adjust"
)

// WalletCreditedEvent: money was added to a user's wallet.
type WalletCreditedEvent struct {
	UserID     string `json:"user_id"`
	TelegramID int64  `json:"telegram_id"`
	IntentID   string `json:"intent_id"`
	OrderID    string `json:"order_id,omitempty"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	Balance    int64  `json:"balance"`
	Reason     string `json:"reason"`
}

// PaymentRejectedEvent: a reviewer rejected a manual payment.
type PaymentRejectedEvent struct {
	UserID     string `json:"user_id"`
	TelegramID int64  `json:"telegram_id"`
	IntentID   string `json:"intent_id"`
	OrderID    string `json:"order_id,omitempty"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	Reason     string `json:"reason,omitempty"`
}

// SubscriptionProvisionedEvent: the VPN account exists and can be delivered.
type SubscriptionProvisionedEvent struct {
	SubscriptionID   string `json:"subscription_id"`
	OrderID          string `json:"order_id"`
	UserID           string `json:"user_id"`
	TelegramID       int64  `json:"telegram_id"`
	SubscriptionLink string `json:"subscription_link"`
	ExpiresAt        int64  `json:"expires_at,omitempty"` // unix seconds, 0 = no expiry
	TrafficBytes     int64  `json:"traffic_bytes,omitempty"`
}

// ProvisionFailedEvent: provisioning keeps failing; an admin should look.
type ProvisionFailedEvent struct {
	OrderID    string `json:"order_id"`
	UserID     string `json:"user_id"`
	TelegramID int64  `json:"telegram_id"`
	Attempts   int    `json:"attempts"`
	Error      string `json:"error"`
}
