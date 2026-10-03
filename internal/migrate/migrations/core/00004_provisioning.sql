-- +goose Up
-- +goose StatementBegin
-- The provisioning worker claims paid orders, retries failures with backoff,
-- and alerts once after several attempts.
ALTER TABLE orders ADD COLUMN provision_attempts int NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN next_attempt_at timestamptz;
ALTER TABLE orders ADD COLUMN last_error text;
CREATE INDEX orders_provisioning_idx ON orders (status, next_attempt_at)
    WHERE status IN ('paid', 'provisioning', 'provision_failed');

-- A subscription row exists (pending, no server yet) before the panel call, so
-- its id keys the provisioner's idempotency. One subscription per order.
ALTER TABLE subscriptions ALTER COLUMN server_id DROP NOT NULL;
ALTER TABLE subscriptions ADD COLUMN sub_link text;
CREATE UNIQUE INDEX subscriptions_order_once_idx ON subscriptions (order_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS subscriptions_order_once_idx;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS sub_link;
DROP INDEX IF EXISTS orders_provisioning_idx;
ALTER TABLE orders DROP COLUMN IF EXISTS last_error;
ALTER TABLE orders DROP COLUMN IF EXISTS next_attempt_at;
ALTER TABLE orders DROP COLUMN IF EXISTS provision_attempts;
-- +goose StatementEnd
