package gateway

import (
	"context"
	"time"
)

// Automatic TON (Toncoin) payments, like the USDT ones, have no payment to create and nothing to verify per
// payment: the customer sends the exact amount, with the payment's comment, to the store's address, and the
// payments service reads the address's transactions from an indexer (TON Center) and matches them itself. The
// indexer's answer is not trusted: every transaction is validated by the domain before it counts. The price of
// TON in USDT, which the amount is worked out from, comes from public exchanges' prices, checked against each
// other.

// TONQuery asks for the newest transactions of one address.
type TONQuery struct {
	Address string // the account, in any form the source takes
	Limit   int    // transactions per page (at most 100)
	// BeforeLT and BeforeHash continue an earlier page: the read starts at that transaction (included) and goes
	// back in time. BeforeHash is the hash exactly as the source reported it (RawHash). Both zero: the newest.
	BeforeLT   int64
	BeforeHash string
}

// TONOutMsg is a message a transaction sent.
type TONOutMsg struct {
	Destination string
	Value       string // nanoton, a decimal string as reported
}

// TONTx is one transaction of the address as the source reports it, unvalidated.
type TONTx struct {
	Hash    string // the transaction hash in lower-case hex (64 characters), as far as the source gave one
	RawHash string // the hash as the source reported it, for paging
	LT      int64  // the account's logical time of the transaction
	Time    time.Time
	// The incoming message. HasMsg false: none. Source "" is an external message, not a transfer.
	HasMsg      bool
	Source      string
	Destination string
	Value       string // nanoton, a decimal string as reported
	Comment     string // the text comment, "" when there is none
	OutMsgs     []TONOutMsg
}

// TONSource reads an address's transactions (TON Center). Errors never carry a secret (the API key, the
// proxy's URL).
type TONSource interface {
	// Transactions returns up to Limit transactions, newest first.
	Transactions(ctx context.Context, q TONQuery) ([]TONTx, error)
}

// TONQuote is a price of one TON.
type TONQuote struct {
	// PriceMicro is micro-USDT (scale 6) per 1 TON.
	PriceMicro int64
	// Source names where it came from (the exchange).
	Source string
	// At is when it was read.
	At time.Time
}

// TONPricer reads the TON price now. An error means no price is known: nothing is guessed.
type TONPricer interface {
	Quote(ctx context.Context) (TONQuote, error)
}
