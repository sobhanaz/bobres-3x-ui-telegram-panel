package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const discountCols = `code, percent, amount, currency, max_uses, used_count, expires_at, enabled`

func scanDiscount(row pgx.Row) (*Discount, error) {
	var d Discount
	err := row.Scan(&d.Code, &d.Percent, &d.Amount, &d.Currency, &d.MaxUses, &d.Used, &d.ExpiresAt, &d.Enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &d, err
}

// GetDiscount returns a discount code (codes are stored upper case).
func (s *Store) GetDiscount(ctx context.Context, q querier, code string) (*Discount, error) {
	d, err := scanDiscount(q.QueryRow(ctx, `SELECT `+discountCols+` FROM core.discount_codes WHERE code = $1`, code))
	if err != nil {
		return nil, fmt.Errorf("get discount: %w", err)
	}
	return d, nil
}

// UpsertDiscount creates a code or replaces its terms; the use count stays.
func (s *Store) UpsertDiscount(ctx context.Context, q querier, d *Discount) (*Discount, error) {
	got, err := scanDiscount(q.QueryRow(ctx, `
		INSERT INTO core.discount_codes (code, percent, amount, currency, max_uses, expires_at, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (code) DO UPDATE SET percent = EXCLUDED.percent, amount = EXCLUDED.amount,
			currency = EXCLUDED.currency, max_uses = EXCLUDED.max_uses, expires_at = EXCLUDED.expires_at,
			enabled = EXCLUDED.enabled, updated_at = now()
		RETURNING `+discountCols, d.Code, d.Percent, d.Amount, d.Currency, d.MaxUses, d.ExpiresAt, d.Enabled))
	if err != nil {
		return nil, fmt.Errorf("upsert discount: %w", err)
	}
	return got, nil
}

// ListDiscounts returns every code, newest first.
func (s *Store) ListDiscounts(ctx context.Context, q querier) ([]Discount, error) {
	rows, err := q.Query(ctx, `SELECT `+discountCols+` FROM core.discount_codes ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		return nil, fmt.Errorf("list discounts: %w", err)
	}
	defer rows.Close()
	var out []Discount
	for rows.Next() {
		d, err := scanDiscount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// HasRedeemed reports whether the user already used a code on a paid order.
func (s *Store) HasRedeemed(ctx context.Context, q querier, code, userID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.discount_redemptions WHERE code = $1 AND user_id = $2)`,
		code, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("has redeemed: %w", err)
	}
	return ok, nil
}

// RecordRedemption counts a code's use by a paid order, once per order and
// per customer. It reports whether this order's use was recorded (false when
// the customer had already used the code on another order: the price they
// paid stands either way).
func (s *Store) RecordRedemption(ctx context.Context, q querier, code, userID, orderID string) (bool, error) {
	tag, err := q.Exec(ctx, `INSERT INTO core.discount_redemptions (code, user_id, order_id) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, code, userID, orderID)
	if err != nil {
		return false, fmt.Errorf("record redemption: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := q.Exec(ctx, `UPDATE core.discount_codes SET used_count = used_count + 1, updated_at = now() WHERE code = $1`, code); err != nil {
		return false, fmt.Errorf("count redemption: %w", err)
	}
	return true, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
