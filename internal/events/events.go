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
	SubscriptionExtended    = "subscription.extended.v1"
	SubscriptionReminder    = "subscription.reminder.v1"
	// Support tickets: ids only (the outbox is kept for ever, bodies are not
	// put in it); the bot fetches a message with GetTicketMessage.
	TicketOpened          = "ticket.opened.v1"
	TicketCustomerReplied = "ticket.customer_replied.v1"
	TicketReplied         = "ticket.replied.v1"
	TicketClosed          = "ticket.closed.v1"
	// SystemAlert: core's watchdog found a problem, or one was fixed.
	SystemAlert = "system.alert.v1"
	// DepositUnmatched: USDT reached the store's TRON address but matched no
	// automatic payment (DepositUnmatchedEvent); staff credit it by hand.
	DepositUnmatched = "payment.deposit_unmatched.v1"
	// DepositClaimed: a manual USDT proof carries a transaction the watcher
	// had seen reach the store's address (DepositClaimedEvent); staff check
	// the proof's customer really sent it before approving.
	DepositClaimed = "payment.deposit_claimed.v1"
)

// Topics core consumes from the payments feed.
const (
	PaymentsPaymentSucceeded = "payment.succeeded.v1"
	PaymentsPaymentRejected  = "payment.rejected.v1"
	// PaymentsLegacyWalletCredited was emitted for top-ups next to
	// payment.succeeded; it is applied with the same idempotency key, so
	// whichever arrives first credits the wallet and the other is a no-op.
	PaymentsLegacyWalletCredited = "wallet.credited.v1"
	// PaymentsDepositUnmatched: the automatic USDT watcher recorded a deposit
	// it could not match (DepositUnmatchedEvent); core passes it on to the
	// bot as DepositUnmatched.
	PaymentsDepositUnmatched = "payment.deposit_unmatched.v1"
	// PaymentsDepositClaimed: a manual crypto proof took a deposit the
	// watcher had seen (DepositClaimedEvent); core passes it on to the bot as
	// DepositClaimed.
	PaymentsDepositClaimed = "payment.deposit_claimed.v1"
)

// Why a deposit is unmatched (DepositUnmatchedEvent.Reason).
const (
	// UnmatchedNoPayment: no automatic payment to the address has exactly
	// this amount (underpaid, overpaid, an exchange took its fee, or not a
	// customer).
	UnmatchedNoPayment = "no_payment"
	// UnmatchedAlreadyPaid: the payment with this amount is paid already (a
	// second transfer).
	UnmatchedAlreadyPaid = "already_paid"
	// UnmatchedClosed: the payment with this amount can no longer be paid
	// (over 48 hours old, or failed).
	UnmatchedClosed = "closed"
	// UnmatchedAmbiguous: more than one payment has this amount; nothing is
	// guessed.
	UnmatchedAmbiguous = "ambiguous"
	// UnmatchedAfterRestore: the payment with this amount was created just
	// after the database was restored from a backup; a customer whose
	// payment the restore erased may have been told the same amount, so
	// staff check who sent it.
	UnmatchedAfterRestore = "after_restore"
)

// DepositUnmatchedEvent: USDT (TRC20) reached the store's address and was
// credited to nobody. Staff find the customer (the amount, the sender, the
// time), credit their wallet by hand, then mark the deposit handled on the
// dashboard. It is announced once per deposit; dust (under 0.1 USDT) never.
// The bot sends it to the staff who get payment alerts (notify.recipients).
// Addresses and the transaction id are public chain data.
type DepositUnmatchedEvent struct {
	TxID       string `json:"txid"` // lower-case hex, no 0x
	EventIndex int    `json:"event_index"`
	Network    string `json:"network"` // TRC20
	From       string `json:"from"`
	To         string `json:"to"`
	Amount     int64  `json:"amount"`     // micro-USDT (scale 6)
	Currency   string `json:"currency"`   // USDT
	BlockTime  int64  `json:"block_time"` // unix seconds
	Reason     string `json:"reason"`     // Unmatched*
}

// DepositClaimedEvent: a manual crypto payment's proof (IntentID) names a
// USDT (TRC20) transfer the watcher had seen reach the store's address, and
// took it. The transaction id of a transfer to the store is public, so
// anyone may send it as their proof: staff check the proof's customer really
// sent it before approving. Nobody credits it by hand. The bot sends it to
// the staff who get payment alerts (notify.recipients), always.
type DepositClaimedEvent struct {
	TxID       string `json:"txid"` // lower-case hex, no 0x
	EventIndex int    `json:"event_index"`
	Network    string `json:"network"` // TRC20
	From       string `json:"from"`
	To         string `json:"to"`
	Amount     int64  `json:"amount"`     // micro-USDT (scale 6)
	Currency   string `json:"currency"`   // USDT
	BlockTime  int64  `json:"block_time"` // unix seconds
	Reason     string `json:"reason"`     // what it is without the proof: no_payment | dust
	IntentID   string `json:"intent_id"`  // the manual crypto payment whose proof carries it
	Proof      string `json:"proof"`      // the proof's state: waiting | approved
}

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
	PaidByWallet  = "wallet"
	PaidManually  = "manual"
	PaidByGateway = "gateway" // Zarinpal, Telegram Stars, automatic USDT
	PaidTrial     = "trial"
	PaidFree      = "free"
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
	// CreditDiscountUsed: a payment for an order arrived after its discount
	// code stopped allowing this use (the customer used it on another order,
	// or its uses ran out); the money stays in the wallet and the order
	// stays unpaid.
	CreditDiscountUsed = "discount_used"
	// CreditAdminAdjust: an admin added balance by hand.
	CreditAdminAdjust = "admin_adjust"
	// CreditReferral: a reward for an invited user's first purchase.
	CreditReferral = "referral"
	// CreditRefund: staff refunded an order to the wallet.
	CreditRefund = "refund"
	// CreditCommission: a reseller's commission on a purchase by a customer
	// who joined through their link (OrderID is the customer's order: never
	// show the customer to the reseller).
	CreditCommission = "commission"
	// CreditSettlement: staff recorded a payment the reseller made to the store.
	CreditSettlement = "settlement"
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

// PaymentRejectedEvent: a payment did not go through: a reviewer rejected a
// manual payment (Reason is what they wrote), or an automated gateway payment
// failed or expired (Reason is one of payments' gateway reasons). Provider says
// which kind (events from before it was added have none, and the sales reports
// tell those by their reason).
type PaymentRejectedEvent struct {
	UserID     string `json:"user_id"`
	TelegramID int64  `json:"telegram_id"`
	IntentID   string `json:"intent_id"`
	OrderID    string `json:"order_id,omitempty"`
	Provider   string `json:"provider,omitempty"`
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
	Label            string `json:"label,omitempty"` // the name a reseller gave the service
}

// ProvisionFailedEvent: provisioning keeps failing; an admin should look.
type ProvisionFailedEvent struct {
	OrderID    string `json:"order_id"`
	UserID     string `json:"user_id"`
	TelegramID int64  `json:"telegram_id"`
	Attempts   int    `json:"attempts"`
	Error      string `json:"error"`
}

// SubscriptionExtendedEvent: a renewal or traffic top-up was applied.
type SubscriptionExtendedEvent struct {
	SubscriptionID    string `json:"subscription_id"`
	OrderID           string `json:"order_id"`
	UserID            string `json:"user_id"`
	TelegramID        int64  `json:"telegram_id"`
	Type              string `json:"type"`                          // renew | traffic_topup
	ExpiresAt         int64  `json:"expires_at,omitempty"`          // unix seconds, 0 = no expiry
	TrafficTotalBytes int64  `json:"traffic_total_bytes,omitempty"` // 0 = unlimited
	TrafficUsedBytes  int64  `json:"traffic_used_bytes,omitempty"`
	Label             string `json:"label,omitempty"` // the name a reseller gave the service
}

// Kinds of subscription.reminder.v1.
const (
	ReminderExpiring   = "expiring"    // expires within days (see Days)
	ReminderLowTraffic = "low_traffic" // most of the traffic is used
	ReminderExpired    = "expired"
	ReminderDepleted   = "depleted" // all traffic used
)

// SubscriptionReminderEvent: a subscription is about to end, or ended.
type SubscriptionReminderEvent struct {
	SubscriptionID    string `json:"subscription_id"`
	UserID            string `json:"user_id"`
	TelegramID        int64  `json:"telegram_id"`
	Kind              string `json:"kind"`
	Days              int    `json:"days,omitempty"`                // expiring: whole days left
	ExpiresAt         int64  `json:"expires_at,omitempty"`          // unix seconds
	TrafficTotalBytes int64  `json:"traffic_total_bytes,omitempty"` // 0 = unlimited
	TrafficUsedBytes  int64  `json:"traffic_used_bytes,omitempty"`
	TopupAvailable    bool   `json:"topup_available,omitempty"` // traffic packages exist
	Label             string `json:"label,omitempty"`           // the name a reseller gave the service
}

// TicketOpenedEvent: a customer started a ticket (staff are alerted).
type TicketOpenedEvent struct {
	TicketID   string `json:"ticket_id"`
	Number     int64  `json:"number"`
	MessageID  string `json:"message_id"`
	UserID     string `json:"user_id"`
	TelegramID int64  `json:"telegram_id"`
	Category   string `json:"category"`
}

// TicketCustomerRepliedEvent: a customer wrote again after staff answered.
// AssigneeTelegramID is set when the ticket has an assignee, who alone is
// alerted.
type TicketCustomerRepliedEvent struct {
	TicketID           string `json:"ticket_id"`
	Number             int64  `json:"number"`
	MessageID          string `json:"message_id"`
	UserID             string `json:"user_id"`
	TelegramID         int64  `json:"telegram_id"`
	AssigneeTelegramID int64  `json:"assignee_telegram_id,omitempty"`
}

// TicketRepliedEvent: staff answered; the bot delivers the message and
// reports back (ReportTicketDelivery). Closed: the reply also closed it.
type TicketRepliedEvent struct {
	TicketID   string `json:"ticket_id"`
	Number     int64  `json:"number"`
	MessageID  string `json:"message_id"`
	TelegramID int64  `json:"telegram_id"`
	Closed     bool   `json:"closed,omitempty"`
}

// TicketClosedEvent: staff closed a ticket without a reply.
type TicketClosedEvent struct {
	TicketID   string `json:"ticket_id"`
	Number     int64  `json:"number"`
	TelegramID int64  `json:"telegram_id"`
}

// System alert states and severities.
const (
	AlertProblem  = "problem"
	AlertResolved = "resolved"

	SeverityCritical = "critical" // customers are affected now
	SeverityWarning  = "warning"  // needs a look soon
)

// SystemAlertEvent: one of core's watchdog checks went wrong (problem), was
// fixed (resolved: it has passed for 5 minutes), or is still wrong after 6
// hours and then once a day (problem, Repeat). The bot sends it to the staff
// who get alerts about failed deliveries (notify.recipients; never support
// staff).
//
// TitleKey is the bot's text (alert.*). Args are its values, all strings:
// count, days, hours and minutes are whole numbers, free is a size in
// bytes, date is unix seconds, and anything else (service, server) is a
// name. Detail is a few English words from the check (an error), or "".
type SystemAlertEvent struct {
	Key      string            `json:"key"` // the check: service.bot, server.<id>, backup.stale...
	Severity string            `json:"severity"`
	State    string            `json:"state"`
	TitleKey string            `json:"title_key"`
	Args     map[string]string `json:"args,omitempty"`
	Detail   string            `json:"detail,omitempty"`
	Since    int64             `json:"since"` // unix seconds: when the problem was first seen
	// At is this run of the watchdog in unix seconds; for resolved, the run
	// that first saw the check pass again (so At - Since is how long it
	// lasted).
	At     int64 `json:"at"`
	Repeat bool  `json:"repeat,omitempty"`
}
