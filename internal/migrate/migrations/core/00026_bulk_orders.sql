-- +goose Up
-- +goose StatementBegin
-- A bulk purchase: a customer (or a reseller) buys N services of one plan at once
-- and pays for all of them from the wallet in ONE transaction. The N orders are
-- ordinary ones (each has its own ledger entry, is provisioned, retried, refunded
-- and reported like any order); this row only ties them together: the request key
-- that makes a retry or a double tap safe, what was asked for, and whether the one
-- message with every link has been announced (delivered_at, NULL until then).
CREATE TABLE bulk_orders (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users (id),
    plan_id uuid NOT NULL REFERENCES plans (id),
    quantity int NOT NULL CHECK (quantity BETWEEN 2 AND 1000),
    unit_amount bigint NOT NULL CHECK (unit_amount > 0),
    amount bigint NOT NULL,
    currency text NOT NULL,
    -- the name the services share ("Family"): each service is "<prefix> <n>"
    label_prefix text CHECK (char_length(label_prefix) <= 64),
    idempotency_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    -- what the whole bulk cost is the price of one service times how many
    CONSTRAINT bulk_orders_total_check CHECK (amount = unit_amount * quantity)
);
CREATE INDEX bulk_orders_user_idx ON bulk_orders (user_id, created_at DESC);
-- the ones still waiting for their delivery message: what the worker looks at
CREATE INDEX bulk_orders_undelivered_idx ON bulk_orders (created_at) WHERE delivered_at IS NULL;

-- Which bulk an order belongs to, and its place in it (1 to quantity): both or neither.
ALTER TABLE orders
    ADD COLUMN bulk_id uuid REFERENCES bulk_orders (id),
    ADD COLUMN bulk_index int CHECK (bulk_index >= 1),
    ADD CONSTRAINT orders_bulk_pair CHECK ((bulk_id IS NULL) = (bulk_index IS NULL));
CREATE UNIQUE INDEX orders_bulk_index_idx ON orders (bulk_id, bulk_index) WHERE bulk_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The orders stay as the ordinary orders they are; only what ties them together goes.
DROP INDEX IF EXISTS orders_bulk_index_idx;
ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_bulk_pair,
    DROP COLUMN IF EXISTS bulk_index,
    DROP COLUMN IF EXISTS bulk_id;
DROP TABLE IF EXISTS bulk_orders;
-- +goose StatementEnd
