package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

// Internal orders (docs/14-reports.md, "Internal orders"): with the switch ExcludeOwnerOrdersKey on, what an owner account buys, tops up and
// is refunded is stamped internal when the row is written, and the reports, the overview, the daily summary and the growth rewards leave
// internal rows out. The stamp lives on the row (orders.internal, ledger_entries.internal; migration 00059), so the switch decides only
// what is written from now on and never rewrites history. Only role owner counts.

// ExcludeOwnerOrdersKey is the setting (group reports, "true" or "false", off by default) that stamps owner accounts' new rows internal.
const ExcludeOwnerOrdersKey = "reports.exclude_owner_orders"

// internalOf is the SQL that decides whether a row of the user named by userParam ("$2") is internal now: the switch is on and the user is
// an owner. It reads the setting and the role in the statement that writes the row, so the decision and the row share a transaction.
func internalOf(userParam string) string {
	return `(COALESCE((SELECT s.value #>> '{}' = 'true' FROM core.settings s WHERE s.key = '` + ExcludeOwnerOrdersKey + `'), false)
		AND EXISTS (SELECT 1 FROM core.users u WHERE u.id = ` + userParam + ` AND u.role = 'owner'))`
}

// ledgerInternal is the internal flag of a new ledger entry, for insertLedger's parameters ($2 user, $5 kind, $6 ref type, $7 ref id). What
// gives something back carries the flag of what it gives back, however the switch stands now, so an internal purchase's refund is internal
// and a customer's is not: the order's own flag for the entries of an order (its purchase, refund, cashback and the cashback's reversal), the
// purchase's for a license's or a digital account's refund. Everything else is decided as a new order is (internalOf).
var ledgerInternal = `COALESCE(
		CASE WHEN $5::text IN ('purchase', 'refund', 'cashback', 'cashback_reversal') AND $6::text = 'order'
			THEN (SELECT o.internal FROM core.orders o WHERE o.id = $7::uuid) END,
		CASE $5::text
			WHEN 'license_refund' THEN (SELECT p.internal FROM core.ledger_entries p WHERE p.kind = 'license_purchase' AND p.ref_type = $6::text AND p.ref_id = $7::uuid LIMIT 1)
			WHEN 'supplier_refund' THEN (SELECT p.internal FROM core.ledger_entries p WHERE p.kind = 'supplier_purchase' AND p.ref_type = $6::text AND p.ref_id = $7::uuid LIMIT 1)
		END,
		` + internalOf("$2::uuid") + `)`

// ExcludeOwnerOrders reports whether the switch is on.
func (s *Store) ExcludeOwnerOrders(ctx context.Context, q querier) (bool, error) {
	var on bool
	if err := q.QueryRow(ctx, `SELECT COALESCE((SELECT value #>> '{}' = 'true' FROM core.settings WHERE key = $1), false)`, ExcludeOwnerOrdersKey).Scan(&on); err != nil {
		return false, fmt.Errorf("exclude owner orders: %w", err)
	}
	return on, nil
}

// InternalCurrency is what owner accounts' rows moved in one currency: the purchases (orders, licenses, digital accounts) that left
// revenue, and what refunds gave back of them.
type InternalCurrency struct {
	Currency      string
	Orders        int64
	Amount        int64
	Refunds       int64
	RefundsAmount int64
}

// InternalAccount is one owner account's share of the internal purchases of a range.
type InternalAccount struct {
	UserID     string
	TelegramID int64
	Username   string
	// Orders is the account's internal purchases of every currency; Currencies has the money, one row for each currency.
	Orders     int64
	Currencies []InternalCurrency
}

// InternalReport is the "Internal orders" card of the Reports page: the internal purchases (and what refunds gave back of them) of the
// range, per currency and per account. Amounts of different currencies are never added.
type InternalReport struct {
	Orders     int64
	Currencies []InternalCurrency
	Accounts   []InternalAccount
}

// InternalOrders reads the internal rows of a range. It uses the range of the reports (their time zone, the start-from-zero instant) and the
// same purchase and refund kinds, so a purchase leaves revenue and appears here, once. Accounts are the most active first.
func (s *Store) InternalOrders(ctx context.Context, q ReportQuery) (*InternalReport, error) {
	rep := &InternalReport{Currencies: []InternalCurrency{}, Accounts: []InternalAccount{}}
	args := []any{q.From, q.To, q.TZ}
	err := s.readOnly(ctx, ReportTimeout, func(tx pgx.Tx) error {
		totals := map[string]*InternalCurrency{}
		accounts := map[string]*InternalAccount{}
		err := each(ctx, tx, `
			SELECT e.user_id::text, u.telegram_id, COALESCE(u.username, ''), e.currency,
				count(*) FILTER (WHERE e.kind IN `+purchaseKinds+`), COALESCE(sum(-e.amount) FILTER (WHERE e.kind IN `+purchaseKinds+`), 0)::bigint,
				count(*) FILTER (WHERE e.kind IN `+refundKinds+`), COALESCE(sum(e.amount) FILTER (WHERE e.kind IN `+refundKinds+`), 0)::bigint
			FROM core.ledger_entries e JOIN core.users u ON u.id = e.user_id
			WHERE e.internal AND (e.kind IN `+purchaseKinds+` OR e.kind IN `+refundKinds+`) AND `+rangeOf("e.created_at")+`
			GROUP BY 1, 2, 3, 4
			ORDER BY 4, 2`, args, func(r pgx.Rows) error {
			var (
				id, name string
				tg       int64
				c        InternalCurrency
			)
			if err := r.Scan(&id, &tg, &name, &c.Currency, &c.Orders, &c.Amount, &c.Refunds, &c.RefundsAmount); err != nil {
				return err
			}
			t := totals[c.Currency]
			if t == nil {
				t = &InternalCurrency{Currency: c.Currency}
				totals[c.Currency] = t
			}
			t.Orders += c.Orders
			t.Amount += c.Amount
			t.Refunds += c.Refunds
			t.RefundsAmount += c.RefundsAmount
			a := accounts[id]
			if a == nil {
				a = &InternalAccount{UserID: id, TelegramID: tg, Username: name}
				accounts[id] = a
			}
			a.Orders += c.Orders
			a.Currencies = append(a.Currencies, c)
			return nil
		})
		if err != nil {
			return reportErr("report internal orders", err)
		}
		for _, t := range totals {
			rep.Orders += t.Orders
			rep.Currencies = append(rep.Currencies, *t)
		}
		for _, a := range accounts {
			rep.Accounts = append(rep.Accounts, *a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(rep.Currencies, func(i, j int) bool { return rep.Currencies[i].Currency < rep.Currencies[j].Currency })
	sort.Slice(rep.Accounts, func(i, j int) bool {
		a, b := rep.Accounts[i], rep.Accounts[j]
		if a.Orders != b.Orders {
			return a.Orders > b.Orders
		}
		return a.TelegramID < b.TelegramID
	})
	return rep, nil
}

// InternalMove is what setting the owner accounts' rows internal (or back) touches: the orders and ledger entries, and the money of the
// purchases and refunds among them per currency (Orders is the number of purchases).
type InternalMove struct {
	Orders     int64
	Entries    int64
	Currencies []InternalCurrency
}

// OwnerRowsToMove counts what SetOwnerRowsInternal(to) would change, without changing it: the owner accounts' orders and ledger entries
// whose flag is not to yet. It is the dry run the dashboard shows before it asks to confirm.
func (s *Store) OwnerRowsToMove(ctx context.Context, q querier, to bool) (*InternalMove, error) {
	m := &InternalMove{Currencies: []InternalCurrency{}}
	if err := q.QueryRow(ctx, `SELECT count(*) FROM core.orders o JOIN core.users u ON u.id = o.user_id WHERE u.role = 'owner' AND o.internal <> $1`, to).
		Scan(&m.Orders); err != nil {
		return nil, fmt.Errorf("owner orders to move: %w", err)
	}
	rows, err := q.Query(ctx, `
		SELECT e.currency, count(*) FILTER (WHERE e.kind IN `+purchaseKinds+`), COALESCE(sum(-e.amount) FILTER (WHERE e.kind IN `+purchaseKinds+`), 0)::bigint,
			count(*) FILTER (WHERE e.kind IN `+refundKinds+`), COALESCE(sum(e.amount) FILTER (WHERE e.kind IN `+refundKinds+`), 0)::bigint, count(*)
		FROM core.ledger_entries e JOIN core.users u ON u.id = e.user_id
		WHERE u.role = 'owner' AND e.internal <> $1
		GROUP BY 1 ORDER BY 1`, to)
	if err != nil {
		return nil, fmt.Errorf("owner entries to move: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			c InternalCurrency
			n int64
		)
		if err := rows.Scan(&c.Currency, &c.Orders, &c.Amount, &c.Refunds, &c.RefundsAmount, &n); err != nil {
			return nil, err
		}
		m.Entries += n
		if c.Orders != 0 || c.Refunds != 0 {
			m.Currencies = append(m.Currencies, c)
		}
	}
	return m, rows.Err()
}

// SetOwnerRowsInternal sets the flag of every order and ledger entry of the owner accounts (users whose role is owner now) to internal, in
// tx, and reports how many rows changed. Rows that already have the value are not touched, so a second call changes nothing. Only the flag
// is written: no amount, status, time or balance (the ledger's trigger allows nothing else).
func (s *Store) SetOwnerRowsInternal(ctx context.Context, tx pgx.Tx, internal bool) (orders, entries int64, err error) {
	tag, err := tx.Exec(ctx, `UPDATE core.orders o SET internal = $1 FROM core.users u WHERE u.id = o.user_id AND u.role = 'owner' AND o.internal <> $1`, internal)
	if err != nil {
		return 0, 0, fmt.Errorf("flag owner orders: %w", err)
	}
	orders = tag.RowsAffected()
	tag, err = tx.Exec(ctx, `UPDATE core.ledger_entries e SET internal = $1 FROM core.users u WHERE u.id = e.user_id AND u.role = 'owner' AND e.internal <> $1`, internal)
	if err != nil {
		return 0, 0, fmt.Errorf("flag owner ledger entries: %w", err)
	}
	return orders, tag.RowsAffected(), nil
}
