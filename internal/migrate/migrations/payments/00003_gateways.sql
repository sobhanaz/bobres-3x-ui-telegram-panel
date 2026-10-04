-- Phase 2: automated gateways (Zarinpal, Telegram Stars).
-- +goose Up
-- +goose StatementBegin
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','zarinpal','stars'));

-- What the customer pays at the gateway, fixed when the intent is created: Rial for
-- Zarinpal, Stars for Telegram. Settlement compares the gateway's report against
-- these, never against a recomputed price.
ALTER TABLE payment_intents
    ADD COLUMN gateway_amount bigint CHECK (gateway_amount IS NULL OR gateway_amount > 0),
    ADD COLUMN gateway_currency text,
    -- Zarinpal authority, Telegram payment charge id.
    ADD COLUMN external_id text,
    ADD COLUMN pay_url text,
    ADD COLUMN checked_at timestamptz,
    ADD COLUMN check_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN failure_reason text;

-- One gateway payment settles at most one intent.
CREATE UNIQUE INDEX payment_intents_external_once_idx
    ON payment_intents (provider, external_id) WHERE external_id IS NOT NULL;
-- The reconciler's work queue: open gateway intents, least recently checked first.
CREATE INDEX payment_intents_gateway_open_idx
    ON payment_intents (checked_at NULLS FIRST)
    WHERE status IN ('pending','confirming') AND provider = 'zarinpal';

-- Audit trail of everything a gateway told us (redacted: no card numbers, no tokens).
CREATE TABLE gateway_events (
    id bigserial PRIMARY KEY,
    intent_id uuid REFERENCES payment_intents (id),
    provider text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('create','callback','webhook','check','precheckout','confirm')),
    outcome text NOT NULL CHECK (outcome IN ('pending','paid','failed','rejected','error')),
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX gateway_events_intent_idx ON gateway_events (intent_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS gateway_events;
DROP INDEX IF EXISTS payment_intents_gateway_open_idx;
DROP INDEX IF EXISTS payment_intents_external_once_idx;
ALTER TABLE payment_intents
    DROP COLUMN IF EXISTS failure_reason,
    DROP COLUMN IF EXISTS check_attempts,
    DROP COLUMN IF EXISTS checked_at,
    DROP COLUMN IF EXISTS pay_url,
    DROP COLUMN IF EXISTS external_id,
    DROP COLUMN IF EXISTS gateway_currency,
    DROP COLUMN IF EXISTS gateway_amount;
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto'));
-- +goose StatementEnd
