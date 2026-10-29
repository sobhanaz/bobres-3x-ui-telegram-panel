package store

// Automatic TON payments (ton_auto): the transfers the watcher saw arrive, how far it has read each address, and
// the queries that find and expire the payments a transfer may settle. A TON payment is found by its comment
// (pay_memo, unique for ever), not by its amount, so none of the unique-amount machinery of usdt_trc20_auto is
// here. The transfers come back as ChainTransfer, the shape of the USDT log, so the staff views are shared.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const tonProvider = "ton_auto"

// tonTransferCols is the ton_transfers row in ChainTransfer's order.
const tonTransferCols = `id, tx_hash, tx_lt, from_address, to_address, value, comment, block_ts, intent_id::text, outcome, reason,
	resolved_by::text, resolved_at, note, created_at`

func scanTONTransfer(row pgx.Row) (*ChainTransfer, error) {
	t := ChainTransfer{Network: "TON", Token: "TON"}
	var comment *string
	err := row.Scan(&t.ID, &t.TxID, &t.LT, &t.From, &t.To, &t.Value, &comment, &t.BlockTS, &t.IntentID, &t.Outcome, &t.Reason,
		&t.ResolvedBy, &t.ResolvedAt, &t.Note, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if comment != nil {
		t.Comment = *comment
	}
	return &t, err
}

func collectTON(rows pgx.Rows) ([]ChainTransfer, error) {
	defer rows.Close()
	var out []ChainTransfer
	for rows.Next() {
		t, err := scanTONTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// LockTONCreate takes the transaction-scoped lock that serialises the creation of TON payments (their comments,
// reuse and the per-customer cap).
func (s *Store) LockTONCreate(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('payments:ton-create', 0))`); err != nil {
		return fmt.Errorf("lock ton create: %w", err)
	}
	return nil
}

// LockTONTx serialises, until the transaction ends, everything that records one TON transaction by its hash.
func (s *Store) LockTONTx(ctx context.Context, tx pgx.Tx, hash string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('payments:ton-tx:' || $1, 0))`, hash); err != nil {
		return fmt.Errorf("lock ton tx: %w", err)
	}
	return nil
}

// TONMemoTaken reports whether a payment already has this comment.
func (s *Store) TONMemoTaken(ctx context.Context, tx pgx.Tx, memo string) (bool, error) {
	var ok bool
	if err := s.q(tx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payments.payment_intents WHERE provider = $1 AND pay_memo = $2)`,
		tonProvider, memo).Scan(&ok); err != nil {
		return false, fmt.Errorf("ton memo taken: %w", err)
	}
	return ok, nil
}

// ReusableTONIntent returns the customer's own TON payment still waiting to be paid that in asks for again under
// another idempotency key: the same order (or top-up), price, USDT amount (base, micro-USDT) and address, with at
// least minLeft of its window left. nil when there is none.
func (s *Store) ReusableTONIntent(ctx context.Context, tx pgx.Tx, in *Intent, address string, base int64, minLeft time.Duration) (*Intent, error) {
	got, err := scanIntent(tx.QueryRow(ctx, `
		SELECT `+intentCols+` FROM payments.payment_intents
		WHERE provider = $1 AND user_id = $2 AND order_id IS NOT DISTINCT FROM $3 AND amount = $4 AND currency = $5
		  AND pay_address = $6 AND quote_usdt = $7 AND status = 'pending' AND expires_at > now() + $8::interval
		ORDER BY created_at DESC LIMIT 1`,
		tonProvider, in.UserID, in.OrderID, in.Amount, in.Currency, address, base, minLeft.String()))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reusable ton intent: %w", err)
	}
	return got, nil
}

// UnpaidTONIntents counts a customer's TON payments created within window that are not paid.
func (s *Store) UnpaidTONIntents(ctx context.Context, tx pgx.Tx, userID string, window time.Duration) (int64, error) {
	var n int64
	if err := s.q(tx).QueryRow(ctx, `
		SELECT count(*) FROM payments.payment_intents
		WHERE provider = $1 AND user_id = $2 AND created_at > now() - $3::interval AND status <> 'succeeded'`,
		tonProvider, userID, window.String()).Scan(&n); err != nil {
		return 0, fmt.Errorf("unpaid ton intents: %w", err)
	}
	return n, nil
}

// TONIntentByMemo locks and returns the TON payment with this comment (ErrNotFound: none).
func (s *Store) TONIntentByMemo(ctx context.Context, tx pgx.Tx, memo string) (*Intent, error) {
	in, err := scanIntent(tx.QueryRow(ctx, `SELECT `+intentCols+` FROM payments.payment_intents
		WHERE provider = $1 AND pay_memo = $2 FOR UPDATE`, tonProvider, memo))
	if err != nil {
		return nil, err
	}
	return in, nil
}

// TONTransferRecorded reports whether this transaction is recorded already, under whatever logical time: a hash
// is one transaction.
func (s *Store) TONTransferRecorded(ctx context.Context, tx pgx.Tx, hash string) (bool, error) {
	var ok bool
	if err := s.q(tx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payments.ton_transfers WHERE tx_hash = $1)`,
		hash).Scan(&ok); err != nil {
		return false, fmt.Errorf("ton transfer recorded: %w", err)
	}
	return ok, nil
}

// InsertTONTransfer records a transfer once; false when it already was.
func (s *Store) InsertTONTransfer(ctx context.Context, tx pgx.Tx, t *ChainTransfer) (bool, error) {
	var comment *string
	if t.Comment != "" {
		comment = &t.Comment
	}
	tag, err := s.q(tx).Exec(ctx, `
		INSERT INTO payments.ton_transfers (tx_hash, tx_lt, from_address, to_address, value, comment, block_ts, intent_id, outcome, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (tx_hash, tx_lt) DO NOTHING`,
		t.TxID, t.LT, t.From, t.To, t.Value, comment, t.BlockTS, t.IntentID, t.Outcome, t.Reason)
	if err != nil {
		return false, fmt.Errorf("insert ton transfer: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// TONScanAddresses lists the addresses of TON payments a transfer may still settle: open, or closed but
// reopenable, created within window.
func (s *Store) TONScanAddresses(ctx context.Context, window time.Duration) ([]string, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT pay_address FROM payments.payment_intents
		WHERE provider = $1 AND pay_address IS NOT NULL AND created_at > now() - $2::interval
		  AND (status IN ('pending','confirming')
		    OR (status IN ('failed','expired') AND COALESCE(failure_reason, '') NOT IN ('amount_mismatch','reversed')))
		ORDER BY pay_address`, tonProvider, window.String())
	if err != nil {
		return nil, fmt.Errorf("ton scan addresses: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, fmt.Errorf("ton scan addresses: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DueTONExpiries lists an address's open TON payments whose pay window ended before before.
func (s *Store) DueTONExpiries(ctx context.Context, address string, before time.Time) ([]*Intent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+intentCols+` FROM payments.payment_intents
		WHERE provider = $1 AND pay_address = $2 AND status IN ('pending','confirming') AND expires_at < $3
		ORDER BY expires_at LIMIT 200`, tonProvider, address, before)
	if err != nil {
		return nil, fmt.Errorf("due ton expiries: %w", err)
	}
	defer rows.Close()
	var out []*Intent
	for rows.Next() {
		in, err := scanIntent(rows)
		if err != nil {
			return nil, fmt.Errorf("due ton expiries: %w", err)
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// OpenTONIntentsElsewhere counts open TON payments to pay to any address other than address (all of them when
// address is "").
func (s *Store) OpenTONIntentsElsewhere(ctx context.Context, tx pgx.Tx, address string) (int64, error) {
	var n int64
	if err := s.q(tx).QueryRow(ctx, `
		SELECT count(*) FROM payments.payment_intents
		WHERE provider = $1 AND status IN ('pending','confirming') AND pay_address IS DISTINCT FROM NULLIF($2, '')`,
		tonProvider, address).Scan(&n); err != nil {
		return 0, fmt.Errorf("open ton intents: %w", err)
	}
	return n, nil
}

// TONCursor is how far the watcher has read an address. FloorTS is the time before which no transaction is
// recorded.
type TONCursor struct {
	Address   string
	LastLT    int64
	FloorTS   time.Time
	ScannedAt *time.Time
	OKAt      *time.Time
	LastError *string
}

// GetTONCursor returns an address's cursor (ErrNotFound if none).
func (s *Store) GetTONCursor(ctx context.Context, address string) (*TONCursor, error) {
	var c TONCursor
	err := s.db.QueryRow(ctx, `SELECT address, last_lt, floor_ts, scanned_at, ok_at, last_error FROM payments.ton_cursors
		WHERE address = $1`, address).Scan(&c.Address, &c.LastLT, &c.FloorTS, &c.ScannedAt, &c.OKAt, &c.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ton cursor: %w", err)
	}
	return &c, nil
}

// EnsureTONCursor starts reading an address at floor unless it is read already.
func (s *Store) EnsureTONCursor(ctx context.Context, tx pgx.Tx, address string, floor time.Time) error {
	if _, err := s.q(tx).Exec(ctx, `INSERT INTO payments.ton_cursors (address, floor_ts) VALUES ($1, $2)
		ON CONFLICT (address) DO NOTHING`, address, floor); err != nil {
		return fmt.Errorf("ensure ton cursor: %w", err)
	}
	return nil
}

// SkipTONHistory moves an address's floor up to floor, unless a payment to it may still be settled: an address
// switched on again must not report the transfers it got while nobody watched as unmatched deposits. The reads
// skip every transaction older than the floor (and go on past them).
func (s *Store) SkipTONHistory(ctx context.Context, tx pgx.Tx, address string, floor time.Time, window time.Duration) error {
	if _, err := s.q(tx).Exec(ctx, `
		INSERT INTO payments.ton_cursors (address, floor_ts) VALUES ($1, $2)
		ON CONFLICT (address) DO UPDATE SET floor_ts = GREATEST(ton_cursors.floor_ts, EXCLUDED.floor_ts), updated_at = now()
		WHERE NOT EXISTS (SELECT 1 FROM payments.payment_intents
			WHERE provider = $3 AND pay_address = $1 AND created_at > now() - $4::interval
			  AND (status IN ('pending','confirming')
			    OR (status IN ('failed','expired') AND COALESCE(failure_reason, '') NOT IN ('amount_mismatch','reversed'))))`,
		address, floor, tonProvider, window.String()); err != nil {
		return fmt.Errorf("skip ton history: %w", err)
	}
	return nil
}

// AdvanceTONCursor moves an address's cursor forward to lt (never back).
func (s *Store) AdvanceTONCursor(ctx context.Context, address string, lt int64) error {
	if _, err := s.db.Exec(ctx, `UPDATE payments.ton_cursors SET last_lt = GREATEST(last_lt, $2), updated_at = now()
		WHERE address = $1`, address, lt); err != nil {
		return fmt.Errorf("advance ton cursor: %w", err)
	}
	return nil
}

// TryTONScan claims a read of an address unless one started within gap (across every replica).
func (s *Store) TryTONScan(ctx context.Context, address string, gap time.Duration) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE payments.ton_cursors SET scanned_at = now()
		WHERE address = $1 AND (scanned_at IS NULL OR scanned_at < now() - $2::interval)`, address, gap.String())
	if err != nil {
		return false, fmt.Errorf("try ton scan: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// TONScanDone records how a read of an address went (errText "" = it worked) and returns the error recorded
// before it.
func (s *Store) TONScanDone(ctx context.Context, address, errText string) (*string, error) {
	var prev *string
	err := s.db.QueryRow(ctx, `
		WITH old AS (SELECT last_error FROM payments.ton_cursors WHERE address = $1)
		UPDATE payments.ton_cursors SET scanned_at = now(),
			ok_at = CASE WHEN $2 = '' THEN now() ELSE ok_at END,
			last_error = NULLIF($2, ''), updated_at = now()
		WHERE address = $1
		RETURNING (SELECT last_error FROM old)`, address, errText).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ton scan done: %w", err)
	}
	return prev, nil
}

// ResolveTONTransfer marks an unmatched (or ignored) transfer handled by staff. ErrInvalidTransition: it is not
// unmatched.
func (s *Store) ResolveTONTransfer(ctx context.Context, tx pgx.Tx, hash string, actor *string, note string) (*ChainTransfer, error) {
	t, err := scanTONTransfer(s.q(tx).QueryRow(ctx, `
		UPDATE payments.ton_transfers
		SET outcome = 'resolved', resolved_by = $2, resolved_at = now(), note = NULLIF($3, ''), updated_at = now()
		WHERE tx_hash = $1 AND outcome IN ('unmatched','ignored')
		RETURNING `+tonTransferCols, hash, actor, note))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInvalidTransition
	}
	if err != nil {
		return nil, fmt.Errorf("resolve ton transfer: %w", err)
	}
	return t, nil
}

// GetTONTransfer returns one recorded transfer by its transaction hash.
func (s *Store) GetTONTransfer(ctx context.Context, hash string) (*ChainTransfer, error) {
	t, err := scanTONTransfer(s.db.QueryRow(ctx, `SELECT `+tonTransferCols+` FROM payments.ton_transfers WHERE tx_hash = $1`, hash))
	if err != nil {
		return nil, fmt.Errorf("get ton transfer: %w", err)
	}
	return t, nil
}

// ListTONTransfers returns a page of the TON transfer log, newest first, and the number of matches.
func (s *Store) ListTONTransfers(ctx context.Context, f ChainTransferFilter, limit, offset int) ([]ChainTransfer, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	const where = `($1 = '' OR outcome = $1) AND ($2 = '' OR tx_hash = $2) AND ($3 = '' OR intent_id::text = $3) AND ($4 = 0 OR value = $4)`
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM payments.ton_transfers WHERE `+where,
		f.Outcome, f.TxID, f.IntentID, f.Value).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list ton transfers: %w", err)
	}
	rows, err := s.db.Query(ctx, `SELECT `+tonTransferCols+` FROM payments.ton_transfers WHERE `+where+`
		ORDER BY block_ts DESC, id DESC LIMIT $5 OFFSET $6`, f.Outcome, f.TxID, f.IntentID, f.Value, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list ton transfers: %w", err)
	}
	out, err := collectTON(rows)
	if err != nil {
		return nil, 0, fmt.Errorf("list ton transfers: %w", err)
	}
	return out, total, nil
}

// TONReport builds the TON part of a report (the deposits staff must still credit by hand, and how the
// watcher's reads go), in the shape of the USDT one. address "" = no address set.
func (s *Store) TONReport(ctx context.Context, address string) (*ChainReport, error) {
	r := &ChainReport{}
	const due = `outcome = 'unmatched'`
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM payments.ton_transfers WHERE `+due).Scan(&r.UnmatchedCount); err != nil {
		return nil, fmt.Errorf("ton report: %w", err)
	}
	rows, err := s.db.Query(ctx, `SELECT `+tonTransferCols+` FROM payments.ton_transfers WHERE `+due+`
		ORDER BY block_ts DESC, id DESC LIMIT 50`)
	if err != nil {
		return nil, fmt.Errorf("ton report: %w", err)
	}
	if r.Unmatched, err = collectTON(rows); err != nil {
		return nil, fmt.Errorf("ton report: %w", err)
	}
	if address != "" {
		c, err := s.GetTONCursor(ctx, address)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if c != nil {
			r.Cursor = &ChainCursor{Address: c.Address, LastTS: c.FloorTS, ScannedAt: c.ScannedAt, OKAt: c.OKAt, LastError: c.LastError}
		}
	}
	return r, nil
}
