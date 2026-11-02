-- +goose Up
-- +goose StatementBegin
-- Config names (docs/03-bot-ux.md, "Config names"): a buyer may choose the name of a new service's config instead of the
-- automatic one. The order keeps the name chosen (with a reseller's prefix in front) until the service is made; the
-- service's own name stays in subscriptions.client_email, which is unique. Orders still on their way hold their name, so a
-- second buyer is told it is taken before paying.
ALTER TABLE orders ADD COLUMN client_name text CHECK (char_length(client_name) BETWEEN 1 AND 64);
CREATE INDEX orders_client_name_idx ON orders (client_name) WHERE client_name IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS orders_client_name_idx;
ALTER TABLE orders DROP COLUMN IF EXISTS client_name;
-- +goose StatementEnd
