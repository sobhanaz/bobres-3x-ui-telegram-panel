package domain

// Automatic TON (Toncoin) payments. The order's price arrives from core in micro-USDT (core worked it out from the
// Toman price at the store's USDT rate, margin included, as for automatic USDT); this service turns it into TON at
// the price it reads from public exchanges (two sources, checked against each other, fresh for three minutes: with
// no fresh price the method is not offered at all, and nothing is guessed). The customer is asked for that
// amount, rounded up to the nanoton, and for the payment's own comment (BOB-<8 hex digits>, made from its id and
// unique for ever), and pays from any wallet. The watcher reads the store's address from TON Center every 30
// seconds and, for a transaction at least a minute old, matches the comment: it settles the payment when the
// amount is at least what was asked for, through the same compare-and-set as the other gateways
// (payment.succeeded.v1, ledger key gateway:<id>), exactly once, whatever the order of the callbacks, checks and
// reads. A transaction is recorded by its hash and logical time before anything is settled with it. Anything else
// is recorded and, unless it is dust, announced to staff, who credit it by hand: a comment that names no payment,
// a payment already paid, one that can no longer be paid, less than was asked, or a late transfer once the TON
// price has fallen so that it no longer covers the price. Nothing unmatched is ever credited by itself.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/events"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/gateway"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/payments/store"
	"github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/tonaddr"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Timing and limits of automatic TON payments.
const (
	// tonPayWindow: how long the amount may be paid (the TON price is locked for that long). A later transfer is
	// looked at too, within reopenWindow of the payment's creation, and settles only if it still covers the price.
	tonPayWindow = 30 * time.Minute
	// tonExpiryGrace: an open payment expires only once a read that started this long after its window has
	// gone through.
	tonExpiryGrace = 5 * time.Minute
	// tonFinality: a transaction is acted on only once it is this old. TON Center reads the account as of the
	// newest masterchain block, which is final, so this is a second line of defence, not the first.
	tonFinality = time.Minute
	// tonSkew: how far a transaction's time and our clock may differ; a new address is read from this far back.
	tonSkew = 2 * time.Minute
	// tonQuoteFresh: how old the TON price may be and still be used.
	tonQuoteFresh = 3 * time.Minute
	// tonMinUSDT and tonMaxUSDT bound a payment's price: 0.1 and 100,000 USDT.
	tonMinUSDT = 100_000
	tonMaxUSDT = 100_000_000_000
	// tonUserUnpaid: unpaid TON payments one customer may hold within tonCapWindow.
	tonUserUnpaid = 10
	tonCapWindow  = 48 * time.Hour
	// tonReuseLeft: a customer asking again for a payment still open is shown that payment again while at least
	// this much of its window is left.
	tonReuseLeft = 10 * time.Minute
	// tonCheckGap: the bot's check button reads an address at most this often, across every customer and replica.
	tonCheckGap = 10 * time.Second
	// tonReadTimeout bounds one read of an address.
	tonReadTimeout = time.Minute
	// tonPages and tonPageSize bound a read: 3,000 transactions.
	tonPages    = 30
	tonPageSize = 100
	// tonDust: an unmatched transfer below 0.01 TON is recorded but nobody is told (address poisoning).
	tonDust = 10_000_000
	// tonFuture: a transaction time further ahead of our clock is not believed.
	tonFuture = time.Hour
	// tonBouncePercent: a transaction that sent at least this share of what it received back to the sender
	// bounced: the money did not stay.
	tonBouncePercent = 90
	tonNetwork       = "TON"
	tonMemoPrefix    = "BOB-"
)

// Why a TON transfer is unmatched (beyond the reasons it shares with the USDT ones).
const (
	// UnmatchedUnderpaid: its comment names a payment and less than was asked arrived.
	UnmatchedUnderpaid = "underpaid"
	// UnmatchedLatePrice: its comment names a payment whose time had run out, and at today's price what arrived
	// is worth less than the price.
	UnmatchedLatePrice = "late_price"
	// IgnoredBounced: the transaction sent the money back (a bounce).
	IgnoredBounced = "bounced"
)

var (
	tonMemoRe   = regexp.MustCompile(`^BOB-[0-9A-F]{8}$`)
	tonHashRe   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	tonDigitsRe = regexp.MustCompile(`^[0-9]{1,19}$`)

	// ErrTONUnavailable: no fresh TON price (or the method is off): nothing is guessed.
	ErrTONUnavailable = fmt.Errorf("%w: automatic TON: no fresh TON price is available now; use another payment method or try again shortly", ErrPrecondition)
	// ErrTONTooManyUnpaid: the customer holds tonUserUnpaid unpaid TON payments.
	ErrTONTooManyUnpaid = fmt.Errorf("%w: automatic TON: too many unpaid automatic TON payments in the last 2 days; "+
		"pay one of them or use another payment method", ErrPrecondition)
)

// tonMemoOf is the payment comment an id has: BOB- and the last 8 hex digits of the id, upper case (the
// reference a customer is shown for any payment).
func tonMemoOf(intentID string) string {
	ref := strings.ToUpper(strings.ReplaceAll(intentID, "-", ""))
	if len(ref) > 8 {
		ref = ref[len(ref)-8:]
	}
	return tonMemoPrefix + ref
}

// tonMemoIn reads a payment comment out of a transfer's: spaces around it and its case do not matter. "" when
// the text is not one.
func tonMemoIn(comment string) string {
	m := strings.ToUpper(strings.TrimSpace(comment))
	if tonMemoRe.MatchString(m) {
		return m
	}
	return ""
}

// tonNano is the TON, in nanoton and rounded up, that usdt micro-USDT is worth at price micro-USDT per TON.
func tonNano(usdt, price int64) (int64, bool) {
	if usdt <= 0 || price <= 0 {
		return 0, false
	}
	n := new(big.Int).Mul(big.NewInt(usdt), big.NewInt(1_000_000_000))
	n.Add(n, big.NewInt(price-1))
	n.Quo(n, big.NewInt(price))
	if !n.IsInt64() || n.Sign() <= 0 {
		return 0, false
	}
	return n.Int64(), true
}

// tonWorth is the micro-USDT that nano nanoton are worth at price micro-USDT per TON, rounded down.
func tonWorth(nano, price int64) *big.Int {
	w := new(big.Int).Mul(big.NewInt(nano), big.NewInt(price))
	return w.Quo(w, big.NewInt(1_000_000_000))
}

// freshTONQuote is the TON price if it is recent enough to use.
func (s *Service) freshTONQuote() (gateway.TONQuote, bool) {
	q := s.tonQ.Load()
	if q == nil || q.PriceMicro <= 0 || time.Since(q.At) > tonQuoteFresh {
		return gateway.TONQuote{}, false
	}
	return *q, true
}

// tonOffered: new payments can use automatic TON now: switched on, readable, with an address and a fresh price.
func (s *Service) tonOffered() bool {
	c := s.ton.Load()
	if !c.listed() || !c.enabled || !c.readable() || c.settings.Address == "" || c.opts.TONPrice == nil {
		return false
	}
	_, ok := s.freshTONQuote()
	return ok
}

// RefreshTONPrice reads the TON price and keeps it. A failure keeps the last price, which goes stale on its own
// after three minutes. Payments reads it every 30 seconds while the watcher can read an address.
func (s *Service) RefreshTONPrice(ctx context.Context) error {
	c := s.ton.Load()
	if c.opts == nil || c.opts.TONPrice == nil {
		return errors.New("payments: no TON price source")
	}
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	q, err := c.opts.TONPrice.Quote(rctx)
	if err != nil {
		text := err.Error()
		if prev := s.tonPriceErr.Swap(&text); prev == nil || *prev != text {
			s.log.Warn("TON price", "err", text)
		}
		return err
	}
	if s.tonPriceErr.Swap(nil) != nil {
		s.log.Info("TON price: readable again", "source", q.Source)
	}
	s.tonQ.Store(&q)
	return nil
}

// StartTON creates an automatic TON payment (idempotent per key): from the price core asked for (micro-USDT, at
// least 0.1 and at most 100,000 USDT) and the TON price now, the exact amount in nanoton, rounded up, the
// payment's comment, the address to pay and a 30-minute window. A retry with the same key returns the payment
// already created, and so does the same customer asking again for the same payment (order or top-up, price)
// under a new key while it is open.
func (s *Service) StartTON(ctx context.Context, in *store.Intent) (*store.Intent, error) {
	// Held until the payment is stored: an address change waits for it, then sees it as open and is refused.
	s.tonMu.RLock()
	defer s.tonMu.RUnlock()
	cs := s.ton.Load()
	if !cs.listed() || !cs.enabled || !cs.readable() || cs.settings.Address == "" {
		return nil, invalid("payment method %q is not enabled", TONAuto)
	}
	in.Provider = TONAuto
	if in.GatewayAmount == nil || *in.GatewayAmount <= 0 || in.GatewayCurrency == nil || *in.GatewayCurrency != "USDT" {
		return nil, invalid("%s needs a positive amount in USDT", TONAuto)
	}
	base := *in.GatewayAmount
	if base < tonMinUSDT || base > tonMaxUSDT {
		return nil, invalid("%s takes 0.1 to 100,000 USDT per payment", TONAuto)
	}
	if err := validIntent(in); err != nil {
		return nil, err
	}
	q, ok := s.freshTONQuote()
	if !ok || cs.opts.TONPrice == nil {
		return nil, ErrTONUnavailable
	}
	nano, ok := tonNano(base, q.PriceMicro)
	if !ok {
		return nil, invalid("%s: this price cannot be asked in TON", TONAuto)
	}
	addr, ton := cs.settings.Address, "TON"
	exp := time.Now().Add(tonPayWindow)
	in.ExpiresAt, in.PayAddress, in.GatewayCurrency, in.Status = &exp, &addr, &ton, "pending"
	in.GatewayAmount, in.QuoteUSDT, in.QuoteRate, in.QuoteSource = &nano, &base, &q.PriceMicro, &q.Source
	var out *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		got, err := s.st.ExistingIntent(ctx, tx, in)
		if err != nil || got != nil {
			out = got
			return err
		}
		// What follows is serialised with every other automatic TON payment's start.
		if err := s.st.LockTONCreate(ctx, tx); err != nil {
			return err
		}
		if got, err = s.st.ReusableTONIntent(ctx, tx, in, addr, base, tonReuseLeft); err != nil || got != nil {
			out = got
			return err
		}
		unpaid, err := s.st.UnpaidTONIntents(ctx, tx, in.UserID, tonCapWindow)
		if err != nil {
			return err
		}
		if unpaid >= tonUserUnpaid {
			s.log.Warn("too many unpaid TON payments", "user", in.UserID, "unpaid", unpaid)
			return ErrTONTooManyUnpaid
		}
		// The comment is made from the id; a clash (a 32-bit tail) takes a new id.
		for range 8 {
			in.ID = buuid.MustV7().String()
			memo := tonMemoOf(in.ID)
			taken, err := s.st.TONMemoTaken(ctx, tx, memo)
			if err != nil {
				return err
			}
			if !taken {
				in.PayMemo = &memo
				break
			}
		}
		if in.PayMemo == nil {
			return errors.New("payments: no free TON comment")
		}
		if out, err = s.st.CreateIntent(ctx, tx, in); err != nil {
			return err
		}
		if out.ID != in.ID {
			return nil // created concurrently with the same key
		}
		if err := s.st.EnsureTONCursor(ctx, tx, addr, time.Now().Add(-tonSkew)); err != nil {
			return err
		}
		return s.st.RecordGatewayEvent(ctx, tx, out.ID, TONAuto, "create", "pending",
			map[string]any{"amount": nano, "usdt": base, "rate": q.PriceMicro, "source": q.Source, "memo": *in.PayMemo})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RunTONWatcher reads the TON price and the chain every interval until ctx ends. It runs even with nothing to
// read: automatic TON may be switched on from the dashboard later.
func (s *Service) RunTONWatcher(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if s.ton.Load().readable() {
			_ = s.RefreshTONPrice(ctx) // logged on a change
		}
		if _, err := s.ScanTONOnce(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("TON watcher", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// ScanTONOnce reads every address that needs it: the store's address while automatic TON is on, and any address
// a payment may still be settled at (open, or closed but within the late-payment window). It returns how many new
// transfers it recorded.
func (s *Service) ScanTONOnce(ctx context.Context) (int, error) {
	cs := s.ton.Load()
	if !cs.readable() {
		return 0, nil
	}
	addrs, err := s.st.TONScanAddresses(ctx, reopenWindow+tonSkew)
	if err != nil {
		return 0, err
	}
	if a := cs.settings.Address; cs.enabled && a != "" && !slices.Contains(addrs, a) {
		addrs = append(addrs, a)
	}
	s.tonScanMu.Lock()
	defer s.tonScanMu.Unlock()
	total := 0
	for _, a := range addrs {
		if ctx.Err() != nil {
			break
		}
		n, _ := s.scanTONAddress(ctx, cs.client, a) // recorded on the cursor and logged
		total += n
	}
	return total, nil
}

// checkTON is the customer's "check payment" for an automatic TON payment: it reads the payment's address now,
// at most every tonCheckGap (a read already running will settle it), and returns the payment as it is then.
func (s *Service) checkTON(ctx context.Context, in *store.Intent) (*store.Intent, error) {
	if in.Status == "succeeded" || in.PayAddress == nil || !tonSettleable(in) || time.Since(in.CreatedAt) > reopenWindow+tonSkew {
		return in, nil
	}
	cs := s.ton.Load()
	if !cs.readable() {
		return in, nil
	}
	ok, err := s.st.TryTONScan(ctx, *in.PayAddress, tonCheckGap)
	if err != nil {
		return nil, err
	}
	if !ok || !s.tonScanMu.TryLock() {
		return in, nil
	}
	defer s.tonScanMu.Unlock()
	_, _ = s.scanTONAddress(ctx, cs.client, *in.PayAddress)
	return s.st.GetIntent(ctx, nil, in.ID)
}

// tonSettleable: a payment a transfer may still settle (the transfer's time is checked by the caller).
func tonSettleable(in *store.Intent) bool { return chainSettleable(in) }

// tonTransfer is a transaction that passed validation.
type tonTransfer struct {
	hash    string
	lt      int64
	from    string
	to      string
	value   int64
	comment string
	ts      time.Time
	bounced bool
}

// scanTONAddress reads an address's new transactions and records them. The cursor only moves past transactions
// that were dealt with, in order, and never past one that is not a minute old yet. Only a complete read expires
// the address's payments whose window ended. The caller holds tonScanMu.
func (s *Service) scanTONAddress(ctx context.Context, client gateway.TONSource, addr string) (int, error) {
	start := time.Now()
	cur, err := s.st.GetTONCursor(ctx, addr)
	if errors.Is(err, store.ErrNotFound) {
		floor := start.Add(-tonSkew)
		if err := s.st.EnsureTONCursor(ctx, nil, addr, floor); err != nil {
			return 0, err
		}
		cur, err = &store.TONCursor{Address: addr, FloorTS: floor}, nil
	}
	if err != nil {
		return 0, err
	}
	rctx, cancel := context.WithTimeout(ctx, tonReadTimeout)
	defer cancel()
	var (
		got      []gateway.TONTx
		complete bool
		readErr  error
	)
	q := gateway.TONQuery{Address: addr, Limit: tonPageSize}
pages:
	for i := range tonPages {
		page, err := client.Transactions(rctx, q)
		if err != nil {
			if i == 0 {
				s.tonScanDone(ctx, addr, err)
				return 0, err
			}
			readErr = err
			break
		}
		for j, t := range page {
			switch {
			case i > 0 && j == 0 && t.LT == q.BeforeLT:
				continue // the page starts at the transaction it continues from
			case t.LT <= cur.LastLT || t.Time.Before(cur.FloorTS):
				complete = true // what is below was dealt with already, or is before the floor
				break pages
			}
			got = append(got, t)
		}
		if len(page) < tonPageSize {
			complete = true
			break
		}
		last := page[len(page)-1]
		if last.LT == q.BeforeLT || last.RawHash == "" {
			break // the source repeats itself: read the rest next time
		}
		q.BeforeLT, q.BeforeHash = last.LT, last.RawHash
	}
	slices.SortStableFunc(got, func(a, b gateway.TONTx) int { return cmp.Compare(a.LT, b.LT) })
	got = slices.CompactFunc(got, func(a, b gateway.TONTx) bool { return a.LT == b.LT && a.Hash == b.Hash })

	recorded, maxLT := 0, cur.LastLT
	future := 0
	for _, t := range got {
		if t.Time.After(start.Add(-tonFinality)) {
			if t.Time.After(start.Add(tonFuture)) {
				future++
			}
			break // not final yet (and nothing after it is older)
		}
		if tt, why := s.validTON(addr, t); tt != nil {
			ok, err := s.recordTON(ctx, tt)
			if err != nil {
				_ = s.st.AdvanceTONCursor(ctx, addr, maxLT)
				s.tonScanDone(ctx, addr, fmt.Errorf("record transfer %s: %w", tt.hash, err))
				return recorded, err
			}
			if ok {
				recorded++
			}
		} else if why != "" {
			s.log.Warn("TON transaction refused", "address", addr, "why", why)
		}
		maxLT = max(maxLT, t.LT)
	}
	if maxLT > cur.LastLT {
		if err := s.st.AdvanceTONCursor(ctx, addr, maxLT); err != nil {
			s.tonScanDone(ctx, addr, err)
			return recorded, err
		}
	}
	if complete {
		if err := s.expireTON(ctx, addr, start.Add(-tonExpiryGrace)); err != nil {
			s.tonScanDone(ctx, addr, err)
			return recorded, err
		}
	}
	switch {
	case readErr != nil:
		s.tonScanDone(ctx, addr, readErr)
		return recorded, readErr
	case future > 0:
		err := errors.New("ton: a transaction has a time more than an hour ahead of this server's clock; it is not recorded until it agrees (check the server's time)")
		s.tonScanDone(ctx, addr, err)
		return recorded, err
	case !complete:
		err := fmt.Errorf("ton: more than %d new transactions in one read; the older ones are read next time", tonPages*tonPageSize)
		s.tonScanDone(ctx, addr, err)
		return recorded, err
	}
	s.tonScanDone(ctx, addr, nil)
	return recorded, nil
}

// validTON keeps an incoming transfer to addr and refuses what is not one. Nothing an indexer says is trusted:
// the receiver is checked, the hash is 64 hex digits, the amount is digits, a zero-value transfer (address
// poisoning) is nothing. It returns nil and, for what looks wrong, why (what is merely not a transfer, an
// external message or an outgoing-only transaction, gives no reason).
func (s *Service) validTON(addr string, t gateway.TONTx) (*tonTransfer, string) {
	if !t.HasMsg || t.Source == "" {
		return nil, ""
	}
	switch {
	case !tonaddr.Same(t.Destination, addr):
		return nil, "another receiver"
	case !tonHashRe.MatchString(t.Hash) || t.LT <= 0:
		return nil, "bad transaction id"
	case !tonDigitsRe.MatchString(t.Value):
		return nil, "bad amount"
	}
	value, err := strconv.ParseInt(t.Value, 10, 64)
	if err != nil {
		return nil, "bad amount"
	}
	if value == 0 {
		return nil, ""
	}
	tt := &tonTransfer{hash: t.Hash, lt: t.LT, to: addr, value: value, comment: t.Comment, ts: t.Time, from: sender(t.Source)}
	for _, o := range t.OutMsgs {
		if tonaddr.Same(o.Destination, t.Source) && tonDigitsRe.MatchString(o.Value) {
			if back, err := strconv.ParseInt(o.Value, 10, 64); err == nil && back*100 >= value*tonBouncePercent {
				tt.bounced = true
			}
		}
	}
	return tt, ""
}

// sender is a sender's address as shown to staff: the canonical form when it is an address, else the text cut
// short.
func sender(src string) string {
	if a, err := tonaddr.Parse(src); err == nil {
		return a.Canonical()
	}
	if r := []rune(src); len(r) > 80 {
		return string(r[:80])
	}
	return src
}

// errTONRecorded: the transfer was recorded by another read.
var errTONRecorded = errors.New("payments: transfer already recorded")

// recordTON records one transfer and acts on it, in one transaction under the transaction hash's lock: it
// settles the payment whose comment it carries, or records it as unmatched and announces it. It reports false
// when the transfer was recorded before.
func (s *Service) recordTON(ctx context.Context, t *tonTransfer) (bool, error) {
	var settled *store.Intent
	err := s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.LockTONTx(ctx, tx, t.hash); err != nil {
			return err
		}
		if seen, err := s.st.TONTransferRecorded(ctx, tx, t.hash); err != nil || seen {
			if err == nil {
				err = errTONRecorded
			}
			return err
		}
		rec := &store.ChainTransfer{TxID: t.hash, LT: t.lt, Network: tonNetwork, Token: tonNetwork, From: t.from, To: t.to,
			Value: t.value, BlockTS: t.ts, Comment: t.comment}
		var (
			in       *store.Intent
			expected int64
			reason   string
			late     bool
		)
		if memo := tonMemoIn(t.comment); memo != "" {
			var err error
			if in, err = s.st.TONIntentByMemo(ctx, tx, memo); errors.Is(err, store.ErrNotFound) {
				in = nil
			} else if err != nil {
				return err
			}
			if in != nil && (in.PayAddress == nil || !tonaddr.Same(*in.PayAddress, t.to)) {
				in = nil // a comment of another address's payment
			}
		}
		if in != nil {
			rec.IntentID = &in.ID
			if in.GatewayAmount != nil {
				expected = *in.GatewayAmount
			}
		}
		switch {
		case t.bounced:
			rec.Outcome, reason = store.TransferIgnored, IgnoredBounced
		case in == nil && t.value < tonDust:
			rec.Outcome, reason = store.TransferIgnored, IgnoredDust
		case in == nil:
			rec.Outcome, reason = store.TransferUnmatched, UnmatchedNoPayment
		case in.Status == "succeeded":
			rec.Outcome, reason = store.TransferUnmatched, UnmatchedAlreadyPaid
		case !tonSettleable(in) || t.ts.Sub(in.CreatedAt) > reopenWindow:
			rec.Outcome, reason = store.TransferUnmatched, UnmatchedClosed
		case t.value < expected:
			rec.Outcome, reason = store.TransferUnmatched, UnmatchedUnderpaid
		default:
			// Enough. A transfer that came after the payment's time ran out is settled only if today's price
			// still makes what arrived worth the price: TON moves, and the amount was fixed 30 minutes or more ago.
			rec.Outcome = store.TransferMatched
			late = in.ExpiresAt != nil && t.ts.After(*in.ExpiresAt)
			if late {
				q, ok := s.freshTONQuote()
				if !ok || in.QuoteUSDT == nil || tonWorth(t.value, q.PriceMicro).Cmp(big.NewInt(*in.QuoteUSDT)) < 0 {
					rec.Outcome, reason = store.TransferUnmatched, UnmatchedLatePrice
				}
			}
		}
		if reason != "" {
			rec.Reason = &reason
		}
		inserted, err := s.st.InsertTONTransfer(ctx, tx, rec)
		if err != nil {
			return err
		}
		if !inserted {
			return errTONRecorded
		}
		switch rec.Outcome {
		case store.TransferMatched:
			got, err := s.settleTON(ctx, tx, in, t, late)
			settled = got
			return err
		case store.TransferUnmatched:
			return s.announceTON(ctx, tx, rec, expected)
		}
		return nil
	})
	if errors.Is(err, errTONRecorded) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if settled != nil {
		s.log.Info("TON payment settled", "intent", settled.ID, "txid", t.hash)
	}
	return true, nil
}

// settleTON settles a payment from its transfer (the caller holds the payment's row lock and has recorded the
// transfer in tx). The ledger and the event carry the payment's own price: never more, whatever was sent.
func (s *Service) settleTON(ctx context.Context, tx pgx.Tx, in *store.Intent, t *tonTransfer, late bool) (*store.Intent, error) {
	got, err := s.st.SettleGatewayPaid(ctx, tx, in.ID, t.hash, t.hash)
	if err != nil {
		return nil, err // locked and settleable: any error is a real one, read again
	}
	if err := s.st.AppendLedgerCredit(ctx, tx, got.UserID, got.Currency, got.Amount, "intent", got.ID, "gateway:"+got.ID); err != nil {
		return nil, err
	}
	detail := map[string]any{"txid": t.hash, "lt": t.lt, "amount": t.value, "late": late}
	if got.GatewayAmount != nil {
		detail["expected"] = *got.GatewayAmount
	}
	if err := s.st.RecordGatewayEvent(ctx, tx, got.ID, TONAuto, "match", "paid", detail); err != nil {
		return nil, err
	}
	return got, s.publishOutcome(ctx, tx, got, events.PaymentsPaymentSucceeded, "")
}

// announceTON publishes payment.deposit_unmatched.v1 for a transfer that just became unmatched, in the
// transaction that made it so, so core can tell staff exactly once.
func (s *Service) announceTON(ctx context.Context, tx pgx.Tx, t *store.ChainTransfer, expected int64) error {
	e := DepositUnmatchedEvent{TxID: t.TxID, Network: tonNetwork, From: t.From, To: t.To, Amount: t.Value, Currency: tonNetwork,
		BlockTime: t.BlockTS.Unix(), Comment: t.Comment}
	if t.Reason != nil {
		e.Reason = *t.Reason
	}
	if t.IntentID != nil {
		e.IntentID, e.Expected = *t.IntentID, expected
	}
	_, err := s.ob.Publish(ctx, tx, TopicDepositUnmatched, e)
	return err
}

// expireTON expires an address's open payments whose window ended before before (a late transfer still settles
// them, if it covers the price).
func (s *Service) expireTON(ctx context.Context, addr string, before time.Time) error {
	due, err := s.st.DueTONExpiries(ctx, addr, before)
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

// tonScanDone records how a read of an address went. A new error is logged and kept as a gateway event (the
// report's last error), once, not every read.
func (s *Service) tonScanDone(ctx context.Context, addr string, err error) {
	text := ""
	if err != nil {
		text = err.Error()
		if len(text) > 300 {
			text = text[:300]
		}
	}
	prev, derr := s.st.TONScanDone(ctx, addr, text)
	if derr != nil {
		s.log.Warn("TON watcher: record read", "address", addr, "err", derr)
		return
	}
	switch {
	case text != "" && (prev == nil || *prev != text):
		s.log.Warn("TON watcher: read failed", "address", addr, "err", text)
		_ = s.st.RecordGatewayEvent(ctx, nil, "", TONAuto, "scan", "error", map[string]any{"error": text})
	case text == "" && prev != nil:
		s.log.Info("TON watcher: reads work again", "address", addr)
	}
}

// ResolveTONTransfer marks an unmatched TON deposit handled: staff credited it by hand (or refunded it).
func (s *Service) ResolveTONTransfer(ctx context.Context, actorID, txid, note string) (*store.ChainTransfer, error) {
	key := txidKey(txid)
	if !tonHashRe.MatchString(key) {
		return nil, invalid("a TON transaction hash is 64 hexadecimal characters")
	}
	id, err := buuid.Parse(actorID)
	if err != nil {
		return nil, invalid("actor id must be a user id")
	}
	actor := id.String()
	if note = strings.TrimSpace(note); len([]rune(note)) > maxReasonLen {
		return nil, invalid("note too long")
	}
	var out *store.ChainTransfer
	err = s.st.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.st.LockTONTx(ctx, tx, key); err != nil {
			return err
		}
		out, err = s.st.ResolveTONTransfer(ctx, tx, key, &actor, note)
		return err
	})
	if errors.Is(err, store.ErrInvalidTransition) {
		t, gerr := s.st.GetTONTransfer(ctx, key)
		if gerr != nil {
			return nil, gerr
		}
		return nil, precondition("this transfer is %s, not unmatched", t.Outcome)
	}
	if err != nil {
		return nil, err
	}
	s.log.Info("unmatched TON deposit resolved", "txid", key, "actor", actor)
	return out, nil
}

// ListTONTransfers is the TON transfer log for staff, newest first.
func (s *Service) ListTONTransfers(ctx context.Context, f store.ChainTransferFilter, limit, offset int) ([]store.ChainTransfer, int64, error) {
	switch f.Outcome {
	case "", store.TransferMatched, store.TransferUnmatched, store.TransferResolved, store.TransferIgnored:
	default:
		return nil, 0, invalid("unknown outcome %q", f.Outcome)
	}
	if f.TxID != "" {
		f.TxID = txidKey(f.TxID)
		if !tonHashRe.MatchString(f.TxID) {
			return nil, 0, invalid("a TON transaction hash is 64 hexadecimal characters")
		}
	}
	if f.IntentID != "" {
		if _, err := buuid.Parse(f.IntentID); err != nil {
			return nil, 0, invalid("intent id must be a payment id")
		}
	}
	return s.st.ListTONTransfers(ctx, f, limit, offset)
}
