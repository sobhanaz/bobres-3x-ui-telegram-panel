-- +goose Up
-- +goose StatementBegin
-- Cashback (docs/01-database.md): when an order that earns it is paid and delivered, the buyer's wallet gets a share of what the order cost, once.
-- The credit is a ledger entry of kind cashback (key cashback:<order id>); this row says what it was worked out on and from what, so the dashboard can
-- show it on the order and a refund can take it back. The order is the key: an order earns at most once, whatever retries its delivery.
CREATE TABLE cashback_credits (
    order_id          uuid PRIMARY KEY REFERENCES orders (id),
    user_id           uuid NOT NULL REFERENCES users (id),
    currency          text NOT NULL CHECK (currency ~ '^[A-Z]{3,5}$'),
    -- What the order cost (after any discount) and the share the owner had set, in hundredths of a percent (250 = 2.5%); the amount is
    -- floor(base_amount x percent_bp / 10000), cut to the owner's maximum per order (capped says it was).
    base_amount       bigint NOT NULL CHECK (base_amount > 0),
    percent_bp        integer NOT NULL CHECK (percent_bp BETWEEN 1 AND 10000),
    amount            bigint NOT NULL CHECK (amount > 0),
    capped            boolean NOT NULL DEFAULT false,
    ledger_entry_id   uuid NOT NULL UNIQUE REFERENCES ledger_entries (id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    -- A refund of the order takes the cashback back as far as the wallet holds it: how much it took (0 to amount) and when. A refund happens once,
    -- so a row is reversed once; reversal_entry_id is NULL when the wallet held nothing to take.
    reversed_amount   bigint CHECK (reversed_amount IS NULL OR (reversed_amount >= 0 AND reversed_amount <= amount)),
    reversed_at       timestamptz,
    reversal_entry_id uuid UNIQUE REFERENCES ledger_entries (id),
    CONSTRAINT cashback_credits_reversal_check CHECK ((reversed_at IS NULL) = (reversed_amount IS NULL))
);
CREATE INDEX cashback_credits_user_idx ON cashback_credits (user_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cashback_credits;
-- The ledger keeps allowing 'cashback' and 'cashback_reversal': it is append-only, so the credits already paid cannot be removed.
-- +goose StatementEnd
