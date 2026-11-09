-- +goose Up
-- +goose StatementBegin
-- Days from the first connection (docs/03-bot-ux.md, "Days from the first connection"): with the switch on, a new
-- service's days start when its config first connects instead of when it is made. Until then it has no end date
-- (expires_at is NULL) and first_use_days holds how long it will run; the panel sets the end at the first connection,
-- and the usage sync records it and clears first_use_days. NULL is every service that is not waiting, as before.
ALTER TABLE subscriptions ADD COLUMN first_use_days integer
    CHECK (first_use_days IS NULL OR first_use_days BETWEEN 1 AND 36500);

-- A renewal of a waiting service adds its days to the wait: the order's target is that wait (with target_expires_at
-- NULL), computed once and kept for retries as the other targets are.
ALTER TABLE orders ADD COLUMN target_first_use_days integer
    CHECK (target_first_use_days IS NULL OR target_first_use_days BETWEEN 1 AND 36500);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE orders DROP COLUMN IF EXISTS target_first_use_days;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS first_use_days;
-- +goose StatementEnd
