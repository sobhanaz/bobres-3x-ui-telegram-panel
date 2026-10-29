-- Aqayepardakht (آقای‌پرداخت), a fourth Iranian card gateway built like IDPay and NextPay: the customer is sent
-- to the gateway, comes back to our callback, and the payment is settled only after the gateway's own verify
-- call confirms it. Its one secret, the merchant's PIN, is stored encrypted with the payments master key
-- (api_key_enc) like the other keys, with an optional proxy. Nothing else changes: it uses the same columns,
-- the same unique gateway reference and the same reconciler queue.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','manual_zarinpal','zarinpal','stars','usdt_trc20_auto',
                        'idpay','nextpay','aqayepardakht'));

ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_provider_check;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_provider_check
    CHECK (provider IN ('zarinpal','usdt_trc20_auto','idpay','nextpay','aqayepardakht'));
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_enabled_needs_credentials;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_enabled_needs_credentials CHECK (
    NOT enabled
    OR (provider = 'zarinpal' AND merchant_enc IS NOT NULL)
    OR (provider = 'usdt_trc20_auto' AND address_enc IS NOT NULL)
    OR (provider IN ('idpay','nextpay','aqayepardakht') AND api_key_enc IS NOT NULL));

-- The reconciler's work queue now covers it too.
DROP INDEX IF EXISTS payment_intents_gateway_open_idx;
CREATE INDEX payment_intents_gateway_open_idx
    ON payment_intents (checked_at NULLS FIRST)
    WHERE status IN ('pending','confirming') AND provider IN ('zarinpal','idpay','nextpay','aqayepardakht');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_intents WHERE provider = 'aqayepardakht') THEN
        RAISE EXCEPTION 'cannot go back: there are aqayepardakht payments';
    END IF;
END $$;
DROP INDEX IF EXISTS payment_intents_gateway_open_idx;
CREATE INDEX payment_intents_gateway_open_idx
    ON payment_intents (checked_at NULLS FIRST)
    WHERE status IN ('pending','confirming') AND provider IN ('zarinpal','idpay','nextpay');
DELETE FROM gateway_configs WHERE provider = 'aqayepardakht';
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_enabled_needs_credentials;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_enabled_needs_credentials CHECK (
    NOT enabled
    OR (provider = 'zarinpal' AND merchant_enc IS NOT NULL)
    OR (provider = 'usdt_trc20_auto' AND address_enc IS NOT NULL)
    OR (provider IN ('idpay','nextpay') AND api_key_enc IS NOT NULL));
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_provider_check;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_provider_check
    CHECK (provider IN ('zarinpal','usdt_trc20_auto','idpay','nextpay'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','manual_zarinpal','zarinpal','stars','usdt_trc20_auto',
                        'idpay','nextpay'));
-- +goose StatementEnd
