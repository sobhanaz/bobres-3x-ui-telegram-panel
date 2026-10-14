-- +goose Up
-- +goose StatementBegin
-- Sales reports (docs/14-reports.md): the three indexes their queries need and
-- the schema did not have. Everything else a report reads is found through
-- ledger_entries_created_idx, ledger_entries_user_idx and the primary keys
-- (migration 00007 and 00001). No table and no column is added.

-- The reports read two kinds of event from core's own outbox, which is kept
-- for ever: order.paid.v1 (how an order was paid: wallet, manual or gateway)
-- and payment.rejected.v1 (a reviewer rejected a payment), both by when they
-- happened. The outbox has no index on topic or time, and it holds every other
-- event too, so the index covers only these two topics.
CREATE INDEX IF NOT EXISTS outbox_core_reports_idx ON outbox_core (topic, created_at)
    WHERE topic IN ('order.paid.v1', 'payment.rejected.v1');

-- The orders export finds how each order was paid by order id: its order.paid.v1
-- event, one index probe per order. Without this index that is a scan of the
-- whole outbox per order, or a hash join that does not fit the export's time
-- limit once the range holds about 100,000 orders.
CREATE INDEX IF NOT EXISTS outbox_core_order_paid_idx ON outbox_core ((payload->>'order_id'))
    WHERE topic = 'order.paid.v1';

-- Orders that ended without being paid (cancelled or expired, and not a paid
-- order that was refunded and cancelled with it), by when they ended.
CREATE INDEX IF NOT EXISTS orders_unpaid_end_idx ON orders (updated_at)
    WHERE status IN ('cancelled', 'expired') AND refunded_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS orders_unpaid_end_idx;
DROP INDEX IF EXISTS outbox_core_order_paid_idx;
DROP INDEX IF EXISTS outbox_core_reports_idx;
-- +goose StatementEnd
