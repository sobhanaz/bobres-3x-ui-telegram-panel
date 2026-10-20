package store

import (
	"context"
	"fmt"
)

// InviterRow is one customer on the top-inviters board.
type InviterRow struct {
	UserID     string
	TelegramID int64
	Username   string
	Invited    int64
}

// TopInviters ranks the customers who invited the most people: users with the
// plain customer role ("user", so no staff and no resellers: a reseller's
// invites are their commission link) who are not banned, by how many users
// joined through their invite link (the same count as ReferralStats), most
// first. A tie goes to whoever joined the bot first. Only customers with at
// least one invited user have a row, and at most limit rows are returned.
//
// It is one statement: the invites are counted over the partial index
// users_referred_by_idx and joined to their inviters, so it costs one pass
// over the invited users (the thing the count is of) however many customers
// the store has.
func (s *Store) TopInviters(ctx context.Context, q querier, limit int) ([]InviterRow, error) {
	if limit <= 0 {
		return []InviterRow{}, nil
	}
	rows, err := q.Query(ctx, `
		SELECT u.id, u.telegram_id, COALESCE(u.username, ''), i.invited
		FROM (SELECT referred_by, count(*) AS invited FROM core.users WHERE referred_by IS NOT NULL
		      GROUP BY referred_by) i
		JOIN core.users u ON u.id = i.referred_by
		WHERE u.status = 'active' AND u.role = 'user'
		ORDER BY i.invited DESC, u.created_at, u.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("top inviters: %w", err)
	}
	defer rows.Close()
	out := []InviterRow{} // never nil: an empty board is a board
	for rows.Next() {
		var r InviterRow
		if err := rows.Scan(&r.UserID, &r.TelegramID, &r.Username, &r.Invited); err != nil {
			return nil, fmt.Errorf("top inviters: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("top inviters: %w", err)
	}
	return out, nil
}

// InvitedCount is how many users joined through userID's invite link, whoever
// userID is (the count of ReferralStats, without the rewards).
func (s *Store) InvitedCount(ctx context.Context, q querier, userID string) (int64, error) {
	var n int64
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.users WHERE referred_by = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("invited count: %w", err)
	}
	return n, nil
}
