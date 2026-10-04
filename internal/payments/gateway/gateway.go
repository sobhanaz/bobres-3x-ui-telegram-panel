// Package gateway is the contract of the automated payment providers whose
// payments the payments service creates and checks itself (Zarinpal, Crypto
// Pay). Telegram Stars has no server-side call to make: the bot reports its
// payments and the domain checks them against the intent instead.
package gateway

import (
	"context"
	"errors"
	"time"
)

// State is a gateway's verdict on one payment.
type State int

const (
	// Pending: not paid yet, or the customer has not come back.
	Pending State = iota
	// Paid: the gateway confirms the payment.
	Paid
	// Failed: cancelled, expired or refused; the payment cannot succeed.
	Failed
)

func (s State) String() string {
	switch s {
	case Paid:
		return "paid"
	case Failed:
		return "failed"
	default:
		return "pending"
	}
}

// Charge is a payment as fixed when the intent was created.
type Charge struct {
	IntentID    string
	Amount      int64  // in the gateway's unit: Rial for Zarinpal, USDT cents for Crypto Pay
	Currency    string // "IRR", "USDT"
	Description string
	// CallbackURL is where the gateway returns the customer's browser (Zarinpal).
	CallbackURL string
	ExpiresAt   time.Time
}

// Started is what the gateway returned when the payment was created.
type Started struct {
	ExternalID string // Zarinpal authority, Crypto Pay invoice id
	PayURL     string // where the customer pays
}

// Result is the gateway's report on one payment.
type Result struct {
	State     State
	Amount    int64  // what the gateway says was paid, in the charge's unit (0 if unknown)
	Currency  string // the charge's currency as reported, "" if the gateway does not say
	Reference string // bank reference or transaction hash, for support
	Reason    string // short machine-readable reason when Failed
}

// Gateway creates payments and reports their state.
type Gateway interface {
	Name() string
	Create(ctx context.Context, c Charge) (Started, error)
	// Check reports the payment's state. For Zarinpal it is the verification
	// call that settles the transaction on Zarinpal's side, so callers only run
	// it for intents that are still open.
	Check(ctx context.Context, externalID string, c Charge) (Result, error)
}

// ErrNotConfigured means the provider has no credentials on this install.
var ErrNotConfigured = errors.New("gateway: not configured")
