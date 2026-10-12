package gateway

import (
	"context"
	"time"
)

// On-chain payments (USDT on TRON) have no payment to create and nothing to
// verify per payment: the customer sends an exact amount to the store's
// address, and the payments service reads the address's incoming transfers
// from an indexer (TronGrid) and matches them itself. The indexer's answer is
// not trusted: every transfer is validated by the domain before it counts.

// ChainQuery asks for the incoming transfers of one token to one address,
// oldest first, from Since (inclusive), confirmed ones only.
type ChainQuery struct {
	Address string    // the receiving address
	Token   string    // the token contract address
	Since   time.Time // block time, inclusive
	Next    string    // the indexer's cursor for the next page, "" = the first page
	Limit   int       // transfers per page (the indexer's maximum when 0)
}

// ChainTransfer is one token transfer as the indexer reports it, unvalidated.
type ChainTransfer struct {
	TxID    string // the transaction hash, as reported
	From    string
	To      string
	Token   string // the token contract address, as reported
	Type    string // the event, "Transfer" for a transfer
	Value   string // the amount in the token's smallest unit, a decimal string as reported
	BlockTS time.Time
}

// ChainPage is one page of transfers.
type ChainPage struct {
	Transfers []ChainTransfer
	Next      string // "" = the last page
}

// ChainSource reads an address's incoming token transfers (TronGrid). Errors
// never carry a secret (the API key, the proxy's URL).
type ChainSource interface {
	Transfers(ctx context.Context, q ChainQuery) (ChainPage, error)
}
