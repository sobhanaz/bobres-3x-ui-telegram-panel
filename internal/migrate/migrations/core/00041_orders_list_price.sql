-- +goose Up
-- +goose StatementBegin
-- A plan priced in USDT can be paid from the wallet, which is in Toman (docs/03-bot-ux.md, "One wallet, in Toman"): the order is then charged in Toman at the
-- store's USDT rate, and it keeps what it was listed at. amount and currency stay what the customer was charged (the ledger, a refund, the reports and the
-- referral and commission rules all read them); these three say what the plan's price was and the rate it was worked out at, as vendor_orders does for a
-- license (00039). All three are set together, by the payment, or none is: an order paid in the currency it was listed in (Toman, or USDT by crypto) has none.
ALTER TABLE orders ADD COLUMN list_amount bigint CHECK (list_amount IS NULL OR list_amount > 0);
ALTER TABLE orders ADD COLUMN list_currency text CHECK (list_currency IS NULL OR list_currency ~ '^[A-Z]{3,5}$');
ALTER TABLE orders ADD COLUMN rate bigint CHECK (rate IS NULL OR rate > 0);
ALTER TABLE orders ADD CONSTRAINT orders_list_price_together
    CHECK ((list_amount IS NULL) = (list_currency IS NULL) AND (list_amount IS NULL) = (rate IS NULL));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- A rollback keeps every order and every credit: amount and currency are what was charged. Only the list price and the rate of the orders paid this way are lost.
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_list_price_together;
ALTER TABLE orders DROP COLUMN IF EXISTS rate;
ALTER TABLE orders DROP COLUMN IF EXISTS list_currency;
ALTER TABLE orders DROP COLUMN IF EXISTS list_amount;
-- +goose StatementEnd
