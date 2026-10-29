package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// CashbackCredit mirrors core.cashback_credits (migration 00049): the cashback an order earned, what it was worked out on, and what a refund of
// the order took back. The credit itself is the ledger entry LedgerEntryID (kind cashback, key cashback:<order id>).
type CashbackCredit struct {
	OrderID, UserID, Currency string
	BaseAmount                int64
	PercentBP                 int64 // hundredths of a percent: 250 is 2.5%
	Amount                    int64
	Capped                    bool
	LedgerEntryID             string
	CreatedAt                 time.Time
	// ReversedAmount is how much a refund took back (nil: the order was not refunded); ReversalEntryID is the ledger entry that did (nil when
	// the wallet held nothing to take).
	ReversedAmount  *int64
	ReversedAt      *time.Time
	ReversalEntryID *string
}

const cashbackCols = `order_id, user_id, currency, base_amount, percent_bp, amount, capped, ledger_entry_id, created_at,
	reversed_amount, reversed_at, reversal_entry_id`

func cashbackDest(c *CashbackCredit) []any {
	return []any{&c.OrderID, &c.UserID, &c.Currency, &c.BaseAmount, &c.PercentBP, &c.Amount, &c.Capped, &c.LedgerEntryID, &c.CreatedAt,
		&c.ReversedAmount, &c.ReversedAt, &c.ReversalEntryID}
}

func scanCashback(row pgx.Row) (*CashbackCredit, error) {
	var c CashbackCredit
	err := row.Scan(cashbackDest(&c)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// InsertCashbackCredit records the cashback of an order, in the transaction that credited it. An order earns once: a second row for it is
// ErrIdempotencyConflict (the caller found the ledger key free but the order already earned: the books disagree).
func (s *Store) InsertCashbackCredit(ctx context.Context, q querier, c *CashbackCredit) error {
	err := q.QueryRow(ctx, `
		INSERT INTO core.cashback_credits (order_id, user_id, currency, base_amount, percent_bp, amount, capped, ledger_entry_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at`,
		c.OrderID, c.UserID, c.Currency, c.BaseAmount, c.PercentBP, c.Amount, c.Capped, c.LedgerEntryID).Scan(&c.CreatedAt)
	if isUniqueViolation(err) {
		return ErrIdempotencyConflict
	}
	if err != nil {
		return fmt.Errorf("insert cashback credit: %w", err)
	}
	return nil
}

// GetCashbackCredit is the cashback an order earned; ErrNotFound when it earned none.
func (s *Store) GetCashbackCredit(ctx context.Context, q querier, orderID string) (*CashbackCredit, error) {
	c, err := scanCashback(q.QueryRow(ctx, `SELECT `+cashbackCols+` FROM core.cashback_credits WHERE order_id = $1`, orderID))
	if err != nil {
		return nil, fmt.Errorf("get cashback credit: %w", err)
	}
	return c, nil
}

// LockCashbackCredit is GetCashbackCredit with the row locked until tx ends, so two reversals of one credit take turns.
func (s *Store) LockCashbackCredit(ctx context.Context, tx pgx.Tx, orderID string) (*CashbackCredit, error) {
	c, err := scanCashback(tx.QueryRow(ctx, `SELECT `+cashbackCols+` FROM core.cashback_credits WHERE order_id = $1 FOR UPDATE`, orderID))
	if err != nil {
		return nil, fmt.Errorf("lock cashback credit: %w", err)
	}
	return c, nil
}

// MarkCashbackReversed records what a refund took back (taken may be 0, with no entry) once: a credit that was reversed already is left as it
// is and false is returned.
func (s *Store) MarkCashbackReversed(ctx context.Context, tx pgx.Tx, orderID string, taken int64, entryID *string) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE core.cashback_credits SET reversed_amount = $2, reversed_at = now(), reversal_entry_id = $3
		WHERE order_id = $1 AND reversed_at IS NULL`, orderID, taken, entryID)
	if err != nil {
		return false, fmt.Errorf("mark cashback reversed: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CashbackOfOrders is the net cashback (credited less taken back) of each of the orders that earned any, by order id.
func (s *Store) CashbackOfOrders(ctx context.Context, q querier, orderIDs []string) (map[string]CashbackCredit, error) {
	out := map[string]CashbackCredit{}
	if len(orderIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+cashbackCols+` FROM core.cashback_credits WHERE order_id = ANY($1)`, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("cashback of orders: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c CashbackCredit
		if err := rows.Scan(cashbackDest(&c)...); err != nil {
			return nil, err
		}
		out[c.OrderID] = c
	}
	return out, rows.Err()
}

// CashbackEarned is what a customer has earned as cashback, less what refunds took back, per currency.
func (s *Store) CashbackEarned(ctx context.Context, q querier, userID string) (map[string]int64, error) {
	rows, err := q.Query(ctx, `SELECT currency, sum(amount - COALESCE(reversed_amount, 0))::bigint
		FROM core.cashback_credits WHERE user_id = $1 GROUP BY currency`, userID)
	if err != nil {
		return nil, fmt.Errorf("cashback earned: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			cur string
			sum int64
		)
		if err := rows.Scan(&cur, &sum); err != nil {
			return nil, err
		}
		out[cur] = sum
	}
	return out, rows.Err()
}

// BulkCashback is the cashback the orders of a bulk purchase earned so far, credited less taken back (one number: a bulk is paid in Toman).
func (s *Store) BulkCashback(ctx context.Context, q querier, bulkID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COALESCE(sum(c.amount - COALESCE(c.reversed_amount, 0)), 0)::bigint
		FROM core.cashback_credits c JOIN core.orders o ON o.id = c.order_id WHERE o.bulk_id = $1`, bulkID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("bulk cashback: %w", err)
	}
	return n, nil
}
