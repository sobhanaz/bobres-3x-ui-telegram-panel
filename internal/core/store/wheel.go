package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// WheelSpin mirrors core.wheel_spins (migration 00051): one customer's spin of one store-local day.
type WheelSpin struct {
	ID     string
	UserID string
	// Day is the store-local calendar day, "2026-10-28".
	Day string
	// Prize is the Toman won (0: the wheel stopped on no prize).
	Prize int64
	// LedgerEntryID is the credit that paid the prize (nil when there was none).
	LedgerEntryID *string
	// Roll and TotalWeight say what was drawn: Roll in [0, TotalWeight) out of the weights in force.
	Roll, TotalWeight int64
	CreatedAt         time.Time
}

const wheelSpinCols = `id, user_id, to_char(day, 'YYYY-MM-DD'), prize, ledger_entry_id, roll, total_weight, created_at`

func wheelSpinDest(w *WheelSpin) []any {
	return []any{&w.ID, &w.UserID, &w.Day, &w.Prize, &w.LedgerEntryID, &w.Roll, &w.TotalWeight, &w.CreatedAt}
}

// LockWheelDay takes a lock on one day of the wheel until tx ends, so the spins of a day are decided one after the other: each sees what the earlier
// ones paid, and the day's budget cannot be overspent by two at once. A transaction lock on a key of its own: it holds no row and cannot meet the
// locks of a wallet.
func (s *Store) LockWheelDay(ctx context.Context, tx pgx.Tx, day string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bobres:wheel-day:' || $1::text, 0))`, day); err != nil {
		return fmt.Errorf("lock wheel day: %w", err)
	}
	return nil
}

// WheelSpinOf is the spin a customer made on a day, or ErrNotFound.
func (s *Store) WheelSpinOf(ctx context.Context, q querier, userID, day string) (*WheelSpin, error) {
	var w WheelSpin
	err := q.QueryRow(ctx, `SELECT `+wheelSpinCols+` FROM core.wheel_spins WHERE user_id = $1 AND day = $2::date`, userID, day).Scan(wheelSpinDest(&w)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("wheel spin: %w", err)
	}
	return &w, nil
}

// InsertWheelSpin records a spin, in the transaction that paid its prize. A customer spins once a day: a second row for the day is
// ErrIdempotencyConflict (the caller holds the day's lock and looked first, so this is the books disagreeing, and the spin rolls back).
func (s *Store) InsertWheelSpin(ctx context.Context, q querier, w *WheelSpin) error {
	err := q.QueryRow(ctx, `INSERT INTO core.wheel_spins (id, user_id, day, prize, ledger_entry_id, roll, total_weight)
		VALUES ($1, $2, $3::date, $4, $5, $6, $7) RETURNING created_at`,
		w.ID, w.UserID, w.Day, w.Prize, w.LedgerEntryID, w.Roll, w.TotalWeight).Scan(&w.CreatedAt)
	if isUniqueViolation(err) {
		return ErrIdempotencyConflict
	}
	if err != nil {
		return fmt.Errorf("insert wheel spin: %w", err)
	}
	return nil
}

// WheelPaidOn is what a day's spins paid out so far, in Toman.
func (s *Store) WheelPaidOn(ctx context.Context, q querier, day string) (int64, error) {
	var paid int64
	if err := q.QueryRow(ctx, `SELECT coalesce(sum(prize), 0)::bigint FROM core.wheel_spins WHERE day = $1::date`, day).Scan(&paid); err != nil {
		return 0, fmt.Errorf("wheel paid: %w", err)
	}
	return paid, nil
}

// WheelDay is the spins of one day, for the owner's report.
type WheelDay struct {
	Day     string
	Spins   int64 // customers who spun
	Winners int64 // of them, those who won a prize
	Paid    int64 // Toman paid out
}

// WheelDays lists the days from..to (both included, "2026-10-01") that had a spin, newest first.
func (s *Store) WheelDays(ctx context.Context, q querier, from, to string) ([]WheelDay, error) {
	rows, err := q.Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), count(*), count(*) FILTER (WHERE prize > 0), coalesce(sum(prize), 0)::bigint
		FROM core.wheel_spins WHERE day BETWEEN $1::date AND $2::date GROUP BY day ORDER BY day DESC`, from, to)
	if err != nil {
		return nil, fmt.Errorf("wheel days: %w", err)
	}
	defer rows.Close()
	var out []WheelDay
	for rows.Next() {
		var d WheelDay
		if err := rows.Scan(&d.Day, &d.Spins, &d.Winners, &d.Paid); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountPaidOrders is how many orders a customer has paid for and kept: they cost something after any discount, are past payment (paid, being
// delivered, delivered or failed to deliver) and were not refunded. It is what the wheel asks of a customer before they may spin.
func (s *Store) CountPaidOrders(ctx context.Context, q querier, userID string) (int64, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT count(*) FROM core.orders
		WHERE user_id = $1 AND amount > 0 AND refunded_at IS NULL AND status IN ('paid', 'provisioning', 'active', 'provision_failed')`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count paid orders: %w", err)
	}
	return n, nil
}
