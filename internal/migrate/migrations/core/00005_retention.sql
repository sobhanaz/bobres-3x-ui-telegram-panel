-- +goose Up
-- +goose StatementBegin
-- Phase 3, retention: renewals and traffic top-ups of an existing
-- subscription, usage sync with reminders, discount codes, referrals.

-- A renewal or top-up names the subscription it extends; a new order never does.
-- The limits it sets are computed once, when the worker first claims it, and
-- stored, so a retry applies the same absolute values (never adds twice).
ALTER TABLE orders ADD COLUMN subscription_id uuid REFERENCES subscriptions (id);
ALTER TABLE orders ADD CONSTRAINT orders_subscription_for_type CHECK ((type = 'new') = (subscription_id IS NULL));
ALTER TABLE orders ADD COLUMN targets_set boolean NOT NULL DEFAULT false;
ALTER TABLE orders ADD COLUMN target_expires_at timestamptz;
ALTER TABLE orders ADD COLUMN target_traffic_bytes bigint;
CREATE INDEX orders_subscription_idx ON orders (subscription_id) WHERE subscription_id IS NOT NULL;

-- The order amount is what the customer pays; the discount is recorded beside it.
ALTER TABLE orders ADD COLUMN discount_code text;
ALTER TABLE orders ADD COLUMN discount_amount bigint NOT NULL DEFAULT 0 CHECK (discount_amount >= 0);

-- Traffic packages for an existing subscription, sold only from its page.
ALTER TABLE plans ADD COLUMN is_topup boolean NOT NULL DEFAULT false;
ALTER TABLE plans ADD CONSTRAINT plans_topup_shape CHECK (NOT is_topup OR (NOT is_trial AND traffic_bytes > 0));

-- Reminders go out once per period; a renewal or top-up clears them.
ALTER TABLE subscriptions ADD COLUMN notified_expiring_at timestamptz;
ALTER TABLE subscriptions ADD COLUMN notified_low_traffic_at timestamptz;
ALTER TABLE subscriptions ADD COLUMN notified_ended_at timestamptz;
CREATE INDEX subscriptions_sync_idx ON subscriptions (last_synced_at NULLS FIRST)
    WHERE status IN ('active', 'expiring_soon', 'expired', 'depleted');

-- Personal invite codes (created on first use) and their rewards.
ALTER TABLE users ADD COLUMN ref_code text UNIQUE;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral'));

CREATE TABLE discount_codes (
    code text PRIMARY KEY CHECK (code ~ '^[A-Z0-9_-]{3,32}$'),
    percent int CHECK (percent BETWEEN 1 AND 100),
    amount bigint CHECK (amount > 0),
    currency text,
    max_uses int CHECK (max_uses > 0),
    used_count int NOT NULL DEFAULT 0,
    expires_at timestamptz,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((percent IS NULL) <> (amount IS NULL)),
    CHECK ((amount IS NULL) = (currency IS NULL))
);
ALTER TABLE orders ADD CONSTRAINT orders_discount_code_fk FOREIGN KEY (discount_code) REFERENCES discount_codes (code);

-- One redemption per customer and code, recorded when the order is paid.
CREATE TABLE discount_redemptions (
    code text NOT NULL REFERENCES discount_codes (code),
    user_id uuid NOT NULL REFERENCES users (id),
    order_id uuid NOT NULL UNIQUE REFERENCES orders (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (code, user_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS discount_redemptions;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_discount_code_fk;
DROP TABLE IF EXISTS discount_codes;
-- The ledger keeps allowing 'referral': it is append-only, so rewards already
-- paid cannot be removed and the old constraint would reject them.
ALTER TABLE users DROP COLUMN IF EXISTS ref_code;
DROP INDEX IF EXISTS subscriptions_sync_idx;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS notified_ended_at;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS notified_low_traffic_at;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS notified_expiring_at;
ALTER TABLE plans DROP CONSTRAINT IF EXISTS plans_topup_shape;
ALTER TABLE plans DROP COLUMN IF EXISTS is_topup;
ALTER TABLE orders DROP COLUMN IF EXISTS discount_amount;
ALTER TABLE orders DROP COLUMN IF EXISTS discount_code;
DROP INDEX IF EXISTS orders_subscription_idx;
ALTER TABLE orders DROP COLUMN IF EXISTS target_traffic_bytes;
ALTER TABLE orders DROP COLUMN IF EXISTS target_expires_at;
ALTER TABLE orders DROP COLUMN IF EXISTS targets_set;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_subscription_for_type;
ALTER TABLE orders DROP COLUMN IF EXISTS subscription_id;
-- +goose StatementEnd
