-- +goose Up
-- +goose StatementBegin
-- Reward codes: a plan the owner marks (reward_percent) gives its buyer, after each paid purchase of it, a discount code for their next
-- purchase of the same plan: bound to the buyer (user_id) and the plan (plan_id), one use, valid for 30 days. source_order_id is the
-- purchase that gave it: unique, so a retried delivery never makes a second code.
ALTER TABLE plans ADD COLUMN reward_percent int CHECK (reward_percent IS NULL OR reward_percent BETWEEN 1 AND 90);
ALTER TABLE discount_codes ADD COLUMN user_id uuid REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE discount_codes ADD COLUMN plan_id uuid REFERENCES plans (id) ON DELETE CASCADE;
ALTER TABLE discount_codes ADD COLUMN source_order_id uuid UNIQUE REFERENCES orders (id) ON DELETE SET NULL;
-- A code made for a buyer is bound to a plan too.
ALTER TABLE discount_codes ADD CONSTRAINT discount_codes_user_has_plan CHECK (user_id IS NULL OR plan_id IS NOT NULL);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE discount_codes DROP CONSTRAINT IF EXISTS discount_codes_user_has_plan;
ALTER TABLE discount_codes DROP COLUMN IF EXISTS source_order_id;
ALTER TABLE discount_codes DROP COLUMN IF EXISTS plan_id;
ALTER TABLE discount_codes DROP COLUMN IF EXISTS user_id;
ALTER TABLE plans DROP COLUMN IF EXISTS reward_percent;
-- +goose StatementEnd
