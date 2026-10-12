-- P6a: automatic USDT (TRC20) payments. The customer sends an exact amount,
-- unique among the open payments, to the store's TRON address; a watcher
-- reads the address's incoming USDT transfers from TronGrid, records each one
-- once and settles the intent with exactly that amount. The address and the
-- TronGrid API key are stored encrypted with the payments master key.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','manual_zarinpal','zarinpal','stars','usdt_trc20_auto'));

-- Where the customer was told to pay, fixed at creation, so a change of the
-- store's address cannot strand a payment (the watcher keeps reading it).
ALTER TABLE payment_intents ADD COLUMN pay_address text;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_chain_fields CHECK (
    provider <> 'usdt_trc20_auto'
    OR (pay_address IS NOT NULL AND gateway_amount IS NOT NULL AND gateway_currency = 'USDT'));
-- Unique amounts are allocated, and transfers matched, by address and amount.
CREATE INDEX payment_intents_chain_amount_idx
    ON payment_intents (pay_address, gateway_amount, created_at) WHERE provider = 'usdt_trc20_auto';
-- The watcher's expiry queue.
CREATE INDEX payment_intents_chain_open_idx
    ON payment_intents (expires_at) WHERE provider = 'usdt_trc20_auto' AND status IN ('pending','confirming');
-- A customer's own recent payments: reused when asked for again, and capped.
CREATE INDEX payment_intents_chain_user_idx
    ON payment_intents (user_id, created_at) WHERE provider = 'usdt_trc20_auto';

-- scan: a TronGrid read failed, or the watcher expired an intent; match: a
-- transfer settled an intent.
ALTER TABLE gateway_events DROP CONSTRAINT IF EXISTS gateway_events_kind_check;
ALTER TABLE gateway_events ADD CONSTRAINT gateway_events_kind_check
    CHECK (kind IN ('create','callback','webhook','check','precheckout','confirm','scan','match'));

ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_provider_check;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_provider_check
    CHECK (provider IN ('zarinpal','usdt_trc20_auto'));
ALTER TABLE gateway_configs
    ADD COLUMN address_enc bytea, -- the store's TRC20 address (usdt_trc20_auto)
    ADD COLUMN api_key_enc bytea; -- TronGrid's TRON-PRO-API-KEY, optional
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_enabled_needs_merchant;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_enabled_needs_credentials CHECK (
    NOT enabled
    OR (provider = 'zarinpal' AND merchant_enc IS NOT NULL)
    OR (provider = 'usdt_trc20_auto' AND address_enc IS NOT NULL));

-- Every USDT transfer the watcher saw arrive, recorded once. matched: it
-- settled intent_id; unmatched: no payment has exactly this amount (staff
-- credit it by hand, then mark it resolved); manual: a manual crypto payment
-- (intent_id) carries this TXID; ignored: dust nobody is told about.
CREATE TABLE chain_transfers (
    id bigserial PRIMARY KEY,
    txid text NOT NULL CHECK (txid ~ '^[0-9a-f]{64}$'), -- lower-case hex, no 0x
    -- The transfer's place among its transaction's transfers to the address
    -- (TronGrid's list has no log index).
    event_index integer NOT NULL CHECK (event_index >= 0),
    network text NOT NULL CHECK (network IN ('TRC20')),
    token text NOT NULL, -- the token contract (always USDT's)
    from_address text NOT NULL,
    to_address text NOT NULL,
    value bigint NOT NULL CHECK (value > 0), -- micro-USDT
    block_ts timestamptz NOT NULL,
    intent_id uuid REFERENCES payment_intents (id),
    outcome text NOT NULL CHECK (outcome IN ('matched','unmatched','manual','resolved','ignored')),
    reason text, -- why it is unmatched or ignored
    resolved_by uuid, -- the core staff user who marked it resolved
    resolved_at timestamptz,
    note text,
    -- An unmatched deposit a manual proof may still claim (the store's
    -- address can take manual USDT too) is announced to staff only once this
    -- time has passed and it is still unmatched; NULL = nothing pending.
    announce_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT chain_transfers_once UNIQUE (txid, event_index),
    CONSTRAINT chain_transfers_intent CHECK ((outcome IN ('matched','manual')) = (intent_id IS NOT NULL)),
    CONSTRAINT chain_transfers_announce CHECK (announce_at IS NULL OR outcome = 'unmatched')
);
-- A transfer settles at most one intent and an intent is settled by at most one transfer.
CREATE UNIQUE INDEX chain_transfers_matched_once_idx ON chain_transfers (intent_id) WHERE outcome = 'matched';
CREATE INDEX chain_transfers_unmatched_idx ON chain_transfers (block_ts DESC) WHERE outcome = 'unmatched';
CREATE INDEX chain_transfers_announce_idx ON chain_transfers (announce_at) WHERE announce_at IS NOT NULL;
CREATE INDEX chain_transfers_block_idx ON chain_transfers (block_ts DESC, id DESC);

-- How far the watcher has read each address, and how its last read went.
CREATE TABLE chain_cursors (
    address text PRIMARY KEY,
    last_ts timestamptz NOT NULL, -- the newest block time read; reads restart 2 minutes before it
    scanned_at timestamptz, -- the last read (the bot's check button waits between reads)
    ok_at timestamptz, -- the last successful read
    last_error text, -- the last read's error, NULL after a success
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_intents WHERE provider = 'usdt_trc20_auto') THEN
        RAISE EXCEPTION 'cannot go back: there are usdt_trc20_auto payments';
    END IF;
END $$;
DROP TABLE IF EXISTS chain_cursors;
DROP TABLE IF EXISTS chain_transfers;
DELETE FROM gateway_configs WHERE provider = 'usdt_trc20_auto';
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_enabled_needs_credentials;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_enabled_needs_merchant
    CHECK (NOT enabled OR merchant_enc IS NOT NULL);
ALTER TABLE gateway_configs DROP COLUMN IF EXISTS api_key_enc, DROP COLUMN IF EXISTS address_enc;
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_provider_check;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_provider_check CHECK (provider IN ('zarinpal'));
DELETE FROM gateway_events WHERE kind IN ('scan','match');
ALTER TABLE gateway_events DROP CONSTRAINT IF EXISTS gateway_events_kind_check;
ALTER TABLE gateway_events ADD CONSTRAINT gateway_events_kind_check
    CHECK (kind IN ('create','callback','webhook','check','precheckout','confirm'));
DROP INDEX IF EXISTS payment_intents_chain_user_idx;
DROP INDEX IF EXISTS payment_intents_chain_open_idx;
DROP INDEX IF EXISTS payment_intents_chain_amount_idx;
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_chain_fields;
ALTER TABLE payment_intents DROP COLUMN IF EXISTS pay_address;
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','manual_zarinpal','zarinpal','stars'));
-- +goose StatementEnd
