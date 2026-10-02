package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

const orderCols = `id, user_id, plan_id, type, status, amount, currency, idempotency_key, created_at, updated_at`

func scanOrder(row pgx.Row) (*Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.UserID, &o.PlanID, &o.Type, &o.Status,
		&o.Amount, &o.Currency, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &o, err
}

// CreateOrder inserts an order, returning the existing row when the
// idempotency_key was already used (safe retry).
func (s *Store) CreateOrder(ctx context.Context, q querier, o *Order) (*Order, error) {
	o.ID = buuid.MustV7().String()
	row := q.QueryRow(ctx, `
		INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING `+orderCols,
		o.ID, o.UserID, o.PlanID, o.Type, o.Status, o.Amount, o.Currency, o.IdempotencyKey)
	got, err := scanOrder(row)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	return got, nil
}

// GetOrder returns an order by id.
func (s *Store) GetOrder(ctx context.Context, q querier, id string) (*Order, error) {
	row := q.QueryRow(ctx, `SELECT `+orderCols+` FROM core.orders WHERE id = $1`, id)
	o, err := scanOrder(row)
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	return o, nil
}

// SetOrderStatus updates status. Returns ErrNotFound when the id is unknown.
func (s *Store) SetOrderStatus(ctx context.Context, q querier, id, status string) error {
	tag, err := q.Exec(ctx,
		`UPDATE core.orders SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("set order status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountTrialOrdersByTelegramID returns how many orders the user made on
// trial plans (one-trial-per-Telegram-ID check).
func (s *Store) CountTrialOrders(ctx context.Context, q querier, userID string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `
		SELECT count(*) FROM core.orders o
		JOIN core.plans p ON p.id = o.plan_id
		WHERE o.user_id = $1 AND p.is_trial`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count trial orders: %w", err)
	}
	return n, nil
}
