package store

import (
	"context"
	"fmt"
	"time"
)

// Overview are the dashboard's headline figures.
type Overview struct {
	UsersTotal, UsersNew24h, UsersNew7d             int64
	ServicesActive, ServicesExpiring, ServicesEnded int64
	ProvisionFailed, OrdersPaid24h                  int64
	// Revenue is what customers spent on orders, per currency.
	Revenue24h, Revenue30d map[string]int64
}

// OverviewStats computes the dashboard's headline figures at now.
func (s *Store) OverviewStats(ctx context.Context, q querier, now time.Time) (*Overview, error) {
	o := &Overview{Revenue24h: map[string]int64{}, Revenue30d: map[string]int64{}}
	day, week, month := now.Add(-24*time.Hour), now.Add(-7*24*time.Hour), now.Add(-30*24*time.Hour)
	err := q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM core.users),
		(SELECT count(*) FROM core.users WHERE created_at >= $1),
		(SELECT count(*) FROM core.users WHERE created_at >= $2),
		(SELECT count(*) FROM core.subscriptions WHERE status IN ('active', 'expiring_soon')),
		(SELECT count(*) FROM core.subscriptions WHERE status = 'expiring_soon'),
		(SELECT count(*) FROM core.subscriptions WHERE status IN ('expired', 'depleted')),
		(SELECT count(*) FROM core.orders WHERE status = 'provision_failed'),
		(SELECT count(*) FROM core.orders WHERE status IN ('paid', 'provisioning', 'active', 'provision_failed')
			AND updated_at >= $1 AND amount > 0)`, day, week).
		Scan(&o.UsersTotal, &o.UsersNew24h, &o.UsersNew7d, &o.ServicesActive, &o.ServicesExpiring, &o.ServicesEnded,
			&o.ProvisionFailed, &o.OrdersPaid24h)
	if err != nil {
		return nil, fmt.Errorf("overview: %w", err)
	}
	rows, err := q.Query(ctx, `SELECT currency, -sum(amount) FILTER (WHERE created_at >= $1), -sum(amount)
		FROM core.ledger_entries WHERE kind = 'purchase' AND created_at >= $2 GROUP BY currency`, day, month)
	if err != nil {
		return nil, fmt.Errorf("overview revenue: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cur        string
			day, month *int64
		)
		if err := rows.Scan(&cur, &day, &month); err != nil {
			return nil, err
		}
		if day != nil {
			o.Revenue24h[cur] = *day
		}
		if month != nil {
			o.Revenue30d[cur] = *month
		}
	}
	return o, rows.Err()
}

// RecentOrder is one line of the dashboard's latest orders.
type RecentOrder struct {
	ID, Type, Status, Currency string
	Amount                     int64
	CreatedAt                  time.Time
	TelegramID                 int64
	Username                   string
	PlanName                   map[string]string
}

// RecentOrders returns the latest orders with their buyer and plan.
func (s *Store) RecentOrders(ctx context.Context, q querier, limit int) ([]RecentOrder, error) {
	rows, err := q.Query(ctx, `
		SELECT o.id, o.type, o.status, o.currency, o.amount, o.created_at, u.telegram_id, COALESCE(u.username, ''), p.name_i18n
		FROM core.orders o JOIN core.users u ON u.id = o.user_id JOIN core.plans p ON p.id = o.plan_id
		ORDER BY o.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent orders: %w", err)
	}
	defer rows.Close()
	var out []RecentOrder
	for rows.Next() {
		var r RecentOrder
		if err := rows.Scan(&r.ID, &r.Type, &r.Status, &r.Currency, &r.Amount, &r.CreatedAt, &r.TelegramID, &r.Username, &r.PlanName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
