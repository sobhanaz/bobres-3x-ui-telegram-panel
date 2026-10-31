-- +goose Up
-- +goose StatementBegin
-- The register of hosted stores (docs/18-hosted-and-affiliate-plans.md, section 1): the customers' stores the owner installed and runs for them
-- (tools/hosted), kept by the owner's own store. Only the owner's install uses it.
--
-- What is NOT here, by rule: the customer's bot token (it stays on the customer's server) and the customer's backup key (BOBRES_BACKUP_KEY: the
-- customer keeps it). The token of the store's /metrics endpoint is kept encrypted with core's master key, so the dashboard can read the store's
-- health through the bot; it is write-only (no answer returns it).
CREATE TABLE hosted_stores (
    id                uuid PRIMARY KEY,
    customer          text NOT NULL CHECK (char_length(customer) BETWEEN 1 AND 200),
    domain            text NOT NULL UNIQUE CHECK (domain ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$'),
    -- The version installed (from the installer); the live one is in last_version.
    version           text NOT NULL DEFAULT '' CHECK (char_length(version) <= 64),
    -- The license server's id of the store's license ("Hosted" product: a pro license made for it), "" while none.
    license_id        text NOT NULL DEFAULT '' CHECK (license_id = '' OR license_id ~ '^lic_[a-z0-9_-]{1,60}$'),
    channel           text NOT NULL DEFAULT 'stable' CHECK (channel IN ('stable', 'beta')),
    notes             text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
    metrics_token_enc bytea,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    -- The last reading of its /metrics: when, whether it answered, the version it runs and its key figures (a small JSON object), or why not.
    checked_at        timestamptz,
    up                boolean,
    last_version      text NOT NULL DEFAULT '' CHECK (char_length(last_version) <= 64),
    figures           jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_error        text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 300)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS hosted_stores;
-- +goose StatementEnd
