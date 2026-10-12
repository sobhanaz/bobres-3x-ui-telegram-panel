package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// InstallID is this install's id (core.install), made when the schema was
// created; it is made again here if the row is ever missing.
func (s *Store) InstallID(ctx context.Context, q querier) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id::text FROM core.install`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = q.QueryRow(ctx, `
			WITH made AS (INSERT INTO core.install DEFAULT VALUES ON CONFLICT DO NOTHING RETURNING id)
			SELECT id::text FROM made UNION ALL SELECT id::text FROM core.install LIMIT 1`).Scan(&id)
	}
	if err != nil {
		return "", fmt.Errorf("install id: %w", err)
	}
	return id, nil
}

// LicenseRow is the stored license token and who installed it.
type LicenseRow struct {
	Token     string
	UpdatedAt time.Time
	UpdatedBy *string // user id; nil when installed from the CLI
	// UpdatedByName is that user's Telegram username ("" when none).
	UpdatedByName string
}

// GetLicense returns the stored license token (ErrNotFound when none).
func (s *Store) GetLicense(ctx context.Context, q querier) (*LicenseRow, error) {
	var r LicenseRow
	err := q.QueryRow(ctx, `
		SELECT l.token, l.updated_at, l.updated_by::text, coalesce(u.username, '')
		FROM core.license l LEFT JOIN core.users u ON u.id = l.updated_by WHERE l.id = 1`).
		Scan(&r.Token, &r.UpdatedAt, &r.UpdatedBy, &r.UpdatedByName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get license: %w", err)
	}
	return &r, nil
}

// SetLicense stores the license token, replacing any other. updatedBy is the
// staff member's id, or nil from the CLI.
func (s *Store) SetLicense(ctx context.Context, q querier, token string, updatedBy *string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.license (id, token, updated_at, updated_by) VALUES (1, $1, now(), $2)
		ON CONFLICT (id) DO UPDATE SET token = EXCLUDED.token, updated_at = now(), updated_by = EXCLUDED.updated_by`,
		token, updatedBy)
	if err != nil {
		return fmt.Errorf("set license: %w", err)
	}
	return nil
}

// CountStaffSeats counts the staff a license's dashboard seats cover: admins
// and support (owners take no seat).
func (s *Store) CountStaffSeats(ctx context.Context, q querier) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.users WHERE role IN ('admin', 'support')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count staff seats: %w", err)
	}
	return n, nil
}

// CountActiveServices counts the services customers have now (active or
// about to expire), against a license's max_active_services.
func (s *Store) CountActiveServices(ctx context.Context, q querier) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.subscriptions WHERE status IN ('active', 'expiring_soon')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active services: %w", err)
	}
	return n, nil
}
