package store

// On-chain payments (usdt_trc20_auto): the transfers the watcher saw arrive,
// how far it has read each address, unique amounts and matching.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ChainProviders are the providers paid on chain and settled by the watcher.
var ChainProviders = []string{chainProvider}

const chainProvider = "usdt_trc20_auto"

// Chain transfer outcomes.
const (
	// TransferMatched: it settled its intent.
	TransferMatched = "matched"
	// TransferUnmatched: no payment has exactly this amount; staff credit it by hand.
	TransferUnmatched = "unmatched"
	// TransferManual: a manual crypto payment carries this TXID.
	TransferManual = "manual"
	// TransferResolved: staff handled an unmatched transfer.
	TransferResolved = "resolved"
	// TransferIgnored: dust (address poisoning); nobody is told.
	TransferIgnored = "ignored"
)

// ErrNoFreeAmount: every unique amount near this price is taken.
var ErrNoFreeAmount = errors.New("store: no free unique amount")

// ChainTransfer mirrors payments.chain_transfers.
type ChainTransfer struct {
	ID         int64
	TxID       string // lower-case hex, no 0x
	EventIndex int
	Network    string
	Token      string
	From       string
	To         string
	Value      int64 // micro-USDT
	BlockTS    time.Time
	IntentID   *string
	Outcome    string
	Reason     *string
	ResolvedBy *string
	ResolvedAt *time.Time
	Note       *string
	// AnnounceAt: an unmatched deposit a manual proof may still claim is
	// announced to staff from then on, if still unmatched (nil = nothing
	// pending: announced already, or not unmatched).
	AnnounceAt *time.Time
	CreatedAt  time.Time
}

const chainTransferCols = `id, txid, event_index, network, token, from_address, to_address, value, block_ts,
	intent_id::text, outcome, reason, resolved_by::text, resolved_at, note, announce_at, created_at`

func scanChainTransfer(row pgx.Row) (*ChainTransfer, error) {
	var t ChainTransfer
	err := row.Scan(&t.ID, &t.TxID, &t.EventIndex, &t.Network, &t.Token, &t.From, &t.To, &t.Value, &t.BlockTS,
		&t.IntentID, &t.Outcome, &t.Reason, &t.ResolvedBy, &t.ResolvedAt, &t.Note, &t.AnnounceAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

func collectTransfers(rows pgx.Rows) ([]ChainTransfer, error) {
	defer rows.Close()
	var out []ChainTransfer
	for rows.Next() {
		t, err := scanChainTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// LockTXID serialises, until the transaction ends, everything that uses a
// transaction hash as a payment: the watcher recording a transfer, a customer
// submitting it as a manual proof, staff approving that proof or resolving
// the transfer. key is the normalised hash (lower case, no 0x).
func (s *Store) LockTXID(ctx context.Context, tx pgx.Tx, key string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('payments:txid:' || $1, 0))`, key); err != nil {
		return fmt.Errorf("lock txid: %w", err)
	}
	return nil
}

// ChainTransfersByTXID lists the recorded transfers of a transaction.
func (s *Store) ChainTransfersByTXID(ctx context.Context, tx pgx.Tx, key string) ([]ChainTransfer, error) {
	rows, err := s.q(tx).Query(ctx, `SELECT `+chainTransferCols+` FROM payments.chain_transfers
		WHERE txid = $1 ORDER BY event_index`, key)
	if err != nil {
		return nil, fmt.Errorf("chain transfers by txid: %w", err)
	}
	out, err := collectTransfers(rows)
	if err != nil {
		return nil, fmt.Errorf("chain transfers by txid: %w", err)
	}
	return out, nil
}

// ChainTransferRecorded reports whether this transfer is recorded already.
func (s *Store) ChainTransferRecorded(ctx context.Context, tx pgx.Tx, txid string, index int) (bool, error) {
	var ok bool
	if err := s.q(tx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payments.chain_transfers
		WHERE txid = $1 AND event_index = $2)`, txid, index).Scan(&ok); err != nil {
		return false, fmt.Errorf("chain transfer recorded: %w", err)
	}
	return ok, nil
}

// InsertChainTransfer records a transfer once; false when it already was.
func (s *Store) InsertChainTransfer(ctx context.Context, tx pgx.Tx, t *ChainTransfer) (bool, error) {
	tag, err := s.q(tx).Exec(ctx, `
		INSERT INTO payments.chain_transfers
			(txid, event_index, network, token, from_address, to_address, value, block_ts, intent_id, outcome, reason, announce_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (txid, event_index) DO NOTHING`,
		t.TxID, t.EventIndex, t.Network, t.Token, t.From, t.To, t.Value, t.BlockTS, t.IntentID, t.Outcome, t.Reason, t.AnnounceAt)
	if err != nil {
		return false, fmt.Errorf("insert chain transfer: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ClaimChainTransfers ties a transaction's unmatched or ignored transfers to
// the manual payment whose proof it is (an announcement still pending is
// dropped) and returns them.
func (s *Store) ClaimChainTransfers(ctx context.Context, tx pgx.Tx, key, intentID string) ([]ChainTransfer, error) {
	rows, err := s.q(tx).Query(ctx, `
		UPDATE payments.chain_transfers SET outcome = 'manual', intent_id = $2, announce_at = NULL, updated_at = now()
		WHERE txid = $1 AND outcome IN ('unmatched','ignored')
		RETURNING `+chainTransferCols, key, intentID)
	if err != nil {
		return nil, fmt.Errorf("claim chain transfers: %w", err)
	}
	out, err := collectTransfers(rows)
	if err != nil {
		return nil, fmt.Errorf("claim chain transfers: %w", err)
	}
	return out, nil
}

// ReleaseChainTransfers makes the transfers a rejected manual payment held
// unmatched again (ignored for dust), by the reason recorded with them, and
// returns them.
func (s *Store) ReleaseChainTransfers(ctx context.Context, tx pgx.Tx, intentID string) ([]ChainTransfer, error) {
	rows, err := s.q(tx).Query(ctx, `
		UPDATE payments.chain_transfers
		SET outcome = CASE WHEN reason = 'dust' THEN 'ignored' ELSE 'unmatched' END,
		    reason = COALESCE(reason, 'no_payment'), intent_id = NULL, updated_at = now()
		WHERE intent_id = $1 AND outcome = 'manual'
		RETURNING `+chainTransferCols, intentID)
	if err != nil {
		return nil, fmt.Errorf("release chain transfers: %w", err)
	}
	out, err := collectTransfers(rows)
	if err != nil {
		return nil, fmt.Errorf("release chain transfers: %w", err)
	}
	return out, nil
}

// ManualReceiptByTXID returns a manual receipt carrying this transaction hash
// (key is normalised; the receipt may have it with 0x or in upper case),
// other than exceptIntent's ("" = any).
func (s *Store) ManualReceiptByTXID(ctx context.Context, tx pgx.Tx, key, exceptIntent string) (*Receipt, error) {
	var r Receipt
	err := s.q(tx).QueryRow(ctx, `
		SELECT id, intent_id, network, txid, decision, reviewed_at FROM payments.manual_receipts
		WHERE txid IS NOT NULL AND lower(txid) = ANY($1) AND ($2 = '' OR intent_id::text <> $2)
		ORDER BY (decision = 'approved') IS TRUE DESC, decision IS NULL DESC, submitted_at LIMIT 1`, []string{key, "0x" + key}, exceptIntent).
		Scan(&r.ID, &r.IntentID, &r.Network, &r.TXID, &r.Decision, &r.ReviewedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("manual receipt by txid: %w", err)
	}
	return &r, nil
}

// ReceiptTXID returns the TXID on an intent's manual receipt (nil = none).
func (s *Store) ReceiptTXID(ctx context.Context, tx pgx.Tx, intentID string) (*string, error) {
	var txid *string
	err := s.q(tx).QueryRow(ctx, `SELECT txid FROM payments.manual_receipts WHERE intent_id = $1`, intentID).Scan(&txid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("receipt txid: %w", err)
	}
	return txid, nil
}

// LockChainAmounts takes the transaction-scoped lock that serialises the
// creation of on-chain intents (their unique amounts, reuse and limits).
func (s *Store) LockChainAmounts(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('payments:chain-amounts', 0))`); err != nil {
		return fmt.Errorf("lock chain amounts: %w", err)
	}
	return nil
}

// ReusableChainIntent returns the customer's own on-chain intent still
// waiting to be paid that in asks for again under another idempotency key:
// the same order (or top-up), amount and address, an amount made from base,
// and at least minLeft of its window left. nil when there is none.
func (s *Store) ReusableChainIntent(ctx context.Context, tx pgx.Tx, in *Intent, address string, base, maxOffset int64, minLeft time.Duration) (*Intent, error) {
	got, err := scanIntent(tx.QueryRow(ctx, `
		SELECT `+intentCols+` FROM payments.payment_intents
		WHERE provider = $1 AND user_id = $2 AND order_id IS NOT DISTINCT FROM $3 AND amount = $4 AND currency = $5
		  AND pay_address = $6 AND gateway_amount > $7 AND gateway_amount <= $7 + $8
		  AND status = 'pending' AND expires_at > now() + $9::interval
		ORDER BY created_at DESC LIMIT 1`,
		chainProvider, in.UserID, in.OrderID, in.Amount, in.Currency, address, base, maxOffset, minLeft.String()))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reusable chain intent: %w", err)
	}
	return got, nil
}

// UnpaidChainIntents counts a customer's on-chain intents created within
// window that are not paid (open, expired or failed): each holds a unique
// amount for that long.
func (s *Store) UnpaidChainIntents(ctx context.Context, tx pgx.Tx, userID string, window time.Duration) (int64, error) {
	var n int64
	if err := s.q(tx).QueryRow(ctx, `
		SELECT count(*) FROM payments.payment_intents
		WHERE provider = $1 AND user_id = $2 AND created_at > now() - $3::interval AND status <> 'succeeded'`,
		chainProvider, userID, window.String()).Scan(&n); err != nil {
		return 0, fmt.Errorf("unpaid chain intents: %w", err)
	}
	return n, nil
}

// chainCent is a whole cent of USDT in micro-USDT. Manual USDT asks for whole
// cents, so a unique amount is never one: a manual payment to a shared
// address can never carry an automatic payment's amount by chance.
const chainCent = 10_000

// AllocateChainAmount returns base plus the smallest offset in 1..maxOffset
// that no on-chain intent to this address created within window has as its
// amount (and that is not a whole cent), so a transfer of exactly that
// amount can only be this payment's. It takes the lock that serialises
// allocations (LockChainAmounts); the caller inserts the intent before
// committing.
func (s *Store) AllocateChainAmount(ctx context.Context, tx pgx.Tx, address string, base, maxOffset int64, window time.Duration) (int64, error) {
	if err := s.LockChainAmounts(ctx, tx); err != nil {
		return 0, fmt.Errorf("allocate amount: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT gateway_amount FROM payments.payment_intents
		WHERE provider = $1 AND pay_address = $2 AND gateway_amount > $3 AND gateway_amount <= $3 + $4
		  AND created_at > now() - $5::interval`, chainProvider, address, base, maxOffset, window.String())
	if err != nil {
		return 0, fmt.Errorf("allocate amount: %w", err)
	}
	taken := map[int64]bool{}
	for rows.Next() {
		var a int64
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return 0, fmt.Errorf("allocate amount: %w", err)
		}
		taken[a] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("allocate amount: %w", err)
	}
	for off := int64(1); off <= maxOffset; off++ {
		if a := base + off; !taken[a] && a%chainCent != 0 {
			return a, nil
		}
	}
	return 0, ErrNoFreeAmount
}

// ChainCandidates locks the on-chain intents a transfer of exactly value to
// address at blockTS could pay: created at most window before it and at most
// skew after it (clocks differ). Unique amounts make it one at most, closed or
// not; the caller decides.
func (s *Store) ChainCandidates(ctx context.Context, tx pgx.Tx, address string, value int64, blockTS time.Time, skew, window time.Duration) ([]*Intent, error) {
	rows, err := tx.Query(ctx, `
		SELECT `+intentCols+` FROM payments.payment_intents
		WHERE provider = $1 AND pay_address = $2 AND gateway_currency = 'USDT' AND gateway_amount = $3
		  AND created_at <= $4::timestamptz + $5::interval AND created_at > $4::timestamptz - $6::interval
		ORDER BY created_at, id
		FOR UPDATE`, chainProvider, address, value, blockTS, skew.String(), window.String())
	if err != nil {
		return nil, fmt.Errorf("chain candidates: %w", err)
	}
	defer rows.Close()
	var out []*Intent
	for rows.Next() {
		in, err := scanIntent(rows)
		if err != nil {
			return nil, fmt.Errorf("chain candidates: %w", err)
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// ChainScanAddresses lists the addresses of on-chain intents a transfer may
// still settle: open, or closed but reopenable, created within window.
func (s *Store) ChainScanAddresses(ctx context.Context, window time.Duration) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT pay_address FROM payments.payment_intents
		WHERE provider = $1 AND pay_address IS NOT NULL AND created_at > now() - $2::interval
		  AND (status IN ('pending','confirming')
		    OR (status IN ('failed','expired') AND COALESCE(failure_reason, '') NOT IN ('amount_mismatch','reversed')))
		ORDER BY pay_address`, chainProvider, window.String())
	if err != nil {
		return nil, fmt.Errorf("chain scan addresses: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, fmt.Errorf("chain scan addresses: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DueChainExpiries lists an address's open on-chain intents whose pay window
// ended before before.
func (s *Store) DueChainExpiries(ctx context.Context, address string, before time.Time) ([]*Intent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+intentCols+` FROM payments.payment_intents
		WHERE provider = $1 AND pay_address = $2 AND status IN ('pending','confirming') AND expires_at < $3
		ORDER BY expires_at LIMIT 200`, chainProvider, address, before)
	if err != nil {
		return nil, fmt.Errorf("due chain expiries: %w", err)
	}
	defer rows.Close()
	var out []*Intent
	for rows.Next() {
		in, err := scanIntent(rows)
		if err != nil {
			return nil, fmt.Errorf("due chain expiries: %w", err)
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// OpenChainIntentsElsewhere counts open on-chain intents to pay to any
// address other than address (all of them when address is "").
func (s *Store) OpenChainIntentsElsewhere(ctx context.Context, tx pgx.Tx, address string) (int64, error) {
	var n int64
	if err := s.q(tx).QueryRow(ctx, `
		SELECT count(*) FROM payments.payment_intents
		WHERE provider = $1 AND status IN ('pending','confirming') AND pay_address IS DISTINCT FROM NULLIF($2, '')`,
		chainProvider, address).Scan(&n); err != nil {
		return 0, fmt.Errorf("open chain intents: %w", err)
	}
	return n, nil
}

// ChainCursor is how far the watcher has read an address.
type ChainCursor struct {
	Address   string
	LastTS    time.Time
	ScannedAt *time.Time
	OKAt      *time.Time
	LastError *string
}

// GetChainCursor returns an address's cursor (ErrNotFound if none).
func (s *Store) GetChainCursor(ctx context.Context, address string) (*ChainCursor, error) {
	var c ChainCursor
	err := s.db.QueryRow(ctx, `SELECT address, last_ts, scanned_at, ok_at, last_error FROM payments.chain_cursors
		WHERE address = $1`, address).Scan(&c.Address, &c.LastTS, &c.ScannedAt, &c.OKAt, &c.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chain cursor: %w", err)
	}
	return &c, nil
}

// EnsureChainCursor starts reading an address at from unless it is read
// already.
func (s *Store) EnsureChainCursor(ctx context.Context, tx pgx.Tx, address string, from time.Time) error {
	if _, err := s.q(tx).Exec(ctx, `INSERT INTO payments.chain_cursors (address, last_ts) VALUES ($1, $2)
		ON CONFLICT (address) DO NOTHING`, address, from); err != nil {
		return fmt.Errorf("ensure chain cursor: %w", err)
	}
	return nil
}

// SkipChainHistory moves an address's cursor up to from, unless a payment to
// it may still be settled: an address switched on again must not report the
// transfers it got while nobody watched as unmatched deposits.
func (s *Store) SkipChainHistory(ctx context.Context, tx pgx.Tx, address string, from time.Time, window time.Duration) error {
	if _, err := s.q(tx).Exec(ctx, `
		INSERT INTO payments.chain_cursors (address, last_ts) VALUES ($1, $2)
		ON CONFLICT (address) DO UPDATE SET last_ts = GREATEST(chain_cursors.last_ts, EXCLUDED.last_ts), updated_at = now()
		WHERE NOT EXISTS (SELECT 1 FROM payments.payment_intents
			WHERE provider = $3 AND pay_address = $1 AND created_at > now() - $4::interval
			  AND (status IN ('pending','confirming')
			    OR (status IN ('failed','expired') AND COALESCE(failure_reason, '') NOT IN ('amount_mismatch','reversed'))))`,
		address, from, chainProvider, window.String()); err != nil {
		return fmt.Errorf("skip chain history: %w", err)
	}
	return nil
}

// AdvanceChainCursor moves an address's cursor forward to ts (never back).
func (s *Store) AdvanceChainCursor(ctx context.Context, address string, ts time.Time) error {
	if _, err := s.db.Exec(ctx, `
		INSERT INTO payments.chain_cursors (address, last_ts) VALUES ($1, $2)
		ON CONFLICT (address) DO UPDATE SET last_ts = GREATEST(chain_cursors.last_ts, EXCLUDED.last_ts), updated_at = now()`,
		address, ts); err != nil {
		return fmt.Errorf("advance chain cursor: %w", err)
	}
	return nil
}

// TryChainScan claims a read of an address unless one started within gap
// (across every replica); false = read a moment ago.
func (s *Store) TryChainScan(ctx context.Context, address string, gap time.Duration) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE payments.chain_cursors SET scanned_at = now()
		WHERE address = $1 AND (scanned_at IS NULL OR scanned_at < now() - $2::interval)`, address, gap.String())
	if err != nil {
		return false, fmt.Errorf("try chain scan: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ChainScanDone records how a read of an address went (errText "" = it
// worked) and returns the error recorded before it.
func (s *Store) ChainScanDone(ctx context.Context, address, errText string) (*string, error) {
	var prev *string
	err := s.db.QueryRow(ctx, `
		WITH old AS (SELECT last_error FROM payments.chain_cursors WHERE address = $1)
		UPDATE payments.chain_cursors SET scanned_at = now(),
			ok_at = CASE WHEN $2 = '' THEN now() ELSE ok_at END,
			last_error = NULLIF($2, ''), updated_at = now()
		WHERE address = $1
		RETURNING (SELECT last_error FROM old)`, address, errText).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chain scan done: %w", err)
	}
	return prev, nil
}

// ResolveChainTransfer marks an unmatched (or ignored) transfer handled by
// staff. ErrInvalidTransition: it is not unmatched.
func (s *Store) ResolveChainTransfer(ctx context.Context, tx pgx.Tx, txid string, index int, actor *string, note string) (*ChainTransfer, error) {
	t, err := scanChainTransfer(s.q(tx).QueryRow(ctx, `
		UPDATE payments.chain_transfers
		SET outcome = 'resolved', resolved_by = $3, resolved_at = now(), note = NULLIF($4, ''), announce_at = NULL,
		    updated_at = now()
		WHERE txid = $1 AND event_index = $2 AND outcome IN ('unmatched','ignored')
		RETURNING `+chainTransferCols, txid, index, actor, note))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidTransition
	}
	if err != nil {
		return nil, fmt.Errorf("resolve chain transfer: %w", err)
	}
	return t, nil
}

// GetChainTransfer returns one recorded transfer.
func (s *Store) GetChainTransfer(ctx context.Context, txid string, index int) (*ChainTransfer, error) {
	t, err := scanChainTransfer(s.db.QueryRow(ctx, `SELECT `+chainTransferCols+` FROM payments.chain_transfers
		WHERE txid = $1 AND event_index = $2`, txid, index))
	if err != nil {
		return nil, fmt.Errorf("get chain transfer: %w", err)
	}
	return t, nil
}

// DueChainAnnouncements lists the unmatched deposits whose announcement is
// due at now, oldest first.
func (s *Store) DueChainAnnouncements(ctx context.Context, now time.Time, limit int) ([]ChainTransfer, error) {
	rows, err := s.db.Query(ctx, `SELECT `+chainTransferCols+` FROM payments.chain_transfers
		WHERE announce_at IS NOT NULL AND announce_at <= $1 AND outcome = 'unmatched'
		ORDER BY announce_at, id LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("due chain announcements: %w", err)
	}
	out, err := collectTransfers(rows)
	if err != nil {
		return nil, fmt.Errorf("due chain announcements: %w", err)
	}
	return out, nil
}

// TakeChainAnnouncement clears a due announcement of a transfer still
// unmatched and returns the transfer; nil when another read took it or a
// manual proof claimed it meanwhile.
func (s *Store) TakeChainAnnouncement(ctx context.Context, tx pgx.Tx, id int64, now time.Time) (*ChainTransfer, error) {
	t, err := scanChainTransfer(tx.QueryRow(ctx, `
		UPDATE payments.chain_transfers SET announce_at = NULL, updated_at = now()
		WHERE id = $1 AND outcome = 'unmatched' AND announce_at IS NOT NULL AND announce_at <= $2
		RETURNING `+chainTransferCols, id, now))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("take chain announcement: %w", err)
	}
	return t, nil
}

// ChainTransferFilter narrows the transfer log; empty fields match everything.
type ChainTransferFilter struct {
	Outcome  string
	TxID     string // normalised
	IntentID string
	Value    int64 // micro-USDT, exactly (0 = any)
}

// ListChainTransfers returns a page of the transfer log, newest first, and
// the number of matches.
func (s *Store) ListChainTransfers(ctx context.Context, f ChainTransferFilter, limit, offset int) ([]ChainTransfer, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	const where = `($1 = '' OR outcome = $1) AND ($2 = '' OR txid = $2) AND ($3 = '' OR intent_id::text = $3)
		AND ($4 = 0 OR value = $4)`
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM payments.chain_transfers WHERE `+where,
		f.Outcome, f.TxID, f.IntentID, f.Value).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list chain transfers: %w", err)
	}
	rows, err := s.db.Query(ctx, `SELECT `+chainTransferCols+` FROM payments.chain_transfers WHERE `+where+`
		ORDER BY block_ts DESC, id DESC LIMIT $5 OFFSET $6`, f.Outcome, f.TxID, f.IntentID, f.Value, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list chain transfers: %w", err)
	}
	out, err := collectTransfers(rows)
	if err != nil {
		return nil, 0, fmt.Errorf("list chain transfers: %w", err)
	}
	return out, total, nil
}

// ChainReport is what a USDT report adds: the deposits staff must still
// credit by hand, and how the watcher's reads go.
type ChainReport struct {
	// Unmatched: newest first, at most 50; not those a manual proof may still
	// claim (their announcement is pending).
	Unmatched      []ChainTransfer
	UnmatchedCount int64
	Cursor         *ChainCursor // the store's address; nil = never read
}

// ChainReport builds the USDT part of a report; address "" = no address set.
func (s *Store) ChainReport(ctx context.Context, address string) (*ChainReport, error) {
	r := &ChainReport{}
	const due = `outcome = 'unmatched' AND (announce_at IS NULL OR announce_at <= now())`
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM payments.chain_transfers WHERE `+due).
		Scan(&r.UnmatchedCount); err != nil {
		return nil, fmt.Errorf("chain report: %w", err)
	}
	rows, err := s.db.Query(ctx, `SELECT `+chainTransferCols+` FROM payments.chain_transfers
		WHERE `+due+` ORDER BY block_ts DESC, id DESC LIMIT 50`)
	if err != nil {
		return nil, fmt.Errorf("chain report: %w", err)
	}
	if r.Unmatched, err = collectTransfers(rows); err != nil {
		return nil, fmt.Errorf("chain report: %w", err)
	}
	if address != "" {
		c, err := s.GetChainCursor(ctx, address)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		r.Cursor = c
	}
	return r, nil
}
