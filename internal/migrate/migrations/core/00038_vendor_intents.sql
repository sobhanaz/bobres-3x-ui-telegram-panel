-- +goose Up
-- +goose StatementBegin
-- Buying a license after a top-up, in one flow (docs/04-dashboard.md, Licenses): a customer whose wallet is short taps a payment method for the
-- shortfall; that tap records what they meant to buy, and when the payment lands and the wallet covers it the purchase is made by itself.
-- One intent per customer (a new one replaces the last), good for a few hours, and gone when the customer buys by hand.
CREATE TABLE vendor_intents (
    user_id    uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    tier       text NOT NULL,
    months     int NOT NULL CHECK (months IN (1, 12)),
    license_id uuid REFERENCES vendor_licenses (id) ON DELETE CASCADE, -- a renewal; NULL: a new license
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX vendor_intents_expires_idx ON vendor_intents (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vendor_intents;
-- +goose StatementEnd
