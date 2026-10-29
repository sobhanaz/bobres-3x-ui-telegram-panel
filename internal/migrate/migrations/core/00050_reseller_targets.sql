-- +goose Up
-- +goose StatementBegin
-- Reseller monthly targets (docs/01-database.md): while the owner has targets on, each service a reseller buys and that is delivered is a sale of
-- the month it was delivered in (store-local; the month is the label yyyy-mm of the calendar the owner chose, Gregorian or Jalali). When the number
-- of sales of a month reaches a tier of the owner's list, the reseller is paid that tier's bonus, once for the month: a ledger entry of kind
-- target_bonus under the key target:<reseller user id>:<period>:<threshold>.
--
-- A sale is recorded once per order (the order is the key, so a retried delivery counts once). A refund does not delete the row: the count of a month
-- leaves out refunded orders, but a bonus already paid is not taken back.
CREATE TABLE reseller_sales (
    order_id   uuid PRIMARY KEY REFERENCES orders (id),
    user_id    uuid NOT NULL REFERENCES users (id),
    period     text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reseller_sales_period_idx ON reseller_sales (user_id, period);

-- The bonuses paid: a tier is identified by its threshold, not by its place in the owner's list, so editing the list in the middle of a month can
-- never pay a tier twice. The primary key is the guard; the ledger key is the second.
CREATE TABLE reseller_target_bonuses (
    user_id         uuid NOT NULL REFERENCES users (id),
    period          text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
    threshold       integer NOT NULL CHECK (threshold > 0),
    amount          bigint NOT NULL CHECK (amount > 0),
    currency        text NOT NULL CHECK (currency ~ '^[A-Z]{3,5}$'),
    -- How many services the reseller had sold that month when they reached the tier.
    sales           integer NOT NULL CHECK (sales >= threshold),
    ledger_entry_id uuid NOT NULL UNIQUE REFERENCES ledger_entries (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, period, threshold)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS reseller_target_bonuses;
DROP TABLE IF EXISTS reseller_sales;
-- The ledger keeps allowing 'target_bonus': it is append-only, so the bonuses already paid cannot be removed.
-- +goose StatementEnd
