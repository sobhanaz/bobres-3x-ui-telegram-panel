package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Sales reports: read-only aggregates over the ledger, the orders and core's
// own outbox (docs/14-reports.md defines every number). Amounts stay integers
// in the currency's minor unit and are only ever added within one currency.

// ReportTimeout bounds each statement of a report (PostgreSQL cancels it);
// ExportTimeout bounds each statement of a CSV export, which reads more rows.
// Variables, so a test can shorten them.
var (
	ReportTimeout = 15 * time.Second
	ExportTimeout = 60 * time.Second
)

// ErrBadTimezone: PostgreSQL does not know the time zone it was asked for.
var ErrBadTimezone = errors.New("store: unknown time zone")

// ErrReportTimeout: PostgreSQL cancelled a statement of a report at its time
// limit (SQLSTATE 57014): the range holds too much to read in time.
var ErrReportTimeout = errors.New("store: the report took too long")

// What payments' payment.rejected.v1 means. Payments publishes it for two
// different things, and core passes it on (domain.paymentRejected) with the
// intent's provider and the reason:
//
//   - a reviewer rejected a manual payment (provider in ManualProviders; the
//     reason is whatever staff typed, so it can say anything, "expired"
//     included): "rejected by staff";
//   - an automated gateway intent failed or ran out (Zarinpal, Telegram Stars,
//     automatic USDT; the reason is one of GatewayFailureReasons): "failed or
//     expired gateway payments".
//
// The provider decides. An event without one (published before core passed it
// on) is told by its reason: one of GatewayFailureReasons is a gateway failure,
// anything else is a staff-typed reason. A provider that is not manual is a
// gateway, so a new gateway lands on the gateway line, never on the staff one.
// Both lists name what payments emits today (internal/payments/domain/
// gateway.go, stars.go and gateway/zarinpal); a test reads those sources, so
// a new reason or a new manual provider fails it until it is added here on
// purpose.
var (
	// ManualProviders are the payment methods whose payments staff review.
	ManualProviders = []string{"manual_card", "manual_crypto", "manual_zarinpal"}
	// GatewayFailureReasons are the reasons an automated payment fails with.
	GatewayFailureReasons = []string{"gateway_failed", "amount_mismatch", "expired", "not_found", "reversed", "payer_mismatch"}
)

// ReportQuery says what a report covers: the local dates From to To, both
// included ("2006-01-02"), read in time zone TZ (an IANA name), and the size
// of a bucket (day, week or month; a week starts on Monday). The caller has
// validated every field.
type ReportQuery struct {
	From, To string
	TZ       string
	Group    string
}

// The range ($1 From, $2 To, $3 zone), written once so every query agrees on
// the edges. col is the column the range applies to. The exact test is the
// local date: it is what a bucket is made of (date_trunc of the local time), so
// a row is in the range exactly when it is in one of the range's buckets, even
// on a day a zone's clocks go back past midnight (America/Havana on 1 Nov 2026
// has two hours 00:xx, and local midnight as an instant is only the second). The
// instant bounds before it only let the index narrow the scan: they are a day
// wider on each side than the dates, so they can never cut a row the date test
// would keep.
func rangeOf(col string) string {
	return col + ` >= ($1::date - 1)::timestamp AT TIME ZONE $3::text
		AND ` + col + ` < ($2::date + 2)::timestamp AT TIME ZONE $3::text
		AND (` + col + ` AT TIME ZONE $3::text)::date BETWEEN $1::date AND $2::date`
}

// bucketOf is the local start date of the bucket an instant falls in.
func bucketOf(col string) string {
	return `to_char(date_trunc($4::text, ` + col + ` AT TIME ZONE $3::text), 'YYYY-MM-DD')`
}

// ReportFlow is how many things happened and the money they carried.
type ReportFlow struct {
	Count  int64
	Amount int64
}

// ReportCell is one bucket's numbers in one currency.
type ReportCell struct {
	// Revenue is what customers paid for orders: the sum of the ledger's
	// purchase entries. Orders counts them; NewOrders, Renewals and
	// TrafficTopups split Orders by the order's type.
	Revenue       int64
	Orders        int64
	NewOrders     int64
	Renewals      int64
	TrafficTopups int64
	// Topups is the money of payments that stayed in a wallet (the part no
	// order took at the moment it arrived); Refunds are orders credited back to
	// a wallet; Rejected are manual payments a reviewer rejected;
	// GatewayFailed are automated gateway payments that failed or expired
	// (see ManualProviders); Cancelled are orders that ended unpaid.
	Topups        ReportFlow
	Refunds       ReportFlow
	Rejected      ReportFlow
	GatewayFailed ReportFlow
	Cancelled     ReportFlow
}

// SalesData is the sparse answer of SalesSummary: only buckets and
// currencies where something happened. Keys are the bucket's local start
// date ("2006-01-02").
type SalesData struct {
	Cells map[string]map[string]*ReportCell // bucket -> currency -> numbers
	// Customers whose first paid order fell in the bucket, and free trials
	// started in it. They count people, so they have no currency.
	NewCustomers map[string]int64
	Trials       map[string]int64
}

func (d *SalesData) cell(bucket, currency string) *ReportCell {
	b := d.Cells[bucket]
	if b == nil {
		b = map[string]*ReportCell{}
		d.Cells[bucket] = b
	}
	c := b[currency]
	if c == nil {
		c = &ReportCell{}
		b[currency] = c
	}
	return c
}

// readOnly runs fn in a read-only transaction on one snapshot (every query of
// a report sees the same data) whose statements PostgreSQL cancels after
// timeout.
func (s *Store) readOnly(ctx context.Context, timeout time.Duration, fn func(tx pgx.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("core store: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // nothing to commit
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL statement_timeout = %d`, timeout.Milliseconds())); err != nil {
		return fmt.Errorf("report timeout: %w", err)
	}
	return fn(tx)
}

// reportErr names what failed, and tells a time zone PostgreSQL does not
// know (SQLSTATE 22023) and a statement that ran out of time (57014) from a
// real failure.
func reportErr(what string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "22023" && strings.Contains(pgErr.Message, "time zone"):
			return fmt.Errorf("%w: %s", ErrBadTimezone, pgErr.Message)
		case pgErr.Code == "57014" && strings.Contains(pgErr.Message, "statement timeout"):
			return fmt.Errorf("%w: %s", ErrReportTimeout, what)
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

// SalesSummary reads the report's numbers per bucket and currency.
func (s *Store) SalesSummary(ctx context.Context, q ReportQuery) (*SalesData, error) {
	d := &SalesData{Cells: map[string]map[string]*ReportCell{}, NewCustomers: map[string]int64{}, Trials: map[string]int64{}}
	args := []any{q.From, q.To, q.TZ, q.Group}
	err := s.readOnly(ctx, ReportTimeout, func(tx pgx.Tx) error {
		// Revenue and paid orders: one purchase entry per paid order, dated
		// by the entry (an order has no "paid at" of its own).
		if err := each(ctx, tx, `
			SELECT `+bucketOf("e.created_at")+`, e.currency, count(*), COALESCE(sum(-e.amount), 0)::bigint,
				count(*) FILTER (WHERE o.type = 'new'), count(*) FILTER (WHERE o.type = 'renew'),
				count(*) FILTER (WHERE o.type = 'traffic_topup')
			FROM core.ledger_entries e LEFT JOIN core.orders o ON e.ref_type = 'order' AND o.id = e.ref_id
			WHERE e.kind = 'purchase' AND `+rangeOf("e.created_at")+`
			GROUP BY 1, 2`, args, func(r pgx.Rows) error {
			var b, cur string
			var o, rev, newO, ren, tt int64
			if err := r.Scan(&b, &cur, &o, &rev, &newO, &ren, &tt); err != nil {
				return err
			}
			c := d.cell(b, cur)
			c.Orders, c.Revenue, c.NewOrders, c.Renewals, c.TrafficTopups = o, rev, newO, ren, tt
			return nil
		}); err != nil {
			return reportErr("report purchases", err)
		}
		// New customers: a purchase with no earlier purchase by the same user
		// (ties on time are broken by the entry id, which is time-ordered).
		if err := each(ctx, tx, `
			SELECT `+bucketOf("e.created_at")+`, count(*)
			FROM core.ledger_entries e
			WHERE e.kind = 'purchase' AND `+rangeOf("e.created_at")+`
				AND NOT EXISTS (SELECT 1 FROM core.ledger_entries p WHERE p.user_id = e.user_id AND p.kind = 'purchase'
					AND (p.created_at, p.id) < (e.created_at, e.id))
			GROUP BY 1`, args, func(r pgx.Rows) error {
			var b string
			var n int64
			if err := r.Scan(&b, &n); err != nil {
				return err
			}
			d.NewCustomers[b] = n
			return nil
		}); err != nil {
			return reportErr("report new customers", err)
		}
		// Free trials: one trial_grant entry per user, ever.
		if err := each(ctx, tx, `
			SELECT `+bucketOf("e.created_at")+`, count(*)
			FROM core.ledger_entries e WHERE e.kind = 'trial_grant' AND `+rangeOf("e.created_at")+` GROUP BY 1`, args, func(r pgx.Rows) error {
			var b string
			var n int64
			if err := r.Scan(&b, &n); err != nil {
				return err
			}
			d.Trials[b] = n
			return nil
		}); err != nil {
			return reportErr("report trials", err)
		}
		flow := func(sql string, into func(c *ReportCell) *ReportFlow) error {
			return each(ctx, tx, sql, args, func(r pgx.Rows) error {
				var b, cur string
				var f ReportFlow
				if err := r.Scan(&b, &cur, &f.Count, &f.Amount); err != nil {
					return err
				}
				*into(d.cell(b, cur)) = f
				return nil
			})
		}
		// Wallet top-ups: a payment's whole amount is credited to the wallet
		// and, when the payment was for an order, the order's price is taken
		// from it in the same transaction, so the entries carry the same
		// time. What a topup entry has left after the purchases of the same
		// user and currency at its instant stayed in the wallet: all of it for
		// a payment with no order, none for an exact payment, the surplus for
		// one that paid more than the price. An entry with nothing left is
		// not a top-up.
		if err := flow(`
			SELECT b, currency, count(*), sum(rest)::bigint FROM (
				SELECT `+bucketOf("e.created_at")+` AS b, e.currency,
					e.amount - COALESCE((SELECT sum(-p.amount) FROM core.ledger_entries p WHERE p.user_id = e.user_id
						AND p.currency = e.currency AND p.kind = 'purchase' AND p.created_at = e.created_at), 0) AS rest
				FROM core.ledger_entries e WHERE e.kind = 'topup' AND `+rangeOf("e.created_at")+`) t
			WHERE rest > 0 GROUP BY 1, 2`, func(c *ReportCell) *ReportFlow { return &c.Topups }); err != nil {
			return reportErr("report topups", err)
		}
		if err := flow(`
			SELECT `+bucketOf("e.created_at")+`, e.currency, count(*), COALESCE(sum(e.amount), 0)::bigint
			FROM core.ledger_entries e WHERE e.kind = 'refund' AND `+rangeOf("e.created_at")+` GROUP BY 1, 2`,
			func(c *ReportCell) *ReportFlow { return &c.Refunds }); err != nil {
			return reportErr("report refunds", err)
		}
		// Payments that were rejected or failed: core records each one as an
		// event. Who rejected it is told as ManualProviders says ($5 the
		// manual providers, $6 the gateway failure reasons).
		if err := each(ctx, tx, `
			SELECT `+bucketOf("o.created_at")+`, o.payload->>'currency',
				CASE WHEN COALESCE(o.payload->>'provider', '') <> '' THEN o.payload->>'provider' = ANY($5::text[])
					ELSE NOT (COALESCE(o.payload->>'reason', '') = ANY($6::text[])) END AS by_staff,
				count(*), COALESCE(sum((o.payload->>'amount')::bigint), 0)::bigint
			FROM core.outbox_core o WHERE o.topic = 'payment.rejected.v1' AND o.payload->>'currency' IS NOT NULL AND `+rangeOf("o.created_at")+`
			GROUP BY 1, 2, 3`, []any{q.From, q.To, q.TZ, q.Group, ManualProviders, GatewayFailureReasons}, func(r pgx.Rows) error {
			var b, cur string
			var staff bool
			var f ReportFlow
			if err := r.Scan(&b, &cur, &staff, &f.Count, &f.Amount); err != nil {
				return err
			}
			c := d.cell(b, cur)
			if staff {
				c.Rejected = f
			} else {
				c.GatewayFailed = f
			}
			return nil
		}); err != nil {
			return reportErr("report rejected", err)
		}
		// Orders that ended unpaid, by when they ended (nothing changes an
		// order after that). A refunded order is cancelled too, but it was
		// paid: it is a refund, not a cancellation.
		if err := flow(`
			SELECT `+bucketOf("o.updated_at")+`, o.currency, count(*), COALESCE(sum(o.amount), 0)::bigint
			FROM core.orders o WHERE o.status IN ('cancelled', 'expired') AND o.refunded_at IS NULL AND `+rangeOf("o.updated_at")+` GROUP BY 1, 2`,
			func(c *ReportCell) *ReportFlow { return &c.Cancelled }); err != nil {
			return reportErr("report cancelled", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// each runs a query and calls fn for every row.
func each(ctx context.Context, q querier, sql string, args []any, fn func(pgx.Rows) error) error {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// MethodRow is the paid orders of one currency that were paid one way.
type MethodRow struct {
	// Method is how the order was paid: "wallet" (the balance), "manual"
	// (a payment staff approved), "gateway" (Zarinpal, Stars or automatic
	// USDT confirmed it), or "unknown" (a purchase with no matching event).
	Method   string
	Currency string
	Orders   int64
	Revenue  int64
}

// SalesByMethod splits the range's revenue by how the orders were paid. Each
// paid order publishes order.paid.v1 in the transaction of its purchase, so
// the events carry the same orders and amounts as the ledger; a purchase
// without its event is listed as "unknown", so the rows always add up to the
// revenue of the range.
func (s *Store) SalesByMethod(ctx context.Context, q ReportQuery) ([]MethodRow, error) {
	args := []any{q.From, q.To, q.TZ}
	var out []MethodRow
	err := s.readOnly(ctx, ReportTimeout, func(tx pgx.Tx) error {
		total := map[string]*MethodRow{}
		if err := each(ctx, tx, `
			SELECT e.currency, count(*), COALESCE(sum(-e.amount), 0)::bigint
			FROM core.ledger_entries e WHERE e.kind = 'purchase' AND `+rangeOf("e.created_at")+` GROUP BY 1`, args, func(r pgx.Rows) error {
			var m MethodRow
			if err := r.Scan(&m.Currency, &m.Orders, &m.Revenue); err != nil {
				return err
			}
			total[m.Currency] = &m
			return nil
		}); err != nil {
			return reportErr("report methods: totals", err)
		}
		if err := each(ctx, tx, `
			SELECT COALESCE(o.payload->>'source', ''), o.payload->>'currency', count(*), COALESCE(sum((o.payload->>'amount')::bigint), 0)::bigint
			FROM core.outbox_core o
			WHERE o.topic = 'order.paid.v1' AND (o.payload->>'amount')::bigint > 0 AND `+rangeOf("o.created_at")+`
			GROUP BY 1, 2`, args, func(r pgx.Rows) error {
			var m MethodRow
			if err := r.Scan(&m.Method, &m.Currency, &m.Orders, &m.Revenue); err != nil {
				return err
			}
			out = append(out, m)
			if t := total[m.Currency]; t != nil {
				t.Orders -= m.Orders
				t.Revenue -= m.Revenue
			} else {
				total[m.Currency] = &MethodRow{Currency: m.Currency, Orders: -m.Orders, Revenue: -m.Revenue}
			}
			return nil
		}); err != nil {
			return reportErr("report methods", err)
		}
		for _, t := range total {
			if t.Orders != 0 || t.Revenue != 0 {
				out = append(out, MethodRow{Method: "unknown", Currency: t.Currency, Orders: t.Orders, Revenue: t.Revenue})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Currency != b.Currency {
			return a.Currency < b.Currency
		}
		if a.Revenue != b.Revenue {
			return a.Revenue > b.Revenue
		}
		return a.Method < b.Method
	})
	return out, nil
}

// PlanRow is what one plan sold in one currency.
type PlanRow struct {
	PlanID   string
	Name     map[string]string
	Currency string
	Orders   int64
	Revenue  int64
}

// TopPlans lists, for every currency, the plans that sold most in the range
// (by revenue, then by orders), at most perCurrency of them.
func (s *Store) TopPlans(ctx context.Context, q ReportQuery, perCurrency int) ([]PlanRow, error) {
	args := []any{q.From, q.To, q.TZ, perCurrency}
	var out []PlanRow
	err := s.readOnly(ctx, ReportTimeout, func(tx pgx.Tx) error {
		return each(ctx, tx, `
			SELECT plan_id, name_i18n, currency, orders, revenue FROM (
				SELECT o.plan_id::text AS plan_id, p.name_i18n, e.currency, count(*) AS orders, sum(-e.amount)::bigint AS revenue,
					row_number() OVER (PARTITION BY e.currency ORDER BY sum(-e.amount) DESC, count(*) DESC, o.plan_id) AS rank
				FROM core.ledger_entries e
				JOIN core.orders o ON e.ref_type = 'order' AND o.id = e.ref_id
				JOIN core.plans p ON p.id = o.plan_id
				WHERE e.kind = 'purchase' AND `+rangeOf("e.created_at")+`
				GROUP BY o.plan_id, p.name_i18n, e.currency) t
			WHERE rank <= $4::int
			ORDER BY currency, revenue DESC, orders DESC, plan_id`, args, func(r pgx.Rows) error {
			var p PlanRow
			if err := r.Scan(&p.PlanID, &p.Name, &p.Currency, &p.Orders, &p.Revenue); err != nil {
				return err
			}
			out = append(out, p)
			return nil
		})
	})
	if err != nil {
		return nil, reportErr("report plans", err)
	}
	return out, nil
}

// PaidOrderRow is one paid order of the export.
type PaidOrderRow struct {
	PaidAt     time.Time
	PaidLocal  string // PaidAt in the report's time zone, "2006-01-02 15:04:05"
	OrderID    string
	Type       string
	TelegramID int64
	Username   string
	PlanName   map[string]string
	Currency   string
	Amount     int64
	Method     string // wallet, manual, gateway; "" when unknown
	Refunded   int64  // what was refunded (0: nothing)
}

// paidOrdersSQL is the export's query. How an order was paid is one index
// probe per order (a LATERAL join on outbox_core_order_paid_idx), so the plan
// is the same at any volume: PostgreSQL cannot turn this join into a hash or a
// materialized scan of the whole outbox, which it did for a join on a subquery
// once the range held about 100,000 orders. $1-$3 are the range, $4 the most
// rows wanted.
func paidOrdersSQL() string {
	return `
	SELECT e.created_at, to_char(e.created_at AT TIME ZONE $3::text, 'YYYY-MM-DD HH24:MI:SS'), e.ref_id::text, o.type,
		u.telegram_id, COALESCE(u.username, ''), p.name_i18n, e.currency, -e.amount, COALESCE(ev.method, ''),
		COALESCE(o.refund_amount, 0)
	FROM core.ledger_entries e
	JOIN core.orders o ON e.ref_type = 'order' AND o.id = e.ref_id
	JOIN core.users u ON u.id = e.user_id
	JOIN core.plans p ON p.id = o.plan_id
	LEFT JOIN LATERAL (SELECT x.payload->>'source' AS method FROM core.outbox_core x
		WHERE x.topic = 'order.paid.v1' AND x.payload->>'order_id' = e.ref_id::text LIMIT 1) ev ON true
	WHERE e.kind = 'purchase' AND ` + rangeOf("e.created_at") + `
	ORDER BY e.created_at, e.id
	LIMIT $4`
}

// EachPaidOrder calls fn for the orders paid in the range, oldest first, up to
// max of them; it reports whether more were paid than max. The rows are
// streamed from the database, one at a time.
func (s *Store) EachPaidOrder(ctx context.Context, q ReportQuery, max int, fn func(PaidOrderRow) error) (bool, error) {
	more := false
	err := s.readOnly(ctx, ExportTimeout, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, paidOrdersSQL(), q.From, q.To, q.TZ, max+1)
		if err != nil {
			return reportErr("export paid orders", err)
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			if n == max {
				more = true
				return nil
			}
			var r PaidOrderRow
			if err := rows.Scan(&r.PaidAt, &r.PaidLocal, &r.OrderID, &r.Type, &r.TelegramID, &r.Username, &r.PlanName,
				&r.Currency, &r.Amount, &r.Method, &r.Refunded); err != nil {
				return err
			}
			if err := fn(r); err != nil {
				return err
			}
			n++
		}
		if err := rows.Err(); err != nil {
			return reportErr("export paid orders", err) // the zone error arrives here
		}
		return nil
	})
	return more, err
}
