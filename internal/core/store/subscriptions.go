package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

const subscriptionCols = `id, user_id, order_id, COALESCE(server_id::text, ''), client_email, sub_id, status,
	expires_at, traffic_total_bytes, COALESCE(traffic_used_bytes, 0), last_synced_at, COALESCE(sub_link, ''), created_at,
	notified_expiring_at, notified_low_traffic_at, notified_ended_at, label, page_token, note`

func subscriptionDest(sc *Subscription) []any {
	return []any{&sc.ID, &sc.UserID, &sc.OrderID, &sc.ServerID, &sc.ClientEmail, &sc.SubID, &sc.Status,
		&sc.ExpiresAt, &sc.TrafficTotal, &sc.TrafficUsed, &sc.LastSyncedAt, &sc.SubLink, &sc.CreatedAt,
		&sc.NotifiedExpiringAt, &sc.NotifiedLowTrafficAt, &sc.NotifiedEndedAt, &sc.Label, &sc.PageToken, &sc.Note}
}

func scanSubscription(row pgx.Row) (*Subscription, error) {
	var sc Subscription
	err := row.Scan(subscriptionDest(&sc)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &sc, err
}

// CreateSubscription inserts a subscription row (pending until provisioned).
func (s *Store) CreateSubscription(ctx context.Context, q querier, sub *Subscription) (*Subscription, error) {
	sub.ID = buuid.MustV7().String()
	sub.PageToken = NewPageToken()
	if sub.Status == "" {
		sub.Status = "pending"
	}
	got, err := scanSubscription(q.QueryRow(ctx, `
		INSERT INTO core.subscriptions
			(id, user_id, order_id, server_id, client_email, sub_id, status,
			 expires_at, traffic_total_bytes, traffic_used_bytes, label, page_token)
		VALUES ($1, $2, $3, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+subscriptionCols,
		sub.ID, sub.UserID, sub.OrderID, sub.ServerID, sub.ClientEmail,
		sub.SubID, sub.Status, sub.ExpiresAt, sub.TrafficTotal, sub.TrafficUsed, sub.Label, sub.PageToken))
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

// ListUserSubscriptions is a page of a user's subscriptions (not the deleted
// ones), newest first, with the number of matches. Query matches part of the
// label (any case) or the end of the id (the bot shows its last 6 hex digits,
// "#" optional).
func (s *Store) ListUserSubscriptions(ctx context.Context, q querier, userID, query string, p Page) ([]Subscription, int64, error) {
	return s.listUserSubscriptions(ctx, q, userID, query, nil, p)
}

// ServiceBuckets groups the statuses of a delivered or pending service the way
// a reseller filters their list: every service is in exactly one bucket, so
// the counts add up. "expired" holds both the services whose time ran out and
// those whose traffic did; "paused" the ones turned off ("disabled").
var ServiceBuckets = map[string][]string{
	"active":        {"active"},
	"expiring_soon": {"expiring_soon"},
	"expired":       {"expired", "depleted"},
	"paused":        {"disabled"},
	"pending":       {"pending"},
}

// ListUserServices is ListUserSubscriptions narrowed to one bucket of
// ServiceBuckets ("" keeps every service); an unknown bucket matches nothing.
func (s *Store) ListUserServices(ctx context.Context, q querier, userID, query, bucket string, p Page) ([]Subscription, int64, error) {
	var statuses []string
	if bucket != "" {
		if statuses = ServiceBuckets[bucket]; statuses == nil {
			return nil, 0, nil
		}
	}
	return s.listUserSubscriptions(ctx, q, userID, query, statuses, p)
}

// userSubscriptionsWhere selects a user's services (not the deleted ones) that
// match a search and, when the status array is not empty, one of its statuses.
//
// A search is a name (part of the label or of the client's email), the end of the service number, a subscription link or page address
// ($6 is its secret: the subscription id or the page token) or a client config ($7 is its name, which holds the client's email).
const userSubscriptionsWhere = `WHERE user_id = $1 AND status <> 'deleted'
	  AND ($2 = '' OR lower(label) LIKE $3 ESCAPE '\' OR lower(client_email) LIKE $3 ESCAPE '\'
	       OR ($4 <> '' AND replace(id::text, '-', '') LIKE '%' || $4)
	       OR ($6 <> '' AND (sub_id = $6 OR page_token = $6
	            OR (COALESCE(sub_link, '') <> '' AND right(sub_link, length($6) + 1) = '/' || $6)))
	       OR ($7 <> '' AND client_email <> '' AND position(lower(client_email) in $7) > 0))
	  AND (cardinality($5::text[]) = 0 OR status = ANY($5::text[]))`

func (s *Store) listUserSubscriptions(ctx context.Context, q querier, userID, query string, statuses []string, p Page) ([]Subscription, int64, error) {
	p = p.clamp()
	query = strings.TrimPrefix(strings.TrimSpace(query), "#")
	secret, remark := serviceLinkTerms(query)
	_, _, like := search(query)
	if statuses == nil {
		statuses = []string{}
	}
	rows, err := q.Query(ctx, `SELECT `+subscriptionCols+`, count(*) OVER () FROM core.subscriptions `+userSubscriptionsWhere+`
		ORDER BY created_at DESC, id DESC
		LIMIT $8 OFFSET $9`, userID, query, like, idPart(query), statuses, secret, remark, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list user subscriptions: %w", err)
	}
	defer rows.Close()
	var (
		out   []Subscription
		total int64
	)
	for rows.Next() {
		var sc Subscription
		if err := rows.Scan(append(subscriptionDest(&sc), &total)...); err != nil {
			return nil, 0, err
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	if len(out) == 0 && p.Offset > 0 { // a page past the end: the total is still what matches
		if err := q.QueryRow(ctx, `SELECT count(*) FROM core.subscriptions `+userSubscriptionsWhere,
			userID, query, like, idPart(query), statuses, secret, remark).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count user subscriptions: %w", err)
		}
	}
	return out, total, nil
}

// SetSubscriptionLabel names a subscription (nil clears the name).
func (s *Store) SetSubscriptionLabel(ctx context.Context, q querier, id string, label *string) error {
	tag, err := q.Exec(ctx, `UPDATE core.subscriptions SET label = $2, updated_at = now() WHERE id = $1`, id, label)
	if err != nil {
		return fmt.Errorf("set subscription label: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSubscriptionNote sets a service's private note (nil clears it).
func (s *Store) SetSubscriptionNote(ctx context.Context, q querier, id string, note *string) error {
	tag, err := q.Exec(ctx, `UPDATE core.subscriptions SET note = $2, updated_at = now() WHERE id = $1`, id, note)
	if err != nil {
		return fmt.Errorf("set subscription note: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ServiceCounts counts a user's services (the deleted ones left out) by
// ServiceBuckets; Total is all of them.
type ServiceCounts struct {
	Total, Active, ExpiringSoon, Expired, Paused, Pending int64
}

// CountUserServices counts a user's services per bucket.
func (s *Store) CountUserServices(ctx context.Context, q querier, userID string) (ServiceCounts, error) {
	var c ServiceCounts
	err := q.QueryRow(ctx, `SELECT count(*),
			count(*) FILTER (WHERE status = 'active'),
			count(*) FILTER (WHERE status = 'expiring_soon'),
			count(*) FILTER (WHERE status IN ('expired', 'depleted')),
			count(*) FILTER (WHERE status = 'disabled'),
			count(*) FILTER (WHERE status = 'pending')
		FROM core.subscriptions WHERE user_id = $1 AND status <> 'deleted'`, userID).
		Scan(&c.Total, &c.Active, &c.ExpiringSoon, &c.Expired, &c.Paused, &c.Pending)
	if err != nil {
		return c, fmt.Errorf("count user services: %w", err)
	}
	return c, nil
}

// SubscriptionToggle is the latest pause or resume of a service in the audit
// log: Action is "subscription.disable" or "subscription.enable", ActorID who
// did it (nil: the system).
type SubscriptionToggle struct {
	Action  string
	ActorID *string
}

// LastSubscriptionToggle returns the latest pause or resume of a service;
// ErrNotFound when it was never paused or resumed.
func (s *Store) LastSubscriptionToggle(ctx context.Context, q querier, id string) (*SubscriptionToggle, error) {
	var t SubscriptionToggle
	err := q.QueryRow(ctx, `SELECT action, actor_id::text FROM core.audit_log
		WHERE entity = 'subscription' AND entity_id = $1 AND action IN ('subscription.disable', 'subscription.enable')
		ORDER BY created_at DESC, id DESC LIMIT 1`, id).Scan(&t.Action, &t.ActorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("last subscription toggle: %w", err)
	}
	return &t, nil
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

// MarkSubLinksStale flags the stored links of a server's services (those on
// the panel) to be fetched again now, with no failed fetch counted, and
// returns how many.
func (s *Store) MarkSubLinksStale(ctx context.Context, q querier, serverID string) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE core.subscriptions SET sub_link_stale_at = now(), sub_link_attempts = 0
		WHERE server_id = $1::uuid AND status NOT IN ('pending', 'deleted')`, serverID)
	if err != nil {
		return 0, fmt.Errorf("mark sub links stale: %w", err)
	}
	return tag.RowsAffected(), nil
}

// StaleSubLink is a service whose stored link must be fetched again.
type StaleSubLink struct {
	ID       string
	ServerID string
	StaleAt  time.Time
	Attempts int // fetches the panel did not answer so far
}

// StaleSubLinks lists services whose link is due to be fetched again,
// longest waiting first (one put back waits until its next try).
func (s *Store) StaleSubLinks(ctx context.Context, q querier, limit int) ([]StaleSubLink, error) {
	rows, err := q.Query(ctx, `SELECT id, COALESCE(server_id::text, ''), sub_link_stale_at, sub_link_attempts
		FROM core.subscriptions WHERE sub_link_stale_at IS NOT NULL AND sub_link_stale_at <= now()
		ORDER BY sub_link_stale_at, id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("stale sub links: %w", err)
	}
	defer rows.Close()
	var out []StaleSubLink
	for rows.Next() {
		var l StaleSubLink
		if err := rows.Scan(&l.ID, &l.ServerID, &l.StaleAt, &l.Attempts); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RefreshSubLink stores a service's fetched link ("" keeps the old one) and
// clears the flag, unless the service was flagged again after staleAt (the
// link may predate that change). It reports whether it stored anything.
func (s *Store) RefreshSubLink(ctx context.Context, q querier, id, link string, staleAt time.Time) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.subscriptions
		SET sub_link = COALESCE(NULLIF($2, ''), sub_link), sub_link_stale_at = NULL, sub_link_attempts = 0, updated_at = now()
		WHERE id = $1 AND sub_link_stale_at = $3`, id, link, staleAt)
	if err != nil {
		return false, fmt.Errorf("refresh sub link: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// PostponeSubLink puts a flagged link back to be fetched after wait,
// counting a failed fetch when failed, unless the service was flagged again
// after staleAt (that new flag is due now). It reports whether it did.
func (s *Store) PostponeSubLink(ctx context.Context, q querier, id string, staleAt time.Time, wait time.Duration, failed bool) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.subscriptions
		SET sub_link_stale_at = now() + make_interval(secs => $3),
		    sub_link_attempts = sub_link_attempts + CASE WHEN $4 THEN 1 ELSE 0 END
		WHERE id = $1 AND sub_link_stale_at = $2`, id, staleAt, wait.Seconds(), failed)
	if err != nil {
		return false, fmt.Errorf("postpone sub link: %w", err)
	}
	return tag.RowsAffected() == 1, nil
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
