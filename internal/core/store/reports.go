package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
//   - an automated gateway intent failed or ran out (Zarinpal, IDPay, NextPay, Telegram Stars,
//     automatic USDT; the reason is one of GatewayFailureReasons): "failed or
//     expired gateway payments".
//
// The provider decides. An event without one (published before core passed it
// on) is told by its reason: one of GatewayFailureReasons is a gateway failure,
// anything else is a staff-typed reason. A provider that is not manual is a
// gateway, so a new gateway lands on the gateway line, never on the staff one.
// Both lists name what payments emits today (internal/payments/domain/
// gateway.go, stars.go and gateway/*); a test reads those sources, so
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
	// ChurnAsOf is "now" for the churn numbers (zero: the current time) and
	// ChurnGraceDays how many days a service may stay expired before it counts as
	// churned (docs/14-reports.md). Both are parameters so a test can fix them.
	ChurnAsOf      time.Time
	ChurnGraceDays int
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
//
// The range also stops at the instant the owner started the reports from zero (ReportsSinceKey): nothing before it is counted, whatever
// dates were asked for. readOnly hands that instant to the transaction ("bobres.reports_since"; unset: no cutoff).
func rangeOf(col string) string {
	return col + ` >= ($1::date - 1)::timestamp AT TIME ZONE $3::text
		AND ` + col + ` < ($2::date + 2)::timestamp AT TIME ZONE $3::text
		AND (` + col + ` AT TIME ZONE $3::text)::date BETWEEN $1::date AND $2::date
		AND ` + col + ` >= COALESCE(NULLIF(current_setting('bobres.reports_since', true), '')::timestamptz, '-infinity'::timestamptz)`
}

// ReportsSinceKey is the setting that holds the instant (RFC 3339, UTC) the reports and the overview count from. The owner sets it with
// "start from zero" when the sales so far were tests; nothing is deleted, the orders, payments, wallets and the ledger stay as they are.
const ReportsSinceKey = "reports.since"

// ReportsSince is the instant the reports count from; false when they count everything.
func (s *Store) ReportsSince(ctx context.Context, q querier) (time.Time, bool, error) {
	var v *string
	if err := q.QueryRow(ctx, `SELECT value #>> '{}' FROM core.settings WHERE key = $1`, ReportsSinceKey).Scan(&v); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("reports since: %w", err)
	}
	if v == nil || *v == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339, *v)
	if err != nil {
		return time.Time{}, false, nil // an unreadable value counts as none rather than hiding every sale
	}
	return t, true, nil
}

// bucketOf is the local start date of the bucket an instant falls in.
func bucketOf(col string) string {
	return `to_char(date_trunc($4::text, ` + col + ` AT TIME ZONE $3::text), 'YYYY-MM-DD')`
}

// The ledger entries that are sales, and the ones that give a sale back: the orders for VPN services, the licenses the store sells and the
// digital accounts it resells all take money from a customer's wallet, and every one of them is revenue.
const (
	purchaseKinds = `('purchase', 'license_purchase', 'supplier_purchase')`
	refundKinds   = `('refund', 'license_refund', 'supplier_refund')`
)

// ReportFlow is how many things happened and the money they carried.
type ReportFlow struct {
	Count  int64
	Amount int64
}

// ReportCell is one bucket's numbers in one currency.
type ReportCell struct {
	// Revenue is what customers paid: the sum of the ledger's purchase
	// entries, which are the orders for VPN services, the licenses the store
	// sells and the digital accounts it resells (PurchaseKinds). Orders counts
	// them; NewOrders, Renewals and TrafficTopups split the VPN orders by the
	// order's type, and Licenses and Digital say how much of Orders and Revenue
	// the other two made.
	Revenue       int64
	Orders        int64
	NewOrders     int64
	Renewals      int64
	TrafficTopups int64
	Licenses      ReportFlow
	Digital       ReportFlow
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
	// Cashback is what the store gave back to its customers' wallets (docs/14-reports.md): Count is the cashback credits of the bucket and
	// Amount what they came to less the cashback that refunds took back in the bucket (so it can be negative in a bucket that only refunded).
	// It is not revenue and is not taken off Net: it is credit the customers may spend.
	Cashback ReportFlow
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
	// Churn, by the local date bucket the service's (or, for ChurnedCustomers,
	// the customer's last) expiry falls in: services that ended and stayed
	// ended past the grace period, the people whose last service did, and
	// services that ended inside the grace period (not churn yet). No
	// currency.
	ChurnedServices  map[string]int64
	ChurnedCustomers map[string]int64
	AtRisk           map[string]int64
}

// NewSalesData is an empty SalesData.
func NewSalesData() *SalesData {
	return &SalesData{Cells: map[string]map[string]*ReportCell{}, NewCustomers: map[string]int64{}, Trials: map[string]int64{},
		ChurnedServices: map[string]int64{}, ChurnedCustomers: map[string]int64{}, AtRisk: map[string]int64{}}
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
	// The instant the reports count from, for rangeOf (an unset or unreadable value: all of time).
	since := ""
	if t, ok, err := s.ReportsSince(ctx, tx); err != nil {
		return err
	} else if ok {
		since = t.UTC().Format(time.RFC3339Nano)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('bobres.reports_since', $1, true)`, since); err != nil {
		return fmt.Errorf("report start: %w", err)
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
	d := NewSalesData()
	args := []any{q.From, q.To, q.TZ, q.Group}
	err := s.readOnly(ctx, ReportTimeout, func(tx pgx.Tx) error {
		// Revenue and paid orders: one purchase entry per paid order, dated
		// by the entry (an order has no "paid at" of its own).
		if err := each(ctx, tx, `
			SELECT `+bucketOf("e.created_at")+`, e.currency, count(*), COALESCE(sum(-e.amount), 0)::bigint,
				count(*) FILTER (WHERE o.type = 'new'), count(*) FILTER (WHERE o.type = 'renew'),
				count(*) FILTER (WHERE o.type = 'traffic_topup'),
				count(*) FILTER (WHERE e.kind = 'license_purchase'), COALESCE(sum(-e.amount) FILTER (WHERE e.kind = 'license_purchase'), 0)::bigint,
				count(*) FILTER (WHERE e.kind = 'supplier_purchase'), COALESCE(sum(-e.amount) FILTER (WHERE e.kind = 'supplier_purchase'), 0)::bigint
			FROM core.ledger_entries e LEFT JOIN core.orders o ON e.ref_type = 'order' AND o.id = e.ref_id
			WHERE e.kind IN `+purchaseKinds+` AND `+rangeOf("e.created_at")+`
			GROUP BY 1, 2`, args, func(r pgx.Rows) error {
			var b, cur string
			var o, rev, newO, ren, tt int64
			var lic, dig ReportFlow
			if err := r.Scan(&b, &cur, &o, &rev, &newO, &ren, &tt, &lic.Count, &lic.Amount, &dig.Count, &dig.Amount); err != nil {
				return err
			}
			c := d.cell(b, cur)
			c.Orders, c.Revenue, c.NewOrders, c.Renewals, c.TrafficTopups = o, rev, newO, ren, tt
			c.Licenses, c.Digital = lic, dig
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
			FROM core.ledger_entries e WHERE e.kind IN `+refundKinds+` AND `+rangeOf("e.created_at")+` GROUP BY 1, 2`,
			func(c *ReportCell) *ReportFlow { return &c.Refunds }); err != nil {
			return reportErr("report refunds", err)
		}
		// Cashback paid: the credits and, taken off, the reversals that refunds made.
		if err := flow(`
			SELECT `+bucketOf("e.created_at")+`, e.currency, count(*) FILTER (WHERE e.kind = 'cashback'), COALESCE(sum(e.amount), 0)::bigint
			FROM core.ledger_entries e WHERE e.kind IN ('cashback', 'cashback_reversal') AND `+rangeOf("e.created_at")+` GROUP BY 1, 2`,
			func(c *ReportCell) *ReportFlow { return &c.Cashback }); err != nil {
			return reportErr("report cashback", err)
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
		return s.churn(ctx, tx, q, d)
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// churnServiceSQL and churnCustomerSQL read churn (docs/14-reports.md). $1-$4
// are the range as everywhere. The service query takes $5 = now and $6 = the end
// of the grace period (now less the grace): an ended service whose expiry is at
// or before $6 is churn, one after it is only at risk. The customer query takes
// $5 = the end of the grace period.
//
// A service has ended when its current expiry is in the past, it is not a
// trial, it is not pending, disabled or deleted, and no paid renewal waits to
// be applied to it (a renewal order that is paid, provisioning or failed to
// provision but has not moved expires_at yet).
const churnEnded = `s.expires_at IS NOT NULL AND s.status NOT IN ('pending', 'disabled', 'deleted') AND NOT p.is_trial
	AND NOT EXISTS (SELECT 1 FROM core.orders r WHERE r.subscription_id = s.id AND r.type = 'renew'
		AND r.status IN ('paid', 'provisioning', 'provision_failed'))`

func churnServiceSQL() string {
	return `
	SELECT ` + bucketOf("s.expires_at") + `, count(*) FILTER (WHERE s.expires_at <= $6::timestamptz),
		count(*) FILTER (WHERE s.expires_at > $6::timestamptz)
	FROM core.subscriptions s
	JOIN core.orders o ON o.id = s.order_id
	JOIN core.plans p ON p.id = o.plan_id
	WHERE ` + churnEnded + ` AND s.expires_at <= $5::timestamptz AND ` + rangeOf("s.expires_at") + `
	GROUP BY 1`
}

// A customer has churned when the last expiry of their non-trial services is
// past the grace period, and they have no other live service: none without an
// expiry (unlimited) and none with a renewal waiting. The people to look at
// are found by the range on the index; their whole set of services is then
// read, so the later expiry of another service is seen.
func churnCustomerSQL() string {
	return `
	SELECT ` + bucketOf("m.last_exp") + `, count(*) FROM (
		SELECT s.user_id, max(s.expires_at) AS last_exp,
			bool_or(s.expires_at IS NULL OR EXISTS (SELECT 1 FROM core.orders r WHERE r.subscription_id = s.id AND r.type = 'renew'
				AND r.status IN ('paid', 'provisioning', 'provision_failed'))) AS open
		FROM core.subscriptions s
		JOIN core.orders o ON o.id = s.order_id
		JOIN core.plans p ON p.id = o.plan_id
		WHERE s.status NOT IN ('pending', 'disabled', 'deleted') AND NOT p.is_trial
			AND s.user_id IN (SELECT c.user_id FROM core.subscriptions c WHERE c.expires_at IS NOT NULL
				AND c.status NOT IN ('pending', 'disabled', 'deleted') AND c.expires_at <= $5::timestamptz AND ` + rangeOf("c.expires_at") + `)
		GROUP BY s.user_id) m
	WHERE NOT m.open AND m.last_exp <= $5::timestamptz AND ` + rangeOf("m.last_exp") + `
	GROUP BY 1`
}

// churn adds the churn numbers into d.
func (s *Store) churn(ctx context.Context, tx pgx.Tx, q ReportQuery, d *SalesData) error {
	asOf := q.ChurnAsOf
	if asOf.IsZero() {
		asOf = time.Now()
	}
	// The grace is a number of 24-hour days, so it never depends on a zone.
	args := []any{q.From, q.To, q.TZ, q.Group, asOf, asOf.Add(-time.Duration(q.ChurnGraceDays) * 24 * time.Hour)}
	if err := each(ctx, tx, churnServiceSQL(), args, func(r pgx.Rows) error {
		var b string
		var churned, risk int64
		if err := r.Scan(&b, &churned, &risk); err != nil {
			return err
		}
		if churned > 0 {
			d.ChurnedServices[b] = churned
		}
		if risk > 0 {
			d.AtRisk[b] = risk
		}
		return nil
	}); err != nil {
		return reportErr("report churned services", err)
	}
	// The customer query has no use for now: its $5 is the end of the grace period.
	if err := each(ctx, tx, churnCustomerSQL(), []any{args[0], args[1], args[2], args[3], args[5]}, func(r pgx.Rows) error {
		var b string
		var n int64
		if err := r.Scan(&b, &n); err != nil {
			return err
		}
		d.ChurnedCustomers[b] = n
		return nil
	}); err != nil {
		return reportErr("report churned customers", err)
	}
	return nil
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
	Method string
	// Provider is who took the money (zarinpal, idpay, nextpay, stars, usdt_auto,
	// manual_card, ...). Empty for wallet and unknown rows, and for orders
	// paid before the event carried it.
	Provider string
	Currency string
	Orders   int64
	Revenue  int64
}

// SalesByMethod splits the range's revenue by how the orders were paid, and
// by which provider took the payment when the event says so. Each
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
			FROM core.ledger_entries e WHERE e.kind IN `+purchaseKinds+` AND `+rangeOf("e.created_at")+` GROUP BY 1`, args, func(r pgx.Rows) error {
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
			SELECT COALESCE(o.payload->>'source', ''), COALESCE(o.payload->>'provider', ''), o.payload->>'currency', count(*), COALESCE(sum((o.payload->>'amount')::bigint), 0)::bigint
			FROM core.outbox_core o
			WHERE o.topic = 'order.paid.v1' AND (o.payload->>'amount')::bigint > 0 AND `+rangeOf("o.created_at")+`
			GROUP BY 1, 2, 3`, args, func(r pgx.Rows) error {
			var m MethodRow
			if err := r.Scan(&m.Method, &m.Provider, &m.Currency, &m.Orders, &m.Revenue); err != nil {
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
		// Licenses and digital accounts are always paid from the wallet and publish no order.paid event: they are wallet sales.
		if err := each(ctx, tx, `
			SELECT e.currency, count(*), COALESCE(sum(-e.amount), 0)::bigint
			FROM core.ledger_entries e WHERE e.kind IN ('license_purchase', 'supplier_purchase') AND `+rangeOf("e.created_at")+` GROUP BY 1`, args, func(r pgx.Rows) error {
			var m MethodRow
			if err := r.Scan(&m.Currency, &m.Orders, &m.Revenue); err != nil {
				return err
			}
			m.Method = "wallet"
			if i := slices.IndexFunc(out, func(x MethodRow) bool { return x.Method == "wallet" && x.Provider == "" && x.Currency == m.Currency }); i >= 0 {
				out[i].Orders += m.Orders
				out[i].Revenue += m.Revenue
			} else {
				out = append(out, m)
			}
			if t := total[m.Currency]; t != nil {
				t.Orders -= m.Orders
				t.Revenue -= m.Revenue
			}
			return nil
		}); err != nil {
			return reportErr("report methods: wallet sales", err)
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
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Provider < b.Provider
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

// PlanBucketRow is what one plan sold in one bucket and one currency.
type PlanBucketRow struct {
	// Bucket is the bucket's local start date ("2006-01-02"), cut by the
	// query's Group (a week starts on Monday).
	Bucket   string
	PlanID   string
	Name     map[string]string
	Currency string
	Orders   int64
	Revenue  int64
}

// PlanSeries lists what each plan sold per bucket and currency, only where
// something was sold (the caller fills the gaps). It reads the same purchase
// entries, joined to the same orders and plans, as TopPlans, so a plan's rows
// add up to its row there.
func (s *Store) PlanSeries(ctx context.Context, q ReportQuery) ([]PlanBucketRow, error) {
	args := []any{q.From, q.To, q.TZ, q.Group}
	var out []PlanBucketRow
	err := s.readOnly(ctx, ReportTimeout, func(tx pgx.Tx) error {
		return each(ctx, tx, `
			SELECT `+bucketOf("e.created_at")+`, o.plan_id::text, p.name_i18n, e.currency, count(*), sum(-e.amount)::bigint
			FROM core.ledger_entries e
			JOIN core.orders o ON e.ref_type = 'order' AND o.id = e.ref_id
			JOIN core.plans p ON p.id = o.plan_id
			WHERE e.kind = 'purchase' AND `+rangeOf("e.created_at")+`
			GROUP BY 1, o.plan_id, p.name_i18n, e.currency
			ORDER BY 1, e.currency, o.plan_id`, args, func(r pgx.Rows) error {
			var row PlanBucketRow
			if err := r.Scan(&row.Bucket, &row.PlanID, &row.Name, &row.Currency, &row.Orders, &row.Revenue); err != nil {
				return err
			}
			out = append(out, row)
			return nil
		})
	})
	if err != nil {
		return nil, reportErr("report plan series", err)
	}
	return out, nil
}

// HourRow is the paid orders of one currency that fell in one hour of the
// week, in the report's time zone.
type HourRow struct {
	// Weekday is the ISO weekday: 1 Monday to 7 Sunday. Hour is 0 to 23.
	Weekday  int
	Hour     int
	Currency string
	Orders   int64
	Revenue  int64
}

// SalesHours counts the range's paid orders and their revenue by weekday and
// hour of the local day, per currency, only where something was sold. The
// week and the hour are the clock on the wall in the zone (on the day the
// clocks change one hour is missing or shown twice, as the wall clock shows it).
func (s *Store) SalesHours(ctx context.Context, q ReportQuery) ([]HourRow, error) {
	args := []any{q.From, q.To, q.TZ}
	var out []HourRow
	err := s.readOnly(ctx, ReportTimeout, func(tx pgx.Tx) error {
		return each(ctx, tx, `
			SELECT extract(isodow FROM e.created_at AT TIME ZONE $3::text)::int, extract(hour FROM e.created_at AT TIME ZONE $3::text)::int,
				e.currency, count(*), COALESCE(sum(-e.amount), 0)::bigint
			FROM core.ledger_entries e
			WHERE e.kind IN `+purchaseKinds+` AND `+rangeOf("e.created_at")+`
			GROUP BY 1, 2, 3
			ORDER BY 3, 1, 2`, args, func(r pgx.Rows) error {
			var h HourRow
			if err := r.Scan(&h.Weekday, &h.Hour, &h.Currency, &h.Orders, &h.Revenue); err != nil {
				return err
			}
			out = append(out, h)
			return nil
		})
	})
	if err != nil {
		return nil, reportErr("report hours", err)
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
	Provider   string // who took the payment; "" for wallet and for orders paid before it was recorded
	Refunded   int64  // what was refunded (0: nothing)
}

// paidOrdersSQL is the export's query. How an order was paid is one index
// probe per order (a LATERAL join on outbox_core_order_paid_idx), so the plan
// is the same at any volume: PostgreSQL cannot turn this join into a hash or a
// materialized scan of the whole outbox, which it did for a join on a subquery
// once the range held about 100,000 orders. $1-$3 are the range, $4 the most
// rows wanted.
func paidOrdersSQL() string {
	local := `to_char(e.created_at AT TIME ZONE $3::text, 'YYYY-MM-DD HH24:MI:SS')`
	return `
	SELECT created_at, local, order_id, type, telegram_id, username, name_i18n, currency, amount, method, refunded, provider FROM (
		SELECT e.created_at, e.id AS eid, ` + local + ` AS local, e.ref_id::text AS order_id, o.type AS type,
			u.telegram_id, COALESCE(u.username, '') AS username, p.name_i18n, e.currency, -e.amount AS amount, COALESCE(ev.method, '') AS method,
			COALESCE(o.refund_amount, 0) AS refunded, COALESCE(ev.provider, '') AS provider
		FROM core.ledger_entries e
		JOIN core.orders o ON e.ref_type = 'order' AND o.id = e.ref_id
		JOIN core.users u ON u.id = e.user_id
		JOIN core.plans p ON p.id = o.plan_id
		LEFT JOIN LATERAL (SELECT x.payload->>'source' AS method, x.payload->>'provider' AS provider FROM core.outbox_core x
			WHERE x.topic = 'order.paid.v1' AND x.payload->>'order_id' = e.ref_id::text LIMIT 1) ev ON true
		WHERE e.kind = 'purchase' AND ` + rangeOf("e.created_at") + `
		UNION ALL
		-- a license the store sold, paid from the wallet
		SELECT e.created_at, e.id, ` + local + `, e.ref_id::text, 'license',
			u.telegram_id, COALESCE(u.username, ''),
			jsonb_build_object('en', 'BOBRES license (' || v.tier || ', ' || v.months || ' months)',
				'fa', 'لایسنس BOBRES (' || CASE v.tier WHEN 'standard' THEN 'استاندارد' WHEN 'pro' THEN 'حرفه' || chr(8204) || 'ای' ELSE v.tier END || '، ' || v.months || ' ماه)'),
			e.currency, -e.amount, 'wallet', CASE WHEN v.status = 'refunded' THEN v.amount ELSE 0 END, ''
		FROM core.ledger_entries e
		JOIN core.vendor_orders v ON e.ref_type = 'vendor_order' AND v.id = e.ref_id
		JOIN core.users u ON u.id = e.user_id
		WHERE e.kind = 'license_purchase' AND ` + rangeOf("e.created_at") + `
		UNION ALL
		-- digital accounts the store resold, paid from the wallet
		SELECT e.created_at, e.id, ` + local + `, e.ref_id::text, 'digital',
			u.telegram_id, COALESCE(u.username, ''),
			jsonb_build_object(
				'en', COALESCE(NULLIF(sp.title_i18n->>'en', ''), sp.name, so.product_id) || ' x ' || so.quantity,
				'fa', COALESCE(NULLIF(sp.title_i18n->>'fa', ''), NULLIF(sp.title_i18n->>'en', ''), sp.name, so.product_id) || ' × ' || so.quantity),
			e.currency, -e.amount, 'wallet', CASE WHEN so.status = 'refunded' THEN so.amount ELSE 0 END, ''
		FROM core.ledger_entries e
		JOIN core.supplier_orders so ON e.ref_type = 'supplier_order' AND so.id = e.ref_id
		JOIN core.users u ON u.id = e.user_id
		LEFT JOIN core.supplier_products sp ON sp.id = so.product_id
		WHERE e.kind = 'supplier_purchase' AND ` + rangeOf("e.created_at") + `
	) t
	ORDER BY created_at, eid
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
				&r.Currency, &r.Amount, &r.Method, &r.Refunded, &r.Provider); err != nil {
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
