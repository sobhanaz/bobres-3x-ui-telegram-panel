package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrNameTaken: another reseller already has this config prefix or group name.
var ErrNameTaken = errors.New("store: name already taken by another reseller")

// ResellerNaming is what a reseller's clients are called and grouped as in the 3x-ui panel
// (migration 00035). Empty strings: not set yet.
type ResellerNaming struct {
	UserID       string
	ConfigPrefix string
	PanelGroup   string // the NAME of "BOBRES-NAME"
	// GroupsSyncedAt is when the clients made before PanelGroup was set were moved into the group
	// (nil: still to do).
	GroupsSyncedAt *time.Time
}

const resellerNamingCols = `user_id, coalesce(config_prefix, ''), coalesce(panel_group, ''), groups_synced_at`

func scanResellerNaming(row pgx.Row) (*ResellerNaming, error) {
	var n ResellerNaming
	if err := row.Scan(&n.UserID, &n.ConfigPrefix, &n.PanelGroup, &n.GroupsSyncedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &n, nil
}

// GetResellerNaming returns a reseller's prefix and group name (ErrNotFound when the user is not a reseller).
func (s *Store) GetResellerNaming(ctx context.Context, q querier, userID string) (*ResellerNaming, error) {
	n, err := scanResellerNaming(q.QueryRow(ctx, `SELECT `+resellerNamingCols+` FROM core.resellers WHERE user_id = $1`, userID))
	if err != nil {
		return nil, fmt.Errorf("get reseller naming: %w", err)
	}
	return n, nil
}

// SetResellerPanelGroup sets (or with "" clears) the reseller's group name and puts their clients back to be moved
// (ErrNameTaken when another reseller has it, ErrNotFound when the user is not a reseller).
func (s *Store) SetResellerPanelGroup(ctx context.Context, q querier, userID, name string) error {
	tag, err := q.Exec(ctx, `UPDATE core.resellers SET panel_group = NULLIF($2, ''), groups_synced_at = NULL, updated_at = now()
		WHERE user_id = $1`, userID, name)
	if isUniqueViolation(err) {
		return ErrNameTaken
	}
	if err != nil {
		return fmt.Errorf("set reseller panel group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetResellerConfigPrefix sets (or with "" clears) the prefix of the reseller's config names. With onlyIfNone the
// prefix is written only while the reseller has none (the reseller sets it once): it returns false when they already have one.
func (s *Store) SetResellerConfigPrefix(ctx context.Context, q querier, userID, prefix string, onlyIfNone bool) (bool, error) {
	sql := `UPDATE core.resellers SET config_prefix = NULLIF($2, ''), updated_at = now() WHERE user_id = $1`
	if onlyIfNone {
		sql += ` AND config_prefix IS NULL`
	}
	tag, err := q.Exec(ctx, sql, userID, prefix)
	if isUniqueViolation(err) {
		return false, ErrNameTaken
	}
	if err != nil {
		return false, fmt.Errorf("set reseller config prefix: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.GetResellerNaming(ctx, q, userID); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// RequestResellerGroupSync puts a reseller's clients back to be moved into their group (the owner's "move the existing configs").
func (s *Store) RequestResellerGroupSync(ctx context.Context, q querier, userID string) error {
	tag, err := q.Exec(ctx, `UPDATE core.resellers SET groups_synced_at = NULL WHERE user_id = $1 AND panel_group IS NOT NULL`, userID)
	if err != nil {
		return fmt.Errorf("request reseller group sync: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResellersToSync lists the resellers with a group whose earlier clients are still to be moved into it.
func (s *Store) ResellersToSync(ctx context.Context, q querier, limit int) ([]ResellerNaming, error) {
	rows, err := q.Query(ctx, `SELECT `+resellerNamingCols+` FROM core.resellers
		WHERE panel_group IS NOT NULL AND groups_synced_at IS NULL ORDER BY updated_at, user_id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("resellers to sync: %w", err)
	}
	defer rows.Close()
	var out []ResellerNaming
	for rows.Next() {
		n, err := scanResellerNaming(rows)
		if err != nil {
			return nil, fmt.Errorf("resellers to sync: %w", err)
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// MarkResellerGroupsSynced records that the reseller's clients were handed to the provisioner for the group
// they have now (group): not when the group changed meanwhile, so the new one is done next.
func (s *Store) MarkResellerGroupsSynced(ctx context.Context, q querier, userID, group string) error {
	if _, err := q.Exec(ctx, `UPDATE core.resellers SET groups_synced_at = now()
		WHERE user_id = $1 AND groups_synced_at IS NULL AND panel_group IS NOT DISTINCT FROM NULLIF($2, '')`, userID, group); err != nil {
		return fmt.Errorf("mark reseller groups synced: %w", err)
	}
	return nil
}

// SubscriptionIDsOf lists the ids of a user's subscriptions that have a panel client, after afterID (for paging), oldest first.
func (s *Store) SubscriptionIDsOf(ctx context.Context, q querier, userID, afterID string, limit int) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id::text FROM core.subscriptions
		WHERE user_id = $1 AND id::text > $2 AND server_id IS NOT NULL ORDER BY id::text LIMIT $3`, userID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("subscription ids of: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("subscription ids of: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
