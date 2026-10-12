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

// How the license token in use came (core.license.source).
const (
	// LicenseManual: pasted in the dashboard or imported with the CLI.
	LicenseManual = "manual"
	// LicenseActivation: fetched from the vendor's license server with a
	// license key; only such a license is refreshed.
	LicenseActivation = "activation"
	// LicenseRefresh: renewed by the daily refresh (of an activated license).
	LicenseRefresh = "refresh"
)

// LicenseRow is the stored license token and who installed it.
type LicenseRow struct {
	Token     string
	UpdatedAt time.Time
	UpdatedBy *string // user id; nil when installed from the CLI
	// UpdatedByName is that user's Telegram username ("" when none).
	UpdatedByName string
	// Source is how the token in use came (LicenseManual, LicenseActivation
	// or LicenseRefresh).
	Source string
	// RefreshedAt is when the daily refresh installed it (nil unless Source
	// is LicenseRefresh).
	RefreshedAt *time.Time
}

// GetLicense returns the stored license token (ErrNotFound when none).
func (s *Store) GetLicense(ctx context.Context, q querier) (*LicenseRow, error) {
	var r LicenseRow
	err := q.QueryRow(ctx, `
		SELECT l.token, l.updated_at, l.updated_by::text, coalesce(u.username, ''), l.source, l.refreshed_at
		FROM core.license l LEFT JOIN core.users u ON u.id = l.updated_by WHERE l.id = 1`).
		Scan(&r.Token, &r.UpdatedAt, &r.UpdatedBy, &r.UpdatedByName, &r.Source, &r.RefreshedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get license: %w", err)
	}
	return &r, nil
}

// SetLicense stores the license token, replacing any other. updatedBy is the
// staff member's id, or nil from the CLI; source is how the token came
// (LicenseManual or LicenseActivation). It has not been refreshed.
func (s *Store) SetLicense(ctx context.Context, q querier, token string, updatedBy *string, source string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.license (id, token, updated_at, updated_by, source, refreshed_at) VALUES (1, $1, now(), $2, $3, NULL)
		ON CONFLICT (id) DO UPDATE SET token = EXCLUDED.token, updated_at = now(), updated_by = EXCLUDED.updated_by,
			source = EXCLUDED.source, refreshed_at = NULL`,
		token, updatedBy, source)
	if err != nil {
		return fmt.Errorf("set license: %w", err)
	}
	return nil
}

// RefreshLicense replaces the stored token old with its renewal (source
// LicenseRefresh) and notes when, keeping who installed the license. false:
// the stored token is no longer old (someone installed another meanwhile),
// nothing changed.
func (s *Store) RefreshLicense(ctx context.Context, q querier, old, token string) (bool, error) {
	tag, err := q.Exec(ctx, `
		UPDATE core.license SET token = $2, source = 'refresh', refreshed_at = now() WHERE id = 1 AND token = $1`, old, token)
	if err != nil {
		return false, fmt.Errorf("refresh license: %w", err)
	}
	return tag.RowsAffected() == 1, nil
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
