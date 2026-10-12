-- +goose Up
-- +goose StatementBegin
-- Phase 6b: online activation. source is how the token in use came:
-- 'manual' (pasted in the dashboard or imported with the CLI),
-- 'activation' (fetched from the vendor's license server with a license
-- key) or 'refresh' (renewed by the daily refresh). Only an activated
-- license is refreshed: once a day core asks the license server (through
-- the bot) for a new token, and refreshed_at is when one was last
-- installed that way. A refresh keeps updated_at and updated_by (who
-- installed the license); installing a token by hand clears refreshed_at.
ALTER TABLE license
    ADD COLUMN source       text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'activation', 'refresh')),
    ADD COLUMN refreshed_at timestamptz,
    -- only a refreshed token has a refresh time
    ADD CONSTRAINT license_refreshed CHECK ((source = 'refresh') = (refreshed_at IS NOT NULL));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE license DROP CONSTRAINT IF EXISTS license_refreshed, DROP COLUMN IF EXISTS refreshed_at,
    DROP COLUMN IF EXISTS source;
-- +goose StatementEnd
