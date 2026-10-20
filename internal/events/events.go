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
	// BulkReady: the services of a bulk purchase are delivered (or are the
	// provisioning machinery's to retry): the bot sends the customer every
	// link in one message (BulkReadyEvent).
	BulkReady = "order.bulk_ready.v1"
	// Support tickets: ids only (the outbox is kept for ever, bodies are not
	// put in it); the bot fetches a message with GetTicketMessage.
	TicketOpened          = "ticket.opened.v1"
	TicketCustomerReplied = "ticket.customer_replied.v1"
	TicketReplied         = "ticket.replied.v1"
	TicketClosed          = "ticket.closed.v1"
	// SystemAlert: core's watchdog found a problem, or one was fixed.
	SystemAlert = "system.alert.v1"
	// DailySummary: yesterday's sales for one staff member who may read the
	// reports (DailySummaryEvent).
	DailySummary = "report.daily_summary.v1"
	// SystemUpdate: an update of the install was asked for in the dashboard
	// (System > Updates > Update now), or one ended; one event for each owner
	// (SystemUpdateEvent), so an update nobody asked for is noticed.
	SystemUpdate = "system.update.v1"
	// SystemBackup: a backup was downloaded from the dashboard, or a restore of a backup was
	// asked for there (System > Backups), or one ended; one event for each owner
	// (SystemBackupEvent), so a copy of the store's data taken, or the data replaced, by
	// someone nobody expected is noticed.
	SystemBackup = "system.backup.v1"
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
	// Provider is who took the money for a payment (zarinpal, stars,
	// usdt_auto, manual_card, ...); empty for wallet, trial and free orders,
	// and for events published before it was recorded.
	Provider string `json:"provider,omitempty"`
	// BulkID, BulkIndex and BulkQuantity are set when the order is one of a
	// bulk purchase: the bulk it belongs to, its place in it (1 to the
	// quantity) and how many orders the bulk has. The bot announces such a
	// purchase to the staff once (with the first order), not once per order.
	BulkID       string `json:"bulk_id,omitempty"`
	BulkIndex    int    `json:"bulk_index,omitempty"`
	BulkQuantity int    `json:"bulk_quantity,omitempty"`
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
	// CreditGiftCode: the customer applied a gift code (docs/03-bot-ux.md). They asked for it in the bot, which
	// answered with the amount and the new balance in the same chat, so the notifier sends no second message.
	CreditGiftCode = "gift_code"
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
	// BulkID is set when the service belongs to a bulk purchase whose one
	// message with every link has not been announced yet (BulkReady): the
	// bot sends nothing for this service on its own. A service that is ready
	// after that message carries no BulkID and is delivered like any other.
	BulkID string `json:"bulk_id,omitempty"`
}

// ProvisionFailedEvent: provisioning keeps failing; an admin should look.
type ProvisionFailedEvent struct {
	OrderID    string `json:"order_id"`
	UserID     string `json:"user_id"`
	TelegramID int64  `json:"telegram_id"`
	Attempts   int    `json:"attempts"`
	Error      string `json:"error"`
	// BulkID is set when the order belongs to a bulk purchase: the staff are
	// told about it as for any order, and the customer is told by the bulk's
	// own message (BulkReady) instead of one "delayed" note per service.
	BulkID string `json:"bulk_id,omitempty"`
}

// BulkReadyEvent: every order of a bulk purchase is delivered or has failed
// enough times for the provisioning machinery to keep retrying it. The bot
// asks core for the services (GetBulkOrder) and sends the customer the links
// of the ones in ReadyOrders in one message (or a few, with a text file when
// there are many), and says which are still being prepared. Announced once
// per bulk; a service that becomes ready after it comes as an ordinary
// SubscriptionProvisionedEvent (so it is never in both).
type BulkReadyEvent struct {
	BulkID     string `json:"bulk_id"`
	UserID     string `json:"user_id"`
	TelegramID int64  `json:"telegram_id"`
	PlanID     string `json:"plan_id"`
	Quantity   int    `json:"quantity"`
	// ReadyOrders are the orders delivered when the bulk was announced, in
	// their order in the bulk; Delayed is how many of the others are still
	// being retried.
	ReadyOrders []string `json:"ready_orders"`
	Delayed     int      `json:"delayed"`
	Amount      int64    `json:"amount"` // what the whole bulk cost
	Currency    string   `json:"currency"`
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

// States of a SystemUpdateEvent.
const (
	// UpdateRequested: an owner asked for the update (the dashboard wrote its
	// request for the server's updater).
	UpdateRequested = "requested"
	// UpdateDone: the install runs the new version.
	UpdateDone = "done"
	// UpdateFailed: the update did not finish.
	UpdateFailed = "failed"
	// UpdateRolledBack: the new version did not come up healthy and the
	// install went back to the old one on its own.
	UpdateRolledBack = "rolled_back"
	// UpdateNotStarted: the updater never picked the request up; nothing changed.
	UpdateNotStarted = "not_started"
)

// SystemUpdateEvent: an owner asked for an update in the dashboard, or the
// update ended. Core writes one event for each owner who has a Telegram
// account, in the transaction that records the request (or its end), so a
// crash neither loses nor repeats the message. The bot sends it as it is to
// that owner (TelegramID), in their language; versions are release numbers
// core checked, and By is the dashboard user's name as core cut it. Message
// is the server updater's own short sentence for an end that is not done (it
// is text from outside core: the bot escapes it).
type SystemUpdateEvent struct {
	TelegramID int64  `json:"telegram_id"`
	Language   string `json:"language"` // fa or en: the owner's own
	State      string `json:"state"`
	From       string `json:"from"`
	To         string `json:"to"`
	Channel    string `json:"channel"` // stable or beta
	By         string `json:"by"`
	// At is when it was asked (requested) or ended, in unix seconds.
	At      int64  `json:"at"`
	Message string `json:"message,omitempty"`
}

// Kinds and states of a SystemBackupEvent. A download has the one state "downloaded"; a restore
// goes through the states of an update (requested, then done, failed, rolled_back or not_started:
// the Update* constants above).
const (
	BackupDownload = "download"
	BackupRestore  = "restore"

	BackupDownloaded = "downloaded"
)

// SystemBackupEvent: an owner downloaded a backup from the dashboard (Kind download), or asked
// for a restore of one there and the restore ended (Kind restore). Core writes one event for each
// owner who has a Telegram account, in the transaction that records it, so a crash neither loses
// nor repeats the message. The bot sends it as it is to that owner (TelegramID), in their language.
// File is the backup (its file name, or the name an uploaded one had) and Source where a restore's
// backup came from (backup or upload); By is the dashboard user's name as core cut it. Message
// is the updater's own short sentence for a restore that did not end well (outside text: the bot
// escapes it). Size is a download's size in bytes.
type SystemBackupEvent struct {
	TelegramID int64  `json:"telegram_id"`
	Language   string `json:"language"` // fa or en: the owner's own
	Kind       string `json:"kind"`     // download or restore
	State      string `json:"state"`
	File       string `json:"file"`
	Source     string `json:"source,omitempty"`
	By         string `json:"by"`
	// At is when it happened, in unix seconds.
	At      int64  `json:"at"`
	Size    int64  `json:"size,omitempty"`
	Message string `json:"message,omitempty"`
}

// DailySummaryFlow is how many things happened and the money they carried
// (amount in the currency's smallest unit).
type DailySummaryFlow struct {
	Count  int64 `json:"count"`
	Amount int64 `json:"amount"`
}

// DailySummaryCurrency is one currency's day. Amounts are integers in the
// currency's smallest unit (Toman 0 decimals, USDT 6) and are never added
// across currencies: each currency has its own entry.
type DailySummaryCurrency struct {
	Currency string `json:"currency"`
	Revenue  int64  `json:"revenue"`
	// PrevRevenue is the same currency's revenue the day before (0: none), for
	// the comparison line.
	PrevRevenue   int64            `json:"prev_revenue"`
	Orders        int64            `json:"orders"` // paid orders; the three below split them
	NewOrders     int64            `json:"new_orders"`
	Renewals      int64            `json:"renewals"`
	TrafficTopups int64            `json:"traffic_topups"`
	Refunds       DailySummaryFlow `json:"refunds"`
	Net           int64            `json:"net"` // revenue less refunds
	Topups        DailySummaryFlow `json:"topups"`
	// Rejected is payments staff rejected; GatewayFailed payments an automated
	// gateway failed or let expire. The bot shows them only when they are not 0.
	Rejected      DailySummaryFlow `json:"rejected"`
	GatewayFailed DailySummaryFlow `json:"gateway_failed"`
}

// DailySummaryEvent: yesterday's sales (one store-local day) for one staff
// member who holds reports.read and has a Telegram account. Core writes one
// event for each such member in the same transaction that claims the day, so a
// message that fails or is refused for one of them never holds up the others,
// and a member who lost the permission before the claim gets none. The bot
// sends it as it is: the figures are the sales report's for that day, with no
// names, no customers and no order or payment ids.
//
// Currencies lists only those with something to report that day (none: a day
// without sales, which the bot says in one line, unless people did something).
type DailySummaryEvent struct {
	TelegramID int64  `json:"telegram_id"`
	Language   string `json:"language"` // fa or en: the member's own
	Date       string `json:"date"`     // the day summed up, "2026-10-13"
	Zone       string `json:"zone"`     // the time zone Date was read in (IANA name)
	// NewCustomers (whose first paid order was that day) and Trials (free
	// trials started) count people, so they have no currency.
	NewCustomers int64                  `json:"new_customers"`
	Trials       int64                  `json:"trials"`
	Currencies   []DailySummaryCurrency `json:"currencies,omitempty"`
}
