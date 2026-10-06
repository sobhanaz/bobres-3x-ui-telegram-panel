package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

const orderCols = `id, user_id, plan_id, type, status, amount, currency, idempotency_key, created_at, updated_at,
	subscription_id, discount_code, discount_amount, targets_set, target_expires_at, target_traffic_bytes`

func orderDest(o *Order) []any {
	return []any{&o.ID, &o.UserID, &o.PlanID, &o.Type, &o.Status,
		&o.Amount, &o.Currency, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt,
		&o.SubscriptionID, &o.DiscountCode, &o.DiscountAmount, &o.TargetsSet, &o.TargetExpiresAt, &o.TargetTrafficBytes}
}

func scanOrder(row pgx.Row) (*Order, error) {
	var o Order
	err := row.Scan(orderDest(&o)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &o, err
}

// CreateOrder inserts an order and reports whether it is new. A retry with the
// same idempotency_key returns the existing order (inserted=false); the same
// key for a different user/plan/type/subscription is ErrIdempotencyConflict
// rather than someone else's order.
func (s *Store) CreateOrder(ctx context.Context, q querier, o *Order) (*Order, bool, error) {
	o.ID = buuid.MustV7().String()
	var (
		got      Order
		inserted bool
	)
	// xmax = 0 only for a freshly inserted row (an ON CONFLICT update sets it).
	err := q.QueryRow(ctx, `
		INSERT INTO core.orders (id, user_id, plan_id, type, status, amount, currency, idempotency_key,
			subscription_id, discount_code, discount_amount)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING `+orderCols+`, (xmax = 0)`,
		o.ID, o.UserID, o.PlanID, o.Type, o.Status, o.Amount, o.Currency, o.IdempotencyKey,
		o.SubscriptionID, o.DiscountCode, o.DiscountAmount).
		Scan(append(orderDest(&got), &inserted)...)
	if err != nil {
		return nil, false, fmt.Errorf("create order: %w", err)
	}
	if got.UserID != o.UserID || got.PlanID != o.PlanID || got.Type != o.Type || deref(got.SubscriptionID) != deref(o.SubscriptionID) {
		return nil, false, ErrIdempotencyConflict
	}
	return &got, inserted, nil
}

// SetOrderTargets stores the limits a renewal or top-up applies, computed
// once so every retry sets the same values.
func (s *Store) SetOrderTargets(ctx context.Context, q querier, id string, expiresAt *time.Time, trafficBytes *int64) error {
	tag, err := q.Exec(ctx, `
		UPDATE core.orders SET targets_set = true, target_expires_at = $2, target_traffic_bytes = $3, updated_at = now()
		WHERE id = $1`, id, expiresAt, trafficBytes)
	if err != nil {
		return fmt.Errorf("set order targets: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// OrderByIdempotencyKey returns the order created with key.
func (s *Store) OrderByIdempotencyKey(ctx context.Context, q querier, key string) (*Order, error) {
	o, err := scanOrder(q.QueryRow(ctx, `SELECT `+orderCols+` FROM core.orders WHERE idempotency_key = $1`, key))
	if err != nil {
		return nil, fmt.Errorf("order by key: %w", err)
	}
	return o, nil
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

// LockOrder returns an order and locks its row until tx ends, so concurrent
// payments of one order serialize (the second one sees it already paid).
func (s *Store) LockOrder(ctx context.Context, tx pgx.Tx, id string) (*Order, error) {
	o, err := scanOrder(tx.QueryRow(ctx, `SELECT `+orderCols+` FROM core.orders WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, fmt.Errorf("lock order: %w", err)
	}
	return o, nil
}

// TransitionOrder moves an order to `to` only from one of `from`. It reports
// whether the row changed, so callers never overwrite a concurrent change.
func (s *Store) TransitionOrder(ctx context.Context, q querier, id string, from []string, to string) (bool, error) {
	tag, err := q.Exec(ctx,
		`UPDATE core.orders SET status = $3, updated_at = now() WHERE id = $1 AND status = ANY($2)`,
		id, from, to)
	if err != nil {
		return false, fmt.Errorf("transition order: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CountTrialOrders returns how many orders the user made on trial plans
// (one trial per Telegram id; users.telegram_id is unique).
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
