package store

import (
	"context"
	"fmt"
	"time"
)

// Undelivered orders: paid orders whose service could not be made or applied yet (status provision_failed), which the
// worker tries again on its own (docs/04-dashboard.md, "Undelivered orders").

// HoldFailedOrder puts off the next try of a failed delivery until until: staff are moving it to another server, and
// the worker must not try it on the old one meanwhile. It reports whether the order was waiting for a retry.
func (s *Store) HoldFailedOrder(ctx context.Context, q querier, id string, until time.Time) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.orders SET next_attempt_at = $2, updated_at = now()
		WHERE id = $1 AND status = 'provision_failed'`, id, until)
	if err != nil {
		return false, fmt.Errorf("hold order: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SetOrderServer records the server staff chose for a failed delivery and makes it due at once. It reports whether
// the order was waiting for a retry.
func (s *Store) SetOrderServer(ctx context.Context, q querier, id, serverID string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.orders SET server_id = $2, next_attempt_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'provision_failed'`, id, serverID)
	if err != nil {
		return false, fmt.Errorf("set order server: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RetryFailedOrders makes every failed delivery due at once and returns how many there were.
func (s *Store) RetryFailedOrders(ctx context.Context, q querier) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE core.orders SET next_attempt_at = now(), updated_at = now()
		WHERE status = 'provision_failed'`)
	if err != nil {
		return 0, fmt.Errorf("retry orders: %w", err)
	}
	return tag.RowsAffected(), nil
}
