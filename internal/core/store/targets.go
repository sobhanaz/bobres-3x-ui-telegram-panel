package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// TargetBonus mirrors core.reseller_target_bonuses (migration 00050): the bonus a reseller was paid for reaching a tier of a month's sales target.
type TargetBonus struct {
	UserID        string
	Period        string // yyyy-mm, in the calendar the owner chose
	Threshold     int64  // the tier: how many sales it asks for
	Amount        int64
	Currency      string
	Sales         int64 // how many sales the reseller had when they reached it
	LedgerEntryID string
	CreatedAt     time.Time
}

// LockResellerTargets takes a lock on one reseller's targets until tx ends, so two deliveries of one reseller's services count and pay one after
// the other (a transaction lock on a key of its own: it holds no row and cannot meet the locks of the wallet or of the reseller's terms).
func (s *Store) LockResellerTargets(ctx context.Context, tx pgx.Tx, userID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bobres:reseller-targets:' || $1::text, 0))`, userID); err != nil {
		return fmt.Errorf("lock reseller targets: %w", err)
	}
	return nil
}

// InsertResellerSale records a delivered service as a sale of a month. It reports whether the sale is new: an order is one sale however often its
// delivery is retried.
func (s *Store) InsertResellerSale(ctx context.Context, q querier, orderID, userID, period string) (bool, error) {
	tag, err := q.Exec(ctx, `INSERT INTO core.reseller_sales (order_id, user_id, period) VALUES ($1, $2, $3) ON CONFLICT (order_id) DO NOTHING`,
		orderID, userID, period)
	if err != nil {
		return false, fmt.Errorf("insert reseller sale: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CountResellerSales is the sales of a reseller in a month that count: the orders that were not refunded.
func (s *Store) CountResellerSales(ctx context.Context, q querier, userID, period string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT count(*) FROM core.reseller_sales r JOIN core.orders o ON o.id = r.order_id
		WHERE r.user_id = $1 AND r.period = $2 AND o.refunded_at IS NULL`, userID, period).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count reseller sales: %w", err)
	}
	return n, nil
}

const targetBonusCols = `user_id, period, threshold, amount, currency, sales, ledger_entry_id, created_at`

func targetBonusDest(b *TargetBonus) []any {
	return []any{&b.UserID, &b.Period, &b.Threshold, &b.Amount, &b.Currency, &b.Sales, &b.LedgerEntryID, &b.CreatedAt}
}

// TargetBonuses lists the bonuses a reseller was paid for a month, lowest tier first.
func (s *Store) TargetBonuses(ctx context.Context, q querier, userID, period string) ([]TargetBonus, error) {
	rows, err := q.Query(ctx, `SELECT `+targetBonusCols+` FROM core.reseller_target_bonuses WHERE user_id = $1 AND period = $2 ORDER BY threshold`, userID, period)
	if err != nil {
		return nil, fmt.Errorf("target bonuses: %w", err)
	}
	defer rows.Close()
	var out []TargetBonus
	for rows.Next() {
		var b TargetBonus
		if err := rows.Scan(targetBonusDest(&b)...); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// InsertTargetBonus records a bonus, in the transaction that credited it. A reseller is paid a tier once a month: a second row for it is
// ErrIdempotencyConflict (the books disagree, and the payment rolls back).
func (s *Store) InsertTargetBonus(ctx context.Context, q querier, b *TargetBonus) error {
	err := q.QueryRow(ctx, `INSERT INTO core.reseller_target_bonuses (user_id, period, threshold, amount, currency, sales, ledger_entry_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING created_at`,
		b.UserID, b.Period, b.Threshold, b.Amount, b.Currency, b.Sales, b.LedgerEntryID).Scan(&b.CreatedAt)
	if isUniqueViolation(err) {
		return ErrIdempotencyConflict
	}
	if err != nil {
		return fmt.Errorf("insert target bonus: %w", err)
	}
	return nil
}

// TargetBonusPaid reports whether a reseller was paid a tier for a month.
func (s *Store) TargetBonusPaid(ctx context.Context, q querier, userID, period string, threshold int64) (bool, error) {
	var one int
	err := q.QueryRow(ctx, `SELECT 1 FROM core.reseller_target_bonuses WHERE user_id = $1 AND period = $2 AND threshold = $3`, userID, period, threshold).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("target bonus paid: %w", err)
	}
	return true, nil
}

// TargetBonusTotal is what a reseller was paid in bonuses for a month, per currency.
func (s *Store) TargetBonusTotal(ctx context.Context, q querier, userID, period string) (map[string]int64, error) {
	rows, err := q.Query(ctx, `SELECT currency, sum(amount)::bigint FROM core.reseller_target_bonuses WHERE user_id = $1 AND period = $2 GROUP BY currency`, userID, period)
	if err != nil {
		return nil, fmt.Errorf("target bonus total: %w", err)
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
