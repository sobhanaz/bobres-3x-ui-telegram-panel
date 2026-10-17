-- P-roadmap 3: the Iranian gateways IDPay and NextPay, built like Zarinpal. A
-- customer is sent to the gateway, comes back to our callback, and the payment
-- is settled only after the gateway's own verify call confirms it. Each has one
-- API key, stored encrypted with the payments master key (api_key_enc), an
-- optional sandbox flag and an optional proxy, like Zarinpal's merchant id.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','manual_zarinpal','zarinpal','stars','usdt_trc20_auto',
                        'idpay','nextpay'));

ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_provider_check;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_provider_check
    CHECK (provider IN ('zarinpal','usdt_trc20_auto','idpay','nextpay'));
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_enabled_needs_credentials;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_enabled_needs_credentials CHECK (
    NOT enabled
    OR (provider = 'zarinpal' AND merchant_enc IS NOT NULL)
    OR (provider = 'usdt_trc20_auto' AND address_enc IS NOT NULL)
    OR (provider IN ('idpay','nextpay') AND api_key_enc IS NOT NULL));

-- The reconciler's work queue now covers every redirect gateway.
DROP INDEX IF EXISTS payment_intents_gateway_open_idx;
CREATE INDEX payment_intents_gateway_open_idx
    ON payment_intents (checked_at NULLS FIRST)
    WHERE status IN ('pending','confirming') AND provider IN ('zarinpal','idpay','nextpay');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM payment_intents WHERE provider IN ('idpay','nextpay')) THEN
        RAISE EXCEPTION 'cannot go back: there are idpay or nextpay payments';
    END IF;
END $$;
DROP INDEX IF EXISTS payment_intents_gateway_open_idx;
CREATE INDEX payment_intents_gateway_open_idx
    ON payment_intents (checked_at NULLS FIRST)
    WHERE status IN ('pending','confirming') AND provider = 'zarinpal';
DELETE FROM gateway_configs WHERE provider IN ('idpay','nextpay');
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_enabled_needs_credentials;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_enabled_needs_credentials CHECK (
    NOT enabled
    OR (provider = 'zarinpal' AND merchant_enc IS NOT NULL)
    OR (provider = 'usdt_trc20_auto' AND address_enc IS NOT NULL));
ALTER TABLE gateway_configs DROP CONSTRAINT IF EXISTS gateway_configs_provider_check;
ALTER TABLE gateway_configs ADD CONSTRAINT gateway_configs_provider_check
    CHECK (provider IN ('zarinpal','usdt_trc20_auto'));
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','manual_zarinpal','zarinpal','stars','usdt_trc20_auto'));
-- +goose StatementEnd
