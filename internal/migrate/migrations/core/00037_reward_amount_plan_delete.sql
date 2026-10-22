-- +goose Up
-- +goose StatementBegin
-- Reward codes, second form: a plan can give a fixed amount (in the plan's own currency) instead of a percent, for a number of days the
-- owner sets (reward_days) and with a text the buyer reads with the code (reward_text_i18n). A plan gives one kind or none. A reward
-- code is now for any plan, not only the one that earned it, so a code for a buyer no longer needs a plan; the codes made before keep
-- the plan they were bound to.
ALTER TABLE plans ADD COLUMN reward_amount bigint CHECK (reward_amount IS NULL OR reward_amount > 0);
ALTER TABLE plans ADD COLUMN reward_days int NOT NULL DEFAULT 10 CHECK (reward_days BETWEEN 1 AND 365);
ALTER TABLE plans ADD COLUMN reward_text_i18n jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE plans ADD CONSTRAINT plans_reward_one_kind CHECK (reward_percent IS NULL OR reward_amount IS NULL);
-- The plans already marked gave 30-day codes: they keep doing so until the owner changes it.
UPDATE plans SET reward_days = 30 WHERE reward_percent IS NOT NULL;
ALTER TABLE discount_codes DROP CONSTRAINT IF EXISTS discount_codes_user_has_plan;

-- Deleting a plan never erases it: its history, the reports and the refunds need it, and an owner may want it later. It is archived (deleted_at):
-- off sale and out of every list, kept as a row.
ALTER TABLE plans ADD COLUMN deleted_at timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE plans DROP COLUMN IF EXISTS deleted_at;
DELETE FROM discount_codes WHERE user_id IS NOT NULL AND plan_id IS NULL;
ALTER TABLE discount_codes ADD CONSTRAINT discount_codes_user_has_plan CHECK (user_id IS NULL OR plan_id IS NOT NULL);
ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_reward_one_kind;
ALTER TABLE plans DROP COLUMN IF EXISTS reward_text_i18n;
ALTER TABLE plans DROP COLUMN IF EXISTS reward_days;
ALTER TABLE plans DROP COLUMN IF EXISTS reward_amount;
-- +goose StatementEnd
