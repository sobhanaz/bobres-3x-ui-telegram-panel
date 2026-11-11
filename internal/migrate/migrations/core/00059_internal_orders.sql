-- +goose Up
-- +goose StatementBegin
-- Owner accounts' purchases kept out of revenue (docs/14-reports.md, "Internal orders"): with the switch reports.exclude_owner_orders on, an
-- order, a purchase, a top-up, a refund or any wallet entry that an owner account makes is stamped internal when it is created, and the
-- reports, the overview, the daily summary and the growth rewards leave internal rows out. The stamp is stored on the row, so flipping the
-- switch later never rewrites history. Nothing else changes: no order, payment, balance or ledger row is touched.
ALTER TABLE orders ADD COLUMN internal boolean NOT NULL DEFAULT false;
ALTER TABLE ledger_entries ADD COLUMN internal boolean NOT NULL DEFAULT false;

-- Few rows are internal: the partial indexes serve the "Internal orders" card and the backfill's counts without touching the big ones.
CREATE INDEX orders_internal_idx ON orders (created_at DESC) WHERE internal;
CREATE INDEX ledger_entries_internal_idx ON ledger_entries (created_at DESC) WHERE internal;

-- The ledger stays append-only except for this flag: the dashboard can count an owner's earlier purchases as internal and undo it
-- (reports.internal_backfill, reports.internal_undo). An update that changes anything else is still refused, and so is a delete.
CREATE OR REPLACE FUNCTION ledger_entries_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (to_jsonb(NEW) - 'internal') = (to_jsonb(OLD) - 'internal') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'ledger_entries is append-only (% is not allowed)', TG_OP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION ledger_entries_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'ledger_entries is append-only (% is not allowed)', TG_OP;
END;
$$;
DROP INDEX IF EXISTS ledger_entries_internal_idx;
DROP INDEX IF EXISTS orders_internal_idx;
ALTER TABLE ledger_entries DROP COLUMN IF EXISTS internal;
ALTER TABLE orders DROP COLUMN IF EXISTS internal;
-- +goose StatementEnd
