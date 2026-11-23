-- +goose Up
-- +goose StatementBegin
-- Pricing the owner's digital accounts from the live USDT price (docs/supplier.md, "Pricing"): a product's Toman price either follows the
-- supplier's cost (cost x the store's USDT rate x the owner's markup, rounded up) or is a price the owner typed ("fixed"). The column is
-- all a product needs: the switch, the markup and the rate last applied are kept as internal state (state.supplier.pricing).
--
-- Nothing changes for a store from before: a product that already has a price keeps it, as a fixed one, until the owner presses "Apply to
-- all products"; a product with no price yet follows the rate once the owner turns the pricing on.
ALTER TABLE supplier_products ADD COLUMN price_mode text NOT NULL DEFAULT 'fixed' CHECK (price_mode IN ('fixed', 'auto'));
ALTER TABLE supplier_products ALTER COLUMN price_mode SET DEFAULT 'auto';
UPDATE supplier_products SET price_mode = 'auto' WHERE sell_price IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE supplier_products DROP COLUMN IF EXISTS price_mode;
-- +goose StatementEnd
