package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	buuid "github.com/sobhanaz/bobres-3x-ui-telegram-panel/internal/uuid"
)

// The register of hosted stores (migration 00055): the customers' stores the owner installed and runs for them, and the last reading of each
// store's /metrics. The token of that endpoint is kept encrypted; the customer's bot token and backup key are never here.

// ErrHostedDomainTaken: another hosted store has this domain.
var ErrHostedDomainTaken = errors.New("store: a hosted store has this domain already")

// HostedStore mirrors core.hosted_stores.
type HostedStore struct {
	ID              string
	Customer        string
	Domain          string
	Version         string
	LicenseID       string
	Channel         string
	Notes           string
	MetricsTokenEnc []byte
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CheckedAt       *time.Time
	Up              *bool
	LastVersion     string
	Figures         json.RawMessage
	LastError       string
}

const hostedCols = `id, customer, domain, version, license_id, channel, notes, metrics_token_enc, created_at, updated_at, checked_at, up,
	last_version, figures, last_error`

func scanHosted(row pgx.Row) (*HostedStore, error) {
	var h HostedStore
	err := row.Scan(&h.ID, &h.Customer, &h.Domain, &h.Version, &h.LicenseID, &h.Channel, &h.Notes, &h.MetricsTokenEnc, &h.CreatedAt, &h.UpdatedAt,
		&h.CheckedAt, &h.Up, &h.LastVersion, &h.Figures, &h.LastError)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("hosted store: %w", err)
	}
	return &h, nil
}

func hostedErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "hosted_stores_domain_key" {
		return ErrHostedDomainTaken
	}
	return err
}

// CreateHostedStore registers a store (its ID is set here).
func (s *Store) CreateHostedStore(ctx context.Context, q querier, h *HostedStore) error {
	h.ID = buuid.MustV7().String()
	err := q.QueryRow(ctx, `
		INSERT INTO core.hosted_stores (id, customer, domain, version, license_id, channel, notes, metrics_token_enc)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING created_at, updated_at`,
		h.ID, h.Customer, h.Domain, h.Version, h.LicenseID, h.Channel, h.Notes, h.MetricsTokenEnc).Scan(&h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create hosted store: %w", hostedErr(err))
	}
	return nil
}

// GetHostedStore returns a hosted store by id.
func (s *Store) GetHostedStore(ctx context.Context, q querier, id string) (*HostedStore, error) {
	return scanHosted(q.QueryRow(ctx, `SELECT `+hostedCols+` FROM core.hosted_stores WHERE id = $1`, id))
}

// ListHostedStores lists every hosted store, newest first (the register is small: at most 500).
func (s *Store) ListHostedStores(ctx context.Context, q querier) ([]HostedStore, error) {
	rows, err := q.Query(ctx, `SELECT `+hostedCols+` FROM core.hosted_stores ORDER BY created_at DESC, id DESC LIMIT 500`)
	if err != nil {
		return nil, fmt.Errorf("list hosted stores: %w", err)
	}
	defer rows.Close()
	var out []HostedStore
	for rows.Next() {
		h, err := scanHosted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}

// UpdateHostedStore saves the fields the owner edits (customer, license id, channel, notes, version).
func (s *Store) UpdateHostedStore(ctx context.Context, q querier, h *HostedStore) error {
	tag, err := q.Exec(ctx, `
		UPDATE core.hosted_stores SET customer = $2, license_id = $3, channel = $4, notes = $5, version = $6, updated_at = now() WHERE id = $1`,
		h.ID, h.Customer, h.LicenseID, h.Channel, h.Notes, h.Version)
	if err != nil {
		return fmt.Errorf("update hosted store: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHostedMetricsToken stores (or with nil, forgets) the encrypted token of a store's /metrics.
func (s *Store) SetHostedMetricsToken(ctx context.Context, q querier, id string, enc []byte) error {
	tag, err := q.Exec(ctx, `UPDATE core.hosted_stores SET metrics_token_enc = $2, updated_at = now() WHERE id = $1`, id, enc)
	if err != nil {
		return fmt.Errorf("set hosted metrics token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordHostedCheck stores the last reading of a store's /metrics.
func (s *Store) RecordHostedCheck(ctx context.Context, q querier, id string, at time.Time, up bool, version string, figures json.RawMessage, lastError string) error {
	if len(figures) == 0 {
		figures = json.RawMessage(`{}`)
	}
	_, err := q.Exec(ctx, `
		UPDATE core.hosted_stores SET checked_at = $2, up = $3, last_version = CASE WHEN $4 = '' THEN last_version ELSE $4 END, figures = $5,
			last_error = $6
		WHERE id = $1`, id, at, up, version, figures, lastError)
	if err != nil {
		return fmt.Errorf("record hosted check: %w", err)
	}
	return nil
}

// DeleteHostedStore removes a store from the register.
func (s *Store) DeleteHostedStore(ctx context.Context, q querier, id string) error {
	tag, err := q.Exec(ctx, `DELETE FROM core.hosted_stores WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete hosted store: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
