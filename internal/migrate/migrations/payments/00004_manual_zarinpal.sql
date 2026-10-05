-- manual_zarinpal: the customer pays through the store's Zarinpal payment link and
-- sends the receipt screenshot; an admin approves it like a card-to-card transfer.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','manual_zarinpal','zarinpal','stars'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE payment_intents DROP CONSTRAINT IF EXISTS payment_intents_provider_check;
ALTER TABLE payment_intents ADD CONSTRAINT payment_intents_provider_check
    CHECK (provider IN ('wallet','manual_card','manual_crypto','zarinpal','stars'));
-- +goose StatementEnd
