package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// WebSession is a dashboard login. Only a hash of the cookie value is kept.
type WebSession struct {
	IDHash     []byte
	UserID     string
	CSRFToken  string
	Method     string // link | password
	IP         string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// CreateSession stores a new dashboard session.
func (s *Store) CreateSession(ctx context.Context, q querier, w *WebSession) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.web_sessions (id_hash, user_id, csrf_token, method, ip, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7)`,
		w.IDHash, w.UserID, w.CSRFToken, w.Method, w.IP, w.UserAgent, w.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// ActiveSession returns a session that is neither revoked nor expired and was
// used within idle. Others read as ErrNotFound.
func (s *Store) ActiveSession(ctx context.Context, q querier, idHash []byte, idle time.Duration) (*WebSession, error) {
	var w WebSession
	err := q.QueryRow(ctx, `
		SELECT id_hash, user_id, csrf_token, method, COALESCE(ip, ''), COALESCE(user_agent, ''), created_at, last_seen_at, expires_at
		FROM core.web_sessions
		WHERE id_hash = $1 AND revoked_at IS NULL AND expires_at > now() AND last_seen_at > now() - $2::interval`,
		idHash, idle.String()).
		Scan(&w.IDHash, &w.UserID, &w.CSRFToken, &w.Method, &w.IP, &w.UserAgent, &w.CreatedAt, &w.LastSeenAt, &w.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("active session: %w", err)
	}
	return &w, nil
}

// TouchSession records that a session was just used.
func (s *Store) TouchSession(ctx context.Context, q querier, idHash []byte) error {
	if _, err := q.Exec(ctx, `UPDATE core.web_sessions SET last_seen_at = now() WHERE id_hash = $1`, idHash); err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// RevokeSession ends one session (logout).
func (s *Store) RevokeSession(ctx context.Context, q querier, idHash []byte) error {
	if _, err := q.Exec(ctx, `UPDATE core.web_sessions SET revoked_at = now() WHERE id_hash = $1 AND revoked_at IS NULL`, idHash); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeUserSessions ends every session of a user (role change, password
// reset) and says how many were live.
func (s *Store) RevokeUserSessions(ctx context.Context, q querier, userID string) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE core.web_sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke user sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RevokeOtherSessions ends every session of a user except the one kept.
func (s *Store) RevokeOtherSessions(ctx context.Context, q querier, userID string, keep []byte) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE core.web_sessions SET revoked_at = now()
		WHERE user_id = $1 AND id_hash <> $2 AND revoked_at IS NULL AND expires_at > now()`, userID, keep)
	if err != nil {
		return 0, fmt.Errorf("revoke other sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CreateLoginLink stores a one-time login token (its hash) for a user.
func (s *Store) CreateLoginLink(ctx context.Context, q querier, tokenHash []byte, userID string, expiresAt time.Time) error {
	if _, err := q.Exec(ctx, `INSERT INTO core.login_links (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt); err != nil {
		return fmt.Errorf("create login link: %w", err)
	}
	return nil
}

// ConsumeLoginLink uses a login token once: it returns the user it was made
// for, or ErrNotFound when the token is unknown, used or expired.
func (s *Store) ConsumeLoginLink(ctx context.Context, q querier, tokenHash []byte) (string, error) {
	var userID string
	err := q.QueryRow(ctx, `
		UPDATE core.login_links SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING user_id`, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("consume login link: %w", err)
	}
	return userID, nil
}

// StaffCredentials is a staff member's password login (always with an
// authenticator code).
type StaffCredentials struct {
	UserID          string
	Username        string
	PasswordHash    string
	TOTPSecretEnc   []byte
	TOTPConfirmedAt *time.Time
	TOTPLastStep    int64
	FailedAttempts  int
	LockedUntil     *time.Time
}

const credentialCols = `user_id, username, password_hash, totp_secret_enc, totp_confirmed_at, totp_last_step, failed_attempts, locked_until` //nolint:gosec // G101: column names, not a credential

func scanCredentials(row pgx.Row) (*StaffCredentials, error) {
	var c StaffCredentials
	err := row.Scan(&c.UserID, &c.Username, &c.PasswordHash, &c.TOTPSecretEnc, &c.TOTPConfirmedAt,
		&c.TOTPLastStep, &c.FailedAttempts, &c.LockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

// CredentialsByUsername finds a password login by its username.
func (s *Store) CredentialsByUsername(ctx context.Context, q querier, username string) (*StaffCredentials, error) {
	c, err := scanCredentials(q.QueryRow(ctx, `SELECT `+credentialCols+` FROM core.staff_credentials WHERE username = $1`, username))
	if err != nil {
		return nil, fmt.Errorf("credentials by username: %w", err)
	}
	return c, nil
}

// CredentialsFor returns a user's password login.
func (s *Store) CredentialsFor(ctx context.Context, q querier, userID string) (*StaffCredentials, error) {
	c, err := scanCredentials(q.QueryRow(ctx, `SELECT `+credentialCols+` FROM core.staff_credentials WHERE user_id = $1`, userID))
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}
	return c, nil
}

// ErrUsernameTaken: another staff member already uses this username.
var ErrUsernameTaken = errors.New("store: username taken")

// SetPendingCredentials stores a new password and authenticator secret for a
// user, unconfirmed until the first code (replacing any earlier ones).
func (s *Store) SetPendingCredentials(ctx context.Context, q querier, userID, username, passwordHash string, secretEnc []byte) error {
	_, err := q.Exec(ctx, `
		INSERT INTO core.staff_credentials (user_id, username, password_hash, totp_secret_enc)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET username = EXCLUDED.username, password_hash = EXCLUDED.password_hash,
			totp_secret_enc = EXCLUDED.totp_secret_enc, totp_confirmed_at = NULL, totp_last_step = 0,
			failed_attempts = 0, locked_until = NULL, updated_at = now()`,
		userID, username, passwordHash, secretEnc)
	if isUniqueViolation(err) {
		return ErrUsernameTaken
	}
	if err != nil {
		return fmt.Errorf("set credentials: %w", err)
	}
	return nil
}

// ConfirmCredentials activates a password login after its first code.
func (s *Store) ConfirmCredentials(ctx context.Context, q querier, userID string, step int64) error {
	tag, err := q.Exec(ctx, `UPDATE core.staff_credentials SET totp_confirmed_at = now(), totp_last_step = $2, updated_at = now()
		WHERE user_id = $1`, userID, step)
	if err != nil {
		return fmt.Errorf("confirm credentials: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordLoginFailure counts a failed password login; the fifth in a row locks
// the login for lockFor (it reports when this one locked it). Failures
// before a lock that has ended no longer count.
func (s *Store) RecordLoginFailure(ctx context.Context, q querier, userID string, lockFor time.Duration) (bool, error) {
	var locked bool
	err := q.QueryRow(ctx, `
		WITH cur AS (
			SELECT CASE WHEN locked_until IS NOT NULL AND locked_until <= now() THEN 0 ELSE failed_attempts END AS n
			FROM core.staff_credentials WHERE user_id = $1 FOR UPDATE)
		UPDATE core.staff_credentials c
		SET failed_attempts = cur.n + 1,
		    locked_until = CASE WHEN cur.n + 1 >= 5 THEN now() + $2::interval
		                        WHEN c.locked_until <= now() THEN NULL ELSE c.locked_until END,
		    updated_at = now()
		FROM cur WHERE c.user_id = $1
		RETURNING cur.n + 1 >= 5`, userID, lockFor.String()).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("record login failure: %w", err)
	}
	return locked, nil
}

// RecordLoginSuccess clears the failure count and remembers the code used.
func (s *Store) RecordLoginSuccess(ctx context.Context, q querier, userID string, step int64) error {
	_, err := q.Exec(ctx, `UPDATE core.staff_credentials SET failed_attempts = 0, locked_until = NULL, totp_last_step = $2,
		updated_at = now() WHERE user_id = $1`, userID, step)
	if err != nil {
		return fmt.Errorf("record login success: %w", err)
	}
	return nil
}

// DeleteCredentials removes a user's password login.
func (s *Store) DeleteCredentials(ctx context.Context, q querier, userID string) error {
	if _, err := q.Exec(ctx, `DELETE FROM core.staff_credentials WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete credentials: %w", err)
	}
	return nil
}
