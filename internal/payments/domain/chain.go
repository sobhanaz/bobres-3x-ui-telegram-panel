package domain

// Automatic USDT (TRC20) payments. The customer is asked for an exact amount
// in micro-USDT, unique among the payments to the store's address that a
// transfer could still settle, and sends it to the address. The watcher reads
// the address's confirmed incoming USDT transfers from TronGrid every 30
// seconds, records every transfer once (chain_transfers), and settles the
// payment with exactly that amount through the same compare-and-set as the
// other gateways (payment.succeeded.v1, ledger key gateway:<id>). Anything
// else (another amount, a payment already settled, an unknown sender) is an
// unmatched deposit: recorded, reported and announced to staff, who credit it
// by hand. A transaction hash pays once, across this path and manual crypto
// proofs.
//
// The store's address may take manual USDT too. A deposit no automatic
// payment asked for may be a manual customer's, who sends the proof a few
// minutes later, so it is announced only if no proof claimed it within
// unmatchedHold; a claim is announced on its own topic instead, so staff
// see which manual payment took which deposit. A transfer carrying an
// automatic payment's unique amount belongs to the automatic path: no
// manual proof can take it, and one approved before the watcher saw it is
// announced at once.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway/trongrid"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Timing and limits of automatic USDT payments.
const (
	// chainPayWindow: how long the amount may be paid (the USDT rate is
	// locked for that long). A later exact payment still settles, within
	// reopenWindow of the payment's creation.
	chainPayWindow = 45 * time.Minute
	// chainExpiryGrace: an open payment expires only once a read that started
	// this long after its window has gone through (a transfer is confirmed
	// about a minute after its block).
	chainExpiryGrace = 5 * time.Minute
	// chainSkew: how far block times and our clock may differ. Every read
	// starts this long before the newest block already read.
	chainSkew = 2 * time.Minute
	// chainAmountWindow: amounts are unique among the payments to an address
	// created this recently, longer than any transfer can match one.
	chainAmountWindow = reopenWindow + time.Hour
	// maxChainOffset: the most micro-USDT added to make an amount unique
	// (under a cent, so a whole-cent price stays within that cent). Offsets
	// are held for chainAmountWindow, paid or not, so the space must outlast
	// a busy price's unpaid payments.
	maxChainOffset = 9_999
	// chainUserUnpaid: unpaid automatic payments one customer may hold within
	// chainAmountWindow (each holds a unique amount that long), so nobody can
	// use up a price's amounts.
	chainUserUnpaid = 10
	// chainReuseLeft: a customer asking again for a payment still open is
	// shown that payment again while at least this much of its window is left.
	chainReuseLeft = 15 * time.Minute
	// chainUnmatchedHold: how long an unmatched deposit a manual proof may
	// claim waits before staff are told to credit it by hand.
	chainUnmatchedHold = 20 * time.Minute
	// chainAnnounceBatch: due announcements handled per pass.
	chainAnnounceBatch = 100
	// chainFuture: a block time further ahead of our clock is not believed.
	chainFuture = time.Hour
	// maxChainAmount: 1,000,000 USDT.
	maxChainAmount = 1_000_000_000_000
	// chainPages: pages per read of an address (200 transfers each).
	chainPages = 10
	// chainCheckGap: the bot's check button reads an address at most this
	// often, across every customer and replica.
	chainCheckGap = 10 * time.Second
	// chainReadTimeout bounds one read of an address.
	chainReadTimeout = time.Minute
	// chainDust: an unmatched transfer below 0.1 USDT is recorded but nobody
	// is told (address poisoning sends such dust to every merchant).
	chainDust = 100_000
	// chainNetwork is the only network watched.
	chainNetwork = "TRC20"
	// chainRestoreHold: after a restore of the database, a transfer made
	// within this long of it that matches a payment created since is not
	// settled but left to staff (UnmatchedAfterRestore): a customer whose
	// payment the restore erased may still send the amount they were told,
	// within their 45 minutes, and a new payment may have been given it.
	chainRestoreHold = chainPayWindow + chainSkew
)

// Why a transfer is unmatched or ignored (chain_transfers.reason).
const (
	// UnmatchedNoPayment: no payment to the address has exactly this amount
	// (underpaid, overpaid, an exchange took its fee, or not a customer).
	UnmatchedNoPayment = "no_payment"
	// UnmatchedAlreadyPaid: the payment with this amount is settled already
	// (a second transfer).
	UnmatchedAlreadyPaid = "already_paid"
	// UnmatchedClosed: the payment with this amount can no longer be settled.
	UnmatchedClosed = "closed"
	// UnmatchedAmbiguous: more than one payment has this amount (never
	// expected: amounts are unique); nothing is guessed.
	UnmatchedAmbiguous = "ambiguous"
	// UnmatchedShadowed: an automatic payment asked for exactly this amount,
	// but staff had already approved a manual proof carrying this transaction
	// before the watcher saw it: the automatic payer was not credited for it
	// (the transfer is credited once, through that proof).
	UnmatchedShadowed = "shadowed_exact_match"
	// UnmatchedAfterRestore: the payment with exactly this amount was
	// created after the database was restored from a backup, and the
	// transfer came within chainRestoreHold of the restore: it may be the
	// money of a customer whose payment the restore erased and who was told
	// the same amount, so nothing is settled and staff check who sent it.
	UnmatchedAfterRestore = "after_restore"
	// IgnoredDust: below chainDust.
	IgnoredDust = "dust"
)

// TopicDepositUnmatched is published on the payments feed for every
// unmatched deposit, in the transaction that records it, so core can tell
// staff (admin.unmatched_deposit) exactly once. Older cores ignore it.
const TopicDepositUnmatched = "payment.deposit_unmatched.v1"

// DepositUnmatchedEvent is the payload of payment.deposit_unmatched.v1.
type DepositUnmatchedEvent struct {
	TxID       string `json:"txid"`
	EventIndex int    `json:"event_index"`
	Network    string `json:"network"` // TRC20
	From       string `json:"from"`
	To         string `json:"to"`
	Amount     int64  `json:"amount"`   // micro-USDT (scale 6)
	Currency   string `json:"currency"` // USDT
	BlockTime  int64  `json:"block_time"`
	Reason     string `json:"reason"` // no_payment | already_paid | closed | ambiguous | shadowed_exact_match | after_restore
}

// TopicDepositClaimed is published on the payments feed when a manual crypto
// proof takes a deposit the watcher saw (its TXID), in the transaction that
// ties them, so staff see the deposit next to the proof they review: the
// TXID of a transfer to the store is public, and anyone may send it. Nobody
// should credit it by hand. Older cores ignore it.
const TopicDepositClaimed = "payment.deposit_claimed.v1"

// DepositClaimedEvent is the payload of payment.deposit_claimed.v1.
type DepositClaimedEvent struct {
	TxID       string `json:"txid"`
	EventIndex int    `json:"event_index"`
	Network    string `json:"network"` // TRC20
	From       string `json:"from"`
	To         string `json:"to"`
	Amount     int64  `json:"amount"`   // micro-USDT (scale 6)
	Currency   string `json:"currency"` // USDT
	BlockTime  int64  `json:"block_time"`
	Reason     string `json:"reason"`    // what it is without the proof: no_payment | dust
	IntentID   string `json:"intent_id"` // the manual crypto payment whose proof carries it
	Proof      string `json:"proof"`     // the proof's state: waiting | approved
}

var (
	// ErrTXIDUsed: the transaction already paid another payment
	// (automatically, or by staff from an unmatched deposit).
	ErrTXIDUsed = errors.New("payments: this transaction already paid another payment")
	// ErrTXIDAutomatic: the transaction carries an automatic payment's unique
	// amount, so it is not a manual proof.
	ErrTXIDAutomatic = fmt.Errorf("%w: this transaction carries an automatic USDT payment's exact amount: "+
		"it is handled as an automatic deposit, not as a manual proof", ErrPrecondition)
	// ErrTXIDAnnounced: the transaction is an unmatched deposit staff were
	// already told to credit by hand, so it is no longer a manual proof.
	ErrTXIDAnnounced = fmt.Errorf("%w: this transaction reached the store as an unmatched deposit, and staff "+
		"were told to credit it by hand: ask support", ErrPrecondition)
	// ErrNoUniqueAmount: every unique amount for this price is held.
	ErrNoUniqueAmount = fmt.Errorf("%w: automatic USDT: every unique amount for this price is in use; "+
		"use another payment method or try again later", ErrPrecondition)
	// ErrTooManyUnpaid: the customer holds chainUserUnpaid unpaid automatic
	// USDT payments.
	ErrTooManyUnpaid = fmt.Errorf("%w: automatic USDT: too many unpaid automatic USDT payments in the last 2 days; "+
		"pay one of them or use another payment method", ErrPrecondition)
)

// errLateBlocks: a read refused transfers whose block time is ahead of our
// clock; it does not read past them until the clock agrees.
var errLateBlocks = errors.New("trongrid: some transfers have a block time more than an hour ahead of this server's clock; " +
	"they are not recorded until it agrees (check the server's time)")

var txidRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// txidKey is the form a transaction hash is recorded and compared in: lower
// case without 0x (a TRON hash has none; customers paste it either way).
func txidKey(txid string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(txid)), "0x")
}

// chainSettleable: a closed on-chain payment a transfer may still settle
// (the transfer's time is checked by the caller).
func chainSettleable(in *store.Intent) bool {
	switch in.Status {
	case "pending", "confirming":
		return true
	case "failed", "expired":
		return in.FailureReason == nil || (*in.FailureReason != ReasonAmountMismatch && *in.FailureReason != "reversed")
	}
	return false
}

// StartChain creates an automatic USDT payment (idempotent per key): the
// amount core computed (whole cents of USDT, or a USDT price) plus a unique
// offset of 1 to 9,999 micro-USDT, the address to pay and a 45-minute window.
// A retry with the same key returns the payment already created, and so does
// the same customer asking again for the same payment (order or top-up,
// amount) under a new key while it is open. Every unique amount is held for
// chainAmountWindow, paid or not, so a customer may hold at most
// chainUserUnpaid unpaid ones (ErrTooManyUnpaid); ErrNoUniqueAmount when a
// price has none left.
func (s *Service) StartChain(ctx context.Context, in *store.Intent) (*store.Intent, error) {
	// Held until the payment is stored: an address change waits for it, then
	// sees it as open and is refused.
	s.chainMu.RLock()
	defer s.chainMu.RUnlock()
	cs := s.chain.Load()
	if !cs.offered() {
		return nil, invalid("payment method %q is not enabled", USDTAuto)
	}
	in.Provider = USDTAuto
	if in.GatewayAmount == nil || *in.GatewayAmount <= 0 || in.GatewayCurrency == nil || *in.GatewayCurrency != "USDT" {
		return nil, invalid("%s needs a positive amount in USDT", USDTAuto)
	}
	if *in.GatewayAmount > maxChainAmount {
		return nil, invalid("%s takes at most 1,000,000 USDT per payment", USDTAuto)
	}
	if err := validIntent(in); err != nil {
		return nil, err
	}
	base, addr, usdt := *in.GatewayAmount, cs.settings.Address, "USDT"
	exp := time.Now().Add(chainPayWindow)
	in.ExpiresAt, in.PayAddress, in.GatewayCurrency, in.Status = &exp, &addr, &usdt, "pending"
	var out *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		got, err := s.st.ExistingIntent(ctx, tx, in)
		if err != nil || got != nil {
			out = got
			return err
		}
		// What follows is serialised with every other automatic payment's start.
		if err := s.st.LockChainAmounts(ctx, tx); err != nil {
			return err
		}
		// Before any amount is given out after a restore, the restore is on
		// record, so the watcher knows which payments came after it.
		if err := s.NoteRestore(ctx, tx); err != nil {
			return err
		}
		// The customer asks again for a payment still open (a new menu, a new
		// button): it is shown again with its amount, rather than holding
		// another one.
		if got, err = s.st.ReusableChainIntent(ctx, tx, in, addr, base, maxChainOffset, chainReuseLeft); err != nil || got != nil {
			out = got
			return err
		}
		unpaid, err := s.st.UnpaidChainIntents(ctx, tx, in.UserID, chainAmountWindow)
		if err != nil {
			return err
		}
		if unpaid >= chainUserUnpaid {
			s.log.Warn("too many unpaid USDT payments", "user", in.UserID, "unpaid", unpaid)
			return ErrTooManyUnpaid
		}
		amount, err := s.st.AllocateChainAmount(ctx, tx, addr, base, maxChainOffset, chainAmountWindow)
		if errors.Is(err, store.ErrNoFreeAmount) {
			s.log.Warn("no unique USDT amount left", "base", base)
			return ErrNoUniqueAmount
		}
		if err != nil {
			return err
		}
		in.GatewayAmount = &amount
		if out, err = s.st.CreateIntent(ctx, tx, in); err != nil {
			return err
		}
		if out.ID != in.ID {
			return nil // created concurrently with the same key
		}
		if err := s.st.EnsureChainCursor(ctx, tx, addr, time.Now().Add(-chainSkew)); err != nil {
			return err
		}
		return s.st.RecordGatewayEvent(ctx, tx, out.ID, USDTAuto, "create", "pending", map[string]any{"amount": amount})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// NoteRestore records a restore of the database from a backup, if there was
// one since payments last looked (store.NoteRestore), in tx (nil: on its
// own). Payments calls it when it starts, so the guard's row is written
// before the first backup that follows, and StartChain again before it gives
// out an amount.
func (s *Service) NoteRestore(ctx context.Context, tx pgx.Tx) error {
	_, noticed, err := s.st.NoteRestore(ctx, tx)
	if noticed {
		s.log.Warn("the database was restored from a backup: for the next 47 minutes, automatic USDT transfers " +
			"that match a payment created since are left to staff (unmatched deposits, reason after_restore)")
	}
	return err
}

// RunChainWatcher reads the chain every interval until ctx ends. It runs
// even with nothing to read: automatic USDT may be switched on from the
// dashboard later.
func (s *Service) RunChainWatcher(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := s.ScanChainOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("chain watcher", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// ScanChainOnce reads every address that needs it: the store's address while
// automatic USDT is on, and any address a payment may still be settled at
// (open, or closed but within the late-payment window). Then it announces
// the unmatched deposits whose hold ended. It returns how many new transfers
// it recorded.
func (s *Service) ScanChainOnce(ctx context.Context) (int, error) {
	n, err := s.scanChain(ctx)
	if _, aerr := s.announceHeld(ctx); aerr != nil && err == nil && ctx.Err() == nil {
		err = aerr
	}
	return n, err
}

// SetUnmatchedHold sets how long an unmatched deposit a manual proof may
// claim waits before it is announced (chainUnmatchedHold by default; tests
// and installs without manual USDT may shorten it).
func (s *Service) SetUnmatchedHold(d time.Duration) { s.hold.Store(int64(max(d, 0))) }

func (s *Service) scanChain(ctx context.Context) (int, error) {
	cs := s.chain.Load()
	if !cs.readable() {
		return 0, nil
	}
	addrs, err := s.st.ChainScanAddresses(ctx, reopenWindow+chainSkew)
	if err != nil {
		return 0, err
	}
	if a := cs.settings.Address; cs.enabled && a != "" && !slices.Contains(addrs, a) {
		addrs = append(addrs, a)
	}
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	total := 0
	for _, a := range addrs {
		if ctx.Err() != nil {
			break
		}
		n, _ := s.scanAddress(ctx, cs.client, a) // recorded on the cursor and logged
		total += n
	}
	return total, nil
}

// checkChain is the customer's "check payment" for an automatic USDT
// payment: it reads the payment's address now, at most every chainCheckGap
// (a read already running will settle it), and returns the payment as it is
// then.
func (s *Service) checkChain(ctx context.Context, in *store.Intent) (*store.Intent, error) {
	if in.Status == "succeeded" || in.PayAddress == nil || !chainSettleable(in) ||
		time.Since(in.CreatedAt) > reopenWindow+chainSkew {
		return in, nil
	}
	cs := s.chain.Load()
	if !cs.readable() {
		return in, nil
	}
	ok, err := s.st.TryChainScan(ctx, *in.PayAddress, chainCheckGap)
	if err != nil {
		return nil, err
	}
	if !ok || !s.scanMu.TryLock() {
		return in, nil
	}
	defer s.scanMu.Unlock()
	_, _ = s.scanAddress(ctx, cs.client, *in.PayAddress) // recorded on the cursor and logged
	return s.st.GetIntent(ctx, nil, in.ID)
}

// chainTransfer is a transfer that passed validation.
type chainTransfer struct {
	txid    string
	index   int
	from    string
	to      string
	value   int64
	blockTS time.Time
}

var digitsRe = regexp.MustCompile(`^[0-9]{1,18}$`)

// scanAddress reads an address's new transfers and records them. The cursor
// only moves past transfers that were recorded, and never past the read's
// own start. A read whose later page fails keeps what the pages before it
// gave, as a read cut short at its page limit does. Only a complete read
// expires the address's payments whose window ended. The caller holds scanMu.
func (s *Service) scanAddress(ctx context.Context, client gateway.ChainSource, addr string) (int, error) {
	start := time.Now()
	cur, err := s.st.GetChainCursor(ctx, addr)
	if errors.Is(err, store.ErrNotFound) {
		from := start.Add(-chainSkew)
		if err := s.st.EnsureChainCursor(ctx, nil, addr, from); err != nil {
			return 0, err
		}
		cur, err = &store.ChainCursor{Address: addr, LastTS: from}, nil
	}
	if err != nil {
		return 0, err
	}
	// Every read starts chainSkew before the newest block read, except after
	// a read cut short (its page limit, or a page that failed): then it starts
	// at that block, so a busy overlap can never keep the reads from moving on.
	since := cur.LastTS.Add(-chainSkew)
	if s.cutShort[addr] {
		since = cur.LastTS
	}
	rctx, cancel := context.WithTimeout(ctx, chainReadTimeout)
	defer cancel()
	var (
		got      []gateway.ChainTransfer
		next     string
		complete bool
		readErr  error // a page after the first failed
	)
	for i := range chainPages {
		page, err := client.Transfers(rctx, gateway.ChainQuery{Address: addr, Token: trongrid.USDTContract,
			Since: since, Next: next, Limit: trongrid.MaxLimit})
		if err != nil {
			if i == 0 {
				s.scanDone(ctx, addr, err)
				return 0, err
			}
			// The pages before it are good: record them and move on from there,
			// so a read that always fails at the same page cannot stall.
			readErr = err
			break
		}
		got = append(got, page.Transfers...)
		if page.Next == "" || len(page.Transfers) == 0 {
			complete = true
			break
		}
		if page.Next == next {
			break // the indexer repeats itself: read the rest next time
		}
		next = page.Next
	}
	s.cutShort[addr] = !complete
	valid, through, late := s.validTransfers(addr, got, complete, start)
	recorded := 0
	for _, t := range valid {
		ok, err := s.recordTransfer(ctx, t)
		if err != nil {
			// Read it again next time: the next read overlaps the cursor, which
			// is at most this transfer's block.
			s.cutShort[addr] = false
			if aerr := s.st.AdvanceChainCursor(ctx, addr, earliest(t.blockTS, start)); aerr != nil {
				err = errors.Join(err, aerr)
			}
			s.scanDone(ctx, addr, fmt.Errorf("record transfer %s: %w", t.txid, err))
			return recorded, err
		}
		if ok {
			recorded++
		}
	}
	if !through.IsZero() {
		if err := s.st.AdvanceChainCursor(ctx, addr, through); err != nil {
			s.scanDone(ctx, addr, err)
			return recorded, err
		}
	}
	if complete {
		if err := s.expireChain(ctx, addr, start.Add(-chainExpiryGrace)); err != nil {
			s.scanDone(ctx, addr, err)
			return recorded, err
		}
	}
	switch {
	case readErr != nil:
		s.scanDone(ctx, addr, readErr)
		return recorded, readErr
	case late > 0:
		s.scanDone(ctx, addr, errLateBlocks)
		return recorded, errLateBlocks
	}
	s.scanDone(ctx, addr, nil)
	return recorded, nil
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// validTransfers keeps the transfers that are USDT sent to addr, numbers
// each transaction's transfers (TronGrid gives no log index: they are
// ordered by sender and amount, over the transaction's complete set) and
// returns them oldest first, with the newest block time the read covered
// (never after now) and how many it refused for their block time. Nothing an
// indexer says is trusted: the token is USDT only by its contract, never its
// name; an item repeated within a read (a paging slip) counts once, so it
// cannot become a second payment; and a block time before 1970 or over
// chainFuture ahead of our clock moves nothing, so neither a bogus one nor a
// clock that is behind can skip transfers for good.
func (s *Service) validTransfers(addr string, got []gateway.ChainTransfer, complete bool, now time.Time) ([]chainTransfer, time.Time, int) {
	believable := func(ts time.Time) bool { return ts.Unix() > 0 && !ts.After(now.Add(chainFuture)) }
	newestOf := func(list []gateway.ChainTransfer) time.Time {
		var newest time.Time
		for _, t := range list {
			if believable(t.BlockTS) && t.BlockTS.After(newest) {
				newest = t.BlockTS
			}
		}
		return newest
	}
	newest := newestOf(got)
	if !complete && !newest.IsZero() {
		// The newest block may go on in the next page: leave it to the next
		// read (it starts at it), unless the whole read is that one block.
		keep := got[:0:0]
		for _, t := range got {
			if t.BlockTS.Before(newest) {
				keep = append(keep, t)
			}
		}
		if n := newestOf(keep); !n.IsZero() {
			got, newest = keep, n
		}
	}
	var (
		out     []chainTransfer
		late    int
		refused = map[string]int{}
	)
	for _, t := range got {
		key := strings.ToLower(t.TxID)
		value, verr := strconv.ParseInt(t.Value, 10, 64)
		switch {
		case t.Token != trongrid.USDTContract:
			refused["not USDT"]++
		case t.To != addr:
			refused["another receiver"]++
		case t.Type != "Transfer":
			refused["not a transfer"]++
		case !txidRe.MatchString(key):
			refused["bad transaction id"]++
		case !digitsRe.MatchString(t.Value) || verr != nil:
			refused["bad amount"]++
		case value == 0:
			// Zero-value transfers are address poisoning: not money.
		case !believable(t.BlockTS):
			late++ // reported on the cursor (scanDone), once, not every read
		case t.From == "" || len(t.From) > 64:
			refused["bad sender"]++
		default:
			out = append(out, chainTransfer{txid: key, from: t.From, to: t.To, value: value, blockTS: t.BlockTS})
		}
	}
	if len(refused) > 0 {
		s.log.Warn("chain transfers refused", "address", addr, "why", refused)
	}
	slices.SortStableFunc(out, func(a, b chainTransfer) int {
		return cmp.Or(strings.Compare(a.txid, b.txid), strings.Compare(a.from, b.from), cmp.Compare(a.value, b.value),
			a.blockTS.Compare(b.blockTS))
	})
	out = slices.CompactFunc(out, func(a, b chainTransfer) bool {
		return a.txid == b.txid && a.from == b.from && a.value == b.value && a.blockTS.Equal(b.blockTS)
	})
	for i := range out {
		if i > 0 && out[i].txid == out[i-1].txid {
			out[i].index = out[i-1].index + 1
		}
	}
	slices.SortStableFunc(out, func(a, b chainTransfer) int {
		return cmp.Or(a.blockTS.Compare(b.blockTS), strings.Compare(a.txid, b.txid), cmp.Compare(a.index, b.index))
	})
	// A block time ahead of our clock (by less than chainFuture) is read
	// again next time rather than moving the cursor past our own present.
	if newest.After(now) {
		newest = now
	}
	return out, newest, late
}

// errRecorded: the transfer was recorded by another read.
var errRecorded = errors.New("payments: transfer already recorded")

// recordTransfer records one transfer and acts on it, in one transaction
// under the transaction hash's lock: it settles the payment with exactly its
// amount, or leaves it to the manual proof that carries its hash, or records
// it as unmatched and announces it (at once, or after the hold when a manual
// proof may still claim it). It reports false when the transfer was recorded
// before.
func (s *Service) recordTransfer(ctx context.Context, t chainTransfer) (bool, error) {
	var settled *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.LockTXID(ctx, tx, t.txid); err != nil {
			return err
		}
		if seen, err := s.st.ChainTransferRecorded(ctx, tx, t.txid, t.index); err != nil || seen {
			if err == nil {
				err = errRecorded
			}
			return err
		}
		manual, err := s.st.ManualReceiptByTXID(ctx, tx, t.txid, "")
		if err != nil {
			return err
		}
		restoredAt, err := s.st.RestoredAt(ctx, tx)
		if err != nil {
			return err
		}
		cands, err := s.st.ChainCandidates(ctx, tx, t.to, t.value, t.blockTS, chainSkew, reopenWindow)
		if err != nil {
			return err
		}
		var (
			open         []*store.Intent
			paid, closed bool
		)
		for _, c := range cands {
			switch {
			case chainSettleable(c):
				open = append(open, c)
			case c.Status == "succeeded":
				paid = true
			default:
				closed = true
			}
		}
		rec := &store.ChainTransfer{TxID: t.txid, EventIndex: t.index, Network: chainNetwork, Token: trongrid.USDTContract,
			From: t.from, To: t.to, Value: t.value, BlockTS: t.blockTS}
		// What the transfer is by its amount alone.
		reason := ""
		switch {
		case len(open) == 1 && heldAfterRestore(open[0], t.blockTS, restoredAt):
			rec.Outcome, reason = store.TransferUnmatched, UnmatchedAfterRestore
		case len(open) == 1:
			rec.Outcome, rec.IntentID = store.TransferMatched, &open[0].ID
		case len(open) > 1:
			rec.Outcome, reason = store.TransferUnmatched, UnmatchedAmbiguous
		case t.value < chainDust:
			rec.Outcome, reason = store.TransferIgnored, IgnoredDust
		case paid:
			rec.Outcome, reason = store.TransferUnmatched, UnmatchedAlreadyPaid
		case closed:
			rec.Outcome, reason = store.TransferUnmatched, UnmatchedClosed
		default:
			rec.Outcome, reason = store.TransferUnmatched, UnmatchedNoPayment
		}
		// A manual proof with this hash, unless rejected. A transfer with an
		// automatic payment's unique amount (a manual price is whole cents,
		// a unique amount never) is the automatic path's: a waiting proof
		// cannot take it (and can no longer be approved), and one already
		// approved means the automatic payer was not credited for it: the
		// automatic payment is not settled as well (the transfer was credited
		// once), and staff are told at once. Any other transfer is the
		// proof's, staff credited it already (approved) or will review it
		// (waiting), and staff are told of the claim. The reason is kept: if
		// the proof is rejected, the transfer is unmatched again for that
		// reason.
		proof := "" // the state of the proof that takes the transfer
		if manual != nil && (manual.Decision == nil || *manual.Decision == "approved") {
			approved := manual.Decision != nil
			switch {
			case len(cands) > 0 && approved:
				rec.Outcome, rec.IntentID, reason = store.TransferUnmatched, nil, UnmatchedShadowed
			case len(cands) > 0:
				// By its amount, as above.
			default:
				rec.Outcome, rec.IntentID, proof = store.TransferManual, &manual.IntentID, "waiting"
				if approved {
					proof = "approved"
				}
			}
		}
		if reason != "" {
			rec.Reason = &reason
		}
		// A deposit no automatic payment asked for may be a manual customer's
		// whose proof is on its way: staff hear of it once the hold ends, if
		// still unmatched. Any other unmatched deposit is announced now: no
		// proof can take it (an automatic amount, or a hash a rejected proof
		// carried).
		if rec.Outcome == store.TransferUnmatched && reason == UnmatchedNoPayment && manual == nil {
			at := time.Now().Add(time.Duration(s.hold.Load()))
			rec.AnnounceAt = &at
		}
		inserted, err := s.st.InsertChainTransfer(ctx, tx, rec)
		if err != nil {
			return err
		}
		if !inserted {
			return errRecorded
		}
		switch {
		case rec.Outcome == store.TransferMatched:
			got, err := s.settleChain(ctx, tx, open[0], t)
			settled = got
			return err
		case rec.Outcome == store.TransferUnmatched && rec.AnnounceAt == nil:
			return s.announceUnmatched(ctx, tx, rec)
		case rec.Outcome == store.TransferManual:
			return s.announceClaim(ctx, tx, rec, proof)
		}
		return nil
	})
	if errors.Is(err, errRecorded) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if settled != nil {
		s.log.Info("USDT payment settled", "intent", settled.ID, "txid", t.txid)
	}
	return true, nil
}

// announceHeld announces the unmatched deposits whose hold ended and that no
// manual proof claimed meanwhile, each once (across replicas), under its
// transaction hash's lock. It returns how many it announced.
func (s *Service) announceHeld(ctx context.Context) (int, error) {
	now := time.Now()
	due, err := s.st.DueChainAnnouncements(ctx, now, chainAnnounceBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range due {
		took := false
		err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
			if err := s.st.LockTXID(ctx, tx, due[i].TxID); err != nil {
				return err
			}
			t, err := s.st.TakeChainAnnouncement(ctx, tx, due[i].ID, now)
			if err != nil || t == nil {
				return err
			}
			took = true
			return s.announceUnmatched(ctx, tx, t)
		})
		if err != nil {
			return n, err
		}
		if took {
			n++
		}
	}
	return n, nil
}

// announceClaim publishes payment.deposit_claimed.v1 for a transfer a manual
// proof (intent, in state proof) just took, in the transaction that tied them.
func (s *Service) announceClaim(ctx context.Context, tx pgx.Tx, t *store.ChainTransfer, proof string) error {
	e := DepositClaimedEvent{TxID: t.TxID, EventIndex: t.EventIndex, Network: t.Network, From: t.From, To: t.To,
		Amount: t.Value, Currency: "USDT", BlockTime: t.BlockTS.Unix(), Proof: proof}
	if t.Reason != nil {
		e.Reason = *t.Reason
	}
	if t.IntentID != nil {
		e.IntentID = *t.IntentID
	}
	_, err := s.ob.Publish(ctx, tx, TopicDepositClaimed, e)
	return err
}

// announceUnmatched publishes payment.deposit_unmatched.v1 for a transfer
// that just became unmatched, in the transaction that made it so.
func (s *Service) announceUnmatched(ctx context.Context, tx pgx.Tx, t *store.ChainTransfer) error {
	e := DepositUnmatchedEvent{TxID: t.TxID, EventIndex: t.EventIndex, Network: t.Network, From: t.From, To: t.To,
		Amount: t.Value, Currency: "USDT", BlockTime: t.BlockTS.Unix()}
	if t.Reason != nil {
		e.Reason = *t.Reason
	}
	_, err := s.ob.Publish(ctx, tx, TopicDepositUnmatched, e)
	return err
}

// settleChain settles a payment from its transfer (the caller holds the
// payment's row lock and has recorded the transfer in tx). The ledger and the
// event carry the payment's own amount: never more, whatever was sent.
func (s *Service) settleChain(ctx context.Context, tx pgx.Tx, in *store.Intent, t chainTransfer) (*store.Intent, error) {
	ext := t.txid
	if t.index > 0 {
		ext += "#" + strconv.Itoa(t.index)
	}
	got, err := s.st.SettleGatewayPaid(ctx, tx, in.ID, ext, t.txid)
	if err != nil {
		return nil, err // locked and settleable: any error is a real one, read again
	}
	if err := s.st.AppendLedgerCredit(ctx, tx, got.UserID, got.Currency, got.Amount, "intent", got.ID, "gateway:"+got.ID); err != nil {
		return nil, err
	}
	if err := s.st.RecordGatewayEvent(ctx, tx, got.ID, USDTAuto, "match", "paid",
		map[string]any{"txid": t.txid, "event_index": t.index, "amount": t.value, "late": in.Status != "pending" && in.Status != "confirming"}); err != nil {
		return nil, err
	}
	return got, s.publishOutcome(ctx, tx, got, events.PaymentsPaymentSucceeded, "")
}

// expireChain expires an address's open payments whose window ended before
// before (a late exact payment still settles them).
func (s *Service) expireChain(ctx context.Context, addr string, before time.Time) error {
	due, err := s.st.DueChainExpiries(ctx, addr, before)
	if err != nil {
		return err
	}
	for _, in := range due {
		if _, err := s.failGateway(ctx, in, "expired", ReasonExpired, "scan", nil); err != nil {
			return err
		}
	}
	return nil
}

// scanDone records how a read of an address went. A new error is logged and
// kept as a gateway event (the report's last error), once, not every read.
func (s *Service) scanDone(ctx context.Context, addr string, err error) {
	text := ""
	if err != nil {
		text = err.Error()
		if len(text) > 300 {
			text = text[:300]
		}
	}
	prev, derr := s.st.ChainScanDone(ctx, addr, text)
	if derr != nil {
		s.log.Warn("chain watcher: record read", "address", addr, "err", derr)
		return
	}
	switch {
	case text != "" && (prev == nil || *prev != text):
		s.log.Warn("chain watcher: read failed", "address", addr, "err", text)
		_ = s.st.RecordGatewayEvent(ctx, nil, "", USDTAuto, "scan", "error", map[string]any{"error": text})
	case text == "" && prev != nil:
		s.log.Info("chain watcher: reads work again", "address", addr)
	}
}

// heldAfterRestore: in was created after the database was restored (at
// restoredAt) and the transfer came within chainRestoreHold of the restore.
// The restore erased the payments made since the backup, but not what their
// customers were told: one of them may send the amount in was given.
func heldAfterRestore(in *store.Intent, blockTS time.Time, restoredAt *time.Time) bool {
	return restoredAt != nil && !in.CreatedAt.Before(*restoredAt) && blockTS.Before(restoredAt.Add(chainRestoreHold))
}

// automaticDeposit: an unmatched transfer whose amount an automatic payment
// asked for (paid already, closed, more than one, taken by a proof approved
// before the watcher saw it, or held after a restore). It belongs to the
// automatic path: staff credit its sender by hand, and no manual proof may
// take it.
func automaticDeposit(t *store.ChainTransfer) bool {
	if t.Outcome != store.TransferUnmatched || t.Reason == nil {
		return false
	}
	switch *t.Reason {
	case UnmatchedAlreadyPaid, UnmatchedClosed, UnmatchedAmbiguous, UnmatchedShadowed, UnmatchedAfterRestore:
		return true
	}
	return false
}

// announcedDeposit: an unmatched transfer staff were told about (at once,
// or once its hold ended), and told to credit by hand. From then on it is
// the staff's to credit, from the deposit, and no manual proof may take it:
// staff credit it by hand first and mark it handled afterwards, and a proof
// approved in between would credit the same USDT a second time.
func announcedDeposit(t *store.ChainTransfer) bool {
	return t.Outcome == store.TransferUnmatched && t.AnnounceAt == nil
}

// guardSubmittedTXID refuses a manual crypto proof whose transaction already
// paid: settled a payment automatically, was credited by staff from an
// unmatched deposit, or is another payment's proof (with or without 0x); one
// whose transfer carries an automatic payment's unique amount; and one whose
// transfer staff were already told to credit by hand (announcedDeposit). A
// deposit still in its hold may be claimed. It holds the hash's lock until
// the submission commits.
func (s *Service) guardSubmittedTXID(ctx context.Context, tx pgx.Tx, intentID, txid string) error {
	key := txidKey(txid)
	if key == "" {
		return nil
	}
	if err := s.st.LockTXID(ctx, tx, key); err != nil {
		return err
	}
	seen, err := s.st.ChainTransfersByTXID(ctx, tx, key)
	if err != nil {
		return err
	}
	for i := range seen {
		switch t := &seen[i]; {
		case t.Outcome == store.TransferMatched || t.Outcome == store.TransferResolved:
			return store.ErrDuplicateProof
		case automaticDeposit(t):
			return ErrTXIDAutomatic
		case announcedDeposit(t):
			return ErrTXIDAnnounced
		}
	}
	other, err := s.st.ManualReceiptByTXID(ctx, tx, key, intentID)
	if err != nil {
		return err
	}
	if other != nil {
		return store.ErrDuplicateProof
	}
	return nil
}

// lockReceiptTXID takes the lock of the hash on an intent's manual proof
// until the review commits, so the watcher sees the review whole; "" when
// the proof has no hash.
func (s *Service) lockReceiptTXID(ctx context.Context, tx pgx.Tx, intentID string) (string, error) {
	txid, err := s.st.ReceiptTXID(ctx, tx, intentID)
	if err != nil || txid == nil {
		return "", err
	}
	key := txidKey(*txid)
	if key == "" {
		return "", nil
	}
	return key, s.st.LockTXID(ctx, tx, key)
}

// guardApprovedTXID refuses to approve a manual crypto proof whose
// transaction (key, locked) paid something else since it was submitted: the
// watcher settled an automatic payment with it, or staff credited it as an
// unmatched deposit, or another proof of it was approved; or whose transfer
// turned out to carry an automatic payment's unique amount, or is an
// unmatched deposit staff were told to credit by hand.
func (s *Service) guardApprovedTXID(ctx context.Context, tx pgx.Tx, intentID, key string) error {
	if key == "" {
		return nil
	}
	seen, err := s.st.ChainTransfersByTXID(ctx, tx, key)
	if err != nil {
		return err
	}
	for i := range seen {
		switch t := &seen[i]; {
		case t.Outcome == store.TransferResolved || (t.Outcome == store.TransferMatched && (t.IntentID == nil || *t.IntentID != intentID)):
			return ErrTXIDUsed
		case automaticDeposit(t):
			return ErrTXIDAutomatic
		case announcedDeposit(t):
			return ErrTXIDAnnounced
		}
	}
	other, err := s.st.ManualReceiptByTXID(ctx, tx, key, intentID)
	if err != nil {
		return err
	}
	if other != nil && other.Decision != nil && *other.Decision == "approved" {
		return ErrTXIDUsed
	}
	return nil
}

// claimChainTransfers ties a transaction's unmatched (or dust) transfers to
// the manual payment whose proof just named it, and tells staff of each
// claim: the hash of a transfer to the store is public.
func (s *Service) claimChainTransfers(ctx context.Context, tx pgx.Tx, key, intentID string) error {
	claimed, err := s.st.ClaimChainTransfers(ctx, tx, key, intentID)
	if err != nil {
		return err
	}
	for i := range claimed {
		if err := s.announceClaim(ctx, tx, &claimed[i], "waiting"); err != nil {
			return err
		}
	}
	return nil
}

// releaseChainTransfers makes the transfers a rejected manual proof held
// unmatched again (dust: ignored again) and announces them: nobody was
// credited for them.
func (s *Service) releaseChainTransfers(ctx context.Context, tx pgx.Tx, intentID string) error {
	released, err := s.st.ReleaseChainTransfers(ctx, tx, intentID)
	if err != nil {
		return err
	}
	for i := range released {
		if released[i].Outcome == store.TransferUnmatched {
			if err := s.announceUnmatched(ctx, tx, &released[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResolveChainTransfer marks an unmatched deposit handled: staff credited it
// by hand (or refunded it). A resolved transaction can no longer be sent as
// a manual proof.
func (s *Service) ResolveChainTransfer(ctx context.Context, actorID, txid string, index int, note string) (*store.ChainTransfer, error) {
	key := txidKey(txid)
	if !txidRe.MatchString(key) || index < 0 {
		return nil, invalid("a TRC20 transaction id is 64 hexadecimal characters")
	}
	id, err := buuid.Parse(actorID)
	if err != nil {
		return nil, invalid("actor id must be a user id")
	}
	actor := id.String()
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxReasonLen {
		return nil, invalid("note too long")
	}
	var out *store.ChainTransfer
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.LockTXID(ctx, tx, key); err != nil {
			return err
		}
		out, err = s.st.ResolveChainTransfer(ctx, tx, key, index, &actor, note)
		return err
	})
	if errors.Is(err, store.ErrInvalidTransition) {
		t, gerr := s.st.GetChainTransfer(ctx, key, index)
		if gerr != nil {
			return nil, gerr
		}
		return nil, precondition("this transfer is %s, not unmatched", t.Outcome)
	}
	if err != nil {
		return nil, err
	}
	s.log.Info("unmatched deposit resolved", "txid", key, "index", index, "actor", actor)
	return out, nil
}

// ListChainTransfers is the USDT transfer log for staff, newest first.
func (s *Service) ListChainTransfers(ctx context.Context, f store.ChainTransferFilter, limit, offset int) ([]store.ChainTransfer, int64, error) {
	switch f.Outcome {
	case "", store.TransferMatched, store.TransferUnmatched, store.TransferManual, store.TransferResolved, store.TransferIgnored:
	default:
		return nil, 0, invalid("unknown outcome %q", f.Outcome)
	}
	if f.TxID != "" {
		f.TxID = txidKey(f.TxID)
		if !txidRe.MatchString(f.TxID) {
			return nil, 0, invalid("a TRC20 transaction id is 64 hexadecimal characters")
		}
	}
	if f.IntentID != "" {
		if _, err := buuid.Parse(f.IntentID); err != nil {
			return nil, 0, invalid("intent id must be a payment id")
		}
	}
	if f.Value < 0 || f.Value > maxChainAmount {
		return nil, 0, invalid("value must be an amount in micro-USDT")
	}
	return s.st.ListChainTransfers(ctx, f, limit, offset)
}
