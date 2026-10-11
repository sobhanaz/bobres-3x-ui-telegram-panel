-- +goose Up
-- +goose StatementBegin
-- Phase 4, milestone 5: resellers. A reseller buys services for their own
-- customers at a tier price, on credit up to a limit, and earns a commission
-- on every purchase by a customer who joined through their invite link.

-- A tier: a discount off the store price and a commission rate.
CREATE TABLE reseller_tiers (
    id                 uuid PRIMARY KEY,
    name               text NOT NULL UNIQUE CHECK (char_length(name) BETWEEN 1 AND 40),
    discount_percent   integer NOT NULL CHECK (discount_percent BETWEEN 0 AND 90),
    commission_percent integer NOT NULL CHECK (commission_percent BETWEEN 0 AND 50),
    version            integer NOT NULL DEFAULT 1,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- A fixed price for one plan in one tier, in the plan's currency; it
-- overrides the tier's discount.
CREATE TABLE reseller_tier_prices (
    tier_id uuid NOT NULL REFERENCES reseller_tiers (id) ON DELETE CASCADE,
    plan_id uuid NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    price   bigint NOT NULL CHECK (price >= 0),
    -- the plan's currency when the price was set: a price in another currency
    -- (the plan changed meanwhile) is ignored
    currency text NOT NULL,
    PRIMARY KEY (tier_id, plan_id)
);

-- A reseller is a user with role 'reseller' and this row (made together).
-- A tier in use cannot be deleted.
CREATE TABLE resellers (
    user_id            uuid PRIMARY KEY REFERENCES users (id),
    tier_id            uuid NOT NULL REFERENCES reseller_tiers (id) ON DELETE RESTRICT,
    commission_percent integer CHECK (commission_percent BETWEEN 0 AND 50), -- NULL: the tier's
    note               text NOT NULL DEFAULT '' CHECK (char_length(note) <= 500), -- staff only
    version            integer NOT NULL DEFAULT 1,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX resellers_tier_idx ON resellers (tier_id);

-- Credit, per currency like the wallet: the balance may go down to
-- -credit_limit. Only resellers get a limit (the domain's rule).
ALTER TABLE wallets ADD COLUMN credit_limit bigint NOT NULL DEFAULT 0 CHECK (credit_limit >= 0);
ALTER TABLE wallets DROP CONSTRAINT wallets_balance_check;
ALTER TABLE wallets ADD CONSTRAINT wallets_balance_check CHECK (balance >= -credit_limit);

-- Commissions paid to resellers and the payments they make to the store.
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral','commission','settlement'));

-- The name a reseller gives a service ("Ali's phone"): set on the order,
-- copied to the subscription it creates.
ALTER TABLE orders ADD COLUMN label text CHECK (char_length(label) <= 64);
ALTER TABLE subscriptions ADD COLUMN label text CHECK (char_length(label) <= 64);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Going back is refused while anyone owes money: the old check would not hold.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM wallets WHERE balance < 0) THEN
        RAISE EXCEPTION 'a wallet has a negative balance (a reseller''s debt): record the payment before going back';
    END IF;
END
$$;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS label;
ALTER TABLE orders DROP COLUMN IF EXISTS label;
-- The ledger keeps allowing 'commission' and 'settlement': it is append-only,
-- so entries already written cannot be removed and the old constraint would
-- reject them.
ALTER TABLE wallets DROP CONSTRAINT wallets_balance_check;
ALTER TABLE wallets DROP COLUMN IF EXISTS credit_limit;
ALTER TABLE wallets ADD CONSTRAINT wallets_balance_check CHECK (balance >= 0);
UPDATE users SET role = 'user', updated_at = now() WHERE role = 'reseller';
DROP TABLE IF EXISTS resellers;
DROP TABLE IF EXISTS reseller_tier_prices;
DROP TABLE IF EXISTS reseller_tiers;
-- +goose StatementEnd
