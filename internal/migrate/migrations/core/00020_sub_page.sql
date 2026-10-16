-- +goose Up
-- +goose StatementBegin
-- The branded subscription page (docs/15-subscription-page.md): every service
-- gets its own unguessable page token, so the page URL never exposes the
-- service id, the panel's sub id or the client email. The token is 128 bits
-- of CSPRNG output as 32 lowercase hex characters (a v4 uuid carries only 122
-- random bits, so three uuids are cut up: 48 + 48 + 32 bits, all random).
-- The default covers rows inserted without a token; the store sets its own.
ALTER TABLE subscriptions ADD COLUMN page_token text;

UPDATE subscriptions SET page_token =
    substr(replace(gen_random_uuid()::text, '-', ''), 1, 12) ||
    substr(replace(gen_random_uuid()::text, '-', ''), 1, 12) ||
    substr(replace(gen_random_uuid()::text, '-', ''), 1, 8);

ALTER TABLE subscriptions ALTER COLUMN page_token SET DEFAULT
    substr(replace(gen_random_uuid()::text, '-', ''), 1, 12) ||
    substr(replace(gen_random_uuid()::text, '-', ''), 1, 12) ||
    substr(replace(gen_random_uuid()::text, '-', ''), 1, 8);
ALTER TABLE subscriptions ALTER COLUMN page_token SET NOT NULL;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_page_token_format
    CHECK (page_token ~ '^[0-9a-f]{32}$');
CREATE UNIQUE INDEX subscriptions_page_token_key ON subscriptions (page_token);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS subscriptions_page_token_key;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_page_token_format;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS page_token;
-- +goose StatementEnd
