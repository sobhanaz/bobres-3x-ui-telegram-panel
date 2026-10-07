package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The dashboard's lists: filtered, newest first, one page at a time, each
// with the total number of matches.

// Page selects a slice of a list.
type Page struct {
	Limit, Offset int
}

func (p Page) clamp() Page {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 25
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}

// search turns what staff type into a numeric id (a Telegram id) and a LIKE
// pattern ("@name" and "name" both match usernames containing it).
func search(q string) (num int64, isNum bool, like string) {
	q = strings.TrimSpace(q)
	if n, err := strconv.ParseInt(q, 10, 64); err == nil && n > 0 {
		num, isNum = n, true
	}
	q = strings.ToLower(strings.TrimPrefix(q, "@"))
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return num, isNum, "%" + q + "%"
}

// UserFilter narrows the users list. Query matches a Telegram id, part of a
// username or an invite code.
type UserFilter struct {
	Query, Status, Role string
}

// UserRow is one line of the users list.
type UserRow struct {
	User
	Subscriptions, Orders int64
	Wallets               []Wallet // non-zero balances only
}

// ListUsers returns a page of users and the number of matches.
func (s *Store) ListUsers(ctx context.Context, q querier, f UserFilter, p Page) ([]UserRow, int64, error) {
	p = p.clamp()
	num, isNum, like := search(f.Query)
	rows, err := q.Query(ctx, `
		SELECT `+userCols+`,
			(SELECT count(*) FROM core.subscriptions s WHERE s.user_id = u.id AND s.status <> 'deleted'),
			(SELECT count(*) FROM core.orders o WHERE o.user_id = u.id),
			count(*) OVER ()
		FROM core.users u
		WHERE ($1 = '' OR (CASE WHEN $2 THEN u.telegram_id = $3 ELSE false END)
		           OR lower(u.username) LIKE $4 ESCAPE '\' OR u.ref_code = lower(btrim($1)))
		  AND ($5 = '' OR u.status = $5)
		  AND ($6 = '' OR u.role = $6)
		ORDER BY u.created_at DESC, u.id DESC
		LIMIT $7 OFFSET $8`,
		strings.TrimSpace(f.Query), isNum, num, like, f.Status, f.Role, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var (
		out   []UserRow
		total int64
		ids   []string
	)
	for rows.Next() {
		var r UserRow
		u := &r.User
		if err := rows.Scan(&u.ID, &u.TelegramID, &u.Username, &u.Language, &u.Role, &u.Status, &u.ReferredBy,
			&u.CreatedAt, &u.UpdatedAt, &u.RefCode, &r.Subscriptions, &r.Orders, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
		ids = append(ids, u.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(ids) == 0 {
		return out, total, nil
	}
	wrows, err := q.Query(ctx, `SELECT user_id, currency, balance, updated_at FROM core.wallets
		WHERE user_id = ANY($1) AND balance <> 0 ORDER BY currency`, ids)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: wallets: %w", err)
	}
	defer wrows.Close()
	byUser := map[string][]Wallet{}
	for wrows.Next() {
		var w Wallet
		if err := wrows.Scan(&w.UserID, &w.Currency, &w.Balance, &w.UpdatedAt); err != nil {
			return nil, 0, err
		}
		byUser[w.UserID] = append(byUser[w.UserID], w)
	}
	for i := range out {
		out[i].Wallets = byUser[out[i].ID]
	}
	return out, total, wrows.Err()
}

// subscriptionColsS is subscriptionCols for a query that names the table s.
const subscriptionColsS = `s.id, s.user_id, s.order_id, COALESCE(s.server_id::text, ''), s.client_email, s.sub_id, s.status,
	s.expires_at, s.traffic_total_bytes, COALESCE(s.traffic_used_bytes, 0), s.last_synced_at, COALESCE(s.sub_link, ''), s.created_at,
	s.notified_expiring_at, s.notified_low_traffic_at, s.notified_ended_at`

// SubscriptionFilter narrows the services list. Query matches a Telegram id,
// part of a username or panel email, or part of a service id (the bot shows
// its last 6 hex digits). A deleted service is listed only when Status asks
// for "deleted" or the list is one user's.
type SubscriptionFilter struct {
	Query, Status, UserID, ID string
}

// SubscriptionRow is one line of the services list.
type SubscriptionRow struct {
	Subscription
	TelegramID int64
	Username   string
	PlanID     string
	PlanName   map[string]string // the plan the service was bought with
}

// ListAllSubscriptions returns a page of services and the number of matches.
func (s *Store) ListAllSubscriptions(ctx context.Context, q querier, f SubscriptionFilter, p Page) ([]SubscriptionRow, int64, error) {
	p = p.clamp()
	num, isNum, like := search(f.Query)
	idPart := idPart(f.Query)
	rows, err := q.Query(ctx, `
		SELECT `+subscriptionColsS+`, u.telegram_id, COALESCE(u.username, ''), p.id, p.name_i18n, count(*) OVER ()
		FROM core.subscriptions s
		JOIN core.users u ON u.id = s.user_id
		JOIN core.orders o ON o.id = s.order_id
		JOIN core.plans p ON p.id = o.plan_id
		WHERE ($1 = '' OR (CASE WHEN $2 THEN u.telegram_id = $3 ELSE false END)
		           OR lower(u.username) LIKE $4 ESCAPE '\' OR lower(s.client_email) LIKE $4 ESCAPE '\'
		           OR ($5 <> '' AND replace(s.id::text, '-', '') LIKE '%' || $5 || '%'))
		  AND (CASE WHEN $6 <> '' THEN s.status = $6 WHEN $7 <> '' OR $8 <> '' THEN true ELSE s.status <> 'deleted' END)
		  AND ($7 = '' OR s.user_id::text = $7)
		  AND ($8 = '' OR s.id::text = $8)
		ORDER BY s.created_at DESC, s.id DESC
		LIMIT $9 OFFSET $10`,
		strings.TrimSpace(f.Query), isNum, num, like, idPart, f.Status, f.UserID, f.ID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()
	var (
		out   []SubscriptionRow
		total int64
	)
	for rows.Next() {
		var (
			r    SubscriptionRow
			name []byte
		)
		sc := &r.Subscription
		if err := rows.Scan(&sc.ID, &sc.UserID, &sc.OrderID, &sc.ServerID, &sc.ClientEmail, &sc.SubID, &sc.Status,
			&sc.ExpiresAt, &sc.TrafficTotal, &sc.TrafficUsed, &sc.LastSyncedAt, &sc.SubLink, &sc.CreatedAt,
			&sc.NotifiedExpiringAt, &sc.NotifiedLowTrafficAt, &sc.NotifiedEndedAt,
			&r.TelegramID, &r.Username, &r.PlanID, &name, &total); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal(name, &r.PlanName); err != nil {
			return nil, 0, fmt.Errorf("plan name_i18n: %w", err)
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// idPart is what staff typed as (part of) an id, as hex digits without
// dashes, or "" when it cannot be one (fewer than 4 hex digits).
func idPart(q string) string {
	s := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(q)), "-", "")
	if len(s) < 4 || len(s) > 32 {
		return ""
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return s
}

// orderColsO is orderCols for a query that names the table o.
const orderColsO = `o.id, o.user_id, o.plan_id, o.type, o.status, o.amount, o.currency, o.idempotency_key, o.created_at, o.updated_at,
	o.subscription_id, o.discount_code, o.discount_amount, o.targets_set, o.target_expires_at, o.target_traffic_bytes,
	o.extend_days, o.extend_bytes, o.created_by, o.refunded_at, o.refund_amount`

// OrderFilter narrows the orders list. Query matches a Telegram id, part of a
// username or part of an order id.
type OrderFilter struct {
	Query, Status, Type, UserID, SubscriptionID, ID string
}

// OrderRow is one line of the orders list.
type OrderRow struct {
	Order
	TelegramID    int64
	Username      string
	PlanName      map[string]string
	Attempts      int
	LastError     string
	NextAttemptAt *time.Time
	CreatedByName string // the staff member's @username or Telegram id
}

// ListOrders returns a page of orders and the number of matches.
func (s *Store) ListOrders(ctx context.Context, q querier, f OrderFilter, p Page) ([]OrderRow, int64, error) {
	p = p.clamp()
	num, isNum, like := search(f.Query)
	idPart := idPart(f.Query)
	rows, err := q.Query(ctx, `
		SELECT `+orderColsO+`, u.telegram_id, COALESCE(u.username, ''), p.name_i18n,
			o.provision_attempts, COALESCE(o.last_error, ''), o.next_attempt_at,
			COALESCE('@' || c.username, c.telegram_id::text, ''), count(*) OVER ()
		FROM core.orders o
		JOIN core.users u ON u.id = o.user_id
		JOIN core.plans p ON p.id = o.plan_id
		LEFT JOIN core.users c ON c.id = o.created_by
		WHERE ($1 = '' OR (CASE WHEN $2 THEN u.telegram_id = $3 ELSE false END)
		           OR lower(u.username) LIKE $4 ESCAPE '\' OR ($5 <> '' AND replace(o.id::text, '-', '') LIKE '%' || $5 || '%'))
		  AND ($6 = '' OR o.status = $6)
		  AND ($7 = '' OR o.type = $7)
		  AND ($8 = '' OR o.user_id::text = $8)
		  AND ($9 = '' OR o.subscription_id::text = $9 OR o.id = (SELECT order_id FROM core.subscriptions WHERE id::text = $9))
		  AND ($10 = '' OR o.id::text = $10)
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $11 OFFSET $12`,
		strings.TrimSpace(f.Query), isNum, num, like, idPart, f.Status, f.Type, f.UserID, f.SubscriptionID, f.ID, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()
	var (
		out   []OrderRow
		total int64
	)
	for rows.Next() {
		var (
			r    OrderRow
			name []byte
		)
		dest := append(orderDest(&r.Order), &r.TelegramID, &r.Username, &name,
			&r.Attempts, &r.LastError, &r.NextAttemptAt, &r.CreatedByName, &total)
		if err := rows.Scan(dest...); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal(name, &r.PlanName); err != nil {
			return nil, 0, fmt.Errorf("plan name_i18n: %w", err)
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// OpenOrdersFor counts a service's renewals and top-ups that are paid but not
// applied yet (the worker would still change it on the panel).
func (s *Store) OpenOrdersFor(ctx context.Context, q querier, subscriptionID string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM core.orders
		WHERE subscription_id = $1 AND status IN ('paid','provisioning','provision_failed')`, subscriptionID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("open orders: %w", err)
	}
	return n, nil
}

// RetryOrderNow makes a failed delivery due at once. It reports whether the
// order was waiting for a retry.
func (s *Store) RetryOrderNow(ctx context.Context, q querier, id string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE core.orders SET next_attempt_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'provision_failed'`, id)
	if err != nil {
		return false, fmt.Errorf("retry order: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkOrderRefunded records a refund on its order.
func (s *Store) MarkOrderRefunded(ctx context.Context, q querier, id string, amount int64) error {
	tag, err := q.Exec(ctx, `UPDATE core.orders SET refunded_at = now(), refund_amount = $2, updated_at = now()
		WHERE id = $1 AND refunded_at IS NULL`, id, amount)
	if err != nil {
		return fmt.Errorf("mark order refunded: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LedgerFilter narrows the ledger. From and To bound created_at ([From, To)).
type LedgerFilter struct {
	UserID, Kind, Currency string
	From, To               *time.Time
}

// LedgerRow is one ledger entry with its user.
type LedgerRow struct {
	LedgerEntry
	TelegramID int64
	Username   string
}

const ledgerWhere = `
		WHERE ($1 = '' OR l.user_id::text = $1)
		  AND ($2 = '' OR l.kind = $2)
		  AND ($3 = '' OR l.currency = $3)
		  AND ($4::timestamptz IS NULL OR l.created_at >= $4)
		  AND ($5::timestamptz IS NULL OR l.created_at < $5)`

const ledgerColsL = `l.id, l.user_id, l.currency, l.amount, l.kind, l.ref_type, l.ref_id, l.idempotency_key, l.balance_after, l.created_at`

func scanLedgerRow(rows pgx.Rows, extra ...any) (LedgerRow, error) {
	var r LedgerRow
	e := &r.LedgerEntry
	dest := append([]any{&e.ID, &e.UserID, &e.Currency, &e.Amount, &e.Kind, &e.RefType, &e.RefID,
		&e.IdempotencyKey, &e.BalanceAfter, &e.CreatedAt, &r.TelegramID, &r.Username}, extra...)
	return r, rows.Scan(dest...)
}

// ListLedgerAll returns a page of ledger entries and the number of matches.
func (s *Store) ListLedgerAll(ctx context.Context, q querier, f LedgerFilter, p Page) ([]LedgerRow, int64, error) {
	p = p.clamp()
	rows, err := q.Query(ctx, `
		SELECT `+ledgerColsL+`, u.telegram_id, COALESCE(u.username, ''), count(*) OVER ()
		FROM core.ledger_entries l JOIN core.users u ON u.id = l.user_id`+ledgerWhere+`
		ORDER BY l.created_at DESC, l.id DESC
		LIMIT $6 OFFSET $7`, f.UserID, f.Kind, f.Currency, f.From, f.To, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list ledger: %w", err)
	}
	defer rows.Close()
	var (
		out   []LedgerRow
		total int64
	)
	for rows.Next() {
		r, err := scanLedgerRow(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// EachLedger calls fn for every matching entry, newest first, up to max
// entries; it reports whether more matched than max.
func (s *Store) EachLedger(ctx context.Context, q querier, f LedgerFilter, max int, fn func(LedgerRow) error) (bool, error) {
	rows, err := q.Query(ctx, `
		SELECT `+ledgerColsL+`, u.telegram_id, COALESCE(u.username, '')
		FROM core.ledger_entries l JOIN core.users u ON u.id = l.user_id`+ledgerWhere+`
		ORDER BY l.created_at DESC, l.id DESC
		LIMIT $6`, f.UserID, f.Kind, f.Currency, f.From, f.To, max+1)
	if err != nil {
		return false, fmt.Errorf("each ledger: %w", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		if n == max {
			return true, nil
		}
		r, err := scanLedgerRow(rows)
		if err != nil {
			return false, err
		}
		if err := fn(r); err != nil {
			return false, err
		}
		n++
	}
	return false, rows.Err()
}

// ReferrerRow is one inviter in the top-inviters list.
type ReferrerRow struct {
	UserID     string
	TelegramID int64
	Username   string
	Invited    int64
	Rewarded   int64
	Earned     map[string]int64 // rewards per currency
}

// TopReferrers lists the users who invited the most people.
func (s *Store) TopReferrers(ctx context.Context, q querier, limit int) ([]ReferrerRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := q.Query(ctx, `
		SELECT u.id, u.telegram_id, COALESCE(u.username, ''), i.invited
		FROM (SELECT referred_by, count(*) AS invited FROM core.users WHERE referred_by IS NOT NULL
		      GROUP BY referred_by ORDER BY count(*) DESC, referred_by LIMIT $1) i
		JOIN core.users u ON u.id = i.referred_by
		ORDER BY i.invited DESC, u.id`, limit)
	if err != nil {
		return nil, fmt.Errorf("top referrers: %w", err)
	}
	defer rows.Close()
	var (
		out []ReferrerRow
		ids []string
	)
	for rows.Next() {
		r := ReferrerRow{Earned: map[string]int64{}}
		if err := rows.Scan(&r.UserID, &r.TelegramID, &r.Username, &r.Invited); err != nil {
			return nil, err
		}
		out = append(out, r)
		ids = append(ids, r.UserID)
	}
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return out, err
	}
	erows, err := q.Query(ctx, `SELECT user_id, currency, count(*), sum(amount) FROM core.ledger_entries
		WHERE kind = 'referral' AND user_id = ANY($1) GROUP BY user_id, currency`, ids)
	if err != nil {
		return nil, fmt.Errorf("top referrers: rewards: %w", err)
	}
	defer erows.Close()
	idx := map[string]int{}
	for i, r := range out {
		idx[r.UserID] = i
	}
	for erows.Next() {
		var (
			id, cur string
			n, sum  int64
		)
		if err := erows.Scan(&id, &cur, &n, &sum); err != nil {
			return nil, err
		}
		r := &out[idx[id]]
		r.Rewarded += n
		r.Earned[cur] = sum
	}
	return out, erows.Err()
}

// PlanSales counts each plan's paid orders by customers (staff extensions
// and unpaid orders do not count).
func (s *Store) PlanSales(ctx context.Context, q querier) (map[string]int64, error) {
	rows, err := q.Query(ctx, `SELECT plan_id, count(*) FROM core.orders
		WHERE created_by IS NULL AND status IN ('paid','provisioning','active','provision_failed')
		GROUP BY plan_id`)
	if err != nil {
		return nil, fmt.Errorf("plan sales: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			id string
			n  int64
		)
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// UsersByID loads users by id; unknown ids are left out.
func (s *Store) UsersByID(ctx context.Context, q querier, ids []string) (map[string]User, error) {
	out := map[string]User{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+userCols+` FROM core.users WHERE id::text = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("users by id: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = *u
	}
	return out, rows.Err()
}
