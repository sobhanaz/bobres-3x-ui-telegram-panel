package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// ErrNotFound is returned by all Get* methods when no row matches.
var ErrNotFound = errors.New("store: not found")

const userCols = `id, telegram_id, username, language, role, status, referred_by, created_at, updated_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.TelegramID, &u.Username, &u.Language, &u.Role,
		&u.Status, &u.ReferredBy, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// UpsertUser inserts or updates the user keyed by telegram_id. Language and
// username refresh; role/status are preserved on conflict.
func (s *Store) UpsertUser(ctx context.Context, q querier, telegramID int64, username, language, referredBy string) (*User, error) {
	if language == "" {
		language = "fa"
	}
	var ref *string
	if referredBy != "" {
		ref = &referredBy
	}
	row := q.QueryRow(ctx, `
		INSERT INTO core.users (id, telegram_id, username, language, referred_by)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5)
		ON CONFLICT (telegram_id) DO UPDATE
		SET username = EXCLUDED.username, language = EXCLUDED.language, updated_at = now()
		RETURNING `+userCols,
		buuid.MustV7(), telegramID, username, language, ref)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}
	return u, nil
}

// GetUserByTelegramID returns the user with the given Telegram id.
func (s *Store) GetUserByTelegramID(ctx context.Context, q querier, telegramID int64) (*User, error) {
	row := q.QueryRow(ctx, `SELECT `+userCols+` FROM core.users WHERE telegram_id = $1`, telegramID)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// GetUser returns the user by UUID.
func (s *Store) GetUser(ctx context.Context, q querier, id string) (*User, error) {
	row := q.QueryRow(ctx, `SELECT `+userCols+` FROM core.users WHERE id = $1`, id)
	u, err := scanUser(row)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// SetUserRole updates role and status (admin actions).
func (s *Store) SetUserRole(ctx context.Context, q querier, id, role, status string) error {
	tag, err := q.Exec(ctx,
		`UPDATE core.users SET role = $2, status = $3, updated_at = now() WHERE id = $1`,
		id, role, status)
	if err != nil {
		return fmt.Errorf("set user role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
