-- Milestone 6: automated gateway settings, changed by the owner from the dashboard
-- (through core). Seeded once from the environment; after that this table is the
-- source of truth. The merchant id and the proxy URL (it can carry a password) are
-- encrypted with the payments master key: core never holds them.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE gateway_configs (
    provider text PRIMARY KEY CHECK (provider IN ('zarinpal')),
    -- Offered for new payments. A disabled gateway with a merchant id still
    -- checks the payments already open.
    enabled boolean NOT NULL DEFAULT false,
    sandbox boolean NOT NULL DEFAULT false,
    merchant_enc bytea,
    proxy_enc bytea,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid, -- the core staff user; NULL for the environment seed
    CONSTRAINT gateway_configs_enabled_needs_merchant CHECK (NOT enabled OR merchant_enc IS NOT NULL)
);

-- The gateways report reads each provider's latest error and payment.
CREATE INDEX gateway_events_outcome_idx ON gateway_events (provider, outcome, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS gateway_events_outcome_idx;
DROP TABLE IF EXISTS gateway_configs;
-- +goose StatementEnd
