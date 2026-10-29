-- Automatic TON (Toncoin) payments, a sibling of automatic USDT (TRC20). The customer sends the exact amount of
-- TON, in nanoton, to the store's address with the payment's own comment (pay_memo, unique), and a watcher reads
-- the address's transactions from TON Center, records each once (ton_transfers, unique by hash and logical time)
-- and settles the intent whose comment and amount it carries. The address and TON Center's API key are stored
-- encrypted with the payments master key, like the other gateways' secrets. The amount is worked out when the
-- payment is created from the order's USDT price and the TON price then (quote_*), and fixed from then on.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','manual_zarinpal','zarinpal','stars','usdt_trc20_auto',
                        'idpay','nextpay','aqayepardakht','ton_auto'));

ALTER TABLE payment_intents
    ADD COLUMN pay_memo text,        -- the comment the transfer must carry (ton_auto), e.g. BOB-3F9A1C20
    ADD COLUMN quote_usdt bigint,    -- micro-USDT the order is worth: what core asked, before the TON price
    ADD COLUMN quote_rate bigint,    -- micro-USDT per 1 TON the amount was worked out at
    ADD COLUMN quote_source text;    -- which exchange gave that price
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_ton_fields CHECK (
    provider <> 'ton_auto'
    OR (pay_address IS NOT NULL AND pay_memo IS NOT NULL AND gateway_amount IS NOT NULL
        AND gateway_currency IS NOT NULL AND gateway_currency = 'TON'
        AND quote_usdt IS NOT NULL AND quote_usdt > 0 AND quote_rate IS NOT NULL AND quote_rate > 0));
-- A comment names one payment, for ever: a late transfer can only be one payment's.
CREATE UNIQUE INDEX payment_intents_ton_memo_once_idx ON payment_intents (pay_memo) WHERE provider = 'ton_auto';
-- The watcher's expiry queue, and a customer's own recent payments (reused when asked for again, and capped).
CREATE INDEX payment_intents_ton_open_idx
    ON payment_intents (expires_at) WHERE provider = 'ton_auto' AND status IN ('pending','confirming');
CREATE INDEX payment_intents_ton_user_idx ON payment_intents (user_id, created_at) WHERE provider = 'ton_auto';

ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_provider_check;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_provider_check
    CHECK (provider IN ('zarinpal','usdt_trc20_auto','idpay','nextpay','aqayepardakht','ton_auto'));
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_enabled_needs_credentials;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_enabled_needs_credentials CHECK (
    NOT enabled
    OR (provider = 'zarinpal' AND merchant_enc IS NOT NULL)
    OR (provider IN ('usdt_trc20_auto','ton_auto') AND address_enc IS NOT NULL)
    OR (provider IN ('idpay','nextpay','aqayepardakht') AND api_key_enc IS NOT NULL));

-- Every incoming TON transfer the watcher saw, recorded once. matched: it settled intent_id; unmatched: nothing
-- could be credited for it (staff credit it by hand, then mark it resolved), and intent_id, when set, is the
-- payment its comment named; resolved: staff handled it; ignored: dust, a bounce or a transfer that is no
-- payment, which nobody is told about.
CREATE TABLE ton_transfers (
    id bigserial PRIMARY KEY,
    tx_hash text NOT NULL CHECK (tx_hash ~ '^[0-9a-f]{64}$'), -- lower-case hex
    tx_lt bigint NOT NULL CHECK (tx_lt > 0),                  -- the account's logical time of the transaction
    from_address text NOT NULL,
    to_address text NOT NULL,
    value bigint NOT NULL CHECK (value > 0),                  -- nanoton
    comment text,                                             -- the transfer's comment, at most 256 bytes
    block_ts timestamptz NOT NULL,
    intent_id uuid REFERENCES payment_intents (id),
    outcome text NOT NULL CHECK (outcome IN ('matched','unmatched','resolved','ignored')),
    reason text,
    resolved_by uuid,
    resolved_at timestamptz,
    note text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ton_transfers_once UNIQUE (tx_hash, tx_lt),
    CONSTRAINT ton_transfers_matched CHECK (outcome <> 'matched' OR intent_id IS NOT NULL)
);
-- A transfer settles at most one intent, a transaction (whatever its logical time) settles at most one, and an
-- intent is settled by at most one transfer.
CREATE UNIQUE INDEX ton_transfers_matched_intent_idx ON ton_transfers (intent_id) WHERE outcome = 'matched';
CREATE UNIQUE INDEX ton_transfers_matched_hash_idx ON ton_transfers (tx_hash) WHERE outcome = 'matched';
CREATE INDEX ton_transfers_unmatched_idx ON ton_transfers (block_ts DESC) WHERE outcome = 'unmatched';
CREATE INDEX ton_transfers_block_idx ON ton_transfers (block_ts DESC, id DESC);

-- How far the watcher has read each address, and how its last read went. last_lt is the highest logical time
-- processed (the account's transactions have increasing ones); floor_ts is where a first read stops, so a new
-- address does not report its whole history.
CREATE TABLE ton_cursors (
    address text PRIMARY KEY,
    last_lt bigint NOT NULL DEFAULT 0,
    floor_ts timestamptz NOT NULL,
    scanned_at timestamptz,
    ok_at timestamptz,
    last_error text,
    updated_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_intents WHERE provider = 'ton_auto') THEN
        RAISE EXCEPTION 'cannot go back: there are ton_auto payments';
    END IF;
END $$;
DROP TABLE IF EXISTS ton_cursors;
DROP TABLE IF EXISTS ton_transfers;
DELETE FROM gateway_configs WHERE provider = 'ton_auto';
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_enabled_needs_credentials;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_enabled_needs_credentials CHECK (
    NOT enabled
    OR (provider = 'zarinpal' AND merchant_enc IS NOT NULL)
    OR (provider = 'usdt_trc20_auto' AND address_enc IS NOT NULL)
    OR (provider IN ('idpay','nextpay','aqayepardakht') AND api_key_enc IS NOT NULL));
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_provider_check;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_provider_check
    CHECK (provider IN ('zarinpal','usdt_trc20_auto','idpay','nextpay','aqayepardakht'));
DROP INDEX IF EXISTS payment_intents_ton_user_idx;
DROP INDEX IF EXISTS payment_intents_ton_open_idx;
DROP INDEX IF EXISTS payment_intents_ton_memo_once_idx;
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_ton_fields;
ALTER TABLE payment_intents
    DROP COLUMN IF EXISTS quote_source,
    DROP COLUMN IF EXISTS quote_rate,
    DROP COLUMN IF EXISTS quote_usdt,
    DROP COLUMN IF EXISTS pay_memo;
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','manual_zarinpal','zarinpal','stars','usdt_trc20_auto',
                        'idpay','nextpay','aqayepardakht'));
-- +goose StatementEnd
