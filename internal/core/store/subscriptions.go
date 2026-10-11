package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

const subscriptionCols = `id, user_id, order_id, COALESCE(server_id::text, ''), client_email, sub_id, status,
	expires_at, traffic_total_bytes, COALESCE(traffic_used_bytes, 0), last_synced_at, COALESCE(sub_link, ''), created_at,
	notified_expiring_at, notified_low_traffic_at, notified_ended_at`

func scanSubscription(row pgx.Row) (*Subscription, error) {
	var sc Subscription
	err := row.Scan(&sc.ID, &sc.UserID, &sc.OrderID, &sc.ServerID, &sc.ClientEmail, &sc.SubID, &sc.Status,
		&sc.ExpiresAt, &sc.TrafficTotal, &sc.TrafficUsed, &sc.LastSyncedAt, &sc.SubLink, &sc.CreatedAt,
		&sc.NotifiedExpiringAt, &sc.NotifiedLowTrafficAt, &sc.NotifiedEndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &sc, err
}

// CreateSubscription inserts a subscription row (pending until provisioned).
func (s *Store) CreateSubscription(ctx context.Context, q querier, sub *Subscription) (*Subscription, error) {
	sub.ID = buuid.MustV7().String()
	if sub.Status == "" {
		sub.Status = "pending"
	}
	got, err := scanSubscription(q.QueryRow(ctx, `
		INSERT INTO core.subscriptions
			(id, user_id, order_id, server_id, client_email, sub_id, status,
			 expires_at, traffic_total_bytes, traffic_used_bytes)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9, $10)
		RETURNING `+subscriptionCols,
		sub.ID, sub.UserID, sub.OrderID, sub.ServerID, sub.ClientEmail,
		sub.SubID, sub.Status, sub.ExpiresAt, sub.TrafficTotal, sub.TrafficUsed))
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}
	return got, nil
}

// GetSubscription returns one subscription.
func (s *Store) GetSubscription(ctx context.Context, q querier, id string) (*Subscription, error) {
	sc, err := scanSubscription(q.QueryRow(ctx, `SELECT `+subscriptionCols+` FROM core.subscriptions WHERE id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	return sc, nil
}

// SubscriptionByOrder returns the subscription created for an order.
func (s *Store) SubscriptionByOrder(ctx context.Context, q querier, orderID string) (*Subscription, error) {
	sc, err := scanSubscription(q.QueryRow(ctx, `SELECT `+subscriptionCols+` FROM core.subscriptions WHERE order_id = $1`, orderID))
	if err != nil {
		return nil, fmt.Errorf("subscription by order: %w", err)
	}
	return sc, nil
}

// ListSubscriptions returns a user's subscriptions, newest first, without
// the ones deleted from the panel (the customer's own list).
func (s *Store) ListSubscriptions(ctx context.Context, q querier, userID string, limit int) ([]Subscription, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := q.Query(ctx, `SELECT `+subscriptionCols+` FROM core.subscriptions
		WHERE user_id = $1 AND status <> 'deleted' ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		sc, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, rows.Err()
}

// ActivateSubscription records the provisioned panel client.
func (s *Store) ActivateSubscription(ctx context.Context, q querier, id, serverID, xuiSubID, link string, expiresAt *time.Time, trafficTotal *int64) error {
	tag, err := q.Exec(ctx, `
		UPDATE core.subscriptions
		SET status = 'active', server_id = $2::uuid, sub_id = NULLIF($3, ''), sub_link = NULLIF($4, ''),
		    expires_at = $5, traffic_total_bytes = $6, last_synced_at = now(), updated_at = now()
		WHERE id = $1`, id, serverID, xuiSubID, link, expiresAt, trafficTotal)
	if err != nil {
		return fmt.Errorf("activate subscription: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LockSubscription returns a subscription and locks its row until tx ends, so
// a renewal and the usage sync never compute from each other's half-states.
func (s *Store) LockSubscription(ctx context.Context, tx pgx.Tx, id string) (*Subscription, error) {
	sc, err := scanSubscription(tx.QueryRow(ctx, `SELECT `+subscriptionCols+` FROM core.subscriptions WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, fmt.Errorf("lock subscription: %w", err)
	}
	return sc, nil
}

// ApplySubscriptionLimits records new limits after a renewal or top-up: the
// subscription is active again and its reminders start over.
func (s *Store) ApplySubscriptionLimits(ctx context.Context, q querier, id string, expiresAt *time.Time, trafficTotal *int64) error {
	tag, err := q.Exec(ctx, `
		UPDATE core.subscriptions
		SET status = 'active', expires_at = $2, traffic_total_bytes = $3,
		    notified_expiring_at = NULL, notified_low_traffic_at = NULL, notified_ended_at = NULL, updated_at = now()
		WHERE id = $1`, id, expiresAt, trafficTotal)
	if err != nil {
		return fmt.Errorf("apply subscription limits: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DueUsageSync lists provisioned subscriptions whose usage was last synced
// before olderThan (never synced first). Disabled ones are left alone.
func (s *Store) DueUsageSync(ctx context.Context, q querier, olderThan time.Time, limit int) ([]Subscription, error) {
	rows, err := q.Query(ctx, `SELECT `+subscriptionCols+` FROM core.subscriptions
		WHERE status IN ('active', 'expiring_soon', 'expired', 'depleted') AND server_id IS NOT NULL
		  AND (last_synced_at IS NULL OR last_synced_at < $1)
		ORDER BY last_synced_at NULLS FIRST LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("due usage sync: %w", err)
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		sc, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, rows.Err()
}

// SubscriptionSync is one usage-sync result for a subscription: its status,
// usage, limits and the full reminder state (nil = not reminded this period).
type SubscriptionSync struct {
	Status               string
	TrafficUsed          int64
	ExpiresAt            *time.Time
	TrafficTotal         *int64
	NotifiedExpiringAt   *time.Time
	NotifiedLowTrafficAt *time.Time
	NotifiedEndedAt      *time.Time
}

// RecordSync stores a usage-sync result.
func (s *Store) RecordSync(ctx context.Context, q querier, id string, r SubscriptionSync) error {
	tag, err := q.Exec(ctx, `
		UPDATE core.subscriptions
		SET status = $2, traffic_used_bytes = $3, expires_at = $4, traffic_total_bytes = $5, last_synced_at = now(),
		    notified_expiring_at = $6, notified_low_traffic_at = $7, notified_ended_at = $8, updated_at = now()
		WHERE id = $1`, id, r.Status, r.TrafficUsed, r.ExpiresAt, r.TrafficTotal,
		r.NotifiedExpiringAt, r.NotifiedLowTrafficAt, r.NotifiedEndedAt)
	if err != nil {
		return fmt.Errorf("record sync: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchSync marks a subscription as just looked at without new usage (the
// panel did not answer), so one failing client does not block the queue.
func (s *Store) TouchSync(ctx context.Context, q querier, id string) error {
	_, err := q.Exec(ctx, `UPDATE core.subscriptions SET last_synced_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("touch sync: %w", err)
	}
	return nil
}

// SetSubscriptionStatus updates a subscription's status.
func (s *Store) SetSubscriptionStatus(ctx context.Context, q querier, id, status string) error {
	tag, err := q.Exec(ctx,
		`UPDATE core.subscriptions SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	if err != nil {
		return fmt.Errorf("set subscription status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimDueOrder locks the next order that needs provisioning (a new client,
// or new limits for a renewal or top-up): paid, failed and due for a retry,
// or stuck in "provisioning" longer than staleAfter (a worker died mid-call;
// both kinds are idempotent, so retrying is safe).
// SKIP LOCKED lets several workers run without blocking each other.
func (s *Store) ClaimDueOrder(ctx context.Context, tx pgx.Tx, staleAfter time.Duration) (*Order, error) {
	o, err := scanOrder(tx.QueryRow(ctx, `
		SELECT `+orderCols+` FROM core.orders
		WHERE status = 'paid'
			OR (status = 'provision_failed' AND next_attempt_at <= now())
			OR (status = 'provisioning' AND updated_at < now() - make_interval(secs => $1))
		ORDER BY updated_at
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, staleAfter.Seconds()))
	if err != nil {
		return nil, fmt.Errorf("claim order: %w", err)
	}
	return o, nil
}

// MarkProvisioning moves a claimed order to provisioning and counts the attempt.
func (s *Store) MarkProvisioning(ctx context.Context, tx pgx.Tx, orderID string) (int, error) {
	var attempts int
	err := tx.QueryRow(ctx, `
		UPDATE core.orders SET status = 'provisioning', provision_attempts = provision_attempts + 1, updated_at = now()
		WHERE id = $1 RETURNING provision_attempts`, orderID).Scan(&attempts)
	if err != nil {
		return 0, fmt.Errorf("mark provisioning: %w", err)
	}
	return attempts, nil
}

// RecordProvisionFailure marks a provisioning attempt as failed and schedules
// the next one. It returns the attempt count.
func (s *Store) RecordProvisionFailure(ctx context.Context, q querier, orderID, reason string, next time.Time) (int, error) {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	var attempts int
	err := q.QueryRow(ctx, `
		UPDATE core.orders SET status = 'provision_failed', last_error = $2, next_attempt_at = $3, updated_at = now()
		WHERE id = $1 AND status = 'provisioning' RETURNING provision_attempts`, orderID, reason, next).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("record provision failure: %w", err)
	}
	return attempts, nil
}
