package store

import "time"

// User mirrors core.users.
type User struct {
	ID         string
	TelegramID int64
	Username   string
	Language   string
	Role       string
	Status     string
	ReferredBy *string
	RefCode    *string // the user's invite code, created on first use
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// BotBlockedAt: when Telegram said the user blocked the bot (nil: reachable).
	BotBlockedAt *time.Time
	// AnnounceOptIn is the customer's answer to "Do you want news of new products?" (nil: no answer, which is no), and AnnounceAskedAt when
	// the bot asked them (nil: not yet; it asks once).
	AnnounceOptIn   *bool
	AnnounceAskedAt *time.Time
}

// Plan mirrors core.plans.
type Plan struct {
	ID           string
	NameI18n     map[string]string
	Kind         string
	DurationDays *int32
	TrafficBytes *int64
	Price        int64
	Currency     string
	Enabled      bool
	IsTrial      bool
	IsTopup      bool // a traffic package for an existing subscription
	Sort         int32
	// ServerIDs are the 3x-ui servers its new services may go to
	// (plan_servers; empty = any). Reads fill it; UpsertPlan leaves the
	// stored list alone when it is nil.
	ServerIDs []string
	// Inbounds are the panel inbounds its new services are attached to, per
	// server (plan_inbounds; a server without an entry: the provisioner's
	// default, every attachable inbound). Reads fill it, sorted by server
	// and inbound; UpsertPlan leaves the stored selection alone when it is
	// nil, and an empty non-nil list clears it.
	Inbounds []PlanInbound
	// GroupID is the plan group it is listed under (plan_groups; nil: none).
	// Reads fill it; UpsertPlan writes what it is given, so a caller that does
	// not know the group (the bot's plan editor) must read it first.
	GroupID *string
	// RewardPercent or RewardAmount (at most one; both nil: none): a paid purchase of this plan gives the buyer a discount code, of this
	// percent or of this amount in the plan's currency, for any plan, for RewardDays days (1 to 365), with RewardTextI18n for them to
	// read. Reads fill them; UpsertPlan writes what it is given, so a caller that does not know them reads first (or sets KeepReward).
	RewardPercent  *int32
	RewardAmount   *int64
	RewardDays     int32
	RewardTextI18n map[string]string
	// KeepReward is not stored: it asks the domain to keep the stored reward (the bot's plan editor and a plan switched on or off never
	// send one).
	KeepReward bool
	// DeletedAt is set on a plan that was deleted but is kept because orders point to it; reads leave such plans out of the lists.
	DeletedAt *time.Time
}

// PlanInbound is a plan's choice of inbounds on one server.
type PlanInbound struct {
	ServerID   string  `json:"server_id"`
	InboundIDs []int32 `json:"inbound_ids"`
}

// Order mirrors core.orders. Amount is what the customer pays, after
// DiscountAmount. A renewal or top-up names its SubscriptionID; its target
// limits are computed once (TargetsSet) and reused on every retry. A staff
// member's extension (CreatedBy) carries its own ExtendDays/ExtendBytes
// instead of its plan's.
type Order struct {
	ID                 string
	UserID             string
	PlanID             string
	Type               string
	Status             string
	Amount             int64
	Currency           string
	IdempotencyKey     string
	SubscriptionID     *string
	DiscountCode       *string
	DiscountAmount     int64
	TargetsSet         bool
	TargetExpiresAt    *time.Time
	TargetTrafficBytes *int64
	ExtendDays         *int32
	ExtendBytes        *int64
	CreatedBy          *string
	RefundedAt         *time.Time
	RefundAmount       *int64
	Label              *string // a reseller's name for the service it creates, or a bulk purchase's "<prefix> <n>"
	// BulkID and BulkIndex: the bulk purchase the order belongs to and its
	// place in it (1 to the quantity); both nil for every other order.
	BulkID    *string
	BulkIndex *int32
	// ListAmount, ListCurrency and Rate: an order listed in USDT and paid from the
	// wallet (which is in Toman) was charged Amount in Currency (Toman); these say
	// what the plan's price was (after any discount) and the Toman per USDT it was
	// worked out at. All three or none: an order paid in the currency it was listed in has none.
	ListAmount   *int64
	ListCurrency *string
	Rate         *int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Bulk mirrors core.bulk_orders: a purchase of Quantity services of one plan
// paid for together (the orders of it carry its id).
type Bulk struct {
	ID             string
	UserID         string
	PlanID         string
	Quantity       int32
	UnitAmount     int64
	Amount         int64 // UnitAmount x Quantity
	Currency       string
	LabelPrefix    *string
	IdempotencyKey string
	CreatedAt      time.Time
	// DeliveredAt: when core announced the one message with every link (nil
	// until then); a service that is ready after it is delivered on its own.
	DeliveredAt *time.Time
}

// Wallet mirrors core.wallets. A reseller's balance may go down to
// -CreditLimit; everyone else's limit is 0.
type Wallet struct {
	UserID      string
	Currency    string
	Balance     int64
	CreditLimit int64
	UpdatedAt   time.Time
}

// LedgerEntry mirrors core.ledger_entries.
type LedgerEntry struct {
	ID             string
	UserID         string
	Currency       string
	Amount         int64
	Kind           string
	RefType        *string
	RefID          *string
	IdempotencyKey string
	BalanceAfter   int64
	CreatedAt      time.Time
}

// Subscription mirrors core.subscriptions. ServerID is "" until provisioned.
type Subscription struct {
	ID           string
	UserID       string
	OrderID      string
	ServerID     string
	ClientEmail  string
	SubID        *string
	Status       string
	ExpiresAt    *time.Time
	TrafficTotal *int64
	TrafficUsed  int64
	LastSyncedAt *time.Time
	SubLink      string
	Label        *string // the name a reseller gave it
	// Note is the reseller's private note about the service (one line, at
	// most 200 characters); never shown to the customer.
	Note      *string
	CreatedAt time.Time
	// PageToken is the secret in the branded page's URL (/sub/<token>); it
	// grants the same power as SubLink and is rotated by RegeneratePageToken.
	PageToken string
	// When each reminder went out in the current period (nil = not yet).
	NotifiedExpiringAt   *time.Time
	NotifiedLowTrafficAt *time.Time
	NotifiedEndedAt      *time.Time
	// PanelMissingSince is when core first saw that the panel has no client for the service any more (an
	// operator deleted it there); nil while the panel has it. See UsageWorker.
	PanelMissingSince *time.Time
}

// Discount mirrors core.discount_codes: a percentage or a fixed amount.
type Discount struct {
	Code      string
	Percent   *int32
	Amount    *int64
	Currency  *string
	MaxUses   *int32
	Used      int32
	ExpiresAt *time.Time
	Enabled   bool
	// A reward code (a plan's reward for a purchase) is bound to the buyer and the plan, and remembers the purchase that gave it;
	// all nil for a code staff made.
	UserID        *string
	PlanID        *string
	SourceOrderID *string
}

// Ticket mirrors core.tickets.
type Ticket struct {
	ID             string
	Number         int64
	UserID         string
	Category       string
	Subject        string
	Status         string // open | answered | closed
	AssignedTo     *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastCustomerAt *time.Time
	LastStaffAt    *time.Time
	ClosedAt       *time.Time
	ClosedBy       *string
	CloseReason    string
}

// Audit mirrors core.audit_log.
type Audit struct {
	ID       string
	ActorID  *string
	Action   string
	Entity   string
	EntityID *string
	Before   []byte
	After    []byte
	Reason   *string
	IP       *string // the dashboard client's address (nil from the bot)
}
