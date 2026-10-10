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
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Wallet mirrors core.wallets.
type Wallet struct {
	UserID    string
	Currency  string
	Balance   int64
	UpdatedAt time.Time
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
	CreatedAt    time.Time
	// When each reminder went out in the current period (nil = not yet).
	NotifiedExpiringAt   *time.Time
	NotifiedLowTrafficAt *time.Time
	NotifiedEndedAt      *time.Time
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
}

// Ticket mirrors core.tickets.
type Ticket struct {
	ID        string
	UserID    string
	Category  string
	Text      string
	Status    string
	CreatedAt time.Time
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
