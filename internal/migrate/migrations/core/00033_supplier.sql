-- +goose Up
-- +goose StatementBegin
-- Reselling a supplier's digital accounts from the owner's own store (docs/supplier.md). The owner's customers pay from the
-- wallet in Toman at the price the owner writes for each product; core buys the account from the supplier (through the
-- bot: core has no internet access) and the bot hands it over. Only the owner's install uses these tables.

ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral','commission','settlement','gift_code',
                    'license_purchase','license_refund','supplier_purchase','supplier_refund'));

-- The supplier's API key, encrypted with core's master key (the same envelope as the other secrets); write-only.
CREATE TABLE supplier_config (
    id            boolean PRIMARY KEY DEFAULT true CHECK (id),
    api_key_enc   bytea NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- The supplier's catalog, as last read, and what the owner makes of each product. A product the supplier lists is imported
-- OFF (enabled = false, no price): the owner writes the price (Toman) and the texts and switches it on. The supplier's own
-- fields stay as they were read (cost, stock, raw), so the owner sees what it costs.
CREATE TABLE supplier_products (
    id            text PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9._:-]{1,100}$'),
    name          text NOT NULL DEFAULT '' CHECK (char_length(name) <= 300),
    description   text NOT NULL DEFAULT '' CHECK (char_length(description) <= 4000),
    cost_micro    bigint,                  -- the wholesale cost in millionths of cost_currency (NULL: not read)
    cost_currency text NOT NULL DEFAULT 'USDT' CHECK (cost_currency ~ '^[A-Z]{3,5}$'),
    stock         integer,                 -- NULL: the supplier does not say
    listed        boolean NOT NULL DEFAULT true,  -- the supplier still lists it (false: it vanished from the catalog)
    raw           jsonb NOT NULL DEFAULT '{}',
    enabled       boolean NOT NULL DEFAULT false, -- the owner sells it
    sell_price    bigint CHECK (sell_price IS NULL OR sell_price > 0),   -- Toman, what a customer pays
    title_i18n    jsonb NOT NULL DEFAULT '{}',    -- the owner's name, per language ("" keeps the supplier's)
    desc_i18n     jsonb NOT NULL DEFAULT '{}',
    sort          integer NOT NULL DEFAULT 0,
    synced_at     timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT supplier_products_enabled_has_price CHECK (NOT enabled OR sell_price IS NOT NULL)
);

-- One row per purchase. paid: the wallet was debited and the supplier has not delivered yet (a worker asks again, with the
-- same external order id, so the supplier never charges twice); done: delivered; refunded: the supplier refused for good,
-- the money went back. delivery_enc is the supplier's answer under core's master key: what the customer receives (an
-- account) is a secret and is never kept in the clear.
CREATE TABLE supplier_orders (
    id              uuid PRIMARY KEY,
    user_id         uuid NOT NULL REFERENCES users (id),
    product_id      text NOT NULL REFERENCES supplier_products (id),
    quantity        integer NOT NULL CHECK (quantity BETWEEN 1 AND 10),
    amount          bigint NOT NULL CHECK (amount > 0),
    currency        text NOT NULL CHECK (currency ~ '^[A-Z]{3,5}$'),
    status          text NOT NULL DEFAULT 'paid' CHECK (status IN ('paid', 'done', 'refunded')),
    idempotency_key text NOT NULL UNIQUE CHECK (char_length(idempotency_key) BETWEEN 1 AND 200),
    delivery_enc    bytea,
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 500),
    notified_at     timestamptz,           -- the owner was told this order failed or waits too long
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz
);
CREATE INDEX supplier_orders_due_idx ON supplier_orders (next_attempt_at) WHERE status = 'paid';
CREATE INDEX supplier_orders_user_idx ON supplier_orders (user_id, created_at DESC);
CREATE INDEX supplier_orders_product_idx ON supplier_orders (product_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS supplier_orders;
DROP TABLE IF EXISTS supplier_products;
DROP TABLE IF EXISTS supplier_config;
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_kind_check;
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_kind_check
    CHECK (kind IN ('topup','purchase','refund','adjust','trial_grant','referral','commission','settlement','gift_code',
                    'license_purchase','license_refund'));
-- +goose StatementEnd
