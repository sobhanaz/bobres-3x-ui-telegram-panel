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
	Sort         int32
}

// Order mirrors core.orders.
type Order struct {
	ID             string
	UserID         string
	PlanID         string
	Type           string
	Status         string
	Amount         int64
	Currency       string
	IdempotencyKey string
	CreatedAt      time.Time
	UpdatedAt      time.Time
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

// Subscription mirrors core.subscriptions.
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
}

// Staff mirrors core.staff.
type Staff struct {
	ID            string
	Username      string
	PasswordHash  string
	TOTPSecretEnc []byte
	Role          string
	Status        string
}
