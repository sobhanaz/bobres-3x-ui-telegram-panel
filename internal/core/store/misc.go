package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

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

// ClaimInbox records that an event was handled. It returns false when the
// event was already claimed (a re-delivery), in which case the caller must not
// apply it again. Call it inside the transaction that applies the event.
func (s *Store) ClaimInbox(ctx context.Context, tx pgx.Tx, messageID string) (bool, error) {
	var claimed bool
	err := tx.QueryRow(ctx, `
		INSERT INTO core.inbox_core (message_id) VALUES ($1)
		ON CONFLICT (message_id) DO NOTHING RETURNING true`, messageID).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim inbox: %w", err)
	}
	return true, nil
}

// DeadLetter is an event that could not be applied.
type DeadLetter struct {
	Source  string
	EventID string
	Topic   string
	Payload []byte
	Error   string
}

// InsertDeadLetter stores a dead-lettered event (idempotent per source+event).
func (s *Store) InsertDeadLetter(ctx context.Context, q querier, d DeadLetter) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.dead_letters (id, source, event_id, topic, payload, error)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source, event_id) DO UPDATE SET error = EXCLUDED.error`,
		buuid.MustV7(), d.Source, d.EventID, d.Topic, string(d.Payload), d.Error)
	if err != nil {
		return fmt.Errorf("dead letter: %w", err)
	}
	return nil
}
