package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// The states of a sign-in that waits for approval (core.login_approvals).
const (
	ApprovalPending       = "pending"       // asked, not decided
	ApprovalApproved      = "approved"      // an approver said yes; the browser has not collected its session yet
	ApprovalDenied        = "denied"        // an approver said no
	ApprovalExpired       = "expired"       // nobody answered in time, or the approval was not collected in time
	ApprovalCancelled     = "cancelled"     // the person at the browser gave up, or the owner switched approval off
	ApprovalUndeliverable = "undeliverable" // no approver could be reached
	ApprovalConsumed      = "consumed"      // the session was made
)

// Who an approver is to the sign-in: the staff member themself, or an owner.
const (
	ApproverSelf  = "self"
	ApproverOwner = "owner"
)

// LoginApproval mirrors core.login_approvals: a sign-in waiting for an approver's yes. TicketHash is the hash of the cookie the browser holds
// while it waits.
type LoginApproval struct {
	ID         string
	UserID     string
	Method     string // link | password | password_initial
	TicketHash []byte
	IP         string
	UserAgent  string
	NewAddress bool
	Status     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	DecidedAt  *time.Time
	DecidedBy  *string
	DecidedVia string // self | owner, or ""
	ConsumedAt *time.Time
	// Due: its time is up, by the database's clock when it was read.
	Due bool
}

// ApprovalTarget mirrors core.login_approval_targets: one person asked to approve a sign-in.
type ApprovalTarget struct {
	ApprovalID string
	TelegramID int64
	UserID     string
	Role       string // self | owner
	NonceHash  []byte
	SentAt     *time.Time
	MessageID  *int64
	SendError  string
	ResolvedAt *time.Time
}

const approvalCols = `id, user_id, method, ticket_hash, COALESCE(ip, ''), COALESCE(user_agent, ''), new_address, status,
	created_at, expires_at, decided_at, decided_by, COALESCE(decided_via, ''), consumed_at, expires_at <= now()`

func scanApproval(row pgx.Row) (*LoginApproval, error) {
	var a LoginApproval
	err := row.Scan(&a.ID, &a.UserID, &a.Method, &a.TicketHash, &a.IP, &a.UserAgent, &a.NewAddress, &a.Status,
		&a.CreatedAt, &a.ExpiresAt, &a.DecidedAt, &a.DecidedBy, &a.DecidedVia, &a.ConsumedAt, &a.Due)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("login approval: %w", err)
	}
	return &a, nil
}

const targetCols = `approval_id, telegram_id, user_id, role, nonce_hash, sent_at, message_id, COALESCE(send_error, ''), resolved_at`

func scanTarget(row pgx.Row) (*ApprovalTarget, error) {
	var t ApprovalTarget
	err := row.Scan(&t.ApprovalID, &t.TelegramID, &t.UserID, &t.Role, &t.NonceHash, &t.SentAt, &t.MessageID, &t.SendError, &t.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("login approval target: %w", err)
	}
	return &t, nil
}

// LockUserRow takes the user's row for the rest of the transaction, so that the sign-ins of one account are counted one at a time.
func (s *Store) LockUserRow(ctx context.Context, tx pgx.Tx, userID string) error {
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM core.users WHERE id = $1 FOR UPDATE`, userID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock user: %w", err)
	}
	return nil
}

// ApprovalCounts says how many sign-ins an account asked for within the last window, how many wait now, and how long until the oldest of the
// recent ones leaves the window (0 when there is none), by the database's clock.
func (s *Store) ApprovalCounts(ctx context.Context, q querier, userID string, window time.Duration) (recent, open int, retryAfter time.Duration, err error) {
	var secs float64
	err = q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE created_at > now() - $2::interval),
		       count(*) FILTER (WHERE status = 'pending' AND expires_at > now()),
		       COALESCE(EXTRACT(EPOCH FROM (min(created_at) FILTER (WHERE created_at > now() - $2::interval) + $2::interval - now())), 0)::float8
		FROM core.login_approvals WHERE user_id = $1`, userID, window.String()).Scan(&recent, &open, &secs)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("approval counts: %w", err)
	}
	return recent, open, time.Duration(secs * float64(time.Second)), nil
}

// UserSeenAddress reports whether any session of the account came from the address before.
func (s *Store) UserSeenAddress(ctx context.Context, q querier, userID, ip string) (bool, error) {
	if ip == "" {
		return false, nil
	}
	var seen bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM core.web_sessions WHERE user_id = $1 AND ip = $2)`, userID, ip).Scan(&seen)
	if err != nil {
		return false, fmt.Errorf("seen address: %w", err)
	}
	return seen, nil
}

// CreateLoginApproval stores a waiting sign-in with the people asked to approve it. It lives for ttl, counted by the database's clock; a is
// filled with the times the database gave.
func (s *Store) CreateLoginApproval(ctx context.Context, tx pgx.Tx, a *LoginApproval, ttl time.Duration, targets []ApprovalTarget) error {
	err := tx.QueryRow(ctx, `
		INSERT INTO core.login_approvals (id, user_id, method, ticket_hash, ip, user_agent, new_address, expires_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, now() + $8::interval)
		RETURNING status, created_at, expires_at`,
		a.ID, a.UserID, a.Method, a.TicketHash, a.IP, a.UserAgent, a.NewAddress, ttl.String()).Scan(&a.Status, &a.CreatedAt, &a.ExpiresAt)
	if err != nil {
		return fmt.Errorf("create login approval: %w", err)
	}
	for _, t := range targets {
		if _, err := tx.Exec(ctx, `
			INSERT INTO core.login_approval_targets (approval_id, telegram_id, user_id, role, nonce_hash)
			VALUES ($1, $2, $3, $4, $5)`, a.ID, t.TelegramID, t.UserID, t.Role, t.NonceHash); err != nil {
			return fmt.Errorf("create login approval target: %w", err)
		}
	}
	return nil
}

// GetLoginApproval returns a sign-in by id.
func (s *Store) GetLoginApproval(ctx context.Context, q querier, id string) (*LoginApproval, error) {
	return scanApproval(q.QueryRow(ctx, `SELECT `+approvalCols+` FROM core.login_approvals WHERE id = $1`, id))
}

// LockLoginApproval is GetLoginApproval that holds the row until the transaction ends, so that two answers to one sign-in are taken in turn.
func (s *Store) LockLoginApproval(ctx context.Context, tx pgx.Tx, id string) (*LoginApproval, error) {
	return scanApproval(tx.QueryRow(ctx, `SELECT `+approvalCols+` FROM core.login_approvals WHERE id = $1 FOR UPDATE`, id))
}

// LoginApprovalByTicket finds the sign-in a browser waits for by the hash of its cookie.
func (s *Store) LoginApprovalByTicket(ctx context.Context, q querier, ticketHash []byte) (*LoginApproval, error) {
	return scanApproval(q.QueryRow(ctx, `SELECT `+approvalCols+` FROM core.login_approvals WHERE ticket_hash = $1`, ticketHash))
}

// ApprovalTargets lists the people asked to approve a sign-in.
func (s *Store) ApprovalTargets(ctx context.Context, q querier, approvalID string) ([]ApprovalTarget, error) {
	rows, err := q.Query(ctx, `SELECT `+targetCols+` FROM core.login_approval_targets WHERE approval_id = $1
		ORDER BY CASE role WHEN 'self' THEN 0 ELSE 1 END, telegram_id`, approvalID)
	if err != nil {
		return nil, fmt.Errorf("approval targets: %w", err)
	}
	defer rows.Close()
	var out []ApprovalTarget
	for rows.Next() {
		t, err := scanTarget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetApprovalTarget returns the person with that Telegram id among those asked to approve a sign-in.
func (s *Store) GetApprovalTarget(ctx context.Context, q querier, approvalID string, telegramID int64) (*ApprovalTarget, error) {
	return scanTarget(q.QueryRow(ctx, `SELECT `+targetCols+` FROM core.login_approval_targets WHERE approval_id = $1 AND telegram_id = $2`,
		approvalID, telegramID))
}

// RecordApprovalDelivery notes what became of the message to one approver: its id when the bot sent it, else a short code.
func (s *Store) RecordApprovalDelivery(ctx context.Context, q querier, approvalID string, telegramID int64, messageID int64, sendError string) error {
	if sendError != "" {
		_, err := q.Exec(ctx, `UPDATE core.login_approval_targets SET send_error = left($3, 40), sent_at = NULL, message_id = NULL
			WHERE approval_id = $1 AND telegram_id = $2`, approvalID, telegramID, sendError)
		if err != nil {
			return fmt.Errorf("record approval delivery: %w", err)
		}
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE core.login_approval_targets SET sent_at = now(), message_id = NULLIF($3, 0), send_error = NULL
		WHERE approval_id = $1 AND telegram_id = $2`, approvalID, telegramID, messageID)
	if err != nil {
		return fmt.Errorf("record approval delivery: %w", err)
	}
	return nil
}

// DecideLoginApproval turns a waiting sign-in into approved or denied for the person who answered, while it is still waiting and not too late;
// an approval stays collectable for at least pickup after it. It reports whether it did.
func (s *Store) DecideLoginApproval(ctx context.Context, tx pgx.Tx, id string, approve bool, by, via string, pickup time.Duration) (bool, error) {
	status := ApprovalDenied
	if approve {
		status = ApprovalApproved
	}
	tag, err := tx.Exec(ctx, `
		UPDATE core.login_approvals SET status = $2, decided_at = now(), decided_by = $3, decided_via = $4,
			expires_at = CASE WHEN $2 = 'approved' THEN greatest(expires_at, now() + $5::interval) ELSE expires_at END
		WHERE id = $1 AND status = 'pending' AND expires_at > now()`, id, status, by, via, pickup.String())
	if err != nil {
		return false, fmt.Errorf("decide login approval: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// EndLoginApproval ends a sign-in that nobody decided (expired, cancelled, undeliverable). From says which states it may end from.
func (s *Store) EndLoginApproval(ctx context.Context, q querier, id, status string, from ...string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.login_approvals SET status = $2, decided_at = COALESCE(decided_at, now())
		WHERE id = $1 AND status = ANY($3)`, id, status, from)
	if err != nil {
		return false, fmt.Errorf("end login approval: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ConsumeLoginApproval takes an approved sign-in for its session, once, while it can still be collected.
func (s *Store) ConsumeLoginApproval(ctx context.Context, q querier, id string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.login_approvals SET status = 'consumed', consumed_at = now()
		WHERE id = $1 AND status = 'approved' AND expires_at > now()`, id)
	if err != nil {
		return false, fmt.Errorf("consume login approval: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DueLoginApprovals locks the sign-ins whose time is up (waiting, or approved and never collected), at most limit of them, skipping any that
// another transaction holds.
func (s *Store) DueLoginApprovals(ctx context.Context, tx pgx.Tx, limit int) ([]LoginApproval, error) {
	rows, err := tx.Query(ctx, `SELECT `+approvalCols+` FROM core.login_approvals
		WHERE status IN ('pending', 'approved') AND expires_at <= now()
		ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("due login approvals: %w", err)
	}
	defer rows.Close()
	var out []LoginApproval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// WaitingLoginApprovals locks every sign-in that waits, for ending them all (approval switched off), optionally those of one account.
func (s *Store) WaitingLoginApprovals(ctx context.Context, tx pgx.Tx, userID string) ([]LoginApproval, error) {
	rows, err := tx.Query(ctx, `SELECT `+approvalCols+` FROM core.login_approvals
		WHERE status = 'pending' AND ($1 = '' OR user_id::text = $1) ORDER BY created_at FOR UPDATE`, userID)
	if err != nil {
		return nil, fmt.Errorf("waiting login approvals: %w", err)
	}
	defer rows.Close()
	var out []LoginApproval
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// CountWaitingLoginApprovals says how many sign-ins wait for an answer now.
func (s *Store) CountWaitingLoginApprovals(ctx context.Context, q querier) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.login_approvals WHERE status = 'pending' AND expires_at > now()`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count waiting login approvals: %w", err)
	}
	return n, nil
}

// ApprovalMessage is a message the bot sent for a sign-in that has ended, to be written over with the result.
type ApprovalMessage struct {
	Approval LoginApproval
	Target   ApprovalTarget
}

// ApprovalMessagesToClose lists the messages of sign-ins that are no longer waiting and that have not been written over yet, limited to those
// that ended within the last window (older ones are left as they are), at most limit.
func (s *Store) ApprovalMessagesToClose(ctx context.Context, q querier, window time.Duration, limit int) ([]ApprovalMessage, error) {
	rows, err := q.Query(ctx, `
		SELECT a.id, a.user_id, a.method, a.ticket_hash, COALESCE(a.ip, ''), COALESCE(a.user_agent, ''), a.new_address, a.status,
		       a.created_at, a.expires_at, a.decided_at, a.decided_by, COALESCE(a.decided_via, ''), a.consumed_at, a.expires_at <= now(),
		       t.approval_id, t.telegram_id, t.user_id, t.role, t.nonce_hash, t.sent_at, t.message_id, COALESCE(t.send_error, ''), t.resolved_at
		FROM core.login_approval_targets t JOIN core.login_approvals a ON a.id = t.approval_id
		WHERE t.message_id IS NOT NULL AND t.resolved_at IS NULL AND a.status <> 'pending'
		  AND COALESCE(a.decided_at, a.expires_at) > now() - $1::interval
		ORDER BY COALESCE(a.decided_at, a.expires_at) LIMIT $2`, window.String(), limit)
	if err != nil {
		return nil, fmt.Errorf("approval messages to close: %w", err)
	}
	defer rows.Close()
	var out []ApprovalMessage
	for rows.Next() {
		var m ApprovalMessage
		a, t := &m.Approval, &m.Target
		if err := rows.Scan(&a.ID, &a.UserID, &a.Method, &a.TicketHash, &a.IP, &a.UserAgent, &a.NewAddress, &a.Status,
			&a.CreatedAt, &a.ExpiresAt, &a.DecidedAt, &a.DecidedBy, &a.DecidedVia, &a.ConsumedAt, &a.Due,
			&t.ApprovalID, &t.TelegramID, &t.UserID, &t.Role, &t.NonceHash, &t.SentAt, &t.MessageID, &t.SendError, &t.ResolvedAt); err != nil {
			return nil, fmt.Errorf("approval messages to close: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkApprovalMessagesClosed notes that the result was written into the messages of the approvers (all of them, or just one).
func (s *Store) MarkApprovalMessagesClosed(ctx context.Context, q querier, approvalID string, telegramIDs ...int64) error {
	if telegramIDs == nil {
		telegramIDs = []int64{} // an empty list, not NULL: it means all of them
	}
	_, err := q.Exec(ctx, `UPDATE core.login_approval_targets SET resolved_at = now()
		WHERE approval_id = $1 AND resolved_at IS NULL AND (cardinality($2::bigint[]) = 0 OR telegram_id = ANY($2))`, approvalID, telegramIDs)
	if err != nil {
		return fmt.Errorf("mark approval messages closed: %w", err)
	}
	return nil
}

// PruneLoginApprovals deletes the sign-ins that ended before a moment (the audit log keeps the record), with their approvers.
func (s *Store) PruneLoginApprovals(ctx context.Context, q querier, before time.Time) (int64, error) {
	tag, err := q.Exec(ctx, `DELETE FROM core.login_approvals WHERE status <> 'pending' AND created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("prune login approvals: %w", err)
	}
	return tag.RowsAffected(), nil
}
