package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ListStaffContacts lists the active owners and admins (owners first), the
// people the bot alerts.
func (s *Store) ListStaffContacts(ctx context.Context, q querier) ([]User, error) {
	rows, err := q.Query(ctx, `
		SELECT `+userCols+` FROM core.users
		WHERE status = 'active' AND role IN ('owner', 'admin')
		ORDER BY role = 'owner' DESC, created_at`)
	if err != nil {
		return nil, fmt.Errorf("list staff contacts: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// StaffRow is a staff member with their login state, for the Staff page.
type StaffRow struct {
	User
	HasPassword     bool
	PasswordUser    string
	TOTPConfirmedAt *time.Time
	LockedUntil     *time.Time
	FailedAttempts  int
	LastLoginAt     *time.Time
	LastLoginMethod string
	LastLoginIP     string
	Sessions        int64 // active ones
}

const staffSelect = `
	SELECT u.id, u.telegram_id, COALESCE(u.username, ''), u.language, u.role, u.status, u.referred_by,
	       u.created_at, u.updated_at, u.ref_code,
	       c.user_id IS NOT NULL, COALESCE(c.username, ''), c.totp_confirmed_at, c.locked_until, COALESCE(c.failed_attempts, 0),
	       ls.created_at, COALESCE(ls.method, ''), COALESCE(ls.ip, ''),
	       (SELECT count(*) FROM core.web_sessions ws
	         WHERE ws.user_id = u.id AND ws.revoked_at IS NULL AND ws.expires_at > now() AND ws.last_seen_at > now() - $1::interval)
	FROM core.users u
	LEFT JOIN core.staff_credentials c ON c.user_id = u.id
	LEFT JOIN LATERAL (SELECT created_at, method, ip FROM core.web_sessions
	                   WHERE user_id = u.id ORDER BY created_at DESC LIMIT 1) ls ON true
	WHERE u.role IN ('owner', 'admin', 'support')`

func scanStaff(row pgx.Row) (*StaffRow, error) {
	var r StaffRow
	err := row.Scan(&r.ID, &r.TelegramID, &r.Username, &r.Language, &r.Role, &r.Status, &r.ReferredBy,
		&r.CreatedAt, &r.UpdatedAt, &r.RefCode,
		&r.HasPassword, &r.PasswordUser, &r.TOTPConfirmedAt, &r.LockedUntil, &r.FailedAttempts,
		&r.LastLoginAt, &r.LastLoginMethod, &r.LastLoginIP, &r.Sessions)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// ListStaff lists everyone with a staff role: owners, then admins, then
// support. idle is how long an unused session stays alive.
func (s *Store) ListStaff(ctx context.Context, q querier, idle time.Duration) ([]StaffRow, error) {
	rows, err := q.Query(ctx, staffSelect+`
		ORDER BY CASE u.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, u.created_at`, idle.String())
	if err != nil {
		return nil, fmt.Errorf("list staff: %w", err)
	}
	defer rows.Close()
	var out []StaffRow
	for rows.Next() {
		r, err := scanStaff(rows)
		if err != nil {
			return nil, fmt.Errorf("list staff: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetStaff is one staff member of ListStaff (ErrNotFound when not staff).
func (s *Store) GetStaff(ctx context.Context, q querier, id string, idle time.Duration) (*StaffRow, error) {
	r, err := scanStaff(q.QueryRow(ctx, staffSelect+` AND u.id = $2`, idle.String(), id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get staff: %w", err)
	}
	return r, err
}

// SetRole changes a user's role if it is still old; ErrNotFound when it
// changed meanwhile (or the user is gone).
func (s *Store) SetRole(ctx context.Context, q querier, id, oldRole, newRole string) error {
	tag, err := q.Exec(ctx, `UPDATE core.users SET role = $3, updated_at = now() WHERE id = $1 AND role = $2`, id, oldRole, newRole)
	if err != nil {
		return fmt.Errorf("set role: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LockActiveOwners returns the active owners, locked until the transaction
// ends, so two owners cannot both step down at once.
func (s *Store) LockActiveOwners(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT id FROM core.users WHERE role = 'owner' AND status = 'active' ORDER BY id FOR UPDATE`)
	if err != nil {
		return nil, fmt.Errorf("lock owners: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListSessions lists a user's live sessions, newest first.
func (s *Store) ListSessions(ctx context.Context, q querier, userID string, idle time.Duration) ([]WebSession, error) {
	rows, err := q.Query(ctx, `
		SELECT id_hash, user_id, method, COALESCE(ip, ''), COALESCE(user_agent, ''), created_at, last_seen_at, expires_at
		FROM core.web_sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now() AND last_seen_at > now() - $2::interval
		ORDER BY last_seen_at DESC`, userID, idle.String())
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []WebSession
	for rows.Next() {
		var w WebSession
		if err := rows.Scan(&w.IDHash, &w.UserID, &w.Method, &w.IP, &w.UserAgent, &w.CreatedAt, &w.LastSeenAt, &w.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// RevokeUserSession ends one session of a user; false when it was not theirs
// or had ended already.
func (s *Store) RevokeUserSession(ctx context.Context, q querier, userID string, idHash []byte) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.web_sessions SET revoked_at = now()
		WHERE user_id = $1 AND id_hash = $2 AND revoked_at IS NULL`, userID, idHash)
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// UnlockCredentials lifts a password login's lock (the code last used stays
// used); false when there was no lock.
func (s *Store) UnlockCredentials(ctx context.Context, q querier, userID string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.staff_credentials SET failed_attempts = 0, locked_until = NULL, updated_at = now()
		WHERE user_id = $1 AND (locked_until > now() OR failed_attempts > 0)`, userID)
	if err != nil {
		return false, fmt.Errorf("unlock credentials: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ExpireLoginLinks makes a user's unused login links stop working.
func (s *Store) ExpireLoginLinks(ctx context.Context, q querier, userID string) error {
	if _, err := q.Exec(ctx, `UPDATE core.login_links SET used_at = now() WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return fmt.Errorf("expire login links: %w", err)
	}
	return nil
}
