package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// CreateSubscription inserts a subscription row in pending state.
func (s *Store) CreateSubscription(ctx context.Context, q querier, sub *Subscription) (*Subscription, error) {
	sub.ID = buuid.MustV7().String()
	err := q.QueryRow(ctx, `
		INSERT INTO core.subscriptions
			(id, user_id, order_id, server_id, client_email, sub_id, status,
			 expires_at, traffic_total_bytes, traffic_used_bytes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING status`,
		sub.ID, sub.UserID, sub.OrderID, sub.ServerID, sub.ClientEmail,
		sub.SubID, sub.Status, sub.ExpiresAt, sub.TrafficTotal, sub.TrafficUsed).
		Scan(&sub.Status)
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}
	return sub, nil
}

// ListSubscriptions returns a user's subscriptions, newest first.
func (s *Store) ListSubscriptions(ctx context.Context, q querier, userID string, limit int) ([]Subscription, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := q.Query(ctx, `
		SELECT id, user_id, order_id, server_id, client_email, sub_id, status,
			expires_at, traffic_total_bytes, traffic_used_bytes, last_synced_at
		FROM core.subscriptions WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var sc Subscription
		if err := rows.Scan(&sc.ID, &sc.UserID, &sc.OrderID, &sc.ServerID,
			&sc.ClientEmail, &sc.SubID, &sc.Status, &sc.ExpiresAt,
			&sc.TrafficTotal, &sc.TrafficUsed, &sc.LastSyncedAt); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
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

// CreateTicket inserts a support ticket.
func (s *Store) CreateTicket(ctx context.Context, q querier, t *Ticket) (*Ticket, error) {
	t.ID = buuid.MustV7().String()
	t.Status = "open"
	err := q.QueryRow(ctx, `
		INSERT INTO core.tickets (id, user_id, category, text, status)
		VALUES ($1, $2, $3, $4, $5) RETURNING created_at`,
		t.ID, t.UserID, t.Category, t.Text, t.Status).Scan(&t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create ticket: %w", err)
	}
	return t, nil
}

// GetSettings returns all settings flattened as key->raw JSON.
func (s *Store) GetSettings(ctx context.Context, q querier) (map[string]json.RawMessage, error) {
	rows, err := q.Query(ctx, `SELECT key, value FROM core.settings`)
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", err)
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k string
		var v json.RawMessage
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetSetting upserts one setting value.
func (s *Store) SetSetting(ctx context.Context, q querier, key string, value json.RawMessage) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		key, value)
	if err != nil {
		return fmt.Errorf("set setting: %w", err)
	}
	return nil
}

// WriteAudit appends an audit_log row. Every sensitive write calls this with actor+reason.
func (s *Store) WriteAudit(ctx context.Context, q querier, a *Audit) error {
	a.ID = buuid.MustV7().String()
	_, err := q.Exec(ctx, `
		INSERT INTO core.audit_log (id, actor_id, action, entity, entity_id, before, after, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		a.ID, a.ActorID, a.Action, a.Entity, a.EntityID, a.Before, a.After, a.Reason)
	if err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	return nil
}

// CreateStaff inserts a staff row.
func (s *Store) CreateStaff(ctx context.Context, q querier, st *Staff) error {
	st.ID = buuid.MustV7().String()
	_, err := q.Exec(ctx, `
		INSERT INTO core.staff (id, username, password_hash, totp_secret_enc, role, status)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		st.ID, st.Username, st.PasswordHash, st.TOTPSecretEnc, st.Role, st.Status)
	if err != nil {
		return fmt.Errorf("create staff: %w", err)
	}
	return nil
}

// GetStaffByUsername returns a staff row for login.
func (s *Store) GetStaffByUsername(ctx context.Context, q querier, username string) (*Staff, error) {
	var st Staff
	err := q.QueryRow(ctx, `
		SELECT id, username, password_hash, totp_secret_enc, role, status
		FROM core.staff WHERE username = $1`, username).
		Scan(&st.ID, &st.Username, &st.PasswordHash, &st.TOTPSecretEnc, &st.Role, &st.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get staff: %w", err)
	}
	return &st, nil
}
