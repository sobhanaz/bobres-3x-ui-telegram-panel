package domain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/core/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/eventbus"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Automatic USDT (TRC20), core's side. Payments owns the watcher, its
// settings and the transfer log; core prices a payment (gatewayCharge),
// forwards the owner's settings and audits them, lets staff mark an
// unmatched deposit handled (audited), and passes payments' "unmatched
// deposit" and "deposit claimed by a manual proof" on to the bot, which
// tells the staff.

// ChainTransfer is one USDT (TRC20) transfer payments' watcher saw reach the
// store's address. Addresses and the transaction id are public chain data.
type ChainTransfer struct {
	TxID       string // lower-case hex, no 0x
	EventIndex int    // the transfer's place in its transaction, from 0
	Network    string // TRC20
	From, To   string
	Amount     int64 // micro-USDT
	Currency   string
	BlockTime  int64 // unix seconds
	// Outcome: matched (it paid IntentID) | unmatched (staff credit it by
	// hand) | manual (IntentID, a manual crypto payment, carries this TXID) |
	// resolved (handled by staff) | ignored (dust under 0.1 USDT).
	Outcome    string
	Reason     string // unmatched: events.Unmatched*; ignored: dust
	IntentID   string
	RecordedAt int64
	ResolvedBy string // core user id
	ResolvedAt int64
	Note       string
}

// ChainTransferFilter narrows the transfer log; empty fields match all.
type ChainTransferFilter struct {
	Outcome, TxID, IntentID string
}

// Chain transfer outcomes.
var chainOutcomes = []string{"matched", "unmatched", "manual", "resolved", "ignored"}

// UnmatchedText says in a few English words why a deposit matched no
// payment (the bot and the dashboard have their own words per code).
func UnmatchedText(code string) string {
	switch code {
	case events.UnmatchedNoPayment:
		return "no open payment has exactly this amount"
	case events.UnmatchedAlreadyPaid:
		return "the payment with this amount was already paid"
	case events.UnmatchedClosed:
		return "the payment with this amount is closed (failed, or over 48 hours old)"
	case events.UnmatchedAmbiguous:
		return "more than one payment has this amount"
	case events.UnmatchedAfterRestore:
		return "the payment with this amount was created just after a database restore: check who sent it"
	case "dust":
		return "under 0.1 USDT (dust)"
	}
	return code
}

// TRON addresses: "T" and 33 base58 characters, whose last 4 bytes are a
// checksum. The USDT contract's own address is not a wallet.
var tronAddressRe = regexp.MustCompile(`^T[1-9A-HJ-NP-Za-km-z]{33}$`)

const (
	tronAlphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	usdtContract = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
)

// tronAPIKeyRe: TronGrid keys are UUIDs; payments accepts these characters
// (anything else could not go in a header safely).
var tronAPIKeyRe = regexp.MustCompile(`^[A-Za-z0-9-]{8,128}$`)

// pasteJunk is what a copy and paste may add around an address or a key:
// spaces and invisible direction marks.
var pasteJunk = strings.NewReplacer("\u200b", "", "\u200c", "", "\u200d", "", "\u200e", "", "\u200f", "",
	"\u202a", "", "\u202b", "", "\u202c", "", "\u202d", "", "\u202e", "",
	"\u2066", "", "\u2067", "", "\u2068", "", "\u2069", "", "\ufeff", "")

// normalizeTronAddress reads a typed address: either digit set, no spaces or
// direction marks. Base58 is case sensitive, so the case stays.
func normalizeTronAddress(v string) string {
	return strings.Join(strings.Fields(pasteJunk.Replace(latinDigits(v))), "")
}

// validTronAddress checks the shape and the base58check checksum: 0x41, the
// 20-byte account, then the first 4 bytes of sha256(sha256(those 21)).
func validTronAddress(a string) bool {
	if !tronAddressRe.MatchString(a) {
		return false
	}
	n := new(big.Int)
	for _, c := range a {
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(strings.IndexRune(tronAlphabet, c))))
	}
	b := n.Bytes() // "T" never decodes to a leading zero byte
	if len(b) != 25 || b[0] != 0x41 {
		return false
	}
	first := sha256.Sum256(b[:21])
	sum := sha256.Sum256(first[:])
	return string(sum[:4]) == string(b[21:])
}

// checkChainSecrets normalises automatic USDT's address and API key and
// checks them. The messages never repeat the key.
func checkChainSecrets(address, apiKey string, clearKey bool) (string, string, error) {
	address = normalizeTronAddress(address)
	apiKey = strings.TrimSpace(pasteJunk.Replace(apiKey))
	switch {
	case address != "" && !validTronAddress(address):
		return "", "", &FieldError{"address", "a TRON (TRC20) address: T and 33 more letters and digits, exactly as your wallet shows it"}
	case address == usdtContract:
		return "", "", &FieldError{"address", "that is the USDT contract's address, not a wallet's"}
	case apiKey != "" && clearKey:
		return "", "", &FieldError{"api_key", "set an API key or clear it, not both"}
	case apiKey != "" && !tronAPIKeyRe.MatchString(apiKey):
		return "", "", &FieldError{"api_key", "the API key from the TronGrid dashboard"}
	}
	return address, apiKey, nil
}

// txidRe: a TRON transaction id, as payments records it.
var txidRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// normalizeTXID reads a pasted transaction id: lower case, no 0x; "" when it
// is not one.
func normalizeTXID(v string) string {
	t := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(pasteJunk.Replace(v))), "0x")
	if !txidRe.MatchString(t) {
		return ""
	}
	return t
}

// ChainTransfers lists automatic USDT's transfer log for staff, newest
// first.
func (s *Service) ChainTransfers(ctx context.Context, ga GatewayAdmin, f ChainTransferFilter, page, size int) ([]ChainTransfer, int64, error) {
	f.Outcome = strings.ToLower(strings.TrimSpace(f.Outcome))
	if f.Outcome != "" && !slices.Contains(chainOutcomes, f.Outcome) {
		return nil, 0, &FieldError{"outcome", "one of " + strings.Join(chainOutcomes, ", ")}
	}
	if strings.TrimSpace(f.TxID) != "" {
		if f.TxID = normalizeTXID(f.TxID); f.TxID == "" {
			return nil, 0, &FieldError{"txid", "a transaction id: 64 hexadecimal digits"}
		}
	}
	return ga.ChainTransfers(ctx, f, page, size)
}

// maxDepositNote bounds the note staff leave on a handled deposit.
const maxDepositNote = 500

// ResolveDeposit marks an unmatched deposit handled (staff credited the
// customer by hand, or refunded the sender): it leaves the list, its
// transaction id can no longer be sent as a manual proof, and the audit
// keeps who did it, the amount and the note.
func (s *Service) ResolveDeposit(ctx context.Context, actor *store.User, ga GatewayAdmin, txid string, index int, note string) (*ChainTransfer, error) {
	key := normalizeTXID(txid)
	note = strings.TrimSpace(note)
	switch {
	case key == "":
		return nil, &FieldError{"txid", "a transaction id: 64 hexadecimal digits"}
	case index < 0 || index > 1_000_000:
		return nil, &FieldError{"event_index", "the transfer's place in its transaction, from 0"}
	case utf8.RuneCountInString(note) > maxDepositNote:
		return nil, &FieldError{"note", fmt.Sprintf("at most %d characters", maxDepositNote)}
	}
	t, err := ga.ResolveChainTransfer(ctx, key, index, actor.ID, note)
	if err != nil {
		return nil, err
	}
	after := map[string]any{"outcome": t.Outcome, "amount": t.Amount, "currency": t.Currency, "from": t.From,
		"to": t.To, "block_time": t.BlockTime}
	if err := s.auditChange(ctx, nil, actor, "gateway.deposit_resolve", "deposit", key+"#"+strconv.Itoa(index),
		map[string]any{"outcome": "unmatched"}, after, note); err != nil {
		return nil, err
	}
	return t, nil
}

// depositUnmatched passes payments' "unmatched deposit" on to the bot (the
// staff alert), exactly once: the inbox claim and the event commit together.
func (s *Service) depositUnmatched(ctx context.Context, m eventbus.Message) error {
	var e events.DepositUnmatchedEvent
	if err := json.Unmarshal(m.Payload, &e); err != nil {
		return eventbus.Permanent(fmt.Errorf("decode %s: %w", m.Topic, err))
	}
	if !txidRe.MatchString(e.TxID) || e.EventIndex < 0 || e.Amount <= 0 || e.Currency != "USDT" {
		return eventbus.Permanent(fmt.Errorf("%s: not a USDT deposit (txid %q, %d %q)", m.Topic, e.TxID, e.Amount, e.Currency))
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		claimed, err := s.st.ClaimInbox(ctx, tx, "payments:"+m.EventID)
		if err != nil || !claimed {
			return err
		}
		return s.publish(ctx, tx, events.DepositUnmatched, e)
	})
}

// depositClaimed passes payments' "a manual proof took a deposit the
// watcher saw" on to the bot (the staff alert), exactly once, as
// depositUnmatched does.
func (s *Service) depositClaimed(ctx context.Context, m eventbus.Message) error {
	var e events.DepositClaimedEvent
	if err := json.Unmarshal(m.Payload, &e); err != nil {
		return eventbus.Permanent(fmt.Errorf("decode %s: %w", m.Topic, err))
	}
	if !txidRe.MatchString(e.TxID) || e.EventIndex < 0 || e.Amount <= 0 || e.Currency != "USDT" {
		return eventbus.Permanent(fmt.Errorf("%s: not a USDT deposit (txid %q, %d %q)", m.Topic, e.TxID, e.Amount, e.Currency))
	}
	if id, err := buuid.Parse(e.IntentID); err != nil || id.String() != e.IntentID {
		return eventbus.Permanent(fmt.Errorf("%s: intent_id %q is not a payment id", m.Topic, e.IntentID))
	}
	return s.st.WithTx(ctx, func(tx pgx.Tx) error {
		claimed, err := s.st.ClaimInbox(ctx, tx, "payments:"+m.EventID)
		if err != nil || !claimed {
			return err
		}
		return s.publish(ctx, tx, events.DepositClaimed, e)
	})
}
