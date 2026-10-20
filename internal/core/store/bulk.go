package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// Bulk purchases: N services of one plan bought and paid for together. The
// orders of a bulk are ordinary orders (store/orders.go); this file keeps
// the row that ties them together and the questions asked about the group.

const bulkCols = `id, user_id, plan_id, quantity, unit_amount, amount, currency, label_prefix, idempotency_key, created_at, delivered_at`

func scanBulk(row pgx.Row) (*Bulk, error) {
	var b Bulk
	err := row.Scan(&b.ID, &b.UserID, &b.PlanID, &b.Quantity, &b.UnitAmount, &b.Amount, &b.Currency,
		&b.LabelPrefix, &b.IdempotencyKey, &b.CreatedAt, &b.DeliveredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &b, err
}

// CreateBulk inserts a bulk and reports whether it is new. A retry with the
// same idempotency_key returns the existing row (inserted=false): a request
// that arrives while the first is still in its transaction waits for it, then
// finds it. The same key for another user, plan, quantity or name is
// ErrIdempotencyConflict rather than someone else's purchase.
func (s *Store) CreateBulk(ctx context.Context, q querier, b *Bulk) (*Bulk, bool, error) {
	b.ID = buuid.MustV7().String()
	var (
		got      Bulk
		inserted bool
	)
	// xmax = 0 only for a freshly inserted row (an ON CONFLICT update sets it).
	err := q.QueryRow(ctx, `
		INSERT INTO core.bulk_orders (id, user_id, plan_id, quantity, unit_amount, amount, currency, label_prefix, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (idempotency_key) DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
		RETURNING `+bulkCols+`, (xmax = 0)`,
		b.ID, b.UserID, b.PlanID, b.Quantity, b.UnitAmount, b.Amount, b.Currency, b.LabelPrefix, b.IdempotencyKey).
		Scan(&got.ID, &got.UserID, &got.PlanID, &got.Quantity, &got.UnitAmount, &got.Amount, &got.Currency,
			&got.LabelPrefix, &got.IdempotencyKey, &got.CreatedAt, &got.DeliveredAt, &inserted)
	if err != nil {
		return nil, false, fmt.Errorf("create bulk: %w", err)
	}
	if got.UserID != b.UserID || got.PlanID != b.PlanID || got.Quantity != b.Quantity || deref(got.LabelPrefix) != deref(b.LabelPrefix) {
		return nil, false, ErrIdempotencyConflict
	}
	return &got, inserted, nil
}

// GetBulk returns a bulk by id.
func (s *Store) GetBulk(ctx context.Context, q querier, id string) (*Bulk, error) {
	b, err := scanBulk(q.QueryRow(ctx, `SELECT `+bulkCols+` FROM core.bulk_orders WHERE id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("get bulk: %w", err)
	}
	return b, nil
}

// BulkByKey returns the bulk made with an idempotency key.
func (s *Store) BulkByKey(ctx context.Context, q querier, key string) (*Bulk, error) {
	b, err := scanBulk(q.QueryRow(ctx, `SELECT `+bulkCols+` FROM core.bulk_orders WHERE idempotency_key = $1`, key))
	if err != nil {
		return nil, fmt.Errorf("bulk by key: %w", err)
	}
	return b, nil
}

// LockBulk returns a bulk and locks its row until tx ends, so two workers
// never announce the same bulk twice.
func (s *Store) LockBulk(ctx context.Context, tx pgx.Tx, id string) (*Bulk, error) {
	b, err := scanBulk(tx.QueryRow(ctx, `SELECT `+bulkCols+` FROM core.bulk_orders WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return nil, fmt.Errorf("lock bulk: %w", err)
	}
	return b, nil
}

// BulkOrders lists the orders of a bulk, in their order in it.
func (s *Store) BulkOrders(ctx context.Context, q querier, bulkID string) ([]Order, error) {
	rows, err := q.Query(ctx, `SELECT `+orderCols+` FROM core.orders WHERE bulk_id = $1 ORDER BY bulk_index`, bulkID)
	if err != nil {
		return nil, fmt.Errorf("bulk orders: %w", err)
	}
	defer rows.Close()
	var out []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

// BulkService is one order of a bulk and the service made for it (zero
// Subscription fields until it exists).
type BulkService struct {
	Order
	SubscriptionID string
	SubStatus      string
	SubLink        string
	ExpiresAt      *time.Time
	TrafficTotal   *int64
	SubLabel       *string
}

// BulkServices lists the orders of a bulk with their services, in their
// order in it.
func (s *Store) BulkServices(ctx context.Context, q querier, bulkID string) ([]BulkService, error) {
	rows, err := q.Query(ctx, `
		SELECT `+orderColsO+`, COALESCE(sub.id::text, ''), COALESCE(sub.status, ''), COALESCE(sub.sub_link, ''),
			sub.expires_at, sub.traffic_total_bytes, sub.label
		FROM core.orders o
		LEFT JOIN LATERAL (SELECT id, status, sub_link, expires_at, traffic_total_bytes, label
			FROM core.subscriptions WHERE order_id = o.id ORDER BY created_at, id LIMIT 1) sub ON true
		WHERE o.bulk_id = $1
		ORDER BY o.bulk_index`, bulkID)
	if err != nil {
		return nil, fmt.Errorf("bulk services: %w", err)
	}
	defer rows.Close()
	var out []BulkService
	for rows.Next() {
		var bs BulkService
		dest := append(orderDest(&bs.Order), &bs.SubscriptionID, &bs.SubStatus, &bs.SubLink,
			&bs.ExpiresAt, &bs.TrafficTotal, &bs.SubLabel)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		out = append(out, bs)
	}
	return out, rows.Err()
}

// BulkProgress is how far the orders of a bulk have come.
type BulkProgress struct {
	// ReadyOrders are the orders delivered (active), in their order in the
	// bulk.
	ReadyOrders []string
	// Working counts the ones still on their way: paid and waiting, being
	// delivered, or failed fewer than the given number of attempts (they
	// are retried soon).
	Working int
	// Delayed counts the ones that failed that many attempts or more: the
	// provisioning machinery keeps retrying them, with growing waits.
	Delayed int
}

// BulkProgress sorts the orders of a bulk by how far they have come. An
// order that is none of these (cancelled by a refund, say) counts in none.
// delayedAfter is the number of failed attempts from which an order counts
// as delayed instead of working (the alert threshold of the provisioning
// worker).
func (s *Store) BulkProgress(ctx context.Context, q querier, bulkID string, delayedAfter int) (BulkProgress, error) {
	var p BulkProgress
	err := q.QueryRow(ctx, `
		SELECT COALESCE(array_agg(id::text ORDER BY bulk_index) FILTER (WHERE status = 'active'), '{}'),
			count(*) FILTER (WHERE status IN ('paid', 'provisioning') OR (status = 'provision_failed' AND provision_attempts < $2)),
			count(*) FILTER (WHERE status = 'provision_failed' AND provision_attempts >= $2)
		FROM core.orders WHERE bulk_id = $1`, bulkID, delayedAfter).Scan(&p.ReadyOrders, &p.Working, &p.Delayed)
	if err != nil {
		return BulkProgress{}, fmt.Errorf("bulk progress: %w", err)
	}
	return p, nil
}

// UndeliveredBulks lists the bulks whose one message with every link has not
// been announced yet, oldest first.
func (s *Store) UndeliveredBulks(ctx context.Context, q querier, limit int) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM core.bulk_orders WHERE delivered_at IS NULL ORDER BY created_at, id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("undelivered bulks: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// MarkBulkDelivered records that the bulk's message was announced; false when
// it was announced before.
func (s *Store) MarkBulkDelivered(ctx context.Context, q querier, id string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.bulk_orders SET delivered_at = now() WHERE id = $1 AND delivered_at IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("mark bulk delivered: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
