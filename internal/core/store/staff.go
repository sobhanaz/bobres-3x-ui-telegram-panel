package store

import (
	"context"
	"fmt"
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
