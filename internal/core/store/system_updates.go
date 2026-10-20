package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The updates asked for in the dashboard (migration 00025, docs/04-dashboard.md):
// one row for each request core handed to the server's updater.

// ErrUpdateOpen: an update is already open (it has no outcome yet).
var ErrUpdateOpen = errors.New("an update is already open")

// SystemUpdate is one update asked for in the dashboard.
type SystemUpdate struct {
	// ID is the request's id (the id in the request file and in the updater's
	// status).
	ID string
	// RequestedBy is the staff account that asked (nil when it was removed
	// since) and RequestedByName the name shown in the audit log and notices.
	RequestedBy     *string
	RequestedByName string
	From, To        string
	Channel         string
	RequestedAt     time.Time
	// Outcome is "" while the update is open, then done, failed, rolled_back
	// or not_started.
	Outcome   string
	OutcomeAt *time.Time
	// Message is the updater's short sentence about how it ended.
	Message string
}

const systemUpdateCols = `id, requested_by, requested_by_name, from_version, to_version, channel, requested_at,
	COALESCE(outcome, ''), outcome_at, message`

func scanSystemUpdate(row pgx.Row) (*SystemUpdate, error) {
	var u SystemUpdate
	if err := row.Scan(&u.ID, &u.RequestedBy, &u.RequestedByName, &u.From, &u.To, &u.Channel, &u.RequestedAt, &u.Outcome, &u.OutcomeAt, &u.Message); err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateSystemUpdate records a request that is now open (RequestedAt is now:
// the database's clock). It is ErrUpdateOpen when another update is open:
// at most one is at a time.
func (s *Store) CreateSystemUpdate(ctx context.Context, tx pgx.Tx, u *SystemUpdate) error {
	err := tx.QueryRow(ctx, `
		INSERT INTO core.system_updates (id, requested_by, requested_by_name, from_version, to_version, channel)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING requested_at`,
		u.ID, u.RequestedBy, u.RequestedByName, u.From, u.To, u.Channel).Scan(&u.RequestedAt)
	if isUniqueViolation(err) {
		return ErrUpdateOpen
	}
	if err != nil {
		return fmt.Errorf("create system update: %w", err)
	}
	return nil
}

// OpenSystemUpdate is the update that is open (ErrNotFound when none is).
func (s *Store) OpenSystemUpdate(ctx context.Context, q querier) (*SystemUpdate, error) {
	u, err := scanSystemUpdate(q.QueryRow(ctx, `SELECT `+systemUpdateCols+` FROM core.system_updates WHERE outcome IS NULL`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open system update: %w", err)
	}
	return u, nil
}

// LastSystemUpdate is the update that ended last (ErrNotFound when none did).
func (s *Store) LastSystemUpdate(ctx context.Context, q querier) (*SystemUpdate, error) {
	u, err := scanSystemUpdate(q.QueryRow(ctx, `SELECT `+systemUpdateCols+` FROM core.system_updates
		WHERE outcome IS NOT NULL ORDER BY outcome_at DESC, requested_at DESC LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("last system update: %w", err)
	}
	return u, nil
}

// EndSystemUpdate gives the open update id its outcome (done, failed,
// rolled_back or not_started) and the updater's sentence. It reports false
// when the update is not open (it ended already: whoever ends it first wins, so
// the audit entry and the notices are written once).
func (s *Store) EndSystemUpdate(ctx context.Context, tx pgx.Tx, id, outcome, message string) (bool, error) {
	tag, err := tx.Exec(ctx, `UPDATE core.system_updates SET outcome = $2, outcome_at = now(), message = $3
		WHERE id = $1 AND outcome IS NULL`, id, outcome, message)
	if err != nil {
		return false, fmt.Errorf("end system update: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
