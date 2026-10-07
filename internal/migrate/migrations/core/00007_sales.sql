-- +goose Up
-- +goose StatementBegin
-- Phase 4, milestone 2 (customers and sales in the dashboard).

-- A staff member extends a service through a free, already-paid renewal or
-- top-up order: it carries its own days and traffic instead of its plan's, so
-- the provisioning worker applies it in turn with the customer's renewals and
-- a retry never adds twice. created_by names the staff member.
ALTER TABLE orders ADD COLUMN extend_days int CHECK (extend_days > 0);
ALTER TABLE orders ADD COLUMN extend_bytes bigint CHECK (extend_bytes > 0);
ALTER TABLE orders ADD COLUMN created_by uuid REFERENCES users (id);

-- A refund credits the order's amount to the customer's wallet once (ledger
-- key refund:<order id>); the order records when and how much.
ALTER TABLE orders ADD COLUMN refunded_at timestamptz;
ALTER TABLE orders ADD COLUMN refund_amount bigint CHECK (refund_amount > 0);

-- A service deleted from the panel keeps its row (orders point at it).
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_status_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_status_check
    CHECK (status IN ('pending','active','expiring_soon','expired','disabled','depleted','deleted'));

-- The dashboard's lists, newest first, and its username search.
CREATE INDEX orders_created_idx ON orders (created_at DESC);
CREATE INDEX subscriptions_created_idx ON subscriptions (created_at DESC);
CREATE INDEX ledger_entries_created_idx ON ledger_entries (created_at DESC, id DESC);
CREATE INDEX ledger_entries_user_idx ON ledger_entries (user_id, created_at DESC);
CREATE INDEX users_created_idx ON users (created_at DESC);
CREATE INDEX users_username_lower_idx ON users (lower(username));
CREATE INDEX users_referred_by_idx ON users (referred_by) WHERE referred_by IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS users_referred_by_idx;
DROP INDEX IF EXISTS users_username_lower_idx;
DROP INDEX IF EXISTS users_created_idx;
DROP INDEX IF EXISTS ledger_entries_user_idx;
DROP INDEX IF EXISTS ledger_entries_created_idx;
DROP INDEX IF EXISTS subscriptions_created_idx;
DROP INDEX IF EXISTS orders_created_idx;
UPDATE subscriptions SET status = 'disabled' WHERE status = 'deleted';
ALTER TABLE subscriptions DROP CONSTRAINT subscriptions_status_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_status_check
    CHECK (status IN ('pending','active','expiring_soon','expired','disabled','depleted'));
ALTER TABLE orders DROP COLUMN IF EXISTS refund_amount;
ALTER TABLE orders DROP COLUMN IF EXISTS refunded_at;
ALTER TABLE orders DROP COLUMN IF EXISTS created_by;
ALTER TABLE orders DROP COLUMN IF EXISTS extend_bytes;
ALTER TABLE orders DROP COLUMN IF EXISTS extend_days;
-- +goose StatementEnd
